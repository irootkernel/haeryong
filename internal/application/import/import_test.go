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
	"testing"

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

func newFixture(t *testing.T) *fixture {
	t.Helper()
	selection := service.Selection{Profile: "test", DataDir: filepath.Join(t.TempDir(), "library")}
	if _, e := service.Initialize(selection); e != nil {
		t.Fatal(e)
	}
	w, e := service.OpenWriter(selection)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { w.Close() })
	if e := w.PrepareStorageBootstrap(nil); e != nil {
		t.Fatal(e)
	}
	f := &fixture{dbPath: filepath.Join(selection.DataDir, "storage", "library.sqlite")}
	f.store = foundation.New(func(b sqlite.Boundary) error {
		if f.dbFault != nil {
			return f.dbFault(b)
		}
		return nil
	})
	_, closeStore, e := f.store.Prepare(ctx, w.Profile)
	if e != nil {
		t.Fatal(e)
	}
	f.closeStore = closeStore
	t.Cleanup(func() { f.closeStore() })
	if e := w.MarkStorageReady(nil); e != nil {
		t.Fatal(e)
	}
	f.profile = w.Profile
	f.auth = auth.New(f.store)
	f.core = importer.New(f.store, f.auth, f.store, staging.New())
	h, e := f.store.Health(ctx)
	if e != nil {
		t.Fatal(e)
	}
	f.project = h.Library.UnfiledProjectID
	p, e := f.auth.CreatePairing(ctx, "http://127.0.0.1:9270", "labelled importer fixture", "test")
	if e != nil {
		t.Fatal(e)
	}
	if _, e := f.auth.BootstrapDecide(ctx, domain.PairingDecision{ID: p.ID, Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: key(t), Action: "approve"}); e != nil {
		t.Fatal(e)
	}
	poll, e := f.auth.Poll(ctx, p.ID, p.PollingSecret, p.Origin)
	if e != nil {
		t.Fatal(e)
	}
	f.token = poll.Token
	return f
}
func key(t *testing.T) string {
	t.Helper()
	id, e := service.NewID()
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func decoded(t *testing.T, r domain.Response) map[string]string {
	t.Helper()
	var out map[string]string
	if json.Unmarshal(r.Body, &out) != nil {
		t.Fatal("bad response")
	}
	return out
}
func (f *fixture) pendingSeal(t *testing.T, doc *string, content string) (string, string, domain.Checkpoint) {
	t.Helper()
	target, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": doc})
	r, e := f.core.Create(ctx, f.token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "directory", SelectionName: "ordinary result", Target: target})
	if e != nil {
		t.Fatal(e)
	}
	var created struct {
		ID string `json:"upload_id"`
	}
	_ = json.Unmarshal(r.Body, &created)
	hash := sha256.Sum256([]byte(content))
	length := int64(len(content))
	ack, e := f.core.Put(ctx, f.token, created.ID, importer.Chunk{Path: "hello.html", Kind: "file", Hash: hex.EncodeToString(hash[:]), Length: &length, Final: true}, bytes.NewBufferString(content))
	if e != nil {
		t.Fatal(e)
	}
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	r, e = f.core.Complete(ctx, f.token, created.ID, key(t), cp)
	if e != nil {
		t.Fatal(e)
	}
	job := decoded(t, r)["job_id"]
	return created.ID, job, cp
}
func (f *fixture) sealed(t *testing.T, doc *string, content string) string {
	u, j, _ := f.pendingSeal(t, doc, content)
	if e := f.core.Process(ctx, j); e != nil {
		t.Fatal(e)
	}
	return u
}

