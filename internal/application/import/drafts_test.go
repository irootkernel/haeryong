package importer_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	"haeryong.local/haeryong/internal/application/auth"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	importer "haeryong.local/haeryong/internal/application/import"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

func readyGeneration(f *fixture, doc, base *string) (domain.GenerationIntent, *generationDriver) {
	g := generationJob(f, "draft", doc, base)
	driver := &generationDriver{data: generationArchive()}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: f.core.PrepareGeneration, Apply: f.core.ApplyGeneration}
	Expect(p.Process(ctx, g.JobID)).To(Succeed())
	return g, driver
}

var _ = It("GDF01 reject a preparing typed draft durably fences its queued generation and exact replay retains its original validator", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	g := generationJob(f, "draft", nil, nil)
	before, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	Expect(before.State).To(Equal("preparing"))
	k := key(GinkgoT())
	r, e := f.core.RejectDraft(ctx, f.token, k, g.DraftID, before.ETag)
	Expect(e).NotTo(HaveOccurred())
	Expect(r.Status).To(Equal(200))
	after, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.State).To(Equal("rejected"))
	Expect(after.Revision).To(Equal(before.Revision + 1))
	Expect(after.ETag).NotTo(Equal(before.ETag))
	replay, e := f.core.RejectDraft(ctx, f.token, k, g.DraftID, before.ETag)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(r))
	_, e = f.core.RejectDraft(ctx, f.token, key(GinkgoT()), g.DraftID, before.ETag)
	Expect(errors.Is(e, domain.ErrPrecondition)).To(BeTrue())
	driver := &generationDriver{data: generationArchive()}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: f.core.PrepareGeneration, Apply: f.core.ApplyGeneration}
	Expect(p.Process(ctx, g.JobID)).To(Succeed())
	Expect(driver.calls.Load()).To(BeZero())
	j, e := f.store.Job(ctx, g.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.CancelRequested).To(BeTrue())
	Expect(j.State).To(Equal("cancelled"))
	history, e := f.core.DraftHistory(ctx, f.token, g.DraftID, 0)
	Expect(e).NotTo(HaveOccurred())
	Expect(history.Items).To(HaveLen(2))
	Expect(history.Items[0].State).To(Equal("preparing"))
	Expect(history.Items[1].State).To(Equal("rejected"))
	_, e = f.store.Version(ctx, g.VersionID)
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue())
})
var _ = It("GDF02 ready draft publishes the saved whole candidate atomically and a stale captured base remains ready without new Execute", Label("persistence"), func() {
	for _, stale := range []bool{false, true} {
		func() {
			f := newFixture(GinkgoT())
			var doc, base *string
			if stale {
				id, v := f.imported(GinkgoT(), f.sealed(GinkgoT(), nil, "old"), key(GinkgoT()))
				doc = &id
				base = &v
			}
			g, driver := readyGeneration(f, doc, base)
			before, e := f.core.Draft(ctx, f.token, g.DraftID)
			Expect(e).NotTo(HaveOccurred())
			Expect(before.State).To(Equal("ready"))
			if stale {
				r, e := f.core.Import(ctx, f.token, key(GinkgoT()), importer.ImportRequest{UploadID: f.sealed(GinkgoT(), doc, "new current"), DocumentID: doc, DocumentPresent: true, BaseVersionID: base, BasePresent: true})
				Expect(e).NotTo(HaveOccurred())
				Expect(f.core.Process(ctx, decoded(GinkgoT(), r)["job_id"])).To(Succeed())
			}
			k := key(GinkgoT())
			r, e := f.core.PublishDraft(ctx, f.token, k, g.DraftID, before.ETag)
			if stale {
				Expect(errors.Is(e, importer.ErrVersionConflict)).To(BeTrue())
				after, e := f.core.Draft(ctx, f.token, g.DraftID)
				Expect(e).NotTo(HaveOccurred())
				Expect(after).To(Equal(before))
				_, e = f.store.Version(ctx, g.VersionID)
				Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue())
			} else {
				Expect(e).NotTo(HaveOccurred())
				after, e := f.core.Draft(ctx, f.token, g.DraftID)
				Expect(e).NotTo(HaveOccurred())
				Expect(after.State).To(Equal("published"))
				Expect(after.PublishedVersionID).To(Equal(&g.VersionID))
				replay, e := f.core.PublishDraft(ctx, f.token, k, g.DraftID, before.ETag)
				Expect(e).NotTo(HaveOccurred())
				Expect(replay).To(Equal(r))
				Expect(r.Headers["ETag"]).To(Equal(after.ETag))
				v, e := f.store.Version(ctx, g.VersionID)
				Expect(e).NotTo(HaveOccurred())
				Expect(v.DefaultPage).NotTo(BeNil())
				Expect(*v.DefaultPage).To(Equal("nested/next.html"))
				Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
					_, e := tx.Page(v.ID, "read.html")
					if e != nil {
						return e
					}
					rows, e := tx.Entries(v.ContentSetID, "", 64)
					Expect(rows).To(HaveLen(6))
					return e
				})).To(Succeed())
				_, e = f.core.RejectDraft(ctx, f.token, key(GinkgoT()), g.DraftID, after.ETag)
				Expect(errors.Is(e, domain.ErrTransition)).To(BeTrue())
			}
			Expect(driver.calls.Load()).To(Equal(int32(1)))
		}()
	}
})
var _ = It("GDF03 draft list/read/history and publish use current exact scoped draft authority with no owner fallback", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	g, _ := readyGeneration(f, nil, nil)
	other, _ := readyGeneration(f, nil, nil)
	token, e := sec.Secret()
	Expect(e).NotTo(HaveOccurred())
	p := domain.WorkflowPrincipal{ID: key(GinkgoT()), BindingID: key(GinkgoT()), Active: true, Scope: domain.CredentialScope{Kind: "workflow", ProjectIDs: []string{f.project}, DraftIDs: []string{g.DraftID}, Actions: []string{"draft.write"}}}
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.PutWorkflowPrincipal(p); e != nil {
			return e
		}
		l, e := tx.Library()
		if e != nil {
			return e
		}
		scope, e := tx.Scope(f.project)
		if e != nil {
			return e
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: key(GinkgoT()), PrincipalID: p.ID, TokenHash: sec.Verifier("workflow", token), AuthEpoch: l.AuthEpoch, ConfigRevision: scope.ConfigRevision, Scope: p.Scope, CreatedAt: time.Now().UTC(), Permissions: map[string]int64{f.project: scope.Project.PermissionsRevision}})
	})).To(Succeed())
	rows, e := f.core.Drafts(ctx, token, "", "")
	Expect(e).NotTo(HaveOccurred())
	Expect(rows.Items).To(HaveLen(1))
	Expect(rows.Items[0].ID).To(Equal(g.DraftID))
	_, e = f.core.Draft(ctx, token, other.DraftID)
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue())
	_, e = f.core.DraftHistory(ctx, token, other.DraftID, 0)
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue())
	before, e := f.core.Draft(ctx, token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	_, e = f.core.PublishDraft(ctx, token, key(GinkgoT()), g.DraftID, before.ETag)
	Expect(errors.Is(e, domain.ErrDenied)).To(BeTrue())
	p.Active = false
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.PutWorkflowPrincipal(p) })).To(Succeed())
	_, e = f.core.RejectDraft(ctx, token, key(GinkgoT()), g.DraftID, before.ETag)
	Expect(e).To(HaveOccurred())
	after, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	Expect(after).To(Equal(before))
	raw, e := json.Marshal(after)
	Expect(e).NotTo(HaveOccurred())
	Expect(string(raw)).NotTo(ContainSubstring(g.AllocatedDocumentID))
})

