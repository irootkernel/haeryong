package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"archive/zip"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/ai/codex"
	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	httpimports "haeryong.local/haeryong/internal/adapters/http/imports"
	httpv1 "haeryong.local/haeryong/internal/adapters/http/v1"
	"haeryong.local/haeryong/internal/adapters/localcontrol"
	appauth "haeryong.local/haeryong/internal/application/auth"
	generation "haeryong.local/haeryong/internal/application/generation"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"haeryong.local/haeryong/skills"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"
)

// The existing dispatcher port controls only synthetic outcomes. The current
// CLI, auth/IPC, HTTP, storage, upload and generation consumers remain real.
type generationCLIOutcome struct {
	store   *foundation.Store
	unknown bool
	calls   atomic.Int32
}

func (d *generationCLIOutcome) Execute(ctx context.Context, a domain.ProviderAttempt, _ []domain.ProviderInput, send func() error, ack func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	var prompt domain.Object
	e := d.store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.GuardProviderPayload(a.JobID, a.ID, false); e != nil {
			return e
		}
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		var scope domain.ProviderScope
		if json.Unmarshal(j.Scope, &scope) != nil || scope.Invocation == nil || scope.Invocation.PromptRef == nil {
			return domain.ErrInvalidTarget
		}
		prompt, e = tx.Object(scope.Invocation.PromptRef.Hash)
		return e
	})
	if e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	e = d.store.Read(ctx, prompt, domain.Pin{Kind: "test_read", OwnerID: a.ID}, func(r io.ReadSeeker) error {
		n, e := io.Copy(io.Discard, r)
		if e == nil && n <= 100000 {
			return domain.ErrContentIntegrity
		}
		return e
	})
	if e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	if e = send(); e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	d.calls.Add(1)
	request, session := "controlled-request:"+a.ID, "controlled-session:"+a.ID
	if e = ack(work.ProviderProgress{RequestID: request, SessionID: session}); e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	if d.unknown {
		return work.ProviderOutcome{State: "outcome_unknown", LocalClosed: true, RequestID: request, SessionID: session}, nil
	}
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, path := range []string{"read.html", "nested/next.html"} {
		w, e := z.Create(path)
		if e != nil {
			return work.ProviderOutcome{LocalClosed: true}, e
		}
		if _, e = w.Write([]byte("<html>complete " + path + "</html>")); e != nil {
			return work.ProviderOutcome{LocalClosed: true}, e
		}
	}
	if e = z.Close(); e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	return work.ProviderOutcome{State: "result_saved", CompletionObserved: true, LocalClosed: true, RequestID: request, SessionID: session, Body: io.NopCloser(bytes.NewReader(out.Bytes()))}, nil
}
func (d *generationCLIOutcome) Cancel(context.Context, domain.ProviderAttempt) error {
	return domain.ErrReconciliation
}
func (d *generationCLIOutcome) Reconcile(context.Context, domain.ProviderAttempt, bool) (work.ProviderOutcome, error) {
	return work.ProviderOutcome{}, domain.ErrReconciliation
}

type generationCLIApplication struct {
	id     service.Identity
	done   chan error
	cancel context.CancelFunc
	driver *generationCLIOutcome
}

