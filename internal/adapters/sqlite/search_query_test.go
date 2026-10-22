package sqlite

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "haeryong.local/haeryong/internal/adapters/http/v1"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/search"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

func searchID() string { id, e := service.NewID(); Expect(e).NotTo(HaveOccurred()); return id }
func seedSearchTree(s *Store, project, doc, base string, files map[string]string, metadata ...func(app.WriteTx, string) error) (string, string, *searchBytes) {
	v, set := searchID(), searchID()
	b := &searchBytes{files: map[string]string{}}
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if doc == "" {
			doc = searchID()
			if e := tx.CreateDocument(domain.Document{ID: doc, ProjectID: project, Title: "Control"}); e != nil {
				return e
			}
		}
		if e := tx.CreateContentSet(set, digest([]byte(set))); e != nil {
			return e
		}
		for path, text := range files {
			hash := digest([]byte(text))
			b.files[hash] = text
			if e := tx.PutDurableObject(domain.Object{Hash: hash, Size: int64(len(text)), State: "ready"}); e != nil {
				return e
			}
			if e := tx.PutEntry(domain.Entry{ContentSetID: set, Path: path, Kind: "file", Hash: hash, Size: int64(len(text))}); e != nil {
				return e
			}
		}
		if e := tx.SealContentSet(set); e != nil {
			return e
		}
		if e := tx.PutVersion(domain.Version{ID: v, DocumentID: doc, BaseVersionID: base, ContentSetID: set, Title: "Control", ViewerProfile: "html_js_v1", SourceBasis: "unverified"}); e != nil {
			return e
		}
		for path := range files {
			if strings.HasSuffix(path, ".html") {
				if e := tx.PutPage(domain.Page{VersionID: v, Path: path}); e != nil {
					return e
				}
			}
		}
		for _, apply := range metadata {
			if e := apply(tx, v); e != nil {
				return e
			}
		}
		d, e := tx.Document(doc)
		if e != nil {
			return e
		}
		return tx.PublishVersion(v, d.Revision)
	})).To(Succeed())
	return v, doc, b
}
func finishSearch(s *Store, v string, b *searchBytes) {
	var index domain.SearchIndex
	Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; index, e = tx.SearchIndex(v); return e })).To(Succeed())
	Expect(driveSearch(s, search.NewWorker(s, b), index.JobID).State).To(Equal("completed"))
}
func searchToken(s *Store, scope domain.CredentialScope) string {
	token, e := sec.Secret()
	Expect(e).NotTo(HaveOccurred())
	id := searchID()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		a, e := tx.AuthState()
		if e != nil {
			return e
		}
		if e = tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: id, BindingID: id, Active: true, Scope: scope, Revision: 1}); e != nil {
			return e
		}
		permissions := map[string]int64{}
		var config int64
		for _, p := range scope.ProjectIDs {
			x, e := tx.Scope(p)
			if e != nil {
				return e
			}
			permissions[p] = x.Project.PermissionsRevision
			config = x.ConfigRevision
		}
		if config == 0 {
			c, e := tx.Configuration()
			if e != nil {
				return e
			}
			config = c.Revision
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: id, PrincipalID: id, TokenHash: sec.Verifier("workflow", token), AuthEpoch: a.AuthEpoch, Scope: scope, Permissions: permissions, ConfigRevision: config, CreatedAt: time.Now().UTC().Truncate(time.Millisecond), Revision: 1})
	})).To(Succeed())
	return token
}
func searchFixture() (*Store, *appauth.Service, string, string) {
	s, a, _, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), a, "search owner")
	h, e := s.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	return s, a, owner.Token, h.Library.UnfiledProjectID
}
func runSearch(q *query.Service, token, text, kind string) query.SearchPage {
	out, e := q.Search(ctx, token, query.SearchOptions{Options: query.Options{Kind: kind}, Text: text})
	Expect(e).NotTo(HaveOccurred())
	return out
}
func paths(hits []query.SearchHit) []string {
	out := []string{}
	for _, h := range hits {
		if h.PagePath != nil {
			out = append(out, *h.PagePath)
		}
	}
	return out
}

