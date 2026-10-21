package query

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"sort"

	appsource "haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// ProjectSources composes the recorded last-observed state of the project's
// configured sources (plan §3): logical identities only — the route never
// resolves a branch, stats a root or reads source. S30/note 4: the values
// derive from observation runs only (verification captures never count):
// _at, last_availability and last_observation_reason from the latest
// per-source observation row; last_observed_revision from the latest row in
// which that source itself was captured, so a partial run's non-null target
// never makes a failed source captured.
func (s *Service) ProjectSources(ctx context.Context, token, projectID string, o Options) (List[map[string]any], error) {
	out := List[map[string]any]{Items: []map[string]any{}}
	err := s.read(ctx, token, "/api/v1/projects/"+projectID+"/sources", func(tx app.WriteTx) error {
		c, limit, _, err := s.cursorStart(ctx, token, tx, "project-sources:"+projectID, o, projectID)
		if err != nil {
			return err
		}
		if _, err := tx.Scope(projectID); err != nil {
			return err
		}
		mappings, err := sourceMappings(tx, projectID)
		if err != nil {
			return err
		}
		statuses, err := tx.LatestObservationBySource(projectID)
		if err != nil {
			return err
		}
		observed := map[string]domain.SourceStatus{}
		for _, status := range statuses {
			observed[status.SourceID] = status
		}
		// G11: deterministic source_id ordering regardless of map order.
		sort.Slice(mappings, func(i, j int) bool { return mappings[i].SourceID < mappings[j].SourceID })
		for _, mapping := range mappings {
			// S31: repo_id is always emitted (null for non-git sources).
			item := map[string]any{"source_id": mapping.SourceID, "kind": mapping.Kind, "repo_id": nil, "retention_mode": mapping.RetentionMode, "provider_ids": mapping.ProviderIDs, "last_observed_revision": nil, "last_observed_at": nil, "last_availability": nil, "last_observation_reason": nil}
			if mapping.RepoID != nil {
				item["repo_id"] = *mapping.RepoID
			}
			if status, ok := observed[mapping.SourceID]; ok {
				if status.LastObservedAt != "" {
					item["last_observed_at"] = status.LastObservedAt
				}
				if status.LastAvailability != "" {
					item["last_availability"] = status.LastAvailability
				}
				if status.LastReason != "" {
					item["last_observation_reason"] = status.LastReason
				}
				if status.LastRevision != "" {
					item["last_observed_revision"] = status.LastRevision
				}
			}
			if c.After != "" && mapping.SourceID <= c.After {
				continue
			}
			out.Items = append(out.Items, item)
		}
		if len(out.Items) > limit {
			next := cursor{Binding: c.Binding, After: out.Items[limit-1]["source_id"].(string)}
			out.NextCursor = cursorNext(next)
			out.Items = out.Items[:limit]
		}
		return nil
	})
	return out, err
}

// Verifications lists the durable check history of one evidence item without
// starting another source read. The cursor binds runtime generation, the
// exact route, the principal's effective scope and the project's permission
// generation (S26); history for an unknown evidence id in a visible version
// is 404, matching the item view (S29).
func (s *Service) Verifications(ctx context.Context, token, versionID, evidenceID string, o Options) (List[map[string]any], error) {
	out := List[map[string]any]{Items: []map[string]any{}}
	err := s.read(ctx, token, "/api/v1/versions/"+versionID+"/source-evidence-verifications", func(tx app.WriteTx) error {
		v, err := visibleVersion(tx, versionID)
		if err != nil {
			return err
		}
		d, err := tx.QueryDocument(v.DocumentID)
		if err != nil {
			return err
		}
		c, limit, _, err := s.cursorStart(ctx, token, tx, "verifications:"+versionID+":"+evidenceID, o, d.ProjectID)
		if err != nil {
			return err
		}
		// S29: the evidence item must exist in this visible version.
		if _, err := tx.QueryEvidence(versionID, evidenceID); err != nil {
			return err
		}
		rows, err := tx.Verifications(versionID, evidenceID, c.Key, c.After, limit+1)
		if err != nil {
			return err
		}
		for i, row := range rows {
			if i == limit {
				last := rows[i-1]
				next := cursor{Binding: c.Binding, Key: last.CheckedAt, After: last.ID}
				out.NextCursor = cursorNext(next)
				break
			}
			item := map[string]any{"verification_id": row.ID, "outcome": row.Outcome, "checked_at": row.CheckedAt, "verified_at": nil}
			if row.VerifiedAt != "" {
				item["verified_at"] = row.VerifiedAt
			}
			if row.Reason != "" {
				item["reason"] = row.Reason
			}
			out.Items = append(out.Items, item)
		}
		return nil
	})
	return out, err
}

// evidenceState is the §3.1 mutually exclusive state table, decided after
// authorization by source kind and actual retained-byte availability.
type evidenceState struct {
	availability string
	reason       string
	bytesOrigin  *string
	excerptSHA   string
	excerpt      string
	read         bool
}

