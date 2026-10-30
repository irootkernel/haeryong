package sqlite

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type lookupCrashRequest struct {
	Selection service.Selection
	Job       string
}
type lookupCrashDriver struct{}

func (lookupCrashDriver) Execute(context.Context, domain.ProviderAttempt, []domain.ProviderInput, func() error, func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	return work.ProviderOutcome{}, errors.New("unexpected dispatch")
}
func (lookupCrashDriver) Cancel(context.Context, domain.ProviderAttempt) error { return nil }
func (lookupCrashDriver) Reconcile(context.Context, domain.ProviderAttempt, bool) (work.ProviderOutcome, error) {
	_, _ = os.Stdout.WriteString("lookup-claimed\n")
	control := os.NewFile(3, "parent-owned-barrier")
	defer control.Close()
	var b [1]byte
	_, _ = control.Read(b[:])
	return work.ProviderOutcome{}, errors.New("crash barrier unexpectedly released")
}
func init() {
	if os.Getenv("HAERYONG_PROVIDER_LOOKUP_CHILD") == "1" {
		runProviderLookupChild()
		os.Exit(0)
	}
}
func runProviderLookupChild() {
	if os.Getenv("HAERYONG_PROVIDER_LOOKUP_CHILD") != "1" {
		return
	}
	var request lookupCrashRequest
	if e := json.NewDecoder(os.Stdin).Decode(&request); e != nil {
		panic(e)
	}
	w, e := service.OpenWriter(request.Selection)
	if e != nil {
		panic(e)
	}
	defer w.Close()
	s := New(nil)
	_, closeDB, e := s.Prepare(context.Background(), w.Profile)
	if e != nil {
		panic(e)
	}
	defer closeDB()
	p := work.ProviderCoordinator{Store: s, Driver: lookupCrashDriver{}}
	for turn := 0; turn < 2; turn++ {
		if e = p.Process(context.Background(), request.Job); e != nil {
			panic(e)
		}
	}
	panic("lookup unexpectedly completed")
}

var _ = It("PA42 real process death during claimed status lookup reclaims pending work including terminal jobs without overlapping live admission", Label("persistence"), func() {
	for _, terminalJob := range []bool{false, true} {
		s, core, w, _ := authFixture(GinkgoT())
		pairOwner(GinkgoT(), core, "lookup-crash")
		selection := service.Selection{Profile: w.Profile.Name, DataDir: w.Profile.DataDir, VolumeRoot: w.Profile.VolumeRoot}
		a, e := providerEmptyJob(s)
		Expect(e).NotTo(HaveOccurred())
		a, e = providerPrepareEmpty(s, a)
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.DispatchProviderAttempt(a.ID); e != nil {
				return e
			}
			if e := tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "request", "session", "transport_unknown"); e != nil {
				return e
			}
			if terminalJob {
				return tx.FinishJob(a.JobID, a.Generation, "fixture_failure", nil)
			}
			return tx.PauseProviderJob(a.JobID, "outcome_unknown")
		})).To(Succeed())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			j, e := tx.Job(a.JobID)
			if e != nil {
				return e
			}
			_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "reconcile_only")
			if e != nil {
				return e
			}
			return tx.ClaimProviderReconciliation(j.ID, a.ID)
		})).To(Succeed())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			j, e := tx.Job(a.JobID)
			if e != nil {
				return e
			}
			_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "reconcile_only")
			return e
		})).To(MatchError(func() error {
			if terminalJob {
				return domain.ErrReconciliation
			}
			return domain.ErrTransition
		}()))
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.CompleteProviderReconciliation(a.JobID, a.ID); e != nil {
				return e
			}
			j, e := tx.Job(a.JobID)
			if e != nil {
				return e
			}
			if !terminalJob {
				if e = tx.PauseProviderJob(j.ID, "outcome_unknown"); e != nil {
					return e
				}
			}
			_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "reconcile_only")
			return e
		})).To(Succeed())
		Expect(s.Close()).To(Succeed())
		Expect(w.Close()).To(Succeed())
		controlRead, controlWrite, e := os.Pipe()
		Expect(e).NotTo(HaveOccurred())
		defer controlRead.Close()
		defer controlWrite.Close()
		command := exec.Command(os.Args[0], "-test.run=^TestSuite$", "-test.timeout=20s")
		command.Env = append(os.Environ(), "HAERYONG_PROVIDER_LOOKUP_CHILD=1")
		command.Stdin = bytes.NewReader(jsonBytes(lookupCrashRequest{selection, a.JobID}))
		command.ExtraFiles = []*os.File{controlRead}
		var diagnostics bytes.Buffer
		command.Stderr = &diagnostics
		stdout, e := command.StdoutPipe()
		Expect(e).NotTo(HaveOccurred())
		Expect(command.Start()).To(Succeed())
		defer func() {
			if command.ProcessState == nil {
				_ = command.Process.Kill()
				_ = command.Wait()
			}
		}()
		reached := make(chan string, 1)
		go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); reached <- line }()
		select {
		case line := <-reached:
			Expect(line).To(Equal("lookup-claimed\n"))
		case <-time.After(8 * time.Second):
			Fail("child never claimed external status lookup")
		}
		_, e = service.OpenWriter(selection)
		Expect(e).To(HaveOccurred(), "actual child owns the profile lock")
		Expect(command.Process.Kill()).To(Succeed())
		Expect(command.Wait()).To(HaveOccurred())
		Expect(command.ProcessState).NotTo(BeNil())
		w, e = service.OpenWriter(selection)
		Expect(e).NotTo(HaveOccurred())
		defer w.Close()
		s = New(nil)
		_, closeDB, e := s.Prepare(ctx, w.Profile)
		Expect(e).NotTo(HaveOccurred())
		defer closeDB()
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			id, e := tx.NextProviderReconciliation(a.JobID)
			Expect(id).To(Equal(a.ID))
			return e
		})).To(Succeed())
		d := &providerFake{reconciled: "cancelled"}
		p := work.ProviderCoordinator{Store: s, Driver: d}
		Expect(p.Process(ctx, a.JobID)).To(Succeed())
		if !terminalJob {
			normalized, e := s.Job(ctx, a.JobID)
			Expect(e).NotTo(HaveOccurred())
			Expect(normalized.State).To(Equal("reconciling"))
			Expect(d.lookups).To(BeZero())
			Expect(p.Process(ctx, a.JobID)).To(Succeed())
		}
		Expect(d.calls).To(BeZero())
		Expect(d.lookups).To(Equal(1))
		j, e := s.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.ProviderAttempts.Unknown).To(BeZero())
		if terminalJob {
			Expect(j.State).To(Equal("failed"))
		}
	}
})

func providerCreateEmptyJob(tx app.WriteTx) (domain.ProviderAttempt, error) {
	lib, e := tx.Library()
	if e != nil {
		return domain.ProviderAttempt{}, e
	}
	owner, e := tx.AuthState()
	if e != nil {
		return domain.ProviderAttempt{}, e
	}
	scope := domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: jsonBytes(map[string]string{"project_id": lib.UnfiledProjectID}), RequestSHA: strings.Repeat("a", 64)}
	a := domain.ProviderAttempt{ID: id(GinkgoT()), JobID: id(GinkgoT()), State: "prepared", Runtime: lib.RuntimeGeneration, RequestSHA: scope.RequestSHA}
	return a, tx.CreateJob(domain.Job{ID: a.JobID, Kind: "question", Scope: jsonBytes(scope), Permissions: map[string]int64{lib.UnfiledProjectID: 1}})
}
func providerEmptyJob(s *Store) (domain.ProviderAttempt, error) {
	var a domain.ProviderAttempt
	e := s.Write(ctx, func(tx app.WriteTx) error { var e error; a, e = providerCreateEmptyJob(tx); return e })
	return a, e
}
func providerStatusCarrier(s *Store, ids []string) (domain.ProviderAttempt, error) {
	var a domain.ProviderAttempt
	e := s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		a, e = providerCreateEmptyJob(tx)
		if e != nil {
			return e
		}
		return tx.AuthorizeProviderReconciliation(a.JobID, ids)
	})
	return a, e
}

func providerPrepareEmpty(s *Store, a domain.ProviderAttempt) (domain.ProviderAttempt, error) {
	e := s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.ClaimJob(a.JobID)
		if e != nil {
			return e
		}
		a.Generation = j.AttemptGeneration
		return tx.PrepareProviderAttempt(a, nil)
	})
	return a, e
}

type providerAfterSaveBytes struct {
	s     *Store
	after func() error
}

func (b providerAfterSaveBytes) Put(ctx context.Context, r io.Reader, commit func(app.WriteTx, domain.Object) error) (domain.Object, error) {
	o, e := (providerFixtureBytes{b.s}).Put(ctx, r, commit)
	if e == nil {
		e = b.after()
	}
	return o, e
}

var _ = It("PA39 result persistence barriers isolate cancellation reassignment and current authority before application", Label("persistence"), func() {
	for _, mode := range []string{"cancel", "reassign", "permission"} {
		s, a := providerFixture(false)
		driver := &providerFake{cancelled: make(chan struct{})}
		applied := 0
		var producing int64
		boundary := providerAfterSaveBytes{s, func() error {
			e := s.Write(ctx, func(tx app.WriteTx) error {
				j, e := tx.Job(a.JobID)
				if e != nil {
					return e
				}
				producing = j.AttemptGeneration
				rows, e := tx.ProviderAttempts(j.ID)
				if e != nil {
					return e
				}
				Expect(rows[0].State).To(Equal("result_saved"))
				switch mode {
				case "cancel":
					_, e = tx.CancelJob(j.ID)
					return e
				case "reassign":
					if e = tx.PauseProviderJob(j.ID, "fixture_pause"); e != nil {
						return e
					}
					if _, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "retry"); e != nil {
						return e
					}
					_, e = tx.ClaimJob(j.ID)
					return e
				default:
					lib, e := tx.Library()
					if e != nil {
						return e
					}
					p, e := tx.Scope(lib.UnfiledProjectID)
					if e != nil {
						return e
					}
					return tx.ApplyProjectConfiguration(p.Project.ID, "", "", true, p.Project.PermissionsRevision)
				}
			})
			if e != nil {
				return e
			}
			if mode == "cancel" {
				select {
				case <-driver.cancelled:
				case <-time.After(3 * time.Second):
					return errors.New("actual Driver.Cancel was not called")
				}
			}
			return nil
		}}
		p := work.ProviderCoordinator{Store: s, Driver: driver, Bytes: boundary, Apply: func(app.WriteTx, domain.ProviderAttempt) error { applied++; return nil }}
		e := p.Process(ctx, a.JobID)
		if mode == "permission" {
			Expect(e).NotTo(HaveOccurred())
			Expect(p.StopProviders()).To(Succeed())
		} else if mode == "reassign" {
			Expect(e).To(MatchError(domain.ErrStaleWorker))
		} else {
			Expect(e).NotTo(HaveOccurred())
			Expect(driver.cancels.Load()).To(BeNumerically(">", 0))
		}
		Expect(applied).To(BeZero())
		j, e := s.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		if mode != "permission" {
			Expect(j.AttemptGeneration).To(BeNumerically(">", producing))
		}
		if mode == "reassign" {
			Expect(j.State).To(Equal("running"))
			Expect(j.CancelRequested).To(BeFalse())
		}
	}
})

var _ = It("PA40 identical and unrelated configuration commits preserve captured work while current project restrictions still fence saved results", Label("persistence"), func() {
	s, a := providerFixture()
	for _, unrelated := range []bool{false, true} {
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			c, e := tx.Configuration()
			if e != nil {
				return e
			}
			if unrelated {
				var v map[string]any
				if e = json.Unmarshal(c.Value, &v); e != nil {
					return e
				}
				v["security"].(map[string]any)["remember_device_default"] = false
				c.Value = jsonBytes(v)
			}
			return tx.SetConfiguration(c, c.Revision)
		})).To(Succeed())
	}
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.DispatchProviderAttempt(a.ID) })).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		result := domain.Object{Hash: strings.Repeat("e", 64), Size: 1, State: "ready"}
		if e := tx.PutDurableObject(result); e != nil {
			return e
		}
		return tx.SaveProviderResult(a.ID, result)
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		p, e := tx.Scope(lib.UnfiledProjectID)
		if e != nil {
			return e
		}
		return tx.ApplyProjectConfiguration(p.Project.ID, "", "", true, p.Project.PermissionsRevision)
	})).To(Succeed())
	called := false
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.ApplyProviderResult(a.ID, func(app.WriteTx) error { called = true; return nil })
	})).To(MatchError(domain.ErrDenied))
	Expect(called).To(BeFalse())
})

