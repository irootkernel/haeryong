package source_test

import (
	"context"
	"encoding/json"
	"io"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	appauth "haeryong.local/haeryong/internal/application/auth"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/service"
	appsource "haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// reopenFixture rebuilds a source fixture over one profile so the store can
// be closed and reopened (S11: real restart sweeps).
type reopenFixture struct {
	vFixture
	selection  service.Selection
	closeStore func() error
}

func newReopenFixture(t GinkgoTInterface) *reopenFixture {
	GinkgoHelper()
	selection := service.Selection{Profile: "test", DataDir: t.TempDir() + "/library"}
	_, e := service.Initialize(selection)
	Expect(e).NotTo(HaveOccurred())
	w, e := service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { w.Close() })
	f := &reopenFixture{selection: selection}
	f.open(t, w)
	return f
}

func (f *reopenFixture) openProfile(t GinkgoTInterface, profile service.Profile) {
	GinkgoHelper()
	f.openProfileInner(t, profile)
}

func (f *reopenFixture) open(t GinkgoTInterface, w *service.LockedProfile) {
	GinkgoHelper()
	f.openProfileInner(t, w.Profile)
}

func (f *reopenFixture) openProfileInner(t GinkgoTInterface, profile service.Profile) {
	GinkgoHelper()
	store := foundation.New(nil)
	state, closeStore, e := store.Prepare(context.Background(), profile)
	Expect(e).NotTo(HaveOccurred())
	Expect(state.Ready).To(BeTrue())
	f.closeStore = closeStore
	DeferCleanup(func() { _ = closeStore() })
	Expect(profile == service.Profile{}).To(BeFalse())
	f.store = store
	f.auth = appauth.New(store)
	f.auth.Now = time.Now
	f.reader = &fakeReader{blobs: map[string]string{}, missing: map[string]bool{}}
	f.core = appsource.New(store, f.auth, store, f.reader)
	f.dataDir = f.selection.DataDir
	if f.token == "" {
		pairing, e := f.auth.CreatePairing(context.Background(), "http://127.0.0.1:9270", "owner", "client")
		Expect(e).NotTo(HaveOccurred())
		op, _ := service.NewID()
		_, e = f.auth.BootstrapDecide(context.Background(), domain.PairingDecision{ID: pairing.ID, Action: "approve", Code: pairing.Code, Origin: pairing.Origin, ETag: pairing.ETag, OperationID: op})
		Expect(e).NotTo(HaveOccurred())
		redeemed, e := f.auth.Poll(context.Background(), pairing.ID, pairing.PollingSecret, pairing.Origin)
		Expect(e).NotTo(HaveOccurred())
		f.token = redeemed.Token
	}
}

// restart closes the store and reopens it over the same profile, running the
// startup sweep like the daemon wiring does.
func (f *reopenFixture) restart(t GinkgoTInterface) {
	GinkgoHelper()
	Expect(f.closeStore()).To(Succeed())
	profile, e := service.Load(f.selection)
	Expect(e).NotTo(HaveOccurred())
	f.openProfile(t, profile)
	Expect(f.core.Recover(context.Background())).To(Succeed())
}

