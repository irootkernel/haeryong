package auth

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

	"haeryong.local/haeryong/internal/adapters/foundation"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	storage "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type fixture struct {
	core         *appauth.Service
	store        *foundation.Store
	server       *httptest.Server
	now          atomic.Int64
	databasePath string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := os.TempDir()
	var volume, parent syscall.Stat_t
	if syscall.Stat("/Volumes/RootKernel", &volume) == nil && syscall.Stat("/Volumes", &parent) == nil && volume.Dev != parent.Dev && syscall.Access("/Volumes/RootKernel", 2) == nil {
		base = "/Volumes/RootKernel/tmp"
		if err := os.MkdirAll(base, 0700); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := os.MkdirTemp(base, "haeryong-task003-http-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	selection := service.Selection{Profile: "http", DataDir: filepath.Join(dir, "profile")}
	if _, err := service.Initialize(selection); err != nil {
		t.Fatal(err)
	}
	writer, err := service.OpenWriter(selection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	if err := writer.PrepareStorageBootstrap(nil); err != nil {
		t.Fatal(err)
	}
	store := foundation.New(nil)
	_, closer, err := store.Prepare(context.Background(), writer.Profile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closer() })
	if err := writer.MarkStorageReady(nil); err != nil {
		t.Fatal(err)
	}
	f := &fixture{store: store, core: appauth.New(store), databasePath: filepath.Join(writer.Profile.DataDir, "storage", "library.sqlite")}
	f.now.Store(time.Now().UnixNano())
	f.core.Now = func() time.Time { return time.Unix(0, f.now.Load()) }
	f.server = httptest.NewUnstartedServer(nil)
	origin := "http://" + f.server.Listener.Addr().String()
	handler, err := New(f.core, Policy{Hosts: []string{f.server.Listener.Addr().String()}, Origins: []string{origin}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, r, 200, map[string]any{"fixture": "synthetic private asset"}, nil)
	}))
	if err != nil {
		t.Fatal(err)
	}
	f.server.Config.Handler = handler
	f.server.Start()
	t.Cleanup(f.server.Close)
	return f
}
func (f *fixture) request(t *testing.T, method, path, token string, body any, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(data)
	}
	r, err := http.NewRequest(method, f.server.URL+path, payload)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", f.server.URL)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		if key == "Host" {
			r.Host = value
		} else {
			r.Header.Set(key, value)
		}
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header, data
}
func dataAs[T any](t *testing.T, data []byte) T {
	t.Helper()
	var envelope struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}
