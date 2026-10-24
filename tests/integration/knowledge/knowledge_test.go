package knowledge_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	sec "haeryong.local/haeryong/internal/security/session"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/adapters/gitreader"
	auth "haeryong.local/haeryong/internal/application/auth"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

// These three composition checks share one tiny synthetic library. Exhaustive
// search/reference matrices and real-source readers retain their existing owners.
type world struct {
	ctx                                 context.Context
	selection                           service.Selection
	writer                              *service.LockedProfile
	store                               *foundation.Store
	closeDB                             func() error
	auth                                *auth.Service
	imports                             *importer.Service
	query                               *query.Service
	token, target, referring, targetDoc string
}

func id() string           { v, e := service.NewID(); Expect(e).NotTo(HaveOccurred()); return v }
func hash(b []byte) string { d := sha256.Sum256(b); return hex.EncodeToString(d[:]) }
func field(r domain.Response, k string) string {
	var v map[string]any
	Expect(json.Unmarshal(r.Body, &v)).To(Succeed())
	return v[k].(string)
}
func (w *world) close() {
	if w.closeDB != nil {
		Expect(w.closeDB()).To(Succeed())
		w.closeDB = nil
	}
	if w.writer != nil {
		Expect(w.writer.Close()).To(Succeed())
		w.writer = nil
	}
}
func (w *world) open() {
	var e error
	w.writer, e = service.OpenWriter(w.selection)
	Expect(e).NotTo(HaveOccurred())
	Expect(w.writer.PrepareStorageBootstrap(nil)).To(Succeed())
	w.store = foundation.New(nil)
	_, w.closeDB, e = w.store.Prepare(w.ctx, w.writer.Profile)
	Expect(e).NotTo(HaveOccurred())
	Expect(w.writer.MarkStorageReady(nil)).To(Succeed())
	w.auth = auth.New(w.store)
	w.imports = importer.New(w.store, w.auth, w.store, staging.New())
	w.query = query.New(w.store, w.auth, w.store)
}
func (w *world) sql(fn func(*sql.DB)) {
	w.close()
	db, e := sql.Open("sqlite", filepath.Join(w.selection.DataDir, "storage/library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	fn(db)
	Expect(db.Close()).To(Succeed())
	w.open()
}
func (w *world) publish(project, body string, metadata map[string]any) string {
	target, _ := json.Marshal(map[string]any{"project_id": project, "document_id": nil})
	created, e := w.imports.Create(w.ctx, w.token, id(), importer.CreateRequest{Purpose: "result", InputMode: "directory", SelectionName: "knowledge", Target: target})
	Expect(e).NotTo(HaveOccurred())
	upload := field(created, "upload_id")
	data := []byte(body)
	n := int64(len(data))
	ack, e := w.imports.Put(w.ctx, w.token, upload, importer.Chunk{Path: "page.html", Kind: "file", Hash: hash(data), Length: &n, Final: true}, bytes.NewReader(data))
	Expect(e).NotTo(HaveOccurred())
	checkpoint := ack.Checkpoint
	checkpoint.Generation++
	checkpoint.Complete = true
	sealed, e := w.imports.Complete(w.ctx, w.token, upload, id(), checkpoint)
	Expect(e).NotTo(HaveOccurred())
	Expect(w.imports.Process(w.ctx, field(sealed, "job_id"))).To(Succeed())
	raw, _ := json.Marshal(metadata)
	published, e := w.imports.Import(w.ctx, w.token, id(), importer.ImportRequest{UploadID: upload, DocumentPresent: true, ProjectID: project, Metadata: bytes.NewReader(raw)})
	Expect(e).NotTo(HaveOccurred())
	Expect(w.imports.Process(w.ctx, field(published, "job_id"))).To(Succeed())
	var version string
	Expect(w.store.Write(w.ctx, func(tx app.WriteTx) error {
		jobs, e := tx.Jobs()
		for _, j := range jobs {
			if j.ID == field(published, "job_id") {
				var result map[string]any
				Expect(json.Unmarshal(j.Result, &result)).To(Succeed())
				version, _ = result["version_id"].(string)
			}
		}
		return e
	})).To(Succeed())
	Expect(version).NotTo(BeEmpty())
	w.drain()
	return version
}
func (w *world) drain() {
	router := work.NewMaintenance(w.store, w.store)
	for turn := 0; turn < 300; turn++ {
		var jobs []domain.Job
		Expect(w.store.Write(w.ctx, func(tx app.WriteTx) error { var e error; jobs, e = tx.WorkJobs(32); return e })).To(Succeed())
		if len(jobs) == 0 {
			return
		}
		for _, j := range jobs {
			Expect(router.Process(w.ctx, j.ID)).To(Succeed())
		}
	}
	Fail("bounded synthetic derived work did not finish")
}

// Seed the existing workflow credential contract; no grant issuer is added.
func (w *world) sourceOnly() string {
	token, e := sec.Secret()
	Expect(e).NotTo(HaveOccurred())
	principal := id()
	Expect(w.store.Write(w.ctx, func(tx app.WriteTx) error {
		version, e := tx.PublishedVersion(w.referring)
		if e != nil {
			return e
		}
		doc, e := tx.QueryDocument(version.DocumentID)
		if e != nil {
			return e
		}
		scope, e := tx.Scope(doc.ProjectID)
		if e != nil {
			return e
		}
		a, e := tx.AuthState()
		if e != nil {
			return e
		}
		grant := domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{doc.ProjectID}, VersionIDs: []string{w.referring}}
		if e = tx.PutWorkflowPrincipal(domain.WorkflowPrincipal{ID: principal, BindingID: principal, Active: true, Scope: grant, Revision: 1}); e != nil {
			return e
		}
		return tx.PutScopedCredential(domain.ScopedCredential{ID: principal, PrincipalID: principal, TokenHash: sec.Verifier("workflow", token), AuthEpoch: a.AuthEpoch, Scope: grant, Permissions: map[string]int64{doc.ProjectID: scope.Project.PermissionsRevision}, ConfigRevision: scope.ConfigRevision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond), Revision: 1})
	})).To(Succeed())
	return token
}
func (w *world) resolve(reference string) query.ReferenceView {
	v, e := w.query.ResolveReference(w.ctx, w.token, w.referring, reference, query.ReferenceOptions{})
	Expect(e).NotTo(HaveOccurred())
	return v
}
func (w *world) graph() query.BacklinkPage {
	v, e := w.query.Backlinks(w.ctx, w.token, w.target, query.BacklinkOptions{})
	Expect(e).NotTo(HaveOccurred())
	return v
}

