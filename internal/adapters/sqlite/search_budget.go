package sqlite

import (
	"context"
	"haeryong.local/haeryong/internal/domain"
)

// Implicit automerge and its emergency fan-in caused the body/742 outlier.
// Request merge work separately from posting writes. FTS5's quantum ends at a
// term boundary, so the host measurement also records any term-list overshoot.
func (s *Store) configureSearchMerges(ctx context.Context) error {
	for _, table := range []string{"search_words", "search_grams"} {
		for _, setting := range []struct {
			name  string
			value int
		}{{"automerge", 0}, {"crisismerge", 1000}, {"usermerge", 16}, {"pgsz", 1024}} {
			if _, err := s.db.ExecContext(ctx, `INSERT INTO `+table+`(`+table+`,rank)VALUES(?,?)`, setting.name, setting.value); err != nil {
				return err
			}
		}
	}
	return nil
}
func (t *writeTx) MergeSearch(table, pages int) error {
	if table < 0 || table > 1 || pages != domain.SearchMergePages {
		return domain.ErrUnsupportedDerivedData
	}
	name := []string{"search_words", "search_grams"}[table]
	return t.exec(`INSERT INTO `+name+`(`+name+`,rank)VALUES('merge',?)`, pages)
}

// A page row is the single EOF activation gate. Regions keep their staged rows;
// their effective body/anchor state is read through this parent gate.
const searchReadUnits = `SELECT u.*,
 CASE WHEN u.kind='region' AND p.body_state='complete' THEN 'ready' ELSE u.state END AS read_state,
 CASE WHEN u.kind='region' AND p.body_state='complete' THEN 'complete' ELSE u.body_state END AS read_body_state,
 CASE WHEN u.kind='region' AND p.body_state='complete' THEN CASE WHEN a.anchor_id IS NULL THEN 'missing' WHEN a.occurrences>1 THEN 'ambiguous' ELSE 'present' END ELSE u.anchor_state END AS read_anchor_state
 FROM search_units u LEFT JOIN search_units p ON u.kind='region' AND p.version_id=u.version_id AND p.page_path=u.page_path AND p.kind='page'
 LEFT JOIN page_anchors a ON u.kind='region' AND a.version_id=u.version_id AND a.page_path=u.page_path AND a.anchor_id=u.region_id`
