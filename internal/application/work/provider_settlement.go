package work

import (
	"context"
	"errors"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// A handled provider accounting error is failed AI work, not dispatcher health.
// Direct Process and Stop retain it; Poll only reports collection/writer health.
type providerAccountingError struct {
	cause       error
	restartSafe bool
}

func (e *providerAccountingError) Error() string { return "provider accounting failed" }
func (e *providerAccountingError) Unwrap() error { return e.cause }
func (p *ProviderCoordinator) settleProvider(ctx context.Context, a domain.ProviderAttempt, j domain.Job, out ProviderOutcome, callErr error, reconcile, outputAllowed, dispatched bool, runtime string, phase *providerPhase) error {
	accounting := context.WithoutCancel(ctx)
	// Only a positively observed drain can make a settled error historical-only.
	// Missing attachments do not prove that an owned transport was collected.
	diagnostic := func(cause error, settled bool) error {
		drained := out.LocalClosed && providerDrainedReason(out.LocalCleanupReason)
		if out.Transport != nil {
			drained = drained || out.Transport.LocalClosed && out.Transport.CleanupState == "drained" && providerDrainedReason(out.Transport.CleanupReason)
		}
		if out.Lookup != nil {
			drained = drained || out.Lookup.LocalClosed && providerDrainedReason(out.Lookup.CleanupReason)
		}
		return &providerAccountingError{cause: cause, restartSafe: settled && drained && out.CleanupError == nil}
	}
	intentActive := reconcile
	expected := a.Generation
	if reconcile {
		expected = j.AttemptGeneration
	}
	if out.Body != nil {
		defer out.Body.Close()
	}
	done := func() {
		if phase != nil {
			phase.accounted = true
			phase.reconcileAttempt = ""
		}
	}
	fail := func(cause error) error {
		e := p.Store.Write(accounting, func(tx app.WriteTx) error {
			if intentActive {
				return tx.AbandonProviderReconciliation(a.JobID, a.ID, runtime, j.AttemptGeneration, "provider_accounting_unavailable")
			}
			current, e := tx.Job(a.JobID)
			if e != nil {
				return e
			}
			if terminalProviderJob(current.State) {
				return nil
			}
			if current.CancelRequested {
				return tx.ConfirmStop(current.ID, current.AttemptGeneration)
			}
			if current.AttemptGeneration != expected {
				return domain.ErrStaleWorker
			}
			return tx.PauseProviderJob(a.JobID, "provider_accounting_unavailable")
		})
		if e == nil {
			done()
		}
		return diagnostic(errors.Join(cause, e), e == nil)
	}
	if !reconcile && !dispatched {
		reason := out.Reason
		if callErr != nil {
			classified := providerPreparationReason(callErr)
			if classified != "preparation_failed" || reason == "" {
				reason = classified
			}
			if ctx.Err() != nil && (errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded)) || errors.Is(callErr, domain.ErrTransition) || errors.Is(callErr, domain.ErrStaleWorker) {
				reason = "dispatch_not_authorized"
			}
		}
		if reason == "" {
			reason = "dispatch_not_authorized"
		}
		var auditErr error
		if out.Transport != nil {
			auditErr = p.Store.Write(accounting, func(tx app.WriteTx) error { return tx.RecordProviderTransport(a.ID, *out.Transport) })
		}
		e := p.Store.Write(accounting, func(tx app.WriteTx) error {
			current, e := tx.Job(a.JobID)
			if e != nil {
				return e
			}
			if current.CancelRequested {
				return tx.ConfirmStop(a.JobID, current.AttemptGeneration)
			}
			if current.AttemptGeneration != expected {
				return domain.ErrStaleWorker
			}
			if err := tx.PauseProviderJob(a.JobID, reason); err != nil {
				return err
			}
			return settleParentScanAfterChild(tx, current)
		})
		if e != nil {
			return fail(e)
		}
		done()
		if auditErr != nil && !ProviderAuthorityRefusal(auditErr) {
			return diagnostic(errors.Join(callErr, auditErr), true)
		}
		if out.CleanupError != nil {
			if callErr != nil {
				return diagnostic(callErr, true)
			}
			return diagnostic(out.CleanupError, true)
		}
		if callErr != nil && out.Reason == "provider_accounting_unavailable" && !ProviderAuthorityRefusal(callErr) {
			return diagnostic(callErr, true)
		}
		return nil
	}
	var progressErr error
	if len(out.Observed) > 0 || out.RequestID != "" || out.SessionID != "" {
		progressErr = p.observeProgress(accounting, a, ProviderProgress{RequestID: out.RequestID, SessionID: out.SessionID, Observed: out.Observed})
		if progressErr != nil {
			// A selectively failed input observation must not erase independently
			// recordable native identities or terminal facts.
			identityErr := p.Store.Write(accounting, func(tx app.WriteTx) error {
				current, e := tx.ProviderAttempt(a.ID)
				if e != nil {
					return e
				}
				state, reason := "running", ""
				if current.State == "outcome_unknown" {
					state = "outcome_unknown"
					reason = "transport_unknown"
				}
				return tx.ObserveProviderAttempt(a.ID, state, out.RequestID, out.SessionID, reason)
			})
			progressErr = errors.Join(progressErr, identityErr)
			if !out.CompletionObserved && out.State != "failed" && out.State != "cancelled" {
				return fail(progressErr)
			}
		}
	}
	saved := out.CompletionObserved && outputAllowed && out.State == "result_saved" && out.Body != nil && p.Bytes != nil
	var putErr error
	if saved {
		// Commit complete output independently before any fallible final audit/Apply.
		_, putErr = p.Bytes.Put(accounting, out.Body, func(tx app.WriteTx, o domain.Object) error { return tx.SaveProviderResult(a.ID, o) })
		if putErr != nil {
			saved = false
			out.State, out.Reason = "outcome_unknown", "result_retrieval_pending"
			if ProviderAuthorityRefusal(putErr) {
				out.State, out.Reason = "failed", "result_unavailable"
			}
		}
	}
	if !saved {
		// Completion is a backend fact, independent of transient local retrieval.
		// Only an explicit definitive output/authority refusal is terminal failed.
		if out.CompletionObserved && out.State != "failed" {
			out.State, out.Reason = "outcome_unknown", "result_retrieval_pending"
		}
		if out.CompletionObserved && !outputAllowed {
			out.State, out.Reason = "failed", "result_unavailable"
		}
		if out.State != "failed" && out.State != "cancelled" {
			out.State = "outcome_unknown"
			if out.Reason == "" {
				out.Reason = "transport_unknown"
			}
		}
		if e := p.Store.Write(accounting, func(tx app.WriteTx) error {
			current, e := tx.ProviderAttempt(a.ID)
			if e != nil {
				return e
			}
			// A Put may have committed before returning an uncertain local error.
			if current.State == "result_saved" {
				return nil
			}
			if reconcile {
				if e := tx.GuardProviderLookup(a.JobID, a.ID); e != nil {
					return e
				}
			}
			return tx.ObserveProviderAttempt(a.ID, out.State, out.RequestID, out.SessionID, out.Reason)
		}); e != nil {
			return fail(errors.Join(putErr, e))
		}
	}

	if progressErr != nil {
		return fail(progressErr)
	}
	if e := p.Store.Write(accounting, func(tx app.WriteTx) error {
		if out.Transport != nil {
			if e := tx.RecordProviderTransport(a.ID, *out.Transport); e != nil {
				return e
			}
		}
		if out.Lookup != nil {
			if e := tx.RecordProviderLookup(a.ID, a.JobID, *out.Lookup); e != nil {
				return e
			}
		}
		if reconcile && (putErr == nil || ProviderAuthorityRefusal(putErr)) && !errors.Is(callErr, domain.ErrConflict) {
			return tx.CompleteProviderReconciliation(a.JobID, a.ID)
		}
		return nil
	}); e != nil {
		return fail(e)
	}
	if putErr != nil && !ProviderAuthorityRefusal(putErr) {
		return fail(putErr)
	}
	if errors.Is(callErr, domain.ErrConflict) {
		return fail(callErr)
	}
	intentActive = false
	if phase != nil {
		phase.reconcileAttempt = ""
	}
	if saved && !reconcile && p.Prepare != nil {
		// The complete result is durable already. New parsing/copying remains
		// cancellable by this phase; only its required accounting is detached.
		if preparationErr := p.Prepare(ctx, a); preparationErr != nil {
			settleErr := p.Store.Write(accounting, func(tx app.WriteTx) error {
				current, e := tx.Job(a.JobID)
				if e != nil {
					return e
				}
				if terminalProviderJob(current.State) {
					return nil
				}
				if current.CancelRequested {
					return tx.ConfirmStop(current.ID, current.AttemptGeneration)
				}
				if current.AttemptGeneration != expected {
					return domain.ErrStaleWorker
				}
				if err := tx.PauseProviderJob(current.ID, providerPreparationReason(preparationErr)); err != nil {
					return err
				}
				return settleParentScanAfterChild(tx, current)
			})
			if settleErr != nil {
				return fail(errors.Join(preparationErr, settleErr))
			}
			done()
			return diagnostic(errors.Join(preparationErr, callErr, out.CleanupError), true)
		}
	}
	e := p.Store.Write(accounting, func(tx app.WriteTx) error {
		current, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		if terminalProviderJob(current.State) {
			return nil
		}
		if current.CancelRequested {
			if err := tx.ConfirmStop(current.ID, current.AttemptGeneration); err != nil {
				return err
			}
			return settleParentScanAfterChild(tx, current)
		}
		expected := a.Generation
		if reconcile {
			expected = j.AttemptGeneration
		}
		if current.AttemptGeneration != expected {
			return domain.ErrStaleWorker
		}
		if saved {
			if reconcile {
				if err := tx.PauseProviderJob(a.JobID, "saved_result_requires_continuation"); err != nil {
					return err
				}
				return settleParentScanAfterChild(tx, current)
			}
			return tx.ApplyProviderResult(a.ID, func(tx app.WriteTx) error {
				if p.Apply != nil {
					return p.Apply(tx, a)
				}
				return nil
			})
		}
		if reconcile {
			if _, e := tx.NextProviderReconciliation(a.JobID); e == nil {
				return nil
			} else if !errors.Is(e, domain.ErrNotFound) {
				return e
			}
		}
		if current.ProviderAttempts != nil && current.ProviderAttempts.Unknown > 0 {
			if err := tx.PauseProviderJob(a.JobID, "outcome_unknown"); err != nil {
				return err
			}
			return settleParentScanAfterChild(tx, current)
		}
		reason := out.Reason
		if reason == "" {
			reason = out.State
		}
		if err := tx.PauseProviderJob(a.JobID, reason); err != nil {
			return err
		}
		return settleParentScanAfterChild(tx, current)
	})
	if e != nil {
		if !ProviderAuthorityRefusal(e) {
			return fail(e)
		}
		// A current owner/policy refusal after saving is ordinary guarded work.
		// Keep the complete saved result; never apply under revoked authority.
		if refusalErr := p.Store.Write(accounting, func(tx app.WriteTx) error {
			current, readErr := tx.Job(a.JobID)
			if readErr != nil {
				return readErr
			}
			if terminalProviderJob(current.State) {
				return nil
			}
			if current.CancelRequested {
				return tx.ConfirmStop(current.ID, current.AttemptGeneration)
			}
			if current.AttemptGeneration != expected {
				return domain.ErrStaleWorker
			}
			if err := tx.PauseProviderJob(a.JobID, "authority_reconciliation_required"); err != nil {
				return err
			}
			return settleParentScanAfterChild(tx, current)
		}); refusalErr != nil {
			if ProviderAuthorityRefusal(refusalErr) {
				done()
				return refusalErr
			}
			return fail(refusalErr)
		}
	}
	done()
	// Preserve cleanup/protocol diagnostics without destroying a known result or
	// shutting down the daemon after truthful durable settlement.
	if out.CleanupError != nil {
		if callErr != nil {
			return diagnostic(callErr, true)
		}
		return diagnostic(out.CleanupError, true)
	}
	if callErr != nil && !ProviderAuthorityRefusal(callErr) && !errors.Is(callErr, context.Canceled) && !errors.Is(callErr, context.DeadlineExceeded) && (saved || out.State == "failed" || out.State == "cancelled") {
		return diagnostic(callErr, true)
	}
	return nil
}

