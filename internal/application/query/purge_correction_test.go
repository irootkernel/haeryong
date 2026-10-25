package query_test

import (
	"database/sql"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"os"
	"path/filepath"
	"time"
)

var _ = It("Purge C1 F1 exact reader ownership preserves another version path containing the target UUID", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	targetDoc, targetVersion := f.document, f.version
	_, e := f.put(GinkgoT(), query.PagePosition, "hello.html", query.PersonalView{}, true, `{"page_query":"","page_fragment":"","position":{"top":12}}`)
	Expect(e).NotTo(HaveOccurred())
	page := "notes/" + targetVersion + ".html"
	f.publishVersion(GinkgoT(), map[string]string{page: "<p>keep position</p>"}, "alias.html", page)
	saved, e := f.put(GinkgoT(), query.PagePosition, page, query.PersonalView{}, true, `{"page_query":"","page_fragment":"","position":{"top":87}}`)
	Expect(e).NotTo(HaveOccurred())
	survivingVersion := f.version
	f.document, f.version = targetDoc, targetVersion
	lifecycleCommit(f, lifecyclePlan(f, "trash"))
	plan := lifecyclePlan(f, "purge")
	count := 0
	for _, item := range plan.Items {
		if item.Kind == "page_position" {
			count++
			Expect(*item.VersionID).To(Equal(targetVersion))
		}
	}
	Expect(count).To(Equal(1))
	r := lifecycleCommit(f, plan)
	purgeTurns(f, &work.Purger{Store: f.store, Bytes: f.store}, r.JobID, 100)
	actual, e := f.queries.PersonalGet(ctx, f.token, survivingVersion, query.PagePosition, page)
	Expect(e).NotTo(HaveOccurred())
	Expect(actual).To(Equal(saved))
	db, e := sql.Open("sqlite", f.dbPath)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	Expect(db.QueryRow(`SELECT count(*) FROM resource_revisions WHERE resource_kind='page_position'`).Scan(&count)).To(Succeed())
	Expect(count).To(Equal(1))
})

var _ = It("Purge C1 F2 abandon before purge collects unique payload with and without supplied metadata", Label("persistence"), func() {
	for _, metadata := range []string{"", `{"schema_version":"1.0","document":{"title":"Private supplied metadata"}}`} {
		f := newFixture(GinkgoT())
		payload := "<p>unique C1 payload " + key(GinkgoT()) + "</p>"
		extra := []string{}
		if metadata != "" {
			extra = append(extra, metadata)
		}
		f.publishVersion(GinkgoT(), map[string]string{"only.html": payload}, "alias.html", "only.html", extra...)
		abandonPurgeUploads(f)
		_, r := purgeAccept(f)
		clock := time.Now()
		p := &work.Purger{Store: f.store, Bytes: f.store, Now: func() time.Time { return clock }}
		var j domain.Job
		for n := 0; n < 200; n++ {
			clock = clock.Add(61 * time.Second)
			j = purgeTurns(f, p, r.JobID, 1)
			if j.State == "completed" {
				break
			}
		}
		Expect(j.State).To(Equal("completed"))
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { _, e := tx.Object(hash(payload)); return e })).To(MatchError(domain.ErrNotFound))
	}
})

