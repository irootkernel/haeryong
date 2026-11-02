package questions

import (
	"context"
	"encoding/json"
	"io"
	"sort"

	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// The authored intent lives in the immutable normalized manifest, not the
// publication's summary Metadata. Read it outside the writer and guard every
// chunk with the current owner/version authority.
func (s *Service) declaredSourceIntent(ctx context.Context, token, key, version string, page *string) (string, error) {
	var object domain.Object
	check := func(tx app.WriteTx) error {
		if _, e := s.owner(ctx, tx, token, key, version, page); e != nil {
			return e
		}
		v, e := tx.PublishedVersion(version)
		if e != nil {
			return e
		}
		if v.ManifestSHA == "" {
			return domain.ErrContentIntegrity
		}
		current, e := tx.Object(v.ManifestSHA)
		if e != nil {
			return e
		}
		if object.Hash != "" && current != object {
			return domain.ErrContentIntegrity
		}
		object = current
		return nil
	}
	if e := s.Store.Write(ctx, check); e != nil {
		return "", e
	}
	if s.Bytes == nil {
		return "", domain.ErrReconciliation
	}
	var manifest struct {
		Version struct {
			Intent *string `json:"source_intent"`
		} `json:"version"`
	}
	e := s.Bytes.Read(ctx, object, domain.Pin{Kind: "question_read", OwnerID: key + ":source-intent"}, func(r io.ReadSeeker) error {
		return json.NewDecoder(&inputReader{reader: r, ctx: ctx, store: s.Store, check: check}).Decode(&manifest)
	})
	if e != nil {
		return "", e
	}
	if manifest.Version.Intent == nil {
		return "", nil
	}
	value := *manifest.Version.Intent
	if value != "historical" && value != "moving_target" {
		return "", domain.ErrContentIntegrity
	}
	return value, nil
}

func resolveSourceMode(declared, configured string, selection *source.CaptureRequest) error {
	if selection.SourceIntent == "" {
		selection.SourceIntent = declared
	}
	if selection.SourceIntent != "" && selection.SourceIntent != "historical" && selection.SourceIntent != "moving_target" {
		return sec.ErrInvalid
	}
	if selection.SourceMode != "" {
		if selection.SourceMode != "pinned" && selection.SourceMode != "current" {
			return sec.ErrInvalid
		}
		return nil
	}
	selection.SourceMode = configured
	if declared == "historical" || selection.SourceIntent == "historical" || selection.SourceMode == "" {
		selection.SourceMode = "pinned"
	}
	if selection.SourceMode != "pinned" && selection.SourceMode != "current" {
		return sec.ErrInvalid
	}
	return nil
}

// Default selection follows exact authored evidence only when every source can
// be linked unambiguously to an approved mapping. Unavailable/ambiguous targets
// never cause a broader root walk, a partial verified set or current fallback.
// Explicit selections can use the existing retained-file chooser instead.
func defaultSelections(tx app.WriteTx, project, version, mode, provider string) ([]source.CaptureSelection, error) {
	mappings, e := source.Mappings(tx, project)
	if e != nil {
		return nil, e
	}
	sources, e := tx.VersionSources(version)
	if e != nil {
		return nil, e
	}
	identities := map[string]struct {
		Kind     string `json:"kind"`
		RepoID   string `json:"repo_id"`
		Revision string `json:"exact_revision"`
	}{}
	for _, row := range sources {
		identity := identities[row.ID]
		if json.Unmarshal(row.Metadata, &identity) != nil {
			return nil, domain.ErrContentIntegrity
		}
		identities[row.ID] = identity
	}
	evidence, e := tx.VersionEvidence(version)
	if e != nil {
		return nil, e
	}
	selected := map[string]source.CaptureSelection{}
	paths := map[string]map[string]bool{}
	for _, row := range evidence {
		identity, found := identities[row.SourceID]
		if !found || identity.Kind != "git" || identity.Revision == "" {
			return nil, nil
		}
		mapping, e := source.LinkGit(mappings, identity.RepoID)
		if e != nil {
			return nil, nil
		}
		if _, e = app.ManagedSourcePolicy(tx, project, mapping.SourceID, provider); e != nil {
			return nil, e
		}
		var metadata struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(row.Metadata, &metadata) != nil || domain.ValidatePayloadPath(metadata.Path) != nil {
			return nil, nil
		}
		selection, exists := selected[mapping.SourceID]
		revision := ""
		if mode == "pinned" {
			revision = identity.Revision
		}
		if exists && selection.Revision != revision {
			return nil, nil
		}
		selection.SourceID, selection.Revision = mapping.SourceID, revision
		selected[mapping.SourceID] = selection
		if paths[mapping.SourceID] == nil {
			paths[mapping.SourceID] = map[string]bool{}
		}
		paths[mapping.SourceID][metadata.Path] = true
	}
	ids := []string{}
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := []source.CaptureSelection{}
	for _, id := range ids {
		selection := selected[id]
		ordered := []string{}
		for path := range paths[id] {
			ordered = append(ordered, path)
		}
		sort.Strings(ordered)
		for _, path := range ordered {
			selection.Paths = append(selection.Paths, source.CapturePath{Path: path})
		}
		out = append(out, selection)
	}
	return out, nil
}
