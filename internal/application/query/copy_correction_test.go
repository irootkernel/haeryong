package query_test

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	"haeryong.local/haeryong/internal/application/auth"
	config "haeryong.local/haeryong/internal/application/configuration"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/application/recovery"
	"haeryong.local/haeryong/internal/application/referencecodec"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

func correctionExport(f *fixture, destination, format string) domain.CopyResult {
	GinkgoHelper()
	r, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", VersionID: f.version, Format: format, Destination: destination, Records: []string{"notes", "review"}})
	Expect(e).NotTo(HaveOccurred())
	var a domain.CopyResult
	Expect(json.Unmarshal(r.Body, &a)).To(Succeed())
	return a
}

var _ = It("CR1 G3 refuses a foreign valid recovered export", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	foreign := filepath.Join(GinkgoT().TempDir(), "foreign")
	a := correctionExport(f, foreign, "directory")
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	f.publishVersion(GinkgoT(), map[string]string{"hello.html": "different frozen output"}, "link", "hello.html")
	destination := filepath.Join(GinkgoT().TempDir(), "destination")
	b := correctionExport(f, destination, "directory")
	interrupted := errors.New("publication interrupted")
	f.store.CopyFault = func(at string) error {
		if at == "export_after_rename" {
			return interrupted
		}
		return nil
	}
	for i := 0; i < 300; i++ {
		if e := f.store.ProcessCopy(ctx, b.JobID); e != nil {
			Expect(e).To(MatchError(interrupted))
			break
		}
	}
	f.store.CopyFault = nil
	Expect(os.RemoveAll(destination)).To(Succeed())
	Expect(os.Rename(foreign, destination)).To(Succeed())
	r, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", ID: b.ID})
	Expect(e).NotTo(HaveOccurred())
	var retry domain.CopyResult
	Expect(json.Unmarshal(r.Body, &retry)).To(Succeed())
	Expect(f.store.ProcessCopy(ctx, retry.JobID)).To(HaveOccurred())
	Expect(copyPinCount(f, b.ID)).To(BeNumerically(">", 0))
})

var _ = It("CR1 G4 exports full annotation history after cross-version reattachment", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	page := "hello.html"
	a, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, nil, "history on A"))
	Expect(e).NotTo(HaveOccurred())
	f.correctionVersion(GinkgoT(), map[string]string{"hello.html": "version B"}, "link", "hello.html")
	b, e := noteMutation(f, key(GinkgoT()), "PATCH", a.AnnotationID, a.ETag, encodedNote(map[string]any{"action": "reattach", "anchor": domain.AnnotationAnchor{VersionID: f.version, PagePath: &page}, "selected_text": nil, "context": map[string]any{"phase": "B"}, "context_ref": nil}))
	Expect(e).NotTo(HaveOccurred())
	destination := filepath.Join(GinkgoT().TempDir(), "output")
	accepted := correctionExport(f, destination, "directory")
	Expect(copyTurns(f, accepted.JobID, 300).State).To(Equal("completed"))
	raw, e := os.ReadFile(filepath.Join(destination, "metadata", "versions", f.version, "records.jsonl"))
	Expect(e).NotTo(HaveOccurred())
	revisions := map[int64]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var r struct {
			Kind  string            `json:"kind"`
			Value domain.Annotation `json:"value"`
		}
		Expect(json.Unmarshal([]byte(line), &r)).To(Succeed())
		if r.Kind == "notes" {
			revisions[r.Value.Revision] = true
		}
	}
	Expect(revisions).To(HaveLen(int(b.Revision)))
	Expect(revisions[a.Revision]).To(BeTrue())
})

var _ = It("CR1 G5 rejects checksum-consistent nested and inner identity mutations", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	_, e := reviewPut(f, key(GinkgoT()), "complete", "", true)
	Expect(e).NotTo(HaveOccurred())
	destination := filepath.Join(GinkgoT().TempDir(), "output")
	a := correctionExport(f, destination, "directory")
	Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
	indexPath := filepath.Join(destination, "export-index.json")
	raw, e := os.ReadFile(indexPath)
	Expect(e).NotTo(HaveOccurred())
	var index map[string]any
	Expect(json.Unmarshal(raw, &index)).To(Succeed())
	v := index["versions"].([]any)[0].(map[string]any)
	v["unexpected"] = true
	changed, _ := json.Marshal(index)
	Expect(os.WriteFile(indexPath, changed, 0600)).To(Succeed())
	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred(), "closed nested grammar")
	delete(v, "unexpected")
	records := filepath.Join(destination, "metadata", "versions", f.version, "records.jsonl")
	body, e := os.ReadFile(records)
	Expect(e).NotTo(HaveOccurred())
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	for i, line := range lines {
		var row map[string]any
		Expect(json.Unmarshal([]byte(line), &row)).To(Succeed())
		if row["kind"] == "review" {
			row["value"].(map[string]any)["version_id"] = key(GinkgoT())
			x, _ := json.Marshal(row)
			lines[i] = string(x)
		}
	}
	body = []byte(strings.Join(lines, "\n") + "\n")
	Expect(os.WriteFile(records, body, 0600)).To(Succeed())
	sum := sha256.Sum256(body)
	v["records_sha256"] = hex.EncodeToString(sum[:])
	changed, _ = json.Marshal(index)
	Expect(os.WriteFile(indexPath, changed, 0600)).To(Succeed())
	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).To(HaveOccurred(), "inner version binding")
})

