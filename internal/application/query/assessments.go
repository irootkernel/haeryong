package query

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"

	appsource "haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// AssessmentQuery carries the assessments read filters (plan §3): the
// optional explicit target capture and the requested scope.
type AssessmentQuery struct {
	Options
	TargetSetID string
	ScopeKind   string
	PagePath    string
	RegionID    string
}

// wireScope is the frozen applicable.scope shape.
func (q AssessmentQuery) wireScope() map[string]any {
	kind := q.ScopeKind
	if kind == "" {
		kind = "version"
	}
	out := map[string]any{"kind": kind, "page_path": nil, "region_id": nil}
	if kind != "version" {
		out["page_path"] = q.PagePath
	}
	if kind == "region" {
		out["region_id"] = q.RegionID
	}
	return out
}

func (q AssessmentQuery) kind() string {
	if q.ScopeKind == "" {
		return "version"
	}
	return q.ScopeKind
}

// selection is the decoded requested_selection of a run or capture.
type selection struct {
	ProjectID string   `json:"project_id"`
	Mode      string   `json:"mode"`
	SourceIDs []string `json:"source_ids"`
}

func decodeSelection(raw []byte) selection {
	var out selection
	_ = json.Unmarshal(raw, &out)
	sort.Strings(out.SourceIDs)
	return out
}

func sameSelection(a, b selection) bool {
	if a.ProjectID != b.ProjectID || a.Mode != b.Mode || len(a.SourceIDs) != len(b.SourceIDs) {
		return false
	}
	for i := range a.SourceIDs {
		if a.SourceIDs[i] != b.SourceIDs[i] {
			return false
		}
	}
	return true
}

// storedScope is a run's captured scope copy.
type storedScope struct {
	Kind     string `json:"kind"`
	PagePath string `json:"page_path"`
	RegionID string `json:"region_id"`
}

// scopeMatchesRequest reports whether a run's captured scope matches the
// requested filters exactly (plan §7.1 step 5).
func scopeMatchesRequest(scope storedScope, q AssessmentQuery) bool {
	if scope.Kind != q.kind() {
		return false
	}
	switch q.kind() {
	case "version":
		return true
	case "page":
		return scope.PagePath == q.PagePath
	default:
		return scope.PagePath == q.PagePath && scope.RegionID == q.RegionID
	}
}

// triggerInScope applies the §7.2 anchor visibility: a version summary counts
// every trigger of the target; a page summary counts its regions' triggers; a
// region summary counts the exact region's.
func triggerInScope(trigger domain.ReassessmentTrigger, q AssessmentQuery) bool {
	switch q.kind() {
	case "version":
		return true
	case "page":
		return trigger.AnchorKind == "region" && trigger.PagePath == q.PagePath
	default:
		return trigger.AnchorKind == "region" && trigger.PagePath == q.PagePath && trigger.RegionID == q.RegionID
	}
}

