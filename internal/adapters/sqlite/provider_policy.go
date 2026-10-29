package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"reflect"
	"time"
)

func (t *writeTx) guardProviderDuplicate(a domain.ProviderAttempt) error {
	var blocked int
	e := t.tx.QueryRowContext(t.ctx, `WITH unresolved AS (
 SELECT a.attempt_id,a.job_id,j.cancel_requested FROM provider_attempts a JOIN jobs j ON j.job_id=a.job_id
 WHERE a.request_sha256=? AND (a.state IN ('prepared','dispatching','running','outcome_unknown') OR (a.state='failed' AND a.reason='result_unavailable') OR (a.state='result_saved' AND j.state NOT IN ('completed','failed','cancelled')))
 UNION ALL SELECT attempt_id,json_extract(value,'$.job_id'),coalesce(json_extract(value,'$.cancel_requested'),1) FROM provider_orphans WHERE request_sha256=? AND (state NOT IN ('failed','cancelled') OR json_extract(value,'$.reason')='result_unavailable')
 ) SELECT count(*) FROM unresolved u
 WHERE NOT EXISTS(SELECT 1 FROM provider_linked_retry_authorizations r WHERE r.job_id=? AND r.prior_attempt_id=u.attempt_id AND r.decision='retry')
 AND NOT (coalesce(u.job_id,'')=? AND u.cancel_requested=0 AND EXISTS(SELECT 1 FROM provider_retry_authorizations r WHERE r.job_id=? AND r.attempt_generation=?))`, a.RequestSHA, a.RequestSHA, a.JobID, a.JobID, a.JobID, a.Generation-1).Scan(&blocked)
	if e != nil {
		return e
	}
	if blocked > 0 {
		return domain.ErrReconciliation
	}
	return nil
}

