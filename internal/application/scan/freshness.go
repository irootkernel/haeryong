package scan

import (
	"context"
	"encoding/json"
	"haeryong.local/haeryong/internal/application/questions"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"sort"
	"strings"
	"time"
)

func freshnessScope(req domain.ScanRequestedScope) map[string]any {
	kind := "version"
	var page, region any
	if req.PagePath != "" {
		kind = "page"
		page = req.PagePath
	}
	if req.RegionID != "" {
		kind = "region"
		region = req.RegionID
	}
	return map[string]any{"kind": kind, "page_path": page, "region_id": region}
}

func freshnessItemID(version string, unit domain.RequiredUnit) string {
	tuple, _ := json.Marshal([]string{unit.Kind, unit.PagePath, unit.RegionID})
	return "freshness:" + version + ":" + string(tuple)
}

// Capture every version's fixed membership at admission, independently of any
// provider/source child. A metadata target is not a successful source capture.
func freshnessMembership(tx app.WriteTx, run domain.ScanRun, cv domain.ScanCapturedVersion) (domain.CoverageSet, []domain.RequiredUnit, error) {
	scope, _ := json.Marshal(freshnessScope(run.RequestedScope))
	sets, err := tx.CoverageSetsByVersion(cv.VersionID, "")
	if err != nil {
		return domain.CoverageSet{}, nil, err
	}
	for _, set := range sets {
		if set.Origin == "freshness:"+run.JobID && freshJSONEqual(set.ScopeJSON, scope) && set.TargetSetID == cv.TargetSetID {
			units, err := app.AllRequiredUnits(tx, set.ID)
			return set, units, err
		}
	}
	units, err := freshnessRequiredUnits(tx, cv, run.RequestedScope)
	if err != nil {
		return domain.CoverageSet{}, nil, err
	}
	id, err := service.NewID()
	if err != nil {
		return domain.CoverageSet{}, nil, err
	}
	at := cv.CapturedAt
	if at == "" {
		at = run.CapturedAt
	}
	if cv.TargetSetID != "" {
		target, err := tx.TargetSetByID(cv.TargetSetID)
		if err != nil {
			return domain.CoverageSet{}, nil, err
		}
		at = target.CapturedAt
	}
	set := domain.CoverageSet{ID: id, VersionID: cv.VersionID, TargetSetID: cv.TargetSetID, ScopeJSON: scope, UnitKind: units[0].Kind, CapturedAt: at, Origin: "freshness:" + run.JobID}
	if err = tx.PutCoverageSet(set); err != nil {
		return domain.CoverageSet{}, nil, err
	}
	for i := range units {
		units[i].CoverageSetID, units[i].TargetSetID, units[i].Reason = set.ID, set.TargetSetID, "not_yet_assessed"
		if err = tx.PutRequiredUnit(units[i]); err != nil {
			return domain.CoverageSet{}, nil, err
		}
	}
	return set, units, nil
}

func markFreshnessUnattempted(tx app.WriteTx, run domain.ScanRun, version, reason string) error {
	intents, err := tx.MaintenanceIntentsByScan(run.JobID)
	if err != nil {
		return err
	}
	completed := map[string]bool{}
	for _, m := range intents {
		if m.Mode != domain.MaintenanceModeFreshness {
			continue
		}
		child, err := tx.Job(m.JobID)
		if err != nil {
			return err
		}
		if child.State == "completed" {
			completed[m.ItemID] = true
		}
	}
	after := 0
	for {
		var versions []domain.ScanCapturedVersion
		if version != "" {
			cv, e := tx.ScanCapturedVersionByID(run.JobID, version)
			err = e
			versions = []domain.ScanCapturedVersion{cv}
		} else {
			versions, err = tx.ScanCapturedVersions(run.JobID, after, 64)
		}
		if err != nil {
			return err
		}
		if len(versions) == 0 {
			return nil
		}
		for _, cv := range versions {
			after = cv.VersionOrdinal
			if cv.VersionID == "" || version != "" && cv.VersionID != version {
				continue
			}
			sets, err := tx.CoverageSetsByVersion(cv.VersionID, "")
			if err != nil {
				return err
			}
			for _, set := range sets {
				if set.Origin != "freshness:"+run.JobID {
					continue
				}
				units, err := app.AllRequiredUnits(tx, set.ID)
				if err != nil {
					return err
				}
				for _, unit := range units {
					if !completed[freshnessItemID(cv.VersionID, unit)] {
						if err = tx.SetFreshnessUnitReason(set.ID, unit, reason); err != nil {
							return err
						}
					}
				}
			}
		}
		if version != "" {
			return nil
		}
	}
}

