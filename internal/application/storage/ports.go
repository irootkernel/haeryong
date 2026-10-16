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
}
