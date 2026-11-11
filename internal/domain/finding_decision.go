package domain

import "encoding/json"

// FindingDecision is private canonical owner memory, distinct from producer evidence.
type FindingDecision struct {
	Key        string                `json:"key"`
	Material   string                `json:"material"`
	Materials  []string              `json:"materials,omitempty"`
	Kind       string                `json:"kind"`
	Action     string                `json:"action"`
	EffectID   string                `json:"effect_id,omitempty"`
	FindingID  string                `json:"finding_id"`
	VersionIDs []string              `json:"version_ids"`
	Actor      string                `json:"actor"`
	Revision   int64                 `json:"revision"`
	At         string                `json:"at"`
	Links      []FindingDecisionLink `json:"links,omitempty"`
}
type FindingDecisionLink struct {
	FindingID  string          `json:"finding_id"`
	Revision   int64           `json:"revision"`
	Status     string          `json:"status"`
	AppliedRef json.RawMessage `json:"applied_ref"`
}
type FindingDecisionBasis struct {
	FindingID      string `json:"finding_id"`
	Key            string `json:"key"`
	Material       string `json:"material"`
	OwnerRevision  int64  `json:"owner_revision"`
	TargetRevision int64  `json:"target_revision"`
	Reassessment   bool   `json:"reassessment"`
}
type FindingEffectLink struct {
	FindingID       string
	EffectID        string
	EffectRevision  int64
	FindingRevision int64
	Active          bool
	ValidHistory    bool
}
type OwnerDecisionView struct {
	Action   string `json:"action"`
	Revision int64  `json:"revision"`
}
type FindingActionResult struct {
	OperationID string            `json:"operation_id"`
	State       string            `json:"state"`
	Finding     *Finding          `json:"finding"`
	Effect      *ManagementEffect `json:"effect"`
	JobID       *string           `json:"job_id"`
}
type FindingActionError struct {
	Code, Reason string
	Status       int
}

func (e *FindingActionError) Error() string { return e.Code }
