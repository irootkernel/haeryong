package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	"haeryong.local/haeryong/internal/adapters/localcontrol"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

type workflowSelectionInput struct {
	BindingID       string          `json:"workflow_binding_id,omitempty"`
	Workflow        string          `json:"workflow"`
	ProjectID       string          `json:"project_id"`
	DocumentID      *string         `json:"document_id"`
	VersionIDs      []string        `json:"version_ids,omitempty"`
	DraftIDs        []string        `json:"draft_ids,omitempty"`
	ProviderID      string          `json:"provider_id,omitempty"`
	SourceSelection json.RawMessage `json:"source_selection,omitempty"`
	ExpiresAt       *time.Time      `json:"expires_at,omitempty"`
}

func grantCommand(ctx context.Context, opt options, emit func(any) error) error {
	if opt.endpoint != "" {
		return invalid("workflow issuance uses verified local profile discovery")
	}
	if len(opt.command) < 3 {
		return invalid("use auth grant create/rotate/show/revoke")
	}
	api, state, e := selectedClient(ctx, opt)
	if e != nil {
		return e
	}
	identity, e := api.Identity(ctx)
	if e != nil {
		return productError(e)
	}
	var actor domain.Session
	if _, e = api.JSON(ctx, "GET", "/api/v1/session", "", nil, &actor); e != nil {
		return productError(e)
	}
	if actor.Scope.Kind != "owner" {
		return commandError{3, errors.New("owner authentication is required for local workflow issuance")}
	}
	profile, e := service.Load(opt.selection)
	if e != nil {
		return e
	}
	id, e := service.ReadDiscovery(profile)
	if e != nil {
		return e
	}
	if identity.LibraryID != profile.LibraryID || identity.LibraryID != id.LibraryID {
		return transfer.ErrIntent
	}
	ownerPath := opt.flags["credential"]
	if ownerPath == "" {
		ownerPath = filepath.Join(state.Root, "credential.json")
	}
	owner, e := client.LoadCredential(ownerPath)
	if e != nil {
		return e
	}
	if owner.LibraryID != identity.LibraryID || owner.Profile != opt.selection.Profile {
		return transfer.ErrIntent
	}
	verb := opt.command[2]
	if verb == "show" || verb == "revoke" {
		if len(opt.command) != 4 || !operationID.MatchString(opt.command[3]) {
			return invalid("workflow binding ID required")
		}
		binding := opt.command[3]
		if verb == "show" {
			grant, e := localcontrol.WorkflowGrant(ctx, profile, id, owner.Token, binding)
			if e != nil {
				return e
			}
			return emit(grant)
		}
		target := "/local/v1/workflow-grants/" + binding + "/revoke"
		r, key, e := mutationDispatch(ctx, api, state, opt.flags["operation-id"], "POST", target, map[string]any{}, nil, func(key string, raw []byte) (domain.Response, error) {
			if !bytes.Equal(raw, []byte(`{}`)) {
				return domain.Response{}, transfer.ErrIntent
			}
			return localcontrol.RevokeWorkflowGrantResult(ctx, profile, id, owner.Token, key, binding)
		})
		if e != nil {
			return productError(e)
		}
		if e = emit(json.RawMessage(r.Body)); e != nil {
			return e
		}
		return finishMutation(ctx, state, clientstate.CommandIntent{Method: "POST", Target: target}, key)
	}
	if verb != "create" && verb != "rotate" || len(opt.command) != 3 || opt.flags["input"] == "" {
		return invalid("auth grant create/rotate requires --input selection.json")
	}
	path, e := filepath.Abs(opt.flags["input"])
	if e != nil {
		return e
	}
	key := opt.flags["operation-id"]
	intent := clientstate.CommandIntent{Method: "POST", Target: "/local/v1/workflow-grants", SourcePath: path}
	if key == "" {
		key, e = service.NewID()
		if e != nil {
			return e
		}
		key, e = state.UseCommandPending(ctx, identity, intent, key)
		if e != nil {
			return e
		}
	}
	if !operationID.MatchString(key) {
		return invalid("canonical operation ID required")
	}
	release, e := state.Lock(ctx, key)
	if e != nil {
		return e
	}
	defer release()
	j, e := state.LoadWorkflow(key)
	if errors.Is(e, domain.ErrNotFound) {
		var input workflowSelectionInput
		file, e := os.Open(path)
		if e != nil {
			return e
		}
		decodeErr := decodeWorkflowSelection(file, &input)
		closeErr := file.Close()
		if decodeErr != nil {
			return decodeErr
		}
		if closeErr != nil {
			return closeErr
		}
		if input.ProjectID == "" {
			return invalid("explicit selected project required")
		}
		if input.BindingID == "" {
			if verb == "rotate" {
				return invalid("rotation requires an existing workflow binding")
			}
			candidate, e := service.NewID()
			if e != nil {
				return e
			}
			input.BindingID, e = state.WorkflowBinding(ctx, identity, path, candidate)
			if e != nil {
				return e
			}
		}
		if !operationID.MatchString(input.BindingID) {
			return invalid("canonical workflow binding ID required")
		}
		request := appauth.WorkflowGrantRequest{BindingID: input.BindingID, Workflow: input.Workflow, ProjectID: input.ProjectID, DocumentID: input.DocumentID, VersionIDs: input.VersionIDs, DraftIDs: input.DraftIDs, ProviderID: input.ProviderID, SourceSelection: input.SourceSelection, ExpiresAt: input.ExpiresAt}
		current, showErr := localcontrol.WorkflowGrant(ctx, profile, id, owner.Token, input.BindingID)
		if showErr == nil {
			request.ExpectedSelectionRevision = current.Selection.Revision
		} else {
			var missing *localcontrol.BootstrapError
			if !errors.As(showErr, &missing) || missing.Status != 404 {
				return showErr
			}
			if verb == "rotate" {
				return showErr
			}
		}
		previous, oldErr := state.ActiveWorkflow(input.BindingID)
		if oldErr == nil {
			if previous.Identity.LibraryID != identity.LibraryID || previous.Identity.PrincipalID != identity.PrincipalID {
				return transfer.ErrIntent
			}
			installed, e := client.LoadCredential(workflowCredentialPath(state, input.BindingID))
			if e != nil {
				return e
			}
			if installed.Token != previous.Credential.Token {
				return transfer.ErrReconcile
			}
			// Reusing an unchanged remembered selection needs no new issuance.
			// Invalid/expired workflow authentication is reported, never retried
			// through the owner's wider API credential.
			if verb == "create" && opt.flags["operation-id"] == "" && showErr == nil && previous.Response != nil && current.Active && current.AuthEpoch == previous.Response.AuthEpoch && reflect.DeepEqual(current.Selection, previous.Response.Selection) && sameWorkflowSelection(input, previous.Request) {
				scoped, e := client.New(id.Endpoint, opt.selection.Profile, installed)
				if e != nil {
					return e
				}
				verified, e := scoped.Identity(ctx)
				if e != nil {
					return productError(e)
				}
				if verified.LibraryID != identity.LibraryID || verified.PrincipalID != previous.Response.PrincipalID {
					return transfer.ErrIntent
				}
				if e = emit(map[string]any{"workflow_binding_id": input.BindingID, "principal_id": verified.PrincipalID, "library_id": verified.LibraryID, "selection": current.Selection, "scope": current.Scope, "credential_path": workflowCredentialPath(state, input.BindingID), "operation_id": previous.OperationID, "reused": true}); e != nil {
					return e
				}
				return state.FinishCommandPending(ctx, identity, intent, key)
			}
			request.PreviousCredentialID = &previous.Request.CredentialID
		} else if !errors.Is(oldErr, domain.ErrNotFound) {
			return oldErr
		} else if verb == "rotate" {
			return transfer.ErrReconcile
		}
		secret, e := sec.Secret()
		if e != nil {
			return e
		}
		request.TokenVerifier = sec.Verifier("workflow", secret)
		request.CredentialID, e = service.NewID()
		if e != nil {
			return e
		}
		j = clientstate.WorkflowJournal{Action: verb, OperationID: key, Identity: identity, InputPath: path, Request: request, Credential: client.Credential{Endpoint: id.Endpoint, Profile: opt.selection.Profile, LibraryID: identity.LibraryID, Token: secret}}
		if e = state.SaveWorkflow(j); e != nil {
			return e
		} // before the first issue send
	} else if e != nil {
		return e
	}
	if j.Action != verb || j.Identity.LibraryID != identity.LibraryID || j.Identity.PrincipalID != identity.PrincipalID || j.InputPath != path {
		return transfer.ErrIntent
	}
	grant, e := localcontrol.IssueWorkflowGrant(ctx, profile, id, owner.Token, key, j.Request)
	if e != nil {
		return e
	}
	if grant.BindingID != j.Request.BindingID || grant.LibraryID != identity.LibraryID || grant.CredentialID != j.Request.CredentialID || grant.Selection.ProjectID != j.Request.ProjectID {
		return transfer.ErrReconcile
	}
	j.Response = &grant
	if e = state.SaveWorkflow(j); e != nil {
		return e
	}
	// A replay of an old completed issue cannot overwrite a later rotation or
	// changed current selection with an expired, revoked or no-longer-matching key.
	current, e := localcontrol.WorkflowGrant(ctx, profile, id, owner.Token, grant.BindingID)
	if e != nil {
		return e
	}
	if !current.Active || current.PrincipalID != grant.PrincipalID || !reflect.DeepEqual(current.Scope, grant.Scope) || !reflect.DeepEqual(current.Selection, grant.Selection) || current.AuthEpoch != grant.AuthEpoch {
		return transfer.ErrReconcile
	}
	scoped, e := client.New(id.Endpoint, opt.selection.Profile, j.Credential)
	if e != nil {
		return e
	}
	verified, e := scoped.Identity(ctx)
	if e != nil {
		return productError(e)
	}
	if verified.LibraryID != identity.LibraryID || verified.PrincipalID != grant.PrincipalID {
		return transfer.ErrIntent
	}
	destination := workflowCredentialPath(state, grant.BindingID)
	if e = client.SaveCredential(destination, j.Credential); e != nil {
		return e
	}
	if e = state.ActivateWorkflow(j); e != nil {
		return e
	}
	if e = emit(map[string]any{"workflow_binding_id": grant.BindingID, "principal_id": grant.PrincipalID, "library_id": grant.LibraryID, "selection": grant.Selection, "scope": grant.Scope, "credential_path": destination, "operation_id": key}); e != nil {
		return e
	}
	if e = state.FinishCommandPending(ctx, identity, intent, key); e != nil {
		return fmt.Errorf("workflow issued; caller acknowledgement index unavailable: %w", e)
	}
	return nil
}
func workflowCredentialPath(state *clientstate.Store, binding string) string {
	return filepath.Join(state.Root, "workflow-"+binding+".credential.json")
}

