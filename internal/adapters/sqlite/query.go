package sqlite

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
)

func scopeValues(scope domain.CredentialScope) (bool, bool, string, string) {
	projects, _ := json.Marshal(scope.ProjectIDs)
	versions, _ := json.Marshal(scope.VersionIDs)
	projectRead := false
	for _, a := range scope.Actions {
		if a == "project.read" {
			projectRead = true
		}
	}
	return scope.Kind == "owner", projectRead, string(projects), string(versions)
}

func (t *writeTx) QueryProject(id string) (domain.QueryProject, error) {
	var p domain.QueryProject
	p.ID = id
	err := t.tx.QueryRowContext(t.ctx, "SELECT name,lifecycle,revision,permissions_revision,updated_at FROM projects WHERE project_id=?", id).Scan(&p.Name, &p.Lifecycle, &p.Revision, &p.PermissionsRevision, &p.UpdatedAt)
	return p, notFound(err)
}
func (t *writeTx) QueryDocument(id string) (domain.QueryDocument, error) {
	var d domain.QueryDocument
	d.ID = id
	err := t.tx.QueryRowContext(t.ctx, "SELECT project_id,title,lifecycle,coalesce(latest_version_id,''),revision,updated_at FROM documents WHERE document_id=?", id).Scan(&d.ProjectID, &d.Title, &d.Lifecycle, &d.LatestVersionID, &d.Revision, &d.UpdatedAt)
	return d, notFound(err)
}

func (t *writeTx) QueryProjects(scope domain.CredentialScope, afterUpdated, afterID string, limit int) ([]domain.QueryProject, error) {
	owner, read, projects, _ := scopeValues(scope)
	rows, err := t.tx.QueryContext(t.ctx, "SELECT project_id,name,lifecycle,revision,permissions_revision,updated_at FROM projects WHERE (? OR (? AND project_id IN (SELECT value FROM json_each(?)))) AND (updated_at,project_id)>(?,?) ORDER BY updated_at COLLATE BINARY,project_id COLLATE BINARY LIMIT ?", owner, read, projects, afterUpdated, afterID, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.QueryProject{}
	for rows.Next() {
		var p domain.QueryProject
		if err := rows.Scan(&p.ID, &p.Name, &p.Lifecycle, &p.Revision, &p.PermissionsRevision, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (t *writeTx) QueryDocuments(scope domain.CredentialScope, project, lifecycle, afterUpdated, afterID string, limit int) ([]domain.QueryDocument, error) {
	owner, read, projects, _ := scopeValues(scope)
	rows, err := t.tx.QueryContext(t.ctx, "SELECT d.document_id,d.project_id,d.title,d.lifecycle,coalesce(d.latest_version_id,''),d.revision,d.updated_at FROM documents d JOIN projects p ON p.project_id=d.project_id WHERE (? OR (? AND d.project_id IN (SELECT value FROM json_each(?)))) AND d.lifecycle NOT IN ('trash','purged') AND (?='' OR d.project_id=?) AND (?='' OR d.lifecycle=?) AND (d.updated_at,d.document_id)>(?,?) ORDER BY d.updated_at COLLATE BINARY,d.document_id COLLATE BINARY LIMIT ?", owner, read, projects, project, project, lifecycle, lifecycle, afterUpdated, afterID, batch(limit))
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

func (t *writeTx) QueryVersions(scope domain.CredentialScope, document string, afterSequence int64, limit int) ([]domain.Version, error) {
	owner, _, projects, versions := scopeValues(scope)
	read := false
	for _, action := range scope.Actions {
		if action == "version.read" {
			read = true
		}
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT v.version_id,v.document_id,v.content_set_id,coalesce(v.base_version_id,''),v.title_snapshot,v.viewer_profile,v.source_basis,v.sequence,v.default_page,v.metadata,v.payload_sha256,v.manifest_sha256,coalesce(v.supplied_metadata_hash,'') FROM document_versions v JOIN documents d ON d.document_id=v.document_id WHERE (? OR (? AND v.version_id IN (SELECT value FROM json_each(?)) AND d.project_id IN (SELECT value FROM json_each(?)))) AND v.document_id=? AND d.lifecycle NOT IN ('trash','purged') AND v.sequence>? ORDER BY v.sequence LIMIT ?", owner, read, versions, projects, document, afterSequence, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Version{}
	for rows.Next() {
		var v domain.Version
		if err := rows.Scan(&v.ID, &v.DocumentID, &v.ContentSetID, &v.BaseVersionID, &v.Title, &v.ViewerProfile, &v.SourceBasis, &v.Sequence, &v.DefaultPage, &v.Metadata, &v.PayloadSHA, &v.ManifestSHA, &v.MetadataHash); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (t *writeTx) QueryEntry(set, path string) (domain.Entry, error) {
	e := domain.Entry{ContentSetID: set, Path: path}
	err := t.tx.QueryRowContext(t.ctx, "SELECT kind,coalesce(content_hash,''),byte_size,coalesce(exact_link_text,'') FROM version_entries WHERE content_set_id=? AND relative_path=? COLLATE BINARY", set, path).Scan(&e.Kind, &e.Hash, &e.Size, &e.LinkText)
	return e, notFound(err)
}

func (t *writeTx) QueryEntries(set, after, directory, kind string, limit int) ([]domain.Entry, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT relative_path,kind,coalesce(content_hash,''),byte_size,coalesce(exact_link_text,'') FROM version_entries WHERE content_set_id=? AND relative_path>? COLLATE BINARY AND (?='' OR substr(relative_path,1,length(?)+1)=?||'/') AND (?='' OR kind=?) ORDER BY relative_path COLLATE BINARY LIMIT ?", set, after, directory, directory, directory, kind, kind, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Entry{}
	for rows.Next() {
		e := domain.Entry{ContentSetID: set}
		if err := rows.Scan(&e.Path, &e.Kind, &e.Hash, &e.Size, &e.LinkText); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (t *writeTx) QuerySource(version, id string) (domain.Source, error) {
	s := domain.Source{VersionID: version, ID: id}
	err := t.tx.QueryRowContext(t.ctx, "SELECT metadata FROM version_sources WHERE version_id=? AND source_id=?", version, id).Scan(&s.Metadata)
	return s, notFound(err)
}

func (t *writeTx) QueryEvidence(version, id string) (domain.Evidence, error) {
	e := domain.Evidence{VersionID: version, ID: id}
	err := t.tx.QueryRowContext(t.ctx, "SELECT source_id,metadata FROM evidence WHERE version_id=? AND evidence_id=?", version, id).Scan(&e.SourceID, &e.Metadata)
	return e, notFound(err)
}
