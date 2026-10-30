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

var selectiveProviderFault = errors.New("controlled provider accounting write failure")

type providerAccountingStore struct {
	app.Store
	failTransport, failLookup atomic.Bool
	failPreparedAudit         atomic.Bool
	failSavedPause            atomic.Bool
	inputFailures             atomic.Int64
}
type providerAccountingTx struct {
	app.WriteTx
	owner *providerAccountingStore
}

func (s *providerAccountingStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error { return fn(providerAccountingTx{tx, s}) })
}
func (t providerAccountingTx) RecordProviderTransport(id string, v domain.ProviderTransport) error {
	if t.owner.failTransport.Load() && v.DriverOutcome != "" || t.owner.failPreparedAudit.Load() && v.CleanupState != "" {
		return selectiveProviderFault
	}
	return t.WriteTx.RecordProviderTransport(id, v)
}
func (t providerAccountingTx) RecordProviderLookup(id, carrier string, v domain.ProviderLookup) error {
	if t.owner.failLookup.Load() {
		return selectiveProviderFault
	}
	return t.WriteTx.RecordProviderLookup(id, carrier, v)
}
func (t providerAccountingTx) ObserveProviderInput(attempt, read, origin string) error {
	n := t.owner.inputFailures.Load()
	if n < 0 || n > 0 && t.owner.inputFailures.CompareAndSwap(n, n-1) {
		return selectiveProviderFault
	}
	return t.WriteTx.ObserveProviderInput(attempt, read, origin)
}
func (t providerAccountingTx) PauseProviderJob(job, reason string) error {
	if reason == "saved_result_requires_continuation" && t.owner.failSavedPause.Swap(false) {
		return selectiveProviderFault
	}
	return t.WriteTx.PauseProviderJob(job, reason)
}
func providerTestAudit(tx app.WriteTx, a domain.ProviderAttempt) (domain.ProviderTransport, error) {
	v := domain.ProviderTransport{Provider: "codex", Version: "0.164.0", BinarySHA: strings.Repeat("b", 64), PreparedAt: "2026-10-29T12:00:00Z", SetupThread: "observed-session", CWD: "/fixture", Approval: "never", Sandbox: "read-only", AuthMode: "chatgpt"}
	inputs, e := tx.ProviderInputs(a.ID)
	if e != nil {
		return v, e
	}
	for _, input := range inputs {
		r, e := tx.CapturedRead(input.ReadID)
		if e != nil {
			return v, e
		}
		o, e := tx.Object(input.Hash)
		if e != nil {
			return v, e
		}
		v.Disclosure = append(v.Disclosure, domain.ProviderDisclosure{ReadID: input.ReadID, Hash: input.Hash, Size: o.Size, Source: r.SourceJSON, Location: r.LocationJSON})
	}
	return v, nil
}

type providerTerminalDriver struct {
	store                 app.Store
	state                 string
	calls                 atomic.Int64
	cleanupError          bool
	cleanupReason         string
	continueAfterACKError bool
	reconcileSaved        bool
}

