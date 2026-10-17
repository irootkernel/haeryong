package sqlite

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"haeryong.local/haeryong/internal/adapters/contentstore"
	appauth "haeryong.local/haeryong/internal/application/auth"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// This is labelled reverse construction of the committed schema4, not execution
// of an old binary. Committed migration source/checksums remain unchanged.
func configurationPriorSchemaFour(t *testing.T, s *Store) {
	t.Helper()
	if _, e := s.db.Exec(`DROP TABLE configuration_plans;DROP TABLE scoped_credentials;DROP TABLE workflow_principals;
DROP TRIGGER project_created;DROP TRIGGER document_created;DROP TRIGGER project_updated;DROP TRIGGER document_updated;
ALTER TABLE projects DROP COLUMN updated_at;ALTER TABLE documents DROP COLUMN updated_at;
DELETE FROM schema_migrations WHERE revision>4;UPDATE libraries SET schema_revision=4;PRAGMA user_version=4`); e != nil {
		t.Fatal(e)
	}
	for revision := 1; revision <= 4; revision++ {
		var checksum string
		if e := s.db.QueryRow("SELECT checksum FROM schema_migrations WHERE revision=?", revision).Scan(&checksum); e != nil || checksum != digest([]byte(migrations[revision-1])) {
			t.Fatal("committed migration history changed", revision, e)
		}
	}
}

