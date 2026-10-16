package imports

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	appauth "haeryong.local/haeryong/internal/application/auth"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/service"
	appstorage "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type httpFixture struct {
	t              *testing.T
	selection      service.Selection
	writer         *service.LockedProfile
	store          *foundation.Store
	closeStore     func() error
	core           *importer.Service
	auth           *appauth.Service
	server         *httptest.Server
	stop           context.CancelFunc
	worker         chan error
	token, project string
	dropOperation  atomic.Pointer[string]
}

func newHTTPFixture(t *testing.T) *httpFixture {
	t.Helper()
	base := os.TempDir()
	var volume, parent syscall.Stat_t
	if syscall.Stat("/Volumes/RootKernel", &volume) == nil && syscall.Stat("/Volumes", &parent) == nil && volume.Dev != parent.Dev && syscall.Access("/Volumes/RootKernel", 2) == nil {
		base = "/Volumes/RootKernel/tmp"
	}
	dir, err := os.MkdirTemp(base, "haeryong-task004-http-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := &httpFixture{t: t, selection: service.Selection{Profile: "imports", DataDir: filepath.Join(dir, "profile")}}
	if _, err := service.Initialize(f.selection); err != nil {
		t.Fatal(err)
	}
	f.open()
	t.Cleanup(f.close)
	p, err := f.auth.CreatePairing(context.Background(), f.server.URL, "synthetic HTTP owner", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.auth.BootstrapDecide(context.Background(), domain.PairingDecision{ID: p.ID, Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: operation(t), Action: "approve"}); err != nil {
		t.Fatal(err)
	}
	poll, err := f.auth.Poll(context.Background(), p.ID, p.PollingSecret, p.Origin)
	if err != nil || poll.Token == "" {
		t.Fatal("owner fixture not authenticated")
	}
	f.token = poll.Token
	return f
}
func operation(t *testing.T) string {
	t.Helper()
	id, err := service.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func (f *httpFixture) open() {
	f.t.Helper()
	var err error
	f.writer, err = service.OpenWriter(f.selection)
	if err != nil {
		f.t.Fatal(err)
	}
	if err = f.writer.PrepareStorageBootstrap(nil); err != nil {
		f.t.Fatal(err)
	}
	f.store = foundation.New(nil)
	_, f.closeStore, err = f.store.Prepare(context.Background(), f.writer.Profile)
	if err != nil {
		f.t.Fatal(err)
	}
	if err = f.writer.MarkStorageReady(nil); err != nil {
		f.t.Fatal(err)
	}
	health, err := f.store.Health(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	f.project = health.Library.UnfiledProjectID
	if _, err := f.store.Project(context.Background(), f.project); err != nil {
		if err := f.store.Write(context.Background(), func(tx appstorage.WriteTx) error {
			return tx.CreateProject(domain.Project{ID: f.project, Name: "Unfiled", Lifecycle: "active", Revision: 1, PermissionsRevision: 1})
		}); err != nil {
			f.t.Fatal(err)
		}
	}
	f.auth = appauth.New(f.store)
	f.core = importer.New(f.store, f.auth, f.store, staging.New())
	f.server = httptest.NewUnstartedServer(nil)
	origin := "http://" + f.server.Listener.Addr().String()
	handler, err := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{f.server.Listener.Addr().String()}, Origins: []string{origin}}, New(f.core))
	if err != nil {
		f.t.Fatal(err)
	}
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := f.dropOperation.Load()
		if key != nil && r.Header.Get("Idempotency-Key") == *key && f.dropOperation.CompareAndSwap(key, nil) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				f.t.Error("response-loss fixture could not close owned connection")
				return
			}
			connection.Close()
			return
		}
		handler.ServeHTTP(w, r)
	})
	f.server.Start()
	ctx, cancel := context.WithCancel(context.Background())
	f.stop = cancel
	f.worker = make(chan error, 1)
	go func() { f.worker <- f.core.Run(ctx) }()
}
func (f *httpFixture) close() {
	if f.server != nil {
		f.server.Close()
		f.server = nil
	}
	if f.stop != nil {
		f.stop()
		select {
		case err := <-f.worker:
			if err != nil && err != context.Canceled {
				f.t.Error(err)
			}
		case <-time.After(10 * time.Second):
			f.t.Error("import worker failed to stop")
		}
		f.stop = nil
	}
	if f.closeStore != nil {
		if err := f.closeStore(); err != nil {
			f.t.Error(err)
		}
		f.closeStore = nil
	}
	if f.writer != nil {
		if err := f.writer.Close(); err != nil {
			f.t.Error(err)
		}
		f.writer = nil
	}
}
func (f *httpFixture) restart() { f.close(); f.open() }
func (f *httpFixture) request(method, path string, body io.Reader, headers map[string]string) (int, http.Header, []byte) {
	f.t.Helper()
	r, err := http.NewRequest(method, f.server.URL+path, body)
	if err != nil {
		f.t.Fatal(err)
	}
	r.Header.Set("Origin", f.server.URL)
	if f.token != "" {
		r.Header.Set("Authorization", "Bearer "+f.token)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(r)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		f.t.Fatal(err)
	}
	return response.StatusCode, response.Header, data
}
func (f *httpFixture) json(method, path, key string, value any) (int, []byte) {
	f.t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		f.t.Fatal(err)
	}
	headers := map[string]string{"Content-Type": "application/json"}
	if key != "" {
		headers["Idempotency-Key"] = key
	}
	status, _, data := f.request(method, path, bytes.NewReader(b), headers)
	return status, data
}
func valueAs[T any](t *testing.T, data []byte) T {
	t.Helper()
	var envelope struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}
