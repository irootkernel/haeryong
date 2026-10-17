package domain

import "encoding/json"

// StagingSummary counts retained upload-owned objects once by immutable hash.
// It is reference accounting, independently of physical filesystem free space.
type StagingSummary struct {
	Bytes             string `json:"staging_bytes"`
	PendingUploads    int64  `json:"pending_upload_count"`
	PendingUploadsURL string `json:"pending_uploads_url"`
}

// ConfigurationPlan is internal persisted exact impact, not a credential.
type ConfigurationPlan struct {
	ID           string           `json:"plan_id"`
	PrincipalID  string           `json:"-"`
	BaseRevision int64            `json:"base_config_revision"`
	ExpiresAt    string           `json:"expires_at"`
	Scope        json.RawMessage  `json:"scope"`
	Changes      json.RawMessage  `json:"normalized_changes"`
	Effective    json.RawMessage  `json:"-"`
	Permissions  map[string]int64 `json:"affected_permissions_revisions"`
	Impact       json.RawMessage  `json:"impact"`
}
