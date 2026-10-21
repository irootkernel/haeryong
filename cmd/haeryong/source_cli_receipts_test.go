package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	client "haeryong.local/haeryong/internal/adapters/client"
	importer "haeryong.local/haeryong/internal/application/import"
	appsource "haeryong.local/haeryong/internal/application/source"
	"haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// G1(a): a definitive failure releases the automatic pending key after it is
// emitted, so the same command run again is a new operation.
var _ = It("TestActualCLIVerifyDefinitiveFailureReleasesPendingKey", Label("persistence"), func() {
	t := GinkgoT()
	e := newExpectationFixture(t)
	root, _, flags := e.callerRoot(t, e.f.base)
	// An unknown version fails definitively (404, exit 3) on one route.
	failing := append([]string{"evidence", "verify", "--version", "00000000-0000-4000-8000-0000000000ff", "--evidence", "ev"}, flags...)
	first := failedOperation(t, cli(t, 3, failing...))
	Expect(pendingOperations(t, root)).To(BeEmpty(), "the definitive failure released its pending key")
	second := failedOperation(t, cli(t, 3, failing...))
	Expect(second).NotTo(Equal(first), "the same command is a new operation, not a replay of the old failure")
	Expect(pendingOperations(t, root)).To(BeEmpty())
})

// E1/G1(a): a lost response is not a definitive failure: the automatic
// pending key survives and the rerun without --operation-id completes the
// same operation (the server already applied it once).
var _ = It("TestActualCLIVerifyLostResponseKeepsPendingKey", Label("persistence"), func() {
	t := GinkgoT()
	e := newExpectationFixture(t)
	proxy := &faultProxy{inner: e.f.handler, host: hostOf(t, e.f.base), method: http.MethodPost, path: "/source-evidence-verifications", drop: true}
	front := httptest.NewServer(proxy)
	DeferCleanup(front.Close)
	root, _, proxyFlags := e.callerRoot(t, front.URL)
	cli(t, 5, append([]string{"evidence", "verify", "--version", e.version, "--evidence", "ev"}, proxyFlags...)...)
	pending := pendingOperations(t, root)
	Expect(pending).To(HaveLen(1), "the uncertain outcome keeps the pending key")
	_, verifications := e.counts(t)
	Expect(verifications).To(Equal(1), "the server applied the request before its response was lost")
	_, _, flags := e.callerRoot(t, e.f.base)
	out := cli(t, 0, append([]string{"evidence", "verify", "--version", e.version, "--evidence", "ev"}, flags...)...)
	var verified struct {
		Data struct {
			OperationID string `json:"operation_id"`
			Result      struct {
				Outcome string `json:"outcome"`
			} `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &verified)).NotTo(HaveOccurred(), string(out))
	Expect(verified.Data.OperationID).To(Equal(pending[0]), "the rerun resumed the same operation")
	Expect(verified.Data.Result.Outcome).To(Equal("captured"))
	_, verifications = e.counts(t)
	Expect(verifications).To(Equal(1), "no second verification")
	Expect(pendingOperations(t, root)).To(BeEmpty(), "released after the result was emitted")
})

// E1/G1(a): a 5xx answer may leave an accepted receipt behind, so it is not
// definitive: the automatic key survives and the rerun completes the same
// operation.
var _ = It("TestActualCLIVerifyServerErrorKeepsPendingKey", Label("persistence"), func() {
	t := GinkgoT()
	e := newExpectationFixture(t)
	proxy := &faultProxy{inner: e.f.handler, host: hostOf(t, e.f.base), method: http.MethodPost, path: "/source-evidence-verifications", status: http.StatusServiceUnavailable}
	front := httptest.NewServer(proxy)
	DeferCleanup(front.Close)
	root, _, proxyFlags := e.callerRoot(t, front.URL)
	cli(t, 5, append([]string{"evidence", "verify", "--version", e.version, "--evidence", "ev"}, proxyFlags...)...)
	pending := pendingOperations(t, root)
	Expect(pending).To(HaveLen(1), "a 5xx answer keeps the pending key")
	_, _, flags := e.callerRoot(t, e.f.base)
	out := cli(t, 0, append([]string{"evidence", "verify", "--version", e.version, "--evidence", "ev"}, flags...)...)
	var verified struct {
		Data struct {
			OperationID string `json:"operation_id"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &verified)).NotTo(HaveOccurred(), string(out))
	Expect(verified.Data.OperationID).To(Equal(pending[0]), "the rerun resumed the same operation")
	_, verifications := e.counts(t)
	Expect(verifications).To(Equal(1))
	Expect(pendingOperations(t, root)).To(BeEmpty())
})

