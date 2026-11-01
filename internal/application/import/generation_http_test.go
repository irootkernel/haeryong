package importer_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/ai/codex"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	httpimports "haeryong.local/haeryong/internal/adapters/http/imports"
	appauth "haeryong.local/haeryong/internal/application/auth"
	generation "haeryong.local/haeryong/internal/application/generation"
	importer "haeryong.local/haeryong/internal/application/import"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"haeryong.local/haeryong/skills"
)

func generationHTTP(f *fixture) http.Handler {
	path, owned, e := skills.Materialize(GinkgoT().TempDir())
	Expect(e).NotTo(HaveOccurred())
	gen := generation.New(f.store, f.auth, nil, domain.GenerationInvocation{SkillPath: path, SkillSHA: owned.SHA256, SkillVersion: owned.Version}, codex.SemanticRequestSHA)
	handler, e := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{"127.0.0.1:9270"}, Origins: []string{"http://127.0.0.1:9270"}}, httpimports.NewGeneration(f.core, gen))
	Expect(e).NotTo(HaveOccurred())
	return handler
}
func generationHTTPRequest(handler http.Handler, f *fixture, method, path, key, etag string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:9270"+path, bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+f.token)
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	if etag != "" {
		r.Header.Set("If-Match", etag)
	}
	if len(body) > 0 {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

var _ = It("GHTTPGEN01 authenticated public generation/draft routes retain explicit-null intent and reject closed-shape and conditional-write violations", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	handler := generationHTTP(f)
	request := map[string]any{"project_id": f.project, "document_id": nil, "intent": "draft", "provider_id": "codex", "prompt": "Create a complete useful explanation", "source_selection": map[string]any{"selections": []any{}}}
	body, _ := json.Marshal(request)
	key := key(GinkgoT())
	r := generationHTTPRequest(handler, f, "POST", "/api/v1/generations", key, "", body)
	Expect(r.Code).To(Equal(202), r.Body.String())
	var accepted struct {
		Data struct {
			Job   string `json:"job_id"`
			Draft string `json:"draft_id"`
		}
	}
	Expect(json.Unmarshal(r.Body.Bytes(), &accepted)).To(Succeed())
	Expect(accepted.Data.Job).NotTo(BeEmpty())
	replay := generationHTTPRequest(handler, f, "POST", "/api/v1/generations", key, "", body)
	Expect(replay.Code).To(Equal(202))
	var replayed struct {
		Data struct {
			Job   string `json:"job_id"`
			Draft string `json:"draft_id"`
		}
	}
	Expect(json.Unmarshal(replay.Body.Bytes(), &replayed)).To(Succeed())
	Expect(replayed.Data.Job).To(Equal(accepted.Data.Job))
	Expect(replayed.Data.Draft).To(Equal(accepted.Data.Draft))
	get := generationHTTPRequest(handler, f, "GET", "/api/v1/drafts/"+accepted.Data.Draft, "", "", nil)
	Expect(get.Code).To(Equal(200), get.Body.String())
	Expect(get.Header().Get("ETag")).NotTo(BeEmpty())
	head := generationHTTPRequest(handler, f, "HEAD", "/api/v1/drafts/"+accepted.Data.Draft, "", "", nil)
	Expect(head.Code).To(Equal(200))
	Expect(head.Body.Len()).To(BeZero())
	Expect(head.Header().Get("ETag")).To(Equal(get.Header().Get("ETag")))
	rejected := generationHTTPRequest(handler, f, "PATCH", "/api/v1/drafts/"+accepted.Data.Draft, keyUUID(), "", []byte(`{"action":"reject"}`))
	Expect(rejected.Code).To(Equal(428), rejected.Body.String())
	rejected = generationHTTPRequest(handler, f, "PATCH", "/api/v1/drafts/"+accepted.Data.Draft, keyUUID(), get.Header().Get("ETag"), []byte(`{"action":"reject","upload_id":""}`))
	Expect(rejected.Code).To(Equal(400), rejected.Body.String())
	rejected = generationHTTPRequest(handler, f, "PATCH", "/api/v1/drafts/"+accepted.Data.Draft, keyUUID(), get.Header().Get("ETag"), []byte(`{"action":"reject"}`))
	Expect(rejected.Code).To(Equal(200), rejected.Body.String())
	for _, invalid := range [][]byte{
		[]byte(`{"project_id":"` + f.project + `","intent":"draft","prompt":"x","source_selection":{}}`),
		[]byte(`{"project_id":"` + f.project + `","document_id":null,"intent":"draft","prompt":"x"}`),
		[]byte(`{"project_id":"` + f.project + `","document_id":null,"intent":"draft","prompt":"x","prompt":"y","source_selection":{}}`),
		[]byte(`{"project_id":"` + f.project + `","document_id":null,"intent":"draft","prompt":null,"source_selection":{}}`),
		[]byte(`{"project_id":"` + f.project + `","document_id":null,"intent":"draft","prompt":"x","source_selection":{"provider_id":"codex","provider_id":"other"}}`),
		[]byte(`{"project_id":"` + f.project + `","document_id":null,"intent":"draft","prompt":"x","body_ref":{},"source_selection":{}}`),
		[]byte(`{"project_id":"` + f.project + `","document_id":null,"intent":"draft","prompt":"x","source_selection":{},"unknown":true}`),
	} {
		r := generationHTTPRequest(handler, f, "POST", "/api/v1/generations", keyUUID(), "", invalid)
		Expect(r.Code).To(Equal(400), r.Body.String())
	}
})

func keyUUID() string { return key(GinkgoT()) }

var _ = It("GHTTPGEN02 public draft replacement and explicit publication consume only the current saved whole candidate", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	handler := generationHTTP(f)
	g, driver := readyGeneration(f, nil, nil)
	before, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	upload := draftUpload(f, g.DraftID, nil, "actual public replacement")
	body, _ := json.Marshal(map[string]any{"action": "replace", "upload_id": upload})
	replaced := generationHTTPRequest(handler, f, "PATCH", "/api/v1/drafts/"+g.DraftID, keyUUID(), before.ETag, body)
	Expect(replaced.Code).To(Equal(200), replaced.Body.String())
	var value struct{ Data importer.DraftView }
	Expect(json.Unmarshal(replaced.Body.Bytes(), &value)).To(Succeed())
	Expect(value.Data.State).To(Equal("ready"))
	Expect(value.Data.ETag).NotTo(Equal(before.ETag))
	published := generationHTTPRequest(handler, f, "POST", "/api/v1/drafts/"+g.DraftID+"/publish", keyUUID(), value.Data.ETag, []byte(`{}`))
	Expect(published.Code).To(Equal(200), published.Body.String())
	Expect(json.Unmarshal(published.Body.Bytes(), &value)).To(Succeed())
	Expect(value.Data.State).To(Equal("published"))
	Expect(driver.calls.Load()).To(Equal(int32(1)))
	v, e := f.store.Version(ctx, *value.Data.PublishedVersionID)
	Expect(e).NotTo(HaveOccurred())
	Expect(v.PayloadSHA).To(Equal(*value.Data.PayloadSHA))
	// A late saved coordinator cannot publish its original superseded candidate.
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: f.core.PrepareGeneration, Apply: f.core.ApplyGeneration}
	Expect(p.Process(ctx, g.JobID)).To(MatchError(domain.ErrTransition))
	Expect(driver.calls.Load()).To(Equal(int32(1)))
})
var _ = It("GHTTPGEN03 actual multi-page draft cursor is bound to project and resource while authorized results enumerate beyond one internal batch", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	handler := generationHTTP(f)
	for i := 0; i < 65; i++ {
		generationJob(f, "draft", nil, nil)
	}
	page, e := f.core.DraftPage(ctx, f.token, f.project, "")
	Expect(e).NotTo(HaveOccurred())
	Expect(page.Items).To(HaveLen(64))
	Expect(page.NextCursor).NotTo(BeNil())
	rest, e := f.core.DraftPage(ctx, f.token, f.project, *page.NextCursor)
	Expect(e).NotTo(HaveOccurred())
	Expect(rest.Items).To(HaveLen(1))
	Expect(rest.NextCursor).To(BeNil())
	_, e = f.core.DraftPage(ctx, f.token, "", *page.NextCursor)
	Expect(e).To(HaveOccurred(), "same principal but changed project filter")
	_, e = f.core.DraftHistoryPage(ctx, f.token, page.Items[0].ID, *page.NextCursor)
	Expect(e).To(HaveOccurred(), "another resource cursor is not a history cursor")
	r := generationHTTPRequest(handler, f, "GET", "/api/v1/drafts?project_id="+f.project, "", "", nil)
	Expect(r.Code).To(Equal(200), r.Body.String())
	var response struct {
		Data importer.Page[importer.DraftView]
	}
	Expect(json.Unmarshal(r.Body.Bytes(), &response)).To(Succeed())
	Expect(response.Data.Items).To(HaveLen(64))
	Expect(response.Data.NextCursor).NotTo(BeNil())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.Drafts(f.project, "", 64, domain.CredentialScope{Kind: "owner"})
		Expect(rows).To(HaveLen(64))
		return e
	})).To(Succeed())
})

