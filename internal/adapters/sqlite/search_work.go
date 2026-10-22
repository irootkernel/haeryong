package sqlite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
)

// EnqueueSearch is called by publication in the same transaction. The immutable
// work key also suppresses replay and terminal same-revision retries.
func (t *writeTx) EnqueueSearch(version string, revision int64) (string, error) {
	var existing string
	err := t.tx.QueryRowContext(t.ctx, `SELECT job_id FROM derived_work WHERE version_id=? AND work_kind='search' AND extractor_revision=?`, version, revision).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(notFound(err), domain.ErrNotFound) {
		return "", err
	}
	v, err := t.PublishedVersion(version)
	if err != nil {
		return "", err
	}
	d, err := t.Document(v.DocumentID)
	if err != nil {
		return "", err
	}
	scope, err := t.Scope(d.ProjectID)
	if err != nil {
		return "", err
	}
	if d.Lifecycle == "trash" || d.Lifecycle == "purged" || scope.Project.Lifecycle == "trash" || scope.Project.Lifecycle == "purged" {
		return "", nil
	}
	library, err := t.Library()
	if err != nil {
		return "", err
	}
	// The work-key constraint, rather than the job ID, suppresses replay. Random
	// identities allow a later typed cleanup/rebuild to retain historical jobs.
	id, err := service.NewID()
	if err != nil {
		return "", err
	}
	captured := domain.SearchMaintenanceScope{Actor: "system", LibraryID: library.ID, ProjectID: d.ProjectID, DocumentID: d.ID, VersionID: v.ID, ContentSetID: v.ContentSetID, PayloadSHA: v.PayloadSHA, ManifestSHA: v.ManifestSHA, ExtractorRevision: revision}
	raw, err := json.Marshal(captured)
	if err != nil {
		return "", err
	}
	if err = t.CreateJob(domain.Job{ID: id, Kind: "derived_index", Scope: raw, ConfigRevision: scope.ConfigRevision, Permissions: map[string]int64{d.ProjectID: scope.Project.PermissionsRevision}}); err != nil {
		return "", err
	}
	if err = t.PutDerivedWork(domain.DerivedWork{JobID: id, VersionID: version, WorkKind: "search", ExtractorRevision: revision}); err != nil {
		return "", err
	}
	old, e := t.SearchIndex(version)
	if e != nil && !errors.Is(e, domain.ErrNotFound) {
		return "", e
	}
	if err = t.PutSearchIndex(domain.SearchIndex{VersionID: version, JobID: id, ExtractorRevision: revision, Revision: old.Revision + 1, State: "pending"}); err != nil {
		return "", err
	}
	if err = t.SearchProgress(id, "inventory"); err != nil {
		return "", err
	}
	return id, t.SearchIndexUpdated(version)
}
func (t *writeTx) SearchVersions(after string, limit int) ([]string, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT version_id FROM document_versions WHERE sequence IS NOT NULL AND version_id>? ORDER BY version_id LIMIT ?`, after, batch(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SearchFence is used before every staged or final write. Maintenance uses a
// captured system authority, never the expiring import credential.
func (t *writeTx) SearchFence(id string, generation int64) (domain.DerivedWork, error) {
	w, err := t.DerivedWork(id)
	if err != nil {
		return w, err
	}
	j, err := t.Job(id)
	if err != nil {
		return w, err
	}
	if j.Kind != "derived_index" || j.State != "running" || j.CancelRequested || j.AttemptGeneration != generation {
		return w, domain.ErrStaleWorker
	}
	return t.searchAuthority(j, w, false)
}

// Static indexing reads only this captured immutable version. Global settings
// do not enlarge that target or grant source reads. Every batch still joins its
// current lifecycle and permission generation.
func (t *writeTx) searchAuthority(j domain.Job, w domain.DerivedWork, reauthorize bool) (domain.DerivedWork, error) {
	index, err := t.SearchIndex(w.VersionID)
	if err != nil {
		return w, err
	}
	if index.JobID != j.ID || index.ExtractorRevision != w.ExtractorRevision {
		return w, domain.ErrStaleWorker
	}
	var s domain.SearchMaintenanceScope
	if json.Unmarshal(j.Scope, &s) != nil {
		return w, domain.ErrDenied
	}
	v, err := t.PublishedVersion(w.VersionID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return w, domain.ErrSearchRestricted
		}
		return w, err
	}
	d, err := t.Document(v.DocumentID)
	if err != nil {
		return w, err
	}
	p, err := t.Scope(d.ProjectID)
	if err != nil {
		return w, err
	}
	library, err := t.Library()
	if err != nil {
		return w, err
	}
	if s.Actor != "system" || s.LibraryID != library.ID || s.VersionID != v.ID || s.DocumentID != d.ID || s.ContentSetID != v.ContentSetID || s.PayloadSHA != v.PayloadSHA || s.ManifestSHA != v.ManifestSHA || s.ExtractorRevision != w.ExtractorRevision {
		return w, domain.ErrDenied
	}
	if s.ProjectID != d.ProjectID {
		return w, domain.ErrSearchRestricted
	}
	if d.Lifecycle == "trash" || d.Lifecycle == "purged" || p.Project.Lifecycle == "trash" || p.Project.Lifecycle == "purged" {
		return w, domain.ErrSearchRestricted
	}
	if !reauthorize && j.Permissions[d.ProjectID] != p.Project.PermissionsRevision {
		return w, domain.ErrSearchRestricted
	}
	return w, nil
}

// SearchInventory returns a bounded prepared input inventory; callers normalize
// metadata outside Write. Tuple keys retain exact Unicode page identities.
func (t *writeTx) SearchInventory(version, kind, after string, limit int) ([]domain.SearchInventoryItem, error) {
	var query string
	args := []any{version, after, batch(limit)}
	switch kind {
	case "entry":
		query = `SELECT e.relative_path,e.kind,coalesce(e.content_hash,''),coalesce(p.page_path,''),p.metadata FROM version_entries e JOIN document_versions v ON v.content_set_id=e.content_set_id LEFT JOIN version_pages p ON p.version_id=v.version_id AND p.page_path=e.relative_path WHERE v.version_id=? AND e.relative_path>? ORDER BY e.relative_path LIMIT ?`
	case "source":
		query = `SELECT source_id,'source','','',metadata FROM version_sources WHERE version_id=? AND source_id>? ORDER BY source_id LIMIT ?`
	case "region":
		pair := []string{"", ""}
		if after != "" {
			if err := json.Unmarshal([]byte(after), &pair); err != nil || len(pair) != 2 {
				return nil, domain.ErrUnsupportedDerivedData
			}
		}
		args = []any{version, pair[0], pair[1], batch(limit)}
		query = `SELECT page_path,'region',region_id,page_path,metadata FROM regions WHERE version_id=? AND (page_path,region_id)>(?,?) ORDER BY page_path COLLATE BINARY,region_id COLLATE BINARY LIMIT ?`
	default:
		return nil, domain.ErrUnsupportedDerivedData
	}
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.SearchInventoryItem{}
	for rows.Next() {
		var i domain.SearchInventoryItem
		var k, hash, page string
		if err = rows.Scan(&i.Key, &k, &hash, &page, &i.Metadata); err != nil {
			return nil, err
		}
		u := domain.SearchUnit{VersionID: version, State: "pending", BodyState: "not_applicable", AnchorState: "not_applicable"}
		switch kind {
		case "entry":
			u.Kind = "attachment"
			u.PagePath = i.Key
			u.Title = i.Key
			u.InputHash = hash
			u.BodyState = "unsupported"
			if k == "file" && page != "" {
				u.Kind = "page"
				u.BodyState = "pending"
			}
		case "source":
			u.Kind = "source"
			u.SourceID = i.Key
		case "region":
			key, _ := json.Marshal([]string{page, hash})
			i.Key = string(key)
			u.Kind = "region"
			u.PagePath = page
			u.RegionID = hash
			u.BodyState = "pending"
			u.AnchorState = "pending"
		}
		i.Unit = u
		out = append(out, i)
	}
	return out, rows.Err()
}
func (t *writeTx) NextSearchPage(version string) (domain.SearchUnit, error) {
	var id int64
	err := t.tx.QueryRowContext(t.ctx, `SELECT unit_id FROM search_units WHERE version_id=? AND kind='page' AND body_state='pending' ORDER BY page_path LIMIT 1`, version).Scan(&id)
	if err != nil {
		return domain.SearchUnit{}, notFound(err)
	}
	return t.SearchUnit(id)
}
func (t *writeTx) SearchRegionUnit(version, page, anchor string) (domain.SearchUnit, error) {
	var id int64
	err := t.tx.QueryRowContext(t.ctx, `SELECT unit_id FROM search_units WHERE version_id=? AND page_path=? AND region_id=? AND kind='region'`, version, page, anchor).Scan(&id)
	if err != nil {
		return domain.SearchUnit{}, notFound(err)
	}
	return t.SearchUnit(id)
}

// ClearSearchData deletes at most limit rows per table and yields. An empty page
// selects all derived rows for a quarantined revision or physical cleanup.
func (t *writeTx) ClearSearchData(version, page string, limit int) (bool, error) {
	limit = batch(limit)
	where := `version_id=? AND (?='' OR page_path=?)`
	filter := `unit_id IN (SELECT unit_id FROM search_units WHERE ` + where + `)`
	if page != "" {
		filter += ` AND part='body'`
	}
	r, err := t.tx.ExecContext(t.ctx, `DELETE FROM search_chunks WHERE chunk_id IN (SELECT chunk_id FROM search_chunks WHERE `+filter+` LIMIT ?)`, version, page, page, limit)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	if err != nil || n > 0 {
		return false, err
	}
	r, err = t.tx.ExecContext(t.ctx, `DELETE FROM page_anchors WHERE (version_id,page_path,anchor_id) IN (SELECT version_id,page_path,anchor_id FROM page_anchors WHERE `+where+` LIMIT ?)`, version, page, page, limit)
	if err != nil {
		return false, err
	}
	n, err = r.RowsAffected()
	if err != nil || n > 0 {
		return false, err
	}
	if page != "" {
		return true, nil
	}
	r, err = t.tx.ExecContext(t.ctx, `DELETE FROM search_units WHERE unit_id IN (SELECT unit_id FROM search_units WHERE version_id=? LIMIT ?)`, version, limit)
	if err != nil {
		return false, err
	}
	n, err = r.RowsAffected()
	return n == 0, err
}

// DeleteDerivedVersion is the typed, resumable pre-deletion hook. Fence all
// active generations before clearing FTS-triggered chunks; keep job history.
func (t *writeTx) DeleteDerivedVersion(version string, limit int) (bool, error) {
	if err := t.exec(`UPDATE jobs SET state='cancelled',cancel_requested=1,attempt_generation=attempt_generation+1,revision=revision+1 WHERE job_id IN (SELECT job_id FROM derived_work WHERE version_id=?) AND state NOT IN ('completed','failed','cancelled')`, version); err != nil {
		return false, err
	}
	if err := t.exec(`UPDATE search_indexes SET state='pending',revision=revision+1 WHERE version_id=? AND state!='pending'`, version); err != nil {
		return false, err
	}
	done, err := t.ClearSearchData(version, "", limit)
	if err != nil || !done {
		return false, err
	}
	if err = t.exec(`DELETE FROM search_indexes WHERE version_id=?`, version); err != nil {
		return false, err
	}
	return true, t.exec(`DELETE FROM derived_work WHERE version_id=?`, version)
}
func (t *writeTx) SearchProgress(id, stage string) error {
	w, err := t.DerivedWork(id)
	if err != nil {
		return err
	}
	var files, bytes, pages, done, read, failed int64
	if err = t.tx.QueryRowContext(t.ctx, `SELECT count(*),coalesce(sum(CASE WHEN EXISTS(SELECT 1 FROM version_pages p WHERE p.version_id=? AND p.page_path=e.relative_path) AND e.kind='file' THEN e.byte_size ELSE 0 END),0) FROM version_entries e WHERE content_set_id=(SELECT content_set_id FROM document_versions WHERE version_id=?)`, w.VersionID, w.VersionID).Scan(&files, &bytes); err != nil {
		return err
	}
	if err = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM version_pages WHERE version_id=?`, w.VersionID).Scan(&pages); err != nil {
		return err
	}
	var inventoried int64
	if err = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM search_units WHERE version_id=? AND kind IN ('page','attachment')`, w.VersionID).Scan(&inventoried); err != nil {
		return err
	}
	if err = t.tx.QueryRowContext(t.ctx, `SELECT count(*),coalesce(sum(e.byte_size),0) FROM search_units u JOIN document_versions v ON v.version_id=u.version_id JOIN version_entries e ON e.content_set_id=v.content_set_id AND e.relative_path=u.page_path WHERE u.version_id=? AND u.kind='page' AND u.body_state='complete'`, w.VersionID).Scan(&done, &read); err != nil {
		return err
	}
	if err = t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM search_units WHERE version_id=? AND state='failed'`, w.VersionID).Scan(&failed); err != nil {
		return err
	}
	index, err := t.SearchIndex(w.VersionID)
	if err != nil {
		return err
	}
	if index.State == "pending" {
		inventoried = 0
		done = 0
		read = 0
		failed = 0
	}
	if index.Reason != "" && failed == 0 {
		j, err := t.Job(id)
		if err != nil {
			return err
		}
		if j.State != "paused" {
			failed = 1
		}
	}
	f, b, p := strconv.FormatInt(files, 10), strconv.FormatInt(bytes, 10), strconv.FormatInt(pages, 10)
	checkpoint := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d:%d", id, w.AfterPath, index.Revision, done)))
	progress := domain.SearchProgress{VersionID: w.VersionID, WorkKind: "search", Stage: stage, FilesDone: strconv.FormatInt(inventoried, 10), FilesTotal: &f, BytesDone: strconv.FormatInt(read, 10), BytesTotal: &b, PagesDone: strconv.FormatInt(done, 10), PagesTotal: &p, Failed: strconv.FormatInt(failed, 10), Checkpoint: hex.EncodeToString(checkpoint[:])}
	raw, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	if err = t.SetJobResult(id, raw); err != nil {
		return err
	}
	if err = t.exec(`UPDATE jobs SET revision=revision+1 WHERE job_id=?`, id); err != nil {
		return err
	}
	return t.jobUpdated(id)
}
func (t *writeTx) SearchIndexUpdated(version string) error {
	index, err := t.SearchIndex(version)
	if err != nil {
		return err
	}
	return t.appendEvent("version", version, "search.index_updated", index.Revision, nil)
}
func (t *writeTx) CompleteSearchPage(id int64) error {
	return t.exec(`UPDATE search_units SET state='ready',body_state='complete' WHERE unit_id=? AND kind='page'`, id)
}

