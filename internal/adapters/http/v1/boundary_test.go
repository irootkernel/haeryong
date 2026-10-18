package v1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/render"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"time"
)

var _ = It("TestActualHTTPCapabilitiesDistinguishTransfersFromUnimplementedRenderer", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	status, _, data := f.request("GET", "/api/v1/capabilities", nil, nil)
	want(t, status, 200, data)
	result := decoded[struct {
		Features map[string]bool `json:"features"`
	}](t, data)
	for _, name := range []string{"resumable_uploads", "atomic_import", "cursor_files", "streamed_files", "byte_ranges"} {
		{
			enabled, present := result.Features[name]
			Expect(!present || !enabled).To(BeFalse(), func() string { return fmt.Sprintf("implemented transfer/query capability %s is not advertised", name) })
		}

	}
	for _, name := range []string{"html_js_v1", "multi_page_loader", "renderer", "preview", "provider", "scheduler"} {
		{
			enabled, present := result.Features[name]
			Expect(!present || enabled).To(BeFalse(), func() string { return fmt.Sprintf("unimplemented capability %s is advertised", name) })
		}

	}
})

var _ = It("reports only the wired renderer without initializing it and retains attachment fallback after a construction failure", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	u := f.create()
	f.put(u, "page.html", "file", []byte("<html>renderer availability</html>"))
	version := f.publish(u, nil)
	calls := 0
	constructionError := errors.New("account metadata lookup unavailable")
	lazy := render.NewLazy(func() (*render.Service, error) {
		calls++
		return nil, constructionError
	})
	defer lazy.Shutdown()
	handler := New(f.query, lazy, nil)
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+f.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		Expect(w.Code).To(Equal(200))
		return w
	}
	for _, available := range []bool{true, false} {
		capabilities := decoded[struct {
			Features map[string]bool `json:"features"`
		}](t, request("/api/v1/capabilities").Body.Bytes())
		Expect(capabilities.Features["renderer"]).To(Equal(available))
		Expect(capabilities.Features["html_js_v1"]).To(Equal(available))
		bootstrap := decoded[query.Bootstrap](t, request("/api/v1/versions/"+version.ID+"/render-bootstrap").Body.Bytes())
		Expect(bootstrap.RendererAvailable).To(Equal(available))
		if available {
			Expect(bootstrap.RenderSessionsURL).NotTo(BeEmpty())
			Expect(calls).To(BeZero())
			_, err := lazy.Get()
			Expect(err).To(MatchError(constructionError))
		} else {
			Expect(bootstrap.Delivery).To(Equal("authenticated_attachment"))
			Expect(bootstrap.RenderSessionsURL).To(BeEmpty())
			Expect(calls).To(Equal(1))
		}
	}
})
var _ = It("TestRendererIngressClassificationHonestAvailabilityAndDenial", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	u := f.create()
	f.put(u, "page.html", "file", []byte("<html>ingress classification</html>"))
	version := f.publish(u, nil)
	calls := 0
	lazy := render.NewLazy(func() (*render.Service, error) {
		calls++
		return nil, errors.New("construction must not happen from classification")
	})
	defer lazy.Shutdown()
	const local = "http://127.0.0.1:9270"
	const external = "https://haeryong.private:9443"
	handler := New(f.query, lazy, func() (string, string) { return local, external })
	type capsShape struct {
		Features map[string]bool `json:"features"`
		Reason   *string         `json:"renderer_unavailable_reason"`
	}
	type failureShape struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
			Details   struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	request := func(handler http.Handler, method, path, host, origin string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+host+path, nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Authorization", "Bearer "+f.token)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	failureOf := func(w *httptest.ResponseRecorder) failureShape {
		var failure failureShape
		Expect(json.Unmarshal(w.Body.Bytes(), &failure)).To(Succeed(), func() string { return w.Body.String() })
		return failure
	}

	// Local ingress keeps the wired renderer advertisement (availability only,
	// still no construction) with no unavailable reason.
	w := request(handler, "GET", "/api/v1/capabilities", "127.0.0.1:9270", "", nil)
	Expect(w.Code).To(Equal(200))
	localCaps := decoded[capsShape](t, w.Body.Bytes())
	Expect(localCaps.Features["renderer"]).To(BeTrue(), "local ingress lost the renderer advertisement")
	Expect(localCaps.Reason).To(BeNil(), "local ingress carried an unavailable reason")
	bootstrap := decoded[query.Bootstrap](t, request(handler, "GET", "/api/v1/versions/"+version.ID+"/render-bootstrap", "127.0.0.1:9270", "", nil).Body.Bytes())
	Expect(bootstrap.RendererAvailable).To(BeTrue())
	Expect(bootstrap.RendererUnavailableReason).To(BeEmpty())

	// Remote Host without Origin and local Host carrying the external Origin
	// are both remote ingress: flags stay off with the honest reason,
	// bootstrap keeps the attachment fallback and no session URL, and
	// open/renew deny 503 FEATURE_NOT_READY retryable false with the exact
	// reason BEFORE any lazy construction, operation reservation or listener.
	for name, ingress := range map[string][2]string{
		"remote-host-no-origin":      {"haeryong.private:9443", ""},
		"local-host-external-origin": {"127.0.0.1:9270", external},
	} {
		w := request(handler, "GET", "/api/v1/capabilities", ingress[0], ingress[1], nil)
		Expect(w.Code).To(Equal(200))
		remoteCaps := decoded[capsShape](t, w.Body.Bytes())
		Expect(remoteCaps.Features["renderer"]).To(BeFalse(), name+": remote ingress advertised the renderer")
		Expect(remoteCaps.Reason).NotTo(BeNil(), name+": reason missing")
		Expect(*remoteCaps.Reason).To(Equal(render.ReasonRemoteIngress))
		remoteBootstrap := decoded[query.Bootstrap](t, request(handler, "GET", "/api/v1/versions/"+version.ID+"/render-bootstrap", ingress[0], ingress[1], nil).Body.Bytes())
		Expect(remoteBootstrap.RendererAvailable).To(BeFalse(), name+": remote bootstrap available")
		Expect(remoteBootstrap.Delivery).To(Equal("authenticated_attachment"))
		Expect(remoteBootstrap.RenderSessionsURL).To(BeEmpty())
		Expect(remoteBootstrap.RendererUnavailableReason).To(Equal(render.ReasonRemoteIngress))
		open := request(handler, "POST", "/api/v1/versions/"+version.ID+"/render-sessions", ingress[0], ingress[1], map[string]string{"Idempotency-Key": id(t)})
		Expect(open.Code).To(Equal(503), name+": remote open status")
		openFailure := failureOf(open)
		Expect(openFailure.Error.Code).To(Equal("FEATURE_NOT_READY"), name+": remote open code")
		Expect(openFailure.Error.Retryable).To(BeFalse(), name+": remote open retryable")
		Expect(openFailure.Error.Details.Reason).To(Equal(render.ReasonRemoteIngress), name+": remote open reason")
		Expect(open.Header().Get("Retry-After")).To(BeEmpty(), name+": remote open suggested retry")
		renew := request(handler, "POST", "/api/v1/versions/"+version.ID+"/render-sessions/"+id(t)+"/renew", ingress[0], ingress[1], nil)
		Expect(renew.Code).To(Equal(503), name+": remote renew status")
		renewFailure := failureOf(renew)
		Expect(renewFailure.Error.Code).To(Equal("FEATURE_NOT_READY"), name+": remote renew code")
		Expect(renewFailure.Error.Retryable).To(BeFalse(), name+": remote renew retryable")
		// No remote surface in this matrix constructs the renderer. Close is
		// exercised separately against a real service in the renderer
		// persistence specs, including the known-owned remote close.
	}
	Expect(calls).To(BeZero(), "classification or a remote open/renew denial constructed the renderer")

	// Defense-in-depth tie rule: when wiring presents equal local/external
	// hosts (refused by production construction), the local endpoint wins.
	tie := New(f.query, lazy, func() (string, string) { return "http://127.0.0.1:9443", "https://127.0.0.1:9443" })
	w = request(tie, "GET", "/api/v1/capabilities", "127.0.0.1:9443", "", nil)
	Expect(w.Code).To(Equal(200))
	tieCaps := decoded[capsShape](t, w.Body.Bytes())
	Expect(tieCaps.Features["renderer"]).To(BeTrue(), "ambiguous tie classified local callers as remote")
	Expect(tieCaps.Reason).To(BeNil())

	// Nil endpoint wiring keeps the historical all-local default.
	plain := New(f.query, lazy, nil)
	w = request(plain, "GET", "/api/v1/capabilities", "127.0.0.1:9270", "", nil)
	Expect(w.Code).To(Equal(200))
	plainCaps := decoded[capsShape](t, w.Body.Bytes())
	Expect(plainCaps.Features["renderer"]).To(BeTrue(), "nil wiring lost the renderer advertisement")
	Expect(plainCaps.Reason).To(BeNil())
})

