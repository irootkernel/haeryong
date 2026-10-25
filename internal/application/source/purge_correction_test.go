package source_test

import (
	"context"
	"database/sql"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/content/staging"
	"haeryong.local/haeryong/internal/adapters/contentstore"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"path/filepath"
	"time"
)

func acceptSourcePurge(f *vFixture) domain.LifecycleResult {
	GinkgoHelper()
	q := query.New(f.store, f.auth, f.store)
	ctx := context.Background()
	var result domain.LifecycleResult
	for _, action := range []string{"trash", "purge"} {
		d, e := f.store.Document(ctx, f.document)
		Expect(e).NotTo(HaveOccurred())
		raw, _ := json.Marshal(map[string]any{"operation": action, "expected_revision": d.Revision})
		response, e := q.LifecyclePlanCreate(ctx, f.token, "", f.document, raw)
		Expect(e).NotTo(HaveOccurred())
		var plan domain.LifecyclePage
		Expect(json.Unmarshal(response.Body, &plan)).To(Succeed())
		raw, _ = json.Marshal(map[string]any{"plan_id": plan.ID, "expected_revision": plan.ExpectedRevision, "confirmed": true})
		response, e = q.LifecycleCommit(ctx, f.token, plan.ID, "", raw)
		Expect(e).NotTo(HaveOccurred())
		Expect(json.Unmarshal(response.Body, &result)).To(Succeed())
	}
	return result
}
func sourcePurgeTurns(f *vFixture, id string, n int) domain.Job {
	GinkgoHelper()
	clock := time.Now()
	p := &work.Purger{Store: f.store, Bytes: f.store, Now: func() time.Time { return clock }}
	var j domain.Job
	for turn := 0; turn < n; turn++ {
		clock = clock.Add(61 * time.Second)
		Expect(p.Process(context.Background(), id)).To(Succeed())
		var e error
		j, e = f.store.Job(context.Background(), id)
		Expect(e).NotTo(HaveOccurred())
		if j.State == "completed" {
			break
		}
	}
	return j
}

