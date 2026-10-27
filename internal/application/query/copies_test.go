package query_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"haeryong.local/haeryong/internal/adapters/localcontrol"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/configpath"
	"haeryong.local/haeryong/internal/adapters/foundation"
	httpauth "haeryong.local/haeryong/internal/adapters/http/auth"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	"haeryong.local/haeryong/internal/application/auth"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/recovery"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

func createBackup(f *fixture, destination string) domain.CopyResult {
	GinkgoHelper()
	core := recovery.New(f.store, f.auth, f.store)
	r, e := core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "backup.create", Destination: destination})
	Expect(e).NotTo(HaveOccurred())
	Expect(r.Status).To(Equal(202))
	var accepted domain.CopyResult
	Expect(json.Unmarshal(r.Body, &accepted)).To(Succeed())
	return accepted
}
func copyTurns(f *fixture, id string, n int) domain.Job {
	GinkgoHelper()
	var j domain.Job
	for i := 0; i < n; i++ {
		Expect(f.store.ProcessCopy(ctx, id)).To(Succeed())
		var e error
		j, e = f.store.Job(ctx, id)
		Expect(e).NotTo(HaveOccurred())
		if j.State == "completed" {
			break
		}
	}
	return j
}

var _ = It("Copies backup production snapshot protects objects during actual purge and verifies full bytes", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	destination := filepath.Join(GinkgoT().TempDir(), "backups")
	accepted := createBackup(f, destination)
	Expect(copyTurns(f, accepted.JobID, 1).State).To(Equal("running"))
	// Membership and backup pins now belong to the real engine, not the old
	// test-only snapshot helper. Real purge advances while each copy turn yields.
	_, purge := purgeAccept(f)
	clock := time.Now()
	p := &work.Purger{Store: f.store, Bytes: f.store, Now: func() time.Time { return clock }}
	for n := 0; n < 100; n++ {
		clock = clock.Add(time.Minute)
		j, e := f.store.Job(ctx, accepted.JobID)
		Expect(e).NotTo(HaveOccurred())
		if j.State == "completed" {
			break
		}
		Expect(f.store.ProcessCopy(ctx, accepted.JobID)).To(Succeed())
		j, e = f.store.Job(ctx, purge.JobID)
		Expect(e).NotTo(HaveOccurred())
		if j.State != "completed" {
			Expect(p.Process(ctx, purge.JobID)).To(Succeed())
		}
	}
	j, e := f.store.Job(ctx, accepted.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	set := filepath.Join(destination, accepted.ID)
	_, e = foundation.VerifyBackup(ctx, set)
	Expect(e).NotTo(HaveOccurred())
	status, e := f.store.BackupStatus(ctx)
	Expect(e).NotTo(HaveOccurred())
	Expect(status.LastComplete.ID).To(Equal(accepted.ID))
	Expect(status.LastTrial.State).To(Equal("not_run"))
	Expect(os.WriteFile(filepath.Join(set, "objects", hash("<html>main</html>")), []byte("corrupt"), 0600)).To(Succeed())
	_, e = foundation.VerifyBackup(ctx, set)
	Expect(e).To(HaveOccurred())
})

