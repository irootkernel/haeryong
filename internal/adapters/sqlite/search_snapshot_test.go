package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "haeryong.local/haeryong/internal/adapters/http/v1"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/search"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// Barriers run after a committed native transaction. No production test hook,
// wall-clock approximation or SQL cursor escapes the single connection gate.
type snapshotObserved struct {
	app.Store
	onPage                                func(string, domain.SearchSnapshotSlice)
	onSnapshot                            func(int)
	snapshots, fullChecks, pages, maxRows int
}
type snapshotObservedTx struct {
	app.WriteTx
	owner    *snapshotObserved
	stage    string
	page     domain.SearchSnapshotSlice
	snapshot bool
}

func (s *snapshotObserved) Write(ctx context.Context, f func(app.WriteTx) error) error {
	t := &snapshotObservedTx{owner: s}
	err := s.Store.Write(ctx, func(real app.WriteTx) error { t.WriteTx = real; return f(t) })
	if err == nil {
		if t.stage != "" && s.onPage != nil {
			s.onPage(t.stage, t.page)
		}
		if t.snapshot && s.onSnapshot != nil {
			s.onSnapshot(s.snapshots)
		}
	}
	return err
}
func (t *snapshotObservedTx) SearchCorpus(o domain.SearchSelection) ([]domain.SearchVersionState, error) {
	t.owner.snapshots++
	t.snapshot = false
	return t.WriteTx.SearchCorpus(o)
}
func (t *snapshotObservedTx) SearchSnapshotSlice(o domain.SearchSelection, stage string, after int64) (domain.SearchSnapshotSlice, error) {
	p, e := t.WriteTx.SearchSnapshotSlice(o, stage, after)
	t.owner.pages++
	if p.Examined > t.owner.maxRows {
		t.owner.maxRows = p.Examined
	}
	t.stage, t.page = stage, p
	return p, e
}
func (t *snapshotObservedTx) SearchReadStamp(o domain.SearchSelection) (string, error) {
	// The final consistency check is a stamp-only transaction: no SQL work
	// remains in the snapshot when the after-commit cancellation is delivered.
	stamp, e := t.WriteTx.SearchReadStamp(o)
	if t.stage == "" && t.owner.snapshots > 0 {
		t.snapshot = true
	}
	return stamp, e
}
func (t *snapshotObservedTx) ScopedCredentialByHash(h string) (domain.ScopedCredential, error) {
	t.owner.fullChecks++
	return t.WriteTx.ScopedCredentialByHash(h)
}

func seedSearchRows(s *Store, p string, n int) string {
	v, _, b := seedSearchTree(s, p, "", "", map[string]string{"a.html": "<p>needle</p>"})
	finishSearch(s, v, b)
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		u, e := tx.SearchRegionUnit(v, "a.html", "extra")
		if errors.Is(e, domain.ErrNotFound) {
			u = domain.SearchUnit{VersionID: v, Kind: "region", PagePath: "a.html", RegionID: "extra", Title: "needle", State: "ready", BodyState: "complete", AnchorState: "present"}
			u.ID, e = tx.PutSearchUnit(u)
		}
		if e != nil {
			return e
		}
		for i := 0; i < n; i++ {
			if e = tx.PutSearchChunk(domain.SearchChunk{UnitID: u.ID, Part: "metadata", Ordinal: i, Title: "needle", Body: "needle " + strings.Repeat("padding ", 100), DisplayBody: fmt.Sprintf("needle %d", i)}); e != nil {
				return e
			}
		}
		return nil
	})).To(Succeed())
	return v
}

