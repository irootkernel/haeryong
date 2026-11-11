package sqlite

import (
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/scan"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func decisionFixture(kind string) (*referenceWorld, *scan.Service, string, domain.Finding) {
	s, a, w, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), a, "Task05 decisions")
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
	v := f.publish(nil, "<h1>Complete retained explanation</h1>")
	target := f.publish(nil, "<h1>Related retained explanation</h1>")
	m := managementFixtureEffect(f, v, target, kind)
	var record domain.Finding
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var value map[string]any
		Expect(json.Unmarshal(cfg.Value, &value)).To(Succeed())
		maintenance := value["maintenance"].(map[string]any)
		maintenance["auto_apply"] = map[string]any{"backlink_index_repair": "propose", "related_explanation": "auto", "topic_assignment": "auto", "freshness_assessment": "propose"}
		cfg.Value = jsonBytes(value)
		if e = tx.SetConfiguration(cfg, cfg.Revision); e != nil {
			return e
		}
		cfg, e = tx.Configuration()
		if e != nil {
			return e
		}
		p, e := tx.Document(m.From.DocumentID)
		if e != nil {
			return e
		}
		scope, e := tx.Scope(p.ProjectID)
		if e != nil {
			return e
		}
		policy, e := scan.ResolveEffectivePolicy(cfg, []string{p.ProjectID}, "full_scope")
		if e != nil {
			return e
		}
		if e = tx.CreateJob(domain.Job{ID: m.JobID, Kind: "scan", Scope: jsonBytes(map[string]any{"principal_id": owner.Session.PrincipalID, "document_id": p.ID, "version_id": v, "project_ids": []string{p.ProjectID}})}); e != nil {
			return e
		}
		if e = tx.CreateScanRun(domain.ScanRun{JobID: m.JobID, PrincipalID: owner.Session.PrincipalID, AuthKind: "owner", ScopeHash: m.JobID, ConfigRevision: cfg.Revision, PermissionsRevisions: map[string]int64{p.ProjectID: scope.Project.PermissionsRevision}, RequestedScope: domain.ScanRequestedScope{DocumentID: p.ID, DocumentIDs: []string{p.ID}, VersionID: v, ProjectIDs: []string{p.ProjectID}, Depth: "full_scope"}, EffectivePolicy: policy, CapturedAt: nowStamp(), UpdatedAt: nowStamp()}, nil); e != nil {
			return e
		}
		proposal := map[string]any{"topic": m.Topic, "evidence_basis": "explanation_text", "relation_type": m.RelationType, "mechanism_key": m.MechanismKey}
		if m.Target != nil {
			proposal["target_document_id"] = m.Target.DocumentID
			proposal["target_version_id"] = m.Target.VersionID
		}
		record = domain.Finding{ID: m.FindingID, JobID: m.JobID, ProjectID: p.ProjectID, DocumentID: &p.ID, VersionID: &v, Kind: kind, Origin: "ai", Evidence: json.RawMessage("[]"), EvidenceFingerprint: "producer-old", Proposal: jsonBytes(proposal), Status: "proposed", ApprovalRequired: true, Rationale: "Exact mechanical controlled proposal", CapturedConfigRevision: cfg.Revision, PermissionsRevision: scope.Project.PermissionsRevision, Revision: 1, CreatedAt: nowStamp(), UpdatedAt: nowStamp()}
		return tx.PutFinding(record)
	})).To(Succeed())
	return f, scan.New(s, a, f.bytes, nil), owner.Token, record
}
func actionResult(response domain.Response) domain.FindingActionResult {
	var out domain.FindingActionResult
	Expect(json.Unmarshal(response.Body, &out)).To(Succeed())
	return out
}

