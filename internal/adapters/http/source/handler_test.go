package source_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	httpsource "haeryong.local/haeryong/internal/adapters/http/source"
	v1 "haeryong.local/haeryong/internal/adapters/http/v1"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	appsource "haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

type httpFixture struct {
	core       *appsource.Service
	auth       *appauth.Service
	handler    http.Handler
	token      string
	scoped     string
	store      *foundation.Store
	project    string
	version    string
	coreReader *httpReader
}

func newHTTPFixture(t GinkgoTInterface) *httpFixture {
	GinkgoHelper()
	selection := service.Selection{Profile: "test", DataDir: t.TempDir() + "/library"}
	_, e := service.Initialize(selection)
	Expect(e).NotTo(HaveOccurred())
	w, e := service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { w.Close() })
	store := foundation.New(nil)
	state, closeStore, e := store.Prepare(context.Background(), w.Profile)
	Expect(e).NotTo(HaveOccurred())
	Expect(state.Ready).To(BeTrue())
	DeferCleanup(func() { _ = closeStore() })
	Expect(w.MarkStorageReady(nil)).To(Succeed())
	auth := appauth.New(store)
	auth.Now = func() time.Time { return time.Now() }
	pairing, e := auth.CreatePairing(context.Background(), "http://127.0.0.1:9270", "owner", "client")
	Expect(e).NotTo(HaveOccurred())
	op, _ := service.NewID()
	_, e = auth.BootstrapDecide(context.Background(), domain.PairingDecision{ID: pairing.ID, Action: "approve", Code: pairing.Code, Origin: pairing.Origin, ETag: pairing.ETag, OperationID: op})
	Expect(e).NotTo(HaveOccurred())
	redeemed, e := auth.Poll(context.Background(), pairing.ID, pairing.PollingSecret, pairing.Origin)
	Expect(e).NotTo(HaveOccurred())
	reader := &httpReader{blobs: map[string]string{}, heads: map[string]string{}}
	core := appsource.New(store, auth, store, reader)
	queries := query.New(store, auth, store)
	posts := httpsource.New(core)
	reads := v1.New(queries, nil, nil)
	composite := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reads.Handles(r) {
			reads.ServeHTTP(w, r)
			return
		}
		posts.ServeHTTP(w, r)
	})
	return &httpFixture{core: core, auth: auth, handler: composite, token: redeemed.Token, store: store, coreReader: reader}
}

// httpReader is the in-memory reader for HTTP-level fixtures.
type httpReader struct {
	blobs map[string]string
	heads map[string]string
}

func (r *httpReader) ResolveRevision(ctx context.Context, root, revision string) error { return nil }
func (r *httpReader) ResolveHead(ctx context.Context, root string) (string, error) {
	if revision, ok := r.heads[root]; ok {
		return revision, nil
	}
	return "", appsource.ErrInvalidRoot
}
func (r *httpReader) ChangedPaths(ctx context.Context, root, base, tip string, cited []string) ([]string, error) {
	return nil, nil
}
func (r *httpReader) ReadBlob(ctx context.Context, root, revision, path string) (io.ReadCloser, error) {
	content, ok := r.blobs[revision+"\x00"+path]
	if !ok {
		return nil, appsource.ErrObjectMissing
	}
	return io.NopCloser(strings.NewReader(content)), nil
}

