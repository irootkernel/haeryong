package importer_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path/filepath"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/contentstore"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	auth "haeryong.local/haeryong/internal/application/auth"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"strings"
	"syscall"
	"time"
)

var ctx = context.Background()

type fixture struct {
	store                  *foundation.Store
	core                   *importer.Service
	auth                   *auth.Service
	token, project, dbPath string
	dbFault                func(sqlite.Boundary) error
	profile                service.Profile
	closeStore             func() error
}

func newFixture(t GinkgoTInterface) *fixture {
	GinkgoHelper()
	selection := service.Selection{Profile: "test", DataDir: filepath.Join(t.TempDir(), "library")}
	{
		_, e := service.Initialize(selection)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	w, e := service.OpenWriter(selection)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	DeferCleanup(func() { w.Close() })
	{
		e := w.PrepareStorageBootstrap(nil)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	f := &fixture{dbPath: filepath.Join(selection.DataDir, "storage", "library.sqlite")}
	f.store = foundation.New(func(b sqlite.Boundary) error {
		if f.dbFault != nil {
			return f.dbFault(b)
		}
		return nil
	})
	_, closeStore, e := f.store.Prepare(ctx, w.Profile)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	f.closeStore = closeStore
	DeferCleanup(func() { f.closeStore() })
	{
		e := w.MarkStorageReady(nil)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	f.profile = w.Profile
	f.auth = auth.New(f.store)
	f.core = importer.New(f.store, f.auth, f.store, staging.New())
	h, e := f.store.Health(ctx)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	f.project = h.Library.UnfiledProjectID
	p, e := f.auth.CreatePairing(ctx, "http://127.0.0.1:9270", "labelled importer fixture", "test")
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	{

		_, e := f.auth.BootstrapDecide(ctx, domain.PairingDecision{ID: p.ID, Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: key(t), Action: "approve"})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	poll, e := f.auth.Poll(ctx, p.ID, p.PollingSecret, p.Origin)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	f.token = poll.Token
	return f
}
func key(t GinkgoTInterface) string {
	GinkgoHelper()
	id, e := service.NewID()
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	return id
}
func decoded(t GinkgoTInterface, r domain.Response) map[string]string {
	GinkgoHelper()
	var out map[string]string
	Expect(json.Unmarshal(r.Body, &out) != nil).To(BeFalse(), func() string { return fmt.Sprint("bad response") })

	return out
}
func (f *fixture) pendingSeal(t GinkgoTInterface, doc *string, content string) (string, string, domain.Checkpoint) {
	GinkgoHelper()
	target, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": doc})
	r, e := f.core.Create(ctx, f.token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "directory", SelectionName: "ordinary result", Target: target})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	var created struct {
		ID string `json:"upload_id"`
	}
	_ = json.Unmarshal(r.Body, &created)
	hash := sha256.Sum256([]byte(content))
	length := int64(len(content))
	ack, e := f.core.Put(ctx, f.token, created.ID, importer.Chunk{Path: "hello.html", Kind: "file", Hash: hex.EncodeToString(hash[:]), Length: &length, Final: true}, bytes.NewBufferString(content))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	r, e = f.core.Complete(ctx, f.token, created.ID, key(t), cp)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	job := decoded(t, r)["job_id"]
	return created.ID, job, cp
}
func (f *fixture) sealed(t GinkgoTInterface, doc *string, content string) string {
	u, j, _ := f.pendingSeal(t, doc, content)
	{
		e := f.core.Process(ctx, j)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	return u
}

func (f *fixture) imported(t GinkgoTInterface, upload, version string) (string, string) {
	GinkgoHelper()
	r, e := f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: upload, DocumentPresent: true, VersionID: version})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	job := decoded(t, r)["job_id"]
	{
		e := f.core.Process(ctx, job)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	j, e := f.store.Job(ctx, job)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	var result map[string]any
	Expect(json.Unmarshal(j.Result, &result) != nil).To(BeFalse(), func() string { return fmt.Sprint("missing actual job result") })

	return result["document_id"].(string), result["version_id"].(string)
}

type barrierStore struct {
	app.Store
	calls        int
	beforeSecond func()
}

func (s *barrierStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	s.calls++
	if s.calls == 2 {
		s.beforeSecond()
	}
	return s.Store.Write(ctx, fn)
}

var _ = It("TestImportAdmissionAbandonAndCompletedReplayOrdering", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	u := f.sealed(t, nil, "<html>admission</html>")
	wrapped := &barrierStore{Store: f.store}
	f.core.Store = wrapped
	wrapped.beforeSecond = func() {
		{
			e := f.store.Write(ctx, func(tx app.WriteTx) error { return tx.AbandonUpload(u) })
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
		}

	}
	op := key(t)
	_, e := f.core.Import(ctx, f.token, op, importer.ImportRequest{UploadID: u, DocumentPresent: true})
	Expect(!errors.Is(e, domain.ErrTransition)).To(BeFalse(), func() string { return fmt.Sprintf("abandon before acceptance must reject, got %v", e) })

	f.core.Store = f.store
	record, e := f.auth.Authenticate(ctx, f.token)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	{

		_, e := f.store.Receipt(ctx, domain.OperationKey{PrincipalID: record.PrincipalID, OperationID: op})
		Expect(!errors.Is(e, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("rejected admission retained receipt", e) })
	}

	u = f.sealed(t, nil, "<html>accepted</html>")
	op = key(t)
	request := importer.ImportRequest{UploadID: u, DocumentPresent: true}
	accepted, e := f.core.Import(ctx, f.token, op, request)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	{

		_, e := f.core.Abandon(ctx, f.token, u, key(t))
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	replayed, e := f.core.Import(ctx, f.token, op, request)
	Expect(e != nil || replayed.Status != 202 || !bytes.Equal(replayed.Body, accepted.Body)).To(BeFalse(), func() string { return fmt.Sprint("completed matching receipt lost after abandon", e) })
	{

		_, e := f.auth.Revoke(ctx, f.token, "")
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	job := decoded(t, accepted)["job_id"]
	{
		e := f.core.Process(ctx, job)
		Expect(e != nil).To(BeFalse(), func() string {
			return fmt.Sprint("accepted work depended on creator logout or later upload abandon", e)
		})
	}

	result, e := f.store.Job(ctx, job)
	Expect(e != nil || result.State != "completed").To(BeFalse(), func() string { return fmt.Sprint("accepted import did not publish", e) })

})
var _ = It("TestExplicitImmutableVersionEqualityKeepsNewBaseSeparate", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	v := key(t)
	u := f.sealed(t, nil, "<html>exact</html>")
	doc, published := f.imported(t, u, v)
	Expect(published != v).To(BeFalse(), func() string { return fmt.Sprint("version changed") })

	u = f.sealed(t, &doc, "<html>exact</html>")
	request := importer.ImportRequest{UploadID: u, DocumentPresent: true, DocumentID: &doc, VersionID: v, BasePresent: true, BaseVersionID: &v}
	response, e := f.core.Import(ctx, f.token, key(t), request)
	Expect(e != nil || response.Status != 200).To(BeFalse(), func() string {
		return fmt.Sprint("existing identical explicit version rejected by fresh base/generated defaults", e)
	})

	d, e := f.store.Document(ctx, doc)
	Expect(e != nil || d.Revision != 1+1 || d.LatestVersionID != v).To(BeFalse(), func() string { return fmt.Sprint("identical version mutated document", d, e) })

	changed := f.sealed(t, &doc, "<html>changed bytes</html>")
	request.UploadID = changed
	{
		_, e := f.core.Import(ctx, f.token, key(t), request)
		Expect(!errors.Is(e, importer.ErrImmutableConflict)).To(BeFalse(), func() string { return fmt.Sprint("changed immutable bytes not distinct conflict", e) })
	}

	request.VersionID = key(t)
	wrong := key(t)
	request.BaseVersionID = &wrong
	{
		_, e := f.core.Import(ctx, f.token, key(t), request)
		Expect(!errors.Is(e, importer.ErrVersionConflict)).To(BeFalse(), func() string { return fmt.Sprint("stale base not distinct", e) })
	}

})
var _ = It("TestUploadReceiptAndOperationRecheckCurrentSavedTarget", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	u := f.sealed(t, nil, "<html>visible</html>")
	doc, _ := f.imported(t, u, "")
	target, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": doc})
	op := key(t)
	input := importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: target}
	original, e := f.core.Create(ctx, f.token, op, input)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	// Only this disposable fixture edits lifecycle directly; product lifecycle owns
	// the future plan route. The operation itself uses production authority/SQLite.
	uri := url.URL{Scheme: "file", Path: f.dbPath}
	q := uri.Query()
	q.Set("mode", "rw")
	uri.RawQuery = q.Encode()
	raw, e := sql.Open("sqlite", uri.String())
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	defer raw.Close()
	{
		e := f.store.Write(ctx, func(tx app.WriteTx) error {
			_, e := raw.Exec("UPDATE documents SET lifecycle='trash' WHERE document_id=?", doc)
			return e
		})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}
	{

		_, e := f.core.Create(ctx, f.token, op, input)
		Expect(!errors.Is(e, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("replay disclosed hidden original upload target", original, e) })
	}
	{

		_, e := f.auth.Operation(ctx, f.token, op)
		Expect(!errors.Is(e, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("GEToperations disclosed hidden saved target", e) })
	}

})

func (f *fixture) filesFault(t GinkgoTInterface, hook func(contentstore.Boundary) error) {
	GinkgoHelper()
	objects, e := contentstore.New(filepath.Dir(f.dbPath), contentstore.WithFault(hook))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	DeferCleanup(func() { objects.Close() })
	f.store.Objects = objects
}

var _ = It("TestSealFilesystemFaultsRetainUnpublishedRetryableInput", Label("persistence"), func() {
	t := GinkgoT()

	for _, boundary := range []contentstore.Boundary{contentstore.Write, contentstore.FileSync, contentstore.FileClose, contentstore.Rename, contentstore.DirectorySync} {
		func() {
			By(string(boundary))
			f := newFixture(t)
			u, j, cp := f.pendingSeal(t, nil, "<html>durable raw input</html>")
			fired := false
			fault := syscall.EIO
			if boundary == contentstore.Rename {
				fault = syscall.EXDEV
			}
			f.filesFault(t, func(b contentstore.Boundary) error {
				if b == boundary && !fired {
					fired = true
					return fault
				}
				return nil
			})
			{
				e := f.core.Process(ctx, j)
				Expect(!errors.Is(e, fault) || !fired).To(BeFalse(), func() string { return fmt.Sprint("actual filesystem boundary fault not reached", e) })
			}

			upload, e := f.store.Upload(ctx, u)
			Expect(e != nil || upload.State != "open" || upload.Checkpoint != cp).To(BeFalse(), func() string { return fmt.Sprint("failed seal lost accepted input/open recovery", upload, e) })

			r, e := f.core.Complete(ctx, f.token, u, key(t), cp)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			retry := decoded(t, r)["job_id"]
			Expect(retry == j).To(BeFalse(), func() string { return fmt.Sprint("terminal seal attempt was reused") })
			{

				e := f.core.Process(ctx, retry)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

			doc, version := f.imported(t, u, "")
			v, e := f.store.Version(ctx, version)
			Expect(e != nil || v.DocumentID != doc).To(BeFalse(), func() string { return fmt.Sprint("retry did not publish whole version", e) })

		}()
	}
})
var _ = It("TestPublicationDatabaseCommitFaultRollsBackVersionEventAndJobResult", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	u := f.sealed(t, nil, "<html>atomic final commit</html>")
	version := key(t)
	op := key(t)
	request := importer.ImportRequest{UploadID: u, DocumentPresent: true, VersionID: version}
	accepted, e := f.core.Import(ctx, f.token, op, request)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	job := decoded(t, accepted)["job_id"]
	armed, fired := false, false
	f.dbFault = func(b sqlite.Boundary) error {
		if b == sqlite.BeforePublicationCommit {
			armed = true
		}
		if b == sqlite.BeforeCommit && armed && !fired {
			fired = true
			return syscall.ENOSPC
		}
		return nil
	}
	{
		e := f.core.Process(ctx, job)
		Expect(!errors.Is(e, syscall.ENOSPC) || !fired).To(BeFalse(), func() string { return fmt.Sprint("actual final SQLite commit fault not reached", e) })
	}

	f.dbFault = nil
	{
		_, e := f.store.Version(ctx, version)
		Expect(!errors.Is(e, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("partial version escaped failed commit", e) })
	}

	failed, e := f.store.Job(ctx, job)
	Expect(e != nil || failed.State != "failed" || len(failed.Result) != 0).To(BeFalse(), func() string { return fmt.Sprint("failed publication retained success result", failed, e) })

	var intent domain.ImportIntent
	{
		e := f.store.Write(ctx, func(tx app.WriteTx) error { var e error; intent, e = tx.ImportIntent(job); return e })
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}
	{

		_, e := f.store.Document(ctx, intent.DocumentID)
		Expect(!errors.Is(e, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("new logical document escaped failed final transaction", e) })
	}

	health, e := f.store.Health(ctx)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	events, e := f.store.Events(ctx, health.Library.StreamEpoch, 0, 64)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	for _, event := range events {
		Expect(event.Kind == "version.published").To(BeFalse(), func() string { return fmt.Sprint("publication event escaped failed final transaction") })

	}
	replay, e := f.core.Import(ctx, f.token, op, request)
	Expect(e != nil || replay.Status != 202 || !bytes.Equal(replay.Body, accepted.Body)).To(BeFalse(), func() string { return fmt.Sprint("completed job creation receipt was rewritten", e) })

	r, e := f.core.Import(ctx, f.token, key(t), request)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	{

		e := f.core.Process(ctx, decoded(t, r)["job_id"])
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	v, e := f.store.Version(ctx, version)
	Expect(e != nil || v.Sequence != 1).To(BeFalse(), func() string { return fmt.Sprint("retry did not produce exactly one first version", e) })

})
var _ = It("TestSealCancelAbandonFencesAndCheckpointKeys", Label("persistence"), func() {
	t := GinkgoT()

	for _, abandon := range []bool{false, true} {
		name := "cancel"
		if abandon {
			name = "abandon"
		}
		func() {
			By(name)
			f := newFixture(t)
			u, j, cp := f.pendingSeal(t, nil, string(bytes.Repeat([]byte("a"), 256<<10)))
			coalesced, e := f.core.Complete(ctx, f.token, u, key(t), cp)
			Expect(e != nil || decoded(t, coalesced)["job_id"] != j).To(BeFalse(), func() string { return fmt.Sprint("same checkpoint did not coalesce", e) })

			changed := cp
			changed.AcceptedBytes = "999"
			{
				_, e := f.core.Complete(ctx, f.token, u, key(t), changed)
				Expect(!errors.Is(e, domain.ErrTransition)).To(BeFalse(), func() string { return fmt.Sprint("different active checkpoint accepted", e) })
			}

			entered, release := make(chan struct{}), make(chan struct{})
			blocked := false
			f.filesFault(t, func(b contentstore.Boundary) error {
				if b == contentstore.Write && !blocked {
					blocked = true
					close(entered)
					<-release
				}
				return nil
			})
			done := make(chan error, 1)
			go func() { defer GinkgoRecover(); done <- f.core.Process(ctx, j) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				Fail(fmt.Sprint("actual FS write barrier not reached"))
			}
			if abandon {
				_, e = f.core.Abandon(ctx, f.token, u, key(t))
			} else {
				_, e = f.auth.CancelJob(ctx, f.token, j, key(t))
			}
			if e != nil {
				close(release)
				Fail(fmt.Sprint(e))
			}
			upload, e := f.store.Upload(ctx, u)
			if e != nil {
				close(release)
				Fail(fmt.Sprint(e))
			}
			if (!abandon && upload.State != "sealing") || (abandon && upload.State != "abandoned") {
				close(release)
				Fail(fmt.Sprint("writes reopened before stop confirmation", upload.State))
			}
			close(release)
			select {
			case e := <-done:
				if !errors.Is(e, domain.ErrStaleWorker) {
					Fail(fmt.Sprint("fenced worker applied or lost fence reason", e))
				}
			case <-time.After(5 * time.Second):
				Fail(fmt.Sprint("cancelled verifier did not stop"))
			}
			job, e := f.store.Job(ctx, j)
			Expect(e != nil || job.State != "cancelled").To(BeFalse(), func() string { return fmt.Sprint("stop not settled", job, e) })

			upload, e = f.store.Upload(ctx, u)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			if abandon {
				Expect(upload.State != "abandoned").To(BeFalse(), func() string { return fmt.Sprint("late worker reopened abandoned upload") })
				{

					_, e := f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: u, DocumentPresent: true})
					Expect(!errors.Is(e, domain.ErrTransition)).To(BeFalse(), func() string { return fmt.Sprint("abandoned input reused", e) })
				}

			} else {
				Expect(upload.State != "open" || upload.Checkpoint != cp).To(BeFalse(), func() string { return fmt.Sprint("cancel discarded accepted bytes/checkpoint", upload) })

				r, e := f.core.Complete(ctx, f.token, u, key(t), cp)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

				next := decoded(t, r)["job_id"]
				Expect(next == j).To(BeFalse(), func() string { return fmt.Sprint("cancelled seal resumed instead of linked new job") })
				{

					e := f.core.Process(ctx, next)
					Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
				}

			}
		}()
	}
})
var _ = It("TestTypedDraftAndAnnotationCurrentTargetScope", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	doc, version := f.imported(t, f.sealed(t, nil, "<html>anchor</html>"), "")
	draft, annotation := key(t), key(t)
	seed := func(kind, id string, value any, revision int64) {
		GinkgoHelper()
		{
			e := f.store.Write(ctx, func(tx app.WriteTx) error {
				if kind == "annotation" {
					state, e := tx.AuthState()
					if e != nil {
						return e
					}
					v := value.(map[string]any)
					page := v["page_path"].(string)
					anchor := domain.AnnotationAnchor{VersionID: v["version_id"].(string), PagePath: &page}
					n := domain.Annotation{ID: id, OwnerID: state.OwnerID, DocumentID: doc, Anchor: anchor, OriginalAnchor: anchor, Revision: revision + 1, Action: "create", ActorSession: "fixture", OperationID: key(t), CreatedAt: "2026-10-24T00:00:00Z", UpdatedAt: "2026-10-24T00:00:00Z"}
					if v["deleted"] == true {
						n.DeletedAt = &n.UpdatedAt
						n.Action = "delete"
					}
					return tx.SaveAnnotation(n)
				}
				_, e := tx.MutateResource(domain.Resource{Kind: kind, ID: id, Value: encodedTest(value)}, revision, "resource.invalidated")
				return e
			})
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
		}

	}
	seed("draft", draft, map[string]any{"project_id": f.project, "document_id": doc, "state": "ready"}, 0)
	seed("annotation", annotation, map[string]any{"version_id": version, "page_path": "hello.html", "deleted": false}, 0)
	create := func(purpose string, target any, keyID string) (domain.Response, error) {
		return f.core.Create(ctx, f.token, keyID, importer.CreateRequest{Purpose: purpose, InputMode: map[bool]string{true: "directory", false: "object"}[purpose == "result"], Target: encodedTest(target)})
	}
	op := key(t)
	r, e := create("result", map[string]any{"project_id": f.project, "document_id": doc, "draft_id": draft}, op)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("valid seeded draft scope denied", e) })

	var created struct {
		ID string `json:"upload_id"`
	}
	_ = json.Unmarshal(r.Body, &created)
	{
		_, e := create("result", map[string]any{"project_id": f.project, "document_id": nil, "draft_id": draft}, key(t))
		Expect(!errors.Is(e, domain.ErrInvalidTarget)).To(BeFalse(), func() string { return fmt.Sprint("draft document binding changed", e) })
	}
	{

		_, e := create("result", map[string]any{"project_id": f.project, "document_id": doc, "draft_id": key(t)}, key(t))
		Expect(!errors.Is(e, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("unknown draft accepted", e) })
	}
	{

		_, e := create("body", map[string]any{"operation_id": key(t), "action": "annotation_edit", "annotation_id": annotation}, key(t))
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("seeded annotation scope denied", e) })
	}
	{

		_, e := create("body", map[string]any{"operation_id": key(t), "action": "annotation_edit", "annotation_id": key(t)}, key(t))
		Expect(!errors.Is(e, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("unknown annotation accepted", e) })
	}

	secondProject := key(t)
	{
		e := f.store.Write(ctx, func(tx app.WriteTx) error {
			return tx.CreateProject(domain.Project{ID: secondProject, Name: "other explicit fixture"})
		})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	badDraft := key(t)
	seed("draft", badDraft, map[string]any{"project_id": secondProject, "document_id": doc, "state": "ready"}, 0)
	{
		_, e := create("metadata", map[string]any{"operation_id": key(t), "action": "draft_replace", "draft_id": badDraft}, key(t))
		Expect(!errors.Is(e, domain.ErrInvalidTarget)).To(BeFalse(), func() string {
			return fmt.Sprint("draft_replace accepted inconsistent current project/document binding", e)
		})
	}

	content := "<html>draft only</html>"
	sum := sha256.Sum256([]byte(content))
	length := int64(len(content))
	ack, e := f.core.Put(ctx, f.token, created.ID, importer.Chunk{Path: "draft.html", Kind: "file", Hash: hex.EncodeToString(sum[:]), Length: &length, Final: true}, bytes.NewBufferString(content))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	sealed, e := f.core.Complete(ctx, f.token, created.ID, key(t), cp)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	{

		e := f.core.Process(ctx, decoded(t, sealed)["job_id"])
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}
	{

		_, e := f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: created.ID, DocumentPresent: true, DocumentID: &doc, BasePresent: true, BaseVersionID: &version})
		Expect(!errors.Is(e, sec.ErrInvalid)).To(BeFalse(), func() string { return fmt.Sprint("draft-only staging was consumed as direct import", e) })
	}

	annotationOp := key(t)
	annotationInput := map[string]any{"operation_id": key(t), "action": "annotation_edit", "annotation_id": annotation}
	{
		_, e := create("body", annotationInput, annotationOp)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	seed("annotation", annotation, map[string]any{"version_id": version, "page_path": "hello.html", "deleted": true}, 1)
	{
		_, e := create("body", annotationInput, annotationOp)
		Expect(!errors.Is(e, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("annotation replay bypassed current hidden/deleted anchor", e) })
	}

	seed("draft", draft, map[string]any{"project_id": f.project, "document_id": doc, "state": "rejected"}, 1)
	{
		_, e := create("result", map[string]any{"project_id": f.project, "document_id": doc, "draft_id": draft}, op)
		Expect(!errors.Is(e, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("saved draft receipt bypassed current lifecycle", e) })
	}
	{

		_, e := f.auth.Operation(ctx, f.token, op)
		Expect(!errors.Is(e, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("saved target operations lookup bypassed lifecycle", e) })
	}

	_ = created
})