var _ = Describe("authenticated static search", Label("persistence"), func() {
	It("SC01 component_precedes_substring retains both candidates in fixed z,a order", func() {
		s, a, owner, p := searchFixture()
		v, _, b := seedSearchTree(s, p, "", "", map[string]string{"z.html": "<p>readSource</p>", "a.html": "<p>resourceful</p>"})
		finishSearch(s, v, b)
		result := runSearch(query.New(s, a, nil), owner, "source", "page")
		Expect(paths(result.Items)).To(Equal([]string{"z.html", "a.html"}))
		Expect(result.TotalMatches).To(Equal("2"))
	})
	It("SC02 literal expansion boundary survives exact overlap and rejects alias-only phrases", func() {
		s, a, owner, p := searchFixture()
		max := []string{"a" + strings.Repeat("ß", 2047) + "z", "b" + strings.Repeat("ﬃ", 1364) + "xyz"}
		files := map[string]string{}
		for i, phrase := range max {
			files[fmt.Sprintf("boundary%d.html", i)] = "<p>" + strings.Repeat("x", 28673) + phrase + "</p>"
			files[fmt.Sprintf("inside%d.html", i)] = "<p>" + phrase + "</p>"
		}
		files["alias.html"] = "<p>readSource source/path.go</p>"
		files["wide.html"] = "<p>left " + strings.Repeat("p", 70000) + " right</p>"
		v, _, b := seedSearchTree(s, p, "", "", files)
		finishSearch(s, v, b)
		q := query.New(s, a, nil)
		for i, phrase := range max {
			result := runSearch(q, owner, `"`+phrase+`"`, "page")
			Expect(paths(result.Items)).To(ConsistOf(fmt.Sprintf("boundary%d.html", i), fmt.Sprintf("inside%d.html", i)))
		}
		Expect(runSearch(q, owner, "left right", "page").Items).To(HaveLen(1))
		Expect(runSearch(q, owner, `"left right"`, "page").Items).To(BeEmpty())
		Expect(runSearch(q, owner, "readSource absentTerm", "page").Items).To(BeEmpty())
		Expect(runSearch(q, owner, `"read source"`, "page").Items).To(BeEmpty())
		Expect(runSearch(q, owner, `"source-path.go"`, "page").Items).To(BeEmpty())
		_, e := q.Search(ctx, owner, query.SearchOptions{Text: `"a` + strings.Repeat("ß", 3000) + `z"`})
		Expect(e).To(MatchError(ContainSubstring("query_too_long")))
	})
	It("SC04 Check-equivalent owner and scoped GET/HEAD matrix uses version.read alone", func() {
		s, a, owner, p := searchFixture()
		v, _, b := seedSearchTree(s, p, "", "", map[string]string{"a.html": "<p>canary 검 source</p>"})
		finishSearch(s, v, b)
		q := query.New(s, a, nil)
		handler := v1.New(q, nil, nil)
		for _, scope := range []domain.CredentialScope{
			{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}, VersionIDs: []string{v}},
			{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}},
			{Kind: "workflow", Actions: []string{"version.read"}, VersionIDs: []string{v}},
			{Kind: "workflow", Actions: []string{"project.read"}, ProjectIDs: []string{p}, VersionIDs: []string{v}},
			{Kind: "workflow", Actions: []string{"source.read"}, ProjectIDs: []string{p}, VersionIDs: []string{v}},
			{Kind: "workflow", ProjectIDs: []string{p}, VersionIDs: []string{v}},
			{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}, VersionIDs: []string{searchID()}},
		} {
			token := searchToken(s, scope)
			eligible := false
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				_, e := source.Check(tx, domain.Session{Scope: scope}, v)
				eligible = e == nil
				return nil
			})).To(Succeed())
			for _, method := range []string{"GET", "HEAD"} {
				r := httptest.NewRequest(method, "/api/v1/search?q=canary&kind=page", nil)
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				hasAction := false
				for _, action := range scope.Actions {
					hasAction = hasAction || action == "version.read"
				}
				if !hasAction {
					Expect(w.Code).To(Equal(403))
				} else {
					Expect(w.Code).To(Equal(200))
					if method == "GET" {
						var response struct {
							Data query.SearchPage `json:"data"`
						}
						Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
						Expect(len(response.Data.Items) > 0).To(Equal(eligible))
						if !eligible {
							Expect(response.Data.Index.VersionsTotal).To(Equal("0"))
						}
					}
					if method == "HEAD" {
						Expect(w.Body.Len()).To(BeZero())
						Expect(w.Header().Get("Content-Length")).NotTo(BeEmpty())
					}
				}
			}
		}
		versionOnly := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, VersionIDs: []string{v}})
		for _, method := range []string{"GET", "HEAD"} {
			request := httptest.NewRequest(method, "/api/v1/search?q=canary&project_id="+p, nil)
			request.Header.Set("Authorization", "Bearer "+versionOnly)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			Expect(response.Code).To(Equal(404))
			if method == "HEAD" {
				Expect(response.Body.Len()).To(BeZero())
			}
		}
		Expect(runSearch(q, owner, "canary", "page").Items).To(HaveLen(1))
	})
	It("SC05 SC15 filters FTS fallback coverage snippets and cursors before LIMIT and restores exact identities", func() {
		s, a, owner, p := searchFixture()
		hidden := searchID()
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.CreateProject(domain.Project{ID: hidden, Name: "hidden"}) })).To(Succeed())
		v, doc, b := seedSearchTree(s, p, "", "", map[string]string{"z.html": "검토 source safe", "a.html": "검토 source safe"})
		finishSearch(s, v, b)
		hv, _, hb := seedSearchTree(s, hidden, "", "", map[string]string{"hidden.html": "검토 source F10canary"})
		finishSearch(s, hv, hb)
		tv, td, tb := seedSearchTree(s, p, "", "", map[string]string{"trash.html": "검토 source TrashCanary"})
		finishSearch(s, tv, tb)
		_, e := s.db.Exec(`UPDATE documents SET lifecycle='trash' WHERE document_id=?`, td)
		Expect(e).NotTo(HaveOccurred())
		Expect(searchCount(s, `SELECT count(*) FROM search_words WHERE search_words MATCH '"F10canary"'`)).To(BeNumerically(">", 0))
		Expect(searchCount(s, `SELECT count(*) FROM search_words WHERE search_words MATCH '"검"'`)).To(BeZero())
		Expect(searchCount(s, `SELECT count(*) FROM search_grams WHERE search_grams MATCH '"검"'`)).To(BeZero())
		token := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}, VersionIDs: []string{v, tv, hv}})
		q := query.New(s, a, nil)
		for _, term := range []string{"검", "source"} {
			result, e := q.Search(ctx, token, query.SearchOptions{Options: query.Options{Kind: "page", Limit: 1, Lifecycle: "all"}, Text: term})
			Expect(e).NotTo(HaveOccurred())
			Expect(paths(result.Items)).To(Equal([]string{"a.html"}))
			Expect(result.TotalMatches).To(Equal("2"))
			Expect(result.Index.VersionsTotal).To(Equal("1"))
			Expect(result.NextCursor).NotTo(BeNil())
			raw, e := base64.RawURLEncoding.DecodeString(*result.NextCursor)
			Expect(e).NotTo(HaveOccurred())
			Expect(string(raw)).NotTo(ContainSubstring(hv))
			Expect(string(raw)).NotTo(ContainSubstring("F10"))
			Expect(fmt.Sprint(result)).NotTo(ContainSubstring("Canary"))
		}
		_, e = s.db.Exec(`UPDATE documents SET lifecycle='purged' WHERE document_id=?`, td)
		Expect(e).NotTo(HaveOccurred())
		purged, e := q.Search(ctx, token, query.SearchOptions{Options: query.Options{Kind: "page", Lifecycle: "all"}, Text: "source"})
		Expect(e).NotTo(HaveOccurred())
		Expect(purged.TotalMatches).To(Equal("2"))
		Expect(purged.Index.VersionsTotal).To(Equal("1"))
		_, e = s.db.Exec(`UPDATE documents SET lifecycle='archived' WHERE document_id=?`, doc)
		Expect(e).NotTo(HaveOccurred())
		Expect(runSearch(q, owner, "source", "page").Index.VersionsTotal).To(Equal("1")) // hidden project remains owner-readable
		archived, e := q.Search(ctx, token, query.SearchOptions{Options: query.Options{Lifecycle: "archived", Kind: "page"}, Text: "source"})
		Expect(e).NotTo(HaveOccurred())
		Expect(paths(archived.Items)).To(Equal([]string{"a.html", "z.html"}))
		_, e = s.db.Exec(`UPDATE documents SET lifecycle='active' WHERE document_id=?`, td)
		Expect(e).NotTo(HaveOccurred())
		Expect(paths(runSearch(q, token, "TrashCanary", "page").Items)).To(Equal([]string{"trash.html"}))
	})
	It("SC06 hidden same-document publication preserves all cursor while visible completion invalidates", func() {
		s, a, owner, p := searchFixture()
		v, doc, b := seedSearchTree(s, p, "", "", map[string]string{"a.html": "needle", "b.html": "needle", "z.html": "needle"})
		finishSearch(s, v, b)
		token := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}, VersionIDs: []string{v}})
		q := query.New(s, a, nil)
		o := query.SearchOptions{Options: query.Options{Kind: "page", Limit: 1}, Text: "needle", Versions: "all"}
		first, e := q.Search(ctx, token, o)
		Expect(e).NotTo(HaveOccurred())
		Expect(paths(first.Items)).To(Equal([]string{"a.html"}))
		Expect(first.NextCursor).NotTo(BeNil())
		ownerFirst, e := q.Search(ctx, owner, o)
		Expect(e).NotTo(HaveOccurred())
		hidden, _, hb := seedSearchTree(s, p, doc, v, map[string]string{"hidden.html": "needle hidden"})
		finishSearch(s, hidden, hb)
		o.Cursor = *first.NextCursor
		next, e := q.Search(ctx, token, o)
		Expect(e).NotTo(HaveOccurred())
		Expect(paths(next.Items)).To(Equal([]string{"b.html"}))
		Expect(next.NextCursor).NotTo(BeNil())
		tailOptions := o
		tailOptions.Cursor = *next.NextCursor
		tail, e := q.Search(ctx, token, tailOptions)
		Expect(e).NotTo(HaveOccurred())
		Expect(paths(tail.Items)).To(Equal([]string{"z.html"}))
		Expect(next.Index.VersionsTotal).To(Equal("1"))
		o.Cursor = *ownerFirst.NextCursor
		_, e = q.Search(ctx, owner, o)
		Expect(e).To(MatchError(ContainSubstring("cursor_mismatch")))
		Expect(runSearch(q, token, "needle", "page").Items).To(BeEmpty()) // actual latest is denied; no fallback
		// A visible pending page completes: the earlier visible view must reject its cursor.
		_, e = s.db.Exec(`UPDATE search_units SET body_state='pending' WHERE version_id=? AND page_path='z.html'`, v)
		Expect(e).NotTo(HaveOccurred())
		o.Cursor = ""
		pending, e := q.Search(ctx, token, o)
		Expect(e).NotTo(HaveOccurred())
		Expect(pending.NextCursor).NotTo(BeNil())
		Expect(pending.TotalMatches).To(Equal("2"))
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			unit, e := tx.NextSearchPage(v)
			if e != nil {
				return e
			}
			if e = tx.CompleteSearchPage(unit.ID); e != nil {
				return e
			}
			index, e := tx.SearchIndex(v)
			if e != nil {
				return e
			}
			index.Revision++
			return tx.PutSearchIndex(index)
		})).To(Succeed())
		o.Cursor = *pending.NextCursor
		_, e = q.Search(ctx, token, o)
		Expect(e).To(MatchError(ContainSubstring("cursor_mismatch")))
		o.Cursor = ""
		o.Limit = 200
		fresh, e := q.Search(ctx, token, o)
		Expect(e).NotTo(HaveOccurred())
		Expect(paths(fresh.Items)).To(Equal([]string{"a.html", "b.html", "z.html"}))
	})
	It("SC09 SC10 SC13 preserves exact Unicode page-local regions attachment paths and ranked restart", func() {
		s, a, owner, p := searchFixture()
		v, _, b := seedSearchTree(s, p, "", "", map[string]string{"pages/cafe\u0301.html": "<p id='retry' data-haeryong-region='retry'>선택적 재시도 selectiveRetry retry_failed_roles retry.go</p>", "pages/café.html": "<p id='retry'>작업 승인 부분 실패</p><p id='dup'>first</p><p id='dup'>second</p>", "opaque.unknown": "not indexed"}, func(tx app.WriteTx, v string) error {
			if e := tx.PutRegion(domain.Region{VersionID: v, PagePath: "pages/café.html", ID: "runtime-only", Metadata: []byte(`{"label":"RuntimeLabel"}`)}); e != nil {
				return e
			}
			return tx.PutSource(domain.Source{VersionID: v, ID: "logical-source", Metadata: []byte(`{"kind":"git","repo_id":"logicalRepo","exact_revision":"revisionOnly","root_path":"PrivateRootCanary"}`)})
		})
		finishSearch(s, v, b)
		q := query.New(s, a, nil)
		Expect(paths(runSearch(q, owner, "retry", "region").Items)).To(ConsistOf("pages/cafe\u0301.html", "pages/café.html"))
		Expect(runSearch(q, owner, "RuntimeLabel", "region").Items[0].AnchorState).To(Equal("missing"))
		Expect(runSearch(q, owner, "dup", "region").Items).To(BeEmpty())
		Expect(paths(runSearch(q, owner, "opaque.unknown", "attachment").Items)).To(Equal([]string{"opaque.unknown"}))
		for _, term := range []string{"selectiveRetry", "retry_failed_roles", "retry.go", "\"선택적 재시도\"", "작업 승인", "부분 실패"} {
			Expect(runSearch(q, owner, term, "").Items).NotTo(BeEmpty())
		}
		Expect(runSearch(q, owner, "logicalRepo", "source").Items).To(HaveLen(1))
		Expect(runSearch(q, owner, "revisionOnly", "source").Items).To(HaveLen(1))
		Expect(runSearch(q, owner, "PrivateRootCanary", "").Items).To(BeEmpty())
		first := runSearch(q, owner, "café.html", "page")
		Expect(paths(first.Items)).To(ConsistOf("pages/cafe\u0301.html", "pages/café.html"))
		// Reopen actual migrated DB with nonempty FTS postings, then query the same rank/identity.
		Expect(s.Close()).To(Succeed())
		next := New(nil)
		_, closeNext, e := next.Prepare(ctx, s.profile)
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(closeNext)
		q = query.New(next, appauth.New(next), nil)
		Expect(runSearch(q, owner, "café.html", "page").Items).To(Equal(first.Items))
	})
	It("SC14 search_reasons_visible_only counts distinct eligible versions and hides diagnostic jobs", func() {
		s, a, owner, p := searchFixture()
		v, doc, b := seedSearchTree(s, p, "", "", map[string]string{"a.html": "needle", "b.html": "needle", "c.html": "needle", "d.html": "needle"})
		finishSearch(s, v, b)
		hidden, _, hb := seedSearchTree(s, p, doc, v, map[string]string{"a.html": "hidden"})
		finishSearch(s, hidden, hb)
		_, e := s.db.Exec(`UPDATE search_units SET body_state='failed',reason='read_failed' WHERE version_id=? AND kind='page' AND page_path IN ('c.html','d.html')`, v)
		Expect(e).NotTo(HaveOccurred())
		_, e = s.db.Exec(`UPDATE search_indexes SET state='partial',reason=CASE WHEN version_id=? THEN 'storage_failed' ELSE '' END WHERE version_id IN (?,?)`, hidden, v, hidden)
		Expect(e).NotTo(HaveOccurred())
		otherProject := searchID()
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			return tx.CreateProject(domain.Project{ID: otherProject, Name: "Reason excluded project"})
		})).To(Succeed())
		other, _, ob := seedSearchTree(s, otherProject, "", "", map[string]string{"a.html": "needle"})
		finishSearch(s, other, ob)
		_, e = s.db.Exec(`UPDATE search_indexes SET state='partial',reason='decode_failed' WHERE version_id=?`, other)
		Expect(e).NotTo(HaveOccurred())
		_, e = s.db.Exec(`UPDATE jobs SET error='PrivateDiagnosticCanary' WHERE job_id=(SELECT job_id FROM search_indexes WHERE version_id=?)`, hidden)
		Expect(e).NotTo(HaveOccurred())
		token := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}, VersionIDs: []string{v}})
		q := query.New(s, a, nil)
		o := query.SearchOptions{Options: query.Options{Project: p, Kind: "page", Limit: 1}, Versions: "all", Text: "needle"}
		scoped, e := q.Search(ctx, token, o)
		Expect(e).NotTo(HaveOccurred())
		Expect(scoped.Index.Reasons).To(Equal([]query.IndexReason{{Code: "read_failed", VersionCount: "1"}}))
		Expect(scoped.NextCursor).NotTo(BeNil())
		own, e := q.Search(ctx, owner, o)
		Expect(e).NotTo(HaveOccurred())
		Expect(own.Index.Reasons).To(Equal([]query.IndexReason{{Code: "read_failed", VersionCount: "1"}, {Code: "storage_failed", VersionCount: "1"}}))
		Expect(fmt.Sprint(own)).NotTo(ContainSubstring("PrivateDiagnosticCanary"))
		handler := v1.New(q, nil, nil)
		for _, reader := range []struct {
			token    string
			expected query.SearchPage
		}{{owner, own}, {token, scoped}} {
			request := httptest.NewRequest("GET", "/api/v1/search?"+url.Values{"q": {"needle"}, "project_id": {p}, "versions": {"all"}, "kind": {"page"}, "limit": {"1"}}.Encode(), nil)
			request.Header.Set("Authorization", "Bearer "+reader.token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			Expect(response.Code).To(Equal(200))
			var body struct {
				Data query.SearchPage `json:"data"`
			}
			Expect(json.Unmarshal(response.Body.Bytes(), &body)).To(Succeed())
			Expect(body.Data).To(Equal(reader.expected))
		}

		_, e = s.db.Exec(`UPDATE search_indexes SET reason='decode_failed',revision=revision+1 WHERE version_id=?`, hidden)
		Expect(e).NotTo(HaveOccurred())
		o.Cursor = *scoped.NextCursor
		next, e := q.Search(ctx, token, o)
		Expect(e).NotTo(HaveOccurred())
		Expect(paths(next.Items)).To(Equal([]string{"b.html"}))
		Expect(next.Index.Reasons).To(Equal(scoped.Index.Reasons))
		o.Cursor = ""
		o.Text = ""
		empty, e := q.Search(ctx, token, o)
		Expect(e).NotTo(HaveOccurred())
		Expect(empty.Index.Reasons).To(Equal(scoped.Index.Reasons))
	})
})

