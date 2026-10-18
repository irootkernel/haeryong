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

	"time"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	storage "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"haeryong.local/haeryong/internal/webassets"
	"regexp"
)

type fixture struct {
	core         *appauth.Service
	store        *foundation.Store
	server       *httptest.Server
	now          atomic.Int64
	databasePath string
}

func newFixture(t GinkgoTInterface) *fixture {
	GinkgoHelper()
	base := os.TempDir()
	var volume, parent syscall.Stat_t
	if syscall.Stat("/Volumes/RootKernel", &volume) == nil && syscall.Stat("/Volumes", &parent) == nil && volume.Dev != parent.Dev && syscall.Access("/Volumes/RootKernel", 2) == nil {
		base = "/Volumes/RootKernel/tmp"
		{
			err := os.MkdirAll(base, 0700)
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

	}
	dir, err := os.MkdirTemp(base, "haeryong-task003-http-test-")
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	DeferCleanup(func() { os.RemoveAll(dir) })
	selection := service.Selection{Profile: "http", DataDir: filepath.Join(dir, "profile")}
	{
		_, err := service.Initialize(selection)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	writer, err := service.OpenWriter(selection)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	DeferCleanup(func() { writer.Close() })
	{
		err := writer.PrepareStorageBootstrap(nil)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	store := foundation.New(nil)
	_, closer, err := store.Prepare(context.Background(), writer.Profile)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	DeferCleanup(func() { closer() })
	{
		err := writer.MarkStorageReady(nil)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	f := &fixture{store: store, core: appauth.New(store), databasePath: filepath.Join(writer.Profile.DataDir, "storage", "library.sqlite")}
	f.now.Store(time.Now().UnixNano())
	f.core.Now = func() time.Time { return time.Unix(0, f.now.Load()) }
	f.server = httptest.NewUnstartedServer(nil)
	origin := "http://" + f.server.Listener.Addr().String()
	handler, err := New(f.core, Policy{Hosts: []string{f.server.Listener.Addr().String()}, Origins: []string{origin}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, r, 200, map[string]any{"fixture": "synthetic private asset"}, nil)
	}))
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	f.server.Config.Handler = handler
	f.server.Start()
	DeferCleanup(f.server.Close)
	return f
}
func (f *fixture) request(t GinkgoTInterface, method, path, token string, body any, headers map[string]string) (int, http.Header, []byte) {
	GinkgoHelper()
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

		payload = bytes.NewReader(data)
	}
	r, err := http.NewRequest(method, f.server.URL+path, payload)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

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
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return response.StatusCode, response.Header, data
}
func dataAs[T any](t GinkgoTInterface, data []byte) T {
	GinkgoHelper()
	var envelope struct {
		Data T `json:"data"`
	}
	{
		err := json.Unmarshal(data, &envelope)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	return envelope.Data
}
func (f *fixture) pairing(t GinkgoTInterface, label string) domain.PairingCreated {
	GinkgoHelper()
	status, _, data := f.request(t, "POST", "/api/v1/auth/pairings", "", map[string]string{"device_label": label}, nil)
	Expect(status != 201).To(BeFalse(), func() string { return fmt.Sprintf("pairing create HTTP%d", status) })

	return dataAs[domain.PairingCreated](t, data)
}
func newOperation(t GinkgoTInterface) string {
	GinkgoHelper()
	id, err := service.NewID()
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return id
}
func (f *fixture) owner(t GinkgoTInterface) (string, domain.Session) {
	GinkgoHelper()
	p := f.pairing(t, "owner")
	{
		_, err := f.core.BootstrapDecide(context.Background(), domain.PairingDecision{ID: p.ID, Code: p.Code, Origin: p.Origin, ETag: p.ETag, OperationID: newOperation(t), Action: "approve"})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	status, _, data := f.request(t, "POST", "/api/v1/auth/pairings/"+p.ID+"/poll", "", map[string]string{"polling_secret": p.PollingSecret}, nil)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("owner poll HTTP%d", status) })

	poll := dataAs[domain.PairingPoll](t, data)
	Expect(poll.Token == "" || poll.Session == nil).To(BeFalse(), func() string { return fmt.Sprint("owner session not issued") })

	return poll.Token, *poll.Session
}

var _ = It("TestProductionHostOriginAndHeaderBearerBoundary", Label("persistence"), func() {
	t := GinkgoT()

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
		func() {
			By(item.name)
			status, headers, _ := f.request(t, "GET", item.path, item.token, nil, item.headers)
			Expect(status != item.want).To(BeFalse(), func() string { return fmt.Sprintf("HTTP%d want%d", status, item.want) })
			Expect(headers.Get("Cache-Control") != "no-store").To(BeFalse(), func() string { return fmt.Sprint("private cache policy missing") })

		}()
	}
	status, _, _ := f.request(t, "POST", "/api/v1/auth/pairings", "", map[string]string{"device_label": "no origin"}, map[string]string{"Origin": ""})
	Expect(status != 403).To(BeFalse(), func() string { return fmt.Sprintf("originless browser pairing HTTP%d", status) })

})
var _ = It("TestProductionPairingDecisionCASAndAuthFirstReplay", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	token, owner := f.owner(t)
	pending := f.pairing(t, "peer")
	key := newOperation(t)
	path := "/api/v1/auth/pairings/" + pending.ID + "/approve"
	body := map[string]string{"code": pending.Code, "origin": pending.Origin}
	headers := map[string]string{"If-Match": pending.ETag, "Idempotency-Key": key}
	status, response, data := f.request(t, "POST", path, token, body, headers)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("approve HTTP%d", status) })

	approved := dataAs[domain.Pairing](t, data)
	Expect(response.Get("ETag") != approved.ETag || approved.ETag == pending.ETag).To(BeFalse(), func() string { return fmt.Sprint("individual strong validator missing") })

	status, _, _ = f.request(t, "POST", path, token, body, headers)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("matching old-ETag receipt replay HTTP%d", status) })

	status, _, _ = f.request(t, "POST", "/api/v1/auth/pairings/"+pending.ID+"/reject", token, body, map[string]string{"If-Match": pending.ETag, "Idempotency-Key": newOperation(t)})
	Expect(status != 412).To(BeFalse(), func() string { return fmt.Sprintf("concurrent stale decision HTTP%d", status) })

	status, _, _ = f.request(t, "POST", "/api/v1/auth/pairings/"+pending.ID+"/reject", token, body, map[string]string{"If-Match": approved.ETag, "Idempotency-Key": newOperation(t)})
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("intentional current-ETag later reject HTTP%d", status) })

	status, _, pollBytes := f.request(t, "POST", "/api/v1/auth/pairings/"+pending.ID+"/poll", "", map[string]string{"polling_secret": pending.PollingSecret}, nil)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("rejected poll HTTP%d", status) })

	poll := dataAs[domain.PairingPoll](t, pollBytes)
	Expect(poll.State != "rejected" || poll.Token != "").To(BeFalse(), func() string { return fmt.Sprint("rejected request issued bearer") })
	{

		_, err := f.core.Revoke(context.Background(), token, owner.ID)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	for _, target := range []string{path, "/api/v1/operations/" + key, "/api/v1/private/assets", "/api/v1/jobs", "/api/v1/events"} {
		method, payload, requestHeaders := "GET", any(nil), map[string]string(nil)
		if target == path {
			method, payload, requestHeaders = "POST", body, headers
		}
		status, _, _ = f.request(t, method, target, token, payload, requestHeaders)
		Expect(status != 401).To(BeFalse(), func() string { return fmt.Sprintf("revoked credential accessed %s HTTP%d", target, status) })

	}
})
var _ = It("TestProductionApproveRejectRaceOneWinner", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	token, _ := f.owner(t)
	pending := f.pairing(t, "racing peer")
	start := make(chan struct{})
	results := make(chan int, 2)
	for _, action := range []string{"approve", "reject"} {
		go func(action string) {
			defer GinkgoRecover()
			<-start
			status, _, _ := f.request(t, "POST", "/api/v1/auth/pairings/"+pending.ID+"/"+action, token, map[string]string{"code": pending.Code, "origin": pending.Origin}, map[string]string{"If-Match": pending.ETag, "Idempotency-Key": newOperation(t)})
			results <- status
		}(action)
	}
	close(start)
	a, b := <-results, <-results
	Expect(!((a == 200 && b == 412) || (a == 412 && b == 200))).To(BeFalse(), func() string { return fmt.Sprintf("decision race statuses%d,%d", a, b) })

})
var _ = It("TestProductionSingleUsePollAndRevokedActiveStream", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	token, owner := f.owner(t)
	pending := f.pairing(t, "single use")
	{
		_, err := f.core.BootstrapDecide(context.Background(), domain.PairingDecision{ID: pending.ID, Code: pending.Code, Origin: pending.Origin, ETag: pending.ETag, OperationID: newOperation(t), Action: "approve"})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	start, results := make(chan struct{}), make(chan int, 2)
	for range 2 {
		go func() {
			defer GinkgoRecover()
			<-start
			status, _, _ := f.request(t, "POST", "/api/v1/auth/pairings/"+pending.ID+"/poll", "", map[string]string{"polling_secret": pending.PollingSecret}, nil)
			results <- status
		}()
	}
	close(start)
	a, b := <-results, <-results
	Expect(!((a == 200 && b == 409) || (a == 409 && b == 200))).To(BeFalse(), func() string { return fmt.Sprintf("single-use poll statuses%d,%d", a, b) })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", f.server.URL+"/api/v1/events", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Origin", f.server.URL)
	stream, err := http.DefaultClient.Do(r)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	defer stream.Body.Close()
	Expect(stream.StatusCode != 200).To(BeFalse(), func() string { return fmt.Sprintf("stream HTTP%d", stream.StatusCode) })
	{

		_, err := f.core.Revoke(context.Background(), token, owner.ID)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	bytes, err := io.ReadAll(stream.Body)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("revoked stream did not terminate before deadline: %v", err) })
	Expect(strings.Contains(string(bytes), "data:")).To(BeFalse(), func() string { return fmt.Sprint("revoked stream delivered private event payload") })

})
var _ = It("TestTrustedShellContainsNoPrivateAuthority", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	index, err := webassets.Index()
	Expect(err).To(BeNil())
	asset := ""
	for _, line := range strings.Split(string(index), "\n") {
		if match := regexp.MustCompile(`src="(/assets/[^"]+)"`).FindStringSubmatch(line); match != nil {
			asset = match[1]
			break
		}
	}
	Expect(asset).NotTo(BeEmpty(), "compiled shell references no asset")
	for _, path := range []string{"/", "/connect", asset} {
		status, headers, data := f.request(t, "GET", path, "", nil, nil)
		Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("public shell%s HTTP%d", path, status) })
		Expect(headers.Get("Referrer-Policy") != "no-referrer" || !strings.Contains(headers.Get("Content-Security-Policy"), "frame-ancestors 'none'")).To(BeFalse(), func() string { return fmt.Sprint("trusted host policy missing") })
		Expect(headers.Get("Cache-Control")).To(Equal("no-store"))

		for _, secret := range []string{"control_key", "polling_secret\":\"", "Authorization: Bearer"} {
			Expect(strings.Contains(string(data), secret)).To(BeFalse(), func() string { return fmt.Sprintf("public asset contains secret material marker %s", secret) })

		}
	}
})

