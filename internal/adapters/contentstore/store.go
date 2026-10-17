// Package contentstore stores private, content-addressed immutable bytes.
// Metadata ownership, pins and GC reservations belong to the SQLite adapter.
package contentstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

const bufferSize = 64 * 1024

// ErrIntegrity distinguishes verified bytes that do not match their immutable identity.
var ErrIntegrity = errors.New("object digest or size mismatch")

type Object struct {
	Hash string
	Size int64
}

// Boundary identifies an actual filesystem operation for deterministic fault
// injection. A returned error prevents that operation and all later publication.
type Boundary string

const (
	Write         Boundary = "write"
	BeforeDelete  Boundary = "before_delete"
	FileSync      Boundary = "file_sync"
	FileClose     Boundary = "file_close"
	Rename        Boundary = "rename"
	DirectorySync Boundary = "directory_sync"
)

type Option func(*Store)

func WithFault(f func(Boundary) error) Option { return func(s *Store) { s.fault = f } }

type Store struct {
	root  *os.Root
	fault func(Boundary) error
}

// Staged retains a recovery locator even after failure. Before publication,
// RecoveryName identifies the private staging file; after rename Object.Hash
// identifies the durable orphan if the metadata transaction fails.
type Staged struct {
	Object       Object
	RecoveryName string
	store        *Store
	complete     bool
	published    bool
}

// New requires an existing private storage directory. Objects and staging use
// one flat destination-local directory; author paths are metadata, not OS paths.
func New(storageDir string, options ...Option) (*Store, error) {
	info, err := os.Lstat(storageDir)
	if err != nil {
		return nil, err
	}
	if err := privateDirectory(info); err != nil {
		return nil, err
	}
	storage, err := os.OpenRoot(storageDir)
	if err != nil {
		return nil, err
	}
	defer storage.Close()
	opened, err := storage.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("storage directory changed while opening")
	}
	if err := storage.Mkdir("objects", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err = storage.Lstat("objects")
	if err != nil {
		return nil, err
	}
	if err := privateDirectory(info); err != nil {
		return nil, err
	}
	root, err := storage.OpenRoot("objects")
	if err != nil {
		return nil, err
	}
	opened, err = root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, errors.New("object directory changed while opening")
	}
	if err := syncRoot(storage); err != nil {
		root.Close()
		return nil, err
	}
	s := &Store{root: root}
	for _, option := range options {
		option(s)
	}
	return s, nil
}

func (s *Store) Close() error { return s.root.Close() }

func (s *Store) fail(point Boundary) error {
	if s.fault != nil {
		return s.fault(point)
	}
	return nil
}

// Stage copies from any input filesystem using finite memory. Failure preserves
// the partial staging file and returns its locator; it never yields a complete
// object. Stage does not create READY metadata or impose a total-byte limit.
func (s *Store) Stage(ctx context.Context, input io.Reader) (*Staged, error) {
	f, name, err := s.temporary(".stage-")
	if err != nil {
		return nil, err
	}
	staged := &Staged{RecoveryName: name, store: s}
	object, copyErr := copyObject(ctx, input, f, s.fail)
	staged.Object = object
	if copyErr == nil {
		copyErr = s.fail(FileSync)
	}
	if copyErr == nil {
		copyErr = f.Sync()
	}
	err = errors.Join(copyErr, s.closeFile(f))
	staged.complete = err == nil
	return staged, err
}