func (f *httpFixture) post(path, token, key, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

// buildEvidenceVersion publishes a version with one evidence row per id.
func (f *httpFixture) buildEvidenceVersion(t GinkgoTInterface, evidenceIDs []string, revision string) {
	t.Helper()
	ctx := context.Background()
	f.project, f.version = "11111111-1111-4111-8111-111111111113", "13131313-1313-4131-8131-131313131314"
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.CreateProject(domain.Project{ID: f.project, Name: "P"}); e != nil {
			return e
		}
		if e := tx.CreateDocument(domain.Document{ID: "12121212-1212-4121-8121-121212121213", ProjectID: f.project, Title: "Doc"}); e != nil {
			return e
		}
		if e := tx.CreateContentSet("14141414-1414-4141-8141-141414141415", shaOf(nil)); e != nil {
			return e
		}
		if e := tx.SealContentSet("14141414-1414-4141-8141-141414141415"); e != nil {
			return e
		}
		if e := tx.PutVersion(domain.Version{ID: f.version, DocumentID: "12121212-1212-4121-8121-121212121213", ContentSetID: "14141414-1414-4141-8141-141414141415", Title: "Doc", ViewerProfile: "html_js_v1", SourceBasis: "evidence_recorded", PayloadSHA: shaOf(nil), ManifestSHA: shaOf(nil)}); e != nil {
			return e
		}
		sourceMeta := `{"id":"src","kind":"git","retention_mode":"reference_only","repo_id":"77777777-7777-4777-8777-777777777777","object_format":"sha1","exact_revision":"` + revision + `"}`
		if e := tx.PutSource(domain.Source{VersionID: f.version, ID: "src", Metadata: json.RawMessage(sourceMeta)}); e != nil {
			return e
		}
		for _, id := range evidenceIDs {
			if e := tx.PutEvidence(domain.Evidence{VersionID: f.version, ID: id, SourceID: "src", Metadata: json.RawMessage(`{"id":"` + idEscape(id) + `","source_id":"src","capture_status":"reference_only","path":"f.txt"}`)}); e != nil {
				return e
			}
		}
		return tx.PublishVersion(f.version, 1)
	})).To(Succeed())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		current, e := tx.Configuration()
		if e != nil {
			return e
		}
		var config map[string]any
		Expect(json.Unmarshal(current.Value, &config)).NotTo(HaveOccurred())
		config["projects"] = map[string]any{f.project: map[string]any{"sources": []any{json.RawMessage(`{"source_id":"git-1","kind":"git","root_path":"/repo","repo_id":"77777777-7777-4777-8777-777777777777","retention_mode":"reference_only","provider_ids":[]}`)}}}
		raw, _ := json.Marshal(config)
		return tx.SetConfiguration(domain.Configuration{Revision: current.Revision, Value: raw, Actor: "fixture"}, current.Revision)
	})).To(Succeed())
}

func idEscape(id string) string {
	raw, _ := json.Marshal(id)
	return strings.Trim(string(raw), `"`)
}

func shaOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func decode(t GinkgoTInterface, body []byte) map[string]any {
	t.Helper()
	var value map[string]any
	ExpectWithOffset(1, json.Unmarshal(body, &value)).NotTo(HaveOccurred())
	return value
}

var httpRevision = strings.Repeat("a1b2c3d4", 5)

