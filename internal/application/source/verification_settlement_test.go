package source_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	importer "haeryong.local/haeryong/internal/application/import"
	appsource "haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// G2: a rolled-back failWithin takes the error path, never a settled 412.
var _ = It("TestPolicySettlementFailureTakesErrorPath", Label("persistence"), func() {
	t := GinkgoT()
	f := newVerificationFixture(t)
	f.reader.blobs[gitRevision+"\x00f.txt"] = "data\n"
	f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
	f.configure(gitMapping(t.TempDir()))
	key := "a1700000-0000-4000-8000-000000000001"
	// Advance the configuration between reservation and commit (a 412
	// policy failure), and resolve the intent first so the settlement's
	// ResolveCheckIntent fails and the whole transaction rolls back.
	f.reader.hook = func() {
		f.reader.hook = nil
		bumpConfig(t, f)
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			return tx.TestReopenResolveFail(key)
		})).To(Succeed())
	}
	_, e := f.verify(key, "")
	Expect(e).To(HaveOccurred(), "the rolled-back settlement takes the error path")
	receipt, e := f.auth.Operation(context.Background(), f.token, key)
	Expect(e).NotTo(HaveOccurred())
	Expect(receipt.State).To(Equal("accepted"), "no 412 was settled by the rolled-back transaction")
})

// G13: a settled storage failure returns the stored response itself, so the
// first response equals its replay.
var _ = It("TestSettledFailureResponseEqualsReplay", Label("persistence"), func() {
	t := GinkgoT()
	f := newVerificationFixture(t)
	f.reader.blobs[gitRevision+"\x00f.txt"] = "data\n"
	f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
	f.configure(gitMapping(t.TempDir()))
	key := "a1700000-0000-4000-8000-000000000002"
	// The commit transaction fails with an ordinary storage error (not a
	// test fault), so the quiet infrastructure settlement runs.
	failing := &armedStore{Store: f.store}
	f.core = appsource.New(failing, f.auth, f.store, f.reader)
	f.reader.hook = func() {
		f.reader.hook = nil
		failing.arm()
	}
	first, e := f.verify(key, "")
	Expect(e).NotTo(HaveOccurred(), "the settled response is returned, not the raw error")
	Expect(first.Status).To(Equal(503))
	Expect(errorCode(t, first)).To(Equal("SERVICE_RECOVERING"))
	Expect(string(first.Body)).To(ContainSubstring("stage_failure"))
	replay, e := f.verify(key, "")
	Expect(e).NotTo(HaveOccurred())
	Expect(replay.Status).To(Equal(first.Status))
	Expect(replay.Body).To(Equal(first.Body), "first response equals the replay")
})

