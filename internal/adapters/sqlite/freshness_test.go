package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/ai/codex"
	gitreader "haeryong.local/haeryong/internal/adapters/gitreader"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/scan"
	appsource "haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"os"
	"path/filepath"
	"time"
)

func freshnessTestPolicy(s *Store, project, mode string) {
	GinkgoHelper()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var v map[string]any
		if e = json.Unmarshal(cfg.Value, &v); e != nil {
			return e
		}
		maintenance := v["maintenance"].(map[string]any)
		maintenance["auto_apply"] = map[string]any{"freshness_assessment": mode, "related_explanation": "disabled", "topic_assignment": "disabled", "backlink_index_repair": "disabled"}
		maintenance["content_generation"] = "disabled"
		projects := v["projects"].(map[string]any)
		p, ok := projects[project].(map[string]any)
		if !ok {
			p = map[string]any{}
			projects[project] = p
		}
		p["provider_id"] = "codex"
		cfg.Value = jsonBytes(v)
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
}

var _ = DescribeTable("T-008-03 persists every captured version before freshness admission gates", Label("persistence"), func(gate, prior string) {
	s, auth, writer, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "fixed membership owner")
	f := &referenceWorld{s: s, writer: writer, bytes: &searchBytes{files: map[string]string{}}}
	project := mustUnfiledProject(s)
	root := filepath.Join(GinkgoT().TempDir(), "source")
	rev := initTestGitRepo(root, map[string]string{"retry.go": "package retry\nfunc Allowed() bool { return true }\n"})
	configureProjectSources(s, project, []map[string]any{{"source_id": "source", "kind": "git", "root_path": root, "repo_id": "source", "retention_mode": "selected_snapshot", "provider_ids": []string{"codex"}}})
	freshnessTestPolicy(s, project, "auto")
	versions := []string{}
	for i := 0; i < 2; i++ {
		versions = append(versions, publishFreshnessPages(f, project, map[string]string{"a.html": "<p>Allowed is true.</p>", "b.html": "<p>Allowed is true.</p>"}, map[string]map[string]any{"source": {"kind": "git", "repo_id": "source", "object_format": "sha1", "exact_revision": rev}}, map[string]map[string]any{"e": {"source_id": "source", "path": "retry.go", "capture_status": "reference_only"}}, "historical"))
	}
	bytes := testCaptureBytes{s: s, bytes: f.bytes}
	sources := appsource.New(s, auth, bytes, gitreader.New())
	for _, v := range versions {
		if prior == "observation" {
			_, e := sources.Observe(ctx, owner.Token, v, f.id(), []byte(`{}`))
			Expect(e).NotTo(HaveOccurred())
		} else if prior == "capture" {
			_, e := sources.Capture(ctx, owner.Token, f.id(), project, appsource.CaptureRequest{VersionID: v, SourceMode: "pinned", ProviderID: "codex", Selections: []appsource.CaptureSelection{{SourceID: "source", Revision: rev, Paths: []appsource.CapturePath{{Path: "retry.go"}}}}})
			Expect(e).NotTo(HaveOccurred())
		}
	}
	if gate == "budget" {
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			cfg, e := tx.Configuration()
			if e != nil {
				return e
			}
			var value map[string]any
			Expect(json.Unmarshal(cfg.Value, &value)).To(Succeed())
			value["maintenance"].(map[string]any)["max_provider_attempts_per_run"] = 1
			cfg.Value = jsonBytes(value)
			return tx.SetConfiguration(cfg, cfg.Revision)
		})).To(Succeed())
	}
	job := startScanJob(scan.New(s, auth, f.bytes, nil), owner.Token, domain.ScanRequest{ProjectID: project, Depth: "full_scope", SourceMode: "pinned"})
	var roster []domain.ScanCapturedVersion
	sets := map[string]string{}
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		roster, e = tx.ScanCapturedVersions(job, 0, 64)
		if e != nil {
			return e
		}
		Expect(roster).To(HaveLen(2))
		for _, cv := range roster {
			rows, e := tx.CoverageSetsByVersion(cv.VersionID, "")
			if e != nil {
				return e
			}
			for _, set := range rows {
				if set.Origin == "freshness:"+job {
					sets[cv.VersionID] = set.ID
					u, e := app.AllRequiredUnits(tx, set.ID)
					if e != nil {
						return e
					}
					Expect(u).To(HaveLen(2))
				}
			}
		}
		Expect(sets).To(HaveLen(2), "every version membership exists before first provider admission")
		return nil
	})).To(Succeed())
	w := scan.NewWorker(s, f.bytes)
	w.Sources, w.Identity = sources, codex.SemanticRequestSHA
	driver := &scriptedMaintenanceDriver{store: s}
	driver.responder = func(a domain.ProviderAttempt, inv domain.ProviderInvocation, inputs []domain.ProviderInput) (string, error) {
		var read domain.CapturedRead
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; read, e = tx.CapturedRead(inputs[0].ReadID); return e })).To(Succeed())
		origin, rid := "daemon_supplied", read.ID
		return string(jsonBytes(domain.FreshnessResult{Units: []domain.FreshnessOutput{{Kind: "page", PagePath: inv.Maintenance.PagePath, Outcome: "assessed", Rationale: "Offline scripted page comparison", Citations: []domain.QuestionCitation{{Basis: "observed_input", ReadID: &rid, InputOrigin: &origin, TargetSetID: read.TargetSetID, Source: read.SourceJSON, Location: read.LocationJSON, Hash: read.BytesSHA}}}}})), nil
	}
	coordinator := &work.ProviderCoordinator{Store: s, Bytes: bytes, Driver: driver, MaintenanceSource: func(c context.Context, id string) error {
		return sources.CaptureMaintenanceAndAdmit(c, id, codex.SemanticRequestSHA)
	}, Prepare: w.PrepareMaintenance, Apply: w.ApplyMaintenance}
	step := func() {
		Expect(w.Process(ctx, job)).To(Succeed())
		var intents []domain.MaintenanceIntent
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; intents, e = tx.MaintenanceIntentsByScan(job); return e })).To(Succeed())
		for _, m := range intents {
			child, e := s.Job(ctx, m.JobID)
			Expect(e).NotTo(HaveOccurred())
			if child.State == "queued" || child.State == "running" || child.State == "paused" && child.Error == "source_capture_pending" {
				Expect(coordinator.Process(ctx, child.ID)).To(Succeed())
			}
		}
	}
	for turn := 0; turn < 96 && driver.executeCalls == 0; turn++ {
		step()
	}
	Expect(driver.executeCalls).To(Equal(1))
	if gate == "provider" {
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			cfg, e := tx.Configuration()
			if e != nil {
				return e
			}
			var value map[string]any
			Expect(json.Unmarshal(cfg.Value, &value)).To(Succeed())
			value["projects"].(map[string]any)[project].(map[string]any)["provider_id"] = nil
			cfg.Value = jsonBytes(value)
			return tx.SetConfiguration(cfg, cfg.Revision)
		})).To(Succeed())
	} else if gate == "fence" {
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.TestTrashDocument(roster[1].DocumentID) })).To(Succeed())
	}
	for turn := 0; turn < 64; turn++ {
		step()
		j, e := s.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		if j.State == "completed" {
			break
		}
	}
	j, e := s.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	var report domain.ScanReport
	Expect(json.Unmarshal(j.Result, &report)).To(Succeed())
	Expect(report.Semantic.Eligible).To(Equal(4))
	Expect(report.Semantic.Complete).To(BeFalse())
	expectedCalls := 1
	if gate == "fence" {
		expectedCalls = 2
	}
	Expect(driver.executeCalls).To(Equal(expectedCalls))
	queries := query.New(s, auth, f.bytes)
	for _, cv := range roster {
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			if m, ok, e := tx.FreshnessIntent(job, cv.VersionID); e != nil {
				return e
			} else if ok {
				sets[cv.VersionID] = m.FullCoverageSetID
			}
			rows, e := app.AllRequiredUnits(tx, sets[cv.VersionID])
			Expect(rows).To(HaveLen(2))
			if gate == "fence" && cv.VersionID == roster[1].VersionID {
				for _, unit := range rows {
					Expect(unit.Reason).To(Equal("document_trashed"))
				}
			}
			return e
		})).To(Succeed())
		view, e := queries.Assessments(ctx, owner.Token, cv.VersionID, query.AssessmentQuery{})
		if gate == "fence" && cv.VersionID == roster[1].VersionID {
			Expect(e).To(HaveOccurred())
			continue
		}
		Expect(e).NotTo(HaveOccurred())
		applicable := view["applicable"].(map[string]any)
		coverage := applicable["coverage"].(map[string]any)
		Expect(coverage["units_url"]).NotTo(BeNil())
		if prior == "" && cv.VersionID == roster[1].VersionID {
			Expect(applicable["target_set_id"]).To(BeNil())
			Expect(applicable["captured_at"]).To(BeNil())
			Expect(applicable["assessed_at"]).To(BeNil())
		}
		var listed []map[string]any
		cursor := ""
		for {
			page, e := queries.AssessmentCoverage(ctx, owner.Token, cv.VersionID, sets[cv.VersionID], "", query.Options{Limit: 1, Cursor: cursor})
			Expect(e).NotTo(HaveOccurred())
			listed = append(listed, page.Items...)
			if page.NextCursor == nil {
				break
			}
			cursor = *page.NextCursor
		}
		Expect(listed).To(HaveLen(2))
		for _, row := range listed {
			if row["outcome"] == "not_checked" {
				reason := "provider_unavailable"
				if gate == "budget" {
					reason = "provider_attempt_budget_exhausted"
				}
				Expect(row["reason"]).To(HaveValue(Equal(reason)))
			}
		}
	}
}, Entry("attempt budget with no earlier observation", "budget", ""), Entry("provider removed after one unit, prior observation retained", "provider", "observation"), Entry("later version lifecycle fence with prior capture", "fence", "capture"))

