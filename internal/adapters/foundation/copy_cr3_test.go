package foundation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func cr3Receipt(f *fixture, ids ...string) {
	GinkgoHelper()
	Expect(f.s.Write(context.Background(), func(tx app.WriteTx) error {
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		for _, id := range ids {
			r := domain.Request{Key: domain.OperationKey{PrincipalID: owner.OwnerID, OperationID: id}, Method: "POST", Target: "/unrelated/cr3", Body: []byte(`{}`)}
			if _, _, e = tx.ReserveOperation(r, nil); e != nil {
				return e
			}
			if e = tx.CompleteOperation(r.Key, domain.Response{Status: 200, Body: []byte(`{"preserved":true}`)}); e != nil {
				return e
			}
		}
		return nil
	})).To(Succeed())
}

func cr3State(selection service.Selection) restoreSwitch {
	GinkgoHelper()
	raw, e := os.ReadFile(filepath.Join(selection.DataDir, "runtime", "restore-active.json"))
	Expect(e).NotTo(HaveOccurred())
	var state restoreSwitch
	Expect(json.Unmarshal(raw, &state)).To(Succeed())
	return state
}

func cr3PlanWrite(selection service.Selection, plan RestorePlan) {
	GinkgoHelper()
	Expect(copyJSON(filepath.Join(selection.DataDir, "runtime", "restore-plan-"+plan.ID+".json"), plan)).To(Succeed())
}

var _ = DescribeTable("CR3 G2 every owned physical side fences new aliases", func(phase string) {
	ctx := context.Background()
	source := newFixture(GinkgoT(), nil)
	source.publish("snapshot", "snapshot")
	input := correctionBackup(source)
	alias, _ := service.NewID()
	fresh, _ := service.NewID()
	// X was committed AFTER the snapshot and survives only in the prior DB.
	cr3Receipt(source, alias)
	selection := source.selectProfile
	source.close()
	plan, e := PlanRestore(ctx, selection, input)
	Expect(e).NotTo(HaveOccurred())
	interrupted := errors.New("CR3 owned-side interruption")
	_, e = applyRestore(ctx, selection, plan.ID, config.Validate, func(at string) error {
		if at == "health" && len(phase) >= 8 && phase[:8] == "rollback" {
			return errors.New("force rollback")
		}
		if at == phase || phase == "committing" && at == "terminal_record" {
			return interrupted
		}
		return nil
	})
	if phase == "complete" {
		Expect(e).NotTo(HaveOccurred())
	} else {
		Expect(e).To(HaveOccurred())
	}
	before := cr3State(selection)
	if phase == "committing" {
		Expect(before.Phase).To(Equal("committing"))
	}
	_, e = ApplyRestore(ctx, selection, plan.ID, config.Validate, fresh, alias)
	Expect(e).To(MatchError(domain.ErrConflict))
	for _, id := range []string{fresh, alias} {
		_, e = os.Stat(filepath.Join(selection.DataDir, "runtime", "restore-operation-"+id+".json"))
		Expect(os.IsNotExist(e)).To(BeTrue())
	}
	Expect(cr3State(selection)).To(Equal(before))
	result, e := ApplyRestore(ctx, selection, plan.ID, config.Validate)
	if len(phase) >= 8 && phase[:8] == "rollback" {
		Expect(e).To(MatchError("restore_health_failed"))
		Expect(cr3State(selection).Phase).To(Equal("rolled_back"))
	} else {
		Expect(e).NotTo(HaveOccurred())
		Expect(result["state"]).To(Equal("complete"))
	}
}, Entry("before moves", "switch_record"), Entry("old moved", "old_renamed"), Entry("incoming active", "new_renamed"), Entry("profile bound", "profile_bound"), Entry("committing", "committing"), Entry("latest completed alias", "complete"), Entry("rollback incoming active", "rollback_move_incoming"), Entry("rollback incoming saved", "rollback_incoming_moved"), Entry("rollback prior absent", "rollback_move_prior"), Entry("rollback prior active", "rollback_prior_moved"), Entry("rollback profile", "rollback_profile_restored"), Label("persistence"))