var _ = It("PA41 durable missing input pauses without monopolizing the provider slot and nil transport never dispatches", Label("persistence"), func() {
	s, a := providerFixture(false)
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	var scope domain.ProviderScope
	Expect(json.Unmarshal(j.Scope, &scope)).To(Succeed())
	scope.ReadIDs = []string{"missing-read"}
	_, e = s.db.Exec(`UPDATE jobs SET scope=? WHERE job_id=?`, jsonBytes(scope), j.ID)
	Expect(e).NotTo(HaveOccurred())
	d := &providerFake{}
	p := work.ProviderCoordinator{Store: s, Driver: d}
	Expect(p.Process(ctx, a.JobID)).To(MatchError(domain.ErrNotFound))
	j, e = s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(j.Error).To(Equal("captured_input_unavailable"))
	Expect(d.calls).To(BeZero())
	other, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	nilProvider := work.ProviderCoordinator{Store: s}
	Expect(nilProvider.Process(ctx, other.JobID)).To(Succeed())
	j, e = s.Job(ctx, other.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.Error).To(Equal("provider_unavailable"))
	next, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	p.Bytes = providerFixtureBytes{s}
	Expect(p.Process(ctx, next.JobID)).To(Succeed())
	Expect(d.calls).To(Equal(1))
	progress, e := s.Job(ctx, next.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(progress.State).To(Equal("completed"))
})

var _ = It("PA43 restored terminal lookups wait for global owner review and explicit job release then the dispatcher collects status-only reconciliation", Label("persistence"), func() {
	s, core, _, _ := authFixture(GinkgoT())
	pairOwner(GinkgoT(), core, "held-status")
	a, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	a, e = providerPrepareEmpty(s, a)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		if e := tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "request", "session", "transport_unknown"); e != nil {
			return e
		}
		if e := tx.FinishJob(a.JobID, a.Generation, "fixture_failure", nil); e != nil {
			return e
		}
		_, e := tx.ResumeProviderJob(a.JobID, a.Generation, "reconcile_only")
		return e
	})).To(Succeed())
	path := filepath.Join(GinkgoT().TempDir(), "held.sqlite")
	Expect(s.SnapshotDatabase(ctx, path)).To(Succeed())
	hold := id(GinkgoT())
	_, e = ProtectRestoredDatabase(ctx, path, s.profile, hold, CopyCurrentConfiguration(ctx, path), configuration.Validate)
	Expect(e).NotTo(HaveOccurred())
	db, e := openCopy(path)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	restored := New(nil)
	restored.db = db
	d := &providerFake{reconciled: "cancelled"}
	p := work.ProviderCoordinator{Store: restored, Driver: d}
	Expect(p.Process(ctx, a.JobID)).To(MatchError(domain.ErrReconciliation))
	Expect(d.lookups).To(BeZero())
	Expect(restored.Write(ctx, func(tx app.WriteTx) error { return tx.ReviewRecovery(hold, nil) })).To(Succeed())
	Expect(p.Process(ctx, a.JobID)).To(MatchError(domain.ErrReconciliation))
	Expect(d.lookups).To(BeZero())
	Expect(restored.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "reconcile_only")
		return e
	})).To(Succeed())
	// Unsupported/nil lookup safely preserves uncertainty and consumes no Execute.
	nilProvider := work.ProviderCoordinator{Store: restored}
	Expect(nilProvider.Process(ctx, a.JobID)).To(Succeed())
	j, e := restored.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.ProviderAttempts.Unknown).To(Equal(1))
	Expect(j.State).To(Equal("failed"))
	Expect(restored.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "reconcile_only")
		return e
	})).To(Succeed())
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (work.Dispatcher{Store: restored, Provider: &p}).Run(runCtx) }()
	Eventually(func() int {
		job, e := restored.Job(ctx, a.JobID)
		if e != nil {
			return -1
		}
		return job.ProviderAttempts.ReconciliationPending
	}, 5*time.Second).Should(BeZero())
	stop()
	Expect(<-done).To(Succeed())
	Expect(d.lookups).To(Equal(1))
	Expect(d.calls).To(BeZero())
	Expect(d.outputAllowed).To(BeFalse())
	j, e = restored.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("failed"))
	Expect(j.ProviderAttempts.Unknown).To(BeZero())
})

var _ = It("PA36 protected restore before the original job preserves orphan-only barriers and permits explicit finite status and retry admission", Label("persistence"), func() {
	s, core, _, _ := authFixture(GinkgoT())
	pairOwner(GinkgoT(), core, "orphan-only")
	older := filepath.Join(GinkgoT().TempDir(), "before-job.sqlite")
	Expect(s.SnapshotDatabase(ctx, older)).To(Succeed())
	a, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	a, e = providerPrepareEmpty(s, a)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		return tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "actual-request", "actual-session", "transport_unknown")
	})).To(Succeed())
	var inventory bytes.Buffer
	Expect(RestoreOutstanding(ctx, s.profile.DataDir+"/storage/library.sqlite", func(raw json.RawMessage) error { _, e := inventory.Write(append(raw, '\n')); return e })).To(Succeed())
	hold := id(GinkgoT())
	_, e = ProtectRestoredDatabase(ctx, older, s.profile, hold, CopyCurrentConfiguration(ctx, older), configuration.Validate)
	Expect(e).NotTo(HaveOccurred())
	Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(inventory.Bytes()), nil)).To(Succeed())
	db, e := openCopy(older)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	restored := New(nil)
	restored.db = db
	Expect(restored.Write(ctx, func(tx app.WriteTx) error { return tx.ReviewRecovery(hold, nil) })).To(Succeed())
	var original int
	Expect(db.QueryRow(`SELECT count(*) FROM jobs WHERE job_id=?`, a.JobID).Scan(&original)).To(Succeed())
	Expect(original).To(BeZero())
	newWork, e := providerEmptyJob(restored)
	Expect(e).NotTo(HaveOccurred())
	_, e = providerPrepareEmpty(restored, newWork)
	Expect(e).To(MatchError(domain.ErrReconciliation), "no ordinary same-SHA attempt exists; only the retained orphan blocks this")
	Expect(restored.Write(ctx, func(tx app.WriteTx) error { return tx.AuthorizeProviderRetry(newWork.JobID, []string{a.ID}) })).To(Succeed(), "explicit finite owner linkage works without fabricating a missing parent job")
	newWork, e = providerPrepareEmpty(restored, newWork)
	Expect(e).NotTo(HaveOccurred())
	// A separate status carrier grants no retry or Execute authority.
	carrier, e := providerStatusCarrier(restored, []string{a.ID})
	Expect(e).NotTo(HaveOccurred())
	driver := &providerFake{reconciled: "cancelled"}
	p := work.ProviderCoordinator{Store: restored, Driver: driver}
	Expect(p.Process(ctx, carrier.JobID)).To(Succeed())
	Expect(driver.calls).To(BeZero())
	Expect(driver.lookups).To(Equal(1))
	Expect(driver.outputAllowed).To(BeFalse())
	var state, request, originalJob string
	Expect(db.QueryRow(`SELECT state,json_extract(value,'$.provider_request_id'),json_extract(value,'$.job_id') FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&state, &request, &originalJob)).To(Succeed())
	Expect(state).To(Equal("cancelled"))
	Expect(request).To(Equal("actual-request"))
	Expect(originalJob).To(Equal(a.JobID))
	j, e := restored.Job(ctx, carrier.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.ProviderAttempts.Unknown).To(BeZero())
	Expect(j.ProviderAttempts.ReconciliationPending).To(BeZero())
	Expect(CheckCopyDatabase(ctx, older)).To(Succeed())
})

var _ = It("PA37 cancel after a prepared snapshot restore cannot erase same-ID dispatch uncertainty or its identifiers on another restore", Label("persistence"), func() {
	s, a := providerFixture()
	older := filepath.Join(GinkgoT().TempDir(), "prepared.sqlite")
	Expect(s.SnapshotDatabase(ctx, older)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		return tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "native-request", "native-session", "transport_unknown")
	})).To(Succeed())
	var first bytes.Buffer
	Expect(RestoreOutstanding(ctx, s.profile.DataDir+"/storage/library.sqlite", func(raw json.RawMessage) error { _, e := first.Write(append(raw, '\n')); return e })).To(Succeed())
	available := providerFixtureCopiedAvailability(older)
	Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(first.Bytes()), available)).To(Succeed())
	db, e := openCopy(older)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	restored := New(nil)
	restored.db = db
	Expect(restored.Write(ctx, func(tx app.WriteTx) error { _, e := tx.CancelJob(a.JobID); return e })).To(Succeed())
	j, e := restored.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(j.CancelRequested).To(BeTrue())
	Expect(j.AttemptGeneration).To(BeNumerically(">", a.Generation))
	var next bytes.Buffer
	Expect(RestoreOutstanding(ctx, older, func(raw json.RawMessage) error { _, e := next.Write(append(raw, '\n')); return e })).To(Succeed())
	Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(next.Bytes()), available)).To(Succeed())
	Expect(restored.Write(ctx, func(tx app.WriteTx) error {
		v, e := tx.ProviderAttempt(a.ID)
		if e != nil {
			return e
		}
		Expect(v.State).To(Equal("outcome_unknown"))
		Expect(v.RequestID).To(Equal("native-request"))
		Expect(v.SessionID).To(Equal("native-session"))
		_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "reconcile_only")
		return e
	})).To(Succeed())
	d := &providerFake{reconciled: "cancelled"}
	p := work.ProviderCoordinator{Store: restored, Driver: d}
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	j, e = restored.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("cancelled"))
	Expect(j.ProviderAttempts.Unknown).To(BeZero())
	Expect(d.calls).To(BeZero())
	var remainingInputs int
	Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_orphan' AND owner_id=? AND pin_id NOT LIKE '%:result:%'`, a.ID).Scan(&remainingInputs)).To(Succeed())
	Expect(remainingInputs).To(BeZero(), "the real source job's terminal transition releases its settled orphan execution inputs too")

})

var _ = It("PA38 orphan merge preserves independent cancellation terminal identity conflicts and every available result owner", Label("persistence"), func() {
	s, a := providerFixture()
	path := s.profile.DataDir + "/storage/library.sqlite"
	for _, hash := range []string{strings.Repeat("c", 64), strings.Repeat("d", 64)} {
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			return tx.PutDurableObject(domain.Object{Hash: hash, Size: 1, State: "ready"})
		})).To(Succeed())
	}
	retain := func(state, request, hash, jobState string, cancel bool) {
		raw := jsonBytes(map[string]any{"attempt_id": a.ID, "job_id": a.JobID, "request_sha256": a.RequestSHA, "state": state, "provider_request_id": request, "result_hash": hash, "job_state": jobState, "cancel_requested": cancel})
		Expect(RetainProviderOrphans(ctx, path, bytes.NewReader(raw), func(domain.Object) (bool, error) { return true, nil })).To(Succeed())
	}
	retain("result_saved", "request-one", strings.Repeat("c", 64), "paused", false)
	retain("result_saved", "request-two", strings.Repeat("d", 64), "cancelled", true)
	var raw []byte
	Expect(s.db.QueryRow(`SELECT value FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&raw)).To(Succeed())
	var fact providerRestoreFact
	Expect(json.Unmarshal(raw, &fact)).To(Succeed())
	Expect(fact.State).To(Equal("outcome_unknown"))
	Expect(fact.CancelRequested).To(BeTrue())
	Expect(fact.JobState).To(Equal("cancelled"))
	Expect(string(raw)).To(ContainSubstring("request-one"))
	Expect(string(raw)).To(ContainSubstring("request-two"))
	var pins int
	Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_orphan' AND owner_id=? AND content_hash IN(?,?)`, a.ID, strings.Repeat("c", 64), strings.Repeat("d", 64)).Scan(&pins)).To(Succeed())
	Expect(pins).To(Equal(2))
})

