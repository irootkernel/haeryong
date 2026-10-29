package query_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/gitreader"
	sourcefiles "haeryong.local/haeryong/internal/adapters/source"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

const purgeSourceID = "d6000000-0000-4000-8000-000000000011"

func sourcePurgeFixture() (*fixture, *source.Service, string) {
	GinkgoHelper()
	f := newFixture(GinkgoT())
	root := GinkgoT().TempDir()
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		cfg, e := tx.Configuration()
		if e != nil {
			return e
		}
		var v map[string]any
		if e = json.Unmarshal(cfg.Value, &v); e != nil {
			return e
		}
		v["projects"] = map[string]any{f.project: map[string]any{"sources": []map[string]any{{"source_id": purgeSourceID, "kind": "files", "root_path": root, "repo_id": nil, "retention_mode": "selected_snapshot", "provider_ids": []string{"codex"}}}}}
		cfg.Value, _ = json.Marshal(v)
		return tx.SetConfiguration(cfg, cfg.Revision)
	})).To(Succeed())
	core := source.New(f.store, f.auth, f.store, gitreader.New())
	core.Files = sourcefiles.ManagedFiles(f.profileDir)
	return f, core, root
}
func sourcePurgeCapture(f *fixture, core *source.Service, version, path string, admit source.CaptureAdmission) domain.SourceCaptureRead {
	GinkgoHelper()
	r, e := core.CaptureAndAdmit(ctx, f.token, key(GinkgoT()), f.project, source.CaptureRequest{VersionID: version, SourceMode: "current", ProviderID: "codex", Selections: []source.CaptureSelection{{SourceID: purgeSourceID, Paths: []source.CapturePath{{Path: path}}}}}, admit)
	Expect(e).NotTo(HaveOccurred())
	Expect(r.Status).To(Equal(201))
	var c struct {
		ID string `json:"capture_id"`
	}
	Expect(json.Unmarshal(r.Body, &c)).To(Succeed())
	var rows []domain.SourceCaptureRead
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; rows, e = tx.SourceCaptureReads(c.ID, "", 1); return e })).To(Succeed())
	Expect(rows).To(HaveLen(1))
	return rows[0]
}

type sourcePurgeDriver struct{ calls int }

func (d *sourcePurgeDriver) Execute(ctx context.Context, a domain.ProviderAttempt, inputs []domain.ProviderInput, send func() error, observe func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	if e := send(); e != nil {
		return work.ProviderOutcome{}, e
	}
	d.calls++
	return work.ProviderOutcome{State: "outcome_unknown", Reason: "transport_unknown"}, nil
}
func (*sourcePurgeDriver) Cancel(context.Context, domain.ProviderAttempt) error { return nil }
func (*sourcePurgeDriver) Reconcile(context.Context, domain.ProviderAttempt, bool) (work.ProviderOutcome, error) {
	return work.ProviderOutcome{State: "outcome_unknown"}, nil
}