// Assessments composes GET /versions/{id}/assessments (plan §7.1 as amended
// by r4.4/r4.5): data.items stays empty in EPIC-004 (the assessments
// relation has no writer) and data.applicable is composed target-bound from
// the latest attempt A and the latest valid capture C of the effective
// selection, or from one explicit target capture. The page validates its
// cursor and limit through the shared bound cursor and always carries
// next_cursor, null in EPIC-004 (W3b L11).
func (s *Service) Assessments(ctx context.Context, token, versionID string, q AssessmentQuery) (map[string]any, error) {
	if q.ScopeKind != "" && q.ScopeKind != "version" && q.ScopeKind != "page" && q.ScopeKind != "region" {
		return nil, &Error{"invalid_scope_kind"}
	}
	if q.ScopeKind == "version" && (q.PagePath != "" || q.RegionID != "") {
		return nil, &Error{"invalid_scope"}
	}
	if q.ScopeKind == "page" && (q.PagePath == "" || q.RegionID != "") {
		return nil, &Error{"invalid_scope"}
	}
	if q.ScopeKind == "region" && (q.PagePath == "" || q.RegionID == "") {
		return nil, &Error{"invalid_scope"}
	}
	// Phase A resolves authority, the page binding and the version's manifest
	// identity (the author-declared source_intent lives in the normalized
	// manifest).
	var manifestHash string
	err := s.read(ctx, token, "/api/v1/versions/"+versionID+"/assessments", func(tx app.WriteTx) error {
		record, err := s.Auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return err
		}
		el, err := appsource.Check(tx, record, versionID)
		if err != nil {
			return err
		}
		if _, _, _, err := s.cursorStart(ctx, token, tx, "assessments:"+versionID+":"+q.TargetSetID+":"+q.kind()+":"+q.PagePath+":"+q.RegionID, q.Options, el.ProjectID); err != nil {
			return err
		}
		version, err := visibleVersion(tx, versionID)
		if err != nil {
			return err
		}
		manifestHash = version.ManifestSHA
		return nil
	})
	if err != nil {
		return nil, err
	}
	sourceIntent, err := s.manifestSourceIntent(ctx, token, versionID, manifestHash)
	if err != nil {
		return nil, err
	}
	var applicable any
	items := []map[string]any{}
	var next *string
	err = s.read(ctx, token, "/api/v1/versions/"+versionID+"/assessments", func(tx app.WriteTx) error {
		record, authErr := s.Auth.AuthenticateWrite(ctx, tx, token)
		if authErr != nil {
			return authErr
		}
		el, elErr := appsource.Check(tx, record, versionID)
		if elErr != nil {
			return elErr
		}
		applicable, err = s.composeApplicable(tx, el, q, sourceIntent)
		if err != nil {
			return err
		}
		c, n, _, e := s.cursorStart(ctx, token, tx, "assessments:"+versionID+":"+q.TargetSetID+":"+q.kind()+":"+q.PagePath+":"+q.RegionID, q.Options, el.ProjectID)
		if e != nil {
			return e
		}
		// Walk storage pages until the filtered public page is full. Filtering
		// must neither cap history nor advance the cursor beyond the last row
		// actually delivered to this consumer.
		scanTime, scanID := c.Key, c.After
		for {
			rows, e := tx.AssessmentsByVersion(versionID, scanTime, scanID, 64)
			if e != nil {
				return e
			}
			if len(rows) == 0 {
				break
			}
			for _, a := range rows {
				scanTime, scanID = a.AssessedAt, a.ID
				if (q.TargetSetID != "" && a.TargetSetID != q.TargetSetID) || !assessmentHistoryInScope(decodeStoredScope(a.ScopeJSON), q) {
					continue
				}
				if len(items) == n {
					next = cursorNext(c)
					return nil
				}
				var v map[string]any
				b, _ := json.Marshal(a)
				_ = json.Unmarshal(b, &v)
				v["created_at"] = a.AssessedAt
				items = append(items, v)
				c.Key, c.After = a.AssessedAt, a.ID
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": items, "next_cursor": next, "applicable": applicable}, nil
}

// manifestSourceIntent reads the version's declared source_intent from its
// normalized manifest by streaming the whole verified object, with no size
// limit (W3b L9). A version without a manifest object honestly carries null;
// a verified manifest that does not decode is an integrity error.
func (s *Service) manifestSourceIntent(ctx context.Context, token, versionID, hash string) (any, error) {
	if hash == "" {
		return nil, nil
	}
	var object domain.Object
	err := s.read(ctx, token, "/api/v1/versions/"+versionID+"/assessments", func(tx app.WriteTx) error {
		row, err := tx.Object(hash)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil
			}
			return err
		}
		object = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	if object.Hash == "" {
		return nil, nil
	}
	var intent any
	readErr := s.Bytes.Read(ctx, object, domain.Pin{Kind: "evidence_view", OwnerID: versionID + ":manifest"}, func(reader io.ReadSeeker) error {
		var manifest struct {
			Version struct {
				SourceIntent any `json:"source_intent"`
			} `json:"version"`
		}
		if err := json.NewDecoder(reader).Decode(&manifest); err != nil {
			return err
		}
		intent = manifest.Version.SourceIntent
		return nil
	})
	if readErr != nil {
		return nil, readErr
	}
	return intent, nil
}

// projectSourceDefault reads the project's remembered source_mode_default
// (configuration.md: pinned or current; default pinned). A storage or decode
// error propagates (W3b L8).
func projectSourceDefault(tx app.WriteTx, projectID string) (string, error) {
	configuration, err := tx.Configuration()
	if err != nil {
		return "", err
	}
	var value struct {
		Projects map[string]struct {
			SourceModeDefault string `json:"source_mode_default"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(configuration.Value, &value); err != nil {
		return "", err
	}
	if mode := value.Projects[projectID].SourceModeDefault; mode == "current" || mode == "pinned" {
		return mode, nil
	}
	return "pinned", nil
}

// pinnedKey canonicalizes a pinned-family requested_selection for the exact
// comparison of W3b L4(b): project, source ids, mode and the revision or
// snapshot selection.
func pinnedKey(raw []byte) string {
	var value struct {
		ProjectID   string   `json:"project_id"`
		Mode        string   `json:"mode"`
		SourceIDs   []string `json:"source_ids"`
		Revision    string   `json:"revision"`
		SnapshotSHA string   `json:"snapshot_sha256"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	sort.Strings(value.SourceIDs)
	if value.SourceIDs == nil {
		value.SourceIDs = []string{}
	}
	key, _ := json.Marshal(value)
	return string(key)
}

// noCaptureApplicable is the registry no-capture form (plan §7.1 step 4).
func noCaptureApplicable(q AssessmentQuery, sourceIntent any) map[string]any {
	return map[string]any{"source_intent": sourceIntent, "target_set_id": nil, "captured_at": nil, "scope": q.wireScope(), "status": "not_assessed", "assessed_at": nil, "reassessment_required": false, "coverage": emptyCoverage(q.kind())}
}

// composeApplicable is the §7.1 selection as amended (r4.4 rule 1/2, r4.5
// rule 1): an explicit target selects exactly its capture; the current
// family composes the latest attempt A with the latest valid capture C; the
// pinned family selects verification-created captures of exactly the
// version's effective pinned selection.
func (s *Service) composeObservedApplicable(tx app.WriteTx, el appsource.Eligibility, q AssessmentQuery, sourceIntent any) (any, error) {
	// Step 6a: the requested scope must project onto the version.
	switch q.kind() {
	case "page":
		pages, err := tx.VersionPages(el.Version.ID)
		if err != nil {
			return nil, err
		}
		found := false
		for _, page := range pages {
			if page.Path == q.PagePath {
				found = true
			}
		}
		if !found {
			return nil, nil
		}
	case "region":
		if _, err := tx.VersionRegion(el.Version.ID, q.PagePath, q.RegionID); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil, nil
			}
			return nil, err
		}
	}
	runs, err := tx.ObservationRunsByVersion(el.Version.ID)
	if err != nil {
		return nil, err
	}
	committed := []domain.ObservationRun{}
	for _, run := range runs {
		if run.Committed {
			committed = append(committed, run)
		}
	}
	evidenceRows, err := tx.VersionEvidence(el.Version.ID)
	if err != nil {
		return nil, err
	}
	if q.TargetSetID != "" {
		return s.composeExplicitTarget(tx, el, q, sourceIntent, committed)
	}
	captures, err := tx.VerificationCaptures(el.Version.ID, el.ProjectID)
	if err != nil {
		return nil, err
	}
	// Step 6c: no source-evidence rows and no observations/captures at all.
	if len(evidenceRows) == 0 && len(committed) == 0 && len(captures) == 0 {
		return nil, nil
	}
	// Effective mode (plan §7.1 step 2): historical stays pinned; a moving
	// target or absent intent follows the remembered project default.
	mode := "pinned"
	intent, _ := sourceIntent.(string)
	switch intent {
	case "historical":
	case "moving_target", "":
		if mode, err = projectSourceDefault(tx, el.ProjectID); err != nil {
			return nil, err
		}
	}
	if mode != "current" {
		// r4.4 rule 2: the pinned family selects verification-created
		// captures only — never an observation run or its coverage — of
		// exactly the version's effective pinned selection (W3b L4(b)).
		applicable := noCaptureApplicable(q, sourceIntent)
		want, ok, err := appsource.PinnedSelection(tx, el)
		if err != nil {
			return nil, err
		}
		if ok {
			wanted := pinnedKey(want)
			for i := len(captures) - 1; i >= 0; i-- {
				if pinnedKey(captures[i].RequestedSelection) == wanted {
					applicable["target_set_id"] = captures[i].ID
					applicable["captured_at"] = captures[i].CapturedAt
					break
				}
			}
		}
		return applicable, nil
	}
	effective, err := appsource.EffectiveSourceIDs(tx, el)
	if errors.Is(err, appsource.ErrMappingAmbiguous) {
		// W3b L4(c): while a cited repo_id is mapped by several sources the
		// default search never picks one of the duplicates.
		return noCaptureApplicable(q, sourceIntent), nil
	}
	if err != nil {
		return nil, err
	}
	want := selection{ProjectID: el.ProjectID, Mode: "current", SourceIDs: effective}
	matched := []domain.ObservationRun{}
	for _, run := range committed {
		if sameSelection(decodeSelection(run.RequestedSelection), want) {
			matched = append(matched, run)
		}
	}
	if len(matched) == 0 {
		// Step 4 no-capture rule: a non-null applicable with nulls.
		return noCaptureApplicable(q, sourceIntent), nil
	}
	attempt := matched[len(matched)-1]
	captureRun := domain.ObservationRun{}
	for i := len(matched) - 1; i >= 0; i-- {
		if matched[i].TargetSetID != "" {
			captureRun = matched[i]
			break
		}
	}
	hasCapture := captureRun.ID != ""
	// Status on the selection basis (r4.2 rule 4, binding note 3).
	blocked, err := runAvailabilityBlocked(tx, attempt.ID)
	if err != nil {
		return nil, err
	}
	triggers := []domain.ReassessmentTrigger{}
	if hasCapture {
		rows, err := tx.TriggersByTargetSet(el.Version.ID, captureRun.TargetSetID)
		if err != nil {
			return nil, err
		}
		for _, trigger := range rows {
			if triggerInScope(trigger, q) {
				triggers = append(triggers, trigger)
			}
		}
	}
	applicable := noCaptureApplicable(q, sourceIntent)
	applicable["status"] = selectionStatus(blocked, len(triggers) > 0)
	applicable["reassessment_required"] = len(triggers) > 0
	if hasCapture {
		capture, err := tx.TargetSetByID(captureRun.TargetSetID)
		if err != nil {
			return nil, err
		}
		applicable["target_set_id"] = capture.ID
		applicable["captured_at"] = capture.CapturedAt
	}
	// Coverage: the captured denominator comes from C's run; without a
	// capture it is the latest attempt's own attempted-scope set (r4.2
	// rule 2 read path).
	owner := captureRun
	if !hasCapture {
		owner = attempt
	}
	if set, err := tx.CoverageSetByRun(owner.ID); err == nil {
		if scopeMatchesRequest(decodeStoredScope(set.ScopeJSON), q) {
			eligible, err := countUnits(tx, set.ID)
			if err != nil {
				return nil, err
			}
			unitsURL := "/api/v1/versions/" + el.Version.ID + "/assessments/coverage?coverage_set_id=" + set.ID
			if owner.ID != attempt.ID {
				// Supervisor note 1: the composition's units_url names the
				// attempt when it differs from the capture run.
				unitsURL += "&attempt_run_id=" + attempt.ID
			}
			applicable["coverage"] = map[string]any{"coverage_set_id": set.ID, "unit_kind": set.UnitKind, "eligible": eligible, "assessed": 0, "complete": false, "units_url": unitsURL}
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	return applicable, nil
}

// selectionStatus is the one status rule of plan-phase note 3 for the default
// and the explicit composition (W3b L16): an availability-blocked run yields
// unable_to_verify; otherwise an in-scope uncleared trigger of the capture
// yields reassessment_needed; otherwise not_assessed.
func selectionStatus(blocked, triggered bool) string {
	switch {
	case blocked:
		return "unable_to_verify"
	case triggered:
		return "reassessment_needed"
	}
	return "not_assessed"
}

// composeExplicitTarget serves an explicit target_set_id (plan §7.1 step 1,
// r4.4 rule 2): exactly that committed capture and its run's scope — never a
// search or substitution in the other family; a foreign or never-committed
// id stays 404.
func (s *Service) composeExplicitTarget(tx app.WriteTx, el appsource.Eligibility, q AssessmentQuery, sourceIntent any, committed []domain.ObservationRun) (any, error) {
	capture, err := tx.VersionCapture(el.Version.ID, q.TargetSetID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, &Missing{Reason: "target_set_id", Details: map[string]any{"target_set_id": q.TargetSetID}}
		}
		return nil, err
	}
	run, hasRun := domain.ObservationRun{}, false
	for _, candidate := range committed {
		if candidate.TargetSetID == capture.ID {
			run, hasRun = candidate, true
			break
		}
	}
	scopeWire := q.wireScope()
	if hasRun {
		stored := decodeStoredScope(run.ScopeJSON)
		if !scopeMatchesRequest(stored, q) {
			// Step 1 mismatch form (step 6b): applicable null.
			return nil, nil
		}
		scopeWire = stored.wire()
	} else if q.kind() != "version" {
		// A verification-created capture carries no run scope; only a
		// version-scope request projects onto it.
		return nil, nil
	}
	applicable := map[string]any{"source_intent": sourceIntent, "target_set_id": capture.ID, "captured_at": capture.CapturedAt, "scope": scopeWire, "status": "not_assessed", "assessed_at": nil, "reassessment_required": false, "coverage": emptyCoverage(q.kind())}
	if hasRun {
		blocked, err := runAvailabilityBlocked(tx, run.ID)
		if err != nil {
			return nil, err
		}
		triggers := []domain.ReassessmentTrigger{}
		rows, err := tx.TriggersByTargetSet(el.Version.ID, capture.ID)
		if err != nil {
			return nil, err
		}
		for _, trigger := range rows {
			if triggerInScope(trigger, q) {
				triggers = append(triggers, trigger)
			}
		}
		applicable["status"] = selectionStatus(blocked, len(triggers) > 0)
		applicable["reassessment_required"] = len(triggers) > 0
		if set, err := tx.CoverageSetByRun(run.ID); err == nil {
			eligible, err := countUnits(tx, set.ID)
			if err != nil {
				return nil, err
			}
			applicable["coverage"] = map[string]any{"coverage_set_id": set.ID, "unit_kind": set.UnitKind, "eligible": eligible, "assessed": 0, "complete": false, "units_url": "/api/v1/versions/" + el.Version.ID + "/assessments/coverage?coverage_set_id=" + set.ID}
		} else if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
	}
	return applicable, nil
}

func decodeStoredScope(raw []byte) storedScope {
	var out storedScope
	_ = json.Unmarshal(raw, &out)
	if out.Kind == "" {
		out.Kind = "version"
	}
	return out
}

func (s storedScope) wire() map[string]any {
	out := map[string]any{"kind": s.Kind, "page_path": nil, "region_id": nil}
	if s.Kind != "version" {
		out["page_path"] = s.PagePath
	}
	if s.Kind == "region" {
		out["region_id"] = s.RegionID
	}
	return out
}

func emptyCoverage(kind string) map[string]any {
	return map[string]any{"coverage_set_id": nil, "unit_kind": kind, "eligible": 0, "assessed": 0, "complete": false, "units_url": nil}
}

// runAvailabilityBlocked reports whether any requested source of the run was
// unavailable or denied, including a partial run that captured a target
// (binding note 3).
func runAvailabilityBlocked(tx app.WriteTx, runID string) (bool, error) {
	rows, err := tx.ObservationsByRun(runID)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.Availability != "available" {
			return true, nil
		}
	}
	return false, nil
}

func countUnits(tx app.WriteTx, coverageSetID string) (int, error) {
	total := 0
	afterKind, afterPage, afterRegion := "", "", ""
	for {
		rows, err := tx.RequiredUnits(coverageSetID, afterKind, afterPage, afterRegion, 500)
		if err != nil {
			return 0, err
		}
		total += len(rows)
		if len(rows) == 0 {
			return total, nil
		}
		last := rows[len(rows)-1]
		afterKind, afterPage, afterRegion = last.Kind, last.PagePath, last.RegionID
	}
}

// AssessmentCoverage serves GET /versions/{id}/assessments/coverage: exactly
// the named coverage set's stored units (denominator and reasons as
// persisted); an attempt_run_id overlays that committed attempt's
// availability-level reasons onto the units whose sources failed in it
// (Supervisor note 1), attributed through the links the runs recorded, never
// through the current mappings (W3b L7).
func (s *Service) AssessmentCoverage(ctx context.Context, token, versionID, coverageSetID, attemptRunID string, o Options) (List[map[string]any], error) {
	out := List[map[string]any]{Items: []map[string]any{}}
	err := s.read(ctx, token, "/api/v1/versions/"+versionID+"/assessments/coverage", func(tx app.WriteTx) error {
		record, err := s.Auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return err
		}
		el, err := appsource.Check(tx, record, versionID)
		if err != nil {
			return err
		}
		c, limit, _, err := s.cursorStart(ctx, token, tx, "assessment-coverage:"+versionID+":"+coverageSetID+":"+attemptRunID, o, el.ProjectID)
		if err != nil {
			return err
		}
		afterKind, afterPage, afterRegion := c.Key, "", ""
		if c.After != "" {
			parts := strings.SplitN(c.After, "\x00", 2)
			if len(parts) == 2 {
				afterPage, afterRegion = parts[0], parts[1]
			}
		}
		missingSet := &Missing{Reason: "coverage_set_id", Details: map[string]any{"coverage_set_id": coverageSetID}}
		set, err := tx.CoverageSetByID(coverageSetID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return missingSet
			}
			return err
		}
		if set.VersionID != versionID {
			return missingSet
		}
		overlay := map[string]string{}
		if attemptRunID != "" {
			missingAttempt := &Missing{Reason: "attempt_run_id", Details: map[string]any{"attempt_run_id": attemptRunID}}
			attempt, err := tx.ObservationRunByID(attemptRunID)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return missingAttempt
				}
				return err
			}
			if !attempt.Committed || attempt.VersionID != versionID || set.RunID == "" {
				return missingAttempt
			}
			owner, err := tx.ObservationRunByID(set.RunID)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return missingAttempt
				}
				return err
			}
			if !sameSelection(decodeSelection(attempt.RequestedSelection), decodeSelection(owner.RequestedSelection)) {
				return missingAttempt
			}
			rows, err := tx.ObservationsByRun(attempt.ID)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if row.Availability != "available" && row.Reason != "" && row.RepoID != "" {
					overlay[row.RepoID] = strongestAvailabilityReason(overlay[row.RepoID], row.Reason)
				}
			}
		}
		attribution := unitRepos{}
		if len(overlay) > 0 {
			if attribution, err = versionUnitRepos(tx, el.Version.ID); err != nil {
				return err
			}
		}
		rows, err := tx.RequiredUnits(set.ID, afterKind, afterPage, afterRegion, limit+1)
		if err != nil {
			return err
		}
		failedAcquisition, acquisitionBlocked, err := failedFreshnessAcquisition(tx, set)
		if err != nil {
			return err
		}
		capturedMembership := strings.HasPrefix(set.Origin, "freshness:")
		uncaptured := false
		if capturedMembership {
			if _, e := tx.VersionCapture(versionID, set.TargetSetID); errors.Is(e, domain.ErrNotFound) {
				_, uncaptured, err = capturedFreshnessMembership(tx, set)
				if err != nil {
					return err
				}
			} else if e != nil {
				return e
			}
		}
		for i, unit := range rows {
			if i == limit {
				last := rows[i-1]
				next := cursor{Binding: c.Binding, Key: last.Kind, After: last.PagePath + "\x00" + last.RegionID}
				out.NextCursor = cursorNext(next)
				break
			}
			reason := unit.Reason
			if len(overlay) > 0 {
				// Note 1: a unit whose attributed source failed in A shows
				// the strongest of A's failed-source reasons for it.
				failed := ""
				for _, repo := range attribution.of(unit) {
					if next, ok := overlay[repo]; ok {
						failed = strongestAvailabilityReason(failed, next)
					}
				}
				if failed != "" {
					reason = failed
				}
			}
			if strings.HasPrefix(set.Origin, "freshness:") {
				if job, e := tx.Job(strings.TrimPrefix(set.Origin, "freshness:")); e == nil {
					if job.CancelRequested || job.State == "cancelled" {
						reason = "scan_cancelled_before_unit_assessment"
					} else if job.State == "failed" {
						reason = "scan_failed_before_unit_assessment"
					}
				} else if !errors.Is(e, domain.ErrNotFound) {
					return e
				}
			}
			view := unitView(unit, reason)
			if uncaptured {
				view["target_set_id"] = nil
			}
			if acquisitionBlocked {
				view["target_set_id"] = nil
				view["reason"] = "source_capture_unavailable_before_unit_assessment"
				if unit.Kind == failedAcquisition.UnitKind && unit.PagePath == failedAcquisition.PagePath && unit.RegionID == failedAcquisition.RegionID {
					for _, l := range failedAcquisition.Limitations {
						if l.Kind == "source_unavailable" || l.Kind == "source_disclosure_restricted" {
							view["reason"] = l.Reason
							break
						}
					}
				}
			}
			if contribution, ok, e := tx.LatestAssessmentUnit(versionID, set.TargetSetID, unit); e != nil {
				return e
			} else if ok {
				view["outcome"] = contribution.Outcome
				view["reason"] = nullable(contribution.Reason)
				view["discrepancy"] = nullable(contribution.Discrepancy)
				view["assessment_id"] = contribution.AssessmentID
				view["coverage_set_id"] = contribution.CoverageSetID
				if capturedMembership && unit.Reason != "" && unit.Reason != "not_yet_assessed" {
					view["reason"] = unit.Reason
				}
				if len(overlay) > 0 && reason != unit.Reason {
					view["reason"] = nullable(reason)
				}
			}
			out.Items = append(out.Items, view)
		}
		return nil
	})
	return out, err
}

