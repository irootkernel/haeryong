package domain

import (
	"encoding/hex"
	"encoding/json"
)

// SourceCapture is a local exact-byte capture, not a provider input observation.
// A target is published only after every selected read and authority check commits.
type SourceCapture struct {
	ID                  string          `json:"capture_id"`
	PrincipalID         string          `json:"-"`
	ProjectID           string          `json:"project_id"`
	TargetSetID         string          `json:"target_set_id"`
	State               string          `json:"state"`
	Selection           json.RawMessage `json:"requested_selection"`
	ConfigRevision      int64           `json:"config_revision"`
	PermissionsRevision int64           `json:"permissions_revision"`
	CapturedAt          string          `json:"captured_at"`
	OperationID         string          `json:"operation_id"`
	Reason              string          `json:"reason,omitempty"`
}

// SourceCaptureRead keeps historical identity after authorized temporary bytes
// are released/omitted. The hash is not an implicit object-row lifetime owner.
type SourceCaptureRead struct {
	CaptureAvailability string          `json:"capture_availability,omitempty"`
	BytesOrigin         string          `json:"bytes_origin,omitempty"`
	ReadState           string          `json:"read_state,omitempty"`
	ID                  string          `json:"read_id"`
	CaptureID           string          `json:"capture_id"`
	SourceID            string          `json:"source_id"`
	SourceJSON          json.RawMessage `json:"source"`
	LocationJSON        json.RawMessage `json:"location"`
	Hash                string          `json:"bytes_sha256"`
	Size                int64           `json:"byte_size"`
	RetentionMode       string          `json:"retention_mode"`
	Availability        string          `json:"availability"`
	Reason              string          `json:"reason,omitempty"`
}

// SourceCaptureDelta is a metadata-only exact-byte comparison row.
type SourceCaptureDelta struct {
	SourceID string             `json:"source_id"`
	Location json.RawMessage    `json:"location"`
	Before   *SourceCaptureRead `json:"before"`
	After    *SourceCaptureRead `json:"after"`
}

// CaptureFacts interprets only the supported copy marker with an intact exact
// source/location/byte identity. It never asserts current byte availability.
func (r SourceCaptureRead) CaptureFacts() (string, string) {
	if r.RetentionMode != "reference_only" || r.Availability != "unavailable" || r.Reason != "reference_only_uncopied" {
		return r.Availability, r.Reason
	}
	hash, err := hex.DecodeString(r.Hash)
	if err != nil || len(hash) != 32 {
		return r.Availability, r.Reason
	}
	var source struct {
		Kind     string `json:"kind"`
		Snapshot string `json:"snapshot_sha256"`
		Repo     string `json:"repo_id"`
		Revision string `json:"exact_revision"`
	}
	var location struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(r.SourceJSON, &source) != nil || json.Unmarshal(r.LocationJSON, &location) != nil || location.Path == "" {
		return r.Availability, r.Reason
	}
	supported := source.Kind == "snapshot" && source.Snapshot == r.Hash
	if source.Kind == "git" && source.Repo != "" {
		revision, err := hex.DecodeString(source.Revision)
		supported = err == nil && (len(revision) == 20 || len(revision) == 32)
	}
	if supported {
		return "available", ""
	}
	return r.Availability, r.Reason
}
