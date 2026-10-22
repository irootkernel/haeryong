package sqlite

import (
	"haeryong.local/haeryong/internal/domain"
)

func admits(value string, allowed ...string) bool {
	for _, v := range allowed {
		if v == value {
			return true
		}
	}
	return false
}
func validateDerivedWork(v domain.DerivedWork) error {
	if v.JobID == "" || v.VersionID == "" || !admits(v.WorkKind, "search") || v.ExtractorRevision < 1 {
		return domain.ErrUnsupportedDerivedData
	}
	return nil
}
func validateSearchIndex(v domain.SearchIndex) error {
	if v.VersionID == "" || v.ExtractorRevision < 1 || v.Revision < 1 || !admits(v.State, "pending", "indexing", "complete", "partial", "failed") {
		return domain.ErrUnsupportedDerivedData
	}
	return nil
}
func validateSearchUnit(v domain.SearchUnit) error {
	if v.VersionID == "" || !admits(v.Kind, "document", "page", "region", "source", "attachment") ||
		!admits(v.State, "pending", "indexing", "ready", "failed") ||
		!admits(v.BodyState, "pending", "complete", "unsupported", "failed", "not_applicable") ||
		!admits(v.AnchorState, "not_applicable", "pending", "present", "missing", "ambiguous") {
		return domain.ErrUnsupportedDerivedData
	}
	valid := false
	switch v.Kind {
	case "document":
		valid = v.PagePath == "" && v.RegionID == "" && v.SourceID == ""
	case "page", "attachment":
		valid = v.PagePath != "" && v.RegionID == "" && v.SourceID == ""
	case "region":
		valid = v.PagePath != "" && v.RegionID != "" && v.SourceID == ""
	case "source":
		valid = v.PagePath == "" && v.RegionID == "" && v.SourceID != ""
	}
	if !valid {
		return domain.ErrUnsupportedDerivedData
	}
	return nil
}
func (t *writeTx) PutDerivedWork(v domain.DerivedWork) error {
	if err := validateDerivedWork(v); err != nil {
		return err
	}
	return t.exec(`INSERT INTO derived_work(job_id,version_id,work_kind,extractor_revision,after_path) VALUES(?,?,?,?,?) ON CONFLICT(job_id) DO UPDATE SET after_path=excluded.after_path`, v.JobID, v.VersionID, v.WorkKind, v.ExtractorRevision, v.AfterPath)
}
func (t *writeTx) DerivedWork(id string) (v domain.DerivedWork, err error) {
	err = t.tx.QueryRowContext(t.ctx, `SELECT job_id,version_id,work_kind,extractor_revision,after_path FROM derived_work WHERE job_id=?`, id).Scan(&v.JobID, &v.VersionID, &v.WorkKind, &v.ExtractorRevision, &v.AfterPath)
	if err != nil {
		return v, notFound(err)
	}
	return v, validateDerivedWork(v)
}
func (t *writeTx) PutSearchIndex(v domain.SearchIndex) error {
	if err := validateSearchIndex(v); err != nil {
		return err
	}
	return t.exec(`INSERT INTO search_indexes(version_id,job_id,extractor_revision,revision,discovery_complete,state,reason) VALUES(?,?,?,?,?,?,?) ON CONFLICT(version_id) DO UPDATE SET job_id=excluded.job_id,extractor_revision=excluded.extractor_revision,revision=excluded.revision,discovery_complete=excluded.discovery_complete,state=excluded.state,reason=excluded.reason`, v.VersionID, nullable(v.JobID), v.ExtractorRevision, v.Revision, v.DiscoveryComplete, v.State, v.Reason)
}
func (t *writeTx) SearchIndex(id string) (v domain.SearchIndex, err error) {
	err = t.tx.QueryRowContext(t.ctx, `SELECT version_id,coalesce(job_id,''),extractor_revision,revision,discovery_complete,state,reason FROM search_indexes WHERE version_id=?`, id).Scan(&v.VersionID, &v.JobID, &v.ExtractorRevision, &v.Revision, &v.DiscoveryComplete, &v.State, &v.Reason)
	if err != nil {
		return v, notFound(err)
	}
	return v, validateSearchIndex(v)
}
func (t *writeTx) PutSearchUnit(v domain.SearchUnit) (int64, error) {
	if err := validateSearchUnit(v); err != nil {
		return 0, err
	}
	var id int64
	err := t.tx.QueryRowContext(t.ctx, `INSERT INTO search_units(version_id,kind,page_path,region_id,source_id,title,input_hash,state,body_state,anchor_state,reason) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(version_id,kind,page_path,region_id,source_id) DO UPDATE SET title=excluded.title,input_hash=excluded.input_hash,state=excluded.state,body_state=excluded.body_state,anchor_state=excluded.anchor_state,reason=excluded.reason RETURNING unit_id`, v.VersionID, v.Kind, v.PagePath, v.RegionID, v.SourceID, v.Title, v.InputHash, v.State, v.BodyState, v.AnchorState, v.Reason).Scan(&id)
	return id, err
}
func (t *writeTx) SearchUnit(id int64) (v domain.SearchUnit, err error) {
	err = t.tx.QueryRowContext(t.ctx, `SELECT unit_id,version_id,kind,page_path,region_id,source_id,title,input_hash,state,body_state,anchor_state,reason FROM search_units WHERE unit_id=?`, id).Scan(&v.ID, &v.VersionID, &v.Kind, &v.PagePath, &v.RegionID, &v.SourceID, &v.Title, &v.InputHash, &v.State, &v.BodyState, &v.AnchorState, &v.Reason)
	if err != nil {
		return v, notFound(err)
	}
	if err = validateSearchUnit(v); err != nil {
		return v, err
	}
	if v.Kind == "region" {
		err = t.tx.QueryRowContext(t.ctx, `SELECT read_state,read_body_state,read_anchor_state FROM (`+searchReadUnits+`) WHERE unit_id=?`, id).Scan(&v.State, &v.BodyState, &v.AnchorState)
	}
	return v, err
}
func (t *writeTx) PutSearchChunk(v domain.SearchChunk) error {
	if !admits(v.Part, "metadata", "body") || v.Ordinal < 0 || len(v.Title)+len(v.Path)+len(v.Terms)+len(v.Body)+len(v.DisplayBody) > domain.SearchPostingBytes {
		return domain.ErrUnsupportedDerivedData
	}
	return t.exec(`INSERT INTO search_chunks(unit_id,part,ordinal,title,path,terms,body,display_body) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(unit_id,part,ordinal) DO UPDATE SET title=excluded.title,path=excluded.path,terms=excluded.terms,body=excluded.body,display_body=excluded.display_body`, v.UnitID, v.Part, v.Ordinal, v.Title, v.Path, v.Terms, v.Body, v.DisplayBody)
}
func validatePageAnchor(v domain.PageAnchor) error {
	if v.VersionID == "" || v.PagePath == "" || v.ID == "" || v.Occurrences < 1 || !admits(v.LocatorKind, "id", "data_region", "both") {
		return domain.ErrUnsupportedDerivedData
	}
	return nil
}
func (t *writeTx) PutPageAnchor(v domain.PageAnchor) error {
	if err := validatePageAnchor(v); err != nil {
		return err
	}
	return t.exec(`INSERT INTO page_anchors(version_id,page_path,anchor_id,locator_kind,occurrences,label,text_sha256) VALUES(?,?,?,?,?,?,?) ON CONFLICT(version_id,page_path,anchor_id) DO UPDATE SET locator_kind=excluded.locator_kind,occurrences=excluded.occurrences,label=excluded.label,text_sha256=excluded.text_sha256`, v.VersionID, v.PagePath, v.ID, v.LocatorKind, v.Occurrences, v.Label, v.TextSHA256)
}
func (t *writeTx) PageAnchor(version, page, id string) (v domain.PageAnchor, err error) {
	err = t.tx.QueryRowContext(t.ctx, `SELECT version_id,page_path,anchor_id,locator_kind,occurrences,label,text_sha256 FROM page_anchors WHERE version_id=? AND page_path=? AND anchor_id=?`, version, page, id).Scan(&v.VersionID, &v.PagePath, &v.ID, &v.LocatorKind, &v.Occurrences, &v.Label, &v.TextSHA256)
	if err != nil {
		return v, notFound(err)
	}
	return v, validatePageAnchor(v)
}