var _ = DescribeTable("CR3 G2 terminal replay ignores later dataset collisions", func(failed bool) {
	ctx := context.Background()
	source := newFixture(GinkgoT(), nil)
	input := correctionBackup(source)
	source.close()
	selection := service.Selection{Profile: "cr3-replay", DataDir: filepath.Join(GinkgoT().TempDir(), "destination")}
	_, e := service.Initialize(selection)
	Expect(e).NotTo(HaveOccurred())
	first, e := PlanRestore(ctx, selection, input)
	Expect(e).NotTo(HaveOccurred())
	alias, _ := service.NewID()
	newAlias, _ := service.NewID()
	result, firstErr := applyRestore(ctx, selection, first.ID, config.Validate, func(at string) error {
		if failed && at == "health" {
			return errors.New("actual health boundary refusal")
		}
		return nil
	}, alias)
	if failed {
		Expect(firstErr).To(HaveOccurred())
	} else {
		Expect(firstErr).NotTo(HaveOccurred())
	}
	other := newFixture(GinkgoT(), nil)
	// A different library and principal legitimately own these unrelated keys.
	cr3Receipt(other, first.ID, alias, newAlias)
	laterInput := correctionBackup(other)
	other.close()
	later, e := PlanRestore(ctx, selection, laterInput)
	Expect(e).NotTo(HaveOccurred())
	_, e = ApplyRestore(ctx, selection, later.ID, config.Validate)
	Expect(e).NotTo(HaveOccurred())
	before := cr2PriorRows(selection.DataDir)
	trialBefore := cr3TrialRows(selection.DataDir)
	first.ExpiresAt = "2000-01-01T00:00:00Z"
	first.PrivateInput = filepath.Join(GinkgoT().TempDir(), "unavailable")
	cr3PlanWrite(selection, first)
	for _, aliases := range [][]string{nil, {alias}} {
		again, e := ApplyRestore(ctx, selection, first.ID, config.Validate, aliases...)
		if failed {
			Expect(e).To(MatchError("restore_health_failed"))
		} else {
			Expect(e).NotTo(HaveOccurred())
			Expect(again).To(Equal(result))
		}
	}
	Expect(cr2PriorRows(selection.DataDir)).To(Equal(before))
	Expect(cr3TrialRows(selection.DataDir)).To(Equal(trialBefore))
	_, e = ApplyRestore(ctx, selection, first.ID, config.Validate, newAlias)
	Expect(e).To(MatchError(domain.ErrConflict))
	_, e = os.Stat(filepath.Join(selection.DataDir, "runtime", "restore-operation-"+newAlias+".json"))
	Expect(os.IsNotExist(e)).To(BeTrue())
	// Already-bound historical replay also requires no readable later database.
	db := filepath.Join(selection.DataDir, "storage", "library.sqlite")
	Expect(os.Rename(db, db+".fixture-held")).To(Succeed())
	defer func() { Expect(os.Rename(db+".fixture-held", db)).To(Succeed()) }()
	_, e = ApplyRestore(ctx, selection, first.ID, config.Validate, alias)
	if failed {
		Expect(e).To(MatchError("restore_health_failed"))
	} else {
		Expect(e).NotTo(HaveOccurred())
	}
}, Entry("success", false), Entry("rolled-back failure", true), Label("persistence"))

