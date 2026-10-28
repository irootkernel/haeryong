package sqlite

import (
	"bytes"
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/configuration"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"path/filepath"
	"strings"
)

var _ = It("PA17 terminal settled attempts release only temporary inputs and retain metadata and result ownership through GC", Label("persistence"), func() {
	s, a := providerFixture()
	var input domain.ProviderInput
	result := domain.Object{Hash: strings.Repeat("c", 64), Size: 6, State: "ready"}
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		inputs, e := tx.ProviderInputs(a.ID)
		if e != nil {
			return e
		}
		input = inputs[0]
		for _, pin := range []domain.Pin{{ID: "selected", Kind: "selected_snapshot", OwnerID: "approved", Hash: input.Hash}, {ID: "independent", Kind: "backup", OwnerID: "other", Hash: input.Hash}} {
			if e = tx.AcquirePin(pin); e != nil {
				return e
			}
		}
		if e = tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		if e = tx.PutDurableObject(result); e != nil {
			return e
		}
		return tx.SaveProviderResult(a.ID, result)
	})).To(Succeed())
	before := filepath.Join(GinkgoT().TempDir(), "active.sqlite")
	Expect(s.SnapshotDatabase(ctx, before)).To(Succeed())
	page, e := CopyObjectPage(ctx, before, "")
	Expect(e).NotTo(HaveOccurred())
	Expect(page).To(ContainElement(HaveField("Hash", input.Hash)))
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ApplyProviderResult(a.ID, nil) })).To(Succeed())
	var pins int
	Expect(s.db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_attempt' AND pin_id=?`, a.ID+":"+input.ReadID).Scan(&pins)).To(Succeed())
	Expect(pins).To(BeZero())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { _, e := tx.ReserveGC(input.Hash, "blocked"); return e })).To(MatchError(domain.ErrPinned))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.ReleasePin("selected"); e != nil {
			return e
		}
		if e := tx.ReleasePin("independent"); e != nil {
			return e
		}
		r, e := tx.ReserveGC(input.Hash, "settled")
		if e != nil {
			return e
		}
		return tx.FinishGC(r)
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		inputs, e := tx.ProviderInputs(a.ID)
		if e != nil {
			return e
		}
		Expect(inputs).To(HaveLen(1))
		Expect(inputs[0].Hash).To(Equal(input.Hash))
		_, e = tx.ProviderAttempt(a.ID)
		return e
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { _, e := tx.ReserveGC(result.Hash, "durable-result"); return e })).To(MatchError(domain.ErrPinned))
	after := filepath.Join(GinkgoT().TempDir(), "settled.sqlite")
	Expect(s.SnapshotDatabase(ctx, after)).To(Succeed())
	Expect(CheckCopyDatabase(ctx, after)).To(Succeed())
	page, e = CopyObjectPage(ctx, after, "")
	Expect(e).NotTo(HaveOccurred())
	Expect(page).NotTo(ContainElement(HaveField("Hash", input.Hash)))
	Expect(page).To(ContainElement(HaveField("Hash", result.Hash)))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.ReleasePin(a.ID + ":result"); e != nil {
			return e
		}
		r, e := tx.ReserveGC(result.Hash, "explicit-result-release")
		if e != nil {
			return e
		}
		if e = tx.FinishGC(r); e != nil {
			return e
		}
		historical, e := tx.ProviderAttempt(a.ID)
		if e != nil {
			return e
		}
		Expect(historical.ResultHash).To(Equal(result.Hash))
		return nil
	})).To(Succeed())

})

