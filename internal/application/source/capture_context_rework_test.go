package source_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/recovery"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

func sourceContextLifecycle(f *vFixture, version, action string) domain.LifecycleResult {
	GinkgoHelper()
	ctx := context.Background()
	var document string
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		v, e := tx.PublishedVersion(version)
		document = v.DocumentID
		return e
	})).To(Succeed())
	q := query.New(f.store, f.auth, f.store)
	d, e := f.store.Document(ctx, document)
	Expect(e).NotTo(HaveOccurred())
	body, _ := json.Marshal(map[string]any{"operation": action, "expected_revision": d.Revision})
	r, e := q.LifecyclePlanCreate(ctx, f.token, "", document, body)
	Expect(e).NotTo(HaveOccurred())
	var plan domain.LifecyclePage
	Expect(json.Unmarshal(r.Body, &plan)).To(Succeed())
	body, _ = json.Marshal(map[string]any{"plan_id": plan.ID, "expected_revision": plan.ExpectedRevision, "confirmed": true})
	r, e = q.LifecycleCommit(ctx, f.token, plan.ID, "", body)
	Expect(e).NotTo(HaveOccurred())
	var result domain.LifecycleResult
	Expect(json.Unmarshal(r.Body, &result)).To(Succeed())
	return result
}

func versionSnapshot(f *vFixture, root, sourceID, version string) source.CaptureRequest {
	GinkgoHelper()
	Expect(os.WriteFile(filepath.Join(root, "readme.txt"), []byte("retained version-context source"), 0600)).To(Succeed())
	request := managedRequest(sourceID, "readme.txt")
	request.VersionID = version
	r, e := f.core.Capture(context.Background(), f.token, sid(), f.project, request)
	Expect(e).NotTo(HaveOccurred())
	view, e := f.core.CaptureView(context.Background(), f.token, captureID(r), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(view["complete"]).To(BeTrue())
	request.VersionID = ""
	request.SourceMode = "pinned"
	request.Selections[0].Paths[0].ReadID = view["items"].([]domain.SourceCaptureRead)[0].ID
	return request
}

var _ = It("T4R2SOURCE01 Trash before retained generation cannot launder a version snapshot into a new project-only input", Label("persistence"), func() {
	ctx := context.Background()
	f, root, sourceID := managedFixture("selected_snapshot")
	version := publishedGenerationContext(f).VersionID
	selection := versionSnapshot(f, root, sourceID, version)
	sourceContextLifecycle(f, version, "trash")
	input := generationInput(f, sourceID)
	input.Sources = selection
	r, e := generationService(f).Start(ctx, f.token, sid(), input)
	Expect(e).NotTo(HaveOccurred())
	job, _ := generatedIDs(r)
	imports := importer.New(f.store, f.auth, f.store, staging.New())
	driver := &admittedDirectoryDriver{f: f}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: imports.PrepareGeneration, Apply: imports.ApplyGeneration}
	_ = p.Process(ctx, job)
	Expect(driver.calls).To(BeZero(), "the real dispatcher must not execute with source bytes whose original context is now Trash")
	j, e := f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).NotTo(Equal("completed"))
})