var _ = It("TestShellServesCompiledAppOnRegistryRoutesWithDefaultsInjection", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	routes := []string{"/", "/connect", "/library", "/projects", "/maintenance", "/trash", "/settings",
		"/projects/11111111-1111-4111-8111-111111111111/sources",
		"/documents/11111111-1111-4111-8111-111111111111/versions/22222222-2222-4222-8222-222222222222?page=nested%2Fguide.html",
		"/maintenance/jobs/33333333-3333-4333-8333-333333333333",
		"/settings/sessions"}
	for _, route := range routes {
		status, headers, data := f.request(t, "GET", route, "", nil, nil)
		Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("shell route %s HTTP%d", route, status) })
		Expect(headers.Get("Content-Type")).To(Equal("text/html; charset=utf-8"))
		Expect(strings.Contains(string(data), `name="haeryong-session-defaults" content="remember-device=true"`)).To(BeTrue(), func() string { return "default remember injection missing on " + route })
		Expect(strings.Contains(string(data), `id="root"`)).To(BeTrue())
	}
	status, _, _ := f.request(t, "GET", "/settings/not-a-section", "", nil, nil)
	Expect(status).To(Equal(404), "unregistered shell path must not serve the app")

	index, err := webassets.Index()
	Expect(err).To(BeNil())
	css := regexp.MustCompile(`href="(/assets/[^"]+\.css)"`).FindStringSubmatch(string(index))
	Expect(css).NotTo(BeNil(), "compiled stylesheet reference missing")
	status, headers, _ := f.request(t, "GET", css[1], "", nil, nil)
	Expect(status).To(Equal(200))
	Expect(headers.Get("Content-Type")).To(Equal("text/css; charset=utf-8"))
	Expect(headers.Get("Cache-Control")).To(Equal("no-store"))
	for _, unknown := range []string{"/assets/missing.js", "/assets/../index.html", "/assets/"} {
		status, _, _ := f.request(t, "GET", unknown, "", nil, nil)
		Expect(status).To(Equal(404), func() string { return "unknown asset served: " + unknown })
	}
})
var _ = It("TestStrictAuthenticationControlObjectsAndMutationHeaders", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	token, _ := f.owner(t)
	pending := f.pairing(t, "strict peer")
	for _, body := range []string{`null`, `{}`, `{"device_label":null}`, `{"device_label":1}`, `{"device_label":"one","device_label":"two"}`, `{"device_label":"one","extra":true}`, `[]`, `{"device_label":"one"} {}`} {
		r := httptest.NewRequest("POST", f.server.URL+"/api/v1/auth/pairings", strings.NewReader(body))
		r.Header.Set("Origin", f.server.URL)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		f.server.Config.Handler.ServeHTTP(w, r)
		Expect(w.Code != 400).To(BeFalse(), func() string { return fmt.Sprintf("ambiguous control object accepted: HTTP%d", w.Code) })

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
		Expect(w.Code != want).To(BeFalse(), func() string { return fmt.Sprintf("%s HTTP%d want%d", kind, w.Code, want) })
		Expect(want == 428 && !strings.Contains(w.Body.String(), `"missing"`)).To(BeFalse(), func() string { return fmt.Sprint("missing precondition details absent") })

	}
	status, _, _ := f.request(t, "POST", "/api/v1/auth/pairings/"+pending.ID+"/approve", "", map[string]string{}, nil)
	Expect(status != 401).To(BeFalse(), func() string {
		return fmt.Sprintf("malformed private request parsed before authentication: %d", status)
	})

	rows, err := f.core.Pairings(context.Background(), token)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	found := false
	for _, row := range rows {
		if row.ID == pending.ID {
			found = true
			Expect(row.ETag != pending.ETag || row.State != "pending").To(BeFalse(), func() string { return fmt.Sprint("rejected control input mutated pairing") })

		}
	}
	Expect(!found).To(BeFalse(), func() string { return fmt.Sprint("pending request lost after malformed controls") })

})
var _ = It("TestProductionReceiptReplayRechecksCurrentVisibility", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	token, owner := f.owner(t)
	operation := newOperation(t)
	{
		err := f.store.Write(context.Background(), func(tx storage.WriteTx) error {
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
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	status, _, data := f.request(t, "GET", "/api/v1/operations/"+operation, token, nil, nil)
	Expect(status != 404 || strings.Contains(string(data), "hidden private text")).To(BeFalse(), func() string { return fmt.Sprintf("receipt bypassed current resource visibility: HTTP%d", status) })

})

func firstSSEData(t GinkgoTInterface, r io.Reader) (string, map[string]any) {
	GinkgoHelper()
	scan := bufio.NewScanner(r)
	event := ""
	for scan.Scan() {
		line := scan.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			var data map[string]any
			{
				err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data)
				Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
			}

			return event, data
		}
	}
	Fail(fmt.Sprintf("SSE data absent: %v", scan.Err()))
	return "", nil
}

