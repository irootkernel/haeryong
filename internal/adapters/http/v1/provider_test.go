package v1

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
	"strings"
	"time"
)

var _ = It("C03HTTP owner auth/quota status leaves stored files and explicit personal Review usable", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	upload := f.create()
	body := []byte("<h1>Readable while AI is unavailable</h1>")
	f.put(upload, "hello.html", "file", body)
	version := f.publish(upload, nil)
	for i, state := range []string{"auth_required", "quota_exhausted"} {
		f.query.ProviderStatus = func(context.Context) domain.ProviderStatus {
			return domain.ProviderStatus{Provider: "codex", State: state, Authentication: "unknown", Models: []string{}}
		}
		status, _, data := f.request("GET", "/api/v1/providers", nil, nil)
		want(GinkgoT(), status, 200, data)
		actual := decoded[domain.ProviderStatus](GinkgoT(), data)
		Expect(actual.State).To(Equal(state))
		Expect(actual.OrdinaryUsageAllowed).To(BeNil())
		status, _, data = f.request("HEAD", "/api/v1/providers", nil, nil)
		want(GinkgoT(), status, 200, data)
		Expect(data).To(BeEmpty())
		status, _, data = f.request("GET", "/api/v1/versions/"+version.ID+"/file?path=hello.html", nil, nil)
		want(GinkgoT(), status, 200, data)
		Expect(data).To(Equal(body))
		target := "/api/v1/versions/" + version.ID + "/review"
		status, headers, data := f.request("GET", target, nil, nil)
		want(GinkgoT(), status, 200, data)
		condition := map[string]string{"Content-Type": "application/json", "Idempotency-Key": id(GinkgoT())}
		action, state := "complete", "reviewed"
		if i == 0 {
			condition["If-None-Match"] = "*"
		} else {
			condition["If-Match"] = headers.Get("ETag")
			action, state = "reopen", "reviewing"
		}
		input, _ := json.Marshal(map[string]string{"action": action, "state": state})
		status, _, data = f.request("PUT", target, bytes.NewReader(input), condition)
		Expect(status == 200 || status == 201).To(BeTrue())
	}
})

var _ = It("C03HTTP rejects unauthorized/unsupported provider probes and revocation during the native-independent probe", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	calls := 0
	f.query.ProviderStatus = func(context.Context) domain.ProviderStatus {
		calls++
		return domain.ProviderStatus{State: "ready", Provider: "codex"}
	}
	for _, auth := range []string{"", "Bearer invalid"} {
		status, _, _ := f.request("GET", "/api/v1/providers", nil, map[string]string{"Authorization": auth})
		Expect(status).To(Equal(401))
		Expect(calls).To(BeZero())
	}
	workflow, _ := f.workflow(domain.CredentialScope{Kind: "workflow", Actions: []string{"project.read"}, ProjectIDs: []string{f.project}})
	status, _, _ := f.request("GET", "/api/v1/providers", nil, map[string]string{"Authorization": "Bearer " + workflow})
	Expect(status).To(Equal(403))
	Expect(calls).To(BeZero())
	status, _, _ = f.request("POST", "/api/v1/providers", nil, nil)
	Expect(status).To(Equal(404))
	Expect(calls).To(BeZero())
	for _, method := range []string{"GET", "HEAD"} {
		for _, query := range []string{"?cursor=x", "?limit=1", "?unknown=1"} {
			status, _, body := f.request(method, "/api/v1/providers"+query, nil, nil)
			Expect(status).To(Equal(400))
			if method == "HEAD" {
				Expect(body).To(BeEmpty())
			}
			Expect(calls).To(BeZero())
		}
	}
	f.query.ProviderStatus = func(context.Context) domain.ProviderStatus {
		calls++
		_, e := f.auth.Revoke(context.Background(), f.token, "")
		Expect(e).NotTo(HaveOccurred())
		return domain.ProviderStatus{State: "ready", Provider: "codex"}
	}
	status, _, _ = f.request("GET", "/api/v1/providers", nil, nil)
	Expect(status).To(Equal(401))
	Expect(calls).To(Equal(1))
})

type providerAvailabilityStore struct {
	app.Store
	failAudit bool
}
type providerAvailabilityTx struct {
	app.WriteTx
	failAudit bool
}

func (s providerAvailabilityStore) Write(ctx context.Context, f func(app.WriteTx) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error { return f(providerAvailabilityTx{tx, s.failAudit}) })
}
func (t providerAvailabilityTx) RecordProviderTransport(id string, v domain.ProviderTransport) error {
	if t.failAudit && v.DriverOutcome != "" {
		return errors.New("controlled final audit failure")
	}
	return t.WriteTx.RecordProviderTransport(id, v)
}

type providerAvailabilityDriver struct {
	store          app.Store
	cleanupUnknown bool
}