func draftUpload(f *fixture, draft string, doc *string, text string) string {
	target := encodedTest(map[string]any{"project_id": f.project, "document_id": doc, "draft_id": draft})
	r, e := f.core.Create(ctx, f.token, key(GinkgoT()), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: target})
	Expect(e).NotTo(HaveOccurred())
	var created struct {
		ID string `json:"upload_id"`
	}
	Expect(json.Unmarshal(r.Body, &created)).To(Succeed())
	id := created.ID
	data := []byte(text)
	h := sha256.Sum256(data)
	size := int64(len(data))
	ack, e := f.core.Put(ctx, f.token, id, importer.Chunk{Path: "new/only.html", Kind: "file", Hash: hex.EncodeToString(h[:]), Length: &size, Final: true}, bytes.NewReader(data))
	Expect(e).NotTo(HaveOccurred())
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	r, e = f.core.Complete(ctx, f.token, id, key(GinkgoT()), cp)
	Expect(e).NotTo(HaveOccurred())
	Expect(f.core.Process(ctx, decoded(GinkgoT(), r)["job_id"])).To(Succeed())
	return id
}

var _ = It("GDF04 typed ready draft replacement uses exact sealed target, preserves history and publishes only the new whole candidate", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	g, driver := readyGeneration(f, nil, nil)
	other, _ := readyGeneration(f, nil, nil)
	before, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	wrong := draftUpload(f, other.DraftID, nil, "wrong draft")
	_, e = f.core.ReplaceDraft(ctx, f.token, key(GinkgoT()), g.DraftID, before.ETag, wrong, nil)
	Expect(errors.Is(e, sec.ErrInvalid)).To(BeTrue())
	plain := f.sealed(GinkgoT(), nil, "missing draft target")
	_, e = f.core.ReplaceDraft(ctx, f.token, key(GinkgoT()), g.DraftID, before.ETag, plain, nil)
	Expect(errors.Is(e, sec.ErrInvalid)).To(BeTrue())
	upload := draftUpload(f, g.DraftID, nil, "replacement candidate")
	k := key(GinkgoT())
	r, e := f.core.ReplaceDraft(ctx, f.token, k, g.DraftID, before.ETag, upload, nil)
	Expect(e).NotTo(HaveOccurred())
	after, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.State).To(Equal("ready"))
	Expect(after.Revision).To(Equal(before.Revision + 1))
	Expect(after.PayloadSHA).NotTo(Equal(before.PayloadSHA))
	Expect(after.BaseVersionID).To(Equal(before.BaseVersionID))
	replay, e := f.core.ReplaceDraft(ctx, f.token, k, g.DraftID, before.ETag, upload, nil)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(r))
	history, e := f.core.DraftHistory(ctx, f.token, g.DraftID, 0)
	Expect(e).NotTo(HaveOccurred())
	Expect(history.Items).To(HaveLen(3))
	Expect(history.Items[1].PayloadSHA).To(Equal(before.PayloadSHA))
	_, e = f.core.PublishDraft(ctx, f.token, key(GinkgoT()), g.DraftID, after.ETag)
	Expect(e).NotTo(HaveOccurred())
	published, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	Expect(published.PublishedVersionID).NotTo(BeNil())
	Expect(*published.PublishedVersionID).NotTo(Equal(g.VersionID))
	v, e := f.store.Version(ctx, *published.PublishedVersionID)
	Expect(e).NotTo(HaveOccurred())
	Expect(v.DefaultPage).To(Equal(strTest("new/only.html")))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.Entries(v.ContentSetID, "", 64)
		Expect(rows).To(HaveLen(2))
		Expect(rows[1].Path).To(Equal("new/only.html"))
		old, e2 := tx.GenerationIntent(g.JobID)
		if e2 != nil {
			return e2
		}
		Expect(old.PreparedVersion.ID).To(Equal(g.VersionID))
		retained, e2 := tx.Entries(old.ContentSetID, "", 64)
		Expect(retained).To(HaveLen(6))
		return errors.Join(e, e2)
	})).To(Succeed())
	Expect(driver.calls.Load()).To(Equal(int32(1)))
})

