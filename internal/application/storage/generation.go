package storage

import "haeryong.local/haeryong/internal/domain"

// GenerationTx stores typed local application intent on the existing job and
// whole unpublished candidates. It does not own provider dispatch or retry.
type GenerationTx interface {
	CreateGenerationIntent(domain.GenerationIntent) error
	GenerationIntent(string) (domain.GenerationIntent, error)
	AdmitGeneration(domain.Job) error
	PrepareGenerationIntent(domain.GenerationIntent) error
	CreateDraft(domain.Draft) error
	Draft(string) (domain.Draft, error)
	Drafts(string, string, int, domain.CredentialScope) ([]domain.Draft, error)
	DraftHistory(string, int64, int) ([]domain.Draft, error)
	UpdateDraft(domain.Draft, int64) error
	WorkflowSelection(string) (domain.WorkflowSelection, error)
	PutWorkflowSelection(domain.WorkflowSelection, int64) error
	WorkflowPrincipalByBinding(string) (domain.WorkflowPrincipal, error)
}

// GenerationInputObjects describes retained non-source request bytes. It does
// not create a source target set or a producing-source observation.
func GenerationInputObjects(tx WriteTx, scope domain.ProviderScope) ([]domain.Object, error) {
	out := []domain.Object{}
	if scope.Invocation == nil {
		return out, nil
	}
	inv := scope.Invocation
	if inv.Generation == nil {
		if inv.PromptRef != nil {
			return nil, domain.ErrInvalidTarget
		}
		return out, nil
	}
	refs := []domain.ProviderText{}
	if inv.PromptRef != nil {
		if inv.Prompt != "" {
			return nil, domain.ErrInvalidTarget
		}
		refs = append(refs, *inv.PromptRef)
	}
	for _, ref := range inv.Generation.References {
		refs = append(refs, domain.ProviderText{Hash: ref.ObjectHash, Size: ref.Size})
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		object, e := tx.Object(ref.Hash)
		if e != nil {
			return nil, e
		}
		if object.State != "ready" || object.Size != ref.Size {
			return nil, domain.ErrContentIntegrity
		}
		if !seen[object.Hash] {
			out = append(out, object)
			seen[object.Hash] = true
		}
	}
	return out, nil
}