func (f *fixture) pairing(t *testing.T, label string) domain.PairingCreated {
	t.Helper()
	status, _, data := f.request(t, "POST", "/api/v1/auth/pairings", "", map[string]string{"device_label": label}, nil)
	if status != 201 {
		t.Fatalf("pairing create HTTP%d", status)
	}
	return dataAs[domain.PairingCreated](t, data)
}
func newOperation(t *testing.T) string {
	t.Helper()
	id, err := service.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func (f *fixture) owner(t *testing.T) (string, domain.Session) {
	t.Helper()
	p := f.pairing(t, "owner")
	if _, err := f.core.BootstrapDecide(context.Background(), domain.PairingDecision{ID: p.ID, Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: newOperation(t), Action: "approve"}); err != nil {
		t.Fatal(err)
	}
	status, _, data := f.request(t, "POST", "/api/v1/auth/pairings/"+p.ID+"/poll", "", map[string]string{"polling_secret": p.PollingSecret}, nil)
	if status != 200 {
		t.Fatalf("owner poll HTTP%d", status)
	}
	poll := dataAs[domain.PairingPoll](t, data)
	if poll.Token == "" || poll.Session == nil {
		t.Fatal("owner session not issued")
	}
	return poll.Token, *poll.Session
}

func TestProductionHostOriginAndHeaderBearerBoundary(t *testing.T) {
	f := newFixture(t)
	token, _ := f.owner(t)
	for _, item := range []struct {
		name, path, token string
		headers           map[string]string
		want              int
	}{
		{"wrong_host", "/api/v1/session", token, map[string]string{"Host": "unrelated.example"}, 403},
		{"wrong_origin", "/api/v1/session", token, map[string]string{"Origin": "https://unrelated.example"}, 403},
		{"opaque_origin", "/api/v1/session", token, map[string]string{"Origin": "null"}, 403},
		{"forwarded_identity", "/api/v1/session", "", map[string]string{"X-Forwarded-For": "127.0.0.1", "X-Forwarded-User": "owner"}, 401},
		{"cookie_identity", "/api/v1/session", "", map[string]string{"Cookie": "token=" + token}, 401},
		{"query_bearer", "/api/v1/session?token=" + token, "", nil, 401},
		{"unpaired_asset", "/api/v1/private/assets", "", nil, 401},
		{"unpaired_event", "/api/v1/events", "", nil, 401},
		{"unpaired_receipt", "/api/v1/operations/" + newOperation(t), "", nil, 401},
		{"native_bearer", "/api/v1/session", token, map[string]string{"Origin": ""}, 200},
	} {
		t.Run(item.name, func(t *testing.T) {
			status, headers, _ := f.request(t, "GET", item.path, item.token, nil, item.headers)
			if status != item.want {
				t.Fatalf("HTTP%d want%d", status, item.want)
			}
			if headers.Get("Cache-Control") != "no-store" {
				t.Fatal("private cache policy missing")
			}
		})
	}
	status, _, _ := f.request(t, "POST", "/api/v1/auth/pairings", "", map[string]string{"device_label": "no origin"}, map[string]string{"Origin": ""})
	if status != 403 {
		t.Fatalf("originless browser pairing HTTP%d", status)
	}
}

func TestProductionPairingDecisionCASAndAuthFirstReplay(t *testing.T) {
	f := newFixture(t)
	token, owner := f.owner(t)
	pending := f.pairing(t, "peer")
	key := newOperation(t)
	path := "/api/v1/auth/pairings/" + pending.ID + "/approve"
	body := map[string]string{"code": pending.Code, "origin": pending.Origin}
	headers := map[string]string{"If-Match": pending.ETag, "Idempotency-Key": key}
	status, response, data := f.request(t, "POST", path, token, body, headers)
	if status != 200 {
		t.Fatalf("approve HTTP%d", status)
	}
	approved := dataAs[domain.Pairing](t, data)
	if response.Get("ETag") != approved.ETag || approved.ETag == pending.ETag {
		t.Fatal("individual strong validator missing")
	}
	status, _, _ = f.request(t, "POST", path, token, body, headers)
	if status != 200 {
		t.Fatalf("matching old-ETag receipt replay HTTP%d", status)
	}
	status, _, _ = f.request(t, "POST", "/api/v1/auth/pairings/"+pending.ID+"/reject", token, body, map[string]string{"If-Match": pending.ETag, "Idempotency-Key": newOperation(t)})
	if status != 412 {
		t.Fatalf("concurrent stale decision HTTP%d", status)
	}
	status, _, _ = f.request(t, "POST", "/api/v1/auth/pairings/"+pending.ID+"/reject", token, body, map[string]string{"If-Match": approved.ETag, "Idempotency-Key": newOperation(t)})
	if status != 200 {
		t.Fatalf("intentional current-ETag later reject HTTP%d", status)
	}
	status, _, pollBytes := f.request(t, "POST", "/api/v1/auth/pairings/"+pending.ID+"/poll", "", map[string]string{"polling_secret": pending.PollingSecret}, nil)
	if status != 200 {
		t.Fatalf("rejected poll HTTP%d", status)
	}
	poll := dataAs[domain.PairingPoll](t, pollBytes)
	if poll.State != "rejected" || poll.Token != "" {
		t.Fatal("rejected request issued bearer")
	}
	if _, err := f.core.Revoke(context.Background(), token, owner.ID); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{path, "/api/v1/operations/" + key, "/api/v1/private/assets", "/api/v1/jobs", "/api/v1/events"} {
		method, payload, requestHeaders := "GET", any(nil), map[string]string(nil)
		if target == path {
			method, payload, requestHeaders = "POST", body, headers
		}
		status, _, _ = f.request(t, method, target, token, payload, requestHeaders)
		if status != 401 {
			t.Fatalf("revoked credential accessed %s HTTP%d", target, status)
		}
	}
}

func TestProductionApproveRejectRaceOneWinner(t *testing.T) {
	f := newFixture(t)
	token, _ := f.owner(t)
	pending := f.pairing(t, "racing peer")
	start := make(chan struct{})
	results := make(chan int, 2)
	for _, action := range []string{"approve", "reject"} {
		go func(action string) {
			<-start
			status, _, _ := f.request(t, "POST", "/api/v1/auth/pairings/"+pending.ID+"/"+action, token, map[string]string{"code": pending.Code, "origin": pending.Origin}, map[string]string{"If-Match": pending.ETag, "Idempotency-Key": newOperation(t)})
			results <- status
		}(action)
	}
	close(start)
	a, b := <-results, <-results
	if !((a == 200 && b == 412) || (a == 412 && b == 200)) {
		t.Fatalf("decision race statuses%d,%d", a, b)
	}
}

func TestProductionSingleUsePollAndRevokedActiveStream(t *testing.T) {
	f := newFixture(t)
	token, owner := f.owner(t)
	pending := f.pairing(t, "single use")
	if _, err := f.core.BootstrapDecide(context.Background(), domain.PairingDecision{ID: pending.ID, Code: pending.Code, Origin: pending.Origin, ETag: pending.ETag, OperationID: newOperation(t), Action: "approve"}); err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan int, 2)
	for range 2 {
		go func() {
			<-start
			status, _, _ := f.request(t, "POST", "/api/v1/auth/pairings/"+pending.ID+"/poll", "", map[string]string{"polling_secret": pending.PollingSecret}, nil)
			results <- status
		}()
	}
	close(start)
	a, b := <-results, <-results
	if !((a == 200 && b == 409) || (a == 409 && b == 200)) {
		t.Fatalf("single-use poll statuses%d,%d", a, b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", f.server.URL+"/api/v1/events", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Origin", f.server.URL)
	stream, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != 200 {
		t.Fatalf("stream HTTP%d", stream.StatusCode)
	}
	if _, err := f.core.Revoke(context.Background(), token, owner.ID); err != nil {
		t.Fatal(err)
	}
	bytes, err := io.ReadAll(stream.Body)
	if err != nil {
		t.Fatalf("revoked stream did not terminate before deadline: %v", err)
	}
	if strings.Contains(string(bytes), "data:") {
		t.Fatal("revoked stream delivered private event payload")
	}
}

func TestTrustedShellContainsNoPrivateAuthority(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/", "/connect", "/auth-client.js"} {
		status, headers, data := f.request(t, "GET", path, "", nil, nil)
		if status != 200 {
			t.Fatalf("public shell%s HTTP%d", path, status)
		}
		if headers.Get("Referrer-Policy") != "no-referrer" || !strings.Contains(headers.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatal("trusted host policy missing")
		}
		for _, secret := range []string{"control_key", "polling_secret\":\"", "Authorization: Bearer"} {
			if strings.Contains(string(data), secret) {
				t.Fatalf("public asset contains secret material marker %s", secret)
			}
		}
	}
}

