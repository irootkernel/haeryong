package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"haeryong.local/haeryong/internal/adapters/contentstore"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

func authFixture(t *testing.T) (*Store, *appauth.Service, *service.LockedProfile, *atomic.Int64) {
	t.Helper()
	_, w := profile(t)
	s := openStore(t, w, nil)
	core := appauth.New(s)
	clock := &atomic.Int64{}
	clock.Store(time.Now().UnixMilli())
	core.Now = func() time.Time { return time.UnixMilli(clock.Load()) }
	return s, core, w, clock
}
func pairOwner(t *testing.T, core *appauth.Service, label string) (domain.PairingCreated, domain.PairingPoll) {
	t.Helper()
	p, e := core.CreatePairing(ctx, "http://127.0.0.1:9270", label, label)
	if e != nil {
		t.Fatal(e)
	}
	op, _ := service.NewID()
	_, e = core.BootstrapDecide(ctx, domain.PairingDecision{ID: p.ID, Action: "approve", Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: op})
	if e != nil {
		t.Fatal(e)
	}
	redeemed, e := core.Poll(ctx, p.ID, p.PollingSecret, p.Origin)
	if e != nil || redeemed.Token == "" {
		t.Fatal(e)
	}
	return p, redeemed
}
func configureExpiry(t *testing.T, s *Store, idle, absolute *int64) {
	t.Helper()
	c, e := s.Configuration(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var config map[string]any
	if json.Unmarshal(c.Value, &config) != nil {
		t.Fatal("configuration")
	}
	security := config["security"].(map[string]any)
	security["session_idle_seconds"] = idle
	security["session_absolute_seconds"] = absolute
	c.Value, _ = json.Marshal(config)
	c.Actor = "labelled-auth-fixture"
	if e := s.Write(ctx, func(tx app.WriteTx) error { return tx.SetConfiguration(c, c.Revision) }); e != nil {
		t.Fatal(e)
	}
}
func TestOwnerSingleUseHashedPersistenceAndRecovery(t *testing.T) {
	s, core, w, clock := authFixture(t)
	p, first := pairOwner(t, core, "first")
	_, second := pairOwner(t, core, "second")
	if first.Session.PrincipalID != second.Session.PrincipalID || first.Session.ID == second.Session.ID {
		t.Fatal("owner/device identity conflated")
	}
	if _, e := core.Poll(ctx, p.ID, p.PollingSecret, p.Origin); !errors.Is(e, domain.ErrTransition) {
		t.Fatalf("second mint: %v", e)
	}
	if _, e := core.Poll(ctx, p.ID, p.PollingSecret, "http://other.invalid"); !errors.Is(e, sec.ErrRequired) {
		t.Fatalf("origin substitution: %v", e)
	}
	// Scan all persisted auth/receipt/audit fields, not just DTO JSON output.
	for _, query := range []string{"SELECT secret_hash,code_hash FROM pairings", "SELECT token_hash,device_label FROM device_sessions", "SELECT CAST(original_request AS TEXT),CAST(result AS TEXT) FROM operation_receipts", "SELECT coalesce(CAST(body AS TEXT),''),kind FROM events"} {
		rows, e := s.db.Query(query)
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			var a, b string
			if e := rows.Scan(&a, &b); e != nil {
				t.Fatal(e)
			}
			for _, secret := range []string{p.PollingSecret, p.Code, first.Token, second.Token, w.Profile.ControlKey} {
				if strings.Contains(a, secret) || strings.Contains(b, secret) {
					t.Fatal("raw credential persisted")
				}
			}
		}
		rows.Close()
	}
	before, e := s.Health(ctx)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	s = openStore(t, w, nil)
	core = appauth.New(s)
	core.Now = func() time.Time { return time.UnixMilli(clock.Load()) }
	clock.Add(int64(100 * 365 * 24 * time.Hour / time.Millisecond))
	if _, e := core.Authenticate(ctx, first.Token); e != nil {
		t.Fatalf("normal restart/default expiry: %v", e)
	}
	after, e := s.Health(ctx)
	if e != nil || before.Library != after.Library {
		t.Fatal("restart changed epochs")
	}
	if _, e := core.Revoke(ctx, second.Token, first.Session.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := core.Authenticate(ctx, first.Token); !errors.Is(e, sec.ErrRevoked) {
		t.Fatal(e)
	}
	if _, e := core.Revoke(ctx, second.Token, second.Session.ID); e != nil {
		t.Fatal(e)
	}
	_, recovery := pairOwner(t, core, "recovery")
	if recovery.Session.PrincipalID != first.Session.PrincipalID {
		t.Fatal("bootstrap allocated another owner")
	}
	// An application-managed epoch change is a labelled restore seam, not a full restore engine.
	if _, e := s.db.Exec("UPDATE libraries SET auth_epoch='labelled-restore-epoch'"); e != nil {
		t.Fatal(e)
	}
	if _, e := core.Authenticate(ctx, recovery.Token); !errors.Is(e, sec.ErrRevoked) {
		t.Fatal(e)
	}
	_, fresh := pairOwner(t, core, "post-restore")
	if _, e := core.Authenticate(ctx, fresh.Token); e != nil {
		t.Fatal(e)
	}
}
func TestPairingDecisionCASReplayAndRedemptionRace(t *testing.T) {
	_, core, _, _ := authFixture(t)
	_, owner := pairOwner(t, core, "owner")
	p, e := core.CreatePairing(ctx, "http://127.0.0.1:9270", "race", "race")
	if e != nil {
		t.Fatal(e)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, action := range []string{"approve", "reject"} {
		wg.Add(1)
		go func(action string) {
			defer wg.Done()
			<-start
			op, _ := service.NewID()
			_, e := core.Decide(ctx, owner.Token, domain.PairingDecision{ID: p.ID, Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: op, Action: action})
			results <- e
		}(action)
	}
	close(start)
	wg.Wait()
	close(results)
	success, stale := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, domain.ErrPrecondition) {
			stale++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || stale != 1 {
		t.Fatalf("CAS %d %d", success, stale)
	}
	q, e := core.CreatePairing(ctx, p.Origin, "approved-race", "approved-race")
	if e != nil {
		t.Fatal(e)
	}
	op, _ := service.NewID()
	decision := domain.PairingDecision{ID: q.ID, Code: q.Code, Origin: q.Origin, ETag: q.ETag, OperationID: op, Action: "approve"}
	wrongResource := decision
	wrongResource.OperationID, _ = service.NewID()
	wrongResource.ETag = p.ETag
	if _, e := core.Decide(ctx, owner.Token, wrongResource); !errors.Is(e, domain.ErrPrecondition) {
		t.Fatal("another resource validator was not stale", e)
	}
	approved, e := core.Decide(ctx, owner.Token, decision)
	if e != nil {
		t.Fatal(e)
	}
	replayed, e := core.Decide(ctx, owner.Token, decision)
	if e != nil || !bytes.Equal(approved.Body, replayed.Body) {
		t.Fatal("matching receipt lost before stale precondition", e)
	}
	changed := decision
	changed.Code += "x"
	if _, e := core.Decide(ctx, owner.Token, changed); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("changed comparison intent replayed", e)
	}
	var current domain.Pairing
	if json.Unmarshal(approved.Body, &current) != nil {
		t.Fatal("pairing response")
	}
	decision.Action = "reject"
	decision.ETag = current.ETag
	decision.OperationID, _ = service.NewID()
	start = make(chan struct{})
	var poll domain.PairingPoll
	var pollErr, rejectErr error
	wg.Add(2)
	go func() { defer wg.Done(); <-start; poll, pollErr = core.Poll(ctx, q.ID, q.PollingSecret, q.Origin) }()
	go func() { defer wg.Done(); <-start; _, rejectErr = core.Decide(ctx, owner.Token, decision) }()
	close(start)
	wg.Wait()
	if poll.Token != "" {
		if pollErr != nil || !errors.Is(rejectErr, domain.ErrPrecondition) {
			t.Fatalf("redemption winner %v %v", pollErr, rejectErr)
		}
	} else if rejectErr != nil || pollErr != nil || poll.State != "rejected" {
		t.Fatalf("reject winner %v %v %+v", pollErr, rejectErr, poll)
	}
	if _, e := core.Revoke(ctx, owner.Token, owner.Session.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := core.Decide(ctx, owner.Token, decision); !errors.Is(e, sec.ErrRevoked) {
		t.Fatal("revoked receipt lookup", e)
	}
}
func TestSessionExpiryPrecommitRollbackAndCurrentReplayVisibility(t *testing.T) {
	s, core, _, clock := authFixture(t)
	_, owner := pairOwner(t, core, "expiry")
	one := int64(1)
	configureExpiry(t, s, nil, &one)
	op, _ := service.NewID()
	request := domain.Request{Key: domain.OperationKey{PrincipalID: owner.Session.PrincipalID, OperationID: op}, Method: "PUT", Target: "/labelled-resource", Body: []byte(`{}`)}
	_, e := app.Execute(ctx, s, core.Authority(owner.Token), request, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		if _, e := tx.MutateResource(domain.Resource{Kind: "labelled", ID: "atomic", Value: []byte("private")}, 0, "changed"); e != nil {
			return domain.Response{}, e
		}
		clock.Add(2000)
		return domain.Response{Status: 200, Body: []byte(`{}`)}, nil
	})
	if !errors.Is(e, sec.ErrExpired) {
		t.Fatal(e)
	}
	for _, q := range []string{"SELECT count(*) FROM resource_revisions WHERE resource_id='atomic'", "SELECT count(*) FROM events WHERE resource_id='atomic'", "SELECT count(*) FROM operation_receipts WHERE operation_id='" + op + "'"} {
		var n int
		if e := s.db.QueryRow(q).Scan(&n); e != nil || n != 0 {
			t.Fatalf("expired commit retained state %d %v", n, e)
		}
	}
	configureExpiry(t, s, nil, nil)
	if _, e := core.Authenticate(ctx, owner.Token); e != nil {
		t.Fatal(e)
	}
	project, _ := service.NewID()
	document, _ := service.NewID()
	if e := s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.CreateProject(domain.Project{ID: project, Name: "private"}); e != nil {
			return e
		}
		return tx.CreateDocument(domain.Document{ID: document, ProjectID: project, Title: "hidden", Lifecycle: "active"})
	}); e != nil {
		t.Fatal(e)
	}
	request.Key.OperationID, _ = service.NewID()
	request.Target = "/labelled-document"
	body, _ := json.Marshal(map[string]string{"document_id": document, "private": "result"})
	if _, e := app.Execute(ctx, s, core.Authority(owner.Token), request, nil, func(app.WriteTx, domain.Receipt) (domain.Response, error) {
		return domain.Response{Status: 200, Body: body}, nil
	}); e != nil {
		t.Fatal(e)
	}
	targetOnly := request
	targetOnly.Key.OperationID, _ = service.NewID()
	targetOnly.Target = "/api/v1/documents/" + document
	if _, e := app.Execute(ctx, s, core.Authority(owner.Token), targetOnly, nil, func(app.WriteTx, domain.Receipt) (domain.Response, error) {
		return domain.Response{Status: 200, Body: []byte(`{"title":"target-only private result"}`)}, nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec("UPDATE documents SET lifecycle='trash' WHERE document_id=?", document); e != nil {
		t.Fatal(e)
	}
	if _, e := core.Operation(ctx, owner.Token, request.Key.OperationID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("now-hidden result disclosed", e)
	}
	if _, e := core.Operation(ctx, owner.Token, targetOnly.Key.OperationID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("canonical-target-only hidden result disclosed", e)
	}
	if _, e := core.Operation(ctx, "", request.Key.OperationID); !errors.Is(e, sec.ErrRequired) {
		t.Fatal("anonymous receipt", e)
	}
	s.Close()
	if _, e := core.Authenticate(ctx, owner.Token); e == nil || errors.Is(e, sec.ErrRequired) || errors.Is(e, sec.ErrRevoked) {
		t.Fatal("storage failure was authentication", e)
	}
}
func TestReadySchemaTwoRecoveryIncludesExactImmutableCopies(t *testing.T) {
	for _, fault := range []Boundary{"", BeforeSnapshotObjectCopy, BeforeSnapshotObjectSync, BeforeMigrationCommit} {
		t.Run(string(fault), func(t *testing.T) {
			_, w := profile(t)
			s := openStore(t, w, nil)
			objects, e := contentstore.New(filepath.Join(w.Profile.DataDir, "storage"))
			if e != nil {
				t.Fatal(e)
			}
			payload := bytes.Repeat([]byte("immutable 복구\n"), 8192)
			staged, e := objects.Stage(ctx, bytes.NewReader(payload))
			if e != nil {
				t.Fatal(e)
			}
			_, e = objects.Finalize(ctx, staged, func(o contentstore.Object, publish func() error) error {
				return s.Write(ctx, func(tx app.WriteTx) error {
					if e := publish(); e != nil {
						return e
					}
					if e := tx.PutDurableObject(domain.Object{Hash: o.Hash, Size: o.Size, State: "ready"}); e != nil {
						return e
					}
					return tx.AcquirePin(domain.Pin{ID: "recovery-source", Kind: "backup", OwnerID: "labelled", Hash: o.Hash})
				})
			})
			if e != nil {
				t.Fatal(e)
			}
			hash := staged.Object.Hash
			objects.Close()
			// Recreate the supported prior schema by removing only the labelled later additions
			// from this labelled fixture; never mutate persisted migration source bytes.
			priorSchemaTwo(t, s)
			before, e := s.Health(ctx)
			if e != nil {
				t.Fatal(e)
			}
			s.Close()
			injected := errors.New("labelled migration durability fault")
			up := New(func(b Boundary) error {
				if b == fault {
					return injected
				}
				return nil
			})
			_, _, e = up.Prepare(ctx, w.Profile)
			if fault != "" {
				if !errors.Is(e, injected) {
					t.Fatal(e)
				}
				if up.db != nil {
					t.Fatal("failed adapter retained DB")
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				after, e := up.Health(ctx)
				if e != nil || after.Library.AuthEpoch != before.Library.AuthEpoch || after.Library.StreamEpoch != before.Library.StreamEpoch {
					t.Fatal("migration rotated normal epochs", e)
				}
				up.Close()
			}
			sets, e := filepath.Glob(filepath.Join(w.Profile.DataDir, "storage", "pre-migration-*.sqlite"))
			if e != nil || len(sets) != 1 {
				t.Fatal("recovery set missing", e)
			}
			set := sets[0]
			if fault == BeforeSnapshotObjectCopy || fault == BeforeSnapshotObjectSync {
				if _, e := os.Stat(set + ".json"); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("incomplete recovery declared complete")
				}
			} else {
				copied, e := os.ReadFile(filepath.Join(set+".objects", hash))
				if e != nil || !bytes.Equal(copied, payload) {
					t.Fatal("recovery bytes missing", e)
				}
				record, e := os.ReadFile(set + ".json")
				if e != nil || !bytes.Contains(record, []byte(`"complete":true`)) || !bytes.Contains(record, []byte(`"object_count":1`)) {
					t.Fatal("complete object recovery descriptor missing", e)
				}
			}
			retry := openStore(t, w, nil)
			if _, e := retry.Health(ctx); e != nil {
				t.Fatal(e)
			}
		})
	}
}

type observedAuthStore struct {
	app.Store
	attempted chan struct{}
}

func (s observedAuthStore) Write(c context.Context, fn func(app.WriteTx) error) error {
	close(s.attempted)
	return s.Store.Write(c, fn)
}

type pausedAuth struct {
	app.Authority
	passed, proceed chan struct{}
}

func (a pausedAuth) Authorize(c context.Context, r domain.Request) error {
	if e := a.Authority.Authorize(c, r); e != nil {
		return e
	}
	close(a.passed)
	<-a.proceed
	return nil
}
func TestCurrentSessionRecheckedAfterRealWriterWaitBeforeReceipt(t *testing.T) {
	s, core, _, _ := authFixture(t)
	_, owner := pairOwner(t, core, "writerwait")
	op, _ := service.NewID()
	request := domain.Request{Key: domain.OperationKey{PrincipalID: owner.Session.PrincipalID, OperationID: op}, Method: "PUT", Target: "/labelled-writer-wait", Body: []byte(`{}`)}
	apply := func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		_, e := tx.MutateResource(domain.Resource{Kind: "labelled", ID: "writerwait"}, 0, "changed")
		return domain.Response{Status: 200, Body: []byte(`{"private":"saved result"}`)}, e
	}
	if _, e := app.Execute(ctx, s, core.Authority(owner.Token), request, nil, apply); e != nil {
		t.Fatal(e)
	}
	a := pausedAuth{Authority: core.Authority(owner.Token), passed: make(chan struct{}), proceed: make(chan struct{})}
	observed := observedAuthStore{Store: s, attempted: make(chan struct{})}
	result := make(chan error, 1)
	var applied atomic.Bool
	go func() {
		_, e := app.Execute(ctx, observed, a, request, nil, func(app.WriteTx, domain.Receipt) (domain.Response, error) {
			applied.Store(true)
			return domain.Response{}, nil
		})
		result <- e
	}()
	<-a.passed
	locked, release := make(chan struct{}), make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- s.Write(ctx, func(tx app.WriteTx) error {
			r, e := tx.SessionByID(owner.Session.ID)
			if e != nil {
				return e
			}
			r.Revoked = true
			if e := tx.PutSession(r); e != nil {
				return e
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	close(a.proceed)
	<-observed.attempted
	select {
	case <-result:
		t.Fatal("request bypassed actual held writer")
	default:
	}
	close(release)
	if e := <-writerDone; e != nil {
		t.Fatal(e)
	}
	if e := <-result; !errors.Is(e, sec.ErrRevoked) {
		t.Fatal("stale authority replayed after writer wait", e)
	}
	if applied.Load() {
		t.Fatal("replayed a mutation")
	}
}

func TestSchemaTwoForeignKeyDamageCannotProduceCompleteRecoveryOrMigrate(t *testing.T) {
	_, writer := profile(t)
	store := openStore(t, writer, nil)
	// Labelled prior-schema corruption: quick_check remains OK, but an object pin
	// references no object. The missing row cannot appear in object enumeration.
	if _, err := store.db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("INSERT INTO object_pins VALUES('dangling-pin','backup','labelled-damage',?)", strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	priorSchemaTwo(t, store)
	store.Close()
	upgrade := New(nil)
	_, _, err := upgrade.Prepare(ctx, writer.Profile)
	if err == nil {
		upgrade.Close()
		t.Fatal("foreign-key damage accepted")
	}
	if upgrade.db != nil {
		t.Fatal("failed migration adapter retained database")
	}
	uri := url.URL{Scheme: "file", Path: filepath.Join(writer.Profile.DataDir, "storage", "library.sqlite")}
	q := uri.Query()
	q.Set("mode", "rw")
	uri.RawQuery = q.Encode()
	raw, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var revision, history int
	if err := raw.QueryRow("SELECT schema_revision FROM libraries").Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow("SELECT max(revision) FROM schema_migrations").Scan(&history); err != nil {
		t.Fatal(err)
	}
	complete, err := filepath.Glob(filepath.Join(writer.Profile.DataDir, "storage", "pre-migration-*.sqlite.json"))
	if err != nil {
		t.Fatal(err)
	}
	if revision != 2 || history != 2 || len(complete) != 0 {
		t.Fatalf("damaged prior snapshot was declared complete/upgraded: schema=%d history=%d complete=%d", revision, history, len(complete))
	}
}

func priorSchemaTwo(t *testing.T, s *Store) {
	t.Helper()
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version >= 5 {
		if _, err := s.db.Exec("DROP TABLE configuration_plans;DROP TABLE scoped_credentials;DROP TABLE workflow_principals;DROP TRIGGER project_created;DROP TRIGGER document_created;DROP TRIGGER project_updated;DROP TRIGGER document_updated;ALTER TABLE projects DROP COLUMN updated_at;ALTER TABLE documents DROP COLUMN updated_at"); err != nil {
			t.Fatal(err)
		}
	}
	if version >= 4 {
		if _, err := s.db.Exec("DROP TRIGGER upload_revision;DROP TABLE import_metadata; DROP TABLE metadata_indexes;DROP TABLE import_intents;DROP TABLE upload_entries;ALTER TABLE jobs DROP COLUMN result;ALTER TABLE uploads DROP COLUMN originating_device_id;ALTER TABLE uploads DROP COLUMN created_at;ALTER TABLE uploads DROP COLUMN updated_at;ALTER TABLE uploads DROP COLUMN revision;ALTER TABLE uploads DROP COLUMN seal_operation_id;ALTER TABLE uploads DROP COLUMN sealed_object_hash;ALTER TABLE document_versions DROP COLUMN payload_sha256;ALTER TABLE document_versions DROP COLUMN manifest_sha256;ALTER TABLE document_versions DROP COLUMN supplied_metadata_hash"); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.db.Query("PRAGMA table_info(jobs)")
	if err != nil {
		t.Fatal(err)
	}
	hasRevision := false
	for rows.Next() {
		var cid, notNull, primary int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primary); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if name == "revision" {
			hasRevision = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if hasRevision {
		if _, err := s.db.Exec("ALTER TABLE jobs DROP COLUMN revision"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec("DROP TABLE pairings; DROP TABLE device_sessions; DROP TABLE auth_owner; DELETE FROM schema_migrations WHERE revision>2; UPDATE libraries SET schema_revision=2; PRAGMA user_version=2"); err != nil {
		t.Fatal(err)
	}
}

func TestDurableJobSessionRevisionsAndAtomicCurrentEvents(t *testing.T) {
	store, core, writer, clock := authFixture(t)
	_, first := pairOwner(t, core, "revision-first")
	same, err := core.Authenticate(ctx, first.Token)
	if err != nil || same.Revision != 1 || same.ETag != first.Session.ETag {
		t.Fatal("same-time touch changed durable revision", err)
	}
	clock.Add(500)
	touched, err := core.Authenticate(ctx, first.Token)
	if err != nil || touched.Revision != 2 || touched.ETag == same.ETag {
		t.Fatal("actual touch did not advance revision/validator", err)
	}
	_, second := pairOwner(t, core, "revision-second")
	one := int64(1)
	configureExpiry(t, store, nil, &one)
	findFirst := func() domain.Session {
		t.Helper()
		rows, err := core.Sessions(ctx, second.Token)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.ID == first.Session.ID {
				return row
			}
		}
		t.Fatal("device absent")
		return domain.Session{}
	}
	active := findFirst()
	clock.Add(750)
	expired := findFirst()
	if active.State != "active" || expired.State != "expired" || expired.Revision != active.Revision || expired.ETag == active.ETag {
		t.Fatal("derived expiry mutation/validator contract failed")
	}
	revoked, err := core.Revoke(ctx, second.Token, first.Session.ID)
	if err != nil || revoked.Revision != expired.Revision+1 || revoked.ETag == expired.ETag {
		t.Fatal("revocation revision missing", err)
	}
	again, err := core.Revoke(ctx, second.Token, first.Session.ID)
	if err != nil || again.Revision != revoked.Revision || again.ETag != revoked.ETag {
		t.Fatal("natural no-op revoke mutated revision", err)
	}
	var sessionEventRevision int64
	if err := store.db.QueryRow("SELECT revision FROM events WHERE resource_kind='session' AND resource_id=? AND kind='session.revoked' ORDER BY sequence DESC LIMIT 1", first.Session.ID).Scan(&sessionEventRevision); err != nil || sessionEventRevision != revoked.Revision {
		t.Fatal("revocation event disagrees with current resource", err)
	}

	create := func() string {
		t.Helper()
		id, _ := service.NewID()
		if err := store.Write(ctx, func(tx app.WriteTx) error { return tx.CreateJob(domain.Job{ID: id, Kind: "export"}) }); err != nil {
			t.Fatal(err)
		}
		return id
	}
	claim := func(id string) domain.Job {
		t.Helper()
		var job domain.Job
		if err := store.Write(ctx, func(tx app.WriteTx) error { var err error; job, err = tx.ClaimJob(id); return err }); err != nil {
			t.Fatal(err)
		}
		return job
	}
	checkEvent := func(id string, want int64) {
		t.Helper()
		var revision int64
		if err := store.db.QueryRow("SELECT revision FROM events WHERE resource_kind='job' AND resource_id=? ORDER BY sequence DESC LIMIT 1", id).Scan(&revision); err != nil || revision != want {
			t.Fatalf("job event/resource revision %d want%d: %v", revision, want, err)
		}
	}
	id := create()
	running := claim(id)
	if running.Revision != 2 {
		t.Fatal("claim revision missing")
	}
	if err := store.Write(ctx, func(tx app.WriteTx) error {
		return tx.FinishJob(id, running.AttemptGeneration, "labelled definite failure", nil)
	}); err != nil {
		t.Fatal(err)
	}
	failed, err := store.Job(ctx, id)
	if err != nil || failed.AttemptGeneration != running.AttemptGeneration || failed.Revision != running.Revision+1 || failed.State != "failed" {
		t.Fatal("same-generation failure did not advance revision", err)
	}
	checkEvent(id, failed.Revision)
	id = create()
	running = claim(id)
	var cancelling domain.Job
	if err := store.Write(ctx, func(tx app.WriteTx) error { var err error; cancelling, err = tx.CancelJob(id); return err }); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(id, cancelling.AttemptGeneration) }); err != nil {
		t.Fatal(err)
	}
	cancelled, err := store.Job(ctx, id)
	if err != nil || cancelled.AttemptGeneration != cancelling.AttemptGeneration || cancelled.Revision != cancelling.Revision+1 {
		t.Fatal("same-generation stop did not advance revision", err)
	}
	checkEvent(id, cancelled.Revision)
	if err := store.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(id, cancelled.AttemptGeneration) }); err != nil {
		t.Fatal(err)
	}
	noop, _ := store.Job(ctx, id)
	if noop.Revision != cancelled.Revision {
		t.Fatal("no-op stop changed revision")
	}
	id = create()
	running = claim(id)
	store.Close()
	store = openStore(t, writer, nil)
	recovered, err := store.Job(ctx, id)
	if err != nil || recovered.State != "interrupted" || recovered.Revision != running.Revision+1 {
		t.Fatal("startup recovery revision missing", err)
	}
	checkEvent(id, recovered.Revision)
	if err := store.Write(ctx, func(tx app.WriteTx) error { return tx.ReconcileJob(id) }); err != nil {
		t.Fatal(err)
	}
	queued, _ := store.Job(ctx, id)
	if queued.Revision != recovered.Revision+1 {
		t.Fatal("reconcile revision missing")
	}
	running = claim(id)
	if err := store.Write(ctx, func(tx app.WriteTx) error { return tx.FinishJob(id, running.AttemptGeneration, "", nil) }); err != nil {
		t.Fatal(err)
	}
	completed, _ := store.Job(ctx, id)
	if completed.AttemptGeneration != running.AttemptGeneration || completed.Revision != running.Revision+1 || completed.State != "completed" {
		t.Fatal("same-generation completion revision missing")
	}
	checkEvent(id, completed.Revision)
	fault := errors.New("labelled revision/outbox commit fault")
	store.fault = func(b Boundary) error {
		if b == BeforeCommit {
			return fault
		}
		return nil
	}
	orphan, _ := service.NewID()
	if err := store.Write(ctx, func(tx app.WriteTx) error { return tx.CreateJob(domain.Job{ID: orphan, Kind: "export"}) }); !errors.Is(err, fault) {
		t.Fatal("job commit fault not reached", err)
	}
	store.fault = nil
	if _, err := store.Job(ctx, orphan); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("rolled back job/revision remained", err)
	}
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM events WHERE resource_id=?", orphan).Scan(&count); err != nil || count != 0 {
		t.Fatal("rolled back job event remained", err)
	}
}

func TestStartupRecoveryCommitFailureRollsBackAndRetryAppliesOnce(t *testing.T) {
	store, _, writer, _ := authFixture(t)
	id, err := service.NewID()
	if err != nil {
		t.Fatal(err)
	}
	var running domain.Job
	if err := store.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.CreateJob(domain.Job{ID: id, Kind: "export"}); err != nil {
			return err
		}
		var err error
		running, err = tx.ClaimJob(id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var priorEvents int
	if err := store.db.QueryRow("SELECT count(*) FROM events WHERE resource_id=?", id).Scan(&priorEvents); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	fault := errors.New("labelled startup recovery commit failure")
	attempts := 0
	failed := New(func(b Boundary) error {
		if b == BeforeCommit {
			attempts++
			return fault
		}
		return nil
	})
	if _, _, err := failed.Prepare(ctx, writer.Profile); !errors.Is(err, fault) || attempts != 1 || failed.db != nil {
		t.Fatalf("startup fault/adapter cleanup: err=%v attempts=%d db=%v", err, attempts, failed.db)
	}
	uri := url.URL{Scheme: "file", Path: filepath.Join(writer.Profile.DataDir, "storage", "library.sqlite")}
	q := uri.Query()
	q.Set("mode", "rw")
	uri.RawQuery = q.Encode()
	raw, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	var state string
	var revision, generation int64
	var events int
	err = raw.QueryRow("SELECT state,revision,attempt_generation FROM jobs WHERE job_id=?", id).Scan(&state, &revision, &generation)
	if err == nil {
		err = raw.QueryRow("SELECT count(*) FROM events WHERE resource_id=?", id).Scan(&events)
	}
	closeErr := raw.Close()
	if err != nil || closeErr != nil || state != running.State || revision != running.Revision || generation != running.AttemptGeneration || events != priorEvents {
		t.Fatalf("failed recovery escaped rollback: state=%s revision=%d generation=%d events=%d err=%v close=%v", state, revision, generation, events, err, closeErr)
	}
	for reopen := 0; reopen < 2; reopen++ {
		s := openStore(t, writer, nil)
		job, err := s.Job(ctx, id)
		var eventRevision int64
		if err == nil {
			err = s.db.QueryRow("SELECT count(*),max(revision) FROM events WHERE resource_id=?", id).Scan(&events, &eventRevision)
		}
		if err != nil || job.State != "interrupted" || job.Revision != running.Revision+1 || job.AttemptGeneration != running.AttemptGeneration+1 || events != priorEvents+1 || eventRevision != job.Revision {
			t.Fatalf("retry/reopen %d recovery must apply once: job=%+v events=%d event_revision=%d err=%v", reopen, job, events, eventRevision, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
