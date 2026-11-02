package questions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

type ContextView struct {
	ID               string  `json:"context_ref"`
	Hash             string  `json:"bytes_sha256"`
	Size             string  `json:"byte_size"`
	MediaType        string  `json:"media_type"`
	RenderInstanceID *string `json:"render_instance_id"`
}

type QuestionView struct {
	ID                 string                      `json:"question_id"`
	ProjectID          string                      `json:"project_id"`
	DocumentID         string                      `json:"document_id"`
	VersionID          string                      `json:"version_id"`
	JobID              string                      `json:"job_id"`
	JobState           string                      `json:"job_state"`
	JobError           *string                     `json:"job_error"`
	LatestAnswerID     *string                     `json:"latest_answer_id"`
	OperationID        string                      `json:"operation_id"`
	CreatedAt          string                      `json:"created_at"`
	PagePath           *string                     `json:"page_path"`
	RegionID           *string                     `json:"region_id"`
	PageQuery          string                      `json:"page_query"`
	PageFragment       string                      `json:"page_fragment"`
	BodyRef            domain.BodyReference        `json:"body_ref"`
	TextURL            string                      `json:"text_url"`
	Context            *ContextView                `json:"display_context"`
	ContextRef         *string                     `json:"context_ref"`
	Explanation        domain.ExplanationReference `json:"explanation_reference"`
	SourceMode         string                      `json:"source_mode"`
	Limitations        []domain.QuestionLimitation `json:"limitations"`
	TargetSetID        *string                     `json:"target_set_id"`
	Targets            json.RawMessage             `json:"targets"`
	RequestedSelection json.RawMessage             `json:"requested_selection"`
	Answers            *Page[AnswerView]           `json:"answers,omitempty"`
}
type CitationView struct {
	domain.QuestionCitation
	Availability string  `json:"availability"`
	Reason       *string `json:"reason"`
	ObservedAt   *string `json:"observed_at"`
	VerifiedAt   *string `json:"verified_at"`
	BytesOrigin  *string `json:"bytes_origin"`
	selectedRead string
}
type AnswerView struct {
	ID                    string                        `json:"answer_id"`
	QuestionID            string                        `json:"question_id"`
	AttemptID             string                        `json:"attempt_id"`
	State                 string                        `json:"state"`
	CreatedAt             string                        `json:"created_at"`
	BodyRef               domain.BodyReference          `json:"body_ref"`
	BodyURL               string                        `json:"body_url"`
	Citations             []CitationView                `json:"citations"`
	ExplanationReferences []domain.ExplanationReference `json:"explanation_references"`
	Limitations           []domain.QuestionLimitation   `json:"limitations"`
}
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}
type cursor struct {
	Binding string `json:"binding"`
	Created string `json:"created"`
	ID      string `json:"id"`
}

func savedBody(id string, text domain.ProviderText) domain.BodyReference {
	return domain.BodyReference{ID: id, Hash: text.Hash, Size: strconv.FormatInt(text.Size, 10), MediaType: "text/plain"}
}
func view(tx app.WriteTx, q domain.Question) (QuestionView, error) {
	out := QuestionView{ID: q.ID, ProjectID: q.ProjectID, DocumentID: q.DocumentID, VersionID: q.VersionID, JobID: q.JobID, OperationID: q.OperationID, CreatedAt: q.CreatedAt, PagePath: q.PagePath, RegionID: q.RegionID, PageQuery: q.PageQuery, PageFragment: q.PageFragment, BodyRef: savedBody(q.ID, q.Text), TextURL: "/api/v1/questions/" + q.ID + "/text", Explanation: q.Explanation.ExplanationReference, SourceMode: q.SourceMode, Limitations: q.Limitations, RequestedSelection: q.SourceSelection, Targets: json.RawMessage(`[]`)}
	j, e := tx.Job(q.JobID)
	if e != nil {
		return out, e
	}
	out.JobState = j.State
	if j.Error != "" {
		out.JobError = &j.Error
	}
	latest, e := tx.LatestCompletedAnswer(q.ID)
	if e != nil {
		return out, e
	}
	if latest != nil {
		out.LatestAnswerID = &latest.ID
	}
	var scope domain.ProviderScope
	if json.Unmarshal(j.Scope, &scope) != nil {
		return out, domain.ErrContentIntegrity
	}
	if scope.TargetSetID != "" {
		target, e := tx.TargetSetByID(scope.TargetSetID)
		if e != nil {
			return out, e
		}
		if target.ProjectID != q.ProjectID {
			return out, domain.ErrContentIntegrity
		}
		out.TargetSetID = &target.ID
		out.Targets = target.TargetsJSON
	}
	if q.Context != nil {
		out.ContextRef = &q.Context.ID
		out.Context = &ContextView{ID: q.Context.ID, Hash: q.Context.Hash, Size: strconv.FormatInt(q.Context.Size, 10), MediaType: q.Context.MediaType, RenderInstanceID: q.Context.RenderInstanceID}
	}
	return out, nil
}