var _ = It("PA18 older prepared snapshots retain newer unknown orphan inputs independently of cancelled temporary pins", Label("persistence"), func() {
	s, a := providerFixture()
	var firstObserved string
	older := filepath.Join(GinkgoT().TempDir(), "older.sqlite")
	Expect(s.SnapshotDatabase(ctx, older)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		inputs, e := tx.ProviderInputs(a.ID)
		if e != nil {
			return e
		}
		if e = tx.ObserveProviderInput(a.ID, inputs[0].ReadID, "daemon_supplied"); e != nil {
			return e
		}
		inputs, e = tx.ProviderInputs(a.ID)
		if e != nil {
			return e
		}
		firstObserved = *inputs[0].ObservedAt
		_, e = tx.CancelJob(a.JobID)
		return e
	})).To(Succeed())
	var inventory bytes.Buffer
	Expect(RestoreOutstanding(ctx, s.profile.DataDir+"/storage/library.sqlite", func(raw json.RawMessage) error { _, e := inventory.Write(append(raw, '\n')); return e })).To(Succeed())
	Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(inventory.Bytes()), providerFixtureCopiedAvailability(older))).To(Succeed())
	db, e := openCopy(older)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	tx, e := db.BeginTx(ctx, nil)
	Expect(e).NotTo(HaveOccurred())
	wrapped := &writeTx{ctx: ctx, tx: tx}
	inputs, e := wrapped.ProviderInputs(a.ID)
	Expect(e).NotTo(HaveOccurred())
	hash := inputs[0].Hash
	_, e = wrapped.CancelJob(a.JobID)
	Expect(e).NotTo(HaveOccurred())
	_, e = wrapped.ReserveGC(hash, "unknown-orphan")
	Expect(e).To(MatchError(domain.ErrPinned))
	var own int
	Expect(tx.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_attempt' AND owner_id=?`, a.ID).Scan(&own)).To(Succeed())
	Expect(own).To(Equal(1), "actual same-ID dispatch uncertainty retains execution input ownership")
	Expect(tx.Commit()).To(Succeed())
	Expect(CheckCopyDatabase(ctx, older)).To(Succeed())
	page, e := CopyObjectPage(ctx, older, "")
	Expect(e).NotTo(HaveOccurred())
	Expect(page).To(ContainElement(HaveField("Hash", hash)))
	Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(inventory.Bytes()), func(domain.Object) (bool, error) { return false, nil })).To(MatchError(domain.ErrContentIntegrity), "missing previously owned bytes must not be silently forgiven")
	var retained int
	Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_orphan' AND owner_id=?`, a.ID).Scan(&retained)).To(Succeed())
	Expect(retained).To(Equal(1))
	// Repeated retention cannot discard input references omitted by a newer receipt.
	definite := jsonBytes(map[string]any{"attempt_id": a.ID, "job_id": a.JobID, "request_sha256": a.RequestSHA, "state": "cancelled", "reason": "cancel_confirmed", "job_state": "cancelled", "cancel_requested": true})
	Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(definite), providerFixtureCopiedAvailability(older))).To(Succeed())
	var count int
	Expect(db.QueryRow(`SELECT json_array_length(value,'$.inputs') FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&count)).To(Succeed())
	Expect(count).To(Equal(1))
	var observed string
	Expect(db.QueryRow(`SELECT json_extract(value,'$.inputs[0].observed_at') FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&observed)).To(Succeed())
	Expect(observed).To(Equal(firstObserved))
	Expect(db.QueryRow(`SELECT count(*) FROM object_pins WHERE owner_kind='provider_orphan' AND owner_id=?`, a.ID).Scan(&count)).To(Succeed())
	Expect(count).To(BeZero())
	missing := jsonBytes(map[string]any{"attempt_id": "absent-object", "request_sha256": strings.Repeat("b", 64), "state": "outcome_unknown", "inputs": []domain.ProviderInput{{ReadID: "missing", Hash: strings.Repeat("f", 64)}}})
	Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(missing), providerFixtureCopiedAvailability(older))).To(Succeed())
	Expect(db.QueryRow(`SELECT count(*) FROM content_objects WHERE content_hash=?`, strings.Repeat("f", 64)).Scan(&count)).To(Succeed())
	Expect(count).To(BeZero())
	fault := errors.New("fixture staging availability failure")
	failed := jsonBytes(map[string]any{"attempt_id": "availability-error", "request_sha256": strings.Repeat("d", 64), "state": "outcome_unknown", "inputs": []domain.ProviderInput{{ReadID: "retained", Hash: hash}}})
	Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(failed), func(domain.Object) (bool, error) { return false, fault })).To(MatchError(fault))
	Expect(db.QueryRow(`SELECT count(*) FROM provider_orphans WHERE attempt_id='availability-error'`).Scan(&count)).To(Succeed())
	Expect(count).To(BeZero(), "availability errors must roll back rather than silently declare missing bytes")
})

func providerClone(s *Store, a domain.ProviderAttempt, sha, parent string, authorize []string) (domain.ProviderAttempt, error) {
	next := a
	next.ID = id(GinkgoT())
	next.JobID = id(GinkgoT())
	next.RequestSHA = sha
	e := s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		j.ID = next.JobID
		j.ParentID = parent
		j.State = "queued"
		j.CancelRequested = false
		j.AttemptGeneration = 1
		var scope domain.ProviderScope
		if e = json.Unmarshal(j.Scope, &scope); e != nil {
			return e
		}
		scope.RequestSHA = sha
		auth, e := tx.AuthState()
		if e != nil {
			return e
		}
		scope.AuthEpoch = auth.AuthEpoch
		j.Scope = jsonBytes(scope)
		config, e := tx.Configuration()
		if e != nil {
			return e
		}
		j.ConfigRevision = config.Revision
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		next.Runtime = lib.RuntimeGeneration
		if e = tx.CreateJob(j); e != nil {
			return e
		}
		if len(authorize) > 0 {
			if e = tx.AuthorizeProviderRetry(j.ID, authorize); e != nil {
				return e
			}
		}
		j, e = tx.ClaimJob(j.ID)
		if e != nil {
			return e
		}
		next.Generation = j.AttemptGeneration
		inputs, e := tx.ProviderInputs(a.ID)
		if e != nil {
			return e
		}
		for i := range inputs {
			inputs[i].AttemptID = next.ID
			inputs[i].ObservedOrigin = ""
			inputs[i].ObservedAt = nil
		}
		return tx.PrepareProviderAttempt(next, inputs)
	})
	return next, e
}

