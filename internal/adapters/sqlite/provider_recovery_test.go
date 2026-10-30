package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

type completedPendingDriver struct{ providerTerminalDriver }

func (d *completedPendingDriver) Execute(ctx context.Context, a domain.ProviderAttempt, in []domain.ProviderInput, send func() error, ack func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	out, e := d.providerTerminalDriver.Execute(ctx, a, in, send, ack)
	if out.Body != nil {
		out.Body.Close()
		out.Body = nil
	}
	out.State = "outcome_unknown"
	out.Reason = "result_retrieval_pending"
	out.Transport.DriverOutcome = "outcome_unknown"
	return out, e
}

var _ = It("C03W4 completed-pending primary facts survive selective audit failure and cold reopen lookup/continue never Execute", Label("persistence"), func() {
	for _, auditFailure := range []bool{false, true} {
		s, a := providerFixture(false)
		faults := &providerAccountingStore{Store: s}
		faults.failTransport.Store(auditFailure)
		driver := &completedPendingDriver{providerTerminalDriver{store: s, state: "result_saved"}}
		p := work.ProviderCoordinator{Store: faults, Driver: driver, Bytes: providerFixtureBytes{s}}
		e := p.Process(ctx, a.JobID)
		if auditFailure {
			Expect(e).To(HaveOccurred())
		} else {
			Expect(e).NotTo(HaveOccurred())
		}
		var retained domain.ProviderAttempt
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.ProviderAttempts(a.JobID)
			if e == nil {
				retained = rows[0]
			}
			return e
		})).To(Succeed())
		Expect(retained.State).To(Equal("outcome_unknown"))
		Expect(retained.Reason).To(Equal("result_retrieval_pending"))
		Expect(retained.ResultHash).To(BeEmpty())
		Expect(retained.RequestID).To(Equal("observed-request"))
		Expect(retained.SessionID).To(Equal("observed-session"))
		profile := s.profile
		Expect(s.Close()).To(Succeed())
		reopened := New(nil)
		_, closeDB, e := reopened.Prepare(ctx, profile)
		Expect(e).NotTo(HaveOccurred())
		defer closeDB()
		lookup := &providerTerminalDriver{store: reopened, reconcileSaved: true}
		p2 := work.ProviderCoordinator{Store: reopened, Driver: lookup, Bytes: providerContinuationFixtureBytes{providerFixtureBytes{reopened}}}
		j, e := reopened.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(reopened.Write(ctx, func(tx app.WriteTx) error {
			_, e := tx.ResumeProviderJob(a.JobID, j.AttemptGeneration, "reconcile_only")
			return e
		})).To(Succeed())
		Expect(p2.Process(ctx, a.JobID)).To(Succeed())
		j, e = reopened.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(reopened.Write(ctx, func(tx app.WriteTx) error {
			actual, e := tx.ProviderAttempt(retained.ID)
			if e == nil {
				Expect(actual.State).To(Equal("result_saved"))
				Expect(actual.ResultHash).To(HaveLen(64))
			}
			return e
		})).To(Succeed())
		Expect(reopened.Write(ctx, func(tx app.WriteTx) error {
			_, e := tx.ResumeProviderJob(a.JobID, j.AttemptGeneration, "continue")
			return e
		})).To(Succeed())
		Expect(p2.Process(ctx, a.JobID)).To(Succeed())
		Expect(lookup.calls.Load()).To(BeZero())
		Expect(driver.calls.Load()).To(Equal(int64(1)))
	}
})

type guardedProviderBytes struct {
	s             *Store
	before, after func()
	failure       error
}
type providerGuardedBody struct {
	io.Reader
	guard func() error
}

func (b providerGuardedBody) Read(p []byte) (int, error) {
	if e := b.guard(); e != nil {
		return 0, e
	}
	return b.Reader.Read(p)
}
func (b guardedProviderBytes) Put(ctx context.Context, r io.Reader, commit func(app.WriteTx, domain.Object) error) (domain.Object, error) {
	if b.before != nil {
		b.before()
	}
	if b.failure != nil {
		return domain.Object{}, b.failure
	}
	var attempt string
	// The actual result-read guard uses the current durable job/attempt authority.
	Expect(b.s.db.QueryRow(`SELECT attempt_id FROM provider_attempts ORDER BY rowid DESC LIMIT 1`).Scan(&attempt)).To(Succeed())
	guarded := providerGuardedBody{r, func() error {
		return b.s.Write(ctx, func(tx app.WriteTx) error {
			a, e := tx.ProviderAttempt(attempt)
			if e != nil {
				return e
			}
			return tx.GuardProviderPayload(a.JobID, a.ID, false)
		})
	}}
	o, e := (providerFixtureBytes{b.s}).Put(ctx, guarded, commit)
	if e == nil && b.after != nil {
		b.after()
	}
	return o, e
}

