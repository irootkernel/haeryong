package query_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// failingBytes surfaces an injected error from the retained read so the view's
// error handling is observable (G3).
type failingBytes struct {
	inner query.Bytes
	err   error
}

func (b *failingBytes) Read(ctx context.Context, object domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) error {
	if b.err != nil {
		return b.err
	}
	return b.inner.Read(ctx, object, pin, consume)
}

var _ = Describe("evidence view read errors (G3/W2d)", Label("persistence"), func() {
	t := GinkgoT()

	It("propagates a general read error instead of a false source fact", func() {
		view := newEvidenceView(t, `{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+hash("retained bytes\n")+`","asset_path":"snap.bin"}`)
		f := view.fixture
		original := f.queries.Bytes
		f.queries.Bytes = &failingBytes{inner: original, err: errors.New("disk went away")}
		DeferCleanup(func() { f.queries.Bytes = original })
		_, e := f.queries.Evidence(ctx, f.token, view.versionID, "ev")
		Expect(e).To(MatchError("disk went away"))
	})

	It("keeps the session recheck ahead of a retained read error", func() {
		view := newEvidenceView(t, `{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+hash("retained bytes\n")+`","asset_path":"snap.bin"}`)
		f := view.fixture
		original := f.queries.Bytes
		// The read fails AND the session is revoked mid-read: the recheck's
		// session error wins over the read error (G3 refinement).
		session, se := f.auth.Authenticate(ctx, f.token)
		Expect(se).NotTo(HaveOccurred())
		f.queries.Bytes = &changingFailingBytes{change: func() {
			_, e := f.auth.Revoke(context.Background(), f.token, session.ID)
			Expect(e).NotTo(HaveOccurred())
		}}
		DeferCleanup(func() { f.queries.Bytes = original })
		_, e := f.queries.Evidence(ctx, f.token, view.versionID, "ev")
		Expect(e).To(MatchError(sec.ErrRevoked))
		Expect(e).NotTo(MatchError(errReadAlongsideChange))
	})

	It("keeps a mid-read loss of source.read as state-1 denied over a read error", func() {
		view := newEvidenceView(t, `{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+hash("retained bytes\n")+`","asset_path":"snap.bin"}`)
		f := view.fixture
		token := seedViewScoped(t, f, view.versionID, true)
		original := f.queries.Bytes
		// The workflow principal loses source.read while its read fails.
		f.queries.Bytes = &changingFailingBytes{change: func() {
			Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
				return tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: viewScopedPrincipal, BindingID: viewScopedPrincipal, Active: true, Scope: domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{f.project}, VersionIDs: []string{view.versionID}}})
			})).To(Succeed())
		}}
		DeferCleanup(func() { f.queries.Bytes = original })
		item, e := f.queries.Evidence(ctx, token, view.versionID, "ev")
		Expect(e).NotTo(HaveOccurred(), "state-1 denied, not the read error")
		Expect(item["availability"]).To(Equal("denied"))
		Expect(item["bytes_origin"]).To(BeNil())
	})
})

var errReadAlongsideChange = errors.New("read failed alongside an authority change")

// changingFailingBytes applies one authority change during the retained
// read and then fails that read.
type changingFailingBytes struct {
	change func()
}

func (b *changingFailingBytes) Read(ctx context.Context, object domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) error {
	if b.change != nil {
		change := b.change
		b.change = nil
		change()
	}
	return errReadAlongsideChange
}

// G11: sorted multi-page source list and permission-bound owner cursors.
var _ = Describe("project sources pagination (G11)", Label("persistence"), func() {
	t := GinkgoT()

	It("sorts unsorted configuration input and pages deterministically", func() {
		view := newEvidenceView(t, `{"id":"src","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+hash("retained bytes\n")+`","asset_path":"snap.bin"}`)
		f := view.fixture
		// Store two mappings in deliberately unsorted order.
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			current, e := tx.Configuration()
			if e != nil {
				return e
			}
			var config map[string]any
			Expect(jsonUnmarshal(current.Value, &config)).NotTo(HaveOccurred())
			config["projects"].(map[string]any)[f.project] = map[string]any{"sources": []any{
				map[string]any{"source_id": "bbbbbbbb-0000-4000-8000-000000000002", "kind": "files", "root_path": "/b", "repo_id": nil, "retention_mode": "reference_only", "provider_ids": []any{}},
				map[string]any{"source_id": "aaaaaaaa-0000-4000-8000-000000000001", "kind": "files", "root_path": "/a", "repo_id": nil, "retention_mode": "reference_only", "provider_ids": []any{}},
			}}
			raw, e := jsonMarshalAny(config)
			if e != nil {
				return e
			}
			return tx.SetConfiguration(domain.Configuration{Revision: current.Revision, Value: raw, Actor: "fixture"}, current.Revision)
		})).To(Succeed())
		first, e := f.queries.ProjectSources(ctx, f.token, f.project, query.Options{Limit: 1})
		Expect(e).NotTo(HaveOccurred())
		Expect(first.Items).To(HaveLen(1))
		Expect(first.Items[0]["source_id"]).To(Equal("aaaaaaaa-0000-4000-8000-000000000001"), "sorted first regardless of configuration order")
		Expect(first.NextCursor).NotTo(BeNil())
		second, e := f.queries.ProjectSources(ctx, f.token, f.project, query.Options{Limit: 1, Cursor: *first.NextCursor})
		Expect(e).NotTo(HaveOccurred())
		Expect(second.Items).To(HaveLen(1))
		Expect(second.Items[0]["source_id"]).To(Equal("bbbbbbbb-0000-4000-8000-000000000002"))
		// A permission-generation change invalidates the cursor.
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			return tx.ApplyProjectConfiguration(f.project, "", "", true, 1)
		})).To(Succeed())
		_, e = f.queries.ProjectSources(ctx, f.token, f.project, query.Options{Limit: 1, Cursor: *first.NextCursor})
		Expect(e).To(MatchError(&query.Error{Reason: "cursor_mismatch"}))
	})
})

func jsonUnmarshal(raw []byte, into any) error {
	var d = jsonDecoder(raw)
	return d(into)
}

func jsonMarshalAny(v any) ([]byte, error) { return jsonMarshal(v) }

func jsonDecoder(raw []byte) func(any) error {
	return func(into any) error { return json.Unmarshal(raw, into) }
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
