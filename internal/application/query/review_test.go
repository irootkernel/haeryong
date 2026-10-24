package query_test

import (
	"database/sql"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"sync"
	"time"
)

var _ = It("Review explicit transition table", Label("unit"), func() {
	for _, current := range []string{"unreviewed", "reviewing", "reviewed"} {
		for _, action := range []string{"start", "complete", "reopen", "clear"} {
			state := map[string]string{"start": "reviewing", "complete": "reviewed", "reopen": "reviewing", "clear": "unreviewed"}[action]
			next, err := query.ReviewNextState(current, query.ReviewRequest{Action: action, State: state})
			invalid := action == "start" && current == "reviewed" || action == "reopen" && current != "reviewed"
			if invalid {
				Expect(err).To(MatchError("INVALID_TRANSITION"))
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(next).To(Equal(state))
			}
		}
	}
	_, err := query.ReviewNextState("unreviewed", query.ReviewRequest{Action: "complete", State: "reviewing"})
	Expect(err).To(MatchError("INVALID_REQUEST"))
	_, err = query.ReviewNextState("unreviewed", query.ReviewRequest{Action: "scroll", State: "reviewed"})
	Expect(err).To(MatchError("INVALID_ENUM"))
})

func reviewPut(f *fixture, key, action, etag string, create bool) (query.ReviewView, error) {
	state := map[string]string{"start": "reviewing", "complete": "reviewed", "reopen": "reviewing", "clear": "unreviewed"}[action]
	response, err := f.queries.ReviewPut(ctx, f.token, key, f.version, etag, create, []byte(`{"action":"`+action+`","state":"`+state+`"}`))
	var view query.ReviewView
	if err == nil {
		err = json.Unmarshal(response.Body, &view)
	}
	return view, err
}

var _ = It("Review T-005-01-01..03 and X11 atomic personal history", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	absent, err := f.queries.ReviewGet(ctx, f.token, f.version)
	Expect(err).NotTo(HaveOccurred())
	Expect(absent.Exists).To(BeFalse())
	Expect(absent.Value.State).To(Equal("unreviewed"))
	_, err = f.put(GinkgoT(), query.ReadingPosition, "", query.PersonalView{}, true, `{"page_path":"hello.html","page_query":"","page_fragment":"","opened":true}`)
	Expect(err).NotTo(HaveOccurred())
	still, err := f.queries.ReviewGet(ctx, f.token, f.version)
	Expect(err).NotTo(HaveOccurred())
	Expect(still).To(Equal(absent))
	k := key(GinkgoT())
	completed, err := reviewPut(f, k, "complete", "", true)
	Expect(err).NotTo(HaveOccurred())
	Expect(completed.Value.ReviewedAt).NotTo(BeNil())
	replay, err := reviewPut(f, k, "complete", "", true)
	Expect(err).NotTo(HaveOccurred())
	Expect(replay).To(Equal(completed))
	_, err = reviewPut(f, k, "start", "", true)
	Expect(err).To(MatchError(domain.ErrConflict))
	_, err = reviewPut(f, key(GinkgoT()), "complete", "stale", false)
	Expect(err).To(MatchError("PRECONDITION_FAILED"))
	noop, err := reviewPut(f, key(GinkgoT()), "complete", *completed.ETag, false)
	Expect(err).NotTo(HaveOccurred())
	Expect(noop.Value).To(Equal(completed.Value))
	Expect(noop.Revision).To(Equal(completed.Revision))
	Expect(noop.ETag).To(Equal(completed.ETag))
	history, err := f.queries.ReviewHistory(ctx, f.token, f.version, query.Options{})
	Expect(err).NotTo(HaveOccurred())
	Expect(history.Items).To(HaveLen(1))
	Expect(history.Items[0].OperationID).To(Equal(k))
	doc, err := f.queries.Document(ctx, f.token, f.document)
	Expect(err).NotTo(HaveOccurred())
	Expect(*doc.LastReviewedVersionID).To(Equal(f.version))
	Expect(*doc.UpdatedSinceReview).To(BeFalse())
	reopened, err := reviewPut(f, key(GinkgoT()), "reopen", *completed.ETag, false)
	Expect(err).NotTo(HaveOccurred())
	Expect(reopened.Value.ReviewedAt).To(BeNil())
	doc, err = f.queries.Document(ctx, f.token, f.document)
	Expect(err).NotTo(HaveOccurred())
	Expect(doc.LastReviewedVersionID).To(BeNil())
	Expect(*doc.ContinueVersionID).To(Equal(f.version))
	queue, err := f.queries.Documents(ctx, f.token, query.Options{Review: "reviewing", Limit: 1})
	Expect(err).NotTo(HaveOccurred())
	Expect(queue.Items).To(HaveLen(1))
	first, err := f.queries.ReviewHistory(ctx, f.token, f.version, query.Options{Limit: 1})
	Expect(err).NotTo(HaveOccurred())
	Expect(first.NextCursor).NotTo(BeNil())
	next, err := f.queries.ReviewHistory(ctx, f.token, f.version, query.Options{Limit: 1, Cursor: *first.NextCursor})
	Expect(err).NotTo(HaveOccurred())
	Expect(next.Items[0].Action).To(Equal("reopen"))
})

