package sqlite

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func pendingVisibilityPage(s *Store, p string) (string, int64) {
	v, _, b := seedSearchTree(s, p, "", "", map[string]string{"a.html": "<p>needle ready</p>", "pending.html": "<p>pending</p>"})
	finishSearch(s, v, b)
	var pending int64
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		real := tx.(*writeTx)
		var ready int64
		if e := real.tx.QueryRow("SELECT unit_id FROM search_units WHERE version_id=? AND kind='page' AND page_path='a.html'", v).Scan(&ready); e != nil {
			return e
		}
		for n := 1; n <= 193; n++ {
			if e := tx.PutSearchChunk(domain.SearchChunk{UnitID: ready, Part: "metadata", Ordinal: n, Title: "needle", Body: "needle padding", DisplayBody: fmt.Sprint("needle ", n)}); e != nil {
				return e
			}
		}
		if e := real.tx.QueryRow("SELECT unit_id FROM search_units WHERE version_id=? AND kind='page' AND page_path='pending.html'", v).Scan(&pending); e != nil {
			return e
		}
		u, e := tx.SearchUnit(pending)
		if e != nil {
			return e
		}
		u.BodyState = "pending"
		if _, e = tx.PutSearchUnit(u); e != nil {
			return e
		}
		index, e := tx.SearchIndex(v)
		if e != nil {
			return e
		}
		index.State = "indexing"
		index.Revision++
		return tx.PutSearchIndex(index)
	})).To(Succeed())
	return v, pending
}

func quarantineSearchVersion(s *Store, version, state string) {
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		index, e := tx.SearchIndex(version)
		if e != nil {
			return e
		}
		index.State = state
		index.Revision++
		return tx.PutSearchIndex(index)
	})).To(Succeed())
}

