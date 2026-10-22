package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/search"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type searchBytes struct {
	files  map[string]string
	fail   error
	onRead func(string)
}

func (b *searchBytes) Read(_ context.Context, o domain.Object, _ domain.Pin, consume func(io.ReadSeeker) error) error {
	if b.onRead != nil {
		b.onRead(o.Hash)
	}
	if b.fail != nil {
		return b.fail
	}
	return consume(strings.NewReader(b.files[o.Hash]))
}

type searchObserved struct {
	app.Store
	afterBody func()
	saw       bool
}
type searchObservedTx struct {
	app.WriteTx
	owner *searchObserved
}

func (t searchObservedTx) PutSearchChunk(c domain.SearchChunk) error {
	err := t.WriteTx.PutSearchChunk(c)
	if err == nil && c.Part == "body" {
		t.owner.saw = true
	}
	return err
}
func (s *searchObserved) Write(ctx context.Context, f func(app.WriteTx) error) error {
	err := s.Store.Write(ctx, func(tx app.WriteTx) error { return f(searchObservedTx{tx, s}) })
	if err == nil && s.saw && s.afterBody != nil {
		s.saw = false
		f := s.afterBody
		s.afterBody = nil
		f()
	}
	return err
}

func workerSeed(s *Store) (string, *searchBytes) {
	bytes := &searchBytes{files: map[string]string{}}
	h, err := s.Health(ctx)
	Expect(err).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.CreateDocument(domain.Document{ID: "worker-doc", ProjectID: h.Library.UnfiledProjectID, Title: "Worker"}); err != nil {
			return err
		}
		if err := tx.CreateContentSet("worker-set", digest([]byte("inventory"))); err != nil {
			return err
		}
		for _, path := range []string{"a.html", "b.html", "untouched.bin"} {
			text := "<html><title>Authored</title><p id='retry'>readSource 한국어 " + strings.Repeat("tail ", 10000) + " EOF-" + path + "</p></html>"
			hash := digest([]byte(text))
			bytes.files[hash] = text
			if err := tx.PutDurableObject(domain.Object{Hash: hash, Size: int64(len(text)), State: "ready"}); err != nil {
				return err
			}
			if err := tx.PutEntry(domain.Entry{ContentSetID: "worker-set", Path: path, Kind: "file", Hash: hash, Size: int64(len(text))}); err != nil {
				return err
			}
		}
		if err := tx.SealContentSet("worker-set"); err != nil {
			return err
		}
		if err := tx.PutVersion(domain.Version{ID: "worker-version", DocumentID: "worker-doc", ContentSetID: "worker-set", Title: "Worker", ViewerProfile: "html_js_v1", SourceBasis: "unverified"}); err != nil {
			return err
		}
		for _, path := range []string{"a.html", "b.html"} {
			if err := tx.PutPage(domain.Page{VersionID: "worker-version", Path: path, Metadata: []byte(`{"label":"SuppliedLabel"}`)}); err != nil {
				return err
			}
			if err := tx.PutRegion(domain.Region{VersionID: "worker-version", PagePath: path, ID: "retry"}); err != nil {
				return err
			}
		}
		return tx.PublishVersion("worker-version", 1)
	})).To(Succeed())
	var index domain.SearchIndex
	Expect(s.Write(ctx, func(tx app.WriteTx) error { var err error; index, err = tx.SearchIndex("worker-version"); return err })).To(Succeed())
	return index.JobID, bytes
}
func driveSearch(s *Store, w *search.Worker, id string) domain.Job {
	for turn := 0; turn < 100; turn++ {
		j, err := s.Job(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		if terminal(j.State) {
			return j
		}
		Expect(w.Process(ctx, id)).To(Succeed())
	}
	Fail("derived work failed to settle")
	return domain.Job{}
}
func searchCount(s *Store, query string, args ...any) int {
	var n int
	Expect(s.db.QueryRowContext(ctx, query, args...).Scan(&n)).To(Succeed())
	return n
}
func toExtract(s *Store, w *search.Worker, id string) {
	for i := 0; i < 20; i++ {
		var index domain.SearchIndex
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; index, e = tx.SearchIndex("worker-version"); return e })).To(Succeed())
		if index.DiscoveryComplete {
			return
		}
		Expect(w.Process(ctx, id)).To(Succeed())
	}
	Fail("inventory failed to advance")
}

