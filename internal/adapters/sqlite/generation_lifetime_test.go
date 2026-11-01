package sqlite

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func generationOwnedFixture(s *Store, a domain.ProviderAttempt) []domain.Object {
	objects := []domain.Object{{Hash: strings.Repeat("d", 64), Size: 10, State: "ready"}, {Hash: strings.Repeat("e", 64), Size: 20, State: "ready"}}
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		for _, o := range objects {
			if e := tx.PutDurableObject(o); e != nil {
				return e
			}
			if e := tx.AcquirePin(domain.Pin{ID: a.ID + ":owned:" + o.Hash, Kind: "provider_attempt", OwnerID: a.ID, Hash: o.Hash}); e != nil {
				return e
			}
		}
		return nil
	})).To(Succeed())
	return objects
}

var _ = It("GGEN18 typed prompt/context execution pins release only after terminal settled jobs and remain through paused or unknown work", Label("persistence"), func() {
	// The policy fixture uses real typed SQLite rows/pins. Actual generation
	// construction and filesystem ownership are covered by GGEN16 separately.
	for _, mode := range []string{"paused", "unknown", "terminal"} {
		s, a := providerFixture()
		objects := generationOwnedFixture(s, a)
		result := domain.Object{Hash: strings.Repeat("c", 64), Size: 6, State: "ready"}
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			if e := tx.DispatchProviderAttempt(a.ID); e != nil {
				return e
			}
			if mode == "unknown" {
				if e := tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "request", "thread", "transport_unknown"); e != nil {
					return e
				}
				_, e := tx.CancelJob(a.JobID)
				return e
			}
			if e := tx.PutDurableObject(result); e != nil {
				return e
			}
			if e := tx.SaveProviderResult(a.ID, result); e != nil {
				return e
			}
			if mode == "paused" {
				return tx.PauseProviderJob(a.JobID, "preparation_failed")
			}
			return tx.ApplyProviderResult(a.ID, nil)
		})).To(Succeed())
		var pins int
		Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_attempt' AND owner_id=? AND pin_id LIKE '%:owned:%'`, a.ID).Scan(&pins)).To(Succeed())
		if mode == "terminal" {
			Expect(pins).To(BeZero())
		} else {
			Expect(pins).To(Equal(2))
		}
		for _, o := range objects {
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				r, e := tx.ReserveGC(o.Hash, "generation-owned-check")
				if mode != "terminal" {
					Expect(e).To(MatchError(domain.ErrPinned))
					return nil
				}
				if e != nil {
					return e
				}
				return tx.FinishGC(r)
			})).To(Succeed())
		}
		if mode != "unknown" {
			Expect(s.Write(ctx, func(tx app.WriteTx) error { _, e := tx.ReserveGC(result.Hash, "result-retained"); return e })).To(MatchError(domain.ErrPinned))
		}
	}
})
var _ = It("GGEN19 restored typed prompt/context orphan pins retain unknown and conflict facts and release only after the matching real job settles", Label("persistence"), func() {
	for _, conflict := range []bool{false, true} {
		s, a := providerFixture()
		objects := generationOwnedFixture(s, a)
		older := filepath.Join(GinkgoT().TempDir(), "owned-older.sqlite")
		Expect(s.SnapshotDatabase(ctx, older)).To(Succeed())
		var inputs []domain.ProviderInput
		Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; inputs, e = tx.ProviderInputs(a.ID); return e })).To(Succeed())
		yes := true
		page := "prior.html"
		fact := providerRestoreFact{ID: a.ID, JobID: a.JobID, SHA: a.RequestSHA, State: "outcome_unknown", RequestID: "request", SessionID: "thread", JobState: "running", MayHaveDispatched: &yes, Inputs: inputs, Transport: &domain.ProviderTransport{Generation: &domain.GenerationTransport{Prompt: &domain.ProviderText{Hash: objects[0].Hash, Size: objects[0].Size}, References: []domain.GenerationReference{{ExplanationReference: domain.ExplanationReference{VersionID: id(GinkgoT()), PagePath: &page, Hash: objects[1].Hash}, ObjectHash: objects[1].Hash, Size: objects[1].Size}}}}}
		Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(jsonBytes(fact)), providerFixtureCopiedAvailability(older))).To(Succeed())
		db, e := openCopy(older)
		Expect(e).NotTo(HaveOccurred())
		defer db.Close()
		restored := New(nil)
		restored.db = db
		var pins int
		count := func() int {
			Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_orphan' AND owner_id=? AND pin_id LIKE '%:owned:%'`, a.ID).Scan(&pins)).To(Succeed())
			return pins
		}
		Expect(count()).To(Equal(2))
		Expect(restored.Write(ctx, func(tx app.WriteTx) error { _, e := tx.CancelJob(a.JobID); return e })).To(Succeed())
		terminal := fact
		terminal.State = "cancelled"
		terminal.Reason = "cancel_confirmed"
		terminal.JobState = "cancelled"
		terminal.CancelRequested = true
		if conflict {
			terminal.RequestID = "different-native-request"
		}
		Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(jsonBytes(terminal)), providerFixtureCopiedAvailability(older))).To(Succeed())
		Expect(count()).To(Equal(2), "paused actual owner still retains these execution inputs")
		Expect(restored.Write(ctx, func(tx app.WriteTx) error {
			j, e := tx.Job(a.JobID)
			if e != nil {
				return e
			}
			return tx.ConfirmStop(j.ID, j.AttemptGeneration)
		})).To(Succeed())
		j, e := restored.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		if conflict {
			Expect(j.State).To(Equal("paused"))
			Expect(count()).To(Equal(2))
			var raw []byte
			Expect(db.QueryRow(`SELECT value FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&raw)).To(Succeed())
			var merged providerRestoreFact
			Expect(json.Unmarshal(raw, &merged)).To(Succeed())
			Expect(hasProviderConflict(merged.Conflict)).To(BeTrue())
		} else {
			Expect(j.State).To(Equal("cancelled"))
			Expect(count()).To(BeZero())
		}
	}
})
