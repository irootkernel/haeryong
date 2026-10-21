package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	client "haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/configpath"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/adapters/gitreader"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	httpconfig "haeryong.local/haeryong/internal/adapters/http/configuration"
	httpimports "haeryong.local/haeryong/internal/adapters/http/imports"
	httpsource "haeryong.local/haeryong/internal/adapters/http/source"
	v1 "haeryong.local/haeryong/internal/adapters/http/v1"
	appauth "haeryong.local/haeryong/internal/application/auth"
	config "haeryong.local/haeryong/internal/application/configuration"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	appsource "haeryong.local/haeryong/internal/application/source"
	"haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// sourceCommandFixture mirrors the command-boundary fixture with the source
// adapter mounted, so the CLI drives the real registration and verification
// routes (D8) including large-expectation staging (D1) and journal recovery
// (D2).
type sourceCommandFixture struct {
	store   *foundation.Store
	auth    *appauth.Service
	source  *appsource.Service
	handler http.Handler
	base    string
	token   string
	session domain.Session
	sel     service.Selection
}

func newSourceCommandFixture(t GinkgoTInterface) sourceCommandFixture {
	t.Helper()
	ctx := context.Background()
	s := selection(t)
	initProfile(t, s)
	writer, e := service.OpenWriter(s)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { writer.Close() })
	Expect(writer.PrepareStorageBootstrap(nil)).NotTo(HaveOccurred())
	store := foundation.New(nil)
	_, closeStore, e := store.Prepare(ctx, writer.Profile)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { closeStore() })
	Expect(writer.MarkStorageReady(nil)).NotTo(HaveOccurred())
	auth := appauth.New(store)
	imports := importer.New(store, auth, store, staging.New())
	configuration := config.New(store, auth, configpath.Resolver{})
	sourceCore := appsource.New(store, auth, store, gitreader.New())
	// The seal worker runs for the fixture's lifetime so purpose=body
	// completions reach the sealed state the CLI awaits.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan error, 1)
	go func() { workerDone <- imports.Run(workerCtx) }()
	DeferCleanup(func() {
		stopWorker()
		if err := <-workerDone; err != nil && !errors.Is(err, context.Canceled) {
			_, _ = fmt.Fprintln(os.Stderr, "seal worker:", err)
		}
	})
	server := httptest.NewUnstartedServer(nil)
	base := "http://" + server.Listener.Addr().String()
	endpoint, _ := url.Parse(base)
	handler, e := httpauth.New(auth, httpauth.Policy{Hosts: []string{endpoint.Host}, Origins: []string{base}}, productRoutes{httpsource.New(sourceCore), httpconfig.New(configuration), v1.New(query.New(store, auth, store), nil, nil), httpimports.New(imports)})
	Expect(e).NotTo(HaveOccurred())
	server.Config.Handler = handler
	server.Start()
	DeferCleanup(server.Close)
	pair, e := auth.CreatePairing(ctx, base, "source command owner", "fixture")
	Expect(e).NotTo(HaveOccurred())
	id, _ := service.NewID()
	_, e = auth.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: id, Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	poll, e := auth.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(e).NotTo(HaveOccurred())
	session, e := auth.Authenticate(ctx, poll.Token)
	Expect(e).NotTo(HaveOccurred())
	return sourceCommandFixture{store: store, auth: auth, source: sourceCore, handler: handler, base: base, token: poll.Token, session: session, sel: s}
}

var _ = It("TestActualCLIProjectsRegisterReplayAndInvalid", Label("persistence"), func() {
	t := GinkgoT()
	f := newSourceCommandFixture(t)
	_, flags := callerProfile(t, f.sel, f.session, f.token, f.base)

	out := cli(t, 0, append([]string{"projects", "register", "--name", "CLI Project"}, flags...)...)
	var registered struct {
		Data struct {
			Result struct {
				ProjectID string `json:"project_id"`
				ETag      string `json:"etag"`
			} `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &registered)).NotTo(HaveOccurred(), string(out))
	Expect(registered.Data.Result.ProjectID).NotTo(BeEmpty())
	Expect(registered.Data.Result.ETag).NotTo(BeEmpty())

	// Invalid names fail closed without touching the registry.
	cli(t, 2, append([]string{"projects", "register", "--name", " "}, flags...)...)

	// A second, distinct registration creates another project.
	out = cli(t, 0, append([]string{"projects", "register", "--name", "Second"}, flags...)...)
	var second struct {
		Data struct {
			Result struct {
				ProjectID string `json:"project_id"`
			} `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &second)).NotTo(HaveOccurred(), string(out))
	Expect(second.Data.Result.ProjectID).NotTo(Equal(registered.Data.Result.ProjectID))
})