var _ = It("Review X11 concurrent duplicate is one transition and competing edit stays stale", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	k := key(GinkgoT())
	var group sync.WaitGroup
	results := make([]error, 2)
	for i := range results {
		group.Add(1)
		go func() { defer group.Done(); _, results[i] = reviewPut(f, k, "complete", "", true) }()
	}
	group.Wait()
	Expect(results).To(Equal([]error{nil, nil}))
	history, err := f.queries.ReviewHistory(ctx, f.token, f.version, query.Options{})
	Expect(err).NotTo(HaveOccurred())
	Expect(history.Items).To(HaveLen(1))
	_, err = reviewPut(f, key(GinkgoT()), "start", "", true)
	Expect(err).To(MatchError("PRECONDITION_FAILED"))
})

var _ = It("Review rolls back state history audit and receipt at each failed storage boundary", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	db, err := sql.Open("sqlite", f.dbPath)
	Expect(err).NotTo(HaveOccurred())
	defer db.Close()
	for _, trigger := range []string{
		`CREATE TRIGGER fail_review BEFORE INSERT ON review_events BEGIN SELECT RAISE(ABORT,'injected history'); END`,
		`CREATE TRIGGER fail_review BEFORE INSERT ON events WHEN NEW.resource_kind='review' BEGIN SELECT RAISE(ABORT,'injected audit outbox'); END`,
		`CREATE TRIGGER fail_review BEFORE UPDATE ON operation_receipts WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'injected receipt'); END`,
	} {
		_, err = db.Exec(trigger)
		Expect(err).NotTo(HaveOccurred())
		k := key(GinkgoT())
		_, err = reviewPut(f, k, "complete", "", true)
		Expect(err).To(HaveOccurred())
		view, err := f.queries.ReviewGet(ctx, f.token, f.version)
		Expect(err).NotTo(HaveOccurred())
		Expect(view.Exists).To(BeFalse())
		for _, table := range []string{"review_states", "review_events"} {
			var count int
			Expect(db.QueryRow("SELECT count(*) FROM " + table).Scan(&count)).To(Succeed())
			Expect(count).To(BeZero())
		}
		var count int
		Expect(db.QueryRow(`SELECT count(*) FROM events WHERE resource_kind='review'`).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			_, err := tx.Receipt(domain.OperationKey{OperationID: k, PrincipalID: "missing"})
			return err
		})).To(MatchError(domain.ErrNotFound))
		Expect(db.QueryRow(`SELECT count(*) FROM operation_receipts WHERE operation_id=?`, k).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		_, err = db.Exec(`DROP TRIGGER fail_review`)
		Expect(err).NotTo(HaveOccurred())
	}
})

var _ = It("Review owner scope summaries event redaction and lifecycle replay visibility", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	k := key(GinkgoT())
	saved, err := reviewPut(f, k, "complete", "", true)
	Expect(err).NotTo(HaveOccurred())
	token, err := sec.Secret()
	Expect(err).NotTo(HaveOccurred())
	principal := key(GinkgoT())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		state, err := tx.AuthState()
		if err != nil {
			return err
		}
		p, err := tx.Scope(f.project)
		if err != nil {
			return err
		}
		scope := domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read", "project.read"}, ProjectIDs: []string{f.project}, VersionIDs: []string{f.version}}
		if err := tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: principal, Active: true, Scope: scope, Revision: 1}); err != nil {
			return err
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: principal, PrincipalID: principal, TokenHash: sec.Verifier("workflow", token), AuthEpoch: state.AuthEpoch, Scope: scope, Permissions: map[string]int64{f.project: p.Project.PermissionsRevision}, ConfigRevision: p.ConfigRevision, CreatedAt: time.Now().UTC(), Revision: 1})
	})).To(Succeed())
	_, err = f.queries.ReviewGet(ctx, token, f.version)
	Expect(err).To(MatchError(domain.ErrDenied))
	_, err = f.queries.ReviewHistory(ctx, token, f.version, query.Options{})
	Expect(err).To(MatchError(domain.ErrDenied))
	_, err = f.queries.ReviewPut(ctx, token, k, f.version, *saved.ETag, false, []byte(`{"action":"clear","state":"unreviewed"}`))
	Expect(err).To(MatchError(domain.ErrDenied))
	doc, err := f.queries.Document(ctx, token, f.document)
	Expect(err).NotTo(HaveOccurred())
	Expect(doc.State).To(BeNil())
	Expect(doc.LastReviewedVersionID).To(BeNil())
	Expect(doc.UpdatedSinceReview).To(BeNil())
	Expect(doc.ContinueVersionID).To(BeNil())
	_, err = f.queries.Documents(ctx, token, query.Options{Review: "reviewing"})
	Expect(err).To(MatchError(domain.ErrDenied))
	var review domain.Event
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		epoch, _, err := tx.EventHead()
		if err != nil {
			return err
		}
		events, err := tx.Events(epoch, 0, 256)
		for _, e := range events {
			if e.ResourceKind == "review" {
				review = e
			}
		}
		return err
	})).To(Succeed())
	Expect(review.Body).NotTo(BeEmpty())
	redacted, err := f.auth.FilterEvent(ctx, token, review)
	Expect(err).NotTo(HaveOccurred())
	Expect(redacted.ResourceKind).To(Equal("library"))
	Expect(redacted.ResourceID).To(BeEmpty())
	Expect(redacted.Body).To(BeEmpty())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.TestTrashDocument(f.document) })).To(Succeed())
	_, err = reviewPut(f, key(GinkgoT()), "clear", *saved.ETag, false)
	Expect(err).To(MatchError("lifecycle blocks write: trash"))
	_, err = reviewPut(f, k, "complete", "", true)
	Expect(err).To(MatchError(domain.ErrNotFound))
	_, err = f.auth.Operation(ctx, f.token, k)
	Expect(err).To(MatchError(domain.ErrNotFound))
})
