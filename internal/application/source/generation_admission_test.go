package source_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/ai/codex"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	adapter "haeryong.local/haeryong/internal/adapters/source"
	generation "haeryong.local/haeryong/internal/application/generation"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"haeryong.local/haeryong/skills"
)

func generationService(f *vFixture) *generation.Service {
	path, owned, e := skills.Materialize(GinkgoT().TempDir())
	Expect(e).NotTo(HaveOccurred())
	return generation.New(f.store, f.auth, f.core, domain.GenerationInvocation{SkillPath: path, SkillSHA: owned.SHA256, SkillVersion: owned.Version}, codex.SemanticRequestSHA)
}
func generatedIDs(r domain.Response) (string, string) {
	var value struct {
		Job   string `json:"job_id"`
		Draft string `json:"draft_id"`
	}
	Expect(json.Unmarshal(r.Body, &value)).To(Succeed())
	Expect(value.Job).NotTo(BeEmpty())
	Expect(value.Draft).NotTo(BeEmpty())
	return value.Job, value.Draft
}
func generationInput(f *vFixture, id string) generation.Request {
	return generation.Request{ProjectID: f.project, DocumentPresent: true, Intent: "draft", Prompt: "Explain the supplied source with useful linked pages and distinguish unknowns", ProviderID: "codex", Sources: managedRequest(id, "readme.txt")}
}

type admittedDirectoryDriver struct {
	f     *vFixture
	calls int
}

func (d *admittedDirectoryDriver) Execute(ctx context.Context, a domain.ProviderAttempt, inputs []domain.ProviderInput, send func() error, ack func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	d.calls++
	if e := send(); e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	observed := []domain.ProviderInput{}
	for _, input := range inputs {
		var object domain.Object
		if e := d.f.store.Write(ctx, func(tx app.WriteTx) error { var e error; object, e = tx.Object(input.Hash); return e }); e != nil {
			return work.ProviderOutcome{LocalClosed: true}, e
		}
		if e := d.f.store.Read(ctx, object, domain.Pin{Kind: "test_read", OwnerID: a.ID}, func(r io.ReadSeeker) error { _, e := io.Copy(io.Discard, r); return e }); e != nil {
			return work.ProviderOutcome{LocalClosed: true}, e
		}
		input.ObservedOrigin = "daemon_supplied"
		observed = append(observed, input)
	}
	if e := ack(work.ProviderProgress{RequestID: "offline-generation-turn", SessionID: "offline-generation-thread", Observed: observed}); e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	var tree bytes.Buffer
	z := zip.NewWriter(&tree)
	w, e := z.Create("explanation.html")
	if e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	if _, e = w.Write([]byte("<html>source-grounded offline fixture</html>")); e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	if e = z.Close(); e != nil {
		return work.ProviderOutcome{LocalClosed: true}, e
	}
	return work.ProviderOutcome{State: "result_saved", CompletionObserved: true, LocalClosed: true, RequestID: "offline-generation-turn", SessionID: "offline-generation-thread", Body: io.NopCloser(bytes.NewReader(tree.Bytes()))}, nil
}
func (d *admittedDirectoryDriver) Cancel(context.Context, domain.ProviderAttempt) error {
	return domain.ErrReconciliation
}
func (d *admittedDirectoryDriver) Reconcile(context.Context, domain.ProviderAttempt, bool) (work.ProviderOutcome, error) {
	Fail("new generation admission must not query a different attempt")
	return work.ProviderOutcome{}, domain.ErrReconciliation
}

