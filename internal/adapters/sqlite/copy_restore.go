package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	app "haeryong.local/haeryong/internal/application/storage"
	"regexp"
	"strings"
	"time"

	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
)

const providerAttemptInventory = `SELECT json_object('attempt_id',a.attempt_id,'transport',json((SELECT value FROM provider_transport WHERE attempt_id=a.attempt_id)),'job_id',a.job_id,'principal_id',json_extract(j.scope,'$.principal_id'),'job_state',j.state,'target',json(json_extract(j.scope,'$.target')),'cancel_requested',json(CASE j.cancel_requested WHEN 1 THEN 'true' ELSE 'false' END),'request_sha256',a.request_sha256,'state',a.state,'may_have_dispatched',json(CASE a.may_have_dispatched WHEN 1 THEN 'true' ELSE 'false' END),'reason',a.reason,'attempt_generation',a.attempt_generation,'target_set_id',a.target_set_id,'provider_request_id',a.provider_request_id,'provider_session_id',a.provider_session_id,'result_hash',a.result_hash,'inputs',json((SELECT json_group_array(json_object('attempt_id',i.attempt_id,'read_id',i.read_id,'bytes_sha256',i.content_hash,'input_origin',i.input_origin,'observed_at',i.observed_at,'retention_mode',(SELECT r.retention_mode FROM source_capture_reads r WHERE r.read_id=i.read_id),'source_version_id',(SELECT json_extract(c.requested_selection,'$.version_id') FROM source_capture_reads r JOIN source_captures c ON c.capture_id=r.capture_id WHERE r.read_id=i.read_id),'source_version_ids',json((` + sourceReadDependencies + `SELECT json_group_array(version_id) FROM(SELECT version_id FROM source_read_contexts WHERE read_id=i.read_id ORDER BY version_id))))) FROM provider_inputs i WHERE i.attempt_id=a.attempt_id))) FROM provider_attempts a JOIN jobs j ON j.job_id=a.job_id`

// ProtectRestoredDatabase operates only on verified destination-local staging.
// Live startup remains strict; missing destination DDL is repaired by replacing
// the complete storage set, never by weakening its normal admission checks.
func ProtectRestoredDatabase(ctx context.Context, path string, p service.Profile, id string, current json.RawMessage, validate func(map[string]any) error) (domain.Library, error) {
	var lib domain.Library
	db, e := openCopy(path)
	if e != nil {
		return lib, e
	}
	defer db.Close()
	s := &Store{db: db, profile: p}
	if e = s.validateTables(ctx, SchemaRevision); e != nil {
		return lib, e
	}
	definition := regexp.MustCompile(`(?s)CREATE INDEX authored_edges_target.*?;`).FindString(migrations[8])
	if definition == "" {
		return lib, errors.New("canonical reverse index declaration unavailable")
	}
	if _, e = db.ExecContext(ctx, `DROP INDEX authored_edges_target;`+definition); e != nil {
		return lib, e
	}
	if e = s.validateTables(ctx, SchemaRevision); e != nil {
		return lib, e
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return lib, e
	}
	defer tx.Rollback()
	t := &writeTx{tx: tx, ctx: ctx}
	lib, e = t.Library()
	if e != nil {
		return lib, e
	}
	lib.ProfileID = p.ProfileID
	if lib.RuntimeGeneration, e = service.NewID(); e != nil {
		return lib, e
	}
	if lib.AuthEpoch, e = service.NewID(); e != nil {
		return lib, e
	}
	if lib.StreamEpoch, e = service.NewID(); e != nil {
		return lib, e
	}
	old, e := t.Configuration()
	if e != nil {
		return lib, e
	}
	var value map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(old.Value)))
	decoder.UseNumber()
	e = decoder.Decode(&value)
	if e != nil {
		return lib, e
	}
	network := value["network"].(map[string]any)
	listener := network["listener"].(map[string]any)
	network["remote_enabled"] = false
	network["external_base_url"] = nil
	listener["bind"] = "127.0.0.1"
	network["allowed_origins"] = []any{"http://127.0.0.1:" + fmt.Sprint(listener["port"])}
	value["retention"].(map[string]any)["auto_purge_enabled"] = false
	schedule := value["maintenance"].(map[string]any)["schedule"].(map[string]any)
	schedule["mode"] = "manual"
	schedule["cron"] = nil
	schedule["interval_seconds"] = nil
	if validate == nil {
		return lib, domain.ErrInvalidTarget
	}
	if e = validate(value); e != nil {
		return lib, e
	}
	safe, _ := json.Marshal(value)
	hold := domain.RecoveryHold{ID: id, State: "pending_review", OriginalConfiguration: old.Value, CurrentConfiguration: current}
	if e = t.exec(`INSERT INTO recovery_holds VALUES(1,?) ON CONFLICT(singleton) DO UPDATE SET value=excluded.value`, jsonBytes(hold)); e != nil {
		return lib, e
	}
	if e = t.exec(`INSERT INTO resource_revisions(resource_kind,resource_id,revision,value) SELECT 'restore_job_hold',job_id,1,'{}' FROM jobs WHERE state NOT IN ('completed','failed','cancelled') OR EXISTS(SELECT 1 FROM provider_reconcile_intents i WHERE i.job_id=jobs.job_id) ON CONFLICT(resource_kind,resource_id) DO NOTHING`); e != nil {
		return lib, e
	}
	if e = t.exec(`UPDATE jobs SET state='paused',attempt_generation=attempt_generation+1,revision=revision+1 WHERE state NOT IN ('completed','failed','cancelled')`); e != nil {
		return lib, e
	}
	if e = t.exec(`UPDATE job_items SET state='pending' WHERE state='running'`); e != nil {
		return lib, e
	}
	if e = pauseRestoredScanBudgets(t); e != nil {
		return lib, e
	}
	if e = t.exec(`UPDATE libraries SET profile_id=?,runtime_generation=?,auth_epoch=?,stream_epoch=?`, lib.ProfileID, lib.RuntimeGeneration, lib.AuthEpoch, lib.StreamEpoch); e != nil {
		return lib, e
	}
	for _, statement := range []string{`DELETE FROM device_sessions`, `DELETE FROM scoped_credentials`, `DELETE FROM pairings`} {
		if e = t.exec(statement); e != nil {
			return lib, e
		}
	}
	if e = t.exec(`UPDATE copy_sets SET stage='restored_paused',value=json_set(value,'$.Stage','restored_paused','$.Capturing',json('false')) WHERE stage NOT IN ('complete','abandoned')`); e != nil {
		return lib, e
	}
	// Use the existing typed configuration mutation rather than inventing a
	// second configuration authority or editing remembered project selections.
	if e = t.SetConfiguration(domain.Configuration{Revision: old.Revision + 1, Value: safe, Actor: "protected_restore"}, old.Revision); e != nil {
		return lib, e
	}
	if e = t.appendEvent("library", lib.ID, "library.invalidated", 1, nil); e != nil {
		return lib, e
	}
	if e = tx.Commit(); e != nil {
		return lib, e
	}
	_, e = db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return lib, e
}

