package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
)

// Exclusive profile startup proves only these in-process capture/read owners
// dead. It does not release queued job, attempt, orphan or snapshot ownership.
func (s *Store) recoverSourceCaptures(ctx context.Context) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	t := &writeTx{tx: tx, ctx: ctx}
	rows, e := tx.QueryContext(ctx, `SELECT capture_id FROM source_captures WHERE state='capturing'`)
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
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		c, e := t.SourceCapture(id)
		if e != nil {
			return e
		}
		key := domain.OperationKey{PrincipalID: c.PrincipalID, OperationID: c.OperationID}
		receipt, e := t.Receipt(key)
		if e != nil {
			return e
		}
		if receipt.State != "accepted" {
			return domain.ErrReconciliation
		}
		if e = t.FinishSourceCapture(id, "failed", "interrupted_before_commit"); e != nil {
			return e
		}
		requestID, e := service.NewID()
		if e != nil {
			return e
		}
		body, _ := json.Marshal(map[string]any{"error": map[string]any{"code": "SERVICE_RECOVERING", "message": "source check failed: interrupted_before_commit", "retryable": false, "details": map[string]string{"reason": "interrupted_before_commit"}}, "request_id": requestID[:16]})
		if e = t.CompleteOperation(key, domain.Response{Status: 503, Body: body}); e != nil {
			return e
		}
	}
	if e = t.exec(`DELETE FROM object_pins WHERE owner_kind='source_capture_read'`); e != nil {
		return e
	}
	return tx.Commit()
}

// projectSourceBackup edits only the private coherent copy. Reference-only
// source owners are projected as uncopied; the same hash's independent payload,
// selected_snapshot/result/other owners remain authoritative and still copied.
func projectSourceBackup(ctx context.Context, s *Store) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	statements := []string{
		`DELETE FROM object_pins WHERE owner_kind='source_capture' AND owner_id IN(SELECT capture_id FROM source_captures)`,
		`DELETE FROM object_pins WHERE owner_kind='source_capture_read'`,
		`DELETE FROM object_pins WHERE owner_kind='job_input' AND pin_id IN(SELECT 'job-input:'||j.job_id||':'||r.read_id FROM job_inputs j JOIN source_capture_reads r ON r.read_id=j.read_id WHERE r.retention_mode='reference_only')`,
		`DELETE FROM object_pins WHERE owner_kind='provider_attempt' AND pin_id IN(SELECT i.attempt_id||':'||i.read_id FROM provider_inputs i JOIN source_capture_reads r ON r.read_id=i.read_id WHERE r.retention_mode='reference_only')`,
		`DELETE FROM object_pins WHERE owner_kind='provider_continuation' AND EXISTS(SELECT 1 FROM provider_inputs i JOIN source_capture_reads r ON r.read_id=i.read_id JOIN provider_attempts a ON a.attempt_id=i.attempt_id WHERE i.attempt_id=object_pins.owner_id AND r.content_hash=object_pins.content_hash AND r.retention_mode='reference_only' AND coalesce(a.result_hash,'')<>object_pins.content_hash)`,
		`UPDATE source_capture_reads SET availability='unavailable',reason='reference_only_uncopied' WHERE retention_mode='reference_only' AND availability='available'`,
	}
	if e = projectOrphanSourceInputs(ctx, tx); e != nil {
		return e
	}
	for _, q := range statements {
		if _, e = tx.ExecContext(ctx, q); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// Carry source retention through orphan-only restore history. A conflicting or
// legacy input without explicit reference-only provenance remains conservatively
// owned; a result with the same bytes has its separate unchanged result pin.
func projectOrphanSourceInputs(ctx context.Context, tx *sql.Tx) error {
	after := ""
	for {
		rows, e := tx.QueryContext(ctx, `SELECT attempt_id,value FROM provider_orphans WHERE attempt_id>? ORDER BY attempt_id LIMIT 64`, after)
		if e != nil {
			return e
		}
		type entry struct {
			id  string
			raw json.RawMessage
		}
		page := []entry{}
		for rows.Next() {
			var r entry
			if e = rows.Scan(&r.id, &r.raw); e != nil {
				rows.Close()
				return e
			}
			page = append(page, r)
		}
		e = errors.Join(rows.Err(), rows.Close())
		if e != nil {
			return e
		}
		if len(page) == 0 {
			return nil
		}
		for _, r := range page {
			nodes := []map[string]json.RawMessage{}
			raw := r.raw
			omit := map[string]bool{}
			for len(raw) > 0 && string(raw) != "null" {
				var node map[string]json.RawMessage
				if e = json.Unmarshal(raw, &node); e != nil {
					return e
				}
				raw = node["conflicting_prior_fact"]
				delete(node, "conflicting_prior_fact")
				var inputs []domain.ProviderInput
				if e = json.Unmarshal(node["inputs"], &inputs); e != nil && len(node["inputs"]) > 0 {
					return e
				}
				for n := range inputs {
					i := &inputs[n]
					var mode, version string
					e = tx.QueryRowContext(ctx, `SELECT r.retention_mode,coalesce(json_extract(c.requested_selection,'$.version_id'),'') FROM source_capture_reads r JOIN source_captures c ON c.capture_id=r.capture_id WHERE r.read_id=? AND r.content_hash=?`, i.ReadID, i.Hash).Scan(&mode, &version)
					if e != nil && !errors.Is(e, sql.ErrNoRows) {
						return e
					}
					if e == nil {
						i.RetentionMode = mode
						if i.SourceVersionID == "" {
							i.SourceVersionID = version
						}
					}
					key := i.ReadID + ":" + i.Hash
					ref := i.RetentionMode == "reference_only"
					if old, ok := omit[key]; ok {
						omit[key] = old && ref
					} else {
						omit[key] = ref
					}
				}
				if len(node["inputs"]) > 0 {
					node["inputs"], e = json.Marshal(inputs)
					if e != nil {
						return e
					}
				}
				nodes = append(nodes, node)
			}
			var merged json.RawMessage
			for n := len(nodes) - 1; n >= 0; n-- {
				if len(merged) > 0 {
					nodes[n]["conflicting_prior_fact"] = merged
				}
				merged, e = json.Marshal(nodes[n])
				if e != nil {
					return e
				}
			}
			if _, e = tx.ExecContext(ctx, `UPDATE provider_orphans SET value=? WHERE attempt_id=?`, merged, r.id); e != nil {
				return e
			}
			for key, yes := range omit {
				if yes {
					if _, e = tx.ExecContext(ctx, `DELETE FROM object_pins WHERE pin_id=? AND owner_kind='provider_orphan' AND owner_id=?`, "provider-orphan:"+r.id+":"+key, r.id); e != nil {
						return e
					}
				}
			}
			after = r.id
		}
	}
}
