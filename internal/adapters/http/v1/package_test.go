package v1

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	httpimports "haeryong.local/haeryong/internal/adapters/http/imports"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/domain"
)

// packageTree is one complete original directory with every representation
// rule the archive must preserve: nested and empty directories, case-distinct
// names, Unicode and hidden entries, a zero-byte file and a symlink.
type packageTree struct {
	kinds map[string]string
	files map[string][]byte
	links map[string]string
	order []string
}

func newPackageTree() *packageTree {
	t := &packageTree{kinds: map[string]string{}, files: map[string][]byte{}, links: map[string]string{}}
	add := func(path, kind string) {
		t.kinds[path] = kind
		t.order = append(t.order, path)
	}
	add("docs", "directory")
	add("docs/empty-nested", "directory")
	add("standalone-empty", "directory")
	add("docs/Readme.md", "file")
	t.files["docs/Readme.md"] = []byte("case-distinct readme\n")
	add("docs/README.MD", "file")
	t.files["docs/README.MD"] = []byte("different bytes under a case-folded sibling\n")
	add("docs/페이지.html", "file")
	t.files["docs/페이지.html"] = []byte("<html><body>유니코드 경로 페이지</body></html>")
	add(".hidden", "file")
	t.files[".hidden"] = []byte("hidden dotfile\n")
	add("binary.bin", "file")
	t.files["binary.bin"] = []byte{0, 1, 2, 0, 255, 254, 0, 0, 7}
	add("empty.bin", "file")
	t.files["empty.bin"] = []byte{}
	add("docs/unicode-link", "symlink")
	t.links["docs/unicode-link"] = "페이지.html"
	return t
}

func (t *packageTree) upload(f *fixture, upload string) {
	for _, path := range t.order {
		switch t.kinds[path] {
		case "directory":
			f.put(upload, path, "directory", []byte(`{}`))
		case "symlink":
			body, err := json.Marshal(map[string]string{"link_target": t.links[path]})
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
			f.put(upload, path, "symlink", body)
		case "file":
			f.put(upload, path, "file", t.files[path])
		}
	}
}

func (t *packageTree) expectedOrder() []string {
	paths := append([]string(nil), t.order...)
	sort.Slice(paths, func(i, j int) bool { return paths[i] < paths[j] })
	return paths
}

var _ = It("TestActualPackageArchivePreservesExactTreeBytesAndOrder", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	tree := newPackageTree()
	upload := f.create()
	tree.upload(f, upload)
	v := f.publish(upload, nil)

	base := "/api/v1/versions/" + v.ID + "/package"
	status, headers, data := f.request("GET", base, nil, nil)
	want(t, status, 200, data)
	Expect(headers.Get("Content-Type") != "application/zip").To(BeFalse(), func() string { return "archive is not application/zip" })
	Expect(!strings.HasPrefix(headers.Get("Content-Disposition"), "attachment;")).To(BeFalse(), func() string { return "archive is not an attachment" })
	Expect(!strings.Contains(headers.Get("Content-Disposition"), "haeryong-"+v.ID+".zip")).To(BeFalse(), func() string { return "attachment filename is not the stable version id name" })
	Expect(headers.Get("Content-Length") != "" || headers.Get("Content-Digest") != "" || headers.Get("Repr-Digest") != "").To(BeFalse(), func() string {
		return "generated archive must not claim a wire length or wire digest"
	})

	_, _, again := f.request("GET", base, nil, nil)
	Expect(bytes.Equal(data, again)).To(BeTrue(), func() string { return "archive bytes must be deterministic" })

	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	Expect(len(reader.File) != len(tree.kinds)).To(BeFalse(), func() string { return fmt.Sprintf("archive lost entries: %d/%d", len(reader.File), len(tree.kinds)) })
	expected := tree.expectedOrder()
	seen := map[string]bool{}
	for i, entry := range reader.File {
		name := strings.TrimSuffix(entry.Name, "/")
		Expect(seen[name] || tree.kinds[name] == "").To(BeFalse(), func() string { return fmt.Sprintf("unexpected archive entry %q", name) })
		seen[name] = true
		Expect(name != expected[i]).To(BeFalse(), func() string {
			return fmt.Sprintf("archive order is not stored-path byte order: %q != %q", name, expected[i])
		})
		switch tree.kinds[name] {
		case "directory":
			Expect(!strings.HasSuffix(entry.Name, "/") || !entry.Mode().IsDir()).To(BeFalse(), func() string { return fmt.Sprintf("directory entry not preserved: %q", name) })
		case "symlink":
			Expect(entry.Mode()&os.ModeSymlink == 0).To(BeFalse(), func() string { return fmt.Sprintf("symlink mode not preserved: %q", name) })
			body, err := io.ReadAll(mustOpen(entry))
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
			Expect(string(body) != tree.links[name]).To(BeFalse(), func() string { return fmt.Sprintf("symlink link text changed: %q", name) })
		case "file":
			body, err := io.ReadAll(mustOpen(entry))
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
			Expect(!bytes.Equal(body, tree.files[name])).To(BeFalse(), func() string { return fmt.Sprintf("exact file bytes changed in archive: %q", name) })
		}
	}
	// Empty directories must be actual archive records, not implied parents.
	for _, empty := range []string{"docs/empty-nested", "standalone-empty"} {
		Expect(seen[empty]).To(BeTrue(), func() string { return fmt.Sprintf("empty directory %q missing from archive", empty) })
	}
})

