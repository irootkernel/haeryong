package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"time"
)

const freshnessMigration = `
CREATE TABLE assessment_trigger_resolutions(trigger_id TEXT PRIMARY KEY REFERENCES reassessment_triggers(trigger_id),assessment_id TEXT NOT NULL REFERENCES assessments(assessment_id));
CREATE INDEX assessments_by_version_time ON assessments(version_id,created_at,assessment_id);
CREATE INDEX assessment_units_by_target ON assessment_units(target_set_id,kind,page_path,region_id,assessment_id);
`

func (t *writeTx) PutAssessment(a domain.Assessment, units []domain.AssessmentUnit) error {
	set, err := t.CoverageSetByID(a.CoverageSetID)
	if err != nil {
		return err
	}
	if a.ID == "" || set.VersionID != a.VersionID || set.TargetSetID != a.TargetSetID || string(set.ScopeJSON) != string(a.ScopeJSON) || len(units) == 0 {
		return domain.ErrInvalidTarget
	}
	if err = t.exec(`INSERT INTO assessments VALUES(?,?, '', '',?,?,?,?,?,?,?,?,?)`, a.ID, a.VersionID, a.TargetSetID, a.CoverageSetID, a.State, a.ScopeJSON, a.EvidenceJSON, a.ConfigRevision, a.PermissionsRevision, a.Origin, a.AssessedAt); err != nil {
		return err
	}
	seen := map[string]bool{}
	required, err := app.AllRequiredUnits(t, set.ID)
	if err != nil {
		return err
	}
	if len(required) != len(units) {
		return domain.ErrInvalidTarget
	}
	for _, u := range units {
		key := u.Kind + "\x00" + u.PagePath + "\x00" + u.RegionID
		if seen[key] || u.AssessmentID != a.ID || u.CoverageSetID != set.ID || u.TargetSetID != set.TargetSetID {
			return domain.ErrInvalidTarget
		}
		seen[key] = true
		found := false
		for _, r := range required {
			if r.Kind == u.Kind && r.PagePath == u.PagePath && r.RegionID == u.RegionID {
				found = true
				break
			}
		}
		if !found || (u.Outcome != "assessed" && u.Outcome != "partial" && u.Outcome != "blocked" && u.Outcome != "not_checked") || (u.Outcome != "assessed" && u.Reason == "") {
			return domain.ErrInvalidTarget
		}
		if err = t.exec(`INSERT INTO assessment_units VALUES(?,?,?,?,?,?,?,?,?)`, set.ID, a.ID, a.TargetSetID, u.Kind, u.PagePath, u.RegionID, u.Outcome, nullableString(u.Discrepancy), nullableString(u.Reason)); err != nil {
			return err
		}
	}
	return nil
}

