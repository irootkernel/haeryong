package sqlite

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
)

func (t *writeTx) LifecycleDocument(id string) (domain.LifecycleDocument, error) {
	d, err := t.Document(id)
	out := domain.LifecycleDocument{Document: d}
	if err != nil {
		return out, err
	}
	err = t.tx.QueryRowContext(t.ctx, `SELECT previous_lifecycle,trashed_at,trash_generation FROM documents WHERE document_id=?`, id).Scan(&out.PreviousLifecycle, &out.TrashedAt, &out.TrashGeneration)
	return out, notFound(err)
}

// Only metadata crosses this port. No note body or context bytes enter a plan.
func (t *writeTx) LifecycleImpact(document string) ([]domain.LifecycleItem, error) {
	rows, err := t.tx.QueryContext(t.ctx, `
 SELECT 'version',version_id,version_id,NULL,NULL,0,NULL,NULL FROM document_versions WHERE document_id=? AND sequence>0
 UNION ALL SELECT 'review',r.owner_id||'/'||r.version_id,r.version_id,NULL,NULL,r.revision,NULL,NULL FROM review_states r JOIN document_versions v USING(version_id) WHERE v.document_id=?
 UNION ALL SELECT 'review_event',r.event_id,r.version_id,NULL,NULL,r.sequence,NULL,NULL FROM review_events r JOIN document_versions v USING(version_id) WHERE v.document_id=?
 UNION ALL SELECT 'annotation',annotation_id,version_id,page_path,region_id,revision,NULL,NULL FROM annotations WHERE document_id=?
 UNION ALL SELECT 'annotation_history',h.annotation_id||'/'||h.revision,json_extract(h.value,'$.anchor.version_id'),json_extract(h.value,'$.anchor.page_path'),json_extract(h.value,'$.anchor.region_id'),h.revision,NULL,NULL FROM annotation_history h JOIN annotations a USING(annotation_id) WHERE a.document_id=?
 UNION ALL SELECT DISTINCT 'context',json_extract(h.value,'$.context_ref'),json_extract(h.value,'$.anchor.version_id'),json_extract(h.value,'$.anchor.page_path'),NULL,0,NULL,NULL FROM annotation_history h JOIN annotations a USING(annotation_id) WHERE a.document_id=? AND json_extract(h.value,'$.context_ref') IS NOT NULL
 UNION ALL SELECT 'revisit',owner_id||'/'||document_id,NULL,NULL,NULL,revision,NULL,NULL FROM revisit_flags WHERE document_id=?
 UNION ALL SELECT r.resource_kind,r.resource_id,v.version_id,NULL,NULL,r.revision,NULL,NULL FROM resource_revisions r JOIN auth_owner o ON substr(r.resource_id,1,length(o.principal_id)+1)=o.principal_id||'/' JOIN document_versions v ON (r.resource_id=o.principal_id||'/'||v.version_id OR substr(r.resource_id,1,length(o.principal_id)+length(v.version_id)+2)=o.principal_id||'/'||v.version_id||'/') WHERE v.document_id=? AND r.resource_kind IN ('reading_position','page_position','reader_preference')
 UNION ALL SELECT 'incoming',r.from_version_id||'/'||r.metadata_reference_id,r.from_version_id,r.from_page_path,r.from_region_id,0,v.document_id,CASE WHEN e.edge_id IS NULL THEN 'unresolved' ELSE 'pinned' END FROM authored_references r JOIN document_versions v ON v.version_id=r.from_version_id LEFT JOIN authored_edges e ON e.from_version_id=r.from_version_id AND e.metadata_reference_id=r.metadata_reference_id WHERE r.target_document_id=?
 ORDER BY 1,2,3,4,5,6`, document, document, document, document, document, document, document, document, document)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.LifecycleItem{}
	for rows.Next() {
		var i domain.LifecycleItem
		if err = rows.Scan(&i.Kind, &i.ID, &i.VersionID, &i.PagePath, &i.RegionID, &i.Revision, &i.SourceDocumentID, &i.Resolution); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
func (t *writeTx) PutLifecyclePlan(p domain.LifecyclePlan, items []domain.LifecycleItem) error {
	if err := t.exec(`INSERT INTO lifecycle_plans VALUES(?,?,?,?,?,?,?,?,?,?,?)`, p.ID, p.OwnerID, p.DocumentID, p.Operation, p.State, p.ExpectedRevision, p.ConfigRevision, p.PermissionsRevision, p.ExpiresAt, p.Digest, jsonBytes(p)); err != nil {
		return err
	}
	for n, i := range items {
		if err := t.exec(`INSERT INTO lifecycle_plan_items VALUES(?,?,?)`, p.ID, n+1, jsonBytes(i)); err != nil {
			return err
		}
	}
	return nil
}
func (t *writeTx) LifecyclePlan(id string) (domain.LifecyclePlan, error) {
	var p domain.LifecyclePlan
	var raw []byte
	var owner, digest string
	var permission int64
	err := t.tx.QueryRowContext(t.ctx, `SELECT owner_id,permissions_revision,digest,value FROM lifecycle_plans WHERE plan_id=?`, id).Scan(&owner, &permission, &digest, &raw)
	if err != nil {
		return p, notFound(err)
	}
	err = json.Unmarshal(raw, &p)
	p.OwnerID = owner
	p.PermissionsRevision = permission
	p.Digest = digest
	return p, err
}
func (t *writeTx) LifecycleItems(id string) ([]domain.LifecycleItem, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT value FROM lifecycle_plan_items WHERE plan_id=? ORDER BY sequence`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.LifecycleItem{}
	for rows.Next() {
		var raw []byte
		var i domain.LifecycleItem
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &i); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
func (t *writeTx) CommitLifecycle(p domain.LifecyclePlan, r domain.LifecycleResult, actor string) error {
	if r.Changed {
		if err := t.exec(`UPDATE documents SET lifecycle=?,previous_lifecycle=?,trashed_at=?,revision=revision+1,trash_generation=trash_generation+? WHERE document_id=? AND revision=?`, r.Lifecycle, r.PreviousLifecycle, r.TrashedAt, r.Lifecycle == "trash", p.DocumentID, p.ExpectedRevision); err != nil {
			return err
		}
		if r.Lifecycle == "purged" {
			if err := t.exec(`UPDATE documents SET title='',latest_version_id=NULL,trash_generation=trash_generation+1 WHERE document_id=?`, p.DocumentID); err != nil {
				return err
			}
		}
		if r.Lifecycle == "trash" || r.Lifecycle == "purged" {
			// Fence accepted publication and deterministic work before releasing the writer gate.
			rows, err := t.tx.QueryContext(t.ctx, `SELECT job_id FROM derived_work WHERE version_id IN (SELECT version_id FROM document_versions WHERE document_id=?) UNION SELECT job_id FROM import_intents WHERE json_extract(value,'$.DocumentID')=?`, p.DocumentID, p.DocumentID)
			if err != nil {
				return err
			}
			ids := []string{}
			for rows.Next() {
				var id string
				if err = rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, id)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, id := range ids {
				if _, err = t.CancelJob(id); err != nil {
					return err
				}
			}
		}
		if err := t.appendEvent("document", p.DocumentID, "document.lifecycle_changed", r.Revision, jsonBytes(map[string]any{"actor_session": actor, "operation_id": r.OperationID, "plan_id": p.ID, "action": p.Operation, "from": p.From, "to": p.To})); err != nil {
			return err
		}
	}
	p.State = "committed"
	p.Result = &r
	return t.exec(`UPDATE lifecycle_plans SET state='committed',value=? WHERE plan_id=?`, jsonBytes(p), p.ID)
}
func (t *writeTx) TrashDocuments(project, after string, limit int) ([]domain.TrashItem, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT document_id,project_id,title,latest_version_id,revision,lifecycle,previous_lifecycle,trashed_at FROM documents WHERE lifecycle='trash' AND (?='' OR project_id=?) AND document_id>? ORDER BY document_id LIMIT ?`, project, project, after, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.TrashItem{}
	for rows.Next() {
		var d domain.TrashItem
		if err = rows.Scan(&d.DocumentID, &d.ProjectID, &d.Title, &d.LatestVersionID, &d.Revision, &d.Lifecycle, &d.PreviousLifecycle, &d.TrashedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