func mustOpen(file *zip.File) io.ReadCloser {
	body, err := file.Open()
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	return body
}

var _ = It("TestActualPackageExportScopeAllowsExportOnlyAndDeniesReadonly", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	tree := newPackageTree()
	upload := f.create()
	tree.upload(f, upload)
	v := f.publish(upload, nil)
	owner := f.token

	exporter, _ := f.workflow(domain.CredentialScope{Kind: "workflow", Actions: []string{"package.export"}, ProjectIDs: []string{f.project}, VersionIDs: []string{v.ID}})
	f.token = exporter
	status, _, data := f.request("GET", "/api/v1/versions/"+v.ID+"/package", nil, nil)
	want(t, status, 200, data)

	// The export-only credential gains no ordinary version reads.
	status, _, data = f.request("GET", "/api/v1/versions/"+v.ID+"/file?path=binary.bin", nil, nil)
	want(t, status, 403, data)

	readerOnly, _ := f.workflow(domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{f.project}, VersionIDs: []string{v.ID}})
	f.token = readerOnly
	status, _, data = f.request("GET", "/api/v1/versions/"+v.ID+"/package", nil, nil)
	want(t, status, 403, data)

	wrongAnchor, _ := f.workflow(domain.CredentialScope{Kind: "workflow", Actions: []string{"package.export"}, ProjectIDs: []string{f.project}})
	f.token = wrongAnchor
	status, _, data = f.request("GET", "/api/v1/versions/"+v.ID+"/package", nil, nil)
	want(t, status, 404, data)

	f.token = owner
})

// failingBytes streams a fixed number of object reads straight through and
// fails the next one, modelling a source-side failure that lands only after
// real archive bytes have already streamed.
type failingBytes struct {
	inner       query.Bytes
	passObjects int
	seen        int
}

func (b *failingBytes) Read(ctx context.Context, object domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) error {
	b.seen++
	if b.seen > b.passObjects {
		return errors.New("injected source failure")
	}
	return b.inner.Read(ctx, object, pin, consume)
}

var _ = It("TestActualPackageMidStreamFailureAbortsConnectionNeverCleanEOF", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	// The first entry is large and incompressible so its compressed bytes
	// reach the response and the started body is observable on the wire; the
	// injected failure then hits a later entry mid-archive.
	upload := f.create()
	random := rand.New(rand.NewSource(42))
	large := make([]byte, 262144)
	for i := range large {
		large[i] = byte(random.Intn(256))
	}
	f.put(upload, "a.bin", "file", large)
	f.put(upload, "b.bin", "file", []byte("second entry"))
	v := f.publish(upload, nil)

	broken := &query.Service{Store: f.store, Auth: f.auth, Bytes: &failingBytes{inner: f.store, passObjects: 1}}
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	handler, err := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{server.Listener.Addr().String()}, Origins: []string{origin}}, routes{New(broken), httpimports.New(f.imports)})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	server.Config.Handler = handler
	server.Start()
	DeferCleanup(server.Close)

	request, err := http.NewRequest("GET", server.URL+"/api/v1/versions/"+v.ID+"/package", nil)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	request.Header.Set("Authorization", "Bearer "+f.token)
	request.Header.Set("Origin", server.URL)
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	Expect(err != nil).To(BeFalse(), func() string {
		return "mid-stream failure must abort an already started response, not the headers phase: " + fmt.Sprint(err)
	})
	Expect(response.StatusCode != 200).To(BeFalse(), func() string { return "mid-stream failure test needs a started body" })
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	Expect(readErr == nil).To(BeFalse(), func() string {
		return "a mid-archive source failure ended in a clean body a client could mistake for completion"
	})
	Expect(len(body) == 0).To(BeFalse(), func() string { return "mid-stream abort produced no started archive bytes" })
})

