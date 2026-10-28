package auth

import (
	"bytes"
	"context"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	"haeryong.local/haeryong/internal/application/service"
	storage "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"io"
	"path/filepath"
	"strings"
	"time"
)

type httpProviderDriver struct{ calls int }

func (d *httpProviderDriver) Execute(ctx context.Context, a domain.ProviderAttempt, _ []domain.ProviderInput, send func() error, observe func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	if e := send(); e != nil {
		return work.ProviderOutcome{}, e
	}
	d.calls++
	if e := observe(work.ProviderProgress{RequestID: "synthetic-turn", SessionID: "synthetic-thread"}); e != nil {
		return work.ProviderOutcome{}, e
	}
	return work.ProviderOutcome{State: "result_saved", CompletionObserved: true, Body: io.NopCloser(strings.NewReader("synthetic protected body"))}, nil
}
func (*httpProviderDriver) Cancel(context.Context, domain.ProviderAttempt) error { return nil }
func (*httpProviderDriver) Reconcile(context.Context, domain.ProviderAttempt, bool) (work.ProviderOutcome, error) {
	return work.ProviderOutcome{State: "outcome_unknown", Reason: "transport_unknown"}, nil
}

var _ = It("PA44 actual HTTP provider resume survives requesting-client exit and session revocation with authorized event replay and protected projection", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	token, owner := f.owner(GinkgoT())
	peer := f.pairing(GinkgoT(), "observer")
	_, e := f.core.Decide(context.Background(), token, domain.PairingDecision{ID: peer.ID, Code: peer.Code, Origin: peer.Origin, ETag: peer.ETag, OperationID: newOperation(GinkgoT()), Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	poll, e := f.core.Poll(context.Background(), peer.ID, peer.PollingSecret, peer.Origin)
	Expect(e).NotTo(HaveOccurred())
	jobID, document := newOperation(GinkgoT()), newOperation(GinkgoT())
	var epoch, project string
	Expect(f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		epoch = lib.StreamEpoch
		project = lib.UnfiledProjectID
		auth, e := tx.AuthState()
		if e != nil {
			return e
		}
		if e = tx.CreateDocument(domain.Document{ID: document, ProjectID: project, Title: "private source"}); e != nil {
			return e
		}
		target, _ := json.Marshal(map[string]string{"project_id": project, "document_id": document})
		scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.PrincipalID, AuthEpoch: auth.AuthEpoch, Target: target, RequestSHA: strings.Repeat("a", 64)})
		if e = tx.CreateJob(domain.Job{ID: jobID, Kind: "question", Scope: scope, Permissions: map[string]int64{project: 1}}); e != nil {
			return e
		}
		return tx.PauseProviderJob(jobID, "provider_unavailable")
	})).To(Succeed())
	prior, e := f.core.Events(context.Background(), poll.Token, epoch, 0)
	Expect(e).NotTo(HaveOccurred())
	var cursor int64
	for _, event := range prior {
		cursor = event.Sequence
	}
	path := "/api/v1/jobs/" + jobID
	status, h, b := f.request(GinkgoT(), "GET", path, token, nil, nil)
	Expect(status).To(Equal(200))
	job := dataAs[map[string]any](GinkgoT(), b)
	headers := map[string]string{"If-Match": h.Get("ETag"), "Idempotency-Key": newOperation(GinkgoT())}
	body := map[string]any{"decision": "continue", "expected_attempt_generation": job["attempt_generation"]}
	// Exact stale generation with current validator must fail before mutation.
	stale := map[string]any{"decision": "continue", "expected_attempt_generation": 999}
	status, _, _ = f.request(GinkgoT(), "POST", path+"/resume", token, stale, headers)
	Expect(status).To(Equal(412))
	headers["Idempotency-Key"] = newOperation(GinkgoT())
	status, _, _ = f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(200))
	_, e = f.core.Revoke(context.Background(), token, owner.ID)
	Expect(e).NotTo(HaveOccurred())
	d := &httpProviderDriver{}
	p := work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d}
	Expect(p.Process(context.Background(), jobID)).To(Succeed())
	Expect(d.calls).To(Equal(1))
	current, e := f.store.Job(context.Background(), jobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(current.State).To(Equal("completed"))
	Expect(current.CancelRequested).To(BeFalse())
	events, e := f.core.Events(context.Background(), poll.Token, epoch, cursor)
	Expect(e).NotTo(HaveOccurred())
	replay, e := f.core.Events(context.Background(), poll.Token, epoch, cursor)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(events))
	Expect(events).To(ContainElement(And(HaveField("ResourceID", jobID), HaveField("Kind", "job.updated"))))
	Expect(f.store.Write(context.Background(), func(tx storage.WriteTx) error { return tx.TestTrashDocument(document) })).To(Succeed())
	hidden, e := f.core.Events(context.Background(), poll.Token, epoch, cursor)
	Expect(e).NotTo(HaveOccurred())
	for _, event := range hidden {
		Expect(event.ResourceID).NotTo(Equal(jobID))
		Expect(string(event.Body)).NotTo(ContainSubstring("synthetic protected body"))
	}
	status, _, _ = f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(401))
})