var _ = It("TestActualCLIEvidenceVerifyLargeExpectationStagesAndRecovers", Label("persistence"), func() {
	t := GinkgoT()
	if _, err := exec.LookPath("git"); err != nil {
		Skip("git binary unavailable")
	}
	f := newSourceCommandFixture(t)
	ctx := context.Background()

	repo := t.TempDir()
	gitRun := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		var out strings.Builder
		cmd.Stdout = &out
		cmd.Stderr = &out
		Expect(cmd.Run()).To(Succeed(), "git %v: %s", args, out.String())
		return strings.TrimSpace(out.String())
	}
	gitRun("init", "--initial-branch=main")
	blob := "package main\n\nfunc main() {}\n"
	Expect(os.MkdirAll(filepath.Join(repo, "pkg"), 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(repo, "pkg", "main.go"), []byte(blob), 0o644)).To(Succeed())
	gitRun("add", "-A")
	gitRun("commit", "-m", "fixture")
	revision := gitRun("rev-parse", "HEAD")

	root, flags := callerProfile(t, f.sel, f.session, f.token, f.base)
	out := cli(t, 0, append([]string{"projects", "register", "--name", "Repo Project"}, flags...)...)
	var registered struct {
		Data struct {
			Result struct {
				ProjectID string `json:"project_id"`
			} `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &registered)).NotTo(HaveOccurred(), string(out))
	project := registered.Data.Result.ProjectID
	repoID := "80000000-0000-4000-8000-000000000001"
	Expect(f.store.Write(ctx, func(tx storage.WriteTx) error {
		current, e := tx.Configuration()
		if e != nil {
			return e
		}
		var config map[string]any
		Expect(json.Unmarshal(current.Value, &config)).NotTo(HaveOccurred())
		config["projects"].(map[string]any)[project] = map[string]any{"sources": []any{map[string]any{"source_id": "git-1", "kind": "git", "root_path": repo, "repo_id": repoID, "retention_mode": "reference_only", "provider_ids": []string{}}}}
		raw, e := json.Marshal(config)
		if e != nil {
			return e
		}
		return tx.SetConfiguration(domain.Configuration{Revision: current.Revision, Value: raw, Actor: "fixture"}, current.Revision)
	})).To(Succeed())

	version := "81000000-0000-4000-8000-000000000001"
	document := "82000000-0000-4000-8000-000000000002"
	set := "83000000-0000-4000-8000-000000000003"
	zero := "0000000000000000000000000000000000000000000000000000000000000000"
	Expect(f.store.Write(ctx, func(tx storage.WriteTx) error {
		if e := tx.CreateDocument(domain.Document{ID: document, ProjectID: project, Title: "Doc"}); e != nil {
			return e
		}
		if e := tx.CreateContentSet(set, zero); e != nil {
			return e
		}
		if e := tx.SealContentSet(set); e != nil {
			return e
		}
		if e := tx.PutVersion(domain.Version{ID: version, DocumentID: document, ContentSetID: set, Title: "Doc", ViewerProfile: "html_js_v1", SourceBasis: "evidence_recorded", PayloadSHA: zero, ManifestSHA: zero}); e != nil {
			return e
		}
		sourceMeta := fmt.Sprintf(`{"id":"src","kind":"git","retention_mode":"reference_only","repo_id":%q,"object_format":"sha1","exact_revision":"%s"}`, repoID, revision)
		if e := tx.PutSource(domain.Source{VersionID: version, ID: "src", Metadata: json.RawMessage(sourceMeta)}); e != nil {
			return e
		}
		if e := tx.PutEvidence(domain.Evidence{VersionID: version, ID: "ev", SourceID: "src", Metadata: json.RawMessage(`{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go","line_start":3,"line_end":3}`)}); e != nil {
			return e
		}
		d, e := tx.QueryDocument(document)
		if e != nil {
			return e
		}
		return tx.PublishVersion(version, d.Revision)
	})).To(Succeed())

	// A large expectation (>64KiB) must stage through purpose=body and seal
	// (D1: completion at generation+1), then verify against the pinned line.
	expectation := "func main() {}\n" + strings.Repeat("// trailing context line padding the expectation past the inline bound\n", 1300)
	Expect(len(expectation)).To(BeNumerically(">", 65536))
	file := filepath.Join(t.TempDir(), "expectation.txt")
	Expect(os.WriteFile(file, []byte(expectation), 0o600)).To(Succeed())
	key := "84000000-0000-4000-8000-000000000004"
	out = cli(t, 0, append([]string{"evidence", "verify", "--version", version, "--evidence", "ev", "--expected-excerpt-file", file, "--operation-id", key}, flags...)...)
	var verified struct {
		Data struct {
			Result struct {
				Outcome string `json:"outcome"`
				Reason  string `json:"reason"`
			} `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &verified)).NotTo(HaveOccurred(), string(out))
	Expect(verified.Data.Result.Outcome).To(Equal("mismatch"), "the padded expectation cannot equal the three-line bounded excerpt: "+verified.Data.Result.Reason)

	// D2: the journal retained the staging identities for the consumer key.
	journalBytes, e := os.ReadFile(filepath.Join(root, "journals", key+".json"))
	if e != nil {
		journalBytes, e = os.ReadFile(filepath.Join(root, key+".json"))
	}
	if e != nil {
		candidates, _ := filepath.Glob(filepath.Join(root, "**", key+".json"))
		Expect(candidates).NotTo(BeEmpty(), "no journal found for the staging consumer key under "+root)
		journalBytes, _ = os.ReadFile(candidates[0])
	}
	var journal struct {
		Result struct {
			UploadID    string `json:"upload_id"`
			CreateKey   string `json:"create_operation_id"`
			CompleteKey string `json:"complete_operation_id"`
			Checkpoint  struct {
				Complete bool `json:"complete_input"`
			} `json:"checkpoint"`
		} `json:"result"`
	}
	Expect(json.Unmarshal(journalBytes, &journal)).NotTo(HaveOccurred(), string(journalBytes))
	Expect(journal.Result.UploadID).NotTo(BeEmpty())
	Expect(journal.Result.CreateKey).NotTo(Equal(key))
	Expect(journal.Result.CompleteKey).NotTo(Equal(key))
	Expect(journal.Result.Checkpoint.Complete).To(BeTrue())

	// A rerun with the same consumer key and expectation replays the same
	// terminal check without creating a new upload (D2).
	uploadBefore := journal.Result.UploadID
	rerun := cli(t, 0, append([]string{"evidence", "verify", "--version", version, "--evidence", "ev", "--expected-excerpt-file", file, "--operation-id", key}, flags...)...)
	var replayed struct {
		Data struct {
			Result struct {
				Outcome string `json:"outcome"`
			} `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(rerun, &replayed)).NotTo(HaveOccurred(), string(rerun))
	Expect(replayed.Data.Result.Outcome).To(Equal("mismatch"))
	var uploads int
	Expect(f.store.Write(ctx, func(tx storage.WriteTx) error {
		rows, e := tx.Uploads("", "", false, 100)
		if e != nil {
			return e
		}
		uploads = len(rows)
		return nil
	})).To(Succeed())
	Expect(uploads).To(Equal(1), "the rerun reused the staged upload "+uploadBefore)
})

