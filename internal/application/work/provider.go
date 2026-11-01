package work

import (
	"context"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"sync/atomic"
	"time"
)

// ProviderDriver is supplied by the ordered provider adapter owner. It must
// distinguish a verified backend terminal outcome from local transport shutdown.
// Execute invokes the send fence immediately before the external request and
// acknowledges observed progress durably before its long wait. It returns only
// after owned native processes, readers and handles are drained.
// Reconcile with outputAllowed=false inspects status/IDs only: no source reads
// or output-body retrieval. Context cancellation does not prove remote cessation.
type ProviderDriver interface {
	Execute(context.Context, domain.ProviderAttempt, []domain.ProviderInput, func() error, func(ProviderProgress) error) (ProviderOutcome, error)
	Cancel(context.Context, domain.ProviderAttempt) error
	Reconcile(context.Context, domain.ProviderAttempt, bool) (ProviderOutcome, error)
}

// Progress is acknowledged only after its observed provider identities and exact
// input delivery facts commit. RequestID denotes the provider operation/turn ID;
// local JSON-RPC correlation is not a provider reconciliation identity.
type ProviderProgress struct {
	RequestID, SessionID string
	Observed             []domain.ProviderInput
}
type ProviderOutcome struct {
	// LocalClosed is an explicit producer observation: owned transport was
	// drained, or no owned transport was created. Its zero value is unknown.
	// It carries no backend, source-delivery or complete native audit identity.
	LocalClosed        bool
	LocalCleanupReason string
	Transport          *domain.ProviderTransport
	// CleanupError is independent owned-process uncertainty, even before verified
	// setup permits a complete typed transport audit. It contains no native prose.
	CleanupError                 error
	Lookup                       *domain.ProviderLookup
	State                        string
	RequestID, SessionID, Reason string
	// CompletionObserved means a successful native completed turn, never EOF.
	// Explicit failed/interrupted outcomes use State without this result flag.
	CompletionObserved bool
	Body               io.ReadCloser
	Observed           []domain.ProviderInput
}
type ProviderBytes interface {
	Put(context.Context, io.Reader, func(app.WriteTx, domain.Object) error) (domain.Object, error)
}

// ProviderCoordinator uses the existing writer and job ledger. Nil transport
// reports unavailable, never substitutes a mock or executes a configured shell.
type ProviderCoordinator struct {
	Store  app.Store
	Bytes  ProviderBytes
	Driver ProviderDriver
	// Prepare verifies and stages a saved whole result outside the writer. It
	// cannot dispatch; final application still rechecks the same authority.
	Prepare           func(context.Context, domain.ProviderAttempt) error
	Apply             func(app.WriteTx, domain.ProviderAttempt) error
	active            atomic.Pointer[providerPhase]
	accountingFailure atomic.Pointer[providerAccountingError]
	accountingUnsafe  atomic.Pointer[providerAccountingError]
}

func (p *ProviderCoordinator) Process(ctx context.Context, id string) error {
	e := p.process(ctx, id, nil)
	p.rememberAccountingFailure(e)
	return e
}

