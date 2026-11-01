package source_test

import (
	"context"
	"database/sql"
	"encoding/json"
	query "haeryong.local/haeryong/internal/application/query"
	"io"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	generation "haeryong.local/haeryong/internal/application/generation"
	importer "haeryong.local/haeryong/internal/application/import"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

func publishedGenerationContext(f *vFixture) domain.ExplanationReference {
	core := generationService(f)
	r, e := core.Start(context.Background(), f.token, sid(), generation.Request{ProjectID: f.project, DocumentPresent: true, ProviderID: "codex", Intent: "publish", Prompt: "Initial useful explanation"})
	Expect(e).NotTo(HaveOccurred())
	job, _ := generatedIDs(r)
	imports := importer.New(f.store, f.auth, f.store, staging.New())
	driver := &admittedDirectoryDriver{f: f}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: imports.PrepareGeneration, Apply: imports.ApplyGeneration}
	Expect(p.Process(context.Background(), job)).To(Succeed())
	page := "explanation.html"
	var ref domain.ExplanationReference
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		g, e := tx.GenerationIntent(job)
		if e != nil {
			return e
		}
		v, e := tx.PublishedVersion(g.VersionID)
		if e != nil {
			return e
		}
		entry, e := tx.QueryEntry(v.ContentSetID, page)
		if e != nil {
			return e
		}
		ref = domain.ExplanationReference{VersionID: v.ID, PagePath: &page, Hash: entry.Hash}
		return nil
	})).To(Succeed())
	return ref
}

var _ = It("GGEN16 selected immutable explanation context is actually delivered and acknowledged separately from source inputs through production adapter and SQLite", Label("persistence"), func() {
	ctx := context.Background()
	f, _, _ := managedFixture("selected_snapshot")
	ref := publishedGenerationContext(f)
	core := generationService(f)
	input := generation.Request{ProjectID: f.project, DocumentPresent: true, ProviderID: "codex", Intent: "publish", Prompt: strings.Repeat("Use the selected prior explanation as context, retaining unknown source basis. ", 1000), PriorReferences: []domain.ExplanationReference{ref}}
	r, e := core.Start(ctx, f.token, sid(), input)
	Expect(e).NotTo(HaveOccurred())
	job, _ := generatedIDs(r)
	var reference domain.GenerationReference
	var producedVersion string
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		g, e := tx.GenerationIntent(job)
		if e != nil {
			return e
		}
		Expect(g.PriorReferences).To(HaveLen(1))
		reference = g.PriorReferences[0]
		Expect(reference.ExplanationReference).To(Equal(ref))
		return nil
	})).To(Succeed())
	driver := pipelineDriver(f.store, f.store, reference)
	imports := importer.New(f.store, f.auth, f.store, staging.New())
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: imports.PrepareGeneration, Apply: imports.ApplyGeneration}
	Expect(p.Process(ctx, job)).To(Succeed())
	db, e := sql.Open("sqlite", "file:"+filepath.Join(f.dataDir, "storage", "library.sqlite")+"?mode=ro")
	Expect(e).NotTo(HaveOccurred())
	var executionPins int
	Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_attempt' AND pin_id LIKE '%:owned:%' AND owner_id IN(SELECT attempt_id FROM provider_attempts WHERE job_id=?)`, job).Scan(&executionPins)).To(Succeed())
	Expect(db.Close()).To(Succeed())
	Expect(executionPins).To(BeZero(), "complete actual generation releases only temporary prompt/context execution pins")
	j, e := f.store.Job(ctx, job)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	var scope domain.ProviderScope
	Expect(json.Unmarshal(j.Scope, &scope)).To(Succeed())
	Expect(scope.ReadIDs).To(BeEmpty())
	Expect(scope.TargetSetID).To(BeEmpty())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		attempts, e := tx.ProviderAttempts(job)
		if e != nil {
			return e
		}
		Expect(attempts).To(HaveLen(1))
		a := attempts[0]
		Expect(a.State).To(Equal("result_saved"))
		Expect(a.Transport.Generation.ReferencesAcknowledged).To(BeTrue())
		Expect(a.Transport.Generation.References).To(Equal([]domain.GenerationReference{reference}))
		Expect(a.Transport.Generation.PromptInputAcknowledged).To(BeTrue())
		inputs, e := tx.ProviderInputs(a.ID)
		if e != nil {
			return e
		}
		Expect(inputs).To(BeEmpty(), "explanation context never creates a project source observation")
		g, e := tx.GenerationIntent(job)
		if e != nil {
			return e
		}
		v, e := tx.PublishedVersion(g.VersionID)
		if e != nil {
			return e
		}
		Expect(v.SourceBasis).To(Equal("unverified"))
		producedVersion = v.ID
		return nil
	})).To(Succeed())
	// Run the existing static consumers, then follow their exact stored target.
	// Merely placing old text in a prompt does not establish navigation.
	maintenance := work.NewMaintenance(f.store, f.store)
	for {
		finished, e := maintenance.Sweep(ctx)
		Expect(e).NotTo(HaveOccurred())
		if finished {
			break
		}
	}
	for {
		var jobs []domain.Job
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; jobs, e = tx.WorkJobs(64); return e })).To(Succeed())
		count := 0
		for _, j := range jobs {
			if j.Kind == "derived_index" {
				Expect(maintenance.Process(ctx, j.ID)).To(Succeed())
				count++
			}
		}
		if count == 0 {
			break
		}
	}
	q := query.New(f.store, f.auth, f.store)
	links, e := q.References(ctx, f.token, producedVersion, query.Options{})
	Expect(e).NotTo(HaveOccurred())
	Expect(links.Materialization.State).To(Equal("ready"))
	Expect(links.Items).To(HaveLen(1))
	link, e := q.ResolveReference(ctx, f.token, producedVersion, "context-1", query.ReferenceOptions{})
	Expect(e).NotTo(HaveOccurred())
	Expect(link.Resolution).To(Equal("resolved"))
	Expect(link.ReaderURL).NotTo(BeNil())
	Expect(*link.ReaderURL).To(ContainSubstring(ref.VersionID))
	target, ok := link.Target.(query.ExactTarget)
	Expect(ok).To(BeTrue())
	Expect(target.VersionID).To(Equal(ref.VersionID))
	Expect(target.PagePath).To(Equal(ref.PagePath))
	Expect(q.ReadFile(ctx, f.token, target.VersionID, *target.PagePath, false, func(file query.ResolvedFile, r io.ReadSeeker) error {
		bytes, e := io.ReadAll(r)
		Expect(vsha(bytes)).To(Equal(ref.Hash))
		Expect(file.ResolvedPath).To(Equal(*ref.PagePath))
		return e
	})).To(Succeed())
})
var _ = It("GGEN17 prior-context authority uses captured and current exact version scope independently of source.read and accepted bearer expiry", Label("persistence"), func() {
	for _, mode := range []string{"version-revoked", "source-read-removed", "expired"} {
		ctx := context.Background()
		f, _, _ := managedFixture("selected_snapshot")
		ref := publishedGenerationContext(f)
		token, principal, expiry := generationWorkflow(f, ref.VersionID)
		core := generationService(f)
		input := generation.Request{ProjectID: f.project, DocumentPresent: true, ProviderID: "codex", Intent: "draft", Prompt: "Use the selected explanation only", PriorReferences: []domain.ExplanationReference{ref}}
		wrong := input
		wrong.PriorReferences = append([]domain.ExplanationReference{}, input.PriorReferences...)
		wrong.PriorReferences[0].Hash = strings.Repeat("0", 64)
		_, e := core.Start(ctx, token, sid(), wrong)
		Expect(e).To(HaveOccurred())
		r, e := core.Start(ctx, token, sid(), input)
		Expect(e).NotTo(HaveOccurred())
		job, _ := generatedIDs(r)
		switch mode {
		case "version-revoked":
			principal.Scope.VersionIDs = nil
		case "source-read-removed":
			principal.Scope.Actions = []string{"generation.request", "project.read", "version.read"}
		case "expired":
			f.auth.Now = func() time.Time { return expiry.Add(time.Second) }
		}
		if mode != "expired" {
			Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.PutWorkflowPrincipal(principal) })).To(Succeed())
		}
		imports := importer.New(f.store, f.auth, f.store, staging.New())
		driver := &admittedDirectoryDriver{f: f}
		p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: imports.PrepareGeneration, Apply: imports.ApplyGeneration}
		e = p.Process(ctx, job)
		if mode == "version-revoked" {
			Expect(e).To(HaveOccurred())
			Expect(driver.calls).To(BeZero())
		} else {
			Expect(e).NotTo(HaveOccurred())
			Expect(driver.calls).To(Equal(1))
		}
	}
})