// Finalize verifies staged and any existing target bytes before entering the
// metadata transaction. commit MUST serialize with pin/adoption/GC decisions,
// reject deleting objects before calling publish, call publish synchronously,
// then durably commit READY and its staging pin together. nil means that commit
// actually succeeded; an error must never be reported as READY success.
//
// publish performs only destination-local rename and directory sync. It replaces
// a verified duplicate with the same verified bytes, retaining old reader handles.
// A failed metadata commit leaves a recoverable durable orphan, never auto-deleted.
func (s *Store) Finalize(ctx context.Context, staged *Staged, commit func(Object, func() error) error) (Object, error) {
	if staged == nil || staged.store != s || !staged.complete || staged.published || commit == nil {
		return Object{}, errors.New("invalid or already published staging handle")
	}
	object := staged.Object
	stagedInfo, err := s.root.Lstat(staged.RecoveryName)
	if err != nil {
		return Object{}, err
	}
	if err := s.verify(ctx, staged.RecoveryName, object); err != nil {
		return Object{}, fmt.Errorf("staged object unavailable: %w", err)
	}
	if _, err := s.root.Lstat(object.Hash); err == nil {
		if err := s.verify(ctx, object.Hash, object); err != nil {
			return Object{}, fmt.Errorf("existing object damaged: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Object{}, err
	}
	if err := ctx.Err(); err != nil {
		return Object{}, err
	}
	called := false
	var publishErr error
	err = commit(object, func() error {
		if called {
			publishErr = errors.New("object publication called more than once")
			return publishErr
		}
		called = true
		publishErr = ctx.Err()
		if publishErr == nil {
			current, err := s.root.Lstat(staged.RecoveryName)
			if err != nil {
				publishErr = err
			} else if !current.Mode().IsRegular() || !os.SameFile(stagedInfo, current) || current.Size() != stagedInfo.Size() || !current.ModTime().Equal(stagedInfo.ModTime()) {
				publishErr = errors.New("staging file changed before publication")
			}
		}
		if publishErr == nil {
			publishErr = s.fail(Rename)
		}
		if publishErr == nil {
			publishErr = s.root.Rename(staged.RecoveryName, object.Hash)
			staged.published = publishErr == nil
		}
		if publishErr == nil {
			publishErr = s.fail(DirectorySync)
		}
		if publishErr == nil {
			publishErr = syncRoot(s.root)
		}
		return publishErr
	})
	if !called && err == nil {
		err = errors.New("metadata commit skipped object publication")
	}
	if err := errors.Join(err, publishErr); err != nil {
		return Object{}, err
	}
	return object, nil
}

// VerifiedRead requires a previously committed active-read pin held until this
// method returns. It hashes a private delivery copy before exposing any bytes,
// unlinks that copy, then delivers through that same read-only interface/handle.
// The original object is never reopened after verification. Memory is bounded;
// transient disk space must accommodate the selected object. GC/metadata and
// pin release are the caller's responsibility, including on callback errors.
func (s *Store) VerifiedRead(ctx context.Context, object Object, consume func(io.ReadSeeker) error) error {
	if err := validObject(object); err != nil {
		return err
	}
	if consume == nil {
		return errors.New("missing object consumer")
	}
	source, err := s.openPrivate(object.Hash)
	if err != nil {
		return err
	}
	delivery, name, err := s.temporary(".delivery-")
	if err != nil {
		return errors.Join(err, source.Close())
	}
	defer s.root.Remove(name)
	actual, copyErr := copyObject(ctx, source, delivery, s.fail)
	err = errors.Join(copyErr, source.Close())
	if err == nil && actual != object {
		err = ErrIntegrity
	}
	if err == nil {
		_, err = delivery.Seek(0, io.SeekStart)
	}
	if err == nil {
		err = s.root.Remove(name)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = consume(struct{ io.ReadSeeker }{delivery})
	}
	return errors.Join(err, s.closeFile(delivery))
}

// Delete is exclusively for an already committed SQLite GC reservation. The
// metadata must remain deleting until this removal and directory sync succeed.
// Re-adoption requires fresh durable verification/publication, never a stale row.
func (s *Store) Delete(object Object) error {
	if err := validObject(object); err != nil {
		return err
	}
	f, err := s.openPrivate(object.Hash)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if err := f.Close(); err != nil {
			return err
		}
		if err := s.fail(BeforeDelete); err != nil {
			return err
		}
		if err := s.root.Remove(object.Hash); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := s.fail(DirectorySync); err != nil {
		return err
	}
	return syncRoot(s.root)
}

func (s *Store) verify(ctx context.Context, name string, expected Object) error {
	if err := validObject(expected); err != nil {
		return err
	}
	f, err := s.openPrivate(name)
	if err != nil {
		return err
	}
	actual, readErr := copyObject(ctx, f, io.Discard, nil)
	if actual != expected && readErr == nil {
		readErr = ErrIntegrity
	}
	return errors.Join(readErr, f.Close())
}

func copyObject(ctx context.Context, input io.Reader, output io.Writer, fault func(Boundary) error) (Object, error) {
	hash := sha256.New()
	buffer := make([]byte, bufferSize)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return Object{Size: size}, err
		}
		n, readErr := input.Read(buffer)
		if n > 0 {
			if fault != nil {
				if err := fault(Write); err != nil {
					return Object{Size: size}, err
				}
			}
			written, err := output.Write(buffer[:n])
			hash.Write(buffer[:written])
			size += int64(written)
			if err != nil {
				return Object{Size: size}, err
			}
			if written != n {
				return Object{Size: size}, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return Object{Hash: hex.EncodeToString(hash.Sum(nil)), Size: size}, nil
		}
		if readErr != nil {
			return Object{Size: size}, readErr
		}
	}
}

func (s *Store) temporary(prefix string) (*os.File, string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, "", err
	}
	name := prefix + hex.EncodeToString(token[:])
	f, err := s.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	return f, name, err
}

func (s *Store) closeFile(f *os.File) error {
	return errors.Join(s.fail(FileClose), f.Close())
}

func (s *Store) openPrivate(name string) (*os.File, error) {
	before, err := s.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("object path must be a regular file, not a symlink")
	}
	f, err := s.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != uint32(os.Getuid()) || stat.Nlink != 1 {
			err = errors.New("object must be a private regular file with one link")
		}
		if err == nil {
			after, statErr := s.root.Lstat(name)
			if statErr != nil {
				err = statErr
			} else if !after.Mode().IsRegular() || !os.SameFile(before, info) || !os.SameFile(info, after) {
				err = errors.New("object path changed while opening")
			}
		}
	}
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}

func validObject(object Object) error {
	decoded, err := hex.DecodeString(object.Hash)
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != object.Hash || object.Size < 0 {
		return errors.New("invalid object identity")
	}
	return nil
}

func privateDirectory(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || stat.Uid != uint32(os.Getuid()) {
		return errors.New("storage must be a private directory owned by this user")
	}
	return nil
}

func syncRoot(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
