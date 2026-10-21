package source_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

const canonicalID = `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`

// citeRows adds one git evidence item of a version-local source — a new
// source pinned at revision when revision is set — and anchors it at a new
// region of page (no anchor when region is empty). Layout rows run before
// publication.
func citeRows(tx app.WriteTx, f *observationFixture, source, repo, revision, evidence, path, page, region string) error {
	if revision != "" {
		meta := `{"id":"` + source + `","kind":"git","retention_mode":"reference_only","repo_id":"` + repo + `","object_format":"sha1","exact_revision":"` + revision + `"}`
		if e := tx.PutSource(domain.Source{VersionID: f.version, ID: source, Metadata: json.RawMessage(meta)}); e != nil {
			return e
		}
	}
	raw, _ := json.Marshal(map[string]string{"id": evidence, "source_id": source, "capture_status": "reference_only", "path": path})
	if e := tx.PutEvidence(domain.Evidence{VersionID: f.version, ID: evidence, SourceID: source, Metadata: raw}); e != nil {
		return e
	}
	if region == "" {
		return nil
	}
	if e := tx.PutRegion(domain.Region{VersionID: f.version, PagePath: page, ID: region}); e != nil {
		return e
	}
	return tx.LinkRegionEvidence(f.version, page, region, evidence)
}

// citing is a layout that adds one cited evidence item (see citeRows).
func citing(source, repo, revision, evidence, path, page, region string) observationOption {
	return withLayout(nil, func(f *observationFixture, tx app.WriteTx) error {
		return citeRows(tx, f, source, repo, revision, evidence, path, page, region)
	})
}

func (f *observationFixture) triggers(t GinkgoTInterface, target string) []domain.ReassessmentTrigger {
	t.Helper()
	var rows []domain.ReassessmentTrigger
	ExpectWithOffset(1, f.store.Write(context.Background(), func(tx app.WriteTx) error {
		var e error
		rows, e = tx.TriggersByTargetSet(f.version, target)
		return e
	})).To(Succeed())
	return rows
}

// unitReasons keys coverage units by page, or page and region.
func unitReasons(units []map[string]any) map[string]string {
	out := map[string]string{}
	for _, unit := range units {
		key := mapString(unit["page_path"])
		if region := mapString(unit["region_id"]); region != "" {
			key += "#" + region
		}
		out[key] = mapString(unit["reason"])
	}
	return out
}

// sourceRow finds one per-source row of an observation response.
func sourceRow(run map[string]any, id string) map[string]any {
	for _, item := range run["sources"].([]any) {
		row := item.(map[string]any)
		if row["source_id"] == id {
			return row
		}
	}
	return nil
}

func (f *observationFixture) sourcesByID(t GinkgoTInterface) map[string]map[string]any {
	t.Helper()
	list, e := f.queries.ProjectSources(context.Background(), f.token, f.project, query.Options{})
	ExpectWithOffset(1, e).NotTo(HaveOccurred())
	out := map[string]map[string]any{}
	for _, item := range list.Items {
		out[item["source_id"].(string)] = item
	}
	return out
}

func (f *observationFixture) targetEntries(t GinkgoTInterface, target string) map[string]map[string]any {
	t.Helper()
	var set domain.TargetSet
	ExpectWithOffset(1, f.store.Write(context.Background(), func(tx app.WriteTx) error {
		var e error
		set, e = tx.TargetSetByID(target)
		return e
	})).To(Succeed())
	var value struct {
		Sources []map[string]any `json:"sources"`
	}
	ExpectWithOffset(1, json.Unmarshal(set.TargetsJSON, &value)).To(Succeed())
	out := map[string]map[string]any{}
	for _, entry := range value.Sources {
		out[mapString(entry["source_id"])] = entry
	}
	return out
}

