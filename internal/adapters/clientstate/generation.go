package clientstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"

	"haeryong.local/haeryong/internal/application/transfer"
)

// GenerationGrantInput serializes only the trusted launcher's scope selection.
// The lease covers its read by the existing issuer command. No bearer or prompt
// is written here; the existing workflow journal owns credential recovery.
func (s *Store) GenerationGrantInput(ctx context.Context, identity transfer.Identity, project string, value any) (string, func() error, error) {
	if identity.LibraryID == "" || identity.PrincipalID == "" || !validID(project) {
		return "", nil, transfer.ErrIntent
	}
	h := sha256.Sum256([]byte("generation-grant-v1\n" + identity.LibraryID + "\n" + identity.PrincipalID + "\n" + project))
	key := hex.EncodeToString(h[:])
	release, e := s.Lock(ctx, pendingLockID(key))
	if e != nil {
		return "", nil, e
	}
	name := filepath.Join("journals", "generation-selection-"+key+".json")
	if e = s.write(name, value); e != nil {
		_ = release()
		return "", nil, e
	}
	return filepath.Join(s.Root, name), release, nil
}
