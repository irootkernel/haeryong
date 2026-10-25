package foundation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/localcontrol"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	"haeryong.local/haeryong/internal/application/auth"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/recovery"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var _ = It("Copies protected restore resumes each durable switch boundary and rolls back health failure with matched identity", Label("persistence"), func() {
	ctx := context.Background()
	f := newFixture(GinkgoT(), nil)
	a := auth.New(f.s)
	pair, e := a.CreatePairing(ctx, "http://127.0.0.1:9270", "backup owner", "copy-test")
	Expect(e).NotTo(HaveOccurred())
	operation, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	_, e = a.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: operation, Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	session, e := a.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(e).NotTo(HaveOccurred())
	f.publish("exact held immutable source bytes", "held-source")
	destination := filepath.Join(GinkgoT().TempDir(), "backups")
	operation, e = service.NewID()
	Expect(e).NotTo(HaveOccurred())
	response, e := recovery.New(f.s, a, f.s).Submit(ctx, session.Token, operation, domain.CopyRequest{Action: "backup.create", Destination: destination})
	Expect(e).NotTo(HaveOccurred())
	var accepted domain.CopyResult
	Expect(json.Unmarshal(response.Body, &accepted)).To(Succeed())
	done := false
	for n := 0; n < 100; n++ {
		Expect(f.s.ProcessCopy(ctx, accepted.JobID)).To(Succeed())
		job, e := f.s.Job(ctx, accepted.JobID)
		Expect(e).NotTo(HaveOccurred())
		if job.State == "completed" {
			done = true
			break
		}
	}
	Expect(done).To(BeTrue())
	input := filepath.Join(destination, accepted.ID)
	for _, phase := range []string{"staged_bytes", "protected_database", "switch_record", "old_renamed", "new_renamed", "profile_bound", "health", "destination_ddl"} {
		selection := service.Selection{Profile: "restore-test", DataDir: filepath.Join(GinkgoT().TempDir(), "destination")}
		prior, e := service.Initialize(selection)
		Expect(e).NotTo(HaveOccurred())
		if phase == "health" || phase == "destination_ddl" {
			w, e := service.OpenWriter(selection)
			Expect(e).NotTo(HaveOccurred())
			Expect(w.PrepareStorageBootstrap(nil)).To(Succeed())
			old := New(nil)
			_, closeOld, e := old.Prepare(ctx, w.Profile)
			Expect(e).NotTo(HaveOccurred())
			Expect(w.MarkStorageReady(nil)).To(Succeed())
			Expect(closeOld()).To(Succeed())
			Expect(w.Close()).To(Succeed())
			if phase == "destination_ddl" {
				db, e := sql.Open("sqlite", filepath.Join(selection.DataDir, "storage", "library.sqlite"))
				Expect(e).NotTo(HaveOccurred())
				_, e = db.Exec(`DROP INDEX authored_edges_target`)
				Expect(e).NotTo(HaveOccurred())
				Expect(db.Close()).To(Succeed())
				p, e := service.Load(selection)
				Expect(e).NotTo(HaveOccurred())
				_, _, e = New(nil).Prepare(ctx, p)
				Expect(e).To(HaveOccurred())
			}
		}
		plan, e := PlanRestore(ctx, selection, input)
		Expect(e).NotTo(HaveOccurred())
		if phase == "destination_ddl" {
			result, e := ApplyRestore(ctx, selection, plan.ID, config.Validate)
			Expect(e).NotTo(HaveOccurred())
			Expect(result["state"]).To(Equal("complete"))
			continue
		}
		injected := errors.New("simulated process/IO interruption at " + phase)
		_, e = applyRestore(ctx, selection, plan.ID, config.Validate, func(at string) error {
			if at == phase {
				return injected
			}
			return nil
		})
		Expect(e).To(MatchError(injected))
		if phase == "health" {
			current, e := service.Load(selection)
			Expect(e).NotTo(HaveOccurred())
			Expect(current.LibraryID).To(Equal(prior.LibraryID))
			Expect(current.ControlKey).To(Equal(prior.ControlKey))
			old := New(nil)
			_, closeOld, e := old.Prepare(ctx, current)
			Expect(e).NotTo(HaveOccurred())
			status, e := old.BackupStatus(ctx)
			Expect(e).NotTo(HaveOccurred())
			Expect(status.LastTrial.State).To(Equal("failed"))
			Expect(closeOld()).To(Succeed())
			// The failed new bytes remain recoverable, never a readiness substitute.
			_, e = os.Stat(filepath.Join(selection.DataDir, ".restore-"+plan.ID+".failed"))
			Expect(e).NotTo(HaveOccurred())
			// CR1 G1: reproduce the old durable combination after profile rollback
			// but before the rolled_back journal write reached storage.
			journal := filepath.Join(selection.DataDir, "runtime", "restore-active.json")
			var interrupted restoreSwitch
			raw, err := os.ReadFile(journal)
			Expect(err).NotTo(HaveOccurred())
			Expect(json.Unmarshal(raw, &interrupted)).To(Succeed())
			interrupted.Phase = "health"
			Expect(copyJSON(journal, interrupted)).To(Succeed())
			_, e = ApplyRestore(ctx, selection, plan.ID, config.Validate)
			Expect(e).To(HaveOccurred(), "prior generation must never become incoming restore success")
			continue
		}
		// Exercise the real normal-startup path, not just its marker predicate.
		startupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		ready := false
		refusal := (service.Runner{Selection: selection, Port: service.DefaultPort, Storage: New(nil), Endpoint: &localcontrol.HTTP{}}).Run(startupCtx, func(service.Identity) { ready = true })
		cancel()
		Expect(refusal).To(HaveOccurred())
		Expect(ready).To(BeFalse())
		if phase != "old_renamed" {
			Expect(strings.Contains(refusal.Error(), "protected restore incomplete")).To(BeTrue(), phase)
		}
		resumed, e := ApplyRestore(ctx, selection, plan.ID, config.Validate)
		Expect(e).NotTo(HaveOccurred())
		Expect(resumed["state"]).To(Equal("complete"))
		again, e := ApplyRestore(ctx, selection, plan.ID, config.Validate)
		Expect(e).NotTo(HaveOccurred())
		Expect(again).To(Equal(resumed))
		p, e := service.Load(selection)
		Expect(e).NotTo(HaveOccurred())
		probe := New(nil)
		_, closeStore, e := probe.Prepare(ctx, p)
		Expect(e).NotTo(HaveOccurred())
		Expect(closeStore()).To(Succeed())
	}
})

