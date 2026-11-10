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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/service"
)

var ctx = context.Background()

func scratch(t GinkgoTInterface) string {
	GinkgoHelper()
	parent := os.TempDir()
	if runtime.GOOS == "darwin" && os.Getenv("TMPDIR") == "" {
		mounts, err := exec.Command("mount").Output()
		if err == nil && bytes.Contains(mounts, []byte(" on /Volumes/RootKernel (")) && syscall.Access("/Volumes/RootKernel", 2) == nil {
			parent = "/Volumes/RootKernel/tmp"
		}
	}
	path, err := os.MkdirTemp(parent, "haeryong-sqlite-tests-*")
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	DeferCleanup(func() { os.RemoveAll(path) })
	return path
}
func profile(t GinkgoTInterface) (service.Selection, *service.LockedProfile) {
	GinkgoHelper()
	selection := service.Selection{Profile: "test", DataDir: filepath.Join(scratch(t), "library ?# 경로")}
	{
		_, err := service.Initialize(selection)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	writer, err := service.OpenWriter(selection)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	DeferCleanup(func() { writer.Close() })
	{
		err := writer.PrepareStorageBootstrap(nil)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	return selection, writer
}
func openStore(t GinkgoTInterface, writer *service.LockedProfile, fault Fault) *Store {
	GinkgoHelper()
	s := New(fault)
	state, _, err := s.Prepare(ctx, writer.Profile)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	Expect(!state.Ready).To(BeFalse(), func() string { return fmt.Sprint("storage not ready") })
	DeferCleanup(func() { s.Close() })
	{
		err := writer.MarkStorageReady(nil)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	return s
}

var _ = It("TestBootstrapReopenIdentityAndSettings", Label("persistence"), func() {
	t := GinkgoT()

	_, writer := profile(t)
	p := writer.Profile
	s := openStore(t, writer, nil)
	h, err := s.Health(ctx)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	Expect(h.Engine != "3.53.4" || h.Journal != "wal" || h.Synchronous != 2 || h.ForeignKeys != 1 || h.BusyTimeout != 5000 || h.Library.ID != p.LibraryID || h.Library.SchemaRevision != SchemaRevision).To(BeFalse(), func() string { return fmt.Sprintf("unexpected health: %+v", h) })

	s.db.SetMaxIdleConns(0)
	replacement, err := s.Health(ctx)
	Expect(err != nil || replacement.ForeignKeys != 1 || replacement.Synchronous != 2 || replacement.BusyTimeout != 5000).To(BeFalse(), func() string { return fmt.Sprintf("replacement connection settings lost: %+v %v", replacement, err) })

	s.Close()
	reopened := openStore(t, writer, nil)
	again, err := reopened.Health(ctx)
	Expect(err != nil || h.Library != again.Library).To(BeFalse(), func() string { return fmt.Sprintf("restart changed identity/epochs: %v %+v", err, again) })

})
var _ = It("TestBootstrapFaultsReuseOrRollback", Label("persistence"), func() {
	t := GinkgoT()

	for _, boundary := range []Boundary{BeforeMigrationCommit, BeforeStorageDirectorySync} {
		func() {
			By(string(boundary))
			_, writer := profile(t)
			injected := errors.New("injected storage durability failure")
			s := New(func(b Boundary) error {
				if b == boundary {
					return injected
				}
				return nil
			})
			{
				_, _, err := s.Prepare(ctx, writer.Profile)
				Expect(!errors.Is(err, injected)).To(BeFalse(), func() string { return fmt.Sprintf("boundary failed: %v", err) })
			}
			Expect(s.db != nil).To(BeFalse(), func() string { return fmt.Sprint("failed Prepare retained database handle") })

			var initial string
			if boundary == BeforeStorageDirectorySync {
				raw := readDB(t, writer.Profile)
				{
					err := raw.QueryRow("SELECT stream_epoch FROM libraries").Scan(&initial)
					Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
				}

				raw.Close()
			}
			ready := openStore(t, writer, nil)
			h, _ := ready.Health(ctx)
			Expect(initial != "" && h.Library.StreamEpoch != initial).To(BeFalse(), func() string { return fmt.Sprint("pending committed bootstrap regenerated epochs") })

		}()
	}
})

func readDB(t GinkgoTInterface, p service.Profile) *sql.DB {
	GinkgoHelper()
	uri := url.URL{Scheme: "file", Path: filepath.Join(p.DataDir, "storage", "library.sqlite")}
	q := uri.Query()
	q.Set("mode", "rw")
	uri.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", uri.String())
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	db.SetMaxOpenConns(1)
	return db
}

var _ = It("TestReadyStorageLossNeverCreatesSubstitute", Label("persistence"), func() {
	t := GinkgoT()

	for _, damage := range []string{"missing", "zero", "corrupt", "foreign", "newer", "partial", "checksum"} {
		func() {
			By(damage)
			_, writer := profile(t)
			s := openStore(t, writer, nil)
			s.Close()
			path := filepath.Join(writer.Profile.DataDir, "storage", "library.sqlite")
			switch damage {
			case "missing":
				if err := os.Remove(path); err != nil {
					Fail(fmt.Sprint(err))
				}
			case "zero":
				if err := os.Truncate(path, 0); err != nil {
					Fail(fmt.Sprint(err))
				}
			case "corrupt":
				if err := os.WriteFile(path, []byte("not a database"), 0600); err != nil {
					Fail(fmt.Sprint(err))
				}
			case "foreign":
				db := readDB(t, writer.Profile)
				_, err := db.Exec("UPDATE libraries SET library_id='foreign'")
				db.Close()
				if err != nil {
					Fail(fmt.Sprint(err))
				}
			case "newer":
				db := readDB(t, writer.Profile)
				_, err := db.Exec("UPDATE libraries SET schema_revision=999; PRAGMA user_version=999")
				db.Close()
				if err != nil {
					Fail(fmt.Sprint(err))
				}
			case "partial":
				db := readDB(t, writer.Profile)
				_, err := db.Exec("DROP TABLE region_evidence")
				db.Close()
				if err != nil {
					Fail(fmt.Sprint(err))
				}
			case "checksum":
				db := readDB(t, writer.Profile)
				_, err := db.Exec("UPDATE schema_migrations SET checksum='unknown' WHERE revision=2")
				db.Close()
				if err != nil {
					Fail(fmt.Sprint(err))
				}
			}
			before, _ := os.ReadFile(path)
			failed := New(nil)
			{
				_, _, err := failed.Prepare(ctx, writer.Profile)
				Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("damaged ready library initialized") })
			}
			Expect(failed.db != nil).To(BeFalse(), func() string { return fmt.Sprint("Prepare failure leaked DB") })

			if damage == "missing" {
				{
					_, err := os.Stat(path)
					Expect(!os.IsNotExist(err)).To(BeFalse(), func() string { return fmt.Sprint("missing database substituted") })
				}

			}
			if damage == "zero" || damage == "corrupt" {
				after, _ := os.ReadFile(path)
				Expect(!bytes.Equal(before, after)).To(BeFalse(), func() string { return fmt.Sprint("damaged bytes overwritten") })

			}
			loaded, err := service.Load(service.Selection{Profile: "test", DataDir: writer.Profile.DataDir})
			Expect(err != nil || loaded.LibraryID != writer.Profile.LibraryID || loaded.StorageState != "ready").To(BeFalse(), func() string { return fmt.Sprintf("profile marker replaced: %v", err) })

		}()
	}
})

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

var _ = It("TestReadyMarkerFaultAndLegacyUpgrade", Label("persistence"), func() {
	t := GinkgoT()

	for _, boundary := range []string{"profile_file_sync", "profile_rename", "profile_directory_sync"} {
		func() {
			By(boundary)
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
			{
				err := runner.Run(ctx, func(service.Identity) { Fail(fmt.Sprint("published failed ready marker")) })
				Expect(!errors.Is(err, injected)).To(BeFalse(), func() string { return fmt.Sprintf("marker fault=%v", err) })
			}
			Expect(endpoint.started || s.db != nil).To(BeFalse(), func() string { return fmt.Sprint("marker failure published or retained DB") })

			loaded, err := service.Load(selection)
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
			Expect(loaded.LibraryID != original.LibraryID || loaded.ProfileID != original.ProfileID || loaded.ControlKey != original.ControlKey).To(BeFalse(), func() string { return fmt.Sprint("marker retry changed profile identity") })
			{

				err := (service.Runner{Selection: selection, Port: 9270, Storage: New(nil), Endpoint: &endpointFixture{}}).Run(ctx, func(id service.Identity) {
					if !id.Storage.Ready {
						Fail(fmt.Sprint("retry not ready"))
					}
				})
				Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
			}

		}()
	}
	for _, nonempty := range []bool{false, true} {
		func() {
			By(fmt.Sprint("legacy-", nonempty))
			selection, writer := profile(t)
			legacy := writer.Profile
			legacy.Format = 1
			legacy.StorageState = ""
			writer.Close()
			data, _ := json.Marshal(legacy)
			{
				err := os.WriteFile(filepath.Join(legacy.DataDir, "profile.json"), data, 0600)
				Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
			}

			if nonempty {
				{
					err := os.WriteFile(filepath.Join(legacy.DataDir, "storage", "unknown"), []byte("preserve"), 0600)
					Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
				}

			}
			endpoint := &endpointFixture{}
			err := (service.Runner{Selection: selection, Port: 9270, Storage: New(nil), Endpoint: endpoint}).Run(ctx, func(service.Identity) {})
			if nonempty {
				Expect(err == nil || endpoint.started).To(BeFalse(), func() string { return fmt.Sprint("adopted unknown legacy storage") })

				return
			}
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

			upgraded, err := service.Load(selection)
			Expect(err != nil || upgraded.Format != 2 || upgraded.StorageState != "ready" || upgraded.LibraryID != legacy.LibraryID || upgraded.ControlKey != legacy.ControlKey).To(BeFalse(), func() string { return fmt.Sprintf("legacy upgrade: %v %+v", err, upgraded) })

		}()
	}
})
var _ = It("TestMigrationRecoverySnapshotRetainedOnFailure", Label("persistence"), func() {
	t := GinkgoT()

	_, writer := profile(t)
	p := writer.Profile
	path := filepath.Join(p.DataDir, "storage", "library.sqlite")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	f.Close()
	db := readDB(t, p)
	{
		_, err := db.Exec(migrations[0])
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
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
		{
			_, err := db.Exec(statement.query, statement.args...)
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

	}

	db.Close()
	{
		err := writer.MarkStorageReady(nil)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	injected := errors.New("injected migration commit failure")
	s := New(func(b Boundary) error {
		if b == BeforeMigrationCommit {
			return injected
		}
		return nil
	})
	{
		_, _, err := s.Prepare(ctx, writer.Profile)
		Expect(!errors.Is(err, injected)).To(BeFalse(), func() string { return fmt.Sprintf("migration error: %v", err) })
	}

	snapshots, err := filepath.Glob(filepath.Join(p.DataDir, "storage", "pre-migration-*.sqlite"))
	Expect(err != nil || len(snapshots) != 1).To(BeFalse(), func() string { return fmt.Sprintf("snapshot missing: %v %v", snapshots, err) })

	for _, name := range snapshots {
		{
			_, err := os.Stat(name + ".json")
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint("verified snapshot record missing") })
		}

	}
	db = readDB(t, p)
	var revision int
	{
		err := db.QueryRow("SELECT schema_revision FROM libraries").Scan(&revision)
		Expect(err != nil || revision != 1).To(BeFalse(), func() string { return fmt.Sprintf("failed migration changed prior schema: %v %d", err, revision) })
	}

	db.Close()
	snapshotFailure := New(func(b Boundary) error {
		if b == BeforeSnapshotSync {
			return injected
		}
		return nil
	})
	{
		_, _, err := snapshotFailure.Prepare(ctx, writer.Profile)
		Expect(!errors.Is(err, injected)).To(BeFalse(), func() string { return fmt.Sprintf("snapshot flush failure not fenced: %v", err) })
	}
	{

		_, err := os.Stat(snapshots[0] + ".json")
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint("prior verified recovery snapshot lost") })
	}

	ready := openStore(t, writer, nil)
	h, err := ready.Health(ctx)
	Expect(err != nil || h.Library.SchemaRevision != SchemaRevision || !strings.HasPrefix(h.Library.ID, p.LibraryID)).To(BeFalse(), func() string { return fmt.Sprintf("migration retry: %v %+v", err, h) })

})
var _ = It("TestPendingUnknownStorageFailsWithoutNewDatabase", Label("persistence"), func() {
	t := GinkgoT()

	_, writer := profile(t)
	path := filepath.Join(writer.Profile.DataDir, "storage", "unrecognized")
	{
		err := os.WriteFile(path, []byte("must preserve"), 0600)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	s := New(nil)
	{
		_, _, err := s.Prepare(ctx, writer.Profile)
		Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("unknown pending content initialized") })
	}
	{

		_, err := os.Stat(filepath.Join(writer.Profile.DataDir, "storage", "library.sqlite"))
		Expect(!os.IsNotExist(err)).To(BeFalse(), func() string { return fmt.Sprint("unknown pending storage received a substitute database") })
	}

	bytes, err := os.ReadFile(path)
	Expect(err != nil || string(bytes) != "must preserve").To(BeFalse(), func() string { return fmt.Sprint("unknown storage discarded") })

})