// AuthorizeProviderRetry records the finite predecessor set approved by trusted
// owner admission. A ParentID or a fresh operation/job ID supplies no authority.
// Future public admission must authenticate and authorize the new semantic work
// before invoking this internal port; callers cannot supply a digest bypass.
func (t *writeTx) AuthorizeProviderRetry(jobID string, predecessors []string) error {
	j, e := t.Job(jobID)
	if e != nil {
		return e
	}
	if !domain.ProviderJob(j.Kind) || j.State != "queued" || j.CancelRequested || len(predecessors) == 0 {
		return domain.ErrTransition
	}
	var scope domain.ProviderScope
	if json.Unmarshal(j.Scope, &scope) != nil {
		return domain.ErrInvalidTarget
	}
	auth, e := t.AuthState()
	if e != nil {
		return e
	}
	if scope.PrincipalID != auth.OwnerID || scope.AuthEpoch != auth.AuthEpoch {
		return domain.ErrDenied
	}
	linked := false
	seen := map[string]bool{}
	for _, id := range predecessors {
		if id == "" || seen[id] {
			return domain.ErrInvalidTarget
		}
		seen[id] = true
		var fact providerRestoreFact
		var raw []byte
		e = t.tx.QueryRowContext(t.ctx, `SELECT value FROM provider_orphans WHERE attempt_id=?`, id).Scan(&raw)
		if e == nil {
			if e = json.Unmarshal(raw, &fact); e != nil {
				return e
			}
		} else if errors.Is(e, sql.ErrNoRows) {
			a, err := t.ProviderAttempt(id)
			if err != nil {
				return err
			}
			prior, err := t.Job(a.JobID)
			if err != nil {
				return err
			}
			var priorScope domain.ProviderScope
			if json.Unmarshal(prior.Scope, &priorScope) != nil {
				return domain.ErrInvalidTarget
			}
			fact = providerRestoreFact{ID: a.ID, SHA: a.RequestSHA, JobID: prior.ID, PrincipalID: priorScope.PrincipalID, JobState: prior.State, CancelRequested: prior.CancelRequested, Target: priorScope.Target}
		} else {
			return e
		}
		if !sameProviderTarget(fact.Target, scope.Target) {
			return domain.ErrDenied
		}
		if fact.JobID == jobID || fact.SHA != scope.RequestSHA || fact.PrincipalID != scope.PrincipalID {
			return domain.ErrDenied
		}
		if len(raw) == 0 && !fact.CancelRequested && !terminal(fact.JobState) {
			return domain.ErrTransition
		}
		var originalExists int
		if e = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM jobs WHERE job_id=?`, fact.JobID).Scan(&originalExists); e != nil {
			return e
		}
		if fact.JobID == j.ParentID || len(raw) > 0 && originalExists == 0 && j.ParentID == "" {
			linked = true
		}
		if e = t.exec(`INSERT OR IGNORE INTO provider_linked_retry_authorizations(job_id,prior_attempt_id,decision) VALUES(?,?,'retry')`, jobID, id); e != nil {
			return e
		}
	}
	if !linked {
		return domain.ErrInvalidTarget
	}
	return t.providerUpdated(jobID)
}

// Inputs retain their historical exact hashes after collection. Only execution
// pins are released, and only after all of this terminal job's effects settle.
func (t *writeTx) releaseProviderInputs(jobID string) error {
	if e := t.exec(`DELETE FROM object_pins WHERE owner_kind='provider_attempt'
 AND pin_id IN (SELECT a.attempt_id||':'||i.read_id FROM provider_attempts a JOIN provider_inputs i ON i.attempt_id=a.attempt_id WHERE a.job_id=?)
 AND EXISTS(SELECT 1 FROM jobs WHERE job_id=? AND state IN ('completed','failed','cancelled'))
 AND NOT EXISTS(SELECT 1 FROM provider_attempts WHERE job_id=? AND state IN ('prepared','dispatching','running','outcome_unknown')) AND NOT EXISTS(SELECT 1 FROM provider_orphans WHERE json_extract(value,'$.job_id')=? AND state IN ('dispatching','running','outcome_unknown'))`, jobID, jobID, jobID, jobID); e != nil {
		return e
	}
	if e := t.exec(`DELETE FROM object_pins WHERE owner_kind='job_input' AND owner_id=? AND EXISTS(SELECT 1 FROM jobs WHERE job_id=? AND state IN ('completed','failed','cancelled')) AND NOT EXISTS(SELECT 1 FROM provider_attempts WHERE job_id=? AND state IN ('prepared','dispatching','running','outcome_unknown')) AND NOT EXISTS(SELECT 1 FROM provider_orphans WHERE json_extract(value,'$.job_id')=? AND state IN ('dispatching','running','outcome_unknown'))`, jobID, jobID, jobID, jobID); e != nil {
		return e
	}
	rows, e := t.tx.QueryContext(t.ctx, `SELECT value FROM provider_orphans WHERE json_extract(value,'$.job_id')=?`, jobID)
	if e != nil {
		return e
	}
	var facts []providerRestoreFact
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		var fact providerRestoreFact
		if e = json.Unmarshal(raw, &fact); e != nil {
			rows.Close()
			return e
		}
		facts = append(facts, fact)
	}
	e = errors.Join(rows.Err(), rows.Close())
	if e != nil {
		return e
	}
	for _, fact := range facts {
		if e = t.releaseResolvedOrphanInputs(fact); e != nil {
			return e
		}
	}
	return nil
}

func (t *writeTx) releaseResolvedOrphanInputs(fact providerRestoreFact) error {
	if len(fact.Conflict) > 0 || !(providerExternalTerminal(fact) || providerLocalTerminal(fact)) {
		return nil
	}
	var live int
	if e := t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM jobs WHERE job_id=? AND state NOT IN ('completed','failed','cancelled')`, fact.JobID).Scan(&live); e != nil {
		return e
	}
	if live > 0 {
		return nil
	}
	if !providerResolutionSettled(fact) {
		var raw []byte
		err := t.tx.QueryRowContext(t.ctx, providerAttemptInventory+` WHERE a.attempt_id=? AND j.job_id=? AND j.state IN ('completed','failed','cancelled')`, fact.ID, fact.JobID).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var actual providerRestoreFact
		if err = json.Unmarshal(raw, &actual); err != nil {
			return err
		}
		if !(providerExternalTerminal(actual) || providerLocalTerminal(actual)) || actual.RequestID != fact.RequestID || actual.SessionID != fact.SessionID {
			return nil
		}
	}
	for _, input := range fact.Inputs {
		if e := t.exec(`DELETE FROM object_pins WHERE pin_id=? AND owner_kind='provider_orphan' AND owner_id=? AND content_hash=?`, "provider-orphan:"+fact.ID+":"+input.ReadID+":"+input.Hash, fact.ID, input.Hash); e != nil {
			return e
		}
	}
	return nil
}