// Authenticate the owner before saved record lookup, including each stream
// chunk, so a scoped principal cannot distinguish existing from missing IDs.
func (s *Service) readActor(ctx context.Context, tx app.WriteTx, token string) (domain.Session, error) {
	actor, e := s.Auth.AuthenticateWrite(ctx, tx, token)
	if e != nil {
		return actor, e
	}
	if actor.Scope.Kind != "owner" {
		return actor, domain.ErrDenied
	}
	return actor, nil
}
func (s *Service) readable(tx app.WriteTx, actor domain.Session, q domain.Question) error {
	if e := app.AuthorizeTarget(tx, textTarget(q.OperationID, q.VersionID, q.PagePath)); e != nil {
		return e
	}
	if actor.PrincipalID != q.OwnerID {
		return domain.ErrNotFound
	}
	return nil
}
func (s *Service) Question(ctx context.Context, token, id string) (out QuestionView, err error) {
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		actor, e := s.readActor(ctx, tx, token)
		if e != nil {
			return e
		}
		q, e := tx.Question(id)
		if e != nil {
			return e
		}
		if e = s.readable(tx, actor, q); e != nil {
			return e
		}
		out, e = view(tx, q)
		if e != nil {
			return e
		}
		answers, e := s.answerPage(ctx, tx, token, q, "")
		if e != nil {
			return e
		}
		out.Answers = &answers
		return nil
	})
	return
}
func (s *Service) pageCursor(ctx context.Context, tx app.WriteTx, token, version, resource, input string) (cursor, domain.Session, error) {
	actor, e := s.owner(ctx, tx, token, "", version, nil)
	if e != nil {
		return cursor{}, actor, e
	}
	l, e := tx.Library()
	if e != nil {
		return cursor{}, actor, e
	}
	v, e := tx.PublishedVersion(version)
	if e != nil {
		return cursor{}, actor, e
	}
	d, e := tx.QueryDocument(v.DocumentID)
	if e != nil {
		return cursor{}, actor, e
	}
	p, e := tx.Scope(d.ProjectID)
	if e != nil {
		return cursor{}, actor, e
	}
	h := sha256.Sum256(encode([]any{l.ID, l.RuntimeGeneration, actor.PrincipalID, actor.AuthEpoch, version, resource, p.Project.PermissionsRevision}))
	binding := hex.EncodeToString(h[:])
	c := cursor{Binding: binding}
	if input != "" {
		raw, e := base64.RawURLEncoding.DecodeString(input)
		if e != nil {
			return c, actor, sec.ErrInvalid
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&c) != nil || c.Binding != binding || base64.RawURLEncoding.EncodeToString(encode(c)) != input {
			return c, actor, sec.ErrInvalid
		}
		if (c.ID == "") != (c.Created == "") {
			return c, actor, sec.ErrInvalid
		}
	}
	return c, actor, nil
}
func next(c cursor) *string { v := base64.RawURLEncoding.EncodeToString(encode(c)); return &v }
func (s *Service) Questions(ctx context.Context, token, version, input string) (out Page[QuestionView], err error) {
	out.Items = []QuestionView{}
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		c, actor, e := s.pageCursor(ctx, tx, token, version, "questions", input)
		if e != nil {
			return e
		}
		rows, e := tx.Questions(actor.PrincipalID, version, c.Created, c.ID, 64)
		if e != nil {
			return e
		}
		for _, q := range rows {
			v, e := view(tx, q)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, v)
		}
		if len(rows) == 64 {
			last := rows[len(rows)-1]
			c.Created, c.ID = last.CreatedAt, last.ID
			out.NextCursor = next(c)
		}
		return nil
	})
	return
}
func (s *Service) Answers(ctx context.Context, token, id, input string) (out Page[AnswerView], err error) {
	out.Items = []AnswerView{}
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		actor, e := s.readActor(ctx, tx, token)
		if e != nil {
			return e
		}
		q, e := tx.Question(id)
		if e != nil {
			return e
		}
		if e = s.readable(tx, actor, q); e != nil {
			return e
		}
		out, e = s.answerPage(ctx, tx, token, q, input)
		return e
	})
	return
}
func (s *Service) answerPage(ctx context.Context, tx app.WriteTx, token string, q domain.Question, input string) (out Page[AnswerView], err error) {
	out.Items = []AnswerView{}
	c, actor, e := s.pageCursor(ctx, tx, token, q.VersionID, "answers:"+q.ID, input)
	if e != nil {
		return out, e
	}
	rows, e := tx.QuestionAnswers(q.ID, c.Created, c.ID, 64)
	if e != nil {
		return out, e
	}
	for _, a := range rows {
		v, e := s.answerView(tx, q, a, actor)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	if len(rows) == 64 {
		last := rows[len(rows)-1]
		c.Created, c.ID = last.CreatedAt, last.ID
		out.NextCursor = next(c)
	}
	return out, nil
}
func (s *Service) Answer(ctx context.Context, token, id string) (out AnswerView, err error) {
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		actor, e := s.readActor(ctx, tx, token)
		if e != nil {
			return e
		}
		a, e := tx.QuestionAnswer(id)
		if e != nil {
			return e
		}
		q, e := tx.Question(a.QuestionID)
		if e != nil {
			return e
		}
		if e = s.readable(tx, actor, q); e != nil {
			return e
		}
		out, e = s.answerView(tx, q, a, actor)
		return e
	})
	return
}
func (s *Service) answerView(tx app.WriteTx, q domain.Question, a domain.QuestionAnswer, actor domain.Session) (AnswerView, error) {
	out := AnswerView{ID: a.ID, QuestionID: a.QuestionID, AttemptID: a.AttemptID, State: a.State, CreatedAt: a.CreatedAt, BodyRef: savedBody(a.ID, a.Body), BodyURL: "/api/v1/answers/" + a.ID + "/body", Citations: []CitationView{}, ExplanationReferences: a.ExplanationReferences, Limitations: a.Limitations}
	attempt, e := tx.ProviderAttempt(a.AttemptID)
	if e != nil {
		return out, e
	}
	inputs, e := tx.ProviderInputs(attempt.ID)
	if e != nil {
		return out, e
	}
	for _, c := range a.Citations {
		v := CitationView{QuestionCitation: c, Availability: "unavailable"}
		selectedRead := ""
		if c.ReadID != nil {
			for _, input := range inputs {
				if input.ReadID == *c.ReadID {
					v.ObservedAt = input.ObservedAt
				}
			}
			selectedRead = *c.ReadID
		} else if c.VerificationID != nil {
			verification, e := tx.Verification(*c.VerificationID)
			if e != nil {
				return out, e
			}
			if verification.VerifiedAt != "" {
				value := verification.VerifiedAt
				v.VerifiedAt = &value
			}
			// Corroboration inherits the accepted selection's current byte guards.
			for _, input := range inputs {
				r, e := tx.CapturedRead(input.ReadID)
				if e != nil {
					return out, e
				}
				if r.BytesSHA == c.Hash && sameSourceIdentity(r.SourceJSON, c.Source) && jsonEqual(r.LocationJSON, c.Location) {
					selectedRead = input.ReadID
					break
				}
			}
		}
		if selectedRead == "" {
			return out, domain.ErrContentIntegrity
		}
		availability, origin, reason, e := citationAvailability(tx, actor, q.ProjectID, selectedRead, c.Hash)
		if e != nil {
			return out, e
		}
		v.Availability, v.BytesOrigin, v.Reason = availability, origin, reason
		v.selectedRead = selectedRead
		out.Citations = append(out.Citations, v)
	}
	return out, nil
}

