package query_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"io"
	"strings"
	"sync"
	"time"
)

func noteBody(page *string, region *string, text string) []byte {
	return encodedNote(map[string]any{"page_path": page, "region_id": region, "page_query": "q=+%2F", "page_fragment": "part", "body": text, "body_ref": nil, "selected_text": "fallback", "context": map[string]any{"scenario": "original"}, "context_ref": nil})
}
func encodedNote(v any) []byte { raw, _ := json.Marshal(v); return raw }
func noteMutation(f *fixture, k, method, id, tag string, body []byte) (query.AnnotationView, error) {
	r, e := f.queries.AnnotationMutate(ctx, f.token, k, method, id, tag, body)
	var v query.AnnotationView
	if e == nil {
		e = json.Unmarshal(r.Body, &v)
	}
	return v, e
}

var _ = It("Annotations T-005-02-01 X19 C07 exact page anchors and original history", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	page, other, region := "hello.html", "pages/nested.html", "retry"
	a, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, &region, "first"))
	Expect(e).NotTo(HaveOccurred())
	b, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&other, &region, "second"))
	Expect(e).NotTo(HaveOccurred())
	Expect(a.Anchor.PagePath).To(Equal(&page))
	Expect(b.Anchor.PagePath).To(Equal(&other))
	Expect(a.Resolution.Status).To(Equal("unresolved"))
	// No region declarations are needed for a page note or whole-version note.
	raw := map[string]any{"page_path": nil, "region_id": nil, "page_query": "", "page_fragment": "", "body": "directory", "body_ref": nil, "selected_text": nil, "context": nil, "context_ref": nil}
	whole, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", encodedNote(raw))
	Expect(e).NotTo(HaveOccurred())
	Expect(whole.Anchor.PagePath).To(BeNil())
	first, e := f.queries.AnnotationList(ctx, f.token, f.version, query.Options{Limit: 1})
	Expect(e).NotTo(HaveOccurred())
	Expect(first.Items).To(HaveLen(1))
	Expect(first.NextCursor).NotTo(BeNil())
	next, e := f.queries.AnnotationList(ctx, f.token, f.version, query.Options{Limit: 1, Cursor: *first.NextCursor})
	Expect(e).NotTo(HaveOccurred())
	Expect(next.Items[0].AnnotationID).NotTo(Equal(first.Items[0].AnnotationID))
	// Reattachment is explicit and body-preserving; fallback context stays in old history.
	destination := domain.AnnotationAnchor{VersionID: f.version, PagePath: &other, PageQuery: "next=1", PageFragment: "next"}
	moved, e := noteMutation(f, key(GinkgoT()), "PATCH", a.AnnotationID, a.ETag, encodedNote(map[string]any{"action": "reattach", "anchor": destination, "selected_text": nil, "context": nil, "context_ref": nil}))
	Expect(e).NotTo(HaveOccurred())
	Expect(moved.Body).To(Equal(a.Body))
	Expect(moved.OriginalAnchor).To(Equal(a.Anchor))
	Expect(moved.Anchor).To(Equal(destination))
	h, e := f.queries.AnnotationHistory(ctx, f.token, a.AnnotationID, query.Options{})
	Expect(e).NotTo(HaveOccurred())
	Expect(h.Items).To(HaveLen(2))
	Expect(h.Items[0].Anchor).To(Equal(a.Anchor))
	Expect(h.Items[0].ContextRef).To(Equal(a.ContextRef))
	var contextBytes bytes.Buffer
	e = f.queries.ReadAnnotationObject(ctx, f.token, a.AnnotationID, 1, "context", func(_ domain.Object, _ string, r io.ReadSeeker) error { _, e := io.Copy(&contextBytes, r); return e })
	Expect(e).NotTo(HaveOccurred())
	Expect(contextBytes.String()).To(MatchJSON(`{"scenario":"original"}`))
	_, _, e = f.queries.AnnotationObject(ctx, f.token, a.AnnotationID, 2, "context")
	Expect(e).To(MatchError("not found: context_absent"))
	deleted, e := noteMutation(f, key(GinkgoT()), "DELETE", a.AnnotationID, moved.ETag, nil)
	Expect(e).NotTo(HaveOccurred())
	Expect(deleted.Body).To(BeNil())
	Expect(deleted.ContextRef).To(BeNil())
	h, e = f.queries.AnnotationHistory(ctx, f.token, a.AnnotationID, query.Options{})
	Expect(e).NotTo(HaveOccurred())
	Expect(h.Items).To(HaveLen(3))
	Expect(h.Items[0].Body).To(Equal(a.Body))
})
var _ = It("Annotations T-005-02-02 concurrent CAS replay and rollback of history pins events receipts", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	page := "hello.html"
	body := noteBody(&page, nil, "base")
	k := key(GinkgoT())
	var group sync.WaitGroup
	results := make([]query.AnnotationView, 2)
	errs := make([]error, 2)
	for i := range results {
		group.Add(1)
		go func() { defer group.Done(); results[i], errs[i] = noteMutation(f, k, "POST", f.version, "", body) }()
	}
	group.Wait()
	Expect(errs).To(Equal([]error{nil, nil}))
	Expect(results[0]).To(Equal(results[1]))
	n := results[0]
	edit := encodedNote(map[string]any{"action": "edit", "body": "new", "body_ref": nil, "selected_text": nil, "context": nil, "context_ref": nil})
	saved, e := noteMutation(f, key(GinkgoT()), "PATCH", n.AnnotationID, n.ETag, edit)
	Expect(e).NotTo(HaveOccurred())
	_, e = noteMutation(f, key(GinkgoT()), "PATCH", n.AnnotationID, n.ETag, edit)
	var conflict *query.PersonalConflict
	Expect(errors.As(e, &conflict)).To(BeTrue())
	Expect(*conflict.ETag).To(Equal(saved.ETag))
	replay, e := noteMutation(f, k, "POST", f.version, "", body)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(n))
	db, e := sql.Open("sqlite", f.dbPath)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	for _, trigger := range []string{`CREATE TRIGGER fail_note BEFORE INSERT ON object_pins WHEN NEW.owner_kind='annotation_history' BEGIN SELECT RAISE(ABORT,'pin');END`, `CREATE TRIGGER fail_note BEFORE INSERT ON annotation_history BEGIN SELECT RAISE(ABORT,'history');END`, `CREATE TRIGGER fail_note BEFORE INSERT ON events WHEN NEW.resource_kind='annotation' BEGIN SELECT RAISE(ABORT,'outbox');END`, `CREATE TRIGGER fail_note BEFORE UPDATE ON operation_receipts WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'receipt');END`} {
		_, e = db.Exec(trigger)
		Expect(e).NotTo(HaveOccurred())
		failedKey := key(GinkgoT())
		_, e = noteMutation(f, failedKey, "POST", f.version, "", body)
		Expect(e).To(HaveOccurred())
		var count int
		Expect(db.QueryRow(`SELECT count(*) FROM operation_receipts WHERE operation_id=?`, failedKey).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		Expect(db.QueryRow(`SELECT count(*) FROM annotations`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
		Expect(db.QueryRow(`SELECT count(*) FROM annotation_history`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(2))
		Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='context_prepare'`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		_, e = db.Exec(`DROP TRIGGER fail_note`)
		Expect(e).NotTo(HaveOccurred())
	}
	// Adopted context cannot be collected when its renderer preparation is gone.
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		c, e := tx.Context(*n.ContextRef)
		if e != nil {
			return e
		}
		_, e = tx.ReserveGC(c.Object.Hash, "collect")
		return e
	})).To(MatchError(domain.ErrPinned))
})
var _ = It("Revisit absent stored false filtering and Review independence", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	v, e := f.queries.RevisitGet(ctx, f.token, f.document)
	Expect(e).NotTo(HaveOccurred())
	Expect(v.Exists).To(BeFalse())
	response, e := f.queries.RevisitPut(ctx, f.token, key(GinkgoT()), f.document, "", true, []byte(`{"flag":false}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(json.Unmarshal(response.Body, &v)).To(Succeed())
	Expect(v.Exists).To(BeTrue())
	Expect(v.ETag).NotTo(BeNil())
	_, e = f.queries.RevisitPut(ctx, f.token, key(GinkgoT()), f.document, *v.ETag, false, []byte(`{"flag":true}`))
	Expect(e).NotTo(HaveOccurred())
	list, e := f.queries.Documents(ctx, f.token, query.Options{Revisit: "true"})
	Expect(e).NotTo(HaveOccurred())
	Expect(list.Items).To(HaveLen(1))
	Expect(*list.Items[0].Revisit).To(BeTrue())
	r, e := f.queries.ReviewGet(ctx, f.token, f.version)
	Expect(e).NotTo(HaveOccurred())
	Expect(r.Exists).To(BeFalse())
})

type noteBarrierStore struct {
	app.Store
	entered chan domain.Pin
	release chan struct{}
}
type noteBarrierTx struct {
	app.WriteTx
	barrier *noteBarrierStore
}

func (s *noteBarrierStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error { return fn(&noteBarrierTx{WriteTx: tx, barrier: s}) })
}
func (t *noteBarrierTx) AcquirePin(pin domain.Pin) error {
	if pin.Kind == "annotation_history" {
		t.barrier.entered <- pin
		<-t.barrier.release
	}
	return t.WriteTx.AcquirePin(pin)
}

var _ = It("Annotations Save context lifetime wins a concurrent collection reservation", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	barrier := &noteBarrierStore{Store: f.store, entered: make(chan domain.Pin, 1), release: make(chan struct{})}
	f.queries.Store = barrier
	page := "hello.html"
	saved := make(chan error, 1)
	go func() {
		_, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, nil, "race"))
		saved <- e
	}()
	pin := <-barrier.entered
	started := make(chan struct{})
	collected := make(chan error, 1)
	go func() { close(started); collected <- f.store.Collect(ctx, domain.Object{Hash: pin.Hash}) }()
	<-started
	close(barrier.release)
	Expect(<-saved).To(Succeed())
	Expect(<-collected).To(MatchError(domain.ErrPinned))
	rows, e := f.queries.AnnotationList(ctx, f.token, f.version, query.Options{})
	Expect(e).NotTo(HaveOccurred())
	Expect(rows.Items).To(HaveLen(1))
	var result bytes.Buffer
	e = f.queries.ReadAnnotationObject(ctx, f.token, rows.Items[0].AnnotationID, 1, "context", func(_ domain.Object, _ string, r io.ReadSeeker) error { _, e := io.Copy(&result, r); return e })
	Expect(e).NotTo(HaveOccurred())
	Expect(result.String()).To(MatchJSON(`{"scenario":"original"}`))
})

