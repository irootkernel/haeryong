package sqlite

import (
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/scan"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func c01Policy(f *referenceWorld, category, mode string) int64 {
	var revision int64
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var v map[string]any
		Expect(json.Unmarshal(cfg.Value, &v)).To(Succeed())
		v["maintenance"].(map[string]any)["auto_apply"].(map[string]any)[category] = mode
		cfg.Value = jsonBytes(v)
		if e = tx.SetConfiguration(cfg, cfg.Revision); e != nil {
			return e
		}
		cfg, e = tx.Configuration()
		revision = cfg.Revision
		return e
	})).To(Succeed())
	return revision
}
func c01Approve(f *referenceWorld, s *scan.Service, token string, record domain.Finding) domain.FindingActionResult {
	response, e := s.FindingAction(ctx, token, f.id(), record.ID, domain.FindingETag(record), "approve", []byte(`{"decision":"approve"}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(200), string(response.Body))
	return actionResult(response)
}

var _ = It("T00805 C01 independent pre-existing sibling topics and relation remain actionable", Label("persistence"), func() {
	f, s, token, a := decisionFixture("topic_assignment")
	b := a
	b.ID = f.id()
	b.Proposal = jsonBytes(map[string]string{"topic": "independent-topic", "evidence_basis": "explanation_text"})
	target := f.publish(nil, "<h1>Related retained explanation</h1>")
	var targetDoc string
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error { v, e := tx.PublishedVersion(target); targetDoc = v.DocumentID; return e })).To(Succeed())
	relation := a
	relation.ID = f.id()
	relation.Kind = "related_explanation"
	relation.Proposal = jsonBytes(map[string]string{"target_document_id": targetDoc, "target_version_id": target, "relation_type": "conceptual_analogy", "mechanism_key": "independent", "evidence_basis": "explanation_text"})
	saveDecisionTestFinding(f, a, false)
	saveDecisionTestFinding(f, b, false)
	saveDecisionTestFinding(f, relation, false)
	c01Approve(f, s, token, a)
	c01Approve(f, s, token, b)
	c01Approve(f, s, token, relation)
})
var _ = DescribeTable("T00805 C01 rejected Topic reactivation honors current policy and permissions", Label("persistence"), func(change string) {
	f, s, token, a := decisionFixture("topic_assignment")
	m := c01Approve(f, s, token, a).Effect
	pin := false
	r, e := s.EffectAction(ctx, token, f.id(), m.ID, m.ETag, &pin, "rejected", []byte(`{"pinned":false,"decision":"rejected"}`))
	Expect(e).NotTo(HaveOccurred())
	m = actionResult(r).Effect
	if change == "policy" {
		c01Policy(f, "topic_assignment", "disabled")
	} else {
		_, e = f.s.db.Exec("UPDATE projects SET permissions_revision=permissions_revision+1 WHERE project_id=?", a.ProjectID)
		Expect(e).NotTo(HaveOccurred())
	}
	r, e = s.EffectAction(ctx, token, f.id(), m.ID, m.ETag, &pin, "accepted", []byte(`{"pinned":false,"decision":"accepted"}`))
	if e == nil {
		Expect(r.Status).To(Equal(409))
	} else {
		var failure *domain.FindingActionError
		Expect(errors.As(e, &failure)).To(BeTrue())
		Expect(failure.Status).To(Equal(409))
	}
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		now, e := tx.ManagementEffect(m.ID)
		Expect(now.Decision).To(Equal("rejected"))
		Expect(now.Revision).To(Equal(m.Revision))
		return e
	})).To(Succeed())
}, Entry("policy", "policy"), Entry("permissions", "permissions"))
var _ = It("T00805 C01 owner-linked approval invalidates stale Undo and Topic PUT", Label("persistence"), func() {
	f, s, token, a := decisionFixture("topic_assignment")
	first := c01Approve(f, s, token, a).Effect
	b := a
	b.ID = f.id()
	saveDecisionTestFinding(f, b, false)
	second := c01Approve(f, s, token, b).Effect
	Expect(second.ETag).NotTo(Equal(first.ETag))
	ownerMemory := func() []string {
		values := []string{}
		for _, table := range []string{"management_decisions", "management_decision_history"} {
			rows, e := f.s.db.Query("SELECT value FROM " + table + " ORDER BY decision_key,revision")
			Expect(e).NotTo(HaveOccurred())
			for rows.Next() {
				var value string
				Expect(rows.Scan(&value)).To(Succeed())
				values = append(values, value)
			}
			Expect(rows.Err()).NotTo(HaveOccurred())
			Expect(rows.Close()).To(Succeed())
		}
		return values
	}
	afterSideApproval := ownerMemory()
	pin := false
	_, e := s.EffectAction(ctx, token, f.id(), first.ID, first.ETag, nil, "undone", []byte("{}"))
	Expect(e).To(MatchError(domain.ErrPrecondition))
	_, e = s.EffectAction(ctx, token, f.id(), first.ID, first.ETag, &pin, "rejected", []byte(`{"pinned":false,"decision":"rejected"}`))
	Expect(e).To(MatchError(domain.ErrPrecondition))
	Expect(ownerMemory()).To(Equal(afterSideApproval)) // includes hidden material/revisions/history
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		links, e := tx.ManagementLinkedFindings(first.ID)
		Expect(links).To(HaveLen(2))
		for _, l := range links {
			Expect(l.Active).To(BeTrue())
			Expect(l.EffectRevision).To(Equal(second.Revision))
		}
		return e
	})).To(Succeed())
	r, e := s.EffectAction(ctx, token, f.id(), second.ID, second.ETag, nil, "undone", []byte("{}"))
	Expect(e).NotTo(HaveOccurred())
	Expect(actionResult(r).Effect.Decision).To(Equal("undone"))
})
var _ = It("T00805 C01 manual new application retains capture and final config with owner reason and replay", Label("persistence"), func() {
	f, s, token, a := decisionFixture("topic_assignment")
	saveDecisionTestFinding(f, a, false)
	current := c01Policy(f, "topic_assignment", "propose")
	Expect(current).To(BeNumerically(">", a.CapturedConfigRevision))
	key := f.id()
	r, e := s.FindingAction(ctx, token, key, a.ID, domain.FindingETag(a), "approve", []byte(`{"decision":"approve"}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(r.Status).To(Equal(200))
	applied := actionResult(r)
	Expect(applied.Effect.CapturedConfigRevision).To(Equal(a.CapturedConfigRevision))
	Expect(applied.Effect.AppliedConfigRevision).To(Equal(current))
	Expect(applied.Finding.Reason).To(Equal("owner_approved"))
	replay, e := s.FindingAction(ctx, token, key, a.ID, domain.FindingETag(a), "approve", []byte(`{"decision":"approve"}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(replay.Body).To(Equal(r.Body))
})

var _ = DescribeTable("T00805 C01 current linked material survives owner Undo or rejection", Label("persistence"), func(action string) {
	f, s, token, a := decisionFixture("topic_assignment")
	initial := c01Approve(f, s, token, a).Effect
	support := f.publish(nil, "<h1>Material B retained support</h1>")
	b := a
	b.ID = f.id()
	b.Evidence = jsonBytes([]domain.FindingEvidence{{Kind: "explanation_context", VersionID: support, PagePath: "page.html"}})
	saveDecisionTestFinding(f, b, false)
	current := c01Approve(f, s, token, b).Effect
	Expect(current.ID).To(Equal(initial.ID))
	var materialB string
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		basis, ok, e := tx.FindingDecisionBasis(b.ID)
		Expect(ok).To(BeTrue())
		materialB = basis.Material
		return e
	})).To(Succeed())
	pin := false
	var r domain.Response
	var e error
	if action == "undo" {
		r, e = s.EffectAction(ctx, token, f.id(), current.ID, current.ETag, nil, "undone", []byte("{}"))
	} else {
		r, e = s.EffectAction(ctx, token, f.id(), current.ID, current.ETag, &pin, "rejected", []byte(`{"pinned":false,"decision":"rejected"}`))
	}
	Expect(e).NotTo(HaveOccurred())
	Expect(r.Status).To(Equal(200))
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		key, _, e := scan.DecisionIdentity(tx, b)
		if e != nil {
			return e
		}
		memory, ok, e := tx.FindingDecision(key)
		Expect(ok).To(BeTrue())
		Expect(memory.Material).To(Equal(materialB))
		for _, id := range []string{a.ID, b.ID} {
			saved, e := tx.Finding(id)
			if e != nil {
				return e
			}
			Expect(saved.Status).To(Equal("undone"))
		}
		return e
	})).To(Succeed())
}, Entry("Undo", "undo"), Entry("Topic rejection", "reject"))

var _ = It("T00805 C01 nonfocal project permission change during retained verification blocks approval", Label("persistence"), func() {
	f, s, token, a := decisionFixture("related_explanation")
	project := f.id()
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		return tx.RegisterProject(domain.Project{ID: project, Name: "C01 dependency"})
	})).To(Succeed())
	target := publishInProjectWithSources(f, project, nil, "<h1>Other project retained dependency</h1>", nil)
	var doc string
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		v, e := tx.PublishedVersion(target)
		if e != nil {
			return e
		}
		doc = v.DocumentID
		run, e := tx.ScanRun(a.JobID)
		if e != nil {
			return e
		}
		scope, e := tx.Scope(project)
		if e != nil {
			return e
		}
		run.PermissionsRevisions[project] = scope.Project.PermissionsRevision
		job, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		job.ID = f.id()
		job.State = ""
		job.Revision = 0
		if e = tx.CreateJob(job); e != nil {
			return e
		}
		run.JobID = job.ID
		run.ScopeHash = job.ID
		run.RequestedScopeJSON = nil
		run.EffectivePolicyJSON = nil
		run.ReportJSON = nil
		run.DenominatorJSON = nil
		if e = tx.CreateScanRun(run, nil); e != nil {
			return e
		}
		a.JobID = job.ID
		return nil
	})).To(Succeed())
	// A finding's job identity is immutable: create a NEW finding for the
	// lawful NEW dependency capture, rather than silently retaining the old job.
	a.ID = f.id()
	a.Proposal = jsonBytes(map[string]string{"target_document_id": doc, "target_version_id": target, "relation_type": "conceptual_analogy", "mechanism_key": "captured-other-project", "evidence_basis": "explanation_text"})
	saveDecisionTestFinding(f, a, false)
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		saved, e := tx.Finding(a.ID)
		if e != nil {
			return e
		}
		Expect(saved.JobID).To(Equal(a.JobID))
		run, e := tx.ScanRun(saved.JobID)
		if e != nil {
			return e
		}
		scope, e := tx.Scope(project)
		if e != nil {
			return e
		}
		Expect(run.PermissionsRevisions[project]).To(Equal(scope.Project.PermissionsRevision))
		return nil
	})).To(Succeed())
	changed := false
	f.bytes.onRead = func(string) {
		if changed {
			return
		}
		changed = true
		_, e := f.s.db.Exec("UPDATE projects SET permissions_revision=permissions_revision+1 WHERE project_id=?", project)
		Expect(e).NotTo(HaveOccurred())
	}
	r, e := s.FindingAction(ctx, token, f.id(), a.ID, domain.FindingETag(a), "approve", []byte(`{"decision":"approve"}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(changed).To(BeTrue())
	Expect(r.Status).To(Equal(409))
	Expect(string(r.Body)).To(ContainSubstring("source_authority_changed"))
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		k, _, e := scan.DecisionIdentity(tx, a)
		if e != nil {
			return e
		}
		_, found, e := tx.ManagementEffectByKey(k)
		Expect(found).To(BeFalse())
		return e
	})).To(Succeed())
})
