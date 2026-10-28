package sqlite

import (
	"bytes"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"path/filepath"
	"time"
)

var _ = It("PA33 actual first input observation is timed once while capture-only and rejected evidence stay null", Label("persistence"), func() {
	s, a := providerFixture()
	var input domain.ProviderInput
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		inputs, e := tx.ProviderInputs(a.ID)
		if e == nil {
			input = inputs[0]
			Expect(input.ObservedAt).To(BeNil())
		}
		return e
	})).To(Succeed())
	var missing int
	Expect(s.db.QueryRow(`SELECT count(*) FROM provider_inputs WHERE attempt_id=? AND input_origin IS NULL AND observed_at IS NULL`, a.ID).Scan(&missing)).To(Succeed())
	Expect(missing).To(Equal(1))
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.DispatchProviderAttempt(a.ID) })).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ObserveProviderInput(a.ID, input.ReadID, "posthoc_verified") })).To(MatchError(domain.ErrInvalidTarget))
	before := time.Now().UTC()
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ObserveProviderInput(a.ID, input.ReadID, "daemon_supplied") })).To(Succeed())
	after := time.Now().UTC()
	var first string
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		inputs, e := tx.ProviderInputs(a.ID)
		if e != nil {
			return e
		}
		Expect(inputs[0].ObservedAt).NotTo(BeNil())
		first = *inputs[0].ObservedAt
		stamp, e := time.Parse(time.RFC3339Nano, first)
		Expect(e).NotTo(HaveOccurred())
		Expect(stamp.Before(before) || stamp.After(after)).To(BeFalse())
		return tx.ObserveProviderInput(a.ID, input.ReadID, "daemon_supplied")
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ObserveProviderInput(a.ID, input.ReadID, "native_observed") })).To(MatchError(domain.ErrConflict))
	var origin, unchanged string
	Expect(s.db.QueryRow(`SELECT input_origin,observed_at FROM provider_inputs WHERE attempt_id=?`, a.ID).Scan(&origin, &unchanged)).To(Succeed())
	Expect(origin).To(Equal("daemon_supplied"))
	Expect(unchanged).To(Equal(first))
	_, e := s.db.Exec(`UPDATE provider_inputs SET observed_at=NULL WHERE attempt_id=?`, a.ID)
	Expect(e).To(HaveOccurred(), "DDL must reject a half observation")

	for _, origin := range []string{"", "daemon_supplied"} {
		s, queued := providerFixture(false)
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			j, e := tx.ClaimJob(queued.JobID)
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
			read, e := tx.CapturedRead(scope.ReadIDs[0])
			if e != nil {
				return e
			}
			attempt := domain.ProviderAttempt{ID: id(GinkgoT()), JobID: j.ID, Generation: j.AttemptGeneration, Runtime: lib.RuntimeGeneration, State: "prepared", TargetSetID: scope.TargetSetID, RequestSHA: scope.RequestSHA}
			return tx.PrepareProviderAttempt(attempt, []domain.ProviderInput{{AttemptID: attempt.ID, ReadID: read.ID, Hash: read.BytesSHA, ObservedOrigin: origin, ObservedAt: &first}})
		})).To(MatchError(domain.ErrInvalidTarget), "capture preparation cannot preseed an observation or its time")
	}
})

var _ = It("PA34 final-only actual input evidence receives its first receipt time without accepting a backdated driver stamp", Label("persistence"), func() {
	s, a := providerFixture(false)
	var afterProgress time.Time
	backdated := "2000-01-01T00:00:00Z"
	d := &providerFake{noInputObservation: true, inputObservedAt: &backdated, afterProgress: func() error {
		afterProgress = time.Now().UTC()
		return s.Write(ctx, func(tx app.WriteTx) error {
			rows, e := tx.ProviderAttempts(a.JobID)
			if e != nil {
				return e
			}
			inputs, e := tx.ProviderInputs(rows[0].ID)
			if e != nil {
				return e
			}
			Expect(inputs[0].ObservedAt).To(BeNil())
			return nil
		})
	}}
	p := work.ProviderCoordinator{Store: s, Driver: d, Bytes: providerFixtureBytes{s}}
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		rows, e := tx.ProviderAttempts(a.JobID)
		if e != nil {
			return e
		}
		inputs, e := tx.ProviderInputs(rows[0].ID)
		if e != nil {
			return e
		}
		Expect(inputs[0].ObservedAt).NotTo(BeNil())
		stamp, e := time.Parse(time.RFC3339Nano, *inputs[0].ObservedAt)
		Expect(e).NotTo(HaveOccurred())
		Expect(stamp.Before(afterProgress)).To(BeFalse())
		return nil
	})).To(Succeed())
})

