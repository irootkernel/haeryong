package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	httpv1 "haeryong.local/haeryong/internal/adapters/http/v1"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/references"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func referenceRowsSnapshot(s *Store) []byte {
	GinkgoHelper()
	snapshot := map[string][][]any{}
	for _, name := range []string{"authored_references", "reference_materializations", "authored_edges", "derived_work", "jobs", "events"} {
		rows, err := s.db.Query("SELECT * FROM " + name + " ORDER BY rowid")
		Expect(err).NotTo(HaveOccurred())
		cols, err := rows.Columns()
		Expect(err).NotTo(HaveOccurred())
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			Expect(rows.Scan(ptrs...)).To(Succeed())
			snapshot[name] = append(snapshot[name], values)
		}
		Expect(rows.Err()).To(Succeed())
		Expect(rows.Close()).To(Succeed())
	}
	raw, err := json.Marshal(snapshot)
	Expect(err).NotTo(HaveOccurred())
	return raw
}
func referenceHead(s *Store) (string, int64) {
	GinkgoHelper()
	var epoch string
	var seq int64
	Expect(s.Write(ctx, func(tx app.WriteTx) error { var err error; epoch, seq, err = tx.EventHead(); return err })).To(Succeed())
	return epoch, seq
}
func referenceHTTP(h *httpv1.Handler, method, token, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var _ = Describe("RF03 ordered reference projections", Ordered, Label("persistence"), func() {
	var f *referenceWorld
	var auth *appauth.Service
	var q *query.Service
	var owner, project, sourceID, targetID, sourceOnly, both string
	const body = "<section id='retry'><a data-haeryong-reference='../참조/%2F'>pin</a></section>"
	BeforeAll(func() {
		s, a, w, _ := authFixture(GinkgoT())
		auth = a
		_, poll := pairOwner(GinkgoT(), auth, "reference reader")
		owner = poll.Token
		f = &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
		h, err := s.Health(ctx)
		Expect(err).NotTo(HaveOccurred())
		project = h.Library.UnfiledProjectID
		q = query.New(s, auth, f.bytes)
	})
	AfterAll(func() { Expect(f.s.Close()).To(Succeed()) })
	It("RF02-READONLY RF03 RF05 projects pending/ready/denied without writes and binds source DOM", func() {
		targetID = f.publish(nil, body)
		f.hold(targetID)
		refs := []any{}
		for _, id := range []string{"../참조/%2F", "b", "c"} {
			r := f.declaration(targetID, id)
			r["label"] = "private canary"
			refs = append(refs, r)
		}
		sourceID = f.publish(refs, body)
		sourceOnly = searchToken(f.s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{project}, VersionIDs: []string{sourceID}})
		both = searchToken(f.s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{project}, VersionIDs: []string{sourceID, targetID}})
		missingAction := searchToken(f.s, domain.CredentialScope{Kind: "workflow", Actions: []string{"job.read"}, ProjectIDs: []string{project}, VersionIDs: []string{sourceID, targetID}})
		h := httpv1.New(q, nil, nil)
		path := "/api/v1/versions/" + sourceID + "/references"
		before := referenceRowsSnapshot(f.s)
		for _, method := range []string{"GET", "HEAD"} {
			w := referenceHTTP(h, method, owner, path)
			Expect(w.Code).To(Equal(200))
			if method == "HEAD" {
				Expect(w.Body.Len()).To(BeZero())
			} else {
				Expect(w.Body.String()).To(ContainSubstring("materialization_pending"))
			}
		}
		Expect(referenceRowsSnapshot(f.s)).To(Equal(before))
		f.run(sourceID, "search")
		f.run(sourceID, "references")
		before = referenceRowsSnapshot(f.s)
		for _, token := range []string{owner, sourceOnly, both, missingAction} {
			for _, method := range []string{"GET", "HEAD"} {
				w := referenceHTTP(h, method, token, path)
				if token == missingAction {
					Expect(w.Code).To(Equal(403))
				} else {
					Expect(w.Code).To(Equal(200))
				}
				if method == "HEAD" {
					Expect(w.Body.Len()).To(BeZero())
				}
			}
		}
		for _, token := range []string{owner, sourceOnly, both} {
			for _, method := range []string{"GET", "HEAD"} {
				w := referenceHTTP(h, method, token, path+"/resolve?"+url.Values{"reference_id": {"../참조/%2F"}}.Encode())
				if token == sourceOnly {
					Expect(w.Code).To(Equal(404))
					if method == "GET" {
						Expect(w.Body.String()).To(ContainSubstring(`"details":{}`))
						Expect(w.Body.String()).NotTo(ContainSubstring("canary"))
					}
				} else {
					Expect(w.Code).To(Equal(200))
					if method == "GET" {
						Expect(w.Body.String()).To(ContainSubstring("anchor_pending"))
					}
				}
				if method == "HEAD" {
					Expect(w.Body.Len()).To(BeZero())
				}
			}
		}
		page, region := "page.html", "retry"
		row, err := q.ResolveReference(ctx, both, sourceID, "../참조/%2F", query.ReferenceOptions{Page: &page, Region: &region})
		Expect(err).NotTo(HaveOccurred())
		Expect(*row.Reason).To(Equal("anchor_pending"))
		other := "other.html"
		row, err = q.ResolveReference(ctx, both, sourceID, "b", query.ReferenceOptions{Page: &other})
		Expect(err).NotTo(HaveOccurred())
		Expect(*row.Reason).To(Equal("page_missing"))
		Expect(referenceRowsSnapshot(f.s)).To(Equal(before))
	})
	It("RF03-C preserves cursor bytes/membership under incoming resolution (RV-CURSOR/RV-READFILTER)", func() {
		first, err := q.References(ctx, sourceOnly, sourceID, query.Options{Limit: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(first.NextCursor).NotTo(BeNil())
		Expect(first.Items[0].Target).To(BeNil())
		Expect(first.Items[0].Label).To(BeNil())
		ownerFirst, err := q.References(ctx, owner, sourceID, query.Options{Limit: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(*ownerFirst.Items[0].Reason).To(Equal("anchor_pending"))
		var before domain.ReferenceMaterialization
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			var err error
			before, err = tx.ReferenceMaterialization(sourceID)
			return err
		})).To(Succeed())
		f.run(targetID, "search")
		f.run(targetID, "references")
		again, err := q.References(ctx, sourceOnly, sourceID, query.Options{Limit: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(first))
		continuation, err := q.References(ctx, sourceOnly, sourceID, query.Options{Limit: 1, Cursor: *first.NextCursor})
		Expect(err).NotTo(HaveOccurred())
		Expect(continuation.Items[0].ID).To(Equal("b"))
		authorized, err := q.References(ctx, owner, sourceID, query.Options{Limit: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(authorized.NextCursor).To(Equal(ownerFirst.NextCursor))
		Expect(authorized.Items[0].Resolution).To(Equal("resolved"))
		Expect(authorized.Items[0].EdgeID).NotTo(BeNil())
		Expect(*authorized.Items[0].ReaderURL).To(ContainSubstring("#region=retry"))
		var after domain.ReferenceMaterialization
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			var err error
			after, err = tx.ReferenceMaterialization(sourceID)
			return err
		})).To(Succeed())
		Expect(after).To(Equal(before))
		page, region := "page.html", "retry"
		row, err := q.ResolveReference(ctx, both, sourceID, "../참조/%2F", query.ReferenceOptions{Page: &page, Region: &region})
		Expect(err).NotTo(HaveOccurred())
		Expect(row.Resolution).To(Equal("resolved"))
	})
	It("RF02-READONLY RF03 seeded archive/trash/history requires four D4 conditions with no prior GET (RV-READHISTORY)", func() {
		// This second referring version and its edge have never been read publicly.
		history := f.publish([]any{f.declaration(targetID, "history")}, body)
		f.run(history, "search")
		f.run(history, "references")
		target, err := f.s.Version(ctx, targetID)
		Expect(err).NotTo(HaveOccurred())
		setLifecycle := func(state string) {
			_, err := f.s.db.Exec(`UPDATE documents SET lifecycle=? WHERE document_id=?`, state, target.DocumentID)
			Expect(err).NotTo(HaveOccurred())
		}
		setLifecycle("archived")
		row, err := q.ResolveReference(ctx, owner, sourceID, "b", query.ReferenceOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(row.Resolution).To(Equal("resolved"))
		setLifecycle("trash")
		before := referenceRowsSnapshot(f.s)
		_, err = q.ResolveReference(ctx, owner, sourceID, "b", query.ReferenceOptions{})
		Expect(err).To(MatchError(domain.ErrNotFound))
		list, err := q.References(ctx, owner, sourceID, query.Options{})
		Expect(err).NotTo(HaveOccurred())
		for _, r := range list.Items {
			Expect(r.Target).To(BeNil())
			Expect(r.Label).To(BeNil())
			Expect(*r.Reason).To(Equal("target_unavailable"))
		}
		Expect(referenceRowsSnapshot(f.s)).To(Equal(before))
		setLifecycle("purged")
		scopedHistory := searchToken(f.s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{project}, VersionIDs: []string{history, targetID}})
		before = referenceRowsSnapshot(f.s)
		h := httpv1.New(q, nil, nil)
		path := "/api/v1/versions/" + history + "/references/resolve?reference_id=history"
		for _, token := range []string{owner, scopedHistory} {
			for _, method := range []string{"GET", "HEAD"} {
				w := referenceHTTP(h, method, token, path)
				Expect(w.Code).To(Equal(410))
				if method == "GET" {
					var env struct {
						Error struct {
							Details map[string]any `json:"details"`
						} `json:"error"`
					}
					Expect(json.Unmarshal(w.Body.Bytes(), &env)).To(Succeed())
					Expect(env.Error.Details).To(Equal(map[string]any{"document_id": target.DocumentID, "version_id": targetID}))
				} else {
					Expect(w.Body.Len()).To(BeZero())
				}
			}
		}
		_, err = q.ResolveReference(ctx, sourceOnly, sourceID, "b", query.ReferenceOptions{})
		Expect(err).To(MatchError(domain.ErrNotFound))
		Expect(referenceRowsSnapshot(f.s)).To(Equal(before))
		// Seed no retained edge: prior purged identity alone must not produce 410.
		_, err = f.s.db.Exec(`DELETE FROM authored_edges WHERE from_version_id=?`, history)
		Expect(err).NotTo(HaveOccurred())
		_, err = q.ResolveReference(ctx, owner, history, "history", query.ReferenceOptions{})
		Expect(err).To(MatchError(domain.ErrNotFound))
		setLifecycle("active")
	})
	It("RF03-E pairs complete live/replay objects for hidden T versus unknown T plus control work", func() {
		// Fork one physical baseline sequentially. Identity, credentials, stream,
		// source declaration bytes and event sequence baseline are byte-identical.
		sourceVersion := "a0000000-0000-4000-8000-000000060001"
		targetVersion := "a0000000-0000-4000-8000-000000050001"
		targetDocument := "a0000000-0000-4000-8000-000000050002"
		token := searchToken(f.s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{project}, VersionIDs: []string{sourceVersion}})
		targetToken := searchToken(f.s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{project}, VersionIDs: []string{sourceVersion, targetVersion}})
		baseline := filepath.Join(GinkgoT().TempDir(), "reference-baseline.sqlite")
		_, err := f.s.db.Exec(`VACUUM INTO ?`, baseline)
		Expect(err).NotTo(HaveOccurred())
		epoch, start := referenceHead(f.s)
		type outcome struct {
			live, replay  []domain.Event
			sourceState   domain.ReferenceMaterialization
			cursor        *string
			incomingDelta int64
		}
		branches := []outcome{}
		for branch := 0; branch < 2; branch++ {
			if branch == 1 {
				Expect(f.s.Close()).To(Succeed())
				raw, err := os.ReadFile(baseline)
				Expect(err).NotTo(HaveOccurred())
				dbPath := filepath.Join(f.writer.Profile.DataDir, "storage", "library.sqlite")
				Expect(os.WriteFile(dbPath, raw, 0600)).To(Succeed())
				f.s = New(nil)
				state, _, err := f.s.Prepare(ctx, f.writer.Profile)
				Expect(err).NotTo(HaveOccurred())
				Expect(state.Ready).To(BeTrue())
				auth = appauth.New(f.s)
				q = query.New(f.s, auth, f.bytes)
			}
			f.n = 50000
			if branch == 1 {
				f.n = 51000
			}
			hidden := f.publish(nil, body)
			f.hold(hidden)
			refs := []any{}
			for _, id := range []string{"a", "b", "c"} {
				refs = append(refs, map[string]any{"id": id, "document_id": targetDocument, "version_id": targetVersion, "page_path": "page.html", "region_id": "retry"})
			}
			f.n = 60000
			v := f.publish(refs, body)
			Expect(v).To(Equal(sourceVersion))
			f.run(v, "search")
			f.run(v, "references")
			first, err := q.References(ctx, token, v, query.Options{Limit: 1})
			Expect(err).NotTo(HaveOccurred())
			f.run(hidden, "search")
			worker := references.NewWorker(f.s, f.bytes)
			job := f.job(hidden, "references")
			for i := 0; i < 20; i++ {
				var work domain.DerivedWork
				Expect(f.s.Write(ctx, func(tx app.WriteTx) error { var err error; work, err = tx.DerivedWork(job.ID); return err })).To(Succeed())
				var cp struct {
					Phase string `json:"phase"`
				}
				if work.AfterPath != "" {
					Expect(json.Unmarshal([]byte(work.AfterPath), &cp)).To(Succeed())
				}
				if cp.Phase == "incoming" {
					break
				}
				Expect(worker.Process(ctx, job.ID)).To(Succeed())
			}
			_, incomingBefore := referenceHead(f.s)
			// Consume precisely one incoming slice, without the subsequent completion.
			Expect(worker.Process(ctx, job.ID)).To(Succeed())
			_, incomingAfter := referenceHead(f.s)

			f.run(hidden, "references")
			again, err := q.References(ctx, token, v, query.Options{Limit: 1})
			Expect(err).NotTo(HaveOccurred())
			Expect(again).To(Equal(first))
			_, err = q.References(ctx, token, v, query.Options{Limit: 1, Cursor: *first.NextCursor})
			Expect(err).NotTo(HaveOccurred())
			events, err := f.s.Events(ctx, epoch, start, 10000)
			Expect(err).NotTo(HaveOccurred())
			result := outcome{cursor: first.NextCursor, incomingDelta: incomingAfter - incomingBefore}
			after := start
			for {
				batch, err := auth.Events(ctx, token, epoch, after)
				Expect(err).NotTo(HaveOccurred())
				if len(batch) == 0 {
					break
				}
				result.replay = append(result.replay, batch...)
				after = batch[len(batch)-1].Sequence
			}
			targetOwn := false
			for _, event := range events {
				projected, err := auth.FilterEvent(ctx, token, event)
				Expect(err).NotTo(HaveOccurred())
				result.live = append(result.live, projected)
				if branch == 0 && event.ResourceID == hidden && event.Kind == "references.updated" {
					for _, reader := range []string{owner, targetToken} {
						visible, err := auth.FilterEvent(ctx, reader, event)
						Expect(err).NotTo(HaveOccurred())
						Expect(visible).To(Equal(event))
					}
					targetOwn = true
				}
			}
			if branch == 0 {
				Expect(targetOwn).To(BeTrue())
			}
			Expect(result.live).To(Equal(result.replay), "complete hidden-source replay/live projection (RV-REPLAYFILTER)")
			Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
				var err error
				result.sourceState, err = tx.ReferenceMaterialization(v)
				return err
			})).To(Succeed())
			branches = append(branches, result)
		}
		// Job IDs are private random identity and absent from source projections.
		// The source materialization job ID itself is not compared across branches.
		Expect(branches[1].live).To(Equal(branches[0].live), "RV-TARGETEVENT")
		Expect(branches[1].replay).To(Equal(branches[0].replay))
		Expect(branches[1].sourceState.Revision).To(Equal(branches[0].sourceState.Revision), "RV-EDGEREVISION")
		Expect(branches[1].cursor).To(Equal(branches[0].cursor))
		Expect(branches[0].incomingDelta).To(BeZero(), "incoming creates no event, including masked replacement invalidation")
	})
	It("RF05 source placement shares implied-end/scope rules before target projection (RV-SCOPE)", func() {
		const mark = "<a data-haeryong-reference='r'>link</a>"
		rows := []struct {
			name, html, region string
			valid              bool
		}{
			{"p-block", "<p id='left'>x<div>" + mark + "</div>", "left", false},
			{"p-sibling", "<p id='left'>x<p id='right'>" + mark + "</p>", "left", false},
			{"li-sibling", "<ul><li id='left'>x<li id='right'>" + mark + "</ul>", "left", false},
			{"td-sibling", "<table><tr><td id='left'>x<td id='right'>" + mark + "</table>", "left", false},
			{"tr-sibling", "<table><tr id='left'><td>x<tr id='right'><td>" + mark + "</table>", "left", false},
			{"nested-valid", "<div id='outer'><section id='left'><p>" + mark + "</section></div>", "left", true},
			{"li-valid", "<ul><li id='left'>" + mark + "<li id='right'>other</ul>", "left", true},
			{"td-valid", "<table><tr><td id='left'>" + mark + "<td id='right'>other</table>", "left", true},
		}
		refs := []any{}
		var body strings.Builder
		for _, row := range rows {
			name := row.name + "-"
			replacement := strings.NewReplacer("id='left'", "id='"+name+"left'", "id='right'", "id='"+name+"right'", "data-haeryong-reference='r'", "data-haeryong-reference='"+name+"r'")
			body.WriteString("<section>" + replacement.Replace(row.html) + "</section>")
			r := f.declaration(targetID, name+"r")
			r["from"] = map[string]any{"page_path": "page.html", "region_id": name + row.region}
			r["label"] = "target canary"
			refs = append(refs, r)
		}
		// One source publication and extraction serves the whole sibling matrix.
		source := f.publish(refs, body.String())
		f.run(source, "search")
		f.run(source, "references")
		type outcome struct {
			Name, Resolution, Reason             string
			Target, Label, Edge, URL, TargetRead bool
		}
		observed, want := []outcome{}, []outcome{}
		for _, row := range rows {
			page, region := "page.html", row.name+"-"+row.region
			spy := &referenceTargetSpy{Store: f.s, target: targetID}
			service := query.New(spy, auth, f.bytes)
			result, err := service.ResolveReference(ctx, owner, source, row.name+"-r", query.ReferenceOptions{Page: &page, Region: &region})
			Expect(err).NotTo(HaveOccurred(), row.name)
			reason := ""
			if result.Reason != nil {
				reason = *result.Reason
			}
			observed = append(observed, outcome{row.name, result.Resolution, reason, result.Target != nil, result.Label != nil, result.EdgeID != nil, result.ReaderURL != nil, spy.targetReads > 0})
			if row.valid {
				want = append(want, outcome{row.name, "resolved", "", true, true, true, true, true})
			} else {
				want = append(want, outcome{Name: row.name, Resolution: "unresolved", Reason: "source_context_mismatch"})
			}
		}
		AddReportEntry("source_scope_matrix", ReportEntryVisibilityFailureOrVerbose, observed)
		Expect(observed).To(Equal(want), "all wrong scopes reject before target projection; valid omitted ends remain usable")
	})
	It("RF08 cancels a request waiting for the SQLite gate", func() {
		f.s.mu.Lock()
		defer f.s.mu.Unlock()
		cancelled, cancel := context.WithTimeout(ctx, 15*time.Millisecond)
		defer cancel()
		_, err := q.References(cancelled, owner, sourceID, query.Options{})
		Expect(err).To(MatchError(context.DeadlineExceeded))
	})
})