// strongestAvailabilityReason picks the strongest availability-level reason
// by the §7.6 precedence.
func strongestAvailabilityReason(a, b string) string {
	rank := map[string]int{"source_not_registered": 5, "missing_source": 4, "missing_retained_bytes": 3, "retained_bytes_corrupt": 2, "source_denied": 1}
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	if rank[a] >= rank[b] {
		return a
	}
	return b
}

// unitView maps the ” sentinel to null on the wire.
func unitView(unit domain.RequiredUnit, reason string) map[string]any {
	var page, region any
	if unit.PagePath != "" {
		page = unit.PagePath
	}
	if unit.RegionID != "" {
		region = unit.RegionID
	}
	var target any
	if unit.TargetSetID != "" {
		target = unit.TargetSetID
	}
	var reasonValue any
	if reason != "" {
		reasonValue = reason
	}
	return map[string]any{"kind": unit.Kind, "page_path": page, "region_id": region, "outcome": "not_checked", "reason": reasonValue, "discrepancy": nil, "assessment_id": nil, "target_set_id": target}
}

// unitRepos attributes coverage units to the repo_ids of their own cited git
// evidence, from the version's immutable authored records only: a page unit
// to the evidence anchored on that page, a region unit to the evidence of
// that exact region, the whole-version unit to every cited git source.
type unitRepos struct {
	all     []string
	pages   map[string][]string
	regions map[string][]string
}