var _ = It("Copies protected restore invalidates old tokens rotates once and retains fresh sessions on normal reopen", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	before, e := f.store.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	page, other := "hello.html", "pages/nested.html"
	note, e := noteMutation(f, key(GinkgoT()), "POST", f.version, "", noteBody(&page, nil, "exact retained historical note"))
	Expect(e).NotTo(HaveOccurred())
	moved, e := noteMutation(f, key(GinkgoT()), "PATCH", note.AnnotationID, note.ETag, encodedNote(map[string]any{"action": "reattach", "anchor": domain.AnnotationAnchor{VersionID: f.version, PagePath: &other, PageQuery: "exact=1", PageFragment: "part"}, "selected_text": nil, "context": nil, "context_ref": nil}))
	Expect(e).NotTo(HaveOccurred())
	_, e = noteMutation(f, key(GinkgoT()), "DELETE", note.AnnotationID, moved.ETag, nil)
	Expect(e).NotTo(HaveOccurred())
	reviewed, e := reviewPut(f, key(GinkgoT()), "complete", "", true)
	Expect(e).NotTo(HaveOccurred())
	_, e = reviewPut(f, key(GinkgoT()), "reopen", *reviewed.ETag, false)
	Expect(e).NotTo(HaveOccurred())
	lifecycleCommit(f, lifecyclePlan(f, "archive"))
	f.project = key(GinkgoT())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		return tx.CreateProject(domain.Project{ID: f.project, Name: "Second restore project"})
	})).To(Succeed())
	f.publishVersion(GinkgoT(), map[string]string{"second.html": "second project bytes"}, "link", "second.html")
	_, purge := purgeAccept(f)
	Expect(purge.JobID).NotTo(BeEmpty())

	// Explicitly synthetic future external-result/rejection fixtures. They
	// exercise only generic receipt/resource preservation, never provider calls.
	oldOperation, newOperation := key(GinkgoT()), key(GinkgoT())
	owner, e := f.auth.Authenticate(ctx, f.token)
	Expect(e).NotTo(HaveOccurred())
	reserve := func(operation string) error {
		return f.store.Write(ctx, func(tx app.WriteTx) error {
			_, _, e := tx.ReserveOperation(domain.Request{Key: domain.OperationKey{PrincipalID: owner.PrincipalID, OperationID: operation}, Method: "POST", Target: "/synthetic/external-result", Body: []byte(`{"fixture":"synthetic","outcome":"unknown"}`)}, nil)
			return e
		})
	}
	Expect(reserve(oldOperation)).To(Succeed())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.MutateResource(domain.Resource{Kind: "synthetic_rejection", ID: "fixture-only", Value: []byte(`{"fixture":"synthetic","decision":"rejected","reason":"preserve exact decision"}`)}, 0, "resource.invalidated")
		return e
	})).To(Succeed())
	accepted := createBackup(f, filepath.Join(GinkgoT().TempDir(), "backups"))
	Expect(copyTurns(f, accepted.JobID, 200).State).To(Equal("completed"))
	Expect(reserve(newOperation)).To(Succeed())
	var set domain.CopySet
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; set, e = tx.CopySet(accepted.ID); return e })).To(Succeed())
	selection := service.Selection{Profile: "restored", DataDir: filepath.Join(GinkgoT().TempDir(), "restored")}
	_, e = service.Initialize(selection)
	Expect(e).NotTo(HaveOccurred())
	// Populate the actual destination ledger after the older snapshot. A new
	// isolated empty destination cannot know the source host's newer outcomes.
	priorWriter, e := service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	Expect(priorWriter.PrepareStorageBootstrap(nil)).To(Succeed())
	priorStore := foundation.New(nil)
	_, closePrior, e := priorStore.Prepare(ctx, priorWriter.Profile)
	Expect(e).NotTo(HaveOccurred())
	Expect(priorStore.Write(ctx, func(tx app.WriteTx) error {
		_, _, e := tx.ReserveOperation(domain.Request{Key: domain.OperationKey{PrincipalID: owner.PrincipalID, OperationID: newOperation}, Method: "POST", Target: "/synthetic/newer-orphan-external-result", Body: []byte(`{"fixture":"synthetic","outcome":"unknown"}`)}, nil)
		return e
	})).To(Succeed())
	Expect(priorWriter.MarkStorageReady(nil)).To(Succeed())
	Expect(closePrior()).To(Succeed())
	Expect(priorWriter.Close()).To(Succeed())
	plan, e := foundation.PlanRestore(ctx, selection, set.Directory)
	Expect(e).NotTo(HaveOccurred())
	alias := key(GinkgoT())
	result, e := foundation.ApplyRestore(ctx, selection, plan.ID, config.Validate, alias)
	Expect(e).NotTo(HaveOccurred())
	Expect(result["library_id"]).To(Equal(before.Library.ID))
	Expect(result["runtime_generation"]).NotTo(Equal(before.Library.RuntimeGeneration))
	Expect(result["auth_epoch"]).NotTo(Equal(before.Library.AuthEpoch))
	Expect(result["stream_epoch"]).NotTo(Equal(before.Library.StreamEpoch))
	inventory, e := os.ReadFile(filepath.Join(selection.DataDir, "runtime", "restore-outstanding-"+plan.ID+".jsonl"))
	Expect(e).NotTo(HaveOccurred())
	Expect(string(inventory)).To(ContainSubstring(newOperation))
	laterAlias := key(GinkgoT())
	again, e := foundation.ApplyRestore(ctx, selection, plan.ID, config.Validate, laterAlias)
	Expect(e).NotTo(HaveOccurred())
	Expect(again).To(Equal(result))
	w, e := service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	s := foundation.New(nil)
	_, closeStore, e := s.Prepare(ctx, w.Profile)
	Expect(e).NotTo(HaveOccurred())
	a := auth.New(s)
	_, e = a.Authenticate(ctx, f.token)
	Expect(e).To(HaveOccurred())
	var hold *domain.RecoveryHold
	Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; hold, e = tx.RecoveryHold(); return e })).To(Succeed())
	Expect(hold.State).To(Equal("pending_review"))
	compareCopyTables(set.Snapshot, filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		intent, e := tx.PurgeIntent(purge.JobID)
		if e != nil {
			return e
		}
		if intent.Runtime != before.Library.RuntimeGeneration {
			return errors.New("purge guard identity was rewritten")
		}
		return nil
	})).To(Succeed())
	oldReceipt, e := s.Receipt(ctx, domain.OperationKey{PrincipalID: owner.PrincipalID, OperationID: oldOperation})
	Expect(e).NotTo(HaveOccurred())
	Expect(oldReceipt.State).To(Equal("accepted"))
	_, e = s.Receipt(ctx, domain.OperationKey{PrincipalID: owner.PrincipalID, OperationID: newOperation})
	Expect(e).To(MatchError(domain.ErrNotFound))
	rejection, e := s.Resource(ctx, "synthetic_rejection", "fixture-only")
	Expect(e).NotTo(HaveOccurred())
	Expect(string(rejection.Value)).To(ContainSubstring("rejected"))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		jobs, e := tx.WorkJobs(64)
		if e != nil {
			return e
		}
		for _, job := range jobs {
			if job.Kind == "backup" || job.Kind == "export" || job.Kind == "import" {
				return errors.New("restored old job escaped quarantine")
			}
		}
		return nil
	})).To(Succeed())
	status, e := s.BackupStatus(ctx)
	Expect(e).NotTo(HaveOccurred())
	Expect(status.LastTrial.State).To(Equal("passed"))
	for _, operation := range []string{plan.ID, alias, laterAlias} {
		receipt, e := s.Receipt(ctx, domain.OperationKey{PrincipalID: owner.PrincipalID, OperationID: operation})
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal("completed"))
		Expect(string(receipt.Response.Body)).To(ContainSubstring(plan.ID))
	}
	restoredJob, e := s.Job(ctx, plan.ID)
	Expect(e).NotTo(HaveOccurred())
	Expect(restoredJob.Kind).To(Equal("restore"))
	Expect(restoredJob.State).To(Equal("completed"))
	pair, e := a.CreatePairing(ctx, "http://127.0.0.1:9270", "fresh device", "restore-test")
	Expect(e).NotTo(HaveOccurred())
	_, e = a.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: key(GinkgoT()), Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	session, e := a.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(e).NotTo(HaveOccurred())
	cfg, e := s.Configuration(ctx)
	Expect(e).NotTo(HaveOccurred())
	choices, e := config.Decode(strings.NewReader(string(cfg.Value)), "json")
	Expect(e).NotTo(HaveOccurred())
	for _, cleanup := range [][]string{nil, {key(GinkgoT())}, {accepted.JobID}, {purge.JobID}} {
		review := config.New(s, a, configpath.Resolver{})
		if cleanup != nil {
			review.RestoreReviewID = hold.ID
			review.RestoreCleanupJobs = cleanup
		}
		response, e := review.Plan(ctx, session.Token, key(GinkgoT()), config.PlanRequest{Scope: config.Scope{Kind: "library"}, ExpectedRevision: cfg.Revision, Changes: choices})
		Expect(e).NotTo(HaveOccurred())
		var planned struct {
			ID string `json:"plan_id"`
		}
		Expect(json.Unmarshal(response.Body, &planned)).To(Succeed())
		_, e = review.Commit(ctx, session.Token, planned.ID, "", config.CommitRequest{ExpectedRevision: cfg.Revision, Confirmed: true})
		valid := len(cleanup) == 1 && cleanup[0] == purge.JobID
		if valid {
			Expect(e).NotTo(HaveOccurred())
		} else {
			Expect(e).To(HaveOccurred())
		}
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			h, e := tx.RecoveryHold()
			if e != nil {
				return e
			}
			if valid {
				Expect(h.State).To(Equal("reviewed"))
				j, e := tx.WorkJobs(64)
				if e != nil {
					return e
				}
				seen := false
				for _, job := range j {
					if job.ID == purge.JobID {
						seen = true
					}
					Expect(job.ID).NotTo(Equal(accepted.JobID))
				}
				Expect(seen).To(BeTrue())
			} else {
				Expect(h.State).To(Equal("pending_review"))
			}
			return nil
		})).To(Succeed())
	}
	Expect(closeStore()).To(Succeed())
	Expect(w.Close()).To(Succeed())
	w, e = service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	defer w.Close()
	s = foundation.New(nil)
	_, closeStore, e = s.Prepare(ctx, w.Profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeStore()
	_, e = auth.New(s).Authenticate(ctx, session.Token)
	Expect(e).NotTo(HaveOccurred())
	after, e := s.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.Library.RuntimeGeneration).To(Equal(result["runtime_generation"]))
	// A normal post-review reopen must expose a fresh replay era through the
	// same cursorless poll used by native stream admission, retaining history.
	server := httptest.NewUnstartedServer(nil)
	handler, e := httpauth.New(auth.New(s), httpauth.Policy{Hosts: []string{server.Listener.Addr().String()}, Origins: []string{"http://" + server.Listener.Addr().String()}}, http.NotFoundHandler())
	Expect(e).NotTo(HaveOccurred())
	server.Config.Handler = handler
	server.Start()
	defer server.Close()
	request, e := http.NewRequest(http.MethodGet, server.URL+"/api/v1/events?transport=poll", nil)
	Expect(e).NotTo(HaveOccurred())
	request.Header.Set("Authorization", "Bearer "+session.Token)
	response, e := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	Expect(e).NotTo(HaveOccurred())
	defer response.Body.Close()
	Expect(response.StatusCode).To(Equal(http.StatusOK), "fresh restored replay must not deliver old-epoch events to the final epoch guard")
	var poll struct {
		Data struct {
			StreamEpoch string `json:"stream_epoch"`
			Items       []struct {
				StreamEpoch string `json:"stream_epoch"`
			} `json:"items"`
		} `json:"data"`
	}
	Expect(json.NewDecoder(response.Body).Decode(&poll)).To(Succeed())
	Expect(poll.Data.StreamEpoch).To(Equal(after.Library.StreamEpoch))
	for _, event := range poll.Data.Items {
		Expect(event.StreamEpoch).To(Equal(after.Library.StreamEpoch))
	}
})