func calibrationPass(count int) bool { return count >= 36 }

var _ = Describe("search frozen relevance worksheets", Label("persistence"), func() {
	It("SC01 SC03 runs every frozen X09 row through the real service and records R/L outcomes", func() {
		s, a, _, p := searchFixture()
		raw, e := os.ReadFile("../../../tests/fixtures/search/x09.json")
		Expect(e).NotTo(HaveOccurred())
		var sheet struct {
			Bodies []string
			Rows   []struct {
				Q           string
				Only        int
				Expected    []int
				Disposition string
			}
		}
		Expect(json.Unmarshal(raw, &sheet)).To(Succeed())
		ids := []string{}
		documents := []string{}
		byVersion := map[string]int{}
		for i, body := range sheet.Bodies {
			v, d, b := seedSearchTree(s, p, "", "", map[string]string{"page.html": "<p>" + body + "</p>"})
			finishSearch(s, v, b)
			ids = append(ids, v)
			documents = append(documents, d)
			byVersion[v] = i + 1
		}
		_, e = s.db.Exec(`UPDATE documents SET lifecycle='trash' WHERE document_id=?`, documents[6])
		Expect(e).NotTo(HaveOccurred())
		allowed := append(append([]string{}, ids[:5]...), ids[6:]...)
		AddReportEntry("X09_prequery_oracle", ReportEntryVisibilityFailureOrVerbose, map[string]any{"worksheet_sha256": digest(raw), "versions_in_row_order": ids, "documents_in_row_order": documents, "project": p})
		q := query.New(s, a, nil)
		observed := []any{}
		for _, row := range sheet.Rows {
			selected := allowed
			if row.Only > 0 {
				selected = []string{ids[row.Only-1]}
			}
			token := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}, VersionIDs: selected})
			result := runSearch(q, token, row.Q, "page")
			got := []int{}
			for _, h := range result.Items {
				got = append(got, byVersion[h.VersionID])
			}
			Expect(got).To(ConsistOf(row.Expected), row.Q)
			observed = append(observed, map[string]any{"q": row.Q, "rows": got, "disposition": row.Disposition, "misses": []string{}})
		}
		AddReportEntry("X09", ReportEntryVisibilityFailureOrVerbose, map[string]any{"worksheet_sha256": digest(raw), "rows": observed, "passed": len(sheet.Rows), "misses": 0, "scope": "seeded production extraction/service/SQLite; owner use unmeasured"})
	})
	It("SC03 calibration40 uses all 1000 documents 5000 versions and rejects the 35/40 negative control", func() {
		s, a, _, p := searchFixture()
		raw, e := os.ReadFile("../../../tests/contracts/calibration-plan.json")
		Expect(e).NotTo(HaveOccurred())
		var plan struct {
			Dataset struct {
				Topics []struct{ Korean, Code string }
			}
			Queries []struct {
				ID, Query string
				Relevant  []int `json:"relevant_topics"`
			}
		}
		Expect(json.Unmarshal(raw, &plan)).To(Succeed())
		Expect(plan.Queries).To(HaveLen(40))
		Expect(plan.Dataset.Topics).To(HaveLen(20))
		Expect(calibrationPass(35)).To(BeFalse())
		allowed := []string{}
		topicByVersion := map[string]int{}
		rowByVersion := map[string]int{}
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			for id := 1; id <= 1000; id++ {
				topic := (id - 1) % 20
				t := plan.Dataset.Topics[topic]
				doc, set := searchID(), searchID()
				title := t.Korean + " " + t.Code + fmt.Sprintf(" explanation %d", id)
				if e := tx.CreateDocument(domain.Document{ID: doc, ProjectID: p, Title: title}); e != nil {
					return e
				}
				if e := tx.CreateContentSet(set, digest([]byte(set))); e != nil {
					return e
				}
				if e := tx.SealContentSet(set); e != nil {
					return e
				}
				base := ""
				for sequence := 1; sequence <= 5; sequence++ {
					v := searchID()
					if e := tx.PutVersion(domain.Version{ID: v, DocumentID: doc, BaseVersionID: base, ContentSetID: set, Title: title, ViewerProfile: "html_js_v1", SourceBasis: "unverified"}); e != nil {
						return e
					}
					d, e := tx.Document(doc)
					if e != nil {
						return e
					}
					if e = tx.PublishVersion(v, d.Revision); e != nil {
						return e
					}
					var index domain.SearchIndex
					index, e = tx.SearchIndex(v)
					if e != nil {
						return e
					}
					index.State = "complete"
					index.DiscoveryComplete = true
					if e = tx.PutSearchIndex(index); e != nil {
						return e
					}
					unit, e := tx.PutSearchUnit(domain.SearchUnit{VersionID: v, Kind: "document", Title: title, State: "ready", BodyState: "complete", AnchorState: "not_applicable"})
					if e != nil {
						return e
					}
					if e = tx.PutSearchChunk(domain.SearchChunk{UnitID: unit, Part: "metadata", Title: search.Normalize(title), Terms: search.Normalize(title), DisplayBody: title}); e != nil {
						return e
					}
					body := title + fmt.Sprintf(" source-controlled synthetic version%d: exact source, immutable evidence, unavailable scope.", sequence)
					if e = tx.PutSearchChunk(domain.SearchChunk{UnitID: unit, Part: "body", Title: search.Normalize(title), Body: search.Normalize(body), Terms: strings.Join(search.GenerateIdentifierAliases(body), " "), DisplayBody: body}); e != nil {
						return e
					}
					if id%17 != 0 {
						allowed = append(allowed, v)
					}
					topicByVersion[v] = topic
					rowByVersion[v] = id
					base = v
				}
				if id%19 == 0 {
					if e := tx.(*writeTx).exec(`UPDATE documents SET lifecycle='trash' WHERE document_id=?`, doc); e != nil {
						return e
					}
				}
			}
			return nil
		})).To(Succeed())
		Expect(searchCount(s, `SELECT count(*) FROM documents`)).To(Equal(1000))
		Expect(searchCount(s, `SELECT count(*) FROM document_versions`)).To(Equal(5000))
		token := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}, VersionIDs: allowed})
		AddReportEntry("calibration_prequery_oracle", ReportEntryVisibilityFailureOrVerbose, map[string]any{"plan_sha256": digest(raw), "version_topic": topicByVersion, "version_document_row": rowByVersion, "project": p})
		q := query.New(s, a, nil)
		passed := 0
		outcomes := []any{}
		for _, row := range plan.Queries {
			start := time.Now()
			result, e := q.Search(ctx, token, query.SearchOptions{Options: query.Options{Kind: "document", Limit: 200}, Text: row.Query})
			Expect(e).NotTo(HaveOccurred())
			first := 0
			top := []any{}
			relevant := 0
			falsePositives := 0
			for i, h := range result.Items {
				topic := topicByVersion[h.VersionID]
				match := false
				for _, t := range row.Relevant {
					match = match || topic == t
				}
				if match {
					relevant++
					if first == 0 {
						first = i + 1
					}
				} else {
					falsePositives++
				}
				if i < 5 {
					top = append(top, map[string]any{"project": h.ProjectID, "document": h.DocumentID, "version": h.VersionID, "sequence": h.Sequence, "kind": h.Kind, "topic": topic, "row": rowByVersion[h.VersionID]})
				}
				Expect(rowByVersion[h.VersionID] % 17).NotTo(BeZero())
				Expect(rowByVersion[h.VersionID] % 19).NotTo(BeZero())
				Expect(h.Sequence).To(Equal(int64(5)))
			}
			if first > 0 && first <= 5 {
				passed++
			}
			Expect(first).To(BeNumerically(">", 0), row.ID+" exact target must remain findable")
			outcomes = append(outcomes, map[string]any{"id": row.ID, "top5": top, "first_relevant_rank": first, "relevant_returned": relevant, "total_matches": result.TotalMatches, "false_positives_returned": falsePositives, "latency_ms": float64(time.Since(start).Microseconds()) / 1000, "disposition": "R", "miss": first == 0 || first > 5})
		}
		AddReportEntry("calibration40", ReportEntryVisibilityFailureOrVerbose, map[string]any{"plan_sha256": digest(raw), "documents": 1000, "versions": 5000, "passed": passed, "rows": outcomes, "scope": "seeded production query/SQLite, not public imports or owner usefulness"})
		Expect(calibrationPass(passed)).To(BeTrue())
	})
})