func (t *writeTx) providerBlocksPurge(document string) (bool, error) {
	var count int
	e := t.tx.QueryRowContext(t.ctx, `WITH versions AS(SELECT version_id FROM purged_versions WHERE document_id=?), source_jobs AS(SELECT i.job_id FROM job_inputs i JOIN source_capture_reads r ON r.read_id=i.read_id JOIN source_captures c ON c.capture_id=r.capture_id WHERE json_extract(c.requested_selection,'$.version_id') IN versions)
 SELECT (SELECT count(*) FROM provider_attempts a JOIN jobs j ON j.job_id=a.job_id WHERE a.state IN('dispatching','running','outcome_unknown') AND(json_extract(j.scope,'$.target.document_id')=? OR json_extract(j.scope,'$.target.version_id') IN versions OR j.job_id IN source_jobs))+
 (SELECT count(*) FROM provider_orphans o WHERE state IN('dispatching','running','outcome_unknown') AND(json_extract(value,'$.target.document_id')=? OR json_extract(value,'$.target.version_id') IN versions OR EXISTS(SELECT 1 FROM json_tree(o.value) n WHERE n.type='object' AND json_extract(n.value,'$.source_version_id') IN versions)))`, document, document, document).Scan(&count)
	return count > 0, e
}

func (t *writeTx) NextProviderReconciliation(jobID string) (string, error) {
	var id string
	e := t.tx.QueryRowContext(t.ctx, `SELECT attempt_id FROM provider_reconcile_intents WHERE job_id=? AND state='pending' ORDER BY attempt_id LIMIT 1`, jobID).Scan(&id)
	return id, notFound(e)
}
func (t *writeTx) ClaimProviderReconciliation(jobID, id string) error {
	hold, e := t.RecoveryHold()
	if e != nil {
		return e
	}
	if hold != nil && hold.State != "reviewed" {
		return domain.ErrReconciliation
	}
	var blocked int
	if e = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM resource_revisions WHERE resource_kind='restore_job_hold' AND resource_id=?`, jobID).Scan(&blocked); e != nil {
		return e
	}
	if blocked > 0 {
		return domain.ErrReconciliation
	}

	var state string
	if e := t.tx.QueryRowContext(t.ctx, `SELECT state FROM provider_reconcile_intents WHERE job_id=? AND attempt_id=?`, jobID, id).Scan(&state); e != nil {
		return notFound(e)
	}
	if state != "pending" {
		return domain.ErrTransition
	}
	lib, e := t.Library()
	if e != nil {
		return e
	}
	if e = t.exec(`UPDATE provider_reconcile_intents SET state='executing',runtime_generation=? WHERE job_id=? AND attempt_id=?`, lib.RuntimeGeneration, jobID, id); e != nil {
		return e
	}
	return t.providerUpdated(jobID)
}
func (t *writeTx) CompleteProviderReconciliation(jobID, id string) error {
	result, e := t.tx.ExecContext(t.ctx, `DELETE FROM provider_reconcile_intents WHERE job_id=? AND attempt_id=? AND state='executing'`, jobID, id)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil || n == 0 {
		return e
	}
	if e = t.finishProviderStatusCarrier(jobID); e != nil {
		return e
	}
	return t.providerUpdated(jobID)
}

// A carrier completes only its admitted operational status work. It never
// completes the historical source job, produces an answer or applies output.
func (t *writeTx) finishProviderStatusCarrier(jobID string) error {
	j, e := t.Job(jobID)
	if e != nil {
		return e
	}
	if terminal(j.State) || j.CancelRequested {
		return nil
	}
	var remaining int
	if e = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM provider_reconcile_intents WHERE job_id=?`, jobID).Scan(&remaining); e != nil {
		return e
	}
	if remaining > 0 {
		return nil
	}
	rows, e := t.tx.QueryContext(t.ctx, `SELECT o.value FROM provider_linked_retry_authorizations r JOIN provider_orphans o ON o.attempt_id=r.prior_attempt_id WHERE r.job_id=? AND r.decision='reconcile_only' ORDER BY o.attempt_id`, jobID)
	if e != nil {
		return e
	}
	var facts []providerRestoreFact
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		var fact providerRestoreFact
		if e = json.Unmarshal(raw, &fact); e != nil {
			rows.Close()
			return e
		}
		facts = append(facts, fact)
	}
	e = errors.Join(rows.Err(), rows.Close())
	if e != nil {
		return e
	}
	if len(facts) == 0 {
		return nil
	}
	var scope domain.ProviderScope
	if json.Unmarshal(j.Scope, &scope) != nil {
		return domain.ErrInvalidTarget
	}
	for _, fact := range facts {
		if fact.State != "failed" && fact.State != "cancelled" && fact.State != "result_saved" || len(fact.Conflict) > 0 {
			return nil
		}
		var unknown int
		if e = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM provider_attempts WHERE attempt_id=? AND state IN ('dispatching','running','outcome_unknown')`, fact.ID).Scan(&unknown); e != nil {
			return e
		}
		if unknown > 0 {
			return nil
		}
		if fact.JobID == jobID || fact.PrincipalID != scope.PrincipalID || fact.SHA != scope.RequestSHA || !sameProviderTarget(fact.Target, scope.Target) {
			return domain.ErrDenied
		}
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, fact := range facts {
		proof := providerResolution{JobID: jobID, State: "completed", Kind: "provider_reconciliation", PrincipalID: scope.PrincipalID, RequestSHA: scope.RequestSHA, AttemptID: fact.ID, Outcome: fact.State, Reason: fact.Reason, RequestID: fact.RequestID, SessionID: fact.SessionID, CompletedAt: stamp}
		if e = t.exec(`UPDATE provider_orphans SET value=json_set(value,'$.resolution_job_id',?,'$.resolution',json(?)) WHERE attempt_id=?`, jobID, jsonBytes(proof), fact.ID); e != nil {
			return e
		}
		fact.ResolutionJobID = jobID
		fact.Resolution = &proof
		if e = t.releaseResolvedOrphanInputs(fact); e != nil {
			return e
		}
	}
	if e = t.SetJobResult(jobID, jsonBytes(map[string]any{"kind": "provider_reconciliation", "state": "completed", "resolved_count": len(facts)})); e != nil {
		return e
	}
	return t.exec(`UPDATE jobs SET state='completed',error='',revision=revision+1 WHERE job_id=?`, jobID)
}

// AuthorizeProviderReconciliation is trusted owner admission for a finite set of
// orphan effects. It grants status-only lookup, never new dispatch or retry.
func (t *writeTx) AuthorizeProviderReconciliation(jobID string, ids []string) error {
	j, e := t.Job(jobID)
	if e != nil {
		return e
	}
	if !domain.ProviderJob(j.Kind) || j.CancelRequested || j.State != "queued" || j.AttemptGeneration != 1 || j.ParentID != "" || len(j.Result) > 0 || len(ids) == 0 {
		return domain.ErrTransition
	}
	var scope domain.ProviderScope
	if json.Unmarshal(j.Scope, &scope) != nil {
		return domain.ErrInvalidTarget
	}
	if scope.TargetSetID != "" || len(scope.ReadIDs) > 0 {
		return domain.ErrTransition
	}
	var owned int
	if e = t.tx.QueryRowContext(t.ctx, `SELECT (SELECT count(*) FROM provider_attempts WHERE job_id=?)+(SELECT count(*) FROM provider_linked_retry_authorizations WHERE job_id=?)+(SELECT count(*) FROM provider_retry_authorizations WHERE job_id=?)`, jobID, jobID, jobID).Scan(&owned); e != nil {
		return e
	}
	if owned > 0 {
		return domain.ErrTransition
	}

	auth, e := t.AuthState()
	if e != nil {
		return e
	}
	if scope.PrincipalID != auth.OwnerID || scope.AuthEpoch != auth.AuthEpoch {
		return domain.ErrDenied
	}
	hold, e := t.RecoveryHold()
	if e != nil {
		return e
	}
	if hold != nil && hold.State != "reviewed" {
		return domain.ErrReconciliation
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] || id == "" {
			return domain.ErrInvalidTarget
		}
		seen[id] = true
		var raw []byte
		if e = t.tx.QueryRowContext(t.ctx, `SELECT value FROM provider_orphans WHERE attempt_id=?`, id).Scan(&raw); e != nil {
			return notFound(e)
		}
		var fact providerRestoreFact
		if e = json.Unmarshal(raw, &fact); e != nil {
			return e
		}
		if fact.PrincipalID != scope.PrincipalID || fact.SHA != scope.RequestSHA || !sameProviderTarget(fact.Target, scope.Target) {
			return domain.ErrDenied
		}
		if e = t.exec(`INSERT OR IGNORE INTO provider_linked_retry_authorizations(job_id,prior_attempt_id,decision) VALUES(?,?,'reconcile_only')`, jobID, id); e != nil {
			return e
		}
		if e = t.queueProviderReconciliation(jobID, id); e != nil {
			return e
		}
	}
	if e = t.exec(`UPDATE jobs SET state='reconciling',revision=revision+1 WHERE job_id=?`, jobID); e != nil {
		return e
	}
	return t.providerUpdated(jobID)
}

func sameProviderTarget(a, b json.RawMessage) bool {
	var left, right any
	if len(a) == 0 || len(b) == 0 || json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

func (t *writeTx) queueProviderReconciliation(jobID, id string) error {
	return t.exec(`INSERT INTO provider_reconcile_intents(job_id,attempt_id,ledger_attempt_id,orphan_attempt_id,state,runtime_generation)
 SELECT ?,?,CASE WHEN EXISTS(SELECT 1 FROM provider_attempts WHERE attempt_id=?) THEN ? END,CASE WHEN NOT EXISTS(SELECT 1 FROM provider_attempts WHERE attempt_id=?) THEN ? END,'pending',''
 ON CONFLICT(job_id,attempt_id) DO UPDATE SET state='pending',runtime_generation=''`, jobID, id, id, id, id, id)
}

// Global maintenance budgets do not apply to explicit owner generation/questions.
// Captured config remains auditable; current project restrictions are checked by
// GuardProvider. Ordered admission owners apply their own actual provider rules.
func (t *writeTx) CheckProviderPolicy(a domain.ProviderAttempt) error {
	var statusCarrier int
	if e := t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM provider_linked_retry_authorizations WHERE job_id=? AND decision='reconcile_only'`, a.JobID).Scan(&statusCarrier); e != nil {
		return e
	}
	if statusCarrier > 0 {
		return domain.ErrTransition
	}
	j, e := t.Job(a.JobID)
	if e != nil {
		return e
	}
	var scope domain.ProviderScope
	if json.Unmarshal(j.Scope, &scope) != nil {
		return domain.ErrInvalidTarget
	}
	for _, readID := range scope.ReadIDs {
		var sourceID, project, state, availability, sourceVersion string
		e := t.tx.QueryRowContext(t.ctx, `SELECT r.source_id,c.project_id,c.state,r.availability,coalesce(json_extract(c.requested_selection,'$.version_id'),'') FROM source_capture_reads r JOIN source_captures c ON c.capture_id=r.capture_id WHERE r.read_id=?`, readID).Scan(&sourceID, &project, &state, &availability, &sourceVersion)
		if errors.Is(e, sql.ErrNoRows) {
			if scope.ProviderID != "" {
				return domain.ErrDenied
			}
			continue
		}
		if e != nil {
			return e
		}
		if scope.ProviderID == "" || state != "completed" || availability != "available" {
			return domain.ErrDenied
		}
		if sourceVersion != "" {
			target, _ := json.Marshal(map[string]string{"project_id": project, "version_id": sourceVersion})
			if e = app.AuthorizeTarget(t, target); e != nil {
				return e
			}
		}
		if _, e = app.ManagedSourcePolicy(t, project, sourceID, scope.ProviderID); e != nil {
			return e
		}
	}
	var revision int64
	e = t.tx.QueryRowContext(t.ctx, `SELECT config_revision FROM config_revisions WHERE config_revision=?`, j.ConfigRevision).Scan(&revision)
	return notFound(e)
}

func (t *writeTx) providerJobEffects(jobID string) ([]domain.ProviderAttempt, error) {
	rows, e := t.ProviderAttempts(jobID)
	if e != nil {
		return nil, e
	}
	known := map[string]bool{}
	for _, a := range rows {
		known[a.ID] = true
	}
	q, e := t.tx.QueryContext(t.ctx, `SELECT attempt_id FROM provider_orphans WHERE json_extract(value,'$.job_id')=? OR attempt_id IN(SELECT prior_attempt_id FROM provider_linked_retry_authorizations WHERE job_id=? AND decision='reconcile_only')`, jobID, jobID)
	if e != nil {
		return nil, e
	}
	var ids []string
	for q.Next() {
		var id string
		if e = q.Scan(&id); e != nil {
			q.Close()
			return nil, e
		}
		if !known[id] {
			ids = append(ids, id)
		}
	}
	e = errors.Join(q.Err(), q.Close())
	if e != nil {
		return nil, e
	}
	for _, id := range ids {
		a, e := t.ProviderAttempt(id)
		if e != nil {
			return nil, e
		}
		rows = append(rows, a)
	}
	return rows, nil
}
