package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"reflect"
	"strings"
)

const providerTransportMigration = `
CREATE TABLE provider_transport(attempt_id TEXT PRIMARY KEY REFERENCES provider_attempts(attempt_id),value BLOB NOT NULL CHECK(json_valid(value)));
`

func (s *Store) validateProviderTransportDeclarations(ctx context.Context) error {
	var actual string
	if e := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='provider_transport'`).Scan(&actual); e != nil {
		return errors.New("missing provider transport schema declaration")
	}
	normalize := func(v string) string { return strings.TrimSuffix(strings.Join(strings.Fields(v), " "), ";") }
	if normalize(actual) != normalize(providerTransportMigration) {
		return errors.New("changed provider transport schema declaration")
	}
	return nil
}
func (t *writeTx) RecordProviderTransport(id string, v domain.ProviderTransport) error {
	a, e := t.ProviderAttempt(id)
	if e != nil {
		return e
	}
	if v.Provider != "codex" || v.Version != "0.164.0" || len(v.BinarySHA) != 64 || v.PreparedAt == "" || v.AuthMode != "chatgpt" || v.Approval != "never" {
		return domain.ErrInvalidTarget
	}
	if e = t.validateGenerationTransport(a, v); e != nil {
		return e
	}
	if e = t.validateQuestionTransport(a, v); e != nil {
		return e
	}
	inputs, e := t.ProviderInputs(id)
	if e != nil {
		return e
	}
	if len(inputs) != len(v.Disclosure) {
		return domain.ErrInvalidTarget
	}
	byRead := map[string]domain.ProviderDisclosure{}
	for _, item := range v.Disclosure {
		if _, duplicate := byRead[item.ReadID]; duplicate {
			return domain.ErrInvalidTarget
		}
		byRead[item.ReadID] = item
	}
	for _, input := range inputs {
		item, ok := byRead[input.ReadID]
		if !ok || item.Hash != input.Hash {
			return domain.ErrInvalidTarget
		}
		read, e := t.CapturedRead(input.ReadID)
		if e != nil {
			return e
		}
		if string(read.SourceJSON) != string(item.Source) || string(read.LocationJSON) != string(item.Location) {
			return domain.ErrInvalidTarget
		}
		object, e := t.Object(input.Hash)
		if e != nil {
			return e
		}
		if object.Size != item.Size {
			return domain.ErrInvalidTarget
		}
	}
	if a.Transport != nil {
		old := *a.Transport
		before, after := old, v
		if old.Generation != nil || v.Generation != nil {
			merged, conflict := mergeGenerationTransport(old.Generation, v.Generation)
			if conflict || !reflect.DeepEqual(merged, v.Generation) {
				return domain.ErrTransition
			}
			before.Generation, after.Generation = merged, merged
		}
		if old.Question != nil || v.Question != nil {
			if !questionTransportAdvance(old.Question, v.Question) {
				return domain.ErrTransition
			}
			before.Question, after.Question = v.Question, v.Question
		}
		if before.FrameSHA == "" {
			after.FrameSHA = ""
		}
		before.AcceptedAt = ""
		after.AcceptedAt = ""
		before.FinishedAt = ""
		after.FinishedAt = ""
		if old.Outcome != "" && v.Outcome != old.Outcome {
			return domain.ErrTransition
		}
		before.DriverOutcome = ""
		after.DriverOutcome = ""
		before.BackendSource = ""
		after.BackendSource = ""
		before.CleanupState = ""
		after.CleanupState = ""
		before.CleanupReason = ""
		after.CleanupReason = ""
		before.BackendState = ""
		after.BackendState = ""
		before.LocalClosed = false
		after.LocalClosed = false
		x, _ := json.Marshal(before)
		y, _ := json.Marshal(after)
		if string(x) != string(y) || old.AcceptedAt != "" && old.AcceptedAt != v.AcceptedAt || old.FinishedAt != "" && old.FinishedAt != v.FinishedAt || old.LocalClosed && !v.LocalClosed {
			return domain.ErrTransition
		}

	} else if e = app.GuardProvider(t, a); e != nil {
		return e
	}
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = t.tx.ExecContext(t.ctx, `INSERT INTO provider_transport(attempt_id,value) VALUES(?,?) ON CONFLICT(attempt_id) DO UPDATE SET value=excluded.value`, id, raw)
	return e
}

// Every source/output read rechecks the current executing owner, policy and
// runtime. Status-only carriers never gain source or output authority.
func (t *writeTx) GuardProviderPayload(jobID, attemptID string, reconcile bool) error {
	a, e := t.ProviderAttempt(attemptID)
	if e != nil {
		return e
	}
	if a.JobID != jobID || a.Runtime == "" {
		return domain.ErrDenied
	}
	if !reconcile {
		return app.GuardProvider(t, a)
	}
	var runtime string
	e = t.tx.QueryRowContext(t.ctx, `SELECT runtime_generation FROM provider_reconcile_intents WHERE job_id=? AND attempt_id=? AND state='executing'`, jobID, attemptID).Scan(&runtime)
	if errors.Is(e, sql.ErrNoRows) {
		return domain.ErrDenied
	}
	if e != nil {
		return e
	}
	library, e := t.Library()
	if e != nil {
		return e
	}
	if library.RuntimeGeneration != runtime {
		return domain.ErrStaleWorker
	}
	j, e := t.Job(jobID)
	if e != nil {
		return e
	}
	if j.CancelRequested || j.State == "completed" || j.State == "failed" || j.State == "cancelled" {
		return domain.ErrDenied
	}
	// The intent owns this runtime, but the original immutable attempt identity
	// and captured permission revisions still govern payload access.
	a.Runtime = runtime
	return app.GuardProviderReconciliation(t, j, a)
}

type providerReconciliationAuthority struct {
	AuthEpoch string `json:"auth_epoch"`
}

func (t *writeTx) GuardProviderLookup(jobID, attemptID string) error {
	var runtime string
	if e := t.tx.QueryRowContext(t.ctx, `SELECT runtime_generation FROM provider_reconcile_intents WHERE job_id=? AND attempt_id=? AND state='executing'`, jobID, attemptID).Scan(&runtime); e != nil {
		return notFound(e)
	}
	library, e := t.Library()
	if e != nil {
		return e
	}
	if library.RuntimeGeneration != runtime {
		return domain.ErrStaleWorker
	}
	j, e := t.Job(jobID)
	if e != nil {
		return e
	}
	var scope domain.ProviderScope
	if json.Unmarshal(j.Scope, &scope) != nil {
		return domain.ErrDenied
	}
	state, e := t.AuthState()
	if e != nil {
		return e
	}
	if scope.PrincipalID != state.OwnerID {
		if j.Kind != "maintenance" {
			return domain.ErrDenied
		}
		var raw []byte
		if e = t.tx.QueryRowContext(t.ctx, `SELECT value FROM resource_revisions WHERE resource_kind='provider_reconciliation_authority' AND resource_id=?`, jobID).Scan(&raw); e != nil {
			p, pErr := t.WorkflowPrincipal(scope.PrincipalID)
			if pErr != nil || !p.Active {
				return domain.ErrDenied
			}
		}
	}
	// Original producing authority remains immutable. A current explicit finite
	// status grant can account an older effect after protected owner recovery.
	if scope.AuthEpoch != state.AuthEpoch {
		var raw []byte
		if e = t.tx.QueryRowContext(t.ctx, `SELECT value FROM resource_revisions WHERE resource_kind='provider_reconciliation_authority' AND resource_id=?`, jobID).Scan(&raw); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return domain.ErrDenied
			}
			return e
		}
		var grant providerReconciliationAuthority
		if json.Unmarshal(raw, &grant) != nil || grant.AuthEpoch != state.AuthEpoch {
			return domain.ErrDenied
		}
	}
	return nil
}

func (t *writeTx) RecordProviderLookup(id, carrier string, observation domain.ProviderLookup) error {
	if e := t.GuardProviderLookup(carrier, id); e != nil {
		return e
	}
	switch observation.BackendState {
	case "completed", "interrupted", "failed", "inProgress":
	default:
		return domain.ErrInvalidTarget
	}
	if observation.ObservedAt == "" {
		return domain.ErrInvalidTarget
	}
	a, e := t.ProviderAttempt(id)
	if e != nil {
		return e
	}
	if a.Transport == nil {
		return domain.ErrReconciliation
	}
	if observation.Source != "" && observation.Source != "history" && observation.Source != "live_event" {
		return domain.ErrInvalidTarget
	}
	if previous := a.Transport.LastLookup; previous != nil && reliableLookup(*previous) {
		if reliableLookup(observation) && previous.BackendState != observation.BackendState {
			return domain.ErrConflict
		}
		if !reliableLookup(observation) {
			return nil
		}
	}
	a.Transport.LastLookup = &observation
	raw, e := json.Marshal(a.Transport)
	if e != nil {
		return e
	}
	if e = t.exec(`UPDATE provider_transport SET value=? WHERE attempt_id=?`, raw, id); e != nil {
		return e
	}
	return t.exec(`UPDATE provider_orphans SET value=json_set(value,'$.transport',json(?)) WHERE attempt_id=?`, raw, id)
}

func reliableLookup(v domain.ProviderLookup) bool {
	return v.BackendState == "completed" || v.BackendState == "failed" || v.BackendState == "interrupted" && v.Source == "live_event"
}
