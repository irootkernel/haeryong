// Package sqlite implements the typed current-foundation ports. SQL handles do
// not cross this adapter boundary. One connection serializes short transactions.
package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	_ "modernc.org/sqlite"
)

const SchemaRevision = 3

type Boundary string

const (
	BeforeMigrationCommit      Boundary = "migration_commit"
	BeforeStorageDirectorySync Boundary = "storage_directory_sync"
	BeforeCommit               Boundary = "transaction_commit"
	BeforeSnapshotSync         Boundary = "snapshot_sync"
	BeforeSnapshotObjectCopy   Boundary = "snapshot_object_copy"
	BeforeSnapshotObjectSync   Boundary = "snapshot_object_sync"
)

type Fault func(Boundary) error

type Store struct {
	mu      sync.Mutex
	db      *sql.DB
	profile service.Profile
	fault   Fault
}

func New(fault Fault) *Store { return &Store{fault: fault} }
func (s *Store) boundary(b Boundary) error {
	if s.fault != nil {
		return s.fault(b)
	}
	return nil
}

func checkPrivate(path string, create bool) (bool, error) {
	flags := os.O_RDWR | syscall.O_NOFOLLOW
	created := false
	if create {
		f, err := os.OpenFile(path, flags|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			created = true
			if err = f.Close(); err != nil {
				return false, err
			}
		} else if !errors.Is(err, os.ErrExist) {
			return false, err
		}
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	stat := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != uint32(os.Getuid()) || stat.Nlink != 1 {
		return false, errors.New("database must be a current-user private regular file")
	}
	return created, nil
}

func (s *Store) Prepare(ctx context.Context, p service.Profile) (state service.StorageState, closer func() error, result error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return state, nil, errors.New("store already open")
	}
	path := filepath.Join(p.DataDir, "storage", "library.sqlite")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) && p.StorageState == "pending" {
		entries, err := os.ReadDir(filepath.Dir(path))
		if err != nil {
			return state, nil, err
		}
		if len(entries) != 0 {
			return state, nil, errors.New("pending storage has unknown contents without database; recovery required")
		}
	}
	_, err := checkPrivate(path, p.StorageState == "pending")
	if err != nil {
		return state, nil, fmt.Errorf("storage unavailable; no substitute: %w", err)
	}
	if p.StorageState == "ready" {
		info, err := os.Stat(path)
		if err != nil {
			return state, nil, err
		}
		if info.Size() == 0 {
			return state, nil, errors.New("ready database is zero bytes; recovery required")
		}
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			if _, err := checkPrivate(path+suffix, false); err != nil {
				return state, nil, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return state, nil, err
		}
	}
	uri := url.URL{Scheme: "file", Path: path}
	q := uri.Query()
	q.Set("mode", "rw")
	for _, pragma := range []string{"busy_timeout(5000)", "foreign_keys(ON)", "journal_mode(WAL)", "synchronous(FULL)"} {
		q.Add("_pragma", pragma)
	}
	uri.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return state, nil, err
	}
	s.db = db
	s.profile = p
	defer func() {
		if result != nil {
			db.Close()
			s.db = nil
		}
	}()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	// The driver executes these DSN pragmas on every connection, including a
	// replacement after a driver/idle close; a single connection is not assumed
	// to retain settings forever.
	if err := s.migrate(ctx, p); err != nil {
		return state, nil, err
	}
	if err := s.boundary(BeforeStorageDirectorySync); err != nil {
		return state, nil, err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return state, nil, err
	}
	if _, err := s.health(ctx); err != nil {
		return state, nil, err
	}
	// Local work left running by a dead process is fenced before any new claim.
	if err := s.recoverLocalJobs(ctx); err != nil {
		return state, nil, err
	}

	return service.StorageState{Ready: true, State: "ready"}, s.Close, nil
}