func (a *generationCLIApplication) wait(t GinkgoTInterface) {
	GinkgoHelper()
	var err error
	Eventually(a.done, 8*time.Second).Should(Receive(&err))
	Expect(err).NotTo(HaveOccurred())
}
func startGenerationCLIApplication(t GinkgoTInterface, s service.Selection, unknown bool, wrappers ...func(http.Handler) http.Handler) *generationCLIApplication {
	store := foundation.New(nil)
	auth := appauth.New(store)
	imports := importer.New(store, auth, store, staging.New())
	queries := query.New(store, auth, store)
	driver := &generationCLIOutcome{store: store, unknown: unknown}
	endpoint := &localcontrol.HTTP{Auth: auth, Product: func(origin string) (http.Handler, error) {
		profile, e := service.Load(s)
		if e != nil {
			return nil, e
		}
		path, owned, e := skills.Materialize(filepath.Join(profile.DataDir, "storage", "owned-skills"))
		if e != nil {
			return nil, e
		}
		gen := generation.New(store, auth, nil, domain.GenerationInvocation{SkillPath: path, SkillSHA: owned.SHA256, SkillVersion: owned.Version}, codex.SemanticRequestSHA)
		cfg, e := store.Configuration(context.Background())
		if e != nil {
			return nil, e
		}
		handler, e := productHandler(auth, origin, cfg.Value, productRoutes{httpimports.NewGeneration(imports, gen), httpv1.New(queries, nil, func() (string, string) { return origin, "" })})
		if e != nil {
			return nil, e
		}
		for _, wrap := range wrappers {
			handler = wrap(handler)
		}
		return handler, nil
	}}
	runner := service.Runner{Selection: s, Port: availablePort(t), Storage: store, Endpoint: endpoint, Work: (work.Dispatcher{Store: store, Imports: imports, Provider: &work.ProviderCoordinator{Store: store, Bytes: store, Driver: driver, Prepare: imports.PrepareGeneration, Apply: imports.ApplyGeneration}, Derived: work.NewMaintenance(store, store)}).Run}
	ctx, cancel := context.WithCancel(context.Background())
	a := &generationCLIApplication{done: make(chan error, 1), cancel: cancel, driver: driver}
	ready := make(chan service.Identity, 1)
	go func() { a.done <- runner.Run(ctx, func(id service.Identity) { ready <- id }); close(a.done) }()
	Eventually(ready, 8*time.Second).Should(Receive(&a.id))
	DeferCleanup(func() {
		cancel()
		select {
		case <-a.done:
		case <-time.After(8 * time.Second):
			Fail("controlled application cleanup timed out")
		}
	})
	return a
}
func generationCLIInput(path, project string) {
	body, _ := json.Marshal(map[string]any{"project_id": project, "document_id": nil, "intent": "draft", "provider_id": "codex", "prompt": strings.Repeat("complete CLI prompt π\n", 10000), "source_selection": map[string]any{"selections": []any{}}})
	Expect(os.WriteFile(path, body, 0600)).To(Succeed())
}

