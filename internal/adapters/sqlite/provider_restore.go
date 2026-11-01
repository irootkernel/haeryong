package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"sort"
	"time"
)

// RetainProviderOrphans carries the destination's pre-restore external facts into
// the protected older snapshot. available inspects only requested objects in the
// caller's freshly rebuilt, verified staging tree. Ready metadata is insufficient.
// Missing source/result bytes remain metadata-only and gain no new ownership.
func RetainProviderOrphans(ctx context.Context, path string, input io.Reader, available func(domain.Object) (bool, error)) error {
	db, e := openCopy(path)
	if e != nil {
		return e
	}
	defer db.Close()
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	verifiedAvailable := func(hash string) (bool, error) {
		if available == nil {
			return false, nil
		}
		object := domain.Object{Hash: hash}
		e := tx.QueryRowContext(ctx, `SELECT byte_size,state FROM content_objects WHERE content_hash=?`, hash).Scan(&object.Size, &object.State)
		if errors.Is(e, sql.ErrNoRows) {
			return false, nil
		}
		if e != nil {
			return false, e
		}
		if object.State != "ready" {
			return false, nil
		}
		return available(object)
	}
	retainPin := func(attemptID, pinID, hash string) error {
		var existing int
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM object_pins WHERE pin_id=? AND owner_kind='provider_orphan' AND owner_id=? AND content_hash=?`, pinID, attemptID, hash).Scan(&existing); e != nil {
			return e
		}
		if existing > 0 && available == nil {
			return nil
		}
		ok, e := verifiedAvailable(hash)
		if e != nil {
			return e
		}
		if !ok {
			if existing > 0 {
				return domain.ErrContentIntegrity
			}
			return nil
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO object_pins(pin_id,owner_kind,owner_id,content_hash) SELECT ?,'provider_orphan',?,content_hash FROM content_objects WHERE content_hash=? AND state='ready' ON CONFLICT(pin_id) DO NOTHING`, pinID, attemptID, hash)
		return e
	}
	d := json.NewDecoder(input)
	for {
		var raw json.RawMessage
		e = d.Decode(&raw)
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return e
		}
		var identity struct {
			ID string `json:"attempt_id"`
		}
		if e = json.Unmarshal(raw, &identity); e != nil {
			return e
		}
		if identity.ID == "" {
			continue
		}
		raw, e = normalizeProviderRestoreFact(raw)
		if e != nil {
			return e
		}
		var a providerRestoreFact
		if e = json.Unmarshal(raw, &a); e != nil {
			return e
		}
		if len(a.SHA) != 64 {
			return domain.ErrInvalidTarget
		}
		for _, input := range a.Inputs {
			if e = validateProviderObservation(input); e != nil {
				return e
			}
		}
		var previous []byte
		err := tx.QueryRowContext(ctx, `SELECT value FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&previous)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.QueryRowContext(ctx, providerAttemptInventory+` WHERE a.attempt_id=?`, a.ID).Scan(&previous)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			previous, e = normalizeProviderRestoreFact(previous)
			if e != nil {
				return e
			}
		}
		if err == nil {
			var old, now map[string]json.RawMessage
			if e = json.Unmarshal(previous, &old); e != nil {
				return e
			}
			if e = json.Unmarshal(raw, &now); e != nil {
				return e
			}
			var oldSHA, oldState string
			_ = json.Unmarshal(old["request_sha256"], &oldSHA)
			_ = json.Unmarshal(old["state"], &oldState)
			if oldSHA != a.SHA {
				return domain.ErrConflict
			}
			var prior, current providerRestoreFact
			if e = json.Unmarshal(previous, &prior); e != nil {
				return e
			}
			if e = json.Unmarshal(raw, &current); e != nil {
				return e
			}
			var oldReason, newReason string
			_ = json.Unmarshal(old["reason"], &oldReason)
			_ = json.Unmarshal(now["reason"], &newReason)
			completed := func(state, reason string) bool {
				return state == "result_saved" || state == "failed" && reason == "result_unavailable" || state == "outcome_unknown" && reason == "result_retrieval_pending"
			}
			bothComplete := completed(oldState, oldReason) && completed(a.State, newReason)
			completeRank := func(state string) int {
				if state == "result_saved" {
					return 3
				}
				if state == "failed" {
					return 2
				}
				return 1
			}

			incoming := now
			if (oldState == "dispatching" || oldState == "running" || oldState == "outcome_unknown") && providerLocalTerminal(current) || providerExternalTerminal(prior) && !providerExternalTerminal(current) || providerLocalTerminal(prior) && current.State == "prepared" || bothComplete && completeRank(oldState) >= completeRank(a.State) {
				now = make(map[string]json.RawMessage, len(old))
				for key, value := range old {
					now[key] = value
				}
				a.State = oldState
			} else if providerExternalTerminal(prior) && providerExternalTerminal(current) && (oldState != a.State && !bothComplete || oldState == "failed" && a.State == "failed" && oldReason != "" && newReason != "" && completed(oldState, oldReason) != completed(a.State, newReason)) {
				now["conflicting_prior_fact"] = previous
				now["state"] = json.RawMessage(`"outcome_unknown"`)
				a.State = "outcome_unknown"
			}
			for _, key := range []string{"provider_request_id", "provider_session_id", "result_hash"} {
				present := func(v json.RawMessage) bool {
					return len(v) > 0 && !bytes.Equal(v, []byte(`""`)) && !bytes.Equal(v, []byte("null"))
				}
				if present(incoming[key]) && present(old[key]) && !bytes.Equal(incoming[key], old[key]) {
					now[key] = incoming[key]
					now["conflicting_prior_fact"] = previous
					now["state"] = json.RawMessage(`"outcome_unknown"`)
					a.State = "outcome_unknown"
				}
				if present(incoming[key]) && !present(now[key]) {
					now[key] = incoming[key]
				}
				if len(now[key]) == 0 || bytes.Equal(now[key], []byte(`""`)) || bytes.Equal(now[key], []byte("null")) {
					if len(old[key]) > 0 {
						now[key] = old[key]
					}
				}
			}
			for _, key := range []string{"job_id", "principal_id", "target"} {
				if len(now[key]) == 0 || bytes.Equal(now[key], []byte(`""`)) || bytes.Equal(now[key], []byte("null")) {
					now[key] = old[key]
				} else if len(old[key]) > 0 && !bytes.Equal(old[key], []byte(`""`)) && !bytes.Equal(old[key], []byte("null")) && !bytes.Equal(old[key], now[key]) {
					return domain.ErrConflict
				}
			}
			transport, conflict, e := mergeProviderTransport(prior.Transport, current.Transport)
			if e != nil {
				return e
			}
			if transport != nil {
				now["transport"], _ = json.Marshal(transport)
			}
			if conflict {
				now["conflicting_prior_fact"] = previous
				now["state"] = json.RawMessage(`"outcome_unknown"`)
				a.State = "outcome_unknown"
			}
			var mergedState string
			_ = json.Unmarshal(now["state"], &mergedState)
			if mergedState == oldState && (len(now["reason"]) == 0 || bytes.Equal(now["reason"], []byte(`""`)) || bytes.Equal(now["reason"], []byte("null"))) {
				now["reason"] = old["reason"]
			}

			if prior.MayHaveDispatched != nil && *prior.MayHaveDispatched || current.MayHaveDispatched != nil && *current.MayHaveDispatched {
				now["may_have_dispatched"] = json.RawMessage("true")
			} else if prior.MayHaveDispatched != nil && current.MayHaveDispatched != nil {
				now["may_have_dispatched"] = json.RawMessage("false")
			} else {
				delete(now, "may_have_dispatched")
			}
			if prior.CancelRequested || current.CancelRequested {
				now["cancel_requested"] = json.RawMessage("true")
			}
			if terminal(current.JobState) {
				now["job_state"], _ = json.Marshal(current.JobState)
			} else if terminal(prior.JobState) {
				now["job_state"], _ = json.Marshal(prior.JobState)
			}
			inputs := map[string]domain.ProviderInput{}
			for _, input := range append(prior.Inputs, current.Inputs...) {
				if e = validateProviderObservation(input); e != nil {
					return e
				}
				key := input.ReadID + ":" + input.Hash
				oldInput, exists := inputs[key]
				dependencies := mergeSourceVersions(oldInput, input)
				if exists && oldInput.ObservedOrigin != "" {
					if input.ObservedOrigin == "" {
						input = oldInput
					} else if input.ObservedOrigin != oldInput.ObservedOrigin {
						// Preserve both actual categories in explicit conflict facts.
						now["conflicting_prior_fact"] = previous
						now["state"] = json.RawMessage(`"outcome_unknown"`)
						a.State = "outcome_unknown"
					} else if oldInput.ObservedAt == nil {
						input = oldInput
					} else if oldInput.ObservedAt != nil {
						oldTime, _ := time.Parse(time.RFC3339Nano, *oldInput.ObservedAt)
						var currentTime time.Time
						if input.ObservedAt != nil {
							currentTime, _ = time.Parse(time.RFC3339Nano, *input.ObservedAt)
						}
						if input.ObservedAt == nil || !currentTime.Before(oldTime) {
							input = oldInput
						}
					}
				}
				// Observation choice must not drop an independently retained owner.
				input.SourceVersionIDs = dependencies
				inputs[key] = input
			}
			keys := make([]string, 0, len(inputs))
			for key := range inputs {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			merged := []domain.ProviderInput{}
			for _, key := range keys {
				merged = append(merged, inputs[key])
			}
			now["inputs"], e = json.Marshal(merged)
			if e != nil {
				return e
			}

			// Keep actual terminal resolver provenance even if an older inventory
			// lacks the carrier row. A bare carrier ID is never terminal proof.
			if current.Resolution != nil && providerResolutionSettled(current) {
				now["resolution"] = incoming["resolution"]
				now["resolution_job_id"] = incoming["resolution_job_id"]
			} else if prior.Resolution != nil {
				now["resolution"] = old["resolution"]
				now["resolution_job_id"] = old["resolution_job_id"]
			}

			now, e = preserveProviderConflicts(previous, raw, now)
			if e != nil {
				return e
			}
			raw, e = json.Marshal(now)
			if e != nil {
				return e
			}
		}
		// A retained conflict stays unknown even on its first import.
		if err != nil {
			var fields map[string]json.RawMessage
			if e = json.Unmarshal(raw, &fields); e != nil {
				return e
			}
			fields, e = preserveProviderConflicts(nil, raw, fields)
			if e != nil {
				return e
			}
			raw, e = json.Marshal(fields)
			if e != nil {
				return e
			}
		}
		if e = json.Unmarshal(raw, &a); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO provider_orphans(attempt_id,request_sha256,state,value) VALUES(?,?,?,?) ON CONFLICT(attempt_id) DO UPDATE SET state=excluded.state,value=excluded.value`, a.ID, a.SHA, a.State, []byte(raw)); e != nil {
			return e
		}
		if e = json.Unmarshal(raw, &a); e != nil {
			return e
		}
		if a.State == "dispatching" || a.State == "running" || a.State == "outcome_unknown" {
			if _, e = tx.ExecContext(ctx, `UPDATE provider_attempts SET state='outcome_unknown',reason='transport_unknown',may_have_dispatched=1 WHERE attempt_id=? AND (state='prepared' OR (state='failed' AND may_have_dispatched=0) OR (state='cancelled' AND reason='cancel_before_dispatch'))`, a.ID); e != nil {
				return e
			}
		}
		if a.State == "outcome_unknown" && a.Reason == "result_retrieval_pending" && !hasProviderConflict(a.Conflict) {
			if _, e = tx.ExecContext(ctx, `UPDATE provider_attempts SET state='outcome_unknown',reason='result_retrieval_pending',may_have_dispatched=1 WHERE attempt_id=? AND state IN ('prepared','dispatching','running','outcome_unknown')`, a.ID); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, `UPDATE provider_attempts SET provider_request_id=CASE WHEN provider_request_id='' THEN ? ELSE provider_request_id END,provider_session_id=CASE WHEN provider_session_id='' THEN ? ELSE provider_session_id END WHERE attempt_id=?`, a.RequestID, a.SessionID, a.ID); e != nil {
			return e
		}
		if a.State == "result_saved" {
			usable, e := verifiedAvailable(a.ResultHash)
			if e != nil {
				return e
			}
			localState, reason := "failed", "result_unavailable"
			if usable {
				localState, reason = "result_saved", ""
			}
			if _, e = tx.ExecContext(ctx, `UPDATE provider_attempts SET state=?,reason=?,result_hash=? WHERE attempt_id=? AND (state IN ('prepared','dispatching','running','outcome_unknown') OR state='cancelled' AND reason='cancel_before_dispatch')`, localState, reason, a.ResultHash, a.ID); e != nil {
				return e
			}
			if usable {
				if _, e = tx.ExecContext(ctx, `INSERT INTO object_pins(pin_id,owner_kind,owner_id,content_hash) SELECT attempt_id||':result','provider_attempt',attempt_id,? FROM provider_attempts WHERE attempt_id=? ON CONFLICT(pin_id) DO NOTHING`, a.ResultHash, a.ID); e != nil {
					return e
				}
			}
		}
		if a.State != "prepared" && (a.MayHaveDispatched == nil || *a.MayHaveDispatched || providerExternalTerminal(a)) {
			if _, e = tx.ExecContext(ctx, `UPDATE provider_attempts SET may_have_dispatched=1 WHERE attempt_id=?`, a.ID); e != nil {
				return e
			}
		}
		// Explicit no-call evidence settles only an older un-crossed local intent.
		// Unknown/conflicting evidence already dominated the merge above.
		if !hasProviderConflict(a.Conflict) && providerLocalTerminal(a) {
			if _, e = tx.ExecContext(ctx, `UPDATE provider_attempts SET state=?,reason=? WHERE attempt_id=? AND may_have_dispatched=0 AND state='prepared'`, a.State, a.Reason, a.ID); e != nil {
				return e
			}
		}
		// Supported terminal external facts also settle older local intent rows.
		if a.State == "failed" || a.State == "cancelled" && a.Reason != "cancel_before_dispatch" {
			if _, e = tx.ExecContext(ctx, `UPDATE provider_attempts SET state=?,reason=? WHERE attempt_id=? AND (state IN ('prepared','dispatching','running','outcome_unknown') OR state='cancelled' AND reason='cancel_before_dispatch')`, a.State, a.Reason, a.ID); e != nil {
				return e
			}
		}
		// New ownership requires actual bytes. Existing source ownership must not
		// disappear merely because verification reports a missing/corrupt file.
		needsInputs := !terminal(a.JobState) || a.State != "failed" && a.State != "cancelled" && a.State != "result_saved"
		if providerResolutionSettled(a) {
			var live int
			if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE job_id=? AND state NOT IN ('completed','failed','cancelled')`, a.JobID).Scan(&live); e != nil {
				return e
			}
			if live == 0 {
				needsInputs = false
			}
		}
		nodes, e := providerConflictNodes(raw)
		if e != nil {
			return e
		}
		retainedInputs := map[string]domain.ProviderInput{}
		for _, node := range nodes {
			var fact providerRestoreFact
			if e = json.Unmarshal(node, &fact); e != nil {
				return e
			}
			for _, input := range fact.Inputs {
				if e = validateProviderObservation(input); e != nil {
					return e
				}
				key := input.ReadID + ":" + input.Hash
				previous, exists := retainedInputs[key]
				if !exists || previous.RetentionMode == "reference_only" {
					retainedInputs[key] = input
				}
			}
		}
		ownedSettled, e := (&writeTx{ctx: ctx, tx: tx}).orphanInputsSettled(a)
		if e != nil {
			return e
		}
		// Typed generation request objects are retained separately from source
		// input facts. Restore never invents an observed source capture.
		for _, node := range nodes {
			var fact providerRestoreFact
			if e = json.Unmarshal(node, &fact); e != nil {
				return e
			}
			if fact.Transport != nil && fact.Transport.Generation != nil {
				owned := generationTransportObjects(fact.Transport.Generation)
				for _, object := range owned {
					if len(object.Hash) != 64 || object.Size < 0 {
						return domain.ErrInvalidTarget
					}
					pinID := "provider-orphan:" + a.ID + ":owned:" + object.Hash
					if !ownedSettled {
						if e = retainPin(a.ID, pinID, object.Hash); e != nil {
							return e
						}
					}
				}
			}
		}
		for _, input := range retainedInputs {
			pinID := "provider-orphan:" + a.ID + ":" + input.ReadID + ":" + input.Hash
			if !needsInputs || input.RetentionMode == "reference_only" {
				if _, e = tx.ExecContext(ctx, `DELETE FROM object_pins WHERE pin_id=? AND owner_kind='provider_orphan' AND owner_id=? AND content_hash=?`, pinID, a.ID, input.Hash); e != nil {
					return e
				}
				continue
			}
			if e = retainPin(a.ID, pinID, input.Hash); e != nil {
				return e
			}
		}

		// Conflicting terminal observations retain every available result object.
		pending := []json.RawMessage{raw}
		results := map[string]bool{}
		for len(pending) > 0 {
			var fact providerRestoreFact
			if e = json.Unmarshal(pending[0], &fact); e != nil {
				return e
			}
			pending = pending[1:]
			if hasProviderConflict(fact.Conflict) {
				pending = append(pending, fact.Conflict)
			}
			if fact.ResultHash != "" {
				results[fact.ResultHash] = true
			}
		}
		for hash := range results {
			if e = retainPin(a.ID, "provider-orphan:"+a.ID+":result:"+hash, hash); e != nil {
				return e
			}
		}
		// A proved settled carrier/no-call fact may release its own execution pins.
		if e = (&writeTx{ctx: ctx, tx: tx}).releaseResolvedOrphanInputs(a); e != nil {
			return e
		}
	}
	// Decide terminal-job lifetime only after all inventory records have merged.
	// Bounded keyset enumeration avoids buffering the library's full job inventory.
	after := ""
	for {
		rows, e := tx.QueryContext(ctx, `SELECT job_id FROM jobs WHERE job_id>? AND kind IN ('question','generation') AND state IN ('completed','failed','cancelled') ORDER BY job_id LIMIT 64`, after)
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = errors.Join(rows.Err(), rows.Close())
		if e != nil {
			return e
		}
		if len(ids) == 0 {
			break
		}
		t := &writeTx{ctx: ctx, tx: tx}
		for _, id := range ids {
			if e = t.releaseProviderInputs(id); e != nil {
				return e
			}
			after = id
		}
	}
	return tx.Commit()
}

// Timestamp-less prior evidence stays unknown; restore never manufactures an
// observation. A supplied timestamp must belong to an actual observation pair.
func validateProviderObservation(input domain.ProviderInput) error {
	if input.RetentionMode != "" && input.RetentionMode != "reference_only" && input.RetentionMode != "selected_snapshot" {
		return domain.ErrInvalidTarget
	}
	if input.ObservedOrigin != "" && input.ObservedOrigin != "daemon_supplied" && input.ObservedOrigin != "native_observed" {
		return domain.ErrInvalidTarget
	}
	if input.ObservedAt != nil {
		if input.ObservedOrigin == "" {
			return domain.ErrInvalidTarget
		}
		if _, e := time.Parse(time.RFC3339Nano, *input.ObservedAt); e != nil {
			return domain.ErrInvalidTarget
		}
	}
	return nil
}

// This inventory is metadata, not a credential or a substitute for missing bytes.
type providerRestoreFact struct {
	Transport         *domain.ProviderTransport `json:"transport,omitempty"`
	ID                string                    `json:"attempt_id"`
	SHA               string                    `json:"request_sha256"`
	State             string                    `json:"state"`
	Generation        int64                     `json:"attempt_generation"`
	RequestID         string                    `json:"provider_request_id"`
	SessionID         string                    `json:"provider_session_id"`
	Reason            string                    `json:"reason"`
	Target            json.RawMessage           `json:"target"`
	JobID             string                    `json:"job_id"`
	PrincipalID       string                    `json:"principal_id"`
	JobState          string                    `json:"job_state"`
	CancelRequested   bool                      `json:"cancel_requested"`
	Inputs            []domain.ProviderInput    `json:"inputs"`
	ResultHash        string                    `json:"result_hash"`
	Conflict          json.RawMessage           `json:"conflicting_prior_fact"`
	MayHaveDispatched *bool                     `json:"may_have_dispatched,omitempty"`
	ResolutionJobID   string                    `json:"resolution_job_id"`
	Resolution        *providerResolution       `json:"resolution"`
}

type providerResolution struct {
	JobID       string `json:"job_id"`
	State       string `json:"state"`
	Kind        string `json:"kind"`
	PrincipalID string `json:"principal_id"`
	RequestSHA  string `json:"request_sha256"`
	AttemptID   string `json:"attempt_id"`
	Outcome     string `json:"outcome"`
	Reason      string `json:"reason"`
	RequestID   string `json:"provider_request_id"`
	SessionID   string `json:"provider_session_id"`
	CompletedAt string `json:"completed_at"`
}

func providerResolutionSettled(f providerRestoreFact) bool {
	r := f.Resolution
	if r == nil || hasProviderConflict(f.Conflict) || !(f.State == "failed" || f.State == "cancelled" || f.State == "result_saved") || r.JobID == "" || r.JobID != f.ResolutionJobID || r.State != "completed" || r.Kind != "provider_reconciliation" || r.PrincipalID == "" || r.PrincipalID != f.PrincipalID || r.RequestSHA != f.SHA || r.AttemptID != f.ID || r.RequestID != f.RequestID || r.SessionID != f.SessionID {
		return false
	}
	if r.Outcome != f.State && !(r.Outcome == "failed" && r.Reason == "result_unavailable" && f.State == "result_saved" || r.Outcome == "result_saved" && f.State == "failed" && f.Reason == "result_unavailable") {
		return false
	}
	_, e := time.Parse(time.RFC3339Nano, r.CompletedAt)
	return e == nil
}

// A supported status observation settles the retained copy of the same effect.
// Contradictory identities remain explicit uncertainty; a single lookup cannot
// certify every divergent effect recorded by a restore conflict.
func (t *writeTx) observeRetainedOrphan(id, state, requestID, sessionID, reason string) error {
	var raw []byte
	e := t.tx.QueryRowContext(t.ctx, `SELECT value FROM provider_orphans WHERE attempt_id=?`, id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	var fact providerRestoreFact
	if e = json.Unmarshal(raw, &fact); e != nil {
		return e
	}
	if requestID == "" {
		requestID = fact.RequestID
	}
	if sessionID == "" {
		sessionID = fact.SessionID
	}
	if fact.RequestID != "" && requestID != fact.RequestID || fact.SessionID != "" && sessionID != fact.SessionID {
		return domain.ErrConflict
	}
	if (fact.State == "dispatching" || fact.State == "running" || fact.State == "outcome_unknown") && state == "cancelled" && reason == "cancel_before_dispatch" {
		return domain.ErrReconciliation
	}
	if hasProviderConflict(fact.Conflict) {
		state = "outcome_unknown"
	}
	return t.exec(`UPDATE provider_orphans SET state=?,value=json_set(value,'$.state',?,'$.provider_request_id',?,'$.provider_session_id',?,'$.reason',?,'$.may_have_dispatched',json(?)) WHERE attempt_id=?`, state, state, requestID, sessionID, reason, fmt.Sprint(fact.State == "dispatching" || fact.State == "running" || fact.State == "outcome_unknown" || fact.MayHaveDispatched != nil && *fact.MayHaveDispatched), id)
}

// Explicit false is a current local intent that never crossed its logical send
// boundary. Missing legacy provenance is not evidence that no call occurred.
func providerLocalTerminal(f providerRestoreFact) bool {
	return f.MayHaveDispatched != nil && !*f.MayHaveDispatched && (f.State == "failed" || f.State == "cancelled" && f.Reason == "cancel_before_dispatch")
}
func providerExternalTerminal(f providerRestoreFact) bool {
	if f.State == "outcome_unknown" && f.Reason == "result_retrieval_pending" {
		return true
	}
	if f.State == "result_saved" {
		return true
	}
	if f.State == "failed" {
		return f.Reason == "result_unavailable" || f.MayHaveDispatched != nil && *f.MayHaveDispatched || f.RequestID != "" && f.Reason == "provider_failed"
	}
	return f.State == "cancelled" && (f.Reason == "cancel_confirmed" || f.Reason != "cancel_before_dispatch" && f.MayHaveDispatched != nil && *f.MayHaveDispatched)
}
func normalizeProviderRestoreFact(raw json.RawMessage) (json.RawMessage, error) {
	var fact providerRestoreFact
	if e := json.Unmarshal(raw, &fact); e != nil {
		return nil, e
	}
	if (fact.State == "failed" || fact.State == "cancelled") && !providerLocalTerminal(fact) && !providerExternalTerminal(fact) {
		var fields map[string]json.RawMessage
		if e := json.Unmarshal(raw, &fields); e != nil {
			return nil, e
		}
		fields["unverified_terminal_fact"] = append(json.RawMessage(nil), raw...)
		fields["state"] = json.RawMessage(`"outcome_unknown"`)
		return json.Marshal(fields)
	}
	return raw, nil
}

// Each node is a complete retained fact with only its chain link removed.
// Rebuilding a canonical set prevents identical restore replay from appending
// nested copies forever, while both incoming and prior chains remain evidence.
func hasProviderConflict(raw json.RawMessage) bool {
	return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func providerConflictNodes(raw json.RawMessage) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	for len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		var fields map[string]json.RawMessage
		if e := json.Unmarshal(raw, &fields); e != nil {
			return nil, e
		}
		next := fields["conflicting_prior_fact"]
		delete(fields, "conflicting_prior_fact")
		base, e := json.Marshal(fields)
		if e != nil {
			return nil, e
		}
		out = append(out, base)
		raw = next
	}
	return out, nil
}

func preserveProviderConflicts(previous, incoming json.RawMessage, merged map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	conflict := hasProviderConflict(merged["conflicting_prior_fact"])
	all := map[string]json.RawMessage{}
	for _, raw := range []json.RawMessage{previous, incoming} {
		if len(raw) == 0 {
			continue
		}
		var fields map[string]json.RawMessage
		if e := json.Unmarshal(raw, &fields); e != nil {
			return nil, e
		}
		conflict = conflict || hasProviderConflict(fields["conflicting_prior_fact"])
		nodes, e := providerConflictNodes(raw)
		if e != nil {
			return nil, e
		}
		for _, node := range nodes {
			all[string(node)] = node
		}
	}
	if !conflict {
		return merged, nil
	}
	merged["state"] = json.RawMessage(`"outcome_unknown"`)
	delete(merged, "conflicting_prior_fact")
	base, e := json.Marshal(merged)
	if e != nil {
		return nil, e
	}
	// The merged top-level fact is already represented once, outside the chain.
	delete(all, string(base))
	keys := make([]string, 0, len(all))
	for key := range all {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var chain json.RawMessage
	for index := len(keys) - 1; index >= 0; index-- {
		var node map[string]json.RawMessage
		if e = json.Unmarshal(all[keys[index]], &node); e != nil {
			return nil, e
		}
		if len(chain) > 0 {
			node["conflicting_prior_fact"] = chain
		}
		chain, e = json.Marshal(node)
		if e != nil {
			return nil, e
		}
	}
	if len(chain) == 0 {
		chain = base
	}
	merged["conflicting_prior_fact"] = chain
	return merged, nil
}

// Conservative ownership union is independent of delivery/category conflicts.
// It does not make an unavailable input dispatchable or invent missing history.
func mergeSourceVersions(inputs ...domain.ProviderInput) []string {
	seen := map[string]bool{}
	for _, input := range inputs {
		for _, id := range append(append([]string{}, input.SourceVersionIDs...), input.SourceVersionID) {
			if id != "" {
				seen[id] = true
			}
		}
	}
	ids := []string{}
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