var _ = DescribeTable("T-008-03 preserves observation composition across unattempted freshness membership", Label("persistence"), func(order string) {
	s, auth, writer, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "observation preservation owner")
	f := &referenceWorld{s: s, writer: writer, bytes: &searchBytes{files: map[string]string{}}}
	project := mustUnfiledProject(s)
	root := filepath.Join(GinkgoT().TempDir(), "source")
	rev := initTestGitRepo(root, map[string]string{"retry.go": "package retry\nfunc Allowed() bool { return true }\n"})
	configureProjectSources(s, project, []map[string]any{{"source_id": "source", "kind": "git", "root_path": root, "repo_id": "source", "retention_mode": "selected_snapshot", "provider_ids": []string{"codex"}}})
	freshnessTestPolicy(s, project, "auto")
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var value map[string]any
		Expect(json.Unmarshal(cfg.Value, &value)).To(Succeed())
		value["projects"].(map[string]any)[project].(map[string]any)["source_mode_default"] = "current"
		cfg.Value = jsonBytes(value)
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
	version := publishFreshnessPages(f, project, map[string]string{"a.html": "<p>Allowed is true.</p>", "b.html": "<p>Allowed is true.</p>"}, map[string]map[string]any{"source": {"kind": "git", "repo_id": "source", "object_format": "sha1", "exact_revision": rev}}, map[string]map[string]any{"e": {"source_id": "source", "path": "retry.go", "capture_status": "reference_only"}}, "moving_target")
	sources := appsource.New(s, auth, testCaptureBytes{s: s, bytes: f.bytes}, gitreader.New())
	queries := query.New(s, auth, f.bytes)
	observe := func() map[string]any {
		response, e := sources.Observe(ctx, owner.Token, version, f.id(), []byte(`{}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(201))
		var result map[string]any
		Expect(json.Unmarshal(response.Body, &result)).To(Succeed())
		return result
	}
	applicable := func(q query.AssessmentQuery) map[string]any {
		view, e := queries.Assessments(ctx, owner.Token, version, q)
		Expect(e).NotTo(HaveOccurred())
		return view["applicable"].(map[string]any)
	}
	var expected, captured map[string]any
	if order == "before" || order == "blocked" {
		_ = commitTestGitFile(root, "retry.go", "package retry\nfunc Allowed() bool { return false }\n", "changed cited behavior")
		captured = observe()
		expected = applicable(query.AssessmentQuery{})
		Expect(expected["target_set_id"]).To(Equal(captured["target_set_id"]))
		Expect(expected["status"]).To(Equal("reassessment_needed"))
		Expect(expected["reassessment_required"]).To(BeTrue())
	}
	job := startScanJob(scan.New(s, auth, f.bytes, nil), owner.Token, domain.ScanRequest{VersionID: version, Depth: "full_scope", SourceMode: "current"})
	if order == "after" {
		_ = commitTestGitFile(root, "retry.go", "package retry\nfunc Allowed() bool { return false }\n", "newer observation than scan membership")
		captured = observe()
		// The explicit real observation is the expected C, independently of
		// the default composition being exercised by this regression.
		expected = applicable(query.AssessmentQuery{TargetSetID: captured["target_set_id"].(string)})
		Expect(expected["status"]).To(Equal("reassessment_needed"))
		Expect(expected["reassessment_required"]).To(BeTrue())
	}
	if order == "blocked" {
		Expect(os.Rename(root, root+"-unavailable")).To(Succeed())
		attempt := observe()
		Expect(os.Rename(root+"-unavailable", root)).To(Succeed())
		Expect(attempt["target_set_id"]).To(BeNil())
		expected = applicable(query.AssessmentQuery{})
		Expect(expected["target_set_id"]).To(Equal(captured["target_set_id"]))
		Expect(expected["status"]).To(Equal("unable_to_verify"))
		Expect(expected["reassessment_required"]).To(BeTrue())
		Expect(expected["coverage"].(map[string]any)["units_url"]).To(ContainSubstring("attempt_run_id=" + attempt["run_id"].(string)))
	}
	var triggers []domain.ReassessmentTrigger
	if captured != nil {
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			var e error
			triggers, e = tx.TriggersByTargetSet(version, captured["target_set_id"].(string))
			return e
		})).To(Succeed())
		Expect(triggers).NotTo(BeEmpty())
	}
	// No provider seam is supplied: the production worker records a real
	// provider-unavailable stop, without fabricating a child or assessment.
	w := scan.NewWorker(s, f.bytes)
	for turn := 0; turn < 64; turn++ {
		Expect(w.Process(ctx, job)).To(Succeed())
		j, e := s.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		if j.State == "completed" {
			break
		}
	}
	j, e := s.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	var report domain.ScanReport
	Expect(json.Unmarshal(j.Result, &report)).To(Succeed())
	Expect(report.Semantic.Eligible).To(Equal(2))
	Expect(report.Semantic.Assessed).To(BeZero())
	Expect(report.Semantic.Complete).To(BeFalse())
	var membership string
	var cv domain.ScanCapturedVersion
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		cv, e = tx.ScanCapturedVersionByID(job, version)
		if e != nil {
			return e
		}
		sets, e := tx.CoverageSetsByVersion(version, "")
		for _, set := range sets {
			if set.Origin == "freshness:"+job {
				membership = set.ID
				Expect(set.TargetSetID).To(Equal(cv.TargetSetID))
			}
		}
		intents, err := tx.MaintenanceIntentsByScan(job)
		Expect(err).NotTo(HaveOccurred())
		Expect(intents).To(BeEmpty())
		if captured != nil {
			after, err := tx.TriggersByTargetSet(version, captured["target_set_id"].(string))
			Expect(err).NotTo(HaveOccurred())
			Expect(after).To(Equal(triggers), "unattempted work never clears or transfers C-bound triggers")
		}
		return e
	})).To(Succeed())
	Expect(membership).NotTo(BeEmpty())
	first, e := queries.AssessmentCoverage(ctx, owner.Token, version, membership, "", query.Options{Limit: 1})
	Expect(e).NotTo(HaveOccurred())
	Expect(first.Items).To(HaveLen(1))
	Expect(first.NextCursor).NotTo(BeNil())
	second, e := queries.AssessmentCoverage(ctx, owner.Token, version, membership, "", query.Options{Limit: 1, Cursor: *first.NextCursor})
	Expect(e).NotTo(HaveOccurred())
	Expect(second.Items).To(HaveLen(1))
	for _, row := range append(first.Items, second.Items...) {
		Expect(row["outcome"]).To(Equal("not_checked"))
		Expect(row["reason"]).To(HaveValue(Equal("provider_unavailable")))
		Expect(row["target_set_id"]).To(BeNil())
	}
	actual := applicable(query.AssessmentQuery{})
	if expected != nil {
		Expect(actual).To(Equal(expected), "uncaptured membership cannot replace the real C/A target, time, trigger, status or full locator")
		document, e := queries.Document(ctx, owner.Token, cv.DocumentID)
		Expect(e).NotTo(HaveOccurred())
		Expect(document.Freshness).To(Equal(expected), "Library summary preserves the same observation composition")
	} else {
		Expect(actual["target_set_id"]).To(BeNil())
		Expect(actual["captured_at"]).To(BeNil())
		Expect(actual["assessed_at"]).To(BeNil())
		Expect(actual["status"]).To(Equal("not_assessed"))
		coverage := actual["coverage"].(map[string]any)
		Expect(coverage["coverage_set_id"]).To(Equal(membership))
		Expect(coverage["eligible"]).To(Equal(2))
		Expect(coverage["complete"]).To(BeFalse())
		Expect(coverage["units_url"]).To(ContainSubstring("coverage_set_id=" + membership))
	}
}, Entry("preserves an existing current observation", "before"), Entry("an older scan cannot replace a newer observation", "after"), Entry("retains the latest blocked attempt and earlier C locator", "blocked"), Entry("keeps genuine no-observation membership discoverable", "none"))

var _ = It("T-008-03 orders published contributions and trigger cutoffs by the full timestamp instant", Label("persistence"), func() {
	s, _, writer, _ := authFixture(GinkgoT())
	f := &referenceWorld{s: s, writer: writer, bytes: &searchBytes{files: map[string]string{}}}
	v := f.publish(nil, "<p>saved</p>")
	project := mustUnfiledProject(s)
	target, run := f.id(), f.id()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.PutTargetSet(domain.TargetSet{ID: target, ProjectID: project, RequestedSelection: []byte(`{"mode":"pinned"}`), TargetsJSON: []byte(`[]`), CapturedAt: "2026-11-01T00:00:00Z"}); e != nil {
			return e
		}
		if e := tx.PutObservationRun(domain.ObservationRun{ID: run, VersionID: v, PrincipalID: "owner", OperationID: f.id(), RequestedSelection: []byte(`{}`), ScopeJSON: []byte(`{}`), TargetSetID: target, CreatedAt: "2026-11-01T00:00:00Z"}); e != nil {
			return e
		}
		for _, at := range []string{"2026-11-01T00:00:00.1Z", "2026-11-01T00:00:00.110000002Z"} {
			if e := tx.PutReassessmentTrigger(domain.ReassessmentTrigger{ID: f.id(), RunID: run, VersionID: v, TargetSetID: target, SourceID: "source", AnchorKind: "page", PagePath: "page.html", ObservationID: "observation", ObservedAt: at, CreatedAt: at}); e != nil {
				return e
			}
		}
		var newest domain.Assessment
		for i, at := range []string{"2026-11-01T00:00:00.1Z", "2026-11-01T00:00:00.11Z", "2026-11-01T00:00:00.110000001Z"} {
			cid, aid := f.id(), f.id()
			scope := []byte(`{"kind":"page","page_path":"page.html","region_id":null}`)
			if e := tx.PutCoverageSet(domain.CoverageSet{ID: cid, VersionID: v, TargetSetID: target, ScopeJSON: scope, UnitKind: "page", CapturedAt: at, Origin: "semantic"}); e != nil {
				return e
			}
			if e := tx.PutRequiredUnit(domain.RequiredUnit{CoverageSetID: cid, TargetSetID: target, Kind: "page", PagePath: "page.html"}); e != nil {
				return e
			}
			newest = domain.Assessment{ID: aid, VersionID: v, TargetSetID: target, CoverageSetID: cid, ScopeJSON: scope, State: "aligned_within_scope", EvidenceJSON: []byte(`{}`), ConfigRevision: 1, PermissionsRevision: 1, Origin: "ai", AssessedAt: at}
			u := domain.AssessmentUnit{CoverageSetID: cid, AssessmentID: aid, TargetSetID: target, Kind: "page", PagePath: "page.html", Outcome: "assessed"}
			if i == 0 {
				u.Discrepancy = "older discrepancy"
			}
			if e := tx.PutAssessment(newest, []domain.AssessmentUnit{u}); e != nil {
				return e
			}
		}
		u, ok, e := tx.LatestAssessmentUnit(v, target, domain.RequiredUnit{Kind: "page", PagePath: "page.html"})
		Expect(e).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(u.AssessmentID).To(Equal(newest.ID))
		Expect(u.Discrepancy).To(BeEmpty())
		afterTime, afterID := "", ""
		for _, expected := range []string{"2026-11-01T00:00:00.1Z", "2026-11-01T00:00:00.11Z", "2026-11-01T00:00:00.110000001Z"} {
			rows, e := tx.AssessmentsByVersion(v, afterTime, afterID, 1)
			Expect(e).NotTo(HaveOccurred())
			Expect(rows).To(HaveLen(1))
			Expect(rows[0].AssessedAt).To(Equal(expected))
			afterTime, afterID = rows[0].AssessedAt, rows[0].ID
		}
		if e := tx.ClearAssessmentTriggers(newest); e != nil {
			return e
		}
		remaining, e := tx.TriggersByTargetSet(v, target)
		Expect(remaining).To(HaveLen(1))
		Expect(remaining[0].CreatedAt).To(Equal("2026-11-01T00:00:00.110000002Z"))
		return e
	})).To(Succeed())
})

var _ = DescribeTable("T-008-03 explicit completed capture context stays exact and implicit selection stays cited-file bounded", func(explicit, revoke bool) {
	s, auth, writer, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "explicit context owner")
	f := &referenceWorld{s: s, writer: writer, bytes: &searchBytes{files: map[string]string{}}}
	project := mustUnfiledProject(s)
	root := filepath.Join(GinkgoT().TempDir(), "source")
	bodies := map[string]string{"cited.go": "package example\nfunc Callee() bool { return true }\n", "caller.go": "package example\nfunc Caller() bool { return Callee() }\n"}
	rev := initTestGitRepo(root, bodies)
	mapping := map[string]any{"source_id": "source", "kind": "git", "root_path": root, "repo_id": "source", "retention_mode": "selected_snapshot", "provider_ids": []string{"codex"}}
	configureProjectSources(s, project, []map[string]any{mapping})
	freshnessTestPolicy(s, project, "auto")
	publish := func() string {
		return publishInProjectWithSourcesAndEvidence(f, project, map[string]map[string]any{"e": {"source_id": "authored-source", "path": "cited.go", "line_start": 2, "line_end": 2, "capture_status": "reference_only"}}, "<p id='claim'>The caller propagates the callee result.</p>", map[string]map[string]any{"authored-source": {"kind": "git", "repo_id": "source", "object_format": "sha1", "exact_revision": rev}}, map[string]any{"id": "claim", "page_path": "page.html", "evidence_ids": []string{"e"}})
	}
	version := publish()
	bytes := testCaptureBytes{s: s, bytes: f.bytes}
	sources := appsource.New(s, auth, bytes, gitreader.New())
	capture := func(paths []appsource.CapturePath) domain.SourceCapture {
		response, e := sources.Capture(ctx, owner.Token, f.id(), project, appsource.CaptureRequest{VersionID: version, SourceMode: "pinned", ProviderID: "codex", Selections: []appsource.CaptureSelection{{SourceID: "source", Revision: rev, Paths: paths}}})
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(201))
		var receipt struct {
			CaptureID string `json:"capture_id"`
		}
		Expect(json.Unmarshal(response.Body, &receipt)).To(Succeed())
		var saved domain.SourceCapture
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; saved, e = tx.SourceCapture(receipt.CaptureID); return e })).To(Succeed())
		return saved
	}
	selected := capture([]appsource.CapturePath{{Path: "cited.go"}, {Path: "caller.go"}})
	Expect(selected.State).To(Equal("completed"))
	scanService := scan.New(s, auth, f.bytes, nil)
	if explicit && !revoke {
		// Public capture/admission guards reject other versions, source/revision
		// constraints, and genuinely failed captures before any dispatch.
		otherVersion := publish()
		invalid := []domain.ScanRequest{
			{VersionID: otherVersion, TargetSetID: selected.TargetSetID},
			{VersionID: version, TargetSetID: selected.TargetSetID, Sources: []domain.ScanSourceSelector{{ProjectID: project, SourceID: "other", Mode: "pinned"}}},
			{VersionID: version, TargetSetID: selected.TargetSetID, Sources: []domain.ScanSourceSelector{{ProjectID: project, SourceID: "source", Mode: "pinned", Revision: "0000000000000000000000000000000000000000"}}},
		}
		failed := capture([]appsource.CapturePath{{Path: "missing.go"}})
		Expect(failed.State).NotTo(Equal("completed"))
		invalid = append(invalid, domain.ScanRequest{VersionID: version, TargetSetID: failed.TargetSetID})
		for _, req := range invalid {
			_, e := scanService.StartScan(ctx, owner.Token, f.id(), nil, req)
			Expect(e).To(MatchError(domain.ErrInvalidTarget))
		}
	}
	req := domain.ScanRequest{VersionID: version, Depth: "full_scope", SourceMode: "pinned"}
	if explicit {
		req.TargetSetID = selected.TargetSetID
	}
	job := startScanJob(scanService, owner.Token, req)
	if revoke {
		mapping["provider_ids"] = []string{}
		configureProjectSources(s, project, []map[string]any{mapping})
	}
	w := scan.NewWorker(s, f.bytes)
	w.Sources, w.Identity = sources, codex.SemanticRequestSHA
	driver := &scriptedMaintenanceDriver{store: s}
	driver.responder = func(a domain.ProviderAttempt, inv domain.ProviderInvocation, inputs []domain.ProviderInput) (string, error) {
		Expect(revoke).To(BeFalse(), "lost current authority cannot disclose the retained context")
		count := 1
		if explicit {
			count = 2
		}
		Expect(inputs).To(HaveLen(count))
		var citations []domain.QuestionCitation
		seen := map[string]bool{}
		for _, input := range inputs {
			var read domain.CapturedRead
			Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; read, e = tx.CapturedRead(input.ReadID); return e })).To(Succeed())
			var location struct {
				Path string `json:"path"`
			}
			var identity struct {
				Revision string `json:"exact_revision"`
			}
			Expect(json.Unmarshal(read.LocationJSON, &location)).To(Succeed())
			Expect(json.Unmarshal(read.SourceJSON, &identity)).To(Succeed())
			Expect(identity.Revision).To(Equal(rev))
			Expect(f.bytes.files[input.Hash]).To(Equal(bodies[location.Path]))
			if explicit {
				Expect(read.TargetSetID).To(Equal(selected.TargetSetID))
			}
			seen[location.Path] = true
			rid, origin := read.ID, "daemon_supplied"
			citations = append(citations, domain.QuestionCitation{Basis: "observed_input", ReadID: &rid, InputOrigin: &origin, TargetSetID: read.TargetSetID, Source: read.SourceJSON, Location: read.LocationJSON, Hash: read.BytesSHA})
		}
		Expect(seen["cited.go"]).To(BeTrue())
		Expect(seen["caller.go"]).To(Equal(explicit))
		var units []domain.RequiredUnit
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			var e error
			units, e = tx.RequiredUnits(inv.Maintenance.CoverageSetID, "", "", "", 64)
			return e
		})).To(Succeed())
		Expect(units).To(HaveLen(1), "additional source context never adds explanation units")
		page := "page.html"
		return string(jsonBytes(domain.FreshnessResult{Units: []domain.FreshnessOutput{{Kind: "page", PagePath: &page, Outcome: "assessed", Rationale: "Offline selector fixture; source bytes and target identity are asserted independently of this scripted verdict", Citations: citations}}})), nil
	}
	coordinator := &work.ProviderCoordinator{Store: s, Bytes: bytes, Driver: driver, MaintenanceSource: func(c context.Context, id string) error {
		return sources.CaptureMaintenanceAndAdmit(c, id, codex.SemanticRequestSHA)
	}, Prepare: w.PrepareMaintenance, Apply: w.ApplyMaintenance}
	for turn := 0; turn < 80; turn++ {
		Expect(w.Process(ctx, job)).To(Succeed())
		var intents []domain.MaintenanceIntent
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; intents, e = tx.MaintenanceIntentsByScan(job); return e })).To(Succeed())
		for _, intent := range intents {
			child, e := s.Job(ctx, intent.JobID)
			Expect(e).NotTo(HaveOccurred())
			if child.State == "queued" || child.State == "paused" && child.Error == "source_capture_pending" {
				Expect(coordinator.Process(ctx, child.ID)).To(Succeed())
			}
		}
		j, e := s.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		if j.State == "completed" {
			break
		}
	}
	if revoke {
		Expect(driver.executeCalls).To(BeZero())
	} else {
		Expect(driver.executeCalls).To(Equal(1))
	}
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		evidence, e := tx.QueryEvidence(version, "e")
		if e != nil {
			return e
		}
		var authored map[string]any
		Expect(json.Unmarshal(evidence.Metadata, &authored)).To(Succeed())
		Expect(authored["line_start"]).To(Equal(float64(2)))
		Expect(authored["line_end"]).To(Equal(float64(2)))
		return nil
	})).To(Succeed())
}, Entry("explicit full context", true, false), Entry("implicit cited files only despite ambient completed capture", false, false), Entry("current provider authority removed", true, true))

var _ = It("T-008-03 freshness-only guarded capture, complete-file provenance, auto history and propose nonapplication", Label("persistence"), func() {
	s, auth, writer, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "freshness owner")
	f := &referenceWorld{s: s, writer: writer, bytes: &searchBytes{files: map[string]string{}}}
	project := mustUnfiledProject(s)
	root := filepath.Join(GinkgoT().TempDir(), "source")
	sourceText := "package retry\nfunc RetryAllowed() bool { return false }\n// caller context remains available outside the cited span\n"
	rev := initTestGitRepo(root, map[string]string{"retry.go": sourceText})
	configureProjectSources(s, project, []map[string]any{{"source_id": "source", "kind": "git", "root_path": root, "repo_id": "source", "retention_mode": "reference_only", "provider_ids": []string{"codex"}}})
	freshnessTestPolicy(s, project, "auto")
	version := publishInProjectWithSourcesAndEvidence(f, project, map[string]map[string]any{"e": {"source_id": "authored-source", "path": "retry.go", "line_start": 2, "line_end": 2, "capture_status": "reference_only"}}, "<html><body><p id='retry'>Retry is allowed.</p></body></html>", map[string]map[string]any{"authored-source": {"kind": "git", "repo_id": "source", "object_format": "sha1", "exact_revision": rev}}, map[string]any{"id": "retry", "page_path": "page.html", "evidence_ids": []string{"e"}})
	bytes := testCaptureBytes{s: s, bytes: f.bytes}
	sources := appsource.New(s, auth, bytes, gitreader.New())
	w := scan.NewWorker(s, f.bytes)
	w.Sources = sources
	w.Identity = codex.SemanticRequestSHA
	driver := &scriptedMaintenanceDriver{store: s}
	outcome, discrepancy := "assessed", "The captured function refuses retry"
	driver.responder = func(a domain.ProviderAttempt, inv domain.ProviderInvocation, observed []domain.ProviderInput) (string, error) {
		Expect(inv.Maintenance.Mode).To(Equal(domain.MaintenanceModeFreshness))
		Expect(observed).To(HaveLen(1))
		Expect(f.bytes.files[observed[0].Hash]).To(Equal(sourceText))
		var read domain.CapturedRead
		var units []domain.RequiredUnit
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			var e error
			read, e = tx.CapturedRead(observed[0].ReadID)
			if e != nil {
				return e
			}
			units, e = tx.RequiredUnits(inv.Maintenance.CoverageSetID, "", "", "", 64)
			return e
		})).To(Succeed())
		Expect(units).To(HaveLen(1))
		origin := "daemon_supplied"
		rid := read.ID
		page := "page.html"
		reason := ""
		if outcome == "partial" {
			reason = "The retry discrepancy is supported; another claim remains unexamined"
		}
		result := domain.FreshnessResult{Units: []domain.FreshnessOutput{{Kind: "page", PagePath: &page, Outcome: outcome, Discrepancy: discrepancy, Reason: reason, Rationale: "Offline scripted comparison over the supplied page and exact complete source", Citations: []domain.QuestionCitation{{Basis: "observed_input", ReadID: &rid, InputOrigin: &origin, TargetSetID: read.TargetSetID, Source: read.SourceJSON, Location: read.LocationJSON, Hash: read.BytesSHA}}}}}
		return string(jsonBytes(result)), nil
	}
	coordinator := &work.ProviderCoordinator{Store: s, Bytes: bytes, Driver: driver, MaintenanceSource: func(c context.Context, id string) error {
		return sources.CaptureMaintenanceAndAdmit(c, id, codex.SemanticRequestSHA)
	}, Prepare: w.PrepareMaintenance, Apply: w.ApplyMaintenance}
	run := func() string {
		job := startScanJob(scan.New(s, auth, f.bytes, nil), owner.Token, domain.ScanRequest{VersionID: version, Depth: "full_scope", SourceMode: "pinned"})
		c, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		done := make(chan error, 1)
		dispatcher := &work.Dispatcher{Store: s, Scans: w, Provider: coordinator, Derived: work.NewMaintenance(s, f.bytes)}
		go func() { done <- dispatcher.Run(c) }()
		Eventually(func() string {
			j, e := s.Job(ctx, job)
			if e != nil {
				return e.Error()
			}
			return j.State
		}, 110*time.Second, 10*time.Millisecond).Should(Equal("completed"))
		cancel()
		Expect(<-done).To(Succeed())
		return job
	}
	job := run()
	Expect(driver.executeCalls).To(Equal(1))
	var intent domain.MaintenanceIntent
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		m, ok, e := tx.FreshnessIntent(job, version)
		Expect(ok).To(BeTrue())
		intent = m
		return e
	})).To(Succeed())
	setID := intent.CoverageSetID
	var set domain.CoverageSet
	Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; set, e = tx.CoverageSetByID(setID); return e })).To(Succeed())
	queries := query.New(s, auth, f.bytes)
	view, e := queries.Assessments(ctx, owner.Token, version, query.AssessmentQuery{TargetSetID: set.TargetSetID})
	Expect(e).NotTo(HaveOccurred())
	Expect(view["items"]).To(HaveLen(1))
	applicable := view["applicable"].(map[string]any)
	Expect(applicable["status"]).To(Equal("outdated_against_target"))
	Expect(applicable["captured_at"]).To(Equal(set.CapturedAt))
	coverage, e := queries.AssessmentCoverage(ctx, owner.Token, version, setID, "", query.Options{})
	Expect(e).NotTo(HaveOccurred())
	Expect(coverage.Items).To(HaveLen(1))
	Expect(coverage.Items[0]["outcome"]).To(Equal("assessed"))
	j, e := s.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	var report domain.ScanReport
	Expect(json.Unmarshal(j.Result, &report)).To(Succeed())
	Expect(report.Semantic.Assessed).To(Equal(1))
	Expect(report.Semantic.Complete).To(BeTrue())
	Expect(report.OutdatedUnits).To(Equal(1))
	// Report refresh is a projection of exact validated work, not an increment.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		r, e := tx.ScanRun(job)
		if e != nil {
			return e
		}
		if e = tx.UpdateScanRun(r); e != nil {
			return e
		}
		return tx.UpdateScanRun(r)
	})).To(Succeed())
	showReport := func(job string) domain.ScanReport {
		j, e := s.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		var r domain.ScanReport
		Expect(json.Unmarshal(j.Result, &r)).To(Succeed())
		return r
	}
	Expect(showReport(job).OutdatedUnits).To(Equal(1))
	freshnessTestPolicy(s, project, "propose")
	proposed := run()
	Expect(driver.executeCalls).To(Equal(2))
	Expect(showReport(proposed).OutdatedUnits).To(Equal(1))
	history, e := queries.Assessments(ctx, owner.Token, version, query.AssessmentQuery{TargetSetID: set.TargetSetID})
	Expect(e).NotTo(HaveOccurred())
	Expect(history["items"]).To(HaveLen(1))
	// T00805 publishes the retained proposed contribution without Execute/capture.
	core := scan.New(s, auth, f.bytes, nil)
	saved, e := core.ListJobFindings(ctx, owner.Token, domain.FindingFilter{JobID: proposed, Kind: "freshness_assessment", Status: "proposed"})
	Expect(e).NotTo(HaveOccurred())
	Expect(saved.Items).To(HaveLen(1))
	approvalKey := f.id()
	accepted, e := core.FindingAction(ctx, owner.Token, approvalKey, saved.Items[0].ID, saved.Items[0].ETag, "approve", []byte(`{"decision":"approve"}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(accepted.Status).To(Equal(200))
	Expect(driver.executeCalls).To(Equal(2))
	Expect(actionResult(accepted).Finding.Status).To(Equal("applied"))
	replay, e := core.FindingAction(ctx, owner.Token, approvalKey, saved.Items[0].ID, saved.Items[0].ETag, "approve", []byte(`{"decision":"approve"}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(replay.Body).To(Equal(accepted.Body))
	Expect(driver.executeCalls).To(Equal(2))
	history, e = queries.Assessments(ctx, owner.Token, version, query.AssessmentQuery{TargetSetID: set.TargetSetID})
	Expect(e).NotTo(HaveOccurred())
	Expect(history["items"]).To(HaveLen(1)) // older exact target remains separate
	var published int
	Expect(s.db.QueryRow("SELECT count(*) FROM assessments WHERE version_id=?", version).Scan(&published)).To(Succeed())
	Expect(published).To(Equal(2))
	outcome = "partial"
	partial := showReport(run())
	Expect(partial.OutdatedUnits).To(Equal(1))
	Expect(partial.Semantic.Assessed).To(BeZero())
	Expect(partial.Semantic.Complete).To(BeFalse())
	outcome, discrepancy = "assessed", ""
	aligned := showReport(run())
	Expect(aligned.OutdatedUnits).To(BeZero())
	Expect(aligned.Semantic.Complete).To(BeTrue())
})

var _ = It("T-008-03 fixed tuple contributions do not sum regions or let older results replace newer page scope", Label("persistence"), func() {
	s, _, writer, _ := authFixture(GinkgoT())
	f := &referenceWorld{s: s, writer: writer, bytes: &searchBytes{files: map[string]string{}}}
	v := f.publish(nil, "<p>saved</p>")
	project := mustUnfiledProject(s)
	target := f.id()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.PutTargetSet(domain.TargetSet{ID: target, ProjectID: project, RequestedSelection: jsonBytes(map[string]any{"mode": "pinned"}), TargetsJSON: []byte("[]"), CapturedAt: "2026-11-01T00:00:00Z"}); e != nil {
			return e
		}
		for _, x := range []struct{ kind, page, region, time, outcome, discrepancy string }{{"page", "page.html", "", "2026-11-02T00:00:00Z", "assessed", ""}, {"page", "page.html", "", "2026-11-01T01:00:00Z", "partial", "old"}, {"region", "page.html", "same", "2026-11-03T00:00:00Z", "assessed", "region only"}, {"region", "other.html", "same", "2026-11-04T00:00:00Z", "assessed", "foreign page"}} {
			cid, aid := f.id(), f.id()
			scope := map[string]any{"kind": x.kind, "page_path": x.page, "region_id": nil}
			if x.region != "" {
				scope["region_id"] = x.region
			}
			set := domain.CoverageSet{ID: cid, VersionID: v, TargetSetID: target, ScopeJSON: jsonBytes(scope), UnitKind: x.kind, CapturedAt: "2026-11-01T00:00:00Z", Origin: "semantic"}
			if e := tx.PutCoverageSet(set); e != nil {
				return e
			}
			if e := tx.PutRequiredUnit(domain.RequiredUnit{CoverageSetID: cid, Kind: x.kind, PagePath: x.page, RegionID: x.region, TargetSetID: target}); e != nil {
				return e
			}
			a := domain.Assessment{ID: aid, VersionID: v, TargetSetID: target, CoverageSetID: cid, ScopeJSON: set.ScopeJSON, State: "not_assessed", EvidenceJSON: []byte("{}"), ConfigRevision: 1, PermissionsRevision: 1, Origin: "ai", AssessedAt: x.time}
			if e := tx.PutAssessment(a, []domain.AssessmentUnit{{CoverageSetID: cid, AssessmentID: aid, TargetSetID: target, Kind: x.kind, PagePath: x.page, RegionID: x.region, Outcome: x.outcome, Discrepancy: x.discrepancy, Reason: "scoped test"}}); e != nil {
				return e
			}
		}
		u, ok, e := tx.LatestAssessmentUnit(v, target, domain.RequiredUnit{Kind: "page", PagePath: "page.html"})
		Expect(ok).To(BeTrue())
		Expect(u.Outcome).To(Equal("assessed"))
		Expect(u.Discrepancy).To(BeEmpty())
		return e
	})).To(Succeed())
})

var _ = It("T-008-03 retains every required tuple beyond storage batching and refuses a truncated contribution", Label("persistence"), func() {
	s, _, writer, _ := authFixture(GinkgoT())
	f := &referenceWorld{s: s, writer: writer, bytes: &searchBytes{files: map[string]string{}}}
	v := f.publish(nil, "<p>saved</p>")
	target, cid, aid := f.id(), f.id(), f.id()
	project := mustUnfiledProject(s)
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.PutTargetSet(domain.TargetSet{ID: target, ProjectID: project, RequestedSelection: []byte(`{"mode":"pinned"}`), TargetsJSON: []byte(`[]`), CapturedAt: "2026-11-01T00:00:00Z"}); e != nil {
			return e
		}
		scope := []byte(`{"kind":"version"}`)
		if e := tx.PutCoverageSet(domain.CoverageSet{ID: cid, VersionID: v, TargetSetID: target, ScopeJSON: scope, UnitKind: "page", CapturedAt: "2026-11-01T00:00:00Z", Origin: "semantic"}); e != nil {
			return e
		}
		for i := 0; i < 300; i++ {
			if e := tx.PutRequiredUnit(domain.RequiredUnit{CoverageSetID: cid, TargetSetID: target, Kind: "page", PagePath: fmt.Sprintf("page-%03d.html", i)}); e != nil {
				return e
			}
		}
		rows, e := app.AllRequiredUnits(tx, cid)
		if e != nil {
			return e
		}
		Expect(rows).To(HaveLen(300))
		return nil
	})).To(Succeed())
	var units []domain.AssessmentUnit
	for i := 0; i < 300; i++ {
		units = append(units, domain.AssessmentUnit{CoverageSetID: cid, AssessmentID: aid, TargetSetID: target, Kind: "page", PagePath: fmt.Sprintf("page-%03d.html", i), Outcome: "assessed"})
	}
	a := domain.Assessment{ID: aid, VersionID: v, TargetSetID: target, CoverageSetID: cid, ScopeJSON: []byte(`{"kind":"version"}`), State: "aligned_within_scope", EvidenceJSON: []byte(`{}`), ConfigRevision: 1, PermissionsRevision: 1, Origin: "ai", AssessedAt: "2026-11-02T00:00:00Z"}
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutAssessment(a, units[:256]) })).To(MatchError(domain.ErrInvalidTarget))
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutAssessment(a, units) })).To(Succeed())
})

var _ = It("T-008-03 offline indirect configuration context is derived from the exact committed observation dependency scope", Label("persistence"), func() {
	s, auth, writer, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "offline config owner")
	f := &referenceWorld{s: s, writer: writer, bytes: &searchBytes{files: map[string]string{}}}
	project := mustUnfiledProject(s)
	root := filepath.Join(GinkgoT().TempDir(), "config-source")
	code := "package retry\nfunc CanRetry(failures int,cfg Config) bool { return failures < cfg.MaxAttempts }\n"
	base := initTestGitRepo(root, map[string]string{"retry.go": code, "loader.go": "package retry\n// The loader reads policy.json into Config.MaxAttempts.\n", "policy.json": "{\"max_attempts\":3}\n"})
	configureProjectSources(s, project, []map[string]any{{"source_id": "source", "kind": "git", "root_path": root, "repo_id": "source", "retention_mode": "reference_only", "provider_ids": []string{"codex"}}})
	freshnessTestPolicy(s, project, "auto")
	version := publishInProjectWithSourcesAndEvidence(f, project, map[string]map[string]any{"e": {"source_id": "source", "path": "retry.go", "capture_status": "reference_only"}}, "<p>At failures=1 with the configured limit, a retry is allowed.</p>", map[string]map[string]any{"source": {"kind": "git", "repo_id": "source", "object_format": "sha1", "exact_revision": base}}, map[string]any{"id": "retry", "page_path": "page.html", "evidence_ids": []string{"e"}})
	changed := commitTestGitFile(root, "policy.json", "{\"max_attempts\":1}\n", "controlled offline configuration change")
	bytes := testCaptureBytes{s: s, bytes: f.bytes}
	sources := appsource.New(s, auth, bytes, gitreader.New())
	response, e := sources.Observe(ctx, owner.Token, version, f.id(), []byte(`{}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(201))
	var observation domain.SourceObservation
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		runs, e := tx.ObservationRunsByVersion(version)
		if e != nil {
			return e
		}
		Expect(runs).To(HaveLen(1))
		rows, e := tx.ObservationsByRun(runs[0].ID)
		if e != nil {
			return e
		}
		Expect(rows).To(HaveLen(1))
		observation = rows[0]
		triggers, e := tx.TriggersByTargetSet(version, runs[0].TargetSetID)
		Expect(triggers).To(BeEmpty())
		return e
	})).To(Succeed())
	Expect(observation.Classification).To(Equal("indirect"))
	Expect(observation.ObservedRevision).To(Equal(changed))
	Expect(string(observation.DependencyScopeJSON)).To(ContainSubstring("policy.json"))
	w := scan.NewWorker(s, f.bytes)
	w.Sources = sources
	w.Identity = codex.SemanticRequestSHA
	driver := &scriptedMaintenanceDriver{store: s}
	driver.responder = func(a domain.ProviderAttempt, inv domain.ProviderInvocation, inputs []domain.ProviderInput) (string, error) {
		Expect(inv.Maintenance.Mode).To(Equal(domain.MaintenanceModeFreshness))
		Expect(inputs).To(HaveLen(2))
		citations := []domain.QuestionCitation{}
		paths := map[string]bool{}
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			for _, input := range inputs {
				read, e := tx.CapturedRead(input.ReadID)
				if e != nil {
					return e
				}
				var loc struct {
					Path string `json:"path"`
				}
				Expect(json.Unmarshal(read.LocationJSON, &loc)).To(Succeed())
				paths[loc.Path] = true
				var identity struct {
					Revision string `json:"exact_revision"`
				}
				Expect(json.Unmarshal(read.SourceJSON, &identity)).To(Succeed())
				Expect(identity.Revision).To(Equal(changed))
				if loc.Path == "policy.json" {
					Expect(f.bytes.files[input.Hash]).To(Equal("{\"max_attempts\":1}\n"))
				}
				origin, id := "daemon_supplied", read.ID
				citations = append(citations, domain.QuestionCitation{Basis: "observed_input", ReadID: &id, InputOrigin: &origin, TargetSetID: read.TargetSetID, Source: read.SourceJSON, Location: read.LocationJSON, Hash: read.BytesSHA})
			}
			return nil
		})).To(Succeed())
		Expect(paths).To(HaveKey("retry.go"))
		Expect(paths).To(HaveKey("policy.json"))
		// This is a predeclared synthetic/offline comparison, not live inference.
		page := "page.html"
		return string(jsonBytes(domain.FreshnessResult{Units: []domain.FreshnessOutput{{Kind: "page", PagePath: &page, Outcome: "assessed", Discrepancy: "At failures=1 the limit of 1 refuses retry", Rationale: "The unchanged comparison 1 < 1 is false under the changed configuration", Citations: citations}}})), nil
	}
	coordinator := &work.ProviderCoordinator{Store: s, Bytes: bytes, Driver: driver, MaintenanceSource: func(c context.Context, id string) error {
		return sources.CaptureMaintenanceAndAdmit(c, id, codex.SemanticRequestSHA)
	}, Prepare: w.PrepareMaintenance, Apply: w.ApplyMaintenance}
	job := startScanJob(scan.New(s, auth, f.bytes, nil), owner.Token, domain.ScanRequest{VersionID: version, Depth: "full_scope", SourceMode: "current"})
	c, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- (&work.Dispatcher{Store: s, Scans: w, Provider: coordinator, Derived: work.NewMaintenance(s, f.bytes)}).Run(c)
	}()
	Eventually(func() string {
		j, e := s.Job(ctx, job)
		if e != nil {
			return e.Error()
		}
		return j.State
	}, 110*time.Second, 10*time.Millisecond).Should(Equal("completed"))
	cancel()
	Expect(<-done).To(Succeed())
	Expect(driver.executeCalls).To(Equal(1))
})

