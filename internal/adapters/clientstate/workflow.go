package clientstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"

	"haeryong.local/haeryong/internal/adapters/client"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// WorkflowJournal is protected caller state, never command output or provider
// input. The bearer exists here before its verifier can be sent to the issuer.
type WorkflowJournal struct {
	Action      string                       `json:"action"`
	OperationID string                       `json:"operation_id"`
	Identity    transfer.Identity            `json:"identity"`
	InputPath   string                       `json:"input_path"`
	Request     appauth.WorkflowGrantRequest `json:"request"`
	Credential  client.Credential            `json:"credential"`
	Response    *appauth.WorkflowGrant       `json:"response,omitempty"`
}

func (s *Store) LoadWorkflow(id string) (WorkflowJournal, error) {
	var j WorkflowJournal
	if !validID(id) {
		return j, fmt.Errorf("invalid workflow operation ID")
	}
	e := s.read(filepath.Join("journals", "workflow-"+id+".json"), &j)
	if e == nil {
		e = validateWorkflowJournal(j)
		if e == nil && j.OperationID != id {
			e = transfer.ErrIntent
		}
	}
	return j, e
}
func validateWorkflowJournal(j WorkflowJournal) error {
	if (j.Action != "create" && j.Action != "rotate") || !validID(j.OperationID) || !validID(j.Request.BindingID) || !validID(j.Request.CredentialID) || j.Identity.LibraryID == "" || j.Identity.PrincipalID == "" || j.InputPath == "" || !filepath.IsAbs(j.InputPath) || j.Credential.LibraryID != j.Identity.LibraryID || !sec.ValidSecret(j.Credential.Token) || sec.Verifier("workflow", j.Credential.Token) != j.Request.TokenVerifier {
		return fmt.Errorf("invalid private workflow journal")
	}
	return nil
}
func (s *Store) SaveWorkflow(j WorkflowJournal) error {
	if e := validateWorkflowJournal(j); e != nil {
		return e
	}
	old, e := s.LoadWorkflow(j.OperationID)
	if e == nil {
		before, after := old, j
		before.Response, after.Response = nil, nil
		if !reflect.DeepEqual(before, after) {
			return transfer.ErrIntent
		}
		if old.Response != nil && !reflect.DeepEqual(old.Response, j.Response) {
			return transfer.ErrIntent
		}
	} else if !errors.Is(e, domain.ErrNotFound) {
		return e
	}
	return s.write(filepath.Join("journals", "workflow-"+j.OperationID+".json"), j)
}

// WorkflowBinding remembers the trusted launcher's existing input selection.
// It does not authorize the selected resources or bypass the server's owner,
// current binding, library, scope, or credential checks.
func (s *Store) WorkflowBinding(ctx context.Context, identity transfer.Identity, inputPath, candidate string) (string, error) {
	if identity.LibraryID == "" || identity.PrincipalID == "" || !validID(candidate) {
		return "", transfer.ErrIntent
	}
	path, e := filepath.Abs(inputPath)
	if e != nil {
		return "", e
	}
	raw, _ := json.Marshal([]string{identity.LibraryID, identity.PrincipalID, path})
	sum := sha256.Sum256(raw)
	key := hex.EncodeToString(sum[:])
	release, e := s.Lock(ctx, pendingLockID(key))
	if e != nil {
		return "", e
	}
	defer release()
	name := filepath.Join("pending", "workflow-binding-"+key+".json")
	var saved struct {
		BindingID string `json:"workflow_binding_id"`
	}
	e = s.read(name, &saved)
	if e == nil {
		if !validID(saved.BindingID) {
			return "", transfer.ErrIntent
		}
		return saved.BindingID, nil
	}
	if !errors.Is(e, domain.ErrNotFound) {
		return "", e
	}
	saved.BindingID = candidate
	return candidate, s.write(name, saved)
}

func (s *Store) ActiveWorkflow(binding string) (WorkflowJournal, error) {
	if !validID(binding) {
		return WorkflowJournal{}, transfer.ErrIntent
	}
	var saved struct {
		OperationID string `json:"operation_id"`
	}
	if e := s.read(filepath.Join("journals", "workflow-active-"+binding+".json"), &saved); e != nil {
		return WorkflowJournal{}, e
	}
	j, e := s.LoadWorkflow(saved.OperationID)
	if e == nil && (j.Request.BindingID != binding || j.Response == nil) {
		e = transfer.ErrIntent
	}
	return j, e
}
func (s *Store) ActivateWorkflow(j WorkflowJournal) error {
	if e := validateWorkflowJournal(j); e != nil {
		return e
	}
	if j.Response == nil || j.Response.BindingID != j.Request.BindingID || j.Response.LibraryID != j.Identity.LibraryID || !j.Response.Active {
		return transfer.ErrIntent
	}
	return s.write(filepath.Join("journals", "workflow-active-"+j.Request.BindingID+".json"), map[string]string{"operation_id": j.OperationID})
}
