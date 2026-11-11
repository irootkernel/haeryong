package sqlite

import (
	"bytes"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/contentstore"
	"haeryong.local/haeryong/internal/application/scan"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"os"
	"path/filepath"
)

// The ordinary case proves two independently approved current links. Export is
// opt-in, into a NEW private directory, and contains controlled proposals only.
var _ = It("T00805 two owner-approved links retain exact controlled consumer fixture", Label("persistence"), func() {
	f, core, token, first := decisionFixture("topic_assignment")
	approve := func(record domain.Finding) domain.FindingActionResult {
		response, e := core.FindingAction(ctx, token, f.id(), record.ID, domain.FindingETag(record), "approve", []byte(`{"decision":"approve"}`))
		Expect(e).NotTo(HaveOccurred())
		Expect(response.Status).To(Equal(200))
		return actionResult(response)
	}
	seed := func(topic string) domain.Finding {
		record := first
		record.ID = f.id()
		record.Proposal = jsonBytes(map[string]string{"topic": topic, "evidence_basis": "explanation_text"})
		Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.PutFinding(record); e != nil {
				return e
			}
			key, material, e := scan.DecisionIdentity(tx, record)
			if e != nil {
				return e
			}
			d, _, e := tx.FindingDecision(key)
			if e != nil {
				return e
			}
			rev, e := tx.TargetOwnerRevision(*record.VersionID)
			if e != nil {
				return e
			}
			return tx.SaveFindingDecisionBasis(domain.FindingDecisionBasis{FindingID: record.ID, Key: key, Material: material, OwnerRevision: d.Revision, TargetRevision: rev})
		})).To(Succeed())
		return record
	}
	// All selected independent siblings pre-exist every owner approval.
	pendingApprove := seed("approval-control")
	pendingReject := seed("rejection-control")
	pendingRejectExtra := seed("extra-rejection-control")
	firstResult := approve(first)
	var topic struct {
		Topic string `json:"topic"`
	}
	Expect(json.Unmarshal(first.Proposal, &topic)).To(Succeed())
	second := seed(topic.Topic)
	secondResult := approve(second)
	Expect(secondResult.Effect.ID).To(Equal(firstResult.Effect.ID))
	// A new ordinary capture after the initial two links owns L's exact basis.
	// L is controlled saved input, not model output; no after-capture rebasing.
	linkRequest := domain.ScanRequest{Scope: "selected_projects", ProjectIDs: []string{first.ProjectID}, DocumentID: *first.DocumentID, VersionID: *first.VersionID, Depth: "full_scope", SourceMode: "pinned"}
	linkStarted, e := core.StartScan(ctx, token, f.id(), jsonBytes(linkRequest), linkRequest)
	Expect(e).NotTo(HaveOccurred())
	var linkAccepted struct {
		JobID string `json:"job_id"`
	}
	Expect(json.Unmarshal(linkStarted.Body, &linkAccepted)).To(Succeed())
	pendingLink := first
	pendingLink.ID = f.id()
	pendingLink.JobID = linkAccepted.JobID
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		run, e := tx.ScanRun(linkAccepted.JobID)
		if e != nil {
			return e
		}
		key, material, e := scan.DecisionIdentity(tx, pendingLink)
		if e != nil {
			return e
		}
		captured, ok := run.RequestedScope.OwnerRevisions[key]
		Expect(ok).To(BeTrue())
		if e = tx.PutFinding(pendingLink); e != nil {
			return e
		}
		if e = tx.SaveFindingDecisionBasis(domain.FindingDecisionBasis{FindingID: pendingLink.ID, Key: key, Material: material, OwnerRevision: captured}); e != nil {
			return e
		}
		j, e := tx.ClaimJob(linkAccepted.JobID)
		if e != nil {
			return e
		}
		return tx.FinishJob(j.ID, j.AttemptGeneration, "", nil)
	})).To(Succeed())

	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		links, e := tx.ManagementLinkedFindings(firstResult.Effect.ID)
		if e != nil {
			return e
		}
		Expect(links).To(HaveLen(2))
		for _, link := range links {
			Expect(link.Active).To(BeTrue())
			Expect(link.ValidHistory).To(BeTrue())
		}
		return nil
	})).To(Succeed())
	destination := os.Getenv("HAERYONG_TASK05_FIXTURE_DIR")
	if destination == "" {
		return
	}
	Expect(filepath.IsAbs(destination)).To(BeTrue())
	_, statErr := os.Lstat(destination)
	Expect(os.IsNotExist(statErr)).To(BeTrue())
	undoneRecord := seed("owner-undo-control")
	undoneResult := approve(undoneRecord)
	response, e := core.EffectAction(ctx, token, f.id(), undoneResult.Effect.ID, undoneResult.Effect.ETag, nil, "undone", []byte("{}"))
	Expect(e).NotTo(HaveOccurred())
	undone := actionResult(response)
	var origin domain.Finding
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error { var e error; origin, e = tx.Finding(undoneRecord.ID); return e })).To(Succeed())
	request := domain.ScanRequest{Scope: "selected_projects", ProjectIDs: []string{first.ProjectID}, DocumentID: *first.DocumentID, VersionID: *first.VersionID, FindingID: origin.ID, ExpectedFindingRevision: &origin.Revision, Depth: "full_scope", SourceMode: "pinned"}
	admitted, e := core.StartScan(ctx, token, f.id(), jsonBytes(request), request)
	Expect(e).NotTo(HaveOccurred())
	var accepted struct {
		JobID string `json:"job_id"`
	}
	Expect(json.Unmarshal(admitted.Body, &accepted)).To(Succeed())
	newFinding := undoneRecord
	newFinding.ID = f.id()
	newFinding.JobID = accepted.JobID
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.PutFinding(newFinding); e != nil {
			return e
		}
		key, material, e := scan.DecisionIdentity(tx, newFinding)
		if e != nil {
			return e
		}
		decision, _, e := tx.FindingDecision(key)
		if e != nil {
			return e
		}
		revision, e := tx.TargetOwnerRevision(*newFinding.VersionID)
		if e != nil {
			return e
		}
		if e = tx.SaveFindingDecisionBasis(domain.FindingDecisionBasis{FindingID: newFinding.ID, Key: key, Material: material, OwnerRevision: decision.Revision, TargetRevision: revision, Reassessment: true}); e != nil {
			return e
		}
		job, e := tx.ClaimJob(accepted.JobID)
		if e != nil {
			return e
		}
		return tx.FinishJob(job.ID, job.AttemptGeneration, "", nil)
	})).To(Succeed())
	// Finish queued work using existing local workers, never a provider dispatcher.
	versions := []string{}
	rows, e := f.s.db.Query("SELECT version_id FROM document_versions ORDER BY version_id")
	Expect(e).NotTo(HaveOccurred())
	for rows.Next() {
		var v string
		Expect(rows.Scan(&v)).To(Succeed())
		versions = append(versions, v)
	}
	Expect(rows.Close()).To(Succeed())
	for _, v := range versions {
		f.run(v, "search")
		f.run(v, "references")
	}
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.ClaimJob(first.JobID)
		if e != nil {
			return e
		}
		return tx.FinishJob(j.ID, j.AttemptGeneration, "", nil)
	})).To(Succeed())
	objects, e := contentstore.New(filepath.Join(f.writer.Profile.DataDir, "storage"))
	Expect(e).NotTo(HaveOccurred())
	for _, body := range f.bytes.files {
		staged, e := objects.Stage(ctx, bytes.NewReader([]byte(body)))
		Expect(e).NotTo(HaveOccurred())
		_, e = objects.Finalize(ctx, staged, func(_ contentstore.Object, publish func() error) error { return publish() })
		Expect(e).NotTo(HaveOccurred())
	}
	Expect(objects.Close()).To(Succeed())
	var authored, review, attempts int
	Expect(f.s.db.QueryRow("SELECT count(*) FROM authored_references").Scan(&authored)).To(Succeed())
	Expect(f.s.db.QueryRow("SELECT count(*) FROM review_events").Scan(&review)).To(Succeed())
	Expect(f.s.db.QueryRow("SELECT count(*) FROM provider_attempts").Scan(&attempts)).To(Succeed())
	Expect(attempts).To(BeZero())
	Expect(f.s.Close()).To(Succeed())
	Expect(f.writer.Close()).To(Succeed())
	Expect(os.Mkdir(destination, 0700)).To(Succeed())
	Expect(os.CopyFS(filepath.Join(destination, "profile"), os.DirFS(f.writer.Profile.DataDir))).To(Succeed())
	Expect(filepath.WalkDir(f.writer.Profile.DataDir, func(source string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		relative, e := filepath.Rel(f.writer.Profile.DataDir, source)
		if e != nil {
			return e
		}
		return os.Chmod(filepath.Join(destination, "profile", relative), info.Mode().Perm())
	})).To(Succeed())
	Expect(os.WriteFile(filepath.Join(destination, "owner-token"), []byte(token), 0600)).To(Succeed())
	manifest := map[string]any{"class": "Controlled retained proposal fixtures with actual owner application service and physical result objects; no provider/source acquisition or semantic quality claim", "profile_dir": filepath.Join(destination, "profile"), "owner_token_file": filepath.Join(destination, "owner-token"), "job_id": first.JobID, "document_id": *first.DocumentID, "version_id": *first.VersionID, "page_path": "page.html", "two_link_effect": secondResult.Effect, "linked_finding_ids": []string{first.ID, second.ID}, "pending_approve": pendingApprove, "pending_reject": pendingReject, "pending_reject_extra": pendingRejectExtra, "pending_link": pendingLink, "pending_link_job_id": linkAccepted.JobID, "independent_pending_ids": []string{pendingApprove.ID, pendingReject.ID}, "independent_capture_job_id": first.JobID, "pending_link_basis": "exact key revision from its new ordinary admitted capture; never rebased after capture", "controlled_class": "Saved fixture proposals only; no provider call, no claim of actual semantic production", "undone_effect": undone.Effect, "undone_finding_id": undoneRecord.ID, "new_reassessment_finding_id": newFinding.ID, "new_reassessment_job_id": accepted.JobID, "versions": versions, "invariants": map[string]any{"authored_reference_count": authored, "review_event_count": review, "provider_attempts": attempts}, "provider_configured": false, "native_provider_calls": 0, "source_acquisition": 0, "quality_claim": false, "runtime_release": false}
	Expect(os.WriteFile(filepath.Join(destination, "manifest.json"), jsonBytes(manifest), 0600)).To(Succeed())
})
