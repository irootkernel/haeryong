// Package configpath validates explicitly chosen service-host paths without
// creating directories, reading source content or configuring external services.
package configpath

import (
	"fmt"
	"os"
	"path/filepath"
)

type Resolver struct{}

func (Resolver) Canonical(path string, output bool) (string, error) {
	full, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	canonical, e := filepath.EvalSymlinks(full)
	if e != nil && output && os.IsNotExist(e) {
		parent, e := filepath.EvalSymlinks(filepath.Dir(full))
		if e != nil {
			return "", e
		}
		info, e := os.Stat(parent)
		if e != nil {
			return "", e
		}
		if !info.IsDir() {
			return "", fmt.Errorf("output parent is not a directory")
		}
		return filepath.Join(parent, filepath.Base(full)), nil
	}
	if e != nil {
		return "", e
	}
	info, e := os.Stat(canonical)
	if e != nil {
		return "", e
	}
	if !info.IsDir() {
		return "", fmt.Errorf("selected configuration path is not a directory")
	}
	return canonical, nil
}
