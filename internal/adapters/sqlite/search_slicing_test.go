package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "haeryong.local/haeryong/internal/adapters/http/v1"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type sliceObserved struct {
	app.Store
	slices, snapshots, maxRows int
	after                      func(int64)
}
type sliceObservedTx struct {
	app.WriteTx
	owner  *sliceObserved
	sliced bool
	after  int64
}

func (s *sliceObserved) Write(ctx context.Context, f func(app.WriteTx) error) error {
	tx := &sliceObservedTx{owner: s}
	err := s.Store.Write(ctx, func(real app.WriteTx) error { tx.WriteTx = real; return f(tx) })
	if err == nil && tx.sliced && s.after != nil {
		s.after(tx.after)
	}
	return err
}
func (t *sliceObservedTx) SearchCorpus(o domain.SearchSelection) ([]domain.SearchVersionState, error) {
	t.owner.snapshots++
	return t.WriteTx.SearchCorpus(o)
}
func (t *sliceObservedTx) SearchCandidates(o domain.SearchSelection, q string, after, through int64) (domain.SearchSlice, error) {
	s, e := t.WriteTx.SearchCandidates(o, q, after, through)
	t.owner.slices++
	if s.Examined > t.owner.maxRows {
		t.owner.maxRows = s.Examined
	}
	t.sliced = true
	t.after = after
	return s, e
}

