package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/ai/codex"
	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	httpconfig "haeryong.local/haeryong/internal/adapters/http/configuration"
	httpimports "haeryong.local/haeryong/internal/adapters/http/imports"
	httpv1 "haeryong.local/haeryong/internal/adapters/http/v1"
	"haeryong.local/haeryong/internal/adapters/localcontrol"
	appauth "haeryong.local/haeryong/internal/application/auth"
	appconfig "haeryong.local/haeryong/internal/application/configuration"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/questions"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

// Only the provider boundary is controlled; the CLI, authenticated HTTP,
// receipts, stream/seal consumers, dispatcher and SQLite remain real.
type questionCLIOutcome struct {
	store   *foundation.Store
	unknown bool
	calls   atomic.Int32
}

func (d *questionCLIOutcome) Execute(ctx context.Context, a domain.ProviderAttempt, _ []domain.ProviderInput, send func() error, ack func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	var q domain.Question
	if e := d.store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.GuardProviderPayload(a.JobID, a.ID, false); e != nil {
			return e
		}
		var e error
		q, e = tx.QuestionByJob(a.JobID)
		return e
	}); e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	for _, descriptor := range []domain.ProviderText{q.Text, func() domain.ProviderText {
		if q.Context == nil {
			return domain.ProviderText{}
		}
		return domain.ProviderText{Hash: q.Context.Hash, Size: q.Context.Size}
	}()} {
		if descriptor.Hash == "" {
			continue
		}
		var object domain.Object
		if e := d.store.Write(ctx, func(tx app.WriteTx) error { var e error; object, e = tx.Object(descriptor.Hash); return e }); e != nil {
			return work.ProviderOutcome{LocalClosed: true}, e
		}
		if e := d.store.Read(ctx, object, domain.Pin{Kind: "controlled_question_input", OwnerID: a.ID}, func(r io.ReadSeeker) error {
			n, e := io.Copy(io.Discard, r)
			if e == nil && n != descriptor.Size {
				return domain.ErrContentIntegrity
			}
			return e
		}); e != nil {
			return work.ProviderOutcome{LocalClosed: true}, e
		}
	}
	if e := send(); e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	d.calls.Add(1)
	if e := ack(work.ProviderProgress{RequestID: a.ID, SessionID: a.JobID}); e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	if d.unknown {
		return work.ProviderOutcome{State: "outcome_unknown", LocalClosed: true, RequestID: a.ID, SessionID: a.JobID}, nil
	}
	raw, e := json.Marshal(domain.AnswerEnvelope{State: "final", Body: "Controlled saved CLI answer", Citations: []domain.QuestionCitation{}, ExplanationReferences: []domain.ExplanationReference{q.Explanation.ExplanationReference}, Limitations: []domain.QuestionLimitation{}})
	if e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	return work.ProviderOutcome{State: "result_saved", LocalClosed: true, CompletionObserved: true, RequestID: a.ID, SessionID: a.JobID, Body: io.NopCloser(bytes.NewReader(raw))}, nil
}
func (d *questionCLIOutcome) Cancel(context.Context, domain.ProviderAttempt) error {
	return domain.ErrReconciliation
}
func (d *questionCLIOutcome) Reconcile(context.Context, domain.ProviderAttempt, bool) (work.ProviderOutcome, error) {
	return work.ProviderOutcome{}, domain.ErrReconciliation
}

type questionCLIApplication struct {
	id     service.Identity
	done   chan error
	driver *questionCLIOutcome
}

