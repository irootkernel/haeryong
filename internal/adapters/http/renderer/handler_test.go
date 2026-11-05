package renderer_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/net/html"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	httprenderer "haeryong.local/haeryong/internal/adapters/http/renderer"
	httpv1 "haeryong.local/haeryong/internal/adapters/http/v1"
	"haeryong.local/haeryong/internal/application/auth"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/render"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	_ "modernc.org/sqlite"
)

var ctx = context.Background()

func key() string {
	id, err := service.NewID()
	Expect(err).NotTo(HaveOccurred())
	return id
}

type delivery struct {
	Origin  string `json:"render_origin"`
	ID      string `json:"session_id"`
	Handoff struct {
		URL    string `json:"url"`
		Secret string `json:"secret"`
	} `json:"handoff"`
}

// Fault injection wraps only the real store's transaction entrance. Successful
// requests still exercise real owner records, SQLite receipts and object pins.
type faultStore struct {
	app.Store
	fail atomic.Bool
	gate atomic.Pointer[writeGate]
}

type writeGate struct {
	entered, resume chan struct{}
	once, released  sync.Once
}

func (g *writeGate) release() { g.released.Do(func() { close(g.resume) }) }

func (s *faultStore) Write(c context.Context, f func(app.WriteTx) error) error {
	if s.fail.Load() {
		return errors.New("temporary fixture store outage")
	}
	if gate := s.gate.Load(); gate != nil {
		gate.once.Do(func() { close(gate.entered) })
		select {
		case <-gate.resume:
		case <-c.Done():
			return c.Err()
		}
	}
	return s.Store.Write(c, f)
}

type fixture struct {
	store                                   *foundation.Store
	control                                 *faultStore
	auth                                    *auth.Service
	core                                    *render.Service
	token, version, document, dbPath, state string
	now                                     atomic.Int64
	client                                  *http.Client
	bytes                                   query.Bytes
	redirectBase                            string
}

func newFixture() *fixture {
	GinkgoHelper()
	t := GinkgoT()
	selection := service.Selection{Profile: "test", DataDir: filepath.Join(t.TempDir(), "library")}
	_, err := service.Initialize(selection)
	Expect(err).NotTo(HaveOccurred())
	w, err := service.OpenWriter(selection)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(w.Close()).To(Succeed()) })
	Expect(w.PrepareStorageBootstrap(nil)).To(Succeed())
	f := &fixture{store: foundation.New(nil), dbPath: filepath.Join(selection.DataDir, "storage", "library.sqlite"), state: filepath.Join(t.TempDir(), "app"), redirectBase: "http://127.0.0.1:9270"}
	_, closeStore, err := f.store.Prepare(ctx, w.Profile)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(closeStore()).To(Succeed()) })
	Expect(w.MarkStorageReady(nil)).To(Succeed())
	f.auth = auth.New(f.store)
	p, err := f.auth.CreatePairing(ctx, "http://127.0.0.1:9270", "renderer fixture", "test")
	Expect(err).NotTo(HaveOccurred())
	_, err = f.auth.BootstrapDecide(ctx, domain.PairingDecision{ID: p.ID, Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: key(), Action: "approve"})
	Expect(err).NotTo(HaveOccurred())
	poll, err := f.auth.Poll(ctx, p.ID, p.PollingSecret, p.Origin)
	Expect(err).NotTo(HaveOccurred())
	f.token = poll.Token
	health, err := f.store.Health(ctx)
	Expect(err).NotTo(HaveOccurred())
	imports := importer.New(f.store, f.auth, f.store, staging.New())
	target, err := json.Marshal(map[string]any{"project_id": health.Library.UnfiledProjectID, "document_id": nil})
	Expect(err).NotTo(HaveOccurred())
	r, err := imports.Create(ctx, f.token, key(), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: target})
	Expect(err).NotTo(HaveOccurred())
	var upload struct {
		ID string `json:"upload_id"`
	}
	Expect(json.Unmarshal(r.Body, &upload)).To(Succeed())
	var ack importer.ChunkResult
	for name, body := range map[string]string{
		"hello.html":           "<!doctype html><html><head id='authored'><title>Reader</title></head><body>한글<script>window.useful = true</script></body></html>",
		"pages/café.html":      "<!doctype html><head></head><body>nested</body>",
		"api/sample.json":      `{"authored":true}`,
		"__haeryong/loader.js": "/* authored loader filename */",
		"legacy.css":           "@charset \"windows-1252\"; body::after { content: 'caf\xe9'; }",
		"legacy.js":            "window.legacy='caf\xe9';",
		"empty.bin":            "",
		"large.bin":            strings.Repeat("0123456789abcdef", 16384),
		"tricky/dir/pa\tge\rge\nna\u00efme \"q\"<&>.html": "<!doctype html><head></head><body>tricky html</body>",
		"adjacent/crlf\r\npage.html":                      "<!doctype html><head></head><body>adjacent crlf identity</body>",
		"tricky/data/pa+th%20name&q=1;z=?.json":           `{"path":"header identity"}`,
		"tricky/spa ce/uni \u2603 and \ud55c\uae00.bin":   "plain-bytes",
	} {
		sum := sha256.Sum256([]byte(body))
		size := int64(len(body))
		ack, err = imports.Put(ctx, f.token, upload.ID, importer.Chunk{Path: name, Kind: "file", Hash: hex.EncodeToString(sum[:]), Length: &size, Final: true}, strings.NewReader(body))
		Expect(err).NotTo(HaveOccurred())
	}
	ack, err = imports.Put(ctx, f.token, upload.ID, importer.Chunk{Path: "alias.html", Kind: "symlink", LinkTarget: "hello.html"}, strings.NewReader(`{"link_target":"hello.html"}`))
	Expect(err).NotTo(HaveOccurred())
	ack, err = imports.Put(ctx, f.token, upload.ID, importer.Chunk{Path: "alias", Kind: "symlink", LinkTarget: "hello.html"}, strings.NewReader(`{"link_target":"hello.html"}`))
	Expect(err).NotTo(HaveOccurred())
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	r, err = imports.Complete(ctx, f.token, upload.ID, key(), cp)
	Expect(err).NotTo(HaveOccurred())
	var job struct {
		ID string `json:"job_id"`
	}
	Expect(json.Unmarshal(r.Body, &job)).To(Succeed())
	Expect(imports.Process(ctx, job.ID)).To(Succeed())
	r, err = imports.Import(ctx, f.token, key(), importer.ImportRequest{UploadID: upload.ID, DocumentPresent: true, VersionID: key()})
	Expect(err).NotTo(HaveOccurred())
	Expect(json.Unmarshal(r.Body, &job)).To(Succeed())
	Expect(imports.Process(ctx, job.ID)).To(Succeed())
	finished, err := f.store.Job(ctx, job.ID)
	Expect(err).NotTo(HaveOccurred())
	var published struct {
		Document string `json:"document_id"`
		Version  string `json:"version_id"`
	}
	Expect(json.Unmarshal(finished.Result, &published)).To(Succeed())
	f.document, f.version = published.Document, published.Version
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	port := listener.Addr().(*net.TCPAddr).Port
	Expect(listener.Close()).To(Succeed())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		config, err := tx.Configuration()
		if err != nil {
			return err
		}
		var value map[string]any
		if err = json.Unmarshal(config.Value, &value); err != nil {
			return err
		}
		value["network"].(map[string]any)["renderer_pool"] = map[string]any{"base_port": port, "count": 1}
		config.Value, err = json.Marshal(value)
		if err != nil {
			return err
		}
		return tx.SetConfiguration(config, config.Revision)
	})).To(Succeed())
	f.control = &faultStore{Store: f.store}
	f.bytes = f.store
	f.now.Store(time.Now().UnixNano())
	f.start()
	jar, err := cookiejar.New(nil)
	Expect(err).NotTo(HaveOccurred())
	f.client = &http.Client{Jar: jar, Timeout: 5 * time.Second}
	DeferCleanup(f.client.CloseIdleConnections)
	return f
}

