package query_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// viewFixture reuses the query fixture's store/auth/token and publishes a
// version with authored source/evidence rows written directly.
type evidenceView struct {
	fixture   *fixture
	versionID string
}

func documentRevision(t GinkgoTInterface, f *fixture) int64 {
	t.Helper()
	var revision int64
	ExpectWithOffset(1, f.store.Write(ctx, func(tx app.WriteTx) error {
		d, e := tx.QueryDocument(f.document)
		if e != nil {
			return e
		}
		revision = d.Revision
		return nil
	})).To(Succeed())
	return revision
}

func newEvidenceView(t GinkgoTInterface, sourceMeta string) *evidenceView {
	GinkgoHelper()
	f := newFixture(t)
	version := key(t)
	set := key(t)
	hash := hash("retained bytes\n")
	// Stage the retained object before the ledger transaction (the store
	// writer is not reentrant).
	object, e := f.store.Put(ctx, strings.NewReader("retained bytes\n"), func(tx app.WriteTx, staged domain.Object) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	Expect(object.Hash).To(Equal(hash))
	revision := documentRevision(t, f)
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.CreateContentSet(set, hash); e != nil {
			return e
		}
		if e := tx.PutEntry(domain.Entry{ContentSetID: set, Path: "snap.bin", Kind: "file", Hash: object.Hash, Size: object.Size}); e != nil {
			return e
		}
		if e := tx.SealContentSet(set); e != nil {
			return e
		}
		if e := tx.PutVersion(domain.Version{ID: version, DocumentID: f.document, BaseVersionID: f.version, ContentSetID: set, Title: "Doc", ViewerProfile: "html_js_v1", SourceBasis: "evidence_recorded", PayloadSHA: hash, ManifestSHA: hash}); e != nil {
			return e
		}
		if e := tx.PutSource(domain.Source{VersionID: version, ID: "src", Metadata: json.RawMessage(sourceMeta)}); e != nil {
			return e
		}
		if e := tx.PutEvidence(domain.Evidence{VersionID: version, ID: "ev", SourceID: "src", Metadata: json.RawMessage(`{"id":"ev","source_id":"src","capture_status":"verified","path":"snap.bin"}`)}); e != nil {
			return e
		}
		return tx.PublishVersion(version, revision)
	})).To(Succeed())
	return &evidenceView{fixture: f, versionID: version}
}