var _ = It("PA47 already settled or settled before lookup status-only carriers never become dispatchable on repeated dispatcher turns", Label("persistence"), func() {
	s, a := providerFixture()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		if e := tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "request", "session", "transport_unknown"); e != nil {
			return e
		}
		return tx.PauseProviderJob(a.JobID, "outcome_unknown")
	})).To(Succeed())
	var inventory bytes.Buffer
	Expect(RestoreOutstanding(ctx, s.profile.DataDir+"/storage/library.sqlite", func(raw json.RawMessage) error { _, e := inventory.Write(append(raw, '\n')); return e })).To(Succeed())
	Expect(RetainProviderOrphans(ctx, s.profile.DataDir+"/storage/library.sqlite", bytes.NewReader(inventory.Bytes()), nil)).To(Succeed())
	for _, settledAtAdmission := range []bool{false, true} {
		carrier, e := providerStatusCarrier(s, []string{a.ID})
		Expect(e).NotTo(HaveOccurred())
		if !settledAtAdmission {
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				return tx.ObserveProviderAttempt(a.ID, "cancelled", "", "", "cancel_confirmed")
			})).To(Succeed())
		}
		d := &providerFake{}
		p := work.ProviderCoordinator{Store: s, Driver: d, Bytes: providerFixtureBytes{s}}
		runCtx, stop := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- (work.Dispatcher{Store: s, Provider: &p}).Run(runCtx) }()
		Eventually(func() string {
			j, e := s.Job(ctx, carrier.JobID)
			if e != nil {
				return "error"
			}
			return j.State
		}, 5*time.Second).Should(Equal("completed"))
		Consistently(func() string {
			j, e := s.Job(ctx, carrier.JobID)
			if e != nil {
				return "error"
			}
			return j.State
		}, 250*time.Millisecond, 20*time.Millisecond).Should(Equal("completed"))
		stop()
		Expect(<-done).To(Succeed())
		Expect(d.calls).To(BeZero())
		Expect(d.lookups).To(BeZero())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			j, e := tx.Job(carrier.JobID)
			if e != nil {
				return e
			}
			_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "continue")
			return e
		})).To(MatchError(domain.ErrTransition))
	}
})

var _ = It("PA49 same-ID live continuation keeps input ownership until its own explicit terminal transition", Label("persistence"), func() {
	s, a := providerFixture()
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.DispatchProviderAttempt(a.ID) })).To(Succeed())
	var input domain.ProviderInput
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.ProviderInputs(a.ID)
		if e == nil {
			input = rows[0]
		}
		return e
	})).To(Succeed())
	var inventory bytes.Buffer
	Expect(RestoreOutstanding(ctx, s.profile.DataDir+"/storage/library.sqlite", func(raw json.RawMessage) error { _, e := inventory.Write(append(raw, '\n')); return e })).To(Succeed())
	Expect(RetainProviderOrphans(ctx, s.profile.DataDir+"/storage/library.sqlite", bytes.NewReader(inventory.Bytes()), func(domain.Object) (bool, error) { return true, nil })).To(Succeed())
	carrier, e := providerStatusCarrier(s, []string{a.ID})
	Expect(e).NotTo(HaveOccurred())
	d := &providerFake{reconciled: "cancelled"}
	p := work.ProviderCoordinator{Store: s, Driver: d}
	Expect(p.Process(ctx, carrier.JobID)).To(Succeed())
	j, e := s.Job(ctx, carrier.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	original, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(original.State).To(Equal("running"))
	Expect(original.CancelRequested).To(BeFalse())
	var pins int
	Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE content_hash=? AND owner_kind IN ('provider_attempt','provider_orphan')`, input.Hash).Scan(&pins)).To(Succeed())
	Expect(pins).To(Equal(2))
	Expect(s.Write(ctx, func(tx app.WriteTx) error { _, e := tx.ReserveGC(input.Hash, "live-continuation"); return e })).To(MatchError(domain.ErrPinned))
	Expect(s.Write(ctx, func(tx app.WriteTx) error { _, e := tx.CancelJob(a.JobID); return e })).To(Succeed())
	original, e = s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(original.State).To(Equal("cancelled"))
	Expect(original.CancelRequested).To(BeTrue())
	Expect(d.calls).To(BeZero())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		reservation, e := tx.ReserveGC(input.Hash, "settled-input")
		if e != nil {
			return e
		}
		return tx.FinishGC(reservation)
	})).To(Succeed())
})

var _ = It("PA50 local prepared failures cannot settle dispatched uncertainty while a correlated backend failure can", Label("persistence"), func() {
	for _, reason := range []string{"provider_unavailable", "auth_required", "quota_exhausted", "provider_failed"} {
		s, a := providerFixture()
		older, local := filepath.Join(GinkgoT().TempDir(), "external.sqlite"), filepath.Join(GinkgoT().TempDir(), "local.sqlite")
		Expect(s.SnapshotDatabase(ctx, older)).To(Succeed())
		Expect(s.SnapshotDatabase(ctx, local)).To(Succeed())
		orderedA, orderedB := filepath.Join(GinkgoT().TempDir(), "ordered-a.sqlite"), filepath.Join(GinkgoT().TempDir(), "ordered-b.sqlite")
		Expect(s.SnapshotDatabase(ctx, orderedA)).To(Succeed())
		Expect(s.SnapshotDatabase(ctx, orderedB)).To(Succeed())

		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.DispatchProviderAttempt(a.ID); e != nil {
				return e
			}
			return tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "actual-request", "actual-session", "transport_unknown")
		})).To(Succeed())
		ledgerOnly := filepath.Join(GinkgoT().TempDir(), "ledger-only.sqlite")
		Expect(s.SnapshotDatabase(ctx, ledgerOnly)).To(Succeed())
		var observed bytes.Buffer
		Expect(RestoreOutstanding(ctx, s.profile.DataDir+"/storage/library.sqlite", func(raw json.RawMessage) error { _, e := observed.Write(append(raw, '\n')); return e })).To(Succeed())
		Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(observed.Bytes()), providerFixtureCopiedAvailability(older))).To(Succeed())
		localDB, e := openCopy(local)
		Expect(e).NotTo(HaveOccurred())
		localStore := New(nil)
		localStore.db = localDB
		Expect(localStore.Write(ctx, func(tx app.WriteTx) error { return tx.ObserveProviderAttempt(a.ID, "failed", "", "", reason) })).To(Succeed())
		var localMay bool
		Expect(localDB.QueryRow(`SELECT may_have_dispatched FROM provider_attempts WHERE attempt_id=?`, a.ID).Scan(&localMay)).To(Succeed())
		Expect(localMay).To(BeFalse())
		Expect(localDB.Close()).To(Succeed())
		var failed bytes.Buffer
		Expect(RestoreOutstanding(ctx, local, func(raw json.RawMessage) error { _, e := failed.Write(append(raw, '\n')); return e })).To(Succeed())
		Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(failed.Bytes()), providerFixtureCopiedAvailability(older))).To(Succeed())
		Expect(RetainProviderOrphans(ctx, ledgerOnly, bytes.NewReader(failed.Bytes()), providerFixtureCopiedAvailability(ledgerOnly))).To(Succeed())
		probe, e := openCopy(ledgerOnly)
		Expect(e).NotTo(HaveOccurred())
		var effective string
		Expect(probe.QueryRow(`SELECT state FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&effective)).To(Succeed())
		Expect(effective).To(Equal("outcome_unknown"))
		Expect(probe.Close()).To(Succeed())
		legacy := jsonBytes(map[string]any{"attempt_id": "legacy-" + reason, "request_sha256": a.RequestSHA, "state": "failed", "reason": reason})
		Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(legacy), nil)).To(Succeed())
		probe, e = openCopy(older)
		Expect(e).NotTo(HaveOccurred())
		var marker, unverified any
		Expect(probe.QueryRow(`SELECT state,json_extract(value,'$.may_have_dispatched'),json_extract(value,'$.unverified_terminal_fact.state') FROM provider_orphans WHERE attempt_id=?`, "legacy-"+reason).Scan(&effective, &marker, &unverified)).To(Succeed())
		Expect(effective).To(Equal("outcome_unknown"))
		Expect(marker).To(BeNil())
		Expect(unverified).To(Equal("failed"))
		Expect(probe.Close()).To(Succeed())

		db, e := openCopy(older)
		Expect(e).NotTo(HaveOccurred())
		var state, request string
		Expect(db.QueryRow(`SELECT state,json_extract(value,'$.provider_request_id') FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&state, &request)).To(Succeed())
		Expect(state).To(Equal("outcome_unknown"))
		Expect(request).To(Equal("actual-request"))
		Expect(db.Close()).To(Succeed())

		for index, path := range []string{orderedA, orderedB} {
			combined := append(append([]byte(nil), observed.Bytes()...), failed.Bytes()...)
			if index == 1 {
				combined = append(append([]byte(nil), failed.Bytes()...), observed.Bytes()...)
			}
			Expect(RetainProviderOrphans(ctx, path, bytes.NewReader(combined), providerFixtureCopiedAvailability(path))).To(Succeed())
			var exported bytes.Buffer
			Expect(RestoreOutstanding(ctx, path, func(raw json.RawMessage) error { _, e := exported.Write(append(raw, '\n')); return e })).To(Succeed())
			Expect(RetainProviderOrphans(ctx, path, bytes.NewReader(exported.Bytes()), providerFixtureCopiedAvailability(path))).To(Succeed())
			check, e := openCopy(path)
			Expect(e).NotTo(HaveOccurred())
			var state string
			var may bool
			Expect(check.QueryRow(`SELECT state,may_have_dispatched FROM provider_attempts WHERE attempt_id=?`, a.ID).Scan(&state, &may)).To(Succeed())
			Expect(state).To(Equal("outcome_unknown"))
			Expect(may).To(BeTrue())
			Expect(check.Close()).To(Succeed())
		}
		// Reverse lineage: the older DB itself locally failed before learning the
		// real dispatch. Re-export must not reinterpret that local failure as remote.
		Expect(RetainProviderOrphans(ctx, local, bytes.NewReader(observed.Bytes()), providerFixtureCopiedAvailability(local))).To(Succeed())
		var again bytes.Buffer
		Expect(RestoreOutstanding(ctx, local, func(raw json.RawMessage) error { _, e := again.Write(append(raw, '\n')); return e })).To(Succeed())
		Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(again.Bytes()), providerFixtureCopiedAvailability(older))).To(Succeed())
		probe, e = openCopy(older)
		Expect(e).NotTo(HaveOccurred())
		Expect(probe.QueryRow(`SELECT state FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&effective)).To(Succeed())
		Expect(effective).To(Equal("outcome_unknown"))
		Expect(probe.Close()).To(Succeed())
		// This failure follows the actual logical boundary and names its observed request.
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			return tx.ObserveProviderAttempt(a.ID, "failed", "actual-request", "actual-session", "provider_failed")
		})).To(Succeed())
		observed.Reset()
		Expect(RestoreOutstanding(ctx, s.profile.DataDir+"/storage/library.sqlite", func(raw json.RawMessage) error { _, e := observed.Write(append(raw, '\n')); return e })).To(Succeed())
		Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(observed.Bytes()), providerFixtureCopiedAvailability(older))).To(Succeed())
		db, e = openCopy(older)
		Expect(e).NotTo(HaveOccurred())
		Expect(db.QueryRow(`SELECT state FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&state)).To(Succeed())
		Expect(state).To(Equal("failed"))
		Expect(db.Close()).To(Succeed())
	}
})

var _ = It("PA51 status admission rejects existing semantic attempts results and retry authority while a fresh carrier commits atomically", Label("persistence"), func() {
	s, core, _, _ := authFixture(GinkgoT())
	pairOwner(GinkgoT(), core, "fresh-status-only")
	a, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	a, e = providerPrepareEmpty(s, a)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		if e := tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "semantic-request", "session", "transport_unknown"); e != nil {
			return e
		}
		if e := tx.PauseProviderJob(a.JobID, "outcome_unknown"); e != nil {
			return e
		}
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "retry")
		return e
	})).To(Succeed())
	var principal string
	var target json.RawMessage
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		var scope domain.ProviderScope
		if e = json.Unmarshal(j.Scope, &scope); e != nil {
			return e
		}
		principal, target = scope.PrincipalID, scope.Target
		return nil
	})).To(Succeed())
	raw := jsonBytes(map[string]any{"attempt_id": "other-orphan", "job_id": "absent-source-job", "principal_id": principal, "target": target, "request_sha256": a.RequestSHA, "state": "outcome_unknown", "provider_request_id": "other-request"})
	Expect(RetainProviderOrphans(ctx, s.profile.DataDir+"/storage/library.sqlite", bytes.NewReader(raw), nil)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.AuthorizeProviderReconciliation(a.JobID, []string{"other-orphan"})
	})).To(MatchError(domain.ErrTransition))
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("queued"))
	Expect(j.ProviderAttempts.Unknown).To(Equal(1))
	resultJob, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.SetJobResult(resultJob.JobID, jsonBytes(map[string]string{"semantic_result": "already-owned"}))
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.AuthorizeProviderReconciliation(resultJob.JobID, []string{"other-orphan"})
	})).To(MatchError(domain.ErrTransition))
	linked, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.AuthorizeProviderRetry(linked.JobID, []string{"other-orphan"}) })).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.AuthorizeProviderReconciliation(linked.JobID, []string{"other-orphan"})
	})).To(MatchError(domain.ErrTransition))
	carrier, e := providerStatusCarrier(s, []string{"other-orphan"})
	Expect(e).NotTo(HaveOccurred())
	j, e = s.Job(ctx, carrier.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("reconciling"))
	Expect(j.ProviderAttempts.ReconciliationPending).To(Equal(1))
	Expect(j.Result).To(BeEmpty())
})

var _ = It("PA20 acknowledged acceptance identifiers survive loss of the entire Execute result", Label("persistence"), func() {
	s, a := providerFixture(false)
	profile := s.profile
	var firstObserved string
	d := &providerFake{afterProgress: func() error {
		if e := s.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.ProviderAttempts(a.JobID)
			if e != nil {
				return e
			}
			inputs, e := tx.ProviderInputs(rows[0].ID)
			if e == nil {
				Expect(inputs[0].ObservedAt).NotTo(BeNil())
				firstObserved = *inputs[0].ObservedAt
			}
			return e
		}); e != nil {
			return e
		}
		if e := s.Close(); e != nil {
			return e
		}
		return context.Canceled
	}}
	p := work.ProviderCoordinator{Store: s, Driver: d, Bytes: providerFixtureBytes{s}}
	Expect(p.Process(ctx, a.JobID)).To(HaveOccurred())
	s = New(nil)
	_, closeDB, e := s.Prepare(ctx, profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeDB()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		attempts, e := tx.ProviderAttempts(a.JobID)
		if e != nil {
			return e
		}
		Expect(attempts).To(HaveLen(1))
		Expect(attempts[0].RequestID).To(Equal("observed-request"))
		Expect(attempts[0].SessionID).To(Equal("observed-session"))
		Expect(attempts[0].State).To(Equal("outcome_unknown"))
		inputs, e := tx.ProviderInputs(attempts[0].ID)
		if e != nil {
			return e
		}
		Expect(inputs[0].ObservedOrigin).To(Equal("daemon_supplied"))
		Expect(inputs[0].ObservedAt).To(Equal(&firstObserved))
		return nil
	})).To(Succeed())
})

type providerBrokenBytes struct{ failure error }

func (b providerBrokenBytes) Put(context.Context, io.Reader, func(app.WriteTx, domain.Object) error) (domain.Object, error) {
	return domain.Object{}, b.failure
}

var _ = It("PA21 output persistence failure retains accepted identities and pauses without implicit retry", Label("persistence"), func() {
	s, a := providerFixture(false)
	failure := errors.New("fixture result persistence failure")
	d := &providerFake{}
	p := work.ProviderCoordinator{Store: s, Driver: d, Bytes: providerBrokenBytes{failure}}
	Expect(p.Process(ctx, a.JobID)).To(MatchError(failure))
	Expect(d.calls).To(Equal(1))
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		v, e := tx.ProviderAttempts(a.JobID)
		if e != nil {
			return e
		}
		Expect(v[0].State).To(Equal("outcome_unknown"))
		Expect(v[0].Reason).To(Equal("result_retrieval_pending"))
		Expect(v[0].RequestID).To(Equal("observed-request"))
		Expect(v[0].ResultHash).To(BeEmpty())
		_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "continue")
		return e
	})).To(MatchError(domain.ErrReconciliation))
	Expect(p.Process(ctx, a.JobID)).To(MatchError(domain.ErrTransition))
	Expect(d.calls).To(Equal(1))
})

var _ = It("PA22 terminal retry output cannot release inputs while an earlier attempt remains uncertain", Label("persistence"), func() {
	s, a := providerFixture()
	var current domain.ProviderAttempt
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		if e := tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "old", "session", "transport_unknown"); e != nil {
			return e
		}
		if e := tx.PauseProviderJob(a.JobID, "outcome_unknown"); e != nil {
			return e
		}
		_, e := tx.ResumeProviderJob(a.JobID, a.Generation, "retry")
		if e != nil {
			return e
		}
		j, e := tx.ClaimJob(a.JobID)
		if e != nil {
			return e
		}
		current = a
		current.ID = id(GinkgoT())
		current.Generation = j.AttemptGeneration
		inputs, e := tx.ProviderInputs(a.ID)
		if e != nil {
			return e
		}
		for i := range inputs {
			inputs[i].AttemptID = current.ID
		}
		if e = tx.PrepareProviderAttempt(current, inputs); e != nil {
			return e
		}
		if e = tx.DispatchProviderAttempt(current.ID); e != nil {
			return e
		}
		if e = tx.ObserveProviderAttempt(current.ID, "outcome_unknown", "older-second", "session", "transport_unknown"); e != nil {
			return e
		}
		if e = tx.PauseProviderJob(current.JobID, "outcome_unknown"); e != nil {
			return e
		}
		if _, e = tx.ResumeProviderJob(current.JobID, current.Generation, "retry"); e != nil {
			return e
		}
		j, e = tx.ClaimJob(current.JobID)
		if e != nil {
			return e
		}
		current.ID = id(GinkgoT())
		current.Generation = j.AttemptGeneration
		for index := range inputs {
			inputs[index].AttemptID = current.ID
		}
		if e = tx.PrepareProviderAttempt(current, inputs); e != nil {
			return e
		}
		if e = tx.DispatchProviderAttempt(current.ID); e != nil {
			return e
		}
		o := domain.Object{Hash: digest([]byte("new output")), Size: 10, State: "ready"}
		if e = tx.PutDurableObject(o); e != nil {
			return e
		}
		if e = tx.SaveProviderResult(current.ID, o); e != nil {
			return e
		}
		return tx.ApplyProviderResult(current.ID, nil)
	})).To(Succeed())
	var pins int
	Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_attempt' AND pin_id IN (SELECT a.attempt_id||':'||i.read_id FROM provider_inputs i JOIN provider_attempts a ON a.attempt_id=i.attempt_id WHERE a.job_id=?)`, a.JobID).Scan(&pins)).To(Succeed())
	Expect(pins).To(Equal(3))
	before, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(before.State).To(Equal("completed"))
	Expect(before.ProviderAttempts.Count).To(Equal(3))
	Expect(before.ProviderAttempts.Unknown).To(Equal(2))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.ResumeProviderJob(before.ID, before.AttemptGeneration, "reconcile_only")
		if e != nil {
			return e
		}
		jobs, e := tx.WorkJobs(64)
		if e != nil {
			return e
		}
		Expect(jobs).To(ContainElement(HaveField("ID", before.ID)))
		return nil
	})).To(Succeed())
	d := &providerFake{reconciled: "cancelled"}
	p := work.ProviderCoordinator{Store: s, Driver: d, Bytes: providerFixtureBytes{s}}
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	first, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(first.ProviderAttempts.Unknown).To(Equal(1))
	Expect(first.ProviderAttempts.ReconciliationPending).To(Equal(1))
	Expect(first.Result).To(Equal(before.Result))
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	Expect(d.calls).To(BeZero())
	Expect(d.lookups).To(Equal(2))
	Expect(d.outputAllowed).To(BeFalse())
	after, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.State).To(Equal(before.State))
	Expect(after.Result).To(Equal(before.Result))
	Expect(after.AttemptGeneration).To(Equal(before.AttemptGeneration))
	Expect(after.ProviderAttempts.Unknown).To(BeZero())
	Expect(after.ProviderAttempts.ReconciliationPending).To(BeZero())
	Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_attempt' AND pin_id IN (SELECT a.attempt_id||':'||i.read_id FROM provider_inputs i JOIN provider_attempts a ON a.attempt_id=i.attempt_id WHERE a.job_id=?)`, a.JobID).Scan(&pins)).To(Succeed())
	Expect(pins).To(BeZero())
})