var _ = Describe("source HTTP adapter", Label("persistence"), func() {
	t := GinkgoT()

	It("registers projects over HTTP with replay and invalid-name rejection (C1)", func() {
		f := newHTTPFixture(t)
		key := "a0000000-0000-4000-8000-000000000001"
		recorder := f.post("/api/v1/projects", f.token, key, `{"name":"HTTP project"}`)
		Expect(recorder.Code).To(Equal(201))
		body := decode(t, recorder.Body.Bytes())["data"].(map[string]any)
		Expect(body["name"]).To(Equal("HTTP project"))
		Expect(body["etag"]).NotTo(BeEmpty())
		replay := f.post("/api/v1/projects", f.token, key, `{"name":"HTTP project"}`)
		Expect(replay.Code).To(Equal(201))
		// request_id differs per response; the recorded result replays.
		Expect(decode(t, replay.Body.Bytes())["data"]).To(Equal(decode(t, recorder.Body.Bytes())["data"]))
		bad := f.post("/api/v1/projects", f.token, "a0000000-0000-4000-8000-000000000002", `{"name":"  "}`)
		Expect(bad.Code).To(Equal(400))
		Expect(decode(t, bad.Body.Bytes())["error"]).NotTo(BeNil())
	})

	It("dispatches both POST forms and preserves opaque evidence IDs through the query form (C2)", func() {
		f := newHTTPFixture(t)
		f.coreReader.blobs[httpRevision+"\x00f.txt"] = "http blob\n"
		ids := []string{".", "..", "%2e", "evidence/상세", "a +", "x?y#z", "verifications", "simple"}
		f.buildEvidenceVersion(t, ids, httpRevision)
		for i, id := range ids {
			key := "a0000000-0000-4000-8000-00000000001" + string(rune('0'+i))
			recorder := f.post("/api/v1/versions/"+f.version+"/source-evidence-verifications?evidence_id="+urlQueryEscape(id), f.token, key, `{}`)
			ExpectWithOffset(1, recorder.Code).To(Equal(201), func() string { return id + ": " + recorder.Body.String() })
			data := decode(t, recorder.Body.Bytes())["data"].(map[string]any)
			ExpectWithOffset(1, data["evidence_id"]).To(Equal(id))
			ExpectWithOffset(1, data["outcome"]).To(Equal("captured"))
		}
		// The catalog path form serves the simple id exactly once decoded.
		recorder := f.post("/api/v1/versions/"+f.version+"/source-evidence/simple/verifications", f.token, "a0000000-0000-4000-8000-000000000090", `{}`)
		Expect(recorder.Code).To(Equal(201))
		Expect(decode(t, recorder.Body.Bytes())["data"].(map[string]any)["evidence_id"]).To(Equal("simple"))
	})

	It("rejects unknown query parameters like the GET alias (C2)", func() {
		f := newHTTPFixture(t)
		f.coreReader.blobs[httpRevision+"\x00f.txt"] = "x\n"
		f.buildEvidenceVersion(t, []string{"simple"}, httpRevision)
		recorder := f.post("/api/v1/versions/"+f.version+"/source-evidence-verifications?evidence_id=simple&unexpected=1", f.token, "a0000000-0000-4000-8000-000000000091", `{}`)
		Expect(recorder.Code).To(Equal(400))
		Expect(decode(t, recorder.Body.Bytes())["error"].(map[string]any)["code"]).To(Equal("INVALID_REQUEST"))
	})

	It("answers 428 with details.missing for a missing Idempotency-Key (C11)", func() {
		f := newHTTPFixture(t)
		recorder := f.post("/api/v1/projects", f.token, "", `{"name":"No key"}`)
		Expect(recorder.Code).To(Equal(428))
		envelope := decode(t, recorder.Body.Bytes())
		errorBody := envelope["error"].(map[string]any)
		Expect(errorBody["code"]).To(Equal("PRECONDITION_REQUIRED"))
		Expect(errorBody["details"].(map[string]any)["missing"]).To(Equal("Idempotency-Key"))
	})

	It("serves settled failures and their replays as the top-level error envelope (C11)", func() {
		f := newHTTPFixture(t)
		f.buildEvidenceVersion(t, []string{"simple"}, httpRevision)
		key := "a0000000-0000-4000-8000-000000000092"
		f.core.Fault = func(stage string) error {
			if stage == "before_commit" {
				return errInjected
			}
			return nil
		}
		recorder := f.post("/api/v1/versions/"+f.version+"/source-evidence-verifications?evidence_id=simple", f.token, key, `{}`)
		Expect(recorder.Code).To(Equal(503))
		envelope := decode(t, recorder.Body.Bytes())
		Expect(envelope["error"]).NotTo(BeNil(), "failures are never wrapped under data")
		Expect(envelope["data"]).To(BeNil())
		f.core.Fault = nil
		replay := f.post("/api/v1/versions/"+f.version+"/source-evidence-verifications?evidence_id=simple", f.token, key, `{}`)
		Expect(replay.Code).To(Equal(503))
		Expect(replay.Body.Bytes()).To(Equal(recorder.Body.Bytes()))
	})

	It("enforces the scoped verification rule over HTTP (C2)", func() {
		f := newHTTPFixture(t)
		f.coreReader.blobs[httpRevision+"\x00f.txt"] = "scoped\n"
		f.buildEvidenceVersion(t, []string{"simple"}, httpRevision)
		// Seed an in-scope credential with version.read+source.read.
		scopedToken := f.seedScoped(t, true, true)
		recorder := f.post("/api/v1/versions/"+f.version+"/source-evidence-verifications?evidence_id=simple", scopedToken, "a0000000-0000-4000-8000-000000000093", `{}`)
		Expect(recorder.Code).To(Equal(201), recorder.Body.String())
		// A foreign version is nondisclosing.
		recorder = f.post("/api/v1/versions/99999999-9999-4999-8999-999999999999/source-evidence-verifications?evidence_id=simple", scopedToken, "a0000000-0000-4000-8000-000000000094", `{}`)
		Expect(recorder.Code).To(Equal(404))
		// Missing source.read fails closed.
		noSource := f.seedScoped(t, true, false)
		recorder = f.post("/api/v1/versions/"+f.version+"/source-evidence-verifications?evidence_id=simple", noSource, "a0000000-0000-4000-8000-000000000095", `{}`)
		Expect(recorder.Code).To(Equal(403))
	})
})

