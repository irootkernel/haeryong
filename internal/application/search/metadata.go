package search

import (
	"encoding/json"
	"fmt"
	"strings"

	"haeryong.local/haeryong/internal/domain"
)

// InventoryUnit retains every entry path. Page selection comes from immutable
// version_pages, not a new extension allowlist; links are metadata, never followed.
func InventoryUnit(version string, e domain.Entry, isPage bool) domain.SearchUnit {
	kind, body := "attachment", "unsupported"
	if isPage && e.Kind == "file" {
		kind, body = "page", "pending"
	}
	return domain.SearchUnit{VersionID: version, Kind: kind, PagePath: e.Path, Title: e.Path, InputHash: e.Hash, State: "pending", BodyState: body, AnchorState: "not_applicable"}
}

// MetadataText selects only authored logical identity/labels. It never accepts a
// configured filesystem root or imports source bytes as searchable metadata.
func MetadataText(raw []byte, source bool) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	keys := []string{"label", "id"}
	if source {
		keys = []string{"id", "kind", "repo_id", "exact_revision", "snapshot_sha256", "release_label"}
	}
	var out []string
	for _, key := range keys {
		if value, ok := m[key]; ok {
			var s string
			if err := json.Unmarshal(value, &s); err != nil {
				return "", fmt.Errorf("metadata %s: %w", key, err)
			}
			out = append(out, s)
		}
	}
	return strings.Join(out, " "), nil
}