var _ = It("Copies checkpoint failures preserve the prior complete set and resume the exact set without new publication", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	destination := filepath.Join(GinkgoT().TempDir(), "backups")
	good := createBackup(f, destination)
	Expect(copyTurns(f, good.JobID, 300).State).To(Equal("completed"))
	for _, phase := range []string{"capture_barrier", "snapshot", "pins", "object_sync", "publish", "after_rename"} {
		accepted := createBackup(f, destination)
		fired := false
		f.store.CopyFault = func(at string) error {
			if at == phase && !fired {
				fired = true
				return syscall.ENOSPC
			}
			return nil
		}
		var failure error
		for n := 0; n < 300; n++ {
			failure = f.store.ProcessCopy(ctx, accepted.JobID)
			if failure != nil {
				break
			}
		}
		Expect(fired).To(BeTrue(), phase)
		Expect(failure).To(MatchError(syscall.ENOSPC))
		f.store.CopyFault = nil
		status, e := f.store.BackupStatus(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(status.LastComplete.ID).To(Equal(good.ID))
		Expect(status.LastAttempt.State).To(Equal("failed"))
		if phase != "after_rename" {
			_, e = foundation.VerifyBackup(ctx, filepath.Join(destination, accepted.ID))
			Expect(e).To(HaveOccurred())
		}
		core := recovery.New(f.store, f.auth, f.store)
		response, e := core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "backup.resume", ID: accepted.ID})
		Expect(e).NotTo(HaveOccurred())
		var resumed domain.CopyResult
		Expect(json.Unmarshal(response.Body, &resumed)).To(Succeed())
		Expect(resumed.ID).To(Equal(accepted.ID))
		Expect(resumed.JobID).NotTo(Equal(accepted.JobID))
		Expect(copyTurns(f, resumed.JobID, 300).State).To(Equal("completed"))
		_, e = foundation.VerifyBackup(ctx, filepath.Join(destination, resumed.ID))
		Expect(e).NotTo(HaveOccurred())
		good = resumed
	}
})