func encodedTest(value any) []byte { b, _ := json.Marshal(value); return b }

type repeatBytes struct{ remaining int64 }

func (r *repeatBytes) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	for i := 0; i < n; i++ {
		p[i] = 'q'
	}
	r.remaining -= int64(n)
	return n, nil
}

var _ = It("TestLargeOptionalMetadataStreamsAndRemainsImmutable", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	u := f.sealed(t, nil, "<html>optional metadata</html>")
	version := key(t)
	questionBytes := int64(20 << 20)
	sourceHash := sha256.Sum256([]byte("labelled exact optional source"))
	source := hex.EncodeToString(sourceHash[:])
	metadata := io.MultiReader(bytes.NewBufferString(`{"document":{"title":"Exact Unicode 日本語","question":"`), &repeatBytes{remaining: questionBytes}, bytes.NewBufferString(`"},"version":{"source_intent":"historical"},"content":{"default_page":"hello.html"},"pages":[{"page_path":"hello.html","label":"A page"}],"sources":[{"id":"source-一","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+source+`"}],"evidence":[{"id":"evidence-一","source_id":"source-一","capture_status":"reference_only","path":"hello.html"}],"regions":[{"id":"overview","page_path":"hello.html","evidence_ids":["evidence-一"]}],"references":[{"id":"unresolved-一","document_id":"`+key(t)+`","version_id":"`+key(t)+`","page_path":null,"region_id":null}],"generation":{"tool":"explicit fixture","provider":null}}`))
	r, e := f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: u, DocumentPresent: true, VersionID: version, Metadata: metadata})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	{

		e := f.core.Process(ctx, decoded(t, r)["job_id"])
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	v, e := f.store.Version(ctx, version)
	Expect(e != nil || v.Title != "Exact Unicode 日本語" || v.SourceBasis != "evidence_recorded" || v.ManifestSHA == "" || v.MetadataHash == "").To(BeFalse(), func() string { return fmt.Sprint("optional metadata identity/index lost", v, e) })

	var generated map[string]any
	e = f.store.Read(ctx, domain.Object{Hash: v.ManifestSHA, Size: objectSize(t, f.store, v.ManifestSHA)}, domain.Pin{Kind: "test", OwnerID: version}, func(reader io.ReadSeeker) error { return json.NewDecoder(reader).Decode(&generated) })
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	document := generated["document"].(map[string]any)
	Expect(len(document["question"].(string)) != int(questionBytes) || document["title"] != v.Title || generated["source_basis"] != "evidence_recorded").To(BeFalse(), func() string { return fmt.Sprint("generated immutable metadata changed author fields") })
	Expect(generated["version"].(map[string]any)["source_intent"] != "historical").To(BeFalse(), func() string { return fmt.Sprint("source intent lost") })
	Expect(len(generated["references"].([]any)) != 1).To(BeFalse(), func() string { return fmt.Sprint("unresolved authored relation omitted") })
	{

		_, e := f.store.Evidence(ctx, version, "evidence-一")
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}
	{

		_, e := f.store.Region(ctx, version, "hello.html", "overview")
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

})

func objectSize(t GinkgoTInterface, store *foundation.Store, hash string) int64 {
	GinkgoHelper()
	var object domain.Object
	{
		e := store.Write(ctx, func(tx app.WriteTx) error { var e error; object, e = tx.Object(hash); return e })
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	return object.Size
}

var _ = It("TestImportDefaultSelectionNameIsSemanticOnlyWhenUsed", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	named := func(name string, document *string) string {
		GinkgoHelper()
		target, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": document})
		r, err := f.core.Create(ctx, f.token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "directory", SelectionName: name, Target: target})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		var created struct {
			ID string `json:"upload_id"`
		}
		{
			err = json.Unmarshal(r.Body, &created)
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

		upload := created.ID
		content := []byte("<html>identical payload</html>")
		hash := sha256.Sum256(content)
		length := int64(len(content))
		ack, err := f.core.Put(ctx, f.token, upload, importer.Chunk{Path: "hello.html", Kind: "file", Hash: hex.EncodeToString(hash[:]), Length: &length, Final: true}, bytes.NewReader(content))
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		cp := ack.Checkpoint
		cp.Generation++
		cp.Complete = true
		r, err = f.core.Complete(ctx, f.token, upload, key(t), cp)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		{

			err = f.core.Process(ctx, decoded(t, r)["job_id"])
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

		return upload
	}
	a, same, changed := named("name A", nil), named("name A", nil), named("name B", nil)
	operation := key(t)
	request := importer.ImportRequest{UploadID: a, DocumentPresent: true}
	first, err := f.core.Import(ctx, f.token, operation, request)
	Expect(err != nil || first.Status != 202).To(BeFalse(), func() string { return fmt.Sprint(first, err) })

	request.UploadID = same
	replay, err := f.core.Import(ctx, f.token, operation, request)
	Expect(err != nil || !bytes.Equal(first.Body, replay.Body)).To(BeFalse(), func() string { return fmt.Sprint("equivalent named upload failed replay", err) })

	request.UploadID = changed
	{
		_, err = f.core.Import(ctx, f.token, operation, request)
		Expect(!errors.Is(err, domain.ErrConflict)).To(BeFalse(), func() string {
			return fmt.Sprintf("changed selection name used for generated title must conflict: %v", err)
		})
	}

	// An explicit authored title makes the transport selection name irrelevant.
	operation = key(t)
	metadata := `{"schema_version":"1.0","document":{"title":"explicit title"}}`
	request = importer.ImportRequest{UploadID: a, DocumentPresent: true, Metadata: bytes.NewBufferString(metadata)}
	first, err = f.core.Import(ctx, f.token, operation, request)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	request.UploadID, request.Metadata = changed, bytes.NewBufferString(metadata)
	replay, err = f.core.Import(ctx, f.token, operation, request)
	Expect(err != nil || !bytes.Equal(first.Body, replay.Body)).To(BeFalse(), func() string { return fmt.Sprint("unused selection name changed explicit-title identity", err) })

	// Existing-document defaults use the document title, not the selected name.
	document, version := f.imported(t, same, "")
	a, changed = named("existing A", &document), named("existing B", &document)
	operation = key(t)
	request = importer.ImportRequest{UploadID: a, DocumentPresent: true, DocumentID: &document, BasePresent: true, BaseVersionID: &version}
	first, err = f.core.Import(ctx, f.token, operation, request)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	request.UploadID = changed
	replay, err = f.core.Import(ctx, f.token, operation, request)
	Expect(err != nil || !bytes.Equal(first.Body, replay.Body)).To(BeFalse(), func() string { return fmt.Sprint("unused selection name changed existing-document identity", err) })

})

type postPutBytes struct {
	importer.Bytes
	after func()
}

func (b *postPutBytes) Put(ctx context.Context, r io.Reader, commit func(app.WriteTx, domain.Object) error) (domain.Object, error) {
	o, err := b.Bytes.Put(ctx, r, commit)
	if err == nil && b.after != nil {
		fn := b.after
		b.after = nil
		fn()
	}
	return o, err
}
func pinCount(t GinkgoTInterface, f *fixture, kind, owner string) int {
	GinkgoHelper()
	u := url.URL{Scheme: "file", Path: f.dbPath}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	defer db.Close()
	var n int
	{
		err = db.QueryRow("SELECT count(*) FROM object_pins WHERE owner_kind=? AND owner_id=?", kind, owner).Scan(&n)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	return n
}

var _ = It("TestInlineMetadataPinCleanupSurvivesRevocationAfterDurablePut", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	u := f.sealed(t, nil, "<html>pin lifetime</html>")
	op := key(t)
	f.core.Bytes = &postPutBytes{Bytes: f.store, after: func() {
		{
			_, err := f.auth.Revoke(ctx, f.token, "")
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

	}}
	_, err := f.core.Import(ctx, f.token, op, importer.ImportRequest{UploadID: u, DocumentPresent: true, Metadata: bytes.NewBufferString(`{"document":{"title":"metadata"}}`)})
	Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("revoked request accepted") })
	{

		n := pinCount(t, f, "import", op)
		Expect(n != 0).To(BeFalse(), func() string { return fmt.Sprintf("request-owned metadata pins leaked after revocation: %d", n) })
	}

})
var _ = It("TestQueuedImportCancellationReleasesOnlyStoppedJobPins", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	u := f.sealed(t, nil, "<html>cancel pins</html>")
	r, err := f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: u, DocumentPresent: true, Metadata: bytes.NewBufferString(`{"document":{"title":"cancel metadata"}}`)})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	job := decoded(t, r)["job_id"]
	{
		n := pinCount(t, f, "job", job)
		Expect(n == 0).To(BeFalse(), func() string { return fmt.Sprint("accepted import owns no pins") })
	}
	{

		err = f.store.Write(ctx, func(tx app.WriteTx) error {
			index, e := tx.MetadataIndex(job)
			if e != nil {
				return e
			}
			return tx.AcquirePin(domain.Pin{ID: "cancel-backup", Kind: "backup", OwnerID: "keep-backup", Hash: index.Hash})
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		_, err = f.auth.CancelJob(ctx, f.token, job, key(t))
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	j, err := f.store.Job(ctx, job)
	Expect(err != nil || j.State != "cancelled").To(BeFalse(), func() string { return fmt.Sprint(j, err) })
	{

		n := pinCount(t, f, "job", job)
		Expect(n != 0).To(BeFalse(), func() string { return fmt.Sprintf("stop-confirmed import retains owned pins: %d", n) })
	}
	{

		n := pinCount(t, f, "backup", "keep-backup")
		Expect(n != 1).To(BeFalse(), func() string { return fmt.Sprint("cancel released independent backup pin", n) })
	}

})

type scopeFailureStore struct {
	app.Store
	failure error
}
type scopeFailureTx struct {
	app.WriteTx
	failure error
}

func (s *scopeFailureStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error { return fn(&scopeFailureTx{WriteTx: tx, failure: s.failure}) })
}
func (t *scopeFailureTx) Scope(string) (domain.ScopeState, error) {
	return domain.ScopeState{}, t.failure
}