var _ = It("GHTTPGEN04 owner unfiltered draft cursor binds actual current permissions of every visible project and refuses reuse after a permission change", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	handler := generationHTTP(f)
	for i := 0; i < 64; i++ {
		generationJob(f, "draft", nil, nil)
	}
	unfiltered, e := f.core.DraftPage(ctx, f.token, "", "")
	Expect(e).NotTo(HaveOccurred())
	Expect(unfiltered.NextCursor).NotTo(BeNil())
	filtered, e := f.core.DraftPage(ctx, f.token, f.project, "")
	Expect(e).NotTo(HaveOccurred())
	Expect(filtered.NextCursor).NotTo(BeNil())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		scope, e := tx.Scope(f.project)
		if e != nil {
			return e
		}
		return tx.ApplyProjectConfiguration(f.project, scope.Project.Name, scope.Project.Lifecycle, true, scope.Project.PermissionsRevision)
	})).To(Succeed())
	_, e = f.core.DraftPage(ctx, f.token, "", *unfiltered.NextCursor)
	Expect(e).To(HaveOccurred(), "unfiltered owner scope must still bind actual project permission generation")
	_, e = f.core.DraftPage(ctx, f.token, f.project, *filtered.NextCursor)
	Expect(e).To(HaveOccurred())
	r := generationHTTPRequest(handler, f, "GET", "/api/v1/drafts?cursor="+*unfiltered.NextCursor, "", "", nil)
	Expect(r.Code).To(Equal(400), r.Body.String())
})

