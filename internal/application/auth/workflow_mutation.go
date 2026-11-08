package auth

import (
	"encoding/json"
	"errors"
	"strings"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// AuthorizeFreshWorkflowMutation separates issuer-selected output authority
// from prior-context read projects. It supplements normal scope/lifecycle
// checks, and does not reinterpret an existing receipt or accepted job.
func AuthorizeFreshWorkflowMutation(tx app.WriteTx, actor domain.Session, r domain.Request) error {
	if actor.Scope.Kind != "workflow" || r.Method != "POST" || r.Key.OperationID == "" {
		return nil
	}
	path := strings.SplitN(r.Target, "?", 2)[0]
	var body struct {
		Purpose  string          `json:"purpose"`
		Target   json.RawMessage `json:"target"`
		UploadID string          `json:"upload_id"`
	}
	var raw []byte
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch path {
	case "/api/v1/generations":
		raw = r.Body
	case "/api/v1/scans":
		if len(r.Body) == 0 {
			return nil
		}
		var scanBody struct {
			ProjectID   string   `json:"project_id"`
			ProjectIDs  []string `json:"project_ids"`
			DocumentID  string   `json:"document_id"`
			DocumentIDs []string `json:"document_ids"`
			VersionID   string   `json:"version_id"`
		}
		if json.Unmarshal(r.Body, &scanBody) != nil {
			return domain.ErrInvalidTarget
		}
		resolved := ""
		addProject := func(pid string) error {
			pid = strings.TrimSpace(pid)
			if pid == "" {
				return nil
			}
			if resolved == "" {
				resolved = pid
			} else if resolved != pid {
				return domain.ErrDenied
			}
			return nil
		}
		if err := addProject(scanBody.ProjectID); err != nil {
			return err
		}
		for _, pid := range scanBody.ProjectIDs {
			if err := addProject(pid); err != nil {
				return err
			}
		}
		docIDs := append([]string{}, scanBody.DocumentIDs...)
		if strings.TrimSpace(scanBody.DocumentID) != "" {
			docIDs = append(docIDs, scanBody.DocumentID)
		}
		for _, docID := range docIDs {
			docID = strings.TrimSpace(docID)
			if docID == "" {
				continue
			}
			d, e := tx.Document(docID)
			if e != nil {
				return e
			}
			if err := addProject(d.ProjectID); err != nil {
				return err
			}
		}
		if vID := strings.TrimSpace(scanBody.VersionID); vID != "" {
			v, e := tx.PublishedVersion(vID)
			if e != nil {
				return e
			}
			d, e := tx.Document(v.DocumentID)
			if e != nil {
				return e
			}
			if err := addProject(d.ProjectID); err != nil {
				return err
			}
		}
		if resolved == "" {
			resolved = actor.MutationProjectID
		}
		if resolved == "" {
			return domain.ErrDenied
		}
		raw, _ = json.Marshal(map[string]string{"project_id": resolved})
	case "/api/v1/uploads":
		if json.Unmarshal(r.Body, &body) != nil {
			return domain.ErrInvalidTarget
		}
		var target struct {
			Action  string  `json:"action"`
			DraftID *string `json:"draft_id"`
		}
		if json.Unmarshal(body.Target, &target) != nil {
			return domain.ErrInvalidTarget
		}
		// Exact draft anchors and non-generation context/evidence transfers keep
		// their own authority; they are not fresh project output selection.
		if target.DraftID != nil || !(body.Purpose == "result" || body.Purpose == "metadata" && target.Action == "import" || body.Purpose == "body" && target.Action == "generation") {
			return nil
		}
		raw = body.Target
	case "/api/v1/imports", "/api/v1/packages/validate":
		if json.Unmarshal(r.Body, &body) != nil {
			return domain.ErrInvalidTarget
		}
		raw = body.Target
		if len(raw) == 0 && body.UploadID != "" {
			u, e := tx.Upload(body.UploadID)
			if e != nil {
				return e
			}
			raw = u.Target
		}
		if len(raw) == 0 {
			raw = r.Body
		}
	default:
		if len(parts) != 5 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "projects" || parts[4] != "source-captures" {
			return nil
		}
		raw, _ = json.Marshal(map[string]string{"project_id": parts[3]})
	}
	project, e := app.TargetProject(tx, raw)
	if e != nil {
		return e
	}
	// A coarse preflight has no resolved target. The consumer checks the
	// normalized target with the same key before reserving any fresh effect.
	if project == "" {
		return nil
	}
	if _, e = tx.Receipt(r.Key); e == nil {
		return nil
	} else if !errors.Is(e, domain.ErrNotFound) {
		return e
	}
	if actor.MutationProjectID == "" || actor.MutationProjectID != project {
		return domain.ErrDenied
	}
	p, e := tx.WorkflowPrincipal(actor.PrincipalID)
	if e != nil {
		return e
	}
	selection, e := tx.WorkflowSelection(p.BindingID)
	if e != nil {
		return e
	}
	if selection.PrincipalID != actor.PrincipalID || selection.ProjectID != project {
		return domain.ErrDenied
	}
	return nil
}
