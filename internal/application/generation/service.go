// Package generation admits explicit generation intent onto the existing
// provider dispatcher and owns no provider queue or retry policy.
package generation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	Bytes    source.CaptureBytes
	Store    app.Store
	Auth     *auth.Service
	Sources  *source.Service
	Skill    domain.GenerationInvocation
	Identity Identity
	Now      func() time.Time
}

func New(store app.Store, a *auth.Service, sources *source.Service, skill domain.GenerationInvocation, identity Identity) *Service {
	bytes, _ := store.(source.CaptureBytes)
	return &Service{Store: store, Bytes: bytes, Auth: a, Sources: sources, Skill: skill, Identity: identity, Now: time.Now}
}

type Request struct {
	ProjectID       string                        `json:"project_id"`
	DocumentID      *string                       `json:"document_id"`
	DocumentPresent bool                          `json:"-"`
	BaseVersionID   *string                       `json:"base_version_id,omitempty"`
	BasePresent     bool                          `json:"-"`
	Intent          string                        `json:"intent"`
	Prompt          string                        `json:"prompt,omitempty"`
	BodyRef         *BodyRef                      `json:"body_ref,omitempty"`
	PriorReferences []domain.ExplanationReference `json:"explanation_references,omitempty"`
	Title           string                        `json:"title,omitempty"`
	ProviderID      string                        `json:"provider_id,omitempty"`
	Model           string                        `json:"model,omitempty"`
	Effort          string                        `json:"effort,omitempty"`
	Sources         source.CaptureRequest         `json:"source_selection"`
}

