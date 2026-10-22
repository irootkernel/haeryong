package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"haeryong.local/haeryong/internal/application/search"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// One immutable completed snapshot, bounded to the last selection. A
// Store generation fences the selected visible corpus; authority is fully checked
// before lookup, and the stamp is rechecked before a cached result is used.
type searchSnapshotCache struct {
	stamp, key string
	corpus     []domain.SearchVersionState
}

func (s *Service) cachedSearchSnapshot(stamp, key string) []domain.SearchVersionState {
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	c := s.searchSnapshotCache
	if c != nil && c.stamp == stamp && c.key == key {
		return c.corpus
	}
	return nil
}
func (s *Service) rememberSearchSnapshot(stamp, key string, corpus []domain.SearchVersionState) {
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	s.searchSnapshotCache = &searchSnapshotCache{stamp, key, corpus}
}

type searchAuthority struct {
	record    domain.Session
	stamp     string
	selection domain.SearchSelection
}

func searchFailure(err error) error {
	if err == nil || errors.Is(err, errSearchChanged) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrDenied) || errors.Is(err, sec.ErrRequired) || errors.Is(err, sec.ErrRevoked) || errors.Is(err, sec.ErrExpired) {
		return err
	}
	var unavailable *SearchUnavailable
	if errors.As(err, &unavailable) {
		return err
	}
	return &SearchUnavailable{err}
}

func (s *Service) searchSliceRead(ctx context.Context, token string, o SearchOptions, a searchAuthority, fn func(app.WriteTx) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error {
		current, err := s.Auth.SearchAuthorityCurrent(tx, a.record, a.stamp)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		if !current {
			record, e := s.Auth.AuthenticateRequestWrite(ctx, tx, token, domain.Request{Method: "GET", Target: "/api/v1/search"})
			if e != nil {
				return e
			}
			if err = searchProject(tx, record, o); err != nil {
				return err
			}
			return errSearchChanged
		}
		return fn(tx)
	})
}