type failedSearchInput struct {
	*strings.Reader
	remaining int
}

func (r *failedSearchInput) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	n, e := r.Reader.Read(p)
	r.remaining -= n
	return n, e
}

type failingSearchBytes struct{ body string }

func (b failingSearchBytes) Read(_ context.Context, _ domain.Object, _ domain.Pin, f func(io.ReadSeeker) error) error {
	return f(&failedSearchInput{strings.NewReader(b.body), 65536})
}

type timedSearchStore struct {
	app.Store
	durations []float64
	budgets   []searchWriteBudget
	labels    []map[string]any
}

func (s *timedSearchStore) Write(ctx context.Context, f func(app.WriteTx) error) error {
	start := time.Now()
	label := "ledger"
	budget := searchWriteBudget{}
	e := s.Store.Write(ctx, func(tx app.WriteTx) error { return f(timedSearchTx{WriteTx: tx, label: &label, budget: &budget}) })
	s.budgets = append(s.budgets, budget)
	elapsed := float64(time.Since(start).Microseconds()) / 1000
	s.durations = append(s.durations, elapsed)
	if elapsed > 100 {
		s.labels = append(s.labels, map[string]any{"operation": label, "ms": elapsed})
	}
	return e
}

type searchWriteBudget struct{ postings, bytes, merges, pages, repeatedIndexed int }