var _ = It("GGEN20 cancelled queued or failed-capture admission releases temporary request pins without creating source input facts", Label("persistence"), func() {
	for _, selected := range []bool{false, true} {
		ctx := context.Background()
		f, _, sourceID := managedFixture("selected_snapshot")
		core := generationService(f)
		input := generation.Request{ProjectID: f.project, DocumentPresent: true, ProviderID: "codex", Intent: "draft", Prompt: "Unique retained request " + sid()}
		if selected {
			input.Sources = managedRequest(sourceID, "missing-source.txt")
		}
		response, e := core.Start(ctx, f.token, sid(), input)
		Expect(e).NotTo(HaveOccurred())
		job, _ := generatedIDs(response)
		j, e := f.store.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		if selected {
			Expect(j.State).To(Equal("paused"))
		} else {
			Expect(j.State).To(Equal("queued"))
		}
		db, e := sql.Open("sqlite", "file:"+filepath.Join(f.dataDir, "storage", "library.sqlite")+"?mode=ro")
		Expect(e).NotTo(HaveOccurred())
		var pins, sourceInputs int
		Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='job_input' AND owner_id=?`, job).Scan(&pins)).To(Succeed())
		Expect(pins).To(Equal(1))
		Expect(db.QueryRow(`SELECT count(*) FROM job_inputs WHERE job_id=?`, job).Scan(&sourceInputs)).To(Succeed())
		Expect(sourceInputs).To(BeZero())
		_, e = f.auth.CancelJob(ctx, f.token, job, sid())
		Expect(e).NotTo(HaveOccurred())
		Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='job_input' AND owner_id=?`, job).Scan(&pins)).To(Succeed())
		Expect(pins).To(BeZero())
		Expect(db.Close()).To(Succeed())
		j, e = f.store.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.State).To(Equal("cancelled"))
	}
})