var _ = It("Annotations and Revisit owner-only views receipts and batched event visibility", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	page := "hello.html"
	k := key(GinkgoT())
	n, e := noteMutation(f, k, "POST", f.version, "", noteBody(&page, nil, "private note"))
	Expect(e).NotTo(HaveOccurred())
	_, e = f.queries.RevisitPut(ctx, f.token, key(GinkgoT()), f.document, "", true, []byte(`{"flag":true}`))
	Expect(e).NotTo(HaveOccurred())
	token, e := sec.Secret()
	Expect(e).NotTo(HaveOccurred())
	principal := key(GinkgoT())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		state, e := tx.AuthState()
		if e != nil {
			return e
		}
		p, e := tx.Scope(f.project)
		if e != nil {
			return e
		}
		scope := domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read", "project.read"}, ProjectIDs: []string{f.project}, VersionIDs: []string{f.version}}
		if e = tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: principal, Active: true, Scope: scope, Revision: 1}); e != nil {
			return e
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: principal, PrincipalID: principal, TokenHash: sec.Verifier("workflow", token), AuthEpoch: state.AuthEpoch, Scope: scope, Permissions: map[string]int64{f.project: p.Project.PermissionsRevision}, ConfigRevision: p.ConfigRevision, CreatedAt: time.Now().UTC(), Revision: 1})
	})).To(Succeed())
	_, e = f.queries.AnnotationGet(ctx, token, n.AnnotationID)
	Expect(e).To(MatchError(domain.ErrDenied))
	_, e = f.queries.AnnotationList(ctx, token, f.version, query.Options{})
	Expect(e).To(MatchError(domain.ErrDenied))
	_, e = f.queries.RevisitGet(ctx, token, f.document)
	Expect(e).To(MatchError(domain.ErrDenied))
	_, e = f.queries.Documents(ctx, token, query.Options{Revisit: "true"})
	Expect(e).To(MatchError(domain.ErrDenied))
	doc, e := f.queries.Document(ctx, token, f.document)
	Expect(e).NotTo(HaveOccurred())
	Expect(doc.Revisit).To(BeNil())
	var epoch string
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; epoch, _, e = tx.EventHead(); return e })).To(Succeed())
	for _, caller := range []string{f.token, token} {
		events, e := f.auth.Events(ctx, caller, epoch, 0)
		Expect(e).NotTo(HaveOccurred())
		for _, event := range events {
			single, e := f.auth.FilterEvent(ctx, caller, event)
			Expect(e).NotTo(HaveOccurred())
			if caller == token {
				Expect(single.ResourceKind).NotTo(BeElementOf("annotation", "revisit"))
			}
		}
	}
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.TestTrashDocument(f.document) })).To(Succeed())
	_, e = f.auth.Operation(ctx, f.token, k)
	Expect(e).To(MatchError(domain.ErrNotFound))
	_, _, e = f.queries.AnnotationObject(ctx, f.token, n.AnnotationID, 1, "context")
	Expect(e).To(MatchError(domain.ErrNotFound))
	events, e := f.auth.Events(ctx, f.token, epoch, 0)
	Expect(e).NotTo(HaveOccurred())
	for _, event := range events {
		Expect(event.ResourceKind).NotTo(BeElementOf("annotation", "revisit"))
	}
})