// ReadCitation binds a saved citation to its accepted retained read. The source
// service owns byte verification and current source guards. Reference-only
// rereads are deliberately unavailable here: inspecting an answer never collects
// source again or substitutes a newer revision for the accepted bytes.
func (s *Service) ReadCitation(ctx context.Context, token, answer string, index int, consume func(domain.SourceCaptureRead, io.Reader) error) error {
	var selected CitationView
	check := func(tx app.WriteTx) error {
		actor, e := s.readActor(ctx, tx, token)
		if e != nil {
			return e
		}
		a, e := tx.QuestionAnswer(answer)
		if e != nil {
			return e
		}
		q, e := tx.Question(a.QuestionID)
		if e != nil {
			return e
		}
		if e = s.readable(tx, actor, q); e != nil {
			return e
		}
		v, e := s.answerView(tx, q, a, actor)
		if e != nil {
			return e
		}
		if index < 0 || index >= len(v.Citations) {
			return domain.ErrNotFound
		}
		c := v.Citations[index]
		if c.Availability == "denied" {
			return domain.ErrDenied
		}
		if c.Availability != "available" || c.BytesOrigin == nil || *c.BytesOrigin != "retained_snapshot" {
			return domain.ErrNotFound
		}
		if selected.selectedRead != "" && (selected.selectedRead != c.selectedRead || selected.Hash != c.Hash) {
			return domain.ErrContentIntegrity
		}
		selected = c
		return nil
	}
	if e := s.Store.Write(ctx, check); e != nil {
		return e
	}
	return s.Sources.ReadCaptured(ctx, token, selected.selectedRead, func(row domain.SourceCaptureRead, reader io.ReadSeeker) error {
		if row.RetentionMode != "selected_snapshot" || row.Hash != selected.Hash || !sameSourceIdentity(row.SourceJSON, selected.Source) || !jsonEqual(row.LocationJSON, selected.Location) {
			return domain.ErrContentIntegrity
		}
		if e := s.Store.Write(ctx, check); e != nil {
			return e
		}
		return consume(row, &authorizedReader{Reader: reader, ctx: ctx, store: s.Store, check: check})
	})
}

