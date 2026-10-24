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
