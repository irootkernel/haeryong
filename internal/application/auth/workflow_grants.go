package auth

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// WorkflowGrantRequest is accepted only by owner-authenticated protected local
// IPC. The trusted caller journals its random bearer before sending its verifier;
// neither the operation receipt nor a provider receives that bearer.
type WorkflowGrantRequest struct {
	BindingID                 string          `json:"workflow_binding_id"`
	PreviousCredentialID      *string         `json:"previous_credential_id,omitempty"`
	CredentialID              string          `json:"credential_id"`
	TokenVerifier             string          `json:"token_verifier"`
	Workflow                  string          `json:"workflow"`
	ProjectID                 string          `json:"project_id"`
	DocumentID                *string         `json:"document_id"`
	VersionIDs                []string        `json:"version_ids"`
	DraftIDs                  []string        `json:"draft_ids"`
	ProviderID                string          `json:"provider_id"`
	SourceSelection           json.RawMessage `json:"source_selection"`
	ExpectedSelectionRevision int64           `json:"expected_selection_revision"`
	ExpiresAt                 *time.Time      `json:"expires_at"`
}
type WorkflowGrant struct {
	BindingID    string                   `json:"workflow_binding_id"`
	PrincipalID  string                   `json:"principal_id"`
	CredentialID string                   `json:"credential_id,omitempty"`
	LibraryID    string                   `json:"library_id"`
	AuthEpoch    string                   `json:"auth_epoch"`
	Active       bool                     `json:"active"`
	Scope        domain.CredentialScope   `json:"scope"`
	Selection    domain.WorkflowSelection `json:"selection"`
	ExpiresAt    *time.Time               `json:"expires_at,omitempty"`
}

var workflowGrantID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func workflowActions(kind string) []string {
	switch kind {
	case "generation":
		return []string{"project.read", "version.read", "source.read", "generation.request", "job.read", "job.cancel"}
	case "import":
		return []string{"project.read", "version.read", "source.read", "import.publish", "job.read"}
	case "draft_write":
		return []string{"project.read", "version.read", "draft.write"}
	case "draft_publish":
		return []string{"project.read", "version.read", "draft.publish"}
	case "scan":
		return []string{"project.read", "version.read", "source.read", "scan.request", "job.read", "job.cancel"}
	}
	return nil
}

