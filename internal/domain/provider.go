package domain

import "encoding/json"

// ProviderAttempt retains possible external effects independently of the job's
// current generation. An obsolete result may reconcile its attempt, never apply.
type ProviderAttempt struct {
	ID                string             `json:"attempt_id"`
	JobID             string             `json:"job_id"`
	Generation        int64              `json:"attempt_generation"`
	ApplyGeneration   int64              `json:"application_generation,omitempty"`
	State             string             `json:"state"`
	Runtime           string             `json:"runtime_generation"`
	MayHaveDispatched bool               `json:"may_have_dispatched"`
	RequestSHA        string             `json:"request_sha256"`
	TargetSetID       string             `json:"target_set_id"`
	RequestID         string             `json:"provider_request_id,omitempty"`
	SessionID         string             `json:"provider_session_id,omitempty"`
	ResultHash        string             `json:"result_sha256,omitempty"`
	Reason            string             `json:"reason,omitempty"`
	Transport         *ProviderTransport `json:"transport,omitempty"`
}

// ProviderInput is a captured exact object. ObservedOrigin is set only after
// actual adapter delivery or exact-byte native telemetry for this attempt.
// ObservedAt is the first local durable acknowledgement time, not capture time.
// A nil timestamp in retained historical evidence remains unknown.
type ProviderInput struct {
	// All inherited version-context owners, separately from the immediate
	// capture's literal SourceVersionID and actual delivery observations.
	SourceVersionIDs []string `json:"source_version_ids,omitempty"`
	SourceVersionID  string   `json:"source_version_id,omitempty"`
	RetentionMode    string   `json:"retention_mode,omitempty"`
	AttemptID        string   `json:"attempt_id"`
	ReadID           string   `json:"read_id"`
	Hash             string   `json:"bytes_sha256"`
	ObservedOrigin   string   `json:"input_origin,omitempty"`
	ObservedAt       *string  `json:"observed_at"`
}

type ProviderScope struct {
	Purpose         string              `json:"purpose,omitempty"`
	Invocation      *ProviderInvocation `json:"invocation,omitempty"`
	ProviderID      string              `json:"provider_id,omitempty"`
	PrincipalID     string              `json:"principal_id"`
	AuthEpoch       string              `json:"auth_epoch"`
	CredentialScope CredentialScope     `json:"credential_scope"`
	Target          json.RawMessage     `json:"target"`
	TargetSetID     string              `json:"target_set_id"`
	RequestSHA      string              `json:"request_sha256"`
	ReadIDs         []string            `json:"read_ids"`
}

func ProviderJob(kind string) bool {
	return kind == "generation" || kind == "question" || kind == "maintenance"
}

type MaintenanceReadBinding struct {
	ReadID      string `json:"read_id"`
	ProjectID   string `json:"project_id"`
	VersionID   string `json:"version_id,omitempty"`
	TargetSetID string `json:"target_set_id"`
}

type MaintenanceExplanation struct {
	Role        string  `json:"role,omitempty"`
	UnitID      string  `json:"unit_id,omitempty"`
	UnitKind    string  `json:"unit_kind,omitempty"`
	ItemID      string  `json:"item_id,omitempty"`
	ProjectID   string  `json:"project_id"`
	DocumentID  string  `json:"document_id"`
	VersionID   string  `json:"version_id"`
	PagePath    *string `json:"page_path,omitempty"`
	RegionID    *string `json:"region_id,omitempty"`
	Title       string  `json:"title,omitempty"`
	ObjectHash  string  `json:"object_hash"`
	Size        int64   `json:"byte_size"`
	TargetSetID string  `json:"target_set_id,omitempty"`
}

type MaintenanceInvocation struct {
	CoverageSetID     string                   `json:"coverage_set_id,omitempty"`
	Mode              string                   `json:"mode,omitempty"`
	ScanJobID         string                   `json:"scan_job_id,omitempty"`
	ItemID            string                   `json:"item_id,omitempty"`
	BatchID           string                   `json:"batch_id,omitempty"`
	BatchItemIDs      []string                 `json:"batch_item_ids,omitempty"`
	CardPayloadDigest string                   `json:"card_payload_digest,omitempty"`
	UnitKind          string                   `json:"unit_kind"`
	ProjectID         string                   `json:"project_id"`
	DocumentID        string                   `json:"document_id"`
	VersionID         string                   `json:"version_id"`
	PagePath          *string                  `json:"page_path,omitempty"`
	RegionID          *string                  `json:"region_id,omitempty"`
	SourceMode        string                   `json:"source_mode"`
	Explanations      []MaintenanceExplanation `json:"explanations,omitempty"`
	ReadBindings      []MaintenanceReadBinding `json:"read_bindings,omitempty"`
}

