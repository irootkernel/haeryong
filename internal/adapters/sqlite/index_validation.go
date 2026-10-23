package sqlite

import "haeryong.local/haeryong/internal/domain"

// Each search traversal starts in an existing selected-version/unit prefix.
// The complete tuple cursor lets SQLite seek before applying the raw-row limit.
func (t *writeTx) SearchAuditUnits(version string, after *domain.SearchUnit, limit int) ([]domain.SearchUnit, error) {
	query := `SELECT unit_id FROM search_units WHERE version_id=?`
	args := []any{version}
	if after != nil {
		query += ` AND (kind,page_path,region_id,source_id)>(?,?,?,?)`
		args = append(args, after.Kind, after.PagePath, after.RegionID, after.SourceID)
	}
	query += ` ORDER BY kind,page_path,region_id,source_id LIMIT ?`
	args = append(args, referenceLimit(limit))
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []domain.SearchUnit{}
	for _, id := range ids {
		u, err := t.SearchUnit(id)
		if err != nil {
			return nil, err // Never filter away a failed typed read's zero value.
		}
		out = append(out, u)
	}
	return out, nil
}

func (t *writeTx) SearchAuditUnit(key domain.SearchUnit) (domain.SearchUnit, error) {
	var id int64
	err := t.tx.QueryRowContext(t.ctx, `SELECT unit_id FROM search_units WHERE version_id=? AND kind=? AND page_path=? AND region_id=? AND source_id=?`, key.VersionID, key.Kind, key.PagePath, key.RegionID, key.SourceID).Scan(&id)
	if err != nil {
		return domain.SearchUnit{}, notFound(err)
	}
	return t.SearchUnit(id)
}

func (t *writeTx) SearchAuditChunks(unit int64, part string, after, limit int) ([]int, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT ordinal FROM search_chunks WHERE unit_id=? AND part=? AND ordinal>? ORDER BY ordinal LIMIT ?`, unit, part, after, referenceLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int{}
	for rows.Next() {
		var ordinal int
		if err = rows.Scan(&ordinal); err != nil {
			return nil, err
		}
		out = append(out, ordinal)
	}
	return out, rows.Err()
}

// Expected ordinals also visit absent chunks, unlike a scan of existing rows.
func (t *writeTx) SearchAuditChunk(unit int64, part string, ordinal int) (bool, error) {
	var present bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM search_chunks c WHERE c.unit_id=? AND c.part=? AND c.ordinal=? AND EXISTS(SELECT 1 FROM search_words_docsize WHERE id=c.chunk_id) AND EXISTS(SELECT 1 FROM search_grams_docsize WHERE id=c.chunk_id))`, unit, part, ordinal).Scan(&present)
	return present, err
}

func (t *writeTx) SearchAuditAnchors(version, page, after string, limit int) ([]domain.PageAnchor, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT version_id,page_path,anchor_id,locator_kind,occurrences,label,text_sha256 FROM page_anchors WHERE version_id=? AND page_path=? AND anchor_id>? ORDER BY anchor_id LIMIT ?`, version, page, after, referenceLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.PageAnchor{}
	for rows.Next() {
		var a domain.PageAnchor
		if err = rows.Scan(&a.VersionID, &a.PagePath, &a.ID, &a.LocatorKind, &a.Occurrences, &a.Label, &a.TextSHA256); err != nil {
			return nil, err
		}
		if err = validatePageAnchor(a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