var _ = It("TestUploadDiscoveryPropagatesOperationalAuthorityFailure", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	f.sealed(t, nil, "<html>list</html>")
	f.core.Store = &scopeFailureStore{Store: f.store, failure: syscall.EIO}
	{
		_, err := f.core.Uploads(ctx, f.token, importer.UploadQuery{})
		Expect(!errors.Is(err, syscall.EIO)).To(BeFalse(), func() string { return fmt.Sprintf("authority storage failure hidden as list success: %v", err) })
	}

	f.core.Store = &scopeFailureStore{Store: f.store, failure: domain.ErrNotFound}
	{
		page, err := f.core.Uploads(ctx, f.token, importer.UploadQuery{})
		Expect(err != nil || len(page.Items) != 0).To(BeFalse(), func() string { return fmt.Sprint("known hidden target was not filtered", page, err) })
	}

})
var _ = It("TestMetadataSemanticValidationIsSharedByPreviewAndAdmission", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	u := f.sealed(t, nil, "<html>metadata validation</html>")
	cases := map[string]string{
		"uuid":                `{"document":{"id":"not-a-uuid"}}`,
		"git_identity":        `{"sources":[{"id":"s","kind":"git","retention_mode":"reference_only"}]}`,
		"snapshot_identity":   `{"sources":[{"id":"s","kind":"snapshot","retention_mode":"reference_only"}]}`,
		"selected_snapshot":   `{"sources":[{"id":"s","kind":"snapshot","retention_mode":"selected_snapshot","snapshot_sha256":"` + strings.Repeat("a", 64) + `","asset_path":"hello.html"}]}`,
		"bounded_range":       `{"sources":[{"id":"s","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"` + strings.Repeat("a", 64) + `"}],"evidence":[{"id":"e","source_id":"s","capture_status":"reference_only","path":"hello.html","line_start":9,"line_end":2}]}`,
		"paired_range":        `{"sources":[{"id":"s","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"` + strings.Repeat("a", 64) + `"}],"evidence":[{"id":"e","source_id":"s","capture_status":"reference_only","path":"hello.html","line_start":1}]}`,
		"duplicate_id":        `{"sources":[{"id":"s","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"` + strings.Repeat("a", 64) + `"},{"id":"s","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"` + strings.Repeat("a", 64) + `"}]}`,
		"unknown_source":      `{"evidence":[{"id":"e","source_id":"missing","capture_status":"reference_only","path":"hello.html"}]}`,
		"region_without_page": `{"references":[{"id":"r","document_id":"` + key(t) + `","version_id":"` + key(t) + `","page_path":null,"region_id":"region"}]}`,
	}
	for name, metadata := range cases {
		func() {
			By(name)
			{
				_, err := f.core.Validate(ctx, f.token, "", importer.ImportRequest{UploadID: u, Metadata: bytes.NewBufferString(metadata)})
				Expect(!errors.Is(err, importer.ErrPackageInvalid)).To(BeFalse(), func() string { return fmt.Sprintf("preview accepted invalid semantic metadata: %v", err) })
			}

			op := key(t)
			{
				_, err := f.core.Import(ctx, f.token, op, importer.ImportRequest{UploadID: u, DocumentPresent: true, Metadata: bytes.NewBufferString(metadata)})
				Expect(!errors.Is(err, importer.ErrPackageInvalid)).To(BeFalse(), func() string { return fmt.Sprintf("admission accepted invalid semantic metadata: %v", err) })
			}

		}()
	}
})

