// Package source opens only selected ordinary files beneath configured roots.
// It does not enumerate arbitrary host paths, edit source, or confine other tools.
package source

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	app "haeryong.local/haeryong/internal/application/source"
)

// Files receives concrete host-owned exclusions (profile and authentication
// paths), not filename heuristics. Ordinary hidden/binary/large files are valid.
type Files struct{ ExcludedPaths []string }
type ownedRead struct {
	*os.File
	root *os.Root
	ctx  context.Context
}

func (r *ownedRead) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.File.Read(p)
}
func (r *ownedRead) Close() error { return errors.Join(r.File.Close(), r.root.Close()) }
func within(root, path string) bool {
	relative, e := filepath.Rel(root, path)
	return e == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func (f Files) Open(ctx context.Context, root, path string) (io.ReadCloser, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || path == "." || strings.ContainsRune(path, 0) || strings.HasPrefix(path, "../") {
		return nil, app.ErrInvalidLocation
	}
	canonical, e := filepath.EvalSymlinks(root)
	if e != nil {
		return nil, e
	}
	if e = f.CheckLocation(ctx, canonical, path); e != nil {
		return nil, e
	}
	r, e := os.OpenRoot(canonical)
	if e != nil {
		return nil, e
	}
	file, e := r.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if e != nil {
		r.Close()
		if strings.Contains(e.Error(), "path escapes") {
			return nil, app.ErrInvalidLocation
		}
		return nil, e
	}
	fail := func(e error) (io.ReadCloser, error) { file.Close(); r.Close(); return nil, e }
	info, e := file.Stat()
	if e != nil {
		return fail(e)
	}
	if !info.Mode().IsRegular() {
		return fail(app.ErrInvalidLocation)
	}
	resolved, e := filepath.EvalSymlinks(filepath.Join(canonical, path))
	if e != nil {
		return fail(e)
	}
	if !within(canonical, resolved) {
		return fail(app.ErrInvalidLocation)
	}
	expected, e := os.Stat(resolved)
	if e != nil || !os.SameFile(info, expected) {
		return fail(app.ErrSourceChanged)
	}
	if e = f.checkExcluded(resolved, info); e != nil {
		return fail(e)
	}
	return &ownedRead{file, r, ctx}, nil
}

// ManagedFiles wires the actual service profile and the supported Codex host
// auth file explicitly. No global configuration or credential content is read.
func ManagedFiles(profileDir string, extraOwnedPaths ...string) Files {
	excluded := []string{profileDir, filepath.Join(profileDir, "profile.json")}
	excluded = append(excluded, extraOwnedPaths...)
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		if ownerHome, e := os.UserHomeDir(); e == nil {
			home = filepath.Join(ownerHome, ".codex")
		}
	}
	if home != "" {
		excluded = append(excluded, filepath.Join(home, "auth.json"))
	}
	if config, e := os.UserConfigDir(); e == nil {
		excluded = append(excluded, filepath.Join(config, "haeryong", "clients"))
	}
	return Files{ExcludedPaths: excluded}
}

// CheckLocation excludes concrete live host paths before a Git object read as
// well as before opening a working-copy file. It is not a secret-name filter.
func (f Files) CheckLocation(ctx context.Context, root, path string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || path == "." || strings.ContainsRune(path, 0) || strings.HasPrefix(path, "../") {
		return app.ErrInvalidLocation
	}
	return f.checkExcluded(filepath.Join(root, path), nil)
}

// Directory identity, rather than caller spelling, fences case/firmlink and
// symlink aliases. Walking existing ancestors also protects a missing Git path
// beneath a known live store. Stat never reads the excluded file contents.
func (f Files) checkExcluded(candidate string, opened os.FileInfo) error {
	for _, excluded := range f.ExcludedPaths {
		protected, e := filepath.Abs(excluded)
		if e != nil {
			return e
		}
		if real, err := filepath.EvalSymlinks(protected); err == nil {
			protected = real
		} else if !os.IsNotExist(err) {
			return err
		}
		if within(protected, candidate) {
			return app.ErrSourceDenied
		}
		identity, e := os.Stat(protected)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if opened != nil && os.SameFile(opened, identity) {
			return app.ErrSourceDenied
		}
		for ancestor := candidate; ; ancestor = filepath.Dir(ancestor) {
			actual, e := os.Stat(ancestor)
			if e != nil && !os.IsNotExist(e) {
				return e
			}
			if e == nil && os.SameFile(actual, identity) {
				return app.ErrSourceDenied
			}
			if parent := filepath.Dir(ancestor); parent == ancestor {
				break
			}
		}
	}
	return nil
}