var _ = It("GHTTPGEN05 owner-derived generation workflow can follow and cancel only its own current authorized job without gaining draft publication authority", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	handler := generationHTTP(f)
	ownerToken := f.token
	token, e := sec.Secret()
	Expect(e).NotTo(HaveOccurred())
	response, e := f.auth.IssueWorkflowGrant(ctx, ownerToken, keyUUID(), appauth.WorkflowGrantRequest{BindingID: keyUUID(), CredentialID: keyUUID(), TokenVerifier: sec.Verifier("workflow", token), Workflow: "generation", ProjectID: f.project, ProviderID: "codex", SourceSelection: json.RawMessage(`{}`)})
	Expect(e).NotTo(HaveOccurred())
	var grant appauth.WorkflowGrant
	Expect(json.Unmarshal(response.Body, &grant)).To(Succeed())
	Expect(grant.Scope.Actions).To(ContainElements("generation.request", "job.read", "job.cancel"))
	Expect(grant.Scope.DraftIDs).To(BeEmpty())
	body, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": nil, "intent": "draft", "provider_id": "codex", "prompt": "Useful complete directory", "source_selection": map[string]any{"selections": []any{}}})
	f.token = token
	created := generationHTTPRequest(handler, f, "POST", "/api/v1/generations", keyUUID(), "", body)
	Expect(created.Code).To(Equal(202), created.Body.String())
	var accepted struct {
		Data struct {
			Job   string `json:"job_id"`
			Draft string `json:"draft_id"`
		}
	}
	Expect(json.Unmarshal(created.Body.Bytes(), &accepted)).To(Succeed())
	job, e := f.auth.Job(ctx, token, accepted.Data.Job)
	Expect(e).NotTo(HaveOccurred())
	Expect(job.ID).To(Equal(accepted.Data.Job))
	Expect(string(job.Scope)).NotTo(ContainSubstring("invocation"))
	f.token = ownerToken
	other := generationJob(f, "draft", nil, nil)
	f.token = token
	_, e = f.auth.Job(ctx, token, other.JobID)
	Expect(e).To(MatchError(domain.ErrNotFound))
	_, e = f.auth.CancelJob(ctx, token, other.JobID, keyUUID())
	Expect(e).To(MatchError(domain.ErrNotFound))
	denied := generationHTTPRequest(handler, f, "GET", "/api/v1/drafts/"+accepted.Data.Draft, "", "", nil)
	Expect(denied.Code).To(Equal(404), "generation grant issued before draft identity does not grant arbitrary draft access")
	_, e = f.auth.CancelJob(ctx, token, accepted.Data.Job, keyUUID())
	Expect(e).NotTo(HaveOccurred())
	job, e = f.auth.Job(ctx, token, accepted.Data.Job)
	Expect(e).NotTo(HaveOccurred())
	Expect(job.CancelRequested).To(BeTrue())
	// Revocation cannot turn workflow recovery into an owner-authenticated call.
	_, e = f.auth.RevokeWorkflowGrant(ctx, ownerToken, keyUUID(), grant.BindingID)
	Expect(e).NotTo(HaveOccurred())
	_, e = f.auth.Job(ctx, token, accepted.Data.Job)
	Expect(e).To(HaveOccurred())
	f.token = ownerToken
})
var _ = It("GHTTPGEN06 draft HEAD carries the GET representation media type and malformed non-single strong If-Match is invalid rather than stale", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	handler := generationHTTP(f)
	g := generationJob(f, "draft", nil, nil)
	path := "/api/v1/drafts/" + g.DraftID
	get := generationHTTPRequest(handler, f, "GET", path, "", "", nil)
	head := generationHTTPRequest(handler, f, "HEAD", path, "", "", nil)
	Expect(get.Code).To(Equal(200))
	Expect(head.Code).To(Equal(200))
	Expect(head.Body.Len()).To(BeZero())
	Expect(head.Header().Get("Content-Type")).To(Equal(get.Header().Get("Content-Type")))
	Expect(head.Header().Get("ETag")).To(Equal(get.Header().Get("ETag")))
	for _, etag := range []string{"unquoted", "W/" + get.Header().Get("ETag"), get.Header().Get("ETag") + ", " + get.Header().Get("ETag"), "*"} {
		r := generationHTTPRequest(handler, f, "PATCH", path, keyUUID(), etag, []byte(`{"action":"reject"}`))
		Expect(r.Code).To(Equal(400), r.Body.String())
	}
	r := httptest.NewRequest("PATCH", "http://127.0.0.1:9270"+path, bytes.NewReader([]byte(`{"action":"reject"}`)))
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", keyUUID())
	r.Header.Add("If-Match", get.Header().Get("ETag"))
	r.Header.Add("If-Match", get.Header().Get("ETag"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	Expect(w.Code).To(Equal(400), w.Body.String())
	stale := generationHTTPRequest(handler, f, "PATCH", path, keyUUID(), `"draft-0000000000000000000000000000000000000000000000000000000000000000"`, []byte(`{"action":"reject"}`))
	Expect(stale.Code).To(Equal(412), stale.Body.String())
	current, e := f.core.Draft(ctx, f.token, g.DraftID)
	Expect(e).NotTo(HaveOccurred())
	Expect(current.State).To(Equal("preparing"))
	Expect(current.ETag).To(Equal(get.Header().Get("ETag")))
})