var _ = Describe("bounded snapshot and authority continuity", Label("persistence"), func() {
	It("I1 yields while an empty-query large metadata snapshot is computed and fences a change between pages", func() {
		s, a, owner, p := searchFixture()
		seedSearchRows(s, p, 193)
		observed := &snapshotObserved{Store: s}
		barriers := 0
		observed.onPage = func(stage string, page domain.SearchSnapshotSlice) {
			if stage != "chunks" || page.Examined == 0 {
				return
			}
			barriers++
			child, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			_, e := query.New(s, a, nil).Projects(child, owner, query.Options{})
			Expect(e).NotTo(HaveOccurred(), "another authenticated request must run before snapshot computation finishes")
		}
		out, e := query.New(observed, a, nil).Search(ctx, owner, query.SearchOptions{})
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Index.State).To(Equal("complete"))
		Expect(barriers).To(BeNumerically(">", 2))
		Expect(observed.maxRows).To(BeNumerically("<=", 64))
		changed := false
		observed.onPage = func(stage string, page domain.SearchSnapshotSlice) {
			if stage == "chunks" && page.Examined > 0 && !changed {
				changed = true
				_, e := s.db.Exec("UPDATE search_units SET title='new visible label' WHERE kind='region'")
				Expect(e).NotTo(HaveOccurred())
			}
		}
		_, e = query.New(observed, a, nil).Search(ctx, owner, query.SearchOptions{})
		Expect(e).NotTo(HaveOccurred(), "a changed snapshot must restart rather than mix pages")
		Expect(changed).To(BeTrue())
		Expect(observed.snapshots).To(Equal(3), "the second query must restart its interrupted snapshot")
	})
	It("I4 returns retryable query_timeout when cancellation arrives immediately after the final snapshot", func() {
		s, a, owner, p := searchFixture()
		seedSearchRows(s, p, 130)
		for _, method := range []string{"GET", "HEAD"} {
			child, cancel := context.WithCancel(ctx)
			observed := &snapshotObserved{Store: s}
			observed.onSnapshot = func(n int) {
				if n == 2 {
					cancel()
				}
			}
			response := httptest.NewRecorder()
			request := httptest.NewRequest(method, "/api/v1/search?q=needle", nil).WithContext(child)
			request.Header.Set("Authorization", "Bearer "+owner)
			v1.New(query.New(observed, a, nil), nil, nil).ServeHTTP(response, request)
			cancel()
			Expect(observed.snapshots).To(Equal(2))
			Expect(response.Code).To(Equal(503))
			Expect(response.Header().Get("Retry-After")).To(Equal("2"))
			if method == "HEAD" {
				Expect(response.Body.Len()).To(BeZero())
			} else {
				Expect(response.Body.String()).To(ContainSubstring(`"reason":"query_timeout"`))
				Expect(response.Body.String()).To(ContainSubstring(`"retryable":true`))
			}
		}
	})
	It("I5 checks authority generations per bounded slice but fully decodes a workflow grant only at start and end", func() {
		s, a, _, p := searchFixture()
		v := seedSearchRows(s, p, 193)
		token := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}, VersionIDs: []string{v}})
		observed := &snapshotObserved{Store: s}
		q := query.New(observed, a, nil)
		out, e := q.Search(ctx, token, query.SearchOptions{Text: "needle"})
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Items).NotTo(BeEmpty())
		Expect(observed.pages).To(BeNumerically(">", 10))
		Expect(observed.fullChecks).To(Equal(2), "large grants must not be decoded per slice")
		pages := observed.pages
		_, e = q.Search(ctx, token, query.SearchOptions{Text: "different"})
		Expect(e).NotTo(HaveOccurred())
		Expect(observed.fullChecks).To(Equal(4), "each request still fully authenticates both ends")
		Expect(observed.pages).To(Equal(pages), "an unchanged completed snapshot must be reused across query text changes")

		Expect(observed.maxRows).To(BeNumerically("<=", 64))
		Expect(domain.SearchSliceTime).To(BeNumerically("<=", 10*time.Millisecond))
	})
	It("I3 preserves GET and HEAD auth, permission and explicit project classes when authority changes between candidate slices", func() {
		for _, method := range []string{"GET", "HEAD"} {
			for _, change := range []string{"revoked", "expired", "action", "target"} {
				s, a, owner, p := searchFixture()
				v := seedSearchRows(s, p, 130)
				token := owner
				if change == "action" || change == "target" {
					token = searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}, VersionIDs: []string{v}})
				}
				if change == "expired" {
					seconds := int64(1)
					configureExpiry(GinkgoT(), s, &seconds, nil)
				}
				observed := &sliceObserved{Store: s}
				changed := false
				observed.after = func(_ int64) {
					if changed {
						return
					}
					changed = true
					Expect(s.Write(ctx, func(tx app.WriteTx) error {
						if change == "revoked" {
							record, e := tx.SessionByHash(sec.Verifier("session", token))
							if e != nil {
								return e
							}
							record.Revoked = true
							return tx.PutSession(record)
						}
						if change == "expired" {
							now := a.Now().Add(time.Hour)
							a.Now = func() time.Time { return now }
							return nil
						}
						c, e := tx.ScopedCredentialByHash(sec.Verifier("workflow", token))
						if e != nil {
							return e
						}
						principal, e := tx.WorkflowPrincipal(c.PrincipalID)
						if e != nil {
							return e
						}
						if change == "action" {
							principal.Scope.Actions = nil
						} else {
							principal.Scope.ProjectIDs = nil
						}
						return tx.PutWorkflowPrincipal(principal)
					})).To(Succeed())
				}
				response := httptest.NewRecorder()
				request := httptest.NewRequest(method, "/api/v1/search?q=needle&project_id="+p, nil)
				request.Header.Set("Authorization", "Bearer "+token)
				v1.New(query.New(observed, a, nil), nil, nil).ServeHTTP(response, request)
				expected := 401
				if change == "action" {
					expected = 403
				}
				if change == "target" {
					expected = 404
				}
				Expect(changed).To(BeTrue())
				Expect(response.Code).To(Equal(expected), method+" "+change)
				Expect(response.Header().Get("Retry-After")).To(BeEmpty())
				if method == "HEAD" {
					Expect(response.Body.Len()).To(BeZero())
				} else {
					Expect(response.Body.String()).NotTo(ContainSubstring("SERVICE_RECOVERING"))
				}
			}
		}
	})
})

