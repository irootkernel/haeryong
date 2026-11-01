package importer_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	httprenderer "haeryong.local/haeryong/internal/adapters/http/renderer"
	query "haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/render"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	importer "haeryong.local/haeryong/internal/application/import"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

type generationDriver struct {
	data         []byte
	calls        atomic.Int32
	faultJob     string
	unknownDrain bool
	cleanup      error
	late         error
}

func (d *generationDriver) Execute(_ context.Context, a domain.ProviderAttempt, _ []domain.ProviderInput, send func() error, ack func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	d.calls.Add(1)
	if e := send(); e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	if e := ack(work.ProviderProgress{RequestID: "controlled-turn", SessionID: "controlled-thread"}); e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	closed, cleanup, late := true, error(nil), error(nil)
	if a.JobID == d.faultJob {
		closed, cleanup, late = !d.unknownDrain, d.cleanup, d.late
	}
	return work.ProviderOutcome{State: "result_saved", CompletionObserved: true, LocalClosed: closed, CleanupError: cleanup, RequestID: "controlled-turn", SessionID: "controlled-thread", Body: io.NopCloser(bytes.NewReader(d.data))}, late
}
func (d *generationDriver) Cancel(context.Context, domain.ProviderAttempt) error {
	return domain.ErrReconciliation
}
func (d *generationDriver) Reconcile(context.Context, domain.ProviderAttempt, bool) (work.ProviderOutcome, error) {
	Fail("saved continuation must not query provider")
	return work.ProviderOutcome{}, domain.ErrReconciliation
}
func generationArchive() []byte {
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, p := range []string{"read.html", "nested/next.html", "assets/app.js", "empty/"} {
		w, e := z.Create(p)
		Expect(e).NotTo(HaveOccurred())
		if !strings.HasSuffix(p, "/") {
			_, e = w.Write([]byte("<html>" + p + "</html>"))
			Expect(e).NotTo(HaveOccurred())
		}
	}
	Expect(z.Close()).To(Succeed())
	return out.Bytes()
}
func generationJob(f *fixture, intent string, doc, base *string) domain.GenerationIntent {
	t := GinkgoT()
	request := sha256.Sum256([]byte("independent requested explanation " + key(t)))
	g := domain.GenerationIntent{JobID: key(t), ProjectID: f.project, AllocatedDocumentID: key(t), VersionID: key(t), DraftID: key(t), Intent: intent, RequestHash: hex.EncodeToString(request[:]), Title: "complete generated explanation", CreatedAt: "2026-10-30T12:00:00Z", DocumentID: doc, BaseVersionID: base}
	actor, e := f.auth.Authenticate(ctx, f.token)
	Expect(e).NotTo(HaveOccurred())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		p, e := tx.Scope(f.project)
		if e != nil {
			return e
		}
		if doc != nil {
			d, e := tx.Document(*doc)
			if e != nil {
				return e
			}
			g.AllocatedDocumentID = d.ID
			g.DocumentRevision = d.Revision
		}
		target, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": doc})
		scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: actor.PrincipalID, AuthEpoch: actor.AuthEpoch, CredentialScope: actor.Scope, Target: target, RequestSHA: g.RequestHash})
		if e = tx.CreateJob(domain.Job{ID: g.JobID, Kind: "generation", Scope: scope, ConfigRevision: cfg.Revision, Permissions: map[string]int64{f.project: p.Project.PermissionsRevision}}); e != nil {
			return e
		}
		if e = tx.CreateDraft(domain.Draft{ID: g.DraftID, ProjectID: g.ProjectID, DocumentID: doc, BaseVersionID: base, AllocatedDocumentID: g.AllocatedDocumentID, ProposedVersionID: g.VersionID, JobID: g.JobID, State: "preparing", Intent: intent, CreatedAt: g.CreatedAt}); e != nil {
			return e
		}
		return tx.CreateGenerationIntent(g)
	})).To(Succeed())
	return g
}