var _ = It("TestActualHTTPExactUnicodeLinksAndRangeValidators", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	u := f.create()
	files := map[string]string{"café.html": "NFC exact", "cafe\u0301.html": "NFD exact", "unique-cafe\u0301.html": "unique canonical fallback", "amb-ḋ\u0323.html": "first equivalent", "amb-ḍ\u0307.html": "second equivalent", "Case.html": "case-sensitive", "literal\\name.html": "literal backslash", "range.bin": "0123456789abcdefghij", "zero.bin": "", "folder-cafe\u0301/nested.txt": "nested exact bytes"}
	for p, content := range files {
		f.put(u, p, "file", []byte(content))
	}
	f.put(u, "empty", "directory", []byte(`{}`))
	links := map[string]string{"links/inside": "../café.html", "links/outside": "../../private", "absolute": "/etc/passwd", "missing": "absent", "cycle-a": "cycle-b", "cycle-b": "cycle-a", "alias-dir": "empty"}
	for p, target := range links {
		body, _ := json.Marshal(map[string]string{"link_target": target})
		f.put(u, p, "symlink", body)
	}
	v := f.publish(u, nil)
	base := "/api/v1/versions/" + v.ID
	get := func(p string, resolve bool, headers map[string]string) (int, http.Header, []byte) {
		suffix := ""
		if resolve {
			suffix = "&resolve_link=true"
		}
		return f.request("GET", base+"/file?path="+url.QueryEscape(p)+suffix, nil, headers)
	}
	for p, content := range files {
		status, _, data := get(p, false, nil)
		want(t, status, 200, data)
		Expect(string(data) != content).To(BeFalse(), func() string { return fmt.Sprint("exact path lost precedence", p) })

	}
	status, headers, data := get("unique-café.html", false, nil)
	want(t, status, 200, data)
	Expect(string(data) != files["unique-cafe\u0301.html"] || headers.Get("Haeryong-Requested-Path") != url.QueryEscape("unique-café.html") || headers.Get("Haeryong-Resolved-Path") != url.QueryEscape("unique-cafe\u0301.html")).To(BeFalse(), func() string { return fmt.Sprint("canonical fallback changed identity/spelling") })

	status, _, data = get("amb-d\u0323\u0307.html", false, nil)
	want(t, status, 400, data)
	var failure struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	Expect(json.Unmarshal(data, &failure) != nil || failure.Error.Details["reason"] != "ambiguous_path").To(BeFalse(), func() string { return fmt.Sprint("ambiguous canonical fallback did not remain explicit") })

	status, _, data = get("case.html", false, nil)
	want(t, status, 404, data)
	status, _, data = f.request("GET", base+"/files?directory="+url.QueryEscape("folder-café"), nil, nil)
	want(t, status, 200, data)
	filtered := decoded[query.List[query.File]](t, data)
	Expect(len(filtered.Items) != 1 || filtered.Items[0].Path != "folder-cafe\u0301/nested.txt").To(BeFalse(), func() string { return fmt.Sprint("directory filter did not use unique exact path lookup") })

	for p, target := range links {
		status, _, data = get(p, false, nil)
		want(t, status, 200, data)
		item := decoded[query.ResolvedFile](t, data)
		Expect(item.Kind != "symlink" || item.LinkTarget == nil || *item.LinkTarget != target).To(BeFalse(), func() string { return fmt.Sprint("link text was followed/changed by default", p) })

	}
	status, headers, data = get("links/inside", true, nil)
	want(t, status, 200, data)
	Expect(string(data) != files["café.html"] || headers.Get("Haeryong-Resolved-Path") != url.QueryEscape("café.html")).To(BeFalse(), func() string { return fmt.Sprint("in-version alias did not bind exact target") })

	status, _, data = f.request("GET", base+"/render-bootstrap?page_path="+url.QueryEscape("links/inside"), nil, nil)
	want(t, status, 200, data)
	aliasBootstrap := decoded[query.Bootstrap](t, data)
	Expect(aliasBootstrap.SelectedPage == nil || *aliasBootstrap.SelectedPage != "café.html" || aliasBootstrap.NavigationPath == nil || *aliasBootstrap.NavigationPath != "links/inside").To(BeFalse(), func() string { return fmt.Sprint("bootstrap lost alias navigation versus canonical page") })

	status, _, data = get("alias-dir", true, nil)
	want(t, status, 200, data)
	Expect(decoded[query.ResolvedFile](t, data).Kind != "directory").To(BeFalse(), func() string { return fmt.Sprint("directory link fabricated file bytes") })

	for _, p := range []string{"links/outside", "absolute", "missing", "cycle-a"} {
		status, _, data = get(p, true, nil)
		want(t, status, 404, data)
		Expect(json.Unmarshal(data, &failure) != nil || failure.Error.Details["reason"] != "unresolved_link").To(BeFalse(), func() string { return fmt.Sprint("link resolution lost unavailable reason", p) })

	}
	status, _, data = get("empty", false, map[string]string{"Range": "bytes=0-1"})
	want(t, status, 416, data)
	status, headers, data = get("range.bin", false, nil)
	want(t, status, 200, data)
	etag := headers.Get("ETag")
	status, headHeaders, headBody := f.request("HEAD", base+"/file?path=range.bin", nil, nil)
	want(t, status, 200, headBody)
	Expect(len(headBody) != 0 || headHeaders.Get("Content-Length") != "20" || headHeaders.Get("ETag") != etag || headHeaders.Get("Repr-Digest") == "").To(BeFalse(), func() string { return fmt.Sprint("HEAD did not describe exact immutable GET representation") })

	hash := sha256.Sum256([]byte(files["range.bin"]))
	Expect(etag != `"sha256-`+hex.EncodeToString(hash[:])+`"`).To(BeFalse(), func() string { return fmt.Sprint("strong validator did not identify immutable bytes") })

	for _, test := range []struct{ rangeValue, body string }{{"bytes=2-5", "2345"}, {"bytes=17-", "hij"}, {"bytes=-3", "hij"}} {
		status, headers, data = get("range.bin", false, map[string]string{"Range": test.rangeValue, "If-Range": etag})
		want(t, status, 206, data)
		Expect(string(data) != test.body || headers.Get("Content-Range") == "" || headers.Get("ETag") != etag || headers.Get("Repr-Digest") == "" || headers.Get("Content-Digest") != "").To(BeFalse(), func() string { return fmt.Sprint("range detached from immutable object", test.rangeValue) })

	}
	for _, ifRange := range []string{`"different"`, "W/" + etag} {
		status, _, data = get("range.bin", false, map[string]string{"Range": "bytes=0-1", "If-Range": ifRange})
		want(t, status, 200, data)
		Expect(string(data) != files["range.bin"]).To(BeFalse(), func() string { return fmt.Sprint("If-Range mismatch stitched bytes") })

	}
	status, _, data = get("range.bin", false, map[string]string{"If-None-Match": "W/" + etag})
	want(t, status, 304, data)
	Expect(len(data) != 0).To(BeFalse(), func() string { return fmt.Sprint("304 carried private body") })

	status, _, data = get("range.bin", false, map[string]string{"If-Match": `"different"`})
	want(t, status, 412, data)
	for _, r := range []string{"bytes=999-", "bytes=9-1", "bytes=broken"} {
		status, headers, data = get("range.bin", false, map[string]string{"Range": r})
		want(t, status, 416, data)
		var invalid struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		Expect(json.Unmarshal(data, &invalid) != nil || invalid.Error.Code != "RANGE_NOT_SATISFIABLE").To(BeFalse(), func() string { return fmt.Sprint("range error escaped canonical JSON") })
		Expect(strings.Contains(r, "999") && headers.Get("Content-Range") != "bytes */20").To(BeFalse(), func() string { return fmt.Sprint("unsatisfiable range lost exact length") })

	}
	status, headers, data = get("zero.bin", false, map[string]string{"Range": "bytes=0-0"})
	want(t, status, 416, data)
	Expect(headers.Get("Content-Range") != "bytes */0").To(BeFalse(), func() string { return fmt.Sprint("empty immutable file range length lost") })

	status, _, data = f.request("GET", base+"/files?limit=1", nil, nil)
	want(t, status, 200, data)
	page := decoded[query.List[query.File]](t, data)
	Expect(page.NextCursor == nil).To(BeFalse(), func() string { return fmt.Sprint("fixture needs continuation") })

	other := f.publish(u, nil)
	status, _, data = f.request("GET", "/api/v1/versions/"+other.ID+"/files?limit=1&cursor="+url.QueryEscape(*page.NextCursor), nil, nil)
	want(t, status, 400, data)
	status, _, data = f.request("GET", base+"/file?path=range.bin&path=zero.bin", nil, nil)
	want(t, status, 400, data)
	status, _, data = f.request("GET", base+"/file?path=%ff", nil, nil)
	want(t, status, 400, data)
})
var _ = It("TestActualHTTPTrustedOriginRangePreflight", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	for _, method := range []string{"GET", "HEAD"} {
		request, err := http.NewRequest("OPTIONS", f.server.URL+"/api/v1/versions/"+id(t)+"/file?path=private.html", nil)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		request.Header.Set("Origin", f.server.URL)
		request.Header.Set("Access-Control-Request-Method", method)
		request.Header.Set("Access-Control-Request-Headers", "authorization, range, if-range, if-none-match")
		response, err := http.DefaultClient.Do(request)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		want(t, response.StatusCode, 204, body)
		Expect(response.Header.Get("Access-Control-Allow-Origin") != f.server.URL || response.Header.Get("Access-Control-Allow-Credentials") != "").To(BeFalse(), func() string { return fmt.Sprint("preflight expanded origin/cookie authority") })

		allowed := strings.ToLower(response.Header.Get("Access-Control-Allow-Headers"))
		for _, header := range []string{"authorization", "range", "if-range", "if-none-match"} {
			Expect(!strings.Contains(allowed, header)).To(BeFalse(), func() string { return fmt.Sprint("actual range preflight omitted", header) })

		}
		Expect(!strings.Contains(response.Header.Get("Access-Control-Allow-Methods"), method)).To(BeFalse(), func() string { return fmt.Sprint("supported file method omitted from preflight", method) })

		request.Header.Set("Origin", "http://untrusted.invalid")
		response, err = http.DefaultClient.Do(request)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		body, _ = io.ReadAll(response.Body)
		response.Body.Close()
		want(t, response.StatusCode, 403, body)
		Expect(response.Header.Get("Access-Control-Allow-Origin") != "").To(BeFalse(), func() string { return fmt.Sprint("unconfigured origin received private CORS grant") })

	}
})
var _ = It("TestActualHTTPFullManifestMetadataAndItemValidators", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	u := f.create()
	payload := []byte("<html>source retained</html>\n")
	f.put(u, "page.html", "file", payload)
	digest := sha256.Sum256(payload)
	metadata := map[string]any{"document": map[string]any{"title": "Metadata linked", "question": strings.Repeat("ordinary author text ", 10000)}, "content": map[string]any{"default_page": "page.html"}, "pages": []any{map[string]any{"page_path": "page.html", "label": "Exact page"}}, "sources": []any{map[string]any{"id": "source/한글", "kind": "snapshot", "retention_mode": "selected_snapshot", "snapshot_sha256": hex.EncodeToString(digest[:]), "asset_path": "page.html"}}, "evidence": []any{map[string]any{"id": "evidence/상세", "source_id": "source/한글", "capture_status": "reference_only", "path": "page.html"}}, "regions": []any{map[string]any{"id": "overview", "page_path": "page.html", "evidence_ids": []string{"evidence/상세"}}}}
	v := f.publish(u, metadata)
	base := "/api/v1/versions/" + v.ID
	status, _, summary := f.request("GET", base, nil, nil)
	want(t, status, 200, summary)
	Expect(bytes.Contains(summary, []byte("ordinary author text")) || len(summary) > 4096).To(BeFalse(), func() string { return fmt.Sprint("compact summary inlined full authored metadata") })

	status, _, data := f.request("GET", base+"/metadata?kind=normalized", nil, nil)
	want(t, status, 200, data)
	sum := sha256.Sum256(data)
	Expect(hex.EncodeToString(sum[:]) != v.ManifestSHA || !bytes.Contains(data, []byte("ordinary author text"))).To(BeFalse(), func() string { return fmt.Sprint("full normalized metadata was truncated/substituted") })

	var full struct {
		Regions []struct {
			ID          string   `json:"id"`
			PagePath    string   `json:"page_path"`
			EvidenceIDs []string `json:"evidence_ids"`
		} `json:"regions"`
		Sources []struct {
			ID string `json:"id"`
		} `json:"sources"`
	}
	Expect(json.Unmarshal(data, &full) != nil || len(full.Regions) != 1 || full.Regions[0].PagePath != "page.html" || len(full.Sources) != 1 || full.Sources[0].ID != "source/한글" || len(full.Regions[0].EvidenceIDs) != 1 || full.Regions[0].EvidenceIDs[0] != "evidence/상세").To(BeFalse(), func() string { return fmt.Sprint("full manifest lost exact region/source bindings") })

	region, err := f.store.Region(context.Background(), v.ID, "page.html", "overview")
	Expect(err != nil || !bytes.Contains(region.Metadata, []byte("evidence"))).To(BeFalse(), func() string { return fmt.Sprint("published typed region not linked", err) })

	status, _, data = f.request("GET", base+"/source-evidence/"+url.PathEscape("evidence/상세"), nil, nil)
	want(t, status, 200, data)
	evidence := decoded[map[string]json.RawMessage](t, data)
	Expect(!bytes.Contains(evidence["authored"], []byte(`"source_id":"source/한글"`)) || !bytes.Contains(evidence["source"], []byte(hex.EncodeToString(digest[:]))) || string(evidence["service_verification"]) != "null").To(BeFalse(), func() string { return fmt.Sprint("evidence view changed source identity or fabricated verification") })

	status, _, data = f.request("GET", base+"/metadata?kind=supplied", nil, nil)
	want(t, status, 200, data)
	sum = sha256.Sum256(data)
	Expect(hex.EncodeToString(sum[:]) != v.MetadataHash).To(BeFalse(), func() string { return fmt.Sprint("supplied metadata bytes/hash changed") })

	status, headers, data := f.request("GET", "/api/v1/documents/"+v.DocumentID, nil, nil)
	want(t, status, 200, data)
	doc := decoded[query.Document](t, data)
	Expect(doc.ETag != headers.Get("ETag") || doc.Revision < 1 || doc.UpdatedAt == "").To(BeFalse(), func() string { return fmt.Sprint("document validator/durable list ordering missing") })

	status, _, data = f.request("GET", "/api/v1/documents?project_id="+f.project, nil, nil)
	want(t, status, 200, data)
	docs := decoded[query.List[query.Document]](t, data)
	Expect(len(docs.Items) != 1 || docs.Items[0].ETag != doc.ETag).To(BeFalse(), func() string { return fmt.Sprint("collection validator substituted for item") })

	status, _, data = f.request("GET", doc.VersionsURL, nil, nil)
	want(t, status, 200, data)
	versions := decoded[query.List[query.Version]](t, data)
	item := decoded[query.Version](t, summary)
	Expect(len(versions.Items) != 1 || versions.Items[0].ETag != item.ETag).To(BeFalse(), func() string { return fmt.Sprint("version list/item validator mismatch") })

	status, _, data = f.request("GET", base+"/render-bootstrap", nil, nil)
	want(t, status, 200, data)
	bootstrap := decoded[query.Bootstrap](t, data)
	Expect(bootstrap.SelectedPage == nil || *bootstrap.SelectedPage != "page.html" || bootstrap.RendererAvailable).To(BeFalse(), func() string { return fmt.Sprint("compact default page context lost actual identity") })

})

