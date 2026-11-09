package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func (t *writeTx) ActiveCoalescedScan(scopeHash string) (domain.Job, bool, error) {
	var id string
	err := t.tx.QueryRowContext(t.ctx, `SELECT j.job_id FROM scan_runs s JOIN jobs j USING(job_id) WHERE s.scope_hash=? AND j.state IN ('queued','running','interrupted') AND j.cancel_requested=0 AND NOT EXISTS(SELECT 1 FROM resource_revisions h WHERE h.resource_kind='restore_job_hold' AND h.resource_id=j.job_id) ORDER BY s.captured_at,j.job_id LIMIT 1`, scopeHash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Job{}, false, nil
	}
	if err != nil {
		return domain.Job{}, false, err
	}
	j, err := t.Job(id)
	if err != nil {
		return domain.Job{}, false, err
	}
	return j, true, nil
}

func (t *writeTx) CreateScanRun(run domain.ScanRun, versions []domain.ScanCapturedVersion) error {
	if len(run.RequestedScopeJSON) == 0 {
		run.RequestedScopeJSON = jsonBytes(run.RequestedScope)
	}
	if len(run.EffectivePolicyJSON) == 0 {
		run.EffectivePolicyJSON = jsonBytes(run.EffectivePolicy)
	}
	if len(run.DenominatorJSON) == 0 {
		run.DenominatorJSON = jsonBytes(run.Denominator)
	}
	if len(run.ReportJSON) == 0 {
		run.ReportJSON = jsonBytes(run.Report)
	}
	if run.NextItemOrdinal < 1 {
		run.NextItemOrdinal = 1
	}
	if run.Stage == "" {
		run.Stage = "inventory"
	}
	perms := jsonBytes(run.PermissionsRevisions)
	if len(perms) == 0 || string(perms) == "null" {
		perms = []byte("{}")
	}
	if err := t.exec(`INSERT INTO scan_runs(job_id,principal_id,auth_kind,scope_hash,target_set_id,requested_scope,effective_policy,config_revision,permissions_revisions,stage,enum_version_ordinal,enum_after_path,enum_after_region,enumeration_complete,next_item_ordinal,denominator,report,captured_at,started_at,elapsed_ms,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		run.JobID, run.PrincipalID, run.AuthKind, run.ScopeHash, nullable(run.TargetSetID),
		run.RequestedScopeJSON, run.EffectivePolicyJSON, run.ConfigRevision, perms,
		run.Stage, run.EnumVersionOrdinal, run.EnumAfterPath, run.EnumAfterRegion,
		boolInt(run.EnumerationComplete), run.NextItemOrdinal, run.DenominatorJSON, run.ReportJSON,
		run.CapturedAt, nullablePtr(run.StartedAt), run.ElapsedMs, run.UpdatedAt,
	); err != nil {
		return err
	}
	for _, v := range versions {
		sel := v.SelectionJSON
		if len(sel) == 0 {
			sel = []byte("{}")
		}
		targets := v.TargetsJSON
		if len(targets) == 0 {
			targets = []byte("[]")
		}
		enumState := v.EnumState
		if enumState == "" {
			enumState = "pending"
		}
		mode := v.SelectedMode
		if mode == "" {
			mode = "pinned"
		}
		selSource := v.SelectionSource
		if selSource == "" {
			selSource = "default_pinned"
		}
		if err := t.exec(`INSERT INTO scan_captured_versions(job_id,version_ordinal,project_id,document_id,version_id,content_set_id,manifest_sha256,metadata_sha256,source_basis,source_intent,selected_mode,selection_source,target_set_id,requested_selection,targets_json,exclusion_reason,page_count,region_count,entry_count,source_count,unit_count,enum_state,enum_after_path,enum_after_region,entry_after_path,source_after_idx,materialized_units,entries_checked,captured_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			run.JobID, v.VersionOrdinal, v.ProjectID, v.DocumentID, v.VersionID, v.ContentSetID,
			v.ManifestSHA256, v.MetadataSHA256, v.SourceBasis, nullablePtr(v.SourceIntent),
			mode, selSource, v.TargetSetID, sel, targets, v.ExclusionReason,
			v.PageCount, v.RegionCount, v.EntryCount, v.SourceCount, v.UnitCount,
			enumState, v.EnumAfterPath, v.EnumAfterRegion, v.EntryAfterPath, v.SourceAfterIdx,
			v.MaterializedUnits, v.EntriesChecked, v.CapturedAt,
		); err != nil {
			return err
		}
		if v.ExclusionReason == "" && v.VersionID != "" {
			for _, hash := range []string{v.ManifestSHA256, v.MetadataSHA256} {
				if hash == "" {
					continue
				}
				if obj, e := t.Object(hash); e == nil && obj.State == "ready" {
					pinID := "scan:" + run.JobID + ":" + v.VersionID + ":" + hash
					if err := t.AcquirePin(domain.Pin{ID: pinID, Kind: "job", OwnerID: run.JobID, Hash: hash}); err != nil {
						return err
					}
				}
			}
		}
	}
	return t.SetJobResult(run.JobID, run.ReportJSON)
}

func (t *writeTx) ScanRun(jobID string) (domain.ScanRun, error) {
	var run domain.ScanRun
	var targetSet, started sql.NullString
	var reqScopeRaw, effPolicyRaw, permsRaw, denomRaw, reportRaw []byte
	var enumComplete int
	err := t.tx.QueryRowContext(t.ctx, `SELECT job_id,principal_id,auth_kind,scope_hash,target_set_id,requested_scope,effective_policy,config_revision,permissions_revisions,stage,enum_version_ordinal,enum_after_path,enum_after_region,enumeration_complete,next_item_ordinal,denominator,report,captured_at,started_at,elapsed_ms,updated_at FROM scan_runs WHERE job_id=?`, jobID).Scan(
		&run.JobID, &run.PrincipalID, &run.AuthKind, &run.ScopeHash, &targetSet,
		&reqScopeRaw, &effPolicyRaw, &run.ConfigRevision, &permsRaw,
		&run.Stage, &run.EnumVersionOrdinal, &run.EnumAfterPath, &run.EnumAfterRegion,
		&enumComplete, &run.NextItemOrdinal, &denomRaw, &reportRaw,
		&run.CapturedAt, &started, &run.ElapsedMs, &run.UpdatedAt,
	)
	if err != nil {
		return run, notFound(err)
	}
	run.RequestedScopeJSON = reqScopeRaw
	run.EffectivePolicyJSON = effPolicyRaw
	run.DenominatorJSON = denomRaw
	run.ReportJSON = reportRaw
	if targetSet.Valid {
		run.TargetSetID = targetSet.String
	}
	if started.Valid {
		s := started.String
		run.StartedAt = &s
	}
	run.EnumerationComplete = enumComplete != 0
	_ = json.Unmarshal(run.RequestedScopeJSON, &run.RequestedScope)
	_ = json.Unmarshal(run.EffectivePolicyJSON, &run.EffectivePolicy)
	_ = json.Unmarshal(permsRaw, &run.PermissionsRevisions)
	_ = json.Unmarshal(run.DenominatorJSON, &run.Denominator)
	_ = json.Unmarshal(run.ReportJSON, &run.Report)
	if j, err := t.Job(jobID); err == nil && len(j.Scope) > 0 {
		var js struct {
			CredScope domain.CredentialScope `json:"credential_scope"`
		}
		if json.Unmarshal(j.Scope, &js) == nil {
			run.CapturedScope = js.CredScope
		}
	}
	return run, nil
}

