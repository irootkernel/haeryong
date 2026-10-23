package work

import (
	"context"
	"haeryong.local/haeryong/internal/application/references"
	"haeryong.local/haeryong/internal/application/search"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// Router shares the one dispatcher. Sweep registers search BEFORE references
// for each exact published version, so absent search cannot be a startup race.
type Router struct {
	Store      app.Store
	Search     *search.Worker
	References *references.Worker
	after      string
}

func NewMaintenance(store app.Store, bytes search.ObjectReader) *Router {
	return &Router{Store: store, Search: search.NewWorker(store, bytes), References: references.NewWorker(store, bytes)}
}
func (r *Router) Sweep(ctx context.Context) (bool, error) {
	var versions []string
	err := r.Store.Write(ctx, func(tx app.WriteTx) error { var err error; versions, err = tx.SearchVersions(r.after, 32); return err })
	if err != nil {
		return false, err
	}
	for _, v := range versions {
		if err = r.Store.Write(ctx, func(tx app.WriteTx) error {
			if _, err := tx.EnqueueSearch(v, r.Search.ExtractorRevision); err != nil {
				return err
			}
			_, err := tx.EnqueueReferences(v, r.References.ReferenceRevision)
			return err
		}); err != nil {
			return false, err
		}
		r.after = v
	}
	return len(versions) == 0, nil
}
func (r *Router) Process(ctx context.Context, id string) error {
	var w domain.DerivedWork
	err := r.Store.Write(ctx, func(tx app.WriteTx) error { var err error; w, err = tx.DerivedWork(id); return err })
	if err != nil {
		return err
	}
	switch w.WorkKind {
	case "search":
		return r.Search.Process(ctx, id)
	case "references":
		return r.References.Process(ctx, id)
	default:
		return domain.ErrUnsupportedDerivedData
	}
}
