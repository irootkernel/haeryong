package sqlite

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"strings"
)

func providerFixture(prepare ...bool) (s *Store, a domain.ProviderAttempt) {
	t := GinkgoT()
	s, core, _, _ := authFixture(t)
	_, owner := pairOwner(t, core, "provider-ledger")
	var lib domain.Library
	var state domain.AuthState
	jobID, attemptID, readID, targetID := id(t), id(t), id(t), id(t)
	requestSHA := strings.Repeat("a", 64)
	inputHash := fmt.Sprintf("%x", sha256.Sum256([]byte("provider fixture source")))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		var e error
		lib, e = tx.Library()
		if e != nil {
			return e
		}
		state, e = tx.AuthState()
		if e != nil {
			return e
		}
		if e = tx.PutDurableObject(domain.Object{Hash: inputHash, Size: int64(len("provider fixture source")), State: "ready"}); e != nil {
			return e
		}
		if e = tx.PutTargetSet(domain.TargetSet{ID: targetID, ProjectID: lib.UnfiledProjectID, RequestedSelection: []byte(`{"kind":"pinned"}`), TargetsJSON: []byte(`[]`), CapturedAt: "fixture"}); e != nil {
			return e
		}
		if e = tx.PutCapturedRead(domain.CapturedRead{ID: readID, TargetSetID: targetID, Basis: "project_source", SourceJSON: []byte(`{}`), LocationJSON: []byte(`{}`), BytesSHA: inputHash, ConfigRevision: 1, PermissionsRevision: 1, CreatedAt: "fixture"}); e != nil {
			return e
		}
		scope := domain.ProviderScope{PrincipalID: owner.Session.PrincipalID, AuthEpoch: state.AuthEpoch, Target: jsonBytes(map[string]string{"project_id": lib.UnfiledProjectID}), TargetSetID: targetID, RequestSHA: requestSHA, ReadIDs: []string{readID}}
		if e = tx.CreateJob(domain.Job{ID: jobID, Kind: "question", Scope: jsonBytes(scope), Permissions: map[string]int64{lib.UnfiledProjectID: 1}}); e != nil {
			return e
		}
		if len(prepare) > 0 && !prepare[0] {
			a.JobID = jobID
			return nil
		}
		j, e := tx.ClaimJob(jobID)
		if e != nil {
			return e
		}
		a = domain.ProviderAttempt{ID: attemptID, JobID: jobID, Generation: j.AttemptGeneration, State: "prepared", Runtime: lib.RuntimeGeneration, TargetSetID: targetID, RequestSHA: requestSHA}
		return tx.PrepareProviderAttempt(a, []domain.ProviderInput{{AttemptID: a.ID, ReadID: readID, Hash: inputHash}})
	})).To(Succeed())
	return
}

var _ = It("PA01 cancellation before dispatch makes the call ineligible and never invents input observation", Label("persistence"), func() {
	s, a := providerFixture()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		inputs, e := tx.ProviderInputs(a.ID)
		if e != nil {
			return e
		}
		Expect(inputs).To(HaveLen(1))
		Expect(inputs[0].ObservedOrigin).To(BeEmpty())
		Expect(inputs[0].ObservedAt).To(BeNil())
		_, e = tx.CancelJob(a.JobID)
		return e
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.DispatchProviderAttempt(a.ID) })).To(MatchError(domain.ErrTransition))
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("cancelled"))
})

var _ = It("PA02 old results reconcile their own attempt after same-daemon cancellation without applying", Label("persistence"), func() {
	s, a := providerFixture()
	result := domain.Object{Hash: strings.Repeat("c", 64), Size: 6, State: "ready"}
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.DispatchProviderAttempt(a.ID) })).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { _, e := tx.CancelJob(a.JobID); return e })).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		return tx.ConfirmStop(j.ID, j.AttemptGeneration)
	})).To(Succeed())
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("paused"))
	Expect(j.Error).To(Equal("outcome_unknown"))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.PutDurableObject(result); e != nil {
			return e
		}
		return tx.SaveProviderResult(a.ID, result)
	})).To(Succeed())
	called := false
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.ApplyProviderResult(a.ID, func(app.WriteTx) error { called = true; return nil })
	})).To(MatchError(domain.ErrStaleWorker))
	Expect(called).To(BeFalse())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.ResumeProviderJob(a.JobID, j.AttemptGeneration, "retry")
		return e
	})).To(MatchError(domain.ErrTransition))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		return tx.ConfirmStop(j.ID, j.AttemptGeneration)
	})).To(Succeed())
	j, e = s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("cancelled"))
})