var _ = Describe("restart sweeps resolve crashed staged checks (S11)", Label("persistence"), func() {
	t := GinkgoT()

	It("settles a real crash after reservation: the reservation pin and the open intent", func() {
		f := newReopenFixture(t)
		f.reader.blobs[gitRevision+"\x00pkg/main.go"] = "package main\n\nfunc main() {}\n"
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go","line_start":3,"line_end":3}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		key := "a1100000-0000-4000-8000-000000000001"
		// A referenced expectation pins its object to the intent from
		// reservation to commit (C6), so the crash leaves that pin.
		uploadID := stageBodyExpectation(t, &f.vFixture, importer.New(f.store, f.auth, f.store, staging.New()), key, "func main() {}\n")
		// The check parks in the source read after reservation; the store
		// closes underneath it like a process crash, so its settlement fails.
		parked, release := make(chan struct{}), make(chan struct{})
		f.reader.hook = func() {
			f.reader.hook = nil
			close(parked)
			<-release
		}
		f.crashDuring(key, []byte(`{"expected_excerpt_ref":{"upload_id":"`+uploadID+`"}}`), parked, release)
		f.expectCrashState(t, key)
		f.expectSwept(t, key)
	})

	It("settles a real crash with an in-flight read pin", func() {
		f := newReopenFixture(t)
		asset := "retained crash bytes\n"
		// The retained-snapshot read holds an intent-owned pin while it
		// streams; park inside that read after the pin was acquired.
		parked, release := make(chan struct{}), make(chan struct{})
		blocking := &blockingBytes{inner: f.store, park: func() { close(parked); <-release }}
		f.core = appsource.New(f.store, f.auth, blocking, f.reader)
		f.buildVersion(`{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+vsha([]byte(asset))+`","asset_path":"snap.bin"}`, `{"id":"ev","source_id":"src","capture_status":"verified","path":"snap.bin"}`, "snap.bin", []byte(asset))
		key := "a1100000-0000-4000-8000-000000000003"
		f.crashDuring(key, []byte(`{}`), parked, release)
		f.expectCrashState(t, key)
		f.expectSwept(t, key)
	})

	It("completes a receipt for a committed verification after restart", func() {
		f := newReopenFixture(t)
		f.reader.blobs[gitRevision+"\x00f.txt"] = "data\n"
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		key := "a1100000-0000-4000-8000-000000000002"
		response, e := f.core.Verify(context.Background(), f.token, f.version, "ev", key, []byte(`{}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(201))
		// A restart resolves nothing new but keeps the terminal receipt.
		f.restart(t)
		receipt, e := f.auth.Operation(context.Background(), f.token, key)
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal("completed"))
	})
})

// S1: typed stage-3 settlements by failure class.
var _ = Describe("typed stage-3 settlements", Label("persistence"), func() {
	t := GinkgoT()

	It("stores and replays a 401 when the session is revoked mid-check", func() {
		f := newVerificationFixture(t)
		f.reader.blobs[gitRevision+"\x00f.txt"] = "data\n"
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		key := "a1200000-0000-4000-8000-000000000001"
		session, se := f.auth.Authenticate(context.Background(), f.token)
		Expect(se).NotTo(HaveOccurred())
		f.reader.hook = func() {
			f.reader.hook = nil
			// Revoke the owner session between the read and the commit.
			_, re := f.auth.Revoke(context.Background(), f.token, session.ID)
			Expect(re).NotTo(HaveOccurred())
		}
		response, e := f.core.Verify(context.Background(), f.token, f.version, "ev", key, []byte(`{}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(401))
		var body map[string]any
		Expect(json.Unmarshal(response.Body, &body)).NotTo(HaveOccurred())
		Expect(body["error"].(map[string]any)["code"]).To(Equal("SESSION_REVOKED"))
		var rows int
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			history, e := tx.Verifications(f.version, "ev", "", "", 10)
			rows = len(history)
			return e
		})).To(Succeed())
		Expect(rows).To(Equal(0), "no verification row for a revoked session")
	})

	It("stores a 404 when the version leaves the scope mid-check", func() {
		f := newVerificationFixture(t)
		f.reader.blobs[gitRevision+"\x00f.txt"] = "data\n"
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		key := "a1200000-0000-4000-8000-000000000002"
		f.reader.hook = func() {
			f.reader.hook = nil
			Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
				return tx.TestTrashDocument(f.document)
			})).To(Succeed())
		}
		response, e := f.core.Verify(context.Background(), f.token, f.version, "ev", key, []byte(`{}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(404))
		var body map[string]any
		Expect(json.Unmarshal(response.Body, &body)).NotTo(HaveOccurred())
		Expect(body["error"].(map[string]any)["code"]).To(Equal("NOT_FOUND"))
	})

	It("never turns a storage failure into a permanent authority failure", func() {
		f := newVerificationFixture(t)
		f.reader.blobs[gitRevision+"\x00f.txt"] = "data\n"
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		key := "a1200000-0000-4000-8000-000000000003"
		// The commit transaction fails with an ordinary storage error.
		failing := &armedStore{Store: f.store}
		f.core = appsource.New(failing, f.auth, f.store, f.reader)
		f.reader.hook = func() {
			f.reader.hook = nil
			failing.arm()
		}
		response, e := f.core.Verify(context.Background(), f.token, f.version, "ev", key, []byte(`{}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(503))
		Expect(errorCode(t, response)).To(Equal("SERVICE_RECOVERING"))
		Expect(string(response.Body)).To(ContainSubstring("stage_failure"))
		receipt, e := f.auth.Operation(context.Background(), f.token, key)
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal("failed"))
		Expect(receipt.Response.Status).To(Equal(503), "never a permanent 403/404")
	})
})

