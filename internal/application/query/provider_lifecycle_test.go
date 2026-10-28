package query_test

import (
	"bytes"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"strings"
)

var _ = It("PA15 Trash fences provider results and purge waits for actual unresolved external work", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	jobID, attemptID := key(GinkgoT()), key(GinkgoT())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		config, e := tx.Configuration()
		if e != nil {
			return e
		}
		target, _ := json.Marshal(map[string]string{"project_id": f.project, "version_id": f.version, "document_id": f.document})
		scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, RequestSHA: strings.Repeat("a", 64)})
		if e = tx.CreateJob(domain.Job{ID: jobID, Kind: "question", Scope: scope, ConfigRevision: config.Revision, Permissions: map[string]int64{f.project: 1}}); e != nil {
			return e
		}
		j, e := tx.ClaimJob(jobID)
		if e != nil {
			return e
		}
		a := domain.ProviderAttempt{ID: attemptID, JobID: jobID, Generation: j.AttemptGeneration, State: "prepared", Runtime: lib.RuntimeGeneration, RequestSHA: strings.Repeat("a", 64)}
		if e = tx.PrepareProviderAttempt(a, nil); e != nil {
			return e
		}
		return tx.DispatchProviderAttempt(a.ID)
	})).To(Succeed())
	lifecycleCommit(f, lifecyclePlan(f, "trash"))
	j, e := f.store.Job(ctx, jobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.CancelRequested).To(BeTrue())
	Expect(j.State).To(Equal("paused"))
	r := lifecycleCommit(f, lifecyclePlan(f, "purge"))
	purger := &work.Purger{Store: f.store, Bytes: f.store}
	Expect(purger.Process(ctx, r.JobID)).To(Succeed())
	purge, e := f.store.Job(ctx, r.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(purge.State).NotTo(Equal("completed"))
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		result := domain.Object{Hash: strings.Repeat("f", 64), Size: 6, State: "ready"}
		if e := tx.PutDurableObject(result); e != nil {
			return e
		}
		return tx.SaveProviderResult(attemptID, result)
	})).To(Succeed())
	applied := false
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		return tx.ApplyProviderResult(attemptID, func(app.WriteTx) error { applied = true; return nil })
	})).To(MatchError(domain.ErrStaleWorker))
	Expect(applied).To(BeFalse())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(jobID)
		if e != nil {
			return e
		}
		return tx.ConfirmStop(j.ID, j.AttemptGeneration)
	})).To(Succeed())
	purgeTurns(f, purger, r.JobID, 100)
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		intent, e := tx.PurgeIntent(r.JobID)
		if e != nil {
			return e
		}
		Expect(intent.MetadataRemoved).To(BeTrue(), "resolved provider work no longer blocks metadata removal; independent upload pins can still retain bytes")
		return nil
	})).To(Succeed())
})

var _ = DescribeTable("PA28 terminal local jobs and restored orphan uncertainty retain target applicability until external resolution", func(shape string) {
	f := newFixture(GinkgoT())
	jobID, attemptID := key(GinkgoT()), key(GinkgoT())
	sha := strings.Repeat("a", 64)
	target, _ := json.Marshal(map[string]string{"project_id": f.project, "document_id": f.document, "version_id": f.version})
	if shape == "terminal job" {
		var next domain.ProviderAttempt
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			owner, e := tx.AuthState()
			if e != nil {
				return e
			}
			lib, e := tx.Library()
			if e != nil {
				return e
			}
			config, e := tx.Configuration()
			if e != nil {
				return e
			}
			scope, _ := json.Marshal(domain.ProviderScope{PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, RequestSHA: sha})
			if e = tx.CreateJob(domain.Job{ID: jobID, Kind: "question", Scope: scope, ConfigRevision: config.Revision, Permissions: map[string]int64{f.project: 1}}); e != nil {
				return e
			}
			j, e := tx.ClaimJob(jobID)
			if e != nil {
				return e
			}
			a := domain.ProviderAttempt{ID: attemptID, JobID: jobID, Generation: j.AttemptGeneration, State: "prepared", Runtime: lib.RuntimeGeneration, RequestSHA: sha}
			if e = tx.PrepareProviderAttempt(a, nil); e != nil {
				return e
			}
			if e = tx.DispatchProviderAttempt(a.ID); e != nil {
				return e
			}
			if e = tx.ObserveProviderAttempt(a.ID, "outcome_unknown", "old-turn", "thread", "transport_unknown"); e != nil {
				return e
			}
			if e = tx.PauseProviderJob(jobID, "outcome_unknown"); e != nil {
				return e
			}
			if _, e = tx.ResumeProviderJob(jobID, j.AttemptGeneration, "retry"); e != nil {
				return e
			}
			j, e = tx.ClaimJob(jobID)
			if e != nil {
				return e
			}
			next = a
			next.ID = key(GinkgoT())
			next.Generation = j.AttemptGeneration
			if e = tx.PrepareProviderAttempt(next, nil); e != nil {
				return e
			}
			return tx.DispatchProviderAttempt(next.ID)
		})).To(Succeed())
		_, e := f.store.Put(ctx, strings.NewReader("complete current reply"), func(tx app.WriteTx, o domain.Object) error { return tx.SaveProviderResult(next.ID, o) })
		Expect(e).NotTo(HaveOccurred())
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error { return tx.ApplyProviderResult(next.ID, nil) })).To(Succeed())
	} else {
		raw, _ := json.Marshal(map[string]any{"attempt_id": attemptID, "job_id": jobID, "request_sha256": sha, "state": "outcome_unknown", "job_state": "paused", "cancel_requested": true, "target": json.RawMessage(target)})
		Expect(sqlite.RetainProviderOrphans(ctx, f.dbPath, bytes.NewReader(raw), nil)).To(Succeed())
	}
	lifecycleCommit(f, lifecyclePlan(f, "trash"))
	result := lifecycleCommit(f, lifecyclePlan(f, "purge"))
	purger := &work.Purger{Store: f.store, Bytes: f.store}
	Expect(purger.Process(ctx, result.JobID)).To(Succeed())
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		intent, e := tx.PurgeIntent(result.JobID)
		if e != nil {
			return e
		}
		Expect(intent.MetadataRemoved).To(BeFalse())
		Expect(intent.Phase).To(BeZero())
		return nil
	})).To(Succeed())
	if shape == "terminal job" {
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			return tx.ObserveProviderAttempt(attemptID, "cancelled", "", "", "cancel_confirmed")
		})).To(Succeed())
	} else {
		raw, _ := json.Marshal(map[string]any{"attempt_id": attemptID, "job_id": jobID, "request_sha256": sha, "state": "failed", "reason": "result_unavailable", "job_state": "cancelled", "cancel_requested": true, "target": json.RawMessage(target)})
		Expect(sqlite.RetainProviderOrphans(ctx, f.dbPath, bytes.NewReader(raw), nil)).To(Succeed())
	}
	purgeTurns(f, purger, result.JobID, 100)
	Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
		intent, e := tx.PurgeIntent(result.JobID)
		if e != nil {
			return e
		}
		Expect(intent.MetadataRemoved).To(BeTrue())
		return nil
	})).To(Succeed())
}, Entry("terminal job", "terminal job"), Entry("restored orphan", "restored orphan"), Label("persistence"))
