package query_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"sync"
	"time"
)

func lifecyclePlan(f *fixture, op string) domain.LifecyclePage {
	GinkgoHelper()
	d, e := f.store.Document(ctx, f.document)
	Expect(e).NotTo(HaveOccurred())
	r, e := f.queries.LifecyclePlanCreate(ctx, f.token, "", f.document, []byte(fmt.Sprintf(`{"operation":%q,"expected_revision":%d}`, op, d.Revision)))
	Expect(e).NotTo(HaveOccurred())
	var p domain.LifecyclePage
	Expect(json.Unmarshal(r.Body, &p)).To(Succeed())
	return p
}
func lifecycleBody(p domain.LifecyclePage) []byte {
	return []byte(fmt.Sprintf(`{"plan_id":%q,"expected_revision":%d,"confirmed":true}`, p.ID, p.ExpectedRevision))
}
func lifecycleCommit(f *fixture, p domain.LifecyclePage) domain.LifecycleResult {
	GinkgoHelper()
	r, e := f.queries.LifecycleCommit(ctx, f.token, p.ID, "", lifecycleBody(p))
	Expect(e).NotTo(HaveOccurred())
	var v domain.LifecycleResult
	Expect(json.Unmarshal(r.Body, &v)).To(Succeed())
	return v
}