func (p *ProviderCoordinator) process(ctx context.Context, id string, phase *providerPhase) error {
	var a domain.ProviderAttempt
	var inputs []domain.ProviderInput
	var j domain.Job
	var reconcile bool
	var ownerRuntime string
	var normalizing bool
	var runningSaved *domain.ProviderAttempt
	var cached *domain.ProviderAttempt
	var cachedObjects []domain.Object
	outputAllowed := true
	e := p.Store.Write(ctx, func(tx app.WriteTx) error {
		var e error
		j, e = tx.Job(id)
		if e != nil {
			return e
		}
		if !domain.ProviderJob(j.Kind) {
			return domain.ErrTransition
		}
		// Commit the normative normalization before any lookup/cancel branch can
		// fail and roll back, or before collector cleanup sees the recovered job.
		if j.State == "interrupted" {
			normalizing = true
			return tx.ReconcileJob(id)
		}
		rows, e := tx.ProviderAttempts(id)
		if e != nil {
			return e
		}
		lookupID, lookupErr := tx.NextProviderReconciliation(id)
		if lookupErr != nil && !errors.Is(lookupErr, domain.ErrNotFound) {
			return lookupErr
		}
		if lookupID != "" {
			a, e = tx.ProviderAttempt(lookupID)
			if e != nil {
				return e
			}
			if e = tx.ClaimProviderReconciliation(id, a.ID); e != nil {
				return e
			}
			if a.State != "dispatching" && a.State != "running" && a.State != "outcome_unknown" {
				if e = tx.CompleteProviderReconciliation(id, a.ID); e != nil {
					return e
				}
				a.ID = ""
				return nil
			}
			reconcile = true
			library, e := tx.Library()
			if e != nil {
				return e
			}
			ownerRuntime = library.RuntimeGeneration
			// Reconciliation is owned by this authorized carrier job.
			originalJob := a.JobID
			a.JobID = id
			outputAllowed = originalJob == id && !terminalProviderJob(j.State)
			if a.Runtime == "" {
				outputAllowed = false
			}
			var scope domain.ProviderScope
			if json.Unmarshal(j.Scope, &scope) != nil {
				return domain.ErrInvalidTarget
			}
			auth, e := tx.AuthState()
			if e != nil {
				return e
			}
			if scope.PrincipalID != auth.OwnerID || scope.AuthEpoch != auth.AuthEpoch {
				outputAllowed = false
			}
			if e = app.AuthorizeTarget(tx, scope.Target); e != nil {
				if !errors.Is(e, domain.ErrDenied) && !errors.Is(e, domain.ErrNotFound) {
					return e
				}
				outputAllowed = false
			}
			return nil
		}
		if j.CancelRequested {
			return tx.ConfirmStop(id, j.AttemptGeneration)
		}

		if j.State == "running" {
			for _, saved := range rows {
				if saved.State == "result_saved" && saved.ApplyGeneration == j.AttemptGeneration {
					runningSaved = &saved
					return nil
				}
			}
			return domain.ErrTransition
		}

		if j.State == "reconciling" {
			return tx.PauseProviderJob(id, "reconciliation_required")
		}

		if j.State != "queued" {
			return domain.ErrTransition
		}
		// Exact owner-authorized cached work is a distinct branch. Unavailable
		// bytes/authority cannot turn the last continue choice into a new dispatch.
		for index := len(rows) - 1; index >= 0; index-- {
			saved := rows[index]
			if saved.ApplyGeneration != j.AttemptGeneration+1 {
				continue
			}
			cached = &saved
			if e = app.GuardProviderContinuation(tx, saved); e != nil {
				return e
			}
			cachedObjects, e = app.ProviderSavedObjects(tx, saved)
			return e
		}
		if p.Driver == nil {
			return tx.PauseProviderJob(id, "provider_unavailable")
		}
		j, e = tx.ClaimJob(id)
		if e != nil {
			return e
		}
		var scope domain.ProviderScope
		if json.Unmarshal(j.Scope, &scope) != nil {
			return domain.ErrInvalidTarget
		}
		l, e := tx.Library()
		if e != nil {
			return e
		}
		attemptID, e := service.NewID()
		if e != nil {
			return e
		}
		ownerRuntime = l.RuntimeGeneration
		a = domain.ProviderAttempt{ID: attemptID, JobID: id, Generation: j.AttemptGeneration, State: "prepared", Runtime: l.RuntimeGeneration, TargetSetID: scope.TargetSetID, RequestSHA: scope.RequestSHA}
		// Captures identify exact objects; constructing these rows does not mark
		// input observed. The dispatching adapter supplies that separate evidence.
		for _, readID := range scope.ReadIDs {
			// CaptureLookup preserves the source reader's exact identity.
			read, e := tx.CapturedRead(readID)
			if e != nil {
				return e
			}
			inputs = append(inputs, domain.ProviderInput{AttemptID: a.ID, ReadID: readID, Hash: read.BytesSHA})
		}
		if e = tx.PrepareProviderAttempt(a, inputs); e != nil {
			return e
		}
		return nil
	})
	if e == nil && runningSaved != nil {
		if p.Prepare != nil {
			if e = p.Prepare(ctx, *runningSaved); e != nil {
				return errors.Join(e, p.pause(context.WithoutCancel(ctx), id, providerPreparationReason(e)))
			}
		}
		return p.Store.Write(ctx, func(tx app.WriteTx) error {
			return tx.ApplyProviderResult(runningSaved.ID, func(tx app.WriteTx) error {
				if p.Apply != nil {
					return p.Apply(tx, *runningSaved)
				}
				return nil
			})
		})
	}
	if e == nil && cached != nil {
		return p.continueSaved(ctx, j, *cached, cachedObjects)
	}
	if e != nil || a.ID == "" {
		if e != nil && ctx.Err() == nil && !normalizing {
			reason := providerPreparationReason(e)
			if cached != nil {
				_ = p.pauseContinuation(context.WithoutCancel(ctx), j, reason)
			} else {
				_ = p.pause(context.WithoutCancel(ctx), id, reason)
			}
		}
		return e
	}
	if phase != nil {
		if reconcile {
			phase.runtime = ownerRuntime
			phase.reconcileAttempt = a.ID
		} else {
			phase.generation = a.Generation
		}
	}
	var out ProviderOutcome
	var dispatched atomic.Bool
	if reconcile {
		if p.Driver == nil {
			out = ProviderOutcome{State: "outcome_unknown", Reason: "provider_unavailable"}
		} else {
			out, e = p.Driver.Reconcile(ctx, a, outputAllowed)
		}
	} else {
		callCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		watchDone := make(chan struct{})
		watchStopped := make(chan struct{})
		defer func() { cancel(); close(watchDone); <-watchStopped }()
		go func() {
			defer close(watchStopped)
			registrationDeadline := time.Time{}
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-watchDone:
					return
				case <-callCtx.Done():
					return
				case <-ticker.C:
					current, err := p.Store.Job(callCtx, id)
					if err == nil && (current.CancelRequested || current.AttemptGeneration != a.Generation) {
						if !dispatched.Load() {
							cancel()
							return
						}
						if registrationDeadline.IsZero() {
							registrationDeadline = time.Now().Add(5 * time.Second)
						}
						if !time.Now().Before(registrationDeadline) {
							cancel()
							return
						}
						if err := p.Driver.Cancel(callCtx, a); !errors.Is(err, domain.ErrReconciliation) {
							return
						}
					}
				}
			}
		}()
		out, e = p.Driver.Execute(callCtx, a, inputs, func() error {
			if dispatched.Load() {
				return domain.ErrTransition
			}
			err := p.Store.Write(callCtx, func(tx app.WriteTx) error { return tx.DispatchProviderAttempt(a.ID) })
			if err == nil {
				dispatched.Store(true)
			}
			return err
		}, func(progress ProviderProgress) error {
			if !dispatched.Load() {
				return domain.ErrTransition
			}
			return p.observeProgress(context.WithoutCancel(ctx), a, progress)
		})
	}
	return p.settleProvider(ctx, a, j, out, e, reconcile, outputAllowed, dispatched.Load(), ownerRuntime, phase)
}