var _ = It("GCLI01 actual local CLI automatically reuses scoped workflow issuance and stages large prompt with frozen final principal and K before the existing managed generation dispatcher and consumers", Label("persistence"), func() {
	t := GinkgoT()
	selection := selection(t)
	initProfile(t, selection)
	daemon := startGenerationCLIApplication(t, selection, false)
	owner, token := bootstrapOwnerCLI(t, selection, daemon.id.Endpoint, "generation CLI owner")
	root, flags := callerProfile(t, selection, owner, token, daemon.id.Endpoint)
	flags = append(flags, "--data-dir", selection.DataDir)
	api, e := client.New(daemon.id.Endpoint, selection.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: selection.Profile, LibraryID: owner.LibraryID, Token: token})
	Expect(e).NotTo(HaveOccurred())
	identity, e := api.Identity(context.Background())
	Expect(e).NotTo(HaveOccurred())
	input := filepath.Join(t.TempDir(), "request.json")
	generationCLIInput(input, identity.UnfiledProjectID)
	key, _ := service.NewID()
	raw := cli(t, 0, append([]string{"generate", "--request-file", input, "--operation-id", key}, flags...)...)
	state, e := clientstate.New(root)
	Expect(e).NotTo(HaveOccurred())
	journal, e := state.Load(key)
	Expect(e).NotTo(HaveOccurred())
	Expect(journal.Purpose).To(Equal("generation"))
	Expect(journal.Generation.DispatchReady).To(BeTrue())
	Expect(journal.Generation.CredentialPath).NotTo(BeEmpty())
	Expect(journal.Identity.PrincipalID).NotTo(Equal(journal.Generation.Launcher.PrincipalID))
	Expect(journal.Result.UploadID).NotTo(BeEmpty())
	Expect(journal.Result.CreateKey).NotTo(Equal(key))
	Expect(journal.Result.CompleteKey).NotTo(Equal(key))
	Expect(bytes.Contains(journal.FinalBody, []byte("complete CLI prompt"))).To(BeFalse())
	workflow, e := client.LoadCredential(journal.Generation.CredentialPath)
	Expect(e).NotTo(HaveOccurred())
	Expect(bytes.Contains(raw, []byte(workflow.Token))).To(BeFalse())
	Expect(bytes.Contains(raw, []byte(token))).To(BeFalse())
	Expect(os.Remove(input)).To(Succeed())
	cli(t, 0, append([]string{"generate", "--request-file", input, "--operation-id", key}, flags...)...)
	Expect(daemon.driver.calls.Load()).To(Equal(int32(1)))
	// A new intended request in the same project automatically reuses the same
	// remembered grant and final principal rather than minting another identity.
	generationCLIInput(input, identity.UnfiledProjectID)
	second, _ := service.NewID()
	cli(t, 0, append([]string{"generate", "--request-file", input, "--operation-id", second}, flags...)...)
	again, e := state.Load(second)
	Expect(e).NotTo(HaveOccurred())
	Expect(again.Generation.CredentialPath).To(Equal(journal.Generation.CredentialPath))
	Expect(again.Identity.PrincipalID).To(Equal(journal.Identity.PrincipalID))
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(selection)...)...)
	daemon.wait(t)
})
var _ = It("GCLI02 unknown managed outcome keeps the implicit pending K and exact replay after request file removal never dispatches another controlled dispatch", Label("persistence"), func() {
	t := GinkgoT()
	selection := selection(t)
	initProfile(t, selection)
	daemon := startGenerationCLIApplication(t, selection, true)
	owner, token := bootstrapOwnerCLI(t, selection, daemon.id.Endpoint, "unknown generation owner")
	root, flags := callerProfile(t, selection, owner, token, daemon.id.Endpoint)
	flags = append(flags, "--data-dir", selection.DataDir)
	api, e := client.New(daemon.id.Endpoint, selection.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: selection.Profile, LibraryID: owner.LibraryID, Token: token})
	Expect(e).NotTo(HaveOccurred())
	identity, e := api.Identity(context.Background())
	Expect(e).NotTo(HaveOccurred())
	input := filepath.Join(t.TempDir(), "request.json")
	generationCLIInput(input, identity.UnfiledProjectID)
	raw := cli(t, 6, append([]string{"generate", "--request-file", input}, flags...)...)
	var result struct {
		Data struct {
			Operation string `json:"operation_id"`
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	Expect(decoder.Decode(&result)).To(Succeed())
	var final struct {
		Error struct {
			Code string `json:"code"`
		}
	}
	Expect(decoder.Decode(&final)).To(Succeed())
	Expect(final.Error.Code).To(Equal("RECONCILIATION_REQUIRED"))
	Expect(result.Data.Operation).NotTo(BeEmpty())
	state, e := clientstate.New(root)
	Expect(e).NotTo(HaveOccurred())
	before, e := state.Load(result.Data.Operation)
	Expect(e).NotTo(HaveOccurred())
	candidate, _ := service.NewID()
	same, e := state.UseCommandPending(context.Background(), identity, clientstate.CommandIntent{Method: "POST", Target: "/api/v1/generations", SourcePath: input}, candidate)
	Expect(e).NotTo(HaveOccurred())
	Expect(same).To(Equal(before.OperationID))
	Expect(os.Remove(input)).To(Succeed())
	cli(t, 6, append([]string{"generate", "--request-file", input}, flags...)...)
	Expect(daemon.driver.calls.Load()).To(Equal(int32(1)))
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(selection)...)...)
	daemon.wait(t)
})