var _ = It("Lifecycle document transitions reject resurrection and preserve previous state", Label("unit"), func() {
	for _, row := range [][4]string{{"active", "", "archive", "archived"}, {"archived", "", "archive", "archived"}, {"active", "", "trash", "trash"}, {"archived", "", "restore", "active"}, {"trash", "archived", "restore", "archived"}, {"trash", "active", "restore", "active"}} {
		next, e := query.LifecycleNext(row[0], row[1], row[2])
		Expect(e).NotTo(HaveOccurred())
		Expect(next).To(Equal(row[3]))
	}
	for _, state := range []string{"purged", "trash", "active"} {
		_, e := query.LifecycleNext(state, "", "restore")
		Expect(e).To(HaveOccurred())
	}
})
var _ = It("Lifecycle real archive trash restore preserves Review Revisit Notes and eligible-input authority T-005-03", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	review, e := reviewPut(f, key(GinkgoT()), "complete", "", true)
	Expect(e).NotTo(HaveOccurred())
	_, e = f.queries.RevisitPut(ctx, f.token, key(GinkgoT()), f.document, "", true, []byte(`{"flag":true}`))
	Expect(e).NotTo(HaveOccurred())
	p := lifecyclePlan(f, "archive")
	archived := lifecycleCommit(f, p)
	Expect(archived.Lifecycle).To(Equal("archived"))
	noop := lifecycleCommit(f, lifecyclePlan(f, "archive"))
	Expect(noop.Changed).To(BeFalse())
	Expect(noop.Revision).To(Equal(archived.Revision))
	still, e := f.queries.ReviewGet(ctx, f.token, f.version)
	Expect(e).NotTo(HaveOccurred())
	Expect(still.Value).To(Equal(review.Value))
	p = lifecyclePlan(f, "trash")
	trashed := lifecycleCommit(f, p)
	Expect(*trashed.PreviousLifecycle).To(Equal("archived"))
	Expect(trashed.TrashedAt).NotTo(BeNil())
	_, e = f.queries.ReviewGet(ctx, f.token, f.version)
	Expect(e).To(MatchError(domain.ErrNotFound))
	actor, e := f.auth.Authenticate(ctx, f.token)
	Expect(e).NotTo(HaveOccurred())
	// Labelled synthetic AI input request uses the production shared predicate.
	e = f.store.Write(ctx, func(tx app.WriteTx) error { _, e := source.Check(tx, actor, f.version); return e })
	Expect(e).To(MatchError(domain.ErrNotFound))
	// Labelled synthetic maintenance request has no owner lifecycle authority and
	// cannot recreate/restore a blocked target through the production target policy.
	raw := []byte(fmt.Sprintf(`{"project_id":%q,"document_id":%q}`, f.project, f.document))
	e = f.store.Write(ctx, func(tx app.WriteTx) error { return app.AuthorizeTarget(tx, raw) })
	Expect(e).To(MatchError(domain.ErrNotFound))
	list, e := f.queries.Trash(ctx, f.token, query.Options{})
	Expect(e).NotTo(HaveOccurred())
	Expect(list.Items).To(HaveLen(1))
	p = lifecyclePlan(f, "restore")
	restored := lifecycleCommit(f, p)
	Expect(restored.Lifecycle).To(Equal("archived"))
	Expect(restored.PreviousLifecycle).To(BeNil())
	still, e = f.queries.ReviewGet(ctx, f.token, f.version)
	Expect(e).NotTo(HaveOccurred())
	review.OperationID = ""
	Expect(still).To(Equal(review))
	revisit, e := f.queries.RevisitGet(ctx, f.token, f.document)
	Expect(e).NotTo(HaveOccurred())
	Expect(string(revisit.Value)).To(Equal(`{"flag":true}`))
})
var _ = It("Lifecycle confirmation replay expiry alias and impact freshness", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	now := time.Now().UTC()
	f.queries.LifecycleNow = func() time.Time { return now }
	p := lifecyclePlan(f, "trash")
	invalid := []byte(fmt.Sprintf(`{"plan_id":%q,"expected_revision":%d,"confirmed":false}`, p.ID, p.ExpectedRevision))
	_, e := f.queries.LifecycleCommit(ctx, f.token, p.ID, "", invalid)
	Expect(e).To(HaveOccurred())
	result := lifecycleCommit(f, p)
	Expect(result.Changed).To(BeTrue())
	for _, alias := range []string{"", key(GinkgoT())} {
		_, e = f.queries.LifecycleCommit(ctx, f.token, p.ID, alias, invalid)
		Expect(e).To(HaveOccurred())
	}
	changed := []byte(fmt.Sprintf(`{"plan_id":%q,"expected_revision":%d,"confirmed":true}`, p.ID, p.ExpectedRevision+1))
	_, e = f.queries.LifecycleCommit(ctx, f.token, p.ID, "", changed)
	Expect(e).To(MatchError(domain.ErrConflict))
	now = now.Add(time.Hour)
	alias := key(GinkgoT())
	replay, e := f.queries.LifecycleCommit(ctx, f.token, p.ID, alias, lifecycleBody(p))
	Expect(e).NotTo(HaveOccurred())
	var replayed domain.LifecycleResult
	Expect(json.Unmarshal(replay.Body, &replayed)).To(Succeed())
	Expect(replayed).To(Equal(result))
	receipt, e := f.auth.Operation(ctx, f.token, alias)
	Expect(e).NotTo(HaveOccurred())
	Expect(receipt.Response.Body).To(Equal(replay.Body))
	lifecycleCommit(f, lifecyclePlan(f, "restore"))
	p = lifecyclePlan(f, "archive")
	_, e = f.put(GinkgoT(), query.ReadingPosition, "", query.PersonalView{}, true, `{"page_path":"hello.html","page_query":"","page_fragment":"","opened":true}`)
	Expect(e).NotTo(HaveOccurred())
	lifecycleCommit(f, p) // an automatic reader save is not impact freshness.
	p = lifecyclePlan(f, "trash")
	_, e = f.queries.RevisitPut(ctx, f.token, key(GinkgoT()), f.document, "", true, []byte(`{"flag":true}`))
	Expect(e).NotTo(HaveOccurred())
	_, e = f.queries.LifecycleCommit(ctx, f.token, p.ID, "", lifecycleBody(p))
	Expect(e).To(MatchError(query.ErrStaleLifecyclePlan))
	p = lifecyclePlan(f, "trash")
	page := "hello.html"
	_, e = noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, nil, "changed after preview"))
	Expect(e).NotTo(HaveOccurred())
	_, e = f.queries.LifecycleCommit(ctx, f.token, p.ID, "", lifecycleBody(p))
	Expect(e).To(MatchError(query.ErrStaleLifecyclePlan))
	p = lifecyclePlan(f, "trash")
	now = now.Add(time.Hour)
	_, e = f.queries.LifecycleCommit(ctx, f.token, p.ID, "", lifecycleBody(p))
	Expect(e).To(MatchError(query.ErrStaleLifecyclePlan))
})

