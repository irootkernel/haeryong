package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"haeryong.local/haeryong/internal/adapters/contentstore"
)

// The profile writer lock excludes publication/GC throughout startup migration.
// Enumerate the snapshot, not a later live query. Independent durable copies
// protect the recovery set even after future live references/pins are released.
func (s *Store) copyRecoveryObjects(ctx context.Context, snapshot *sql.DB, path string, revision int) (string, int64, error) {
	directory := path + ".objects"
	if err := os.Mkdir(directory, 0700); err != nil {
		return "", 0, err
	}
	manifest, err := os.OpenFile(path+".objects.jsonl", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", 0, err
	}
	defer manifest.Close()
	manifestHash := sha256.New()
	encoder := json.NewEncoder(io.MultiWriter(manifest, manifestHash))
	count := int64(0)
	if revision >= 2 {
		source, err := contentstore.New(filepath.Join(s.profile.DataDir, "storage"))
		if err != nil {
			return "", 0, err
		}
		defer source.Close()
		after := ""
		for {
			rows, err := snapshot.QueryContext(ctx, `SELECT content_hash,byte_size,state FROM content_objects o WHERE content_hash>? AND (EXISTS(SELECT 1 FROM object_pins p WHERE p.content_hash=o.content_hash) OR EXISTS(SELECT 1 FROM object_references r WHERE r.content_hash=o.content_hash) OR EXISTS(SELECT 1 FROM version_entries e WHERE e.content_hash=o.content_hash)) ORDER BY content_hash LIMIT 64`, after)
			if err != nil {
				return "", count, err
			}
			objects := []contentstore.Object{}
			for rows.Next() {
				var o contentstore.Object
				var state string
				if err := rows.Scan(&o.Hash, &o.Size, &state); err != nil {
					rows.Close()
					return "", count, err
				}
				if state != "ready" {
					rows.Close()
					return "", count, errors.New("required migration object is not ready")
				}
				objects = append(objects, o)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return "", count, err
			}
			if len(objects) == 0 {
				break
			}
			for _, o := range objects {
				err := source.VerifiedRead(ctx, o, func(r io.ReadSeeker) error {
					if err := s.boundary(BeforeSnapshotObjectCopy); err != nil {
						return err
					}
					destination, err := os.OpenFile(filepath.Join(directory, o.Hash), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
					if err != nil {
						return err
					}
					hash := sha256.New()
					n, copyErr := io.Copy(io.MultiWriter(destination, hash), r)
					if copyErr == nil && (n != o.Size || hex.EncodeToString(hash.Sum(nil)) != o.Hash) {
						copyErr = errors.New("migration object copy integrity failed")
					}
					if copyErr == nil {
						copyErr = s.boundary(BeforeSnapshotObjectSync)
					}
					if copyErr == nil {
						copyErr = destination.Sync()
					}
					return errors.Join(copyErr, destination.Close())
				})
				if err != nil {
					return "", count, err
				}
				if err := encoder.Encode(map[string]any{"sha256": o.Hash, "bytes": o.Size}); err != nil {
					return "", count, err
				}
				count++
				after = o.Hash
			}
		}
	}
	if err := syncDirectory(directory); err != nil {
		return "", count, err
	}
	if err := manifest.Sync(); err != nil {
		return "", count, err
	}
	if err := manifest.Close(); err != nil {
		return "", count, err
	}
	return hex.EncodeToString(manifestHash.Sum(nil)), count, nil
}