// G1(b): a completed staging is reused only for the same large expectation.
var _ = It("TestActualCLIChangedLargeExpectationFileRejected", Label("persistence"), func() {
	t := GinkgoT()
	e := newExpectationFixture(t)
	_, _, flags := e.callerRoot(t, e.f.base)
	verify := append([]string{"evidence", "verify", "--version", e.version, "--evidence", "ev", "--expected-excerpt-file", e.filePath, "--operation-id", e.key}, flags...)
	cli(t, 0, verify...)
	// Different bytes above the inline bound take the staged path again.
	changed := "func main() {}\n" + strings.Repeat("// CHANGED padding line for the fingerprint test\n", 1600)
	Expect(len(changed)).To(BeNumerically(">", appsource.MaxInlineExcerpt))
	Expect(os.WriteFile(e.filePath, []byte(changed), 0o600)).To(Succeed())
	cli(t, 4, verify...)
	uploads, verifications := e.counts(t)
	Expect(uploads).To(Equal(1), "no second upload for the changed input")
	Expect(verifications).To(Equal(1), "no second verification for the changed input")
})

// G1(b): an interrupted staging resumes only for the same large expectation.
var _ = It("TestActualCLIChangedExpectationAfterInterruptedStagingRejected", Label("persistence"), func() {
	t := GinkgoT()
	e := newExpectationFixture(t)
	proxy := &interruptingProxy{inner: e.f.handler, host: hostOf(t, e.f.base), killPath: "/uploads/", method: http.MethodPut, remaining: 3}
	front := httptest.NewServer(proxy)
	DeferCleanup(front.Close)
	e.runAgainst(t, proxy, front.URL, 5)
	uploads, verifications := e.counts(t)
	Expect(uploads).To(Equal(1), "the upload exists with no accepted bytes")
	Expect(verifications).To(Equal(0))
	changed := "func main() {}\n" + strings.Repeat("// CHANGED padding line for the fingerprint test\n", 1600)
	Expect(len(changed)).To(BeNumerically(">", appsource.MaxInlineExcerpt))
	Expect(os.WriteFile(e.filePath, []byte(changed), 0o600)).To(Succeed())
	_, _, flags := e.callerRoot(t, e.f.base)
	cli(t, 4, append([]string{"evidence", "verify", "--version", e.version, "--evidence", "ev", "--expected-excerpt-file", e.filePath, "--operation-id", e.key}, flags...)...)
	uploads, verifications = e.counts(t)
	Expect(uploads).To(Equal(1), "the interrupted staging was not continued with other bytes")
	Expect(verifications).To(Equal(0))
})

