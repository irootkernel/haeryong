package query_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"bytes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/contentstore"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"path/filepath"
	"sync"
	"time"
)

func purgeAccept(f *fixture) (domain.LifecyclePage, domain.LifecycleResult) {
	GinkgoHelper()
	lifecycleCommit(f, lifecyclePlan(f, "trash"))
	p := lifecyclePlan(f, "purge")
	return p, lifecycleCommit(f, p)
}
func purgeTurns(f *fixture, p *work.Purger, id string, n int) domain.Job {
	GinkgoHelper()
	var j domain.Job
	for turn := 0; turn < n; turn++ {
		Expect(p.Process(ctx, id)).To(Succeed())
		var e error
		j, e = f.store.Job(ctx, id)
		Expect(e).NotTo(HaveOccurred())
		if j.State == "completed" {
			break
		}
	}
	return j
}

var _ = It("Purge T-005-04-01 exact irreversible winner replay and parallel confirmation", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	lifecycleCommit(f, lifecyclePlan(f, "trash"))
	p := lifecyclePlan(f, "purge")
	restore := lifecyclePlan(f, "restore")
	var wg sync.WaitGroup
	var re, pe error
	var result domain.Response
	wg.Add(2)
	go func() {
		defer wg.Done()
		result, pe = f.queries.LifecycleCommit(ctx, f.token, p.ID, "", lifecycleBody(p))
	}()
	go func() {
		defer wg.Done()
		_, re = f.queries.LifecycleCommit(ctx, f.token, restore.ID, "", lifecycleBody(restore))
	}()
	wg.Wait()
	Expect((pe == nil) != (re == nil)).To(BeTrue())
	if pe != nil {
		lifecycleCommit(f, lifecyclePlan(f, "trash"))
		p = lifecyclePlan(f, "purge")
		result, pe = f.queries.LifecycleCommit(ctx, f.token, p.ID, "", lifecycleBody(p))
		Expect(pe).NotTo(HaveOccurred())
	}
	Expect(result.Status).To(Equal(202))
	var r domain.LifecycleResult
	Expect(json.Unmarshal(result.Body, &r)).To(Succeed())
	Expect(r.JobID).NotTo(BeEmpty())
	replay, e := f.queries.LifecycleCommit(ctx, f.token, p.ID, key(GinkgoT()), lifecycleBody(p))
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(result))
	_, e = f.auth.CancelJob(ctx, f.token, r.JobID, key(GinkgoT()))
	Expect(e).To(HaveOccurred())
	j, e := f.auth.Job(ctx, f.token, r.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.CancelRequested).To(BeFalse())
	d, e := f.store.Document(ctx, f.document)
	Expect(e).NotTo(HaveOccurred())
	Expect(d.Title).To(BeEmpty())
	Expect(d.Lifecycle).To(Equal("purged"))
})

type flakyCollector struct {
	target interface {
		Collect(context.Context, domain.Object) error
	}
	fail bool
}

func (c *flakyCollector) Collect(ctx context.Context, o domain.Object) error {
	if c.fail {
		c.fail = false
		return errors.New("injected collection IO")
	}
	return c.target.Collect(ctx, o)
}
func abandonPurgeUploads(f *fixture) {
	GinkgoHelper()
	db, e := sql.Open("sqlite", f.dbPath)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	rows, e := db.Query(`SELECT upload_id FROM uploads`)
	Expect(e).NotTo(HaveOccurred())
	var ids []string
	for rows.Next() {
		var id string
		Expect(rows.Scan(&id)).To(Succeed())
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		_, e = f.core.Abandon(ctx, f.token, id, key(GinkgoT()))
		Expect(e).NotTo(HaveOccurred())
	}
}

