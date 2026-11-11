package scan

import (
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// Called only after ApplyMaintenance's current input, scope, lifecycle and policy
// fences. This adds no provider operation or semantic interpretation.
func storeManagementFinding(tx app.WriteTx, f *domain.Finding, mode string, config int64, now string) error {
	if f.Kind != "related_explanation" && f.Kind != "topic_assignment" || mode != "auto" {
		return tx.PutFinding(*f)
	}
	var p struct {
		TargetDocumentID string `json:"target_document_id"`
		TargetVersionID  string `json:"target_version_id"`
		TargetPagePath   string `json:"target_page_path"`
		TargetRegionID   string `json:"target_region_id"`
		RelationType     string `json:"relation_type"`
		MechanismKey     string `json:"mechanism_key"`
		EvidenceBasis    string `json:"evidence_basis"`
		Topic            string `json:"topic"`
	}
	if err := json.Unmarshal(f.Proposal, &p); err != nil {
		return err
	}
	id, err := service.NewID()
	if err != nil {
		return err
	}
	if f.DocumentID == nil || f.VersionID == nil {
		return domain.ErrInvalidTarget
	}
	m := domain.ManagementEffect{ID: id, Kind: f.Kind, FindingID: f.ID, JobID: f.JobID, From: domain.ManagementAnchor{DocumentID: *f.DocumentID, VersionID: *f.VersionID, PagePath: f.PagePath, RegionID: f.RegionID}, Origin: f.Origin, EvidenceBasis: p.EvidenceBasis, Evidence: f.Evidence, EvidenceFingerprint: f.EvidenceFingerprint, Rationale: f.Rationale, Uncertainty: f.Uncertainty, TargetSetID: f.TargetSetID, CapturedConfigRevision: f.CapturedConfigRevision, AppliedConfigRevision: config, PermissionsRevision: f.PermissionsRevision, Decision: "accepted", DecisionActor: "auto", Revision: 1, CreatedAt: now, UpdatedAt: now}
	if f.DecidedBy != nil {
		m.DecisionActor = *f.DecidedBy
	}
	if f.Kind == "related_explanation" {
		m.Target = &domain.ManagementAnchor{DocumentID: p.TargetDocumentID, VersionID: p.TargetVersionID, PagePath: nullableStr(p.TargetPagePath), RegionID: nullableStr(p.TargetRegionID)}
		m.RelationType = p.RelationType
		m.MechanismKey = p.MechanismKey
	} else {
		m.Topic = p.Topic
		m.TopicID = domain.ManagementDigest(p.Topic)
	}
	effect, created, err := tx.PutManagementEffect(m)
	if errors.Is(err, domain.ErrConflict) {
		f.Status = "blocked"
		f.Reason = "owner_decision_conflict"
		return tx.PutFinding(*f)
	}
	if err != nil {
		return err
	}
	f.Status = "applied"
	f.ApprovalRequired = false
	f.Reason = "policy_auto_applied"
	if f.DecidedBy != nil && *f.DecidedBy != "auto" {
		f.Reason = "owner_approved"
	}
	if !created {
		f.Reason = "already_applied"
	}
	f.AppliedRef, _ = json.Marshal(map[string]any{"overlay_id": effect.ID, "kind": effect.Kind, "revision": effect.Revision})
	if f.DecidedBy == nil {
		f.DecidedBy = freshPtr("auto")
	}
	f.DecidedAt = freshPtr(now)
	if err = tx.PutFinding(*f); err != nil {
		return err
	}
	if err = tx.LinkManagementFinding(*f, effect); err != nil {
		return err
	}
	if created {
		event := "overlay.changed"
		if f.Kind == "topic_assignment" {
			event = "topic_assignment.changed"
		}
		return tx.AppendEvent("finding", f.ID, event, effect.Revision, []byte(`{"finding_id":"`+f.ID+`","overlay_id":"`+effect.ID+`"}`))
	}
	return nil
}