type providerCountedBody struct{ reads, closed int }

func (b *providerCountedBody) Read([]byte) (int, error) { b.reads++; return 0, io.EOF }
func (b *providerCountedBody) Close() error             { b.closed++; return nil }

var _ = It("PA25 hidden-target reconciliation requests status only and closes an unexpected output without reading", Label("persistence"), func() {
	s, a := providerFixture(false)
	document := id(GinkgoT())
	var capturedScope []byte
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		var scope domain.ProviderScope
		if e = json.Unmarshal(j.Scope, &scope); e != nil {
			return e
		}
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		if e = tx.CreateDocument(domain.Document{ID: document, ProjectID: lib.UnfiledProjectID, Title: "private"}); e != nil {
			return e
		}
		scope.Target = jsonBytes(map[string]string{"document_id": document, "project_id": lib.UnfiledProjectID})
		capturedScope = jsonBytes(scope)
		return nil
	})).To(Succeed())
	_, e := s.db.Exec(`UPDATE jobs SET scope=? WHERE job_id=?`, capturedScope, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	d := &providerFake{failure: true}
	p := work.ProviderCoordinator{Store: s, Driver: d, Bytes: providerFixtureBytes{s}}
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.TestTrashDocument(document); e != nil {
			return e
		}
		j, e := tx.CancelJob(a.JobID)
		if e != nil {
			return e
		}
		_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "reconcile_only")
		return e
	})).To(Succeed())
	body := &providerCountedBody{}
	d.reconciled = "result_saved"
	d.reconciledBody = body
	d.completionObserved = true
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	Expect(d.outputAllowed).To(BeFalse())
	Expect(body.reads).To(BeZero())
	Expect(body.closed).To(Equal(1))
	Expect(d.calls).To(Equal(1))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(a.JobID)
		if e != nil {
			return e
		}
		Expect(rows[0].State).To(Equal("failed"))
		Expect(rows[0].Reason).To(Equal("result_unavailable"))
		j, e := tx.CancelJob(a.JobID)
		if e != nil {
			return e
		}
		Expect(j.State).To(Equal("cancelled"))
		return nil
	})).To(Succeed())
	exported := false
	Expect(RestoreOutstanding(ctx, s.profile.DataDir+"/storage/library.sqlite", func(raw json.RawMessage) error {
		var identity struct {
			ID string `json:"attempt_id"`
		}
		if e := json.Unmarshal(raw, &identity); e != nil {
			return e
		}
		if identity.ID == "" {
			return nil
		}
		var fact providerRestoreFact
		if e := json.Unmarshal(raw, &fact); e != nil {
			return e
		}
		if fact.ID != "" && fact.JobID == a.JobID {
			Expect(fact.State).To(Equal("failed"))
			Expect(fact.Reason).To(Equal("result_unavailable"))
			exported = true
		}
		return nil
	})).To(Succeed())
	Expect(exported).To(BeTrue())

})

type providerSlowDriver struct {
	entered, stopped chan struct{}
	once             sync.Once
}

func (d *providerSlowDriver) Execute(ctx context.Context, a domain.ProviderAttempt, _ []domain.ProviderInput, send func() error, observe func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	defer close(d.stopped)
	if e := send(); e != nil {
		return work.ProviderOutcome{}, e
	}
	if e := observe(work.ProviderProgress{RequestID: "accepted-turn", SessionID: "accepted-thread"}); e != nil {
		return work.ProviderOutcome{}, e
	}
	d.once.Do(func() { close(d.entered) })
	<-ctx.Done()
	return work.ProviderOutcome{}, ctx.Err()
}
func (d *providerSlowDriver) Cancel(context.Context, domain.ProviderAttempt) error { return nil }
func (d *providerSlowDriver) Reconcile(context.Context, domain.ProviderAttempt, bool) (work.ProviderOutcome, error) {
	return work.ProviderOutcome{State: "outcome_unknown", Reason: "transport_unknown"}, nil
}

type providerLocalProcessor struct {
	s        *Store
	finished chan struct{}
}

func (p providerLocalProcessor) Process(ctx context.Context, id string) error {
	e := p.s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.ClaimJob(id)
		if e != nil {
			return e
		}
		return tx.FinishJob(id, j.AttemptGeneration, "", nil)
	})
	if e == nil {
		close(p.finished)
	}
	return e
}

var _ = It("PA26 one dispatcher advances local work during a slow provider and drains its owned phase with honest unknown accounting", Label("persistence"), func() {
	s, a := providerFixture(false)
	turn, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &providerSlowDriver{entered: make(chan struct{}), stopped: make(chan struct{})}
	provider := &work.ProviderCoordinator{Store: s, Driver: d, Bytes: providerFixtureBytes{s}}
	local := providerLocalProcessor{s: s, finished: make(chan struct{})}
	dispatcher := work.Dispatcher{Store: s, Provider: provider, Imports: local}
	done := make(chan error, 1)
	awaited := false
	go func() { done <- dispatcher.Run(turn) }()
	DeferCleanup(func() {
		cancel()
		if !awaited {
			Eventually(done, 30*time.Second).Should(Receive(Succeed()))
		}
	})
	Eventually(d.entered, 30*time.Second).Should(BeClosed())
	localID := id(GinkgoT())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.CreateJob(domain.Job{ID: localID, Kind: "import"}) })).To(Succeed())
	Eventually(local.finished, 30*time.Second).Should(BeClosed())
	select {
	case <-d.stopped:
		Fail("external phase stopped before local work progressed")
	default:
	}
	cancel()
	Eventually(d.stopped, 30*time.Second).Should(BeClosed())
	Eventually(done, 30*time.Second).Should(Receive(Succeed()))
	awaited = true
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(j.Error).To(Equal("outcome_unknown"))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(j.ID)
		if e != nil {
			return e
		}
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].State).To(Equal("outcome_unknown"))
		Expect(rows[0].RequestID).To(Equal("accepted-turn"))
		return nil
	})).To(Succeed())
})