func (d *providerTerminalDriver) Execute(ctx context.Context, a domain.ProviderAttempt, inputs []domain.ProviderInput, send func() error, ack func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	var audit domain.ProviderTransport
	if e := d.store.Write(ctx, func(tx app.WriteTx) error {
		var e error
		audit, e = providerTestAudit(tx, a)
		if e != nil {
			return e
		}
		return tx.RecordProviderTransport(a.ID, audit)
	}); e != nil {
		return work.ProviderOutcome{}, e
	}
	if e := send(); e != nil {
		return work.ProviderOutcome{Reason: "dispatch_not_authorized"}, e
	}
	d.calls.Add(1)
	for i := range inputs {
		inputs[i].ObservedOrigin = "daemon_supplied"
	}
	out := work.ProviderOutcome{State: d.state, RequestID: "observed-request", SessionID: "observed-session", Observed: inputs}
	ackErr := ack(work.ProviderProgress{RequestID: out.RequestID, SessionID: out.SessionID, Observed: inputs})
	if ackErr != nil && !d.continueAfterACKError {
		return out, ackErr
	}
	audit.AcceptedAt = "2026-10-29T12:00:01Z"
	audit.FinishedAt = "2026-10-29T12:00:02Z"
	audit.DriverOutcome = d.state
	audit.BackendSource = "live_event"
	audit.LocalClosed = !d.cleanupError
	audit.CleanupState = "drained"
	audit.CleanupReason = d.cleanupReason
	switch d.state {
	case "result_saved":
		out.CompletionObserved = true
		out.Body = io.NopCloser(strings.NewReader("complete fake provider output"))
		audit.DriverOutcome = "result_ready"
		audit.BackendState = "completed"
	case "cancelled":
		out.Reason = "cancel_confirmed"
		audit.BackendState = "interrupted"
	default:
		out.State = "failed"
		out.Reason = "provider_failed"
		audit.BackendState = "failed"
	}
	out.Transport = &audit
	if d.cleanupError {
		audit.CleanupState = "unknown"
		audit.CleanupReason = "controlled_cleanup_unknown"
		return out, selectiveProviderFault
	}
	return out, ackErr
}
func (d *providerTerminalDriver) Cancel(context.Context, domain.ProviderAttempt) error { return nil }
func (d *providerTerminalDriver) Reconcile(_ context.Context, a domain.ProviderAttempt, _ bool) (work.ProviderOutcome, error) {
	if d.reconcileSaved {
		return work.ProviderOutcome{State: "result_saved", CompletionObserved: true, Body: io.NopCloser(strings.NewReader("complete recovered output")), RequestID: a.RequestID, SessionID: a.SessionID, Lookup: &domain.ProviderLookup{BackendState: "completed", Source: "history", ObservedAt: "2026-10-29T12:01:00Z", LocalClosed: true}}, nil
	}
	return work.ProviderOutcome{State: "failed", Reason: "provider_failed", RequestID: a.RequestID, SessionID: a.SessionID, Lookup: &domain.ProviderLookup{BackendState: "failed", Source: "history", ObservedAt: "2026-10-29T12:01:00Z", LocalClosed: true}}, nil
}