func strTest(v string) *string { return &v }

var _ = It("GDF05 replacing a stale-base ready candidate fences original saved-job continuation and never changes its base", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	doc, base := f.imported(GinkgoT(), f.sealed(GinkgoT(), nil, "old base"), key(GinkgoT()))
	g := generationJob(f, "publish", &doc, &base)
	r, e := f.core.Import(ctx, f.token, key(GinkgoT()), importer.ImportRequest{UploadID: f.sealed(GinkgoT(), &doc, "new base"), DocumentID: &doc, DocumentPresent: true, BaseVersionID: &base, BasePresent: true})
	Expect(e).NotTo(HaveOccurred())
	Expect(f.core.Process(ctx, decoded(GinkgoT(), r)["job_id"])).To(Succeed())
	driver := &generationDriver{data: generationArchive()}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: f.core.PrepareGeneration, Apply: f.core.ApplyGeneration}
	Expect(p.Process(ctx, g.JobID)).To(Succeed())
	j, e := f.store.Job(ctx, g.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	before, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	upload := draftUpload(f, g.DraftID, &doc, "explicit replacement")
	_, e = f.core.ReplaceDraft(ctx, f.token, key(GinkgoT()), g.DraftID, before.ETag, upload, nil)
	Expect(e).NotTo(HaveOccurred())
	after, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.BaseVersionID).To(Equal(&base))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		current, e := tx.Job(g.JobID)
		if e != nil {
			return e
		}
		Expect(current.CancelRequested).To(BeTrue())
		_, e = tx.ResumeProviderJob(current.ID, current.AttemptGeneration, "continue")
		Expect(e).To(HaveOccurred())
		return nil
	})).To(Succeed())
	Expect(p.Process(ctx, g.JobID)).To(Succeed())
	Expect(driver.calls.Load()).To(Equal(int32(1)))
	_, e = f.core.PublishDraft(ctx, f.token, key(GinkgoT()), g.DraftID, after.ETag)
	Expect(errors.Is(e, importer.ErrVersionConflict)).To(BeTrue())
	final, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	Expect(final).To(Equal(after))
	_, e = f.store.Version(ctx, g.VersionID)
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue())
})