var _ = It("CR1 G8 failed destination ancestor sync prevents completion", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	a := createBackup(f, filepath.Join(GinkgoT().TempDir(), "new-root"))
	injected := errors.New("ancestor sync failed")
	f.store.CopyFault = func(at string) error {
		if at == "backup_destination_parent_sync" {
			return injected
		}
		return nil
	}
	var failure error
	for i := 0; i < 300; i++ {
		failure = f.store.ProcessCopy(ctx, a.JobID)
		if failure != nil {
			break
		}
		j, _ := f.store.Job(ctx, a.JobID)
		if j.State == "completed" {
			break
		}
	}
	Expect(failure).To(MatchError(injected))
	Expect(copyPinCount(f, a.ID)).To(BeNumerically(">", 0))
})

// Queue an actual production upload-seal job after the copy barrier is held.
func correctionPendingSeal(f *fixture) string {
	GinkgoHelper()
	target, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": nil})
	r, e := f.core.Create(ctx, f.token, key(GinkgoT()), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: target})
	Expect(e).NotTo(HaveOccurred())
	var upload struct {
		ID string `json:"upload_id"`
	}
	Expect(json.Unmarshal(r.Body, &upload)).To(Succeed())
	content := "independent real queued seal"
	length := int64(len(content))
	ack, e := f.core.Put(ctx, f.token, upload.ID, importer.Chunk{Path: "work.html", Kind: "file", Hash: hash(content), Length: &length, Final: true}, strings.NewReader(content))
	Expect(e).NotTo(HaveOccurred())
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	r, e = f.core.Complete(ctx, f.token, upload.ID, key(GinkgoT()), cp)
	Expect(e).NotTo(HaveOccurred())
	var job struct {
		ID string `json:"job_id"`
	}
	Expect(json.Unmarshal(r.Body, &job)).To(Succeed())
	return job.ID
}

var _ = It("CR1 G7 actual dispatcher advances real queued work throughout long phases and drains before exit", Label("persistence"), func() {
	for _, phase := range []string{"snapshot", "backup_final_verification", "export_final_transfer", "export_verification", "retention_verify"} {
		f := newFixture(GinkgoT())
		core := recovery.New(f.store, f.auth, f.store)
		destination := filepath.Join(GinkgoT().TempDir(), "copy")
		if phase == "retention_verify" {
			old := createBackup(f, destination)
			Expect(copyTurns(f, old.JobID, 300).State).To(Equal("completed"))
			cfg, e := f.store.Configuration(ctx)
			Expect(e).NotTo(HaveOccurred())
			value, e := config.Decode(strings.NewReader(string(cfg.Value)), "json")
			Expect(e).NotTo(HaveOccurred())
			value["retention"].(map[string]any)["backups"] = "1s"
			raw, _ := json.Marshal(value)
			Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
				set, e := tx.CopySet(old.ID)
				if e != nil {
					return e
				}
				set.CompletedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
				if e = tx.PutCopySet(set); e != nil {
					return e
				}
				return tx.SetConfiguration(domain.Configuration{Value: raw, Actor: "retention-clock-fixture"}, cfg.Revision)
			})).To(Succeed())
		}
		request := domain.CopyRequest{Action: "backup.create", Destination: destination}
		if strings.HasPrefix(phase, "export_") {
			request = domain.CopyRequest{Action: "export.create", VersionID: f.version, Format: "zip", Destination: destination, Records: []string{"notes"}}
		}
		r, e := core.Submit(ctx, f.token, key(GinkgoT()), request)
		Expect(e).NotTo(HaveOccurred())
		var accepted domain.CopyResult
		Expect(json.Unmarshal(r.Body, &accepted)).To(Succeed())
		entered, release := make(chan struct{}), make(chan struct{})
		var held, unblock sync.Once
		f.store.CopyFault = func(at string) error {
			if at == phase {
				held.Do(func() { close(entered); <-release })
			}
			return nil
		}
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			done <- (work.Dispatcher{Store: f.store, Copies: core, Imports: f.core}).Run(runCtx)
			close(done)
		}()
		cleanup := func() { cancel(); unblock.Do(func() { close(release) }); Expect(<-done).To(Succeed()) }
		DeferCleanup(cleanup)
		Eventually(entered, 5*time.Second).Should(BeClosed(), phase)
		// Initial queued claim is legitimate, not a stale-generation cancellation.
		j, e := f.store.Job(ctx, accepted.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.AttemptGeneration).To(BeNumerically(">", 0))
		if phase != "retention_verify" {
			Expect(j.State).To(Equal("running"))
		} else {
			Expect(j.State).To(Equal("completed"))
		}
		independent := correctionPendingSeal(f)
		Eventually(func() string {
			j, e := f.store.Job(ctx, independent)
			if e != nil {
				return e.Error()
			}
			return j.State
		}, 5*time.Second).Should(Equal("completed"), phase)
		cancel()
		select {
		case <-done:
			Fail("dispatcher returned before held phase drained")
		default:
		}
		unblock.Do(func() { close(release) })
		Expect(<-done).To(Succeed())
	}
})

