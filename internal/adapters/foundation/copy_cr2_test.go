package foundation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	config "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func cr2RestorePair() (service.Selection, string, RestorePlan, map[string]any, RestorePlan) {
	GinkgoHelper()
	ctx := context.Background()
	f := newFixture(GinkgoT(), nil)
	f.publish("source backup", "source")
	input := correctionBackup(f)
	f.close()
	selection := service.Selection{Profile: "cr2", DataDir: filepath.Join(GinkgoT().TempDir(), "destination")}
	_, e := service.Initialize(selection)
	Expect(e).NotTo(HaveOccurred())
	first, e := PlanRestore(ctx, selection, input)
	Expect(e).NotTo(HaveOccurred())
	result, e := ApplyRestore(ctx, selection, first.ID, config.Validate)
	Expect(e).NotTo(HaveOccurred())
	second, e := PlanRestore(ctx, selection, input)
	Expect(e).NotTo(HaveOccurred())
	_, e = ApplyRestore(ctx, selection, second.ID, config.Validate)
	Expect(e).NotTo(HaveOccurred())
	return selection, input, first, result, second
}

var _ = DescribeTable("CR2 G2 natural alias receipt namespace", func(mode string) {
	ctx := context.Background()
	selection, input, first, result, second := cr2RestorePair()
	alias := second.ID
	if mode == "planned" {
		p, e := PlanRestore(ctx, selection, input)
		Expect(e).NotTo(HaveOccurred())
		alias = p.ID
	}
	if mode == "receipt" {
		alias, _ = service.NewID()
		p, e := service.Load(selection)
		Expect(e).NotTo(HaveOccurred())
		s := New(nil)
		_, closer, e := s.Prepare(ctx, p)
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			owner, e := tx.AuthState()
			if e != nil {
				return e
			}
			_, _, e = tx.ReserveOperation(domain.Request{Key: domain.OperationKey{PrincipalID: owner.OwnerID, OperationID: alias}, Method: "POST", Target: "/unrelated", Body: []byte(`{}`)}, nil)
			return e
		})).To(Succeed())
		Expect(closer()).To(Succeed())
	}
	before, e := restoreState(service.Profile{DataDir: selection.DataDir})
	Expect(e).NotTo(HaveOccurred())
	_, e = ApplyRestore(ctx, selection, first.ID, config.Validate, alias)
	Expect(e).To(MatchError(domain.ErrConflict))
	_, e = os.Stat(filepath.Join(selection.DataDir, "runtime", "restore-operation-"+alias+".json"))
	Expect(os.IsNotExist(e)).To(BeTrue())
	after, e := restoreState(service.Profile{DataDir: selection.DataDir})
	Expect(e).NotTo(HaveOccurred())
	Expect(after).To(Equal(before))
	replay, e := ApplyRestore(ctx, selection, first.ID, config.Validate, first.ID)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(result))
}, Entry("completed natural", "completed"), Entry("unexecuted natural", "planned"), Entry("current owner unrelated receipt", "receipt"), Label("persistence"))

var _ = DescribeTable("CR2 G8 visible older record adoption must repeat failed parent sync", func(kind string) {
	ctx := context.Background()
	selection, _, first, result, _ := cr2RestorePair()
	alias, _ := service.NewID()
	failure := errors.New("actual runtime parent sync failure")
	name := "restore-" + kind + "-" + first.ID + ".json"
	if kind == "operation" {
		name = "restore-operation-" + alias + ".json"
	}
	{
		calls := 0
		fault := func(at string) error {
			if at == "record_parent_sync:"+name {
				calls++
				return failure
			}
			return nil
		}
		for i := 0; i < 2; i++ {
			_, e := applyRestore(ctx, selection, first.ID, config.Validate, fault, alias)
			Expect(e).To(MatchError(failure), name)
		}
		Expect(calls).To(Equal(2))
		replay, e := ApplyRestore(ctx, selection, first.ID, config.Validate, alias)
		Expect(e).NotTo(HaveOccurred())
		Expect(replay).To(Equal(result))
	}
}, Entry("alias", "operation"), Entry("terminal", "result"), Label("persistence"))