var _ = It("PA45 HTTP provider cancel preserves unknown outcome and every cancelled resume decision or nonowner admission uses the required error boundary", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	token, owner := f.owner(GinkgoT())
	jobID, attemptID := newOperation(GinkgoT()), newOperation(GinkgoT())
	restricted, e := sec.Secret()
	Expect(e).NotTo(HaveOccurred())
	Expect(f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		auth, e := tx.AuthState()
		if e != nil {
			return e
		}
		target, _ := json.Marshal(map[string]string{"project_id": lib.UnfiledProjectID})
		scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.PrincipalID, AuthEpoch: auth.AuthEpoch, Target: target, RequestSHA: strings.Repeat("a", 64)})
		if e = tx.CreateJob(domain.Job{ID: jobID, Kind: "question", Scope: scope, Permissions: map[string]int64{lib.UnfiledProjectID: 1}}); e != nil {
			return e
		}
		j, e := tx.ClaimJob(jobID)
		if e != nil {
			return e
		}
		a := domain.ProviderAttempt{ID: attemptID, JobID: jobID, Generation: j.AttemptGeneration, State: "prepared", Runtime: lib.RuntimeGeneration, RequestSHA: strings.Repeat("a", 64)}
		if e = tx.PrepareProviderAttempt(a, nil); e != nil {
			return e
		}
		if e = tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		if e = tx.PauseProviderJob(jobID, "outcome_unknown"); e != nil {
			return e
		}
		principal := newOperation(GinkgoT())
		grant := domain.CredentialScope{Kind: "workflow", Actions: []string{"draft.write"}, ProjectIDs: []string{lib.UnfiledProjectID}}
		if e = tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: principal, Active: true, Scope: grant}); e != nil {
			return e
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: newOperation(GinkgoT()), PrincipalID: principal, TokenHash: sec.Verifier("workflow", restricted), AuthEpoch: auth.AuthEpoch, Scope: grant, Permissions: map[string]int64{lib.UnfiledProjectID: 1}, ConfigRevision: 1, CreatedAt: time.Now().UTC()})
	})).To(Succeed())
	path := "/api/v1/jobs/" + jobID
	status, h, b := f.request(GinkgoT(), "GET", path, token, nil, nil)
	Expect(status).To(Equal(200))
	j := dataAs[map[string]any](GinkgoT(), b)
	body := map[string]any{"decision": "reconcile_only", "expected_attempt_generation": j["attempt_generation"]}
	headers := map[string]string{"If-Match": h.Get("ETag"), "Idempotency-Key": newOperation(GinkgoT())}
	status, _, _ = f.request(GinkgoT(), "POST", path+"/resume", restricted, body, headers)
	Expect(status).To(Equal(403))
	status, _, _ = f.request(GinkgoT(), "POST", path+"/cancel", token, map[string]any{}, map[string]string{"Idempotency-Key": newOperation(GinkgoT())})
	Expect(status).To(Equal(200))
	current, e := f.store.Job(context.Background(), jobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(current.State).To(Equal("paused"))
	Expect(current.CancelRequested).To(BeTrue())
	status, h, b = f.request(GinkgoT(), "GET", path, token, nil, nil)
	Expect(status).To(Equal(200))
	j = dataAs[map[string]any](GinkgoT(), b)
	for _, decision := range []string{"continue", "retry"} {
		body["decision"] = decision
		body["expected_attempt_generation"] = j["attempt_generation"]
		headers["If-Match"] = h.Get("ETag")
		headers["Idempotency-Key"] = newOperation(GinkgoT())
		status, _, b = f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
		Expect(status).To(Equal(409))
		Expect(string(b)).To(ContainSubstring("INVALID_TRANSITION"))
	}
	nonProvider := newOperation(GinkgoT())
	Expect(f.store.Write(context.Background(), func(tx storage.WriteTx) error { return tx.CreateJob(domain.Job{ID: nonProvider, Kind: "import"}) })).To(Succeed())
	otherPath := "/api/v1/jobs/" + nonProvider
	status, otherHeaders, otherBody := f.request(GinkgoT(), "GET", otherPath, token, nil, nil)
	Expect(status).To(Equal(200))
	other := dataAs[map[string]any](GinkgoT(), otherBody)
	status, _, otherBody = f.request(GinkgoT(), "POST", otherPath+"/resume", token, map[string]any{"decision": "continue", "expected_attempt_generation": other["attempt_generation"]}, map[string]string{"If-Match": otherHeaders.Get("ETag"), "Idempotency-Key": newOperation(GinkgoT())})
	Expect(status).To(Equal(409))
	Expect(string(otherBody)).To(ContainSubstring("INVALID_TRANSITION"))

})