func (d providerAvailabilityDriver) Execute(ctx context.Context, a domain.ProviderAttempt, _ []domain.ProviderInput, send func() error, ack func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	audit := domain.ProviderTransport{Provider: "codex", Version: "0.164.0", BinarySHA: strings.Repeat("b", 64), PreparedAt: "2026-10-29T12:00:00Z", SetupThread: "fixture-thread", CWD: "/fixture", Approval: "never", Sandbox: "read-only", AuthMode: "chatgpt"}
	if e := d.store.Write(ctx, func(tx app.WriteTx) error { return tx.RecordProviderTransport(a.ID, audit) }); e != nil {
		return work.ProviderOutcome{}, e
	}
	if e := send(); e != nil {
		return work.ProviderOutcome{}, e
	}
	if e := ack(work.ProviderProgress{RequestID: "fixture-turn", SessionID: "fixture-thread"}); e != nil {
		return work.ProviderOutcome{}, e
	}
	audit.DriverOutcome = "result_ready"
	audit.BackendState = "completed"
	audit.BackendSource = "live_event"
	audit.LocalClosed = !d.cleanupUnknown
	audit.CleanupState = "drained"
	audit.FinishedAt = "2026-10-29T12:00:02Z"
	out := work.ProviderOutcome{State: "result_saved", CompletionObserved: true, RequestID: "fixture-turn", SessionID: "fixture-thread", Body: io.NopCloser(strings.NewReader("complete availability output")), Transport: &audit}
	if d.cleanupUnknown {
		audit.CleanupState = "unknown"
		audit.CleanupReason = "controlled_cleanup_unknown"
		return out, errors.New("controlled cleanup failure")
	}
	return out, nil
}
func (providerAvailabilityDriver) Cancel(context.Context, domain.ProviderAttempt) error { return nil }
func (providerAvailabilityDriver) Reconcile(context.Context, domain.ProviderAttempt, bool) (work.ProviderOutcome, error) {
	return work.ProviderOutcome{}, errors.New("unexpected lookup")
}

var _ = It("C03HTTP completed output remains durable and Dispatcher/Reader/Review stay available after audit or cleanup errors", Label("persistence"), func() {
	for _, failAudit := range []bool{true, false} {
		f := newFixture(GinkgoT())
		upload := f.create()
		body := []byte("<h1>Available after provider accounting failure</h1>")
		f.put(upload, "hello.html", "file", body)
		version := f.publish(upload, nil)
		f.stop()
		Expect(<-f.worker).To(Succeed())
		f.stop = nil
		jobID := id(GinkgoT())
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			library, e := tx.Library()
			if e != nil {
				return e
			}
			owner, e := tx.AuthState()
			if e != nil {
				return e
			}
			target, _ := json.Marshal(map[string]string{"project_id": library.UnfiledProjectID})
			scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, RequestSHA: strings.Repeat("a", 64)})
			return tx.CreateJob(domain.Job{ID: jobID, Kind: "question", Scope: scope})
		})).To(Succeed())
		faults := providerAvailabilityStore{f.store, failAudit}
		provider := &work.ProviderCoordinator{Store: faults, Bytes: f.store, Driver: providerAvailabilityDriver{store: f.store, cleanupUnknown: !failAudit}}
		run, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { defer GinkgoRecover(); done <- (work.Dispatcher{Store: f.store, Provider: provider}).Run(run) }()
		Eventually(func() string { j, _ := f.store.Job(context.Background(), jobID); return j.State }, 3*time.Second).Should(Equal(map[bool]string{true: "paused", false: "completed"}[failAudit]))
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			attempts, e := tx.ProviderAttempts(jobID)
			if e == nil {
				Expect(attempts).To(HaveLen(1))
				Expect(attempts[0].State).To(Equal("result_saved"))
				Expect(attempts[0].ResultHash).To(HaveLen(64))
			}
			return e
		})).To(Succeed())
		Consistently(done, 100*time.Millisecond).ShouldNot(Receive())
		status, _, data := f.request("GET", "/api/v1/versions/"+version.ID+"/file?path=hello.html", nil, nil)
		want(GinkgoT(), status, 200, data)
		Expect(data).To(Equal(body))
		review := "/api/v1/versions/" + version.ID + "/review"
		status, _, data = f.request("GET", review, nil, nil)
		want(GinkgoT(), status, 200, data)
		change, _ := json.Marshal(map[string]string{"action": "complete", "state": "reviewed"})
		status, _, data = f.request("PUT", review, bytes.NewReader(change), map[string]string{"Content-Type": "application/json", "Idempotency-Key": id(GinkgoT()), "If-None-Match": "*"})
		Expect(status == 200 || status == 201).To(BeTrue())
		cancel()
		Eventually(done, 3*time.Second).Should(Receive(MatchError(ContainSubstring("provider accounting failed"))))
	}
})