var _ = It("GGEN05 complete saved directory is prepared outside the writer and atomically published without requiring an index page", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	g := generationJob(f, "publish", nil, nil)
	driver := &generationDriver{data: generationArchive()}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: f.core.PrepareGeneration, Apply: f.core.ApplyGeneration}
	Expect(p.Process(ctx, g.JobID)).To(Succeed())
	Expect(driver.calls.Load()).To(Equal(int32(1)))
	j, e := f.store.Job(ctx, g.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"), j.Error)
	v, e := f.store.Version(ctx, g.VersionID)
	Expect(e).NotTo(HaveOccurred())
	Expect(v.DocumentID).To(Equal(g.AllocatedDocumentID))
	Expect(v.DefaultPage).NotTo(BeNil())
	Expect(*v.DefaultPage).To(Equal("nested/next.html"))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		d, e := tx.Draft(g.DraftID)
		if e != nil {
			return e
		}
		Expect(d.State).To(Equal("published"))
		Expect(d.PublishedVersionID).To(Equal(v.ID))
		rows, e := tx.Entries(v.ContentSetID, "", 64)
		if e != nil {
			return e
		}
		Expect(rows).To(HaveLen(6))
		_, e = tx.Page(v.ID, "read.html")
		return e
	})).To(Succeed())
	var result map[string]any
	Expect(json.Unmarshal(j.Result, &result)).To(Succeed())
	Expect(result["version_id"]).To(Equal(v.ID))
})
var _ = It("GGEN06 stale publication keeps a complete ready draft and exact saved continuation performs no Execute", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	u := f.sealed(GinkgoT(), nil, "<html>old base</html>")
	doc, base := f.imported(GinkgoT(), u, key(GinkgoT()))
	g := generationJob(f, "publish", &doc, &base)
	driver := &generationDriver{data: generationArchive()}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: f.core.PrepareGeneration, Apply: f.core.ApplyGeneration}
	// A deliberately newer independent publication, not an overwritten captured base.
	upload := f.sealed(GinkgoT(), &doc, "<html>new current</html>")
	r, e := f.core.Import(ctx, f.token, key(GinkgoT()), importer.ImportRequest{UploadID: upload, DocumentID: &doc, DocumentPresent: true, BaseVersionID: &base, BasePresent: true})
	Expect(e).NotTo(HaveOccurred())
	Expect(f.core.Process(ctx, decoded(GinkgoT(), r)["job_id"])).To(Succeed())
	Expect(p.Process(ctx, g.JobID)).To(Succeed())
	Expect(driver.calls.Load()).To(Equal(int32(1)))
	j, e := f.store.Job(ctx, g.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(j.Error).To(Equal("VERSION_CONFLICT"))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		d, e := tx.Draft(g.DraftID)
		if e != nil {
			return e
		}
		Expect(d.State).To(Equal("ready"))
		Expect(*d.BaseVersionID).To(Equal(base))
		Expect(d.ContentSetID).NotTo(BeEmpty())
		history, e := tx.DraftHistory(d.ID, 0, 64)
		if e != nil {
			return e
		}
		Expect(history).To(HaveLen(2))
		_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "continue")
		return e
	})).To(Succeed())
	p.Driver = nil
	Expect(p.Process(ctx, g.JobID)).To(Succeed())
	Expect(driver.calls.Load()).To(Equal(int32(1)))
	j, e = f.store.Job(ctx, g.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(j.Error).To(Equal("VERSION_CONFLICT"))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		d, e := tx.Draft(g.DraftID)
		if e != nil {
			return e
		}
		Expect(d.State).To(Equal("ready"))
		h, e := tx.DraftHistory(d.ID, 0, 64)
		Expect(h).To(HaveLen(2))
		return e
	})).To(Succeed())
})

