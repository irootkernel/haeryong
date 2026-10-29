package query_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/configpath"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/adapters/gitreader"
	sourcefiles "haeryong.local/haeryong/internal/adapters/source"
	"haeryong.local/haeryong/internal/application/auth"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/recovery"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type interruptedSourceFiles struct{}

func (interruptedSourceFiles) Open(context.Context, string, string) (io.ReadCloser, error) {
	return nil, context.DeadlineExceeded
}

var _ = It("SCAP15 production coherent backup/protected restore and opt-in export preserve selected source bytes while reference-only stays uncopied", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	root := GinkgoT().TempDir()
	snapshotBytes := []byte("additional exact selected source\x00\xff")
	referenceBytes := []byte("uncopied reference source")
	Expect(os.WriteFile(filepath.Join(root, "snapshot"), snapshotBytes, 0600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(root, "reference"), referenceBytes, 0600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(root, "payload-copy"), []byte("<html>main</html>"), 0600)).To(Succeed())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var value map[string]any
		if e = json.Unmarshal(cfg.Value, &value); e != nil {
			return e
		}
		value["projects"] = map[string]any{f.project: map[string]any{"sources": []map[string]any{{"source_id": "d6000000-0000-4000-8000-000000000001", "kind": "files", "root_path": root, "repo_id": nil, "retention_mode": "selected_snapshot", "provider_ids": []string{}}, {"source_id": "d6000000-0000-4000-8000-000000000002", "kind": "files", "root_path": root, "repo_id": nil, "retention_mode": "reference_only", "provider_ids": []string{}}}}}
		cfg.Value, _ = json.Marshal(value)
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
	core := source.New(f.store, f.auth, f.store, gitreader.New())
	core.Files = sourcefiles.ManagedFiles(f.profileDir)
	reads := map[string]domain.SourceCaptureRead{}
	for _, selection := range []struct{ id, path string }{{"d6000000-0000-4000-8000-000000000001", "snapshot"}, {"d6000000-0000-4000-8000-000000000002", "reference"}, {"d6000000-0000-4000-8000-000000000002", "payload-copy"}} {
		r, e := core.Capture(ctx, f.token, key(GinkgoT()), f.project, source.CaptureRequest{VersionID: f.version, SourceMode: "current", Selections: []source.CaptureSelection{{SourceID: selection.id, Paths: []source.CapturePath{{Path: selection.path}}}}})
		Expect(e).NotTo(HaveOccurred())
		var result struct {
			ID string `json:"capture_id"`
		}
		Expect(json.Unmarshal(r.Body, &result)).To(Succeed())
		view, e := core.CaptureView(ctx, f.token, result.ID, "")
		Expect(e).NotTo(HaveOccurred())
		reads[selection.path] = view["items"].([]domain.SourceCaptureRead)[0]
	}
	// Failed selections carry actual distinct reasons into the coherent copy.
	Expect(os.WriteFile(filepath.Join(root, "partial-present"), []byte("unretained partial source"), 0600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(root, "protected"), []byte("synthetic protected file"), 0600)).To(Succeed())
	core.Files = sourcefiles.Files{ExcludedPaths: []string{f.profileDir, filepath.Join(root, "protected")}}
	failed, e := core.Capture(ctx, f.token, key(GinkgoT()), f.project, source.CaptureRequest{VersionID: f.version, SourceMode: "current", Selections: []source.CaptureSelection{{SourceID: "d6000000-0000-4000-8000-000000000002", Paths: []source.CapturePath{{Path: "partial-present"}, {Path: "missing"}, {Path: "protected"}}}}})
	Expect(e).NotTo(HaveOccurred())
	var failedID struct {
		ID string `json:"capture_id"`
	}
	Expect(json.Unmarshal(failed.Body, &failedID)).To(Succeed())
	originalFailures := map[string]domain.SourceCaptureRead{}
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.SourceCaptureReads(failedID.ID, "", 64)
		for _, row := range rows {
			originalFailures[row.ID] = row
		}
		return e
	})).To(Succeed())
	core.Files = interruptedSourceFiles{} // labelled input-read interruption, no source byte was supplied
	interruptedKey := key(GinkgoT())
	interrupted, e := core.Capture(ctx, f.token, interruptedKey, f.project, source.CaptureRequest{VersionID: f.version, SourceMode: "current", Selections: []source.CaptureSelection{{SourceID: "d6000000-0000-4000-8000-000000000002", Paths: []source.CapturePath{{Path: "not-started"}}}}})
	Expect(e).NotTo(HaveOccurred())
	Expect(interrupted.Status).To(Equal(503))
	actor, e := f.auth.Authenticate(ctx, f.token)
	Expect(e).NotTo(HaveOccurred())
	receipt, e := f.store.Receipt(ctx, domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: interruptedKey})
	Expect(e).NotTo(HaveOccurred())
	var normalized map[string]string
	Expect(json.Unmarshal(receipt.Normalization, &normalized)).To(Succeed())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.SourceCaptureReads(normalized["capture_id"], "", 64)
		for _, row := range rows {
			originalFailures[row.ID] = row
		}
		return e
	})).To(Succeed())
	Expect(originalFailures).To(HaveLen(4))
	core.Files = sourcefiles.ManagedFiles(f.profileDir)
	// Plain complete directory remains unchanged. Additional selected snapshots
	// are exported only on the explicit retained-source choice and exact version.
	for _, include := range []bool{false, true} {
		destination := filepath.Join(GinkgoT().TempDir(), "export")
		r, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", VersionID: f.version, Format: "directory", Destination: destination, Records: []string{"references"}, RetainedSources: include})
		Expect(e).NotTo(HaveOccurred())
		var accepted domain.CopyResult
		Expect(json.Unmarshal(r.Body, &accepted)).To(Succeed())
		Expect(copyTurns(f, accepted.JobID, 300).State).To(Equal("completed"))
		_, e = foundation.VerifyExport(ctx, destination, "")
		Expect(e).NotTo(HaveOccurred())
		_, e = os.Stat(filepath.Join(destination, "metadata", "versions", f.version, "objects", reads["snapshot"].Hash))
		if include {
			Expect(e).NotTo(HaveOccurred())
		} else {
			Expect(os.IsNotExist(e)).To(BeTrue())
		}
		_, e = os.Stat(filepath.Join(destination, "metadata", "versions", f.version, "objects", reads["reference"].Hash))
		Expect(os.IsNotExist(e)).To(BeTrue())
	}
	backup := createBackup(f, filepath.Join(GinkgoT().TempDir(), "backups"))
	Expect(copyTurns(f, backup.JobID, 300).State).To(Equal("completed"))
	var set domain.CopySet
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; set, e = tx.CopySet(backup.ID); return e })).To(Succeed())
	_, e = foundation.VerifyBackup(ctx, set.Directory)
	Expect(e).NotTo(HaveOccurred())
	_, e = os.Stat(filepath.Join(set.Directory, "objects", reads["reference"].Hash))
	Expect(os.IsNotExist(e)).To(BeTrue())
	_, e = os.Stat(filepath.Join(set.Directory, "objects", reads["payload-copy"].Hash))
	Expect(e).NotTo(HaveOccurred(), "independent complete payload ownership still requires exact bytes")
	selection := service.Selection{Profile: "source-restored", DataDir: filepath.Join(GinkgoT().TempDir(), "restored")}
	_, e = service.Initialize(selection)
	Expect(e).NotTo(HaveOccurred())
	plan, e := foundation.PlanRestore(ctx, selection, set.Directory)
	Expect(e).NotTo(HaveOccurred())
	_, e = foundation.ApplyRestore(ctx, selection, plan.ID, config.Validate, key(GinkgoT()))
	Expect(e).NotTo(HaveOccurred())
	w, e := service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	defer w.Close()
	store := foundation.New(nil)
	_, closeDB, e := store.Prepare(ctx, w.Profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeDB()
	a := auth.New(store)
	pair, e := a.CreatePairing(ctx, "http://127.0.0.1:9270", "source restore", "fixture")
	Expect(e).NotTo(HaveOccurred())
	_, e = a.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: key(GinkgoT()), Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	session, e := a.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(e).NotTo(HaveOccurred())
	restored := source.New(store, a, store, gitreader.New())
	restored.Files = sourcefiles.ManagedFiles(selection.DataDir)
	Expect(restored.ReadCaptured(ctx, session.Token, reads["snapshot"].ID, func(domain.SourceCaptureRead, io.ReadSeeker) error { return nil })).To(MatchError(domain.ErrReconciliation))
	cfg, e := store.Configuration(ctx)
	Expect(e).NotTo(HaveOccurred())
	choices, e := config.Decode(strings.NewReader(string(cfg.Value)), "json")
	Expect(e).NotTo(HaveOccurred())
	review := config.New(store, a, configpath.Resolver{})
	var hold *domain.RecoveryHold
	Expect(store.Write(ctx, func(tx app.WriteTx) error { var e error; hold, e = tx.RecoveryHold(); return e })).To(Succeed())
	review.RestoreReviewID = hold.ID
	planned, e := review.Plan(ctx, session.Token, key(GinkgoT()), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: cfg.Revision, Changes: choices})
	Expect(e).NotTo(HaveOccurred())
	var p struct {
		ID string `json:"plan_id"`
	}
	Expect(json.Unmarshal(planned.Body, &p)).To(Succeed())
	_, e = review.Commit(ctx, session.Token, p.ID, "", config.CommitRequest{ExpectedRevision: cfg.Revision, Confirmed: true})
	Expect(e).NotTo(HaveOccurred())

	// Restored uncopied rows retain historical capture availability. Comparing a
	// newly authorized identical capture must not report storage omission as change.
	restoredView, e := restored.CaptureView(ctx, session.Token, reads["reference"].CaptureID, "")
	Expect(e).NotTo(HaveOccurred())
	history := restoredView["items"].([]domain.SourceCaptureRead)[0]
	Expect(history.CaptureAvailability).To(Equal("available"))
	Expect(history.Availability).To(Equal("unavailable"))
	Expect(history.Reason).To(Equal("not_retained"))
	Expect(history.ReadState).To(Equal("not_read_by_view"))
	Expect(history.BytesOrigin).To(BeEmpty())
	recapture := func() string {
		GinkgoHelper()
		r, e := restored.Capture(ctx, session.Token, key(GinkgoT()), f.project, source.CaptureRequest{VersionID: f.version, SourceMode: "current", Selections: []source.CaptureSelection{{SourceID: "d6000000-0000-4000-8000-000000000002", Paths: []source.CapturePath{{Path: "reference"}}}}})
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(201))
		var value struct {
			ID string `json:"capture_id"`
		}
		Expect(json.Unmarshal(r.Body, &value)).To(Succeed())
		return value.ID
	}
	same := recapture()
	for _, pair := range [][2]string{{history.CaptureID, same}, {same, history.CaptureID}} {
		diff, e := restored.CompareCaptures(ctx, session.Token, pair[0], pair[1], "")
		Expect(e).NotTo(HaveOccurred())
		Expect(diff["changes"]).To(BeEmpty())
		Expect(diff["next_cursor"]).To(BeNil())
	}

	// Bounded unsupported legacy fixtures must remain conservative in BOTH the
	// metadata projector and SQL keyset diff, including JSON type mismatches.
	legacyDB, e := sql.Open("sqlite", filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	func() {
		defer legacyDB.Close()
		var originalSource, originalLocation string
		Expect(legacyDB.QueryRow(`SELECT CAST(source_json AS TEXT),CAST(location_json AS TEXT) FROM source_capture_reads WHERE read_id=?`, history.ID).Scan(&originalSource, &originalLocation)).To(Succeed())
		for _, bad := range []struct{ source, location string }{
			{originalSource, `{"path":42}`},
			{`{"kind":"snapshot","snapshot_sha256":"` + history.Hash + `","repo_id":42}`, originalLocation},
			{`{"kind":"snapshot","snapshot_sha256":"` + history.Hash + `","exact_revision":42}`, originalLocation},
			{`{}`, originalLocation},
		} {
			_, e = legacyDB.Exec(`UPDATE source_capture_reads SET source_json=?,location_json=? WHERE read_id=?`, []byte(bad.source), []byte(bad.location), history.ID)
			Expect(e).NotTo(HaveOccurred())
			view, e := restored.CaptureView(ctx, session.Token, history.CaptureID, "")
			Expect(e).NotTo(HaveOccurred())
			Expect(view["items"].([]domain.SourceCaptureRead)[0].CaptureAvailability).To(Equal("unavailable"))
			for _, pair := range [][2]string{{history.CaptureID, same}, {same, history.CaptureID}} {
				diff, e := restored.CompareCaptures(ctx, session.Token, pair[0], pair[1], "")
				Expect(e).NotTo(HaveOccurred())
				expectedChanges := 1
				if bad.location != originalLocation {
					expectedChanges = 2
				}
				Expect(diff["changes"]).To(HaveLen(expectedChanges))
			}
		}
		_, e = legacyDB.Exec(`UPDATE source_capture_reads SET source_json=?,location_json=? WHERE read_id=?`, []byte(originalSource), []byte(originalLocation), history.ID)
		Expect(e).NotTo(HaveOccurred())
	}()
	Expect(os.WriteFile(filepath.Join(root, "reference"), []byte("genuinely changed authorized bytes"), 0600)).To(Succeed())
	changed := recapture()
	for _, pair := range [][2]string{{history.CaptureID, changed}, {changed, history.CaptureID}} {
		diff, e := restored.CompareCaptures(ctx, session.Token, pair[0], pair[1], "")
		Expect(e).NotTo(HaveOccurred())
		Expect(diff["changes"]).To(HaveLen(1))
	}
	// A marker without the supported exact source identity cannot invent past IO.
	unsupported := history
	unsupported.Availability = "unavailable"
	unsupported.Reason = "reference_only_uncopied"
	unsupported.SourceJSON = json.RawMessage(`{}`)
	availability, reason := unsupported.CaptureFacts()
	Expect(availability).To(Equal("unavailable"))
	Expect(reason).To(Equal("reference_only_uncopied"))
	Expect(os.RemoveAll(root)).To(Succeed())
	Expect(restored.ReadCaptured(ctx, session.Token, reads["snapshot"].ID, func(_ domain.SourceCaptureRead, r io.ReadSeeker) error {
		body, e := io.ReadAll(r)
		Expect(body).To(Equal(snapshotBytes))
		return e
	})).To(Succeed())
	called := false
	e = restored.ReadCaptured(ctx, session.Token, reads["reference"].ID, func(domain.SourceCaptureRead, io.ReadSeeker) error { called = true; return nil })
	Expect(e).To(HaveOccurred())
	Expect(called).To(BeFalse())
	Expect(store.Write(ctx, func(tx app.WriteTx) error {
		r, e := tx.SourceCaptureRead(reads["reference"].ID)
		if e == nil {
			Expect(r.Availability).To(Equal("unavailable"))
			Expect(r.Reason).To(Equal("reference_only_uncopied"))
		}
		return e
	})).To(Succeed())
	assertOriginalFailureRows := func(store *foundation.Store) {
		GinkgoHelper()
		Expect(store.Write(ctx, func(tx app.WriteTx) error {
			for id, original := range originalFailures {
				current, e := tx.SourceCaptureRead(id)
				if e != nil {
					return e
				}
				if original.Availability == "available" {
					original.Availability, original.Reason = "unavailable", "reference_only_uncopied"
				}
				Expect(current).To(Equal(original), "restore must preserve failed/missing/denied/not_read facts and exact identity")
			}
			return nil
		})).To(Succeed())
	}
	assertOriginalFailureRows(store)
	// A new coherent backup of the restored state exercises repeated projection;
	// historic reasons must not turn into an uncopied success on re-export.
	restoredFixture := *f
	restoredFixture.store, restoredFixture.auth, restoredFixture.token = store, a, session.Token
	second := createBackup(&restoredFixture, filepath.Join(GinkgoT().TempDir(), "reexport"))
	Expect(copyTurns(&restoredFixture, second.JobID, 300).State).To(Equal("completed"))
	var secondSet domain.CopySet
	Expect(store.Write(ctx, func(tx app.WriteTx) error { var e error; secondSet, e = tx.CopySet(second.ID); return e })).To(Succeed())
	_, e = foundation.VerifyBackup(ctx, secondSet.Directory)
	Expect(e).NotTo(HaveOccurred())
	assertOriginalFailureRows(store)
	copiedDB, e := sql.Open("sqlite", "file:"+filepath.Join(secondSet.Directory, "database.sqlite")+"?mode=ro")
	Expect(e).NotTo(HaveOccurred())
	defer copiedDB.Close()
	for id, original := range originalFailures {
		var availability, reason, hash string
		Expect(copiedDB.QueryRow(`SELECT availability,reason,content_hash FROM source_capture_reads WHERE read_id=?`, id).Scan(&availability, &reason, &hash)).To(Succeed())
		if original.Availability == "available" {
			original.Availability, original.Reason = "unavailable", "reference_only_uncopied"
		}
		Expect(availability).To(Equal(original.Availability))
		Expect(reason).To(Equal(original.Reason))
		Expect(hash).To(Equal(original.Hash))
	}
})