// interruptingProxy forwards to the fixture handler but severs one chosen
// request connection before a response, leaving the CLI journal mid-flight.
type interruptingProxy struct {
	inner    http.Handler
	host     string
	killPath string
	method   string
	// remaining severs every matching request while positive: the HTTP
	// transport legitimately retries a written-but-unanswered request on a
	// new connection, so one severance is not enough to stop the caller.
	remaining int
}

// rewrite makes the proxied request carry the fixture's host identity.
func (p *interruptingProxy) rewrite(r *http.Request) *http.Request {
	clone := r.Clone(r.Context())
	clone.Host = p.host
	clone.URL.Host = p.host
	clone.RequestURI = ""
	return clone
}

func (p *interruptingProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == p.method && strings.Contains(r.URL.Path, p.killPath) && p.remaining > 0 {
		p.remaining--
		if hijacker, ok := w.(http.Hijacker); ok {
			conn, _, err := hijacker.Hijack()
			if err == nil {
				_ = conn.Close()
				return
			}
		}
		panic(http.ErrAbortHandler)
	}
	// The inner adapter validates exact Host/Origin: present the fixture's
	// identity so the proxy is transparent to policy.
	p.inner.ServeHTTP(w, p.rewrite(r))
}

// expectationFixture bundles the fixture state shared by the interrupt cases.
type expectationFixture struct {
	f             sourceCommandFixture
	scopedProject string
	scopedVersion string
	flags         []string
	repo          string
	version       string
	project       string
	filePath      string
	key           string
	sharedRoot    string
}