var _ = It("T4R2SOURCE12 current principal scope cannot widen an accepted credential missing the original snapshot version", Label("persistence"), func() {
	for _, capturedVersion := range []bool{false, true} {
		ctx := context.Background()
		f, root, sourceID := managedFixture("selected_snapshot")
		version := publishedGenerationContext(f).VersionID
		captureToken, principal, expiry := generationWorkflow(f, version)
		Expect(os.WriteFile(filepath.Join(root, "readme.txt"), []byte("captured and current scope are separate"), 0600)).To(Succeed())
		selected := managedRequest(sourceID, "readme.txt")
		selected.VersionID = version
		response, e := f.core.Capture(ctx, captureToken, sid(), f.project, selected)
		Expect(e).NotTo(HaveOccurred())
		view, e := f.core.CaptureView(ctx, captureToken, captureID(response), "")
		Expect(e).NotTo(HaveOccurred())
		selected.VersionID = ""
		selected.SourceMode = "pinned"
		selected.Selections[0].Paths[0].ReadID = view["items"].([]domain.SourceCaptureRead)[0].ID
		// Same principal and current V remain valid. Only this new credential's
		// immutable captured VersionIDs differ; no owner fallback is involved.
		token, e := sec.Secret()
		Expect(e).NotTo(HaveOccurred())
		scope := principal.Scope
		if !capturedVersion {
			scope.VersionIDs = nil
		}
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			current, e := tx.WorkflowPrincipal(principal.ID)
			if e != nil {
				return e
			}
			Expect(current.Scope.VersionIDs).To(ContainElement(version))
			auth, e := tx.AuthState()
			if e != nil {
				return e
			}
			project, e := tx.Scope(f.project)
			if e != nil {
				return e
			}
			return tx.PutScopedCredential(domain.ScopedCredential{ID: sid(), PrincipalID: principal.ID, TokenHash: sec.Verifier("workflow", token), AuthEpoch: auth.AuthEpoch, Scope: scope, MutationProjectID: f.project, Permissions: map[string]int64{f.project: project.Project.PermissionsRevision}, ConfigRevision: project.ConfigRevision, CreatedAt: expiry.Add(-time.Second), ExpiresAt: &expiry, Revision: 1})
		})).To(Succeed())
		input := generationInput(f, sourceID)
		input.Sources = selected
		response, e = generationService(f).Start(ctx, token, sid(), input)
		Expect(e).NotTo(HaveOccurred())
		job, _ := generatedIDs(response)
		imports := importer.New(f.store, f.auth, f.store, staging.New())
		driver := &admittedDirectoryDriver{f: f}
		_ = (&work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: imports.PrepareGeneration, Apply: imports.ApplyGeneration}).Process(ctx, job)
		if capturedVersion {
			Expect(driver.calls).To(Equal(1))
		} else {
			Expect(driver.calls).To(BeZero())
		}
	}
})

var _ = It("T4R2SOURCE10 selected retained-source export checks inherited visibility at snapshot and publication without changing ordinary payload export", Label("persistence"), func() {
	for _, mode := range []string{"eligible", "trash-before", "trash-before-publication", "payload-only"} {
		ctx := context.Background()
		f, root, sourceID := managedFixture("selected_snapshot")
		v1 := publishedGenerationContext(f).VersionID
		v2 := publishedGenerationContext(f).VersionID
		selection := versionSnapshot(f, root, sourceID, v1)
		selection.VersionID = v2
		_, e := f.core.Capture(ctx, f.token, sid(), f.project, selection)
		Expect(e).NotTo(HaveOccurred())
		if mode == "trash-before" || mode == "payload-only" {
			sourceContextLifecycle(f, v1, "trash")
		}
		output := filepath.Join(GinkgoT().TempDir(), "export")
		core := recovery.New(f.store, f.auth, f.store)
		r, e := core.Submit(ctx, f.token, sid(), domain.CopyRequest{Action: "export.plan", VersionID: v2, Format: "directory", Destination: output, RetainedSources: mode != "payload-only"})
		Expect(e).NotTo(HaveOccurred())
		var plan domain.CopyResult
		Expect(json.Unmarshal(r.Body, &plan)).To(Succeed())
		r, e = core.Submit(ctx, f.token, sid(), domain.CopyRequest{Action: "export.create", ID: plan.PlanID})
		Expect(e).NotTo(HaveOccurred())
		var accepted domain.CopyResult
		Expect(json.Unmarshal(r.Body, &accepted)).To(Succeed())
		triggered := false
		if mode == "trash-before-publication" {
			f.store.CopyFault = func(stage string) error {
				if stage == "export_verified" && !triggered {
					triggered = true
					sourceContextLifecycle(f, v1, "trash")
				}
				return nil
			}
		}
		var state string
		for turn := 0; turn < 300; turn++ {
			e = f.store.ProcessCopy(ctx, accepted.JobID)
			if e != nil {
				break
			}
			Expect(f.store.Write(ctx, func(tx app.WriteTx) error { set, e := tx.CopySet(accepted.ID); state = set.Stage; return e })).To(Succeed())
			if state == "complete" {
				break
			}
		}
		if mode == "eligible" || mode == "payload-only" {
			Expect(e).NotTo(HaveOccurred())
			Expect(state).To(Equal("complete"))
			if mode == "payload-only" {
				// One-version ordinary directory export intentionally has no index.
				body, err := os.ReadFile(filepath.Join(output, "explanation.html"))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(body)).To(Equal("<html>source-grounded offline fixture</html>"))
			} else {
				_, e = foundation.VerifyExport(ctx, output, "")
				Expect(e).NotTo(HaveOccurred())
			}
		} else {
			Expect(e).To(HaveOccurred())
			Expect(state).NotTo(Equal("complete"))
			_, e = os.Stat(output)
			Expect(os.IsNotExist(e)).To(BeTrue())
		}
		if mode == "trash-before-publication" {
			Expect(triggered).To(BeTrue())
		}
	}
})

