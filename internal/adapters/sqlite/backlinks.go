package sqlite

import (
	"errors"
	"haeryong.local/haeryong/internal/domain"
	"regexp"
	"strings"
)

const edgeKeyColumns = `rowid,target_version_id,target_page_path,target_region_id,from_version_id,metadata_reference_id`

func (t *writeTx) edgeKeys(query string, args ...any) ([]domain.EdgeKey, error) {
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.EdgeKey{}
	for rows.Next() {
		var k domain.EdgeKey
		if err = rows.Scan(&k.RowID, &k.TargetVersion, &k.Page, &k.Region, &k.SourceVersion, &k.Reference); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Select physical rows FIRST. No joins, authority postfilter or computed sort
// can turn LIMIT into a full candidate scan while the single gate is held.
func (t *writeTx) IncomingEdgeKeys(version string, page, region *string, after *domain.EdgeKey, limit int) ([]domain.EdgeKey, error) {
	limit = referenceLimit(limit)
	base := `SELECT ` + edgeKeyColumns + ` FROM authored_edges INDEXED BY authored_edges_target WHERE target_version_id=?`
	args := []any{version}
	if page != nil {
		base += ` AND target_page_path=?`
		args = append(args, *page)
	}
	if region != nil {
		base += ` AND target_region_id=?`
		args = append(args, *region)
	}
	order := ` ORDER BY target_page_path,target_region_id,from_version_id,metadata_reference_id LIMIT ?`
	if after == nil {
		return t.edgeKeys(base+order, append(args, limit)...)
	}
	out := []domain.EdgeKey{}
	run := func(where string, more ...any) error {
		if len(out) == limit {
			return nil
		}
		bound := append(append([]any{}, args...), more...)
		bound = append(bound, limit-len(out))
		rows, err := t.edgeKeys(base+where+order, bound...)
		out = append(out, rows...)
		return err
	}
	if err := run(` AND target_page_path IS ? AND target_region_id IS ? AND (from_version_id,metadata_reference_id)>(?,?)`, after.Page, after.Region, after.SourceVersion, after.Reference); err != nil {
		return nil, err
	}
	if region == nil {
		where := ` AND target_page_path IS ? AND target_region_id IS NOT NULL`
		more := []any{after.Page}
		if after.Region != nil {
			where = ` AND target_page_path IS ? AND target_region_id>?`
			more = append(more, *after.Region)
		}
		if err := run(where, more...); err != nil {
			return nil, err
		}
	}
	if page == nil {
		where := ` AND target_page_path IS NOT NULL`
		var more []any
		if after.Page != nil {
			where = ` AND target_page_path>?`
			more = append(more, *after.Page)
		}
		if err := run(where, more...); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (t *writeTx) OutgoingEdgeKeys(version, after string, limit int) ([]domain.EdgeKey, error) {
	return t.edgeKeys(`SELECT `+edgeKeyColumns+` FROM authored_edges WHERE from_version_id=? AND metadata_reference_id>? ORDER BY metadata_reference_id LIMIT ?`, version, after, referenceLimit(limit))
}
func (t *writeTx) CanonicalEdgeKeys(after int64, limit int) ([]domain.EdgeKey, error) {
	return t.edgeKeys(`SELECT `+edgeKeyColumns+` FROM authored_edges NOT INDEXED WHERE rowid>? ORDER BY rowid LIMIT ?`, after, referenceLimit(limit))
}
func (t *writeTx) CanonicalEdgeKey(rowID int64) (domain.EdgeKey, error) {
	rows, err := t.edgeKeys(`SELECT `+edgeKeyColumns+` FROM authored_edges NOT INDEXED WHERE rowid=?`, rowID)
	if err != nil {
		return domain.EdgeKey{}, err
	}
	if len(rows) != 1 {
		return domain.EdgeKey{}, domain.ErrContentIntegrity
	}
	return rows[0], nil
}
func normalizeReferenceSQL(v string) string {
	return strings.TrimSuffix(strings.Join(strings.Fields(v), " "), ";")
}
func (t *writeTx) ReferenceIndexDefinition() (string, error) {
	var actual string
	err := t.tx.QueryRowContext(t.ctx, `SELECT sql FROM sqlite_master WHERE type='index' AND name='authored_edges_target'`).Scan(&actual)
	if errors.Is(notFound(err), domain.ErrNotFound) {
		return "reverse_index_definition_missing", nil
	}
	if err != nil {
		return "", err
	}
	expected := regexp.MustCompile(`(?s)CREATE INDEX authored_edges_target.*?;`).FindString(migrations[8])
	if normalizeReferenceSQL(actual) != normalizeReferenceSQL(expected) {
		return "reverse_index_definition_changed", nil
	}
	return "", nil
}
func (t *writeTx) ReindexReferences() error {
	reason, err := t.ReferenceIndexDefinition()
	if err != nil {
		return err
	}
	if reason != "" {
		return domain.ErrUnsupportedDerivedData
	}
	return t.exec(`REINDEX main.authored_edges_target`)
}

// Re-entry replaces terminal slots, never terminal jobs. This entire method is
// called inside the owner-authenticated receipt transaction.
func (t *writeTx) RebuildIndexes(version, kind string) ([]string, error) {
	kinds := []string{"references"}
	if kind == "search" || kind == "all" {
		kinds = []string{"search", "references"}
	}
	for _, k := range kinds {
		var active bool
		if err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM derived_work w JOIN jobs j USING(job_id) WHERE w.version_id=? AND w.work_kind=? AND j.state NOT IN ('completed','failed','cancelled'))`, version, k).Scan(&active); err != nil {
			return nil, err
		}
		if active {
			return nil, domain.ErrConflict
		}
	}
	reason, err := t.ReferenceIndexDefinition()
	if err != nil {
		return nil, err
	}
	if reason != "" {
		return nil, domain.ErrUnsupportedDerivedData
	}
	ids := []string{}
	for _, k := range kinds {
		if err = t.exec(`DELETE FROM derived_work WHERE version_id=? AND work_kind=? AND extractor_revision=?`, version, k, map[string]int64{"search": domain.SearchExtractorRevision, "references": domain.ReferenceRevision}[k]); err != nil {
			return nil, err
		}
		var id string
		if k == "search" {
			id, err = t.EnqueueSearch(version, domain.SearchExtractorRevision)
		} else {
			id, err = t.enqueueReferences(version, domain.ReferenceRevision, true)
		}
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
		if k == "references" {
			w, e := t.DerivedWork(id)
			if e != nil {
				return nil, e
			}
			w.AfterPath = `{"phase":"repair"}`
			if e = t.PutDerivedWork(w); e != nil {
				return nil, e
			}
		}
	}
	return ids, nil
}

func (t *writeTx) HasIndexedEdge(k domain.EdgeKey) (bool, error) {
	rows, err := t.edgeKeys(`SELECT `+edgeKeyColumns+` FROM authored_edges INDEXED BY authored_edges_target WHERE target_version_id=? AND target_page_path IS ? AND target_region_id IS ? AND from_version_id=? AND metadata_reference_id=? LIMIT 2`, k.TargetVersion, k.Page, k.Region, k.SourceVersion, k.Reference)
	return err == nil && len(rows) == 1 && rows[0].RowID == k.RowID, err
}