func (f *fixture) correctionVersion(t GinkgoTInterface, files map[string]string, alias, target string, metadata ...string) {
	GinkgoHelper()
	doc := &f.document
	base := f.version
	targetJSON, _ := json.Marshal(map[string]any{"project_id": f.project, "document_id": doc})
	r, e := f.core.Create(ctx, f.token, key(t), importer.CreateRequest{Purpose: "result", InputMode: "directory", Target: targetJSON})
	Expect(e != nil).To(BeFalse())
	var created struct {
		ID string `json:"upload_id"`
	}
	_ = json.Unmarshal(r.Body, &created)
	var ack importer.ChunkResult
	for path, content := range files {
		length := int64(len(content))
		ack, e = f.core.Put(ctx, f.token, created.ID, importer.Chunk{Path: path, Kind: "file", Hash: hash(content), Length: &length, Final: true}, strings.NewReader(content))
		Expect(e != nil).To(BeFalse())
	}
	ack, e = f.core.Put(ctx, f.token, created.ID, importer.Chunk{Path: alias, Kind: "symlink", LinkTarget: target}, strings.NewReader(`{"link_target":"`+target+`"}`))
	Expect(e != nil).To(BeFalse())
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	r, e = f.core.Complete(ctx, f.token, created.ID, key(t), cp)
	Expect(e != nil).To(BeFalse())
	var envelope struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(r.Body, &envelope)
	Expect(f.core.Process(ctx, envelope.JobID) != nil).To(BeFalse())
	version := key(t)
	input := importer.ImportRequest{UploadID: created.ID, DocumentPresent: true, DocumentID: doc, BasePresent: true, BaseVersionID: &base, VersionID: version}
	if len(metadata) > 0 {
		input.Metadata = strings.NewReader(metadata[0])
	}
	importResponse, e := f.core.Import(ctx, f.token, key(t), input)
	Expect(e != nil).To(BeFalse())
	var imported struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(importResponse.Body, &imported)
	Expect(f.core.Process(ctx, imported.JobID) != nil).To(BeFalse())
	j, e := f.store.Job(ctx, imported.JobID)
	Expect(e != nil).To(BeFalse())
	var result struct {
		DocumentID string `json:"document_id"`
		VersionID  string `json:"version_id"`
	}
	Expect(json.Unmarshal(j.Result, &result) != nil).To(BeFalse())
	f.document, f.version = result.DocumentID, result.VersionID
}

var _ = It("CR1 G6 restored complete rows cannot delete origin backups", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	destination := filepath.Join(GinkgoT().TempDir(), "origin")
	first := createBackup(f, destination)
	Expect(copyTurns(f, first.JobID, 300).State).To(Equal("completed"))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		set, e := tx.CopySet(first.ID)
		if e != nil {
			return e
		}
		set.CompletedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
		return tx.PutCopySet(set)
	})).To(Succeed())
	second := createBackup(f, destination)
	Expect(copyTurns(f, second.JobID, 300).State).To(Equal("completed"))
	selection := service.Selection{Profile: "retention", DataDir: filepath.Join(GinkgoT().TempDir(), "restored")}
	_, e := service.Initialize(selection)
	Expect(e).NotTo(HaveOccurred())
	plan, e := foundation.PlanRestore(ctx, selection, filepath.Join(destination, second.ID))
	Expect(e).NotTo(HaveOccurred())
	_, e = foundation.ApplyRestore(ctx, selection, plan.ID, config.Validate)
	Expect(e).NotTo(HaveOccurred())
	w, e := service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	defer w.Close()
	s := foundation.New(nil)
	_, closeStore, e := s.Prepare(ctx, w.Profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeStore()
	a := auth.New(s)
	pair, e := a.CreatePairing(ctx, "http://127.0.0.1:9270", "restored owner", "retention")
	Expect(e).NotTo(HaveOccurred())
	_, e = a.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: key(GinkgoT()), Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	token, e := a.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(e).NotTo(HaveOccurred())
	// The restored owner explicitly reviews configuration before finite retention.
	cfg, e := s.Configuration(ctx)
	Expect(e).NotTo(HaveOccurred())
	choices, e := config.Decode(strings.NewReader(string(cfg.Value)), "json")
	Expect(e).NotTo(HaveOccurred())
	choices["retention"].(map[string]any)["backups"] = "1s"
	review := config.New(s, a, nil)
	review.RestoreReviewID = plan.ID
	response, e := review.Plan(ctx, token.Token, key(GinkgoT()), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: cfg.Revision, Changes: choices})
	Expect(e).NotTo(HaveOccurred())
	var planned struct {
		ID string `json:"plan_id"`
	}
	Expect(json.Unmarshal(response.Body, &planned)).To(Succeed())
	_, e = review.Commit(ctx, token.Token, planned.ID, "", config.CommitRequest{ExpectedRevision: cfg.Revision, Confirmed: true})
	Expect(e).NotTo(HaveOccurred())
	response, e = recovery.New(s, a, s).Submit(ctx, token.Token, key(GinkgoT()), domain.CopyRequest{Action: "backup.create", Destination: filepath.Join(GinkgoT().TempDir(), "local")})
	Expect(e).NotTo(HaveOccurred())
	var accepted domain.CopyResult
	Expect(json.Unmarshal(response.Body, &accepted)).To(Succeed())
	for i := 0; i < 300; i++ {
		Expect(s.ProcessCopy(ctx, accepted.JobID)).To(Succeed())
		j, e := s.Job(ctx, accepted.JobID)
		Expect(e).NotTo(HaveOccurred())
		if j.State == "completed" {
			break
		}
	}
	_, e = os.Stat(filepath.Join(destination, first.ID))
	Expect(e).NotTo(HaveOccurred(), "origin backup is observed, never destination-owned")
})