type sourceContextRevokingDriver struct {
	admittedDirectoryDriver
	version string
}

func (d *sourceContextRevokingDriver) Execute(ctx context.Context, a domain.ProviderAttempt, inputs []domain.ProviderInput, send func() error, ack func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	out, e := d.admittedDirectoryDriver.Execute(ctx, a, inputs, send, ack)
	if e == nil {
		sourceContextLifecycle(d.f, d.version, "trash")
	}
	return out, e
}

var _ = It("T4R2SOURCE11 Trash after actual controlled delivery retains its observation but fences result application", Label("persistence"), func() {
	ctx := context.Background()
	f, root, sourceID := managedFixture("selected_snapshot")
	version := publishedGenerationContext(f).VersionID
	selection := versionSnapshot(f, root, sourceID, version)
	input := generationInput(f, sourceID)
	input.Sources = selection
	r, e := generationService(f).Start(ctx, f.token, sid(), input)
	Expect(e).NotTo(HaveOccurred())
	job, draft := generatedIDs(r)
	imports := importer.New(f.store, f.auth, f.store, staging.New())
	driver := &sourceContextRevokingDriver{admittedDirectoryDriver: admittedDirectoryDriver{f: f}, version: version}
	_ = (&work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: imports.PrepareGeneration, Apply: imports.ApplyGeneration}).Process(ctx, job)
	Expect(driver.calls).To(Equal(1), "one already authorized controlled delivery cannot be recalled")
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		d, e := tx.Draft(draft)
		if e != nil {
			return e
		}
		Expect(d.State).NotTo(Equal("ready"))
		Expect(d.PublishedVersionID).To(BeEmpty())
		attempts, e := tx.ProviderAttempts(job)
		if e != nil {
			return e
		}
		Expect(attempts).To(HaveLen(1))
		inputs, e := tx.ProviderInputs(attempts[0].ID)
		if e != nil {
			return e
		}
		Expect(inputs).To(HaveLen(1))
		Expect(inputs[0].ObservedAt).NotTo(BeNil())
		Expect(inputs[0].ObservedOrigin).To(Equal("daemon_supplied"))
		return nil
	})).To(Succeed())
})

var _ = It("T4R2SOURCE02 Trash and restore before capture admission cannot resurrect accepted source preparation", Label("persistence"), func() {
	ctx := context.Background()
	f, root, sourceID := managedFixture("selected_snapshot")
	version := publishedGenerationContext(f).VersionID
	selection := versionSnapshot(f, root, sourceID, version)
	triggered := false
	f.core.Fault = func(stage string) error {
		if stage == "capture_before_commit" && !triggered {
			triggered = true
			sourceContextLifecycle(f, version, "trash")
			sourceContextLifecycle(f, version, "restore")
		}
		return nil
	}
	input := generationInput(f, sourceID)
	input.Sources = selection
	r, e := generationService(f).Start(ctx, f.token, sid(), input)
	Expect(e).NotTo(HaveOccurred())
	job, _ := generatedIDs(r)
	Expect(triggered).To(BeTrue())
	j, e := f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.CancelRequested || j.State == "cancelled").To(BeTrue())
	driver := &admittedDirectoryDriver{f: f}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver}
	_ = p.Process(ctx, job)
	Expect(driver.calls).To(BeZero())
})

type sourceContextReadBarrier struct {
	source.CaptureBytes
	after func()
}

func (b sourceContextReadBarrier) Read(ctx context.Context, o domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) error {
	return b.CaptureBytes.Read(ctx, o, pin, func(r io.ReadSeeker) error {
		return consume(&sourceContextByteBarrier{ReadSeeker: r, after: b.after})
	})
}

type sourceContextByteBarrier struct {
	io.ReadSeeker
	after func()
}

func (r *sourceContextByteBarrier) Read(p []byte) (int, error) {
	if r.after == nil || len(p) == 0 {
		return r.ReadSeeker.Read(p)
	}
	n, e := r.ReadSeeker.Read(p[:1])
	if n > 0 {
		after := r.after
		r.after = nil
		after()
	}
	return n, e
}

