package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrFeatureNotReady = errors.New("feature not ready")

type ScanAutoApplyPolicy struct {
	BacklinkIndexRepair string `json:"backlink_index_repair"`
	RelatedExplanation  string `json:"related_explanation"`
	TopicAssignment     string `json:"topic_assignment"`
	FreshnessAssessment string `json:"freshness_assessment"`
}

type ScanPolicySnapshot struct {
	Scope                     string              `json:"scope"`
	ProjectIDs                []string            `json:"project_ids"`
	Depth                     string              `json:"depth"`
	MissedRuns                string              `json:"missed_runs,omitempty"`
	MaxConcurrentAIJobs       int                 `json:"max_concurrent_ai_jobs"`
	MaxProviderAttemptsPerRun int                 `json:"max_provider_attempts_per_run"`
	MaxRunSeconds             int                 `json:"max_run_seconds"`
	AutoApply                 ScanAutoApplyPolicy `json:"auto_apply"`
	ContentGeneration         string              `json:"content_generation"`
}

type ScanSourceSelector struct {
	ProjectID   string   `json:"project_id,omitempty"`
	SourceID    string   `json:"source_id,omitempty"`
	SourceIDs   []string `json:"source_ids,omitempty"`
	Mode        string   `json:"mode,omitempty"`
	Revision    string   `json:"revision,omitempty"`
	Branch      string   `json:"branch,omitempty"`
	SnapshotID  string   `json:"snapshot_id,omitempty"`
	TargetSetID string   `json:"target_set_id,omitempty"`
}

type ScanRequestedScope struct {
	Scope       string               `json:"scope"`
	ProjectIDs  []string             `json:"project_ids"`
	DocumentID  string               `json:"document_id,omitempty"`
	DocumentIDs []string             `json:"document_ids,omitempty"`
	VersionID   string               `json:"version_id,omitempty"`
	PathPrefix  string               `json:"path_prefix,omitempty"`
	PagePath    string               `json:"page_path,omitempty"`
	RegionID    string               `json:"region_id,omitempty"`
	Depth       string               `json:"depth"`
	SourceMode  string               `json:"source_mode,omitempty"`
	TargetSetID string               `json:"target_set_id,omitempty"`
	Sources     []ScanSourceSelector `json:"sources,omitempty"`
}

type ScanSourceTarget struct {
	ProjectID          string             `json:"project_id"`
	SourceID           string             `json:"source_id"`
	Kind               string             `json:"kind"`
	Mode               string             `json:"mode"`
	SelectionSource    string             `json:"selection_source"`
	ResolvedRevision   string             `json:"resolved_revision,omitempty"`
	SnapshotSHA256     string             `json:"snapshot_sha256,omitempty"`
	AssetPath          string             `json:"asset_path,omitempty"`
	BytesOrigin        string             `json:"bytes_origin,omitempty"`
	Availability       string             `json:"availability"`
	Reason             string             `json:"reason,omitempty"`
	ReassessmentNeeded bool               `json:"reassessment_required,omitempty"`
	CapturedAt         string             `json:"captured_at"`
	RequestedSelection ScanSourceSelector `json:"requested_selection"`
}

type ScanDenominator struct {
	Projects          int `json:"projects"`
	Documents         int `json:"documents"`
	Versions          int `json:"versions"`
	Pages             int `json:"pages"`
	Regions           int `json:"regions"`
	Entries           int `json:"entries"`
	Sources           int `json:"sources"`
	Indexes           int `json:"indexes"`
	ExcludedDocuments int `json:"excluded_documents"`
	TotalUnits        int `json:"total_units"`
}

type ScanStructuralCounts struct {
	Checked              int `json:"checked"`
	CheckedIndexItems    int `json:"checked_index_items"`
	Repaired             int `json:"repaired"`
	Failed               int `json:"failed"`
	AnomaliesReported    int `json:"anomalies_reported"`
	Excluded             int `json:"excluded"`
	Blocked              int `json:"blocked"`
	Skipped              int `json:"skipped"`
	RegionlessVersions   int `json:"regionless_versions"`
	MetadataLessVersions int `json:"metadata_less_versions"`
}

