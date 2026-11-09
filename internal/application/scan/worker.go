package scan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/search"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type Worker struct {
	Store    app.Store
	Bytes    search.ObjectReader
	Sources  *source.Service
	Identity func(string, json.RawMessage, domain.ProviderInvocation, []domain.CapturedRead) (string, error)
	Now      func() time.Time
	StepHook func(jobID, stage string)
}

func NewWorker(store app.Store, bytes search.ObjectReader) *Worker {
	return &Worker{
		Store: store,
		Bytes: bytes,
		Now:   time.Now,
	}
}

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}

func (w *Worker) Process(ctx context.Context, jobID string) error {
	var j domain.Job
	var run domain.ScanRun
	var budgetExhausted bool
	var authorityRevoked bool

	err := w.Store.Write(ctx, func(tx app.WriteTx) error {
		var err error
		j, err = tx.Job(jobID)
		if err != nil {
			return err
		}
		if j.Kind != "scan" {
			return domain.ErrTransition
		}
		if j.CancelRequested {
			return tx.ConfirmStop(jobID, j.AttemptGeneration)
		}
		if j.State == "paused" {
			return nil
		}
		if j.State == "interrupted" {
			if err = tx.ReconcileJob(jobID); err != nil {
				return err
			}
			j, err = tx.Job(jobID)
			if err != nil {
				return err
			}
			if j.CancelRequested || j.State != "queued" {
				return nil
			}
		}
		var hadStartedAt bool
		if j.State == "queued" {
			if preRun, e := tx.ScanRun(jobID); e == nil && preRun.StartedAt != nil && *preRun.StartedAt != "" {
				hadStartedAt = true
			}
			j, err = tx.ClaimJob(jobID)
			if err != nil {
				return err
			}
		} else {
			hadStartedAt = true
		}
		if j.State != "running" {
			return nil
		}
		run, err = tx.ScanRun(jobID)
		if err != nil {
			return err
		}

		now := w.now()
		if !hadStartedAt || run.StartedAt == nil || *run.StartedAt == "" {
			nowStr := now.Format(time.RFC3339Nano)
			run.StartedAt = &nowStr
			run.UpdatedAt = nowStr
			if err := tx.UpdateScanRun(run); err != nil {
				return err
			}
		}
		elapsedMs, exhausted := w.currentElapsedAndBudget(tx, run, now)
		if exhausted {
			budgetExhausted = true
			if err := w.exhaustRunBudget(tx, j, run, elapsedMs, now); err != nil {
				return err
			}
			return nil
		}

		if err := w.checkGlobalRunAuthority(tx, j, run, now); err != nil {
			if errors.Is(err, domain.ErrSearchRestricted) || errors.Is(err, domain.ErrDenied) {
				authorityRevoked = true
				return w.revokeRunAuthority(tx, j, run, "authority_revoked", now)
			}
			return err
		}
		return nil
	})
	if err != nil || j.CancelRequested || j.State != "running" || budgetExhausted || authorityRevoked {
		return err
	}

	if w.StepHook != nil {
		w.StepHook(jobID, run.Stage)
	}

	if !run.EnumerationComplete {
		return w.stepInventory(ctx, j)
	}
	return w.stepInspectAndRepair(ctx, j)
}

func (w *Worker) effectiveMaxRunSeconds(tx app.WriteTx, run domain.ScanRun) int {
	maxRunSec := run.EffectivePolicy.MaxRunSeconds
	if cfg, err := tx.Configuration(); err == nil {
		maxRunSec = EffectiveMaxRunSeconds(maxRunSec, cfg, run.RequestedScope.ProjectIDs)
	}
	return maxRunSec
}