type timedSearchTx struct {
	app.WriteTx
	label  *string
	budget *searchWriteBudget
}

func (t timedSearchTx) PutSearchChunk(c domain.SearchChunk) error {
	*t.label = fmt.Sprintf("chunk %s/%d body=%d terms=%d", c.Part, c.Ordinal, len(c.Body), len(c.Terms))
	t.budget.postings++
	t.budget.bytes += len(c.Title) + len(c.Path) + len(c.Terms) + len(c.Body) + len(c.DisplayBody)
	if c.Part == "body" && len(c.DisplayBody) >= 4096 && strings.Trim(c.DisplayBody, "a") == "" {
		t.budget.repeatedIndexed = len(c.Body) + len(c.Terms)
	}
	return t.WriteTx.PutSearchChunk(c)
}

func (t timedSearchTx) MergeSearch(table, pages int) error {
	*t.label = fmt.Sprintf("merge table=%d requested_pages=%d", table, pages)
	t.budget.merges++
	t.budget.pages += pages
	return t.WriteTx.MergeSearch(table, pages)
}

var _ = Describe("search completeness and lock probes", Label("persistence"), func() {
	It("SC16 a read failure before real EOF keeps large body unsearchable and coverage incomplete", func() {
		s, a, owner, p := searchFixture()
		body := "<p>" + strings.Repeat("padding ", 6*1024*1024) + "searchLargeFinalTailOnly</p>"
		v, _, _ := seedSearchTree(s, p, "", "", map[string]string{"large.html": body})
		var index domain.SearchIndex
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; index, e = tx.SearchIndex(v); return e })).To(Succeed())
		w := search.NewWorker(s, failingSearchBytes{body})
		for turn := 0; turn < 30; turn++ {
			j, e := s.Job(ctx, index.JobID)
			Expect(e).NotTo(HaveOccurred())
			if terminal(j.State) {
				Expect(j.State).To(Equal("failed"))
				break
			}
			e = w.Process(ctx, index.JobID)
			if e != nil {
				Expect(errors.Is(e, io.ErrUnexpectedEOF)).To(BeTrue())
			}
		}
		j, e := s.Job(ctx, index.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.State).To(Equal("failed"))
		result := runSearch(query.New(s, a, nil), owner, "padding", "page")
		Expect(result.Items).To(BeEmpty())
		Expect(result.Index.State).To(Equal("failed"))
		Expect(result.Index.HTMLTotal).To(Equal("1"))
		Expect(result.Index.HTMLComplete).To(Equal("0"))
		Expect(result.Index.HTMLFailed).To(Equal("1"))
		Expect(result.Index.HTMLPending).To(Equal("0"))
		Expect(result.Index.Reasons).To(Equal([]query.IndexReason{{Code: "read_failed", VersionCount: "1"}}))
		metadata := runSearch(query.New(s, a, nil), owner, "large.html", "page")
		Expect(metadata.Items).To(HaveLen(1))
		Expect(metadata.Items[0].BodyState).To(Equal("failed"))
		var failedUnit domain.SearchUnit
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			var e error
			failedUnit, e = tx.NextSearchPage(v)
			Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue())
			// A terminal attempt cannot write a page failure again.
			return tx.FailSearchPage(index.JobID, j.AttemptGeneration, failedUnit.ID, "storage_failed")
		})).To(MatchError(domain.ErrStaleWorker))
	})
	It("SC20 records prepared write durations and permits search while verified object consumption runs", func() {
		s, a, owner, p := searchFixture()
		v, _, b := seedSearchTree(s, p, "", "", map[string]string{"first.html": "<p>first ready</p>", "large.html": "<p>" + strings.Repeat("a", 40*1024*1024) + "</p>"})
		observed := &searchObserved{Store: s}
		timed := &timedSearchStore{Store: observed}
		w := search.NewWorker(timed, b)
		q := query.New(s, a, nil)
		var index domain.SearchIndex
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; index, e = tx.SearchIndex(v); return e })).To(Succeed())
		observed.afterBody = func() {
			result := runSearch(q, owner, "first.html", "page")
			Expect(result.Index.HTMLTotal).To(Equal("2"))
			Expect(result.Index.HTMLComplete).NotTo(Equal("2"))
		}
		start := time.Now()
		Expect(driveSearch(s, w, index.JobID).State).To(Equal("completed"))
		sort.Float64s(timed.durations)
		n := len(timed.durations)
		max := timed.durations[n-1]
		AddReportEntry("SC20_prepared_transactions", ReportEntryVisibilityFailureOrVerbose, map[string]any{"n": n, "p50_ms": timed.durations[n/2], "p95_ms": timed.durations[(n*95-1)/100], "max_ms": max, "slow_operations": timed.labels, "index_seconds": time.Since(start).Seconds(), "scope": "instrumented storage wrapper, seeded objects; public read/RSS/DB probes separate"})
		merges := 0
		for _, budget := range timed.budgets {
			Expect(budget.postings).To(BeNumerically("<=", 1))
			Expect(budget.repeatedIndexed).To(BeNumerically("<=", 64), "repeated source positions must not reappear in the FTS nomination payload")
			Expect(budget.bytes).To(BeNumerically("<=", 4194304))
			Expect(budget.merges).To(BeNumerically("<=", 1))
			Expect(budget.pages).To(BeNumerically("<=", 1))
			Expect(budget.postings*budget.merges).To(BeZero(), "posting and explicit merge must release the lock between them")
			merges += budget.merges
		}
		Expect(merges).To(BeNumerically(">", 0))
		for _, table := range []string{"search_words", "search_grams"} {
			for _, setting := range []struct {
				name  string
				value int
			}{{"automerge", 0}, {"crisismerge", 1000}, {"usermerge", 16}, {"pgsz", 1024}} {
				var value int
				Expect(s.db.QueryRow(`SELECT v FROM `+table+`_config WHERE k=?`, setting.name).Scan(&value)).To(Succeed())
				Expect(value).To(Equal(setting.value))
			}
		}
		slices := &sliceObserved{Store: s}
		shortStart := time.Now()
		result := runSearch(query.New(slices, a, nil), owner, "aa", "page")
		Expect(paths(result.Items)).To(Equal([]string{"large.html"}))
		Expect(slices.maxRows).To(BeNumerically("<=", 64))
		var count int
		Expect(s.db.QueryRow(`SELECT count(*) FROM search_chunks`).Scan(&count)).To(Succeed())
		// Elapsed-time yields may finish before the row ceiling. Every
		// slice must advance and the complete physical corpus must be visited.
		Expect(slices.slices).To(BeNumerically(">=", (count+63)/64))
		Expect(slices.slices).To(BeNumerically("<=", count))
		AddReportEntry("SC20_sliced_short_query", ReportEntryVisibilityFailureOrVerbose, map[string]any{"duration_ms": float64(time.Since(shortStart).Microseconds()) / 1000, "slices": slices.slices, "max_examined_rows": slices.maxRows, "merge_transactions": merges, "posting_byte_budget": 4194304, "requested_merge_pages_per_transaction": 1})
	})
})

