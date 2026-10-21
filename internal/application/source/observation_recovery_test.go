package source_test

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsource "haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// observationRecoveryState stages one observation intent with its receipt
// and rows directly through the storage port: recovery states the atomic
// commit never produces (plan r4 §7.3 step 5, G6).
type observationRecoveryState struct {
	receipt  string   // "", "accepted", "completed", "failed" or "reconciling"
	rows     []string // per-source rows, inserted in this order
	intentID string
}

func (f *observationFixture) stageRecovery(t GinkgoTInterface, key string, state observationRecoveryState) {
	t.Helper()
	owner := ownerPrincipal(&f.vFixture)
	operation := domain.OperationKey{PrincipalID: owner, OperationID: key}
	route := "/api/v1/versions/" + f.version + "/source-observations"
	ExpectWithOffset(1, f.store.Write(context.Background(), func(tx app.WriteTx) error {
		if state.receipt != "" {
			if _, _, e := tx.ReserveOperation(domain.Request{Key: operation, Method: "POST", Target: route, Body: []byte(`{"scope":{"kind":"version"}}`)}, nil); e != nil {
				return e
			}
			switch state.receipt {
			case "completed":
				if e := tx.CompleteOperation(operation, domain.Response{Status: 201, Body: []byte(`{}`)}); e != nil {
					return e
				}
			case "failed":
				if e := tx.CompleteOperation(operation, domain.Response{Status: 412, Body: []byte(`{}`)}); e != nil {
					return e
				}
			case "reconciling":
				if e := tx.ReconcileOperation(operation); e != nil {
					return e
				}
			}
		}
		if len(state.rows) > 0 {
			run := domain.ObservationRun{ID: "4a000000-0000-4000-8000-0000000000" + key[len(key)-2:], VersionID: f.version, PrincipalID: owner, OperationID: key, RequestedSelection: []byte(`{"project_id":"` + f.project + `","mode":"current","source_ids":["git-1","git-2"]}`), ScopeJSON: []byte(`{"kind":"version","page_path":null,"region_id":null,"declared":true,"reason":null}`), CreatedAt: "2026-10-01T09:00:00.000Z"}
			if e := tx.PutObservationRun(run); e != nil {
				return e
			}
			for i, source := range state.rows {
				if e := tx.PutSourceObservation(domain.SourceObservation{ID: "4b000000-0000-4000-8000-0000000000" + string(rune('a'+i)) + key[len(key)-1:], RunID: run.ID, VersionID: f.version, SourceID: source, RepoID: repoAID, BaseRevision: f.baseA, ObservedRevision: f.baseA, Availability: "available", Classification: "unchanged", ConfigRevision: 1, PermissionsRevision: 1, ObservedAt: run.CreatedAt, OperationID: key}); e != nil {
					return e
				}
			}
		}
		return tx.PutCheckIntent(domain.CheckIntent{ID: state.intentID, OperationID: key, PrincipalID: owner, Kind: "observation", CanonicalTarget: []byte(`{"route":"` + route + `"}`), ConfigRevision: 1, PermissionsRevision: 1, State: "open", CreatedAt: "2026-10-01T09:00:00.000Z"})
	})).To(Succeed())
}

func (f *observationFixture) intentStateOf(t GinkgoTInterface, key string) string {
	t.Helper()
	return readObservationState(t, f, key).intent
}

var _ = Describe("observation recovery", Label("persistence"), func() {
	t := GinkgoT()

	// W3b L12: the accepted-with-rows branch recomposes the 201 with the same
	// view function as the commit.
	It("recomposes an accepted receipt with committed rows in the commit's shape", func() {
		f := newObservationFixture(t, "current")
		// W3c 3: a reversed source_ids request still lists the sources by id.
		real := mustObserve(t, f, "39000000-0000-4000-8000-000000000001", `{"source_ids":["git-2","git-1"]}`)
		key := "39000000-0000-4000-8000-000000000002"
		f.stageRecovery(t, key, observationRecoveryState{receipt: "accepted", rows: []string{"git-2", "git-1"}, intentID: "39000000-0000-4000-8000-0000000000f2"})
		Expect(f.core.Recover(context.Background())).To(Succeed())
		receipt, e := f.auth.Operation(context.Background(), f.token, key)
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal("completed"))
		Expect(receipt.Response.Status).To(Equal(201))
		var recomposed map[string]any
		Expect(json.Unmarshal(receipt.Response.Body, &recomposed)).To(Succeed())
		keys := func(m map[string]any) []string {
			out := []string{}
			for key := range m {
				out = append(out, key)
			}
			return out
		}
		Expect(keys(recomposed)).To(ConsistOf(keys(real)), "the same top-level shape as a committed 201")
		Expect(recomposed["scope"]).To(Equal(real["scope"]), "the observation scope view, never the stored body form")
		Expect(recomposed["observed_at"]).To(Equal("2026-10-01T09:00:00.000Z"))
		Expect(recomposed["target_set_id"]).To(BeNil())
		ids := func(view map[string]any) []string {
			out := []string{}
			for _, item := range view["sources"].([]any) {
				out = append(out, mapString(item.(map[string]any)["source_id"]))
			}
			return out
		}
		Expect(ids(real)).To(Equal([]string{"git-1", "git-2"}), "the commit lists the sources by id")
		Expect(ids(recomposed)).To(Equal(ids(real)), "the sweep lists them in the same order")
		Expect(f.intentStateOf(t, key)).To(Equal("completed"))
	})

	// W3b L21: each contradiction names the intent and leaves it open (G6).
	DescribeTable("names the intent for a contradictory observation recovery state",
		func(state observationRecoveryState, contradiction string) {
			f := newObservationFixture(t, "current")
			key := "39000000-0000-4000-8000-0000000000" + state.intentID[len(state.intentID)-2:]
			f.stageRecovery(t, key, state)
			err := f.core.Recover(context.Background())
			Expect(err).To(MatchError(appsource.ErrRecoveryContradiction))
			Expect(err.Error()).To(ContainSubstring(state.intentID), "the error names the intent")
			Expect(err.Error()).To(ContainSubstring(contradiction))
			Expect(f.intentStateOf(t, key)).To(Equal("open"), "the contradiction is not resolved silently")
		},
		Entry("a completed receipt without rows", observationRecoveryState{receipt: "completed", intentID: "39000000-0000-4000-8000-0000000000e1"}, "completed receipt without its observation rows"),
		Entry("a failed receipt with rows", observationRecoveryState{receipt: "failed", rows: []string{"git-1"}, intentID: "39000000-0000-4000-8000-0000000000e2"}, "failed receipt with observation rows"),
		Entry("a reconciling receipt", observationRecoveryState{receipt: "reconciling", intentID: "39000000-0000-4000-8000-0000000000e3"}, "reconciling receipt"),
		Entry("no receipt at all", observationRecoveryState{intentID: "39000000-0000-4000-8000-0000000000e4"}, "no operation receipt"),
	)
})