var _ = It("GCLI03 actual production executable keeps binary SHA refusal before native execution and retains its paused original implicit request", Label("persistence"), func() {
	t := GinkgoT()
	selection := selection(t)
	initProfile(t, selection)
	peer := filepath.Join(t.TempDir(), "unsupported-peer")
	Expect(os.WriteFile(peer, []byte("#!/bin/sh\nexit 9\n"), 0700)).To(Succeed())
	daemon := startDaemon(t, selection, availablePort(t), "--provider-binary", peer)
	owner, token := bootstrapOwnerCLI(t, selection, daemon.id.Endpoint, "unsupported peer owner")
	root, flags := callerProfile(t, selection, owner, token, daemon.id.Endpoint)
	flags = append(flags, "--data-dir", selection.DataDir)
	api, e := client.New(daemon.id.Endpoint, selection.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: selection.Profile, LibraryID: owner.LibraryID, Token: token})
	Expect(e).NotTo(HaveOccurred())
	identity, e := api.Identity(context.Background())
	Expect(e).NotTo(HaveOccurred())
	input := filepath.Join(t.TempDir(), "request.json")
	generationCLIInput(input, identity.UnfiledProjectID)
	raw := cli(t, 5, append([]string{"generate", "--request-file", input}, flags...)...)
	var response struct {
		Data struct {
			Operation string     `json:"operation_id"`
			Job       domain.Job `json:"job_outcome"`
		}
	}
	Expect(json.NewDecoder(bytes.NewReader(raw)).Decode(&response)).To(Succeed())
	Expect(response.Data.Job.State).To(Equal("paused"))
	Expect(response.Data.Job.Error).To(Equal("unsupported_version"))
	Expect(response.Data.Job.ProviderAttempts.Unknown).To(BeZero())
	Expect(response.Data.Job.ProviderAttempts.LatestState).To(Equal("prepared"))
	state, e := clientstate.New(root)
	Expect(e).NotTo(HaveOccurred())
	candidate, _ := service.NewID()
	same, e := state.UseCommandPending(context.Background(), identity, clientstate.CommandIntent{Method: "POST", Target: "/api/v1/generations", SourcePath: input}, candidate)
	Expect(e).NotTo(HaveOccurred())
	Expect(same).To(Equal(response.Data.Operation))
	Expect(os.Remove(input)).To(Succeed())
	again := cli(t, 5, append([]string{"generate", "--request-file", input}, flags...)...)
	var replayed struct {
		Data struct {
			Operation string     `json:"operation_id"`
			Job       domain.Job `json:"job_outcome"`
		}
	}
	Expect(json.NewDecoder(bytes.NewReader(again)).Decode(&replayed)).To(Succeed())
	Expect(replayed.Data.Operation).To(Equal(response.Data.Operation))
	Expect(replayed.Data.Job.ID).To(Equal(response.Data.Job.ID))
	Expect(replayed.Data.Job.ProviderAttempts.Count).To(Equal(1))
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(selection)...)...)
	daemon.wait(t)
})
var _ = It("GCLI04 explicit owner endpoint submission stays on its frozen original principal and never changes to local issuance during replay", Label("persistence"), func() {
	t := GinkgoT()
	selection := selection(t)
	initProfile(t, selection)
	application := startGenerationCLIApplication(t, selection, false)
	owner, token := bootstrapOwnerCLI(t, selection, application.id.Endpoint, "explicit owner API")
	root, flags := callerProfile(t, selection, owner, token, application.id.Endpoint)
	flags = append(flags, "--data-dir", selection.DataDir)
	api, e := client.New(application.id.Endpoint, selection.Profile, client.Credential{Endpoint: application.id.Endpoint, Profile: selection.Profile, LibraryID: owner.LibraryID, Token: token})
	Expect(e).NotTo(HaveOccurred())
	identity, e := api.Identity(context.Background())
	Expect(e).NotTo(HaveOccurred())
	input := filepath.Join(t.TempDir(), "request.json")
	generationCLIInput(input, identity.UnfiledProjectID)
	key, _ := service.NewID()
	explicit := append(append([]string{}, flags...), "--endpoint", application.id.Endpoint)
	cli(t, 0, append([]string{"generate", "--request-file", input, "--operation-id", key}, explicit...)...)
	state, e := clientstate.New(root)
	Expect(e).NotTo(HaveOccurred())
	journal, e := state.Load(key)
	Expect(e).NotTo(HaveOccurred())
	Expect(journal.Generation.DispatchReady).To(BeTrue())
	Expect(journal.Generation.CredentialPath).To(BeEmpty())
	Expect(journal.Identity.PrincipalID).To(Equal(identity.PrincipalID))
	Expect(journal.Generation.Launcher.PrincipalID).To(Equal(identity.PrincipalID))
	Expect(os.Remove(input)).To(Succeed())
	// Omission of the override now exposes local discovery, but the operation's
	// prepared identity stays owner-authenticated; it cannot mint/switch midway.
	cli(t, 0, append([]string{"generate", "--request-file", input, "--operation-id", key}, flags...)...)
	Expect(application.driver.calls.Load()).To(Equal(int32(1)))
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(selection)...)...)
	application.wait(t)
})

// This peer controls only final refusal/receipt and transport outcomes. The real
// CLI, client, immutable journal, pending index and output acknowledgement run.
type r3CompositePeer struct {
	mu            sync.Mutex
	status        int
	code          string
	mode          string
	runtime       string
	beforeReceipt func()
	requests      []c2CommandRequest
	lookups       []string
}

