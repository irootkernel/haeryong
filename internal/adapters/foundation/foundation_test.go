package foundation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"haeryong.local/haeryong/internal/adapters/contentstore"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// These are actual private-profile SQLite/filesystem foundation fixtures. They
// establish generic X14/X17 behavior, not HTTP uploads/import publication or a
// full backup implementation.
type fixture struct {
	t             *testing.T
	selectProfile service.Selection
	writer        *service.LockedProfile
	s             *Store
	closer        func() error
	fault         sqlite.Fault
}

func foundationScratch(t *testing.T) string {
	t.Helper()
	base := os.TempDir()
	var volume, parent syscall.Stat_t
	if syscall.Stat("/Volumes/RootKernel", &volume) == nil && syscall.Stat("/Volumes", &parent) == nil && volume.Dev != parent.Dev && syscall.Access("/Volumes/RootKernel", 2) == nil {
		base = "/Volumes/RootKernel/tmp"
		if err := os.MkdirAll(base, 0700); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := os.MkdirTemp(base, "haeryong-task00202-foundation-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	return dir
}

func newFixture(t *testing.T, fault sqlite.Fault) *fixture {
	t.Helper()
	f := &fixture{t: t, fault: fault, selectProfile: service.Selection{Profile: "foundation", DataDir: filepath.Join(foundationScratch(t), "profile")}}
	if _, err := service.Initialize(f.selectProfile); err != nil {
		t.Fatal(err)
	}
	f.open()
	t.Cleanup(f.close)
	return f
}

func (f *fixture) open() {
	f.t.Helper()
	writer, err := service.OpenWriter(f.selectProfile)
	if err != nil {
		f.t.Fatal(err)
	}
	f.writer = writer
	if err := writer.PrepareStorageBootstrap(nil); err != nil {
		writer.Close()
		f.t.Fatal(err)
	}
	f.s = New(f.fault)
	state, closer, err := f.s.Prepare(context.Background(), writer.Profile)
	if err != nil {
		writer.Close()
		f.t.Fatal(err)
	}
	f.closer = closer
	if !state.Ready || state.State != "ready" {
		f.t.Fatal("storage not ready")
	}
	if err := writer.MarkStorageReady(nil); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) close() {
	if f.closer != nil {
		if err := f.closer(); err != nil {
			f.t.Error(err)
		}
		f.closer = nil
	}
	if f.writer != nil {
		if err := f.writer.Close(); err != nil {
			f.t.Error(err)
		}
		f.writer = nil
	}
}

func (f *fixture) reopen() { f.close(); f.open() }

func (f *fixture) objectsWithFault(fault func(contentstore.Boundary) error) {
	f.t.Helper()
	objects, err := contentstore.New(filepath.Join(f.writer.Profile.DataDir, "storage"), contentstore.WithFault(fault))
	if err != nil {
		f.t.Fatal(err)
	}
	f.s.Objects = objects
	// Prepare's closer captures the original objects adapter. Keep the injected
	// adapter explicitly owned and close it before the profile writer is released.
	previous := f.closer
	f.closer = func() error { return errors.Join(objects.Close(), previous()) }
}

func (f *fixture) stage(data string) *contentstore.Staged {
	f.t.Helper()
	staged, err := f.s.Objects.Stage(context.Background(), strings.NewReader(data))
	if err != nil {
		f.t.Fatal(err)
	}
	return staged
}

func (f *fixture) publish(data, pinID string) domain.Object {
	f.t.Helper()
	object, err := f.s.Publish(context.Background(), f.stage(data), domain.Pin{ID: pinID, Kind: "staging", OwnerID: pinID})
	if err != nil {
		f.t.Fatal(err)
	}
	return object
}

func (f *fixture) write(fn func(app.WriteTx) error) error {
	return f.s.Write(context.Background(), fn)
}

func (f *fixture) object(hash string) (domain.Object, error) {
	var object domain.Object
	err := f.write(func(tx app.WriteTx) error { var err error; object, err = tx.Object(hash); return err })
	return object, err
}

func awaitResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("foundation barrier did not settle")
		return nil
	}
}

func awaitBarrier(t *testing.T, barrier <-chan struct{}) {
	t.Helper()
	select {
	case <-barrier:
	case <-time.After(10 * time.Second):
		t.Fatal("foundation barrier was not reached")
	}
}