var _ = It("Lifecycle atomic rollback alias collision concurrent replay and late synthetic maintenance", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	p := lifecyclePlan(f, "trash")
	db, e := sql.Open("sqlite", f.dbPath)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	for _, trigger := range []string{
		`CREATE TRIGGER fail_lifecycle BEFORE UPDATE ON documents BEGIN SELECT RAISE(ABORT,'injected document'); END`,
		`CREATE TRIGGER fail_lifecycle BEFORE INSERT ON events WHEN NEW.kind='document.lifecycle_changed' BEGIN SELECT RAISE(ABORT,'injected outbox'); END`,
		`CREATE TRIGGER fail_lifecycle BEFORE UPDATE ON lifecycle_plans BEGIN SELECT RAISE(ABORT,'injected plan'); END`,
		`CREATE TRIGGER fail_lifecycle BEFORE UPDATE ON operation_receipts WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'injected receipt'); END`,
	} {
		_, e = db.Exec(trigger)
		Expect(e).NotTo(HaveOccurred())
		_, e = f.queries.LifecycleCommit(ctx, f.token, p.ID, "", lifecycleBody(p))
		Expect(e).To(HaveOccurred())
		d, e := f.store.Document(ctx, f.document)
		Expect(e).NotTo(HaveOccurred())
		Expect(d.Lifecycle).To(Equal("active"))
		Expect(d.Revision).To(Equal(p.ExpectedRevision))
		stored, e := f.queries.LifecyclePlanGet(ctx, f.token, p.ID, query.Options{})
		Expect(e).NotTo(HaveOccurred())
		Expect(stored.State).To(Equal("open"))
		Expect(stored.Result).To(BeNil())
		var count int
		Expect(db.QueryRow(`SELECT count(*) FROM events WHERE kind='document.lifecycle_changed'`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		Expect(db.QueryRow(`SELECT count(*) FROM operation_receipts WHERE operation_id=?`, p.ID).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		_, e = db.Exec(`DROP TRIGGER fail_lifecycle`)
		Expect(e).NotTo(HaveOccurred())
	}
	collision := key(GinkgoT())
	_, e = reviewPut(f, collision, "complete", "", true)
	Expect(e).NotTo(HaveOccurred())
	p = lifecyclePlan(f, "trash")
	_, e = f.queries.LifecycleCommit(ctx, f.token, p.ID, collision, lifecycleBody(p))
	Expect(e).To(MatchError(domain.ErrConflict))
	d, e := f.store.Document(ctx, f.document)
	Expect(e).NotTo(HaveOccurred())
	Expect(d.Lifecycle).To(Equal("active"))
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.queries.LifecycleCommit(ctx, f.token, p.ID, "", lifecycleBody(p))
			errors <- e
		}()
	}
	wg.Wait()
	close(errors)
	for e := range errors {
		Expect(e).NotTo(HaveOccurred())
	}
	captured := p.ExpectedRevision
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return app.CheckLifecycleApplication(tx, f.document, captured) })).To(MatchError(domain.ErrNotFound))
	lifecycleCommit(f, lifecyclePlan(f, "restore"))
	// Synthetic maintenance accepted before trash is still stale after owner restore.
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return app.CheckLifecycleApplication(tx, f.document, captured) })).To(MatchError(domain.ErrPrecondition))
})
var _ = It("Lifecycle owner-only plans list and current event masking", Label("persistence"), func() {
	f := newFixture(GinkgoT())
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
	p := lifecyclePlan(f, "trash")
	_, e = f.queries.LifecyclePlanGet(ctx, token, p.ID, query.Options{})
	Expect(e).To(MatchError(domain.ErrNotFound))
	_, e = f.queries.Trash(ctx, token, query.Options{})
	Expect(e).To(MatchError(domain.ErrNotFound))
	_, e = f.queries.LifecycleCommit(ctx, token, p.ID, "", lifecycleBody(p))
	Expect(e).To(MatchError(domain.ErrNotFound))
	lifecycleCommit(f, p)
	var events []domain.Event
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		epoch, _, e := tx.EventHead()
		if e != nil {
			return e
		}
		events, e = tx.Events(epoch, 0, 256)
		return e
	})).To(Succeed())
	found := false
	for _, event := range events {
		if event.Kind != "document.lifecycle_changed" {
			continue
		}
		found = true
		for _, credential := range []string{f.token, token} {
			masked, e := f.auth.FilterEvent(ctx, credential, event)
			Expect(e).NotTo(HaveOccurred())
			Expect(masked.Kind).To(Equal("library.invalidated"))
			Expect(masked.ResourceID).To(BeEmpty())
			Expect(masked.Body).To(BeEmpty())
		}
	}
	Expect(found).To(BeTrue())
})
