package scan

import (
	"encoding/json"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func (s *Service) validateReassessment(tx app.WriteTx, actor domain.Session, r domain.ScanRequest) error {
	if actor.Scope.Kind != "owner" {
		return domain.ErrDenied
	}
	if r.FindingID == "" || r.ExpectedFindingRevision == nil || *r.ExpectedFindingRevision < 1 || r.DocumentID == "" || r.VersionID == "" || r.Depth == "" || r.SourceMode == "" || r.Scope == "all_allowed_projects" || len(r.DocumentIDs) > 0 || r.PathPrefix != "" {
		return domain.ErrInvalidTarget
	}
	f, e := tx.Finding(r.FindingID)
	if e != nil {
		return e
	}
	if !visibleFindingForActor(tx, actor, f) {
		return domain.ErrNotFound
	}
	if f.Revision != *r.ExpectedFindingRevision {
		return domain.ErrPrecondition
	}
	if f.DocumentID == nil || r.DocumentID != *f.DocumentID {
		return domain.ErrInvalidTarget
	}
	v, e := tx.PublishedVersion(r.VersionID)
	if e != nil {
		return e
	}
	if v.DocumentID != r.DocumentID {
		return domain.ErrInvalidTarget
	}
	if r.PagePath != "" {
		if _, e = tx.Page(r.VersionID, r.PagePath); e != nil {
			return e
		}
	}
	if r.RegionID != "" {
		if r.PagePath == "" {
			return domain.ErrInvalidTarget
		}
		if _, e = tx.VersionRegion(r.VersionID, r.PagePath, r.RegionID); e != nil {
			return e
		}
	}
	if f.Kind == "freshness_assessment" {
		var p struct {
			Scope struct {
				PagePath *string `json:"page_path"`
				RegionID *string `json:"region_id"`
			} `json:"scope"`
		}
		if json.Unmarshal(f.Proposal, &p) != nil {
			return domain.ErrReconciliation
		}
		if p.Scope.PagePath != nil && r.PagePath != *p.Scope.PagePath || p.Scope.RegionID != nil && r.RegionID != *p.Scope.RegionID {
			return domain.ErrInvalidTarget
		}
	} else if f.PagePath != nil && r.PagePath != *f.PagePath || f.RegionID != nil && r.RegionID != *f.RegionID {
		return domain.ErrInvalidTarget
	}
	return nil
}
