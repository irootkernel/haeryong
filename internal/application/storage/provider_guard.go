package storage

import (
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
)

// GuardProvider binds detached external work to its captured authority. A device
// disconnect is deliberately absent: accepted work belongs to its principal.
func GuardProvider(tx WriteTx, a domain.ProviderAttempt) error {
	j, e := tx.Job(a.JobID)
	if e != nil {
		return e
	}
	if !domain.ProviderJob(j.Kind) || j.State != "running" || j.CancelRequested || j.AttemptGeneration != providerApplyGeneration(a) {
		return domain.ErrStaleWorker
	}
	return guardProviderAuthority(tx, j, a)
}

// GuardProviderContinuation validates only an explicit queued saved-result
// reservation. Actual callbacks still require GuardProvider's running fence.
func GuardProviderContinuation(tx WriteTx, a domain.ProviderAttempt) error {
	j, e := tx.Job(a.JobID)
	if e != nil {
		return e
	}
	if !domain.ProviderJob(j.Kind) || j.State != "queued" || j.CancelRequested || a.State != "result_saved" || a.ApplyGeneration != j.AttemptGeneration+1 {
		return domain.ErrStaleWorker
	}
	return guardProviderAuthority(tx, j, a)
}

func guardProviderAuthority(tx WriteTx, j domain.Job, a domain.ProviderAttempt) error {
	l, e := tx.Library()
	if e != nil {
		return e
	}
	if l.RuntimeGeneration != a.Runtime {
		return domain.ErrReconciliation
	}
	h, e := tx.RecoveryHold()
	if e != nil {
		return e
	}
	if h != nil && h.State != "reviewed" {
		return domain.ErrReconciliation
	}
	var scope domain.ProviderScope
	if json.Unmarshal(j.Scope, &scope) != nil || scope.PrincipalID == "" || scope.TargetSetID != a.TargetSetID || scope.RequestSHA != a.RequestSHA {
		return domain.ErrDenied
	}
	state, e := tx.AuthState()
	if e != nil {
		return e
	}
	if state.AuthEpoch != scope.AuthEpoch {
		return domain.ErrDenied
	}
	if scope.PrincipalID != state.OwnerID {
		p, e := tx.WorkflowPrincipal(scope.PrincipalID)
		if e != nil {
			return e
		}
		if !p.Active {
			return domain.ErrDenied
		}
		// Production managed submissions are owner-owned until the feature owner
		// installs its exact workflow action authorization. Never infer privilege.
		return domain.ErrDenied
	}
	if e = AuthorizeTarget(tx, scope.Target); e != nil {
		return e
	}
	if a.TargetSetID != "" {
		target, e := tx.TargetSetByID(a.TargetSetID)
		if e != nil {
			return e
		}
		project, e := TargetProject(tx, scope.Target)
		if e != nil {
			return e
		}
		if project == "" || target.ProjectID != project {
			return domain.ErrDenied
		}
		if _, ok := j.Permissions[project]; !ok {
			return domain.ErrDenied
		}
	}
	if e = tx.CheckProviderPolicy(a); e != nil {
		return e
	}
	for id, rev := range j.Permissions {
		p, e := tx.Scope(id)
		if e != nil {
			return e
		}
		if p.Project.PermissionsRevision != rev {
			return domain.ErrDenied
		}
	}
	return nil
}

func providerApplyGeneration(a domain.ProviderAttempt) int64 {
	if a.State == "result_saved" && a.ApplyGeneration > 0 {
		return a.ApplyGeneration
	}
	return a.Generation
}

// ProviderSavedObjects enumerates the exact captured inputs and complete result
// whose actual bytes the coordinator verifies before consuming a continuation.
// Metadata readiness alone does not establish byte availability or integrity.
func ProviderSavedObjects(tx WriteTx, a domain.ProviderAttempt) ([]domain.Object, error) {
	if a.State != "result_saved" || a.ResultHash == "" {
		return nil, domain.ErrReconciliation
	}
	j, e := tx.Job(a.JobID)
	if e != nil {
		return nil, e
	}
	var scope domain.ProviderScope
	if json.Unmarshal(j.Scope, &scope) != nil {
		return nil, domain.ErrInvalidTarget
	}
	inputs, e := tx.ProviderInputs(a.ID)
	if e != nil {
		return nil, e
	}
	if len(inputs) != len(scope.ReadIDs) {
		return nil, domain.ErrReconciliation
	}
	byRead := map[string]domain.ProviderInput{}
	for _, input := range inputs {
		byRead[input.ReadID] = input
	}
	objects := make([]domain.Object, 0, len(inputs)+1)
	for _, id := range scope.ReadIDs {
		input, ok := byRead[id]
		if !ok {
			return nil, domain.ErrReconciliation
		}
		read, e := tx.CapturedRead(id)
		if e != nil {
			return nil, e
		}
		if read.TargetSetID != a.TargetSetID || read.BytesSHA != input.Hash {
			return nil, domain.ErrReconciliation
		}
		object, e := tx.Object(input.Hash)
		if e != nil {
			return nil, e
		}
		if object.State != "ready" {
			return nil, domain.ErrReconciliation
		}
		objects = append(objects, object)
	}
	result, e := tx.Object(a.ResultHash)
	if e != nil {
		return nil, e
	}
	if result.State != "ready" {
		return nil, domain.ErrReconciliation
	}
	return append(objects, result), nil
}

// GuardProviderReconciliation is used only after the storage adapter verifies
// the current executing reconciliation intent. It grants no dispatch authority.
func GuardProviderReconciliation(tx WriteTx, j domain.Job, a domain.ProviderAttempt) error {
	return guardProviderAuthority(tx, j, a)
}