func CopyCurrentConfiguration(ctx context.Context, path string) json.RawMessage {
	db, e := openCopy(path)
	if e != nil {
		return nil
	}
	defer db.Close()
	var raw []byte
	if db.QueryRowContext(ctx, `SELECT validated_json FROM config_revisions ORDER BY config_revision DESC LIMIT 1`).Scan(&raw) != nil {
		return nil
	}
	return raw
}

func RestoreOutstanding(ctx context.Context, path string, consume func(json.RawMessage) error) error {
	db, e := openCopy(path)
	if e != nil {
		return e
	}
	defer db.Close()
	rows, e := db.QueryContext(ctx, `SELECT json_object('principal_id',principal_id,'operation_id',operation_id,'state',state,'fingerprint',request_fingerprint) FROM operation_receipts WHERE state IN ('accepted','reconciling') UNION ALL SELECT json_object('job_id',job_id,'kind',kind,'state',state,'attempt_generation',attempt_generation,'cancel_requested',cancel_requested) FROM jobs WHERE state NOT IN ('completed','failed','cancelled') UNION ALL `+providerAttemptInventory+` WHERE a.state<>'prepared' UNION ALL SELECT value FROM provider_orphans`)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			return e
		}
		if e = consume(raw); e != nil {
			return e
		}
	}
	return rows.Err()
}

