package foundation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var copyUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// Lock the actual directory inode across verification/restore. Retention uses
// an exclusive nonblocking lock and can only dispose of whole owned sets.
func copyUse(path string, exclusive bool) (func(), error) {
	return copyUseContext(context.Background(), path, exclusive)
}
func copyUseContext(ctx context.Context, path string, exclusive bool) (func(), error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	op := syscall.LOCK_SH
	if exclusive {
		op = syscall.LOCK_EX | syscall.LOCK_NB
	}
	for {
		e = syscall.Flock(int(f.Fd()), op|syscall.LOCK_NB)
		if e == nil {
			break
		}
		if exclusive || (e != syscall.EWOULDBLOCK && e != syscall.EAGAIN) {
			f.Close()
			return nil, e
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

func retentionDuration(raw string) (time.Duration, bool) {
	if raw == "indefinite" || len(raw) < 2 {
		return 0, false
	}
	n, e := strconv.ParseInt(raw[:len(raw)-1], 10, 64)
	if e != nil || n <= 0 {
		return 0, false
	}
	unit := map[byte]time.Duration{'s': time.Second, 'm': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[raw[len(raw)-1]]
	if unit == 0 || n > int64((1<<63-1)/unit) {
		return 0, false
	}
	return time.Duration(n) * unit, true
}

// Retention is opportunistic after a new usable complete set. Failures retain
// bytes; they cannot invalidate the newly completed job or delete its fallback.
func (s *Store) retainBackups(ctx context.Context, keep string) {
	config, e := s.Configuration(ctx)
	if e != nil {
		return
	}
	var v struct {
		Retention struct {
			Backups string `json:"backups"`
		} `json:"retention"`
	}
	if json.Unmarshal(config.Value, &v) != nil {
		return
	}
	age, enabled := retentionDuration(v.Retention.Backups)
	if !enabled {
		return
	}
	after := ""
	for {
		var sets []domain.CopySet
		e = s.Write(ctx, func(tx app.WriteTx) error { var e error; sets, e = tx.CopySets("backup", after, 64); return e })
		if e != nil || len(sets) == 0 {
			return
		}
		for _, set := range sets {
			after = set.ID
			stamp, e := time.Parse(time.RFC3339Nano, set.CompletedAt)
			if e != nil || set.ID == keep || set.Stage != "complete" || time.Since(stamp) < age {
				continue
			}
			if filepath.Clean(set.Directory) != filepath.Join(set.Destination, set.ID) || !copyUUID.MatchString(set.ID) {
				continue
			}
			if s.ownedBackup(ctx, set) != nil {
				continue
			}
			release, e := copyUse(set.Directory, true)
			if e != nil {
				continue
			}
			// Verify without reacquiring the directory lock we hold exclusively.
			if e = s.copyFault("retention_verify"); e != nil {
				release()
				return
			}
			m, e := verifyBackup(ctx, set.Directory)
			if e == nil && m.SetID == set.ID && m.LibraryID == set.LibraryID && m.OriginProfileID == s.CopyProfile().ProfileID && m.SnapshotSHA == set.SnapshotSHA {
				// Keep is rechecked immediately before deletion; no partial replacement
				// can remove the last usable verified complete set.
				var current domain.CopySet
				e = s.Write(ctx, func(tx app.WriteTx) error { var e error; current, e = tx.CopySet(keep); return e })
				if e == nil {
					e = s.ownedBackup(ctx, current)
				}
				if e == nil {
					verified, err := VerifyBackup(ctx, current.Directory)
					e = err
					if e == nil && (verified.SetID != current.ID || verified.LibraryID != current.LibraryID || verified.OriginProfileID != s.CopyProfile().ProfileID || verified.SnapshotSHA != current.SnapshotSHA) {
						e = domain.ErrContentIntegrity
					}
				}
				if e == nil {
					e = s.ownedBackup(ctx, set)
				}
				if e == nil {
					e = os.RemoveAll(set.Directory)
				}
				if e == nil {
					e = copySyncDirectory(set.Destination)
				}
				if e == nil {
					set.Stage = "expired"
					_ = s.Write(ctx, func(tx app.WriteTx) error { return tx.PutCopySet(set) })
				}
			}
			release()
		}
	}
}

// Restored rows are historical observations. Even same-profile restoration
// rotates runtime and does not adopt the origin output for local deletion.
func (s *Store) ownedBackup(ctx context.Context, set domain.CopySet) error {
	return s.Write(ctx, func(tx app.WriteTx) error {
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		if owner.OwnerID != set.OwnerID {
			return domain.ErrDenied
		}
		current, e := tx.CopySet(set.ID)
		if e != nil {
			return e
		}
		if set.Kind != "backup" || current.Runtime != lib.RuntimeGeneration || set.Runtime != lib.RuntimeGeneration || set.LibraryID != lib.ID || lib.ProfileID != s.CopyProfile().ProfileID || current.Directory != set.Directory || current.Stage != "complete" {
			return domain.ErrDenied
		}
		return nil
	})
}

func (s *Store) AbandonCopy(set domain.CopySet) error {
	if set.Directory == "" {
		return nil
	}
	base := set.Destination
	if set.Kind == "export" {
		base = filepath.Dir(set.Destination)
	}
	expected := filepath.Join(base, "."+set.ID+".incomplete")
	if (set.Kind != "backup" && set.Kind != "export") || !copyUUID.MatchString(set.ID) || set.Directory != expected {
		return domain.ErrInvalidTarget
	}
	// Only this terminal set's recorded incomplete output may be discarded.
	if e := os.RemoveAll(expected); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return copySyncDirectory(base)
}
