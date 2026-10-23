package references

import (
	"context"
	"errors"
	"golang.org/x/text/unicode/norm"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"path"
	"reflect"
	"strings"
	"unicode/utf8"
)

type endpoint struct {
	version                              domain.Version
	project, lifecycle, projectLifecycle string
	page, region                         *string
	anchor                               domain.ReferenceAnchor
}

func endpointBase(tx app.WriteTx, id string) (e endpoint, err error) {
	e.version, err = tx.PublishedVersion(id)
	if err != nil {
		return
	}
	d, err := tx.Document(e.version.DocumentID)
	if err != nil {
		return e, err
	}
	p, err := tx.Scope(d.ProjectID)
	if err != nil {
		return e, err
	}
	e.project = d.ProjectID
	e.lifecycle = d.Lifecycle
	e.projectLifecycle = p.Project.Lifecycle
	if d.Lifecycle == "purged" || p.Project.Lifecycle == "purged" {
		return e, &EndpointError{"page_missing"}
	}
	return e, nil
}
func (s *Worker) endpoint(ctx context.Context, id string, page, region *string) (e endpoint, err error) {
	err = s.Store.Write(ctx, func(tx app.WriteTx) error { var err error; e, err = endpointBase(tx, id); return err })
	if err != nil {
		return
	}
	if page == nil {
		return e, nil
	}
	entry, err := s.lookupEntry(ctx, e.version.ContentSetID, *page)
	if err != nil {
		return e, err
	}
	seen := map[string]bool{}
	for entry.Kind == "symlink" {
		// Match query.ResolveFile: exact-first on every hop, with the same link-text checks.
		if seen[entry.Path] || strings.HasPrefix(entry.LinkText, "/") || !utf8.ValidString(entry.LinkText) || strings.ContainsRune(entry.LinkText, 0) {
			return e, &EndpointError{"page_missing"}
		}
		seen[entry.Path] = true
		target := path.Clean(path.Join(path.Dir(entry.Path), entry.LinkText))
		if target == ".." || strings.HasPrefix(target, "../") {
			return e, &EndpointError{"page_missing"}
		}
		// Query lookup also rejects non-inventory paths (including drive-qualified text).
		if !domain.ValidReferencePath(&target) {
			return e, &EndpointError{"page_missing"}
		}
		entry, err = s.lookupEntry(ctx, e.version.ContentSetID, target)
		if err != nil {
			return e, err
		}
	}
	if entry.Kind != "file" || !domain.IsHTMLPath(entry.Path) {
		return e, &EndpointError{"page_missing"}
	}
	e.page = &entry.Path
	e.region = region
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		var err error
		r := ""
		if region != nil {
			r = *region
		}
		e.anchor, err = tx.ReferenceAnchor(id, entry.Path, r)
		return err
	})
	if err == nil && e.anchor.Reason != "" {
		err = &EndpointError{anchorReason(e.anchor.Reason)}
	}
	return
}
func (s *Worker) lookupEntry(ctx context.Context, set, name string) (entry domain.Entry, err error) {
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		var err error
		entry, err = tx.QueryEntry(set, name)
		return err
	})
	if errors.Is(err, domain.ErrNotFound) {
		// Exact spelling wins. Normalize comparisons outside the mutex and examine
		// every finite batch before accepting the unique equivalent spelling.
		after := ""
		matched := false
		canonical := norm.NFC.String(name)
		for {
			var entries []domain.Entry
			err = s.Store.Write(ctx, func(tx app.WriteTx) error {
				var err error
				entries, err = tx.Entries(set, after, 64)
				return err
			})
			if err != nil {
				return entry, err
			}
			for _, item := range entries {
				after = item.Path
				if norm.NFC.String(item.Path) == canonical {
					if matched {
						return entry, &EndpointError{"ambiguous_path"}
					}
					entry = item
					matched = true
				}
			}
			if len(entries) < 64 {
				break
			}
		}
		if !matched {
			return entry, &EndpointError{"page_missing"}
		}
	} else if err != nil {
		return entry, err
	}
	return entry, nil
}