var _ = It("C03R primary saved/failed/live-interrupted facts survive selective final audit failure and Stop reports it", Label("persistence"), func() {
	for _, state := range []string{"result_saved", "failed", "cancelled"} {
		s, a := providerFixture(false)
		faults := &providerAccountingStore{Store: s}
		faults.failTransport.Store(true)
		driver := &providerTerminalDriver{store: s, state: state}
		p := &work.ProviderCoordinator{Store: faults, Bytes: providerFixtureBytes{s}, Driver: driver}
		Expect(p.ScheduleProvider(ctx, a.JobID)).To(Succeed())
		Eventually(func() string {
			j, e := s.Job(ctx, a.JobID)
			if e != nil {
				return ""
			}
			return j.State
		}, 3*time.Second).Should(Equal("paused"))
		Expect(p.StopProviders()).To(MatchError(ContainSubstring("provider accounting failed")))
		var saved domain.ProviderAttempt
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.ProviderAttempts(a.JobID)
			if e != nil {
				return e
			}
			Expect(rows).To(HaveLen(1))
			saved = rows[0]
			return nil
		})).To(Succeed())
		Expect(saved.State).To(Equal(state))
		Expect(saved.RequestID).To(Equal("observed-request"))
		Expect(saved.Transport.DriverOutcome).To(BeEmpty(), "failed final audit was not fabricated")
		j, e := s.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.Error).To(Equal("provider_accounting_unavailable"))
		if state == "result_saved" {
			Expect(saved.ResultHash).To(HaveLen(64))
			faults.failTransport.Store(false)
			p.Bytes = providerContinuationFixtureBytes{providerFixtureBytes{s}}
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				_, e := tx.ResumeProviderJob(a.JobID, j.AttemptGeneration, "continue")
				return e
			})).To(Succeed())
			Expect(p.Process(ctx, a.JobID)).To(Succeed())
			Expect(driver.calls.Load()).To(Equal(int64(1)))
			j, e = s.Job(ctx, a.JobID)
			Expect(e).NotTo(HaveOccurred())
			Expect(j.State).To(Equal("completed"))
		}
	}
})
var _ = It("C03R cleanup uncertainty preserves a saved result and handled accounting errors do not end Dispatcher", Label("persistence"), func() {
	s, a := providerFixture(false)
	faults := &providerAccountingStore{Store: s}
	faults.failTransport.Store(true)
	driver := &providerTerminalDriver{store: s, state: "result_saved"}
	p := &work.ProviderCoordinator{Store: faults, Bytes: providerFixtureBytes{s}, Driver: driver}
	run, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (work.Dispatcher{Store: s, Provider: p}).Run(run) }()
	Eventually(func() string { j, _ := s.Job(ctx, a.JobID); return j.Error }, 3*time.Second).Should(Equal("provider_accounting_unavailable"))
	Consistently(done, 150*time.Millisecond).ShouldNot(Receive())
	cancel()
	Eventually(done, 3*time.Second).Should(Receive())
	s, a = providerFixture(false)
	driver = &providerTerminalDriver{store: s, state: "result_saved", cleanupError: true}
	p = &work.ProviderCoordinator{Store: s, Bytes: providerFixtureBytes{s}, Driver: driver}
	Expect(p.Process(ctx, a.JobID)).To(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(a.JobID)
		if e != nil {
			return e
		}
		Expect(rows[0].State).To(Equal("result_saved"))
		Expect(rows[0].Transport.BackendState).To(Equal("completed"))
		Expect(rows[0].Transport.LocalClosed).To(BeFalse())
		return nil
	})).To(Succeed())
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
})

func providerExecutingLookup() (*Store, domain.ProviderAttempt) {
	s, a := providerFixture()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		audit, e := providerTestAudit(tx, a)
		if e != nil {
			return e
		}
		if e = tx.RecordProviderTransport(a.ID, audit); e != nil {
			return e
		}
		if e = tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		if e = tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "observed-request", "observed-session", "transport_unknown"); e != nil {
			return e
		}
		return tx.PauseProviderJob(a.JobID, "outcome_unknown")
	})).To(Succeed())
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if _, e := tx.ResumeProviderJob(a.JobID, j.AttemptGeneration, "reconcile_only"); e != nil {
			return e
		}
		return tx.ClaimProviderReconciliation(a.JobID, a.ID)
	})).To(Succeed())
	return s, a
}