func requireStatus(t *testing.T, got, want int, data []byte) {
	t.Helper()
	if got != want {
		var value struct {
			Error struct {
				Code    string `json:"code"`
				Details struct {
					Field string `json:"field"`
				} `json:"details"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &value)
		t.Fatalf("HTTP status=%d want=%d error=%s field=%s", got, want, value.Error.Code, value.Error.Details.Field)
	}
}
func (f *httpFixture) upload(mode, name string) string {
	f.t.Helper()
	status, data := f.json("POST", "/api/v1/uploads", operation(f.t), map[string]any{"purpose": "result", "target": map[string]any{"project_id": f.project, "document_id": nil}, "input_mode": mode, "selection_name": name})
	requireStatus(f.t, status, 201, data)
	value := valueAs[struct {
		ID string `json:"upload_id"`
	}](f.t, data)
	return value.ID
}
func (f *httpFixture) put(id, path, kind string, b []byte, offset int64, final bool) (int, importer.ChunkResult, []byte) {
	f.t.Helper()
	headers := map[string]string{"Content-Type": "application/octet-stream"}
	if kind == "file" {
		digest := sha256.Sum256(b)
		headers["Haeryong-Upload-Offset"] = strconv.FormatInt(offset, 10)
		headers["Haeryong-Upload-Chunk-SHA256"] = hex.EncodeToString(digest[:])
		if final {
			headers["Haeryong-Upload-Final"] = "true"
		}
	} else {
		headers["Content-Type"] = "application/json"
	}
	status, _, data := f.request("PUT", "/api/v1/uploads/"+id+"/entries?path="+url.QueryEscape(path)+"&kind="+kind, bytes.NewReader(b), headers)
	return status, valueAs[importer.ChunkResult](f.t, data), data
}
func (f *httpFixture) detail(id string) importer.UploadDetail {
	f.t.Helper()
	status, _, data := f.request("GET", "/api/v1/uploads/"+id, nil, nil)
	requireStatus(f.t, status, 200, data)
	return valueAs[importer.UploadDetail](f.t, data)
}
func (f *httpFixture) seal(id string, timeout ...time.Duration) importer.UploadDetail {
	f.t.Helper()
	d := f.detail(id)
	cp := d.Checkpoint
	cp.Generation++
	cp.Complete = true
	started := time.Now()
	key := operation(f.t)
	status, data := f.json("POST", "/api/v1/uploads/"+id+"/complete", key, map[string]any{"checkpoint": cp})
	if status != 202 {
		_, cause := f.core.Complete(context.Background(), f.token, id, key, cp)
		f.t.Fatalf("complete HTTP%d; actual core cause: %v", status, cause)
	}
	requireStatus(f.t, status, 202, data)
	duration := 15 * time.Second
	interval := 10 * time.Millisecond
	if len(timeout) > 0 {
		duration = timeout[0]
		interval = 100 * time.Millisecond
	}
	deadline := time.Now().Add(duration)
	for {
		d = f.detail(id)
		if d.State == "sealed" {
			return d
		}
		if d.State == "open" && d.Error != nil {
			f.t.Fatalf("seal failed: %s", f.sealDiagnostics(d, time.Since(started)))
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("seal did not finish: %s", f.sealDiagnostics(d, time.Since(started)))
		}
		time.Sleep(interval)
	}
}

// Collect progress only on failure, keeping the observer out of the worker's
// ordinary write path. These are live typed snapshots, not a publication claim.
func (f *httpFixture) sealDiagnostics(detail importer.UploadDetail, elapsed time.Duration) string {
	status := map[string]any{"phase": "seal", "elapsed_ms": elapsed.Milliseconds(), "upload_state": detail.State, "upload_error": detail.Error, "checkpoint": detail.Checkpoint}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if detail.SealJobID != nil {
		job, err := f.store.Job(ctx, *detail.SealJobID)
		if err != nil {
			status["job_read_error"] = err.Error()
		} else {
			status["job_id"], status["job_state"], status["job_error"] = job.ID, job.State, job.Error
			status["attempt_generation"], status["cancel_requested"] = job.AttemptGeneration, job.CancelRequested
			counts := map[string]int{}
			var stagedBytes int64
			after := ""
			for {
				rows, err := f.store.Entries(ctx, job.ID, after, 64)
				if err != nil {
					status["staged_progress_error"] = err.Error()
					break
				}
				if len(rows) == 0 {
					break
				}
				for _, entry := range rows {
					counts[entry.Kind]++
					if entry.Kind == "file" {
						stagedBytes += entry.Size
					}
					after = entry.Path
				}
			}
			status["staged_entries_by_kind"], status["staged_file_bytes"], status["last_staged_path"] = counts, stagedBytes, after
		}
	}
	data, _ := json.Marshal(status)
	return string(data)
}
func (f *httpFixture) publish(id, key string) domain.Version {
	f.t.Helper()
	status, data := f.json("POST", "/api/v1/imports", key, map[string]any{"upload_id": id, "document_id": nil})
	if status != 201 && status != 202 {
		requireStatus(f.t, status, 202, data)
	}
	var result struct {
		VersionID string `json:"version_id"`
	}
	if status == 202 {
		job := valueAs[struct {
			ID string `json:"job_id"`
		}](f.t, data)
		deadline := time.Now().Add(15 * time.Second)
		for {
			state, err := f.store.Job(context.Background(), job.ID)
			if err != nil {
				f.t.Fatal(err)
			}
			if state.State == "failed" {
				f.t.Fatalf("import failed: %s", state.Error)
			}
			if state.State == "completed" {
				if err := json.Unmarshal(state.Result, &result); err != nil {
					f.t.Fatal(err)
				}
				break
			}
			if time.Now().After(deadline) {
				f.t.Fatal("import did not finish")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	record, err := f.auth.Operation(context.Background(), f.token, key)
	if err != nil {
		f.t.Fatal(err)
	}
	if status != 202 && record.Response != nil {
		_ = json.Unmarshal(record.Response.Body, &result)
	}
	if result.VersionID == "" {
		f.t.Fatal("completed import receipt has no version identity")
	}
	version, err := f.store.Version(context.Background(), result.VersionID)
	if err != nil {
		f.t.Fatal(err)
	}
	return version
}
func (f *httpFixture) verify(version domain.Version, expected map[string]string, kinds map[string]string) {
	f.t.Helper()
	seen := map[string]bool{}
	after := ""
	for {
		rows, err := f.store.Entries(context.Background(), version.ContentSetID, after, 64)
		if err != nil {
			f.t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		for _, entry := range rows {
			seen[entry.Path] = true
			if kind, ok := kinds[entry.Path]; ok && kind != entry.Kind {
				f.t.Fatalf("entry kind changed: %q", entry.Path)
			}
			if want, ok := expected[entry.Path]; ok {
				var got strings.Builder
				err := f.store.Read(context.Background(), domain.Object{Hash: entry.Hash, Size: entry.Size, State: "ready"}, domain.Pin{Kind: "active_read", OwnerID: "http-proof"}, func(r io.ReadSeeker) error { _, err := io.Copy(&got, r); return err })
				if err != nil || got.String() != want {
					f.t.Fatalf("stored bytes changed: %q %v", entry.Path, err)
				}
			}
			after = entry.Path
		}
	}
	for path := range expected {
		if !seen[path] {
			f.t.Fatalf("file omitted: %q", path)
		}
	}
	for path := range kinds {
		if !seen[path] {
			f.t.Fatalf("typed entry omitted: %q", path)
		}
	}
}

func TestActualAuthorizedResultDirectoryHTTPPublicationAndRestart(t *testing.T) {
	f := newHTTPFixture(t)
	id := f.upload("directory", "existing gaori result")
	expected := map[string]string{}
	root := filepath.Join("..", "..", "..", "..", "spikes", "knowledge-value", "samples", "gaori")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			status, _, data := f.put(id, relative, "directory", []byte("{}"), 0, true)
			requireStatus(t, status, 200, data)
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		expected[relative] = string(b)
		status, _, data := f.put(id, relative, "file", b, 0, true)
		requireStatus(t, status, 200, data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(expected) < 3 {
		t.Fatal("existing result fixture missing")
	}
	f.seal(id)
	version := f.publish(id, operation(t))
	f.verify(version, expected, nil)
	f.restart()
	f.verify(version, expected, nil)
}

func TestActualUploadChunkIdentityResumeAndPolicy(t *testing.T) {
	f := newHTTPFixture(t)
	id := f.upload("directory", "resume")
	status, first, data := f.put(id, "nested/page.html", "file", []byte("first"), 0, false)
	requireStatus(t, status, 200, data)
	if first.Checkpoint.Complete {
		t.Fatal("file chunk declared whole input complete")
	}
	status, replay, data := f.put(id, "nested/page.html", "file", []byte("first"), 0, false)
	requireStatus(t, status, 200, data)
	if first.Checkpoint != replay.Checkpoint {
		t.Fatal("natural replay mutated checkpoint")
	}
	status, _, data = f.put(id, "nested/page.html", "file", []byte("other"), 0, false)
	requireStatus(t, status, 409, data)
	f.restart()
	if f.detail(id).Checkpoint != first.Checkpoint {
		t.Fatal("durable ACK lost after restart")
	}
	status, _, data = f.put(id, "nested/page.html", "file", []byte("last"), 5, true)
	requireStatus(t, status, 200, data)
	status, _, data = f.put(id, "empty.bin", "file", nil, 0, true)
	requireStatus(t, status, 200, data)
	status, _, data = f.put(id, "../outside", "file", []byte("x"), 0, true)
	requireStatus(t, status, 422, data)
	for _, resource := range []string{"", "/entries", "/ranges", "/pins"} {
		status, _, data = f.request("GET", "/api/v1/uploads/"+id+resource, nil, nil)
		requireStatus(t, status, 200, data)
	}
	status, _, data = f.request("OPTIONS", "/api/v1/uploads/"+id+"/entries", nil, map[string]string{"Access-Control-Request-Method": "PUT", "Access-Control-Request-Headers": "authorization,content-type,haeryong-upload-offset,haeryong-upload-chunk-sha256,haeryong-upload-length,haeryong-upload-final"})
	requireStatus(t, status, 204, data)
	f.seal(id)
	version := f.publish(id, operation(t))
	f.verify(version, map[string]string{"nested/page.html": "firstlast", "empty.bin": ""}, map[string]string{"nested": "directory"})
}

func TestActualZIPAndSingleFileTypedEntries(t *testing.T) {
	for _, mode := range []string{"archive", "file"} {
		t.Run(mode, func(t *testing.T) {
			f := newHTTPFixture(t)
			id := f.upload(mode, "selected.html")
			expected := map[string]string{"selected.html": "<html>single</html>"}
			kinds := map[string]string{}
			var payload []byte
			if mode == "file" {
				payload = []byte(expected["selected.html"])
			} else {
				expected = map[string]string{"guide.htm": "<script>normal()</script>", "nested/é.html": "unicode", "manifest.json": "ordinary", ".hidden": "hidden", "unknown.data": "bytes", `literal\name`: "backslash"}
				var b bytes.Buffer
				w := zip.NewWriter(&b)
				for path, value := range expected {
					entry, err := w.Create(path)
					if err != nil {
						t.Fatal(err)
					}
					_, _ = io.WriteString(entry, value)
				}
				_, _ = w.Create("empty/")
				linkHeader := &zip.FileHeader{Name: "outside-link", Method: zip.Store}
				linkHeader.SetMode(os.ModeSymlink | 0777)
				link, err := w.CreateHeader(linkHeader)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.WriteString(link, "../outside")
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				payload = b.Bytes()
				kinds["empty"] = "directory"
				kinds["nested"] = "directory"
				kinds["outside-link"] = "symlink"
			}
			status, _, data := f.put(id, "_input", "file", payload, 0, true)
			requireStatus(t, status, 200, data)
			f.seal(id)
			version := f.publish(id, operation(t))
			f.restart()
			f.verify(version, expected, kinds)
			if mode == "archive" {
				rows, err := f.store.Entries(context.Background(), version.ContentSetID, "", 64)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range rows {
					if entry.Path == "outside-link" && entry.LinkText != "../outside" {
						t.Fatal("ZIP link text changed")
					}
				}
			}
		})
	}
}