var _ = Describe("durable search work SC12–SC15", Label("persistence"), func() {
	It("SC12 atomically enqueues once, reports decimal checkpoints and completes every page without following attachments", func() {
		_, p := profile(GinkgoT())
		s := openStore(GinkgoT(), p, nil)
		id, b := workerSeed(s)
		Expect(searchCount(s, `SELECT count(*) FROM derived_work WHERE version_id='worker-version'`)).To(Equal(1))
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			again, e := tx.EnqueueSearch("worker-version", domain.SearchExtractorRevision)
			Expect(again).To(Equal(id))
			return e
		})).To(Succeed())
		w := search.NewWorker(s, b)
		j := driveSearch(s, w, id)
		Expect(j.State).To(Equal("completed"))
		Expect(searchCount(s, `SELECT count(*) FROM search_units WHERE kind='page' AND body_state='complete'`)).To(Equal(2))
		Expect(searchCount(s, `SELECT count(*) FROM page_anchors WHERE anchor_id='retry'`)).To(Equal(2))
		Expect(searchCount(s, `SELECT count(*) FROM search_chunks c JOIN search_units u USING(unit_id) WHERE u.kind='page' AND c.part='metadata' AND instr(c.terms,'suppliedlabel')>0`)).To(Equal(2))
		Expect(string(j.Result)).To(ContainSubstring(`"pages_done":"2"`))
		Expect(string(j.Result)).To(ContainSubstring(`"files_done":"3"`))
		Expect(string(j.Result)).To(ContainSubstring(`"stage":"complete"`))
		Expect(searchCount(s, `SELECT count(*) FROM search_units WHERE kind='attachment' AND body_state='unsupported'`)).To(Equal(1))
		// Publication and enqueue roll back together, including their events.
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.PutVersion(domain.Version{ID: "rolled-back", DocumentID: "worker-doc", BaseVersionID: "worker-version", ContentSetID: "worker-set", ViewerProfile: "html_js_v1", SourceBasis: "unverified"}); e != nil {
				return e
			}
			if e := tx.PublishVersion("rolled-back", 2); e != nil {
				return e
			}
			return domain.ErrConflict
		})).To(MatchError(domain.ErrConflict))
		Expect(searchCount(s, `SELECT count(*) FROM document_versions WHERE version_id='rolled-back'`)).To(BeZero())
		Expect(searchCount(s, `SELECT count(*) FROM derived_work WHERE version_id='rolled-back'`)).To(BeZero())
	})
	It("SC13 keeps staged bodies hidden, resumes only the unfinished page and rejects the pre-restart generation", func() {
		_, p := profile(GinkgoT())
		s := openStore(GinkgoT(), p, nil)
		id, b := workerSeed(s)
		w := search.NewWorker(s, b)
		toExtract(s, w, id)
		Expect(searchCount(s, `SELECT count(*) FROM search_units WHERE kind='page' AND state='ready' AND body_state='pending'`)).To(Equal(2))
		Expect(w.Process(ctx, id)).To(Succeed())
		Expect(searchCount(s, `SELECT count(*) FROM search_units WHERE kind='page' AND body_state='complete'`)).To(Equal(1))
		interrupted, cancel := context.WithCancel(ctx)
		observed := &searchObserved{Store: s, afterBody: cancel}
		old, _ := s.Job(ctx, id)
		err := search.NewWorker(observed, b).Process(interrupted, id)
		Expect(err).To(HaveOccurred())
		Expect(searchCount(s, `SELECT count(*) FROM search_chunks c JOIN search_units u USING(unit_id) WHERE c.part='body' AND u.kind='page' AND u.body_state='pending'`)).To(BeNumerically(">", 0))
		Expect(searchCount(s, `SELECT count(*) FROM search_units WHERE kind='page' AND body_state='complete'`)).To(Equal(1))
		Expect(s.Close()).To(Succeed())
		s = openStore(GinkgoT(), p, nil)
		recovered, _ := s.Job(ctx, id)
		Expect(recovered.State).To(Equal("interrupted"))
		Expect(recovered.AttemptGeneration).To(BeNumerically(">", old.AttemptGeneration))
		Expect(search.NewWorker(s, b).Process(ctx, id)).To(Succeed()) // Claim the recovered generation; stale fencing must hold while running too.
		Expect(s.Write(ctx, func(tx app.WriteTx) error { _, e := tx.SearchFence(id, old.AttemptGeneration); return e })).To(MatchError(domain.ErrStaleWorker))
		reads := 0
		b.onRead = func(string) { reads++ }
		j := driveSearch(s, search.NewWorker(s, b), id)
		Expect(j.State).To(Equal("completed"))
		Expect(reads).To(Equal(1))
		Expect(searchCount(s, `SELECT count(*) FROM search_units WHERE kind='page' AND body_state='complete'`)).To(Equal(2))
	})
	It("SC14 persists ENOSPC without restart retry; a new extractor revision quarantines then replaces the index", func() {
		_, p := profile(GinkgoT())
		s := openStore(GinkgoT(), p, nil)
		id, b := workerSeed(s)
		w := search.NewWorker(s, b)
		toExtract(s, w, id)
		b.fail = syscall.ENOSPC
		Expect(w.Process(ctx, id)).To(HaveOccurred())
		j, err := s.Job(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(j.State).To(Equal("failed"))
		Expect(j.Error).To(Equal("resource_exhausted"))
		generation := j.AttemptGeneration
		Expect(s.Close()).To(Succeed())
		s = openStore(GinkgoT(), p, nil)
		w = search.NewWorker(s, b)
		_, err = w.Sweep(ctx)
		Expect(err).NotTo(HaveOccurred())
		same, _ := s.Job(ctx, id)
		Expect(same.State).To(Equal("failed"))
		Expect(same.AttemptGeneration).To(Equal(generation))
		Expect(searchCount(s, `SELECT count(*) FROM derived_work`)).To(Equal(1))
		var next string
		w = search.NewWorker(s, b)
		w.ExtractorRevision++
		_, err = w.Sweep(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			index, e := tx.SearchIndex("worker-version")
			next = index.JobID
			Expect(index.State).To(Equal("pending"))
			Expect(index.Reason).To(BeEmpty())
			return e
		})).To(Succeed())
		Expect(next).NotTo(Equal(id))
		b.fail = nil
		Expect(driveSearch(s, w, next).State).To(Equal("completed"))
		Expect(searchCount(s, `SELECT count(*) FROM derived_work`)).To(Equal(2))
		prior, _ := s.Job(ctx, id)
		Expect(prior.State).To(Equal("failed"))
	})
	It("SC15 fences cancellation and live trash, and typed cleanup removes both FTS postings while retaining jobs", func() {
		_, p := profile(GinkgoT())
		s := openStore(GinkgoT(), p, nil)
		id, b := workerSeed(s)
		w := search.NewWorker(s, b)
		toExtract(s, w, id)
		old, _ := s.Job(ctx, id)
		observed := &searchObserved{Store: s, afterBody: func() {
			Expect(s.Write(ctx, func(tx app.WriteTx) error { _, e := tx.CancelJob(id); return e })).To(Succeed())
		}}
		Expect(search.NewWorker(observed, b).Process(ctx, id)).To(HaveOccurred())
		j, _ := s.Job(ctx, id)
		Expect(j.State).To(Equal("cancelled"))
		Expect(s.Write(ctx, func(tx app.WriteTx) error { _, e := tx.SearchFence(id, old.AttemptGeneration); return e })).To(MatchError(domain.ErrStaleWorker))
		Expect(searchCount(s, `SELECT count(*) FROM search_units WHERE kind='page' AND body_state='complete'`)).To(BeZero())
		_, err := w.Sweep(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(searchCount(s, `SELECT count(*) FROM derived_work`)).To(Equal(1))
		// A new revision supplies a distinct active generation for lifecycle fencing.
		var next string
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			var e error
			next, e = tx.EnqueueSearch("worker-version", domain.SearchExtractorRevision+1)
			return e
		})).To(Succeed())
		toExtract(s, w, next)
		observed = &searchObserved{Store: s, afterBody: func() {
			_, e := s.db.Exec(`UPDATE documents SET lifecycle='trash' WHERE document_id='worker-doc'`)
			Expect(e).NotTo(HaveOccurred())
		}}
		Expect(search.NewWorker(observed, b).Process(ctx, next)).To(HaveOccurred())
		j, _ = s.Job(ctx, next)
		Expect(j.State).To(Equal("paused"))
		Expect(searchCount(s, `SELECT count(*) FROM search_units WHERE kind='page' AND body_state='complete'`)).To(BeZero())
		Expect(searchCount(s, `SELECT count(*) FROM search_words WHERE search_words MATCH 'readsource'`)).To(BeNumerically(">", 0))
		Expect(searchCount(s, `SELECT count(*) FROM search_grams WHERE search_grams MATCH 'sou'`)).To(BeNumerically(">", 0))
		for i := 0; i < 100; i++ {
			var done bool
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				var e error
				done, e = tx.DeleteDerivedVersion("worker-version", 1)
				return e
			})).To(Succeed())
			if done {
				break
			}
		}
		for _, table := range []string{"search_units", "search_chunks", "page_anchors", "search_indexes", "derived_work"} {
			Expect(searchCount(s, `SELECT count(*) FROM `+table)).To(BeZero())
		}
		Expect(searchCount(s, `SELECT count(*) FROM search_words WHERE search_words MATCH 'readsource'`)).To(BeZero())
		Expect(searchCount(s, `SELECT count(*) FROM search_grams WHERE search_grams MATCH 'sou'`)).To(BeZero())
		Expect(searchCount(s, `SELECT count(*) FROM jobs WHERE kind='derived_index'`)).To(Equal(2))
		Expect(searchCount(s, `SELECT count(*) FROM version_entries`)).To(Equal(3))
	})
})