var _ = It("PA27 output EOF without explicit backend completion cannot save or apply a result", Label("persistence"), func() {
	s, a := providerFixture(false)
	d := &providerFake{withoutCompletion: true}
	p := work.ProviderCoordinator{Store: s, Driver: d, Bytes: providerFixtureBytes{s}}
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(a.JobID)
		if e != nil {
			return e
		}
		Expect(rows[0].State).To(Equal("outcome_unknown"))
		Expect(rows[0].ResultHash).To(BeEmpty())
		return nil
	})).To(Succeed())
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
})

// This test byte adapter is synthetic metadata cooperation, not filesystem proof.
type providerContinuationFixtureBytes struct{ providerFixtureBytes }

func (b providerContinuationFixtureBytes) Read(ctx context.Context, o domain.Object, _ domain.Pin, consume func(io.ReadSeeker) error) error {
	e := b.s.Write(ctx, func(tx app.WriteTx) error {
		actual, e := tx.Object(o.Hash)
		if e != nil {
			return e
		}
		if actual.State != "ready" || actual.Size != o.Size {
			return domain.ErrContentIntegrity
		}
		return nil
	})
	if e != nil {
		return e
	}
	return consume(strings.NewReader("synthetic verified-byte callback"))
}

var _ = It("PA52 output obtained by explicit reconciliation after actual Store close and reopen remains reusable without redispatch", Label("persistence"), func() {
	s, a := providerFixture()
	profile := s.profile
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		return tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "restart-request", "restart-session", "transport_unknown")
	})).To(Succeed())
	Expect(s.Close()).To(Succeed())
	reopened := New(nil)
	_, closeDB, e := reopened.Prepare(ctx, profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeDB()
	d := &providerFake{reconciled: "result_saved", completionObserved: true, reconciledBody: io.NopCloser(strings.NewReader("complete recovered output"))}
	applied := 0
	p := work.ProviderCoordinator{Store: reopened, Driver: d, Bytes: providerContinuationFixtureBytes{providerFixtureBytes{reopened}}, Apply: func(app.WriteTx, domain.ProviderAttempt) error { applied++; return nil }}
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	Expect(reopened.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "reconcile_only")
		return e
	})).To(Succeed())
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	Expect(reopened.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "continue")
		return e
	})).To(Succeed())
	j, e := reopened.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("queued"))
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	j, e = reopened.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
	Expect(d.calls).To(BeZero())
	Expect(d.lookups).To(Equal(1))
	Expect(applied).To(Equal(1))
})

var _ = It("PA53 explicit no-call restore facts settle raw prepared ledgers through retention and re-export", Label("persistence"), func() {
	for _, reverse := range []bool{false, true} {
		s, a := providerFixture()
		older := filepath.Join(GinkgoT().TempDir(), "prepared.sqlite")
		Expect(s.SnapshotDatabase(ctx, older)).To(Succeed())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			j, e := tx.CancelJob(a.JobID)
			if e != nil {
				return e
			}
			return tx.ConfirmStop(j.ID, j.AttemptGeneration)
		})).To(Succeed())
		var inventory bytes.Buffer
		Expect(RestoreOutstanding(ctx, s.profile.DataDir+"/storage/library.sqlite", func(raw json.RawMessage) error { inventory.Write(raw); inventory.WriteByte('\n'); return nil })).To(Succeed())
		if reverse {
			// Re-export of a previously retained no-call fact precedes the older intent.
			Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(inventory.Bytes()), func(domain.Object) (bool, error) { return true, nil })).To(Succeed())
			var exported bytes.Buffer
			Expect(RestoreOutstanding(ctx, older, func(raw json.RawMessage) error { exported.Write(raw); exported.WriteByte('\n'); return nil })).To(Succeed())
			inventory = exported
		}
		Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(inventory.Bytes()), func(domain.Object) (bool, error) { return true, nil })).To(Succeed())
		db, e := openCopy(older)
		Expect(e).NotTo(HaveOccurred())
		var state, reason string
		var may int
		Expect(db.QueryRow(`SELECT state,reason,may_have_dispatched FROM provider_attempts WHERE attempt_id=?`, a.ID).Scan(&state, &reason, &may)).To(Succeed())
		Expect(db.Close()).To(Succeed())
		Expect(state).To(Equal("cancelled"))
		Expect(reason).To(Equal("cancel_before_dispatch"))
		Expect(may).To(BeZero())
		// The exact captured request is now safe new owner-authorized work without
		// an uncertainty-bypass grant; no provider call is performed by this setup.
		cloneDB, e := openCopy(older)
		Expect(e).NotTo(HaveOccurred())
		clone := New(nil)
		clone.db = cloneDB
		_, e = providerClone(clone, a, a.RequestSHA, "", nil)
		Expect(cloneDB.Close()).To(Succeed())
		Expect(e).NotTo(HaveOccurred())
	}
})

var _ = It("PA54 both retained unknown conflict chains survive union and identical replay without fact growth", Label("persistence"), func() {
	s, a := providerFixture()
	hashes := []string{strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64), strings.Repeat("4", 64)}
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		for _, hash := range hashes {
			if e := tx.PutDurableObject(domain.Object{Hash: hash, Size: 1, State: "ready"}); e != nil {
				return e
			}
		}
		return nil
	})).To(Succeed())
	fact := func(request, hash string) map[string]any {
		return map[string]any{"attempt_id": "divergent-chains", "request_sha256": a.RequestSHA, "state": "result_saved", "may_have_dispatched": true, "provider_request_id": request, "result_hash": hash}
	}
	left, right := fact("left", hashes[0]), fact("right", hashes[2])
	left["state"], right["state"] = "outcome_unknown", "outcome_unknown"
	left["conflicting_prior_fact"], right["conflicting_prior_fact"] = fact("left-prior", hashes[1]), fact("right-prior", hashes[3])
	path := s.profile.DataDir + "/storage/library.sqlite"
	for _, v := range []map[string]any{left, right} {
		Expect(RetainProviderOrphans(ctx, path, bytes.NewReader(jsonBytes(v)), func(domain.Object) (bool, error) { return true, nil })).To(Succeed())
	}
	var raw []byte
	var state string
	Expect(s.db.QueryRow(`SELECT state,value FROM provider_orphans WHERE attempt_id='divergent-chains'`).Scan(&state, &raw)).To(Succeed())
	Expect(state).To(Equal("outcome_unknown"))
	for _, hash := range hashes {
		Expect(string(raw)).To(ContainSubstring(hash))
	}
	for _, request := range []string{"left", "left-prior", "right", "right-prior"} {
		Expect(string(raw)).To(ContainSubstring(request))
	}
	size := len(raw)
	for turn := 0; turn < 3; turn++ {
		Expect(RetainProviderOrphans(ctx, path, bytes.NewReader(raw), func(domain.Object) (bool, error) { return true, nil })).To(Succeed())
		Expect(s.db.QueryRow(`SELECT value FROM provider_orphans WHERE attempt_id='divergent-chains'`).Scan(&raw)).To(Succeed())
		Expect(len(raw)).To(Equal(size))
	}
	var pins int
	Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_orphan' AND owner_id='divergent-chains'`).Scan(&pins)).To(Succeed())
	Expect(pins).To(Equal(4))
})

var _ = It("PA55 repeated owner retry reuses exactly one unconsumed generation grant", Label("persistence"), func() {
	s, core, _, _ := authFixture(GinkgoT())
	pairOwner(GinkgoT(), core, "retry-unconsumed")
	a, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	p := work.ProviderCoordinator{Store: s}
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	var generation int64
	for turn := 0; turn < 2; turn++ {
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			j, e := tx.Job(a.JobID)
			if e != nil {
				return e
			}
			generation = j.AttemptGeneration
			_, e = tx.ResumeProviderJob(j.ID, generation, "retry")
			return e
		})).To(Succeed())
		Expect(p.Process(ctx, a.JobID)).To(Succeed())
	}
	var count int
	Expect(s.db.QueryRow(`SELECT count(*) FROM provider_retry_authorizations WHERE job_id=? AND attempt_generation=?`, a.JobID, generation).Scan(&count)).To(Succeed())
	Expect(count).To(Equal(1))
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.AttemptGeneration).To(Equal(generation))
})

var _ = It("PA56 recovered pending lookup commits interrupted to reconciling before external status work", Label("persistence"), func() {
	s, core, _, _ := authFixture(GinkgoT())
	pairOwner(GinkgoT(), core, "normalize-lookup")
	a, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	a, e = providerPrepareEmpty(s, a)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		if e := tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "request", "session", "transport_unknown"); e != nil {
			return e
		}
		if e := tx.PauseProviderJob(a.JobID, "unknown"); e != nil {
			return e
		}
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "reconcile_only")
		return e
	})).To(Succeed())
	profile := s.profile
	Expect(s.Close()).To(Succeed())
	reopened := New(nil)
	_, closeDB, e := reopened.Prepare(ctx, profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeDB()
	Expect(providerResume(reopened, a.JobID, "continue")).To(MatchError(domain.ErrTransition))
	Expect(providerResume(reopened, a.JobID, "retry")).To(MatchError(domain.ErrTransition))
	d := &providerFake{}
	p := work.ProviderCoordinator{Store: reopened, Driver: d}
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	j, e := reopened.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("reconciling"))
	Expect(d.lookups).To(BeZero())
	Expect(d.calls).To(BeZero())
})

func providerSavedEmptyFixture() (*Store, domain.ProviderAttempt) {
	s, core, _, _ := authFixture(GinkgoT())
	pairOwner(GinkgoT(), core, "saved-choice")
	a, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	a, e = providerPrepareEmpty(s, a)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		o := domain.Object{Hash: strings.Repeat("d", 64), Size: 4, State: "ready"}
		if e := tx.PutDurableObject(o); e != nil {
			return e
		}
		if e := tx.SaveProviderResult(a.ID, o); e != nil {
			return e
		}
		return tx.PauseProviderJob(a.JobID, "saved_result_requires_continuation")
	})).To(Succeed())
	return s, a
}
func providerResume(s *Store, job, decision string) error {
	return s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(job)
		if e != nil {
			return e
		}
		_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, decision)
		return e
	})
}

var _ = It("PA57 unconsumed continuation survives rolled-back claim and honors both last owner-choice directions", Label("persistence"), func() {
	for _, last := range []string{"continue", "retry"} {
		s, a := providerSavedEmptyFixture()
		before, e := s.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		d := &providerFake{}
		p := work.ProviderCoordinator{Store: s, Driver: d, Bytes: providerContinuationFixtureBytes{providerFixtureBytes{s}}}
		Expect(providerResume(s, a.JobID, "continue")).To(Succeed())
		failure := errors.New("local application rollback before claim commits")
		p.Apply = func(app.WriteTx, domain.ProviderAttempt) error { return failure }
		Expect(p.Process(ctx, a.JobID)).To(MatchError(failure))
		paused, e := s.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(paused.State).To(Equal("paused"))
		Expect(paused.AttemptGeneration).To(Equal(before.AttemptGeneration))
		Expect(paused.Result).To(BeEmpty())
		p.Apply = nil
		if last == "continue" {
			Expect(providerResume(s, a.JobID, "retry")).To(Succeed())
			nilDriver := work.ProviderCoordinator{Store: s}
			Expect(nilDriver.Process(ctx, a.JobID)).To(Succeed())
			Expect(providerResume(s, a.JobID, "continue")).To(Succeed())
		} else {
			Expect(providerResume(s, a.JobID, "continue")).To(Succeed())
			// A second failed claim leaves exactly the same unconsumed reservation.
			p.Apply = func(app.WriteTx, domain.ProviderAttempt) error { return failure }
			Expect(p.Process(ctx, a.JobID)).To(MatchError(failure))
			p.Apply = nil
			Expect(providerResume(s, a.JobID, "retry")).To(Succeed())
		}
		Expect(p.Process(ctx, a.JobID)).To(Succeed())
		done, e := s.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(done.State).To(Equal("completed"))
		if last == "continue" {
			Expect(d.calls).To(BeZero())
		} else {
			Expect(d.calls).To(Equal(1))
		}
		Expect(d.lookups).To(BeZero())
		var grants int
		Expect(s.db.QueryRow(`SELECT count(*) FROM provider_retry_authorizations WHERE job_id=?`, a.JobID).Scan(&grants)).To(Succeed())
		Expect(grants).To(Equal(1))
	}
})