var _ = It("GGEN07 phase collection retains preparation and independent cleanup diagnostics while Reader Review and subsequent work remain available", Label("persistence"), func() {
	for _, mode := range []string{"safe", "unknown-drain", "cleanup-failure", "late-protocol"} {
		f := newFixture(GinkgoT())
		baselineUpload := f.sealed(GinkgoT(), nil, "<html>Reader remains independent</html>")
		_, version := f.imported(GinkgoT(), baselineUpload, key(GinkgoT()))
		first := generationJob(f, "draft", nil, nil)
		next := generationJob(f, "draft", nil, nil)
		preparation := errors.New("controlled generation preparation failure")
		independent := errors.New("independent native diagnostic")
		d := &generationDriver{data: generationArchive(), faultJob: first.JobID, unknownDrain: mode == "unknown-drain"}
		if mode == "cleanup-failure" {
			d.cleanup = independent
		}
		if mode == "late-protocol" {
			d.late = independent
		}
		p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d, Apply: f.core.ApplyGeneration, Prepare: func(c context.Context, a domain.ProviderAttempt) error {
			if a.JobID == first.JobID {
				return preparation
			}
			return f.core.PrepareGeneration(c, a)
		}}
		Expect(p.ScheduleProvider(ctx, first.JobID)).To(Succeed())
		Eventually(func() string {
			j, e := f.store.Job(ctx, first.JobID)
			if e != nil {
				return e.Error()
			}
			return j.State
		}, 5*time.Second).Should(Equal("paused"))
		// The second Execute can start only after Poll collected the first phase.
		Eventually(func() int32 {
			Expect(p.PollProvider(ctx)).To(Succeed())
			Expect(p.ScheduleProvider(ctx, next.JobID)).To(Succeed())
			return d.calls.Load()
		}, 5*time.Second).Should(Equal(int32(2)))
		Eventually(func() string {
			j, e := f.store.Job(ctx, next.JobID)
			if e != nil {
				return e.Error()
			}
			return j.State
		}, 5*time.Second).Should(Equal("completed"))
		q := query.New(f.store, f.auth, f.store)
		Expect(q.ReadFile(ctx, f.token, version, "hello.html", false, func(_ query.ResolvedFile, r io.ReadSeeker) error {
			b, e := io.ReadAll(r)
			Expect(string(b)).To(ContainSubstring("Reader remains independent"))
			return e
		})).To(Succeed())
		review, e := q.ReviewGet(ctx, f.token, version)
		Expect(e).NotTo(HaveOccurred())
		Expect(review.Value.State).To(Equal("unreviewed"))
		stop := p.StopProviders()
		Expect(errors.Is(stop, preparation)).To(BeTrue(), mode)
		Expect(work.ProviderRestartDiagnostics(stop)).To(Equal(mode == "safe" || mode == "late-protocol"), mode)
		if mode == "cleanup-failure" || mode == "late-protocol" {
			Expect(errors.Is(stop, independent)).To(BeTrue())
		}
		Expect(errors.Is(p.StopProviders(), preparation)).To(BeTrue(), "first diagnostic survives final repeated Stop")
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.ProviderAttempts(first.JobID)
			if e != nil {
				return e
			}
			Expect(rows).To(HaveLen(1))
			Expect(rows[0].State).To(Equal("result_saved"))
			Expect(rows[0].ResultHash).NotTo(BeEmpty())
			return nil
		})).To(Succeed())
	}
})

var _ = It("GGEN08 phase Stop cancels new heavy preparation while the complete saved directory can continue without Execute", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	g := generationJob(f, "draft", nil, nil)
	entered := make(chan struct{})
	d := &generationDriver{data: generationArchive()}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d, Apply: f.core.ApplyGeneration, Prepare: func(c context.Context, _ domain.ProviderAttempt) error { close(entered); <-c.Done(); return c.Err() }}
	Expect(p.ScheduleProvider(ctx, g.JobID)).To(Succeed())
	Eventually(entered, 5*time.Second).Should(BeClosed())
	stopped := make(chan error, 1)
	go func() { stopped <- p.StopProviders() }()
	var stop error
	Eventually(stopped, 5*time.Second).Should(Receive(&stop))
	Expect(errors.Is(stop, context.Canceled)).To(BeTrue())
	j, e := f.store.Job(ctx, g.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(j.CancelRequested).To(BeFalse())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "continue")
		return e
	})).To(Succeed())
	p.Prepare = f.core.PrepareGeneration
	p.Driver = nil
	Expect(p.Process(ctx, g.JobID)).To(Succeed())
	Expect(d.calls.Load()).To(Equal(int32(1)))
	j, e = f.store.Job(ctx, g.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		draft, e := tx.Draft(g.DraftID)
		if e != nil {
			return e
		}
		Expect(draft.State).To(Equal("ready"))
		return nil
	})).To(Succeed())
})

