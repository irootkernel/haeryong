package query

import (
	"encoding/json"
	"errors"
	appsource "haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"strings"
	"time"
)

// composeApplicable preserves observation attempt/capture selection and overlays
// only published, exact-target contributions. Metadata reads perform no source IO.
func (s *Service) composeApplicable(tx app.WriteTx, el appsource.Eligibility, q AssessmentQuery, intent any) (any, error) {
	base, err := s.composeObservedApplicable(tx, el, q, intent)
	// A managed semantic capture is independently committed by the source service;
	// it must not be disguised as a source observation or author verification.
	if err != nil {
		var missing *Missing
		if !errors.As(err, &missing) || q.TargetSetID == "" {
			return nil, err
		}
	}
	view, _ := base.(map[string]any)
	blocked := view != nil && view["status"] == "unable_to_verify"
	sets, e := tx.CoverageSetsByVersion(el.Version.ID, q.TargetSetID)
	if e != nil {
		return nil, e
	}
	var selected *domain.CoverageSet
	var unavailable *domain.CoverageSet
	var unattempted *domain.CoverageSet
	mode := "pinned"
	if intent != "historical" {
		if mode, e = projectSourceDefault(tx, el.ProjectID); e != nil {
			return nil, e
		}
	}
	for i := range sets {
		set := &sets[i]
		if !scopeMatchesRequest(decodeStoredScope(set.ScopeJSON), q) {
			continue
		}
		// An observer-only set is already composed with its exact A/C attempt
		// attribution. It is not a semantic replacement for that composition.
		if set.Origin == "source_observation" {
			units, e := app.AllRequiredUnits(tx, set.ID)
			if e != nil {
				return nil, e
			}
			published := false
			for _, unit := range units {
				if _, ok, e := tx.LatestAssessmentUnit(el.Version.ID, set.TargetSetID, unit); e != nil {
					return nil, e
				} else if ok {
					published = true
					break
				}
			}
			if !published {
				continue
			}
		}
		// Failed acquisition is an attempted fixed scope, not a source capture
		// or semantic contribution. Its metadata target must never be promoted
		// into a verified empty target. Explicit capture queries stay strict.
		if q.TargetSetID == "" {
			m, failed, e := failedFreshnessAcquisition(tx, *set)
			if e != nil {
				return nil, e
			}
			if failed && m.SourceMode == mode {
				if unavailable == nil || freshnessTimeCompare(set.CapturedAt, unavailable.CapturedAt) > 0 || freshnessTimeCompare(set.CapturedAt, unavailable.CapturedAt) == 0 && set.ID > unavailable.ID {
					unavailable = set
				}
				continue
			}
		}
		if strings.HasPrefix(set.Origin, "freshness:") {
			if _, e := tx.VersionCapture(el.Version.ID, set.TargetSetID); errors.Is(e, domain.ErrNotFound) {
				if cv, genuine, e := capturedFreshnessMembership(tx, *set); e != nil {
					return nil, e
				} else if genuine && (q.TargetSetID != "" || cv.SelectedMode == mode) {
					if unattempted == nil || freshnessTimeCompare(set.CapturedAt, unattempted.CapturedAt) > 0 || freshnessTimeCompare(set.CapturedAt, unattempted.CapturedAt) == 0 && set.ID > unattempted.ID {
						unattempted = set
					}
				}
				continue
			} else if e != nil {
				return nil, e
			}
		}
		ts, e := tx.TargetSetByID(set.TargetSetID)
		if e != nil {
			continue
		}
		if q.TargetSetID == "" && (view == nil || view["target_set_id"] != set.TargetSetID) {
			matches, e := semanticSelectionMatches(tx, el, ts, mode)
			if e != nil {
				return nil, e
			}
			if !matches {
				continue
			}
			if view != nil && view["captured_at"] != nil && freshnessTimeCompare(ts.CapturedAt, view["captured_at"].(string)) < 0 {
				continue
			}
		}
		if _, e = tx.VersionCapture(el.Version.ID, set.TargetSetID); e != nil {
			continue
		}
		if selected == nil || freshnessTimeCompare(set.CapturedAt, selected.CapturedAt) > 0 || (freshnessTimeCompare(set.CapturedAt, selected.CapturedAt) == 0 && set.ID > selected.ID) {
			selected = set
		}
	}
	if selected == nil {
		if unavailable != nil {
			return s.unavailableFreshness(tx, view, q, intent, *unavailable)
		}
		if unattempted != nil && !blocked {
			// Unattempted membership supplies no new observation or capture.
			// Keep the existing C/A composition and its attempt-bound locator.
			if view != nil {
				coverage, _ := view["coverage"].(map[string]any)
				if view["target_set_id"] != nil || coverage["coverage_set_id"] != nil {
					return base, err
				}
			}
			units, e := app.AllRequiredUnits(tx, unattempted.ID)
			if e != nil {
				return nil, e
			}
			view = noCaptureApplicable(q, intent)
			view["coverage"] = map[string]any{"coverage_set_id": unattempted.ID, "unit_kind": unattempted.UnitKind, "eligible": len(units), "assessed": 0, "complete": false, "units_url": "/api/v1/versions/" + el.Version.ID + "/assessments/coverage?coverage_set_id=" + unattempted.ID}
			return view, nil
		}
		return base, err
	}
	set := *selected
	if blocked && view["target_set_id"] != nil && view["target_set_id"] != set.TargetSetID {
		return base, nil
	}
	if view == nil || view["target_set_id"] != set.TargetSetID {
		view = noCaptureApplicable(q, intent)
		view["target_set_id"] = set.TargetSetID
		view["captured_at"] = set.CapturedAt
	}
	units, e := app.AllRequiredUnits(tx, set.ID)
	if e != nil {
		return nil, e
	}
	assessed, partial, blockedUnits, discrepancy := 0, 0, 0, false
	var assessedAt any
	for _, r := range units {
		u, ok, e := tx.LatestAssessmentUnit(el.Version.ID, set.TargetSetID, r)
		if e != nil {
			return nil, e
		}
		if !ok {
			partial++
			continue
		}
		switch u.Outcome {
		case "assessed":
			assessed++
		case "blocked":
			blockedUnits++
		default:
			partial++
		}
		if assessedAt == nil || freshnessTimeCompare(u.AssessedAt, assessedAt.(string)) > 0 {
			assessedAt = u.AssessedAt
		}
		if u.Discrepancy != "" {
			discrepancy = true
		}
	}
	triggers, e := tx.TriggersByTargetSet(el.Version.ID, set.TargetSetID)
	if e != nil {
		return nil, e
	}
	triggered := false
	for _, t := range triggers {
		if triggerInScope(t, q) {
			triggered = true
		}
	}
	complete := len(units) > 0 && assessed == len(units) && partial == 0 && blockedUnits == 0
	status := "not_assessed"
	switch {
	case blocked:
		status = "unable_to_verify"
	case discrepancy:
		status = "outdated_against_target"
	case triggered:
		status = "reassessment_needed"
	case blockedUnits > 0:
		status = "unable_to_verify"
	case complete:
		status = "aligned_within_scope"
	}
	view["scope"] = q.wireScope()
	view["status"] = status
	view["reassessment_required"] = triggered
	view["assessed_at"] = assessedAt
	unitsURL := "/api/v1/versions/" + el.Version.ID + "/assessments/coverage?coverage_set_id=" + set.ID
	if previous, ok := view["coverage"].(map[string]any); ok && previous["coverage_set_id"] == set.ID {
		if observedURL, ok := previous["units_url"].(string); ok {
			unitsURL = observedURL
		}
	}
	view["coverage"] = map[string]any{"coverage_set_id": set.ID, "unit_kind": set.UnitKind, "eligible": len(units), "assessed": assessed, "complete": complete, "units_url": unitsURL}
	if unavailable != nil && freshnessTimeCompare(unavailable.CapturedAt, set.CapturedAt) >= 0 {
		view["status"] = "unable_to_verify"
	}
	return view, nil
}

