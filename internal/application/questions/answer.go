package questions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"time"

	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

var ErrAnswerInvalid = errors.New("invalid question answer")

func questionFence(tx app.WriteTx, a domain.ProviderAttempt) (domain.Question, error) {
	current, e := tx.ProviderAttempt(a.ID)
	if e != nil {
		return domain.Question{}, e
	}
	if current.JobID != a.JobID || current.State != "result_saved" {
		return domain.Question{}, domain.ErrStaleWorker
	}
	j, e := tx.Job(a.JobID)
	if e != nil {
		return domain.Question{}, e
	}
	if j.Kind != "question" {
		return domain.Question{}, domain.ErrInvalidTarget
	}
	q, e := tx.QuestionByJob(j.ID)
	if e != nil {
		return q, e
	}
	if j.State == "queued" {
		e = app.GuardProviderContinuation(tx, current)
	} else {
		e = app.GuardProvider(tx, current)
	}
	return q, e
}

// Prepare consumes the complete saved public final outside the writer. Saved
// continuation reuses its immutable prepared answer; it grants no provider turn.
func (s *Service) Prepare(ctx context.Context, attempt domain.ProviderAttempt) error {
	var a domain.ProviderAttempt
	var object domain.Object
	prepared := false
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(attempt.JobID)
		if e != nil || j.Kind != "question" {
			return e
		}
		a, e = tx.ProviderAttempt(attempt.ID)
		if e != nil {
			return e
		}
		q, e := tx.QuestionByJob(j.ID)
		if e != nil {
			return e
		}
		if q.PreparedAnswer != nil {
			if q.PreparedAnswer.AttemptID != a.ID {
				return domain.ErrConflict
			}
			prepared = true
			return nil
		}
		if _, e = questionFence(tx, a); e != nil {
			return e
		}
		object, e = tx.Object(a.ResultHash)
		return e
	})
	if e != nil || a.ID == "" || prepared {
		return e
	}
	var output domain.AnswerEnvelope
	e = s.Bytes.Read(ctx, object, domain.Pin{Kind: "provider_result", OwnerID: a.ID}, func(r io.ReadSeeker) error {
		var err error
		output, err = decodeAnswer(&answerReader{Reader: r, ctx: ctx, service: s, attempt: a})
		return err
	})
	if e != nil {
		return s.invalidAnswer(ctx, a, e)
	}
	if (output.State != "final" && output.State != "partial") || strings.TrimSpace(output.Body) == "" {
		return s.invalidAnswer(ctx, a, ErrAnswerInvalid)
	}
	// Validate before creating the output object, then repeat at preparation and
	// application so policy/lifecycle changes cannot race stored completion.
	e = s.Store.Write(ctx, func(tx app.WriteTx) error {
		q, e := questionFence(tx, a)
		if e != nil {
			return e
		}
		return validateAnswer(tx, q, a, output)
	})
	if e != nil {
		if errors.Is(e, ErrAnswerInvalid) {
			return s.invalidAnswer(ctx, a, e)
		}
		return e
	}
	id, e := service.NewID()
	if e != nil {
		return e
	}
	_, e = s.Bytes.Put(ctx, &answerReader{Reader: strings.NewReader(output.Body), ctx: ctx, service: s, attempt: a}, func(tx app.WriteTx, object domain.Object) error {
		q, e := questionFence(tx, a)
		if e != nil {
			return e
		}
		if e = validateAnswer(tx, q, a, output); e != nil {
			return e
		}
		limitations := append([]domain.QuestionLimitation{}, q.Limitations...)
		for _, limit := range output.Limitations {
			if !containsLimitation(limitations, limit) {
				limitations = append(limitations, limit)
			}
		}
		answer := domain.QuestionAnswer{ID: id, QuestionID: q.ID, AttemptID: a.ID, State: output.State, CreatedAt: s.Now().UTC().Format(time.RFC3339Nano), Body: domain.ProviderText{Hash: object.Hash, Size: object.Size}, Citations: output.Citations, ExplanationReferences: output.ExplanationReferences, Limitations: limitations}
		return tx.PrepareQuestionAnswer(q.ID, answer)
	})
	return e
}