func newExpectationFixture(t GinkgoTInterface) *expectationFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		Skip("git binary unavailable")
	}
	f := newSourceCommandFixture(t)
	ctx := context.Background()
	repo := t.TempDir()
	gitRun := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		var out strings.Builder
		cmd.Stdout = &out
		cmd.Stderr = &out
		Expect(cmd.Run()).To(Succeed(), "git %v: %s", args, out.String())
		return strings.TrimSpace(out.String())
	}
	gitRun("init", "--initial-branch=main")
	blob := "package main\n\nfunc main() {}\n"
	Expect(os.MkdirAll(filepath.Join(repo, "pkg"), 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(repo, "pkg", "main.go"), []byte(blob), 0o644)).To(Succeed())
	gitRun("add", "-A")
	gitRun("commit", "-m", "fixture")
	revision := gitRun("rev-parse", "HEAD")
	_, flags := callerProfile(t, f.sel, f.session, f.token, f.base)
	out := cli(t, 0, append([]string{"projects", "register", "--name", "Interrupt Project"}, flags...)...)
	var registered struct {
		Data struct {
			Result struct {
				ProjectID string `json:"project_id"`
			} `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &registered)).NotTo(HaveOccurred(), string(out))
	project := registered.Data.Result.ProjectID
	repoID := "86000000-0000-4000-8000-000000000001"
	Expect(f.store.Write(ctx, func(tx storage.WriteTx) error {
		current, e := tx.Configuration()
		if e != nil {
			return e
		}
		var config map[string]any
		Expect(json.Unmarshal(current.Value, &config)).NotTo(HaveOccurred())
		config["projects"].(map[string]any)[project] = map[string]any{"sources": []any{map[string]any{"source_id": "git-1", "kind": "git", "root_path": repo, "repo_id": repoID, "retention_mode": "reference_only", "provider_ids": []string{}}}}
		raw, e := json.Marshal(config)
		if e != nil {
			return e
		}
		return tx.SetConfiguration(domain.Configuration{Revision: current.Revision, Value: raw, Actor: "fixture"}, current.Revision)
	})).To(Succeed())
	version := "87000000-0000-4000-8000-000000000001"
	document := "88000000-0000-4000-8000-000000000002"
	set := "89000000-0000-4000-8000-000000000003"
	zero := "0000000000000000000000000000000000000000000000000000000000000000"
	Expect(f.store.Write(ctx, func(tx storage.WriteTx) error {
		if e := tx.CreateDocument(domain.Document{ID: document, ProjectID: project, Title: "Doc"}); e != nil {
			return e
		}
		if e := tx.CreateContentSet(set, zero); e != nil {
			return e
		}
		if e := tx.SealContentSet(set); e != nil {
			return e
		}
		if e := tx.PutVersion(domain.Version{ID: version, DocumentID: document, ContentSetID: set, Title: "Doc", ViewerProfile: "html_js_v1", SourceBasis: "evidence_recorded", PayloadSHA: zero, ManifestSHA: zero}); e != nil {
			return e
		}
		sourceMeta := fmt.Sprintf(`{"id":"src","kind":"git","retention_mode":"reference_only","repo_id":%q,"object_format":"sha1","exact_revision":"%s"}`, repoID, revision)
		if e := tx.PutSource(domain.Source{VersionID: version, ID: "src", Metadata: json.RawMessage(sourceMeta)}); e != nil {
			return e
		}
		if e := tx.PutEvidence(domain.Evidence{VersionID: version, ID: "ev", SourceID: "src", Metadata: json.RawMessage(`{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go","line_start":3,"line_end":3}`)}); e != nil {
			return e
		}
		d, e := tx.QueryDocument(document)
		if e != nil {
			return e
		}
		return tx.PublishVersion(version, d.Revision)
	})).To(Succeed())
	expectation := "func main() {}\n" + strings.Repeat("// trailing context line padding the expectation past the inline bound\n", 1300)
	file := filepath.Join(t.TempDir(), "expectation.txt")
	Expect(os.WriteFile(file, []byte(expectation), 0o600)).To(Succeed())
	return &expectationFixture{f: f, scopedProject: project, scopedVersion: version, flags: flags, repo: repo, version: version, project: project, filePath: file, key: "8a000000-0000-4000-8000-000000000001"}
}