var _ = It("GDF06 actual draft rejection during outside-writer preparation prevents late ready application and keeps the complete saved result", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	g := generationJob(f, "draft", nil, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	driver := &generationDriver{data: generationArchive()}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Apply: f.core.ApplyGeneration, Prepare: func(c context.Context, a domain.ProviderAttempt) error {
		close(entered)
		<-release
		return f.core.PrepareGeneration(c, a)
	}}
	Expect(p.ScheduleProvider(ctx, g.JobID)).To(Succeed())
	Eventually(entered, 5*time.Second).Should(BeClosed())
	before, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	_, e = f.core.RejectDraft(ctx, f.token, key(GinkgoT()), g.DraftID, before.ETag)
	Expect(e).NotTo(HaveOccurred())
	close(release)
	Eventually(func() string {
		Expect(p.PollProvider(ctx)).To(Succeed())
		j, e := f.store.Job(ctx, g.JobID)
		Expect(e).NotTo(HaveOccurred())
		return j.State
	}, 5*time.Second).Should(Equal("cancelled"))
	_ = p.StopProviders()
	after, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.State).To(Equal("rejected"))
	Expect(after.PayloadSHA).To(BeNil())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(g.JobID)
		if e != nil {
			return e
		}
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].State).To(Equal("result_saved"))
		Expect(rows[0].ResultHash).NotTo(BeEmpty())
		return nil
	})).To(Succeed())
	_, e = f.store.Version(ctx, g.VersionID)
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue())
	Expect(driver.calls.Load()).To(Equal(int32(1)))
})
var _ = It("GDF07 draft publication commit failure rolls back candidate state and version together and exact retry does not Execute", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	g, driver := readyGeneration(f, nil, nil)
	before, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	fault := errors.New("controlled draft publication commit")
	f.dbFault = func(b sqlite.Boundary) error {
		if b == sqlite.BeforePublicationCommit {
			return fault
		}
		return nil
	}
	k := key(GinkgoT())
	_, e = f.core.PublishDraft(ctx, f.token, k, g.DraftID, before.ETag)
	Expect(errors.Is(e, fault)).To(BeTrue())
	f.dbFault = nil
	after, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	Expect(after).To(Equal(before))
	_, e = f.store.Version(ctx, g.VersionID)
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue())
	_, e = f.core.PublishDraft(ctx, f.token, k, g.DraftID, before.ETag)
	Expect(e).NotTo(HaveOccurred())
	Expect(driver.calls.Load()).To(Equal(int32(1)))
})