func TestX14VerifiedReadPinProtectsDeliveryAndCleanup(t *testing.T) {
	f := newFixture(t, nil)
	data := strings.Repeat("verified private bytes\n", 3*1024*1024/23)
	object := f.publish(data, "staging-read")
	entered, releaseConsumer := make(chan struct{}), make(chan struct{})
	closing, releaseClose := make(chan struct{}), make(chan struct{})
	var closeArmed atomic.Bool
	f.objectsWithFault(func(p contentstore.Boundary) error {
		if p == contentstore.FileClose && closeArmed.Load() {
			close(closing)
			<-releaseClose
		}
		return nil
	})
	want := errors.New("consumer interrupted after verified bytes")
	result := make(chan error, 1)
	go func() {
		result <- f.s.Read(context.Background(), object, domain.Pin{ID: "read-lifetime", Kind: "active_read", OwnerID: "reader"}, func(r io.ReadSeeker) error {
			close(entered)
			<-releaseConsumer
			hash := sha256.New()
			if _, err := io.Copy(hash, r); err != nil {
				return err
			}
			if hex.EncodeToString(hash.Sum(nil)) != object.Hash {
				return errors.New("verified delivery changed")
			}
			return want
		})
	}()
	awaitBarrier(t, entered)
	if err := f.write(func(tx app.WriteTx) error { return tx.ReleasePin("staging-read") }); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Collect(context.Background(), object); !errors.Is(err, domain.ErrPinned) {
		t.Fatalf("active read not pinned: %v", err)
	}
	closeArmed.Store(true)
	close(releaseConsumer)
	awaitBarrier(t, closing)
	if err := f.s.Collect(context.Background(), object); !errors.Is(err, domain.ErrPinned) {
		t.Fatalf("cleanup released read pin early: %v", err)
	}
	close(releaseClose)
	if err := awaitResult(t, result); !errors.Is(err, want) {
		t.Fatalf("consumer error lost: %v", err)
	}
	closeArmed.Store(false)
	if err := f.s.Collect(context.Background(), object); err != nil {
		t.Fatal(err)
	}
	if _, err := f.object(object.Hash); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("collected metadata remains: %v", err)
	}
}

func TestX14DurablePinKindsSurviveRestartAndPreventGC(t *testing.T) {
	for _, kind := range []string{"staging", "backup", "job", "recovery", "draft"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t, nil)
			object := f.publish("pin fixture "+kind, "seed")
			if err := f.write(func(tx app.WriteTx) error {
				if err := tx.AcquirePin(domain.Pin{ID: "protected", Kind: kind, OwnerID: "fixture", Hash: object.Hash}); err != nil {
					return err
				}
				return tx.ReleasePin("seed")
			}); err != nil {
				t.Fatal(err)
			}
			f.reopen()
			if err := f.s.Collect(context.Background(), object); !errors.Is(err, domain.ErrPinned) {
				t.Fatalf("%s pin lost on restart: %v", kind, err)
			}
			if err := f.write(func(tx app.WriteTx) error { return tx.ReleasePin("protected") }); err != nil {
				t.Fatal(err)
			}
			if err := f.s.Collect(context.Background(), object); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestX14AdoptionReferenceAndConsumerPinCommitBeforeStagingRelease(t *testing.T) {
	for _, failCommit := range []bool{false, true} {
		t.Run(fmt.Sprintf("commit_failure_%v", failCommit), func(t *testing.T) {
			var armed atomic.Bool
			f := newFixture(t, func(p sqlite.Boundary) error {
				if armed.Load() && p == sqlite.BeforeCommit {
					return syscall.EIO
				}
				return nil
			})
			object := f.publish("adopt exact bytes", "stage")
			ref := domain.Reference{Kind: "saved_context", ID: "context", Hash: object.Hash}
			consumer := domain.Pin{ID: "consumer", Kind: "consumer", OwnerID: "accepted-operation", Hash: object.Hash}
			adopted, release := make(chan struct{}), make(chan struct{})
			result := make(chan error, 1)
			armed.Store(failCommit)
			go func() {
				result <- f.write(func(tx app.WriteTx) error {
					if err := tx.AcquirePin(consumer); err != nil {
						return err
					}
					if err := tx.Adopt(ref, "stage"); err != nil {
						return err
					}
					close(adopted)
					<-release
					return nil
				})
			}()
			awaitBarrier(t, adopted)
			gcStarted, gcResult := make(chan struct{}), make(chan error, 1)
			go func() { close(gcStarted); gcResult <- f.s.Collect(context.Background(), object) }()
			awaitBarrier(t, gcStarted)
			close(release)
			err := awaitResult(t, result)
			if failCommit && !errors.Is(err, syscall.EIO) {
				t.Fatalf("commit fault missing: %v", err)
			}
			if !failCommit && err != nil {
				t.Fatal(err)
			}
			// The serialized collector sees either the original staging pin after
			// rollback, or the committed consumer/ref pair, never an unpinned gap.
			if err := awaitResult(t, gcResult); !errors.Is(err, domain.ErrPinned) {
				t.Fatalf("GC crossed adoption transaction: %v", err)
			}
			armed.Store(false)
			f.reopen()
			probe := errors.New("rollback read-only pin probe")
			err = f.write(func(tx app.WriteTx) error {
				if err := tx.Adopt(ref, "stage"); err != nil {
					return err
				}
				return probe
			})
			if failCommit {
				if !errors.Is(err, probe) {
					t.Fatalf("rollback lost staging pin: %v", err)
				}
				if err := f.write(func(tx app.WriteTx) error { return tx.ReleasePin("stage") }); err != nil {
					t.Fatal(err)
				}
				if err := f.s.Collect(context.Background(), object); err != nil {
					t.Fatalf("failed adoption leaked consumer pin/reference: %v", err)
				}
			} else {
				if !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("staging pin not transferred: %v", err)
				}
				changed := consumer
				changed.OwnerID = "other-owner"
				if err := f.write(func(tx app.WriteTx) error { return tx.AcquirePin(changed) }); !errors.Is(err, domain.ErrConflict) {
					t.Fatalf("consumer pin not committed: %v", err)
				}
				if err := f.write(func(tx app.WriteTx) error { return tx.ReleasePin("consumer") }); err != nil {
					t.Fatal(err)
				}
				if err := f.s.Collect(context.Background(), object); !errors.Is(err, domain.ErrPinned) {
					t.Fatalf("accepted reference not retained: %v", err)
				}
			}
		})
	}
}