var _ = Describe("selection-specific search projection commits", Label("persistence"), func() {
	It("K1 hidden same-document publications between snapshot slices preserve the scoped all-version projection", func() {
		s, a, _, p := searchFixture()
		v := seedSearchRows(s, p, 193)
		var doc string
		Expect(s.db.QueryRow("SELECT document_id FROM document_versions WHERE version_id=?", v).Scan(&doc)).To(Succeed())
		token := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}, VersionIDs: []string{v}})
		latestSelection := domain.SearchSelection{Scope: domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}, VersionIDs: []string{v}}, FenceKey: token, Lifecycle: "active", Versions: "latest"}
		var latestBefore, latestAfter string
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			var e error
			latestBefore, e = tx.SearchReadStamp(latestSelection)
			return e
		})).To(Succeed())
		observed := &snapshotObserved{Store: s}
		published := 0
		base := v
		observed.onPage = func(stage string, page domain.SearchSnapshotSlice) {
			if stage != "chunks" || page.Examined == 0 || published == 2 {
				return
			}
			published++
			base, _, _ = seedSearchTree(s, p, doc, base, map[string]string{"hidden.html": fmt.Sprintf("<p>hidden publication %d</p>", published)})
		}
		out, e := query.New(observed, a, nil).Search(ctx, token, query.SearchOptions{Text: "needle", Versions: "all"})
		Expect(e).NotTo(HaveOccurred())
		Expect(published).To(Equal(2))
		Expect(observed.snapshots).To(Equal(2), "neither hidden publication may restart the attempt")
		Expect(out.Index.VersionsTotal).To(Equal("1"))
		Expect(out.Items).NotTo(BeEmpty())
		for _, hit := range out.Items {
			Expect(hit.VersionID).To(Equal(v))
		}
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			var e error
			latestAfter, e = tx.SearchReadStamp(latestSelection)
			return e
		})).To(Succeed())
		Expect(latestAfter).NotTo(Equal(latestBefore), "the same publication must invalidate latest membership")
	})
	It("K1 pending-page derived units and long-label metadata windows between slices do not restart a search", func() {
		s, a, owner, p := searchFixture()
		v, _ := pendingVisibilityPage(s, p)
		observed := &snapshotObserved{Store: s}
		writes := 0
		observed.onPage = func(stage string, page domain.SearchSnapshotSlice) {
			if stage != "chunks" || page.Examined == 0 || writes == 3 {
				return
			}
			writes++
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				u := domain.SearchUnit{VersionID: v, Kind: "region", PagePath: "pending.html", RegionID: "derived", Title: "needle " + strings.Repeat("pending label ", 8192) + fmt.Sprint(writes), State: "pending", BodyState: "pending", AnchorState: "pending"}
				id, e := tx.PutSearchUnit(u)
				if e != nil {
					return e
				}
				return tx.PutSearchChunk(domain.SearchChunk{UnitID: id, Part: "metadata", Ordinal: writes - 1, Body: "needle " + strings.Repeat("pending ", 4096), DisplayBody: "needle staged label"})
			})).To(Succeed())
		}
		out, e := query.New(observed, a, nil).Search(ctx, owner, query.SearchOptions{Text: "needle"})
		Expect(e).NotTo(HaveOccurred())
		Expect(writes).To(Equal(3))
		Expect(observed.snapshots).To(Equal(2))
		Expect(out.Index.HTMLComplete).To(Equal("1"))
		Expect(out.Index.HTMLPending).To(Equal("1"))
		for _, hit := range out.Items {
			Expect(hit.RegionID).To(BeNil(), "pending derived region metadata must remain invisible")
		}
	})
	It("K1 EOF activation invalidates the snapshot and publishes staged region metadata", func() {
		s, a, owner, p := searchFixture()
		v, pending := pendingVisibilityPage(s, p)
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			id, e := tx.PutSearchUnit(domain.SearchUnit{VersionID: v, Kind: "region", PagePath: "pending.html", RegionID: "derived", Title: "needle activated", State: "pending", BodyState: "pending", AnchorState: "pending"})
			if e != nil {
				return e
			}
			return tx.PutSearchChunk(domain.SearchChunk{UnitID: id, Part: "metadata", Body: "needle activated", DisplayBody: "needle activated"})
		})).To(Succeed())
		observed := &snapshotObserved{Store: s}
		activated := false
		observed.onPage = func(stage string, page domain.SearchSnapshotSlice) {
			if activated || stage != "chunks" || page.Examined == 0 {
				return
			}
			activated = true
			Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.CompleteSearchPage(pending) })).To(Succeed())
		}
		out, e := query.New(observed, a, nil).Search(ctx, owner, query.SearchOptions{Text: "needle"})
		Expect(e).NotTo(HaveOccurred())
		Expect(activated).To(BeTrue())
		Expect(observed.snapshots).To(Equal(3))
		Expect(out.Index.HTMLComplete).To(Equal("2"))
		found := false
		for _, hit := range out.Items {
			if hit.RegionID != nil && *hit.RegionID == "derived" {
				found = true
			}
		}
		Expect(found).To(BeTrue(), "the retry must include the newly activated region")
	})
	It("L1 quarantined cleanup commits preserve snapshots and leaving quarantine invalidates", func() {
		s, a, owner, p := searchFixture()
		seedSearchRows(s, p, 193)
		hidden := seedSearchRows(s, p, 193)
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			return tx.PutPageAnchor(domain.PageAnchor{VersionID: hidden, PagePath: "a.html", ID: "cleanup", LocatorKind: "id", Occurrences: 1})
		})).To(Succeed())
		quarantineSearchVersion(s, hidden, "pending")
		observed := &snapshotObserved{Store: s}
		writes := 0
		observed.onPage = func(stage string, page domain.SearchSnapshotSlice) {
			if stage != "chunks" || page.Examined == 0 || writes == 3 {
				return
			}
			writes++
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				// Real worker cleanup is split across transactions, while the public
				// index remains quarantined. Exercise anchors, chunks and units separately.
				real := tx.(*writeTx)
				statements := []string{
					"DELETE FROM page_anchors WHERE version_id=?",
					"DELETE FROM search_chunks WHERE unit_id IN (SELECT unit_id FROM search_units WHERE version_id=?)",
					"DELETE FROM search_units WHERE version_id=?",
				}
				_, e := real.tx.Exec(statements[writes-1], hidden)
				return e
			})).To(Succeed())
		}
		out, e := query.New(observed, a, nil).Search(ctx, owner, query.SearchOptions{Text: "needle"})
		Expect(e).NotTo(HaveOccurred())
		Expect(writes).To(Equal(3))
		Expect(observed.snapshots).To(Equal(2))
		Expect(out.Items).NotTo(BeEmpty())
		Expect(out.Index.VersionsTotal).To(Equal("2"))
		observed = &snapshotObserved{Store: s}
		released := false
		observed.onPage = func(stage string, page domain.SearchSnapshotSlice) {
			if released || stage != "chunks" || page.Examined == 0 {
				return
			}
			released = true
			quarantineSearchVersion(s, hidden, "indexing")
		}
		_, e = query.New(observed, a, nil).Search(ctx, owner, query.SearchOptions{Text: "needle"})
		Expect(e).NotTo(HaveOccurred())
		Expect(released).To(BeTrue())
		Expect(observed.snapshots).To(Equal(3))
	})

})
