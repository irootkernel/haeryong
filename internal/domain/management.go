package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// ManagementAnchor never retargets an immutable version to its document's latest.
type ManagementAnchor struct {
	DocumentID string  `json:"document_id"`
	VersionID  string  `json:"version_id"`
	PagePath   *string `json:"page_path"`
	RegionID   *string `json:"region_id"`
}

// Origin records authorship permanently; DecisionActor records authorization.
type ManagementEffect struct {
	Availability           string            `json:"availability,omitempty"`
	ID                     string            `json:"overlay_id"`
	Kind                   string            `json:"kind"`
	SemanticKey            string            `json:"semantic_key"`
	FindingID              string            `json:"finding_id"`
	JobID                  string            `json:"job_id"`
	From                   ManagementAnchor  `json:"from"`
	Target                 *ManagementAnchor `json:"target"`
	RelationType           string            `json:"relation_type,omitempty"`
	MechanismKey           string            `json:"mechanism_key,omitempty"`
	TopicID                string            `json:"topic_id,omitempty"`
	Topic                  string            `json:"topic,omitempty"`
	Origin                 string            `json:"origin"`
	EvidenceBasis          string            `json:"evidence_basis"`
	Evidence               json.RawMessage   `json:"evidence"`
	EvidenceFingerprint    string            `json:"evidence_fingerprint"`
	Rationale              string            `json:"rationale"`
	Uncertainty            string            `json:"uncertainty"`
	TargetSetID            *string           `json:"target_set_id"`
	CapturedConfigRevision int64             `json:"captured_config_revision"`
	AppliedConfigRevision  int64             `json:"applied_config_revision"`
	PermissionsRevision    int64             `json:"permissions_revision"`
	Decision               string            `json:"decision"`
	DecisionActor          string            `json:"decision_actor"`
	Pinned                 bool              `json:"pinned"`
	Revision               int64             `json:"revision"`
	CreatedAt              string            `json:"created_at"`
	UpdatedAt              string            `json:"updated_at"`
	ETag                   string            `json:"etag"`
}

// Unavailable retains only the authorized focal context and an opaque row identity.
// The same representation covers hidden, Trash and purged dependencies.
func (m ManagementEffect) Unavailable() ManagementEffect {
	return ManagementEffect{Availability: "unavailable", ID: m.ID, Kind: m.Kind,
		From: m.From, Origin: m.Origin, Decision: m.Decision, Revision: m.Revision}
}

// MarshalJSON keeps the unavailable DTO closed even if a caller retains a full
// in-memory effect. No target/evidence identity or sensitive payload can escape.
func (m ManagementEffect) MarshalJSON() ([]byte, error) {
	if m.Availability == "unavailable" {
		return json.Marshal(struct {
			Availability string           `json:"availability"`
			ID           string           `json:"overlay_id"`
			Kind         string           `json:"kind"`
			From         ManagementAnchor `json:"from"`
			Origin       string           `json:"origin"`
			Decision     string           `json:"decision"`
			Revision     int64            `json:"revision"`
			ETag         string           `json:"etag"`
		}{m.Availability, m.ID, m.Kind, m.From, m.Origin, m.Decision, m.Revision, m.Validator()})
	}
	type effect ManagementEffect
	return json.Marshal(effect(m))
}

// A legacy read never upgrades missing/noncanonical historical audit metadata.
func TopicEvidenceBasis(basis string) string {
	if basis == "explanation_text" || basis == "source_verified" {
		return basis
	}
	return "legacy_unknown"
}

func ManagementDigest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (m ManagementEffect) Key() string {
	if m.Kind == "topic_assignment" {
		return ManagementDigest([]any{m.Kind, m.TopicID, m.From})
	}
	return ManagementDigest([]any{m.Kind, m.From, m.Target, m.RelationType, m.MechanismKey})
}
func (m ManagementEffect) Validator() string {
	if m.Availability == "unavailable" {
		return fmt.Sprintf("\"overlay-%s-r%d-unavailable\"", m.ID, m.Revision)
	}
	return fmt.Sprintf("\"overlay-%s-r%d\"", m.ID, m.Revision)
}

type ManagementFilter struct {
	Kind       string  `json:"kind"`
	DocumentID string  `json:"document_id,omitempty"`
	VersionID  string  `json:"version_id,omitempty"`
	PagePath   *string `json:"page_path,omitempty"`
	RegionID   *string `json:"region_id,omitempty"`
}
type ManagementPage struct {
	Items      []ManagementEffect `json:"items"`
	NextCursor *string            `json:"next_cursor"`
}
