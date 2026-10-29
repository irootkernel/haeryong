package sqlite

import (
	"encoding/json"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func (t *writeTx) CreateSourceCapture(c domain.SourceCapture) error {
	if c.ID == "" || c.State != "capturing" || c.TargetSetID == "" {
		return domain.ErrInvalidTarget
	}
	return t.exec(`INSERT INTO source_captures VALUES(?,?,?,?,?,?,?,?,?,?,?)`, c.ID, c.PrincipalID, c.ProjectID, c.TargetSetID, c.State, c.Selection, c.ConfigRevision, c.PermissionsRevision, c.CapturedAt, c.OperationID, c.Reason)
}
func (t *writeTx) SourceCapture(id string) (domain.SourceCapture, error) {
	c := domain.SourceCapture{ID: id}
	e := t.tx.QueryRowContext(t.ctx, `SELECT principal_id,project_id,target_set_id,state,requested_selection,config_revision,permissions_revision,captured_at,operation_id,reason FROM source_captures WHERE capture_id=?`, id).Scan(&c.PrincipalID, &c.ProjectID, &c.TargetSetID, &c.State, &c.Selection, &c.ConfigRevision, &c.PermissionsRevision, &c.CapturedAt, &c.OperationID, &c.Reason)
	return c, notFound(e)
}
func (t *writeTx) PutSourceCaptureRead(r domain.SourceCaptureRead) error {
	c, e := t.SourceCapture(r.CaptureID)
	if e != nil {
		return e
	}
	if c.State != "capturing" {
		return domain.ErrTransition
	}
	if r.Availability == "available" {
		object, e := t.Object(r.Hash)
		if e != nil {
			return e
		}
		if object.State != "ready" || object.Size != r.Size {
			return domain.ErrInvalidTarget
		}
	} else if r.Hash != "" || r.Reason == "" || r.Availability != "unavailable" && r.Availability != "denied" {
		return domain.ErrInvalidTarget
	}

	if e = t.exec(`INSERT INTO source_capture_reads VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(read_id) DO UPDATE SET source_json=excluded.source_json,content_hash=excluded.content_hash,byte_size=excluded.byte_size,availability=excluded.availability,reason=excluded.reason WHERE source_capture_reads.capture_id=excluded.capture_id AND source_capture_reads.source_id=excluded.source_id AND source_capture_reads.location_json=excluded.location_json AND source_capture_reads.retention_mode=excluded.retention_mode`, r.ID, r.CaptureID, r.SourceID, r.SourceJSON, r.LocationJSON, r.Hash, r.Size, r.RetentionMode, r.Availability, r.Reason); e != nil {
		return e
	}
	if r.Availability != "available" {
		return t.ReleasePin("source-capture:" + c.ID + ":" + r.ID)
	}
	return t.AcquirePin(domain.Pin{ID: "source-capture:" + c.ID + ":" + r.ID, Kind: "source_capture", OwnerID: c.ID, Hash: r.Hash})
}
func (t *writeTx) SourceCaptureRead(id string) (domain.SourceCaptureRead, error) {
	r := domain.SourceCaptureRead{ID: id}
	e := t.tx.QueryRowContext(t.ctx, `SELECT capture_id,source_id,source_json,location_json,content_hash,byte_size,retention_mode,availability,reason FROM source_capture_reads WHERE read_id=?`, id).Scan(&r.CaptureID, &r.SourceID, &r.SourceJSON, &r.LocationJSON, &r.Hash, &r.Size, &r.RetentionMode, &r.Availability, &r.Reason)
	return r, notFound(e)
}
func (t *writeTx) SourceCaptureReads(id, after string, limit int) ([]domain.SourceCaptureRead, error) {
	rows, e := t.tx.QueryContext(t.ctx, `SELECT read_id FROM source_capture_reads WHERE capture_id=? AND read_id>? ORDER BY read_id LIMIT ?`, id, after, batch(limit))
	if e != nil {
		return nil, e
	}
	ids := []string{}
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
	out := make([]domain.SourceCaptureRead, 0, len(ids))
	for _, id := range ids {
		r, e := t.SourceCaptureRead(id)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}
func (t *writeTx) FinishSourceCapture(id, state, reason string) error {
	c, e := t.SourceCapture(id)
	if e != nil {
		return e
	}
	if c.State != "capturing" || state != "completed" && state != "failed" {
		return domain.ErrTransition
	}
	if state == "completed" {
		if _, e = t.TargetSetByID(c.TargetSetID); e != nil {
			return e
		}
		after := ""
		for {
			rows, e := t.SourceCaptureReads(id, after, 64)
			if e != nil {
				return e
			}
			if len(rows) == 0 {
				break
			}
			for _, r := range rows {
				if e = t.PutCapturedRead(domain.CapturedRead{ID: r.ID, TargetSetID: c.TargetSetID, Basis: "project_source", SourceJSON: r.SourceJSON, LocationJSON: r.LocationJSON, BytesSHA: r.Hash, ConfigRevision: c.ConfigRevision, PermissionsRevision: c.PermissionsRevision, CreatedAt: c.CapturedAt}); e != nil {
					return e
				}
				if r.RetentionMode == "selected_snapshot" {
					if e = t.Adopt(domain.Reference{Kind: "selected_snapshot", ID: r.ID, Hash: r.Hash}, "source-capture:"+id+":"+r.ID); e != nil {
						return e
					}
				}
				after = r.ID
			}
		}
	}
	if e = t.exec(`UPDATE source_captures SET state=?,reason=? WHERE capture_id=?`, state, reason, id); e != nil {
		return e
	}
	// A standalone reference-only capture retains identity only. A future job
	// admission must acquire its authorized read-set ownership atomically.
	if state == "failed" {
		if e = t.ReleaseOwnerPins("source_capture", id); e != nil {
			return e
		}
	}
	return nil
}
func (t *writeTx) AttachSourceInputs(jobID string, reads []string) error {
	j, e := t.Job(jobID)
	if e != nil {
		return e
	}
	if !domain.ProviderJob(j.Kind) || j.State != "queued" || j.CancelRequested {
		return domain.ErrTransition
	}
	var scope domain.ProviderScope
	if json.Unmarshal(j.Scope, &scope) != nil || len(reads) != len(scope.ReadIDs) {
		return domain.ErrInvalidTarget
	}
	expected := map[string]bool{}
	for _, id := range scope.ReadIDs {
		if expected[id] {
			return domain.ErrInvalidTarget
		}
		expected[id] = true
	}
	for _, id := range reads {
		if !expected[id] {
			return domain.ErrInvalidTarget
		}
		delete(expected, id)
		r, e := t.SourceCaptureRead(id)
		if e != nil {
			return e
		}
		c, e := t.SourceCapture(r.CaptureID)
		if e != nil {
			return e
		}
		if scope.ProviderID == "" {
			return domain.ErrDenied
		}
		if _, e = app.ManagedSourcePolicy(t, c.ProjectID, r.SourceID, scope.ProviderID); e != nil {
			return e
		}
		if c.State != "completed" || c.TargetSetID != scope.TargetSetID || c.PrincipalID != scope.PrincipalID || r.Availability != "available" {
			return domain.ErrDenied
		}
		owner, e := t.AuthState()
		if e != nil {
			return e
		}
		project, e := app.TargetProject(t, scope.Target)
		if e != nil {
			return e
		}
		if scope.PrincipalID != owner.OwnerID || scope.AuthEpoch != owner.AuthEpoch || project != c.ProjectID {
			return domain.ErrDenied
		}
		current, e := t.Scope(project)
		if e != nil {
			return e
		}
		if j.Permissions[project] != current.Project.PermissionsRevision {
			return domain.ErrPrecondition
		}
		var pinned int
		if e = t.tx.QueryRowContext(t.ctx, `SELECT (SELECT count(*) FROM object_pins WHERE pin_id=? AND owner_kind='source_capture' AND owner_id=? AND content_hash=?)+(SELECT count(*) FROM object_references WHERE owner_kind='selected_snapshot' AND owner_id=? AND content_hash=? AND ?='selected_snapshot')`, "source-capture:"+c.ID+":"+id, c.ID, r.Hash, id, r.Hash, r.RetentionMode).Scan(&pinned); e != nil {
			return e
		}
		if pinned == 0 {
			return domain.ErrReconciliation
		}
		if e = t.exec(`INSERT INTO job_inputs VALUES(?,?)`, jobID, id); e != nil {
			return e
		}
		if e = t.AcquirePin(domain.Pin{ID: "job-input:" + jobID + ":" + id, Kind: "job_input", OwnerID: jobID, Hash: r.Hash}); e != nil {
			return e
		}
	}
	return nil
}

// Diff rows are enumerated by immutable source/location keys, without loading
// either whole inventory or any body into memory.
func (t *writeTx) SourceCaptureDiff(left, right, source, location string, limit int) ([]domain.SourceCaptureDelta, error) {
	// This condition mirrors domain.SourceCaptureRead.CaptureFacts. Unsupported
	// legacy/missing identity stays conservative, including keyset comparisons.
	rows, e := t.tx.QueryContext(t.ctx, `WITH facts AS (
 SELECT *, CASE WHEN retention_mode='reference_only' AND availability='unavailable' AND reason='reference_only_uncopied'
 AND length(content_hash)=64 AND content_hash NOT GLOB '*[^0-9a-fA-F]*'
 AND json_type(location_json,'$.path')='text' AND json_extract(location_json,'$.path')<>''
 AND json_type(source_json,'$.kind')='text'
 AND coalesce(json_type(source_json,'$.snapshot_sha256'),'null') IN ('text','null')
 AND coalesce(json_type(source_json,'$.repo_id'),'null') IN ('text','null')
 AND coalesce(json_type(source_json,'$.exact_revision'),'null') IN ('text','null')
 AND ((json_extract(source_json,'$.kind')='snapshot' AND json_extract(source_json,'$.snapshot_sha256')=content_hash)
 OR (json_extract(source_json,'$.kind')='git' AND coalesce(json_extract(source_json,'$.repo_id'),'')<>''
 AND length(json_extract(source_json,'$.exact_revision')) IN (40,64) AND json_extract(source_json,'$.exact_revision') NOT GLOB '*[^0-9a-fA-F]*'))
 THEN 1 ELSE 0 END AS copied_available FROM source_capture_reads WHERE capture_id IN (?,?)
 ), keys AS (
 SELECT DISTINCT source_id,CAST(location_json AS TEXT) AS location FROM facts
 ) SELECT k.source_id,k.location,l.read_id,r.read_id FROM keys k
 LEFT JOIN facts l ON l.capture_id=? AND l.source_id=k.source_id AND CAST(l.location_json AS TEXT)=k.location
 LEFT JOIN facts r ON r.capture_id=? AND r.source_id=k.source_id AND CAST(r.location_json AS TEXT)=k.location
 WHERE (k.source_id,k.location)>(?,?) AND (l.read_id IS NULL OR r.read_id IS NULL OR l.content_hash<>r.content_hash
 OR (CASE WHEN l.copied_available THEN 'available' ELSE l.availability END)<>(CASE WHEN r.copied_available THEN 'available' ELSE r.availability END)
 OR (CASE WHEN l.copied_available THEN '' ELSE l.reason END)<>(CASE WHEN r.copied_available THEN '' ELSE r.reason END))
 ORDER BY k.source_id,k.location LIMIT ?`, left, right, left, right, source, location, batch(limit))
	if e != nil {
		return nil, e
	}
	type key struct {
		source, location string
		left, right      *string
	}
	keys := []key{}
	for rows.Next() {
		var k key
		if e = rows.Scan(&k.source, &k.location, &k.left, &k.right); e != nil {
			rows.Close()
			return nil, e
		}
		keys = append(keys, k)
	}
	e = errors.Join(rows.Err(), rows.Close())
	if e != nil {
		return nil, e
	}
	out := make([]domain.SourceCaptureDelta, 0, len(keys))
	for _, k := range keys {
		d := domain.SourceCaptureDelta{SourceID: k.source, Location: json.RawMessage(k.location)}
		if k.left != nil {
			r, e := t.SourceCaptureRead(*k.left)
			if e != nil {
				return nil, e
			}
			d.Before = &r
		}
		if k.right != nil {
			r, e := t.SourceCaptureRead(*k.right)
			if e != nil {
				return nil, e
			}
			d.After = &r
		}
		out = append(out, d)
	}
	return out, nil
}