var _ = It("CR1 G3 frozen output binding covers directory ZIP plain and separate two-output recovery", Label("persistence"), func() {
	for _, format := range []string{"directory", "zip"} {
		for _, layout := range []string{"embedded", "separate", "plain"} {
			f := newFixture(GinkgoT())
			core := recovery.New(f.store, f.auth, f.store)
			request := func(destination string) domain.CopyRequest {
				r := domain.CopyRequest{Action: "export.create", VersionID: f.version, Format: format, Destination: destination}
				if layout != "plain" {
					r.Records = []string{"notes"}
				}
				if layout == "separate" {
					r.MetadataDestination = destination + "-metadata"
				}
				return r
			}
			run := func(r domain.CopyRequest) domain.CopyResult {
				response, e := core.Submit(ctx, f.token, key(GinkgoT()), r)
				Expect(e).NotTo(HaveOccurred())
				var accepted domain.CopyResult
				Expect(json.Unmarshal(response.Body, &accepted)).To(Succeed())
				return accepted
			}
			foreign := filepath.Join(GinkgoT().TempDir(), "foreign")
			a := run(request(foreign))
			Expect(copyTurns(f, a.JobID, 300).State).To(Equal("completed"))
			f.publishVersion(GinkgoT(), map[string]string{"hello.html": "new frozen output"}, "link", "hello.html")
			destination := filepath.Join(GinkgoT().TempDir(), "destination")
			r := request(destination)
			b := run(r)
			failure := errors.New("after publication")
			f.store.CopyFault = func(at string) error {
				if at == "export_after_rename" {
					return failure
				}
				return nil
			}
			for i := 0; i < 300; i++ {
				if e := f.store.ProcessCopy(ctx, b.JobID); e != nil {
					Expect(e).To(MatchError(failure))
					break
				}
			}
			f.store.CopyFault = nil
			var before domain.CopySet
			Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; before, e = tx.CopySet(b.ID); return e })).To(Succeed())
			Expect(before.ExpectedIndexSHA).NotTo(BeEmpty())
			Expect(os.RemoveAll(destination)).To(Succeed())
			Expect(os.Rename(foreign, destination)).To(Succeed())
			if layout == "separate" {
				Expect(os.RemoveAll(r.MetadataDestination)).To(Succeed())
				Expect(os.Rename(foreign+"-metadata", r.MetadataDestination)).To(Succeed())
			}
			retry := run(domain.CopyRequest{Action: "export.create", ID: b.ID})
			Expect(f.store.ProcessCopy(ctx, retry.JobID)).To(HaveOccurred())
			Expect(copyPinCount(f, b.ID)).To(BeNumerically(">", 0))
			var after domain.CopySet
			Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; after, e = tx.CopySet(b.ID); return e })).To(Succeed())
			Expect(after.PlanDigest).To(Equal(before.PlanDigest))
			Expect(after.ExpectedIndexSHA).To(Equal(before.ExpectedIndexSHA))
		}
	}
})

func correctionSealedBody(f *fixture, target map[string]any, text string) string {
	GinkgoHelper()
	r, e := f.core.Create(ctx, f.token, key(GinkgoT()), importer.CreateRequest{Purpose: "body", InputMode: "object", Target: encodedNote(target)})
	Expect(e).NotTo(HaveOccurred())
	var created struct {
		ID string `json:"upload_id"`
	}
	Expect(json.Unmarshal(r.Body, &created)).To(Succeed())
	length := int64(len(text))
	ack, e := f.core.Put(ctx, f.token, created.ID, importer.Chunk{Path: "_input", Kind: "file", Hash: hash(text), Length: &length, Final: true}, strings.NewReader(text))
	Expect(e).NotTo(HaveOccurred())
	cp := ack.Checkpoint
	cp.Generation++
	cp.Complete = true
	r, e = f.core.Complete(ctx, f.token, created.ID, key(GinkgoT()), cp)
	Expect(e).NotTo(HaveOccurred())
	var sealed struct {
		ID string `json:"job_id"`
	}
	Expect(json.Unmarshal(r.Body, &sealed)).To(Succeed())
	Expect(f.core.Process(ctx, sealed.ID)).To(Succeed())
	return created.ID
}

func correctionRead(input, format, name string) []byte {
	GinkgoHelper()
	if format == "directory" {
		raw, e := os.ReadFile(filepath.Join(input, filepath.FromSlash(name)))
		Expect(e).NotTo(HaveOccurred())
		return raw
	}
	z, e := zip.OpenReader(input)
	Expect(e).NotTo(HaveOccurred())
	defer z.Close()
	for _, entry := range z.File {
		if entry.Name == name {
			r, e := entry.Open()
			Expect(e).NotTo(HaveOccurred())
			raw, e := io.ReadAll(r)
			Expect(e).NotTo(HaveOccurred())
			Expect(r.Close()).To(Succeed())
			return raw
		}
	}
	Fail("missing ZIP entry " + name)
	return nil
}

