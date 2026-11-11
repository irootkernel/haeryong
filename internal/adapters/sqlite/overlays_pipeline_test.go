package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/ai/codex"
	"haeryong.local/haeryong/internal/adapters/contentstore"
	v1 "haeryong.local/haeryong/internal/adapters/http/v1"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/scan"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
)

// The public read below consumes physical content-store objects, independently
// of the fake provider/worker input map used by this offline pipeline.
type managementObjectBytes struct{ objects *contentstore.Store }

func (b managementObjectBytes) Read(c context.Context, o domain.Object, _ domain.Pin, consume func(io.ReadSeeker) error) error {
	return b.objects.VerifiedRead(c, contentstore.Object{Hash: o.Hash, Size: o.Size}, consume)
}

var _ = DescribeTable("T00804 guarded production maintenance pipeline applies only effective category modes and never publishes content", Label("persistence"), func(mode string, restrict bool) {
	s, auth, w, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "Task04 offline pipeline")
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
	b := f.publish(nil, "<h1 id='retry'>Checkpoint recovery comparison</h1><p>Checkpoint journal recovery coordinates a worker restart by restoring durable retry state.</p>")
	a := f.publish([]any{f.declaration(b, "authored-checkpoint")}, "<h1 id='retry'>Checkpoint recovery</h1><p>Checkpoint journal recovery records retry state in a durable journal before restarting a worker.</p>")
	for _, v := range []string{a, b} {
		f.run(v, "search")
		f.run(v, "references")
	}
	objects, e := contentstore.New(filepath.Join(w.Profile.DataDir, "storage"))
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(objects.Close()).To(Succeed()) })
	for _, body := range f.bytes.files {
		staged, e := objects.Stage(ctx, bytes.NewReader([]byte(body)))
		Expect(e).NotTo(HaveOccurred())
		_, e = objects.Finalize(ctx, staged, func(_ contentstore.Object, publish func() error) error { return publish() })
		Expect(e).NotTo(HaveOccurred())
	}
	public := v1.New(query.New(s, auth, managementObjectBytes{objects}), nil, nil)
	// Snapshot complete rows, not a count or a selected authored edge. Each table
	// has stable primary-key ordering; original authored_json remains raw bytes.
	originalInventory := func() []byte {
		out := []any{}
		for _, table := range []string{"document_versions", "content_sets", "version_entries", "authored_references", "authored_edges", "review_states", "review_events"} {
			rows, e := s.db.Query("SELECT * FROM " + table + " ORDER BY 1,2")
			Expect(e).NotTo(HaveOccurred())
			columns, e := rows.Columns()
			Expect(e).NotTo(HaveOccurred())
			values := []any{}
			for rows.Next() {
				row := make([]any, len(columns))
				ptr := make([]any, len(columns))
				for i := range row {
					ptr[i] = &row[i]
				}
				Expect(rows.Scan(ptr...)).To(Succeed())
				values = append(values, row)
			}
			Expect(rows.Err()).NotTo(HaveOccurred())
			Expect(rows.Close()).To(Succeed())
			out = append(out, []any{table, values})
		}
		return jsonBytes(out)
	}
	publicBytes := func() [][]byte {
		out := [][]byte{}
		for _, version := range []string{a, b} {
			for _, suffix := range []string{"/file?path=page.html", "/metadata", "/references", "/backlinks"} {
				r := httptest.NewRequest("GET", "/api/v1/versions/"+version+suffix, nil)
				r.Header.Set("Authorization", "Bearer "+owner.Token)
				w := httptest.NewRecorder()
				public.ServeHTTP(w, r)
				Expect(w.Code).To(Equal(200), suffix+" "+w.Body.String())
				raw := w.Body.Bytes()
				if suffix == "/references" || suffix == "/backlinks" {
					// request_id is delivery accounting, not authored graph data.
					var envelope struct {
						Data json.RawMessage `json:"data"`
					}
					Expect(json.Unmarshal(raw, &envelope)).To(Succeed())
					raw = envelope.Data
				}
				out = append(out, append([]byte(nil), raw...))
			}
		}
		return out
	}
	var authored, backlinks int
	Expect(s.db.QueryRow("SELECT count(*) FROM authored_references").Scan(&authored)).To(Succeed())
	Expect(s.db.QueryRow("SELECT count(*) FROM authored_edges").Scan(&backlinks)).To(Succeed())
	Expect(authored).To(Equal(1))
	Expect(backlinks).To(Equal(1))
	beforeInventory, beforePublic := originalInventory(), publicBytes()
	project := mustUnfiledProject(s)
	setPolicy := func(category string) {
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			cfg, e := tx.Configuration()
			if e != nil {
				return e
			}
			var value map[string]any
			Expect(json.Unmarshal(cfg.Value, &value)).To(Succeed())
			maintenance := value["maintenance"].(map[string]any)
			maintenance["scope"] = "selected_projects"
			maintenance["project_ids"] = []string{project}
			maintenance["max_provider_attempts_per_run"] = 20
			maintenance["auto_apply"] = map[string]any{"backlink_index_repair": "disabled", "freshness_assessment": "disabled", "related_explanation": category, "topic_assignment": category}
			maintenance["content_generation"] = "propose"
			value["projects"].(map[string]any)[project] = map[string]any{"provider_id": "codex"}
			cfg.Value = jsonBytes(value)
			return tx.SetConfiguration(cfg, cfg.Revision)
		})).To(Succeed())
	}
	setPolicy(mode)
	driver := &scriptedMaintenanceDriver{store: s, responder: func(_ domain.ProviderAttempt, inv domain.ProviderInvocation, _ []domain.ProviderInput) (string, error) {
		if raw, ok := defaultNominationEnvelopeJSON(inv); ok {
			return raw, nil
		}
		focal := inv.Maintenance.VersionID
		target := b
		if focal == b {
			target = a
		}
		topics := []map[string]any{{"topic": "checkpoint-recovery", "rationale": "The focal checkpoint journal recovery explanation explicitly restores durable retry state before restarting a worker.", "uncertainty": "source_unverified"}}
		if mode == "propose" && !restrict {
			topics = append(topics, map[string]any{"topic": "journal-recovery", "rationale": "The focal explanation records retry state in a durable journal for recovery before worker restart.", "uncertainty": "source_unverified"}, map[string]any{"topic": "retry-state", "rationale": "The focal explanation explicitly preserves durable retry state before the worker restarts.", "uncertainty": "source_unverified"})
		}
		return string(jsonBytes(map[string]any{
			"relations":    []map[string]any{{"target_version_id": target, "target_page_path": "page.html", "target_region_id": "", "relation_type": "conceptual_analogy", "rationale": "Both checkpoint recovery explanations compare durable journal retry-state restoration before a worker restart; this is a conceptual comparison, not a code dependency.", "uncertainty": "source_unverified"}},
			"topics":       topics,
			"content_gaps": []map[string]any{}, "limitations": []map[string]any{},
		})), nil
	}}
	worker := scan.NewWorker(s, f.bytes)
	worker.Identity = codex.SemanticRequestSHA
	coord := work.ProviderCoordinator{Store: s, Bytes: testCaptureBytes{s: s, bytes: f.bytes}, Driver: driver, Prepare: worker.PrepareMaintenance, Apply: worker.ApplyMaintenance}
	if mode == "auto" && !restrict {
		coord.Apply = func(tx app.WriteTx, attempt domain.ProviderAttempt) error {
			intent, e := tx.MaintenanceIntent(attempt.JobID)
			if e != nil {
				return e
			}
			if intent.Mode != "" {
				return worker.ApplyMaintenance(tx, attempt)
			}
			before, e := tx.JobItem(intent.ScanJobID, intent.ItemID)
			if e != nil {
				return e
			}
			if e = worker.ApplyMaintenance(tx, attempt); e != nil {
				return e
			}
			findings, e := tx.JobFindings(domain.FindingFilter{JobID: intent.ScanJobID, Limit: 200}, "", "")
			if e != nil {
				return e
			}
			// Exercise same-result delivery with an already committed finding in the
			// writer while retaining the item's original admissible state.
			if e = tx.UpdateJobItem(before); e != nil {
				return e
			}
			if e = worker.ApplyMaintenance(tx, attempt); e != nil {
				return e
			}
			for _, original := range findings {
				if original.Status != "applied" {
					continue
				}
				current, e := tx.Finding(original.ID)
				if e != nil {
					return e
				}
				Expect(current).To(Equal(original))
			}
			return nil
		}
	}
	if restrict {
		changed := false
		coord.Prepare = func(c context.Context, attempt domain.ProviderAttempt) error {
			if e := worker.PrepareMaintenance(c, attempt); e != nil {
				return e
			}
			if !changed {
				setPolicy("propose")
				changed = true
			}
			return nil
		}
	}
	core := scan.New(s, auth, f.bytes, nil)
	runRequest := func(request domain.ScanRequest) string {
		job := startScanJob(core, owner.Token, request)
		for turn := 0; turn < 100; turn++ {
			j, e := s.Job(ctx, job)
			Expect(e).NotTo(HaveOccurred())
			if j.State == "completed" {
				return job
			}
			Expect(j.State).NotTo(Or(Equal("failed"), Equal("paused"), Equal("cancelled")))
			Expect(worker.Process(ctx, job)).To(Succeed())
			var children []domain.MaintenanceIntent
			Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; children, e = tx.MaintenanceIntentsByScan(job); return e })).To(Succeed())
			for _, m := range children {
				child, e := s.Job(ctx, m.JobID)
				Expect(e).NotTo(HaveOccurred())
				if child.State == "queued" || child.State == "running" || child.State == "reconciling" {
					Expect(coord.Process(ctx, child.ID)).To(Succeed())
				}
			}
		}
		Fail("Task04 pipeline did not complete")
		return ""
	}
	run := func() string { return runRequest(domain.ScanRequest{ProjectID: project, Depth: "deep_semantic"}) }
	first := run()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		findings, e := tx.JobFindings(domain.FindingFilter{JobID: first, Limit: 200}, "", "")
		if e != nil {
			return e
		}
		for _, f := range findings {
			if f.Kind != "topic_assignment" {
				continue
			}
			var p map[string]any
			Expect(json.Unmarshal(f.Proposal, &p)).To(Succeed())
			Expect(p["evidence_basis"]).To(Equal("explanation_text"))
			unit := p["origin_unit"].(map[string]any)
			Expect(unit["version_id"]).To(Equal(*f.VersionID))
			Expect(unit["document_id"]).To(Equal(*f.DocumentID))
			Expect(unit["project_id"]).To(Equal(project))
			var ev []domain.FindingEvidence
			Expect(json.Unmarshal(f.Evidence, &ev)).To(Succeed())
			Expect(ev).NotTo(BeEmpty())
			Expect(ev[0].Kind).To(Equal("explanation_context"))
		}
		rows, e := tx.ManagementEffects(domain.ManagementFilter{Kind: "topic_assignment"}, "", 200)
		if e != nil {
			return e
		}
		for _, m := range rows {
			Expect(m.EvidenceBasis).To(Equal("explanation_text"))
		}
		return nil
	})).To(Succeed())
	var effects, topics, proposed, applied int
	Expect(s.db.QueryRow("SELECT count(*) FROM overlay_relations").Scan(&effects)).To(Succeed())
	Expect(s.db.QueryRow("SELECT count(*) FROM topic_assignments").Scan(&topics)).To(Succeed())
	Expect(s.db.QueryRow("SELECT count(*) FROM findings WHERE job_id=? AND status='proposed'", first).Scan(&proposed)).To(Succeed())
	Expect(s.db.QueryRow("SELECT count(*) FROM findings WHERE job_id=? AND status='applied'", first).Scan(&applied)).To(Succeed())
	if mode == "auto" && !restrict {
		Expect(effects).To(BeNumerically(">", 0))
		Expect(topics).To(BeNumerically(">", 0))
		Expect(applied).To(Equal(effects + topics))
		var before []string
		rows, e := s.db.Query("SELECT value FROM overlay_relations UNION ALL SELECT value FROM topic_assignments ORDER BY value")
		Expect(e).NotTo(HaveOccurred())
		for rows.Next() {
			var v string
			Expect(rows.Scan(&v)).To(Succeed())
			before = append(before, v)
		}
		Expect(rows.Close()).To(Succeed())
		second := run()
		Expect(second).NotTo(Equal(first))
		var after []string
		rows, e = s.db.Query("SELECT value FROM overlay_relations UNION ALL SELECT value FROM topic_assignments ORDER BY value")
		Expect(e).NotTo(HaveOccurred())
		for rows.Next() {
			var v string
			Expect(rows.Scan(&v)).To(Succeed())
			after = append(after, v)
		}
		Expect(rows.Close()).To(Succeed())
		Expect(after).To(Equal(before))
		var reused int
		Expect(s.db.QueryRow("SELECT count(*) FROM findings WHERE job_id=? AND reason='already_applied'", second).Scan(&reused)).To(Succeed())
		Expect(reused).To(Equal(effects + topics))
		for _, id := range []string{first, second} {
			var stored domain.ScanRun
			Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; stored, e = tx.ScanRun(id); return e })).To(Succeed())
			Expect(stored.Report.Overlays.Applied).To(Equal(effects + topics))
			Expect(stored.Report.Overlays.Proposed).To(BeZero())
		}
	} else {
		Expect(effects + topics).To(BeZero())
		Expect(applied).To(BeZero())
		if mode != "disabled" {
			Expect(proposed).To(BeNumerically(">", 0))
		}
	}
	if mode == "propose" && !restrict {
		// Same production scan captured all siblings before ANY owner mutation.
		var topics []domain.Finding
		var relation domain.Finding
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			findings, e := tx.JobFindings(domain.FindingFilter{JobID: first, Limit: 200}, "", "")
			if e != nil {
				return e
			}
			for _, saved := range findings {
				if saved.VersionID == nil || *saved.VersionID != a || saved.Status != "proposed" {
					continue
				}
				if saved.Kind == "topic_assignment" {
					topics = append(topics, saved)
				}
				if saved.Kind == "related_explanation" {
					relation = saved
				}
			}
			return nil
		})).To(Succeed())
		Expect(len(topics)).To(BeNumerically(">=", 3))
		Expect(relation.ID).NotTo(BeEmpty())
		calls := driver.executeCalls
		approve := func(saved domain.Finding) {
			response, e := core.FindingAction(ctx, owner.Token, f.id(), saved.ID, domain.FindingETag(saved), "approve", []byte(`{"decision":"approve"}`))
			Expect(e).NotTo(HaveOccurred())
			Expect(response.Status).To(Equal(200), string(response.Body))
		}
		approve(topics[0])
		rejected, e := core.FindingAction(ctx, owner.Token, f.id(), topics[2].ID, domain.FindingETag(topics[2]), "reject", []byte(`{"decision":"reject"}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(rejected.Status).To(Equal(200))
		approve(topics[1])
		approve(relation)
		Expect(driver.executeCalls).To(Equal(calls))
	}
	// Task05's actual guarded producer must honor an Undo across later scans.
	if mode == "auto" && !restrict {
		var chosen domain.ManagementEffect
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.ManagementEffects(domain.ManagementFilter{Kind: "topic_assignment"}, "", 200)
			if e != nil {
				return e
			}
			Expect(rows).NotTo(BeEmpty())
			chosen = rows[0]
			return nil
		})).To(Succeed())
		undoKey := f.id()
		response, e := core.EffectAction(ctx, owner.Token, undoKey, chosen.ID, chosen.Validator(), nil, "undone", []byte("{}"))
		Expect(e).NotTo(HaveOccurred())
		var result domain.FindingActionResult
		Expect(json.Unmarshal(response.Body, &result)).To(Succeed())
		Expect(result.Effect.Decision).To(Equal("undone"))
		third := run()
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			saved, e := tx.ManagementEffect(chosen.ID)
			if e != nil {
				return e
			}
			Expect(saved.Decision).To(Equal("undone"))
			findings, e := tx.JobFindings(domain.FindingFilter{JobID: third, Limit: 200}, "", "")
			if e != nil {
				return e
			}
			suppressed := false
			for _, f := range findings {
				if f.Kind == "topic_assignment" && f.VersionID != nil && *f.VersionID == chosen.From.VersionID {
					Expect(f.Status).To(Equal("rejected"))
					Expect(f.Reason).To(Equal("owner_decision_suppressed"))
					suppressed = true
				}
			}
			Expect(suppressed).To(BeTrue())
			links, e := tx.ManagementLinkedFindings(chosen.ID)
			if e != nil {
				return e
			}
			Expect(len(links)).To(BeNumerically(">=", 2))
			for _, l := range links {
				Expect(l.Active).To(BeFalse())
			}
			return nil
		})).To(Succeed())
		replay, e := core.EffectAction(ctx, owner.Token, undoKey, chosen.ID, chosen.Validator(), nil, "undone", []byte("{}"))
		Expect(e).NotTo(HaveOccurred())
		Expect(replay.Body).To(Equal(response.Body))
		// C01: actual StartScan admission -> ordinary retained-output producer ->
		// NEW equivalent approval-required finding -> separate current Undo CAS.
		var origin domain.Finding
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; origin, e = tx.Finding(chosen.FindingID); return e })).To(Succeed())
		request := domain.ScanRequest{Scope: "selected_projects", ProjectIDs: []string{project}, DocumentID: chosen.From.DocumentID, VersionID: chosen.From.VersionID, Depth: "deep_semantic", SourceMode: "pinned", FindingID: origin.ID, ExpectedFindingRevision: &origin.Revision}
		if chosen.From.PagePath != nil {
			request.PagePath = *chosen.From.PagePath
		}
		if chosen.From.RegionID != nil {
			request.RegionID = *chosen.From.RegionID
		}
		reassessed := runRequest(request)
		var fresh domain.Finding
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			run, e := tx.ScanRun(reassessed)
			if e != nil {
				return e
			}
			Expect(run.RequestedScope.FindingID).To(Equal(origin.ID))
			findings, e := tx.JobFindings(domain.FindingFilter{JobID: reassessed, Kind: "topic_assignment", Limit: 200}, "", "")
			if e != nil {
				return e
			}
			for _, candidate := range findings {
				key, _, e := scan.DecisionIdentity(tx, candidate)
				if e != nil {
					return e
				}
				if key == chosen.Key() {
					fresh = candidate
				}
			}
			Expect(fresh.ID).NotTo(BeEmpty())
			Expect(fresh.ID).NotTo(Equal(origin.ID))
			Expect(fresh.Status).To(Equal("proposed"))
			Expect(fresh.ApprovalRequired).To(BeTrue())
			basis, exists, e := tx.FindingDecisionBasis(fresh.ID)
			if e != nil {
				return e
			}
			Expect(exists).To(BeTrue())
			Expect(basis.Reassessment).To(BeTrue())
			memory, _, e := tx.FindingDecision(basis.Key)
			if e != nil {
				return e
			}
			Expect(memory.Action).To(Equal("undo"))
			Expect(memory.Revision).To(Equal(basis.OwnerRevision))
			Expect(memory.Material).To(Equal(basis.Material))
			effect, e := tx.ManagementEffect(chosen.ID)
			Expect(effect.Decision).To(Equal("undone"))
			return e
		})).To(Succeed())
		calls := driver.executeCalls
		accepted, e := core.FindingAction(ctx, owner.Token, f.id(), fresh.ID, domain.FindingETag(fresh), "approve", []byte(`{"decision":"approve"}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(accepted.Status).To(Equal(200))
		Expect(actionResult(accepted).Effect.ID).To(Equal(chosen.ID))
		Expect(driver.executeCalls).To(Equal(calls))
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			old, e := tx.Finding(origin.ID)
			Expect(old.Status).To(Equal("undone"))
			return e
		})).To(Succeed())
		replay, e = core.EffectAction(ctx, owner.Token, undoKey, chosen.ID, chosen.Validator(), nil, "undone", []byte("{}"))
		Expect(e).NotTo(HaveOccurred())
		Expect(replay.Body).To(Equal(response.Body))
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			effect, e := tx.ManagementEffect(chosen.ID)
			Expect(effect.Decision).To(Equal("accepted"))
			return e
		})).To(Succeed())
	}
	// Every actual auto application above, including the second run's reuse,
	// preserves the nonempty authored graph, physical objects and public bytes.
	Expect(originalInventory()).To(Equal(beforeInventory))
	Expect(publicBytes()).To(Equal(beforePublic))
	if mode == "auto" && !restrict {
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			for _, kind := range []string{"related_explanation", "topic_assignment"} {
				rows, e := tx.ManagementEffects(domain.ManagementFilter{Kind: kind}, "", 200)
				if e != nil {
					return e
				}
				Expect(rows).NotTo(BeEmpty())
				for _, m := range rows {
					m.DecisionActor = owner.Session.PrincipalID
					m.Revision++
					m.UpdatedAt = nowStamp()
					if e = tx.ReviseManagementEffect(m, m.Revision-1); e != nil {
						return e
					}
				}
			}
			return nil
		})).To(Succeed())
		Expect(originalInventory()).To(Equal(beforeInventory))
		Expect(publicBytes()).To(Equal(beforePublic))
	}
	var versions, drafts, reviews int
	Expect(s.db.QueryRow("SELECT count(*) FROM document_versions").Scan(&versions)).To(Succeed())
	Expect(versions).To(Equal(2))
	Expect(s.db.QueryRow("SELECT count(*) FROM drafts").Scan(&drafts)).To(Succeed())
	Expect(drafts).To(BeZero())
	Expect(s.db.QueryRow("SELECT count(*) FROM review_events").Scan(&reviews)).To(Succeed())
	Expect(reviews).To(BeZero())
	destination := os.Getenv("HAERYONG_TASK04_UI_FIXTURE")
	correctionFixture := os.Getenv("HAERYONG_TASK04_C01_UI_FIXTURE")
	if correctionFixture != "" {
		destination = correctionFixture
	}
	if destination != "" && mode == "auto" && !restrict {
		if correctionFixture != "" {
			// A NEW controlled old-writer fixture, never a mutation of retained
			// genuine records: a's topic audit is absent, b's stays canonical.
			_, e := s.db.Exec("UPDATE topic_assignments SET value=json_remove(value,'$.evidence_basis') WHERE version_id=?", a)
			Expect(e).NotTo(HaveOccurred())
			_, e = s.db.Exec("UPDATE management_effect_history SET value=json_remove(value,'$.evidence_basis') WHERE kind='topic_assignment' AND effect_id IN(SELECT effect_id FROM topic_assignments WHERE version_id=?)", a)
			Expect(e).NotTo(HaveOccurred())
			_, e = s.db.Exec("UPDATE findings SET proposal_json=json_remove(proposal_json,'$.evidence_basis') WHERE kind='topic_assignment' AND version_id=?", a)
			Expect(e).NotTo(HaveOccurred())
			// Unique ordinary objects avoid an unrelated shared-object GC pause.
			deleted := f.publish([]any{f.declaration(b, "controlled-only-purge")}, "<p id='retry'>Controlled deletion target. No source or model claim.</p>")
			f.run(deleted, "search")
			f.run(deleted, "references")
			m := managementFixtureEffect(f, a, deleted, "related_explanation")
			m.JobID = first
			Expect(s.Write(ctx, func(tx app.WriteTx) error { _, _, e := saveManagementFixture(tx, m); return e })).To(Succeed())
			v, e := s.Version(ctx, deleted)
			Expect(e).NotTo(HaveOccurred())
			applyDocumentLifecycle(s, auth, f.bytes, owner.Token, v.DocumentID, "trash")
			purged := applyDocumentLifecycle(s, auth, f.bytes, owner.Token, v.DocumentID, "purge")
			Expect(runPurgeTurns(s, f.bytes, purged.JobID, 64).State).To(Equal("completed"))
			page, e := query.New(s, auth, f.bytes).Management(ctx, owner.Token, domain.ManagementFilter{Kind: "related_explanation", VersionID: a}, query.Options{})
			Expect(e).NotTo(HaveOccurred())
			Expect(page.Items).To(HaveLen(2))
			unavailable := 0
			for _, row := range page.Items {
				if row.Availability == "unavailable" {
					unavailable++
				}
			}
			Expect(unavailable).To(Equal(1))
		}
		// Opt-in controlled offline pipeline artifact, never real-provider evidence.
		Expect(filepath.IsAbs(destination)).To(BeTrue())
		_, statErr := os.Lstat(destination)
		Expect(os.IsNotExist(statErr)).To(BeTrue())
		objects, e := contentstore.New(filepath.Join(w.Profile.DataDir, "storage"))
		Expect(e).NotTo(HaveOccurred())
		for _, body := range f.bytes.files {
			staged, e := objects.Stage(ctx, bytes.NewReader([]byte(body)))
			Expect(e).NotTo(HaveOccurred())
			_, e = objects.Finalize(ctx, staged, func(_ contentstore.Object, publish func() error) error { return publish() })
			Expect(e).NotTo(HaveOccurred())
		}
		Expect(objects.Close()).To(Succeed())
		// Also provide an explicitly controlled owner-approved metadata revision.
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.ManagementEffects(domain.ManagementFilter{Kind: "topic_assignment"}, "", 1)
			if e != nil {
				return e
			}
			Expect(rows).To(HaveLen(1))
			m := rows[0]
			m.Revision++
			m.DecisionActor = owner.Session.PrincipalID
			m.UpdatedAt = nowStamp()
			return tx.ReviseManagementEffect(m, m.Revision-1)
		})).To(Succeed())
		Expect(s.Close()).To(Succeed())
		Expect(w.Close()).To(Succeed())
		Expect(os.Mkdir(destination, 0700)).To(Succeed())
		Expect(os.CopyFS(filepath.Join(destination, "profile"), os.DirFS(w.Profile.DataDir))).To(Succeed())
		Expect(filepath.WalkDir(w.Profile.DataDir, func(source string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, e := entry.Info()
			if e != nil {
				return e
			}
			relative, e := filepath.Rel(w.Profile.DataDir, source)
			if e != nil {
				return e
			}
			return os.Chmod(filepath.Join(destination, "profile", relative), info.Mode().Perm())
		})).To(Succeed())
		Expect(os.WriteFile(filepath.Join(destination, "owner-token"), []byte(owner.Token), 0600)).To(Succeed())
		manifest := map[string]any{"class": "Controlled offline production pipeline and controlled repository owner-approved state; fake driver; no native/model call", "profile_dir": filepath.Join(destination, "profile"), "owner_token_file": filepath.Join(destination, "owner-token"), "versions": []string{a, b}, "auto_run": first, "origin": "ai", "owner_approval_route": "not implemented; repository seam only", "native_provider_calls": 0, "source_capture": 0, "quality_claim": false}
		if correctionFixture != "" {
			manifest["class"] = "NEW controlled offline production pipeline plus explicit missing-basis old-writer and repository-only unavailable fixtures; fake driver; no real model/source/semantic quality claim"
			manifest["correction_fixture"] = true
			manifest["legacy_topic_version"] = a
			manifest["canonical_topic_version"] = b
			manifest["unavailable_relation_focal_version"] = a
			manifest["nonempty_authored_reference_count"] = 1
			manifest["page_path"] = "page.html"
			manifest["region_id"] = "retry"
			Expect(os.MkdirAll(filepath.Join(destination, "profile", "runtime"), 0700)).To(Succeed())
			Expect(os.Chmod(filepath.Join(destination, "profile", "runtime"), 0700)).To(Succeed())
		}
		Expect(os.WriteFile(filepath.Join(destination, "manifest.json"), jsonBytes(manifest), 0600)).To(Succeed())
	}
}, Entry("auto with two real guarded offline scan runs", "auto", false), Entry("propose stores candidates only", "propose", false), Entry("disabled stores no category proposals", "disabled", false), Entry("current restriction prevents captured auto", "auto", true))
