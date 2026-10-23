package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/references"
	"haeryong.local/haeryong/internal/application/search"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

type referenceWorld struct {
	s         *Store
	writer    *service.LockedProfile
	bytes     *searchBytes
	n         int
	wantError error
}

func (f *referenceWorld) id() string { f.n++; return fmt.Sprintf("a0000000-0000-4000-8000-%012d", f.n) }
func (f *referenceWorld) publish(refs []any, body string) string {
	return f.publishVersion(refs, body, "")
}
func (f *referenceWorld) publishVersion(refs []any, body, base string) string {
	return f.publishTree(refs, body, base, nil, nil)
}
func (f *referenceWorld) publishTree(refs []any, body, base string, entries []domain.Entry, pages []string) string {
	GinkgoHelper()
	version, document, set := f.id(), f.id(), f.id()
	if refs == nil {
		refs = []any{}
	}
	raw, _ := json.Marshal(map[string]any{"schema_version": "1.0", "viewer_profile": "html_js_v1", "references": refs})
	manifest, page := digest(raw), digest([]byte(body))
	f.bytes.files[manifest] = string(raw)
	f.bytes.files[page] = body
	h, err := f.s.Health(ctx)
	Expect(err).NotTo(HaveOccurred())
	revision := int64(1)
	if base != "" {
		v, err := f.s.Version(ctx, base)
		Expect(err).NotTo(HaveOccurred())
		document = v.DocumentID
		d, err := f.s.Document(ctx, document)
		Expect(err).NotTo(HaveOccurred())
		revision = d.Revision
	}
	writeErr := f.s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.CreateContentSet(set, digest(nil)); err != nil {
			return err
		}
		for _, o := range []domain.Object{{Hash: manifest, Size: int64(len(raw)), State: "ready"}, {Hash: page, Size: int64(len(body)), State: "ready"}} {
			if err := tx.PutDurableObject(o); err != nil {
				return err
			}
		}
		if err := tx.PutEntry(domain.Entry{ContentSetID: set, Path: "page.html", Kind: "file", Hash: page, Size: int64(len(body))}); err != nil {
			return err
		}
		for _, entry := range entries {
			entry.ContentSetID = set
			if entry.Kind == "file" {
				entry.Hash = page
				entry.Size = int64(len(body))
			}
			if err := tx.PutEntry(entry); err != nil {
				return err
			}
		}
		if err := tx.SealContentSet(set); err != nil {
			return err
		}
		metadataID := ""
		if len(pages) > 0 {
			metadataID = f.id()
			if err := tx.CreateMetadataIndex(domain.MetadataIndex{ID: metadataID, PrincipalID: "system", Hash: manifest, ContentSetID: set}); err != nil {
				return err
			}
			for _, name := range pages {
				raw, _ := json.Marshal(map[string]any{"page_path": name, "label": "Explicit HTML page"})
				if err := tx.PutImportMetadata(metadataID, "pages", name, raw); err != nil {
					return err
				}
			}
		}
		return tx.FinalizeImport(domain.ImportIntent{JobID: metadataID, NewDocument: base == "", DocumentID: document, ProjectID: h.Library.UnfiledProjectID, DocumentRevision: revision, Title: "References"}, domain.Version{ID: version, DocumentID: document, BaseVersionID: base, ContentSetID: set, ManifestSHA: manifest, PayloadSHA: digest(nil), Title: "References", ViewerProfile: "html_js_v1", SourceBasis: "unverified"})
	})
	if f.wantError != nil {
		Expect(writeErr).To(MatchError(f.wantError))
	} else {
		Expect(writeErr).To(Succeed())
	}
	return version
}
func (f *referenceWorld) declaration(target, id string) map[string]any {
	GinkgoHelper()
	v, err := f.s.Version(ctx, target)
	Expect(err).NotTo(HaveOccurred())
	return map[string]any{"id": id, "document_id": v.DocumentID, "version_id": target, "page_path": "page.html", "region_id": "retry", "from": map[string]any{"page_path": "page.html", "region_id": "retry"}}
}
func (f *referenceWorld) job(version, kind string) domain.Job {
	GinkgoHelper()
	var id string
	Expect(f.s.db.QueryRow(`SELECT job_id FROM derived_work WHERE version_id=? AND work_kind=? ORDER BY extractor_revision DESC LIMIT 1`, version, kind).Scan(&id)).To(Succeed())
	j, err := f.s.Job(ctx, id)
	Expect(err).NotTo(HaveOccurred())
	return j
}
func (f *referenceWorld) run(version, kind string) {
	GinkgoHelper()
	var p work.Processor
	if kind == "search" {
		p = search.NewWorker(f.s, f.bytes)
	} else {
		p = references.NewWorker(f.s, f.bytes)
	}
	id := f.job(version, kind).ID
	for i := 0; i < 100; i++ {
		j, err := f.s.Job(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		if j.State == "completed" {
			return
		}
		Expect(j.State).NotTo(Or(Equal("failed"), Equal("cancelled")))
		Expect(p.Process(ctx, id)).To(Succeed())
	}
	Fail("reference fixture did not reach terminal checkpoint")
}
func (f *referenceWorld) hold(version string) {
	GinkgoHelper()
	p := search.NewWorker(f.s, f.bytes)
	id := f.job(version, "search").ID
	for i := 0; i < 30; i++ {
		var index domain.SearchIndex
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error { var err error; index, err = tx.SearchIndex(version); return err })).To(Succeed())
		if index.DiscoveryComplete {
			return
		}
		Expect(p.Process(ctx, id)).To(Succeed())
	}
	Fail("fixture did not reach held page EOF")
}