var _ = It("SCAP20 version snapshot purge preserves active reads/project snapshots and project-target job uncertainty before actual eligible GC", Label("persistence"), func() {
	for _, independent := range []bool{false, true} {
		f, core, root := sourcePurgeFixture()
		Expect(os.WriteFile(filepath.Join(root, "version"), []byte("version-only source bytes"), 0600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "active"), []byte("active source bytes"), 0600)).To(Succeed())
		versionRead := sourcePurgeCapture(f, core, f.version, "version", nil)
		var standalone domain.SourceCaptureRead
		if independent {
			standalone = sourcePurgeCapture(f, core, "", "version", nil)
		}
		job := key(GinkgoT())
		activeRead := sourcePurgeCapture(f, core, f.version, "active", func(tx app.WriteTx, c domain.SourceCapture) error {
			rows, e := tx.SourceCaptureReads(c.ID, "", 1)
			if e != nil {
				return e
			}
			owner, e := tx.AuthState()
			if e != nil {
				return e
			}
			target, _ := json.Marshal(map[string]string{"project_id": f.project})
			scope, _ := json.Marshal(domain.ProviderScope{ProviderID: "codex", PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, TargetSetID: c.TargetSetID, ReadIDs: []string{rows[0].ID}, RequestSHA: hash("source purge uncertainty")})
			if e = tx.CreateJob(domain.Job{ID: job, Kind: "question", Scope: scope, ConfigRevision: c.ConfigRevision, Permissions: map[string]int64{f.project: c.PermissionsRevision}}); e != nil {
				return e
			}
			return tx.AttachSourceInputs(job, []string{rows[0].ID})
		})
		driver := &sourcePurgeDriver{}
		p := work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: driver}
		Expect(p.Process(ctx, job)).To(Succeed())
		Expect(driver.calls).To(Equal(1))
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			attempts, e := tx.ProviderAttempts(job)
			if e != nil {
				return e
			}
			inputs, e := tx.ProviderInputs(attempts[0].ID)
			if e == nil {
				Expect(inputs).To(HaveLen(1))
				Expect(inputs[0].SourceVersionID).To(Equal(f.version))
			}
			return e
		})).To(Succeed())
		entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		var once sync.Once
		joined := false
		join := func() {
			once.Do(func() { close(release) })
			if !joined {
				Eventually(done, 30*time.Second).Should(Receive(Succeed()))
				joined = true
			}
		}
		DeferCleanup(join)
		var object domain.Object
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; object, e = tx.Object(versionRead.Hash); return e })).To(Succeed())
		go func() {
			done <- f.store.Read(ctx, object, domain.Pin{Kind: "source_capture_read", OwnerID: versionRead.ID}, func(io.ReadSeeker) error { close(entered); <-release; return nil })
		}()
		Eventually(entered, 30*time.Second).Should(BeClosed())
		plan, accepted := purgeAccept(f)
		Expect(plan.Impact["managed_source_snapshot"]).To(BeNumerically(">=", 2))
		abandonPurgeUploads(f)
		clock := time.Now()
		purger := &work.Purger{Store: f.store, Bytes: f.store, Now: func() time.Time { return clock }}
		for n := 0; n < 5; n++ {
			clock = clock.Add(time.Minute)
			Expect(purger.Process(ctx, accepted.JobID)).To(Succeed())
		}
		j, e := f.store.Job(ctx, job)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.CancelRequested).To(BeTrue(), "source version association fences a project-target job too")
		Expect(f.store.Collect(ctx, domain.Object{Hash: activeRead.Hash, Size: activeRead.Size})).To(MatchError(domain.ErrPinned))
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			r, e := tx.SourceCaptureRead(versionRead.ID)
			Expect(r.Availability).To(Equal("available"), "unknown effect blocks version source detachment")
			return e
		})).To(Succeed())
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			attempts, e := tx.ProviderAttempts(job)
			if e != nil {
				return e
			}
			Expect(attempts).To(HaveLen(1))
			if e = tx.ObserveProviderAttempt(attempts[0].ID, "cancelled", "", "", "cancel_confirmed"); e != nil {
				return e
			}
			j, e := tx.Job(job)
			if e != nil {
				return e
			}
			return tx.ConfirmStop(job, j.AttemptGeneration)
		})).To(Succeed())
		for n := 0; n < 100; n++ {
			clock = clock.Add(time.Minute)
			Expect(purger.Process(ctx, accepted.JobID)).To(Succeed())
			j, e = f.store.Job(ctx, accepted.JobID)
			Expect(e).NotTo(HaveOccurred())
			if j.State == "completed" {
				break
			}
		}
		Expect(j.State).To(Equal("paused"), "actual reader pin still protects bytes")
		_, e = os.Stat(filepath.Join(f.profileDir, "storage", "objects", versionRead.Hash))
		Expect(e).NotTo(HaveOccurred())
		called := false
		Expect(core.ReadCaptured(ctx, f.token, versionRead.ID, func(domain.SourceCaptureRead, io.ReadSeeker) error { called = true; return nil })).To(MatchError(domain.ErrNotFound))
		Expect(called).To(BeFalse())
		join()
		for n := 0; n < 150; n++ {
			clock = clock.Add(time.Minute)
			Expect(purger.Process(ctx, accepted.JobID)).To(Succeed())
			j, e = f.store.Job(ctx, accepted.JobID)
			Expect(e).NotTo(HaveOccurred())
			if j.State == "completed" {
				break
			}
		}
		_, e = os.Stat(filepath.Join(f.profileDir, "storage", "objects", activeRead.Hash))
		Expect(os.IsNotExist(e)).To(BeTrue())
		if independent {
			Expect(j.State).To(Equal("paused"))
			_, e = os.Stat(filepath.Join(f.profileDir, "storage", "objects", versionRead.Hash))
			Expect(e).NotTo(HaveOccurred())
			Expect(core.ReadCaptured(ctx, f.token, standalone.ID, func(_ domain.SourceCaptureRead, r io.ReadSeeker) error {
				body, e := io.ReadAll(r)
				Expect(body).To(Equal([]byte("version-only source bytes")))
				return e
			})).To(Succeed())
		} else {
			Expect(j.State).To(Equal("completed"))
			_, e = os.Stat(filepath.Join(f.profileDir, "storage", "objects", versionRead.Hash))
			Expect(os.IsNotExist(e)).To(BeTrue())
		}
	}
})

