package transfer

import (
	"context"

	importer "haeryong.local/haeryong/internal/application/import"
)

// PrepareGenerationPrompt reuses ordinary resumable object upload/sealing. Its
// preparation keys are distinct from the final generation operation key.
func (s *Service) PrepareGenerationPrompt(ctx context.Context, j *Journal) (*importer.ObjectRef, error) {
	if j.Purpose != "generation" || j.Generation == nil || j.Method != "POST" || j.Target != "/api/v1/generations" || j.Selection.ProjectID == "" || j.Result.Snapshot.ID == "" {
		return nil, ErrIntent
	}
	current, e := s.API.Identity(ctx)
	if e != nil {
		return nil, e
	}
	if current.PrincipalID != j.Identity.PrincipalID || current.LibraryID != j.Identity.LibraryID {
		return nil, ErrIntent
	}
	if current.RuntimeGeneration != j.Identity.RuntimeGeneration {
		return nil, ErrReconcile
	}
	if j.Result.CreateKey == "" {
		snapshot := j.Result.Snapshot
		prepared, e := s.keys()
		if e != nil {
			return nil, e
		}
		prepared.Snapshot = snapshot
		j.Result = prepared
		if e = s.State.Save(*j); e != nil {
			return nil, e
		}
	}
	target := encoded(map[string]any{"operation_id": j.OperationID, "action": "generation", "project_id": j.Selection.ProjectID, "document_id": j.Selection.DocumentID})
	if e = s.prepare(ctx, j, &j.Result, "body", target); e != nil {
		return nil, e
	}
	detail, e := s.API.Upload(ctx, j.Result.UploadID)
	if e != nil {
		return nil, e
	}
	if detail.State != "sealed" || !detail.Reusable || detail.ObjectRef == nil {
		return nil, ErrReconcile
	}
	ref := *detail.ObjectRef
	ref.MediaType = "text/plain"
	return &ref, nil
}
