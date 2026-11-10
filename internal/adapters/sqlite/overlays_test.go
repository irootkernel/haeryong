package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/contentstore"
	v1 "haeryong.local/haeryong/internal/adapters/http/v1"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"net/http/httptest"
	"path/filepath"
	"sync"
)

func managementFixtureEffect(f *referenceWorld, version, target, kind string) domain.ManagementEffect {
	v, e := f.s.Version(ctx, version)
	Expect(e).NotTo(HaveOccurred())
	m := domain.ManagementEffect{ID: f.id(), Kind: kind, FindingID: f.id(), JobID: f.id(), From: domain.ManagementAnchor{DocumentID: v.DocumentID, VersionID: v.ID}, Origin: "ai", EvidenceBasis: "thematic_analogy", Evidence: json.RawMessage("[]"), EvidenceFingerprint: "controlled", Rationale: "Controlled exact-scope comparison", Uncertainty: "source_unverified", CapturedConfigRevision: 1, AppliedConfigRevision: 1, PermissionsRevision: 1, Decision: "accepted", DecisionActor: "auto", Revision: 1, CreatedAt: nowStamp(), UpdatedAt: nowStamp()}
	if kind == "related_explanation" {
		t, e := f.s.Version(ctx, target)
		Expect(e).NotTo(HaveOccurred())
		m.Target = &domain.ManagementAnchor{DocumentID: t.DocumentID, VersionID: t.ID}
		m.RelationType = "thematic_analogy"
		m.MechanismKey = "checkpoint-journal"
	} else {
		m.EvidenceBasis = "explanation_text"
		m.Topic = "checkpoint-recovery"
		m.TopicID = domain.ManagementDigest(m.Topic)
	}
	return m
}
func saveManagementFixture(tx app.WriteTx, m domain.ManagementEffect) (domain.ManagementEffect, bool, error) {
	if _, e := tx.Job(m.JobID); errors.Is(e, domain.ErrNotFound) {
		if e = tx.CreateJob(domain.Job{ID: m.JobID, Kind: "scan"}); e != nil {
			return m, false, e
		}
	} else if e != nil {
		return m, false, e
	}
	f := domain.Finding{ID: m.FindingID, JobID: m.JobID, Kind: m.Kind, Origin: "ai", DocumentID: &m.From.DocumentID, VersionID: &m.From.VersionID, Evidence: m.Evidence, Proposal: json.RawMessage("{}"), EvidenceFingerprint: m.EvidenceFingerprint, Rationale: m.Rationale, CapturedConfigRevision: 1, PermissionsRevision: 1, Status: "applied", Revision: 1}
	if m.Target != nil {
		f.Proposal = jsonBytes(map[string]any{"target_document_id": m.Target.DocumentID, "target_version_id": m.Target.VersionID})
	}
	v, e := tx.Document(m.From.DocumentID)
	if e != nil {
		return m, false, e
	}
	f.ProjectID = v.ProjectID
	applied, created, e := tx.PutManagementEffect(m)
	if e != nil {
		return m, false, e
	}
	f.AppliedRef = jsonBytes(map[string]any{"overlay_id": applied.ID, "kind": m.Kind, "revision": applied.Revision})
	if e = tx.PutFinding(f); e != nil {
		return m, false, e
	}
	if e = tx.LinkManagementFinding(f, applied); e != nil {
		return m, false, e
	}
	return applied, created, nil
}

