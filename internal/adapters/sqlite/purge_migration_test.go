package sqlite

import (
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
)

var _ = It("Purge authentic twelve to thirteen rollback recovery and every declaration remains reachable", Label("persistence"), func() {
	_, w := profile(GinkgoT())
	manifest, hash := referenceSchema8(w)
	db := readDB(GinkgoT(), w.Profile)
	for n := 8; n < 12; n++ {
		_, err := db.Exec(migrations[n])
		Expect(err).NotTo(HaveOccurred())
		_, err = db.Exec(`INSERT INTO schema_migrations VALUES(?,?,?)`, n+1, digest([]byte(migrations[n])), nowStamp())
		Expect(err).NotTo(HaveOccurred())
	}
	_, err := db.Exec(`UPDATE libraries SET schema_revision=12;PRAGMA user_version=12`)
	Expect(err).NotTo(HaveOccurred())
	Expect(db.Close()).To(Succeed())
	injected := errors.New("purge migration injected")
	broken := New(func(b Boundary) error {
		if b == BeforeMigrationCommit {
			return injected
		}
		return nil
	})
	_, _, err = broken.Prepare(ctx, w.Profile)
	Expect(err).To(MatchError(injected))
	db = readDB(GinkgoT(), w.Profile)
	var rev int
	Expect(db.QueryRow(`PRAGMA user_version`).Scan(&rev)).To(Succeed())
	Expect(rev).To(Equal(12))
	Expect(db.Close()).To(Succeed())
	s := openStore(GinkgoT(), w, nil)
	Expect(s.validateTables(ctx, SchemaRevision)).To(Succeed())
	snapshots, err := filepath.Glob(filepath.Join(w.Profile.DataDir, "storage", "pre-migration-*.sqlite"))
	Expect(err).NotTo(HaveOccurred())
	Expect(snapshots).NotTo(BeEmpty())
	verified := false
	for _, snapshot := range snapshots {
		raw, err := os.ReadFile(filepath.Join(snapshot+".objects", hash))
		if err == nil {
			Expect(raw).To(Equal(manifest))
			verified = true
		}
	}
	Expect(verified).To(BeTrue())
	Expect(s.Close()).To(Succeed())
	s = openStore(GinkgoT(), w, nil)
	_, err = s.db.Exec(`DROP INDEX purged_versions_document`)
	Expect(err).NotTo(HaveOccurred())
	Expect(s.validateTables(ctx, SchemaRevision)).To(MatchError("missing purge schema declaration"))
	Expect(s.Close()).To(Succeed())
	refused := New(nil)
	_, _, err = refused.Prepare(ctx, w.Profile)
	Expect(err).To(HaveOccurred())
})