func (f *referenceWorld) edge(source, id string) (e domain.AuthoredEdge, err error) {
	err = f.s.Write(ctx, func(tx app.WriteTx) error { var err error; e, err = tx.AuthoredEdge(source, id); return err })
	return
}
func (f *referenceWorld) restart() {
	GinkgoHelper()
	Expect(f.s.Close()).To(Succeed())
	f.s = New(nil)
	state, _, err := f.s.Prepare(ctx, f.writer.Profile)
	Expect(err).NotTo(HaveOccurred())
	Expect(state.Ready).To(BeTrue())
}

// Barrier wrapping uses the public typed port and yields AFTER Write returns.
// It independently checks the durable row immediately before resolution begins.
type referenceBarrier struct {
	app.Store
	source, id           string
	before               func()
	after                func()
	observed, checkpoint bool
}
type referenceBarrierTx struct {
	app.WriteTx
	b *referenceBarrier
}

func (t referenceBarrierTx) AuthoredReference(v, id string) (domain.AuthoredReference, error) {
	if v == t.b.source && id == t.b.id {
		t.b.observed = true
	}
	return t.WriteTx.AuthoredReference(v, id)
}
func (t referenceBarrierTx) PutDerivedWork(w domain.DerivedWork) error {
	if w.WorkKind == "references" {
		var c struct{ Phase, After string }
		if json.Unmarshal([]byte(w.AfterPath), &c) == nil && c.Phase == "outgoing" && c.After == t.b.id {
			t.b.checkpoint = true
		}
	}
	return t.WriteTx.PutDerivedWork(w)
}
func (b *referenceBarrier) Write(ctx context.Context, apply func(app.WriteTx) error) error {
	err := b.Store.Write(ctx, func(tx app.WriteTx) error { return apply(referenceBarrierTx{tx, b}) })
	if b.observed && b.before != nil {
		b.observed = false
		b.before()
	}
	if err == nil && b.checkpoint && b.after != nil {
		b.checkpoint = false
		f := b.after
		b.after = nil
		f()
	}
	return err
}

