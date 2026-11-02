// Package questions admits owner Ask requests onto the existing provider job
// dispatcher. Saved question/answer ownership is independent of a Reader frame.
package questions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

type Identity func(string, json.RawMessage, domain.ProviderInvocation, []domain.CapturedRead) (string, error)

type Service struct {
	Store    app.Store
	Bytes    source.CaptureBytes
	Auth     *auth.Service
	Sources  *source.Service
	Identity Identity
	Now      func() time.Time
}

func New(store app.Store, a *auth.Service, sources *source.Service, identity Identity) *Service {
	bytes, _ := store.(source.CaptureBytes)
	return &Service{Store: store, Bytes: bytes, Auth: a, Sources: sources, Identity: identity, Now: time.Now}
}

type Request struct {
	Question       string                `json:"question,omitempty"`
	BodyRef        *domain.BodyReference `json:"body_ref,omitempty"`
	PagePath       *string               `json:"page_path"`
	RegionID       *string               `json:"region_id"`
	PageQuery      string                `json:"page_query,omitempty"`
	PageFragment   string                `json:"page_fragment,omitempty"`
	SourceMode     string                `json:"source_mode,omitempty"`
	ContextRef     *string               `json:"context_ref,omitempty"`
	DisplayState   json.RawMessage       `json:"display_state,omitempty"`
	ProviderID     string                `json:"provider_id,omitempty"`
	Model          string                `json:"model,omitempty"`
	Effort         string                `json:"effort,omitempty"`
	Sources        source.CaptureRequest `json:"source_selection"`
	SourcesPresent bool                  `json:"-"`
}

type normalization struct {
	Refusal             *AdmissionRefusal `json:"admission_refusal,omitempty"`
	Question            domain.Question
	Scope               domain.ProviderScope
	NewContext          *domain.ContextRecord
	ConfigRevision      int64
	PermissionsRevision int64
}

// AdmissionRefusal is a terminal failed operation that never created a Question
// or job. The existing receipt fences even an earlier same-key request still
// preparing outside the admission writer. General permission failures and
// failures after admission cannot create this proof.
type AdmissionRefusal struct {
	OperationID       string `json:"operation_id"`
	RuntimeGeneration string `json:"runtime_generation"`
	PrincipalID       string `json:"principal_id"`
}

func (e *AdmissionRefusal) Error() string { return domain.ErrDenied.Error() }
func (e *AdmissionRefusal) Unwrap() error { return domain.ErrDenied }

func sourceRefusal(tx app.WriteTx, actor domain.Session, key string, cause error) ([]byte, error) {
	if !errors.Is(cause, domain.ErrDenied) {
		return nil, cause
	}
	library, e := tx.Library()
	if e != nil {
		return nil, e
	}
	return encode(normalization{Refusal: &AdmissionRefusal{OperationID: key, RuntimeGeneration: library.RuntimeGeneration, PrincipalID: actor.PrincipalID}}), nil
}

func refusalResponse(refusal AdmissionRefusal) domain.Response {
	return domain.Response{Status: 403, Body: encode(map[string]any{"error": map[string]any{"code": "FORBIDDEN", "message": "Forbidden", "retryable": false, "details": map[string]string{"question_admission": "refused_before_admission", "operation_id": refusal.OperationID, "runtime_generation": refusal.RuntimeGeneration, "principal_id": refusal.PrincipalID}}})}
}