var _ = It("T-008-03 actually commits the first page then interrupts before the next admission, preserving the full denominator and separate target history", Label("persistence"), func() {
	s, auth, writer, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "partial freshness owner")
	f := &referenceWorld{s: s, writer: writer, bytes: &searchBytes{files: map[string]string{}}}
	project := mustUnfiledProject(s)
	root := filepath.Join(GinkgoT().TempDir(), "source")
	rev := initTestGitRepo(root, map[string]string{"retry.go": "package retry\nfunc Allowed() bool { return true }\n"})
	configureProjectSources(s, project, []map[string]any{{"source_id": "source", "kind": "git", "root_path": root, "repo_id": "source", "retention_mode": "reference_only", "provider_ids": []string{"codex"}}})
	freshnessTestPolicy(s, project, "auto")
	v := publishFreshnessPages(f, project, map[string]string{"a.html": "<p>Allowed returns true.</p>", "b.html": "<p>The configured Allowed function returns true.</p>"}, map[string]map[string]any{"source": {"kind": "git", "repo_id": "source", "object_format": "sha1", "exact_revision": rev}}, map[string]map[string]any{"source-evidence": {"source_id": "source", "path": "retry.go", "capture_status": "reference_only"}}, "historical")
	// This models an older import whose compact version summary omitted intent.
	// The normalized immutable manifest remains historical while project default
	// and current HEAD both move; default admission must still read pinned A.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var body map[string]any
		Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
		body["projects"].(map[string]any)[project].(map[string]any)["source_mode_default"] = "current"
		cfg.Value = jsonBytes(body)
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
	_ = commitTestGitFile(root, "retry.go", "package retry\nfunc Allowed() bool { return false }\n", "controlled moving current target")
	bytes := testCaptureBytes{s: s, bytes: f.bytes}
	sources := appsource.New(s, auth, bytes, gitreader.New())
	w := scan.NewWorker(s, f.bytes)
	w.Sources = sources
	w.Identity = codex.SemanticRequestSHA
	driver := &scriptedMaintenanceDriver{store: s}
	driver.responder = func(a domain.ProviderAttempt, inv domain.ProviderInvocation, inputs []domain.ProviderInput) (string, error) {
		Expect(inv.Maintenance.SourceMode).To(Equal("pinned"))
		Expect(inv.Maintenance.Explanations).To(HaveLen(1))
		Expect(inputs).To(HaveLen(1))
		Expect(f.bytes.files[inputs[0].Hash]).To(ContainSubstring("return true"))
		var r domain.CapturedRead
		var units []domain.RequiredUnit
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			var e error
			r, e = tx.CapturedRead(inputs[0].ReadID)
			if e != nil {
				return e
			}
			units, e = app.AllRequiredUnits(tx, inv.Maintenance.CoverageSetID)
			return e
		})).To(Succeed())
		Expect(units).To(HaveLen(1))
		Expect(units[0].PagePath).To(Equal(*inv.Maintenance.Explanations[0].PagePath))
		origin, rid := "daemon_supplied", r.ID
		page := units[0].PagePath
		return string(jsonBytes(domain.FreshnessResult{Units: []domain.FreshnessOutput{{Kind: "page", PagePath: &page, Outcome: "assessed", Rationale: "Offline scripted comparison: the single supplied page claim matches Allowed returning true", Citations: []domain.QuestionCitation{{Basis: "observed_input", ReadID: &rid, InputOrigin: &origin, TargetSetID: r.TargetSetID, Source: r.SourceJSON, Location: r.LocationJSON, Hash: r.BytesSHA}}}}})), nil
	}
	coordinator := &work.ProviderCoordinator{Store: s, Bytes: bytes, Driver: driver, MaintenanceSource: func(c context.Context, id string) error {
		return sources.CaptureMaintenanceAndAdmit(c, id, codex.SemanticRequestSHA)
	}, Prepare: w.PrepareMaintenance, Apply: w.ApplyMaintenance}
	step := func(job string) {
		Expect(w.Process(ctx, job)).To(Succeed())
		var ms []domain.MaintenanceIntent
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; ms, e = tx.MaintenanceIntentsByScan(job); return e })).To(Succeed())
		for _, m := range ms {
			child, e := s.Job(ctx, m.JobID)
			Expect(e).NotTo(HaveOccurred())
			if child.State == "queued" || child.State == "running" || child.State == "paused" && child.Error == "source_capture_pending" {
				Expect(coordinator.Process(ctx, child.ID)).To(Succeed())
			}
		}
	}
	job := startScanJob(scan.New(s, auth, f.bytes, nil), owner.Token, domain.ScanRequest{VersionID: v, Depth: "full_scope"})
	for turn := 0; turn < 64 && driver.executeCalls == 0; turn++ {
		step(job)
	}
	Expect(driver.executeCalls).To(Equal(1))
	var first domain.MaintenanceIntent
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		ms, e := tx.MaintenanceIntentsByScan(job)
		Expect(ms).To(HaveLen(1))
		first = ms[0]
		return e
	})).To(Succeed())
	// No second Process/admission occurs before this actual authenticated cancel.
	_, e := auth.CancelJob(ctx, owner.Token, job, f.id())
	Expect(e).NotTo(HaveOccurred())
	Expect(w.Process(ctx, job)).To(Succeed())
	Expect(driver.executeCalls).To(Equal(1))
	var held int
	Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='job' AND owner_id=?`, job).Scan(&held)).To(Succeed())
	Expect(held).To(BeZero())
	q := query.New(s, auth, f.bytes)
	var full domain.CoverageSet
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		full, e = tx.CoverageSetByID(first.FullCoverageSetID)
		return e
	})).To(Succeed())
	view, e := q.Assessments(ctx, owner.Token, v, query.AssessmentQuery{TargetSetID: full.TargetSetID})
	Expect(e).NotTo(HaveOccurred())
	a := view["applicable"].(map[string]any)
	Expect(a["status"]).To(Equal("not_assessed"))
	counts := a["coverage"].(map[string]any)
	Expect(counts["eligible"]).To(Equal(2))
	Expect(counts["assessed"]).To(Equal(1))
	Expect(counts["complete"]).To(BeFalse())
	rows, e := q.AssessmentCoverage(ctx, owner.Token, v, full.ID, "", query.Options{})
	Expect(e).NotTo(HaveOccurred())
	Expect(rows.Items).To(HaveLen(2))
	Expect(rows.Items[0]["outcome"]).To(Equal("assessed"))
	Expect(rows.Items[1]["outcome"]).To(Equal("not_checked"))
	Expect(rows.Items[1]["reason"]).To(HaveValue(Equal("scan_cancelled_before_unit_assessment")))
	page, e := q.Assessments(ctx, owner.Token, v, query.AssessmentQuery{TargetSetID: full.TargetSetID, ScopeKind: "page", PagePath: "a.html"})
	Expect(e).NotTo(HaveOccurred())
	Expect(page["applicable"].(map[string]any)["status"]).To(Equal("aligned_within_scope"))
	// A subsequent complete scan has its own exact target and two actual calls;
	// it cannot fill the missing unit of the cancelled capture's history.
	next := startScanJob(scan.New(s, auth, f.bytes, nil), owner.Token, domain.ScanRequest{VersionID: v, Depth: "full_scope", SourceMode: "pinned"})
	for turn := 0; turn < 96; turn++ {
		step(next)
		j, e := s.Job(ctx, next)
		Expect(e).NotTo(HaveOccurred())
		if j.State == "completed" {
			break
		}
	}
	Expect(driver.executeCalls).To(Equal(3))
	j, e := s.Job(ctx, next)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	var report domain.ScanReport
	Expect(json.Unmarshal(j.Result, &report)).To(Succeed())
	Expect(report.Semantic.Assessed).To(Equal(2))
	Expect(report.Semantic.Complete).To(BeTrue())
	var version domain.Version
	Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; version, e = tx.Version(v); return e })).To(Succeed())
	document, e := q.Document(ctx, owner.Token, version.DocumentID)
	Expect(e).NotTo(HaveOccurred())
	Expect(document.Freshness.(map[string]any)["source_intent"]).To(Equal("historical"))
	Expect(document.Freshness.(map[string]any)["status"]).To(Equal("aligned_within_scope"))
	Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='job' AND owner_id=?`, next).Scan(&held)).To(Succeed())
	Expect(held).To(BeZero())
	again, e := q.Assessments(ctx, owner.Token, v, query.AssessmentQuery{TargetSetID: full.TargetSetID})
	Expect(e).NotTo(HaveOccurred())
	Expect(again["applicable"].(map[string]any)["status"]).To(Equal("not_assessed"))
})

