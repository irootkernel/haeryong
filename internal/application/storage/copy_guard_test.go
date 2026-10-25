package storage

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/domain"
)

type copyGuardTx struct {
	WriteTx
	set        domain.CopySet
	job        domain.Job
	library    domain.Library
	owner      domain.AuthState
	hold       *domain.RecoveryHold
	permission int64
}

func (t copyGuardTx) CopySet(string) (domain.CopySet, error) { return t.set, nil }

func (t copyGuardTx) Job(string) (domain.Job, error)              { return t.job, nil }
func (t copyGuardTx) Library() (domain.Library, error)            { return t.library, nil }
func (t copyGuardTx) AuthState() (domain.AuthState, error)        { return t.owner, nil }
func (t copyGuardTx) RecoveryHold() (*domain.RecoveryHold, error) { return t.hold, nil }
func (t copyGuardTx) Scope(string) (domain.ScopeState, error) {
	return domain.ScopeState{Project: domain.Project{PermissionsRevision: t.permission}}, nil
}

var _ = It("Copies guard equivalence classes fence every detached checkpoint and publication", Label("unit"), func() {
	set := domain.CopySet{ID: "set", Stage: "copying", JobID: "job", LibraryID: "library", Runtime: "runtime", OwnerID: "owner", Permissions: map[string]int64{"project": 3}}
	base := copyGuardTx{set: set, job: domain.Job{ID: "job", State: "running", AttemptGeneration: 2}, library: domain.Library{ID: "library", RuntimeGeneration: "runtime"}, owner: domain.AuthState{OwnerID: "owner"}, permission: 3}
	Expect(GuardCopy(base, set, "job", 2)).To(Succeed())
	for _, state := range []string{"queued", "paused", "interrupted", "cancelling", "cancelled", "failed", "completed"} {
		tx := base
		tx.job.State = state
		Expect(GuardCopy(tx, set, "job", 2)).To(MatchError(domain.ErrStaleWorker))
	}
	for _, generation := range []int64{1, 3} {
		Expect(GuardCopy(base, set, "job", generation)).To(MatchError(domain.ErrStaleWorker))
	}
	tx := base
	tx.job.CancelRequested = true
	Expect(GuardCopy(tx, set, "job", 2)).To(MatchError(domain.ErrStaleWorker))
	for _, change := range []func(*copyGuardTx){func(t *copyGuardTx) { t.library.ID = "foreign" }, func(t *copyGuardTx) { t.library.RuntimeGeneration = "old" }, func(t *copyGuardTx) { t.owner.OwnerID = "foreign" }} {
		tx := base
		change(&tx)
		Expect(GuardCopy(tx, set, "job", 2)).To(MatchError(domain.ErrReconciliation))
	}
	for _, state := range []string{"pending_review", "reviewed", "unknown"} {
		tx := base
		tx.hold = &domain.RecoveryHold{State: state}
		if state == "reviewed" {
			Expect(GuardCopy(tx, set, "job", 2)).To(Succeed())
		} else {
			Expect(GuardCopy(tx, set, "job", 2)).To(MatchError(domain.ErrReconciliation))
		}
	}
	for _, revision := range []int64{0, 2, 4} {
		tx := base
		tx.permission = revision
		Expect(GuardCopy(tx, set, "job", 2)).To(MatchError(domain.ErrDenied))
	}
})

var _ = It("CR2 G7 ownership never follows another selected job with a coincident generation or terminal set", Label("unit"), func() {
	set := domain.CopySet{ID: "set", Stage: "copying", JobID: "J1", LibraryID: "library", Runtime: "runtime", OwnerID: "owner"}
	base := copyGuardTx{set: set, job: domain.Job{ID: "J1", State: "running", AttemptGeneration: 2}, library: domain.Library{ID: "library", RuntimeGeneration: "runtime"}, owner: domain.AuthState{OwnerID: "owner"}}
	for _, stage := range []string{"complete", "abandoned", "expired", "restored_paused"} {
		tx := base
		tx.set.Stage = stage
		Expect(GuardCopy(tx, set, "J1", 2)).To(MatchError(domain.ErrStaleWorker))
		Expect(FailCopy(tx, set, "J1", 2, "copy_io_failure")).To(MatchError(domain.ErrStaleWorker))
	}
	tx := base
	tx.set.JobID = "J2"
	tx.job.ID = "J2"
	Expect(GuardCopy(tx, set, "J1", 2)).To(MatchError(domain.ErrStaleWorker))
	// Even if a caller just refreshed set, selected J1 is still the binding.
	Expect(GuardCopy(tx, tx.set, "J1", 2)).To(MatchError(domain.ErrStaleWorker))
	Expect(FailCopy(tx, tx.set, "J1", 2, "copy_io_failure")).To(MatchError(domain.ErrStaleWorker))
})