var _ = It("PA59 literal prepared and no-call inventory orders converge and both conflict chains survive ledger-first re-export", Label("persistence"), func() {
	s, a := providerFixture()
	var prepared []byte
	Expect(s.db.QueryRow(providerAttemptInventory+` WHERE a.attempt_id=?`, a.ID).Scan(&prepared)).To(Succeed())
	basePaths := []string{filepath.Join(GinkgoT().TempDir(), "prepared-left.sqlite"), filepath.Join(GinkgoT().TempDir(), "prepared-right.sqlite")}
	for _, path := range basePaths {
		Expect(s.SnapshotDatabase(ctx, path)).To(Succeed())
	}
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.CancelJob(a.JobID)
		if e != nil {
			return e
		}
		return tx.ConfirmStop(j.ID, j.AttemptGeneration)
	})).To(Succeed())
	var noCall []byte
	Expect(s.db.QueryRow(providerAttemptInventory+` WHERE a.attempt_id=?`, a.ID).Scan(&noCall)).To(Succeed())
	for index, order := range [][]json.RawMessage{{prepared, noCall}, {noCall, prepared}} {
		snapshot := basePaths[index]
		var records bytes.Buffer
		for _, raw := range order {
			records.Write(raw)
			records.WriteByte('\n')
		}
		Expect(RetainProviderOrphans(ctx, snapshot, bytes.NewReader(records.Bytes()), func(domain.Object) (bool, error) { return true, nil })).To(Succeed())
		db, e := openCopy(snapshot)
		Expect(e).NotTo(HaveOccurred())
		var rawState, orphanState string
		Expect(db.QueryRow(`SELECT state FROM provider_attempts WHERE attempt_id=?`, a.ID).Scan(&rawState)).To(Succeed())
		Expect(db.QueryRow(`SELECT state FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&orphanState)).To(Succeed())
		Expect(db.Close()).To(Succeed())
		Expect(rawState).To(Equal("cancelled"))
		Expect(orphanState).To(Equal("cancelled"))
	}
	// A real same-ID ledger is terminal while its merged orphan remains conflicted.
	x, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	x, e = providerPrepareEmpty(s, x)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(x.ID); e != nil {
			return e
		}
		return tx.ObserveProviderAttempt(x.ID, "failed", "native-current", "session", "provider_failed")
	})).To(Succeed())
	older := filepath.Join(GinkgoT().TempDir(), "terminal-ledger.sqlite")
	Expect(s.SnapshotDatabase(ctx, older)).To(Succeed())
	var terminalFact map[string]any
	var raw []byte
	Expect(s.db.QueryRow(providerAttemptInventory+` WHERE a.attempt_id=?`, x.ID).Scan(&raw)).To(Succeed())
	Expect(json.Unmarshal(raw, &terminalFact)).To(Succeed())
	conflict := map[string]any{}
	for k, v := range terminalFact {
		conflict[k] = v
	}
	conflict["state"] = "outcome_unknown"
	prior := map[string]any{}
	for k, v := range terminalFact {
		prior[k] = v
	}
	prior["state"] = "result_saved"
	prior["result_hash"] = strings.Repeat("e", 64)
	conflict["conflicting_prior_fact"] = prior
	path := s.profile.DataDir + "/storage/library.sqlite"
	Expect(RetainProviderOrphans(ctx, path, bytes.NewReader(jsonBytes(conflict)), nil)).To(Succeed())
	// Actual supported observation leaves raw ledger terminal but cannot settle all conflicts.
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.ObserveProviderAttempt(x.ID, "failed", "native-current", "session", "provider_failed")
	})).To(Succeed())
	for turn := 0; turn < 2; turn++ {
		var inventory bytes.Buffer
		Expect(RestoreOutstanding(ctx, path, func(raw json.RawMessage) error { inventory.Write(raw); inventory.WriteByte('\n'); return nil })).To(Succeed())
		Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(inventory.Bytes()), nil)).To(Succeed())
		db, e := openCopy(older)
		Expect(e).NotTo(HaveOccurred())
		var state, value string
		Expect(db.QueryRow(`SELECT state,CAST(value AS TEXT) FROM provider_orphans WHERE attempt_id=?`, x.ID).Scan(&state, &value)).To(Succeed())
		Expect(db.Close()).To(Succeed())
		Expect(state).To(Equal("outcome_unknown"))
		Expect(value).To(ContainSubstring(strings.Repeat("e", 64)))
		Expect(value).To(ContainSubstring("provider_failed"))
		path = older
	}
})

var _ = It("PA60 a restored terminal intent that becomes definite can clear its finite hold without any external operation", Label("persistence"), func() {
	s, core, _, _ := authFixture(GinkgoT())
	pairOwner(GinkgoT(), core, "settled-held")
	a, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	a, e = providerPrepareEmpty(s, a)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		if e := tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "request", "session", "transport_unknown"); e != nil {
			return e
		}
		if e := tx.FinishJob(a.JobID, a.Generation, "fixture_failed", nil); e != nil {
			return e
		}
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "reconcile_only")
		return e
	})).To(Succeed())
	path := filepath.Join(GinkgoT().TempDir(), "settled-hold.sqlite")
	Expect(s.SnapshotDatabase(ctx, path)).To(Succeed())
	hold := id(GinkgoT())
	_, e = ProtectRestoredDatabase(ctx, path, s.profile, hold, CopyCurrentConfiguration(ctx, path), configuration.Validate)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.ObserveProviderAttempt(a.ID, "cancelled", "request", "session", "cancel_confirmed")
	})).To(Succeed())
	var inventory bytes.Buffer
	Expect(RestoreOutstanding(ctx, s.profile.DataDir+"/storage/library.sqlite", func(raw json.RawMessage) error { inventory.Write(raw); inventory.WriteByte('\n'); return nil })).To(Succeed())
	Expect(RetainProviderOrphans(ctx, path, bytes.NewReader(inventory.Bytes()), nil)).To(Succeed())
	db, e := openCopy(path)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	restored := New(nil)
	restored.db = db
	before, e := restored.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(before.ProviderAttempts.Unknown).To(BeZero())
	Expect(before.ProviderAttempts.ReconciliationPending).To(Equal(1))
	Expect(providerResume(restored, a.JobID, "reconcile_only")).To(MatchError(domain.ErrReconciliation))
	Expect(restored.Write(ctx, func(tx app.WriteTx) error { return tx.ReviewRecovery(hold, nil) })).To(Succeed())
	Expect(providerResume(restored, a.JobID, "reconcile_only")).To(Succeed())
	d := &providerFake{}
	p := work.ProviderCoordinator{Store: restored, Driver: d}
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	after, e := restored.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(after.State).To(Equal(before.State))
	Expect(after.Result).To(Equal(before.Result))
	Expect(after.AttemptGeneration).To(Equal(before.AttemptGeneration))
	Expect(after.ProviderAttempts.ReconciliationPending).To(BeZero())
	Expect(d.calls).To(BeZero())
	Expect(d.lookups).To(BeZero())
	Expect(providerResume(restored, a.JobID, "reconcile_only")).To(MatchError(domain.ErrTransition))
})

var _ = It("PA61 durable cancellation while Execute is blocked invokes Cancel before late output and never applies it", Label("persistence"), func() {
	s, a := providerFixture(false)
	entered, abort := make(chan struct{}), make(chan struct{})
	defer close(abort)
	d := &providerFake{cancelled: make(chan struct{})}
	d.afterProgress = func() error {
		close(entered)
		select {
		case <-d.cancelled:
			return nil
		case <-abort:
			return errors.New("fixture aborted")
		}
	}
	applied := 0
	p := work.ProviderCoordinator{Store: s, Driver: d, Bytes: providerFixtureBytes{s}, Apply: func(app.WriteTx, domain.ProviderAttempt) error { applied++; return nil }}
	done := make(chan error, 1)
	awaited := false
	DeferCleanup(func() {
		if !awaited {
			Eventually(done, 30*time.Second).Should(Receive())
		}
	})
	go func() { done <- p.Process(ctx, a.JobID) }()
	Eventually(entered, 30*time.Second).Should(BeClosed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { _, e := tx.CancelJob(a.JobID); return e })).To(Succeed())
	Eventually(d.cancelled, 30*time.Second).Should(BeClosed())
	Eventually(done, 30*time.Second).Should(Receive(Succeed()))
	awaited = true
	Expect(d.cancels.Load()).To(Equal(int64(1)))
	Expect(applied).To(BeZero())
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("cancelled"))
	Expect(j.CancelRequested).To(BeTrue())
	Expect(j.Result).To(BeEmpty())
})

var _ = It("PA62 fresh carrier admission isolates captured parent finite-grant predicates and rolls back partial finite admission", Label("persistence"), func() {
	s, core, _, _ := authFixture(GinkgoT())
	pairOwner(GinkgoT(), core, "carrier-guards")
	source, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	var principal string
	var target json.RawMessage
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(source.JobID)
		if e != nil {
			return e
		}
		var scope domain.ProviderScope
		if e = json.Unmarshal(j.Scope, &scope); e != nil {
			return e
		}
		principal, target = scope.PrincipalID, scope.Target
		return nil
	})).To(Succeed())
	raw := jsonBytes(map[string]any{"attempt_id": "finite-valid", "job_id": "missing-original", "request_sha256": source.RequestSHA, "state": "outcome_unknown", "principal_id": principal, "target": target})
	Expect(RetainProviderOrphans(ctx, s.profile.DataDir+"/storage/library.sqlite", bytes.NewReader(raw), nil)).To(Succeed())
	for _, guard := range []string{"parent", "target-set", "read-set", "generation-retry"} {
		a, e := providerEmptyJob(s)
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			j, e := tx.Job(a.JobID)
			if e != nil {
				return e
			}
			switch guard {
			case "parent":
				return tx.CreateJob(domain.Job{ID: id(GinkgoT()), Kind: "question", ParentID: source.JobID, Scope: j.Scope})
			case "generation-retry":
				if e = tx.PauseProviderJob(j.ID, "unavailable"); e != nil {
					return e
				}
				_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "retry")
				return e
			default:
				var scope domain.ProviderScope
				if e = json.Unmarshal(j.Scope, &scope); e != nil {
					return e
				}
				if guard == "target-set" {
					scope.TargetSetID = "captured-target"
				} else {
					scope.ReadIDs = []string{"captured-read"}
				}
				return s.execFixtureScope(tx, j.ID, scope)
			}
		})).To(Succeed())
		if guard == "parent" {
			// Create the parent-bearing candidate explicitly; keep every earlier guard valid.
			var parentJob string
			Expect(s.db.QueryRow(`SELECT job_id FROM jobs WHERE parent_id=?`, source.JobID).Scan(&parentJob)).To(Succeed())
			a.JobID = parentJob
		}
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			return tx.AuthorizeProviderReconciliation(a.JobID, []string{"finite-valid"})
		})).To(MatchError(domain.ErrTransition))
	}
	denied := jsonBytes(map[string]any{"attempt_id": "finite-other-owner", "job_id": "missing-other", "request_sha256": source.RequestSHA, "state": "outcome_unknown", "principal_id": "different-owner", "target": target})
	Expect(RetainProviderOrphans(ctx, s.profile.DataDir+"/storage/library.sqlite", bytes.NewReader(denied), nil)).To(Succeed())
	for _, test := range []struct {
		ids  []string
		want error
	}{{[]string{"finite-valid", "finite-other-owner"}, domain.ErrDenied}, {[]string{"finite-valid", "finite-valid"}, domain.ErrInvalidTarget}} {
		var refused string
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			a, e := providerCreateEmptyJob(tx)
			if e != nil {
				return e
			}
			refused = a.JobID
			return tx.AuthorizeProviderReconciliation(a.JobID, test.ids)
		})).To(MatchError(test.want))
		_, e := s.Job(ctx, refused)
		Expect(e).To(MatchError(domain.ErrNotFound))
	}
	var candidate string
	var eventsBefore int
	Expect(s.db.QueryRow(`SELECT count(*) FROM events`).Scan(&eventsBefore)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		a, e := providerCreateEmptyJob(tx)
		if e != nil {
			return e
		}
		candidate = a.JobID
		return tx.AuthorizeProviderReconciliation(a.JobID, []string{"finite-valid", "missing-second"})
	})).To(MatchError(domain.ErrNotFound))
	_, e = s.Job(ctx, candidate)
	Expect(e).To(MatchError(domain.ErrNotFound))
	var count int
	Expect(s.db.QueryRow(`SELECT count(*) FROM provider_reconcile_intents WHERE job_id=?`, candidate).Scan(&count)).To(Succeed())
	Expect(count).To(BeZero())
	Expect(s.db.QueryRow(`SELECT count(*) FROM provider_linked_retry_authorizations WHERE job_id=?`, candidate).Scan(&count)).To(Succeed())
	Expect(count).To(BeZero())
	Expect(s.db.QueryRow(`SELECT count(*) FROM events`).Scan(&count)).To(Succeed())
	Expect(count).To(Equal(eventsBefore))
})

// Setup-only mutation of the captured scope; assertions remain in Ginkgo.
func (s *Store) execFixtureScope(tx app.WriteTx, id string, scope domain.ProviderScope) error {
	return tx.(*writeTx).exec(`UPDATE jobs SET scope=? WHERE job_id=?`, jsonBytes(scope), id)
}

var _ = It("PA63 a real preparation rollback pauses outside WorkJobs while the dispatcher advances eligible provider and local jobs", Label("persistence"), func() {
	s, core, _, _ := authFixture(GinkgoT())
	pairOwner(GinkgoT(), core, "prepare-rollback")
	bad, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	d := &providerFake{}
	p := &work.ProviderCoordinator{Store: s, Driver: d, Bytes: providerFixtureBytes{s}}
	sentinel := errors.New("fixture transaction failure")
	armed := true
	s.fault = func(b Boundary) error {
		if b == BeforeCommit && armed {
			armed = false
			return sentinel
		}
		return nil
	}
	Expect(p.Process(ctx, bad.JobID)).To(MatchError(sentinel))
	s.fault = nil
	j, e := s.Job(ctx, bad.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(j.Error).To(Equal("preparation_failed"))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		jobs, e := tx.WorkJobs(64)
		if e != nil {
			return e
		}
		for _, j := range jobs {
			Expect(j.ID).NotTo(Equal(bad.JobID))
		}
		return nil
	})).To(Succeed())
	good, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	localID := id(GinkgoT())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.CreateJob(domain.Job{ID: localID, Kind: "import"}) })).To(Succeed())
	local := providerLocalProcessor{s: s, finished: make(chan struct{})}
	turn, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (work.Dispatcher{Store: s, Provider: p, Imports: local}).Run(turn) }()
	Eventually(local.finished, 30*time.Second).Should(BeClosed())
	Eventually(func() string {
		j, e := s.Job(ctx, good.JobID)
		if e != nil {
			return "error"
		}
		return j.State
	}, 30*time.Second).Should(Equal("completed"))
	cancel()
	Eventually(done, 30*time.Second).Should(Receive(Succeed()))
	Expect(d.calls).To(Equal(1))
})

var _ = It("PA67 queued continuation restart is reusable but protected restore keeps the old runtime ineligible", Label("persistence"), func() {
	for _, mode := range []string{"queued", "claimed", "protected"} {
		s, a := providerSavedEmptyFixture()
		Expect(providerResume(s, a.JobID, "continue")).To(Succeed())
		if mode == "claimed" {
			Expect(s.Write(ctx, func(tx app.WriteTx) error { _, e := tx.ClaimJob(a.JobID); return e })).To(Succeed())
		}
		if mode != "protected" {
			profile := s.profile
			Expect(s.Close()).To(Succeed())
			reopened := New(nil)
			_, closeDB, e := reopened.Prepare(ctx, profile)
			Expect(e).NotTo(HaveOccurred())
			defer closeDB()
			d := &providerFake{}
			p := work.ProviderCoordinator{Store: reopened, Driver: d, Bytes: providerContinuationFixtureBytes{providerFixtureBytes{reopened}}}
			if mode == "claimed" {
				Expect(p.Process(ctx, a.JobID)).To(Succeed())
				Expect(p.Process(ctx, a.JobID)).To(Succeed())
				Expect(providerResume(reopened, a.JobID, "continue")).To(Succeed())
			}
			Expect(p.Process(ctx, a.JobID)).To(Succeed())
			j, e := reopened.Job(ctx, a.JobID)
			Expect(e).NotTo(HaveOccurred())
			Expect(j.State).To(Equal("completed"))
			Expect(d.calls).To(BeZero())
		} else {
			path := filepath.Join(GinkgoT().TempDir(), "queued-before-protected-restore.sqlite")
			Expect(s.SnapshotDatabase(ctx, path)).To(Succeed())
			hold := id(GinkgoT())
			lib, e := ProtectRestoredDatabase(ctx, path, s.profile, hold, CopyCurrentConfiguration(ctx, path), configuration.Validate)
			Expect(e).NotTo(HaveOccurred())
			Expect(lib.RuntimeGeneration).NotTo(Equal(a.Runtime))
			db, e := openCopy(path)
			Expect(e).NotTo(HaveOccurred())
			defer db.Close()
			restored := New(nil)
			restored.db = db
			Expect(restored.Write(ctx, func(tx app.WriteTx) error { return tx.ReviewRecovery(hold, nil) })).To(Succeed())
			Expect(providerResume(restored, a.JobID, "reconcile_only")).To(Succeed())
			p := work.ProviderCoordinator{Store: restored}
			Expect(p.Process(ctx, a.JobID)).To(Succeed())
			Expect(restored.Write(ctx, func(tx app.WriteTx) error {
				actual, e := tx.ProviderAttempt(a.ID)
				if e != nil {
					return e
				}
				j, e := tx.Job(a.JobID)
				if e != nil {
					return e
				}
				Expect(actual.ApplyGeneration).To(Equal(j.AttemptGeneration))
				Expect(actual.Runtime).To(Equal(a.Runtime))
				_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "continue")
				return e
			})).To(MatchError(domain.ErrReconciliation), "generation eligibility passes; actual restored runtime is the first current-authority refusal")
		}
	}
})

var _ = It("PA68 cancelled and status-carrier restart branches preserve interrupted reconciliation before resolution", Label("persistence"), func() {
	for _, carrier := range []bool{false, true} {
		s, core, _, _ := authFixture(GinkgoT())
		pairOwner(GinkgoT(), core, "normalize-all")
		a, e := providerEmptyJob(s)
		Expect(e).NotTo(HaveOccurred())
		if carrier {
			var scope domain.ProviderScope
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				j, e := tx.Job(a.JobID)
				if e != nil {
					return e
				}
				return json.Unmarshal(j.Scope, &scope)
			})).To(Succeed())
			raw := jsonBytes(map[string]any{"attempt_id": "carrier-restart-orphan", "job_id": "absent-original", "principal_id": scope.PrincipalID, "target": scope.Target, "request_sha256": scope.RequestSHA, "state": "outcome_unknown"})
			Expect(RetainProviderOrphans(ctx, s.profile.DataDir+"/storage/library.sqlite", bytes.NewReader(raw), nil)).To(Succeed())
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				return tx.AuthorizeProviderReconciliation(a.JobID, []string{"carrier-restart-orphan"})
			})).To(Succeed())
		} else {
			a, e = providerPrepareEmpty(s, a)
			Expect(e).NotTo(HaveOccurred())
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				if e := tx.DispatchProviderAttempt(a.ID); e != nil {
					return e
				}
				j, e := tx.CancelJob(a.JobID)
				if e != nil {
					return e
				}
				return tx.ConfirmStop(j.ID, j.AttemptGeneration)
			})).To(Succeed())
		}
		profile := s.profile
		Expect(s.Close()).To(Succeed())
		reopened := New(nil)
		_, closeDB, e := reopened.Prepare(ctx, profile)
		Expect(e).NotTo(HaveOccurred())
		defer closeDB()
		d := &providerFake{reconciled: "cancelled"}
		p := work.ProviderCoordinator{Store: reopened, Driver: d}
		Expect(p.Process(ctx, a.JobID)).To(Succeed())
		j, e := reopened.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		Expect(j.State).To(Equal("reconciling"))
		Expect(d.lookups).To(BeZero())
		Expect(p.Process(ctx, a.JobID)).To(Succeed())
		j, e = reopened.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		if carrier {
			Expect(j.State).To(Equal("completed"))
			Expect(d.lookups).To(Equal(1))
		} else {
			Expect(j.State).To(Equal("paused"))
			Expect(j.CancelRequested).To(BeTrue())
		}
		Expect(d.calls).To(BeZero())
	}
})

// Poll owns collection; the non-provider refusal proves the old slot was drained.
func providerCollectPhase(p *work.ProviderCoordinator, s *Store) {
	probe := id(GinkgoT())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.CreateJob(domain.Job{ID: probe, Kind: "import"}) })).To(Succeed())
	Eventually(func() error {
		if e := p.PollProvider(ctx); e != nil {
			return e
		}
		return p.ScheduleProvider(ctx, probe)
	}, 30*time.Second).Should(MatchError(domain.ErrTransition))
}

var _ = It("PA70 actual Poll preserves a successful recovered normalization instead of inserting a paused state", Label("persistence"), func() {
	s, core, _, _ := authFixture(GinkgoT())
	pairOwner(GinkgoT(), core, "collector-normalization")
	a, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	a, e = providerPrepareEmpty(s, a)
	Expect(e).NotTo(HaveOccurred())
	profile := s.profile
	Expect(s.Close()).To(Succeed())
	reopened := New(nil)
	_, closeDB, e := reopened.Prepare(ctx, profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeDB()
	d := &providerFake{}
	p := &work.ProviderCoordinator{Store: reopened, Driver: d}
	Expect(p.ScheduleProvider(ctx, a.JobID)).To(Succeed())
	providerCollectPhase(p, reopened)
	j, e := reopened.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("reconciling"))
	Expect(d.calls).To(BeZero())
	Expect(d.lookups).To(BeZero())
	Expect(p.StopProviders()).To(Succeed())
})

var _ = It("PA71 public cancel normalizes recovered work atomically and preserves an existing cancel generation", Label("persistence"), func() {
	for _, dispatched := range []bool{false, true} {
		for _, priorCancel := range []bool{false, true} {
			s, core, _, _ := authFixture(GinkgoT())
			pairOwner(GinkgoT(), core, "cancel-normalization")
			a, e := providerEmptyJob(s)
			Expect(e).NotTo(HaveOccurred())
			a, e = providerPrepareEmpty(s, a)
			Expect(e).NotTo(HaveOccurred())
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				if dispatched {
					if e := tx.DispatchProviderAttempt(a.ID); e != nil {
						return e
					}
				}
				if priorCancel {
					_, e := tx.CancelJob(a.JobID)
					return e
				}
				return nil
			})).To(Succeed())
			// A definite prepared cancellation is already terminal and does not recover.
			if priorCancel && !dispatched {
				continue
			}
			profile := s.profile
			Expect(s.Close()).To(Succeed())
			reopened := New(nil)
			_, closeDB, e := reopened.Prepare(ctx, profile)
			Expect(e).NotTo(HaveOccurred())
			defer closeDB()
			before, e := reopened.Job(ctx, a.JobID)
			Expect(e).NotTo(HaveOccurred())
			Expect(before.State).To(Equal("interrupted"))
			var j domain.Job
			Expect(reopened.Write(ctx, func(tx app.WriteTx) error { var e error; j, e = tx.CancelJob(a.JobID); return e })).To(Succeed())
			wantGeneration := before.AttemptGeneration + 1
			if priorCancel {
				wantGeneration = before.AttemptGeneration
			}
			Expect(j.AttemptGeneration).To(Equal(wantGeneration))
			Expect(j.CancelRequested).To(BeTrue())
			// One durable normalization revision precedes the final disposition in the same transaction.
			Expect(j.Revision).To(BeNumerically(">=", before.Revision+2))
			health, e := reopened.Health(ctx)
			Expect(e).NotTo(HaveOccurred())
			events, e := reopened.Events(ctx, health.Library.StreamEpoch, 0, 1000)
			Expect(e).NotTo(HaveOccurred())
			updates := []domain.Event{}
			for _, event := range events {
				if event.ResourceID == a.JobID && event.Revision > before.Revision {
					updates = append(updates, event)
				}
			}
			wantEvents := 2
			if !dispatched {
				wantEvents = 3
			}
			Expect(updates).To(HaveLen(wantEvents))
			Expect(updates[0].Revision).To(Equal(before.Revision + 1))
			Expect(updates[len(updates)-1].Revision).To(Equal(j.Revision))
			Expect(updates[0].Sequence).To(BeNumerically("<", updates[1].Sequence))
			for _, event := range updates {
				Expect(event.Body).To(BeEmpty())
			}
			if dispatched {
				Expect(j.State).To(Equal("paused"))
				Expect(j.Error).To(Equal("outcome_unknown"))
			} else {
				Expect(j.State).To(Equal("cancelled"))
			}
		}
	}
})

var _ = It("PA72 failed normalization commit leaves interrupted work reconstructible without a direct paused cleanup", Label("persistence"), func() {
	s, a := providerFixture()
	var e error
	profile := s.profile
	Expect(s.Close()).To(Succeed())
	reopened := New(nil)
	_, closeDB, e := reopened.Prepare(ctx, profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeDB()
	before, e := reopened.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	health, e := reopened.Health(ctx)
	Expect(e).NotTo(HaveOccurred())
	beforeEvents, e := reopened.Events(ctx, health.Library.StreamEpoch, 0, 256)
	Expect(e).NotTo(HaveOccurred())
	var beforePins int
	Expect(reopened.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_attempt' AND owner_id=?`, a.ID).Scan(&beforePins)).To(Succeed())
	Expect(beforePins).To(Equal(1))

	sentinel := errors.New("normalization commit rejected")
	armed := true
	reopened.fault = func(b Boundary) error {
		if b == BeforeCommit && armed {
			armed = false
			return sentinel
		}
		return nil
	}
	d := &providerFake{}
	p := &work.ProviderCoordinator{Store: reopened, Driver: d}
	Expect(p.Process(ctx, a.JobID)).To(MatchError(sentinel))
	reopened.fault = nil
	j, e := reopened.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j).To(Equal(before))
	afterEvents, e := reopened.Events(ctx, health.Library.StreamEpoch, 0, 256)
	Expect(e).NotTo(HaveOccurred())
	Expect(afterEvents).To(Equal(beforeEvents))
	var afterPins int
	Expect(reopened.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_attempt' AND owner_id=?`, a.ID).Scan(&afterPins)).To(Succeed())
	Expect(afterPins).To(Equal(beforePins))
	Expect(p.ScheduleProvider(ctx, a.JobID)).To(Succeed())
	providerCollectPhase(p, reopened)
	j, e = reopened.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("reconciling"))
	events, e := reopened.Events(ctx, health.Library.StreamEpoch, beforeEvents[len(beforeEvents)-1].Sequence, 256)
	Expect(e).NotTo(HaveOccurred())
	updates := []domain.Event{}
	for _, event := range events {
		if event.ResourceID == a.JobID {
			updates = append(updates, event)
		}
	}
	Expect(updates).To(HaveLen(1))
	Expect(updates[0].Revision).To(Equal(before.Revision + 1))
	Expect(updates[0].Body).To(BeEmpty())

	Expect(d.calls).To(BeZero())
	Expect(d.lookups).To(BeZero())
})