var _ = Describe("RF03 manual reference variants", Label("manual_references"), func() {
	It("RF03 manual_references rejects malformed query IDs, stale permissions and hidden anchors", func() {
		// Exhaustive lifecycle/anchor variants share the existing read projection fixture
		// rather than repeating the canonical import/transfer workload.
		s, a, _, _ := authFixture(GinkgoT())
		_, poll := pairOwner(GinkgoT(), a, "manual references")
		f := &referenceWorld{s: s, bytes: &searchBytes{files: map[string]string{}}}
		q := query.New(s, a, f.bytes)
		targetBody := "<p id='dup'>one</p><p id='dup'>two</p>"
		target := f.publishTree(nil, targetBody, "", []domain.Entry{{Path: "Ångström.html", Kind: "file"}, {Path: "Ångström.html", Kind: "file"}}, nil)
		f.run(target, "search")
		f.run(target, "references")
		refs := []any{}
		for i, region := range []string{"dup", "missing"} {
			r := f.declaration(target, fmt.Sprint(i))
			r["region_id"] = region
			delete(r, "from")
			refs = append(refs, r)
		}
		r := f.declaration(target, "ambiguous-path")
		r["page_path"] = "Ångström.html"
		r["region_id"] = nil
		delete(r, "from")
		refs = append(refs, r)
		same := f.declaration(target, "same")
		same["region_id"] = "dup"
		same["from"] = map[string]any{"page_path": "page.html", "region_id": "left"}
		refs = append(refs, same)
		sourceBody := "<section id='left'><a data-haeryong-reference='0'>one</a><a id='child-one' data-haeryong-reference='same'>same</a><a id='child-two' data-haeryong-reference='same'>same</a></section><section id='right'><a data-haeryong-reference='0'>two</a></section>"
		source := f.publishTree(refs, sourceBody, "", []domain.Entry{{Path: "other.html", Kind: "file"}}, nil)
		f.run(source, "search")
		f.run(source, "references")
		list, err := q.References(ctx, poll.Token, source, query.Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(*list.Items[0].Reason).To(Equal("anchor_ambiguous"))
		Expect(*list.Items[1].Reason).To(Equal("anchor_missing"))
		page := "page.html"
		bound, err := q.ResolveReference(ctx, poll.Token, source, "0", query.ReferenceOptions{Page: &page})
		Expect(err).NotTo(HaveOccurred())
		Expect(*bound.Reason).To(Equal("ambiguous_reference_context"))
		Expect(bound.Target).To(BeNil())
		Expect(bound.EdgeID).To(BeNil())
		region := "left"
		sameRow, err := q.ResolveReference(ctx, poll.Token, source, "same", query.ReferenceOptions{Page: &page, Region: &region})
		Expect(err).NotTo(HaveOccurred())
		Expect(*sameRow.Reason).To(Equal("anchor_ambiguous"), "identical page/region placements share one declaration")
		Expect(*list.Items[2].Reason).To(Equal("ambiguous_path"))
		_, err = q.ResolveReference(ctx, poll.Token, source, "ambiguous-path", query.ReferenceOptions{})
		var invalid *query.ReferenceInvalid
		Expect(err).To(BeAssignableToTypeOf(invalid))
		h := httpv1.New(q, nil, nil)
		base := "/api/v1/versions/" + source + "/references"
		for _, suffix := range []string{"?limit=0", "?limit=201", "?cursor=bad", "?unknown=x", "?limit=1&limit=2", "/resolve", "/resolve?reference_id=0&region_id=r", "/resolve?reference_id=0&reference_id=1", "/resolve?reference_id=%FF", "/resolve?reference_id=0&page_path=../bad", "/resolve?reference_id=0&page_path="} {
			for _, method := range []string{"GET", "HEAD"} {
				w := referenceHTTP(h, method, poll.Token, base+suffix)
				Expect(w.Code).To(Equal(400), suffix)
				if method == "HEAD" {
					Expect(w.Body.Len()).To(BeZero())
				} else {
					Expect(w.Body.String()).To(ContainSubstring("pointer"))
				}
			}
		}
		token := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{func() string {
			h, e := s.Health(ctx)
			Expect(e).NotTo(HaveOccurred())
			return h.Library.UnfiledProjectID
		}()}, VersionIDs: []string{source}})
		_, err = s.db.Exec(`UPDATE search_units SET input_hash=? WHERE version_id=? AND kind='page'`, digest([]byte("contradictory immutable input")), target)
		Expect(err).NotTo(HaveOccurred())
		visible, err := q.ResolveReference(ctx, poll.Token, source, "0", query.ReferenceOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(*visible.Reason).To(Equal("anchor_drift"))
		Expect(visible.ReaderURL).NotTo(BeNil())
		masked, err := q.References(ctx, token, source, query.Options{})
		Expect(err).NotTo(HaveOccurred())
		for _, row := range masked.Items {
			Expect(*row.Reason).To(Equal("target_unavailable"))
			Expect(row.Target).To(BeNil())
		}
		targetVersion, err := s.Version(ctx, target)
		Expect(err).NotTo(HaveOccurred())
		_, err = s.db.Exec(`UPDATE documents SET lifecycle='trash' WHERE document_id=?`, targetVersion.DocumentID)
		Expect(err).NotTo(HaveOccurred())
		_, err = q.ResolveReference(ctx, poll.Token, source, "0", query.ReferenceOptions{})
		Expect(err).To(MatchError(domain.ErrNotFound))
		_, err = s.db.Exec(`UPDATE documents SET lifecycle='active' WHERE document_id=?`, targetVersion.DocumentID)
		Expect(err).NotTo(HaveOccurred())
		_, err = s.db.Exec(`UPDATE projects SET permissions_revision=permissions_revision+1`)
		Expect(err).NotTo(HaveOccurred())
		_, err = q.References(ctx, token, source, query.Options{})
		Expect(err).To(MatchError(domain.ErrDenied))
	})

})

// Observe actual target eligibility/structure reads without changing SQL.
type referenceTargetSpy struct {
	app.Store
	target      string
	targetReads int
}
type referenceTargetTx struct {
	app.WriteTx
	spy *referenceTargetSpy
}

func (s *referenceTargetSpy) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error { return fn(referenceTargetTx{tx, s}) })
}
func (t referenceTargetTx) PublishedVersion(id string) (domain.Version, error) {
	if id == t.spy.target {
		t.spy.targetReads++
	}
	return t.WriteTx.PublishedVersion(id)
}
func (t referenceTargetTx) ReferenceLifecycle(id string) (domain.ReferenceLifecycle, error) {
	t.spy.targetReads++
	return t.WriteTx.ReferenceLifecycle(id)
}