var _ = It("PA19 fresh IDs cannot bypass equivalent uncertainty but deliberate linked predecessor sets and changed requests can proceed", Label("persistence"), func() {
	s, a := providerFixture()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		_, e := tx.CancelJob(a.JobID)
		return e
	})).To(Succeed())
	_, e := providerClone(s, a, a.RequestSHA, "", nil)
	Expect(e).To(MatchError(domain.ErrReconciliation))
	_, e = providerClone(s, a, a.RequestSHA, a.JobID, nil)
	Expect(e).To(MatchError(domain.ErrReconciliation))
	changed, e := providerClone(s, a, strings.Repeat("b", 64), "", nil)
	Expect(e).NotTo(HaveOccurred())
	Expect(changed.RequestSHA).NotTo(Equal(a.RequestSHA))
	second, e := providerClone(s, a, a.RequestSHA, a.JobID, []string{a.ID})
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(second.ID); e != nil {
			return e
		}
		_, e := tx.CancelJob(second.JobID)
		return e
	})).To(Succeed())
	_, e = providerClone(s, a, a.RequestSHA, second.JobID, []string{second.ID})
	Expect(e).To(MatchError(domain.ErrReconciliation))
	third, e := providerClone(s, a, a.RequestSHA, second.JobID, []string{a.ID, second.ID})
	Expect(e).NotTo(HaveOccurred())
	Expect(third.ID).NotTo(Equal(a.ID))
	old, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(old.CancelRequested).To(BeTrue())
})

var _ = It("PA24 protected same-ID restore composes review and linked retry without reviving cancellation", Label("persistence"), func() {
	s, a := providerFixture()
	older := filepath.Join(GinkgoT().TempDir(), "protected.sqlite")
	Expect(s.SnapshotDatabase(ctx, older)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		_, e := tx.CancelJob(a.JobID)
		return e
	})).To(Succeed())
	var inventory bytes.Buffer
	Expect(RestoreOutstanding(ctx, s.profile.DataDir+"/storage/library.sqlite", func(raw json.RawMessage) error { _, e := inventory.Write(append(raw, '\n')); return e })).To(Succeed())
	hold := id(GinkgoT())
	_, e := ProtectRestoredDatabase(ctx, older, s.profile, hold, CopyCurrentConfiguration(ctx, older), configuration.Validate)
	Expect(e).NotTo(HaveOccurred())
	Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(inventory.Bytes()), providerFixtureCopiedAvailability(older))).To(Succeed())
	db, e := openCopy(older)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	restored := New(nil)
	restored.db = db
	_, e = providerClone(restored, a, a.RequestSHA, a.JobID, []string{a.ID})
	Expect(e).To(MatchError(domain.ErrReconciliation))
	Expect(restored.Write(ctx, func(tx app.WriteTx) error { return tx.ReviewRecovery(hold, nil) })).To(Succeed())
	_, e = providerClone(restored, a, a.RequestSHA, a.JobID, nil)
	Expect(e).To(MatchError(domain.ErrReconciliation))
	_, e = providerClone(restored, a, a.RequestSHA, a.JobID, []string{a.ID})
	Expect(e).NotTo(HaveOccurred())
	var cancelled bool
	Expect(db.QueryRow(`SELECT json_extract(value,'$.cancel_requested') FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&cancelled)).To(Succeed())
	Expect(cancelled).To(BeTrue())
})

// White-box fixtures supply the already copied object boundary. Production
// checks the fresh verified staging directory only for each requested object.
func providerFixtureCopiedAvailability(path string) func(domain.Object) (bool, error) {
	GinkgoHelper()
	copied := map[string]bool{}
	after := ""
	for {
		objects, e := CopyObjectPage(ctx, path, after)
		Expect(e).NotTo(HaveOccurred())
		if len(objects) == 0 {
			break
		}
		for _, object := range objects {
			copied[object.Hash] = true
			after = object.Hash
		}
	}
	return func(object domain.Object) (bool, error) { return copied[object.Hash], nil }
}