var _ = It("GGEN10 authenticated generation admission atomically hands exact captured source to the existing dispatcher and replays one job", Label("persistence"), func() {
	ctx := context.Background()
	f, root, id := managedFixture("selected_snapshot")
	Expect(os.WriteFile(filepath.Join(root, "readme.txt"), []byte("exact original source"), 0600)).To(Succeed())
	core := generationService(f)
	input := generationInput(f, id)
	key := sid()
	response, e := core.Start(ctx, f.token, key, input)
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(202))
	job, draft := generatedIDs(response)
	j, e := f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("queued"))
	var scope domain.ProviderScope
	Expect(json.Unmarshal(j.Scope, &scope)).To(Succeed())
	Expect(scope.ReadIDs).To(HaveLen(1))
	Expect(scope.TargetSetID).NotTo(BeEmpty())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		g, e := tx.GenerationIntent(job)
		if e != nil {
			return e
		}
		Expect(g.AdmissionReady).To(BeTrue())
		r, e := tx.CapturedRead(scope.ReadIDs[0])
		if e != nil {
			return e
		}
		Expect(r.BytesSHA).To(Equal(vsha([]byte("exact original source"))))
		return nil
	})).To(Succeed())
	// A response-loss retry cannot reread changed input or allocate a second job.
	Expect(os.WriteFile(filepath.Join(root, "readme.txt"), []byte("later changed source"), 0600)).To(Succeed())
	replay, e := core.Start(ctx, f.token, key, input)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(response))
	changed := input
	changed.Prompt = "different requested work"
	_, e = core.Start(ctx, f.token, key, changed)
	Expect(e).To(MatchError(domain.ErrConflict))
	imports := importer.New(f.store, f.auth, f.store, staging.New())
	driver := &admittedDirectoryDriver{f: f}
	p := work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: imports.PrepareGeneration, Apply: imports.ApplyGeneration}
	Expect(p.Process(ctx, job)).To(Succeed())
	Expect(driver.calls).To(Equal(1))
	j, e = f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		d, e := tx.Draft(draft)
		if e != nil {
			return e
		}
		Expect(d.State).To(Equal("ready"))
		rows, e := tx.ProviderAttempts(job)
		if e != nil {
			return e
		}
		observed, e := tx.ProviderInputs(rows[0].ID)
		if e != nil {
			return e
		}
		Expect(observed[0].ObservedOrigin).To(Equal("daemon_supplied"))
		return nil
	})).To(Succeed())
})

type generationCaptureFiles struct {
	inner  source.FileReader
	before func()
}

func (f *generationCaptureFiles) Open(ctx context.Context, root, path string) (io.ReadCloser, error) {
	if f.before != nil {
		before := f.before
		f.before = nil
		before()
	}
	return f.inner.Open(ctx, root, path)
}
func generationWorkflow(f *vFixture, versions ...string) (string, domain.WorkflowPrincipal, time.Time) {
	token, e := sec.Secret()
	Expect(e).NotTo(HaveOccurred())
	now := time.Now().UTC().Truncate(time.Millisecond)
	expiry := now.Add(time.Second)
	scope := domain.CredentialScope{Kind: "workflow", VersionIDs: versions, ProjectIDs: []string{f.project}, Actions: []string{"generation.request", "source.read", "project.read", "version.read"}}
	p := domain.WorkflowPrincipal{ID: sid(), BindingID: sid(), Active: true, Scope: scope, Revision: 1}
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		if e := tx.PutWorkflowPrincipal(p); e != nil {
			return e
		}
		if e := tx.PutWorkflowSelection(domain.WorkflowSelection{BindingID: p.BindingID, PrincipalID: p.ID, ProjectID: f.project, ProviderID: "codex", SourceSelection: []byte(`{}`)}, 0); e != nil {
			return e
		}
		state, e := tx.AuthState()
		if e != nil {
			return e
		}
		project, e := tx.Scope(f.project)
		if e != nil {
			return e
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: sid(), PrincipalID: p.ID, TokenHash: sec.Verifier("workflow", token), AuthEpoch: state.AuthEpoch, Scope: scope, MutationProjectID: f.project, Permissions: map[string]int64{f.project: project.Project.PermissionsRevision}, ConfigRevision: project.ConfigRevision, CreatedAt: now, ExpiresAt: &expiry, Revision: 1})
	})).To(Succeed())
	f.auth.Now = func() time.Time { return now }
	return token, p, expiry
}