var _ = It("C03W4 real cancel and epoch refusal during copy/apply are ordinary while disk failures remain visible", Label("persistence"), func() {
	for _, mode := range []string{"copy_cancel", "copy_epoch", "apply_epoch", "disk", "joined"} {
		s, a := providerFixture(false)
		d := &providerTerminalDriver{store: s, state: "result_saved"}
		b := guardedProviderBytes{s: s}
		revoke := func() {
			_, e := s.db.Exec(`UPDATE libraries SET auth_epoch='revoked'`)
			Expect(e).NotTo(HaveOccurred())
		}
		switch mode {
		case "copy_cancel":
			b.before = func() {
				Expect(s.Write(ctx, func(tx app.WriteTx) error { _, e := tx.CancelJob(a.JobID); return e })).To(Succeed())
			}
		case "copy_epoch":
			b.before = revoke
		case "apply_epoch":
			b.after = revoke
		case "disk":
			b.failure = selectiveProviderFault
		case "joined":
			b.failure = errors.Join(domain.ErrDenied, selectiveProviderFault)
		}
		p := &work.ProviderCoordinator{Store: s, Driver: d, Bytes: b}
		e := p.Process(ctx, a.JobID)
		j, readErr := s.Job(ctx, a.JobID)
		Expect(readErr).NotTo(HaveOccurred())
		if mode == "disk" || mode == "joined" {
			Expect(e).To(HaveOccurred())
			Expect(j.Error).To(Equal("provider_accounting_unavailable"))
			Expect(p.StopProviders()).To(HaveOccurred())
		} else {
			Expect(e).NotTo(HaveOccurred())
			Expect(j.Error).NotTo(Equal("provider_accounting_unavailable"))
			Expect(p.StopProviders()).To(Succeed())
			if mode == "copy_cancel" {
				Expect(j.CancelRequested).To(BeTrue())
				Expect(j.State).To(Equal("cancelled"))
			} else {
				Expect(j.State).To(Equal("paused"))
			}
		}
		Expect(p.PollProvider(ctx)).To(Succeed())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.ProviderAttempts(a.JobID)
			if e != nil {
				return e
			}
			actual := rows[0]
			if mode == "apply_epoch" {
				Expect(actual.State).To(Equal("result_saved"))
				Expect(actual.ResultHash).To(HaveLen(64))
			} else if mode == "disk" || mode == "joined" {
				Expect(actual.State).To(Equal("outcome_unknown"))
				Expect(actual.Reason).To(Equal("result_retrieval_pending"))
			} else {
				Expect(actual.State).To(Equal("failed"))
				Expect(actual.Reason).To(Equal("result_unavailable"))
			}
			return nil
		})).To(Succeed())
	}
})

type preparationCleanupDriver struct {
	store app.Store
	audit bool
	clean bool
}

func (d preparationCleanupDriver) Execute(ctx context.Context, a domain.ProviderAttempt, _ []domain.ProviderInput, _ func() error, _ func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	out := work.ProviderOutcome{Reason: "auth_required"}
	if !d.clean {
		out.CleanupError = selectiveProviderFault
	}
	if d.audit {
		out.Reason = "dispatch_not_authorized"
		Expect(d.store.Write(ctx, func(tx app.WriteTx) error {
			v, e := providerTestAudit(tx, a)
			if e != nil {
				return e
			}
			if e = tx.RecordProviderTransport(a.ID, v); e != nil {
				return e
			}
			v.LocalClosed = d.clean
			v.CleanupState = "unknown"
			v.CleanupReason = "controlled_cleanup_unknown"
			if d.clean {
				v.CleanupState, v.CleanupReason = "drained", ""
			}
			v.FinishedAt = "2026-10-30T00:00:00Z"
			out.Transport = &v
			return nil
		})).To(Succeed())
	}
	if !d.clean {
		return out, selectiveProviderFault
	}
	return out, nil
}
func (preparationCleanupDriver) Cancel(context.Context, domain.ProviderAttempt) error { return nil }
func (preparationCleanupDriver) Reconcile(context.Context, domain.ProviderAttempt, bool) (work.ProviderOutcome, error) {
	panic("no dispatch")
}