func citationAvailability(tx app.WriteTx, actor domain.Session, project, id, hash string) (string, *string, *string, error) {
	missing := func(availability, reason string) (string, *string, *string, error) {
		return availability, nil, &reason, nil
	}
	// Saved availability is a current owner read, not another outbound provider
	// disclosure or a continuation under the original attempt's epoch.
	if e := app.CheckSourceReadAuthority(tx, actor, id, project, ""); e != nil {
		if errors.Is(e, domain.ErrDenied) {
			return missing("denied", "source_denied")
		}
		if errors.Is(e, domain.ErrNotFound) {
			return missing("unavailable", "source_context_unavailable")
		}
		if errors.Is(e, domain.ErrReconciliation) {
			return missing("unavailable", "source_access_unavailable")
		}
		return "", nil, nil, e
	}
	r, e := tx.SourceCaptureRead(id)
	if e != nil {
		return "", nil, nil, e
	}
	if r.RetentionMode == "reference_only" {
		return missing("unavailable", "not_retained")
	}
	object, e := tx.Object(hash)
	if errors.Is(e, domain.ErrNotFound) {
		return missing("unavailable", "bytes_unavailable")
	}
	if e != nil {
		return "", nil, nil, e
	}
	if object.State != "ready" {
		return missing("unavailable", "bytes_unavailable")
	}
	origin := "retained_snapshot"
	return "available", &origin, nil, nil
}

