package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/scan"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func saveDecisionTestFinding(f *referenceWorld, record domain.Finding, reassessment bool) {
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.PutFinding(record); e != nil {
			return e
		}
		key, material, e := scan.DecisionIdentity(tx, record)
		if e != nil {
			return e
		}
		decision, _, e := tx.FindingDecision(key)
		if e != nil {
			return e
		}
		revision, e := tx.TargetOwnerRevision(*record.VersionID)
		if e != nil {
			return e
		}
		return tx.SaveFindingDecisionBasis(domain.FindingDecisionBasis{FindingID: record.ID, Key: key, Material: material, OwnerRevision: decision.Revision, TargetRevision: revision, Reassessment: reassessment})
	})).To(Succeed())
}

type decisionLinkFaultStore struct{ app.Store }
type decisionLinkFaultTx struct {
	app.WriteTx
	links int
}

var errDecisionLinkFault = errors.New("controlled second linked write failure")

func (t *decisionLinkFaultTx) SaveManagementLink(l domain.FindingEffectLink) error {
	t.links++
	if t.links == 2 {
		return errDecisionLinkFault
	}
	return t.WriteTx.SaveManagementLink(l)
}
func (s decisionLinkFaultStore) Write(c context.Context, apply func(app.WriteTx) error) error {
	return s.Store.Write(c, func(tx app.WriteTx) error { return apply(&decisionLinkFaultTx{WriteTx: tx}) })
}

