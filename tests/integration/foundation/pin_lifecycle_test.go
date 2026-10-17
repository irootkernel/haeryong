package foundation_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"time"

	. "github.com/onsi/ginkgo/v2"
	"haeryong.local/haeryong/internal/adapters/contentstore"
	"haeryong.local/haeryong/internal/application/service"
	storage "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"

	// These synthetic fixtures use actual SQLite, immutable files and profile reopen.
	// They do not simulate process death or claim physical power-loss durability.
	"fmt"
	. "github.com/onsi/gomega"
)

func pinID(t GinkgoTInterface) string {
	GinkgoHelper()
	id, err := service.NewID()
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return id
}

func pinWrite(t GinkgoTInterface, f *fixture, apply func(storage.WriteTx) error) {
	GinkgoHelper()
	{
		err := f.store.Write(context.Background(), apply)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

}

func pinObject(t GinkgoTInterface, f *fixture, text string) (domain.Object, string) {
	GinkgoHelper()
	stage := pinID(t)
	o, err := f.store.Put(context.Background(), strings.NewReader(text), func(tx storage.WriteTx, o domain.Object) error {
		return tx.AcquirePin(domain.Pin{ID: stage, Kind: "staging", OwnerID: stage, Hash: o.Hash})
	})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return o, stage
}

func pinProtected(t GinkgoTInterface, f *fixture, o domain.Object) {
	GinkgoHelper()
	{
		err := f.store.Collect(context.Background(), o)
		Expect(!errors.Is(err, domain.ErrPinned)).To(BeFalse(), func() string { return fmt.Sprintf("live ownership did not protect bytes: %v", err) })
	}

}

var _ = It("TestPinLifecycleReadOutlivesRecoveredJobCancellationAndStaging", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	o, stage := pinObject(t, f, strings.Repeat("verified cancellation read\n", 4096))
	job := pinID(t)
	var old domain.Job
	pinWrite(t, f, func(tx storage.WriteTx) error {
		if err := tx.CreateJob(domain.Job{ID: job, Kind: "import"}); err != nil {
			return err
		}
		var err error
		old, err = tx.ClaimJob(job)
		if err != nil {
			return err
		}
		return tx.AcquirePin(domain.Pin{ID: pinID(t), Kind: "job", OwnerID: job, Hash: o.Hash})
	})
	f.reopen(t)
	recovered, err := f.store.Job(context.Background(), job)
	Expect(err != nil || recovered.State != "interrupted" || recovered.AttemptGeneration <= old.AttemptGeneration).To(BeFalse(), func() string {
		return fmt.Sprintf("restart did not fence running job: state=%s error=%v", recovered.State, err)
	})

	pinProtected(t, f, o)
	var cancelled domain.Job
	pinWrite(t, f, func(tx storage.WriteTx) error {
		if err := tx.ReconcileJob(job); err != nil {
			return err
		}
		if _, err := tx.ClaimJob(job); err != nil {
			return err
		}
		var err error
		cancelled, err = tx.CancelJob(job)
		return err
	})
	// Removing staging still leaves job-owned bytes until confirmed worker stop.
	pinWrite(t, f, func(tx storage.WriteTx) error { return tx.ReleasePin(stage) })
	pinProtected(t, f, o)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	consumerErr := errors.New("synthetic consumer stopped after verified delivery")
	go func() {
		defer GinkgoRecover()
		done <- f.store.Read(context.Background(), o, domain.Pin{Kind: "active_read", OwnerID: job}, func(r io.ReadSeeker) error {
			close(entered)
			<-release
			h := sha256.New()
			if _, err := io.Copy(h, r); err != nil {
				return err
			}
			if hex.EncodeToString(h.Sum(nil)) != o.Hash {
				return errors.New("verified read delivered different bytes")
			}
			return consumerErr
		})
	}()
	// Unblock and await before fixture cleanup even on an assertion failure.
	defer func() {
		close(release)
		select {
		case err := <-done:
			if !errors.Is(err, consumerErr) {
				Fail(fmt.Sprintf("consumer failure or pin cleanup lost: %v", err))
			}
			if err := f.store.Collect(context.Background(), o); err != nil {
				Fail(fmt.Sprintf("read cleanup retained terminal ownership: %v", err))
			}
		case <-time.After(5 * time.Second):
			Fail(fmt.Sprint("verified reader did not terminate"))
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		Fail(fmt.Sprint("verified reader did not reach delivery barrier"))
	}
	pinWrite(t, f, func(tx storage.WriteTx) error { return tx.ConfirmStop(job, cancelled.AttemptGeneration) })
	settled, err := f.store.Job(context.Background(), job)
	Expect(err != nil || settled.State != "cancelled" || !settled.CancelRequested).To(BeFalse(), func() string {
		return fmt.Sprintf("confirmed cancellation was not durable: state=%s error=%v", settled.State, err)
	})

	// Only the in-flight read remains: terminal job cleanup must not release it.
	pinProtected(t, f, o)
	// The deferred cleanup checks that collection succeeds only after delivery ends.
})
var _ = It("TestPinLifecycleReassignmentFencesOwnershipAndOutboxEffects", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	o, stage := pinObject(t, f, "reassignment retains immutable job input")
	job, jobPin, consumer := pinID(t), pinID(t), pinID(t)
	var old domain.Job
	pinWrite(t, f, func(tx storage.WriteTx) error {
		if err := tx.CreateJob(domain.Job{ID: job, Kind: "import"}); err != nil {
			return err
		}
		var err error
		old, err = tx.ClaimJob(job)
		if err != nil {
			return err
		}
		return tx.AcquirePin(domain.Pin{ID: jobPin, Kind: "job", OwnerID: job, Hash: o.Hash})
	})
	f.reopen(t)
	var current domain.Job
	pinWrite(t, f, func(tx storage.WriteTx) error {
		if err := tx.ReconcileJob(job); err != nil {
			return err
		}
		var err error
		current, err = tx.ClaimJob(job)
		return err
	})
	var head int64
	pinWrite(t, f, func(tx storage.WriteTx) error { _, h, err := tx.EventHead(); head = h; return err })
	for _, failure := range []string{"", "synthetic late failure"} {
		applied := false
		err := f.store.Write(context.Background(), func(tx storage.WriteTx) error {
			return tx.FinishJob(job, old.AttemptGeneration, failure, func(tx storage.WriteTx) error {
				applied = true
				return tx.ReleasePin(jobPin)
			})
		})
		Expect(!errors.Is(err, domain.ErrStaleWorker) || applied).To(BeFalse(), func() string {
			return fmt.Sprintf("stale attempt applied ownership: callback=%v error=%v", applied, err)
		})

	}
	pinWrite(t, f, func(tx storage.WriteTx) error {
		_, after, err := tx.EventHead()
		Expect(err == nil && after != head).To(BeFalse(), func() string { return fmt.Sprint("stale finalization emitted an event") })

		return err
	})
	pinProtected(t, f, o)
	pinWrite(t, f, func(tx storage.WriteTx) error {
		return tx.FinishJob(job, current.AttemptGeneration, "", func(tx storage.WriteTx) error {
			if err := tx.AcquirePin(domain.Pin{ID: consumer, Kind: "backup", OwnerID: consumer, Hash: o.Hash}); err != nil {
				return err
			}
			if err := tx.ReleasePin(stage); err != nil {
				return err
			}
			return tx.ReleasePin(jobPin)
		})
	})
	f.reopen(t)
	pinProtected(t, f, o)
	pinWrite(t, f, func(tx storage.WriteTx) error { return tx.ReleasePin(consumer) })
	{
		err := f.store.Collect(context.Background(), o)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("terminal ownership was not released: %v", err) })
	}

})
var _ = It("TestPinLifecycleRollbackOrphanRequiresFreshVerifiedAdoption", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t)
	text := "durable but unpublished synthetic orphan"
	digest := sha256.Sum256([]byte(text))
	o := domain.Object{Hash: hex.EncodeToString(digest[:]), Size: int64(len(text)), State: "ready"}
	want := errors.New("synthetic metadata callback rollback")
	_, err := f.store.Put(context.Background(), strings.NewReader(text), func(storage.WriteTx, domain.Object) error { return want })
	Expect(!errors.Is(err, want)).To(BeFalse(), func() string { return fmt.Sprintf("publication rollback returned success: %v", err) })

	f.reopen(t)
	err = f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		return tx.AcquirePin(domain.Pin{ID: pinID(t), Kind: "job", OwnerID: "orphan", Hash: o.Hash})
	})
	Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprintf("unpublished orphan acquired READY ownership: %v", err) })
	{

		err := f.store.Objects.VerifiedRead(context.Background(), contentstore.Object{Hash: o.Hash, Size: o.Size}, func(r io.ReadSeeker) error {
			got, err := io.ReadAll(r)
			if err == nil && string(got) != text {
				return errors.New("orphan bytes changed")
			}
			return err
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("durable orphan was not recoverable: %v", err) })
	}

	recovered, stage := pinObject(t, f, text)
	Expect(recovered.Hash != o.Hash || recovered.Size != o.Size).To(BeFalse(), func() string { return fmt.Sprint("fresh verified adoption changed immutable identity") })

	pinProtected(t, f, recovered)
	pinWrite(t, f, func(tx storage.WriteTx) error { return tx.ReleasePin(stage) })
	{
		err := f.store.Collect(context.Background(), recovered)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

})