var _ = It("PA11 owner resume uses exact generation and validator, preserves receipt replay and denies unauthenticated access", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	token, _ := f.owner(GinkgoT())
	jobID := newOperation(GinkgoT())
	Expect(f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		if e := tx.CreateJob(domain.Job{ID: jobID, Kind: "question"}); e != nil {
			return e
		}
		return tx.PauseProviderJob(jobID, "provider_unavailable")
	})).To(Succeed())
	path := "/api/v1/jobs/" + jobID
	status, h, b := f.request(GinkgoT(), "GET", path, token, nil, nil)
	Expect(status).To(Equal(200))
	j := dataAs[map[string]any](GinkgoT(), b)
	body := map[string]any{"decision": "continue", "expected_attempt_generation": j["attempt_generation"]}
	key := newOperation(GinkgoT())
	headers := map[string]string{"Idempotency-Key": key}
	status, _, _ = f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(428))
	headers["If-Match"] = `"stale"`
	status, _, _ = f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(412))
	headers["If-Match"] = h.Get("ETag")
	status, _, b = f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(200))
	first := dataAs[map[string]any](GinkgoT(), b)
	Expect(first["state"]).To(Equal("queued"))
	status, _, b = f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(200))
	Expect(dataAs[map[string]any](GinkgoT(), b)).To(Equal(first))
	status, _, _ = f.request(GinkgoT(), "POST", path+"/resume", "", body, headers)
	Expect(status).To(Equal(401))
	body["decision"] = "retry"
	status, _, _ = f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(409))
	status, _, _ = f.request(GinkgoT(), "POST", path+"/resume", token, map[string]any{"decision": "continue", "expected_attempt_generation": 1, "extra": true}, headers)
	Expect(status).To(Equal(400))
})

var _ = It("PA16 hidden provider targets retain minimal owner status and reconcile-only receipts without payload access", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	token, owner := f.owner(GinkgoT())
	jobID, document := newOperation(GinkgoT()), newOperation(GinkgoT())
	Expect(f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		if e = tx.CreateDocument(domain.Document{ID: document, ProjectID: lib.UnfiledProjectID, Title: "hidden-private-title"}); e != nil {
			return e
		}
		target, _ := json.Marshal(map[string]string{"document_id": document, "project_id": lib.UnfiledProjectID})
		scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.PrincipalID, Target: target})
		if e = tx.CreateJob(domain.Job{ID: jobID, Kind: "question", Scope: scope}); e != nil {
			return e
		}
		if e = tx.SetJobResult(jobID, []byte(`{"private_text":"forbidden-result-body"}`)); e != nil {
			return e
		}
		if e = tx.PauseProviderJob(jobID, "outcome_unknown"); e != nil {
			return e
		}
		return tx.TestTrashDocument(document)
	})).To(Succeed())
	path := "/api/v1/jobs/" + jobID
	status, h, b := f.request(GinkgoT(), "GET", path, token, nil, nil)
	Expect(status).To(Equal(200))
	Expect(string(b)).NotTo(ContainSubstring(document))
	Expect(string(b)).NotTo(ContainSubstring("forbidden-result-body"))
	j := dataAs[map[string]any](GinkgoT(), b)
	body := map[string]any{"decision": "reconcile_only", "expected_attempt_generation": j["attempt_generation"]}
	key := newOperation(GinkgoT())
	headers := map[string]string{"Idempotency-Key": key, "If-Match": h.Get("ETag")}
	status, _, b = f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(200))
	Expect(string(b)).NotTo(ContainSubstring(document))
	Expect(string(b)).NotTo(ContainSubstring("forbidden-result-body"))
	status, _, b = f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(200))
	Expect(string(b)).NotTo(ContainSubstring("forbidden-result-body"))
	status, _, b = f.request(GinkgoT(), "GET", "/api/v1/operations/"+key, token, nil, nil)
	Expect(status).To(Equal(200))
	Expect(string(b)).NotTo(ContainSubstring(document))
	Expect(string(b)).NotTo(ContainSubstring("forbidden-result-body"))
	body["decision"] = "continue"
	headers["Idempotency-Key"] = newOperation(GinkgoT())
	status, _, _ = f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(404))
})

