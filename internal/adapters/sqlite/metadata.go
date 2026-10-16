package sqlite

import (
	"context"
	"haeryong.local/haeryong/internal/domain"
)

func (s *Store) Project(ctx context.Context, id string) (domain.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := domain.Project{ID: id}
	err := s.db.QueryRowContext(ctx, "SELECT name,lifecycle,revision,permissions_revision FROM projects WHERE project_id=?", id).Scan(&p.Name, &p.Lifecycle, &p.Revision, &p.PermissionsRevision)
	return p, notFound(err)
}
func (s *Store) Document(ctx context.Context, id string) (domain.Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := domain.Document{ID: id}
	err := s.db.QueryRowContext(ctx, "SELECT project_id,title,lifecycle,coalesce(latest_version_id,''),revision FROM documents WHERE document_id=?", id).Scan(&d.ProjectID, &d.Title, &d.Lifecycle, &d.LatestVersionID, &d.Revision)
	return d, notFound(err)
}
func (s *Store) Version(ctx context.Context, id string) (domain.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := domain.Version{ID: id}
	err := s.db.QueryRowContext(ctx, "SELECT document_id,content_set_id,coalesce(base_version_id,''),title_snapshot,viewer_profile,source_basis,coalesce(sequence,0),default_page,metadata,payload_sha256,manifest_sha256,coalesce(supplied_metadata_hash,'') FROM document_versions WHERE version_id=?", id).Scan(&v.DocumentID, &v.ContentSetID, &v.BaseVersionID, &v.Title, &v.ViewerProfile, &v.SourceBasis, &v.Sequence, &v.DefaultPage, &v.Metadata, &v.PayloadSHA, &v.ManifestSHA, &v.MetadataHash)
	return v, notFound(err)
}
func (s *Store) Page(ctx context.Context, version, path string) (domain.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := domain.Page{VersionID: version, Path: path}
	err := s.db.QueryRowContext(ctx, "SELECT metadata FROM version_pages WHERE version_id=? AND page_path=?", version, path).Scan(&p.Metadata)
	return p, notFound(err)
}
func (s *Store) Region(ctx context.Context, version, path, id string) (domain.Region, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := domain.Region{VersionID: version, PagePath: path, ID: id}
	err := s.db.QueryRowContext(ctx, "SELECT metadata FROM regions WHERE version_id=? AND page_path=? AND region_id=?", version, path, id).Scan(&r.Metadata)
	return r, notFound(err)
}
func (s *Store) Evidence(ctx context.Context, version, id string) (domain.Evidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := domain.Evidence{VersionID: version, ID: id}
	err := s.db.QueryRowContext(ctx, "SELECT source_id,metadata FROM evidence WHERE version_id=? AND evidence_id=?", version, id).Scan(&e.SourceID, &e.Metadata)
	return e, notFound(err)
}
func (s *Store) Configuration(ctx context.Context) (domain.Configuration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c domain.Configuration
	err := s.db.QueryRowContext(ctx, "SELECT config_revision,validated_json,actor FROM config_revisions ORDER BY config_revision DESC LIMIT 1").Scan(&c.Revision, &c.Value, &c.Actor)
	return c, notFound(err)
}
func (t *writeTx) LinkRegionEvidence(version, page, region, evidence string) error {
	if err := t.mutableVersion(version); err != nil {
		return err
	}
	return t.exec("INSERT INTO region_evidence VALUES(?,?,?,?)", version, page, region, evidence)
}

func (t *writeTx) Scope(projectID string) (domain.ScopeState, error) {
	var state domain.ScopeState
	if err := t.tx.QueryRowContext(t.ctx, "SELECT max(config_revision) FROM config_revisions").Scan(&state.ConfigRevision); err != nil {
		return state, err
	}
	if projectID != "" {
		state.Project.ID = projectID
		if err := t.tx.QueryRowContext(t.ctx, "SELECT name,lifecycle,revision,permissions_revision FROM projects WHERE project_id=?", projectID).Scan(&state.Project.Name, &state.Project.Lifecycle, &state.Project.Revision, &state.Project.PermissionsRevision); err != nil {
			return state, notFound(err)
		}
	}
	return state, nil
}