func TestConfigurationSchemaFourUpgradePreservesReferencesSessionsAndRecovery(t *testing.T) {
	for _, fault := range []Boundary{"", BeforeSnapshotObjectCopy, BeforeSnapshotObjectSync, BeforeMigrationCommit} {
		t.Run(string(fault), func(t *testing.T) {
			s, auth, writer, _ := authFixture(t)
			_, owner := pairOwner(t, auth, "schema4-migration owner")
			health, e := s.Health(ctx)
			if e != nil {
				t.Fatal(e)
			}
			objects, e := contentstore.New(filepath.Join(writer.Profile.DataDir, "storage"))
			if e != nil {
				t.Fatal(e)
			}
			payload := bytes.Repeat([]byte("schema4 retained immutable bytes\n"), 8192)
			staged, e := objects.Stage(ctx, bytes.NewReader(payload))
			if e != nil {
				t.Fatal(e)
			}
			document, version, set := id(t), id(t), id(t)
			_, e = objects.Finalize(ctx, staged, func(object contentstore.Object, publish func() error) error {
				return s.Write(ctx, func(tx app.WriteTx) error {
					if e := publish(); e != nil {
						return e
					}
					if e := tx.PutDurableObject(domain.Object{Hash: object.Hash, Size: object.Size, State: "ready"}); e != nil {
						return e
					}
					if e := tx.CreateDocument(domain.Document{ID: document, ProjectID: health.Library.UnfiledProjectID, Title: "retained"}); e != nil {
						return e
					}
					if e := tx.CreateContentSet(set, digest([]byte("labelled immutable inventory"))); e != nil {
						return e
					}
					if e := tx.PutEntry(domain.Entry{ContentSetID: set, Path: "literal\\u2028.html", Kind: "file", Hash: object.Hash, Size: object.Size}); e != nil {
						return e
					}
					if e := tx.SealContentSet(set); e != nil {
						return e
					}
					if e := tx.PutVersion(domain.Version{ID: version, DocumentID: document, ContentSetID: set, Title: "retained", ViewerProfile: "html_js_v1", SourceBasis: "unverified", Metadata: []byte(`{}`)}); e != nil {
						return e
					}
					if e := tx.PublishVersion(version, 1); e != nil {
						return e
					}
					return tx.AcquirePin(domain.Pin{ID: "independent-backup", Kind: "backup", OwnerID: "labelled-schema4", Hash: object.Hash})
				})
			})
			if e != nil {
				t.Fatal(e)
			}
			hash := staged.Object.Hash
			if e = objects.Close(); e != nil {
				t.Fatal(e)
			}
			configurationPriorSchemaFour(t, s)
			prior, e := s.Health(ctx)
			if e != nil {
				t.Fatal(e)
			}
			configuration, e := s.Configuration(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			injected := errors.New("labelled schema5 migration durability fault")
			up := New(func(boundary Boundary) error {
				if boundary == fault {
					return injected
				}
				return nil
			})
			observed := time.Now().UTC()
			_, closeUp, e := up.Prepare(ctx, writer.Profile)
			if fault != "" {
				if !errors.Is(e, injected) || up.db != nil {
					t.Fatal("migration failure did not close adapter", e)
				}
				priorDB := readDB(t, writer.Profile)
				var revision int
				if e = priorDB.QueryRow("PRAGMA user_version").Scan(&revision); e != nil || revision != 4 {
					t.Fatal("failed migration advanced prior schema", e, revision)
				}
				priorDB.Close()
			} else {
				if e != nil {
					t.Fatal(e)
				}
				closeUp()
			}
			sets, e := filepath.Glob(filepath.Join(writer.Profile.DataDir, "storage", "pre-migration-*.sqlite"))
			if e != nil || len(sets) != 1 {
				t.Fatal("prior coherent recovery set missing", e)
			}
			if fault == BeforeSnapshotObjectCopy || fault == BeforeSnapshotObjectSync {
				if _, e = os.Stat(sets[0] + ".json"); !os.IsNotExist(e) {
					t.Fatal("incomplete recovery marked complete")
				}
			} else {
				copied, e := os.ReadFile(filepath.Join(sets[0]+".objects", hash))
				if e != nil || !bytes.Equal(copied, payload) {
					t.Fatal("referenced recovery bytes changed", e)
				}
				record, e := os.ReadFile(sets[0] + ".json")
				if e != nil || !bytes.Contains(record, []byte(`"complete":true`)) || !bytes.Contains(record, []byte(`"object_count":1`)) {
					t.Fatal("verified recovery descriptor missing", e)
				}
			}
			reopened := openStore(t, writer, nil)
			after, e := reopened.Health(ctx)
			if e != nil || after.Library.ID != prior.Library.ID || after.Library.ProfileID != prior.Library.ProfileID || after.Library.RuntimeGeneration != prior.Library.RuntimeGeneration || after.Library.AuthEpoch != prior.Library.AuthEpoch || after.Library.StreamEpoch != prior.Library.StreamEpoch || after.Library.SchemaRevision != 5 {
				t.Fatal("normal upgrade changed stable identities", e)
			}
			current, e := reopened.Configuration(ctx)
			if e != nil || current.Revision != configuration.Revision || !bytes.Equal(current.Value, configuration.Value) {
				t.Fatal("migration changed effective configuration", e)
			}
			if _, e = appauth.New(reopened).Authenticate(ctx, owner.Token); e != nil {
				t.Fatal("normal upgrade invalidated session", e)
			}
			rows, e := reopened.Entries(ctx, set, "", 64)
			if e != nil || len(rows) != 1 || rows[0].Path != "literal\\u2028.html" || rows[0].Hash != hash {
				t.Fatal("migration lost exact referenced entry", e)
			}
			var updated string
			if e = reopened.db.QueryRow("SELECT updated_at FROM projects WHERE project_id=?", prior.Library.UnfiledProjectID).Scan(&updated); e != nil {
				t.Fatal(e)
			}
			stamp, e := time.Parse(time.RFC3339Nano, updated)
			if e != nil || stamp.Before(observed.Add(-time.Second)) || stamp.After(time.Now().UTC().Add(time.Second)) {
				t.Fatal("backfill did not report actual migration observation", e, updated)
			}
			for revision := 1; revision <= 4; revision++ {
				var checksum string
				if e = reopened.db.QueryRow("SELECT checksum FROM schema_migrations WHERE revision=?", revision).Scan(&checksum); e != nil || checksum != digest([]byte(migrations[revision-1])) {
					t.Fatal("prior checksum changed", revision, e)
				}
			}
		})
	}
}