func TestStrictAuthenticationControlObjectsAndMutationHeaders(t *testing.T) {
	f := newFixture(t)
	token, _ := f.owner(t)
	pending := f.pairing(t, "strict peer")
	for _, body := range []string{`null`, `{}`, `{"device_label":null}`, `{"device_label":1}`, `{"device_label":"one","device_label":"two"}`, `{"device_label":"one","extra":true}`, `[]`, `{"device_label":"one"} {}`} {
		r := httptest.NewRequest("POST", f.server.URL+"/api/v1/auth/pairings", strings.NewReader(body))
		r.Header.Set("Origin", f.server.URL)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		f.server.Config.Handler.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("ambiguous control object accepted: HTTP%d", w.Code)
		}
	}
	for _, kind := range []string{"missing_key", "quoted_key", "multiple_key", "missing_match", "weak_match", "multiple_match"} {
		body, _ := json.Marshal(map[string]string{"code": pending.Code, "origin": pending.Origin})
		r := httptest.NewRequest("POST", f.server.URL+"/api/v1/auth/pairings/"+pending.ID+"/approve", bytes.NewReader(body))
		r.Header.Set("Origin", f.server.URL)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", newOperation(t))
		r.Header.Set("If-Match", pending.ETag)
		want := 400
		switch kind {
		case "missing_key":
			r.Header.Del("Idempotency-Key")
			want = 428
		case "quoted_key":
			r.Header.Set("Idempotency-Key", `"`+newOperation(t)+`"`)
		case "multiple_key":
			r.Header.Add("Idempotency-Key", newOperation(t))
		case "missing_match":
			r.Header.Del("If-Match")
			want = 428
		case "weak_match":
			r.Header.Set("If-Match", "W/"+pending.ETag)
		case "multiple_match":
			r.Header.Add("If-Match", pending.ETag)
		}
		w := httptest.NewRecorder()
		f.server.Config.Handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s HTTP%d want%d", kind, w.Code, want)
		}
		if want == 428 && !strings.Contains(w.Body.String(), `"missing"`) {
			t.Fatal("missing precondition details absent")
		}
	}
	status, _, _ := f.request(t, "POST", "/api/v1/auth/pairings/"+pending.ID+"/approve", "", map[string]string{}, nil)
	if status != 401 {
		t.Fatalf("malformed private request parsed before authentication: %d", status)
	}
	rows, err := f.core.Pairings(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.ID == pending.ID {
			found = true
			if row.ETag != pending.ETag || row.State != "pending" {
				t.Fatal("rejected control input mutated pairing")
			}
		}
	}
	if !found {
		t.Fatal("pending request lost after malformed controls")
	}
}

