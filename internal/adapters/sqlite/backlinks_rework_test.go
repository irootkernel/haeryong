package sqlite

import (
	"context"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// Test-owned turn barrier and iterator records, backed by the real Store/SQL.
type backlinkAuditStore struct {
	app.Store
	beforeCounts           func(app.WriteTx) error
	units, chunks, anchors int
}
type backlinkAuditTx struct {
	app.WriteTx
	owner *backlinkAuditStore
}

func (s *backlinkAuditStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error { return fn(&backlinkAuditTx{WriteTx: tx, owner: s}) })
}
func (t *backlinkAuditTx) OutgoingEdgeKeys(v, after string, limit int) ([]domain.EdgeKey, error) {
	if barrier := t.owner.beforeCounts; barrier != nil {
		t.owner.beforeCounts = nil
		if err := barrier(t.WriteTx); err != nil {
			return nil, err
		}
	}
	return t.WriteTx.OutgoingEdgeKeys(v, after, limit)
}
func (t *backlinkAuditTx) SearchAuditUnits(v string, after *domain.SearchUnit, limit int) ([]domain.SearchUnit, error) {
	rows, err := t.WriteTx.SearchAuditUnits(v, after, limit)
	t.owner.units += len(rows)
	for _, u := range rows {
		Expect(u.VersionID).To(Equal(v))
	}
	Expect(len(rows)).To(BeNumerically("<=", 64))
	return rows, err
}
func (t *backlinkAuditTx) SearchAuditChunks(unit int64, part string, after, limit int) ([]int, error) {
	rows, err := t.WriteTx.SearchAuditChunks(unit, part, after, limit)
	t.owner.chunks += len(rows)
	Expect(len(rows)).To(BeNumerically("<=", 64))
	return rows, err
}
func (t *backlinkAuditTx) SearchAuditAnchors(v, page, after string, limit int) ([]domain.PageAnchor, error) {
	rows, err := t.WriteTx.SearchAuditAnchors(v, page, after, limit)
	t.owner.anchors += len(rows)
	for _, a := range rows {
		Expect(a.VersionID).To(Equal(v))
		Expect(a.PagePath).To(Equal(page))
	}
	Expect(len(rows)).To(BeNumerically("<=", 64))
	return rows, err
}

