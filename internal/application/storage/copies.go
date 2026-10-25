package storage

import "haeryong.local/haeryong/internal/domain"

type CopyTx interface {
	CopySet(string) (domain.CopySet, error)
	PutCopySet(domain.CopySet) error
	CopySets(string, string, int) ([]domain.CopySet, error)
	CaptureCopyVersions(string, domain.CopyRequest) error
	CopyVersions(string, string, int) ([]domain.CopyVersion, error)
	RecoveryHold() (*domain.RecoveryHold, error)
	ReviewRecovery(string, []string) error
	CopyBackupStatus() (domain.BackupStatus, error)
}
