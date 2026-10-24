package sqlite

import (
	"bytes"
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	importer "haeryong.local/haeryong/internal/application/import"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"strings"
	"time"
)

const refSource = "11111111-1111-4111-8111-111111111111"
const refTarget = "22222222-2222-4222-8222-222222222222"
const refDocument = "33333333-3333-4333-8333-333333333333"
const refTargetDocument = "44444444-4444-4444-8444-444444444444"
const refJob = "55555555-5555-4555-8555-555555555555"
const refUnknown = "66666666-6666-4666-8666-666666666666"

const dropReferenceSchema = `DROP TABLE authored_edges;DROP TABLE authored_references;DROP TABLE reference_materializations;`

func refText(s string) *string { return &s }
func referenceSeed(s *Store) domain.ReferenceMaterialization {
	GinkgoHelper()
	h, err := s.Health(ctx)
	Expect(err).NotTo(HaveOccurred())
	manifest := digest([]byte("reference manifest fixture"))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		for i, pair := range [][2]string{{refDocument, refSource}, {refTargetDocument, refTarget}} {
			if err := tx.CreateDocument(domain.Document{ID: pair[0], ProjectID: h.Library.UnfiledProjectID, Title: "Reference fixture"}); err != nil {
				return err
			}
			set := fmt.Sprintf("ref-set-%d", i)
			if err := tx.CreateContentSet(set, digest(nil)); err != nil {
				return err
			}
			if err := tx.SealContentSet(set); err != nil {
				return err
			}
			if err := tx.PutVersion(domain.Version{ID: pair[1], DocumentID: pair[0], ContentSetID: set, ManifestSHA: manifest, ViewerProfile: "html_js_v1", SourceBasis: "unverified"}); err != nil {
				return err
			}
			for _, path := range []string{"source.html", "a.html", "b.html"} {
				if err := tx.PutPage(domain.Page{VersionID: pair[1], Path: path}); err != nil {
					return err
				}
			}
			if err := tx.PublishVersion(pair[1], 1); err != nil {
				return err
			}
		}
		if err := tx.CreateJob(domain.Job{ID: refJob, Kind: "derived_index"}); err != nil {
			return err
		}
		return tx.PutDerivedWork(domain.DerivedWork{JobID: refJob, VersionID: refSource, WorkKind: "references", ExtractorRevision: domain.ReferenceRevision})
	})).To(Succeed())
	return domain.ReferenceMaterialization{FromVersionID: refSource, ManifestSHA: manifest, ReferenceRevision: domain.ReferenceRevision, State: "building", JobID: refJob, Revision: 1}
}
func referenceRecord(id string) domain.AuthoredReference {
	GinkgoHelper()
	raw, err := json.Marshal(map[string]any{"id": id, "document_id": refTargetDocument, "version_id": refTarget, "page_path": "a.html", "region_id": "retry", "from": map[string]any{"page_path": "source.html", "region_id": "retry"}, "relation": "analogy", "page_query": "q=한글 +%2F", "page_fragment": "fragment"})
	Expect(err).NotTo(HaveOccurred())
	r, err := importer.DecodeReference(raw)
	Expect(err).NotTo(HaveOccurred())
	r.FromVersionID = refSource
	return r
}
func prepareReference(r domain.AuthoredReference) app.PreparedReference {
	GinkgoHelper()
	prepared, err := app.PrepareAuthoredReference(r)
	Expect(err).NotTo(HaveOccurred())
	return prepared
}

func referenceEdge(s *Store, r domain.AuthoredReference) domain.AuthoredEdge {
	GinkgoHelper()
	h, err := s.Health(ctx)
	Expect(err).NotTo(HaveOccurred())
	id, err := domain.ReferenceEdgeID(h.Library.ID, r.FromVersionID, r.ID)
	Expect(err).NotTo(HaveOccurred())
	hash := refText(digest([]byte("anchor")))
	return domain.AuthoredEdge{ID: id, FromVersionID: r.FromVersionID, ReferenceID: r.ID, FromPagePath: r.FromPagePath, FromRegionID: r.FromRegionID, TargetProjectID: h.Library.UnfiledProjectID, TargetDocumentID: r.TargetDocumentID, TargetVersionID: r.TargetVersionID, TargetPagePath: r.TargetPagePath, TargetRegionID: r.TargetRegionID, FromPageSHA: hash, FromAnchorSHA: hash, TargetPageSHA: hash, TargetAnchorSHA: hash, AnchorRevision: domain.SearchExtractorRevision}
}