func (f *fixture) imported(t *testing.T, upload, version string) (string, string) {
	t.Helper()
	r, e := f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: upload, DocumentPresent: true, VersionID: version})
	if e != nil {
		t.Fatal(e)
	}
	job := decoded(t, r)["job_id"]
	if e := f.core.Process(ctx, job); e != nil {
		t.Fatal(e)
	}
	j, e := f.store.Job(ctx, job)
	if e != nil {
		t.Fatal(e)
	}
	var result map[string]any
	if json.Unmarshal(j.Result, &result) != nil {
		t.Fatal("missing actual job result")
	}
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
func TestImportAdmissionAbandonAndCompletedReplayOrdering(t *testing.T) {
	f := newFixture(t)
	u := f.sealed(t, nil, "<html>admission</html>")
	wrapped := &barrierStore{Store: f.store}
	f.core.Store = wrapped
	wrapped.beforeSecond = func() {
		if e := f.store.Write(ctx, func(tx app.WriteTx) error { return tx.AbandonUpload(u) }); e != nil {
			t.Fatal(e)
		}
	}
	op := key(t)
	_, e := f.core.Import(ctx, f.token, op, importer.ImportRequest{UploadID: u, DocumentPresent: true})
	if !errors.Is(e, domain.ErrTransition) {
		t.Fatalf("abandon before acceptance must reject, got %v", e)
	}
	f.core.Store = f.store
	record, e := f.auth.Authenticate(ctx, f.token)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := f.store.Receipt(ctx, domain.OperationKey{PrincipalID: record.PrincipalID, OperationID: op}); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("rejected admission retained receipt", e)
	}
	u = f.sealed(t, nil, "<html>accepted</html>")
	op = key(t)
	request := importer.ImportRequest{UploadID: u, DocumentPresent: true}
	accepted, e := f.core.Import(ctx, f.token, op, request)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := f.core.Abandon(ctx, f.token, u, key(t)); e != nil {
		t.Fatal(e)
	}
	replayed, e := f.core.Import(ctx, f.token, op, request)
	if e != nil || replayed.Status != 202 || !bytes.Equal(replayed.Body, accepted.Body) {
		t.Fatal("completed matching receipt lost after abandon", e)
	}
	if _, e := f.auth.Revoke(ctx, f.token, ""); e != nil {
		t.Fatal(e)
	}
	job := decoded(t, accepted)["job_id"]
	if e := f.core.Process(ctx, job); e != nil {
		t.Fatal("accepted work depended on creator logout or later upload abandon", e)
	}
	result, e := f.store.Job(ctx, job)
	if e != nil || result.State != "completed" {
		t.Fatal("accepted import did not publish", e)
	}
}
func TestExplicitImmutableVersionEqualityKeepsNewBaseSeparate(t *testing.T) {
	f := newFixture(t)
	v := key(t)
	u := f.sealed(t, nil, "<html>exact</html>")
	doc, published := f.imported(t, u, v)
	if published != v {
		t.Fatal("version changed")
	}
	u = f.sealed(t, &doc, "<html>exact</html>")
	request := importer.ImportRequest{UploadID: u, DocumentPresent: true, DocumentID: &doc, VersionID: v, BasePresent: true, BaseVersionID: &v}
	response, e := f.core.Import(ctx, f.token, key(t), request)
	if e != nil || response.Status != 200 {
		t.Fatal("existing identical explicit version rejected by fresh base/generated defaults", e)
	}
	d, e := f.store.Document(ctx, doc)
	if e != nil || d.Revision != 1+1 || d.LatestVersionID != v {
		t.Fatal("identical version mutated document", d, e)
	}
	changed := f.sealed(t, &doc, "<html>changed bytes</html>")
	request.UploadID = changed
	if _, e := f.core.Import(ctx, f.token, key(t), request); !errors.Is(e, importer.ErrImmutableConflict) {
		t.Fatal("changed immutable bytes not distinct conflict", e)
	}
	request.VersionID = key(t)
	wrong := key(t)
	request.BaseVersionID = &wrong
	if _, e := f.core.Import(ctx, f.token, key(t), request); !errors.Is(e, importer.ErrVersionConflict) {
		t.Fatal("stale base not distinct", e)
	}
}
func TestUploadReceiptAndOperationRecheckCurrentSavedTarget(t *testing.T) {
	f := newFixture(t)
	u := f.sealed(t, nil, "<html>visible</html>")
	doc, _ := f.imported(t, u, "")
	target, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": doc})
	op := key(t)
	input := importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: target}
	original, e := f.core.Create(ctx, f.token, op, input)
	if e != nil {
		t.Fatal(e)
	}
	// Only this disposable fixture edits lifecycle directly; product lifecycle owns
	// the future plan route. The operation itself uses production authority/SQLite.
	uri := url.URL{Scheme: "file", Path: f.dbPath}
	q := uri.Query()
	q.Set("mode", "rw")
	uri.RawQuery = q.Encode()
	raw, e := sql.Open("sqlite", uri.String())
	if e != nil {
		t.Fatal(e)
	}
	defer raw.Close()
	if e := f.store.Write(ctx, func(tx app.WriteTx) error {
		_, e := raw.Exec("UPDATE documents SET lifecycle='trash' WHERE document_id=?", doc)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := f.core.Create(ctx, f.token, op, input); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("replay disclosed hidden original upload target", original, e)
	}
	if _, e := f.auth.Operation(ctx, f.token, op); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("GEToperations disclosed hidden saved target", e)
	}
}