var _ = It("SCAP21 version purge waits for an in-flight capture to refuse stale publication and release its own temporary bytes", Label("persistence"), func() {
	f, core, root := sourcePurgeFixture()
	body := []byte("in-flight unpublished capture")
	Expect(os.WriteFile(filepath.Join(root, "active"), body, 0600)).To(Succeed())
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan domain.Response, 1)
	var once sync.Once
	joined := false
	unblock := func() { once.Do(func() { close(release) }) }
	DeferCleanup(func() {
		unblock()
		if !joined {
			Eventually(done, 30*time.Second).Should(Receive())
			joined = true
		}
	})
	core.Fault = func(stage string) error {
		if stage == "capture_before_commit" {
			close(entered)
			<-release
		}
		return nil
	}
	go func() {
		defer GinkgoRecover()
		r, e := core.Capture(ctx, f.token, key(GinkgoT()), f.project, source.CaptureRequest{VersionID: f.version, SourceMode: "current", Selections: []source.CaptureSelection{{SourceID: purgeSourceID, Paths: []source.CapturePath{{Path: "active"}}}}})
		Expect(e).NotTo(HaveOccurred())
		done <- r
	}()
	Eventually(entered, 30*time.Second).Should(BeClosed())
	Expect(f.store.Collect(ctx, domain.Object{Hash: hash(string(body)), Size: int64(len(body))})).To(MatchError(domain.ErrPinned))
	_, accepted := purgeAccept(f)
	abandonPurgeUploads(f)
	clock := time.Now()
	purger := &work.Purger{Store: f.store, Bytes: f.store, Now: func() time.Time { return clock }}
	for n := 0; n < 5; n++ {
		clock = clock.Add(time.Minute)
		Expect(purger.Process(ctx, accepted.JobID)).To(Succeed())
	}
	j, e := f.store.Job(ctx, accepted.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("running"))
	unblock()
	var response domain.Response
	Eventually(done, 30*time.Second).Should(Receive(&response))
	joined = true
	Expect(response.Status).To(Equal(404))
	Expect(f.store.Collect(ctx, domain.Object{Hash: hash(string(body)), Size: int64(len(body))})).To(Succeed())
	for n := 0; n < 150; n++ {
		clock = clock.Add(time.Minute)
		Expect(purger.Process(ctx, accepted.JobID)).To(Succeed())
		j, e = f.store.Job(ctx, accepted.JobID)
		Expect(e).NotTo(HaveOccurred())
		if j.State == "completed" {
			break
		}
	}
	Expect(j.State).To(Equal("completed"))
})

