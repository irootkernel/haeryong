package importer_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/ai/codex"
	"haeryong.local/haeryong/internal/adapters/configpath"
	"haeryong.local/haeryong/internal/adapters/foundation"
	appauth "haeryong.local/haeryong/internal/application/auth"
	config "haeryong.local/haeryong/internal/application/configuration"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/questions"
	"haeryong.local/haeryong/internal/application/recovery"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"time"
)

func questionCopy(f *fixture, request domain.CopyRequest) domain.CopySet {
	GinkgoHelper()
	r, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), request)
	Expect(e).NotTo(HaveOccurred())
	Expect(r.Status).To(Equal(202))
	var accepted domain.CopyResult
	Expect(json.Unmarshal(r.Body, &accepted)).To(Succeed())
	var job domain.Job
	for turn := 0; turn < 300; turn++ {
		Expect(f.store.ProcessCopy(ctx, accepted.JobID)).To(Succeed())
		job, e = f.store.Job(ctx, accepted.JobID)
		Expect(e).NotTo(HaveOccurred())
		if job.State == "completed" || job.State == "failed" || job.State == "paused" {
			break
		}
	}
	Expect(job.State).To(Equal("completed"), job.Error)
	var set domain.CopySet
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; set, e = tx.CopySet(accepted.ID); return e })).To(Succeed())
	return set
}

var _ = It("T5QA12 saved question bodies obey normal Trash restore and permanent purge without replaying provider work", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, version, input := askFixture(f)
	input.DisplayState = json.RawMessage(`{"selected_text":"original full saved context","scroll":27}`)
	qid, jid := askStart(s, f, version, input)
	driver := &generationDriver{data: answerBytes("final", "retained answer after original turn")}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: s.Prepare, Apply: s.Apply}
	Expect(p.Process(ctx, jid)).To(Succeed())
	Expect(p.StopProviders()).To(Succeed())
	view, e := s.Question(ctx, f.token, qid)
	Expect(e).NotTo(HaveOccurred())
	answer := view.Answers.Items[0]
	document := view.DocumentID
	lifecycleRaceChange(f, document, "trash")
	_, e = s.Question(ctx, f.token, qid)
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue())
	_, e = s.Answers(ctx, f.token, qid, "")
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue())
	Expect(s.ReadText(ctx, f.token, answer.ID, true, func(domain.Object, io.Reader) error { Fail("hidden answer bytes reached reader"); return nil })).To(MatchError(domain.ErrNotFound))
	_, e = s.Start(ctx, f.token, key(GinkgoT()), version, input)
	Expect(errors.Is(e, domain.ErrNotFound)).To(BeTrue())
	lifecycleRaceChange(f, document, "restore")
	restored, e := s.Question(ctx, f.token, qid)
	Expect(e).NotTo(HaveOccurred())
	Expect(restored.Answers.Items[0].ID).To(Equal(answer.ID))
	var full strings.Builder
	Expect(s.ReadText(ctx, f.token, answer.ID, true, func(_ domain.Object, r io.Reader) error { _, e := io.Copy(&full, r); return e })).To(Succeed())
	Expect(full.String()).To(Equal("retained answer after original turn"))
	Expect(driver.calls.Load()).To(Equal(int32(1)))
	lifecycleRaceChange(f, document, "trash")
	lifecycleRaceChange(f, document, "purge")
	var purge string
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		jobs, e := tx.Jobs()
		for _, j := range jobs {
			if j.Kind == "lifecycle" {
				purge = j.ID
			}
		}
		return e
	})).To(Succeed())
	Expect(purge).NotTo(BeEmpty())
	purger := &work.Purger{Store: f.store, Bytes: f.store}
	var job domain.Job
	for turn := 0; turn < 200; turn++ {
		job, e = f.store.Job(ctx, purge)
		Expect(e).NotTo(HaveOccurred())
		if job.State == "completed" || job.State == "paused" || job.State == "failed" {
			break
		}
		Expect(purger.Process(ctx, purge)).To(Succeed())
	}
	if job.State == "paused" {
		db, e := sql.Open("sqlite", f.dbPath)
		Expect(e).NotTo(HaveOccurred())
		defer db.Close()
		rows, e := db.Query(`SELECT 'reference',owner_kind,owner_id,content_hash FROM object_references WHERE content_hash IN(SELECT content_hash FROM purge_candidates WHERE job_id=?) UNION ALL SELECT 'pin',owner_kind,owner_id,content_hash FROM object_pins WHERE content_hash IN(SELECT content_hash FROM purge_candidates WHERE job_id=?) ORDER BY 1,2,3`, purge, purge)
		Expect(e).NotTo(HaveOccurred())
		var owners [][4]string
		for rows.Next() {
			var row [4]string
			Expect(rows.Scan(&row[0], &row[1], &row[2], &row[3])).To(Succeed())
			owners = append(owners, row)
		}
		Expect(rows.Err()).NotTo(HaveOccurred())
		rows.Close()
		AddReportEntry("actual remaining protection after bounded purge", owners)
	}
	Expect(job.State).To(Equal("paused"))
	Expect(job.Error).To(Equal("protected"))
	// Reusable upload ownership is independent from the purged Question. Its
	// normal explicit abandonment is required before those shared bytes collect.
	uploads, e := f.core.Uploads(ctx, f.token, importer.UploadQuery{})
	Expect(e).NotTo(HaveOccurred())
	Expect(uploads.Items).NotTo(BeEmpty())
	for _, upload := range uploads.Items {
		_, e = f.core.Abandon(ctx, f.token, upload.ID, key(GinkgoT()))
		Expect(e).NotTo(HaveOccurred())
	}
	purger.Now = func() time.Time { return time.Now().Add(61 * time.Second) }
	for turn := 0; turn < 200; turn++ {
		job, e = f.store.Job(ctx, purge)
		Expect(e).NotTo(HaveOccurred())
		if job.State == "completed" {
			break
		}
		Expect(purger.Process(ctx, purge)).To(Succeed())
	}
	Expect(job.State).To(Equal("completed"), job.Error)
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.Question(qid)
		Expect(e).To(MatchError(domain.ErrNotFound))
		_, e = tx.QuestionAnswer(answer.ID)
		Expect(e).To(MatchError(domain.ErrNotFound))
		return nil
	})).To(Succeed())
	Expect(driver.calls.Load()).To(Equal(int32(1)))
})