var _ = Describe("T6 two-project composition", Ordered, Label("persistence"), func() {
	var w *world
	BeforeAll(func() {
		w = &world{ctx: context.Background(), selection: service.Selection{Profile: "knowledge", DataDir: filepath.Join(GinkgoT().TempDir(), "library")}}
		_, e := service.Initialize(w.selection)
		Expect(e).NotTo(HaveOccurred())
		w.open()
		DeferCleanup(w.close)
		pair, e := w.auth.CreatePairing(w.ctx, "http://127.0.0.1:9270", "knowledge owner", "test")
		Expect(e).NotTo(HaveOccurred())
		_, e = w.auth.BootstrapDecide(w.ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: id(), Action: "approve"})
		Expect(e).NotTo(HaveOccurred())
		poll, e := w.auth.Poll(w.ctx, pair.ID, pair.PollingSecret, pair.Origin)
		Expect(e).NotTo(HaveOccurred())
		w.token = poll.Token
		projects := []string{id(), id()}
		Expect(w.store.Write(w.ctx, func(tx app.WriteTx) error {
			for _, p := range projects {
				if e := tx.CreateProject(domain.Project{ID: p, Name: "synthetic knowledge", Lifecycle: "active", Revision: 1, PermissionsRevision: 1}); e != nil {
					return e
				}
			}
			return nil
		})).To(Succeed())
		w.target = w.publish(projects[0], "<p id='answer'>saved exact answer</p>", map[string]any{"sources": []any{map[string]any{"id": "retained", "kind": "snapshot", "retention_mode": "reference_only", "snapshot_sha256": hash(nil)}}, "evidence": []any{map[string]any{"id": "citation", "source_id": "retained", "capture_status": "reference_only", "path": "missing.go"}}})
		target, e := w.store.Version(w.ctx, w.target)
		Expect(e).NotTo(HaveOccurred())
		w.targetDoc = target.DocumentID
		w.referring = w.publish(projects[1], "<p id='origin'><a data-haeryong-reference='pin'>prior answer</a></p>", map[string]any{"references": []any{map[string]any{"id": "pin", "label": "private target label", "document_id": w.targetDoc, "version_id": w.target, "page_path": "page.html", "region_id": "answer", "from": map[string]any{"page_path": "page.html", "region_id": "origin"}}, map[string]any{"id": "missing-anchor", "document_id": w.targetDoc, "version_id": w.target, "page_path": "page.html", "region_id": "missing"}, map[string]any{"id": "unknown", "document_id": id(), "version_id": id(), "page_path": "page.html", "region_id": nil}}})
	})
	It("T-004-06-01 reopens imported search, exact pins and separate verification history", func() {
		before := w.resolve("pin")
		Expect(before.Resolution).To(Equal("resolved"))
		Expect(before.Label).NotTo(BeNil())
		Expect(*before.Label).To(Equal("private target label"))
		Expect(w.graph().Items).To(HaveLen(1))
		page, e := w.query.Search(w.ctx, w.token, query.SearchOptions{Text: "saved exact answer", Versions: "all", Options: query.Options{Limit: 20}})
		Expect(e).NotTo(HaveOccurred())
		Expect(page.Items).NotTo(BeEmpty())
		Expect(page.Items[0].VersionID).To(Equal(w.target))
		verify := source.New(w.store, w.auth, w.store, gitreader.New())
		key := id()
		r, e := verify.Verify(w.ctx, w.token, w.target, "citation", key, []byte(`{}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(201))
		var check map[string]any
		Expect(json.Unmarshal(r.Body, &check)).To(Succeed())
		Expect(check["outcome"]).To(Equal("unavailable"))
		version, e := w.store.Version(w.ctx, w.target)
		Expect(e).NotTo(HaveOccurred())
		w.close()
		w.open()
		Expect(w.resolve("pin")).To(Equal(before))
		after, e := w.store.Version(w.ctx, w.target)
		Expect(e).NotTo(HaveOccurred())
		Expect(after).To(Equal(version))
		history, e := w.query.Verifications(w.ctx, w.token, w.target, "citation", query.Options{})
		Expect(e).NotTo(HaveOccurred())
		Expect(history.Items).To(HaveLen(1))
		for _, key := range []string{"verification_id", "outcome", "checked_at"} {
			Expect(history.Items[0][key]).To(Equal(check[key]))
		}
		replay, e := source.New(w.store, w.auth, w.store, gitreader.New()).Verify(w.ctx, w.token, w.target, "citation", key, []byte(`{}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(replay).To(Equal(r))
	})
	It("T-004-06-02 rebuilds derived references without changing authored inputs", func() {
		before := w.resolve("pin")
		graph := w.graph()
		version, e := w.store.Version(w.ctx, w.referring)
		Expect(e).NotTo(HaveOccurred())
		key := id()
		r, e := w.query.RebuildVersionIndexes(w.ctx, w.token, w.referring, key, "references")
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(202))
		w.drain()
		Expect(w.resolve("pin")).To(Equal(before))
		Expect(w.graph()).To(Equal(graph))
		after, e := w.store.Version(w.ctx, w.referring)
		Expect(e).NotTo(HaveOccurred())
		Expect(after).To(Equal(version))
		validation, e := w.query.ValidateVersionIndexes(w.ctx, w.token, w.referring, "references")
		Expect(e).NotTo(HaveOccurred())
		Expect(validation.State).To(Equal("consistent"))
		replay, e := w.query.RebuildVersionIndexes(w.ctx, w.token, w.referring, key, "references")
		Expect(e).NotTo(HaveOccurred())
		Expect(replay).To(Equal(r))
	})
	It("T-004-06-03 distinguishes unresolved, masked and historical gone targets", func() {
		Expect(w.resolve("missing-anchor").Resolution).To(Equal("unresolved"))
		scoped := w.sourceOnly()
		masked, e := w.query.ResolveReference(w.ctx, scoped, w.referring, "pin", query.ReferenceOptions{})
		Expect(e).To(MatchError(domain.ErrNotFound))
		Expect(masked.Target).To(BeNil())
		Expect(masked.Label).To(BeNil())
		Expect(masked.EdgeID).To(BeNil())
		restricted, e := w.query.References(w.ctx, scoped, w.referring, query.Options{})
		Expect(e).NotTo(HaveOccurred())
		Expect(restricted.Counts.EdgeCount).To(Equal("0"))
		Expect(restricted.Counts.Documents).To(Equal("0"))
		unknown, e := w.query.ResolveReference(w.ctx, w.token, w.referring, "unknown", query.ReferenceOptions{})
		Expect(e).To(MatchError(domain.ErrNotFound))
		Expect(unknown.Resolution).To(Equal("unavailable"))
		w.sql(func(db *sql.DB) {
			_, e := db.Exec("UPDATE documents SET lifecycle='trash' WHERE document_id=?", w.targetDoc)
			Expect(e).NotTo(HaveOccurred())
		})
		hidden, e := w.query.ResolveReference(w.ctx, w.token, w.referring, "pin", query.ReferenceOptions{})
		Expect(e).To(MatchError(domain.ErrNotFound))
		Expect(hidden.Resolution).To(Equal("unavailable"))
		Expect(hidden.Target).To(BeNil())
		Expect(hidden.ReaderURL).To(BeNil())
		w.sql(func(db *sql.DB) {
			_, e := db.Exec("UPDATE documents SET lifecycle='purged' WHERE document_id=?", w.targetDoc)
			Expect(e).NotTo(HaveOccurred())
		})
		_, e = w.query.ResolveReference(w.ctx, w.token, w.referring, "pin", query.ReferenceOptions{})
		gone := &query.TargetGone{}
		Expect(e).To(BeAssignableToTypeOf(gone))
		Expect(e.(*query.TargetGone).Target).To(Equal(query.GoneTarget{DocumentID: w.targetDoc, VersionID: w.target}))
		masked, e = w.query.ResolveReference(w.ctx, scoped, w.referring, "pin", query.ReferenceOptions{})
		Expect(e).To(MatchError(domain.ErrNotFound))
		Expect(masked.Target).To(BeNil())
		list, e := w.query.References(w.ctx, w.token, w.referring, query.Options{})
		Expect(e).NotTo(HaveOccurred())
		raw, _ := json.Marshal(list)
		Expect(strings.Contains(string(raw), "saved exact answer")).To(BeFalse())
		Expect(list.Counts.EdgeCount).To(Equal("0"))
	})
})