// G7: the server applies the commit but its response is lost. A rerun with a
// changed flag is a different request; the unchanged rerun resumes the
// journaled plan and commit on the same caller state without another plan.
var _ = It("TestActualCLISourcesApproveLostCommitResponseResumes", Label("persistence"), func() {
	t := GinkgoT()
	f := newSourceCommandFixture(t)
	root := filepath.Join(t.TempDir(), "caller")
	Expect(os.MkdirAll(root, 0o700)).To(Succeed())
	flags := f.sharedCaller(t, root, f.base)
	project := registerProject(t, flags, "Lost Commit Project")
	proxy := &faultProxy{inner: f.handler, host: hostOf(t, f.base), method: http.MethodPost, path: "/commit", drop: true}
	front := httptest.NewServer(proxy)
	DeferCleanup(front.Close)
	sourceRoot := t.TempDir()
	approve := []string{"sources", "approve", "--project", project, "--source-id", "9b000000-0000-4000-8000-000000000001", "--kind", "files", "--root-path", sourceRoot, "--confirmed"}
	before := configRevision(t, f)
	cli(t, 5, append(approve, f.sharedCaller(t, root, front.URL)...)...)
	Expect(configRevision(t, f)).To(Equal(before+1), "the commit was applied; only its response was lost")
	planID := proxy.planID(t)
	Expect(pendingOperations(t, root)).To(HaveLen(1), "the uncertain commit keeps the preparation key")

	changed := []string{"sources", "approve", "--project", project, "--source-id", "9b000000-0000-4000-8000-000000000001", "--kind", "files", "--root-path", t.TempDir(), "--confirmed"}
	cli(t, 4, append(changed, flags...)...)
	Expect(configRevision(t, f)).To(Equal(before + 1))

	out := cli(t, 0, append(approve, flags...)...)
	var committed struct {
		Data struct {
			PlanID      string `json:"plan_id"`
			OperationID string `json:"operation_id"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &committed)).NotTo(HaveOccurred(), string(out))
	Expect(committed.Data.PlanID).To(Equal(planID), "the rerun reused the journaled plan")
	Expect(committed.Data.OperationID).To(Equal(planID), "the commit uses the plan's natural identity")
	Expect(configRevision(t, f)).To(Equal(before+1), "exactly one committed change")
	sources := projectSources(t, f, project)
	Expect(sources).To(HaveLen(1))
	Expect(sources[0]["root_path"]).To(Equal(sourceRoot))
	Expect(pendingOperations(t, root)).To(BeEmpty(), "released after the result was emitted")
})

// G7: a definitive commit failure releases the preparation key after it is
// emitted, so the next run builds a fresh plan.
var _ = It("TestActualCLISourcesApproveDefinitiveCommitFailureReleasesPlan", Label("persistence"), func() {
	t := GinkgoT()
	f := newSourceCommandFixture(t)
	root := filepath.Join(t.TempDir(), "caller")
	Expect(os.MkdirAll(root, 0o700)).To(Succeed())
	flags := f.sharedCaller(t, root, f.base)
	project := registerProject(t, flags, "Stale Plan Project")
	// Another configuration change lands between the plan and its commit, so
	// the commit fails definitively as a stale plan.
	proxy := &faultProxy{inner: f.handler, host: hostOf(t, f.base), method: http.MethodPost, path: "/commit", before: func() { bumpConfiguration(t, f) }}
	front := httptest.NewServer(proxy)
	DeferCleanup(front.Close)
	approve := []string{"sources", "approve", "--project", project, "--source-id", "9b000000-0000-4000-8000-000000000002", "--kind", "files", "--root-path", t.TempDir(), "--confirmed"}
	cli(t, 4, append(approve, f.sharedCaller(t, root, front.URL)...)...)
	stale := proxy.planID(t)
	Expect(pendingOperations(t, root)).To(BeEmpty(), "the definitive failure released the preparation key")
	out := cli(t, 0, append(approve, flags...)...)
	var committed struct {
		Data struct {
			PlanID string `json:"plan_id"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &committed)).NotTo(HaveOccurred(), string(out))
	Expect(committed.Data.PlanID).NotTo(Equal(stale), "a fresh plan against the current configuration")
	Expect(projectSources(t, f, project)).To(HaveLen(1))
})