var _ = It("T5QA13 explicit question export and coherent backup protected restore retain complete owned bytes without source or provider fabrication", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, version, input := askFixture(f)
	input.Question = strings.Repeat("independent retained question α\n", 2048)
	input.DisplayState = json.RawMessage(`{"selection":"` + strings.Repeat("saved display context 한글", 2048) + `","scroll":51}`)
	questionSource(f, s, &input)
	qid, jid := askStart(s, f, version, input)
	body := strings.Repeat("complete retained answer\n", 4096)
	driver := &questionCitationDriver{f: f, observe: true, body: body}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: s.Prepare, Apply: s.Apply}
	Expect(p.Process(ctx, jid)).To(Succeed())
	Expect(p.StopProviders()).To(Succeed())
	view, e := s.Question(ctx, f.token, qid)
	Expect(e).NotTo(HaveOccurred())
	answer := view.Answers.Items[0]
	Expect(answer.Citations).To(HaveLen(1))
	Expect(answer.Citations[0].Availability).To(Equal("available"))
	originalJob, e := f.store.Job(ctx, jid)
	Expect(e).NotTo(HaveOccurred())
	var originalScope domain.ProviderScope
	Expect(json.Unmarshal(originalJob.Scope, &originalScope)).To(Succeed())
	for _, include := range []bool{false, true} {
		destination := filepath.Join(GinkgoT().TempDir(), "export")
		records := []string{}
		if include {
			records = []string{"questions"}
		}
		questionCopy(f, domain.CopyRequest{Action: "export.create", VersionID: version, Format: "directory", Destination: destination, Records: records})
		if include {
			_, e = foundation.VerifyExport(ctx, destination, "")
			Expect(e).NotTo(HaveOccurred())
		} else {
			payload, e := os.ReadFile(filepath.Join(destination, "hello.html"))
			Expect(e).NotTo(HaveOccurred())
			Expect(string(payload)).To(Equal("<html><body>exact explanation</body></html>"))
		}
		recordsPath := filepath.Join(destination, "metadata", "versions", version, "records.jsonl")
		raw, e := os.ReadFile(recordsPath)
		if include {
			Expect(e).NotTo(HaveOccurred())
			Expect(string(raw)).To(ContainSubstring(qid))
			Expect(string(raw)).To(ContainSubstring(answer.ID))
			Expect(string(raw)).NotTo(ContainSubstring("PreparedAnswer"))
			for _, hash := range []string{view.BodyRef.Hash, view.Context.Hash, answer.BodyRef.Hash} {
				_, e := os.Stat(filepath.Join(destination, "metadata", "versions", version, "objects", hash))
				Expect(e).NotTo(HaveOccurred())
			}
		} else {
			Expect(os.IsNotExist(e)).To(BeTrue())
		}
	}
	backup := questionCopy(f, domain.CopyRequest{Action: "backup.create", Destination: filepath.Join(GinkgoT().TempDir(), "backups")})
	_, e = foundation.VerifyBackup(ctx, backup.Directory)
	Expect(e).NotTo(HaveOccurred())
	for _, hash := range []string{view.BodyRef.Hash, view.Context.Hash, answer.BodyRef.Hash} {
		_, e := os.Stat(filepath.Join(backup.Directory, "objects", hash))
		Expect(e).NotTo(HaveOccurred())
	}
	selection := service.Selection{Profile: "restored-question", DataDir: filepath.Join(GinkgoT().TempDir(), "restored")}
	_, e = service.Initialize(selection)
	Expect(e).NotTo(HaveOccurred())
	plan, e := foundation.PlanRestore(ctx, selection, backup.Directory)
	Expect(e).NotTo(HaveOccurred())
	_, e = foundation.ApplyRestore(ctx, selection, plan.ID, config.Validate, key(GinkgoT()))
	Expect(e).NotTo(HaveOccurred())
	writer, e := service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	defer writer.Close()
	store := foundation.New(nil)
	_, closeDB, e := store.Prepare(ctx, writer.Profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeDB()
	auth := appauth.New(store)
	pair, e := auth.CreatePairing(ctx, "http://127.0.0.1:9270", "controlled restored Ask owner", "fixture")
	Expect(e).NotTo(HaveOccurred())
	_, e = auth.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: key(GinkgoT()), Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	session, e := auth.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(e).NotTo(HaveOccurred())
	restored := questions.New(store, auth, nil, codex.SemanticRequestSHA)
	currentActor, e := auth.Authenticate(ctx, session.Token)
	Expect(e).NotTo(HaveOccurred())
	Expect(currentActor.AuthEpoch).NotTo(Equal(originalScope.AuthEpoch))
	heldView, e := restored.Question(ctx, session.Token, qid)
	Expect(e).NotTo(HaveOccurred(), "saved answer history remains readable while restored source access awaits review")
	Expect(heldView.Answers.Items[0].Citations[0].Availability).To(Equal("unavailable"))
	Expect(heldView.Answers.Items[0].Citations[0].BytesOrigin).To(BeNil())
	heldInput := input
	heldInput.Sources.Selections = nil
	heldInput.SourcesPresent = true
	admitted, e := restored.Start(ctx, session.Token, key(GinkgoT()), version, heldInput)
	Expect(e).NotTo(HaveOccurred())
	Expect(admitted.Status).To(Equal(202))
	var held struct {
		Job string `json:"job_id"`
	}
	Expect(json.Unmarshal(admitted.Body, &held)).To(Succeed())
	heldDriver := &generationDriver{data: answerBytes("final", "must not run during restore review")}
	heldCoordinator := &work.ProviderCoordinator{Store: store, Bytes: store, Driver: heldDriver, Prepare: restored.Prepare, Apply: restored.Apply}
	Expect(errors.Is(heldCoordinator.Process(ctx, held.Job), domain.ErrReconciliation)).To(BeTrue())
	Expect(heldCoordinator.StopProviders()).To(Succeed())
	Expect(heldDriver.calls.Load()).To(BeZero())
	heldJob, e := store.Job(ctx, held.Job)
	Expect(e).NotTo(HaveOccurred())
	Expect(heldJob.State).To(Equal("paused"))
	cfg, e := store.Configuration(ctx)
	Expect(e).NotTo(HaveOccurred())
	choices, e := config.Decode(bytes.NewReader(cfg.Value), "json")
	Expect(e).NotTo(HaveOccurred())
	review := config.New(store, auth, configpath.Resolver{})
	var hold *domain.RecoveryHold
	Expect(store.Write(ctx, func(tx app.WriteTx) error { var e error; hold, e = tx.RecoveryHold(); return e })).To(Succeed())
	review.RestoreReviewID = hold.ID
	planned, e := review.Plan(ctx, session.Token, key(GinkgoT()), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: cfg.Revision, Changes: choices})
	Expect(e).NotTo(HaveOccurred())
	var approved struct {
		ID string `json:"plan_id"`
	}
	Expect(json.Unmarshal(planned.Body, &approved)).To(Succeed())
	_, e = review.Commit(ctx, session.Token, approved.ID, "", config.CommitRequest{ExpectedRevision: cfg.Revision, Confirmed: true})
	Expect(e).NotTo(HaveOccurred())
	heldJob, e = store.Job(ctx, held.Job)
	Expect(e).NotTo(HaveOccurred())
	Expect(heldJob.State).To(Equal("paused"))
	Expect(heldDriver.calls.Load()).To(BeZero())
	read, e := restored.Question(ctx, session.Token, qid)
	Expect(e).NotTo(HaveOccurred())
	Expect(read.Answers.Items[0].ID).To(Equal(answer.ID))
	Expect(read.Answers.Items[0].Citations).To(HaveLen(1))
	Expect(read.Answers.Items[0].Citations[0]).To(Equal(answer.Citations[0]), "current owner access recovers without rewriting original citation observation")
	restoredJob, e := store.Job(ctx, jid)
	Expect(e).NotTo(HaveOccurred())
	Expect(restoredJob.Scope).To(Equal(originalJob.Scope), "original attempt scope is immutable across restore")
	Expect(read.Answers.Items[0].ExplanationReferences).To(BeEmpty())
	for _, read := range []struct {
		id     string
		answer bool
		text   string
	}{{qid, false, input.Question}, {answer.ID, true, body}} {
		var full strings.Builder
		Expect(restored.ReadText(ctx, session.Token, read.id, read.answer, func(_ domain.Object, r io.Reader) error { _, e := io.Copy(&full, r); return e })).To(Succeed())
		Expect(full.String()).To(Equal(read.text))
	}
	var context strings.Builder
	Expect(restored.ReadContext(ctx, session.Token, qid, func(_ domain.QuestionContext, _ domain.Object, r io.Reader) error {
		_, e := io.Copy(&context, r)
		return e
	})).To(Succeed())
	Expect(context.String()).To(Equal(string(input.DisplayState)))
	Expect(driver.calls.Load()).To(Equal(int32(1)))
})