// S12: snapshot evidence with a range past EOF.
var _ = Describe("snapshot range bounds", Label("persistence"), func() {
	t := GinkgoT()
	It("records line_range_out_of_bounds for snapshot evidence past EOF", func() {
		asset := "one\n"
		f := newVerificationFixture(t)
		f.buildVersion(`{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+vsha([]byte(asset))+`","asset_path":"snap.bin"}`, `{"id":"ev","source_id":"src","capture_status":"verified","path":"snap.bin","line_start":2,"line_end":2}`, "snap.bin", []byte(asset))
		response, e := f.verify("a1300000-0000-4000-8000-000000000001", "")
		Expect(e).NotTo(HaveOccurred())
		Expect(outcome(t, response)["reason"]).To(Equal("line_range_out_of_bounds"))
	})
})

// S22: another principal's upload is hidden with 404.
var _ = Describe("expectation upload ownership", Label("persistence"), func() {
	t := GinkgoT()
	It("hides a foreign workflow upload before any other check", func() {
		f := newVerificationFixture(t)
		blob := "package main\n\nfunc main() {}\n"
		f.reader.blobs[gitRevision+"\x00pkg/main.go"] = blob
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go","line_start":3,"line_end":3}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		// Seed a foreign sealed body upload bound to this key, and call as a
		// scoped principal (the owner keeps the adoption exception).
		foreign := "a1400000-0000-4000-8000-000000000009"
		key := "a1400000-0000-4000-8000-000000000001"
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			return tx.CreateUpload(domain.Upload{ID: foreign, PrincipalID: "foreign-principal", Purpose: "body", InputMode: "object", SelectionName: "expectation", Target: []byte(`{"operation_id":"` + key + `","action":"evidence_verification","version_id":"` + f.version + `","evidence_id":"ev"}`), Checkpoint: domain.Checkpoint{Generation: 1, InventorySHA256: app.EmptyInventorySHA256, AcceptedBytes: "0"}, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z"})
		})).To(Succeed())
		scoped := seedScopedSourceReader(t, f)
		_, e := f.core.Verify(context.Background(), scoped, f.version, "ev", key, []byte(`{"expected_excerpt_ref":{"upload_id":"`+foreign+`"}}`))
		Expect(e).To(MatchError(domain.ErrNotFound))
	})
})

// S24: invalid UTF-8 inline excerpts are rejected.
var _ = Describe("inline excerpt encoding", Label("persistence"), func() {
	It("rejects invalid UTF-8 inline excerpts with an invalid request", func() {
		t := GinkgoT()
		f := newVerificationFixture(t)
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
		_, e := f.core.Verify(context.Background(), f.token, f.version, "ev", "a1500000-0000-4000-8000-000000000001", []byte(`{"expected_excerpt":"`+"\xc3\x28"+`"}`))
		Expect(e).To(HaveOccurred())
	})
})

// seedScopedSourceReader issues a scoped credential with version.read and
// source.read for this fixture's version/project.
func seedScopedSourceReader(t GinkgoTInterface, f *vFixture) string {
	t.Helper()
	secret, e := sec.Secret()
	ExpectWithOffset(1, e).NotTo(HaveOccurred())
	principal := "a1400000-0000-4000-8000-0000000000ff"
	scope := domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read", "source.read"}, ProjectIDs: []string{f.project}, VersionIDs: []string{f.version}}
	ExpectWithOffset(1, f.store.Write(context.Background(), func(tx app.WriteTx) error {
		state, e := tx.AuthState()
		if e != nil {
			return e
		}
		if e := tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: principal, Active: true, Scope: scope, Revision: 1}); e != nil {
			return e
		}
		p, e := tx.Scope(f.project)
		if e != nil {
			return e
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: principal, PrincipalID: principal, TokenHash: sec.Verifier("workflow", secret), AuthEpoch: state.AuthEpoch, Scope: scope, Permissions: map[string]int64{f.project: p.Project.PermissionsRevision}, ConfigRevision: p.ConfigRevision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond), Revision: 1})
	})).To(Succeed())
	return secret
}

// blockingBytes parks inside the first read's consumer — after the inner
// read acquired its intent-owned pin — so a crash with that pin in flight is
// reproducible (S11).
type blockingBytes struct {
	inner appsource.Bytes
	park  func()
}

