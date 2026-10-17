// Package clientstate owns protected caller journals and immutable input copies.
// Only files created in this caller profile are renamed or removed.
package clientstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

type Store struct{ Root string }

func New(root string) (*Store, error) {
	root, e := filepath.Abs(root)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	info, e := os.Lstat(root)
	if e != nil {
		return nil, e
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("caller profile must be a protected directory")
	}
	for _, name := range []string{"journals", "locks", "snapshots", "pending"} {
		path := filepath.Join(root, name)
		if e = os.Mkdir(path, 0700); e != nil && !errors.Is(e, os.ErrExist) {
			return nil, e
		}
		info, e = os.Lstat(path)
		if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return nil, fmt.Errorf("unsafe caller state directory")
		}
	}
	return &Store{root}, nil
}
func validID(id string) bool { return len(id) == 36 && !strings.ContainsAny(id, "/\\.") }
func (s *Store) Lock(ctx context.Context, id string) (func() error, error) {
	if !validID(id) {
		return nil, fmt.Errorf("invalid operation ID")
	}
	path := filepath.Join(s.Root, "locks", id)
	if info, e := os.Lstat(path); e == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("unsafe lock file")
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	for {
		e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if e == nil {
			return f.Close, nil
		}
		if e != syscall.EWOULDBLOCK && e != syscall.EAGAIN {
			f.Close()
			return nil, e
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	return errors.Join(f.Sync(), f.Close())
}
func (s *Store) write(name string, value any) error {
	parent := filepath.Dir(filepath.Join(s.Root, name))
	f, e := os.CreateTemp(parent, ".pending-")
	if e != nil {
		return e
	}
	path := f.Name()
	defer os.Remove(path)
	if e = json.NewEncoder(f).Encode(value); e != nil {
		f.Close()
		return e
	}
	if e = errors.Join(f.Sync(), f.Close()); e != nil {
		return e
	}
	target := filepath.Join(s.Root, name)
	if info, e := os.Lstat(target); e == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("unsafe state file")
	}
	if e = os.Rename(path, target); e != nil {
		return e
	}
	return syncDir(parent)
}
func (s *Store) read(name string, value any) error {
	path := filepath.Join(s.Root, name)
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(e, os.ErrNotExist) {
		return domain.ErrNotFound
	}
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("unsafe state file")
	}
	return json.NewDecoder(f).Decode(value)
}
func (s *Store) Load(id string) (transfer.Journal, error) {
	var j transfer.Journal
	if !validID(id) {
		return j, fmt.Errorf("invalid operation ID")
	}
	e := s.read(filepath.Join("journals", id+".json"), &j)
	if e == nil && j.OperationID != id {
		return j, fmt.Errorf("journal identity mismatch")
	}
	return j, e
}
func (s *Store) Save(j transfer.Journal) error {
	if !validID(j.OperationID) {
		return fmt.Errorf("invalid operation ID")
	}
	return s.write(filepath.Join("journals", j.OperationID+".json"), j)
}
func (s *Store) Association() (transfer.Selection, error) {
	var selected transfer.Selection
	e := s.read("association.json", &selected)
	return selected, e
}
func (s *Store) Remember(selected transfer.Selection) error {
	return s.write("association.json", selected)
}
func (s *Store) Capture(ctx context.Context, input string) (transfer.Snapshot, error) {
	var out transfer.Snapshot
	id, e := service.NewID()
	if e != nil {
		return out, e
	}
	root := filepath.Join(s.Root, "snapshots", id)
	if e = os.Mkdir(root, 0700); e != nil {
		return out, e
	}
	if e = os.Mkdir(filepath.Join(root, "objects"), 0700); e != nil {
		return out, e
	}
	manifest, e := os.OpenFile(filepath.Join(root, "entries.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return out, e
	}
	defer manifest.Close()
	digest := sha256.New()
	writer := io.MultiWriter(manifest, digest)
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	full, e := filepath.Abs(input)
	if e != nil {
		return out, e
	}
	info, e := os.Lstat(full)
	if e != nil {
		return out, e
	}
	out = transfer.Snapshot{ID: id, Mode: "file", SelectionName: filepath.Base(full)}
	if !utf8.ValidString(out.SelectionName) {
		return out, importer.ErrInputChanged
	}
	if info.IsDir() {
		out.Mode = "directory"
	} else if strings.EqualFold(filepath.Ext(full), ".zip") {
		out.Mode = "archive"
	}
	// Anchor traversal to the selected directory handle. A pathname swap cannot
	// redirect child opens outside the selected tree.
	anchorPath, selectedPath := full, "."
	if !info.IsDir() {
		anchorPath, selectedPath = filepath.Dir(full), filepath.Base(full)
	}
	if e := ctx.Err(); e != nil {
		return out, e
	}
	anchor, e := os.OpenRoot(anchorPath)
	if e != nil {
		return out, e
	}
	defer anchor.Close()
	opened, e := anchor.Lstat(selectedPath)
	if e != nil || !os.SameFile(info, opened) || info.Mode().Type() != opened.Mode().Type() {
		return out, importer.ErrInputChanged
	}
	var walk func(string, string) error
	walk = func(path, relative string) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		before, e := anchor.Lstat(path)
		if e != nil {
			return e
		}
		entry := transfer.Entry{Path: relative}
		if relative != "" {
			if e = importer.ValidatePath(relative); e != nil {
				return e
			}
		}
		switch {
		case before.Mode()&os.ModeSymlink != 0:
			entry.Kind = "symlink"
			entry.Link, e = anchor.Readlink(path)
			if e != nil {
				return e
			}
			if !utf8.ValidString(entry.Link) {
				return importer.ErrInputChanged
			}
			if relative == "" {
				return fmt.Errorf("select a symlink through its containing directory")
			}
			return encoder.Encode(entry)
		case before.IsDir():
			if relative != "" {
				entry.Kind = "directory"
				if e = encoder.Encode(entry); e != nil {
					return e
				}
			}
			dir, e := anchor.Open(path)
			if e != nil {
				return e
			}
			defer dir.Close()
			actual, e := dir.Stat()
			if e != nil || !os.SameFile(before, actual) {
				return importer.ErrInputChanged
			}
			for {
				children, e := dir.ReadDir(64)
				for _, child := range children {
					rel := child.Name()
					if relative != "" {
						rel = relative + "/" + rel
					}
					if e = walk(filepath.Join(path, child.Name()), rel); e != nil {
						return e
					}
				}
				if e == io.EOF {
					break
				}
				if e != nil {
					return e
				}
			}
		case before.Mode().IsRegular():
			entry.Kind = "file"
			if out.Mode != "directory" {
				entry.Path = "_input"
			}
			source, e := anchor.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
			if e != nil {
				return e
			}
			actual, e := source.Stat()
			if e != nil || !os.SameFile(before, actual) {
				source.Close()
				return importer.ErrInputChanged
			}
			stage, e := os.CreateTemp(filepath.Join(root, "objects"), ".stage-")
			if e != nil {
				source.Close()
				return e
			}
			hash := sha256.New()
			n, e := io.CopyBuffer(io.MultiWriter(stage, hash), &contextReader{ctx, source}, make([]byte, 64*1024))
			after, statError := source.Stat()
			closeError := source.Close()
			if e = errors.Join(e, statError, closeError); e != nil {
				stage.Close()
				return e
			}
			if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || n != before.Size() {
				stage.Close()
				return importer.ErrInputChanged
			}
			if e = errors.Join(stage.Sync(), stage.Close()); e != nil {
				return e
			}
			entry.Hash = hex.EncodeToString(hash.Sum(nil))
			entry.Size = n
			destination := filepath.Join(root, "objects", entry.Hash)
			if e = os.Rename(stage.Name(), destination); e != nil {
				return e
			}
			if e = encoder.Encode(entry); e != nil {
				return e
			}
		default:
			return fmt.Errorf("unsupported filesystem entry: %s", relative)
		}
		after, e := anchor.Lstat(path)
		if e != nil {
			return e
		}
		if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
			return importer.ErrInputChanged
		}
		return nil
	}
	if e = walk(selectedPath, ""); e != nil {
		return out, e
	}
	if e = errors.Join(manifest.Sync(), manifest.Close()); e != nil {
		return out, e
	}
	if e = syncDir(filepath.Join(root, "objects")); e != nil {
		return out, e
	}
	if e = syncDir(root); e != nil {
		return out, e
	}
	if e = syncDir(filepath.Dir(root)); e != nil {
		return out, e
	}
	out.Identity = hex.EncodeToString(digest.Sum(nil))
	return out, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}
