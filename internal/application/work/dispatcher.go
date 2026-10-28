// Package work owns one serial local dispatcher and ledger. Local batches yield
// between turns; externally waiting copy/provider phases have bounded ownership
// and are collected by that same dispatcher through shutdown.
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
	Store    app.Store
	Imports  Processor
	Derived  Maintenance
	Purge    *Purger
	Copies   Processor
	Provider Processor
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
	providerPhases, _ := d.Provider.(interface {
		ScheduleProvider(context.Context, string) error
		PollProvider(context.Context) error
		StopProviders() error
	})
	if providerPhases != nil {
		defer func() { result = errors.Join(result, providerPhases.StopProviders()) }()
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
		if providerPhases != nil {
			if err := providerPhases.PollProvider(ctx); err != nil {
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
			if domain.ProviderJob(j.Kind) {
				if providerPhases != nil {
					_ = providerPhases.ScheduleProvider(ctx, j.ID)
				} else if d.Provider != nil {
					_ = d.Provider.Process(ctx, j.ID)
				}
			} else if (j.Kind == "backup" || j.Kind == "export") && d.Copies != nil {
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