// stepFreshness fixes the complete requested denominator before its first
// provider admission, then commits one exact required unit at a time. Every
// child shares one selected source capture; continuation never repeats Execute.
func (w *Worker) stepFreshness(tx app.WriteTx, j domain.Job, run *domain.ScanRun) (bool, error) {
	cfg, e := tx.Configuration()
	if e != nil {
		return false, e
	}
	current, e := ResolveEffectivePolicy(cfg, run.RequestedScope.ProjectIDs, run.EffectivePolicy.Depth)
	if e != nil {
		return false, e
	}
	if StricterAutoApplyMode(run.EffectivePolicy.AutoApply.FreshnessAssessment, current.AutoApply.FreshnessAssessment) == "disabled" {
		return false, markFreshnessUnattempted(tx, *run, "", "freshness_assessment_disabled")
	}
	allIntents, e := tx.MaintenanceIntentsByScan(j.ID)
	if e != nil {
		return false, e
	}
	after := 0
	for {
		versions, e := tx.ScanCapturedVersions(j.ID, after, 64)
		if e != nil {
			return false, e
		}
		if len(versions) == 0 {
			break
		}
		for _, cv := range versions {
			after = cv.VersionOrdinal
			if cv.VersionID == "" {
				continue
			}
			// Purge deliberately removes private coverage/targets and leaves a
			// redacted captured-roster stub. Never recreate that deleted evidence
			// or an invented successful target while settling the old scan.
			if cv.ExclusionReason != "" || cv.EnumState == "purged" || cv.TargetSetID == "" {
				reason := cv.ExclusionReason
				if reason == "" {
					reason = "source_target_unavailable"
				}
				if e = markFreshnessUnattempted(tx, *run, cv.VersionID, reason); e != nil {
					return false, e
				}
				continue
			}
			var first *domain.MaintenanceIntent
			byItem := map[string]domain.MaintenanceIntent{}
			for _, m := range allIntents {
				if m.Mode == domain.MaintenanceModeFreshness && m.VersionID == cv.VersionID {
					byItem[m.ItemID] = m
					if first == nil || m.FullCoverageSetID != "" && first.FullCoverageSetID == "" {
						copy := m
						first = &copy
					}
				}
			}
			var full domain.CoverageSet
			var units []domain.RequiredUnit
			if first != nil {
				fullID := first.FullCoverageSetID
				if fullID == "" {
					fullID = first.CoverageSetID
				}
				full, e = tx.CoverageSetByID(fullID)
				if e != nil {
					return false, e
				}
				units, e = app.AllRequiredUnits(tx, full.ID)
				if e != nil {
					return false, e
				}
			} else {
				full, units, e = freshnessMembership(tx, *run, cv)
				if e != nil {
					return false, e
				}
			}
			reason := cv.ExclusionReason
			if reason == "" && cv.TargetSetID == "" {
				reason = "source_target_unavailable"
			}
			if reason == "" && !projectHasProviderID(cfg.Value, cv.ProjectID, "codex") {
				reason = "provider_unavailable"
			}
			if reason != "" {
				if e = markFreshnessUnattempted(tx, *run, cv.VersionID, reason); e != nil {
					return false, e
				}
				continue
			}
			for _, unit := range units {
				itemID := freshnessItemID(cv.VersionID, unit)
				if m, ok := byItem[itemID]; ok {
					child, e := tx.Job(m.JobID)
					if e != nil {
						return false, e
					}
					switch child.State {
					case "completed", "failed", "cancelled":
						continue
					case "paused":
						if child.Error == "source_capture_pending" {
							return true, nil
						}
						if child.ProviderAttempts != nil && (child.ProviderAttempts.Unknown > 0 || child.ProviderAttempts.LatestState == "result_saved") {
							return true, tx.PauseProviderJob(j.ID, child.Error)
						}
						continue
					default:
						return true, nil
					}
				}
				// A failed shared capture cannot be silently replaced with another target
				// for the remaining units. Their fixed rows remain unattempted.
				if first != nil && !first.AdmissionReady {
					if e = markFreshnessUnattempted(tx, *run, cv.VersionID, "source_capture_unavailable_before_unit_assessment"); e != nil {
						return false, e
					}
					break
				}
				if w.Identity == nil || w.Sources == nil {
					if e = markFreshnessUnattempted(tx, *run, cv.VersionID, "provider_unavailable"); e != nil {
						return false, e
					}
					break
				}
				count, e := tx.ScanProviderAttemptCount(j.ID, "")
				if e != nil {
					return false, e
				}
				limit := StricterPositiveInt(run.EffectivePolicy.MaxProviderAttemptsPerRun, current.MaxProviderAttemptsPerRun)
				if limit > 0 && count >= limit {
					run.Report.BudgetExhausted = true
					return false, markFreshnessUnattempted(tx, *run, "", "provider_attempt_budget_exhausted")
				}
				if _, _, _, ok := w.checkCandidateTargetFence(tx, *run, cv.VersionID, "codex"); !ok {
					reason := "target_fence_refused"
					if r, _, _, fenced := w.checkItemFence(tx, j, *run, domain.JobItem{ProjectID: cv.ProjectID, DocumentID: cv.DocumentID, VersionID: cv.VersionID}); fenced && r != "" {
						reason = r
					}
					if e = markFreshnessUnattempted(tx, *run, cv.VersionID, reason); e != nil {
						return false, e
					}
					break
				}
				id, e := service.NewID()
				if e != nil {
					return false, e
				}
				// A child records its own actual scope; the full fixed set is a projection
				// over those exact-target contributions, never an invented aggregate row.
				childSet := full
				childScope, _ := json.Marshal(map[string]any{"kind": unit.Kind, "page_path": freshNullable(unit.PagePath), "region_id": freshNullable(unit.RegionID)})
				if !freshJSONEqual(full.ScopeJSON, childScope) || len(units) > 1 {
					childSet.ID, e = service.NewID()
					if e != nil {
						return false, e
					}
					childSet.ScopeJSON = childScope
					childSet.UnitKind = unit.Kind
					if e = tx.PutCoverageSet(childSet); e != nil {
						return false, e
					}
					u := unit
					u.CoverageSetID = childSet.ID
					u.Reason = "not_yet_assessed"
					u.TargetSetID = childSet.TargetSetID
					if e = tx.PutRequiredUnit(u); e != nil {
						return false, e
					}
				}
				exps, e := w.freshnessUnitExplanations(tx, cv, unit)
				if e != nil {
					return false, e
				}
				inv := domain.ProviderInvocation{Prompt: string(childScope), Maintenance: &domain.MaintenanceInvocation{Mode: domain.MaintenanceModeFreshness, CoverageSetID: childSet.ID, ScanJobID: j.ID, ItemID: itemID, UnitKind: unit.Kind, ProjectID: cv.ProjectID, DocumentID: cv.DocumentID, VersionID: cv.VersionID, PagePath: freshOptionalPtr(unit.PagePath), RegionID: freshOptionalPtr(unit.RegionID), SourceMode: cv.SelectedMode, Explanations: exps}}
				target, _ := json.Marshal(map[string]any{"action": "maintenance", "project_id": cv.ProjectID, "document_id": cv.DocumentID, "version_id": cv.VersionID, "scan_job_id": j.ID, "item_id": itemID})
				m := domain.MaintenanceIntent{JobID: id, ScanJobID: j.ID, ItemID: itemID, Mode: domain.MaintenanceModeFreshness, CoverageSetID: childSet.ID, FullCoverageSetID: full.ID, UnitKind: unit.Kind, ProjectID: cv.ProjectID, DocumentID: cv.DocumentID, VersionID: cv.VersionID, PagePath: unit.PagePath, RegionID: unit.RegionID, SourceMode: cv.SelectedMode}
				var reads []domain.CapturedRead
				if first != nil {
					m.AdmissionReady = true
					m.SourceReadIDs = append([]string(nil), first.SourceReadIDs...)
					m.ReadBindings = append([]domain.MaintenanceReadBinding(nil), first.ReadBindings...)
					m.Limitations = first.Limitations
					inv.Maintenance.ReadBindings = m.ReadBindings
					for _, rid := range m.SourceReadIDs {
						r, e := tx.CapturedRead(rid)
						if e != nil {
							return false, e
						}
						reads = append(reads, r)
					}
					for i := range inv.Maintenance.Explanations {
						inv.Maintenance.Explanations[i].TargetSetID = full.TargetSetID
					}
				} else {
					selections, existingIDs, bindings, limitations, e := w.freshnessCaptureSelection(tx, *run, cv)
					if e != nil {
						return false, e
					}
					m.Limitations = limitations
					if len(selections) == 0 && len(existingIDs) > 0 {
						m.AdmissionReady = true
						m.SourceReadIDs = existingIDs
						m.ReadBindings = bindings
						inv.Maintenance.ReadBindings = bindings
						for _, rid := range existingIDs {
							r, e := tx.CapturedRead(rid)
							if e != nil {
								return false, e
							}
							reads = append(reads, r)
						}
					}
					body, _ := json.Marshal(source.CaptureRequest{VersionID: cv.VersionID, SourceMode: cv.SelectedMode, ProviderID: "codex", Selections: selections})
					if len(selections) > 0 {
						m.Captures = []domain.MaintenanceCaptureStep{{ProjectID: cv.ProjectID, VersionID: cv.VersionID, VersionIDs: []string{cv.VersionID}, OperationID: id, SourceMode: cv.SelectedMode, SourceSelection: body}}
					}
				}
				sha, e := w.Identity(run.PrincipalID, target, inv, reads)
				if e != nil {
					return false, e
				}
				auth, e := tx.AuthState()
				if e != nil {
					return false, e
				}
				cred := run.CapturedScope
				if run.AuthKind == "owner" {
					cred = domain.CredentialScope{Kind: "owner"}
				}
				scope := domain.ProviderScope{Purpose: "body", PrincipalID: run.PrincipalID, AuthEpoch: auth.AuthEpoch, CredentialScope: cred, ProviderID: "codex", RequestSHA: sha, Target: target, Invocation: &inv, ReadIDs: m.SourceReadIDs}
				if m.AdmissionReady {
					scope.TargetSetID = full.TargetSetID
				}
				body, _ := json.Marshal(scope)
				if e = tx.CreateMaintenanceJob(domain.Job{ID: id, Kind: "maintenance", ParentID: j.ID, State: "queued", Scope: body, ConfigRevision: run.ConfigRevision, Permissions: map[string]int64{cv.ProjectID: run.PermissionsRevisions[cv.ProjectID]}}, m); e != nil {
					return false, e
				}
				if m.AdmissionReady {
					if e = tx.AttachSourceInputs(id, m.SourceReadIDs); e != nil {
						return false, e
					}
					for _, read := range reads {
						if e = tx.AcquirePin(domain.Pin{ID: "job-source:" + j.ID + ":" + read.ID, Kind: "job", OwnerID: j.ID, Hash: read.BytesSHA}); e != nil {
							return false, e
						}
					}
				} else if len(m.Captures) == 0 {
					if e = tx.PauseMaintenanceCapture(id, "source_capture_unavailable", m.Limitations); e != nil {
						return false, e
					}
				}
				return true, nil
			}
		}
	}
	return false, nil
}

func freshNullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func freshnessRequiredUnits(tx app.WriteTx, cv domain.ScanCapturedVersion, req domain.ScanRequestedScope) ([]domain.RequiredUnit, error) {
	if req.RegionID != "" {
		return []domain.RequiredUnit{{Kind: "region", PagePath: req.PagePath, RegionID: req.RegionID}}, nil
	}
	if req.PagePath != "" {
		return []domain.RequiredUnit{{Kind: "page", PagePath: req.PagePath}}, nil
	}
	pages, e := tx.VersionPages(cv.VersionID)
	if e != nil {
		return nil, e
	}
	units := []domain.RequiredUnit{}
	for _, p := range pages {
		units = append(units, domain.RequiredUnit{Kind: "page", PagePath: p.Path})
	}
	if len(units) == 0 {
		units = append(units, domain.RequiredUnit{Kind: "version"})
	}
	return units, nil
}

func (w *Worker) freshnessUnitExplanations(tx app.WriteTx, cv domain.ScanCapturedVersion, u domain.RequiredUnit) ([]domain.MaintenanceExplanation, error) {
	if u.Kind != "version" {
		exp, ok := w.resolveUnitExplanation(tx, cv, cv.DocumentID, u.PagePath, u.RegionID, "focal", "")
		if !ok {
			return nil, domain.ErrContentIntegrity
		}
		return []domain.MaintenanceExplanation{exp}, nil
	}
	exps := []domain.MaintenanceExplanation{}
	after := ""
	for {
		entries, e := tx.Entries(cv.ContentSetID, after, 64)
		if e != nil {
			return nil, e
		}
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			after = entry.Path
			if entry.Kind == "file" {
				exp, ok := w.resolveUnitExplanation(tx, cv, cv.DocumentID, entry.Path, "", "focal", "")
				if !ok {
					return nil, domain.ErrContentIntegrity
				}
				exps = append(exps, exp)
			}
		}
	}
	if len(exps) == 0 {
		return nil, domain.ErrContentIntegrity
	}
	return exps, nil
}

