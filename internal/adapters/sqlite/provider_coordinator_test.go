package sqlite

import (
	"context"
	"crypto/sha256"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"strings"
	"sync/atomic"
)

type providerFake struct {
	preparationError     error
	preparationReason    string
	before               func()
	calls                int
	failure              bool
	reconciled           string
	lookups              int
	afterProgress        func() error
	requestID, sessionID string
	outputAllowed        bool
	reconciledBody       io.ReadCloser
	completionObserved   bool
	withoutCompletion    bool
	noInputObservation   bool
	inputObservedAt      *string
	cancels              atomic.Int64
	cancelled            chan struct{}
}

func (d *providerFake) Execute(ctx context.Context, a domain.ProviderAttempt, inputs []domain.ProviderInput, send func() error, observe func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	if d.before != nil {
		d.before()
	}
	if d.preparationError != nil {
		return work.ProviderOutcome{Reason: d.preparationReason}, d.preparationError
	}
	if e := send(); e != nil {
		return work.ProviderOutcome{}, e
	}
	d.calls++
	for i := range inputs {
		inputs[i].ObservedOrigin = "daemon_supplied"
		inputs[i].ObservedAt = d.inputObservedAt
	}
	requestID, sessionID := d.requestID, d.sessionID
	if requestID == "" {
		requestID = "observed-request"
	}
	if sessionID == "" {
		sessionID = "observed-session"
	}
	observed := inputs
	if d.noInputObservation {
		observed = nil
	}
	if e := observe(work.ProviderProgress{RequestID: requestID, SessionID: sessionID, Observed: observed}); e != nil {
		return work.ProviderOutcome{}, e
	}
	if d.afterProgress != nil {
		if e := d.afterProgress(); e != nil {
			return work.ProviderOutcome{}, e
		}
	}
	if d.failure {
		return work.ProviderOutcome{State: "outcome_unknown", RequestID: requestID, SessionID: sessionID, Observed: inputs, Reason: "transport_unknown"}, nil
	}
	return work.ProviderOutcome{State: "result_saved", CompletionObserved: !d.withoutCompletion, RequestID: requestID, SessionID: sessionID, Observed: inputs, Body: io.NopCloser(strings.NewReader("complete fake provider output"))}, nil
}
func (d *providerFake) Cancel(context.Context, domain.ProviderAttempt) error {
	if d.cancels.Add(1) == 1 && d.cancelled != nil {
		close(d.cancelled)
	}
	return nil
}
func (d *providerFake) Reconcile(_ context.Context, _ domain.ProviderAttempt, outputAllowed bool) (work.ProviderOutcome, error) {
	d.outputAllowed = outputAllowed
	d.lookups++
	if d.reconciled != "" {
		return work.ProviderOutcome{State: d.reconciled, CompletionObserved: d.completionObserved, Reason: "cancel_confirmed", Body: d.reconciledBody}, nil
	}
	return work.ProviderOutcome{State: "outcome_unknown", Reason: "transport_unknown"}, nil
}

var _ = It("PA14 cancelled uncertain work performs explicit reconciliation without new execution", Label("persistence"), func() {
	s, a := providerFixture(false)
	d := &providerFake{failure: true}
	p := work.ProviderCoordinator{Store: s, Driver: d, Bytes: providerFixtureBytes{s}}
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.CancelJob(a.JobID)
		if e != nil {
			return e
		}
		_, e = tx.ResumeProviderJob(j.ID, j.AttemptGeneration, "reconcile_only")
		return e
	})).To(Succeed())
	d.reconciled = "cancelled"
	Expect(p.Process(ctx, a.JobID)).To(Succeed())
	Expect(d.calls).To(Equal(1))
	Expect(d.lookups).To(Equal(1))
	j, e := s.Job(ctx, a.JobID)
	Expect(e).NotTo(HaveOccurred())
	Expect(j.State).To(Equal("cancelled"))
	Expect(j.CancelRequested).To(BeTrue())
})

type providerFixtureBytes struct{ s *Store }

func (b providerFixtureBytes) Put(ctx context.Context, r io.Reader, commit func(app.WriteTx, domain.Object) error) (domain.Object, error) {
	data, e := io.ReadAll(r)
	if e != nil {
		return domain.Object{}, e
	}
	hash := sha256.Sum256(data)
	o := domain.Object{Hash: fmt.Sprintf("%x", hash), Size: int64(len(data)), State: "ready"}
	e = b.s.Write(ctx, func(tx app.WriteTx) error {
		if e := tx.PutDurableObject(o); e != nil {
			return e
		}
		return commit(tx, o)
	})
	return o, e
}

var _ = It("PA06 cancel or policy restriction during native startup prevents the external send", Label("persistence"), func() {
	for _, change := range []string{"cancel", "policy"} {
		s, a := providerFixture(false)
		d := &providerFake{}
		d.before = func() {
			Expect(s.Write(ctx, func(tx app.WriteTx) error {
				if change == "cancel" {
					_, e := tx.CancelJob(a.JobID)
					return e
				}
				lib, e := tx.Library()
				if e != nil {
					return e
				}
				p, e := tx.Scope(lib.UnfiledProjectID)
				if e != nil {
					return e
				}
				return tx.ApplyProjectConfiguration(p.Project.ID, "", "", true, p.Project.PermissionsRevision)
			})).To(Succeed())
		}
		p := work.ProviderCoordinator{Store: s, Driver: d, Bytes: providerFixtureBytes{s}}
		Expect(p.Process(ctx, a.JobID)).To(Succeed())
		Expect(d.calls).To(BeZero())
	}
})
var _ = It("PA07 successful and interrupted outcomes persist actual identities and input observations separately", Label("persistence"), func() {
	for _, failure := range []bool{false, true} {
		s, a := providerFixture(false)
		d := &providerFake{failure: failure}
		p := work.ProviderCoordinator{Store: s, Driver: d, Bytes: providerFixtureBytes{s}}
		Expect(p.Process(ctx, a.JobID)).To(Succeed())
		Expect(d.calls).To(Equal(1))
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			attempts, e := tx.ProviderAttempts(a.JobID)
			if e != nil {
				return e
			}
			Expect(attempts).To(HaveLen(1))
			v := attempts[0]
			Expect(v.RequestID).To(Equal("observed-request"))
			Expect(v.SessionID).To(Equal("observed-session"))
			inputs, e := tx.ProviderInputs(v.ID)
			if e != nil {
				return e
			}
			Expect(inputs[0].ObservedOrigin).To(Equal("daemon_supplied"))
			Expect(inputs[0].ObservedAt).NotTo(BeNil())
			if failure {
				Expect(v.State).To(Equal("outcome_unknown"))
			} else {
				Expect(v.State).To(Equal("result_saved"))
				Expect(v.ResultHash).NotTo(BeEmpty())
			}
			return nil
		})).To(Succeed())
		j, e := s.Job(ctx, a.JobID)
		Expect(e).NotTo(HaveOccurred())
		if failure {
			Expect(j.State).To(Equal("paused"))
		} else {
			Expect(j.State).To(Equal("completed"))
		}
	}
})