var _ = It("C03R history-interrupted lookup refines monotonically and reliable terminal conflicts remain retained", Label("persistence"), func() {
	s, a := providerExecutingLookup()
	for _, state := range []string{"interrupted", "inProgress", "completed"} {
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			return tx.RecordProviderLookup(a.ID, a.JobID, domain.ProviderLookup{BackendState: state, Source: "history", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), LocalClosed: true})
		})).To(Succeed())
	}
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.RecordProviderLookup(a.ID, a.JobID, domain.ProviderLookup{BackendState: "failed", Source: "history", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	})).To(MatchError(domain.ErrConflict))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		actual, e := tx.ProviderAttempt(a.ID)
		Expect(actual.Transport.LastLookup.BackendState).To(Equal("completed"))
		return e
	})).To(Succeed())
})
var _ = It("C03R real SQLite executing intent authorizes payload while missing/pending/stale/cancelled/terminal states do not", Label("persistence"), func() {
	for _, mode := range []string{"valid", "missing", "pending", "runtime", "cancel", "terminal", "epoch", "permissions", "policy", "hidden"} {
		s, a := providerExecutingLookup()
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.GuardProviderPayload(a.JobID, a.ID, true) })).To(Succeed())
		switch mode {
		case "missing":
			_, e := s.db.Exec(`DELETE FROM provider_reconcile_intents WHERE job_id=?`, a.JobID)
			Expect(e).NotTo(HaveOccurred())
		case "pending":
			_, e := s.db.Exec(`UPDATE provider_reconcile_intents SET state='pending' WHERE job_id=?`, a.JobID)
			Expect(e).NotTo(HaveOccurred())
		case "runtime":
			_, e := s.db.Exec(`UPDATE provider_reconcile_intents SET runtime_generation='stale' WHERE job_id=?`, a.JobID)
			Expect(e).NotTo(HaveOccurred())
		case "cancel":
			_, e := s.db.Exec(`UPDATE jobs SET cancel_requested=1 WHERE job_id=?`, a.JobID)
			Expect(e).NotTo(HaveOccurred())
		case "terminal":
			_, e := s.db.Exec(`UPDATE jobs SET state='cancelled' WHERE job_id=?`, a.JobID)
			Expect(e).NotTo(HaveOccurred())
		case "epoch":
			_, e := s.db.Exec(`UPDATE libraries SET auth_epoch='changed'`)
			Expect(e).NotTo(HaveOccurred())
		case "permissions":
			_, e := s.db.Exec(`UPDATE projects SET permissions_revision=permissions_revision+1`)
			Expect(e).NotTo(HaveOccurred())
		case "policy":
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				lib, e := tx.Library()
				if e != nil {
					return e
				}
				scope, e := tx.Scope(lib.UnfiledProjectID)
				if e != nil {
					return e
				}
				return tx.ApplyProjectConfiguration(scope.Project.ID, "", "", true, scope.Project.PermissionsRevision)
			})).To(Succeed())
		case "hidden":
			var hiddenScope []byte
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				lib, e := tx.Library()
				if e != nil {
					return e
				}
				doc := id(GinkgoT())
				if e = tx.CreateDocument(domain.Document{ID: doc, ProjectID: lib.UnfiledProjectID, Title: "hidden"}); e != nil {
					return e
				}
				j, e := tx.Job(a.JobID)
				if e != nil {
					return e
				}
				var scope domain.ProviderScope
				if e = json.Unmarshal(j.Scope, &scope); e != nil {
					return e
				}
				scope.Target = jsonBytes(map[string]string{"document_id": doc, "project_id": lib.UnfiledProjectID})
				hiddenScope = jsonBytes(scope)
				return tx.TestTrashDocument(doc)
			})).To(Succeed())
			_, e := s.db.Exec(`UPDATE jobs SET scope=? WHERE job_id=?`, hiddenScope, a.JobID)
			Expect(e).NotTo(HaveOccurred())
		}
		e := s.Write(ctx, func(tx app.WriteTx) error { return tx.GuardProviderPayload(a.JobID, a.ID, true) })
		if mode == "valid" {
			Expect(e).NotTo(HaveOccurred())
		} else {
			Expect(e).To(HaveOccurred(), mode)
		}
		e = s.Write(ctx, func(tx app.WriteTx) error { return tx.GuardProviderLookup(a.JobID, a.ID) })
		if mode == "cancel" || mode == "terminal" || mode == "valid" || mode == "permissions" || mode == "policy" || mode == "hidden" {
			Expect(e).NotTo(HaveOccurred(), "status-only observation remains separate")
		} else {
			Expect(e).To(HaveOccurred(), mode)
		}
	}
})

