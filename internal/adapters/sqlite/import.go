package sqlite

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/domain"
)

func (t *writeTx) Library() (domain.Library, error) {
	var l domain.Library
	err := t.tx.QueryRowContext(t.ctx, "SELECT library_id,profile_id,schema_revision,runtime_generation,stream_epoch,auth_epoch,unfiled_project_id FROM libraries WHERE singleton=1").Scan(&l.ID, &l.ProfileID, &l.SchemaRevision, &l.RuntimeGeneration, &l.StreamEpoch, &l.AuthEpoch, &l.UnfiledProjectID)
	return l, err
}
func (t *writeTx) Upload(id string) (domain.Upload, error) { return upload(t.ctx, t.tx, id) }
func (t *writeTx) Uploads(after, state string, unadopted bool, limit int) ([]domain.Upload, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT upload_id FROM uploads WHERE upload_id>? AND (?='' AND state!='abandoned' OR state=?) AND (?=0 OR NOT EXISTS(SELECT 1 FROM import_intents i JOIN jobs j ON j.job_id=i.job_id WHERE json_extract(i.value,'$.UploadID')=uploads.upload_id AND j.state='completed')) ORDER BY upload_id LIMIT ?", after, state, state, unadopted, batch(limit))
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []domain.Upload{}
	for _, id := range ids {
		u, err := t.Upload(id)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}
func (t *writeTx) UploadEntries(id, after string, limit int) ([]domain.UploadEntry, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT e.relative_path,e.kind,e.link_text,e.expected_size,e.final,coalesce((SELECT sum(byte_size) FROM upload_ranges r WHERE r.upload_id=e.upload_id AND r.relative_path=e.relative_path),0) FROM upload_entries e WHERE e.upload_id=? AND e.relative_path>? COLLATE BINARY ORDER BY e.relative_path COLLATE BINARY LIMIT ?", id, after, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.UploadEntry{}
	for rows.Next() {
		e := domain.UploadEntry{UploadID: id}
		if err := rows.Scan(&e.Path, &e.Kind, &e.LinkText, &e.ExpectedSize, &e.Final, &e.Size); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (t *writeTx) PutUploadEntry(e domain.UploadEntry) error {
	var kind, link string
	var expected *int64
	var final bool
	err := t.tx.QueryRowContext(t.ctx, "SELECT kind,link_text,expected_size,final FROM upload_entries WHERE upload_id=? AND relative_path=?", e.UploadID, e.Path).Scan(&kind, &link, &expected, &final)
	if err == nil {
		if kind != e.Kind || link != e.LinkText || (expected != nil && e.ExpectedSize != nil && *expected != *e.ExpectedSize) {
			return domain.ErrConflict
		}
		if (final || !e.Final) && (expected != nil || e.ExpectedSize == nil) {
			return nil
		}
		if final && !e.Final {
			return nil
		}
		if err := t.exec("UPDATE upload_entries SET expected_size=coalesce(expected_size,?),final=max(final,?) WHERE upload_id=? AND relative_path=?", e.ExpectedSize, e.Final, e.UploadID, e.Path); err != nil {
			return err
		}
		return t.exec("UPDATE uploads SET checkpoint=json_set(checkpoint,'$.generation',json_extract(checkpoint,'$.generation')+1) WHERE upload_id=?", e.UploadID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// A represented file/link may never shadow an implied directory or vice versa.
	var conflict int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM upload_entries WHERE upload_id=? AND ((relative_path=? AND kind!=?) OR (?!='directory' AND substr(relative_path,1,length(?)+1)=?||'/') OR (kind!='directory' AND substr(?,1,length(relative_path)+1)=relative_path||'/'))", e.UploadID, e.Path, e.Kind, e.Kind, e.Path, e.Path, e.Path).Scan(&conflict); err != nil {
		return err
	}
	if conflict != 0 {
		return domain.ErrConflict
	}
	if err := t.exec("INSERT INTO upload_entries VALUES(?,?,?,?,?,?)", e.UploadID, e.Path, e.Kind, e.LinkText, e.ExpectedSize, e.Final); err != nil {
		return err
	}
	return t.exec("UPDATE uploads SET checkpoint=json_set(checkpoint,'$.generation',json_extract(checkpoint,'$.generation')+1) WHERE upload_id=?", e.UploadID)
}
func (t *writeTx) Ranges(id, path string, offset int64, limit int) ([]domain.UploadRange, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT relative_path,range_offset,byte_size,content_hash FROM upload_ranges WHERE upload_id=? AND (relative_path>? COLLATE BINARY OR (relative_path=? AND range_offset>?)) ORDER BY relative_path COLLATE BINARY,range_offset LIMIT ?", id, path, path, offset, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.UploadRange{}
	for rows.Next() {
		r := domain.UploadRange{UploadID: id}
		if err := rows.Scan(&r.Path, &r.Offset, &r.Size, &r.Hash); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (t *writeTx) TransferDigest(id string) (string, error) {
	h := sha256.New()
	rows, err := t.tx.QueryContext(t.ctx, "SELECT relative_path,kind,link_text,expected_size,final FROM upload_entries WHERE upload_id=? ORDER BY relative_path COLLATE BINARY", id)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var path, kind, link string
		var size *int64
		var final bool
		if err := rows.Scan(&path, &kind, &link, &size, &final); err != nil {
			rows.Close()
			return "", err
		}
		b, _ := json.Marshal([]any{path, kind, link, size, final})
		h.Write(b)
		h.Write([]byte{'\n'})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	rows, err = t.tx.QueryContext(t.ctx, "SELECT relative_path,range_offset,byte_size,content_hash FROM upload_ranges WHERE upload_id=? ORDER BY relative_path COLLATE BINARY,range_offset", id)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var p, hash string
		var o, n int64
		if err := rows.Scan(&p, &o, &n, &hash); err != nil {
			return "", err
		}
		b, _ := json.Marshal([]any{p, o, n, hash})
		h.Write(b)
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)), rows.Err()
}
func (t *writeTx) UploadComplete(id string) error {
	u, err := t.Upload(id)
	if err != nil {
		return err
	}
	var invalid int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM upload_ranges r LEFT JOIN upload_entries e ON e.upload_id=r.upload_id AND e.relative_path=r.relative_path WHERE r.upload_id=? AND (e.kind IS NULL OR e.kind!='file' OR (e.expected_size IS NOT NULL AND r.range_offset+r.byte_size>e.expected_size))", id).Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return domain.ErrTransition
	}
	if u.InputMode != "directory" {
		if err := t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM upload_entries WHERE upload_id=? AND kind='file' AND relative_path='_input'", id).Scan(&invalid); err != nil {
			return err
		}
		if invalid != 1 {
			return domain.ErrTransition
		}
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT e.relative_path,e.expected_size,e.final,coalesce((SELECT sum(byte_size) FROM upload_ranges r WHERE r.upload_id=e.upload_id AND r.relative_path=e.relative_path),0) FROM upload_entries e WHERE e.upload_id=? AND e.kind='file'", id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var length *int64
		var final bool
		var size int64
		if err := rows.Scan(&p, &length, &final, &size); err != nil {
			return err
		}
		if !final || length == nil || *length != size {
			return domain.ErrTransition
		}
	}
	return rows.Err()
}
func (t *writeTx) TouchUpload(id, at string) error {
	h, err := t.TransferDigest(id)
	if err != nil {
		return err
	}
	return t.exec("UPDATE uploads SET checkpoint=json_set(checkpoint,'$.inventory_sha256',?),updated_at=? WHERE upload_id=? AND json_extract(checkpoint,'$.inventory_sha256')<>?", h, at, id, h)
}
func (t *writeTx) SetSealOperation(id, op string) error {
	return t.exec("UPDATE uploads SET seal_operation_id=coalesce(seal_operation_id,?) WHERE upload_id=?", op, id)
}
func (t *writeTx) ContentSet(id string) (string, bool, error) {
	var h string
	var sealed bool
	err := t.tx.QueryRowContext(t.ctx, "SELECT payload_sha256,sealed FROM content_sets WHERE content_set_id=?", id).Scan(&h, &sealed)
	return h, sealed, notFound(err)
}
func (t *writeTx) Entries(id, after string, limit int) ([]domain.Entry, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT relative_path,kind,coalesce(content_hash,''),byte_size,coalesce(exact_link_text,'') FROM version_entries WHERE content_set_id=? AND relative_path>? COLLATE BINARY ORDER BY relative_path COLLATE BINARY LIMIT ?", id, after, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Entry{}
	for rows.Next() {
		e := domain.Entry{ContentSetID: id}
		if err := rows.Scan(&e.Path, &e.Kind, &e.Hash, &e.Size, &e.LinkText); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (t *writeTx) SealContentSetDigest(id, hash string) error {
	if !hashPattern.MatchString(hash) {
		return domain.ErrConflict
	}
	if err := t.exec("UPDATE content_sets SET payload_sha256=? WHERE content_set_id=? AND sealed=0", hash, id); err != nil {
		return err
	}
	return t.SealContentSet(id)
}
func (t *writeTx) PutUploadObject(id, hash string) error {
	return t.exec("UPDATE uploads SET sealed_object_hash=? WHERE upload_id=?", hash, id)
}
func (t *writeTx) PutImportIntent(i domain.ImportIntent) error {
	return t.exec("INSERT INTO import_intents VALUES(?,?)", i.JobID, jsonBytes(i))
}
func (t *writeTx) ImportIntent(id string) (domain.ImportIntent, error) {
	var i domain.ImportIntent
	var b []byte
	err := t.tx.QueryRowContext(t.ctx, "SELECT value FROM import_intents WHERE job_id=?", id).Scan(&b)
	if err != nil {
		return i, notFound(err)
	}
	return i, json.Unmarshal(b, &i)
}
func (t *writeTx) SetJobResult(id string, b []byte) error {
	return t.exec("UPDATE jobs SET result=? WHERE job_id=?", b, id)
}
func (t *writeTx) PutImportMetadata(id, kind, key string, b []byte) error {
	var n int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM import_metadata WHERE job_id=? AND kind=? AND item_id=?", id, kind, key).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return domain.ErrConflict
	}
	return t.exec("INSERT INTO import_metadata VALUES(?,?,?,?)", id, kind, key, b)
}
func (t *writeTx) ImportMetadata(id, kind, after string, limit int) ([]domain.Resource, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT item_id,value FROM import_metadata WHERE job_id=? AND kind=? AND item_id>? COLLATE BINARY ORDER BY item_id COLLATE BINARY LIMIT ?", id, kind, after, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Resource{}
	for rows.Next() {
		r := domain.Resource{Kind: kind}
		if err := rows.Scan(&r.ID, &r.Value); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (t *writeTx) PublishedVersion(id string) (domain.Version, error) {
	v, err := t.Version(id)
	if err == nil && v.Sequence == 0 {
		return v, domain.ErrNotFound
	}
	return v, err
}

const uploadHashes = "SELECT content_hash FROM upload_ranges WHERE upload_id=? UNION SELECT content_hash FROM version_entries WHERE content_set_id IN(SELECT coalesce(sealed_content_set_id,seal_job_id) FROM uploads WHERE upload_id=?) AND content_hash IS NOT NULL"

func (t *writeTx) UploadPins(id, after string, limit int) ([]domain.Pin, error) {
	rows, err := t.tx.QueryContext(t.ctx, "WITH hashes AS ("+uploadHashes+"), allpins AS(SELECT pin_id AS id,owner_kind AS kind,owner_id AS owner,content_hash AS hash FROM object_pins WHERE content_hash IN hashes UNION ALL SELECT 'reference:'||owner_kind||':'||owner_id||':'||content_hash,CASE WHEN owner_kind='content_set' AND NOT EXISTS(SELECT 1 FROM document_versions v WHERE v.content_set_id=owner_id AND v.sequence IS NOT NULL) THEN 'staging' ELSE 'adopted' END,owner_id,content_hash FROM object_references WHERE content_hash IN hashes) SELECT id,kind,owner,hash FROM allpins WHERE id>? COLLATE BINARY ORDER BY id COLLATE BINARY LIMIT ?", id, id, after, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Pin{}
	for rows.Next() {
		var p domain.Pin
		if err := rows.Scan(&p.ID, &p.Kind, &p.OwnerID, &p.Hash); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (t *writeTx) UploadStats(id string) (domain.UploadStats, error) {
	var out domain.UploadStats
	var size string
	err := t.tx.QueryRowContext(t.ctx, "WITH hashes AS ("+uploadHashes+") SELECT coalesce(cast(sum(byte_size) AS TEXT),'0') FROM content_objects WHERE content_hash IN hashes", id, id).Scan(&size)
	if err != nil {
		return out, err
	}
	out.StagingBytes = size
	after := ""
	for {
		pins, err := t.UploadPins(id, after, 64)
		if err != nil {
			return out, err
		}
		if len(pins) == 0 {
			break
		}
		for _, p := range pins {
			switch p.Kind {
			case "adopted":
				out.Adopted++
			case "job", "input", "upload_read":
				out.Active++
			case "backup", "recovery", "draft":
				out.Recovery++
			default:
				out.Staging++
			}
			after = p.ID
		}
	}
	return out, nil
}

func (t *writeTx) DiscardContentSet(id string) error {
	var n int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM document_versions WHERE content_set_id=?", id).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return domain.ErrPinned
	}
	if err := t.exec("DELETE FROM version_entries WHERE content_set_id=?", id); err != nil {
		return err
	}
	if err := t.exec("DELETE FROM object_references WHERE owner_kind='content_set' AND owner_id=?", id); err != nil {
		return err
	}
	return t.exec("DELETE FROM content_sets WHERE content_set_id=?", id)
}
func (t *writeTx) FinalizeImport(i domain.ImportIntent, v domain.Version) error {
	if i.NewDocument {
		if err := t.CreateDocument(domain.Document{ID: i.DocumentID, ProjectID: i.ProjectID, Title: i.Title}); err != nil {
			return err
		}
	}
	if err := t.PutVersion(v); err != nil {
		return err
	}
	if err := t.exec("INSERT INTO version_pages SELECT ?,relative_path,NULL FROM version_entries WHERE content_set_id=? AND kind='file' AND (lower(substr(relative_path,-5))='.html' OR lower(substr(relative_path,-4))='.htm')", v.ID, v.ContentSetID); err != nil {
		return err
	}
	if err := t.exec("INSERT INTO version_pages SELECT ?,json_extract(value,'$.page_path'),value FROM import_metadata WHERE job_id=? AND kind='pages' ON CONFLICT(version_id,page_path) DO UPDATE SET metadata=excluded.metadata", v.ID, i.JobID); err != nil {
		return err
	}
	if err := t.exec("INSERT INTO version_sources SELECT ?,item_id,value FROM import_metadata WHERE job_id=? AND kind='sources'", v.ID, i.JobID); err != nil {
		return err
	}
	if err := t.exec("INSERT INTO evidence SELECT ?,item_id,json_extract(value,'$.source_id'),value FROM import_metadata WHERE job_id=? AND kind='evidence'", v.ID, i.JobID); err != nil {
		return err
	}
	if err := t.exec("INSERT INTO regions SELECT ?,json_extract(value,'$.page_path'),json_extract(value,'$.id'),value FROM import_metadata WHERE job_id=? AND kind='regions'", v.ID, i.JobID); err != nil {
		return err
	}
	if err := t.exec("INSERT INTO region_evidence SELECT ?,json_extract(r.value,'$.page_path'),json_extract(r.value,'$.id'),e.value FROM import_metadata r JOIN json_each(r.value,'$.evidence_ids') e WHERE r.job_id=? AND r.kind='regions'", v.ID, i.JobID); err != nil {
		return err
	}
	// Authored references remain immutable unresolved metadata, never fabricated edges.
	if err := t.PublishVersion(v.ID, i.DocumentRevision); err != nil {
		return err
	}
	if v.MetadataHash != "" {
		if err := t.Adopt(domain.Reference{Kind: "version_metadata", ID: v.ID, Hash: v.MetadataHash}, ""); err != nil {
			return err
		}
	}
	if v.ManifestSHA != "" {
		if err := t.Adopt(domain.Reference{Kind: "version_manifest", ID: v.ID, Hash: v.ManifestSHA}, ""); err != nil {
			return err
		}
	}
	t.publication = true
	return nil
}
func (t *writeTx) WorkJobs(limit int) ([]domain.Job, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT job_id FROM jobs WHERE kind IN ('upload_seal','import') AND state IN ('queued','interrupted','cancelling') ORDER BY job_id LIMIT ?", batch(limit))
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []domain.Job{}
	for _, id := range ids {
		j, err := t.Job(id)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}
func (t *writeTx) StageEntry(e domain.Entry) error {
	var kind string
	err := t.tx.QueryRowContext(t.ctx, "SELECT kind FROM version_entries WHERE content_set_id=? AND relative_path=?", e.ContentSetID, e.Path).Scan(&kind)
	if err == nil {
		if kind == "directory" && e.Kind == "directory" {
			return nil
		}
		return domain.ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return t.PutEntry(e)
}
func (t *writeTx) PinContentSet(job, set string) error {
	var n int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT count(*) FROM version_entries e LEFT JOIN content_objects o ON e.content_hash=o.content_hash WHERE e.content_set_id=? AND e.kind='file' AND (o.content_hash IS NULL OR o.state!='ready')", set).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return domain.ErrDeleting
	}
	return t.exec("INSERT INTO object_pins SELECT 'job-content:'||?||':'||content_hash,'job',?,content_hash FROM version_entries WHERE content_set_id=? AND kind='file' GROUP BY content_hash", job, job, set)
}
func (t *writeTx) ReleaseOwnerPins(kind, id string) error {
	return t.exec("DELETE FROM object_pins WHERE owner_kind=? AND owner_id=?", kind, id)
}
func (t *writeTx) DraftTarget(id string) (domain.DraftTarget, error) {
	out := domain.DraftTarget{ID: id}
	r, e := resource(t.ctx, t.tx, "draft", id)
	if e != nil {
		return out, e
	}
	var value struct {
		ProjectID  string  `json:"project_id"`
		DocumentID *string `json:"document_id"`
		State      string  `json:"state"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(r.Value, &fields) != nil {
		return out, domain.ErrNotFound
	}
	if _, ok := fields["document_id"]; !ok {
		return out, domain.ErrNotFound
	}
	if json.Unmarshal(r.Value, &value) != nil || value.ProjectID == "" || value.State == "" {
		return out, domain.ErrNotFound
	}
	out.ProjectID = value.ProjectID
	out.DocumentID = value.DocumentID
	out.State = value.State
	out.Revision = r.Revision
	return out, nil
}
func (t *writeTx) AnnotationTarget(id string) (domain.AnnotationTarget, error) {
	out := domain.AnnotationTarget{ID: id}
	r, e := resource(t.ctx, t.tx, "annotation", id)
	if e != nil {
		return out, e
	}
	var v struct {
		VersionID string  `json:"version_id"`
		PagePath  *string `json:"page_path"`
		Deleted   bool    `json:"deleted"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(r.Value, &fields) != nil {
		return out, domain.ErrNotFound
	}
	if _, ok := fields["page_path"]; !ok {
		return out, domain.ErrNotFound
	}
	if json.Unmarshal(r.Value, &v) != nil || v.VersionID == "" {
		return out, domain.ErrNotFound
	}
	out.VersionID = v.VersionID
	out.PagePath = v.PagePath
	out.Deleted = v.Deleted
	out.Revision = r.Revision
	return out, nil
}
func (t *writeTx) Page(version, path string) (domain.Page, error) {
	out := domain.Page{VersionID: version, Path: path}
	e := t.tx.QueryRowContext(t.ctx, "SELECT metadata FROM version_pages WHERE version_id=? AND page_path=?", version, path).Scan(&out.Metadata)
	return out, notFound(e)
}

func (t *writeTx) ConfigureJobScope(j domain.Job) error {
	return t.exec("UPDATE jobs SET scope=?,permissions_revisions=?,config_revision=? WHERE job_id=? AND state='queued'", j.Scope, jsonBytes(j.Permissions), j.ConfigRevision, j.ID)
}
func (t *writeTx) ReclaimAbandonedUpload(id string) error {
	u, e := t.Upload(id)
	if e != nil {
		return e
	}
	if u.State != "abandoned" {
		return nil
	}
	set := u.ContentSetID
	if set == "" {
		set = u.SealJobID
	}
	if set == "" {
		return nil
	}
	var n int
	if e := t.tx.QueryRowContext(t.ctx, "SELECT (SELECT count(*) FROM document_versions WHERE content_set_id=?)+(SELECT count(*) FROM import_intents i JOIN jobs j ON j.job_id=i.job_id WHERE json_extract(i.value,'$.ContentSetID')=? AND j.state NOT IN ('failed','cancelled'))+(SELECT count(*) FROM jobs WHERE job_id=? AND state NOT IN ('completed','failed','cancelled'))", set, set, u.SealJobID).Scan(&n); e != nil {
		return e
	}
	if n > 0 {
		return nil
	}
	if e := t.exec("DELETE FROM version_entries WHERE content_set_id=?", set); e != nil {
		return e
	}
	return t.exec("DELETE FROM object_references WHERE owner_kind='content_set' AND owner_id=?", set)
}

func (t *writeTx) ValidateImportMetadata(job, set string) (string, error) {
	checks := []struct{ query, field string }{
		{"SELECT count(*) FROM import_metadata m WHERE m.job_id=? AND m.kind='pages' AND NOT EXISTS(SELECT 1 FROM version_entries e WHERE e.content_set_id=? AND e.relative_path=json_extract(m.value,'$.page_path') AND e.kind='file')", "/pages/page_path"},
		{"SELECT count(*) FROM import_metadata m WHERE m.job_id=? AND m.kind='sources' AND json_extract(m.value,'$.retention_mode')='selected_snapshot' AND NOT EXISTS(SELECT 1 FROM version_entries e WHERE e.content_set_id=? AND e.relative_path=json_extract(m.value,'$.asset_path') AND e.kind='file' AND (json_extract(m.value,'$.retention_mode')!='selected_snapshot' OR coalesce(json_extract(m.value,'$.bytes_sha256'),json_extract(m.value,'$.snapshot_sha256'))=e.content_hash))", "/sources/asset_path"},
		{"SELECT count(*) FROM import_metadata m WHERE m.job_id=? AND m.kind='evidence' AND NOT EXISTS(SELECT 1 FROM import_metadata s WHERE s.job_id=m.job_id AND s.kind='sources' AND s.item_id=json_extract(m.value,'$.source_id')) AND ? IS NOT NULL", "/evidence/source_id"},
		{"SELECT count(*) FROM import_metadata m WHERE m.job_id=? AND m.kind='regions' AND NOT EXISTS(SELECT 1 FROM version_entries e WHERE e.content_set_id=? AND e.relative_path=json_extract(m.value,'$.page_path') AND e.kind='file' AND (lower(substr(e.relative_path,-5))='.html' OR lower(substr(e.relative_path,-4))='.htm'))", "/regions/page_path"},
		{"SELECT count(*) FROM import_metadata m JOIN json_each(m.value,'$.evidence_ids') e WHERE m.job_id=? AND m.kind='regions' AND NOT EXISTS(SELECT 1 FROM import_metadata v WHERE v.job_id=m.job_id AND v.kind='evidence' AND v.item_id=e.value) AND ? IS NOT NULL", "/regions/evidence_ids"},
	}
	for _, check := range checks {
		var count int
		if err := t.tx.QueryRowContext(t.ctx, check.query, job, set).Scan(&count); err != nil {
			return "", err
		}
		if count != 0 {
			return check.field, nil
		}
	}
	return "", nil
}

func (t *writeTx) CreateMetadataIndex(i domain.MetadataIndex) error {
	if err := t.exec("INSERT INTO metadata_indexes(index_id,principal_id,metadata_hash,content_set_id)VALUES(?,?,?,?)", i.ID, i.PrincipalID, i.Hash, i.ContentSetID); err != nil {
		return err
	}
	return t.AcquirePin(domain.Pin{ID: "metadata-index:" + i.ID, Kind: "metadata_prepare", OwnerID: i.ID, Hash: i.Hash})
}
func (t *writeTx) MetadataIndex(id string) (domain.MetadataIndex, error) {
	i := domain.MetadataIndex{ID: id}
	var job sql.NullString
	err := t.tx.QueryRowContext(t.ctx, "SELECT principal_id,metadata_hash,content_set_id,job_id,complete FROM metadata_indexes WHERE index_id=?", id).Scan(&i.PrincipalID, &i.Hash, &i.ContentSetID, &job, &i.Complete)
	i.JobID = job.String
	return i, notFound(err)
}
func (t *writeTx) CompleteMetadataIndex(id string) error {
	return t.exec("UPDATE metadata_indexes SET complete=1 WHERE index_id=? AND job_id IS NULL", id)
}
func (t *writeTx) ReleaseMetadataPreparation(id string) error {
	if err := t.exec("DELETE FROM object_pins WHERE pin_id=? AND EXISTS(SELECT 1 FROM metadata_indexes WHERE index_id=? AND job_id IS NULL)", "metadata-index:"+id, id); err != nil {
		return err
	}
	return t.exec("DELETE FROM metadata_indexes WHERE index_id=? AND job_id IS NULL", id)
}
func (t *writeTx) LinkMetadataIndex(id, job string) error {
	r, err := t.tx.ExecContext(t.ctx, "UPDATE metadata_indexes SET index_id=?,job_id=? WHERE index_id=? AND job_id IS NULL AND complete=1", job, job, id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n != 1 {
		return domain.ErrTransition
	}
	if err != nil {
		return err
	}
	return t.ReleasePin("metadata-index:" + id)
}