func startQuestionCLIApplication(t GinkgoTInterface, s service.Selection, unknown bool, wrappers ...func(http.Handler) http.Handler) *questionCLIApplication {
	store := foundation.New(nil)
	auth := appauth.New(store)
	imports := importer.New(store, auth, store, staging.New())
	queries := query.New(store, auth, store)
	queries.QuestionsAvailable = true
	questionsCore := questions.New(store, auth, nil, codex.SemanticRequestSHA)
	configurationCore := appconfig.New(store, auth, nil)
	configurationCore.ProviderTransportAvailable = true
	configurationCore.QuestionsAvailable = true
	driver := &questionCLIOutcome{store: store, unknown: unknown}
	endpoint := &localcontrol.HTTP{Auth: auth, Product: func(origin string) (http.Handler, error) {
		cfg, e := store.Configuration(context.Background())
		if e != nil {
			return nil, e
		}
		handler, e := productHandler(auth, origin, cfg.Value, productRoutes{httpconfig.New(configurationCore), httpimports.NewQuestions(imports, nil, questionsCore), httpv1.New(queries, nil, func() (string, string) { return origin, "" })})
		if e != nil {
			return nil, e
		}
		for _, wrap := range wrappers {
			handler = wrap(handler)
		}
		return handler, nil
	}}
	runner := service.Runner{Selection: s, Port: availablePort(t), Storage: store, Endpoint: endpoint, Work: (work.Dispatcher{Store: store, Imports: imports, Provider: &work.ProviderCoordinator{Store: store, Bytes: store, Driver: driver, Prepare: questionsCore.Prepare, Apply: questionsCore.Apply}, Derived: work.NewMaintenance(store, store)}).Run}
	ctx, cancel := context.WithCancel(context.Background())
	a := &questionCLIApplication{done: make(chan error, 1), driver: driver}
	ready := make(chan service.Identity, 1)
	go func() { a.done <- runner.Run(ctx, func(id service.Identity) { ready <- id }); close(a.done) }()
	Eventually(ready, 8*time.Second).Should(Receive(&a.id))
	DeferCleanup(func() {
		cancel()
		select {
		case <-a.done:
		case <-time.After(8 * time.Second):
			Fail("controlled question application cleanup timed out")
		}
	})
	return a
}
func questionCLISeed(t GinkgoTInterface, flags []string, state *clientstate.Store, api *client.Client) string {
	input := filepath.Join(t.TempDir(), "explanation")
	Expect(os.Mkdir(input, 0700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(input, "page.html"), []byte("<html>normal saved explanation</html>"), 0600)).To(Succeed())
	k, _ := service.NewID()
	cli(t, 0, append([]string{"import", input, "--operation-id", k}, flags...)...)
	journal, e := state.Load(k)
	Expect(e).NotTo(HaveOccurred())
	Expect(journal.Response).NotTo(BeNil())
	var accepted struct {
		Job string `json:"job_id"`
	}
	Expect(json.Unmarshal(journal.Response.Body, &accepted)).To(Succeed())
	job, e := api.Job(context.Background(), accepted.Job)
	Expect(e).NotTo(HaveOccurred())
	var result struct {
		Version string `json:"version_id"`
	}
	Expect(json.Unmarshal(job.Result, &result)).To(Succeed())
	Expect(result.Version).NotTo(BeEmpty())
	return result.Version
}