// composeEvidence runs phase 1 (ledger facts inside one transaction) and
// phase 2 (optional retained-byte read outside the lock) of the item view.
func (s *Service) composeEvidence(ctx context.Context, token, versionID, evidenceID string) (map[string]any, error) {
	var view map[string]any
	authorization := ""
	state := evidenceState{}
	var authored, source json.RawMessage
	var verification domain.CitationVerification
	hasVerification := false
	var entry domain.Entry
	var permissionsAtRead int64
	err := s.read(ctx, token, "/api/v1/versions/"+versionID+"/source-evidence/"+evidenceID, func(tx app.WriteTx) error {
		record, err := s.Auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return err
		}
		// S7: one shared eligibility helper for every reader (plan §4); a
		// missing source.read stays state 1 denied, not a rejected view.
		el, err := appsource.Check(tx, record, versionID)
		if err != nil {
			return err
		}
		if !el.MayReadSource() {
			authorization = "denied"
		}
		e, err := tx.QueryEvidence(versionID, evidenceID)
		if err != nil {
			return err
		}
		sourceRow, err := tx.QuerySource(versionID, e.SourceID)
		if err != nil {
			return err
		}
		authored, source = json.RawMessage(e.Metadata), json.RawMessage(sourceRow.Metadata)
		verification, err = tx.LatestVerification(versionID, evidenceID)
		hasVerification = err == nil
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		var identity struct {
			Kind          string `json:"kind"`
			RetentionMode string `json:"retention_mode"`
			RepoID        string `json:"repo_id"`
			AssetPath     string `json:"asset_path"`
		}
		if json.Unmarshal(sourceRow.Metadata, &identity) != nil {
			return &Error{"invalid_source_metadata"}
		}
		retainedDefined := identity.Kind == "snapshot" || identity.Kind == "git" && identity.RetentionMode == "selected_snapshot"
		if retainedDefined && identity.AssetPath != "" {
			row, entryErr := tx.QueryEntry(el.Version.ContentSetID, identity.AssetPath)
			switch {
			case entryErr == nil:
				entry = row
				state.read = true
			case errors.Is(entryErr, domain.ErrNotFound):
				// D6: retained bytes defined but the asset entry is absent
				// is the explicit state-3a fact.
				state.availability, state.reason = "unavailable", "missing_retained_bytes"
			default:
				return entryErr
			}
		}
		if scopeState, err := tx.Scope(el.ProjectID); err == nil {
			permissionsAtRead = scopeState.Project.PermissionsRevision
		}
		if !state.read && state.reason == "" {
			// Git-kind without readable retained bytes: recorded facts only.
			if identity.Kind == "git" {
				mappings, err := sourceMappings(tx, el.ProjectID)
				if err != nil {
					return err
				}
				registered := ""
				for _, m := range mappings {
					if m.Kind == "git" && m.RepoID != nil && *m.RepoID == identity.RepoID {
						registered = m.SourceID
					}
				}
				if registered == "" {
					state.availability, state.reason = "unavailable", "source_not_registered"
					return nil
				}
				if observation, err := tx.LatestEvidenceObservation(versionID, registered); err == nil && observation.Availability == "unavailable" {
					state.availability, state.reason = "unavailable", "missing_source"
					return nil
				}
			}
			state.availability, state.reason = "unavailable", "not_read_by_view"
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if authorization == "denied" {
		return map[string]any{"version_id": versionID, "evidence_id": evidenceID, "authored": authored, "source": source, "availability": "denied", "bytes_origin": nil, "reason": nil, "service_verification": serviceVerification(verification, hasVerification), "verifications_url": verificationsURL(versionID, evidenceID)}, nil
	}
	if state.read {
		// State 2/3a/3b: retained bytes decide availability; the digest
		// verification failure is the corrupt path and never yields payload.
		start, end := evidenceLineBounds(authored)
		result, readErr := s.readBounded(ctx, entry, start, end)
		// G3: the bytes were read outside the lock, so current authority is
		// rechecked for EVERY read outcome — the recheck's 401/404 and the
		// state-1 denied take precedence over any retained-read error.
		recheck := s.recheckEvidenceAuthority(ctx, token, versionID, permissionsAtRead)
		if recheck.err != nil {
			// S8: database errors propagate; session/visibility losses
			// return the matching error.
			return nil, recheck.err
		}
		if recheck.denied {
			return map[string]any{"version_id": versionID, "evidence_id": evidenceID, "authored": authored, "source": source, "availability": "denied", "bytes_origin": nil, "reason": nil, "service_verification": serviceVerification(verification, hasVerification), "verifications_url": verificationsURL(versionID, evidenceID)}, nil
		}
		switch {
		case readErr == nil && recheck.permissionMoved:
			// Still eligible but the permission generation moved: the
			// view stays honest without a fresh read (S8).
			state.availability, state.reason = "unavailable", "not_read_by_view"
		case readErr == nil:
			state.availability = "available"
			origin := "retained_snapshot"
			state.bytesOrigin = &origin
			state.excerptSHA = result.SHA
			if result.Bounded <= maxViewExcerpt {
				state.excerpt = base64.StdEncoding.EncodeToString(result.Buffered)
			}
		case errors.Is(readErr, appsource.ErrLineRangeOutOfBounds):
			// S12/G3: an authored range past the retained bytes is its own
			// unavailable fact.
			state.availability, state.reason = "unavailable", "line_range_out_of_bounds"
		case errors.Is(readErr, domain.ErrContentIntegrity):
			state.availability, state.reason = "unavailable", "retained_bytes_corrupt"
		case errors.Is(readErr, domain.ErrNotFound) || errors.Is(readErr, domain.ErrDeleting) || isNotExist(readErr):
			state.availability, state.reason = "unavailable", "missing_retained_bytes"
		default:
			// G3: other errors (I/O, infrastructure) propagate — never a
			// false source fact.
			return nil, readErr
		}
	}
	view = map[string]any{"version_id": versionID, "evidence_id": evidenceID, "authored": authored, "source": source, "availability": state.availability, "bytes_origin": state.bytesOrigin, "reason": nilReason(state.reason), "service_verification": serviceVerification(verification, hasVerification), "verifications_url": verificationsURL(versionID, evidenceID)}
	if state.excerptSHA != "" {
		view["excerpt"] = map[string]any{"bytes_sha256": state.excerptSHA}
		if state.excerpt != "" {
			view["excerpt"].(map[string]any)["data_base64"] = state.excerpt
		}
	}
	return view, nil
}

type evidenceAuthority struct {
	err             error
	denied          bool
	permissionMoved bool
}

// recheckEvidenceAuthority rechecks the reader after the unlocked read (S8):
// invalid sessions and invisible versions return their errors; a lost
// source.read is state 1; a moved permission generation only withholds the
// excerpt; database errors propagate.
func (s *Service) recheckEvidenceAuthority(ctx context.Context, token, versionID string, permissionsAtRead int64) evidenceAuthority {
	recheck := evidenceAuthority{}
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		record, err := s.Auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			recheck.err = err
			return nil
		}
		el, err := appsource.Check(tx, record, versionID)
		if err != nil {
			recheck.err = err
			return nil
		}
		if !el.MayReadSource() {
			recheck.denied = true
			return nil
		}
		scopeState, err := tx.Scope(el.ProjectID)
		if err != nil {
			recheck.err = err
			return nil
		}
		recheck.permissionMoved = scopeState.Project.PermissionsRevision != permissionsAtRead
		return nil
	})
	if err != nil {
		recheck.err = err
	}
	return recheck
}

