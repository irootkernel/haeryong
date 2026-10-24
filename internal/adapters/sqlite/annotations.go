package sqlite

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
)

func (t *writeTx) Annotation(id string) (domain.Annotation, error) {
	var n domain.Annotation
	var raw []byte
	var owner string
	err := t.tx.QueryRowContext(t.ctx, `SELECT owner_id,value FROM annotations WHERE annotation_id=?`, id).Scan(&owner, &raw)
	if err != nil {
		return n, notFound(err)
	}
	err = json.Unmarshal(raw, &n)
	n.OwnerID = owner
	return n, err
}
func (t *writeTx) AnnotationRevision(id string, rev int64) (domain.Annotation, error) {
	var n domain.Annotation
	var raw []byte
	var owner string
	err := t.tx.QueryRowContext(t.ctx, `SELECT a.owner_id,h.value FROM annotation_history h JOIN annotations a USING(annotation_id) WHERE h.annotation_id=? AND h.revision=?`, id, rev).Scan(&owner, &raw)
	if err != nil {
		return n, notFound(err)
	}
	err = json.Unmarshal(raw, &n)
	n.OwnerID = owner
	return n, err
}
func (t *writeTx) Annotations(owner, version, after string, limit int) ([]domain.Annotation, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT value FROM annotations WHERE owner_id=? AND version_id=? AND deleted_at IS NULL AND annotation_id>? ORDER BY annotation_id LIMIT ?`, owner, version, after, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Annotation{}
	for rows.Next() {
		var raw []byte
		var n domain.Annotation
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		n.OwnerID = owner
		out = append(out, n)
	}
	return out, rows.Err()
}
func (t *writeTx) AnnotationHistory(id string, after int64, limit int) ([]domain.Annotation, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT value FROM annotation_history WHERE annotation_id=? AND revision>? ORDER BY revision LIMIT ?`, id, after, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Annotation{}
	for rows.Next() {
		var raw []byte
		var n domain.Annotation
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (t *writeTx) SaveAnnotation(n domain.Annotation) error {
	raw := jsonBytes(n)
	if err := t.exec(`INSERT INTO annotations VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(annotation_id) DO UPDATE SET version_id=excluded.version_id,page_path=excluded.page_path,region_id=excluded.region_id,revision=excluded.revision,deleted_at=excluded.deleted_at,value=excluded.value`, n.ID, n.OwnerID, n.DocumentID, n.OriginalAnchor.VersionID, n.Anchor.VersionID, nullableText(n.Anchor.PagePath), nullableText(n.Anchor.RegionID), n.Revision, nullableText(n.DeletedAt), raw); err != nil {
		return err
	}
	if err := t.exec(`INSERT INTO annotation_history VALUES(?,?,?)`, n.ID, n.Revision, raw); err != nil {
		return err
	}
	return t.appendEvent("annotation", n.ID, "resource.invalidated", n.Revision, jsonBytes(map[string]any{"owner_id": n.OwnerID, "document_id": n.DocumentID, "version_id": n.Anchor.VersionID, "actor_session": n.ActorSession, "operation_id": n.OperationID, "action": n.Action}))
}
func (t *writeTx) Revisit(owner, document string) (domain.Revisit, error) {
	n := domain.Revisit{OwnerID: owner, DocumentID: document}
	err := t.tx.QueryRowContext(t.ctx, `SELECT flag,revision FROM revisit_flags WHERE owner_id=? AND document_id=?`, owner, document).Scan(&n.Flag, &n.Revision)
	return n, notFound(err)
}
func (t *writeTx) SaveRevisit(n domain.Revisit, actor, key string) error {
	if err := t.exec(`INSERT INTO revisit_flags VALUES(?,?,?,?) ON CONFLICT(owner_id,document_id) DO UPDATE SET flag=excluded.flag,revision=excluded.revision`, n.OwnerID, n.DocumentID, n.Flag, n.Revision); err != nil {
		return err
	}
	return t.appendEvent("revisit", n.DocumentID, "resource.invalidated", n.Revision, jsonBytes(map[string]any{"owner_id": n.OwnerID, "document_id": n.DocumentID, "actor_session": actor, "operation_id": key, "flag": n.Flag}))
}
func (t *writeTx) QueryRevisitDocuments(owner, project, lifecycle, review, flag, afterUpdated, afterID string, limit int) ([]domain.QueryDocument, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT d.document_id,d.project_id,d.title,d.lifecycle,coalesce(d.latest_version_id,''),d.revision,d.updated_at FROM documents d JOIN projects p ON p.project_id=d.project_id LEFT JOIN revisit_flags f ON f.document_id=d.document_id AND f.owner_id=? WHERE d.lifecycle NOT IN ('trash','purged') AND p.lifecycle NOT IN ('trash','purged') AND (?='' OR d.project_id=?) AND (?='' OR d.lifecycle=?) AND coalesce(f.flag,0)=? AND (d.updated_at,d.document_id)>(?,?) AND (?='' OR (?='reviewing' AND EXISTS(SELECT 1 FROM review_states r JOIN document_versions v ON v.version_id=r.version_id WHERE r.owner_id=? AND v.document_id=d.document_id AND r.state='reviewing')) OR (?='updated_since_review' AND (SELECT v.sequence FROM review_states r JOIN document_versions v ON v.version_id=r.version_id WHERE r.owner_id=? AND v.document_id=d.document_id AND r.state='reviewed' ORDER BY r.event_sequence DESC LIMIT 1)<(SELECT sequence FROM document_versions WHERE version_id=d.latest_version_id))) ORDER BY d.updated_at COLLATE BINARY,d.document_id COLLATE BINARY LIMIT ?`, owner, project, project, lifecycle, lifecycle, flag == "true", afterUpdated, afterID, review, review, owner, review, owner, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.QueryDocument{}
	for rows.Next() {
		var d domain.QueryDocument
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.Title, &d.Lifecycle, &d.LatestVersionID, &d.Revision, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
