package storage

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
)

// AuthorizeTarget follows current typed resource anchors, including bounded
// pre-feature draft/annotation scope rows. It never creates missing resources.
func AuthorizeTarget(tx WriteTx, raw []byte) error {
	var target struct {
		ProjectID    string  `json:"project_id"`
		DocumentID   *string `json:"document_id"`
		VersionID    string  `json:"version_id"`
		DraftID      *string `json:"draft_id"`
		AnnotationID string  `json:"annotation_id"`
		Action       string  `json:"action"`
		PagePath     *string `json:"page_path"`
	}
	if json.Unmarshal(raw, &target) != nil {
		return domain.ErrDenied
	}
	project := func(id string) error { _, e := tx.Scope(id); return e }
	document := func(id string) error {
		d, e := tx.Document(id)
		if e != nil {
			return e
		}
		if d.Lifecycle == "trash" || d.Lifecycle == "purged" {
			return domain.ErrNotFound
		}
		if target.ProjectID != "" && d.ProjectID != target.ProjectID {
			return domain.ErrInvalidTarget
		}
		return project(d.ProjectID)
	}
	version := func(id string, page *string) error {
		v, e := tx.PublishedVersion(id)
		if e != nil {
			return e
		}
		if e := document(v.DocumentID); e != nil {
			return e
		}
		if page != nil {
			_, e := tx.Page(id, *page)
			return e
		}
		return nil
	}
	if target.ProjectID != "" {
		if e := project(target.ProjectID); e != nil {
			return e
		}
	}
	if target.DocumentID != nil {
		if e := document(*target.DocumentID); e != nil {
			return e
		}
	}
	if target.VersionID != "" {
		if e := version(target.VersionID, target.PagePath); e != nil {
			return e
		}
	}
	draftID := ""
	if target.DraftID != nil {
		draftID = *target.DraftID
	}
	if draftID != "" && target.Action != "draft_replace" {
		d, e := tx.DraftTarget(draftID)
		if e != nil {
			return e
		}
		if d.State != "preparing" && d.State != "ready" {
			return domain.ErrNotFound
		}
		if target.ProjectID != "" && d.ProjectID != target.ProjectID {
			return domain.ErrInvalidTarget
		}
		if (d.DocumentID == nil) != (target.DocumentID == nil) || d.DocumentID != nil && *d.DocumentID != *target.DocumentID {
			return domain.ErrInvalidTarget
		}
		if e := project(d.ProjectID); e != nil {
			return e
		}
		if d.DocumentID != nil {
			actual, e := tx.Document(*d.DocumentID)
			if e != nil {
				return e
			}
			if actual.ProjectID != d.ProjectID {
				return domain.ErrInvalidTarget
			}
			if e := document(*d.DocumentID); e != nil {
				return e
			}
		}
	}
	if target.Action == "draft_replace" {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		var id string
		_ = json.Unmarshal(fields["draft_id"], &id)
		d, e := tx.DraftTarget(id)
		if e != nil {
			return e
		}
		if d.State != "ready" {
			return domain.ErrNotFound
		}
		if e := project(d.ProjectID); e != nil {
			return e
		}
		if d.DocumentID != nil {
			actual, e := tx.Document(*d.DocumentID)
			if e != nil {
				return e
			}
			if actual.ProjectID != d.ProjectID {
				return domain.ErrInvalidTarget
			}
			if e := document(*d.DocumentID); e != nil {
				return e
			}
		}
	}
	if target.AnnotationID != "" {
		a, e := tx.AnnotationTarget(target.AnnotationID)
		if e != nil {
			return e
		}
		if a.Deleted || a.Revision < 1 {
			return domain.ErrNotFound
		}
		if e := version(a.VersionID, a.PagePath); e != nil {
			return e
		}
	}
	return nil
}
