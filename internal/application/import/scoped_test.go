package importer_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	httpimports "haeryong.local/haeryong/internal/adapters/http/imports"
	importer "haeryong.local/haeryong/internal/application/import"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestProductionScopedCredentialDraftWriteAndAcceptedWork(t *testing.T) {
	f := newFixture(t)
	draft, principal, binding, credential := key(t), key(t), key(t), key(t)
	token, e := sec.Secret()
	if e != nil {
		t.Fatal(e)
	}
	scope := domain.CredentialScope{Kind: "workflow", Actions: []string{"draft.write"}, ProjectIDs: []string{f.project}, DraftIDs: []string{draft}}
	e = f.store.Write(ctx, func(tx app.WriteTx) error {
		if _, e := tx.MutateResource(domain.Resource{Kind: "draft", ID: draft, Value: encodedTest(map[string]any{"project_id": f.project, "document_id": nil, "state": "ready"})}, 0, "resource.invalidated"); e != nil {
			return e
		}
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
	})
	if e != nil {
		t.Fatal(e)
	}
	handler, e := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{"127.0.0.1:9270"}, Origins: []string{"http://127.0.0.1:9270"}}, httpimports.New(f.core))
	if e != nil {
		t.Fatal(e)
	}
	request := func(method, target string, body []byte, operation string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://127.0.0.1:9270"+target, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		if operation != "" {
			r.Header.Set("Idempotency-Key", operation)
		}
		if len(body) > 0 {
			r.Header.Set("Content-Type", "application/json")
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	target := map[string]any{"project_id": f.project, "document_id": nil, "draft_id": draft}
	create := request("POST", "/api/v1/uploads", encodedTest(map[string]any{"purpose": "result", "input_mode": "directory", "target": target}), key(t), nil)
	if create.Code != 201 {
		t.Fatal("draft create", create.Code, create.Body.String())
	}
	var envelope struct {
		Data struct {
			ID string `json:"upload_id"`
		}
	}
	_ = json.Unmarshal(create.Body.Bytes(), &envelope)
	content := []byte("<html>scoped draft</html>")
	sum := sha256.Sum256(content)
	r := httptest.NewRequest("PUT", "http://127.0.0.1:9270/api/v1/uploads/"+envelope.Data.ID+"/entries?path=hello.html&kind=file", bytes.NewReader(content))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("Haeryong-Upload-Offset", "0")
	r.Header.Set("Haeryong-Upload-Chunk-SHA256", hex.EncodeToString(sum[:]))
	r.Header.Set("Haeryong-Upload-Length", strconv.Itoa(len(content)))
	r.Header.Set("Haeryong-Upload-Final", "true")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("draft transfer", w.Code, w.Body.String())
	}
	var ack struct{ Data importer.ChunkResult }
	if e = json.Unmarshal(w.Body.Bytes(), &ack); e != nil {
		t.Fatal(e)
	}
	cp := ack.Data.Checkpoint
	cp.Generation++
	cp.Complete = true
	complete := request("POST", "/api/v1/uploads/"+envelope.Data.ID+"/complete", encodedTest(map[string]any{"checkpoint": cp}), key(t), nil)
	if complete.Code != 202 {
		t.Fatal("draft seal", complete.Code, complete.Body.String())
	}
	var accepted struct {
		Data struct {
			Job string `json:"job_id"`
		}
	}
	_ = json.Unmarshal(complete.Body.Bytes(), &accepted)
	// A revoked creator credential does not revoke the stable accepted authority.
	e = f.store.Write(ctx, func(tx app.WriteTx) error {
		c, e := tx.ScopedCredentialByHash(sec.Verifier("workflow", token))
		if e != nil {
			return e
		}
		c.Revoked = true
		return tx.PutScopedCredential(c)
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = f.core.Process(ctx, accepted.Data.Job); e != nil {
		t.Fatal("accepted seal depends on creator credential", e)
	}
	j, e := f.store.Job(ctx, accepted.Data.Job)
	if e != nil || j.State != "completed" {
		t.Fatal(j, e)
	}
	if got := request("GET", "/api/v1/uploads/"+envelope.Data.ID, nil, "", nil); got.Code != 401 {
		t.Fatal("revoked credential accepted", got.Code)
	}
	// A fresh actual scoped credential still cannot publish or manage owner policy.
	token, e = sec.Secret()
	if e != nil {
		t.Fatal(e)
	}
	e = f.store.Write(ctx, func(tx app.WriteTx) error {
		l, e := tx.Library()
		if e != nil {
			return e
		}
		p, e := tx.Scope(f.project)
		if e != nil {
			return e
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: key(t), PrincipalID: principal, TokenHash: sec.Verifier("workflow", token), AuthEpoch: l.AuthEpoch, Scope: scope, Permissions: map[string]int64{f.project: p.Project.PermissionsRevision}, ConfigRevision: p.ConfigRevision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)})
	})
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{{"POST", "/api/v1/imports", map[string]any{"upload_id": envelope.Data.ID, "document_id": nil}}, {"GET", "/api/v1/sessions", nil}, {"POST", "/api/v1/auth/pairings/" + key(t) + "/approve", map[string]string{"code": "00000000", "origin": "http://127.0.0.1:9270"}}} {
		var b []byte
		if c.body != nil {
			b = encodedTest(c.body)
		}
		got := request(c.method, c.path, b, key(t), map[string]string{"If-Match": "\"pairing-" + key(t) + "-r1\""})
		if got.Code != 403 {
			t.Errorf("scoped %s %s: %d", c.method, c.path, got.Code)
		}
	}
	for _, target := range []string{"/api/v1/configuration", "/api/v1/personal/review"} {
		e := f.store.Write(ctx, func(tx app.WriteTx) error {
			return f.auth.Authority(token).AuthorizeWrite(ctx, tx, domain.Request{Key: domain.OperationKey{PrincipalID: principal}, Method: http.MethodPost, Target: target})
		})
		if e != domain.ErrDenied {
			t.Fatal("scoped management admitted", target, e)
		}
	}
}
