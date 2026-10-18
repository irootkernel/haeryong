package importer_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	httpimports "haeryong.local/haeryong/internal/adapters/http/imports"
	importer "haeryong.local/haeryong/internal/application/import"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	_ "modernc.org/sqlite"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (f *fixture) stageContext(t GinkgoTInterface, version, content string, page, render *string) string {
	GinkgoHelper()
	target, _ := json.Marshal(map[string]any{"version_id": version, "page_path": page, "render_instance_id": render})
	r, e := f.core.Create(ctx, f.token, key(t), importer.CreateRequest{Purpose: "context", InputMode: "object", Target: target})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	var created struct {
		ID string `json:"upload_id"`
	}
	_ = json.Unmarshal(r.Body, &created)
	length := int64(len(content))
	ack, e := f.core.Put(ctx, f.token, created.ID, importer.Chunk{Path: "_input", Kind: "file", Hash: sha256Hex(content), Length: &length, Final: true}, strings.NewReader(content))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	r, e = f.core.Complete(ctx, f.token, created.ID, key(t), cp)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	Expect(f.core.Process(ctx, decoded(t, r)["job_id"]) != nil).To(BeFalse())
	return created.ID
}

func (f *fixture) execSQL(statement string) {
	db, e := sql.Open("sqlite", "file:"+f.dbPath+"?mode=rw")
	Expect(e != nil).To(BeFalse())
	defer db.Close()
	_, e = db.ExecContext(context.Background(), statement)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("exec ", statement, ": ", e) })
}

func (f *fixture) pinCount(query string, args ...any) int {
	db, e := sql.Open("sqlite", "file:"+filepath.Join(f.dbPath)+"?mode=ro")
	Expect(e != nil).To(BeFalse())
	defer db.Close()
	var n int
	Expect(db.QueryRowContext(context.Background(), query, args...).Scan(&n) != nil).To(BeFalse())
	return n
}

