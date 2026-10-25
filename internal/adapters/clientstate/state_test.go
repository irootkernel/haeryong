package clientstate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/transfer"
	"os"
	"path/filepath"
	"strings"
)

var _ = It("TestSnapshotManifestRejectsCompleteRecordPrefix", Label("unit"), func() {
	t := GinkgoT()

	root := t.TempDir()
	input := filepath.Join(root, "selected")
	{
		e := os.Mkdir(input, 0700)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	for _, name := range []string{"first.html", "second.html"} {
		{
			e := os.WriteFile(filepath.Join(input, name), []byte(name), 0600)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
		}

	}
	store, e := New(filepath.Join(root, "caller"))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	snapshot, e := store.Capture(context.Background(), input)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	manifest := filepath.Join(store.Root, "snapshots", snapshot.ID, "entries.jsonl")
	original, e := os.ReadFile(manifest)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	boundary := bytes.IndexByte(original, '\n') + 1
	Expect(boundary <= 0 || boundary == len(original)).To(BeFalse(), func() string { return fmt.Sprint("fixture lacks two complete records") })
	{

		e = os.WriteFile(manifest, original[:boundary], 0600)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}
	{

		e = store.Entries(snapshot, func(transfer.Entry) error { return nil })
		Expect(!errors.Is(e, importer.ErrInputChanged)).To(BeFalse(), func() string { return fmt.Sprint("valid JSON prefix was treated as complete selected input", e) })
	}
	{

		e = os.WriteFile(manifest, original, 0600)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	count := 0
	{
		e = store.Entries(snapshot, func(transfer.Entry) error { count++; return nil })
		Expect(e != nil || count != 2).To(BeFalse(), func() string { return fmt.Sprint("original snapshot did not recover", count, e) })
	}

})

// Err is called at the existing traversal/read boundaries. The callback changes
// actual directory names only; it neither changes the capture implementation nor
// supplies a replacement filesystem.
type traversalContext struct {
	context.Context
	boundary func()
}

func (c traversalContext) Err() error { c.boundary(); return nil }

var _ = It("TestSelectedDirectorySwapNeverCapturesOutsideBytes", Label("unit"), func() {
	t := GinkgoT()

	root := t.TempDir()
	selected, parked, outside := filepath.Join(root, "selected"), filepath.Join(root, "parked"), filepath.Join(root, "outside")
	for _, path := range []string{selected, outside} {
		{
			e := os.Mkdir(path, 0700)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
		}

	}
	for _, name := range []string{"first.html", "second.html"} {
		{
			e := os.WriteFile(filepath.Join(selected, name), []byte("selected:"+name), 0600)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
		}
		{

			e := os.WriteFile(filepath.Join(outside, name), []byte("OUTSIDE:"+name), 0600)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
		}

	}
	store, e := New(filepath.Join(root, "caller"))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	calls, swapped, restored := 0, false, false
	restore := func() {
		if swapped && !restored {
			{
				e := os.Remove(selected)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}
			{

				e := os.Rename(parked, selected)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

			restored = true
		}
	}
	defer restore()
	ctx := traversalContext{Context: context.Background(), boundary: func() {
		calls++
		if calls == 3 {
			{
				e := os.Rename(selected, parked)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}
			{

				e := os.Symlink(outside, selected)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

			swapped = true
			return
		}
		if swapped && !restored {
			entries, e := os.ReadDir(filepath.Join(store.Root, "snapshots"))
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			for _, snapshot := range entries {
				objects, e := os.ReadDir(filepath.Join(store.Root, "snapshots", snapshot.Name(), "objects"))
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

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
	Expect(!swapped).To(BeFalse(), func() string { return fmt.Sprint("directory-open/child-walk barrier was not reached") })

	if e != nil {
		return
	}
	Expect( // Explicit changed-input rejection is a valid safe outcome.
		!restored).To(BeFalse(), func() string { return fmt.Sprint("counterexample did not restore original directory identity") })

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
		Expect(strings.HasPrefix(string(b[:n]), "OUTSIDE:")).To(BeFalse(), func() string { return fmt.Sprint("outside bytes were captured through replaced directory pathname") })

		return nil
	})
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

})
var _ = It("TestSelectedDirectoryOpeningSwapRejectsDifferentInitialIdentity", Label("unit"), func() {
	t := GinkgoT()

	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	outside := filepath.Join(root, "outside")
	parked := filepath.Join(root, "parked")
	for _, p := range []string{selected, outside} {
		{
			e := os.Mkdir(p, 0700)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
		}

	}
	{
		e := os.WriteFile(filepath.Join(selected, "page.html"), []byte("selected"), 0600)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}
	{

		e := os.WriteFile(filepath.Join(outside, "page.html"), []byte("OUTSIDE"), 0600)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	store, e := New(filepath.Join(root, "caller"))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	swapped := false
	ctx := traversalContext{Context: context.Background(), boundary: func() {
		if !swapped {
			{
				e := os.Rename(selected, parked)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}
			{

				e := os.Symlink(outside, selected)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
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
	Expect(!swapped).To(BeFalse(), func() string { return fmt.Sprint("initial-stat/open boundary was not reached") })

	if e != nil {
		return
	}
	{
		e = store.Entries(snapshot, func(entry transfer.Entry) error {
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
				Fail(fmt.Sprint("opened outside directory replaced initially selected identity"))
			}
			return nil
		})
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

})

var _ = It("Copies pending command actions survive reload and finishing one never clears another", Label("integration"), func() {
	root := filepath.Join(GinkgoT().TempDir(), "caller")
	store, e := New(root)
	Expect(e).NotTo(HaveOccurred())
	identity := transfer.Identity{LibraryID: "library", PrincipalID: "owner"}
	backup := CommandIntent{Method: "POST", Target: "/local/v1/copy-requests", CopyAction: "backup.create"}
	export := backup
	export.CopyAction = "export.plan"
	a := "11111111-1111-4111-8111-111111111111"
	b := "22222222-2222-4222-8222-222222222222"
	c := "33333333-3333-4333-8333-333333333333"
	id, e := store.UseCommandPending(context.Background(), identity, backup, a)
	Expect(e).NotTo(HaveOccurred())
	Expect(id).To(Equal(a))
	id, e = store.UseCommandPending(context.Background(), identity, export, b)
	Expect(e).NotTo(HaveOccurred())
	Expect(id).To(Equal(b))
	reopened, e := New(root)
	Expect(e).NotTo(HaveOccurred())
	Expect(reopened.FinishCommandPending(context.Background(), identity, backup, a)).To(Succeed())
	id, e = reopened.UseCommandPending(context.Background(), identity, export, c)
	Expect(e).NotTo(HaveOccurred())
	Expect(id).To(Equal(b))
	id, e = reopened.UseCommandPending(context.Background(), identity, backup, c)
	Expect(e).NotTo(HaveOccurred())
	Expect(id).To(Equal(c))
	wrong := backup
	wrong.Target = "/local/v1/arbitrary"
	_, e = reopened.UseCommandPending(context.Background(), identity, wrong, a)
	Expect(e).To(HaveOccurred())
})