// G7: an explicit --operation-id fixes the whole approval: an identical rerun
// after completion replays the original plan and commit, and a changed flag
// under the same key is a different request.
var _ = It("TestActualCLISourcesApproveExplicitKeyReplaysAfterCompletion", Label("persistence"), func() {
	t := GinkgoT()
	f := newSourceCommandFixture(t)
	_, flags := callerProfile(t, f.sel, f.session, f.token, f.base)
	project := registerProject(t, flags, "Explicit Key Project")
	sourceRoot := t.TempDir()
	approve := append([]string{"sources", "approve", "--project", project, "--source-id", "9d000000-0000-4000-8000-000000000001", "--kind", "files", "--root-path", sourceRoot, "--confirmed", "--operation-id", "9d000000-0000-4000-8000-0000000000aa"}, flags...)
	var first, again struct {
		Data struct {
			PlanID      string `json:"plan_id"`
			OperationID string `json:"operation_id"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(cli(t, 0, approve...), &first)).To(Succeed())
	Expect(first.Data.OperationID).To(Equal("9d000000-0000-4000-8000-0000000000aa"))
	revision := configRevision(t, f)
	Expect(json.Unmarshal(cli(t, 0, approve...), &again)).To(Succeed(), "the identical rerun replays")
	Expect(again.Data.PlanID).To(Equal(first.Data.PlanID), "no new plan")
	Expect(again.Data.OperationID).To(Equal(first.Data.OperationID))
	Expect(configRevision(t, f)).To(Equal(revision), "nothing committed twice")
	cli(t, 4, append([]string{"sources", "approve", "--project", project, "--source-id", "9d000000-0000-4000-8000-000000000001", "--kind", "files", "--root-path", t.TempDir(), "--confirmed", "--operation-id", "9d000000-0000-4000-8000-0000000000aa"}, flags...)...)
	Expect(configRevision(t, f)).To(Equal(revision))
	Expect(projectSources(t, f, project)).To(HaveLen(1))
})

// G7: a preview is committed later through --plan-id with its original
// expected revision and replays on rerun; approvals in a row keep every
// source.
var _ = It("TestActualCLISourcesApprovePreviewPlanIdAndApprovalsInARow", Label("persistence"), func() {
	t := GinkgoT()
	f := newSourceCommandFixture(t)
	_, flags := callerProfile(t, f.sel, f.session, f.token, f.base)
	project := registerProject(t, flags, "Journal Project")
	preview := cli(t, 0, append([]string{"sources", "approve", "--project", project, "--source-id", "9a000000-0000-4000-8000-000000000001", "--kind", "files", "--root-path", t.TempDir()}, flags...)...)
	var planOut struct {
		Data struct {
			PlanID string `json:"plan_id"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(preview, &planOut)).NotTo(HaveOccurred(), string(preview))
	Expect(planOut.Data.PlanID).NotTo(BeEmpty())
	Expect(projectSources(t, f, project)).To(BeEmpty(), "a preview never commits")
	commitPlan := append([]string{"sources", "approve", "--project", project, "--plan-id", planOut.Data.PlanID, "--confirmed"}, flags...)
	var committed struct {
		Data struct {
			PlanID      string `json:"plan_id"`
			OperationID string `json:"operation_id"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(cli(t, 0, commitPlan...), &committed)).To(Succeed())
	Expect(committed.Data.PlanID).To(Equal(planOut.Data.PlanID))
	Expect(committed.Data.OperationID).To(Equal(planOut.Data.PlanID))
	revision := configRevision(t, f)
	Expect(json.Unmarshal(cli(t, 0, commitPlan...), &committed)).To(Succeed(), "the recorded preview replays")
	Expect(committed.Data.OperationID).To(Equal(planOut.Data.PlanID))
	Expect(configRevision(t, f)).To(Equal(revision), "a replay commits nothing new")

	for _, id := range []string{"9a000000-0000-4000-8000-000000000002", "9a000000-0000-4000-8000-000000000003"} {
		cli(t, 0, append([]string{"sources", "approve", "--project", project, "--source-id", id, "--kind", "files", "--root-path", t.TempDir(), "--confirmed"}, flags...)...)
	}
	Expect(projectSources(t, f, project)).To(HaveLen(3))
})

// G7(e): an invalid retention mode fails before any request.
var _ = It("TestActualCLISourcesApproveRejectsInvalidRetentionMode", Label("persistence"), func() {
	t := GinkgoT()
	f := newSourceCommandFixture(t)
	proxy := &faultProxy{inner: f.handler, host: hostOf(t, f.base)}
	front := httptest.NewServer(proxy)
	DeferCleanup(front.Close)
	_, flags := callerProfile(t, f.sel, f.session, f.token, front.URL)
	cli(t, 2, append([]string{"sources", "approve", "--project", "9c000000-0000-4000-8000-000000000001", "--source-id", "9c000000-0000-4000-8000-000000000002", "--kind", "files", "--root-path", t.TempDir(), "--retention-mode", "forever", "--confirmed"}, flags...)...)
	// Only the CLI's own session and identity checks ran: no configuration
	// read and no plan.
	Expect(proxy.paths()).NotTo(ContainElement(HavePrefix("/api/v1/configuration")))
	Expect(proxy.paths()).To(ContainElement("/api/v1/capabilities"), "the request path was observed")
})

// G9: the seal wait has no time quota; a recorded error, abandonment or a
// stopped upload ends it at once.
var _ = It("TestActualCLIExpectationSealWaitEndsOnlyOnOutcome", Label("unit"), func() {
	states := func(sequence ...importer.UploadDetail) (func() (importer.UploadDetail, error), *int) {
		calls := 0
		return func() (importer.UploadDetail, error) {
			detail := sequence[len(sequence)-1]
			if calls < len(sequence) {
				detail = sequence[calls]
			}
			calls++
			return detail, nil
		}, &calls
	}
	sealing := importer.UploadDetail{}
	sealing.State = "sealing"
	sealed := importer.UploadDetail{}
	sealed.State = "sealed"
	// A slow seal keeps waiting far beyond any round count.
	long := make([]importer.UploadDetail, 0, 1001)
	for i := 0; i < 1000; i++ {
		long = append(long, sealing)
	}
	fetch, calls := states(append(long, sealed)...)
	Expect(pollUploadUntilSealed(context.Background(), fetch, time.Microsecond)).To(Succeed())
	Expect(*calls).To(Equal(1001))

	message := "worker exploded"
	failed := importer.UploadDetail{}
	failed.State = "open"
	failed.Error = &message
	fetch, calls = states(failed)
	err := pollUploadUntilSealed(context.Background(), fetch, time.Microsecond)
	Expect(err).To(MatchError(ContainSubstring("worker exploded")))
	Expect(*calls).To(Equal(1))

	stopped := importer.UploadDetail{}
	stopped.State = "open"
	fetch, calls = states(stopped)
	err = pollUploadUntilSealed(context.Background(), fetch, time.Microsecond)
	Expect(err).To(MatchError(ContainSubstring("explicit resume")))
	Expect(*calls).To(Equal(1), "a stopped upload ends the wait at once")

	abandoned := importer.UploadDetail{}
	abandoned.State = "abandoned"
	fetch, _ = states(abandoned)
	Expect(pollUploadUntilSealed(context.Background(), fetch, time.Microsecond)).To(MatchError(ContainSubstring("abandoned")))
})

// faultProxy forwards CLI requests to the fixture handler, recording them. For
// a matching request it can run before ahead of forwarding; with drop it loses
// the response after the server handled it (the request was applied); with
// status it answers that server error itself without forwarding.
type faultProxy struct {
	inner  http.Handler
	host   string
	method string
	path   string
	drop   bool
	status int
	before func()

	mu        sync.Mutex
	received  []string
	responses []recordedResponse
}

type recordedResponse struct {
	method, path string
	body         []byte
}

func (p *faultProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	clone := r.Clone(r.Context())
	clone.Host = p.host
	clone.URL.Host = p.host
	clone.RequestURI = ""
	matched := p.path != "" && r.Method == p.method && strings.Contains(r.URL.Path, p.path)
	p.mu.Lock()
	p.received = append(p.received, r.URL.Path)
	before := p.before
	if matched {
		p.before = nil
	}
	p.mu.Unlock()
	if matched && before != nil {
		before()
	}
	if matched && p.status != 0 {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(p.status)
		_, _ = w.Write([]byte(`{"error":{"code":"SERVICE_RECOVERING","message":"Service Unavailable","retryable":true,"details":{}},"request_id":"fault-proxy"}`))
		return
	}
	recorder := httptest.NewRecorder()
	p.inner.ServeHTTP(recorder, clone)
	p.mu.Lock()
	p.responses = append(p.responses, recordedResponse{method: r.Method, path: r.URL.Path, body: recorder.Body.Bytes()})
	p.mu.Unlock()
	if matched && p.drop {
		if hijacker, ok := w.(http.Hijacker); ok {
			if conn, _, err := hijacker.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
		panic(http.ErrAbortHandler)
	}
	for name, values := range recorder.Header() {
		w.Header()[name] = values
	}
	w.WriteHeader(recorder.Code)
	_, _ = w.Write(recorder.Body.Bytes())
}

// paths lists the request paths the proxy received, in order.
func (p *faultProxy) paths() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string{}, p.received...)
}

// planID returns the plan id of the single plan created through the proxy.
func (p *faultProxy) planID(t GinkgoTInterface) string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := map[string]bool{}
	for _, response := range p.responses {
		if response.method != http.MethodPost || response.path != "/api/v1/configuration/plans" {
			continue
		}
		var body struct {
			Data struct {
				PlanID string `json:"plan_id"`
			} `json:"data"`
		}
		ExpectWithOffset(1, json.Unmarshal(response.body, &body)).To(Succeed())
		ids[body.Data.PlanID] = true
	}
	ExpectWithOffset(1, ids).To(HaveLen(1), "exactly one plan went through the proxy")
	for id := range ids {
		return id
	}
	return ""
}

// sharedCaller returns CLI flags for one caller directory shared by several
// endpoints (a proxy and the fixture itself), so an interrupted run and its
// rerun use the same journals and pending keys.
func (f *sourceCommandFixture) sharedCaller(t GinkgoTInterface, root, base string) []string {
	t.Helper()
	credential := filepath.Join(root, "credential-"+strings.NewReplacer(":", "-", "/", "-").Replace(hostOf(t, base))+".json")
	raw, err := json.Marshal(client.Credential{Endpoint: base, Profile: f.sel.Profile, LibraryID: f.session.LibraryID, Token: f.token})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, os.WriteFile(credential, raw, 0o600)).To(Succeed())
	return []string{"--profile", f.sel.Profile, "--client-dir", root, "--credential", credential, "--json"}
}

func registerProject(t GinkgoTInterface, flags []string, name string) string {
	t.Helper()
	out := cli(t, 0, append([]string{"projects", "register", "--name", name}, flags...)...)
	var registered struct {
		Data struct {
			Result struct {
				ProjectID string `json:"project_id"`
			} `json:"result"`
		} `json:"data"`
	}
	ExpectWithOffset(1, json.Unmarshal(out, &registered)).To(Succeed(), string(out))
	ExpectWithOffset(1, registered.Data.Result.ProjectID).NotTo(BeEmpty())
	return registered.Data.Result.ProjectID
}

func configRevision(t GinkgoTInterface, f sourceCommandFixture) int64 {
	t.Helper()
	configuration, err := f.store.Configuration(context.Background())
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return configuration.Revision
}

// bumpConfiguration commits an unrelated configuration revision.
func bumpConfiguration(t GinkgoTInterface, f sourceCommandFixture) {
	t.Helper()
	ExpectWithOffset(1, f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		current, err := tx.Configuration()
		if err != nil {
			return err
		}
		return tx.SetConfiguration(domain.Configuration{Revision: current.Revision, Value: current.Value, Actor: "fixture"}, current.Revision)
	})).To(Succeed())
}

func projectSources(t GinkgoTInterface, f sourceCommandFixture, project string) []map[string]any {
	t.Helper()
	var config struct {
		Configuration struct {
			Projects map[string]struct {
				Sources []map[string]any `json:"sources"`
			} `json:"projects"`
		} `json:"configuration"`
	}
	ExpectWithOffset(1, f.ReadConfigurationForTest(t, &config)).To(Succeed())
	return config.Configuration.Projects[project].Sources
}

// pendingOperations lists the operation ids of the caller's pending commands.
func pendingOperations(t GinkgoTInterface, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "pending"))
	if os.IsNotExist(err) {
		return nil
	}
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ids := []string{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, "pending", entry.Name()))
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		var saved struct {
			ID string `json:"operation_id"`
		}
		ExpectWithOffset(1, json.Unmarshal(raw, &saved)).To(Succeed())
		ids = append(ids, saved.ID)
	}
	return ids
}

// failedOperation returns the operation id a definitive failure reported.
func failedOperation(t GinkgoTInterface, out []byte) string {
	t.Helper()
	var failure struct {
		Data struct {
			OperationID string `json:"operation_id"`
		} `json:"data"`
	}
	ExpectWithOffset(1, json.Unmarshal(out, &failure)).To(Succeed(), string(out))
	ExpectWithOffset(1, failure.Data.OperationID).NotTo(BeEmpty(), string(out))
	return failure.Data.OperationID
}