// runAgainst proxies the CLI through an interrupting server.
// callerRoot prepares one shared caller-profile directory; the interrupt run
// and the rerun share its journals while pointing at different endpoints.
func (e *expectationFixture) callerRoot(t GinkgoTInterface, base string) (root, credential string, flags []string) {
	t.Helper()
	if e.sharedRoot == "" {
		e.sharedRoot = filepath.Join(t.TempDir(), "caller")
		Expect(os.MkdirAll(e.sharedRoot, 0o700)).To(Succeed())
	}
	root = e.sharedRoot
	credential = filepath.Join(root, "credential-"+strings.Map(func(r rune) rune {
		if r == ':' || r == '/' {
			return '-'
		}
		return r
	}, base)+".json")
	raw, merr := json.Marshal(client.Credential{Endpoint: base, Profile: e.f.sel.Profile, LibraryID: e.f.session.LibraryID, Token: e.f.token})
	Expect(merr).NotTo(HaveOccurred())
	Expect(os.WriteFile(credential, raw, 0o600)).To(Succeed())
	return root, credential, []string{"--profile", e.f.sel.Profile, "--client-dir", root, "--credential", credential, "--json"}
}

func (e *expectationFixture) runAgainst(t GinkgoTInterface, proxy http.Handler, base string, expectExit int) []byte {
	t.Helper()
	_, _, flags := e.callerRoot(t, base)
	return cli(t, expectExit, append([]string{"evidence", "verify", "--version", e.version, "--evidence", "ev", "--expected-excerpt-file", e.filePath, "--operation-id", e.key}, flags...)...)
}

func (e *expectationFixture) counts(t GinkgoTInterface) (uploads, verifications int) {
	t.Helper()
	Expect(e.f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		rows, err := tx.Uploads("", "", false, 100)
		if err != nil {
			return err
		}
		uploads = len(rows)
		history, err := tx.Verifications(e.version, "ev", "", "", 100)
		if err != nil {
			return err
		}
		verifications = len(history)
		return nil
	})).To(Succeed())
	return uploads, verifications
}