var _ = It("PA73 orphan-only saved results with independently ready bytes refuse continuation as a permanent transition", Label("persistence"), func() {
	s, core, _, _ := authFixture(GinkgoT())
	pairOwner(GinkgoT(), core, "orphan-dedup")
	a, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	var fact map[string]any
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		var scope domain.ProviderScope
		if e = json.Unmarshal(j.Scope, &scope); e != nil {
			return e
		}
		o := domain.Object{Hash: strings.Repeat("d", 64), Size: 4, State: "ready"}
		if e = tx.PutDurableObject(o); e != nil {
			return e
		}
		if e = tx.AcquirePin(domain.Pin{ID: "dedup-owner", Kind: "selected_snapshot", OwnerID: "independent", Hash: o.Hash}); e != nil {
			return e
		}
		fact = map[string]any{"attempt_id": a.ID, "job_id": a.JobID, "principal_id": scope.PrincipalID, "target": scope.Target, "request_sha256": scope.RequestSHA, "state": "result_saved", "result_hash": o.Hash, "provider_request_id": "prior-turn"}
		return tx.PauseProviderJob(a.JobID, "restored_result_requires_review")
	})).To(Succeed())
	Expect(RetainProviderOrphans(ctx, s.profile.DataDir+"/storage/library.sqlite", bytes.NewReader(jsonBytes(fact)), func(domain.Object) (bool, error) { return true, nil })).To(Succeed())
	before, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(providerResume(s, a.JobID, "continue")).To(MatchError(domain.ErrTransition))
	after, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(after).To(Equal(before))
})

