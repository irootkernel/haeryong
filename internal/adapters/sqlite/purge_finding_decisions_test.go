package sqlite

import (
	"database/sql"
	"encoding/json"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appauth "haeryong.local/haeryong/internal/application/auth"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// These are controlled real SQLite fixtures and normal lifecycle/worker calls,
// not public process E2E or model observations.
func settleDecisionPurgeFixture(f *referenceWorld, jobs ...string) {
	GinkgoHelper()
	rows, e := f.s.db.Query("SELECT version_id FROM document_versions ORDER BY version_id")
	Expect(e).NotTo(HaveOccurred())
	var versions []string
	for rows.Next() {
		var v string
		Expect(rows.Scan(&v)).To(Succeed())
		versions = append(versions, v)
	}
	Expect(rows.Err()).NotTo(HaveOccurred())
	Expect(rows.Close()).To(Succeed())
	for _, v := range versions {
		f.run(v, "search")
		f.run(v, "references")
	}
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		for _, id := range jobs {
			j, e := tx.ClaimJob(id)
			if e != nil {
				return e
			}
			if e = tx.FinishJob(j.ID, j.AttemptGeneration, "", nil); e != nil {
				return e
			}
		}
		return nil
	})).To(Succeed())
}

func normalDecisionPurge(f *referenceWorld, token, document string) string {
	GinkgoHelper()
	auth := appauth.New(f.s)
	applyDocumentLifecycle(f.s, auth, f.bytes, token, document, "trash")
	purge := applyDocumentLifecycle(f.s, auth, f.bytes, token, document, "purge")
	// Surface the underlying error on the first normal guarded worker step.
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
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
	j := runPurgeTurns(f.s, f.bytes, purge.JobID, 128)
	Expect(j.State).To(Or(Equal("completed"), Equal("paused")))
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		i, e := tx.PurgeIntent(j.ID)
		if e != nil {
			return e
		}
		Expect(i.MetadataRemoved).To(BeTrue())
		if j.State == "paused" {
			Expect(i.Reason).To(Equal("protected"))
		}
		return nil
	})).To(Succeed())
	return j.State
}