func containsLimitation(values []domain.QuestionLimitation, value domain.QuestionLimitation) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// Apply runs in the coordinator's existing guarded result transaction. The
// prepared candidate remains owned after any refusal for explicit continuation.
func (s *Service) Apply(tx app.WriteTx, attempt domain.ProviderAttempt) error {
	j, e := tx.Job(attempt.JobID)
	if e != nil || j.Kind != "question" {
		return e
	}
	a, e := tx.ProviderAttempt(attempt.ID)
	if e != nil {
		return e
	}
	if e = app.GuardProvider(tx, a); e != nil {
		return e
	}
	q, e := tx.QuestionByJob(j.ID)
	if e != nil {
		return e
	}
	answer := q.PreparedAnswer
	if answer == nil || answer.AttemptID != a.ID {
		return domain.ErrReconciliation
	}
	if e = validateAnswer(tx, q, a, domain.AnswerEnvelope{State: answer.State, Citations: answer.Citations, ExplanationReferences: answer.ExplanationReferences, Limitations: answer.Limitations}); e != nil {
		return e
	}
	if e = tx.PutQuestionAnswer(*answer); e != nil {
		return e
	}
	if e = tx.SetJobResult(j.ID, encode(map[string]string{"question_id": q.ID, "answer_id": answer.ID, "answer_state": answer.State, "attempt_id": a.ID})); e != nil {
		return e
	}
	return tx.ReleasePin("question-answer-prepare:" + q.ID)
}

// invalidAnswer retains a safe error history while leaving the saved result
// unresolved. Malformed output never becomes a completed or fabricated answer.
func (s *Service) invalidAnswer(ctx context.Context, a domain.ProviderAttempt, cause error) error {
	// Transport/storage and authority errors are not authored-output failures.
	if !errors.Is(cause, ErrAnswerInvalid) && !errors.Is(cause, sec.ErrInvalid) {
		return cause
	}
	var existing bool
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		_, e := questionFence(tx, a)
		if e != nil {
			return e
		}
		_, e = tx.QuestionAnswerForAttempt(a.ID, "error")
		if e == nil {
			existing = true
			return nil
		}
		if errors.Is(e, domain.ErrNotFound) {
			return nil
		}
		return e
	})
	if e != nil {
		return e
	}
	if !existing {
		id, e := service.NewID()
		if e != nil {
			return e
		}
		_, e = s.Bytes.Put(ctx, bytes.NewBufferString("The saved provider output could not be validated as a grounded answer."), func(tx app.WriteTx, object domain.Object) error {
			q, e := questionFence(tx, a)
			if e != nil {
				return e
			}
			if _, e = tx.QuestionAnswerForAttempt(a.ID, "error"); e == nil {
				return nil
			} else if !errors.Is(e, domain.ErrNotFound) {
				return e
			}
			return tx.PutQuestionAnswer(domain.QuestionAnswer{ID: id, QuestionID: q.ID, AttemptID: a.ID, State: "error", CreatedAt: s.Now().UTC().Format(time.RFC3339Nano), Body: domain.ProviderText{Hash: object.Hash, Size: object.Size}, Citations: []domain.QuestionCitation{}, ExplanationReferences: []domain.ExplanationReference{}, Limitations: []domain.QuestionLimitation{{Code: "invalid_answer", Message: "Saved output validation failed; no source-support claim was accepted."}}})
		})
		if e != nil {
			return e
		}
	}
	return ErrAnswerInvalid
}

type answerReader struct {
	io.Reader
	ctx     context.Context
	service *Service
	attempt domain.ProviderAttempt
}

