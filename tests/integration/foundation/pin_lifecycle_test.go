package foundation_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"haeryong.local/haeryong/internal/adapters/contentstore"
	"haeryong.local/haeryong/internal/application/service"
	storage "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// These synthetic fixtures use actual SQLite, immutable files and profile reopen.
// They do not simulate process death or claim physical power-loss durability.
func pinID(t *testing.T) string {
	t.Helper()
	id, err := service.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func pinWrite(t *testing.T, f *fixture, apply func(storage.WriteTx) error) {
	t.Helper()
	if err := f.store.Write(context.Background(), apply); err != nil {
		t.Fatal(err)
	}
}

func pinObject(t *testing.T, f *fixture, text string) (domain.Object, string) {
	t.Helper()
	stage := pinID(t)
	o, err := f.store.Put(context.Background(), strings.NewReader(text), func(tx storage.WriteTx, o domain.Object) error {
		return tx.AcquirePin(domain.Pin{ID: stage, Kind: "staging", OwnerID: stage, Hash: o.Hash})
	})
	if err != nil {
		t.Fatal(err)
	}
	return o, stage
}

func pinProtected(t *testing.T, f *fixture, o domain.Object) {
	t.Helper()
	if err := f.store.Collect(context.Background(), o); !errors.Is(err, domain.ErrPinned) {
		t.Fatalf("live ownership did not protect bytes: %v", err)
	}
}

func TestPinLifecycleReadOutlivesRecoveredJobCancellationAndStaging(t *testing.T) {
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
	if err != nil || recovered.State != "interrupted" || recovered.AttemptGeneration <= old.AttemptGeneration {
		t.Fatalf("restart did not fence running job: state=%s error=%v", recovered.State, err)
	}
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
				t.Errorf("consumer failure or pin cleanup lost: %v", err)
			}
			if err := f.store.Collect(context.Background(), o); err != nil {
				t.Errorf("read cleanup retained terminal ownership: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("verified reader did not terminate")
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("verified reader did not reach delivery barrier")
	}
	pinWrite(t, f, func(tx storage.WriteTx) error { return tx.ConfirmStop(job, cancelled.AttemptGeneration) })
	settled, err := f.store.Job(context.Background(), job)
	if err != nil || settled.State != "cancelled" || !settled.CancelRequested {
		t.Fatalf("confirmed cancellation was not durable: state=%s error=%v", settled.State, err)
	}
	// Only the in-flight read remains: terminal job cleanup must not release it.
	pinProtected(t, f, o)
	// The deferred cleanup checks that collection succeeds only after delivery ends.
}

func TestPinLifecycleReassignmentFencesOwnershipAndOutboxEffects(t *testing.T) {
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
		if !errors.Is(err, domain.ErrStaleWorker) || applied {
			t.Fatalf("stale attempt applied ownership: callback=%v error=%v", applied, err)
		}
	}
	pinWrite(t, f, func(tx storage.WriteTx) error {
		_, after, err := tx.EventHead()
		if err == nil && after != head {
			t.Fatal("stale finalization emitted an event")
		}
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
	if err := f.store.Collect(context.Background(), o); err != nil {
		t.Fatalf("terminal ownership was not released: %v", err)
	}
}

func TestPinLifecycleRollbackOrphanRequiresFreshVerifiedAdoption(t *testing.T) {
	f := newFixture(t)
	text := "durable but unpublished synthetic orphan"
	digest := sha256.Sum256([]byte(text))
	o := domain.Object{Hash: hex.EncodeToString(digest[:]), Size: int64(len(text)), State: "ready"}
	want := errors.New("synthetic metadata callback rollback")
	_, err := f.store.Put(context.Background(), strings.NewReader(text), func(storage.WriteTx, domain.Object) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("publication rollback returned success: %v", err)
	}
	f.reopen(t)
	err = f.store.Write(context.Background(), func(tx storage.WriteTx) error {
		return tx.AcquirePin(domain.Pin{ID: pinID(t), Kind: "job", OwnerID: "orphan", Hash: o.Hash})
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unpublished orphan acquired READY ownership: %v", err)
	}
	if err := f.store.Objects.VerifiedRead(context.Background(), contentstore.Object{Hash: o.Hash, Size: o.Size}, func(r io.ReadSeeker) error {
		got, err := io.ReadAll(r)
		if err == nil && string(got) != text {
			return errors.New("orphan bytes changed")
		}
		return err
	}); err != nil {
		t.Fatalf("durable orphan was not recoverable: %v", err)
	}
	recovered, stage := pinObject(t, f, text)
	if recovered.Hash != o.Hash || recovered.Size != o.Size {
		t.Fatal("fresh verified adoption changed immutable identity")
	}
	pinProtected(t, f, recovered)
	pinWrite(t, f, func(tx storage.WriteTx) error { return tx.ReleasePin(stage) })
	if err := f.store.Collect(context.Background(), recovered); err != nil {
		t.Fatal(err)
	}
}