var _ = It("GGEN09 explicit owner cancellation during preparation stays terminal and cannot apply its retained complete result", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	g := generationJob(f, "draft", nil, nil)
	entered := make(chan struct{})
	d := &generationDriver{data: generationArchive()}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d, Apply: f.core.ApplyGeneration, Prepare: func(c context.Context, _ domain.ProviderAttempt) error { close(entered); <-c.Done(); return c.Err() }}
	Expect(p.ScheduleProvider(ctx, g.JobID)).To(Succeed())
	Eventually(entered, 5*time.Second).Should(BeClosed())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { _, e := tx.CancelJob(g.JobID); return e })).To(Succeed())
	stopped := make(chan error, 1)
	go func() { stopped <- p.StopProviders() }()
	var stop error
	Eventually(stopped, 5*time.Second).Should(Receive(&stop))
	Expect(errors.Is(stop, context.Canceled)).To(BeTrue())
	j, e := f.store.Job(ctx, g.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("cancelled"))
	Expect(j.CancelRequested).To(BeTrue())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(j.ID)
		if e != nil {
			return e
		}
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].State).To(Equal("result_saved"))
		draft, e := tx.Draft(g.DraftID)
		if e != nil {
			return e
		}
		Expect(draft.State).To(Equal("preparing"))
		Expect(draft.ContentSetID).To(BeEmpty())
		_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "continue")
		Expect(e).To(HaveOccurred())
		return nil
	})).To(Succeed())
})