var _ = It("T00804 global active semantic key reuses two-run and concurrent-child effects without changing original actor/revision/decision", Label("persistence"), func() {
	s, _, w, _ := authFixture(GinkgoT())
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
	a, b := f.publish(nil, "<p>Checkpoint journal recovery.</p>"), f.publish(nil, "<p>Journal checkpoint recovery comparison.</p>")
	for _, kind := range []string{"related_explanation", "topic_assignment"} {
		original := managementFixtureEffect(f, a, b, kind)
		var first domain.ManagementEffect
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			var created bool
			var e error
			first, created, e = saveManagementFixture(tx, original)
			Expect(created).To(BeTrue())
			return e
		})).To(Succeed())
		second := original
		second.ID = f.id()
		second.FindingID = f.id()
		second.JobID = f.id()
		second.DecisionActor = "different-auto-run"
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			got, created, e := saveManagementFixture(tx, second)
			Expect(created).To(BeFalse())
			Expect(got).To(Equal(first))
			return e
		})).To(Succeed())
		// Independently prepared children pass a common barrier before serialized writes.
		contenders := []domain.ManagementEffect{original, original}
		for i := range contenders {
			contenders[i].ID = f.id()
			contenders[i].FindingID = f.id()
			contenders[i].JobID = f.id()
		}
		gate := make(chan struct{})
		results := make(chan error, 2)
		var ready sync.WaitGroup
		ready.Add(2)
		for _, candidate := range contenders {
			go func(m domain.ManagementEffect) {
				ready.Done()
				<-gate
				results <- s.Write(context.Background(), func(tx app.WriteTx) error {
					got, created, e := saveManagementFixture(tx, m)
					if e != nil {
						return e
					}
					if created || domain.ManagementDigest(got) != domain.ManagementDigest(first) {
						return errors.New("concurrent duplicate or altered effect")
					}
					return nil
				})
			}(candidate)
		}
		ready.Wait()
		close(gate)
		Expect(<-results).To(Succeed())
		Expect(<-results).To(Succeed())
		table, _ := managementTable(kind)
		var n, linked int
		Expect(s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n)).To(Succeed())
		Expect(n).To(Equal(1))
		Expect(s.db.QueryRow("SELECT count(*) FROM finding_effects WHERE effect_id=?", first.ID).Scan(&linked)).To(Succeed())
		Expect(linked).To(Equal(4))
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			finding, e := tx.Finding(second.FindingID)
			if e != nil {
				return e
			}
			if e = tx.ValidateManagementFinding(finding); e != nil {
				return e
			}
			finding.AppliedRef = jsonBytes(map[string]any{"overlay_id": first.ID, "kind": kind, "revision": 999})
			Expect(tx.ValidateManagementFinding(finding)).To(MatchError(domain.ErrReconciliation))
			return nil
		})).To(Succeed())
		if kind == "related_explanation" {
			distinct := original
			distinct.ID = f.id()
			distinct.FindingID = f.id()
			distinct.JobID = f.id()
			distinct.MechanismKey = "different-mechanism"
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				_, created, e := saveManagementFixture(tx, distinct)
				Expect(created).To(BeTrue())
				return e
			})).To(Succeed())
		}
	}
	f.restart()
	Expect(f.s.validateTables(ctx, 23)).To(Succeed())
	var n int
	Expect(f.s.db.QueryRow("SELECT count(*) FROM management_effect_history").Scan(&n)).To(Succeed())
	Expect(n).To(Equal(3))
})