func (u unitRepos) of(unit domain.RequiredUnit) []string {
	switch unit.Kind {
	case "version":
		return u.all
	case "page":
		return u.pages[unit.PagePath]
	}
	return u.regions[unit.PagePath+"\x00"+unit.RegionID]
}

func versionUnitRepos(tx app.WriteTx, versionID string) (unitRepos, error) {
	out := unitRepos{pages: map[string][]string{}, regions: map[string][]string{}}
	sources, err := tx.VersionSources(versionID)
	if err != nil {
		return out, err
	}
	repoOf := map[string]string{}
	for _, row := range sources {
		var identity struct {
			Kind   string `json:"kind"`
			RepoID string `json:"repo_id"`
		}
		if json.Unmarshal(row.Metadata, &identity) == nil && identity.Kind == "git" && identity.RepoID != "" {
			repoOf[row.ID] = identity.RepoID
		}
	}
	evidence, err := tx.VersionEvidence(versionID)
	if err != nil {
		return out, err
	}
	evidenceRepo := map[string]string{}
	all := map[string]bool{}
	for _, row := range evidence {
		if repo, ok := repoOf[row.SourceID]; ok {
			evidenceRepo[row.ID] = repo
			if !all[repo] {
				all[repo] = true
				out.all = append(out.all, repo)
			}
		}
	}
	anchors, err := tx.VersionRegionEvidence(versionID)
	if err != nil {
		return out, err
	}
	for _, anchor := range anchors {
		repo, ok := evidenceRepo[anchor.EvidenceID]
		if !ok {
			continue
		}
		out.pages[anchor.PagePath] = append(out.pages[anchor.PagePath], repo)
		key := anchor.PagePath + "\x00" + anchor.RegionID
		out.regions[key] = append(out.regions[key], repo)
	}
	return out, nil
}

func assessmentHistoryInScope(scope storedScope, q AssessmentQuery) bool {
	if q.kind() == "version" {
		return true
	}
	return scopeMatchesRequest(scope, q)
}