func (b *blockingBytes) Read(ctx context.Context, object domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) error {
	park := b.park
	b.park = nil
	if park == nil {
		return b.inner.Read(ctx, object, pin, consume)
	}
	return b.inner.Read(ctx, object, pin, func(reader io.ReadSeeker) error {
		park()
		return consume(reader)
	})
}

// crashDuring runs one check until it parks, closes the store underneath it
// like a process crash, lets the check finish against the closed store and
// joins it, so the crashed execution never overlaps the recovery (S11).
func (f *reopenFixture) crashDuring(key string, body []byte, parked <-chan struct{}, release chan<- struct{}) {
	GinkgoHelper()
	done := make(chan struct{})
	go func() {
		defer GinkgoRecover()
		defer close(done)
		_, _ = f.core.Verify(context.Background(), f.token, f.version, "ev", key, body)
	}()
	<-parked
	Expect(f.closeStore()).To(Succeed())
	close(release)
	<-done
}

// expectCrashState observes the crash before any sweep: the intent is open
// and still owns at least one pin.
func (f *reopenFixture) expectCrashState(t GinkgoTInterface, key string) {
	GinkgoHelper()
	probe, closeProbe := f.reopenStore(t)
	Expect(f.intentOpen(probe, key)).To(BeTrue(), "the crashed check left an open intent")
	pins, _, e := probe.SourceCheckObservations(context.Background())
	Expect(e).NotTo(HaveOccurred())
	Expect(pins).To(BeNumerically(">=", 1), "the crash left an intent-owned pin")
	Expect(closeProbe()).To(Succeed())
}

// expectSwept runs the startup sweep like the daemon wiring and asserts the
// settled state: failed intent and receipt with the recorded reason, no pin,
// no read and no verification row.
func (f *reopenFixture) expectSwept(t GinkgoTInterface, key string) {
	GinkgoHelper()
	store, _ := f.reopenStore(t)
	auth := appauth.New(store)
	core := appsource.New(store, auth, store, &fakeReader{})
	Expect(core.Recover(context.Background())).To(Succeed())
	receipt, e := auth.Operation(context.Background(), f.token, key)
	Expect(e).NotTo(HaveOccurred())
	Expect(receipt.State).To(Equal("failed"))
	Expect(receipt.Response.Status).To(Equal(503))
	Expect(string(receipt.Response.Body)).To(ContainSubstring("interrupted_before_commit"))
	Expect(f.intentOpen(store, key)).To(BeFalse())
	owner, e := auth.Authenticate(context.Background(), f.token)
	Expect(e).NotTo(HaveOccurred())
	var intent domain.CheckIntent
	Expect(store.Write(context.Background(), func(tx app.WriteTx) error {
		var e error
		intent, e = tx.CheckIntentByOperation(owner.PrincipalID, key)
		return e
	})).To(Succeed())
	Expect(intent.State).To(Equal("failed"))
	Expect(intent.FailureReason).To(Equal("interrupted_before_commit"))
	pins, refs, e := store.SourceCheckObservations(context.Background())
	Expect(e).NotTo(HaveOccurred())
	Expect(pins).To(Equal(0), "no intent-owned pin survives the sweep")
	Expect(refs).To(Equal(0))
	reads, e := store.SourceCheckReads(context.Background())
	Expect(e).NotTo(HaveOccurred())
	Expect(reads).To(Equal(0), "no read row for the crashed check")
	var rows int
	Expect(store.Write(context.Background(), func(tx app.WriteTx) error {
		history, e := tx.Verifications(f.version, "ev", "", "", 10)
		rows = len(history)
		return e
	})).To(Succeed())
	Expect(rows).To(Equal(0), "no verification row for the crashed check")
}

// reopenStore opens the profile's store again without running the sweep.
func (f *reopenFixture) reopenStore(t GinkgoTInterface) (*foundation.Store, func() error) {
	GinkgoHelper()
	store := foundation.New(nil)
	state, closeStore, e := store.Prepare(context.Background(), mustProfile(t, f))
	Expect(e).NotTo(HaveOccurred())
	Expect(state.Ready).To(BeTrue())
	DeferCleanup(func() { _ = closeStore() })
	return store, closeStore
}

