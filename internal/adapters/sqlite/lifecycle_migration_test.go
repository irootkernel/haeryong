package sqlite

import (
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
)

var _ = It("Lifecycle revision eleven upgrade rollback snapshot and declaration refusal", Label("persistence"), func() {
	_, w := profile(GinkgoT())
	manifest, hash := referenceSchema8(w)
	db := readDB(GinkgoT(), w.Profile)
	_, err := db.Exec(migrations[8] + migrations[9] + migrations[10])
	Expect(err).NotTo(HaveOccurred())
	for i := 8; i < 11; i++ {
		_, err = db.Exec(`INSERT INTO schema_migrations VALUES(?,?,?)`, i+1, digest([]byte(migrations[i])), nowStamp())
		Expect(err).NotTo(HaveOccurred())
	}
	_, err = db.Exec(`UPDATE libraries SET schema_revision=11;PRAGMA user_version=11`)
	Expect(err).NotTo(HaveOccurred())

	Expect(db.Close()).To(Succeed())
	fault := errors.New("lifecycle migration injected")
	broken := New(func(b Boundary) error {
		if b == BeforeMigrationCommit {
			return fault
		}
		return nil
	})
	_, _, err = broken.Prepare(ctx, w.Profile)
	Expect(err).To(MatchError(fault))
	db = readDB(GinkgoT(), w.Profile)
	var revision int
	Expect(db.QueryRow(`PRAGMA user_version`).Scan(&revision)).To(Succeed())
	Expect(revision).To(Equal(11))
	Expect(db.Close()).To(Succeed())
	s := openStore(GinkgoT(), w, nil)
	Expect(s.validateTables(ctx, SchemaRevision)).To(Succeed())
	var stored string
	Expect(s.db.QueryRow(`SELECT manifest_sha256 FROM document_versions WHERE version_id=?`, refSource).Scan(&stored)).To(Succeed())
	Expect(stored).To(Equal(hash))
	snapshots, err := filepath.Glob(filepath.Join(w.Profile.DataDir, "storage", "pre-migration-*.sqlite"))
	Expect(err).NotTo(HaveOccurred())
	Expect(snapshots).NotTo(BeEmpty())
	for _, snapshot := range snapshots {
		if _, err := os.Stat(snapshot + ".json"); err == nil {
			bytes, err := os.ReadFile(filepath.Join(snapshot+".objects", hash))
			Expect(err).NotTo(HaveOccurred())
			Expect(bytes).To(Equal(manifest))
		}
	}
	Expect(s.Close()).To(Succeed())
	s = openStore(GinkgoT(), w, nil)
	Expect(s.validateTables(ctx, SchemaRevision)).To(Succeed())
	_, err = s.db.Exec(`DROP INDEX lifecycle_trash`)
	Expect(err).NotTo(HaveOccurred())
	Expect(s.validateTables(ctx, SchemaRevision)).To(MatchError("missing lifecycle schema declaration"))
	Expect(s.Close()).To(Succeed())
	refused := New(nil)
	_, _, err = refused.Prepare(ctx, w.Profile)
	Expect(err).To(HaveOccurred())
	Expect(refused.db).To(BeNil())
})

var _ = It("Lifecycle startup refuses added and changed constraints on every revision twelve column", Label("persistence"), func() {
	for _, change := range []struct{ original, altered string }{
		{"previous_lifecycle TEXT CHECK(previous_lifecycle IS NULL OR previous_lifecycle IN ('active','archived'))", "previous_lifecycle TEXT CHECK(previous_lifecycle IS NULL OR previous_lifecycle IN ('active','archived')) CHECK(previous_lifecycle IS NULL)"},
		{"previous_lifecycle TEXT CHECK(previous_lifecycle IS NULL OR previous_lifecycle IN ('active','archived'))", "previous_lifecycle TEXT CHECK(previous_lifecycle IS NULL OR previous_lifecycle IN ('active'))"},
		{"trashed_at TEXT", "trashed_at TEXT CHECK(trashed_at IS NULL)"},
		{"trashed_at TEXT", "trashed_at TEXT NOT NULL"},
		{"trash_generation INTEGER NOT NULL DEFAULT 0 CHECK(trash_generation>=0)", "trash_generation INTEGER NOT NULL DEFAULT 0 CHECK(trash_generation>=0) CHECK(trash_generation=0)"},
		{"trash_generation INTEGER NOT NULL DEFAULT 0 CHECK(trash_generation>=0)", "trash_generation INTEGER NOT NULL DEFAULT 0 CHECK(trash_generation>0)"},
	} {
		By(change.altered)
		_, w := profile(GinkgoT())
		s := openStore(GinkgoT(), w, nil)
		Expect(s.Close()).To(Succeed())
		db := readDB(GinkgoT(), w.Profile)
		_, err := db.Exec(`PRAGMA writable_schema=ON;UPDATE sqlite_master SET sql=replace(sql,?,?) WHERE name='documents';PRAGMA writable_schema=OFF`, change.original, change.altered)
		Expect(err).NotTo(HaveOccurred())
		Expect(db.Close()).To(Succeed())
		refused := New(nil)
		_, _, err = refused.Prepare(ctx, w.Profile)
		Expect(err).To(MatchError(ContainSubstring("changed lifecycle column declaration")))
		Expect(refused.db).To(BeNil())
	}
})
