package ownerrecords_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/application/auth"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/recovery"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// Synthetic F09/F13 boundary fixture. Real-source/browser/native proof has its
// own executable owners; direct internal state is explicitly persistence evidence.
var ctx = context.Background()

func id() string       { v, e := service.NewID(); Expect(e).NotTo(HaveOccurred()); return v }
func raw(v any) []byte { b, e := json.Marshal(v); Expect(e).NotTo(HaveOccurred()); return b }
func field(r domain.Response, key string) string {
	var value map[string]any
	Expect(json.Unmarshal(r.Body, &value)).To(Succeed())
	return value[key].(string)
}

type world struct {
	selection service.Selection
	writer    *service.LockedProfile
	store     *foundation.Store
	closeDB   func() error
	auth      *auth.Service
	query     *query.Service
	imports   *importer.Service
	token     string
}

func (w *world) open() {
	var e error
	w.writer, e = service.OpenWriter(w.selection)
	Expect(e).NotTo(HaveOccurred())
	Expect(w.writer.PrepareStorageBootstrap(nil)).To(Succeed())
	w.store = foundation.New(nil)
	_, w.closeDB, e = w.store.Prepare(ctx, w.writer.Profile)
	Expect(e).NotTo(HaveOccurred())
	Expect(w.writer.MarkStorageReady(nil)).To(Succeed())
	w.auth = auth.New(w.store)
	w.query = query.New(w.store, w.auth, w.store)
	w.imports = importer.New(w.store, w.auth, w.store, staging.New())
}
func (w *world) close() {
	if w.closeDB != nil {
		Expect(w.closeDB()).To(Succeed())
		w.closeDB = nil
	}
	if w.writer != nil {
		Expect(w.writer.Close()).To(Succeed())
		w.writer = nil
	}
}
func setup() *world {
	w := &world{selection: service.Selection{Profile: "owner-records", DataDir: filepath.Join(GinkgoT().TempDir(), "library")}}
	_, e := service.Initialize(w.selection)
	Expect(e).NotTo(HaveOccurred())
	w.open()
	DeferCleanup(w.close)
	p, e := w.auth.CreatePairing(ctx, "http://127.0.0.1:9270", "synthetic owner", "test")
	Expect(e).NotTo(HaveOccurred())
	_, e = w.auth.BootstrapDecide(ctx, domain.PairingDecision{ID: p.ID, Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: id(), Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	poll, e := w.auth.Poll(ctx, p.ID, p.PollingSecret, p.Origin)
	Expect(e).NotTo(HaveOccurred())
	w.token = poll.Token
	return w
}
func (w *world) publish(project string, prior *domain.Version, files map[string]string) domain.Version {
	var document, base *string
	if prior != nil {
		document = &prior.DocumentID
		base = &prior.ID
	}
	r, e := w.imports.Create(ctx, w.token, id(), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: raw(map[string]any{"project_id": project, "document_id": document})})
	Expect(e).NotTo(HaveOccurred())
	upload := field(r, "upload_id")
	var ack importer.ChunkResult
	for path, body := range files {
		n := int64(len(body))
		digest := sha256.Sum256([]byte(body))
		ack, e = w.imports.Put(ctx, w.token, upload, importer.Chunk{Path: path, Kind: "file", Hash: hex.EncodeToString(digest[:]), Length: &n, Final: true}, bytes.NewBufferString(body))
		Expect(e).NotTo(HaveOccurred())
	}
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	r, e = w.imports.Complete(ctx, w.token, upload, id(), cp)
	Expect(e).NotTo(HaveOccurred())
	Expect(w.imports.Process(ctx, field(r, "job_id"))).To(Succeed())
	r, e = w.imports.Import(ctx, w.token, id(), importer.ImportRequest{UploadID: upload, ProjectID: project, DocumentID: document, DocumentPresent: true, BaseVersionID: base, BasePresent: true})
	Expect(e).NotTo(HaveOccurred())
	Expect(w.imports.Process(ctx, field(r, "job_id"))).To(Succeed())
	j, e := w.store.Job(ctx, field(r, "job_id"))
	Expect(e).NotTo(HaveOccurred())
	var result struct {
		Version string `json:"version_id"`
	}
	Expect(json.Unmarshal(j.Result, &result)).To(Succeed())
	v, e := w.store.Version(ctx, result.Version)
	Expect(e).NotTo(HaveOccurred())
	return v
}
func (w *world) project() string {
	p := id()
	Expect(w.store.Write(ctx, func(tx app.WriteTx) error {
		return tx.CreateProject(domain.Project{ID: p, Name: "synthetic Task6 project"})
	})).To(Succeed())
	return p
}
func (w *world) note(version, page string, region *string, text string) query.AnnotationView {
	var path *string
	if page != "" {
		path = &page
	}
	q, f := "", ""
	if path != nil {
		q, f = "q=+%2F", "fragment/ +"
	}
	r, e := w.query.AnnotationMutate(ctx, w.token, id(), "POST", version, "", raw(map[string]any{"page_path": path, "region_id": region, "page_query": q, "page_fragment": f, "body": text, "body_ref": nil, "selected_text": "fallback", "context": "retained original context", "context_ref": nil}))
	Expect(e).NotTo(HaveOccurred())
	var v query.AnnotationView
	Expect(json.Unmarshal(r.Body, &v)).To(Succeed())
	return v
}

var _ = It("OwnerRecords C07 C08 exact tuples and old history survive publication and physical reopen", Label("persistence"), func() {
	w := setup()
	p := w.project()
	v1 := w.publish(p, nil, map[string]string{"a.html": "<p id='retry'>one</p>", "b.html": "<p id='retry'>two</p>", "plain.html": "<h1>plain</h1>"})
	region := "retry"
	notes := []query.AnnotationView{w.note(v1.ID, "a.html", &region, "one"), w.note(v1.ID, "b.html", &region, "two"), w.note(v1.ID, "plain.html", nil, "plain"), w.note(v1.ID, "", nil, "whole")}
	for i, created := range notes {
		// Mutation and history carry operation_id; GET does not invent one.
		h, e := w.query.AnnotationHistory(ctx, w.token, created.AnnotationID, query.Options{})
		Expect(e).NotTo(HaveOccurred())
		Expect(h.Items).To(HaveLen(1))
		Expect(h.Items[0].OperationID).To(Equal(created.OperationID))
		created.OperationID = ""
		current, e := w.query.AnnotationGet(ctx, w.token, created.AnnotationID)
		Expect(e).NotTo(HaveOccurred())
		Expect(current).To(Equal(created))
		notes[i] = current
	}
	r, e := w.query.ReviewPut(ctx, w.token, id(), v1.ID, "", true, raw(map[string]any{"action": "complete", "state": "reviewed"}))
	Expect(e).NotTo(HaveOccurred())
	Expect(r.Status).To(Equal(200))
	history, e := w.query.ReviewHistory(ctx, w.token, v1.ID, query.Options{})
	Expect(e).NotTo(HaveOccurred())
	v2 := w.publish(p, &v1, map[string]string{"a.html": "<p id='retry'>changed</p><p id='retry'>ambiguous</p>"})
	for _, n := range notes {
		saved, e := w.query.AnnotationGet(ctx, w.token, n.AnnotationID)
		Expect(e).NotTo(HaveOccurred())
		Expect(saved).To(Equal(n))
	}
	w.close()
	w.open()
	for _, n := range notes {
		saved, e := w.query.AnnotationGet(ctx, w.token, n.AnnotationID)
		Expect(e).NotTo(HaveOccurred())
		Expect(saved).To(Equal(n))
		var content bytes.Buffer
		Expect(w.query.ReadAnnotationObject(ctx, w.token, n.AnnotationID, 1, "context", func(_ domain.Object, _ string, r io.ReadSeeker) error { _, e := io.Copy(&content, r); return e })).To(Succeed())
		Expect(content.String()).To(Equal("retained original context"))
	}
	newNotes, e := w.query.AnnotationList(ctx, w.token, v2.ID, query.Options{})
	Expect(e).NotTo(HaveOccurred())
	Expect(newNotes.Items).To(BeEmpty())
	review, e := w.query.ReviewGet(ctx, w.token, v2.ID)
	Expect(e).NotTo(HaveOccurred())
	Expect(review.Exists).To(BeFalse())
	after, e := w.query.ReviewHistory(ctx, w.token, v1.ID, query.Options{})
	Expect(e).NotTo(HaveOccurred())
	Expect(after).To(Equal(history))
	_, e = w.query.AnnotationMutate(ctx, w.token, id(), "PATCH", notes[0].AnnotationID, notes[0].ETag, raw(map[string]any{"action": "reattach", "anchor": domain.AnnotationAnchor{VersionID: v2.ID, PagePath: notes[0].Anchor.PagePath, RegionID: &region}, "selected_text": nil, "context": nil, "context_ref": nil}))
	Expect(e).To(HaveOccurred())
})

var _ = It("OwnerRecords stale edit after real copy capture cannot overwrite and snapshot context remains exact", Label("persistence"), func() {
	w := setup()
	v := w.publish(w.project(), nil, map[string]string{"a.html": "<p>copy capture</p>"})
	note := w.note(v.ID, "a.html", nil, "snapshot body")
	core := recovery.New(w.store, w.auth, w.store)
	r, e := core.Submit(ctx, w.token, id(), domain.CopyRequest{Action: "backup.create", Destination: filepath.Join(GinkgoT().TempDir(), "backups")})
	Expect(e).NotTo(HaveOccurred())
	var accepted domain.CopyResult
	Expect(json.Unmarshal(r.Body, &accepted)).To(Succeed())
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	w.store.CopyFault = func(phase string) error {
		if phase == "snapshot" {
			close(entered)
			<-release
		}
		return nil
	}
	finished := make(chan error, 1)
	go func() { finished <- w.store.ProcessCopy(ctx, accepted.JobID) }()
	joined := false
	DeferCleanup(func() {
		once.Do(func() { close(release) })
		if !joined {
			Eventually(finished, 30*time.Second).Should(Receive())
		}
	})
	Eventually(entered, 30*time.Second).Should(BeClosed())
	body := raw(map[string]any{"action": "edit", "body": "after snapshot", "body_ref": nil, "selected_text": nil, "context": nil, "context_ref": note.ContextRef})
	key := id()
	first, e := w.query.AnnotationMutate(ctx, w.token, key, "PATCH", note.AnnotationID, note.ETag, body)
	Expect(e).NotTo(HaveOccurred())
	replay, e := w.query.AnnotationMutate(ctx, w.token, key, "PATCH", note.AnnotationID, note.ETag, body)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(first))
	_, e = w.query.AnnotationMutate(ctx, w.token, id(), "PATCH", note.AnnotationID, note.ETag, body)
	Expect(errors.Is(e, domain.ErrPrecondition)).To(BeTrue())
	once.Do(func() { close(release) })
	Expect(<-finished).To(Succeed())
	joined = true
	w.store.CopyFault = nil
	for turn := 0; turn < 100; turn++ {
		j, e := w.store.Job(ctx, accepted.JobID)
		Expect(e).NotTo(HaveOccurred())
		if j.State == "completed" {
			break
		}
		Expect(w.store.ProcessCopy(ctx, accepted.JobID)).To(Succeed())
	}
	j, e := w.store.Job(ctx, accepted.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	var set domain.CopySet
	Expect(w.store.Write(ctx, func(tx app.WriteTx) error { var e error; set, e = tx.CopySet(accepted.ID); return e })).To(Succeed())
	_, e = foundation.VerifyBackup(ctx, set.Directory)
	Expect(e).NotTo(HaveOccurred())
	h, e := w.query.AnnotationHistory(ctx, w.token, note.AnnotationID, query.Options{})
	Expect(e).NotTo(HaveOccurred())
	Expect(h.Items).To(HaveLen(2))
	Expect(h.Items[0].Body).To(Equal(note.Body))
	Expect(h.Items[0].ContextRef).To(Equal(note.ContextRef))
})
