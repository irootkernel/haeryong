package sqlite

import (
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
)

var _ = It("Annotations revision ten upgrade rollback snapshot and declaration refusal", Label("persistence"), func() {
	_, w := profile(GinkgoT())
	manifest, hash := referenceSchema8(w)
	db := readDB(GinkgoT(), w.Profile)
	_, err := db.Exec(migrations[8] + migrations[9])
	Expect(err).NotTo(HaveOccurred())
	for i := 8; i < 10; i++ {
		_, err = db.Exec(`INSERT INTO schema_migrations VALUES(?,?,?)`, i+1, digest([]byte(migrations[i])), nowStamp())
		Expect(err).NotTo(HaveOccurred())
	}
	_, err = db.Exec(`UPDATE libraries SET schema_revision=10;PRAGMA user_version=10`)
	Expect(err).NotTo(HaveOccurred())

	Expect(db.Close()).To(Succeed())
	fault := errors.New("annotation migration injected")
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
	Expect(revision).To(Equal(10))
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
	_, err = s.db.Exec(`DROP INDEX annotations_owner_version`)
	Expect(err).NotTo(HaveOccurred())
	Expect(s.validateTables(ctx, SchemaRevision)).To(MatchError("missing annotation schema declaration"))
	Expect(s.Close()).To(Succeed())
	refused := New(nil)
	_, _, err = refused.Prepare(ctx, w.Profile)
	Expect(err).To(HaveOccurred())
	Expect(refused.db).To(BeNil())
})
