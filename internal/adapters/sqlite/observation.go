package sqlite

import (
	"context"
	"database/sql"
	"sort"

	"haeryong.local/haeryong/internal/domain"
)

// Observation-run, per-source observation, coverage and trigger persistence
// (TASK-004-01 M3). Every write is append-only; only check intents carry a
// mutable state.

func (t *writeTx) PutObservationRun(r domain.ObservationRun) error {
	return t.exec("INSERT INTO observation_runs VALUES(?,?,?,?,?,?,?,?)", r.ID, r.VersionID, r.PrincipalID, r.OperationID, r.RequestedSelection, r.ScopeJSON, nullableString(r.TargetSetID), r.CreatedAt)
}

func (t *writeTx) PutSourceObservation(o domain.SourceObservation) error {
	return t.exec("INSERT INTO source_observations VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", o.ID, o.RunID, o.VersionID, nullableString(o.TargetSetID), o.SourceID, o.RepoID, o.BaseRevision, nullableString(o.ObservedRevision), o.Availability, o.Classification, nullableBytes(o.ChangedPathsJSON), nullableBytes(o.DependencyScopeJSON), o.Reason, o.ConfigRevision, o.PermissionsRevision, o.ObservedAt, o.OperationID)
}

func (t *writeTx) PutReassessmentTrigger(g domain.ReassessmentTrigger) error {
	return t.exec("INSERT INTO reassessment_triggers VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)", g.ID, g.RunID, g.VersionID, g.TargetSetID, g.SourceID, g.AnchorKind, g.PagePath, g.RegionID, g.Reason, nullableBytes(g.ChangedPathsJSON), g.ObservationID, g.ObservedAt, g.CreatedAt)
}

func (t *writeTx) PutCoverageSet(c domain.CoverageSet) error {
	return t.exec("INSERT INTO coverage_sets VALUES(?,?,?,?,?,?,?,?)", c.ID, c.VersionID, nullableString(c.TargetSetID), nullableString(c.RunID), c.ScopeJSON, c.UnitKind, c.CapturedAt, c.Origin)
}

func (t *writeTx) PutRequiredUnit(u domain.RequiredUnit) error {
	return t.exec("INSERT INTO required_units VALUES(?,?,?,?,?,?)", u.CoverageSetID, u.Kind, u.PagePath, u.RegionID, u.Reason, nullableString(u.TargetSetID))
}

// runColumns lists observation_runs in scan order; committed derives from the
// per-source rows so a run whose staged protocol crashed after the target
// persist is never a selection candidate (addendum r4.1 rule 4).
const runColumns = "run_id,version_id,principal_id,operation_id,requested_selection,scope_json,target_set_id,created_at,EXISTS(SELECT 1 FROM source_observations o WHERE o.run_id=observation_runs.run_id)"

func scanRun(scan func(...any) error) (domain.ObservationRun, error) {
	var r domain.ObservationRun
	var targetSet sql.NullString
	err := scan(&r.ID, &r.VersionID, &r.PrincipalID, &r.OperationID, &r.RequestedSelection, &r.ScopeJSON, &targetSet, &r.CreatedAt, &r.Committed)
	if targetSet.Valid {
		r.TargetSetID = targetSet.String
	}
	return r, err
}