func (w *Worker) freshnessCaptureSelection(tx app.WriteTx, run domain.ScanRun, cv domain.ScanCapturedVersion) ([]source.CaptureSelection, []string, []domain.MaintenanceReadBinding, []domain.MaintenanceLimitation, error) {
	// Freshness always captures complete cited files, not old span reads.
	selectCV := cv
	selectCV.TargetSetID = ""
	// Managed captures contain complete path reads. Span-only verification
	// captures are never reused as full files.
	if capture, ok, e := tx.SourceCaptureByTargetSet(cv.TargetSetID); e != nil {
		return nil, nil, nil, nil, e
	} else if ok && capture.State == "completed" {
		selectCV.TargetSetID = cv.TargetSetID
	}
	aliases, e := freshnessSourceAliases(tx, cv.ProjectID, cv.VersionID)
	if e != nil {
		return nil, nil, nil, nil, e
	}
	selectCV.Targets = append([]domain.ScanSourceTarget(nil), cv.Targets...)
	for i := range selectCV.Targets {
		if mapped := aliases[selectCV.Targets[i].SourceID]; mapped != "" {
			selectCV.Targets[i].SourceID = mapped
		}
	}
	selections, existingIDs, bindings, limitations := w.collectUnitSourceSelectionsWithAliases(tx, run, selectCV, run.RequestedScope.PagePath, run.RequestedScope.RegionID, aliases, true)
	// Observation and full-file capture have separate target IDs.
	// Carry only dependency_scope from a committed observation of this
	// version and these exact resolved source revisions. No trigger is
	// transferred to the new capture.
	observations, err := tx.ObservationRunsByVersion(cv.VersionID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	seenDependencySource := map[string]bool{}
	for ri := len(observations) - 1; ri >= 0; ri-- {
		if !observations[ri].Committed {
			continue
		}
		rows, e := tx.ObservationsByRun(observations[ri].ID)
		if e != nil {
			return nil, nil, nil, nil, e
		}
		for _, o := range rows {
			if seenDependencySource[o.SourceID] || o.Availability != "available" {
				continue
			}
			for _, t := range selectCV.Targets {
				if t.SourceID != o.SourceID || t.ResolvedRevision == "" || t.ResolvedRevision != o.ObservedRevision {
					continue
				}
				seenDependencySource[o.SourceID] = true
				var paths []string
				if len(o.DependencyScopeJSON) > 0 && json.Unmarshal(o.DependencyScopeJSON, &paths) != nil {
					return nil, nil, nil, nil, domain.ErrContentIntegrity
				}
				for _, p := range paths {
					present := false
					for _, rid := range existingIDs {
						read, e := tx.SourceCaptureRead(rid)
						if e != nil {
							return nil, nil, nil, nil, e
						}
						var loc struct {
							Path string `json:"path"`
						}
						if json.Unmarshal(read.LocationJSON, &loc) != nil {
							return nil, nil, nil, nil, domain.ErrContentIntegrity
						}
						if read.SourceID == o.SourceID && loc.Path == p {
							present = true
							break
						}
					}
					if present {
						continue
					}
					idx := -1
					for i, selection := range selections {
						if selection.SourceID == o.SourceID {
							idx = i
							break
						}
					}
					if idx < 0 {
						selections = append(selections, source.CaptureSelection{SourceID: o.SourceID, Revision: t.ResolvedRevision})
						idx = len(selections) - 1
					}
					selections[idx].Paths = append(selections[idx].Paths, source.CapturePath{Path: p})
				}
			}
		}
	}
	if len(selections) > 0 && len(existingIDs) > 0 {
		for _, id := range existingIDs {
			read, e := tx.SourceCaptureRead(id)
			if e != nil {
				return nil, nil, nil, nil, e
			}
			var src struct {
				Revision string `json:"exact_revision"`
			}
			var loc struct {
				Path string `json:"path"`
			}
			if json.Unmarshal(read.SourceJSON, &src) != nil || json.Unmarshal(read.LocationJSON, &loc) != nil {
				return nil, nil, nil, nil, domain.ErrContentIntegrity
			}
			found := -1
			for i, v := range selections {
				if v.SourceID == read.SourceID {
					found = i
					break
				}
			}
			path := source.CapturePath{Path: loc.Path}
			if cv.SelectedMode == "pinned" && src.Revision == "" {
				path.ReadID = id
			}
			if found < 0 {
				selections = append(selections, source.CaptureSelection{SourceID: read.SourceID, Revision: src.Revision, Paths: []source.CapturePath{path}})
			} else {
				selections[found].Paths = append(selections[found].Paths, path)
			}
		}
		existingIDs = nil
		bindings = nil
	}
	for i := range selections {
		paths := map[string]source.CapturePath{}
		for _, p := range selections[i].Paths {
			paths[p.Path] = p
		}
		selections[i].Paths = nil
		var keys []string
		for p := range paths {
			keys = append(keys, p)
		}
		sort.Strings(keys)
		for _, p := range keys {
			selections[i].Paths = append(selections[i].Paths, paths[p])
		}
	}
	return selections, existingIDs, bindings, limitations, nil
}
func (w *Worker) prepareFreshness(ctx context.Context, attempt domain.ProviderAttempt) (bool, error) {
	var m domain.MaintenanceIntent
	var obj domain.Object
	var digest string
	var required []domain.RequiredUnit
	var inputs []domain.ProviderInput
	err := w.Store.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(attempt.JobID)
		if e != nil {
			return e
		}
		if j.Kind != "maintenance" {
			return nil
		}
		m, e = tx.MaintenanceIntent(j.ID)
		if e != nil {
			return e
		}
		if m.Mode != domain.MaintenanceModeFreshness {
			return nil
		}
		a, e := tx.ProviderAttempt(attempt.ID)
		if e != nil {
			return e
		}
		if j.State == "queued" {
			e = app.GuardProviderContinuation(tx, a)
		} else {
			e = app.GuardProvider(tx, a)
		}
		if e != nil {
			return e
		}
		if a.State != "result_saved" {
			return domain.ErrStaleWorker
		}
		inputs, e = tx.ProviderInputs(a.ID)
		if e != nil {
			return e
		}
		digest = computeMaintenanceInputDigest(a.ID, a.ResultHash, inputs)
		required, e = app.AllRequiredUnits(tx, m.CoverageSetID)
		if e != nil {
			return e
		}
		obj, e = tx.Object(a.ResultHash)
		return e
	})
	if err != nil || m.Mode != domain.MaintenanceModeFreshness {
		return m.Mode == domain.MaintenanceModeFreshness, err
	}
	var result domain.FreshnessResult
	err = w.Bytes.Read(ctx, obj, domain.Pin{Kind: "provider_continuation", OwnerID: attempt.ID}, func(r io.ReadSeeker) error {
		var e error
		result, e = decodeFreshnessResult(r)
		return e
	})
	if err != nil {
		return true, err
	}
	if len(result.Units) != len(required) || result.Units == nil {
		return true, ErrMaintenanceInvalid
	}
	err = w.Store.Write(ctx, func(tx app.WriteTx) error {
		a, e := tx.ProviderAttempt(attempt.ID)
		if e != nil {
			return e
		}
		currentInputs, e := tx.ProviderInputs(a.ID)
		if e != nil {
			return e
		}
		if computeMaintenanceInputDigest(a.ID, a.ResultHash, currentInputs) != digest {
			return domain.ErrStaleWorker
		}
		set, e := tx.CoverageSetByID(m.CoverageSetID)
		if e != nil {
			return e
		}
		sourceIncomplete := false
		for _, l := range m.Limitations {
			if l.Kind == "source_unavailable" || l.Kind == "source_disclosure_restricted" || l.Kind == "partial_source" || l.Kind == "source_unverified" {
				sourceIncomplete = true
			}
		}
		seen := map[string]bool{}
		for _, u := range result.Units {
			if sourceIncomplete && u.Outcome == "assessed" {
				return ErrMaintenanceInvalid
			}
			p, r := "", ""
			if u.PagePath != nil {
				p = *u.PagePath
			}
			if u.RegionID != nil {
				r = *u.RegionID
			}
			key := u.Kind + "\x00" + p + "\x00" + r
			if seen[key] {
				return ErrMaintenanceInvalid
			}
			seen[key] = true
			found := false
			for _, want := range required {
				if want.Kind == u.Kind && want.PagePath == p && want.RegionID == r {
					found = true
				}
			}
			if !found || strings.TrimSpace(u.Rationale) == "" || u.Citations == nil {
				return ErrMaintenanceInvalid
			}
			switch u.Outcome {
			case "assessed":
			case "partial", "blocked", "not_checked":
				if strings.TrimSpace(u.Reason) == "" {
					return ErrMaintenanceInvalid
				}
			default:
				return ErrMaintenanceInvalid
			}
			if (u.Outcome == "assessed" || u.Discrepancy != "") && len(u.Citations) == 0 {
				return ErrMaintenanceInvalid
			}
			for _, c := range u.Citations {
				if c.TargetSetID != set.TargetSetID {
					return ErrMaintenanceInvalid
				}
			}
			parent, e := tx.ScanRun(m.ScanJobID)
			if e != nil {
				return e
			}
			if e = questions.ValidateSourceCitations(tx, parent.PrincipalID, m.VersionID, a, u.Citations); e != nil {
				return e
			}

		}
		prepared, _ := json.Marshal(domain.PreparedFreshness{InputDigest: digest, Output: result})
		return tx.RecordPreparedMaintenanceOutput(m.JobID, attempt.ID, prepared)
	})
	return true, err
}