// Project the committed failure through the normal typed HTTP error path. The
// original operation retains the same error metadata for lost-response reads.
func admissionResponse(response domain.Response, err error) (domain.Response, error) {
	if err != nil || response.Status != 403 {
		return response, err
	}
	var body struct {
		Error struct {
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(response.Body, &body) != nil || body.Error.Code != "FORBIDDEN" || body.Error.Details["question_admission"] != "refused_before_admission" {
		return response, domain.ErrContentIntegrity
	}
	proof := &AdmissionRefusal{OperationID: body.Error.Details["operation_id"], RuntimeGeneration: body.Error.Details["runtime_generation"], PrincipalID: body.Error.Details["principal_id"]}
	if proof.OperationID == "" || proof.RuntimeGeneration == "" || proof.PrincipalID == "" {
		return response, domain.ErrContentIntegrity
	}
	return response, proof
}

func encode(value any) []byte { b, _ := json.Marshal(value); return b }

func (s *Service) Start(ctx context.Context, token, key, version string, input Request) (domain.Response, error) {
	actor, e := s.Auth.Authenticate(ctx, token)
	if e != nil {
		return domain.Response{}, e
	}
	if actor.Scope.Kind != "owner" {
		return domain.Response{}, domain.ErrDenied
	}
	if s.Identity == nil || input.RegionID != nil && (input.PagePath == nil || *input.RegionID == "") || input.ContextRef != nil && input.DisplayState != nil {
		return domain.Response{}, sec.ErrInvalid
	}
	if _, e = app.NormalizeTarget("body", textTarget(key, version, input.PagePath)); e != nil {
		return domain.Response{}, sec.ErrInvalid
	}
	text, e := s.textIdentity(ctx, token, key, version, input)
	if e != nil {
		return domain.Response{}, e
	}
	contextInput, e := s.contextIdentity(ctx, token, version, input)
	if e != nil {
		return domain.Response{}, e
	}
	// Semantic identity uses complete retained bytes and the exact anchor. A
	// transport handle, renderer or later configured default cannot replace it.
	body := map[string]any{"question": text, "version_id": version, "page_path": input.PagePath, "region_id": input.RegionID, "page_query": input.PageQuery, "page_fragment": input.PageFragment, "source_mode": input.SourceMode, "provider_id": input.ProviderID, "model": input.Model, "effort": input.Effort, "source_selection": input.Sources, "source_selection_present": input.SourcesPresent}
	if contextInput != nil {
		body["context"] = map[string]any{"bytes_sha256": contextInput.Hash, "byte_size": contextInput.Size, "media_type": contextInput.MediaType, "render_instance_id": contextInput.RenderInstanceID}
	}
	request := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: "POST", Target: "/api/v1/versions/" + version + "/questions", Body: encode(body)}
	if response, found, e := app.Replay(ctx, s.Store, s.Auth.Authority(token), request); e != nil || found {
		return admissionResponse(response, e)
	}
	declaredIntent, e := s.declaredSourceIntent(ctx, token, key, version, input.PagePath)
	if e != nil {
		return domain.Response{}, e
	}
	text, releaseText, e := s.prepareText(ctx, token, key, version, input, text)
	defer releaseText()
	if e != nil {
		return domain.Response{}, e
	}
	contextInput, releaseContext, e := s.prepareContext(ctx, token, key, version, input, contextInput)
	defer releaseContext()
	if e != nil {
		return domain.Response{}, e
	}
	fresh := false
	var n normalization
	response, e := app.ExecuteNormalized(ctx, s.Store, s.Auth.Authority(token), request, func(tx app.WriteTx) ([]byte, error) {
		current, e := s.owner(ctx, tx, token, key, version, input.PagePath)
		if e != nil {
			return nil, e
		}
		if current.PrincipalID != actor.PrincipalID {
			return nil, domain.ErrDenied
		}
		explanation, e := resolveExplanation(tx, current.Scope, version, input.PagePath, input.RegionID)
		if e != nil {
			return nil, e
		}
		project, e := tx.Scope(explanation.ProjectID)
		if e != nil {
			return nil, e
		}
		config, e := tx.Configuration()
		if e != nil {
			return nil, e
		}
		var defaults struct {
			Projects map[string]struct {
				Provider string `json:"provider_id"`
				Mode     string `json:"source_mode_default"`
			} `json:"projects"`
		}
		if json.Unmarshal(config.Value, &defaults) != nil {
			return nil, domain.ErrInvalidTarget
		}
		provider := input.ProviderID
		if provider == "" {
			provider = defaults.Projects[explanation.ProjectID].Provider
		}
		if provider != "codex" {
			return nil, sec.ErrInvalid
		}
		var selected source.CaptureRequest
		if json.Unmarshal(encode(input.Sources), &selected) != nil || selected.VersionID != "" && selected.VersionID != version || selected.ProviderID != "" && selected.ProviderID != provider {
			return nil, sec.ErrInvalid
		}
		selected.VersionID, selected.ProviderID = version, provider
		if input.SourceMode != "" {
			if selected.SourceMode != "" && selected.SourceMode != input.SourceMode {
				return nil, sec.ErrInvalid
			}
			selected.SourceMode = input.SourceMode
		}
		if e = resolveSourceMode(declaredIntent, defaults.Projects[explanation.ProjectID].Mode, &selected); e != nil {
			return nil, e
		}
		limitations := []domain.QuestionLimitation{}
		if !input.SourcesPresent {
			selected.Selections, e = defaultSelections(tx, explanation.ProjectID, version, selected.SourceMode, provider)
			if e != nil {
				return sourceRefusal(tx, current, key, e)
			}
		}
		if len(selected.Selections) == 0 {
			limitations = append(limitations, domain.QuestionLimitation{Code: "unable_to_verify", Message: "No project source bytes were selected. Explanation context does not establish project-source verification."})
		}
		mappings := map[string]domain.ManagedSourceMapping{}
		for _, selection := range selected.Selections {
			mapping, e := app.ManagedSourcePolicy(tx, explanation.ProjectID, selection.SourceID, provider)
			if e != nil {
				return sourceRefusal(tx, current, key, e)
			}
			mappings[selection.SourceID] = mapping
		}
		q := domain.Question{OwnerID: current.PrincipalID, ProjectID: explanation.ProjectID, DocumentID: explanation.DocumentID, VersionID: version, OperationID: key, CreatedAt: s.Now().UTC().Format(time.RFC3339Nano), PagePath: input.PagePath, RegionID: input.RegionID, PageQuery: input.PageQuery, PageFragment: input.PageFragment, Text: domain.ProviderText{Hash: text.Hash, Size: text.Size}, Explanation: explanation, SourceMode: selected.SourceMode, SourceSelection: encode(selected), SourceReadIDs: source.RetainedReadIDs(selected, mappings), AdmissionReady: len(selected.Selections) == 0, Limitations: limitations}
		for _, id := range []*string{&q.ID, &q.JobID, &q.CaptureOperationID} {
			*id, e = service.NewID()
			if e != nil {
				return nil, e
			}
		}
		if contextInput != nil {
			value := *contextInput
			if value.ID == "" {
				value.ID, e = service.NewID()
				if e != nil {
					return nil, e
				}
				n.NewContext = &domain.ContextRecord{ID: value.ID, PrincipalID: current.PrincipalID, SessionID: current.ID, VersionID: version, PagePath: input.PagePath, MediaType: value.MediaType, Object: domain.Object{Hash: value.Hash, Size: value.Size, State: "ready"}, CreatedAt: q.CreatedAt}
			} else if e = checkContext(tx, current, version, input.PagePath, value); e != nil {
				return nil, e
			}
			q.Context = &value
		}
		hash := sha256.Sum256(request.Body)
		q.RequestHash = hex.EncodeToString(hash[:])
		inv := domain.ProviderInvocation{PromptRef: &q.Text, Model: input.Model, Effort: input.Effort, Question: &domain.QuestionInvocation{QuestionID: q.ID, VersionID: q.VersionID, PagePath: q.PagePath, RegionID: q.RegionID, PageQuery: q.PageQuery, PageFragment: q.PageFragment, SourceMode: q.SourceMode, Context: q.Context, Explanation: q.Explanation, Limitations: q.Limitations}}
		scope := domain.ProviderScope{Purpose: "body", ProviderID: provider, PrincipalID: current.PrincipalID, AuthEpoch: current.AuthEpoch, CredentialScope: current.Scope, Invocation: &inv, Target: textTarget(key, version, input.PagePath)}
		scope.RequestSHA, e = s.Identity(scope.PrincipalID, scope.Target, inv, nil)
		if e != nil {
			return nil, e
		}
		n.Question, n.Scope, n.ConfigRevision, n.PermissionsRevision = q, scope, config.Revision, project.Project.PermissionsRevision
		return encode(n), nil
	}, func(tx app.WriteTx, receipt domain.Receipt) (domain.Response, error) {
		if e := json.Unmarshal(receipt.Normalization, &n); e != nil {
			return domain.Response{}, e
		}
		if n.Refusal != nil {
			return refusalResponse(*n.Refusal), nil
		}
		q := n.Question
		if n.NewContext != nil {
			if e := tx.PutContext(*n.NewContext); e != nil {
				return domain.Response{}, e
			}
		}
		job := domain.Job{ID: q.JobID, Kind: "question", Scope: encode(n.Scope), ConfigRevision: n.ConfigRevision, Permissions: map[string]int64{q.ProjectID: n.PermissionsRevision}}
		if e := tx.CreateJob(job); e != nil {
			return domain.Response{}, e
		}
		if e := tx.CreateQuestion(q); e != nil {
			return domain.Response{}, e
		}
		if !q.AdmissionReady {
			if e := tx.PauseProviderJob(job.ID, "source_capture_pending"); e != nil {
				return domain.Response{}, e
			}
		}
		fresh = true
		return domain.Response{Status: 202, Body: encode(map[string]any{"operation_id": key, "question_id": q.ID, "job_id": q.JobID, "job_url": "/api/v1/jobs/" + q.JobID})}, nil
	})
	if e != nil || !fresh || n.Question.AdmissionReady {
		return admissionResponse(response, e)
	}
	if s.Sources == nil {
		return response, s.captureUnavailable(context.WithoutCancel(ctx), n.Question.JobID)
	}
	_, captureErr := s.Sources.CaptureQuestionAndAdmit(ctx, n.Question.JobID, func(tx app.WriteTx, capture domain.SourceCapture) error {
		job, e := tx.Job(n.Question.JobID)
		if e != nil {
			return e
		}
		var scope domain.ProviderScope
		if json.Unmarshal(job.Scope, &scope) != nil {
			return domain.ErrInvalidTarget
		}
		if e = app.AuthorizeQuestion(tx, scope); e != nil {
			return e
		}
		if capture.ProjectID != n.Question.ProjectID || capture.PrincipalID != scope.PrincipalID || capture.ConfigRevision != job.ConfigRevision || capture.PermissionsRevision != job.Permissions[capture.ProjectID] {
			return domain.ErrPrecondition
		}
		scope.TargetSetID = capture.TargetSetID
		reads := []domain.CapturedRead{}
		after := ""
		for {
			rows, e := tx.SourceCaptureReads(capture.ID, after, 64)
			if e != nil {
				return e
			}
			if len(rows) == 0 {
				break
			}
			for _, row := range rows {
				if row.Availability != "available" {
					return domain.ErrReconciliation
				}
				read, e := tx.CapturedRead(row.ID)
				if e != nil {
					return e
				}
				reads, scope.ReadIDs, after = append(reads, read), append(scope.ReadIDs, row.ID), row.ID
			}
		}
		scope.RequestSHA, e = s.Identity(scope.PrincipalID, scope.Target, *scope.Invocation, reads)
		if e != nil {
			return e
		}
		job.Scope = encode(scope)
		if e = tx.AdmitQuestion(job); e != nil {
			return e
		}
		return tx.AttachSourceInputs(job.ID, scope.ReadIDs)
	})
	if captureErr != nil {
		return response, errors.Join(captureErr, s.captureUnavailable(context.WithoutCancel(ctx), n.Question.JobID))
	}
	return response, s.captureUnavailable(context.WithoutCancel(ctx), n.Question.JobID)
}

func (s *Service) captureUnavailable(ctx context.Context, id string) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error {
		job, e := tx.Job(id)
		if e != nil {
			return e
		}
		if job.CancelRequested {
			return tx.ConfirmStop(id, job.AttemptGeneration)
		}
		if job.State != "paused" || job.Error != "source_capture_pending" {
			return nil
		}
		return tx.QuestionCaptureUnavailable(id, []domain.QuestionLimitation{{Code: "unable_to_verify", Message: "The exact requested project-source selection is unavailable; no partial source request was dispatched."}})
	})
}