var _ = It("GGEN11 accepted project-scoped workflow survives bearer expiry but binding or captured source-action revocation prevents dispatcher admission", Label("persistence"), func() {
	for _, mode := range []string{"expiry", "binding-revoked", "source-action-revoked"} {
		ctx := context.Background()
		f, root, id := managedFixture("selected_snapshot")
		Expect(os.WriteFile(filepath.Join(root, "readme.txt"), []byte("accepted source"), 0600)).To(Succeed())
		token, principal, expiry := generationWorkflow(f)
		core := generationService(f)
		f.core.Files = &generationCaptureFiles{inner: adapter.Files{ExcludedPaths: []string{f.dataDir}}, before: func() {
			if mode == "expiry" {
				f.auth.Now = func() time.Time { return expiry.Add(time.Second) }
				return
			}
			if mode == "binding-revoked" {
				principal.Active = false
			} else {
				principal.Scope.Actions = []string{"generation.request"}
			}
			Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.PutWorkflowPrincipal(principal) })).To(Succeed())
		}}
		response, e := core.Start(ctx, token, sid(), generationInput(f, id))
		Expect(e).NotTo(HaveOccurred(), mode)
		job, _ := generatedIDs(response)
		j, e := f.store.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		if mode != "expiry" {
			Expect(j.State).To(Equal("paused"))
			Expect(j.Error).To(Equal("source_capture_unavailable"))
			continue
		}
		_, e = f.auth.Authenticate(ctx, token)
		Expect(e).To(MatchError(sec.ErrExpired))
		imports := importer.New(f.store, f.auth, f.store, staging.New())
		d := &admittedDirectoryDriver{f: f}
		p := work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d, Prepare: imports.PrepareGeneration, Apply: imports.ApplyGeneration}
		Expect(p.Process(ctx, job)).To(Succeed())
		Expect(d.calls).To(Equal(1))
		j, e = f.store.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.State).To(Equal("completed"))
	}
})
var _ = It("GGEN12 incomplete selected source retains the full failed capture and one paused non-dispatchable generation job", Label("persistence"), func() {
	ctx := context.Background()
	f, _, id := managedFixture("selected_snapshot")
	core := generationService(f)
	input := generationInput(f, id)
	key := sid()
	r, e := core.Start(ctx, f.token, key, input)
	Expect(e).NotTo(HaveOccurred())
	job, _ := generatedIDs(r)
	j, e := f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(j.Error).To(Equal("source_capture_unavailable"))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		g, e := tx.GenerationIntent(job)
		if e != nil {
			return e
		}
		Expect(g.AdmissionReady).To(BeFalse())
		rows, e := tx.ProviderAttempts(job)
		Expect(rows).To(BeEmpty())
		if e != nil {
			return e
		}
		receipt, e := tx.Receipt(domain.OperationKey{PrincipalID: func() string { var s domain.ProviderScope; _ = json.Unmarshal(j.Scope, &s); return s.PrincipalID }(), OperationID: g.CaptureOperationID})
		if e != nil {
			return e
		}
		Expect(receipt.State).To(Equal("completed"))
		for _, decision := range []string{"continue", "retry"} {
			_, e = tx.ResumeProviderJob(job, j.AttemptGeneration, decision)
			Expect(e).To(MatchError(domain.ErrReconciliation))
		}
		return nil
	})).To(Succeed())
	replay, e := core.Start(ctx, f.token, key, input)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(r))
})
var _ = It("GGEN13 source-free admission stays unverified and runtime skill paths and operation IDs cannot change semantic provider identity", Label("persistence"), func() {
	ctx := context.Background()
	f, _, _ := managedFixture("selected_snapshot")
	core := generationService(f)
	input := generation.Request{ProjectID: f.project, DocumentPresent: true, Intent: "draft", ProviderID: "codex", Prompt: "Explain this conceptual question without supplied project source"}
	r, e := core.Start(ctx, f.token, sid(), input)
	Expect(e).NotTo(HaveOccurred())
	job, _ := generatedIDs(r)
	j, e := f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	var scope domain.ProviderScope
	Expect(json.Unmarshal(j.Scope, &scope)).To(Succeed())
	Expect(scope.TargetSetID).To(BeEmpty())
	Expect(scope.ReadIDs).To(BeEmpty())
	changed := *scope.Invocation
	g := *changed.Generation
	g.SkillPath = filepath.Join(GinkgoT().TempDir(), "different-cache", "SKILL.md")
	changed.Generation = &g
	changed.CWD = GinkgoT().TempDir()
	var target map[string]any
	Expect(json.Unmarshal(scope.Target, &target)).To(Succeed())
	target["operation_id"] = sid()
	raw, _ := json.Marshal(target)
	sha, e := codex.SemanticRequestSHA(scope.PrincipalID, raw, changed, nil)
	Expect(e).NotTo(HaveOccurred())
	Expect(sha).To(Equal(scope.RequestSHA))
	g.Intent = "publish"
	changed.Generation = &g
	other, e := codex.SemanticRequestSHA(scope.PrincipalID, raw, changed, nil)
	Expect(e).NotTo(HaveOccurred())
	Expect(other).NotTo(Equal(sha))
})
