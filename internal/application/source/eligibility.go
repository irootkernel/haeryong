package source

import (
	"encoding/json"
	"errors"
	"net/url"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// Eligibility is the single shared rule (plan §4) reused by evidence views,
// verification, observations, assessments reads and project-sources reads:
// scope containment for scoped principals, document lifecycle, and the
// version.read/source.read action layer. Availability is never conflated
// with authorization.
type Eligibility struct {
	Session   domain.Session
	Version   domain.Version
	Document  domain.QueryDocument
	ProjectID string
}

// Check computes eligibility for an exact version. Scoped principals receive
// the nondisclosing ErrNotFound for out-of-scope projects; trash/purged
// documents are never visible; archived documents and projects remain
// eligible (project archival is categorization only).
func Check(tx app.WriteTx, record domain.Session, versionID string) (Eligibility, error) {
	v, d, err := app.CheckSourceVersion(tx, record, versionID)
	if err != nil {
		return Eligibility{}, err
	}

	return Eligibility{Session: record, Version: v, Document: d, ProjectID: d.ProjectID}, nil
}

// MayReadSource reports the byte-affecting action layer: the owner always
// passes; a scoped principal needs source.read alongside version.read.
func (e Eligibility) MayReadSource() bool {
	if e.Session.Scope.Kind == "owner" {
		return true
	}
	return containsAction(e.Session.Scope.Actions, "source.read") && containsAction(e.Session.Scope.Actions, "version.read")
}

func containsID(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func containsAction(values []string, value string) bool { return containsID(values, value) }

// Mapping is one configured project source selection (configuration.md
// §Remembered project settings). Host paths are for the internal reader only;
// wire views build their own projections and never expose them.
type Mapping struct {
	SourceID      string   `json:"source_id"`
	Kind          string   `json:"kind"`
	RootPath      string   `json:"root_path"`
	RepoID        string   `json:"repo_id,omitempty"`
	RetentionMode string   `json:"retention_mode"`
	ProviderIDs   []string `json:"provider_ids"`
}

// Mappings decodes the project's effective configured sources.
func Mappings(tx app.WriteTx, projectID string) ([]Mapping, error) {
	if hold, e := tx.RecoveryHold(); e != nil {
		return nil, e
	} else if hold != nil && hold.State != "reviewed" {
		return nil, domain.ErrReconciliation
	}
	configuration, err := tx.Configuration()
	if err != nil {
		return nil, err
	}
	var value struct {
		Projects map[string]struct {
			Sources []json.RawMessage `json:"sources"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(configuration.Value, &value); err != nil {
		return nil, err
	}
	out := []Mapping{}
	project, ok := value.Projects[projectID]
	if !ok {
		return out, nil
	}
	for _, raw := range project.Sources {
		var mapping Mapping
		if err := json.Unmarshal(raw, &mapping); err != nil {
			return nil, err
		}
		out = append(out, mapping)
	}
	return out, nil
}

// ErrMappingAmbiguous fail-closes when one repo_id is mapped twice under
// different source ids; the owner resolves the configuration.
var ErrMappingAmbiguous = errors.New("mapping_ambiguous")

// LinkGit resolves the project mapping for an authored git source identity
// (plan §7.5): project + exact repo_id match among kind=git mappings.
func LinkGit(mappings []Mapping, repoID string) (Mapping, error) {
	if repoID == "" {
		return Mapping{}, errors.New("source_not_registered")
	}
	found := []Mapping{}
	for _, m := range mappings {
		if m.Kind == "git" && m.RepoID == repoID {
			found = append(found, m)
		}
	}
	switch len(found) {
	case 0:
		return Mapping{}, errors.New("source_not_registered")
	case 1:
		return found[0], nil
	default:
		return Mapping{}, ErrMappingAmbiguous
	}
}

// CanonicalTarget renders the canonical query-form verification route used
// for receipt identity; the path form normalizes to it before fingerprinting.
func CanonicalTarget(versionID, evidenceID string) string {
	return "/api/v1/versions/" + versionID + "/source-evidence-verifications?evidence_id=" + url.QueryEscape(evidenceID)
}
