package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"haeryong.local/haeryong/internal/application/service"
)

var ctx = context.Background()

func scratch(t *testing.T) string {
	t.Helper()
	parent := os.TempDir()
	if runtime.GOOS == "darwin" {
		mounts, err := exec.Command("mount").Output()
		if err == nil && bytes.Contains(mounts, []byte(" on /Volumes/RootKernel (")) && syscall.Access("/Volumes/RootKernel", 2) == nil {
			parent = "/Volumes/RootKernel/tmp"
		}
	}
	path, err := os.MkdirTemp(parent, "haeryong-sqlite-tests-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(path) })
	return path
}
func profile(t *testing.T) (service.Selection, *service.LockedProfile) {
	t.Helper()
	selection := service.Selection{Profile: "test", DataDir: filepath.Join(scratch(t), "library ?# 경로")}
	if _, err := service.Initialize(selection); err != nil {
		t.Fatal(err)
	}
	writer, err := service.OpenWriter(selection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	if err := writer.PrepareStorageBootstrap(nil); err != nil {
		t.Fatal(err)
	}
	return selection, writer
}
func openStore(t *testing.T, writer *service.LockedProfile, fault Fault) *Store {
	t.Helper()
	s := New(fault)
	state, _, err := s.Prepare(ctx, writer.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Ready {
		t.Fatal("storage not ready")
	}
	t.Cleanup(func() { s.Close() })
	if err := writer.MarkStorageReady(nil); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestBootstrapReopenIdentityAndSettings(t *testing.T) {
	_, writer := profile(t)
	p := writer.Profile
	s := openStore(t, writer, nil)
	h, err := s.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h.Engine != "3.53.4" || h.Journal != "wal" || h.Synchronous != 2 || h.ForeignKeys != 1 || h.BusyTimeout != 5000 || h.Library.ID != p.LibraryID || h.Library.SchemaRevision != SchemaRevision {
		t.Fatalf("unexpected health: %+v", h)
	}
	s.db.SetMaxIdleConns(0)
	replacement, err := s.Health(ctx)
	if err != nil || replacement.ForeignKeys != 1 || replacement.Synchronous != 2 || replacement.BusyTimeout != 5000 {
		t.Fatalf("replacement connection settings lost: %+v %v", replacement, err)
	}
	s.Close()
	reopened := openStore(t, writer, nil)
	again, err := reopened.Health(ctx)
	if err != nil || h.Library != again.Library {
		t.Fatalf("restart changed identity/epochs: %v %+v", err, again)
	}
}
func TestBootstrapFaultsReuseOrRollback(t *testing.T) {
	for _, boundary := range []Boundary{BeforeMigrationCommit, BeforeStorageDirectorySync} {
		t.Run(string(boundary), func(t *testing.T) {
			_, writer := profile(t)
			injected := errors.New("injected storage durability failure")
			s := New(func(b Boundary) error {
				if b == boundary {
					return injected
				}
				return nil
			})
			if _, _, err := s.Prepare(ctx, writer.Profile); !errors.Is(err, injected) {
				t.Fatalf("boundary failed: %v", err)
			}
			if s.db != nil {
				t.Fatal("failed Prepare retained database handle")
			}
			var initial string
			if boundary == BeforeStorageDirectorySync {
				raw := readDB(t, writer.Profile)
				if err := raw.QueryRow("SELECT stream_epoch FROM libraries").Scan(&initial); err != nil {
					t.Fatal(err)
				}
				raw.Close()
			}
			ready := openStore(t, writer, nil)
			h, _ := ready.Health(ctx)
			if initial != "" && h.Library.StreamEpoch != initial {
				t.Fatal("pending committed bootstrap regenerated epochs")
			}
		})
	}
}
func readDB(t *testing.T, p service.Profile) *sql.DB {
	t.Helper()
	uri := url.URL{Scheme: "file", Path: filepath.Join(p.DataDir, "storage", "library.sqlite")}
	q := uri.Query()
	q.Set("mode", "rw")
	uri.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return db
}
func TestReadyStorageLossNeverCreatesSubstitute(t *testing.T) {
	for _, damage := range []string{"missing", "zero", "corrupt", "foreign", "newer", "partial", "checksum"} {
		t.Run(damage, func(t *testing.T) {
			_, writer := profile(t)
			s := openStore(t, writer, nil)
			s.Close()
			path := filepath.Join(writer.Profile.DataDir, "storage", "library.sqlite")
			switch damage {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "zero":
				if err := os.Truncate(path, 0); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(path, []byte("not a database"), 0600); err != nil {
					t.Fatal(err)
				}
			case "foreign":
				db := readDB(t, writer.Profile)
				_, err := db.Exec("UPDATE libraries SET library_id='foreign'")
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "newer":
				db := readDB(t, writer.Profile)
				_, err := db.Exec("UPDATE libraries SET schema_revision=999; PRAGMA user_version=999")
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "partial":
				db := readDB(t, writer.Profile)
				_, err := db.Exec("DROP TABLE region_evidence")
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "checksum":
				db := readDB(t, writer.Profile)
				_, err := db.Exec("UPDATE schema_migrations SET checksum='unknown' WHERE revision=2")
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			failed := New(nil)
			if _, _, err := failed.Prepare(ctx, writer.Profile); err == nil {
				t.Fatal("damaged ready library initialized")
			}
			if failed.db != nil {
				t.Fatal("Prepare failure leaked DB")
			}
			if damage == "missing" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("missing database substituted")
				}
			}
			if damage == "zero" || damage == "corrupt" {
				after, _ := os.ReadFile(path)
				if !bytes.Equal(before, after) {
					t.Fatal("damaged bytes overwritten")
				}
			}
			loaded, err := service.Load(service.Selection{Profile: "test", DataDir: writer.Profile.DataDir})
			if err != nil || loaded.LibraryID != writer.Profile.LibraryID || loaded.StorageState != "ready" {
				t.Fatalf("profile marker replaced: %v", err)
			}
		})
	}
}

type endpointFixture struct {
	started bool
	done    chan error
}

func (e *endpointFixture) Start(_ context.Context, _ service.Profile, id service.Identity, _ int, stop func()) (service.Identity, error) {
	e.started = true
	e.done = make(chan error)
	id.Endpoint = "http://127.0.0.1:9270"
	id.ControlReady = true
	stop()
	return id, nil
}
func (e *endpointFixture) Close(context.Context) error { return nil }
func (e *endpointFixture) Done() <-chan error          { return e.done }
func TestReadyMarkerFaultAndLegacyUpgrade(t *testing.T) {
	for _, boundary := range []string{"profile_file_sync", "profile_rename", "profile_directory_sync"} {
		t.Run(boundary, func(t *testing.T) {
			selection, writer := profile(t)
			original := writer.Profile
			writer.Close()
			endpoint := &endpointFixture{}
			s := New(nil)
			injected := errors.New("injected profile flush failure")
			runner := service.Runner{Selection: selection, Port: 9270, Storage: s, Endpoint: endpoint, ProfileFault: func(b string) error {
				if b == boundary {
					return injected
				}
				return nil
			}}
			if err := runner.Run(ctx, func(service.Identity) { t.Error("published failed ready marker") }); !errors.Is(err, injected) {
				t.Fatalf("marker fault=%v", err)
			}
			if endpoint.started || s.db != nil {
				t.Fatal("marker failure published or retained DB")
			}
			loaded, err := service.Load(selection)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.LibraryID != original.LibraryID || loaded.ProfileID != original.ProfileID || loaded.ControlKey != original.ControlKey {
				t.Fatal("marker retry changed profile identity")
			}
			if err := (service.Runner{Selection: selection, Port: 9270, Storage: New(nil), Endpoint: &endpointFixture{}}).Run(ctx, func(id service.Identity) {
				if !id.Storage.Ready {
					t.Error("retry not ready")
				}
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, nonempty := range []bool{false, true} {
		t.Run(fmt.Sprint("legacy-", nonempty), func(t *testing.T) {
			selection, writer := profile(t)
			legacy := writer.Profile
			legacy.Format = 1
			legacy.StorageState = ""
			writer.Close()
			data, _ := json.Marshal(legacy)
			if err := os.WriteFile(filepath.Join(legacy.DataDir, "profile.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if nonempty {
				if err := os.WriteFile(filepath.Join(legacy.DataDir, "storage", "unknown"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			endpoint := &endpointFixture{}
			err := (service.Runner{Selection: selection, Port: 9270, Storage: New(nil), Endpoint: endpoint}).Run(ctx, func(service.Identity) {})
			if nonempty {
				if err == nil || endpoint.started {
					t.Fatal("adopted unknown legacy storage")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			upgraded, err := service.Load(selection)
			if err != nil || upgraded.Format != 2 || upgraded.StorageState != "ready" || upgraded.LibraryID != legacy.LibraryID || upgraded.ControlKey != legacy.ControlKey {
				t.Fatalf("legacy upgrade: %v %+v", err, upgraded)
			}
		})
	}
}
func TestMigrationRecoverySnapshotRetainedOnFailure(t *testing.T) {
	_, writer := profile(t)
	p := writer.Profile
	path := filepath.Join(p.DataDir, "storage", "library.sqlite")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	db := readDB(t, p)
	if _, err := db.Exec(migrations[0]); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO libraries VALUES(1,?,?,1,'runtime','stream','auth','unfiled')", []any{p.LibraryID, p.ProfileID}},
		{"INSERT INTO projects VALUES('unfiled','Unfiled','active',1,1)", nil},
		{"INSERT INTO config_revisions VALUES(1,?,'fixture','now')", []any{[]byte(`{}`)}},
		{"INSERT INTO schema_migrations VALUES(1,?,'now')", []any{digest([]byte(migrations[0]))}},
		{"PRAGMA user_version=1", nil},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}

	db.Close()
	if err := writer.MarkStorageReady(nil); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected migration commit failure")
	s := New(func(b Boundary) error {
		if b == BeforeMigrationCommit {
			return injected
		}
		return nil
	})
	if _, _, err := s.Prepare(ctx, writer.Profile); !errors.Is(err, injected) {
		t.Fatalf("migration error: %v", err)
	}
	snapshots, err := filepath.Glob(filepath.Join(p.DataDir, "storage", "pre-migration-*.sqlite"))
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshot missing: %v %v", snapshots, err)
	}
	for _, name := range snapshots {
		if _, err := os.Stat(name + ".json"); err != nil {
			t.Fatal("verified snapshot record missing")
		}
	}
	db = readDB(t, p)
	var revision int
	if err := db.QueryRow("SELECT schema_revision FROM libraries").Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("failed migration changed prior schema: %v %d", err, revision)
	}
	db.Close()
	snapshotFailure := New(func(b Boundary) error {
		if b == BeforeSnapshotSync {
			return injected
		}
		return nil
	})
	if _, _, err := snapshotFailure.Prepare(ctx, writer.Profile); !errors.Is(err, injected) {
		t.Fatalf("snapshot flush failure not fenced: %v", err)
	}
	if _, err := os.Stat(snapshots[0] + ".json"); err != nil {
		t.Fatal("prior verified recovery snapshot lost")
	}
	ready := openStore(t, writer, nil)
	h, err := ready.Health(ctx)
	if err != nil || h.Library.SchemaRevision != SchemaRevision || !strings.HasPrefix(h.Library.ID, p.LibraryID) {
		t.Fatalf("migration retry: %v %+v", err, h)
	}
}

func TestPendingUnknownStorageFailsWithoutNewDatabase(t *testing.T) {
	_, writer := profile(t)
	path := filepath.Join(writer.Profile.DataDir, "storage", "unrecognized")
	if err := os.WriteFile(path, []byte("must preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(nil)
	if _, _, err := s.Prepare(ctx, writer.Profile); err == nil {
		t.Fatal("unknown pending content initialized")
	}
	if _, err := os.Stat(filepath.Join(writer.Profile.DataDir, "storage", "library.sqlite")); !os.IsNotExist(err) {
		t.Fatal("unknown pending storage received a substitute database")
	}
	bytes, err := os.ReadFile(path)
	if err != nil || string(bytes) != "must preserve" {
		t.Fatal("unknown storage discarded")
	}
}