func (w *Worker) currentElapsedAndBudget(tx app.WriteTx, run domain.ScanRun, now time.Time) (int64, bool) {
	elapsedMs := run.ElapsedMs
	if run.StartedAt != nil && *run.StartedAt != "" {
		if started, e := time.Parse(time.RFC3339Nano, *run.StartedAt); e == nil {
			if delta := now.Sub(started).Milliseconds(); delta > 0 {
				elapsedMs += delta
			}
		}
	}
	maxRunSec := w.effectiveMaxRunSeconds(tx, run)
	return elapsedMs, maxRunSec > 0 && elapsedMs >= int64(maxRunSec)*1000
}

func (w *Worker) checkGlobalRunAuthority(tx app.WriteTx, j domain.Job, run domain.ScanRun, now time.Time) error {
	authState, err := tx.AuthState()
	if err != nil {
		return err
	}
	var parentScope struct {
		PrincipalID string `json:"principal_id"`
		AuthEpoch   string `json:"auth_epoch"`
		AuthKind    string `json:"auth_kind"`
	}
	_ = json.Unmarshal(j.Scope, &parentScope)
	if parentScope.AuthEpoch != "" && parentScope.AuthEpoch != authState.AuthEpoch {
		return domain.ErrSearchRestricted
	}
	if run.AuthKind == "owner" {
		if authState.OwnerID == "" || run.PrincipalID != authState.OwnerID {
			return domain.ErrSearchRestricted
		}
	} else {
		wp, err := tx.WorkflowPrincipal(run.PrincipalID)
		if err != nil || !wp.Active {
			return domain.ErrSearchRestricted
		}
		effScope := auth.RestrictScope(run.CapturedScope, wp.Scope)
		if !containsScanAction(effScope.Actions, "scan.request") {
			return domain.ErrSearchRestricted
		}
	}
	return nil
}

func containsScanAction(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

func (w *Worker) exhaustRunBudget(tx app.WriteTx, j domain.Job, run domain.ScanRun, elapsedMs int64, now time.Time) error {
	nowStr := now.Format(time.RFC3339Nano)
	// Settle any active repair children before skipping remaining items.
	if err := w.cancelActiveRepairChildren(tx, j.ID, "run_budget_exhausted", nowStr); err != nil {
		return err
	}
	if err := w.materializeRemainingUnitsAs(tx, &run, "skipped", "run_budget_exhausted", false, nowStr); err != nil {
		return err
	}
	if err := tx.SkipRemainingScanItems(j.ID, "run_budget_exhausted"); err != nil {
		return err
	}
	run.ElapsedMs = elapsedMs
	run.Stage = "complete"
	run.Report.BudgetExhausted = true
	run.Report.Semantic.Complete = false
	run.UpdatedAt = nowStr
	if err := tx.UpdateScanRun(run); err != nil {
		return err
	}
	if curJob, err := tx.Job(j.ID); err == nil && curJob.ProviderAttempts != nil && curJob.ProviderAttempts.Unknown > 0 {
		return tx.PauseProviderJob(j.ID, "outcome_unknown")
	}
	return tx.FinishJob(j.ID, j.AttemptGeneration, "", func(tx app.WriteTx) error {
		return tx.ReleaseOwnerPins("job", j.ID)
	})
}

func (w *Worker) revokeRunAuthority(tx app.WriteTx, j domain.Job, run domain.ScanRun, reason string, now time.Time) error {
	nowStr := now.Format(time.RFC3339Nano)
	if err := w.cancelActiveRepairChildren(tx, j.ID, reason, nowStr); err != nil {
		return err
	}
	if err := w.materializeRemainingUnitsAs(tx, &run, "blocked", reason, false, nowStr); err != nil {
		return err
	}
	afterOrd := 0
	for {
		items, err := tx.JobItems(j.ID, afterOrd, 256)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			break
		}
		for _, it := range items {
			afterOrd = it.Ordinal
			if it.State == "pending" || it.State == "running" {
				it.State = "blocked"
				it.Reason = reason
				it.Stage = "complete"
				it.UpdatedAt = nowStr
				if err := tx.UpdateJobItem(it); err != nil {
					return err
				}
			}
		}
	}
	run.Stage = "complete"
	run.UpdatedAt = nowStr
	if err := tx.UpdateScanRun(run); err != nil {
		return err
	}
	if curJob, err := tx.Job(j.ID); err == nil && curJob.ProviderAttempts != nil && curJob.ProviderAttempts.Unknown > 0 {
		return tx.PauseProviderJob(j.ID, "outcome_unknown")
	}
	return tx.FinishJob(j.ID, j.AttemptGeneration, "", func(tx app.WriteTx) error {
		return tx.ReleaseOwnerPins("job", j.ID)
	})
}