func (e endpoint) matches(tx app.WriteTx) error {
	current, err := endpointBase(tx, e.version.ID)
	if err != nil {
		return err
	}
	if current.version.DocumentID != e.version.DocumentID || current.version.ContentSetID != e.version.ContentSetID || current.version.ManifestSHA != e.version.ManifestSHA || current.version.PayloadSHA != e.version.PayloadSHA || current.project != e.project || current.lifecycle != e.lifecycle || current.projectLifecycle != e.projectLifecycle {
		return domain.ErrStaleWorker
	}
	if e.page != nil {
		r := ""
		if e.region != nil {
			r = *e.region
		}
		a, err := tx.ReferenceAnchor(e.version.ID, *e.page, r)
		if err != nil {
			return err
		}
		if a != e.anchor {
			return domain.ErrStaleWorker
		}
	}
	return nil
}

type resolution struct {
	source, target endpoint
	declaration    domain.AuthoredReference
	edge           domain.AuthoredEdge
}

func hashPointer(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func (s *Worker) resolve(ctx context.Context, r domain.AuthoredReference) (*resolution, error) {
	// Rejoin the durable declaration before any structural attempt. Besides
	// immutability, this prevents a future caller from resolving an uncommitted row.
	var durable domain.AuthoredReference
	if err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		var err error
		durable, err = tx.AuthoredReference(r.FromVersionID, r.ID)
		return err
	}); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(durable, r) {
		return nil, domain.ErrStaleWorker
	}
	// A stored declaration is immutable; validate its raw/typed representation
	// outside Write before using it for incoming or outgoing maintenance.
	if _, err := app.PrepareAuthoredReference(r); err != nil {
		return nil, err
	}
	source, err := s.endpoint(ctx, r.FromVersionID, r.FromPagePath, r.FromRegionID)
	if errors.Is(err, errUnavailable) || errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if source.lifecycle == "trash" || source.projectLifecycle == "trash" {
		return nil, nil
	}
	target, err := s.endpoint(ctx, r.TargetVersionID, r.TargetPagePath, r.TargetRegionID)
	if errors.Is(err, errUnavailable) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrUnsupportedDerivedData) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if target.version.DocumentID != r.TargetDocumentID {
		return nil, nil
	}
	var library domain.Library
	err = s.Store.Write(ctx, func(tx app.WriteTx) error { var err error; library, err = tx.Library(); return err })
	if err != nil {
		return nil, err
	}
	id, err := domain.ReferenceEdgeID(library.ID, r.FromVersionID, r.ID)
	if err != nil {
		return nil, err
	}
	edge := domain.AuthoredEdge{ID: id, FromVersionID: r.FromVersionID, ReferenceID: r.ID, FromPagePath: source.page, FromRegionID: r.FromRegionID, TargetProjectID: target.project, TargetDocumentID: r.TargetDocumentID, TargetVersionID: r.TargetVersionID, TargetPagePath: target.page, TargetRegionID: r.TargetRegionID, FromPageSHA: hashPointer(source.anchor.PageHash), FromAnchorSHA: hashPointer(source.anchor.AnchorHash), TargetPageSHA: hashPointer(target.anchor.PageHash), TargetAnchorSHA: hashPointer(target.anchor.AnchorHash), AnchorRevision: domain.SearchExtractorRevision}
	return &resolution{source, target, r, edge}, nil
}
func (r *resolution) commit(tx app.WriteTx) error {
	if err := r.source.matches(tx); err != nil {
		return err
	}
	if err := r.target.matches(tx); err != nil {
		if errors.Is(err, errUnavailable) || errors.Is(err, domain.ErrNotFound) {
			return domain.ErrStaleWorker
		}
		return err
	}
	declaration, err := tx.AuthoredReference(r.declaration.FromVersionID, r.declaration.ID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(declaration, r.declaration) {
		return domain.ErrStaleWorker
	}
	m, err := tx.ReferenceMaterialization(r.declaration.FromVersionID)
	if err != nil {
		return err
	}
	if m.ManifestSHA != r.source.version.ManifestSHA {
		return domain.ErrStaleWorker
	}
	err = tx.PutAuthoredEdge(r.edge)
	// Immutable tuple/hash disagreement is anchor_drift. Keep the historical edge
	// untouched; it is a target-resolution result, never source materialization failure.
	if errors.Is(err, domain.ErrConflict) {
		return nil
	}
	return err
}