const maxViewExcerpt = 64 * 1024

func nilReason(reason string) any {
	if reason == "" {
		return nil
	}
	return reason
}

func hasAction(actions []string, action string) bool {
	for _, value := range actions {
		if value == action {
			return true
		}
	}
	return false
}

func evidenceLineBounds(authored json.RawMessage) (int64, int64) {
	var meta struct {
		LineStart int64 `json:"line_start"`
		LineEnd   int64 `json:"line_end"`
	}
	_ = json.Unmarshal(authored, &meta)
	return meta.LineStart, meta.LineEnd
}

func (s *Service) readBounded(ctx context.Context, entry domain.Entry, start, end int64) (appsource.BoundedResult, error) {
	var result appsource.BoundedResult
	err := s.Bytes.Read(ctx, domain.Object{Hash: entry.Hash, Size: entry.Size, State: "ready"}, domain.Pin{Kind: "evidence_view", OwnerID: entry.ContentSetID + ":" + entry.Path}, func(reader io.ReadSeeker) error {
		var err error
		result, err = appsource.SelectBounded(reader, start, end, maxViewExcerpt)
		return err
	})
	return result, err
}

func serviceVerification(v domain.CitationVerification, present bool) any {
	if !present {
		return nil
	}
	out := map[string]any{"verification_id": v.ID, "outcome": v.Outcome, "checked_at": v.CheckedAt, "verified_at": nil}
	if v.VerifiedAt != "" {
		out["verified_at"] = v.VerifiedAt
	}
	if v.Reason != "" {
		out["reason"] = v.Reason
	}
	return out
}

func verificationsURL(versionID, evidenceID string) string {
	return "/api/v1/versions/" + versionID + "/source-evidence-verifications?evidence_id=" + url.QueryEscape(evidenceID)
}

type mappingRow struct {
	SourceID      string   `json:"source_id"`
	Kind          string   `json:"kind"`
	RepoID        *string  `json:"repo_id"`
	RetentionMode string   `json:"retention_mode"`
	ProviderIDs   []string `json:"provider_ids"`
}

func sourceMappings(tx app.WriteTx, projectID string) ([]mappingRow, error) {
	configuration, err := tx.Configuration()
	if err != nil {
		return nil, err
	}
	var value struct {
		Projects map[string]struct {
			Sources []mappingRow `json:"sources"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(configuration.Value, &value); err != nil {
		return nil, err
	}
	return value.Projects[projectID].Sources, nil
}

func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}
