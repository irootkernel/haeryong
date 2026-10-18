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

	"time"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/contentstore"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

func authFixture(t GinkgoTInterface) (*Store, *appauth.Service, *service.LockedProfile, *atomic.Int64) {
	GinkgoHelper()
	_, w := profile(t)
	s := openStore(t, w, nil)
	core := appauth.New(s)
	clock := &atomic.Int64{}
	clock.Store(time.Now().UnixMilli())
	core.Now = func() time.Time { return time.UnixMilli(clock.Load()) }
	return s, core, w, clock
}
func pairOwner(t GinkgoTInterface, core *appauth.Service, label string) (domain.PairingCreated, domain.PairingPoll) {
	GinkgoHelper()
	p, e := core.CreatePairing(ctx, "http://127.0.0.1:9270", label, label)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	op, _ := service.NewID()
	_, e = core.BootstrapDecide(ctx, domain.PairingDecision{ID: p.ID, Action: "approve", Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: op})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	redeemed, e := core.Poll(ctx, p.ID, p.PollingSecret, p.Origin)
	Expect(e != nil || redeemed.Token == "").To(BeFalse(), func() string { return fmt.Sprint(e) })

	return p, redeemed
}
func configureExpiry(t GinkgoTInterface, s *Store, idle, absolute *int64) {
	GinkgoHelper()
	c, e := s.Configuration(ctx)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	var config map[string]any
	Expect(json.Unmarshal(c.Value, &config) != nil).To(BeFalse(), func() string { return fmt.Sprint("configuration") })

	security := config["security"].(map[string]any)
	security["session_idle_seconds"] = idle
	security["session_absolute_seconds"] = absolute
	c.Value, _ = json.Marshal(config)
	c.Actor = "labelled-auth-fixture"
	{
		e := s.Write(ctx, func(tx app.WriteTx) error { return tx.SetConfiguration(c, c.Revision) })
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

}

var _ = It("TestOwnerSingleUseHashedPersistenceAndRecovery", Label("persistence"), func() {
	t := GinkgoT()

	s, core, w, clock := authFixture(t)
	p, first := pairOwner(t, core, "first")
	_, second := pairOwner(t, core, "second")
	Expect(first.Session.PrincipalID != second.Session.PrincipalID || first.Session.ID == second.Session.ID).To(BeFalse(), func() string { return fmt.Sprint("owner/device identity conflated") })
	{

		_, e := core.Poll(ctx, p.ID, p.PollingSecret, p.Origin)
		Expect(!errors.Is(e, domain.ErrTransition)).To(BeFalse(), func() string { return fmt.Sprintf("second mint: %v", e) })
	}
	{

		_, e := core.Poll(ctx, p.ID, p.PollingSecret, "http://other.invalid")
		Expect(!errors.Is(e, sec.ErrRequired)).To(BeFalse(), func() string { return fmt.Sprintf("origin substitution: %v", e) })
	}

	// Scan all persisted auth/receipt/audit fields, not just DTO JSON output.
	for _, query := range []string{"SELECT secret_hash,code_hash FROM pairings", "SELECT token_hash,device_label FROM device_sessions", "SELECT CAST(original_request AS TEXT),CAST(result AS TEXT) FROM operation_receipts", "SELECT coalesce(CAST(body AS TEXT),''),kind FROM events"} {
		rows, e := s.db.Query(query)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

		for rows.Next() {
			var a, b string
			{
				e := rows.Scan(&a, &b)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

			for _, secret := range []string{p.PollingSecret, p.Code, first.Token, second.Token, w.Profile.ControlKey} {
				Expect(strings.Contains(a, secret) || strings.Contains(b, secret)).To(BeFalse(), func() string { return fmt.Sprint("raw credential persisted") })

			}
		}
		rows.Close()
	}
	before, e := s.Health(ctx)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	s.Close()
	s = openStore(t, w, nil)
	core = appauth.New(s)
	core.Now = func() time.Time { return time.UnixMilli(clock.Load()) }
	clock.Add(int64(100 * 365 * 24 * time.Hour / time.Millisecond))
	{
		_, e := core.Authenticate(ctx, first.Token)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprintf("normal restart/default expiry: %v", e) })
	}

	after, e := s.Health(ctx)
	Expect(e != nil || before.Library != after.Library).To(BeFalse(), func() string { return fmt.Sprint("restart changed epochs") })
	{

		_, e := core.Revoke(ctx, second.Token, first.Session.ID)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}
	{

		_, e := core.Authenticate(ctx, first.Token)
		Expect(!errors.Is(e, sec.ErrRevoked)).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}
	{

		_, e := core.Revoke(ctx, second.Token, second.Session.ID)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	_, recovery := pairOwner(t, core, "recovery")
	Expect(recovery.Session.PrincipalID != first.Session.PrincipalID).To(BeFalse(), func() string { return fmt.Sprint("bootstrap allocated another owner") })
	{

		// An application-managed epoch change is a labelled restore seam, not a full restore engine.
		_, e := s.db.Exec("UPDATE libraries SET auth_epoch='labelled-restore-epoch'")
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}
	{

		_, e := core.Authenticate(ctx, recovery.Token)
		Expect(!errors.Is(e, sec.ErrRevoked)).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	_, fresh := pairOwner(t, core, "post-restore")
	{
		_, e := core.Authenticate(ctx, fresh.Token)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

})
var _ = It("TestPairingDecisionCASReplayAndRedemptionRace", Label("persistence"), func() {
	t := GinkgoT()

	_, core, _, _ := authFixture(t)
	_, owner := pairOwner(t, core, "owner")
	p, e := core.CreatePairing(ctx, "http://127.0.0.1:9270", "race", "race")
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, action := range []string{"approve", "reject"} {
		wg.Add(1)
		go func(action string) {
			defer GinkgoRecover()
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
			Fail(fmt.Sprint(e))
		}
	}
	Expect(success != 1 || stale != 1).To(BeFalse(), func() string { return fmt.Sprintf("CAS %d %d", success, stale) })

	q, e := core.CreatePairing(ctx, p.Origin, "approved-race", "approved-race")
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	op, _ := service.NewID()
	decision := domain.PairingDecision{ID: q.ID, Code: q.Code, Origin: q.Origin, ETag: q.ETag, OperationID: op, Action: "approve"}
	wrongResource := decision
	wrongResource.OperationID, _ = service.NewID()
	wrongResource.ETag = p.ETag
	{
		_, e := core.Decide(ctx, owner.Token, wrongResource)
		Expect(!errors.Is(e, domain.ErrPrecondition)).To(BeFalse(), func() string { return fmt.Sprint("another resource validator was not stale", e) })
	}

	approved, e := core.Decide(ctx, owner.Token, decision)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	replayed, e := core.Decide(ctx, owner.Token, decision)
	Expect(e != nil || !bytes.Equal(approved.Body, replayed.Body)).To(BeFalse(), func() string { return fmt.Sprint("matching receipt lost before stale precondition", e) })

	changed := decision
	changed.Code += "x"
	{
		_, e := core.Decide(ctx, owner.Token, changed)
		Expect(!errors.Is(e, domain.ErrConflict)).To(BeFalse(), func() string { return fmt.Sprint("changed comparison intent replayed", e) })
	}

	var current domain.Pairing
	Expect(json.Unmarshal(approved.Body, &current) != nil).To(BeFalse(), func() string { return fmt.Sprint("pairing response") })

	decision.Action = "reject"
	decision.ETag = current.ETag
	decision.OperationID, _ = service.NewID()
	start = make(chan struct{})
	var poll domain.PairingPoll
	var pollErr, rejectErr error
	wg.Add(2)
	go func() {
		defer GinkgoRecover()
		defer wg.Done()
		<-start
		poll, pollErr = core.Poll(ctx, q.ID, q.PollingSecret, q.Origin)
	}()
	go func() {
		defer GinkgoRecover()
		defer wg.Done()
		<-start
		_, rejectErr = core.Decide(ctx, owner.Token, decision)
	}()
	close(start)
	wg.Wait()
	if poll.Token != "" {
		Expect(pollErr != nil || !errors.Is(rejectErr, domain.ErrPrecondition)).To(BeFalse(), func() string { return fmt.Sprintf("redemption winner %v %v", pollErr, rejectErr) })

	} else if rejectErr != nil || pollErr != nil || poll.State != "rejected" {
		Fail(fmt.Sprintf("reject winner %v %v %+v", pollErr, rejectErr, poll))
	}
	{
		_, e := core.Revoke(ctx, owner.Token, owner.Session.ID)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}
	{

		_, e := core.Decide(ctx, owner.Token, decision)
		Expect(!errors.Is(e, sec.ErrRevoked)).To(BeFalse(), func() string { return fmt.Sprint("revoked receipt lookup", e) })
	}

})
var _ = It("TestSessionExpiryPrecommitRollbackAndCurrentReplayVisibility", Label("persistence"), func() {
	t := GinkgoT()

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
	Expect(!errors.Is(e, sec.ErrExpired)).To(BeFalse(), func() string { return fmt.Sprint(e) })

	for _, q := range []string{"SELECT count(*) FROM resource_revisions WHERE resource_id='atomic'", "SELECT count(*) FROM events WHERE resource_id='atomic'", "SELECT count(*) FROM operation_receipts WHERE operation_id='" + op + "'"} {
		var n int
		{
			e := s.db.QueryRow(q).Scan(&n)
			Expect(e != nil || n != 0).To(BeFalse(), func() string { return fmt.Sprintf("expired commit retained state %d %v", n, e) })
		}

	}
	configureExpiry(t, s, nil, nil)
	{
		_, e := core.Authenticate(ctx, owner.Token)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	project, _ := service.NewID()
	document, _ := service.NewID()
	{
		e := s.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.CreateProject(domain.Project{ID: project, Name: "private"}); e != nil {
				return e
			}
			return tx.CreateDocument(domain.Document{ID: document, ProjectID: project, Title: "hidden", Lifecycle: "active"})
		})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	request.Key.OperationID, _ = service.NewID()
	request.Target = "/labelled-document"
	body, _ := json.Marshal(map[string]string{"document_id": document, "private": "result"})
	{
		_, e := app.Execute(ctx, s, core.Authority(owner.Token), request, nil, func(app.WriteTx, domain.Receipt) (domain.Response, error) {
			return domain.Response{Status: 200, Body: body}, nil
		})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	targetOnly := request
	targetOnly.Key.OperationID, _ = service.NewID()
	targetOnly.Target = "/api/v1/documents/" + document
	{
		_, e := app.Execute(ctx, s, core.Authority(owner.Token), targetOnly, nil, func(app.WriteTx, domain.Receipt) (domain.Response, error) {
			return domain.Response{Status: 200, Body: []byte(`{"title":"target-only private result"}`)}, nil
		})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}
	{

		_, e := s.db.Exec("UPDATE documents SET lifecycle='trash' WHERE document_id=?", document)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}
	{

		_, e := core.Operation(ctx, owner.Token, request.Key.OperationID)
		Expect(!errors.Is(e, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("now-hidden result disclosed", e) })
	}
	{

		_, e := core.Operation(ctx, owner.Token, targetOnly.Key.OperationID)
		Expect(!errors.Is(e, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("canonical-target-only hidden result disclosed", e) })
	}
	{

		_, e := core.Operation(ctx, "", request.Key.OperationID)
		Expect(!errors.Is(e, sec.ErrRequired)).To(BeFalse(), func() string { return fmt.Sprint("anonymous receipt", e) })
	}

	s.Close()
	{
		_, e := core.Authenticate(ctx, owner.Token)
		Expect(e == nil || errors.Is(e, sec.ErrRequired) || errors.Is(e, sec.ErrRevoked)).To(BeFalse(), func() string { return fmt.Sprint("storage failure was authentication", e) })
	}

})
var _ = It("TestReadySchemaTwoRecoveryIncludesExactImmutableCopies", Label("persistence"), func() {
	t := GinkgoT()

	for _, fault := range []Boundary{"", BeforeSnapshotObjectCopy, BeforeSnapshotObjectSync, BeforeMigrationCommit} {
		func() {
			By(string(fault))
			_, w := profile(t)
			s := openStore(t, w, nil)
			objects, e := contentstore.New(filepath.Join(w.Profile.DataDir, "storage"))
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			payload := bytes.Repeat([]byte("immutable 복구\n"), 8192)
			staged, e := objects.Stage(ctx, bytes.NewReader(payload))
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

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
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			hash := staged.Object.Hash
			objects.Close()
			// Recreate the supported prior schema by removing only the labelled later additions
			// from this labelled fixture; never mutate persisted migration source bytes.
			priorSchemaTwo(t, s)
			before, e := s.Health(ctx)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

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
				Expect(!errors.Is(e, injected)).To(BeFalse(), func() string { return fmt.Sprint(e) })
				Expect(up.db != nil).To(BeFalse(), func() string { return fmt.Sprint("failed adapter retained DB") })

			} else {
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

				after, e := up.Health(ctx)
				Expect(e != nil || after.Library.AuthEpoch != before.Library.AuthEpoch || after.Library.StreamEpoch != before.Library.StreamEpoch).To(BeFalse(), func() string { return fmt.Sprint("migration rotated normal epochs", e) })

				up.Close()
			}
			sets, e := filepath.Glob(filepath.Join(w.Profile.DataDir, "storage", "pre-migration-*.sqlite"))
			Expect(e != nil || len(sets) != 1).To(BeFalse(), func() string { return fmt.Sprint("recovery set missing", e) })

			set := sets[0]
			if fault == BeforeSnapshotObjectCopy || fault == BeforeSnapshotObjectSync {
				{
					_, e := os.Stat(set + ".json")
					Expect(!errors.Is(e, os.ErrNotExist)).To(BeFalse(), func() string { return fmt.Sprint("incomplete recovery declared complete") })
				}

			} else {
				copied, e := os.ReadFile(filepath.Join(set+".objects", hash))
				Expect(e != nil || !bytes.Equal(copied, payload)).To(BeFalse(), func() string { return fmt.Sprint("recovery bytes missing", e) })

				record, e := os.ReadFile(set + ".json")
				Expect(e != nil || !bytes.Contains(record, []byte(`"complete":true`)) || !bytes.Contains(record, []byte(`"object_count":1`))).To(BeFalse(), func() string { return fmt.Sprint("complete object recovery descriptor missing", e) })

			}
			retry := openStore(t, w, nil)
			{
				_, e := retry.Health(ctx)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

		}()
	}
})

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

var _ = It("TestCurrentSessionRecheckedAfterRealWriterWaitBeforeReceipt", Label("persistence"), func() {
	t := GinkgoT()

	s, core, _, _ := authFixture(t)
	_, owner := pairOwner(t, core, "writerwait")
	op, _ := service.NewID()
	request := domain.Request{Key: domain.OperationKey{PrincipalID: owner.Session.PrincipalID, OperationID: op}, Method: "PUT", Target: "/labelled-writer-wait", Body: []byte(`{}`)}
	apply := func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		_, e := tx.MutateResource(domain.Resource{Kind: "labelled", ID: "writerwait"}, 0, "changed")
		return domain.Response{Status: 200, Body: []byte(`{"private":"saved result"}`)}, e
	}
	{
		_, e := app.Execute(ctx, s, core.Authority(owner.Token), request, nil, apply)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	a := pausedAuth{Authority: core.Authority(owner.Token), passed: make(chan struct{}), proceed: make(chan struct{})}
	observed := observedAuthStore{Store: s, attempted: make(chan struct{})}
	result := make(chan error, 1)
	var applied atomic.Bool
	go func() {
		defer GinkgoRecover()
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
		defer GinkgoRecover()
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
		Fail(fmt.Sprint("request bypassed actual held writer"))
	default:
	}
	close(release)
	{
		e := <-writerDone
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}
	{

		e := <-result
		Expect(!errors.Is(e, sec.ErrRevoked)).To(BeFalse(), func() string { return fmt.Sprint("stale authority replayed after writer wait", e) })
	}
	Expect(applied.Load()).To(BeFalse(), func() string { return fmt.Sprint("replayed a mutation") })

})
var _ = It("TestSchemaTwoForeignKeyDamageCannotProduceCompleteRecoveryOrMigrate", Label("persistence"), func() {
	t := GinkgoT()

	_, writer := profile(t)
	store := openStore(t, writer, nil)
	{
		// Labelled prior-schema corruption: quick_check remains OK, but an object pin
		// references no object. The missing row cannot appear in object enumeration.
		_, err := store.db.Exec("PRAGMA foreign_keys=OFF")
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		_, err := store.db.Exec("INSERT INTO object_pins VALUES('dangling-pin','backup','labelled-damage',?)", strings.Repeat("0", 64))
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		_, err := store.db.Exec("PRAGMA foreign_keys=ON")
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	priorSchemaTwo(t, store)
	store.Close()
	upgrade := New(nil)
	_, _, err := upgrade.Prepare(ctx, writer.Profile)
	if err == nil {
		upgrade.Close()
		Fail(fmt.Sprint("foreign-key damage accepted"))
	}
	Expect(upgrade.db != nil).To(BeFalse(), func() string { return fmt.Sprint("failed migration adapter retained database") })

	uri := url.URL{Scheme: "file", Path: filepath.Join(writer.Profile.DataDir, "storage", "library.sqlite")}
	q := uri.Query()
	q.Set("mode", "rw")
	uri.RawQuery = q.Encode()
	raw, err := sql.Open("sqlite", uri.String())
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	defer raw.Close()
	var revision, history int
	{
		err := raw.QueryRow("SELECT schema_revision FROM libraries").Scan(&revision)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		err := raw.QueryRow("SELECT max(revision) FROM schema_migrations").Scan(&history)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	complete, err := filepath.Glob(filepath.Join(writer.Profile.DataDir, "storage", "pre-migration-*.sqlite.json"))
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	Expect(revision != 2 || history != 2 || len(complete) != 0).To(BeFalse(), func() string {
		return fmt.Sprintf("damaged prior snapshot was declared complete/upgraded: schema=%d history=%d complete=%d", revision, history, len(complete))
	})

})

func priorSchemaTwo(t GinkgoTInterface, s *Store) {
	GinkgoHelper()
	var version int
	{
		err := s.db.QueryRow("PRAGMA user_version").Scan(&version)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	if version >= 6 {
		_, err := s.db.Exec("DROP TABLE contexts")
		Expect(err).NotTo(HaveOccurred())
	}
	if version >= 5 {
		{
			_, err := s.db.Exec("DROP TABLE configuration_plans;DROP TABLE scoped_credentials;DROP TABLE workflow_principals;DROP TRIGGER project_created;DROP TRIGGER document_created;DROP TRIGGER project_updated;DROP TRIGGER document_updated;ALTER TABLE projects DROP COLUMN updated_at;ALTER TABLE documents DROP COLUMN updated_at")
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

	}
	if version >= 4 {
		{
			_, err := s.db.Exec("DROP TRIGGER upload_revision;DROP TABLE import_metadata; DROP TABLE metadata_indexes;DROP TABLE import_intents;DROP TABLE upload_entries;ALTER TABLE jobs DROP COLUMN result;ALTER TABLE uploads DROP COLUMN originating_device_id;ALTER TABLE uploads DROP COLUMN created_at;ALTER TABLE uploads DROP COLUMN updated_at;ALTER TABLE uploads DROP COLUMN revision;ALTER TABLE uploads DROP COLUMN seal_operation_id;ALTER TABLE uploads DROP COLUMN sealed_object_hash;ALTER TABLE document_versions DROP COLUMN payload_sha256;ALTER TABLE document_versions DROP COLUMN manifest_sha256;ALTER TABLE document_versions DROP COLUMN supplied_metadata_hash")
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

	}
	rows, err := s.db.Query("PRAGMA table_info(jobs)")
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	hasRevision := false
	for rows.Next() {
		var cid, notNull, primary int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primary); err != nil {
			rows.Close()
			Fail(fmt.Sprint(err))
		}
		if name == "revision" {
			hasRevision = true
		}
	}
	err = rows.Err()
	rows.Close()
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	if hasRevision {
		{
			_, err := s.db.Exec("ALTER TABLE jobs DROP COLUMN revision")
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

	}
	{
		_, err := s.db.Exec("DROP TABLE pairings; DROP TABLE device_sessions; DROP TABLE auth_owner; DELETE FROM schema_migrations WHERE revision>2; UPDATE libraries SET schema_revision=2; PRAGMA user_version=2")
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

}

var _ = It("TestDurableJobSessionRevisionsAndAtomicCurrentEvents", Label("persistence"), func() {
	t := GinkgoT()

	store, core, writer, clock := authFixture(t)
	_, first := pairOwner(t, core, "revision-first")
	same, err := core.Authenticate(ctx, first.Token)
	Expect(err != nil || same.Revision != 1 || same.ETag != first.Session.ETag).To(BeFalse(), func() string { return fmt.Sprint("same-time touch changed durable revision", err) })

	clock.Add(500)
	touched, err := core.Authenticate(ctx, first.Token)
	Expect(err != nil || touched.Revision != 2 || touched.ETag == same.ETag).To(BeFalse(), func() string { return fmt.Sprint("actual touch did not advance revision/validator", err) })

	_, second := pairOwner(t, core, "revision-second")
	one := int64(1)
	configureExpiry(t, store, nil, &one)
	findFirst := func() domain.Session {
		GinkgoHelper()
		rows, err := core.Sessions(ctx, second.Token)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		for _, row := range rows {
			if row.ID == first.Session.ID {
				return row
			}
		}
		Fail(fmt.Sprint("device absent"))
		return domain.Session{}
	}
	active := findFirst()
	clock.Add(750)
	expired := findFirst()
	Expect(active.State != "active" || expired.State != "expired" || expired.Revision != active.Revision || expired.ETag == active.ETag).To(BeFalse(), func() string { return fmt.Sprint("derived expiry mutation/validator contract failed") })

	revoked, err := core.Revoke(ctx, second.Token, first.Session.ID)
	Expect(err != nil || revoked.Revision != expired.Revision+1 || revoked.ETag == expired.ETag).To(BeFalse(), func() string { return fmt.Sprint("revocation revision missing", err) })

	again, err := core.Revoke(ctx, second.Token, first.Session.ID)
	Expect(err != nil || again.Revision != revoked.Revision || again.ETag != revoked.ETag).To(BeFalse(), func() string { return fmt.Sprint("natural no-op revoke mutated revision", err) })

	var sessionEventRevision int64
	{
		err := store.db.QueryRow("SELECT revision FROM events WHERE resource_kind='session' AND resource_id=? AND kind='session.revoked' ORDER BY sequence DESC LIMIT 1", first.Session.ID).Scan(&sessionEventRevision)
		Expect(err != nil || sessionEventRevision != revoked.Revision).To(BeFalse(), func() string { return fmt.Sprint("revocation event disagrees with current resource", err) })
	}

	create := func() string {
		GinkgoHelper()
		id, _ := service.NewID()
		{
			err := store.Write(ctx, func(tx app.WriteTx) error { return tx.CreateJob(domain.Job{ID: id, Kind: "export"}) })
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

		return id
	}
	claim := func(id string) domain.Job {
		GinkgoHelper()
		var job domain.Job
		{
			err := store.Write(ctx, func(tx app.WriteTx) error { var err error; job, err = tx.ClaimJob(id); return err })
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

		return job
	}
	checkEvent := func(id string, want int64) {
		GinkgoHelper()
		var revision int64
		{
			err := store.db.QueryRow("SELECT revision FROM events WHERE resource_kind='job' AND resource_id=? ORDER BY sequence DESC LIMIT 1", id).Scan(&revision)
			Expect(err != nil || revision != want).To(BeFalse(), func() string { return fmt.Sprintf("job event/resource revision %d want%d: %v", revision, want, err) })
		}

	}
	id := create()
	running := claim(id)
	Expect(running.Revision != 2).To(BeFalse(), func() string { return fmt.Sprint("claim revision missing") })
	{

		err := store.Write(ctx, func(tx app.WriteTx) error {
			return tx.FinishJob(id, running.AttemptGeneration, "labelled definite failure", nil)
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	failed, err := store.Job(ctx, id)
	Expect(err != nil || failed.AttemptGeneration != running.AttemptGeneration || failed.Revision != running.Revision+1 || failed.State != "failed").To(BeFalse(), func() string { return fmt.Sprint("same-generation failure did not advance revision", err) })

	checkEvent(id, failed.Revision)
	id = create()
	running = claim(id)
	var cancelling domain.Job
	{
		err := store.Write(ctx, func(tx app.WriteTx) error { var err error; cancelling, err = tx.CancelJob(id); return err })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		err := store.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(id, cancelling.AttemptGeneration) })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	cancelled, err := store.Job(ctx, id)
	Expect(err != nil || cancelled.AttemptGeneration != cancelling.AttemptGeneration || cancelled.Revision != cancelling.Revision+1).To(BeFalse(), func() string { return fmt.Sprint("same-generation stop did not advance revision", err) })

	checkEvent(id, cancelled.Revision)
	{
		err := store.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(id, cancelled.AttemptGeneration) })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	noop, _ := store.Job(ctx, id)
	Expect(noop.Revision != cancelled.Revision).To(BeFalse(), func() string { return fmt.Sprint("no-op stop changed revision") })

	id = create()
	running = claim(id)
	store.Close()
	store = openStore(t, writer, nil)
	recovered, err := store.Job(ctx, id)
	Expect(err != nil || recovered.State != "interrupted" || recovered.Revision != running.Revision+1).To(BeFalse(), func() string { return fmt.Sprint("startup recovery revision missing", err) })

	checkEvent(id, recovered.Revision)
	{
		err := store.Write(ctx, func(tx app.WriteTx) error { return tx.ReconcileJob(id) })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	queued, _ := store.Job(ctx, id)
	Expect(queued.Revision != recovered.Revision+1).To(BeFalse(), func() string { return fmt.Sprint("reconcile revision missing") })

	running = claim(id)
	{
		err := store.Write(ctx, func(tx app.WriteTx) error { return tx.FinishJob(id, running.AttemptGeneration, "", nil) })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	completed, _ := store.Job(ctx, id)
	Expect(completed.AttemptGeneration != running.AttemptGeneration || completed.Revision != running.Revision+1 || completed.State != "completed").To(BeFalse(), func() string { return fmt.Sprint("same-generation completion revision missing") })

	checkEvent(id, completed.Revision)
	fault := errors.New("labelled revision/outbox commit fault")
	store.fault = func(b Boundary) error {
		if b == BeforeCommit {
			return fault
		}
		return nil
	}
	orphan, _ := service.NewID()
	{
		err := store.Write(ctx, func(tx app.WriteTx) error { return tx.CreateJob(domain.Job{ID: orphan, Kind: "export"}) })
		Expect(!errors.Is(err, fault)).To(BeFalse(), func() string { return fmt.Sprint("job commit fault not reached", err) })
	}

	store.fault = nil
	{
		_, err := store.Job(ctx, orphan)
		Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("rolled back job/revision remained", err) })
	}

	var count int
	{
		err := store.db.QueryRow("SELECT count(*) FROM events WHERE resource_id=?", orphan).Scan(&count)
		Expect(err != nil || count != 0).To(BeFalse(), func() string { return fmt.Sprint("rolled back job event remained", err) })
	}

})
var _ = It("TestStartupRecoveryCommitFailureRollsBackAndRetryAppliesOnce", Label("persistence"), func() {
	t := GinkgoT()

	store, _, writer, _ := authFixture(t)
	id, err := service.NewID()
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	var running domain.Job
	{
		err := store.Write(ctx, func(tx app.WriteTx) error {
			if err := tx.CreateJob(domain.Job{ID: id, Kind: "export"}); err != nil {
				return err
			}
			var err error
			running, err = tx.ClaimJob(id)
			return err
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	var priorEvents int
	{
		err := store.db.QueryRow("SELECT count(*) FROM events WHERE resource_id=?", id).Scan(&priorEvents)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		err := store.Close()
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
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
	{
		_, _, err := failed.Prepare(ctx, writer.Profile)
		Expect(!errors.Is(err, fault) || attempts != 1 || failed.db != nil).To(BeFalse(), func() string {
			return fmt.Sprintf("startup fault/adapter cleanup: err=%v attempts=%d db=%v", err, attempts, failed.db)
		})
	}

	uri := url.URL{Scheme: "file", Path: filepath.Join(writer.Profile.DataDir, "storage", "library.sqlite")}
	q := uri.Query()
	q.Set("mode", "rw")
	uri.RawQuery = q.Encode()
	raw, err := sql.Open("sqlite", uri.String())
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	var state string
	var revision, generation int64
	var events int
	err = raw.QueryRow("SELECT state,revision,attempt_generation FROM jobs WHERE job_id=?", id).Scan(&state, &revision, &generation)
	if err == nil {
		err = raw.QueryRow("SELECT count(*) FROM events WHERE resource_id=?", id).Scan(&events)
	}
	closeErr := raw.Close()
	Expect(err != nil || closeErr != nil || state != running.State || revision != running.Revision || generation != running.AttemptGeneration || events != priorEvents).To(BeFalse(), func() string {
		return fmt.Sprintf("failed recovery escaped rollback: state=%s revision=%d generation=%d events=%d err=%v close=%v", state, revision, generation, events, err, closeErr)
	})

	for reopen := 0; reopen < 2; reopen++ {
		s := openStore(t, writer, nil)
		job, err := s.Job(ctx, id)
		var eventRevision int64
		if err == nil {
			err = s.db.QueryRow("SELECT count(*),max(revision) FROM events WHERE resource_id=?", id).Scan(&events, &eventRevision)
		}
		Expect(err != nil || job.State != "interrupted" || job.Revision != running.Revision+1 || job.AttemptGeneration != running.AttemptGeneration+1 || events != priorEvents+1 || eventRevision != job.Revision).To(BeFalse(), func() string {
			return fmt.Sprintf("retry/reopen %d recovery must apply once: job=%+v events=%d event_revision=%d err=%v", reopen, job, events, eventRevision, err)
		})
		{

			err := s.Close()
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

	}
})
