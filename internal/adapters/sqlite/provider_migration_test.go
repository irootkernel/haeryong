package sqlite

import (
	"database/sql"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"path/filepath"
)

const dropProviderSchema = `DROP TABLE IF EXISTS provider_inputs;DROP TABLE IF EXISTS provider_reconcile_intents;DROP TABLE IF EXISTS provider_retry_authorizations;DROP TABLE IF EXISTS provider_linked_retry_authorizations;DROP TABLE IF EXISTS provider_orphans;DROP TABLE IF EXISTS provider_attempts;`

var _ = It("PA12 revision14 migration fails atomically and upgrades to exact provider declarations", Label("persistence"), func() {
	_, w := profile(GinkgoT())
	referenceSchema8(w)
	db := readDB(GinkgoT(), w.Profile)
	for n := 8; n < 14; n++ {
		_, e := db.Exec(migrations[n])
		Expect(e).NotTo(HaveOccurred())
		_, e = db.Exec(`INSERT INTO schema_migrations VALUES(?,?,?)`, n+1, digest([]byte(migrations[n])), nowStamp())
		Expect(e).NotTo(HaveOccurred())
	}
	_, e := db.Exec(`UPDATE libraries SET schema_revision=14;PRAGMA user_version=14`)
	Expect(e).NotTo(HaveOccurred())
	Expect(db.Close()).To(Succeed())
	fault := errors.New("provider migration fault")
	broken := New(func(b Boundary) error {
		if b == BeforeMigrationCommit {
			return fault
		}
		return nil
	})
	_, _, e = broken.Prepare(ctx, w.Profile)
	Expect(e).To(MatchError(fault))
	db = readDB(GinkgoT(), w.Profile)
	var n int
	Expect(db.QueryRow(`PRAGMA user_version`).Scan(&n)).To(Succeed())
	Expect(n).To(Equal(14))
	Expect(db.Close()).To(Succeed())
	s := openStore(GinkgoT(), w, nil)
	Expect(s.validateTables(ctx, SchemaRevision)).To(Succeed())
	priorLayout := filepath.Join(GinkgoT().TempDir(), "missing-dispatch-provenance.sqlite")
	Expect(s.SnapshotDatabase(ctx, priorLayout)).To(Succeed())
	probe, e := openCopy(priorLayout)
	Expect(e).NotTo(HaveOccurred())
	_, e = probe.Exec(`ALTER TABLE provider_attempts DROP COLUMN may_have_dispatched`)
	Expect(e).NotTo(HaveOccurred())
	Expect((&Store{db: probe}).validateTables(ctx, SchemaRevision)).To(MatchError("changed provider schema declaration"), "an earlier incompatible15 layout is refused, never labelled current")
	Expect(probe.Close()).To(Succeed())
	_, e = s.db.Exec(`DROP INDEX provider_attempts_job`)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.validateTables(ctx, SchemaRevision)).NotTo(Succeed())
	_, e = s.db.Exec(`CREATE INDEX provider_attempts_job ON provider_attempts(job_id,attempt_generation)`)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.validateTables(ctx, SchemaRevision)).To(Succeed())
	_, e = s.db.Exec(`DROP TABLE provider_inputs;CREATE TABLE provider_inputs(attempt_id TEXT NOT NULL REFERENCES provider_attempts(attempt_id),read_id TEXT NOT NULL REFERENCES captured_reads(read_id),content_hash TEXT NOT NULL,input_origin TEXT CHECK(input_origin IN ('daemon_supplied','native_observed')),PRIMARY KEY(attempt_id,read_id))`)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.validateTables(ctx, SchemaRevision)).To(MatchError("changed provider schema declaration"), "earlier undelivered schema15 without observation time is incompatible, never silently relabelled")
})

func assertEmptyProviderMigrationTables(db *sql.DB) {
	GinkgoHelper()
	for _, name := range []string{"provider_attempts", "provider_inputs", "provider_retry_authorizations", "provider_linked_retry_authorizations", "provider_orphans", "provider_reconcile_intents"} {
		var exists, count int
		Expect(db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&exists)).To(Succeed())
		if exists == 0 {
			continue
		}
		Expect(db.QueryRow(`SELECT count(*) FROM ` + name).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero(), "schema15-only tables excluded from historical row comparison must contain no historical or backfilled rows")
	}
}
