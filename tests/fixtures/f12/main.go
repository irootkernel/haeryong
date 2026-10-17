package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
)

func panicMessage(args ...any)               { panic(fmt.Sprint(args...)) }
func panicFormat(format string, args ...any) { panic(fmt.Sprintf(format, args...)) }

type f12Identity struct {
	Size int64  `json:"byte_size"`
	Hash string `json:"sha256"`
}
type f12Byte byte

func (b f12Byte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(b)
	}
	return len(p), nil
}

// These are the exact original Task04 F12 bytes/ZIP construction, not reduced
// examples or application admission limits. The independent CLI test imports
// once and reuses that version for cursor/range/download/restart checks.
func f12Archive(root string) (string, int64, int64, map[string]f12Identity) {
	name := filepath.Join(root, "large-result.zip")
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		panicMessage(err)
	}
	w := zip.NewWriter(file)
	expected := map[string]f12Identity{}
	var expanded int64
	add := func(path string, size int64, input io.Reader) {
		out, err := w.CreateHeader(&zip.FileHeader{Name: path, Method: zip.Deflate})
		if err != nil {
			panicMessage(err)
		}
		hash := sha256.New()
		n, err := io.CopyBuffer(io.MultiWriter(out, hash), io.LimitReader(input, size), make([]byte, 64<<10))
		if err != nil || n != size {
			panicMessage("canonical F12 generation failed", err)
		}
		expanded += n
		expected[path] = f12Identity{n, hex.EncodeToString(hash.Sum(nil))}
	}
	const htmlSize = 40 << 20
	prefix, suffix := "<!doctype html><html><body>", "</body></html>"
	add("deep/nested/large.html", htmlSize, io.MultiReader(strings.NewReader(prefix), io.LimitReader(f12Byte('a'), htmlSize-int64(len(prefix)+len(suffix))), strings.NewReader(suffix)))
	add("unknown.attachment", 12<<20, rand.New(rand.NewSource(20261016)))
	const first = "<!doctype html><title>Small first page</title><p>F12 import proof</p>"
	add("first.html", int64(len(first)), strings.NewReader(first))
	for i := 0; i < 1024; i++ {
		add(fmt.Sprintf("entries/%04d.data", i), int64(i+1), f12Byte(byte(i)))
	}
	if _, err := w.Create("represented-empty/"); err != nil {
		panicMessage(err)
	}
	if err := w.Close(); err != nil {
		panicMessage(err)
	}
	if err := file.Sync(); err != nil {
		panicMessage(err)
	}
	info, err := file.Stat()
	if err != nil {
		panicMessage(err)
	}
	if err := file.Close(); err != nil {
		panicMessage(err)
	}
	if info.Size() != 12768177 || expanded != 55050821 || len(expected) != 1027 || len(first) != 69 {
		panicFormat("canonical F12 identity changed: zip=%d expanded=%d files=%d small=%d", info.Size(), expanded, len(expected), len(first))
	}
	return name, info.Size(), expanded, expected
}

func main() {
	if len(os.Args) != 2 {
		panic("one new test-owned output directory is required")
	}
	root, err := filepath.Abs(os.Args[1])
	if err != nil {
		panic(err)
	}
	if err = os.Mkdir(root, 0700); err != nil {
		panic(err)
	}
	archive, zipped, expanded, files := f12Archive(root)
	report := map[string]any{"archive": archive, "archive_bytes": zipped, "expanded_bytes": expanded, "files": files}
	raw, err := json.Marshal(report)
	if err != nil {
		panic(err)
	}
	if err = os.WriteFile(filepath.Join(root, "manifest.json"), raw, 0600); err != nil {
		panic(err)
	}
}