func (t *writeTx) ObservationRunsByVersion(versionID string) ([]domain.ObservationRun, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT "+runColumns+" FROM observation_runs WHERE version_id=? ORDER BY created_at,run_id", versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ObservationRun{}
	for rows.Next() {
		r, err := scanRun(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *writeTx) ObservationRunByID(runID string) (domain.ObservationRun, error) {
	r, err := scanRun(t.tx.QueryRowContext(t.ctx, "SELECT "+runColumns+" FROM observation_runs WHERE run_id=?", runID).Scan)
	return r, notFound(err)
}

func (t *writeTx) ObservationRunByTargetSet(targetSetID string) (domain.ObservationRun, bool, error) {
	r, err := scanRun(t.tx.QueryRowContext(t.ctx, "SELECT "+runColumns+" FROM observation_runs WHERE target_set_id=? ORDER BY created_at,run_id LIMIT 1", targetSetID).Scan)
	if err != nil {
		if err == sql.ErrNoRows {
			return domain.ObservationRun{}, false, nil
		}
		return domain.ObservationRun{}, false, err
	}
	return r, true, nil
}

func (t *writeTx) ObservationRunForOperation(principalID, operationID string) (domain.ObservationRun, bool, error) {
	r, err := scanRun(t.tx.QueryRowContext(t.ctx, "SELECT "+runColumns+" FROM observation_runs WHERE principal_id=? AND operation_id=?", principalID, operationID).Scan)
	if err != nil {
		if err == sql.ErrNoRows {
			return domain.ObservationRun{}, false, nil
		}
		return domain.ObservationRun{}, false, err
	}
	return r, true, nil
}

// observationColumns lists source_observations in scan order.
const observationColumns = "observation_id,run_id,version_id,target_set_id,source_id,repo_id,base_revision,observed_revision,availability,classification,changed_paths_json,dependency_scope_json,reason,config_revision,permissions_revision,observed_at,operation_id"

func scanObservation(scan func(...any) error) (domain.SourceObservation, error) {
	var o domain.SourceObservation
	var targetSet, observed sql.NullString
	var changed, dependency []byte
	err := scan(&o.ID, &o.RunID, &o.VersionID, &targetSet, &o.SourceID, &o.RepoID, &o.BaseRevision, &observed, &o.Availability, &o.Classification, &changed, &dependency, &o.Reason, &o.ConfigRevision, &o.PermissionsRevision, &o.ObservedAt, &o.OperationID)
	if targetSet.Valid {
		o.TargetSetID = targetSet.String
	}
	if observed.Valid {
		o.ObservedRevision = observed.String
	}
	o.ChangedPathsJSON, o.DependencyScopeJSON = changed, dependency
	return o, err
}

// rowsReader is the read shape shared by store- and tx-level row queries.
type rowsReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func observationsWhere(ctx context.Context, q rowsReader, where string, args ...any) ([]domain.SourceObservation, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+observationColumns+" FROM source_observations WHERE "+where+" ORDER BY source_id,observed_at,observation_id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.SourceObservation{}
	for rows.Next() {
		o, err := scanObservation(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (t *writeTx) ObservationsByRun(runID string) ([]domain.SourceObservation, error) {
	return observationsWhere(t.ctx, t.tx, "run_id=?", runID)
}

func (t *writeTx) ObservationsByOperation(principalID, operationID string) ([]domain.SourceObservation, error) {
	return observationsWhere(t.ctx, t.tx, "operation_id=? AND run_id IN (SELECT run_id FROM observation_runs WHERE principal_id=?)", operationID, principalID)
}

func (t *writeTx) TriggersByTargetSet(versionID, targetSetID string) ([]domain.ReassessmentTrigger, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT trigger_id,run_id,version_id,target_set_id,source_id,anchor_kind,page_path,region_id,reason,changed_paths_json,observation_id,observed_at,created_at FROM reassessment_triggers WHERE version_id=? AND target_set_id=? AND NOT EXISTS(SELECT 1 FROM assessment_trigger_resolutions x WHERE x.trigger_id=reassessment_triggers.trigger_id) ORDER BY anchor_kind,page_path,region_id,trigger_id", versionID, targetSetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ReassessmentTrigger{}
	for rows.Next() {
		var g domain.ReassessmentTrigger
		var changed []byte
		if err := rows.Scan(&g.ID, &g.RunID, &g.VersionID, &g.TargetSetID, &g.SourceID, &g.AnchorKind, &g.PagePath, &g.RegionID, &g.Reason, &changed, &g.ObservationID, &g.ObservedAt, &g.CreatedAt); err != nil {
			return nil, err
		}
		g.ChangedPathsJSON = changed
		out = append(out, g)
	}
	return out, rows.Err()
}

func scanCoverageSet(scan func(...any) error) (domain.CoverageSet, error) {
	var c domain.CoverageSet
	var targetSet, runID sql.NullString
	err := scan(&c.ID, &c.VersionID, &targetSet, &runID, &c.ScopeJSON, &c.UnitKind, &c.CapturedAt, &c.Origin)
	if targetSet.Valid {
		c.TargetSetID = targetSet.String
	}
	if runID.Valid {
		c.RunID = runID.String
	}
	return c, err
}

func (t *writeTx) CoverageSetByRun(runID string) (domain.CoverageSet, error) {
	c, err := scanCoverageSet(t.tx.QueryRowContext(t.ctx, "SELECT coverage_set_id,version_id,target_set_id,run_id,scope_json,unit_kind,captured_at,origin FROM coverage_sets WHERE run_id=? ORDER BY captured_at,coverage_set_id LIMIT 1", runID).Scan)
	return c, notFound(err)
}

func (t *writeTx) CoverageSetByID(id string) (domain.CoverageSet, error) {
	c, err := scanCoverageSet(t.tx.QueryRowContext(t.ctx, "SELECT coverage_set_id,version_id,target_set_id,run_id,scope_json,unit_kind,captured_at,origin FROM coverage_sets WHERE coverage_set_id=?", id).Scan)
	return c, notFound(err)
}

// RequiredUnits keyset-pages the captured denominator by the anchor tuple.
func (t *writeTx) RequiredUnits(coverageSetID, afterKind, afterPage, afterRegion string, limit int) ([]domain.RequiredUnit, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT coverage_set_id,kind,page_path,region_id,reason,target_set_id FROM required_units WHERE coverage_set_id=? AND (kind,page_path,region_id)>(?,?,?) ORDER BY kind,page_path,region_id LIMIT ?", coverageSetID, afterKind, afterPage, afterRegion, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.RequiredUnit{}
	for rows.Next() {
		var u domain.RequiredUnit
		var targetSet sql.NullString
		if err := rows.Scan(&u.CoverageSetID, &u.Kind, &u.PagePath, &u.RegionID, &u.Reason, &targetSet); err != nil {
			return nil, err
		}
		if targetSet.Valid {
			u.TargetSetID = targetSet.String
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (t *writeTx) TargetSetByID(id string) (domain.TargetSet, error) {
	var ts domain.TargetSet
	err := t.tx.QueryRowContext(t.ctx, "SELECT target_set_id,project_id,requested_selection,targets_json,captured_at FROM captured_target_sets WHERE target_set_id=?", id).Scan(&ts.ID, &ts.ProjectID, &ts.RequestedSelection, &ts.TargetsJSON, &ts.CapturedAt)
	return ts, notFound(err)
}

// VersionCapture loads a target set only when this version's committed
// records reference it: a verification of this version's evidence or a
// committed observation run of this version (plan §7.1 step 1). A capture
// referenced only by a run that never committed stays unknown (r4.1 rule 4,
// W3b L14).
func (t *writeTx) VersionCapture(versionID, targetSetID string) (domain.TargetSet, error) {
	var ts domain.TargetSet
	err := t.tx.QueryRowContext(t.ctx, "SELECT target_set_id,project_id,requested_selection,targets_json,captured_at FROM captured_target_sets WHERE target_set_id=? AND (EXISTS(SELECT 1 FROM citation_verifications v WHERE v.target_set_id=? AND v.version_id=?) OR EXISTS(SELECT 1 FROM observation_runs r WHERE r.target_set_id=? AND r.version_id=? AND EXISTS(SELECT 1 FROM source_observations o WHERE o.run_id=r.run_id)) OR EXISTS(SELECT 1 FROM coverage_sets c JOIN source_captures sc ON sc.target_set_id=c.target_set_id WHERE c.version_id=? AND c.target_set_id=captured_target_sets.target_set_id AND c.origin LIKE 'freshness:%' AND sc.state='completed'))", targetSetID, targetSetID, versionID, targetSetID, versionID, versionID).Scan(&ts.ID, &ts.ProjectID, &ts.RequestedSelection, &ts.TargetsJSON, &ts.CapturedAt)
	return ts, notFound(err)
}

// VerificationCaptures lists the pinned-family captures of this version:
// captured target sets referenced by a verification of this version and by no
// observation run (a dangling reservation capture is never a candidate,
// addendum r4.1 rule 4).
func (t *writeTx) VerificationCaptures(versionID, projectID string) ([]domain.TargetSet, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT ts.target_set_id,ts.project_id,ts.requested_selection,ts.targets_json,ts.captured_at FROM captured_target_sets ts WHERE ts.project_id=? AND json_extract(ts.requested_selection,'$.mode')='pinned' AND EXISTS(SELECT 1 FROM citation_verifications v WHERE v.target_set_id=ts.target_set_id AND v.version_id=?) AND NOT EXISTS(SELECT 1 FROM observation_runs r WHERE r.target_set_id=ts.target_set_id) ORDER BY ts.captured_at,ts.target_set_id", projectID, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.TargetSet{}
	for rows.Next() {
		var ts domain.TargetSet
		if err := rows.Scan(&ts.ID, &ts.ProjectID, &ts.RequestedSelection, &ts.TargetsJSON, &ts.CapturedAt); err != nil {
			return nil, err
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}

func (t *writeTx) VersionSources(versionID string) ([]domain.Source, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT version_id,source_id,metadata FROM version_sources WHERE version_id=? ORDER BY source_id", versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Source{}
	for rows.Next() {
		var s domain.Source
		if err := rows.Scan(&s.VersionID, &s.ID, &s.Metadata); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (t *writeTx) VersionEvidence(versionID string) ([]domain.Evidence, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT version_id,evidence_id,source_id,metadata FROM evidence WHERE version_id=? ORDER BY evidence_id", versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Evidence{}
	for rows.Next() {
		var e domain.Evidence
		if err := rows.Scan(&e.VersionID, &e.ID, &e.SourceID, &e.Metadata); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (t *writeTx) VersionPages(versionID string) ([]domain.Page, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT version_id,page_path,metadata FROM version_pages WHERE version_id=? ORDER BY page_path", versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Page{}
	for rows.Next() {
		var p domain.Page
		if err := rows.Scan(&p.VersionID, &p.Path, &p.Metadata); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (t *writeTx) VersionRegion(versionID, pagePath, regionID string) (domain.Region, error) {
	var r domain.Region
	err := t.tx.QueryRowContext(t.ctx, "SELECT version_id,page_path,region_id,metadata FROM regions WHERE version_id=? AND page_path=? AND region_id=?", versionID, pagePath, regionID).Scan(&r.VersionID, &r.PagePath, &r.ID, &r.Metadata)
	return r, notFound(err)
}

func (t *writeTx) VersionRegionEvidence(versionID string) ([]domain.RegionEvidence, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT version_id,page_path,region_id,evidence_id FROM region_evidence WHERE version_id=? ORDER BY page_path,region_id,evidence_id", versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.RegionEvidence{}
	for rows.Next() {
		var r domain.RegionEvidence
		if err := rows.Scan(&r.VersionID, &r.PagePath, &r.RegionID, &r.EvidenceID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SourceObservationSummaries derives, per configured (project, source), the
// latest per-source observation row of observation runs plus the latest row
// in which that source itself was captured (S30/r4.2 rule 3).
func (s *Store) SourceObservationSummaries(ctx context.Context) ([]domain.SourceStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, "SELECT d.project_id,o.source_id,o.observed_at,o.availability,o.reason,o.observed_revision FROM source_observations o JOIN observation_runs r ON r.run_id=o.run_id JOIN document_versions v ON v.version_id=r.version_id JOIN documents d ON d.document_id=v.document_id ORDER BY o.source_id,o.observed_at,o.observation_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type fold struct {
		at, availability, reason string
	}
	latest := map[[2]string]fold{}
	revisions := map[[2]string]string{}
	for rows.Next() {
		var project, source, at, availability string
		var reason sql.NullString
		var observed sql.NullString
		if err := rows.Scan(&project, &source, &at, &availability, &reason, &observed); err != nil {
			return nil, err
		}
		key := [2]string{project, source}
		latest[key] = fold{at: at, availability: availability}
		if reason.Valid {
			latest[key] = fold{at: at, availability: availability, reason: reason.String}
		}
		if observed.Valid && observed.String != "" {
			revisions[key] = observed.String
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]domain.SourceStatus, 0, len(latest))
	for key, value := range latest {
		status := domain.SourceStatus{ProjectID: key[0], SourceID: key[1], LastObservedAt: value.at, LastAvailability: value.availability, LastReason: value.reason, LastRevision: revisions[key], Observed: true}
		out = append(out, status)
	}
	// Deterministic order for every caller.
	sortStatuses(out)
	return out, nil
}

func sortStatuses(out []domain.SourceStatus) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProjectID != out[j].ProjectID {
			return out[i].ProjectID < out[j].ProjectID
		}
		return out[i].SourceID < out[j].SourceID
	})
}

// LatestObservationBySource is the WriteTx form of the summaries restricted
// to one project, for list reads already inside the serialized transaction.
func (t *writeTx) LatestObservationBySource(projectID string) ([]domain.SourceStatus, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT o.source_id,o.observed_at,o.availability,o.reason,o.observed_revision FROM source_observations o JOIN observation_runs r ON r.run_id=o.run_id JOIN document_versions v ON v.version_id=r.version_id JOIN documents d ON d.document_id=v.document_id WHERE d.project_id=? ORDER BY o.source_id,o.observed_at,o.observation_id", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type fold struct {
		at, availability, reason string
	}
	latest := map[string]fold{}
	revisions := map[string]string{}
	for rows.Next() {
		var source, at, availability string
		var reason, observed sql.NullString
		if err := rows.Scan(&source, &at, &availability, &reason, &observed); err != nil {
			return nil, err
		}
		latest[source] = fold{at: at, availability: availability}
		if reason.Valid {
			latest[source] = fold{at: at, availability: availability, reason: reason.String}
		}
		if observed.Valid && observed.String != "" {
			revisions[source] = observed.String
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]domain.SourceStatus, 0, len(latest))
	for source, value := range latest {
		out = append(out, domain.SourceStatus{ProjectID: projectID, SourceID: source, LastObservedAt: value.at, LastAvailability: value.availability, LastReason: value.reason, LastRevision: revisions[source], Observed: true})
	}
	sortStatuses(out)
	return out, nil
}
