package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"time"
)

func (t *writeTx) ProviderAttempt(id string) (domain.ProviderAttempt, error) {
	a := domain.ProviderAttempt{ID: id}
	e := t.tx.QueryRowContext(t.ctx, `SELECT job_id,attempt_generation,application_generation,state,runtime_generation,request_sha256,coalesce(target_set_id,''),provider_request_id,provider_session_id,coalesce(result_hash,''),reason,may_have_dispatched FROM provider_attempts WHERE attempt_id=?`, id).Scan(&a.JobID, &a.Generation, &a.ApplyGeneration, &a.State, &a.Runtime, &a.RequestSHA, &a.TargetSetID, &a.RequestID, &a.SessionID, &a.ResultHash, &a.Reason, &a.MayHaveDispatched)
	if errors.Is(e, sql.ErrNoRows) {
		var raw []byte
		if e = t.tx.QueryRowContext(t.ctx, `SELECT value FROM provider_orphans WHERE attempt_id=?`, id).Scan(&raw); e != nil {
			return a, notFound(e)
		}
		var fact providerRestoreFact
		if e = json.Unmarshal(raw, &fact); e != nil {
			return a, e
		}
		a = domain.ProviderAttempt{ID: id, JobID: fact.JobID, Generation: fact.Generation, State: fact.State, RequestSHA: fact.SHA, RequestID: fact.RequestID, SessionID: fact.SessionID, ResultHash: fact.ResultHash, Reason: fact.Reason, Transport: fact.Transport, MayHaveDispatched: fact.MayHaveDispatched == nil || *fact.MayHaveDispatched || fact.State == "dispatching" || fact.State == "running" || fact.State == "outcome_unknown"}
	}
	if e == nil {
		var raw []byte
		err := t.tx.QueryRowContext(t.ctx, `SELECT value FROM provider_orphans WHERE attempt_id=?`, id).Scan(&raw)
		if err == nil {
			var fact providerRestoreFact
			if err = json.Unmarshal(raw, &fact); err != nil {
				return a, err
			}
			if fact.State == "dispatching" || fact.State == "running" || fact.State == "outcome_unknown" {
				a.State = "outcome_unknown"
			} else if a.State == "prepared" || a.State == "cancelled" && a.Reason == "cancel_before_dispatch" {
				a.State = fact.State
				a.Reason = fact.Reason
				if a.ResultHash == "" {
					a.ResultHash = fact.ResultHash
				}
			}
			if a.Transport == nil {
				a.Transport = fact.Transport
			}
			if a.RequestID == "" {
				a.RequestID = fact.RequestID
			}
			if a.SessionID == "" {
				a.SessionID = fact.SessionID
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return a, err
		}
	}
	if e == nil && a.Transport == nil {
		var raw []byte
		err := t.tx.QueryRowContext(t.ctx, `SELECT value FROM provider_transport WHERE attempt_id=?`, id).Scan(&raw)
		if err == nil {
			a.Transport = new(domain.ProviderTransport)
			if err = json.Unmarshal(raw, a.Transport); err != nil {
				return a, err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return a, err
		}
	}
	return a, notFound(e)
}
func (t *writeTx) ProviderAttempts(jobID string) ([]domain.ProviderAttempt, error) {
	rows, e := t.tx.QueryContext(t.ctx, `SELECT attempt_id FROM provider_attempts WHERE job_id=? ORDER BY attempt_generation`, jobID)
	if e != nil {
		return nil, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = errors.Join(rows.Err(), rows.Close())
	if e != nil {
		return nil, e
	}
	out := []domain.ProviderAttempt{}
	for _, id := range ids {
		a, e := t.ProviderAttempt(id)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, nil
}
func (t *writeTx) ProviderInputs(id string) ([]domain.ProviderInput, error) {
	rows, e := t.tx.QueryContext(t.ctx, `SELECT i.read_id,i.content_hash,coalesce(i.input_origin,''),i.observed_at,coalesce(r.retention_mode,''),coalesce(json_extract(c.requested_selection,'$.version_id'),'') FROM provider_inputs i LEFT JOIN source_capture_reads r ON r.read_id=i.read_id LEFT JOIN source_captures c ON c.capture_id=r.capture_id WHERE i.attempt_id=? ORDER BY i.read_id`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.ProviderInput{}
	for rows.Next() {
		v := domain.ProviderInput{AttemptID: id}
		if e = rows.Scan(&v.ReadID, &v.Hash, &v.ObservedOrigin, &v.ObservedAt, &v.RetentionMode, &v.SourceVersionID); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return nil, e
	}
	for i := range out {
		versions, err := t.sourceReadVersions(out[i].ReadID)
		if err != nil {
			return nil, err
		}
		out[i].SourceVersionIDs = versions
	}
	return out, nil
}
func (t *writeTx) PrepareProviderAttempt(a domain.ProviderAttempt, inputs []domain.ProviderInput) error {
	if a.ID == "" || a.MayHaveDispatched || a.State != "prepared" || a.RequestID != "" || a.SessionID != "" || a.ResultHash != "" || a.Reason != "" || len(a.RequestSHA) != 64 {
		return domain.ErrInvalidTarget
	}
	if e := app.GuardProvider(t, a); e != nil {
		return e
	}
	j, e := t.Job(a.JobID)
	if e != nil {
		return e
	}
	var scope domain.ProviderScope
	if json.Unmarshal(j.Scope, &scope) != nil {
		return domain.ErrInvalidTarget
	}
	owned, e := app.ProviderInputObjects(t, scope)
	if e != nil {
		return e
	}
	if len(scope.ReadIDs) != len(inputs) {
		return domain.ErrInvalidTarget
	}
	wanted := map[string]bool{}
	for _, id := range scope.ReadIDs {
		if wanted[id] || id == "" {
			return domain.ErrInvalidTarget
		}
		wanted[id] = true
	}
	if e = t.guardProviderDuplicate(a); e != nil {
		return e
	}
	if e = t.exec(`INSERT INTO provider_attempts(attempt_id,job_id,attempt_generation,application_generation,state,runtime_generation,request_sha256,target_set_id) VALUES(?,?,?,?,?,?,?,?)`, a.ID, a.JobID, a.Generation, a.Generation, a.State, a.Runtime, a.RequestSHA, nullableString(a.TargetSetID)); e != nil {
		return e
	}
	for _, v := range inputs {
		if v.AttemptID != a.ID || !wanted[v.ReadID] || v.ObservedOrigin != "" || v.ObservedAt != nil {
			return domain.ErrInvalidTarget
		}
		delete(wanted, v.ReadID)
		var target, hash string
		if e = t.tx.QueryRowContext(t.ctx, `SELECT target_set_id,coalesce(bytes_sha256,'') FROM captured_reads WHERE read_id=?`, v.ReadID).Scan(&target, &hash); e != nil {
			return notFound(e)
		}
		expectedTarget, _, _, ok := domain.MaintenanceReadTarget(scope, a.TargetSetID, "", v.ReadID)
		if !ok || target != expectedTarget || hash != v.Hash {
			return domain.ErrInvalidTarget
		}
		o, e := t.Object(v.Hash)
		if e != nil {
			return e
		}
		if o.State != "ready" {
			return domain.ErrDeleting
		}
		if e = t.exec(`INSERT INTO provider_inputs(attempt_id,read_id,content_hash) VALUES(?,?,?)`, a.ID, v.ReadID, v.Hash); e != nil {
			return e
		}
		if e = t.AcquirePin(domain.Pin{ID: a.ID + ":" + v.ReadID, Kind: "provider_attempt", OwnerID: a.ID, Hash: v.Hash}); e != nil {
			return e
		}
	}
	for _, object := range owned {
		if e = t.AcquirePin(domain.Pin{ID: a.ID + ":owned:" + object.Hash, Kind: "provider_attempt", OwnerID: a.ID, Hash: object.Hash}); e != nil {
			return e
		}
	}
	return t.providerUpdated(a.JobID)
}
func (t *writeTx) DispatchProviderAttempt(id string) error {
	a, e := t.ProviderAttempt(id)
	if e != nil {
		return e
	}
	if a.State != "prepared" {
		return domain.ErrTransition
	}
	if e = app.GuardProvider(t, a); e != nil {
		return e
	}
	if e = t.exec(`UPDATE provider_attempts SET state='dispatching',may_have_dispatched=1 WHERE attempt_id=?`, id); e != nil {
		return e
	}
	return t.providerUpdated(a.JobID)
}
func (t *writeTx) ObserveProviderInput(id, readID, origin string) error {
	if origin != "daemon_supplied" && origin != "native_observed" {
		return domain.ErrInvalidTarget
	}
	a, e := t.ProviderAttempt(id)
	if e != nil {
		return e
	}
	if a.State != "dispatching" && a.State != "running" && a.State != "outcome_unknown" {
		return domain.ErrTransition
	}
	var old sql.NullString
	if e = t.tx.QueryRowContext(t.ctx, `SELECT input_origin FROM provider_inputs WHERE attempt_id=? AND read_id=?`, id, readID).Scan(&old); e != nil {
		return notFound(e)
	}
	if old.Valid && old.String != origin {
		return domain.ErrConflict
	}
	if old.Valid {
		return nil
	}
	// This is the first local durable acknowledgement of actual input delivery
	// or supported native telemetry, never the earlier capture or restore time.
	return t.exec(`UPDATE provider_inputs SET input_origin=?,observed_at=? WHERE attempt_id=? AND read_id=?`, origin, time.Now().UTC().Format(time.RFC3339Nano), id, readID)
}
func (t *writeTx) ObserveProviderAttempt(id, state, requestID, sessionID, reason string) error {
	a, e := t.ProviderAttempt(id)
	if e != nil {
		return e
	}
	if a.State == "prepared" && (requestID != "" || sessionID != "" || reason == "cancel_confirmed" || reason == "result_unavailable") {
		return domain.ErrTransition
	}
	allowed := false
	switch a.State {
	case "dispatching", "running":
		allowed = state == "running" || state == "failed" || state == "cancelled" || state == "outcome_unknown"
	case "outcome_unknown":
		allowed = state == "failed" || state == "cancelled" || state == "outcome_unknown"
	case "prepared":
		allowed = state == "cancelled" || state == "failed"
	}
	if !allowed {
		return domain.ErrTransition
	}
	if requestID == "" {
		requestID = a.RequestID
	}
	if sessionID == "" {
		sessionID = a.SessionID
	}
	if a.Transport != nil && a.Transport.SetupThread != "" && sessionID != "" && a.Transport.SetupThread != sessionID {
		return domain.ErrConflict
	}
	if a.RequestID != "" && requestID != a.RequestID || a.SessionID != "" && sessionID != a.SessionID {
		return domain.ErrConflict
	}
	if a.Reason == "result_retrieval_pending" && (state == "failed" && reason != "result_unavailable" || state == "cancelled" && reason == "cancel_confirmed") {
		return domain.ErrConflict
	}
	// A later inconclusive lookup cannot erase a committed native completion.
	if a.Reason == "result_retrieval_pending" && state == "outcome_unknown" {
		reason = a.Reason
	}
	// Caller supplies normalized categories, never provider error prose.
	switch reason {
	case "", "transport_unknown", "source_unavailable", "unsupported_input_format", "provider_rate_limited", "provider_budget_exhausted", "provider_context_limit", "provider_unavailable", "auth_required", "quota_exhausted", "cancel_confirmed", "cancel_before_dispatch", "provider_failed", "result_unavailable", "result_retrieval_pending":
	default:
		return domain.ErrInvalidTarget
	}
	if e = t.observeRetainedOrphan(id, state, requestID, sessionID, reason); e != nil {
		return e
	}
	if e = t.exec(`UPDATE provider_attempts SET state=?,provider_request_id=?,provider_session_id=?,reason=?,may_have_dispatched=max(may_have_dispatched,?) WHERE attempt_id=?`, state, requestID, sessionID, reason, a.State != "prepared", id); e != nil {
		return e
	}
	var exists int
	if e = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM jobs WHERE job_id=?`, a.JobID).Scan(&exists); e != nil {
		return e
	}
	if exists > 0 {
		return t.providerUpdated(a.JobID)
	}
	return nil
}
func (t *writeTx) SaveProviderResult(id string, o domain.Object) error {
	a, e := t.ProviderAttempt(id)
	if e != nil {
		return e
	}
	if a.State != "dispatching" && a.State != "running" && a.State != "outcome_unknown" {
		return domain.ErrTransition
	}
	actual, e := t.Object(o.Hash)
	if e != nil {
		return e
	}
	if actual.State != "ready" || actual.Size != o.Size {
		return domain.ErrInvalidTarget
	}
	if e = t.AcquirePin(domain.Pin{ID: a.ID + ":result", Kind: "provider_attempt", OwnerID: a.ID, Hash: o.Hash}); e != nil {
		return e
	}
	if e = t.observeRetainedOrphan(id, "result_saved", a.RequestID, a.SessionID, ""); e != nil {
		return e
	}
	if e = t.exec(`UPDATE provider_orphans SET value=json_set(value,'$.result_hash',?) WHERE attempt_id=?`, o.Hash, id); e != nil {
		return e
	}
	if e = t.exec(`UPDATE provider_attempts SET state='result_saved',result_hash=?,reason='' WHERE attempt_id=?`, o.Hash, id); e != nil {
		return e
	}
	return t.providerUpdated(a.JobID)
}
func (t *writeTx) ApplyProviderResult(id string, apply func(app.WriteTx) error) error {
	a, e := t.ProviderAttempt(id)
	if e != nil {
		return e
	}
	if a.State != "result_saved" || a.ResultHash == "" {
		return domain.ErrTransition
	}
	if object, err := t.Object(a.ResultHash); err != nil || object.State != "ready" {
		return domain.ErrReconciliation
	}

	if e = app.GuardProvider(t, a); e != nil {
		return e
	}
	if e = t.SetJobResult(a.JobID, jsonBytes(map[string]string{"attempt_id": a.ID, "result_sha256": a.ResultHash})); e != nil {
		return e
	}
	if apply != nil {
		if e = apply(t); e != nil {
			return e
		}
	}
	j, e := t.Job(a.JobID)
	if e != nil {
		return e
	}
	// A generation publication conflict retains its ready candidate and saved
	// result in this same transaction. It is never a completed publication.
	if j.Kind == "generation" && j.State == "paused" && j.Error == "VERSION_CONFLICT" {
		return nil
	}
	return t.FinishJob(a.JobID, a.ApplyGeneration, "", nil)
}

func (t *writeTx) RebindProviderResult(id string) error {
	a, e := t.ProviderAttempt(id)
	if e != nil {
		return e
	}
	if a.State != "result_saved" {
		return domain.ErrTransition
	}
	if object, err := t.Object(a.ResultHash); err != nil || object.State != "ready" {
		return domain.ErrReconciliation
	}
	j, e := t.Job(a.JobID)
	if e != nil {
		return e
	}
	var restart int64
	if e = t.tx.QueryRowContext(t.ctx, `SELECT restart_generation FROM provider_attempts WHERE attempt_id=?`, id).Scan(&restart); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return domain.ErrTransition
		}
		return e
	}
	if a.ApplyGeneration != j.AttemptGeneration && restart != j.AttemptGeneration && !(j.State == "queued" && a.ApplyGeneration == j.AttemptGeneration+1) {
		return domain.ErrStaleWorker
	}
	a.ApplyGeneration = j.AttemptGeneration
	if j.State == "queued" {
		// A retry that never claimed can leave a saved continuation unconsumed.
		// Reuse the exact next generation; never authorize a later assignment.
		a.ApplyGeneration++
		if e = app.GuardProviderContinuation(t, a); e != nil {
			return e
		}
	} else if e = app.GuardProvider(t, a); e != nil {
		return e
	}
	if _, e = app.ProviderSavedObjects(t, a); e != nil {
		return e
	}
	return t.exec(`UPDATE provider_attempts SET application_generation=? WHERE attempt_id=?`, a.ApplyGeneration, id)
}
func (t *writeTx) PauseProviderJob(id, reason string) error {
	j, e := t.Job(id)
	if e != nil {
		return e
	}
	if (!domain.ProviderJob(j.Kind) && j.Kind != "scan") || terminal(j.State) {
		return domain.ErrTransition
	}
	if e = t.exec(`UPDATE jobs SET state='paused',error=?,revision=revision+1 WHERE job_id=?`, reason, id); e != nil {
		return e
	}
	if j.Kind == "scan" {
		if e = t.pauseScanRunElapsed(id, time.Now().UTC()); e != nil {
			return e
		}
	}
	return t.jobUpdated(id)
}

func (t *writeTx) pauseScanRunElapsed(id string, now time.Time) error {
	var started sql.NullString
	var elapsed int64
	err := t.tx.QueryRowContext(t.ctx, `SELECT started_at,elapsed_ms FROM scan_runs WHERE job_id=?`, id).Scan(&started, &elapsed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	add := int64(0)
	if started.Valid && started.String != "" {
		if ts, e := time.Parse(time.RFC3339Nano, started.String); e == nil && now.After(ts) {
			add = now.Sub(ts).Milliseconds()
		}
	}
	return t.exec(`UPDATE scan_runs SET started_at=NULL,elapsed_ms=?,updated_at=? WHERE job_id=?`, elapsed+add, now.Format(time.RFC3339Nano), id)
}

func (t *writeTx) checkScanResumeBudget(scanID, decision string) error {
	if decision != "continue" && decision != "retry" {
		return nil
	}
	run, err := t.ScanRun(scanID)
	if err != nil {
		return err
	}
	maxRunSec, err := t.effectiveScanMaxRunSeconds(run, "")
	if err != nil {
		return err
	}
	elapsed := run.ElapsedMs
	if run.StartedAt != nil && *run.StartedAt != "" {
		if ts, e := time.Parse(time.RFC3339Nano, *run.StartedAt); e == nil {
			now := time.Now().UTC()
			if now.After(ts) {
				elapsed += now.Sub(ts).Milliseconds()
			}
		}
	}
	if maxRunSec > 0 && elapsed >= int64(maxRunSec)*1000 {
		return domain.ErrDenied
	}
	if decision == "retry" {
		maxAttempts, err := t.effectiveScanMaxProviderAttempts(run, "")
		if err != nil {
			return err
		}
		count, err := t.ScanProviderAttemptCount(scanID, "")
		if err != nil {
			return err
		}
		if maxAttempts > 0 && count >= maxAttempts {
			return domain.ErrDenied
		}
	}
	return nil
}
func (t *writeTx) resumeScanProviderJob(j domain.Job, decision string) (domain.Job, error) {
	if j.CancelRequested && decision != "reconcile_only" {
		return j, domain.ErrTransition
	}
	hold, err := t.RecoveryHold()
	if err != nil {
		return j, err
	}
	if hold != nil && hold.State != "reviewed" {
		return j, domain.ErrReconciliation
	}
	terminalReconcile := terminal(j.State) && decision == "reconcile_only"
	if j.State != "paused" && !(j.State == "interrupted" && decision == "reconcile_only") && !terminalReconcile {
		return j, domain.ErrTransition
	}
	var restoreHeld int
	if err = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?`, j.ID).Scan(&restoreHeld); err != nil {
		return j, err
	}
	if restoreHeld > 0 && decision != "reconcile_only" {
		return j, domain.ErrReconciliation
	}
	if err = t.checkScanResumeBudget(j.ID, decision); err != nil {
		return j, err
	}
	if decision != "reconcile_only" && decision != "continue" && decision != "retry" {
		return j, domain.ErrInvalidTarget
	}
	cRows, err := t.tx.QueryContext(t.ctx, `SELECT job_id FROM jobs WHERE parent_id=? AND kind='maintenance' ORDER BY rowid,job_id`, j.ID)
	if err != nil {
		return j, err
	}
	childIDs := []string{}
	for cRows.Next() {
		var cid string
		if err := cRows.Scan(&cid); err != nil {
			cRows.Close()
			return j, err
		}
		childIDs = append(childIDs, cid)
	}
	err = errors.Join(cRows.Err(), cRows.Close())
	if err != nil {
		return j, err
	}
	if decision == "continue" || decision == "retry" {
		if err = t.exec(`UPDATE jobs SET state='queued',error='',revision=revision+1 WHERE job_id=?`, j.ID); err != nil {
			return j, err
		}
		if err = t.jobUpdated(j.ID); err != nil {
			return j, err
		}
	}
	resumedAny := false
	for _, cid := range childIDs {
		child, err := t.Job(cid)
		if err != nil {
			return j, err
		}
		var childHeld int
		if err = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?`, cid).Scan(&childHeld); err != nil {
			return j, err
		}
		if decision == "reconcile_only" {
			hasUnknown := child.ProviderAttempts != nil && (child.ProviderAttempts.Unknown > 0 || child.ProviderAttempts.ReconciliationPending > 0)
			if !hasUnknown && childHeld == 0 {
				continue
			}
			if _, err = t.ResumeProviderJob(cid, child.AttemptGeneration, decision); err != nil {
				return j, err
			}
			resumedAny = true
			continue
		}
		if terminal(child.State) {
			continue
		}
		if _, err = t.ResumeProviderJob(cid, child.AttemptGeneration, decision); err != nil {
			return j, err
		}
		resumedAny = true
	}
	if decision == "reconcile_only" {
		if !resumedAny && restoreHeld == 0 {
			return j, domain.ErrTransition
		}
		if err = t.exec(`DELETE FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?`, j.ID); err != nil {
			return j, err
		}
		if err = t.exec(`UPDATE jobs SET revision=revision+1 WHERE job_id=?`, j.ID); err != nil {
			return j, err
		}
		if err = t.jobUpdated(j.ID); err != nil {
			return j, err
		}
		return job(t.ctx, t.tx, j.ID)
	}
	updated, err := job(t.ctx, t.tx, j.ID)
	if err != nil {
		return j, err
	}
	if decision == "continue" && updated.ProviderAttempts != nil && updated.ProviderAttempts.Unknown > 0 {
		return j, domain.ErrReconciliation
	}
	return updated, nil
}
func (t *writeTx) ResumeProviderJob(id string, generation int64, decision string) (domain.Job, error) {
	j, e := t.Job(id)
	if e != nil {
		return j, e
	}
	if j.AttemptGeneration != generation {
		return j, domain.ErrPrecondition
	}
	if j.Kind == "scan" {
		return t.resumeScanProviderJob(j, decision)
	}
	if !domain.ProviderJob(j.Kind) {
		return j, domain.ErrTransition
	}
	if j.Kind == "generation" && decision != "reconcile_only" {
		var scope domain.ProviderScope
		if json.Unmarshal(j.Scope, &scope) != nil {
			return j, domain.ErrInvalidTarget
		}
		if scope.Invocation != nil && scope.Invocation.Generation != nil {
			intent, e := t.GenerationIntent(j.ID)
			if e != nil {
				return j, e
			}
			if !intent.AdmissionReady {
				return j, domain.ErrReconciliation
			}
			if decision == "retry" && intent.PreparedVersion != nil {
				// A prepared candidate belongs to its saved attempt. Use that
				// candidate or a new explicit intent; a new result cannot replace it.
				return j, domain.ErrTransition
			}
		}
	}
	if j.Kind == "question" && decision != "reconcile_only" {
		var scope domain.ProviderScope
		if json.Unmarshal(j.Scope, &scope) != nil {
			return j, domain.ErrInvalidTarget
		}
		if scope.Invocation != nil && scope.Invocation.Question != nil {
			question, e := t.QuestionByJob(j.ID)
			if e != nil {
				return j, e
			}
			if !question.AdmissionReady {
				return j, domain.ErrReconciliation
			}
			if decision == "retry" && question.PreparedAnswer != nil {
				return j, domain.ErrTransition
			}
		}
	}
	if j.Kind == "maintenance" && decision != "reconcile_only" {
		if j.ParentID != "" {
			if e = t.checkScanResumeBudget(j.ParentID, decision); e != nil {
				return j, e
			}
		}
		var scope domain.ProviderScope
		if json.Unmarshal(j.Scope, &scope) != nil {
			return j, domain.ErrInvalidTarget
		}
		if scope.Invocation != nil && scope.Invocation.Maintenance != nil {
			intent, e := t.MaintenanceIntent(j.ID)
			if e != nil {
				return j, e
			}
			if !intent.AdmissionReady {
				return j, domain.ErrReconciliation
			}
			if decision == "retry" && len(intent.PreparedOutputJSON) > 0 {
				return j, domain.ErrTransition
			}
		}
	}
	if j.CancelRequested && decision != "reconcile_only" {
		return j, domain.ErrTransition
	}
	hold, err := t.RecoveryHold()
	if err != nil {
		return j, err
	}
	if hold != nil && hold.State != "reviewed" {
		return j, domain.ErrReconciliation
	}
	var statusCarrier int
	if e = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM provider_linked_retry_authorizations WHERE job_id=? AND decision='reconcile_only'`, id).Scan(&statusCarrier); e != nil {
		return j, e
	}
	if statusCarrier > 0 && decision != "reconcile_only" {
		return j, domain.ErrTransition
	}
	terminalReconcile := terminal(j.State) && decision == "reconcile_only"
	if j.State != "paused" && !(j.State == "interrupted" && decision == "reconcile_only") && !terminalReconcile {
		return j, domain.ErrTransition
	}
	var restoreHeld int
	if e = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?`, id).Scan(&restoreHeld); e != nil {
		return j, e
	}
	if restoreHeld > 0 && decision != "reconcile_only" {
		return j, domain.ErrReconciliation
	}
	rows, e := t.providerJobEffects(id)
	if e != nil {
		return j, e
	}
	unknown := j.ProviderAttempts != nil && j.ProviderAttempts.Unknown > 0
	for _, a := range rows {
		if a.State == "dispatching" || a.State == "running" || a.State == "outcome_unknown" {
			unknown = true
		}
	}
	lib, e := t.Library()
	if e != nil {
		return j, e
	}
	var activeLookup int
	if e = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM provider_reconcile_intents WHERE job_id=? AND state='executing' AND runtime_generation=?`, id, lib.RuntimeGeneration).Scan(&activeLookup); e != nil {
		return j, e
	}
	if activeLookup > 0 {
		return j, domain.ErrReconciliation
	}
	state := "queued"
	var selectedSaved string
	switch decision {
	case "reconcile_only":
		eligible := false
		for _, a := range rows {
			if a.State == "dispatching" || a.State == "running" || a.State == "outcome_unknown" {
				eligible = true
			}
		}
		if !eligible && restoreHeld == 0 && (j.ProviderAttempts == nil || j.ProviderAttempts.ReconciliationPending == 0) {
			return j, domain.ErrTransition
		}
		if terminalReconcile && !unknown && restoreHeld == 0 && (j.ProviderAttempts == nil || j.ProviderAttempts.ReconciliationPending == 0) {
			return j, domain.ErrTransition
		}
		state = "reconciling"
		if terminalReconcile {
			state = j.State
		}
		for _, a := range rows {
			if a.State == "dispatching" || a.State == "running" || a.State == "outcome_unknown" {
				if e = t.queueProviderReconciliation(id, a.ID); e != nil {
					return j, e
				}
			}
		}
		if e = t.exec(`DELETE FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?`, id); e != nil {
			return j, e
		}
	case "continue":
		if j.CancelRequested || unknown {
			return j, domain.ErrReconciliation
		}
		hasSaved := false
		for _, a := range rows {
			if a.State == "result_saved" {
				hasSaved = true
			}
		}
		for _, a := range rows {
			if !hasSaved && (a.State == "failed" || a.State == "cancelled" && a.Reason != "cancel_before_dispatch") {
				return j, domain.ErrReconciliation
			}
			if a.State == "prepared" {
				if e = t.ObserveProviderAttempt(a.ID, "cancelled", "", "", "cancel_before_dispatch"); e != nil {
					return j, e
				}
			}
		}
		for _, a := range rows {
			if a.State == "result_saved" {
				selectedSaved = a.ID
			}
		}
	case "retry":
		if j.CancelRequested {
			return j, domain.ErrTransition
		}
		for _, a := range rows {
			if a.State == "prepared" {
				if e = t.ObserveProviderAttempt(a.ID, "cancelled", "", "", "cancel_before_dispatch"); e != nil {
					return j, e
				}
			}
		}
		// An explicit retry replaces only a still-unclaimed cached continuation.
		if e = t.exec(`UPDATE provider_attempts SET application_generation=? WHERE job_id=? AND application_generation=?`, generation, id, generation+1); e != nil {
			return j, e
		}
		if e = t.exec(`INSERT INTO provider_retry_authorizations(job_id,attempt_generation) VALUES(?,?) ON CONFLICT(job_id,attempt_generation) DO NOTHING`, id, generation); e != nil {
			return j, e
		}
	default:
		return j, domain.ErrInvalidTarget
	}
	if e = t.exec(`UPDATE jobs SET state=?,revision=revision+1,error=CASE WHEN ? THEN error ELSE '' END WHERE job_id=?`, state, terminalReconcile, id); e != nil {
		return j, e
	}
	if e = t.jobUpdated(id); e != nil {
		return j, e
	}
	if selectedSaved != "" {
		if e = t.RebindProviderResult(selectedSaved); e != nil {
			return j, e
		}
	}
	if j.Kind == "maintenance" && j.ParentID != "" && (decision == "continue" || decision == "retry") {
		parent, pErr := t.Job(j.ParentID)
		if pErr == nil && parent.State == "paused" && !parent.CancelRequested {
			var parentHeld int
			_ = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?`, parent.ID).Scan(&parentHeld)
			if parentHeld == 0 {
				if e = t.exec(`UPDATE jobs SET state='queued',error='',revision=revision+1 WHERE job_id=?`, parent.ID); e != nil {
					return j, e
				}
				if e = t.jobUpdated(parent.ID); e != nil {
					return j, e
				}
			}
		}
	}

	return job(t.ctx, t.tx, id)
}
func (t *writeTx) providerUpdated(id string) error {
	if e := t.releaseProviderInputs(id); e != nil {
		return e
	}
	if e := t.exec(`UPDATE jobs SET revision=revision+1 WHERE job_id=?`, id); e != nil {
		return e
	}
	if e := t.jobUpdated(id); e != nil {
		return e
	}
	var kind, parentID string
	if e := t.tx.QueryRowContext(t.ctx, `SELECT kind,coalesce(parent_id,'') FROM jobs WHERE job_id=?`, id).Scan(&kind, &parentID); e == nil && kind == "maintenance" && parentID != "" {
		if e := t.exec(`UPDATE jobs SET revision=revision+1 WHERE job_id=?`, parentID); e != nil {
			return e
		}
		if e := t.jobUpdated(parentID); e != nil {
			return e
		}
	}
	return nil
}