var _ = It("Purge C1 F6 nonterminal checkpoint reopens and fences the stale continuation", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	plan, r := purgeAccept(f)
	clock := time.Now()
	p := &work.Purger{Store: f.store, Bytes: f.store, Now: func() time.Time { return clock }}
	var before domain.PurgeIntent
	j := purgeTurns(f, p, r.JobID, 100)
	Expect(j.State).To(Equal("paused"))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; before, e = tx.PurgeIntent(r.JobID); return e })).To(Succeed())
	Expect(before.MetadataRemoved).To(BeTrue())
	Expect(before.RetainedSets).NotTo(BeEmpty())
	Expect(f.store.Objects.Close()).To(Succeed())
	Expect(f.store.Close()).To(Succeed())
	profile, e := service.Load(service.Selection{Profile: "test", DataDir: f.profileDir})
	Expect(e).NotTo(HaveOccurred())
	reopened := foundation.New(nil)
	_, closeStore, e := reopened.Prepare(ctx, profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeStore()
	f.store = reopened
	p.Store = reopened
	p.Bytes = reopened
	f.auth.Store = reopened
	f.core.Store = reopened
	f.core.Bytes = reopened
	f.queries.Store = reopened
	f.queries.Bytes = reopened
	Expect(reopened.Write(ctx, func(tx app.WriteTx) error {
		after, e := tx.PurgeIntent(r.JobID)
		Expect(after).To(Equal(before))
		return e
	})).To(Succeed())
	current, e := reopened.Job(ctx, r.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(reopened.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.ContinuePurge(r.JobID, current.Revision, current.AttemptGeneration)
		return e
	})).To(Succeed())
	Expect(reopened.Write(ctx, func(tx app.WriteTx) error { _, e := tx.PurgeStep(r.JobID, j.AttemptGeneration, 64); return e })).To(MatchError(domain.ErrStaleWorker))
	abandonPurgeUploads(f)
	for n := 0; n < 200; n++ {
		clock = clock.Add(61 * time.Second)
		j = purgeTurns(f, p, r.JobID, 1)
		if j.State == "completed" {
			break
		}
	}
	Expect(j.State).To(Equal("completed"))
	// Reuse the existing query/auth composers over the reopened durable store.
	f.auth.Store = reopened
	f.queries.Store = reopened
	replay, e := f.queries.LifecycleCommit(ctx, f.token, plan.ID, "", lifecycleBody(plan))
	Expect(e).NotTo(HaveOccurred())
	Expect(replay.Status).To(Equal(202))
	Expect(replay.Body).To(Equal(encodedNote(r)))
	db, e := sql.Open("sqlite", filepath.Join(f.profileDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	var count int
	Expect(db.QueryRow(`SELECT count(*) FROM purge_intents`).Scan(&count)).To(Succeed())
	Expect(count).To(Equal(1))
})

// Test-only bounded coherent-copy fixture: the live writer gate freezes the
// snapshot-to-pin handoff. Membership is read from the snapshot, not live rows.
func pinnedPurgeSnapshot(f *fixture, snapshot string) []domain.Object {
	GinkgoHelper()
	var objects []domain.Object
	db, e := sql.Open("sqlite", f.dbPath)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		if _, e := db.Exec(`VACUUM INTO ?`, snapshot); e != nil {
			return e
		}
		snap, e := sql.Open("sqlite", snapshot)
		if e != nil {
			return e
		}
		defer snap.Close()
		rows, e := snap.Query(`SELECT content_hash,byte_size FROM content_objects o WHERE EXISTS(SELECT 1 FROM object_references r WHERE r.content_hash=o.content_hash) OR EXISTS(SELECT 1 FROM object_pins p WHERE p.content_hash=o.content_hash) OR EXISTS(SELECT 1 FROM version_entries e WHERE e.content_hash=o.content_hash) OR EXISTS(SELECT 1 FROM contexts c WHERE c.content_hash=o.content_hash) OR EXISTS(SELECT 1 FROM metadata_indexes m WHERE m.metadata_hash=o.content_hash) OR EXISTS(SELECT 1 FROM document_versions v WHERE v.manifest_sha256=o.content_hash OR v.supplied_metadata_hash=o.content_hash) ORDER BY content_hash`)
		if e != nil {
			return e
		}
		for rows.Next() {
			var o domain.Object
			if e = rows.Scan(&o.Hash, &o.Size); e != nil {
				rows.Close()
				return e
			}
			objects = append(objects, o)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, o := range objects {
			if e = tx.AcquirePin(domain.Pin{ID: "snapshot-" + o.Hash, Kind: "backup", OwnerID: "snapshot", Hash: o.Hash}); e != nil {
				return e
			}
		}
		return nil
	})).To(Succeed())
	Expect(len(objects)).To(BeNumerically(">", 1))
	return objects
}
func copyPurgeSnapshot(f *fixture, snapshot, destination string, objects []domain.Object, afterFirst func() error) error {
	if e := os.MkdirAll(destination, 0700); e != nil {
		return e
	}
	for n, o := range objects {
		e := f.store.Read(ctx, o, domain.Pin{Kind: "read", OwnerID: "snapshot-copy"}, func(r io.ReadSeeker) error {
			raw, e := io.ReadAll(r)
			if e != nil {
				return e
			}
			if hash(string(raw)) != o.Hash || int64(len(raw)) != o.Size {
				return domain.ErrPrecondition
			}
			return os.WriteFile(filepath.Join(destination, o.Hash), raw, 0600)
		})
		if e != nil {
			return e
		}
		if n == 0 && afterFirst != nil {
			if e = afterFirst(); e != nil {
				return e
			}
		}
	}
	raw, e := os.ReadFile(snapshot)
	if e != nil {
		return e
	}
	marker, e := json.Marshal(map[string]any{"snapshot_sha256": hash(string(raw)), "objects": objects})
	if e != nil {
		return e
	}
	return os.WriteFile(filepath.Join(destination, "COMPLETE.json"), marker, 0600)
}