var _ = Describe("BL RR1 deterministic corrections", Label("persistence"), func() {
	It("F1 rejoins returned references after a target changes between row validation and counts", func() {
		s, auth, w, _ := authFixture(GinkgoT())
		_, owner := pairOwner(GinkgoT(), auth, "F1 owner")
		f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
		target := f.publish(nil, "<p id='retry'>target</p>")
		f.run(target, "search")
		f.run(target, "references")
		a, b := f.declaration(target, "a"), f.declaration(target, "b")
		a["label"], b["label"] = "F1 private canary", "F1 private canary"
		source := f.publish([]any{a, b}, "<p id='retry'>source</p>")
		f.run(source, "search")
		f.run(source, "references")
		before, err := query.New(s, auth, f.bytes).References(ctx, owner.Token, source, query.Options{Limit: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(before.Items[0].Resolution).To(Equal("resolved"))
		observed := &backlinkAuditStore{Store: s}
		mutated := false
		observed.beforeCounts = func(tx app.WriteTx) error {
			mutated = true
			return tx.(*writeTx).exec(`UPDATE documents SET lifecycle='trash' WHERE document_id=(SELECT document_id FROM document_versions WHERE version_id=?)`, target)
		}
		q := query.New(observed, appauth.New(observed), f.bytes)
		got, err := q.References(ctx, owner.Token, source, query.Options{Limit: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(mutated).To(BeTrue())
		Expect(got.Counts).To(Equal(query.OutgoingCounts{EdgeCount: "0", Documents: "0"}))
		Expect(got.Items[0].Resolution).To(Equal("unavailable"))
		Expect(got.Items[0].Label).To(BeNil())
		Expect(got.Items[0].Target).To(BeNil())
		Expect(got.Items[0].ReaderURL).To(BeNil())
		Expect(got.NextCursor).To(Equal(before.NextCursor), "target changes must not bind declaration cursors")
	})

	It("F1 refreshes unavailable rows when a target becomes active before counts without rebinding cursors", func() {
		s, auth, w, _ := authFixture(GinkgoT())
		_, owner := pairOwner(GinkgoT(), auth, "F1 restored owner")
		f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
		target := f.publish(nil, "<p id='retry'>target</p>")
		f.run(target, "search")
		f.run(target, "references")
		a, b := f.declaration(target, "a"), f.declaration(target, "b")
		a["label"], b["label"] = "F1 restored label", "F1 restored label"
		source := f.publish([]any{a, b}, "<p id='retry'>source</p>")
		f.run(source, "search")
		f.run(source, "references")
		edge, err := f.edge(source, "a")
		Expect(err).NotTo(HaveOccurred())
		_, err = s.db.Exec(`UPDATE documents SET lifecycle='trash' WHERE document_id=(SELECT document_id FROM document_versions WHERE version_id=?)`, target)
		Expect(err).NotTo(HaveOccurred())
		before, err := query.New(s, auth, f.bytes).References(ctx, owner.Token, source, query.Options{Limit: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(before.Items[0].Resolution).To(Equal("unavailable"))
		Expect(before.Counts).To(Equal(query.OutgoingCounts{EdgeCount: "0", Documents: "0"}))
		Expect(before.NextCursor).NotTo(BeNil())
		observed := &backlinkAuditStore{Store: s}
		mutated := false
		observed.beforeCounts = func(tx app.WriteTx) error {
			mutated = true
			return tx.(*writeTx).exec(`UPDATE documents SET lifecycle='active' WHERE document_id=(SELECT document_id FROM document_versions WHERE version_id=?)`, target)
		}
		q := query.New(observed, appauth.New(observed), f.bytes)
		got, err := q.References(ctx, owner.Token, source, query.Options{Limit: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(mutated).To(BeTrue())
		Expect(got.Counts).To(Equal(query.OutgoingCounts{EdgeCount: "2", Documents: "1"}))
		Expect(got.Items).To(HaveLen(1))
		Expect(got.Items[0]).To(Equal(query.ReferenceView{
			ID: "a", From: query.ReferenceContext{PagePath: refText("page.html"), RegionID: refText("retry")},
			Label: refText("F1 restored label"), Resolution: "resolved", EdgeID: &edge.ID,
			Target: query.ExactTarget{DocumentID: edge.TargetDocumentID, VersionID: target,
				PagePath: refText("page.html"), RegionID: refText("retry"), RequestedPath: refText("page.html"), ResolvedPath: refText("page.html")},
			ReaderURL: refText("/documents/" + edge.TargetDocumentID + "/versions/" + target + "?page=page.html#region=retry"),
		}))
		Expect(got.NextCursor).To(Equal(before.NextCursor), "target restoration must not bind declaration cursors")
	})

	It("F2 compares required inventory/chunks/anchors, keeps empty results and visits selected prefixes only", func() {
		s, auth, w, _ := authFixture(GinkgoT())
		_, owner := pairOwner(GinkgoT(), auth, "F2 owner")
		f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
		version := f.publish(nil, "<p id='retry'>required nonempty body</p>")
		f.run(version, "search")
		f.run(version, "references")
		q := query.New(s, auth, f.bytes)
		for _, damage := range []string{
			`DELETE FROM search_units WHERE version_id=?`,
			`DELETE FROM search_chunks WHERE part='body' AND unit_id IN(SELECT unit_id FROM search_units WHERE version_id=?)`,
			`DELETE FROM page_anchors WHERE version_id=?`,
			`INSERT INTO search_chunks(unit_id,part,ordinal,title,path,terms,body,display_body) SELECT unit_id,'body',99999,'','','','','' FROM search_units WHERE version_id=? AND kind='page'`,
		} {
			_, err := s.db.Exec(damage, version)
			Expect(err).NotTo(HaveOccurred())
			var index domain.SearchIndex
			Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; index, e = tx.SearchIndex(version); return e })).To(Succeed())
			Expect(index.State).To(Equal("complete"))
			Expect(index.DiscoveryComplete).To(BeTrue())
			bad, err := q.ValidateVersionIndexes(ctx, owner.Token, version, "search")
			Expect(err).NotTo(HaveOccurred())
			Expect(bad.State).To(Equal("inconsistent"), damage)
			if strings.HasPrefix(damage, "DELETE") {
				Expect(bad.Counts.Missing).NotTo(Equal("0"))
			} else {
				Expect(bad.Counts.Unexpected).NotTo(Equal("0"))
			}
			_, err = q.RebuildVersionIndexes(ctx, owner.Token, version, f.id(), "search")
			Expect(err).NotTo(HaveOccurred())
			f.run(version, "search")
			f.run(version, "references")
			good, err := q.ValidateVersionIndexes(ctx, owner.Token, version, "search")
			Expect(err).NotTo(HaveOccurred())
			Expect(good.State).To(Equal("consistent"), damage)
		}
		empty := f.publish(nil, "<div></div>")
		f.run(empty, "search")
		f.run(empty, "references")
		good, err := q.ValidateVersionIndexes(ctx, owner.Token, empty, "search")
		Expect(err).NotTo(HaveOccurred())
		Expect(good.State).To(Equal("consistent"))
		observed := &backlinkAuditStore{Store: s}
		selected := query.New(observed, appauth.New(observed), f.bytes)
		baseline, err := selected.ValidateVersionIndexes(ctx, owner.Token, version, "search")
		Expect(err).NotTo(HaveOccurred())
		units, chunks, anchors := observed.units, observed.chunks, observed.anchors
		unrelated := f.publish(nil, "<p id='other'>unrelated graph</p>")
		f.run(unrelated, "search")
		f.run(unrelated, "references")
		observed.units, observed.chunks, observed.anchors = 0, 0, 0
		after, err := selected.ValidateVersionIndexes(ctx, owner.Token, version, "search")
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(Equal(baseline))
		Expect([]int{observed.units, observed.chunks, observed.anchors}).To(Equal([]int{units, chunks, anchors}))
		_, err = s.db.Exec(`UPDATE search_units SET state='future_state' WHERE version_id=? AND kind='document'`, version)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Write(ctx, func(tx app.WriteTx) error { _, e := tx.SearchAuditUnits(version, nil, 64); return e })).To(MatchError(domain.ErrUnsupportedDerivedData), "COORD-1 typed read failures cannot become filtered zero values")
		_, err = q.ValidateVersionIndexes(ctx, owner.Token, version, "search")
		Expect(err).To(HaveOccurred())
	})

	It("F3 reports pending anchor prerequisites as incomplete and missing/ambiguous anchors as complete observations", func() {
		s, auth, w, _ := authFixture(GinkgoT())
		_, owner := pairOwner(GinkgoT(), auth, "F3 owner")
		f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
		target := f.publish(nil, "<p id='retry'>target</p><i id='dup'>a</i><b id='dup'>b</b>")
		f.run(target, "search")
		f.run(target, "references")
		missing, ambiguous := f.declaration(target, "missing"), f.declaration(target, "ambiguous")
		missing["region_id"], ambiguous["region_id"] = "absent", "dup"
		source := f.publish([]any{f.declaration(target, "valid"), missing, ambiguous}, "<p id='retry'>source</p>")
		f.run(source, "search")
		f.run(source, "references")
		q := query.New(s, auth, f.bytes)
		before, err := q.ValidateVersionIndexes(ctx, owner.Token, source, "references")
		Expect(err).NotTo(HaveOccurred())
		Expect(before.State).To(Equal("consistent"))
		_, err = q.RebuildVersionIndexes(ctx, owner.Token, target, f.id(), "search")
		Expect(err).NotTo(HaveOccurred())
		// Advance bounded cleanup/inventory turns, then stop before page EOF.
		// Queuing or one cleanup turn can still retain the prior valid page.
		f.hold(target)
		rows, err := q.References(ctx, owner.Token, source, query.Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(rows.Items[0].Reason).To(Equal(refText("anchor_pending")))
		pending, err := q.ValidateVersionIndexes(ctx, owner.Token, source, "references")
		Expect(err).NotTo(HaveOccurred())
		Expect(pending.State).To(Equal("incomplete"))
		Expect(pending.Reasons).To(ContainElement("anchor_prerequisite_pending"))
		f.run(target, "search")
		f.run(target, "references")
		after, err := q.ValidateVersionIndexes(ctx, owner.Token, source, "references")
		Expect(err).NotTo(HaveOccurred())
		Expect(after.State).To(Equal("consistent"))
	})

	It("F4 rejects same-source covering-key collisions by the NOT INDEXED rowid base and repairs them", func() {
		s, auth, w, _ := authFixture(GinkgoT())
		_, owner := pairOwner(GinkgoT(), auth, "F4 owner")
		f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
		target := f.publish(nil, "<p id='retry'>target</p>")
		f.run(target, "search")
		f.run(target, "references")
		source := f.publish([]any{f.declaration(target, "a"), f.declaration(target, "C06_FAULT")}, "<p id='retry'>source</p>")
		f.run(source, "search")
		f.run(source, "references")
		damageReverseIndexWith(s, filepath.Join(w.Profile.DataDir, "storage", "library.sqlite"), target, func(original string) string {
			return strings.Replace(original, "metadata_reference_id)", `(CASE WHEN metadata_reference_id='C06_FAULT' THEN 'a' ELSE metadata_reference_id END))`, 1)
		})
		s = New(nil)
		_, closeStore, err := s.Prepare(ctx, w.Profile)
		Expect(err).NotTo(HaveOccurred())
		defer closeStore()
		f.s = s
		var collisions int
		Expect(s.db.QueryRow(`SELECT count(*) FROM authored_edges INDEXED BY authored_edges_target WHERE target_version_id=? AND metadata_reference_id='a'`, target).Scan(&collisions)).To(Succeed())
		Expect(collisions).To(Equal(2), "actual equal-cardinality same-source covering-key collision required")
		q := query.New(s, appauth.New(s), f.bytes)
		_, err = q.Backlinks(ctx, owner.Token, target, query.BacklinkOptions{})
		Expect(err).To(HaveOccurred(), "never return duplicate edge IDs/counts")
		bad, err := q.ValidateVersionIndexes(ctx, owner.Token, target, "references")
		Expect(err).NotTo(HaveOccurred())
		Expect(bad.State).To(Equal("inconsistent"))
		Expect(bad.Counts.Mismatched).NotTo(Equal("0"))
		_, err = q.RebuildVersionIndexes(ctx, owner.Token, target, f.id(), "references")
		Expect(err).NotTo(HaveOccurred())
		f.run(target, "references")
		good, err := q.ValidateVersionIndexes(ctx, owner.Token, target, "references")
		Expect(err).NotTo(HaveOccurred())
		Expect(good.State).To(Equal("consistent"))
		page, err := q.Backlinks(ctx, owner.Token, target, query.BacklinkOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(page.Counts.EdgeCount).To(Equal("2"))
		Expect(page.Items).To(HaveLen(2))
		Expect(page.Items[0].EdgeID).NotTo(Equal(page.Items[1].EdgeID))
	})
})
