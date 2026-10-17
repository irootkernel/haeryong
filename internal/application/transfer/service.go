package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
	"regexp"
	"time"
)

var operationID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var ErrInvalid = errors.New("INVALID_REQUEST")
var ErrIntent = errors.New("OPERATION_CONFLICT")
var ErrReconcile = errors.New("RECONCILIATION_REQUIRED")
var ErrPending = errors.New("operation reservation pending")

// AwaitOperation observes only the original durable operation. Pending does not
// authorize redispatch, and an uncertain outcome remains explicit.
func AwaitOperation(ctx context.Context, api interface {
	Operation(context.Context, string) (domain.Response, error)
}, id string) (domain.Response, error) {
	for {
		response, err := api.Operation(ctx, id)
		if !errors.Is(err, ErrPending) {
			return response, err
		}
		select {
		case <-ctx.Done():
			return domain.Response{}, ErrReconcile
		case <-time.After(100 * time.Millisecond):
		}
	}
}

type Service struct {
	API   API
	State State
	NewID func() (string, error)
}

func New(api API, state State) *Service { return &Service{api, state, service.NewID} }
func encoded(v any) []byte              { b, _ := json.Marshal(v); return b }
func (s *Service) keys() (Preparation, error) {
	u, e := s.NewID()
	if e != nil {
		return Preparation{}, e
	}
	complete, e := s.NewID()
	return Preparation{CreateKey: u, CompleteKey: complete, Phase: "capturing"}, e
}
func (s *Service) Run(ctx context.Context, input Input) (Journal, error) {
	identity, e := s.API.Identity(ctx)
	if e != nil {
		return Journal{}, e
	}
	id := input.OperationID
	if id == "" {
		candidate, e := s.NewID()
		if e != nil {
			return Journal{}, e
		}
		id, e = s.State.UsePending(ctx, identity, input, candidate)
		if e != nil {
			return Journal{}, e
		}
	}
	if !operationID.MatchString(id) || input.NewDocument && input.DocumentID != "" {
		return Journal{}, ErrInvalid
	}
	release, e := s.State.Lock(ctx, id)
	if e != nil {
		return Journal{}, e
	}
	defer release()
	j, e := s.State.Load(id)
	if e == nil {
		if j.Identity.LibraryID != identity.LibraryID || j.Identity.PrincipalID != identity.PrincipalID {
			return j, ErrIntent
		}
		if input.Validate != (j.Purpose == "validate") || input.ProjectID != "" && input.ProjectID != j.Selection.ProjectID || input.DocumentID != "" && (j.Selection.DocumentID == nil || *j.Selection.DocumentID != input.DocumentID) || input.NewDocument && j.Selection.DocumentID != nil || input.BaseVersionID != "" && (j.BaseVersionID == nil || *j.BaseVersionID != input.BaseVersionID) || input.VersionID != "" && input.VersionID != j.VersionID {
			return j, ErrIntent
		}
		// Reconcile final dispatch before reading any current local input. A 404 never
		// erases keys/base: the same original request may still be in flight.
		if j.Phase == "final_dispatch" || j.Response != nil {
			response, err := AwaitOperation(ctx, s.API, id)
			if err == nil {
				j.Response = &response
				j.Phase = "accepted"
				if e = s.State.Save(j); e != nil {
					return j, e
				}
				return j, nil
			}
			if !errors.Is(err, domain.ErrNotFound) {
				if response.Status >= 400 {
					j.Response = &response
					j.Phase = "failed"
					if e = s.State.Save(j); e != nil {
						return j, e
					}
				}
				return j, err
			}
		}
		if j.Identity.RuntimeGeneration != identity.RuntimeGeneration {
			return j, ErrReconcile
		}
		if j.Response != nil {
			return j, ErrReconcile
		}
		if j.FinalBody != nil {
			var response domain.Response
			if j.Purpose == "validate" {
				response, e = s.API.Validate(ctx, j.OperationID, j.FinalBody)
			} else {
				response, e = s.API.Import(ctx, j.OperationID, j.FinalBody)
			}
			if e != nil {
				if response.Status >= 400 {
					j.Response = &response
					j.Phase = "failed"
					if saveError := s.State.Save(j); saveError != nil {
						return j, saveError
					}
				}
				return j, e
			}
			j.Response = &response
			j.Phase = "accepted"
			e = s.State.Save(j)
			return j, e
		}

	} else if errors.Is(e, domain.ErrNotFound) {
		selected, err := s.State.Association()
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return j, err
		}
		if input.ProjectID != "" {
			selected = Selection{ProjectID: input.ProjectID}
		}
		if input.DocumentID != "" {
			doc, e := s.API.Document(ctx, input.DocumentID)
			if e != nil {
				return j, e
			}
			if input.ProjectID != "" && input.ProjectID != doc.ProjectID {
				return j, ErrIntent
			}
			selected = Selection{ProjectID: doc.ProjectID, DocumentID: &doc.ID}
		}
		if input.NewDocument {
			selected.DocumentID = nil
		}
		if selected.ProjectID == "" {
			if identity.SelectedProjectID != "" {
				selected.ProjectID = identity.SelectedProjectID
			} else {
				if identity.UnfiledProjectID == "" {
					return j, fmt.Errorf("explicit authorized project required")
				}
				selected.ProjectID = identity.UnfiledProjectID
			}
		}
		var base *string
		if selected.DocumentID != nil {
			doc, e := s.API.Document(ctx, *selected.DocumentID)
			if e != nil {
				return j, e
			}
			if doc.ProjectID != selected.ProjectID {
				return j, ErrIntent
			}
			base = doc.LatestVersionID
			if input.BaseVersionID != "" {
				value := input.BaseVersionID
				base = &value
			}
		} else if input.BaseVersionID != "" {
			return j, ErrIntent
		}
		result, e := s.keys()
		if e != nil {
			return j, e
		}
		j = Journal{OperationID: id, Purpose: "import", Identity: identity, Selection: selected, BaseVersionID: base, VersionID: input.VersionID, Result: result, Phase: "capturing"}
		if input.Validate {
			j.Purpose = "validate"
		}
		if input.Metadata != "" {
			metadata, e := s.keys()
			if e != nil {
				return j, e
			}
			j.Metadata = &metadata
		}
		if e = s.State.Save(j); e != nil {
			return j, e
		}
		if e = s.State.Remember(selected); e != nil {
			return j, e
		}
	} else {
		return j, e
	}
	if j.Result.Snapshot.ID == "" {
		snapshot, e := s.State.Capture(ctx, input.Path)
		if e != nil {
			return j, e
		}
		j.Result.Snapshot = snapshot
		j.Result.Phase = "captured"
		if e = s.State.Save(j); e != nil {
			return j, e
		}
	}
	if j.Metadata != nil && j.Metadata.Snapshot.ID == "" {
		snapshot, e := s.State.Capture(ctx, input.Metadata)
		if e != nil {
			return j, e
		}
		snapshot.Mode = "object"
		j.Metadata.Snapshot = snapshot
		j.Metadata.Phase = "captured"
		if e = s.State.Save(j); e != nil {
			return j, e
		}
	}
	target := encoded(map[string]any{"project_id": j.Selection.ProjectID, "document_id": j.Selection.DocumentID})
	if e = s.prepare(ctx, &j, &j.Result, "result", target); e != nil {
		return j, e
	}
	var metadataRef *importer.ObjectRef
	if j.Metadata != nil {
		metadataTarget := encoded(map[string]any{"action": "import", "operation_id": j.OperationID, "project_id": j.Selection.ProjectID, "document_id": j.Selection.DocumentID})
		if e = s.prepare(ctx, &j, j.Metadata, "metadata", metadataTarget); e != nil {
			return j, e
		}
		detail, e := s.API.Upload(ctx, j.Metadata.UploadID)
		if e != nil {
			return j, e
		}
		metadataRef = detail.ObjectRef
	}
	if j.FinalBody == nil {
		body := map[string]any{"upload_id": j.Result.UploadID}
		if j.Purpose == "import" {
			body["project_id"] = j.Selection.ProjectID
			body["document_id"] = j.Selection.DocumentID
			body["base_version_id"] = j.BaseVersionID
			if j.VersionID != "" {
				body["version_id"] = j.VersionID
			}
		}
		if metadataRef != nil {
			body["metadata_ref"] = metadataRef
		}
		j.FinalBody = encoded(body)
	}
	j.Phase = "final_dispatch"
	if e = s.State.Save(j); e != nil {
		return j, e
	}
	var response domain.Response
	if j.Purpose == "validate" {
		response, e = s.API.Validate(ctx, j.OperationID, j.FinalBody)
	} else {
		response, e = s.API.Import(ctx, j.OperationID, j.FinalBody)
	}
	if e != nil {
		if response.Status >= 400 {
			j.Response = &response
			j.Phase = "failed"
			if saveError := s.State.Save(j); saveError != nil {
				return j, saveError
			}
		}
		return j, e
	}
	j.Response = &response
	j.Phase = "accepted"
	e = s.State.Save(j)
	return j, e
}
func (s *Service) prepare(ctx context.Context, j *Journal, p *Preparation, purpose string, target []byte) error {
	if p.UploadID == "" {
		if p.Phase == "create_dispatch" {
			response, e := AwaitOperation(ctx, s.API, p.CreateKey)
			if e == nil {
				var out Created
				if e = json.Unmarshal(response.Body, &out); e != nil {
					return e
				}
				p.UploadID = out.ID
				p.Checkpoint = out.Checkpoint
				if p.UploadID == "" {
					return ErrReconcile
				}
				if e = s.State.Save(*j); e != nil {
					return e
				}
			} else if !errors.Is(e, domain.ErrNotFound) {
				return e
			}
		}
		if p.UploadID == "" {
			p.Phase = "create_dispatch"
			if e := s.State.Save(*j); e != nil {
				return e
			}
			out, e := s.API.Create(ctx, p.CreateKey, importer.CreateRequest{Purpose: purpose, Target: target, InputMode: p.Snapshot.Mode, SelectionName: p.Snapshot.SelectionName})
			if e != nil {
				return e
			}
			p.UploadID = out.ID
			p.Checkpoint = out.Checkpoint
			p.Phase = "transferring"
			if e = s.State.Save(*j); e != nil {
				return e
			}
		}
	}
	detail, e := s.API.Upload(ctx, p.UploadID)
	if e != nil {
		return e
	}
	if detail.State == "abandoned" {
		return ErrIntent
	}
	if detail.State == "sealed" {
		p.Phase = "sealed"
		return s.State.Save(*j)
	}
	if detail.State == "open" && p.Phase != "complete_dispatch" {
		e = s.State.Entries(p.Snapshot, func(entry Entry) error {
			if entry.Kind != "file" {
				ack, e := s.API.Put(ctx, p.UploadID, importer.Chunk{Path: entry.Path, Kind: entry.Kind, LinkTarget: entry.Link, Final: true}, nil)
				if e != nil {
					return e
				}
				p.Checkpoint = ack.Checkpoint
				return s.State.Save(*j)
			}
			return s.transferFile(ctx, j, p, entry)
		})
		if e != nil {
			return e
		}
		p.Checkpoint.Generation++
		p.Checkpoint.Complete = true
		p.Phase = "complete_dispatch"
		if e = s.State.Save(*j); e != nil {
			return e
		}
	}
	if detail.State != "sealing" || p.Phase == "complete_dispatch" {
		response, e := s.API.Complete(ctx, p.UploadID, p.CompleteKey, p.Checkpoint)
		if e != nil {
			return e
		}
		var accepted struct {
			JobID string `json:"job_id"`
		}
		_ = json.Unmarshal(response.Body, &accepted)
		p.JobID = accepted.JobID
		p.Phase = "sealing"
		if e = s.State.Save(*j); e != nil {
			return e
		}
	}
	// No total size/time quota. Caller cancellation retains the exact journal and
	// upload; worker failure and cancellation are visible, never treated as seal.
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		detail, e = s.API.Upload(ctx, p.UploadID)
		if e != nil {
			return e
		}
		if detail.State == "sealed" {
			p.Phase = "sealed"
			return s.State.Save(*j)
		}
		if detail.State == "abandoned" {
			return ErrIntent
		}
		if detail.Error != nil && *detail.Error != "" {
			return fmt.Errorf("seal failed: %s", *detail.Error)
		}
		if detail.State == "open" {
			return fmt.Errorf("verification stopped; retained input requires explicit resume")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Resume verifies the original upload binding and confirmed ranges. It never
// turns an existing upload into publication or changes its exact target.
func (s *Service) Resume(ctx context.Context, id, inputPath, completionID string) (Journal, error) {
	identity, e := s.API.Identity(ctx)
	if e != nil {
		return Journal{}, e
	}
	detail, e := s.API.Upload(ctx, id)
	if e != nil {
		return Journal{}, e
	}
	if detail.State == "abandoned" {
		return Journal{}, ErrIntent
	}

	if completionID == "" {
		candidate, e := s.NewID()
		if e != nil {
			return Journal{}, e
		}
		completionID, e = s.State.UsePending(ctx, identity, Input{ResumeUploadID: id}, candidate)
		if e != nil {
			return Journal{}, e
		}
	}
	if !operationID.MatchString(completionID) {
		return Journal{}, ErrInvalid
	}
	release, e := s.State.Lock(ctx, completionID)
	if e != nil {
		return Journal{}, e
	}
	defer release()
	journal, e := s.State.Load(completionID)
	if e == nil {
		if journal.Purpose != "resume" || journal.Result.UploadID != id || journal.Identity.PrincipalID != identity.PrincipalID || journal.Identity.LibraryID != identity.LibraryID {
			return journal, ErrIntent
		}
		if journal.Identity.RuntimeGeneration != identity.RuntimeGeneration {
			return journal, ErrReconcile
		}
	} else if errors.Is(e, domain.ErrNotFound) {
		journal = Journal{OperationID: completionID, Purpose: "resume", Identity: identity, Phase: "capturing", Result: Preparation{UploadID: id, CompleteKey: completionID, Phase: "capturing"}}
		if detail.State == "sealing" {
			journal.Result.Checkpoint = detail.Checkpoint
			journal.Result.Phase = "complete_dispatch"
		}
		if e = s.State.Save(journal); e != nil {
			return journal, e
		}
	} else {
		return journal, e
	}
	if detail.State == "sealed" {
		response, e := AwaitOperation(ctx, s.API, completionID)
		if errors.Is(e, domain.ErrNotFound) {
			journal.Result.Checkpoint = detail.Checkpoint
			journal.Result.Phase = "complete_dispatch"
			if e = s.State.Save(journal); e != nil {
				return journal, e
			}
			response, e = s.API.Complete(ctx, id, completionID, journal.Result.Checkpoint)
		}
		if e != nil {
			return journal, e
		}
		journal.Response = &response
		journal.Result.Phase = "sealed"
		journal.Phase = "sealed"
		return journal, s.State.Save(journal)
	}
	if detail.State == "open" && journal.Result.Snapshot.ID == "" && journal.Result.Phase != "complete_dispatch" {
		if inputPath == "" {
			return journal, ErrInvalid
		}
		snapshot, e := s.State.Capture(ctx, inputPath)
		if e != nil {
			return journal, e
		}
		if detail.InputMode == "object" {
			snapshot.Mode = "object"
		}
		if snapshot.Mode != detail.InputMode {
			return journal, ErrIntent
		}
		if detail.SelectionName != nil {
			snapshot.SelectionName = *detail.SelectionName
		}
		journal.Result.Snapshot = snapshot
		journal.Result.Phase = "transferring"
		if e = s.State.Save(journal); e != nil {
			return journal, e
		}
	}
	if e = s.prepare(ctx, &journal, &journal.Result, detail.Purpose, detail.Target); e != nil {
		return journal, e
	}
	journal.Phase = "sealed"
	e = s.State.Save(journal)
	return journal, e
}