var _ = It("T5QA14 real question outbox replay and final event redelivery obey current owner and Trash visibility", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, version, input := askFixture(f)
	epoch, after, e := f.auth.EventHead(ctx, f.token)
	Expect(e).NotTo(HaveOccurred())
	qid, jid := askStart(s, f, version, input)
	driver := &generationDriver{data: answerBytes("final", "saved event answer")}
	p := &work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver, Prepare: s.Prepare, Apply: s.Apply}
	Expect(p.Process(ctx, jid)).To(Succeed())
	Expect(p.StopProviders()).To(Succeed())
	raw, e := f.store.Events(ctx, epoch, after, 1000)
	Expect(e).NotTo(HaveOccurred())
	var saved domain.Event
	for _, row := range raw {
		if row.ResourceKind == "question" && row.Kind == "answer.saved" {
			saved = row
		}
	}
	Expect(saved.ResourceID).To(Equal(qid))
	Expect(saved.Body).NotTo(BeEmpty())
	assertBatch := func(token string, masked bool) {
		GinkgoHelper()
		rows, e := f.auth.Events(ctx, token, epoch, saved.Sequence-1)
		Expect(e).NotTo(HaveOccurred())
		found := false
		for _, row := range rows {
			if row.Sequence == saved.Sequence {
				found = true
				if masked {
					Expect(row.ResourceKind).To(Equal("library"))
					Expect(row.Kind).To(Equal("library.invalidated"))
					Expect(row.ResourceID).To(BeEmpty())
					Expect(row.Body).To(BeEmpty())
				} else {
					Expect(row).To(Equal(saved))
				}
			}
		}
		Expect(found).To(BeTrue())
	}
	assertSingle := func(token string, masked bool) {
		GinkgoHelper()
		row, e := f.auth.FilterEvent(ctx, token, saved)
		Expect(e).NotTo(HaveOccurred())
		if masked {
			Expect(row.Kind).To(Equal("library.invalidated"))
			Expect(row.ResourceID).To(BeEmpty())
			Expect(row.Body).To(BeEmpty())
			Expect(row.Sequence).To(Equal(saved.Sequence))
			Expect(row.StreamEpoch).To(Equal(epoch))
		} else {
			Expect(row).To(Equal(saved))
		}
	}
	assertBatch(f.token, false)
	assertSingle(f.token, false)
	scoped := f.seedScoped(GinkgoT(), domain.CredentialScope{Kind: "workflow", Actions: []string{"version.read", "project.read"}, ProjectIDs: []string{f.project}, VersionIDs: []string{version}})
	assertBatch(scoped, true)
	assertSingle(scoped, true)
	view, e := s.Question(ctx, f.token, qid)
	Expect(e).NotTo(HaveOccurred())
	lifecycleRaceChange(f, view.DocumentID, "trash")
	assertBatch(f.token, true)
	assertSingle(f.token, true)
	lifecycleRaceChange(f, view.DocumentID, "restore")
	assertBatch(f.token, false)
	assertSingle(f.token, false)
	var missing domain.Event
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.AppendEvent("question", key(GinkgoT()), "answer.saved", 1, []byte(`{"answer_id":"missing"}`)); e != nil {
			return e
		}
		rows, e := tx.Events(epoch, saved.Sequence, 1000)
		for _, row := range rows {
			if row.ResourceKind == "question" {
				missing = row
			}
		}
		return e
	})).To(Succeed())
	Expect(missing.Sequence).To(BeNumerically(">", saved.Sequence))
	row, e := f.auth.FilterEvent(ctx, f.token, missing)
	Expect(e).NotTo(HaveOccurred())
	Expect(row.Kind).To(Equal("library.invalidated"))
	Expect(row.Body).To(BeEmpty())
	rows, e := f.auth.Events(ctx, f.token, epoch, missing.Sequence-1)
	Expect(e).NotTo(HaveOccurred())
	Expect(rows[0].Sequence).To(Equal(missing.Sequence))
	Expect(rows[0].Kind).To(Equal("library.invalidated"))
	Expect(rows[0].Body).To(BeEmpty())
	Expect(driver.calls.Load()).To(Equal(int32(1)))
})
