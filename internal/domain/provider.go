package domain

import "encoding/json"

// ProviderAttempt retains possible external effects independently of the job's
// current generation. An obsolete result may reconcile its attempt, never apply.
type ProviderAttempt struct {
	ID                string `json:"attempt_id"`
	JobID             string `json:"job_id"`
	Generation        int64  `json:"attempt_generation"`
	ApplyGeneration   int64  `json:"application_generation,omitempty"`
	State             string `json:"state"`
	Runtime           string `json:"runtime_generation"`
	MayHaveDispatched bool   `json:"may_have_dispatched"`
	RequestSHA        string `json:"request_sha256"`
	TargetSetID       string `json:"target_set_id"`
	RequestID         string `json:"provider_request_id,omitempty"`
	SessionID         string `json:"provider_session_id,omitempty"`
	ResultHash        string `json:"result_sha256,omitempty"`
	Reason            string `json:"reason,omitempty"`
}

// ProviderInput is a captured exact object. ObservedOrigin is set only after
// actual adapter delivery or exact-byte native telemetry for this attempt.
// ObservedAt is the first local durable acknowledgement time, not capture time.
// A nil timestamp in retained historical evidence remains unknown.
type ProviderInput struct {
	AttemptID      string  `json:"attempt_id"`
	ReadID         string  `json:"read_id"`
	Hash           string  `json:"bytes_sha256"`
	ObservedOrigin string  `json:"input_origin,omitempty"`
	ObservedAt     *string `json:"observed_at"`
}

type ProviderScope struct {
	PrincipalID     string          `json:"principal_id"`
	AuthEpoch       string          `json:"auth_epoch"`
	CredentialScope CredentialScope `json:"credential_scope"`
	Target          json.RawMessage `json:"target"`
	TargetSetID     string          `json:"target_set_id"`
	RequestSHA      string          `json:"request_sha256"`
	ReadIDs         []string        `json:"read_ids"`
}

func ProviderJob(kind string) bool { return kind == "generation" || kind == "question" }

// Summary exposes operational accounting, never native IDs, input or output.
type ProviderAttemptSummary struct {
	Count                 int    `json:"count"`
	Unknown               int    `json:"unknown"`
	LatestState           string `json:"latest_state,omitempty"`
	ReconciliationPending int    `json:"reconciliation_pending"`
	UnknownReason         string `json:"unknown_outcome_reason,omitempty"`
}