var _ = It("TestActualZeroByteEOFFinalAfterRetainedRangesPreservesOriginalBytes", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	upload := f.create()
	put := func(offset int, data []byte, final bool) []byte {
		digest := sha256.Sum256(data)
		headers := map[string]string{"Content-Type": "application/octet-stream", "Haeryong-Upload-Offset": strconv.Itoa(offset), "Haeryong-Upload-Chunk-SHA256": hex.EncodeToString(digest[:]), "Haeryong-Upload-Length": "6"}
		if final {
			headers["Haeryong-Upload-Final"] = "true"
		}
		status, _, body := f.request("PUT", "/api/v1/uploads/"+upload+"/entries?path=original.bin&kind=file", bytes.NewReader(data), headers)
		want(f.t, status, 200, body)
		return body
	}
	put(0, []byte("abc"), false)
	put(3, []byte("def"), false)
	first := put(6, nil, true)
	replay := put(6, nil, true)
	Expect(decoded[map[string]any](f.t, replay)).To(Equal(decoded[map[string]any](f.t, first)))
	v := f.publish(upload, nil)
	status, _, data := f.request("GET", "/api/v1/versions/"+v.ID+"/file?path=original.bin", nil, nil)
	want(f.t, status, 200, data)
	Expect(string(data)).To(Equal("abcdef"))
})

type packageFaultWriter struct {
	header                 http.Header
	body                   bytes.Buffer
	writeError, flushError error
}

func (w *packageFaultWriter) Header() http.Header              { return w.header }
func (w *packageFaultWriter) WriteHeader(int)                  {}
func (w *packageFaultWriter) SetWriteDeadline(time.Time) error { return nil }
func (w *packageFaultWriter) FlushError() error                { return w.flushError }
func (w *packageFaultWriter) Write(p []byte) (int, error) {
	if w.writeError != nil {
		n, _ := w.body.Write(p[:len(p)/2])
		return n, w.writeError
	}
	return w.body.Write(p)
}

var _ = It("TestPackageFirstWriteAndFlushErrorsAbortInsteadOfAppendingJSON", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	upload := f.create()
	f.put(upload, "original.bin", "file", []byte("source bytes"))
	v := f.publish(upload, nil)
	for _, kind := range []string{"write", "flush"} {
		writer := &packageFaultWriter{header: http.Header{}}
		if kind == "write" {
			writer.writeError = errors.New("injected partial write")
		} else {
			writer.flushError = errors.New("injected flush failure")
		}
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			New(f.query).deliverPackage(writer, httptest.NewRequest("GET", "/api/v1/versions/"+v.ID+"/package", nil), f.token, v.ID)
		}()
		Expect(recovered).To(Equal(http.ErrAbortHandler), kind)
		Expect(writer.body.Len()).To(BeNumerically(">", 0), kind)
		Expect(writer.Header().Get("Content-Type")).To(Equal("application/zip"), kind)
	}
})

type packageRevokingWriter struct {
	http.ResponseWriter
	afterFirstWrite func()
}

func (w *packageRevokingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *packageRevokingWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if n > 0 && w.afterFirstWrite != nil {
		callback := w.afterFirstWrite
		w.afterFirstWrite = nil
		callback()
	}
	return n, err
}

var _ = It("TestActualPackageStreamStopsWhenItsOwnerSessionIsRevoked", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	upload := f.create()
	large := make([]byte, 256*1024)
	_, err := rand.New(rand.NewSource(57)).Read(large)
	Expect(err).NotTo(HaveOccurred())
	f.put(upload, "original.bin", "file", large)
	v := f.publish(upload, nil)
	session, err := f.auth.Authenticate(context.Background(), f.token)
	Expect(err).NotTo(HaveOccurred())
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	handler, err := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{server.Listener.Addr().String()}, Origins: []string{origin}}, routes{New(f.query), httpimports.New(f.imports)})
	Expect(err).NotTo(HaveOccurred())
	revoked := make(chan error, 1)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(&packageRevokingWriter{ResponseWriter: w, afterFirstWrite: func() {
			_, err := f.auth.Revoke(context.Background(), f.token, session.ID)
			revoked <- err
		}}, r)
	})
	server.Start()
	DeferCleanup(server.Close)
	request, err := http.NewRequest("GET", origin+"/api/v1/versions/"+v.ID+"/package", nil)
	Expect(err).NotTo(HaveOccurred())
	request.Header.Set("Authorization", "Bearer "+f.token)
	request.Header.Set("Origin", origin)
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	Expect(err).NotTo(HaveOccurred())
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	Expect(<-revoked).To(Succeed())
	Expect(response.StatusCode).To(Equal(200))
	Expect(len(body)).To(BeNumerically(">", 0))
	Expect(readErr).To(HaveOccurred())
})