var _ = It("TestProductionEventReplayWireAndResync", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	token, _ := f.owner(t)
	epoch, head, err := f.core.EventHead(context.Background(), token)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	for _, cursor := range []string{"malformed", newOperation(t) + ":0", epoch + ":" + strconv.FormatInt(head+100, 10)} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		r, _ := http.NewRequestWithContext(ctx, "GET", f.server.URL+"/api/v1/events", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Origin", f.server.URL)
		r.Header.Set("Last-Event-ID", cursor)
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			cancel()
			Fail(fmt.Sprint(err))
		}
		event, data := firstSSEData(t, response.Body)
		response.Body.Close()
		cancel()
		Expect(event != "resync.required" || data["stream_epoch"] != epoch || data["head"] != strconv.FormatInt(head, 10)).To(BeFalse(), func() string { return fmt.Sprintf("unsafe resync shape: %s %#v", event, data) })

	}
	{
		err := f.store.Write(context.Background(), func(tx storage.WriteTx) error {
			_, err := tx.MutateResource(domain.Resource{Kind: "fixture", ID: "one", Value: []byte(`{}`)}, 0, "resource.invalidated")
			return err
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", f.server.URL+"/api/v1/events", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Origin", f.server.URL)
	r.Header.Set("Last-Event-ID", epoch+":"+strconv.FormatInt(head, 10))
	response, err := http.DefaultClient.Do(r)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	defer response.Body.Close()
	event, data := firstSSEData(t, response.Body)
	Expect(event != "resource.invalidated" || data["stream_epoch"] != epoch || data["sequence"] != strconv.FormatInt(head+1, 10) || data["library_id"] == "").To(BeFalse(), func() string { return fmt.Sprintf("noncanonical event wire: %s %#v", event, data) })
	{

		_, ok := data["Sequence"]
		Expect(ok).To(BeFalse(), func() string { return fmt.Sprint("domain representation leaked into event wire") })
	}

})
var _ = It("TestPassiveEventStreamHonorsConfiguredIdleExpiry", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	token, _ := f.owner(t)
	configuration, err := f.store.Configuration(context.Background())
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	var config map[string]any
	{
		err := json.Unmarshal(configuration.Value, &config)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	config["security"].(map[string]any)["session_idle_seconds"] = int64(1)
	configuration.Value, _ = json.Marshal(config)
	configuration.Actor = "fixture"
	{
		err := f.store.Write(context.Background(), func(tx storage.WriteTx) error { return tx.SetConfiguration(configuration, configuration.Revision) })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", f.server.URL+"/api/v1/events", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Origin", f.server.URL)
	response, err := http.DefaultClient.Do(request)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	defer response.Body.Close()
	Expect(response.StatusCode != 200).To(BeFalse(), func() string { return fmt.Sprintf("idle stream HTTP%d", response.StatusCode) })

	f.now.Add(int64(2 * time.Second))
	{
		_, err := io.ReadAll(response.Body)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("passive event stream renewed idle expiry or did not stop: %v", err) })
	}

	status, _, _ := f.request(t, "GET", "/api/v1/session", token, nil, nil)
	Expect(status != 401).To(BeFalse(), func() string { return fmt.Sprintf("idle-expired session still authorized HTTP%d", status) })

})
var _ = It("TestFutureCapabilitiesAndUnknownRoutesHaveDistinctErrors", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	token, _ := f.owner(t)
	h, err := New(f.core, Policy{Hosts: []string{strings.TrimPrefix(f.server.URL, "http://")}, Origins: []string{f.server.URL}}, nil)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	for _, item := range []struct {
		method, path, credential, code string
		status                         int
	}{
		{"GET", "/api/v1/documents", token, "FEATURE_NOT_READY", 503},
		{"POST", "/api/v1/imports", token, "FEATURE_NOT_READY", 503},
		{"GET", "/api/v1/versions/example/files", token, "FEATURE_NOT_READY", 503},
		{"GET", "/api/v1/versions/example/source-evidence-verifications?evidence_id=.", token, "FEATURE_NOT_READY", 503},
		{"GET", "/api/v1/versions/example/source-evidence-verifications?evidence_id=..", token, "FEATURE_NOT_READY", 503},
		{"GET", "/api/v1/versions/example/source-evidence-verifications?evidence_id=%252e", token, "FEATURE_NOT_READY", 503},
		{"GET", "/api/v1/versions/example/source-evidence-verifications?evidence_id=evidence%2F%EC%83%81%EC%84%B8&cursor=next", token, "FEATURE_NOT_READY", 503},
		{"GET", "/api/v1/versions/example/source-evidence/evidence%2F%EC%83%81%EC%84%B8/verifications", token, "NOT_FOUND", 404},
		{"POST", "/api/v1/versions/example/source-evidence-verifications?evidence_id=.", token, "NOT_FOUND", 404},
		{"GET", "/api/v1/versions/example/source-evidence-verifications?evidence_id=.", "", "AUTH_REQUIRED", 401},
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
		{
			err := json.Unmarshal(w.Body.Bytes(), &response)
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}
		Expect(w.Code != item.status || response.Error.Code != item.code).To(BeFalse(), func() string {
			return fmt.Sprintf("%s %s HTTP%d/%s want%d/%s", item.method, item.path, w.Code, response.Error.Code, item.status, item.code)
		})
		Expect(item.code == "FEATURE_NOT_READY" && (response.Error.Retryable || w.Header().Get("Retry-After") != "")).To(BeFalse(), func() string { return fmt.Sprint("unimplemented capability incorrectly advertised transient recovery") })

	}
	w := httptest.NewRecorder()
	fail(w, httptest.NewRequest("GET", "/", nil), errors.New("actual adapter failure"))
	Expect(w.Code != 503 || !strings.Contains(w.Body.String(), `"SERVICE_RECOVERING"`)).To(BeFalse(), func() string { return fmt.Sprint("actual outage lost recovery error") })

})

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

