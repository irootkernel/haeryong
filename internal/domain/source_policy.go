package domain

// ManagedSourceMapping is a remembered host selection. RootPath is never part
// of a source-capture projection returned to a scoped/remote reader.
type ManagedSourceMapping struct {
	SourceID      string   `json:"source_id"`
	Kind          string   `json:"kind"`
	RootPath      string   `json:"root_path"`
	RepoID        string   `json:"repo_id,omitempty"`
	RetentionMode string   `json:"retention_mode"`
	ProviderIDs   []string `json:"provider_ids"`
}
