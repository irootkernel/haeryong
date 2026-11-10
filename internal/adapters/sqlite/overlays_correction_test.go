package sqlite

import (
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/scan"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"path/filepath"
)

var _ = It("T00804 C01 unavailable AI links redact target and evidence across scope Trash purge backup and focal loss", Label("persistence"), func() {
	s, auth, w, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "C01 link privacy")
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
	a, b, c := f.publish(nil, "<p>Visible focal</p>"), f.publish(nil, "<p>Private target</p>"), f.publish(nil, "<p>Private evidence</p>")
	for _, v := range []string{a, b, c} {
		f.run(v, "search")
		f.run(v, "references")
	}
	m := managementFixtureEffect(f, a, b, "related_explanation")
	m.Rationale = "sensitive comparison rationale"
	m.Evidence = jsonBytes([]domain.FindingEvidence{{Kind: "explanation_context", VersionID: c, Path: "sensitive-evidence-path"}})
	core := scan.New(s, auth, f.bytes, nil)
	m.JobID = startScanJob(core, owner.Token, domain.ScanRequest{VersionID: a, Depth: "change_focused"})
	Expect(runScanTurns(s, f.bytes, m.JobID, 64).State).To(Equal("completed"))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		effect, _, e := tx.PutManagementEffect(m)
		if e != nil {
			return e
		}
		v, e := tx.Document(m.From.DocumentID)
		if e != nil {
			return e
		}
		finding := domain.Finding{ID: m.FindingID, JobID: m.JobID, ProjectID: v.ProjectID, Kind: m.Kind, Origin: "ai", DocumentID: &m.From.DocumentID, VersionID: &m.From.VersionID, Evidence: m.Evidence, Proposal: jsonBytes(map[string]any{"target_version_id": b, "target_document_id": m.Target.DocumentID}), EvidenceFingerprint: m.EvidenceFingerprint, Rationale: m.Rationale, CapturedConfigRevision: 1, PermissionsRevision: 1, Status: "applied", Revision: 1, AppliedRef: jsonBytes(map[string]any{"overlay_id": effect.ID, "kind": effect.Kind, "revision": effect.Revision})}
		if e = tx.PutFinding(finding); e != nil {
			return e
		}
		return tx.LinkManagementFinding(finding, effect)
	})).To(Succeed())
	q := query.New(s, auth, f.bytes)
	filter := domain.ManagementFilter{Kind: "related_explanation", VersionID: a}
	read := func(token string) domain.ManagementEffect {
		p, e := q.Management(ctx, token, filter, query.Options{})
		Expect(e).NotTo(HaveOccurred())
		Expect(p.Items).To(HaveLen(1))
		return p.Items[0]
	}
	safe := func(got domain.ManagementEffect) {
		Expect(got.Availability).To(Equal("unavailable"))
		raw, e := json.Marshal(got)
		Expect(e).NotTo(HaveOccurred())
		var fields map[string]json.RawMessage
		Expect(json.Unmarshal(raw, &fields)).To(Succeed())
		Expect(fields).To(HaveLen(8))
		for _, key := range []string{"availability", "overlay_id", "kind", "from", "origin", "decision", "revision", "etag"} {
			Expect(fields).To(HaveKey(key))
		}
		for _, secret := range []string{b, c, m.Target.DocumentID, m.JobID, m.FindingID, "sensitive", "thematic_analogy", "checkpoint-journal"} {
			Expect(string(raw)).NotTo(ContainSubstring(secret))
		}
		Expect(got.From).To(Equal(m.From))
		Expect(got.Origin).To(Equal("ai"))
		Expect(got.Decision).To(Equal("accepted"))
	}
	Expect(read(owner.Token).Availability).To(Equal("available"))
	project := mustUnfiledProject(s)
	targetDenied := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{project}, VersionIDs: []string{a, c}})
	evidenceDenied := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{project}, VersionIDs: []string{a, b}})
	safe(read(targetDenied))
	safe(read(evidenceDenied))
	focalDenied := searchToken(s, domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read"}, ProjectIDs: []string{project}, VersionIDs: []string{b, c}})
	_, e := q.Management(ctx, focalDenied, filter, query.Options{})
	Expect(e).To(HaveOccurred())
	vB, e := s.Version(ctx, b)
	Expect(e).NotTo(HaveOccurred())
	applyDocumentLifecycle(s, auth, f.bytes, owner.Token, vB.DocumentID, "trash")
	safe(read(owner.Token))
	applyDocumentLifecycle(s, auth, f.bytes, owner.Token, vB.DocumentID, "restore")
	Expect(read(owner.Token).Availability).To(Equal("available"))
	vC, e := s.Version(ctx, c)
	Expect(e).NotTo(HaveOccurred())
	applyDocumentLifecycle(s, auth, f.bytes, owner.Token, vC.DocumentID, "trash")
	safe(read(owner.Token))
	purge := applyDocumentLifecycle(s, auth, f.bytes, owner.Token, vC.DocumentID, "purge")
	// Exercise the initial guarded transaction directly so an underlying storage
	// error is reported rather than hidden behind the worker's io_retry summary.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(purge.JobID)
		if e != nil {
			return e
		}
		j, e = tx.ContinuePurge(j.ID, j.Revision, j.AttemptGeneration)
		if e != nil {
			return e
		}
		_, e = tx.PurgeStep(j.ID, j.AttemptGeneration, 64)
		return e
	})).To(Succeed())
	purged := runPurgeTurns(s, f.bytes, purge.JobID, 64)
	Expect(purged.State).To(Or(Equal("completed"), Equal("paused")))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		i, e := tx.PurgeIntent(purge.JobID)
		if e != nil {
			return e
		}
		Expect(i.MetadataRemoved).To(BeTrue())
		if purged.State == "paused" {
			Expect(i.Reason).To(Equal("protected"))
		}
		return nil
	})).To(Succeed())
	safe(read(owner.Token))
	var raw []byte
	var target, key string
	Expect(s.db.QueryRow("SELECT value,target_version_id,semantic_key FROM overlay_relations WHERE effect_id=?", m.ID).Scan(&raw, &target, &key)).To(Succeed())
	Expect(target).To(BeEmpty())
	Expect(key).NotTo(Equal(m.Key()))
	var stored domain.ManagementEffect
	Expect(json.Unmarshal(raw, &stored)).To(Succeed())
	safe(stored)
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		stored.Revision++
		return tx.ReviseManagementEffect(stored, stored.Revision-1)
	})).To(MatchError(domain.ErrConflict))
	for _, table := range []string{"management_effect_history", "finding_effects"} {
		var count int
		Expect(s.db.QueryRow("SELECT count(*) FROM "+table+" WHERE effect_id=?", m.ID).Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
	}
	// Coherent copy/protected restore retains the safe marker, not its old body.
	backup := filepath.Join(GinkgoT().TempDir(), "unavailable.sqlite")
	Expect(s.SnapshotDatabase(ctx, backup)).To(Succeed())
	Expect(CheckCopyDatabase(ctx, backup)).To(Succeed())
	cfg, e := s.Configuration(ctx)
	Expect(e).NotTo(HaveOccurred())
	_, e = ProtectRestoredDatabase(ctx, backup, w.Profile, "C01-marker", cfg.Value, func(map[string]any) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	db, e := openCopy(backup)
	Expect(e).NotTo(HaveOccurred())
	var restored []byte
	Expect(db.QueryRow("SELECT value FROM overlay_relations WHERE effect_id=?", m.ID).Scan(&restored)).To(Succeed())
	Expect(restored).To(Equal(raw))
	Expect(db.Close()).To(Succeed())
	vA, e := s.Version(ctx, a)
	Expect(e).NotTo(HaveOccurred())
	applyDocumentLifecycle(s, auth, f.bytes, owner.Token, vA.DocumentID, "trash")
	_, e = q.Management(ctx, owner.Token, filter, query.Options{})
	Expect(e).To(HaveOccurred())
	all, e := q.Management(ctx, owner.Token, domain.ManagementFilter{Kind: "related_explanation"}, query.Options{})
	Expect(e).NotTo(HaveOccurred())
	Expect(all.Items).To(BeEmpty())
	purge = applyDocumentLifecycle(s, auth, f.bytes, owner.Token, vA.DocumentID, "purge")
	purged = runPurgeTurns(s, f.bytes, purge.JobID, 64)
	Expect(purged.State).To(Or(Equal("completed"), Equal("paused")))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		i, e := tx.PurgeIntent(purge.JobID)
		if e != nil {
			return e
		}
		Expect(i.MetadataRemoved).To(BeTrue())
		if purged.State == "paused" {
			Expect(i.Reason).To(Equal("protected"))
		}
		return nil
	})).To(Succeed())
	var count int
	Expect(s.db.QueryRow("SELECT count(*) FROM overlay_relations").Scan(&count)).To(Succeed())
	Expect(count).To(BeZero())
})