// Interrupt exactly after committing the first metadata window of the derived
// target. Supplied region provenance and unrelated outside label text survive.
type regionWindowObserved struct {
	app.Store
	cancel context.CancelFunc
	saw    bool
}
type regionWindowObservedTx struct {
	app.WriteTx
	owner *regionWindowObserved
}

func (s *regionWindowObserved) Write(ctx context.Context, f func(app.WriteTx) error) error {
	e := s.Store.Write(ctx, func(tx app.WriteTx) error { return f(regionWindowObservedTx{tx, s}) })
	if e == nil && s.saw && s.cancel != nil {
		cancel := s.cancel
		s.cancel = nil
		cancel()
	}
	return e
}
func (t regionWindowObservedTx) PutSearchChunk(c domain.SearchChunk) error {
	e := t.WriteTx.PutSearchChunk(c)
	if e == nil && c.Part == "metadata" && c.Ordinal == 0 {
		u, err := t.WriteTx.SearchUnit(c.UnitID)
		if err != nil {
			return err
		}
		if u.Kind == "region" && u.RegionID == "target" {
			t.owner.saw = true
		}
	}
	return e
}

var _ = It("I2 replays every derived region metadata window after interruption and preserves supplied provenance", Label("persistence"), func() {
	s, a, w, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), a, "region restart")
	health, e := s.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	label := strings.Repeat("padding ", 6000) + "uniquelateword"
	html := "<span id='outside'>" + label + "</span><section id='target' aria-labelledby='outside'>ordinary</section><section id='supplied'>ordinary</section>"
	supplied := []byte(`{"label":"Original supplied provenance"}`)
	v, _, b := seedSearchTree(s, health.Library.UnfiledProjectID, "", "", map[string]string{"a.html": html}, func(tx app.WriteTx, v string) error {
		return tx.PutRegion(domain.Region{VersionID: v, PagePath: "a.html", ID: "supplied", Metadata: supplied})
	})
	var index domain.SearchIndex
	worker := search.NewWorker(s, b)
	for n := 0; n < 20; n++ {
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; index, e = tx.SearchIndex(v); return e })).To(Succeed())
		if index.DiscoveryComplete {
			break
		}
		Expect(worker.Process(ctx, index.JobID)).To(Succeed())
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	observed := &regionWindowObserved{Store: s, cancel: cancel}
	Expect(search.NewWorker(observed, b).Process(child, index.JobID)).To(HaveOccurred())
	Expect(observed.saw).To(BeTrue())
	Expect(searchCount(s, `SELECT count(*) FROM search_chunks c JOIN search_units u USING(unit_id) WHERE u.version_id=? AND u.region_id='target' AND c.part='metadata'`, v)).To(Equal(1))
	Expect(s.Close()).To(Succeed())
	s = openStore(GinkgoT(), w, nil)
	a.Store = s
	Expect(driveSearch(s, search.NewWorker(s, b), index.JobID).State).To(Equal("completed"))
	out := runSearch(query.New(s, a, nil), owner.Token, "uniquelateword", "region")
	Expect(out.Index.State).To(Equal("complete"))
	ids := []string{}
	for _, hit := range out.Items {
		ids = append(ids, *hit.RegionID)
	}
	Expect(ids).To(ContainElement("target"), "a label outside the region cannot be rescued by its body")

	r, e := s.Region(ctx, v, "a.html", "supplied")
	Expect(e).NotTo(HaveOccurred())
	var got, want any
	Expect(json.Unmarshal(r.Metadata, &got)).To(Succeed())
	Expect(json.Unmarshal(supplied, &want)).To(Succeed())
	Expect(got).To(Equal(want))
})
