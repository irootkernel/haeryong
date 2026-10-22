package main

import (
	"context"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "haeryong.local/haeryong/internal/adapters/http/v1"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

type searchAuthorityStore struct {
	app.Store
	after func()
}
type searchAuthorityTx struct {
	app.WriteTx
	sliced bool
}

func (t *searchAuthorityTx) SearchCandidates(o domain.SearchSelection, q string, after, through int64) (domain.SearchSlice, error) {
	t.sliced = true
	return t.WriteTx.SearchCandidates(o, q, after, through)
}
func (s *searchAuthorityStore) Write(ctx context.Context, f func(app.WriteTx) error) error {
	t := &searchAuthorityTx{}
	err := s.Store.Write(ctx, func(real app.WriteTx) error { t.WriteTx = real; return f(t) })
	if err == nil && t.sliced && s.after != nil {
		after := s.after
		s.after = nil
		after()
	}
	return err
}

var _ = It("I3 actual CLI exits 3 when revocation expiry or version.read removal occurs between search slices", Label("persistence"), func() {
	f := newSourceCommandFixture(GinkgoT())
	child := context.Background()
	health, e := f.store.Health(child)
	Expect(e).NotTo(HaveOccurred())
	p := health.Library.UnfiledProjectID
	id := func() string { x, e := service.NewID(); Expect(e).NotTo(HaveOccurred()); return x }
	doc, set, v := id(), id(), id()
	Expect(f.store.Write(child, func(tx app.WriteTx) error {
		if e := tx.CreateDocument(domain.Document{ID: doc, ProjectID: p, Title: "needle"}); e != nil {
			return e
		}
		if e := tx.CreateContentSet(set, strings.Repeat("a", 64)); e != nil {
			return e
		}
		if e := tx.SealContentSet(set); e != nil {
			return e
		}
		if e := tx.PutVersion(domain.Version{ID: v, DocumentID: doc, ContentSetID: set, Title: "needle", ViewerProfile: "html_js_v1", SourceBasis: "unverified"}); e != nil {
			return e
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
		index.State = "complete"
		index.DiscoveryComplete = true
		if e = tx.PutSearchIndex(index); e != nil {
			return e
		}
		unit, e := tx.PutSearchUnit(domain.SearchUnit{VersionID: v, Kind: "document", Title: "needle", State: "ready", BodyState: "complete", AnchorState: "not_applicable"})
		if e != nil {
			return e
		}
		for n := 0; n < 130; n++ {
			if e = tx.PutSearchChunk(domain.SearchChunk{UnitID: unit, Part: "metadata", Ordinal: n, Title: "needle", DisplayBody: "needle"}); e != nil {
				return e
			}
		}
		return nil
	})).To(Succeed())
	// Each credential captures one current workflow grant; expiry uses the auth
	// clock rather than a sleeping test or production timing hook.
	for _, change := range []string{"revoked", "expired", "action"} {
		token, e := sec.Secret()
		Expect(e).NotTo(HaveOccurred())
		principal := id()
		now := time.Now().UTC().Truncate(time.Millisecond)
		expiry := now.Add(time.Hour)
		f.auth.Now = func() time.Time { return now }
		scope := domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}, VersionIDs: []string{v}}
		Expect(f.store.Write(child, func(tx app.WriteTx) error {
			state, e := tx.AuthState()
			if e != nil {
				return e
			}
			if e = tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: principal, Active: true, Scope: scope}); e != nil {
				return e
			}
			project, e := tx.Scope(p)
			if e != nil {
				return e
			}
			return tx.PutScopedCredential(domain.ScopedCredential{ID: principal, PrincipalID: principal, TokenHash: sec.Verifier("workflow", token), AuthEpoch: state.AuthEpoch, Scope: scope, Permissions: map[string]int64{p: project.Project.PermissionsRevision}, ConfigRevision: project.ConfigRevision, CreatedAt: now, ExpiresAt: &expiry})
		})).To(Succeed())
		observed := &searchAuthorityStore{Store: f.store}
		changed := false
		observed.after = func() {
			changed = true
			Expect(f.store.Write(child, func(tx app.WriteTx) error {
				if change == "expired" {
					now = expiry.Add(time.Second)
					return nil
				}
				if change == "revoked" {
					c, e := tx.ScopedCredential(principal)
					if e != nil {
						return e
					}
					c.Revoked = true
					return tx.PutScopedCredential(c)
				}
				principal, e := tx.WorkflowPrincipal(principal)
				if e != nil {
					return e
				}
				principal.Scope.Actions = nil
				return tx.PutWorkflowPrincipal(principal)
			})).To(Succeed())
		}
		server := httptest.NewUnstartedServer(nil)
		base := "http://" + server.Listener.Addr().String()
		endpoint, e := url.Parse(base)
		Expect(e).NotTo(HaveOccurred())
		handler, e := httpauth.New(f.auth, httpauth.Policy{Hosts: []string{endpoint.Host}, Origins: []string{base}}, v1.New(query.New(observed, f.auth, f.store), nil, nil))
		Expect(e).NotTo(HaveOccurred())
		server.Config.Handler = handler
		server.Start()
		_, flags := callerProfile(GinkgoT(), f.sel, f.session, token, server.URL)
		raw := cli(GinkgoT(), 3, append([]string{"search", "--query", "needle"}, flags...)...)
		server.Close()
		Expect(changed).To(BeTrue(), change)
		Expect(string(raw)).NotTo(ContainSubstring("SERVICE_RECOVERING"))
	}
})