var _ = It("CR1 G4 complete retained A B C identity closure preserves object bytes deletion and outside payload anchors", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	page := "hello.html"
	versionA := f.version
	operation := key(GinkgoT())
	body := correctionSealedBody(f, map[string]any{"operation_id": operation, "action": "annotation_create", "version_id": f.version, "page_path": page}, "object body A")
	note, e := noteMutation(f, operation, "POST", f.version, "", encodedNote(map[string]any{"page_path": page, "region_id": nil, "page_query": "a=1", "page_fragment": "A", "body": nil, "body_ref": map[string]string{"upload_id": body}, "selected_text": nil, "context": map[string]any{"phase": "A"}, "context_ref": nil}))
	Expect(e).NotTo(HaveOccurred())
	f.correctionVersion(GinkgoT(), map[string]string{"hello.html": "B payload"}, "link", "hello.html")
	versionB := f.version
	note, e = noteMutation(f, key(GinkgoT()), "PATCH", note.AnnotationID, note.ETag, encodedNote(map[string]any{"action": "reattach", "anchor": domain.AnnotationAnchor{VersionID: f.version, PagePath: &page, PageQuery: "b=2", PageFragment: "B"}, "selected_text": nil, "context": map[string]any{"phase": "B-move"}, "context_ref": nil}))
	Expect(e).NotTo(HaveOccurred())
	operation = key(GinkgoT())
	body = correctionSealedBody(f, map[string]any{"operation_id": operation, "action": "annotation_edit", "annotation_id": note.AnnotationID}, "object body B")
	note, e = noteMutation(f, operation, "PATCH", note.AnnotationID, note.ETag, encodedNote(map[string]any{"action": "edit", "body": nil, "body_ref": map[string]string{"upload_id": body}, "selected_text": nil, "context": map[string]any{"phase": "B-edit"}, "context_ref": nil}))
	Expect(e).NotTo(HaveOccurred())
	verify := func(selected []string) {
		GinkgoHelper()
		var expected []domain.Annotation
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			var e error
			expected, e = tx.AnnotationHistory(note.AnnotationID, 0, 64)
			return e
		})).To(Succeed())
		objects := map[string][]byte{}
		for _, n := range expected {
			for _, resource := range []string{"body", "context"} {
				if resource == "body" && n.BodyObject == nil || resource == "context" && n.ContextRef == nil {
					continue
				}
				var hash string
				if resource == "body" {
					hash = n.BodyObject.Hash
				} else {
					Expect(f.store.Write(ctx, func(tx app.WriteTx) error { c, e := tx.Context(*n.ContextRef); hash = c.Object.Hash; return e })).To(Succeed())
				}
				var bytes strings.Builder
				Expect(f.queries.ReadAnnotationObject(ctx, f.token, n.ID, n.Revision, resource, func(_ domain.Object, _ string, r io.ReadSeeker) error { _, e := io.Copy(&bytes, r); return e })).To(Succeed())
				objects[hash] = []byte(bytes.String())
			}
		}
		for _, format := range []string{"directory", "zip"} {
			for _, version := range selected {
				destination := filepath.Join(GinkgoT().TempDir(), "output")
				request := domain.CopyRequest{Action: "export.create", VersionID: version, Format: format, Destination: destination, Records: []string{"notes"}}
				if version == "all" {
					request.VersionID = ""
					request.DocumentID = f.document
					request.Versions = "all"
				}
				response, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), request)
				Expect(e).NotTo(HaveOccurred())
				var accepted domain.CopyResult
				Expect(json.Unmarshal(response.Body, &accepted)).To(Succeed())
				Expect(copyTurns(f, accepted.JobID, 500).State).To(Equal("completed"))
				_, e = foundation.VerifyExport(ctx, destination, "")
				Expect(e).NotTo(HaveOccurred())
				var index struct {
					Versions []domain.CopyVersion `json:"versions"`
				}
				Expect(json.Unmarshal(correctionRead(destination, format, "export-index.json"), &index)).To(Succeed())
				if version != "all" {
					Expect(index.Versions).To(HaveLen(1))
					Expect(index.Versions[0].VersionID).To(Equal(version))
				} else {
					Expect(index.Versions).To(HaveLen(3))
				}
				for _, v := range index.Versions {
					records := correctionRead(destination, format, *v.MetadataRoot+"/records.jsonl")
					found := map[int64]json.RawMessage{}
					for _, line := range strings.Split(strings.TrimSpace(string(records)), "\n") {
						var row struct {
							Kind  string          `json:"kind"`
							Value json.RawMessage `json:"value"`
						}
						Expect(json.Unmarshal([]byte(line), &row)).To(Succeed())
						if row.Kind == "notes" {
							var n domain.Annotation
							Expect(json.Unmarshal(row.Value, &n)).To(Succeed())
							Expect(n.ID).To(Equal(note.AnnotationID))
							found[n.Revision] = row.Value
						}
					}
					Expect(found).To(HaveLen(len(expected)))
					for _, n := range expected {
						raw, _ := json.Marshal(n)
						Expect(found[n.Revision]).To(MatchJSON(raw))
					}
					for hash, bytes := range objects {
						Expect(correctionRead(destination, format, *v.MetadataRoot+"/objects/"+hash)).To(Equal(bytes))
					}
					dependencies := string(correctionRead(destination, format, *v.MetadataRoot+"/dependencies.jsonl"))
					Expect(dependencies).To(ContainSubstring(`"kind":"note_anchor"`))
				}
			}
		}
	}
	verify([]string{versionA, versionB})
	f.correctionVersion(GinkgoT(), map[string]string{"hello.html": "C payload"}, "link", "hello.html")
	versionC := f.version
	note, e = noteMutation(f, key(GinkgoT()), "PATCH", note.AnnotationID, note.ETag, encodedNote(map[string]any{"action": "reattach", "anchor": domain.AnnotationAnchor{VersionID: versionC, PagePath: &page}, "selected_text": nil, "context": map[string]any{"phase": "C"}, "context_ref": nil}))
	Expect(e).NotTo(HaveOccurred())
	verify([]string{"all", versionC})
	// Another identity only on C must not join a history-only B selection.
	unrelated, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, nil, "unrelated C note"))
	Expect(e).NotTo(HaveOccurred())
	_ = unrelated
	note, e = noteMutation(f, key(GinkgoT()), "DELETE", note.AnnotationID, note.ETag, nil)
	Expect(e).NotTo(HaveOccurred())
	// For the all/current selection the unrelated C note is legitimately selected;
	// the exact single-identity oracle above is intentionally for A/history-only B.
	verify([]string{versionA, versionB})
})