func (f *fixture) start() {
	var core *render.Service
	var err error
	core, err = render.New(f.control, auth.New(f.control), f.bytes, render.Options{AppStateDir: f.state, Lease: time.Minute, Now: func() time.Time { return time.Unix(0, f.now.Load()) }, NewServer: func(origin string, binding render.Binding) render.Server {
		server := httprenderer.New(core, origin, binding)
		if f.redirectBase != "" {
			base := f.redirectBase
			server.SetPublicReaderBase(func() string { return base })
		}
		return server
	}})
	Expect(err).NotTo(HaveOccurred())
	f.core = core
	DeferCleanup(func() { Expect(core.Shutdown()).To(Succeed()) })
}

func (f *fixture) open() delivery {
	GinkgoHelper()
	operation := key()
	r, err := f.core.Open(ctx, f.token, operation, f.version)
	Expect(err).NotTo(HaveOccurred())
	replay, err := f.core.Open(ctx, f.token, operation, f.version)
	Expect(err).NotTo(HaveOccurred())
	Expect(replay.Body).To(Equal(r.Body))
	var d delivery
	Expect(json.Unmarshal(r.Body, &d)).To(Succeed())
	return d
}

func (f *fixture) handoff(d delivery) (int, []byte) {
	response, err := f.client.PostForm(d.Handoff.URL+"?page="+url.QueryEscape("/hello.html?q=%23#한글"), url.Values{"secret": {d.Handoff.Secret}})
	Expect(err).NotTo(HaveOccurred())
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	Expect(err).NotTo(HaveOccurred())
	return response.StatusCode, body
}

func (f *fixture) get(d delivery, name string, headers map[string]string) (*http.Response, []byte) {
	GinkgoHelper()
	r, err := http.NewRequest(http.MethodGet, d.Origin+name, nil)
	Expect(err).NotTo(HaveOccurred())
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	response, err := f.client.Do(r)
	Expect(err).NotTo(HaveOccurred())
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	Expect(err).NotTo(HaveOccurred())
	return response, body
}

func (f *fixture) lifecycle(value string) {
	db, err := sql.Open("sqlite", "file:"+f.dbPath+"?mode=rw")
	Expect(err).NotTo(HaveOccurred())
	defer db.Close()
	_, err = db.Exec("UPDATE documents SET lifecycle=? WHERE document_id=?", value, f.document)
	Expect(err).NotTo(HaveOccurred())
}

