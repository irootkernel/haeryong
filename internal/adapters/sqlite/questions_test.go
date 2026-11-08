package sqlite

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("T5QSQL01 revision19 Q&A migration rolls back atomically preserves all prior checksums and refuses altered declarations", Label("persistence"), func() {
	_, writer := profile(GinkgoT())
	s := openStore(GinkgoT(), writer, nil)
	old, e := s.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	// A labelled reverse-constructed18 fixture, never an older binary claim.
	_, e = s.db.Exec(dropQuestionSchema + `DELETE FROM schema_migrations WHERE revision>18;UPDATE libraries SET schema_revision=18;PRAGMA user_version=18`)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Close()).To(Succeed())
	fault := errors.New("controlled Q&A migration interruption")
	broken := New(func(boundary Boundary) error {
		if boundary == BeforeMigrationCommit {
			return fault
		}
		return nil
	})
	_, _, e = broken.Prepare(ctx, writer.Profile)
	Expect(e).To(MatchError(fault))
	Expect(broken.Close()).To(Succeed())
	db := readDB(GinkgoT(), writer.Profile)
	var revision, count int
	Expect(db.QueryRow(`PRAGMA user_version`).Scan(&revision)).To(Succeed())
	Expect(revision).To(Equal(18))
	Expect(db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN('questions','answers')`).Scan(&count)).To(Succeed())
	Expect(count).To(BeZero())
	for n := 1; n <= 18; n++ {
		var checksum string
		Expect(db.QueryRow(`SELECT checksum FROM schema_migrations WHERE revision=?`, n).Scan(&checksum)).To(Succeed())
		Expect(checksum).To(Equal(digest([]byte(migrations[n-1]))))
	}
	Expect(db.Close()).To(Succeed())
	upgraded := New(nil)
	state, closeDB, e := upgraded.Prepare(ctx, writer.Profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeDB()
	Expect(state.Ready).To(BeTrue())
	health, e := upgraded.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	Expect(health.Library.ID).To(Equal(old.Library.ID))
	Expect(health.Library.SchemaRevision).To(Equal(SchemaRevision))
	Expect(upgraded.validateTables(ctx, SchemaRevision)).To(Succeed())
	for _, table := range []string{"questions", "answers"} {
		Expect(upgraded.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	}
	_, e = upgraded.db.Exec(`DROP INDEX questions_version;CREATE INDEX questions_version ON questions(version_id,owner_id,created_at,question_id)`)
	Expect(e).NotTo(HaveOccurred())
	Expect(upgraded.validateTables(ctx, SchemaRevision)).To(MatchError("changed question schema declaration"))
})
