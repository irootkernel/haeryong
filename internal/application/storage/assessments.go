package storage

import "haeryong.local/haeryong/internal/domain"

// AllRequiredUnits walks the storage cursor to exhaustion. A storage page is a
// transport batch, never a semantic coverage or provider budget.
func AllRequiredUnits(tx interface {
	RequiredUnits(string, string, string, string, int) ([]domain.RequiredUnit, error)
}, set string) ([]domain.RequiredUnit, error) {
	out := []domain.RequiredUnit{}
	kind, page, region := "", "", ""
	for {
		rows, e := tx.RequiredUnits(set, kind, page, region, 256)
		if e != nil {
			return nil, e
		}
		if len(rows) == 0 {
			return out, nil
		}
		out = append(out, rows...)
		last := rows[len(rows)-1]
		kind, page, region = last.Kind, last.PagePath, last.RegionID
	}
}