var _ = Describe("evidence view states", Label("persistence"), func() {
	t := GinkgoT()

	It("serves the retained excerpt for snapshot-kind evidence (state 2)", func() {
		view := newEvidenceView(t, `{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+hash("retained bytes\n")+`","asset_path":"snap.bin"}`)
		item, e := view.fixture.queries.Evidence(ctx, view.fixture.token, view.versionID, "ev")
		Expect(e).NotTo(HaveOccurred())
		Expect(item["availability"]).To(Equal("available"))
		Expect(*item["bytes_origin"].(*string)).To(Equal("retained_snapshot"))
		Expect(item["reason"]).To(BeNil())
	})

	It("reports missing_retained_bytes when the asset entry is absent (state 3a, snapshot kind)", func() {
		view := newEvidenceView(t, `{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+hash("retained bytes\n")+`","asset_path":"gone.bin"}`)
		item, e := view.fixture.queries.Evidence(ctx, view.fixture.token, view.versionID, "ev")
		Expect(e).NotTo(HaveOccurred())
		Expect(item["availability"]).To(Equal("unavailable"))
		Expect(item["reason"]).To(Equal("missing_retained_bytes"))
		Expect(item["bytes_origin"]).To(BeNil())
	})

	It("reports missing_retained_bytes when the asset entry is absent (state 3a, git selected_snapshot)", func() {
		view := newEvidenceView(t, `{"id":"src","kind":"git","retention_mode":"selected_snapshot","repo_id":"77777777-7777-4777-8777-777777777777","object_format":"sha1","exact_revision":"`+strings.Repeat("a1b2c3d4", 5)+`","asset_path":"gone.bin"}`)
		item, e := view.fixture.queries.Evidence(ctx, view.fixture.token, view.versionID, "ev")
		Expect(e).NotTo(HaveOccurred())
		Expect(item["availability"]).To(Equal("unavailable"))
		Expect(item["reason"]).To(Equal("missing_retained_bytes"))
	})

	It("withholds the excerpt when a permission generation advances mid-read (D9)", func() {
		view := newEvidenceView(t, `{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+hash("retained bytes\n")+`","asset_path":"snap.bin"}`)
		f := view.fixture
		original := f.queries.Bytes
		revoking := &revokingBytes{inner: original, project: f.project, store: f.store}
		f.queries.Bytes = revoking
		DeferCleanup(func() { f.queries.Bytes = original })
		item, e := f.queries.Evidence(ctx, f.token, view.versionID, "ev")
		Expect(e).NotTo(HaveOccurred())
		Expect(item["availability"]).To(Equal("unavailable"))
		Expect(item["reason"]).To(Equal("not_read_by_view"))
		Expect(item["excerpt"]).To(BeNil(), "no excerpt leaves the service after revocation")
	})
})

// revokingBytes advances the project permission generation during the
// retained-byte read, between the locked phase and the excerpt return.
type revokingBytes struct {
	inner   query.Bytes
	project string
	store   app.Store
	revoked bool
}

func (r *revokingBytes) Read(ctx context.Context, object domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) error {
	if !r.revoked {
		r.revoked = true
		Expect(r.store.Write(context.Background(), func(tx app.WriteTx) error {
			return tx.ApplyProjectConfiguration(r.project, "", "", true, 1)
		})).To(Succeed())
	}
	return r.inner.Read(ctx, object, pin, consume)
}

// S7/S8: the view uses the shared helper; the recheck returns errors for
// lost sessions/visibility and withholds only the excerpt on a permission
// move (already covered by the mid-read revocation spec).
var _ = Describe("evidence view states (W2c)", Label("persistence"), func() {
	t := GinkgoT()

	It("returns state 1 denied for a scoped reader without source.read", func() {
		view := newEvidenceView(t, `{"id":"src","kind":"git","retention_mode":"reference_only","repo_id":"77777777-7777-4777-8777-777777777777","exact_revision":"`+strings.Repeat("a1b2c3d4", 5)+`"}`)
		f := view.fixture
		token := seedViewScoped(t, f, view.versionID, false)
		item, e := f.queries.Evidence(ctx, token, view.versionID, "ev")
		Expect(e).NotTo(HaveOccurred())
		Expect(item["availability"]).To(Equal("denied"))
	})

	It("serves a scoped reader with version.read and source.read", func() {
		view := newEvidenceView(t, `{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+hash("retained bytes\n")+`","asset_path":"snap.bin"}`)
		f := view.fixture
		token := seedViewScoped(t, f, view.versionID, true)
		item, e := f.queries.Evidence(ctx, token, view.versionID, "ev")
		Expect(e).NotTo(HaveOccurred())
		Expect(*item["bytes_origin"].(*string)).To(Equal("retained_snapshot"))
	})

	It("answers 404 for an unknown evidence id in a visible version (S29)", func() {
		view := newEvidenceView(t, `{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+hash("retained bytes\n")+`","asset_path":"snap.bin"}`)
		_, e := view.fixture.queries.Verifications(ctx, view.fixture.token, view.versionID, "unknown-evidence", query.Options{})
		Expect(e).To(MatchError(domain.ErrNotFound))
	})

	It("rejects a foreign cursor binding on the history read (S26)", func() {
		view := newEvidenceView(t, `{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+hash("retained bytes\n")+`","asset_path":"snap.bin"}`)
		f := view.fixture
		_, e := f.queries.Evidence(ctx, f.token, view.versionID, "ev")
		Expect(e).NotTo(HaveOccurred())
		// Run one check to create history.
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			return tx.PutVerification(domain.CitationVerification{ID: "96000000-0000-4000-8000-000000000001", PrincipalID: "view-owner", VersionID: view.versionID, EvidenceID: "ev", SourceJSON: []byte(`{}`), LocationJSON: []byte(`{"path":"snap.bin"}`), Outcome: "captured", ConfigRevision: 1, PermissionsRevision: 1, CheckedAt: "2026-01-01T00:00:00.000Z", OperationID: "97000000-0000-4000-8000-000000000002"})
		})).To(Succeed())
		_, e = f.queries.Verifications(ctx, f.token, view.versionID, "ev", query.Options{Cursor: "aGVsbG8"})
		Expect(e).To(MatchError(&query.Error{Reason: "cursor_mismatch"}))
		_, e = f.queries.Verifications(ctx, f.token, view.versionID, "ev", query.Options{Limit: 500})
		Expect(e).To(MatchError(&query.Error{Reason: "invalid_page_size"}))
	})

	It("reports line_range_out_of_bounds when the retained excerpt range is past EOF (S12)", func() {
		view := newEvidenceView(t, `{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+hash("retained bytes\n")+`","asset_path":"snap.bin"}`)
		// Rebind the evidence to a range past EOF.
		Expect(view.fixture.store.Write(ctx, func(tx app.WriteTx) error {
			return tx.TestReplaceEvidence(view.versionID, "ev", json.RawMessage(`{"id":"ev","source_id":"src","capture_status":"verified","path":"snap.bin","line_start":50,"line_end":51}`))
		})).To(Succeed())
		item, e := view.fixture.queries.Evidence(ctx, view.fixture.token, view.versionID, "ev")
		Expect(e).NotTo(HaveOccurred())
		Expect(item["availability"]).To(Equal("unavailable"))
		Expect(item["reason"]).To(Equal("line_range_out_of_bounds"))
	})
})

// viewScopedPrincipal is the workflow principal seedViewScoped issues with
// source.read.
const viewScopedPrincipal = "98000000-0000-4000-8000-000000000001"

// seedViewScoped issues a scoped credential anchored to the fixture document's
// project and this version with the two byte-affecting actions.
func seedViewScoped(t GinkgoTInterface, f *fixture, version string, withSourceRead bool) string {
	t.Helper()
	secret, e := sec.Secret()
	ExpectWithOffset(1, e).NotTo(HaveOccurred())
	principal := "98000000-0000-4000-8000-000000000002"
	if withSourceRead {
		principal = viewScopedPrincipal
	}
	actions := []string{"version.read"}
	if withSourceRead {
		actions = append(actions, "source.read")
	}
	scope := domain.CredentialScope{Kind: "workflow", Actions: actions, ProjectIDs: []string{f.project}, VersionIDs: []string{version}}
	ExpectWithOffset(1, f.store.Write(ctx, func(tx app.WriteTx) error {
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
