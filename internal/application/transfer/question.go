package transfer

import (
	"context"
	importer "haeryong.local/haeryong/internal/application/import"
)

// Display state owns separate preparation keys and an exact context target.
// It is never sent as a large final question control envelope.
func (s *Service) PrepareQuestionContext(ctx context.Context, j *Journal, page *string) (string, *importer.ObjectRef, error) {
	if j.Purpose != "question" || j.Question == nil || j.Question.Context == nil || j.Question.Context.Snapshot.ID == "" || j.VersionID == "" {
		return "", nil, ErrIntent
	}
	identity, e := s.API.Identity(ctx)
	if e != nil {
		return "", nil, e
	}
	if identity.LibraryID != j.Identity.LibraryID || identity.PrincipalID != j.Identity.PrincipalID {
		return "", nil, ErrIntent
	}
	if identity.RuntimeGeneration != j.Identity.RuntimeGeneration {
		return "", nil, ErrReconcile
	}
	p := j.Question.Context
	if p.CreateKey == "" {
		prepared, e := s.keys()
		if e != nil {
			return "", nil, e
		}
		prepared.Snapshot = p.Snapshot
		*p = prepared
		if e = s.State.Save(*j); e != nil {
			return "", nil, e
		}
	}
	target := encoded(map[string]any{"version_id": j.VersionID, "page_path": page, "render_instance_id": nil})
	if e = s.prepare(ctx, j, p, "context", target); e != nil {
		return "", nil, e
	}
	detail, e := s.API.Upload(ctx, p.UploadID)
	if e != nil {
		return "", nil, e
	}
	if detail.State != "sealed" || !detail.Reusable || detail.ObjectRef == nil {
		return "", nil, ErrReconcile
	}
	ref := *detail.ObjectRef
	ref.MediaType = "application/json"
	return p.UploadID, &ref, nil
}
