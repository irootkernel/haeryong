package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func job(ctx context.Context, q rowReader, id string) (domain.Job, error) {
	j := domain.Job{ID: id}
	var permissions, result []byte
	err := q.QueryRowContext(ctx, "SELECT kind,state,attempt_generation,cancel_requested,coalesce(parent_id,''),coalesce(upload_id,''),error,scope,config_revision,permissions_revisions,coalesce(deadline,''),revision,result FROM jobs WHERE job_id=?", id).Scan(&j.Kind, &j.State, &j.AttemptGeneration, &j.CancelRequested, &j.ParentID, &j.UploadID, &j.Error, &j.Scope, &j.ConfigRevision, &permissions, &j.Deadline, &j.Revision, &result)
	if err != nil {
		return j, notFound(err)
	}
	j.Result = result
	if err := json.Unmarshal(permissions, &j.Permissions); err != nil {
		return j, err
	}
	if domain.ProviderJob(j.Kind) {
		summary := &domain.ProviderAttemptSummary{}
		err = q.QueryRowContext(ctx, `WITH facts AS (
 SELECT attempt_id,attempt_generation AS generation,state FROM provider_attempts WHERE job_id=?
 UNION ALL SELECT attempt_id,coalesce(json_extract(value,'$.attempt_generation'),0),state FROM provider_orphans WHERE json_extract(value,'$.job_id')=? OR attempt_id IN(SELECT prior_attempt_id FROM provider_linked_retry_authorizations WHERE job_id=? AND decision='reconcile_only')
 ), unique_facts AS(SELECT attempt_id,max(state IN ('dispatching','running','outcome_unknown')) AS unresolved FROM facts GROUP BY attempt_id)
 SELECT count(*),coalesce(sum(unresolved),0),coalesce((SELECT state FROM facts ORDER BY generation DESC,(state IN ('dispatching','running','outcome_unknown')) DESC LIMIT 1),''),(SELECT count(*) FROM provider_reconcile_intents WHERE job_id=?) FROM unique_facts`, id, id, id, id).Scan(&summary.Count, &summary.Unknown, &summary.LatestState, &summary.ReconciliationPending)
		if err != nil {
			return j, err
		}
		if summary.Unknown > 0 {
			summary.UnknownReason = "outcome_unknown"
		}
		j.ProviderAttempts = summary
	} else if j.Kind == "scan" {
		summary := &domain.ProviderAttemptSummary{}
		err = q.QueryRowContext(ctx, `WITH child_jobs AS (
 SELECT job_id,revision,rowid AS child_seq FROM jobs WHERE parent_id=? AND kind='maintenance'
 ), facts AS (
 SELECT a.attempt_id,a.attempt_generation AS generation,a.state,c.revision AS child_rev,c.child_seq,c.job_id AS child_id,a.rowid AS seq FROM provider_attempts a JOIN child_jobs c ON c.job_id=a.job_id
 UNION ALL SELECT o.attempt_id,coalesce(json_extract(o.value,'$.attempt_generation'),0),o.state,coalesce(c.revision,0),coalesce(c.child_seq,0),coalesce(c.job_id,coalesce(json_extract(o.value,'$.job_id'),'')),0 FROM provider_orphans o LEFT JOIN child_jobs c ON c.job_id=json_extract(o.value,'$.job_id') WHERE json_extract(o.value,'$.job_id') IN (SELECT job_id FROM child_jobs) OR o.attempt_id IN(SELECT prior_attempt_id FROM provider_linked_retry_authorizations WHERE job_id IN (SELECT job_id FROM child_jobs) AND decision='reconcile_only')
 ), unique_facts AS(SELECT attempt_id,max(state IN ('dispatching','running','outcome_unknown')) AS unresolved FROM facts GROUP BY attempt_id)
 SELECT count(*),coalesce(sum(unresolved),0),coalesce((SELECT state FROM facts ORDER BY (state IN ('dispatching','running','outcome_unknown')) DESC,(state='result_saved') DESC,child_rev DESC,child_seq DESC,child_id DESC,generation DESC,seq DESC LIMIT 1),''),(SELECT count(*) FROM provider_reconcile_intents WHERE job_id IN (SELECT job_id FROM child_jobs)) FROM unique_facts`, id).Scan(&summary.Count, &summary.Unknown, &summary.LatestState, &summary.ReconciliationPending)
		if err != nil {
			return j, err
		}
		if summary.Count > 0 || summary.Unknown > 0 || summary.ReconciliationPending > 0 {
			if summary.Unknown > 0 {
				summary.UnknownReason = "outcome_unknown"
			}
			j.ProviderAttempts = summary
		}
	}
	return j, nil
}
func (s *Store) Job(ctx context.Context, id string) (domain.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return job(ctx, s.db, id)
}
func (t *writeTx) CreateJob(j domain.Job) error {
	switch j.Kind {
	case "upload_seal", "import", "lifecycle", "backup", "export", "restore", "derived_index", "generation", "question", "scan", "maintenance":
	default:
		return errors.New("local job kind not implemented")
	}
	if j.AttemptGeneration == 0 {
		j.AttemptGeneration = 1
	}
	if j.State == "" {
		j.State = "queued"
	}
	if j.State != "queued" || j.CancelRequested {
		return domain.ErrTransition
	}
	if j.Scope == nil {
		j.Scope = []byte(`{}`)
	}
	if j.Permissions == nil {
		j.Permissions = map[string]int64{}
	}
	if j.ConfigRevision == 0 {
		if err := t.tx.QueryRowContext(t.ctx, "SELECT max(config_revision) FROM config_revisions").Scan(&j.ConfigRevision); err != nil {
			return err
		}
	}
	if err := t.exec("INSERT INTO jobs(job_id,kind,state,attempt_generation,cancel_requested,parent_id,upload_id,error,scope,config_revision,permissions_revisions,deadline) VALUES(?,?,?,?,0,?,?,?,?,?,?,?)", j.ID, j.Kind, j.State, j.AttemptGeneration, nullable(j.ParentID), nullable(j.UploadID), j.Error, j.Scope, j.ConfigRevision, jsonBytes(j.Permissions), nullable(j.Deadline)); err != nil {
		return err
	}
	return t.jobUpdated(j.ID)
}
func (t *writeTx) ClaimJob(id string) (domain.Job, error) {
	j, err := job(t.ctx, t.tx, id)
	if err != nil {
		return j, err
	}
	if j.State != "queued" || j.CancelRequested {
		return j, domain.ErrTransition
	}
	j.AttemptGeneration++
	j.State = "running"
	j.Revision++
	if err := t.exec("UPDATE jobs SET state='running',attempt_generation=?,revision=revision+1 WHERE job_id=?", j.AttemptGeneration, id); err != nil {
		return j, err
	}
	if j.Kind == "scan" {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if err := t.exec("UPDATE scan_runs SET started_at=coalesce(started_at,?),updated_at=? WHERE job_id=?", now, now, id); err != nil {
			return j, err
		}
	}
	return j, t.jobUpdated(id)
}
func terminal(state string) bool {
	return state == "completed" || state == "failed" || state == "cancelled"
}
func (t *writeTx) CancelJob(id string) (domain.Job, error) {
	j, err := job(t.ctx, t.tx, id)
	if err != nil {
		return j, err
	}
	if j.Kind == "lifecycle" {
		if _, err := t.PurgeIntent(id); err == nil {
			return j, domain.ErrTransition
		} else if !errors.Is(err, domain.ErrNotFound) {
			return j, err
		}
	}
	// The public cancel receipt and recovered-state normalization share this
	// writer transaction. Its bodyless outbox records the ordered revisions.
	if domain.ProviderJob(j.Kind) && j.State == "interrupted" {
		if err = t.ReconcileJob(id); err != nil {
			return j, err
		}
		j, err = job(t.ctx, t.tx, id)
		if err != nil {
			return j, err
		}
		if j.CancelRequested {
			if err = t.ConfirmStop(id, j.AttemptGeneration); err != nil {
				return j, err
			}
			return job(t.ctx, t.tx, id)
		}
	}
	if terminal(j.State) || j.CancelRequested {
		return j, nil
	}
	wasQueued := j.State == "queued" || ((j.Kind == "scan" || j.Kind == "derived_index") && j.State == "paused")
	if j.Kind == "scan" {
		if err := t.exec(`DELETE FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?`, id); err != nil {
			return j, err
		}
		if err := t.settleScanOwnedRepairs(id); err != nil {
			return j, err
		}
	}
	j.CancelRequested = true
	j.AttemptGeneration++
	j.State = "cancelling"
	j.Revision++
	if err := t.exec("UPDATE jobs SET state='cancelling',cancel_requested=1,attempt_generation=?,revision=revision+1 WHERE job_id=?", j.AttemptGeneration, id); err != nil {
		return j, err
	}
	if wasQueued || domain.ProviderJob(j.Kind) {
		if err := t.ConfirmStop(id, j.AttemptGeneration); err != nil {
			return j, err
		}
		return job(t.ctx, t.tx, id)
	}
	if j.Kind == "scan" {
		updated, err := job(t.ctx, t.tx, id)
		if err != nil {
			return j, err
		}
		if updated.ProviderAttempts != nil && updated.ProviderAttempts.Unknown > 0 {
			if err := t.ConfirmStop(id, updated.AttemptGeneration); err != nil {
				return j, err
			}
			return job(t.ctx, t.tx, id)
		}
	}
	return j, t.jobUpdated(id)
}
func (t *writeTx) ConfirmStop(id string, generation int64) error {
	j, err := job(t.ctx, t.tx, id)
	if err != nil {
		return err
	}
	if j.AttemptGeneration != generation || !j.CancelRequested || (j.State != "cancelling" && j.State != "interrupted" && j.State != "cancelled" && !(domain.ProviderJob(j.Kind) && (j.State == "paused" || j.State == "reconciling")) && !((j.Kind == "scan" || j.Kind == "derived_index") && j.State == "paused")) {
		return domain.ErrStaleWorker
	}
	if domain.ProviderJob(j.Kind) {
		if j.ProviderAttempts != nil && j.ProviderAttempts.Unknown > 0 {
			return t.PauseProviderJob(id, "outcome_unknown")
		}
		attempts, e := t.providerJobEffects(id)
		if e != nil {
			return e
		}
		for _, a := range attempts {
			if a.State == "dispatching" || a.State == "running" || a.State == "outcome_unknown" {
				return t.PauseProviderJob(id, "outcome_unknown")
			}
			if a.State == "prepared" {
				if e = t.ObserveProviderAttempt(a.ID, "cancelled", "", "", "cancel_before_dispatch"); e != nil {
					return e
				}
			}
		}
	}
	if j.Kind == "scan" {
		if err := t.settleScanOwnedRepairs(id); err != nil {
			return err
		}
		j, err = job(t.ctx, t.tx, id)
		if err != nil {
			return err
		}
		var unresolvedChildren int
		if err := t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM jobs WHERE parent_id=? AND kind='maintenance' AND state NOT IN ('completed','failed','cancelled')`, id).Scan(&unresolvedChildren); err != nil {
			return err
		}
		if (j.ProviderAttempts != nil && j.ProviderAttempts.Unknown > 0) || unresolvedChildren > 0 {
			return t.PauseProviderJob(id, "outcome_unknown")
		}
	}
	if j.State != "cancelled" {
		if err := t.exec("UPDATE jobs SET state='cancelled',revision=revision+1 WHERE job_id=?", id); err != nil {
			return err
		}
		if err := t.jobUpdated(id); err != nil {
			return err
		}
	}
	if domain.ProviderJob(j.Kind) {
		if err := t.releaseProviderInputs(id); err != nil {
			return err
		}
	}
	if err := t.stoppedOwnership(j); err != nil {
		return err
	}
	if j.UploadID != "" {
		if err := t.exec("UPDATE uploads SET state='open' WHERE upload_id=? AND state='sealing' AND seal_job_id=?", j.UploadID, id); err != nil {
			return err
		}
		// Abandon releases staging only after stop is confirmed. Other consumer,
		// active read, backup and recovery ownership is deliberately untouched.
		return t.exec("DELETE FROM object_pins WHERE owner_kind='upload' AND owner_id=? AND EXISTS(SELECT 1 FROM uploads WHERE upload_id=? AND state='abandoned')", j.UploadID, j.UploadID)
	}
	return nil
}
func (t *writeTx) ReconcileJob(id string) error {
	j, err := job(t.ctx, t.tx, id)
	if err != nil {
		return err
	}
	if j.State != "interrupted" {
		return domain.ErrTransition
	}
	if domain.ProviderJob(j.Kind) {
		if err = t.exec(`UPDATE jobs SET state='reconciling',revision=revision+1 WHERE job_id=?`, id); err != nil {
			return err
		}
		return t.jobUpdated(id)
	}
	if j.CancelRequested {
		return t.ConfirmStop(id, j.AttemptGeneration)
	}
	if err := t.exec("UPDATE jobs SET state='queued',revision=revision+1 WHERE job_id=?", id); err != nil {
		return err
	}
	return t.jobUpdated(id)
}
func (t *writeTx) RetryJob(id, newID string) (domain.Job, error) {
	old, err := job(t.ctx, t.tx, id)
	if err != nil {
		return domain.Job{}, err
	}
	if !terminal(old.State) || newID == id {
		return domain.Job{}, domain.ErrTransition
	}
	next := domain.Job{ID: newID, Kind: old.Kind, ParentID: id, UploadID: old.UploadID, Scope: old.Scope, ConfigRevision: old.ConfigRevision, Permissions: old.Permissions, Deadline: old.Deadline, State: "queued", AttemptGeneration: 1, Revision: 1}
	return next, t.CreateJob(next)
}
func (t *writeTx) FinishJob(id string, generation int64, failure string, apply func(app.WriteTx) error) error {
	j, err := job(t.ctx, t.tx, id)
	if err != nil {
		return err
	}
	if j.State != "running" || j.CancelRequested || j.AttemptGeneration != generation {
		return domain.ErrStaleWorker
	}
	if failure != "" {
		j.Error = failure
		if err := t.exec("UPDATE jobs SET state='failed',error=?,revision=revision+1 WHERE job_id=?", failure, id); err != nil {
			return err
		}
		if j.UploadID != "" {
			if err := t.exec("UPDATE uploads SET state='open',error=? WHERE upload_id=? AND state='sealing' AND seal_job_id=?", failure, j.UploadID, id); err != nil {
				return err
			}
		}
		if err := t.stoppedOwnership(j); err != nil {
			return err
		}
		if domain.ProviderJob(j.Kind) {
			if err := t.releaseProviderInputs(id); err != nil {
				return err
			}
		}
		return t.jobUpdated(id)
	}
	if apply != nil {
		if err := apply(t); err != nil {
			return err
		}
	}
	if err := t.exec("UPDATE jobs SET state='completed',revision=revision+1 WHERE job_id=?", id); err != nil {
		return err
	}
	if domain.ProviderJob(j.Kind) {
		if err := t.releaseProviderInputs(id); err != nil {
			return err
		}
	}
	return t.jobUpdated(id)
}

func upload(ctx context.Context, q rowReader, id string) (domain.Upload, error) {
	u := domain.Upload{ID: id}
	var checkpoint []byte
	err := q.QueryRowContext(ctx, "SELECT principal_id,purpose,input_mode,selection_name,target,state,checkpoint,coalesce(seal_job_id,''),coalesce(sealed_content_set_id,''),error,coalesce(originating_device_id,''),created_at,updated_at,revision,coalesce(seal_operation_id,''),coalesce(sealed_object_hash,'') FROM uploads WHERE upload_id=?", id).Scan(&u.PrincipalID, &u.Purpose, &u.InputMode, &u.SelectionName, &u.Target, &u.State, &checkpoint, &u.SealJobID, &u.ContentSetID, &u.Error, &u.DeviceID, &u.CreatedAt, &u.UpdatedAt, &u.Revision, &u.SealOperationID, &u.ObjectHash)
	if err != nil {
		return u, notFound(err)
	}
	cp, err := app.ParseCheckpoint(checkpoint)
	u.Checkpoint = cp
	return u, err
}
func (s *Store) Upload(ctx context.Context, id string) (domain.Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return upload(ctx, s.db, id)
}
func (t *writeTx) CreateUpload(u domain.Upload) error {
	if err := app.ValidateCheckpoint(u.Checkpoint); err != nil {
		return err
	}
	if u.Checkpoint.Generation != 1 || u.Checkpoint.AcceptedBytes != "0" || u.Checkpoint.Complete || u.Checkpoint.InventorySHA256 != app.EmptyInventorySHA256 {
		return domain.ErrTransition
	}
	if (u.Purpose == "result" && u.InputMode != "directory" && u.InputMode != "archive" && u.InputMode != "file") || (u.Purpose != "result" && u.InputMode != "object") {
		return errors.New("invalid purpose/input_mode combination")
	}
	if u.State != "" && u.State != "open" {
		return domain.ErrTransition
	}
	if u.SealJobID != "" || u.ContentSetID != "" {
		return domain.ErrTransition
	}
	if len(u.Target) == 0 || u.PrincipalID == "" {
		return errors.New("upload principal and exact purpose/target required")
	}
	normalizedTarget, err := app.NormalizeTarget(u.Purpose, u.Target)
	if err != nil {
		return err
	}
	u.Target = normalizedTarget
	switch u.Purpose {
	case "result", "context", "body", "metadata":
	default:
		return errors.New("invalid upload purpose")
	}
	return t.exec("INSERT INTO uploads(upload_id,principal_id,purpose,input_mode,selection_name,target,state,checkpoint,seal_job_id,sealed_content_set_id,error,originating_device_id,created_at,updated_at) VALUES(?,?,?,?,?,?,'open',?,NULL,NULL,'',?,?,?)", u.ID, u.PrincipalID, u.Purpose, u.InputMode, u.SelectionName, u.Target, jsonBytes(u.Checkpoint), nullable(u.DeviceID), u.CreatedAt, u.UpdatedAt)
}
func (t *writeTx) RecordRange(r domain.UploadRange, expectedGeneration int64, inventory string) (domain.Checkpoint, error) {
	u, err := upload(t.ctx, t.tx, r.UploadID)
	if err != nil {
		return u.Checkpoint, err
	}
	if u.State != "open" || u.Checkpoint.Complete {
		return u.Checkpoint, domain.ErrTransition
	}
	if r.Offset < 0 || r.Size < 0 || r.Size > math.MaxInt64-r.Offset || !hashPattern.MatchString(inventory) {
		return u.Checkpoint, errors.New("invalid physical file range/digest")
	}
	var size int64
	var hash string
	err = t.tx.QueryRowContext(t.ctx, "SELECT byte_size,content_hash FROM upload_ranges WHERE upload_id=? AND relative_path=? AND range_offset=?", r.UploadID, r.Path, r.Offset).Scan(&size, &hash)
	if err == nil {
		if size != r.Size || hash != r.Hash {
			return u.Checkpoint, domain.ErrConflict
		}
		return u.Checkpoint, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return u.Checkpoint, err
	}
	if u.Checkpoint.Generation != expectedGeneration {
		return u.Checkpoint, domain.ErrPrecondition
	}
	var overlaps int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM upload_ranges WHERE upload_id=? AND relative_path=? AND range_offset<? AND range_offset+byte_size>?", r.UploadID, r.Path, r.Offset+r.Size, r.Offset).Scan(&overlaps); err != nil {
		return u.Checkpoint, err
	}
	if overlaps != 0 {
		return u.Checkpoint, domain.ErrConflict
	}
	obj, err := t.Object(r.Hash)
	if err != nil {
		return u.Checkpoint, err
	}
	if obj.Size != r.Size {
		return u.Checkpoint, domain.ErrConflict
	}
	if err := t.AcquirePin(domain.Pin{ID: fmt.Sprintf("upload:%s:%s:%d", r.UploadID, r.Path, r.Offset), Kind: "upload", OwnerID: r.UploadID, Hash: r.Hash}); err != nil {
		return u.Checkpoint, err
	}
	if err := t.exec("INSERT INTO upload_ranges VALUES(?,?,?,?,?)", r.UploadID, r.Path, r.Offset, r.Size, r.Hash); err != nil {
		return u.Checkpoint, err
	}
	count, _ := new(big.Int).SetString(u.Checkpoint.AcceptedBytes, 10)
	count.Add(count, big.NewInt(r.Size))
	u.Checkpoint.Generation++
	u.Checkpoint.AcceptedBytes = count.String()
	u.Checkpoint.InventorySHA256 = inventory
	return u.Checkpoint, t.exec("UPDATE uploads SET checkpoint=? WHERE upload_id=?", jsonBytes(u.Checkpoint), r.UploadID)
}
func (t *writeTx) CompleteCheckpoint(id string, expected int64, inventory string) (domain.Checkpoint, error) {
	u, err := upload(t.ctx, t.tx, id)
	if err != nil {
		return u.Checkpoint, err
	}
	if u.State != "open" {
		return u.Checkpoint, domain.ErrTransition
	}
	if u.Checkpoint.Generation != expected {
		return u.Checkpoint, domain.ErrPrecondition
	}
	if !hashPattern.MatchString(inventory) {
		return u.Checkpoint, errors.New("invalid inventory digest")
	}
	if !u.Checkpoint.Complete {
		u.Checkpoint.Generation++
	}
	u.Checkpoint.Complete = true
	u.Checkpoint.InventorySHA256 = inventory
	return u.Checkpoint, t.exec("UPDATE uploads SET checkpoint=? WHERE upload_id=?", jsonBytes(u.Checkpoint), id)
}
func (t *writeTx) CompleteUpload(id string, checkpoint domain.Checkpoint, jobID string) (domain.Response, error) {
	if err := app.ValidateCheckpoint(checkpoint); err != nil {
		return domain.Response{}, err
	}
	u, err := upload(t.ctx, t.tx, id)
	if err != nil {
		return domain.Response{}, err
	}
	if !checkpoint.Complete || u.Checkpoint != checkpoint || u.State == "abandoned" {
		return domain.Response{}, domain.ErrTransition
	}
	if u.State == "sealed" {
		return domain.Response{Status: 200, Body: jsonBytes(map[string]string{"upload_id": id, "content_set_id": u.ContentSetID})}, nil
	}
	if u.State == "open" {
		if err := t.CreateJob(domain.Job{ID: jobID, Kind: "upload_seal", UploadID: id, ParentID: u.SealJobID}); err != nil {
			return domain.Response{}, err
		}
		if err := t.exec("UPDATE uploads SET state='sealing',seal_job_id=?,error='' WHERE upload_id=?", jobID, id); err != nil {
			return domain.Response{}, err
		}
		u.SealJobID = jobID
	}
	return domain.Response{Status: 202, Body: jsonBytes(map[string]string{"upload_id": id, "job_id": u.SealJobID})}, nil
}
func (t *writeTx) FinishSeal(id string, generation int64, setID string) error {
	j, err := job(t.ctx, t.tx, id)
	if err != nil {
		return err
	}
	if j.Kind != "upload_seal" {
		return domain.ErrTransition
	}
	return t.FinishJob(id, generation, "", func(app.WriteTx) error {
		u, err := upload(t.ctx, t.tx, j.UploadID)
		if err != nil {
			return err
		}
		if u.State != "sealing" || u.SealJobID != id {
			return domain.ErrStaleWorker
		}
		var sealed bool
		if err := t.tx.QueryRowContext(t.ctx, "SELECT sealed FROM content_sets WHERE content_set_id=?", setID).Scan(&sealed); err != nil {
			return notFound(err)
		}
		if !sealed {
			return domain.ErrTransition
		}
		return t.exec("UPDATE uploads SET state='sealed',sealed_content_set_id=? WHERE upload_id=?", setID, j.UploadID)
	})
}
func (t *writeTx) AbandonUpload(id string) error {
	u, err := upload(t.ctx, t.tx, id)
	if err != nil {
		return err
	}
	if u.State == "abandoned" {
		return nil
	}
	if err := t.exec("UPDATE uploads SET state='abandoned' WHERE upload_id=?", id); err != nil {
		return err
	}
	if u.SealJobID != "" {
		j, err := t.CancelJob(u.SealJobID)
		if err != nil {
			return err
		}
		if !terminal(j.State) {
			return nil
		}
	}
	return t.exec("DELETE FROM object_pins WHERE owner_kind='upload' AND owner_id=?", id)
}
func (s *Store) Ranges(ctx context.Context, id, afterPath string, afterOffset int64, limit int) ([]domain.UploadRange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, "SELECT upload_id,relative_path,range_offset,byte_size,content_hash FROM upload_ranges WHERE upload_id=? AND (relative_path>? COLLATE BINARY OR (relative_path=? AND range_offset>?)) ORDER BY relative_path COLLATE BINARY,range_offset LIMIT ?", id, afterPath, afterPath, afterOffset, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.UploadRange{}
	for rows.Next() {
		var r domain.UploadRange
		if err := rows.Scan(&r.UploadID, &r.Path, &r.Offset, &r.Size, &r.Hash); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (t *writeTx) AdoptUploadObject(id, purpose string, target []byte, ref domain.Reference, pin string) error {
	u, err := upload(t.ctx, t.tx, id)
	if err != nil {
		return err
	}
	if u.State != "sealed" || u.Purpose != purpose {
		return domain.ErrTransition
	}
	normalized, err := app.NormalizeTarget(purpose, target)
	if err != nil {
		return err
	}
	if string(normalized) != string(u.Target) {
		return domain.ErrConflict
	}
	var count int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT (SELECT count(*) FROM upload_ranges WHERE upload_id=? AND content_hash=?)+(SELECT count(*) FROM version_entries WHERE content_set_id=? AND content_hash=?)", id, ref.Hash, u.ContentSetID, ref.Hash).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return domain.ErrConflict
	}
	return t.Adopt(ref, pin)
}

func (t *writeTx) jobUpdated(id string) error {
	j, err := job(t.ctx, t.tx, id)
	if err != nil {
		return err
	}
	return t.appendEvent("job", id, "job.updated", j.Revision, nil)
}
func (s *Store) recoverLocalJobs(ctx context.Context) error {
	const needs = "state IN ('running','cancelling','reconciling') OR (state='paused' AND cancel_requested=1)"
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE "+needs).Scan(&count); err != nil {
		return err
	}
	var pending int
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM provider_reconcile_intents WHERE state='executing')+(SELECT count(*) FROM provider_attempts WHERE state IN ('dispatching','running'))+(SELECT count(*) FROM job_items WHERE state='running')`).Scan(&pending); err != nil {
		return err
	}
	if count == 0 && pending == 0 {
		return nil
	}
	// Prepare already owns s.mu and the profile lock. Start the private SQL
	// transaction here rather than recursively acquiring Store.Write's mutex.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t := &writeTx{tx: tx, ctx: ctx}
	if err := t.exec(`UPDATE provider_reconcile_intents SET state='pending',runtime_generation='' WHERE state='executing'`); err != nil {
		return err
	}
	if err := t.exec(`UPDATE provider_attempts SET restart_generation=(SELECT attempt_generation+1 FROM jobs WHERE jobs.job_id=provider_attempts.job_id) WHERE state IN ('result_saved','dispatching','running','outcome_unknown') AND (application_generation=(SELECT attempt_generation FROM jobs WHERE jobs.job_id=provider_attempts.job_id) OR restart_generation=(SELECT attempt_generation FROM jobs WHERE jobs.job_id=provider_attempts.job_id)) AND job_id IN(SELECT job_id FROM jobs WHERE ` + needs + `)`); err != nil {
		return err
	}
	if err := t.exec(`UPDATE provider_attempts SET state='outcome_unknown',reason='transport_unknown' WHERE state IN ('dispatching','running')`); err != nil {
		return err
	}
	if err := t.exec(`UPDATE job_items SET state='pending' WHERE state='running'`); err != nil {
		return err
	}
	if err := t.exec("INSERT INTO events(stream_epoch,resource_kind,resource_id,kind,revision,body) SELECT libraries.stream_epoch,'job',jobs.job_id,'job.updated',jobs.revision+1,NULL FROM jobs CROSS JOIN libraries WHERE libraries.singleton=1 AND (" + needs + ")"); err != nil {
		return err
	}
	if err := t.exec("UPDATE jobs SET state='interrupted',attempt_generation=attempt_generation+1,revision=revision+1 WHERE " + needs); err != nil {
		return err
	}
	if err := s.boundary(BeforeCommit); err != nil {
		return err
	}
	return tx.Commit()
}

