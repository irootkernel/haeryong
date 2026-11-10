package query_test

import (
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("T-008-03 composes fixed pages, partial and late scoped history, exact trigger resolution and latest availability exception", Label("unit"), func() {
	t := GinkgoT()
	v := newAssessmentsView(t)
	f := v.fixture
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var body map[string]any
		Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
		body["projects"].(map[string]any)[f.project].(map[string]any)["source_mode_default"] = "current"
		cfg.Value, _ = json.Marshal(body)
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
	target, run := key(t), key(t)
	set := v.seedRun(t, run, key(t), target, `{"kind":"version"}`, []domain.SourceObservation{{ID: key(t), SourceID: "git-1", Availability: "available", Classification: "direct", ConfigRevision: 1, PermissionsRevision: 1, BaseRevision: "aaaa", ObservedRevision: "bbbb"}}, []domain.RequiredUnit{{Kind: "page", PagePath: "pages/first.html"}, {Kind: "page", PagePath: "pages/second.html"}})
	put := func(cid, scope, at string, us []domain.AssessmentUnit) {
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			cs, e := tx.CoverageSetByID(set)
			if e != nil {
				return e
			}
			if cid != set {
				cs.ID = cid
				cs.RunID = ""
				cs.Origin = "semantic"
				cs.ScopeJSON = []byte(scope)
				cs.UnitKind = us[0].Kind
				if e = tx.PutCoverageSet(cs); e != nil {
					return e
				}
				for _, u := range us {
					if e = tx.PutRequiredUnit(domain.RequiredUnit{CoverageSetID: cid, TargetSetID: target, Kind: u.Kind, PagePath: u.PagePath, RegionID: u.RegionID}); e != nil {
						return e
					}
				}
			}
			aid := key(t)
			for i := range us {
				us[i].CoverageSetID = cid
				us[i].TargetSetID = target
				us[i].AssessmentID = aid
			}
			a := domain.Assessment{ID: aid, VersionID: v.versionID, TargetSetID: target, CoverageSetID: cid, ScopeJSON: cs.ScopeJSON, State: domain.AssessmentState(us), EvidenceJSON: []byte(`{}`), ConfigRevision: 1, PermissionsRevision: 1, Origin: "ai", AssessedAt: at}
			if e = tx.PutAssessment(a, us); e != nil {
				return e
			}
			return tx.ClearAssessmentTriggers(a)
		})).To(Succeed())
	}
	first := domain.AssessmentUnit{Kind: "page", PagePath: "pages/first.html", Outcome: "assessed"}
	second := domain.AssessmentUnit{Kind: "page", PagePath: "pages/second.html", Outcome: "not_checked", Reason: "controlled stop"}
	put(set, "", "2026-11-01T00:00:00.1Z", []domain.AssessmentUnit{first, second})
	a, e := v.applicable(t, query.AssessmentQuery{})
	Expect(e).NotTo(HaveOccurred())
	Expect(a["status"]).To(Equal("not_assessed"))
	Expect(a["coverage"].(map[string]any)["eligible"]).To(Equal(float64(2)))
	Expect(a["coverage"].(map[string]any)["assessed"]).To(Equal(float64(1)))
	// A newer region is not a page contribution and cannot change its time.
	put(key(t), `{"kind":"region","page_path":"pages/first.html","region_id":"R1"}`, "2026-11-04T00:00:00Z", []domain.AssessmentUnit{{Kind: "region", PagePath: "pages/first.html", RegionID: "R1", Outcome: "assessed", Discrepancy: "region only"}})
	a, e = v.applicable(t, query.AssessmentQuery{})
	Expect(e).NotTo(HaveOccurred())
	Expect(a["status"]).To(Equal("not_assessed"))
	Expect(a["assessed_at"]).To(Equal("2026-11-01T00:00:00.1Z"))
	second.Outcome = "assessed"
	second.Reason = ""
	second.Discrepancy = "changed claim"
	put(key(t), `{"kind":"page","page_path":"pages/second.html","region_id":null}`, "2026-11-01T00:00:00.11Z", []domain.AssessmentUnit{second})
	a, e = v.applicable(t, query.AssessmentQuery{})
	Expect(e).NotTo(HaveOccurred())
	Expect(a["status"]).To(Equal("outdated_against_target"))
	Expect(a["coverage"].(map[string]any)["complete"]).To(BeTrue())
	Expect(a["assessed_at"]).To(Equal("2026-11-01T00:00:00.11Z"))
	// A later unavailable attempt wins status while retaining the captured target,
	// discrepancy history and its complete C-bound coverage.
	v.seedRun(t, key(t), key(t), "", `{"kind":"version"}`, []domain.SourceObservation{{ID: key(t), SourceID: "git-1", Availability: "unavailable", Classification: "unavailable", Reason: "missing_source", ConfigRevision: 1, PermissionsRevision: 1}}, nil)
	a, e = v.applicable(t, query.AssessmentQuery{})
	Expect(e).NotTo(HaveOccurred())
	Expect(a["status"]).To(Equal("unable_to_verify"))
	Expect(a["target_set_id"]).To(Equal(target))
	Expect(a["coverage"].(map[string]any)["assessed"]).To(Equal(float64(2)))
	explicit, e := v.applicable(t, query.AssessmentQuery{TargetSetID: target})
	Expect(e).NotTo(HaveOccurred())
	Expect(explicit["status"]).To(Equal("outdated_against_target"))
})