// G10: an invalid-UTF-8 referenced expectation settles 400 with cleanup.
var _ = It("TestInvalidUTF8ReferenceSettles400", Label("persistence"), func() {
	t := GinkgoT()
	f := newVerificationFixture(t)
	core := importer.New(f.store, f.auth, f.store, staging.New())
	blob := "package main\n\nfunc main() {}\n"
	f.reader.blobs[gitRevision+"\x00pkg/main.go"] = blob
	f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go","line_start":3,"line_end":3}`, "", nil)
	f.configure(gitMapping(t.TempDir()))
	key := "a1700000-0000-4000-8000-000000000003"
	// Stage a sealed body expectation that is NOT valid UTF-8.
	uploadID := stageBodyExpectationAs(t, f, core, f.token, key, []byte("func main() {}\n\xff\xfe"))
	response, e := f.core.Verify(context.Background(), f.token, f.version, "ev", key, []byte(`{"expected_excerpt_ref":{"upload_id":"`+uploadID+`"}}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(400))
	Expect(errorCode(t, response)).To(Equal("INVALID_REQUEST"))
	receipt, e := f.auth.Operation(context.Background(), f.token, key)
	Expect(e).NotTo(HaveOccurred())
	Expect(receipt.State).To(Equal("failed"))
	Expect(receipt.Response.Status).To(Equal(400), "the stored failed receipt")
	principal := ownerPrincipal(f)
	var intent domain.CheckIntent
	var rows int
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		var e error
		if intent, e = tx.CheckIntentByOperation(principal, key); e != nil {
			return e
		}
		history, e := tx.Verifications(f.version, "ev", "", "", 10)
		rows = len(history)
		return e
	})).To(Succeed())
	Expect(intent.State).To(Equal("failed"), "the intent is resolved")
	Expect(rows).To(Equal(0), "no verification row")
	pins, _, e := f.store.SourceCheckObservations(context.Background())
	Expect(e).NotTo(HaveOccurred())
	Expect(pins).To(Equal(0), "the intent pin is released")
})

// G10: a failed lookup of the referenced expectation object rolls the whole
// reservation back: no receipt, no intent and no pin.
var _ = It("TestExpectationObjectLookupFailureRollsBackReservation", Label("persistence"), func() {
	t := GinkgoT()
	f := newVerificationFixture(t)
	core := importer.New(f.store, f.auth, f.store, staging.New())
	f.reader.blobs[gitRevision+"\x00pkg/main.go"] = "package main\n\nfunc main() {}\n"
	f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go","line_start":3,"line_end":3}`, "", nil)
	f.configure(gitMapping(t.TempDir()))
	key := "a1700000-0000-4000-8000-000000000006"
	uploadID := stageBodyExpectationAs(t, f, core, f.token, key, []byte("func main() {}\n"))
	f.core = appsource.New(objectFailingStore{Store: f.store}, f.auth, f.store, f.reader)
	_, e := f.verify(key, `{"expected_excerpt_ref":{"upload_id":"`+uploadID+`"}}`)
	Expect(e).To(MatchError(errStorageFailure), "the lookup failure is returned, not skipped")
	_, e = f.auth.Operation(context.Background(), f.token, key)
	Expect(e).To(MatchError(domain.ErrNotFound), "no receipt survives the rolled-back reservation")
	principal := ownerPrincipal(f)
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		_, e := tx.CheckIntentByOperation(principal, key)
		return e
	})).To(MatchError(domain.ErrNotFound), "no intent")
	pins, _, e := f.store.SourceCheckObservations(context.Background())
	Expect(e).NotTo(HaveOccurred())
	Expect(pins).To(Equal(0), "no pin")
})