var _ = It("Copies restore expired unexecuted plans fail while completed replay ignores expired changed or unavailable input and aliases cannot retarget", Label("persistence"), func() {
	ctx := context.Background()
	f := newFixture(GinkgoT(), nil)
	a := auth.New(f.s)
	pair, e := a.CreatePairing(ctx, "http://127.0.0.1:9270", "restore classes", "fixture")
	Expect(e).NotTo(HaveOccurred())
	operation, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	_, e = a.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: operation, Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	session, e := a.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(e).NotTo(HaveOccurred())
	f.publish("restore class bytes", "class-object")
	operation, e = service.NewID()
	Expect(e).NotTo(HaveOccurred())
	response, e := recovery.New(f.s, a, f.s).Submit(ctx, session.Token, operation, domain.CopyRequest{Action: "backup.create", Destination: filepath.Join(GinkgoT().TempDir(), "backup")})
	Expect(e).NotTo(HaveOccurred())
	var accepted domain.CopyResult
	Expect(json.Unmarshal(response.Body, &accepted)).To(Succeed())
	for n := 0; n < 100; n++ {
		Expect(f.s.ProcessCopy(ctx, accepted.JobID)).To(Succeed())
		j, e := f.s.Job(ctx, accepted.JobID)
		Expect(e).NotTo(HaveOccurred())
		if j.State == "completed" {
			break
		}
	}
	var set domain.CopySet
	Expect(f.s.Write(ctx, func(tx app.WriteTx) error { var e error; set, e = tx.CopySet(accepted.ID); return e })).To(Succeed())
	selection := service.Selection{Profile: "restore-classes", DataDir: filepath.Join(GinkgoT().TempDir(), "destination")}
	_, e = service.Initialize(selection)
	Expect(e).NotTo(HaveOccurred())
	held, e := service.OpenWriter(selection)
	Expect(e).NotTo(HaveOccurred())
	_, e = PlanRestore(ctx, selection, set.Directory)
	Expect(e).To(MatchError(service.ErrLocked))
	Expect(held.Close()).To(Succeed())
	for _, mode := range []string{"expired", "invalid_expiry", "profile", "state", "input"} {
		plan, e := PlanRestore(ctx, selection, set.Directory)
		Expect(e).NotTo(HaveOccurred())
		switch mode {
		case "expired":
			plan.ExpiresAt = "2000-01-01T00:00:00Z"
		case "invalid_expiry":
			plan.ExpiresAt = "invalid"
		case "profile":
			plan.DestinationProfile = "foreign"
		case "state":
			plan.StateSHA = "changed"
		case "input":
			plan.InputSHA = "changed"
		}
		Expect(copyJSON(filepath.Join(selection.DataDir, "runtime", "restore-plan-"+plan.ID+".json"), plan)).To(Succeed())
		_, e = ApplyRestore(ctx, selection, plan.ID, config.Validate)
		Expect(e).To(HaveOccurred(), mode)
		_, e = os.Stat(filepath.Join(selection.DataDir, "runtime", "restore-active.json"))
		Expect(os.IsNotExist(e)).To(BeTrue())
	}
	plan, e := PlanRestore(ctx, selection, set.Directory)
	Expect(e).NotTo(HaveOccurred())
	alias, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	result, e := ApplyRestore(ctx, selection, plan.ID, config.Validate, alias)
	Expect(e).NotTo(HaveOccurred())
	// CR1 G2: a later successful restore must not erase an earlier terminal result.
	later, e := PlanRestore(ctx, selection, set.Directory)
	Expect(e).NotTo(HaveOccurred())
	_, e = ApplyRestore(ctx, selection, later.ID, config.Validate)
	Expect(e).NotTo(HaveOccurred())
	newer, e := sqlite.CopyLibrary(ctx, filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	plan.ExpiresAt = "2000-01-01T00:00:00Z"
	plan.StateSHA = "changed-after-completion"
	plan.PrivateInput = filepath.Join(GinkgoT().TempDir(), "unavailable")
	Expect(copyJSON(filepath.Join(selection.DataDir, "runtime", "restore-plan-"+plan.ID+".json"), plan)).To(Succeed())
	again, e := ApplyRestore(ctx, selection, plan.ID, config.Validate, alias)
	Expect(e).NotTo(HaveOccurred())
	Expect(again).To(Equal(result))
	newAlias, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	for _, aliases := range [][]string{nil, {newAlias}} {
		replay, e := ApplyRestore(ctx, selection, plan.ID, config.Validate, aliases...)
		Expect(e).NotTo(HaveOccurred())
		Expect(replay).To(Equal(result))
	}
	unchanged, e := sqlite.CopyLibrary(ctx, filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	Expect(unchanged).To(Equal(newer))
	conflict, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	Expect(copyJSON(filepath.Join(selection.DataDir, "runtime", "restore-operation-"+conflict+".json"), "another-plan")).To(Succeed())
	_, e = ApplyRestore(ctx, selection, plan.ID, config.Validate, conflict)
	Expect(e).To(MatchError(domain.ErrConflict))
})