var _ = It("T4R2SOURCE03 Trash during retained-byte copy fences the next read and prevents admission and Execute", Label("persistence"), func() {
	ctx := context.Background()
	f, root, sourceID := managedFixture("selected_snapshot")
	version := publishedGenerationContext(f).VersionID
	selection := versionSnapshot(f, root, sourceID, version)
	barrierCalls := 0
	f.core.Bytes = sourceContextReadBarrier{CaptureBytes: f.store, after: func() {
		barrierCalls++
		sourceContextLifecycle(f, version, "trash")
	}}
	input := generationInput(f, sourceID)
	input.Sources = selection
	r, e := generationService(f).Start(ctx, f.token, sid(), input)
	Expect(e).NotTo(HaveOccurred())
	job, _ := generatedIDs(r)
	Expect(barrierCalls).To(Equal(1), "one already checked byte crosses the barrier; later bytes cannot be recalled or relabelled")
	driver := &admittedDirectoryDriver{f: f}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver}
	_ = p.Process(ctx, job)
	Expect(driver.calls).To(BeZero())
})

var _ = It("T4R2SOURCE04 copied ancestry remains version-owned through subsequent reads and queued dispatch", Label("persistence"), func() {
	ctx := context.Background()
	f, root, sourceID := managedFixture("selected_snapshot")
	version := publishedGenerationContext(f).VersionID
	selection := versionSnapshot(f, root, sourceID, version)
	copy, e := f.core.Capture(ctx, f.token, sid(), f.project, selection)
	Expect(e).NotTo(HaveOccurred())
	view, e := f.core.CaptureView(ctx, f.token, captureID(copy), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(view["complete"]).To(BeTrue())
	copied := view["items"].([]domain.SourceCaptureRead)[0]
	selection.Selections[0].Paths[0].ReadID = copied.ID
	input := generationInput(f, sourceID)
	input.Sources = selection
	r, e := generationService(f).Start(ctx, f.token, sid(), input)
	Expect(e).NotTo(HaveOccurred())
	job, _ := generatedIDs(r)
	sourceContextLifecycle(f, version, "trash")
	bytesRead := false
	Expect(f.core.ReadCaptured(ctx, f.token, copied.ID, func(domain.SourceCaptureRead, io.ReadSeeker) error { bytesRead = true; return nil })).NotTo(Succeed())
	Expect(bytesRead).To(BeFalse())
	j, e := f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.CancelRequested || j.State == "cancelled").To(BeTrue())
	driver := &admittedDirectoryDriver{f: f}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver}
	_ = p.Process(ctx, job)
	Expect(driver.calls).To(BeZero())
})

var _ = It("T4R2SOURCE05 accepted scoped snapshot uses captured and current version scope without requiring the original bearer lifetime", Label("persistence"), func() {
	for _, mode := range []string{"expired", "current-version-removed", "foreign-capture"} {
		ctx := context.Background()
		f, root, sourceID := managedFixture("selected_snapshot")
		version := publishedGenerationContext(f).VersionID
		token, principal, expiry := generationWorkflow(f, version)
		Expect(os.WriteFile(filepath.Join(root, "readme.txt"), []byte("same-principal retained source"), 0600)).To(Succeed())
		request := managedRequest(sourceID, "readme.txt")
		request.VersionID = version
		captureToken := token
		if mode == "foreign-capture" {
			captureToken = f.token
		}
		c, e := f.core.Capture(ctx, captureToken, sid(), f.project, request)
		Expect(e).NotTo(HaveOccurred())
		view, e := f.core.CaptureView(ctx, captureToken, captureID(c), "")
		Expect(e).NotTo(HaveOccurred())
		request.VersionID, request.SourceMode = "", "pinned"
		request.Selections[0].Paths[0].ReadID = view["items"].([]domain.SourceCaptureRead)[0].ID
		input := generationInput(f, sourceID)
		input.Sources = request
		r, e := generationService(f).Start(ctx, token, sid(), input)
		Expect(e).NotTo(HaveOccurred())
		job, _ := generatedIDs(r)
		if mode == "expired" {
			f.auth.Now = func() time.Time { return expiry.Add(time.Second) }
		}
		if mode == "current-version-removed" {
			principal.Scope.VersionIDs = nil
			Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.PutWorkflowPrincipal(principal) })).To(Succeed())
		}
		imports := importer.New(f.store, f.auth, f.store, staging.New())
		driver := &admittedDirectoryDriver{f: f}
		p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: imports.PrepareGeneration, Apply: imports.ApplyGeneration}
		_ = p.Process(ctx, job)
		if mode == "expired" {
			Expect(driver.calls).To(Equal(1))
		} else {
			Expect(driver.calls).To(BeZero())
		}
	}
})