var _ = It("PA35 repeated older orphan inventories preserve first observation pairs chronologically without timing legacy evidence", Label("persistence"), func() {
	s, a := providerFixture()
	older := filepath.Join(GinkgoT().TempDir(), "older.sqlite")
	Expect(s.SnapshotDatabase(ctx, older)).To(Succeed())
	var read domain.ProviderInput
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		inputs, e := tx.ProviderInputs(a.ID)
		if e == nil {
			read = inputs[0]
		}
		return e
	})).To(Succeed())
	retain := func(inputs []domain.ProviderInput) {
		raw := jsonBytes(map[string]any{"attempt_id": a.ID, "request_sha256": a.RequestSHA, "state": "outcome_unknown", "inputs": inputs})
		Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(raw), providerFixtureCopiedAvailability(older))).To(Succeed())
	}
	// Chronological order differs from string order across zones and fractional seconds.
	late, early := "2026-01-01T00:00:00.1Z", "2026-01-01T01:00:00+01:00"
	read.ObservedOrigin = "native_observed"
	read.ObservedAt = &late
	retain([]domain.ProviderInput{read})
	read.ObservedAt = &early
	retain([]domain.ProviderInput{read})
	equalInstant := "2026-01-01T00:00:00Z"
	read.ObservedAt = &equalInstant
	retain([]domain.ProviderInput{read})
	read.ObservedOrigin = ""
	read.ObservedAt = nil
	retain([]domain.ProviderInput{read})
	retain(nil)
	db, e := openCopy(older)
	Expect(e).NotTo(HaveOccurred())
	defer db.Close()
	var raw []byte
	Expect(db.QueryRow(`SELECT value FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&raw)).To(Succeed())
	var fact providerRestoreFact
	Expect(json.Unmarshal(raw, &fact)).To(Succeed())
	Expect(fact.Inputs).To(HaveLen(1))
	Expect(fact.Inputs[0].ObservedAt).To(Equal(&early))
	Expect(fact.Inputs[0].ObservedOrigin).To(Equal("native_observed"))
	// An earlier historical record without a timestamp gains no invented date.
	legacy := read
	legacy.ReadID = "legacy"
	legacy.ObservedOrigin = "daemon_supplied"
	retain([]domain.ProviderInput{legacy})
	Expect(db.QueryRow(`SELECT value FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&raw)).To(Succeed())
	Expect(json.Unmarshal(raw, &fact)).To(Succeed())
	Expect(fact.Inputs).To(ContainElement(And(HaveField("ReadID", "legacy"), HaveField("ObservedAt", BeNil()))))
	legacy.ObservedAt = &late
	retain([]domain.ProviderInput{legacy})
	Expect(db.QueryRow(`SELECT value FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&raw)).To(Succeed())
	Expect(json.Unmarshal(raw, &fact)).To(Succeed())
	Expect(fact.Inputs).To(ContainElement(And(HaveField("ReadID", "legacy"), HaveField("ObservedAt", BeNil()))))
	// Contradictory supported origins retain explicit prior facts, never overwrite silently.
	read.ObservedOrigin = "daemon_supplied"
	read.ObservedAt = &late
	retain([]domain.ProviderInput{read})
	Expect(db.QueryRow(`SELECT value FROM provider_orphans WHERE attempt_id=?`, a.ID).Scan(&raw)).To(Succeed())
	Expect(json.Unmarshal(raw, &fact)).To(Succeed())
	Expect(fact.State).To(Equal("outcome_unknown"))
	Expect(fact.Conflict).NotTo(BeEmpty())
	read.ObservedAt = new(string)
	*read.ObservedAt = "not-a-time"
	bad := jsonBytes(map[string]any{"attempt_id": a.ID, "request_sha256": a.RequestSHA, "state": "outcome_unknown", "inputs": []domain.ProviderInput{read}})
	Expect(RetainProviderOrphans(ctx, older, bytes.NewReader(bad), nil)).To(MatchError(domain.ErrInvalidTarget))
})