var _ = It("SC12 inventories region tuples without separator collisions or path normalization", Label("persistence"), func() {
	_, p := profile(GinkgoT())
	s := openStore(GinkgoT(), p, nil)
	_, b := workerSeed(s)
	_ = b
	// This is an explicit internal pre-publication metadata fixture: the product
	// importer accepts exact control characters other than NUL in ordinary paths.
	_, err := s.db.Exec(`INSERT INTO version_pages VALUES('worker-version',?,NULL)`, "a\x01b")
	Expect(err).NotTo(HaveOccurred())
	for _, tuple := range [][2]string{{"a.html", "b\x01c"}, {"a.html\x01b", "c"}, {"a\x01b", "c"}} {
		_, err = s.db.Exec(`INSERT OR IGNORE INTO version_pages VALUES('worker-version',?,NULL)`, tuple[0])
		Expect(err).NotTo(HaveOccurred())
		_, err = s.db.Exec(`INSERT INTO regions VALUES('worker-version',?,?,NULL)`, tuple[0], tuple[1])
		Expect(err).NotTo(HaveOccurred())
	}
	after := ""
	seen := map[[2]string]bool{}
	for i := 0; i < 10; i++ {
		var rows []domain.SearchInventoryItem
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			var e error
			rows, e = tx.SearchInventory("worker-version", "region", after, 1)
			return e
		})).To(Succeed())
		if len(rows) == 0 {
			break
		}
		u := rows[0].Unit
		tuple := [2]string{u.PagePath, u.RegionID}
		Expect(seen[tuple]).To(BeFalse())
		seen[tuple] = true
		after = rows[0].Key
	}
	Expect(seen).To(HaveLen(5))
})

