package work

import (
	"context"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"time"
)

type Collector interface {
	Collect(context.Context, domain.Object) error
}

// Purger is part of the serial local dispatcher. Its durable intent, rather
// than a bearer or a fresh confirmation, authorizes each guarded cleanup turn.
type Purger struct {
	Store app.Store
	Bytes Collector
	Now   func() time.Time
}

func (p *Purger) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}
func (p *Purger) Process(ctx context.Context, id string) error {
	var j domain.Job
	var intent domain.PurgeIntent
	err := p.Store.Write(ctx, func(tx app.WriteTx) error {
		var e error
		j, e = tx.Job(id)
		if e != nil {
			return e
		}
		if j.State == "completed" || j.State == "failed" || j.State == "cancelled" {
			return domain.ErrTransition
		}
		intent, e = tx.PurgeIntent(id)
		if e != nil {
			return e
		}
		if j.State == "paused" {
			due, e := time.Parse(time.RFC3339Nano, intent.NextRetry)
			if e == nil && due.After(p.now()) && due.Before(p.now().Add(61*time.Second)) {
				return nil
			}
		}
		j, e = tx.ContinuePurge(id, j.Revision, j.AttemptGeneration)
		return e
	})
	if err != nil {
		if errors.Is(err, domain.ErrPrecondition) {
			return p.pause(ctx, j, intent, "guard_blocked")
		}
		return err
	}
	if j.State != "running" {
		return nil
	}
	done := false
	err = p.Store.Write(ctx, func(tx app.WriteTx) error { var e error; done, e = tx.PurgeStep(id, j.AttemptGeneration, 64); return e })
	if err != nil {
		reason := "io_retry"
		if errors.Is(err, domain.ErrPrecondition) {
			reason = "guard_blocked"
		}
		return p.pause(ctx, j, intent, reason)
	}
	if !done {
		return nil
	}
	var candidates []domain.Object
	if err = p.Store.Write(ctx, func(tx app.WriteTx) error { var e error; candidates, e = tx.PurgeCandidates(id, 64); return e }); err != nil {
		return err
	}
	reason := "protected"
	for _, object := range candidates {
		err = p.Bytes.Collect(ctx, object)
		if err == nil || errors.Is(err, domain.ErrNotFound) {
			if err = p.Store.Write(ctx, func(tx app.WriteTx) error { return tx.PurgeCandidateDone(id, object.Hash) }); err != nil {
				return err
			}
		} else if !errors.Is(err, domain.ErrPinned) {
			reason = "io_retry"
		}
	}
	return p.Store.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.PurgeCandidates(id, 1)
		if e != nil {
			return e
		}
		current, e := tx.PurgeIntent(id)
		if e != nil {
			return e
		}
		if len(rows) > 0 || len(current.RetainedSets) > 0 {
			return tx.PausePurge(id, j.AttemptGeneration, reason, p.retryAt(intent))
		}
		return tx.FinishPurge(id, j.AttemptGeneration)
	})
}
func (p *Purger) retryAt(i domain.PurgeIntent) string {
	n := i.Retry
	if n > 6 {
		n = 6
	}
	wait := time.Second * time.Duration(1<<n)
	if wait > 60*time.Second {
		wait = 60 * time.Second
	}
	return p.now().Add(wait).Format(time.RFC3339Nano)
}
func (p *Purger) pause(ctx context.Context, j domain.Job, i domain.PurgeIntent, reason string) error {
	return p.Store.Write(ctx, func(tx app.WriteTx) error { return tx.PausePurge(j.ID, j.AttemptGeneration, reason, p.retryAt(i)) })
}