func (t *writeTx) FailSearch(id, reason string) error {
	if !admits(reason, "read_failed", "integrity_failed", "resource_exhausted", "storage_failed", "decode_failed", "cancelled", "interrupted") {
		return domain.ErrUnsupportedDerivedData
	}
	w, err := t.DerivedWork(id)
	if err != nil {
		return err
	}
	index, err := t.SearchIndex(w.VersionID)
	if err != nil {
		return err
	}
	if index.JobID != id {
		return nil
	}
	index.State = "partial"
	index.Reason = reason
	index.Revision++
	if err = t.PutSearchIndex(index); err != nil {
		return err
	}
	if err = t.SearchProgress(id, "complete"); err != nil {
		return err
	}
	return t.SearchIndexUpdated(w.VersionID)
}

func (t *writeTx) FailSearchPage(job string, generation, unitID int64, reason string) error {
	w, err := t.SearchFence(job, generation)
	if err != nil {
		return err
	}
	if !admits(reason, "read_failed", "integrity_failed", "resource_exhausted", "storage_failed", "decode_failed", "interrupted") {
		return domain.ErrUnsupportedDerivedData
	}
	// Metadata stays ready; only the verified body attempt has failed.
	return t.exec(`UPDATE search_units SET body_state='failed',reason=? WHERE unit_id=? AND version_id=? AND kind='page' AND body_state='pending'`, reason, unitID, w.VersionID)
}