func (s *Store) Entries(snapshot transfer.Snapshot, consume func(transfer.Entry) error) error {
	if !validID(snapshot.ID) {
		return fmt.Errorf("invalid snapshot identity")
	}
	f, e := os.OpenFile(filepath.Join(s.Root, "snapshots", snapshot.ID, "entries.jsonl"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return e
	}
	defer f.Close()
	hash := sha256.New()
	decoder := json.NewDecoder(io.TeeReader(f, hash))
	for {
		var entry transfer.Entry
		e = decoder.Decode(&entry)
		if e == io.EOF {
			if hex.EncodeToString(hash.Sum(nil)) != snapshot.Identity {
				return importer.ErrInputChanged
			}
			return nil
		}
		if e != nil {
			return e
		}
		if e = consume(entry); e != nil {
			return e
		}
	}
}
func (s *Store) Open(snapshot transfer.Snapshot, entry transfer.Entry) (io.ReadSeekCloser, error) {
	if !validID(snapshot.ID) || len(entry.Hash) != 64 || strings.ContainsAny(entry.Hash, "/\\.") {
		return nil, fmt.Errorf("invalid snapshot object")
	}
	f, e := os.OpenFile(filepath.Join(s.Root, "snapshots", snapshot.ID, "objects", entry.Hash), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, e
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() != entry.Size {
		f.Close()
		return nil, importer.ErrInputChanged
	}
	return f, nil
}

func (s *Store) pendingKey(identity transfer.Identity, input transfer.Input) (string, error) {
	path := ""
	var e error
	if input.Path != "" {
		path, e = filepath.Abs(input.Path)
		if e != nil {
			return "", e
		}
	}
	metadata := ""
	if input.Metadata != "" {
		metadata, e = filepath.Abs(input.Metadata)
		if e != nil {
			return "", e
		}
	}
	input.OperationID = ""
	input.Path = path
	input.Metadata = metadata
	b, e := json.Marshal([]any{identity.LibraryID, identity.PrincipalID, input})
	if e != nil {
		return "", e
	}
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:]), nil
}
func pendingLockID(key string) string {
	return key[:8] + "-" + key[8:12] + "-" + key[12:16] + "-" + key[16:20] + "-" + key[20:32]
}
func (s *Store) UsePending(ctx context.Context, identity transfer.Identity, input transfer.Input, candidate string) (string, error) {
	key, e := s.pendingKey(identity, input)
	if e != nil {
		return "", e
	}
	release, e := s.Lock(ctx, pendingLockID(key))
	if e != nil {
		return "", e
	}
	defer release()
	var value struct {
		ID string `json:"operation_id"`
	}
	e = s.read(filepath.Join("pending", key+".json"), &value)
	if e == nil {
		if !validID(value.ID) {
			return "", fmt.Errorf("invalid pending identity")
		}
		return value.ID, nil
	}
	if !errors.Is(e, domain.ErrNotFound) {
		return "", e
	}
	value.ID = candidate
	if e = s.write(filepath.Join("pending", key+".json"), value); e != nil {
		return "", e
	}
	return candidate, nil
}
func (s *Store) FinishPending(ctx context.Context, identity transfer.Identity, input transfer.Input, id string) error {
	if input.OperationID != "" {
		return nil
	}
	key, e := s.pendingKey(identity, input)
	if e != nil {
		return e
	}
	release, e := s.Lock(ctx, pendingLockID(key))
	if e != nil {
		return e
	}
	defer release()
	var value struct {
		ID string `json:"operation_id"`
	}
	if e = s.read(filepath.Join("pending", key+".json"), &value); errors.Is(e, domain.ErrNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	if value.ID != id {
		return transfer.ErrIntent
	}
	if e = os.Remove(filepath.Join(s.Root, "pending", key+".json")); e != nil {
		return e
	}
	return syncDir(filepath.Join(s.Root, "pending"))
}

type Pairing struct {
	Endpoint string                `json:"endpoint"`
	Profile  string                `json:"profile"`
	Value    domain.PairingCreated `json:"pairing"`
}

func (s *Store) Pairing() (Pairing, error) {
	var p Pairing
	e := s.read("pairing.json", &p)
	return p, e
}
func (s *Store) SavePairing(p Pairing) error { return s.write("pairing.json", p) }
func (s *Store) ClearPairing() error {
	e := os.Remove(filepath.Join(s.Root, "pairing.json"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	return syncDir(s.Root)
}