func (p *ProviderCoordinator) pause(ctx context.Context, id, reason string) error {
	return p.Store.Write(ctx, func(tx app.WriteTx) error { return tx.PauseProviderJob(id, reason) })
}

func (p *ProviderCoordinator) observeProgress(ctx context.Context, a domain.ProviderAttempt, progress ProviderProgress) error {
	return p.Store.Write(ctx, func(tx app.WriteTx) error {
		current, e := tx.ProviderAttempt(a.ID)
		if e != nil {
			return e
		}
		state, reason := "running", ""
		if current.State == "outcome_unknown" {
			state, reason = "outcome_unknown", "transport_unknown"
		}
		for _, v := range progress.Observed {
			if v.AttemptID != a.ID {
				return domain.ErrInvalidTarget
			}
			if e = tx.ObserveProviderInput(a.ID, v.ReadID, v.ObservedOrigin); e != nil {
				return e
			}
		}
		return tx.ObserveProviderAttempt(a.ID, state, progress.RequestID, progress.SessionID, reason)
	})
}
func (p *ProviderCoordinator) continueSaved(ctx context.Context, expected domain.Job, saved domain.ProviderAttempt, objects []domain.Object) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	reader, ok := p.Bytes.(interface {
		Read(context.Context, domain.Object, domain.Pin, func(io.ReadSeeker) error) error
	})
	if !ok || len(objects) == 0 {
		_ = p.pauseContinuation(context.WithoutCancel(ctx), expected, "saved_result_unavailable")
		return domain.ErrReconciliation
	}
	claim := func() error {
		if p.Prepare != nil {
			if e := p.Prepare(ctx, saved); e != nil {
				return e
			}
		}
		return p.Store.Write(ctx, func(tx app.WriteTx) error {
			j, e := tx.Job(expected.ID)
			if e != nil {
				return e
			}
			a, e := tx.ProviderAttempt(saved.ID)
			if e != nil {
				return e
			}
			if j.AttemptGeneration != expected.AttemptGeneration || a.ApplyGeneration != saved.ApplyGeneration || a.ResultHash != saved.ResultHash {
				return domain.ErrStaleWorker
			}
			if e = app.GuardProviderContinuation(tx, a); e != nil {
				return e
			}
			currentObjects, e := app.ProviderSavedObjects(tx, a)
			if e != nil {
				return e
			}
			if len(currentObjects) != len(objects) {
				return domain.ErrReconciliation
			}
			for index, object := range currentObjects {
				if object.Hash != objects[index].Hash || object.Size != objects[index].Size {
					return domain.ErrReconciliation
				}
			}
			if _, e = tx.ClaimJob(j.ID); e != nil {
				return e
			}
			return tx.ApplyProviderResult(a.ID, func(tx app.WriteTx) error {
				if p.Apply != nil {
					return p.Apply(tx, a)
				}
				return nil
			})
		})
	}
	var e error
	reason := "saved_result_unavailable"
	for index, object := range objects {
		callbackEntered := false
		e = reader.Read(ctx, object, domain.Pin{Kind: "provider_continuation", OwnerID: saved.ID}, func(io.ReadSeeker) error {
			callbackEntered = true
			if index == len(objects)-1 {
				return claim()
			}
			return nil
		})
		if e != nil {
			if callbackEntered || errors.Is(e, domain.ErrDenied) || errors.Is(e, domain.ErrPrecondition) || errors.Is(e, domain.ErrReconciliation) {
				reason = providerPreparationReason(e)
			} else if index != len(objects)-1 {
				reason = "captured_input_unavailable"
				if errors.Is(e, domain.ErrContentIntegrity) {
					reason = "captured_input_invalid"
				}
			}
			break
		}
	}
	if e != nil && ctx.Err() == nil {
		_ = p.pauseContinuation(context.WithoutCancel(ctx), expected, reason)
	}
	return e
}

func (p *ProviderCoordinator) pauseContinuation(ctx context.Context, expected domain.Job, reason string) error {
	return p.Store.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(expected.ID)
		if e != nil {
			return e
		}
		if j.AttemptGeneration != expected.AttemptGeneration || j.State != "queued" || j.CancelRequested {
			return nil
		}
		return tx.PauseProviderJob(j.ID, reason)
	})
}

// These are local preparation/authority categories, never backend terminal facts.
func providerPreparationReason(e error) string {
	switch {
	case errors.Is(e, domain.ErrNotFound), errors.Is(e, domain.ErrDeleting):
		return "captured_input_unavailable"
	case errors.Is(e, domain.ErrInvalidTarget), errors.Is(e, domain.ErrContentIntegrity):
		return "captured_input_invalid"
	case errors.Is(e, domain.ErrDenied), errors.Is(e, domain.ErrPrecondition), errors.Is(e, domain.ErrReconciliation):
		return "authority_reconciliation_required"
	default:
		return "preparation_failed"
	}
}