var _ = Describe("observation identifiers and selection", Label("persistence"), func() {
	t := GinkgoT()

	// W3b L1 + L20: canonical UUID identifiers; a captured run's units carry
	// its target.
	It("issues canonical identifiers and binds captured units to the target", func() {
		f := newObservationFixture(t, "current")
		f.repoA.commit(t, map[string]string{"a.txt": "alpha changed\n"})
		run := mustObserve(t, f, "36000000-0000-4000-8000-000000000001", "")
		target := mapString(run["target_set_id"])
		Expect(target).To(MatchRegexp(canonicalID))
		Expect(mapString(run["run_id"])).To(MatchRegexp(canonicalID))
		Expect(mapString(run["coverage_set_id"])).To(MatchRegexp(canonicalID))
		for _, item := range run["sources"].([]any) {
			Expect(mapString(item.(map[string]any)["observation_id"])).To(MatchRegexp(canonicalID))
		}
		Expect(mapString(f.applicable(t, query.AssessmentQuery{})["target_set_id"])).To(MatchRegexp(canonicalID))
		for _, unit := range f.coverage(t, mapString(run["coverage_set_id"]), "") {
			Expect(unit["target_set_id"]).To(Equal(target), "a captured run's units carry its target")
		}
		triggers := f.triggers(t, target)
		Expect(triggers).NotTo(BeEmpty())
		for _, trigger := range triggers {
			Expect(trigger.ID).To(MatchRegexp(canonicalID))
		}
	})

	// W3b L4(a): writes and reads share one effective source set — a declared
	// mapped git source no evidence cites never splits the selection.
	It("selects the run of a version that also declares an uncited mapped source", func() {
		const repoC = "99999999-9999-4999-8999-999999999999"
		f := newObservationFixture(t, "current", withLayout(nil, func(f *observationFixture, tx app.WriteTx) error {
			meta := `{"id":"srcC","kind":"git","retention_mode":"reference_only","repo_id":"` + repoC + `","object_format":"sha1","exact_revision":"` + f.baseA + `"}`
			return tx.PutSource(domain.Source{VersionID: f.version, ID: "srcC", Metadata: json.RawMessage(meta)})
		}))
		f.configureMappings(t, "current", obsMapping("git-1", f.repoA.root, repoAID), obsMapping("git-2", f.repoB.root, repoBID), obsMapping("git-3", f.repoA.root, repoC))
		run := mustObserve(t, f, "36000000-0000-4000-8000-000000000011", "")
		Expect(run["sources"]).To(HaveLen(2), "an uncited source is not observed")
		Expect(f.applicable(t, query.AssessmentQuery{})["target_set_id"]).To(Equal(run["target_set_id"]))
	})

	// W3b L4(b): a version citing two identities has no matching one-source
	// verification capture; the pinned summary composes the no-capture form.
	It("never selects a one-identity verification capture for a two-identity version", func() {
		f := newObservationFixture(t, "pinned")
		verified, e := f.core.Verify(context.Background(), f.token, f.version, "evA", "36000000-0000-4000-8000-000000000021", []byte(`{}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(verified.Status).To(Equal(201))
		Expect(outcome(t, verified)["target_set_id"]).NotTo(BeNil(), "the verification captured its one-source target")
		applicable := f.applicable(t, query.AssessmentQuery{})
		Expect(applicable["target_set_id"]).To(BeNil(), "two identities: no one-source capture is the version's selection")
		Expect(applicable["status"]).To(Equal("not_assessed"))
	})

	// W3b L13: a created_at tie between two runs of one selection is broken by
	// run identity (r4.3 rule 2).
	It("breaks a created_at tie by run identity", func() {
		f := newObservationFixture(t, "current")
		f.clock.step = 0
		one := mustObserve(t, f, "36000000-0000-4000-8000-000000000031", "")
		two := mustObserve(t, f, "36000000-0000-4000-8000-000000000032", "")
		Expect(one["observed_at"]).To(Equal(two["observed_at"]), "identical created_at")
		expected := one
		if mapString(two["run_id"]) > mapString(one["run_id"]) {
			expected = two
		}
		Expect(f.applicable(t, query.AssessmentQuery{})["target_set_id"]).To(Equal(expected["target_set_id"]))
	})

	// W3b L9: the declared source intent is read from a manifest of any size.
	It("reads the declared source intent from a manifest larger than 4 MiB", func() {
		big := `{"schema_version":"1.0","padding":"` + strings.Repeat("x", 5<<20) + `","version":{"source_intent":"historical"}}`
		f := newObservationFixture(t, "current", withManifest(big))
		run := mustObserve(t, f, "36000000-0000-4000-8000-000000000041", "")
		Expect(run["target_set_id"]).NotTo(BeNil())
		applicable := f.applicable(t, query.AssessmentQuery{})
		Expect(applicable["source_intent"]).To(Equal("historical"))
		Expect(applicable["target_set_id"]).To(BeNil(), "historical intent selects the pinned family, never the HEAD capture")
	})

	// W3b L14: an explicit target or an attempt that never committed stays
	// 404 like a foreign id (r4.1 rule 4).
	It("answers 404 for a capture and an attempt that never committed", func() {
		f := newObservationFixture(t, "current")
		key := "36000000-0000-4000-8000-000000000051"
		response := observeParkedAt(t, f, key, "diff", func() { bumpConfig(t, &f.vFixture) })
		Expect(response.Status).To(Equal(412))
		state := readObservationState(t, f, key)
		Expect(state.runExists).To(BeTrue())
		Expect(state.runCaptured).NotTo(BeEmpty(), "stage 2 persisted the capture")
		_, e := f.queries.Assessments(context.Background(), f.token, f.version, query.AssessmentQuery{TargetSetID: state.runCaptured})
		Expect(e).To(MatchError(domain.ErrNotFound))
		run := mustObserve(t, f, "36000000-0000-4000-8000-000000000052", "")
		_, e = f.queries.AssessmentCoverage(context.Background(), f.token, f.version, mapString(run["coverage_set_id"]), state.runID, query.Options{})
		Expect(e).To(MatchError(domain.ErrNotFound))
		var missing *query.Missing
		Expect(errors.As(e, &missing)).To(BeTrue())
		Expect(missing.Details).To(HaveKeyWithValue("attempt_run_id", state.runID))
	})

	// W3b L16: one status rule (plan-phase note 3) for the default and the
	// explicit composition.
	It("applies one status rule to the default and the explicit composition", func() {
		f := newObservationFixture(t, "current")
		f.repoA.commit(t, map[string]string{"a.txt": "alpha changed\n"})
		f.repoB.breakRoot(t)
		run := mustObserve(t, f, "36000000-0000-4000-8000-000000000061", "")
		for _, q := range []query.AssessmentQuery{{}, {TargetSetID: mapString(run["target_set_id"])}} {
			view := f.applicable(t, q)
			Expect(view["status"]).To(Equal("unable_to_verify"), "a partial run is availability-blocked")
			Expect(view["reassessment_required"]).To(BeTrue())
		}
	})
})

var _ = Describe("observation attribution", Label("persistence"), func() {
	t := GinkgoT()

	// W3b L5: every evidence item is compared against its own declared base.
	It("compares every evidence item against its own declared base", func() {
		r2 := ""
		f := newObservationFixture(t, "current", withLayout(func(f *observationFixture) {
			r2 = f.repoA.commit(t, map[string]string{"q.txt": "q\n"})
		}, func(f *observationFixture, tx app.WriteTx) error {
			return citeRows(tx, f, "srcA2", repoAID, r2, "evQ", "q.txt", "pages/second.html", "R3")
		}))
		f.repoA.commit(t, map[string]string{"z.txt": "unrelated\n"})
		first := mustObserve(t, f, "37000000-0000-4000-8000-000000000001", "")
		gitOne := sourceRow(first, "git-1")
		Expect(gitOne["classification"]).To(Equal("indirect"), "q.txt is unchanged since evQ's own base")
		Expect(gitOne["changed_paths"]).To(BeEmpty())
		Expect(f.triggers(t, mapString(first["target_set_id"]))).To(BeEmpty())
		f.repoA.commit(t, map[string]string{"q.txt": "q changed\n"})
		second := mustObserve(t, f, "37000000-0000-4000-8000-000000000002", "")
		gitOne = sourceRow(second, "git-1")
		Expect(gitOne["classification"]).To(Equal("direct"))
		Expect(gitOne["changed_paths"]).To(ConsistOf("q.txt"))
		triggers := f.triggers(t, mapString(second["target_set_id"]))
		Expect(triggers).To(HaveLen(1))
		Expect(triggers[0].PagePath).To(Equal("pages/second.html"))
		Expect(triggers[0].RegionID).To(Equal("R3"))
	})

	// W3b L6: a unit's reason comes from its own cited evidence — the exact
	// region under a region scope, the page under a version scope.
	It("derives each unit's reason from its own cited evidence", func() {
		f := newObservationFixture(t, "current", citing("srcA", repoAID, "", "evA2", "a2.txt", "pages/first.html", "R1b"), citing("srcA", repoAID, "", "evA4", "a.txt", "pages/second.html", "R2b"))
		f.repoA.commit(t, map[string]string{"a2.txt": "added\n"})
		r1 := mustObserve(t, f, "37000000-0000-4000-8000-000000000011", `{"scope":{"kind":"region","page_path":"pages/first.html","region_id":"R1"}}`)
		Expect(unitReasons(f.coverage(t, mapString(r1["coverage_set_id"]), ""))).To(Equal(map[string]string{"pages/first.html#R1": "unexamined_dependency_scope"}))
		Expect(f.applicable(t, query.AssessmentQuery{ScopeKind: "region", PagePath: "pages/first.html", RegionID: "R1"})["reassessment_required"]).To(BeFalse(), "the change is anchored at R1b")
		r1b := mustObserve(t, f, "37000000-0000-4000-8000-000000000012", `{"scope":{"kind":"region","page_path":"pages/first.html","region_id":"R1b"}}`)
		Expect(unitReasons(f.coverage(t, mapString(r1b["coverage_set_id"]), ""))).To(Equal(map[string]string{"pages/first.html#R1b": "direct_source_change"}))
		triggers := f.triggers(t, mapString(r1b["target_set_id"]))
		Expect(triggers).To(HaveLen(1))
		Expect(triggers[0].RegionID).To(Equal("R1b"))
		whole := mustObserve(t, f, "37000000-0000-4000-8000-000000000013", "")
		reasons := unitReasons(f.coverage(t, mapString(whole["coverage_set_id"]), ""))
		Expect(reasons["pages/first.html"]).To(Equal("direct_source_change"), "a direct change outranks the unexamined scope")
		Expect(reasons["pages/second.html"]).To(Equal("unexamined_dependency_scope"), "unchanged cited evidence of an advanced source")
	})

	// W3b L6 + L20: the §7.6 precedence per unit, no_cited_evidence for a page
	// without cited evidence, also in an all-unavailable run.
	It("keeps the reason precedence and no_cited_evidence per unit", func() {
		f := newObservationFixture(t, "current", withLayout(nil, func(f *observationFixture, tx app.WriteTx) error {
			return tx.PutPage(domain.Page{VersionID: f.version, Path: "pages/third.html"})
		}), citing("srcA", repoAID, "", "evA5", "a5.txt", "pages/second.html", "R2c"))
		f.repoA.commit(t, map[string]string{"a5.txt": "added\n"})
		f.repoB.breakRoot(t)
		partial := mustObserve(t, f, "37000000-0000-4000-8000-000000000021", "")
		reasons := unitReasons(f.coverage(t, mapString(partial["coverage_set_id"]), ""))
		Expect(reasons["pages/second.html"]).To(Equal("missing_source"), "an availability reason outranks a direct change")
		Expect(reasons["pages/first.html"]).To(Equal("unexamined_dependency_scope"))
		Expect(reasons["pages/third.html"]).To(Equal("no_cited_evidence"))
		f.repoA.breakRoot(t)
		all := mustObserve(t, f, "37000000-0000-4000-8000-000000000022", "")
		Expect(all["target_set_id"]).To(BeNil())
		reasons = unitReasons(f.coverage(t, mapString(all["coverage_set_id"]), ""))
		Expect(reasons["pages/first.html"]).To(Equal("missing_source"))
		Expect(reasons["pages/second.html"]).To(Equal("missing_source"))
		Expect(reasons["pages/third.html"]).To(Equal("no_cited_evidence"), "never the failure reason of a source it does not cite")
	})

	// W3b L7: the overlay reaches the whole-version unit and attributes through
	// the links the runs recorded, not the current mappings.
	It("overlays the attempt's reasons on a whole-version unit through recorded links", func() {
		f := newObservationFixture(t, "current", withoutPages())
		f.repoA.commit(t, map[string]string{"a.txt": "alpha changed\n"})
		first := mustObserve(t, f, "37000000-0000-4000-8000-000000000031", "")
		setID := mapString(first["coverage_set_id"])
		plain := f.coverage(t, setID, "")
		Expect(plain).To(HaveLen(1))
		Expect(plain[0]["kind"]).To(Equal("version"))
		Expect(plain[0]["reason"]).To(Equal("direct_source_change"))
		Expect(f.triggers(t, mapString(first["target_set_id"]))[0].AnchorKind).To(Equal("version"))
		f.repoA.breakRoot(t)
		f.repoB.breakRoot(t)
		second := mustObserve(t, f, "37000000-0000-4000-8000-000000000032", "")
		attempt := mapString(second["run_id"])
		unitsURL := mapString(f.applicable(t, query.AssessmentQuery{})["coverage"].(map[string]any)["units_url"])
		Expect(unitsURL).To(ContainSubstring("attempt_run_id=" + attempt))
		Expect(f.coverage(t, setID, attempt)[0]["reason"]).To(Equal("missing_source"))
		Expect(f.coverage(t, setID, "")[0]["reason"]).To(Equal("direct_source_change"), "the plain endpoint keeps the stored reason")
		f.configureMappings(t, "current")
		Expect(f.coverage(t, setID, attempt)[0]["reason"]).To(Equal("missing_source"), "attribution never depends on the current mappings")
	})

	// W3b L15: an undeclared region still records the run's triggers and the
	// region_not_declared fact; only units and the coverage set are skipped.
	It("records triggers and region_not_declared for an undeclared region scope", func() {
		f := newObservationFixture(t, "current")
		f.repoA.commit(t, map[string]string{"a.txt": "alpha changed\n"})
		run := mustObserve(t, f, "37000000-0000-4000-8000-000000000041", `{"scope":{"kind":"region","page_path":"pages/first.html","region_id":"missing"}}`)
		scope := run["scope"].(map[string]any)
		Expect(scope).To(Equal(map[string]any{"kind": "region", "page_path": "pages/first.html", "region_id": "missing", "declared": false, "reason": "region_not_declared"}))
		Expect(run["coverage_set_id"]).To(BeNil())
		Expect(readObservationState(t, f, "37000000-0000-4000-8000-000000000041").runScope).To(Equal(scope), "the run stores the same five-field scope record")
		triggers := f.triggers(t, mapString(run["target_set_id"]))
		Expect(triggers).To(HaveLen(1))
		Expect(triggers[0].RegionID).To(Equal("R1"))
		Expect(sourceRow(run, "git-1")["reassessment_required"]).To(BeTrue())
		summary := f.applicable(t, query.AssessmentQuery{})
		Expect(summary["target_set_id"]).To(Equal(run["target_set_id"]))
		Expect(summary["reassessment_required"]).To(BeTrue())
		version := mustObserve(t, f, "37000000-0000-4000-8000-000000000042", "")
		record := map[string]any{"kind": "version", "page_path": nil, "region_id": nil, "declared": true, "reason": nil}
		Expect(version["scope"]).To(Equal(record))
		Expect(readObservationState(t, f, "37000000-0000-4000-8000-000000000042").runScope).To(Equal(record))
	})

	// W3b L10: cited paths with non-ASCII characters and spaces compare byte
	// for byte; an absent declared base is a per-source missing_source.
	It("matches cited paths with Hangul and spaces and records an absent base", func() {
		const hangul = "문서 a.txt"
		r2 := ""
		f := newObservationFixture(t, "current", withLayout(func(f *observationFixture) {
			r2 = f.repoA.commit(t, map[string]string{hangul: "문서\n"})
		}, func(f *observationFixture, tx app.WriteTx) error {
			return citeRows(tx, f, "srcH", repoAID, r2, "evH", hangul, "pages/first.html", "RH")
		}))
		f.repoA.commit(t, map[string]string{"z.txt": "unrelated\n"})
		unchanged := mustObserve(t, f, "37000000-0000-4000-8000-000000000051", "")
		Expect(sourceRow(unchanged, "git-1")["classification"]).To(Equal("indirect"))
		f.repoA.commit(t, map[string]string{hangul: "문서 changed\n"})
		changed := mustObserve(t, f, "37000000-0000-4000-8000-000000000052", "")
		Expect(sourceRow(changed, "git-1")["classification"]).To(Equal("direct"))
		Expect(sourceRow(changed, "git-1")["changed_paths"]).To(ConsistOf(hangul))
		Expect(f.triggers(t, mapString(changed["target_set_id"]))[0].RegionID).To(Equal("RH"))
		g := newObservationFixture(t, "current", citing("srcX", repoAID, strings.Repeat("0123456789", 4), "evX", "x.txt", "", ""))
		run := mustObserve(t, g, "37000000-0000-4000-8000-000000000053", "")
		Expect(sourceRow(run, "git-1")["availability"]).To(Equal("unavailable"))
		Expect(sourceRow(run, "git-1")["reason"]).To(Equal("missing_source"))
		Expect(sourceRow(run, "git-2")["availability"]).To(Equal("available"))
		entries := g.targetEntries(t, mapString(run["target_set_id"]))
		Expect(entries["git-1"]["status"]).To(Equal("unavailable"), "an absent base is never a captured source")
	})
})

var _ = Describe("observation records", Label("persistence"), func() {
	t := GinkgoT()

	// W3b L18: r4.2(a) per-source entries and r4.2(c) last_observed_revision.
	It("records per-source target entries and keeps the last captured revision", func() {
		f := newObservationFixture(t, "current")
		headA := f.repoA.head(t)
		f.repoB.breakRoot(t)
		run := mustObserve(t, f, "38000000-0000-4000-8000-000000000001", "")
		entries := f.targetEntries(t, mapString(run["target_set_id"]))
		Expect(entries["git-1"]).To(Equal(map[string]any{"source_id": "git-1", "status": "captured", "revision": headA}))
		Expect(entries["git-2"]).To(Equal(map[string]any{"source_id": "git-2", "status": "unavailable", "reason": "missing_source"}))
		f.repoA.breakRoot(t)
		mustObserve(t, f, "38000000-0000-4000-8000-000000000002", "")
		sources := f.sourcesByID(t)
		Expect(sources["git-1"]["last_availability"]).To(Equal("unavailable"))
		Expect(sources["git-1"]["last_observation_reason"]).To(Equal("missing_source"))
		Expect(sources["git-1"]["last_observed_revision"]).To(Equal(headA), "the last captured revision stays (note 4)")
		Expect(sources["git-2"]["last_observed_revision"]).To(BeNil())
	})

	// W3b L18: a version-anchored trigger is invisible to page and region
	// scopes; a partial-source run leaves another source's triggers untouched.
	It("scopes version-anchored triggers and isolates partial-source runs", func() {
		f := newObservationFixture(t, "current", citing("srcA", repoAID, "", "evV", "v.txt", "", ""))
		f.repoA.commit(t, map[string]string{"v.txt": "added\n"})
		run := mustObserve(t, f, "38000000-0000-4000-8000-000000000011", "")
		target := mapString(run["target_set_id"])
		triggers := f.triggers(t, target)
		Expect(triggers).To(HaveLen(1))
		Expect(triggers[0].AnchorKind).To(Equal("version"))
		Expect(f.applicable(t, query.AssessmentQuery{})["reassessment_required"]).To(BeTrue())
		Expect(f.applicable(t, query.AssessmentQuery{ScopeKind: "page", PagePath: "pages/first.html"})["reassessment_required"]).To(BeFalse())
		Expect(f.applicable(t, query.AssessmentQuery{ScopeKind: "region", PagePath: "pages/first.html", RegionID: "R1"})["reassessment_required"]).To(BeFalse())
		partial := mustObserve(t, f, "38000000-0000-4000-8000-000000000012", `{"source_ids":["git-2"]}`)
		Expect(f.triggers(t, mapString(partial["target_set_id"]))).To(BeEmpty())
		after := f.triggers(t, target)
		Expect(after).To(HaveLen(1))
		Expect(after[0].ID).To(Equal(triggers[0].ID), "the other run's trigger row is untouched")
		Expect(f.applicable(t, query.AssessmentQuery{TargetSetID: target})["reassessment_required"]).To(BeTrue())
	})

	// W3b L18 (plan r4 §7.3): a branch move after reservation is captured at
	// resolution once; a move after the target persist is never re-resolved.
	It("captures the resolution-time HEAD once", func() {
		f := newObservationFixture(t, "current")
		moved := ""
		response := observeParkedAt(t, f, "38000000-0000-4000-8000-000000000021", "resolve", func() {
			moved = f.repoA.commit(t, map[string]string{"a.txt": "moved before resolution\n"})
		})
		Expect(response.Status).To(Equal(201))
		run := outcome(t, response)
		Expect(f.targetEntries(t, mapString(run["target_set_id"]))["git-1"]["revision"]).To(Equal(moved))
		later := ""
		response = observeParkedAt(t, f, "38000000-0000-4000-8000-000000000022", "diff", func() {
			later = f.repoA.commit(t, map[string]string{"a.txt": "moved after the target persist\n"})
		})
		Expect(response.Status).To(Equal(201))
		run = outcome(t, response)
		Expect(later).NotTo(Equal(moved))
		Expect(f.targetEntries(t, mapString(run["target_set_id"]))["git-1"]["revision"]).To(Equal(moved), "nothing re-resolves after the acceptance point")
		Expect(f.sourcesByID(t)["git-1"]["last_observed_revision"]).To(Equal(moved))
	})
})

var _ = Describe("observation source facts and event masking", Label("persistence"), func() {
	t := GinkgoT()

	// W3d (S12, plan r4 §7.6): a mode-only change is no cited byte difference,
	// and an unreadable checkout settles typed instead of becoming a recorded
	// missing_source.
	It("never records an unreadable checkout or a mode-only change as a source fact", func() {
		if os.Geteuid() == 0 {
			Skip("permission bits do not bind the superuser")
		}
		f := newObservationFixture(t, "current")
		Expect(os.Chmod(filepath.Join(f.repoA.root, "a.txt"), 0o755)).To(Succeed())
		f.repoA.git(t, "add", "-A")
		f.repoA.git(t, "commit", "-m", "mode only")
		run := mustObserve(t, f, "3a000000-0000-4000-8000-000000000001", "")
		Expect(sourceRow(run, "git-1")["classification"]).To(Equal("indirect"), "a mode-only change is no cited byte difference")
		Expect(sourceRow(run, "git-1")["changed_paths"]).To(BeEmpty())
		Expect(f.triggers(t, mapString(run["target_set_id"]))).To(BeEmpty())
		gitDir := filepath.Join(f.repoA.root, ".git")
		Expect(os.Chmod(gitDir, 0)).To(Succeed())
		DeferCleanup(func() { _ = os.Chmod(gitDir, 0o755) })
		key := "3a000000-0000-4000-8000-000000000002"
		response, e := f.observe(key, "")
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(503))
		Expect(errorCode(t, response)).To(Equal("FILE_ACCESS_ERROR"))
		Expect(errorReason(t, response)).To(Equal("source_read_failed"), "an I/O error is kind-neutral, never git_unavailable")
		state := readObservationState(t, f, key)
		Expect(state.rows).To(Equal(0))
		Expect(state.intent).To(Equal("failed"))
		Expect(state.receipt).To(Equal("failed"))
		Expect(os.Chmod(gitDir, 0o755)).To(Succeed())
		Expect(f.sourcesByID(t)["git-1"]["last_availability"]).To(Equal("available"), "the unreadable checkout recorded nothing")
	})

	// W3d: a source event names its observed version and stays visible only
	// while that version does.
	It("masks source events once their version leaves visibility", func() {
		f := newObservationFixture(t, "current")
		f.repoA.commit(t, map[string]string{"a.txt": "alpha changed\n"})
		mustObserve(t, f, "3a000000-0000-4000-8000-000000000011", "")
		var events []domain.Event
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			var e error
			events, e = tx.Events("", 0, 50)
			return e
		})).To(Succeed())
		changed := []domain.Event{}
		for _, event := range events {
			if event.Kind == "source.changed" {
				changed = append(changed, event)
			}
		}
		Expect(changed).NotTo(BeEmpty())
		for _, event := range changed {
			visible, e := f.auth.FilterEvent(context.Background(), f.token, event)
			Expect(e).NotTo(HaveOccurred())
			Expect(visible.Kind).To(Equal("source.changed"))
		}
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			return tx.TestTrashDocument(f.document)
		})).To(Succeed())
		for _, event := range changed {
			masked, e := f.auth.FilterEvent(context.Background(), f.token, event)
			Expect(e).NotTo(HaveOccurred())
			Expect(masked.Kind).To(Equal("library.invalidated"), "the owner no longer sees a trashed version's source event")
			Expect(masked.Body).To(BeNil())
		}
	})
})

var _ = Describe("verification read failures", Label("persistence"), func() {
	t := GinkgoT()

	// W3e (S12, G14): the verification reads through the same reader; an
	// unreadable checkout settles 503 source_read_failed with no recorded
	// outcome, never an unavailable verification row.
	It("settles a verification against an unreadable checkout without a recorded outcome", func() {
		if os.Geteuid() == 0 {
			Skip("permission bits do not bind the superuser")
		}
		f := newObservationFixture(t, "pinned")
		gitDir := filepath.Join(f.repoA.root, ".git")
		Expect(os.Chmod(gitDir, 0)).To(Succeed())
		DeferCleanup(func() { _ = os.Chmod(gitDir, 0o755) })
		key := "3a000000-0000-4000-8000-000000000021"
		first, e := f.core.Verify(context.Background(), f.token, f.version, "evA", key, []byte(`{}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(first.Status).To(Equal(503), string(first.Body))
		Expect(errorCode(t, first)).To(Equal("FILE_ACCESS_ERROR"))
		Expect(errorReason(t, first)).To(Equal("source_read_failed"))
		replay, e := f.core.Verify(context.Background(), f.token, f.version, "evA", key, []byte(`{}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(replay.Body).To(Equal(first.Body), "the settled failure replays")
		var rows int
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			history, e := tx.Verifications(f.version, "evA", "", "", 10)
			rows = len(history)
			return e
		})).To(Succeed())
		Expect(rows).To(Equal(0), "no verification row for an unreadable checkout")
		state := readObservationState(t, f, key)
		Expect(state.receipt).To(Equal("failed"))
		Expect(state.intent).To(Equal("failed"))
	})
})