func MaintenanceReadTarget(scope ProviderScope, defaultTargetSetID, defaultProjectID, readID string) (targetSetID, projectID, versionID string, ok bool) {
	if scope.Invocation != nil && scope.Invocation.Maintenance != nil && len(scope.Invocation.Maintenance.ReadBindings) > 0 {
		seen := map[string]bool{}
		var matched *MaintenanceReadBinding
		for i := range scope.Invocation.Maintenance.ReadBindings {
			b := &scope.Invocation.Maintenance.ReadBindings[i]
			if b.ReadID == "" || b.ProjectID == "" || b.VersionID == "" || b.TargetSetID == "" || seen[b.ReadID] {
				return "", "", "", false
			}
			seen[b.ReadID] = true
			if b.ReadID == readID {
				matched = b
			}
		}
		if matched == nil {
			return "", "", "", false
		}
		return matched.TargetSetID, matched.ProjectID, matched.VersionID, true
	}
	if scope.Invocation != nil && scope.Invocation.Maintenance != nil {
		if defaultTargetSetID == "" || (defaultProjectID == "" && scope.Invocation.Maintenance.ProjectID == "") {
			return "", "", "", false
		}
		proj := defaultProjectID
		if proj == "" {
			proj = scope.Invocation.Maintenance.ProjectID
		}
		return defaultTargetSetID, proj, scope.Invocation.Maintenance.VersionID, true
	}
	return defaultTargetSetID, defaultProjectID, "", true
}

// Summary exposes operational accounting, never native IDs, input or output.
type ProviderAttemptSummary struct {
	Count                 int    `json:"count"`
	Unknown               int    `json:"unknown"`
	LatestState           string `json:"latest_state,omitempty"`
	ReconciliationPending int    `json:"reconciliation_pending"`
	UnknownReason         string `json:"unknown_outcome_reason,omitempty"`
}

// ProviderInvocation is trusted admission state. Public job projections omit it.
// Task04/05 own construction from their authenticated generation/question input.
// ProviderText is a retained exact UTF-8 request object, separate from project
// source captures and their observation ledger.
type ProviderText struct {
	Hash string `json:"bytes_sha256"`
	Size int64  `json:"byte_size"`
}

type ProviderInvocation struct {
	PromptRef   *ProviderText          `json:"prompt_ref,omitempty"`
	Prompt      string                 `json:"prompt"`
	CWD         string                 `json:"cwd"`
	Model       string                 `json:"model,omitempty"`
	Effort      string                 `json:"effort,omitempty"`
	Generation  *GenerationInvocation  `json:"generation,omitempty"`
	Question    *QuestionInvocation    `json:"question,omitempty"`
	Maintenance *MaintenanceInvocation `json:"maintenance,omitempty"`
}

type ProviderDisclosure struct {
	ReadID   string          `json:"read_id"`
	Hash     string          `json:"bytes_sha256"`
	Size     int64           `json:"byte_size"`
	Source   json.RawMessage `json:"source"`
	Location json.RawMessage `json:"location"`
}

type ProviderLookup struct {
	Source        string `json:"observation_source,omitempty"`
	ObservedAt    string `json:"observed_at"`
	BackendState  string `json:"backend_state"`
	LocalClosed   bool   `json:"local_transport_closed"`
	CleanupReason string `json:"cleanup_reason,omitempty"`
}

// ProviderCapabilities contains bounded observed declarations and native runtime counts.
// It never retains configuration, tool catalogs, server names or credentials.
type ProviderCapabilities struct {
	WebSearch     string `json:"configured_web_search"`
	MCPConnected  *int   `json:"observed_connected_mcp_servers"`
	MCPTools      *int   `json:"reported_tools_in_connected_mcp_servers"`
	MCPComplete   bool   `json:"mcp_enumeration_complete"`
	AppsAvailable *bool  `json:"apps_available"`
	ObservedAt    string `json:"observed_at"`
}

type ProviderTransport struct {
	Generation    *GenerationTransport  `json:"generation_output,omitempty"`
	Question      *QuestionTransport    `json:"question_input,omitempty"`
	Capabilities  *ProviderCapabilities `json:"inherited_capabilities,omitempty"`
	BackendSource string                `json:"backend_observation_source,omitempty"`
	DriverOutcome string                `json:"driver_outcome,omitempty"`
	LastLookup    *ProviderLookup       `json:"last_lookup,omitempty"`

	// Outcome retains an existing historical label; new drivers use DriverOutcome.
	Outcome              string               `json:"normalized_outcome,omitempty"`
	CleanupState         string               `json:"cleanup_state,omitempty"`
	CleanupReason        string               `json:"cleanup_reason,omitempty"`
	Disclosure           []ProviderDisclosure `json:"disclosure"`
	OrdinaryUsageAllowed *bool                `json:"ordinary_usage_allowed"`

	Provider            string `json:"provider"`
	Version             string `json:"version"`
	BinarySHA           string `json:"binary_sha256"`
	PreparedAt          string `json:"prepared_at"`
	SetupThread         string `json:"setup_thread_id,omitempty"`
	AuthMode            string `json:"authentication_mode"`
	Model               string `json:"model,omitempty"`
	Effort              string `json:"effort,omitempty"`
	CWD                 string `json:"cwd"`
	Approval            string `json:"approval_policy"`
	Sandbox             string `json:"sandbox"`
	InstructionCount    int    `json:"instruction_count"`
	DisabledPluginCount int    `json:"disabled_plugin_count"`
	FrameSHA            string `json:"frame_sha256,omitempty"`
	AcceptedAt          string `json:"accepted_at,omitempty"`
	FinishedAt          string `json:"finished_at,omitempty"`
	BackendState        string `json:"backend_state,omitempty"`
	LocalClosed         bool   `json:"local_transport_closed"`
	AllowanceKnown      bool   `json:"allowance_known"`
}
