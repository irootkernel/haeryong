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

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"time"
)

var _ = It("TestProductionScopedCredentialDraftWriteAndAcceptedWork", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	g := generationJob(f, "draft", nil, nil)
	draft, principal, binding, credential := g.DraftID, key(t), key(t), key(t)
	token, e := sec.Secret()
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	scope := domain.CredentialScope{Kind: "workflow", Actions: []string{"draft.write"}, ProjectIDs: []string{f.project}, DraftIDs: []string{draft}}
	e = f.store.Write(ctx, func(tx app.WriteTx) error {
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
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	handler, e := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{"127.0.0.1:9270"}, Origins: []string{"http://127.0.0.1:9270"}}, httpimports.New(f.core))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	request := func(method, target string, body []byte, operation string, headers map[string]string) *httptest.ResponseRecorder {
		GinkgoHelper()
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
	Expect(create.Code != 201).To(BeFalse(), func() string { return fmt.Sprint("draft create", create.Code, create.Body.String()) })

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
	Expect(w.Code != 200).To(BeFalse(), func() string { return fmt.Sprint("draft transfer", w.Code, w.Body.String()) })

	var ack struct{ Data importer.ChunkResult }
	{
		e = json.Unmarshal(w.Body.Bytes(), &ack)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	cp := ack.Data.Checkpoint
	cp.Generation++
	cp.Complete = true
	complete := request("POST", "/api/v1/uploads/"+envelope.Data.ID+"/complete", encodedTest(map[string]any{"checkpoint": cp}), key(t), nil)
	Expect(complete.Code != 202).To(BeFalse(), func() string { return fmt.Sprint("draft seal", complete.Code, complete.Body.String()) })

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
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	{

		e = f.core.Process(ctx, accepted.Data.Job)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint("accepted seal depends on creator credential", e) })
	}

	j, e := f.store.Job(ctx, accepted.Data.Job)
	Expect(e != nil || j.State != "completed").To(BeFalse(), func() string { return fmt.Sprint(j, e) })
	{

		got := request("GET", "/api/v1/uploads/"+envelope.Data.ID, nil, "", nil)
		Expect(got.Code != 401).To(BeFalse(), func() string { return fmt.Sprint("revoked credential accepted", got.Code) })
	}

	// A fresh actual scoped credential still cannot publish or manage owner policy.
	token, e = sec.Secret()
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

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
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	for _, c := range []struct {
		method, path string
		body         any
	}{{"POST", "/api/v1/imports", map[string]any{"upload_id": envelope.Data.ID, "document_id": nil}}, {"GET", "/api/v1/sessions", nil}, {"POST", "/api/v1/auth/pairings/" + key(t) + "/approve", map[string]string{"code": "00000000", "origin": "http://127.0.0.1:9270"}}} {
		var b []byte
		if c.body != nil {
			b = encodedTest(c.body)
		}
		got := request(c.method, c.path, b, key(t), map[string]string{"If-Match": "\"pairing-" + key(t) + "-r1\""})
		Expect(got.Code != 403).To(BeFalse(), func() string { return fmt.Sprintf("scoped %s %s: %d", c.method, c.path, got.Code) })

	}
	for _, target := range []string{"/api/v1/configuration", "/api/v1/personal/review"} {
		e := f.store.Write(ctx, func(tx app.WriteTx) error {
			return f.auth.Authority(token).AuthorizeWrite(ctx, tx, domain.Request{Key: domain.OperationKey{PrincipalID: principal}, Method: http.MethodPost, Target: target})
		})
		Expect(e != domain.ErrDenied).To(BeFalse(), func() string { return fmt.Sprint("scoped management admitted", target, e) })

	}
})