func (f *fixture) reopen(t GinkgoTInterface) {
	GinkgoHelper()
	{
		err := f.closeStore()
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	_, closeStore, err := f.store.Prepare(ctx, f.profile)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	f.closeStore = closeStore
}

var _ = It("TestMetadataPreparationRestartReclaimsOnlyDeadCallOwnership", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	u := f.sealed(t, nil, "<html>namespace restart</html>")
	response, err := f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: u, DocumentPresent: true, Metadata: bytes.NewBufferString(`{"document":{"title":"linked index"}}`)})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	job := decoded(t, response)["job_id"]
	unlinked := key(t)
	err = f.store.Write(ctx, func(tx app.WriteTx) error {
		index, e := tx.MetadataIndex(job)
		if e != nil {
			return e
		}
		if e = tx.CreateMetadataIndex(domain.MetadataIndex{ID: unlinked, PrincipalID: index.PrincipalID, Hash: index.Hash, ContentSetID: index.ContentSetID}); e != nil {
			return e
		}
		if e = tx.PutImportMetadata(unlinked, "sources", "partial", []byte(`{"id":"partial","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+strings.Repeat("a", 64)+`"}`)); e != nil {
			return e
		}
		for _, p := range []domain.Pin{{ID: "dead-read", Kind: "import_prepare", OwnerID: unlinked, Hash: index.Hash}, {ID: "metadata-request:" + key(t), Kind: "import", OwnerID: "dead-call", Hash: index.Hash}, {ID: "other-owner", Kind: "backup", OwnerID: "independent", Hash: index.Hash}} {
			if e = tx.AcquirePin(p); e != nil {
				return e
			}
		}
		return nil
	})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	f.reopen(t)
	err = f.store.Write(ctx, func(tx app.WriteTx) error {
		{
			_, e := tx.MetadataIndex(unlinked)
			Expect(!errors.Is(e, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprintf("dead-call index survived: %v", e) })
		}

		rows, e := tx.ImportMetadata(unlinked, "sources", "", 64)
		Expect(e != nil || len(rows) != 0).To(BeFalse(), func() string { return fmt.Sprint("partial dead-call rows survived cleanup", rows, e) })

		index, e := tx.MetadataIndex(job)
		Expect(e != nil || !index.Complete || index.JobID != job).To(BeFalse(), func() string { return fmt.Sprint("linked accepted index changed", index, e) })

		return nil
	})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	Expect(pinCount(t, f, "import_prepare", unlinked) != 0 || pinCount(t, f, "metadata_prepare", unlinked) != 0 || pinCount(t, f, "import", "dead-call") != 0).To(BeFalse(), func() string { return fmt.Sprint("dead call pins survived restart") })
	Expect(pinCount(t, f, "backup", "independent") != 1 || pinCount(t, f, "job", job) == 0).To(BeFalse(), func() string { return fmt.Sprint("restart reclaimed independent or accepted ownership") })
	{

		err = f.core.Process(ctx, job)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint("linked index could not publish after restart", err) })
	}

})
var _ = It("TestInterruptedImportCancelRecoveryReleasesOwnedIndexAndPins", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	u := f.sealed(t, nil, "<html>cancel recovery</html>")
	response, err := f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: u, DocumentPresent: true, Metadata: bytes.NewBufferString(`{"document":{"title":"cancel recovery"}}`)})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	job := decoded(t, response)["job_id"]
	{
		err = f.store.Write(ctx, func(tx app.WriteTx) error { _, e := tx.ClaimJob(job); return e })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		_, err = f.auth.CancelJob(ctx, f.token, job, key(t))
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	Expect(pinCount(t, f, "job", job) == 0).To(BeFalse(), func() string { return fmt.Sprint("unconfirmed cancellation released active pins") })

	f.reopen(t)
	{
		err = f.core.Process(ctx, job)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	j, err := f.store.Job(ctx, job)
	Expect(err != nil || j.State != "cancelled").To(BeFalse(), func() string { return fmt.Sprint(j, err) })
	Expect(pinCount(t, f, "job", job) != 0).To(BeFalse(), func() string { return fmt.Sprint("confirmed recovered cancellation leaked pins") })
	{

		err = f.store.Write(ctx, func(tx app.WriteTx) error {
			_, e := tx.MetadataIndex(job)
			if !errors.Is(e, domain.ErrNotFound) {
				Fail(fmt.Sprint("cancelled index remained", e))
			}
			return nil
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

})
var _ = It("TestActualCompetingPublicationAndOnceCapturedBasePreserveReplay", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	document, base := f.imported(t, f.sealed(t, nil, "<html>base</html>"), "")
	publish := func(upload, base string) (string, string) {
		GinkgoHelper()
		op, version := key(t), key(t)
		r, e := f.core.Import(ctx, f.token, op, importer.ImportRequest{UploadID: upload, DocumentPresent: true, DocumentID: &document, BasePresent: true, BaseVersionID: &base, VersionID: version})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

		job := decoded(t, r)["job_id"]
		{
			e = f.core.Process(ctx, job)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
		}

		return op, version
	}
	staged := f.sealed(t, &document, "<html>pre-transfer base intent</html>")
	_, winner := publish(f.sealed(t, &document, "<html>first competitor</html>"), base)
	staleOperation := key(t)
	{
		_, e := f.core.Import(ctx, f.token, staleOperation, importer.ImportRequest{UploadID: staged, DocumentPresent: true, DocumentID: &document, BasePresent: true, BaseVersionID: &base})
		Expect(!errors.Is(e, importer.ErrVersionConflict)).To(BeFalse(), func() string { return fmt.Sprint("actual competing publication did not reject original base", e) })
	}

	record, e := f.auth.Authenticate(ctx, f.token)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	{

		_, e = f.store.Receipt(ctx, domain.OperationKey{PrincipalID: record.PrincipalID, OperationID: staleOperation})
		Expect(!errors.Is(e, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("stale admission left durable effect", e) })
	}

	operation := key(t)
	request := importer.ImportRequest{UploadID: staged, DocumentPresent: true, DocumentID: &document}
	accepted, e := f.core.Import(ctx, f.token, operation, request)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	job := decoded(t, accepted)["job_id"]
	receipt, e := f.store.Receipt(ctx, domain.OperationKey{PrincipalID: record.PrincipalID, OperationID: operation})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	var normalization struct {
		Base string `json:"base_version_id"`
	}
	{
		e = json.Unmarshal(receipt.Normalization, &normalization)
		Expect(e != nil || normalization.Base != winner).To(BeFalse(), func() string { return fmt.Sprint("omitted base not captured at reservation", normalization, e) })
	}

	_, current := publish(f.sealed(t, &document, "<html>second competitor</html>"), winner)
	replay, e := f.core.Import(ctx, f.token, operation, request)
	Expect(e != nil || !bytes.Equal(accepted.Body, replay.Body)).To(BeFalse(), func() string { return fmt.Sprint("old accepted response lost to new latest", e) })
	{

		e = f.core.Process(ctx, job)
		Expect(!errors.Is(e, importer.ErrVersionConflict)).To(BeFalse(), func() string { return fmt.Sprint("omitted base recaptured at worker rather than reservation", e) })
	}

	before, e := f.store.Document(ctx, document)
	Expect(e != nil || before.LatestVersionID != current).To(BeFalse(), func() string { return fmt.Sprint(before, e) })

	upload := f.sealed(t, &document, "<html>successful replay</html>")
	op, version := key(t), key(t)
	request = importer.ImportRequest{UploadID: upload, DocumentPresent: true, DocumentID: &document, BasePresent: true, BaseVersionID: &current, VersionID: version}
	accepted, e = f.core.Import(ctx, f.token, op, request)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	{

		e = f.core.Process(ctx, decoded(t, accepted)["job_id"])
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	before, e = f.store.Document(ctx, document)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	replay, e = f.core.Import(ctx, f.token, op, request)
	Expect(e != nil || !bytes.Equal(accepted.Body, replay.Body)).To(BeFalse(), func() string { return fmt.Sprint("successful same-key original now-stale base failed replay", e) })

	after, e := f.store.Document(ctx, document)
	Expect(e != nil || after.Revision != before.Revision || after.LatestVersionID != version).To(BeFalse(), func() string { return fmt.Sprint("replay published again", after, e) })

})

type interruptAfterEntryCodec struct {
	importer.Codec
	stop context.CancelFunc
}

func (c interruptAfterEntryCodec) Expand(ctx context.Context, r io.ReaderAt, size int64, emit func(importer.EntryInput, io.Reader) error) error {
	return c.Codec.Expand(ctx, r, size, func(entry importer.EntryInput, input io.Reader) error {
		if e := emit(entry, input); e != nil {
			return e
		}
		c.stop()
		return nil
	})
}

var _ = It("TestInterruptedSealCancellationReclaimsOnlyStoppedPartialOwnership", Label("persistence"), func() {
	t := GinkgoT()

	for _, abandon := range []bool{false, true} {
		name := "cancel"
		if abandon {
			name = "abandon"
		}
		func() {
			By(name)
			f := newFixture(t)
			var archive bytes.Buffer
			z := zip.NewWriter(&archive)
			for _, name := range []string{"first.html", "second.html"} {
				w, e := z.Create(name)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
				{

					_, e = w.Write([]byte("<html>" + name + "</html>"))
					Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
				}

			}
			{
				e := z.Close()
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

			target, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": nil})
			response, e := f.core.Create(ctx, f.token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "archive", SelectionName: "interrupted.zip", Target: target})
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			var created struct {
				ID string `json:"upload_id"`
			}
			{
				e = json.Unmarshal(response.Body, &created)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

			upload := created.ID
			sum := sha256.Sum256(archive.Bytes())
			length := int64(archive.Len())
			ack, e := f.core.Put(ctx, f.token, upload, importer.Chunk{Path: "_input", Kind: "file", Hash: hex.EncodeToString(sum[:]), Length: &length, Final: true}, bytes.NewReader(archive.Bytes()))
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			cp := ack.Checkpoint
			cp.Generation++
			cp.Complete = true
			response, e = f.core.Complete(ctx, f.token, upload, key(t), cp)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			job := decoded(t, response)["job_id"]
			workCtx, stop := context.WithCancel(ctx)
			f.core.Codec = interruptAfterEntryCodec{Codec: staging.New(), stop: stop}
			{
				e = f.core.Process(workCtx, job)
				Expect(!errors.Is(e, context.Canceled)).To(BeFalse(), func() string { return fmt.Sprint("actual worker interruption not observed", e) })
			}

			f.core.Codec = staging.New()
			partial, e := f.store.Entries(ctx, job, "", 64)
			Expect(e != nil || len(partial) != 1).To(BeFalse(), func() string {
				return fmt.Sprint("actual interrupted seal did not retain durable first entry", partial, e)
			})
			Expect(pinCount(t, f, "job", job) == 0).To(BeFalse(), func() string { return fmt.Sprint("archive input job pin missing before confirmed stop") })
			{

				e = f.store.Write(ctx, func(tx app.WriteTx) error {
					return tx.AcquirePin(domain.Pin{ID: "retained-seal-backup", Kind: "backup", OwnerID: "keep-recovered-seal", Hash: partial[0].Hash})
				})
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

			if abandon {
				_, e = f.core.Abandon(ctx, f.token, upload, key(t))
			} else {
				_, e = f.auth.CancelJob(ctx, f.token, job, key(t))
			}
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			f.reopen(t)
			{
				e = f.core.Process(ctx, job)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

			partial, e = f.store.Entries(ctx, job, "", 64)
			Expect(e != nil || len(partial) != 0).To(BeFalse(), func() string {
				return fmt.Sprintf("stop-confirmed recovered seal retained partial content refs: %d %v", len(partial), e)
			})
			{

				n := pinCount(t, f, "job", job)
				Expect(n != 0).To(BeFalse(), func() string { return fmt.Sprintf("recovered cancelled seal retained owned job pins: %d", n) })
			}
			Expect(pinCount(t, f, "backup", "keep-recovered-seal") != 1).To(BeFalse(), func() string { return fmt.Sprint("stop cleanup released independent backup") })

			u, e := f.store.Upload(ctx, upload)
			Expect(e != nil || u.Checkpoint != cp).To(BeFalse(), func() string { return fmt.Sprint("raw confirmed input changed", u, e) })

			if abandon {
				Expect(u.State != "abandoned").To(BeFalse(), func() string { return fmt.Sprint("abandoned input reopened", u) })

			} else {
				Expect(u.State != "open" || pinCount(t, f, "upload", upload) == 0).To(BeFalse(), func() string { return fmt.Sprint("cancel discarded resumable raw input", u) })

				response, e = f.core.Complete(ctx, f.token, upload, key(t), cp)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

				next := decoded(t, response)["job_id"]
				Expect(next == job).To(BeFalse(), func() string { return fmt.Sprint("terminal seal reused") })
				{

					e = f.core.Process(ctx, next)
					Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("retained raw input did not reseal", e) })
				}

			}
		}()
	}
})