func publishFreshnessPages(f *referenceWorld, projectID string, pages map[string]string, sources map[string]map[string]any, evidence map[string]map[string]any, intent any) string {
	GinkgoHelper()
	version, document, set := f.id(), f.id(), f.id()
	raw, _ := json.Marshal(map[string]any{"schema_version": "1.0", "viewer_profile": "html_js_v1", "references": []any{}, "version": map[string]any{"source_intent": intent}})
	manifest := digest(raw)
	f.bytes.files[manifest] = string(raw)
	if projectID == "" {
		h, err := f.s.Health(ctx)
		Expect(err).NotTo(HaveOccurred())
		projectID = h.Library.UnfiledProjectID
	}
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.CreateContentSet(set, digest(nil)); err != nil {
			return err
		}
		if err := tx.PutDurableObject(domain.Object{Hash: manifest, Size: int64(len(raw)), State: "ready"}); err != nil {
			return err
		}
		for pPath, body := range pages {
			pHash := digest([]byte(body))
			f.bytes.files[pHash] = body
			if err := tx.PutDurableObject(domain.Object{Hash: pHash, Size: int64(len(body)), State: "ready"}); err != nil {
				return err
			}
			if err := tx.PutEntry(domain.Entry{ContentSetID: set, Path: pPath, Kind: "file", Hash: pHash, Size: int64(len(body))}); err != nil {
				return err
			}
		}
		if err := tx.SealContentSet(set); err != nil {
			return err
		}
		metadataID := ""
		if len(sources) > 0 || len(evidence) > 0 {
			metadataID = f.id()
			if err := tx.CreateMetadataIndex(domain.MetadataIndex{ID: metadataID, PrincipalID: "system", Hash: manifest, ContentSetID: set}); err != nil {
				return err
			}
			for srcID, srcMeta := range sources {
				if err := tx.PutImportMetadata(metadataID, "sources", srcID, jsonBytes(srcMeta)); err != nil {
					return err
				}
			}
			for id, meta := range evidence {
				if e := tx.PutImportMetadata(metadataID, "evidence", id, jsonBytes(meta)); e != nil {
					return e
				}
			}
		}
		if err := tx.FinalizeImport(
			domain.ImportIntent{JobID: metadataID, NewDocument: true, DocumentID: document, ProjectID: projectID, DocumentRevision: 1, Title: "MultiPage"},
			domain.Version{ID: version, DocumentID: document, ContentSetID: set, ManifestSHA: manifest, PayloadSHA: digest(nil), Title: "MultiPage", ViewerProfile: "html_js_v1", SourceBasis: "unverified"},
		); err != nil {
			return err
		}
		if metadataID != "" {
			if err := tx.ReleaseMetadataPreparation(metadataID); err != nil {
				return err
			}
		}
		return nil
	})).To(Succeed())
	return version
}

