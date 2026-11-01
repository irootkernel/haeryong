package sqlite

import (
	"errors"
	"strings"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func generationDraftFixture(s *Store) domain.Draft {
	d := domain.Draft{ID: id(GinkgoT()), JobID: id(GinkgoT()), ProposedVersionID: id(GinkgoT()), AllocatedDocumentID: id(GinkgoT()), State: "preparing", Intent: "draft", CreatedAt: "2026-10-30T12:00:00Z", UpdatedAt: "2026-10-30T12:00:00Z"}
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		library, e := tx.Library()
		if e != nil {
			return e
		}
		d.ProjectID = library.UnfiledProjectID
		if e := tx.CreateJob(domain.Job{ID: d.JobID, Kind: "generation", Scope: []byte(`{}`)}); e != nil {
			return e
		}
		return tx.CreateDraft(d)
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; d, e = tx.Draft(d.ID); return e })).To(Succeed())
	return d
}

// This storage-layer fixture creates typed immutable object rows. End-to-end
// generated-byte verification belongs to the actual importer/coordinator tests.
func generationCandidate(s *Store, d domain.Draft, marker string) domain.Draft {
	d.ContentSetID = id(GinkgoT())
	d.PayloadSHA = strings.Repeat(marker, 64)
	d.ManifestSHA = strings.Repeat("e", 64)
	d.State = "ready"
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.PutDurableObject(domain.Object{Hash: d.ManifestSHA, Size: 0, State: "ready"}); e != nil {
			return e
		}
		if e := tx.PutDurableObject(domain.Object{Hash: d.PayloadSHA, Size: 1, State: "ready"}); e != nil {
			return e
		}
		if e := tx.CreateContentSet(d.ContentSetID, d.PayloadSHA); e != nil {
			return e
		}
		if e := tx.PutEntry(domain.Entry{ContentSetID: d.ContentSetID, Path: "page.html", Kind: "file", Hash: d.PayloadSHA, Size: 1}); e != nil {
			return e
		}
		return tx.SealContentSet(d.ContentSetID)
	})).To(Succeed())
	return d
}

var _ = It("GGEN01 revision18 migration rolls back atomically and enforces its exact declarations", Label("persistence"), func() {
	_, writer := profile(GinkgoT())
	s := openStore(GinkgoT(), writer, nil)
	before, e := s.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	// Reverse-constructed prior17 fixture; this never changes a real library or
	// claims that the older binary executed the new migration.
	_, e = s.db.Exec(`DROP TABLE workflow_selections; DROP TABLE draft_history; DROP TABLE drafts; DROP TABLE generation_intents; DELETE FROM schema_migrations WHERE revision=18; UPDATE libraries SET schema_revision=17; PRAGMA user_version=17`)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Close()).To(Succeed())
	fault := errors.New("controlled generation migration failure")
	probe := New(func(b Boundary) error {
		if b == BeforeMigrationCommit {
			return fault
		}
		return nil
	})
	_, _, e = probe.Prepare(ctx, writer.Profile)
	Expect(e).To(MatchError(fault))
	Expect(probe.Close()).To(Succeed())
	db := readDB(GinkgoT(), writer.Profile)
	var revision, count int
	Expect(db.QueryRow(`PRAGMA user_version`).Scan(&revision)).To(Succeed())
	Expect(revision).To(Equal(17))
	Expect(db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='drafts'`).Scan(&count)).To(Succeed())
	Expect(count).To(BeZero())
	Expect(db.Close()).To(Succeed())
	s = openStore(GinkgoT(), writer, nil)
	after, e := s.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.Library.ID).To(Equal(before.Library.ID))
	Expect(after.Library.RuntimeGeneration).To(Equal(before.Library.RuntimeGeneration))
	Expect(after.Library.AuthEpoch).To(Equal(before.Library.AuthEpoch))
	Expect(after.Library.SchemaRevision).To(Equal(18))
	Expect(s.validateTables(ctx, SchemaRevision)).To(Succeed())
	_, e = s.db.Exec(`DROP INDEX drafts_project`)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.validateTables(ctx, SchemaRevision)).To(MatchError("missing generation schema declaration"))
})

var _ = It("GGEN02 replacement preserves both candidates and rejection prevents late ready mutation", Label("persistence"), func() {
	_, writer := profile(GinkgoT())
	s := openStore(GinkgoT(), writer, nil)
	d := generationDraftFixture(s)
	first := generationCandidate(s, d, "a")
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.UpdateDraft(first, d.Revision) })).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; d, e = tx.Draft(d.ID); return e })).To(Succeed())
	second := generationCandidate(s, d, "b")
	second.ProposedVersionID = id(GinkgoT())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.UpdateDraft(second, d.Revision) })).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; d, e = tx.Draft(d.ID); return e })).To(Succeed())
	rejected := d
	rejected.State = "rejected"
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.UpdateDraft(rejected, d.Revision) })).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.UpdateDraft(second, d.Revision) })).To(MatchError(domain.ErrPrecondition))
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.UpdateDraft(second, d.Revision+1) })).To(MatchError(domain.ErrTransition))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		history, e := tx.DraftHistory(d.ID, 0, 64)
		if e != nil {
			return e
		}
		Expect(history).To(HaveLen(4))
		Expect(history[1].ContentSetID).To(Equal(first.ContentSetID))
		Expect(history[2].ContentSetID).To(Equal(second.ContentSetID))
		Expect(history[3].State).To(Equal("rejected"))
		Expect(history[3].ContentSetID).To(Equal(second.ContentSetID))
		for _, candidate := range []domain.Draft{first, second} {
			_, e := tx.ReserveGC(candidate.PayloadSHA, id(GinkgoT()))
			Expect(e).To(MatchError(domain.ErrPinned))
		}
		return nil
	})).To(Succeed())
	Expect(s.Close()).To(Succeed())
	s = openStore(GinkgoT(), writer, nil)
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		actual, e := tx.Draft(d.ID)
		if e != nil {
			return e
		}
		Expect(actual.State).To(Equal("rejected"))
		Expect(actual.ContentSetID).To(Equal(second.ContentSetID))
		return nil
	})).To(Succeed())
})

var _ = It("GGEN03 failed candidate commit preserves its prior revision history and base", Label("persistence"), func() {
	_, writer := profile(GinkgoT())
	var fail atomic.Bool
	fault := errors.New("controlled draft commit failure")
	s := openStore(GinkgoT(), writer, func(b Boundary) error {
		if b == BeforeCommit && fail.Load() {
			return fault
		}
		return nil
	})
	d := generationDraftFixture(s)
	ready := generationCandidate(s, d, "a")
	fail.Store(true)
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.UpdateDraft(ready, d.Revision) })).To(MatchError(fault))
	fail.Store(false)
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		actual, e := tx.Draft(d.ID)
		if e != nil {
			return e
		}
		Expect(actual).To(Equal(d))
		history, e := tx.DraftHistory(d.ID, 0, 64)
		if e != nil {
			return e
		}
		Expect(history).To(HaveLen(1))
		return nil
	})).To(Succeed())
	changed := ready
	base := id(GinkgoT())
	changed.BaseVersionID = &base
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.UpdateDraft(changed, d.Revision) })).To(MatchError(domain.ErrInvalidTarget))
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.UpdateDraft(ready, d.Revision) })).To(Succeed())
})