func (s *Service) owner(ctx context.Context, tx app.WriteTx, token, key, version string, page *string) (domain.Session, error) {
	actor, e := s.Auth.AuthenticateWrite(ctx, tx, token)
	if e != nil {
		return actor, e
	}
	if actor.Scope.Kind != "owner" {
		return actor, domain.ErrDenied
	}
	e = app.AuthorizeTarget(tx, textTarget(key, version, page))
	return actor, e
}

func textTarget(key, version string, page *string) []byte {
	return encode(map[string]any{"operation_id": key, "action": "question", "version_id": version, "page_path": page})
}

func resolveExplanation(tx app.WriteTx, scope domain.CredentialScope, version string, page, region *string) (domain.GenerationReference, error) {
	v, e := tx.PublishedVersion(version)
	if e != nil {
		return domain.GenerationReference{}, e
	}
	hash := v.PayloadSHA
	if page != nil {
		entry, e := tx.QueryEntry(v.ContentSetID, *page)
		if e != nil {
			return domain.GenerationReference{}, e
		}
		hash = entry.Hash
	}
	return app.ResolveQuestionReference(tx, scope, domain.ExplanationReference{VersionID: version, PagePath: page, RegionID: region, Hash: hash})
}

func checkContext(tx app.WriteTx, actor domain.Session, version string, page *string, input domain.QuestionContext) error {
	c, e := tx.Context(input.ID)
	if e != nil {
		return e
	}
	if c.PrincipalID != actor.PrincipalID || c.VersionID != version || !reflect.DeepEqual(c.PagePath, page) || !reflect.DeepEqual(c.RenderInstanceID, input.RenderInstanceID) || c.Object.Hash != input.Hash || c.Object.Size != input.Size || c.MediaType != input.MediaType {
		return domain.ErrDenied
	}
	return nil
}
