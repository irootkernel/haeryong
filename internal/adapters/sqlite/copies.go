package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"haeryong.local/haeryong/internal/domain"
)

const copyMigration = `CREATE TABLE copy_sets(set_id TEXT PRIMARY KEY,kind TEXT NOT NULL CHECK(kind IN ('backup','export')),stage TEXT NOT NULL,job_id TEXT,value BLOB NOT NULL CHECK(json_valid(value)));
CREATE INDEX copy_sets_kind ON copy_sets(kind,set_id);
CREATE TABLE copy_versions(set_id TEXT NOT NULL REFERENCES copy_sets(set_id),version_id TEXT NOT NULL,document_id TEXT NOT NULL,content_set_id TEXT NOT NULL,payload_sha TEXT NOT NULL,manifest_sha TEXT NOT NULL,metadata_sha TEXT NOT NULL,PRIMARY KEY(set_id,version_id));
CREATE TABLE recovery_holds(singleton INTEGER PRIMARY KEY CHECK(singleton=1),value BLOB NOT NULL CHECK(json_valid(value)));
`

func (s *Store) validateCopyDeclarations(ctx context.Context) error {
	normalize := func(v string) string { return strings.TrimSuffix(strings.Join(strings.Fields(v), " "), ";") }
	for _, m := range regexp.MustCompile(`(?s)CREATE (TABLE|INDEX) ([a-z_]+).*?;`).FindAllStringSubmatch(copyMigration, -1) {
		var actual string
		if e := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type=? AND name=?`, strings.ToLower(m[1]), m[2]).Scan(&actual); e != nil {
			return errors.New("missing copy schema declaration")
		}
		if normalize(actual) != normalize(m[0]) {
			return errors.New("changed copy schema declaration")
		}
	}
	return nil
}
func (t *writeTx) CopySet(id string) (out domain.CopySet, err error) {
	var raw []byte
	err = notFound(t.tx.QueryRowContext(t.ctx, `SELECT value FROM copy_sets WHERE set_id=?`, id).Scan(&raw))
	if err == nil {
		err = json.Unmarshal(raw, &out)
	}
	return
}
func (t *writeTx) PutCopySet(v domain.CopySet) error {
	if e := t.exec(`INSERT INTO copy_sets VALUES(?,?,?,?,?) ON CONFLICT(set_id) DO UPDATE SET stage=excluded.stage,job_id=excluded.job_id,value=excluded.value`, v.ID, v.Kind, v.Stage, nullable(v.JobID), jsonBytes(v)); e != nil {
		return e
	}
	if v.JobID != "" {
		progress := map[string]any{"set_id": v.ID, "stage": v.Stage, "verified_objects": strconv.FormatInt(v.ObjectCount, 10), "current_object_bytes": strconv.FormatInt(v.Offset, 10), "total_objects": nil, "total_bytes": nil, "checkpoint_id": v.ID}
		if v.Error != "" {
			progress["error"] = v.Error
		}
		if e := t.SetJobResult(v.JobID, jsonBytes(progress)); e != nil {
			return e
		}
		if e := t.exec(`UPDATE jobs SET revision=revision+1 WHERE job_id=?`, v.JobID); e != nil {
			return e
		}
		return t.jobUpdated(v.JobID)
	}
	return nil
}
func (t *writeTx) CopySets(kind, after string, limit int) ([]domain.CopySet, error) {
	rows, e := t.tx.QueryContext(t.ctx, `SELECT value FROM copy_sets WHERE (?='' OR kind=?) AND set_id>? ORDER BY set_id LIMIT ?`, kind, kind, after, batch(limit))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.CopySet{}
	for rows.Next() {
		var raw []byte
		var v domain.CopySet
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (t *writeTx) CaptureCopyVersions(id string, r domain.CopyRequest) error {
	filter := ` AND (?='' OR d.document_id=?) AND (?='' OR d.project_id=?) AND (?='' OR v.version_id=?) AND (?='all' OR d.lifecycle=?)`
	if r.Versions != "all" && r.VersionID == "" {
		filter += ` AND v.version_id=d.latest_version_id`
	}
	_, e := t.tx.ExecContext(t.ctx, `INSERT INTO copy_versions SELECT ?,v.version_id,d.document_id,v.content_set_id,v.payload_sha256,v.manifest_sha256,coalesce(v.supplied_metadata_hash,'') FROM document_versions v JOIN documents d ON d.document_id=v.document_id WHERE v.sequence IS NOT NULL AND d.lifecycle!='purged'`+filter, id, r.DocumentID, r.DocumentID, r.ProjectID, r.ProjectID, r.VersionID, r.VersionID, r.Lifecycle, r.Lifecycle)
	if e != nil {
		return e
	}
	var n int
	if e = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM copy_versions WHERE set_id=?`, id).Scan(&n); e != nil {
		return e
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
func (t *writeTx) CopyVersions(id, after string, limit int) ([]domain.CopyVersion, error) {
	rows, e := t.tx.QueryContext(t.ctx, `SELECT document_id,version_id,content_set_id,payload_sha,manifest_sha,metadata_sha FROM copy_versions WHERE set_id=? AND version_id>? ORDER BY version_id LIMIT ?`, id, after, batch(limit))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.CopyVersion{}
	for rows.Next() {
		var v domain.CopyVersion
		if e = rows.Scan(&v.DocumentID, &v.VersionID, &v.ContentSetID, &v.PayloadSHA, &v.ManifestSHA, &v.MetadataSHA); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (t *writeTx) RecoveryHold() (*domain.RecoveryHold, error) {
	var raw []byte
	e := t.tx.QueryRowContext(t.ctx, `SELECT value FROM recovery_holds WHERE singleton=1`).Scan(&raw)
	if errors.Is(notFound(e), domain.ErrNotFound) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var v domain.RecoveryHold
	e = json.Unmarshal(raw, &v)
	return &v, e
}
