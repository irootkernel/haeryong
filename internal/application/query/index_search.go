package query

import (
	"context"
	"errors"
	"io"
	"reflect"
	"runtime"
	"strings"

	"haeryong.local/haeryong/internal/application/search"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type searchIndexAudit struct {
	checked, missing, unexpected, mismatched int64
	incomplete                               bool
}

type searchChunkShape struct{ metadata, even, odd int }

// Compare required rows from immutable inventory and deterministic extraction,
// then inspect existing rows through selected-version/unit prefix seeks. Every
// store turn is a bounded read; parsing, hashing and spool IO stay detached.
func (s *Service) auditSearchIndex(ctx context.Context, token string, v domain.Version) (out searchIndexAudit, err error) {
	read := func(fn func(app.WriteTx) error) error {
		return s.indexRead(ctx, token, v.ID, func(tx app.WriteTx, _ source.Eligibility) error { return fn(tx) })
	}
	// One small shape per admitted unit, independent of body/chunk count. The
	// revisioned emitters produce contiguous ordinals within each parity.
	expected := map[int64]*searchChunkShape{}
	unit := func(key domain.SearchUnit) (actual domain.SearchUnit, err error) {
		out.checked++
		err = read(func(tx app.WriteTx) error { var e error; actual, e = tx.SearchAuditUnit(key); return e })
		if errors.Is(err, domain.ErrNotFound) {
			out.missing++
			return actual, nil
		}
		if err == nil && actual.InputHash != key.InputHash {
			out.mismatched++
		}
		if err == nil && expected[actual.ID] == nil {
			expected[actual.ID] = &searchChunkShape{-1, -1, -1}
		}
		return actual, err
	}
	posting := func(id int64, part string, ordinal int) error {
		if id == 0 { // A missing parent was already counted.
			return nil
		}
		out.checked++
		var exists bool
		err := read(func(tx app.WriteTx) error { var e error; exists, e = tx.SearchAuditChunk(id, part, ordinal); return e })
		if err == nil && !exists {
			out.missing++
		}
		return err
	}
	chunk := func(id int64, part string, ordinal int) error {
		if shape := expected[id]; shape != nil {
			switch {
			case part == "metadata":
				shape.metadata = max(shape.metadata, ordinal)
			case ordinal%2 == 0:
				shape.even = max(shape.even, ordinal)
			default:
				shape.odd = max(shape.odd, ordinal)
			}
		}
		return posting(id, part, ordinal)
	}
	metadata := func(u domain.SearchUnit, text string) error {
		return search.InspectMetadata(ctx, text, func(n int) error { return chunk(u.ID, "metadata", n) })
	}
	document, err := unit(domain.SearchUnit{VersionID: v.ID, Kind: "document"})
	if err != nil {
		return out, err
	}
	if err = metadata(document, v.Title); err != nil {
		return out, err
	}
	for _, kind := range []string{"entry", "source", "region"} {
		after := ""
		for {
			var items []domain.SearchInventoryItem
			err = read(func(tx app.WriteTx) error {
				var e error
				items, e = tx.SearchInventory(v.ID, kind, after, 64)
				return e
			})
			if err != nil || len(items) == 0 {
				break
			}
			for _, item := range items {
				u, e := unit(item.Unit)
				if e != nil {
					return out, e
				}
				text, e := search.MetadataText(item.Metadata, kind == "source")
				if e != nil {
					return out, e
				}
				if item.Unit.Kind == "page" {
					e = s.auditSearchPage(ctx, read, item.Unit, u, text, unit, chunk, metadata, &out)
				} else {
					if kind == "entry" {
						text = strings.TrimSpace(item.Unit.PagePath + " " + text)
					} else {
						text = strings.Join([]string{item.Unit.PagePath, item.Unit.RegionID, item.Unit.SourceID, text}, " ")
					}
					e = metadata(u, text)
				}
				if e != nil {
					return out, e
				}
				after = item.Key
			}
			runtime.Gosched()
		}
		if err != nil {
			return out, err
		}
	}
	var after *domain.SearchUnit
	for {
		var units []domain.SearchUnit
		err = read(func(tx app.WriteTx) error { var e error; units, e = tx.SearchAuditUnits(v.ID, after, 64); return e })
		if err != nil || len(units) == 0 {
			return out, err
		}
		for _, u := range units {
			out.checked++
			shape := expected[u.ID]
			if shape == nil {
				out.unexpected++
			}
			if u.State != "ready" || oneOf(u.BodyState, "pending", "failed") || u.AnchorState == "pending" {
				out.incomplete = true
			}
			if u.Kind == "page" || u.Kind == "attachment" || u.Kind == "region" && u.InputHash != "" {
				err = read(func(tx app.WriteTx) error {
					entry, e := tx.QueryEntry(v.ContentSetID, u.PagePath)
					if errors.Is(e, domain.ErrNotFound) {
						out.unexpected++
						return nil
					}
					if e == nil && entry.Hash != u.InputHash {
						out.mismatched++
					}
					return e
				})
				if err != nil {
					return out, err
				}
			}
			for _, part := range []string{"metadata", "body"} {
				ordinal := -1
				for {
					var ordinals []int
					err = read(func(tx app.WriteTx) error {
						var e error
						ordinals, e = tx.SearchAuditChunks(u.ID, part, ordinal, 64)
						return e
					})
					if err != nil {
						return out, err
					}
					if len(ordinals) == 0 {
						break
					}
					for _, n := range ordinals {
						required := shape != nil && (part == "metadata" && n <= shape.metadata || part == "body" && (n%2 == 0 && n <= shape.even || n%2 == 1 && n <= shape.odd))
						if !required {
							out.unexpected++
						}
						if err = posting(u.ID, part, n); err != nil {
							return out, err
						}
						ordinal = n
					}
					runtime.Gosched()
				}
			}
			after = &u
		}
		runtime.Gosched()
	}
}

func (s *Service) auditSearchPage(ctx context.Context, read func(func(app.WriteTx) error) error, key, actual domain.SearchUnit, supplied string,
	unit func(domain.SearchUnit) (domain.SearchUnit, error), chunk func(int64, string, int) error, metadata func(domain.SearchUnit, string) error, out *searchIndexAudit) error {
	var object domain.Object
	if err := read(func(tx app.WriteTx) error { var e error; object, e = tx.Object(key.InputHash); return e }); err != nil {
		return err
	}
	regions := map[string]domain.SearchUnit{"": actual}
	regionUnit := func(id string) (domain.SearchUnit, error) {
		if u, ok := regions[id]; ok {
			return u, nil
		}
		region := domain.SearchUnit{VersionID: key.VersionID, Kind: "region", PagePath: key.PagePath, RegionID: id, InputHash: key.InputHash}
		// Supplied metadata retains its own empty input hash/provenance.
		err := read(func(tx app.WriteTx) error {
			_, e := tx.VersionRegion(key.VersionID, key.PagePath, id)
			if e == nil {
				region.InputHash = ""
			} else if errors.Is(e, domain.ErrNotFound) {
				e = nil
			}
			return e
		})
		if err != nil {
			return domain.SearchUnit{}, err
		}
		u, err := unit(region)
		regions[id] = u
		return u, err
	}
	var extracted search.Extraction
	err := s.Bytes.Read(ctx, object, domain.Pin{Kind: "active_read", OwnerID: key.VersionID}, func(reader io.ReadSeeker) error {
		var err error
		extracted, err = search.InspectPage(ctx, reader, func(region string, n int) error {
			u, e := regionUnit(region)
			if e != nil {
				return e
			}
			return chunk(u.ID, "body", n)
		})
		return err
	})
	if err != nil {
		return err
	}
	title := key.Title
	if extracted.Title != "" {
		title = extracted.Title
	} else if supplied != "" {
		title = supplied
	}
	if err = metadata(actual, strings.Join([]string{key.PagePath, supplied, title, extracted.Keywords}, " ")); err != nil {
		return err
	}
	expected := map[string]domain.PageAnchor{}
	for _, a := range extracted.Anchors {
		expected[a.ID] = domain.PageAnchor{VersionID: key.VersionID, PagePath: key.PagePath, ID: a.ID, LocatorKind: a.LocatorKind, Occurrences: a.Occurrences, Label: a.Label, TextSHA256: a.TextSHA256}
		if a.Occurrences == 1 {
			u, e := regionUnit(a.ID)
			if e != nil {
				return e
			}
			if u.InputHash != "" { // Supplied region metadata was checked by inventory.
				if e = metadata(u, strings.Join([]string{key.PagePath, a.ID, a.Label}, " ")); e != nil {
					return e
				}
			}
		}
	}
	after := ""
	for {
		var anchors []domain.PageAnchor
		err = read(func(tx app.WriteTx) error {
			var e error
			anchors, e = tx.SearchAuditAnchors(key.VersionID, key.PagePath, after, 64)
			return e
		})
		if err != nil {
			return err
		}
		if len(anchors) == 0 {
			break
		}
		for _, a := range anchors {
			out.checked++
			want, ok := expected[a.ID]
			if !ok {
				out.unexpected++
			} else if !reflect.DeepEqual(want, a) {
				out.mismatched++
			}
			delete(expected, a.ID)
			after = a.ID
		}
		runtime.Gosched()
	}
	out.missing += int64(len(expected))
	return nil
}
