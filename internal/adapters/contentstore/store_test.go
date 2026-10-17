package contentstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
)

func scratch(t GinkgoTInterface) string {
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
	dir, err := os.MkdirTemp(base, "haeryong-contentstore-test-")
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	DeferCleanup(func() {
		{
			err := os.RemoveAll(dir)
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

	})
	return dir
}

func newStore(t GinkgoTInterface, options ...Option) (*Store, string) {
	GinkgoHelper()
	dir := scratch(t)
	s, err := New(dir, options...)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	DeferCleanup(func() { s.Close() })
	return s, filepath.Join(dir, "objects")
}

func stage(t GinkgoTInterface, s *Store, data string) *Staged {
	GinkgoHelper()
	staged, err := s.Stage(context.Background(), strings.NewReader(data))
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return staged
}

func finalize(t GinkgoTInterface, s *Store, staged *Staged) Object {
	GinkgoHelper()
	object, err := s.Finalize(context.Background(), staged, func(_ Object, publish func() error) error { return publish() })
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return object
}

type boundedReader struct {
	left int64
	max  int
}

func (r *boundedReader) Read(b []byte) (int, error) {
	if len(b) > r.max {
		r.max = len(b)
	}
	if r.left == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(b)), r.left)
	for i := range b[:n] {
		b[i] = byte((r.left - int64(i)) % 251)
	}
	r.left -= n
	return int(n), nil
}

var _ = It("TestStreamDurabilityOrderingAndDuplicateVerification", Label("unit"), func() {
	t := GinkgoT()

	var events []string
	s, dir := newStore(t, WithFault(func(p Boundary) error { events = append(events, string(p)); return nil }))
	reader := &boundedReader{left: 9*1024*1024 + 17}
	staged, err := s.Stage(context.Background(), reader)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	Expect(reader.max > bufferSize || staged.Object.Size != 9*1024*1024+17).To(BeFalse(), func() string {
		return fmt.Sprintf("not finite-buffer streaming: max=%d size=%d", reader.max, staged.Object.Size)
	})

	object, err := s.Finalize(context.Background(), staged, func(object Object, publish func() error) error {
		events = append(events, "transaction")
		if err := publish(); err != nil {
			return err
		}
		events = append(events, "metadata_commit")
		return nil
	})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	{

		got := events[len(events)-6:]
		Expect(!reflect.DeepEqual(got, []string{"file_sync", "file_close", "transaction", "rename", "directory_sync", "metadata_commit"})).To(BeFalse(), func() string { return fmt.Sprintf("durability order: %v", got) })
	}

	data, err := os.ReadFile(filepath.Join(dir, object.Hash))
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	digest := sha256.Sum256(data)
	Expect(hex.EncodeToString(digest[:]) != object.Hash).To(BeFalse(), func() string { return fmt.Sprint("wrong streamed digest") })

	duplicate := stage(t, s, string(data))
	{
		got := finalize(t, s, duplicate)
		Expect(got != object).To(BeFalse(), func() string { return fmt.Sprint("identical bytes did not deduplicate identity") })
	}
	{

		err := os.WriteFile(filepath.Join(dir, object.Hash), []byte("corrupt target"), 0600)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	called := false
	_, err = s.Finalize(context.Background(), stage(t, s, string(data)), func(_ Object, _ func() error) error { called = true; return nil })
	Expect(err == nil || called).To(BeFalse(), func() string {
		return fmt.Sprintf("unverified target trusted by its name: called=%v err=%v", called, err)
	})

})
var _ = It("TestPublicationFaultsLeaveRecoverableUnpublishedBytes", Label("unit"), func() {
	t := GinkgoT()

	for _, boundary := range []Boundary{Write, FileSync, FileClose, Rename, DirectorySync, "metadata_commit"} {
		func() {
			By(string(boundary))
			injected := syscall.ENOSPC
			armed := true
			s, dir := newStore(t, WithFault(func(point Boundary) error {
				if armed && point == boundary {
					return injected
				}
				return nil
			}))
			staged, stageErr := s.Stage(context.Background(), strings.NewReader(strings.Repeat("x", bufferSize+1)))
			ready, called := false, false
			_, finalErr := s.Finalize(context.Background(), staged, func(_ Object, publish func() error) error {
				called = true
				if err := publish(); err != nil {
					return err
				}
				if boundary == "metadata_commit" {
					return injected
				}
				ready = true
				return nil
			})
			Expect(ready || finalErr == nil || staged == nil).To(BeFalse(), func() string {
				return fmt.Sprintf("false publication: stage=%v final=%v ready=%v", stageErr, finalErr, ready)
			})

			if boundary == Write || boundary == FileSync || boundary == FileClose {
				Expect(!errors.Is(stageErr, injected) || called).To(BeFalse(), func() string {
					return fmt.Sprintf("stage failure reached commit: stage=%v called=%v", stageErr, called)
				})

			} else if stageErr != nil || !errors.Is(finalErr, injected) {
				Fail(fmt.Sprintf("wrong fault boundary: stage=%v final=%v", stageErr, finalErr))
			}
			name := staged.RecoveryName
			if staged.published {
				name = staged.Object.Hash
			}
			{
				_, err := os.Stat(filepath.Join(dir, name))
				Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("recovery bytes removed: %v", err) })
			}

			armed = false
			if boundary == "metadata_commit" {
				{
					err := s.VerifiedRead(context.Background(), staged.Object, func(r io.ReadSeeker) error { _, err := io.Copy(io.Discard, r); return err })
					Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("durable orphan unavailable: %v", err) })
				}

			}
		}()
	}
})