var _ = It("SC14 classifies read failure safely and startup backfills an upgraded publication without work", Label("persistence"), func() {
	_, p := profile(GinkgoT())
	s := openStore(GinkgoT(), p, nil)
	id, b := workerSeed(s)
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var done bool
		var err error
		for !done {
			done, err = tx.DeleteDerivedVersion("worker-version", 64)
			if err != nil {
				return err
			}
		}
		return nil
	})).To(Succeed())
	w := search.NewWorker(s, b)
	_, err := w.Sweep(ctx)
	Expect(err).NotTo(HaveOccurred())
	var next string
	Expect(s.Write(ctx, func(tx app.WriteTx) error { index, e := tx.SearchIndex("worker-version"); next = index.JobID; return e })).To(Succeed())
	Expect(next).NotTo(Equal(id))
	toExtract(s, w, next)
	b.fail = io.ErrClosedPipe
	Expect(w.Process(ctx, next)).To(HaveOccurred())
	j, _ := s.Job(ctx, next)
	Expect(j.State).To(Equal("failed"))
	Expect(j.Error).To(Equal("read_failed"))
	Expect(string(j.Result)).To(ContainSubstring(`"failed":"1"`))
	for _, original := range b.files {
		Expect(original).To(ContainSubstring("EOF-"))
	}
})

