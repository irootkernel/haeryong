package domain

import "encoding/json"

// GenerationInvocation is constructed by trusted admission. The adapter derives
// a fresh output directory from its private root and the actual attempt ID.
// Skill identity is captured before dispatch; source delivery remains separate.
type GenerationInvocation struct {
	SkillPath    string                `json:"skill_path"`
	SkillSHA     string                `json:"skill_sha256"`
	SkillVersion string                `json:"skill_version"`
	Intent       string                `json:"intent"`
	References   []GenerationReference `json:"explanation_context,omitempty"`
}

// GenerationIntent owns the deterministic application of a saved directory.
// The existing job and provider attempt still own execution and uncertainty.
type GenerationIntent struct {
	JobID, ProjectID, AllocatedDocumentID, VersionID string
	DocumentID                                       *string
	BaseVersionID                                    *string
	DraftID, Intent, RequestHash, CreatedAt          string
	OutputUploadID, ContentSetID, MetadataHash       string
	PayloadSHA, PreparationAttemptID                 string
	DocumentRevision                                 int64
	AdmissionReady                                   bool
	CaptureOperationID                               string
	SourceSelection                                  json.RawMessage
	SourceReadIDs                                    []string
	PreparedVersion                                  *Version
	Title                                            string
	PriorReferences                                  []GenerationReference
}

// ExplanationReference pins an immutable explanation separately from source
// evidence. Whole-version identity is its canonical payload inventory hash.
type ExplanationReference struct {
	VersionID string  `json:"version_id"`
	PagePath  *string `json:"page_path"`
	RegionID  *string `json:"region_id"`
	Hash      string  `json:"bytes_sha256"`
}

// GenerationReference records the exact object supplied for that reference:
// a selected page, or the service manifest for whole-version context.
type GenerationReference struct {
	ExplanationReference
	ProjectID  string `json:"project_id"`
	DocumentID string `json:"document_id"`
	ObjectHash string `json:"object_sha256"`
	Size       int64  `json:"byte_size"`
}

// Draft is a whole unpublished candidate. DocumentID preserves explicit new-
// document intent; AllocatedDocumentID is private until publication.
type Draft struct {
	ID, ProjectID, JobID, ProposedVersionID string
	DocumentID, BaseVersionID               *string
	AllocatedDocumentID                     string
	State, Intent, CreatedAt, UpdatedAt     string
	Revision                                int64
	ContentSetID, PayloadSHA, MetadataHash  string
	ManifestSHA, FailureReason              string
	PublishedVersionID                      string
	Metadata                                json.RawMessage
}

// WorkflowSelection remembers resource choices, never a frozen operation base
// or additional authority. Issued credentials retain their own captured scope.
type WorkflowSelection struct {
	BindingID, PrincipalID, ProjectID string
	DocumentID                        *string
	ProviderID                        string
	SourceSelection                   json.RawMessage
	Revision                          int64
}

// GenerationTransport separates actual public-final native text from the
// service-derived complete directory archive. It never retains private reasoning.
type GenerationTransport struct {
	References              []GenerationReference `json:"explanation_context,omitempty"`
	ReferencesAcknowledged  bool                  `json:"explanation_context_acknowledged,omitempty"`
	Prompt                  *ProviderText         `json:"prompt,omitempty"`
	PromptInputAcknowledged bool                  `json:"prompt_input_acknowledged,omitempty"`
	Format                  string                `json:"saved_output_format"`
	SkillVersion            string                `json:"skill_version"`
	SkillSHA                string                `json:"skill_sha256"`
	AdmittedSkillPath       string                `json:"admitted_skill_path"`
	NativeSkillPath         string                `json:"native_skill_path"`
	SkillDiscovered         bool                  `json:"native_skill_discovered"`
	SkillInputAcknowledged  bool                  `json:"skill_input_acknowledged"`
	OutputDirectory         string                `json:"output_directory"`
	PublicFinalSHA          string                `json:"public_final_sha256,omitempty"`
	PublicFinalSize         int64                 `json:"public_final_byte_size"`
	CapturedAt              string                `json:"directory_captured_at,omitempty"`
}