// This exercises the actual origins ledger and renderer sockets, not ReadFile
// alone: publication can succeed with an identity the renderer must refuse.
var _ = It("GGEN21 generated publication and saved continuation retain a UUID whole set through draft history and real renderer delivery", Label("persistence"), func() {
	for _, resumed := range []bool{false, true} {
		func() {
			f := newFixture(GinkgoT())
			// Use the existing renderer HTTP fixture pattern: a test-local single-port
			// pool selected by an actual loopback :0 probe, never the owner's 9281 pool.
			// Close-to-bind races remain real bind failures; no fallback or retry hides them.
			probe, e := net.Listen("tcp", "127.0.0.1:0")
			Expect(e).NotTo(HaveOccurred())
			port := probe.Addr().(*net.TCPAddr).Port
			Expect(probe.Close()).To(Succeed())
			Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
				configuration, e := tx.Configuration()
				if e != nil {
					return e
				}
				var value map[string]any
				if e = json.Unmarshal(configuration.Value, &value); e != nil {
					return e
				}
				value["network"].(map[string]any)["renderer_pool"] = map[string]any{"base_port": port, "count": 1}
				configuration.Value, e = json.Marshal(value)
				if e != nil {
					return e
				}
				return tx.SetConfiguration(configuration, configuration.Revision)
			})).To(Succeed())
			g := generationJob(f, "publish", nil, nil)
			driver := &generationDriver{data: generationArchive()}
			p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: f.core.PrepareGeneration, Apply: f.core.ApplyGeneration}
			if resumed {
				preparation := errors.New("controlled pre-import interruption after complete saved output")
				p.Prepare = func(context.Context, domain.ProviderAttempt) error { return preparation }
				Expect(errors.Is(p.Process(ctx, g.JobID), preparation)).To(BeTrue())
				job, e := f.store.Job(ctx, g.JobID)
				Expect(e).NotTo(HaveOccurred())
				Expect(job.State).To(Equal("paused"))
				f.reopen(GinkgoT())
				Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
					_, e := tx.ResumeProviderJob(g.JobID, job.AttemptGeneration, "continue")
					return e
				})).To(Succeed())
				p.Driver = nil
				p.Prepare = f.core.PrepareGeneration
			}
			Expect(p.Process(ctx, g.JobID)).To(Succeed())
			Expect(driver.calls.Load()).To(Equal(int32(1)))
			version, e := f.store.Version(ctx, g.VersionID)
			Expect(e).NotTo(HaveOccurred())
			var renderer *render.Service
			renderer, e = render.New(f.store, f.auth, f.store, render.Options{AppStateDir: filepath.Join(GinkgoT().TempDir(), "renderer-origins"), NewServer: func(origin string, binding render.Binding) render.Server {
				return httprenderer.New(renderer, origin, binding)
			}})
			Expect(e).NotTo(HaveOccurred())
			defer func() { Expect(renderer.Shutdown()).To(Succeed()) }()
			opened, e := renderer.Open(ctx, f.token, key(GinkgoT()), version.ID)
			Expect(e).NotTo(HaveOccurred())
			var delivery struct {
				Origin  string `json:"render_origin"`
				Handoff struct {
					URL    string `json:"url"`
					Secret string `json:"secret"`
				} `json:"handoff"`
			}
			Expect(json.Unmarshal(opened.Body, &delivery)).To(Succeed())
			jar, e := cookiejar.New(nil)
			Expect(e).NotTo(HaveOccurred())
			client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
			defer client.CloseIdleConnections()
			handoff, e := client.PostForm(delivery.Handoff.URL+"?page="+url.QueryEscape("/read.html"), url.Values{"secret": {delivery.Handoff.Secret}})
			Expect(e).NotTo(HaveOccurred())
			Expect(handoff.StatusCode).To(Equal(200))
			Expect(handoff.Body.Close()).To(Succeed())
			for _, page := range []string{"read.html", "nested/next.html", "assets/app.js"} {
				request, e := http.NewRequest(http.MethodGet, delivery.Origin+"/"+page, nil)
				Expect(e).NotTo(HaveOccurred())
				request.Header.Set("Sec-Fetch-Site", "same-origin")
				response, e := client.Do(request)
				Expect(e).NotTo(HaveOccurred())
				body, e := io.ReadAll(response.Body)
				Expect(e).NotTo(HaveOccurred())
				Expect(response.Body.Close()).To(Succeed())
				Expect(response.StatusCode).To(Equal(200), page)
				Expect(string(body)).To(ContainSubstring(page))
			}
			// The real missed consumer above decides validity first. These assertions
			// check stable retained ownership, not a particular UUID allocation scheme.
			Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
				attempts, e := tx.ProviderAttempts(g.JobID)
				if e != nil {
					return e
				}
				Expect(attempts).To(HaveLen(1))
				Expect(attempts[0].State).To(Equal("result_saved"))
				Expect(attempts[0].JobID).To(Equal(g.JobID))
				intent, e := tx.GenerationIntent(g.JobID)
				if e != nil {
					return e
				}
				Expect(intent.ContentSetID).To(Equal(version.ContentSetID))
				draft, e := tx.Draft(g.DraftID)
				if e != nil {
					return e
				}
				Expect(draft.ContentSetID).To(Equal(version.ContentSetID))
				history, e := tx.DraftHistory(g.DraftID, 0, 64)
				if e != nil {
					return e
				}
				Expect(history).To(HaveLen(3))
				Expect(history[0].ContentSetID).To(BeEmpty())
				Expect(history[1].ContentSetID).To(Equal(version.ContentSetID))
				Expect(history[2].ContentSetID).To(Equal(version.ContentSetID))
				// Existing ownership protection must keep the exact directory inventory.
				Expect(errors.Is(tx.DiscardContentSet(version.ContentSetID), domain.ErrPinned)).To(BeTrue())
				entries, e := tx.Entries(version.ContentSetID, "", 64)
				Expect(entries).To(HaveLen(6))
				return e
			})).To(Succeed())
		}()
	}
})
