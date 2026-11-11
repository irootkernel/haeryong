package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/domain"
	"time"
)

func (t *writeTx) ownerRepairFinding(job string) (domain.Finding, bool, error) {
	var id string
	e := t.tx.QueryRowContext(t.ctx, `SELECT finding_id FROM findings WHERE kind='backlink_index_repair' AND json_extract(applied_ref_json,'$.job_id')=? ORDER BY finding_id LIMIT 1`, job).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return domain.Finding{}, false, nil
	}
	if e != nil {
		return domain.Finding{}, false, e
	}
	f, e := t.Finding(id)
	return f, true, e
}
func (t *writeTx) ownerRepairAuthority(job string) error {
	f, found, e := t.ownerRepairFinding(job)
	if e != nil || !found {
		return e
	}
	if f.Status != "proposed" || f.Reason != "approved_repair_pending" || f.VersionID == nil {
		return domain.ErrSearchRestricted
	}
	owner, e := t.AuthState()
	if e != nil {
		return e
	}
	if f.DecidedBy == nil || *f.DecidedBy != owner.OwnerID {
		return domain.ErrSearchRestricted
	}
	scope, e := t.Scope(f.ProjectID)
	if e != nil {
		return e
	}
	if scope.Project.PermissionsRevision != f.PermissionsRevision {
		return domain.ErrSearchRestricted
	}
	doc, e := t.Document(*f.DocumentID)
	if e != nil {
		return e
	}
	if doc.Lifecycle == "trash" || doc.Lifecycle == "purged" {
		return domain.ErrSearchRestricted
	}
	run, e := t.ScanRun(f.JobID)
	if e != nil {
		return e
	}
	if run.RequestedScope.VersionID == "" && doc.LatestVersionID != *f.VersionID || run.EffectivePolicy.AutoApply.BacklinkIndexRepair == "disabled" {
		return domain.ErrSearchRestricted
	}
	cfg, e := t.Configuration()
	if e != nil {
		return e
	}
	var policy struct {
		Maintenance struct {
			AutoApply struct {
				Mode string `json:"backlink_index_repair"`
			} `json:"auto_apply"`
		} `json:"maintenance"`
		Projects map[string]struct {
			Maintenance *struct {
				AutoApply *struct {
					Mode *string `json:"backlink_index_repair"`
				} `json:"auto_apply"`
			} `json:"maintenance"`
		} `json:"projects"`
	}
	if json.Unmarshal(cfg.Value, &policy) != nil {
		return domain.ErrSearchRestricted
	}
	if policy.Maintenance.AutoApply.Mode == "disabled" {
		return domain.ErrSearchRestricted
	}
	p := policy.Projects[f.ProjectID]
	if p.Maintenance != nil && p.Maintenance.AutoApply != nil && p.Maintenance.AutoApply.Mode != nil && *p.Maintenance.AutoApply.Mode == "disabled" {
		return domain.ErrSearchRestricted
	}
	return nil
}
func (t *writeTx) completeOwnerRepair(job string) error {
	f, found, e := t.ownerRepairFinding(job)
	if e != nil || !found {
		return e
	}
	if e = t.ownerRepairAuthority(job); e != nil {
		return e
	}
	work, e := t.DerivedWork(job)
	if e != nil {
		return e
	}
	material, e := t.ReferenceMaterialization(*f.VersionID)
	if e != nil {
		return e
	}
	if work.WorkKind != "references" || work.VersionID != *f.VersionID || material.JobID != job || material.State != "ready" {
		return domain.ErrReconciliation
	}
	expected := f.Revision
	f.Revision++
	f.Status = "applied"
	f.Reason = "owner_repair_verified"
	f.ApprovalRequired = false
	f.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return t.ReviseFinding(f, expected)
}