var _ = It("C03R failed lookup audit abandons its finite campaign while committed native failure survives", Label("persistence"), func() {
	s, a := providerExecutingLookup()
	_, e := s.db.Exec(`UPDATE provider_reconcile_intents SET state='pending',runtime_generation='' WHERE job_id=?`, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	faults := &providerAccountingStore{Store: s}
	faults.failLookup.Store(true)
	driver := &providerTerminalDriver{store: s}
	p := work.ProviderCoordinator{Store: faults, Driver: driver, Bytes: providerFixtureBytes{s}}
	Expect(p.Process(ctx, a.JobID)).To(HaveOccurred())
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(j.Error).To(Equal("provider_accounting_unavailable"))
	Expect(j.ProviderAttempts.Unknown).To(BeZero())
	Expect(j.ProviderAttempts.ReconciliationPending).To(BeZero())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		actual, e := tx.ProviderAttempt(a.ID)
		if e != nil {
			return e
		}
		Expect(actual.State).To(Equal("failed"))
		Expect(actual.Transport.LastLookup).To(BeNil())
		jobs, e := tx.WorkJobs(64)
		if e != nil {
			return e
		}
		for _, candidate := range jobs {
			Expect(candidate.ID).NotTo(Equal(a.JobID))
		}
		return nil
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.ResumeProviderJob(a.JobID, j.AttemptGeneration, "reconcile_only")
		return e
	})).To(MatchError(domain.ErrTransition))
})

var _ = It("C03R protected restore merges transport observations in both directions without losing newer lookup or empty-core progress", Label("persistence"), func() {
	for _, terminalFirst := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			s, a := providerFixture()
			destination := filepath.Join(GinkgoT().TempDir(), "restore.sqlite")
			Expect(s.SnapshotDatabase(ctx, destination)).To(Succeed())
			var fact providerRestoreFact
			var old domain.ProviderTransport
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				var e error
				old, e = providerTestAudit(tx, a)
				if e != nil {
					return e
				}
				j, e := tx.Job(a.JobID)
				if e != nil {
					return e
				}
				var scope domain.ProviderScope
				if e = json.Unmarshal(j.Scope, &scope); e != nil {
					return e
				}
				inputs, e := tx.ProviderInputs(a.ID)
				if e != nil {
					return e
				}
				yes := true
				fact = providerRestoreFact{ID: a.ID, SHA: a.RequestSHA, State: "outcome_unknown", Generation: a.Generation, RequestID: "observed-request", SessionID: "observed-session", JobID: a.JobID, PrincipalID: scope.PrincipalID, Target: scope.Target, Inputs: inputs, MayHaveDispatched: &yes}
				return nil
			})).To(Succeed())
			newer := old
			newer.BackendState = "inProgress"
			newer.BackendSource = "history"
			newer.FinishedAt = "2026-10-29T12:00:02Z"
			newer.AcceptedAt = "2026-10-29T12:00:01Z"
			newer.LastLookup = &domain.ProviderLookup{BackendState: "inProgress", Source: "history", ObservedAt: "2026-10-29T12:01:00Z", LocalClosed: true}
			left, right := fact, fact
			if terminalFirst {
				left.State = "failed"
				left.Reason = "provider_failed"
				old.BackendState = "failed"
				old.BackendSource = "live_event"
				old.FinishedAt = "2026-10-29T12:00:02Z"
				newer.BackendState = "failed"
				newer.LastLookup.BackendState = "failed"
			}
			left.Transport = &old
			right.Transport = &newer
			values := []providerRestoreFact{left, right}
			if reverse {
				values = []providerRestoreFact{right, left}
			}
			for _, v := range values {
				Expect(RetainProviderOrphans(ctx, destination, bytes.NewReader(jsonBytes(v)), nil)).To(Succeed())
			}
			db, e := openCopy(destination)
			Expect(e).NotTo(HaveOccurred())
			var first []byte
			Expect(db.QueryRow(`SELECT value FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&first)).To(Succeed())
			Expect(db.Close()).To(Succeed())
			var merged providerRestoreFact
			Expect(json.Unmarshal(first, &merged)).To(Succeed())
			Expect(merged.Transport.LastLookup.ObservedAt).To(Equal(newer.LastLookup.ObservedAt))
			if terminalFirst {
				Expect(merged.State).To(Equal("failed"), "reverse=%v fact=%s", reverse, first)
				Expect(merged.Transport.BackendState).To(Equal("failed"))
			} else {
				Expect(merged.Transport.BackendState).To(Equal("inProgress"))
			}
			Expect(RetainProviderOrphans(ctx, destination, bytes.NewReader(jsonBytes(values[1])), nil)).To(Succeed())
			db, e = openCopy(destination)
			Expect(e).NotTo(HaveOccurred())
			var repeated []byte
			Expect(db.QueryRow(`SELECT value FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&repeated)).To(Succeed())
			Expect(db.Close()).To(Succeed())
			Expect(repeated).To(Equal(first))
		}
	}
})

