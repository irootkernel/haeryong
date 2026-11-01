package transfer

import (
	"context"
	importer "haeryong.local/haeryong/internal/application/import"
)

// PrepareDraftReplacement owns ordinary result/optional metadata upload only.
// The caller retains the individual draft validator and final PATCH key.
func (s *Service) PrepareDraftReplacement(ctx context.Context, j *Journal) (string, *importer.ObjectRef, error) {
	if j.Purpose != "draft_replace" || j.Draft == nil || j.Draft.Action != "replace" || j.Method != "PATCH" || j.Target != "/api/v1/drafts/"+j.Draft.ID {
		return "", nil, ErrIntent
	}
	current, e := s.API.Identity(ctx)
	if e != nil {
		return "", nil, e
	}
	if current.LibraryID != j.Identity.LibraryID || current.PrincipalID != j.Identity.PrincipalID {
		return "", nil, ErrIntent
	}
	if current.RuntimeGeneration != j.Identity.RuntimeGeneration {
		return "", nil, ErrReconcile
	}
	if j.Result.CreateKey == "" {
		prepared, e := s.keys()
		if e != nil {
			return "", nil, e
		}
		j.Result = prepared
		if e = s.State.Save(*j); e != nil {
			return "", nil, e
		}
	}
	if j.Result.Snapshot.ID == "" {
		snapshot, e := s.State.Capture(ctx, j.Draft.SourcePath)
		if e != nil {
			return "", nil, e
		}
		j.Result.Snapshot = snapshot
		if e = s.State.Save(*j); e != nil {
			return "", nil, e
		}
	}
	target := encoded(map[string]any{"draft_id": j.Draft.ID, "project_id": j.Selection.ProjectID, "document_id": j.Selection.DocumentID})
	if e = s.prepare(ctx, j, &j.Result, "result", target); e != nil {
		return "", nil, e
	}
	if j.Draft.MetadataPath == "" {
		return j.Result.UploadID, nil, nil
	}
	if j.Metadata == nil {
		prepared, e := s.keys()
		if e != nil {
			return "", nil, e
		}
		j.Metadata = &prepared
		if e = s.State.Save(*j); e != nil {
			return "", nil, e
		}
	}
	if j.Metadata.Snapshot.ID == "" {
		snapshot, e := s.State.Capture(ctx, j.Draft.MetadataPath)
		if e != nil {
			return "", nil, e
		}
		snapshot.Mode = "object"
		j.Metadata.Snapshot = snapshot
		if e = s.State.Save(*j); e != nil {
			return "", nil, e
		}
	}
	metadataTarget := encoded(map[string]any{"operation_id": j.OperationID, "action": "draft_replace", "draft_id": j.Draft.ID})
	if e = s.prepare(ctx, j, j.Metadata, "metadata", metadataTarget); e != nil {
		return "", nil, e
	}
	detail, e := s.API.Upload(ctx, j.Metadata.UploadID)
	if e != nil {
		return "", nil, e
	}
	if detail.State != "sealed" || !detail.Reusable || detail.ObjectRef == nil {
		return "", nil, ErrReconcile
	}
	ref := *detail.ObjectRef
	ref.MediaType = "application/json"
	return j.Result.UploadID, &ref, nil
}