// Real sealed uploads/contexts: changing a handle alone is immaterial, but
// its operation/target or version/page/render/session binding is not.
func annotationSealedBody(f *fixture, target map[string]any) string {
	GinkgoHelper()
	r, e := f.core.Create(ctx, f.token, key(GinkgoT()), importer.CreateRequest{Purpose: "body", InputMode: "object", Target: encodedNote(target)})
	Expect(e).NotTo(HaveOccurred())
	var created struct {
		ID string `json:"upload_id"`
	}
	Expect(json.Unmarshal(r.Body, &created)).To(Succeed())
	text := "identical note object"
	length := int64(len(text))
	ack, e := f.core.Put(ctx, f.token, created.ID, importer.Chunk{Path: "_input", Kind: "file", Hash: hash(text), Length: &length, Final: true}, strings.NewReader(text))
	Expect(e).NotTo(HaveOccurred())
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	r, e = f.core.Complete(ctx, f.token, created.ID, key(GinkgoT()), cp)
	Expect(e).NotTo(HaveOccurred())
	var sealed struct {
		ID string `json:"job_id"`
	}
	Expect(json.Unmarshal(r.Body, &sealed)).To(Succeed())
	Expect(f.core.Process(ctx, sealed.ID)).To(Succeed())
	return created.ID
}
func annotationBoundContext(f *fixture, page, render string) string {
	GinkgoHelper()
	r, e := f.core.CreateContext(ctx, f.token, key(GinkgoT()), importer.ContextQuery{VersionID: f.version, PagePath: &page, RenderInstanceID: &render, Input: "direct", MediaType: "text/plain"}, strings.NewReader("identical context bytes"))
	Expect(e).NotTo(HaveOccurred())
	var result struct {
		ID string `json:"context_ref"`
	}
	Expect(json.Unmarshal(r.Body, &result)).To(Succeed())
	return result.ID
}
func annotationStateCounts(db *sql.DB) map[string]int {
	GinkgoHelper()
	counts := map[string]int{}
	for _, table := range []string{"annotations", "annotation_history", "object_pins", "object_references", "events", "operation_receipts"} {
		var n int
		Expect(db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n)).To(Succeed())
		counts[table] = n
	}
	return counts
}