var _ = It("PA29 terminal provider status accounts older uncertainty and keeps validators consistent for explicit reconciliation and replay", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	token, _ := f.owner(GinkgoT())
	jobID, oldID, newID := newOperation(GinkgoT()), newOperation(GinkgoT()), newOperation(GinkgoT())
	Expect(f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		config, e := tx.Configuration()
		if e != nil {
			return e
		}
		target, _ := json.Marshal(map[string]string{"project_id": lib.UnfiledProjectID})
		scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, RequestSHA: strings.Repeat("a", 64)})
		if e = tx.CreateJob(domain.Job{ID: jobID, Kind: "question", Scope: scope, ConfigRevision: config.Revision, Permissions: map[string]int64{lib.UnfiledProjectID: 1}}); e != nil {
			return e
		}
		j, e := tx.ClaimJob(jobID)
		if e != nil {
			return e
		}
		a := domain.ProviderAttempt{ID: oldID, JobID: jobID, Generation: j.AttemptGeneration, State: "prepared", Runtime: lib.RuntimeGeneration, RequestSHA: strings.Repeat("a", 64)}
		if e = tx.PrepareProviderAttempt(a, nil); e != nil {
			return e
		}
		if e = tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		if e = tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "private-old-request", "private-thread", "transport_unknown"); e != nil {
			return e
		}
		if e = tx.PauseProviderJob(jobID, "outcome_unknown"); e != nil {
			return e
		}
		if _, e = tx.ResumeProviderJob(jobID, j.AttemptGeneration, "retry"); e != nil {
			return e
		}
		j, e = tx.ClaimJob(jobID)
		if e != nil {
			return e
		}
		a.ID = newID
		a.Generation = j.AttemptGeneration
		if e = tx.PrepareProviderAttempt(a, nil); e != nil {
			return e
		}
		if e = tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		o := domain.Object{Hash: strings.Repeat("c", 64), Size: 6, State: "ready"}
		if e = tx.PutDurableObject(o); e != nil {
			return e
		}
		if e = tx.SaveProviderResult(a.ID, o); e != nil {
			return e
		}
		return tx.ApplyProviderResult(a.ID, nil)
	})).To(Succeed())
	path := "/api/v1/jobs/" + jobID
	status, h, raw := f.request(GinkgoT(), "GET", path, token, nil, nil)
	Expect(status).To(Equal(200))
	data := dataAs[map[string]any](GinkgoT(), raw)
	summary := data["provider_attempts"].(map[string]any)
	Expect(data["state"]).To(Equal("completed"))
	Expect(summary["count"]).To(BeNumerically("==", 2))
	Expect(summary["unknown"]).To(BeNumerically("==", 1))
	Expect(summary["unknown_outcome_reason"]).To(Equal("outcome_unknown"))
	Expect(data["etag"]).To(Equal(h.Get("ETag")))
	Expect(string(raw)).NotTo(ContainSubstring("private-old-request"))
	Expect(string(raw)).NotTo(ContainSubstring("private-thread"))
	currentResult, _ := json.Marshal(data["result"])
	key := newOperation(GinkgoT())
	headers := map[string]string{"Idempotency-Key": key, "If-Match": h.Get("ETag")}
	body := map[string]any{"decision": "reconcile_only", "expected_attempt_generation": data["attempt_generation"]}
	status, acceptedHeaders, first := f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(200))
	accepted := dataAs[map[string]any](GinkgoT(), first)
	Expect(accepted["state"]).To(Equal("completed"))
	result, _ := json.Marshal(accepted["result"])
	Expect(result).To(Equal(currentResult))
	Expect(accepted["etag"]).To(Equal(acceptedHeaders.Get("ETag")))
	status, againHeaders, again := f.request(GinkgoT(), "GET", path, token, nil, nil)
	Expect(status).To(Equal(200))
	Expect(dataAs[map[string]any](GinkgoT(), again)).To(Equal(accepted))
	Expect(againHeaders.Get("ETag")).To(Equal(acceptedHeaders.Get("ETag")))
	status, replayedHeaders, replayed := f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(200))
	Expect(dataAs[map[string]any](GinkgoT(), replayed)).To(Equal(accepted))
	Expect(replayedHeaders.Get("ETag")).To(Equal(acceptedHeaders.Get("ETag")))
})