var _ = It("C03W4 pre-dispatch cleanup failure retains prepared refusal and only complete truthful audit", Label("persistence"), func() {
	for _, audit := range []bool{false, true} {
		s, a := providerFixture(false)
		p := &work.ProviderCoordinator{Store: s, Driver: preparationCleanupDriver{store: s, audit: audit}}
		Expect(p.Process(ctx, a.JobID)).To(HaveOccurred())
		Expect(p.PollProvider(ctx)).To(Succeed())
		Expect(p.StopProviders()).To(HaveOccurred())
		j, e := s.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.State).To(Equal("paused"))
		if audit {
			Expect(j.Error).To(Equal("dispatch_not_authorized"))
		} else {
			Expect(j.Error).To(Equal("auth_required"))
		}
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.ProviderAttempts(a.JobID)
			if e != nil {
				return e
			}
			actual := rows[0]
			Expect(actual.State).To(Equal("prepared"))
			Expect(actual.MayHaveDispatched).To(BeFalse())
			Expect(actual.RequestID).To(BeEmpty())
			if audit {
				Expect(actual.Transport.CleanupState).To(Equal("unknown"))
			} else {
				Expect(actual.Transport).To(BeNil())
			}
			_, e = tx.ResumeProviderJob(a.JobID, j.AttemptGeneration, "continue")
			return e
		})).To(Succeed())
	}
})