func sameWorkflowSelection(input workflowSelectionInput, old appauth.WorkflowGrantRequest) bool {
	return input.BindingID == old.BindingID && input.Workflow == old.Workflow && input.ProjectID == old.ProjectID && reflect.DeepEqual(input.DocumentID, old.DocumentID) && reflect.DeepEqual(input.VersionIDs, old.VersionIDs) && reflect.DeepEqual(input.DraftIDs, old.DraftIDs) && input.ProviderID == old.ProviderID && bytes.Equal(input.SourceSelection, old.SourceSelection) && reflect.DeepEqual(input.ExpiresAt, old.ExpiresAt)
}
func decodeWorkflowSelection(r io.Reader, out *workflowSelectionInput) error {
	d := json.NewDecoder(r)
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return invalid("invalid workflow selection JSON")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		name, e := d.Token()
		if e != nil {
			return invalid("invalid workflow selection JSON")
		}
		key, ok := name.(string)
		if !ok || fields[key] != nil {
			return invalid("duplicate workflow selection field")
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return invalid("invalid workflow selection JSON")
		}
		fields[key] = raw
	}
	if _, e = d.Token(); e != nil {
		return invalid("invalid workflow selection JSON")
	}
	if _, e = d.Token(); e != io.EOF {
		return invalid("trailing workflow selection JSON")
	}
	raw, e := json.Marshal(fields)
	if e != nil {
		return e
	}
	decode := json.NewDecoder(bytes.NewReader(raw))
	decode.DisallowUnknownFields()
	if decode.Decode(out) != nil {
		return invalid("invalid workflow selection fields")
	}
	return nil
}