// SuspendSearch preserves the checkpoint and completed pages instead of making
// a policy restriction a definitive extraction failure. Old callbacks are fenced.
func (t *writeTx) SuspendSearch(id string, generation int64) error {
	j, err := t.Job(id)
	if err != nil {
		return err
	}
	if j.State != "running" || j.CancelRequested || j.AttemptGeneration != generation {
		return domain.ErrStaleWorker
	}
	if err = t.exec(`UPDATE jobs SET state='paused',attempt_generation=attempt_generation+1,revision=revision+1 WHERE job_id=?`, id); err != nil {
		return err
	}
	w, err := t.DerivedWork(id)
	if err != nil {
		return err
	}
	index, err := t.SearchIndex(w.VersionID)
	if err != nil {
		return err
	}
	if index.State != "pending" {
		index.State = "partial"
	}
	index.Reason = "interrupted"
	index.Revision++
	if err = t.PutSearchIndex(index); err != nil {
		return err
	}
	if err = t.SearchProgress(id, "extract"); err != nil {
		return err
	}
	return t.SearchIndexUpdated(w.VersionID)
}

// ResumeSearch is an internal reconciliation port, not a public resume action.
// Automatic reconciliation preserves captured permissions. A trusted maintenance
// owner may explicitly reauthorize the SAME exact target under current revisions.
func (t *writeTx) ResumeSearch(id string, generation int64, reauthorize bool) error {
	j, err := t.Job(id)
	if err != nil {
		return err
	}
	if j.State != "paused" || j.CancelRequested || j.AttemptGeneration != generation {
		return domain.ErrStaleWorker
	}
	w, err := t.DerivedWork(id)
	if err != nil {
		return err
	}
	if _, err = t.searchAuthority(j, w, reauthorize); err != nil {
		return err
	}
	if reauthorize {
		var scope domain.SearchMaintenanceScope
		if err = json.Unmarshal(j.Scope, &scope); err != nil {
			return err
		}
		p, err := t.Scope(scope.ProjectID)
		if err != nil {
			return err
		}
		j.Permissions = map[string]int64{scope.ProjectID: p.Project.PermissionsRevision}
		j.ConfigRevision = p.ConfigRevision
		if err = t.exec(`UPDATE jobs SET permissions_revisions=?,config_revision=? WHERE job_id=?`, jsonBytes(j.Permissions), j.ConfigRevision, id); err != nil {
			return err
		}
	}
	index, err := t.SearchIndex(w.VersionID)
	if err != nil {
		return err
	}
	if index.State != "pending" {
		index.State = "indexing"
	}
	index.Reason = ""
	index.Revision++
	if err = t.PutSearchIndex(index); err != nil {
		return err
	}
	if err = t.exec(`UPDATE jobs SET state='queued',revision=revision+1 WHERE job_id=?`, id); err != nil {
		return err
	}
	if err = t.SearchProgress(id, "extract"); err != nil {
		return err
	}
	return t.SearchIndexUpdated(w.VersionID)
}
