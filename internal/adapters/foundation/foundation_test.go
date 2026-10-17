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
	"time"

	. "github.com/onsi/ginkgo/v2"
	"haeryong.local/haeryong/internal/adapters/contentstore"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"

	// These are actual private-profile SQLite/filesystem foundation fixtures. They
	// establish generic X14/X17 behavior, not HTTP uploads/import publication or a
	// full backup implementation.
	. "github.com/onsi/gomega"
)

type fixture struct {
	t             GinkgoTInterface
	selectProfile service.Selection
	writer        *service.LockedProfile
	s             *Store
	closer        func() error
	fault         sqlite.Fault
}

func foundationScratch(t GinkgoTInterface) string {
	GinkgoHelper()
	base := os.TempDir()
	var volume, parent syscall.Stat_t
	if syscall.Stat("/Volumes/RootKernel", &volume) == nil && syscall.Stat("/Volumes", &parent) == nil && volume.Dev != parent.Dev && syscall.Access("/Volumes/RootKernel", 2) == nil {
		base = "/Volumes/RootKernel/tmp"
		{
			err := os.MkdirAll(base, 0700)
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

	}
	dir, err := os.MkdirTemp(base, "haeryong-task00202-foundation-test-")
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	DeferCleanup(func() {
		{
			err := os.RemoveAll(dir)
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

	})
	return dir
}

func newFixture(t GinkgoTInterface, fault sqlite.Fault) *fixture {
	GinkgoHelper()
	f := &fixture{t: t, fault: fault, selectProfile: service.Selection{Profile: "foundation", DataDir: filepath.Join(foundationScratch(t), "profile")}}
	{
		_, err := service.Initialize(f.selectProfile)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	f.open()
	DeferCleanup(f.close)
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

func awaitResult(t GinkgoTInterface, result <-chan error) error {
	GinkgoHelper()
	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		Fail(fmt.Sprint("foundation barrier did not settle"))
		return nil
	}
}

func awaitBarrier(t GinkgoTInterface, barrier <-chan struct{}) {
	GinkgoHelper()
	select {
	case <-barrier:
	case <-time.After(10 * time.Second):
		Fail(fmt.Sprint("foundation barrier was not reached"))
	}
}

var _ = It("TestX14VerifiedReadPinProtectsDeliveryAndCleanup", Label("persistence"), func() {
	t := GinkgoT()

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
		defer GinkgoRecover()
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
	{
		err := f.write(func(tx app.WriteTx) error { return tx.ReleasePin("staging-read") })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		err := f.s.Collect(context.Background(), object)
		Expect(!errors.Is(err, domain.ErrPinned)).To(BeFalse(), func() string { return fmt.Sprintf("active read not pinned: %v", err) })
	}

	closeArmed.Store(true)
	close(releaseConsumer)
	awaitBarrier(t, closing)
	{
		err := f.s.Collect(context.Background(), object)
		Expect(!errors.Is(err, domain.ErrPinned)).To(BeFalse(), func() string { return fmt.Sprintf("cleanup released read pin early: %v", err) })
	}

	close(releaseClose)
	{
		err := awaitResult(t, result)
		Expect(!errors.Is(err, want)).To(BeFalse(), func() string { return fmt.Sprintf("consumer error lost: %v", err) })
	}

	closeArmed.Store(false)
	{
		err := f.s.Collect(context.Background(), object)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		_, err := f.object(object.Hash)
		Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprintf("collected metadata remains: %v", err) })
	}

})
var _ = It("TestX14DurablePinKindsSurviveRestartAndPreventGC", Label("persistence"), func() {
	t := GinkgoT()

	for _, kind := range []string{"staging", "backup", "job", "recovery", "draft"} {
		func() {
			By(kind)
			f := newFixture(t, nil)
			object := f.publish("pin fixture "+kind, "seed")
			{
				err := f.write(func(tx app.WriteTx) error {
					if err := tx.AcquirePin(domain.Pin{ID: "protected", Kind: kind, OwnerID: "fixture", Hash: object.Hash}); err != nil {
						return err
					}
					return tx.ReleasePin("seed")
				})
				Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
			}

			f.reopen()
			{
				err := f.s.Collect(context.Background(), object)
				Expect(!errors.Is(err, domain.ErrPinned)).To(BeFalse(), func() string { return fmt.Sprintf("%s pin lost on restart: %v", kind, err) })
			}
			{

				err := f.write(func(tx app.WriteTx) error { return tx.ReleasePin("protected") })
				Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
			}
			{

				err := f.s.Collect(context.Background(), object)
				Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
			}

		}()
	}
})
var _ = It("TestX14AdoptionReferenceAndConsumerPinCommitBeforeStagingRelease", Label("persistence"), func() {
	t := GinkgoT()

	for _, failCommit := range []bool{false, true} {
		func() {
			By(fmt.Sprintf("commit_failure_%v", failCommit))
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
				defer GinkgoRecover()
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
			go func() { defer GinkgoRecover(); close(gcStarted); gcResult <- f.s.Collect(context.Background(), object) }()
			awaitBarrier(t, gcStarted)
			close(release)
			err := awaitResult(t, result)
			Expect(failCommit && !errors.Is(err, syscall.EIO)).To(BeFalse(), func() string { return fmt.Sprintf("commit fault missing: %v", err) })
			Expect(!failCommit && err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
			{

				// The serialized collector sees either the original staging pin after
				// rollback, or the committed consumer/ref pair, never an unpinned gap.
				err := awaitResult(t, gcResult)
				Expect(!errors.Is(err, domain.ErrPinned)).To(BeFalse(), func() string { return fmt.Sprintf("GC crossed adoption transaction: %v", err) })
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
				Expect(!errors.Is(err, probe)).To(BeFalse(), func() string { return fmt.Sprintf("rollback lost staging pin: %v", err) })
				{

					err := f.write(func(tx app.WriteTx) error { return tx.ReleasePin("stage") })
					Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
				}
				{

					err := f.s.Collect(context.Background(), object)
					Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("failed adoption leaked consumer pin/reference: %v", err) })
				}

			} else {
				Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprintf("staging pin not transferred: %v", err) })

				changed := consumer
				changed.OwnerID = "other-owner"
				{
					err := f.write(func(tx app.WriteTx) error { return tx.AcquirePin(changed) })
					Expect(!errors.Is(err, domain.ErrConflict)).To(BeFalse(), func() string { return fmt.Sprintf("consumer pin not committed: %v", err) })
				}
				{

					err := f.write(func(tx app.WriteTx) error { return tx.ReleasePin("consumer") })
					Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
				}
				{

					err := f.s.Collect(context.Background(), object)
					Expect(!errors.Is(err, domain.ErrPinned)).To(BeFalse(), func() string { return fmt.Sprintf("accepted reference not retained: %v", err) })
				}

			}
		}()
	}
})
var _ = It("TestX14GCFirstFencesAdoptionUntilFreshDurablePublication", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t, nil)
	data := "GC reservation and replacement bytes"
	object := f.publish(data, "stage")
	{
		err := f.write(func(tx app.WriteTx) error { return tx.ReleasePin("stage") })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
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
	go func() { defer GinkgoRecover(); gcResult <- f.s.Collect(context.Background(), object) }()
	awaitBarrier(t, deleted)
	current, err := f.object(object.Hash)
	Expect(err != nil || current.State != "deleting" || current.GCToken == "").To(BeFalse(), func() string { return fmt.Sprintf("GC reservation not durable: %#v %v", current, err) })
	{

		err := f.write(func(tx app.WriteTx) error {
			return tx.AcquirePin(domain.Pin{ID: "late", Kind: "backup", OwnerID: "late", Hash: object.Hash})
		})
		Expect(!errors.Is(err, domain.ErrDeleting)).To(BeFalse(), func() string { return fmt.Sprintf("pin raced reserved deletion: %v", err) })
	}
	{

		err := f.write(func(tx app.WriteTx) error {
			return tx.Adopt(domain.Reference{Kind: "context", ID: "late", Hash: object.Hash}, "")
		})
		Expect(!errors.Is(err, domain.ErrDeleting)).To(BeFalse(), func() string { return fmt.Sprintf("adoption raced reserved deletion: %v", err) })
	}
	{

		_, err := f.s.Publish(context.Background(), replacement, domain.Pin{ID: "replacement", Kind: "staging", OwnerID: "replacement"})
		Expect(!errors.Is(err, domain.ErrDeleting)).To(BeFalse(), func() string { return fmt.Sprintf("publish raced reserved deletion: %v", err) })
	}
	Expect(publishes.Load() != 0).To(BeFalse(), func() string { return fmt.Sprint("deleting object reached filesystem publication") })

	close(settleDelete)
	{
		err := awaitResult(t, gcResult)
		Expect(!errors.Is(err, syscall.EIO)).To(BeFalse(), func() string { return fmt.Sprintf("GC fault missing: %v", err) })
	}

	armed.Store(false)
	f.reopen()
	current, err = f.object(object.Hash)
	Expect(err != nil || current.State != "deleting").To(BeFalse(), func() string { return fmt.Sprintf("deleting fence lost on restart: %#v %v", current, err) })
	{

		err := f.s.Collect(context.Background(), object)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("reserved deletion not recoverable: %v", err) })
	}
	{

		err := f.write(func(tx app.WriteTx) error {
			return tx.Adopt(domain.Reference{Kind: "context", ID: "stale", Hash: object.Hash}, "")
		})
		Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprintf("missing bytes adopted from stale metadata: %v", err) })
	}

	fresh := f.publish(data, "fresh-stage")
	Expect(fresh.Hash != object.Hash).To(BeFalse(), func() string { return fmt.Sprint("fresh verification changed content identity") })
	{

		err := f.write(func(tx app.WriteTx) error {
			return tx.Adopt(domain.Reference{Kind: "context", ID: "fresh", Hash: fresh.Hash}, "fresh-stage")
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	f.reopen()
	{
		err := f.s.Read(context.Background(), fresh, domain.Pin{ID: "fresh-read", Kind: "active_read", OwnerID: "reader"}, func(r io.ReadSeeker) error {
			b, err := io.ReadAll(r)
			if string(b) != data {
				return errors.New("reverified bytes differ")
			}
			return err
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

})
var _ = It("TestX17FilesystemAndSQLiteCommitFaultsNeverPublishReadyMetadata", Label("persistence"), func() {
	t := GinkgoT()

	for _, point := range []string{"write", "file_sync", "file_close", "rename", "directory_sync", "transaction_commit"} {
		func() {
			By(point)
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
			Expect(staged == nil).To(BeFalse(), func() string { return fmt.Sprint("missing recovery staging handle") })

			_, publishErr := f.s.Publish(context.Background(), staged, domain.Pin{ID: "failed-stage", Kind: "staging", OwnerID: "failed"})
			Expect(publishErr == nil).To(BeFalse(), func() string { return fmt.Sprint("failed durable write returned publication success") })

			wantCommits := int32(0)
			if point == "transaction_commit" {
				wantCommits = 1
			}
			{
				got := dbCommits.Load()
				Expect(got != wantCommits).To(BeFalse(), func() string {
					return fmt.Sprintf("publication fault was ignored before DB commit: got %d commit attempts, want %d", got, wantCommits)
				})
			}

			if point == "write" || point == "file_sync" || point == "file_close" {
				Expect(!errors.Is(stageErr, syscall.ENOSPC)).To(BeFalse(), func() string { return fmt.Sprintf("stage fault not observed: %v", stageErr) })

			} else if stageErr != nil || !errors.Is(publishErr, syscall.ENOSPC) {
				Fail(fmt.Sprintf("wrong fault: stage=%v publish=%v", stageErr, publishErr))
			}
			armed.Store(false)
			// The real wrapper must return a publish error before inserting READY
			// or a pin; Finalize returning an error alone cannot undo a bad callback.
			if staged.Object.Hash != "" {
				{
					_, err := f.object(staged.Object.Hash)
					Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprintf("READY committed after failed publication: %v", err) })
				}

			}
			rows, err := f.s.ReferencedObjects(context.Background(), "", 64)
			Expect(err != nil || len(rows) != 0).To(BeFalse(), func() string { return fmt.Sprintf("failed publication committed pins/references: %v %#v", err, rows) })

			if point == "transaction_commit" || point == "directory_sync" {
				path := filepath.Join(f.writer.Profile.DataDir, "storage", "objects", staged.Object.Hash)
				{
					_, err := os.Stat(path)
					Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("recoverable finalized orphan missing: %v", err) })
				}

			} else if _, err := os.Stat(filepath.Join(f.writer.Profile.DataDir, "storage", "objects", staged.RecoveryName)); err != nil {
				Fail(fmt.Sprintf("recoverable staging missing: %v", err))
			}
			f.reopen()
			rows, err = f.s.ReferencedObjects(context.Background(), "", 64)
			Expect(err != nil || len(rows) != 0).To(BeFalse(), func() string { return fmt.Sprintf("restart exposed failed publication: %v %#v", err, rows) })

			if staged.Object.Hash != "" {
				{
					_, err := f.object(staged.Object.Hash)
					Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprintf("restart resurrected READY metadata: %v", err) })
				}

			}
		}()
	}
})
var _ = It("TestX17ReopenRejectsDamagedReferencedBytes", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t, nil)
	object := f.publish("must stay verified", "retained")
	path := filepath.Join(f.writer.Profile.DataDir, "storage", "objects", object.Hash)
	f.close()
	{
		err := os.WriteFile(path, []byte("damaged"), 0600)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	writer, err := service.OpenWriter(f.selectProfile)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	defer writer.Close()
	s := New(nil)
	state, closer, err := s.Prepare(context.Background(), writer.Profile)
	if closer != nil {
		closer()
	}
	Expect(err == nil || state.Ready).To(BeFalse(), func() string {
		return fmt.Sprintf("damaged pinned bytes advertised ready: state=%#v err=%v", state, err)
	})

})
var _ = It("TestX14GCExecutorDoesNotReuseInflightReservation", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t, nil)
	data := "identical bytes published after completed GC"
	object := f.publish(data, "old-staging")
	{
		err := f.write(func(tx app.WriteTx) error { return tx.ReleasePin("old-staging") })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
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
	go func() { defer GinkgoRecover(); first <- f.s.Collect(context.Background(), object) }()
	awaitBarrier(t, beforeDelete)
	// C1 has committed deleting but has not removed its file. C2 must not reuse
	// that live executor's reservation and complete deletion ahead of C1.
	secondStarted, second := make(chan struct{}), make(chan error, 1)
	go func() {
		defer GinkgoRecover()
		close(secondStarted)
		second <- f.s.Collect(context.Background(), object)
	}()
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
	Expect(crossed).To(BeFalse(), func() string {
		return fmt.Sprintf("second Collect reused an in-flight reservation before the first physical delete: %v", secondErr)
	})
	Expect(firstErr != nil).To(BeFalse(), func() string {
		return fmt.Sprintf("first GC executor did not finish its own reservation: %v", firstErr)
	})
	Expect(!crossed && !errors.Is(secondErr, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprintf("second executor did not recheck completed metadata: %v", secondErr) })
	{

		got := deletes.Load()
		Expect(got != 1).To(BeFalse(), func() string {
			return fmt.Sprintf("same reservation entered physical deletion %d times, want once", got)
		})
	}
	{

		err := f.s.Read(context.Background(), fresh, domain.Pin{ID: "fresh-active-read", Kind: "active_read", OwnerID: "reader"}, func(r io.ReadSeeker) error {
			bytes, err := io.ReadAll(r)
			if string(bytes) != data {
				return errors.New("fresh pinned bytes changed")
			}
			return err
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("late GC deleted freshly published pinned bytes: %v", err) })
	}

})
var _ = It("TestX14GCExecutorReleasesAfterDeleteFailureAndCancellation", Label("persistence"), func() {
	t := GinkgoT()

	for _, mode := range []string{"delete_failure", "cancelled_finish"} {
		func() {
			By(mode)
			f := newFixture(t, nil)
			object := f.publish("retry after executor interruption", "old-stage")
			{
				err := f.write(func(tx app.WriteTx) error { return tx.ReleasePin("old-stage") })
				Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
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
			Expect(!errors.Is(err, want)).To(BeFalse(), func() string { return fmt.Sprintf("executor interruption missing: %v", err) })

			current, err := f.object(object.Hash)
			Expect(err != nil || current.State != "deleting").To(BeFalse(), func() string { return fmt.Sprintf("failed executor lost recovery fence: %#v %v", current, err) })

			retried := make(chan error, 1)
			go func() { defer GinkgoRecover(); retried <- f.s.Collect(context.Background(), object) }()
			{
				err := awaitResult(t, retried)
				Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("executor was not released for recovery: %v", err) })
			}

			f.reopen()
			{
				_, err := f.object(object.Hash)
				Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprintf("completed retry lost across restart: %v", err) })
			}

		}()
	}
})
