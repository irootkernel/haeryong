package foundation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"haeryong.local/haeryong/internal/adapters/sqlite"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func (s *Store) CopyDestination(path string) (string, error) {
	abs, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(abs))
	if e != nil {
		return "", e
	}
	abs = filepath.Join(parent, filepath.Base(abs))
	if info, e := os.Lstat(abs); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("copy destination is a link")
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	profile := s.CopyProfile().DataDir
	if abs == profile || strings.HasPrefix(abs, profile+string(os.PathSeparator)) {
		return "", errors.New("copy destination overlaps live profile")
	}
	return abs, nil
}
func copySyncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	return errors.Join(f.Sync(), f.Close())
}
func copyHash(path string) (string, int64, error) { return copyHashContext(context.Background(), path) }
func copyHashContext(ctx context.Context, path string) (string, int64, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return "", 0, e
	}
	h := sha256.New()
	n, e := copyStream(ctx, h, f)
	e = errors.Join(e, f.Close())
	return hex.EncodeToString(h.Sum(nil)), n, e
}
func copyJSON(path string, value any) error { return copyJSONWithFault(path, value, nil) }

func copyRecordSync(path string, fault func(string) error) error {
	if fault != nil {
		if e := fault("record_parent_sync:" + filepath.Base(path)); e != nil {
			return e
		}
	}
	return copySyncDirectory(filepath.Dir(path))
}

func copyJSONWithFault(path string, value any, fault func(string) error) error {
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".copy-record-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	_, e = f.Write(raw)
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	if e = os.Rename(name, path); e != nil {
		return e
	}
	return copyRecordSync(path, fault)
}

type backupManifest struct {
	Toolchain         string `json:"go_toolchain"`
	OriginProfileID   string `json:"origin_profile_id"`
	SchemaFingerprint string `json:"migration_sha256"`
	Format            int    `json:"format_version"`
	SetID             string `json:"set_id"`
	LibraryID         string `json:"library_id"`
	Schema            int    `json:"schema_revision"`
	SnapshotSHA       string `json:"database_sha256"`
	InventorySHA      string `json:"inventory_sha256"`
	ObjectCount       int64  `json:"object_count"`
	CreatedAt         string `json:"created_at"`
	SourceLimitation  string `json:"source_limitation"`
}
type backupObject struct {
	Hash string `json:"sha256"`
	Size int64  `json:"bytes"`
}