// G8: the owner adopts a workflow principal's sealed body upload bound to
// exactly this operation; a binding for another operation is rejected.
var _ = It("TestOwnerAdoptsBoundForeignExpectation", Label("persistence"), func() {
	t := GinkgoT()
	f := newVerificationFixture(t)
	core := importer.New(f.store, f.auth, f.store, staging.New())
	f.reader.blobs[gitRevision+"\x00pkg/main.go"] = "package main\n\nfunc main() {}\n"
	f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go","line_start":3,"line_end":3}`, "", nil)
	f.configure(gitMapping(t.TempDir()))
	workflow := seedScopedSourceReader(t, f)
	key := "a1700000-0000-4000-8000-000000000010"
	uploadID := stageBodyExpectationAs(t, f, core, workflow, key, []byte("func main() {}\n"))
	Expect(uploadOwner(t, f, uploadID)).NotTo(Equal(ownerPrincipal(f)), "the upload belongs to the workflow principal")
	response, e := f.core.Verify(context.Background(), f.token, f.version, "ev", key, []byte(`{"expected_excerpt_ref":{"upload_id":"`+uploadID+`"}}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(201))
	Expect(outcome(t, response)["outcome"]).To(Equal("matched"), "the adopted expectation bytes were compared")
	other := stageBodyExpectationAs(t, f, core, workflow, "a1700000-0000-4000-8000-000000000011", []byte("func main() {}\n"))
	_, e = f.core.Verify(context.Background(), f.token, f.version, "ev", "a1700000-0000-4000-8000-000000000012", []byte(`{"expected_excerpt_ref":{"upload_id":"`+other+`"}}`))
	Expect(e).To(MatchError(sec.ErrInvalid), "a foreign upload bound to another operation is never adopted")
})

func uploadOwner(t GinkgoTInterface, f *vFixture, id string) string {
	t.Helper()
	var owner string
	ExpectWithOffset(1, f.store.Write(context.Background(), func(tx app.WriteTx) error {
		upload, e := tx.Upload(id)
		owner = upload.PrincipalID
		return e
	})).To(Succeed())
	return owner
}

// G14: read failures settle with a kind-neutral reason unless the git binary
// itself failed.
var _ = It("TestReadFailureReasonIsKindNeutral", Label("persistence"), func() {
	t := GinkgoT()
	// A retained-snapshot read that fails with an ordinary error settles as
	// FILE_ACCESS_ERROR with the kind-neutral reason.
	f := newVerificationFixture(t)
	asset := "retained bytes\n"
	f.buildVersion(`{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+vsha([]byte(asset))+`","asset_path":"snap.bin"}`, `{"id":"ev","source_id":"src","capture_status":"verified","path":"snap.bin"}`, "snap.bin", []byte(asset))
	f.core = appsource.New(f.store, f.auth, &erroringBytes{inner: f.store}, f.reader)
	response, e := f.verify("a1700000-0000-4000-8000-000000000004", "")
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(503))
	Expect(string(response.Body)).To(ContainSubstring("FILE_ACCESS_ERROR"))
	Expect(string(response.Body)).To(ContainSubstring("source_read_failed"))
	Expect(string(response.Body)).NotTo(ContainSubstring("git_unavailable"))

	// A genuine git-binary failure keeps the git-specific reason.
	g := newVerificationFixture(t)
	g.reader.blobs[gitRevision+"\x00f.txt"] = "data\n"
	g.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"f.txt"}`, "", nil)
	g.configure(gitMapping(t.TempDir()))
	g.core = appsource.New(g.store, g.auth, g.store, &unavailableReader{inner: g.reader})
	response, e = g.verify("a1700000-0000-4000-8000-000000000005", "")
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(503))
	Expect(string(response.Body)).To(ContainSubstring("git_unavailable"))
})

// erroringBytes fails every read with an ordinary infrastructure error.
type erroringBytes struct {
	inner appsource.Bytes
}

func (b *erroringBytes) Read(ctx context.Context, object domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) error {
	if err := b.inner.Read(ctx, object, pin, func(r io.ReadSeeker) error { return nil }); err != nil {
		return err
	}
	return errOrdinaryRead
}

var errOrdinaryRead = errors.New("snapshot device unavailable")

// unavailableReader reports the git-binary failure for every operation.
type unavailableReader struct {
	inner *fakeReader
}

func (r *unavailableReader) ResolveRevision(ctx context.Context, root, revision string) error {
	return appsource.ErrGitUnavailable
}

func (r *unavailableReader) ResolveHead(ctx context.Context, root string) (string, error) {
	return "", appsource.ErrGitUnavailable
}

func (r *unavailableReader) ChangedPaths(ctx context.Context, root, base, tip string, cited []string) ([]string, error) {
	return nil, appsource.ErrGitUnavailable
}

func (r *unavailableReader) ReadBlob(ctx context.Context, root, revision, path string) (io.ReadCloser, error) {
	return nil, appsource.ErrGitUnavailable
}

// armedStore fails the next writer transaction after arm, once, with an
// ordinary storage error (not a test fault).
type armedStore struct {
	app.Store
	mu    sync.Mutex
	armed bool
}

var errStorageFailure = errors.New("database disk image is malformed")

func (s *armedStore) arm() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed = true
}

func (s *armedStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	s.mu.Lock()
	armed := s.armed
	s.armed = false
	s.mu.Unlock()
	if armed {
		return errStorageFailure
	}
	return s.Store.Write(ctx, fn)
}

// objectFailingStore fails every object lookup inside its writer
// transactions with an ordinary storage error.
type objectFailingStore struct {
	app.Store
}

func (s objectFailingStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error { return fn(objectFailingTx{WriteTx: tx}) })
}

type objectFailingTx struct {
	app.WriteTx
}

func (objectFailingTx) Object(string) (domain.Object, error) {
	return domain.Object{}, errStorageFailure
}

// bumpConfig commits a new configuration revision with the same value.
func bumpConfig(t GinkgoTInterface, f *vFixture) {
	t.Helper()
	ExpectWithOffset(1, f.store.Write(context.Background(), func(tx app.WriteTx) error {
		current, e := tx.Configuration()
		if e != nil {
			return e
		}
		return tx.SetConfiguration(domain.Configuration{Revision: current.Revision, Value: current.Value, Actor: "fixture"}, current.Revision)
	})).To(Succeed())
}

func errorCode(t GinkgoTInterface, response domain.Response) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	ExpectWithOffset(1, json.Unmarshal(response.Body, &body)).To(Succeed())
	return body.Error.Code
}
