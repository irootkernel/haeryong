package sqlite

import (
	"database/sql/driver"
	"fmt"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "haeryong.local/haeryong/internal/adapters/http/v1"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func replaceSearchConnection(s *Store) {
	conn, err := s.db.Conn(ctx)
	Expect(err).NotTo(HaveOccurred())
	Expect(conn.Raw(func(any) error { return driver.ErrBadConn })).To(Equal(driver.ErrBadConn))
	_ = conn.Close()
}
func connectionSearchCounters(s *Store) string {
	var changes, version int64
	Expect(s.db.QueryRow("SELECT total_changes()").Scan(&changes)).To(Succeed())
	Expect(s.db.QueryRow("PRAGMA data_version").Scan(&version)).To(Succeed())
	return fmt.Sprintf("%d:%d", changes, version)
}
func searchHTTP(handler *v1.Handler, token, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", target, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

var _ = Describe("search visibility generations", Label("persistence"), func() {
	It("J1 ignores real owner HTTP touches, job progress, hidden index batches and staged body writes between slices", func() {
		s, a, owner, p := searchFixture()
		visible := seedSearchRows(s, p, 193)
		hidden, doc, b := seedSearchTree(s, p, "", "", map[string]string{"hidden.html": "<p>needle hidden</p>"})
		finishSearch(s, hidden, b)
		// The owner's active/latest selection cannot see this archived version.
		_, err := s.db.Exec("UPDATE documents SET lifecycle='archived' WHERE document_id=?", doc)
		Expect(err).NotTo(HaveOccurred())
		plain := v1.New(query.New(s, a, nil), nil, nil)
		observed := &snapshotObserved{Store: s}
		barriers := 0
		observed.onPage = func(stage string, page domain.SearchSnapshotSlice) {
			if stage != "chunks" || page.Examined == 0 {
				return
			}
			barriers++
			a.Now = func() time.Time { return time.Now().Add(time.Duration(barriers) * time.Second) }
			Expect(searchHTTP(plain, owner, "/api/v1/projects").Code).To(Equal(200))
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				index, e := tx.SearchIndex(visible)
				if e != nil {
					return e
				}
				if e = tx.SetJobResult(index.JobID, []byte(`{"stage":"indexing","files_done":"1"}`)); e != nil {
					return e
				}
				index, e = tx.SearchIndex(hidden)
				if e != nil {
					return e
				}
				index.Revision++
				return tx.PutSearchIndex(index)
			})).To(Succeed())
		}
		out := searchHTTP(v1.New(query.New(observed, a, nil), nil, nil), owner, "/api/v1/search?q=needle")
		Expect(out.Code).To(Equal(200), out.Body.String())
		Expect(barriers).To(BeNumerically(">", 2))
		Expect(observed.snapshots).To(Equal(2), "one start and one final snapshot, without a restart")
		// A workflow credential must likewise ignore an active, inaccessible version.
		_, err = s.db.Exec("UPDATE documents SET lifecycle='active' WHERE document_id=?", doc)
		Expect(err).NotTo(HaveOccurred())
		token := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}, VersionIDs: []string{visible}})
		observed.snapshots = 0
		out = searchHTTP(v1.New(query.New(observed, a, nil), nil, nil), token, "/api/v1/search?q=needle")
		Expect(out.Code).To(Equal(200), out.Body.String())
		Expect(observed.snapshots).To(Equal(2))
		// Posting a pending page's body does not change any searchable answer.
		var pageID int64
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			e := tx.(*writeTx).tx.QueryRow("SELECT unit_id FROM search_units WHERE version_id=? AND kind='page'", visible).Scan(&pageID)
			if e != nil {
				return e
			}
			u, e := tx.SearchUnit(pageID)
			if e != nil {
				return e
			}
			u.BodyState = "pending"
			_, e = tx.PutSearchUnit(u)
			return e
		})).To(Succeed())
		selection := domain.SearchSelection{Scope: domain.CredentialScope{Kind: "owner"}, Lifecycle: "active", Versions: "latest", FenceKey: owner}
		var before, after string
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; before, e = tx.SearchReadStamp(selection); return e })).To(Succeed())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			return tx.PutSearchChunk(domain.SearchChunk{UnitID: pageID, Part: "body", Ordinal: 0, Body: "staged pending"})
		})).To(Succeed())
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; after, e = tx.SearchReadStamp(selection); return e })).To(Succeed())
		Expect(after).To(Equal(before))
	})
	It("J1 accepts an HTTP idle-expiry extension without masking real expiry", func() {
		s, a, _, clock := authFixture(GinkgoT())
		_, owner := pairOwner(GinkgoT(), a, "search expiry")
		seconds := int64(10)
		configureExpiry(GinkgoT(), s, &seconds, nil)
		h, e := s.Health(ctx)
		Expect(e).NotTo(HaveOccurred())
		seedSearchRows(s, h.Library.UnfiledProjectID, 130)
		observed := &snapshotObserved{Store: s}
		plain := v1.New(query.New(s, a, nil), nil, nil)
		barriers := 0
		observed.onPage = func(stage string, page domain.SearchSnapshotSlice) {
			if stage != "chunks" || page.Examined == 0 {
				return
			}
			barriers++
			// Each HTTP request is inside the current idle interval, while the search
			// proceeds past its initially captured expiry after the second barrier.
			clock.Add(6000)
			Expect(searchHTTP(plain, owner.Token, "/api/v1/projects").Code).To(Equal(200))
		}
		out := searchHTTP(v1.New(query.New(observed, a, nil), nil, nil), owner.Token, "/api/v1/search?q=needle")
		Expect(out.Code).To(Equal(200), out.Body.String())
		Expect(barriers).To(BeNumerically(">", 2))
		Expect(observed.snapshots).To(Equal(2))
		clock.Add(11000)
		Expect(searchHTTP(plain, owner.Token, "/api/v1/search?q=needle").Code).To(Equal(401))
	})

	It("J1 still restarts on a visible commit and preserves real HTTP revocation", func() {
		for _, revoke := range []bool{false, true} {
			s, a, owner, p := searchFixture()
			seedSearchRows(s, p, 193)
			observed := &snapshotObserved{Store: s}
			changed := false
			observed.onPage = func(stage string, page domain.SearchSnapshotSlice) {
				if changed || stage != "chunks" || page.Examined == 0 {
					return
				}
				changed = true
				if revoke {
					_, e := s.db.Exec("UPDATE device_sessions SET revoked=1")
					Expect(e).NotTo(HaveOccurred())
				} else {
					_, e := s.db.Exec("UPDATE search_units SET title='new needle' WHERE kind='region'")
					Expect(e).NotTo(HaveOccurred())
				}
			}
			out := searchHTTP(v1.New(query.New(observed, a, nil), nil, nil), owner, "/api/v1/search?q=needle")
			Expect(changed).To(BeTrue())
			if revoke {
				Expect(out.Code).To(Equal(401))
			} else {
				Expect(out.Code).To(Equal(200), out.Body.String())
				Expect(observed.snapshots).To(Equal(3))
			}
		}
	})
	It("J2 cannot reuse a cached corpus when physical connection counters collide after a visible commit", func() {
		s, a, owner, p := searchFixture()
		v := seedSearchRows(s, p, 130)
		replaceSearchConnection(s)
		counters := connectionSearchCounters(s)
		observed := &snapshotObserved{Store: s}
		q := query.New(observed, a, nil)
		before, e := q.Search(ctx, owner, query.SearchOptions{})
		Expect(e).NotTo(HaveOccurred())
		Expect(before.Index.State).To(Equal("complete"))
		pages := observed.pages
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			i, e := tx.SearchIndex(v)
			if e != nil {
				return e
			}
			i.State = "partial"
			i.Reason = "failed_unknown"
			i.Revision++
			return tx.PutSearchIndex(i)
		})).To(Succeed())
		replaceSearchConnection(s)
		Expect(connectionSearchCounters(s)).To(Equal(counters), "the old connection-local fence really collides")
		after, e := q.Search(ctx, owner, query.SearchOptions{})
		Expect(e).NotTo(HaveOccurred())
		Expect(after.Index.State).To(Equal("partial"))
		Expect(after.Index.Reasons).NotTo(BeEmpty())
		Expect(observed.pages).To(BeNumerically(">", pages), "the stale complete snapshot must not satisfy the new query")
	})
	It("J2 publishes no generations for rollback and retains invalidation after removal and reopening", func() {
		s, _, _, p := searchFixture()
		v := seedSearchRows(s, p, 0)
		o := domain.SearchSelection{Scope: domain.CredentialScope{Kind: "owner"}, Lifecycle: "active", Versions: "latest", FenceKey: "owner"}
		read := func() string {
			var stamp string
			Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; stamp, e = tx.SearchReadStamp(o); return e })).To(Succeed())
			return stamp
		}
		before := read()
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			i, e := tx.SearchIndex(v)
			if e != nil {
				return e
			}
			i.Revision++
			if e = tx.PutSearchIndex(i); e != nil {
				return e
			}
			return domain.ErrConflict
		})).To(Equal(domain.ErrConflict))
		Expect(read()).To(Equal(before))
		_, e := s.db.Exec("UPDATE documents SET lifecycle='trash' WHERE document_id=(SELECT document_id FROM document_versions WHERE version_id=?)", v)
		Expect(e).NotTo(HaveOccurred())
		removed := read()
		Expect(removed).NotTo(Equal(before))
		replaceSearchConnection(s)
		Expect(read()).To(Equal(removed))
	})
})
