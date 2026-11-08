package scan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"time"

	"haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/references"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func (w *Worker) stepInspectAndRepair(ctx context.Context, j domain.Job) error {
	var item domain.JobItem
	var hasItem bool
	var run domain.ScanRun
	var settledInTx bool

	err := w.Store.Write(ctx, func(tx app.WriteTx) error {
		curJob, err := tx.Job(j.ID)
		if err != nil {
			return err
		}
		if curJob.CancelRequested {
			settledInTx = true
			return tx.ConfirmStop(j.ID, curJob.AttemptGeneration)
		}
		if curJob.State != "running" || curJob.AttemptGeneration != j.AttemptGeneration {
			return domain.ErrStaleWorker
		}
		run, err = tx.ScanRun(j.ID)
		if err != nil {
			return err
		}
		nowStr := w.now().Format(time.RFC3339Nano)

		// Recover any leftover 'running' items from an interrupted turn back to 'pending' first.
		var activeRunning []domain.JobItem
		afterRunOrd := 0
		for {
			runningBatch, err := tx.JobItemsByState(j.ID, "running", afterRunOrd, 256)
			if err != nil {
				return err
			}
			if len(runningBatch) == 0 {
				break
			}
			for _, ri := range runningBatch {
				afterRunOrd = ri.Ordinal
				if ri.AttemptGeneration != j.AttemptGeneration {
					ri.State = "pending"
					ri.UpdatedAt = nowStr
					if err := tx.UpdateJobItem(ri); err != nil {
						return err
					}
				} else {
					activeRunning = append(activeRunning, ri)
				}
			}
		}

		item, hasItem, err = tx.NextPendingJobItem(j.ID)
		if err != nil {
			return err
		}
		if !hasItem {
			if len(activeRunning) > 0 {
				item = activeRunning[0]
				hasItem = true
			} else {
				// All items are terminal; complete the scan run.
				settledInTx = true
				run.Stage = "complete"
				run.UpdatedAt = nowStr
				if run.StartedAt != nil && *run.StartedAt != "" {
					if started, e := time.Parse(time.RFC3339Nano, *run.StartedAt); e == nil {
						if delta := w.now().Sub(started).Milliseconds(); delta > 0 {
							run.ElapsedMs += delta
						}
					}
				}
				if err := tx.UpdateScanRun(run); err != nil {
					return err
				}
				return tx.FinishJob(j.ID, j.AttemptGeneration, "", func(tx app.WriteTx) error {
					return tx.ReleaseOwnerPins("job", j.ID)
				})
			}
		}

		// Per-item lifecycle, permission, and authority fence before inspecting or continuing repair.
		if reason, state, purged, fenced := w.checkItemFence(tx, j, run, item); fenced {
			settledInTx = true
			if item.RepairJobID != "" {
				_ = w.cancelSingleRepairChild(tx, item.RepairJobID, reason, nowStr)
			}
			item.State = state
			item.Reason = reason
			item.Stage = "complete"
			item.AttemptGeneration = j.AttemptGeneration
			item.UpdatedAt = nowStr
			if purged {
				item.PagePath = ""
				item.RegionID = ""
				item.SourceID = ""
				item.TargetSetID = ""
				item.Detail = []byte(`{"purged":true}`)
			}
			if item.FindingID != "" {
				if f, e := tx.Finding(item.FindingID); e == nil && f.Status == "proposed" {
					if state == "skipped" {
						f.Status = "stale"
					} else {
						f.Status = "blocked"
					}
					f.Reason = reason
					f.Revision++
					f.UpdatedAt = nowStr
					_ = tx.PutFinding(f)
				}
			}
			if err := tx.UpdateJobItem(item); err != nil {
				return err
			}
			run.UpdatedAt = nowStr
			return tx.UpdateScanRun(run)
		}

		if item.UnitKind != "index" || item.RepairJobID == "" {
			item.State = "running"
			item.AttemptGeneration = j.AttemptGeneration
			item.UpdatedAt = nowStr
			if err := tx.UpdateJobItem(item); err != nil {
				return err
			}
		} else if item.AttemptGeneration != j.AttemptGeneration {
			// Sync generation after legitimate parent resume while authority still holds.
			item.AttemptGeneration = j.AttemptGeneration
			item.UpdatedAt = nowStr
			if err := tx.UpdateJobItem(item); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil || settledInTx || !hasItem {
		return err
	}

	switch item.UnitKind {
	case "document":
		return w.completeItem(ctx, j, item, "skipped", item.Reason, item.Detail)
	case "entry":
		return w.inspectEntryItem(ctx, j, run, item)
	case "version":
		return w.inspectVersionItem(ctx, j, run, item)
	case "page":
		return w.inspectPageItem(ctx, j, run, item)
	case "region":
		return w.inspectRegionItem(ctx, j, run, item)
	case "source":
		return w.inspectSourceItem(ctx, j, run, item)
	case "index":
		return w.inspectOrRepairIndexItem(ctx, j, run, item)
	default:
		return w.completeItem(ctx, j, item, "failed", "unsupported_unit_kind", []byte("{}"))
	}
}

func (w *Worker) checkItemFence(tx app.WriteTx, j domain.Job, run domain.ScanRun, item domain.JobItem) (reason, state string, purged, fenced bool) {
	if item.DocumentID != "" {
		doc, err := tx.Document(item.DocumentID)
		if errors.Is(err, domain.ErrNotFound) || (err == nil && doc.Lifecycle == "purged") {
			return "document_purged", "skipped", true, true
		}
		if err == nil && doc.Lifecycle == "trash" {
			return "document_trashed", "skipped", false, true
		}
	}
	if item.VersionID != "" {
		if tomb, err := tx.TombstonedVersion(item.VersionID); err == nil && tomb {
			return "document_purged", "skipped", true, true
		}
	}
	pScope, err := tx.Scope(item.ProjectID)
	if err != nil {
		return "project_unavailable", "blocked", false, true
	}
	if pScope.Project.Lifecycle == "trash" || pScope.Project.Lifecycle == "purged" {
		return "document_trashed", "skipped", pScope.Project.Lifecycle == "purged", true
	}
	if capturedPerm, ok := run.PermissionsRevisions[item.ProjectID]; !ok || capturedPerm != pScope.Project.PermissionsRevision {
		return "permission_changed", "blocked", false, true
	}
	if run.AuthKind != "owner" {
		cfg, err := tx.Configuration()
		if err != nil || !projectPostWorkScanEnabled(cfg.Value, item.ProjectID) {
			return "authority_revoked", "blocked", false, true
		}
		wp, err := tx.WorkflowPrincipal(run.PrincipalID)
		if err != nil || !wp.Active {
			return "authority_revoked", "blocked", false, true
		}
		effScope := auth.RestrictScope(run.CapturedScope, wp.Scope)
		if !containsScanAction(effScope.Actions, "scan.request") || !containsScanAction(effScope.ProjectIDs, item.ProjectID) {
			return "authority_revoked", "blocked", false, true
		}
		if item.VersionID != "" {
			authState, _ := tx.AuthState()
			actor := domain.Session{
				PrincipalID: run.PrincipalID,
				AuthEpoch:   authState.AuthEpoch,
				Scope:       effScope,
			}
			if _, _, err := app.CheckSourceVersion(tx, actor, item.VersionID); err != nil {
				return "authority_revoked", "blocked", false, true
			}
		}
	}
	return "", "", false, false
}

func projectPostWorkScanEnabled(cfgJSON []byte, projectID string) bool {
	var parsed struct {
		Projects map[string]struct {
			PostWorkScanEnabled bool `json:"post_work_scan_enabled"`
		} `json:"projects"`
	}
	if json.Unmarshal(cfgJSON, &parsed) != nil {
		return false
	}
	return parsed.Projects[projectID].PostWorkScanEnabled
}

func (w *Worker) cancelSingleRepairChild(tx app.WriteTx, childJobID, reason, nowStr string) error {
	child, err := tx.Job(childJobID)
	if err != nil {
		return nil
	}
	if child.State != "completed" && child.State != "failed" && child.State != "cancelled" {
		if !child.CancelRequested {
			child, err = tx.CancelJob(child.ID)
			if err != nil {
				return err
			}
		}
		if child.State == "queued" || child.State == "paused" || child.State == "interrupted" || child.State == "running" || child.State == "cancelling" {
			_ = tx.ConfirmStop(child.ID, child.AttemptGeneration)
		}
	}
	return nil
}

func (w *Worker) completeItem(ctx context.Context, j domain.Job, item domain.JobItem, state, reason string, detail []byte) error {
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
		curItem, err := tx.JobItem(j.ID, item.ItemID)
		if err != nil {
			return err
		}
		if curItem.State != "running" && curItem.State != "pending" {
			// Already fenced by concurrent lifecycle/purge operation.
			return nil
		}
		run, err := tx.ScanRun(j.ID)
		if err != nil {
			return err
		}
		nowStr := w.now().Format(time.RFC3339Nano)
		if fReason, fState, purged, fenced := w.checkItemFence(tx, j, run, curItem); fenced {
			curItem.State = fState
			curItem.Reason = fReason
			curItem.Stage = "complete"
			if purged {
				curItem.PagePath = ""
				curItem.RegionID = ""
				curItem.Detail = []byte(`{"purged":true}`)
			}
			curItem.UpdatedAt = nowStr
			if err := tx.UpdateJobItem(curItem); err != nil {
				return err
			}
			run.UpdatedAt = nowStr
			return tx.UpdateScanRun(run)
		}
		curItem.State = state
		curItem.Reason = reason
		curItem.Stage = "complete"
		if len(detail) > 0 {
			curItem.Detail = detail
		}
		curItem.UpdatedAt = nowStr
		if err := tx.UpdateJobItem(curItem); err != nil {
			return err
		}
		if item.UnitKind == "entry" && item.VersionID != "" {
			if cv, e := tx.ScanCapturedVersionByID(j.ID, item.VersionID); e == nil {
				cv.EntriesChecked++
				cv.EntryAfterPath = item.PagePath
				_ = tx.UpdateScanCapturedVersion(cv)
			}
		}
		run.UpdatedAt = nowStr
		return tx.UpdateScanRun(run)
	})
}

func (w *Worker) inspectEntryItem(ctx context.Context, j domain.Job, run domain.ScanRun, item domain.JobItem) error {
	var entry domain.Entry
	var obj domain.Object
	var checkFailedReason string

	err := w.Store.Write(ctx, func(tx app.WriteTx) error {
		cv, err := tx.ScanCapturedVersionByID(j.ID, item.VersionID)
		if err != nil {
			checkFailedReason = "version_missing"
			return nil
		}
		entry, err = tx.QueryEntry(cv.ContentSetID, item.PagePath)
		if err != nil {
			checkFailedReason = "entry_missing"
			return nil
		}
		if _, err := domain.InventoryLine(entry); err != nil {
			checkFailedReason = "content_integrity_failed"
			return nil
		}
		switch entry.Kind {
		case "directory":
			if entry.Hash != "" || entry.LinkText != "" || entry.Size != 0 {
				checkFailedReason = "content_integrity_failed"
			}
			return nil
		case "symlink":
			if entry.Hash != "" || entry.Size != 0 {
				checkFailedReason = "content_integrity_failed"
			}
			return nil
		case "file":
			if entry.Hash == "" || entry.LinkText != "" {
				checkFailedReason = "content_integrity_failed"
				return nil
			}
			obj, err = tx.Object(entry.Hash)
			if err != nil || obj.State != "ready" || obj.Size != entry.Size {
				checkFailedReason = "content_integrity_failed"
				return nil
			}
			return nil
		default:
			checkFailedReason = "content_integrity_failed"
			return nil
		}
	})
	if err != nil {
		return err
	}
	if checkFailedReason != "" {
		detail, _ := json.Marshal(map[string]any{"error": checkFailedReason})
		return w.completeItem(ctx, j, item, "failed", "content_integrity_failed", detail)
	}
	if entry.Kind == "directory" {
		detail, _ := json.Marshal(map[string]any{"kind": "directory", "verified": true})
		return w.completeItem(ctx, j, item, "completed", "ok", detail)
	}
	if entry.Kind == "symlink" {
		detail, _ := json.Marshal(map[string]any{
			"kind":      "symlink",
			"link_text": entry.LinkText,
			"verified":  true,
		})
		return w.completeItem(ctx, j, item, "completed", "ok", detail)
	}
	if w.Bytes != nil {
		var actualHash string
		var actualSize int64
		readErr := w.Bytes.Read(ctx, obj, domain.Pin{Kind: "scan_read", OwnerID: j.ID}, func(r io.ReadSeeker) error {
			h := sha256.New()
			n, copyErr := io.Copy(h, r)
			if copyErr != nil {
				return copyErr
			}
			actualSize = n
			actualHash = hex.EncodeToString(h.Sum(nil))
			return nil
		})
		if readErr != nil || actualSize != entry.Size || actualHash != entry.Hash {
			detail, _ := json.Marshal(map[string]any{
				"expected_hash": entry.Hash,
				"actual_hash":   actualHash,
				"expected_size": entry.Size,
				"actual_size":   actualSize,
			})
			return w.completeItem(ctx, j, item, "failed", "content_integrity_failed", detail)
		}
	}
	detail, _ := json.Marshal(map[string]any{
		"hash":     entry.Hash,
		"size":     entry.Size,
		"kind":     entry.Kind,
		"verified": true,
	})
	return w.completeItem(ctx, j, item, "completed", "ok", detail)
}

func (w *Worker) inspectVersionItem(ctx context.Context, j domain.Job, run domain.ScanRun, item domain.JobItem) error {
	var v domain.Version
	var cv domain.ScanCapturedVersion
	var manifestObj domain.Object
	var metaObj domain.Object
	var hasMeta bool
	var searchState string
	var checkFailed string

	err := w.Store.Write(ctx, func(tx app.WriteTx) error {
		var err error
		cv, err = tx.ScanCapturedVersionByID(j.ID, item.VersionID)
		if err != nil {
			checkFailed = "version_missing"
			return nil
		}
		v, err = tx.Version(item.VersionID)
		if err != nil {
			checkFailed = "version_missing"
			return nil
		}
		manifestObj, err = tx.Object(v.ManifestSHA)
		if err != nil || manifestObj.State != "ready" {
			checkFailed = "manifest_object_missing"
			return nil
		}
		if v.MetadataHash != "" {
			hasMeta = true
			metaObj, err = tx.Object(v.MetadataHash)
			if err != nil || metaObj.State != "ready" {
				checkFailed = "metadata_object_missing"
				return nil
			}
		}
		idx, err := tx.SearchIndex(item.VersionID)
		if errors.Is(err, domain.ErrNotFound) {
			searchState = "missing"
		} else if err != nil {
			return err
		} else if idx.State != "complete" || !idx.DiscoveryComplete || idx.ExtractorRevision != domain.SearchExtractorRevision {
			searchState = "incomplete"
		} else {
			searchState = "complete"
		}
		return nil
	})
	if err != nil {
		return err
	}
	if checkFailed != "" {
		detail, _ := json.Marshal(map[string]any{"error": checkFailed})
		return w.completeItem(ctx, j, item, "failed", "content_integrity_failed", detail)
	}
	if w.Bytes != nil {
		if err := w.verifyObjectBytes(ctx, j.ID, manifestObj); err != nil {
			detail, _ := json.Marshal(map[string]any{"error": "manifest_integrity_failed"})
			return w.completeItem(ctx, j, item, "failed", "content_integrity_failed", detail)
		}
		if hasMeta {
			if err := w.verifyObjectBytes(ctx, j.ID, metaObj); err != nil {
				detail, _ := json.Marshal(map[string]any{"error": "metadata_integrity_failed"})
				return w.completeItem(ctx, j, item, "failed", "content_integrity_failed", detail)
			}
		}
	}
	// Ordinary imports without supplied metadata or without regions remain completely valid (R07 / C06).
	detail, _ := json.Marshal(map[string]any{
		"manifest_sha256": v.ManifestSHA,
		"regionless":      cv.RegionCount == 0,
		"metadata_less":   !hasMeta,
		"search_index":    searchState,
		"page_count":      cv.PageCount,
		"region_count":    cv.RegionCount,
		"entry_count":     cv.EntryCount,
		"source_count":    cv.SourceCount,
	})
	return w.completeItem(ctx, j, item, "completed", "ok", detail)
}

func (w *Worker) verifyObjectBytes(ctx context.Context, jobID string, obj domain.Object) error {
	var actualHash string
	var actualSize int64
	err := w.Bytes.Read(ctx, obj, domain.Pin{Kind: "scan_read", OwnerID: jobID}, func(r io.ReadSeeker) error {
		h := sha256.New()
		n, copyErr := io.Copy(h, r)
		if copyErr != nil {
			return copyErr
		}
		actualSize = n
		actualHash = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	if err != nil {
		return err
	}
	if actualSize != obj.Size || actualHash != obj.Hash {
		return domain.ErrContentIntegrity
	}
	return nil
}

func (w *Worker) inspectPageItem(ctx context.Context, j domain.Job, run domain.ScanRun, item domain.JobItem) error {
	var failedReason string
	err := w.Store.Write(ctx, func(tx app.WriteTx) error {
		cv, err := tx.ScanCapturedVersionByID(j.ID, item.VersionID)
		if err != nil {
			failedReason = "version_missing"
			return nil
		}
		if _, err := tx.Page(item.VersionID, item.PagePath); err != nil {
			failedReason = "page_missing"
			return nil
		}
		entry, err := tx.QueryEntry(cv.ContentSetID, item.PagePath)
		if err != nil {
			failedReason = "page_entry_missing"
			return nil
		}
		obj, err := tx.Object(entry.Hash)
		if err != nil || obj.State != "ready" || obj.Size != entry.Size {
			failedReason = "content_integrity_failed"
			return nil
		}
		return nil
	})
	if err != nil {
		return err
	}
	if failedReason != "" {
		detail, _ := json.Marshal(map[string]any{"error": failedReason})
		return w.completeItem(ctx, j, item, "failed", failedReason, detail)
	}
	detail, _ := json.Marshal(map[string]any{
		"page_path": item.PagePath,
		"eligible":  true,
	})
	return w.completeItem(ctx, j, item, "completed", "ok", detail)
}

func (w *Worker) inspectRegionItem(ctx context.Context, j domain.Job, run domain.ScanRun, item domain.JobItem) error {
	var failedReason string
	var anchorStatus string
	var evidenceCount int
	err := w.Store.Write(ctx, func(tx app.WriteTx) error {
		_, err := tx.VersionRegion(item.VersionID, item.PagePath, item.RegionID)
		if err != nil {
			failedReason = "region_missing"
			return nil
		}
		anchor, err := tx.PageAnchor(item.VersionID, item.PagePath, item.RegionID)
		if errors.Is(err, domain.ErrNotFound) {
			anchorStatus = "anchor_missing"
		} else if err != nil {
			return err
		} else if anchor.Occurrences > 1 {
			anchorStatus = "anchor_ambiguous"
		} else {
			anchorStatus = "resolved"
		}
		links, err := tx.VersionRegionEvidence(item.VersionID)
		if err != nil {
			return err
		}
		for _, link := range links {
			if link.PagePath == item.PagePath && link.RegionID == item.RegionID {
				evidenceCount++
				ev, err := tx.QueryEvidence(item.VersionID, link.EvidenceID)
				if err != nil {
					failedReason = "evidence_missing"
					return nil
				}
				if _, err := tx.QuerySource(item.VersionID, ev.SourceID); err != nil {
					failedReason = "source_missing"
					return nil
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if failedReason != "" {
		detail, _ := json.Marshal(map[string]any{"error": failedReason})
		return w.completeItem(ctx, j, item, "failed", failedReason, detail)
	}
	detail, _ := json.Marshal(map[string]any{
		"page_path":      item.PagePath,
		"region_id":      item.RegionID,
		"anchor_status":  anchorStatus,
		"evidence_count": evidenceCount,
		"eligible":       true,
	})
	return w.completeItem(ctx, j, item, "completed", "ok", detail)
}

func (w *Worker) inspectSourceItem(ctx context.Context, j domain.Job, run domain.ScanRun, item domain.JobItem) error {
	var itemState, itemReason string
	var detail []byte

	err := w.Store.Write(ctx, func(tx app.WriteTx) error {
		cv, err := tx.ScanCapturedVersionByID(j.ID, item.VersionID)
		if err != nil {
			itemState = "failed"
			itemReason = "version_missing"
			return nil
		}
		targets := cv.Targets
		if len(targets) == 0 && item.TargetSetID != "" {
			if ts, e := tx.TargetSetByID(item.TargetSetID); e == nil {
				var wrapped struct {
					Sources []domain.ScanSourceTarget `json:"sources"`
				}
				if json.Unmarshal(ts.TargetsJSON, &wrapped) == nil && wrapped.Sources != nil {
					targets = wrapped.Sources
				} else {
					_ = json.Unmarshal(ts.TargetsJSON, &targets)
				}
			}
		}
		if len(targets) == 0 {
			itemState = "skipped"
			itemReason = "no_source_declared"
			detail, _ = json.Marshal(map[string]any{"source_basis": cv.SourceBasis, "status": "unverified"})
			return nil
		}
		var matched *domain.ScanSourceTarget
		for i := range targets {
			if targets[i].SourceID == item.SourceID || (item.SourceID == "" && i == 0) {
				matched = &targets[i]
				break
			}
		}
		if matched == nil {
			matched = &targets[0]
		}
		if matched.Availability == "unverified" || matched.Reason == "no_source_declared" {
			itemState = "skipped"
			itemReason = "no_source_declared"
			detail, _ = json.Marshal(map[string]any{"source_basis": cv.SourceBasis, "status": "unverified"})
			return nil
		}
		if matched.Availability == "denied" || matched.Availability == "unavailable" {
			itemState = "blocked"
			itemReason = matched.Reason
			if itemReason == "" {
				itemReason = "source_unavailable"
			}
			detail, _ = json.Marshal(matched)
			return nil
		}
		// If selection used current live mappings, verify the project source mapping is still approved/present.
		if matched.Mode == "current" {
			mappings, mErr := source.Mappings(tx, item.ProjectID)
			if mErr != nil {
				itemState = "blocked"
				itemReason = "source_unavailable"
				return nil
			}
			stillApproved := false
			for _, m := range mappings {
				if m.SourceID == matched.SourceID {
					stillApproved = true
					break
				}
			}
			if !stillApproved && len(mappings) == 0 {
				itemState = "blocked"
				itemReason = "source_not_registered"
				detail, _ = json.Marshal(matched)
				return nil
			}
		}

		itemState = "completed"
		itemReason = "ok"
		detail, _ = json.Marshal(map[string]any{
			"source_id":             matched.SourceID,
			"kind":                  matched.Kind,
			"selection_mode":        matched.Mode,
			"selection_source":      matched.SelectionSource,
			"resolved_revision":     matched.ResolvedRevision,
			"snapshot_sha256":       matched.SnapshotSHA256,
			"status":                matched.Availability,
			"reassessment_required": matched.ReassessmentNeeded,
		})
		return nil
	})
	if err != nil {
		return err
	}
	return w.completeItem(ctx, j, item, itemState, itemReason, detail)
}

type referenceValidationResult struct {
	State      string            `json:"state"`
	Counts     map[string]string `json:"counts"`
	Reasons    []string          `json:"reasons"`
	Definition string            `json:"definition,omitempty"`
}

func (w *Worker) validateVersionReferences(ctx context.Context, jobID, versionID string) (referenceValidationResult, error) {
	out := referenceValidationResult{
		State:   "consistent",
		Counts:  map[string]string{"checked": "0", "missing": "0", "unexpected": "0", "mismatched": "0"},
		Reasons: []string{},
	}
	var v domain.Version
	var m domain.ReferenceMaterialization
	var hasMat bool
	var definition string
	var manifestObj domain.Object

	err := w.Store.Write(ctx, func(tx app.WriteTx) error {
		var err error
		definition, err = tx.ReferenceIndexDefinition()
		if err != nil {
			return err
		}
		if definition != "" {
			return nil
		}
		v, err = tx.Version(versionID)
		if err != nil {
			return err
		}
		m, err = tx.ReferenceMaterialization(versionID)
		if errors.Is(err, domain.ErrNotFound) {
			hasMat = false
		} else if err != nil {
			return err
		} else {
			hasMat = true
		}
		manifestObj, err = tx.Object(v.ManifestSHA)
		return err
	})
	if err != nil {
		return out, err
	}
	if definition != "" {
		out.Definition = definition
		out.State = "inconsistent"
		out.Reasons = append(out.Reasons, definition)
		return out, nil
	}

	var checked, missing, unexpected, mismatched int64
	incomplete := false
	if !hasMat {
		incomplete = true
		out.Reasons = append(out.Reasons, "materialization_pending")
	} else if m.State != "ready" || m.ReferenceRevision != domain.ReferenceRevision || m.ManifestSHA != v.ManifestSHA {
		incomplete = true
		out.Reasons = append(out.Reasons, "materialization_incomplete")
	}

	expected := map[string]bool{}
	if w.Bytes != nil {
		err = w.Bytes.Read(ctx, manifestObj, domain.Pin{Kind: "scan_read", OwnerID: jobID}, func(reader io.ReadSeeker) error {
			return references.WalkDeclarations(ctx, reader, versionID, func(prepared app.PreparedReference) error {
				r := prepared.Columns()
				expected[r.ID] = true
				return w.Store.Write(ctx, func(tx app.WriteTx) error {
					actual, e := tx.AuthoredReference(versionID, r.ID)
					checked++
					if errors.Is(e, domain.ErrNotFound) {
						missing++
						return nil
					}
					if e != nil {
						return e
					}
					raw := string(actual.AuthoredJSON)
					actual.AuthoredJSON = nil
					if !reflect.DeepEqual(actual, r) || raw != prepared.JSON() {
						mismatched++
					}
					return nil
				})
			})
		})
		if err != nil {
			return out, err
		}
	}

	err = w.Store.Write(ctx, func(tx app.WriteTx) error {
		after := ""
		for {
			rows, e := tx.AuthoredReferences(versionID, after, 64)
			if e != nil {
				return e
			}
			if len(rows) == 0 {
				break
			}
			for _, r := range rows {
				after = r.ID
				if w.Bytes != nil && !expected[r.ID] {
					unexpected++
					continue
				}
				checked++
			}
		}

		var rowID int64
		for {
			keys, e := tx.CanonicalEdgeKeys(rowID, 64)
			if e != nil {
				return e
			}
			if len(keys) == 0 {
				break
			}
			for _, k := range keys {
				rowID = k.RowID
				if k.TargetVersion != versionID && k.SourceVersion != versionID {
					continue
				}
				checked++
				found, e := tx.HasIndexedEdge(k)
				if e != nil {
					return e
				}
				if !found {
					missing++
				}
			}
		}

		var position *domain.EdgeKey
		for {
			keys, e := tx.IncomingEdgeKeys(versionID, nil, nil, position, 64)
			if e != nil {
				return e
			}
			if len(keys) == 0 {
				break
			}
			for _, k := range keys {
				pos := k
				position = &pos
				checked++
				canonical, e := tx.CanonicalEdgeKey(k.RowID)
				if errors.Is(e, domain.ErrNotFound) || errors.Is(e, domain.ErrContentIntegrity) {
					mismatched++
					continue
				}
				if e != nil {
					return e
				}
				if !reflect.DeepEqual(canonical, k) {
					mismatched++
				}
			}
		}
		return nil
	})
	if err != nil {
		return out, err
	}

	out.Counts = map[string]string{
		"checked":    strconv.FormatInt(checked, 10),
		"missing":    strconv.FormatInt(missing, 10),
		"unexpected": strconv.FormatInt(unexpected, 10),
		"mismatched": strconv.FormatInt(mismatched, 10),
	}
	if incomplete {
		out.State = "incomplete"
	}
	if missing+unexpected+mismatched > 0 {
		out.State = "inconsistent"
		out.Reasons = append(out.Reasons, "reference_index_divergence")
	}
	return out, nil
}

func (w *Worker) inspectOrRepairIndexItem(ctx context.Context, j domain.Job, run domain.ScanRun, item domain.JobItem) error {
	if item.RepairJobID != "" {
		return w.observeOrStepRepairChild(ctx, j, run, item)
	}

	val, err := w.validateVersionReferences(ctx, j.ID, item.VersionID)
	if err != nil {
		detail, _ := json.Marshal(map[string]any{"error": err.Error()})
		return w.completeItem(ctx, j, item, "failed", "index_validation_failed", detail)
	}
	if val.Definition != "" {
		// Global SQLite DDL drift is never repaired by a scan.
		detail, _ := json.Marshal(val)
		return w.completeItem(ctx, j, item, "failed", val.Definition, detail)
	}
	if val.State == "consistent" {
		detail, _ := json.Marshal(val)
		return w.completeItem(ctx, j, item, "completed", "ok", detail)
	}

	// Reference index divergence detected; evaluate stricter of captured and current policy.
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
		curRun, err := tx.ScanRun(j.ID)
		if err != nil {
			return err
		}
		curItem, err := tx.JobItem(j.ID, item.ItemID)
		if err != nil {
			return err
		}
		if curItem.State != "running" && curItem.State != "pending" {
			return nil
		}
		now := w.now()
		nowStr := now.Format(time.RFC3339Nano)
		if elapsedMs, exhausted := w.currentElapsedAndBudget(tx, curRun, now); exhausted {
			return w.exhaustRunBudget(tx, curJob, curRun, elapsedMs, now)
		}
		if fReason, fState, purged, fenced := w.checkItemFence(tx, j, curRun, curItem); fenced {
			curItem.State = fState
			curItem.Reason = fReason
			curItem.Stage = "complete"
			if purged {
				curItem.PagePath = ""
				curItem.RegionID = ""
				curItem.Detail = []byte(`{"purged":true}`)
			}
			curItem.UpdatedAt = nowStr
			if err := tx.UpdateJobItem(curItem); err != nil {
				return err
			}
			curRun.UpdatedAt = nowStr
			return tx.UpdateScanRun(curRun)
		}

		currentMode, err := w.currentBacklinkRepairMode(tx, item.ProjectID)
		if err != nil {
			return err
		}
		effectiveMode := StricterAutoApplyMode(curRun.EffectivePolicy.AutoApply.BacklinkIndexRepair, currentMode)
		fp := ComputeEvidenceFingerprint("backlink_index_repair", item.VersionID, map[string]any{
			"state":   val.State,
			"counts":  val.Counts,
			"reasons": val.Reasons,
		})

		switch effectiveMode {
		case "disabled":
			detail, _ := json.Marshal(map[string]any{
				"state":          val.State,
				"counts":         val.Counts,
				"reasons":        val.Reasons,
				"repair_policy":  "disabled",
				"repair_applied": false,
			})
			curItem.State = "completed"
			curItem.Reason = "reference_index_divergence"
			curItem.Stage = "complete"
			curItem.Detail = detail
			curItem.UpdatedAt = nowStr
			if err := tx.UpdateJobItem(curItem); err != nil {
				return err
			}
			curRun.UpdatedAt = nowStr
			return tx.UpdateScanRun(curRun)

		case "propose":
			f, err := w.upsertBacklinkFinding(tx, curRun, curItem, val, fp, "proposed", true, "reference_index_divergence", nil, nil, nil, nowStr)
			if err != nil {
				return err
			}
			detail, _ := json.Marshal(map[string]any{
				"state":          val.State,
				"counts":         val.Counts,
				"reasons":        val.Reasons,
				"repair_policy":  "propose",
				"repair_applied": false,
				"finding_id":     f.ID,
			})
			curItem.State = "completed"
			curItem.Reason = "repair_proposed"
			curItem.Stage = "complete"
			curItem.FindingID = f.ID
			curItem.Detail = detail
			curItem.UpdatedAt = nowStr
			if err := tx.UpdateJobItem(curItem); err != nil {
				return err
			}
			curRun.UpdatedAt = nowStr
			return tx.UpdateScanRun(curRun)

		default: // "auto"
			// Check if an unrelated active derived_index references job exists for this version.
			if _, hasActive, err := tx.ActiveDerivedWorkJob(item.VersionID, "references"); err != nil {
				return err
			} else if hasActive {
				// Yield turn to let the existing derived_index job finish on the serial dispatcher.
				curItem.State = "pending"
				curItem.Stage = "repair"
				curItem.UpdatedAt = nowStr
				return tx.UpdateJobItem(curItem)
			}

			childID, err := tx.RebuildScanReferences(j.ID, curItem.ItemID, curItem.VersionID)
			if errors.Is(err, domain.ErrSearchRestricted) || errors.Is(err, domain.ErrStaleWorker) {
				curItem.State = "blocked"
				curItem.Reason = "authority_revoked"
				curItem.Stage = "complete"
				curItem.UpdatedAt = nowStr
				if err := tx.UpdateJobItem(curItem); err != nil {
					return err
				}
				curRun.UpdatedAt = nowStr
				return tx.UpdateScanRun(curRun)
			}
			if err != nil {
				return err
			}
			curItem.State = "pending"
			curItem.Stage = "repair"
			curItem.RepairJobID = childID
			curItem.AttemptGeneration = curJob.AttemptGeneration
			curItem.UpdatedAt = nowStr
			if err := tx.UpdateJobItem(curItem); err != nil {
				return err
			}
			curRun.Stage = "repair"
			curRun.UpdatedAt = nowStr
			return tx.UpdateScanRun(curRun)
		}
	})
}

func (w *Worker) observeOrStepRepairChild(ctx context.Context, j domain.Job, run domain.ScanRun, item domain.JobItem) error {
	var child domain.Job
	var childTerminal bool
	var settled bool

	err := w.Store.Write(ctx, func(tx app.WriteTx) error {
		curJob, err := tx.Job(j.ID)
		if err != nil {
			return err
		}
		if curJob.CancelRequested {
			settled = true
			return tx.ConfirmStop(j.ID, curJob.AttemptGeneration)
		}
		if curJob.State != "running" || curJob.AttemptGeneration != j.AttemptGeneration {
			return domain.ErrStaleWorker
		}
		curRun, err := tx.ScanRun(j.ID)
		if err != nil {
			return err
		}
		curItem, err := tx.JobItem(j.ID, item.ItemID)
		if err != nil {
			return err
		}
		if curItem.State != "pending" && curItem.State != "running" {
			settled = true
			return nil
		}
		now := w.now()
		nowStr := now.Format(time.RFC3339Nano)
		if elapsedMs, exhausted := w.currentElapsedAndBudget(tx, curRun, now); exhausted {
			settled = true
			return w.exhaustRunBudget(tx, curJob, curRun, elapsedMs, now)
		}

		// Check current policy for mid-flight policy narrowing (e.g., auto -> propose or auto -> disabled).
		currentMode, err := w.currentBacklinkRepairMode(tx, curItem.ProjectID)
		if err != nil {
			return err
		}
		effectiveMode := StricterAutoApplyMode(curRun.EffectivePolicy.AutoApply.BacklinkIndexRepair, currentMode)

		child, err = tx.Job(curItem.RepairJobID)
		if err != nil {
			return err
		}
		childTerminal = child.State == "completed" || child.State == "failed" || child.State == "cancelled"

		if effectiveMode != "auto" {
			// Policy narrowed while child was queued/running/completed: cancel if non-terminal and do not auto-apply!
			settled = true
			if !childTerminal {
				_ = w.cancelSingleRepairChild(tx, child.ID, "policy_narrowed", nowStr)
			}
			val := referenceValidationResult{
				State:   "inconsistent",
				Counts:  map[string]string{"checked": "1", "missing": "1", "unexpected": "0", "mismatched": "0"},
				Reasons: []string{"reference_index_divergence"},
			}
			fp := ComputeEvidenceFingerprint("backlink_index_repair", curItem.VersionID, map[string]any{
				"state":   val.State,
				"reasons": val.Reasons,
			})
			if effectiveMode == "propose" {
				f, err := w.upsertBacklinkFinding(tx, curRun, curItem, val, fp, "proposed", true, "reference_index_divergence", nil, nil, nil, nowStr)
				if err != nil {
					return err
				}
				curItem.FindingID = f.ID
				curItem.State = "completed"
				curItem.Reason = "repair_proposed"
			} else {
				curItem.State = "completed"
				curItem.Reason = "reference_index_divergence"
			}
			curItem.Stage = "complete"
			curItem.UpdatedAt = nowStr
			if err := tx.UpdateJobItem(curItem); err != nil {
				return err
			}
			curRun.Stage = "inspect"
			curRun.UpdatedAt = nowStr
			return tx.UpdateScanRun(curRun)
		}

		if !childTerminal {
			// If child was paused because referenceAuthority detected a restriction, cancel child and mark item blocked.
			if child.State == "paused" {
				settled = true
				_ = w.cancelSingleRepairChild(tx, child.ID, "repair_restricted", nowStr)
				val := referenceValidationResult{State: "inconsistent", Reasons: []string{"repair_restricted"}}
				fp := ComputeEvidenceFingerprint("backlink_index_repair", curItem.VersionID, val)
				f, err := w.upsertBacklinkFinding(tx, curRun, curItem, val, fp, "blocked", true, "repair_restricted", nil, nil, nil, nowStr)
				if err != nil {
					return err
				}
				curItem.FindingID = f.ID
				curItem.State = "blocked"
				curItem.Reason = "repair_restricted"
				curItem.Stage = "complete"
				curItem.UpdatedAt = nowStr
				if err := tx.UpdateJobItem(curItem); err != nil {
					return err
				}
				curRun.Stage = "inspect"
				curRun.UpdatedAt = nowStr
				return tx.UpdateScanRun(curRun)
			}
			return nil
		}
		return nil
	})
	if err != nil || settled {
		return err
	}

	if !childTerminal {
		return nil
	}

	// Child is terminal; verify outcome and re-validate reference index before recording 'applied'.
	var reval referenceValidationResult
	if child.State == "completed" {
		reval, err = w.validateVersionReferences(ctx, j.ID, item.VersionID)
		if err != nil {
			return err
		}
	}

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
		curRun, err := tx.ScanRun(j.ID)
		if err != nil {
			return err
		}
		curItem, err := tx.JobItem(j.ID, item.ItemID)
		if err != nil {
			return err
		}
		if curItem.State != "pending" && curItem.State != "running" {
			return nil
		}
		now := w.now()
		nowStr := now.Format(time.RFC3339Nano)
		if elapsedMs, exhausted := w.currentElapsedAndBudget(tx, curRun, now); exhausted {
			return w.exhaustRunBudget(tx, curJob, curRun, elapsedMs, now)
		}
		if fReason, fState, purged, fenced := w.checkItemFence(tx, j, curRun, curItem); fenced {
			curItem.State = fState
			curItem.Reason = fReason
			curItem.Stage = "complete"
			if purged {
				curItem.PagePath = ""
				curItem.RegionID = ""
				curItem.Detail = []byte(`{"purged":true}`)
			}
			curItem.UpdatedAt = nowStr
			if err := tx.UpdateJobItem(curItem); err != nil {
				return err
			}
			curRun.Stage = "inspect"
			curRun.UpdatedAt = nowStr
			return tx.UpdateScanRun(curRun)
		}

		currentMode, err := w.currentBacklinkRepairMode(tx, curItem.ProjectID)
		if err != nil {
			return err
		}
		effectiveMode := StricterAutoApplyMode(curRun.EffectivePolicy.AutoApply.BacklinkIndexRepair, currentMode)

		fp := ComputeEvidenceFingerprint("backlink_index_repair", curItem.VersionID, map[string]any{
			"repair_job_id": child.ID,
			"version_id":    curItem.VersionID,
		})

		if child.State == "completed" && reval.State == "consistent" && effectiveMode == "auto" {
			decidedBy := "auto_policy"
			appliedRef, _ := json.Marshal(map[string]any{
				"kind":       "derived_index_job",
				"job_id":     child.ID,
				"version_id": curItem.VersionID,
			})
			f, err := w.upsertBacklinkFinding(tx, curRun, curItem, reval, fp, "applied", false, "repaired_auto", appliedRef, &decidedBy, &nowStr, nowStr)
			if err != nil {
				return err
			}
			detail, _ := json.Marshal(map[string]any{
				"state":          reval.State,
				"counts":         reval.Counts,
				"repair_policy":  "auto",
				"repair_applied": true,
				"repair_job_id":  child.ID,
				"finding_id":     f.ID,
			})
			curItem.State = "completed"
			curItem.Reason = "repaired_auto"
			curItem.Stage = "complete"
			curItem.FindingID = f.ID
			curItem.Detail = detail
			curItem.UpdatedAt = nowStr
			if err := tx.UpdateJobItem(curItem); err != nil {
				return err
			}
			curRun.Stage = "inspect"
			curRun.UpdatedAt = nowStr
			return tx.UpdateScanRun(curRun)
		}

		failReason := "repair_verification_failed"
		itemState := "failed"
		if child.State == "cancelled" {
			failReason = "repair_cancelled"
			itemState = "blocked"
		} else if child.State == "failed" && child.Error != "" {
			failReason = child.Error
		} else if effectiveMode != "auto" {
			failReason = "policy_narrowed"
			itemState = "blocked"
		}
		f, err := w.upsertBacklinkFinding(tx, curRun, curItem, reval, fp, "blocked", true, failReason, nil, nil, nil, nowStr)
		if err != nil {
			return err
		}
		detail, _ := json.Marshal(map[string]any{
			"state":          reval.State,
			"repair_applied": false,
			"repair_job_id":  child.ID,
			"finding_id":     f.ID,
			"reason":         failReason,
		})
		curItem.State = itemState
		curItem.Reason = failReason
		curItem.Stage = "complete"
		curItem.FindingID = f.ID
		curItem.Detail = detail
		curItem.UpdatedAt = nowStr
		if err := tx.UpdateJobItem(curItem); err != nil {
			return err
		}
		curRun.Stage = "inspect"
		curRun.UpdatedAt = nowStr
		return tx.UpdateScanRun(curRun)
	})
}

func (w *Worker) currentBacklinkRepairMode(tx app.WriteTx, projectID string) (string, error) {
	cfg, err := tx.Configuration()
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
		mode = StricterAutoApplyMode(mode, *p.Maintenance.AutoApply.BacklinkIndexRepair)
	}
	return mode, nil
}

func (w *Worker) upsertBacklinkFinding(
	tx app.WriteTx,
	run domain.ScanRun,
	item domain.JobItem,
	val referenceValidationResult,
	fp string,
	status string,
	approvalRequired bool,
	reason string,
	appliedRef []byte,
	decidedBy *string,
	decidedAt *string,
	nowStr string,
) (domain.Finding, error) {
	permRev := run.PermissionsRevisions[item.ProjectID]
	if permRev < 1 {
		permRev = 1
	}
	evBytes, _ := json.Marshal([]map[string]any{
		{
			"kind":       "reference_index_validation",
			"version_id": item.VersionID,
			"state":      val.State,
			"counts":     val.Counts,
			"reasons":    val.Reasons,
		},
	})
	propBytes, _ := json.Marshal(map[string]any{
		"action":     "rebuild_reference_index",
		"version_id": item.VersionID,
	})
	existing, exists, err := tx.FindingByFingerprint(run.JobID, "backlink_index_repair", fp)
	if err != nil {
		return domain.Finding{}, err
	}
	if exists {
		existing.Status = status
		existing.ApprovalRequired = approvalRequired
		existing.Reason = reason
		existing.Evidence = evBytes
		existing.Proposal = propBytes
		existing.AppliedRef = appliedRef
		existing.DecidedBy = decidedBy
		existing.DecidedAt = decidedAt
		existing.Revision++
		existing.UpdatedAt = nowStr
		existing.ETag = domain.FindingETag(existing)
		if err := tx.PutFinding(existing); err != nil {
			return domain.Finding{}, err
		}
		return existing, nil
	}
	var targetSetPtr *string
	if item.TargetSetID != "" {
		ts := item.TargetSetID
		targetSetPtr = &ts
	}
	f := domain.Finding{
		ID:                     fmt.Sprintf("fnd-%s-%06d", run.JobID, item.Ordinal),
		JobID:                  run.JobID,
		ProjectID:              item.ProjectID,
		DocumentID:             nullableStr(item.DocumentID),
		VersionID:              nullableStr(item.VersionID),
		Kind:                   "backlink_index_repair",
		Origin:                 "deterministic",
		TargetSetID:            targetSetPtr,
		Evidence:               evBytes,
		EvidenceFingerprint:    fp,
		Rationale:              "Deterministic reference and reverse-backlink index verification detected divergence from immutable manifest declarations.",
		Uncertainty:            "none",
		Proposal:               propBytes,
		Status:                 status,
		ApprovalRequired:       approvalRequired,
		Reason:                 reason,
		CapturedConfigRevision: run.ConfigRevision,
		PermissionsRevision:    permRev,
		AppliedRef:             appliedRef,
		DecidedBy:              decidedBy,
		DecidedAt:              decidedAt,
		Revision:               1,
		CreatedAt:              nowStr,
		UpdatedAt:              nowStr,
	}
	f.ETag = domain.FindingETag(f)
	if err := tx.PutFinding(f); err != nil {
		return domain.Finding{}, err
	}
	return f, nil
}