func draftMetadata(f *fixture, draft, operation, raw string) *importer.ObjectRef {
	r, e := f.core.Create(ctx, f.token, key(GinkgoT()), importer.CreateRequest{Purpose: "metadata", InputMode: "object", Target: encodedTest(map[string]any{"action": "draft_replace", "operation_id": operation, "draft_id": draft})})
	Expect(e).NotTo(HaveOccurred())
	var created struct {
		ID string `json:"upload_id"`
	}
	Expect(json.Unmarshal(r.Body, &created)).To(Succeed())
	b := []byte(raw)
	h := sha256.Sum256(b)
	size := int64(len(b))
	ack, e := f.core.Put(ctx, f.token, created.ID, importer.Chunk{Path: "_input", Kind: "file", Hash: hex.EncodeToString(h[:]), Length: &size, Final: true}, bytes.NewReader(b))
	Expect(e).NotTo(HaveOccurred())
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	r, e = f.core.Complete(ctx, f.token, created.ID, key(GinkgoT()), cp)
	Expect(e).NotTo(HaveOccurred())
	Expect(f.core.Process(ctx, decoded(GinkgoT(), r)["job_id"])).To(Succeed())
	return &importer.ObjectRef{ID: created.ID, Hash: hex.EncodeToString(h[:]), Size: strconv.FormatInt(size, 10), MediaType: "application/json"}
}

var _ = It("GDF08 exact draft_replace metadata remains optional, validates consuming operation and survives actual store reopen with its indexed publication", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	g, driver := readyGeneration(f, nil, nil)
	other, _ := readyGeneration(f, nil, nil)
	before, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	upload := draftUpload(f, g.DraftID, nil, "new whole tree")
	raw := `{"schema_version":"1.0","document":{"title":"Replacement 日本語"},"content":{"default_page":"new/only.html"},"pages":[{"page_path":"new/only.html","label":"Replacement page"}]}`
	operation := key(GinkgoT())
	ref := draftMetadata(f, g.DraftID, operation, raw)
	_, e = f.core.ReplaceDraft(ctx, f.token, key(GinkgoT()), g.DraftID, before.ETag, upload, ref)
	Expect(errors.Is(e, sec.ErrInvalid)).To(BeTrue(), "metadata bound to a different consuming operation")
	wrong := draftMetadata(f, other.DraftID, operation, raw)
	_, e = f.core.ReplaceDraft(ctx, f.token, operation, g.DraftID, before.ETag, upload, wrong)
	Expect(errors.Is(e, sec.ErrInvalid)).To(BeTrue(), "metadata bound to a different draft")
	mismatch := *ref
	mismatch.Size = "1"
	_, e = f.core.ReplaceDraft(ctx, f.token, operation, g.DraftID, before.ETag, upload, &mismatch)
	Expect(errors.Is(e, sec.ErrInvalid)).To(BeTrue())
	response, e := f.core.ReplaceDraft(ctx, f.token, operation, g.DraftID, before.ETag, upload, ref)
	Expect(e).NotTo(HaveOccurred())
	after, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.MetadataSHA).To(Equal(&ref.Hash))
	Expect(f.closeStore()).To(Succeed())
	reopened := foundation.New(nil)
	_, closeStore, e := reopened.Prepare(ctx, f.profile)
	Expect(e).NotTo(HaveOccurred())
	f.store = reopened
	f.closeStore = closeStore
	f.auth = auth.New(reopened)
	f.core = importer.New(reopened, f.auth, reopened, staging.New())
	current, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	Expect(current).To(Equal(after))
	replay, e := f.core.ReplaceDraft(ctx, f.token, operation, g.DraftID, before.ETag, upload, ref)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(response))
	history, e := f.core.DraftHistory(ctx, f.token, g.DraftID, 0)
	Expect(e).NotTo(HaveOccurred())
	Expect(history.Items).To(HaveLen(3))
	Expect(history.Items[1].PayloadSHA).To(Equal(before.PayloadSHA))
	_, e = f.core.PublishDraft(ctx, f.token, key(GinkgoT()), g.DraftID, current.ETag)
	Expect(e).NotTo(HaveOccurred())
	published, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	v, e := f.store.Version(ctx, *published.PublishedVersionID)
	Expect(e).NotTo(HaveOccurred())
	Expect(v.Title).To(Equal("Replacement 日本語"))
	Expect(v.DefaultPage).To(Equal(strTest("new/only.html")))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		page, e := tx.Page(v.ID, "new/only.html")
		if e != nil {
			return e
		}
		Expect(string(page.Metadata)).To(ContainSubstring("Replacement page"))
		old, e := tx.GenerationIntent(g.JobID)
		if e != nil {
			return e
		}
		entries, e := tx.Entries(old.ContentSetID, "", 64)
		Expect(entries).To(HaveLen(6))
		return e
	})).To(Succeed())
	Expect(driver.calls.Load()).To(Equal(int32(1)))
})