var _ = It("C03R ACK observation failure recovers accepted inputs or preserves known native terminal independently", Label("persistence"), func() {
	for _, persistent := range []bool{false, true} {
		s, a := providerFixture(false)
		faults := &providerAccountingStore{Store: s}
		if persistent {
			faults.inputFailures.Store(-1)
		} else {
			faults.inputFailures.Store(1)
		}
		driver := &providerTerminalDriver{store: s, state: "failed", continueAfterACKError: true}
		p := work.ProviderCoordinator{Store: faults, Bytes: providerFixtureBytes{s}, Driver: driver}
		Expect(p.Process(ctx, a.JobID)).To(HaveOccurred())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.ProviderAttempts(a.JobID)
			if e != nil {
				return e
			}
			Expect(rows).To(HaveLen(1))
			Expect(rows[0].State).To(Equal("failed"))
			Expect(rows[0].RequestID).To(Equal("observed-request"))
			Expect(rows[0].Reason).To(Equal("provider_failed"))
			inputs, e := tx.ProviderInputs(rows[0].ID)
			if e != nil {
				return e
			}
			Expect(inputs).To(HaveLen(1))
			if persistent {
				Expect(inputs[0].ObservedOrigin).To(BeEmpty())
				Expect(rows[0].Transport.DriverOutcome).To(BeEmpty())
			} else {
				Expect(inputs[0].ObservedOrigin).To(Equal("daemon_supplied"))
				Expect(rows[0].Transport.BackendState).To(Equal("failed"))
			}
			return nil
		})).To(Succeed())
		j, e := s.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.State).To(Equal("paused"))
		if persistent {
			Expect(j.Error).To(Equal("provider_accounting_unavailable"))
		}
	}
})

var _ = It("C03R Abandon is exact-owner bounded, removes same finite pending siblings and never fabricates carrier completion", Label("persistence"), func() {
	s, a := providerExecutingLookup()
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	var runtime string
	Expect(s.db.QueryRow(`SELECT runtime_generation FROM provider_reconcile_intents WHERE job_id=? AND attempt_id=?`, a.JobID, a.ID).Scan(&runtime)).To(Succeed())
	for _, mismatch := range []string{"runtime", "generation"} {
		wrongRuntime, wrongGeneration := runtime, j.AttemptGeneration
		if mismatch == "runtime" {
			wrongRuntime = "stale"
		} else {
			wrongGeneration++
		}
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			return tx.AbandonProviderReconciliation(a.JobID, a.ID, wrongRuntime, wrongGeneration, "provider_accounting_unavailable")
		})).To(HaveOccurred())
	}
	// A second finite campaign member must not survive as an automatic retry.
	_, e = s.db.Exec(`INSERT INTO provider_orphans(attempt_id,request_sha256,state,value) VALUES(?,?,'outcome_unknown',?)`, "finite-sibling", a.RequestSHA, []byte(`{"attempt_id":"finite-sibling"}`))
	Expect(e).NotTo(HaveOccurred())
	_, e = s.db.Exec(`INSERT INTO provider_reconcile_intents(job_id,attempt_id,orphan_attempt_id,state,runtime_generation) VALUES(?,?,?,'pending',?)`, a.JobID, "finite-sibling", "finite-sibling", runtime)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.AbandonProviderReconciliation(a.JobID, a.ID, runtime, j.AttemptGeneration, "provider_accounting_unavailable")
	})).To(Succeed())
	var count int
	Expect(s.db.QueryRow(`SELECT count(*) FROM provider_reconcile_intents WHERE job_id=?`, a.JobID).Scan(&count)).To(Succeed())
	Expect(count).To(BeZero())
	Expect(s.db.QueryRow(`SELECT count(*) FROM resource_revisions WHERE resource_kind='provider_reconciliation_authority' AND resource_id=?`, a.JobID).Scan(&count)).To(Succeed())
	Expect(count).To(BeZero())
	j, e = s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(j.Result).To(BeEmpty())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		actual, e := tx.ProviderAttempt(a.ID)
		if e == nil {
			Expect(actual.State).To(Equal("outcome_unknown"))
		}
		return e
	})).To(Succeed())
})

