package work

import (
	"context"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// One externally waiting turn is owned, scheduled and collected by Dispatcher.
// It shares WorkJobs and the serialized writer; it is not another queue.
type providerPhase struct {
	jobID            string
	generation       int64
	cancel           context.CancelFunc
	done             chan error
	result           error
	drained          bool
	reconcileAttempt string
	runtime          string
	accounted        bool
}

func (p *ProviderCoordinator) ScheduleProvider(ctx context.Context, id string) error {
	if p.active.Load() != nil {
		return nil
	}
	j, e := p.Store.Job(ctx, id)
	if e != nil {
		return e
	}
	if !domain.ProviderJob(j.Kind) {
		return domain.ErrTransition
	}
	generation := j.AttemptGeneration
	turn, cancel := context.WithCancel(ctx)
	phase := &providerPhase{jobID: id, generation: generation, cancel: cancel, done: make(chan error, 1)}
	p.active.Store(phase)
	go func() { phase.done <- p.process(turn, id, phase); close(phase.done) }()
	return nil
}

func (p *ProviderCoordinator) collectProvider(phase *providerPhase) error {
	phase.cancel()
	if phase.accounted {
		p.rememberAccountingFailure(phase.result)
		p.active.Store(nil)
		return nil
	}
	e := p.Store.Write(context.Background(), func(tx app.WriteTx) error {
		j, e := tx.Job(phase.jobID)
		if e != nil {
			return e
		}
		if phase.reconcileAttempt != "" {
			if phase.result != nil {
				return tx.AbandonProviderReconciliation(j.ID, phase.reconcileAttempt, phase.runtime, phase.generation, "provider_accounting_unavailable")
			}
			if e = tx.CompleteProviderReconciliation(j.ID, phase.reconcileAttempt); e != nil {
				return e
			}
		}
		if terminalProviderJob(j.State) {
			return nil
		}
		if j.AttemptGeneration != phase.generation && !(j.CancelRequested && j.AttemptGeneration == phase.generation+1) {
			return nil
		}
		attempts, e := tx.ProviderAttempts(j.ID)
		if e != nil {
			return e
		}
		for _, a := range attempts {
			if a.Generation != phase.generation {
				continue
			}
			if a.State == "dispatching" || a.State == "running" {
				if e = tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "", "", "transport_unknown"); e != nil {
					return e
				}
			}
		}
		if j.CancelRequested {
			if e := tx.ConfirmStop(j.ID, j.AttemptGeneration); e != nil {
				return e
			}
			return settleParentScanAfterChild(tx, j)
		}
		// A successful turn may only normalize or advance a finite lookup set.
		// Keep that intended progress after the unchanged ownership/cancel guards.
		if j.State == "reconciling" && phase.result == nil {
			return nil
		}
		if j.State == "running" || j.State == "reconciling" {
			return tx.PauseProviderJob(j.ID, "reconciliation_required")
		}
		return nil
	})
	if e == nil {
		p.active.Store(nil)
	}
	return e
}

func terminalProviderJob(state string) bool {
	return state == "completed" || state == "failed" || state == "cancelled"
}

func (p *ProviderCoordinator) PollProvider(context.Context) error {
	phase := p.active.Load()
	if phase == nil {
		return nil
	}
	if !phase.drained {
		select {
		case phase.result = <-phase.done:
			phase.drained = true
		default:
			return nil
		}
	}
	return p.collectProvider(phase)
}

func (p *ProviderCoordinator) StopProviders() (result error) {
	defer func() {
		if first := p.accountingFailure.Load(); first != nil && !errors.Is(result, first) {
			if first.restartSafe {
				result = errors.Join(result, &providerShutdownDiagnostic{first})
			} else {
				result = errors.Join(result, first)
			}
		}
		if unsafe := p.accountingUnsafe.Load(); unsafe != nil && !errors.Is(result, unsafe) {
			result = errors.Join(result, unsafe)
		}
	}()
	phase := p.active.Load()
	if phase == nil {
		return nil
	}
	phase.cancel()
	if !phase.drained {
		phase.result = <-phase.done
		phase.drained = true
	}
	result = phase.result
	var accounting *providerAccountingError
	if !errors.As(result, &accounting) && providerStopRefusal(result) {
		result = nil
	}
	return errors.Join(result, p.collectProvider(phase))
}

// Retain the first required accounting failure after collection in bounded memory.
// Its durable job pause remains the operational record; this is shutdown health.
func (p *ProviderCoordinator) rememberAccountingFailure(e error) {
	var accounting *providerAccountingError
	if errors.As(e, &accounting) {
		p.accountingFailure.CompareAndSwap(nil, accounting)
		// A later unsafe failure must not hide behind an earlier safe diagnostic.
		if !accounting.restartSafe {
			p.accountingUnsafe.CompareAndSwap(nil, accounting)
		}
	}
}

// This wrapper is created only for an already collected, durably settled error
// with positive owned-drain evidence. Active Stop errors remain unwrapped.
type providerShutdownDiagnostic struct{ cause *providerAccountingError }

func (e *providerShutdownDiagnostic) Error() string { return e.cause.Error() }
func (e *providerShutdownDiagnostic) Unwrap() error { return e.cause }

// ProviderRestartDiagnostics permits only entire trees of typed historical
// diagnostics. A joined current drain, storage, endpoint or output leaf refuses.
func ProviderRestartDiagnostics(cause error) bool {
	if cause == nil {
		return false
	}
	if _, ok := cause.(*providerShutdownDiagnostic); ok {
		return true
	}
	if many, ok := cause.(interface{ Unwrap() []error }); ok {
		children := many.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, e := range children {
			if !ProviderRestartDiagnostics(e) {
				return false
			}
		}
		return true
	}
	if one, ok := cause.(interface{ Unwrap() error }); ok {
		return ProviderRestartDiagnostics(one.Unwrap())
	}
	return false
}

// Preserve the existing benign set, but require every joined leaf to qualify.
// An independent current I/O error cannot disappear beside cancellation/refusal.
func providerStopRefusal(cause error) bool {
	if cause == nil {
		return false
	}
	if many, ok := cause.(interface{ Unwrap() []error }); ok {
		children := many.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, e := range children {
			if !providerStopRefusal(e) {
				return false
			}
		}
		return true
	}
	if one, ok := cause.(interface{ Unwrap() error }); ok {
		return providerStopRefusal(one.Unwrap())
	}
	return cause == context.Canceled || cause == domain.ErrTransition || ProviderAuthorityRefusal(cause)
}