var _ = It("CR1 G7 cancellation fences immediately but confirms stop and permits abandon only after drain", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	core := recovery.New(f.store, f.auth, f.store)
	response, e := core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "backup.create", Destination: filepath.Join(GinkgoT().TempDir(), "output")})
	Expect(e).NotTo(HaveOccurred())
	var accepted domain.CopyResult
	Expect(json.Unmarshal(response.Body, &accepted)).To(Succeed())
	entered, release := make(chan struct{}), make(chan struct{})
	var held, unblock sync.Once
	f.store.CopyFault = func(at string) error {
		if at == "snapshot" {
			held.Do(func() { close(entered); <-release })
		}
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- (work.Dispatcher{Store: f.store, Copies: core, Imports: f.core}).Run(runCtx)
		close(done)
	}()
	DeferCleanup(func() { cancel(); unblock.Do(func() { close(release) }); Expect(<-done).To(Succeed()) })
	Eventually(entered, 5*time.Second).Should(BeClosed())
	var cancelled domain.Job
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; cancelled, e = tx.CancelJob(accepted.JobID); return e })).To(Succeed())
	Expect(cancelled.State).To(Equal("cancelling"))
	_, e = core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "backup.abandon", ID: accepted.ID, Confirmed: true})
	Expect(e).To(MatchError(domain.ErrTransition))
	_, e = core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "backup.resume", ID: accepted.ID})
	Expect(e).To(MatchError(domain.ErrTransition))
	independent := correctionPendingSeal(f)
	Eventually(func() string { j, _ := f.store.Job(ctx, independent); return j.State }, 5*time.Second).Should(Equal("completed"))
	j, e := f.store.Job(ctx, accepted.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("cancelling"))
	var heldSet domain.CopySet
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; heldSet, e = tx.CopySet(accepted.ID); return e })).To(Succeed())
	Expect(heldSet.Capturing).To(BeTrue(), "snapshot barrier precedes pin handoff")
	unblock.Do(func() { close(release) })
	Eventually(func() string { j, _ := f.store.Job(ctx, accepted.JobID); return j.State }, 5*time.Second).Should(Equal("cancelled"))
	operation := key(GinkgoT())
	Eventually(func() error {
		_, e := core.Submit(ctx, f.token, operation, domain.CopyRequest{Action: "backup.abandon", ID: accepted.ID, Confirmed: true})
		return e
	}, 5*time.Second).Should(Succeed())
	Expect(copyPinCount(f, accepted.ID)).To(Equal(0))
})

var _ = It("CR1 G5 every emitted family rejects checksum-consistent semantic mutations", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	page := "hello.html"
	_, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, nil, "actual note and context"))
	Expect(e).NotTo(HaveOccurred())
	_, e = reviewPut(f, key(GinkgoT()), "complete", "", true)
	Expect(e).NotTo(HaveOccurred())
	_, e = f.queries.RevisitPut(ctx, f.token, key(GinkgoT()), f.document, "", true, []byte(`{"flag":false}`))
	Expect(e).NotTo(HaveOccurred())
	for _, v := range []struct {
		kind       query.PersonalKind
		page, body string
	}{{query.ReaderPreference, "", `{"default_page":"hello.html"}`}, {query.ReadingPosition, "", `{"page_path":"hello.html","page_query":"q=1","page_fragment":"part","opened":true}`}, {query.PagePosition, "hello.html", `{"page_query":"q=2","page_fragment":"x","position":{"arbitrary":{"value":"preserved"}}}`}} {
		_, e = f.put(GinkgoT(), v.kind, v.page, query.PersonalView{}, true, v.body)
		Expect(e).NotTo(HaveOccurred())
	}
	// Labelled typed reference fixture exercises the actual persisted emitter;
	// it is not evidence of an import/derived reference-production journey.
	raw, _ := json.Marshal(map[string]any{"id": "typed-export-fixture", "document_id": f.document, "version_id": f.version, "page_path": page, "region_id": nil})
	reference, e := referencecodec.DecodeReference(raw)
	Expect(e).NotTo(HaveOccurred())
	reference.FromVersionID = f.version
	prepared, e := app.PrepareAuthoredReference(reference)
	Expect(e).NotTo(HaveOccurred())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.PutAuthoredReference(prepared) })).To(Succeed())
	destination := filepath.Join(GinkgoT().TempDir(), "output")
	response, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", VersionID: f.version, Format: "directory", Destination: destination, Records: []string{"notes", "review", "reader", "revisit", "references"}})
	Expect(e).NotTo(HaveOccurred())
	var accepted domain.CopyResult
	Expect(json.Unmarshal(response.Body, &accepted)).To(Succeed())
	Expect(copyTurns(f, accepted.JobID, 300).State).To(Equal("completed"))
	indexPath := filepath.Join(destination, "export-index.json")
	originalIndex, e := os.ReadFile(indexPath)
	Expect(e).NotTo(HaveOccurred())
	recordsPath := filepath.Join(destination, "metadata", "versions", f.version, "records.jsonl")
	originalRecords, e := os.ReadFile(recordsPath)
	Expect(e).NotTo(HaveOccurred())
	// Index checks have no stale enclosing checksum to explain their rejection.
	for _, field := range []string{"document_id", "version_id", "payload_sha256", "manifest_sha256", "payload_root", "inventory_path", "metadata_root"} {
		var index map[string]any
		Expect(json.Unmarshal(originalIndex, &index)).To(Succeed())
		v := index["versions"].([]any)[0].(map[string]any)
		delete(v, field)
		raw, _ := json.Marshal(index)
		Expect(os.WriteFile(indexPath, raw, 0600)).To(Succeed())
		_, e = foundation.VerifyExport(ctx, destination, "")
		Expect(e).To(HaveOccurred(), "missing "+field)
	}
	Expect(os.WriteFile(indexPath, originalIndex, 0600)).To(Succeed())
	for _, test := range []struct{ kind, field string }{{"review", "version_id"}, {"review", "owner_id"}, {"revisit", "document_id"}, {"revisit", "owner_id"}, {"reader", "resource_id"}, {"reader", "kind"}, {"references", "from_version_id"}, {"references", "metadata_reference_id"}, {"notes", "document_id"}, {"notes", "context_ref"}, {"context", "version_id"}, {"context", "page_path"}} {
		lines := strings.Split(strings.TrimSpace(string(originalRecords)), "\n")
		changed := false
		for i, line := range lines {
			var row map[string]any
			Expect(json.Unmarshal([]byte(line), &row)).To(Succeed())
			if row["kind"] == test.kind {
				value := row["value"].(map[string]any)
				value[test.field] = key(GinkgoT())
				updated, _ := json.Marshal(row)
				lines[i] = string(updated)
				changed = true
				break
			}
		}
		Expect(changed).To(BeTrue(), test.kind+"/"+test.field)
		body := []byte(strings.Join(lines, "\n") + "\n")
		Expect(os.WriteFile(recordsPath, body, 0600)).To(Succeed())
		var index map[string]any
		Expect(json.Unmarshal(originalIndex, &index)).To(Succeed())
		v := index["versions"].([]any)[0].(map[string]any)
		sum := sha256.Sum256(body)
		v["records_sha256"] = hex.EncodeToString(sum[:])
		updated, _ := json.Marshal(index)
		Expect(os.WriteFile(indexPath, updated, 0600)).To(Succeed())
		_, e = foundation.VerifyExport(ctx, destination, "")
		Expect(e).To(HaveOccurred(), test.kind+"/"+test.field)
	}
	Expect(os.WriteFile(recordsPath, originalRecords, 0600)).To(Succeed())
	Expect(os.WriteFile(indexPath, originalIndex, 0600)).To(Succeed())
	_, e = foundation.VerifyExport(ctx, destination, "")
	Expect(e).NotTo(HaveOccurred())
})