func (t *writeTx) ReviewRecovery(id string, jobs []string) error {
	hold, e := t.RecoveryHold()
	if e != nil {
		return e
	}
	if hold == nil || hold.ID != id {
		return domain.ErrNotFound
	}
	if hold.State == "reviewed" {
		return nil
	}
	hold.State = "reviewed"
	if e = t.exec(`UPDATE recovery_holds SET value=? WHERE singleton=1`, jsonBytes(hold)); e != nil {
		return e
	}
	for _, id := range jobs {
		j, e := t.Job(id)
		if e != nil {
			return e
		}
		if terminal(j.State) || j.CancelRequested {
			return domain.ErrTransition
		}
		switch j.Kind {
		case "lifecycle":
			intent, e := t.PurgeIntent(id)
			if e != nil {
				return e
			}
			lib, e := t.Library()
			if e != nil {
				return e
			}
			intent.Runtime = lib.RuntimeGeneration
			if e = t.purgeGuard(intent); e != nil {
				return e
			}
			if e = t.savePurge(intent); e != nil {
				return e
			}
			if e = t.exec(`DELETE FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?`, id); e != nil {
				return e
			}
		case "scan":
			if e = t.reviewRestoredScan(j); e != nil {
				return e
			}
		default:
			return domain.ErrTransition
		}
	}
	return t.exec(`UPDATE recovery_holds SET value=? WHERE singleton=1`, jsonBytes(hold))
}

