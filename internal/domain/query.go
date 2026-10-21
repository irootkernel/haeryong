package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// ErrContentIntegrity means stored bytes failed their immutable identity check.
// Submitted-input validation has separate package/hash errors.
var ErrContentIntegrity = errors.New("stored content integrity failure")

// Query rows retain the durable list ordering key independently of the item's
// semantic revision and validator.
type QueryProject struct {
	Project
	UpdatedAt string
}

// ProjectView is the one shared wire projection and validator for projects
// (S25): registration responses and query reads derive identical fields.
func ProjectView(p QueryProject) map[string]any {
	return map[string]any{"project_id": p.ID, "name": p.Name, "lifecycle": p.Lifecycle, "revision": p.Revision, "permissions_revision": p.PermissionsRevision, "updated_at": p.UpdatedAt}
}

// ProjectETag hashes the shared projection: registration's 201 validator
// equals the query read's validator for the same state.
func ProjectETag(p QueryProject) string {
	body, _ := json.Marshal(ProjectView(p))
	sum := sha256.Sum256(body)
	return `"sha256-` + hex.EncodeToString(sum[:]) + `"`
}

type QueryDocument struct {
	Document
	UpdatedAt string
}