var _ = It("PA64 HTTP stale saved continuation is a permanent invalid transition after precondition and authentication checks", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	token, _ := f.owner(GinkgoT())
	jobID, attempt := newOperation(GinkgoT()), newOperation(GinkgoT())
	Expect(f.store.Write(context.Background(), func(tx storage.WriteTx) error {
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
		return tx.PauseProviderJob(jobID, "replacement")
	})).To(Succeed())
	path := "/api/v1/jobs/" + jobID
	status, h, b := f.request(GinkgoT(), "GET", path, token, nil, nil)
	Expect(status).To(Equal(200))
	job := dataAs[map[string]any](GinkgoT(), b)
	headers := map[string]string{"If-Match": h.Get("ETag"), "Idempotency-Key": newOperation(GinkgoT())}
	body := map[string]any{"decision": "continue", "expected_attempt_generation": job["attempt_generation"]}
	status, responseHeaders, b := f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(409))
	Expect(responseHeaders.Get("Retry-After")).To(BeEmpty())
	Expect(string(b)).To(ContainSubstring("INVALID_TRANSITION"))
	Expect(string(b)).To(ContainSubstring(`"retryable":false`))
	headers["Idempotency-Key"] = newOperation(GinkgoT())
	headers["If-Match"] = `"old"`
	status, _, _ = f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(412))
	headers["If-Match"] = h.Get("ETag")
	body["expected_attempt_generation"] = 999
	status, _, _ = f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(412))
	status, _, _ = f.request(GinkgoT(), "POST", path+"/resume", "", body, headers)
	Expect(status).To(Equal(401))
})

var _ = It("PA80 actual HTTP cancel after Store reopen commits normalization and cancellation once with receipt replay", Label("persistence"), func() {
	for _, dispatched := range []bool{false, true} {
		f := newFixture(GinkgoT())
		token, _ := f.owner(GinkgoT())
		job, attempt := newOperation(GinkgoT()), newOperation(GinkgoT())
		Expect(f.store.Write(context.Background(), func(tx storage.WriteTx) error {
			lib, e := tx.Library()
			if e != nil {
				return e
			}
			owner, e := tx.AuthState()
			if e != nil {
				return e
			}
			target, _ := json.Marshal(map[string]string{"project_id": lib.UnfiledProjectID})
			scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, RequestSHA: strings.Repeat("a", 64)})
			if e = tx.CreateJob(domain.Job{ID: job, Kind: "question", Scope: scope}); e != nil {
				return e
			}
			j, e := tx.ClaimJob(job)
			if e != nil {
				return e
			}
			a := domain.ProviderAttempt{ID: attempt, JobID: job, Generation: j.AttemptGeneration, State: "prepared", Runtime: lib.RuntimeGeneration, RequestSHA: strings.Repeat("a", 64)}
			if e = tx.PrepareProviderAttempt(a, nil); e != nil {
				return e
			}
			if dispatched {
				return tx.DispatchProviderAttempt(attempt)
			}
			return nil
		})).To(Succeed())
		profile, e := service.Load(service.Selection{Profile: "http", DataDir: filepath.Dir(filepath.Dir(f.databasePath))})
		Expect(e).NotTo(HaveOccurred())
		Expect(f.store.Close()).To(Succeed())
		_, closeDB, e := f.store.Prepare(context.Background(), profile)
		Expect(e).NotTo(HaveOccurred())
		defer closeDB()
		before, e := f.store.Job(context.Background(), job)
		Expect(e).NotTo(HaveOccurred())
		Expect(before.State).To(Equal("interrupted"))
		operation := newOperation(GinkgoT())
		headers := map[string]string{"Idempotency-Key": operation}
		path := "/api/v1/jobs/" + job
		status, _, body := f.request(GinkgoT(), "POST", path+"/cancel", token, map[string]any{}, headers)
		Expect(status).To(Equal(200))
		first := dataAs[map[string]any](GinkgoT(), body)
		status, _, body = f.request(GinkgoT(), "POST", path+"/cancel", token, map[string]any{}, headers)
		Expect(status).To(Equal(200))
		Expect(dataAs[map[string]any](GinkgoT(), body)).To(Equal(first))
		current, e := f.store.Job(context.Background(), job)
		Expect(e).NotTo(HaveOccurred())
		Expect(current.AttemptGeneration).To(Equal(before.AttemptGeneration + 1))
		Expect(current.CancelRequested).To(BeTrue())
		if dispatched {
			Expect(current.State).To(Equal("paused"))
			Expect(current.Error).To(Equal("outcome_unknown"))
		} else {
			Expect(current.State).To(Equal("cancelled"))
		}
		health, e := f.store.Health(context.Background())
		Expect(e).NotTo(HaveOccurred())
		events, e := f.store.Events(context.Background(), health.Library.StreamEpoch, 0, 1000)
		Expect(e).NotTo(HaveOccurred())
		updates := []domain.Event{}
		for _, event := range events {
			if event.ResourceID == job && event.Revision > before.Revision {
				updates = append(updates, event)
			}
		}
		wantEvents := 2
		if !dispatched {
			wantEvents = 3
		}
		Expect(updates).To(HaveLen(wantEvents))
		Expect(updates[0].Revision).To(Equal(before.Revision + 1))
		Expect(updates[len(updates)-1].Revision).To(Equal(current.Revision))
		Expect(updates[0].Sequence).To(BeNumerically("<", updates[1].Sequence))
		for _, event := range updates {
			Expect(event.Body).To(BeEmpty())
		}
		status, _, _ = f.request(GinkgoT(), "POST", path+"/cancel", "", map[string]any{}, headers)
		Expect(status).To(Equal(401))
	}
})

