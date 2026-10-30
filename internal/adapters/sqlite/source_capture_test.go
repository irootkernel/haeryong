package sqlite

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("SCAP12 exact revision15 through current migration is atomic and legacy captured hashes gain no invented objects, bindings or observations", Label("persistence"), func() {
	_, w := profile(GinkgoT())
	referenceSchema8(w)
	db := readDB(GinkgoT(), w.Profile)
	for n := 8; n < 15; n++ {
		_, e := db.Exec(migrations[n])
		Expect(e).NotTo(HaveOccurred())
		_, e = db.Exec(`INSERT INTO schema_migrations VALUES(?,?,?)`, n+1, digest([]byte(migrations[n])), nowStamp())
		Expect(e).NotTo(HaveOccurred())
	}
	_, e := db.Exec(`UPDATE libraries SET schema_revision=15;PRAGMA user_version=15`)
	Expect(e).NotTo(HaveOccurred())
	var project string
	Expect(db.QueryRow(`SELECT unfiled_project_id FROM libraries`).Scan(&project)).To(Succeed())
	_, e = db.Exec(`INSERT INTO captured_target_sets VALUES('historical',?,'{}','{}','2026-01-01T00:00:00Z')`, project)
	Expect(e).NotTo(HaveOccurred())
	_, e = db.Exec(`INSERT INTO captured_reads VALUES('historical-read','historical','project_source','{}','{}',?,1,1,'2026-01-01T00:00:00Z')`, strings.Repeat("a", 64))
	Expect(e).NotTo(HaveOccurred())
	Expect(db.Close()).To(Succeed())
	fault := errors.New("capture migration fault")
	_, _, e = New(func(b Boundary) error {
		if b == BeforeMigrationCommit {
			return fault
		}
		return nil
	}).Prepare(ctx, w.Profile)
	Expect(e).To(MatchError(fault))
	db = readDB(GinkgoT(), w.Profile)
	var revision, n int
	Expect(db.QueryRow(`PRAGMA user_version`).Scan(&revision)).To(Succeed())
	Expect(revision).To(Equal(15))
	Expect(db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='source_captures'`).Scan(&n)).To(Succeed())
	Expect(n).To(BeZero())
	Expect(db.Close()).To(Succeed())
	s := openStore(GinkgoT(), w, nil)
	Expect(s.validateTables(ctx, SchemaRevision)).To(Succeed())
	assertEmptyProviderMigrationTables(s.db)
	Expect(s.db.QueryRow(`SELECT count(*) FROM captured_reads WHERE read_id='historical-read' AND bytes_sha256=?`, strings.Repeat("a", 64)).Scan(&n)).To(Succeed())
	Expect(n).To(Equal(1))
	Expect(s.db.QueryRow(`SELECT count(*) FROM content_objects WHERE content_hash=?`, strings.Repeat("a", 64)).Scan(&n)).To(Succeed())
	Expect(n).To(BeZero())
	before := filepath.Join(GinkgoT().TempDir(), "prior15.sqlite")
	Expect(s.SnapshotDatabase(ctx, before)).To(Succeed())
	probe, e := openCopy(before)
	Expect(e).NotTo(HaveOccurred())
	_, e = probe.Exec(`DROP INDEX source_capture_reads_capture`)
	Expect(e).NotTo(HaveOccurred())
	Expect((&Store{db: probe}).validateTables(ctx, SchemaRevision)).To(MatchError("missing source capture schema declaration"))
	Expect(probe.Close()).To(Succeed())
})

var _ = It("SCAP13 source omission follows orphan-only retention provenance through restore/re-export and preserves result/independent/unclassified ownership", Label("persistence"), func() {
	s, a := providerFixture()
	var inputs []domain.ProviderInput
	Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; inputs, e = tx.ProviderInputs(a.ID); return e })).To(Succeed())
	input := inputs[0]
	input.RetentionMode = "reference_only"
	input.SourceVersionID = "d6000000-0000-4000-8000-000000000022"
	fact := map[string]any{"attempt_id": "retention-orphan", "job_id": "absent", "principal_id": "owner", "target": json.RawMessage(`{}`), "job_state": "paused", "request_sha256": strings.Repeat("a", 64), "state": "outcome_unknown", "may_have_dispatched": true, "result_hash": input.Hash, "inputs": []domain.ProviderInput{input}}
	raw, e := json.Marshal(fact)
	Expect(e).NotTo(HaveOccurred())
	path := filepath.Join(GinkgoT().TempDir(), "orphan.sqlite")
	Expect(s.SnapshotDatabase(ctx, path)).To(Succeed())
	Expect(RetainProviderOrphans(ctx, path, bytes.NewReader(raw), providerFixtureCopiedAvailability(path))).To(Succeed())
	db, e := openCopy(path)
	Expect(e).NotTo(HaveOccurred())
	// This copied fixture models an existing source-temporary orphan input owner.
	_, e = db.Exec(`INSERT INTO object_pins VALUES(?,'provider_orphan','retention-orphan',?)`, "provider-orphan:retention-orphan:"+input.ReadID+":"+input.Hash, input.Hash)
	Expect(e).NotTo(HaveOccurred())
	_, e = db.Exec(`INSERT INTO object_pins VALUES('independent-source','backup','independent',?)`, input.Hash)
	Expect(e).NotTo(HaveOccurred())
	Expect(db.Close()).To(Succeed())
	Expect(SanitizeCopyDatabase(ctx, path)).To(Succeed())
	Expect(CheckCopyDatabase(ctx, path)).To(Succeed())
	db, e = openCopy(path)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	var n int
	Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE pin_id=?`, "provider-orphan:retention-orphan:"+input.ReadID+":"+input.Hash).Scan(&n)).To(Succeed())
	Expect(n).To(BeZero())
	var retainedVersion string
	Expect(db.QueryRow(`SELECT json_extract(value,'$.inputs[0].source_version_id') FROM provider_orphans WHERE attempt_id='retention-orphan'`).Scan(&retainedVersion)).To(Succeed())
	Expect(retainedVersion).To(Equal(input.SourceVersionID))
	Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE pin_id IN ('independent-source',?)`, "provider-orphan:retention-orphan:result:"+input.Hash).Scan(&n)).To(Succeed())
	Expect(n).To(Equal(2))
	Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE pin_id=?`, a.ID+":"+input.ReadID).Scan(&n)).To(Succeed())
	Expect(n).To(Equal(1), "unclassified legacy input ownership is not projected away")
	var inventory bytes.Buffer
	Expect(RestoreOutstanding(ctx, path, func(raw json.RawMessage) error { _, e := inventory.Write(append(raw, '\n')); return e })).To(Succeed())
	again := filepath.Join(GinkgoT().TempDir(), "again.sqlite")
	Expect(s.SnapshotDatabase(ctx, again)).To(Succeed())
	Expect(RetainProviderOrphans(ctx, again, bytes.NewReader(inventory.Bytes()), providerFixtureCopiedAvailability(again))).To(Succeed())
	other, e := openCopy(again)
	Expect(e).NotTo(HaveOccurred())
	defer other.Close()
	Expect(other.QueryRow(`SELECT count(*) FROM object_pins WHERE pin_id=?`, "provider-orphan:retention-orphan:"+input.ReadID+":"+input.Hash).Scan(&n)).To(Succeed())
	Expect(n).To(BeZero())
	Expect(other.QueryRow(`SELECT count(*) FROM object_pins WHERE pin_id=?`, "provider-orphan:retention-orphan:result:"+input.Hash).Scan(&n)).To(Succeed())
	Expect(n).To(Equal(1), "same-byte result ownership survives a repeated restore")
	Expect(other.QueryRow(`SELECT json_extract(value,'$.inputs[0].source_version_id') FROM provider_orphans WHERE attempt_id='retention-orphan'`).Scan(&retainedVersion)).To(Succeed())
	Expect(retainedVersion).To(Equal(input.SourceVersionID), "source-version identity survives absent-job re-export/older restore")
})