var _ = It("T4R2SOURCE06 copied owner metadata stays scrubbed and inspectable after source revocation while bytes and provider use are denied", Label("persistence"), func() {
	ctx := context.Background()
	f, root, sourceID := managedFixture("selected_snapshot")
	version := publishedGenerationContext(f).VersionID
	selection := versionSnapshot(f, root, sourceID, version)
	copy, e := f.core.Capture(ctx, f.token, sid(), f.project, selection)
	Expect(e).NotTo(HaveOccurred())
	view, e := f.core.CaptureView(ctx, f.token, captureID(copy), "")
	Expect(e).NotTo(HaveOccurred())
	read := view["items"].([]domain.SourceCaptureRead)[0]
	input := generationInput(f, sourceID)
	input.Sources = selection
	r, e := generationService(f).Start(ctx, f.token, sid(), input)
	Expect(e).NotTo(HaveOccurred())
	job, _ := generatedIDs(r)
	mapping, _ := json.Marshal(domain.ManagedSourceMapping{SourceID: "different-source", Kind: "files", RootPath: root, RetentionMode: "selected_snapshot", ProviderIDs: []string{"codex"}})
	f.configure(string(mapping))
	view, e = f.core.CaptureView(ctx, f.token, captureID(copy), "")
	Expect(e).NotTo(HaveOccurred())
	projected := view["items"].([]domain.SourceCaptureRead)[0]
	Expect(projected.Availability).To(Equal("denied"))
	Expect(projected.Hash).To(BeEmpty())
	Expect(projected.SourceJSON).To(BeEmpty())
	readCalled := false
	Expect(f.core.ReadCaptured(ctx, f.token, read.ID, func(domain.SourceCaptureRead, io.ReadSeeker) error { readCalled = true; return nil })).NotTo(Succeed())
	Expect(readCalled).To(BeFalse())
	driver := &admittedDirectoryDriver{f: f}
	_ = (&work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver}).Process(ctx, job)
	Expect(driver.calls).To(BeZero())
})

type sourceBeforeReadBarrier struct {
	source.CaptureBytes
	before func()
}

func (b sourceBeforeReadBarrier) Read(ctx context.Context, o domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) error {
	b.before()
	return b.CaptureBytes.Read(ctx, o, pin, consume)
}

var _ = It("T4R2SOURCE07 active public pinned copy blocks dependent purge before its first Put and independent snapshots remain owned", Label("persistence"), func() {
	ctx := context.Background()
	f, root, sourceID := managedFixture("selected_snapshot")
	version := publishedGenerationContext(f).VersionID
	selection := versionSnapshot(f, root, sourceID, version)
	independent, e := f.core.Capture(ctx, f.token, sid(), f.project, managedRequest(sourceID, "readme.txt"))
	Expect(e).NotTo(HaveOccurred())
	view, e := f.core.CaptureView(ctx, f.token, captureID(independent), "")
	Expect(e).NotTo(HaveOccurred())
	independentRead := view["items"].([]domain.SourceCaptureRead)[0].ID
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { v, e := tx.PublishedVersion(version); f.document = v.DocumentID; return e })).To(Succeed())
	var purgeJob string
	barrierCalls := 0
	f.core.Bytes = sourceBeforeReadBarrier{CaptureBytes: f.store, before: func() {
		barrierCalls++
		purgeJob = acceptSourcePurge(f).JobID
		j := sourcePurgeTurns(f, purgeJob, 2)
		Expect(j.State).NotTo(Equal("completed"), "capturing copy already owns the version dependency before any copied row exists")
	}}
	r, e := f.core.Capture(ctx, f.token, sid(), f.project, selection)
	Expect(e).NotTo(HaveOccurred())
	Expect(barrierCalls).To(Equal(1))
	Expect(r.Status).NotTo(Equal(201))
	f.core.Bytes = f.store
	settled := sourcePurgeTurns(f, purgeJob, 100)
	Expect(settled.State).To(Equal("paused"), "the independent snapshot still protects the shared bytes")
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		intent, e := tx.PurgeIntent(purgeJob)
		Expect(intent.MetadataRemoved).To(BeTrue())
		return e
	})).To(Succeed())
	var body []byte
	Expect(f.core.ReadCaptured(ctx, f.token, independentRead, func(_ domain.SourceCaptureRead, r io.ReadSeeker) error {
		var e error
		body, e = io.ReadAll(r)
		return e
	})).To(Succeed())
	Expect(string(body)).To(Equal("retained version-context source"))
})