var _ = It("Purge PR1-F1 live IO recovery pin release and terminal immutability X10 X14", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	_, r := purgeAccept(f)
	// A real immutable version manifest becomes collectible after target removal.
	var manifest domain.Object
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		v, e := tx.Version(f.version)
		if e != nil {
			return e
		}
		manifest, e = tx.Object(v.ManifestSHA)
		return e
	})).To(Succeed())
	// A consistent SQLite snapshot and its transitive referenced set remain
	// pinned while a real verified copy is paused across production purge turns.
	snapshot := filepath.Join(GinkgoT().TempDir(), "snapshot.sqlite")
	retained := pinnedPurgeSnapshot(f, snapshot)
	copyRoot := GinkgoT().TempDir()
	copyEntered, copyRelease, copyDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		copyDone <- copyPurgeSnapshot(f, snapshot, filepath.Join(copyRoot, "interrupted"), retained, func() error { close(copyEntered); <-copyRelease; return errors.New("interrupted copy") })
	}()
	<-copyEntered
	for _, kind := range []string{"backup", "read", "active_read", "draft", "job", "staging", "active_source", "evidence_view", "upload_read"} {
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			return tx.AcquirePin(domain.Pin{ID: "held-" + kind, Kind: kind, OwnerID: "independent", Hash: manifest.Hash})
		})).To(Succeed())
	}
	clock := time.Now()
	collector := &flakyCollector{target: f.store, fail: true}
	p := &work.Purger{Store: f.store, Bytes: collector, Now: func() time.Time { return clock }}
	for turn := 0; turn < 100; turn++ {
		clock = clock.Add(61 * time.Second)
		Expect(p.Process(ctx, r.JobID)).To(Succeed())
	}
	j, e := f.store.Job(ctx, r.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(f.store.Objects.VerifiedRead(ctx, contentstore.Object{Hash: manifest.Hash, Size: manifest.Size}, func(io.ReadSeeker) error { return nil })).To(Succeed())
	close(copyRelease)
	Expect(<-copyDone).To(MatchError("interrupted copy"))
	_, e = os.Stat(filepath.Join(copyRoot, "interrupted", "COMPLETE.json"))
	Expect(os.IsNotExist(e)).To(BeTrue())
	Expect(copyPurgeSnapshot(f, snapshot, filepath.Join(copyRoot, "verified"), retained, nil)).To(Succeed())
	copied, e := os.ReadFile(filepath.Join(copyRoot, "verified", manifest.Hash))
	Expect(e).NotTo(HaveOccurred())
	Expect(bytes.Contains(copied, []byte(f.version))).To(BeTrue())
	marker, e := os.ReadFile(filepath.Join(copyRoot, "verified", "COMPLETE.json"))
	Expect(e).NotTo(HaveOccurred())
	var complete struct {
		Objects  []domain.Object `json:"objects"`
		Snapshot string          `json:"snapshot_sha256"`
	}
	Expect(json.Unmarshal(marker, &complete)).To(Succeed())
	Expect(complete.Objects).To(Equal(retained))
	Expect(complete.Snapshot).NotTo(BeEmpty())
	for _, o := range retained {
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.ReleasePin("snapshot-" + o.Hash) })).To(Succeed())
	}
	abandonPurgeUploads(f)
	for _, kind := range []string{"backup", "read", "active_read", "draft", "job", "staging", "active_source", "evidence_view", "upload_read"} {
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.ReleasePin("held-" + kind) })).To(Succeed())
	}
	for turn := 0; turn < 100; turn++ {
		clock = clock.Add(61 * time.Second)
		Expect(p.Process(ctx, r.JobID)).To(Succeed())
		j, e = f.store.Job(ctx, r.JobID)
		Expect(e).NotTo(HaveOccurred())
		if j.State == "completed" {
			break
		}
	}
	Expect(j.State).To(Equal("completed"))
	before := j
	Expect(p.Process(ctx, r.JobID)).To(MatchError(domain.ErrTransition))
	j, e = f.store.Job(ctx, r.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j).To(Equal(before))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { _, e := tx.Object(manifest.Hash); return e })).To(MatchError(domain.ErrNotFound))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { _, e := tx.Object(hash("<html>main</html>")); return e })).To(MatchError(domain.ErrNotFound))
})
var _ = It("Purge history ownership title minimization fresh reader rows and permanent version IDs", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	n, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(func() *string { v := "hello.html"; return &v }(), nil, "SensitiveNoteMarker"))
	Expect(e).NotTo(HaveOccurred())
	_, e = noteMutation(f, key(GinkgoT()), "PATCH", n.AnnotationID, n.ETag, encodedNote(map[string]any{"action": "edit", "body": "new", "body_ref": nil, "context": nil, "context_ref": nil, "selected_text": nil}))
	Expect(e).NotTo(HaveOccurred())
	_, r := purgeAccept(f)
	p := &work.Purger{Store: f.store, Bytes: f.store}
	purgeTurns(f, p, r.JobID, 70)
	db, e := sql.Open("sqlite", f.dbPath)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	var count int
	for _, table := range []string{"annotations", "annotation_history", "document_versions"} {
		Expect(db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(0), table)
	}
	Expect(db.QueryRow(`SELECT count(*) FROM purged_versions WHERE version_id=?`, f.version).Scan(&count)).To(Succeed())
	Expect(count).To(Equal(1))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		gone, e := tx.TombstonedVersion(f.version)
		Expect(gone).To(BeTrue())
		return e
	})).To(Succeed())

	var upload string
	Expect(db.QueryRow(`SELECT upload_id FROM uploads LIMIT 1`).Scan(&upload)).To(Succeed())
	_, e = f.core.Import(ctx, f.token, key(GinkgoT()), importer.ImportRequest{UploadID: upload, DocumentPresent: true, VersionID: f.version})
	Expect(e).To(MatchError(importer.ErrImmutableConflict))

	var title string
	Expect(db.QueryRow(`SELECT title FROM documents WHERE document_id=?`, f.document).Scan(&title)).To(Succeed())
	Expect(title).To(BeEmpty())
	// Every saved note response and raw original request is minimized, while
	// operation identity/fingerprint and uncertainty remain persisted.
	Expect(db.QueryRow(`SELECT count(*) FROM operation_receipts WHERE instr(CAST(original_request AS TEXT),'SensitiveNoteMarker')>0`).Scan(&count)).To(Succeed())
	Expect(count).To(Equal(0))

})