func (s *Store) ProcessCopy(ctx context.Context, id string) (result error) {
	var set domain.CopySet
	var job domain.Job
	var scope []string
	e := s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		job, e = tx.Job(id)
		if e != nil {
			return e
		}
		var scope struct {
			ID string `json:"copy_set_id"`
		}
		if json.Unmarshal(job.Scope, &scope) != nil || scope.ID == "" {
			return domain.ErrInvalidTarget
		}
		set, e = tx.CopySet(scope.ID)
		if e != nil {
			return e
		}
		if set.JobID != job.ID || set.Stage == "complete" || set.Stage == "abandoned" || set.Stage == "expired" || set.Stage == "restored_paused" {
			return domain.ErrStaleWorker
		}
		if job.State == "cancelled" || job.State == "failed" || job.State == "completed" {
			return domain.ErrStaleWorker
		}
		if _, e = app.CopyOwner(tx, set, job.ID, job.AttemptGeneration); e != nil {
			return e
		}
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		hold, e := tx.RecoveryHold()
		if e != nil {
			return e
		}
		if hold != nil && hold.State != "reviewed" || set.Runtime != lib.RuntimeGeneration {
			return domain.ErrReconciliation
		}
		if job.State == "interrupted" {
			if e = tx.ReconcileJob(id); e != nil {
				return e
			}
			job, e = tx.Job(id)
			if e != nil {
				return e
			}
		}
		if job.CancelRequested {
			if job.State != "cancelled" {
				if e = tx.ConfirmStop(id, job.AttemptGeneration); e != nil {
					return e
				}
			}
			return nil
		}
		if job.State == "queued" {
			job, e = tx.ClaimJob(id)
			return e
		}
		if job.State != "running" {
			return domain.ErrTransition
		}
		return nil
	})
	if e != nil {
		return e
	}
	if job.CancelRequested {
		return nil
	}
	defer func() {
		if result != nil && ctx.Err() == nil && !errors.Is(result, domain.ErrStaleWorker) {
			persistErr := s.Write(context.Background(), func(tx app.WriteTx) error {
				code := "copy_io_failure"
				switch {
				case errors.Is(result, syscall.ENOSPC):
					code = "destination_full"
				case errors.Is(result, domain.ErrContentIntegrity):
					code = "copy_integrity_failure"
				case errors.Is(result, os.ErrNotExist):
					code = "copy_input_or_destination_unavailable"
				case errors.Is(result, domain.ErrDenied):
					code = "copy_authority_changed"
				case errors.Is(result, domain.ErrReconciliation):
					code = "copy_reconciliation_required"
				}
				return app.FailCopy(tx, set, job.ID, job.AttemptGeneration, code)
			})
			if persistErr != nil && !errors.Is(persistErr, domain.ErrStaleWorker) {
				result = errors.Join(result, persistErr)
			}
		}
	}()
	physical, e := s.CopyDestination(set.Destination)
	if e != nil {
		return e
	}
	if physical != set.Destination {
		return domain.ErrConflict
	}
	if set.Kind == "export" {
		scope = []string{set.ID}
	}
	if set.Stage == "queued" || set.Stage == "capturing" {
		// Lock order matches Collect. Drain existing collectors, persist the
		// barrier, then release coordination for the long database copy.
		s.gcMu.Lock()
		e = s.Write(ctx, func(tx app.WriteTx) error {
			var e error
			set, e = tx.CopySet(set.ID)
			if e != nil {
				return e
			}
			if e := app.GuardCopy(tx, set, job.ID, job.AttemptGeneration); e != nil {
				return e
			}
			set.Capturing = true
			set.Stage = "capturing"
			if set.Directory == "" {
				base := set.Destination
				if set.Kind == "export" {
					base = filepath.Dir(set.Destination)
				}
				set.Directory = filepath.Join(base, "."+set.ID+".incomplete")
				set.Snapshot = filepath.Join(set.Directory, "database.sqlite")
			}
			return tx.PutCopySet(set)
		})
		s.gcMu.Unlock()
		if e != nil {
			return e
		}
		if e = s.copyFault("capture_barrier"); e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Join(set.Directory, "objects"), 0700); e != nil {
			return e
		}
		if _, e = os.Stat(set.Snapshot); errors.Is(e, os.ErrNotExist) {
			temp, err := os.CreateTemp(set.Directory, ".snapshot-")
			if err != nil {
				return err
			}
			name := temp.Name()
			temp.Close()
			os.Remove(name)
			defer os.Remove(name)
			if e = s.SnapshotDatabase(ctx, name); e != nil {
				return e
			}
			f, err := os.OpenFile(name, os.O_RDWR, 0600)
			if err != nil {
				return err
			}
			e = errors.Join(f.Sync(), f.Close())
			if e != nil {
				return e
			}
			if e = os.Rename(name, set.Snapshot); e != nil {
				return e
			}
			if e = copySyncDirectory(set.Directory); e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
		if e = os.Chmod(set.Snapshot, 0600); e != nil {
			return e
		}
		if e = sqlite.CheckCopyDatabase(ctx, set.Snapshot); e != nil {
			return e
		}
		if e = s.copyFault("snapshot"); e != nil {
			return e
		}
		if e = s.InstallCopyPins(ctx, set.ID, set.Snapshot, func(tx app.WriteTx) error { return app.GuardCopy(tx, set, job.ID, job.AttemptGeneration) }, scope...); e != nil {
			return e
		}
		if e = s.copyFault("pins"); e != nil {
			return e
		}
		if e = sqlite.SanitizeCopyDatabase(ctx, set.Snapshot); e != nil {
			return e
		}
		if e = sqlite.CheckCopyDatabase(ctx, set.Snapshot); e != nil {
			return e
		}
		if e = s.Write(ctx, func(tx app.WriteTx) error {
			if e := app.GuardCopy(tx, set, job.ID, job.AttemptGeneration); e != nil {
				return e
			}
			set.Capturing = false
			set.Stage = "copying"
			return tx.PutCopySet(set)
		}); e != nil {
			return e
		}
		return nil
	}
	if set.Stage != "copying" && set.Stage != "finalizing" && set.Stage != "publishing" {
		return domain.ErrTransition
	}
	if set.Kind == "export" && set.Stage == "publishing" {
		return s.finishExport(ctx, set, job)
	}
	if set.Kind == "backup" {
		final := filepath.Join(set.Destination, set.ID)
		if _, err := os.Lstat(final); err == nil {
			m, err := VerifyBackup(ctx, final)
			if err != nil {
				return err
			}
			return s.completeBackup(ctx, set, job, m, false)
		}
	}
	objects, e := sqlite.CopyObjectPage(ctx, set.Snapshot, set.AfterHash, scope...)
	if e != nil {
		return e
	}
	if len(objects) > 0 {
		o := objects[0]
		source, e := os.OpenFile(filepath.Join(s.CopyProfile().DataDir, "storage", "objects", o.Hash), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if e != nil {
			return e
		}
		defer source.Close()
		output := filepath.Join(set.Directory, "objects", o.Hash)
		f, e := os.OpenFile(output, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
		if e != nil {
			return e
		}
		defer f.Close()
		if set.Offset > 0 {
			a, b := sha256.New(), sha256.New()
			n, err := io.CopyN(a, contextCopyReader{ctx, source}, set.Offset)
			if err != nil || n != set.Offset {
				return domain.ErrContentIntegrity
			}
			n, err = io.CopyN(b, contextCopyReader{ctx, f}, set.Offset)
			if err != nil || n != set.Offset || !strings.EqualFold(hex.EncodeToString(a.Sum(nil)), hex.EncodeToString(b.Sum(nil))) {
				return domain.ErrContentIntegrity
			}
		}
		if e = f.Truncate(set.Offset); e != nil {
			return e
		}
		if _, e = source.Seek(set.Offset, io.SeekStart); e != nil {
			return e
		}
		if _, e = f.Seek(set.Offset, io.SeekStart); e != nil {
			return e
		}
		n, e := io.CopyN(f, contextCopyReader{ctx, source}, min(int64(1024*1024), o.Size-set.Offset))
		if e != nil && !(errors.Is(e, io.EOF) && set.Offset+n == o.Size) {
			return e
		}
		if e = s.copyFault("object_sync"); e != nil {
			return e
		}
		if e = f.Sync(); e != nil {
			return e
		}
		if e = s.copyFault("object_parent_sync"); e != nil {
			return e
		}
		if e = copySyncDirectory(filepath.Dir(output)); e != nil {
			return e
		}
		set.Offset += n
		if set.Offset == o.Size {
			hash, size, e := copyHashContext(ctx, output)
			if e != nil {
				return e
			}
			if hash != o.Hash || size != o.Size {
				return domain.ErrContentIntegrity
			}
			set.AfterHash = o.Hash
			set.Offset = 0
			set.ObjectCount++
		}
		return s.Write(ctx, func(tx app.WriteTx) error {
			j, e := tx.Job(id)
			if e != nil {
				return e
			}
			if j.CancelRequested || j.AttemptGeneration != job.AttemptGeneration {
				return domain.ErrStaleWorker
			}
			if e := app.GuardCopy(tx, set, job.ID, job.AttemptGeneration); e != nil {
				return e
			}
			return tx.PutCopySet(set)
		})
	}
	if set.Kind == "export" {
		return s.finishExport(ctx, set, job)
	}
	manifest, e := s.finalizeBackup(ctx, set)
	if e != nil {
		return e
	}
	if e = s.copyFault("backup_verified"); e != nil {
		return e
	}
	if e = s.completeBackup(ctx, set, job, manifest, true); e != nil {
		return e
	}
	s.retainBackups(ctx, set.ID)
	return nil
}

func (s *Store) completeBackup(ctx context.Context, set domain.CopySet, job domain.Job, manifest backupManifest, publish bool) error {
	if manifest.SetID != set.ID || manifest.LibraryID != set.LibraryID || manifest.OriginProfileID != s.CopyProfile().ProfileID {
		return domain.ErrContentIntegrity
	}
	return s.Write(ctx, func(tx app.WriteTx) error {
		if e := app.GuardCopy(tx, set, job.ID, job.AttemptGeneration); e != nil {
			return e
		}
		return tx.FinishJob(job.ID, job.AttemptGeneration, "", func(tx app.WriteTx) error {
			if publish {
				if e := s.copyFault("publish"); e != nil {
					return e
				}
				if _, e := os.Lstat(filepath.Join(set.Destination, set.ID)); !errors.Is(e, os.ErrNotExist) {
					return domain.ErrConflict
				}
				if e := copyJSON(filepath.Join(set.Directory, "COMPLETE.json"), manifest); e != nil {
					return e
				}
				if e := os.Rename(set.Directory, filepath.Join(set.Destination, set.ID)); e != nil {
					return e
				}
				if e := s.copyFault("after_rename"); e != nil {
					return e
				}
			}
			// A recovered rename may have failed before its parent was synced.
			// Re-establish that durability even when publication already exists.
			if e := s.copyFault("backup_parent_sync"); e != nil {
				return e
			}
			if e := copySyncDirectory(set.Destination); e != nil {
				return e
			}
			if e := s.copyFault("backup_destination_parent_sync"); e != nil {
				return e
			}
			if e := copySyncDirectory(filepath.Dir(set.Destination)); e != nil {
				return e
			}
			set.Directory = filepath.Join(set.Destination, set.ID)
			set.Snapshot = filepath.Join(set.Directory, "database.sqlite")
			set.Stage = "complete"
			set.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			set.VerifiedAt = set.CompletedAt
			set.SnapshotSHA = manifest.SnapshotSHA
			set.InventorySHA = manifest.InventorySHA
			if e := tx.PutCopySet(set); e != nil {
				return e
			}
			return tx.ReleaseOwnerPins("backup", set.ID)
		})
	})
}

func (s *Store) finalizeBackup(ctx context.Context, set domain.CopySet) (backupManifest, error) {
	var m backupManifest
	if e := s.copyFault("backup_final_verification"); e != nil {
		return m, e
	}
	for _, pattern := range []string{".snapshot-*", ".copy-record-*"} {
		names, e := filepath.Glob(filepath.Join(set.Directory, pattern))
		if e != nil {
			return m, e
		}
		for _, name := range names {
			if e = os.Remove(name); e != nil {
				return m, e
			}
		}
	}

	f, e := os.OpenFile(filepath.Join(set.Directory, "inventory.jsonl"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return m, e
	}
	enc := json.NewEncoder(f)
	after := ""
	for {
		objects, e := sqlite.CopyObjectPage(ctx, set.Snapshot, after)
		if e != nil {
			f.Close()
			return m, e
		}
		if len(objects) == 0 {
			break
		}
		for _, o := range objects {
			hash, size, e := copyHashContext(ctx, filepath.Join(set.Directory, "objects", o.Hash))
			if e != nil || hash != o.Hash || size != o.Size {
				f.Close()
				return m, errors.New("backup object verification failed")
			}
			if e = enc.Encode(backupObject{o.Hash, o.Size}); e != nil {
				f.Close()
				return m, e
			}
			after = o.Hash
		}
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return m, e
	}
	if e = f.Close(); e != nil {
		return m, e
	}
	dbhash, _, e := copyHashContext(ctx, set.Snapshot)
	if e != nil {
		return m, e
	}
	invhash, _, e := copyHashContext(ctx, filepath.Join(set.Directory, "inventory.jsonl"))
	if e != nil {
		return m, e
	}
	m = backupManifest{Toolchain: runtime.Version(), OriginProfileID: s.CopyProfile().ProfileID, SchemaFingerprint: sqlite.CopySchemaFingerprint(), Format: 1, SetID: set.ID, LibraryID: set.LibraryID, Schema: sqlite.SchemaRevision, SnapshotSHA: dbhash, InventorySHA: invhash, ObjectCount: set.ObjectCount, CreatedAt: set.CreatedAt, SourceLimitation: "reference_only_sources_not_copied"}
	if e = copyJSON(filepath.Join(set.Directory, "manifest.json"), m); e != nil {
		return m, e
	}
	if e = copySyncDirectory(filepath.Join(set.Directory, "objects")); e != nil {
		return m, e
	}
	if e = copySyncDirectory(set.Directory); e != nil {
		return m, e
	}
	return m, nil
}

func VerifyBackup(ctx context.Context, path string) (backupManifest, error) {
	release, e := copyUseContext(ctx, path, false)
	if e != nil {
		return backupManifest{}, e
	}
	defer release()
	return verifyBackup(ctx, path)
}
func verifyBackup(ctx context.Context, path string) (backupManifest, error) {
	actualObjects, e := validateBackupTree(ctx, path)
	if e != nil {
		return backupManifest{}, e
	}
	var m, marker backupManifest
	raw, e := os.ReadFile(filepath.Join(path, "manifest.json"))
	if e != nil {
		return m, e
	}
	if e = json.Unmarshal(raw, &m); e != nil {
		return m, e
	}
	raw, e = os.ReadFile(filepath.Join(path, "COMPLETE.json"))
	if e != nil {
		return m, e
	}
	if e = json.Unmarshal(raw, &marker); e != nil {
		return m, e
	}
	if m != marker || m.Format != 1 || m.Schema != sqlite.SchemaRevision || m.SchemaFingerprint != sqlite.CopySchemaFingerprint() || filepath.Base(filepath.Clean(path)) != m.SetID {
		return m, errors.New("backup completion/format mismatch")
	}
	db := filepath.Join(path, "database.sqlite")
	hash, _, e := copyHashContext(ctx, db)
	if e != nil || hash != m.SnapshotSHA {
		return m, errors.New("backup database checksum mismatch")
	}
	hash, _, e = copyHashContext(ctx, filepath.Join(path, "inventory.jsonl"))
	if e != nil || hash != m.InventorySHA {
		return m, errors.New("backup inventory checksum mismatch")
	}
	if e = sqlite.CheckCopyDatabase(ctx, db); e != nil {
		return m, e
	}
	library, e := sqlite.CopyLibrary(ctx, db)
	if e != nil {
		return m, e
	}
	if library.ID != m.LibraryID || library.ProfileID != m.OriginProfileID || library.SchemaRevision != m.Schema || !copyUUID.MatchString(m.SetID) {
		return m, domain.ErrContentIntegrity
	}
	after := ""
	count := int64(0)
	expected := sha256.New()
	inventory := json.NewEncoder(expected)
	for {
		objects, e := sqlite.CopyObjectPage(ctx, db, after)
		if e != nil {
			return m, e
		}
		if len(objects) == 0 {
			break
		}
		for _, o := range objects {
			hash, size, e := copyHashContext(ctx, filepath.Join(path, "objects", o.Hash))
			if e != nil || hash != o.Hash || size != o.Size {
				return m, errors.New("backup object checksum mismatch")
			}
			if e = inventory.Encode(backupObject{o.Hash, o.Size}); e != nil {
				return m, e
			}
			after = o.Hash
			count++
		}
	}
	if actualObjects != m.ObjectCount || count != m.ObjectCount || hex.EncodeToString(expected.Sum(nil)) != m.InventorySHA {
		return m, errors.New("backup object inventory count mismatch")
	}
	return m, nil
}
func (s *Store) BackupStatus(ctx context.Context) (out domain.BackupStatus, e error) {
	e = s.Write(ctx, func(tx app.WriteTx) error { var e error; out, e = tx.CopyBackupStatus(); return e })
	return
}

func (s *Store) copyFault(phase string) error {
	if s.CopyFault != nil {
		return s.CopyFault(phase)
	}
	return nil
}

func validateBackupTree(ctx context.Context, root string) (int64, error) {
	info, e := os.Lstat(root)
	if e != nil {
		return 0, e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0, domain.ErrContentIntegrity
	}
	directory, e := os.Open(root)
	if e != nil {
		return 0, e
	}
	defer directory.Close()
	var count int64
	hasObjects := false
	for {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		entries, e := directory.ReadDir(64)
		if e != nil && e != io.EOF {
			return 0, e
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				return 0, domain.ErrContentIntegrity
			}
			name := entry.Name()
			if name == "objects" && entry.IsDir() {
				hasObjects = true
				objects, e := os.Open(filepath.Join(root, name))
				if e != nil {
					return 0, e
				}
				for {
					if ctx.Err() != nil {
						objects.Close()
						return 0, ctx.Err()
					}
					batch, e := objects.ReadDir(64)
					if e != nil && e != io.EOF {
						objects.Close()
						return 0, e
					}
					for _, o := range batch {
						if !o.Type().IsRegular() || len(o.Name()) != 64 {
							objects.Close()
							return 0, domain.ErrContentIntegrity
						}
						if _, e = hex.DecodeString(o.Name()); e != nil {
							objects.Close()
							return 0, e
						}
						count++
					}
					if e == io.EOF {
						break
					}
				}
				if e = objects.Close(); e != nil {
					return 0, e
				}
				continue
			}
			if !entry.Type().IsRegular() {
				return 0, domain.ErrContentIntegrity
			}
			switch name {
			case "database.sqlite", "inventory.jsonl", "manifest.json", "COMPLETE.json":
			default:
				return 0, domain.ErrContentIntegrity
			}
		}
		if e == io.EOF {
			break
		}
	}
	if !hasObjects {
		return 0, domain.ErrContentIntegrity
	}
	return count, nil
}