func (w *Worker) applyFreshness(tx app.WriteTx, a domain.ProviderAttempt, m domain.MaintenanceIntent) error {
	var prepared domain.PreparedFreshness
	if m.PreparedAttemptID != a.ID || json.Unmarshal(m.PreparedOutputJSON, &prepared) != nil {
		return domain.ErrReconciliation
	}
	inputs, err := tx.ProviderInputs(a.ID)
	if err != nil {
		return err
	}
	if prepared.InputDigest != computeMaintenanceInputDigest(a.ID, a.ResultHash, inputs) {
		return domain.ErrReconciliation
	}
	parent, err := tx.Job(m.ScanJobID)
	if err != nil {
		return err
	}
	run, err := tx.ScanRun(parent.ID)
	if err != nil {
		return err
	}
	if parent.CancelRequested || parent.State != "running" {
		return domain.ErrStaleWorker
	}
	if err = w.checkGlobalRunAuthority(tx, parent, run, w.now()); err != nil {
		return err
	}
	cv, _, _, ok := w.checkCandidateTargetFence(tx, run, m.VersionID, "codex")
	if !ok {
		return domain.ErrDenied
	}
	_ = cv
	cfg, err := tx.Configuration()
	if err != nil {
		return err
	}
	policy, err := ResolveEffectivePolicy(cfg, run.RequestedScope.ProjectIDs, run.EffectivePolicy.Depth)
	if err != nil {
		return err
	}
	mode := StricterAutoApplyMode(run.EffectivePolicy.AutoApply.FreshnessAssessment, policy.AutoApply.FreshnessAssessment)
	if mode == "disabled" {
		return domain.ErrDenied
	}
	set, err := tx.CoverageSetByID(m.CoverageSetID)
	if err != nil {
		return err
	}
	now := w.now().Format(time.RFC3339Nano)
	id, err := service.NewID()
	if err != nil {
		return err
	}
	units := []domain.AssessmentUnit{}
	for _, o := range prepared.Output.Units {
		u := domain.AssessmentUnit{CoverageSetID: set.ID, AssessmentID: id, TargetSetID: set.TargetSetID, Kind: o.Kind, Outcome: o.Outcome, Discrepancy: o.Discrepancy, Reason: o.Reason}
		if o.PagePath != nil {
			u.PagePath = *o.PagePath
		}
		if o.RegionID != nil {
			u.RegionID = *o.RegionID
		}
		units = append(units, u)
	}
	evidence, _ := json.Marshal(prepared.Output)
	aRecord := domain.Assessment{ID: id, VersionID: m.VersionID, TargetSetID: set.TargetSetID, CoverageSetID: set.ID, ScopeJSON: set.ScopeJSON, State: domain.AssessmentState(units), EvidenceJSON: evidence, ConfigRevision: cfg.Revision, PermissionsRevision: run.PermissionsRevisions[m.ProjectID], Origin: "ai", AssessedAt: now}
	fid, err := service.NewID()
	if err != nil {
		return err
	}
	status := "proposed"
	var applied json.RawMessage
	if mode == "auto" {
		if err = tx.PutAssessment(aRecord, units); err != nil {
			return err
		}
		if err = tx.ClearAssessmentTriggers(aRecord); err != nil {
			return err
		}
		if err = tx.AppendEvent("version", m.VersionID, "assessment.published", 1, []byte(`{"version_id":"`+m.VersionID+`","assessment_id":"`+id+`"}`)); err != nil {
			return err
		}
		status = "applied"
		applied, _ = json.Marshal(map[string]any{"assessment_id": id, "coverage_set_id": set.ID})
	}
	proposal, _ := json.Marshal(aRecord)
	f := domain.Finding{ID: fid, JobID: parent.ID, ProjectID: m.ProjectID, DocumentID: freshPtr(m.DocumentID), VersionID: freshPtr(m.VersionID), Kind: "freshness_assessment", Origin: "ai", TargetSetID: freshPtr(set.TargetSetID), Evidence: evidence, EvidenceFingerprint: m.JobID, Rationale: "Exact-target scoped freshness assessment", Proposal: proposal, Status: status, ApprovalRequired: mode != "auto", AppliedRef: applied, CapturedConfigRevision: cfg.Revision, PermissionsRevision: run.PermissionsRevisions[m.ProjectID], Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = tx.PutFinding(f); err != nil {
		return err
	}
	return nil
}

func freshPtr(s string) *string { return &s }
func freshJSONEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	aa, _ := json.Marshal(x)
	bb, _ := json.Marshal(y)
	return string(aa) == string(bb)
}

