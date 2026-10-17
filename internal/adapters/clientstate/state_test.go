package clientstate

import (
	"bytes"
	"context"
	"errors"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/transfer"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotManifestRejectsCompleteRecordPrefix(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "selected")
	if e := os.Mkdir(input, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"first.html", "second.html"} {
		if e := os.WriteFile(filepath.Join(input, name), []byte(name), 0600); e != nil {
			t.Fatal(e)
		}
	}
	store, e := New(filepath.Join(root, "caller"))
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := store.Capture(context.Background(), input)
	if e != nil {
		t.Fatal(e)
	}
	manifest := filepath.Join(store.Root, "snapshots", snapshot.ID, "entries.jsonl")
	original, e := os.ReadFile(manifest)
	if e != nil {
		t.Fatal(e)
	}
	boundary := bytes.IndexByte(original, '\n') + 1
	if boundary <= 0 || boundary == len(original) {
		t.Fatal("fixture lacks two complete records")
	}
	if e = os.WriteFile(manifest, original[:boundary], 0600); e != nil {
		t.Fatal(e)
	}
	if e = store.Entries(snapshot, func(transfer.Entry) error { return nil }); !errors.Is(e, importer.ErrInputChanged) {
		t.Fatal("valid JSON prefix was treated as complete selected input", e)
	}
	if e = os.WriteFile(manifest, original, 0600); e != nil {
		t.Fatal(e)
	}
	count := 0
	if e = store.Entries(snapshot, func(transfer.Entry) error { count++; return nil }); e != nil || count != 2 {
		t.Fatal("original snapshot did not recover", count, e)
	}
}

// Err is called at the existing traversal/read boundaries. The callback changes
// actual directory names only; it neither changes the capture implementation nor
// supplies a replacement filesystem.
type traversalContext struct {
	context.Context
	boundary func()
}

func (c traversalContext) Err() error { c.boundary(); return nil }

func TestSelectedDirectorySwapNeverCapturesOutsideBytes(t *testing.T) {
	root := t.TempDir()
	selected, parked, outside := filepath.Join(root, "selected"), filepath.Join(root, "parked"), filepath.Join(root, "outside")
	for _, path := range []string{selected, outside} {
		if e := os.Mkdir(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"first.html", "second.html"} {
		if e := os.WriteFile(filepath.Join(selected, name), []byte("selected:"+name), 0600); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(outside, name), []byte("OUTSIDE:"+name), 0600); e != nil {
			t.Fatal(e)
		}
	}
	store, e := New(filepath.Join(root, "caller"))
	if e != nil {
		t.Fatal(e)
	}
	calls, swapped, restored := 0, false, false
	restore := func() {
		if swapped && !restored {
			if e := os.Remove(selected); e != nil {
				t.Fatal(e)
			}
			if e := os.Rename(parked, selected); e != nil {
				t.Fatal(e)
			}
			restored = true
		}
	}
	defer restore()
	ctx := traversalContext{Context: context.Background(), boundary: func() {
		calls++
		if calls == 3 {
			if e := os.Rename(selected, parked); e != nil {
				t.Fatal(e)
			}
			if e := os.Symlink(outside, selected); e != nil {
				t.Fatal(e)
			}
			swapped = true
			return
		}
		if swapped && !restored {
			entries, e := os.ReadDir(filepath.Join(store.Root, "snapshots"))
			if e != nil {
				t.Fatal(e)
			}
			for _, snapshot := range entries {
				objects, e := os.ReadDir(filepath.Join(store.Root, "snapshots", snapshot.Name(), "objects"))
				if e != nil {
					t.Fatal(e)
				}
				for _, object := range objects {
					if !strings.HasPrefix(object.Name(), ".stage-") {
						restore()
						return
					}
				}
			}
		}
	}}
	snapshot, e := store.Capture(ctx, selected)
	if !swapped {
		t.Fatal("directory-open/child-walk barrier was not reached")
	}
	if e != nil {
		return
	} // Explicit changed-input rejection is a valid safe outcome.
	if !restored {
		t.Fatal("counterexample did not restore original directory identity")
	}
	e = store.Entries(snapshot, func(entry transfer.Entry) error {
		if entry.Kind != "file" {
			return nil
		}
		f, e := store.Open(snapshot, entry)
		if e != nil {
			return e
		}
		defer f.Close()
		b := make([]byte, 8)
		n, _ := f.Read(b)
		if strings.HasPrefix(string(b[:n]), "OUTSIDE:") {
			t.Error("outside bytes were captured through replaced directory pathname")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}

func TestSelectedDirectoryOpeningSwapRejectsDifferentInitialIdentity(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	outside := filepath.Join(root, "outside")
	parked := filepath.Join(root, "parked")
	for _, p := range []string{selected, outside} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(selected, "page.html"), []byte("selected"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(outside, "page.html"), []byte("OUTSIDE"), 0600); e != nil {
		t.Fatal(e)
	}
	store, e := New(filepath.Join(root, "caller"))
	if e != nil {
		t.Fatal(e)
	}
	swapped := false
	ctx := traversalContext{Context: context.Background(), boundary: func() {
		if !swapped {
			if e := os.Rename(selected, parked); e != nil {
				t.Fatal(e)
			}
			if e := os.Symlink(outside, selected); e != nil {
				t.Fatal(e)
			}
			swapped = true
		}
	}}
	defer func() {
		if swapped {
			os.Remove(selected)
			os.Rename(parked, selected)
		}
	}()
	snapshot, e := store.Capture(ctx, selected)
	if !swapped {
		t.Fatal("initial-stat/open boundary was not reached")
	}
	if e != nil {
		return
	}
	if e = store.Entries(snapshot, func(entry transfer.Entry) error {
		if entry.Kind != "file" {
			return nil
		}
		f, e := store.Open(snapshot, entry)
		if e != nil {
			return e
		}
		defer f.Close()
		b := make([]byte, 7)
		n, _ := f.Read(b)
		if string(b[:n]) == "OUTSIDE" {
			t.Error("opened outside directory replaced initially selected identity")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
