package contentstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func scratch(t *testing.T) string {
	t.Helper()
	base := os.TempDir()
	var volume, parent syscall.Stat_t
	if syscall.Stat("/Volumes/RootKernel", &volume) == nil && syscall.Stat("/Volumes", &parent) == nil && volume.Dev != parent.Dev && syscall.Access("/Volumes/RootKernel", 2) == nil {
		base = "/Volumes/RootKernel/tmp"
		if err := os.MkdirAll(base, 0700); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := os.MkdirTemp(base, "haeryong-contentstore-test-")
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

func newStore(t *testing.T, options ...Option) (*Store, string) {
	t.Helper()
	dir := scratch(t)
	s, err := New(dir, options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, filepath.Join(dir, "objects")
}

func stage(t *testing.T, s *Store, data string) *Staged {
	t.Helper()
	staged, err := s.Stage(context.Background(), strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return staged
}

func finalize(t *testing.T, s *Store, staged *Staged) Object {
	t.Helper()
	object, err := s.Finalize(context.Background(), staged, func(_ Object, publish func() error) error { return publish() })
	if err != nil {
		t.Fatal(err)
	}
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

func TestStreamDurabilityOrderingAndDuplicateVerification(t *testing.T) {
	var events []string
	s, dir := newStore(t, WithFault(func(p Boundary) error { events = append(events, string(p)); return nil }))
	reader := &boundedReader{left: 9*1024*1024 + 17}
	staged, err := s.Stage(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}
	if reader.max > bufferSize || staged.Object.Size != 9*1024*1024+17 {
		t.Fatalf("not finite-buffer streaming: max=%d size=%d", reader.max, staged.Object.Size)
	}
	object, err := s.Finalize(context.Background(), staged, func(object Object, publish func() error) error {
		events = append(events, "transaction")
		if err := publish(); err != nil {
			return err
		}
		events = append(events, "metadata_commit")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := events[len(events)-6:]; !reflect.DeepEqual(got, []string{"file_sync", "file_close", "transaction", "rename", "directory_sync", "metadata_commit"}) {
		t.Fatalf("durability order: %v", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, object.Hash))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != object.Hash {
		t.Fatal("wrong streamed digest")
	}
	duplicate := stage(t, s, string(data))
	if got := finalize(t, s, duplicate); got != object {
		t.Fatal("identical bytes did not deduplicate identity")
	}
	if err := os.WriteFile(filepath.Join(dir, object.Hash), []byte("corrupt target"), 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = s.Finalize(context.Background(), stage(t, s, string(data)), func(_ Object, _ func() error) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("unverified target trusted by its name: called=%v err=%v", called, err)
	}
}

func TestPublicationFaultsLeaveRecoverableUnpublishedBytes(t *testing.T) {
	for _, boundary := range []Boundary{Write, FileSync, FileClose, Rename, DirectorySync, "metadata_commit"} {
		t.Run(string(boundary), func(t *testing.T) {
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
			if ready || finalErr == nil || staged == nil {
				t.Fatalf("false publication: stage=%v final=%v ready=%v", stageErr, finalErr, ready)
			}
			if boundary == Write || boundary == FileSync || boundary == FileClose {
				if !errors.Is(stageErr, injected) || called {
					t.Fatalf("stage failure reached commit: stage=%v called=%v", stageErr, called)
				}
			} else if stageErr != nil || !errors.Is(finalErr, injected) {
				t.Fatalf("wrong fault boundary: stage=%v final=%v", stageErr, finalErr)
			}
			name := staged.RecoveryName
			if staged.published {
				name = staged.Object.Hash
			}
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				t.Fatalf("recovery bytes removed: %v", err)
			}
			armed = false
			if boundary == "metadata_commit" {
				if err := s.VerifiedRead(context.Background(), staged.Object, func(r io.ReadSeeker) error { _, err := io.Copy(io.Discard, r); return err }); err != nil {
					t.Fatalf("durable orphan unavailable: %v", err)
				}
			}
		})
	}
}

type failedInput struct{ sent bool }

func (r *failedInput) Read(b []byte) (int, error) {
	if r.sent {
		return 0, syscall.EIO
	}
	r.sent = true
	return copy(b, "retained partial input"), nil
}

func TestPartialWriteAndInputFailureRetainRecoveryFile(t *testing.T) {
	for _, kind := range []string{"partial_write", "input_failure"} {
		t.Run(kind, func(t *testing.T) {
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
			if err == nil || staged == nil || staged.complete || staged.Object.Size == 0 {
				t.Fatalf("partial failure lost recovery: staged=%#v err=%v", staged, err)
			}
			info, err := os.Stat(filepath.Join(dir, staged.RecoveryName))
			if err != nil || info.Size() != staged.Object.Size {
				t.Fatalf("partial byte checkpoint mismatch: %v", err)
			}
		})
	}
}

func TestVerifiedReadDeliversSnapshotAndRejectsDamageBeforeConsumer(t *testing.T) {
	s, dir := newStore(t)
	data := strings.Repeat("source bytes", bufferSize)
	object := finalize(t, s, stage(t, s, data))
	err := s.VerifiedRead(context.Background(), object, func(r io.ReadSeeker) error {
		if _, ok := r.(io.Writer); ok {
			t.Fatal("delivery exposed a writable handle")
		}
		if err := os.WriteFile(filepath.Join(dir, object.Hash), bytes.Repeat([]byte{'!'}, len(data)), 0600); err != nil {
			return err
		}
		got, err := io.ReadAll(r)
		if string(got) != data {
			t.Fatal("delivery reopened mutable source after verification")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = s.VerifiedRead(context.Background(), object, func(io.ReadSeeker) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("damaged object reached consumer: called=%v err=%v", called, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".delivery-") {
			t.Fatal("transient delivery leaked")
		}
	}
}

func TestRejectSymlinkHardlinkAndStagingSubstitution(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "public_file", "stage_substitution"} {
		t.Run(kind, func(t *testing.T) {
			s, dir := newStore(t)
			staged := stage(t, s, "exact bytes")
			path := filepath.Join(dir, staged.Object.Hash)
			other := filepath.Join(dir, "other")
			if err := os.WriteFile(other, []byte("exact bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "symlink":
				if err := os.Symlink("other", path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(other, path); err != nil {
					t.Fatal(err)
				}
			case "public_file":
				if err := os.WriteFile(path, []byte("exact bytes"), 0644); err != nil {
					t.Fatal(err)
				}
				// Establish the unsafe mode even when the caller uses a private umask.
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
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
			if err == nil || ready {
				t.Fatalf("unsafe file accepted: ready=%v err=%v", ready, err)
			}
		})
	}
}

func TestCancelInputAndDeliveryErrorsNeverSucceed(t *testing.T) {
	s, dir := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	staged, err := s.Stage(ctx, strings.NewReader("cancelled"))
	if !errors.Is(err, context.Canceled) || staged == nil || staged.complete {
		t.Fatalf("cancelled stage: %v %#v", err, staged)
	}
	object := finalize(t, s, stage(t, s, "keep me"))
	called := false
	if err := s.VerifiedRead(ctx, object, func(io.ReadSeeker) error { called = true; return nil }); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancelled delivery: %v called=%v", err, called)
	}
	want := errors.New("consumer interrupted")
	if err := s.VerifiedRead(context.Background(), object, func(io.ReadSeeker) error { return want }); !errors.Is(err, want) {
		t.Fatal(err)
	}
	for _, point := range []Boundary{Write, FileClose} {
		s.fault = func(p Boundary) error {
			if p == point {
				return syscall.ENOSPC
			}
			return nil
		}
		if err := s.VerifiedRead(context.Background(), object, func(io.ReadSeeker) error { return nil }); !errors.Is(err, syscall.ENOSPC) {
			t.Fatalf("delivery fault %s returned %v", point, err)
		}
	}
	s.fault = nil
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".delivery-") {
			t.Fatal("failed delivery leaked")
		}
	}
}

func TestPublicationRequiresExactlyOneSuccessfulClosure(t *testing.T) {
	for _, kind := range []string{"skipped", "repeated", "ignored_failure"} {
		t.Run(kind, func(t *testing.T) {
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
			if err == nil {
				t.Fatal("invalid metadata callback reported success")
			}
		})
	}
}

func TestDeleteSyncFailureRetainsRetryableGCBoundary(t *testing.T) {
	s, dir := newStore(t)
	object := finalize(t, s, stage(t, s, "collect after reservation"))
	s.fault = func(p Boundary) error {
		if p == DirectorySync {
			return syscall.EIO
		}
		return nil
	}
	if err := s.Delete(object); !errors.Is(err, syscall.EIO) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, object.Hash)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	s.fault = nil
	if err := s.Delete(object); err != nil {
		t.Fatalf("reserved deletion not retryable: %v", err)
	}
}