type failedInput struct{ sent bool }

func (r *failedInput) Read(b []byte) (int, error) {
	if r.sent {
		return 0, syscall.EIO
	}
	r.sent = true
	return copy(b, "retained partial input"), nil
}

var _ = It("TestPartialWriteAndInputFailureRetainRecoveryFile", Label("unit"), func() {
	t := GinkgoT()

	for _, kind := range []string{"partial_write", "input_failure"} {
		func() {
			By(kind)
			writes := 0
			s, dir := newStore(t, WithFault(func(p Boundary) error {
				if p == Write {
					writes++
					if kind == "partial_write" && writes == 2 {
						return syscall.ENOSPC
					}
				}
				return nil
			}))
			var input io.Reader = strings.NewReader(strings.Repeat("x", bufferSize+1))
			if kind == "input_failure" {
				input = &failedInput{}
			}
			staged, err := s.Stage(context.Background(), input)
			Expect(err == nil || staged == nil || staged.complete || staged.Object.Size == 0).To(BeFalse(), func() string { return fmt.Sprintf("partial failure lost recovery: staged=%#v err=%v", staged, err) })

			info, err := os.Stat(filepath.Join(dir, staged.RecoveryName))
			Expect(err != nil || info.Size() != staged.Object.Size).To(BeFalse(), func() string { return fmt.Sprintf("partial byte checkpoint mismatch: %v", err) })

		}()
	}
})
var _ = It("TestVerifiedReadDeliversSnapshotAndRejectsDamageBeforeConsumer", Label("unit"), func() {
	t := GinkgoT()

	s, dir := newStore(t)
	data := strings.Repeat("source bytes", bufferSize)
	object := finalize(t, s, stage(t, s, data))
	err := s.VerifiedRead(context.Background(), object, func(r io.ReadSeeker) error {
		{
			_, ok := r.(io.Writer)
			Expect(ok).To(BeFalse(), func() string { return fmt.Sprint("delivery exposed a writable handle") })
		}

		if err := os.WriteFile(filepath.Join(dir, object.Hash), bytes.Repeat([]byte{'!'}, len(data)), 0600); err != nil {
			return err
		}
		got, err := io.ReadAll(r)
		Expect(string(got) != data).To(BeFalse(), func() string { return fmt.Sprint("delivery reopened mutable source after verification") })

		return err
	})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	called := false
	err = s.VerifiedRead(context.Background(), object, func(io.ReadSeeker) error { called = true; return nil })
	Expect(err == nil || called).To(BeFalse(), func() string { return fmt.Sprintf("damaged object reached consumer: called=%v err=%v", called, err) })

	entries, err := os.ReadDir(dir)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	for _, entry := range entries {
		Expect(strings.HasPrefix(entry.Name(), ".delivery-")).To(BeFalse(), func() string { return fmt.Sprint("transient delivery leaked") })

	}
})
var _ = It("TestRejectSymlinkHardlinkAndStagingSubstitution", Label("unit"), func() {
	t := GinkgoT()

	for _, kind := range []string{"symlink", "hardlink", "public_file", "stage_substitution"} {
		func() {
			By(kind)
			s, dir := newStore(t)
			staged := stage(t, s, "exact bytes")
			path := filepath.Join(dir, staged.Object.Hash)
			other := filepath.Join(dir, "other")
			{
				err := os.WriteFile(other, []byte("exact bytes"), 0600)
				Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
			}

			switch kind {
			case "symlink":
				if err := os.Symlink("other", path); err != nil {
					Fail(fmt.Sprint(err))
				}
			case "hardlink":
				if err := os.Link(other, path); err != nil {
					Fail(fmt.Sprint(err))
				}
			case "public_file":
				if err := os.WriteFile(path, []byte("exact bytes"), 0644); err != nil {
					Fail(fmt.Sprint(err))
				}
				// Establish the unsafe mode even when the caller uses a private umask.
				if err := os.Chmod(path, 0644); err != nil {
					Fail(fmt.Sprint(err))
				}
			}
			ready := false
			_, err := s.Finalize(context.Background(), staged, func(_ Object, publish func() error) error {
				if kind == "stage_substitution" {
					stagePath := filepath.Join(dir, staged.RecoveryName)
					if err := os.Remove(stagePath); err != nil {
						return err
					}
					if err := os.Symlink("other", stagePath); err != nil {
						return err
					}
				}
				if err := publish(); err != nil {
					return err
				}
				ready = true
				return nil
			})
			Expect(err == nil || ready).To(BeFalse(), func() string { return fmt.Sprintf("unsafe file accepted: ready=%v err=%v", ready, err) })

		}()
	}
})
var _ = It("TestCancelInputAndDeliveryErrorsNeverSucceed", Label("unit"), func() {
	t := GinkgoT()

	s, dir := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	staged, err := s.Stage(ctx, strings.NewReader("cancelled"))
	Expect(!errors.Is(err, context.Canceled) || staged == nil || staged.complete).To(BeFalse(), func() string { return fmt.Sprintf("cancelled stage: %v %#v", err, staged) })

	object := finalize(t, s, stage(t, s, "keep me"))
	called := false
	{
		err := s.VerifiedRead(ctx, object, func(io.ReadSeeker) error { called = true; return nil })
		Expect(!errors.Is(err, context.Canceled) || called).To(BeFalse(), func() string { return fmt.Sprintf("cancelled delivery: %v called=%v", err, called) })
	}

	want := errors.New("consumer interrupted")
	{
		err := s.VerifiedRead(context.Background(), object, func(io.ReadSeeker) error { return want })
		Expect(!errors.Is(err, want)).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	for _, point := range []Boundary{Write, FileClose} {
		s.fault = func(p Boundary) error {
			if p == point {
				return syscall.ENOSPC
			}
			return nil
		}
		{
			err := s.VerifiedRead(context.Background(), object, func(io.ReadSeeker) error { return nil })
			Expect(!errors.Is(err, syscall.ENOSPC)).To(BeFalse(), func() string { return fmt.Sprintf("delivery fault %s returned %v", point, err) })
		}

	}
	s.fault = nil
	entries, err := os.ReadDir(dir)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	for _, entry := range entries {
		Expect(strings.HasPrefix(entry.Name(), ".delivery-")).To(BeFalse(), func() string { return fmt.Sprint("failed delivery leaked") })

	}
})
var _ = It("TestPublicationRequiresExactlyOneSuccessfulClosure", Label("unit"), func() {
	t := GinkgoT()

	for _, kind := range []string{"skipped", "repeated", "ignored_failure"} {
		func() {
			By(kind)
			s, _ := newStore(t)
			staged := stage(t, s, "publish protocol")
			if kind == "ignored_failure" {
				s.fault = func(p Boundary) error {
					if p == Rename {
						return syscall.EIO
					}
					return nil
				}
			}
			_, err := s.Finalize(context.Background(), staged, func(_ Object, publish func() error) error {
				if kind != "skipped" {
					_ = publish()
				}
				if kind == "repeated" {
					_ = publish()
				}
				return nil
			})
			Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("invalid metadata callback reported success") })

		}()
	}
})
var _ = It("TestDeleteSyncFailureRetainsRetryableGCBoundary", Label("unit"), func() {
	t := GinkgoT()

	s, dir := newStore(t)
	object := finalize(t, s, stage(t, s, "collect after reservation"))
	s.fault = func(p Boundary) error {
		if p == DirectorySync {
			return syscall.EIO
		}
		return nil
	}
	{
		err := s.Delete(object)
		Expect(!errors.Is(err, syscall.EIO)).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		_, err := os.Stat(filepath.Join(dir, object.Hash))
		Expect(!errors.Is(err, os.ErrNotExist)).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	s.fault = nil
	{
		err := s.Delete(object)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("reserved deletion not retryable: %v", err) })
	}

})