var _ = Describe("W3b bounded search and derived repair", Label("persistence"), func() {
	It("H1 slices misses and matches before filtering and releases the store between slices", func() {
		s, a, owner, p := searchFixture()
		v, _, b := seedSearchTree(s, p, "", "", map[string]string{"a.html": "<p>" + strings.Repeat("needle padding ", 100000) + "</p>"})
		finishSearch(s, v, b)
		observed := &sliceObserved{Store: s}
		writes := 0
		observed.after = func(_ int64) {
			child, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			Expect(s.Write(child, func(app.WriteTx) error { writes++; return nil })).To(Succeed())
		}
		q := query.New(observed, a, nil)
		for _, term := range []string{"needle", "not_present_anywhere"} {
			observed.slices, observed.maxRows = 0, 0
			result, e := q.Search(ctx, owner, query.SearchOptions{Options: query.Options{Kind: "page"}, Text: term})
			Expect(e).NotTo(HaveOccurred())
			Expect(observed.slices).To(BeNumerically(">", 1))
			Expect(observed.maxRows).To(BeNumerically("<=", 64))
			if term == "needle" {
				Expect(result.Items).To(HaveLen(1))
			} else {
				Expect(result.Items).To(BeEmpty())
			}
		}
		Expect(writes).To(BeNumerically(">", 2))
	})
	It("H1 rechecks visible state, restarts once, and never returns a mixed snapshot", func() {
		for _, changes := range []int{1, 2} {
			s, a, owner, p := searchFixture()
			v, _, b := seedSearchTree(s, p, "", "", map[string]string{"a.html": "<title>old needle</title><p>" + strings.Repeat("needle padding ", 10000) + "</p>"})
			finishSearch(s, v, b)
			observed := &sliceObserved{Store: s}
			changed := 0
			observed.after = func(after int64) {
				if after == 0 && changed < changes {
					changed++
					_, e := s.db.Exec(`UPDATE search_units SET title=? WHERE version_id=? AND kind='page'`, strings.Repeat("new", changed)+" needle", v)
					Expect(e).NotTo(HaveOccurred())
				}
			}
			result, e := query.New(observed, a, nil).Search(ctx, owner, query.SearchOptions{Options: query.Options{Kind: "page"}, Text: "needle"})
			Expect(changed).To(Equal(changes))
			Expect(observed.snapshots).To(Equal(4))
			if changes == 1 {
				Expect(e).NotTo(HaveOccurred())
				Expect(result.Items[0].Title).To(Equal("new needle"))
			} else {
				var unavailable *query.SearchUnavailable
				Expect(errors.As(e, &unavailable)).To(BeTrue())
				Expect(errors.Is(e, context.DeadlineExceeded)).To(BeTrue())
			}
		}
	})
	It("H1 hidden-only changes between slices preserve the visible snapshot", func() {
		s, a, _, p := searchFixture()
		visible, _, b := seedSearchTree(s, p, "", "", map[string]string{"visible.html": "<p>" + strings.Repeat("needle padding ", 10000) + "</p>"})
		finishSearch(s, visible, b)
		hidden, _, b := seedSearchTree(s, p, "", "", map[string]string{"hidden.html": "<p>needle hidden</p>"})
		finishSearch(s, hidden, b)
		token := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}, VersionIDs: []string{visible}})
		observed := &sliceObserved{Store: s}
		observed.after = func(after int64) {
			if after == 0 {
				_, e := s.db.Exec(`UPDATE search_units SET title='hidden change' WHERE version_id=?`, hidden)
				Expect(e).NotTo(HaveOccurred())
			}
		}
		result, e := query.New(observed, a, nil).Search(ctx, token, query.SearchOptions{Options: query.Options{Kind: "page"}, Text: "needle"})
		Expect(e).NotTo(HaveOccurred())
		Expect(result.Items).To(HaveLen(1))
		Expect(result.Items[0].VersionID).To(Equal(visible))
		Expect(observed.snapshots).To(Equal(2))
	})

	It("H2 cancellation and deadlines return while the first authentication is queued behind a real lock barrier", func() {
		s, a, owner, _ := searchFixture()
		handler := v1.New(query.New(s, a, nil), nil, nil)
		for _, method := range []string{"GET", "HEAD"} {
			for _, expire := range []bool{false, true} {
				entered, release, writerDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
				go func() { writerDone <- s.Write(ctx, func(app.WriteTx) error { close(entered); <-release; return nil }) }()
				<-entered
				child, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
				request := httptest.NewRequest(method, "/api/v1/search?q=needle", nil).WithContext(child)
				request.Header.Set("Authorization", "Bearer "+owner)
				response := httptest.NewRecorder()
				done := make(chan struct{})
				go func() { handler.ServeHTTP(response, request); close(done) }()
				if !expire {
					cancel()
				}
				returned := false
				select {
				case <-done:
					returned = true
				case <-time.After(400 * time.Millisecond):
				}
				close(release)
				Expect(<-writerDone).To(Succeed())
				<-done
				cancel()
				Expect(returned).To(BeTrue(), "queued search must return before the holder releases")
				Expect(response.Code).To(Equal(503))
				Expect(response.Header().Get("Retry-After")).To(Equal("2"))
				if method == "HEAD" {
					Expect(response.Body.Len()).To(BeZero())
				} else {
					Expect(response.Body.String()).To(ContainSubstring(`"reason":"query_timeout"`))
				}
			}
		}
	})
	It("H3 EOF activation updates one row regardless of region count and preserves staged visibility", func() {
		s, _, _, p := searchFixture()
		v, _, _ := seedSearchTree(s, p, "", "", map[string]string{"a.html": "<p>original</p>"})
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			id, e := tx.PutSearchUnit(domain.SearchUnit{VersionID: v, Kind: "page", PagePath: "a.html", State: "ready", BodyState: "pending", AnchorState: "not_applicable"})
			if e != nil {
				return e
			}
			for i := 0; i < 1000; i++ {
				_, e = tx.PutSearchUnit(domain.SearchUnit{VersionID: v, Kind: "region", PagePath: "a.html", RegionID: strings.Repeat("r", i+1), State: "pending", BodyState: "pending", AnchorState: "pending"})
				if e != nil {
					return e
				}
			}
			Expect(tx.MergeSearch(0, 2)).To(MatchError(domain.ErrUnsupportedDerivedData))
			Expect(tx.PutSearchChunk(domain.SearchChunk{UnitID: id, Part: "body", Body: strings.Repeat("x", 4194305)})).To(MatchError(domain.ErrUnsupportedDerivedData))
			before, after := 0, 0
			real := tx.(*writeTx)
			Expect(real.tx.QueryRow(`SELECT total_changes()`).Scan(&before)).To(Succeed())
			if e = tx.CompleteSearchPage(id); e != nil {
				return e
			}
			Expect(real.tx.QueryRow(`SELECT total_changes()`).Scan(&after)).To(Succeed())
			Expect(after - before).To(Equal(1))
			region, e := tx.SearchRegionUnit(v, "a.html", "r")
			Expect(region.State).To(Equal("ready"))
			Expect(region.BodyState).To(Equal("complete"))
			Expect(region.AnchorState).To(Equal("missing"))
			return e
		})).To(Succeed())
	})
	It("H5 normalized metadata windows recall a contracting NFD phrase across the former authored boundary", func() {
		s, a, owner, p := searchFixture()
		nfd := "a" + strings.Repeat("e\u0301", 4092) + "z"
		nfc := "a" + strings.Repeat("é", 4092) + "z"
		v, _, b := seedSearchTree(s, p, "", "", map[string]string{"a.html": "<p>original</p>"}, func(tx app.WriteTx, v string) error {
			for _, row := range []struct{ id, text string }{{"nfd-source", nfd}, {"nfc-source", nfc}} {
				raw, _ := json.Marshal(map[string]string{"kind": "git", "repo_id": strings.Repeat("x", 27768) + row.text})
				if e := tx.PutSource(domain.Source{VersionID: v, ID: row.id, Metadata: raw}); e != nil {
					return e
				}
			}
			return nil
		})
		finishSearch(s, v, b)
		result := runSearch(query.New(s, a, nil), owner, `"`+nfc+`"`, "source")
		Expect(result.Items).To(HaveLen(2))
		for _, hit := range result.Items {
			if *hit.SourceID == "nfd-source" {
				Expect(hit.Snippet).NotTo(BeNil())
				Expect(*hit.Snippet).To(ContainSubstring("e\u0301"))
			}
		}

		_, e := s.db.Exec(`UPDATE search_chunks SET display_body='null' WHERE part='metadata' AND unit_id IN(SELECT unit_id FROM search_units WHERE version_id=? AND source_id='nfd-source')`, v)
		Expect(e).NotTo(HaveOccurred())
		_, e = query.New(s, a, nil).Search(ctx, owner, query.SearchOptions{Options: query.Options{Kind: "source"}, Text: `"` + nfc + `"`})
		var unavailable *query.SearchUnavailable
		Expect(errors.As(e, &unavailable)).To(BeTrue(), "malformed derived metadata must fail closed rather than return partial hits")

	})
})
