package storage

import "haeryong.local/haeryong/internal/domain"

// SourceCaptureTx extends the serialized writer; filesystem work remains outside.
type SourceCaptureTx interface {
	CreateSourceCapture(domain.SourceCapture) error
	SourceCapture(string) (domain.SourceCapture, error)
	PutSourceCaptureRead(domain.SourceCaptureRead) error
	SourceCaptureRead(string) (domain.SourceCaptureRead, error)
	SourceCaptureReads(string, string, int) ([]domain.SourceCaptureRead, error)
	SourceCaptureDiff(string, string, string, string, int) ([]domain.SourceCaptureDelta, error)
	FinishSourceCapture(string, string, string) error
	AttachSourceInputs(string, []string) error
}
