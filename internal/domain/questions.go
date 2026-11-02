package domain

import "encoding/json"

// Question keeps the owner's accepted scope independently of the Reader and
// mutable project defaults. The existing job owns execution and recovery.
type Question struct {
	ID, OwnerID, ProjectID, DocumentID, VersionID string
	JobID, OperationID, RequestHash, CreatedAt    string
	PagePath, RegionID                            *string
	PageQuery, PageFragment                       string
	Text                                          ProviderText
	Context                                       *QuestionContext
	Explanation                                   GenerationReference
	SourceMode, CaptureOperationID                string
	SourceSelection                               json.RawMessage
	SourceReadIDs                                 []string
	AdmissionReady                                bool
	Limitations                                   []QuestionLimitation
	PreparedAnswer                                *QuestionAnswer
}

// QuestionContext is an adopted exact display snapshot, not project-source
// evidence. Saved access does not require its old render channel to survive.
type QuestionContext struct {
	ID               string  `json:"context_ref"`
	Hash             string  `json:"bytes_sha256"`
	Size             int64   `json:"byte_size"`
	MediaType        string  `json:"media_type"`
	RenderInstanceID *string `json:"render_instance_id"`
}

// QuestionInvocation is trusted read-only input. Generation remains absent:
// asking a question never requests a writable result directory or publication.
type QuestionInvocation struct {
	QuestionID   string               `json:"question_id"`
	VersionID    string               `json:"version_id"`
	PagePath     *string              `json:"page_path"`
	RegionID     *string              `json:"region_id"`
	PageQuery    string               `json:"page_query"`
	PageFragment string               `json:"page_fragment"`
	SourceMode   string               `json:"source_mode"`
	Context      *QuestionContext     `json:"context,omitempty"`
	Explanation  GenerationReference  `json:"explanation_context"`
	Limitations  []QuestionLimitation `json:"limitations"`
}

// QuestionCitation records an acquisition basis, never automatic claim support.
// Current availability and readable excerpts are composed under current access.
type QuestionCitation struct {
	Basis              string                   `json:"basis"`
	ReadID             *string                  `json:"read_id"`
	VerificationID     *string                  `json:"verification_id"`
	InputOrigin        *string                  `json:"input_origin"`
	TargetSetID        string                   `json:"target_set_id"`
	Source             json.RawMessage          `json:"source"`
	Location           json.RawMessage          `json:"location"`
	Hash               string                   `json:"bytes_sha256"`
	PackageEvidenceRef *QuestionPackageEvidence `json:"package_evidence_ref,omitempty"`
}

type QuestionPackageEvidence struct {
	VersionID  string `json:"version_id"`
	EvidenceID string `json:"evidence_id"`
}

type QuestionLimitation struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// AnswerEnvelope is public-final question output, validated before settlement.
// Final/partial content does not attest its own citation acquisition or support.
type AnswerEnvelope struct {
	State                 string                 `json:"state"`
	Body                  string                 `json:"body"`
	Citations             []QuestionCitation     `json:"citations"`
	ExplanationReferences []ExplanationReference `json:"explanation_references"`
	Limitations           []QuestionLimitation   `json:"limitations"`
}

// QuestionTransport records complete-frame acknowledgement separately from
// project-source observations. It contains descriptors, never question bytes.
type QuestionTransport struct {
	QuestionID                   string              `json:"question_id"`
	Text                         ProviderText        `json:"question_text"`
	Context                      *QuestionContext    `json:"display_context,omitempty"`
	Explanation                  GenerationReference `json:"explanation_context"`
	SourceMode                   string              `json:"source_mode"`
	PromptInputAcknowledged      bool                `json:"prompt_input_acknowledged"`
	ContextInputAcknowledged     bool                `json:"context_input_acknowledged"`
	ExplanationInputAcknowledged bool                `json:"explanation_input_acknowledged"`
}

// QuestionAnswer is immutable. Partial/error records never replace a completed
// answer, and each answer retains its actual producing attempt identity.
type QuestionAnswer struct {
	ID, QuestionID, AttemptID, State, CreatedAt string
	Body                                        ProviderText
	Citations                                   []QuestionCitation
	ExplanationReferences                       []ExplanationReference
	Limitations                                 []QuestionLimitation
}