var _ = It("Copies cancelled capture stays protected until explicit abandon and equivalent replay cannot recapture", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	core := recovery.New(f.store, f.auth, f.store)
	destination := filepath.Join(GinkgoT().TempDir(), "backups")
	operation := key(GinkgoT())
	request := domain.CopyRequest{Action: "backup.create", Destination: destination}
	response, e := core.Submit(ctx, f.token, operation, request)
	Expect(e).NotTo(HaveOccurred())
	var accepted domain.CopyResult
	Expect(json.Unmarshal(response.Body, &accepted)).To(Succeed())
	Expect(copyTurns(f, accepted.JobID, 1).State).To(Equal("running"))
	var set domain.CopySet
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		var e error
		set, e = tx.CopySet(accepted.ID)
		if e != nil {
			return e
		}
		_, e = tx.CancelJob(accepted.JobID)
		return e
	})).To(Succeed())
	Expect(f.store.ProcessCopy(ctx, accepted.JobID)).To(Succeed())
	_, e = core.Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "backup.abandon", ID: accepted.ID, Confirmed: true})
	Expect(e).NotTo(HaveOccurred())
	_, e = os.Stat(set.Directory)
	Expect(os.IsNotExist(e)).To(BeTrue())
	replay, e := core.Submit(ctx, f.token, operation, request)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(response))
	request.Destination = filepath.Join(GinkgoT().TempDir(), "different")
	_, e = core.Submit(ctx, f.token, operation, request)
	Expect(e).To(MatchError(domain.ErrConflict))
})

var _ = It("Copies protected running-daemon route replays one acceptance and rejects wrong session discovery origin host and forwarding", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	p := f.store.CopyProfile()
	health, e := f.store.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	endpoint := &localcontrol.HTTP{Auth: f.auth, Copies: recovery.New(f.store, f.auth, f.store)}
	id, e := endpoint.Start(ctx, p, service.Identity{LibraryID: p.LibraryID, ProfileID: p.ProfileID, API: service.APIVersion, InstanceID: key(GinkgoT()), Storage: service.StorageState{Ready: true}}, 0, func() {})
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(endpoint.Close(ctx)).To(Succeed()) })
	operation := key(GinkgoT())
	request := domain.CopyRequest{Action: "backup.create", Destination: filepath.Join(GinkgoT().TempDir(), "backups"), Runtime: health.Library.RuntimeGeneration}
	response, e := localcontrol.CopyCall(ctx, p, id, f.token, operation, request)
	Expect(e).NotTo(HaveOccurred())
	replay, e := localcontrol.CopyCall(ctx, p, id, f.token, operation, request)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(response))
	request.Destination = filepath.Join(GinkgoT().TempDir(), "different")
	_, e = localcontrol.CopyCall(ctx, p, id, f.token, operation, request)
	Expect(e).To(HaveOccurred())
	_, e = localcontrol.CopyCall(ctx, p, id, annotationReaderToken(f), key(GinkgoT()), request)
	Expect(e).To(HaveOccurred())
	_, e = localcontrol.CopyCall(ctx, p, id, "revoked-or-missing", key(GinkgoT()), request)
	Expect(e).To(HaveOccurred())
	wrong := id
	wrong.InstanceID = key(GinkgoT())
	_, e = localcontrol.CopyCall(ctx, p, wrong, f.token, key(GinkgoT()), request)
	Expect(e).To(HaveOccurred())
	for _, mode := range []string{"origin", "host", "forwarded"} {
		req, e := http.NewRequest("POST", id.Endpoint+"/local/v1/copy-requests", strings.NewReader(`{}`))
		Expect(e).NotTo(HaveOccurred())
		switch mode {
		case "origin":
			req.Header.Set("Origin", id.Endpoint)
		case "host":
			req.Host = "foreign.invalid"
		case "forwarded":
			req.Header.Set("X-Forwarded-For", "127.0.0.1")
		}
		res, e := http.DefaultClient.Do(req)
		Expect(e).NotTo(HaveOccurred())
		Expect(res.StatusCode).To(Equal(403))
		Expect(res.Body.Close()).To(Succeed())
	}
})