// seedScoped creates a scoped workflow credential anchored to the fixture
// project and version.
func (f *httpFixture) seedScoped(t GinkgoTInterface, withVersionRead, withSourceRead bool) string {
	t.Helper()
	token, e := sec.Secret()
	ExpectWithOffset(1, e).NotTo(HaveOccurred())
	principal := "b1000000-0000-4000-8000-0000000000" + map[bool]string{true: "01", false: "02"}[withSourceRead]
	actions := []string{}
	if withVersionRead {
		actions = append(actions, "version.read", "project.read")
	}
	if withSourceRead {
		actions = append(actions, "source.read")
	}
	scope := domain.CredentialScope{Kind: "workflow", Actions: actions, ProjectIDs: []string{f.project}, VersionIDs: []string{f.version}}
	ExpectWithOffset(1, f.store.Write(context.Background(), func(tx app.WriteTx) error {
		state, e := tx.AuthState()
		if e != nil {
			return e
		}
		if e := tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: principal, Active: true, Scope: scope, Revision: 1}); e != nil {
			return e
		}
		// This fixture performs fresh capture, so retain both issuer-selected
		// output authority and its current remembered project restriction.
		if e := tx.PutWorkflowSelection(domain.WorkflowSelection{BindingID: principal, PrincipalID: principal, ProjectID: f.project, ProviderID: "codex", SourceSelection: []byte(`{}`)}, 0); e != nil {
			return e
		}
		project, e := tx.Scope(f.project)
		if e != nil {
			return e
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: principal, PrincipalID: principal, TokenHash: sec.Verifier("workflow", token), AuthEpoch: state.AuthEpoch, Scope: scope, MutationProjectID: f.project, Permissions: map[string]int64{f.project: project.Project.PermissionsRevision}, ConfigRevision: project.ConfigRevision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond), Revision: 1})
	})).To(Succeed())
	return token
}

var errInjected = &injectedError{}

type injectedError struct{}

func (*injectedError) Error() string { return "injected before commit" }

func urlQueryEscape(v string) string {
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' || r == '~':
			b.WriteRune(r)
		default:
			for _, byte_ := range []byte(string(r)) {
				const hex = "0123456789ABCDEF"
				b.WriteByte('%')
				b.WriteByte(hex[byte_>>4])
				b.WriteByte(hex[byte_&0xF])
			}
		}
	}
	return b.String()
}

var _ = sec.ErrInvalid

