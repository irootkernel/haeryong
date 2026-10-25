// Package work owns the single serial local dispatcher. It never launches a
// second queue or concurrent processor; each derived turn yields a bounded batch
// or one page to existing import/seal work.
package work

import (
	"context"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"time"
)

type Processor interface {
	Process(context.Context, string) error
}
type Maintenance interface {
	Processor
	Sweep(context.Context) (bool, error)
}
type Dispatcher struct {
	Store   app.Store
	Imports Processor
	Derived Maintenance
	Purge   *Purger
	Copies  Processor
}

func (d Dispatcher) Run(ctx context.Context) (result error) {
	// Optional only for the copy adapter. Existing import/derived/purge routing
	// remains serial; the dispatcher owns the one copy phase through shutdown.
	phases, _ := d.Copies.(interface {
		PollCopy(context.Context) error
		StopCopies() error
	})
	if phases != nil {
		defer func() { result = errors.Join(result, phases.StopCopies()) }()
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	swept := false
	for {
		if ctx.Err() != nil {
			return nil
		}
		if phases != nil {
			if err := phases.PollCopy(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
		if !swept && d.Derived != nil {
			var err error
			swept, err = d.Derived.Sweep(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
		var jobs []domain.Job
		err := d.Store.Write(ctx, func(tx app.WriteTx) error { var err error; jobs, err = tx.WorkJobs(64); return err })
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		for _, j := range jobs {
			if ctx.Err() != nil {
				return nil
			}
			if (j.Kind == "backup" || j.Kind == "export") && d.Copies != nil {
				_ = d.Copies.Process(ctx, j.ID)
			} else if j.Kind == "lifecycle" && d.Purge != nil {
				_ = d.Purge.Process(ctx, j.ID)
			} else if j.Kind == "derived_index" {
				if d.Derived != nil {
					_ = d.Derived.Process(ctx, j.ID)
				}
			} else {
				_ = d.Imports.Process(ctx, j.ID)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