var _ = It("Copies actual capture barrier permits library writes while purge cannot collect the unpinned snapshot", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	accepted := createBackup(f, filepath.Join(GinkgoT().TempDir(), "backups"))
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	DeferCleanup(func() { once.Do(func() { close(release) }) })
	f.store.CopyFault = func(phase string) error {
		if phase == "snapshot" {
			close(entered)
			<-release
		}
		return nil
	}
	finished := make(chan error, 1)
	go func() { finished <- f.store.ProcessCopy(ctx, accepted.JobID) }()
	Eventually(entered, 30*time.Second).Should(BeClosed())
	_, purge := purgeAccept(f)
	clock := time.Now()
	purger := &work.Purger{Store: f.store, Bytes: f.store, Now: func() time.Time { return clock }}
	for n := 0; n < 60; n++ {
		clock = clock.Add(time.Minute)
		Expect(purger.Process(ctx, purge.JobID)).To(Succeed())
		job, e := f.store.Job(ctx, purge.JobID)
		Expect(e).NotTo(HaveOccurred())
		if job.State == "paused" {
			break
		}
	}
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.MutateResource(domain.Resource{Kind: "copy_concurrent_write", ID: "after_snapshot", Value: []byte(`{}`)}, 0, "resource.invalidated")
		return e
	})).To(Succeed())
	once.Do(func() { close(release) })
	Expect(<-finished).To(Succeed())
	f.store.CopyFault = nil
	Expect(copyTurns(f, accepted.JobID, 300).State).To(Equal("completed"))
	var set domain.CopySet
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; set, e = tx.CopySet(accepted.ID); return e })).To(Succeed())
	_, e := foundation.VerifyBackup(ctx, set.Directory)
	Expect(e).NotTo(HaveOccurred())
})

// Exact row bytes for preserved user/operational data; intentionally exclude
// credentials, generations, config and worker states changed by protected restore.
func compareCopyTables(snapshot, restored string) {
	GinkgoHelper()
	a, e := sql.Open("sqlite", snapshot)
	Expect(e).NotTo(HaveOccurred())
	defer a.Close()
	b, e := sql.Open("sqlite", restored)
	Expect(e).NotTo(HaveOccurred())
	defer b.Close()
	tables := map[string]string{"documents": "document_id", "document_versions": "version_id", "annotations": "annotation_id", "annotation_history": "annotation_id,revision", "review_states": "owner_id,version_id", "review_events": "event_id", "revisit_flags": "owner_id,document_id", "contexts": "context_id", "purge_intents": "job_id", "purge_candidates": "job_id,content_hash", "purged_versions": "version_id", "lifecycle_plans": "plan_id", "lifecycle_plan_items": "plan_id,sequence", "authored_references": "from_version_id,metadata_reference_id"}
	for table, order := range tables {
		digest := func(db *sql.DB) []byte {
			rows, e := db.Query("SELECT * FROM " + table + " ORDER BY " + order)
			Expect(e).NotTo(HaveOccurred())
			defer rows.Close()
			cols, e := rows.Columns()
			Expect(e).NotTo(HaveOccurred())
			h := sha256.New()
			for rows.Next() {
				values := make([]any, len(cols))
				dest := make([]any, len(cols))
				for i := range values {
					dest[i] = &values[i]
				}
				Expect(rows.Scan(dest...)).To(Succeed())
				fmt.Fprintf(h, "%#v\n", values)
			}
			Expect(rows.Err()).NotTo(HaveOccurred())
			return h.Sum(nil)
		}
		Expect(digest(b)).To(Equal(digest(a)), table)
	}
}

var _ = It("Copies whole-set retention honors actual verify use and indefinite default before expiring an older set", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	destination := filepath.Join(GinkgoT().TempDir(), "backups")
	first := createBackup(f, destination)
	Expect(copyTurns(f, first.JobID, 300).State).To(Equal("completed"))
	cfg, e := f.store.Configuration(ctx)
	Expect(e).NotTo(HaveOccurred())
	value, e := config.Decode(strings.NewReader(string(cfg.Value)), "json")
	Expect(e).NotTo(HaveOccurred())
	value["retention"].(map[string]any)["backups"] = "1s"
	Expect(config.Validate(value)).To(Succeed())
	raw, _ := json.Marshal(value)
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		set, e := tx.CopySet(first.ID)
		if e != nil {
			return e
		}
		set.CompletedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
		if e = tx.PutCopySet(set); e != nil {
			return e
		}
		return tx.SetConfiguration(domain.Configuration{Value: raw, Actor: "test-owned-retention-clock-fixture"}, cfg.Revision)
	})).To(Succeed())
	path := filepath.Join(destination, first.ID)
	held, e := os.Open(path)
	Expect(e).NotTo(HaveOccurred())
	defer held.Close()
	Expect(syscall.Flock(int(held.Fd()), syscall.LOCK_SH)).To(Succeed())
	second := createBackup(f, destination)
	Expect(copyTurns(f, second.JobID, 300).State).To(Equal("completed"))
	_, e = os.Stat(path)
	Expect(e).NotTo(HaveOccurred())
	Expect(syscall.Flock(int(held.Fd()), syscall.LOCK_UN)).To(Succeed())
	third := createBackup(f, destination)
	Expect(copyTurns(f, third.JobID, 300).State).To(Equal("completed"))
	_, e = os.Stat(path)
	Expect(os.IsNotExist(e)).To(BeTrue())
	_, e = foundation.VerifyBackup(ctx, filepath.Join(destination, third.ID))
	Expect(e).NotTo(HaveOccurred())
})

