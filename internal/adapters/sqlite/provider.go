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
		a = domain.ProviderAttempt{ID: id, JobID: fact.JobID, Generation: fact.Generation, State: fact.State, RequestSHA: fact.SHA, RequestID: fact.RequestID, SessionID: fact.SessionID, ResultHash: fact.ResultHash, Reason: fact.Reason, MayHaveDispatched: fact.MayHaveDispatched == nil || *fact.MayHaveDispatched || fact.State == "dispatching" || fact.State == "running" || fact.State == "outcome_unknown"}
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
	rows, e := t.tx.QueryContext(t.ctx, `SELECT read_id,content_hash,coalesce(input_origin,''),observed_at FROM provider_inputs WHERE attempt_id=? ORDER BY read_id`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.ProviderInput{}
	for rows.Next() {
		v := domain.ProviderInput{AttemptID: id}
		if e = rows.Scan(&v.ReadID, &v.Hash, &v.ObservedOrigin, &v.ObservedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
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
		if target != a.TargetSetID || hash != v.Hash {
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
	if a.RequestID != "" && requestID != a.RequestID || a.SessionID != "" && sessionID != a.SessionID {
		return domain.ErrConflict
	}
	// Caller supplies normalized categories, never provider error prose.
	switch reason {
	case "", "transport_unknown", "provider_unavailable", "auth_required", "quota_exhausted", "cancel_confirmed", "cancel_before_dispatch", "provider_failed", "result_unavailable":
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
	if e = t.FinishJob(a.JobID, a.ApplyGeneration, "", apply); e != nil {
		return e
	}
	return t.SetJobResult(a.JobID, jsonBytes(map[string]string{"attempt_id": a.ID, "result_sha256": a.ResultHash}))
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
	if !domain.ProviderJob(j.Kind) || terminal(j.State) {
		return domain.ErrTransition
	}
	if e = t.exec(`UPDATE jobs SET state='paused',error=?,revision=revision+1 WHERE job_id=?`, reason, id); e != nil {
		return e
	}
	return t.jobUpdated(id)
}
func (t *writeTx) ResumeProviderJob(id string, generation int64, decision string) (domain.Job, error) {
	j, e := t.Job(id)
	if e != nil {
		return j, e
	}
	if j.AttemptGeneration != generation {
		return j, domain.ErrPrecondition
	}
	if !domain.ProviderJob(j.Kind) {
		return j, domain.ErrTransition
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

	return job(t.ctx, t.tx, id)
}
func (t *writeTx) providerUpdated(id string) error {
	if e := t.releaseProviderInputs(id); e != nil {
		return e
	}
	if e := t.exec(`UPDATE jobs SET revision=revision+1 WHERE job_id=?`, id); e != nil {
		return e
	}
	return t.jobUpdated(id)
}
