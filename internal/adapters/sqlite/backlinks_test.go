package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/references"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
)

// Observe admission to a real Store.Write without replacing its gate or SQL.
type backlinkObservedStore struct {
	app.Store
	entered chan struct{}
	once    sync.Once
}

func (s *backlinkObservedStore) Write(ctx context.Context, fn func(app.WriteTx) error) error {
	s.once.Do(func() { close(s.entered) })
	return s.Store.Write(ctx, fn)
}

// SQL-only damage in a disposable, exclusively held fixture. The final DDL is
// byte-identical: one reverse key differs from its canonical edge. Keep the
// index cardinality intact: this engine's quick_check checks entry counts.
func damageReverseIndex(s *Store, path, target, wrong string) {
	damageReverseIndexWith(s, path, target, func(original string) string {
		return strings.Replace(original, "target_version_id,", fmt.Sprintf(`(CASE WHEN metadata_reference_id='C06_FAULT' THEN '%s' ELSE target_version_id END),`, wrong), 1)
	})
}
func damageReverseIndexWith(s *Store, path, target string, transform func(string) string) {
	GinkgoHelper()
	var seq int
	var name, actual string
	Expect(s.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &actual)).To(Succeed())
	Expect(name).To(Equal("main"))
	Expect(actual).To(Equal(path))
	// profile() deliberately uses a path containing ? and #. A bare DSN treats
	// those as URI syntax and silently opens a different empty database.
	uri := url.URL{Scheme: "file", Path: actual}
	q := uri.Query()
	q.Set("mode", "rw")
	for _, pragma := range []string{"busy_timeout(5000)", "foreign_keys(ON)", "journal_mode(WAL)", "synchronous(FULL)"} {
		q.Add("_pragma", pragma)
	}
	uri.RawQuery = q.Encode()
	var original string
	Expect(s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='authored_edges_target'`).Scan(&original)).To(Succeed())
	Expect(s.Close()).To(Succeed())
	phase := func(fn func(*sql.DB)) {
		db, err := sql.Open("sqlite", uri.String())
		Expect(err).NotTo(HaveOccurred())
		db.SetMaxOpenConns(1)
		defer db.Close()
		var definition string
		Expect(db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name='authored_edges_target'`).Scan(&definition)).To(Succeed())
		Expect(definition).NotTo(BeEmpty())
		fn(db)
	}
	phase(func(db *sql.DB) {
		_, err := db.Exec(`PRAGMA writable_schema=ON`)
		Expect(err).NotTo(HaveOccurred())
		temporary := transform(original)
		Expect(temporary).NotTo(Equal(original))
		result, err := db.Exec(`UPDATE sqlite_master SET sql=? WHERE name='authored_edges_target'`, temporary)
		Expect(err).NotTo(HaveOccurred())
		affected, err := result.RowsAffected()
		Expect(err).NotTo(HaveOccurred())
		Expect(affected).To(Equal(int64(1)))
		_, err = db.Exec(`PRAGMA writable_schema=OFF`)
		Expect(err).NotTo(HaveOccurred())
	})
	phase(func(db *sql.DB) {
		_, err := db.Exec(`REINDEX main.authored_edges_target`)
		Expect(err).NotTo(HaveOccurred())
	})
	phase(func(db *sql.DB) {
		_, err := db.Exec(`PRAGMA writable_schema=ON`)
		Expect(err).NotTo(HaveOccurred())
		result, err := db.Exec(`UPDATE sqlite_master SET sql=? WHERE name='authored_edges_target'`, original)
		Expect(err).NotTo(HaveOccurred())
		affected, err := result.RowsAffected()
		Expect(err).NotTo(HaveOccurred())
		Expect(affected).To(Equal(int64(1)))
		_, err = db.Exec(`PRAGMA writable_schema=OFF`)
		Expect(err).NotTo(HaveOccurred())
	})
	phase(func(db *sql.DB) {
		var canonical, indexed int
		Expect(db.QueryRow(`SELECT count(*) FROM authored_edges NOT INDEXED`).Scan(&canonical)).To(Succeed())
		Expect(db.QueryRow(`SELECT count(*) FROM authored_edges INDEXED BY authored_edges_target WHERE target_version_id=?`, target).Scan(&indexed)).To(Succeed())
		fmt.Fprintf(GinkgoWriter, "C06 probe canonical=%d indexed=%d\n", canonical, indexed)
		rows, err := db.Query(`PRAGMA quick_check`)
		Expect(err).NotTo(HaveOccurred())
		defer rows.Close()
		for rows.Next() {
			var diagnostic string
			Expect(rows.Scan(&diagnostic)).To(Succeed())
			fmt.Fprintln(GinkgoWriter, "C06 quick_check:", diagnostic)
		}
		Expect(rows.Err()).NotTo(HaveOccurred())
	})
}