func (t *writeTx) UpdateScanRun(run domain.ScanRun) error {
	if err := t.refreshScanReportCounts(&run); err != nil {
		return err
	}
	run.DenominatorJSON = jsonBytes(run.Denominator)
	run.ReportJSON = jsonBytes(run.Report)
	if run.UpdatedAt == "" {
		run.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := t.exec(`UPDATE scan_runs SET stage=?,enum_version_ordinal=?,enum_after_path=?,enum_after_region=?,enumeration_complete=?,next_item_ordinal=?,denominator=?,report=?,started_at=?,elapsed_ms=?,updated_at=? WHERE job_id=?`,
		run.Stage, run.EnumVersionOrdinal, run.EnumAfterPath, run.EnumAfterRegion,
		boolInt(run.EnumerationComplete), run.NextItemOrdinal, run.DenominatorJSON, run.ReportJSON,
		nullablePtr(run.StartedAt), run.ElapsedMs, run.UpdatedAt, run.JobID,
	); err != nil {
		return err
	}
	return t.SetJobResult(run.JobID, run.ReportJSON)
}

const capturedVersionColumns = `job_id,version_ordinal,project_id,document_id,version_id,content_set_id,manifest_sha256,metadata_sha256,source_basis,source_intent,selected_mode,selection_source,target_set_id,requested_selection,targets_json,exclusion_reason,page_count,region_count,entry_count,source_count,unit_count,enum_state,enum_after_path,enum_after_region,entry_after_path,source_after_idx,materialized_units,entries_checked,captured_at`

func scanCapturedVersionRow(scan func(...any) error) (domain.ScanCapturedVersion, error) {
	var v domain.ScanCapturedVersion
	var intent sql.NullString
	var selRaw, targetsRaw []byte
	err := scan(
		&v.JobID, &v.VersionOrdinal, &v.ProjectID, &v.DocumentID, &v.VersionID, &v.ContentSetID,
		&v.ManifestSHA256, &v.MetadataSHA256, &v.SourceBasis, &intent,
		&v.SelectedMode, &v.SelectionSource, &v.TargetSetID, &selRaw, &targetsRaw,
		&v.ExclusionReason, &v.PageCount, &v.RegionCount, &v.EntryCount, &v.SourceCount, &v.UnitCount,
		&v.EnumState, &v.EnumAfterPath, &v.EnumAfterRegion, &v.EntryAfterPath, &v.SourceAfterIdx,
		&v.MaterializedUnits, &v.EntriesChecked, &v.CapturedAt,
	)
	if err != nil {
		return v, err
	}
	v.SelectionJSON = selRaw
	v.TargetsJSON = targetsRaw
	if intent.Valid {
		s := intent.String
		v.SourceIntent = &s
	}
	v.Targets = unmarshalScanTargets(v.TargetsJSON)
	return v, nil
}

func unmarshalScanTargets(raw []byte) []domain.ScanSourceTarget {
	if len(raw) == 0 {
		return nil
	}
	var env struct {
		Sources []domain.ScanSourceTarget `json:"sources"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Sources != nil {
		return env.Sources
	}
	var list []domain.ScanSourceTarget
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	return nil
}

func (t *writeTx) ScanCapturedVersions(jobID string, afterOrdinal, limit int) ([]domain.ScanCapturedVersion, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT `+capturedVersionColumns+` FROM scan_captured_versions WHERE job_id=? AND version_ordinal>? ORDER BY version_ordinal LIMIT ?`, jobID, afterOrdinal, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ScanCapturedVersion{}
	for rows.Next() {
		v, e := scanCapturedVersionRow(rows.Scan)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (t *writeTx) ScanCapturedVersion(jobID string, ordinal int) (domain.ScanCapturedVersion, error) {
	v, err := scanCapturedVersionRow(t.tx.QueryRowContext(t.ctx, `SELECT `+capturedVersionColumns+` FROM scan_captured_versions WHERE job_id=? AND version_ordinal=?`, jobID, ordinal).Scan)
	return v, notFound(err)
}

func (t *writeTx) ScanCapturedVersionByID(jobID, versionID string) (domain.ScanCapturedVersion, error) {
	v, err := scanCapturedVersionRow(t.tx.QueryRowContext(t.ctx, `SELECT `+capturedVersionColumns+` FROM scan_captured_versions WHERE job_id=? AND version_id=? ORDER BY version_ordinal LIMIT 1`, jobID, versionID).Scan)
	return v, notFound(err)
}

func (t *writeTx) UpdateScanCapturedVersion(v domain.ScanCapturedVersion) error {
	if err := t.exec(`UPDATE scan_captured_versions SET exclusion_reason=?,enum_state=?,enum_after_path=?,enum_after_region=?,entry_after_path=?,source_after_idx=?,materialized_units=?,entries_checked=? WHERE job_id=? AND version_ordinal=?`,
		v.ExclusionReason, v.EnumState, v.EnumAfterPath, v.EnumAfterRegion, v.EntryAfterPath, v.SourceAfterIdx, v.MaterializedUnits, v.EntriesChecked, v.JobID, v.VersionOrdinal,
	); err != nil {
		return err
	}
	if (v.EnumState == "purged" || v.ExclusionReason == "document_purged") && v.DocumentID != "" {
		return t.redactPurgedScanRecords(v.DocumentID)
	}
	return nil
}

const jobItemColumns = `job_id,item_id,ordinal,unit_kind,project_id,document_id,version_id,page_path,region_id,source_id,target_set_id,stage,state,attempt_generation,reason,detail,finding_id,repair_job_id,updated_at`

func scanJobItemRow(scan func(...any) error) (domain.JobItem, error) {
	var item domain.JobItem
	var detail []byte
	err := scan(
		&item.JobID, &item.ItemID, &item.Ordinal, &item.UnitKind, &item.ProjectID,
		&item.DocumentID, &item.VersionID, &item.PagePath, &item.RegionID, &item.SourceID,
		&item.TargetSetID, &item.Stage, &item.State, &item.AttemptGeneration, &item.Reason,
		&detail, &item.FindingID, &item.RepairJobID, &item.UpdatedAt,
	)
	item.Detail = detail
	return item, err
}

func (t *writeTx) PutJobItem(item domain.JobItem) error {
	detail := item.Detail
	if len(detail) == 0 {
		detail = []byte("{}")
	}
	if item.UpdatedAt == "" {
		item.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return t.exec(`INSERT INTO job_items(job_id,item_id,ordinal,unit_kind,project_id,document_id,version_id,page_path,region_id,source_id,target_set_id,stage,state,attempt_generation,reason,detail,finding_id,repair_job_id,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(job_id,ordinal) DO NOTHING`,
		item.JobID, item.ItemID, item.Ordinal, item.UnitKind, item.ProjectID,
		item.DocumentID, item.VersionID, item.PagePath, item.RegionID, item.SourceID,
		item.TargetSetID, item.Stage, item.State, item.AttemptGeneration, item.Reason,
		detail, item.FindingID, item.RepairJobID, item.UpdatedAt,
	)
}

func (t *writeTx) JobItem(jobID, itemID string) (domain.JobItem, error) {
	item, err := scanJobItemRow(t.tx.QueryRowContext(t.ctx, `SELECT `+jobItemColumns+` FROM job_items WHERE job_id=? AND item_id=?`, jobID, itemID).Scan)
	return item, notFound(err)
}

func (t *writeTx) JobItems(jobID string, afterOrdinal, limit int) ([]domain.JobItem, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT `+jobItemColumns+` FROM job_items WHERE job_id=? AND ordinal>? ORDER BY ordinal LIMIT ?`, jobID, afterOrdinal, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.JobItem{}
	for rows.Next() {
		item, e := scanJobItemRow(rows.Scan)
		if e != nil {
			return nil, e
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (t *writeTx) JobItemsByState(jobID, state string, afterOrdinal, limit int) ([]domain.JobItem, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT `+jobItemColumns+` FROM job_items WHERE job_id=? AND state=? AND ordinal>? ORDER BY ordinal LIMIT ?`, jobID, state, afterOrdinal, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.JobItem{}
	for rows.Next() {
		item, e := scanJobItemRow(rows.Scan)
		if e != nil {
			return nil, e
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (t *writeTx) NextPendingJobItem(jobID string) (domain.JobItem, bool, error) {
	item, err := scanJobItemRow(t.tx.QueryRowContext(t.ctx, `SELECT `+jobItemColumns+` FROM job_items WHERE job_id=? AND state='pending' ORDER BY CASE WHEN coalesce(json_extract(detail,'$.structural_ready'),0)=1 THEN 1 ELSE 0 END, ordinal LIMIT 1`, jobID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.JobItem{}, false, nil
	}
	if err != nil {
		return domain.JobItem{}, false, err
	}
	return item, true, nil
}

func (t *writeTx) UpdateJobItem(item domain.JobItem) error {
	detail := item.Detail
	if len(detail) == 0 {
		detail = []byte("{}")
	}
	if item.UpdatedAt == "" {
		item.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := t.exec(`UPDATE job_items SET page_path=?,region_id=?,source_id=?,target_set_id=?,stage=?,state=?,attempt_generation=?,reason=?,detail=?,finding_id=?,repair_job_id=?,updated_at=? WHERE job_id=? AND item_id=?`,
		item.PagePath, item.RegionID, item.SourceID, item.TargetSetID, item.Stage, item.State, item.AttemptGeneration, item.Reason, detail, item.FindingID, item.RepairJobID, item.UpdatedAt, item.JobID, item.ItemID,
	); err != nil {
		return err
	}
	if item.Reason == "document_purged" && item.DocumentID != "" {
		return t.redactPurgedScanRecords(item.DocumentID)
	}
	return nil
}

func (t *writeTx) SkipRemainingScanItems(jobID, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return t.exec(`UPDATE job_items SET state='skipped',reason=?,stage='complete',updated_at=? WHERE job_id=? AND state IN ('pending','running')`, reason, now, jobID)
}

func (t *writeTx) ActiveDerivedWorkJob(versionID, workKind string) (domain.Job, bool, error) {
	var id string
	err := t.tx.QueryRowContext(t.ctx, `SELECT j.job_id FROM derived_work w JOIN jobs j USING(job_id) WHERE w.version_id=? AND w.work_kind=? AND j.state NOT IN ('completed','failed','cancelled') ORDER BY j.job_id LIMIT 1`, versionID, workKind).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Job{}, false, nil
	}
	if err != nil {
		return domain.Job{}, false, err
	}
	j, err := t.Job(id)
	if err != nil {
		return domain.Job{}, false, err
	}
	return j, true, nil
}

func stricterRepairMode(a, b string) string {
	rank := func(m string) int {
		switch m {
		case "disabled":
			return 0
		case "propose":
			return 1
		case "auto":
			return 2
		default:
			return 0
		}
	}
	if rank(a) <= rank(b) {
		return a
	}
	return b
}

func (t *writeTx) currentProjectRepairMode(projectID string) (string, error) {
	cfg, err := t.Configuration()
	if err != nil {
		return "", err
	}
	var parsed struct {
		Maintenance struct {
			AutoApply struct {
				BacklinkIndexRepair string `json:"backlink_index_repair"`
			} `json:"auto_apply"`
		} `json:"maintenance"`
		Projects map[string]struct {
			Maintenance *struct {
				AutoApply *struct {
					BacklinkIndexRepair *string `json:"backlink_index_repair"`
				} `json:"auto_apply"`
			} `json:"maintenance"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(cfg.Value, &parsed); err != nil {
		return "", err
	}
	mode := parsed.Maintenance.AutoApply.BacklinkIndexRepair
	if mode == "" {
		mode = "auto"
	}
	if p, ok := parsed.Projects[projectID]; ok && p.Maintenance != nil && p.Maintenance.AutoApply != nil && p.Maintenance.AutoApply.BacklinkIndexRepair != nil && *p.Maintenance.AutoApply.BacklinkIndexRepair != "" {
		mode = stricterRepairMode(mode, *p.Maintenance.AutoApply.BacklinkIndexRepair)
	}
	return mode, nil
}

func (t *writeTx) currentProjectPostWorkScanEnabled(projectID string) (bool, error) {
	cfg, err := t.Configuration()
	if err != nil {
		return false, err
	}
	var parsed struct {
		Projects map[string]struct {
			PostWorkScanEnabled bool `json:"post_work_scan_enabled"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(cfg.Value, &parsed); err != nil {
		return false, err
	}
	return parsed.Projects[projectID].PostWorkScanEnabled, nil
}

func sliceContainsStr(list []string, val string) bool {
	for _, x := range list {
		if x == val {
			return true
		}
	}
	return false
}

func (t *writeTx) effectiveScanMaxRunSeconds(run domain.ScanRun, projectID string) (int, error) {
	maxRunSec := run.EffectivePolicy.MaxRunSeconds
	cfg, err := t.Configuration()
	if err != nil {
		return maxRunSec, err
	}
	var parsed struct {
		Maintenance struct {
			MaxRunSeconds int `json:"max_run_seconds"`
		} `json:"maintenance"`
		Projects map[string]struct {
			Maintenance *struct {
				MaxRunSeconds *int `json:"max_run_seconds"`
			} `json:"maintenance"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(cfg.Value, &parsed); err != nil {
		return maxRunSec, nil
	}
	curMax := parsed.Maintenance.MaxRunSeconds
	if curMax <= 0 {
		curMax = 1800
	}
	checkProj := func(pid string) {
		if p, ok := parsed.Projects[pid]; ok && p.Maintenance != nil && p.Maintenance.MaxRunSeconds != nil && *p.Maintenance.MaxRunSeconds > 0 && *p.Maintenance.MaxRunSeconds < curMax {
			curMax = *p.Maintenance.MaxRunSeconds
		}
	}
	for _, pid := range run.RequestedScope.ProjectIDs {
		checkProj(pid)
	}
	if projectID != "" {
		checkProj(projectID)
	}
	if maxRunSec <= 0 || (curMax > 0 && curMax < maxRunSec) {
		maxRunSec = curMax
	}
	return maxRunSec, nil
}

func (t *writeTx) effectiveScanMaxProviderAttempts(run domain.ScanRun, projectID string) (int, error) {
	maxAttempts := run.EffectivePolicy.MaxProviderAttemptsPerRun
	cfg, err := t.Configuration()
	if err != nil {
		return maxAttempts, err
	}
	var parsed struct {
		Maintenance struct {
			MaxProviderAttemptsPerRun int `json:"max_provider_attempts_per_run"`
		} `json:"maintenance"`
		Projects map[string]struct {
			Maintenance *struct {
				MaxProviderAttemptsPerRun *int `json:"max_provider_attempts_per_run"`
			} `json:"maintenance"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(cfg.Value, &parsed); err != nil {
		return maxAttempts, nil
	}
	curMax := parsed.Maintenance.MaxProviderAttemptsPerRun
	if curMax <= 0 {
		curMax = 32
	}
	checkProj := func(pid string) {
		if p, ok := parsed.Projects[pid]; ok && p.Maintenance != nil && p.Maintenance.MaxProviderAttemptsPerRun != nil && *p.Maintenance.MaxProviderAttemptsPerRun > 0 && *p.Maintenance.MaxProviderAttemptsPerRun < curMax {
			curMax = *p.Maintenance.MaxProviderAttemptsPerRun
		}
	}
	for _, pid := range run.RequestedScope.ProjectIDs {
		checkProj(pid)
	}
	if projectID != "" {
		checkProj(projectID)
	}
	if maxAttempts <= 0 || (curMax > 0 && curMax < maxAttempts) {
		maxAttempts = curMax
	}
	return maxAttempts, nil
}

// scanRepairAuthority checks that a parent scan job associated with a repair
// child (or about to enqueue one) still has active authority, matching attempt
// generation, requesting-principal/workflow/source authority, target visibility,
// and an effective 'auto' backlink_index_repair policy under the stricter of
// captured and current policies.
func (t *writeTx) scanRepairAuthority(scanJobID, itemID, versionID string) error {
	parent, err := t.Job(scanJobID)
	if err != nil {
		return err
	}
	if parent.Kind != "scan" || parent.CancelRequested || (parent.State != "running" && parent.State != "queued" && parent.State != "interrupted") {
		return domain.ErrStaleWorker
	}
	var held bool
	if err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?)`, scanJobID).Scan(&held); err != nil {
		return err
	}
	if held {
		return domain.ErrStaleWorker
	}
	run, err := t.ScanRun(scanJobID)
	if err != nil {
		return err
	}
	item, err := t.JobItem(scanJobID, itemID)
	if err != nil {
		return err
	}
	if item.VersionID != versionID || (item.State != "pending" && item.State != "running") {
		return domain.ErrStaleWorker
	}
	if item.AttemptGeneration != parent.AttemptGeneration {
		return domain.ErrStaleWorker
	}
	authState, err := t.AuthState()
	if err != nil {
		return err
	}
	var parentScope struct {
		PrincipalID string                 `json:"principal_id"`
		AuthEpoch   string                 `json:"auth_epoch"`
		AuthKind    string                 `json:"auth_kind"`
		ProjectIDs  []string               `json:"project_ids"`
		CredScope   domain.CredentialScope `json:"credential_scope"`
	}
	_ = json.Unmarshal(parent.Scope, &parentScope)
	if parentScope.AuthEpoch != "" && parentScope.AuthEpoch != authState.AuthEpoch {
		return domain.ErrSearchRestricted
	}
	actor := domain.Session{
		PrincipalID: run.PrincipalID,
		AuthEpoch:   authState.AuthEpoch,
	}
	if run.AuthKind == "owner" {
		if authState.OwnerID == "" || run.PrincipalID != authState.OwnerID {
			return domain.ErrSearchRestricted
		}
		actor.Scope = domain.CredentialScope{Kind: "owner"}
	} else {
		wp, e := t.WorkflowPrincipal(run.PrincipalID)
		if e != nil || !wp.Active {
			return domain.ErrSearchRestricted
		}
		effScope := domain.RestrictCredentialScope(parentScope.CredScope, wp.Scope)
		if !sliceContainsStr(effScope.Actions, "scan.request") || !sliceContainsStr(effScope.Actions, "version.read") {
			return domain.ErrSearchRestricted
		}
		actor.Scope = effScope
	}
	_, d, err := app.CheckSourceVersion(t, actor, versionID)
	if err != nil {
		return domain.ErrSearchRestricted
	}
	if !sliceContainsStr(run.RequestedScope.ProjectIDs, d.ProjectID) {
		return domain.ErrSearchRestricted
	}
	p, err := t.Scope(d.ProjectID)
	if err != nil {
		return err
	}
	if d.Lifecycle == "trash" || d.Lifecycle == "purged" || p.Project.Lifecycle == "trash" || p.Project.Lifecycle == "purged" {
		return domain.ErrSearchRestricted
	}
	if capturedPerm, ok := run.PermissionsRevisions[d.ProjectID]; !ok || capturedPerm != p.Project.PermissionsRevision {
		return domain.ErrSearchRestricted
	}
	if run.AuthKind != "owner" {
		enabled, e := t.currentProjectPostWorkScanEnabled(d.ProjectID)
		if e != nil || !enabled {
			return domain.ErrSearchRestricted
		}
	}
	cv, err := t.ScanCapturedVersionByID(scanJobID, versionID)
	if err != nil || cv.ExclusionReason != "" {
		return domain.ErrSearchRestricted
	}
	if cv.SourceCount > 0 && run.AuthKind != "owner" && !sliceContainsStr(actor.Scope.Actions, "source.read") {
		return domain.ErrSearchRestricted
	}
	currentMode, err := t.currentProjectRepairMode(d.ProjectID)
	if err != nil {
		return err
	}
	effective := stricterRepairMode(run.EffectivePolicy.AutoApply.BacklinkIndexRepair, currentMode)
	if effective != "auto" {
		return domain.ErrSearchRestricted
	}
	maxRunSec, err := t.effectiveScanMaxRunSeconds(run, d.ProjectID)
	if err != nil {
		return err
	}
	if maxRunSec > 0 {
		elapsedMs := run.ElapsedMs
		if run.StartedAt != nil && *run.StartedAt != "" {
			if started, e := time.Parse(time.RFC3339Nano, *run.StartedAt); e == nil {
				if delta := time.Now().UTC().Sub(started).Milliseconds(); delta > 0 {
					elapsedMs += delta
				}
			}
		}
		if elapsedMs >= int64(maxRunSec)*1000 {
			return domain.ErrSearchRestricted
		}
	}
	return nil
}

func (t *writeTx) RebuildScanReferences(scanJobID, itemID, versionID string) (string, error) {
	parent, err := t.Job(scanJobID)
	if err != nil {
		return "", err
	}
	if err := t.scanRepairAuthority(scanJobID, itemID, versionID); err != nil {
		return "", err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	ids, err := t.RebuildIndexes(versionID, "references")
	if err != nil {
		return "", err
	}
	if len(ids) == 0 || ids[0] == "" {
		return "", domain.ErrConflict
	}
	childID := ids[0]
	if err := t.exec(`UPDATE job_items SET repair_job_id=?,stage='repair',attempt_generation=?,updated_at=? WHERE job_id=? AND item_id=?`, childID, parent.AttemptGeneration, now, scanJobID, itemID); err != nil {
		return "", err
	}
	return childID, nil
}

func unitKindAtVersionOffset(cv domain.ScanCapturedVersion, idx int) string {
	if cv.ExclusionReason != "" || cv.VersionID == "" {
		return "document"
	}
	if idx == 0 {
		return "version"
	}
	rem := idx - 1
	if rem < cv.PageCount {
		return "page"
	}
	rem -= cv.PageCount
	if rem < cv.RegionCount {
		return "region"
	}
	rem -= cv.RegionCount
	if rem < cv.EntryCount {
		return "entry"
	}
	rem -= cv.EntryCount
	srcUnits := cv.SourceCount
	if srcUnits == 0 {
		srcUnits = 1
	}
	if rem < srcUnits {
		return "source"
	}
	return "index"
}

func (t *writeTx) finalizeUnmaterializedScanUnits(jobID, defaultState, defaultReason, now string) error {
	run, err := t.ScanRun(jobID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	afterOrd := 0
	for {
		versions, err := t.ScanCapturedVersions(jobID, afterOrd, 256)
		if err != nil {
			return err
		}
		if len(versions) == 0 {
			break
		}
		for _, cv := range versions {
			afterOrd = cv.VersionOrdinal
			for uIdx := cv.MaterializedUnits; uIdx < cv.UnitCount; uIdx++ {
				ord := run.NextItemOrdinal
				run.NextItemOrdinal++
				uKind := unitKindAtVersionOffset(cv, uIdx)
				uState, uReason := defaultState, defaultReason
				detail := json.RawMessage(`{}`)
				if cv.ExclusionReason != "" || cv.EnumState == "purged" {
					uState = "skipped"
					uReason = cv.ExclusionReason
					if uReason == "" && cv.EnumState == "purged" {
						uReason = "document_purged"
					}
					if uReason == "document_purged" || cv.EnumState == "purged" {
						detail = json.RawMessage(`{"purged":true}`)
					}
				}
				if err := t.PutJobItem(domain.JobItem{
					JobID:       jobID,
					ItemID:      fmt.Sprintf("item-%06d", ord),
					Ordinal:     ord,
					UnitKind:    uKind,
					ProjectID:   cv.ProjectID,
					DocumentID:  cv.DocumentID,
					VersionID:   cv.VersionID,
					TargetSetID: cv.TargetSetID,
					Stage:       "complete",
					State:       uState,
					Reason:      uReason,
					Detail:      detail,
					UpdatedAt:   now,
				}); err != nil {
					return err
				}
			}
			if cv.MaterializedUnits < cv.UnitCount || (cv.EnumState != "complete" && cv.EnumState != "purged") {
				cv.MaterializedUnits = cv.UnitCount
				if cv.EnumState == "purged" || cv.ExclusionReason == "document_purged" {
					cv.EnumState = "purged"
				} else {
					cv.EnumState = "complete"
				}
				cv.EnumAfterPath = ""
				cv.EnumAfterRegion = ""
				cv.EntryAfterPath = ""
				cv.SourceAfterIdx = 0
				if err := t.UpdateScanCapturedVersion(cv); err != nil {
					return err
				}
			}
			run.EnumVersionOrdinal = cv.VersionOrdinal
		}
	}
	run.EnumerationComplete = true
	run.Stage = "complete"
	run.UpdatedAt = now
	return t.UpdateScanRun(run)
}

func (t *writeTx) refreshScanReportCounts(run *domain.ScanRun) error {
	var st domain.ScanStructuralCounts
	st.RegionlessVersions = run.Report.Structural.RegionlessVersions
	st.MetadataLessVersions = run.Report.Structural.MetadataLessVersions
	var eligible, semSkipped, semUnresolved int
	var incompleteRepairs []string
	seenIncomplete := map[string]bool{}
	afterOrd := 0
	for {
		items, err := t.JobItems(run.JobID, afterOrd, 256)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			break
		}
		for _, it := range items {
			afterOrd = it.Ordinal
			switch it.State {
			case "completed":
				st.Checked++
				if it.UnitKind == "index" {
					st.CheckedIndexItems++
					if it.Reason == "repaired_auto" {
						st.Repaired++
					} else if it.Reason == "reference_index_divergence" || it.Reason == "repair_proposed" {
						st.AnomaliesReported++
					}
				} else if it.Reason == "search_index_divergence" || it.Reason == "anchor_missing" || it.Reason == "anchor_unresolved" {
					st.AnomaliesReported++
				}
				if it.UnitKind == "page" || it.UnitKind == "region" {
					eligible++
				}
			case "failed":
				st.Failed++
				st.AnomaliesReported++
				if it.UnitKind == "index" {
					st.CheckedIndexItems++
					if it.VersionID != "" && !seenIncomplete[it.VersionID] {
						seenIncomplete[it.VersionID] = true
						incompleteRepairs = append(incompleteRepairs, it.VersionID)
					}
				}
				semUnresolved++
			case "blocked":
				st.Blocked++
				if it.UnitKind == "index" && it.VersionID != "" && !seenIncomplete[it.VersionID] {
					seenIncomplete[it.VersionID] = true
					incompleteRepairs = append(incompleteRepairs, it.VersionID)
				}
				semUnresolved++
			case "skipped":
				st.Skipped++
				if it.Reason == "document_trashed" || it.Reason == "document_purged" || it.Reason == "no_published_version" {
					st.Excluded++
				}
				if it.UnitKind == "index" && it.RepairJobID != "" && it.VersionID != "" && it.Reason != "document_purged" && !seenIncomplete[it.VersionID] {
					seenIncomplete[it.VersionID] = true
					incompleteRepairs = append(incompleteRepairs, it.VersionID)
				}
				semSkipped++
			case "cancelled":
				if it.UnitKind == "index" && it.RepairJobID != "" && it.VersionID != "" && !seenIncomplete[it.VersionID] {
					seenIncomplete[it.VersionID] = true
					incompleteRepairs = append(incompleteRepairs, it.VersionID)
				}
			}
		}
	}
	if !run.EnumerationComplete {
		afterCVOrd := 0
		for {
			cvs, err := t.ScanCapturedVersions(run.JobID, afterCVOrd, 256)
			if err != nil {
				return err
			}
			if len(cvs) == 0 {
				break
			}
			for _, cv := range cvs {
				afterCVOrd = cv.VersionOrdinal
				if (cv.ExclusionReason != "" || cv.EnumState == "purged") && cv.MaterializedUnits < cv.UnitCount {
					rem := cv.UnitCount - cv.MaterializedUnits
					st.Skipped += rem
					semSkipped += rem
					if cv.ExclusionReason == "document_trashed" || cv.ExclusionReason == "document_purged" || cv.ExclusionReason == "no_published_version" || cv.EnumState == "purged" {
						st.Excluded += rem
					}
				}
			}
		}
	}
	fSummary, err := t.ScanFindingSummary(run.JobID)
	if err != nil {
		return err
	}
	provAttempts, err := t.ScanProviderAttemptCount(run.JobID, "")
	if err != nil {
		return err
	}
	var inspectedRelations int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT coalesce(sum(CASE WHEN json_type(m.value,'$.shortlist.candidates')='array' THEN json_array_length(m.value,'$.shortlist.candidates') ELSE 0 END),0) FROM maintenance_intents m JOIN jobs j ON j.job_id=m.job_id WHERE m.scan_job_id=? AND j.state='completed' AND coalesce(json_extract(m.value,'$.mode'),'')='' AND coalesce(json_extract(m.value,'$.purged'),0)=0`, run.JobID).Scan(&inspectedRelations); err != nil {
		return err
	}
	var itemInspected int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT coalesce(sum(CASE WHEN json_type(detail,'$.inspected_candidates')='integer' THEN CAST(json_extract(detail,'$.inspected_candidates') AS INTEGER) ELSE 0 END),0) FROM job_items WHERE job_id=?`, run.JobID).Scan(&itemInspected); err != nil {
		return err
	}
	if itemInspected > inspectedRelations {
		inspectedRelations = itemInspected
	}
	if inspectedRelations > run.Report.InspectedCandidateRelations {
		run.Report.InspectedCandidateRelations = inspectedRelations
	}
	var hasAttemptBudgetExhausted, hasProviderUnavailable int
	_ = t.tx.QueryRowContext(t.ctx, `SELECT coalesce(sum(CASE WHEN json_extract(detail,'$.semantic_reason')='provider_attempt_budget_exhausted' THEN 1 ELSE 0 END),0), coalesce(sum(CASE WHEN json_extract(detail,'$.semantic_reason')='provider_unavailable' THEN 1 ELSE 0 END),0) FROM job_items WHERE job_id=?`, run.JobID).Scan(&hasAttemptBudgetExhausted, &hasProviderUnavailable)
	run.Report.ProviderAttempts = provAttempts
	run.Report.Stage = run.Stage
	run.Report.EnumerationComplete = run.EnumerationComplete
	run.Report.Structural = st
	run.Report.Findings = fSummary
	run.Report.IncompleteReferenceRepairs = incompleteRepairs
	run.Report.RescanRecommended = len(incompleteRepairs) > 0
	run.Report.Semantic.Eligible = eligible
	run.Report.Semantic.Skipped = semSkipped
	run.Report.Semantic.Unresolved = semUnresolved
	run.Report.Semantic.Assessed = 0
	run.Report.Semantic.Complete = false
	if run.Report.BudgetExhausted {
		run.Report.Semantic.Status = "not_assessed"
		run.Report.Semantic.Reason = "run_budget_exhausted"
	} else if st.Failed > 0 || st.Blocked > 0 {
		run.Report.Semantic.Status = "unable_to_verify"
		run.Report.Semantic.Reason = "structural_or_source_blocked"
	} else if hasProviderUnavailable > 0 {
		run.Report.Semantic.Status = "unable_to_verify"
		run.Report.Semantic.Reason = "provider_unavailable"
	} else if hasAttemptBudgetExhausted > 0 {
		run.Report.Semantic.Status = "unable_to_verify"
		run.Report.Semantic.Reason = "provider_attempt_budget_exhausted"
	} else {
		run.Report.Semantic.Status = "not_assessed"
		run.Report.Semantic.Reason = "semantic_assessment_not_run"
	}
	return nil
}

const findingColumns = `finding_id,job_id,project_id,document_id,version_id,page_path,region_id,kind,origin,target_set_id,evidence_json,evidence_fingerprint,rationale,uncertainty,proposal_json,status,approval_required,reason,config_revision,permissions_revision,applied_ref_json,decided_by,decided_at,revision,created_at,updated_at`

func scanFindingRow(scan func(...any) error) (domain.Finding, error) {
	var f domain.Finding
	var doc, ver, page, reg, targetSet, decidedBy, decidedAt sql.NullString
	var evidence, proposal, applied []byte
	var approval int
	err := scan(
		&f.ID, &f.JobID, &f.ProjectID, &doc, &ver, &page, &reg,
		&f.Kind, &f.Origin, &targetSet, &evidence, &f.EvidenceFingerprint,
		&f.Rationale, &f.Uncertainty, &proposal, &f.Status, &approval,
		&f.Reason, &f.CapturedConfigRevision, &f.PermissionsRevision,
		&applied, &decidedBy, &decidedAt, &f.Revision, &f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		return f, err
	}
	if doc.Valid {
		s := doc.String
		f.DocumentID = &s
	}
	if ver.Valid {
		s := ver.String
		f.VersionID = &s
	}
	if page.Valid {
		s := page.String
		f.PagePath = &s
	}
	if reg.Valid {
		s := reg.String
		f.RegionID = &s
	}
	if targetSet.Valid {
		s := targetSet.String
		f.TargetSetID = &s
	}
	if decidedBy.Valid {
		s := decidedBy.String
		f.DecidedBy = &s
	}
	if decidedAt.Valid {
		s := decidedAt.String
		f.DecidedAt = &s
	}
	if len(evidence) == 0 {
		evidence = []byte("[]")
	}
	if len(proposal) == 0 {
		proposal = []byte("{}")
	}
	f.Evidence = evidence
	f.Proposal = proposal
	if len(applied) > 0 {
		f.AppliedRef = applied
	} else {
		f.AppliedRef = []byte("null")
	}
	f.ApprovalRequired = approval != 0
	f.ETag = domain.FindingETag(f)
	return f, nil
}

func (t *writeTx) PutFinding(f domain.Finding) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if f.CreatedAt == "" {
		f.CreatedAt = now
	}
	if f.UpdatedAt == "" {
		f.UpdatedAt = now
	}
	if f.Revision < 1 {
		f.Revision = 1
	}
	evidence := f.Evidence
	if len(evidence) == 0 {
		evidence = []byte("[]")
	}
	proposal := f.Proposal
	if len(proposal) == 0 {
		proposal = []byte("{}")
	}
	var applied any
	if len(f.AppliedRef) > 0 && string(f.AppliedRef) != "null" {
		applied = []byte(f.AppliedRef)
	}
	var exists bool
	if err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM findings WHERE finding_id=?)`, f.ID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if err := t.exec(`INSERT INTO findings(finding_id,job_id,project_id,document_id,version_id,page_path,region_id,kind,origin,target_set_id,evidence_json,evidence_fingerprint,rationale,uncertainty,proposal_json,status,approval_required,reason,config_revision,permissions_revision,applied_ref_json,decided_by,decided_at,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			f.ID, f.JobID, f.ProjectID, nullablePtr(f.DocumentID), nullablePtr(f.VersionID),
			nullablePtr(f.PagePath), nullablePtr(f.RegionID), f.Kind, f.Origin, nullablePtr(f.TargetSetID),
			evidence, f.EvidenceFingerprint, f.Rationale, f.Uncertainty, proposal,
			f.Status, boolInt(f.ApprovalRequired), f.Reason, f.CapturedConfigRevision, f.PermissionsRevision,
			applied, nullablePtr(f.DecidedBy), nullablePtr(f.DecidedAt), f.Revision, f.CreatedAt, f.UpdatedAt,
		); err != nil {
			return err
		}
		return t.appendEvent("finding", f.ID, "finding.created", f.Revision, jsonBytes(map[string]any{"finding_id": f.ID, "job_id": f.JobID, "kind": f.Kind, "status": f.Status}))
	}
	if err := t.exec(`UPDATE findings SET evidence_json=?,rationale=?,uncertainty=?,proposal_json=?,status=?,approval_required=?,reason=?,applied_ref_json=?,decided_by=?,decided_at=?,revision=?,updated_at=? WHERE finding_id=?`,
		evidence, f.Rationale, f.Uncertainty, proposal, f.Status, boolInt(f.ApprovalRequired),
		f.Reason, applied, nullablePtr(f.DecidedBy), nullablePtr(f.DecidedAt), f.Revision, f.UpdatedAt, f.ID,
	); err != nil {
		return err
	}
	return t.appendEvent("finding", f.ID, "finding.updated", f.Revision, jsonBytes(map[string]any{"finding_id": f.ID, "job_id": f.JobID, "kind": f.Kind, "status": f.Status}))
}

func (t *writeTx) Finding(findingID string) (domain.Finding, error) {
	f, err := scanFindingRow(t.tx.QueryRowContext(t.ctx, `SELECT `+findingColumns+` FROM findings WHERE finding_id=?`, findingID).Scan)
	return f, notFound(err)
}

func (t *writeTx) FindingByFingerprint(jobID, kind, fingerprint string) (domain.Finding, bool, error) {
	f, err := scanFindingRow(t.tx.QueryRowContext(t.ctx, `SELECT `+findingColumns+` FROM findings WHERE job_id=? AND kind=? AND evidence_fingerprint=? ORDER BY created_at DESC,finding_id DESC LIMIT 1`, jobID, kind, fingerprint).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Finding{}, false, nil
	}
	if err != nil {
		return domain.Finding{}, false, err
	}
	return f, true, nil
}

func (t *writeTx) JobFindings(filter domain.FindingFilter, afterCreatedAt, afterID string) ([]domain.Finding, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 201 {
		limit = 201
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT `+findingColumns+` FROM findings WHERE job_id=? AND (?='' OR kind=?) AND (?='' OR status=?) AND reason!='document_purged' AND NOT EXISTS(SELECT 1 FROM documents d WHERE d.document_id=findings.document_id AND d.lifecycle IN ('trash','purged')) AND NOT EXISTS(SELECT 1 FROM purged_versions pv WHERE pv.version_id=findings.version_id) AND NOT EXISTS(SELECT 1 FROM documents td WHERE td.document_id=json_extract(findings.proposal_json,'$.target_document_id') AND td.lifecycle IN ('trash','purged')) AND NOT EXISTS(SELECT 1 FROM purged_versions tpv WHERE tpv.version_id=json_extract(findings.proposal_json,'$.target_version_id')) AND NOT EXISTS(SELECT 1 FROM projects tp WHERE tp.project_id=json_extract(findings.proposal_json,'$.target_project_id') AND tp.lifecycle!='active') AND NOT EXISTS(SELECT 1 FROM documents od WHERE (od.document_id=json_extract(findings.proposal_json,'$.origin_unit.document_id') OR od.document_id IN(SELECT dv.document_id FROM document_versions dv WHERE dv.version_id=json_extract(findings.proposal_json,'$.origin_unit.version_id'))) AND od.lifecycle IN ('trash','purged')) AND NOT EXISTS(SELECT 1 FROM purged_versions opv WHERE opv.version_id=json_extract(findings.proposal_json,'$.origin_unit.version_id')) AND NOT EXISTS(SELECT 1 FROM json_each(CASE WHEN json_type(findings.evidence_json)='array' THEN findings.evidence_json ELSE '[]' END) ev WHERE json_extract(ev.value,'$.version_id') IN(SELECT pv.version_id FROM purged_versions pv UNION SELECT dv.version_id FROM document_versions dv JOIN documents d ON d.document_id=dv.document_id WHERE d.lifecycle IN ('trash','purged'))) AND (created_at>? OR (created_at=? AND finding_id>?)) ORDER BY created_at,finding_id LIMIT ?`,
		filter.JobID, filter.Kind, filter.Kind, filter.Status, filter.Status, afterCreatedAt, afterCreatedAt, afterID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Finding{}
	for rows.Next() {
		f, e := scanFindingRow(rows.Scan)
		if e != nil {
			return nil, e
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (t *writeTx) ScanFindingSummary(jobID string) (domain.ScanFindingSummary, error) {
	var s domain.ScanFindingSummary
	rows, err := t.tx.QueryContext(t.ctx, `SELECT status,count(*) FROM findings WHERE job_id=? GROUP BY status`, jobID)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return s, err
		}
		s.Total += n
		switch status {
		case "proposed":
			s.Proposed += n
		case "applied":
			s.Applied += n
		case "rejected":
			s.Rejected += n
		case "stale":
			s.Stale += n
		case "blocked":
			s.Blocked += n
		case "undone":
			s.Undone += n
		}
	}
	return s, rows.Err()
}

func (t *writeTx) ProjectDocumentsAll(projectID string) ([]domain.Document, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT document_id,project_id,title,lifecycle,coalesce(latest_version_id,''),revision FROM documents WHERE project_id=? ORDER BY document_id COLLATE BINARY`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Document{}
	for rows.Next() {
		var d domain.Document
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.Title, &d.Lifecycle, &d.LatestVersionID, &d.Revision); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func normalizePathPrefix(prefix string) string {
	return strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(prefix), "/"), "/")
}

func (t *writeTx) CountVersionInventory(versionID, contentSetID, pathPrefix, regionID string) (pages, regions, entries, sources int, err error) {
	pfx := normalizePathPrefix(pathPrefix)
	if err = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM version_pages WHERE version_id=? AND (?='' OR page_path=? OR substr(page_path,1,length(?)+1)=?||'/')`, versionID, pfx, pfx, pfx, pfx).Scan(&pages); err != nil {
		return
	}
	if err = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM regions WHERE version_id=? AND (?='' OR page_path=? OR substr(page_path,1,length(?)+1)=?||'/') AND (?='' OR region_id=?)`, versionID, pfx, pfx, pfx, pfx, regionID, regionID).Scan(&regions); err != nil {
		return
	}
	if err = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM version_entries WHERE content_set_id=? AND (?='' OR relative_path=? OR substr(relative_path,1,length(?)+1)=?||'/')`, contentSetID, pfx, pfx, pfx, pfx).Scan(&entries); err != nil {
		return
	}
	err = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM version_sources WHERE version_id=?`, versionID).Scan(&sources)
	return
}

func (t *writeTx) VersionPagesAfter(versionID, pathPrefix, afterPath string, limit int) ([]domain.Page, error) {
	pfx := normalizePathPrefix(pathPrefix)
	rows, err := t.tx.QueryContext(t.ctx, `SELECT page_path,metadata FROM version_pages WHERE version_id=? AND (?='' OR page_path=? OR substr(page_path,1,length(?)+1)=?||'/') AND page_path>? COLLATE BINARY ORDER BY page_path COLLATE BINARY LIMIT ?`, versionID, pfx, pfx, pfx, pfx, afterPath, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Page{}
	for rows.Next() {
		p := domain.Page{VersionID: versionID}
		if err := rows.Scan(&p.Path, &p.Metadata); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (t *writeTx) VersionRegionsAfter(versionID, pagePath, regionID, afterRegion string, limit int) ([]domain.Region, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT region_id,metadata FROM regions WHERE version_id=? AND page_path=? AND (?='' OR region_id=?) AND region_id>? COLLATE BINARY ORDER BY region_id COLLATE BINARY LIMIT ?`, versionID, pagePath, regionID, regionID, afterRegion, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Region{}
	for rows.Next() {
		r := domain.Region{VersionID: versionID, PagePath: pagePath}
		if err := rows.Scan(&r.ID, &r.Metadata); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *writeTx) SourceCaptureByTargetSet(targetSetID string) (domain.SourceCapture, bool, error) {
	var c domain.SourceCapture
	err := t.tx.QueryRowContext(t.ctx, `SELECT capture_id,principal_id,project_id,target_set_id,state,requested_selection,config_revision,permissions_revision,captured_at,operation_id,reason FROM source_captures WHERE target_set_id=? ORDER BY captured_at DESC,capture_id DESC LIMIT 1`, targetSetID).Scan(
		&c.ID, &c.PrincipalID, &c.ProjectID, &c.TargetSetID, &c.State, &c.Selection,
		&c.ConfigRevision, &c.PermissionsRevision, &c.CapturedAt, &c.OperationID, &c.Reason,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SourceCapture{}, false, nil
	}
	if err != nil {
		return domain.SourceCapture{}, false, err
	}
	return c, true, nil
}

func (t *writeTx) VerificationsByTargetSet(versionID, targetSetID string) ([]domain.CitationVerification, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT "+verificationColumns+" FROM citation_verifications WHERE version_id=? AND target_set_id=? ORDER BY checked_at,verification_id", versionID, targetSetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.CitationVerification{}
	for rows.Next() {
		v, e := scanVerification(rows.Scan)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (t *writeTx) ScanTargetSetCommitted(projectID, versionID, targetSetID string) (bool, error) {
	var ok bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM scan_captured_versions WHERE project_id=? AND (?='' OR version_id=?) AND target_set_id=?)`, projectID, versionID, versionID, targetSetID).Scan(&ok)
	return ok, err
}

func (t *writeTx) CreateMaintenanceJob(j domain.Job, intent domain.MaintenanceIntent) error {
	if j.Kind != "maintenance" || j.ParentID == "" || j.ParentID != intent.ScanJobID || j.ID == "" || j.ID != intent.JobID || intent.ItemID == "" {
		return domain.ErrInvalidTarget
	}
	var scope domain.ProviderScope
	if json.Unmarshal(j.Scope, &scope) != nil || scope.Invocation == nil || scope.Invocation.Maintenance == nil || scope.Invocation.Generation != nil || scope.Invocation.Question != nil || len(scope.Invocation.Maintenance.Explanations) == 0 {
		return domain.ErrInvalidTarget
	}
	if err := t.CreateJob(j); err != nil {
		return err
	}
	if err := t.exec(`INSERT INTO maintenance_intents(job_id,scan_job_id,item_id,value) VALUES(?,?,?,?)`, intent.JobID, intent.ScanJobID, intent.ItemID, jsonBytes(intent)); err != nil {
		return err
	}
	if err := app.AuthorizeMaintenance(t, j, scope, "", ""); err != nil {
		return err
	}
	if !intent.AdmissionReady {
		if err := t.PauseProviderJob(j.ID, "source_capture_pending"); err != nil {
			return err
		}
	}
	objects, err := app.ProviderInputObjects(t, scope)
	if err != nil {
		return err
	}
	for _, object := range objects {
		if err := t.AcquirePin(domain.Pin{ID: "job-input:" + j.ID + ":exp:" + object.Hash, Kind: "job_input", OwnerID: j.ID, Hash: object.Hash}); err != nil {
			return err
		}
	}
	return nil
}

func (t *writeTx) MaintenanceIntent(jobID string) (domain.MaintenanceIntent, error) {
	var m domain.MaintenanceIntent
	var scanJobID, itemID string
	var raw []byte
	if err := t.tx.QueryRowContext(t.ctx, `SELECT scan_job_id,item_id,value FROM maintenance_intents WHERE job_id=?`, jobID).Scan(&scanJobID, &itemID, &raw); err != nil {
		return m, notFound(err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	if m.JobID == "" {
		m.JobID = jobID
		m.ScanJobID = scanJobID
		m.ItemID = itemID
	}
	if m.JobID != jobID || m.ScanJobID != scanJobID || m.ItemID != itemID {
		return m, domain.ErrContentIntegrity
	}
	return m, nil
}

func (t *writeTx) MaintenanceIntentByItem(scanJobID, itemID string) (domain.MaintenanceIntent, bool, error) {
	var jobID string
	err := t.tx.QueryRowContext(t.ctx, `SELECT job_id FROM maintenance_intents WHERE scan_job_id=? AND item_id=? ORDER BY job_id DESC LIMIT 1`, scanJobID, itemID).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.MaintenanceIntent{}, false, nil
	}
	if err != nil {
		return domain.MaintenanceIntent{}, false, err
	}
	m, err := t.MaintenanceIntent(jobID)
	if err != nil {
		return domain.MaintenanceIntent{}, false, err
	}
	return m, true, nil
}

func (t *writeTx) MaintenanceIntentsByScan(scanJobID string) ([]domain.MaintenanceIntent, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT job_id FROM maintenance_intents WHERE scan_job_id=? ORDER BY item_id ASC, job_id ASC`, scanJobID)
	if err != nil {
		return nil, err
	}
	var jobIDs []string
	for rows.Next() {
		var jid string
		if err := rows.Scan(&jid); err != nil {
			_ = rows.Close()
			return nil, err
		}
		jobIDs = append(jobIDs, jid)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]domain.MaintenanceIntent, 0, len(jobIDs))
	for _, jid := range jobIDs {
		m, err := t.MaintenanceIntent(jid)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (t *writeTx) UpdateMaintenanceIntent(intent domain.MaintenanceIntent) error {
	if intent.JobID == "" || intent.ScanJobID == "" || intent.ItemID == "" {
		return domain.ErrInvalidTarget
	}
	return t.exec(`UPDATE maintenance_intents SET value=? WHERE job_id=? AND scan_job_id=? AND item_id=?`, jsonBytes(intent), intent.JobID, intent.ScanJobID, intent.ItemID)
}

func (t *writeTx) AdmitMaintenanceCapture(next domain.Job, intent domain.MaintenanceIntent) error {
	old, err := t.Job(next.ID)
	if err != nil {
		return err
	}
	oldIntent, err := t.MaintenanceIntent(next.ID)
	if err != nil {
		return err
	}
	if old.Kind != "maintenance" || old.State != "paused" || old.Error != "source_capture_pending" || old.CancelRequested || oldIntent.AdmissionReady || old.AttemptGeneration != next.AttemptGeneration || intent.JobID != next.ID {
		return domain.ErrStaleWorker
	}
	before, after := old, next
	before.Scope, after.Scope = nil, nil
	before.Permissions, after.Permissions = nil, nil
	if !reflect.DeepEqual(before, after) {
		return domain.ErrInvalidTarget
	}
	attempts, err := t.ProviderAttempts(next.ID)
	if err != nil {
		return err
	}
	if len(attempts) != 0 {
		return domain.ErrReconciliation
	}
	var scope domain.ProviderScope
	if json.Unmarshal(next.Scope, &scope) != nil || scope.Invocation == nil || scope.Invocation.Maintenance == nil || len(scope.RequestSHA) != 64 {
		return domain.ErrInvalidTarget
	}
	if err := app.AuthorizeMaintenance(t, next, scope, "", ""); err != nil {
		return err
	}
	intent.AdmissionReady = true
	if err := t.UpdateMaintenanceIntent(intent); err != nil {
		return err
	}
	if err := t.exec(`UPDATE jobs SET scope=?,permissions_revisions=?,state='queued',error='',revision=revision+1 WHERE job_id=?`, next.Scope, jsonBytes(next.Permissions), next.ID); err != nil {
		return err
	}
	return t.jobUpdated(next.ID)
}

func (t *writeTx) PauseMaintenanceCapture(jobID, reason string, limitations []domain.MaintenanceLimitation) error {
	j, err := t.Job(jobID)
	if err != nil {
		return err
	}
	intent, err := t.MaintenanceIntent(jobID)
	if err != nil {
		return err
	}
	if j.Kind != "maintenance" || j.State != "paused" || j.Error != "source_capture_pending" || j.CancelRequested || intent.AdmissionReady {
		return domain.ErrStaleWorker
	}
	intent.Limitations = append(intent.Limitations, limitations...)
	if err := t.UpdateMaintenanceIntent(intent); err != nil {
		return err
	}
	if err := t.exec(`DELETE FROM object_pins WHERE owner_kind='job_input' AND owner_id=? AND pin_id NOT GLOB 'job-input:*:exp:*'`, jobID); err != nil {
		return err
	}
	return t.PauseProviderJob(jobID, reason)
}

func (t *writeTx) RecordPreparedMaintenanceOutput(jobID, attemptID string, output json.RawMessage) error {
	a, err := t.ProviderAttempt(attemptID)
	if err != nil {
		return err
	}
	if a.JobID != jobID || a.State != "result_saved" {
		return domain.ErrStaleWorker
	}
	intent, err := t.MaintenanceIntent(jobID)
	if err != nil {
		return err
	}
	if len(intent.PreparedOutputJSON) > 0 {
		if intent.PreparedAttemptID == attemptID && string(intent.PreparedOutputJSON) == string(output) {
			return nil
		}
		return domain.ErrConflict
	}
	intent.PreparedAttemptID = attemptID
	intent.PreparedOutputJSON = output
	return t.UpdateMaintenanceIntent(intent)
}

func (t *writeTx) ScanProviderAttemptCount(scanJobID, excludeAttemptID string) (int, error) {
	var n int
	err := t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM provider_attempts a JOIN jobs j ON j.job_id=a.job_id WHERE j.parent_id=? AND j.kind='maintenance' AND (?='' OR a.attempt_id!=?) AND NOT (a.state='cancelled' AND a.reason='cancel_before_dispatch' AND a.may_have_dispatched=0)`, scanJobID, excludeAttemptID, excludeAttemptID).Scan(&n)
	return n, err
}

func (t *writeTx) ScanSearchText(versionID, pagePath, regionID string) (title string, headings []string, terms []string, body string, err error) {
	var docTitle string
	_ = t.tx.QueryRowContext(t.ctx, `SELECT coalesce(d.title,'') FROM document_versions v JOIN documents d ON d.document_id=v.document_id WHERE v.version_id=?`, versionID).Scan(&docTitle)
	seenHeadings := map[string]bool{}
	addHeading := func(h string) {
		h = strings.TrimSpace(h)
		if h != "" && !seenHeadings[h] {
			seenHeadings[h] = true
			headings = append(headings, h)
			if title == "" {
				title = h
			}
		}
	}
	if pagePath != "" && regionID != "" {
		var anchorLabel string
		_ = t.tx.QueryRowContext(t.ctx, `SELECT coalesce(pa.label,'') FROM page_anchors pa WHERE pa.version_id=? AND pa.page_path=? AND pa.anchor_id=? AND NOT EXISTS(SELECT 1 FROM search_indexes si WHERE si.version_id=pa.version_id AND si.state='pending') AND EXISTS(SELECT 1 FROM (`+searchReadUnits+`) psu WHERE psu.version_id=pa.version_id AND coalesce(psu.page_path,'')=pa.page_path AND coalesce(psu.region_id,'')='' AND psu.read_state='ready' AND psu.read_body_state='complete') LIMIT 1`, versionID, pagePath, regionID).Scan(&anchorLabel)
		if strings.TrimSpace(anchorLabel) != "" && strings.TrimSpace(anchorLabel) != regionID {
			addHeading(anchorLabel)
		}
	} else if pagePath != "" {
		aRows, aErr := t.tx.QueryContext(t.ctx, `SELECT coalesce(pa.label,'') FROM page_anchors pa WHERE pa.version_id=? AND pa.page_path=? AND coalesce(pa.label,'')!='' AND coalesce(pa.label,'')!=pa.anchor_id AND NOT EXISTS(SELECT 1 FROM search_indexes si WHERE si.version_id=pa.version_id AND si.state='pending') AND EXISTS(SELECT 1 FROM (`+searchReadUnits+`) psu WHERE psu.version_id=pa.version_id AND coalesce(psu.page_path,'')=pa.page_path AND coalesce(psu.region_id,'')='' AND psu.read_state='ready' AND psu.read_body_state='complete') ORDER BY pa.ordinal ASC`, versionID, pagePath)
		if aErr == nil {
			for aRows.Next() {
				var lbl string
				if aRows.Scan(&lbl) == nil {
					addHeading(lbl)
				}
			}
			_ = aRows.Close()
		}
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT u.kind,coalesce(u.title,''),coalesce(c.part,''),coalesce(c.ordinal,0),coalesce(c.title,''),coalesce(c.terms,''),coalesce(c.display_body,''),coalesce(c.body,'') FROM (`+searchReadUnits+`) u LEFT JOIN search_chunks c ON c.unit_id=u.unit_id AND (c.part='metadata' OR u.read_body_state='complete') WHERE u.version_id=? AND u.read_state='ready' AND NOT EXISTS(SELECT 1 FROM search_indexes si WHERE si.version_id=u.version_id AND si.state='pending') AND (?='' OR coalesce(u.page_path,'')=?) AND ((?='' AND (?='' OR coalesce(u.region_id,'')='')) OR (? !='' AND coalesce(u.region_id,'')=?)) ORDER BY u.unit_id,coalesce(c.ordinal,0)`, versionID, pagePath, pagePath, regionID, pagePath, regionID, regionID)
	if err != nil {
		if title == "" {
			title = docTitle
		}
		return title, nil, nil, "", err
	}
	defer rows.Close()
	seenTerms := map[string]bool{}
	seenBody := map[string]bool{}
	var bodyParts []string
	for rows.Next() {
		var uKind, uTitle, cPart, cTitle, cTerms, cDisplay, cBody string
		var cOrdinal int
		if err := rows.Scan(&uKind, &uTitle, &cPart, &cOrdinal, &cTitle, &cTerms, &cDisplay, &cBody); err != nil {
			if title == "" {
				title = docTitle
			}
			return title, nil, nil, "", err
		}
		for _, h := range []string{uTitle, cTitle} {
			addHeading(h)
		}
		if cPart != "body" {
			continue
		}
		for _, tok := range strings.Fields(cTerms) {
			tok = strings.TrimSpace(tok)
			if tok != "" && !seenTerms[tok] {
				seenTerms[tok] = true
				terms = append(terms, tok)
			}
		}
		txt := strings.TrimSpace(cDisplay)
		if txt == "" && cOrdinal%2 == 0 {
			rawBody := cBody
			if strings.HasPrefix(rawBody, domain.SearchNominationPrefix) {
				if nl := strings.IndexByte(rawBody, '\n'); nl >= 0 {
					rawBody = rawBody[nl+1:]
				} else {
					rawBody = ""
				}
			}
			txt = strings.TrimSpace(rawBody)
		}
		if txt != "" && !seenBody[txt] {
			seenBody[txt] = true
			bodyParts = append(bodyParts, txt)
		}
	}
	if err := rows.Err(); err != nil {
		if title == "" {
			title = docTitle
		}
		return title, nil, nil, "", err
	}
	if title == "" {
		title = docTitle
	}
	body = strings.Join(bodyParts, "\n")
	return title, headings, terms, body, nil
}

func (t *writeTx) ScanCandidateUnits(scanJobID, focalVersionID, focalPagePath, focalRegionID string, terms, sourcePaths []string, limit int) ([]domain.CandidateTarget, int, error) {
	if limit <= 0 {
		limit = 64
	}
	var skipped int
	_ = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM scan_captured_versions cv WHERE cv.job_id=? AND (cv.exclusion_reason!='' OR cv.enum_state='purged' OR cv.version_id='' OR EXISTS(SELECT 1 FROM documents d WHERE d.document_id=cv.document_id AND d.lifecycle IN ('trash','purged')) OR EXISTS(SELECT 1 FROM projects p WHERE p.project_id=cv.project_id AND p.lifecycle!='active') OR EXISTS(SELECT 1 FROM purged_versions pv WHERE pv.version_id=cv.version_id))`, scanJobID).Scan(&skipped)

	normTerms := []string{}
	seenTerms := map[string]bool{}
	for _, tok := range terms {
		tok = strings.ToLower(strings.TrimSpace(tok))
		if len(tok) >= 3 && !seenTerms[tok] {
			seenTerms[tok] = true
			normTerms = append(normTerms, tok)
			if len(normTerms) >= 64 {
				break
			}
		}
	}
	normPaths := []string{}
	normStems := []string{}
	seenPaths := map[string]bool{}
	seenStems := map[string]bool{}
	for _, sp := range sourcePaths {
		sp = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(sp), "/"))
		if sp != "" && !seenPaths[sp] {
			seenPaths[sp] = true
			normPaths = append(normPaths, sp)
		}
		base := sp
		if idx := strings.LastIndexByte(base, '/'); idx >= 0 {
			base = base[idx+1:]
		}
		if dot := strings.LastIndexByte(base, '.'); dot > 0 {
			base = base[:dot]
		}
		if len(base) >= 3 && !seenStems[base] {
			seenStems[base] = true
			normStems = append(normStems, base)
		}
	}
	termsJSON := string(jsonBytes(normTerms))
	pathsJSON := string(jsonBytes(normPaths))
	stemsJSON := string(jsonBytes(normStems))

	rows, err := t.tx.QueryContext(t.ctx, `
		WITH read_units AS (`+searchReadUnits+`),
		eligible_units AS (
			SELECT cv.project_id, cv.document_id, cv.version_id, r.page_path, r.region_id, 'region' AS unit_kind, cv.target_set_id, coalesce(d.title,'') AS doc_title, coalesce(CAST(r.metadata AS TEXT),'') AS unit_meta,
				coalesce((SELECT pa.label FROM page_anchors pa WHERE pa.version_id=cv.version_id AND pa.page_path=r.page_path AND pa.anchor_id=r.region_id AND NOT EXISTS(SELECT 1 FROM search_indexes si WHERE si.version_id=cv.version_id AND si.state='pending') AND EXISTS(SELECT 1 FROM read_units psu WHERE psu.version_id=cv.version_id AND coalesce(psu.page_path,'')=r.page_path AND coalesce(psu.region_id,'')='' AND psu.read_state='ready' AND psu.read_body_state='complete') LIMIT 1),'') AS anchor_label
			FROM scan_captured_versions cv
			JOIN documents d ON d.document_id=cv.document_id
			JOIN projects p ON p.project_id=cv.project_id
			JOIN regions r ON r.version_id=cv.version_id
			WHERE cv.job_id=? AND cv.exclusion_reason='' AND cv.enum_state!='purged' AND cv.version_id!=''
			  AND d.lifecycle NOT IN ('trash','purged') AND p.lifecycle='active'
			  AND NOT EXISTS(SELECT 1 FROM purged_versions pv WHERE pv.version_id=cv.version_id)
			UNION ALL
			SELECT cv.project_id, cv.document_id, cv.version_id, vp.page_path, '' AS region_id, 'page' AS unit_kind, cv.target_set_id, coalesce(d.title,'') AS doc_title, coalesce(CAST(vp.metadata AS TEXT),'') AS unit_meta, '' AS anchor_label
			FROM scan_captured_versions cv
			JOIN documents d ON d.document_id=cv.document_id
			JOIN projects p ON p.project_id=cv.project_id
			JOIN version_pages vp ON vp.version_id=cv.version_id
			WHERE cv.job_id=? AND cv.exclusion_reason='' AND cv.enum_state!='purged' AND cv.version_id!=''
			  AND d.lifecycle NOT IN ('trash','purged') AND p.lifecycle='active'
			  AND NOT EXISTS(SELECT 1 FROM purged_versions pv WHERE pv.version_id=cv.version_id)
			  AND NOT EXISTS(SELECT 1 FROM regions r WHERE r.version_id=vp.version_id AND r.page_path=vp.page_path)
			UNION ALL
			SELECT cv.project_id, cv.document_id, cv.version_id, '' AS page_path, '' AS region_id, 'version' AS unit_kind, cv.target_set_id, coalesce(d.title,'') AS doc_title, '' AS unit_meta, '' AS anchor_label
			FROM scan_captured_versions cv
			JOIN documents d ON d.document_id=cv.document_id
			JOIN projects p ON p.project_id=cv.project_id
			WHERE cv.job_id=? AND cv.exclusion_reason='' AND cv.enum_state!='purged' AND cv.version_id!=''
			  AND d.lifecycle NOT IN ('trash','purged') AND p.lifecycle='active'
			  AND NOT EXISTS(SELECT 1 FROM purged_versions pv WHERE pv.version_id=cv.version_id)
			  AND cv.page_count=0 AND cv.region_count=0
		),
		scored_units AS (
			SELECT u.project_id, u.document_id, u.version_id, u.page_path, u.region_id, u.unit_kind, u.target_set_id, u.doc_title,
				CASE WHEN EXISTS(
					SELECT 1 FROM authored_references ar
					WHERE ar.from_version_id=?
					  AND (?='' OR ar.from_page_path IS NULL OR ar.from_page_path=?)
					  AND (?='' OR ar.from_region_id IS NULL OR ar.from_region_id=?)
					  AND (ar.target_version_id=u.version_id OR ar.target_document_id=u.document_id)
					  AND (ar.target_page_path IS NULL OR ar.target_page_path='' OR ar.target_page_path=u.page_path)
					  AND (ar.target_region_id IS NULL OR ar.target_region_id='' OR ar.target_region_id=u.region_id)
				) THEN 1 ELSE 0 END AS ref_hit,
				CASE WHEN EXISTS(
					SELECT 1 FROM evidence ev
					LEFT JOIN region_evidence re ON re.version_id=ev.version_id AND re.evidence_id=ev.evidence_id
					WHERE ev.version_id=u.version_id
					  AND (u.page_path='' OR re.page_path IS NULL OR re.page_path=u.page_path)
					  AND (u.region_id='' OR re.region_id IS NULL OR re.region_id=u.region_id)
					  AND (
						lower(ltrim(coalesce(json_extract(ev.metadata,'$.locator.path'),json_extract(ev.metadata,'$.path'),''),'/')) IN (SELECT value FROM json_each(?))
						OR EXISTS(SELECT 1 FROM json_each(?) st WHERE instr(lower(coalesce(json_extract(ev.metadata,'$.locator.path'),json_extract(ev.metadata,'$.path'),'')), st.value)>0)
					  )
				) THEN 1 ELSE 0 END AS source_hit,
				coalesce((
					SELECT sum(CASE WHEN length(tm.value)>=7 THEN 3 WHEN length(tm.value)>=5 THEN 2 ELSE 1 END) FROM json_each(?) tm
					WHERE instr(lower(u.unit_meta), tm.value)>0
					   OR instr(lower(u.anchor_label), tm.value)>0
					   OR (u.unit_kind='version' AND instr(lower(u.doc_title), tm.value)>0)
					   OR EXISTS(
						   SELECT 1 FROM read_units su
						   LEFT JOIN search_chunks sc ON sc.unit_id=su.unit_id AND (sc.part='metadata' OR su.read_body_state='complete')
						   WHERE su.version_id=u.version_id
						     AND su.read_state='ready'
						     AND NOT EXISTS(SELECT 1 FROM search_indexes si WHERE si.version_id=su.version_id AND si.state='pending')
						     AND (u.page_path='' OR coalesce(su.page_path,'')=u.page_path)
						     AND (
							   (u.region_id!='' AND coalesce(su.region_id,'')=u.region_id)
							   OR (u.region_id='' AND coalesce(su.region_id,'')='')
							   OR (u.region_id!='' AND coalesce(su.region_id,'')='' AND su.read_body_state='complete' AND NOT EXISTS(SELECT 1 FROM read_units su2 WHERE su2.version_id=u.version_id AND coalesce(su2.page_path,'')=u.page_path AND coalesce(su2.region_id,'')=u.region_id))
						     )
						     AND (
							   instr(lower(coalesce(su.title,'')), tm.value)>0
							   OR instr(lower(coalesce(sc.title,'')), tm.value)>0
							   OR instr(' '||lower(coalesce(sc.terms,''))||' ', ' '||tm.value||' ')>0
							   OR (length(tm.value)>=5 AND instr(lower(coalesce(sc.terms,'')), tm.value)>0)
							   OR instr(lower(coalesce(sc.display_body,'')), tm.value)>0
							   OR instr(lower(coalesce(sc.body,'')), tm.value)>0
						     )
					   )
				), 0) AS search_hits
			FROM eligible_units u
			WHERE NOT (u.version_id=? AND u.page_path=? AND u.region_id=?)
		)
		SELECT project_id, document_id, version_id, page_path, region_id, unit_kind, target_set_id, doc_title
		FROM scored_units
		WHERE ref_hit>0 OR source_hit>0 OR search_hits>0
		ORDER BY (ref_hit*100 + source_hit*50 + search_hits) DESC, version_id ASC, page_path ASC, region_id ASC
		LIMIT ?`,
		scanJobID, scanJobID, scanJobID,
		focalVersionID, focalPagePath, focalPagePath, focalRegionID, focalRegionID,
		pathsJSON, stemsJSON, termsJSON,
		focalVersionID, focalPagePath, focalRegionID,
		limit,
	)
	if err != nil {
		return nil, skipped, err
	}
	defer rows.Close()
	var out []domain.CandidateTarget
	for rows.Next() {
		var c domain.CandidateTarget
		if err := rows.Scan(&c.ProjectID, &c.DocumentID, &c.VersionID, &c.PagePath, &c.RegionID, &c.UnitKind, &c.TargetSetID, &c.Title); err != nil {
			return nil, skipped, err
		}
		out = append(out, c)
	}
	return out, skipped, rows.Err()
}

func nullablePtr(p *string) any {
	if p == nil || *p == "" {
		return nil
	}
	return *p
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
