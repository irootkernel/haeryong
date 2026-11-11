package scan

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"time"

	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// Finding actions use canonical rows and the existing atomic receipt writer.
func actionError(status int, code, reason string) error {
	return &domain.FindingActionError{Status: status, Code: code, Reason: reason}
}
func actionJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func decisionFailure(status int, code, reason string) domain.Response {
	return domain.Response{Status: status, Body: actionJSON(map[string]any{"error": map[string]any{"code": code, "message": code, "retryable": false, "details": map[string]string{"reason": reason}}})}
}
func managementCandidate(f domain.Finding) (domain.ManagementEffect, error) {
	var p struct {
		TargetDocumentID string `json:"target_document_id"`
		TargetVersionID  string `json:"target_version_id"`
		TargetPagePath   string `json:"target_page_path"`
		TargetRegionID   string `json:"target_region_id"`
		RelationType     string `json:"relation_type"`
		MechanismKey     string `json:"mechanism_key"`
		Topic            string `json:"topic"`
	}
	if json.Unmarshal(f.Proposal, &p) != nil || f.DocumentID == nil || f.VersionID == nil {
		return domain.ManagementEffect{}, domain.ErrInvalidTarget
	}
	m := domain.ManagementEffect{Kind: f.Kind, From: domain.ManagementAnchor{DocumentID: *f.DocumentID, VersionID: *f.VersionID, PagePath: f.PagePath, RegionID: f.RegionID}, Topic: p.Topic, TopicID: domain.ManagementDigest(p.Topic), RelationType: p.RelationType, MechanismKey: p.MechanismKey}
	if f.Kind == "related_explanation" {
		m.Target = &domain.ManagementAnchor{DocumentID: p.TargetDocumentID, VersionID: p.TargetVersionID, PagePath: nullableStr(p.TargetPagePath), RegionID: nullableStr(p.TargetRegionID)}
	}
	return m, nil
}
func materialValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, v := range x {
			switch k {
			case "job_id", "finding_id", "assessment_id", "coverage_set_id", "target_set_id", "read_id", "verification_id", "attempt_id", "captured_at", "assessed_at", "verified_at", "checked_at", "created_at", "updated_at", "rationale", "uncertainty", "config_revision", "permissions_revision", "shortlist_fingerprint", "captured_read_id", "source_capture_id", "owner_decision_revisions":
				continue
			}
			out[k] = materialValue(v)
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, v := range x {
			out = append(out, materialValue(v))
		}
		sort.Slice(out, func(i, j int) bool { return string(actionJSON(out[i])) < string(actionJSON(out[j])) })
		return out
	}
	return v
}
func DecisionIdentity(tx app.WriteTx, f domain.Finding) (string, string, error) {
	var proposal, evidence any
	if json.Unmarshal(f.Proposal, &proposal) != nil || json.Unmarshal(f.Evidence, &evidence) != nil {
		return "", "", domain.ErrReconciliation
	}
	key := domain.ManagementDigest([]any{f.Kind, f.VersionID, f.PagePath, f.RegionID, materialValue(proposal)})
	if f.Kind == "related_explanation" || f.Kind == "topic_assignment" {
		m, e := managementCandidate(f)
		if e != nil {
			return "", "", e
		}
		key = m.Key()
	}
	if f.Kind == "freshness_assessment" {
		var a domain.Assessment
		if json.Unmarshal(f.Proposal, &a) != nil {
			return "", "", domain.ErrReconciliation
		}
		var scope any
		if json.Unmarshal(a.ScopeJSON, &scope) != nil {
			return "", "", domain.ErrReconciliation
		}
		key = domain.ManagementDigest([]any{f.Kind, f.VersionID, scope})
	}
	material := []any{"decision-evidence-v1", f.Kind, f.VersionID, f.PagePath, f.RegionID, materialValue(evidence)}
	for _, id := range decisionVersions(f) {
		v, e := tx.PublishedVersion(id)
		if e != nil {
			return "", "", e
		}
		material = append(material, []string{id, v.ManifestSHA, v.PayloadSHA, v.MetadataHash})
	}
	if f.TargetSetID != nil && *f.TargetSetID != "" {
		ts, e := tx.TargetSetByID(*f.TargetSetID)
		if e != nil {
			return "", "", e
		}
		var targets any
		if json.Unmarshal(ts.TargetsJSON, &targets) != nil {
			return "", "", domain.ErrReconciliation
		}
		// Resolved targets and inspected evidence define novelty; a request mode
		// or another allocated capture identity cannot dismiss the owner choice.
		material = append(material, materialValue(targets))
	}
	return key, domain.ManagementDigest(material), nil
}
func decisionVersions(f domain.Finding) []string {
	set := map[string]bool{}
	if f.VersionID != nil {
		set[*f.VersionID] = true
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				if k == "version_id" || k == "target_version_id" || k == "source_version_id" {
					if id, ok := v.(string); ok && id != "" {
						set[id] = true
					}
				}
				walk(v)
			}
		case []any:
			for _, v := range x {
				walk(v)
			}
		}
	}
	for _, raw := range []json.RawMessage{f.Proposal, f.Evidence} {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			walk(v)
		}
	}
	out := []string{}
	for id := range set {
		if id != "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Called at production proposal creation, before any automatic materialization.
func captureDecisionBasis(tx app.WriteTx, f domain.Finding) (domain.FindingDecisionBasis, error) {
	if b, ok, e := tx.FindingDecisionBasis(f.ID); e != nil || ok {
		return b, e
	}
	key, material, e := DecisionIdentity(tx, f)
	if e != nil {
		return domain.FindingDecisionBasis{}, e
	}
	d, _, e := tx.FindingDecision(key)
	if e != nil {
		return domain.FindingDecisionBasis{}, e
	}
	run, e := tx.ScanRun(f.JobID)
	if e != nil {
		return domain.FindingDecisionBasis{}, e
	}
	ownerRevision := d.Revision
	if run.RequestedScope.OwnerBasisCaptured || run.RequestedScope.OwnerRevisions != nil {
		ownerRevision = run.RequestedScope.OwnerRevisions[key]
	}
	return domain.FindingDecisionBasis{FindingID: f.ID, Key: key, Material: material, OwnerRevision: ownerRevision, Reassessment: run.RequestedScope.FindingID != ""}, nil
}
func saveDecisionFinding(tx app.WriteTx, f *domain.Finding, mode string, config int64, now string) error {
	b, e := captureDecisionBasis(tx, *f)
	if e != nil {
		return e
	}
	d, found, e := tx.FindingDecision(b.Key)
	if e != nil {
		return e
	}
	if found && d.Revision != b.OwnerRevision {
		f.Status = "stale"
		f.Reason = "owner_decision_changed"
		f.ApprovalRequired = false
		if e = tx.PutFinding(*f); e != nil {
			return e
		}
		return tx.SaveFindingDecisionBasis(b)
	}
	if found && (d.Action == "reject" || d.Action == "undo") {
		mode = "propose"
		f.ApprovalRequired = true
		if decisionMaterialMatches(d, b.Material) && !(d.Action == "undo" && b.Reassessment) {
			f.Status = "rejected"
			f.ApprovalRequired = false
			f.Reason = "owner_decision_suppressed"
		} else {
			f.Reason = "owner_decision_requires_approval"
		}
	}
	if e = storeManagementFinding(tx, f, mode, config, now); e != nil {
		return e
	}
	return tx.SaveFindingDecisionBasis(b)
}
func (s *Service) FindingAction(ctx context.Context, token, key, id, etag, decision string, body []byte) (domain.Response, error) {
	if decision != "approve" && decision != "reject" {
		return domain.Response{}, domain.ErrInvalidTarget
	}
	var preparedErr error
	if decision == "approve" {
		replay, found, release, e := s.prepareFindingAction(ctx, token, key, id, etag, body)
		defer release()
		if found {
			return replay, e
		}
		preparedErr = e
	}
	return s.ownerAction(ctx, token, key, "POST", "/api/v1/findings/"+url.PathEscape(id)+"/decisions", etag, body, func(tx app.WriteTx, actor domain.Session) (domain.Response, error) {
		f, e := tx.Finding(id)
		if e != nil {
			return domain.Response{}, e
		}
		if !visibleFindingForActor(tx, actor, f) {
			return domain.Response{}, domain.ErrNotFound
		}
		if etag != domain.FindingETag(f) {
			return domain.Response{}, domain.ErrPrecondition
		}
		if f.Status != "proposed" && !(f.Status == "blocked" && decision == "approve") {
			return domain.Response{}, actionError(409, "INVALID_TRANSITION", "historical_finding")
		}
		if decision == "approve" && (f.Kind == "new_document_proposal" || f.Kind == "new_version_proposal") {
			return domain.Response{}, actionError(503, "FEATURE_NOT_READY", "finding_draft_owned_by_task06")
		}
		b, exists, e := tx.FindingDecisionBasis(id)
		if e != nil {
			return domain.Response{}, e
		}
		if !exists {
			b, e = captureDecisionBasis(tx, f)
			if e != nil {
				return domain.Response{}, e
			}
			if b.OwnerRevision != 0 {
				return domain.Response{}, actionError(409, "OPERATION_CONFLICT", "owner_basis_unverified")
			}
		}
		current, _, e := tx.FindingDecision(b.Key)
		if e != nil {
			return domain.Response{}, e
		}
		if current.Revision != b.OwnerRevision {
			return s.refuseFinding(tx, f, actionError(409, "OPERATION_CONFLICT", "owner_decision_changed"))
		}
		now := s.Now().UTC().Format(time.RFC3339Nano)
		previous := f.Revision
		result := domain.FindingActionResult{OperationID: key, State: "completed"}
		if decision == "approve" {
			if preparedErr != nil {
				return s.refuseFinding(tx, f, preparedErr)
			}
			if e = s.guardFinding(tx, actor, f, b); e != nil {
				return s.refuseFinding(tx, f, e)
			}
			if current.Action == "reject" && decisionMaterialMatches(current, b.Material) {
				return domain.Response{}, actionError(409, "OPERATION_CONFLICT", "owner_rejection_unchanged")
			}
			if current.Action == "undo" && decisionMaterialMatches(current, b.Material) && !b.Reassessment {
				return domain.Response{}, actionError(409, "OPERATION_CONFLICT", "undo_requires_reassessment")
			}
			if f.Status == "blocked" {
				f.Status = "proposed"
				if e = tx.AppendEvent("finding", f.ID, "finding.revalidated", f.Revision+1, actionJSON(map[string]string{"finding_id": f.ID})); e != nil {
					return domain.Response{}, e
				}
			}
			switch f.Kind {
			case "related_explanation", "topic_assignment":
				m, e := managementCandidate(f)
				if e != nil {
					return domain.Response{}, e
				}
				old, found, e := tx.ManagementEffectByKey(m.Key())
				if e != nil {
					return domain.Response{}, e
				}
				if found && (old.Pinned || old.Availability == "unavailable") {
					return domain.Response{}, actionError(409, "OPERATION_CONFLICT", "owner_effect_conflict")
				}
				if found {
					old.Decision = "accepted"
					old.DecisionActor = actor.PrincipalID
					old.Revision++
					old.UpdatedAt = now
					// New owner-approved linkage is an intervening owner change.
					// Advance the client validator and all existing active links together.
					if e = s.reviseEffectLinks(tx, actor, old, true, now); e != nil {
						return domain.Response{}, e
					}
					if e = tx.ReviseManagementEffect(old, old.Revision-1); e != nil {
						return domain.Response{}, e
					}
					event := "overlay.changed"
					if old.Kind == "topic_assignment" {
						event = "topic_assignment.changed"
					}
					if e = tx.AppendEvent("finding", f.ID, event, old.Revision, actionJSON(map[string]string{"finding_id": f.ID, "overlay_id": old.ID})); e != nil {
						return domain.Response{}, e
					}

				}
				// The ledger supersession and materialization commit in this same writer.
				d := domain.FindingDecision{Key: b.Key, Material: b.Material, Kind: f.Kind, Action: "approve", FindingID: f.ID, VersionIDs: decisionVersions(f), Actor: actor.PrincipalID, Revision: current.Revision + 1, At: now}
				if e = tx.SaveFindingDecision(d, current.Revision); e != nil {
					return domain.Response{}, e
				}
				f.Revision++
				f.UpdatedAt = now
				f.DecidedBy = freshPtr(actor.PrincipalID)
				f.DecidedAt = &now
				cfg, e := tx.Configuration()
				if e != nil {
					return domain.Response{}, e
				}
				if e = storeManagementFinding(tx, &f, "auto", cfg.Revision, now); e != nil {
					return domain.Response{}, e
				}
				if f.Status != "applied" {
					return domain.Response{}, actionError(409, "OPERATION_CONFLICT", "effect_application_conflict")
				}
				var ref struct {
					ID string `json:"overlay_id"`
				}
				_ = json.Unmarshal(f.AppliedRef, &ref)
				applied, e := tx.ManagementEffect(ref.ID)
				if e != nil {
					return domain.Response{}, e
				}
				applied.Availability = "available"
				applied.ETag = applied.Validator()
				result.Effect = &applied
			case "freshness_assessment":
				if e = s.approveFreshness(tx, f); e != nil {
					return s.refuseFinding(tx, f, e)
				}
				f.Status = "applied"
				f.ApprovalRequired = false
				f.AppliedRef = f.Proposal
			case "backlink_index_repair":
				jobs, e := tx.RebuildIndexes(*f.VersionID, "references")
				if e != nil {
					return domain.Response{}, e
				}
				if len(jobs) != 1 {
					return domain.Response{}, domain.ErrReconciliation
				}
				job := jobs[0]
				f.Reason = "approved_repair_pending"
				f.ApprovalRequired = false
				f.AppliedRef = actionJSON(map[string]string{"job_id": job})
				result.State = "repair_pending"
				result.JobID = &job
			default:
				return domain.Response{}, domain.ErrInvalidTarget
			}
		} else {
			f.Status = "rejected"
			f.ApprovalRequired = false
			f.Reason = "owner_rejected"
			d := domain.FindingDecision{Key: b.Key, Material: b.Material, Kind: f.Kind, Action: "reject", FindingID: f.ID, VersionIDs: decisionVersions(f), Actor: actor.PrincipalID, Revision: current.Revision + 1, At: now}
			if e = tx.SaveFindingDecision(d, current.Revision); e != nil {
				return domain.Response{}, e
			}
		}
		if decision == "approve" && f.Kind != "related_explanation" && f.Kind != "topic_assignment" {
			d := domain.FindingDecision{Key: b.Key, Material: b.Material, Kind: f.Kind, Action: "approve", FindingID: f.ID, VersionIDs: decisionVersions(f), Actor: actor.PrincipalID, Revision: current.Revision + 1, At: now}
			if e = tx.SaveFindingDecision(d, current.Revision); e != nil {
				return domain.Response{}, e
			}
		}
		if f.Revision == previous {
			f.Revision++
			f.UpdatedAt = now
			f.DecidedBy = freshPtr(actor.PrincipalID)
			f.DecidedAt = &now
			if e = tx.ReviseFinding(f, previous); e != nil {
				return domain.Response{}, e
			}
		}
		if e = tx.SaveFindingDecisionBasis(b); e != nil {
			return domain.Response{}, e
		}
		s.projectDecision(tx, actor, &f)
		result.Finding = &f
		status := 200
		if result.State == "repair_pending" {
			status = 202
		}
		return domain.Response{Status: status, Body: actionJSON(result), Headers: map[string]string{"ETag": domain.FindingETag(f)}}, nil
	})
}
func (s *Service) ownerAction(ctx context.Context, token, key, method, target, etag string, body []byte, apply func(app.WriteTx, domain.Session) (domain.Response, error)) (domain.Response, error) {
	actor, e := s.Auth.Authenticate(ctx, token)
	if e != nil {
		return domain.Response{}, e
	}
	if actor.Scope.Kind != "owner" {
		return domain.Response{}, domain.ErrDenied
	}
	if etag == "" {
		return domain.Response{}, actionError(428, "PRECONDITION_REQUIRED", "if_match_required")
	}
	req := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: method, Target: target, Body: body, Preconditions: map[string]string{"if-match": etag}}
	return app.Execute(ctx, s.Store, s.Auth.Authority(token), req, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		owner, e := s.Auth.AuthenticateWrite(ctx, tx, token)
		if e != nil {
			return domain.Response{}, e
		}
		if owner.Scope.Kind != "owner" {
			return domain.Response{}, domain.ErrDenied
		}
		return apply(tx, owner)
	})
}
func (s *Service) guardFinding(tx app.WriteTx, actor domain.Session, f domain.Finding, b domain.FindingDecisionBasis) error {
	return s.guardApplication(tx, actor, f)
}
func (s *Service) guardApplication(tx app.WriteTx, actor domain.Session, f domain.Finding) error {
	if !visibleFindingForActor(tx, actor, f) {
		return domain.ErrNotFound
	}
	if f.VersionID == nil {
		return domain.ErrInvalidTarget
	}
	job, e := tx.Job(f.JobID)
	if e != nil {
		return e
	}
	if job.CancelRequested || job.State == "cancelled" {
		return actionError(409, "OPERATION_CONFLICT", "scan_cancelled")
	}
	run, e := tx.ScanRun(f.JobID)
	if e != nil {
		return e
	}
	projects := []string{}
	seen := map[string]bool{}
	for _, id := range decisionVersions(f) {
		if _, e := source.Check(tx, actor, id); e != nil {
			return e
		}
		v, e := tx.PublishedVersion(id)
		if e != nil {
			return e
		}
		doc, e := tx.Document(v.DocumentID)
		if e != nil {
			return e
		}
		scope, e := tx.Scope(doc.ProjectID)
		if e != nil {
			return e
		}
		expected, ok := run.PermissionsRevisions[doc.ProjectID]
		if !ok {
			return actionError(409, "OPERATION_CONFLICT", "owner_basis_unverified")
		}
		if scope.Project.PermissionsRevision != expected {
			return actionError(409, "OPERATION_CONFLICT", "source_authority_changed")
		}
		if !seen[doc.ProjectID] {
			seen[doc.ProjectID] = true
			projects = append(projects, doc.ProjectID)
		}
	}
	v, e := tx.PublishedVersion(*f.VersionID)
	if e != nil {
		return e
	}
	doc, e := tx.Document(v.DocumentID)
	if e != nil {
		return e
	}
	if run.RequestedScope.VersionID == "" && doc.LatestVersionID != v.ID {
		return actionError(409, "VERSION_CONFLICT", "document_updated")
	}
	cfg, e := tx.Configuration()
	if e != nil {
		return e
	}
	policy, e := ResolveEffectivePolicy(cfg, projects, run.EffectivePolicy.Depth)
	if e != nil {
		return e
	}
	modes := map[string][2]string{"related_explanation": {run.EffectivePolicy.AutoApply.RelatedExplanation, policy.AutoApply.RelatedExplanation}, "topic_assignment": {run.EffectivePolicy.AutoApply.TopicAssignment, policy.AutoApply.TopicAssignment}, "freshness_assessment": {run.EffectivePolicy.AutoApply.FreshnessAssessment, policy.AutoApply.FreshnessAssessment}, "backlink_index_repair": {run.EffectivePolicy.AutoApply.BacklinkIndexRepair, policy.AutoApply.BacklinkIndexRepair}}
	pair := modes[f.Kind]
	if StricterAutoApplyMode(pair[0], pair[1]) == "disabled" {
		return actionError(409, "OPERATION_CONFLICT", "policy_disabled")
	}
	return nil
}
func (s *Service) projectDecision(tx app.WriteTx, actor domain.Session, f *domain.Finding) {
	if b, ok, e := tx.FindingDecisionBasis(f.ID); e == nil && ok {
		if d, yes, e := tx.FindingDecision(b.Key); e == nil && yes {
			f.OwnerDecision = &domain.OwnerDecisionView{Action: d.Action, Revision: d.Revision}
		}
	}
	var ref struct {
		ID string `json:"overlay_id"`
	}
	if json.Unmarshal(f.AppliedRef, &ref) == nil && ref.ID != "" {
		if m, e := tx.ManagementEffect(ref.ID); e == nil {
			visible := m.Availability != "unavailable"
			p := map[string]string{}
			if m.Target != nil {
				p["target_version_id"] = m.Target.VersionID
			}
			for _, id := range decisionVersions(domain.Finding{VersionID: &m.From.VersionID, Proposal: actionJSON(p), Evidence: m.Evidence}) {
				visible = visible && checkVersionVisibleForFinding(tx, actor, id)
			}
			if visible {
				m.Availability = "available"
				m.ETag = m.Validator()
			} else {
				m = m.Unavailable()
			}
			f.Effect = &m
		}
	}
	f.ETag = domain.FindingETag(*f)
}
func (s *Service) EffectAction(ctx context.Context, token, key, id, etag string, pinned *bool, decision string, body []byte) (domain.Response, error) {
	method, target := "POST", "/api/v1/overlays/"+url.PathEscape(id)+"/undo"
	if pinned != nil {
		method, target = "PUT", "/api/v1/topic-assignments/"+url.PathEscape(id)
	}
	return s.ownerAction(ctx, token, key, method, target, etag, body, func(tx app.WriteTx, actor domain.Session) (domain.Response, error) {
		m, e := tx.ManagementEffect(id)
		if e != nil {
			return domain.Response{}, e
		}
		if m.Availability == "unavailable" {
			return domain.Response{}, domain.ErrNotFound
		}
		if etag != m.Validator() {
			return domain.Response{}, domain.ErrPrecondition
		}
		now := s.Now().UTC().Format(time.RFC3339Nano)
		origin, e := tx.Finding(m.FindingID)
		if e != nil {
			return domain.Response{}, e
		}
		for _, version := range decisionVersions(origin) {
			if _, e = source.Check(tx, actor, version); e != nil {
				return domain.Response{}, e
			}
		}
		links, e := tx.ManagementLinkedFindings(m.ID)
		if e != nil {
			return domain.Response{}, e
		}
		history := []domain.FindingDecisionLink{}
		for _, link := range links {
			f, e := tx.Finding(link.FindingID)
			if e != nil {
				return domain.Response{}, e
			}
			history = append(history, domain.FindingDecisionLink{FindingID: f.ID, Revision: f.Revision, Status: f.Status, AppliedRef: f.AppliedRef})
		}
		if pinned != nil {
			if m.Kind != "topic_assignment" {
				return domain.Response{}, domain.ErrNotFound
			}
			if m.Decision == "undone" {
				return domain.Response{}, actionError(409, "INVALID_TRANSITION", "undone_requires_reassessment_approve")
			}
			if decision != "accepted" && decision != "rejected" || *pinned && decision == "rejected" {
				return domain.Response{}, domain.ErrInvalidTarget
			}
			if m.Decision != "accepted" && decision == "accepted" {
				if e = s.guardApplication(tx, actor, origin); e != nil {
					return domain.Response{}, e
				}
			}
			m.Decision = decision
			m.Pinned = *pinned
		} else {
			if m.Decision != "accepted" || m.Pinned {
				return domain.Response{}, actionError(409, "OPERATION_CONFLICT", "effect_not_undoable")
			}
			m.Decision = "undone"
		}
		m.DecisionActor = actor.PrincipalID
		m.Revision++
		m.UpdatedAt = now
		if e = s.reviseEffectLinks(tx, actor, m, true, now); e != nil {
			return domain.Response{}, e
		}
		if e = tx.ReviseManagementEffect(m, m.Revision-1); e != nil {
			return domain.Response{}, e
		}
		f, e := tx.Finding(m.FindingID)
		if e != nil {
			return domain.Response{}, e
		}
		k, material, e := DecisionIdentity(tx, f)
		if e != nil {
			return domain.Response{}, e
		}
		d, _, e := tx.FindingDecision(k)
		if e != nil {
			return domain.Response{}, e
		}
		// Canonical provenance stays on m.FindingID. Decision memory instead
		// covers every active application that this owner operation reverses.
		materials := []string{}
		versions := map[string]bool{}
		for _, id := range d.VersionIDs {
			versions[id] = true
		}
		for _, link := range links {
			if !link.Active {
				continue
			}
			linked, e := tx.Finding(link.FindingID)
			if e != nil {
				return domain.Response{}, e
			}
			basis, ok, e := tx.FindingDecisionBasis(linked.ID)
			if e != nil {
				return domain.Response{}, e
			}
			if !ok {
				return domain.Response{}, domain.ErrReconciliation
			}
			materials = append(materials, basis.Material)
			for _, id := range decisionVersions(linked) {
				versions[id] = true
			}
		}
		if d.Material != "" {
			material = d.Material
		}
		if len(materials) == 0 {
			materials = append(materials, material)
		}
		for _, id := range decisionVersions(f) {
			versions[id] = true
		}
		versionIDs := []string{}
		for id := range versions {
			versionIDs = append(versionIDs, id)
		}
		sort.Strings(versionIDs)
		action := "approve"
		if m.Decision == "undone" {
			action = "undo"
		} else if m.Decision == "rejected" {
			action = "reject"
		}
		d = domain.FindingDecision{Key: k, Material: material, Materials: materials, Kind: m.Kind, Action: action, EffectID: m.ID, FindingID: f.ID, Links: history, VersionIDs: versionIDs, Actor: actor.PrincipalID, Revision: d.Revision + 1, At: now}
		if e = tx.SaveFindingDecision(d, d.Revision-1); e != nil {
			return domain.Response{}, e
		}
		if e = tx.AppendEvent("finding", f.ID, map[bool]string{true: "topic_assignment.changed", false: "overlay.changed"}[m.Kind == "topic_assignment"], m.Revision, actionJSON(map[string]string{"finding_id": f.ID, "overlay_id": m.ID})); e != nil {
			return domain.Response{}, e
		}
		m.Availability = "available"
		m.ETag = m.Validator()
		return domain.Response{Status: 200, Body: actionJSON(domain.FindingActionResult{OperationID: key, State: "completed", Effect: &m}), Headers: map[string]string{"ETag": m.ETag}}, nil
	})
}
func (s *Service) reviseEffectLinks(tx app.WriteTx, actor domain.Session, m domain.ManagementEffect, check bool, now string) error {
	links, e := tx.ManagementLinkedFindings(m.ID)
	if e != nil {
		return e
	}
	if len(links) == 0 {
		return domain.ErrReconciliation
	}
	for _, l := range links {
		f, e := tx.Finding(l.FindingID)
		if e != nil {
			return e
		}
		if !visibleFindingForActor(tx, actor, f) {
			return domain.ErrNotFound
		}
		if l.FindingRevision != f.Revision {
			return domain.ErrPrecondition
		}
		if !l.ValidHistory || l.EffectRevision > m.Revision || f.Kind != m.Kind {
			return domain.ErrReconciliation
		}
		if !l.Active {
			if f.Status != "undone" {
				return domain.ErrReconciliation
			}
			continue
		}
		if f.Status != "applied" || f.Kind != m.Kind || check && l.EffectRevision != m.Revision-1 {
			return domain.ErrReconciliation
		}
		if check {
			if e = tx.ValidateManagementFinding(f); e != nil {
				return e
			}
		}
		previous := f.Revision
		f.Revision++
		f.UpdatedAt = now
		f.DecidedBy = freshPtr(actor.PrincipalID)
		f.DecidedAt = &now
		if m.Decision != "accepted" {
			f.Status = "undone"
			f.Reason = "owner_" + m.Decision
			l.Active = false
		} else {
			f.AppliedRef = actionJSON(map[string]any{"overlay_id": m.ID, "kind": m.Kind, "revision": m.Revision})
		}
		if e = tx.ReviseFinding(f, previous); e != nil {
			return e
		}
		l.FindingRevision = f.Revision
		l.EffectRevision = m.Revision
		if e = tx.SaveManagementLink(l); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) approveFreshness(tx app.WriteTx, f domain.Finding) error {
	var a domain.Assessment
	var output domain.FreshnessResult
	if json.Unmarshal(f.Proposal, &a) != nil || json.Unmarshal(f.Evidence, &output) != nil || a.ID == "" || a.VersionID == "" || len(output.Units) == 0 {
		return domain.ErrReconciliation
	}
	set, e := tx.CoverageSetByID(a.CoverageSetID)
	if e != nil {
		return e
	}
	if set.TargetSetID != a.TargetSetID || set.VersionID != a.VersionID || f.VersionID == nil || a.VersionID != *f.VersionID || !freshJSONEqual(a.ScopeJSON, set.ScopeJSON) {
		return domain.ErrReconciliation
	}
	if e = validateSavedFreshness(tx, f, a, output); e != nil {
		return e
	}
	units := []domain.AssessmentUnit{}
	for _, u := range output.Units {
		p, r := "", ""
		if u.PagePath != nil {
			p = *u.PagePath
		}
		if u.RegionID != nil {
			r = *u.RegionID
		}
		units = append(units, domain.AssessmentUnit{AssessmentID: a.ID, CoverageSetID: a.CoverageSetID, TargetSetID: a.TargetSetID, Kind: u.Kind, PagePath: p, RegionID: r, Outcome: u.Outcome, Discrepancy: u.Discrepancy, Reason: u.Reason})
	}
	if a.State != domain.AssessmentState(units) {
		return domain.ErrReconciliation
	}
	if e = tx.PutAssessment(a, units); e != nil {
		return e
	}
	if e = tx.ClearAssessmentTriggers(a); e != nil {
		return e
	}
	return tx.AppendEvent("version", a.VersionID, "assessment.published", 1, actionJSON(map[string]string{"version_id": a.VersionID, "assessment_id": a.ID}))
}

func decisionMaterialMatches(d domain.FindingDecision, material string) bool {
	if d.Material == material {
		return true
	}
	for _, m := range d.Materials {
		if m == material {
			return true
		}
	}
	return false
}
