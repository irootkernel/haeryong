package staging

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	importer "haeryong.local/haeryong/internal/application/import"
)

func zipFixture(t GinkgoTInterface, entries map[string]string) []byte {
	GinkgoHelper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, value := range entries {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if name == "alias" {
			header.SetMode(os.ModeSymlink | 0777)
		}
		f, err := w.CreateHeader(header)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		{

			_, err = f.Write([]byte(value))
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

	}
	{
		err := w.Close()
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	return b.Bytes()
}

var _ = It("TestZIPPreservesTypedEntriesAndExactNames", Label("unit"), func() {
	t := GinkgoT()

	entries := map[string]string{"nested/α.html": "<script>ordinary()</script>", "empty/": "", "alias": "../outside", "manifest.json": "ordinary payload", ".hidden": "hidden", `literal\name.unknown`: "attachment", "Case": "A", "case": "a", "é.html": "NFC", "é.html": "NFD"}
	raw := zipFixture(t, entries)
	seen := map[string]importer.EntryInput{}
	contents := map[string]string{}
	err := New().Expand(context.Background(), bytes.NewReader(raw), int64(len(raw)), func(e importer.EntryInput, r io.Reader) error {
		seen[e.Path] = e
		if r != nil {
			b, err := io.ReadAll(r)
			contents[e.Path] = string(b)
			return err
		}
		return nil
	})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	Expect(len(seen) != len(entries)).To(BeFalse(), func() string { return fmt.Sprintf("lost entries: %d", len(seen)) })

	for path, value := range entries {
		name := strings.TrimSuffix(path, "/")
		switch name {
		case "empty":
			if seen[name].Kind != "directory" {
				Fail(fmt.Sprint("empty directory lost"))
			}
		case "alias":
			if seen[name].Kind != "symlink" || seen[name].LinkTarget != value {
				Fail(fmt.Sprint("link was followed or changed"))
			}
		default:
			if seen[name].Kind != "file" || contents[name] != value {
				Fail(fmt.Sprintf("bytes/name changed: %q", name))
			}
		}
	}
})
var _ = It("TestZIPEncodingUsesFlagsAndUnicodeExtra", Label("unit"), func() {

	var b bytes.Buffer
	w := zip.NewWriter(&b)
	name := string([]byte{0x82}) + ".txt"
	h := &zip.FileHeader{Name: name, NonUTF8: true, Method: zip.Store}
	f, err := w.CreateHeader(h)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	_, _ = f.Write([]byte("cp437"))
	extra := make([]byte, 4+5+len("한글.txt"))
	binary.LittleEndian.PutUint16(extra, 0x7075)
	binary.LittleEndian.PutUint16(extra[2:], uint16(len(extra)-4))
	extra[4] = 1
	binary.LittleEndian.PutUint32(extra[5:], crc32.ChecksumIEEE([]byte("ascii.txt")))
	copy(extra[9:], "한글.txt")
	f, err = w.CreateHeader(&zip.FileHeader{Name: "ascii.txt", Extra: extra, Method: zip.Store})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	_, _ = f.Write([]byte("unicode"))
	{
		err = w.Close()
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	seen := map[string]bool{}
	err = New().Expand(context.Background(), bytes.NewReader(b.Bytes()), int64(b.Len()), func(e importer.EntryInput, r io.Reader) error {
		seen[e.Path] = true
		_, err := io.Copy(io.Discard, r)
		return err
	})
	Expect(err != nil || !seen["é.txt"] || !seen["한글.txt"]).To(BeFalse(), func() string { return fmt.Sprintf("encoding not preserved: %v %v", seen, err) })

})
var _ = It("TestZIPRejectsPathsCorruptionAndIncompleteConsumption", Label("unit"), func() {
	t := GinkgoT()

	for _, path := range []string{"/absolute", "../outside", "a/../b", "a//b", "C:/drive"} {
		func() {
			By(path)
			raw := zipFixture(t, map[string]string{path: "bytes"})
			{
				err := New().Expand(context.Background(), bytes.NewReader(raw), int64(len(raw)), func(importer.EntryInput, io.Reader) error { return nil })
				Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("invalid path accepted") })
			}

		}()
	}
	raw := zipFixture(t, map[string]string{"file": "bytes"})
	{
		err := New().Expand(context.Background(), bytes.NewReader(raw), int64(len(raw)), func(importer.EntryInput, io.Reader) error { return nil })
		Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("unconsumed file accepted") })
	}

	for _, suffix := range []int{1, 12, 22} {
		{
			err := New().Expand(context.Background(), bytes.NewReader(raw[:len(raw)-suffix]), int64(len(raw)-suffix), func(importer.EntryInput, io.Reader) error { return nil })
			Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("truncated archive accepted") })
		}

	}
	// Go's no-descriptor checksum reader skips a zero declared CRC; production
	// must still compare it to the actual expanded bytes, including zero.
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, err := w.CreateRaw(&zip.FileHeader{Name: "crc", Method: zip.Store, CRC32: 0, CompressedSize64: 5, UncompressedSize64: 5})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	_, _ = f.Write([]byte("bytes"))
	{
		err = w.Close()
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	err = New().Expand(context.Background(), bytes.NewReader(b.Bytes()), int64(b.Len()), func(_ importer.EntryInput, r io.Reader) error { _, err := io.Copy(io.Discard, r); return err })
	Expect(!errors.Is(err, zip.ErrChecksum)).To(BeFalse(), func() string { return fmt.Sprintf("zero CRC bypass: %v", err) })

})
var _ = It("TestZIPCountAboveClassicLimitAndPrefixedArchive", Label("unit"), func() {
	t := GinkgoT()

	dir := t.TempDir()
	p, err := os.Create(filepath.Join(dir, "many.zip"))
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	defer p.Close()
	w := zip.NewWriter(p)
	const count = 65536
	for i := 0; i < count; i++ {
		{
			_, err := w.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("entry-%05d", i), Method: zip.Store})
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

	}
	{
		err = w.Close()
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	info, err := p.Stat()
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	seen := 0
	err = New().Expand(context.Background(), p, info.Size(), func(_ importer.EntryInput, r io.Reader) error { seen++; _, err := io.Copy(io.Discard, r); return err })
	Expect(err != nil || seen != count).To(BeFalse(), func() string { return fmt.Sprintf("ZIP64 count lost: %d %v", seen, err) })

	plain := zipFixture(t, map[string]string{"file": "contents"})
	prefixed := append([]byte("executable prefix"), plain...)
	err = New().Expand(context.Background(), bytes.NewReader(prefixed), int64(len(prefixed)), func(_ importer.EntryInput, r io.Reader) error {
		b, err := io.ReadAll(r)
		Expect(string(b) != "contents").To(BeFalse(), func() string { return fmt.Sprint("prefix changed data") })

		return err
	})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

})
var _ = It("TestZIPHonorsCancellationAndInvalidUTF8Flag", Label("unit"), func() {
	t := GinkgoT()

	raw := zipFixture(t, map[string]string{"file": "payload"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	{
		err := New().Expand(ctx, bytes.NewReader(raw), int64(len(raw)), func(importer.EntryInput, io.Reader) error { return nil })
		Expect(!errors.Is(err, context.Canceled)).To(BeFalse(), func() string { return fmt.Sprintf("cancel: %v", err) })
	}

	file := &zip.File{FileHeader: zip.FileHeader{Name: string([]byte{0xff}), Flags: 0x800}}
	{
		_, err := entryName(file)
		Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("invalid UTF8 flag silently decoded") })
	}

})
var _ = It("TestZIPClassicCount65535WithoutZIP64", Label("unit"), func() {

	var buffer bytes.Buffer
	w := zip.NewWriter(&buffer)
	for i := 0; i < 65535; i++ {
		{
			_, err := w.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("entry-%05d", i), Method: zip.Store})
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

	}
	{
		err := w.Close()
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	raw := buffer.Bytes()
	end := len(raw) - 22
	position := int(le.Uint64(raw[end-12:]))
	classic := append([]byte{}, raw[:position]...)
	classic = append(classic, raw[end:]...)
	e := classic[len(classic)-22:]
	le.PutUint32(e[12:], uint32(le.Uint64(raw[position+40:])))
	le.PutUint32(e[16:], uint32(le.Uint64(raw[position+48:])))
	seen := 0
	err := New().Expand(context.Background(), bytes.NewReader(classic), int64(len(classic)), func(_ importer.EntryInput, r io.Reader) error { seen++; _, err := io.Copy(io.Discard, r); return err })
	Expect(err != nil || seen != 65535).To(BeFalse(), func() string {
		return fmt.Sprintf("classic 65535-entry archive rejected: count=%d error=%v", seen, err)
	})

})
var _ = It("TestZIPUnixLiteralBackslashWithStdlibPathGuardEnabled", Label("unit"), func() {
	t := GinkgoT()

	t.Setenv("GODEBUG", "zipinsecurepath=0")
	raw := zipFixture(t, map[string]string{`literal\filename`: "bytes"})
	err := New().Expand(context.Background(), bytes.NewReader(raw), int64(len(raw)), func(entry importer.EntryInput, r io.Reader) error {
		Expect(entry.Path != `literal\filename`).To(BeFalse(), func() string { return fmt.Sprint("Unix filename character changed") })

		_, err := io.Copy(io.Discard, r)
		return err
	})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

})
