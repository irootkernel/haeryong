package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"path/filepath"
	"strings"

	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// SnapshotDatabase never holds the live Store mutex while SQLite copies pages.
// The caller has already persisted its capture barrier and drained collection.
func (s *Store) SnapshotDatabase(ctx context.Context, destination string) error {
	u := url.URL{Scheme: "file", Path: filepath.Join(s.profile.DataDir, "storage", "library.sqlite")}
	q := u.Query()
	q.Set("mode", "rw")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, e = db.ExecContext(ctx, `VACUUM INTO ?`, destination)
	return e
}
func (s *Store) CopyProfile() service.Profile { return s.profile }

const requiredCopyHashes = `SELECT content_hash AS hash FROM object_pins
UNION SELECT content_hash FROM object_references
UNION SELECT content_hash FROM version_entries WHERE content_hash IS NOT NULL
UNION SELECT content_hash FROM contexts
UNION SELECT metadata_hash FROM metadata_indexes
UNION SELECT manifest_sha256 FROM document_versions WHERE manifest_sha256!=''
UNION SELECT supplied_metadata_hash FROM document_versions WHERE supplied_metadata_hash IS NOT NULL AND supplied_metadata_hash!=''`

func openCopy(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "rw")
	q.Add("_pragma", "foreign_keys(ON)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e == nil {
		db.SetMaxOpenConns(1)
	}
	return db, e
}
func CheckCopyDatabase(ctx context.Context, path string) error {
	db, e := openCopy(path)
	if e != nil {
		return e
	}
	defer db.Close()
	if e = (&Store{db: db}).validateTables(ctx, SchemaRevision); e != nil {
		return e
	}
	var check string
	if e = db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&check); e != nil {
		return e
	}
	if check != "ok" {
		return errors.New("copy database integrity failed")
	}
	rows, e := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if e != nil {
		return e
	}
	bad := rows.Next()
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if bad {
		return errors.New("copy database foreign key integrity failed")
	}
	var absent int
	if e = db.QueryRowContext(ctx, `WITH required AS (`+requiredCopyHashes+`) SELECT count(*) FROM required r LEFT JOIN content_objects o ON o.content_hash=r.hash WHERE o.content_hash IS NULL OR o.state!='ready'`).Scan(&absent); e != nil {
		return e
	}
	if absent > 0 {
		return errors.New("copy required object metadata missing or deleting")
	}
	return nil
}
func SanitizeCopyDatabase(ctx context.Context, path string) error {
	db, e := openCopy(path)
	if e != nil {
		return e
	}
	defer db.Close()
	// Owner IDs, historical actor IDs and nonsecret receipts remain ordinary data.
	// Raw profile/control/provider secrets are never copied into this database.
	_, e = db.ExecContext(ctx, `BEGIN;DELETE FROM device_sessions;DELETE FROM pairings;DELETE FROM scoped_credentials;COMMIT;VACUUM`)
	return e
}
func CopyObjectPage(ctx context.Context, path, after string, scope ...string) ([]domain.Object, error) {
	db, e := openCopy(path)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	required := requiredCopyHashes
	args := []any{after}
	if len(scope) > 0 && scope[0] != "" {
		required = scopedCopyHashes
		args = []any{scope[0], after}
	}
	var missing int
	checkArgs := []any{}
	if len(scope) > 0 && scope[0] != "" {
		checkArgs = append(checkArgs, scope[0])
	}
	if e = db.QueryRowContext(ctx, `WITH required AS (`+required+`) SELECT count(*) FROM required r LEFT JOIN content_objects o ON o.content_hash=r.hash WHERE o.content_hash IS NULL OR o.state!='ready'`, checkArgs...).Scan(&missing); e != nil {
		return nil, e
	}
	if missing > 0 {
		return nil, domain.ErrContentIntegrity
	}
	rows, e := db.QueryContext(ctx, `WITH required AS (`+required+`) SELECT o.content_hash,o.byte_size,o.state FROM required r JOIN content_objects o ON o.content_hash=r.hash WHERE o.content_hash>? ORDER BY o.content_hash LIMIT 64`, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Object{}
	for rows.Next() {
		var o domain.Object
		if e = rows.Scan(&o.Hash, &o.Size, &o.State); e != nil {
			return nil, e
		}
		if len(o.Hash) != 64 || o.Size < 0 {
			return nil, domain.ErrContentIntegrity
		}
		if _, e = hex.DecodeString(o.Hash); e != nil {
			return nil, domain.ErrContentIntegrity
		}
		if o.State != "ready" {
			return nil, domain.ErrDeleting
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// InstallCopyPins reads the immutable snapshot in bounded pages. Every live
// ownership transition still goes through the ordinary serialized pin gate.
func (s *Store) InstallCopyPins(ctx context.Context, id, path string, guard func(app.WriteTx) error, scope ...string) error {
	if e := CheckCopyDatabase(ctx, path); e != nil {
		return e
	}
	after := ""
	for {
		objects, e := CopyObjectPage(ctx, path, after, scope...)
		if e != nil {
			return e
		}
		if len(objects) == 0 {
			return nil
		}
		if e = s.Write(ctx, func(tx app.WriteTx) error {
			if guard != nil {
				if e := guard(tx); e != nil {
					return e
				}
			}
			for _, o := range objects {
				if e := tx.AcquirePin(domain.Pin{ID: "backup:" + id + ":" + o.Hash, Kind: "backup", OwnerID: id, Hash: o.Hash}); e != nil {
					return e
				}
			}
			return nil
		}); e != nil {
			return e
		}
		after = objects[len(objects)-1].Hash
	}
}

func CopySchemaFingerprint() string { return digest([]byte(strings.Join(migrations, "\n"))) }

func CopyLibrary(ctx context.Context, path string) (domain.Library, error) {
	db, e := openCopy(path)
	if e != nil {
		return domain.Library{}, e
	}
	defer db.Close()
	var l domain.Library
	e = db.QueryRowContext(ctx, `SELECT library_id,profile_id,schema_revision,runtime_generation,auth_epoch,stream_epoch,unfiled_project_id FROM libraries WHERE singleton=1`).Scan(&l.ID, &l.ProfileID, &l.SchemaRevision, &l.RuntimeGeneration, &l.AuthEpoch, &l.StreamEpoch, &l.UnfiledProjectID)
	return l, e
}