// No SQL cursor or transaction survives a slice. A Store visibility generation
// brackets all pages; the final visible binding excludes unrelated hidden writes.
func (s *Service) searchSnapshot(ctx context.Context, token string, o SearchOptions, q search.Query) (corpus []domain.SearchVersionState, binding string, highWater int64, authority searchAuthority, err error) {
	var library domain.Library
	var stamp string
	err = s.searchRead(ctx, token, o, func(tx app.WriteTx, record domain.Session) error {
		authority.record = record
		authority.selection = domain.SearchSelection{Scope: record.Scope, Project: o.Project, Lifecycle: o.Lifecycle, Versions: o.Versions, Kind: o.Kind, VersionSet: map[string]bool{}}
		for _, id := range record.Scope.VersionIDs {
			authority.selection.VersionSet[id] = true
		}
		var e error
		authority.stamp, e = tx.SearchAuthorityStamp(record)
		if e != nil {
			return e
		}
		authority.selection.FenceKey = record.ID + authority.stamp
		stamp, e = tx.SearchReadStamp(authority.selection)
		if e != nil {
			return e
		}
		corpus, e = tx.SearchCorpus(authority.selection)
		if e != nil {
			return e
		}
		library, e = tx.Library()
		if e != nil {
			return e
		}
		highWater, e = tx.SearchHighWater()
		return e
	})
	if err != nil {
		err = searchFailure(err)
		return
	}
	// The key is encoded outside the transaction. Query text/page size/kind
	// affect ranking and cursor binding, not corpus eligibility or coverage.
	cacheKey := validator([]any{library.ID, library.RuntimeGeneration, library.AuthEpoch, library.StreamEpoch, authority.record.PrincipalID, authority.record.Scope, o.Project, o.Lifecycle, o.Versions})
	check := func() error {
		return searchFailure(s.searchSliceRead(ctx, token, o, authority, func(tx app.WriteTx) error {
			current, e := tx.SearchReadStamp(authority.selection)
			if e != nil {
				return e
			}
			if current != stamp {
				return errSearchChanged
			}
			return nil
		}))
	}
	if cached := s.cachedSearchSnapshot(stamp, cacheKey); cached != nil {
		corpus = cached
		binding = visibleSearchState(library, authority.record.PrincipalID, q, o, corpus, searchCoverage(corpus))
		err = check()
		return
	}
	sliceRead := func(stage string, after int64) (domain.SearchSnapshotSlice, error) {
		var page domain.SearchSnapshotSlice
		e := s.searchSliceRead(ctx, token, o, authority, func(tx app.WriteTx) error {
			current, e := tx.SearchReadStamp(authority.selection)
			if e != nil {
				return e
			}
			if current != stamp {
				return errSearchChanged
			}
			page, e = tx.SearchSnapshotSlice(authority.selection, stage, after)
			return e
		})
		return page, searchFailure(e)
	}
	walk := func(stage string, after int64, consume func(domain.SearchSnapshotSlice) error) error {
		for {
			page, e := sliceRead(stage, after)
			if e != nil {
				return e
			}
			if e = consume(page); e != nil {
				return e
			}
			if e = ctx.Err(); e != nil {
				return searchFailure(e)
			}
			if page.After <= after {
				return nil
			}
			after = page.After
		}
	}
	after := int64(0)
	for _, v := range corpus {
		if v.SnapshotRow > after {
			after = v.SnapshotRow
		}
	}
	err = walk("versions", after, func(p domain.SearchSnapshotSlice) error { corpus = append(corpus, p.Versions...); return nil })
	if err != nil {
		return
	}
	byVersion := map[string]*domain.SearchVersionState{}
	bySet := map[string][]*domain.SearchVersionState{}
	pages := map[string]map[string]bool{}
	units := map[int64]domain.SearchUnit{}
	facts := map[string][][]string{}
	metadata := map[string][]string{}
	for i := range corpus {
		v := &corpus[i]
		byVersion[v.ID] = v
		bySet[v.ContentSetID] = append(bySet[v.ContentSetID], v)
		pages[v.ID] = map[string]bool{}
	}
	err = walk("pages", 0, func(p domain.SearchSnapshotSlice) error {
		for _, row := range p.Pages {
			if v := byVersion[row.VersionID]; v != nil {
				v.HTMLTotal++
				pages[v.ID][row.Path] = true
			}
		}
		return nil
	})
	if err != nil {
		return
	}
	err = walk("entries", 0, func(p domain.SearchSnapshotSlice) error {
		for _, row := range p.Entries {
			for _, v := range bySet[row.ContentSetID] {
				v.FilesTotal++
				if !pages[v.ID][row.Path] {
					v.Unsupported++
				}
			}
		}
		return nil
	})
	if err != nil {
		return
	}
	// Validate only selected, non-quarantined units, as in the original read view.
	authority.selection.VersionSet = map[string]bool{}
	for _, v := range corpus {
		if v.IndexState != "pending" {
			authority.selection.VersionSet[v.ID] = true
		}
	}
	err = walk("units", 0, func(p domain.SearchSnapshotSlice) error {
		for _, row := range p.Units {
			u := row.Unit
			v := byVersion[u.VersionID]
			if v == nil || v.IndexState == "pending" {
				continue
			}
			if u.State == "failed" || u.BodyState == "failed" {
				reason := u.Reason
				if reason == "" {
					reason = "failed_unknown"
				}
				v.UnitReasons = append(v.UnitReasons, reason)
			}
			if u.Kind == "page" && u.BodyState == "failed" {
				v.HTMLFailed++
			}
			u.State, u.BodyState, u.AnchorState = row.ReadState, row.ReadBodyState, row.ReadAnchorState
			units[u.ID] = u
			if u.State != "ready" {
				continue
			}
			if u.Kind == "page" || u.Kind == "attachment" {
				v.FilesDiscovered++
			}
			if u.Kind == "page" && u.BodyState == "complete" {
				v.HTMLComplete++
			}
			facts[v.ID] = append(facts[v.ID], []string{u.Kind, u.PagePath, u.RegionID, u.SourceID, u.Title, u.BodyState, u.AnchorState})
		}
		return nil
	})
	if err != nil {
		return
	}
	err = walk("chunks", 0, func(p domain.SearchSnapshotSlice) error {
		for _, c := range p.Chunks {
			u, ok := units[c.UnitID]
			if !ok {
				continue
			}
			if c.Part != "metadata" && c.Part != "body" {
				return &SearchUnavailable{domain.ErrUnsupportedDerivedData}
			}
			if c.Part != "metadata" || u.State != "ready" {
				continue
			}
			raw, e := json.Marshal([]any{u.Kind, u.PagePath, u.RegionID, u.SourceID, c.Ordinal, c.Title, c.Path, c.Terms, c.Body, c.DisplayBody})
			if e != nil {
				return e
			}
			metadata[u.VersionID] = append(metadata[u.VersionID], string(raw))
		}
		return nil
	})
	if err != nil {
		return
	}
	// Encode/hash outside the lock, including metadata from arbitrarily many
	// windows of one version. No per-version aggregate is hidden in a SQL row.
	for i := range corpus {
		if e := ctx.Err(); e != nil {
			err = searchFailure(e)
			return
		}
		v := &corpus[i]
		rows := facts[v.ID]
		if rows == nil {
			rows = [][]string{}
		}
		sort.Slice(rows, func(i, j int) bool {
			for k := 0; k < 4; k++ {
				if rows[i][k] != rows[j][k] {
					return rows[i][k] < rows[j][k]
				}
			}
			return false
		})
		raw, e := json.Marshal(rows)
		if e != nil {
			err = e
			return
		}
		v.VisibleUnits = string(raw)
		sort.Strings(metadata[v.ID])
		h := sha256.New()
		for _, row := range metadata[v.ID] {
			if e := ctx.Err(); e != nil {
				err = searchFailure(e)
				return
			}
			h.Write([]byte(row))
			h.Write([]byte{'\n'})
		}
		v.VisibleMetadata = hex.EncodeToString(h.Sum(nil))
	}
	sort.Slice(corpus, func(i, j int) bool {
		a, b := corpus[i], corpus[j]
		if a.ProjectID != b.ProjectID {
			return a.ProjectID < b.ProjectID
		}
		if a.DocumentID != b.DocumentID {
			return a.DocumentID < b.DocumentID
		}
		if a.Sequence != b.Sequence {
			return a.Sequence > b.Sequence
		}
		return a.ID < b.ID
	})
	binding = visibleSearchState(library, authority.record.PrincipalID, q, o, corpus, searchCoverage(corpus))
	// The final check also covers time spent hashing outside the lock.
	err = check()
	if err == nil {
		s.rememberSearchSnapshot(stamp, cacheKey, corpus)
	}
	return
}