func (p *r3CompositePeer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Connection", "close")
	data := func(status int, v any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
	}
	failure := func(status int, code string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code}})
	}
	switch {
	case r.URL.Path == "/api/v1/capabilities":
		data(200, map[string]any{"library_id": "r3-library", "runtime_generation": p.runtime, "unfiled_project_id": c2ReviewVersion})
	case r.URL.Path == "/api/v1/session":
		data(200, domain.Session{LibraryID: "r3-library", PrincipalID: "r3-owner", Scope: domain.CredentialScope{Kind: "owner"}})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/operations/"):
		p.lookups = append(p.lookups, strings.TrimPrefix(r.URL.Path, "/api/v1/operations/"))
		if p.beforeReceipt != nil {
			action := p.beforeReceipt
			p.beforeReceipt = nil
			action()
		}
		if p.mode == "accepted" {
			data(200, map[string]any{"state": "completed", "original_status": 202, "result_metadata": map[string]any{}})
			return
		}
		if p.mode == "recorded" {
			data(200, map[string]any{"state": "failed", "original_status": 503, "result_metadata": map[string]any{"error": map[string]any{"code": "SERVICE_RECOVERING"}}})
			return
		}
		failure(404, "NOT_FOUND")
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/drafts/"):
		etag := `"draft-` + strings.Repeat("a", 64) + `"`
		if p.mode == "invalid-validator" {
			etag = "not-a-strong-draft-validator"
		}
		data(200, map[string]any{"draft_id": c2ReviewVersion, "project_id": c2ReviewVersion, "document_id": nil, "base_version_id": nil, "state": "ready", "revision": 1, "etag": etag})
	case r.Method == "POST" && r.URL.Path == "/api/v1/generations" || (r.Method == "PATCH" || r.Method == "POST") && strings.HasPrefix(r.URL.Path, "/api/v1/drafts/"):
		body, _ := io.ReadAll(r.Body)
		p.requests = append(p.requests, c2CommandRequest{key: r.Header.Get("Idempotency-Key"), method: r.Method, path: r.URL.Path, body: string(body), match: r.Header.Get("If-Match")})
		if p.mode == "lost" {
			conn, _, e := w.(http.Hijacker).Hijack()
			if e == nil {
				_ = conn.Close()
			}
			return
		}
		if p.mode == "decode" {
			_, _ = w.Write([]byte(`{"data":`))
			return
		}
		failure(p.status, p.code)
	default:
		failure(404, "NOT_FOUND")
	}
}
func r3CompositeCaller(t GinkgoTInterface, kind, mode string, status int, code string) (*r3CompositePeer, *clientstate.Store, transfer.Journal, []string) {
	GinkgoHelper()
	peer := &r3CompositePeer{mode: mode, status: status, code: code, runtime: "original-runtime"}
	server := httptest.NewServer(peer)
	DeferCleanup(server.Close)
	token, e := sec.Secret()
	Expect(e).NotTo(HaveOccurred())
	root, flags := callerProfile(t, service.Selection{Profile: "r3-composite"}, domain.Session{LibraryID: "r3-library"}, token, server.URL)
	state, e := clientstate.New(root)
	Expect(e).NotTo(HaveOccurred())
	identity := transfer.Identity{LibraryID: "r3-library", PrincipalID: "r3-owner", RuntimeGeneration: "original-runtime"}
	key, _ := service.NewID()
	target, method := "/api/v1/generations", "POST"
	source := filepath.Join(t.TempDir(), "request.json")
	body := json.RawMessage(`{"project_id":"` + c2ReviewVersion + `","document_id":null,"intent":"draft","provider_id":"codex","body_ref":{"object_id":"` + c2ReviewVersion + `","bytes_sha256":"` + strings.Repeat("a", 64) + `","byte_size":"1","media_type":"text/plain"},"source_selection":{"selections":[]}}`)
	args := []string{"generate", "--request-file", source}
	intent := clientstate.CommandIntent{Method: method, Target: target, SourcePath: source}
	j := transfer.Journal{OperationID: key, Purpose: "generation", Identity: identity, Method: method, Target: target, FinalBody: body, Generation: &transfer.GenerationLaunch{SourcePath: source, Launcher: identity, Request: body, DispatchReady: true}}
	if kind != "generation" {
		target = "/api/v1/drafts/" + c2ReviewVersion
		method = "PATCH"
		if kind == "publish" {
			target += "/publish"
			method = "POST"
			body = json.RawMessage(`{}`)
		} else {
			body = json.RawMessage(`{"action":"` + kind + `"}`)
		}
		source = ""
		intent = clientstate.CommandIntent{Method: method, Target: target}
		args = []string{"drafts", kind, c2ReviewVersion}
		j = transfer.Journal{OperationID: key, Purpose: "draft_" + kind, Identity: identity, Method: method, Target: target, FinalBody: body, Preconditions: map[string]string{"If-Match": `"draft-` + strings.Repeat("a", 64) + `"`}, Draft: &transfer.DraftLaunch{ID: c2ReviewVersion, Action: kind}}
		if kind == "replace" {
			source = filepath.Join(t.TempDir(), "original-whole")
			Expect(os.Mkdir(source, 0700)).To(Succeed())
			intent.SourcePath = source
			j.Draft.SourcePath = source
			args = append(args, "--input", source)
		}
	}
	if kind == "generation" {
		Expect(os.WriteFile(source, body, 0600)).To(Succeed())
	}
	actual, e := state.UseCommandPending(context.Background(), identity, intent, key)
	Expect(e).NotTo(HaveOccurred())
	Expect(actual).To(Equal(key))
	Expect(state.Save(j)).To(Succeed())
	return peer, state, j, append(args, flags...)
}
func r3ErrorOperation(t GinkgoTInterface, out []byte, status int) string {
	GinkgoHelper()
	id := failedOperation(t, out)
	var envelope struct {
		Data struct {
			Status *int `json:"original_status"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &envelope)).To(Succeed())
	if status < 0 {
		Expect(envelope.Data.Status).To(BeNil())
	} else {
		Expect(envelope.Data.Status).NotTo(BeNil())
		Expect(*envelope.Data.Status).To(Equal(status))
	}
	return id
}
func r3WireSnapshot(p *r3CompositePeer) ([]c2CommandRequest, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]c2CommandRequest(nil), p.requests...), append([]string(nil), p.lookups...)
}

var _ = It("T4R3CLI03 actual automatic launcher to workflow refusal releases only the launcher pending index after output", Label("persistence"), func() {
	t := GinkgoT()
	s := selection(t)
	initProfile(t, s)
	daemon := startGenerationCLIApplication(t, s, false, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" && r.URL.Path == "/api/v1/generations" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(422)
				_, _ = w.Write([]byte(`{"error":{"code":"INVALID_REQUEST"}}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	owner, token := bootstrapOwnerCLI(t, s, daemon.id.Endpoint, "refused generation owner")
	root, flags := callerProfile(t, s, owner, token, daemon.id.Endpoint)
	flags = append(flags, "--data-dir", s.DataDir)
	api, e := client.New(daemon.id.Endpoint, s.Profile, client.Credential{Endpoint: daemon.id.Endpoint, Profile: s.Profile, LibraryID: owner.LibraryID, Token: token})
	Expect(e).NotTo(HaveOccurred())
	identity, e := api.Identity(context.Background())
	Expect(e).NotTo(HaveOccurred())
	input := filepath.Join(t.TempDir(), "request.json")
	generationCLIInput(input, identity.UnfiledProjectID)
	args := append([]string{"generate", "--request-file", input}, flags...)
	Expect(run(context.Background(), args, c2RejectedOutput{}, io.Discard)).To(Equal(2))
	var pending []string
	for _, id := range pendingOperations(t, root) {
		if operationID.MatchString(id) {
			pending = append(pending, id)
		}
	}
	Expect(pending).To(HaveLen(1))
	key := pending[0]
	state, e := clientstate.New(root)
	Expect(e).NotTo(HaveOccurred())
	journal, e := state.Load(key)
	Expect(e).NotTo(HaveOccurred())
	Expect(journal.Identity.PrincipalID).NotTo(Equal(journal.Generation.Launcher.PrincipalID))
	Expect(r3ErrorOperation(t, cli(t, 2, args...), 422)).To(Equal(key))
	Expect(pendingOperations(t, root)).NotTo(ContainElement(key))
	retained, e := state.Load(key)
	Expect(e).NotTo(HaveOccurred())
	Expect(retained).To(Equal(journal))
	Expect(daemon.driver.calls.Load()).To(BeZero())
	cli(t, 0, append([]string{"service", "stop"}, profileArgs(s)...)...)
	daemon.wait(t)
})