var _ = Describe("RF07 ordered reference work", Ordered, Label("persistence"), func() {
	var f *referenceWorld
	BeforeAll(func() {
		_, w := profile(GinkgoT())
		f = &referenceWorld{s: openStore(GinkgoT(), w, nil), writer: w, bytes: &searchBytes{files: map[string]string{}}}
	})
	AfterAll(func() { Expect(f.s.Close()).To(Succeed()) })
	const html = "<html><p id='retry'>immutable anchor</p></html>"
	It("RF02-AUTHFREE publishes before materialization and enqueues atomically without publisher credentials (RV-STAGE/RV-ACTOR)", func() {
		rollback := errors.New("publication rollback")
		f.wantError = rollback
		f.s.fault = func(b Boundary) error {
			if b == BeforePublicationCommit {
				return rollback
			}
			return nil
		}
		absent := f.publish(nil, html)
		f.s.fault = nil
		f.wantError = nil
		_, err := f.s.Version(ctx, absent)
		Expect(err).To(MatchError(domain.ErrNotFound))
		var absentJobs int
		Expect(f.s.db.QueryRow(`SELECT count(*) FROM derived_work WHERE version_id=?`, absent).Scan(&absentJobs)).To(Succeed())
		Expect(absentJobs).To(BeZero())
		target := f.publish(nil, html)
		f.run(target, "search")
		f.run(target, "references")
		source := f.publish([]any{f.declaration(target, "pin")}, html)
		var count int
		Expect(f.s.db.QueryRow(`SELECT count(*) FROM authored_references WHERE from_version_id=?`, source).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		job := f.job(source, "references")
		var capture domain.SearchMaintenanceScope
		Expect(json.Unmarshal(job.Scope, &capture)).To(Succeed())
		Expect(capture.Actor).To(Equal("system"))
		// No original principal/session is present in the job. Expire all remembered
		// publishers before work; canonical maintenance remains authority-free.
		_, err = f.s.db.Exec(`INSERT OR IGNORE INTO auth_owner VALUES(1,'publisher');INSERT INTO device_sessions(session_id,principal_id,library_id,auth_epoch,token_hash,device_label,origin,created_at,last_seen_at,revoked) SELECT 'publisher-session',principal_id,library_id,auth_epoch,'publisher-verifier','fixture','http://127.0.0.1',0,0,1 FROM libraries CROSS JOIN auth_owner`)
		Expect(err).NotTo(HaveOccurred())
		f.run(source, "search")
		f.run(source, "references")
		edge, err := f.edge(source, "pin")
		Expect(err).NotTo(HaveOccurred())
		Expect(edge.TargetVersionID).To(Equal(target))
		before := f.job(source, "references")
		var again string
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			var err error
			again, err = tx.EnqueueReferences(source, domain.ReferenceRevision)
			return err
		})).To(Succeed())
		Expect(again).To(Equal(before.ID))
		Expect(f.job(source, "references")).To(Equal(before))
		// The enclosing publication transaction owns enqueue as well as visibility.
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			if _, err := tx.EnqueueReferences(target, domain.ReferenceRevision+1); err != nil {
				return err
			}
			return errors.New("rollback publication work")
		})).To(MatchError("rollback publication work"))
		Expect(f.job(target, "references").ID).NotTo(BeEmpty())
		var revisions int
		Expect(f.s.db.QueryRow(`SELECT count(*) FROM derived_work WHERE version_id=? AND work_kind='references'`, target).Scan(&revisions)).To(Succeed())
		Expect(revisions).To(Equal(1))
	})
	It("RF07-O1 commits before each outgoing attempt and resolves late target anchors after restart (RV-INCOMING/RV-COMMITFIRST)", func() {
		for _, interleave := range []bool{false, true} {
			target := f.publish(nil, html)
			f.hold(target)
			source := f.publish([]any{f.declaration(target, "late")}, html)
			f.run(source, "search")
			// T remains nonterminal before its page EOF; V's completed outgoing pass
			// cannot fabricate an edge. The barrier reads the committed d separately.
			barriers := 0
			observed := &referenceBarrier{Store: f.s, source: source, id: "late"}
			observed.before = func() {
				barriers++
				Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
					r, err := tx.AuthoredReference(source, "late")
					if err != nil {
						return err
					}
					Expect(r.TargetVersionID).To(Equal(target))
					return nil
				})).To(Succeed(), "durable declaration before outgoing attempt")
			}
			if interleave {
				observed.after = func() {
					f.run(target, "search")
					f.run(target, "references")
					_, err := f.edge(source, "late")
					Expect(err).NotTo(HaveOccurred(), "T incoming ran after V outgoing and before source continued")
				}
			}
			worker := references.NewWorker(observed, f.bytes)
			id := f.job(source, "references").ID
			for i := 0; i < 30 && f.job(source, "references").State != "completed"; i++ {
				Expect(worker.Process(ctx, id)).To(Succeed())
			}
			Expect(barriers).To(BeNumerically(">", 0))
			before := f.job(source, "references")
			var beforeState domain.ReferenceMaterialization
			Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
				var err error
				beforeState, err = tx.ReferenceMaterialization(source)
				return err
			})).To(Succeed())
			Expect(before.State).To(Equal("completed"))
			if !interleave {
				_, err := f.edge(source, "late")
				Expect(err).To(MatchError(domain.ErrNotFound))
				f.run(target, "search")
				f.restart()
				f.run(target, "references")
			}
			edge, err := f.edge(source, "late")
			Expect(err).NotTo(HaveOccurred())
			Expect(edge.TargetVersionID).To(Equal(target))
			Expect(f.job(source, "references")).To(Equal(before))
			Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
				after, err := tx.ReferenceMaterialization(source)
				Expect(after).To(Equal(beforeState))
				return err
			})).To(Succeed())
			var events int
			Expect(f.s.db.QueryRow(`SELECT count(*) FROM events WHERE kind='references.updated' AND resource_id=?`, source).Scan(&events)).To(Succeed())
			Expect(events).To(Equal(1), "incoming adds no source event")
			var count int
			Expect(f.s.db.QueryRow(`SELECT count(*) FROM derived_work WHERE version_id IN (?,?) AND work_kind='references'`, source, target).Scan(&count)).To(Succeed())
			Expect(count).To(Equal(2))
		}
	})
	It("RF07-O2 recovers the first committed outgoing row behind a completed incoming cursor (RV-OUTGOING)", func() {
		target := f.publish(nil, html)
		source := f.publish([]any{f.declaration(target, "first")}, html)
		control := f.publish([]any{f.declaration(target, "higher")}, html)
		f.run(control, "search")
		f.run(control, "references")
		f.run(target, "search")
		f.run(target, "references")
		targetJob := f.job(target, "references")
		var saved string
		Expect(f.s.db.QueryRow(`SELECT after_path FROM derived_work WHERE job_id=?`, targetJob.ID).Scan(&saved)).To(Succeed())
		Expect(saved).To(ContainSubstring(control))
		f.run(source, "search")
		worker := references.NewWorker(f.s, f.bytes)
		id := f.job(source, "references").ID
		for i := 0; i < 10; i++ {
			Expect(worker.Process(ctx, id)).To(Succeed())
			var phase string
			var raw string
			Expect(f.s.db.QueryRow(`SELECT after_path FROM derived_work WHERE job_id=?`, id).Scan(&raw)).To(Succeed())
			var c struct{ Phase string }
			Expect(json.Unmarshal([]byte(raw), &c)).To(Succeed())
			phase = c.Phase
			if phase == "outgoing" {
				break
			}
		}
		_, err := f.edge(source, "first")
		Expect(err).To(MatchError(domain.ErrNotFound))
		generation := f.job(source, "references").AttemptGeneration
		f.restart()
		f.run(source, "references")
		edge, err := f.edge(source, "first")
		Expect(err).NotTo(HaveOccurred())
		Expect(edge.TargetVersionID).To(Equal(target))
		Expect(f.job(source, "references").AttemptGeneration).To(BeNumerically(">", generation))
		Expect(f.job(target, "references")).To(Equal(targetJob))
	})
	It("RF07-R2 and RF04 gate delayed explicit source anchors in selection and direct router calls (RV-SOURCEGATE/RV-ROUTERGATE)", func() {
		target := f.publish(nil, html)
		f.run(target, "search")
		f.run(target, "references")
		source := f.publish([]any{f.declaration(target, "source-gate")}, html)
		f.hold(source)
		before := f.job(source, "references")
		router := work.NewMaintenance(f.s, f.bytes)
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			jobs, err := tx.WorkJobs(64)
			for _, j := range jobs {
				Expect(j.ID).NotTo(Equal(before.ID))
			}
			return err
		})).To(Succeed())
		Expect(router.Process(ctx, before.ID)).To(Succeed())
		Expect(f.job(source, "references")).To(Equal(before))
		f.run(source, "search")
		f.run(source, "references")
		_, err := f.edge(source, "source-gate")
		Expect(err).NotTo(HaveOccurred())
		// A region on another page, a missing source anchor, an ambiguous anchor and
		// a mismatched target document never become a structural edge.
		invalid := f.declaration(target, "wrong-source")
		invalid["from"] = map[string]any{"page_path": "page.html", "region_id": "missing"}
		mismatch := f.declaration(target, "wrong-document")
		mismatch["document_id"] = f.id()
		bad := f.publish([]any{invalid, mismatch}, html)
		f.run(bad, "search")
		f.run(bad, "references")
		for _, id := range []string{"wrong-source", "wrong-document"} {
			_, err = f.edge(bad, id)
			Expect(err).To(MatchError(domain.ErrNotFound))
		}
		ambiguous := f.publish(nil, "<p id='retry'>one</p><p data-haeryong-region='retry'>two</p>")
		f.run(ambiguous, "search")
		f.run(ambiguous, "references")
		linked := f.publish([]any{f.declaration(ambiguous, "ambiguous")}, html)
		f.run(linked, "search")
		f.run(linked, "references")
		_, err = f.edge(linked, "ambiguous")
		Expect(err).To(MatchError(domain.ErrNotFound))
		pending := f.publish(nil, html)
		f.hold(pending)
		_, err = f.s.db.Exec(`UPDATE jobs SET state='failed' WHERE job_id=?`, f.job(pending, "search").ID)
		Expect(err).NotTo(HaveOccurred())
		f.run(pending, "references")
		linked = f.publish([]any{f.declaration(pending, "failed-anchor")}, html)
		f.run(linked, "search")
		f.run(linked, "references")
		_, err = f.edge(linked, "failed-anchor")
		Expect(err).To(MatchError(domain.ErrNotFound))
		original, err := f.edge(source, "source-gate")
		Expect(err).NotTo(HaveOccurred())
		_, err = f.s.db.Exec(`UPDATE page_anchors SET text_sha256='bad' WHERE version_id=?`, target)
		Expect(err).NotTo(HaveOccurred())
		corrupt := f.publish([]any{f.declaration(target, "corrupt-target-anchor")}, html)
		f.run(corrupt, "search")
		f.run(corrupt, "references")
		_, err = f.edge(corrupt, "corrupt-target-anchor")
		Expect(err).To(MatchError(domain.ErrNotFound))
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			m, err := tx.ReferenceMaterialization(corrupt)
			Expect(m.State).To(Equal("ready"), "target-derived failure does not fail source completeness")
			return err
		})).To(Succeed())
		_, err = f.s.db.Exec(`UPDATE page_anchors SET text_sha256=? WHERE version_id=?`, *original.TargetAnchorSHA, target)
		Expect(err).NotTo(HaveOccurred())

	})

	It("RF04-PAGE resolves page-only target and from before failed/cancelled search EOF (RV-PAGE)", func() {
		for _, state := range []string{"failed", "cancelled"} {
			target := f.publish(nil, html)
			f.hold(target)
			_, err := f.s.db.Exec(`UPDATE jobs SET state=? WHERE job_id=?`, state, f.job(target, "search").ID)
			Expect(err).NotTo(HaveOccurred())
			// Both endpoint versions stop before EOF; one declaration needs no anchor,
			// while each region-bearing counterpart must remain honestly pending.
			page := f.declaration(target, "page")
			page["region_id"] = nil
			page["from"] = map[string]any{"page_path": "page.html", "region_id": nil}
			targetRegion := f.declaration(target, "target-region")
			targetRegion["from"] = map[string]any{"page_path": "page.html", "region_id": nil}
			sourceRegion := f.declaration(target, "source-region")
			sourceRegion["region_id"] = nil
			source := f.publish([]any{page, targetRegion, sourceRegion}, html)
			f.hold(source)
			_, err = f.s.db.Exec(`UPDATE jobs SET state=? WHERE job_id=?`, state, f.job(source, "search").ID)
			Expect(err).NotTo(HaveOccurred())
			if state == "cancelled" {
				// Inventory-only validation also survives absent extractor output, not merely incomplete units.
				_, err = f.s.db.Exec(`DELETE FROM search_indexes WHERE version_id IN (?,?)`, target, source)
				Expect(err).NotTo(HaveOccurred())
			}
			for _, version := range []string{target, source} {
				Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
					a, err := tx.ReferenceAnchor(version, "page.html", "retry")
					Expect(a.Reason).To(Equal("anchor_pending"))
					return err
				})).To(Succeed())
			}
			f.run(source, "references")
			edge, err := f.edge(source, "page")
			Expect(err).NotTo(HaveOccurred(), state+" page-only endpoints must not wait for extraction")
			Expect(edge.FromPageSHA).NotTo(BeNil())
			Expect(edge.TargetPageSHA).To(Equal(edge.FromPageSHA))
			Expect(edge.FromAnchorSHA).To(BeNil())
			Expect(edge.TargetAnchorSHA).To(BeNil())
			for _, id := range []string{"target-region", "source-region"} {
				_, err = f.edge(source, id)
				Expect(err).To(MatchError(domain.ErrNotFound))
			}
		}
	})
	It("RF04-ALIAS canonicalizes HTML endpoints and rejects metadata-only non-HTML pages (RV-ALIAS/RV-HTML)", func() {
		entries := []domain.Entry{
			{Path: "real.html", Kind: "file"}, {Path: "alias.html", Kind: "symlink", LinkText: "real.html"},
			{Path: "alias", Kind: "symlink", LinkText: "real.html"},
			{Path: "cycle.html", Kind: "symlink", LinkText: "cycle.html"},
			{Path: "outside.html", Kind: "symlink", LinkText: "../real.html"},
			{Path: "absolute.html", Kind: "symlink", LinkText: "/real.html"},
			{Path: "dangling.html", Kind: "symlink", LinkText: "missing.html"},
			{Path: "explicit", Kind: "file"},
		}
		target := f.publishTree(nil, html, "", entries, []string{"explicit"})
		f.run(target, "search")
		refs := []any{}
		for _, endpoint := range []string{"target", "from"} {
			for _, alias := range []string{"alias.html", "alias", "cycle.html", "outside.html", "absolute.html", "dangling.html"} {
				r := f.declaration(target, endpoint+alias)
				if endpoint == "target" {
					r["page_path"] = alias
				} else {
					r["from"] = map[string]any{"page_path": alias, "region_id": "retry"}
				}
				refs = append(refs, r)
			}
		}
		for _, endpoint := range []string{"target", "from"} {
			explicit := f.declaration(target, endpoint+"-non-html")
			explicit["region_id"] = nil
			explicit["from"] = map[string]any{"page_path": "page.html", "region_id": nil}
			if endpoint == "target" {
				explicit["page_path"] = "explicit"
			} else {
				explicit["from"] = map[string]any{"page_path": "explicit", "region_id": nil}
			}
			refs = append(refs, explicit)
		}
		source := f.publishTree(refs, html, "", entries, []string{"explicit"})
		f.run(source, "search")
		f.run(source, "references")
		health, err := f.s.Health(ctx)
		Expect(err).NotTo(HaveOccurred())
		for _, endpoint := range []string{"target", "from"} {
			for _, alias := range []string{"alias.html", "alias"} {
				edge, err := f.edge(source, endpoint+alias)
				Expect(err).NotTo(HaveOccurred())
				canonical := "page.html"
				if endpoint == "from" {
					canonical = "real.html"
				}
				Expect(*edge.FromPagePath).To(Equal(canonical))
				canonical = "page.html"
				if endpoint == "target" {
					canonical = "real.html"
				}
				Expect(*edge.TargetPagePath).To(Equal(canonical))
				Expect(edge.FromAnchorSHA).NotTo(BeNil())
				Expect(edge.TargetAnchorSHA).NotTo(BeNil())
				want, err := domain.ReferenceEdgeID(health.Library.ID, source, endpoint+alias)
				Expect(err).NotTo(HaveOccurred())
				Expect(edge.ID).To(Equal(want))
				Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
					declaration, err := tx.AuthoredReference(source, endpoint+alias)
					if endpoint == "target" {
						Expect(*declaration.TargetPagePath).To(Equal(alias))
					} else {
						Expect(*declaration.FromPagePath).To(Equal(alias))
					}
					return err
				})).To(Succeed())

			}
			for _, alias := range []string{"cycle.html", "outside.html", "absolute.html", "dangling.html"} {
				_, err = f.edge(source, endpoint+alias)
				Expect(err).To(MatchError(domain.ErrNotFound))
			}
		}
		for _, endpoint := range []string{"target", "from"} {
			_, err = f.edge(source, endpoint+"-non-html")
			Expect(err).To(MatchError(domain.ErrNotFound), "metadata cannot turn a non-HTML file into a renderer page")
		}
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			// The declaration really entered version_pages; it is not renderer authority.
			_, err := tx.Page(source, "explicit")
			if err != nil {
				return err
			}
			_, err = tx.Page(target, "explicit")
			if err != nil {
				return err
			}
			m, err := tx.ReferenceMaterialization(source)
			Expect(m.State).To(Equal("ready"))
			return err
		})).To(Succeed())

	})
	It("RF07-TERMINAL/FAIR admits only search terminality and preserves cancelled/failed keys (RV-GATESTATES/RV-REOPEN)", func() {
		source := f.publish(nil, html)
		sj, rj := f.job(source, "search"), f.job(source, "references")
		for _, state := range []string{"queued", "running", "interrupted", "paused", "reconciling", "cancelling", "completed", "failed", "cancelled"} {
			_, err := f.s.db.Exec(`UPDATE jobs SET state=? WHERE job_id=?`, state, sj.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
				ready, err := tx.ReferenceSearchTerminal(source)
				Expect(ready).To(Equal(terminal(state)), state)
				return err
			})).To(Succeed())
		}
		// A cancelled search supplies no false EOF anchors; no search is a distinct
		// allowed state, not a promise of an existing anchor.
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error { _, err := tx.ClaimJob(rj.ID); return err })).To(Succeed())
		_, err := f.s.db.Exec(`UPDATE jobs SET state='paused' WHERE job_id=?`, sj.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error { _, err := tx.CancelJob(rj.ID); return err })).To(Succeed())
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			jobs, err := tx.WorkJobs(64)
			ids := []string{}
			for _, j := range jobs {
				ids = append(ids, j.ID)
			}
			Expect(ids).To(ContainElement(rj.ID))
			return err
		})).To(Succeed())
		Expect(work.NewMaintenance(f.s, f.bytes).Process(ctx, rj.ID)).To(Succeed())
		cancelled := f.job(source, "references")
		Expect(cancelled.State).To(Equal("cancelled"))
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			_, err := tx.EnqueueReferences(source, domain.ReferenceRevision)
			return err
		})).To(Succeed())
		Expect(f.job(source, "references")).To(Equal(cancelled))
		_, err = f.s.db.Exec(`DELETE FROM derived_work WHERE job_id=?`, sj.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			ready, err := tx.ReferenceSearchTerminal(source)
			Expect(ready).To(BeTrue())
			return err
		})).To(Succeed())
	})
	It("RF07-FAIR filters 65 blocked references before ranking/LIMIT and acknowledges cancel while gated (RV-LIMITFIRST)", func() {
		h, err := f.s.Health(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			for i := 0; i < 65; i++ {
				version, doc, set, job := f.id(), f.id(), f.id(), fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
				if err := tx.CreateDocument(domain.Document{ID: doc, ProjectID: h.Library.UnfiledProjectID, Title: "blocked"}); err != nil {
					return err
				}
				if err := tx.CreateContentSet(set, digest(nil)); err != nil {
					return err
				}
				if err := tx.SealContentSet(set); err != nil {
					return err
				}
				manifest := digest([]byte("fairness manifest"))
				if err := tx.PutVersion(domain.Version{ID: version, DocumentID: doc, ContentSetID: set, ManifestSHA: manifest, ViewerProfile: "html_js_v1", SourceBasis: "unverified"}); err != nil {
					return err
				}
				if err := tx.PublishVersion(version, 1); err != nil {
					return err
				}
				if err := tx.CreateJob(domain.Job{ID: job, Kind: "derived_index"}); err != nil {
					return err
				}
				if err := tx.PutDerivedWork(domain.DerivedWork{JobID: job, VersionID: version, WorkKind: "references", ExtractorRevision: domain.ReferenceRevision}); err != nil {
					return err
				}
				if err := tx.PutReferenceMaterialization(domain.ReferenceMaterialization{FromVersionID: version, ManifestSHA: manifest, ReferenceRevision: domain.ReferenceRevision, State: "building", JobID: job, Revision: 1}); err != nil {
					return err
				}
			}
			jobs, err := tx.WorkJobs(1)
			Expect(jobs).To(HaveLen(1), "blocked prefix must not hide runnable search")
			if len(jobs) > 0 {
				w, err := tx.DerivedWork(jobs[0].ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(w.WorkKind).To(Equal("search"))
			}
			return err
		})).To(Succeed())
		// Cleanup the selection-only fixtures, keeping their absence out of backfill.
		_, err = f.s.db.Exec(`DELETE FROM reference_materializations WHERE job_id LIKE '00000000-%';DELETE FROM derived_work WHERE version_id IN (SELECT version_id FROM document_versions WHERE manifest_sha256=?);UPDATE document_versions SET sequence=NULL WHERE manifest_sha256=?`, digest([]byte("fairness manifest")), digest([]byte("fairness manifest")))
		Expect(err).NotTo(HaveOccurred())
	})
	It("RF06/RF07 fences cancel, stale callbacks, source restrictions and malformed manifest failures (RV-FENCE/RV-CANCEL)", func() {
		source := f.publish(nil, html)
		f.run(source, "search")
		id := f.job(source, "references").ID
		// Cancel during detached manifest IO, then prove no declaration/checkpoint can
		// be applied by that old attempt. The callback is a test-only object adapter.
		f.bytes.onRead = func(hash string) {
			f.bytes.onRead = nil
			Expect(f.s.Write(ctx, func(tx app.WriteTx) error { _, err := tx.CancelJob(id); return err })).To(Succeed())
		}
		err := references.NewWorker(f.s, f.bytes).Process(ctx, id)
		Expect(err).To(HaveOccurred())
		Expect(f.job(source, "references").State).To(Equal("cancelled"))
		var progress domain.SearchProgress
		Expect(json.Unmarshal(f.job(source, "references").Result, &progress)).To(Succeed())
		Expect(progress.FilesDone).To(Equal("0"), "cancelled manifest was never completed")
		Expect(progress.BytesDone).To(Equal("0"))
		var m domain.ReferenceMaterialization
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error { var err error; m, err = tx.ReferenceMaterialization(source); return err })).To(Succeed())
		Expect(m.State).To(Equal("failed"))
		paused := f.publish(nil, html)
		f.run(paused, "search")
		pausedID := f.job(paused, "references").ID
		h, err := f.s.Health(ctx)
		Expect(err).NotTo(HaveOccurred())
		f.bytes.onRead = func(hash string) {
			f.bytes.onRead = nil
			_, err := f.s.db.Exec(`UPDATE projects SET permissions_revision=permissions_revision+1 WHERE project_id=?`, h.Library.UnfiledProjectID)
			Expect(err).NotTo(HaveOccurred())
		}
		err = references.NewWorker(f.s, f.bytes).Process(ctx, pausedID)
		Expect(err).To(HaveOccurred())
		Expect(f.job(paused, "references").State).To(Equal("paused"))
		generation := f.job(paused, "references").AttemptGeneration
		_, err = f.s.db.Exec(`UPDATE projects SET permissions_revision=permissions_revision-1 WHERE project_id=?`, h.Library.UnfiledProjectID)
		Expect(err).NotTo(HaveOccurred())
		f.run(paused, "references")
		Expect(f.job(paused, "references").AttemptGeneration).To(BeNumerically(">", generation))
		broken := f.publish(nil, html)
		f.run(broken, "search")
		f.bytes.fail = domain.ErrContentIntegrity
		err = references.NewWorker(f.s, f.bytes).Process(ctx, f.job(broken, "references").ID)
		f.bytes.fail = nil
		Expect(err).To(HaveOccurred())
		Expect(f.job(broken, "references").State).To(Equal("failed"))
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error { var err error; m, err = tx.ReferenceMaterialization(broken); return err })).To(Succeed())
		Expect(m.State).To(Equal("failed"))
		failed := f.job(broken, "references")
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			_, err := tx.EnqueueReferences(broken, domain.ReferenceRevision)
			return err
		})).To(Succeed())
		Expect(f.job(broken, "references")).To(Equal(failed))
	})

	It("RF07-REV sweeps every historical version search-first and preserves stable edges (RV-SWEEP)", func() {
		target := f.publish(nil, html)
		f.run(target, "search")
		regionReference := f.declaration(target, "sweep-region")
		pageReference := f.declaration(target, "sweep-page")
		pageReference["region_id"] = nil
		pageReference["from"] = map[string]any{"page_path": "page.html", "region_id": nil}
		regionSource := f.publish([]any{regionReference, pageReference}, html)
		f.run(regionSource, "search")
		f.run(regionSource, "references")
		pageBefore, err := f.edge(regionSource, "sweep-page")
		Expect(err).NotTo(HaveOccurred())
		Expect(pageBefore.TargetPagePath).NotTo(BeNil())
		Expect(pageBefore.TargetRegionID).To(BeNil())
		historical := f.publish(nil, html)
		newer := f.publishVersion(nil, html, historical)
		f.run(historical, "search")
		f.run(historical, "references")
		f.run(newer, "search")
		f.run(newer, "references")
		original := f.job(historical, "references")
		router := work.NewMaintenance(f.s, f.bytes)
		router.References.ReferenceRevision = domain.ReferenceRevision + 1
		for i := 0; i < 20; i++ {
			done, err := router.Sweep(ctx)
			Expect(err).NotTo(HaveOccurred())
			if done {
				break
			}
		}
		var published, current int
		Expect(f.s.db.QueryRow(`SELECT count(*) FROM document_versions WHERE sequence IS NOT NULL`).Scan(&published)).To(Succeed())
		Expect(f.s.db.QueryRow(`SELECT count(*) FROM derived_work WHERE work_kind='references' AND extractor_revision=?`, domain.ReferenceRevision+1).Scan(&current)).To(Succeed())
		Expect(current).To(Equal(published))
		// Replay retains the same jobs even across another startup sweep.
		var before int
		Expect(f.s.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&before)).To(Succeed())
		again := work.NewMaintenance(f.s, f.bytes)
		again.References.ReferenceRevision = domain.ReferenceRevision + 1
		for i := 0; i < 20; i++ {
			done, err := again.Sweep(ctx)
			Expect(err).NotTo(HaveOccurred())
			if done {
				break
			}
		}
		var after int
		Expect(f.s.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&after)).To(Succeed())
		Expect(after).To(Equal(before))
		Expect(f.job(historical, "references").ID).NotTo(Equal(original.ID))
		old, err := f.s.Job(ctx, original.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(old).To(Equal(original))
		f.run(historical, "references")
		var source, id string
		Expect(f.s.db.QueryRow(`SELECT from_version_id,metadata_reference_id FROM authored_edges WHERE from_version_id=? AND metadata_reference_id=? AND target_region_id IS NOT NULL AND target_page_path IS NOT NULL`, regionSource, "sweep-region").Scan(&source, &id)).To(Succeed())
		oldEdge, err := f.edge(source, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(oldEdge.TargetRegionID).NotTo(BeNil(), "the drift assertion requires a region-bearing edge")
		Expect(oldEdge.TargetPagePath).NotTo(BeNil())
		f.run(source, "references")
		pageAfter, err := f.edge(regionSource, "sweep-page")
		Expect(err).NotTo(HaveOccurred())
		Expect(pageAfter).To(Equal(pageBefore), "page-only identity and immutable tuple survive revision revalidation")
		same, err := f.edge(source, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(same).To(Equal(oldEdge), "revision revalidation keeps identity and tuple")
		// A changed recorded anchor hash cannot silently re-pin this edge.
		_, err = f.s.db.Exec(`UPDATE page_anchors SET text_sha256=? WHERE version_id=? AND page_path=? AND anchor_id=?`, digest([]byte("drift")), oldEdge.TargetVersionID, *oldEdge.TargetPagePath, *oldEdge.TargetRegionID)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			_, err := tx.EnqueueReferences(source, domain.ReferenceRevision+2)
			return err
		})).To(Succeed())
		f.run(source, "references")
		same, err = f.edge(source, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(same).To(Equal(oldEdge), "anchor_drift retains old canonical edge")
		// Typed permanent-source cleanup never follows target pins into rows
		// owned by another source (RV-CLEANUP-TARGET).
		clear := func(version string) {
			done := false
			for i := 0; i < 20 && !done; i++ {
				Expect(f.s.Write(ctx, func(tx app.WriteTx) error { var err error; done, err = tx.ClearReferenceData(version, 1); return err })).To(Succeed())
			}
			Expect(done).To(BeTrue())
		}
		clear(oldEdge.TargetVersionID)
		same, err = f.edge(source, id)
		Expect(err).NotTo(HaveOccurred(), "target cleanup preserves other sources' incoming edge")
		Expect(same).To(Equal(oldEdge))
		clear(source)
		_, err = f.edge(source, id)
		Expect(err).To(MatchError(domain.ErrNotFound))

	})
})