var _ = It("T00804 AI origin survives controlled owner approval and public HTTP projection while originals/Review remain immutable and hidden evidence is masked", Label("persistence"), func() {
	s, auth, w, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "management origin")
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
	a, b := f.publish(nil, "<p>Original checkpoint.</p>"), f.publish(nil, "<p>Related original.</p>")
	originalA, e := s.Version(ctx, a)
	Expect(e).NotTo(HaveOccurred())
	originalB, e := s.Version(ctx, b)
	Expect(e).NotTo(HaveOccurred())
	for _, kind := range []string{"related_explanation", "topic_assignment"} {
		m := managementFixtureEffect(f, a, b, kind)
		var effect domain.ManagementEffect
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; effect, _, e = saveManagementFixture(tx, m); return e })).To(Succeed())
		effect.Revision++
		effect.DecisionActor = owner.Session.PrincipalID
		effect.UpdatedAt = nowStamp()
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ReviseManagementEffect(effect, 1) })).To(Succeed())
		effect.Origin = "owner"
		effect.Revision++
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ReviseManagementEffect(effect, 2) })).To(MatchError(domain.ErrConflict))
	}
	core := query.New(s, auth, f.bytes)
	h := v1.New(core, nil, nil)
	for _, path := range []string{"/api/v1/versions/" + a + "/related", "/api/v1/topic-assignments?version_id=" + a} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+owner.Token)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		Expect(out.Code).To(Equal(200), out.Body.String())
		var response struct{ Data domain.ManagementPage }
		Expect(json.Unmarshal(out.Body.Bytes(), &response)).To(Succeed())
		Expect(response.Data.Items).To(HaveLen(1))
		m := response.Data.Items[0]
		Expect(m.Origin).To(Equal("ai"))
		Expect(m.DecisionActor).To(Equal(owner.Session.PrincipalID))
		Expect(m.Revision).To(Equal(int64(2)))
		Expect(m.ETag).To(Equal(m.Validator()))
		head := httptest.NewRequest("HEAD", path, nil)
		head.Header.Set("Authorization", "Bearer "+owner.Token)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, head)
		Expect(res.Code).To(Equal(200))
		Expect(res.Body.Len()).To(BeZero())
		Expect(res.Header().Get("ETag")).NotTo(BeEmpty())
		conditional := httptest.NewRequest("GET", path, nil)
		conditional.Header.Set("Authorization", "Bearer "+owner.Token)
		conditional.Header.Set("If-None-Match", res.Header().Get("ETag"))
		unchanged := httptest.NewRecorder()
		h.ServeHTTP(unchanged, conditional)
		Expect(unchanged.Code).To(Equal(304))
		Expect(unchanged.Body.Len()).To(BeZero())
	}
	additional := managementFixtureEffect(f, a, b, "related_explanation")
	additional.MechanismKey = "different-mechanism"
	Expect(s.Write(ctx, func(tx app.WriteTx) error { _, _, e := saveManagementFixture(tx, additional); return e })).To(Succeed())
	filter := domain.ManagementFilter{Kind: "related_explanation", VersionID: a}
	first, e := core.Management(ctx, owner.Token, filter, query.Options{Limit: 1})
	Expect(e).NotTo(HaveOccurred())
	Expect(first.Items).To(HaveLen(1))
	Expect(first.NextCursor).NotTo(BeNil())
	second, e := core.Management(ctx, owner.Token, filter, query.Options{Limit: 1, Cursor: *first.NextCursor})
	Expect(e).NotTo(HaveOccurred())
	Expect(second.Items).To(HaveLen(1))
	Expect(second.Items[0].ID).NotTo(Equal(first.Items[0].ID))
	Expect(second.NextCursor).To(BeNil())
	_, e = core.Management(ctx, owner.Token, filter, query.Options{Limit: 2, Cursor: *first.NextCursor})
	Expect(e).To(MatchError(domain.ErrInvalidTarget))
	otherFilter := filter
	otherFilter.VersionID = b
	_, e = core.Management(ctx, owner.Token, otherFilter, query.Options{Limit: 1, Cursor: *first.NextCursor})
	Expect(e).To(MatchError(domain.ErrInvalidTarget))
	after, e := s.Version(ctx, a)
	Expect(e).NotTo(HaveOccurred())
	Expect(after).To(Equal(originalA))
	after, e = s.Version(ctx, b)
	Expect(e).NotTo(HaveOccurred())
	Expect(after).To(Equal(originalB))
	var review, authored int
	Expect(s.db.QueryRow("SELECT count(*) FROM review_events").Scan(&review)).To(Succeed())
	Expect(review).To(BeZero())
	Expect(s.db.QueryRow("SELECT count(*) FROM authored_references").Scan(&authored)).To(Succeed())
	Expect(authored).To(BeZero())
	Expect(applyDocumentLifecycle(s, auth, f.bytes, owner.Token, originalB.DocumentID, "trash").Lifecycle).To(Equal("trash"))
	page, e := core.Management(ctx, owner.Token, domain.ManagementFilter{Kind: "related_explanation", VersionID: a}, query.Options{})
	Expect(e).NotTo(HaveOccurred())
	Expect(page.Items).To(HaveLen(2))
	for _, m := range page.Items {
		Expect(m.Availability).To(Equal("unavailable"))
		Expect(m.Target).To(BeNil())
		Expect(m.Rationale).To(BeEmpty())
	}
	// Current event delivery hides even an older notification's IDs after Trash.
	var finding string
	Expect(s.db.QueryRow("SELECT finding_id FROM findings WHERE kind='related_explanation' ORDER BY finding_id LIMIT 1").Scan(&finding)).To(Succeed())
	lib, e := s.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	ev, e := auth.FilterEvent(ctx, owner.Token, domain.Event{StreamEpoch: lib.Library.StreamEpoch, ResourceKind: "finding", ResourceID: finding, Kind: "overlay.changed", Body: []byte("private")})
	Expect(e).NotTo(HaveOccurred())
	Expect(ev.Kind).To(Equal("library.invalidated"))
	Expect(ev.Body).To(BeNil())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.AppendEvent("finding", finding, "overlay.changed", 2, []byte("private"))
	})).To(Succeed())
	events, e := auth.Events(ctx, owner.Token, lib.Library.StreamEpoch, 0)
	Expect(e).NotTo(HaveOccurred())
	for _, event := range events {
		Expect(event.ResourceID).NotTo(Equal(finding))
		Expect(string(event.Body)).NotTo(ContainSubstring("private"))
	}
})