var _ = It("SC12 selects all three kinds fairly even when derived continuations fill the batch", Label("persistence"), func() {
	_, p := profile(GinkgoT())
	s := openStore(GinkgoT(), p, nil)
	id, _ := workerSeed(s)
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		for i := 0; i < 70; i++ {
			if e := tx.CreateJob(domain.Job{ID: strings.Repeat("0", i+1), Kind: "derived_index"}); e != nil {
				return e
			}
		}
		for _, kind := range []string{"upload_seal", "import"} {
			if e := tx.CreateJob(domain.Job{ID: "z-" + kind, Kind: kind}); e != nil {
				return e
			}
		}
		_, e := tx.ClaimJob(id)
		return e
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.WorkJobs(3)
		if e != nil {
			return e
		}
		kinds := []string{}
		for _, j := range rows {
			kinds = append(kinds, j.Kind)
		}
		Expect(kinds).To(ConsistOf("derived_index", "upload_seal", "import"))
		return nil
	})).To(Succeed())
})

// These are synthetic immutable-input fixtures, installed before dispatch.
func replaceWorkerHTML(s *Store, b *searchBytes, path, text string) {
	hash := digest([]byte(text))
	b.files[hash] = text
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.PutDurableObject(domain.Object{Hash: hash, Size: int64(len(text)), State: "ready"})
	})).To(Succeed())
	_, err := s.db.Exec(`UPDATE version_entries SET content_hash=?,byte_size=? WHERE content_set_id='worker-set' AND relative_path=?`, hash, len(text), path)
	Expect(err).NotTo(HaveOccurred())
}