var _ = It("CR1 G7 queued cancellation retains resumable identity and a linked retry claims its actual generation", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	core := recovery.New(f.store, f.auth, f.store)
	operation := key(GinkgoT())
	request := domain.CopyRequest{Action: "backup.create", Destination: filepath.Join(GinkgoT().TempDir(), "output")}
	response, e := core.Submit(ctx, f.token, operation, request)
	Expect(e).NotTo(HaveOccurred())
	var original domain.CopyResult
	Expect(json.Unmarshal(response.Body, &original)).To(Succeed())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.CancelJob(original.JobID)
		if e != nil {
			return e
		}
		Expect(j.State).To(Equal("cancelled"))
		return nil
	})).To(Succeed())
	response, e = core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "backup.resume", ID: original.ID})
	Expect(e).NotTo(HaveOccurred())
	var retry domain.CopyResult
	Expect(json.Unmarshal(response.Body, &retry)).To(Succeed())
	Expect(retry.ID).To(Equal(original.ID))
	Expect(retry.JobID).NotTo(Equal(original.JobID))
	replay, e := core.Submit(ctx, f.token, operation, request)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay.Body).To(MatchJSON(responseBody(original)))
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- (work.Dispatcher{Store: f.store, Copies: core, Imports: f.core}).Run(runCtx)
		close(done)
	}()
	DeferCleanup(func() { cancel(); Expect(<-done).To(Succeed()) })
	Eventually(func() string { j, _ := f.store.Job(ctx, retry.JobID); return j.State }, 10*time.Second).Should(Equal("completed"))
	j, e := f.store.Job(ctx, original.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("cancelled"))
	Expect(j.CancelRequested).To(BeTrue())
})

func responseBody(v any) []byte { raw, _ := json.Marshal(v); return raw }

var _ = It("CR1 G7 terminal failed job still owns its phase until the dispatcher collects it", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	core := recovery.New(f.store, f.auth, f.store)
	accepted := createBackup(f, filepath.Join(GinkgoT().TempDir(), "output"))
	failure := errors.New("snapshot failed")
	f.store.CopyFault = func(at string) error {
		if at == "snapshot" {
			return failure
		}
		return nil
	}
	Expect(core.Process(ctx, accepted.JobID)).To(Succeed())
	DeferCleanup(func() { Expect(core.StopCopies()).To(Succeed()) })
	Eventually(func() string { j, _ := f.store.Job(ctx, accepted.JobID); return j.State }, 5*time.Second).Should(Equal("failed"))
	_, e := core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "backup.resume", ID: accepted.ID})
	Expect(e).To(MatchError(domain.ErrTransition))
	// PollCopy is the same sole-collector operation Dispatcher.Run uses. Neither
	// DB terminality nor API submission clears the owned completion slot.
	f.store.CopyFault = nil
	operation := key(GinkgoT())
	Eventually(func() error {
		if e := core.PollCopy(ctx); e != nil {
			return e
		}
		_, e := core.Submit(ctx, f.token, operation, domain.CopyRequest{Action: "backup.resume", ID: accepted.ID})
		return e
	}, 5*time.Second).Should(Succeed())
})

