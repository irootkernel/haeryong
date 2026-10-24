// Package transfer orchestrates one durable caller-side intent. It has no HTTP,
// SQL or filesystem dependencies; its journal precedes every dispatch.
package transfer

import (
	"context"
	"encoding/json"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/domain"
	"io"
)

type Identity struct {
	SelectedProjectID string `json:"selected_project_id,omitempty"`
	LibraryID         string `json:"library_id"`
	RuntimeGeneration string `json:"runtime_generation"`
	PrincipalID       string `json:"principal_id"`
	UnfiledProjectID  string `json:"unfiled_project_id"`
}
type Created struct {
	ID         string            `json:"upload_id"`
	Checkpoint domain.Checkpoint `json:"checkpoint"`
}
type API interface {
	Identity(context.Context) (Identity, error)
	Document(context.Context, string) (Document, error)
	Operation(context.Context, string) (domain.Response, error)
	Create(context.Context, string, importer.CreateRequest) (Created, error)
	Put(context.Context, string, importer.Chunk, io.Reader) (importer.ChunkResult, error)
	Upload(context.Context, string) (importer.UploadDetail, error)
	ConfirmedRanges(context.Context, string, string, string) (RangePage, error)
	Complete(context.Context, string, string, domain.Checkpoint) (domain.Response, error)
	Import(context.Context, string, json.RawMessage) (domain.Response, error)
	Validate(context.Context, string, json.RawMessage) (domain.Response, error)
	Job(context.Context, string) (domain.Job, error)
}
type ConfirmedRange struct {
	Path, Hash   string
	Offset, Size int64
}
type RangePage struct {
	Items      []ConfirmedRange
	NextCursor *string
}
type Document struct {
	ID              string  `json:"document_id"`
	ProjectID       string  `json:"project_id"`
	LatestVersionID *string `json:"latest_version_id"`
}
type Selection struct {
	ProjectID  string  `json:"project_id"`
	DocumentID *string `json:"document_id"`
}
type Input struct {
	ResumeUploadID                                                               string `json:"resume_upload_id,omitempty"`
	OperationID, Path, Metadata, ProjectID, DocumentID, BaseVersionID, VersionID string
	NewDocument, Validate                                                        bool
}
type Entry struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Link string `json:"link_target,omitempty"`
	Hash string `json:"sha256,omitempty"`
	Size int64  `json:"byte_size"`
}
type Snapshot struct {
	ID            string `json:"id"`
	Mode          string `json:"input_mode"`
	SelectionName string `json:"selection_name"`
	Identity      string `json:"sha256"`
}
type Preparation struct {
	CreateKey   string            `json:"create_operation_id"`
	CompleteKey string            `json:"complete_operation_id"`
	UploadID    string            `json:"upload_id"`
	Snapshot    Snapshot          `json:"snapshot"`
	Checkpoint  domain.Checkpoint `json:"checkpoint"`
	Phase       string            `json:"phase"`
	JobID       string            `json:"job_id,omitempty"`
}
type Journal struct {
	Preconditions map[string]string `json:"preconditions,omitempty"`
	Method        string            `json:"method,omitempty"`
	Target        string            `json:"target,omitempty"`
	OperationID   string            `json:"operation_id"`
	Purpose       string            `json:"purpose"`
	Identity      Identity          `json:"service"`
	Selection     Selection         `json:"selection"`
	BaseVersionID *string           `json:"base_version_id"`
	VersionID     string            `json:"version_id,omitempty"`
	Result        Preparation       `json:"result"`
	Metadata      *Preparation      `json:"metadata,omitempty"`
	Phase         string            `json:"dispatch_phase"`
	FinalBody     json.RawMessage   `json:"final_request,omitempty"`
	Response      *domain.Response  `json:"response,omitempty"`
}
type State interface {
	UsePending(context.Context, Identity, Input, string) (string, error)
	FinishPending(context.Context, Identity, Input, string) error
	Lock(context.Context, string) (func() error, error)
	Load(string) (Journal, error)
	Save(Journal) error
	Association() (Selection, error)
	Remember(Selection) error
	Capture(context.Context, string) (Snapshot, error)
	Entries(Snapshot, func(Entry) error) error
	Open(Snapshot, Entry) (io.ReadSeekCloser, error)
}