var _ = It("G1 revalidates unrelated settings and pauses narrower authority without losing resumable checkpoints (reversal: searchAuthority/SuspendSearch/ResumeSearch)", Label("persistence"), func() {
	_, p := profile(GinkgoT())
	s := openStore(GinkgoT(), p, nil)
	id, b := workerSeed(s)
	w := search.NewWorker(s, b)
	toExtract(s, w, id)
	config, err := s.Configuration(ctx)
	Expect(err).NotTo(HaveOccurred())
	var value map[string]any
	Expect(json.Unmarshal(config.Value, &value)).To(Succeed())
	value["security"].(map[string]any)["remember_device_default"] = false
	config.Value, err = json.Marshal(value)
	Expect(err).NotTo(HaveOccurred())
	config.Revision++
	config.Actor = "owner"
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.SetConfiguration(config, config.Revision-1) })).To(Succeed())
	Expect(w.Process(ctx, id)).To(Succeed())
	Expect(searchCount(s, `SELECT count(*) FROM search_units WHERE kind='page' AND body_state='complete'`)).To(Equal(1))
	observed := &searchObserved{Store: s, afterBody: func() {
		_, e := s.db.Exec(`UPDATE documents SET lifecycle='trash' WHERE document_id='worker-doc'`)
		Expect(e).NotTo(HaveOccurred())
	}}
	Expect(search.NewWorker(observed, b).Process(ctx, id)).To(HaveOccurred())
	paused, _ := s.Job(ctx, id)
	Expect(paused.State).To(Equal("paused"))
	Expect(paused.Error).To(BeEmpty())
	Expect(searchCount(s, `SELECT count(*) FROM search_units WHERE kind='page' AND body_state='complete'`)).To(Equal(1))
	_, err = s.db.Exec(`UPDATE documents SET lifecycle='active' WHERE document_id='worker-doc'`)
	Expect(err).NotTo(HaveOccurred())
	Expect(driveSearch(s, w, id).State).To(Equal("completed"))
	// A distinct revision exercises permission narrowing and explicit internal
	// reauthorization; no HTTP resume operation is introduced.
	var next string
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		next, e = tx.EnqueueSearch("worker-version", domain.SearchExtractorRevision+1)
		return e
	})).To(Succeed())
	toExtract(s, w, next)
	observed = &searchObserved{Store: s, afterBody: func() {
		_, e := s.db.Exec(`UPDATE projects SET permissions_revision=permissions_revision+1`)
		Expect(e).NotTo(HaveOccurred())
	}}
	Expect(search.NewWorker(observed, b).Process(ctx, next)).To(HaveOccurred())
	paused, _ = s.Job(ctx, next)
	Expect(paused.State).To(Equal("paused"))
	captured := string(paused.Scope)
	generation := paused.AttemptGeneration
	Expect(s.Close()).To(Succeed())
	s = openStore(GinkgoT(), p, nil)
	w = search.NewWorker(s, b)
	_, err = w.Sweep(ctx)
	Expect(err).NotTo(HaveOccurred())
	Expect(w.Process(ctx, next)).To(Succeed())
	paused, _ = s.Job(ctx, next)
	Expect(paused.State).To(Equal("paused"))
	Expect(paused.AttemptGeneration).To(Equal(generation))
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ResumeSearch(next, generation, false) })).To(MatchError(domain.ErrSearchRestricted))
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ResumeSearch(next, generation, true) })).To(Succeed())
	done := driveSearch(s, w, next)
	Expect(done.State).To(Equal("completed"))
	Expect(string(done.Scope)).To(Equal(captured))
	Expect(done.AttemptGeneration).To(BeNumerically(">", generation))
})