func (w *Worker) cancelActiveRepairChildren(tx app.WriteTx, scanJobID, reason, nowStr string) error {
	seenChild := map[string]bool{}
	cancelChildID := func(cid string) error {
		if cid == "" || seenChild[cid] {
			return nil
		}
		seenChild[cid] = true
		child, err := tx.Job(cid)
		if err != nil {
			return err
		}
		if child.State != "completed" && child.State != "failed" && child.State != "cancelled" {
			if !child.CancelRequested {
				child, err = tx.CancelJob(child.ID)
				if err != nil {
					return err
				}
			}
			if child.State != "completed" && child.State != "failed" && child.State != "cancelled" && (!domain.ProviderJob(child.Kind) || child.ProviderAttempts == nil || child.ProviderAttempts.Unknown == 0) {
				if err := tx.ConfirmStop(child.ID, child.AttemptGeneration); err != nil {
					return err
				}
			}
		}
		return nil
	}
	mIntents, err := tx.MaintenanceIntentsByScan(scanJobID)
	if err != nil {
		return err
	}
	for _, m := range mIntents {
		if err := cancelChildID(m.JobID); err != nil {
			return err
		}
	}
	afterOrd := 0
	for {
		items, err := tx.JobItems(scanJobID, afterOrd, 256)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			break
		}
		for _, it := range items {
			afterOrd = it.Ordinal
			if it.RepairJobID != "" {
				if err := cancelChildID(it.RepairJobID); err != nil {
					return err
				}
			}
			mIntent, ok, e := tx.MaintenanceIntentByItem(scanJobID, it.ItemID)
			if e != nil {
				return e
			}
			if ok && mIntent.JobID != "" {
				if err := cancelChildID(mIntent.JobID); err != nil {
					return err
				}
			}
			if it.FindingID != "" {
				if f, e := tx.Finding(it.FindingID); e == nil && f.Status == "proposed" {
					f.Status = "blocked"
					f.Reason = reason
					f.Revision++
					f.UpdatedAt = nowStr
					if err := tx.PutFinding(f); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (w *Worker) materializeRemainingUnitsAs(tx app.WriteTx, run *domain.ScanRun, state, reason string, purged bool, nowStr string) error {
	if run.EnumerationComplete {
		return nil
	}
	afterOrd := 0
	for {
		cvs, err := tx.ScanCapturedVersions(run.JobID, afterOrd, 64)
		if err != nil {
			return err
		}
		if len(cvs) == 0 {
			break
		}
		for _, cv := range cvs {
			afterOrd = cv.VersionOrdinal
			if err := w.materializeVersionRemainder(tx, run, &cv, state, reason, purged, nowStr); err != nil {
				return err
			}
			run.EnumVersionOrdinal = cv.VersionOrdinal
		}
	}
	run.EnumerationComplete = true
	run.EnumAfterPath = ""
	run.EnumAfterRegion = ""
	return nil
}

func (w *Worker) materializeVersionRemainder(tx app.WriteTx, run *domain.ScanRun, cv *domain.ScanCapturedVersion, state, reason string, purged bool, nowStr string) error {
	if cv.MaterializedUnits >= cv.UnitCount {
		if cv.EnumState != "complete" && cv.EnumState != "purged" {
			cv.EnumState = "complete"
			return tx.UpdateScanCapturedVersion(*cv)
		}
		return nil
	}
	detail := []byte("{}")
	if purged || cv.EnumState == "purged" || reason == "document_purged" {
		detail = []byte(`{"purged":true}`)
	}
	for idx := cv.MaterializedUnits; idx < cv.UnitCount; idx++ {
		ord := run.NextItemOrdinal
		run.NextItemOrdinal++
		itemState := state
		itemReason := reason
		if cv.ExclusionReason != "" {
			itemState = "skipped"
			itemReason = cv.ExclusionReason
		}
		uk := w.unitKindAtOffset(*cv, idx)
		item := domain.JobItem{
			JobID:       run.JobID,
			ItemID:      fmt.Sprintf("item-%06d", ord),
			Ordinal:     ord,
			UnitKind:    uk,
			ProjectID:   cv.ProjectID,
			DocumentID:  cv.DocumentID,
			VersionID:   cv.VersionID,
			TargetSetID: cv.TargetSetID,
			Stage:       "complete",
			State:       itemState,
			Reason:      itemReason,
			Detail:      detail,
			UpdatedAt:   nowStr,
		}
		if err := tx.PutJobItem(item); err != nil {
			return err
		}
		cv.MaterializedUnits++
	}
	if purged || cv.EnumState == "purged" || reason == "document_purged" || cv.ExclusionReason == "document_purged" {
		cv.EnumState = "purged"
	} else {
		cv.EnumState = "complete"
	}
	cv.EnumAfterPath = ""
	cv.EnumAfterRegion = ""
	cv.EntryAfterPath = ""
	cv.SourceAfterIdx = 0
	return tx.UpdateScanCapturedVersion(*cv)
}

func (w *Worker) unitKindAtOffset(cv domain.ScanCapturedVersion, idx int) string {
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
	sourceUnits := cv.SourceCount
	if sourceUnits == 0 {
		sourceUnits = 1
	}
	if rem < sourceUnits {
		return "source"
	}
	return "index"
}

func (w *Worker) stepInventory(ctx context.Context, j domain.Job) error {
	return w.Store.Write(ctx, func(tx app.WriteTx) error {
		curJob, err := tx.Job(j.ID)
		if err != nil {
			return err
		}
		if curJob.CancelRequested {
			return tx.ConfirmStop(j.ID, curJob.AttemptGeneration)
		}
		if curJob.State != "running" || curJob.AttemptGeneration != j.AttemptGeneration {
			return domain.ErrStaleWorker
		}
		run, err := tx.ScanRun(j.ID)
		if err != nil {
			return err
		}
		if run.EnumerationComplete {
			return nil
		}
		nowStr := w.now().Format(time.RFC3339Nano)
		cvs, err := tx.ScanCapturedVersions(j.ID, run.EnumVersionOrdinal, 16)
		if err != nil {
			return err
		}
		if len(cvs) == 0 {
			run.EnumerationComplete = true
			run.Stage = "inspect"
			run.EnumAfterPath = ""
			run.EnumAfterRegion = ""
			run.UpdatedAt = nowStr
			return tx.UpdateScanRun(run)
		}

		budget := 64
		for i := range cvs {
			if budget <= 0 {
				break
			}
			cv := cvs[i]
			used, done, err := w.enumerateCapturedVersionBatch(tx, &run, &cv, budget, nowStr)
			if err != nil {
				return err
			}
			budget -= used
			if done {
				run.EnumVersionOrdinal = cv.VersionOrdinal
				run.EnumAfterPath = ""
				run.EnumAfterRegion = ""
			} else {
				run.EnumAfterPath = cv.EnumAfterPath
				run.EnumAfterRegion = cv.EnumAfterRegion
				break
			}
		}

		remaining, err := tx.ScanCapturedVersions(j.ID, run.EnumVersionOrdinal, 1)
		if err != nil {
			return err
		}
		if len(remaining) == 0 {
			run.EnumerationComplete = true
			run.Stage = "inspect"
			run.EnumAfterPath = ""
			run.EnumAfterRegion = ""
		}
		run.UpdatedAt = nowStr
		return tx.UpdateScanRun(run)
	})
}

func (w *Worker) enumerateCapturedVersionBatch(tx app.WriteTx, run *domain.ScanRun, cv *domain.ScanCapturedVersion, budget int, nowStr string) (int, bool, error) {
	if cv.MaterializedUnits >= cv.UnitCount {
		if cv.EnumState != "complete" && cv.EnumState != "purged" {
			cv.EnumState = "complete"
			if err := tx.UpdateScanCapturedVersion(*cv); err != nil {
				return 0, false, err
			}
		}
		return 0, true, nil
	}

	// Check if excluded at admission or if document was trashed/purged since admission.
	if cv.ExclusionReason != "" || cv.EnumState == "purged" {
		rem := cv.UnitCount - cv.MaterializedUnits
		reason := cv.ExclusionReason
		purged := cv.EnumState == "purged" || reason == "document_purged"
		if reason == "" && purged {
			reason = "document_purged"
		}
		if err := w.materializeVersionRemainder(tx, run, cv, "skipped", reason, purged, nowStr); err != nil {
			return 0, false, err
		}
		return rem, true, nil
	}

	if cv.DocumentID != "" {
		doc, err := tx.Document(cv.DocumentID)
		if errors.Is(err, domain.ErrNotFound) || (err == nil && doc.Lifecycle == "purged") {
			rem := cv.UnitCount - cv.MaterializedUnits
			cv.ExclusionReason = "document_purged"
			if err := w.materializeVersionRemainder(tx, run, cv, "skipped", "document_purged", true, nowStr); err != nil {
				return 0, false, err
			}
			return rem, true, nil
		}
		if err != nil {
			return 0, false, err
		}
		if doc.Lifecycle == "trash" {
			rem := cv.UnitCount - cv.MaterializedUnits
			cv.ExclusionReason = "document_trashed"
			if err := w.materializeVersionRemainder(tx, run, cv, "skipped", "document_trashed", false, nowStr); err != nil {
				return 0, false, err
			}
			return rem, true, nil
		}
	}

	// Check if project permissions changed since admission; if so, materialize remaining units as blocked without reading private tables.
	if pScope, err := tx.Scope(cv.ProjectID); err == nil {
		if capturedPerm, ok := run.PermissionsRevisions[cv.ProjectID]; !ok || capturedPerm != pScope.Project.PermissionsRevision {
			rem := cv.UnitCount - cv.MaterializedUnits
			if err := w.materializeVersionRemainder(tx, run, cv, "blocked", "permission_changed", false, nowStr); err != nil {
				return 0, false, err
			}
			return rem, true, nil
		}
	}
	if run.AuthKind != "owner" {
		cfg, cfgErr := tx.Configuration()
		wp, wpErr := tx.WorkflowPrincipal(run.PrincipalID)
		effScope := auth.RestrictScope(run.CapturedScope, wp.Scope)
		revoked := cfgErr != nil || !projectPostWorkScanEnabled(cfg.Value, cv.ProjectID) || wpErr != nil || !wp.Active || !containsScanAction(effScope.Actions, "scan.request") || !containsScanAction(effScope.ProjectIDs, cv.ProjectID)
		if !revoked && cv.VersionID != "" {
			authState, _ := tx.AuthState()
			actor := domain.Session{
				PrincipalID: run.PrincipalID,
				AuthEpoch:   authState.AuthEpoch,
				Scope:       effScope,
			}
			if _, _, err := app.CheckSourceVersion(tx, actor, cv.VersionID); err != nil {
				revoked = true
			}
		}
		if revoked {
			rem := cv.UnitCount - cv.MaterializedUnits
			if err := w.materializeVersionRemainder(tx, run, cv, "blocked", "authority_revoked", false, nowStr); err != nil {
				return 0, false, err
			}
			return rem, true, nil
		}
	}

	used := 0
	pEnd := 1 + cv.PageCount + cv.RegionCount
	eEnd := pEnd + cv.EntryCount
	sourceUnits := cv.SourceCount
	if sourceUnits == 0 {
		sourceUnits = 1
	}
	sEnd := eEnd + sourceUnits

	emit := func(unitKind, pagePath, regionID, sourceID string, detail []byte) error {
		ord := run.NextItemOrdinal
		run.NextItemOrdinal++
		item := domain.JobItem{
			JobID:       run.JobID,
			ItemID:      fmt.Sprintf("item-%06d", ord),
			Ordinal:     ord,
			UnitKind:    unitKind,
			ProjectID:   cv.ProjectID,
			DocumentID:  cv.DocumentID,
			VersionID:   cv.VersionID,
			PagePath:    pagePath,
			RegionID:    regionID,
			SourceID:    sourceID,
			TargetSetID: cv.TargetSetID,
			Stage:       "inspect",
			State:       "pending",
			Detail:      detail,
			UpdatedAt:   nowStr,
		}
		if err := tx.PutJobItem(item); err != nil {
			return err
		}
		cv.MaterializedUnits++
		used++
		budget--
		return nil
	}

	// 1. Version unit (offset 0).
	if cv.MaterializedUnits == 0 && budget > 0 {
		if err := emit("version", "", "", "", []byte("{}")); err != nil {
			return used, false, err
		}
	}

	// 2. Page and region units (offsets 1 .. pEnd-1).
	for cv.MaterializedUnits < pEnd && budget > 0 {
		if cv.RegionCount > 0 && cv.EnumAfterPath != "" && (cv.EnumAfterRegion != "" || cv.SourceAfterIdx == 1) {
			regs, err := tx.VersionRegionsAfter(cv.VersionID, cv.EnumAfterPath, run.RequestedScope.RegionID, cv.EnumAfterRegion, budget+1)
			if err != nil {
				return used, false, err
			}
			if len(regs) == 0 {
				cv.EnumAfterRegion = ""
				cv.SourceAfterIdx = 0
			} else {
				for idx, r := range regs {
					if cv.MaterializedUnits >= pEnd || budget <= 0 {
						break
					}
					if err := emit("region", r.PagePath, r.ID, "", []byte("{}")); err != nil {
						return used, false, err
					}
					if idx+1 < len(regs) {
						cv.EnumAfterRegion = r.ID
						cv.SourceAfterIdx = 1
					} else {
						cv.EnumAfterRegion = ""
						cv.SourceAfterIdx = 0
					}
				}
			}
			if cv.MaterializedUnits >= pEnd || budget <= 0 {
				break
			}
		}

		if cv.PageCount == 0 {
			break
		}
		pages, err := tx.VersionPagesAfter(cv.VersionID, run.RequestedScope.PathPrefix, cv.EnumAfterPath, 16)
		if err != nil {
			return used, false, err
		}
		if len(pages) == 0 {
			return used, false, domain.ErrContentIntegrity
		}
		for _, pg := range pages {
			if cv.MaterializedUnits >= pEnd || budget <= 0 {
				break
			}
			if err := emit("page", pg.Path, "", "", []byte("{}")); err != nil {
				return used, false, err
			}
			cv.EnumAfterPath = pg.Path
			cv.EnumAfterRegion = ""
			cv.SourceAfterIdx = 0

			if cv.RegionCount > 0 && cv.MaterializedUnits < pEnd {
				regs, err := tx.VersionRegionsAfter(cv.VersionID, pg.Path, run.RequestedScope.RegionID, "", budget+1)
				if err != nil {
					return used, false, err
				}
				if len(regs) > 0 && budget <= 0 {
					cv.SourceAfterIdx = 1
					break
				}
				for idx, r := range regs {
					if cv.MaterializedUnits >= pEnd || budget <= 0 {
						break
					}
					if err := emit("region", r.PagePath, r.ID, "", []byte("{}")); err != nil {
						return used, false, err
					}
					if idx+1 < len(regs) {
						cv.EnumAfterRegion = r.ID
						cv.SourceAfterIdx = 1
					} else {
						cv.EnumAfterRegion = ""
						cv.SourceAfterIdx = 0
					}
				}
			}
		}
	}
	if cv.MaterializedUnits >= pEnd {
		cv.SourceAfterIdx = 0
	}

	// 3. Entry units (offsets pEnd .. eEnd-1).
	for cv.MaterializedUnits >= pEnd && cv.MaterializedUnits < eEnd && budget > 0 {
		entries, err := tx.Entries(cv.ContentSetID, cv.EntryAfterPath, 64)
		if err != nil {
			return used, false, err
		}
		if len(entries) == 0 {
			return used, false, domain.ErrContentIntegrity
		}
		for _, e := range entries {
			cv.EntryAfterPath = e.Path
			if !matchesPathPrefix(e.Path, run.RequestedScope.PathPrefix) {
				continue
			}
			detailMap := map[string]any{
				"hash": e.Hash,
				"size": e.Size,
				"kind": e.Kind,
			}
			if e.Kind == "symlink" {
				detailMap["link_text"] = e.LinkText
			}
			detail, _ := json.Marshal(detailMap)
			if err := emit("entry", e.Path, "", "", detail); err != nil {
				return used, false, err
			}
			if cv.MaterializedUnits >= eEnd || budget <= 0 {
				break
			}
		}
	}

	// 4. Source units (offsets eEnd .. sEnd-1).
	if cv.MaterializedUnits >= eEnd && cv.MaterializedUnits < sEnd && budget > 0 {
		if cv.SourceCount == 0 {
			if err := emit("source", "", "", "", []byte("{}")); err != nil {
				return used, false, err
			}
		} else {
			sources, err := tx.VersionSources(cv.VersionID)
			if err != nil {
				return used, false, err
			}
			for _, src := range sources {
				if cv.EnumAfterRegion != "" && src.ID <= cv.EnumAfterRegion {
					continue
				}
				if cv.MaterializedUnits >= sEnd || budget <= 0 {
					break
				}
				if err := emit("source", "", "", src.ID, []byte("{}")); err != nil {
					return used, false, err
				}
				cv.EnumAfterRegion = src.ID
			}
			for cv.MaterializedUnits < sEnd && budget > 0 {
				if err := emit("source", "", "", "", []byte("{}")); err != nil {
					return used, false, err
				}
			}
			if cv.MaterializedUnits >= sEnd {
				cv.EnumAfterRegion = ""
			}
		}
	}

	// 5. Index unit (offset sEnd == cv.UnitCount-1).
	if cv.MaterializedUnits == sEnd && cv.MaterializedUnits < cv.UnitCount && budget > 0 {
		if err := emit("index", "", "", "", []byte("{}")); err != nil {
			return used, false, err
		}
	}

	done := cv.MaterializedUnits >= cv.UnitCount
	if done {
		cv.EnumState = "complete"
		cv.EnumAfterPath = ""
		cv.EnumAfterRegion = ""
	} else if cv.MaterializedUnits >= eEnd {
		cv.EnumState = "entries_done"
	} else if cv.MaterializedUnits >= pEnd {
		cv.EnumState = "regions_done"
	} else if cv.MaterializedUnits >= 1+cv.PageCount && cv.EnumAfterRegion == "" {
		cv.EnumState = "pages_done"
	} else {
		cv.EnumState = "pending"
	}
	if err := tx.UpdateScanCapturedVersion(*cv); err != nil {
		return used, false, err
	}
	return used, done, nil
}

func matchesPathPrefix(path, prefix string) bool {
	if prefix == "" {
		return true
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
