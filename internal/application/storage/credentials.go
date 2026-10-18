package storage

import "haeryong.local/haeryong/internal/domain"

// CredentialsTx persists verifiers and stable current bindings; no issuance
// endpoint or implicit owner authority is provided by these storage primitives.
type CredentialsTx interface {
	AuthorizedUploads(string, string, string, bool, int, domain.CredentialScope) ([]domain.Upload, error)
	WorkflowPrincipal(string) (domain.WorkflowPrincipal, error)
	PutWorkflowPrincipal(domain.WorkflowPrincipal) error
	ScopedCredential(string) (domain.ScopedCredential, error)
	ScopedCredentialByHash(string) (domain.ScopedCredential, error)
	PutScopedCredential(domain.ScopedCredential) error
}
