// Package domain defines SQL-free values for the current durable foundations.
package domain

import "errors"

var (
	ErrNotFound       = errors.New("record not found")
	ErrConflict       = errors.New("operation or immutable identity conflict")
	ErrPrecondition   = errors.New("resource precondition failed")
	ErrStaleWorker    = errors.New("worker generation is fenced")
	ErrTransition     = errors.New("invalid state transition")
	ErrPinned         = errors.New("object is pinned or referenced")
	ErrDeleting       = errors.New("object deletion is reserved")
	ErrReconciliation = errors.New("operation reconciliation required")
	ErrDenied         = errors.New("current authority denied")
)

type Object struct {
	GCToken string
	Hash    string
	Size    int64
	State   string
}
type Pin struct{ ID, Kind, OwnerID, Hash string }
type Reference struct{ Kind, ID, Hash string }
type GCReservation struct{ Hash, Token string }
type Library struct {
	ID, ProfileID, RuntimeGeneration, StreamEpoch, AuthEpoch, UnfiledProjectID string
	SchemaRevision                                                             int
}
type Health struct {
	Library                               Library
	Engine, Journal                       string
	Synchronous, ForeignKeys, BusyTimeout int
}

type OperationKey struct{ PrincipalID, OperationID string }
type Request struct {
	Key            OperationKey
	Method, Target string
	Body           []byte
	Preconditions  map[string]string
	Omitted        []string
}
type Response struct {
	Status  int
	Body    []byte
	Headers map[string]string
}
type Receipt struct {
	Key           OperationKey
	Fingerprint   string
	Original      Request
	Normalization []byte
	State         string
	Response      *Response
}
type Resource struct {
	Kind, ID string
	Revision int64
	Value    []byte
}
type Event struct {
	StreamEpoch                    string
	Sequence                       int64
	ResourceKind, ResourceID, Kind string
	Revision                       int64
	Body                           []byte
}

type Project struct {
	ID, Name, Lifecycle           string
	Revision, PermissionsRevision int64
}
type Document struct {
	ID, ProjectID, Title, Lifecycle, LatestVersionID string
	Revision                                         int64
}
type Version struct {
	ID, DocumentID, ContentSetID, BaseVersionID, Title, ViewerProfile, SourceBasis string
	Sequence                                                                       int64
	DefaultPage                                                                    *string
	Metadata                                                                       []byte
}
type Entry struct {
	ContentSetID, Path, Kind, Hash, LinkText string
	Size                                     int64
}
type Page struct {
	VersionID, Path string
	Metadata        []byte
}
type Region struct {
	VersionID, PagePath, ID string
	Metadata                []byte
}
type Source struct {
	VersionID, ID string
	Metadata      []byte
}
type Evidence struct {
	VersionID, ID, SourceID string
	Metadata                []byte
}
type Configuration struct {
	Revision int64
	Value    []byte
	Actor    string
}

type Job struct {
	Scope                               []byte
	ConfigRevision                      int64
	Permissions                         map[string]int64
	Deadline                            string
	ID, Kind, State, ParentID, UploadID string
	AttemptGeneration                   int64
	CancelRequested                     bool
	Error                               string
}
type Checkpoint struct {
	Generation      int64  `json:"generation"`
	InventorySHA256 string `json:"inventory_sha256"`
	AcceptedBytes   string `json:"accepted_bytes"`
	Complete        bool   `json:"complete_input"`
}
type Upload struct {
	ID, PrincipalID, Purpose, InputMode, SelectionName string
	Target                                             []byte
	State                                              string
	Checkpoint                                         Checkpoint
	SealJobID, ContentSetID, Error                     string
}
type UploadRange struct {
	UploadID, Path string
	Offset, Size   int64
	Hash           string
}

type ScopeState struct {
	Project        Project
	ConfigRevision int64
}