var _ = It("PA74 cached Apply rollback reports its local failure rather than unavailable saved bytes", Label("persistence"), func() {
	s, a := providerSavedEmptyFixture()
	Expect(providerResume(s, a.JobID, "continue")).To(Succeed())
	sentinel := errors.New("local apply rollback")
	d := &providerFake{}
	p := &work.ProviderCoordinator{Store: s, Driver: d, Bytes: providerContinuationFixtureBytes{providerFixtureBytes{s}}, Apply: func(app.WriteTx, domain.ProviderAttempt) error { return sentinel }}
	Expect(p.Process(ctx, a.JobID)).To(MatchError(sentinel))
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.Error).To(Equal("preparation_failed"))
	Expect(d.calls).To(BeZero())
	Expect(d.lookups).To(BeZero())
})

type providerReconcileBoundary struct {
	providerFake
	store   *Store
	job     string
	entered chan domain.Job
	release chan struct{}
}

func (d *providerReconcileBoundary) Reconcile(ctx context.Context, a domain.ProviderAttempt, allowed bool) (work.ProviderOutcome, error) {
	j, e := d.store.Job(ctx, d.job)
	if e != nil {
		return work.ProviderOutcome{}, e
	}
	select {
	case d.entered <- j:
	case <-ctx.Done():
		return work.ProviderOutcome{}, ctx.Err()
	}
	select {
	case <-d.release:
	case <-ctx.Done():
		return work.ProviderOutcome{}, ctx.Err()
	}
	return d.providerFake.Reconcile(ctx, a, allowed)
}

var _ = It("PA77 actual Dispatcher keeps finite restarted status work reconciling between lookups and never admits retry", Label("persistence"), func() {
	s, core, _, _ := authFixture(GinkgoT())
	pairOwner(GinkgoT(), core, "finite-dispatcher")
	carrier, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	var scope domain.ProviderScope
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(carrier.JobID)
		if e != nil {
			return e
		}
		return json.Unmarshal(j.Scope, &scope)
	})).To(Succeed())
	var records bytes.Buffer
	for _, attempt := range []string{"finite-first", "finite-second"} {
		Expect(json.NewEncoder(&records).Encode(map[string]any{"attempt_id": attempt, "job_id": "absent-original", "principal_id": scope.PrincipalID, "target": scope.Target, "request_sha256": scope.RequestSHA, "state": "outcome_unknown", "may_have_dispatched": true})).To(Succeed())
	}
	Expect(RetainProviderOrphans(ctx, s.profile.DataDir+"/storage/library.sqlite", bytes.NewReader(records.Bytes()), nil)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.AuthorizeProviderReconciliation(carrier.JobID, []string{"finite-first", "finite-second"})
	})).To(Succeed())
	profile := s.profile
	Expect(s.Close()).To(Succeed())
	reopened := New(nil)
	_, closeDB, e := reopened.Prepare(ctx, profile)
	Expect(e).NotTo(HaveOccurred())
	defer closeDB()
	recovered, e := reopened.Job(ctx, carrier.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(recovered.State).To(Equal("interrupted"))
	health, e := reopened.Health(ctx)
	Expect(e).NotTo(HaveOccurred())

	d := &providerReconcileBoundary{providerFake: providerFake{reconciled: "cancelled"}, store: reopened, job: carrier.JobID, entered: make(chan domain.Job, 1), release: make(chan struct{})}
	p := &work.ProviderCoordinator{Store: reopened, Driver: d}
	turn, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- (work.Dispatcher{Store: reopened, Provider: p}).Run(turn) }()
	awaited := false
	DeferCleanup(func() {
		cancel()
		if !awaited {
			Eventually(done, 30*time.Second).Should(Receive(Succeed()))
		}
	})
	for remaining := 2; remaining > 0; remaining-- {
		var observed domain.Job
		Eventually(d.entered, 30*time.Second).Should(Receive(&observed))
		Expect(observed.State).To(Equal("reconciling"))
		events, e := reopened.Events(ctx, health.Library.StreamEpoch, 0, 256)
		Expect(e).NotTo(HaveOccurred())
		normalized := false
		for _, event := range events {
			if event.ResourceID == carrier.JobID && event.Revision == recovered.Revision+1 {
				normalized = true
				Expect(event.Body).To(BeEmpty())
				Expect(event.Revision).To(BeNumerically("<=", observed.Revision))
			}
		}
		Expect(normalized).To(BeTrue(), "normalization's bodyless revision is durably visible before the fake status lookup is released")

		Expect(observed.ProviderAttempts.ReconciliationPending).To(Equal(remaining))
		Expect(providerResume(reopened, carrier.JobID, "retry")).To(MatchError(domain.ErrTransition))
		d.release <- struct{}{}
	}
	Eventually(func() string {
		j, e := reopened.Job(ctx, carrier.JobID)
		if e != nil {
			return "error"
		}
		return j.State
	}, 30*time.Second).Should(Equal("completed"))
	cancel()
	// Access fake counters only after the owned Dispatcher has drained all phases.
	Eventually(done, 30*time.Second).Should(Receive(Succeed()))
	awaited = true
	Expect(d.calls).To(BeZero())
	Expect(d.lookups).To(Equal(2))
})

var _ = It("PA78 a single incoming terminal inventory record cannot erase an already retained conflict or grow it on identical replay", Label("persistence"), func() {
	s, core, _, _ := authFixture(GinkgoT())
	pairOwner(GinkgoT(), core, "single-terminal-conflict")
	a, e := providerEmptyJob(s)
	Expect(e).NotTo(HaveOccurred())
	a, e = providerPrepareEmpty(s, a)
	Expect(e).NotTo(HaveOccurred())
	hashes := []string{strings.Repeat("d", 64), strings.Repeat("e", 64)}
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		if e := tx.ObserveProviderAttempt(a.ID, "running", "current-turn", "session", ""); e != nil {
			return e
		}
		for _, h := range hashes {
			if e := tx.PutDurableObject(domain.Object{Hash: h, Size: 4, State: "ready"}); e != nil {
				return e
			}
		}
		return tx.SaveProviderResult(a.ID, domain.Object{Hash: hashes[0], Size: 4, State: "ready"})
	})).To(Succeed())
	var plain []byte
	Expect(s.db.QueryRow(providerAttemptInventory+` WHERE a.attempt_id=?`, a.ID).Scan(&plain)).To(Succeed())
	var conflict, prior map[string]any
	Expect(json.Unmarshal(plain, &conflict)).To(Succeed())
	Expect(json.Unmarshal(plain, &prior)).To(Succeed())
	conflict["state"] = "outcome_unknown"
	prior["result_hash"] = hashes[1]
	prior["provider_request_id"] = "prior-turn"

	observed := "2026-01-01T01:02:03Z"
	conflict["inputs"] = []domain.ProviderInput{{AttemptID: a.ID, ReadID: "current-observed", Hash: hashes[0], ObservedOrigin: "daemon_supplied", ObservedAt: &observed}}
	prior["inputs"] = []domain.ProviderInput{{AttemptID: a.ID, ReadID: "prior-unobserved", Hash: hashes[1]}}
	conflict["conflicting_prior_fact"] = prior
	path := s.profile.DataDir + "/storage/library.sqlite"
	available := func(domain.Object) (bool, error) { return true, nil }
	Expect(RetainProviderOrphans(ctx, path, bytes.NewReader(jsonBytes(conflict)), available)).To(Succeed())
	var length int
	for turn := 0; turn < 3; turn++ {
		// Exactly ONE raw ledger record, not RestoreOutstanding's later orphan record.
		Expect(RetainProviderOrphans(ctx, path, bytes.NewReader(plain), available)).To(Succeed())
		var state string
		var value []byte
		Expect(s.db.QueryRow(`SELECT state,value FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&state, &value)).To(Succeed())
		Expect(state).To(Equal("outcome_unknown"))
		Expect(string(value)).To(ContainSubstring("prior-turn"))
		Expect(string(value)).To(ContainSubstring("current-turn"))
		Expect(string(value)).To(ContainSubstring("current-observed"))
		Expect(string(value)).To(ContainSubstring("prior-unobserved"))
		Expect(string(value)).To(ContainSubstring(observed))
		Expect(string(value)).To(ContainSubstring("daemon_supplied"))
		for _, h := range hashes {
			Expect(string(value)).To(ContainSubstring(h))
			var pins int
			Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_orphan' AND owner_id=? AND content_hash=?`, a.ID, h).Scan(&pins)).To(Succeed())
			Expect(pins).To(Equal(2))
		}
		if turn > 0 {
			Expect(len(value)).To(Equal(length))
		}
		length = len(value)
	}
})
