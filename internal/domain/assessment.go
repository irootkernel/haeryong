package domain

import "encoding/json"

const MaintenanceModeFreshness = "freshness_assessment"

// Assessment is an immutable, published semantic contribution. Candidates stay
// in findings; neither capture nor provider delivery establishes claim support.
type Assessment struct {
	ID                  string          `json:"assessment_id"`
	VersionID           string          `json:"version_id"`
	TargetSetID         string          `json:"target_set_id"`
	CoverageSetID       string          `json:"coverage_set_id"`
	ScopeJSON           json.RawMessage `json:"scope"`
	State               string          `json:"state"`
	EvidenceJSON        json.RawMessage `json:"evidence"`
	ConfigRevision      int64           `json:"config_revision"`
	PermissionsRevision int64           `json:"permissions_revision"`
	Origin              string          `json:"origin"`
	AssessedAt          string          `json:"assessed_at"`
}

type AssessmentUnit struct {
	AssessedAt    string `json:"-"`
	CoverageSetID string `json:"coverage_set_id"`
	AssessmentID  string `json:"assessment_id"`
	TargetSetID   string `json:"target_set_id"`
	Kind          string `json:"kind"`
	PagePath      string `json:"page_path"`
	RegionID      string `json:"region_id"`
	Outcome       string `json:"outcome"`
	Discrepancy   string `json:"discrepancy"`
	Reason        string `json:"reason"`
}

// FreshnessOutput is the closed provider result for one fixed required unit.
// Full coverage is validated by the service, never inferred from a state label.
type FreshnessOutput struct {
	Kind        string             `json:"kind"`
	PagePath    *string            `json:"page_path"`
	RegionID    *string            `json:"region_id"`
	Outcome     string             `json:"outcome"`
	Discrepancy string             `json:"discrepancy"`
	Reason      string             `json:"reason"`
	Rationale   string             `json:"rationale"`
	Citations   []QuestionCitation `json:"citations"`
}

type FreshnessResult struct {
	Units []FreshnessOutput `json:"units"`
}

type PreparedFreshness struct {
	InputDigest string          `json:"input_digest"`
	Output      FreshnessResult `json:"output"`
}

// AssessmentState describes only the recorded semantic contribution. Current
// availability and uncleared target-bound triggers are composed by the query.
func AssessmentState(units []AssessmentUnit) string {
	complete, blocked, discrepancy := len(units) > 0, false, false
	for _, u := range units {
		complete = complete && u.Outcome == "assessed"
		blocked = blocked || u.Outcome == "blocked"
		discrepancy = discrepancy || u.Discrepancy != ""
	}
	if discrepancy {
		return "outdated_against_target"
	}
	if blocked {
		return "unable_to_verify"
	}
	if complete {
		return "aligned_within_scope"
	}
	return "not_assessed"
}