var _ = DescribeTable("T-008-03 saved freshness continuation obeys current policy and never repeats Execute", Label("persistence"), func(invalid bool) {
	s, auth, writer, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "saved freshness owner")
	f := &referenceWorld{s: s, writer: writer, bytes: &searchBytes{files: map[string]string{}}}
	project := mustUnfiledProject(s)
	root := filepath.Join(GinkgoT().TempDir(), "source")
	rev := initTestGitRepo(root, map[string]string{"retry.go": "package retry\nfunc Allowed() bool { return true }\n"})
	configureProjectSources(s, project, []map[string]any{{"source_id": "source", "kind": "git", "root_path": root, "repo_id": "source", "retention_mode": "reference_only", "provider_ids": []string{"codex"}}})
	freshnessTestPolicy(s, project, "auto")
	v := publishInProjectWithSourcesAndEvidence(f, project, map[string]map[string]any{"e": {"source_id": "source", "path": "retry.go", "capture_status": "reference_only"}}, "<p>Allowed returns true.</p>", map[string]map[string]any{"source": {"kind": "git", "repo_id": "source", "object_format": "sha1", "exact_revision": rev}})
	bytes := testCaptureBytes{s: s, bytes: f.bytes}
	sources := appsource.New(s, auth, bytes, gitreader.New())
	w := scan.NewWorker(s, f.bytes)
	w.Sources = sources
	w.Identity = codex.SemanticRequestSHA
	driver := &scriptedMaintenanceDriver{store: s}
	driver.responder = func(a domain.ProviderAttempt, inv domain.ProviderInvocation, inputs []domain.ProviderInput) (string, error) {
		var read domain.CapturedRead
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; read, e = tx.CapturedRead(inputs[0].ReadID); return e })).To(Succeed())
		page := "page.html"
		if invalid {
			page = "foreign.html"
		}
		origin, rid := "daemon_supplied", read.ID
		return string(jsonBytes(domain.FreshnessResult{Units: []domain.FreshnessOutput{{Kind: "page", PagePath: &page, Outcome: "assessed", Rationale: "Offline scripted single-page comparison", Citations: []domain.QuestionCitation{{Basis: "observed_input", ReadID: &rid, InputOrigin: &origin, TargetSetID: read.TargetSetID, Source: read.SourceJSON, Location: read.LocationJSON, Hash: read.BytesSHA}}}}})), nil
	}
	injected := false
	prepare := func(c context.Context, a domain.ProviderAttempt) error {
		if e := w.PrepareMaintenance(c, a); e != nil {
			return e
		}
		if !injected {
			injected = true
			return errors.New("controlled interruption after durable preparation")
		}
		return nil
	}
	coordinator := &work.ProviderCoordinator{Store: s, Bytes: bytes, Driver: driver, MaintenanceSource: func(c context.Context, id string) error {
		return sources.CaptureMaintenanceAndAdmit(c, id, codex.SemanticRequestSHA)
	}, Prepare: prepare, Apply: w.ApplyMaintenance}
	job := startScanJob(scan.New(s, auth, f.bytes, nil), owner.Token, domain.ScanRequest{VersionID: v, Depth: "full_scope", SourceMode: "pinned"})
	var childID string
	var gotError bool
	for turn := 0; turn < 64 && !gotError; turn++ {
		Expect(w.Process(ctx, job)).To(Succeed())
		var ms []domain.MaintenanceIntent
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; ms, e = tx.MaintenanceIntentsByScan(job); return e })).To(Succeed())
		for _, m := range ms {
			childID = m.JobID
			child, e := s.Job(ctx, childID)
			Expect(e).NotTo(HaveOccurred())
			if child.State == "queued" || child.State == "paused" && child.Error == "source_capture_pending" {
				if e = coordinator.Process(ctx, childID); e != nil {
					gotError = true
					break
				}
			}
		}
	}
	Expect(gotError).To(BeTrue())
	Expect(driver.executeCalls).To(Equal(1))
	Expect(w.Process(ctx, job)).To(Succeed())
	parent, e := s.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(parent.State).To(Equal("paused"))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(childID)
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].State).To(Equal("result_saved"))
		m, e := tx.MaintenanceIntent(childID)
		if !invalid {
			Expect(m.PreparedAttemptID).To(Equal(rows[0].ID))
		}
		history, e := tx.AssessmentsByVersion(v, "", "", 64)
		Expect(history).To(BeEmpty())
		return e
	})).To(Succeed())
	freshnessTestPolicy(s, project, "disabled")
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.ResumeProviderJob(job, parent.AttemptGeneration, "continue")
		return e
	})).To(MatchError(domain.ErrDenied))
	after, e := s.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.State).To(Equal("paused"))
	Expect(after.Revision).To(Equal(parent.Revision))
	Expect(driver.executeCalls).To(Equal(1))
	freshnessTestPolicy(s, project, "auto")
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.ResumeProviderJob(job, parent.AttemptGeneration, "continue")
		return e
	})).To(Succeed())
	Expect(w.Process(ctx, job)).To(Succeed())
	e = coordinator.Process(ctx, childID)
	if invalid {
		Expect(e).To(HaveOccurred())
		Expect(driver.executeCalls).To(Equal(1))
		Expect(w.Process(ctx, job)).To(Succeed())
		return
	}
	Expect(e).NotTo(HaveOccurred())
	Expect(driver.executeCalls).To(Equal(1))
	Expect(w.Process(ctx, job)).To(Succeed())
	parent, e = s.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(parent.State).To(Equal("completed"))
	q := query.New(s, auth, f.bytes)
	view, e := q.Assessments(ctx, owner.Token, v, query.AssessmentQuery{})
	Expect(e).NotTo(HaveOccurred())
	Expect(view["items"]).To(HaveLen(1))
	Expect(view["applicable"].(map[string]any)["status"]).To(Equal("aligned_within_scope"))
}, Entry("validated saved result resumes without another dispatch", false), Entry("foreign unit output remains rejected on repeated Continue", true))