var _ = It("PA81 HTTP orphan-only deduplicated saved results return permanent409 without retry advice and preserve refusal precedence", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	token, _ := f.owner(GinkgoT())
	job, attempt := newOperation(GinkgoT()), newOperation(GinkgoT())
	var fact []byte
	Expect(f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		target, _ := json.Marshal(map[string]string{"project_id": lib.UnfiledProjectID})
		sha := strings.Repeat("e", 64)
		scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, RequestSHA: sha})
		if e = tx.CreateJob(domain.Job{ID: job, Kind: "question", Scope: scope}); e != nil {
			return e
		}
		o := domain.Object{Hash: strings.Repeat("c", 64), Size: 1, State: "ready"}
		if e = tx.PutDurableObject(o); e != nil {
			return e
		}
		if e = tx.AcquirePin(domain.Pin{ID: "http-dedup", Kind: "selected_snapshot", OwnerID: "independent", Hash: o.Hash}); e != nil {
			return e
		}
		fact, _ = json.Marshal(map[string]any{"attempt_id": attempt, "job_id": job, "principal_id": owner.OwnerID, "target": json.RawMessage(target), "request_sha256": sha, "state": "result_saved", "result_hash": o.Hash})
		return tx.PauseProviderJob(job, "restored_saved_result")
	})).To(Succeed())
	Expect(sqlite.RetainProviderOrphans(context.Background(), f.databasePath, bytes.NewReader(fact), func(domain.Object) (bool, error) { return true, nil })).To(Succeed())
	path := "/api/v1/jobs/" + job
	status, h, b := f.request(GinkgoT(), "GET", path, token, nil, nil)
	Expect(status).To(Equal(200))
	before := dataAs[map[string]any](GinkgoT(), b)
	body := map[string]any{"decision": "continue", "expected_attempt_generation": before["attempt_generation"]}
	headers := map[string]string{"If-Match": h.Get("ETag"), "Idempotency-Key": newOperation(GinkgoT())}
	status, _, _ = f.request(GinkgoT(), "POST", path+"/resume", "", body, headers)
	Expect(status).To(Equal(401))
	headers["If-Match"] = `"stale"`
	status, _, _ = f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(412))
	headers["If-Match"] = h.Get("ETag")
	status, h, b = f.request(GinkgoT(), "POST", path+"/resume", token, body, headers)
	Expect(status).To(Equal(409))
	Expect(string(b)).To(ContainSubstring("INVALID_TRANSITION"))
	Expect(string(b)).To(ContainSubstring(`"retryable":false`))
	Expect(h.Get("Retry-After")).To(BeEmpty())
	after, e := f.store.Job(context.Background(), job)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.State).To(Equal("paused"))
	Expect(after.Result).To(BeEmpty())
})