var _ = It("CR2 G1 damaged prior health rollback terminalizes actual failure and admits a later healthy restore", Label("persistence"), func() {
	ctx := context.Background()
	source := newFixture(GinkgoT(), nil)
	source.publish("healthy backup", "source")
	input := correctionBackup(source)
	source.close()
	prior := newFixture(GinkgoT(), nil)
	prior.publish("preserved prior bytes", "prior")
	selection := prior.selectProfile
	profile := prior.writer.Profile
	prior.close()
	db, e := sql.Open("sqlite", filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	_, e = db.Exec(`DROP INDEX authored_edges_target`)
	Expect(e).NotTo(HaveOccurred())
	Expect(db.Close()).To(Succeed())
	before, e := sqlite.CopyLibrary(ctx, filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	preserved := cr2PriorRows(selection.DataDir)
	plan, e := PlanRestore(ctx, selection, input)
	Expect(e).NotTo(HaveOccurred())
	_, e = applyRestore(ctx, selection, plan.ID, config.Validate, func(at string) error {
		if at == "health" {
			incoming, err := sql.Open("sqlite", filepath.Join(selection.DataDir, "storage", "library.sqlite"))
			if err != nil {
				return err
			}
			_, err = incoming.Exec(`DROP INDEX authored_edges_target`)
			return errors.Join(err, incoming.Close())
		}
		return nil
	})
	Expect(e).To(HaveOccurred())
	var state restoreSwitch
	raw, e := os.ReadFile(filepath.Join(selection.DataDir, "runtime", "restore-active.json"))
	Expect(e).NotTo(HaveOccurred())
	Expect(json.Unmarshal(raw, &state)).To(Succeed())
	Expect(state.Phase).To(Equal("rolled_back"))
	after, e := sqlite.CopyLibrary(ctx, filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	Expect(after).To(Equal(before))
	Expect(cr2PriorRows(selection.DataDir)).To(Equal(preserved))
	current, e := service.Load(selection)
	Expect(e).NotTo(HaveOccurred())
	Expect(current).To(Equal(profile))
	_, _, e = New(nil).Prepare(ctx, current)
	Expect(e).To(HaveOccurred())
	for i := 0; i < 2; i++ {
		_, e = ApplyRestore(ctx, selection, plan.ID, config.Validate)
		Expect(e).To(MatchError("restore_health_failed"))
	}
	next, e := PlanRestore(ctx, selection, input)
	Expect(e).NotTo(HaveOccurred())
	result, e := ApplyRestore(ctx, selection, next.ID, config.Validate)
	Expect(e).NotTo(HaveOccurred())
	Expect(result["state"]).To(Equal("complete"))
	lib, e := sqlite.CopyLibrary(ctx, filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	Expect(lib.RuntimeGeneration).NotTo(Equal(state.OldLibrary.RuntimeGeneration))
	terminal, e := loadRestoreTerminal(current, plan.ID)
	Expect(e).NotTo(HaveOccurred())
	Expect(terminal.Error).To(Equal("restore_health_failed"))
	Expect(strings.Contains(terminal.Result["state"].(string), "rolled_back")).To(BeTrue())
})

var _ = It("CR2 G2 all namespace bindings are preflighted and natural admission cannot follow a foreign alias", Label("persistence"), func() {
	ctx := context.Background()
	selection, input, first, _, second := cr2RestorePair()
	fresh, _ := service.NewID()
	_, e := ApplyRestore(ctx, selection, first.ID, config.Validate, fresh, second.ID)
	Expect(e).To(MatchError(domain.ErrConflict))
	_, e = os.Stat(filepath.Join(selection.DataDir, "runtime", "restore-operation-"+fresh+".json"))
	Expect(os.IsNotExist(e)).To(BeTrue())
	planned, e := PlanRestore(ctx, selection, input)
	Expect(e).NotTo(HaveOccurred())
	// A durable legacy/foreign binding cannot be reused as natural execution.
	Expect(copyJSON(filepath.Join(selection.DataDir, "runtime", "restore-operation-"+planned.ID+".json"), first.ID)).To(Succeed())
	before, e := restoreState(service.Profile{DataDir: selection.DataDir})
	Expect(e).NotTo(HaveOccurred())
	_, e = ApplyRestore(ctx, selection, planned.ID, config.Validate)
	Expect(e).To(MatchError(domain.ErrConflict))
	after, e := restoreState(service.Profile{DataDir: selection.DataDir})
	Expect(e).NotTo(HaveOccurred())
	Expect(after).To(Equal(before))
})

var _ = DescribeTable("CR2 G8 every private restore record adoption sync failure refuses acknowledgement", func(kind string) {
	ctx := context.Background()
	selection, _, first, _, second := cr2RestorePair()
	selected := first.ID
	if kind == "active" {
		selected = second.ID
	}
	name := "restore-" + kind + "-" + selected + ".json"
	if kind == "active" {
		name = "restore-active.json"
	}
	before, e := restoreState(service.Profile{DataDir: selection.DataDir})
	Expect(e).NotTo(HaveOccurred())
	failure := errors.New("private record sync failure")
	calls := 0
	for i := 0; i < 2; i++ {
		_, e = applyRestore(ctx, selection, selected, config.Validate, func(at string) error {
			if at == "record_parent_sync:"+name {
				calls++
				return failure
			}
			return nil
		})
		Expect(e).To(MatchError(failure))
	}
	Expect(calls).To(Equal(2))
	after, e := restoreState(service.Profile{DataDir: selection.DataDir})
	Expect(e).NotTo(HaveOccurred())
	Expect(after).To(Equal(before))
	_, e = ApplyRestore(ctx, selection, selected, config.Validate)
	Expect(e).NotTo(HaveOccurred())
}, Entry("plan", "plan"), Entry("active journal", "active"), Label("persistence"))

var _ = DescribeTable("CR2 G1 compound rollback preserves truthful failure across trial and terminal IO faults", func(phase string) {
	ctx := context.Background()
	source := newFixture(GinkgoT(), nil)
	input := correctionBackup(source)
	source.close()
	prior := newFixture(GinkgoT(), nil)
	prior.publish("prior immutable payload", "prior")
	selection := prior.selectProfile
	prior.close()
	db, e := sql.Open("sqlite", filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	_, e = db.Exec(`DROP INDEX authored_edges_target`)
	Expect(e).NotTo(HaveOccurred())
	Expect(db.Close()).To(Succeed())
	plan, e := PlanRestore(ctx, selection, input)
	Expect(e).NotTo(HaveOccurred())
	failure := errors.New("actual rollback IO failure")
	atPhase := phase
	if phase == "terminal_sync" {
		atPhase = "record_parent_sync:restore-result-" + plan.ID + ".json"
	}
	_, e = applyRestore(ctx, selection, plan.ID, config.Validate, func(at string) error {
		if at == "health" {
			return errors.New("health failed")
		}
		if at == atPhase {
			return failure
		}
		return nil
	})
	Expect(e).To(MatchError(failure))
	before, e := sqlite.CopyLibrary(ctx, filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	for i := 0; i < 2; i++ {
		_, e = ApplyRestore(ctx, selection, plan.ID, config.Validate)
		Expect(e).To(MatchError("restore_health_failed"))
	}
	after, e := sqlite.CopyLibrary(ctx, filepath.Join(selection.DataDir, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	Expect(after).To(Equal(before))
	next, e := PlanRestore(ctx, selection, input)
	Expect(e).NotTo(HaveOccurred())
	_, e = ApplyRestore(ctx, selection, next.ID, config.Validate)
	Expect(e).NotTo(HaveOccurred())
}, Entry("actual trial write boundary", "rollback_trial_record"), Entry("external terminal write boundary", "rollback_terminal"), Entry("external terminal visible unsynced", "terminal_sync"), Label("persistence"))

// Trial failure changes only its own resource/outbox projection. These original
// authoritative rows (including credentials and operation identities) stay exact.
func cr2PriorRows(root string) map[string][]string {
	GinkgoHelper()
	db, e := sql.Open("sqlite", filepath.Join(root, "storage", "library.sqlite"))
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	out := map[string][]string{}
	for _, table := range []string{"libraries", "documents", "document_versions", "version_entries", "annotations", "annotation_history", "auth_owner", "device_sessions", "operation_receipts", "config_revisions"} {
		rows, e := db.Query("SELECT * FROM " + table)
		Expect(e).NotTo(HaveOccurred())
		columns, e := rows.Columns()
		Expect(e).NotTo(HaveOccurred())
		values := []string{}
		for rows.Next() {
			cells := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range cells {
				pointers[i] = &cells[i]
			}
			Expect(rows.Scan(pointers...)).To(Succeed())
			raw, e := json.Marshal(cells)
			Expect(e).NotTo(HaveOccurred())
			values = append(values, string(raw))
		}
		Expect(rows.Err()).NotTo(HaveOccurred())
		Expect(rows.Close()).To(Succeed())
		sort.Strings(values)
		out[table] = values
	}
	return out
}

var _ = It("CR2 G2 current receipt uncertainty is not negative knowledge and other principals retain their namespaces", Label("persistence"), func() {
	ctx := context.Background()
	selection, _, first, result, _ := cr2RestorePair()
	uncertain, _ := service.NewID()
	other, _ := service.NewID()
	p, e := service.Load(selection)
	Expect(e).NotTo(HaveOccurred())
	s := New(nil)
	_, closer, e := s.Prepare(ctx, p)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		for _, r := range []domain.Request{{Key: domain.OperationKey{PrincipalID: owner.OwnerID, OperationID: uncertain}, Method: "POST", Target: "/local/v1/restore/" + first.ID, Body: []byte(`{}`)}, {Key: domain.OperationKey{PrincipalID: "other-principal", OperationID: other}, Method: "POST", Target: "/unrelated", Body: []byte(`{}`)}} {
			if _, _, e = tx.ReserveOperation(r, nil); e != nil {
				return e
			}
		}
		return nil
	})).To(Succeed())
	Expect(closer()).To(Succeed())
	_, e = ApplyRestore(ctx, selection, first.ID, config.Validate, uncertain)
	Expect(e).To(MatchError(domain.ErrReconciliation))
	_, e = os.Stat(filepath.Join(selection.DataDir, "runtime", "restore-operation-"+uncertain+".json"))
	Expect(os.IsNotExist(e)).To(BeTrue())
	replay, e := ApplyRestore(ctx, selection, first.ID, config.Validate, other)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(result))
})

var _ = DescribeTable("CR2 G2 incoming owner receipts refuse conflicting aliases before any switch or alias publication", func(mode string) {
	ctx := context.Background()
	source := newFixture(GinkgoT(), nil)
	alias, _ := service.NewID()
	var original domain.Receipt
	Expect(source.s.Write(ctx, func(tx app.WriteTx) error {
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		req := domain.Request{Key: domain.OperationKey{PrincipalID: owner.OwnerID, OperationID: alias}, Method: "POST", Target: "/unrelated/source-work", Body: []byte(`{}`)}
		if _, _, e = tx.ReserveOperation(req, nil); e != nil {
			return e
		}
		if e = tx.CompleteOperation(req.Key, domain.Response{Status: 200, Body: []byte(`{"source":"receipt"}`)}); e != nil {
			return e
		}
		original, e = tx.Receipt(req.Key)
		return e
	})).To(Succeed())
	source.publish("source original", "source")
	input := correctionBackup(source)
	source.close()
	prior := newFixture(GinkgoT(), nil)
	prior.publish("prior retained", "prior")
	selection := prior.selectProfile
	profile := prior.writer.Profile
	prior.close()
	before := cr2PriorRows(selection.DataDir)
	plan, e := PlanRestore(ctx, selection, input)
	Expect(e).NotTo(HaveOccurred())
	if mode == "switching" {
		interrupted := errors.New("before physical switch")
		_, e = applyRestore(ctx, selection, plan.ID, config.Validate, func(at string) error {
			if at == "switch_record" {
				return interrupted
			}
			return nil
		})
		Expect(e).To(MatchError(interrupted))
	}
	_, e = ApplyRestore(ctx, selection, plan.ID, config.Validate, alias)
	Expect(e).To(MatchError(domain.ErrConflict))
	_, e = os.Stat(filepath.Join(selection.DataDir, "runtime", "restore-operation-"+alias+".json"))
	Expect(os.IsNotExist(e)).To(BeTrue())
	current, e := service.Load(selection)
	Expect(e).NotTo(HaveOccurred())
	Expect(current).To(Equal(profile))
	Expect(cr2PriorRows(selection.DataDir)).To(Equal(before))
	// No abandoned switch, replacement ID, lost receipt or rollback of healthy
	// committing work is needed: the same natural plan can proceed without X.
	result, e := ApplyRestore(ctx, selection, plan.ID, config.Validate)
	Expect(e).NotTo(HaveOccurred())
	Expect(result["state"]).To(Equal("complete"))
	p, e := service.Load(selection)
	Expect(e).NotTo(HaveOccurred())
	s := New(nil)
	_, closer, e := s.Prepare(ctx, p)
	Expect(e).NotTo(HaveOccurred())
	defer closer()
	retained, e := s.Receipt(ctx, original.Key)
	Expect(e).NotTo(HaveOccurred())
	Expect(retained).To(Equal(original))
}, Entry("new application", "new"), Entry("owned staged incoming after interruption", "switching"), Label("persistence"))
