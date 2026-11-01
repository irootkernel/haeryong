package source_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	sourcefiles "haeryong.local/haeryong/internal/adapters/source"
	"haeryong.local/haeryong/internal/application/service"
	appsource "haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("SCAP16 public exact source capture/status/read/HEAD/diff keep hostile HTML inert and enforce current scope before bytes or counts", Label("persistence"), func() {
	f := newHTTPFixture(GinkgoT())
	f.buildEvidenceVersion(GinkgoT(), []string{"ev"}, strings.Repeat("a", 40))
	root := GinkgoT().TempDir()
	body := `<!doctype html><script>parent.document.body.textContent='owned';localStorage.getItem('haeryong.owner-session.v1');fetch('/api/v1/configuration/plans',{method:'POST'});parent.postMessage({kind:'review',action:'complete'},'*')</script>`
	Expect(os.WriteFile(filepath.Join(root, "source.html"), []byte(body), 0600)).To(Succeed())
	f.core.Files = sourcefiles.Files{}
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var value map[string]any
		if e = json.Unmarshal(cfg.Value, &value); e != nil {
			return e
		}
		value["projects"] = map[string]any{f.project: map[string]any{"sources": []domain.ManagedSourceMapping{{SourceID: "source", Kind: "files", RootPath: root, RetentionMode: "selected_snapshot", ProviderIDs: []string{}}}}}
		cfg.Value, _ = json.Marshal(value)
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
	scoped := f.seedScoped(GinkgoT(), true, true)
	key := "a6000000-0000-4000-8000-000000000001"
	payload := `{"version_id":"` + f.version + `","source_mode":"current","selections":[{"source_id":"source","paths":[{"path":"source.html"}]}]}`
	response := f.post("/api/v1/projects/"+f.project+"/source-captures", scoped, key, payload)
	Expect(response.Code).To(Equal(201), response.Body.String())
	var result struct {
		Data struct {
			ID string `json:"capture_id"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(response.Body.Bytes(), &result)).To(Succeed())
	get := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)
		return rec
	}
	view := get("GET", "/api/v1/source-captures/"+result.Data.ID, scoped)
	Expect(view.Code).To(Equal(200), view.Body.String())
	var page struct {
		Data struct {
			Items []domain.SourceCaptureRead `json:"items"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(view.Body.Bytes(), &page)).To(Succeed())
	Expect(page.Data.Items).To(HaveLen(1))
	read := page.Data.Items[0]
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		r := get(method, "/api/v1/source-reads/"+read.ID, scoped)
		Expect(r.Code).To(Equal(200))
		Expect(r.Header().Get("Content-Type")).To(Equal("application/octet-stream"))
		Expect(r.Header().Get("X-Content-Type-Options")).To(Equal("nosniff"))
		Expect(r.Header().Get("Content-Disposition")).To(ContainSubstring("attachment"))
		if method == "GET" {
			Expect(r.Body.String()).To(Equal(body))
		} else {
			Expect(r.Body.Len()).To(BeZero())
		}
	}
	Expect(get("GET", "/api/v1/source-captures/"+result.Data.ID+"/diff?other="+result.Data.ID, scoped).Code).To(Equal(200))
	Expect(get("GET", "/api/v1/source-reads/"+read.ID, "").Code).To(Equal(401))
	extra := strings.TrimSuffix(payload, "}") + `,"job_id":"caller-chosen"}`
	Expect(f.post("/api/v1/projects/"+f.project+"/source-captures", scoped, "a6000000-0000-4000-8000-000000000002", extra).Code).To(Equal(400))
	// A scoped reader cannot use an owner's same-version capture as an oracle.
	ownerCapture := f.post("/api/v1/projects/"+f.project+"/source-captures", f.token, "a6000000-0000-4000-8000-000000000003", payload)
	Expect(ownerCapture.Code).To(Equal(201))
	var ownerResult struct {
		Data struct {
			ID string `json:"capture_id"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(ownerCapture.Body.Bytes(), &ownerResult)).To(Succeed())
	Expect(get("GET", "/api/v1/source-captures/"+ownerResult.Data.ID, scoped).Code).To(Equal(404))
	// Current source mapping loss hides all metadata and byte response bodies.
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var value map[string]any
		if e = json.Unmarshal(cfg.Value, &value); e != nil {
			return e
		}
		value["projects"] = map[string]any{}
		cfg.Value, _ = json.Marshal(value)
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
	for _, path := range []string{"/api/v1/source-captures/" + result.Data.ID, "/api/v1/source-captures/" + result.Data.ID + "/diff?other=" + result.Data.ID, "/api/v1/source-reads/" + read.ID} {
		r := get("GET", path, scoped)
		Expect(r.Code).To(Equal(404))
		Expect(r.Body.String()).NotTo(ContainSubstring("source.html"))
		Expect(r.Body.String()).NotTo(ContainSubstring(read.Hash))
		Expect(r.Body.String()).NotTo(ContainSubstring("<script>"))
		Expect(r.Body.String()).NotTo(ContainSubstring("snapshot_sha256"))
	}
})

var _ = It("SCAP24 changed/missing exact source and corrupt stored bytes return canonical unavailable/integrity errors without payload", Label("persistence"), func() {
	f := newHTTPFixture(GinkgoT())
	health, e := f.store.Health(context.Background())
	Expect(e).NotTo(HaveOccurred())
	project := health.Library.UnfiledProjectID
	root := GinkgoT().TempDir()
	body := []byte("exact input bytes")
	Expect(os.WriteFile(filepath.Join(root, "file"), body, 0600)).To(Succeed())
	f.core.Files = sourcefiles.Files{}
	configure := func(retention string) {
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			cfg, e := tx.Configuration()
			if e != nil {
				return e
			}
			var v map[string]any
			if e = json.Unmarshal(cfg.Value, &v); e != nil {
				return e
			}
			v["projects"] = map[string]any{project: map[string]any{"sources": []domain.ManagedSourceMapping{{SourceID: "source", Kind: "files", RootPath: root, RetentionMode: retention, ProviderIDs: []string{}}}}}
			cfg.Value, _ = json.Marshal(v)
			return tx.SetConfiguration(cfg, cfg.Revision)
		})).To(Succeed())
	}
	capture := func() domain.SourceCaptureRead {
		r, e := f.core.Capture(context.Background(), f.token, sourceKey(), project, appsource.CaptureRequest{SourceMode: "current", Selections: []appsource.CaptureSelection{{SourceID: "source", Paths: []appsource.CapturePath{{Path: "file"}}}}})
		Expect(e).NotTo(HaveOccurred())
		var result struct {
			ID string `json:"capture_id"`
		}
		Expect(json.Unmarshal(r.Body, &result)).To(Succeed())
		view, e := f.core.CaptureView(context.Background(), f.token, result.ID, "")
		Expect(e).NotTo(HaveOccurred())
		return view["items"].([]domain.SourceCaptureRead)[0]
	}
	read := func(id string) (int, string) {
		req := httptest.NewRequest("GET", "/api/v1/source-reads/"+id, nil)
		req.Header.Set("Authorization", "Bearer "+f.token)
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)
		Expect(rec.Body.Bytes()).NotTo(Equal(body))
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())
		return rec.Code, envelope.Error.Code
	}
	configure("reference_only")
	reference := capture()
	Expect(os.WriteFile(filepath.Join(root, "file"), []byte("changed bytes"), 0600)).To(Succeed())
	status, code := read(reference.ID)
	Expect(status).To(Equal(409))
	Expect(code).To(Equal("SOURCE_UNAVAILABLE"))
	Expect(os.Remove(filepath.Join(root, "file"))).To(Succeed())
	status, code = read(reference.ID)
	Expect(status).To(Equal(409))
	Expect(code).To(Equal("SOURCE_UNAVAILABLE"))
	configure("selected_snapshot")
	Expect(os.WriteFile(filepath.Join(root, "file"), body, 0600)).To(Succeed())
	retained := capture()
	Expect(os.WriteFile(filepath.Join(f.store.CopyProfile().DataDir, "storage", "objects", retained.Hash), []byte(strings.Repeat("x", len(body))), 0600)).To(Succeed())
	status, code = read(retained.ID)
	Expect(status).To(Equal(503))
	Expect(code).To(Equal("CONTENT_INTEGRITY_ERROR"))
})

func sourceKey() string {
	GinkgoHelper()
	id, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	return id
}

var _ = It("SCAP41 public omitted modes use actual remembered defaults and explicit pinned keeps the old exact bytes", Label("persistence"), func() {
	f := newHTTPFixture(GinkgoT())
	h, e := f.store.Health(context.Background())
	Expect(e).NotTo(HaveOccurred())
	project := h.Library.UnfiledProjectID
	root := GinkgoT().TempDir()
	id := "mode-source"
	f.core.Files = sourcefiles.Files{}
	configure := func(mode string) {
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			cfg, e := tx.Configuration()
			if e != nil {
				return e
			}
			var value map[string]any
			if e = json.Unmarshal(cfg.Value, &value); e != nil {
				return e
			}
			settings := map[string]any{"sources": []map[string]any{{"source_id": id, "kind": "files", "root_path": root, "retention_mode": "selected_snapshot", "provider_ids": []string{}}}}
			if mode != "" {
				settings["source_mode_default"] = mode
			}
			value["projects"] = map[string]any{project: settings}
			cfg.Value, _ = json.Marshal(value)
			return tx.SetConfiguration(cfg, cfg.Revision)
		})).To(Succeed())
	}
	post := func(request appsource.CaptureRequest) (string, domain.SourceCaptureRead, string) {
		raw, e := json.Marshal(request)
		Expect(e).NotTo(HaveOccurred())
		operation, e := service.NewID()
		Expect(e).NotTo(HaveOccurred())
		response := f.post("/api/v1/projects/"+project+"/source-captures", f.token, operation, string(raw))
		Expect(response.Code).To(Equal(201))
		var data struct {
			Data struct {
				ID        string                   `json:"capture_id"`
				Complete  bool                     `json:"complete"`
				Selection appsource.CaptureRequest `json:"requested_selection"`
			} `json:"data"`
		}
		Expect(json.Unmarshal(response.Body.Bytes(), &data)).To(Succeed())
		Expect(data.Data.Complete).To(BeTrue())
		view, e := f.core.CaptureView(context.Background(), f.token, data.Data.ID, "")
		Expect(e).NotTo(HaveOccurred())
		return data.Data.ID, view["items"].([]domain.SourceCaptureRead)[0], data.Data.Selection.SourceMode
	}
	configure("current")
	Expect(os.WriteFile(filepath.Join(root, "one"), []byte("pinned original"), 0600)).To(Succeed())
	request := appsource.CaptureRequest{Selections: []appsource.CaptureSelection{{SourceID: id, Paths: []appsource.CapturePath{{Path: "one"}}}}}
	_, old, mode := post(request)
	Expect(mode).To(Equal("current"))
	Expect(os.WriteFile(filepath.Join(root, "one"), []byte("current later"), 0600)).To(Succeed())
	request.Selections[0].Paths[0].ReadID = old.ID
	for _, choice := range []string{"current_default", "absent_default", "explicit_pinned", "historical"} {
		configure("current")
		request.SourceMode = ""
		request.SourceIntent = ""
		expected := old.Hash
		expectedMode := "pinned"
		switch choice {
		case "current_default":
			expected = shaOf([]byte("current later"))
			expectedMode = "current"
		case "absent_default":
			configure("")
		case "explicit_pinned":
			request.SourceMode = "pinned"
		case "historical":
			request.SourceIntent = "historical"
		}
		_, read, mode := post(request)
		Expect(read.Hash).To(Equal(expected), choice)
		Expect(mode).To(Equal(expectedMode), choice)
	}
})

var _ = It("T4R1F5HTTP pinned files chooser enumerates existing capture metadata and submits exact retained path/read identity through strict HTTP", Label("persistence"), func() {
	f := newHTTPFixture(GinkgoT())
	ctx := context.Background()
	health, e := f.store.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	project := health.Library.UnfiledProjectID
	root := GinkgoT().TempDir()
	Expect(os.WriteFile(filepath.Join(root, "one.txt"), []byte("historical A"), 0600)).To(Succeed())
	f.core.Files = sourcefiles.Files{}
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var v map[string]any
		if e = json.Unmarshal(cfg.Value, &v); e != nil {
			return e
		}
		v["projects"] = map[string]any{project: map[string]any{"sources": []domain.ManagedSourceMapping{{SourceID: "files", Kind: "files", RootPath: root, RetentionMode: "selected_snapshot", ProviderIDs: []string{}}}}}
		cfg.Value, _ = json.Marshal(v)
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
	key, _ := service.NewID()
	created := f.post("/api/v1/projects/"+project+"/source-captures", f.token, key, `{"source_mode":"current","selections":[{"source_id":"files","paths":[{"path":"one.txt"}]}]}`)
	Expect(created.Code).To(Equal(201), created.Body.String())
	request := func(method, path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	route := "/api/v1/projects/" + project + "/source-reads?source_id=files"
	listed := request("GET", route, f.token)
	Expect(listed.Code).To(Equal(200), listed.Body.String())
	var page struct {
		Data struct {
			Items []struct {
				domain.SourceCaptureRead
				CapturedAt string `json:"captured_at"`
			} `json:"items"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(listed.Body.Bytes(), &page)).To(Succeed())
	Expect(page.Data.Items).To(HaveLen(1))
	read := page.Data.Items[0]
	var originalCapture domain.SourceCapture
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		var e error
		originalCapture, e = tx.SourceCapture(read.CaptureID)
		return e
	})).To(Succeed())
	Expect(read.CapturedAt).To(Equal(originalCapture.CapturedAt))
	Expect(read.CapturedAt).NotTo(BeEmpty())

	Expect(read.ReadState).To(Equal("not_read_by_view"))
	Expect(read.BytesOrigin).To(Equal("retained_snapshot"))
	Expect(read.Availability).To(Equal("available"))
	head := request("HEAD", route, f.token)
	Expect(head.Code).To(Equal(200))
	Expect(head.Header().Get("Content-Type")).To(Equal(listed.Header().Get("Content-Type")))
	Expect(head.Body.Len()).To(BeZero())
	Expect(request("GET", route, "").Code).To(Equal(401))
	Expect(request("GET", route+"&source_id=files", f.token).Code).To(Equal(400))
	Expect(os.WriteFile(filepath.Join(root, "one.txt"), []byte("current B"), 0600)).To(Succeed())
	key, _ = service.NewID()
	pin := f.post("/api/v1/projects/"+project+"/source-captures", f.token, key, `{"source_mode":"pinned","source_intent":"historical","selections":[{"source_id":"files","paths":[{"path":"one.txt","read_id":"`+read.ID+`"}]}]}`)
	Expect(pin.Code).To(Equal(201), pin.Body.String())
	var pinned struct {
		Data struct {
			ID       string `json:"capture_id"`
			State    string `json:"state"`
			Complete bool   `json:"complete"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(pin.Body.Bytes(), &pinned)).To(Succeed())
	Expect(pinned.Data.ID).NotTo(Equal(read.CaptureID))
	Expect(pinned.Data.ID).NotTo(BeEmpty())
	Expect(pinned.Data.State).To(Equal("completed"))
	Expect(pinned.Data.Complete).To(BeTrue())
	pinnedReads := request("GET", "/api/v1/source-captures/"+pinned.Data.ID+"/reads", f.token)
	Expect(pinnedReads.Code).To(Equal(200), pinnedReads.Body.String())
	Expect(json.Unmarshal(pinnedReads.Body.Bytes(), &page)).To(Succeed())
	Expect(page.Data.Items).To(HaveLen(1))
	newRead := page.Data.Items[0]
	Expect(newRead.ID).NotTo(Equal(read.ID))
	Expect(newRead.CaptureID).To(Equal(pinned.Data.ID))
	Expect(newRead.SourceID).To(Equal(read.SourceID))
	Expect(newRead.LocationJSON).To(MatchJSON(read.LocationJSON))
	Expect(newRead.Hash).To(Equal(read.Hash))
	Expect(newRead.Availability).To(Equal("available"))
	retained := request("GET", "/api/v1/source-reads/"+newRead.ID, f.token)
	Expect(retained.Code).To(Equal(200))
	Expect(retained.Body.String()).To(Equal("historical A"))
	key, _ = service.NewID()
	wrong := f.post("/api/v1/projects/"+project+"/source-captures", f.token, key, `{"source_mode":"pinned","selections":[{"source_id":"files","paths":[{"path":"different.txt","read_id":"`+read.ID+`"}]}]}`)
	Expect(wrong.Code).NotTo(Equal(201))
	Expect(wrong.Body.String()).NotTo(ContainSubstring("current B"))
})

var _ = It("T4R1F5PAGE retained chooser keyset pages are complete and source/permission-bound without rereading files", Label("persistence"), func() {
	f := newHTTPFixture(GinkgoT())
	authClock := time.Now().UTC().Add(time.Second)
	f.auth.Now = func() time.Time { return authClock }
	ctx := context.Background()
	h, e := f.store.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	project := h.Library.UnfiledProjectID
	root := GinkgoT().TempDir()
	f.core.Files = sourcefiles.Files{}
	paths := []appsource.CapturePath{}
	for i := 0; i < 65; i++ {
		name := fmt.Sprintf("file-%02d", i)
		Expect(os.WriteFile(filepath.Join(root, name), []byte(name), 0600)).To(Succeed())
		paths = append(paths, appsource.CapturePath{Path: name})
	}
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var v map[string]any
		if e = json.Unmarshal(cfg.Value, &v); e != nil {
			return e
		}
		v["projects"] = map[string]any{project: map[string]any{"sources": []domain.ManagedSourceMapping{{SourceID: "files", Kind: "files", RootPath: root, RetentionMode: "selected_snapshot", ProviderIDs: []string{}}, {SourceID: "other", Kind: "files", RootPath: root, RetentionMode: "selected_snapshot", ProviderIDs: []string{}}}}}
		cfg.Value, _ = json.Marshal(v)
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
	key, _ := service.NewID()
	body, _ := json.Marshal(appsource.CaptureRequest{SourceMode: "current", Selections: []appsource.CaptureSelection{{SourceID: "files", Paths: paths}}})
	Expect(f.post("/api/v1/projects/"+project+"/source-captures", f.token, key, string(body)).Code).To(Equal(201))
	get := func(route string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", route, nil)
		r.Header.Set("Authorization", "Bearer "+f.token)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	route := "/api/v1/projects/" + project + "/source-reads?source_id=files"
	first := get(route)
	Expect(first.Code).To(Equal(200))
	var page struct {
		Data struct {
			Items []struct {
				domain.SourceCaptureRead
				CapturedAt string `json:"captured_at"`
			} `json:"items"`
			Next *string `json:"next_cursor"`
		} `json:"data"`
	}
	Expect(json.Unmarshal(first.Body.Bytes(), &page)).To(Succeed())
	Expect(page.Data.Items).To(HaveLen(64))
	Expect(page.Data.Next).NotTo(BeNil())
	cursor := *page.Data.Next
	seen := map[string]bool{}
	for _, read := range page.Data.Items {
		seen[read.ID] = true
	}
	// Read the owner revision without touching it, then force a real HTTP
	// LastSeen update on the next page. Activity alone is not changed authority.
	sessionRevision := func() int64 {
		var actor domain.Session
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			var err error
			actor, err = f.auth.AuthenticateWrite(ctx, tx, f.token)
			return err
		})).To(Succeed())
		return actor.Revision
	}
	beforeTouch := sessionRevision()
	authClock = authClock.Add(time.Second)
	second := get(route + "&cursor=" + url.QueryEscape(cursor))
	Expect(second.Code).To(Equal(200), second.Body.String())
	Expect(sessionRevision()).To(BeNumerically(">", beforeTouch))
	Expect(json.Unmarshal(second.Body.Bytes(), &page)).To(Succeed())
	Expect(page.Data.Items).To(HaveLen(1))
	Expect(page.Data.Next).To(BeNil())
	Expect(seen[page.Data.Items[0].ID]).To(BeFalse())
	Expect(get("/api/v1/projects/" + project + "/source-reads?source_id=other&cursor=" + url.QueryEscape(cursor)).Code).To(Equal(400))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
	Expect(get(route+"&cursor="+url.QueryEscape(cursor)).Code).To(Equal(400), "current configuration invalidates the old cursor")
	fresh := get(route)
	Expect(fresh.Code).To(Equal(200))
	Expect(json.Unmarshal(fresh.Body.Bytes(), &page)).To(Succeed())
	Expect(page.Data.Next).NotTo(BeNil())
	permissionCursor := *page.Data.Next
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		scope, err := tx.Scope(project)
		if err != nil {
			return err
		}
		return tx.ApplyProjectConfiguration(project, "", "", true, scope.Project.PermissionsRevision)
	})).To(Succeed())
	Expect(get(route+"&cursor="+url.QueryEscape(permissionCursor)).Code).To(Equal(400), "a permission revision change independently invalidates the old cursor")
	_, err := f.auth.Revoke(ctx, f.token, "")
	Expect(err).NotTo(HaveOccurred())
	revoked := get(route + "&cursor=" + url.QueryEscape(permissionCursor))
	Expect(revoked.Code).To(Equal(401))
	Expect(revoked.Body.String()).To(ContainSubstring("SESSION_REVOKED"))
	Expect(revoked.Body.String()).NotTo(ContainSubstring("file-"))
})