var _ = It("TestContextsDirectUploadReplayScopeAndLifecycle", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	v := key(t)
	u := f.sealed(t, nil, "<html>ctx</html>")
	document, version := f.imported(t, u, v)

	body := `{"선택":"시나리오","step":3}`
	operation := key(t)
	response, e := f.core.CreateContext(ctx, f.token, operation, importer.ContextQuery{VersionID: version, Input: "direct", MediaType: "application/json"}, strings.NewReader(body))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	Expect(response.Status != 201).To(BeFalse(), func() string { return fmt.Sprint(response.Status, string(response.Body)) })
	var created map[string]any
	Expect(json.Unmarshal(response.Body, &created) != nil).To(BeFalse())
	ref := created["context_ref"].(string)
	Expect(created["media_type"] != "application/json").To(BeFalse())
	Expect(created["bytes_sha256"] != sha256Hex(body)).To(BeFalse())

	// Same key, identical bytes and binding replays the original response.
	replay, e := f.core.CreateContext(ctx, f.token, operation, importer.ContextQuery{VersionID: version, Input: "direct", MediaType: "application/json"}, strings.NewReader(body))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	Expect(string(replay.Body) != string(response.Body)).To(BeFalse(), func() string { return "replay body differs" })

	// Same key with changed bytes conflicts.
	_, e = f.core.CreateContext(ctx, f.token, operation, importer.ContextQuery{VersionID: version, Input: "direct", MediaType: "application/json"}, strings.NewReader(`{"other":true}`))
	Expect(errors.Is(e, domain.ErrConflict)).To(BeTrue(), func() string { return fmt.Sprint("changed bytes: ", e) })

	// Cross-form normalization: identical bytes through a sealed upload under
	// the SAME key replay rather than conflict.
	upload := f.stageContext(t, version, body, nil, nil)
	cross, e := f.core.CreateContext(ctx, f.token, operation, importer.ContextQuery{VersionID: version, Input: "upload", UploadID: upload}, nil)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("cross-form: ", e) })
	Expect(string(cross.Body) != string(response.Body)).To(BeFalse(), func() string { return "cross-form replay body differs" })

	// Declared text/plain intent with JSON-looking bytes keeps text media.
	textKey := key(t)
	textResponse, e := f.core.CreateContext(ctx, f.token, textKey, importer.ContextQuery{VersionID: version, Input: "direct", MediaType: "text/plain"}, strings.NewReader(body))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	var textCreated map[string]any
	_ = json.Unmarshal(textResponse.Body, &textCreated)
	Expect(textCreated["media_type"] != "text/plain").To(BeFalse(), func() string { return "declared text intent reclassified" })
	// Logical-media identity: the same JSON bytes under the SAME key replay
	// through the upload form even though the first submission declared
	// text/plain (and the reverse direction shares the same identity).
	textUpload := f.stageContext(t, version, body, nil, nil)
	textCross, e := f.core.CreateContext(ctx, f.token, textKey, importer.ContextQuery{VersionID: version, Input: "upload", UploadID: textUpload}, nil)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("text-declared cross-form: ", e) })
	Expect(string(textCross.Body) != string(textResponse.Body)).To(BeFalse(), func() string { return "text-declared cross-form replay differs" })

	// Exact bytes stream back under the record media type.
	var streamed bytes.Buffer
	Expect(f.core.ReadContext(ctx, f.token, ref, func(record domain.ContextRecord, handle io.ReadSeeker) error {
		_, err := io.Copy(&streamed, handle)
		return err
	}) != nil).To(BeFalse(), func() string { return "read failed" })
	Expect(streamed.String() != body).To(BeFalse(), func() string { return "context bytes changed" })

	// The record owns exactly one durable context pin and retains its session relation.
	Expect(f.pinCount("SELECT count(*) FROM object_pins WHERE owner_kind='context' AND owner_id=?", ref) != 1).To(BeFalse(), func() string { return "context pin missing" })
	Expect(f.pinCount("SELECT count(*) FROM contexts WHERE context_id=? AND origin_session_id!='' AND media_type='application/json'", ref) != 1).To(BeFalse(), func() string { return "session relation missing" })

	// Invalid declared JSON fails without creating a record.
	before := f.pinCount("SELECT count(*) FROM contexts")
	_, e = f.core.CreateContext(ctx, f.token, key(t), importer.ContextQuery{VersionID: version, Input: "direct", MediaType: "application/json"}, strings.NewReader(`{"a":1,}`))
	Expect(e == nil).To(BeFalse(), func() string { return "trailing comma accepted as JSON" })
	Expect(f.pinCount("SELECT count(*) FROM contexts") != before).To(BeFalse(), func() string { return "failed staging left a record" })

	// Tuple normalization: stored null render pairs with an omitted query.
	ok, e := f.core.CreateContext(ctx, f.token, key(t), importer.ContextQuery{VersionID: version, Input: "upload", UploadID: f.stageContext(t, version, `{"x":1}`, nil, nil)}, nil)
	Expect(e != nil || ok.Status != 201).To(BeFalse(), func() string { return fmt.Sprint("null/omitted tuple mismatch: ", e) })
	// A stored non-null render never pairs with an omitted parameter or a different instance.
	r1 := key(t)
	bound := f.stageContext(t, version, `{"x":2}`, nil, &r1)
	_, e = f.core.CreateContext(ctx, f.token, key(t), importer.ContextQuery{VersionID: version, Input: "upload", UploadID: bound}, nil)
	Expect(errors.Is(e, sec.ErrInvalid)).To(BeTrue(), func() string { return fmt.Sprint("R1/omitted accepted: ", e) })
	_, e = f.core.CreateContext(ctx, f.token, key(t), importer.ContextQuery{VersionID: version, Input: "upload", UploadID: bound, RenderInstanceID: strPtr(key(t))}, nil)
	Expect(errors.Is(e, sec.ErrInvalid)).To(BeTrue(), func() string { return fmt.Sprint("implicit rebinding accepted: ", e) })
	exact, e := f.core.CreateContext(ctx, f.token, key(t), importer.ContextQuery{VersionID: version, Input: "upload", UploadID: bound, RenderInstanceID: &r1}, nil)
	Expect(e != nil || exact.Status != 201).To(BeFalse(), func() string { return fmt.Sprint("exact render tuple rejected: ", e) })

	// Scoped context.write with exact anchors creates; without the action or
	// anchors it is denied; the record principal alone can read it back.
	scopedToken := f.seedScoped(t, domain.CredentialScope{Kind: "workflow", Actions: []string{"context.write"}, VersionIDs: []string{version}, ProjectIDs: []string{f.project}})
	scopedResponse, e := f.core.CreateContext(ctx, scopedToken, key(t), importer.ContextQuery{VersionID: version, Input: "direct", MediaType: "text/plain"}, strings.NewReader("scoped text"))
	Expect(e != nil || scopedResponse.Status != 201).To(BeFalse(), func() string { return fmt.Sprint("scoped context.write rejected: ", e, string(scopedResponse.Body)) })
	var scopedCreated map[string]any
	_ = json.Unmarshal(scopedResponse.Body, &scopedCreated)
	scopedRef := scopedCreated["context_ref"].(string)
	Expect(errors.Is(f.core.ReadContext(ctx, scopedToken, ref, func(domain.ContextRecord, io.ReadSeeker) error { return nil }), domain.ErrNotFound)).To(BeTrue(), func() string { return "foreign record readable" })
	Expect(f.core.ReadContext(ctx, scopedToken, scopedRef, func(domain.ContextRecord, io.ReadSeeker) error { return nil }) != nil).To(BeFalse(), func() string { return "own record unreadable" })
	noAction := f.seedScoped(t, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, VersionIDs: []string{version}, ProjectIDs: []string{f.project}})
	_, e = f.core.CreateContext(ctx, noAction, key(t), importer.ContextQuery{VersionID: version, Input: "direct", MediaType: "text/plain"}, strings.NewReader("no action"))
	Expect(errors.Is(e, domain.ErrDenied)).To(BeTrue(), func() string { return fmt.Sprint("missing context.write allowed: ", e) })
	_, e = f.core.CreateContext(ctx, scopedToken, key(t), importer.ContextQuery{VersionID: key(t), Input: "direct", MediaType: "text/plain"}, strings.NewReader("outside"))
	Expect(e == nil).To(BeFalse(), func() string { return "outside anchors accepted" })

	// Fresh-use proof: one transaction proves the exact originating session,
	// the persisted tuple and the object digest without opening the staged
	// object. Other sessions, tuples or digests fail as a binding
	// precondition; the plain recovery read above stays unaffected.
	etag := "\"sha256-" + sha256Hex(body) + "\""
	Expect(f.core.VerifyContextFresh(ctx, f.token, ref, importer.ContextQuery{VersionID: version}, etag)).To(Succeed(), "originating-session fresh proof failed")
	wrongDigest := "\"sha256-" + sha256Hex("other bytes") + "\""
	Expect(errors.Is(f.core.VerifyContextFresh(ctx, f.token, ref, importer.ContextQuery{VersionID: version}, wrongDigest), domain.ErrPrecondition)).To(BeTrue(), "wrong digest accepted as fresh")
	Expect(errors.Is(f.core.VerifyContextFresh(ctx, f.token, ref, importer.ContextQuery{VersionID: key(t)}, etag), domain.ErrPrecondition)).To(BeTrue(), "wrong version accepted as fresh")
	var exactRef string
	{
		var boundRecord map[string]any
		Expect(json.Unmarshal(exact.Body, &boundRecord) != nil).To(BeFalse())
		exactRef = boundRecord["context_ref"].(string)
	}
	boundDigest := "\"sha256-" + sha256Hex(`{"x":2}`) + "\""
	Expect(f.core.VerifyContextFresh(ctx, f.token, exactRef, importer.ContextQuery{VersionID: version, RenderInstanceID: &r1}, boundDigest)).To(Succeed(), "bound-tuple fresh proof failed")
	Expect(errors.Is(f.core.VerifyContextFresh(ctx, f.token, exactRef, importer.ContextQuery{VersionID: version}, boundDigest), domain.ErrPrecondition)).To(BeTrue(), "null render paired with the stored R1 binding")
	scopedDigest := "\"sha256-" + sha256Hex("scoped text") + "\""
	Expect(f.core.VerifyContextFresh(ctx, scopedToken, scopedRef, importer.ContextQuery{VersionID: version}, scopedDigest)).To(Succeed(), "scoped originating-session fresh proof failed")
	Expect(errors.Is(f.core.VerifyContextFresh(ctx, f.token, scopedRef, importer.ContextQuery{VersionID: version}, scopedDigest), domain.ErrPrecondition)).To(BeTrue(), "another session claimed a foreign record as fresh")

	// Revoked credentials lose context reads.
	f.execSQL("UPDATE scoped_credentials SET revoked=1")
	Expect(f.core.ReadContext(ctx, scopedToken, scopedRef, func(domain.ContextRecord, io.ReadSeeker) error { return nil }) == nil).To(BeFalse(), func() string { return "revoked credential read contexts" })

	// Lifecycle: trashing the document hides creation, reads AND the saved
	// same-key receipt (no now-hidden body survives revocation).
	f.execSQL("UPDATE documents SET lifecycle='trash' WHERE document_id='" + document + "'")
	_, e = f.core.CreateContext(ctx, f.token, key(t), importer.ContextQuery{VersionID: version, Input: "direct", MediaType: "text/plain"}, strings.NewReader("after trash"))
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue(), func() string { return fmt.Sprint("trash create not hidden: ", e) })
	_, e = f.core.CreateContext(ctx, f.token, operation, importer.ContextQuery{VersionID: version, Input: "direct", MediaType: "application/json"}, strings.NewReader(body))
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue(), func() string { return fmt.Sprint("same-key replay after trash returned: ", e) })
	Expect(errors.Is(f.core.ReadContext(ctx, f.token, ref, func(domain.ContextRecord, io.ReadSeeker) error { return nil }), domain.ErrNotFound)).To(BeTrue(), func() string { return "trash read did not 404" })
	Expect(errors.Is(f.core.VerifyContextFresh(ctx, f.token, ref, importer.ContextQuery{VersionID: version}, etag), domain.ErrNotFound)).To(BeTrue(), func() string { return "trash fresh proof did not 404" })
	_ = time.Now
})

