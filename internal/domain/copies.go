package domain

import "encoding/json"

// CopyRequest is a host-local owner request, never an arbitrary file API.
type CopyRequest struct {
	Runtime             string   `json:"runtime_generation,omitempty"`
	Action              string   `json:"action"`
	ID                  string   `json:"id,omitempty"`
	Destination         string   `json:"destination,omitempty"`
	MetadataDestination string   `json:"metadata_destination,omitempty"`
	Format              string   `json:"format,omitempty"`
	VersionID           string   `json:"version_id,omitempty"`
	DocumentID          string   `json:"document_id,omitempty"`
	ProjectID           string   `json:"project_id,omitempty"`
	Versions            string   `json:"versions,omitempty"`
	Lifecycle           string   `json:"lifecycle,omitempty"`
	Records             []string `json:"records,omitempty"`
	RetainedSources     bool     `json:"include_retained_sources,omitempty"`
	Confirmed           bool     `json:"confirmed,omitempty"`
	Cursor              string   `json:"cursor,omitempty"`
}

// CopySet is private coordinator state. Host paths never enter job/status DTOs.
type CopySet struct {
	ID, JobID, OwnerID, LibraryID, Runtime               string
	Kind, Stage, Destination, Directory, Snapshot        string
	Request                                              CopyRequest
	CreatedAt, AttemptAt, CompletedAt, VerifiedAt, Error string
	AfterHash                                            string
	Offset                                               int64
	SnapshotSHA, InventorySHA                            string
	ObjectCount                                          int64
	PublicationReady                                     bool
	Capturing                                            bool
	PlanDigest                                           string
	ExpectedIndexSHA                                     string
	ExpectedOutputSHA, ExpectedMetadataSHA               string
	Permissions                                          map[string]int64
}

type CopyVersion struct {
	DocumentID      string  `json:"document_id"`
	VersionID       string  `json:"version_id"`
	ContentSetID    string  `json:"-"`
	PayloadSHA      string  `json:"payload_sha256"`
	ManifestSHA     string  `json:"manifest_sha256"`
	MetadataSHA     string  `json:"supplied_metadata_sha256,omitempty"`
	PayloadRoot     string  `json:"payload_root"`
	InventoryPath   string  `json:"inventory_path"`
	MetadataRoot    *string `json:"metadata_root"`
	RecordsSHA      string  `json:"records_sha256,omitempty"`
	DependenciesSHA string  `json:"dependencies_sha256,omitempty"`
}

type BackupStatus struct {
	Mode         string          `json:"mode"`
	Available    bool            `json:"available"`
	Reason       string          `json:"reason,omitempty"`
	LastComplete *BackupComplete `json:"last_complete_verified"`
	LastAttempt  *BackupAttempt  `json:"last_attempt"`
	LastTrial    TrialRestore    `json:"last_trial_restore"`
}
type BackupComplete struct {
	ID          string `json:"set_id"`
	CompletedAt string `json:"completed_at"`
	VerifiedAt  string `json:"verified_at"`
	Age         int64  `json:"age_seconds"`
}
type BackupAttempt struct {
	JobID       string  `json:"job_id"`
	State       string  `json:"state"`
	Stage       string  `json:"stage"`
	CompletedAt *string `json:"completed_at"`
	Error       string  `json:"error,omitempty"`
}
type TrialRestore struct {
	State       string  `json:"state"`
	CompletedAt *string `json:"completed_at"`
	Error       string  `json:"error,omitempty"`
}
type CopyResult struct {
	OperationID string `json:"operation_id,omitempty"`
	ID          string `json:"set_id"`
	JobID       string `json:"job_id,omitempty"`
	PlanID      string `json:"plan_id,omitempty"`
	State       string `json:"state"`
	Digest      string `json:"digest,omitempty"`
}
type CopySelection struct {
	DocumentID  string `json:"document_id"`
	VersionID   string `json:"version_id"`
	PayloadSHA  string `json:"payload_sha256"`
	ManifestSHA string `json:"manifest_sha256"`
	MetadataSHA string `json:"supplied_metadata_sha256,omitempty"`
}
type CopyPlanPage struct {
	ID         string          `json:"set_id"`
	PlanID     string          `json:"plan_id"`
	State      string          `json:"state"`
	Digest     string          `json:"digest"`
	Items      []CopySelection `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}
type RecoveryHold struct {
	ID                    string          `json:"restore_id"`
	State                 string          `json:"state"`
	OriginalConfiguration json.RawMessage `json:"original_configuration"`
	CurrentConfiguration  json.RawMessage `json:"current_configuration,omitempty"`
}