var _ = It("C03R explicit current-epoch finite status authority survives ordinary reopen without restoring payload authority", Label("persistence"), func() {
	s, a := providerExecutingLookup()
	var runtime string
	Expect(s.db.QueryRow(`SELECT runtime_generation FROM libraries`).Scan(&runtime)).To(Succeed())
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	_, e = s.db.Exec(`UPDATE libraries SET auth_epoch=?`, id(GinkgoT()))
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.GuardProviderLookup(a.JobID, a.ID) })).To(MatchError(domain.ErrDenied))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.AbandonProviderReconciliation(a.JobID, a.ID, runtime, j.AttemptGeneration, "provider_accounting_unavailable")
	})).To(Succeed())
	j, e = s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "reconcile_only")
		if e != nil {
			return e
		}
		return tx.ClaimProviderReconciliation(j.ID, a.ID)
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.GuardProviderLookup(a.JobID, a.ID) })).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.GuardProviderPayload(a.JobID, a.ID, true) })).To(MatchError(domain.ErrDenied))
	profile := s.profile
	Expect(s.Close()).To(Succeed())
	reopened := New(nil)
	_, closeDB, e := reopened.Prepare(ctx, profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeDB()
	Expect(reopened.Write(ctx, func(tx app.WriteTx) error { return tx.ReconcileJob(a.JobID) })).To(Succeed())
	Expect(reopened.Write(ctx, func(tx app.WriteTx) error { return tx.ClaimProviderReconciliation(a.JobID, a.ID) })).To(Succeed())
	Expect(reopened.Write(ctx, func(tx app.WriteTx) error { return tx.GuardProviderLookup(a.JobID, a.ID) })).To(Succeed())
	Expect(reopened.Write(ctx, func(tx app.WriteTx) error { return tx.GuardProviderPayload(a.JobID, a.ID, true) })).To(MatchError(domain.ErrDenied))
	Expect(reopened.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.ObserveProviderAttempt(a.ID, "failed", a.RequestID, a.SessionID, "provider_failed"); e != nil {
			return e
		}
		return tx.CompleteProviderReconciliation(a.JobID, a.ID)
	})).To(Succeed())
	var grants int
	Expect(reopened.db.QueryRow(`SELECT count(*) FROM resource_revisions WHERE resource_kind='provider_reconciliation_authority' AND resource_id=?`, a.JobID).Scan(&grants)).To(Succeed())
	Expect(grants).To(BeZero())
})

var _ = It("C03R uncertain Put response never demotes an independently committed saved result", Label("persistence"), func() {
	s, a := providerFixture(false)
	p := work.ProviderCoordinator{Store: s, Driver: &providerTerminalDriver{store: s, state: "result_saved"}, Bytes: providerCommittedErrorBytes{s}}
	Expect(p.Process(ctx, a.JobID)).To(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(a.JobID)
		if e == nil {
			Expect(rows[0].State).To(Equal("result_saved"))
			Expect(rows[0].ResultHash).To(HaveLen(64))
			Expect(rows[0].Transport.BackendState).To(Equal("completed"))
		}
		return e
	})).To(Succeed())
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(j.Error).To(Equal("provider_accounting_unavailable"))
})

