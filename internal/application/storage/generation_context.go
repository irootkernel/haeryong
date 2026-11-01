package storage

import (
	"reflect"

	"haeryong.local/haeryong/internal/domain"
)

// ResolveGenerationReference binds explanation context to current immutable
// library data. It grants no project-source read or evidence observation.
func ResolveGenerationReference(tx WriteTx, scope domain.CredentialScope, ref domain.ExplanationReference) (domain.GenerationReference, error) {
	out := domain.GenerationReference{ExplanationReference: ref}
	v, e := tx.PublishedVersion(ref.VersionID)
	if e != nil {
		return out, e
	}
	d, e := tx.QueryDocument(v.DocumentID)
	if e != nil {
		return out, e
	}
	if d.Lifecycle == "trash" || d.Lifecycle == "purged" {
		return out, domain.ErrNotFound
	}
	if _, e = tx.Scope(d.ProjectID); e != nil {
		return out, e
	}
	if scope.Kind != "owner" {
		if !generationContains(scope.ProjectIDs, d.ProjectID) || !generationContains(scope.VersionIDs, v.ID) {
			return out, domain.ErrNotFound
		}
		if !generationContains(scope.Actions, "version.read") {
			return out, domain.ErrDenied
		}
	}
	out.ProjectID = d.ProjectID
	out.DocumentID = d.ID
	hash := v.PayloadSHA
	out.ObjectHash = v.ManifestSHA
	if ref.PagePath != nil {
		if e = domain.ValidatePayloadPath(*ref.PagePath); e != nil {
			return out, domain.ErrInvalidTarget
		}
		if _, e = tx.Page(v.ID, *ref.PagePath); e != nil {
			return out, e
		}
		entry, e := tx.QueryEntry(v.ContentSetID, *ref.PagePath)
		if e != nil {
			return out, e
		}
		if entry.Kind != "file" {
			return out, domain.ErrInvalidTarget
		}
		hash = entry.Hash
		out.ObjectHash = entry.Hash
	}
	if ref.RegionID != nil {
		if ref.PagePath == nil || *ref.RegionID == "" {
			return out, domain.ErrInvalidTarget
		}
		if _, e = tx.VersionRegion(v.ID, *ref.PagePath, *ref.RegionID); e != nil {
			return out, e
		}
	}
	if ref.Hash != hash || len(hash) != 64 {
		return out, domain.ErrContentIntegrity
	}
	object, e := tx.Object(out.ObjectHash)
	if e != nil {
		return out, e
	}
	if object.State != "ready" {
		return out, domain.ErrDeleting
	}
	out.Size = object.Size
	return out, nil
}
func generationContains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func generationContextAuthority(tx WriteTx, scope domain.ProviderScope, current domain.CredentialScope) error {
	if scope.Invocation == nil || scope.Invocation.Generation == nil {
		return nil
	}
	for _, ref := range scope.Invocation.Generation.References {
		for _, allowed := range []domain.CredentialScope{scope.CredentialScope, current} {
			actual, e := ResolveGenerationReference(tx, allowed, ref.ExplanationReference)
			if e != nil {
				return e
			}
			if !reflect.DeepEqual(actual, ref) {
				return domain.ErrContentIntegrity
			}
		}
	}
	return nil
}