var _ = Describe("BL C06 intact-definition reverse index", Label("persistence"), func() {
	It("refuses startup with a reordered reverse-index definition", func() {
		_, w := profile(GinkgoT())
		s := openStore(GinkgoT(), w, nil)
		_, err := s.db.Exec(`DROP INDEX authored_edges_target; CREATE INDEX authored_edges_target ON authored_edges(target_version_id,target_region_id,target_page_path,from_version_id,metadata_reference_id)`)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.validateTables(ctx, SchemaRevision)).NotTo(Succeed())
		Expect(s.Close()).To(Succeed())
		failed := New(nil)
		_, _, err = failed.Prepare(ctx, w.Profile)
		Expect(err).To(HaveOccurred())
		Expect(failed.db).To(BeNil())
	})
	It("detects real SQL-only content divergence while normal preparation succeeds", func() {
		s, auth, w, _ := authFixture(GinkgoT())
		_, owner := pairOwner(GinkgoT(), auth, "C06 owner")
		f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
		target := f.publish(nil, "<p id='retry'>target</p>")
		f.run(target, "search")
		f.run(target, "references")
		wrong := f.publish(nil, "<p id='retry'>wrong target</p>")
		f.run(wrong, "search")
		f.run(wrong, "references")
		source := f.publish([]any{f.declaration(target, "C06_FAULT")}, "<p id='retry'>source</p>")
		f.run(source, "search")
		f.run(source, "references")
		originalEdge, e := f.edge(source, "C06_FAULT")
		Expect(e).NotTo(HaveOccurred())
		version, e := s.Version(ctx, source)
		Expect(e).NotTo(HaveOccurred())
		pin := domain.Pin{ID: f.id(), Kind: "job", OwnerID: f.job(source, "references").ID, Hash: version.ManifestSHA}
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.AcquirePin(pin) })).To(Succeed())
		damageReverseIndex(s, filepath.Join(w.Profile.DataDir, "storage", "library.sqlite"), target, wrong)
		s = New(nil)
		_, closeStore, err := s.Prepare(ctx, w.Profile)
		Expect(err).NotTo(HaveOccurred())
		defer closeStore()
		var canonical, indexed int
		Expect(s.db.QueryRow(`SELECT count(*) FROM authored_edges NOT INDEXED WHERE metadata_reference_id='C06_FAULT'`).Scan(&canonical)).To(Succeed())
		Expect(s.db.QueryRow(`SELECT count(*) FROM authored_edges INDEXED BY authored_edges_target WHERE target_version_id=?`, target).Scan(&indexed)).To(Succeed())
		Expect(canonical).To(Equal(1))
		Expect(indexed).To(Equal(0), "stop: fixture must establish actual content divergence")
		f.s = s
		auth = appauth.New(s)
		q := query.New(s, auth, f.bytes)
		// The damaged covering key points into an admitted target, while the
		// canonical endpoint is denied. Its mismatch must reveal no edge/count.
		h, e := s.Health(ctx)
		Expect(e).NotTo(HaveOccurred())
		limited := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{h.Library.UnfiledProjectID}, VersionIDs: []string{source, wrong}})
		hidden, e := q.Backlinks(ctx, limited, wrong, query.BacklinkOptions{})
		Expect(e).NotTo(HaveOccurred())
		Expect(hidden.Items).To(BeEmpty())
		Expect(hidden.Counts).To(Equal(query.BacklinkCounts{EdgeCount: "0", Documents: "0"}))
		bad, err := q.ValidateVersionIndexes(ctx, owner.Token, target, "references")
		Expect(err).NotTo(HaveOccurred())
		Expect(bad.State).To(Equal("inconsistent"))
		Expect(bad.Counts.Missing).NotTo(Equal("0"))
		before := referenceRowsSnapshot(s)
		response, err := q.RebuildVersionIndexes(ctx, owner.Token, target, f.id(), "references")
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(202))
		// Cancel daemon context after REINDEX has run, before its transaction
		// commits. The statement and private checkpoint must roll back together.
		stopped, cancel := context.WithCancel(ctx)
		defer cancel()
		commits := 0
		s.fault = func(b Boundary) error {
			if b == BeforeCommit {
				commits++
				if commits == 3 {
					cancel()
					return context.Canceled
				}
			}
			return nil
		}
		repair := f.job(target, "references")
		Expect(references.NewWorker(s, f.bytes).Process(stopped, repair.ID)).To(MatchError(context.Canceled))
		s.fault = nil
		Expect(commits).To(Equal(3))
		var checkpoint domain.DerivedWork
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; checkpoint, e = tx.DerivedWork(repair.ID); return e })).To(Succeed())
		var phase struct{ Phase string }
		Expect(json.Unmarshal([]byte(checkpoint.AfterPath), &phase)).To(Succeed())
		Expect(phase.Phase).To(Equal("repair"))
		Expect(s.db.QueryRow(`SELECT count(*) FROM authored_edges INDEXED BY authored_edges_target WHERE target_version_id=?`, target).Scan(&indexed)).To(Succeed())
		Expect(indexed).To(Equal(0))
		f.restart()
		s = f.s
		defer func() { f.s.Close() }()
		q = query.New(s, appauth.New(s), f.bytes)
		f.run(target, "references")
		good, err := q.ValidateVersionIndexes(ctx, owner.Token, target, "references")
		Expect(err).NotTo(HaveOccurred())
		Expect(good.State).To(Equal("consistent"))
		Expect(referenceRowsSnapshot(s)).NotTo(Equal(before))
		Expect(s.db.QueryRow(`SELECT count(*) FROM authored_edges INDEXED BY authored_edges_target WHERE target_version_id=?`, target).Scan(&indexed)).To(Succeed())
		Expect(indexed).To(Equal(1))
		restored, e := f.edge(source, "C06_FAULT")
		Expect(e).NotTo(HaveOccurred())
		Expect(restored).To(Equal(originalEdge))
		var retained int
		Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE pin_id=?`, pin.ID).Scan(&retained)).To(Succeed())
		Expect(retained).To(Equal(1))
	})
})

var _ = Describe("BL selected-scope projections and recovery", Label("persistence"), func() {
	It("reconciles edges/documents, masks hidden sources and re-enters terminal work with receipts", func() {
		s, auth, w, _ := authFixture(GinkgoT())
		_, poll := pairOwner(GinkgoT(), auth, "backlink reader")
		f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
		target := f.publish(nil, "<section id='retry'>target</section>")
		f.run(target, "search")
		f.run(target, "references")
		a := f.declaration(target, "a")
		a["label"] = "F10 backlink label"
		b := f.declaration(target, "b")
		source := f.publish([]any{a, b}, "<section id='retry'>source</section>")
		f.run(source, "search")
		f.run(source, "references")
		source2 := f.publishVersion([]any{f.declaration(target, "c")}, "<section id='retry'>second version</section>", source)
		f.run(source2, "search")
		f.run(source2, "references")
		q := query.New(s, auth, f.bytes)
		before := referenceRowsSnapshot(s)
		page, err := q.Backlinks(ctx, poll.Token, target, query.BacklinkOptions{Options: query.Options{Limit: 1}})
		Expect(err).NotTo(HaveOccurred())
		Expect(page.Items).To(HaveLen(1))
		Expect(page.NextCursor).NotTo(BeNil())
		Expect(page.Counts).To(Equal(query.BacklinkCounts{EdgeCount: "3", Documents: "1"}))
		more, err := q.Backlinks(ctx, poll.Token, target, query.BacklinkOptions{Options: query.Options{Limit: 1, Cursor: *page.NextCursor}})
		Expect(err).NotTo(HaveOccurred())
		Expect(more.Items[0].EdgeID).NotTo(Equal(page.Items[0].EdgeID))
		outgoing, err := q.References(ctx, poll.Token, source, query.Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(outgoing.Counts).To(Equal(query.OutgoingCounts{EdgeCount: "2", Documents: "1"}))
		Expect(referenceRowsSnapshot(s)).To(Equal(before))
		// Another referring document changes owner totals. Its denied-only
		// lifecycle change must not invalidate the admitted document's cursor.
		other := f.publish([]any{f.declaration(target, "other")}, "<section id='retry'>other</section>")
		f.run(other, "search")
		f.run(other, "references")
		ownerTotals, err := q.Backlinks(ctx, poll.Token, target, query.BacklinkOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(ownerTotals.Counts).To(Equal(query.BacklinkCounts{EdgeCount: "4", Documents: "2"}))
		h, _ := s.Health(ctx)
		scoped := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{h.Library.UnfiledProjectID}, VersionIDs: []string{target, source, source2}})
		visible, err := q.Backlinks(ctx, scoped, target, query.BacklinkOptions{Options: query.Options{Limit: 1}})
		Expect(err).NotTo(HaveOccurred())
		Expect(visible.Counts).To(Equal(query.BacklinkCounts{EdgeCount: "3", Documents: "1"}))
		continuation, err := q.Backlinks(ctx, scoped, target, query.BacklinkOptions{Options: query.Options{Limit: 1, Cursor: *visible.NextCursor}})
		Expect(err).NotTo(HaveOccurred())
		_, err = s.db.Exec(`UPDATE documents SET lifecycle='trash' WHERE document_id=(SELECT document_id FROM document_versions WHERE version_id=?)`, other)
		Expect(err).NotTo(HaveOccurred())
		unchanged, err := q.Backlinks(ctx, scoped, target, query.BacklinkOptions{Options: query.Options{Limit: 1, Cursor: *visible.NextCursor}})
		Expect(err).NotTo(HaveOccurred())
		Expect(unchanged).To(Equal(continuation))
		_, err = s.db.Exec(`UPDATE documents SET lifecycle='active' WHERE document_id=(SELECT document_id FROM document_versions WHERE version_id=?)`, other)
		Expect(err).NotTo(HaveOccurred())
		targetOnly := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{h.Library.UnfiledProjectID}, VersionIDs: []string{target}})
		hidden, err := q.Backlinks(ctx, targetOnly, target, query.BacklinkOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(hidden.Items).To(BeEmpty())
		Expect(hidden.Counts.EdgeCount).To(Equal("0"))
		_, err = q.ValidateVersionIndexes(ctx, targetOnly, target, "references")
		Expect(err).To(MatchError(domain.ErrDenied))
		_, err = s.db.Exec(`UPDATE documents SET lifecycle='trash' WHERE document_id=(SELECT document_id FROM document_versions WHERE version_id=?)`, source)
		Expect(err).NotTo(HaveOccurred())
		hidden, err = q.Backlinks(ctx, poll.Token, target, query.BacklinkOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(hidden.Items).To(HaveLen(1))
		Expect(hidden.Items[0].From.VersionID).To(Equal(other))
		Expect(hidden.Counts).To(Equal(query.BacklinkCounts{EdgeCount: "1", Documents: "1"}))
		_, err = s.db.Exec(`UPDATE documents SET lifecycle='active' WHERE document_id=(SELECT document_id FROM document_versions WHERE version_id=?)`, source)
		Expect(err).NotTo(HaveOccurred())
		original := f.job(target, "references")
		key := f.id()
		reply, err := q.RebuildVersionIndexes(ctx, poll.Token, target, key, "references")
		Expect(err).NotTo(HaveOccurred())
		Expect(reply.Status).To(Equal(202))
		replay, err := q.RebuildVersionIndexes(ctx, poll.Token, target, key, "references")
		Expect(err).NotTo(HaveOccurred())
		Expect(replay.Body).To(Equal(reply.Body))
		_, err = q.RebuildVersionIndexes(ctx, poll.Token, target, f.id(), "references")
		Expect(err).To(MatchError(domain.ErrConflict))
		Expect(f.job(target, "references").ID).NotTo(Equal(original.ID))
		old, err := s.Job(ctx, original.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(old.State).To(Equal("completed"))
		cancelled := f.job(target, "references")
		_, err = auth.CancelJob(ctx, poll.Token, cancelled.ID, f.id())
		Expect(err).NotTo(HaveOccurred())
		ended, err := s.Job(ctx, cancelled.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ended.State).To(Equal("cancelled"))
		_, err = q.RebuildVersionIndexes(ctx, poll.Token, target, f.id(), "references")
		Expect(err).NotTo(HaveOccurred())
		// A real owner cancellation reaches the gate while REINDEX/checkpoint
		// hold it. It cannot return durable success before that step commits.
		afterStep := f.job(target, "references")
		observed := &backlinkObservedStore{Store: s, entered: make(chan struct{})}
		cancelAuth := appauth.New(observed)
		cancelResult := make(chan error, 1)
		cancelKey := f.id()
		commits := 0
		s.fault = func(b Boundary) error {
			if b == BeforeCommit {
				commits++
				if commits == 3 {
					go func() {
						_, e := cancelAuth.CancelJob(ctx, poll.Token, afterStep.ID, cancelKey)
						cancelResult <- e
					}()
					<-observed.entered
					select {
					case e := <-cancelResult:
						return fmt.Errorf("owner cancel escaped the held store gate: %v", e)
					default:
					}
				}
			}
			return nil
		}
		Expect(references.NewWorker(s, f.bytes).Process(ctx, afterStep.ID)).To(Succeed())
		Expect(<-cancelResult).To(Succeed())
		s.fault = nil
		var progress domain.DerivedWork
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; progress, e = tx.DerivedWork(afterStep.ID); return e })).To(Succeed())
		var phase struct{ Phase string }
		Expect(json.Unmarshal([]byte(progress.AfterPath), &phase)).To(Succeed())
		Expect(phase.Phase).To(Equal("declarations"))
		Expect(references.NewWorker(s, f.bytes).Process(ctx, afterStep.ID)).To(Succeed())
		ended, err = s.Job(ctx, afterStep.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(ended.State).To(Equal("cancelled"))
		Expect(referenceRowsSnapshot(s)).NotTo(BeEmpty())
		_, err = q.RebuildVersionIndexes(ctx, poll.Token, target, f.id(), "references")
		Expect(err).NotTo(HaveOccurred())
		f.run(target, "references")
		validation, err := q.ValidateVersionIndexes(ctx, poll.Token, target, "references")
		Expect(err).NotTo(HaveOccurred())
		Expect(validation.State).To(Equal("consistent"))
		edge, e := f.edge(source, "a")
		Expect(e).NotTo(HaveOccurred())
		_, err = s.db.Exec(`UPDATE documents SET lifecycle='purged' WHERE document_id=(SELECT document_id FROM document_versions WHERE version_id=?)`, target)
		Expect(err).NotTo(HaveOccurred())
		_, err = q.RebuildVersionIndexes(ctx, poll.Token, source, f.id(), "references")
		Expect(err).NotTo(HaveOccurred())
		f.run(source, "references")
		preserved, e := f.edge(source, "a")
		Expect(e).NotTo(HaveOccurred())
		Expect(preserved).To(Equal(edge))
		gone, err := q.References(ctx, poll.Token, source, query.Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(gone.Counts.EdgeCount).To(Equal("0"))
		Expect(gone.Items[0].Resolution).To(Equal("tombstoned"))
		Expect(gone.Items[0].Label).To(BeNil())
		Expect(gone.Items[0].Target).To(Equal(query.GoneTarget{DocumentID: edge.TargetDocumentID, VersionID: target}))
	})
})

var _ = Describe("BL physical edge slices", Label("persistence"), func() {
	It("advances across NULL anchors in 64-raw-row slices and seeks exact target scopes", func() {
		_, w := profile(GinkgoT())
		s := openStore(GinkgoT(), w, nil)
		m := referenceSeed(s)
		records := []app.PreparedReference{}
		edges := []domain.AuthoredEdge{}
		for i := 0; i < 73; i++ {
			r := referenceRecord(fmt.Sprintf("bulk-%03d", i))
			if i < 70 {
				r.TargetPagePath = nil
				r.TargetRegionID = nil
			} else if i == 70 {
				r.TargetRegionID = nil
			} else if i == 72 {
				r.TargetPagePath = refText("b.html")
			}
			var raw map[string]any
			Expect(json.Unmarshal(r.AuthoredJSON, &raw)).To(Succeed())
			raw["page_path"] = r.TargetPagePath
			raw["region_id"] = r.TargetRegionID
			r.AuthoredJSON, _ = json.Marshal(raw)
			records = append(records, prepareReference(r))
			edge := referenceEdge(s, r)
			if r.TargetPagePath == nil {
				edge.TargetPageSHA = nil
			}
			if r.TargetRegionID == nil {
				edge.TargetAnchorSHA = nil
			}
			edges = append(edges, edge)
		}
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.PutReferenceMaterialization(m); e != nil {
				return e
			}
			for i, r := range records {
				if e := tx.PutAuthoredReference(r); e != nil {
					return e
				}
				if e := tx.PutAuthoredEdge(edges[i]); e != nil {
					return e
				}
			}
			return nil
		})).To(Succeed())
		var after *domain.EdgeKey
		seen := []string{}
		sizes := []int{}
		for {
			var keys []domain.EdgeKey
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				var e error
				keys, e = tx.IncomingEdgeKeys(refTarget, nil, nil, after, 64)
				return e
			})).To(Succeed())
			sizes = append(sizes, len(keys))
			if len(keys) == 0 {
				break
			}
			Expect(len(keys)).To(BeNumerically("<=", 64))
			for _, k := range keys {
				seen = append(seen, k.Reference)
				after = &k
			}
		}
		Expect(sizes).To(Equal([]int{64, 9, 0}))
		Expect(seen).To(HaveLen(73))
		Expect(seen[70:]).To(Equal([]string{"bulk-070", "bulk-071", "bulk-072"}))
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.IncomingEdgeKeys(refTarget, refText("a.html"), refText("retry"), nil, 64)
			Expect(rows).To(HaveLen(1))
			return e
		})).To(Succeed())
	})
})