func (r *answerReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	if e := r.service.Store.Write(r.ctx, func(tx app.WriteTx) error { _, e := questionFence(tx, r.attempt); return e }); e != nil {
		return 0, e
	}
	return r.Reader.Read(p)
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// Source wrappers retain their original metadata. Only the canonical immutable
// identity is compared across managed capture and independent verification;
// labels, local IDs and retention metadata cannot change that identity.
func sameSourceIdentity(a, b []byte) bool {
	type identity struct {
		Kind     string `json:"kind"`
		Repo     string `json:"repo_id"`
		Format   string `json:"object_format"`
		Revision string `json:"exact_revision"`
		Snapshot string `json:"snapshot_sha256"`
	}
	var x, y identity
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil || x.Kind != y.Kind {
		return false
	}
	switch x.Kind {
	case "git":
		return x.Repo != "" && x.Format != "" && x.Revision != "" && x.Repo == y.Repo && x.Format == y.Format && x.Revision == y.Revision
	case "snapshot":
		return x.Snapshot != "" && x.Snapshot == y.Snapshot
	default:
		return false
	}
}

func validateAnswer(tx app.WriteTx, q domain.Question, a domain.ProviderAttempt, output domain.AnswerEnvelope) error {
	if output.State != "final" && output.State != "partial" {
		return ErrAnswerInvalid
	}
	inputs, e := tx.ProviderInputs(a.ID)
	if e != nil {
		return e
	}
	byID := map[string]domain.ProviderInput{}
	for _, input := range inputs {
		byID[input.ReadID] = input
	}
	for _, c := range output.Citations {
		var read domain.CapturedRead
		switch c.Basis {
		case "observed_input":
			if c.ReadID == nil || c.VerificationID != nil || c.InputOrigin == nil {
				return ErrAnswerInvalid
			}
			input, ok := byID[*c.ReadID]
			if !ok || input.AttemptID != a.ID || input.Hash != c.Hash || input.ObservedAt == nil || *input.ObservedAt == "" || input.ObservedOrigin != *c.InputOrigin || (input.ObservedOrigin != "daemon_supplied" && input.ObservedOrigin != "native_observed") || c.TargetSetID != a.TargetSetID {
				return ErrAnswerInvalid
			}
			read, e = tx.CapturedRead(input.ReadID)
			if e != nil {
				return e
			}
		case "posthoc_verified":
			if c.ReadID != nil || c.InputOrigin != nil || c.VerificationID == nil {
				return ErrAnswerInvalid
			}
			v, err := tx.Verification(*c.VerificationID)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return ErrAnswerInvalid
				}
				return err
			}
			if v.PrincipalID != q.OwnerID || v.VersionID != q.VersionID || v.VerifiedAt == "" || (v.Outcome != "captured" && v.Outcome != "matched") || v.TargetSetID != c.TargetSetID || v.BytesSHA != c.Hash || !sameSourceIdentity(v.SourceJSON, c.Source) || !jsonEqual(v.LocationJSON, c.Location) {
				return ErrAnswerInvalid
			}
			read, e = tx.CapturedRead(v.ReadID)
			if e != nil {
				return e
			}
			// A later check may corroborate only this accepted selection. It cannot
			// authorize a broader read or invent provider observation.
			selected := false
			for _, input := range inputs {
				r, err := tx.CapturedRead(input.ReadID)
				if err != nil {
					return err
				}
				if r.BytesSHA == c.Hash && sameSourceIdentity(r.SourceJSON, c.Source) && jsonEqual(r.LocationJSON, c.Location) {
					selected = true
					break
				}
			}
			if !selected {
				return ErrAnswerInvalid
			}
		default:
			return ErrAnswerInvalid
		}
		if read.TargetSetID != c.TargetSetID || read.BytesSHA != c.Hash || !sameSourceIdentity(read.SourceJSON, c.Source) || !jsonEqual(read.LocationJSON, c.Location) {
			return ErrAnswerInvalid
		}
		object, err := tx.Object(c.Hash)
		if err != nil {
			return err
		}
		if object.State != "ready" {
			return domain.ErrReconciliation
		}
		if c.PackageEvidenceRef != nil {
			if c.PackageEvidenceRef.VersionID != q.VersionID {
				return ErrAnswerInvalid
			}
			checks, err := tx.Verifications(q.VersionID, c.PackageEvidenceRef.EvidenceID, "", "", 64)
			if err != nil {
				return err
			}
			matched := false
			// The package binding must be the same checked immutable evidence,
			// not an arbitrary author's ID attached to a different target.
			for len(checks) > 0 {
				for _, v := range checks {
					if v.BytesSHA == c.Hash && v.VerifiedAt != "" && (v.Outcome == "captured" || v.Outcome == "matched") && sameSourceIdentity(v.SourceJSON, c.Source) && jsonEqual(v.LocationJSON, c.Location) {
						matched = true
					}
				}
				last := checks[len(checks)-1]
				checks, err = tx.Verifications(q.VersionID, c.PackageEvidenceRef.EvidenceID, last.CheckedAt, last.ID, 64)
				if err != nil {
					return err
				}
			}
			if !matched {
				return ErrAnswerInvalid
			}
		}
	}
	for _, ref := range output.ExplanationReferences {
		if !reflect.DeepEqual(ref, q.Explanation.ExplanationReference) {
			return ErrAnswerInvalid
		}
	}
	for _, limitation := range output.Limitations {
		if strings.TrimSpace(limitation.Code) == "" || strings.TrimSpace(limitation.Message) == "" {
			return ErrAnswerInvalid
		}
	}
	return nil
}

// ValidateSourceCitations applies the shared citation union to another read-only
// consumer using the same exact attempt and immutable version authority.
func ValidateSourceCitations(tx app.WriteTx, owner, version string, a domain.ProviderAttempt, citations []domain.QuestionCitation) error {
	return validateAnswer(tx, domain.Question{OwnerID: owner, VersionID: version}, a, domain.AnswerEnvelope{State: "partial", Citations: citations})
}
