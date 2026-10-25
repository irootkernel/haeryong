package clientstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/domain"
)

// CommandIntent identifies the caller's pending command before local file reads
// or fresh default capture. Request bytes and resolved defaults remain in the
// operation journal; this index never replaces those original semantics.
type CommandIntent struct {
	CopyAction       string `json:"copy_action,omitempty"`
	Method           string `json:"method"`
	Target           string `json:"target"`
	SourcePath       string `json:"source_path,omitempty"`
	ProjectID        string `json:"explicit_project_id,omitempty"`
	SourceID         string `json:"explicit_source_id,omitempty"`
	ExpectedRevision string `json:"explicit_expected_revision,omitempty"`
}

func commandKey(identity transfer.Identity, intent CommandIntent) (string, error) {
	if identity.LibraryID == "" || identity.PrincipalID == "" || intent.Method == "" || intent.Method != strings.ToUpper(intent.Method) || (!strings.HasPrefix(intent.Target, "/api/v1/") && intent.Target != "/local/v1/copy-requests") || strings.ContainsAny(intent.Target, "\r\n") {
		return "", fmt.Errorf("invalid pending command binding")
	}
	if intent.CopyAction != "" {
		if intent.Target != "/local/v1/copy-requests" || intent.Method != "POST" {
			return "", fmt.Errorf("invalid copy command binding")
		}
		switch intent.CopyAction {
		case "backup.create", "backup.resume", "backup.abandon", "export.plan", "export.create":
		default:
			return "", fmt.Errorf("invalid copy command action")
		}
	}
	if intent.SourcePath != "" {
		var e error
		intent.SourcePath, e = filepath.Abs(intent.SourcePath)
		if e != nil {
			return "", e
		}
	}
	b, e := json.Marshal([]any{"command-v1", identity.LibraryID, identity.PrincipalID, intent})
	if e != nil {
		return "", e
	}
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:]), nil
}

func (s *Store) UseCommandPending(ctx context.Context, identity transfer.Identity, intent CommandIntent, candidate string) (string, error) {
	if !validID(candidate) {
		return "", fmt.Errorf("invalid pending operation identity")
	}
	key, e := commandKey(identity, intent)
	if e != nil {
		return "", e
	}
	release, e := s.Lock(ctx, pendingLockID(key))
	if e != nil {
		return "", e
	}
	defer release()
	var saved struct {
		ID string `json:"operation_id"`
	}
	e = s.read(filepath.Join("pending", key+".json"), &saved)
	if e == nil {
		if !validID(saved.ID) {
			return "", fmt.Errorf("invalid pending operation identity")
		}
		return saved.ID, nil
	}
	if !errors.Is(e, domain.ErrNotFound) {
		return "", e
	}
	saved.ID = candidate
	if e = s.write(filepath.Join("pending", key+".json"), saved); e != nil {
		return "", e
	}
	return candidate, nil
}

// FinishCommandPending removes only the acknowledged command's own index.
// The durable operation journal and original response remain available.
func (s *Store) FinishCommandPending(ctx context.Context, identity transfer.Identity, intent CommandIntent, operation string) error {
	key, e := commandKey(identity, intent)
	if e != nil {
		return e
	}
	release, e := s.Lock(ctx, pendingLockID(key))
	if e != nil {
		return e
	}
	defer release()
	var saved struct {
		ID string `json:"operation_id"`
	}
	name := filepath.Join("pending", key+".json")
	if e = s.read(name, &saved); errors.Is(e, domain.ErrNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	if saved.ID != operation {
		return transfer.ErrIntent
	}
	if e = os.Remove(filepath.Join(s.Root, name)); e != nil {
		return e
	}
	return syncDir(filepath.Join(s.Root, "pending"))
}