var _ = It("TestActualCLIExpectationInterruptedAfterCreateResumesSameUpload", Label("persistence"), func() {
	t := GinkgoT()
	e := newExpectationFixture(t)
	// The caller profile journal is shared across both invocations.
	shared := t.TempDir()
	_ = shared
	fixtureURL, parseErr := url.Parse(e.f.base)
	Expect(parseErr).NotTo(HaveOccurred())
	proxy := &interruptingProxy{inner: e.f.handler, host: fixtureURL.Host, killPath: "/uploads/", method: http.MethodPut, remaining: 3}
	front := httptest.NewServer(proxy)
	DeferCleanup(front.Close)
	// First run: the chunk request is severed after the journaled create.
	e.runAgainst(t, proxy, front.URL, 5)
	uploads, verifications := e.counts(t)
	Expect(uploads).To(Equal(1), "the interrupted run created exactly one upload")
	Expect(verifications).To(Equal(0))
	// Rerun against the real fixture through the SAME caller profile: the
	// same upload resumes from its checkpoint, seals with the journaled seal
	// key and sends the final verification with the original consumer key.
	_, _, rerunFlags := e.callerRoot(t, e.f.base)
	out := cli(t, 0, append([]string{"evidence", "verify", "--version", e.version, "--evidence", "ev", "--expected-excerpt-file", e.filePath, "--operation-id", e.key}, rerunFlags...)...)
	var verified struct {
		Data struct {
			Result struct {
				Outcome string `json:"outcome"`
			} `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &verified)).NotTo(HaveOccurred(), string(out))
	Expect(verified.Data.Result.Outcome).To(Equal("mismatch"))
	uploads, verifications = e.counts(t)
	Expect(uploads).To(Equal(1), "the rerun reused the staged upload")
	Expect(verifications).To(Equal(1), "exactly one verification exists")
})

var _ = It("TestActualCLIExpectationInterruptedBeforeFinalPostReplaysConsumerKey", Label("persistence"), func() {
	t := GinkgoT()
	e := newExpectationFixture(t)
	fixtureURL, parseErr := url.Parse(e.f.base)
	Expect(parseErr).NotTo(HaveOccurred())
	proxy := &interruptingProxy{inner: e.f.handler, host: fixtureURL.Host, killPath: "/source-evidence-verifications", method: http.MethodPost, remaining: 3}
	front := httptest.NewServer(proxy)
	DeferCleanup(front.Close)
	// First run: the seal completes; the final verification POST is severed.
	e.runAgainst(t, proxy, front.URL, 5)
	uploads, verifications := e.counts(t)
	Expect(uploads).To(Equal(1), "the staging completed before the interruption")
	Expect(verifications).To(Equal(0), "no verification was recorded")
	// Rerun through the SAME caller profile: the sealed upload is reused; the
	// final POST replays the original consumer key without a new upload or
	// seal.
	_, _, rerunFlags := e.callerRoot(t, e.f.base)
	out := cli(t, 0, append([]string{"evidence", "verify", "--version", e.version, "--evidence", "ev", "--expected-excerpt-file", e.filePath, "--operation-id", e.key}, rerunFlags...)...)
	var verified struct {
		Data struct {
			Result struct {
				Outcome string `json:"outcome"`
			} `json:"result"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(out, &verified)).NotTo(HaveOccurred(), string(out))
	Expect(verified.Data.Result.Outcome).To(Equal("mismatch"))
	uploads, verifications = e.counts(t)
	Expect(uploads).To(Equal(1), "no new upload appeared")
	Expect(verifications).To(Equal(1), "exactly one verification exists")
})

// ReadConfigurationForTest decodes the effective configuration for specs.
func (f *sourceCommandFixture) ReadConfigurationForTest(t GinkgoTInterface, into any) error {
	t.Helper()
	return readSourceConfiguration(f.store, into)
}

// readSourceConfiguration decodes the effective configuration through the
// store's exported read.
func readSourceConfiguration(store *foundation.Store, into any) error {
	configuration, e := store.Configuration(context.Background())
	if e != nil {
		return e
	}
	raw, _ := json.Marshal(map[string]any{"configuration": json.RawMessage(configuration.Value)})
	return json.Unmarshal(raw, into)
}

// seedScopedSource issues a scoped credential with exactly version.read and
// source.read anchored to the expectation fixture's version and project.
func (f *sourceCommandFixture) seedScopedSourceWith(t GinkgoTInterface, project, version string) string {
	t.Helper()
	token, e := sec.Secret()
	ExpectWithOffset(1, e).NotTo(HaveOccurred())
	principal := "94000000-0000-4000-8000-000000000040"
	scope := domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read", "source.read"}, ProjectIDs: []string{project}, VersionIDs: []string{version}}
	ExpectWithOffset(1, f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		state, e := tx.AuthState()
		if e != nil {
			return e
		}
		if e := tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: principal, Active: true, Scope: scope, Revision: 1}); e != nil {
			return e
		}
		p, e := tx.Scope(project)
		if e != nil {
			return e
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: principal, PrincipalID: principal, TokenHash: sec.Verifier("workflow", token), AuthEpoch: state.AuthEpoch, Scope: scope, Permissions: map[string]int64{project: p.Project.PermissionsRevision}, ConfigRevision: p.ConfigRevision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond), Revision: 1})
	})).To(Succeed())
	return token
}