var _ = It("T4R2SOURCE08 ignored current and pinned Git read IDs create no inherited cancellation or purge dependency", Label("persistence"), func() {
	for _, mode := range []string{"current", "pinned-git"} {
		ctx := context.Background()
		f, root, sourceID := managedFixture("selected_snapshot")
		version := publishedGenerationContext(f).VersionID
		selection := versionSnapshot(f, root, sourceID, version)
		selection.SourceMode = "current"
		if mode == "pinned-git" {
			mapping, _ := json.Marshal(domain.ManagedSourceMapping{SourceID: sourceID, Kind: "git", RootPath: root, RepoID: "e2000000-0000-4000-8000-000000000001", RetentionMode: "selected_snapshot", ProviderIDs: []string{"codex"}})
			f.configure(string(mapping))
			selection.SourceMode = "pinned"
			selection.Selections[0].Revision = gitRevision
			f.reader.blobs[gitRevision+"\x00readme.txt"] = "unrelated exact Git source"
		}
		input := generationInput(f, sourceID)
		input.Sources = selection
		original, _ := json.Marshal(input)
		r, e := generationService(f).Start(ctx, f.token, sid(), input)
		Expect(e).NotTo(HaveOccurred())
		after, _ := json.Marshal(input)
		Expect(after).To(Equal(original), "normalization must not mutate caller selection slices or request identity")
		job, _ := generatedIDs(r)
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			g, e := tx.GenerationIntent(job)
			Expect(g.SourceReadIDs).To(BeEmpty())
			return e
		})).To(Succeed())
		sourceContextLifecycle(f, version, "trash")
		j, e := f.store.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.CancelRequested).To(BeFalse())
		Expect(j.State).To(Equal("queued"))
		purge := sourceContextLifecycle(f, version, "purge")
		_ = sourcePurgeTurns(f, purge.JobID, 100)
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			intent, e := tx.PurgeIntent(purge.JobID)
			Expect(intent.MetadataRemoved).To(BeTrue(), "an ignored read_id must not block metadata purge while this unrelated job is still queued")
			return e
		})).To(Succeed())
		imports := importer.New(f.store, f.auth, f.store, staging.New())
		driver := &admittedDirectoryDriver{f: f}
		Expect((&work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: imports.PrepareGeneration, Apply: imports.ApplyGeneration}).Process(ctx, job)).To(Succeed())
		Expect(driver.calls).To(Equal(1))
	}
})

var _ = It("T4R2SOURCE09 a real multi-context copy preserves every version owner in provider audit without changing immediate-context meaning", Label("persistence"), func() {
	ctx := context.Background()
	f, root, sourceID := managedFixture("selected_snapshot")
	v1 := publishedGenerationContext(f).VersionID
	v2 := publishedGenerationContext(f).VersionID
	selection := versionSnapshot(f, root, sourceID, v1)
	selection.VersionID = v2
	copy, e := f.core.Capture(ctx, f.token, sid(), f.project, selection)
	Expect(e).NotTo(HaveOccurred())
	view, e := f.core.CaptureView(ctx, f.token, captureID(copy), "")
	Expect(e).NotTo(HaveOccurred())
	selection.VersionID = ""
	selection.Selections[0].Paths[0].ReadID = view["items"].([]domain.SourceCaptureRead)[0].ID
	input := generationInput(f, sourceID)
	input.Sources = selection
	r, e := generationService(f).Start(ctx, f.token, sid(), input)
	Expect(e).NotTo(HaveOccurred())
	job, _ := generatedIDs(r)
	imports := importer.New(f.store, f.auth, f.store, staging.New())
	driver := &admittedDirectoryDriver{f: f}
	Expect((&work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: imports.PrepareGeneration, Apply: imports.ApplyGeneration}).Process(ctx, job)).To(Succeed())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		attempts, e := tx.ProviderAttempts(job)
		if e != nil {
			return e
		}
		Expect(attempts).To(HaveLen(1))
		inputs, e := tx.ProviderInputs(attempts[0].ID)
		if e != nil {
			return e
		}
		Expect(inputs).To(HaveLen(1))
		Expect(inputs[0].SourceVersionID).To(BeEmpty(), "the immediate generation capture is still project-only")
		Expect(inputs[0].SourceVersionIDs).To(ConsistOf(v1, v2))
		Expect(inputs[0].ObservedOrigin).To(Equal("daemon_supplied"))
		return nil
	})).To(Succeed())
})
