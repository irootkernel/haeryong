package foundation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	"haeryong.local/haeryong/internal/application/auth"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/recovery"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
)

func correctionBackup(f *fixture) string {
	GinkgoHelper()
	ctx := context.Background()
	a := auth.New(f.s)
	pair, e := a.CreatePairing(ctx, "http://127.0.0.1:9270", "correction owner", "fixture")
	Expect(e).NotTo(HaveOccurred())
	id, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	_, e = a.BootstrapDecide(ctx, domain.PairingDecision{ID: pair.ID, Code: pair.Code, Origin: pair.Origin, ETag: pair.ETag, OperationID: id, Action: "approve"})
	Expect(e).NotTo(HaveOccurred())
	token, e := a.Poll(ctx, pair.ID, pair.PollingSecret, pair.Origin)
	Expect(e).NotTo(HaveOccurred())
	id, e = service.NewID()
	Expect(e).NotTo(HaveOccurred())
	destination := filepath.Join(GinkgoT().TempDir(), "backup")
	r, e := recovery.New(f.s, a, f.s).Submit(ctx, token.Token, id, domain.CopyRequest{Action: "backup.create", Destination: destination})
	Expect(e).NotTo(HaveOccurred())
	var accepted domain.CopyResult
	Expect(json.Unmarshal(r.Body, &accepted)).To(Succeed())
	for i := 0; i < 300; i++ {
		Expect(f.s.ProcessCopy(ctx, accepted.JobID)).To(Succeed())
		j, e := f.s.Job(ctx, accepted.JobID)
		Expect(e).NotTo(HaveOccurred())
		if j.State == "completed" {
			return filepath.Join(destination, accepted.ID)
		}
	}
	Fail("backup did not complete")
	return ""
}

var _ = It("CR1 G1 rollback direction survives every actual interrupted move and profile boundary", Label("persistence"), func() {
	ctx := context.Background()
	for _, same := range []bool{false, true} {
		for _, phase := range []string{"rollback_move_incoming", "rollback_incoming_moved", "rollback_move_prior", "rollback_prior_moved", "rollback_profile_write", "rollback_profile_restored", "rollback_terminal"} {
			source := newFixture(GinkgoT(), nil)
			source.publish("backup bytes", "source")
			input := correctionBackup(source)
			prior := source
			if !same {
				prior = newFixture(GinkgoT(), nil)
			}
			prior.publish("populated prior bytes absent from backup", "prior")
			before, e := prior.s.Health(ctx)
			Expect(e).NotTo(HaveOccurred())
			profile := prior.writer.Profile
			selection := prior.selectProfile
			source.close()
			if !same {
				prior.close()
			}
			plan, e := PlanRestore(ctx, selection, input)
			Expect(e).NotTo(HaveOccurred())
			interrupted := errors.New("rollback interruption " + phase)
			health := errors.New("actual health failure")
			_, e = applyRestore(ctx, selection, plan.ID, config.Validate, func(at string) error {
				if at == "health" {
					return health
				}
				if at == phase {
					return interrupted
				}
				return nil
			})
			Expect(e).To(MatchError(interrupted))
			owner, e := service.OpenRecoveryWriter(selection)
			Expect(e).NotTo(HaveOccurred())
			Expect(owner.CheckRecoverySwitch()).To(HaveOccurred())
			Expect(owner.Close()).To(Succeed())
			for retry := 0; retry < 2; retry++ {
				_, e = ApplyRestore(ctx, selection, plan.ID, config.Validate)
				Expect(e).To(MatchError("restore_health_failed"))
			}
			after, e := service.Load(selection)
			Expect(e).NotTo(HaveOccurred())
			Expect(after).To(Equal(profile))
			lib, e := sqlite.CopyLibrary(ctx, filepath.Join(selection.DataDir, "storage", "library.sqlite"))
			Expect(e).NotTo(HaveOccurred())
			Expect(lib).To(Equal(before.Library))
			probe := New(nil)
			_, closeStore, e := probe.Prepare(ctx, after)
			Expect(e).NotTo(HaveOccurred())
			status, e := probe.BackupStatus(ctx)
			Expect(e).NotTo(HaveOccurred())
			if !same {
				Expect(status.LastTrial.State).To(Equal("failed"))
			} else {
				Expect(status.LastTrial.State).To(Equal("not_run"))
			}
			Expect(closeStore()).To(Succeed())
			_, e = os.Stat(filepath.Join(selection.DataDir, ".restore-"+plan.ID+".failed"))
			Expect(e).NotTo(HaveOccurred())
		}
	}
})

var _ = It("CR1 G2 completion-record failure retries without rollback or another rotation", Label("persistence"), func() {
	ctx := context.Background()
	f := newFixture(GinkgoT(), nil)
	f.publish("exact backup bytes", "source")
	input := correctionBackup(f)
	f.close()
	selection := service.Selection{Profile: "terminal", DataDir: filepath.Join(GinkgoT().TempDir(), "destination")}
	_, e := service.Initialize(selection)
	Expect(e).NotTo(HaveOccurred())
	plan, e := PlanRestore(ctx, selection, input)
	Expect(e).NotTo(HaveOccurred())
	failure := errors.New("terminal record unavailable")
	_, e = applyRestore(ctx, selection, plan.ID, config.Validate, func(at string) error {
		if at == "terminal_record" {
			return failure
		}
		return nil
	})
	Expect(e).To(MatchError(failure))
	before, e := sqlite.CopyLibrary(ctx, filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	first, e := ApplyRestore(ctx, selection, plan.ID, config.Validate)
	Expect(e).NotTo(HaveOccurred())
	second, e := ApplyRestore(ctx, selection, plan.ID, config.Validate)
	Expect(e).NotTo(HaveOccurred())
	Expect(second).To(Equal(first))
	after, e := sqlite.CopyLibrary(ctx, filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	Expect(after).To(Equal(before))
	_, e = os.Stat(filepath.Join(selection.DataDir, ".restore-"+plan.ID+".failed"))
	Expect(os.IsNotExist(e)).To(BeTrue())
})
