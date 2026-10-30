package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	v1 "haeryong.local/haeryong/internal/adapters/http/v1"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
)

var _ = It("PA46 actual CLI provider resume journals a lost HTTP response and replays the original operation with its captured If-Match", Label("persistence"), func() {
	f := newSourceCommandFixture(GinkgoT())
	jobID, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	var validator string
	var generation int64
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		target, _ := json.Marshal(map[string]string{"project_id": lib.UnfiledProjectID})
		scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target})
		if e = tx.CreateJob(domain.Job{ID: jobID, Kind: "question", Scope: scope}); e != nil {
			return e
		}
		if e = tx.PauseProviderJob(jobID, "provider_unavailable"); e != nil {
			return e
		}
		j, e := tx.Job(jobID)
		validator = appauth.JobETag(j)
		generation = j.AttemptGeneration
		return e
	})).To(Succeed())
	proxy := &faultProxy{inner: f.handler, host: hostOf(GinkgoT(), f.base), method: http.MethodPost, path: "/resume", drop: true}
	front := httptest.NewServer(proxy)
	defer front.Close()
	root, flags := callerProfile(GinkgoT(), f.sel, f.session, f.token, front.URL)
	command := []string{"jobs", "resume", jobID, "--decision", "continue", "--expected-attempt-generation", fmt.Sprint(generation), "--if-match", validator}
	cli(GinkgoT(), 5, append(append([]string{}, command...), flags...)...)
	pending := pendingOperations(GinkgoT(), root)
	Expect(pending).To(HaveLen(1))
	flags = f.sharedCaller(GinkgoT(), root, f.base)
	output := cli(GinkgoT(), 0, append(append([]string{}, command...), flags...)...)
	var response struct {
		Data struct {
			OperationID string     `json:"operation_id"`
			Result      domain.Job `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(output, &response)).To(Succeed())
	Expect(response.Data.OperationID).To(Equal(pending[0]))
	Expect(response.Data.Result.ID).To(Equal(jobID))
	Expect(pendingOperations(GinkgoT(), root)).To(BeEmpty())
	current, e := f.store.Job(context.Background(), jobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(current.AttemptGeneration).To(Equal(generation))
	Expect(current.CancelRequested).To(BeFalse())
})

var _ = It("PA31 actual CLI job detail retains scoped unknown accounting for a terminal local job without native identifiers", Label("persistence"), func() {
	f := newSourceCommandFixture(GinkgoT())
	jobID, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		target, _ := json.Marshal(map[string]string{"project_id": lib.UnfiledProjectID})
		scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target})
		if e = tx.CreateJob(domain.Job{ID: jobID, Kind: "question", Scope: scope}); e != nil {
			return e
		}
		j, e := tx.ClaimJob(jobID)
		if e != nil {
			return e
		}
		return tx.FinishJob(jobID, j.AttemptGeneration, "", nil)
	})).To(Succeed())
	raw, _ := json.Marshal(map[string]any{"attempt_id": "retained-old-attempt", "job_id": jobID, "request_sha256": strings.Repeat("a", 64), "state": "outcome_unknown", "attempt_generation": 1, "provider_request_id": "private-turn", "provider_session_id": "private-thread"})
	Expect(sqlite.RetainProviderOrphans(context.Background(), filepath.Join(f.sel.DataDir, "storage/library.sqlite"), bytes.NewReader(raw), nil)).To(Succeed())
	_, flags := callerProfile(GinkgoT(), f.sel, f.session, f.token, f.base)
	output := cli(GinkgoT(), 0, append([]string{"jobs", "show", jobID}, flags...)...)
	var response struct {
		Data domain.Job `json:"data"`
	}
	Expect(json.Unmarshal(output, &response)).To(Succeed())
	Expect(response.Data.State).To(Equal("completed"))
	Expect(response.Data.ProviderAttempts).NotTo(BeNil())
	Expect(response.Data.ProviderAttempts.Unknown).To(Equal(1))
	Expect(response.Data.ProviderAttempts.UnknownReason).To(Equal("outcome_unknown"))
	Expect(string(output)).NotTo(ContainSubstring("private-turn"))
	Expect(string(output)).NotTo(ContainSubstring("private-thread"))
})

var _ = It("PA65 actual CLI reports permanent saved-result supersession as exit4 and releases its automatic journal", Label("persistence"), func() {
	f := newSourceCommandFixture(GinkgoT())
	jobID, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	attempt, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	var validator string
	var generation int64
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		target, _ := json.Marshal(map[string]string{"project_id": lib.UnfiledProjectID})
		scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, RequestSHA: strings.Repeat("d", 64)})
		if e = tx.CreateJob(domain.Job{ID: jobID, Kind: "question", Scope: scope}); e != nil {
			return e
		}
		j, e := tx.ClaimJob(jobID)
		if e != nil {
			return e
		}
		a := domain.ProviderAttempt{ID: attempt, JobID: jobID, Generation: j.AttemptGeneration, State: "prepared", Runtime: lib.RuntimeGeneration, RequestSHA: strings.Repeat("d", 64)}
		if e = tx.PrepareProviderAttempt(a, nil); e != nil {
			return e
		}
		if e = tx.DispatchProviderAttempt(attempt); e != nil {
			return e
		}
		o := domain.Object{Hash: strings.Repeat("c", 64), Size: 1, State: "ready"}
		if e = tx.PutDurableObject(o); e != nil {
			return e
		}
		if e = tx.SaveProviderResult(attempt, o); e != nil {
			return e
		}
		if e = tx.PauseProviderJob(jobID, "saved"); e != nil {
			return e
		}
		if _, e = tx.ResumeProviderJob(jobID, j.AttemptGeneration, "retry"); e != nil {
			return e
		}
		if _, e = tx.ClaimJob(jobID); e != nil {
			return e
		}
		if e = tx.PauseProviderJob(jobID, "replacement"); e != nil {
			return e
		}
		j, e = tx.Job(jobID)
		validator, generation = appauth.JobETag(j), j.AttemptGeneration
		return e
	})).To(Succeed())
	root, flags := callerProfile(GinkgoT(), f.sel, f.session, f.token, f.base)
	args := []string{"jobs", "resume", jobID, "--decision", "continue", "--expected-attempt-generation", fmt.Sprint(generation), "--if-match", validator}
	output := cli(GinkgoT(), 4, append(args, flags...)...)
	Expect(string(output)).To(ContainSubstring("INVALID_TRANSITION"))
	Expect(pendingOperations(GinkgoT(), root)).To(BeEmpty())
	j, e := f.store.Job(context.Background(), jobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(j.AttemptGeneration).To(Equal(generation))
	Expect(j.Result).To(BeEmpty())
})

// This fixture mounts the real public routes without an automatic worker so the
// requesting CLI reaches the recovered job before any provider coordinator turn.
func providerCommandFixture() sourceCommandFixture {
	GinkgoHelper()
	ctx := context.Background()
	sel := selection(GinkgoT())
	initProfile(GinkgoT(), sel)
	writer, e := service.OpenWriter(sel)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { writer.Close() })
	Expect(writer.PrepareStorageBootstrap(nil)).To(Succeed())
	store := foundation.New(nil)
	_, closeStore, e := store.Prepare(ctx, writer.Profile)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { closeStore() })
	Expect(writer.MarkStorageReady(nil)).To(Succeed())
	auth := appauth.New(store)
	server := httptest.NewUnstartedServer(nil)
	base := "http://" + server.Listener.Addr().String()
	endpoint, e := url.Parse(base)
	Expect(e).NotTo(HaveOccurred())
	handler, e := httpauth.New(auth, httpauth.Policy{Hosts: []string{endpoint.Host}, Origins: []string{base}}, v1.New(query.New(store, auth, store), nil, nil))
	Expect(e).NotTo(HaveOccurred())
	server.Config.Handler = handler
	server.Start()
	DeferCleanup(server.Close)
	pair, e := auth.CreatePairing(ctx, base, "provider command owner", "fixture")
	Expect(e).NotTo(HaveOccurred())
	operation, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	_, e = auth.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: operation, Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	poll, e := auth.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(e).NotTo(HaveOccurred())
	session, e := auth.Authenticate(ctx, poll.Token)
	Expect(e).NotTo(HaveOccurred())
	return sourceCommandFixture{store: store, auth: auth, handler: handler, base: base, token: poll.Token, session: session, sel: sel}
}

var _ = It("PA82 actual CLI cancel normalizes reopened work and orphan-only saved continuation has permanent exit4 with a converged journal", Label("persistence"), func() {
	for _, mode := range []string{"prepared", "unknown", "orphan"} {
		f := providerCommandFixture()
		job, e := service.NewID()
		Expect(e).NotTo(HaveOccurred())
		attempt, e := service.NewID()
		Expect(e).NotTo(HaveOccurred())
		var raw []byte
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			lib, e := tx.Library()
			if e != nil {
				return e
			}
			owner, e := tx.AuthState()
			if e != nil {
				return e
			}
			sha := strings.Repeat("a", 64)
			target, _ := json.Marshal(map[string]string{"project_id": lib.UnfiledProjectID})
			scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, RequestSHA: sha})
			if e = tx.CreateJob(domain.Job{ID: job, Kind: "question", Scope: scope}); e != nil {
				return e
			}
			if mode == "orphan" {
				o := domain.Object{Hash: strings.Repeat("d", 64), Size: 1, State: "ready"}
				if e = tx.PutDurableObject(o); e != nil {
					return e
				}
				if e = tx.AcquirePin(domain.Pin{ID: "cli-independent", Kind: "selected_snapshot", OwnerID: "independent", Hash: o.Hash}); e != nil {
					return e
				}
				raw, _ = json.Marshal(map[string]any{"attempt_id": attempt, "job_id": job, "principal_id": owner.OwnerID, "target": json.RawMessage(target), "request_sha256": sha, "state": "result_saved", "result_hash": o.Hash})
				return tx.PauseProviderJob(job, "restored_saved_result")
			}
			j, e := tx.ClaimJob(job)
			if e != nil {
				return e
			}
			a := domain.ProviderAttempt{ID: attempt, JobID: job, Generation: j.AttemptGeneration, Runtime: lib.RuntimeGeneration, State: "prepared", RequestSHA: sha}
			if e = tx.PrepareProviderAttempt(a, nil); e != nil {
				return e
			}
			if mode == "unknown" {
				return tx.DispatchProviderAttempt(attempt)
			}
			return nil
		})).To(Succeed())
		if mode == "orphan" {
			Expect(sqlite.RetainProviderOrphans(context.Background(), filepath.Join(f.sel.DataDir, "storage/library.sqlite"), bytes.NewReader(raw), func(domain.Object) (bool, error) { return true, nil })).To(Succeed())
		} else {
			profile, e := service.Load(f.sel)
			Expect(e).NotTo(HaveOccurred())
			Expect(f.store.Close()).To(Succeed())
			_, closer, e := f.store.Prepare(context.Background(), profile)
			Expect(e).NotTo(HaveOccurred())
			defer closer()
		}
		before, e := f.store.Job(context.Background(), job)
		Expect(e).NotTo(HaveOccurred())
		root, flags := callerProfile(GinkgoT(), f.sel, f.session, f.token, f.base)
		if mode == "orphan" {
			cli(GinkgoT(), 4, append([]string{"jobs", "resume", job, "--decision", "continue", "--expected-attempt-generation", fmt.Sprint(before.AttemptGeneration), "--if-match", appauth.JobETag(before)}, flags...)...)
			Expect(pendingOperations(GinkgoT(), root)).To(BeEmpty())
			after, e := f.store.Job(context.Background(), job)
			Expect(e).NotTo(HaveOccurred())
			Expect(after).To(Equal(before))
		} else {
			Expect(before.State).To(Equal("interrupted"))
			cli(GinkgoT(), 0, append([]string{"jobs", "cancel", job}, flags...)...)
			after, e := f.store.Job(context.Background(), job)
			Expect(e).NotTo(HaveOccurred())
			Expect(after.CancelRequested).To(BeTrue())
			Expect(after.AttemptGeneration).To(Equal(before.AttemptGeneration + 1))
			Expect(after.Revision).To(BeNumerically(">=", before.Revision+2))
			if mode == "unknown" {
				Expect(after.State).To(Equal("paused"))
				Expect(after.Error).To(Equal("outcome_unknown"))
			} else {
				Expect(after.State).To(Equal("cancelled"))
			}
			Expect(pendingOperations(GinkgoT(), root)).To(BeEmpty())
		}
	}
})

var _ = It("C03PROJECTION private invocation stays out of job views and persisted cancel/resume receipts with coherent ETags", Label("persistence"), func() {
	f := newSourceCommandFixture(GinkgoT())
	ctx := context.Background()
	jobID, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	var principal string
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		principal = owner.OwnerID
		library, e := tx.Library()
		if e != nil {
			return e
		}
		target, _ := json.Marshal(map[string]string{"project_id": library.UnfiledProjectID})
		scope, _ := json.Marshal(domain.ProviderScope{ProviderID: "codex", PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, RequestSHA: strings.Repeat("a", 64), Invocation: &domain.ProviderInvocation{Prompt: "C03_PRIVATE_PROMPT", CWD: "/C03_PRIVATE_CWD", Model: "C03_PRIVATE_MODEL"}})
		if e = tx.CreateJob(domain.Job{ID: jobID, Kind: "question", Scope: scope}); e != nil {
			return e
		}
		return tx.PauseProviderJob(jobID, "provider_unavailable")
	})).To(Succeed())
	assertPrivate := func(v any) {
		raw, e := json.Marshal(v)
		if response, ok := v.(domain.Response); ok {
			raw = response.Body
		}
		if response, ok := v.(*domain.Response); ok && response != nil {
			raw = response.Body
		}
		Expect(e).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("C03_PRIVATE"))
		Expect(string(raw)).NotTo(ContainSubstring(`"invocation"`))
	}
	public, e := f.auth.Job(ctx, f.token, jobID)
	Expect(e).NotTo(HaveOccurred())
	assertPrivate(public)
	jobs, e := f.auth.Jobs(ctx, f.token)
	Expect(e).NotTo(HaveOccurred())
	assertPrivate(jobs)
	actual, e := f.store.Job(ctx, jobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(appauth.JobETag(actual)).To(Equal(appauth.JobETag(public)))
	operation, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	resumed, e := f.auth.ResumeJob(ctx, f.token, jobID, operation, appauth.JobETag(public), appauth.ResumeJobRequest{Decision: "continue", ExpectedGeneration: public.AttemptGeneration})
	Expect(e).NotTo(HaveOccurred())
	assertPrivate(resumed)
	receipt, e := f.store.Receipt(ctx, domain.OperationKey{PrincipalID: principal, OperationID: operation})
	Expect(e).NotTo(HaveOccurred())
	Expect(receipt.Response).NotTo(BeNil())
	assertPrivate(receipt.Response)
	replay, e := f.auth.ResumeJob(ctx, f.token, jobID, operation, appauth.JobETag(public), appauth.ResumeJobRequest{Decision: "continue", ExpectedGeneration: public.AttemptGeneration})
	Expect(e).NotTo(HaveOccurred())
	Expect(replay.Body).To(Equal(resumed.Body))
	assertPrivate(replay)
	cancel, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	cancelled, e := f.auth.CancelJob(ctx, f.token, jobID, cancel)
	Expect(e).NotTo(HaveOccurred())
	assertPrivate(cancelled)
	receipt, e = f.store.Receipt(ctx, domain.OperationKey{PrincipalID: principal, OperationID: cancel})
	Expect(e).NotTo(HaveOccurred())
	assertPrivate(receipt.Response)
})

var _ = It("C03CLI actual provider status emits nullable safe data and canonical authentication errors", Label("persistence"), func() {
	f := providerCommandFixture()
	_, flags := callerProfile(GinkgoT(), f.sel, f.session, f.token, f.base)
	output := cli(GinkgoT(), 0, append([]string{"providers", "status"}, flags...)...)
	var response struct {
		Data domain.ProviderStatus `json:"data"`
	}
	Expect(json.Unmarshal(output, &response)).To(Succeed())
	actual := response.Data
	Expect(actual.Provider).To(Equal("codex"))
	Expect(actual.State).To(Equal("provider_error"))
	Expect(actual.Reason).To(Equal("transport_unavailable"))
	Expect(actual.OrdinaryUsageAllowed).To(BeNil())
	Expect(actual.CatalogProvesAccess).To(BeFalse())
	Expect(string(output)).NotTo(ContainSubstring(f.token))
	_, e := f.auth.Revoke(context.Background(), f.token, "")
	Expect(e).NotTo(HaveOccurred())
	failure := cli(GinkgoT(), 3, append([]string{"providers", "status"}, flags...)...)
	Expect(string(failure)).NotTo(ContainSubstring(f.token))
})

// This offline driver supplies a verified local-drain attachment; no native
// client, status probe or provider request is opened by these restart controls.
type restartAuditStore struct {
	app.Store
	fault error
}
type restartAuditTx struct {
	app.WriteTx
	fault error
}

func (s restartAuditStore) Write(ctx context.Context, f func(app.WriteTx) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error { return f(restartAuditTx{tx, s.fault}) })
}
func (t restartAuditTx) RecordProviderTransport(id string, v domain.ProviderTransport) error {
	if v.FinishedAt != "" {
		return t.fault
	}
	return t.WriteTx.RecordProviderTransport(id, v)
}

type restartDriver struct {
	store   app.Store
	cleanup bool
	calls   int
}

func (d *restartDriver) Execute(ctx context.Context, a domain.ProviderAttempt, _ []domain.ProviderInput, send func() error, ack func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	v := domain.ProviderTransport{Provider: "codex", Version: "0.164.0", BinarySHA: strings.Repeat("b", 64), PreparedAt: "2026-10-30T00:00:00Z", SetupThread: "fixture-thread", AuthMode: "chatgpt", CWD: "/fixture", Approval: "never", Sandbox: "read-only", Disclosure: []domain.ProviderDisclosure{}}
	if e := d.store.Write(ctx, func(tx app.WriteTx) error { return tx.RecordProviderTransport(a.ID, v) }); e != nil {
		return work.ProviderOutcome{}, e
	}
	if e := send(); e != nil {
		return work.ProviderOutcome{}, e
	}
	d.calls++
	if e := ack(work.ProviderProgress{RequestID: "fixture-turn", SessionID: "fixture-thread"}); e != nil {
		return work.ProviderOutcome{}, e
	}
	v.FinishedAt = "2026-10-30T00:00:01Z"
	v.DriverOutcome = "failed"
	v.BackendState = "failed"
	v.BackendSource = "live_event"
	v.LocalClosed = !d.cleanup
	v.CleanupState = "drained"
	out := work.ProviderOutcome{State: "failed", Reason: "provider_failed", RequestID: "fixture-turn", SessionID: "fixture-thread", Transport: &v}
	if d.cleanup {
		v.CleanupState = "unknown"
		out.CleanupError = errRestartFixture
	}
	return out, nil
}
func (*restartDriver) Cancel(context.Context, domain.ProviderAttempt) error { return nil }
func (*restartDriver) Reconcile(context.Context, domain.ProviderAttempt, bool) (work.ProviderOutcome, error) {
	panic("no lookup expected")
}

var errRestartFixture = errors.New("controlled offline accounting failure")

func restartProviderDiagnostic(cleanup bool) error {
	ctx := context.Background()
	sel := selection(GinkgoT())
	initProfile(GinkgoT(), sel)
	writer, e := service.OpenWriter(sel)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(writer.Close()).To(Succeed()) })
	Expect(writer.PrepareStorageBootstrap(nil)).To(Succeed())
	backing := foundation.New(nil)
	_, closeStore, e := backing.Prepare(ctx, writer.Profile)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(closeStore()).To(Succeed()) })
	Expect(writer.MarkStorageReady(nil)).To(Succeed())
	auth := appauth.New(backing)
	pair, e := auth.CreatePairing(ctx, "http://127.0.0.1:9481", "offline restart owner", "fixture")
	Expect(e).NotTo(HaveOccurred())
	operation, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	_, e = auth.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: operation, Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	f := struct{ store *foundation.Store }{backing}
	job, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		target, _ := json.Marshal(map[string]string{"project_id": lib.UnfiledProjectID})
		scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, RequestSHA: strings.Repeat("a", 64)})
		return tx.CreateJob(domain.Job{ID: job, Kind: "question", Scope: scope, Permissions: map[string]int64{lib.UnfiledProjectID: 1}})
	})).To(Succeed())
	store := restartAuditStore{f.store, errRestartFixture}
	driver := &restartDriver{store: store, cleanup: cleanup}
	p := &work.ProviderCoordinator{Store: store, Driver: driver}
	Expect(p.ScheduleProvider(ctx, job)).To(Succeed())
	Eventually(func() string {
		Expect(p.PollProvider(ctx)).To(Succeed())
		j, e := f.store.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		return j.State
	}).Should(Equal("paused"))
	Eventually(func() error { Expect(p.PollProvider(ctx)).To(Succeed()); return p.ScheduleProvider(ctx, "missing-job") }).Should(MatchError(domain.ErrNotFound))
	run, cancel := context.WithCancel(ctx)
	cancel()
	diagnostic := (work.Dispatcher{Store: store, Provider: p}).Run(run)
	Expect(diagnostic).To(HaveOccurred())
	Expect(errors.Is(diagnostic, errRestartFixture)).To(BeTrue())
	Expect(driver.calls).To(Equal(1))
	return diagnostic
}

var _ = It("C03W5 controlled daemon restart accepts only collected safely drained diagnostics and retains the first until terminal stop", Label("persistence"), func() {
	diagnostic := restartProviderDiagnostic(false)
	var retained error
	again, e := daemonRestartResult(context.Background(), true, diagnostic, nil, &retained)
	Expect(again).To(BeTrue())
	Expect(e).NotTo(HaveOccurred())
	Expect(errors.Is(retained, errRestartFixture)).To(BeTrue())
	first := retained
	again, e = daemonRestartResult(context.Background(), true, diagnostic, nil, &retained)
	Expect(again).To(BeTrue())
	Expect(e).NotTo(HaveOccurred())
	Expect(retained).To(BeIdenticalTo(first))
	startup := errors.New("next startup failed")
	again, e = daemonRestartResult(context.Background(), false, startup, nil, &retained)
	Expect(again).To(BeFalse())
	Expect(errors.Is(e, startup)).To(BeTrue())
	Expect(errors.Is(e, errRestartFixture)).To(BeTrue())
	again, e = daemonRestartResult(context.Background(), false, nil, nil, &retained)
	Expect(again).To(BeFalse())
	Expect(errors.Is(e, errRestartFixture)).To(BeTrue())
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	again, e = daemonRestartResult(stopped, true, nil, nil, &retained)
	Expect(again).To(BeFalse())
	Expect(errors.Is(e, errRestartFixture)).To(BeTrue())
})

var _ = It("C03W5 unsafe cleanup and joined current endpoint storage output failures block the actual daemon restart decision", Label("persistence"), func() {
	safe := restartProviderDiagnostic(false)
	unsafe := restartProviderDiagnostic(true)
	for _, fault := range []error{unsafe, errors.New("endpoint close"), errors.New("storage close"), errors.New("current drain"), errors.New("dependent close")} {
		var retained error
		again, e := daemonRestartResult(context.Background(), true, errors.Join(safe, fault), nil, &retained)
		Expect(again).To(BeFalse())
		Expect(errors.Is(e, fault)).To(BeTrue())
		Expect(retained).To(BeNil())
	}
	output := errors.New("identity output failed")
	var retained error
	again, e := daemonRestartResult(context.Background(), true, safe, output, &retained)
	Expect(again).To(BeFalse())
	Expect(errors.Is(e, output)).To(BeTrue())
})