var _ = It("Annotations RR1-F3 receipt identity retains body operation target and context binding on create and edit", Label("persistence"), func() {
	for _, action := range []string{"create", "edit"} {
		f := newFixture(GinkgoT())
		page := "hello.html"
		operation := key(GinkgoT())
		method, target, tag := "POST", f.version, ""
		raw := map[string]any{"page_path": page, "region_id": nil, "page_query": "", "page_fragment": "", "body": nil, "body_ref": nil, "selected_text": nil, "context": nil, "context_ref": nil}
		binding := map[string]any{"operation_id": operation, "action": "annotation_create", "version_id": f.version, "page_path": page}
		if action == "edit" {
			base, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, nil, "base"))
			Expect(e).NotTo(HaveOccurred())
			method, target, tag = "PATCH", base.AnnotationID, base.ETag
			raw = map[string]any{"action": "edit", "body": nil, "body_ref": nil, "selected_text": nil, "context": nil, "context_ref": nil}
			binding = map[string]any{"operation_id": operation, "action": "annotation_edit", "annotation_id": target}
		}
		first, alternate := annotationSealedBody(f, binding), annotationSealedBody(f, binding)
		wrongOperation := map[string]any{}
		for k, v := range binding {
			wrongOperation[k] = v
		}
		wrongOperation["operation_id"] = key(GinkgoT())
		wrongTarget := map[string]any{}
		for k, v := range binding {
			wrongTarget[k] = v
		}
		if action == "create" {
			wrongTarget["page_path"] = "pages/nested.html"
		} else {
			other, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, nil, "other"))
			Expect(e).NotTo(HaveOccurred())
			wrongTarget["annotation_id"] = other.AnnotationID
		}
		wrongHandles := []string{annotationSealedBody(f, wrongOperation), annotationSealedBody(f, wrongTarget)}
		render := key(GinkgoT())
		contextID, contextAlternate := annotationBoundContext(f, page, render), annotationBoundContext(f, page, render)
		wrongContexts := []string{annotationBoundContext(f, "pages/nested.html", render), annotationBoundContext(f, page, key(GinkgoT()))}
		raw["body_ref"], raw["context_ref"] = map[string]string{"upload_id": first}, contextID
		saved, e := noteMutation(f, operation, method, target, tag, encodedNote(raw))
		Expect(e).NotTo(HaveOccurred())
		db, e := sql.Open("sqlite", f.dbPath)
		Expect(e).NotTo(HaveOccurred())
		defer db.Close()
		before := annotationStateCounts(db)
		raw["body_ref"], raw["context_ref"] = map[string]string{"upload_id": alternate}, contextAlternate
		replay, e := noteMutation(f, operation, method, target, tag, encodedNote(raw))
		Expect(e).NotTo(HaveOccurred())
		Expect(replay).To(Equal(saved))
		Expect(annotationStateCounts(db)).To(Equal(before))
		raw["body"], raw["body_ref"] = "identical note object", nil
		inlineReplay, e := noteMutation(f, operation, method, target, tag, encodedNote(raw))
		Expect(e).NotTo(HaveOccurred())
		Expect(inlineReplay).To(Equal(saved))
		Expect(annotationStateCounts(db)).To(Equal(before))
		raw["body"] = nil
		for _, wrong := range wrongHandles {
			raw["body_ref"] = map[string]string{"upload_id": wrong}
			_, e = noteMutation(f, operation, method, target, tag, encodedNote(raw))
			Expect(e).To(MatchError(domain.ErrConflict))
			Expect(annotationStateCounts(db)).To(Equal(before))
		}
		raw["body_ref"] = map[string]string{"upload_id": alternate}
		for _, wrong := range wrongContexts {
			raw["context_ref"] = wrong
			_, e = noteMutation(f, operation, method, target, tag, encodedNote(raw))
			Expect(e).To(MatchError(domain.ErrConflict))
			Expect(annotationStateCounts(db)).To(Equal(before))
		}
		current, e := f.queries.AnnotationGet(ctx, f.token, saved.AnnotationID)
		Expect(e).NotTo(HaveOccurred())
		Expect(current.Revision).To(Equal(saved.Revision))
		Expect(current.ETag).To(Equal(saved.ETag))
	}
})