var _ = It("T00805 C01F finding-bound normal purge removes origin through restart and protected copy", Label("persistence"), func() {
	f, core, token, finding := decisionFixture("topic_assignment")
	req := domain.ScanRequest{Scope: "selected_projects", ProjectIDs: []string{finding.ProjectID}, DocumentID: *finding.DocumentID, VersionID: *finding.VersionID,
		FindingID: finding.ID, ExpectedFindingRevision: &finding.Revision, Depth: "change_focused", SourceMode: "pinned"}
	response, e := core.StartScan(ctx, token, f.id(), jsonBytes(req), req)
	Expect(e).NotTo(HaveOccurred())
	var accepted struct {
		JobID string `json:"job_id"`
	}
	Expect(json.Unmarshal(response.Body, &accepted)).To(Succeed())
	settleDecisionPurgeFixture(f, finding.JobID, accepted.JobID)
	normalDecisionPurge(f, token, *finding.DocumentID)
	assertOriginGone := func(db *sql.DB) {
		var raw []byte
		Expect(db.QueryRow("SELECT requested_scope FROM scan_runs WHERE job_id=?", accepted.JobID).Scan(&raw)).To(Succeed())
		var scope domain.ScanRequestedScope
		Expect(json.Unmarshal(raw, &scope)).To(Succeed())
		Expect(scope.FindingID).To(BeEmpty())
		Expect(scope.ExpectedFindingRevision).To(BeNil())
	}
	assertOriginGone(f.s.db)
	Expect(f.s.Close()).To(Succeed())
	f.s = openStore(GinkgoT(), f.writer, nil)
	assertOriginGone(f.s.db)
	copyPath := filepath.Join(GinkgoT().TempDir(), "purged-origin.sqlite")
	Expect(f.s.SnapshotDatabase(ctx, copyPath)).To(Succeed())
	cfg, e := f.s.Configuration(ctx)
	Expect(e).NotTo(HaveOccurred())
	_, e = ProtectRestoredDatabase(ctx, copyPath, f.writer.Profile, "C01F-origin", cfg.Value, func(map[string]any) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	db, e := openCopy(copyPath)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	assertOriginGone(db)
})

var _ = It("T00805 C01F pending and failed decision receipts bind canonical ownership before purge redaction", Label("persistence"), func() {
	f, _, token, finding := decisionFixture("topic_assignment")
	var owner string
	Expect(f.s.db.QueryRow("SELECT principal_id FROM auth_owner WHERE singleton=1").Scan(&owner)).To(Succeed())
	var requests []domain.Request
	for _, state := range []string{"accepted", "failed"} {
		request := domain.Request{Key: domain.OperationKey{PrincipalID: owner, OperationID: f.id()}, Method: "POST", Target: "/api/v1/findings/" + finding.ID + "/decisions", Body: []byte(`{"decision":"reject"}`), Preconditions: map[string]string{"if-match": domain.FindingETag(finding)}}
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			_, _, e := tx.ReserveOperation(request, nil)
			if e != nil {
				return e
			}
			if state == "failed" {
				return tx.CompleteOperation(request.Key, domain.Response{Status: 409, Body: []byte(`{"error":{"code":"OPERATION_CONFLICT"}}`)})
			}
			return nil
		})).To(Succeed())
		requests = append(requests, request)
	}
	settleDecisionPurgeFixture(f, finding.JobID)
	normalDecisionPurge(f, token, *finding.DocumentID)
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		for n, request := range requests {
			r, e := tx.Receipt(request.Key)
			if e != nil {
				return e
			}
			Expect(r.State).To(Equal([]string{"accepted", "failed"}[n]))
			Expect(r.Original.Body).To(BeEmpty())
			var marker map[string]any
			Expect(json.Unmarshal(r.Normalization, &marker)).To(Succeed())
			Expect(marker["purged_document_id"]).To(Equal(*finding.DocumentID))
			_, exists, e := tx.ReserveOperation(request, func() ([]byte, error) { Fail("purged pending receipt cannot redispatch"); return nil, nil })
			Expect(exists).To(BeTrue())
			Expect(e).To(MatchError(domain.ErrNotFound))
		}
		return nil
	})).To(Succeed())
})