// Genuine scan membership exists before a source/provider intent. Reading it
// does not invent a successful capture, observed input or semantic contribution.
func capturedFreshnessMembership(tx app.WriteTx, set domain.CoverageSet) (domain.ScanCapturedVersion, bool, error) {
	if !strings.HasPrefix(set.Origin, "freshness:") {
		return domain.ScanCapturedVersion{}, false, nil
	}
	job := strings.TrimPrefix(set.Origin, "freshness:")
	cv, err := tx.ScanCapturedVersionByID(job, set.VersionID)
	if errors.Is(err, domain.ErrNotFound) {
		return cv, false, nil
	}
	if err != nil {
		return cv, false, err
	}
	intents, err := tx.MaintenanceIntentsByScan(job)
	if err != nil {
		return cv, false, err
	}
	hasIntent, linked := false, false
	for _, m := range intents {
		if m.Mode == domain.MaintenanceModeFreshness && m.VersionID == set.VersionID {
			hasIntent = true
			linked = linked || m.FullCoverageSetID == set.ID || m.CoverageSetID == set.ID
		}
	}
	return cv, linked || !hasIntent && cv.TargetSetID == set.TargetSetID, nil
}

func freshnessTimeCompare(a, b string) int {
	x, xe := time.Parse(time.RFC3339Nano, a)
	y, ye := time.Parse(time.RFC3339Nano, b)
	if xe == nil && ye == nil {
		return x.Compare(y)
	}
	return strings.Compare(a, b)
}