var _ = It("T00805 rollback includes every linked finding effect memory and receipt", Label("persistence"), func() {
	f, s, token, first := decisionFixture("related_explanation")
	response, e := s.FindingAction(ctx, token, f.id(), first.ID, domain.FindingETag(first), "approve", []byte(`{"decision":"approve"}`))
	Expect(e).NotTo(HaveOccurred())
	effect := actionResult(response).Effect
	second := first
	second.ID = f.id()
	saveDecisionTestFinding(f, second, false)
	joined, e := s.FindingAction(ctx, token, f.id(), second.ID, domain.FindingETag(second), "approve", []byte(`{"decision":"approve"}`))
	Expect(e).NotTo(HaveOccurred())
	effect = actionResult(joined).Effect
	var before domain.FindingDecision
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		key, _, e := scan.DecisionIdentity(tx, first)
		if e != nil {
			return e
		}
		before, _, e = tx.FindingDecision(key)
		return e
	})).To(Succeed())
	s.Store = decisionLinkFaultStore{Store: f.s}
	op := f.id()
	_, e = s.EffectAction(ctx, token, op, effect.ID, effect.ETag, nil, "undone", []byte("{}"))
	Expect(e).To(MatchError(errDecisionLinkFault))
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		current, e := tx.ManagementEffect(effect.ID)
		if e != nil {
			return e
		}
		Expect(current.Decision).To(Equal("accepted"))
		Expect(current.Revision).To(Equal(effect.Revision))
		links, e := tx.ManagementLinkedFindings(effect.ID)
		if e != nil {
			return e
		}
		Expect(links).To(HaveLen(2))
		for _, link := range links {
			Expect(link.Active).To(BeTrue())
			record, e := tx.Finding(link.FindingID)
			if e != nil {
				return e
			}
			Expect(record.Status).To(Equal("applied"))
			Expect(record.Revision).To(Equal(link.FindingRevision))
		}
		after, _, e := tx.FindingDecision(before.Key)
		if e != nil {
			return e
		}
		Expect(after).To(Equal(before))
		auth, e := tx.AuthState()
		if e != nil {
			return e
		}
		_, e = tx.Receipt(domain.OperationKey{PrincipalID: auth.OwnerID, OperationID: op})
		Expect(e).To(MatchError(domain.ErrNotFound))
		return nil
	})).To(Succeed())
})
var _ = It("T00805 current pin and Topic rejection preserve legal linked history and obsolete classifications", Label("persistence"), func() {
	f, s, token, first := decisionFixture("topic_assignment")
	response, e := s.FindingAction(ctx, token, f.id(), first.ID, domain.FindingETag(first), "approve", []byte(`{"decision":"approve"}`))
	Expect(e).NotTo(HaveOccurred())
	effect := actionResult(response).Effect
	pending := first
	pending.ID = f.id()
	pending.Proposal = first.Proposal // same exact assignment; pin changes its owner basis
	saveDecisionTestFinding(f, pending, false)
	pin := true
	response, e = s.EffectAction(ctx, token, f.id(), effect.ID, effect.ETag, &pin, "accepted", []byte(`{"pinned":true,"decision":"accepted"}`))
	Expect(e).NotTo(HaveOccurred())
	pinned := actionResult(response).Effect
	_, e = s.EffectAction(ctx, token, f.id(), effect.ID, effect.ETag, nil, "undone", []byte("{}"))
	Expect(e).To(MatchError(domain.ErrPrecondition))
	_, e = s.EffectAction(ctx, token, f.id(), effect.ID, pinned.ETag, nil, "undone", []byte("{}"))
	var failure *domain.FindingActionError
	Expect(errors.As(e, &failure)).To(BeTrue())
	Expect(failure.Reason).To(Equal("effect_not_undoable"))
	op := f.id()
	refusal, e := s.FindingAction(ctx, token, op, pending.ID, domain.FindingETag(pending), "approve", []byte(`{"decision":"approve"}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(refusal.Status).To(Equal(409))
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		saved, e := tx.Finding(pending.ID)
		if e != nil {
			return e
		}
		Expect(saved.Status).To(Equal("stale"))
		a, e := tx.AuthState()
		if e != nil {
			return e
		}
		receipt, e := tx.Receipt(domain.OperationKey{PrincipalID: a.OwnerID, OperationID: op})
		Expect(receipt.State).To(Equal("failed"))
		return e
	})).To(Succeed())
	replay, e := s.FindingAction(ctx, token, op, pending.ID, domain.FindingETag(pending), "approve", []byte(`{"decision":"approve"}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(replay.Body).To(Equal(refusal.Body))
	pin = false
	response, e = s.EffectAction(ctx, token, f.id(), effect.ID, pinned.ETag, &pin, "rejected", []byte(`{"pinned":false,"decision":"rejected"}`))
	Expect(e).NotTo(HaveOccurred())
	rejected := actionResult(response).Effect
	response, e = s.EffectAction(ctx, token, f.id(), effect.ID, rejected.ETag, &pin, "accepted", []byte(`{"pinned":false,"decision":"accepted"}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(actionResult(response).Effect.Decision).To(Equal("accepted"))
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		saved, e := tx.Finding(first.ID)
		if e != nil {
			return e
		}
		Expect(saved.Status).To(Equal("undone"))
		links, e := tx.ManagementLinkedFindings(effect.ID)
		for _, link := range links {
			Expect(link.Active).To(BeFalse())
			Expect(link.ValidHistory).To(BeTrue())
		}
		return e
	})).To(Succeed())
})
var _ = It("T00805 policy changes during pinned retained-byte verification commit blocked failure without effect", Label("persistence"), func() {
	f, s, token, record := decisionFixture("related_explanation")
	changed := false
	f.bytes.onRead = func(string) {
		if changed {
			return
		}
		changed = true
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			cfg, e := tx.Configuration()
			if e != nil {
				return e
			}
			var value map[string]any
			Expect(json.Unmarshal(cfg.Value, &value)).To(Succeed())
			value["maintenance"].(map[string]any)["auto_apply"].(map[string]any)["related_explanation"] = "disabled"
			cfg.Value = jsonBytes(value)
			return tx.SetConfiguration(cfg, cfg.Revision)
		})).To(Succeed())
	}
	op := f.id()
	response, e := s.FindingAction(ctx, token, op, record.ID, domain.FindingETag(record), "approve", []byte(`{"decision":"approve"}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(409))
	Expect(changed).To(BeTrue())
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		saved, e := tx.Finding(record.ID)
		if e != nil {
			return e
		}
		Expect(saved.Status).To(Equal("blocked"))
		Expect(saved.Reason).To(Equal("policy_disabled"))
		key, _, e := scan.DecisionIdentity(tx, record)
		if e != nil {
			return e
		}
		_, exists, e := tx.ManagementEffectByKey(key)
		Expect(exists).To(BeFalse())
		return e
	})).To(Succeed())
	var pins int
	Expect(f.s.db.QueryRow("SELECT count(*) FROM object_pins WHERE owner_kind='finding_decision'").Scan(&pins)).To(Succeed())
	Expect(pins).To(BeZero())
	replay, e := s.FindingAction(ctx, token, op, record.ID, domain.FindingETag(record), "approve", []byte(`{"decision":"approve"}`))
	Expect(e).NotTo(HaveOccurred())
	Expect(replay.Body).To(Equal(response.Body))
})
var _ = It("T00805 exact finding origin and revision survive coalescing replay and stale admission", Label("persistence"), func() {
	f, s, token, first := decisionFixture("topic_assignment")
	revision := first.Revision
	request := domain.ScanRequest{Scope: "selected_projects", ProjectIDs: []string{first.ProjectID}, DocumentID: *first.DocumentID, VersionID: *first.VersionID, Depth: "full_scope", SourceMode: "pinned", FindingID: first.ID, ExpectedFindingRevision: &revision}
	admit := func(key string, r domain.ScanRequest) domain.Response {
		response, e := s.StartScan(ctx, token, key, jsonBytes(r), r)
		Expect(e).NotTo(HaveOccurred())
		return response
	}
	jobID := func(r domain.Response) string {
		var v struct {
			JobID string `json:"job_id"`
		}
		Expect(json.Unmarshal(r.Body, &v)).To(Succeed())
		return v.JobID
	}
	op := f.id()
	one := admit(op, request)
	same := admit(f.id(), request)
	Expect(jobID(same)).To(Equal(jobID(one)))
	second := first
	second.ID = f.id()
	saveDecisionTestFinding(f, second, false)
	other := request
	other.FindingID = second.ID
	Expect(jobID(admit(f.id(), other))).NotTo(Equal(jobID(one)))
	unbound := request
	unbound.FindingID = ""
	unbound.ExpectedFindingRevision = nil
	Expect(jobID(admit(f.id(), unbound))).NotTo(Equal(jobID(one)))
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		updated := first
		updated.Revision++
		updated.Status = "rejected"
		return tx.ReviseFinding(updated, first.Revision)
	})).To(Succeed())
	Expect(admit(op, request).Body).To(Equal(one.Body))
	_, e := s.StartScan(ctx, token, f.id(), jsonBytes(request), request)
	Expect(e).To(MatchError(domain.ErrPrecondition))
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		run, e := tx.ScanRun(jobID(one))
		if e != nil {
			return e
		}
		Expect(run.RequestedScope.FindingID).To(Equal(first.ID))
		Expect(*run.RequestedScope.ExpectedFindingRevision).To(Equal(revision))
		return nil
	})).To(Succeed())
})
