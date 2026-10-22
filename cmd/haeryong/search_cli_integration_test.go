package main

import (
	"context"
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var _ = It("SC14 seeded scoped and owner CLI preserve required safe reasons from real authenticated HTTP", Label("persistence"), func() {
	t := GinkgoT()
	f := newSourceCommandFixture(t)
	ctx := context.Background()
	health, e := f.store.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	p := health.Library.UnfiledProjectID
	object, e := f.store.Put(ctx, strings.NewReader("<p>needle</p>"), func(app.WriteTx, domain.Object) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	ids := []string{}
	newID := func() string { id, e := service.NewID(); Expect(e).NotTo(HaveOccurred()); return id }
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		for n := 0; n < 2; n++ {
			doc, set, v := newID(), newID(), newID()
			ids = append(ids, v)
			if e := tx.CreateDocument(domain.Document{ID: doc, ProjectID: p, Title: "Reason fixture"}); e != nil {
				return e
			}
			if e := tx.CreateContentSet(set, strings.Repeat("a", 64)); e != nil {
				return e
			}
			for i := 0; i < 4; i++ {
				if e := tx.PutEntry(domain.Entry{ContentSetID: set, Path: fmt.Sprintf("page%d.html", i), Kind: "file", Hash: object.Hash, Size: object.Size}); e != nil {
					return e
				}
			}
			if e := tx.SealContentSet(set); e != nil {
				return e
			}
			if e := tx.PutVersion(domain.Version{ID: v, DocumentID: doc, ContentSetID: set, Title: "Reason fixture", ViewerProfile: "html_js_v1", SourceBasis: "unverified"}); e != nil {
				return e
			}
			for i := 0; i < 4; i++ {
				if e := tx.PutPage(domain.Page{VersionID: v, Path: fmt.Sprintf("page%d.html", i)}); e != nil {
					return e
				}
			}
			if e := tx.PublishVersion(v, 1); e != nil {
				return e
			}
			index, e := tx.SearchIndex(v)
			if e != nil {
				return e
			}
			job, e := tx.ClaimJob(index.JobID)
			if e != nil {
				return e
			}
			if e = tx.FinishJob(job.ID, job.AttemptGeneration, "", nil); e != nil {
				return e
			}
			index.State = "partial"
			index.DiscoveryComplete = true
			index.Revision++
			if e = tx.PutSearchIndex(index); e != nil {
				return e
			}
			for i := 0; i < 4; i++ {
				state, reason := "complete", ""
				if i >= 2 {
					state = "failed"
					reason = "read_failed"
					if n == 1 {
						reason = "storage_failed"
					}
				}
				unit, e := tx.PutSearchUnit(domain.SearchUnit{VersionID: v, Kind: "page", PagePath: fmt.Sprintf("page%d.html", i), Title: "Reason fixture", State: "ready", BodyState: state, AnchorState: "not_applicable", Reason: reason})
				if e != nil {
					return e
				}
				if state == "complete" {
					if e = tx.PutSearchChunk(domain.SearchChunk{UnitID: unit, Part: "body", Body: "needle", DisplayBody: "needle"}); e != nil {
						return e
					}
				}
			}
		}
		return nil
	})).To(Succeed())
	token, e := sec.Secret()
	Expect(e).NotTo(HaveOccurred())
	principal := newID()
	scope := domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}, VersionIDs: []string{ids[0]}}
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		state, e := tx.AuthState()
		if e != nil {
			return e
		}
		if e = tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: principal, Active: true, Scope: scope, Revision: 1}); e != nil {
			return e
		}
		project, e := tx.Scope(p)
		if e != nil {
			return e
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: principal, PrincipalID: principal, TokenHash: sec.Verifier("workflow", token), AuthEpoch: state.AuthEpoch, Scope: scope, Permissions: map[string]int64{p: project.Project.PermissionsRevision}, ConfigRevision: project.ConfigRevision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond), Revision: 1})
	})).To(Succeed())
	for _, reader := range []struct {
		token   string
		reasons []query.IndexReason
	}{{f.token, []query.IndexReason{{Code: "read_failed", VersionCount: "1"}, {Code: "storage_failed", VersionCount: "1"}}}, {token, []query.IndexReason{{Code: "read_failed", VersionCount: "1"}}}} {
		_, flags := callerProfile(t, f.sel, f.session, reader.token, f.base)
		args := []string{"search", "--query", "needle", "--project", p, "--versions", "all", "--kind", "page", "--limit", "1"}
		raw := cli(t, 0, append(args, flags...)...)
		var result struct {
			Data query.SearchPage `json:"data"`
		}
		Expect(json.Unmarshal(raw, &result)).To(Succeed())
		Expect(result.Data.Index.Reasons).To(Equal(reader.reasons))
		Expect(result.Data.NextCursor).NotTo(BeNil())
		request, e := http.NewRequest("GET", f.base+"/api/v1/search?"+url.Values{"q": {"needle"}, "project_id": {p}, "versions": {"all"}, "kind": {"page"}, "limit": {"1"}}.Encode(), nil)
		Expect(e).NotTo(HaveOccurred())
		request.Header.Set("Authorization", "Bearer "+reader.token)
		response, e := http.DefaultClient.Do(request)
		Expect(e).NotTo(HaveOccurred())
		var expected struct {
			Data query.SearchPage `json:"data"`
		}
		Expect(json.NewDecoder(response.Body).Decode(&expected)).To(Succeed())
		Expect(response.Body.Close()).To(Succeed())
		Expect(response.StatusCode).To(Equal(200))
		Expect(result.Data).To(Equal(expected.Data))
		next := cli(t, 0, append(append(args, "--cursor", *result.Data.NextCursor), flags...)...)
		Expect(json.Unmarshal(next, &result)).To(Succeed())
		Expect(result.Data.Index.Reasons).To(Equal(reader.reasons))
	}
})