func (s *Store) migrate(ctx context.Context, p service.Profile) error {
	var tables int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
		return err
	}
	revision := 0
	if tables == 0 {
		if p.StorageState != "pending" {
			return errors.New("ready library database is empty; recovery required")
		}
		entries, err := os.ReadDir(filepath.Join(p.DataDir, "storage"))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			switch entry.Name() {
			case "library.sqlite", "library.sqlite-wal", "library.sqlite-shm":
			default:
				return errors.New("empty database has unknown storage contents; recovery required")
			}
		}

	} else {
		var libraryID, profileID string
		if err := s.db.QueryRowContext(ctx, "SELECT library_id,profile_id,schema_revision FROM libraries WHERE singleton=1").Scan(&libraryID, &profileID, &revision); err != nil {
			return fmt.Errorf("unknown or partial schema: %w", err)
		}
		if libraryID != p.LibraryID || profileID != p.ProfileID {
			return errors.New("database library/profile identity mismatch")
		}
		if revision < 1 || revision > SchemaRevision {
			return errors.New("unsupported database schema revision")
		}
		var userVersion int
		if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil || userVersion != revision {
			return errors.New("database schema version mismatch")
		}
		rows, err := s.db.QueryContext(ctx, "SELECT revision,checksum FROM schema_migrations ORDER BY revision")
		if err != nil {
			return err
		}
		count := 0
		for rows.Next() {
			var version int
			var checksum string
			if err := rows.Scan(&version, &checksum); err != nil {
				rows.Close()
				return err
			}
			count++
			if version != count || version > revision || checksum != digest([]byte(migrations[version-1])) {
				rows.Close()
				return errors.New("migration history/checksum mismatch")
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if count != revision {
			return errors.New("incomplete migration history")
		}
		if err := s.validateTables(ctx, revision); err != nil {
			return err
		}
	}
	if revision == SchemaRevision {
		return nil
	}
	if revision > 0 && p.StorageState == "ready" {
		if err := s.recoverySnapshot(ctx, revision); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i := revision; i < SchemaRevision; i++ {
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			return err
		}
		if i == 0 {
			ids := make([]string, 4)
			for index := range ids {
				generated, err := service.NewID()
				if err != nil {
					return err
				}
				ids[index] = generated
			}
			runtimeID, stream, auth, unfiled := ids[0], ids[1], ids[2], ids[3]
			if _, err := tx.ExecContext(ctx, "INSERT INTO libraries VALUES(1,?,?,?,?,?,?,?)", p.LibraryID, p.ProfileID, 1, runtimeID, stream, auth, unfiled); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO projects VALUES(?, 'Unfiled','active',1,1)", unfiled); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO config_revisions VALUES(1,?,'bootstrap',?)", app.DefaultConfiguration(), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return err
			}
		}
		if i == 2 {
			owner, err := service.NewID()
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO auth_owner VALUES(1,?)", owner); err != nil {
				return err
			}
		}

		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations VALUES(?,?,?)", i+1, digest([]byte(migrations[i])), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE libraries SET schema_revision=?", i+1); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", i+1)); err != nil {
			return err
		}
	}
	if err := s.boundary(BeforeMigrationCommit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) recoverySnapshot(ctx context.Context, revision int) error {
	// Retain a coherent local recovery set before any supported state migration.
	// Startup holds the exclusive profile writer lock throughout snapshot/copy.
	// Full backup scheduling, restore and retention remain TASK-005-04/05.
	id, err := service.NewID()
	if err != nil {
		return err
	}
	path := filepath.Join(s.profile.DataDir, "storage", "pre-migration-"+id+".sqlite")
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO '"+strings.ReplaceAll(path, "'", "''")+"'"); err != nil {
		return err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err := s.boundary(BeforeSnapshotSync); err != nil {
		f.Close()
		return err
	}
	err = f.Sync()
	f.Close()
	if err != nil {
		return err
	}
	snapshotURI := url.URL{Scheme: "file", Path: path}
	snapshotQuery := snapshotURI.Query()
	snapshotQuery.Set("mode", "ro")
	snapshotURI.RawQuery = snapshotQuery.Encode()
	snapshot, err := sql.Open("sqlite", snapshotURI.String())
	if err != nil {
		return err
	}
	defer snapshot.Close()
	var check, identity string
	var recorded int
	if err := snapshot.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		return errors.New("premigration snapshot integrity failed")
	}
	foreignKeys, err := snapshot.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	damaged := foreignKeys.Next()
	scanErr := foreignKeys.Err()
	foreignCloseErr := foreignKeys.Close()
	if damaged || scanErr != nil || foreignCloseErr != nil {
		return errors.New("premigration snapshot foreign-key integrity failed")
	}
	if err := snapshot.QueryRowContext(ctx, "SELECT library_id,schema_revision FROM libraries").Scan(&identity, &recorded); err != nil || identity != s.profile.LibraryID || recorded != revision {
		return errors.New("premigration snapshot identity failed")
	}
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, source)
	closeErr := source.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	objectDigest, objectCount, err := s.copyRecoveryObjects(ctx, snapshot, path, revision)
	if err != nil {
		return err
	}
	record, _ := json.Marshal(map[string]any{"complete": true, "library_id": identity, "schema_revision": revision, "sha256": hex.EncodeToString(hash.Sum(nil)), "objects_manifest_sha256": objectDigest, "object_count": objectCount})
	meta, err := os.OpenFile(path+".json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := meta.Write(record); err != nil {
		meta.Close()
		return err
	}
	err = meta.Sync()
	meta.Close()
	if err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}