func projectPostWorkScanEnabled(cfg domain.Configuration, projectID string) bool {
	var parsed struct {
		Projects map[string]struct {
			PostWorkScanEnabled bool `json:"post_work_scan_enabled"`
		} `json:"projects"`
	}
	if json.Unmarshal(cfg.Value, &parsed) != nil {
		return false
	}
	return parsed.Projects[projectID].PostWorkScanEnabled
}
func (s *Service) workflowOwner(ctx context.Context, tx app.WriteTx, token string) (domain.Session, error) {
	actor, e := s.AuthenticateWrite(ctx, tx, token)
	if e != nil {
		return actor, e
	}
	if actor.Scope.Kind != "owner" {
		return actor, domain.ErrDenied
	}
	return actor, nil
}
func (s *Service) WorkflowGrant(ctx context.Context, token, binding string) (WorkflowGrant, error) {
	var out WorkflowGrant
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		if _, e := s.workflowOwner(ctx, tx, token); e != nil {
			return e
		}
		p, e := tx.WorkflowPrincipalByBinding(binding)
		if e != nil {
			return e
		}
		selection, e := tx.WorkflowSelection(binding)
		if e != nil {
			return e
		}
		state, e := tx.AuthState()
		if e != nil {
			return e
		}
		out = WorkflowGrant{BindingID: p.BindingID, PrincipalID: p.ID, LibraryID: state.LibraryID, AuthEpoch: state.AuthEpoch, Active: p.Active, Scope: p.Scope, Selection: selection}
		return nil
	})
	return out, e
}
func (s *Service) IssueWorkflowGrant(ctx context.Context, token, key string, input WorkflowGrantRequest) (domain.Response, error) {
	actor, e := s.Authenticate(ctx, token)
	if e != nil {
		return domain.Response{}, e
	}
	if actor.Scope.Kind != "owner" {
		return domain.Response{}, domain.ErrDenied
	}
	verifier, e := hex.DecodeString(input.TokenVerifier)
	if e != nil || len(verifier) != 32 || len(input.TokenVerifier) != 64 || !workflowGrantID.MatchString(input.BindingID) || !workflowGrantID.MatchString(input.CredentialID) || !workflowGrantID.MatchString(input.ProjectID) || hex.EncodeToString(verifier) != input.TokenVerifier || workflowActions(input.Workflow) == nil || input.ExpectedSelectionRevision < 0 || input.SourceSelection != nil && !json.Valid(input.SourceSelection) || input.ExpiresAt != nil && !s.now().Before(*input.ExpiresAt) {
		return domain.Response{}, sec.ErrInvalid
	}
	request := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: "POST", Target: "/local/v1/workflow-grants", Body: encodeGrant(input)}
	return app.ExecuteNormalized(ctx, s.Store, s.Authority(token), request, func(tx app.WriteTx) ([]byte, error) {
		if _, e := s.workflowOwner(ctx, tx, token); e != nil {
			return nil, e
		}
		p, e := tx.WorkflowPrincipalByBinding(input.BindingID)
		if errors.Is(e, domain.ErrNotFound) {
			p.ID, e = service.NewID()
		} else if e == nil {
			selection, e := tx.WorkflowSelection(input.BindingID)
			if e != nil {
				return nil, e
			}
			if selection.Revision != input.ExpectedSelectionRevision {
				return nil, domain.ErrPrecondition
			}
		}
		if e != nil {
			return nil, e
		}
		return encodeGrant(map[string]any{"principal_id": p.ID, "created_at": s.now().UTC().Truncate(time.Millisecond)}), nil
	}, func(tx app.WriteTx, r domain.Receipt) (domain.Response, error) {
		if _, e := s.workflowOwner(ctx, tx, token); e != nil {
			return domain.Response{}, e
		}
		var n struct {
			Principal string    `json:"principal_id"`
			Created   time.Time `json:"created_at"`
		}
		if json.Unmarshal(r.Normalization, &n) != nil {
			return domain.Response{}, sec.ErrInvalid
		}
		target, _ := json.Marshal(map[string]any{"project_id": input.ProjectID, "document_id": input.DocumentID})
		if e := app.AuthorizeTarget(tx, target); e != nil {
			return domain.Response{}, e
		}
		cfg, e := tx.Configuration()
		if e != nil {
			return domain.Response{}, e
		}
		scanEnabled := projectPostWorkScanEnabled(cfg, input.ProjectID)
		if input.Workflow == "scan" && !scanEnabled {
			return domain.Response{}, domain.ErrDenied
		}
		actions := append([]string(nil), workflowActions(input.Workflow)...)
		if scanEnabled {
			for _, extra := range []string{"scan.request", "job.read", "job.cancel"} {
				if !contains(actions, extra) {
					actions = append(actions, extra)
				}
			}
		}
		scope := domain.CredentialScope{Kind: "workflow", Actions: actions, ProjectIDs: []string{input.ProjectID}, VersionIDs: input.VersionIDs, DraftIDs: input.DraftIDs}
		for _, id := range input.VersionIDs {
			v, e := tx.PublishedVersion(id)
			if e != nil {
				return domain.Response{}, e
			}
			d, e := tx.Document(v.DocumentID)
			if e != nil {
				return domain.Response{}, e
			}
			if e = app.AuthorizeTarget(tx, encodeGrant(map[string]string{"version_id": id})); e != nil {
				return domain.Response{}, e
			}
			if !contains(scope.ProjectIDs, d.ProjectID) {
				scope.ProjectIDs = append(scope.ProjectIDs, d.ProjectID)
			}
		}
		for _, id := range input.DraftIDs {
			d, e := tx.Draft(id)
			if e != nil {
				return domain.Response{}, e
			}
			if d.ProjectID != input.ProjectID {
				return domain.Response{}, domain.ErrDenied
			}
			if e = app.AuthorizeTarget(tx, encodeGrant(map[string]any{"project_id": d.ProjectID, "document_id": d.DocumentID})); e != nil {
				return domain.Response{}, e
			}
		}
		if (input.Workflow == "draft_write" || input.Workflow == "draft_publish") && len(input.DraftIDs) == 0 {
			return domain.Response{}, sec.ErrInvalid
		}
		p := domain.WorkflowPrincipal{ID: n.Principal, BindingID: input.BindingID, Active: true, Scope: scope}
		if e := tx.PutWorkflowPrincipal(p); e != nil {
			return domain.Response{}, e
		}
		selection := domain.WorkflowSelection{BindingID: input.BindingID, PrincipalID: p.ID, ProjectID: input.ProjectID, DocumentID: input.DocumentID, ProviderID: input.ProviderID, SourceSelection: input.SourceSelection}
		if e := tx.PutWorkflowSelection(selection, input.ExpectedSelectionRevision); e != nil {
			return domain.Response{}, e
		}
		state, e := tx.AuthState()
		if e != nil {
			return domain.Response{}, e
		}
		permissions := map[string]int64{}
		var config int64
		for _, id := range scope.ProjectIDs {
			project, e := tx.Scope(id)
			if e != nil {
				return domain.Response{}, e
			}
			permissions[id] = project.Project.PermissionsRevision
			config = project.ConfigRevision
		}
		credential := domain.ScopedCredential{ID: input.CredentialID, PrincipalID: p.ID, TokenHash: input.TokenVerifier, AuthEpoch: state.AuthEpoch, Scope: scope, MutationProjectID: input.ProjectID, Permissions: permissions, ConfigRevision: config, CreatedAt: n.Created, ExpiresAt: input.ExpiresAt, Revision: 1}
		if input.PreviousCredentialID != nil {
			old, e := tx.ScopedCredential(*input.PreviousCredentialID)
			if e != nil {
				return domain.Response{}, e
			}
			if old.PrincipalID != p.ID || old.ID == credential.ID {
				return domain.Response{}, domain.ErrInvalidTarget
			}
			old.Revoked = true
			if e = tx.PutScopedCredential(old); e != nil {
				return domain.Response{}, e
			}
		}
		if e = tx.PutScopedCredential(credential); e != nil {
			return domain.Response{}, e
		}
		selection, e = tx.WorkflowSelection(input.BindingID)
		if e != nil {
			return domain.Response{}, e
		}
		return domain.Response{Status: 200, Body: encodeGrant(WorkflowGrant{BindingID: input.BindingID, PrincipalID: p.ID, CredentialID: credential.ID, LibraryID: state.LibraryID, AuthEpoch: state.AuthEpoch, Active: true, Scope: scope, Selection: selection, ExpiresAt: credential.ExpiresAt})}, nil
	})
}
func (s *Service) RevokeWorkflowGrant(ctx context.Context, token, key, binding string) (domain.Response, error) {
	actor, e := s.Authenticate(ctx, token)
	if e != nil {
		return domain.Response{}, e
	}
	if actor.Scope.Kind != "owner" {
		return domain.Response{}, domain.ErrDenied
	}
	request := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: "POST", Target: "/local/v1/workflow-grants/" + binding + "/revoke", Body: []byte(`{}`)}
	return app.Execute(ctx, s.Store, s.Authority(token), request, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		if _, e := s.workflowOwner(ctx, tx, token); e != nil {
			return domain.Response{}, e
		}
		p, e := tx.WorkflowPrincipalByBinding(binding)
		if e != nil {
			return domain.Response{}, e
		}
		p.Active = false
		if e = tx.PutWorkflowPrincipal(p); e != nil {
			return domain.Response{}, e
		}
		return domain.Response{Status: 200, Body: encodeGrant(map[string]any{"workflow_binding_id": binding, "principal_id": p.ID, "active": false})}, nil
	})
}
func encodeGrant(value any) []byte { raw, _ := json.Marshal(value); return raw }