func (f *fixture) seedScoped(t GinkgoTInterface, scope domain.CredentialScope) string {
	GinkgoHelper()
	token, e := sec.Secret()
	Expect(e != nil).To(BeFalse())
	principal, binding, credential := key(t), key(t), key(t)
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: binding, Active: true, Scope: scope}); e != nil {
			return e
		}
		l, e := tx.Library()
		if e != nil {
			return e
		}
		p, e := tx.Scope(f.project)
		if e != nil {
			return e
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: credential, PrincipalID: principal, TokenHash: sec.Verifier("workflow", token), AuthEpoch: l.AuthEpoch, Scope: scope, Permissions: map[string]int64{f.project: p.Project.PermissionsRevision}, ConfigRevision: p.ConfigRevision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)})
	}) != nil).To(BeFalse())
	return token
}

var _ = It("TestContextsHTTPAdapterQueryDiscipline", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	v := key(t)
	u := f.sealed(t, nil, "<html>http</html>")
	_, version := f.imported(t, u, v)
	handler, e := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{"127.0.0.1:9270"}, Origins: []string{"http://127.0.0.1:9270"}}, httpimports.New(f.core))
	Expect(e != nil).To(BeFalse())
	request := func(target, contentType, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:9270"+target, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+f.token)
		r.Header.Set("Origin", "http://127.0.0.1:9270")
		r.Header.Set("Idempotency-Key", key(t))
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	Expect(request("/api/v1/contexts?version_id="+version+"&page_path=a.html&page_path=b.html", "text/plain", "x").Code != 400).To(BeFalse(), func() string { return "duplicate binding parameter accepted" })
	Expect(request("/api/v1/contexts?version_id="+version+"&upload_id="+key(t), "application/json", `{}`).Code != 400).To(BeFalse(), func() string { return "upload_id query accepted" })
	Expect(request("/api/v1/contexts?version_id="+version+"&page_path=", "text/plain", "x").Code != 400).To(BeFalse(), func() string { return "empty binding parameter accepted" })
	Expect(request("/api/v1/contexts?version_id="+version, "text/plain", "plain text").Code != 201).To(BeFalse(), func() string { return "valid direct creation failed" })
})

