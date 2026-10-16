// Package importer owns streamed staging and atomic directory publication.
package importer

import (
	"context"
	"encoding/json"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
)

// Bytes verifies and flushes before the short typed metadata transaction.
// commit must reject stale authority/fences before durable publication.
type Bytes interface {
	Put(context.Context, io.Reader, func(app.WriteTx, domain.Object) error) (domain.Object, error)
	Read(context.Context, domain.Object, domain.Pin, func(io.ReadSeeker) error) error
}
type EntryInput struct {
	Path, Kind, LinkTarget string
	Size                   int64
}
type Codec interface {
	Expand(context.Context, io.ReaderAt, int64, func(EntryInput, io.Reader) error) error
}
type CreateRequest struct {
	Purpose       string          `json:"purpose"`
	Target        json.RawMessage `json:"target"`
	InputMode     string          `json:"input_mode"`
	SelectionName string          `json:"selection_name,omitempty"`
}
type Chunk struct {
	Path, Kind, LinkTarget, Hash string
	Offset                       int64
	Length                       *int64
	Final                        bool
}
type ImportRequest struct {
	UploadID             string
	DocumentID           *string
	DocumentPresent      bool
	ProjectID, VersionID string
	BaseVersionID        *string
	BasePresent          bool
	Metadata             io.Reader
	MetadataRef          *ObjectRef
}
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}
type UploadQuery struct {
	Cursor, State string
	Unadopted     bool
}
type EntryProgress struct {
	Path          string  `json:"path"`
	Kind          string  `json:"kind"`
	LinkTarget    *string `json:"link_target,omitempty"`
	ByteSize      string  `json:"byte_size"`
	AcceptedBytes string  `json:"accepted_bytes"`
	Complete      bool    `json:"complete"`
}
type ChunkResult struct {
	Offset     string            `json:"accepted_offset"`
	Checkpoint domain.Checkpoint `json:"checkpoint"`
}
type UploadView struct {
	ID                string          `json:"upload_id"`
	PrincipalID       string          `json:"originating_principal_id"`
	DeviceID          *string         `json:"originating_device_id"`
	Purpose           string          `json:"purpose"`
	InputMode         string          `json:"input_mode"`
	SelectionName     *string         `json:"selection_name"`
	Target            json.RawMessage `json:"target"`
	State             string          `json:"state"`
	ByteSize          string          `json:"byte_size"`
	StagingBytes      string          `json:"staging_bytes"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
	Reusable          bool            `json:"reusable"`
	UnavailableReason *string         `json:"unavailable_reason"`
	PinSummary        map[string]int  `json:"pin_summary"`
	PinsURL           string          `json:"pins_url"`
	DetailURL         string          `json:"detail_url"`
	Revision          int64           `json:"revision"`
	ETag              string          `json:"etag"`
}
type UploadDetail struct {
	UploadView
	Checkpoint      domain.Checkpoint `json:"checkpoint"`
	RangesURL       string            `json:"ranges_url"`
	EntriesURL      string            `json:"entries_url"`
	Error           *string           `json:"error"`
	SealJobID       *string           `json:"seal_job_id"`
	SealOperationID *string           `json:"seal_operation_id"`
	ContentSetID    *string           `json:"sealed_content_set_id"`
	ObjectRef       *ObjectRef        `json:"sealed_object_ref"`
	TransportEntry  *string           `json:"transport_entry,omitempty"`
}

type ObjectRef struct {
	ID        string `json:"object_id"`
	Hash      string `json:"bytes_sha256"`
	Size      string `json:"byte_size"`
	MediaType string `json:"media_type"`
}