var _ = It("SC13 query hides staged body candidates until the verified EOF commit and keeps metadata visible", Label("persistence"), func() {
	s, a, owner, p := searchFixture()
	v, _, b := seedSearchTree(s, p, "", "", map[string]string{"page.html": "<p>stagedneedle</p>"})
	q := query.New(s, a, nil)
	observed := &searchObserved{Store: s}
	called := false
	observed.afterBody = func() {
		called = true
		Expect(runSearch(q, owner, "stagedneedle", "page").Items).To(BeEmpty())
		Expect(runSearch(q, owner, "page.html", "page").Items).To(HaveLen(1))
	}
	var index domain.SearchIndex
	Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; index, e = tx.SearchIndex(v); return e })).To(Succeed())
	Expect(driveSearch(s, search.NewWorker(observed, b), index.JobID).State).To(Equal("completed"))
	Expect(called).To(BeTrue())
	Expect(runSearch(q, owner, "stagedneedle", "page").Items).To(HaveLen(1))
})

var _ = It("SC01 field tiers keep exact source IDs above title phrases and region/source labels in their own fields", Label("persistence"), func() {
	s, a, owner, p := searchFixture()
	v, _, b := seedSearchTree(s, p, "", "", map[string]string{"page.html": "<html><title>needle extra</title><section id='anchor'><h2>needle</h2>body</section></html>"}, func(tx app.WriteTx, v string) error {
		if e := tx.PutSource(domain.Source{VersionID: v, ID: "needle", Metadata: []byte(`{"kind":"git"}`)}); e != nil {
			return e
		}
		return tx.PutSource(domain.Source{VersionID: v, ID: "other", Metadata: []byte(`{"kind":"git","release_label":"needle"}`)})
	})
	finishSearch(s, v, b)
	result := runSearch(query.New(s, a, nil), owner, "needle", "")
	Expect(result.Items).To(HaveLen(4))
	kinds := []string{}
	for _, h := range result.Items {
		kinds = append(kinds, h.Kind)
	}
	Expect(kinds).To(Equal([]string{"source", "page", "region", "source"}))
	Expect(*result.Items[0].SourceID).To(Equal("needle"))
	Expect(*result.Items[2].RegionID).To(Equal("anchor"))
	Expect(*result.Items[3].SourceID).To(Equal("other"))
	Expect(result.Items[2].MatchFields).NotTo(ContainElement("title"))
	Expect(result.Items[3].MatchFields).To(Equal([]string{"source"}))
})
var _ = It("SC06 ready metadata content invalidates a cursor before the next inventory checkpoint", Label("persistence"), func() {
	s, a, owner, p := searchFixture()
	v, _, b := seedSearchTree(s, p, "", "", map[string]string{"a.html": "needle", "b.html": "needle", "c.html": "absent"})
	finishSearch(s, v, b)
	q := query.New(s, a, nil)
	o := query.SearchOptions{Options: query.Options{Kind: "page", Limit: 1}, Text: "needle"}
	first, e := q.Search(ctx, owner, o)
	Expect(e).NotTo(HaveOccurred())
	Expect(first.NextCursor).NotTo(BeNil())
	var unit int64
	Expect(s.db.QueryRow(`SELECT unit_id FROM search_units WHERE version_id=? AND page_path='c.html' AND kind='page'`, v).Scan(&unit)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.PutSearchChunk(domain.SearchChunk{UnitID: unit, Part: "metadata", Title: "c.html", Path: "c.html", Terms: "c.html c.html needle", DisplayBody: "c.html  c.html needle"})
	})).To(Succeed())
	o.Cursor = *first.NextCursor
	_, e = q.Search(ctx, owner, o)
	Expect(e).To(MatchError(ContainSubstring("cursor_mismatch")))
	o.Cursor = ""
	o.Limit = 200
	fresh, e := q.Search(ctx, owner, o)
	Expect(e).NotTo(HaveOccurred())
	Expect(paths(fresh.Items)).To(ConsistOf("a.html", "b.html", "c.html"))
	Expect(*fresh.Items[0].PagePath).To(Equal("c.html"))
})