var _ = Describe("RF06 reference persistence", Label("persistence"), func() {
	It("round-trips immutable declarations and stable edges with bounded outgoing/incoming cursors (RV-PIN: immutable conflict guard)", func() {
		_, w := profile(GinkgoT())
		s := openStore(GinkgoT(), w, nil)
		m := referenceSeed(s)
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutReferenceMaterialization(m) })).To(Succeed())
		r := referenceRecord("../참조/%2F")
		edge := referenceEdge(s, r)
		prepared := prepareReference(r)
		batch := make([]app.PreparedReference, 70)
		for i := range batch {
			batch[i] = prepareReference(referenceRecord(fmt.Sprintf("batch-%02d", i)))
		}
		unknown := referenceRecord("unknown")
		unknown.TargetVersionID = refUnknown
		unknown.AuthoredJSON = []byte(strings.ReplaceAll(string(unknown.AuthoredJSON), refTarget, refUnknown))
		preparedUnknown := prepareReference(unknown)
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			if err := tx.PutAuthoredReference(prepared); err != nil {
				return err
			}
			if err := tx.PutAuthoredReference(prepared); err != nil {
				return err
			}
			if err := tx.PutAuthoredEdge(edge); err != nil {
				return err
			}
			if err := tx.PutAuthoredEdge(edge); err != nil {
				return err
			}
			got, err := tx.AuthoredReference(refSource, r.ID)
			if err != nil {
				return err
			}
			Expect(got).To(Equal(r))
			actual, err := tx.AuthoredEdge(refSource, r.ID)
			if err != nil {
				return err
			}
			Expect(actual).To(Equal(edge))
			for _, item := range batch {
				if err := tx.PutAuthoredReference(item); err != nil {
					return err
				}
			}
			rows, err := tx.AuthoredReferences(refSource, "", 500)
			if err != nil {
				return err
			}
			Expect(rows).To(HaveLen(64))
			next, err := tx.AuthoredReferences(refSource, rows[63].ID, 64)
			if err != nil {
				return err
			}
			Expect(next).To(HaveLen(7))
			incoming, err := tx.IncomingReferences(refTarget, "", "", 1)
			if err != nil {
				return err
			}
			Expect(incoming).To(HaveLen(1))
			Expect(incoming[0].ID).To(Equal(r.ID))
			following, err := tx.IncomingReferences(refTarget, incoming[0].FromVersionID, incoming[0].ID, 1)
			if err != nil {
				return err
			}
			Expect(following[0].ID).To(Equal("batch-00"))
			// Unknown targets are immutable declarations, not target FKs.
			return tx.PutAuthoredReference(preparedUnknown)
		})).To(Succeed())
		changed := referenceRecord(r.ID)
		changed.TargetPagePath = refText("b.html")
		changed.AuthoredJSON = []byte(strings.ReplaceAll(string(changed.AuthoredJSON), "a.html", "b.html"))
		preparedChanged := prepareReference(changed)
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutAuthoredReference(preparedChanged) })).To(MatchError(domain.ErrConflict))
		drift := edge
		drift.TargetAnchorSHA = refText(digest([]byte("drift")))
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutAuthoredEdge(drift) })).To(MatchError(domain.ErrConflict))
		m.State = "ready"
		m.Revision++
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutReferenceMaterialization(m) })).To(Succeed())
		Expect(s.Close()).To(Succeed())
		s = openStore(GinkgoT(), w, nil)
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			got, err := tx.AuthoredEdge(refSource, r.ID)
			Expect(got).To(Equal(edge))
			return err
		})).To(Succeed())
		_, err := s.db.Exec(`UPDATE documents SET lifecycle='purged' WHERE document_id=?`, refTargetDocument)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			got, err := tx.AuthoredEdge(refSource, r.ID)
			Expect(got).To(Equal(edge))
			return err
		})).To(Succeed())
	})
	It("rejects typed state/shape/identity corruption while SQL admits future nonempty enums (RV-ADMISSION, RV-ENUM, RV-FK)", func() {
		_, w := profile(GinkgoT())
		s := openStore(GinkgoT(), w, nil)
		m := referenceSeed(s)
		for _, mutate := range []func(*domain.ReferenceMaterialization){func(v *domain.ReferenceMaterialization) { v.State = "future" }, func(v *domain.ReferenceMaterialization) { v.Revision = 0 }, func(v *domain.ReferenceMaterialization) { v.ReferenceRevision = 0 }, func(v *domain.ReferenceMaterialization) { v.ManifestSHA = "bad" }, func(v *domain.ReferenceMaterialization) { v.FromVersionID = "bad" }, func(v *domain.ReferenceMaterialization) { v.JobID = "bad" }} {
			invalid := m
			mutate(&invalid)
			Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutReferenceMaterialization(invalid) })).To(MatchError(domain.ErrUnsupportedDerivedData))
		}
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutReferenceMaterialization(m) })).To(Succeed())
		_, err := s.db.Exec(`INSERT INTO reference_materializations VALUES(?, ?,1,'building',?,1)`, refUnknown, m.ManifestSHA, refJob)
		Expect(err).To(HaveOccurred(), "source FK is real")
		_, err = s.db.Exec(`UPDATE reference_materializations SET state=''`)
		Expect(err).To(HaveOccurred())
		_, err = s.db.Exec(`UPDATE reference_materializations SET state='future'`)
		Expect(err).NotTo(HaveOccurred(), "SQL is extensible, Go admission is strict")
		Expect(s.Write(ctx, func(tx app.WriteTx) error { _, err := tx.ReferenceMaterialization(refSource); return err })).To(MatchError(domain.ErrUnsupportedDerivedData))
		_, err = s.db.Exec(`UPDATE reference_materializations SET state='building';UPDATE derived_work SET work_kind='future' WHERE job_id=?`, refJob)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Write(ctx, func(tx app.WriteTx) error { _, err := tx.DerivedWork(refJob); return err })).To(MatchError(domain.ErrUnsupportedDerivedData))
		_, err = s.db.Exec(`UPDATE derived_work SET work_kind='references' WHERE job_id=?`, refJob)
		Expect(err).NotTo(HaveOccurred())
		r := referenceRecord("r")
		for _, mutate := range []func(*domain.AuthoredReference){func(v *domain.AuthoredReference) { v.FromVersionID = "bad" }, func(v *domain.AuthoredReference) { v.TargetVersionID = "bad" }, func(v *domain.AuthoredReference) { v.TargetPagePath = nil }, func(v *domain.AuthoredReference) { v.FromPagePath = refText("../escape") }, func(v *domain.AuthoredReference) { v.ID = "" }, func(v *domain.AuthoredReference) { v.PageQuery = "different from raw" }, func(v *domain.AuthoredReference) { v.AuthoredJSON = []byte(`null`) }} {
			invalid := r
			mutate(&invalid)
			_, err := app.PrepareAuthoredReference(invalid)
			Expect(err).To(MatchError(domain.ErrUnsupportedDerivedData))
		}
		prepared := prepareReference(r)
		// The constructor owns bytes and pointers, rather than trusting aliases.
		detached := prepared.Columns()
		*detached.Relation = "mutated"
		Expect(*prepared.Columns().Relation).To(Equal("analogy"))
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutAuthoredReference(app.PreparedReference{}) })).To(MatchError(domain.ErrUnsupportedDerivedData))
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutAuthoredReference(prepared) })).To(Succeed())
		e := referenceEdge(s, r)
		for _, mutate := range []func(*domain.AuthoredEdge){func(v *domain.AuthoredEdge) { v.ID = "bad" }, func(v *domain.AuthoredEdge) { v.ID = refUnknown }, func(v *domain.AuthoredEdge) { v.TargetProjectID = "bad" }, func(v *domain.AuthoredEdge) { v.TargetPageSHA = nil }, func(v *domain.AuthoredEdge) { v.TargetAnchorSHA = refText("bad") }, func(v *domain.AuthoredEdge) { v.AnchorRevision = 0 }, func(v *domain.AuthoredEdge) { v.FromRegionID = refText("") }} {
			invalid := e
			mutate(&invalid)
			Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutAuthoredEdge(invalid) })).To(MatchError(domain.ErrUnsupportedDerivedData))
		}
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutAuthoredEdge(e) })).To(Succeed())
		_, err = s.db.Exec(`UPDATE authored_edges SET target_document_id=?`, refUnknown)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Write(ctx, func(tx app.WriteTx) error { _, err := tx.AuthoredEdge(refSource, r.ID); return err })).To(MatchError(domain.ErrUnsupportedDerivedData))
		_, err = s.db.Exec(`UPDATE authored_edges SET target_document_id=?`, refTargetDocument)
		Expect(err).NotTo(HaveOccurred())
		_, err = s.db.Exec(`UPDATE authored_edges SET target_anchor_sha256='bad'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Write(ctx, func(tx app.WriteTx) error { _, err := tx.AuthoredEdge(refSource, r.ID); return err })).To(MatchError(domain.ErrUnsupportedDerivedData))
		_, err = s.db.Exec(`UPDATE authored_references SET target_version_id='corrupt'`)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Write(ctx, func(tx app.WriteTx) error { _, err := tx.AuthoredReferences(refSource, "", 64); return err })).To(MatchError(domain.ErrUnsupportedDerivedData))
	})
	It("prepares large declarations without holding the store lock and reads typed columns without decoding JSON (RV-PREPARE-LOCK)", func() {
		_, w := profile(GinkgoT())
		s := openStore(GinkgoT(), w, nil)
		m := referenceSeed(s)
		r := referenceRecord("large")
		large := strings.Repeat("large relation ", 1<<18)
		r.Relation = &large
		var raw map[string]any
		Expect(json.Unmarshal(r.AuthoredJSON, &raw)).To(Succeed())
		raw["relation"] = large
		r.AuthoredJSON, _ = json.Marshal(raw)
		// A channel marks actual ongoing preparation; the first complete large
		// decode must still finish while a competing short read is admitted.
		started, done := make(chan struct{}), make(chan error, 1)
		go func() {
			close(started)
			for i := 0; i < 4; i++ {
				if _, err := app.PrepareAuthoredReference(r); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
		<-started
		readResult := make(chan error, 1)
		go func() { _, err := s.Health(ctx); readResult <- err }()
		select {
		case err := <-readResult:
			Expect(err).NotTo(HaveOccurred())
		case <-time.After(2 * time.Second):
			Fail("small read waited for declaration preparation")
		}
		select {
		case <-done:
			Fail("small read did not overlap large declaration preparation")
		default:
		}
		Eventually(done, 30*time.Second).Should(Receive(BeNil()))
		prepared := prepareReference(r)
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			if err := tx.PutReferenceMaterialization(m); err != nil {
				return err
			}
			return tx.PutAuthoredReference(prepared)
		})).To(Succeed())
		// Reading raw bytes is permitted, parsing them inside Write is not.
		// Deliberately invalid stored JSON discriminates a hidden read decoder.
		_, err := s.db.Exec(`UPDATE authored_references SET authored_json=?`, []byte(`not JSON`))
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			got, err := tx.AuthoredReference(refSource, r.ID)
			if err != nil {
				return err
			}
			Expect(got.Relation).To(Equal(r.Relation))
			Expect(got.AuthoredJSON).To(Equal([]byte(`not JSON`)))
			rows, err := tx.IncomingReferences(refTarget, "", "", 64)
			Expect(rows).To(HaveLen(1))
			return err
		})).To(Succeed())
		// Invalid JSON/typed divergence is rejected before any transaction.
		r.AuthoredJSON = bytes.Replace(r.AuthoredJSON, []byte(`"large"`), []byte(`"other"`), 1)
		_, err = app.PrepareAuthoredReference(r)
		Expect(err).To(MatchError(domain.ErrUnsupportedDerivedData))
	})

	It("admits exactly the complete reference tables/indexes and uses the incoming target index (RV-TARGETINDEX)", func() {
		_, w := profile(GinkgoT())
		s := openStore(GinkgoT(), w, nil)
		Expect(s.validateTables(ctx, SchemaRevision)).To(Succeed())
		var id, parent, unused int
		var detail string
		Expect(s.db.QueryRow(`EXPLAIN QUERY PLAN SELECT from_version_id,metadata_reference_id FROM authored_references WHERE target_version_id=? AND (from_version_id,metadata_reference_id)>(?,?) ORDER BY from_version_id,metadata_reference_id LIMIT 64`, refTarget, "", "").Scan(&id, &parent, &unused, &detail)).To(Succeed())
		Expect(detail).To(ContainSubstring("authored_references_target"))
		for _, tamper := range []string{`DROP TABLE authored_edges`, `DROP TABLE authored_edges;CREATE TABLE authored_edges(edge_id TEXT)`, `DROP INDEX authored_references_target`, `DROP INDEX authored_edges_target`, `DROP INDEX authored_references_target;CREATE INDEX authored_references_target ON authored_references(from_version_id,target_version_id,metadata_reference_id)`} {
			_, err := s.db.Exec(tamper)
			Expect(err).NotTo(HaveOccurred())
			Expect(s.validateTables(ctx, SchemaRevision)).NotTo(Succeed(), tamper)
			_, err = s.db.Exec(`DROP TABLE IF EXISTS authored_edges;DROP TABLE authored_references;DROP TABLE reference_materializations;` + migrations[8])
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := s.db.Exec(`CREATE TABLE unknown_reference_table(x)`)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.validateTables(ctx, SchemaRevision)).NotTo(Succeed())
		Expect(s.Close()).To(Succeed())
		failed := New(nil)
		_, _, err = failed.Prepare(ctx, w.Profile)
		Expect(err).To(HaveOccurred())
		Expect(failed.db).To(BeNil())
	})
})