var _ = It("SCAP40 purge impact counts actual version snapshot owners rather than reference-only digest history", Label("persistence"), func() {
	for _, independent := range []bool{false, true} {
		f, core, root := sourcePurgeFixture()
		Expect(os.WriteFile(filepath.Join(root, "snapshot"), []byte("version snapshot exact"), 0600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "reference"), []byte("unretained digest only"), 0600)).To(Succeed())
		snapshot := sourcePurgeCapture(f, core, f.version, "snapshot", nil)
		var standalone domain.SourceCaptureRead
		if independent {
			standalone = sourcePurgeCapture(f, core, "", "snapshot", nil)
		}
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			cfg, e := tx.Configuration()
			if e != nil {
				return e
			}
			var value map[string]any
			if e = json.Unmarshal(cfg.Value, &value); e != nil {
				return e
			}
			value["projects"].(map[string]any)[f.project].(map[string]any)["sources"].([]any)[0].(map[string]any)["retention_mode"] = "reference_only"
			cfg.Value, _ = json.Marshal(value)
			return tx.SetConfiguration(cfg, cfg.Revision)
		})).To(Succeed())
		reference := sourcePurgeCapture(f, core, f.version, "reference", nil)
		var object domain.Object
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { var e error; object, e = tx.Object(reference.Hash); return e })).To(Succeed())
		Expect(f.store.Collect(ctx, object)).To(Succeed())
		plan, accepted := purgeAccept(f)
		Expect(plan.Impact["managed_source_snapshot"]).To(Equal(1))
		managed := 0
		for _, item := range plan.Items {
			if item.Kind == "managed_source_snapshot" {
				managed++
			}
		}
		Expect(managed).To(Equal(1))
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			stored, e := tx.LifecyclePlan(plan.ID)
			if e == nil {
				Expect(stored.Digest).NotTo(BeEmpty())
				Expect(stored.Result).NotTo(BeNil())
				Expect(stored.Result.JobID).To(Equal(accepted.JobID))
			}
			return e
		})).To(Succeed())
		abandonPurgeUploads(f)
		clock := time.Now()
		purger := &work.Purger{Store: f.store, Bytes: f.store, Now: func() time.Time { return clock }}
		var job domain.Job
		for n := 0; n < 150; n++ {
			clock = clock.Add(time.Minute)
			Expect(purger.Process(ctx, accepted.JobID)).To(Succeed())
			var e error
			job, e = f.store.Job(ctx, accepted.JobID)
			Expect(e).NotTo(HaveOccurred())
			if job.State == "completed" {
				break
			}
		}
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			r, e := tx.SourceCaptureRead(reference.ID)
			if e != nil {
				return e
			}
			Expect(r.Hash).To(Equal(reference.Hash))
			Expect(r.Reason).To(Equal(reference.Reason), "unretained history was never a detached snapshot")
			r, e = tx.SourceCaptureRead(snapshot.ID)
			Expect(r.Reason).To(Equal("version_purged"))
			return e
		})).To(Succeed())
		_, e := os.Stat(filepath.Join(f.profileDir, "storage", "objects", snapshot.Hash))
		if independent {
			Expect(job.State).To(Equal("paused"))
			Expect(e).NotTo(HaveOccurred())
			Expect(core.ReadCaptured(ctx, f.token, standalone.ID, func(_ domain.SourceCaptureRead, r io.ReadSeeker) error {
				body, e := io.ReadAll(r)
				Expect(body).To(Equal([]byte("version snapshot exact")))
				return e
			})).To(Succeed())
		} else {
			Expect(job.State).To(Equal("completed"))
			Expect(os.IsNotExist(e)).To(BeTrue())
		}
	}
})
