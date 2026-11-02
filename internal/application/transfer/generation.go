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
	target := encoded(map[string]any{"operation_id": j.OperationID, "action": "generation", "project_id": j.Selection.ProjectID, "document_id": j.Selection.DocumentID})
	return s.prepareRequestText(ctx, j, target)
}

func (s *Service) PrepareQuestionText(ctx context.Context, j *Journal, page *string) (*importer.ObjectRef, error) {
	if j.Purpose != "question" || j.Question == nil || j.Method != "POST" || j.VersionID == "" || j.Target != "/api/v1/versions/"+j.VersionID+"/questions" || j.Result.Snapshot.ID == "" {
		return nil, ErrIntent
	}
	target := encoded(map[string]any{"operation_id": j.OperationID, "action": "question", "version_id": j.VersionID, "page_path": page})
	return s.prepareRequestText(ctx, j, target)
}
func (s *Service) prepareRequestText(ctx context.Context, j *Journal, target []byte) (*importer.ObjectRef, error) {
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