var _ = It("G2 indexes exact page-local DOM regions without supplied metadata and excludes ambiguous anchors (reversal: derived region creation)", Label("persistence"), func() {
	_, p := profile(GinkgoT())
	s := openStore(GinkgoT(), p, nil)
	id, b := workerSeed(s)
	_, err := s.db.Exec(`DELETE FROM regions WHERE version_id='worker-version'`)
	Expect(err).NotTo(HaveOccurred())
	replaceWorkerHTML(s, b, "a.html", `<section id=retry aria-labelledby=label><h2 id=label>Page A label</h2><p>UniqueA body</p></section><i id=dup>one</i><b id=dup>two</b><p id=supplied>SuppliedBody</p><div id=empty></div>`)
	replaceWorkerHTML(s, b, "b.html", `<section id=retry aria-labelledby=label><h2 id=label>Page B label</h2><p>UniqueB body</p></section>`)
	_, err = s.db.Exec(`INSERT INTO regions VALUES('worker-version','a.html','supplied',?)`, []byte(`{"label":"Authored label"}`))
	Expect(err).NotTo(HaveOccurred())
	Expect(driveSearch(s, search.NewWorker(s, b), id).State).To(Equal("completed"))
	for _, row := range []struct{ page, label, body string }{{"a.html", "Page A label", "uniquea body"}, {"b.html", "Page B label", "uniqueb body"}} {
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			u, e := tx.SearchRegionUnit("worker-version", row.page, "retry")
			if e != nil {
				return e
			}
			Expect(u.Title).To(Equal(row.label))
			Expect(u.State).To(Equal("ready"))
			Expect(u.BodyState).To(Equal("complete"))
			Expect(u.AnchorState).To(Equal("present"))
			return nil
		})).To(Succeed())
		Expect(searchCount(s, `SELECT count(*) FROM search_chunks c JOIN search_units u USING(unit_id) WHERE u.kind='region' AND u.page_path=? AND u.region_id='retry' AND c.ordinal%2=0 AND instr(c.display_body,?)>0`, row.page, row.body)).To(Equal(1))
	}
	Expect(searchCount(s, `SELECT count(*) FROM search_units WHERE kind='region' AND region_id='dup'`)).To(BeZero())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		u, e := tx.SearchRegionUnit("worker-version", "a.html", "empty")
		Expect(u.State).To(Equal("ready"))
		Expect(u.AnchorState).To(Equal("present"))
		return e
	})).To(Succeed())
	supplied, err := s.Region(ctx, "worker-version", "a.html", "supplied")
	Expect(err).NotTo(HaveOccurred())
	Expect(string(supplied.Metadata)).To(Equal(`{"label":"Authored label"}`))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		u, e := tx.SearchRegionUnit("worker-version", "a.html", "supplied")
		Expect(u.Title).To(ContainSubstring("Authored label"))
		return e
	})).To(Succeed())
})

var _ = It("G3 indexes undeclared UTF-8 Korean text and anchor ids after the 1024-byte prescan (reversal: shared Reader decoding)", Label("persistence"), func() {
	_, p := profile(GinkgoT())
	s := openStore(GinkgoT(), p, nil)
	id, b := workerSeed(s)
	replaceWorkerHTML(s, b, "a.html", "<!--"+strings.Repeat("ascii ", 300)+"--><section id='재시도'><h2>승인 복구</h2>한글 본문</section>")
	Expect(driveSearch(s, search.NewWorker(s, b), id).State).To(Equal("completed"))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		a, e := tx.PageAnchor("worker-version", "a.html", "재시도")
		Expect(a.Label).To(Equal("승인 복구"))
		return e
	})).To(Succeed())
	Expect(searchCount(s, `SELECT count(*) FROM search_chunks c JOIN search_units u USING(unit_id) WHERE u.kind='page' AND u.page_path='a.html' AND c.ordinal%2=0 AND instr(c.display_body,'한글 본문')>0`)).To(Equal(1))
})

var _ = It("G4 classifies wrapped EDQUOT as resource_exhausted in job and index (reversal: EDQUOT admission)", Label("persistence"), func() {
	_, p := profile(GinkgoT())
	s := openStore(GinkgoT(), p, nil)
	id, b := workerSeed(s)
	w := search.NewWorker(s, b)
	toExtract(s, w, id)
	b.fail = fmt.Errorf("synthetic quota: %w", syscall.EDQUOT)
	Expect(w.Process(ctx, id)).To(HaveOccurred())
	j, err := s.Job(ctx, id)
	Expect(err).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("failed"))
	Expect(j.Error).To(Equal("resource_exhausted"))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		index, e := tx.SearchIndex("worker-version")
		Expect(index.Reason).To(Equal("resource_exhausted"))
		return e
	})).To(Succeed())
})