func decodeFreshnessResult(r io.Reader) (domain.FreshnessResult, error) {
	var result domain.FreshnessResult
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var raw json.RawMessage
	if e := dec.Decode(&raw); e != nil {
		return result, e
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return result, ErrMaintenanceInvalid
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil || len(root) != 1 || root["units"] == nil {
		return result, ErrMaintenanceInvalid
	}
	var units []map[string]json.RawMessage
	if json.Unmarshal(root["units"], &units) != nil || units == nil {
		return result, ErrMaintenanceInvalid
	}
	for _, u := range units {
		if len(u) != 8 {
			return result, ErrMaintenanceInvalid
		}
		for _, k := range []string{"kind", "outcome", "discrepancy", "reason", "rationale"} {
			var text string
			if string(u[k]) == "null" || json.Unmarshal(u[k], &text) != nil {
				return result, ErrMaintenanceInvalid
			}
		}
		for _, k := range []string{"kind", "page_path", "region_id", "outcome", "discrepancy", "reason", "rationale", "citations"} {
			if u[k] == nil {
				return result, ErrMaintenanceInvalid
			}
		}
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if e := d.Decode(&result); e != nil {
		return result, e
	}
	return result, nil
}

// Imported source aliases are author metadata, not disclosure authorities.
// Bind a Git alias only through the service's unique approved repo mapping.
func freshnessSourceAliases(tx app.WriteTx, project, version string) (map[string]string, error) {
	out := map[string]string{}
	mappings, e := source.Mappings(tx, project)
	if e != nil {
		return nil, e
	}
	rows, e := tx.VersionSources(version)
	if e != nil {
		return nil, e
	}
	for _, r := range rows {
		var meta struct {
			Kind   string `json:"kind"`
			RepoID string `json:"repo_id"`
		}
		if json.Unmarshal(r.Metadata, &meta) != nil {
			return nil, domain.ErrContentIntegrity
		}
		if meta.Kind == "snapshot" {
			files := []string{}
			for _, m := range mappings {
				if m.Kind == "files" {
					files = append(files, m.SourceID)
					if m.SourceID == r.ID {
						out[r.ID] = m.SourceID
					}
				}
			}
			if out[r.ID] == "" && len(files) == 1 {
				out[r.ID] = files[0]
			}
		}
		if meta.Kind == "git" {
			m, e := source.LinkGit(mappings, meta.RepoID)
			if e == nil {
				out[r.ID] = m.SourceID
			}
		}
	}
	return out, nil
}

func freshOptionalPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
