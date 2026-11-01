// Package skills embeds the product-owned guidance from its single source.
package skills

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

//go:embed use-haeryong/SKILL.md
var guidance []byte

type Descriptor struct{ Version, SHA256 string }

func Owned() (Descriptor, error) {
	var header struct {
		Name     string `yaml:"name"`
		Metadata struct {
			Version string `yaml:"version"`
		} `yaml:"metadata"`
	}
	parts := bytes.SplitN(guidance, []byte("---\n"), 3)
	if len(parts) != 3 || len(parts[0]) != 0 || yaml.Unmarshal(parts[1], &header) != nil || header.Name != "use-haeryong" || header.Metadata.Version == "" {
		return Descriptor{}, errors.New("invalid owned skill guidance")
	}
	sum := sha256.Sum256(guidance)
	return Descriptor{Version: header.Metadata.Version, SHA256: hex.EncodeToString(sum[:])}, nil
}
func Content() []byte { return bytes.Clone(guidance) }

// Materialize is host-local packaging, not a global skill installation. The
// verified bytes and version are the same ones exported to compatible hosts.
func Materialize(root string) (string, Descriptor, error) {
	d, e := Owned()
	if e != nil {
		return "", d, e
	}
	if !filepath.IsAbs(root) {
		return "", d, errors.New("absolute private skill root required")
	}
	directory := filepath.Join(root, d.SHA256, "use-haeryong")
	if e = os.MkdirAll(directory, 0700); e != nil {
		return "", d, e
	}
	physical, e := filepath.EvalSymlinks(directory)
	if e != nil || physical != directory {
		return "", d, errors.New("physical private skill directory required")
	}
	info, e := os.Stat(directory)
	if e != nil || info.Mode().Perm()&0077 != 0 {
		return "", d, errors.New("private skill directory required")
	}
	path := filepath.Join(directory, "SKILL.md")
	if info, e := os.Lstat(path); e == nil && !info.Mode().IsRegular() {
		return "", d, errors.New("owned skill must be a regular file")
	}
	existing, e := os.ReadFile(path)
	if e == nil {
		if !bytes.Equal(existing, guidance) {
			return "", d, errors.New("owned skill bytes changed")
		}
		return path, d, nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return "", d, e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", d, e
	}
	_, writeErr := f.Write(guidance)
	syncErr := f.Sync()
	closeErr := f.Close()
	if e = errors.Join(writeErr, syncErr, closeErr); e != nil {
		return "", d, e
	}
	return path, d, nil
}
