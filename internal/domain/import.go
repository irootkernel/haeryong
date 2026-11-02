package domain

import "encoding/json"

// BodyReference names complete sealed request bytes. The handle remains bound
// to its operation/action target; content digest and size define byte identity.
type BodyReference struct {
	ID        string `json:"object_id"`
	Hash      string `json:"bytes_sha256"`
	Size      string `json:"byte_size"`
	MediaType string `json:"media_type"`
}

type UploadEntry struct {
	UploadID, Path, Kind, LinkText string
	ExpectedSize                   *int64
	Final                          bool
	Size                           int64
	Hash                           string
}
type ImportIntent struct {
	ExplanationReferences                                                                                                                              []GenerationReference `json:",omitempty"`
	JobID, PrincipalID, UploadID, ContentSetID, PayloadSHA, MetadataHash, ProjectID, DocumentID, VersionID, BaseVersionID, Title, CreatedAt, AuthEpoch string
	NewDocument                                                                                                                                        bool
	DocumentRevision, PermissionsRevision, ConfigRevision                                                                                              int64
	Normalization                                                                                                                                      json.RawMessage
}

type UploadStats struct {
	StagingBytes                       string
	Staging, Adopted, Active, Recovery int
}

type DraftTarget struct {
	ID, ProjectID string
	DocumentID    *string
	State         string
	Revision      int64
}
type AnnotationTarget struct {
	ID, VersionID string
	PagePath      *string
	Revision      int64
	Deleted       bool
}

type MetadataIndex struct {
	ID, PrincipalID, Hash, ContentSetID, JobID string
	Complete                                   bool
}