// ReadText streams only the question/answer's independently owned bytes, with
// current owner/version authorization at every chunk. It never dispatches work.
func (s *Service) ReadText(ctx context.Context, token, id string, answer bool, consume func(domain.Object, io.Reader) error) error {
	var object domain.Object
	var q domain.Question
	var expected domain.ProviderText
	check := func(tx app.WriteTx) error {
		actor, e := s.readActor(ctx, tx, token)
		if e != nil {
			return e
		}
		if answer {
			a, err := tx.QuestionAnswer(id)
			if err != nil {
				return err
			}
			expected = a.Body
			q, e = tx.Question(a.QuestionID)
		} else {
			q, e = tx.Question(id)
			expected = q.Text
		}
		if e != nil {
			return e
		}
		if e = s.readable(tx, actor, q); e != nil {
			return e
		}
		object, e = tx.Object(expected.Hash)
		if e != nil {
			return e
		}
		if object.State != "ready" || object.Size != expected.Size {
			return domain.ErrReconciliation
		}
		return nil
	}
	if e := s.Store.Write(ctx, check); e != nil {
		return e
	}
	return s.Bytes.Read(ctx, object, domain.Pin{Kind: "question_read", OwnerID: q.ID}, func(r io.ReadSeeker) error {
		return consume(object, &authorizedReader{Reader: r, ctx: ctx, store: s.Store, check: check})
	})
}

func (s *Service) ReadContext(ctx context.Context, token, id string, consume func(domain.QuestionContext, domain.Object, io.Reader) error) error {
	var q domain.Question
	var object domain.Object
	check := func(tx app.WriteTx) error {
		actor, e := s.readActor(ctx, tx, token)
		if e != nil {
			return e
		}
		q, e = tx.Question(id)
		if e != nil {
			return e
		}
		if e = s.readable(tx, actor, q); e != nil {
			return e
		}
		if q.Context == nil {
			return domain.ErrNotFound
		}
		current, e := tx.Context(q.Context.ID)
		if e != nil {
			return e
		}
		if current.PrincipalID != q.OwnerID || current.VersionID != q.VersionID || !reflect.DeepEqual(current.PagePath, q.PagePath) || current.Object.Hash != q.Context.Hash || current.Object.Size != q.Context.Size || current.MediaType != q.Context.MediaType {
			return domain.ErrContentIntegrity
		}
		object, e = tx.Object(q.Context.Hash)
		if e != nil {
			return e
		}
		if object.State != "ready" || object.Size != q.Context.Size {
			return domain.ErrReconciliation
		}
		return nil
	}
	if e := s.Store.Write(ctx, check); e != nil {
		return e
	}
	return s.Bytes.Read(ctx, object, domain.Pin{Kind: "question_read", OwnerID: q.ID}, func(r io.ReadSeeker) error {
		return consume(*q.Context, object, &authorizedReader{Reader: r, ctx: ctx, store: s.Store, check: check})
	})
}

type authorizedReader struct {
	io.Reader
	ctx   context.Context
	store app.Store
	check func(app.WriteTx) error
}

func (r *authorizedReader) Read(p []byte) (int, error) {
	if e := r.store.Write(r.ctx, r.check); e != nil {
		return 0, e
	}
	return r.Reader.Read(p)
}
