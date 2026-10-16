package imports

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"haeryong.local/haeryong/internal/domain"
)

// F12's sizes are reproducible fixture workloads, never admission thresholds.
// Only Task04's import/resume/restart contribution is claimed by this test.
func TestActualHTTPF12ImportResumeAndRestart(t *testing.T) {
	f := newHTTPFixture(t)
	var resources syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(f.selection.DataDir), &resources); err != nil {
		t.Fatal(err)
	}
	archive, err := os.Create(filepath.Join(filepath.Dir(f.selection.DataDir), "large.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	w := zip.NewWriter(archive)
	type identity struct {
		Size int64  `json:"byte_size"`
		Hash string `json:"sha256"`
	}
	expected := map[string]identity{}
	var expanded int64
	add := func(path string, size int64, input io.Reader) {
		t.Helper()
		out, err := w.CreateHeader(&zip.FileHeader{Name: path, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.New()
		n, err := io.CopyBuffer(io.MultiWriter(out, hash), io.LimitReader(input, size), make([]byte, 64<<10))
		if err != nil || n != size {
			t.Fatal("workload generation failed")
		}
		expanded += n
		expected[path] = identity{Size: n, Hash: hex.EncodeToString(hash.Sum(nil))}
	}
	const htmlSize = 40 << 20
	prefix, suffix := "<!doctype html><html><body>", "</body></html>"
	add("deep/nested/large.html", htmlSize, io.MultiReader(strings.NewReader(prefix), io.LimitReader(repeatedByte('a'), htmlSize-int64(len(prefix)+len(suffix))), strings.NewReader(suffix)))
	add("unknown.attachment", 12<<20, rand.New(rand.NewSource(20261016)))
	const first = "<!doctype html><title>Small first page</title><p>F12 import proof</p>"
	add("first.html", int64(len(first)), strings.NewReader(first))
	for i := 0; i < 1024; i++ {
		add(fmt.Sprintf("entries/%04d.data", i), int64(i+1), repeatedByte(byte(i)))
	}
	if _, err = w.Create("represented-empty/"); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := archive.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= 10<<20 || expanded <= 32<<20 || len(expected) <= 256 || expected["deep/nested/large.html"].Size <= 16<<20 {
		t.Fatal("fixture did not cross every former limit")
	}
	id := f.upload("archive", "large-result.zip")
	if _, err = archive.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	offset := int64(0)
	buffer := make([]byte, 1<<20)
	for {
		n, readErr := io.ReadFull(archive, buffer)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			t.Fatal(readErr)
		}
		if n == 0 {
			break
		}
		final := offset+int64(n) == info.Size()
		status, ack, data := f.put(id, "_input", "file", buffer[:n], offset, final)
		requireStatus(t, status, 200, data)
		offset += int64(n)
		if ack.Checkpoint.AcceptedBytes != fmt.Sprint(offset) {
			t.Fatal("progress differs from accepted bytes")
		}
		if offset == int64(len(buffer))*3 {
			before := ack.Checkpoint
			f.restart()
			if f.detail(id).Checkpoint != before {
				t.Fatal("F12 resume lost acknowledged checkpoint")
			}
		}
	}
	f.seal(id, 300*time.Second)
	version := f.publish(id, operation(t))
	f.restart()
	seen := map[string]bool{}
	directories := map[string]bool{"deep": false, "deep/nested": false, "entries": false, "represented-empty": false}
	after := ""
	for {
		entries, err := f.store.Entries(context.Background(), version.ContentSetID, after, 64)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			after = entry.Path
			if entry.Kind != "file" {
				if _, ok := directories[entry.Path]; !ok || entry.Kind != "directory" {
					t.Fatalf("unexpected stored type/path: %q %s", entry.Path, entry.Kind)
				}
				directories[entry.Path] = true
				continue
			}
			want, ok := expected[entry.Path]
			if !ok {
				t.Fatalf("unexpected stored file: %q", entry.Path)
			}
			hash := sha256.New()
			var n int64
			err = f.store.Read(context.Background(), domain.Object{Hash: entry.Hash, Size: entry.Size, State: "ready"}, domain.Pin{Kind: "active_read", OwnerID: "large-workload-proof"}, func(r io.ReadSeeker) error {
				var err error
				n, err = io.CopyBuffer(hash, r, make([]byte, 64<<10))
				return err
			})
			if err != nil || n != want.Size || hex.EncodeToString(hash.Sum(nil)) != want.Hash {
				t.Fatalf("workload bytes changed: %q %v", entry.Path, err)
			}
			seen[entry.Path] = true
		}
	}
	if len(seen) != len(expected) {
		t.Fatalf("workload files omitted: got %d want %d", len(seen), len(expected))
	}
	for path, present := range directories {
		if !present {
			t.Fatalf("F12 directory omitted: %q", path)
		}
	}
	if path := os.Getenv("HAERYONG_TASK004_WORKLOAD_REPORT"); path != "" {
		report := map[string]any{"fixture": "F12 synthetic actual HTTP/SQLite streamed archive", "scope": "Task04 import/resume/restart only; no viewer/export/global F12 claim", "archive_bytes": info.Size(), "expanded_file_bytes": expanded, "regular_file_count": len(expected), "ordinary_files": 1024, "poorly_compressible_attachment_bytes": 12 << 20, "attachment_deterministic_seed": 20261016, "small_first_html": "first.html", "largest_file_bytes": expected["deep/nested/large.html"].Size, "restart_all_file_hashes_equal": true, "mid_transfer_restart_checkpoint_equal": true, "transport_chunk_bytes": len(buffer), "environment": map[string]any{"goos": runtime.GOOS, "goarch": runtime.GOARCH, "available_disk_bytes_before_fixture": uint64(resources.Bavail) * uint64(resources.Bsize)}, "files": expected}
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = file.Write(append(data, '\n'))
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatal("workload evidence write failed")
		}
	}
}