var _ = It("T00804 transaction rollback, coherent backup, protected restore and purge scrub new effect history/linkage without exporting decisions", Label("persistence"), func() {
	s, _, w, _ := authFixture(GinkgoT())
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
	a, b := f.publish(nil, "<p>Original A.</p>"), f.publish(nil, "<p>Original B.</p>")
	m := managementFixtureEffect(f, a, b, "related_explanation")
	failure := errors.New("controlled before commit")
	s.fault = func(boundary Boundary) error {
		if boundary == BeforeCommit {
			return failure
		}
		return nil
	}
	Expect(s.Write(ctx, func(tx app.WriteTx) error { _, _, e := saveManagementFixture(tx, m); return e })).To(MatchError(failure))
	s.fault = nil
	var n int
	Expect(s.db.QueryRow("SELECT count(*) FROM overlay_relations").Scan(&n)).To(Succeed())
	Expect(n).To(BeZero())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { _, _, e := saveManagementFixture(tx, m); return e })).To(Succeed())
	backup := filepath.Join(GinkgoT().TempDir(), "backup.sqlite")
	Expect(s.SnapshotDatabase(ctx, backup)).To(Succeed())
	Expect(CheckCopyDatabase(ctx, backup)).To(Succeed())
	cfg, e := s.Configuration(ctx)
	Expect(e).NotTo(HaveOccurred())
	_, e = ProtectRestoredDatabase(ctx, backup, w.Profile, "controlled-restore", cfg.Value, func(map[string]any) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	db, e := openCopy(backup)
	Expect(e).NotTo(HaveOccurred())
	Expect(db.QueryRow("SELECT count(*) FROM overlay_relations").Scan(&n)).To(Succeed())
	Expect(n).To(Equal(1))
	Expect(db.Close()).To(Succeed())
	// Internal exact tombstone boundary: not an end-to-end public purge claim.
	v, e := s.Version(ctx, b)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		t := tx.(*writeTx)
		if e := t.exec("INSERT INTO purged_versions(version_id,document_id,deleted_at) VALUES(?,?,?)", b, v.DocumentID, nowStamp()); e != nil {
			return e
		}
		return t.purgeManagement(v.DocumentID)
	})).To(Succeed())
	Expect(s.db.QueryRow("SELECT count(*) FROM overlay_relations").Scan(&n)).To(Succeed())
	Expect(n).To(Equal(1))
	for _, table := range []string{"management_effect_history", "finding_effects"} {
		Expect(s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n)).To(Succeed())
		Expect(n).To(BeZero())
	}
	Expect(StreamCopyRecords(ctx, backup, "selection", "owner", domain.CopyVersion{VersionID: a}, []string{"decisions"}, func(string, json.RawMessage) error { return nil })).To(MatchError("decision export producer unavailable"))
})

var _ = It("T00804 populated schema22 upgrades additively and missing active-key DDL refuses startup", Label("persistence"), func() {
	s, _, w, _ := authFixture(GinkgoT())
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
	a := f.publish(nil, "<p>Immutable old version.</p>")
	before, e := s.Version(ctx, a)
	Expect(e).NotTo(HaveOccurred())
	objects, e := contentstore.New(filepath.Join(w.Profile.DataDir, "storage"))
	Expect(e).NotTo(HaveOccurred())
	for _, body := range f.bytes.files {
		staged, e := objects.Stage(ctx, bytes.NewReader([]byte(body)))
		Expect(e).NotTo(HaveOccurred())
		_, e = objects.Finalize(ctx, staged, func(_ contentstore.Object, publish func() error) error { return publish() })
		Expect(e).NotTo(HaveOccurred())
	}
	Expect(objects.Close()).To(Succeed())
	_, e = s.db.Exec("DROP TABLE finding_effects;DROP TABLE management_effect_history;DROP TABLE topic_assignments;DROP TABLE overlay_relations;DROP TABLE topics;DELETE FROM schema_migrations WHERE revision=23;UPDATE libraries SET schema_revision=22;PRAGMA user_version=22")
	Expect(e).NotTo(HaveOccurred())
	f.restart()
	after, e := f.s.Version(ctx, a)
	Expect(e).NotTo(HaveOccurred())
	Expect(after).To(Equal(before))
	paths, e := filepath.Glob(filepath.Join(w.Profile.DataDir, "storage", "pre-migration-*.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	Expect(paths).NotTo(BeEmpty())
	_, e = f.s.db.Exec("DROP INDEX overlay_relations_active_key")
	Expect(e).NotTo(HaveOccurred())
	Expect(f.s.validateTables(ctx, 23)).To(MatchError("missing management overlays schema declaration"))
	// Restore only this test's DDL before its owned cleanup.
	_, e = f.s.db.Exec("CREATE UNIQUE INDEX overlay_relations_active_key ON overlay_relations(semantic_key) WHERE decision='accepted'")
	Expect(e).NotTo(HaveOccurred())
	Expect(f.s.validateTables(ctx, 23)).To(Succeed())
})
