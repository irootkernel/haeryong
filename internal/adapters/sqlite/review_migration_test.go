package sqlite

import (
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
)

var _ = It("Review revision nine upgrade rollback snapshot and declaration refusal", Label("persistence"), func() {
	_, w := profile(GinkgoT())
	manifest, hash := referenceSchema8(w)
	db := readDB(GinkgoT(), w.Profile)
	_, err := db.Exec(migrations[8])
	Expect(err).NotTo(HaveOccurred())
	_, err = db.Exec(`INSERT INTO schema_migrations VALUES(9,?,?); UPDATE libraries SET schema_revision=9; PRAGMA user_version=9`, digest([]byte(migrations[8])), nowStamp())
	Expect(err).NotTo(HaveOccurred())
	Expect(db.Close()).To(Succeed())
	fault := errors.New("review migration injected")
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
	Expect(revision).To(Equal(9))
	Expect(db.Close()).To(Succeed())
	s := openStore(GinkgoT(), w, nil)
	Expect(s.validateTables(ctx, 10)).To(Succeed())
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
	Expect(s.validateTables(ctx, 10)).To(Succeed())
	_, err = s.db.Exec(`DROP INDEX review_states_owner_state`)
	Expect(err).NotTo(HaveOccurred())
	Expect(s.validateTables(ctx, 10)).To(MatchError("missing review schema declaration"))
	Expect(s.Close()).To(Succeed())
	refused := New(nil)
	_, _, err = refused.Prepare(ctx, w.Profile)
	Expect(err).To(HaveOccurred())
	Expect(refused.db).To(BeNil())
})