func (s *Store) Health(ctx context.Context) (domain.Health, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.health(ctx)
}
func (s *Store) health(ctx context.Context) (domain.Health, error) {
	var h domain.Health
	if s.db == nil {
		return h, errors.New("store closed")
	}
	var check string
	if err := s.db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		return h, errors.New("database integrity unavailable")
	}
	row := s.db.QueryRowContext(ctx, "SELECT library_id,profile_id,schema_revision,runtime_generation,stream_epoch,auth_epoch,unfiled_project_id FROM libraries WHERE singleton=1")
	if err := row.Scan(&h.Library.ID, &h.Library.ProfileID, &h.Library.SchemaRevision, &h.Library.RuntimeGeneration, &h.Library.StreamEpoch, &h.Library.AuthEpoch, &h.Library.UnfiledProjectID); err != nil {
		return h, err
	}
	if h.Library.ID != s.profile.LibraryID || h.Library.ProfileID != s.profile.ProfileID {
		return h, errors.New("database identity mismatch")
	}
	for _, item := range []struct {
		query string
		value any
	}{{"SELECT sqlite_version()", &h.Engine}, {"PRAGMA journal_mode", &h.Journal}, {"PRAGMA synchronous", &h.Synchronous}, {"PRAGMA foreign_keys", &h.ForeignKeys}, {"PRAGMA busy_timeout", &h.BusyTimeout}} {
		if err := s.db.QueryRowContext(ctx, item.query).Scan(item.value); err != nil {
			return h, err
		}
	}
	rows, err := s.db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return h, err
	}
	bad := rows.Next()
	scanErr := rows.Err()
	rows.Close()
	if bad || scanErr != nil {
		return h, errors.New("database foreign-key integrity failed")
	}
	return h, nil
}

type writeTx struct {
	tx  *sql.Tx
	ctx context.Context
}

func (s *Store) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("store closed")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(&writeTx{tx: tx, ctx: ctx}); err != nil {
		return err
	}
	if err := s.boundary(BeforeCommit); err != nil {
		return err
	}
	return tx.Commit()
}
func (t *writeTx) exec(query string, args ...any) error {
	_, err := t.tx.ExecContext(t.ctx, query, args...)
	return err
}
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}
func jsonBytes(v any) []byte { b, _ := json.Marshal(v); return b }
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Store) validateTables(ctx context.Context, revision int) error {
	expected := map[string]bool{}
	pattern := regexp.MustCompile(`CREATE TABLE ([a-z_]+)`)
	for _, migration := range migrations[:revision] {
		for _, match := range pattern.FindAllStringSubmatch(migration, -1) {
			expected[match[1]] = true
		}
	}
	rows, err := s.db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if !expected[name] {
			return errors.New("unknown database schema table")
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != len(expected) {
		return errors.New("partial database schema")
	}
	return nil
}
