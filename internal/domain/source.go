package domain

// Source-target capture, check and observation records (TASK-004-01). These
// rows are service facts about exact source targets; none of them rewrites
// authored evidence or claims semantic assessment.
type TargetSet struct {
	ID, ProjectID, SourceID         string
	RequestedSelection, TargetsJSON []byte
	CapturedAt                      string
}

// CapturedRead records the bounded bytes actually obtained for one check.
type CapturedRead struct {
	ID, TargetSetID, Basis              string
	SourceJSON, LocationJSON            []byte
	BytesSHA                            string
	ConfigRevision, PermissionsRevision int64
	CreatedAt                           string
}

// CitationVerification is the append-only service check history of one
// imported evidence item. Outcome/reasons follow the registry; verified_at is
// populated only for captured/matched. PrincipalID keys the owning receipt.
type CitationVerification struct {
	ID, PrincipalID, VersionID, EvidenceID, ReadID, TargetSetID string
	BytesOrigin                                                 string
	SourceJSON, LocationJSON                                    []byte
	BytesSHA                                                    string
	Outcome                                                     string
	ExpectedBytesSHA                                            string
	ExpectedExcerpt                                             []byte
	ExpectedExcerptRef                                          string
	ConfigRevision, PermissionsRevision                         int64
	CheckedAt, VerifiedAt, Reason, OperationID                  string
}

// SourceObservation is one (run, source) availability/change fact. An
// availability-blocked result keeps TargetSetID empty: only a real capture
// creates a target set, never an empty verified target.
type SourceObservation struct {
	ID, RunID, VersionID, TargetSetID, SourceID string
	RepoID                                      string
	BaseRevision, ObservedRevision              string
	Availability, Classification, Reason        string
	ChangedPathsJSON, DependencyScopeJSON       []byte
	ConfigRevision, PermissionsRevision         int64
	ObservedAt, OperationID                     string
}

// ObservationRun is one observation POST's run identity (addendum r4.3): it
// names the requested selection and scope, links the receipt and — when at
// least one source resolved — the run's single target set. Committed marks a
// run whose per-source observation rows exist; a run whose staged protocol
// crashed after the target persist stays uncommitted and is never a selection
// candidate (addendum r4.1 rule 4).
type ObservationRun struct {
	ID, VersionID, PrincipalID, OperationID string
	RequestedSelection, ScopeJSON           []byte
	TargetSetID                             string
	CreatedAt                               string
	Committed                               bool
}

// CoverageSet is one observation run's captured denominator. TargetSetID is
// empty exactly for an all-unavailable run's attempted-scope set.
type CoverageSet struct {
	ID, VersionID, TargetSetID, RunID string
	ScopeJSON                         []byte
	UnitKind, CapturedAt, Origin      string
}

// RequiredUnit is one captured required tuple of a coverage set; the ” page
// and region values are the schema sentinel (the wire maps them to null).
// TargetSetID is null exactly when the owning coverage set's is null.
type RequiredUnit struct {
	CoverageSetID, Kind, PagePath, RegionID, Reason, TargetSetID string
}

// RegionEvidence anchors one evidence item to an exact declared region.
type RegionEvidence struct {
	VersionID, PagePath, RegionID, EvidenceID string
}

// SourceStatus is the latest recorded observation state of one configured
// (project, source) pair, derived from observation runs only (S30).
type SourceStatus struct {
	ProjectID, SourceID          string
	LastObservedAt               string
	LastAvailability, LastReason string
	LastRevision                 string
	Observed                     bool
}

// ReassessmentTrigger is a durable direct-change fact anchored to one exact
// captured target. EPIC-004 inserts and reads only; assessments clear them.
type ReassessmentTrigger struct {
	ID, RunID, VersionID, TargetSetID, SourceID string
	AnchorKind, PagePath, RegionID              string
	Reason                                      string
	ChangedPathsJSON                            []byte
	ObservationID, ObservedAt, CreatedAt        string
}

// CheckIntent is the staged check protocol's durable intent (plan §7.3). The
// restart sweep resolves open intents by operation_id; it never touches
// import/upload receipts.
type CheckIntent struct {
	ID, OperationID, PrincipalID, Kind          string
	ExpectationUpload                           string
	CanonicalTarget, ValidatedBody              []byte
	ConfigRevision, PermissionsRevision         int64
	State, FailureReason, CreatedAt, ResolvedAt string
}

// CheckIntentPinOwner names object pins acquired by a staged check.
const CheckIntentPinOwner = "check_intent"
