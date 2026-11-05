package v1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	httpconfiguration "haeryong.local/haeryong/internal/adapters/http/configuration"
	httpimports "haeryong.local/haeryong/internal/adapters/http/imports"
	appauth "haeryong.local/haeryong/internal/application/auth"
	appconfiguration "haeryong.local/haeryong/internal/application/configuration"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/render"
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
	t              GinkgoTInterface
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

func id(t GinkgoTInterface) string {
	GinkgoHelper()
	v, err := service.NewID()
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return v
}
func newFixture(t GinkgoTInterface) *fixture {
	GinkgoHelper()
	base := os.TempDir()
	var volume, parent syscall.Stat_t
	if syscall.Stat("/Volumes/RootKernel", &volume) == nil && syscall.Stat("/Volumes", &parent) == nil && volume.Dev != parent.Dev && syscall.Access("/Volumes/RootKernel", 2) == nil {
		base = "/Volumes/RootKernel/tmp"
	}
	dir, err := os.MkdirTemp(base, "haeryong-task005-http-test-")
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	DeferCleanup(func() { os.RemoveAll(dir) })
	f := &fixture{t: t, selection: service.Selection{Profile: "query", DataDir: filepath.Join(dir, "profile")}}
	{
		_, err := service.Initialize(f.selection)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	f.open()
	DeferCleanup(f.close)
	p, err := f.auth.CreatePairing(context.Background(), f.server.URL, "synthetic query owner", "query-fixture")
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	{

		_, err := f.auth.BootstrapDecide(context.Background(), domain.PairingDecision{ID: p.ID, Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: id(t), Action: "approve"})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	poll, err := f.auth.Poll(context.Background(), p.ID, p.PollingSecret, p.Origin)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

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
	handler, err := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{f.server.Listener.Addr().String()}, Origins: []string{origin}}, routes{New(f.query, nil, nil), httpimports.New(f.imports)})
	if err != nil {
		f.t.Fatal(err)
	}
	f.server.Config.Handler = handler
	f.server.Start()
	ctx, cancel := context.WithCancel(context.Background())
	f.stop = cancel
	f.worker = make(chan error, 1)
	go func() { defer GinkgoRecover(); f.worker <- f.imports.Run(ctx) }()
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
func decoded[T any](t GinkgoTInterface, data []byte) T {
	GinkgoHelper()
	var result struct {
		Data T `json:"data"`
	}
	{
		err := json.Unmarshal(data, &result)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	return result.Data
}
func want(t GinkgoTInterface, got, expected int, data []byte) {
	GinkgoHelper()
	if got != expected {
		var failure struct {
			Error struct {
				Code    string         `json:"code"`
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		Fail(fmt.Sprintf("HTTP%d want%d code=%s details=%v", got, expected, failure.Error.Code, failure.Error.Details))
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

var _ = It("TestActualExistingDirectoryQueryCursorRangeAndRestart", Label("persistence"), func() {
	t := GinkgoT()

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
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	v := f.publish(upload, nil)
	base := "/api/v1/versions/" + v.ID
	status, headers, data := f.request("GET", base, nil, nil)
	want(t, status, 200, data)
	summary := decoded[query.Version](t, data)
	Expect(summary.ManifestSHA != v.ManifestSHA || summary.FilesURL != base+"/files" || summary.SuppliedMetadataURL != nil || summary.ETag != headers.Get("ETag")).To(BeFalse(), func() string { return fmt.Sprint("compact version/validator mismatch") })

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
			Expect(seen[entry.Path] || kinds[entry.Path] != entry.Kind).To(BeFalse(), func() string { return fmt.Sprint("file cursor lost exact inventory identity", entry.Path) })

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
	Expect(len(seen) != len(kinds)).To(BeFalse(), func() string { return fmt.Sprintf("cursor omitted entries %d/%d", len(seen), len(kinds)) })

	for name, content := range expected {
		status, headers, data = f.request("GET", base+"/file?path="+url.QueryEscape(name), nil, nil)
		want(t, status, 200, data)
		Expect(!bytes.Equal(content, data)).To(BeFalse(), func() string { return fmt.Sprint("exact source bytes changed", name) })
		Expect(headers.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(headers.Get("Content-Disposition"), "attachment;") || headers.Get("Cache-Control") != "no-store" || headers.Get("X-Content-Type-Options") != "nosniff" || headers.Get("Content-Digest") == "").To(BeFalse(), func() string { return fmt.Sprint("authored bytes lack safe attachment/integrity headers") })

		if len(content) > 4 {
			status, _, part := f.request("GET", base+"/file?path="+url.QueryEscape(name), nil, map[string]string{"Range": "bytes=1-3", "If-Range": headers.Get("ETag")})
			want(t, status, 206, part)
			Expect(!bytes.Equal(part, content[1:4])).To(BeFalse(), func() string { return fmt.Sprint("range changed immutable bytes") })

		}
	}
	status, _, data = f.request("GET", base+"/metadata?kind=normalized", nil, nil)
	want(t, status, 200, data)
	var full map[string]any
	manifestHash := sha256.Sum256(data)
	Expect(json.Unmarshal(data, &full) != nil || full["content"] == nil || hex.EncodeToString(manifestHash[:]) != v.ManifestSHA).To(BeFalse(), func() string {
		return fmt.Sprint("normalized endpoint served compact summary instead of full manifest")
	})

	status, _, data = f.request("GET", base+"/metadata?kind=supplied", nil, nil)
	want(t, status, 404, data)
	status, _, data = f.request("GET", base+"/render-bootstrap", nil, nil)
	want(t, status, 200, data)
	bootstrap := decoded[query.Bootstrap](t, data)
	Expect(bootstrap.RendererAvailable || bootstrap.Delivery != "authenticated_attachment" || len(data) > 4096).To(BeFalse(), func() string { return fmt.Sprint("bootstrap pretends renderer or envelopes payload") })

	f.restart()
	status, _, data = f.request("GET", base+"/files?limit=2&cursor="+url.QueryEscape(firstCursor), nil, nil)
	want(t, status, 200, data)
	status, _, data = f.request("GET", base+"/files?limit=2&kind=file&cursor="+url.QueryEscape(firstCursor), nil, nil)
	want(t, status, 400, data)
	for name, content := range expected {
		status, _, data = f.request("GET", base+"/file?path="+url.QueryEscape(name), nil, nil)
		want(t, status, 200, data)
		Expect(!bytes.Equal(content, data)).To(BeFalse(), func() string { return fmt.Sprint("restart changed source bytes", name) })

	}
})

// C2: the three absent personal-resource responses expose the registry's
// default visible value with EXPLICIT null revision/etag JSON keys (not
// omitted) and the create precondition; a stored pointer carries the
// once-encoded selected page-position link or an explicit null link.
var _ = It("TestPersonalAbsentHTTPWireDefaultsAndPointerLink", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	upload := f.create()
	f.put(upload, "hello.html", "file", []byte("<html>hi</html>"))
	v := f.publish(upload, nil)
	// The closed machine shapes own the exact response key set; every actual
	// Go HTTP payload must align with its family's shape, including the
	// mandatory nullable page_position_url on non-pointer families.
	exactKeys := func(data []byte, keys ...string) {
		var envelope struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		Expect(json.Unmarshal(data, &envelope) != nil).To(BeFalse(), func() string { return string(data) })
		Expect(len(envelope.Data)).To(Equal(len(keys)), func() string { return string(data) })
		for _, key := range keys {
			_, ok := envelope.Data[key]
			Expect(ok).To(BeTrue(), func() string { return "missing key " + key + ": " + string(data) })
		}
	}
	status, _, data := f.request("GET", "/api/v1/versions/"+v.ID+"/reader-preference", nil, nil)
	want(t, status, 200, data)
	exactKeys(data, "exists", "value", "revision", "etag", "required_precondition", "page_position_url")
	Expect(string(data)).To(ContainSubstring(`"exists":false`), func() string { return string(data) })
	Expect(string(data)).To(ContainSubstring(`"default_page":null`), func() string { return string(data) })
	Expect(string(data)).To(ContainSubstring(`"revision":null`), func() string { return string(data) })
	Expect(string(data)).To(ContainSubstring(`"etag":null`), func() string { return string(data) })
	Expect(string(data)).To(ContainSubstring(`"required_precondition":"if_none_match_star"`), func() string { return string(data) })
	Expect(string(data)).To(ContainSubstring(`"page_position_url":null`), func() string { return string(data) })
	status, _, data = f.request("GET", "/api/v1/versions/"+v.ID+"/reading-position", nil, nil)
	want(t, status, 200, data)
	exactKeys(data, "exists", "value", "revision", "etag", "required_precondition", "page_position_url")
	Expect(string(data)).To(ContainSubstring(`"page_path":null,"page_query":"","page_fragment":"","opened":false,"first_opened_at":null,"last_opened_at":null`), func() string { return string(data) })
	Expect(string(data)).To(ContainSubstring(`"page_position_url":null`), func() string { return string(data) })
	status, _, data = f.request("GET", "/api/v1/versions/"+v.ID+"/page-position?page=hello.html", nil, nil)
	want(t, status, 200, data)
	exactKeys(data, "exists", "value", "revision", "etag", "required_precondition", "page_position_url")
	Expect(string(data)).To(ContainSubstring(`"page_query":"","page_fragment":"","position":{}`), func() string { return string(data) })
	Expect(string(data)).To(ContainSubstring(`"page_position_url":null`), func() string { return string(data) })
	// Store a page-bound pointer and observe the canonical encoded link;
	// a stored response replaces the precondition with the real validator
	// while keeping the exact pointer key set.
	pointerBody := `{"page_path":"hello.html","page_query":"s=1","page_fragment":"f","opened":true}`
	status, _, data = f.request("PUT", "/api/v1/versions/"+v.ID+"/reading-position", bytes.NewReader([]byte(pointerBody)), map[string]string{"Content-Type": "application/json", "Idempotency-Key": id(t), "If-None-Match": "*"})
	want(t, status, 200, data)
	exactKeys(data, "exists", "value", "revision", "etag", "page_position_url")
	Expect(string(data)).To(ContainSubstring(`"page_position_url":"/api/v1/versions/`+v.ID+`/page-position?page=hello.html"`), func() string { return string(data) })
	var stored struct {
		Data struct {
			ETag string `json:"etag"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(data, &stored) != nil).To(BeFalse())
	status, _, data = f.request("PUT", "/api/v1/versions/"+v.ID+"/reading-position", bytes.NewReader([]byte(`{"page_path":null,"page_query":"","page_fragment":"","opened":true}`)), map[string]string{"Content-Type": "application/json", "Idempotency-Key": id(t), "If-Match": stored.Data.ETag})
	want(t, status, 200, data)
	Expect(string(data)).To(ContainSubstring(`"page_position_url":null`), func() string { return string(data) })
})

var _ = It("TestSourceEvidenceVerificationHistoryServesPathFormAlias", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	u := f.create()
	f.put(u, "page.html", "file", []byte("<html>history alias</html>"))
	version := f.publish(u, nil)
	handler := New(f.query, nil, nil)
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+f.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	// The catalogued path form serves the same history read for simple ids
	// (S27): the unknown version is a nondisclosing 404, never 503.
	recorder := request("/api/v1/versions/00000000-0000-4000-8000-000000000001/source-evidence/simple/verifications")
	Expect(recorder.Code).To(Equal(404))
	Expect(recorder.Body.String()).NotTo(ContainSubstring("FEATURE_NOT_READY"))
	// A visible version answers the same alias for an unknown evidence id
	// with the item view's nondisclosing 404 (S29), never 503.
	recorder = request("/api/v1/versions/" + version.ID + "/source-evidence/simple/verifications")
	Expect(recorder.Code).To(Equal(404))
	Expect(recorder.Body.String()).NotTo(ContainSubstring("FEATURE_NOT_READY"))
})

var _ = It("TestAssessmentsRoutesServeCompositionAndCoverageValidation", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	u := f.create()
	f.put(u, "page.html", "file", []byte("<html>assessments</html>"))
	version := f.publish(u, nil)
	handler := New(f.query, nil, nil)
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+f.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	// A version without source-evidence rows and without observations or
	// captures yields the definite null applicable (§7.1 step 6c), never a
	// 503 feature gate.
	recorder := request("/api/v1/versions/" + version.ID + "/assessments")
	Expect(recorder.Code).To(Equal(200), recorder.Body.String())
	Expect(recorder.Body.String()).NotTo(ContainSubstring("FEATURE_NOT_READY"))
	Expect(recorder.Body.String()).To(ContainSubstring(`"applicable":null`), recorder.Body.String())
	Expect(recorder.Body.String()).To(ContainSubstring(`"items":[]`))
	Expect(recorder.Body.String()).To(ContainSubstring(`"next_cursor":null`), "the page always carries next_cursor (W3b L11)")
	// Scope and query validation stay typed.
	recorder = request("/api/v1/versions/" + version.ID + "/assessments?unexpected=1")
	Expect(recorder.Code).To(Equal(400))
	recorder = request("/api/v1/versions/" + version.ID + "/assessments?scope_kind=page")
	Expect(recorder.Code).To(Equal(400), "a page scope requires page_path")
	// The coverage endpoint requires and validates its set id.
	recorder = request("/api/v1/versions/" + version.ID + "/assessments/coverage")
	Expect(recorder.Code).To(Equal(400))
	recorder = request("/api/v1/versions/" + version.ID + "/assessments/coverage?coverage_set_id=00000000-0000-4000-8000-0000000000ff")
	Expect(recorder.Code).To(Equal(404))
	Expect(recorder.Body.String()).NotTo(ContainSubstring("FEATURE_NOT_READY"))
	details := func(recorder *httptest.ResponseRecorder) map[string]any {
		var body struct {
			Error struct {
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		Expect(json.Unmarshal(recorder.Body.Bytes(), &body)).To(Succeed(), recorder.Body.String())
		return body.Error.Details
	}
	// W3b L21: the 404 merges the named detail beside its reason.
	Expect(details(recorder)).To(Equal(map[string]any{"reason": "coverage_set_id", "coverage_set_id": "00000000-0000-4000-8000-0000000000ff"}))
	// W3b L19: attempt_run_id passes through the route to the attempt check.
	run, set := "00000000-0000-4000-8000-0000000000a1", "00000000-0000-4000-8000-0000000000a2"
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		if e := tx.PutObservationRun(domain.ObservationRun{ID: run, VersionID: version.ID, PrincipalID: "owner", OperationID: "00000000-0000-4000-8000-0000000000a3", RequestedSelection: []byte(`{"project_id":"p","mode":"current","source_ids":["git-1"]}`), ScopeJSON: []byte(`{"kind":"version","declared":true}`), CreatedAt: "2026-10-01T09:00:00.000Z"}); e != nil {
			return e
		}
		if e := tx.PutSourceObservation(domain.SourceObservation{ID: "00000000-0000-4000-8000-0000000000a4", RunID: run, VersionID: version.ID, SourceID: "git-1", Availability: "unavailable", Classification: "unavailable", Reason: "missing_source", ConfigRevision: 1, PermissionsRevision: 1, ObservedAt: "2026-10-01T09:00:00.000Z", OperationID: "00000000-0000-4000-8000-0000000000a3"}); e != nil {
			return e
		}
		return tx.PutCoverageSet(domain.CoverageSet{ID: set, VersionID: version.ID, RunID: run, ScopeJSON: []byte(`{"kind":"version","declared":true}`), UnitKind: "page", CapturedAt: "2026-10-01T09:00:00.000Z", Origin: "source_observation"})
	})).To(Succeed())
	recorder = request("/api/v1/versions/" + version.ID + "/assessments/coverage?coverage_set_id=" + set)
	Expect(recorder.Code).To(Equal(200), recorder.Body.String())
	recorder = request("/api/v1/versions/" + version.ID + "/assessments/coverage?coverage_set_id=" + set + "&attempt_run_id=00000000-0000-4000-8000-0000000000ff")
	Expect(recorder.Code).To(Equal(404), recorder.Body.String())
	Expect(details(recorder)).To(Equal(map[string]any{"reason": "attempt_run_id", "attempt_run_id": "00000000-0000-4000-8000-0000000000ff"}))
	// An unknown version is a nondisclosing 404 on both reads.
	recorder = request("/api/v1/versions/00000000-0000-4000-8000-000000000002/assessments")
	Expect(recorder.Code).To(Equal(404))
	recorder = request("/api/v1/versions/00000000-0000-4000-8000-000000000002/assessments/coverage?coverage_set_id=00000000-0000-4000-8000-0000000000ff")
	Expect(recorder.Code).To(Equal(404))
})

type noopRenderServer struct{}

func (noopRenderServer) Serve(l net.Listener) error {
	_ = l.Close()
	return nil
}

var _ = It("TestPassiveCapabilitiesAndRenderSessionRenewHonorIdleExpiryWhileOwnerReadsTouch", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	f.auth.Now = func() time.Time { return time.Unix(0, now.Load()) }

	appState := filepath.Join(filepath.Dir(f.selection.DataDir), "appstate")
	Expect(os.MkdirAll(appState, 0700)).To(Succeed())
	lazy := render.NewLazy(func() (*render.Service, error) {
		return render.New(f.store, f.auth, f.store, render.Options{
			AppStateDir:      appState,
			Now:              f.auth.Now,
			ManagementOrigin: func() string { return f.server.URL },
			NewServer: func(origin string, binding render.Binding) render.Server {
				return noopRenderServer{}
			},
		})
	})
	DeferCleanup(func() { _ = lazy.Shutdown() })
	origin := f.server.URL
	host := strings.TrimPrefix(origin, "http://")
	handler, err := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{host}, Origins: []string{origin}}, routes{New(f.query, lazy, nil), httpimports.New(f.imports)})
	Expect(err).NotTo(HaveOccurred())
	f.server.Config.Handler = handler

	u := f.create()
	f.put(u, "hello.html", "file", []byte("<html>passive-renew</html>"))
	version := f.publish(u, nil)

	configuration, err := f.store.Configuration(context.Background())
	Expect(err).NotTo(HaveOccurred())
	var config map[string]any
	Expect(json.Unmarshal(configuration.Value, &config)).To(Succeed())
	config["security"].(map[string]any)["session_idle_seconds"] = int64(3)
	configuration.Value, err = json.Marshal(config)
	Expect(err).NotTo(HaveOccurred())
	configuration.Actor = "fixture"
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		return tx.SetConfiguration(configuration, configuration.Revision)
	})).To(Succeed())

	// Opening a render session is a genuine owner action that touches LastSeenAt.
	status, openData := f.json("POST", "/api/v1/versions/"+version.ID+"/render-sessions", map[string]any{})
	want(t, status, 201, openData)
	opened := decoded[struct {
		SessionID string `json:"session_id"`
	}](t, openData)
	Expect(opened.SessionID).NotTo(BeEmpty())

	status, _, sessBody := f.request("GET", "/api/v1/session", nil, nil)
	want(t, status, 200, sessBody)
	before := decoded[map[string]any](t, sessBody)
	beforeLastSeen := before["last_seen_at"]
	beforeRevision := before["revision"]

	// Advance 1s: passive capabilities (GET/HEAD) and render-session renew must not touch LastSeenAt or revision.
	now.Add(int64(time.Second))
	status, _, capBody := f.request("GET", "/api/v1/capabilities", nil, nil)
	want(t, status, 200, capBody)
	status, _, headBody := f.request("HEAD", "/api/v1/capabilities", nil, nil)
	want(t, status, 200, headBody)
	status, _, renewBody := f.request("POST", "/api/v1/versions/"+version.ID+"/render-sessions/"+opened.SessionID+"/renew", bytes.NewReader([]byte(`{}`)), map[string]string{"Content-Type": "application/json"})
	want(t, status, 200, renewBody)

	status, _, sessBody = f.request("GET", "/api/v1/session", nil, nil)
	want(t, status, 200, sessBody)
	afterPassive := decoded[map[string]any](t, sessBody)
	Expect(afterPassive["last_seen_at"]).To(Equal(beforeLastSeen))
	Expect(afterPassive["revision"]).To(Equal(beforeRevision))

	// Advance another 1s and execute active owner reads (documents + attachment download): LastSeenAt and revision advance.
	now.Add(int64(time.Second))
	status, _, docBody := f.request("GET", "/api/v1/documents", nil, nil)
	want(t, status, 200, docBody)
	status, _, sessBody = f.request("GET", "/api/v1/session", nil, nil)
	want(t, status, 200, sessBody)
	afterDoc := decoded[map[string]any](t, sessBody)
	Expect(afterDoc["last_seen_at"]).NotTo(Equal(beforeLastSeen))
	Expect(afterDoc["revision"].(float64)).To(BeNumerically(">", beforeRevision.(float64)))

	now.Add(int64(time.Second))
	status, _, fileBody := f.request("GET", "/api/v1/versions/"+version.ID+"/file?path=hello.html", nil, nil)
	want(t, status, 200, fileBody)
	status, _, sessBody = f.request("GET", "/api/v1/session", nil, nil)
	want(t, status, 200, sessBody)
	afterFile := decoded[map[string]any](t, sessBody)
	Expect(afterFile["last_seen_at"]).NotTo(Equal(afterDoc["last_seen_at"]))
	Expect(afterFile["revision"].(float64)).To(BeNumerically(">", afterDoc["revision"].(float64)))

	// Passive capabilities and render-session renew at +2s keep the session alive only within the 3s idle window, without resetting LastSeenAt.
	now.Add(int64(2 * time.Second))
	status, _, capBody = f.request("GET", "/api/v1/capabilities", nil, nil)
	want(t, status, 200, capBody)
	status, _, renewBody = f.request("POST", "/api/v1/versions/"+version.ID+"/render-sessions/"+opened.SessionID+"/renew", bytes.NewReader([]byte(`{}`)), map[string]string{"Content-Type": "application/json"})
	want(t, status, 200, renewBody)

	// Advance +2s more (total 4s > 3s idle limit since last active touch): passive and active routes fail with 401 SESSION_EXPIRED and cannot revive the credential.
	now.Add(int64(2 * time.Second))
	status, _, expCap := f.request("GET", "/api/v1/capabilities", nil, nil)
	Expect(status).To(Equal(401))
	Expect(string(expCap)).To(ContainSubstring(`"SESSION_EXPIRED"`))
	status, _, expRenew := f.request("POST", "/api/v1/versions/"+version.ID+"/render-sessions/"+opened.SessionID+"/renew", bytes.NewReader([]byte(`{}`)), map[string]string{"Content-Type": "application/json"})
	Expect(status).To(Equal(401))
	Expect(string(expRenew)).To(ContainSubstring(`"SESSION_EXPIRED"`))
	status, _, expDoc := f.request("GET", "/api/v1/documents", nil, nil)
	Expect(status).To(Equal(401))
	Expect(string(expDoc)).To(ContainSubstring(`"SESSION_EXPIRED"`))
})

type postCommitGuardStore struct {
	app.Store
	armPostCommitGuardFailure atomic.Bool
	failNextAuthWrite         atomic.Bool
}

type authGuardInterceptStore struct {
	*postCommitGuardStore
}

func (s *postCommitGuardStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	err := s.Store.Write(ctx, fn)
	if err == nil && s.armPostCommitGuardFailure.CompareAndSwap(true, false) {
		s.failNextAuthWrite.Store(true)
	}
	return err
}

func (s *authGuardInterceptStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	if s.failNextAuthWrite.CompareAndSwap(true, false) {
		return errors.New("simulated post-commit Auth.Guard store failure")
	}
	return s.Store.Write(ctx, fn)
}

type configAndQueryRoutes struct {
	config  *httpconfiguration.Handler
	queries *Handler
	imports http.Handler
}

func (m configAndQueryRoutes) Handles(r *http.Request) bool {
	return m.config.Handles(r) || m.queries.Handles(r) || m.imports.(interface{ Handles(*http.Request) bool }).Handles(r)
}

func (m configAndQueryRoutes) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case m.config.Handles(r):
		m.config.ServeHTTP(w, r)
	case m.queries.Handles(r):
		m.queries.ServeHTTP(w, r)
	default:
		m.imports.ServeHTTP(w, r)
	}
}

var _ = It("TestPolicyCommitDurableBeforePostCommitGuard503And401ReconcileViaOperationReceipt", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	f.auth.Now = func() time.Time { return time.Unix(0, now.Load()) }

	intercept := &postCommitGuardStore{Store: f.store}
	f.auth.Store = &authGuardInterceptStore{postCommitGuardStore: intercept}
	cfgService := appconfiguration.New(intercept, f.auth, nil)
	cfgService.Now = f.auth.Now

	origin := f.server.URL
	host := strings.TrimPrefix(origin, "http://")
	handler, err := httpauth.New(
		f.auth,
		httpauth.Policy{Hosts: []string{host}, Origins: []string{origin}},
		configAndQueryRoutes{
			config:  httpconfiguration.New(cfgService),
			queries: New(f.query, nil, nil),
			imports: httpimports.New(f.imports),
		},
	)
	Expect(err).NotTo(HaveOccurred())
	f.server.Config.Handler = handler

	// 1. Create a security policy plan (revision 1 -> session_idle_seconds: 300).
	plan1Body := map[string]any{
		"scope":                    map[string]any{"kind": "library"},
		"expected_config_revision": 1,
		"changes": map[string]any{
			"security": map[string]any{
				"remember_device_default":  false,
				"session_idle_seconds":     300,
				"session_absolute_seconds": nil,
			},
		},
	}
	plan1Raw, err := json.Marshal(plan1Body)
	Expect(err).NotTo(HaveOccurred())
	status, _, plan1Resp := f.request("POST", "/api/v1/configuration/plans", bytes.NewReader(plan1Raw), map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": id(t),
	})
	want(t, status, 200, plan1Resp)
	plan1 := decoded[struct {
		PlanID             string `json:"plan_id"`
		BaseConfigRevision int64  `json:"base_config_revision"`
	}](t, plan1Resp)
	Expect(plan1.PlanID).NotTo(BeEmpty())
	Expect(plan1.BaseConfigRevision).To(Equal(int64(1)))

	// Arm a single store failure on the post-commit Auth.Guard right after
	// Commit's durable SetConfiguration + CompleteOperation transaction commits.
	intercept.armPostCommitGuardFailure.Store(true)
	commit1Raw, err := json.Marshal(map[string]any{
		"expected_config_revision": 1,
		"confirmed":                true,
	})
	Expect(err).NotTo(HaveOccurred())
	status, _, commit1Resp := f.request("POST", "/api/v1/configuration/plans/"+plan1.PlanID+"/commit", bytes.NewReader(commit1Raw), map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": plan1.PlanID,
	})
	Expect(status).To(Equal(503), string(commit1Resp))
	Expect(string(commit1Resp)).To(ContainSubstring(`"code":"SERVICE_RECOVERING"`))
	Expect(string(commit1Resp)).To(ContainSubstring(`"retryable":true`))

	// The session remains valid (503 is not auth loss), the policy is durably
	// committed at revision 2 in SQLite, and GET /api/v1/operations/{plan_id}
	// returns the completed 200 receipt with the same token and no duplicate POST.
	status, _, sessCheck := f.request("GET", "/api/v1/session", nil, nil)
	want(t, status, 200, sessCheck)

	status, _, op1Resp := f.request("GET", "/api/v1/operations/"+plan1.PlanID, nil, nil)
	want(t, status, 200, op1Resp)
	op1 := decoded[struct {
		OperationID    string `json:"operation_id"`
		State          string `json:"state"`
		OriginalStatus int    `json:"original_status"`
		ResultMetadata struct {
			PlanID         string `json:"plan_id"`
			ConfigRevision int64  `json:"config_revision"`
		} `json:"result_metadata"`
	}](t, op1Resp)
	Expect(op1.OperationID).To(Equal(plan1.PlanID))
	Expect(op1.State).To(Equal("completed"))
	Expect(op1.OriginalStatus).To(Equal(200))
	Expect(op1.ResultMetadata.PlanID).To(Equal(plan1.PlanID))
	Expect(op1.ResultMetadata.ConfigRevision).To(Equal(int64(2)))

	status, _, cfgAfter1 := f.request("GET", "/api/v1/configuration", nil, nil)
	want(t, status, 200, cfgAfter1)
	Expect(string(cfgAfter1)).To(ContainSubstring(`"config_revision":2`))
	Expect(string(cfgAfter1)).To(ContainSubstring(`"session_idle_seconds":300`))

	// 2. Advance clock by 2s so the current session is 2s old, then commit a plan
	// setting session_absolute_seconds: 1. Commit durably writes revision 3 and
	// completes the operation receipt under the pre-commit policy, and the final
	// Auth.Guard immediately expires the committing session with 401 SESSION_EXPIRED.
	now.Add(int64(2 * time.Second))
	plan2Body := map[string]any{
		"scope":                    map[string]any{"kind": "library"},
		"expected_config_revision": 2,
		"changes": map[string]any{
			"security": map[string]any{
				"remember_device_default":  false,
				"session_idle_seconds":     300,
				"session_absolute_seconds": 1,
			},
		},
	}
	plan2Raw, err := json.Marshal(plan2Body)
	Expect(err).NotTo(HaveOccurred())
	status, _, plan2Resp := f.request("POST", "/api/v1/configuration/plans", bytes.NewReader(plan2Raw), map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": id(t),
	})
	want(t, status, 200, plan2Resp)
	plan2 := decoded[struct {
		PlanID             string `json:"plan_id"`
		BaseConfigRevision int64  `json:"base_config_revision"`
	}](t, plan2Resp)
	Expect(plan2.PlanID).NotTo(BeEmpty())
	Expect(plan2.BaseConfigRevision).To(Equal(int64(2)))

	commit2Raw, err := json.Marshal(map[string]any{
		"expected_config_revision": 2,
		"confirmed":                true,
	})
	Expect(err).NotTo(HaveOccurred())
	status, _, commit2Resp := f.request("POST", "/api/v1/configuration/plans/"+plan2.PlanID+"/commit", bytes.NewReader(commit2Raw), map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": plan2.PlanID,
	})
	Expect(status).To(Equal(401), string(commit2Resp))
	Expect(string(commit2Resp)).To(ContainSubstring(`"code":"SESSION_EXPIRED"`))

	// The old session token is now expired.
	status, _, oldSessResp := f.request("GET", "/api/v1/session", nil, nil)
	Expect(status).To(Equal(401))
	Expect(string(oldSessResp)).To(ContainSubstring(`"SESSION_EXPIRED"`))

	// Issue a replacement same-owner session at current clock (age 0s < 1s) and
	// reconcile the original operation receipt via GET /api/v1/operations/{plan_id}.
	replacementPair, err := f.auth.CreatePairing(context.Background(), f.server.URL, "replacement owner", "query-fixture")
	Expect(err).NotTo(HaveOccurred())
	_, err = f.auth.BootstrapDecide(context.Background(), domain.PairingDecision{
		ID:          replacementPair.ID,
		Code:        replacementPair.Code,
		Origin:      replacementPair.Origin,
		ETag:        replacementPair.ETag,
		OperationID: id(t),
		Action:      "approve",
	})
	Expect(err).NotTo(HaveOccurred())
	replacementPoll, err := f.auth.Poll(context.Background(), replacementPair.ID, replacementPair.PollingSecret, replacementPair.Origin)
	Expect(err).NotTo(HaveOccurred())
	f.token = replacementPoll.Token

	status, _, op2Resp := f.request("GET", "/api/v1/operations/"+plan2.PlanID, nil, nil)
	want(t, status, 200, op2Resp)
	op2 := decoded[struct {
		OperationID    string `json:"operation_id"`
		State          string `json:"state"`
		OriginalStatus int    `json:"original_status"`
		ResultMetadata struct {
			PlanID         string `json:"plan_id"`
			ConfigRevision int64  `json:"config_revision"`
		} `json:"result_metadata"`
	}](t, op2Resp)
	Expect(op2.OperationID).To(Equal(plan2.PlanID))
	Expect(op2.State).To(Equal("completed"))
	Expect(op2.OriginalStatus).To(Equal(200))
	Expect(op2.ResultMetadata.PlanID).To(Equal(plan2.PlanID))
	Expect(op2.ResultMetadata.ConfigRevision).To(Equal(int64(3)))

	status, _, cfgAfter2 := f.request("GET", "/api/v1/configuration", nil, nil)
	want(t, status, 200, cfgAfter2)
	Expect(string(cfgAfter2)).To(ContainSubstring(`"config_revision":3`))
	Expect(string(cfgAfter2)).To(ContainSubstring(`"session_absolute_seconds":1`))
})