var _ = DescribeTable("T-008-03 actual missing or denied source retains unavailable full coverage without dispatch", Label("persistence"), func(denied bool) {
	s, auth, writer, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "source fault owner")
	f := &referenceWorld{s: s, writer: writer, bytes: &searchBytes{files: map[string]string{}}}
	project := mustUnfiledProject(s)
	root := filepath.Join(GinkgoT().TempDir(), "source")
	rev := initTestGitRepo(root, map[string]string{"retry.go": "package retry\nfunc Allowed() bool { return true }\n"})
	configureProjectSources(s, project, []map[string]any{{"source_id": "source", "kind": "git", "root_path": root, "repo_id": "source", "retention_mode": "reference_only", "provider_ids": []string{"codex"}}})
	freshnessTestPolicy(s, project, "auto")
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var body map[string]any
		Expect(json.Unmarshal(cfg.Value, &body)).To(Succeed())
		body["projects"].(map[string]any)[project].(map[string]any)["source_mode_default"] = "current"
		cfg.Value = jsonBytes(body)
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
	v := publishFreshnessPages(f, project, map[string]string{"a.html": "<p>Allowed returns true.</p>", "b.html": "<p>Allowed returns true in the selected revision.</p>"}, map[string]map[string]any{"source": {"kind": "git", "repo_id": "source", "object_format": "sha1", "exact_revision": rev}}, map[string]map[string]any{"e": {"source_id": "source", "path": "retry.go", "capture_status": "reference_only"}}, "moving_target")
	if denied {
		Expect(os.Chmod(filepath.Join(root, ".git"), 0)).To(Succeed())
		DeferCleanup(func() { Expect(os.Chmod(filepath.Join(root, ".git"), 0700)).To(Succeed()) })
	} else {
		Expect(os.Rename(root, root+"-unavailable")).To(Succeed())
	}
	bytes := testCaptureBytes{s: s, bytes: f.bytes}
	sources := appsource.New(s, auth, bytes, gitreader.New())
	response, err := sources.Observe(ctx, owner.Token, v, f.id(), []byte(`{}`))
	Expect(err).NotTo(HaveOccurred())
	if denied {
		Expect(response.Status).To(Equal(503))
		Expect(string(response.Body)).To(ContainSubstring("source_read_failed"))
	} else {
		Expect(response.Status).To(Equal(201))
	}
	w := scan.NewWorker(s, f.bytes)
	w.Sources, w.Identity = sources, codex.SemanticRequestSHA
	driver := &scriptedMaintenanceDriver{store: s}
	driver.responder = func(domain.ProviderAttempt, domain.ProviderInvocation, []domain.ProviderInput) (string, error) {
		Fail("Unavailable source must not submit a model request")
		return "", nil
	}
	job := startScanJob(scan.New(s, auth, f.bytes, nil), owner.Token, domain.ScanRequest{VersionID: v, Depth: "full_scope", SourceMode: "current"})
	for turn := 0; turn < 64; turn++ {
		Expect(w.Process(ctx, job)).To(Succeed())
		j, err := s.Job(ctx, job)
		Expect(err).NotTo(HaveOccurred())
		if j.State == "completed" {
			break
		}
	}
	j, err := s.Job(ctx, job)
	Expect(err).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	var report domain.ScanReport
	Expect(json.Unmarshal(j.Result, &report)).To(Succeed())
	Expect(report.Semantic.Eligible).To(Equal(2))
	Expect(report.Semantic.Assessed).To(BeZero())
	Expect(report.Semantic.Complete).To(BeFalse())
	Expect(driver.executeCalls).To(BeZero())
	q := query.New(s, auth, f.bytes)
	view, err := q.Assessments(ctx, owner.Token, v, query.AssessmentQuery{})
	Expect(err).NotTo(HaveOccurred())
	Expect(view["items"]).To(BeEmpty())
	a := view["applicable"].(map[string]any)
	Expect(a["status"]).To(Equal("unable_to_verify"))
	Expect(a["target_set_id"]).To(BeNil())
	coverage := a["coverage"].(map[string]any)
	Expect(coverage["eligible"]).To(Equal(2))
	Expect(coverage["complete"]).To(BeFalse())
	units, err := q.AssessmentCoverage(ctx, owner.Token, v, coverage["coverage_set_id"].(string), "", query.Options{})
	Expect(err).NotTo(HaveOccurred())
	Expect(units.Items).To(HaveLen(2))
	for _, unit := range units.Items {
		Expect(unit["outcome"]).To(Equal("not_checked"), "blocked acquisition is not a fabricated semantic contribution")
		Expect(unit["reason"]).NotTo(BeNil())
		Expect(unit["assessment_id"]).To(BeNil())
		Expect(unit["target_set_id"]).To(BeNil())
	}
}, Entry("missing approved root", false), Entry("approved checkout permission denied", true))