var _ = It("CR1 G3 partial sidecar recovery and missing expected bindings refuse completion", Label("persistence"), func() {
	for _, phase := range []string{"export_sidecar_renamed", "export_after_rename"} {
		for _, missing := range []bool{false, true} {
			f := newFixture(GinkgoT())
			core := recovery.New(f.store, f.auth, f.store)
			destination := filepath.Join(GinkgoT().TempDir(), "output")
			metadata := filepath.Join(GinkgoT().TempDir(), "metadata")
			response, e := core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", VersionID: f.version, Format: "directory", Destination: destination, MetadataDestination: metadata, Records: []string{"notes"}})
			Expect(e).NotTo(HaveOccurred())
			var accepted domain.CopyResult
			Expect(json.Unmarshal(response.Body, &accepted)).To(Succeed())
			failure := errors.New("publication interrupted")
			f.store.CopyFault = func(at string) error {
				if at == phase {
					return failure
				}
				return nil
			}
			for i := 0; i < 300; i++ {
				if e := f.store.ProcessCopy(ctx, accepted.JobID); e != nil {
					Expect(e).To(MatchError(failure))
					break
				}
			}
			f.store.CopyFault = nil
			if missing {
				Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
					set, e := tx.CopySet(accepted.ID)
					if e != nil {
						return e
					}
					set.ExpectedIndexSHA = ""
					return tx.PutCopySet(set)
				})).To(Succeed())
			} else {
				index := filepath.Join(metadata, "export-index.json")
				raw, e := os.ReadFile(index)
				Expect(e).NotTo(HaveOccurred())
				Expect(os.WriteFile(index, append(raw, ' '), 0600)).To(Succeed())
			}
			response, e = core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", ID: accepted.ID})
			Expect(e).NotTo(HaveOccurred())
			var retry domain.CopyResult
			Expect(json.Unmarshal(response.Body, &retry)).To(Succeed())
			Expect(f.store.ProcessCopy(ctx, retry.JobID)).To(HaveOccurred())
			Expect(copyPinCount(f, accepted.ID)).To(BeNumerically(">", 0))
		}
	}
})

var _ = It("CR1 G3 recovery refuses extra output bytes outside the index-bound payload roots", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	core := recovery.New(f.store, f.auth, f.store)
	destination := filepath.Join(GinkgoT().TempDir(), "output")
	accepted := correctionExport(f, destination, "directory")
	failure := errors.New("after rename")
	f.store.CopyFault = func(at string) error {
		if at == "export_after_rename" {
			return failure
		}
		return nil
	}
	for i := 0; i < 300; i++ {
		if e := f.store.ProcessCopy(ctx, accepted.JobID); e != nil {
			Expect(e).To(MatchError(failure))
			break
		}
	}
	f.store.CopyFault = nil
	Expect(os.WriteFile(filepath.Join(destination, "unselected-private-data"), []byte("not in the frozen formatted output"), 0600)).To(Succeed())
	// The ordinary inventory verifier alone is insufficient for recovered-output
	// ownership: this file lies outside every declared payload/metadata resource.
	_, e := foundation.VerifyExport(ctx, destination, "")
	Expect(e).NotTo(HaveOccurred())
	response, e := core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", ID: accepted.ID})
	Expect(e).NotTo(HaveOccurred())
	var retry domain.CopyResult
	Expect(json.Unmarshal(response.Body, &retry)).To(Succeed())
	Expect(f.store.ProcessCopy(ctx, retry.JobID)).To(HaveOccurred())
	Expect(copyPinCount(f, accepted.ID)).To(BeNumerically(">", 0))
})

var _ = It("CR1 G8 every export tree directory sync failure retains exact ownership until a clean retry", Label("persistence"), func() {
	for _, format := range []string{"directory", "zip"} {
		f := newFixture(GinkgoT())
		core := recovery.New(f.store, f.auth, f.store)
		destination := filepath.Join(GinkgoT().TempDir(), "output")
		metadata := filepath.Join(GinkgoT().TempDir(), "metadata")
		response, e := core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", VersionID: f.version, Format: format, Destination: destination, MetadataDestination: metadata, Records: []string{"notes"}})
		Expect(e).NotTo(HaveOccurred())
		var accepted domain.CopyResult
		Expect(json.Unmarshal(response.Body, &accepted)).To(Succeed())
		seen := map[string]bool{}
		failure := errors.New("directory fsync failed")
		f.store.CopyFault = func(at string) error {
			if strings.HasPrefix(at, "directory_sync:") {
				key := at
				if i := strings.Index(at, "/.export-metadata-"); i >= 0 {
					tail := at[i+1:]
					if slash := strings.Index(tail, "/"); slash >= 0 {
						key = "sidecar/" + tail[slash+1:]
					} else {
						key = "sidecar/."
					}
				}
				if !seen[key] {
					seen[key] = true
					return failure
				}
			}
			return nil
		}
		complete := false
		for attempt := 0; attempt < 40; attempt++ {
			var err error
			for turn := 0; turn < 300; turn++ {
				err = f.store.ProcessCopy(ctx, accepted.JobID)
				if err != nil {
					break
				}
				j, _ := f.store.Job(ctx, accepted.JobID)
				if j.State == "completed" {
					complete = true
					break
				}
			}
			if complete {
				break
			}
			Expect(err).To(MatchError(failure))
			Expect(copyPinCount(f, accepted.ID)).To(BeNumerically(">", 0))
			response, e = core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "export.create", ID: accepted.ID})
			Expect(e).NotTo(HaveOccurred())
			var retry domain.CopyResult
			Expect(json.Unmarshal(response.Body, &retry)).To(Succeed())
			Expect(retry.ID).To(Equal(accepted.ID))
			accepted = retry
		}
		f.store.CopyFault = nil
		Expect(complete).To(BeTrue())
		Expect(len(seen)).To(BeNumerically(">=", 4))
		Expect(copyPinCount(f, accepted.ID)).To(Equal(0))
		_, e = foundation.VerifyExport(ctx, destination, filepath.Join(metadata, "export-index.json"))
		Expect(e).NotTo(HaveOccurred())
	}
})