func TestProductionReceiptReplayRechecksCurrentVisibility(t *testing.T) {
	f := newFixture(t)
	token, owner := f.owner(t)
	operation := newOperation(t)
	if err := f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		if err := tx.CreateProject(domain.Project{ID: "private-project", Name: "private", Lifecycle: "active", Revision: 1, PermissionsRevision: 1}); err != nil {
			return err
		}
		if err := tx.CreateDocument(domain.Document{ID: "hidden-document", ProjectID: "private-project", Title: "hidden", Lifecycle: "trash", Revision: 1}); err != nil {
			return err
		}
		request := domain.Request{Key: domain.OperationKey{PrincipalID: owner.PrincipalID, OperationID: operation}, Method: "POST", Target: "/api/v1/documents/hidden-document/fixture", Body: []byte(`{}`)}
		if _, _, err := tx.ReserveOperation(request, nil); err != nil {
			return err
		}
		return tx.CompleteOperation(request.Key, domain.Response{Status: 200, Body: []byte(`{"document_id":"hidden-document","fixture":"hidden private text"}`)})
	}); err != nil {
		t.Fatal(err)
	}
	status, _, data := f.request(t, "GET", "/api/v1/operations/"+operation, token, nil, nil)
	if status != 404 || strings.Contains(string(data), "hidden private text") {
		t.Fatalf("receipt bypassed current resource visibility: HTTP%d", status)
	}
}

func firstSSEData(t *testing.T, r io.Reader) (string, map[string]any) {
	t.Helper()
	scan := bufio.NewScanner(r)
	event := ""
	for scan.Scan() {
		line := scan.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			var data map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data); err != nil {
				t.Fatal(err)
			}
			return event, data
		}
	}
	t.Fatalf("SSE data absent: %v", scan.Err())
	return "", nil
}

func TestProductionEventReplayWireAndResync(t *testing.T) {
	f := newFixture(t)
	token, _ := f.owner(t)
	epoch, head, err := f.core.EventHead(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	for _, cursor := range []string{"malformed", newOperation(t) + ":0", epoch + ":" + strconv.FormatInt(head+100, 10)} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		r, _ := http.NewRequestWithContext(ctx, "GET", f.server.URL+"/api/v1/events", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Origin", f.server.URL)
		r.Header.Set("Last-Event-ID", cursor)
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		event, data := firstSSEData(t, response.Body)
		response.Body.Close()
		cancel()
		if event != "resync.required" || data["stream_epoch"] != epoch || data["head"] != strconv.FormatInt(head, 10) {
			t.Fatalf("unsafe resync shape: %s %#v", event, data)
		}
	}
	if err := f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		_, err := tx.MutateResource(domain.Resource{Kind: "fixture", ID: "one", Value: []byte(`{}`)}, 0, "resource.invalidated")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", f.server.URL+"/api/v1/events", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Origin", f.server.URL)
	r.Header.Set("Last-Event-ID", epoch+":"+strconv.FormatInt(head, 10))
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	event, data := firstSSEData(t, response.Body)
	if event != "resource.invalidated" || data["stream_epoch"] != epoch || data["sequence"] != strconv.FormatInt(head+1, 10) || data["library_id"] == "" {
		t.Fatalf("noncanonical event wire: %s %#v", event, data)
	}
	if _, ok := data["Sequence"]; ok {
		t.Fatal("domain representation leaked into event wire")
	}
}