type ScanSemanticCounts struct {
	Eligible   int    `json:"eligible"`
	Assessed   int    `json:"assessed"`
	Skipped    int    `json:"skipped"`
	Unresolved int    `json:"unresolved"`
	Complete   bool   `json:"complete"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
}

type ScanOverlayCounts struct {
	Applied  int `json:"applied"`
	Proposed int `json:"proposed"`
	Rejected int `json:"rejected"`
}

type ScanFindingSummary struct {
	Total    int `json:"total"`
	Proposed int `json:"proposed"`
	Applied  int `json:"applied"`
	Rejected int `json:"rejected"`
	Stale    int `json:"stale"`
	Blocked  int `json:"blocked"`
	Undone   int `json:"undone"`
}

type ScanReport struct {
	JobID                       string               `json:"job_id,omitempty"`
	Stage                       string               `json:"stage"`
	EnumerationComplete         bool                 `json:"enumeration_complete"`
	BudgetExhausted             bool                 `json:"budget_exhausted,omitempty"`
	TargetSetID                 string               `json:"target_set_id,omitempty"`
	ConfigRevision              int64                `json:"config_revision,omitempty"`
	PermissionsRevisions        map[string]int64     `json:"permissions_revisions,omitempty"`
	RequestedScope              *ScanRequestedScope  `json:"requested_scope,omitempty"`
	EffectivePolicy             *ScanPolicySnapshot  `json:"effective_policy,omitempty"`
	SourceTargets               []ScanSourceTarget   `json:"source_targets,omitempty"`
	IncompleteReferenceRepairs  []string             `json:"incomplete_reference_repairs,omitempty"`
	RescanRecommended           bool                 `json:"rescan_recommended,omitempty"`
	Captured                    ScanDenominator      `json:"captured"`
	Structural                  ScanStructuralCounts `json:"structural"`
	Semantic                    ScanSemanticCounts   `json:"semantic"`
	InspectedCandidateRelations int                  `json:"inspected_candidate_relations"`
	Overlays                    ScanOverlayCounts    `json:"overlays"`
	Drafts                      int                  `json:"drafts"`
	OutdatedUnits               int                  `json:"outdated_units"`
	AffectedReviewedVersions    int                  `json:"affected_reviewed_versions"`
	ProviderAttempts            int                  `json:"provider_attempts"`
	Findings                    ScanFindingSummary   `json:"findings"`
}

type ScanCapturedVersion struct {
	JobID             string             `json:"job_id"`
	VersionOrdinal    int                `json:"version_ordinal"`
	ProjectID         string             `json:"project_id"`
	DocumentID        string             `json:"document_id"`
	VersionID         string             `json:"version_id"`
	ContentSetID      string             `json:"content_set_id"`
	ManifestSHA256    string             `json:"manifest_sha256"`
	MetadataSHA256    string             `json:"metadata_sha256,omitempty"`
	SourceBasis       string             `json:"source_basis,omitempty"`
	SourceIntent      *string            `json:"source_intent,omitempty"`
	SelectedMode      string             `json:"selected_mode"`
	SelectionSource   string             `json:"selection_source"`
	TargetSetID       string             `json:"target_set_id,omitempty"`
	SelectionJSON     json.RawMessage    `json:"requested_selection"`
	TargetsJSON       json.RawMessage    `json:"targets"`
	Targets           []ScanSourceTarget `json:"-"`
	ExclusionReason   string             `json:"exclusion_reason,omitempty"`
	Regionless        bool               `json:"-"`
	MetadataLess      bool               `json:"-"`
	PageCount         int                `json:"page_count"`
	RegionCount       int                `json:"region_count"`
	EntryCount        int                `json:"entry_count"`
	SourceCount       int                `json:"source_count"`
	UnitCount         int                `json:"unit_count"`
	EnumState         string             `json:"enum_state"`
	EnumAfterPath     string             `json:"enum_after_path,omitempty"`
	EnumAfterRegion   string             `json:"enum_after_region,omitempty"`
	EntryAfterPath    string             `json:"entry_after_path,omitempty"`
	SourceAfterIdx    int                `json:"source_after_idx"`
	MaterializedUnits int                `json:"materialized_units"`
	EntriesChecked    int                `json:"entries_checked"`
	CapturedAt        string             `json:"captured_at"`
}

type ScanRun struct {
	JobID                string             `json:"job_id"`
	PrincipalID          string             `json:"principal_id"`
	AuthKind             string             `json:"auth_kind"`
	CapturedScope        CredentialScope    `json:"-"`
	ScopeHash            string             `json:"scope_hash"`
	TargetSetID          string             `json:"target_set_id,omitempty"`
	RequestedScopeJSON   json.RawMessage    `json:"requested_scope"`
	RequestedScope       ScanRequestedScope `json:"-"`
	EffectivePolicyJSON  json.RawMessage    `json:"effective_policy"`
	EffectivePolicy      ScanPolicySnapshot `json:"-"`
	ConfigRevision       int64              `json:"config_revision"`
	PermissionsRevisions map[string]int64   `json:"permissions_revisions"`
	Stage                string             `json:"stage"`
	EnumVersionOrdinal   int                `json:"enum_version_ordinal"`
	EnumAfterPath        string             `json:"enum_after_path,omitempty"`
	EnumAfterRegion      string             `json:"enum_after_region,omitempty"`
	EnumerationComplete  bool               `json:"enumeration_complete"`
	NextItemOrdinal      int                `json:"next_item_ordinal"`
	DenominatorJSON      json.RawMessage    `json:"denominator"`
	Denominator          ScanDenominator    `json:"-"`
	ReportJSON           json.RawMessage    `json:"report"`
	Report               ScanReport         `json:"-"`
	CapturedAt           string             `json:"captured_at"`
	StartedAt            *string            `json:"started_at,omitempty"`
	ElapsedMs            int64              `json:"elapsed_ms"`
	UpdatedAt            string             `json:"updated_at"`
}

type JobItem struct {
	JobID             string          `json:"job_id"`
	ItemID            string          `json:"item_id"`
	Ordinal           int             `json:"ordinal"`
	UnitKind          string          `json:"unit_kind"`
	ProjectID         string          `json:"project_id"`
	DocumentID        string          `json:"document_id,omitempty"`
	VersionID         string          `json:"version_id,omitempty"`
	PagePath          string          `json:"page_path,omitempty"`
	RegionID          string          `json:"region_id,omitempty"`
	SourceID          string          `json:"source_id,omitempty"`
	TargetSetID       string          `json:"target_set_id,omitempty"`
	Stage             string          `json:"stage"`
	State             string          `json:"state"`
	AttemptGeneration int64           `json:"attempt_generation"`
	Reason            string          `json:"reason,omitempty"`
	Detail            json.RawMessage `json:"detail"`
	FindingID         string          `json:"finding_id,omitempty"`
	RepairJobID       string          `json:"repair_job_id,omitempty"`
	UpdatedAt         string          `json:"updated_at"`
}

type Finding struct {
	ID                     string          `json:"finding_id"`
	JobID                  string          `json:"job_id"`
	Kind                   string          `json:"kind"`
	Origin                 string          `json:"origin"`
	ProjectID              string          `json:"project_id"`
	DocumentID             *string         `json:"document_id"`
	VersionID              *string         `json:"version_id"`
	PagePath               *string         `json:"page_path"`
	RegionID               *string         `json:"region_id"`
	TargetSetID            *string         `json:"target_set_id"`
	Evidence               json.RawMessage `json:"evidence"`
	EvidenceFingerprint    string          `json:"evidence_fingerprint"`
	Rationale              string          `json:"rationale"`
	Uncertainty            string          `json:"uncertainty"`
	CapturedConfigRevision int64           `json:"captured_config_revision"`
	PermissionsRevision    int64           `json:"permissions_revision"`
	Proposal               json.RawMessage `json:"proposal"`
	Status                 string          `json:"status"`
	ApprovalRequired       bool            `json:"approval_required"`
	Reason                 string          `json:"reason,omitempty"`
	AppliedRef             json.RawMessage `json:"applied_ref"`
	DecidedBy              *string         `json:"decided_by,omitempty"`
	DecidedAt              *string         `json:"decided_at,omitempty"`
	Revision               int64           `json:"revision"`
	ETag                   string          `json:"etag"`
	CreatedAt              string          `json:"created_at"`
	UpdatedAt              string          `json:"updated_at"`
}

func FindingETag(f Finding) string {
	return fmt.Sprintf("\"finding-%s-r%d\"", f.ID, f.Revision)
}

func FindingPageETag(jobID string, jobRev int64, kind, status string, items []Finding, nextCursor *string) string {
	raw, _ := json.Marshal([]any{jobID, jobRev, kind, status, items, nextCursor})
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("\"sha256-%s\"", hex.EncodeToString(sum[:]))
}

type FindingFilter struct {
	JobID  string
	Kind   string
	Status string
	Limit  int
	Cursor string
}

type FindingPage struct {
	Items      []Finding `json:"items"`
	NextCursor *string   `json:"next_cursor"`
	ETag       string    `json:"-"`
}

type ScanRequest struct {
	Scope                   string               `json:"scope,omitempty"`
	ProjectID               string               `json:"project_id,omitempty"`
	ProjectIDs              []string             `json:"project_ids,omitempty"`
	DocumentID              string               `json:"document_id,omitempty"`
	DocumentIDs             []string             `json:"document_ids,omitempty"`
	VersionID               string               `json:"version_id,omitempty"`
	PathPrefix              string               `json:"path_prefix,omitempty"`
	PagePath                string               `json:"page_path,omitempty"`
	RegionID                string               `json:"region_id,omitempty"`
	Depth                   string               `json:"depth,omitempty"`
	SourceMode              string               `json:"source_mode,omitempty"`
	TargetSetID             string               `json:"target_set_id,omitempty"`
	Sources                 []ScanSourceSelector `json:"sources,omitempty"`
	FindingID               string               `json:"finding_id,omitempty"`
	ExpectedFindingRevision *int64               `json:"expected_finding_revision,omitempty"`
}

func intersectScopeStrings(a, b []string) []string {
	allowed, seen := make(map[string]bool, len(b)), make(map[string]bool, len(a))
	for _, v := range b {
		allowed[v] = true
	}
	out := []string{}
	for _, v := range a {
		if allowed[v] && !seen[v] {
			out = append(out, v)
			seen[v] = true
		}
	}
	return out
}

func RestrictCredentialScope(captured, current CredentialScope) CredentialScope {
	return CredentialScope{
		Kind:       "workflow",
		Actions:    intersectScopeStrings(captured.Actions, current.Actions),
		ProjectIDs: intersectScopeStrings(captured.ProjectIDs, current.ProjectIDs),
		DraftIDs:   intersectScopeStrings(captured.DraftIDs, current.DraftIDs),
		VersionIDs: intersectScopeStrings(captured.VersionIDs, current.VersionIDs),
	}
}