func (t *writeTx) stoppedOwnership(j domain.Job) error {
	switch j.Kind {
	case "backup", "export":
		// Failure finalization also calls stoppedOwnership. Preserve its precise
		// failed stage/error; only confirmed cancellation becomes interrupted.
		if !j.CancelRequested {
			return nil
		}
		var scope map[string]json.RawMessage
		if json.Unmarshal(j.Scope, &scope) != nil || scope == nil {
			return domain.ErrInvalidTarget
		}
		raw, declared := scope["copy_set_id"]
		var id string
		if declared && (json.Unmarshal(raw, &id) != nil || id == "") {
			return domain.ErrInvalidTarget
		}
		// Generic local ledger jobs predate the copy coordinator. Absence of a
		// declaration is unowned only when no set claims this job in reverse.
		var owners int
		var bound string
		if err := t.tx.QueryRowContext(t.ctx, "SELECT count(*),coalesce(min(set_id),'') FROM copy_sets WHERE job_id=?", j.ID).Scan(&owners, &bound); err != nil {
			return err
		}
		if owners > 1 || owners == 1 && (!declared || bound != id) {
			return domain.ErrInvalidTarget
		}
		if !declared {
			return nil
		}
		set, err := t.CopySet(id)
		if err != nil {
			return err
		}
		if set.ID != id || set.Kind != j.Kind || (set.JobID == j.ID) != (owners == 1) {
			return domain.ErrInvalidTarget
		}
		// A former linked job cannot touch its replacement. An already
		// interrupted set has no new progress/revision/event to record.
		if set.JobID == j.ID && set.Stage != "interrupted" && set.Stage != "complete" && set.Stage != "abandoned" && set.Stage != "expired" && set.Stage != "restored_paused" {
			set.Stage = "interrupted"
			// Capture barriers and copy pins remain until exact resume/abandon.
			return t.PutCopySet(set)
		}
		return nil
	case "derived_index":
		reason := j.Error
		if j.CancelRequested {
			reason = "cancelled"
		}
		if reason == "" {
			reason = "interrupted"
		}
		w, err := t.DerivedWork(j.ID)
		if err != nil {
			return err
		}
		if w.WorkKind == "references" {
			if err = t.FailReferences(j.ID); err != nil {
				return err
			}
			return t.ReferenceProgress(j.ID, "complete", w.AfterPath, 0, 0)
		}
		return t.FailSearch(j.ID, reason)
	case "import":
		if err := t.exec("DELETE FROM metadata_indexes WHERE job_id=?", j.ID); err != nil {
			return err
		}
	case "upload_seal":
		if err := t.DiscardContentSet(j.ID); err != nil {
			return err
		}
	case "scan":
		if err := t.settleScanOwnedRepairs(j.ID); err != nil {
			return err
		}
		itemState := "failed"
		reason := j.Error
		if j.CancelRequested {
			itemState = "cancelled"
			reason = "cancelled"
		}
		if reason == "" {
			reason = itemState
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if err := t.exec(`UPDATE job_items SET state=?,reason=?,stage='complete',updated_at=? WHERE job_id=? AND state IN ('pending','running')`, itemState, reason, now, j.ID); err != nil {
			return err
		}
		if err := t.finalizeUnmaterializedScanUnits(j.ID, itemState, reason, now); err != nil {
			return err
		}
	default:
		return nil
	}
	if err := t.ReleaseOwnerPins("job", j.ID); err != nil {
		return err
	}
	if j.UploadID != "" {
		return t.ReclaimAbandonedUpload(j.UploadID)
	}
	return nil
}

func (t *writeTx) settleScanOwnedRepairs(scanJobID string) error {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT DISTINCT repair_job_id FROM job_items WHERE job_id=? AND repair_job_id!='' UNION SELECT job_id FROM jobs WHERE parent_id=? AND kind='maintenance' ORDER BY 1`, scanJobID, scanJobID)
	if err != nil {
		return err
	}
	childIDs := []string{}
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err != nil {
			rows.Close()
			return err
		}
		childIDs = append(childIDs, cid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, cid := range childIDs {
		if err := t.exec(`DELETE FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?`, cid); err != nil {
			return err
		}
		child, e := job(t.ctx, t.tx, cid)
		if errors.Is(e, domain.ErrNotFound) {
			continue
		}
		if e != nil {
			return e
		}
		if !terminal(child.State) {
			if !child.CancelRequested {
				child, e = t.CancelJob(cid)
				if e != nil {
					return e
				}
			}
			if !terminal(child.State) && !domain.ProviderJob(child.Kind) {
				if e = t.ConfirmStop(cid, child.AttemptGeneration); e != nil {
					return e
				}
			}
			if !terminal(child.State) && domain.ProviderJob(child.Kind) && (child.ProviderAttempts == nil || child.ProviderAttempts.Unknown == 0) {
				if e = t.ConfirmStop(cid, child.AttemptGeneration); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
