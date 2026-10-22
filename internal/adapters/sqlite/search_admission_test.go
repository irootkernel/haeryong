package sqlite

import (
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var _ = Describe("SC11 derived storage admission", Label("persistence"), func() {
	It("admits derived_index and search records, preserves immutable supplied regions (reversal: CreateJob derived_index/typed admission)", func() {
		t := GinkgoT()
		_, w := profile(t)
		s := openStore(t, w, nil)
		version, err := searchSeed(s)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.CreateJob(domain.Job{ID: "search-job", Kind: "derived_index"}); e != nil {
				return e
			}
			if e := tx.PutDerivedWork(domain.DerivedWork{JobID: "search-job", VersionID: version, WorkKind: "search", ExtractorRevision: domain.SearchExtractorRevision + 1, AfterPath: "pages/é.html"}); e != nil {
				return e
			}
			if e := tx.PutSearchIndex(domain.SearchIndex{VersionID: version, JobID: "search-job", ExtractorRevision: 1, Revision: 1, State: "pending"}); e != nil {
				return e
			}
			if e := tx.PutPageAnchor(domain.PageAnchor{VersionID: version, PagePath: "pages/é.html", ID: "retry", LocatorKind: "both", Occurrences: 1, Label: "Derived"}); e != nil {
				return e
			}
			for _, u := range []domain.SearchUnit{
				{Kind: "document"}, {Kind: "page", PagePath: "pages/é.html"}, {Kind: "region", PagePath: "pages/é.html", RegionID: "retry"}, {Kind: "source", SourceID: "git-source"}, {Kind: "attachment", PagePath: "opaque.unknown"},
			} {
				u.VersionID = version
				u.State = "pending"
				u.BodyState = "pending"
				u.AnchorState = "not_applicable"
				id, e := tx.PutSearchUnit(u)
				if e != nil {
					return e
				}
				got, e := tx.SearchUnit(id)
				if e != nil {
					return e
				}
				u.ID = id
				Expect(got).To(Equal(u))
			}
			work, e := tx.DerivedWork("search-job")
			if e != nil {
				return e
			}
			Expect(work.AfterPath).To(Equal("pages/é.html"))
			index, e := tx.SearchIndex(version)
			if e != nil {
				return e
			}
			Expect(index.State).To(Equal("pending"))
			a, e := tx.PageAnchor(version, "pages/é.html", "retry")
			if e != nil {
				return e
			}
			Expect(a.Label).To(Equal("Derived"))
			return nil
		})).To(Succeed())
		region, err := s.Region(ctx, version, "pages/é.html", "retry")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(region.Metadata)).To(Equal(`{"label":"Supplied region"}`))
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.CreateJob(domain.Job{ID: "future-job", Kind: "future_kind"}) })).NotTo(Succeed())
	})
	It("rejects every current enum and anchor-shape violation before SQL (reversal: validateSearchUnit/Index/Work/Chunk/Anchor allowlists)", func() {
		t := GinkgoT()
		_, w := profile(t)
		s := openStore(t, w, nil)
		version, err := searchSeed(s)
		Expect(err).NotTo(HaveOccurred())
		valid := domain.SearchUnit{VersionID: version, Kind: "region", PagePath: "p", RegionID: "r", State: "pending", BodyState: "pending", AnchorState: "pending"}
		invalid := []domain.SearchUnit{}
		for _, field := range []string{"kind", "state", "body", "anchor", "shape"} {
			u := valid
			switch field {
			case "kind":
				u.Kind = "future"
			case "state":
				u.State = "future"
			case "body":
				u.BodyState = "future"
			case "anchor":
				u.AnchorState = "future"
			case "shape":
				u.PagePath = ""
			}
			invalid = append(invalid, u)
		}
		for _, u := range []domain.SearchUnit{{Kind: "document", PagePath: "p"}, {Kind: "page"}, {Kind: "page", PagePath: "p", RegionID: "r"}, {Kind: "attachment", PagePath: "p", SourceID: "s"}, {Kind: "source", SourceID: "s", PagePath: "p"}} {
			u.VersionID = version
			u.State = "pending"
			u.BodyState = "pending"
			u.AnchorState = "pending"
			invalid = append(invalid, u)
		}
		for _, u := range invalid {
			Expect(s.Write(ctx, func(tx app.WriteTx) error { _, e := tx.PutSearchUnit(u); return e })).To(MatchError(domain.ErrUnsupportedDerivedData))
		}
		for _, fn := range []func(app.WriteTx) error{
			func(tx app.WriteTx) error {
				return tx.PutDerivedWork(domain.DerivedWork{JobID: "job", VersionID: version, WorkKind: "future", ExtractorRevision: 1})
			},
			func(tx app.WriteTx) error {
				return tx.PutSearchIndex(domain.SearchIndex{VersionID: version, ExtractorRevision: 1, Revision: 1, State: "future"})
			},
			func(tx app.WriteTx) error { return tx.PutSearchChunk(domain.SearchChunk{UnitID: 1, Part: "future"}) },
			func(tx app.WriteTx) error {
				return tx.PutPageAnchor(domain.PageAnchor{VersionID: version, PagePath: "p", ID: "r", LocatorKind: "future", Occurrences: 1})
			},
		} {
			Expect(s.Write(ctx, fn)).To(MatchError(domain.ErrUnsupportedDerivedData))
		}
	})
	It("allows nonempty future SQL enums but fails unknown readback and enforces structural constraints (reversal: schema8 nonempty checks/Go read validation)", func() {
		t := GinkgoT()
		_, w := profile(t)
		s := openStore(t, w, nil)
		version, err := searchSeed(s)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			return tx.CreateJob(domain.Job{ID: "future-work-job", Kind: "derived_index"})
		})).To(Succeed())
		statements := []string{
			`INSERT INTO derived_work VALUES('future-work-job','search-version','future_work',1,'')`,
			`UPDATE search_indexes SET state='future_state' WHERE version_id='search-version'`,
			`INSERT INTO search_units VALUES(100,'search-version','future_kind','p','r','','','', 'future_state','future_body','future_anchor','')`,
			`INSERT INTO search_chunks VALUES(100,100,'future_part',0,'','','','','')`,
			`INSERT INTO page_anchors VALUES('search-version','pages/é.html','retry','future_locator',1,'','')`,
		}
		for _, sql := range statements {
			_, err := s.db.Exec(sql)
			Expect(err).NotTo(HaveOccurred(), sql)
		}
		for _, fn := range []func(app.WriteTx) error{
			func(tx app.WriteTx) error { _, e := tx.DerivedWork("future-work-job"); return e },
			func(tx app.WriteTx) error { _, e := tx.SearchIndex(version); return e },
			func(tx app.WriteTx) error { _, e := tx.SearchUnit(100); return e },
			func(tx app.WriteTx) error { _, e := tx.PageAnchor(version, "pages/é.html", "retry"); return e },
		} {
			err := s.Write(ctx, fn)
			Expect(errors.Is(err, domain.ErrUnsupportedDerivedData)).To(BeTrue())
		}
		for _, sql := range []string{
			`UPDATE derived_work SET work_kind=''`, `UPDATE search_indexes SET state=''`,
			`UPDATE search_units SET kind=''`, `UPDATE search_units SET state=''`, `UPDATE search_units SET body_state=''`, `UPDATE search_units SET anchor_state=''`,
			`UPDATE search_chunks SET part=''`, `UPDATE page_anchors SET locator_kind=''`,
			`UPDATE derived_work SET extractor_revision=0`, `UPDATE search_indexes SET discovery_complete=2`,
			`UPDATE search_chunks SET ordinal=-1`, `UPDATE page_anchors SET occurrences=0`,
			`UPDATE search_units SET page_path=''`, `UPDATE page_anchors SET version_id='missing'`,
		} {
			_, err := s.db.Exec(sql)
			Expect(err).To(HaveOccurred(), sql)
		}
	})
})