func (f *reopenFixture) intentOpen(store *foundation.Store, operationID string) bool {
	open := false
	Expect(store.Write(context.Background(), func(tx app.WriteTx) error {
		intents, e := tx.OpenCheckIntents()
		if e != nil {
			return e
		}
		for _, intent := range intents {
			if intent.OperationID == operationID {
				open = true
			}
		}
		return nil
	})).To(Succeed())
	return open
}

func mustProfile(t GinkgoTInterface, f *reopenFixture) service.Profile {
	t.Helper()
	profile, e := service.Load(f.selection)
	Expect(e).NotTo(HaveOccurred())
	return profile
}

// G6: terminal receipts are preserved and checked against the result rows.
var _ = Describe("recover receipt consistency (G6)", Label("persistence"), func() {
	t := GinkgoT()

	It("keeps a consistent terminal receipt and resolves its reopened intent", func() {
		f := newReopenFixture(t)
		f.reader.blobs[gitRevision+"\x00f.txt"] = "data\n"
		f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
		f.configure(gitMapping(t.TempDir()))
		principal := ownerPrincipal(&f.vFixture)
		// A settled 412 (a configuration change between reservation and
		// commit) and a completed 201, each with its intent reopened: the
		// crash-between-result-and-intent state.
		failed := "a1600000-0000-4000-8000-000000000001"
		f.reader.hook = func() {
			f.reader.hook = nil
			bumpConfig(t, &f.vFixture)
		}
		response, e := f.core.Verify(context.Background(), f.token, f.version, "ev", failed, []byte(`{}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(412))
		completed := "a1600000-0000-4000-8000-000000000003"
		response, e = f.core.Verify(context.Background(), f.token, f.version, "ev", completed, []byte(`{}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(201))
		stored := map[string]domain.Receipt{}
		for _, key := range []string{failed, completed} {
			receipt, e := f.auth.Operation(context.Background(), f.token, key)
			Expect(e).NotTo(HaveOccurred())
			stored[key] = receipt
			Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
				return tx.TestReopenIntent(key, principal)
			})).To(Succeed())
			Expect(f.intentOpen(f.store, key)).To(BeTrue())
		}
		Expect(f.core.Recover(context.Background())).To(Succeed())
		for key, state := range map[string]string{failed: "failed", completed: "completed"} {
			receipt, e := f.auth.Operation(context.Background(), f.token, key)
			Expect(e).NotTo(HaveOccurred())
			Expect(receipt.State).To(Equal(stored[key].State), "the terminal receipt is kept")
			Expect(receipt.Response.Status).To(Equal(stored[key].Response.Status))
			Expect(receipt.Response.Body).To(Equal(stored[key].Response.Body))
			Expect(f.intentOpen(f.store, key)).To(BeFalse(), "the reopened intent is resolved")
			var intent domain.CheckIntent
			Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
				var e error
				intent, e = tx.CheckIntentByOperation(principal, key)
				return e
			})).To(Succeed())
			Expect(intent.State).To(Equal(state))
		}
	})

	It("names the intent for a contradictory completed receipt without a verification row", func() {
		f := newReopenFixture(t)
		key := "a1600000-0000-4000-8000-000000000002"
		intentID := "a1600000-0000-4000-8000-0000000000f1"
		principal := ownerPrincipal(&f.vFixture)
		// A completed receipt with NO verification row: a contradiction.
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			if _, existing, e := tx.ReserveOperation(domain.Request{Key: domain.OperationKey{PrincipalID: principal, OperationID: key}, Method: "POST", Target: appsource.CanonicalTarget("v", "e")}, nil); e != nil || existing {
				return e
			}
			if e := tx.CompleteOperation(domain.OperationKey{PrincipalID: principal, OperationID: key}, domain.Response{Status: 201, Body: []byte(`{"data":{}}`)}); e != nil {
				return e
			}
			return tx.PutCheckIntent(domain.CheckIntent{ID: intentID, OperationID: key, PrincipalID: principal, Kind: "verification", CanonicalTarget: []byte(`{"route":"/x"}`), ConfigRevision: 1, PermissionsRevision: 1, State: "open", CreatedAt: "2026-01-01T00:00:00.000Z"})
		})).To(Succeed())
		err := f.core.Recover(context.Background())
		Expect(err).To(MatchError(appsource.ErrRecoveryContradiction))
		Expect(err.Error()).To(ContainSubstring(intentID), "the error names the intent")
		Expect(f.intentOpen(f.store, key)).To(BeTrue(), "the contradiction is not resolved silently")
	})
})