var _ = It("TestContextsHTTPFreshBindingProofMode", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	u := f.sealed(t, nil, "<html>proof mode</html>")
	_, version := f.imported(t, u, key(t))
	body := `{"fresh":"증명"}`
	response, e := f.core.CreateContext(ctx, f.token, key(t), importer.ContextQuery{VersionID: version, Input: "direct", MediaType: "application/json"}, strings.NewReader(body))
	Expect(e != nil || response.Status != 201).To(BeFalse(), func() string { return fmt.Sprint("create failed: ", e, string(response.Body)) })
	var created map[string]any
	Expect(json.Unmarshal(response.Body, &created) != nil).To(BeFalse())
	ref := created["context_ref"].(string)
	etag := "\"sha256-" + created["bytes_sha256"].(string) + "\""
	handler, e := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{"127.0.0.1:9270"}, Origins: []string{"http://127.0.0.1:9270"}}, httpimports.New(f.core))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	binding := encodeBindingHeader(`{"version_id":"` + version + `","page_path":null,"render_instance_id":null}`)
	otherVersion := encodeBindingHeader(`{"version_id":"` + key(t) + `","page_path":null,"render_instance_id":null}`)
	get := func(headers map[string][]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://127.0.0.1:9270/api/v1/contexts/"+ref, nil)
		r.Header.Set("Authorization", "Bearer "+f.token)
		r.Header.Set("Origin", "http://127.0.0.1:9270")
		for k, values := range headers {
			for _, v := range values {
				r.Header.Add(k, v)
			}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	one := func(k, v string) map[string][]string { return map[string][]string{k: {v}} }
	pair := func(b string) map[string][]string {
		return map[string][]string{"Haeryong-Context-Binding": {b}, "If-None-Match": {etag}}
	}

	// The fresh pair proves with a body-less 304, the exact strong ETag and
	// the dedicated marker; the staged object is never opened for the proof.
	w := get(pair(binding))
	Expect(w.Code != 304).To(BeFalse(), func() string { return fmt.Sprint("proof status ", w.Code, " body ", w.Body.String()) })
	Expect(w.Header().Get("ETag") != etag).To(BeFalse(), "proof ETag mismatch")
	Expect(w.Header().Get("Haeryong-Context-Verified") != "fresh").To(BeFalse(), "proof marker missing")
	Expect(w.Body.Len() != 0).To(BeFalse(), "proof response carried a body")
	Expect(w.Header().Get("Content-Length") != "").To(BeFalse(), "proof response declared a body length")

	// Binding absence keeps the ordinary recovery GET byte-exact: standalone
	// conditional or Range headers are ignored exactly as before, and the
	// fresh marker is never emitted on a plain GET.
	for name, headers := range map[string]map[string][]string{
		"plain":              {},
		"if-none-match":      one("If-None-Match", etag),
		"range":              one("Range", "bytes=0-2"),
		"if-match":           one("If-Match", etag),
		"if-none-match-weak": one("If-None-Match", "W/"+etag),
	} {
		plain := get(headers)
		Expect(plain.Code != 200).To(BeFalse(), func() string { return name + " plain GET status " + fmt.Sprint(plain.Code) })
		Expect(plain.Body.String() != body).To(BeFalse(), name+" plain GET bytes changed")
		Expect(plain.Header().Get("Haeryong-Context-Verified") != "").To(BeFalse(), name+" plain GET emitted the fresh marker")
		Expect(plain.Header().Get("ETag") != etag).To(BeFalse(), name+" plain GET ETag changed")
	}

	// Mismatching proofs are binding rejections, never byte responses.
	wrongDigest := "\"sha256-" + strings.Repeat("a", 64) + "\""
	Expect(wrongDigest == etag).To(BeFalse())
	w = get(map[string][]string{"Haeryong-Context-Binding": {binding}, "If-None-Match": {wrongDigest}})
	Expect(w.Code != 412).To(BeFalse(), func() string { return fmt.Sprint("digest mismatch status ", w.Code, " ", w.Body.String()) })
	Expect(w.Body.String() != "" && !strings.Contains(w.Body.String(), "PRECONDITION_FAILED")).To(BeFalse(), "digest mismatch error code")
	w = get(map[string][]string{"Haeryong-Context-Binding": {otherVersion}, "If-None-Match": {etag}})
	Expect(w.Code != 412).To(BeFalse(), "tuple mismatch accepted")
	w = get(map[string][]string{"Haeryong-Context-Binding": {binding}, "If-None-Match": {etag}, "Range": {"bytes=0-2"}})
	Expect(w.Code != 400).To(BeFalse(), "Range combined with the proof mode accepted")

	// The mode is closed: malformed pairs fail INVALID_REQUEST before any
	// record lookup.
	encoded := encodeBindingHeader(`{"version_id":"` + version + `","page_path":null,"render_instance_id":null}`)
	crlf := encoded[:len(encoded)/2] + "\r\n" + encoded[len(encoded)/2:]
	// A binding whose JSON length leaves final-group don't-care bits: the
	// canonical encoding reaches tuple validation (412), while flipping only
	// the unused bits fails parsing (400).
	noncanonicalJSON := `{"version_id":"` + version + `","page_path":"pg.html","render_instance_id":null}`
	noncanonicalEncoding := encodeBindingHeader(noncanonicalJSON)
	variant := nonCanonicalVariant(noncanonicalEncoding)
	Expect(variant).NotTo(BeEmpty())
	canonicalPair := get(map[string][]string{"Haeryong-Context-Binding": {noncanonicalEncoding}, "If-None-Match": {etag}})
	Expect(canonicalPair.Code != 412).To(BeFalse(), func() string { return fmt.Sprint("canonical page-bound proof status ", canonicalPair.Code) })
	for name, headers := range map[string]map[string][]string{
		"binding-without-match": one("Haeryong-Context-Binding", binding),
		"duplicate-binding":     {"Haeryong-Context-Binding": {binding, binding}, "If-None-Match": {etag}},
		"duplicate-match":       {"Haeryong-Context-Binding": {binding}, "If-None-Match": {etag, etag}},
		"wildcard-match":        {"Haeryong-Context-Binding": {binding}, "If-None-Match": {"*"}},
		"weak-match":            {"Haeryong-Context-Binding": {binding}, "If-None-Match": {"W/" + etag}},
		"match-list":            {"Haeryong-Context-Binding": {binding}, "If-None-Match": {etag + ", \"sha256-" + strings.Repeat("c", 64) + "\""}},
		"empty-binding":         one("Haeryong-Context-Binding", ""),
		"invalid-base64":        one("Haeryong-Context-Binding", "!!!!"),
		"padded-base64":         one("Haeryong-Context-Binding", encoded+"="),
		"crlf-inside-binding":   one("Haeryong-Context-Binding", crlf),
		"noncanonical-bits":     one("Haeryong-Context-Binding", variant),
		"non-utf8":              one("Haeryong-Context-Binding", base64.RawURLEncoding.EncodeToString([]byte{0xff, '{', '}'})),
		"missing-key":           one("Haeryong-Context-Binding", encodeBindingHeader(`{"version_id":"`+version+`","render_instance_id":null}`)),
		"unknown-key":           one("Haeryong-Context-Binding", encodeBindingHeader(`{"version_id":"`+version+`","page_path":null,"render_instance_id":null,"x":1}`)),
		"duplicate-json-key":    one("Haeryong-Context-Binding", encodeBindingHeader(`{"version_id":"`+version+`","version_id":"`+version+`","page_path":null,"render_instance_id":null}`)),
		"number-page":           one("Haeryong-Context-Binding", encodeBindingHeader(`{"version_id":"`+version+`","page_path":7,"render_instance_id":null}`)),
		"bad-render-uuid":       one("Haeryong-Context-Binding", encodeBindingHeader(`{"version_id":"`+version+`","page_path":null,"render_instance_id":"nope"}`)),
		"trailing-json":         one("Haeryong-Context-Binding", encodeBindingHeader(`{"version_id":"`+version+`","page_path":null,"render_instance_id":null} {}`)),
	} {
		w = get(headers)
		Expect(w.Code != 400).To(BeFalse(), func() string { return name + " accepted: " + fmt.Sprint(w.Code) + " " + w.Body.String() })
	}

	// Unknown references stay nondisclosing in the proof mode.
	r := httptest.NewRequest("GET", "http://127.0.0.1:9270/api/v1/contexts/"+key(t), nil)
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Origin", "http://127.0.0.1:9270")
	r.Header.Set("Haeryong-Context-Binding", binding)
	r.Header.Set("If-None-Match", etag)
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, r)
	Expect(unknown.Code != 404).To(BeFalse(), "unknown ref not nondisclosing in proof mode")
})