func (f *fixture) filesFault(t *testing.T, hook func(contentstore.Boundary) error) {
	t.Helper()
	objects, e := contentstore.New(filepath.Dir(f.dbPath), contentstore.WithFault(hook))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { objects.Close() })
	f.store.Objects = objects
}
func TestSealFilesystemFaultsRetainUnpublishedRetryableInput(t *testing.T) {
	for _, boundary := range []contentstore.Boundary{contentstore.Write, contentstore.FileSync, contentstore.FileClose, contentstore.Rename, contentstore.DirectorySync} {
		t.Run(string(boundary), func(t *testing.T) {
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
			if e := f.core.Process(ctx, j); !errors.Is(e, fault) || !fired {
				t.Fatal("actual filesystem boundary fault not reached", e)
			}
			upload, e := f.store.Upload(ctx, u)
			if e != nil || upload.State != "open" || upload.Checkpoint != cp {
				t.Fatal("failed seal lost accepted input/open recovery", upload, e)
			}
			r, e := f.core.Complete(ctx, f.token, u, key(t), cp)
			if e != nil {
				t.Fatal(e)
			}
			retry := decoded(t, r)["job_id"]
			if retry == j {
				t.Fatal("terminal seal attempt was reused")
			}
			if e := f.core.Process(ctx, retry); e != nil {
				t.Fatal(e)
			}
			doc, version := f.imported(t, u, "")
			v, e := f.store.Version(ctx, version)
			if e != nil || v.DocumentID != doc {
				t.Fatal("retry did not publish whole version", e)
			}
		})
	}
}
func TestPublicationDatabaseCommitFaultRollsBackVersionEventAndJobResult(t *testing.T) {
	f := newFixture(t)
	u := f.sealed(t, nil, "<html>atomic final commit</html>")
	version := key(t)
	op := key(t)
	request := importer.ImportRequest{UploadID: u, DocumentPresent: true, VersionID: version}
	accepted, e := f.core.Import(ctx, f.token, op, request)
	if e != nil {
		t.Fatal(e)
	}
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
	if e := f.core.Process(ctx, job); !errors.Is(e, syscall.ENOSPC) || !fired {
		t.Fatal("actual final SQLite commit fault not reached", e)
	}
	f.dbFault = nil
	if _, e := f.store.Version(ctx, version); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("partial version escaped failed commit", e)
	}
	failed, e := f.store.Job(ctx, job)
	if e != nil || failed.State != "failed" || len(failed.Result) != 0 {
		t.Fatal("failed publication retained success result", failed, e)
	}
	var intent domain.ImportIntent
	if e := f.store.Write(ctx, func(tx app.WriteTx) error { var e error; intent, e = tx.ImportIntent(job); return e }); e != nil {
		t.Fatal(e)
	}
	if _, e := f.store.Document(ctx, intent.DocumentID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("new logical document escaped failed final transaction", e)
	}
	health, e := f.store.Health(ctx)
	if e != nil {
		t.Fatal(e)
	}
	events, e := f.store.Events(ctx, health.Library.StreamEpoch, 0, 64)
	if e != nil {
		t.Fatal(e)
	}
	for _, event := range events {
		if event.Kind == "version.published" {
			t.Fatal("publication event escaped failed final transaction")
		}
	}
	replay, e := f.core.Import(ctx, f.token, op, request)
	if e != nil || replay.Status != 202 || !bytes.Equal(replay.Body, accepted.Body) {
		t.Fatal("completed job creation receipt was rewritten", e)
	}
	r, e := f.core.Import(ctx, f.token, key(t), request)
	if e != nil {
		t.Fatal(e)
	}
	if e := f.core.Process(ctx, decoded(t, r)["job_id"]); e != nil {
		t.Fatal(e)
	}
	v, e := f.store.Version(ctx, version)
	if e != nil || v.Sequence != 1 {
		t.Fatal("retry did not produce exactly one first version", e)
	}
}
func TestSealCancelAbandonFencesAndCheckpointKeys(t *testing.T) {
	for _, abandon := range []bool{false, true} {
		name := "cancel"
		if abandon {
			name = "abandon"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			u, j, cp := f.pendingSeal(t, nil, string(bytes.Repeat([]byte("a"), 256<<10)))
			coalesced, e := f.core.Complete(ctx, f.token, u, key(t), cp)
			if e != nil || decoded(t, coalesced)["job_id"] != j {
				t.Fatal("same checkpoint did not coalesce", e)
			}
			changed := cp
			changed.AcceptedBytes = "999"
			if _, e := f.core.Complete(ctx, f.token, u, key(t), changed); !errors.Is(e, domain.ErrTransition) {
				t.Fatal("different active checkpoint accepted", e)
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
			go func() { done <- f.core.Process(ctx, j) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("actual FS write barrier not reached")
			}
			if abandon {
				_, e = f.core.Abandon(ctx, f.token, u, key(t))
			} else {
				_, e = f.auth.CancelJob(ctx, f.token, j, key(t))
			}
			if e != nil {
				close(release)
				t.Fatal(e)
			}
			upload, e := f.store.Upload(ctx, u)
			if e != nil {
				close(release)
				t.Fatal(e)
			}
			if (!abandon && upload.State != "sealing") || (abandon && upload.State != "abandoned") {
				close(release)
				t.Fatal("writes reopened before stop confirmation", upload.State)
			}
			close(release)
			select {
			case e := <-done:
				if !errors.Is(e, domain.ErrStaleWorker) {
					t.Fatal("fenced worker applied or lost fence reason", e)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled verifier did not stop")
			}
			job, e := f.store.Job(ctx, j)
			if e != nil || job.State != "cancelled" {
				t.Fatal("stop not settled", job, e)
			}
			upload, e = f.store.Upload(ctx, u)
			if e != nil {
				t.Fatal(e)
			}
			if abandon {
				if upload.State != "abandoned" {
					t.Fatal("late worker reopened abandoned upload")
				}
				if _, e := f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: u, DocumentPresent: true}); !errors.Is(e, domain.ErrTransition) {
					t.Fatal("abandoned input reused", e)
				}
			} else {
				if upload.State != "open" || upload.Checkpoint != cp {
					t.Fatal("cancel discarded accepted bytes/checkpoint", upload)
				}
				r, e := f.core.Complete(ctx, f.token, u, key(t), cp)
				if e != nil {
					t.Fatal(e)
				}
				next := decoded(t, r)["job_id"]
				if next == j {
					t.Fatal("cancelled seal resumed instead of linked new job")
				}
				if e := f.core.Process(ctx, next); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}
func TestTypedDraftAndAnnotationCurrentTargetScope(t *testing.T) {
	f := newFixture(t)
	doc, version := f.imported(t, f.sealed(t, nil, "<html>anchor</html>"), "")
	draft, annotation := key(t), key(t)
	seed := func(kind, id string, value any, revision int64) {
		t.Helper()
		if e := f.store.Write(ctx, func(tx app.WriteTx) error {
			_, e := tx.MutateResource(domain.Resource{Kind: kind, ID: id, Value: encodedTest(value)}, revision, "resource.invalidated")
			return e
		}); e != nil {
			t.Fatal(e)
		}
	}
	seed("draft", draft, map[string]any{"project_id": f.project, "document_id": doc, "state": "ready"}, 0)
	seed("annotation", annotation, map[string]any{"version_id": version, "page_path": "hello.html", "deleted": false}, 0)
	create := func(purpose string, target any, keyID string) (domain.Response, error) {
		return f.core.Create(ctx, f.token, keyID, importer.CreateRequest{Purpose: purpose, InputMode: map[bool]string{true: "directory", false: "object"}[purpose == "result"], Target: encodedTest(target)})
	}
	op := key(t)
	r, e := create("result", map[string]any{"project_id": f.project, "document_id": doc, "draft_id": draft}, op)
	if e != nil {
		t.Fatal("valid seeded draft scope denied", e)
	}
	var created struct {
		ID string `json:"upload_id"`
	}
	_ = json.Unmarshal(r.Body, &created)
	if _, e := create("result", map[string]any{"project_id": f.project, "document_id": nil, "draft_id": draft}, key(t)); !errors.Is(e, domain.ErrInvalidTarget) {
		t.Fatal("draft document binding changed", e)
	}
	if _, e := create("result", map[string]any{"project_id": f.project, "document_id": doc, "draft_id": key(t)}, key(t)); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("unknown draft accepted", e)
	}
	if _, e := create("body", map[string]any{"operation_id": key(t), "action": "annotation_edit", "annotation_id": annotation}, key(t)); e != nil {
		t.Fatal("seeded annotation scope denied", e)
	}
	if _, e := create("body", map[string]any{"operation_id": key(t), "action": "annotation_edit", "annotation_id": key(t)}, key(t)); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("unknown annotation accepted", e)
	}
	secondProject := key(t)
	if e := f.store.Write(ctx, func(tx app.WriteTx) error {
		return tx.CreateProject(domain.Project{ID: secondProject, Name: "other explicit fixture"})
	}); e != nil {
		t.Fatal(e)
	}
	badDraft := key(t)
	seed("draft", badDraft, map[string]any{"project_id": secondProject, "document_id": doc, "state": "ready"}, 0)
	if _, e := create("metadata", map[string]any{"operation_id": key(t), "action": "draft_replace", "draft_id": badDraft}, key(t)); !errors.Is(e, domain.ErrInvalidTarget) {
		t.Fatal("draft_replace accepted inconsistent current project/document binding", e)
	}
	content := "<html>draft only</html>"
	sum := sha256.Sum256([]byte(content))
	length := int64(len(content))
	ack, e := f.core.Put(ctx, f.token, created.ID, importer.Chunk{Path: "draft.html", Kind: "file", Hash: hex.EncodeToString(sum[:]), Length: &length, Final: true}, bytes.NewBufferString(content))
	if e != nil {
		t.Fatal(e)
	}
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	sealed, e := f.core.Complete(ctx, f.token, created.ID, key(t), cp)
	if e != nil {
		t.Fatal(e)
	}
	if e := f.core.Process(ctx, decoded(t, sealed)["job_id"]); e != nil {
		t.Fatal(e)
	}
	if _, e := f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: created.ID, DocumentPresent: true, DocumentID: &doc, BasePresent: true, BaseVersionID: &version}); !errors.Is(e, sec.ErrInvalid) {
		t.Fatal("draft-only staging was consumed as direct import", e)
	}
	annotationOp := key(t)
	annotationInput := map[string]any{"operation_id": key(t), "action": "annotation_edit", "annotation_id": annotation}
	if _, e := create("body", annotationInput, annotationOp); e != nil {
		t.Fatal(e)
	}
	seed("annotation", annotation, map[string]any{"version_id": version, "page_path": "hello.html", "deleted": true}, 1)
	if _, e := create("body", annotationInput, annotationOp); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("annotation replay bypassed current hidden/deleted anchor", e)
	}
	seed("draft", draft, map[string]any{"project_id": f.project, "document_id": doc, "state": "rejected"}, 1)
	if _, e := create("result", map[string]any{"project_id": f.project, "document_id": doc, "draft_id": draft}, op); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("saved draft receipt bypassed current lifecycle", e)
	}
	if _, e := f.auth.Operation(ctx, f.token, op); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("saved target operations lookup bypassed lifecycle", e)
	}
	_ = created
}
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
func TestLargeOptionalMetadataStreamsAndRemainsImmutable(t *testing.T) {
	f := newFixture(t)
	u := f.sealed(t, nil, "<html>optional metadata</html>")
	version := key(t)
	questionBytes := int64(20 << 20)
	sourceHash := sha256.Sum256([]byte("labelled exact optional source"))
	source := hex.EncodeToString(sourceHash[:])
	metadata := io.MultiReader(bytes.NewBufferString(`{"document":{"title":"Exact Unicode 日本語","question":"`), &repeatBytes{remaining: questionBytes}, bytes.NewBufferString(`"},"version":{"source_intent":"historical"},"content":{"default_page":"hello.html"},"pages":[{"page_path":"hello.html","label":"A page"}],"sources":[{"id":"source-一","kind":"snapshot","retention_mode":"reference_only","snapshot_sha256":"`+source+`"}],"evidence":[{"id":"evidence-一","source_id":"source-一","capture_status":"reference_only","path":"hello.html"}],"regions":[{"id":"overview","page_path":"hello.html","evidence_ids":["evidence-一"]}],"references":[{"id":"unresolved-一","document_id":"`+key(t)+`","version_id":"`+key(t)+`","page_path":null,"region_id":null}],"generation":{"tool":"explicit fixture","provider":null}}`))
	r, e := f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: u, DocumentPresent: true, VersionID: version, Metadata: metadata})
	if e != nil {
		t.Fatal(e)
	}
	if e := f.core.Process(ctx, decoded(t, r)["job_id"]); e != nil {
		t.Fatal(e)
	}
	v, e := f.store.Version(ctx, version)
	if e != nil || v.Title != "Exact Unicode 日本語" || v.SourceBasis != "evidence_recorded" || v.ManifestSHA == "" || v.MetadataHash == "" {
		t.Fatal("optional metadata identity/index lost", v, e)
	}
	var generated map[string]any
	e = f.store.Read(ctx, domain.Object{Hash: v.ManifestSHA, Size: objectSize(t, f.store, v.ManifestSHA)}, domain.Pin{Kind: "test", OwnerID: version}, func(reader io.ReadSeeker) error { return json.NewDecoder(reader).Decode(&generated) })
	if e != nil {
		t.Fatal(e)
	}
	document := generated["document"].(map[string]any)
	if len(document["question"].(string)) != int(questionBytes) || document["title"] != v.Title || generated["source_basis"] != "evidence_recorded" {
		t.Fatal("generated immutable metadata changed author fields")
	}
	if generated["version"].(map[string]any)["source_intent"] != "historical" {
		t.Fatal("source intent lost")
	}
	if len(generated["references"].([]any)) != 1 {
		t.Fatal("unresolved authored relation omitted")
	}
	if _, e := f.store.Evidence(ctx, version, "evidence-一"); e != nil {
		t.Fatal(e)
	}
	if _, e := f.store.Region(ctx, version, "hello.html", "overview"); e != nil {
		t.Fatal(e)
	}
}
func objectSize(t *testing.T, store *foundation.Store, hash string) int64 {
	t.Helper()
	var object domain.Object
	if e := store.Write(ctx, func(tx app.WriteTx) error { var e error; object, e = tx.Object(hash); return e }); e != nil {
		t.Fatal(e)
	}
	return object.Size
}