func failedFreshnessAcquisition(tx app.WriteTx, set domain.CoverageSet) (domain.MaintenanceIntent, bool, error) {
	if !strings.HasPrefix(set.Origin, "freshness:") {
		return domain.MaintenanceIntent{}, false, nil
	}
	rows, err := tx.MaintenanceIntentsByScan(strings.TrimPrefix(set.Origin, "freshness:"))
	if err != nil {
		return domain.MaintenanceIntent{}, false, err
	}
	for _, m := range rows {
		if m.Mode != domain.MaintenanceModeFreshness || m.VersionID != set.VersionID || m.AdmissionReady || (m.CoverageSetID != set.ID && m.FullCoverageSetID != set.ID) {
			continue
		}
		job, err := tx.Job(m.JobID)
		if err != nil {
			return m, false, err
		}
		if job.State != "paused" || job.Error != "source_capture_unavailable" {
			continue
		}
		for _, l := range m.Limitations {
			if l.Kind == "source_unavailable" || l.Kind == "source_disclosure_restricted" {
				return m, true, nil
			}
		}
	}
	return domain.MaintenanceIntent{}, false, nil
}

func (s *Service) unavailableFreshness(tx app.WriteTx, view map[string]any, q AssessmentQuery, intent any, set domain.CoverageSet) (any, error) {
	if view != nil && view["target_set_id"] != nil {
		// Keep the earlier actual target, discrepancy, coverage and triggers.
		view["status"] = "unable_to_verify"
		return view, nil
	}
	units, err := app.AllRequiredUnits(tx, set.ID)
	if err != nil {
		return nil, err
	}
	view = noCaptureApplicable(q, intent)
	view["status"] = "unable_to_verify"
	view["coverage"] = map[string]any{"coverage_set_id": set.ID, "unit_kind": set.UnitKind, "eligible": len(units), "assessed": 0, "complete": false, "units_url": "/api/v1/versions/" + set.VersionID + "/assessments/coverage?coverage_set_id=" + set.ID}
	return view, nil
}

