package recovery

import (
	"context"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type phaseStore struct {
	app.Store
	tx *phaseTx
}

func (s phaseStore) Write(_ context.Context, f func(app.WriteTx) error) error { return f(s.tx) }

type phaseTx struct {
	app.WriteTx
	job     domain.Job
	set     domain.CopySet
	lib     domain.Library
	stopped bool
}

func (t *phaseTx) Job(string) (domain.Job, error)         { return t.job, nil }
func (t *phaseTx) CopySet(string) (domain.CopySet, error) { return t.set, nil }
func (t *phaseTx) PutCopySet(s domain.CopySet) error      { t.set = s; return nil }
func (t *phaseTx) Library() (domain.Library, error)       { return t.lib, nil }
func (t *phaseTx) AuthState() (domain.AuthState, error) {
	return domain.AuthState{OwnerID: "owner"}, nil
}
func (t *phaseTx) RecoveryHold() (*domain.RecoveryHold, error) { return nil, nil }
func (t *phaseTx) ConfirmStop(string, int64) error {
	t.stopped = true
	t.job.State = "cancelled"
	return nil
}

var _ = It("CR1 G7 owned-slot polling fences generation and cancellation without WorkJobs selection and retains terminal drain ownership", Label("unit"), func() {
	for _, mode := range []string{"generation", "cancelled", "runtime", "terminal_retention"} {
		tx := &phaseTx{job: domain.Job{ID: "job", State: "running", AttemptGeneration: 2}, set: domain.CopySet{ID: "set", JobID: "job", LibraryID: "library", Runtime: "runtime", OwnerID: "owner"}, lib: domain.Library{ID: "library", RuntimeGeneration: "runtime"}}
		owned, cancel := context.WithCancel(context.Background())
		phase := &copyPhase{set: tx.set, job: tx.job, cancel: cancel, done: make(chan error, 1)}
		s := &Service{Store: phaseStore{tx: tx}}
		s.active.Store(phase)
		switch mode {
		case "generation":
			tx.job.AttemptGeneration++
		case "cancelled":
			tx.job.CancelRequested = true
			tx.job.State = "cancelling"
			tx.job.AttemptGeneration++
		case "runtime":
			tx.lib.RuntimeGeneration = "new"
		case "terminal_retention":
			tx.job.State = "completed"
		}
		Expect(s.PollCopy(context.Background())).To(Succeed())
		if mode == "terminal_retention" {
			Expect(owned.Err()).NotTo(HaveOccurred())
		} else {
			Expect(owned.Err()).To(MatchError(context.Canceled))
		}
		Expect(s.active.Load()).To(BeIdenticalTo(phase))
		Expect(tx.stopped).To(BeFalse())
		phase.done <- nil
		close(phase.done)
		Expect(s.PollCopy(context.Background())).To(Succeed())
		Expect(s.active.Load()).To(BeNil())
		Expect(tx.stopped).To(Equal(mode == "cancelled"))
	}
})

type failedPhaseStore struct {
	app.Store
	tx      *phaseTx
	failure error
}

func (s *failedPhaseStore) Write(_ context.Context, f func(app.WriteTx) error) error {
	if s.failure != nil {
		return s.failure
	}
	return f(s.tx)
}
func (t *phaseTx) FinishJob(_ string, _ int64, code string, _ func(app.WriteTx) error) error {
	t.job.State = "failed"
	t.job.Error = code
	return nil
}

var _ = It("CR2 G7 drained completion retains ownership until durable failure acknowledgement", Label("unit"), func() {
	tx := &phaseTx{job: domain.Job{ID: "job", State: "running", AttemptGeneration: 2}, set: domain.CopySet{ID: "set", Stage: "copying", JobID: "job", LibraryID: "library", Runtime: "runtime", OwnerID: "owner"}, lib: domain.Library{ID: "library", RuntimeGeneration: "runtime"}}
	failure := errors.New("failure transition unavailable")
	store := &failedPhaseStore{tx: tx, failure: failure}
	s := &Service{Store: store}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	phase := &copyPhase{set: tx.set, job: tx.job, cancel: cancel, done: make(chan error, 1), stopReason: domain.ErrDenied}
	phase.done <- context.Canceled
	close(phase.done)
	s.active.Store(phase)
	Expect(s.PollCopy(context.Background())).To(MatchError(failure))
	Expect(s.active.Load()).To(BeIdenticalTo(phase))
	Expect(phase.drained).To(BeTrue())
	Expect(tx.job.State).To(Equal("running"))
	store.failure = nil
	Expect(s.PollCopy(context.Background())).To(Succeed())
	Expect(s.active.Load()).To(BeNil())
	Expect(tx.job.State).To(Equal("failed"))
	Expect(tx.set.Stage).To(Equal("failed"))
	Expect(tx.job.Error).To(Equal("copy_authority_changed"))
})