var _ = DescribeTable("CR3 G1 admitted staging survives preview TTL with original guards", func(phase, change string) {
	ctx := context.Background()
	source := newFixture(GinkgoT(), nil)
	input := correctionBackup(source)
	source.close()
	prior := newFixture(GinkgoT(), nil)
	selection := prior.selectProfile
	prior.close()
	plan, e := PlanRestore(ctx, selection, input)
	Expect(e).NotTo(HaveOccurred())
	interrupted := errors.New("admitted staging interruption")
	_, e = applyRestore(ctx, selection, plan.ID, config.Validate, func(at string) error {
		if at == phase {
			return interrupted
		}
		return nil
	})
	Expect(e).To(MatchError(interrupted))
	state := cr3State(selection)
	Expect(state.Phase).To(Equal("staging"))
	plan.ExpiresAt = "2000-01-01T00:00:00Z"
	switch change {
	case "input":
		plan.InputSHA = "changed"
	case "state":
		plan.StateSHA = "changed"
	case "journal":
		state.Input = filepath.Join(GinkgoT().TempDir(), "foreign")
		Expect(copyJSON(filepath.Join(selection.DataDir, "runtime", "restore-active.json"), state)).To(Succeed())
	}
	cr3PlanWrite(selection, plan)
	result, e := ApplyRestore(ctx, selection, plan.ID, config.Validate)
	if change != "" {
		Expect(e).To(HaveOccurred())
		return
	}
	Expect(e).NotTo(HaveOccurred())
	Expect(result["state"]).To(Equal("complete"))
	lib, e := sqlite.CopyLibrary(ctx, filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	again, e := ApplyRestore(ctx, selection, plan.ID, config.Validate)
	Expect(e).NotTo(HaveOccurred())
	Expect(again).To(Equal(result))
	after, e := sqlite.CopyLibrary(ctx, filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	Expect(after).To(Equal(lib))
}, Entry("staged bytes after expiry", "staged_bytes", ""), Entry("protected database after expiry", "protected_database", ""), Entry("changed input", "staged_bytes", "input"), Entry("changed destination", "staged_bytes", "state"), Entry("foreign staging journal", "staged_bytes", "journal"), Label("persistence"))

var _ = DescribeTable("CR3 G2 rollback incoming namespace remains owned at failed path", func(phase string) {
	ctx := context.Background()
	source := newFixture(GinkgoT(), nil)
	alias, _ := service.NewID()
	cr3Receipt(source, alias)
	input := correctionBackup(source)
	source.close()
	prior := newFixture(GinkgoT(), nil)
	selection := prior.selectProfile
	prior.close()
	plan, e := PlanRestore(ctx, selection, input)
	Expect(e).NotTo(HaveOccurred())
	_, e = applyRestore(ctx, selection, plan.ID, config.Validate, func(at string) error {
		if at == "health" {
			return errors.New("health failed")
		}
		if at == phase {
			return errors.New("rollback interrupted")
		}
		return nil
	})
	Expect(e).To(HaveOccurred())
	before := cr3State(selection)
	_, e = ApplyRestore(ctx, selection, plan.ID, config.Validate, alias)
	Expect(e).To(MatchError(domain.ErrConflict))
	Expect(cr3State(selection)).To(Equal(before))
	_, e = os.Stat(filepath.Join(selection.DataDir, "runtime", "restore-operation-"+alias+".json"))
	Expect(os.IsNotExist(e)).To(BeTrue())
	_, e = ApplyRestore(ctx, selection, plan.ID, config.Validate)
	Expect(e).To(MatchError("restore_health_failed"))
}, Entry("incoming saved", "rollback_incoming_moved"), Entry("prior restored", "rollback_prior_moved"), Entry("profile restored", "rollback_profile_restored"), Label("persistence"))

var _ = DescribeTable("CR3 G2 owned role and profile identity refuse foreign adoption", func(mode string) {
	ctx := context.Background()
	source := newFixture(GinkgoT(), nil)
	input := correctionBackup(source)
	selection := source.selectProfile
	source.close()
	plan, e := PlanRestore(ctx, selection, input)
	Expect(e).NotTo(HaveOccurred())
	interrupted := errors.New("before profile switch")
	_, e = applyRestore(ctx, selection, plan.ID, config.Validate, func(at string) error {
		if at == "new_renamed" {
			return interrupted
		}
		return nil
	})
	Expect(e).To(MatchError(interrupted))
	state := cr3State(selection)
	if mode == "profile" {
		profile := state.OldProfile
		profile.ControlKey = strings.Repeat("ab", 32)
		Expect(copyJSON(filepath.Join(selection.DataDir, "profile.json"), profile)).To(Succeed())
	} else {
		file := filepath.Join(selection.DataDir, "storage", "library.sqlite")
		if mode == "prior" {
			file = filepath.Join(state.Rollback, "library.sqlite")
		}
		db, e := sql.Open("sqlite", file)
		Expect(e).NotTo(HaveOccurred())
		_, e = db.Exec(`UPDATE libraries SET runtime_generation='foreign-runtime'`)
		Expect(e).NotTo(HaveOccurred())
		Expect(db.Close()).To(Succeed())
	}
	fresh, _ := service.NewID()
	_, e = ApplyRestore(ctx, selection, plan.ID, config.Validate, fresh)
	Expect(e).To(MatchError(domain.ErrContentIntegrity))
	_, e = os.Stat(filepath.Join(selection.DataDir, "runtime", "restore-operation-"+fresh+".json"))
	Expect(os.IsNotExist(e)).To(BeTrue())
	Expect(cr3State(selection)).To(Equal(state))
}, Entry("prior identity", "prior"), Entry("incoming identity", "incoming"), Entry("profile identity", "profile"), Label("persistence"))

func cr3TrialRows(data string) []string {
	GinkgoHelper()
	db, e := sql.Open("sqlite", filepath.Join(data, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	rows, e := db.Query(`SELECT json_object('kind',resource_kind,'id',resource_id,'revision',revision,'value',json(value)) FROM resource_revisions WHERE resource_kind='backup_trial_restore' ORDER BY resource_id`)
	Expect(e).NotTo(HaveOccurred())
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		Expect(rows.Scan(&value)).To(Succeed())
		values = append(values, value)
	}
	Expect(rows.Err()).NotTo(HaveOccurred())
	Expect(values).NotTo(BeEmpty())
	return values
}