var _ = It("T5QCLI01 real owner CLI stages complete question/context independently and exact saved replay/read executes zero", Label("persistence"), func() {
	t := GinkgoT()
	s := selection(t)
	initProfile(t, s)
	daemon := startQuestionCLIApplication(t, s, false)
	owner, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "controlled Ask owner")
	root, flags := callerProfile(t, s, owner, token, daemon.id.Endpoint)
	flags = append(flags, "--data-dir", s.DataDir)
	api, e := client.New(daemon.id.Endpoint, s.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: s.Profile, LibraryID: owner.LibraryID, Token: token})
	Expect(e).NotTo(HaveOccurred())
	state, e := clientstate.New(root)
	Expect(e).NotTo(HaveOccurred())
	status := cli(t, 0, append([]string{"status"}, flags...)...)
	var reported struct {
		Data struct {
			Provider struct {
				Question  bool   `json:"question_available"`
				Available any    `json:"available"`
				Reason    string `json:"reason"`
			} `json:"provider"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(status, &reported)).To(Succeed())
	Expect(reported.Data.Provider.Question).To(BeTrue())
	Expect(reported.Data.Provider.Available).To(BeNil())
	Expect(reported.Data.Provider.Reason).To(Equal("status_not_observed"))
	Expect(daemon.driver.calls.Load()).To(BeZero())
	version := questionCLISeed(t, flags, state, api)
	question := strings.Repeat("complete original CLI question π\n", 4096)
	display := map[string]any{"selected_text": strings.Repeat("full captured context 한글\n", 4096), "scroll": 41}
	raw, e := json.Marshal(map[string]any{"question": question, "display_state": display, "page_path": "page.html", "region_id": nil, "page_query": "original=1", "page_fragment": "exact", "provider_id": "codex", "source_selection": map[string]any{"selections": []any{}}})
	Expect(e).NotTo(HaveOccurred())
	path := filepath.Join(t.TempDir(), "question.json")
	Expect(os.WriteFile(path, raw, 0600)).To(Succeed())
	k, _ := service.NewID()
	out := cli(t, 0, append([]string{"ask", "--version", version, "--request-file", path, "--operation-id", k}, flags...)...)
	Expect(string(out)).To(ContainSubstring("Controlled saved CLI answer"))
	Expect(bytes.Contains(out, []byte(token))).To(BeFalse())
	j, e := state.Load(k)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.Question).NotTo(BeNil())
	Expect(j.Question.Launcher.PrincipalID).To(Equal(owner.PrincipalID))
	Expect(j.Identity.PrincipalID).To(Equal(owner.PrincipalID))
	Expect(j.Question.Context).NotTo(BeNil())
	Expect(j.Question.ContextRef).NotTo(BeEmpty())
	Expect(j.Question.ContextKey).NotTo(Equal(k))
	Expect(j.Result.CreateKey).NotTo(Equal(j.Question.Context.CreateKey))
	Expect(j.Result.CompleteKey).NotTo(Equal(j.Question.Context.CompleteKey))
	Expect(string(j.FinalBody)).NotTo(ContainSubstring("complete original CLI question"))
	Expect(string(j.FinalBody)).NotTo(ContainSubstring("full captured context"))
	Expect(string(j.FinalBody)).NotTo(ContainSubstring("display_state"))
	var request questions.Request
	Expect(json.Unmarshal(j.FinalBody, &request)).To(Succeed())
	Expect(request.BodyRef).NotTo(BeNil())
	Expect(request.ContextRef).NotTo(BeNil())
	Expect(request.PageQuery).To(Equal("original=1"))
	Expect(request.PageFragment).To(Equal("exact"))
	Expect(request.PagePath).NotTo(BeNil())
	Expect(*request.PagePath).To(Equal("page.html"))
	var accepted struct {
		Question string `json:"question_id"`
	}
	Expect(json.Unmarshal(j.Response.Body, &accepted)).To(Succeed())
	Expect(os.Remove(path)).To(Succeed())
	cli(t, 0, append([]string{"ask", "--version", version, "--request-file", path, "--operation-id", k}, flags...)...)
	saved := cli(t, 0, append([]string{"questions", "show", accepted.Question}, flags...)...)
	Expect(string(saved)).To(ContainSubstring("Controlled saved CLI answer"))
	Expect(string(saved)).To(ContainSubstring("full captured context"))
	Expect(string(saved)).To(ContainSubstring("complete original CLI question"))
	Expect(daemon.driver.calls.Load()).To(Equal(int32(1)))
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
	var stopped error
	Eventually(daemon.done, 8*time.Second).Should(Receive(&stopped))
	Expect(stopped).NotTo(HaveOccurred())
})

var _ = It("T5QCLI02 uncertain controlled question keeps automatic original K and saved inputs after request removal with no fresh Execute", Label("persistence"), func() {
	t := GinkgoT()
	s := selection(t)
	initProfile(t, s)
	daemon := startQuestionCLIApplication(t, s, true)
	owner, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "controlled unknown Ask owner")
	root, flags := callerProfile(t, s, owner, token, daemon.id.Endpoint)
	flags = append(flags, "--data-dir", s.DataDir)
	api, e := client.New(daemon.id.Endpoint, s.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: s.Profile, LibraryID: owner.LibraryID, Token: token})
	Expect(e).NotTo(HaveOccurred())
	state, e := clientstate.New(root)
	Expect(e).NotTo(HaveOccurred())
	version := questionCLISeed(t, flags, state, api)
	input := filepath.Join(t.TempDir(), "question.json")
	Expect(os.WriteFile(input, []byte(`{"question":"one original uncertain question","provider_id":"codex","source_selection":{"selections":[]}}`), 0600)).To(Succeed())
	args := append([]string{"ask", "--version", version, "--request-file", input}, flags...)
	first := cli(t, 6, args...)
	pending := pendingOperations(t, root)
	Expect(pending).To(HaveLen(1))
	k := pending[0]
	Expect(string(first)).To(ContainSubstring(k))
	before, e := state.Load(k)
	Expect(e).NotTo(HaveOccurred())
	Expect(before.Question.Launcher.PrincipalID).To(Equal(owner.PrincipalID))
	Expect(before.Response).NotTo(BeNil())
	Expect(daemon.driver.calls.Load()).To(Equal(int32(1)))
	Expect(os.Remove(input)).To(Succeed())
	again := cli(t, 6, args...)
	Expect(string(again)).To(ContainSubstring(k))
	after, e := state.Load(k)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.FinalBody).To(Equal(before.FinalBody))
	Expect(after.Result.CreateKey).To(Equal(before.Result.CreateKey))
	Expect(after.Result.CompleteKey).To(Equal(before.Result.CompleteKey))
	Expect(pendingOperations(t, root)).To(Equal([]string{k}))
	Expect(daemon.driver.calls.Load()).To(Equal(int32(1)))
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
	var stopped error
	Eventually(daemon.done, 8*time.Second).Should(Receive(&stopped))
	Expect(stopped).NotTo(HaveOccurred())
})

var _ = It("T5QCLI03 successful saved answer cannot acknowledge automatic pending identity until caller output succeeds", Label("persistence"), func() {
	t := GinkgoT()
	s := selection(t)
	initProfile(t, s)
	daemon := startQuestionCLIApplication(t, s, false)
	owner, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "controlled output Ask owner")
	root, flags := callerProfile(t, s, owner, token, daemon.id.Endpoint)
	flags = append(flags, "--data-dir", s.DataDir)
	api, e := client.New(daemon.id.Endpoint, s.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: s.Profile, LibraryID: owner.LibraryID, Token: token})
	Expect(e).NotTo(HaveOccurred())
	state, e := clientstate.New(root)
	Expect(e).NotTo(HaveOccurred())
	version := questionCLISeed(t, flags, state, api)
	input := filepath.Join(t.TempDir(), "question.json")
	Expect(os.WriteFile(input, []byte(`{"question":"one original output question","provider_id":"codex","source_selection":{"selections":[]}}`), 0600)).To(Succeed())
	args := append([]string{"ask", "--version", version, "--request-file", input}, flags...)
	opt, e := parse(args)
	Expect(e).NotTo(HaveOccurred())
	e = questionCommand(context.Background(), opt, func(any) error { return io.ErrClosedPipe })
	Expect(errors.Is(e, io.ErrClosedPipe)).To(BeTrue())
	pending := pendingOperations(t, root)
	Expect(pending).To(HaveLen(1))
	k := pending[0]
	before, e := state.Load(k)
	Expect(e).NotTo(HaveOccurred())
	Expect(before.Response).NotTo(BeNil())
	Expect(daemon.driver.calls.Load()).To(Equal(int32(1)))
	Expect(os.Remove(input)).To(Succeed())
	cli(t, 0, args...)
	Expect(pendingOperations(t, root)).To(BeEmpty())
	after, e := state.Load(k)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.FinalBody).To(Equal(before.FinalBody))
	Expect(after.Identity).To(Equal(before.Identity))
	Expect(daemon.driver.calls.Load()).To(Equal(int32(1)))
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
	var stopped error
	Eventually(daemon.done, 8*time.Second).Should(Receive(&stopped))
	Expect(stopped).NotTo(HaveOccurred())
})
