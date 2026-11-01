package storage

import (
	"encoding/json"

	"haeryong.local/haeryong/internal/domain"
)

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

// CheckSourceVersion is the shared exact-version source-read predicate. It
// checks current visibility and scope, never whether an old bearer has expired.
func CheckSourceVersion(tx WriteTx, actor domain.Session, id string) (domain.Version, domain.QueryDocument, error) {
	v, err := tx.PublishedVersion(id)
	if err != nil {
		return v, domain.QueryDocument{}, err
	}
	d, err := tx.QueryDocument(v.DocumentID)
	if err != nil {
		return v, d, err
	}
	if d.Lifecycle == "trash" || d.Lifecycle == "purged" {
		return v, d, domain.ErrNotFound
	}
	if _, err = tx.Scope(d.ProjectID); err != nil {
		return v, d, err
	}
	if actor.Scope.Kind != "owner" {
		if !generationContains(actor.Scope.ProjectIDs, d.ProjectID) || !generationContains(actor.Scope.VersionIDs, v.ID) {
			return v, d, domain.ErrNotFound
		}
		if !generationContains(actor.Scope.Actions, "version.read") {
			return v, d, domain.ErrDenied
		}
	}
	return v, d, nil
}

// CheckCaptureActor retains the normal capture-principal/version boundary for
// both authenticated reads and the principal of already accepted local work.
func CheckCaptureActor(tx WriteTx, actor domain.Session, c domain.SourceCapture) error {
	if actor.Scope.Kind != "owner" && c.PrincipalID != actor.PrincipalID {
		return domain.ErrNotFound
	}
	var selected struct {
		VersionID string `json:"version_id"`
	}
	if err := json.Unmarshal(c.Selection, &selected); err != nil {
		return err
	}
	if _, err := tx.Scope(c.ProjectID); err != nil {
		return err
	}
	if selected.VersionID != "" {
		_, d, err := CheckSourceVersion(tx, actor, selected.VersionID)
		if err != nil {
			return err
		}
		if d.ProjectID != c.ProjectID {
			return domain.ErrNotFound
		}
		if actor.Scope.Kind != "owner" && !generationContains(actor.Scope.Actions, "source.read") {
			return domain.ErrDenied
		}
		return nil
	}
	if actor.Scope.Kind == "owner" {
		return nil
	}
	receipt, err := tx.Receipt(domain.OperationKey{PrincipalID: c.PrincipalID, OperationID: c.OperationID})
	if err != nil {
		return err
	}
	if receipt.Original.Target != "/api/v1/projects/"+c.ProjectID+"/generation-source-captures" {
		return domain.ErrNotFound
	}
	if !generationContains(actor.Scope.ProjectIDs, c.ProjectID) {
		return domain.ErrNotFound
	}
	if !generationContains(actor.Scope.Actions, "generation.request") || !generationContains(actor.Scope.Actions, "source.read") {
		return domain.ErrDenied
	}
	return nil
}

// SourceReadParent follows only a retained snapshot actually copied under
// pinned mode. An ignored read_id on a current or Git selection is not lineage.
func SourceReadParent(c domain.SourceCapture, r domain.SourceCaptureRead) (string, error) {
	var selected struct {
		Mode       string `json:"source_mode"`
		Selections []struct {
			SourceID string `json:"source_id"`
			Paths    []struct {
				Path   string `json:"path"`
				ReadID string `json:"read_id"`
			} `json:"paths"`
		} `json:"selections"`
	}
	var identity struct {
		Kind string `json:"kind"`
	}
	var location struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(c.Selection, &selected); err != nil {
		return "", err
	}
	if selected.Mode != "pinned" {
		return "", nil
	}
	if err := json.Unmarshal(r.SourceJSON, &identity); err != nil {
		return "", err
	}
	if identity.Kind != "snapshot" {
		return "", nil
	}
	if err := json.Unmarshal(r.LocationJSON, &location); err != nil {
		return "", err
	}
	for _, selection := range selected.Selections {
		if selection.SourceID == r.SourceID {
			for _, path := range selection.Paths {
				if path.Path == location.Path {
					return path.ReadID, nil
				}
			}
		}
	}
	return "", domain.ErrInvalidTarget
}

// CheckSourceReadAuthority keeps every inherited context on a copied read. A
// project-only child cannot turn version-owned material into independent data.
func CheckSourceReadAuthority(tx WriteTx, actor domain.Session, id, project, provider string) error {
	return checkSourceRead(tx, actor, id, project, provider, true)
}

// History keeps safe metadata inspectable after source policy revocation; it
// still obeys original principal/version visibility without granting any bytes.
func CheckSourceReadContext(tx WriteTx, actor domain.Session, id, project string) error {
	return checkSourceRead(tx, actor, id, project, "", false)
}

func checkSourceRead(tx WriteTx, actor domain.Session, id, project, provider string, bytes bool) error {
	seen := map[string]bool{}
	for id != "" && !seen[id] {
		seen[id] = true
		r, err := tx.SourceCaptureRead(id)
		if err != nil {
			return err
		}
		c, err := tx.SourceCapture(r.CaptureID)
		if err != nil {
			return err
		}
		if c.ProjectID != project || bytes && (c.State != "completed" || r.Availability != "available" && !(r.RetentionMode == "reference_only" && r.Reason == "reference_only_uncopied")) {
			return domain.ErrDenied
		}
		if err = CheckCaptureActor(tx, actor, c); err != nil {
			return err
		}
		if bytes {
			if _, err = ManagedSourcePolicy(tx, project, r.SourceID, provider); err != nil {
				return err
			}
		}
		parent, err := SourceReadParent(c, r)
		if err != nil {
			return err
		}
		if parent != "" {
			original, err := tx.SourceCaptureRead(parent)
			if err != nil {
				return err
			}
			if original.SourceID != r.SourceID || string(original.LocationJSON) != string(r.LocationJSON) || original.Hash != r.Hash || original.RetentionMode != "selected_snapshot" {
				return domain.ErrDenied
			}
		}
		id = parent
	}
	return nil
}

// GuardSourceInput compares captured and current scopes separately. Expiry of
// the original bearer is not a new lifetime requirement for accepted work.
func GuardSourceInput(tx WriteTx, scope domain.ProviderScope, readID string) error {
	auth, err := tx.AuthState()
	if err != nil {
		return err
	}
	if scope.AuthEpoch != auth.AuthEpoch {
		return domain.ErrDenied
	}
	project, err := TargetProject(tx, scope.Target)
	if err != nil {
		return err
	}
	actor := domain.Session{PrincipalID: scope.PrincipalID, AuthEpoch: scope.AuthEpoch, Scope: scope.CredentialScope}
	if scope.PrincipalID == auth.OwnerID {
		actor.Scope = domain.CredentialScope{Kind: "owner"}
	}
	if err = CheckSourceReadAuthority(tx, actor, readID, project, scope.ProviderID); err != nil {
		return err
	}
	if scope.PrincipalID == auth.OwnerID {
		return nil
	}
	current, err := tx.WorkflowPrincipal(scope.PrincipalID)
	if err != nil {
		return err
	}
	if !current.Active {
		return domain.ErrDenied
	}
	actor.Scope = current.Scope
	return CheckSourceReadAuthority(tx, actor, readID, project, scope.ProviderID)
}
