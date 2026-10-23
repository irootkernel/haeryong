package sqlite

import (
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"reflect"
)

func (t *writeTx) ReferenceMaterialization(version string) (m domain.ReferenceMaterialization, err error) {
	err = t.tx.QueryRowContext(t.ctx, `SELECT from_version_id,manifest_sha256,reference_revision,state,job_id,revision FROM reference_materializations WHERE from_version_id=?`, version).Scan(&m.FromVersionID, &m.ManifestSHA, &m.ReferenceRevision, &m.State, &m.JobID, &m.Revision)
	if err != nil {
		return m, notFound(err)
	}
	return m, m.Validate()
}
func (t *writeTx) PutReferenceMaterialization(m domain.ReferenceMaterialization) error {
	if err := m.Validate(); err != nil {
		return err
	}
	v, err := t.PublishedVersion(m.FromVersionID)
	if err != nil {
		return err
	}
	if v.ManifestSHA != m.ManifestSHA {
		return domain.ErrConflict
	}
	w, err := t.DerivedWork(m.JobID)
	if err != nil {
		return err
	}
	if w.VersionID != m.FromVersionID || w.WorkKind != "references" || w.ExtractorRevision != m.ReferenceRevision {
		return domain.ErrConflict
	}
	old, err := t.ReferenceMaterialization(m.FromVersionID)
	if err == nil {
		if old == m {
			return nil
		}
		if old.ManifestSHA != m.ManifestSHA || m.Revision != old.Revision+1 || m.ReferenceRevision < old.ReferenceRevision {
			return domain.ErrConflict
		}
		if m.ReferenceRevision == old.ReferenceRevision && (m.JobID != old.JobID || old.State != "building") {
			return domain.ErrTransition
		}
		if m.ReferenceRevision > old.ReferenceRevision && m.State != "building" {
			return domain.ErrTransition
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	} else if m.State != "building" || m.Revision != 1 {
		return domain.ErrTransition
	}
	return t.exec(`INSERT INTO reference_materializations VALUES(?,?,?,?,?,?) ON CONFLICT(from_version_id) DO UPDATE SET reference_revision=excluded.reference_revision,state=excluded.state,job_id=excluded.job_id,revision=excluded.revision`, m.FromVersionID, m.ManifestSHA, m.ReferenceRevision, m.State, m.JobID, m.Revision)
}

const referenceColumns = `from_version_id,metadata_reference_id,from_page_path,from_region_id,target_document_id,target_version_id,target_page_path,target_region_id,page_query,page_fragment,relation,label,authored_json`

func scanAuthoredReference(row interface{ Scan(...any) error }) (r domain.AuthoredReference, err error) {
	err = row.Scan(&r.FromVersionID, &r.ID, &r.FromPagePath, &r.FromRegionID, &r.TargetDocumentID, &r.TargetVersionID, &r.TargetPagePath, &r.TargetRegionID, &r.PageQuery, &r.PageFragment, &r.Relation, &r.Label, &r.AuthoredJSON)
	if err != nil {
		return r, notFound(err)
	}
	return r, r.ValidateFields()
}
func (t *writeTx) AuthoredReference(version, id string) (domain.AuthoredReference, error) {
	return scanAuthoredReference(t.tx.QueryRowContext(t.ctx, `SELECT `+referenceColumns+` FROM authored_references WHERE from_version_id=? AND metadata_reference_id=?`, version, id))
}
func (t *writeTx) PutAuthoredReference(prepared app.PreparedReference) error {
	if !prepared.Ready() {
		return domain.ErrUnsupportedDerivedData
	}
	r := prepared.Columns()
	old, err := t.AuthoredReference(r.FromVersionID, r.ID)
	if err == nil {
		raw := old.AuthoredJSON
		old.AuthoredJSON = nil
		if reflect.DeepEqual(old, r) && string(raw) == prepared.JSON() {
			return nil
		}
		return domain.ErrConflict
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	m, err := t.ReferenceMaterialization(r.FromVersionID)
	if err != nil {
		return err
	}
	if m.State != "building" {
		return domain.ErrTransition
	}
	return t.exec(`INSERT INTO authored_references VALUES(?,?,?,?,?,?,?,?,?,?,?,?,CAST(? AS BLOB))`, r.FromVersionID, r.ID, r.FromPagePath, r.FromRegionID, r.TargetDocumentID, r.TargetVersionID, r.TargetPagePath, r.TargetRegionID, r.PageQuery, r.PageFragment, r.Relation, r.Label, prepared.JSON())
}
func referenceLimit(n int) int {
	if n < 1 || n > 64 {
		return 64
	}
	return n
}
func (t *writeTx) referenceRows(query string, args ...any) ([]domain.AuthoredReference, error) {
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.AuthoredReference{}
	for rows.Next() {
		r, e := scanAuthoredReference(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (t *writeTx) AuthoredReferences(version, after string, limit int) ([]domain.AuthoredReference, error) {
	return t.referenceRows(`SELECT `+referenceColumns+` FROM authored_references WHERE from_version_id=? AND metadata_reference_id>? COLLATE BINARY ORDER BY metadata_reference_id COLLATE BINARY LIMIT ?`, version, after, referenceLimit(limit))
}
func (t *writeTx) IncomingReferences(target, afterVersion, afterID string, limit int) ([]domain.AuthoredReference, error) {
	return t.referenceRows(`SELECT `+referenceColumns+` FROM authored_references WHERE target_version_id=? AND (from_version_id,metadata_reference_id)>(?,?) ORDER BY from_version_id COLLATE BINARY,metadata_reference_id COLLATE BINARY LIMIT ?`, target, afterVersion, afterID, referenceLimit(limit))
}

const edgeColumns = `edge_id,from_version_id,metadata_reference_id,from_page_path,from_region_id,target_project_id,target_document_id,target_version_id,target_page_path,target_region_id,from_page_sha256,from_anchor_sha256,target_page_sha256,target_anchor_sha256,anchor_revision`

func (t *writeTx) AuthoredEdge(version, reference string) (e domain.AuthoredEdge, err error) {
	err = t.tx.QueryRowContext(t.ctx, `SELECT `+edgeColumns+` FROM authored_edges WHERE from_version_id=? AND metadata_reference_id=?`, version, reference).Scan(&e.ID, &e.FromVersionID, &e.ReferenceID, &e.FromPagePath, &e.FromRegionID, &e.TargetProjectID, &e.TargetDocumentID, &e.TargetVersionID, &e.TargetPagePath, &e.TargetRegionID, &e.FromPageSHA, &e.FromAnchorSHA, &e.TargetPageSHA, &e.TargetAnchorSHA, &e.AnchorRevision)
	if err != nil {
		return e, notFound(err)
	}
	if err := e.Validate(); err != nil {
		return e, err
	}
	library, err := t.Library()
	if err != nil {
		return e, err
	}
	id, err := domain.ReferenceEdgeID(library.ID, e.FromVersionID, e.ReferenceID)
	if err != nil || id != e.ID {
		return e, domain.ErrUnsupportedDerivedData
	}
	declaration, err := t.AuthoredReference(e.FromVersionID, e.ReferenceID)
	if err != nil {
		return e, err
	}
	if !edgeMatchesReference(declaration, e) {
		return e, domain.ErrUnsupportedDerivedData
	}
	return e, nil
}
func (t *writeTx) PutAuthoredEdge(e domain.AuthoredEdge) error {
	if err := e.Validate(); err != nil {
		return err
	}
	library, err := t.Library()
	if err != nil {
		return err
	}
	id, err := domain.ReferenceEdgeID(library.ID, e.FromVersionID, e.ReferenceID)
	if err != nil {
		return err
	}
	if id != e.ID {
		return domain.ErrUnsupportedDerivedData
	}
	r, err := t.AuthoredReference(e.FromVersionID, e.ReferenceID)
	if err != nil {
		return err
	}
	if !edgeMatchesReference(r, e) {
		return domain.ErrConflict
	}
	old, err := t.AuthoredEdge(e.FromVersionID, e.ReferenceID)
	if err == nil {
		revision := old.AnchorRevision
		old.AnchorRevision = e.AnchorRevision
		if !reflect.DeepEqual(old, e) || e.AnchorRevision < revision {
			return domain.ErrConflict
		}
		return t.exec(`UPDATE authored_edges SET anchor_revision=? WHERE edge_id=?`, e.AnchorRevision, e.ID)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	target, err := t.PublishedVersion(e.TargetVersionID)
	if err != nil {
		return err
	}
	doc, err := t.Document(target.DocumentID)
	if err != nil {
		return err
	}
	if target.DocumentID != e.TargetDocumentID || doc.ProjectID != e.TargetProjectID || doc.Lifecycle == "purged" {
		return domain.ErrConflict
	}
	return t.exec(`INSERT INTO authored_edges VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, e.ID, e.FromVersionID, e.ReferenceID, e.FromPagePath, e.FromRegionID, e.TargetProjectID, e.TargetDocumentID, e.TargetVersionID, e.TargetPagePath, e.TargetRegionID, e.FromPageSHA, e.FromAnchorSHA, e.TargetPageSHA, e.TargetAnchorSHA, e.AnchorRevision)
}

func edgeMatchesReference(r domain.AuthoredReference, e domain.AuthoredEdge) bool {
	return r.TargetDocumentID == e.TargetDocumentID && r.TargetVersionID == e.TargetVersionID && reflect.DeepEqual(r.FromRegionID, e.FromRegionID) && reflect.DeepEqual(r.TargetRegionID, e.TargetRegionID) && (r.FromPagePath == nil) == (e.FromPagePath == nil) && (r.TargetPagePath == nil) == (e.TargetPagePath == nil)
}

func (t *writeTx) ReferenceLifecycle(document string) (state domain.ReferenceLifecycle, err error) {
	err = t.tx.QueryRowContext(t.ctx, `SELECT project_id,lifecycle FROM documents WHERE document_id=?`, document).Scan(&state.ProjectID, &state.Lifecycle)
	return state, notFound(err)
}
