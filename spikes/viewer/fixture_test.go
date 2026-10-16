package viewer

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type heldContextBody struct {
	io.Reader
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *heldContextBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered); <-b.release })
	return b.Reader.Read(p)
}

func TestContextIODoesNotBlockDelivery(t *testing.T) {
	f, err := New(Config{State: ownedState(t), Base: "http://127.0.0.1:9270", Renderers: [2]string{"http://127.0.0.1:9272", "http://127.0.0.1:9273"}})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	token := fixtureOwner(t, f)
	host := http.HandlerFunc(f.trusted)
	headers := map[string]string{"X-Page-Binding": "page-a", "Content-Type": "text/plain"}
	if w := request(t, host, f.config.Base, "POST", "/viewer/context?id=held", token, f.config.Base, nil, headers); w.Code != 201 {
		t.Fatal(w.Code)
	}
	issued := object(t, request(t, host, f.config.Base, "POST", "/viewer/deliver", token, f.config.Base, []byte(`{"Version":"a"}`), nil))
	body := &heldContextBody{Reader: strings.NewReader("exact bytes"), entered: make(chan struct{}), release: make(chan struct{})}
	r := httptest.NewRequest("PUT", f.config.Base+"/viewer/context?id=held&offset=0", body)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Origin", f.config.Base)
	r.Header.Set("X-Page-Binding", "page-a")
	r.Header.Set("X-Bytes-SHA256", digest([]byte("exact bytes")))
	completed := make(chan int, 1)
	go func() { w := httptest.NewRecorder(); host.ServeHTTP(w, r); completed <- w.Code }()
	<-body.entered
	defer func() {
		select {
		case <-body.release:
		default:
			close(body.release)
		}
	}()
	progress := make(chan error, 1)
	go func() {
		b, _ := json.Marshal(map[string]any{"ID": issued["id"]})
		if w := request(t, host, f.config.Base, "POST", "/viewer/renew", token, f.config.Base, b, nil); w.Code != 204 {
			progress <- fmt.Errorf("renew %d", w.Code)
			return
		}
		d := &delivery{Owner: token, Kind: "download", Version: "a", Expires: time.Now().Add(time.Second)}
		writer := &deliveryWriter{ResponseWriter: httptest.NewRecorder(), fixture: f, delivery: d}
		if n, e := writer.Write([]byte("progress")); e != nil || n != 8 {
			progress <- fmt.Errorf("stream %d %v", n, e)
			return
		}
		if w := request(t, host, f.config.Base, "POST", "/viewer/context?id=other", token, f.config.Base, nil, headers); w.Code != 201 {
			progress <- fmt.Errorf("other context %d", w.Code)
			return
		}
		progress <- nil
	}()
	select {
	case e := <-progress:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("held context IO blocked independent delivery/renew/context")
	}
	close(body.release)
	if code := <-completed; code != 200 {
		t.Fatal(code)
	}
	for _, method := range []string{"PATCH", "PATCH"} {
		w := request(t, host, f.config.Base, method, "/viewer/context?id=held", token, f.config.Base, nil, map[string]string{"X-Page-Binding": "page-a", "X-Bytes-SHA256": digest([]byte("exact bytes"))})
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if w := request(t, host, f.config.Base, "POST", "/viewer/context?id=revoked", token, f.config.Base, nil, headers); w.Code != 201 {
		t.Fatal(w.Code)
	}
	held := &heldContextBody{Reader: strings.NewReader("unaccepted"), entered: make(chan struct{}), release: make(chan struct{})}
	revokedRequest := httptest.NewRequest("PUT", f.config.Base+"/viewer/context?id=revoked&offset=0", held)
	for k, v := range map[string]string{"Authorization": "Bearer " + token, "Origin": f.config.Base, "X-Page-Binding": "page-a", "X-Bytes-SHA256": digest([]byte("unaccepted"))} {
		revokedRequest.Header.Set(k, v)
	}
	go func() { w := httptest.NewRecorder(); host.ServeHTTP(w, revokedRequest); completed <- w.Code }()
	<-held.entered
	device := object(t, request(t, f.auth, f.config.Base, "GET", "/session", token, "", nil, nil))["id"]
	b, _ := json.Marshal(map[string]any{"ID": device})
	if w := request(t, f.auth, f.config.Base, "POST", "/devices/revoke", token, f.config.Base, b, nil); w.Code != 204 {
		close(held.release)
		t.Fatal(w.Code)
	}
	close(held.release)
	if code := <-completed; code != 401 {
		t.Fatal("held context accepted bytes after owner revocation", code)
	}
	if f.contexts["revoked"].Offset != 0 || f.contexts["revoked"].Sealed {
		t.Fatal("revoked context advanced or sealed")
	}
}

func ownedState(t *testing.T) string {
	t.Helper()
	parent := os.Getenv("HAERYONG_VIEWER_TEST_TMP_ROOT")
	if parent == "" {
		t.Fatal("HAERYONG_VIEWER_TEST_TMP_ROOT must select the verified agent scratch")
	}
	p, e := os.MkdirTemp(parent, "unit-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(p) })
	return p
}
func request(t *testing.T, h http.Handler, base, method, path, token, origin string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, base+path, bytes.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func object(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatalf("expected JSON, status %d", w.Code)
	}
	return out
}
func fixtureOwner(t *testing.T, f *Fixture) string {
	t.Helper()
	created := object(t, request(t, f.auth, f.config.Base, "POST", "/pair", "", f.config.Base, []byte(`{"Label":"unit owner"}`), nil))
	control, e := os.ReadFile(filepath.Join(f.config.State, "control"))
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(map[string]any{"ID": created["ID"], "Code": created["Code"], "Decision": "approved"})
	w := request(t, f.auth, f.config.Base, "POST", "/pair/decision", strings.TrimSpace(string(control)), "", b, nil)
	if w.Code != 200 {
		t.Fatalf("exact test approval %d", w.Code)
	}
	b, _ = json.Marshal(map[string]any{"ID": created["ID"], "Secret": created["Secret"]})
	return object(t, request(t, f.auth, f.config.Base, "POST", "/pair/redeem", "", f.config.Base, b, nil))["token"].(string)
}
func TestViewerAuthorityAndIntegrity(t *testing.T) {
	f, e := New(Config{State: ownedState(t), Base: "http://127.0.0.1:9270", Renderers: [2]string{"http://127.0.0.1:9272", "http://127.0.0.1:9273"}})
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	token := fixtureOwner(t, f)
	host := http.HandlerFunc(f.trusted)
	issued := object(t, request(t, host, f.config.Base, "POST", "/viewer/deliver", token, f.config.Base, []byte(`{"Version":"a"}`), nil))
	values := url.Values{"handoff": {issued["handoff"].(string)}, "credential": {issued["credential"].(string)}}
	renderer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.render(w, r, "a") })
	h := map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-site"}
	handshake := request(t, renderer, f.config.Renderers[0], "POST", "/__handoff", "", "null", []byte(values.Encode()), h)
	if handshake.Code != 200 {
		t.Fatalf("scoped null handshake %d", handshake.Code)
	}
	cookies := handshake.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("renderer cookie missing")
	}
	cookie := cookies[0].Name + "=" + cookies[0].Value
	t.Run("handoff replay and cross origin", func(t *testing.T) {
		for _, origin := range []string{"null", "http://127.0.0.1:9273", "https://unrelated.invalid"} {
			if w := request(t, renderer, f.config.Renderers[0], "POST", "/__handoff", "", origin, []byte(values.Encode()), h); w.Code != 403 {
				t.Fatalf("handoff %s %d", origin, w.Code)
			}
		}
	})
	t.Run("cookie sharing does not authorize a cross port sender", func(t *testing.T) {
		for _, origin := range []string{"", "http://127.0.0.1:9273", "null"} {
			w := request(t, renderer, f.config.Renderers[0], "GET", "/data.json", "", origin, nil, map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "same-site"})
			if w.Code != 403 {
				t.Fatalf("cross port %q %d", origin, w.Code)
			}
		}
		w := request(t, renderer, f.config.Renderers[0], "GET", "/data.json", "", f.config.Renderers[0], nil, map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "same-origin"})
		if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"version":"a"`)) {
			t.Fatal("bound version failed")
		}
	})

	t.Run("same-version logical tabs keep independent delivery cookies", func(t *testing.T) {
		second := object(t, request(t, host, f.config.Base, "POST", "/viewer/deliver", token, f.config.Base, []byte(`{"Version":"a"}`), nil))
		fields := url.Values{"handoff": {second["handoff"].(string)}, "credential": {second["credential"].(string)}}
		established := request(t, renderer, f.config.Renderers[0], "POST", "/__handoff", "", "null", []byte(fields.Encode()), h)
		if established.Code != 200 {
			t.Fatal(established.Code)
		}
		other := established.Result().Cookies()[0]
		if other.Name == cookies[0].Name {
			t.Fatal("logical tab overwrote sibling cookie")
		}
		otherCookie := other.Name + "=" + other.Value
		both := otherCookie + "; " + cookie
		f.mu.Lock()
		f.deliveries[second["id"].(string)].Expires = time.Now().Add(-time.Second)
		f.mu.Unlock()
		for _, tc := range []struct {
			selected string
			want     int
		}{{"", 200}, {second["id"].(string), 401}, {issued["id"].(string), 200}} {
			w := request(t, renderer, f.config.Renderers[0], "GET", "/data.json", "", f.config.Renderers[0], nil, map[string]string{"Cookie": both, "Sec-Fetch-Site": "same-origin", "X-Fixture-Delivery": tc.selected})
			if w.Code != tc.want {
				t.Fatalf("explicit selected expiry: %d want %d", w.Code, tc.want)
			}
		}
		f.mu.Lock()
		f.deliveries[second["id"].(string)].Expires = time.Now().Add(30 * time.Second)
		f.mu.Unlock()
		b, _ := json.Marshal(map[string]any{"ID": second["id"]})
		if w := request(t, host, f.config.Base, "POST", "/viewer/close", token, f.config.Base, b, nil); w.Code != 204 {
			t.Fatal(w.Code)
		}
		for _, tc := range []struct {
			cookie string
			want   int
		}{{cookie, 200}, {otherCookie, 401}, {otherCookie + "; " + cookie, 200}} {
			w := request(t, renderer, f.config.Renderers[0], "GET", "/data.json", "", f.config.Renderers[0], nil, map[string]string{"Cookie": tc.cookie, "Sec-Fetch-Site": "same-origin"})
			if w.Code != tc.want {
				t.Fatalf("independent logical tab: %d want %d", w.Code, tc.want)
			}
		}
	})
	t.Run("download is independent and exact-object scoped", func(t *testing.T) {
		issuedDownload := request(t, host, f.config.Base, "POST", "/viewer/download-issue", token, f.config.Base, []byte(`{"Version":"a"}`), nil)
		d := object(t, issuedDownload)
		downloadCookies := issuedDownload.Result().Cookies()
		if len(downloadCookies) != 1 || !downloadCookies[0].HttpOnly || downloadCookies[0].Path != "/viewer/download" {
			t.Fatal("download-only cookie missing")
		}
		selectedCookie := downloadCookies[0].Name + "=" + downloadCookies[0].Value
		for _, tc := range []struct {
			origin, site string
			want         int
		}{{f.config.Base, "same-origin", 206}, {f.config.Renderers[0], "same-site", 403}, {"", "same-site", 403}} {
			w := request(t, host, f.config.Base, "GET", "/viewer/download?id="+d["id"].(string), "", tc.origin, nil, map[string]string{"Cookie": selectedCookie, "Sec-Fetch-Site": tc.site, "Range": "bytes=0-65535"})
			if w.Code != tc.want {
				t.Fatalf("download cookie sender scope: %d want %d", w.Code, tc.want)
			}
		}
		id, credential := d["id"].(string), d["credential"].(string)
		b, _ := json.Marshal(map[string]any{"ID": issued["id"]})
		if w := request(t, host, f.config.Base, "POST", "/viewer/close", token, f.config.Base, b, nil); w.Code != 204 {
			t.Fatal("close failed")
		}
		headers := map[string]string{"Authorization": "Delivery " + credential, "Range": "bytes=0-65535"}
		w := request(t, host, f.config.Base, "GET", "/viewer/download?id="+id, "", "", nil, headers)
		if w.Code != 206 || w.Body.Len() != 65536 {
			t.Fatalf("independent selected range %d/%d", w.Code, w.Body.Len())
		}
		if w = request(t, host, f.config.Base, "GET", "/viewer/download?id="+id+"&path=data.json", "", "", nil, headers); w.Code != 403 {
			t.Fatal("selected-object scope bypass")
		}
		if w = request(t, renderer, f.config.Renderers[0], "GET", "/data.json", "", f.config.Renderers[0], nil, map[string]string{"Cookie": "hy_view_a=" + id + "." + credential, "Sec-Fetch-Site": "same-origin"}); w.Code != 401 {
			t.Fatal("download became renderer authority")
		}
		f.mu.Lock()
		f.deliveries[id].Expires = time.Now().Add(-time.Second)
		f.mu.Unlock()
		if w = request(t, host, f.config.Base, "GET", "/viewer/download?id="+id, "", "", nil, headers); w.Code != 401 {
			t.Fatal("expired download accepted")
		}
	})
	t.Run("corrupt and missing stored bytes fail before consumption", func(t *testing.T) {
		file, e := f.verifiedFile("a", "data.json")
		if e != nil {
			t.Fatal(e)
		}
		original, _ := io.ReadAll(file)
		file.Close()
		name := filepath.Join(f.config.State, "verified", "a", "data.json")
		os.WriteFile(name, []byte("corrupt"), 0600)
		if file, e := f.verifiedFile("a", "data.json"); e == nil {
			file.Close()
			t.Fatal("corrupt stored bytes accepted")
		}
		os.WriteFile(name, original, 0600)
		os.Remove(name)
		if file, e := f.verifiedFile("a", "data.json"); e == nil {
			file.Close()
			t.Fatal("missing stored bytes accepted")
		}
		os.WriteFile(name, original, 0600)
	})
	t.Run("origin quarantine survives restart with immutable bytes", func(t *testing.T) {
		f.closeAuth()
		f.closeAuth = nil
		next, e := New(f.config)
		if e != nil {
			t.Fatal(e)
		}
		defer next.Close()
		if next.inventories["a"]["attachment.bin"] != f.inventories["a"]["attachment.bin"] {
			t.Fatal("immutable snapshot changed on restart")
		}
		if code, _ := next.ownerStatus(token); code != 200 {
			t.Fatal("normal restart rotated owner session")
		}
		if len(next.deliveries) != 0 {
			t.Fatal("ephemeral delivery silently recreated")
		}
	})
}

func TestTransientContextValidation(t *testing.T) {
	f, e := New(Config{State: ownedState(t), Base: "http://127.0.0.1:9270", Renderers: [2]string{"http://127.0.0.1:9272", "http://127.0.0.1:9273"}})
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	token := fixtureOwner(t, f)
	host := http.HandlerFunc(f.trusted)
	call := func(id, method, binding, media, offset string, b []byte, sha string) *httptest.ResponseRecorder {
		return request(t, host, f.config.Base, method, "/viewer/context?id="+id+"&offset="+offset, token, f.config.Base, b, map[string]string{"X-Page-Binding": binding, "Content-Type": media, "X-Bytes-SHA256": sha})
	}
	t.Run("valid exact bytes idempotence and incorrect replays", func(t *testing.T) {
		b := []byte("한글🌊 exact selected scenario")
		if w := call("ok", "POST", "page-a", "text/plain", "", nil, ""); w.Code != 201 {
			t.Fatal(w.Code)
		}
		for _, tc := range []struct {
			offset, binding string
			body            []byte
			want            int
		}{{"1", "page-a", b, 409}, {"0", "page-b", b, 409}, {"0", "page-a", b, 200}, {"0", "page-a", b, 200}, {"0", "page-a", []byte("changed"), 409}} {
			if w := call("ok", "PUT", tc.binding, "application/octet-stream", tc.offset, tc.body, digest(tc.body)); w.Code != tc.want {
				t.Fatalf("chunk expected %d got %d", tc.want, w.Code)
			}
		}
		if w := call("ok", "PATCH", "page-a", "", "", nil, "wrong"); w.Code != 409 {
			t.Fatal("incorrect complete digest accepted")
		}
		first := object(t, call("ok", "PATCH", "page-a", "", "", nil, digest(b)))
		second := object(t, call("ok", "PATCH", "page-a", "", "", nil, digest(b)))
		if first["context_ref"] != second["context_ref"] || int(first["bytes"].(float64)) != len(b) {
			t.Fatal("seal replay changed ref/bytes")
		}
	})
	t.Run("invalid text JSON and unsupported media never produce ready", func(t *testing.T) {
		valid := []byte(`{"selection":[1,"한글🌊",{"scenario":"retry"}]}`)
		call("json-valid", "POST", "page-a", "application/json", "", nil, "")
		call("json-valid", "PUT", "page-a", "application/octet-stream", "0", valid, digest(valid))
		if w := call("json-valid", "PATCH", "page-a", "", "", nil, digest(valid)); w.Code != 200 {
			t.Fatal("valid nested JSON refused")
		}
		for i, tc := range []struct {
			media string
			bytes []byte
		}{{"text/plain", []byte{0xff, 0xfe}}, {"application/json", []byte(`{"broken":`)}, {"application/json", []byte(`{} {}`)}} {
			id := string(rune('a' + i))
			call(id, "POST", "page-a", tc.media, "", nil, "")
			call(id, "PUT", "page-a", "application/octet-stream", "0", tc.bytes, digest(tc.bytes))
			if w := call(id, "PATCH", "page-a", "", "", nil, digest(tc.bytes)); w.Code != 409 || f.contexts[id].Sealed {
				t.Fatalf("invalid context admitted %s", tc.media)
			}
		}
		if w := call("media", "POST", "page-a", "application/javascript", "", nil, ""); w.Code != 400 {
			t.Fatal("unsupported media admitted")
		}
	})
	t.Run("interrupted temporary transfer has no sealed prefix", func(t *testing.T) {
		call("partial", "POST", "page-a", "text/plain", "", nil, "")
		b := []byte("prefix")
		call("partial", "PUT", "page-a", "application/octet-stream", "0", b, digest(b))
		call("partial", "DELETE", "page-a", "", "", nil, "")
		if w := call("partial", "PATCH", "page-a", "", "", nil, digest(b)); w.Code != 409 {
			t.Fatal("interruption fabricated prefix success")
		}
	})
}

func TestOriginQuarantineRejectsRestartReassignment(t *testing.T) {
	c := Config{State: ownedState(t), Base: "http://127.0.0.1:9270", Renderers: [2]string{"http://127.0.0.1:9272", "http://127.0.0.1:9273"}, Remote: "https://private.test:9271", RemoteRenderers: [2]string{"https://private.test:9272", "https://private.test:9273"}}
	f, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	for _, remote := range []bool{false, true} {
		changed := c
		if remote {
			changed.RemoteRenderers[0], changed.RemoteRenderers[1] = changed.RemoteRenderers[1], changed.RemoteRenderers[0]
		} else {
			changed.Renderers[0], changed.Renderers[1] = changed.Renderers[1], changed.Renderers[0]
		}
		if next, e := New(changed); e == nil {
			next.Close()
			t.Fatal("origin reused for different immutable version")
		}
	}
	next, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	next.Close()
}

type revokingWriter struct {
	*httptest.ResponseRecorder
	revoke func()
	writes int
}

func (w *revokingWriter) Write(b []byte) (int, error) {
	n, e := w.ResponseRecorder.Write(b)
	w.writes++
	if w.writes == 1 {
		w.revoke()
	}
	return n, e
}
func TestProtectedStreamAndResourceRestore(t *testing.T) {
	f, e := New(Config{State: ownedState(t), Base: "http://127.0.0.1:9270", Renderers: [2]string{"http://127.0.0.1:9272", "http://127.0.0.1:9273"}, Remote: "https://private.test:9271", RemoteRenderers: [2]string{"https://private.test:9272", "https://private.test:9273"}})
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	host := http.HandlerFunc(f.trusted)
	token := fixtureOwner(t, f)
	issue := func() map[string]any {
		return object(t, request(t, host, f.config.Base, "POST", "/viewer/download-issue", token, f.config.Base, []byte(`{"Version":"a"}`), nil))
	}
	d := issue()
	id := d["id"].(string)
	device := object(t, request(t, f.auth, f.config.Base, "GET", "/session", token, "", nil, nil))["id"]
	revoked := &revokingWriter{ResponseRecorder: httptest.NewRecorder(), revoke: func() {
		b, _ := json.Marshal(map[string]any{"ID": device})
		if w := request(t, f.auth, f.config.Base, "POST", "/devices/revoke", token, f.config.Base, b, nil); w.Code != 204 {
			t.Fatal("revoke", w.Code)
		}
	}}
	r := httptest.NewRequest("GET", f.config.Base+"/viewer/download?id="+id, nil)
	r.Header.Set("Authorization", "Delivery "+d["credential"].(string))
	host.ServeHTTP(revoked, r)
	if revoked.writes != 1 || revoked.Body.Len() == 0 || revoked.Body.Len() >= 36*1024*1024 {
		t.Fatalf("revoked stream continued: %d writes %d bytes", revoked.writes, revoked.Body.Len())
	}
	last := f.observations[len(f.observations)-1]
	if last.Kind != "download-finished" || last.Error != "owner access ended during stream" {
		t.Fatalf("stream interruption lost cause: %+v", last)
	}
	closed := &deliveryWriter{ResponseWriter: httptest.NewRecorder(), fixture: f, delivery: &delivery{Owner: token, Closed: true}}
	// A revoked owner takes precedence; both early returns must record their cause.
	if _, e := closed.Write([]byte("x")); e == nil || closed.failure != "owner access ended during stream" {
		t.Fatal("missing owner interruption")
	}
	token = fixtureOwner(t, f)
	closed.delivery.Owner = token
	if _, e := closed.Write([]byte("x")); e == nil || closed.failure != "delivery closed" {
		t.Fatal("missing close interruption")
	}
	d = issue()
	if w := request(t, host, f.config.Base, "POST", "/viewer/version-state", token, f.config.Base, []byte(`{"Version":"a","State":"trash"}`), nil); w.Code != 204 {
		t.Fatal(w.Code)
	}
	for _, route := range []string{"/viewer/deliver", "/viewer/download-issue"} {
		if w := request(t, host, f.config.Base, "POST", route, token, f.config.Base, []byte(`{"Version":"a"}`), nil); w.Code != 410 {
			t.Fatalf("trash %s %d", route, w.Code)
		}
	}
	if w := request(t, host, f.config.Base, "GET", "/viewer/download?id="+d["id"].(string), "", "", nil, map[string]string{"Authorization": "Delivery " + d["credential"].(string)}); w.Code != 401 {
		t.Fatal("trash retained old download", w.Code)
	}
	if w := request(t, host, f.config.Base, "POST", "/viewer/version-state", token, f.config.Base, []byte(`{"Version":"a","State":"available"}`), nil); w.Code != 204 {
		t.Fatal(w.Code)
	}
	issue()
	control, _ := os.ReadFile(filepath.Join(f.config.State, "control"))
	local := strings.TrimSpace(string(control))
	for _, tc := range []struct{ base, token, origin string }{{f.config.Base, token, ""}, {f.config.Base, local, f.config.Base}, {f.config.Remote, local, ""}} {
		if w := request(t, host, tc.base, "POST", "/viewer/restore-fixture", tc.token, tc.origin, nil, nil); w.Code != 403 {
			t.Fatal("restore authority accepted", w.Code)
		}
	}
	if w := request(t, host, f.config.Base, "POST", "/viewer/restore-fixture", local, "", nil, nil); w.Code != 204 {
		t.Fatal("local restore failed", w.Code)
	}
	if code, _ := f.ownerStatus(token); code != 401 {
		t.Fatal("restore retained remembered owner")
	}
	for _, delivery := range f.deliveries {
		if !delivery.Closed {
			t.Fatal("restore retained delivery")
		}
	}
	if w := request(t, host, f.config.Base, "POST", "/viewer/deliver", token, f.config.Base, []byte(`{"Version":"a"}`), nil); w.Code != 401 {
		t.Fatal("restored owner still reads", w.Code)
	}
}

// Fault the real disposable auth store without changing credentials or handler policy.
func authQueryOutage(t *testing.T, f *Fixture) func() {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(f.config.State, "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("ALTER TABLE sessions RENAME TO held_sessions"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	restored := false
	restore := func() {
		if restored {
			return
		}
		if _, err := db.Exec("ALTER TABLE held_sessions RENAME TO sessions"); err != nil {
			t.Fatal(err)
		}
		restored = true
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(restore)
	return restore
}
func unavailableFixture(t *testing.T) (*Fixture, string) {
	t.Helper()
	f, err := New(Config{State: ownedState(t), Base: "http://127.0.0.1:9270", Renderers: [2]string{"http://127.0.0.1:9272", "http://127.0.0.1:9273"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f, fixtureOwner(t, f)
}
func expectOwnerUnavailable(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != 503 || w.Header().Get("Retry-After") != "2" {
		t.Fatal("unavailable lost status/retry", w.Code, w.Header())
	}
}
func TestOwnerUnavailablePreservesDeliveryCredentials(t *testing.T) {
	f, token := unavailableFixture(t)
	host := http.HandlerFunc(f.trusted)
	renderer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.render(w, r, "a") })
	issue := func() map[string]any {
		return object(t, request(t, host, f.config.Base, "POST", "/viewer/deliver", token, f.config.Base, []byte(`{"Version":"a"}`), nil))
	}
	first := issue()
	handoff := func(d map[string]any) *httptest.ResponseRecorder {
		fields := url.Values{"handoff": {d["handoff"].(string)}, "credential": {d["credential"].(string)}}
		return request(t, renderer, f.config.Renderers[0], "POST", "/__handoff", "", f.config.Base, []byte(fields.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	}
	established := handoff(first)
	if established.Code != 200 {
		t.Fatal(established.Code)
	}
	cookie := established.Result().Cookies()[0]
	pending := issue()
	download := object(t, request(t, host, f.config.Base, "POST", "/viewer/download-issue", token, f.config.Base, []byte(`{"Version":"a"}`), nil))
	before := len(f.deliveries)
	restore := authQueryOutage(t, f)
	expectOwnerUnavailable(t, request(t, host, f.config.Base, "GET", "/viewer/config", token, f.config.Base, nil, nil))
	expectOwnerUnavailable(t, request(t, host, f.config.Base, "POST", "/viewer/deliver", token, f.config.Base, []byte(`{"Version":"a"}`), nil))
	if len(f.deliveries) != before {
		t.Fatal("unavailable minted delivery")
	}
	denied := request(t, host, f.config.Base, "GET", "/viewer/config", token, "http://wrong.example", nil, nil)
	if denied.Code != 403 {
		t.Fatal("origin guard changed", denied.Code)
	}
	read := request(t, renderer, f.config.Renderers[0], "GET", "/data.json", "", f.config.Renderers[0], nil, map[string]string{"Cookie": cookie.Name + "=" + cookie.Value, "Sec-Fetch-Site": "same-origin"})
	expectOwnerUnavailable(t, read)
	if len(read.Result().Cookies()) != 0 {
		t.Fatal("unavailable expired valid cookie")
	}
	expectOwnerUnavailable(t, handoff(pending))
	if _, ok := f.handoffs[pending["handoff"].(string)]; !ok {
		t.Fatal("unavailable consumed handoff")
	}
	expectOwnerUnavailable(t, request(t, host, f.config.Base, "GET", "/viewer/download?id="+download["id"].(string), "", "", nil, map[string]string{"Authorization": "Delivery " + download["credential"].(string)}))
	d := f.deliveries[download["id"].(string)]
	writer := &deliveryWriter{ResponseWriter: httptest.NewRecorder(), fixture: f, delivery: d}
	if n, e := writer.Write([]byte("no unauthorized bytes")); n != 0 || e == nil || !strings.Contains(writer.failure, "unavailable") || d.Closed {
		t.Fatal("stream outage invalidated owner", n, e, writer.failure)
	}
	restore()
	if code, _ := f.ownerStatus(token); code != 200 {
		t.Fatal("remembered owner not restored", code)
	}
	read = request(t, renderer, f.config.Renderers[0], "GET", "/data.json", "", f.config.Renderers[0], nil, map[string]string{"Cookie": cookie.Name + "=" + cookie.Value, "Sec-Fetch-Site": "same-origin"})
	if read.Code != 200 {
		t.Fatal("same cookie not recovered", read.Code)
	}
	// Two retries race on the same one-use handle after the real outage is restored.
	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- handoff(pending).Code }()
	}
	counts := map[int]int{}
	counts[<-results]++
	counts[<-results]++
	if counts[200] != 1 || counts[403] != 1 {
		t.Fatal("handoff no longer single use", counts)
	}
}
func TestOwnerUnavailableLateContextChecks(t *testing.T) {
	f, token := unavailableFixture(t)
	host := http.HandlerFunc(f.trusted)
	headers := map[string]string{"X-Page-Binding": "same-page", "Content-Type": "text/plain"}
	original := f.auth
	call := func(method string, body []byte) *httptest.ResponseRecorder {
		h := map[string]string{}
		for k, v := range headers {
			h[k] = v
		}
		h["X-Bytes-SHA256"] = digest(body)
		return request(t, host, f.config.Base, method, "/viewer/context?id=late&offset=0", token, f.config.Base, body, h)
	}
	faultAt := func(at int) func() {
		calls := 0
		restore := func() {}
		f.auth = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/session" {
				calls++
				if calls == at {
					restore = authQueryOutage(t, f)
				}
			}
			original.ServeHTTP(w, r)
		})
		return func() { f.auth = original; restore() }
	}
	reset := faultAt(2)
	expectOwnerUnavailable(t, call("POST", nil))
	if f.contexts["late"] != nil {
		t.Fatal("failed initial check left phantom context")
	}
	files, _ := filepath.Glob(filepath.Join(f.config.State, "context-*"))
	if len(files) != 0 {
		t.Fatal("failed initial check created file")
	}
	reset()
	if w := call("POST", nil); w.Code != 201 {
		t.Fatal("retry reused phantom initialization", w.Code)
	}
	transfer := f.contexts["late"]
	raw := []byte("exact context")
	reset = faultAt(3)
	expectOwnerUnavailable(t, call("PUT", raw))
	b, e := os.ReadFile(transfer.File)
	if e != nil || len(b) != 0 || transfer.Offset != 0 || transfer.Sealed || transfer.Ref != "" {
		t.Fatal("late PUT adopted bytes", e)
	}
	reset()
	if w := call("PUT", raw); w.Code != 200 {
		t.Fatal("exact PUT retry failed", w.Code)
	}
	reset = faultAt(3)
	expectOwnerUnavailable(t, call("PATCH", raw))
	b, e = os.ReadFile(transfer.File)
	if e != nil || !bytes.Equal(b, raw) || transfer.Sealed || transfer.Ref != "" || transfer.Digest != "" {
		t.Fatal("late seal adopted context", e)
	}
	reset()
	if w := call("PATCH", raw); w.Code != 200 || !transfer.Sealed {
		t.Fatal("exact seal retry failed", w.Code)
	}
}
func TestOwnerUnavailableLateStreamCheck(t *testing.T) {
	f, token := unavailableFixture(t)
	original := f.auth
	calls := 0
	restore := func() {}
	f.auth = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 2 {
			restore = authQueryOutage(t, f)
		}
		original.ServeHTTP(w, r)
	})
	recorder := httptest.NewRecorder()
	delivery := &delivery{Owner: token, Expires: time.Now().Add(time.Minute)}
	writer := &deliveryWriter{ResponseWriter: recorder, fixture: f, delivery: delivery}
	n, err := writer.Write([]byte("exact bytes"))
	f.auth = original
	restore()
	if n != 0 || err == nil || !strings.Contains(writer.failure, "unavailable") || writer.bytes != 0 || recorder.Body.Len() != 0 || delivery.Closed {
		t.Fatal("late outage leaked bytes or invalidated delivery", n, err)
	}
	if n, err = writer.Write([]byte("exact bytes")); n != len("exact bytes") || err != nil {
		t.Fatal("stream retry not recoverable", n, err)
	}
	delivery.Owner = "invalid-session"
	if n, err = writer.Write([]byte("denied")); n != 0 || err == nil || !strings.Contains(writer.failure, "access ended") {
		t.Fatal("definitive denial not fenced", n, err)
	}
}