// ProviderAuthorityRefusal recognizes only actual authority fences. Joined
// persistence/cleanup failures are not swallowed merely because one leaf denies.
func ProviderAuthorityRefusal(cause error) bool {
	if cause == nil {
		return false
	}
	if many, ok := cause.(interface{ Unwrap() []error }); ok {
		leaves := many.Unwrap()
		if len(leaves) == 0 {
			return false
		}
		for _, leaf := range leaves {
			if !ProviderAuthorityRefusal(leaf) {
				return false
			}
		}
		return true
	}
	if one, ok := cause.(interface{ Unwrap() error }); ok {
		return ProviderAuthorityRefusal(one.Unwrap())
	}
	return cause == domain.ErrDenied || cause == domain.ErrStaleWorker || cause == domain.ErrPrecondition || cause == domain.ErrReconciliation
}

// signal_error_drained retains a signal diagnostic after independent group,
// leader and reader drain succeeded; it is not cleanup uncertainty.
func providerDrainedReason(reason string) bool {
	return reason == "" || reason == "signal_error_drained"
}

func settleParentScanAfterChild(tx app.WriteTx, child domain.Job) error {
	if child.Kind != "maintenance" || child.ParentID == "" {
		return nil
	}
	parent, err := tx.Job(child.ParentID)
	if err != nil || parent.Kind != "scan" {
		return nil
	}
	updatedChild, err := tx.Job(child.ID)
	if err != nil {
		return nil
	}
	if parent.CancelRequested && (parent.State == "paused" || parent.State == "cancelling") {
		if parent.ProviderAttempts == nil || parent.ProviderAttempts.Unknown == 0 {
			return tx.ConfirmStop(parent.ID, parent.AttemptGeneration)
		}
		return nil
	}
	if (parent.State == "running" || parent.State == "queued") && !parent.CancelRequested && updatedChild.State == "paused" {
		switch updatedChild.Error {
		case "outcome_unknown", "saved_result_requires_continuation", "reconciliation_required", "authority_reconciliation_required", "preparation_failed", "captured_input_unavailable", "captured_input_invalid":
			return tx.PauseProviderJob(parent.ID, updatedChild.Error)
		}
		if updatedChild.ProviderAttempts != nil && (updatedChild.ProviderAttempts.Unknown > 0 || updatedChild.ProviderAttempts.LatestState == "result_saved") {
			reason := updatedChild.Error
			if reason == "" {
				reason = "outcome_unknown"
			}
			return tx.PauseProviderJob(parent.ID, reason)
		}
	}
	if parent.State == "paused" && (parent.ProviderAttempts == nil || parent.ProviderAttempts.Unknown == 0) {
		if updatedChild.State == "paused" && updatedChild.Error == "saved_result_requires_continuation" {
			return tx.PauseProviderJob(parent.ID, "saved_result_requires_continuation")
		}
		if updatedChild.State == "paused" && updatedChild.Error != "" && parent.Error == "outcome_unknown" {
			return tx.PauseProviderJob(parent.ID, updatedChild.Error)
		}
	}
	return nil
}