var _ = It("PA03 explicit retry retains uncertain old attempts and fences reassigned results", Label("persistence"), func() {
	s, a := providerFixture()
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.DispatchProviderAttempt(a.ID) })).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "request", "session", "transport_unknown"); e != nil {
			return e
		}
		return tx.PauseProviderJob(a.JobID, "outcome_unknown")
	})).To(Succeed())
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.ResumeProviderJob(a.JobID, j.AttemptGeneration, "continue")
		return e
	})).To(MatchError(domain.ErrReconciliation))
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.ResumeProviderJob(a.JobID, j.AttemptGeneration, "retry")
		if e != nil {
			return e
		}
		next, e := tx.ClaimJob(a.JobID)
		if e != nil {
			return e
		}
		a2 := a
		a2.ID = id(GinkgoT())
		a2.Generation = next.AttemptGeneration
		inputs, e := tx.ProviderInputs(a.ID)
		if e != nil {
			return e
		}
		for i := range inputs {
			inputs[i].AttemptID = a2.ID
		}
		if e = tx.PrepareProviderAttempt(a2, inputs); e != nil {
			return e
		}
		attempts, e := tx.ProviderAttempts(a.JobID)
		Expect(attempts).To(HaveLen(2))
		return e
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.DispatchProviderAttempt(a.ID) })).To(MatchError(domain.ErrTransition))
	// The newer job is running and not cancelled: only generation may fence A1.
	current, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(current.State).To(Equal("running"))
	Expect(current.CancelRequested).To(BeFalse())
	Expect(current.AttemptGeneration).To(BeNumerically(">", a.Generation))
	called := false
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		result := domain.Object{Hash: strings.Repeat("c", 64), Size: 6, State: "ready"}
		if e := tx.PutDurableObject(result); e != nil {
			return e
		}
		return tx.SaveProviderResult(a.ID, result)
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.ApplyProviderResult(a.ID, func(app.WriteTx) error { called = true; return nil })
	})).To(MatchError(domain.ErrStaleWorker))
	Expect(called).To(BeFalse())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PauseProviderJob(a.JobID, "fixture_pause") })).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.ResumeProviderJob(a.JobID, current.AttemptGeneration, "continue")
		return e
	})).To(MatchError(domain.ErrStaleWorker))
})

var _ = It("PA04 identical configuration permits dispatch and an uncaptured read observation is refused", Label("persistence"), func() {
	s, a := providerFixture()
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		c, e := tx.Configuration()
		if e != nil {
			return e
		}
		return tx.SetConfiguration(c, c.Revision)
	})).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.DispatchProviderAttempt(a.ID) })).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ObserveProviderInput(a.ID, "not-captured", "daemon_supplied") })).To(MatchError(domain.ErrNotFound))
})

var _ = It("PA05 retained orphan metadata composes with an existing same-request prepared barrier", Label("persistence"), func() {
	s, a := providerFixture()
	path := s.profile.DataDir + "/storage/library.sqlite"
	orphan := jsonBytes(map[string]any{"attempt_id": "newer-attempt", "request_sha256": a.RequestSHA, "state": "outcome_unknown", "provider_request_id": "retained-native-id"})
	Expect(RetainProviderOrphans(ctx, path, bytes.NewReader(append(orphan, '\n')), nil)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(a.JobID)
		if e != nil {
			return e
		}
		newJob := j
		newJob.ID = id(GinkgoT())
		newJob.State = "queued"
		if e = tx.CreateJob(newJob); e != nil {
			return e
		}
		newJob, e = tx.ClaimJob(newJob.ID)
		if e != nil {
			return e
		}
		next := a
		next.ID = id(GinkgoT())
		next.JobID = newJob.ID
		next.Generation = newJob.AttemptGeneration
		inputs, e := tx.ProviderInputs(a.ID)
		if e != nil {
			return e
		}
		for i := range inputs {
			inputs[i].AttemptID = next.ID
		}
		return tx.PrepareProviderAttempt(next, inputs)
	})).To(MatchError(domain.ErrReconciliation))
})

var _ = It("PA08 repeated older restore retains newer definite orphan evidence and identities", Label("persistence"), func() {
	s, a := providerFixture()
	path := s.profile.DataDir + "/storage/library.sqlite"
	for _, state := range []string{"outcome_unknown", "result_saved", "outcome_unknown"} {
		raw := jsonBytes(map[string]string{"attempt_id": "orphan", "request_sha256": a.RequestSHA, "state": state, "provider_request_id": "actual-request", "result_hash": "known-result"})
		Expect(RetainProviderOrphans(ctx, path, bytes.NewReader(append(raw, '\n')), nil)).To(Succeed())
	}
	var state, value string
	Expect(s.db.QueryRow(`SELECT state,CAST(value AS TEXT) FROM provider_orphans WHERE attempt_id='orphan'`).Scan(&state, &value)).To(Succeed())
	Expect(state).To(Equal("result_saved"))
	Expect(value).To(ContainSubstring("actual-request"))
	Expect(value).To(ContainSubstring("known-result"))
})

var _ = It("PA09 explicit continued saved output after normal restart rebinds application without another dispatch", Label("persistence"), func() {
	s, a := providerFixture()
	p := s.profile
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.DispatchProviderAttempt(a.ID); e != nil {
			return e
		}
		o := domain.Object{Hash: strings.Repeat("c", 64), Size: 6, State: "ready"}
		if e := tx.PutDurableObject(o); e != nil {
			return e
		}
		return tx.SaveProviderResult(a.ID, o)
	})).To(Succeed())
	Expect(s.Close()).To(Succeed())
	s = New(nil)
	_, closeDB, e := s.Prepare(ctx, p)
	Expect(e).NotTo(HaveOccurred())
	defer closeDB()
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ReconcileJob(a.JobID) })).To(Succeed())
	j1, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j1.State).To(Equal("reconciling"))
	coordinator := work.ProviderCoordinator{Store: s, Bytes: providerContinuationFixtureBytes{providerFixtureBytes{s}}}
	Expect(coordinator.Process(ctx, a.JobID)).To(Succeed())
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		_, e := tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "continue")
		return e
	})).To(Succeed())
	Expect(coordinator.Process(ctx, a.JobID)).To(Succeed())
	j, e = s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("completed"))
})