func TestImportDefaultSelectionNameIsSemanticOnlyWhenUsed(t *testing.T) {
	f := newFixture(t)
	named := func(name string, document *string) string {
		t.Helper()
		target, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": document})
		r, err := f.core.Create(ctx, f.token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "directory", SelectionName: name, Target: target})
		if err != nil {
			t.Fatal(err)
		}
		var created struct {
			ID string `json:"upload_id"`
		}
		if err = json.Unmarshal(r.Body, &created); err != nil {
			t.Fatal(err)
		}
		upload := created.ID
		content := []byte("<html>identical payload</html>")
		hash := sha256.Sum256(content)
		length := int64(len(content))
		ack, err := f.core.Put(ctx, f.token, upload, importer.Chunk{Path: "hello.html", Kind: "file", Hash: hex.EncodeToString(hash[:]), Length: &length, Final: true}, bytes.NewReader(content))
		if err != nil {
			t.Fatal(err)
		}
		cp := ack.Checkpoint
		cp.Generation++
		cp.Complete = true
		r, err = f.core.Complete(ctx, f.token, upload, key(t), cp)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.core.Process(ctx, decoded(t, r)["job_id"]); err != nil {
			t.Fatal(err)
		}
		return upload
	}
	a, same, changed := named("name A", nil), named("name A", nil), named("name B", nil)
	operation := key(t)
	request := importer.ImportRequest{UploadID: a, DocumentPresent: true}
	first, err := f.core.Import(ctx, f.token, operation, request)
	if err != nil || first.Status != 202 {
		t.Fatal(first, err)
	}
	request.UploadID = same
	replay, err := f.core.Import(ctx, f.token, operation, request)
	if err != nil || !bytes.Equal(first.Body, replay.Body) {
		t.Fatal("equivalent named upload failed replay", err)
	}
	request.UploadID = changed
	if _, err = f.core.Import(ctx, f.token, operation, request); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("changed selection name used for generated title must conflict: %v", err)
	}

	// An explicit authored title makes the transport selection name irrelevant.
	operation = key(t)
	metadata := `{"schema_version":"1.0","document":{"title":"explicit title"}}`
	request = importer.ImportRequest{UploadID: a, DocumentPresent: true, Metadata: bytes.NewBufferString(metadata)}
	first, err = f.core.Import(ctx, f.token, operation, request)
	if err != nil {
		t.Fatal(err)
	}
	request.UploadID, request.Metadata = changed, bytes.NewBufferString(metadata)
	replay, err = f.core.Import(ctx, f.token, operation, request)
	if err != nil || !bytes.Equal(first.Body, replay.Body) {
		t.Fatal("unused selection name changed explicit-title identity", err)
	}

	// Existing-document defaults use the document title, not the selected name.
	document, version := f.imported(t, same, "")
	a, changed = named("existing A", &document), named("existing B", &document)
	operation = key(t)
	request = importer.ImportRequest{UploadID: a, DocumentPresent: true, DocumentID: &document, BasePresent: true, BaseVersionID: &version}
	first, err = f.core.Import(ctx, f.token, operation, request)
	if err != nil {
		t.Fatal(err)
	}
	request.UploadID = changed
	replay, err = f.core.Import(ctx, f.token, operation, request)
	if err != nil || !bytes.Equal(first.Body, replay.Body) {
		t.Fatal("unused selection name changed existing-document identity", err)
	}
}

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
func pinCount(t *testing.T, f *fixture, kind, owner string) int {
	t.Helper()
	u := url.URL{Scheme: "file", Path: f.dbPath}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err = db.QueryRow("SELECT count(*) FROM object_pins WHERE owner_kind=? AND owner_id=?", kind, owner).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestInlineMetadataPinCleanupSurvivesRevocationAfterDurablePut(t *testing.T) {
	f := newFixture(t)
	u := f.sealed(t, nil, "<html>pin lifetime</html>")
	op := key(t)
	f.core.Bytes = &postPutBytes{Bytes: f.store, after: func() {
		if _, err := f.auth.Revoke(ctx, f.token, ""); err != nil {
			t.Fatal(err)
		}
	}}
	_, err := f.core.Import(ctx, f.token, op, importer.ImportRequest{UploadID: u, DocumentPresent: true, Metadata: bytes.NewBufferString(`{"document":{"title":"metadata"}}`)})
	if err == nil {
		t.Fatal("revoked request accepted")
	}
	if n := pinCount(t, f, "import", op); n != 0 {
		t.Fatalf("request-owned metadata pins leaked after revocation: %d", n)
	}
}
func TestQueuedImportCancellationReleasesOnlyStoppedJobPins(t *testing.T) {
	f := newFixture(t)
	u := f.sealed(t, nil, "<html>cancel pins</html>")
	r, err := f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: u, DocumentPresent: true, Metadata: bytes.NewBufferString(`{"document":{"title":"cancel metadata"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	job := decoded(t, r)["job_id"]
	if n := pinCount(t, f, "job", job); n == 0 {
		t.Fatal("accepted import owns no pins")
	}
	if err = f.store.Write(ctx, func(tx app.WriteTx) error {
		index, e := tx.MetadataIndex(job)
		if e != nil {
			return e
		}
		return tx.AcquirePin(domain.Pin{ID: "cancel-backup", Kind: "backup", OwnerID: "keep-backup", Hash: index.Hash})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.auth.CancelJob(ctx, f.token, job, key(t)); err != nil {
		t.Fatal(err)
	}
	j, err := f.store.Job(ctx, job)
	if err != nil || j.State != "cancelled" {
		t.Fatal(j, err)
	}
	if n := pinCount(t, f, "job", job); n != 0 {
		t.Fatalf("stop-confirmed import retains owned pins: %d", n)
	}
	if n := pinCount(t, f, "backup", "keep-backup"); n != 1 {
		t.Fatal("cancel released independent backup pin", n)
	}
}

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
func TestUploadDiscoveryPropagatesOperationalAuthorityFailure(t *testing.T) {
	f := newFixture(t)
	f.sealed(t, nil, "<html>list</html>")
	f.core.Store = &scopeFailureStore{Store: f.store, failure: syscall.EIO}
	if _, err := f.core.Uploads(ctx, f.token, importer.UploadQuery{}); !errors.Is(err, syscall.EIO) {
		t.Fatalf("authority storage failure hidden as list success: %v", err)
	}
	f.core.Store = &scopeFailureStore{Store: f.store, failure: domain.ErrNotFound}
	if page, err := f.core.Uploads(ctx, f.token, importer.UploadQuery{}); err != nil || len(page.Items) != 0 {
		t.Fatal("known hidden target was not filtered", page, err)
	}
}

func TestMetadataSemanticValidationIsSharedByPreviewAndAdmission(t *testing.T) {
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
		t.Run(name, func(t *testing.T) {
			if _, err := f.core.Validate(ctx, f.token, "", importer.ImportRequest{UploadID: u, Metadata: bytes.NewBufferString(metadata)}); !errors.Is(err, importer.ErrPackageInvalid) {
				t.Errorf("preview accepted invalid semantic metadata: %v", err)
			}
			op := key(t)
			if _, err := f.core.Import(ctx, f.token, op, importer.ImportRequest{UploadID: u, DocumentPresent: true, Metadata: bytes.NewBufferString(metadata)}); !errors.Is(err, importer.ErrPackageInvalid) {
				t.Errorf("admission accepted invalid semantic metadata: %v", err)
			}
		})
	}
}

func (f *fixture) reopen(t *testing.T) {
	t.Helper()
	if err := f.closeStore(); err != nil {
		t.Fatal(err)
	}
	_, closeStore, err := f.store.Prepare(ctx, f.profile)
	if err != nil {
		t.Fatal(err)
	}
	f.closeStore = closeStore
}
func TestMetadataPreparationRestartReclaimsOnlyDeadCallOwnership(t *testing.T) {
	f := newFixture(t)
	u := f.sealed(t, nil, "<html>namespace restart</html>")
	response, err := f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: u, DocumentPresent: true, Metadata: bytes.NewBufferString(`{"document":{"title":"linked index"}}`)})
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	err = f.store.Write(ctx, func(tx app.WriteTx) error {
		if _, e := tx.MetadataIndex(unlinked); !errors.Is(e, domain.ErrNotFound) {
			t.Fatalf("dead-call index survived: %v", e)
		}
		rows, e := tx.ImportMetadata(unlinked, "sources", "", 64)
		if e != nil || len(rows) != 0 {
			t.Fatal("partial dead-call rows survived cleanup", rows, e)
		}
		index, e := tx.MetadataIndex(job)
		if e != nil || !index.Complete || index.JobID != job {
			t.Fatal("linked accepted index changed", index, e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if pinCount(t, f, "import_prepare", unlinked) != 0 || pinCount(t, f, "metadata_prepare", unlinked) != 0 || pinCount(t, f, "import", "dead-call") != 0 {
		t.Fatal("dead call pins survived restart")
	}
	if pinCount(t, f, "backup", "independent") != 1 || pinCount(t, f, "job", job) == 0 {
		t.Fatal("restart reclaimed independent or accepted ownership")
	}
	if err = f.core.Process(ctx, job); err != nil {
		t.Fatal("linked index could not publish after restart", err)
	}
}
func TestInterruptedImportCancelRecoveryReleasesOwnedIndexAndPins(t *testing.T) {
	f := newFixture(t)
	u := f.sealed(t, nil, "<html>cancel recovery</html>")
	response, err := f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: u, DocumentPresent: true, Metadata: bytes.NewBufferString(`{"document":{"title":"cancel recovery"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	job := decoded(t, response)["job_id"]
	if err = f.store.Write(ctx, func(tx app.WriteTx) error { _, e := tx.ClaimJob(job); return e }); err != nil {
		t.Fatal(err)
	}
	if _, err = f.auth.CancelJob(ctx, f.token, job, key(t)); err != nil {
		t.Fatal(err)
	}
	if pinCount(t, f, "job", job) == 0 {
		t.Fatal("unconfirmed cancellation released active pins")
	}
	f.reopen(t)
	if err = f.core.Process(ctx, job); err != nil {
		t.Fatal(err)
	}
	j, err := f.store.Job(ctx, job)
	if err != nil || j.State != "cancelled" {
		t.Fatal(j, err)
	}
	if pinCount(t, f, "job", job) != 0 {
		t.Fatal("confirmed recovered cancellation leaked pins")
	}
	if err = f.store.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.MetadataIndex(job)
		if !errors.Is(e, domain.ErrNotFound) {
			t.Fatal("cancelled index remained", e)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestActualCompetingPublicationAndOnceCapturedBasePreserveReplay(t *testing.T) {
	f := newFixture(t)
	document, base := f.imported(t, f.sealed(t, nil, "<html>base</html>"), "")
	publish := func(upload, base string) (string, string) {
		t.Helper()
		op, version := key(t), key(t)
		r, e := f.core.Import(ctx, f.token, op, importer.ImportRequest{UploadID: upload, DocumentPresent: true, DocumentID: &document, BasePresent: true, BaseVersionID: &base, VersionID: version})
		if e != nil {
			t.Fatal(e)
		}
		job := decoded(t, r)["job_id"]
		if e = f.core.Process(ctx, job); e != nil {
			t.Fatal(e)
		}
		return op, version
	}
	staged := f.sealed(t, &document, "<html>pre-transfer base intent</html>")
	_, winner := publish(f.sealed(t, &document, "<html>first competitor</html>"), base)
	staleOperation := key(t)
	if _, e := f.core.Import(ctx, f.token, staleOperation, importer.ImportRequest{UploadID: staged, DocumentPresent: true, DocumentID: &document, BasePresent: true, BaseVersionID: &base}); !errors.Is(e, importer.ErrVersionConflict) {
		t.Fatal("actual competing publication did not reject original base", e)
	}
	record, e := f.auth.Authenticate(ctx, f.token)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.store.Receipt(ctx, domain.OperationKey{PrincipalID: record.PrincipalID, OperationID: staleOperation}); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("stale admission left durable effect", e)
	}
	operation := key(t)
	request := importer.ImportRequest{UploadID: staged, DocumentPresent: true, DocumentID: &document}
	accepted, e := f.core.Import(ctx, f.token, operation, request)
	if e != nil {
		t.Fatal(e)
	}
	job := decoded(t, accepted)["job_id"]
	receipt, e := f.store.Receipt(ctx, domain.OperationKey{PrincipalID: record.PrincipalID, OperationID: operation})
	if e != nil {
		t.Fatal(e)
	}
	var normalization struct {
		Base string `json:"base_version_id"`
	}
	if e = json.Unmarshal(receipt.Normalization, &normalization); e != nil || normalization.Base != winner {
		t.Fatal("omitted base not captured at reservation", normalization, e)
	}
	_, current := publish(f.sealed(t, &document, "<html>second competitor</html>"), winner)
	replay, e := f.core.Import(ctx, f.token, operation, request)
	if e != nil || !bytes.Equal(accepted.Body, replay.Body) {
		t.Fatal("old accepted response lost to new latest", e)
	}
	if e = f.core.Process(ctx, job); !errors.Is(e, importer.ErrVersionConflict) {
		t.Fatal("omitted base recaptured at worker rather than reservation", e)
	}
	before, e := f.store.Document(ctx, document)
	if e != nil || before.LatestVersionID != current {
		t.Fatal(before, e)
	}
	upload := f.sealed(t, &document, "<html>successful replay</html>")
	op, version := key(t), key(t)
	request = importer.ImportRequest{UploadID: upload, DocumentPresent: true, DocumentID: &document, BasePresent: true, BaseVersionID: &current, VersionID: version}
	accepted, e = f.core.Import(ctx, f.token, op, request)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.core.Process(ctx, decoded(t, accepted)["job_id"]); e != nil {
		t.Fatal(e)
	}
	before, e = f.store.Document(ctx, document)
	if e != nil {
		t.Fatal(e)
	}
	replay, e = f.core.Import(ctx, f.token, op, request)
	if e != nil || !bytes.Equal(accepted.Body, replay.Body) {
		t.Fatal("successful same-key original now-stale base failed replay", e)
	}
	after, e := f.store.Document(ctx, document)
	if e != nil || after.Revision != before.Revision || after.LatestVersionID != version {
		t.Fatal("replay published again", after, e)
	}
}

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
func TestInterruptedSealCancellationReclaimsOnlyStoppedPartialOwnership(t *testing.T) {
	for _, abandon := range []bool{false, true} {
		name := "cancel"
		if abandon {
			name = "abandon"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			var archive bytes.Buffer
			z := zip.NewWriter(&archive)
			for _, name := range []string{"first.html", "second.html"} {
				w, e := z.Create(name)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = w.Write([]byte("<html>" + name + "</html>")); e != nil {
					t.Fatal(e)
				}
			}
			if e := z.Close(); e != nil {
				t.Fatal(e)
			}
			target, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": nil})
			response, e := f.core.Create(ctx, f.token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "archive", SelectionName: "interrupted.zip", Target: target})
			if e != nil {
				t.Fatal(e)
			}
			var created struct {
				ID string `json:"upload_id"`
			}
			if e = json.Unmarshal(response.Body, &created); e != nil {
				t.Fatal(e)
			}
			upload := created.ID
			sum := sha256.Sum256(archive.Bytes())
			length := int64(archive.Len())
			ack, e := f.core.Put(ctx, f.token, upload, importer.Chunk{Path: "_input", Kind: "file", Hash: hex.EncodeToString(sum[:]), Length: &length, Final: true}, bytes.NewReader(archive.Bytes()))
			if e != nil {
				t.Fatal(e)
			}
			cp := ack.Checkpoint
			cp.Generation++
			cp.Complete = true
			response, e = f.core.Complete(ctx, f.token, upload, key(t), cp)
			if e != nil {
				t.Fatal(e)
			}
			job := decoded(t, response)["job_id"]
			workCtx, stop := context.WithCancel(ctx)
			f.core.Codec = interruptAfterEntryCodec{Codec: staging.New(), stop: stop}
			if e = f.core.Process(workCtx, job); !errors.Is(e, context.Canceled) {
				t.Fatal("actual worker interruption not observed", e)
			}
			f.core.Codec = staging.New()
			partial, e := f.store.Entries(ctx, job, "", 64)
			if e != nil || len(partial) != 1 {
				t.Fatal("actual interrupted seal did not retain durable first entry", partial, e)
			}
			if pinCount(t, f, "job", job) == 0 {
				t.Fatal("archive input job pin missing before confirmed stop")
			}
			if e = f.store.Write(ctx, func(tx app.WriteTx) error {
				return tx.AcquirePin(domain.Pin{ID: "retained-seal-backup", Kind: "backup", OwnerID: "keep-recovered-seal", Hash: partial[0].Hash})
			}); e != nil {
				t.Fatal(e)
			}
			if abandon {
				_, e = f.core.Abandon(ctx, f.token, upload, key(t))
			} else {
				_, e = f.auth.CancelJob(ctx, f.token, job, key(t))
			}
			if e != nil {
				t.Fatal(e)
			}
			f.reopen(t)
			if e = f.core.Process(ctx, job); e != nil {
				t.Fatal(e)
			}
			partial, e = f.store.Entries(ctx, job, "", 64)
			if e != nil || len(partial) != 0 {
				t.Errorf("stop-confirmed recovered seal retained partial content refs: %d %v", len(partial), e)
			}
			if n := pinCount(t, f, "job", job); n != 0 {
				t.Errorf("recovered cancelled seal retained owned job pins: %d", n)
			}
			if pinCount(t, f, "backup", "keep-recovered-seal") != 1 {
				t.Fatal("stop cleanup released independent backup")
			}
			u, e := f.store.Upload(ctx, upload)
			if e != nil || u.Checkpoint != cp {
				t.Fatal("raw confirmed input changed", u, e)
			}
			if abandon {
				if u.State != "abandoned" {
					t.Fatal("abandoned input reopened", u)
				}
			} else {
				if u.State != "open" || pinCount(t, f, "upload", upload) == 0 {
					t.Fatal("cancel discarded resumable raw input", u)
				}
				response, e = f.core.Complete(ctx, f.token, upload, key(t), cp)
				if e != nil {
					t.Fatal(e)
				}
				next := decoded(t, response)["job_id"]
				if next == job {
					t.Fatal("terminal seal reused")
				}
				if e = f.core.Process(ctx, next); e != nil {
					t.Fatal("retained raw input did not reseal", e)
				}
			}
		})
	}
}