var _ = DescribeTable("T00805 owner approval Undo memory and all-linked revisions", Label("persistence"), func(kind string) {
	f, s, token, first := decisionFixture(kind)
	response, e := s.FindingAction(ctx, token, f.id(), first.ID, domain.FindingETag(first), "approve", jsonBytes(map[string]string{"decision": "approve"}))
	Expect(e).NotTo(HaveOccurred())
	r := actionResult(response)
	Expect(r.Finding.Status).To(Equal("applied"))
	Expect(r.Effect.Origin).To(Equal("ai"))
	Expect(r.Effect.DecisionActor).NotTo(Equal("auto"))
	// A distinct saved finding joins the same global effect, with its own basis.
	second := first
	second.ID = f.id()
	second.Revision = 1
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.PutFinding(second); e != nil {
			return e
		}
		k, m, e := scan.DecisionIdentity(tx, second)
		if e != nil {
			return e
		}
		d, _, e := tx.FindingDecision(k)
		if e != nil {
			return e
		}
		rev, e := tx.TargetOwnerRevision(*second.VersionID)
		if e != nil {
			return e
		}
		return tx.SaveFindingDecisionBasis(domain.FindingDecisionBasis{FindingID: second.ID, Key: k, Material: m, OwnerRevision: d.Revision, TargetRevision: rev})
	})).To(Succeed())
	response, e = s.FindingAction(ctx, token, f.id(), second.ID, domain.FindingETag(second), "approve", jsonBytes(map[string]string{"decision": "approve"}))
	Expect(e).NotTo(HaveOccurred())
	r2 := actionResult(response)
	Expect(r2.Effect.ID).To(Equal(r.Effect.ID))
	undoKey := f.id()
	oldTag := r2.Effect.ETag
	response, e = s.EffectAction(ctx, token, undoKey, r.Effect.ID, oldTag, nil, "undone", []byte("{}"))
	Expect(e).NotTo(HaveOccurred())
	undone := actionResult(response)
	Expect(undone.Effect.Decision).To(Equal("undone"))
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		links, e := tx.ManagementLinkedFindings(r.Effect.ID)
		if e != nil {
			return e
		}
		Expect(links).To(HaveLen(2))
		for _, l := range links {
			Expect(l.Active).To(BeFalse())
			saved, e := tx.Finding(l.FindingID)
			if e != nil {
				return e
			}
			Expect(saved.Status).To(Equal("undone"))
			Expect(saved.Revision).To(Equal(l.FindingRevision))
		}
		key, _, e := scan.DecisionIdentity(tx, first)
		if e != nil {
			return e
		}
		d, yes, e := tx.FindingDecision(key)
		if e != nil {
			return e
		}
		Expect(yes).To(BeTrue())
		Expect(d.Action).To(Equal("undo"))
		return nil
	})).To(Succeed())
	// Decision history retains every previous application reference.
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		k, _, e := scan.DecisionIdentity(tx, first)
		if e != nil {
			return e
		}
		d, _, e := tx.FindingDecision(k)
		if e != nil {
			return e
		}
		Expect(d.Links).To(HaveLen(2))
		for _, prior := range d.Links {
			Expect(prior.Status).To(Equal("applied"))
			Expect(prior.AppliedRef).NotTo(BeEmpty())
		}
		return nil
	})).To(Succeed())
	// Reopen the actual DB: suppression, history, current links and receipt must
	// survive storage restart; this is not a GUI/process-recovery qualification.
	Expect(f.s.Close()).To(Succeed())
	f.s = openStore(GinkgoT(), f.writer, nil)
	s.Store = f.s
	s.Auth = appauth.New(f.s)
	// Same operation replays despite original now-stale overlay validator.
	replay, e := s.EffectAction(ctx, token, undoKey, r.Effect.ID, oldTag, nil, "undone", []byte("{}"))
	Expect(e).NotTo(HaveOccurred())
	Expect(replay.Body).To(Equal(response.Body))
	if kind == "topic_assignment" {
		for _, body := range []struct {
			Pin      bool
			Decision string
		}{{false, "accepted"}, {true, "accepted"}, {false, "rejected"}} {
			_, e = s.EffectAction(ctx, token, f.id(), r.Effect.ID, undone.Effect.ETag, &body.Pin, body.Decision, jsonBytes(map[string]any{"pinned": body.Pin, "decision": body.Decision}))
			var ae *domain.FindingActionError
			Expect(errors.As(e, &ae)).To(BeTrue())
			Expect(ae.Code).To(Equal("INVALID_TRANSITION"))
			Expect(ae.Reason).To(Equal("undone_requires_reassessment_approve"))
		}
	}
	// A NEW owner-bound reassessment, then a separate approval, supersedes Undo.
	var old domain.Finding
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error { var e error; old, e = tx.Finding(first.ID); return e })).To(Succeed())
	request := domain.ScanRequest{Scope: "selected_projects", ProjectIDs: []string{first.ProjectID}, DocumentID: *first.DocumentID, VersionID: *first.VersionID, Depth: "full_scope", SourceMode: "pinned", FindingID: first.ID, ExpectedFindingRevision: &old.Revision}
	started, e := s.StartScan(ctx, token, f.id(), jsonBytes(request), request)
	Expect(e).NotTo(HaveOccurred())
	Expect(started.Status).To(Equal(202))
	var accepted struct {
		JobID string `json:"job_id"`
	}
	Expect(json.Unmarshal(started.Body, &accepted)).To(Succeed())
	third := first
	third.ID = f.id()
	third.JobID = accepted.JobID
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		run, e := tx.ScanRun(accepted.JobID)
		if e != nil {
			return e
		}
		Expect(run.RequestedScope.FindingID).To(Equal(first.ID))
		Expect(*run.RequestedScope.ExpectedFindingRevision).To(Equal(old.Revision))
		if e = tx.PutFinding(third); e != nil {
			return e
		}
		k, m, e := scan.DecisionIdentity(tx, third)
		if e != nil {
			return e
		}
		d, _, e := tx.FindingDecision(k)
		if e != nil {
			return e
		}
		rev, e := tx.TargetOwnerRevision(*third.VersionID)
		if e != nil {
			return e
		}
		return tx.SaveFindingDecisionBasis(domain.FindingDecisionBasis{FindingID: third.ID, Key: k, Material: m, OwnerRevision: d.Revision, TargetRevision: rev, Reassessment: true})
	})).To(Succeed())
	reapplied, e := s.FindingAction(ctx, token, f.id(), third.ID, domain.FindingETag(third), "approve", []byte(`{"decision":"approve"}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(actionResult(reapplied).Effect.ID).To(Equal(r.Effect.ID))
	Expect(actionResult(reapplied).Effect.Decision).To(Equal("accepted"))
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		for _, id := range []string{first.ID, second.ID} {
			saved, e := tx.Finding(id)
			if e != nil {
				return e
			}
			Expect(saved.Status).To(Equal("undone"))
		}
		links, e := tx.ManagementLinkedFindings(r.Effect.ID)
		if e != nil {
			return e
		}
		active := 0
		for _, l := range links {
			if l.Active {
				active++
			}
		}
		Expect(active).To(Equal(1))
		return nil
	})).To(Succeed())
	afterReplay, e := s.EffectAction(ctx, token, undoKey, r.Effect.ID, oldTag, nil, "undone", []byte("{}"))
	Expect(e).NotTo(HaveOccurred())
	Expect(afterReplay.Body).To(Equal(response.Body))
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		m, e := tx.ManagementEffect(r.Effect.ID)
		Expect(m.Decision).To(Equal("accepted"))
		return e
	})).To(Succeed())
}, Entry("relation", "related_explanation"), Entry("topic/F4", "topic_assignment"))

var _ = It("T00805 propose-only reject persists cross-run stable material identity and stale individual writes cannot overwrite it", Label("persistence"), func() {
	f, s, token, record := decisionFixture("topic_assignment")
	key := f.id()
	response, e := s.FindingAction(ctx, token, key, record.ID, domain.FindingETag(record), "reject", []byte(`{"decision":"reject"}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(actionResult(response).Finding.Status).To(Equal("rejected"))
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		k, m, e := scan.DecisionIdentity(tx, record)
		if e != nil {
			return e
		}
		other := record
		other.ID = f.id()
		other.JobID = f.id()
		other.EvidenceFingerprint = "another-run-id"
		k2, m2, e := scan.DecisionIdentity(tx, other)
		if e != nil {
			return e
		}
		Expect(k2).To(Equal(k))
		Expect(m2).To(Equal(m))
		decision, yes, e := tx.FindingDecision(k)
		if e != nil {
			return e
		}
		Expect(yes).To(BeTrue())
		Expect(decision.Action).To(Equal("reject"))
		Expect(decision.Material).To(Equal(m))
		_, exists, e := tx.ManagementEffectByKey(k)
		Expect(exists).To(BeFalse())
		return e
	})).To(Succeed())
	_, e = s.FindingAction(ctx, token, f.id(), record.ID, domain.FindingETag(record), "approve", []byte(`{"decision":"approve"}`))
	Expect(e).To(MatchError(domain.ErrPrecondition))
})