var _ = It("T00804 C01 topic new records require canonical basis and legacy reads never rewrite missing audit history", Label("persistence"), func() {
	s, auth, w, _ := authFixture(GinkgoT())
	_, owner := pairOwner(GinkgoT(), auth, "C01 topic legacy")
	f := &referenceWorld{s: s, writer: w, bytes: &searchBytes{files: map[string]string{}}}
	a := f.publish(nil, "<p>Checkpoint recovery</p>")
	m := managementFixtureEffect(f, a, "", "topic_assignment")
	invalid := m
	invalid.Availability = "unavailable"
	Expect(s.Write(ctx, func(tx app.WriteTx) error { _, _, e := saveManagementFixture(tx, invalid); return e })).To(MatchError(domain.ErrInvalidTarget))
	invalid = m
	invalid.EvidenceBasis = ""
	Expect(s.Write(ctx, func(tx app.WriteTx) error { _, _, e := saveManagementFixture(tx, invalid); return e })).To(MatchError(domain.ErrInvalidTarget))
	Expect(s.Write(ctx, func(tx app.WriteTx) error { _, _, e := saveManagementFixture(tx, m); return e })).To(Succeed())
	q := query.New(s, auth, f.bytes)
	read := func() domain.ManagementEffect {
		p, e := q.Management(ctx, owner.Token, domain.ManagementFilter{Kind: "topic_assignment", VersionID: a}, query.Options{})
		Expect(e).NotTo(HaveOccurred())
		Expect(p.Items).To(HaveLen(1))
		return p.Items[0]
	}
	Expect(read().EvidenceBasis).To(Equal("explanation_text"))
	// Isolated old-record fixture: old Task04 omitted the basis. No retained
	// genuine record is changed and no schema bytes are malformed.
	var legacy map[string]any
	Expect(json.Unmarshal(jsonBytes(m), &legacy)).To(Succeed())
	delete(legacy, "evidence_basis")
	before := jsonBytes(legacy)
	_, e := s.db.Exec("UPDATE topic_assignments SET value=? WHERE effect_id=?", before, m.ID)
	Expect(e).NotTo(HaveOccurred())
	_, e = s.db.Exec("UPDATE management_effect_history SET value=? WHERE effect_id=?", before, m.ID)
	Expect(e).NotTo(HaveOccurred())
	// The original finding proposal also omitted basis. Read its normal closed
	// job consumer without changing that stored proposal or any semantic record.
	scope := jsonBytes(map[string]any{"principal_id": owner.Session.PrincipalID, "project_ids": []string{mustUnfiledProject(s)}, "version_id": a})
	_, e = s.db.Exec("UPDATE jobs SET scope=? WHERE job_id=?", scope, m.JobID)
	Expect(e).NotTo(HaveOccurred())
	core := scan.New(s, auth, f.bytes, nil)
	page, e := core.ListJobFindings(ctx, owner.Token, domain.FindingFilter{JobID: m.JobID})
	Expect(e).NotTo(HaveOccurred())
	Expect(page.Items).To(HaveLen(1))
	var p map[string]any
	Expect(json.Unmarshal(page.Items[0].Proposal, &p)).To(Succeed())
	Expect(p["evidence_basis"]).To(Equal("legacy_unknown"))
	var storedProposal []byte
	Expect(s.db.QueryRow("SELECT proposal_json FROM findings WHERE finding_id=?", m.FindingID).Scan(&storedProposal)).To(Succeed())
	Expect(string(storedProposal)).To(Equal("{}"))
	got := read()
	Expect(got.EvidenceBasis).To(Equal("legacy_unknown"))
	Expect(got.Origin).To(Equal("ai"))
	Expect(got.Revision).To(Equal(int64(1)))
	for _, table := range []string{"topic_assignments", "management_effect_history"} {
		var after []byte
		Expect(s.db.QueryRow("SELECT value FROM "+table+" WHERE effect_id=?", m.ID).Scan(&after)).To(Succeed())
		Expect(after).To(Equal(before))
	}
})