var _ = It("Purge freshness includes late reader rows rather than the preserving lifecycle exclusion", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	var owner string
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { a, e := tx.AuthState(); owner = a.OwnerID; return e })).To(Succeed())
	lifecycleCommit(f, lifecyclePlan(f, "trash"))
	plan := lifecyclePlan(f, "purge")
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.MutateResource(domain.Resource{Kind: "reading_position", ID: owner + "/" + f.version, Value: []byte(`{}`)}, 0, "resource.invalidated")
		return e
	})).To(Succeed())
	_, e := f.queries.LifecycleCommit(ctx, f.token, plan.ID, "", lifecycleBody(plan))
	Expect(e).To(MatchError(query.ErrStaleLifecyclePlan))
	d, e := f.store.Document(ctx, f.document)
	Expect(e).NotTo(HaveOccurred())
	Expect(d.Lifecycle).To(Equal("trash"))
})

var _ = It("Purge all interleaved note history revision pins cross the bounded deletion batch", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	page := "hello.html"
	a, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, nil, "first"))
	Expect(e).NotTo(HaveOccurred())
	b, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, nil, "second"))
	Expect(e).NotTo(HaveOccurred())
	notes := []query.AnnotationView{a, b}
	for round := 0; round < 35; round++ {
		for n := range notes {
			old := notes[n]
			body := encodedNote(map[string]any{"action": "edit", "selected_text": nil, "context": nil, "context_ref": old.ContextRef, "body": "next revision", "body_ref": nil})
			notes[n], e = noteMutation(f, key(GinkgoT()), "PATCH", old.AnnotationID, old.ETag, body)
			Expect(e).NotTo(HaveOccurred())
		}
	}
	_, e = noteMutation(f, key(GinkgoT()), "DELETE", notes[0].AnnotationID, notes[0].ETag, nil)
	Expect(e).NotTo(HaveOccurred())
	_, r := purgeAccept(f)
	p := &work.Purger{Store: f.store, Bytes: f.store}
	purgeTurns(f, p, r.JobID, 200)
	db, e := sql.Open("sqlite", f.dbPath)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	var count int
	for _, q := range []string{`SELECT count(*) FROM annotation_history`, `SELECT count(*) FROM object_pins WHERE owner_kind='annotation_history'`, `SELECT count(*) FROM object_references WHERE owner_kind='annotation_history'`} {
		Expect(db.QueryRow(q).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero(), q)
	}
})