func TestPassiveEventStreamHonorsConfiguredIdleExpiry(t *testing.T) {
	f := newFixture(t)
	token, _ := f.owner(t)
	configuration, err := f.store.Configuration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(configuration.Value, &config); err != nil {
		t.Fatal(err)
	}
	config["security"].(map[string]any)["session_idle_seconds"] = int64(1)
	configuration.Value, _ = json.Marshal(config)
	configuration.Actor = "fixture"
	if err := f.store.Write(context.Background(), func(tx storage.WriteTx) error { return tx.SetConfiguration(configuration, configuration.Revision) }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", f.server.URL+"/api/v1/events", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Origin", f.server.URL)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("idle stream HTTP%d", response.StatusCode)
	}
	f.now.Add(int64(2 * time.Second))
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatalf("passive event stream renewed idle expiry or did not stop: %v", err)
	}
	status, _, _ := f.request(t, "GET", "/api/v1/session", token, nil, nil)
	if status != 401 {
		t.Fatalf("idle-expired session still authorized HTTP%d", status)
	}
}

func TestFutureCapabilitiesAndUnknownRoutesHaveDistinctErrors(t *testing.T) {
	f := newFixture(t)
	token, _ := f.owner(t)
	h, err := New(f.core, Policy{Hosts: []string{strings.TrimPrefix(f.server.URL, "http://")}, Origins: []string{f.server.URL}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		method, path, credential, code string
		status                         int
	}{
		{"GET", "/api/v1/documents", token, "FEATURE_NOT_READY", 503},
		{"POST", "/api/v1/imports", token, "FEATURE_NOT_READY", 503},
		{"GET", "/api/v1/versions/example/files", token, "FEATURE_NOT_READY", 503},
		{"GET", "/api/v1/unknown", token, "NOT_FOUND", 404},
		{"DELETE", "/api/v1/documents", token, "NOT_FOUND", 404},
		{"POST", "/api/v1/auth/pairings/example/redeem", token, "NOT_FOUND", 404},
		{"GET", "/api/v1/documents", "", "AUTH_REQUIRED", 401},
		{"GET", "/api/v1/unknown", "", "AUTH_REQUIRED", 401},
	} {
		r := httptest.NewRequest(item.method, f.server.URL+item.path, nil)
		r.Header.Set("Origin", f.server.URL)
		if item.credential != "" {
			r.Header.Set("Authorization", "Bearer "+item.credential)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var response struct {
			Error struct {
				Code      string `json:"code"`
				Retryable bool   `json:"retryable"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if w.Code != item.status || response.Error.Code != item.code {
			t.Fatalf("%s %s HTTP%d/%s want%d/%s", item.method, item.path, w.Code, response.Error.Code, item.status, item.code)
		}
		if item.code == "FEATURE_NOT_READY" && (response.Error.Retryable || w.Header().Get("Retry-After") != "") {
			t.Fatal("unimplemented capability incorrectly advertised transient recovery")
		}
	}
	w := httptest.NewRecorder()
	fail(w, httptest.NewRequest("GET", "/", nil), errors.New("actual adapter failure"))
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"SERVICE_RECOVERING"`) {
		t.Fatal("actual outage lost recovery error")
	}
}

// Only the first event's socket write is delayed. SQLite/core batch capture has
// already finished, so changing a later target here tests delivery authority
// rather than merely testing the batch query's initial visibility filter.
type delayedEventWriter struct {
	*httptest.ResponseRecorder
	ctx     context.Context
	first   chan struct{}
	release chan struct{}
	cancel  context.CancelFunc
	blocked bool
}

func (w *delayedEventWriter) Write(data []byte) (int, error) {
	if !w.blocked && bytes.Contains(data, []byte("event: resource.invalidated\n")) {
		w.blocked = true
		close(w.first)
		select {
		case <-w.release:
		case <-w.ctx.Done():
			return 0, w.ctx.Err()
		}
	}
	n, err := w.ResponseRecorder.Write(data)
	if bytes.Contains(data, []byte("event: library.invalidated\n")) || bytes.Contains(data, []byte("event: document.lifecycle_changed\n")) {
		w.cancel()
	}
	return n, err
}

func TestEventDeliveryRechecksLaterBatchTargetAfterDelayedWrite(t *testing.T) {
	f := newFixture(t)
	token, _ := f.owner(t)
	epoch, head, err := f.core.EventHead(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	const hiddenID = "later-private-document"
	if err := f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		if err := tx.CreateProject(domain.Project{ID: "delayed-project", Name: "fixture", Lifecycle: "active", Revision: 1, PermissionsRevision: 1}); err != nil {
			return err
		}
		if err := tx.CreateDocument(domain.Document{ID: hiddenID, ProjectID: "delayed-project", Title: "synthetic private title", Lifecycle: "active", Revision: 1}); err != nil {
			return err
		}
		if _, err := tx.MutateResource(domain.Resource{Kind: "fixture", ID: "first-public-notification", Value: []byte(`{}`)}, 0, "resource.invalidated"); err != nil {
			return err
		}
		_, err := tx.MutateResource(domain.Resource{Kind: "document", ID: hiddenID, Value: []byte(`{}`)}, 0, "document.lifecycle_changed")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	batch, err := f.core.Events(context.Background(), token, epoch, head)
	if err != nil || len(batch) != 2 || batch[1].ResourceID != hiddenID {
		t.Fatalf("later target was not initially visible in real outbox batch: %#v %v", batch, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w := &delayedEventWriter{ResponseRecorder: httptest.NewRecorder(), ctx: ctx, first: make(chan struct{}), release: make(chan struct{}), cancel: cancel}
	r := httptest.NewRequest("GET", f.server.URL+"/api/v1/events", nil).WithContext(ctx)
	r.Header.Set("Origin", f.server.URL)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Last-Event-ID", epoch+":"+strconv.FormatInt(head, 10))
	finished := make(chan struct{})
	go func() { defer close(finished); f.server.Config.Handler.ServeHTTP(w, r) }()
	select {
	case <-w.first:
	case <-ctx.Done():
		t.Fatal("first actual SSE write did not reach barrier")
	}
	// Lifecycle mutation belongs to a later Task. This explicitly synthetic
	// fixture changes the real disposable SQLite row under the actual Store.Write
	// serialization lock; no product lifecycle endpoint or competing writer is
	// asserted. The normal transaction has no read/write statement before this
	// fixture update, and no DB transaction spans the blocked socket write.
	uri := url.URL{Scheme: "file", Path: f.databasePath}
	query := uri.Query()
	query.Set("mode", "rw")
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := f.store.Write(context.Background(), func(storage.WriteTx) error {
		result, err := db.ExecContext(context.Background(), "UPDATE documents SET lifecycle='trash',revision=revision+1 WHERE document_id=? AND lifecycle='active'", hiddenID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("fixture did not hide the actual document")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	document, err := f.store.Document(context.Background(), hiddenID)
	if err != nil || document.Lifecycle != "trash" {
		t.Fatalf("SQLite visibility transition did not commit: %#v %v", document, err)
	}
	close(w.release)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE did not complete bounded delayed-batch proof")
	}
	body := w.Body.String()
	if strings.Contains(body, hiddenID) || strings.Contains(body, "synthetic private title") || strings.Contains(body, "event: document.lifecycle_changed") {
		t.Fatalf("delayed event disclosed a now-hidden target: %s", body)
	}
	if !strings.Contains(body, "event: library.invalidated") {
		t.Fatal("now-hidden later event did not produce content-free invalidation")
	}
	scanner := bufio.NewScanner(strings.NewReader(body))
	var sequence string
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			var data map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &data); err != nil {
				t.Fatal(err)
			}
			if data["type"] == "library.invalidated" {
				if data["resource"] != nil {
					t.Fatal("content-free invalidation retained target identity")
				}
				sequence, _ = data["sequence"].(string)
			}
		}
	}
	if sequence != strconv.FormatInt(head+2, 10) {
		t.Fatalf("later event sequence was lost: %q", sequence)
	}
}

func TestCommonListEnvelopesPreserveItemsAndValidators(t *testing.T) {
	f := newFixture(t)
	token, owner := f.owner(t)
	pending := f.pairing(t, "listed peer")
	jobID := newOperation(t)
	if err := f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		return tx.CreateJob(domain.Job{ID: jobID, Kind: "import", State: "queued"})
	}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		path, idKey, id string
		validator       bool
	}{
		{"/api/v1/auth/pairings", "pairing_id", pending.ID, true},
		{"/api/v1/sessions", "session_id", owner.ID, true},
		{"/api/v1/jobs", "job_id", jobID, true},
	} {
		status, _, body := f.request(t, "GET", item.path, token, nil, nil)
		if status != 200 {
			t.Fatalf("list %s HTTP%d", item.path, status)
		}
		var response struct {
			Data      map[string]json.RawMessage `json:"data"`
			RequestID string                     `json:"request_id"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		if response.RequestID == "" || string(response.Data["next_cursor"]) != "null" {
			t.Fatalf("list missing common cursor envelope: %s", body)
		}
		var items []map[string]any
		if err := json.Unmarshal(response.Data["items"], &items); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entry := range items {
			if entry[item.idKey] == item.id {
				found = true
				if item.validator {
					if etag, ok := entry["etag"].(string); !ok || etag == "" {
						t.Fatal("list item lost its individual validator")
					}
				}
			}
		}
		if !found {
			t.Fatalf("list omitted its persisted item: %s", item.path)
		}
	}
}

func wireValidator(t *testing.T, value map[string]any) (float64, string) {
	t.Helper()
	revision, ok := value["revision"].(float64)
	if !ok || revision < 1 {
		t.Fatal("mutable wire row has no durable revision")
	}
	etag, ok := value["etag"].(string)
	if !ok || !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) {
		t.Fatal("mutable wire row has no strong validator")
	}
	return revision, etag
}

func listMember(t *testing.T, body []byte, key, id string) map[string]any {
	t.Helper()
	data := dataAs[struct {
		Items []map[string]any `json:"items"`
	}](t, body)
	for _, item := range data.Items {
		if item[key] == id {
			return item
		}
	}
	t.Fatal("persisted item missing from list")
	return nil
}

func TestSessionValidatorsTrackTouchRevocationAndDerivedExpiry(t *testing.T) {
	f := newFixture(t)
	token, owner := f.owner(t)
	pairing := f.pairing(t, "expiry peer")
	if _, err := f.core.BootstrapDecide(context.Background(), domain.PairingDecision{ID: pairing.ID, Code: pairing.Code, Origin: pairing.Origin, ETag: pairing.ETag, OperationID: newOperation(t), Action: "approve"}); err != nil {
		t.Fatal(err)
	}
	status, _, body := f.request(t, "POST", "/api/v1/auth/pairings/"+pairing.ID+"/poll", "", map[string]string{"polling_secret": pairing.PollingSecret}, nil)
	if status != 200 {
		t.Fatalf("peer issuance HTTP%d", status)
	}
	peer := dataAs[domain.PairingPoll](t, body)
	if peer.Session == nil {
		t.Fatal("peer session missing")
	}
	status, headers, body := f.request(t, "GET", "/api/v1/session", token, nil, nil)
	if status != 200 {
		t.Fatalf("current session HTTP%d", status)
	}
	before := dataAs[map[string]any](t, body)
	revision, etag := wireValidator(t, before)
	if headers.Get("ETag") != etag {
		t.Fatal("single session header/body validator differs")
	}
	status, _, body = f.request(t, "GET", "/api/v1/sessions", token, nil, nil)
	if status != 200 {
		t.Fatalf("session list HTTP%d", status)
	}
	listed := listMember(t, body, "session_id", owner.ID)
	listRevision, listETag := wireValidator(t, listed)
	if listRevision != revision || listETag != etag {
		t.Fatal("session list/single validators disagree at same logical time")
	}
	peerBefore := listMember(t, body, "session_id", peer.Session.ID)
	peerRevision, peerETag := wireValidator(t, peerBefore)
	f.now.Add(int64(time.Second))
	status, headers, body = f.request(t, "GET", "/api/v1/session", token, nil, nil)
	if status != 200 {
		t.Fatalf("touched session HTTP%d", status)
	}
	touched := dataAs[map[string]any](t, body)
	touchRevision, touchETag := wireValidator(t, touched)
	if touchRevision <= revision || touchETag == etag || headers.Get("ETag") != touchETag {
		t.Fatal("real session touch did not advance durable revision/validator")
	}
	configuration, err := f.store.Configuration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(configuration.Value, &config); err != nil {
		t.Fatal(err)
	}
	config["security"].(map[string]any)["session_idle_seconds"] = int64(1)
	configuration.Value, _ = json.Marshal(config)
	configuration.Actor = "fixture"
	if err := f.store.Write(context.Background(), func(tx storage.WriteTx) error { return tx.SetConfiguration(configuration, configuration.Revision) }); err != nil {
		t.Fatal(err)
	}
	status, _, body = f.request(t, "GET", "/api/v1/sessions", token, nil, nil)
	if status != 200 {
		t.Fatalf("expiry projection list HTTP%d", status)
	}
	expired := listMember(t, body, "session_id", peer.Session.ID)
	expiredRevision, expiredETag := wireValidator(t, expired)
	if expired["state"] != "expired" || expiredRevision != peerRevision || expiredETag == peerETag {
		t.Fatal("derived expiry changed row revision or failed to update public representation validator")
	}
	status, _, _ = f.request(t, "DELETE", "/api/v1/sessions/"+peer.Session.ID, token, nil, nil)
	if status != 204 {
		t.Fatalf("natural session revoke unexpectedly requires mutation key/validator: HTTP%d", status)
	}
	status, _, body = f.request(t, "GET", "/api/v1/sessions", token, nil, nil)
	if status != 200 {
		t.Fatalf("revoked list HTTP%d", status)
	}
	revoked := listMember(t, body, "session_id", peer.Session.ID)
	revokeRevision, revokeETag := wireValidator(t, revoked)
	if revoked["state"] != "revoked" || revokeRevision <= expiredRevision || revokeETag == expiredETag {
		t.Fatal("session revocation lost durable revision/public validator")
	}
}

func TestJobListSingleAndCancelValidatorsUseDurableRevision(t *testing.T) {
	f := newFixture(t)
	token, _ := f.owner(t)
	id := newOperation(t)
	if err := f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		if err := tx.CreateJob(domain.Job{ID: id, Kind: "import", State: "queued"}); err != nil {
			return err
		}
		_, err := tx.ClaimJob(id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	status, headers, body := f.request(t, "GET", "/api/v1/jobs/"+id, token, nil, nil)
	if status != 200 {
		t.Fatalf("running job HTTP%d", status)
	}
	before := dataAs[map[string]any](t, body)
	revision, etag := wireValidator(t, before)
	if headers.Get("ETag") != etag {
		t.Fatal("job header/body validator differs")
	}
	status, _, body = f.request(t, "GET", "/api/v1/jobs", token, nil, nil)
	if status != 200 {
		t.Fatalf("job list HTTP%d", status)
	}
	listed := listMember(t, body, "job_id", id)
	listRevision, listETag := wireValidator(t, listed)
	if listRevision != revision || listETag != etag {
		t.Fatal("job list/single validators differ")
	}
	status, headers, body = f.request(t, "POST", "/api/v1/jobs/"+id+"/cancel", token, map[string]any{}, map[string]string{"Idempotency-Key": newOperation(t)})
	if status != 200 {
		t.Fatalf("job cancel HTTP%d", status)
	}
	cancelled := dataAs[map[string]any](t, body)
	cancelRevision, cancelETag := wireValidator(t, cancelled)
	if cancelled["state"] != "cancelling" || cancelRevision <= revision || cancelETag == etag || headers.Get("ETag") != cancelETag {
		t.Fatal("cancel response lacks current mutable job validator")
	}
	status, _, body = f.request(t, "GET", "/api/v1/jobs", token, nil, nil)
	if status != 200 {
		t.Fatalf("cancelling list HTTP%d", status)
	}
	listed = listMember(t, body, "job_id", id)
	listRevision, listETag = wireValidator(t, listed)
	if listRevision != cancelRevision || listETag != cancelETag {
		t.Fatal("cancel receipt DTO does not match persisted list DTO")
	}
	generation := int64(cancelled["attempt_generation"].(float64))
	if err := f.store.Write(context.Background(), func(tx storage.WriteTx) error { return tx.ConfirmStop(id, generation) }); err != nil {
		t.Fatal(err)
	}
	status, headers, body = f.request(t, "GET", "/api/v1/jobs/"+id, token, nil, nil)
	if status != 200 {
		t.Fatalf("stopped job HTTP%d", status)
	}
	stopped := dataAs[map[string]any](t, body)
	stopRevision, stopETag := wireValidator(t, stopped)
	if stopped["state"] != "cancelled" || stopped["attempt_generation"] != cancelled["attempt_generation"] || stopRevision <= cancelRevision || stopETag == cancelETag || headers.Get("ETag") != stopETag {
		t.Fatal("same-generation semantic state change did not advance durable revision/validator")
	}
}
