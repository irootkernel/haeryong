package sqlite

import (
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
)

var _ = It("Copies revision thirteen migration rollback preserves recovery objects and exact fourteen declarations", Label("persistence"), func() {
	_, w := profile(GinkgoT())
	manifest, hash := referenceSchema8(w)
	db := readDB(GinkgoT(), w.Profile)
	for n := 8; n < 13; n++ {
		_, e := db.Exec(migrations[n])
		Expect(e).NotTo(HaveOccurred())
		_, e = db.Exec(`INSERT INTO schema_migrations VALUES(?,?,?)`, n+1, digest([]byte(migrations[n])), nowStamp())
		Expect(e).NotTo(HaveOccurred())
	}
	_, e := db.Exec(`UPDATE libraries SET schema_revision=13;PRAGMA user_version=13`)
	Expect(e).NotTo(HaveOccurred())
	Expect(db.Close()).To(Succeed())
	injected := errors.New("copy migration fault")
	broken := New(func(b Boundary) error {
		if b == BeforeMigrationCommit {
			return injected
		}
		return nil
	})
	_, _, e = broken.Prepare(ctx, w.Profile)
	Expect(e).To(MatchError(injected))
	db = readDB(GinkgoT(), w.Profile)
	var revision int
	Expect(db.QueryRow(`PRAGMA user_version`).Scan(&revision)).To(Succeed())
	Expect(revision).To(Equal(13))
	Expect(db.Close()).To(Succeed())
	s := openStore(GinkgoT(), w, nil)
	Expect(s.validateTables(ctx, SchemaRevision)).To(Succeed())
	snapshots, e := filepath.Glob(filepath.Join(w.Profile.DataDir, "storage", "pre-migration-*.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	verified := false
	for _, snapshot := range snapshots {
		raw, e := os.ReadFile(filepath.Join(snapshot+".objects", hash))
		if e == nil {
			Expect(raw).To(Equal(manifest))
			verified = true
		}
	}
	Expect(verified).To(BeTrue())
	Expect(s.Close()).To(Succeed())
	s = openStore(GinkgoT(), w, nil)
	_, e = s.db.Exec(`DROP INDEX copy_sets_kind;CREATE INDEX copy_sets_kind ON copy_sets(kind,set_id) WHERE stage='complete'`)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.validateTables(ctx, SchemaRevision)).To(MatchError("changed copy schema declaration"))
	Expect(s.Close()).To(Succeed())
	_, _, e = New(nil).Prepare(ctx, w.Profile)
	Expect(e).To(HaveOccurred())
})