type providerCommittedErrorBytes struct{ s *Store }

func (b providerCommittedErrorBytes) Put(ctx context.Context, body io.Reader, commit func(app.WriteTx, domain.Object) error) (domain.Object, error) {
	o, e := (providerFixtureBytes{b.s}).Put(ctx, body, commit)
	if e != nil {
		return o, e
	}
	return o, selectiveProviderFault
}

var _ = It("C03R post-audit saved-result pause failure uses the current reconciliation generation and retains saved output", Label("persistence"), func() {
	s, a := providerExecutingLookup()
	_, e := s.db.Exec(`UPDATE provider_reconcile_intents SET state='pending',runtime_generation='' WHERE job_id=?`, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	_, e = s.db.Exec(`UPDATE jobs SET attempt_generation=attempt_generation+2 WHERE job_id=?`, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	faults := &providerAccountingStore{Store: s}
	faults.failSavedPause.Store(true)
	p := work.ProviderCoordinator{Store: faults, Bytes: providerFixtureBytes{s}, Driver: &providerTerminalDriver{store: s, reconcileSaved: true}}
	Expect(p.Process(ctx, a.JobID)).To(HaveOccurred())
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.AttemptGeneration).To(Equal(a.Generation + 2))
	Expect(j.State).To(Equal("paused"))
	Expect(j.Error).To(Equal("provider_accounting_unavailable"))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		actual, e := tx.ProviderAttempt(a.ID)
		if e == nil {
			Expect(actual.State).To(Equal("result_saved"))
			Expect(actual.Transport.LastLookup.BackendState).To(Equal("completed"))
		}
		return e
	})).To(Succeed())
	var pending int
	Expect(s.db.QueryRow(`SELECT count(*) FROM provider_reconcile_intents WHERE job_id=?`, a.JobID).Scan(&pending)).To(Succeed())
	Expect(pending).To(BeZero())
})

var _ = It("C03R Stop retains a collected accounting error while a later healthy phase remains active", Label("persistence"), func() {
	s, first := providerFixture(false)
	faults := &providerAccountingStore{Store: s}
	faults.failTransport.Store(true)
	driver := &providerTerminalDriver{store: s, state: "result_saved"}
	p := &work.ProviderCoordinator{Store: faults, Bytes: providerFixtureBytes{s}, Driver: driver}
	Expect(p.Process(ctx, first.JobID)).To(HaveOccurred())
	faults.failTransport.Store(false)
	second, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	j, e := s.Job(ctx, second.JobID)
	Expect(e).NotTo(HaveOccurred())
	var scope domain.ProviderScope
	Expect(json.Unmarshal(j.Scope, &scope)).To(Succeed())
	scope.RequestSHA = strings.Repeat("d", 64)
	_, e = s.db.Exec(`UPDATE jobs SET scope=? WHERE job_id=?`, jsonBytes(scope), j.ID)
	Expect(e).NotTo(HaveOccurred())
	Expect(p.ScheduleProvider(ctx, second.JobID)).To(Succeed())
	Eventually(func() string { j, _ := s.Job(ctx, second.JobID); return j.State }, 3*time.Second).Should(Equal("completed"))
	// No Poll has collected the healthy phase. Shutdown must still retain the
	// independently recorded earlier required accounting failure.
	stopped := p.StopProviders()
	Expect(stopped).To(MatchError(ContainSubstring("provider accounting failed")))
	Expect(strings.Count(stopped.Error(), "provider accounting failed")).To(Equal(1))
	Expect(p.StopProviders()).To(MatchError(stopped.Error()))
	Expect(p.ScheduleProvider(ctx, "missing-job")).To(MatchError(domain.ErrNotFound))
	Expect(driver.calls.Load()).To(Equal(int64(2)))
})