var _ = DescribeTable("T00805 C01F normal purge minimizes real nested decision receipts and preserves replay through protected recovery", Label("persistence"), func(action, endpoint string) {
	kind := "related_explanation"
	if action == "topic" {
		kind = "topic_assignment"
	}
	f, core, token, finding := decisionFixture(kind)
	finding.Rationale = "C01F-private-rationale"
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error { return tx.PutFinding(finding) })).To(Succeed())
	var owner string
	Expect(f.s.db.QueryRow("SELECT principal_id FROM auth_owner WHERE singleton=1").Scan(&owner)).To(Succeed())
	var keys []domain.OperationKey
	if action == "reject" {
		key := f.id()
		_, e := core.FindingAction(ctx, token, key, finding.ID, domain.FindingETag(finding), "reject", []byte(`{"decision":"reject"}`))
		Expect(e).NotTo(HaveOccurred())
		keys = append(keys, domain.OperationKey{PrincipalID: owner, OperationID: key})
	} else {
		key := f.id()
		r, e := core.FindingAction(ctx, token, key, finding.ID, domain.FindingETag(finding), "approve", []byte(`{"decision":"approve"}`))
		Expect(e).NotTo(HaveOccurred())
		keys = append(keys, domain.OperationKey{PrincipalID: owner, OperationID: key})
		applied := actionResult(r)
		if action == "undo" || action == "topic" {
			key = f.id()
			if action == "topic" {
				pin := true
				_, e = core.EffectAction(ctx, token, key, applied.Effect.ID, applied.Effect.ETag, &pin, "accepted", []byte(`{"pinned":true,"decision":"accepted"}`))
			} else {
				_, e = core.EffectAction(ctx, token, key, applied.Effect.ID, applied.Effect.ETag, nil, "undone", []byte(`{}`))
			}
			Expect(e).NotTo(HaveOccurred())
			keys = append(keys, domain.OperationKey{PrincipalID: owner, OperationID: key})
		}
	}
	var originals []domain.Receipt
	for _, key := range keys {
		r, e := f.s.Receipt(ctx, key)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(r.Response.Body)).To(ContainSubstring("C01F-private-rationale"))
		originals = append(originals, r)
	}
	settleDecisionPurgeFixture(f, finding.JobID)
	document := *finding.DocumentID
	if endpoint == "target" {
		var proposal struct {
			Document string `json:"target_document_id"`
		}
		Expect(json.Unmarshal(finding.Proposal, &proposal)).To(Succeed())
		document = proposal.Document
	}
	normalDecisionPurge(f, token, document)
	assertMinimal := func(db *sql.DB) {
		for n, key := range keys {
			var raw, original []byte
			var fingerprint, state string
			Expect(db.QueryRow("SELECT result,original_request,request_fingerprint,state FROM operation_receipts WHERE principal_id=? AND operation_id=?", key.PrincipalID, key.OperationID).Scan(&raw, &original, &fingerprint, &state)).To(Succeed())
			Expect(fingerprint).To(Equal(originals[n].Fingerprint))
			Expect(state).To(Equal(originals[n].State))
			var response domain.Response
			Expect(json.Unmarshal(raw, &response)).To(Succeed())
			var marker map[string]any
			Expect(json.Unmarshal(response.Body, &marker)).To(Succeed())
			Expect(marker).To(Equal(map[string]any{"document_id": document, "purged": true}))
			Expect(string(raw)).NotTo(ContainSubstring("C01F-private-rationale"))
			var request domain.Request
			Expect(json.Unmarshal(original, &request)).To(Succeed())
			Expect(request.Body).To(BeEmpty())
			Expect(request.Target).To(Equal(originals[n].Original.Target))
			Expect(request.Preconditions).To(Equal(originals[n].Original.Preconditions))
		}
	}
	assertMinimal(f.s.db)
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		for _, r := range originals {
			replayed, exists, e := tx.ReserveOperation(r.Original, func() ([]byte, error) {
				Fail("existing purge receipt must not normalize or redispatch")
				return nil, nil
			})
			// Purged equivalence preserves identity but returns nondisclosing
			// NotFound; it never returns the old payload or redispatches.
			Expect(e).To(MatchError(domain.ErrNotFound))
			Expect(exists).To(BeTrue())
			Expect(replayed.Fingerprint).To(Equal(r.Fingerprint))
			changed := r.Original
			changed.Body = []byte(`{"decision":"changed"}`)
			_, _, e = tx.ReserveOperation(changed, nil)
			Expect(e).To(MatchError(domain.ErrConflict))
		}
		return nil
	})).To(Succeed())
	Expect(f.s.Close()).To(Succeed())
	f.s = openStore(GinkgoT(), f.writer, nil)
	assertMinimal(f.s.db)
	copyPath := filepath.Join(GinkgoT().TempDir(), "purged-decision.sqlite")
	Expect(f.s.SnapshotDatabase(ctx, copyPath)).To(Succeed())
	Expect(CheckCopyDatabase(ctx, copyPath)).To(Succeed())
	cfg, e := f.s.Configuration(ctx)
	Expect(e).NotTo(HaveOccurred())
	_, e = ProtectRestoredDatabase(ctx, copyPath, f.writer.Profile, "C01F-receipt", cfg.Value, func(map[string]any) error { return nil })
	Expect(e).NotTo(HaveOccurred())
	db, e := openCopy(copyPath)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	assertMinimal(db)
}, Entry("approve source", "approve", "source"), Entry("approve target", "approve", "target"),
	Entry("reject source", "reject", "source"), Entry("reject target", "reject", "target"),
	Entry("Undo source", "undo", "source"), Entry("Undo target", "undo", "target"), Entry("Topic PUT", "topic", "source"))
