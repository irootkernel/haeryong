package sqlite

import (
	"encoding/json"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// The immutable requested read_id links are the ownership relation; copying
// bytes never makes an inherited version context independent. UNION deduplicates
// shared ancestry without imposing a content/traversal quota.
const sourceReadDependencies = `WITH RECURSIVE source_read_edges(child,parent) AS (
 SELECT r.read_id,json_extract(p.value,'$.read_id')
 FROM source_capture_reads r JOIN source_captures c ON c.capture_id=r.capture_id,
 json_each(c.requested_selection,'$.selections') s,json_each(s.value,'$.paths') p
 WHERE json_extract(c.requested_selection,'$.source_mode')='pinned'
 AND json_extract(r.source_json,'$.kind')='snapshot'
 AND json_extract(s.value,'$.source_id')=r.source_id
 AND json_extract(p.value,'$.path')=json_extract(r.location_json,'$.path')
 AND coalesce(json_extract(p.value,'$.read_id'),'')<>''
), source_read_ancestors(read_id,ancestor) AS (
 SELECT read_id,read_id FROM source_capture_reads
 UNION SELECT a.read_id,e.parent FROM source_read_ancestors a JOIN source_read_edges e ON e.child=a.ancestor
), source_read_contexts(read_id,version_id) AS (
 SELECT DISTINCT a.read_id,json_extract(c.requested_selection,'$.version_id')
 FROM source_read_ancestors a JOIN source_capture_reads r ON r.read_id=a.ancestor JOIN source_captures c ON c.capture_id=r.capture_id
 WHERE coalesce(json_extract(c.requested_selection,'$.version_id'),'')<>''
) `

const sourceJobDependencies = sourceReadDependencies + `, generation_source_contexts(job_id,version_id) AS (
 SELECT DISTINCT g.job_id,c.version_id FROM generation_intents g,json_each(g.value,'$.SourceReadIDs') r
 JOIN source_read_contexts c ON c.read_id=r.value
 UNION SELECT DISTINCT g.job_id,ctx.version_id FROM generation_intents g JOIN jobs j ON j.job_id=g.job_id JOIN source_captures capture ON capture.operation_id=json_extract(g.value,'$.CaptureOperationID') AND capture.principal_id=json_extract(j.scope,'$.principal_id') AND capture.project_id=json_extract(g.value,'$.ProjectID'),json_each(capture.requested_selection,'$._retained_read_ids') r
 JOIN source_read_contexts ctx ON ctx.read_id=r.value WHERE capture.state='capturing'
), question_source_contexts(job_id,version_id) AS (
 SELECT q.job_id,q.version_id FROM questions q
 UNION SELECT DISTINCT q.job_id,c.version_id FROM questions q,json_each(q.value,'$.SourceReadIDs') r
 JOIN source_read_contexts c ON c.read_id=r.value
 UNION SELECT DISTINCT q.job_id,ctx.version_id FROM questions q JOIN jobs j ON j.job_id=q.job_id JOIN source_captures capture ON capture.operation_id=json_extract(q.value,'$.CaptureOperationID') AND capture.principal_id=q.owner_id AND capture.project_id=q.project_id,json_each(capture.requested_selection,'$._retained_read_ids') r
 JOIN source_read_contexts ctx ON ctx.read_id=r.value WHERE capture.state='capturing'
), maintenance_source_contexts(job_id,version_id) AS (
 SELECT j.job_id,json_extract(e.value,'$.version_id') FROM jobs j,json_each(j.scope,'$.invocation.maintenance.explanations') e WHERE j.kind='maintenance' AND coalesce(json_extract(e.value,'$.version_id'),'')<>''
 UNION SELECT DISTINCT m.job_id,c.version_id FROM maintenance_intents m,json_each(m.value,'$.source_read_ids') r
 JOIN source_read_contexts c ON c.read_id=r.value
 UNION SELECT DISTINCT m.job_id,coalesce(nullif(json_extract(step.value,'$.version_id'),''),ctx.version_id) FROM maintenance_intents m JOIN jobs j ON j.job_id=m.job_id,json_each(m.value,'$.captures') step JOIN source_captures capture ON capture.operation_id=json_extract(step.value,'$.operation_id') AND capture.principal_id=json_extract(j.scope,'$.principal_id') AND capture.project_id=json_extract(step.value,'$.project_id') LEFT JOIN json_each(capture.requested_selection,'$._retained_read_ids') r LEFT JOIN source_read_contexts ctx ON ctx.read_id=r.value WHERE capture.state='capturing' AND coalesce(nullif(json_extract(step.value,'$.version_id'),''),ctx.version_id,'')<>''
), source_job_contexts(job_id,version_id) AS (
 SELECT i.job_id,c.version_id FROM job_inputs i JOIN source_read_contexts c ON c.read_id=i.read_id
 UNION SELECT job_id,version_id FROM generation_source_contexts
 UNION SELECT job_id,version_id FROM question_source_contexts
 UNION SELECT job_id,version_id FROM maintenance_source_contexts
) `

func (t *writeTx) sourceReadVersions(id string) ([]string, error) {
	rows, err := t.tx.QueryContext(t.ctx, sourceReadDependencies+`SELECT version_id FROM source_read_contexts WHERE read_id=? ORDER BY version_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

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
		owner, e := t.AuthState()
		if e != nil {
			return e
		}
		project, e := app.TargetProject(t, scope.Target)
		if e != nil {
			return e
		}
		expectedTargetSet, expectedProject, _, ok := domain.MaintenanceReadTarget(scope, scope.TargetSetID, project, id)
		if !ok || c.State != "completed" || c.TargetSetID != expectedTargetSet || c.PrincipalID != scope.PrincipalID || r.Availability != "available" {
			return domain.ErrDenied
		}
		if scope.AuthEpoch != owner.AuthEpoch || expectedProject != c.ProjectID {
			return domain.ErrDenied
		}
		if j.Kind == "generation" && scope.Purpose == "body" {
			if e = app.AuthorizeGeneration(t, scope); e != nil {
				return e
			}
		} else if j.Kind == "maintenance" && scope.Purpose == "body" {
			if e = app.AuthorizeMaintenance(t, j, scope, "", ""); e != nil {
				return e
			}
		} else if scope.PrincipalID != owner.OwnerID {
			return domain.ErrDenied
		}
		current, e := t.Scope(expectedProject)
		if e != nil {
			return e
		}
		if j.Permissions[expectedProject] != current.Project.PermissionsRevision {
			return domain.ErrPrecondition
		}
		var pinned int
		if e = t.tx.QueryRowContext(t.ctx, `SELECT (SELECT count(*) FROM object_pins WHERE pin_id=? AND owner_kind='source_capture' AND owner_id=? AND content_hash=?)+(SELECT count(*) FROM object_pins WHERE pin_id=? AND owner_kind='job_input' AND owner_id=? AND content_hash=?)+(SELECT count(*) FROM object_references WHERE owner_kind='selected_snapshot' AND owner_id=? AND content_hash=? AND ?='selected_snapshot')`, "source-capture:"+c.ID+":"+id, c.ID, r.Hash, "job-input:"+jobID+":"+id, jobID, r.Hash, id, r.Hash, r.RetentionMode).Scan(&pinned); e != nil {
			return e
		}
		if pinned == 0 {
			return domain.ErrReconciliation
		}
		if e = app.GuardSourceInput(t, scope, id); e != nil {
			return e
		}
		if e = t.exec(`INSERT INTO job_inputs VALUES(?,?) ON CONFLICT(job_id,read_id) DO NOTHING`, jobID, id); e != nil {
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
