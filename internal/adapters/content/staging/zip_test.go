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
	"testing"

	importer "haeryong.local/haeryong/internal/application/import"
)

func zipFixture(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, value := range entries {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if name == "alias" {
			header.SetMode(os.ModeSymlink | 0777)
		}
		f, err := w.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestZIPPreservesTypedEntriesAndExactNames(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != len(entries) {
		t.Fatalf("lost entries: %d", len(seen))
	}
	for path, value := range entries {
		name := strings.TrimSuffix(path, "/")
		switch name {
		case "empty":
			if seen[name].Kind != "directory" {
				t.Fatal("empty directory lost")
			}
		case "alias":
			if seen[name].Kind != "symlink" || seen[name].LinkTarget != value {
				t.Fatal("link was followed or changed")
			}
		default:
			if seen[name].Kind != "file" || contents[name] != value {
				t.Fatalf("bytes/name changed: %q", name)
			}
		}
	}
}

func TestZIPEncodingUsesFlagsAndUnicodeExtra(t *testing.T) {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	name := string([]byte{0x82}) + ".txt"
	h := &zip.FileHeader{Name: name, NonUTF8: true, Method: zip.Store}
	f, err := w.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("cp437"))
	extra := make([]byte, 4+5+len("한글.txt"))
	binary.LittleEndian.PutUint16(extra, 0x7075)
	binary.LittleEndian.PutUint16(extra[2:], uint16(len(extra)-4))
	extra[4] = 1
	binary.LittleEndian.PutUint32(extra[5:], crc32.ChecksumIEEE([]byte("ascii.txt")))
	copy(extra[9:], "한글.txt")
	f, err = w.CreateHeader(&zip.FileHeader{Name: "ascii.txt", Extra: extra, Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("unicode"))
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	err = New().Expand(context.Background(), bytes.NewReader(b.Bytes()), int64(b.Len()), func(e importer.EntryInput, r io.Reader) error {
		seen[e.Path] = true
		_, err := io.Copy(io.Discard, r)
		return err
	})
	if err != nil || !seen["é.txt"] || !seen["한글.txt"] {
		t.Fatalf("encoding not preserved: %v %v", seen, err)
	}
}

func TestZIPRejectsPathsCorruptionAndIncompleteConsumption(t *testing.T) {
	for _, path := range []string{"/absolute", "../outside", "a/../b", "a//b", "C:/drive"} {
		t.Run(path, func(t *testing.T) {
			raw := zipFixture(t, map[string]string{path: "bytes"})
			if err := New().Expand(context.Background(), bytes.NewReader(raw), int64(len(raw)), func(importer.EntryInput, io.Reader) error { return nil }); err == nil {
				t.Fatal("invalid path accepted")
			}
		})
	}
	raw := zipFixture(t, map[string]string{"file": "bytes"})
	if err := New().Expand(context.Background(), bytes.NewReader(raw), int64(len(raw)), func(importer.EntryInput, io.Reader) error { return nil }); err == nil {
		t.Fatal("unconsumed file accepted")
	}
	for _, suffix := range []int{1, 12, 22} {
		if err := New().Expand(context.Background(), bytes.NewReader(raw[:len(raw)-suffix]), int64(len(raw)-suffix), func(importer.EntryInput, io.Reader) error { return nil }); err == nil {
			t.Fatal("truncated archive accepted")
		}
	}
	// Go's no-descriptor checksum reader skips a zero declared CRC; production
	// must still compare it to the actual expanded bytes, including zero.
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, err := w.CreateRaw(&zip.FileHeader{Name: "crc", Method: zip.Store, CRC32: 0, CompressedSize64: 5, UncompressedSize64: 5})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("bytes"))
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	err = New().Expand(context.Background(), bytes.NewReader(b.Bytes()), int64(b.Len()), func(_ importer.EntryInput, r io.Reader) error { _, err := io.Copy(io.Discard, r); return err })
	if !errors.Is(err, zip.ErrChecksum) {
		t.Fatalf("zero CRC bypass: %v", err)
	}
}

func TestZIPCountAboveClassicLimitAndPrefixedArchive(t *testing.T) {
	dir := t.TempDir()
	p, err := os.Create(filepath.Join(dir, "many.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	w := zip.NewWriter(p)
	const count = 65536
	for i := 0; i < count; i++ {
		if _, err := w.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("entry-%05d", i), Method: zip.Store}); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := p.Stat()
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	err = New().Expand(context.Background(), p, info.Size(), func(_ importer.EntryInput, r io.Reader) error { seen++; _, err := io.Copy(io.Discard, r); return err })
	if err != nil || seen != count {
		t.Fatalf("ZIP64 count lost: %d %v", seen, err)
	}
	plain := zipFixture(t, map[string]string{"file": "contents"})
	prefixed := append([]byte("executable prefix"), plain...)
	err = New().Expand(context.Background(), bytes.NewReader(prefixed), int64(len(prefixed)), func(_ importer.EntryInput, r io.Reader) error {
		b, err := io.ReadAll(r)
		if string(b) != "contents" {
			t.Fatal("prefix changed data")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestZIPHonorsCancellationAndInvalidUTF8Flag(t *testing.T) {
	raw := zipFixture(t, map[string]string{"file": "payload"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := New().Expand(ctx, bytes.NewReader(raw), int64(len(raw)), func(importer.EntryInput, io.Reader) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	file := &zip.File{FileHeader: zip.FileHeader{Name: string([]byte{0xff}), Flags: 0x800}}
	if _, err := entryName(file); err == nil {
		t.Fatal("invalid UTF8 flag silently decoded")
	}
}

func TestZIPClassicCount65535WithoutZIP64(t *testing.T) {
	var buffer bytes.Buffer
	w := zip.NewWriter(&buffer)
	for i := 0; i < 65535; i++ {
		if _, err := w.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("entry-%05d", i), Method: zip.Store}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
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
	if err != nil || seen != 65535 {
		t.Fatalf("classic 65535-entry archive rejected: count=%d error=%v", seen, err)
	}
}

func TestZIPUnixLiteralBackslashWithStdlibPathGuardEnabled(t *testing.T) {
	t.Setenv("GODEBUG", "zipinsecurepath=0")
	raw := zipFixture(t, map[string]string{`literal\filename`: "bytes"})
	err := New().Expand(context.Background(), bytes.NewReader(raw), int64(len(raw)), func(entry importer.EntryInput, r io.Reader) error {
		if entry.Path != `literal\filename` {
			t.Fatal("Unix filename character changed")
		}
		_, err := io.Copy(io.Discard, r)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