var _ = Describe("source HTTP adapter (W2c)", Label("persistence"), func() {
	t := GinkgoT()

	It("rejects an empty-named query parameter on POST /projects (E4)", func() {
		f := newHTTPFixture(t)
		recorder := f.post("/api/v1/projects?=x", f.token, "b1000000-0000-4000-8000-000000000001", `{"name":"X"}`)
		Expect(recorder.Code).To(Equal(400))
	})

	It("rejects null expectation fields with 400 (S28)", func() {
		f := newHTTPFixture(t)
		f.coreReader.blobs[httpRevision+"\x00f.txt"] = "x\n"
		f.buildEvidenceVersion(t, []string{"simple"}, httpRevision)
		recorder := f.post("/api/v1/versions/"+f.version+"/source-evidence-verifications?evidence_id=simple", f.token, "b1000000-0000-4000-8000-000000000002", `{"expected_excerpt":null}`)
		Expect(recorder.Code).To(Equal(400))
	})

	It("hides a foreign upload with 404 before any other check (S22)", func() {
		f := newHTTPFixture(t)
		f.buildEvidenceVersion(t, []string{"simple"}, httpRevision)
		uploadID := "b2000000-0000-4000-8000-000000000001"
		foreign := "b2000000-0000-4000-8000-0000000000ff"
		Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
			return tx.CreateUpload(domain.Upload{ID: uploadID, PrincipalID: foreign, Purpose: "body", InputMode: "object", SelectionName: "expectation", Target: []byte(`{"operation_id":"b1000000-0000-4000-8000-000000000003","action":"evidence_verification","version_id":"` + f.version + `","evidence_id":"simple"}`), Checkpoint: domain.Checkpoint{Generation: 1, InventorySHA256: app.EmptyInventorySHA256, AcceptedBytes: "0"}, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z"})
		})).To(Succeed())
		// The scoped reader is not the upload owner: 404, nondisclosing.
		scoped := f.seedScoped(t, true, true)
		recorder := f.post("/api/v1/versions/"+f.version+"/source-evidence-verifications?evidence_id=simple", scoped, "b1000000-0000-4000-8000-000000000003", `{"expected_excerpt_ref":{"upload_id":"`+uploadID+`"}}`)
		Expect(recorder.Code).To(Equal(404))
		// G8: the owner passes the hiding gate for that foreign upload; it
		// is not sealed yet, so it is not adoptable: 409 INVALID_TRANSITION
		// (the sealed adoption itself is covered in the source package).
		ownerRun := f.post("/api/v1/versions/"+f.version+"/source-evidence-verifications?evidence_id=simple", f.token, "b1000000-0000-4000-8000-000000000003", `{"expected_excerpt_ref":{"upload_id":"`+uploadID+`"}}`)
		Expect(ownerRun.Code).To(Equal(409), ownerRun.Body.String())
		Expect(decode(t, ownerRun.Body.Bytes())["error"].(map[string]any)["code"]).To(Equal("INVALID_TRANSITION"))
	})
})

func ownerPrincipalOf(f *httpFixture) string {
	record, e := f.auth.Authenticate(context.Background(), f.token)
	ExpectWithOffset(1, e).NotTo(HaveOccurred())
	return record.PrincipalID
}

var _ = Describe("verification history paging across both forms (G12)", Label("persistence"), func() {
	t := GinkgoT()
	It("pages through the query alias and the path form with cursor and limit", func() {
		f := newHTTPFixture(t)
		f.coreReader.blobs[httpRevision+"\x00f.txt"] = "x\n"
		f.buildEvidenceVersion(t, []string{"ev.1"}, httpRevision)
		// Three checks create three history rows.
		for i := 0; i < 3; i++ {
			key := "b3000000-0000-4000-8000-00000000000" + string(rune('0'+i))
			recorder := f.post("/api/v1/versions/"+f.version+"/source-evidence-verifications?evidence_id="+urlQueryEscape("ev.1"), f.token, key, `{}`)
			Expect(recorder.Code).To(Equal(201), recorder.Body.String())
		}
		// Page 1 through the canonical query alias.
		first := f.get(t, "/api/v1/versions/"+f.version+"/source-evidence-verifications?evidence_id="+urlQueryEscape("ev.1")+"&limit=2")
		Expect(first.Code).To(Equal(200))
		page1 := decode(t, first.Body.Bytes())["data"].(map[string]any)
		Expect(page1["items"]).To(HaveLen(2))
		cursor, ok := page1["next_cursor"].(string)
		Expect(ok).To(BeTrue())
		// Page 2 through the catalogued path form with the same cursor.
		second := f.get(t, "/api/v1/versions/"+f.version+"/source-evidence/ev.1/verifications?limit=2&cursor="+urlQueryEscape(cursor))
		Expect(second.Code).To(Equal(200), second.Body.String())
		page2 := decode(t, second.Body.Bytes())["data"].(map[string]any)
		Expect(page2["items"]).To(HaveLen(1))
		Expect(page2["next_cursor"]).To(BeNil())
	})
})