var _ = It("T-008-03 reuses an exact complete retained pinned snapshot after the live files disappear", Label("persistence"), func() {
	s, auth, writer, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "snapshot freshness owner")
	f := &referenceWorld{s: s, writer: writer, bytes: &searchBytes{files: map[string]string{}}}
	project := mustUnfiledProject(s)
	root := GinkgoT().TempDir()
	body := "The selected flag is true.\n"
	Expect(os.WriteFile(filepath.Join(root, "selected.txt"), []byte(body), 0600)).To(Succeed())
	configureProjectSources(s, project, []map[string]any{{"source_id": "source", "kind": "files", "root_path": root, "retention_mode": "selected_snapshot", "provider_ids": []string{"codex"}}})
	freshnessTestPolicy(s, project, "auto")
	v := publishFreshnessPages(f, project, map[string]string{"a.html": "<p>The selected flag is true.</p>"}, map[string]map[string]any{"authored-snapshot": {"kind": "snapshot", "retention_mode": "reference_only", "snapshot_sha256": digest([]byte(body))}}, map[string]map[string]any{"e": {"source_id": "authored-snapshot", "path": "selected.txt", "capture_status": "reference_only"}}, "historical")
	bytes := testCaptureBytes{s: s, bytes: f.bytes}
	sources := appsource.New(s, auth, bytes, gitreader.New())
	sources.Files = localTestFileReader{}
	captured, e := sources.Capture(ctx, owner.Token, f.id(), project, appsource.CaptureRequest{VersionID: v, SourceMode: "current", ProviderID: "codex", Selections: []appsource.CaptureSelection{{SourceID: "source", Paths: []appsource.CapturePath{{Path: "selected.txt"}}}}})
	Expect(e).NotTo(HaveOccurred())
	var receipt struct {
		CaptureID string `json:"capture_id"`
	}
	Expect(json.Unmarshal(captured.Body, &receipt)).To(Succeed())
	var reads []domain.SourceCaptureRead
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		reads, e = tx.SourceCaptureReads(receipt.CaptureID, "", 64)
		return e
	})).To(Succeed())
	Expect(reads).To(HaveLen(1))
	Expect(reads[0].Availability).To(Equal("available"))
	pinned, e := sources.Capture(ctx, owner.Token, f.id(), project, appsource.CaptureRequest{VersionID: v, SourceMode: "pinned", ProviderID: "codex", Selections: []appsource.CaptureSelection{{SourceID: "source", Paths: []appsource.CapturePath{{Path: "selected.txt", ReadID: reads[0].ID}}}}})
	Expect(e).NotTo(HaveOccurred())
	var exact struct {
		Target string `json:"target_set_id"`
	}
	Expect(json.Unmarshal(pinned.Body, &exact)).To(Succeed())
	Expect(exact.Target).NotTo(BeEmpty(), string(pinned.Body))
	Expect(os.RemoveAll(root)).To(Succeed())
	w := scan.NewWorker(s, f.bytes)
	w.Sources = sources
	w.Identity = codex.SemanticRequestSHA
	driver := &scriptedMaintenanceDriver{store: s}
	driver.responder = func(a domain.ProviderAttempt, inv domain.ProviderInvocation, inputs []domain.ProviderInput) (string, error) {
		Expect(inv.Maintenance.SourceMode).To(Equal("pinned"))
		Expect(inputs).To(HaveLen(1))
		Expect(f.bytes.files[inputs[0].Hash]).To(Equal(body))
		var read domain.CapturedRead
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; read, e = tx.CapturedRead(inputs[0].ReadID); return e })).To(Succeed())
		Expect(read.TargetSetID).To(Equal(exact.Target))
		origin, rid, page := "daemon_supplied", read.ID, "a.html"
		return string(jsonBytes(domain.FreshnessResult{Units: []domain.FreshnessOutput{{Kind: "page", PagePath: &page, Outcome: "assessed", Rationale: "Offline scripted comparison of the one-page claim to the complete retained snapshot", Citations: []domain.QuestionCitation{{Basis: "observed_input", ReadID: &rid, InputOrigin: &origin, TargetSetID: read.TargetSetID, Source: read.SourceJSON, Location: read.LocationJSON, Hash: read.BytesSHA}}}}})), nil
	}
	coordinator := &work.ProviderCoordinator{Store: s, Bytes: bytes, Driver: driver, MaintenanceSource: func(c context.Context, id string) error {
		return sources.CaptureMaintenanceAndAdmit(c, id, codex.SemanticRequestSHA)
	}, Prepare: w.PrepareMaintenance, Apply: w.ApplyMaintenance}
	job := startScanJob(scan.New(s, auth, f.bytes, nil), owner.Token, domain.ScanRequest{VersionID: v, Depth: "full_scope", TargetSetID: exact.Target})
	for turn := 0; turn < 64; turn++ {
		Expect(w.Process(ctx, job)).To(Succeed())
		var ms []domain.MaintenanceIntent
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; ms, e = tx.MaintenanceIntentsByScan(job); return e })).To(Succeed())
		for _, m := range ms {
			child, e := s.Job(ctx, m.JobID)
			Expect(e).NotTo(HaveOccurred())
			if child.State == "queued" || child.State == "paused" && child.Error == "source_capture_pending" {
				Expect(coordinator.Process(ctx, child.ID)).To(Succeed())
			}
		}
		j, e := s.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		if j.State == "completed" {
			break
		}
	}
	Expect(driver.executeCalls).To(Equal(1))
	q := query.New(s, auth, f.bytes)
	view, e := q.Assessments(ctx, owner.Token, v, query.AssessmentQuery{})
	Expect(e).NotTo(HaveOccurred())
	Expect(view["applicable"].(map[string]any)["status"]).To(Equal("aligned_within_scope"))
	Expect(view["applicable"].(map[string]any)["target_set_id"]).To(Equal(exact.Target))
})
