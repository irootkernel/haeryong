package recovery

import (
	"context"
	"encoding/json"
	"errors"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// One dispatcher-owned turn, not a queue or autonomous worker. Only the serial
// dispatcher schedules and collects it. API submission only observes ownership.
type copyPhase struct {
	set        domain.CopySet
	job        domain.Job
	cancel     context.CancelFunc
	result     error
	stopReason error
	drained    bool
	done       chan error
}

func (s *Service) Process(ctx context.Context, id string) error {
	if s.active.Load() != nil {
		return nil
	}
	var set domain.CopySet
	var job domain.Job
	admitted := false
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		var e error
		job, e = tx.Job(id)
		if e != nil {
			return e
		}
		var scope struct {
			ID string `json:"copy_set_id"`
		}
		if json.Unmarshal(job.Scope, &scope) != nil || scope.ID == "" {
			return domain.ErrInvalidTarget
		}
		set, e = tx.CopySet(scope.ID)
		if e != nil {
			return e
		}
		if set.JobID != job.ID || set.Stage == "complete" || set.Stage == "abandoned" || set.Stage == "expired" || set.Stage == "restored_paused" {
			return domain.ErrStaleWorker
		}
		if job.State == "cancelled" || job.State == "failed" || job.State == "completed" {
			return domain.ErrStaleWorker
		}
		if _, e = app.CopyOwner(tx, set, job.ID, job.AttemptGeneration); e != nil {
			return e
		}
		if job.CancelRequested {
			return tx.ConfirmStop(job.ID, job.AttemptGeneration)
		}
		if job.State == "interrupted" {
			if e = tx.ReconcileJob(id); e != nil {
				return e
			}
			job, e = tx.Job(id)
			if e != nil {
				return e
			}
		}
		if job.State == "queued" {
			job, e = tx.ClaimJob(id)
			if e != nil {
				return e
			}
		}
		// Capture the actual claimed generation, never the queued WorkJobs value.
		if e = app.GuardCopy(tx, set, job.ID, job.AttemptGeneration); e != nil {
			if errors.Is(e, domain.ErrDenied) || errors.Is(e, domain.ErrReconciliation) {
				return app.FailCopy(tx, set, job.ID, job.AttemptGeneration, copyStopCode(e))
			}
			return e
		}
		admitted = true
		return nil
	})
	if e != nil {
		return e
	}
	if !admitted {
		return nil
	}
	turn, cancel := context.WithCancel(ctx)
	phase := &copyPhase{set: set, job: job, cancel: cancel, done: make(chan error, 1)}
	s.active.Store(phase)
	go func() { phase.done <- s.Bytes.ProcessCopy(turn, id); close(phase.done) }()
	return nil
}

func (s *Service) collect(phase *copyPhase) error {
	// The completion channel is sent only after ProcessCopy's owned defers close
	// every handle. A terminal DB state alone is never used as drain evidence.
	phase.cancel()
	e := s.Store.Write(context.Background(), func(tx app.WriteTx) error {
		set, e := tx.CopySet(phase.set.ID)
		if e != nil {
			return e
		}
		j, e := tx.Job(phase.job.ID)
		if e != nil {
			return e
		}
		if set.JobID != phase.job.ID || set.Stage == "complete" || set.Stage == "abandoned" || set.Stage == "expired" || set.Stage == "restored_paused" {
			return nil
		}
		if j.State == "completed" || j.State == "failed" || j.State == "cancelled" {
			return nil
		}
		// Cancellation advances generation exactly once. Only that same owned
		// attempt may be confirmed after this completion channel proves drain.
		if j.CancelRequested && j.AttemptGeneration == phase.job.AttemptGeneration+1 {
			if _, e = app.CopyOwner(tx, phase.set, phase.job.ID, j.AttemptGeneration); e != nil {
				if errors.Is(e, domain.ErrStaleWorker) || errors.Is(e, domain.ErrReconciliation) {
					return nil
				}
				return e
			}
			return tx.ConfirmStop(j.ID, j.AttemptGeneration)
		}
		cause := phase.stopReason
		if cause == nil && !errors.Is(phase.result, context.Canceled) && !errors.Is(phase.result, domain.ErrStaleWorker) {
			cause = phase.result
		}
		if cause != nil {
			e = app.FailCopy(tx, phase.set, phase.job.ID, phase.job.AttemptGeneration, copyStopCode(cause))
			if errors.Is(e, domain.ErrStaleWorker) || errors.Is(e, domain.ErrReconciliation) {
				return nil
			}
			return e
		}
		return nil
	})
	if e == nil {
		s.active.Store(nil)
	}
	return e
}
func (s *Service) PollCopy(ctx context.Context) error {
	phase := s.active.Load()
	if phase == nil {
		return nil
	}
	if phase.drained {
		return s.collect(phase)
	}
	select {
	case phase.result = <-phase.done:
		phase.drained = true
		return s.collect(phase)
	default:
	}
	// Poll even when WorkJobs no longer selects this job, including while a
	// completed copy still owns retention verification/deletion handles.
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(phase.job.ID)
		if e != nil {
			return e
		}
		if j.State == "completed" {
			lib, e := tx.Library()
			if e != nil {
				return e
			}
			owner, e := tx.AuthState()
			if e != nil {
				return e
			}
			hold, e := tx.RecoveryHold()
			if e != nil {
				return e
			}
			if lib.ID == phase.set.LibraryID && lib.RuntimeGeneration == phase.set.Runtime && owner.OwnerID == phase.set.OwnerID && j.AttemptGeneration == phase.job.AttemptGeneration && !j.CancelRequested && (hold == nil || hold.State == "reviewed") {
				return nil
			}
		}
		return app.GuardCopy(tx, phase.set, phase.job.ID, phase.job.AttemptGeneration)
	})
	if e != nil {
		phase.stopReason = e
		phase.cancel()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}
func (s *Service) StopCopies() error {
	phase := s.active.Load()
	if phase == nil {
		return nil
	}
	phase.cancel()
	if !phase.drained {
		phase.result = <-phase.done
		phase.drained = true
	}
	result := phase.result
	e := s.collect(phase)
	if errors.Is(result, context.Canceled) || errors.Is(result, domain.ErrStaleWorker) {
		result = nil
	}
	return errors.Join(result, e)
}

func copyStopCode(e error) string {
	if errors.Is(e, domain.ErrDenied) {
		return "copy_authority_changed"
	}
	if errors.Is(e, domain.ErrReconciliation) {
		return "copy_reconciliation_required"
	}
	return "copy_io_failure"
}