var _ = It("SC01 overlapping windows never turn a suffix of resource into a source component", Label("persistence"), func() {
	s, a, owner, p := searchFixture()
	v, _, b := seedSearchTree(s, p, "", "", map[string]string{"a.html": "<p>" + strings.Repeat("p", 28670) + " resource " + strings.Repeat("p", 10000) + "</p>", "z.html": "<p>readSource</p>"})
	finishSearch(s, v, b)
	result := runSearch(query.New(s, a, nil), owner, "source", "page")
	Expect(paths(result.Items)).To(Equal([]string{"z.html", "a.html"}))
	Expect(result.TotalMatches).To(Equal("2"))
})

var _ = It("SC08 closed derived admission rejects visible future enums while hidden future rows preserve cursors", Label("persistence"), func() {
	s, a, owner, p := searchFixture()
	v, _, b := seedSearchTree(s, p, "", "", map[string]string{"a.html": "needle", "b.html": "needle"})
	finishSearch(s, v, b)
	hidden, _, hb := seedSearchTree(s, p, "", "", map[string]string{"future.html": "needle"})
	finishSearch(s, hidden, hb)
	token := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{p}, VersionIDs: []string{v}})
	q := query.New(s, a, nil)
	o := query.SearchOptions{Options: query.Options{Kind: "page", Limit: 1}, Text: "needle"}
	first, e := q.Search(ctx, token, o)
	Expect(e).NotTo(HaveOccurred())
	Expect(first.NextCursor).NotTo(BeNil())
	_, e = s.db.Exec(`UPDATE search_units SET kind='future_kind' WHERE version_id=? AND kind='page'`, hidden)
	Expect(e).NotTo(HaveOccurred())
	o.Cursor = *first.NextCursor
	next, e := q.Search(ctx, token, o)
	Expect(e).NotTo(HaveOccurred())
	Expect(paths(next.Items)).To(Equal([]string{"b.html"}))
	_, e = q.Search(ctx, owner, query.SearchOptions{Text: "needle"})
	Expect(errors.Is(e, domain.ErrUnsupportedDerivedData)).To(BeTrue())
	_, e = q.Search(ctx, owner, query.SearchOptions{})
	Expect(errors.Is(e, domain.ErrUnsupportedDerivedData)).To(BeTrue())
	_, e = s.db.Exec(`UPDATE search_units SET kind='page' WHERE version_id=? AND kind='future_kind'`, hidden)
	Expect(e).NotTo(HaveOccurred())
	_, e = s.db.Exec(`UPDATE search_indexes SET state='future_state' WHERE version_id=?`, hidden)
	Expect(e).NotTo(HaveOccurred())
	_, e = q.Search(ctx, owner, query.SearchOptions{})
	Expect(errors.Is(e, domain.ErrUnsupportedDerivedData)).To(BeTrue())
	next, e = q.Search(ctx, token, o)
	Expect(e).NotTo(HaveOccurred())
	Expect(paths(next.Items)).To(Equal([]string{"b.html"}))

	handler := v1.New(q, nil, nil)
	r := httptest.NewRequest("GET", "/api/v1/search?q=needle", nil)
	r.Header.Set("Authorization", "Bearer "+owner)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	Expect(w.Code).To(Equal(503))
	Expect(w.Header().Get("Retry-After")).To(Equal("2"))
	Expect(w.Body.String()).NotTo(ContainSubstring("future_kind"))
	Expect(w.Body.String()).NotTo(ContainSubstring("future_state"))
})

var _ = It("SC08 body snippets retain authored text after a long casefold-expanding prefix", Label("persistence"), func() {
	s, a, owner, p := searchFixture()
	text := strings.Repeat("ß", 15000) + " Needle " + strings.Repeat("Z", 30000)
	v, _, b := seedSearchTree(s, p, "", "", map[string]string{"page.html": "<p>" + text + "</p>"})
	finishSearch(s, v, b)
	result := runSearch(query.New(s, a, nil), owner, `"Needle"`, "page")
	Expect(result.Items).To(HaveLen(1))
	Expect(result.Items[0].Snippet).NotTo(BeNil())
	Expect(*result.Items[0].Snippet).To(ContainSubstring("Needle"))
	Expect(*result.Items[0].Snippet).To(ContainSubstring("ß"))
	Expect(text).To(ContainSubstring(*result.Items[0].Snippet))
})
