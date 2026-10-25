package domain

// Lifecycle metadata is separate from ordinary document visibility.
type LifecycleDocument struct {
	Document
	PreviousLifecycle *string `json:"previous_lifecycle"`
	TrashedAt         *string `json:"trashed_at"`
	TrashGeneration   int64   `json:"-"`
}
type LifecycleItem struct {
	Kind             string  `json:"kind"`
	ID               string  `json:"id"`
	VersionID        *string `json:"version_id"`
	PagePath         *string `json:"page_path"`
	RegionID         *string `json:"region_id"`
	Revision         int64   `json:"revision"`
	SourceDocumentID *string `json:"source_document_id"`
	Resolution       *string `json:"resolution"`
}
type LifecycleResult struct {
	OperationID       string  `json:"operation_id"`
	PlanID            string  `json:"plan_id"`
	DocumentID        string  `json:"document_id"`
	Lifecycle         string  `json:"lifecycle"`
	PreviousLifecycle *string `json:"previous_lifecycle"`
	TrashedAt         *string `json:"trashed_at"`
	Revision          int64   `json:"revision"`
	Changed           bool    `json:"changed"`
	JobID             string  `json:"job_id,omitempty"`
	JobURL            string  `json:"job_url,omitempty"`
}
type LifecyclePlan struct {
	ID                  string           `json:"plan_id"`
	OwnerID             string           `json:"-"`
	DocumentID          string           `json:"document_id"`
	Operation           string           `json:"operation"`
	State               string           `json:"state"`
	From                string           `json:"from_lifecycle"`
	To                  string           `json:"to_lifecycle"`
	ExpectedRevision    int64            `json:"expected_revision"`
	ConfigRevision      int64            `json:"config_revision"`
	ExpiresAt           string           `json:"expires_at"`
	Impact              map[string]int   `json:"impact"`
	Result              *LifecycleResult `json:"result"`
	PermissionsRevision int64            `json:"-"`
	Digest              string           `json:"-"`
	Backup              *PurgeBackup     `json:"backup,omitempty"`
}
type LifecyclePage struct {
	LifecyclePlan
	Items      []LifecycleItem `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}
type TrashItem struct {
	DocumentID        string  `json:"document_id"`
	ProjectID         string  `json:"project_id"`
	Title             string  `json:"title"`
	LatestVersionID   *string `json:"latest_version_id"`
	Revision          int64   `json:"revision"`
	Lifecycle         string  `json:"lifecycle"`
	PreviousLifecycle *string `json:"previous_lifecycle"`
	TrashedAt         *string `json:"trashed_at"`
	PurgeEligibleAt   *string `json:"purge_eligible_at"`
}

// PurgeIntent is durable job-bound authority; it never stores private bodies.
type PurgeIntent struct {
	ReceiptsMinimized bool     `json:"receipts_minimized"`
	JobID             string   `json:"job_id"`
	PlanID            string   `json:"plan_id"`
	DocumentID        string   `json:"document_id"`
	OwnerID           string   `json:"owner_id"`
	Runtime           string   `json:"runtime_generation"`
	LibraryID         string   `json:"library_id"`
	Revision          int64    `json:"revision"`
	Phase             int      `json:"phase"`
	Reason            string   `json:"reason"`
	Retry             int      `json:"retry"`
	NextRetry         string   `json:"next_retry_at"`
	DeletedAt         string   `json:"deleted_at"`
	PendingSet        string   `json:"pending_set,omitempty"`
	RetainedSets      []string `json:"retained_sets,omitempty"`
	SetAfterPath      string   `json:"set_after_path,omitempty"`
	MetadataRemoved   bool     `json:"metadata_removed"`
	ReceiptPrincipal  string   `json:"receipt_principal,omitempty"`
	ReceiptID         string   `json:"receipt_id,omitempty"`
}
type PurgeBackup struct {
	SetID          *string        `json:"set_id"`
	LastAttempt    *BackupAttempt `json:"last_attempt"`
	LastTrial      TrialRestore   `json:"last_trial_restore"`
	State          string         `json:"state"`
	LastCompleteAt *string        `json:"last_complete_at"`
	Reason         *string        `json:"reason"`
}

// LifecycleTransition carries the shared owner-write conflict across application
// authority and query layers without exposing a private resource.
type LifecycleTransition struct{ Lifecycle string }

func (e *LifecycleTransition) Error() string { return "lifecycle blocks write: " + e.Lifecycle }