func copyPinCount(f *fixture, id string) int {
	GinkgoHelper()
	db, e := sql.Open("sqlite", filepath.Join(f.store.CopyProfile().DataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	var count int
	Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='backup' AND owner_id=?`, id).Scan(&count)).To(Succeed())
	return count
}

var _ = It("Copies detached capture and publication reject changed authority cancellation and generations", Label("persistence"), func() {
	for _, phase := range []string{"snapshot", "object_sync", "export_verified", "backup_verified"} {
		for _, change := range []string{"cancel", "generation", "owner", "runtime", "hold", "permission"} {
			// Full owner backup has no per-project export permission binding.
			if phase == "backup_verified" && change == "permission" {
				continue
			}
			f := newFixture(GinkgoT())
			core := recovery.New(f.store, f.auth, f.store)
			req := domain.CopyRequest{Action: "export.create", VersionID: f.version, Format: "directory", Destination: filepath.Join(GinkgoT().TempDir(), "output")}
			if phase == "backup_verified" {
				req = domain.CopyRequest{Action: "backup.create", Destination: req.Destination}
			}
			response, e := core.Submit(ctx, f.token, key(GinkgoT()), req)
			Expect(e).NotTo(HaveOccurred())
			var accepted domain.CopyResult
			Expect(json.Unmarshal(response.Body, &accepted)).To(Succeed())
			mutated := false
			f.store.CopyFault = func(at string) error {
				if at != phase || mutated {
					return nil
				}
				mutated = true
				switch change {
				case "cancel":
					Expect(f.store.Write(ctx, func(tx app.WriteTx) error { _, e := tx.CancelJob(accepted.JobID); return e })).To(Succeed())
				case "generation":
					f.execSQL("UPDATE jobs SET attempt_generation=attempt_generation+1 WHERE job_id='" + accepted.JobID + "'")
				case "owner":
					f.execSQL("UPDATE auth_owner SET principal_id='" + key(GinkgoT()) + "'")
				case "runtime":
					f.execSQL("UPDATE libraries SET runtime_generation='" + key(GinkgoT()) + "'")
				case "hold":
					f.execSQL(`INSERT INTO recovery_holds VALUES(1,'{"restore_id":"held","state":"pending_review"}')`)
				case "permission":
					f.execSQL("UPDATE projects SET permissions_revision=permissions_revision+1 WHERE project_id='" + f.project + "'")
				}
				return nil
			}
			var err error
			for n := 0; n < 300; n++ {
				err = f.store.ProcessCopy(ctx, accepted.JobID)
				if err != nil {
					break
				}
			}
			Expect(mutated).To(BeTrue(), phase+change)
			Expect(err).To(HaveOccurred())
			f.store.CopyFault = nil
			output := req.Destination
			if phase == "backup_verified" {
				output = filepath.Join(output, accepted.ID)
			}
			_, e = os.Stat(output)
			Expect(os.IsNotExist(e)).To(BeTrue(), phase+change)
			if phase != "snapshot" {
				Expect(copyPinCount(f, accepted.ID)).To(BeNumerically(">", 0))
			}
		}
	}
})

var _ = It("Copies backup manifest tree and inventory mutation classes reject incomplete or mismatched sets", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	accepted := createBackup(f, filepath.Join(GinkgoT().TempDir(), "backups"))
	Expect(copyTurns(f, accepted.JobID, 300).State).To(Equal("completed"))
	var set domain.CopySet
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; set, e = tx.CopySet(accepted.ID); return e })).To(Succeed())
	manifest, e := os.ReadFile(filepath.Join(set.Directory, "manifest.json"))
	Expect(e).NotTo(HaveOccurred())
	var original map[string]any
	Expect(json.Unmarshal(manifest, &original)).To(Succeed())
	for field, value := range map[string]any{"format_version": 2, "schema_revision": 99, "migration_sha256": "bad", "set_id": key(GinkgoT()), "library_id": key(GinkgoT()), "origin_profile_id": key(GinkgoT()), "database_sha256": "bad", "inventory_sha256": "bad", "object_count": 0} {
		var changed map[string]any
		Expect(json.Unmarshal(manifest, &changed)).To(Succeed())
		changed[field] = value
		raw, e := json.Marshal(changed)
		Expect(e).NotTo(HaveOccurred())
		for _, name := range []string{"manifest.json", "COMPLETE.json"} {
			Expect(os.WriteFile(filepath.Join(set.Directory, name), raw, 0600)).To(Succeed())
		}
		_, e = foundation.VerifyBackup(ctx, set.Directory)
		Expect(e).To(HaveOccurred(), field)
	}
	for _, name := range []string{"manifest.json", "COMPLETE.json"} {
		Expect(os.WriteFile(filepath.Join(set.Directory, name), manifest, 0600)).To(Succeed())
	}
	for _, name := range []string{"unexpected", "objects/not-a-hash"} {
		path := filepath.Join(set.Directory, name)
		Expect(os.WriteFile(path, []byte("unexpected"), 0600)).To(Succeed())
		_, e = foundation.VerifyBackup(ctx, set.Directory)
		Expect(e).To(HaveOccurred())
		Expect(os.Remove(path)).To(Succeed())
	}
	path := filepath.Join(set.Directory, "inventory.jsonl")
	raw, e := os.ReadFile(path)
	Expect(e).NotTo(HaveOccurred())
	Expect(os.Remove(path)).To(Succeed())
	Expect(os.Symlink("database.sqlite", path)).To(Succeed())
	_, e = foundation.VerifyBackup(ctx, set.Directory)
	Expect(e).To(HaveOccurred())
	Expect(os.Remove(path)).To(Succeed())
	Expect(os.WriteFile(path, raw, 0600)).To(Succeed())
	_, e = foundation.VerifyBackup(ctx, set.Directory)
	Expect(e).NotTo(HaveOccurred())
	before, e := f.store.BackupStatus(ctx)
	Expect(e).NotTo(HaveOccurred())
	_, e = foundation.VerifyBackup(ctx, set.Directory)
	Expect(e).NotTo(HaveOccurred())
	after, e := f.store.BackupStatus(ctx)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.LastComplete.VerifiedAt).To(Equal(before.LastComplete.VerifiedAt))
	Expect(after.LastTrial).To(Equal(before.LastTrial))
})

var _ = It("Copies signed local grammar corpus starts from successful owner input and rejects each invalid class without effects", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	p := f.store.CopyProfile()
	health, e := f.store.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	endpoint := &localcontrol.HTTP{Auth: f.auth, Copies: recovery.New(f.store, f.auth, f.store)}
	id, e := endpoint.Start(ctx, p, service.Identity{LibraryID: p.LibraryID, ProfileID: p.ProfileID, API: service.APIVersion, InstanceID: key(GinkgoT()), Storage: service.StorageState{Ready: true}}, 0, func() {})
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(endpoint.Close(ctx)).To(Succeed()) })
	body, _ := json.Marshal(domain.CopyRequest{Action: "backup.create", Runtime: health.Library.RuntimeGeneration, Destination: filepath.Join(GinkgoT().TempDir(), "output")})
	call := func(raw []byte, token, nonce, operation, alter string) int {
		request, e := http.NewRequest("POST", id.Endpoint+"/local/v1/copy-requests", strings.NewReader(string(raw)))
		Expect(e).NotTo(HaveOccurred())
		bh := sha256.Sum256(raw)
		th := sha256.Sum256([]byte(token))
		keyBytes, e := hex.DecodeString(p.ControlKey)
		Expect(e).NotTo(HaveOccurred())
		text := "POST\n/local/v1/copy-requests\n" + id.InstanceID + "\n" + p.ProfileID + "\n" + p.LibraryID + "\n" + operation + "\n" + hex.EncodeToString(th[:]) + "\n" + hex.EncodeToString(bh[:])
		mac := hmac.New(sha256.New, keyBytes)
		mac.Write([]byte("copy-request-v1\x00" + nonce + "\x00" + text))
		request.Header.Set("X-Haeryong-Nonce", nonce)
		request.Header.Set("Idempotency-Key", operation)
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-Haeryong-Proof", hex.EncodeToString(mac.Sum(nil)))
		if alter != "" {
			request.Header.Set(alter, "invalid")
		}
		response, e := http.DefaultClient.Do(request)
		Expect(e).NotTo(HaveOccurred())
		defer response.Body.Close()
		return response.StatusCode
	}
	nonce := strings.Repeat("01", 32)
	for _, size := range []int{len(body), 16383, 16384} {
		raw := append(append([]byte{}, body...), []byte(strings.Repeat(" ", size-len(body)))...)
		Expect(call(raw, f.token, nonce, key(GinkgoT()), "")).To(Equal(202))
	}
	db, e := sql.Open("sqlite", f.dbPath)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	counts := func() string {
		var a, b, c int
		Expect(db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&a)).To(Succeed())
		Expect(db.QueryRow(`SELECT count(*) FROM copy_sets`).Scan(&b)).To(Succeed())
		Expect(db.QueryRow(`SELECT count(*) FROM operation_receipts`).Scan(&c)).To(Succeed())
		return fmt.Sprint(a, b, c)
	}
	before := counts()
	invalid := []string{"null", "[]", string(body) + " {}", strings.Replace(string(body), "{", "{\"action\":\"backup.create\",", 1), strings.Replace(string(body), "{", "{\"input\":\"/etc/passwd\",", 1), strings.Replace(string(body), "{", "{\"confirmed\":null,", 1), strings.Replace(string(body), "{", "{\"include_retained_sources\":null,", 1), strings.Replace(string(body), "{", "{\"records\":null,", 1), strings.Replace(string(body), "{", "{\"records\":{},", 1), string(body) + strings.Repeat(" ", 16385-len(body))}
	for n, raw := range invalid {
		Expect(call([]byte(raw), f.token, nonce, key(GinkgoT()), "")).To(Equal(400), n)
		Expect(counts()).To(Equal(before))
	}
	for _, header := range []string{"X-Haeryong-Proof", "X-Haeryong-Nonce"} {
		Expect(call(body, f.token, nonce, key(GinkgoT()), header)).To(Equal(401))
		Expect(counts()).To(Equal(before))
	}
	owner, e := f.auth.Authenticate(ctx, f.token)
	Expect(e).NotTo(HaveOccurred())
	_, e = f.auth.Revoke(ctx, f.token, owner.ID)
	Expect(e).NotTo(HaveOccurred())
	Expect(call(body, f.token, nonce, key(GinkgoT()), "")).To(Equal(401))
	Expect(counts()).To(Equal(before))
})

var _ = It("Copies every persisted snapshot owner branch requires its object metadata including non-version ownership", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	owner, e := f.auth.Authenticate(ctx, f.token)
	Expect(e).NotTo(HaveOccurred())
	for _, kind := range []string{"pin", "reference", "entry", "context", "index", "manifest", "supplied"} {
		snapshot := filepath.Join(GinkgoT().TempDir(), "snapshot.sqlite")
		Expect(f.store.SnapshotDatabase(ctx, snapshot)).To(Succeed())
		db, e := sql.Open("sqlite", snapshot)
		Expect(e).NotTo(HaveOccurred())
		h := strings.Repeat("a", 64)
		_, e = db.Exec(`INSERT INTO content_objects VALUES(?,1,'ready',NULL)`, h)
		Expect(e).NotTo(HaveOccurred())
		var set string
		Expect(db.QueryRow(`SELECT content_set_id FROM document_versions WHERE version_id=?`, f.version).Scan(&set)).To(Succeed())
		switch kind {
		case "pin":
			_, e = db.Exec(`INSERT INTO object_pins VALUES('isolated-owner','read','owner',?)`, h)
		case "reference":
			_, e = db.Exec(`INSERT INTO object_references VALUES('verification_excerpt','exact-owner',?)`, h)
		case "entry":
			_, e = db.Exec(`INSERT INTO version_entries VALUES(?,'owner.bin','file',?,1,NULL)`, set, h)
		case "context":
			_, e = db.Exec(`INSERT INTO contexts VALUES(?,?,?,NULL,NULL,'synthetic-session','application/json',?,1,'2026-10-25T00:00:00Z')`, key(GinkgoT()), owner.PrincipalID, f.version, h)
		case "index":
			_, e = db.Exec(`INSERT INTO metadata_indexes VALUES(?,?,?, ?,NULL,1)`, key(GinkgoT()), owner.PrincipalID, h, set)
		case "manifest":
			_, e = db.Exec(`UPDATE document_versions SET manifest_sha256=? WHERE version_id=?`, h, f.version)
		case "supplied":
			_, e = db.Exec(`UPDATE document_versions SET supplied_metadata_hash=? WHERE version_id=?`, h, f.version)
		}
		Expect(e).NotTo(HaveOccurred(), kind)
		page, e := sqlite.CopyObjectPage(ctx, snapshot, "")
		Expect(e).NotTo(HaveOccurred())
		found := false
		for _, o := range page {
			if o.Hash == h {
				found = true
			}
		}
		Expect(found).To(BeTrue(), kind)
		_, e = db.Exec(`DELETE FROM content_objects WHERE content_hash=?`, h)
		Expect(e).NotTo(HaveOccurred())
		_, e = sqlite.CopyObjectPage(ctx, snapshot, "")
		Expect(e).To(HaveOccurred(), kind)
		Expect(db.Close()).To(Succeed())
	}
})

var _ = It("Copies unavailable destination before capture records failed attempt rather than a running job", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	parent := filepath.Join(GinkgoT().TempDir(), "destination-parent")
	Expect(os.Mkdir(parent, 0700)).To(Succeed())
	accepted := createBackup(f, filepath.Join(parent, "backup"))
	Expect(os.Rename(parent, parent+".unavailable")).To(Succeed())
	Expect(f.store.ProcessCopy(ctx, accepted.JobID)).To(HaveOccurred())
	j, e := f.store.Job(ctx, accepted.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("failed"))
	status, e := f.store.BackupStatus(ctx)
	Expect(e).NotTo(HaveOccurred())
	Expect(status.LastComplete).To(BeNil())
	Expect(status.LastAttempt.Error).To(Equal("copy_input_or_destination_unavailable"))
})

var _ = It("Copies zero and MiB object checkpoints validate prefixes and truncate only unsynced suffixes", Label("persistence"), func() {
	for _, size := range []int{0, 1024*1024 - 1, 1024 * 1024, 1024*1024 + 1} {
		f := newFixture(GinkgoT())
		body := strings.Repeat("b", size)
		f.publishVersion(GinkgoT(), map[string]string{"boundary.bin": body}, "alias", "boundary.bin")
		accepted := createBackup(f, filepath.Join(GinkgoT().TempDir(), "backup"))
		var set domain.CopySet
		if size > 1024*1024 {
			for n := 0; n < 100; n++ {
				Expect(f.store.ProcessCopy(ctx, accepted.JobID)).To(Succeed())
				Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; set, e = tx.CopySet(accepted.ID); return e })).To(Succeed())
				if set.Offset > 0 {
					break
				}
			}
			Expect(set.Offset).To(Equal(int64(1024 * 1024)))
			output := filepath.Join(set.Directory, "objects", hash(body))
			file, e := os.OpenFile(output, os.O_WRONLY, 0600)
			Expect(e).NotTo(HaveOccurred())
			_, e = file.WriteAt([]byte("x"), 0)
			Expect(e).NotTo(HaveOccurred())
			Expect(file.Close()).To(Succeed())
			Expect(f.store.ProcessCopy(ctx, accepted.JobID)).To(MatchError(domain.ErrContentIntegrity))
			Expect(copyPinCount(f, accepted.ID)).To(BeNumerically(">", 0))
			// Repair only test-owned damaged prefix, then append a suffix absent from
			// the durable checkpoint. Resume must keep the prefix and discard suffix.
			Expect(os.WriteFile(output, append([]byte(body[:set.Offset]), []byte("unsynced suffix")...), 0600)).To(Succeed())
			response, e := recovery.New(f.store, f.auth, f.store).Submit(ctx, f.token, key(GinkgoT()), domain.CopyRequest{Action: "backup.resume", ID: accepted.ID})
			Expect(e).NotTo(HaveOccurred())
			var retry domain.CopyResult
			Expect(json.Unmarshal(response.Body, &retry)).To(Succeed())
			Expect(retry.ID).To(Equal(accepted.ID))
			accepted = retry
		}
		Expect(copyTurns(f, accepted.JobID, 300).State).To(Equal("completed"))
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; set, e = tx.CopySet(accepted.ID); return e })).To(Succeed())
		_, e := foundation.VerifyBackup(ctx, set.Directory)
		Expect(e).NotTo(HaveOccurred())
		actual, e := os.ReadFile(filepath.Join(set.Directory, "objects", hash(body)))
		Expect(e).NotTo(HaveOccurred())
		Expect(string(actual)).To(Equal(body))
	}
})
