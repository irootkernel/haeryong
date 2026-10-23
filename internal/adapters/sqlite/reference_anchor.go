package sqlite

import (
	"errors"
	"haeryong.local/haeryong/internal/domain"
)

// ReferenceAnchor admits page pins from inventory and regions after current page EOF. It is a
// short indexed snapshot, rechecked by the worker immediately before edge writes.
func (t *writeTx) ReferenceAnchor(version, page, region string) (a domain.ReferenceAnchor, err error) {
	if !domain.IsHTMLPath(page) {
		return a, domain.ErrNotFound
	}
	// A page-only pin is inventory authority; search completion matters only for regions.
	if region == "" {
		v, err := t.PublishedVersion(version)
		if err != nil {
			return a, err
		}
		entry, err := t.QueryEntry(v.ContentSetID, page)
		if err != nil {
			return a, err
		}
		if entry.Kind != "file" || !hashPattern.MatchString(entry.Hash) {
			a.Reason = "anchor_drift"
			return a, nil
		}
		a.PageHash = entry.Hash
		return a, nil
	}
	index, err := t.SearchIndex(version)
	if errors.Is(err, domain.ErrNotFound) {
		a.Reason = "anchor_pending"
		return a, nil
	}
	if err != nil {
		return a, err
	}
	a.SearchJob = index.JobID
	a.SearchRevision = index.ExtractorRevision
	a.IndexRevision = index.Revision
	if index.ExtractorRevision != domain.SearchExtractorRevision {
		a.Reason = "anchor_pending"
		return a, nil
	}
	var input, state string
	err = t.tx.QueryRowContext(t.ctx, `SELECT input_hash,body_state FROM search_units WHERE version_id=? AND kind='page' AND page_path=?`, version, page).Scan(&input, &state)
	if errors.Is(notFound(err), domain.ErrNotFound) {
		a.Reason = "anchor_pending"
		return a, nil
	}
	if err != nil {
		return a, err
	}
	v, err := t.PublishedVersion(version)
	if err != nil {
		return a, err
	}
	entry, err := t.QueryEntry(v.ContentSetID, page)
	if err != nil {
		return a, err
	}
	a.PageHash = entry.Hash
	if entry.Kind != "file" || input != entry.Hash || !hashPattern.MatchString(entry.Hash) {
		a.Reason = "anchor_drift"
		return a, nil
	}
	if state != "complete" {
		a.Reason = "anchor_pending"
		return a, nil
	}
	anchor, err := t.PageAnchor(version, page, region)
	if errors.Is(err, domain.ErrNotFound) {
		a.Reason = "missing_region"
		return a, nil
	}
	if err != nil {
		return a, err
	}
	if anchor.Occurrences != 1 {
		a.Reason = "ambiguous_region"
		return a, nil
	}
	if !hashPattern.MatchString(anchor.TextSHA256) {
		a.Reason = "anchor_drift"
		return a, nil
	}
	a.AnchorHash = anchor.TextSHA256
	return a, nil
}