var _ = It("C03W4 restore merges completed-pending monotonically and retains conflicting native terminal facts", Label("persistence"), func() {
	for _, terminal := range []string{"inProgress", "failed", "interrupted", "forbidden"} {
		conflict := terminal == "failed" || terminal == "interrupted"
		for _, reverse := range []bool{false, true} {
			s, a := providerFixture()
			destination := filepath.Join(GinkgoT().TempDir(), "restore.sqlite")
			Expect(s.SnapshotDatabase(ctx, destination)).To(Succeed())
			yes := true
			old := providerRestoreFact{ID: a.ID, SHA: a.RequestSHA, JobID: a.JobID, State: "outcome_unknown", Reason: "result_retrieval_pending", Generation: a.Generation, RequestID: "request", SessionID: "session", MayHaveDispatched: &yes}
			newer := old
			newer.Reason = "transport_unknown"
			if terminal == "failed" {
				newer.State, newer.Reason = "failed", "provider_failed"
			}
			if terminal == "interrupted" {
				newer.State, newer.Reason = "cancelled", "cancel_confirmed"
			}
			if terminal == "forbidden" {
				newer.State, newer.Reason = "failed", "result_unavailable"
			}
			values := []providerRestoreFact{old, newer}
			if reverse {
				values = []providerRestoreFact{newer, old}
			}
			for _, v := range values {
				Expect(RetainProviderOrphans(ctx, destination, bytes.NewReader(jsonBytes(v)), nil)).To(Succeed())
			}
			db, e := openCopy(destination)
			Expect(e).NotTo(HaveOccurred())
			var raw []byte
			Expect(db.QueryRow(`SELECT value FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&raw)).To(Succeed())
			if !conflict {
				var state, reason string
				Expect(db.QueryRow(`SELECT state,reason FROM provider_attempts WHERE attempt_id=?`, a.ID).Scan(&state, &reason)).To(Succeed())
				if terminal == "forbidden" {
					Expect(state).To(Equal("failed"))
					Expect(reason).To(Equal("result_unavailable"))
				} else {
					Expect(state).To(Equal("outcome_unknown"))
					Expect(reason).To(Equal("result_retrieval_pending"))
				}
			}
			Expect(db.Close()).To(Succeed())
			var got providerRestoreFact
			Expect(json.Unmarshal(raw, &got)).To(Succeed())
			if terminal == "forbidden" {
				Expect(got.State).To(Equal("failed"))
				Expect(got.Reason).To(Equal("result_unavailable"))
				continue
			}
			Expect(got.State).To(Equal("outcome_unknown"))
			if conflict {
				Expect(hasProviderConflict(got.Conflict)).To(BeTrue())
			} else {
				Expect(got.Reason).To(Equal("result_retrieval_pending"))
			}
		}
	}
})

var _ = It("C03W4 completed primary survives inconclusive observations but conflicting reliable terminals are refused", Label("persistence"), func() {
	s, a := providerFixture()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		return tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "request", "session", "result_retrieval_pending")
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "request", "session", "transport_unknown")
	})).To(Succeed())
	for _, state := range []string{"failed", "cancelled"} {
		reason := "provider_failed"
		if state == "cancelled" {
			reason = "cancel_confirmed"
		}
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			return tx.ObserveProviderAttempt(a.ID, state, "request", "session", reason)
		})).To(MatchError(domain.ErrConflict))
	}
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		actual, e := tx.ProviderAttempt(a.ID)
		if e != nil {
			return e
		}
		Expect(actual.Reason).To(Equal("result_retrieval_pending"))
		return tx.ObserveProviderAttempt(a.ID, "failed", "request", "session", "result_unavailable")
	})).To(Succeed())
})

var _ = It("C03W4 no-send required audit failure remains diagnostic without replacing its primary refusal", Label("persistence"), func() {
	s, a := providerFixture(false)
	faults := &providerAccountingStore{Store: s}
	faults.failPreparedAudit.Store(true)
	p := &work.ProviderCoordinator{Store: faults, Driver: preparationCleanupDriver{store: s, audit: true, clean: true}}
	Expect(p.Process(ctx, a.JobID)).To(HaveOccurred())
	Expect(p.PollProvider(ctx)).To(Succeed())
	Expect(p.StopProviders()).To(HaveOccurred())
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.Error).To(Equal("dispatch_not_authorized"))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(a.JobID)
		if e == nil {
			Expect(rows[0].State).To(Equal("prepared"))
			Expect(rows[0].Transport.CleanupState).To(BeEmpty())
		}
		return e
	})).To(Succeed())
})

var _ = It("C03W4 ordinary no-send transport or fence failure is handled while required storage failure is retained", Label("persistence"), func() {
	for _, reason := range []string{"provider_unavailable", "provider_accounting_unavailable"} {
		s, a := providerFixture(false)
		d := &providerFake{preparationError: io.ErrClosedPipe, preparationReason: reason}
		p := &work.ProviderCoordinator{Store: s, Driver: d}
		e := p.Process(ctx, a.JobID)
		if reason == "provider_unavailable" {
			Expect(e).NotTo(HaveOccurred())
			Expect(p.StopProviders()).To(Succeed())
		} else {
			Expect(e).To(HaveOccurred())
			Expect(p.StopProviders()).To(HaveOccurred())
		}
		Expect(d.calls).To(BeZero())
		j, e := s.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.State).To(Equal("paused"))
		Expect(j.Error).To(Equal(reason))
	}
})

var _ = It("C03W5 collected drained accounting diagnostics stay distinct from current and later unsafe phase errors", Label("persistence"), func() {
	for _, mode := range []string{"current", "later_unsafe", "later_safe"} {
		s, first := providerFixture(false)
		faults := &providerAccountingStore{Store: s}
		faults.failTransport.Store(true)
		driver := &providerTerminalDriver{store: s, state: "result_saved"}
		p := &work.ProviderCoordinator{Store: faults, Bytes: providerFixtureBytes{s}, Driver: driver}
		Expect(p.Process(ctx, first.JobID)).To(HaveOccurred())
		historical := p.StopProviders()
		Expect(work.ProviderRestartDiagnostics(historical)).To(BeTrue())
		Expect(errors.Is(historical, selectiveProviderFault)).To(BeTrue())
		var originalHash string
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.ProviderAttempts(first.JobID)
			if e == nil {
				originalHash = rows[0].ResultHash
				Expect(originalHash).To(HaveLen(64))
			}
			return e
		})).To(Succeed())
		second, e := providerEmptyJob(s)
		Expect(e).NotTo(HaveOccurred())
		job, e := s.Job(ctx, second.JobID)
		Expect(e).NotTo(HaveOccurred())
		var scope domain.ProviderScope
		Expect(json.Unmarshal(job.Scope, &scope)).To(Succeed())
		scope.RequestSHA = strings.Repeat("d", 64)
		_, e = s.db.Exec(`UPDATE jobs SET scope=? WHERE job_id=?`, jsonBytes(scope), job.ID)
		Expect(e).NotTo(HaveOccurred())
		driver.cleanupError = mode == "later_unsafe"
		Expect(p.ScheduleProvider(ctx, second.JobID)).To(Succeed())
		Eventually(func() string { job, _ := s.Job(ctx, second.JobID); return job.State }, 3*time.Second).Should(Equal("paused"))
		if mode != "current" {
			Eventually(func() error { Expect(p.PollProvider(ctx)).To(Succeed()); return p.ScheduleProvider(ctx, "missing-job") }).Should(MatchError(domain.ErrNotFound))
		}
		stopped := p.StopProviders()
		Expect(work.ProviderRestartDiagnostics(stopped)).To(Equal(mode == "later_safe"), mode)
		Expect(errors.Is(stopped, selectiveProviderFault)).To(BeTrue())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.ProviderAttempts(first.JobID)
			if e == nil {
				Expect(rows[0].ResultHash).To(Equal(originalHash))
			}
			return e
		})).To(Succeed())
		Expect(driver.calls.Load()).To(Equal(int64(2)))
	}
})

var _ = It("C03W5 missing drain evidence cannot promote a durable no-send accounting error to restart-safe", Label("persistence"), func() {
	s, a := providerFixture(false)
	p := &work.ProviderCoordinator{Store: s, Driver: &providerFake{preparationError: io.ErrClosedPipe, preparationReason: "provider_accounting_unavailable"}}
	Expect(p.Process(ctx, a.JobID)).To(HaveOccurred())
	Expect(work.ProviderRestartDiagnostics(p.StopProviders())).To(BeFalse())
})

type setupReasonDriver struct {
	providerFake
	cancel context.CancelFunc
	fence  bool
}

func (d *setupReasonDriver) Execute(_ context.Context, _ domain.ProviderAttempt, _ []domain.ProviderInput, _ func() error, _ func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	if d.cancel != nil {
		d.cancel()
		return work.ProviderOutcome{Reason: "setup_timeout"}, context.Canceled
	}
	if d.fence {
		return work.ProviderOutcome{Reason: "setup_timeout"}, domain.ErrStaleWorker
	}
	return work.ProviderOutcome{Reason: "setup_timeout"}, context.DeadlineExceeded
}

var _ = It("C03W5 durable no-send settlement distinguishes internal setup timeout caller cancellation and owner fence", Label("persistence"), func() {
	for _, mode := range []string{"internal", "caller", "fence"} {
		s, a := providerFixture(false)
		driver := &setupReasonDriver{fence: mode == "fence"}
		caller, cancel := context.WithCancel(ctx)
		defer cancel()
		if mode == "caller" {
			driver.cancel = cancel
		}
		p := &work.ProviderCoordinator{Store: s, Driver: driver}
		Expect(p.Process(caller, a.JobID)).To(Succeed())
		Expect(p.StopProviders()).To(Succeed())
		j, e := s.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.State).To(Equal("paused"))
		reason := "dispatch_not_authorized"
		if mode == "internal" {
			reason = "setup_timeout"
			Expect(caller.Err()).NotTo(HaveOccurred())
		}
		Expect(j.Error).To(Equal(reason))
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.ProviderAttempts(a.JobID)
			if e == nil {
				Expect(rows).To(HaveLen(1))
				Expect(rows[0].State).To(Equal("prepared"))
				Expect(rows[0].MayHaveDispatched).To(BeFalse())
				Expect(rows[0].RequestID).To(BeEmpty())
				Expect(rows[0].SessionID).To(BeEmpty())
				Expect(rows[0].Transport).To(BeNil())
			}
			return e
		})).To(Succeed())
	}
})

var _ = It("C03W5 positive signal-error drain remains historical-safe while unknown drain reasons refuse restart", Label("persistence"), func() {
	for _, reason := range []string{"signal_error_drained", "group_probe_failed"} {
		s, a := providerFixture(false)
		faults := &providerAccountingStore{Store: s}
		faults.failTransport.Store(true)
		driver := &providerTerminalDriver{store: s, state: "result_saved", cleanupReason: reason}
		p := &work.ProviderCoordinator{Store: faults, Bytes: providerFixtureBytes{s}, Driver: driver}
		Expect(p.Process(ctx, a.JobID)).To(HaveOccurred())
		stopped := p.StopProviders()
		Expect(errors.Is(stopped, selectiveProviderFault)).To(BeTrue())
		Expect(work.ProviderRestartDiagnostics(stopped)).To(Equal(reason == "signal_error_drained"))
	}
})

type restartWriteFaultStore struct {
	app.Store
	first      atomic.Bool
	everyPause bool
	fault      error
}
type restartWriteFaultTx struct {
	app.WriteTx
	owner *restartWriteFaultStore
}

func (s *restartWriteFaultStore) Write(ctx context.Context, f func(app.WriteTx) error) error {
	if s.first.Swap(false) {
		return s.fault
	}
	return s.Store.Write(ctx, func(tx app.WriteTx) error { return f(restartWriteFaultTx{tx, s}) })
}
func (t restartWriteFaultTx) PauseProviderJob(job, reason string) error {
	if t.owner.everyPause {
		return t.owner.fault
	}
	return t.WriteTx.PauseProviderJob(job, reason)
}

var _ = It("C03W5 actual fallback writer failure remains unsafe despite positive drain and preserves saved output", Label("persistence"), func() {
	s, a := providerFixture(false)
	faults := &restartWriteFaultStore{Store: s, everyPause: true, fault: selectiveProviderFault}
	p := &work.ProviderCoordinator{Store: faults, Bytes: providerFixtureBytes{s}, Driver: &providerTerminalDriver{store: s, state: "result_saved"}, Apply: func(app.WriteTx, domain.ProviderAttempt) error { return selectiveProviderFault }}
	Expect(p.Process(ctx, a.JobID)).To(HaveOccurred())
	Expect(work.ProviderRestartDiagnostics(p.StopProviders())).To(BeFalse())
	Expect(errors.Is(p.StopProviders(), selectiveProviderFault)).To(BeTrue())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(a.JobID)
		if e == nil {
			Expect(rows[0].State).To(Equal("result_saved"))
			Expect(rows[0].ResultHash).To(HaveLen(64))
		}
		return e
	})).To(Succeed())
})
var _ = It("C03W5 Stop preserves an independent preparation I/O leaf joined with each previously benign cancellation or refusal", Label("persistence"), func() {
	for _, benign := range []error{context.Canceled, domain.ErrDenied, domain.ErrStaleWorker, domain.ErrTransition, domain.ErrPrecondition, domain.ErrReconciliation} {
		s, a := providerFixture(false)
		mixed := errors.Join(benign, io.ErrClosedPipe)
		faults := &restartWriteFaultStore{Store: s, fault: mixed}
		faults.first.Store(true)
		p := &work.ProviderCoordinator{Store: faults, Driver: &providerFake{}}
		Expect(p.ScheduleProvider(ctx, a.JobID)).To(Succeed())
		Eventually(func() string { j, _ := s.Job(ctx, a.JobID); return j.State }, 3*time.Second).Should(Equal("paused"))
		stopped := p.StopProviders()
		Expect(errors.Is(stopped, io.ErrClosedPipe)).To(BeTrue(), benign.Error())
		Expect(work.ProviderRestartDiagnostics(stopped)).To(BeFalse())
	}
})

type localCleanupDriver struct {
	providerFake
	closed  bool
	reason  string
	cleanup error
}

func (d *localCleanupDriver) Execute(context.Context, domain.ProviderAttempt, []domain.ProviderInput, func() error, func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	return work.ProviderOutcome{Reason: "provider_accounting_unavailable", LocalClosed: d.closed, LocalCleanupReason: d.reason, CleanupError: d.cleanup}, selectiveProviderFault
}

var _ = It("C03L6 independent actual local facts permit durable collected history while default unknown failed and invalid-reason cleanup remain unsafe", Label("persistence"), func() {
	for _, row := range []struct {
		closed  bool
		reason  string
		cleanup error
		safe    bool
	}{{true, "", nil, true}, {true, "signal_error_drained", nil, true}, {false, "", nil, false}, {true, "group_probe_failed", nil, false}, {true, "", selectiveProviderFault, false}} {
		s, a := providerFixture(false)
		p := &work.ProviderCoordinator{Store: s, Driver: &localCleanupDriver{closed: row.closed, reason: row.reason, cleanup: row.cleanup}}
		Expect(p.Process(ctx, a.JobID)).To(HaveOccurred())
		stopped := p.StopProviders()
		Expect(errors.Is(stopped, selectiveProviderFault)).To(BeTrue())
		Expect(work.ProviderRestartDiagnostics(stopped)).To(Equal(row.safe))
		j, e := s.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.State).To(Equal("paused"))
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.ProviderAttempts(a.JobID)
			if e == nil {
				Expect(rows[0].Transport).To(BeNil())
				Expect(rows[0].MayHaveDispatched).To(BeFalse())
			}
			return e
		})).To(Succeed())
	}
})