func strPtr(v string) *string { return &v }

// C3: declared-JSON failures (including whitespace-terminated bare minus)
// publish no ContextRecord or context pin through the real store, an existing
// owned upload pin is untouched, and the identical bytes remain valid
// text/plain content with preserved media classification.
var _ = It("TestContextsDeclaredJSONFailurePublishesNothingAndTextPreserved", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	v := key(t)
	u := f.sealed(t, nil, "<html>ctx3</html>")
	_, version := f.imported(t, u, v)
	// The REAL chunk-acceptance pin (Kind "upload", one per accepted range)
	// must exist positively before the failures and stay exactly preserved:
	// a 0->0 staging count would be vacuous.
	openTarget, _ := json.Marshal(map[string]any{"version_id": version, "page_path": nil, "render_instance_id": nil})
	openResponse, e := f.core.Create(ctx, f.token, key(t), importer.CreateRequest{Purpose: "context", InputMode: "object", Target: openTarget})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	var openUpload struct {
		ID string `json:"upload_id"`
	}
	_ = json.Unmarshal(openResponse.Body, &openUpload)
	chunkLength := int64(len(`{"kept":true}`))
	_, e = f.core.Put(ctx, f.token, openUpload.ID, importer.Chunk{Path: "_input", Kind: "file", Hash: sha256Hex(`{"kept":true}`), Length: &chunkLength, Final: true}, strings.NewReader(`{"kept":true}`))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	beforeUploadPins := f.pinCount("SELECT count(*) FROM object_pins WHERE owner_kind='upload' AND owner_id=?", openUpload.ID)
	Expect(beforeUploadPins < 1).To(BeFalse(), func() string { return "accepted chunk left no upload pin to preserve" })
	beforeRecords := f.pinCount("SELECT count(*) FROM contexts")
	beforeContextPins := f.pinCount("SELECT count(*) FROM object_pins WHERE owner_kind='context'")
	for _, invalid := range []string{"-", "- ", "[-]", "[- ]", `{"a":-}`, `{"a":- }`} {
		_, e := f.core.CreateContext(ctx, f.token, key(t), importer.ContextQuery{VersionID: version, Input: "direct", MediaType: "application/json"}, strings.NewReader(invalid))
		Expect(e == nil).To(BeFalse(), func() string { return "invalid JSON accepted: " + invalid })
	}
	Expect(f.pinCount("SELECT count(*) FROM contexts") != beforeRecords).To(BeFalse(), func() string { return "failed declared JSON published a record" })
	Expect(f.pinCount("SELECT count(*) FROM object_pins WHERE owner_kind='context'") != beforeContextPins).To(BeFalse(), func() string { return "failed declared JSON published a context pin" })
	Expect(f.pinCount("SELECT count(*) FROM object_pins WHERE owner_kind='upload' AND owner_id=?", openUpload.ID) != beforeUploadPins).To(BeFalse(), func() string { return "failed staging disturbed the accepted upload pin" })
	// The identical bytes remain honest text content with preserved media.
	response, e := f.core.CreateContext(ctx, f.token, key(t), importer.ContextQuery{VersionID: version, Input: "direct", MediaType: "text/plain"}, strings.NewReader(`{"a":-}`))
	Expect(e != nil || response.Status != 201).To(BeFalse(), func() string { return fmt.Sprint("text bytes rejected: ", e) })
	var created map[string]any
	_ = json.Unmarshal(response.Body, &created)
	Expect(created["media_type"] != "text/plain").To(BeFalse(), func() string { return "text media reclassified" })
	var streamed bytes.Buffer
	Expect(f.core.ReadContext(ctx, f.token, created["context_ref"].(string), func(record domain.ContextRecord, handle io.ReadSeeker) error {
		_, err := io.Copy(&streamed, handle)
		return err
	}) != nil).To(BeFalse(), func() string { return "text context unreadable" })
	Expect(streamed.String() != `{"a":-}`).To(BeFalse(), func() string { return "text bytes changed" })
})