var _ = It("TestEventDeliveryRechecksLaterBatchTargetAfterDelayedWrite", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	token, _ := f.owner(t)
	epoch, head, err := f.core.EventHead(context.Background(), token)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	const hiddenID = "later-private-document"
	{
		err := f.store.Write(context.Background(), func(tx storage.WriteTx) error {
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
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	batch, err := f.core.Events(context.Background(), token, epoch, head)
	Expect(err != nil || len(batch) != 2 || batch[1].ResourceID != hiddenID).To(BeFalse(), func() string {
		return fmt.Sprintf("later target was not initially visible in real outbox batch: %#v %v", batch, err)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w := &delayedEventWriter{ResponseRecorder: httptest.NewRecorder(), ctx: ctx, first: make(chan struct{}), release: make(chan struct{}), cancel: cancel}
	r := httptest.NewRequest("GET", f.server.URL+"/api/v1/events", nil).WithContext(ctx)
	r.Header.Set("Origin", f.server.URL)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Last-Event-ID", epoch+":"+strconv.FormatInt(head, 10))
	finished := make(chan struct{})
	go func() { defer GinkgoRecover(); defer close(finished); f.server.Config.Handler.ServeHTTP(w, r) }()
	select {
	case <-w.first:
	case <-ctx.Done():
		Fail(fmt.Sprint("first actual SSE write did not reach barrier"))
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
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	defer db.Close()
	db.SetMaxOpenConns(1)
	{
		err := f.store.Write(context.Background(), func(storage.WriteTx) error {
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
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	document, err := f.store.Document(context.Background(), hiddenID)
	Expect(err != nil || document.Lifecycle != "trash").To(BeFalse(), func() string {
		return fmt.Sprintf("SQLite visibility transition did not commit: %#v %v", document, err)
	})

	close(w.release)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		Fail(fmt.Sprint("SSE did not complete bounded delayed-batch proof"))
	}
	body := w.Body.String()
	Expect(strings.Contains(body, hiddenID) || strings.Contains(body, "synthetic private title") || strings.Contains(body, "event: document.lifecycle_changed")).To(BeFalse(), func() string { return fmt.Sprintf("delayed event disclosed a now-hidden target: %s", body) })
	Expect(!strings.Contains(body, "event: library.invalidated")).To(BeFalse(), func() string { return fmt.Sprint("now-hidden later event did not produce content-free invalidation") })

	scanner := bufio.NewScanner(strings.NewReader(body))
	var sequence string
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			var data map[string]any
			{
				err := json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &data)
				Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
			}

			if data["type"] == "library.invalidated" {
				Expect(data["resource"] != nil).To(BeFalse(), func() string { return fmt.Sprint("content-free invalidation retained target identity") })

				sequence, _ = data["sequence"].(string)
			}
		}
	}
	Expect(sequence != strconv.FormatInt(head+2, 10)).To(BeFalse(), func() string { return fmt.Sprintf("later event sequence was lost: %q", sequence) })

})
var _ = It("TestCommonListEnvelopesPreserveItemsAndValidators", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	token, owner := f.owner(t)
	pending := f.pairing(t, "listed peer")
	jobID := newOperation(t)
	{
		err := f.store.Write(context.Background(), func(tx storage.WriteTx) error {
			return tx.CreateJob(domain.Job{ID: jobID, Kind: "import", State: "queued"})
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
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
		Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("list %s HTTP%d", item.path, status) })

		var response struct {
			Data      map[string]json.RawMessage `json:"data"`
			RequestID string                     `json:"request_id"`
		}
		{
			err := json.Unmarshal(body, &response)
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}
		Expect(response.RequestID == "" || string(response.Data["next_cursor"]) != "null").To(BeFalse(), func() string { return fmt.Sprintf("list missing common cursor envelope: %s", body) })

		var items []map[string]any
		{
			err := json.Unmarshal(response.Data["items"], &items)
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

		found := false
		for _, entry := range items {
			if entry[item.idKey] == item.id {
				found = true
				if item.validator {
					{
						etag, ok := entry["etag"].(string)
						Expect(!ok || etag == "").To(BeFalse(), func() string { return fmt.Sprint("list item lost its individual validator") })
					}

				}
			}
		}
		Expect(!found).To(BeFalse(), func() string { return fmt.Sprintf("list omitted its persisted item: %s", item.path) })

	}
})

func wireValidator(t GinkgoTInterface, value map[string]any) (float64, string) {
	GinkgoHelper()
	revision, ok := value["revision"].(float64)
	Expect(!ok || revision < 1).To(BeFalse(), func() string { return fmt.Sprint("mutable wire row has no durable revision") })

	etag, ok := value["etag"].(string)
	Expect(!ok || !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`)).To(BeFalse(), func() string { return fmt.Sprint("mutable wire row has no strong validator") })

	return revision, etag
}

func listMember(t GinkgoTInterface, body []byte, key, id string) map[string]any {
	GinkgoHelper()
	data := dataAs[struct {
		Items []map[string]any `json:"items"`
	}](t, body)
	for _, item := range data.Items {
		if item[key] == id {
			return item
		}
	}
	Fail(fmt.Sprint("persisted item missing from list"))
	return nil
}

var _ = It("TestSessionValidatorsTrackTouchRevocationAndDerivedExpiry", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	token, owner := f.owner(t)
	pairing := f.pairing(t, "expiry peer")
	{
		_, err := f.core.BootstrapDecide(context.Background(), domain.PairingDecision{ID: pairing.ID, Code: pairing.Code, Origin: pairing.Origin, ETag: pairing.ETag, OperationID: newOperation(t), Action: "approve"})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	status, _, body := f.request(t, "POST", "/api/v1/auth/pairings/"+pairing.ID+"/poll", "", map[string]string{"polling_secret": pairing.PollingSecret}, nil)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("peer issuance HTTP%d", status) })

	peer := dataAs[domain.PairingPoll](t, body)
	Expect(peer.Session == nil).To(BeFalse(), func() string { return fmt.Sprint("peer session missing") })

	status, headers, body := f.request(t, "GET", "/api/v1/session", token, nil, nil)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("current session HTTP%d", status) })

	before := dataAs[map[string]any](t, body)
	revision, etag := wireValidator(t, before)
	Expect(headers.Get("ETag") != etag).To(BeFalse(), func() string { return fmt.Sprint("single session header/body validator differs") })

	status, _, body = f.request(t, "GET", "/api/v1/sessions", token, nil, nil)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("session list HTTP%d", status) })

	listed := listMember(t, body, "session_id", owner.ID)
	listRevision, listETag := wireValidator(t, listed)
	Expect(listRevision != revision || listETag != etag).To(BeFalse(), func() string { return fmt.Sprint("session list/single validators disagree at same logical time") })

	peerBefore := listMember(t, body, "session_id", peer.Session.ID)
	peerRevision, peerETag := wireValidator(t, peerBefore)
	f.now.Add(int64(time.Second))
	status, headers, body = f.request(t, "GET", "/api/v1/session", token, nil, nil)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("touched session HTTP%d", status) })

	touched := dataAs[map[string]any](t, body)
	touchRevision, touchETag := wireValidator(t, touched)
	Expect(touchRevision <= revision || touchETag == etag || headers.Get("ETag") != touchETag).To(BeFalse(), func() string { return fmt.Sprint("real session touch did not advance durable revision/validator") })

	configuration, err := f.store.Configuration(context.Background())
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	var config map[string]any
	{
		err := json.Unmarshal(configuration.Value, &config)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	config["security"].(map[string]any)["session_idle_seconds"] = int64(1)
	configuration.Value, _ = json.Marshal(config)
	configuration.Actor = "fixture"
	{
		err := f.store.Write(context.Background(), func(tx storage.WriteTx) error { return tx.SetConfiguration(configuration, configuration.Revision) })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	status, _, body = f.request(t, "GET", "/api/v1/sessions", token, nil, nil)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("expiry projection list HTTP%d", status) })

	expired := listMember(t, body, "session_id", peer.Session.ID)
	expiredRevision, expiredETag := wireValidator(t, expired)
	Expect(expired["state"] != "expired" || expiredRevision != peerRevision || expiredETag == peerETag).To(BeFalse(), func() string {
		return fmt.Sprint("derived expiry changed row revision or failed to update public representation validator")
	})

	status, _, _ = f.request(t, "DELETE", "/api/v1/sessions/"+peer.Session.ID, token, nil, nil)
	Expect(status != 204).To(BeFalse(), func() string {
		return fmt.Sprintf("natural session revoke unexpectedly requires mutation key/validator: HTTP%d", status)
	})

	status, _, body = f.request(t, "GET", "/api/v1/sessions", token, nil, nil)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("revoked list HTTP%d", status) })

	revoked := listMember(t, body, "session_id", peer.Session.ID)
	revokeRevision, revokeETag := wireValidator(t, revoked)
	Expect(revoked["state"] != "revoked" || revokeRevision <= expiredRevision || revokeETag == expiredETag).To(BeFalse(), func() string { return fmt.Sprint("session revocation lost durable revision/public validator") })

})
var _ = It("TestJobListSingleAndCancelValidatorsUseDurableRevision", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	token, _ := f.owner(t)
	id := newOperation(t)
	{
		err := f.store.Write(context.Background(), func(tx storage.WriteTx) error {
			if err := tx.CreateJob(domain.Job{ID: id, Kind: "import", State: "queued"}); err != nil {
				return err
			}
			_, err := tx.ClaimJob(id)
			return err
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	status, headers, body := f.request(t, "GET", "/api/v1/jobs/"+id, token, nil, nil)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("running job HTTP%d", status) })

	before := dataAs[map[string]any](t, body)
	revision, etag := wireValidator(t, before)
	Expect(headers.Get("ETag") != etag).To(BeFalse(), func() string { return fmt.Sprint("job header/body validator differs") })

	status, _, body = f.request(t, "GET", "/api/v1/jobs", token, nil, nil)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("job list HTTP%d", status) })

	listed := listMember(t, body, "job_id", id)
	listRevision, listETag := wireValidator(t, listed)
	Expect(listRevision != revision || listETag != etag).To(BeFalse(), func() string { return fmt.Sprint("job list/single validators differ") })

	status, headers, body = f.request(t, "POST", "/api/v1/jobs/"+id+"/cancel", token, map[string]any{}, map[string]string{"Idempotency-Key": newOperation(t)})
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("job cancel HTTP%d", status) })

	cancelled := dataAs[map[string]any](t, body)
	cancelRevision, cancelETag := wireValidator(t, cancelled)
	Expect(cancelled["state"] != "cancelling" || cancelRevision <= revision || cancelETag == etag || headers.Get("ETag") != cancelETag).To(BeFalse(), func() string { return fmt.Sprint("cancel response lacks current mutable job validator") })

	status, _, body = f.request(t, "GET", "/api/v1/jobs", token, nil, nil)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("cancelling list HTTP%d", status) })

	listed = listMember(t, body, "job_id", id)
	listRevision, listETag = wireValidator(t, listed)
	Expect(listRevision != cancelRevision || listETag != cancelETag).To(BeFalse(), func() string { return fmt.Sprint("cancel receipt DTO does not match persisted list DTO") })

	generation := int64(cancelled["attempt_generation"].(float64))
	{
		err := f.store.Write(context.Background(), func(tx storage.WriteTx) error { return tx.ConfirmStop(id, generation) })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	status, headers, body = f.request(t, "GET", "/api/v1/jobs/"+id, token, nil, nil)
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("stopped job HTTP%d", status) })

	stopped := dataAs[map[string]any](t, body)
	stopRevision, stopETag := wireValidator(t, stopped)
	Expect(stopped["state"] != "cancelled" || stopped["attempt_generation"] != cancelled["attempt_generation"] || stopRevision <= cancelRevision || stopETag == cancelETag || headers.Get("ETag") != stopETag).To(BeFalse(), func() string {
		return fmt.Sprint("same-generation semantic state change did not advance durable revision/validator")
	})

})

var _ = It("TestCORSRequestBindingAllowedResponseVerifiedExposedForFreshProof", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	token, _ := f.owner(t)
	// A permitted cross-origin trusted caller may send the fresh-proof
	// request pair: the preflight admits the request header alongside the
	// standard conditional header.
	preflight, _, _ := f.request(t, "OPTIONS", "/api/v1/contexts/00000000-0000-4000-8000-000000000001", token, nil, map[string]string{
		"Origin":                         f.server.URL,
		"Access-Control-Request-Method":  "GET",
		"Access-Control-Request-Headers": "haeryong-context-binding, if-none-match",
	})
	Expect(preflight != 204).To(BeFalse(), func() string { return fmt.Sprintf("preflight HTTP%d", preflight) })
	// The 304 proof response's dedicated marker is exposed for the same
	// caller, while no response marker header is submitted by clients.
	status, headers, _ := f.request(t, "GET", "/api/v1/session", token, nil, map[string]string{"Origin": f.server.URL})
	Expect(status != 200).To(BeFalse(), func() string { return fmt.Sprintf("origin GET HTTP%d", status) })
	Expect(headers.Get("Access-Control-Expose-Headers")).To(ContainSubstring("Haeryong-Context-Verified"))
	Expect(headers.Get("Access-Control-Expose-Headers")).To(ContainSubstring("ETag"))
	// Supplying the response marker as a request header is not part of the
	// allowed request surface.
	rejected, _, _ := f.request(t, "OPTIONS", "/api/v1/contexts/00000000-0000-4000-8000-000000000001", token, nil, map[string]string{
		"Origin":                         f.server.URL,
		"Access-Control-Request-Method":  "GET",
		"Access-Control-Request-Headers": "haeryong-context-verified",
	})
	Expect(rejected != 403).To(BeFalse(), func() string { return fmt.Sprintf("response marker accepted as request header HTTP%d", rejected) })
})