var _ = It("Purge C1 F3 real referenced verification releases exclusive excerpts and preserves shared and independent pins", Label("persistence"), func() {
	f := newVerificationFixture(GinkgoT())
	ctx := context.Background()
	core := importer.New(f.store, f.auth, f.store, staging.New())
	f.reader.blobs[gitRevision+"\x00pkg/main.go"] = "package main\n\nfunc main() {}\n"
	f.buildVersion(gitMeta(gitRevision), `{"id":"ev","source_id":"src","capture_status":"reference_only","path":"pkg/main.go","line_start":3,"line_end":3}`, "", nil)
	f.configure(gitMapping(GinkgoT().TempDir()))
	excerpts := []string{"func main() {}\n", "func main() {}"}
	for _, text := range excerpts {
		operation := opKey(GinkgoT())
		upload := stageBodyExpectationAs(GinkgoT(), f, core, f.token, operation, []byte(text))
		response, e := f.verify(operation, `{"expected_excerpt_ref":{"upload_id":"`+upload+`"}}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(201))
		_, e = core.Abandon(ctx, f.token, upload, opKey(GinkgoT()))
		Expect(e).NotTo(HaveOccurred())
	}
	shared := vsha([]byte(excerpts[1]))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		document, version, set := opKey(GinkgoT()), opKey(GinkgoT()), opKey(GinkgoT())
		if e := tx.CreateDocument(domain.Document{ID: document, ProjectID: f.project, Title: "Retained shared excerpt"}); e != nil {
			return e
		}
		if e := tx.CreateContentSet(set, vsha([]byte(excerpts[1]))); e != nil {
			return e
		}
		if e := tx.PutEntry(domain.Entry{ContentSetID: set, Path: "excerpt.txt", Kind: "file", Hash: shared, Size: int64(len(excerpts[1]))}); e != nil {
			return e
		}
		if e := tx.SealContentSet(set); e != nil {
			return e
		}
		if e := tx.PutVersion(domain.Version{ID: version, DocumentID: document, ContentSetID: set, Title: "Retained shared excerpt", ViewerProfile: "html_js_v1", SourceBasis: "evidence_recorded", PayloadSHA: vsha([]byte(excerpts[1])), ManifestSHA: vsha(nil)}); e != nil {
			return e
		}
		if e := tx.PublishVersion(version, 1); e != nil {
			return e
		}
		return tx.AcquirePin(domain.Pin{ID: "unknown-active", Kind: "unknown_reader", OwnerID: "independent", Hash: shared})
	})).To(Succeed())
	r := acceptSourcePurge(f)
	j := sourcePurgeTurns(f, r.JobID, 150)
	Expect(j.State).To(Equal("paused"))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { _, e := tx.Object(vsha([]byte(excerpts[0]))); return e })).To(MatchError(domain.ErrNotFound))
	Expect(f.store.Objects.VerifiedRead(ctx, contentstore.Object{Hash: shared, Size: int64(len(excerpts[1]))}, func(io.ReadSeeker) error { return nil })).To(Succeed())
	db, e := sql.Open("sqlite", filepath.Join(f.dataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	var count int
	Expect(db.QueryRow(`SELECT count(*) FROM object_references WHERE owner_kind='verification'`).Scan(&count)).To(Succeed())
	Expect(count).To(Equal(0))
	Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE pin_id='unknown-active'`).Scan(&count)).To(Succeed())
	Expect(count).To(Equal(1))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.ReleasePin("unknown-active") })).To(Succeed())
	Expect(f.store.Collect(ctx, domain.Object{Hash: shared, Size: int64(len(excerpts[1]))})).To(MatchError(domain.ErrPinned))
})
var _ = It("Purge C1 F4 real observation reservation and intermediate capture cannot revive a terminal target", Label("persistence"), func() {
	for _, barrier := range []string{"resolve", "diff"} {
		f := newObservationFixture(GinkgoT(), "current")
		ctx := context.Background()
		operation := opKey(GinkgoT())
		entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		released := false
		DeferCleanup(func() {
			if !released {
				close(release)
				<-done
			}
		})
		f.core.Reader = &parkingReader{inner: f.core.Reader, at: barrier, park: func() { close(entered); <-release }}
		go func() { defer GinkgoRecover(); _, e := f.observe(operation, ""); done <- e }()
		<-entered
		r := acceptSourcePurge(&f.vFixture)
		j := sourcePurgeTurns(&f.vFixture, r.JobID, 5)
		Expect(j.State).To(Equal("running"))
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			i, e := tx.PurgeIntent(r.JobID)
			Expect(i.MetadataRemoved).To(BeFalse())
			return e
		})).To(Succeed())
		close(release)
		released = true
		Expect(<-done).NotTo(HaveOccurred())
		sourcePurgeTurns(&f.vFixture, r.JobID, 150)
		owner := ownerPrincipal(&f.vFixture)
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			_, exists, e := tx.ObservationRunForOperation(owner, operation)
			Expect(exists).To(BeFalse())
			i, e2 := tx.CheckIntentByOperation(owner, operation)
			Expect(i.State).To(Equal("failed"))
			if e != nil {
				return e
			}
			return e2
		})).To(Succeed())
		db, e := sql.Open("sqlite", filepath.Join(f.dataDir, "storage", "library.sqlite"))
		Expect(e).NotTo(HaveOccurred())
		var count int
		Expect(db.QueryRow(`SELECT count(*) FROM observation_runs WHERE version_id=?`, f.version).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(0))
		if barrier == "resolve" {
			Expect(db.QueryRow(`SELECT count(*) FROM captured_target_sets`).Scan(&count)).To(Succeed())
			Expect(count).To(Equal(0))
		}
		Expect(db.Close()).To(Succeed())
	}
})
