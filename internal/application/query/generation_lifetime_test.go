package query_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

func generationLifetimeDirectory(f *fixture) domain.Version {
	t := GinkgoT()
	target, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": nil})
	r, e := f.core.Create(ctx, f.token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: target})
	Expect(e).NotTo(HaveOccurred())
	var upload struct {
		ID string `json:"upload_id"`
	}
	Expect(json.Unmarshal(r.Body, &upload)).To(Succeed())
	var ack importer.ChunkResult
	for n := 0; n < 80; n++ {
		body := fmt.Sprintf("<html>complete page %d</html>", n)
		length := int64(len(body))
		ack, e = f.core.Put(ctx, f.token, upload.ID, importer.Chunk{Path: fmt.Sprintf("pages/%03d.html", n), Kind: "file", Length: &length, Final: true, Hash: hash(body)}, strings.NewReader(body))
		Expect(e).NotTo(HaveOccurred())
	}
	ack, e = f.core.Put(ctx, f.token, upload.ID, importer.Chunk{Path: "empty", Kind: "directory"}, strings.NewReader(`{}`))
	Expect(e).NotTo(HaveOccurred())
	ack, e = f.core.Put(ctx, f.token, upload.ID, importer.Chunk{Path: "alias", Kind: "symlink", LinkTarget: "pages/000.html"}, strings.NewReader(`{"link_target":"pages/000.html"}`))
	Expect(e).NotTo(HaveOccurred())
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	r, e = f.core.Complete(ctx, f.token, upload.ID, key(t), cp)
	Expect(e).NotTo(HaveOccurred())
	var accepted struct {
		JobID string `json:"job_id"`
	}
	Expect(json.Unmarshal(r.Body, &accepted)).To(Succeed())
	Expect(f.core.Process(ctx, accepted.JobID)).To(Succeed())
	r, e = f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: upload.ID, DocumentPresent: true, VersionID: key(t)})
	Expect(e).NotTo(HaveOccurred())
	Expect(json.Unmarshal(r.Body, &accepted)).To(Succeed())
	Expect(f.core.Process(ctx, accepted.JobID)).To(Succeed())
	j, e := f.store.Job(ctx, accepted.JobID)
	Expect(e).NotTo(HaveOccurred())
	var result struct {
		DocumentID string `json:"document_id"`
		VersionID  string `json:"version_id"`
	}
	Expect(json.Unmarshal(j.Result, &result)).To(Succeed())
	f.document, f.version = result.DocumentID, result.VersionID
	v, e := f.store.Version(ctx, f.version)
	Expect(e).NotTo(HaveOccurred())
	return v
}

func generationInventory(f *fixture, set string) []domain.Entry {
	out := []domain.Entry{}
	after := ""
	for {
		rows, e := f.store.Entries(ctx, set, after, 64)
		Expect(e).NotTo(HaveOccurred())
		if len(rows) == 0 {
			return out
		}
		out = append(out, rows...)
		after = rows[len(rows)-1].Path
	}
}

var _ = It("GGEN04 real upload abandon and batched document purge preserve current and replaced draft inventory through restart", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	small, e := f.store.Version(ctx, f.version)
	Expect(e).NotTo(HaveOccurred())
	large := generationLifetimeDirectory(f)
	before := generationInventory(f, large.ContentSetID)
	Expect(len(before)).To(BeNumerically(">", 64))
	d := domain.Draft{ID: key(GinkgoT()), ProjectID: f.project, DocumentID: &f.document, BaseVersionID: &f.version, JobID: key(GinkgoT()), AllocatedDocumentID: f.document, ProposedVersionID: key(GinkgoT()), State: "preparing", Intent: "draft", CreatedAt: "2026-10-30T12:00:00Z"}
	// Typed unpublished-candidate fixture over actually imported complete bytes.
	// This tests destructive consumers, not provider generation or admission.
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.CreateJob(domain.Job{ID: d.JobID, Kind: "generation"}); e != nil {
			return e
		}
		if e := tx.CreateDraft(d); e != nil {
			return e
		}
		d.State, d.ContentSetID, d.PayloadSHA, d.ManifestSHA = "ready", large.ContentSetID, large.PayloadSHA, large.ManifestSHA
		return tx.UpdateDraft(d, 1)
	})).To(Succeed())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.DiscardContentSet(large.ContentSetID) })).To(MatchError(domain.ErrPinned))
	abandonPurgeUploads(f)
	Expect(generationInventory(f, large.ContentSetID)).To(Equal(before))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		var e error
		d, e = tx.Draft(d.ID)
		if e != nil {
			return e
		}
		d.ContentSetID, d.PayloadSHA, d.ManifestSHA = small.ContentSetID, small.PayloadSHA, small.ManifestSHA
		d.ProposedVersionID = key(GinkgoT())
		return tx.UpdateDraft(d, d.Revision)
	})).To(Succeed())
	_, accepted := purgeAccept(f)
	clock := time.Now()
	purger := &work.Purger{Store: f.store, Bytes: f.store, Now: func() time.Time { return clock }}
	job := purgeTurns(f, purger, accepted.JobID, 220)
	Expect(job.State).To(Equal("paused"))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		intent, e := tx.PurgeIntent(accepted.JobID)
		if e != nil {
			return e
		}
		Expect(intent.MetadataRemoved).To(BeTrue())
		Expect(intent.RetainedSets).To(ContainElement(large.ContentSetID))
		return nil
	})).To(Succeed())
	Expect(generationInventory(f, large.ContentSetID)).To(Equal(before), "history owns paths, empty directories and links before every purge batch")
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.DiscardContentSet(large.ContentSetID) })).To(MatchError(domain.ErrPinned))
	Expect(f.store.Objects.Close()).To(Succeed())
	Expect(f.store.Close()).To(Succeed())
	profile, e := service.Load(service.Selection{Profile: "test", DataDir: f.profileDir})
	Expect(e).NotTo(HaveOccurred())
	reopened := foundation.New(nil)
	_, closeStore, e := reopened.Prepare(ctx, profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeStore()
	f.store, purger.Store, purger.Bytes = reopened, reopened, reopened
	clock = clock.Add(time.Minute)
	job = purgeTurns(f, purger, accepted.JobID, 220)
	Expect(job.State).To(Equal("paused"))
	Expect(generationInventory(f, large.ContentSetID)).To(Equal(before))
	Expect(reopened.Write(ctx, func(tx app.WriteTx) error {
		history, e := tx.DraftHistory(d.ID, 0, 64)
		if e != nil {
			return e
		}
		Expect(history).To(HaveLen(3))
		Expect(history[1].ContentSetID).To(Equal(large.ContentSetID))
		Expect(history[2].ContentSetID).To(Equal(small.ContentSetID))
		return nil
	})).To(Succeed())
})