// Semantic captures carry the production capture request rather than an
// observation selector. Compare its actual authorized source family, including
// pinned revisions, before considering a default applicable contribution.
func semanticSelectionMatches(tx app.WriteTx, el appsource.Eligibility, ts domain.TargetSet, mode string) (bool, error) {
	var req appsource.CaptureRequest
	if json.Unmarshal(ts.RequestedSelection, &req) != nil || req.VersionID != el.Version.ID || req.SourceMode != mode || len(req.Selections) == 0 {
		return false, nil
	}
	effective, err := appsource.EffectiveSourceIDs(tx, el)
	if errors.Is(err, appsource.ErrMappingAmbiguous) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	sources, err := tx.VersionSources(el.Version.ID)
	if err != nil {
		return false, err
	}
	mappings, err := appsource.Mappings(tx, el.ProjectID)
	if err != nil {
		return false, err
	}
	snapshots := map[string]bool{}
	for _, v := range sources {
		var identity struct {
			Kind     string `json:"kind"`
			Snapshot string `json:"snapshot_sha256"`
		}
		if json.Unmarshal(v.Metadata, &identity) != nil {
			return false, domain.ErrContentIntegrity
		}
		if identity.Kind != "snapshot" {
			continue
		}
		sid := ""
		files := []string{}
		for _, m := range mappings {
			if m.Kind == "files" {
				files = append(files, m.SourceID)
				if m.SourceID == v.ID {
					sid = m.SourceID
				}
			}
		}
		if sid == "" && len(files) == 1 {
			sid = files[0]
		}
		if sid == "" {
			return false, nil
		}
		effective = append(effective, sid)
		snapshots[identity.Snapshot] = true
	}
	expected := map[string]bool{}
	for _, id := range effective {
		expected[id] = true
	}
	actual := map[string]bool{}
	for _, v := range req.Selections {
		actual[v.SourceID] = true
	}
	if len(expected) != len(actual) {
		return false, nil
	}
	for id := range expected {
		if !actual[id] {
			return false, nil
		}
	}
	if mode == "pinned" && len(snapshots) > 0 {
		capture, ok, e := tx.SourceCaptureByTargetSet(ts.ID)
		if e != nil {
			return false, e
		}
		if !ok || capture.State != "completed" {
			return false, nil
		}
		after := ""
		seen := false
		for {
			rows, e := tx.SourceCaptureReads(capture.ID, after, 256)
			if e != nil {
				return false, e
			}
			if len(rows) == 0 {
				break
			}
			for _, r := range rows {
				after = r.ID
				var identity struct {
					Kind     string `json:"kind"`
					Snapshot string `json:"snapshot_sha256"`
				}
				if json.Unmarshal(r.SourceJSON, &identity) != nil {
					return false, domain.ErrContentIntegrity
				}
				if identity.Kind == "snapshot" {
					if !snapshots[identity.Snapshot] {
						return false, nil
					}
					seen = true
				}
			}
		}
		if !seen {
			return false, nil
		}
	}
	if mode == "pinned" {
		// Reuse the canonical pinned family identity. Multi-source captures must
		// likewise match each version-declared exact identity, never just the mode.
		sources, e := tx.VersionSources(el.Version.ID)
		if e != nil {
			return false, e
		}
		mappings, e := appsource.Mappings(tx, el.ProjectID)
		if e != nil {
			return false, e
		}
		revisions := map[string]string{}
		for _, v := range sources {
			var ident struct {
				Kind     string `json:"kind"`
				RepoID   string `json:"repo_id"`
				Revision string `json:"exact_revision"`
			}
			if json.Unmarshal(v.Metadata, &ident) != nil {
				return false, nil
			}
			if ident.Kind == "snapshot" {
				continue
			}
			if ident.Kind != "git" {
				return false, nil
			}
			mapping, e := appsource.LinkGit(mappings, ident.RepoID)
			if e != nil {
				return false, nil
			}
			if old := revisions[mapping.SourceID]; old != "" && old != ident.Revision {
				return false, nil
			}
			revisions[mapping.SourceID] = ident.Revision
		}
		for _, v := range req.Selections {
			if len(snapshots) > 0 && revisions[v.SourceID] == "" {
				continue
			}
			if revisions[v.SourceID] == "" || v.Revision != revisions[v.SourceID] || v.WorkingCopy {
				return false, nil
			}
		}
	}
	return true, nil
}