// These are explicitly seeded durable workflow bindings, exercised through the
// production authenticator. No production credential-issuance route is added.
func (f *fixture) workflow(scope domain.CredentialScope) (string, string) {
	f.t.Helper()
	token, err := sec.Secret()
	if err != nil {
		f.t.Fatal(err)
	}
	principal := id(f.t)
	err = f.store.Write(context.Background(), func(tx app.WriteTx) error {
		state, err := tx.AuthState()
		if err != nil {
			return err
		}
		if err := tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: id(f.t), Active: true, Scope: scope, Revision: 1}); err != nil {
			return err
		}
		permissions := map[string]int64{}
		for _, project := range scope.ProjectIDs {
			current, err := tx.Scope(project)
			if err != nil {
				return err
			}
			permissions[project] = current.Project.PermissionsRevision
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: id(f.t), PrincipalID: principal, TokenHash: sec.Verifier("workflow", token), AuthEpoch: state.AuthEpoch, Scope: scope, Permissions: permissions, ConfigRevision: 1, CreatedAt: time.Now().UTC(), Revision: 1})
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return token, principal
}

var _ = It("TestActualHTTPScopedQueriesFilterBeforePaginationAndBindCursor", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	owner := f.token
	allowedProject := f.project
	foreignProject := id(t)
	{
		err := f.store.Write(context.Background(), func(tx app.WriteTx) error {
			return tx.CreateProject(domain.Project{ID: foreignProject, Name: "private other project", Lifecycle: "active"})
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	f.project = foreignProject
	u := f.create()
	f.put(u, "private.html", "file", []byte("private other project bytes"))
	foreign := f.publish(u, nil)
	f.project = allowedProject
	u = f.create()
	f.put(u, "allowed.html", "file", []byte("allowed version bytes"))
	allowed := f.publish(u, nil)
	u = f.create()
	f.put(u, "another.html", "file", []byte("another version"))
	another := f.publish(u, nil)
	projectScope := domain.CredentialScope{Kind: "workflow", Actions: []string{"project.read"}, ProjectIDs: []string{allowedProject}}
	reader, principal := f.workflow(projectScope)
	otherReader, _ := f.workflow(projectScope)
	f.token = reader
	status, _, data := f.request("GET", "/api/v1/projects?limit=1", nil, nil)
	want(t, status, 200, data)
	projects := decoded[query.List[query.Project]](t, data)
	Expect(len(projects.Items) != 1 || projects.Items[0].ID != allowedProject || projects.NextCursor != nil).To(BeFalse(), func() string { return fmt.Sprint("scope filtering followed pagination or exposed other project") })

	seen := map[string]bool{}
	cursor := ""
	firstCursor := ""
	for {
		target := "/api/v1/documents?limit=1"
		if cursor != "" {
			target += "&cursor=" + url.QueryEscape(cursor)
		}
		status, _, data = f.request("GET", target, nil, nil)
		want(t, status, 200, data)
		docs := decoded[query.List[query.Document]](t, data)
		for _, d := range docs.Items {
			Expect(d.ProjectID != allowedProject || d.ID == foreign.DocumentID || seen[d.ID]).To(BeFalse(), func() string { return fmt.Sprint("scoped list included private/duplicate document") })

			seen[d.ID] = true
		}
		if docs.NextCursor == nil {
			break
		}
		cursor = *docs.NextCursor
		if firstCursor == "" {
			firstCursor = cursor
		}
	}
	Expect(len(seen) != 2 || firstCursor == "").To(BeFalse(), func() string { return fmt.Sprint("scope pagination silently omitted allowed documents") })

	f.token = otherReader
	status, _, data = f.request("GET", "/api/v1/documents?limit=1&cursor="+url.QueryEscape(firstCursor), nil, nil)
	want(t, status, 400, data)
	f.token = reader
	status, _, data = f.request("GET", "/api/v1/documents/"+foreign.DocumentID, nil, nil)
	want(t, status, 404, data)
	{
		err := f.store.Write(context.Background(), func(tx app.WriteTx) error {
			p, err := tx.WorkflowPrincipal(principal)
			if err != nil {
				return err
			}
			p.Scope.ProjectIDs = nil
			p.Revision++
			return tx.PutWorkflowPrincipal(p)
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	status, _, data = f.request("GET", "/api/v1/documents?limit=1&cursor="+url.QueryEscape(firstCursor), nil, nil)
	want(t, status, 400, data)
	status, _, data = f.request("GET", "/api/v1/documents?limit=1", nil, nil)
	want(t, status, 200, data)
	Expect(len(decoded[query.List[query.Document]](t, data).Items) != 0).To(BeFalse(), func() string { return fmt.Sprint("current restriction did not precede list disclosure") })

	f.token = owner
	versionReader, _ := f.workflow(domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{allowedProject}, VersionIDs: []string{allowed.ID}})
	f.token = versionReader
	status, _, data = f.request("GET", "/api/v1/versions/"+allowed.ID+"/file?path=allowed.html", nil, nil)
	want(t, status, 200, data)
	Expect(string(data) != "allowed version bytes").To(BeFalse(), func() string { return fmt.Sprint("exact allowed version not delivered") })

	for _, v := range []string{another.ID, foreign.ID} {
		status, _, data = f.request("GET", "/api/v1/versions/"+v, nil, nil)
		want(t, status, 404, data)
	}
	status, _, data = f.request("GET", "/api/v1/documents/"+allowed.DocumentID, nil, nil)
	want(t, status, 404, data)
	status, _, data = f.request("GET", "/api/v1/documents/"+allowed.DocumentID+"/versions?limit=1", nil, nil)
	want(t, status, 200, data)
	versions := decoded[query.List[query.Version]](t, data)
	Expect(len(versions.Items) != 1 || versions.Items[0].ID != allowed.ID).To(BeFalse(), func() string { return fmt.Sprint("version-scoped list did not preserve exact allow set") })

	f.token = owner
	draftReader, _ := f.workflow(domain.CredentialScope{Kind: "workflow", Actions: []string{"draft.write"}, ProjectIDs: []string{allowedProject}, DraftIDs: []string{id(t)}})
	f.token = draftReader
	status, _, data = f.request("GET", "/api/v1/versions/"+allowed.ID+"/file?path=allowed.html", nil, nil)
	want(t, status, 403, data)
	f.token = ""
	status, _, data = f.request("GET", "/api/v1/versions/"+allowed.ID+"/file?path=allowed.html&path=private", nil, nil)
	want(t, status, 401, data)
	f.token = owner
})

type blockedBytes struct {
	query.Bytes
	entered, release chan struct{}
	once             sync.Once
}
type blockedRead struct {
	io.ReadSeeker
	parent *blockedBytes
	reads  int
}

func (r *blockedRead) Read(p []byte) (int, error) {
	r.reads++
	if r.reads == 2 {
		r.parent.once.Do(func() { close(r.parent.entered) })
		<-r.parent.release
	}
	return r.ReadSeeker.Read(p)
}
func (b *blockedBytes) Read(ctx context.Context, obj domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) error {
	return b.Bytes.Read(ctx, obj, pin, func(handle io.ReadSeeker) error { return consume(&blockedRead{ReadSeeker: handle, parent: b}) })
}

var _ = It("TestActualHTTPRevocationDuringPinnedVerifiedFileDelivery", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	u := f.create()
	content := bytes.Repeat([]byte("immutable verified source bytes\n"), 20000)
	f.put(u, "stream.bin", "file", content)
	v := f.publish(u, nil)
	block := &blockedBytes{Bytes: f.store, entered: make(chan struct{}), release: make(chan struct{})}
	f.query.Bytes = block
	request, err := http.NewRequest("GET", f.server.URL+"/api/v1/versions/"+v.ID+"/file?path=stream.bin", nil)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	request.Header.Set("Origin", f.server.URL)
	request.Header.Set("Authorization", "Bearer "+f.token)
	type result struct {
		body []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		defer GinkgoRecover()
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		done <- result{data, err}
	}()
	select {
	case <-block.entered:
	case <-time.After(10 * time.Second):
		close(block.release)
		Fail(fmt.Sprint("actual second read barrier not reached"))
	}
	readPins := func() int {
		count := 0
		{
			err := f.store.Write(context.Background(), func(tx app.WriteTx) error {
				rows, err := tx.UploadPins(u, "", 256)
				for _, p := range rows {
					if p.Kind == "read" && p.OwnerID == v.ID {
						count++
					}
				}
				return err
			})
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

		return count
	}
	if readPins() != 1 {
		close(block.release)
		Fail(fmt.Sprint("delivery did not retain its own active read pin"))
	}
	session, err := f.auth.Authenticate(context.Background(), f.token)
	if err != nil {
		close(block.release)
		Fail(fmt.Sprint(err))
	}
	if _, err := f.auth.Revoke(context.Background(), f.token, session.ID); err != nil {
		close(block.release)
		Fail(fmt.Sprint(err))
	}
	close(block.release)
	select {
	case got := <-done:
		if got.err == nil || len(got.body) >= len(content) || !bytes.Equal(got.body, content[:len(got.body)]) {
			Fail(fmt.Sprint("revocation released remaining bytes or reported complete delivery"))
		}
	case <-time.After(10 * time.Second):
		Fail(fmt.Sprint("revoked delivery did not terminate"))
	}
	deadline := time.Now().Add(3 * time.Second)
	for readPins() != 0 {
		Expect(time.Now().After(deadline)).To(BeFalse(), func() string { return fmt.Sprint("failed protected delivery leaked its read pin") })

		time.Sleep(10 * time.Millisecond)
	}
	status, _, data := f.request("GET", "/api/v1/versions/"+v.ID+"/file?path=stream.bin", nil, nil)
	want(t, status, 401, data)
	_, err = f.store.Version(context.Background(), v.ID)
	Expect(errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("session revocation erased accepted publication") })

})
