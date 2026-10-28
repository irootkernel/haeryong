package main

import (
	"bytes"
	"context"
	"encoding/json"
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