func TestX14GCFirstFencesAdoptionUntilFreshDurablePublication(t *testing.T) {
	f := newFixture(t, nil)
	data := "GC reservation and replacement bytes"
	object := f.publish(data, "stage")
	if err := f.write(func(tx app.WriteTx) error { return tx.ReleasePin("stage") }); err != nil {
		t.Fatal(err)
	}
	deleted, settleDelete := make(chan struct{}), make(chan struct{})
	var armed atomic.Bool
	armed.Store(true)
	var publishes atomic.Int32
	f.objectsWithFault(func(p contentstore.Boundary) error {
		if p == contentstore.Rename {
			publishes.Add(1)
		}
		if p == contentstore.DirectorySync && armed.Load() {
			close(deleted)
			<-settleDelete
			return syscall.EIO
		}
		return nil
	})
	replacement := f.stage(data)
	gcResult := make(chan error, 1)
	go func() { gcResult <- f.s.Collect(context.Background(), object) }()
	awaitBarrier(t, deleted)
	current, err := f.object(object.Hash)
	if err != nil || current.State != "deleting" || current.GCToken == "" {
		t.Fatalf("GC reservation not durable: %#v %v", current, err)
	}
	if err := f.write(func(tx app.WriteTx) error {
		return tx.AcquirePin(domain.Pin{ID: "late", Kind: "backup", OwnerID: "late", Hash: object.Hash})
	}); !errors.Is(err, domain.ErrDeleting) {
		t.Fatalf("pin raced reserved deletion: %v", err)
	}
	if err := f.write(func(tx app.WriteTx) error {
		return tx.Adopt(domain.Reference{Kind: "context", ID: "late", Hash: object.Hash}, "")
	}); !errors.Is(err, domain.ErrDeleting) {
		t.Fatalf("adoption raced reserved deletion: %v", err)
	}
	if _, err := f.s.Publish(context.Background(), replacement, domain.Pin{ID: "replacement", Kind: "staging", OwnerID: "replacement"}); !errors.Is(err, domain.ErrDeleting) {
		t.Fatalf("publish raced reserved deletion: %v", err)
	}
	if publishes.Load() != 0 {
		t.Fatal("deleting object reached filesystem publication")
	}
	close(settleDelete)
	if err := awaitResult(t, gcResult); !errors.Is(err, syscall.EIO) {
		t.Fatalf("GC fault missing: %v", err)
	}
	armed.Store(false)
	f.reopen()
	current, err = f.object(object.Hash)
	if err != nil || current.State != "deleting" {
		t.Fatalf("deleting fence lost on restart: %#v %v", current, err)
	}
	if err := f.s.Collect(context.Background(), object); err != nil {
		t.Fatalf("reserved deletion not recoverable: %v", err)
	}
	if err := f.write(func(tx app.WriteTx) error {
		return tx.Adopt(domain.Reference{Kind: "context", ID: "stale", Hash: object.Hash}, "")
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing bytes adopted from stale metadata: %v", err)
	}
	fresh := f.publish(data, "fresh-stage")
	if fresh.Hash != object.Hash {
		t.Fatal("fresh verification changed content identity")
	}
	if err := f.write(func(tx app.WriteTx) error {
		return tx.Adopt(domain.Reference{Kind: "context", ID: "fresh", Hash: fresh.Hash}, "fresh-stage")
	}); err != nil {
		t.Fatal(err)
	}
	f.reopen()
	if err := f.s.Read(context.Background(), fresh, domain.Pin{ID: "fresh-read", Kind: "active_read", OwnerID: "reader"}, func(r io.ReadSeeker) error {
		b, err := io.ReadAll(r)
		if string(b) != data {
			return errors.New("reverified bytes differ")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestX17FilesystemAndSQLiteCommitFaultsNeverPublishReadyMetadata(t *testing.T) {
	for _, point := range []string{"write", "file_sync", "file_close", "rename", "directory_sync", "transaction_commit"} {
		t.Run(point, func(t *testing.T) {
			var armed atomic.Bool
			var dbCommits atomic.Int32
			f := newFixture(t, func(p sqlite.Boundary) error {
				if armed.Load() && p == sqlite.BeforeCommit {
					dbCommits.Add(1)
					if string(p) == point {
						return syscall.ENOSPC
					}
				}
				return nil
			})
			f.objectsWithFault(func(p contentstore.Boundary) error {
				if armed.Load() && string(p) == point {
					return syscall.ENOSPC
				}
				return nil
			})
			armed.Store(true)
			staged, stageErr := f.s.Objects.Stage(context.Background(), strings.NewReader(strings.Repeat("durability", 70000)))
			if staged == nil {
				t.Fatal("missing recovery staging handle")
			}
			_, publishErr := f.s.Publish(context.Background(), staged, domain.Pin{ID: "failed-stage", Kind: "staging", OwnerID: "failed"})
			if publishErr == nil {
				t.Fatal("failed durable write returned publication success")
			}
			wantCommits := int32(0)
			if point == "transaction_commit" {
				wantCommits = 1
			}
			if got := dbCommits.Load(); got != wantCommits {
				t.Fatalf("publication fault was ignored before DB commit: got %d commit attempts, want %d", got, wantCommits)
			}
			if point == "write" || point == "file_sync" || point == "file_close" {
				if !errors.Is(stageErr, syscall.ENOSPC) {
					t.Fatalf("stage fault not observed: %v", stageErr)
				}
			} else if stageErr != nil || !errors.Is(publishErr, syscall.ENOSPC) {
				t.Fatalf("wrong fault: stage=%v publish=%v", stageErr, publishErr)
			}
			armed.Store(false)
			// The real wrapper must return a publish error before inserting READY
			// or a pin; Finalize returning an error alone cannot undo a bad callback.
			if staged.Object.Hash != "" {
				if _, err := f.object(staged.Object.Hash); !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("READY committed after failed publication: %v", err)
				}
			}
			rows, err := f.s.ReferencedObjects(context.Background(), "", 64)
			if err != nil || len(rows) != 0 {
				t.Fatalf("failed publication committed pins/references: %v %#v", err, rows)
			}
			if point == "transaction_commit" || point == "directory_sync" {
				path := filepath.Join(f.writer.Profile.DataDir, "storage", "objects", staged.Object.Hash)
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("recoverable finalized orphan missing: %v", err)
				}
			} else if _, err := os.Stat(filepath.Join(f.writer.Profile.DataDir, "storage", "objects", staged.RecoveryName)); err != nil {
				t.Fatalf("recoverable staging missing: %v", err)
			}
			f.reopen()
			rows, err = f.s.ReferencedObjects(context.Background(), "", 64)
			if err != nil || len(rows) != 0 {
				t.Fatalf("restart exposed failed publication: %v %#v", err, rows)
			}
			if staged.Object.Hash != "" {
				if _, err := f.object(staged.Object.Hash); !errors.Is(err, domain.ErrNotFound) {
					t.Fatalf("restart resurrected READY metadata: %v", err)
				}
			}
		})
	}
}

func TestX17ReopenRejectsDamagedReferencedBytes(t *testing.T) {
	f := newFixture(t, nil)
	object := f.publish("must stay verified", "retained")
	path := filepath.Join(f.writer.Profile.DataDir, "storage", "objects", object.Hash)
	f.close()
	if err := os.WriteFile(path, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	writer, err := service.OpenWriter(f.selectProfile)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	s := New(nil)
	state, closer, err := s.Prepare(context.Background(), writer.Profile)
	if closer != nil {
		closer()
	}
	if err == nil || state.Ready {
		t.Fatalf("damaged pinned bytes advertised ready: state=%#v err=%v", state, err)
	}
}

func TestX14GCExecutorDoesNotReuseInflightReservation(t *testing.T) {
	f := newFixture(t, nil)
	data := "identical bytes published after completed GC"
	object := f.publish(data, "old-staging")
	if err := f.write(func(tx app.WriteTx) error { return tx.ReleasePin("old-staging") }); err != nil {
		t.Fatal(err)
	}
	beforeDelete, releaseDelete := make(chan struct{}), make(chan struct{})
	var deletes atomic.Int32
	f.objectsWithFault(func(point contentstore.Boundary) error {
		if point == contentstore.BeforeDelete && deletes.Add(1) == 1 {
			close(beforeDelete)
			<-releaseDelete
		}
		return nil
	})
	first := make(chan error, 1)
	go func() { first <- f.s.Collect(context.Background(), object) }()
	awaitBarrier(t, beforeDelete)
	// C1 has committed deleting but has not removed its file. C2 must not reuse
	// that live executor's reservation and complete deletion ahead of C1.
	secondStarted, second := make(chan struct{}), make(chan error, 1)
	go func() { close(secondStarted); second <- f.s.Collect(context.Background(), object) }()
	awaitBarrier(t, secondStarted)
	var secondErr error
	crossed := false
	select {
	case secondErr = <-second:
		crossed = true
	case <-time.After(250 * time.Millisecond):
	}
	var fresh domain.Object
	if crossed {
		// Exercise the actual unsafe ordering before reporting the defect: C2
		// completes, a fresh same-hash object becomes pinned, then C1 removes it.
		fresh = f.publish(data, "fresh-staging")
	}
	close(releaseDelete)
	firstErr := awaitResult(t, first)
	if !crossed {
		secondErr = awaitResult(t, second)
		fresh = f.publish(data, "fresh-staging")
	}
	if crossed {
		t.Errorf("second Collect reused an in-flight reservation before the first physical delete: %v", secondErr)
	}
	if firstErr != nil {
		t.Errorf("first GC executor did not finish its own reservation: %v", firstErr)
	}
	if !crossed && !errors.Is(secondErr, domain.ErrNotFound) {
		t.Errorf("second executor did not recheck completed metadata: %v", secondErr)
	}
	if got := deletes.Load(); got != 1 {
		t.Errorf("same reservation entered physical deletion %d times, want once", got)
	}
	if err := f.s.Read(context.Background(), fresh, domain.Pin{ID: "fresh-active-read", Kind: "active_read", OwnerID: "reader"}, func(r io.ReadSeeker) error {
		bytes, err := io.ReadAll(r)
		if string(bytes) != data {
			return errors.New("fresh pinned bytes changed")
		}
		return err
	}); err != nil {
		t.Errorf("late GC deleted freshly published pinned bytes: %v", err)
	}
}

func TestX14GCExecutorReleasesAfterDeleteFailureAndCancellation(t *testing.T) {
	for _, mode := range []string{"delete_failure", "cancelled_finish"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, nil)
			object := f.publish("retry after executor interruption", "old-stage")
			if err := f.write(func(tx app.WriteTx) error { return tx.ReleasePin("old-stage") }); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var injected atomic.Bool
			f.objectsWithFault(func(point contentstore.Boundary) error {
				if point == contentstore.BeforeDelete && injected.CompareAndSwap(false, true) {
					if mode == "delete_failure" {
						return syscall.EIO
					}
					cancel()
				}
				return nil
			})
			err := f.s.Collect(ctx, object)
			want := error(syscall.EIO)
			if mode == "cancelled_finish" {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("executor interruption missing: %v", err)
			}
			current, err := f.object(object.Hash)
			if err != nil || current.State != "deleting" {
				t.Fatalf("failed executor lost recovery fence: %#v %v", current, err)
			}
			retried := make(chan error, 1)
			go func() { retried <- f.s.Collect(context.Background(), object) }()
			if err := awaitResult(t, retried); err != nil {
				t.Fatalf("executor was not released for recovery: %v", err)
			}
			f.reopen()
			if _, err := f.object(object.Hash); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("completed retry lost across restart: %v", err)
			}
		})
	}
}