var _ = It("round-trips an adjacent-CRLF page identity through the real socket and parser", Label("persistence"), func() {
	f := newFixture()
	d := f.open()
	status, _ := f.handoff(d)
	Expect(status).To(Equal(200))
	page := "adjacent/crlf\r\npage.html"
	target := "/" + (&url.URL{Path: page}).EscapedPath()
	response, body := f.get(d, target, nil)
	Expect(response.StatusCode).To(Equal(200))
	parsed, err := html.Parse(bytes.NewReader(body))
	Expect(err).NotTo(HaveOccurred())
	var found string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "script" {
			for _, attr := range n.Attr {
				if attr.Key == "data-haeryong-page" {
					found = attr.Val
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(parsed)
	decoded, err := url.PathUnescape(found)
	Expect(err).NotTo(HaveOccurred())
	Expect(decoded).To(Equal(page), "real socket/parser normalized the adjacent CRLF page identity")
})

var _ = It("delivers actual verified pages, ranges and authored API-like filenames only under the narrow origin cookie", Label("persistence"), func() {
	f := newFixture()
	d := f.open()
	status, landing := f.handoff(d)
	Expect(status).To(Equal(200))
	Expect(string(landing)).To(ContainSubstring("q=%23#"))
	response, whole := f.get(d, "/hello.html", nil)
	Expect(response.StatusCode).To(Equal(200))
	Expect(string(whole)).To(ContainSubstring("<head id='authored'><script "))
	Expect(string(whole)).To(ContainSubstring("window.useful = true"))
	etag := response.Header.Get("ETag")
	Expect(etag).To(HavePrefix(`"hyr-`))
	Expect(response.Header.Get("Haeryong-Content-Digest")).NotTo(BeEmpty())
	response, part := f.get(d, "/hello.html", map[string]string{"Range": "bytes=12-45", "If-Range": etag})
	Expect(response.StatusCode).To(Equal(206))
	Expect(part).To(Equal(whole[12:46]))
	response, part = f.get(d, "/hello.html", map[string]string{"If-None-Match": etag})
	Expect(response.StatusCode).To(Equal(304))
	Expect(part).To(BeEmpty())
	response, part = f.get(d, "/api/sample.json", nil)
	Expect(response.StatusCode).To(Equal(200))
	Expect(string(part)).To(Equal(`{"authored":true}`))
	response, part = f.get(d, "/__haeryong/loader.js", nil)
	Expect(response.StatusCode).To(Equal(200))
	Expect(string(part)).To(Equal("/* authored loader filename */"))
	response, part = f.get(d, "/__haeryong/loader.js?"+render.LoaderQuery, nil)
	Expect(response.StatusCode).To(Equal(200))
	Expect(string(part)).To(ContainSubstring("context.transfer.begin"))
	response, _ = f.get(d, "/__haeryong/loader.js?"+render.LoaderQuery, map[string]string{"Sec-Fetch-Site": "same-site"})
	Expect(response.StatusCode).To(Equal(403))
	response, _ = f.get(d, "/hello.html", map[string]string{"Origin": "http://127.0.0.1:9270"})
	Expect(response.StatusCode).To(Equal(403))
	response, _ = f.get(d, "/empty.bin", map[string]string{"Range": "bytes=0-0"})
	Expect(response.StatusCode).To(Equal(416))
	Expect(response.Header.Get("Content-Range")).To(Equal("bytes */0"))
	response, part = f.get(d, "/alias.html", nil)
	Expect(response.StatusCode).To(Equal(200))
	Expect(string(part)).To(ContainSubstring(`data-haeryong-page="hello.html"`))
	response, part = f.get(d, "/alias", nil)
	Expect(response.StatusCode).To(Equal(200))
	Expect(part).To(Equal(whole), "suffix-less navigation keeps the canonical HTML derivative byte-identical")
	status, _ = f.handoff(d)
	Expect(status).To(Equal(401))
})

var _ = It("preserves a transient handoff and active progress but expires idle delivery", Label("persistence"), func() {
	f := newFixture()
	d := f.open()
	f.control.fail.Store(true)
	status, _ := f.handoff(d)
	Expect(status).To(Equal(503))
	f.control.fail.Store(false)
	status, _ = f.handoff(d)
	Expect(status).To(Equal(200))
	for range 3 {
		f.now.Add(int64(45 * time.Second))
		response, _ := f.get(d, "/hello.html", nil)
		Expect(response.StatusCode).To(Equal(200))
	}
	f.control.fail.Store(true)
	response, _ := f.get(d, "/hello.html", nil)
	Expect(response.StatusCode).To(Equal(503))
	Expect(response.Cookies()).To(BeEmpty())
	f.control.fail.Store(false)
	f.now.Add(int64(61 * time.Second))
	response, _ = f.get(d, "/hello.html", nil)
	Expect(response.StatusCode).To(Equal(401))
	_, err := f.core.Renew(ctx, f.token, f.version, d.ID)
	Expect(err).NotTo(HaveOccurred())
	// Expiry is not logout; establish a new offer explicitly, rather than
	// guessing a cookie discarded by the HTTP denial.
	d = f.open()
	status, _ = f.handoff(d)
	Expect(status).To(Equal(200))
})

var _ = It("allows terminal close after trash and rebinds the sticky origin after shutdown without reviving old delivery", Label("persistence"), func() {
	f := newFixture()
	d := f.open()
	status, _ := f.handoff(d)
	Expect(status).To(Equal(200))
	f.lifecycle("trash")
	_, err := f.core.Close(ctx, f.token, f.version, d.ID)
	Expect(err).NotTo(HaveOccurred())
	f.lifecycle("active")
	response, _ := f.get(d, "/hello.html", nil)
	Expect(response.StatusCode).To(Equal(401))
	Expect(f.core.Shutdown()).To(Succeed())
	f.start()
	next := f.open()
	Expect(next.Origin).To(Equal(d.Origin))
	Expect(next.ID).NotTo(Equal(d.ID))
	response, _ = f.get(next, "/hello.html", nil)
	Expect(response.StatusCode).To(Equal(401))
	status, _ = f.handoff(next)
	Expect(status).To(Equal(200))
	response, _ = f.get(next, "/hello.html", nil)
	Expect(response.StatusCode).To(Equal(200))
})

type observedBytes struct {
	query.Bytes
	wrap func(context.Context, io.ReadSeeker) io.ReadSeeker
}

func (b observedBytes) Read(ctx context.Context, object domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) error {
	return b.Bytes.Read(ctx, object, pin, func(reader io.ReadSeeker) error { return consume(b.wrap(ctx, reader)) })
}

type timedRead struct {
	io.ReadSeeker
	now *atomic.Int64
}

func (r timedRead) Read(out []byte) (int, error) {
	r.now.Add(int64(45 * time.Second))
	return r.ReadSeeker.Read(out)
}

var _ = It("keeps one progressing HTTP response alive beyond its initial lease", Label("persistence"), func() {
	f := newFixture()
	Expect(f.core.Shutdown()).To(Succeed())
	f.bytes = observedBytes{Bytes: f.store, wrap: func(_ context.Context, r io.ReadSeeker) io.ReadSeeker { return timedRead{ReadSeeker: r, now: &f.now} }}
	f.start()
	d := f.open()
	status, _ := f.handoff(d)
	Expect(status).To(Equal(200))
	started := f.now.Load()
	response, body := f.get(d, "/large.bin", nil)
	Expect(response.StatusCode).To(Equal(200))
	Expect(string(body)).To(Equal(strings.Repeat("0123456789abcdef", 16384)))
	Expect(time.Duration(f.now.Load() - started)).To(BeNumerically(">", time.Minute))
})

var _ = It("redeems a handoff at most once under concurrent requests and permits the reserved consumer to complete", Label("persistence"), func() {
	f := newFixture()
	d := f.open()
	gate := &writeGate{entered: make(chan struct{}), resume: make(chan struct{})}
	DeferCleanup(gate.release)
	f.control.gate.Store(gate)
	type outcome struct {
		code int
		err  error
	}
	result := make(chan outcome, 1)
	go func() {
		response, err := f.client.PostForm(d.Handoff.URL, url.Values{"secret": {d.Handoff.Secret}})
		code := 0
		if response != nil {
			code = response.StatusCode
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
		result <- outcome{code, err}
	}()
	Eventually(gate.entered, "2s").Should(BeClosed())
	status, _ := f.handoff(d)
	Expect(status).To(Equal(503))
	gate.release()
	var first outcome
	Eventually(result, "2s").Should(Receive(&first))
	Expect(first.err).NotTo(HaveOccurred())
	Expect(first.code).To(Equal(200))
	f.control.gate.Store(nil)
	status, _ = f.handoff(d)
	Expect(status).To(Equal(401))
})

type blockedRead struct {
	io.ReadSeeker
	ctx      context.Context
	entered  chan struct{}
	once     *sync.Once
	canceled chan struct{}
	release  <-chan struct{}
}

func (r blockedRead) Read(out []byte) (int, error) {
	r.once.Do(func() { close(r.entered) })
	<-r.ctx.Done()
	if r.canceled != nil {
		close(r.canceled)
	}
	if r.release != nil {
		<-r.release
	}
	return 0, r.ctx.Err()
}

var _ = It("drains an active verified read and releases its durable pin before renderer shutdown returns", Label("persistence"), func() {
	f := newFixture()
	Expect(f.core.Shutdown()).To(Succeed())
	entered := make(chan struct{})
	var once sync.Once
	f.bytes = observedBytes{Bytes: f.store, wrap: func(c context.Context, r io.ReadSeeker) io.ReadSeeker {
		return blockedRead{ReadSeeker: r, ctx: c, entered: entered, once: &once}
	}}
	f.start()
	d := f.open()
	status, _ := f.handoff(d)
	Expect(status).To(Equal(200))
	finished := make(chan error, 1)
	go func() {
		request, _ := http.NewRequest("GET", d.Origin+"/large.bin", nil)
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		response, err := f.client.Do(request)
		if response != nil {
			_, readErr := io.Copy(io.Discard, response.Body)
			response.Body.Close()
			err = errors.Join(err, readErr)
		}
		finished <- err
	}()
	Eventually(entered, "2s").Should(BeClosed())
	db, err := sql.Open("sqlite", "file:"+f.dbPath+"?mode=ro")
	Expect(err).NotTo(HaveOccurred())
	defer db.Close()
	var pins int
	Expect(db.QueryRow("SELECT count(*) FROM object_pins WHERE owner_kind='read'").Scan(&pins)).To(Succeed())
	Expect(pins).To(Equal(1))
	Expect(f.core.Shutdown()).To(Succeed())
	Expect(db.QueryRow("SELECT count(*) FROM object_pins WHERE owner_kind='read'").Scan(&pins)).To(Succeed())
	Expect(pins).To(BeZero())
	Eventually(finished, "2s").Should(Receive())
	f.bytes = f.store
	f.start()
	next := f.open()
	Expect(next.Origin).To(Equal(d.Origin))
})

var _ = It("rejects corrupted HTML before delivering authored bytes or a success validator", Label("persistence"), func() {
	f := newFixture()
	d := f.open()
	status, _ := f.handoff(d)
	Expect(status).To(Equal(200))
	content, err := f.core.ResolveVersionContent(ctx, f.version, "hello.html")
	Expect(err).NotTo(HaveOccurred())
	objectPath := filepath.Join(filepath.Dir(f.dbPath), "objects", content.Hash)
	Expect(os.WriteFile(objectPath, []byte(strings.Repeat("x", int(content.Size))), 0600)).To(Succeed())
	response, body := f.get(d, "/hello.html", nil)
	Expect(response.StatusCode).To(Equal(503))
	Expect(string(body)).To(ContainSubstring("content integrity"))
	Expect(string(body)).NotTo(ContainSubstring("window.useful"))
	Expect(response.Header.Get("ETag")).To(BeEmpty())
	Expect(response.Header.Get("Haeryong-Content-Digest")).To(BeEmpty())
})

var _ = It("rejects hidden delivery renewal without logging out its valid owner and keeps closed sessions terminal", Label("persistence"), func() {
	f := newFixture()
	d := f.open()
	lazy := render.NewLazy(func() (*render.Service, error) { return f.core, nil })
	handler := httpv1.New(query.New(f.store, f.auth, f.store), lazy, nil)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+f.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	base := "/api/v1/versions/" + f.version + "/render-sessions/" + d.ID
	f.lifecycle("trash")
	denied := request("POST", base+"/renew", "{}")
	Expect(denied.Code).To(Equal(409))
	var response struct {
		Error struct {
			Code      string         `json:"code"`
			Retryable bool           `json:"retryable"`
			Details   map[string]any `json:"details"`
		} `json:"error"`
	}
	Expect(json.Unmarshal(denied.Body.Bytes(), &response)).To(Succeed())
	Expect(response.Error.Code).To(Equal("INVALID_TRANSITION"))
	Expect(response.Error.Retryable).To(BeFalse())
	Expect(response.Error.Details["reason"]).To(Equal(render.ReasonDeniedDelivery))
	Expect(request("GET", "/api/v1/capabilities", "").Code).To(Equal(200))
	f.lifecycle("active")
	closed := request("POST", base+"/renew", "")
	Expect(closed.Code).To(Equal(409))
	Expect(json.Unmarshal(closed.Body.Bytes(), &response)).To(Succeed())
	Expect(response.Error.Details["reason"]).To(Equal(render.ReasonClosedSession))
	Expect(request("POST", base+"/close", "{}").Code).To(Equal(204))
	Expect(request("POST", base+"/close", "").Code).To(Equal(204))
})

var _ = It("keeps known owned session close usable from remote ingress while denying renewal there", Label("persistence"), func() {
	f := newFixture()
	d := f.open()
	lazy := render.NewLazy(func() (*render.Service, error) { return f.core, nil })
	handler := httpv1.New(query.New(f.store, f.auth, f.store), lazy, func() (string, string) {
		return "http://127.0.0.1:9270", "https://haeryong.private:9443"
	})
	request := func(method, path, body, host string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
		r.Host = host
		r.Header.Set("Authorization", "Bearer "+f.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	base := "/api/v1/versions/" + f.version + "/render-sessions/" + d.ID
	// A known owned session can still be cleaned up from remote ingress...
	Expect(request("POST", base+"/close", "{}", "haeryong.private:9443").Code).To(Equal(204))
	// ...and the terminal state is observable from remote ingress too.
	renew := request("POST", base+"/renew", "{}", "haeryong.private:9443")
	Expect(renew.Code).To(Equal(503))
	var response struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
			Details   struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	Expect(json.Unmarshal(renew.Body.Bytes(), &response)).To(Succeed())
	Expect(response.Error.Code).To(Equal("FEATURE_NOT_READY"))
	Expect(response.Error.Retryable).To(BeFalse())
	Expect(response.Error.Details.Reason).To(Equal(render.ReasonRemoteIngress))
})

var _ = It("validates empty render control and operation key before constructing a renderer or creating state", Label("persistence"), func() {
	f := newFixture()
	calls := 0
	lazy := render.NewLazy(func() (*render.Service, error) { calls++; return f.core, nil })
	handler := httpv1.New(query.New(f.store, f.auth, f.store), lazy, nil)
	for _, body := range []string{`null`, `[]`, `{"unknown":true}`, `{} {}`, `{"secret":"ignored"}`} {
		r := httptest.NewRequest("POST", "/api/v1/versions/"+f.version+"/render-sessions", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+f.token)
		r.Header.Set("Idempotency-Key", key())
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		Expect(w.Code).To(Equal(400), body)
	}
	r := httptest.NewRequest("POST", "/api/v1/versions/"+f.version+"/render-sessions", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+f.token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	Expect(w.Code).To(Equal(428))
	Expect(calls).To(BeZero())
})

var _ = It("joins concurrent shutdown callers until the active handler and its pin have actually drained", Label("persistence"), func() {
	f := newFixture()
	Expect(f.core.Shutdown()).To(Succeed())
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	f.bytes = observedBytes{Bytes: f.store, wrap: func(c context.Context, r io.ReadSeeker) io.ReadSeeker {
		return blockedRead{ReadSeeker: r, ctx: c, entered: entered, once: &once, canceled: canceled, release: release}
	}}
	f.start()
	DeferCleanup(func() { releaseOnce.Do(func() { close(release) }) })
	d := f.open()
	status, _ := f.handoff(d)
	Expect(status).To(Equal(200))
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		r, _ := http.NewRequest("GET", d.Origin+"/large.bin", nil)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		response, _ := f.client.Do(r)
		if response != nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
	}()
	Eventually(entered, "2s").Should(BeClosed())
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- f.core.Shutdown() }()
	Eventually(canceled, "2s").Should(BeClosed())
	go func() { second <- f.core.Shutdown() }()
	Consistently(second, "50ms").ShouldNot(Receive())
	releaseOnce.Do(func() { close(release) })
	var err error
	Eventually(first, "2s").Should(Receive(&err))
	Expect(err).NotTo(HaveOccurred())
	Eventually(second, "2s").Should(Receive(&err))
	Expect(err).NotTo(HaveOccurred())
	Eventually(requestDone, "2s").Should(BeClosed())
})

// C2 F4: page-identity headers must percent-encode once so Go's header
// serialization cannot replace raw CR/LF with spaces on the real wire. A real
// server/client round trip (not a ResponseRecorder) proves identity for
// control, Unicode, space, literal plus/percent/ampersand and reserved
// characters, on both HTML headers and the non-HTML resolved header, with
// authored bytes unchanged.
var _ = It("round-trips exact page identity headers over a real socket for CR/LF/Unicode/reserved filenames", Label("persistence"), func() {
	f := newFixture()
	d := f.open()
	f.handoff(d)
	cases := []struct {
		name string
		html bool
		body string
	}{
		{"tricky/dir/pa\tge\rge\nnaïme \"q\"<&>.html", true, "<!doctype html><head></head><body>tricky html</body>"},
		{"tricky/data/pa+th%20name&q=1;z=?.json", false, `{"path":"header identity"}`},
		{"tricky/spa ce/uni ☃ and 한글.bin", false, "plain-bytes"},
	}
	getRaw := func(name string) (*http.Response, []byte) {
		GinkgoHelper()
		target, err := url.Parse(d.Origin)
		Expect(err).NotTo(HaveOccurred())
		target.Path = "/" + name
		request, err := http.NewRequest(http.MethodGet, target.String(), nil)
		Expect(err).NotTo(HaveOccurred())
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		response, err := f.client.Do(request)
		Expect(err).NotTo(HaveOccurred())
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		Expect(err).NotTo(HaveOccurred())
		return response, body
	}
	for _, item := range cases {
		response, body := getRaw(item.name)
		Expect(response.StatusCode).To(Equal(200), func() string { return fmt.Sprintf("%s: HTTP %d", item.name, response.StatusCode) })
		escaped := response.Header.Get("Haeryong-Resolved-Path")
		Expect(escaped).NotTo(Equal(item.name), func() string { return item.name + ": raw path reached the wire unencoded" })
		decoded, err := url.QueryUnescape(escaped)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded).To(Equal(item.name), func() string {
			return fmt.Sprintf("resolved identity changed: %q != %q (wire %q)", decoded, item.name, escaped)
		})
		if item.html {
			pageEscaped := response.Header.Get("Haeryong-Page-Path")
			pageDecoded, err := url.QueryUnescape(pageEscaped)
			Expect(err).NotTo(HaveOccurred())
			Expect(pageDecoded).To(Equal(item.name), func() string {
				return fmt.Sprintf("page identity changed: %q != %q (wire %q)", pageDecoded, item.name, pageEscaped)
			})
			Expect(string(body)).To(ContainSubstring("tricky html"))
		} else {
			Expect(string(body)).To(Equal(item.body), func() string { return item.name + ": authored bytes changed" })
		}
	}
})

var _ = It("keeps authored CSS/classic script encoding rules and original bytes over real HTTP", Label("persistence"), func() {
	f := newFixture()
	d := f.open()
	status, _ := f.handoff(d)
	Expect(status).To(Equal(200))
	originals := map[string]string{"legacy.css": "@charset \"windows-1252\"; body::after { content: 'caf\xe9'; }", "legacy.js": "window.legacy='caf\xe9';"}
	api := httptest.NewServer(httpv1.New(query.New(f.store, f.auth, f.store), nil, nil))
	DeferCleanup(api.Close)
	for name, want := range originals {
		response, body := f.get(d, "/"+name, nil)
		Expect(response.StatusCode).To(Equal(200))
		Expect(body).To(Equal([]byte(want)))
		Expect(response.Header.Get("Content-Type")).NotTo(ContainSubstring("charset"))
		response, body = f.get(d, "/"+name, map[string]string{"Range": "bytes=2-5", "If-Range": response.Header.Get("ETag")})
		Expect(response.StatusCode).To(Equal(206))
		Expect(body).To(Equal([]byte(want)[2:6]))
		request, err := http.NewRequest("GET", api.URL+"/api/v1/versions/"+f.version+"/file?path="+url.QueryEscape(name), nil)
		Expect(err).NotTo(HaveOccurred())
		request.Header.Set("Authorization", "Bearer "+f.token)
		original, err := http.DefaultClient.Do(request)
		Expect(err).NotTo(HaveOccurred())
		data, err := io.ReadAll(original.Body)
		original.Body.Close()
		Expect(err).NotTo(HaveOccurred())
		Expect(original.StatusCode).To(Equal(200))
		Expect(data).To(Equal([]byte(want)))
	}
})

// M2: a proven top-level navigation (Sec-Fetch-Dest=document + navigate) of
// an HTML entry receives the daemon-owned unprivileged redirect document —
// never delivered author bytes — composing the public Reader route from the
// request's own exact page; authored page_query/page_fragment stay
// client-side. Headerless HTML navigation without a cookie redirects too,
// while a headerless authenticated fetch with a delivery cookie and a
// subresource (Sec-Fetch-Dest=script) keep ordinary delivery.
var _ = It("redirects proven top-level navigations to the public Reader route", Label("persistence"), func() {
	f := newFixture()
	d := f.open()
	f.handoff(d)
	top := func(name string, headers map[string]string) *http.Response {
		path, rest := func() (string, string) {
			i := strings.IndexAny(name, "?#")
			if i < 0 {
				return name, ""
			}
			return name[:i], name[i:]
		}()
		query, fragment := func() (string, string) {
			if j := strings.IndexAny(rest, "#"); j >= 0 {
				return strings.TrimPrefix(rest[:j], "?"), rest[j+1:]
			}
			return strings.TrimPrefix(rest, "?"), ""
		}()
		target, err := url.Parse(d.Origin)
		Expect(err).NotTo(HaveOccurred())
		target.Path = "/" + path
		target.RawQuery = query
		target.Fragment = fragment
		request, err := http.NewRequest(http.MethodGet, target.String(), nil)
		Expect(err).NotTo(HaveOccurred())
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response, err := f.client.Do(request)
		Expect(err).NotTo(HaveOccurred())
		defer response.Body.Close()
		_, _ = io.ReadAll(response.Body)
		return response
	}
	response := top("hello.html?q=authored#frag", map[string]string{"Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate"})
	Expect(response.StatusCode).To(Equal(200))
	Expect(response.Header.Get("Content-Type")).To(ContainSubstring("text/html"))
	// No delivery derivative headers on the redirect document.
	Expect(response.Header.Get("Haeryong-Derivative")).To(BeEmpty())
	// Headerless missing-cookie navigation-shaped request also redirects.
	Expect(top("hello.html", map[string]string{"Accept": "text/html,application/xhtml+xml"}).Header.Get("Haeryong-Derivative")).To(BeEmpty())
	// A subresource keeps ordinary delivery through the existing cookie.
	sub, body := f.get(d, "/hello.html", map[string]string{"Sec-Fetch-Dest": "script", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Site": "same-origin"})
	Expect(sub.Header.Get("Haeryong-Derivative")).NotTo(BeEmpty())
	Expect(string(body)).To(ContainSubstring("Reader"))
	// A non-HTML top-level navigation keeps ordinary delivery.
	data, body := f.get(d, "/api/sample.json", map[string]string{"Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "none"})
	Expect(data.Header.Get("Content-Type")).To(ContainSubstring("application/json"))
	Expect(string(body)).To(ContainSubstring("authored"))
})

var _ = It("supports mapped private HTTPS renderer lifecycle, normalized browser origins, receipt preconditions and revocation", Label("persistence"), func() {
	f := newFixture()
	dLocal := f.open()
	// Local handoff accepts CompanionUIOrigin, exact parent, Origin: null, and headerless POST,
	// while rejecting foreign origins and duplicate Origin headers without consuming the secret.
	badReq, err := http.NewRequest(http.MethodPost, dLocal.Handoff.URL, strings.NewReader(url.Values{"secret": {dLocal.Handoff.Secret}}.Encode()))
	Expect(err).NotTo(HaveOccurred())
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badReq.Header.Set("Origin", "http://127.0.0.1:9999")
	badResp, err := f.client.Do(badReq)
	Expect(err).NotTo(HaveOccurred())
	badResp.Body.Close()
	Expect(badResp.StatusCode).To(Equal(http.StatusForbidden))

	dupReq, err := http.NewRequest(http.MethodPost, dLocal.Handoff.URL, strings.NewReader(url.Values{"secret": {dLocal.Handoff.Secret}}.Encode()))
	Expect(err).NotTo(HaveOccurred())
	dupReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	dupReq.Header.Add("Origin", "http://127.0.0.1:9270")
	dupReq.Header.Add("Origin", render.CompanionUIOrigin)
	dupResp, err := f.client.Do(dupReq)
	Expect(err).NotTo(HaveOccurred())
	dupResp.Body.Close()
	Expect(dupResp.StatusCode).To(Equal(http.StatusForbidden))

	companionReq, err := http.NewRequest(http.MethodPost, dLocal.Handoff.URL, strings.NewReader(url.Values{"secret": {dLocal.Handoff.Secret}}.Encode()))
	Expect(err).NotTo(HaveOccurred())
	companionReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	companionReq.Header.Set("Origin", render.CompanionUIOrigin)
	companionResp, err := f.client.Do(companionReq)
	Expect(err).NotTo(HaveOccurred())
	companionResp.Body.Close()
	Expect(companionResp.StatusCode).To(Equal(http.StatusOK))
	_, err = f.core.Close(ctx, f.token, f.version, dLocal.ID)
	Expect(err).NotTo(HaveOccurred())

	// Shift renderer_pool away from the version's sticky local port and map that sticky upstream to a private HTTPS renderer origin.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	shiftedPoolPort := listener.Addr().(*net.TCPAddr).Port
	Expect(listener.Close()).To(Succeed())
	upstreamOrigin := dLocal.Origin
	remoteParent := "https://Node.Tailnet.ts.net:443"
	remoteRenderer := "https://Node.Tailnet.ts.net:09291"

	setRemoteConfig := func(parent string, mappings []any) {
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			config, err := tx.Configuration()
			if err != nil {
				return err
			}
			var value map[string]any
			if err = json.Unmarshal(config.Value, &value); err != nil {
				return err
			}
			network := value["network"].(map[string]any)
			network["renderer_pool"] = map[string]any{"base_port": shiftedPoolPort, "count": 1}
			network["remote_enabled"] = true
			network["external_base_url"] = parent
			network["allowed_origins"] = []any{"http://127.0.0.1:9270", parent}
			network["renderer_mappings"] = mappings
			config.Value, err = json.Marshal(value)
			if err != nil {
				return err
			}
			return tx.SetConfiguration(config, config.Revision)
		})).To(Succeed())
	}
	setRemoteConfig(remoteParent, []any{
		map[string]any{"version_id": f.version, "origin": remoteRenderer, "upstream": upstreamOrigin, "reassignable": false},
	})

	q := query.New(f.store, f.auth, f.store)
	lazy := render.NewLazy(func() (*render.Service, error) { return f.core, nil })
	v1Handler := httpv1.New(q, lazy, func() (string, string) {
		return "http://127.0.0.1:9270", remoteParent
	})
	v1Req := func(method, path, host, origin string, extra map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader("{}"))
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Authorization", "Bearer "+f.token)
		for k, v := range extra {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		v1Handler.ServeHTTP(w, r)
		return w
	}

	capRec := v1Req(http.MethodGet, "/api/v1/capabilities", "node.tailnet.ts.net", "https://node.tailnet.ts.net", nil)
	Expect(capRec.Code).To(Equal(http.StatusOK))
	var capEnv struct {
		Data struct {
			Features                  map[string]bool `json:"features"`
			RendererUnavailableReason any             `json:"renderer_unavailable_reason"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(capRec.Body.Bytes(), &capEnv)).To(Succeed())
	Expect(capEnv.Data.Features["renderer"]).To(BeTrue())
	Expect(capEnv.Data.RendererUnavailableReason).To(BeNil())

	bootRec := v1Req(http.MethodGet, "/api/v1/versions/"+f.version+"/render-bootstrap", "node.tailnet.ts.net", "https://node.tailnet.ts.net", nil)
	Expect(bootRec.Code).To(Equal(http.StatusOK))
	var bootEnv struct {
		Data struct {
			Delivery                  string `json:"delivery"`
			RendererAvailable         bool   `json:"renderer_available"`
			RenderSessionsURL         string `json:"render_sessions_url"`
			RendererUnavailableReason any    `json:"renderer_unavailable_reason"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(bootRec.Body.Bytes(), &bootEnv)).To(Succeed())
	Expect(bootEnv.Data.Delivery).To(Equal("private_renderer_origin"))
	Expect(bootEnv.Data.RendererAvailable).To(BeTrue())
	Expect(bootEnv.Data.RenderSessionsURL).To(Equal("/api/v1/versions/" + f.version + "/render-sessions"))
	Expect(bootEnv.Data.RendererUnavailableReason).To(BeNil())

	localKey := key()
	_, err = f.core.Open(ctx, f.token, localKey, f.version)
	Expect(err).NotTo(HaveOccurred())
	_, err = f.core.OpenWithIngress(ctx, f.token, localKey, f.version, render.Ingress{Remote: true, ParentOrigin: remoteParent})
	Expect(errors.Is(err, domain.ErrConflict)).To(BeTrue())

	opKey := key()
	remoteIngress := render.Ingress{Remote: true, ParentOrigin: remoteParent}
	openResp, err := f.core.OpenWithIngress(ctx, f.token, opKey, f.version, remoteIngress)
	Expect(err).NotTo(HaveOccurred())
	Expect(openResp.Status).To(Equal(http.StatusCreated))
	replayResp, err := f.core.OpenWithIngress(ctx, f.token, opKey, f.version, remoteIngress)
	Expect(err).NotTo(HaveOccurred())
	Expect(replayResp.Body).To(Equal(openResp.Body))

	// Replaying the same Idempotency-Key across local ingress or a changed remote parent conflicts.
	_, err = f.core.Open(ctx, f.token, opKey, f.version)
	Expect(errors.Is(err, domain.ErrConflict)).To(BeTrue())
	setRemoteConfig("https://Node.Tailnet.ts.net:9443", []any{
		map[string]any{"version_id": f.version, "origin": remoteRenderer, "upstream": upstreamOrigin, "reassignable": false},
	})
	_, err = f.core.OpenWithIngress(ctx, f.token, opKey, f.version, render.Ingress{Remote: true, ParentOrigin: "https://Node.Tailnet.ts.net:9443"})
	Expect(errors.Is(err, domain.ErrConflict)).To(BeTrue())
	setRemoteConfig(remoteParent, []any{
		map[string]any{"version_id": f.version, "origin": remoteRenderer, "upstream": upstreamOrigin, "reassignable": false},
	})

	// An unchanged parent cannot replay the old key into another delivery origin.
	setRemoteConfig(remoteParent, []any{
		map[string]any{"version_id": f.version, "origin": "https://node.tailnet.ts.net:9492", "upstream": upstreamOrigin, "reassignable": false},
	})
	_, err = f.core.OpenWithIngress(ctx, f.token, opKey, f.version, remoteIngress)
	Expect(errors.Is(err, domain.ErrConflict)).To(BeTrue())
	setRemoteConfig(remoteParent, []any{
		map[string]any{"version_id": f.version, "origin": remoteRenderer, "upstream": upstreamOrigin, "reassignable": false},
	})

	var dRemote delivery
	Expect(json.Unmarshal(openResp.Body, &dRemote)).To(Succeed())
	Expect(dRemote.Origin).To(Equal("https://node.tailnet.ts.net:9291"))
	Expect(dRemote.Handoff.URL).To(Equal("https://node.tailnet.ts.net:9291/__haeryong/handoff"))

	// Remote handoff rejects headerless POST, CompanionUIOrigin, and local parent without consuming the secret.
	postRemote := func(hostHeader string, originHeaders []string, querySuffix string) (*http.Response, []byte) {
		target := upstreamOrigin + "/__haeryong/handoff" + querySuffix
		r, err := http.NewRequest(http.MethodPost, target, strings.NewReader(url.Values{"secret": {dRemote.Handoff.Secret}}.Encode()))
		Expect(err).NotTo(HaveOccurred())
		r.Host = hostHeader
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, o := range originHeaders {
			r.Header.Add("Origin", o)
		}
		resp, err := http.DefaultClient.Do(r)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		return resp, b
	}
	resp, _ := postRemote("node.tailnet.ts.net:9291", nil, "")
	Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
	resp, _ = postRemote("node.tailnet.ts.net:9291", []string{render.CompanionUIOrigin}, "")
	Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
	resp, _ = postRemote("node.tailnet.ts.net:9291", []string{"http://127.0.0.1:9270"}, "")
	Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
	resp, _ = postRemote("node.tailnet.ts.net:9291", []string{"null", "https://node.tailnet.ts.net"}, "")
	Expect(resp.StatusCode).To(Equal(http.StatusForbidden))

	// Browser-normalized Host and Origin: null succeed and issue a Secure, HttpOnly, SameSite=Strict cookie.
	resp, landing := postRemote("node.tailnet.ts.net:9291", []string{"null"}, "?page=/hello.html")
	Expect(resp.StatusCode).To(Equal(http.StatusOK))
	Expect(string(landing)).To(ContainSubstring("hello.html"))
	cookies := resp.Cookies()
	Expect(cookies).To(HaveLen(1))
	Expect(cookies[0].Name).To(HavePrefix("hyr_view_" + f.version + "_"))
	Expect(cookies[0].Secure).To(BeTrue())
	Expect(cookies[0].HttpOnly).To(BeTrue())
	Expect(cookies[0].SameSite).To(Equal(http.SameSiteStrictMode))
	Expect(cookies[0].Path).To(Equal("/"))

	// Deliver HTML and loader over normalized remote Host and verify canonical parentOrigin injection.
	getRemote := func(path string, headers map[string]string) (*http.Response, []byte) {
		r, err := http.NewRequest(http.MethodGet, upstreamOrigin+path, nil)
		Expect(err).NotTo(HaveOccurred())
		r.Host = "node.tailnet.ts.net:9291"
		r.AddCookie(cookies[0])
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(r)
		Expect(err).NotTo(HaveOccurred())
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		Expect(err).NotTo(HaveOccurred())
		return res, b
	}
	pageResp, pageBody := getRemote("/hello.html", nil)
	Expect(pageResp.StatusCode).To(Equal(http.StatusOK))
	Expect(string(pageBody)).To(ContainSubstring("window.useful = true"))
	loaderResp, loaderBody := getRemote("/__haeryong/loader.js?"+render.LoaderQuery, nil)
	Expect(loaderResp.StatusCode).To(Equal(http.StatusOK))
	Expect(string(loaderBody)).To(ContainSubstring("context.transfer.begin"))

	// Top-level navigation redirects to the canonical remote parentOrigin.
	navResp, navBody := getRemote("/hello.html", map[string]string{"Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate"})
	Expect(navResp.StatusCode).To(Equal(http.StatusOK))
	Expect(navResp.Header.Get("Haeryong-Derivative")).To(BeEmpty())
	Expect(string(navBody)).To(ContainSubstring("https://node.tailnet.ts.net/documents/" + f.document + "/versions/" + f.version))

	// Cross-endpoint renewal is denied without closing the session; RenewWithIngress succeeds.
	_, err = f.core.Renew(ctx, f.token, f.version, dRemote.ID)
	Expect(err).To(HaveOccurred())
	_, err = f.core.RenewWithIngress(ctx, f.token, f.version, dRemote.ID, remoteIngress)
	Expect(err).NotTo(HaveOccurred())

	// Removing the renderer mapping revokes active remote delivery and renewal.
	setRemoteConfig(remoteParent, []any{})
	revokedResp, _ := getRemote("/hello.html", nil)
	Expect(revokedResp.StatusCode).To(Equal(http.StatusUnauthorized))
	_, err = f.core.RenewWithIngress(ctx, f.token, f.version, dRemote.ID, remoteIngress)
	Expect(err).To(HaveOccurred())

	// Revoking the owner bearer token rejects replay of the completed OpenWithIngress receipt with 401.
	sess, err := f.auth.Authenticate(ctx, f.token)
	Expect(err).NotTo(HaveOccurred())
	_, err = f.auth.Revoke(ctx, f.token, sess.ID)
	Expect(err).NotTo(HaveOccurred())
	_, err = f.core.OpenWithIngress(ctx, f.token, opKey, f.version, remoteIngress)
	Expect(err).To(HaveOccurred())
})