func noteOwnerSession(f *fixture) string {
	GinkgoHelper()
	p, e := f.auth.CreatePairing(ctx, "http://127.0.0.1:9270", "another note owner", "test")
	Expect(e).NotTo(HaveOccurred())
	_, e = f.auth.Decide(ctx, f.token, domain.PairingDecision{ID: p.ID, Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: key(GinkgoT()), Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	poll, e := f.auth.Poll(ctx, p.ID, p.PollingSecret, p.Origin)
	Expect(e).NotTo(HaveOccurred())
	return poll.Token
}

var _ = It("Annotations RR2-A-1 inline and saved context representations share stored binding on create and edit", Label("persistence"), func() {
	for _, action := range []string{"create", "edit"} {
		f := newFixture(GinkgoT())
		page, other := "hello.html", "pages/nested.html"
		operation := key(GinkgoT())
		method, target, tag := "POST", f.version, ""
		raw := map[string]any{"page_path": page, "region_id": nil, "page_query": "", "page_fragment": "", "body": "same note", "body_ref": nil, "selected_text": nil, "context": map[string]any{"scenario": "equivalent"}, "context_ref": nil}
		if action == "edit" {
			base, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, nil, "base"))
			Expect(e).NotTo(HaveOccurred())
			method, target, tag = "PATCH", base.AnnotationID, base.ETag
			delete(raw, "page_path")
			delete(raw, "region_id")
			delete(raw, "page_query")
			delete(raw, "page_fragment")
			raw["action"] = "edit"
		}
		saved, e := noteMutation(f, operation, method, target, tag, encodedNote(raw))
		Expect(e).NotTo(HaveOccurred())
		Expect(saved.ContextRef).NotTo(BeNil())
		var stored domain.ContextRecord
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var err error; stored, err = tx.Context(*saved.ContextRef); return err })).To(Succeed())
		equivalent := map[string]any{}
		for k, v := range raw {
			equivalent[k] = v
		}
		equivalent["context"], equivalent["context_ref"] = nil, *saved.ContextRef
		replay, e := noteMutation(f, operation, method, target, tag, encodedNote(equivalent))
		Expect(e).NotTo(HaveOccurred())
		Expect(replay).To(Equal(saved))
		// The implicit original binding survives a later reattach and another
		// authorized owner session, rather than following today's anchor/session.
		moved, e := noteMutation(f, key(GinkgoT()), "PATCH", saved.AnnotationID, saved.ETag, encodedNote(map[string]any{"action": "reattach", "anchor": domain.AnnotationAnchor{VersionID: f.version, PagePath: &other}, "selected_text": nil, "context": nil, "context_ref": nil}))
		Expect(e).NotTo(HaveOccurred())
		another := noteOwnerSession(f)
		wrong := stored
		wrong.ID = key(GinkgoT())
		wrong.PagePath = &other
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.PutContext(wrong) })).To(Succeed())
		db, e := sql.Open("sqlite", f.dbPath)
		Expect(e).NotTo(HaveOccurred())
		defer db.Close()
		before := annotationStateCounts(db)
		r, e := f.queries.AnnotationMutate(ctx, another, operation, method, target, tag, encodedNote(raw))
		Expect(e).NotTo(HaveOccurred())
		var after query.AnnotationView
		Expect(json.Unmarshal(r.Body, &after)).To(Succeed())
		Expect(after).To(Equal(saved))
		replay, e = noteMutation(f, operation, method, target, tag, encodedNote(equivalent))
		Expect(e).NotTo(HaveOccurred())
		Expect(replay).To(Equal(saved))
		different := map[string]any{}
		for k, v := range equivalent {
			different[k] = v
		}
		different["context_ref"] = wrong.ID
		_, e = noteMutation(f, operation, method, target, tag, encodedNote(different))
		Expect(e).To(MatchError(domain.ErrConflict))
		different["context"], different["context_ref"] = map[string]any{"scenario": "different"}, nil
		_, e = noteMutation(f, operation, method, target, tag, encodedNote(different))
		Expect(e).To(MatchError(domain.ErrConflict))
		Expect(annotationStateCounts(db)).To(Equal(before))
		current, e := f.queries.AnnotationGet(ctx, f.token, saved.AnnotationID)
		Expect(e).NotTo(HaveOccurred())
		Expect(current.ETag).To(Equal(moved.ETag))
	}
})