var _ = It("Purge minimizes uncertain note receipts without forgetting fingerprint or dispatching equivalence", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	page := "hello.html"
	saveKey := key(GinkgoT())
	n, e := noteMutation(f, saveKey, "POST", f.version, "", noteBody(&page, nil, "private receipt marker"))
	Expect(e).NotTo(HaveOccurred())
	// An uncertain edit reservation is a labelled operation-ledger fixture. It
	// uses the real note route and binding and is not a provider-execution claim.
	a, e := f.auth.Authenticate(ctx, f.token)
	Expect(e).NotTo(HaveOccurred())
	request := domain.Request{Key: domain.OperationKey{PrincipalID: a.PrincipalID, OperationID: key(GinkgoT())}, Method: "PATCH", Target: "/api/v1/annotations/" + n.AnnotationID, Body: []byte(`{"action":"edit","body":"private receipt marker"}`)}
	var original domain.Receipt
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		var err error
		original, _, err = tx.ReserveOperation(request, nil)
		if err != nil {
			return err
		}
		return tx.ReconcileOperation(request.Key)
	})).To(Succeed())
	_, r := purgeAccept(f)
	purger := &work.Purger{Store: f.store, Bytes: f.store}
	purgeTurns(f, purger, r.JobID, 100)
	preserved, e := f.store.Receipt(ctx, request.Key)
	Expect(e).NotTo(HaveOccurred())
	Expect(preserved.State).To(Equal("reconciling"))
	Expect(preserved.Fingerprint).To(Equal(original.Fingerprint))
	Expect(preserved.Response).To(BeNil())
	Expect(preserved.Original.Body).To(BeEmpty())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { _, _, err := tx.ReserveOperation(request, nil); return err })).To(MatchError(domain.ErrNotFound))
	changed := request
	changed.Body = []byte(`{"action":"edit","body":"different"}`)
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { _, _, err := tx.ReserveOperation(changed, nil); return err })).To(MatchError(domain.ErrConflict))
})

var _ = It("Purge queue selects due waits without starving later paused cleanup", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	_, r := purgeAccept(f)
	worker := &work.Purger{Store: f.store, Bytes: f.store}
	purgeTurns(f, worker, r.JobID, 100)
	j, e := f.store.Job(ctx, r.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		return tx.PausePurge(r.JobID, j.AttemptGeneration, "protected", time.Now().UTC().Add(30*time.Second).Format(time.RFC3339Nano))
	})).To(Succeed())
	selected := func() bool {
		GinkgoHelper()
		found := false
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			jobs, e := tx.WorkJobs(64)
			for _, j := range jobs {
				found = found || j.ID == r.JobID
			}
			return e
		})).To(Succeed())
		return found
	}
	Expect(selected()).To(BeFalse())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		return tx.PausePurge(r.JobID, j.AttemptGeneration, "protected", time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano))
	})).To(Succeed())
	Expect(selected()).To(BeTrue())
	// A retained timestamp far beyond the bounded wait is rechecked after clock
	// rollback; this does not make any pinned object eligible for deletion.
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		return tx.PausePurge(r.JobID, j.AttemptGeneration, "protected", time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano))
	})).To(Succeed())
	Expect(selected()).To(BeTrue())
})

var _ = It("Purge minimizes past keyed preview paths while preserving receipt plan status and fingerprint", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	page := "hello.html"
	note, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, nil, "private preview note"))
	Expect(e).NotTo(HaveOccurred())
	d, e := f.store.Document(ctx, f.document)
	Expect(e).NotTo(HaveOccurred())
	previewKey := key(GinkgoT())
	response, e := f.queries.LifecyclePlanCreate(ctx, f.token, previewKey, f.document, []byte(fmt.Sprintf(`{"operation":"trash","expected_revision":%d}`, d.Revision)))
	Expect(e).NotTo(HaveOccurred())
	Expect(string(response.Body)).To(ContainSubstring(note.AnnotationID))
	Expect(string(response.Body)).To(ContainSubstring(page))
	actor, e := f.auth.Authenticate(ctx, f.token)
	Expect(e).NotTo(HaveOccurred())
	key := domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: previewKey}
	original, e := f.store.Receipt(ctx, key)
	Expect(e).NotTo(HaveOccurred())
	_, r := purgeAccept(f)
	purger := &work.Purger{Store: f.store, Bytes: f.store}
	purgeTurns(f, purger, r.JobID, 100)
	saved, e := f.store.Receipt(ctx, key)
	Expect(e).NotTo(HaveOccurred())
	Expect(saved.Fingerprint).To(Equal(original.Fingerprint))
	Expect(saved.State).To(Equal("completed"))
	Expect(saved.Response.Status).To(Equal(201))
	Expect(string(saved.Response.Body)).NotTo(ContainSubstring(note.AnnotationID))
	Expect(string(saved.Response.Body)).NotTo(ContainSubstring(page))
	var before, after domain.LifecyclePage
	Expect(json.Unmarshal(original.Response.Body, &before)).To(Succeed())
	Expect(json.Unmarshal(saved.Response.Body, &after)).To(Succeed())
	Expect(after.ID).To(Equal(before.ID))
	Expect(after.Items).To(BeEmpty())
	Expect(after.NextCursor).To(BeNil())
})
