package source

import (
	"encoding/json"

	app "haeryong.local/haeryong/internal/application/storage"
	sec "haeryong.local/haeryong/internal/security/session"
)

// PinnedSelection builds the version's effective pinned selection in the
// exact shape verification records (verifiedEvidence.requestedSelection):
// when the version's cited evidence resolves to exactly one identity — one
// linked git source at one declared exact revision, or one snapshot — ok
// reports that selection. Several identities, or a git source that does not
// link to exactly one configured mapping, leave no one-source verification
// capture that can match (W3b L4(b); registry ~L151).
func PinnedSelection(tx app.WriteTx, el Eligibility) ([]byte, bool, error) {
	mappings, err := Mappings(tx, el.ProjectID)
	if err != nil {
		return nil, false, err
	}
	sources, err := tx.VersionSources(el.Version.ID)
	if err != nil {
		return nil, false, err
	}
	identities := map[string]sourceIdentity{}
	for _, row := range sources {
		var identity sourceIdentity
		if json.Unmarshal(row.Metadata, &identity) != nil {
			return nil, false, sec.ErrInvalid
		}
		identities[row.ID] = identity
	}
	evidence, err := tx.VersionEvidence(el.Version.ID)
	if err != nil {
		return nil, false, err
	}
	selections := map[string][]byte{}
	for _, row := range evidence {
		identity, ok := identities[row.SourceID]
		if !ok {
			return nil, false, nil
		}
		ve := verifiedEvidence{identity: identity}
		switch identity.Kind {
		case "git":
			mapping, err := LinkGit(mappings, identity.RepoID)
			if err != nil {
				return nil, false, nil
			}
			ve.mappingSourceID = mapping.SourceID
		case "snapshot":
		default:
			return nil, false, nil
		}
		selection := ve.requestedSelection(el.ProjectID)
		selections[string(selection)] = selection
	}
	if len(selections) != 1 {
		return nil, false, nil
	}
	for _, selection := range selections {
		return selection, true, nil
	}
	return nil, false, nil
}