func (t *writeTx) AssessmentsByVersion(version, afterTime, afterID string, limit int) ([]domain.Assessment, error) {
	afterKey := ""
	if afterTime != "" {
		at, err := time.Parse(time.RFC3339Nano, afterTime)
		if err != nil {
			return nil, domain.ErrInvalidTarget
		}
		afterKey = at.UTC().Format("2006-01-02T15:04:05.000000000")
	}
	stamp := assessmentInstantSQL("created_at")
	rows, err := t.tx.QueryContext(t.ctx, `SELECT assessment_id,version_id,target_set_id,coverage_set_id,state,scope_json,evidence_json,config_revision,permissions_revision,origin,created_at FROM assessments WHERE version_id=? AND (`+stamp+`>? OR (`+stamp+`=? AND assessment_id>?)) ORDER BY `+stamp+`,assessment_id LIMIT ?`, version, afterKey, afterKey, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Assessment{}
	for rows.Next() {
		var a domain.Assessment
		if err = rows.Scan(&a.ID, &a.VersionID, &a.TargetSetID, &a.CoverageSetID, &a.State, &a.ScopeJSON, &a.EvidenceJSON, &a.ConfigRevision, &a.PermissionsRevision, &a.Origin, &a.AssessedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (t *writeTx) CoverageSetsByVersion(version, target string) ([]domain.CoverageSet, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT coverage_set_id,version_id,coalesce(target_set_id,''),coalesce(run_id,''),scope_json,unit_kind,captured_at,origin FROM coverage_sets WHERE version_id=? AND (?='' OR target_set_id=?) ORDER BY captured_at,coverage_set_id`, version, target, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.CoverageSet{}
	for rows.Next() {
		var s domain.CoverageSet
		if err = rows.Scan(&s.ID, &s.VersionID, &s.TargetSetID, &s.RunID, &s.ScopeJSON, &s.UnitKind, &s.CapturedAt, &s.Origin); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (t *writeTx) LatestAssessmentUnit(version, target string, r domain.RequiredUnit) (domain.AssessmentUnit, bool, error) {
	var u domain.AssessmentUnit
	err := t.tx.QueryRowContext(t.ctx, `SELECT u.coverage_set_id,u.assessment_id,u.target_set_id,u.kind,u.page_path,u.region_id,u.outcome,coalesce(u.discrepancy,''),coalesce(u.reason,''),a.created_at FROM assessment_units u JOIN assessments a USING(assessment_id) JOIN required_units r ON r.coverage_set_id=u.coverage_set_id AND r.kind=u.kind AND r.page_path=u.page_path AND r.region_id=u.region_id WHERE a.version_id=? AND a.target_set_id=? AND u.target_set_id=? AND u.kind=? AND u.page_path=? AND u.region_id=? ORDER BY `+assessmentInstantSQL("a.created_at")+` DESC,a.assessment_id DESC LIMIT 1`, version, target, target, r.Kind, r.PagePath, r.RegionID).Scan(&u.CoverageSetID, &u.AssessmentID, &u.TargetSetID, &u.Kind, &u.PagePath, &u.RegionID, &u.Outcome, &u.Discrepancy, &u.Reason, &u.AssessedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return u, false, nil
	}
	return u, err == nil, err
}

// These timestamps are service-owned UTC RFC3339Nano. Pad only the comparison
// key: raw variable-width fractions are not lexically chronological, and the
// stored/public timestamp must retain its original value and precision.
func assessmentInstantSQL(column string) string {
	return "(substr(" + column + ",1,19)||'.'||substr(rtrim(substr(" + column + ",21),'Z')||'000000000',1,9))"
}

func (t *writeTx) ClearAssessmentTriggers(a domain.Assessment) error {
	at, err := time.Parse(time.RFC3339Nano, a.AssessedAt)
	if err != nil {
		return domain.ErrContentIntegrity
	}
	cutoff := at.UTC().Format("2006-01-02T15:04:05.000000000")
	// Only fully assessed exact required tuples clear matching older triggers.
	if e := t.exec(`INSERT OR IGNORE INTO assessment_trigger_resolutions SELECT g.trigger_id,? FROM reassessment_triggers g WHERE g.version_id=? AND g.target_set_id=? AND `+assessmentInstantSQL("g.created_at")+`<=? AND EXISTS(SELECT 1 FROM assessment_units u WHERE u.assessment_id=? AND u.outcome='assessed' AND ((u.kind='version') OR (u.kind='page' AND g.anchor_kind IN ('page','region') AND g.page_path=u.page_path) OR (u.kind='region' AND g.anchor_kind='region' AND g.page_path=u.page_path AND g.region_id=u.region_id)))`, a.ID, a.VersionID, a.TargetSetID, cutoff, a.ID); e != nil {
		return e
	}
	// A version-anchored trigger is covered only after every member of an
	// actual full-version fixed set has a fully assessed matching contribution.
	sets, e := t.CoverageSetsByVersion(a.VersionID, a.TargetSetID)
	if e != nil {
		return e
	}
	for _, set := range sets {
		var scope struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(set.ScopeJSON, &scope) != nil {
			return domain.ErrContentIntegrity
		}
		if scope.Kind != "version" {
			continue
		}
		units, e := app.AllRequiredUnits(t, set.ID)
		if e != nil {
			return e
		}
		complete := len(units) > 0
		for _, r := range units {
			u, ok, e := t.LatestAssessmentUnit(a.VersionID, a.TargetSetID, r)
			if e != nil {
				return e
			}
			if !ok || u.Outcome != "assessed" {
				complete = false
				break
			}
		}
		if complete {
			return t.exec(`INSERT OR IGNORE INTO assessment_trigger_resolutions SELECT trigger_id,? FROM reassessment_triggers WHERE version_id=? AND target_set_id=? AND anchor_kind='version' AND `+assessmentInstantSQL("created_at")+`<=?`, a.ID, a.VersionID, a.TargetSetID, cutoff)
		}
	}
	return nil
}

func (t *writeTx) FreshnessIntent(scan, version string) (domain.MaintenanceIntent, bool, error) {
	var id string
	err := t.tx.QueryRowContext(t.ctx, `SELECT job_id FROM maintenance_intents WHERE scan_job_id=? AND json_extract(value,'$.mode')=? AND json_extract(value,'$.version_id')=? ORDER BY job_id LIMIT 1`, scan, domain.MaintenanceModeFreshness, version).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.MaintenanceIntent{}, false, nil
	}
	if err != nil {
		return domain.MaintenanceIntent{}, false, err
	}
	m, err := t.MaintenanceIntent(id)
	return m, err == nil, err
}

// Membership is immutable; only a scan-owned uncompleted unit's actual work
// reason changes. Source-observation coverage cannot be edited through this path.
func (t *writeTx) SetFreshnessUnitReason(set string, unit domain.RequiredUnit, reason string) error {
	return t.exec(`UPDATE required_units SET reason=? WHERE coverage_set_id=? AND kind=? AND page_path=? AND region_id=? AND EXISTS(SELECT 1 FROM coverage_sets c WHERE c.coverage_set_id=required_units.coverage_set_id AND c.origin LIKE 'freshness:%')`, reason, set, unit.Kind, unit.PagePath, unit.RegionID)
}

func (t *writeTx) BindFreshnessCoverage(m *domain.MaintenanceIntent, target string) error {
	ids := map[string]string{}
	for _, oldID := range []string{m.FullCoverageSetID, m.CoverageSetID} {
		if oldID == "" {
			continue
		}
		if _, ok := ids[oldID]; ok {
			continue
		}
		old, e := t.CoverageSetByID(oldID)
		if e != nil {
			return e
		}
		ids[oldID] = oldID
		if old.TargetSetID == target {
			continue
		}
		ts, e := t.TargetSetByID(target)
		if e != nil {
			return e
		}
		units, e := app.AllRequiredUnits(t, oldID)
		if e != nil {
			return e
		}
		old.ID, e = service.NewID()
		if e != nil {
			return e
		}
		old.TargetSetID = target
		old.CapturedAt = ts.CapturedAt
		old.RunID = ""
		if e = t.PutCoverageSet(old); e != nil {
			return e
		}
		for _, u := range units {
			u.CoverageSetID = old.ID
			u.TargetSetID = target
			if e = t.PutRequiredUnit(u); e != nil {
				return e
			}
		}
		ids[oldID] = old.ID
	}
	m.CoverageSetID = ids[m.CoverageSetID]
	if m.FullCoverageSetID != "" {
		m.FullCoverageSetID = ids[m.FullCoverageSetID]
	}
	return nil
}

// The scan result describes completed semantic work, including a validated
// proposed result. Applicable views independently count only published rows.
func (t *writeTx) refreshFreshnessCounts(run *domain.ScanRun) error {
	intents, err := t.MaintenanceIntentsByScan(run.JobID)
	if err != nil {
		return err
	}
	counts := domain.ScanSemanticCounts{Status: "not_assessed"}
	// Job work can include a validated proposal; published applicable state is
	// composed separately. Recompute exact discrepancy-bearing tuples so a
	// report refresh or linked retry never increments this public counter twice.
	outdated := map[string]bool{}
	blocked := false
	// The captured roster fixes eligibility even when no source/provider child
	// can be admitted. Never substitute Task02 region/candidate work counts.
	after := 0
	for {
		versions, e := t.ScanCapturedVersions(run.JobID, after, 64)
		if e != nil {
			return e
		}
		if len(versions) == 0 {
			break
		}
		for _, v := range versions {
			after = v.VersionOrdinal
			if v.VersionID == "" {
				continue
			}
			if run.RequestedScope.PagePath != "" || run.RequestedScope.RegionID != "" || v.PageCount == 0 {
				counts.Eligible++
			} else {
				counts.Eligible += v.PageCount
			}
		}
	}
	for _, m := range intents {
		if m.Mode != domain.MaintenanceModeFreshness {
			continue
		}
		units, err := app.AllRequiredUnits(t, m.CoverageSetID)
		if err != nil {
			return err
		}
		j, err := t.Job(m.JobID)
		if err != nil {
			return err
		}
		if j.State != "completed" {
			blocked = blocked || j.State == "paused" || j.State == "failed"
			counts.Unresolved += len(units)
			continue
		}
		var p domain.PreparedFreshness
		if json.Unmarshal(m.PreparedOutputJSON, &p) != nil {
			return domain.ErrContentIntegrity
		}
		for _, u := range p.Output.Units {
			if u.Outcome == "assessed" {
				counts.Assessed++
			} else if u.Outcome == "not_checked" {
				counts.Skipped++
			} else {
				counts.Unresolved++
			}
		}
	}
	if missing := counts.Eligible - counts.Assessed - counts.Skipped - counts.Unresolved; missing > 0 {
		counts.Skipped += missing
	}
	counts.Complete = counts.Eligible > 0 && counts.Assessed == counts.Eligible && !run.Report.BudgetExhausted
	if counts.Complete {
		counts.Status = "aligned_within_scope"
	} else {
		counts.Reason = "incomplete_semantic_coverage"
	}
	for _, m := range intents {
		if m.Mode != domain.MaintenanceModeFreshness {
			continue
		}
		j, err := t.Job(m.JobID)
		if err != nil {
			return err
		}
		if j.State != "completed" {
			continue
		}
		var p domain.PreparedFreshness
		if json.Unmarshal(m.PreparedOutputJSON, &p) != nil {
			return domain.ErrContentIntegrity
		}
		for _, u := range p.Output.Units {
			if u.Discrepancy != "" {
				counts.Status = "outdated_against_target"
				page, region := "", ""
				if u.PagePath != nil {
					page = *u.PagePath
				}
				if u.RegionID != nil {
					region = *u.RegionID
				}
				key, _ := json.Marshal([]string{m.VersionID, u.Kind, page, region})
				outdated[string(key)] = true
			}
		}
	}
	if blocked && counts.Status != "outdated_against_target" {
		counts.Status = "unable_to_verify"
		counts.Reason = "structural_or_source_blocked"
	}
	if run.Report.BudgetExhausted {
		counts.Reason = "run_budget_exhausted"
	}
	run.Report.Semantic = counts
	run.Report.OutdatedUnits = len(outdated)
	return nil
}