func (f *httpFixture) get(t GinkgoTInterface, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+f.token)
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

var _ = Describe("observation HTTP adapter", Label("persistence"), func() {
	t := GinkgoT()

	It("runs the owner-only observation POST with a durable receipt (M3)", func() {
		f := newHTTPFixture(t)
		f.coreReader.heads["/repo"] = httpRevision
		f.buildEvidenceVersion(t, []string{"simple"}, httpRevision)
		key := "b4000000-0000-4000-8000-000000000001"
		recorder := f.post("/api/v1/versions/"+f.version+"/source-observations", f.token, key, `{}`)
		Expect(recorder.Code).To(Equal(201), recorder.Body.String())
		body := decode(t, recorder.Body.Bytes())["data"].(map[string]any)
		Expect(body["run_id"]).NotTo(BeEmpty())
		Expect(body["target_set_id"]).NotTo(BeNil())
		Expect(body["sources"].([]any)).To(HaveLen(1))
		source := body["sources"].([]any)[0].(map[string]any)
		Expect(source["classification"]).To(Equal("unchanged"))
		Expect(source["availability"]).To(Equal("available"))
		// The receipt replays byte-for-byte.
		replay := f.post("/api/v1/versions/"+f.version+"/source-observations", f.token, key, `{}`)
		Expect(replay.Code).To(Equal(201))
		Expect(decode(t, replay.Body.Bytes())["data"]).To(Equal(body))
		// The scoped reader results stay readable; the POST itself is
		// owner-only (O1).
		scoped := f.seedScoped(t, true, true)
		denied := f.post("/api/v1/versions/"+f.version+"/source-observations", scoped, "b4000000-0000-4000-8000-000000000002", `{}`)
		Expect(denied.Code).To(Equal(403))
	})

	It("rejects malformed bodies and unknown query parameters (M3)", func() {
		f := newHTTPFixture(t)
		f.coreReader.heads["/repo"] = httpRevision
		f.buildEvidenceVersion(t, []string{"simple"}, httpRevision)
		unknown := f.post("/api/v1/versions/"+f.version+"/source-observations?unexpected=1", f.token, "b4000000-0000-4000-8000-000000000003", `{}`)
		Expect(unknown.Code).To(Equal(400))
		badBody := f.post("/api/v1/versions/"+f.version+"/source-observations", f.token, "b4000000-0000-4000-8000-000000000004", `{"nope":1}`)
		Expect(badBody.Code).To(Equal(400))
		badScope := f.post("/api/v1/versions/"+f.version+"/source-observations", f.token, "b4000000-0000-4000-8000-000000000005", `{"scope":{"kind":"region","region_id":"R"}}`)
		Expect(badScope.Code).To(Equal(400), "a region scope requires its page")
		unknownSource := f.post("/api/v1/versions/"+f.version+"/source-observations", f.token, "b4000000-0000-4000-8000-000000000006", `{"source_ids":["git-2"]}`)
		Expect(unknownSource.Code).To(Equal(400), "a requested source outside the effective set is rejected")
	})
})
