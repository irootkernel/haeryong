package storage

import "haeryong.local/haeryong/internal/domain"

type ProviderTx interface {
	ProviderAttempt(string) (domain.ProviderAttempt, error)
	ProviderAttempts(string) ([]domain.ProviderAttempt, error)
	PrepareProviderAttempt(domain.ProviderAttempt, []domain.ProviderInput) error
	// AuthorizeProviderRetry is a trusted admission seam, never a client-supplied bypass.
	AuthorizeProviderRetry(string, []string) error
	AuthorizeProviderReconciliation(string, []string) error
	CheckProviderPolicy(domain.ProviderAttempt) error
	ProviderInputs(string) ([]domain.ProviderInput, error)
	DispatchProviderAttempt(string) error
	ObserveProviderAttempt(string, string, string, string, string) error
	ObserveProviderInput(string, string, string) error
	SaveProviderResult(string, domain.Object) error
	ApplyProviderResult(string, func(WriteTx) error) error
	RebindProviderResult(string) error
	PauseProviderJob(string, string) error
	NextProviderReconciliation(string) (string, error)
	ClaimProviderReconciliation(string, string) error
	CompleteProviderReconciliation(string, string) error
	ResumeProviderJob(string, int64, string) (domain.Job, error)
}