func encode(v any) []byte { b, _ := json.Marshal(v); return b }
func pointer(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

type normalization struct {
	Intent              domain.GenerationIntent `json:"intent"`
	Scope               domain.ProviderScope    `json:"scope"`
	ConfigRevision      int64                   `json:"config_revision"`
	PermissionsRevision int64                   `json:"permissions_revision"`
	ContextPermissions  map[string]int64        `json:"context_permissions,omitempty"`
}

func (s *Service) Start(ctx context.Context, token, key string, input Request) (domain.Response, error) {
	actor, e := s.Auth.Authenticate(ctx, token)
	if e != nil {
		return domain.Response{}, e
	}
	if !input.DocumentPresent || input.ProjectID == "" || (input.Intent != "publish" && input.Intent != "draft") || s.Identity == nil || s.Skill.SkillPath == "" || len(s.Skill.SkillSHA) != 64 || s.Skill.SkillVersion == "" {
		return domain.Response{}, sec.ErrInvalid
	}
	if _, e = app.NormalizeTarget("body", s.promptTarget(key, input)); e != nil {
		return domain.Response{}, sec.ErrInvalid
	}
	prompt, e := s.promptIdentity(ctx, token, key, input)
	if e != nil {
		return domain.Response{}, e
	}
	body := map[string]any{"project_id": input.ProjectID, "document_id": input.DocumentID, "intent": input.Intent, "prompt_sha256": prompt.Hash, "prompt_byte_size": prompt.Size, "title": input.Title, "provider_id": input.ProviderID, "model": input.Model, "effort": input.Effort, "source_selection": input.Sources, "explanation_references": input.PriorReferences}
	if input.BasePresent {
		body["base_version_id"] = input.BaseVersionID
	}
	request := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: "POST", Target: "/api/v1/generations", Body: encode(body)}
	if response, found, e := app.Replay(ctx, s.Store, s.Auth.Authority(token), request); e != nil || found {
		return response, e
	}
	prompt, release, e := s.preparePrompt(ctx, token, key, input, prompt)
	defer release()
	if e != nil {
		return domain.Response{}, e
	}
	fresh := false
	var n normalization
	response, e := app.ExecuteNormalized(ctx, s.Store, s.Auth.Authority(token), request, func(tx app.WriteTx) ([]byte, error) {
		current, e := s.Auth.AuthenticateWrite(ctx, tx, token)
		if e != nil {
			return nil, e
		}
		if current.PrincipalID != actor.PrincipalID {
			return nil, domain.ErrDenied
		}
		project, e := tx.Scope(input.ProjectID)
		if e != nil {
			return nil, e
		}
		cfg, e := tx.Configuration()
		if e != nil {
			return nil, e
		}
		provider := input.ProviderID
		if provider == "" {
			var configured struct {
				Projects map[string]struct {
					Provider string `json:"provider_id"`
				} `json:"projects"`
			}
			if json.Unmarshal(cfg.Value, &configured) != nil {
				return nil, domain.ErrInvalidTarget
			}
			provider = configured.Projects[input.ProjectID].Provider
		}
		if provider != "codex" {
			return nil, domain.ErrDenied
		}
		target := encode(map[string]any{"operation_id": key, "action": "generation", "project_id": input.ProjectID, "document_id": input.DocumentID})
		if e = auth.AuthorizeUploadScope(current.Scope, "body", target); e != nil {
			return nil, e
		}
		if e = app.AuthorizeTarget(tx, target); e != nil {
			return nil, e
		}
		g := domain.GenerationIntent{ProjectID: input.ProjectID, DocumentID: input.DocumentID, Intent: input.Intent, Title: input.Title, CreatedAt: s.Now().UTC().Format(time.RFC3339Nano)}
		for _, dst := range []*string{&g.JobID, &g.DraftID, &g.VersionID, &g.AllocatedDocumentID, &g.CaptureOperationID} {
			*dst, e = service.NewID()
			if e != nil {
				return nil, e
			}
		}
		if input.DocumentID != nil {
			d, e := tx.Document(*input.DocumentID)
			if e != nil {
				return nil, e
			}
			g.AllocatedDocumentID = d.ID
			g.DocumentRevision = d.Revision
			if input.BasePresent {
				if input.BaseVersionID == nil {
					return nil, sec.ErrInvalid
				}
				g.BaseVersionID = input.BaseVersionID
			} else {
				g.BaseVersionID = pointer(d.LatestVersionID)
			}
			if g.BaseVersionID == nil {
				return nil, sec.ErrInvalid
			}
			base, e := tx.PublishedVersion(*g.BaseVersionID)
			if e != nil {
				return nil, e
			}
			if base.DocumentID != d.ID {
				return nil, domain.ErrInvalidTarget
			}
			if g.Title == "" {
				g.Title = d.Title
			}
		} else if input.BaseVersionID != nil {
			return nil, sec.ErrInvalid
		}
		if g.Title == "" {
			g.Title = "Generated explanation"
		}
		var selected source.CaptureRequest
		if e = json.Unmarshal(encode(input.Sources), &selected); e != nil {
			return nil, e
		}
		if selected.SourceIntent != "" && selected.SourceIntent != "historical" && selected.SourceIntent != "moving_target" || selected.SourceMode != "" && selected.SourceMode != "pinned" && selected.SourceMode != "current" {
			return nil, sec.ErrInvalid
		}
		if selected.ProviderID != "" && selected.ProviderID != provider {
			return nil, sec.ErrInvalid
		}
		selected.ProviderID = provider
		if selected.VersionID != "" {
			return nil, sec.ErrInvalid
		} // project source selection is distinct from prior explanation context
		// Resolve the accepted source mode once so pending-job lifetime follows the
		// same retained reads as capture, before job_inputs have been adopted.
		if selected.SourceMode == "" {
			var defaults struct {
				Projects map[string]struct {
					Mode string `json:"source_mode_default"`
				} `json:"projects"`
			}
			if e = json.Unmarshal(cfg.Value, &defaults); e != nil {
				return nil, e
			}
			selected.SourceMode = defaults.Projects[input.ProjectID].Mode
			if selected.SourceIntent == "historical" || selected.SourceMode == "" {
				selected.SourceMode = "pinned"
			}
		}
		selectionMappings := map[string]domain.ManagedSourceMapping{}
		for _, selection := range selected.Selections {
			mapping, err := app.ManagedSourcePolicy(tx, input.ProjectID, selection.SourceID, provider)
			if err != nil {
				return nil, err
			}
			selectionMappings[selection.SourceID] = mapping
		}
		g.SourceReadIDs = source.RetainedReadIDs(selected, selectionMappings)

		g.SourceSelection = encode(selected)
		g.AdmissionReady = len(selected.Selections) == 0
		fingerprint := sha256.Sum256(request.Body)
		g.RequestHash = hex.EncodeToString(fingerprint[:])
		contextPermissions := map[string]int64{}
		for _, ref := range input.PriorReferences {
			captured, e := app.ResolveGenerationReference(tx, current.Scope, ref)
			if e != nil {
				return nil, e
			}
			priorScope, e := tx.Scope(captured.ProjectID)
			if e != nil {
				return nil, e
			}
			contextPermissions[captured.ProjectID] = priorScope.Project.PermissionsRevision
			g.PriorReferences = append(g.PriorReferences, captured)
		}
		skill := s.Skill
		skill.Intent = input.Intent
		skill.References = g.PriorReferences
		inv := domain.ProviderInvocation{PromptRef: promptDescriptor(prompt), Model: input.Model, Effort: input.Effort, Generation: &skill}
		// The frozen base and explicit output intent are semantic. Runtime output
		// directories, operation IDs and allocated document/version IDs are not.
		var targetFields map[string]any
		_ = json.Unmarshal(target, &targetFields)
		targetFields["base_version_id"] = g.BaseVersionID
		targetFields["intent"] = g.Intent
		scope := domain.ProviderScope{Purpose: "body", Invocation: &inv, ProviderID: provider, PrincipalID: current.PrincipalID, AuthEpoch: current.AuthEpoch, CredentialScope: current.Scope, Target: encode(targetFields)}
		scope.RequestSHA, e = s.Identity(scope.PrincipalID, scope.Target, inv, nil)
		if e != nil {
			return nil, e
		}
		n = normalization{Intent: g, Scope: scope, ConfigRevision: cfg.Revision, PermissionsRevision: project.Project.PermissionsRevision, ContextPermissions: contextPermissions}
		return encode(n), nil
	}, func(tx app.WriteTx, r domain.Receipt) (domain.Response, error) {
		if e := json.Unmarshal(r.Normalization, &n); e != nil {
			return domain.Response{}, e
		}
		g := n.Intent
		j := domain.Job{ID: g.JobID, Kind: "generation", Scope: encode(n.Scope), ConfigRevision: n.ConfigRevision, Permissions: map[string]int64{g.ProjectID: n.PermissionsRevision}}
		for project, revision := range n.ContextPermissions {
			j.Permissions[project] = revision
		}
		if e := tx.CreateJob(j); e != nil {
			return domain.Response{}, e
		}
		owned, e := app.GenerationInputObjects(tx, n.Scope)
		if e != nil {
			return domain.Response{}, e
		}
		for _, object := range owned {
			if e = tx.AcquirePin(domain.Pin{ID: "generation-input:" + j.ID + ":" + object.Hash, Kind: "job_input", OwnerID: j.ID, Hash: object.Hash}); e != nil {
				return domain.Response{}, e
			}
		}
		d := domain.Draft{ID: g.DraftID, ProjectID: g.ProjectID, JobID: g.JobID, ProposedVersionID: g.VersionID, DocumentID: g.DocumentID, BaseVersionID: g.BaseVersionID, AllocatedDocumentID: g.AllocatedDocumentID, State: "preparing", Intent: g.Intent, CreatedAt: g.CreatedAt, UpdatedAt: g.CreatedAt}
		if e := tx.CreateDraft(d); e != nil {
			return domain.Response{}, e
		}
		if e := tx.CreateGenerationIntent(g); e != nil {
			return domain.Response{}, e
		}
		if !g.AdmissionReady {
			if e := tx.PauseProviderJob(j.ID, "source_capture_pending"); e != nil {
				return domain.Response{}, e
			}
		}
		fresh = true
		return domain.Response{Status: 202, Body: encode(map[string]any{"operation_id": key, "job_id": j.ID, "draft_id": d.ID, "intent": g.Intent, "job_url": "/api/v1/jobs/" + j.ID})}, nil
	})
	if e != nil || !fresh || n.Intent.AdmissionReady {
		return response, e
	}
	if s.Sources == nil {
		return response, s.failPreparation(context.WithoutCancel(ctx), n.Intent.JobID)
	}
	_, captureErr := s.Sources.CaptureGenerationAndAdmit(ctx, n.Intent.JobID, func(tx app.WriteTx, c domain.SourceCapture) error {
		j, e := tx.Job(n.Intent.JobID)
		if e != nil {
			return e
		}
		var scope domain.ProviderScope
		if json.Unmarshal(j.Scope, &scope) != nil {
			return domain.ErrInvalidTarget
		}
		if e = auth.AuthorizeAccepted(tx, scope.PrincipalID, scope.AuthEpoch, "body", scope.Target, scope.CredentialScope); e != nil {
			return e
		}
		if c.ProjectID != n.Intent.ProjectID || c.PrincipalID != scope.PrincipalID || c.ConfigRevision != j.ConfigRevision || c.PermissionsRevision != j.Permissions[c.ProjectID] {
			return domain.ErrPrecondition
		}
		scope.TargetSetID = c.TargetSetID
		captured := []domain.CapturedRead{}
		after := ""
		for {
			rows, e := tx.SourceCaptureReads(c.ID, after, 64)
			if e != nil {
				return e
			}
			if len(rows) == 0 {
				break
			}
			for _, r := range rows {
				if r.Availability != "available" {
					return domain.ErrReconciliation
				}
				read, e := tx.CapturedRead(r.ID)
				if e != nil {
					return e
				}
				captured = append(captured, read)
				scope.ReadIDs = append(scope.ReadIDs, r.ID)
				after = r.ID
			}
		}
		scope.RequestSHA, e = s.Identity(scope.PrincipalID, scope.Target, *scope.Invocation, captured)
		if e != nil {
			return e
		}
		j.Scope = encode(scope)
		if e = tx.AdmitGeneration(j); e != nil {
			return e
		}
		return tx.AttachSourceInputs(j.ID, scope.ReadIDs)
	})
	if captureErr != nil {
		return response, errors.Join(captureErr, s.failPreparation(context.WithoutCancel(ctx), n.Intent.JobID))
	}
	// Incomplete capture is an actual durable failed preparation even when its
	// metadata response itself was delivered successfully.
	j, e := s.Store.Job(context.WithoutCancel(ctx), n.Intent.JobID)
	if e != nil {
		return response, e
	}
	if j.State == "paused" && j.Error == "source_capture_pending" {
		return response, s.failPreparation(context.WithoutCancel(ctx), j.ID)
	}
	return response, nil
}
func (s *Service) failPreparation(ctx context.Context, id string) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(id)
		if e != nil {
			return e
		}
		if j.CancelRequested {
			return tx.ConfirmStop(j.ID, j.AttemptGeneration)
		}
		if j.State != "paused" || j.Error != "source_capture_pending" {
			return nil
		}
		return tx.PauseProviderJob(id, "source_capture_unavailable")
	})
}