type inlineContextRaceStore struct {
	app.Store
	operation string
	mu        sync.Mutex
	arrived   int
	ready     chan struct{}
}
type inlineContextRaceTx struct {
	app.WriteTx
	operation string
	missed    bool
}

func (t *inlineContextRaceTx) Receipt(k domain.OperationKey) (domain.Receipt, error) {
	r, e := t.WriteTx.Receipt(k)
	if k.OperationID == t.operation && errors.Is(e, domain.ErrNotFound) {
		t.missed = true
	}
	return r, e
}
func (s *inlineContextRaceStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	var probe *inlineContextRaceTx
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		probe = &inlineContextRaceTx{WriteTx: tx, operation: s.operation}
		return fn(probe)
	})
	if err == nil && probe.missed {
		s.mu.Lock()
		s.arrived++
		if s.arrived == 2 {
			close(s.ready)
		}
		s.mu.Unlock()
		select {
		case <-s.ready:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

var _ = It("Annotations RR2-A-1 concurrent inline sessions replay the first committed implicit binding", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	another := noteOwnerSession(f)
	operation := key(GinkgoT())
	observed := &inlineContextRaceStore{Store: f.store, operation: operation, ready: make(chan struct{})}
	queries := query.New(observed, f.auth, f.store)
	body := noteBody(nil, nil, "concurrent inline")
	var raw map[string]any
	Expect(json.Unmarshal(body, &raw)).To(Succeed())
	raw["page_query"], raw["page_fragment"] = "", ""
	body = encodedNote(raw)
	responses := make([]domain.Response, 2)
	errs := make([]error, 2)
	var group sync.WaitGroup
	for i, token := range []string{f.token, another} {
		group.Add(1)
		go func() {
			defer group.Done()
			responses[i], errs[i] = queries.AnnotationMutate(ctx, token, operation, "POST", f.version, "", body)
		}()
	}
	group.Wait()
	Expect(errs).To(Equal([]error{nil, nil}))
	Expect(responses[0]).To(Equal(responses[1]))
	Expect(observed.arrived).To(Equal(2))
	notes, e := f.queries.AnnotationList(ctx, f.token, f.version, query.Options{})
	Expect(e).NotTo(HaveOccurred())
	Expect(notes.Items).To(HaveLen(1))
	history, e := f.queries.AnnotationHistory(ctx, f.token, notes.Items[0].AnnotationID, query.Options{})
	Expect(e).NotTo(HaveOccurred())
	Expect(history.Items).To(HaveLen(1))
})

func annotationWriteSnapshot(db *sql.DB) map[string][]string {
	GinkgoHelper()
	out := map[string][]string{}
	tables := map[string]string{"annotations": "annotation_id", "annotation_history": "annotation_id,revision", "revisit_flags": "owner_id,document_id", "contexts": "context_id", "content_objects": "content_hash", "object_pins": "pin_id", "object_references": "owner_kind,owner_id,content_hash", "events": "sequence", "operation_receipts": "principal_id,operation_id"}
	for table, order := range tables {
		rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY ` + order)
		Expect(err).NotTo(HaveOccurred())
		columns, err := rows.Columns()
		Expect(err).NotTo(HaveOccurred())
		out[table] = []string{}
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			Expect(rows.Scan(dest...)).To(Succeed())
			for i, v := range values {
				if b, ok := v.([]byte); ok {
					values[i] = string(b)
				}
			}
			out[table] = append(out[table], string(encodedNote(values)))
		}
		Expect(rows.Err()).NotTo(HaveOccurred())
		Expect(rows.Close()).To(Succeed())
	}
	return out
}
func annotationReaderToken(f *fixture) string {
	GinkgoHelper()
	token, e := sec.Secret()
	Expect(e).NotTo(HaveOccurred())
	principal := key(GinkgoT())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		state, e := tx.AuthState()
		if e != nil {
			return e
		}
		p, e := tx.Scope(f.project)
		if e != nil {
			return e
		}
		scope := domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read", "project.read"}, ProjectIDs: []string{f.project}, VersionIDs: []string{f.version}}
		if e = tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: principal, Active: true, Scope: scope, Revision: 1}); e != nil {
			return e
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: principal, PrincipalID: principal, TokenHash: sec.Verifier("workflow", token), AuthEpoch: state.AuthEpoch, Scope: scope, Permissions: map[string]int64{f.project: p.Project.PermissionsRevision}, ConfigRevision: p.ConfigRevision, CreatedAt: time.Now().UTC(), Revision: 1})
	})).To(Succeed())
	return token
}

var _ = It("Annotations Revisit EE1-1 active archived writes and exact blocked lifecycle with no durable mutation", Label("persistence"), func() {
	for _, life := range []string{"active", "archived", "trash", "purged"} {
		f := newFixture(GinkgoT())
		page, other := "hello.html", "pages/nested.html"
		noteKey := key(GinkgoT())
		originalBody := noteBody(&page, nil, "saved note")
		edit, e := noteMutation(f, noteKey, "POST", f.version, "", originalBody)
		Expect(e).NotTo(HaveOccurred())
		move, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, nil, "move"))
		Expect(e).NotTo(HaveOccurred())
		remove, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, nil, "delete"))
		Expect(e).NotTo(HaveOccurred())
		revisitKey := key(GinkgoT())
		r, e := f.queries.RevisitPut(ctx, f.token, revisitKey, f.document, "", true, []byte(`{"flag":false}`))
		Expect(e).NotTo(HaveOccurred())
		var revisit query.RevisitView
		Expect(json.Unmarshal(r.Body, &revisit)).To(Succeed())
		reader := annotationReaderToken(f)
		db, e := sql.Open("sqlite", f.dbPath)
		Expect(e).NotTo(HaveOccurred())
		defer db.Close()
		_, e = db.Exec(`UPDATE documents SET lifecycle=? WHERE document_id=?`, life, f.document)
		Expect(e).NotTo(HaveOccurred())
		before := annotationWriteSnapshot(db)
		type mutation struct {
			name, method, id, etag string
			body                   []byte
		}
		calls := []mutation{
			{"create", "POST", f.version, "", noteBody(&page, nil, "new inline context")},
			{"edit", "PATCH", edit.AnnotationID, edit.ETag, encodedNote(map[string]any{"action": "edit", "body": "edited", "body_ref": nil, "selected_text": nil, "context": "new edit context", "context_ref": nil})},
			{"reattach", "PATCH", move.AnnotationID, move.ETag, encodedNote(map[string]any{"action": "reattach", "anchor": domain.AnnotationAnchor{VersionID: f.version, PagePath: &other}, "selected_text": nil, "context": "new move context", "context_ref": nil})},
			{"delete", "DELETE", remove.AnnotationID, remove.ETag, nil},
		}
		blocked := life == "trash" || life == "purged"
		for _, call := range calls {
			_, err := noteMutation(f, key(GinkgoT()), call.method, call.id, call.etag, call.body)
			if blocked {
				var transition *query.Transition
				Expect(errors.As(err, &transition)).To(BeTrue(), call.name)
				Expect(transition.Lifecycle).To(Equal(life))
				Expect(annotationWriteSnapshot(db)).To(Equal(before), call.name)
			} else {
				Expect(err).NotTo(HaveOccurred(), call.name)
			}
		}
		_, err := f.queries.RevisitPut(ctx, f.token, key(GinkgoT()), f.document, *revisit.ETag, false, []byte(`{"flag":true}`))
		if blocked {
			var transition *query.Transition
			Expect(errors.As(err, &transition)).To(BeTrue())
			Expect(transition.Lifecycle).To(Equal(life))
			Expect(annotationWriteSnapshot(db)).To(Equal(before))
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
		// A valid reader credential receives the same nondisclosing write error
		// for known and unknown identities, independent of document lifecycle.
		before = annotationWriteSnapshot(db)
		for _, call := range calls {
			for _, target := range []string{call.id, key(GinkgoT())} {
				_, err := f.queries.AnnotationMutate(ctx, reader, key(GinkgoT()), call.method, target, call.etag, call.body)
				Expect(err).To(MatchError(domain.ErrNotFound))
				Expect(annotationWriteSnapshot(db)).To(Equal(before))
			}
		}
		for _, doc := range []string{f.document, key(GinkgoT())} {
			_, err := f.queries.RevisitPut(ctx, reader, key(GinkgoT()), doc, *revisit.ETag, false, []byte(`{"flag":true}`))
			Expect(err).To(MatchError(domain.ErrNotFound))
			Expect(annotationWriteSnapshot(db)).To(Equal(before))
		}
		for _, call := range calls {
			_, err := noteMutation(f, key(GinkgoT()), call.method, key(GinkgoT()), call.etag, call.body)
			Expect(err).To(MatchError(domain.ErrNotFound))
			Expect(annotationWriteSnapshot(db)).To(Equal(before))
		}
		if blocked {
			_, err = f.queries.AnnotationGet(ctx, f.token, edit.AnnotationID)
			Expect(err).To(MatchError(domain.ErrNotFound))
			_, err = f.queries.RevisitGet(ctx, f.token, f.document)
			Expect(err).To(MatchError(domain.ErrNotFound))
			_, err = f.queries.AnnotationMutate(ctx, f.token, noteKey, "POST", f.version, "", originalBody)
			Expect(err).To(MatchError(domain.ErrNotFound))
			_, err = f.auth.Operation(ctx, f.token, noteKey)
			Expect(err).To(MatchError(domain.ErrNotFound))
			_, err = f.queries.RevisitPut(ctx, f.token, revisitKey, f.document, "", true, []byte(`{"flag":false}`))
			Expect(err).To(MatchError(domain.ErrNotFound))
			_, err = f.auth.Operation(ctx, f.token, revisitKey)
			Expect(err).To(MatchError(domain.ErrNotFound))
			Expect(annotationWriteSnapshot(db)).To(Equal(before))
		}
	}
})
