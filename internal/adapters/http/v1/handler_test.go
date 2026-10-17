package v1

import (
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
	"strings"
	"syscall"
	"testing"
	"time"

	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	httpimports "haeryong.local/haeryong/internal/adapters/http/imports"
	appauth "haeryong.local/haeryong/internal/application/auth"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type routes struct {
	queries *Handler
	imports http.Handler
}

func (m routes) Handles(r *http.Request) bool {
	return m.queries.Handles(r) || m.imports.(interface{ Handles(*http.Request) bool }).Handles(r)
}
func (m routes) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m.queries.Handles(r) {
		m.queries.ServeHTTP(w, r)
	} else {
		m.imports.ServeHTTP(w, r)
	}
}

type fixture struct {
	t              *testing.T
	selection      service.Selection
	writer         *service.LockedProfile
	store          *foundation.Store
	closeStore     func() error
	auth           *appauth.Service
	imports        *importer.Service
	query          *query.Service
	server         *httptest.Server
	stop           context.CancelFunc
	worker         chan error
	token, project string
}

func id(t *testing.T) string {
	t.Helper()
	v, err := service.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := os.TempDir()
	var volume, parent syscall.Stat_t
	if syscall.Stat("/Volumes/RootKernel", &volume) == nil && syscall.Stat("/Volumes", &parent) == nil && volume.Dev != parent.Dev && syscall.Access("/Volumes/RootKernel", 2) == nil {
		base = "/Volumes/RootKernel/tmp"
	}
	dir, err := os.MkdirTemp(base, "haeryong-task005-http-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := &fixture{t: t, selection: service.Selection{Profile: "query", DataDir: filepath.Join(dir, "profile")}}
	if _, err := service.Initialize(f.selection); err != nil {
		t.Fatal(err)
	}
	f.open()
	t.Cleanup(f.close)
	p, err := f.auth.CreatePairing(context.Background(), f.server.URL, "synthetic query owner", "query-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.auth.BootstrapDecide(context.Background(), domain.PairingDecision{ID: p.ID, Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: id(t), Action: "approve"}); err != nil {
		t.Fatal(err)
	}
	poll, err := f.auth.Poll(context.Background(), p.ID, p.PollingSecret, p.Origin)
	if err != nil {
		t.Fatal(err)
	}
	f.token = poll.Token
	return f
}
func (f *fixture) open() {
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
		if err := f.store.Write(context.Background(), func(tx app.WriteTx) error {
			return tx.CreateProject(domain.Project{ID: f.project, Name: "Unfiled", Lifecycle: "active", Revision: 1, PermissionsRevision: 1})
		}); err != nil {
			f.t.Fatal(err)
		}
	}
	f.auth = appauth.New(f.store)
	f.imports = importer.New(f.store, f.auth, f.store, staging.New())
	f.query = query.New(f.store, f.auth, f.store)
	f.server = httptest.NewUnstartedServer(nil)
	origin := "http://" + f.server.Listener.Addr().String()
	handler, err := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{f.server.Listener.Addr().String()}, Origins: []string{origin}}, routes{New(f.query), httpimports.New(f.imports)})
	if err != nil {
		f.t.Fatal(err)
	}
	f.server.Config.Handler = handler
	f.server.Start()
	ctx, cancel := context.WithCancel(context.Background())
	f.stop = cancel
	f.worker = make(chan error, 1)
	go func() { f.worker <- f.imports.Run(ctx) }()
}
func (f *fixture) close() {
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
			f.t.Error("worker did not stop")
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
func (f *fixture) restart() { f.close(); f.open() }
func (f *fixture) request(method, path string, body io.Reader, headers map[string]string) (int, http.Header, []byte) {
	f.t.Helper()
	request, err := http.NewRequest(method, f.server.URL+path, body)
	if err != nil {
		f.t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+f.token)
	request.Header.Set("Origin", f.server.URL)
	for k, v := range headers {
		request.Header.Set(k, v)
	}
	client := http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
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
func (f *fixture) json(method, path string, body any) (int, []byte) {
	f.t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		f.t.Fatal(err)
	}
	status, _, data := f.request(method, path, bytes.NewReader(data), map[string]string{"Content-Type": "application/json", "Idempotency-Key": id(f.t)})
	return status, data
}
func decoded[T any](t *testing.T, data []byte) T {
	t.Helper()
	var result struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result.Data
}
func want(t *testing.T, got, expected int, data []byte) {
	t.Helper()
	if got != expected {
		var failure struct {
			Error struct {
				Code    string         `json:"code"`
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		t.Fatalf("HTTP%d want%d code=%s details=%v", got, expected, failure.Error.Code, failure.Error.Details)
	}
}
func (f *fixture) create() string {
	status, data := f.json("POST", "/api/v1/uploads", map[string]any{"purpose": "result", "input_mode": "directory", "selection_name": "query fixture", "target": map[string]any{"project_id": f.project, "document_id": nil}})
	want(f.t, status, 201, data)
	return decoded[struct {
		ID string `json:"upload_id"`
	}](f.t, data).ID
}
func (f *fixture) put(upload, path, kind string, data []byte) {
	f.t.Helper()
	headers := map[string]string{"Content-Type": "application/json"}
	if kind == "file" {
		h := sha256.Sum256(data)
		headers = map[string]string{"Content-Type": "application/octet-stream", "Haeryong-Upload-Offset": "0", "Haeryong-Upload-Chunk-SHA256": hex.EncodeToString(h[:]), "Haeryong-Upload-Final": "true"}
	}
	status, _, body := f.request("PUT", "/api/v1/uploads/"+upload+"/entries?path="+url.QueryEscape(path)+"&kind="+kind, bytes.NewReader(data), headers)
	want(f.t, status, 200, body)
}
func (f *fixture) publish(upload string, metadata any) domain.Version {
	status, _, data := f.request("GET", "/api/v1/uploads/"+upload, nil, nil)
	want(f.t, status, 200, data)
	detail := decoded[importer.UploadDetail](f.t, data)
	if detail.State != "sealed" {
		cp := detail.Checkpoint
		cp.Generation++
		cp.Complete = true
		status, data = f.json("POST", "/api/v1/uploads/"+upload+"/complete", map[string]any{"checkpoint": cp})
		want(f.t, status, 202, data)
		seal := decoded[struct {
			ID string `json:"job_id"`
		}](f.t, data)
		f.wait(seal.ID)
	}
	body := map[string]any{"upload_id": upload, "document_id": nil}
	if metadata != nil {
		body["metadata"] = metadata
	}
	status, data = f.json("POST", "/api/v1/imports", body)
	want(f.t, status, 202, data)
	job := decoded[struct {
		ID string `json:"job_id"`
	}](f.t, data)
	done := f.wait(job.ID)
	var result struct {
		ID string `json:"version_id"`
	}
	if err := json.Unmarshal(done.Result, &result); err != nil || result.ID == "" {
		f.t.Fatal("publication result missing", err)
	}
	v, err := f.store.Version(context.Background(), result.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return v
}
func (f *fixture) wait(job string) domain.Job {
	f.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		row, err := f.store.Job(context.Background(), job)
		if err != nil {
			f.t.Fatal(err)
		}
		if row.State == "completed" {
			return row
		}
		if row.State == "failed" || time.Now().After(deadline) {
			f.t.Fatalf("job state=%s error=%s", row.State, row.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestActualExistingDirectoryQueryCursorRangeAndRestart(t *testing.T) {
	f := newFixture(t)
	upload := f.create()
	source := "../../../../spikes/knowledge-value/samples/gaori"
	expected := map[string][]byte{}
	kinds := map[string]string{}
	err := filepath.WalkDir(source, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, p)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if e.IsDir() {
			kinds[relative] = "directory"
			f.put(upload, relative, "directory", []byte(`{}`))
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		expected[relative] = data
		kinds[relative] = "file"
		f.put(upload, relative, "file", data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	v := f.publish(upload, nil)
	base := "/api/v1/versions/" + v.ID
	status, headers, data := f.request("GET", base, nil, nil)
	want(t, status, 200, data)
	summary := decoded[query.Version](t, data)
	if summary.ManifestSHA != v.ManifestSHA || summary.FilesURL != base+"/files" || summary.SuppliedMetadataURL != nil || summary.ETag != headers.Get("ETag") {
		t.Fatal("compact version/validator mismatch")
	}
	status, _, data = f.request("GET", base, nil, map[string]string{"If-None-Match": summary.ETag})
	want(t, status, 304, data)
	seen := map[string]bool{}
	cursor := ""
	firstCursor := ""
	for {
		request := base + "/files?limit=2"
		if cursor != "" {
			request += "&cursor=" + url.QueryEscape(cursor)
		}
		status, _, data = f.request("GET", request, nil, nil)
		want(t, status, 200, data)
		list := decoded[query.List[query.File]](t, data)
		for _, entry := range list.Items {
			if seen[entry.Path] || kinds[entry.Path] != entry.Kind {
				t.Fatal("file cursor lost exact inventory identity", entry.Path)
			}
			seen[entry.Path] = true
		}
		if list.NextCursor == nil {
			break
		}
		cursor = *list.NextCursor
		if firstCursor == "" {
			firstCursor = cursor
		}
	}
	if len(seen) != len(kinds) {
		t.Fatalf("cursor omitted entries %d/%d", len(seen), len(kinds))
	}
	for name, content := range expected {
		status, headers, data = f.request("GET", base+"/file?path="+url.QueryEscape(name), nil, nil)
		want(t, status, 200, data)
		if !bytes.Equal(content, data) {
			t.Fatal("exact source bytes changed", name)
		}
		if headers.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(headers.Get("Content-Disposition"), "attachment;") || headers.Get("Cache-Control") != "no-store" || headers.Get("X-Content-Type-Options") != "nosniff" || headers.Get("Content-Digest") == "" {
			t.Fatal("authored bytes lack safe attachment/integrity headers")
		}
		if len(content) > 4 {
			status, _, part := f.request("GET", base+"/file?path="+url.QueryEscape(name), nil, map[string]string{"Range": "bytes=1-3", "If-Range": headers.Get("ETag")})
			want(t, status, 206, part)
			if !bytes.Equal(part, content[1:4]) {
				t.Fatal("range changed immutable bytes")
			}
		}
	}
	status, _, data = f.request("GET", base+"/metadata?kind=normalized", nil, nil)
	want(t, status, 200, data)
	var full map[string]any
	manifestHash := sha256.Sum256(data)
	if json.Unmarshal(data, &full) != nil || full["content"] == nil || hex.EncodeToString(manifestHash[:]) != v.ManifestSHA {
		t.Fatal("normalized endpoint served compact summary instead of full manifest")
	}
	status, _, data = f.request("GET", base+"/metadata?kind=supplied", nil, nil)
	want(t, status, 404, data)
	status, _, data = f.request("GET", base+"/render-bootstrap", nil, nil)
	want(t, status, 200, data)
	bootstrap := decoded[query.Bootstrap](t, data)
	if bootstrap.RendererAvailable || bootstrap.Delivery != "authenticated_attachment" || len(data) > 4096 {
		t.Fatal("bootstrap pretends renderer or envelopes payload")
	}
	f.restart()
	status, _, data = f.request("GET", base+"/files?limit=2&cursor="+url.QueryEscape(firstCursor), nil, nil)
	want(t, status, 200, data)
	status, _, data = f.request("GET", base+"/files?limit=2&kind=file&cursor="+url.QueryEscape(firstCursor), nil, nil)
	want(t, status, 400, data)
	for name, content := range expected {
		status, _, data = f.request("GET", base+"/file?path="+url.QueryEscape(name), nil, nil)
		want(t, status, 200, data)
		if !bytes.Equal(content, data) {
			t.Fatal("restart changed source bytes", name)
		}
	}
}