func pauseRestoredScanBudgets(t *writeTx) error {
	now := time.Now().UTC()
	rows, err := t.tx.QueryContext(t.ctx, `SELECT job_id,started_at,elapsed_ms FROM scan_runs WHERE started_at IS NOT NULL AND started_at!=''`)
	if err != nil {
		return err
	}
	type active struct {
		id      string
		started string
		elapsed int64
	}
	list := []active{}
	for rows.Next() {
		var a active
		if err := rows.Scan(&a.id, &a.started, &a.elapsed); err != nil {
			rows.Close()
			return err
		}
		list = append(list, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range list {
		add := int64(0)
		if ts, e := time.Parse(time.RFC3339Nano, a.started); e == nil && now.After(ts) {
			add = now.Sub(ts).Milliseconds()
		}
		if err := t.exec(`UPDATE scan_runs SET started_at=NULL,elapsed_ms=?,updated_at=? WHERE job_id=?`, a.elapsed+add, now.Format(time.RFC3339Nano), a.id); err != nil {
			return err
		}
	}
	return nil
}

func (t *writeTx) reviewRestoredScan(j domain.Job) error {
	run, err := t.ScanRun(j.ID)
	if err != nil {
		return err
	}
	for _, pid := range run.RequestedScope.ProjectIDs {
		if _, err := t.Scope(pid); err != nil {
			return err
		}
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT item_id,project_id,document_id,version_id,repair_job_id FROM job_items WHERE job_id=? AND repair_job_id!='' ORDER BY ordinal`, j.ID)
	if err != nil {
		return err
	}
	type repairItem struct {
		itemID, projectID, documentID, versionID, childID string
	}
	items := []repairItem{}
	for rows.Next() {
		var ri repairItem
		if err := rows.Scan(&ri.itemID, &ri.projectID, &ri.documentID, &ri.versionID, &ri.childID); err != nil {
			rows.Close()
			return err
		}
		items = append(items, ri)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, ri := range items {
		var held bool
		if err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?)`, ri.childID).Scan(&held); err != nil {
			return err
		}
		child, childErr := t.Job(ri.childID)
		if childErr != nil && !errors.Is(childErr, domain.ErrNotFound) {
			return childErr
		}
		if held || (childErr == nil && !terminal(child.State)) {
			if err := t.exec(`DELETE FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?`, ri.childID); err != nil {
				return err
			}
			if childErr == nil && !terminal(child.State) {
				child, err = t.CancelJob(ri.childID)
				if err != nil {
					return err
				}
				if !terminal(child.State) {
					if err = t.ConfirmStop(ri.childID, child.AttemptGeneration); err != nil {
						return err
					}
				}
			}
			detail := jsonBytes(map[string]any{
				"repair_job_id":                        ri.childID,
				"reason":                               "restored_repair_cancelled",
				"incomplete_reference_materialization": true,
				"rescan_recommended":                   true,
			})
			if err := t.exec(`UPDATE job_items SET state='blocked',stage='complete',reason='restored_repair_cancelled',detail=?,updated_at=? WHERE job_id=? AND item_id=?`, detail, now, j.ID, ri.itemID); err != nil {
				return err
			}
			fp := "backlink_index_repair:" + ri.versionID + ":restored_repair_cancelled"
			if existing, ok, e := t.FindingByFingerprint(j.ID, "backlink_index_repair", fp); e != nil {
				return e
			} else if !ok {
				fid, e := service.NewID()
				if e != nil {
					return e
				}
				docID, verID := ri.documentID, ri.versionID
				permRev := run.PermissionsRevisions[ri.projectID]
				if permRev < 1 {
					permRev = 1
				}
				if e = t.PutFinding(domain.Finding{
					ID:                     fid,
					JobID:                  j.ID,
					Kind:                   "backlink_index_repair",
					Origin:                 "deterministic",
					ProjectID:              ri.projectID,
					DocumentID:             &docID,
					VersionID:              &verID,
					Evidence:               detail,
					EvidenceFingerprint:    fp,
					Rationale:              "Restored active reference index repair child was cancelled during protected recovery review; reference materialization remains incomplete until an explicit rebuild or rescan.",
					Uncertainty:            "none",
					CapturedConfigRevision: run.ConfigRevision,
					PermissionsRevision:    permRev,
					Proposal:               jsonBytes(map[string]any{"action": "rebuild_reference_index", "version_id": ri.versionID, "repair_job_id": ri.childID}),
					Status:                 "blocked",
					ApprovalRequired:       true,
					Reason:                 "restored_repair_cancelled",
					Revision:               1,
					CreatedAt:              now,
					UpdatedAt:              now,
				}); e != nil {
					return e
				}
				_ = t.exec(`UPDATE job_items SET finding_id=? WHERE job_id=? AND item_id=?`, fid, j.ID, ri.itemID)
			} else if existing.Status != "blocked" {
				existing.Status = "blocked"
				existing.ApprovalRequired = true
				existing.Reason = "restored_repair_cancelled"
				existing.AppliedRef = nil
				existing.Revision++
				existing.UpdatedAt = now
				if e := t.PutFinding(existing); e != nil {
					return e
				}
			}
			run.Report.RescanRecommended = true
			foundVer := false
			for _, v := range run.Report.IncompleteReferenceRepairs {
				if v == ri.versionID {
					foundVer = true
					break
				}
			}
			if !foundVer && ri.versionID != "" {
				run.Report.IncompleteReferenceRepairs = append(run.Report.IncompleteReferenceRepairs, ri.versionID)
			}
		}
	}
	run.UpdatedAt = now
	if err := t.UpdateScanRun(run); err != nil {
		return err
	}
	if err := t.exec(`DELETE FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?`, j.ID); err != nil {
		return err
	}
	if err := t.exec(`UPDATE jobs SET state='queued',attempt_generation=attempt_generation+1,revision=revision+1 WHERE job_id=?`, j.ID); err != nil {
		return err
	}
	return t.jobUpdated(j.ID)
}

// CheckRestoreOperationBindings reads only the current owner's available
// receipts. Absence is not a claim about receipts lost in an older snapshot.
func CheckRestoreOperationBindings(ctx context.Context, path, id string, keys []string) error {
	db, e := openCopy(path)
	if e != nil {
		return e
	}
	defer db.Close()
	var owner string
	e = db.QueryRowContext(ctx, `SELECT principal_id FROM auth_owner WHERE singleton=1`).Scan(&owner)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		r, e := receipt(ctx, db, domain.OperationKey{PrincipalID: owner, OperationID: key})
		if errors.Is(e, domain.ErrNotFound) {
			continue
		}
		if e != nil {
			return e
		}
		request := domain.Request{Key: r.Key, Method: "POST", Target: "/local/v1/restore/" + id, Body: []byte(`{}`)}
		fingerprint, e := app.Fingerprint(request, r.Normalization)
		if e != nil {
			return e
		}
		if fingerprint != r.Fingerprint {
			return domain.ErrConflict
		}
		if r.State != "completed" {
			return domain.ErrReconciliation
		}
	}
	return nil
}

// RecordRestoreTrialFailureAt touches only the truthful isolated-trial resource
// after physical rollback. Missing reverse-index DDL still refuses normal
// startup, but cannot prevent this metadata transaction and external failure
// terminalization. Actual query/write/commit/close errors remain fatal/retryable.
func RecordRestoreTrialFailureAt(ctx context.Context, path, now string) (result error) {
	db, e := openCopy(path)
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, db.Close()) }()
	if _, e = db.ExecContext(ctx, `PRAGMA synchronous=FULL`); e != nil {
		return e
	}
	s := &Store{db: db}
	return s.Write(ctx, func(tx app.WriteTx) error {
		old, e := tx.ResourceRow("backup_trial_restore", "last")
		if e != nil && !errors.Is(e, domain.ErrNotFound) {
			return e
		}
		raw, _ := json.Marshal(domain.TrialRestore{State: "failed", CompletedAt: &now, Error: "restore_health_failed"})
		if string(old.Value) == string(raw) {
			return nil
		}
		_, e = tx.MutateResource(domain.Resource{Kind: "backup_trial_restore", ID: "last", Value: raw}, old.Revision, "resource.invalidated")
		return e
	})
}
