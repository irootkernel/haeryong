// Package storage defines application storage ports without leaking SQL handles.
package storage

import (
	"context"
	"haeryong.local/haeryong/internal/domain"
)

type Store interface {
	Write(context.Context, func(WriteTx) error) error
	Health(context.Context) (domain.Health, error)
	ReferencedObjects(context.Context, string, int) ([]domain.Object, error)
	Project(context.Context, string) (domain.Project, error)
	Document(context.Context, string) (domain.Document, error)
	Version(context.Context, string) (domain.Version, error)
	Page(context.Context, string, string) (domain.Page, error)
	Region(context.Context, string, string, string) (domain.Region, error)
	Evidence(context.Context, string, string) (domain.Evidence, error)
	Configuration(context.Context) (domain.Configuration, error)
	Receipt(context.Context, domain.OperationKey) (domain.Receipt, error)
	Job(context.Context, string) (domain.Job, error)
	Upload(context.Context, string) (domain.Upload, error)
	Resource(context.Context, string, string) (domain.Resource, error)
	Events(context.Context, string, int64, int) ([]domain.Event, error)
	Entries(context.Context, string, string, int) ([]domain.Entry, error)
	Ranges(context.Context, string, string, int64, int) ([]domain.UploadRange, error)
}

// Each callback is a short serialized write transaction. Files are streamed and
// synced before entry; only atomic publication/directory sync may run inside it.
type WriteTx interface {
	ReferenceTx
	SearchReadTx
	SearchTx
	QueryReadTx
	CredentialsTx
	ConfigurationTx
	WorkJobs(int) ([]domain.Job, error)
	StageEntry(domain.Entry) error
	DraftTarget(string) (domain.DraftTarget, error)
	AnnotationTarget(string) (domain.AnnotationTarget, error)
	Page(string, string) (domain.Page, error)
	Library() (domain.Library, error)
	Upload(string) (domain.Upload, error)
	Uploads(string, string, bool, int) ([]domain.Upload, error)
	UploadEntries(string, string, int) ([]domain.UploadEntry, error)
	Ranges(string, string, int64, int) ([]domain.UploadRange, error)
	UploadPins(string, string, int) ([]domain.Pin, error)
	UploadStats(string) (domain.UploadStats, error)
	PutUploadEntry(domain.UploadEntry) error
	TransferDigest(string) (string, error)
	UploadComplete(string) error
	TouchUpload(string, string) error
	SetSealOperation(string, string) error
	ContentSet(string) (string, bool, error)
	Entries(string, string, int) ([]domain.Entry, error)
	SealContentSetDigest(string, string) error
	PutUploadObject(string, string) error
	PutImportIntent(domain.ImportIntent) error
	FinalizeImport(domain.ImportIntent, domain.Version) error
	DiscardContentSet(string) error
	PinContentSet(string, string) error
	ReleaseOwnerPins(string, string) error
	ImportIntent(string) (domain.ImportIntent, error)
	SetJobResult(string, []byte) error
	ConfigureJobScope(domain.Job) error
	ReclaimAbandonedUpload(string) error
	CreateMetadataIndex(domain.MetadataIndex) error
	MetadataIndex(string) (domain.MetadataIndex, error)
	CompleteMetadataIndex(string) error
	ReleaseMetadataPreparation(string) error
	LinkMetadataIndex(string, string) error
	PutImportMetadata(string, string, string, []byte) error
	ImportMetadata(string, string, string, int) ([]domain.Resource, error)
	ValidateImportMetadata(string, string) (string, error)
	PublishedVersion(string) (domain.Version, error)

	Receipt(domain.OperationKey) (domain.Receipt, error)
	Events(string, int64, int) ([]domain.Event, error)
	EventHead() (string, int64, error)
	Document(string) (domain.Document, error)
	Version(string) (domain.Version, error)
	AuthState() (domain.AuthState, error)
	SessionByHash(string) (domain.Session, error)
	SessionByID(string) (domain.Session, error)
	Sessions() ([]domain.Session, error)
	PutSession(domain.Session) error
	Pairing(string) (domain.Pairing, error)
	Pairings(bool) ([]domain.Pairing, error)
	PairingStats(string, int64, string) (int, int, error)
	PutPairing(domain.Pairing) error
	AuthAudit(string, string, string) error
	Job(string) (domain.Job, error)
	Jobs() ([]domain.Job, error)

	Scope(string) (domain.ScopeState, error)
	ReserveOperation(domain.Request, func() ([]byte, error)) (domain.Receipt, bool, error)
	CompleteOperation(domain.OperationKey, domain.Response) error
	ReconcileOperation(domain.OperationKey) error
	MutateResource(domain.Resource, int64, string) (domain.Resource, error)
	CreateProject(domain.Project) error
	CreateDocument(domain.Document) error
	CreateContentSet(string, string) error
	SealContentSet(string) error
	PutEntry(domain.Entry) error
	PutVersion(domain.Version) error
	PutPage(domain.Page) error
	PutRegion(domain.Region) error
	PutSource(domain.Source) error
	PutEvidence(domain.Evidence) error
	LinkRegionEvidence(string, string, string, string) error
	SetConfiguration(domain.Configuration, int64) error
	PublishVersion(string, int64) error
	Object(string) (domain.Object, error)
	PutDurableObject(domain.Object) error
	AcquirePin(domain.Pin) error
	ReleasePin(string) error
	Adopt(domain.Reference, string) error
	AdoptUploadObject(string, string, []byte, domain.Reference, string) error
	ReserveGC(string, string) (domain.GCReservation, error)
	FinishGC(domain.GCReservation) error
	CreateJob(domain.Job) error
	ClaimJob(string) (domain.Job, error)
	CancelJob(string) (domain.Job, error)
	ConfirmStop(string, int64) error
	ReconcileJob(string) error
	RetryJob(string, string) (domain.Job, error)
	FinishJob(string, int64, string, func(WriteTx) error) error
	CreateUpload(domain.Upload) error
	RecordRange(domain.UploadRange, int64, string) (domain.Checkpoint, error)
	CompleteCheckpoint(string, int64, string) (domain.Checkpoint, error)
	CompleteUpload(string, domain.Checkpoint, string) (domain.Response, error)
	FinishSeal(string, int64, string) error
	AbandonUpload(string) error

	// Personal reader state and sealed contexts read inside the same
	// serialized transaction as their authority checks.
	ResourceRow(kind, id string) (domain.Resource, error)
	PutContext(domain.ContextRecord) error
	Context(string) (domain.ContextRecord, error)

	// Source-target capture, verification and staged-check records
	// (TASK-004-01). Writes are append-only except check-intent state.
	PutTargetSet(domain.TargetSet) error
	PutCapturedRead(domain.CapturedRead) error
	PutVerification(domain.CitationVerification) error
	PutCheckIntent(domain.CheckIntent) error
	CheckIntent(string) (domain.CheckIntent, error)
	CheckIntentByOperation(principalID, operationID string) (domain.CheckIntent, error)
	OpenCheckIntents() ([]domain.CheckIntent, error)
	ResolveCheckIntent(id, state, reason, resolvedAt string) error
	ReleaseCheckIntentPins(intentID string) error
	CleanupCheckIntentPins() error
	TestTrashDocument(id string) error
	TestReplaceEvidence(version, evidence string, metadata []byte) error
	TestReopenIntent(operationID, principalID string) error
	TestReopenResolveFail(operationID string) error
	VerificationByOperation(principalID, operationID string) (domain.CitationVerification, bool, error)
	Verifications(versionID, evidenceID, afterCheckedAt, afterID string, limit int) ([]domain.CitationVerification, error)
	LatestVerification(versionID, evidenceID string) (domain.CitationVerification, error)
	LatestEvidenceObservation(versionID, sourceID string) (domain.SourceObservation, error)
	SourceCaptures(projectID string) ([]domain.TargetSet, error)
	RegisterProject(domain.Project) error

	// Observation runs, per-source rows, coverage sets, units and triggers
	// (TASK-004-01 M3). Observation/trigger/coverage writes are append-only.
	PutObservationRun(domain.ObservationRun) error
	PutSourceObservation(domain.SourceObservation) error
	PutReassessmentTrigger(domain.ReassessmentTrigger) error
	PutCoverageSet(domain.CoverageSet) error
	PutRequiredUnit(domain.RequiredUnit) error
	ObservationRunsByVersion(versionID string) ([]domain.ObservationRun, error)
	ObservationRunByID(runID string) (domain.ObservationRun, error)
	ObservationRunByTargetSet(targetSetID string) (domain.ObservationRun, bool, error)
	ObservationsByRun(runID string) ([]domain.SourceObservation, error)
	ObservationsByOperation(principalID, operationID string) ([]domain.SourceObservation, error)
	ObservationRunForOperation(principalID, operationID string) (domain.ObservationRun, bool, error)
	TriggersByTargetSet(versionID, targetSetID string) ([]domain.ReassessmentTrigger, error)
	CoverageSetByRun(runID string) (domain.CoverageSet, error)
	CoverageSetByID(id string) (domain.CoverageSet, error)
	RequiredUnits(coverageSetID, afterKind, afterPage, afterRegion string, limit int) ([]domain.RequiredUnit, error)
	TargetSetByID(id string) (domain.TargetSet, error)
	VersionCapture(versionID, targetSetID string) (domain.TargetSet, error)
	VerificationCaptures(versionID, projectID string) ([]domain.TargetSet, error)
	VersionSources(versionID string) ([]domain.Source, error)
	VersionEvidence(versionID string) ([]domain.Evidence, error)
	VersionPages(versionID string) ([]domain.Page, error)
	VersionRegion(versionID, pagePath, regionID string) (domain.Region, error)
	VersionRegionEvidence(versionID string) ([]domain.RegionEvidence, error)
	LatestObservationBySource(projectID string) ([]domain.SourceStatus, error)
	// AppendEvent appends one transactional-outbox event inside the caller's
	// transaction (the sqlite adapter's appendEvent).
	AppendEvent(resourceKind, id, kind string, revision int64, body []byte) error
}

// SourceReadStore exposes recorded source facts to read-only composers.
type SourceReadStore interface {
	Store
	SourceObservationSummaries(ctx context.Context) ([]domain.SourceStatus, error)
}
