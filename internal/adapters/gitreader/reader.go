// Package gitreader reads exact revisions through the system git binary using
// plumbing object reads only. It never checks out, fetches, pulls, invokes
// hooks, filters or textconv, and never modifies the repository.
package gitreader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"haeryong.local/haeryong/internal/application/source"
)

// Typed reader errors live in the application port (SN4): adapters may
// import application/domain; the reverse import is rejected by the prepare
// stage.
var (
	ErrObjectMissing   = source.ErrObjectMissing
	ErrInvalidLocation = source.ErrInvalidLocation
	ErrInvalidRoot     = source.ErrInvalidRoot
)

var fullRevision = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)

// Reader executes the plumbing subset the source contract allows. The binary
// name stays configurable for tests; production wiring uses "git".
type Reader struct {
	Binary string
}

func New() *Reader { return &Reader{Binary: "git"} }

// environment drops every inherited GIT_* variable so reads cannot leave
// the approved root (a caller launched from a git hook must not redirect
// GIT_DIR/GIT_WORK_TREE/...), then sets the closed protections: no replace
// refs (they would change exact-revision bytes), no lazy fetch (a partial
// clone must never fetch), literal pathspecs (cited paths are data, never
// pathspec magic), no system config and no prompts (SN5). The ceiling is the
// resolved root's parent (G4): discovery must never walk to a repository
// above the selected root even when the root itself is not a repository.
func environment(ceiling string) []string {
	env := []string{}
	for _, entry := range os.Environ() {
		if name, _, ok := strings.Cut(entry, "="); ok {
			if strings.HasPrefix(name, "GIT_") {
				continue
			}
			env = append(env, entry)
		}
	}
	if ceiling != "" {
		env = append(env, "GIT_CEILING_DIRECTORIES="+ceiling)
	}
	return append(env,
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_LITERAL_PATHSPECS=1",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
}

func (r *Reader) command(ctx context.Context, root string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.Binary, args...)
	cmd.Dir = root
	cmd.Env = environment(ceilingOf(root))
	return cmd
}

// ceilingOf yields the directory above the resolved root. Git compares
// GIT_CEILING_DIRECTORIES with its resolved working directory, so the parent
// of a symlinked root's own path would not stop discovery above the real
// directory (G4).
func ceilingOf(root string) string {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		// The command itself fails on a vanished root; keep the configured
		// path's parent as the ceiling.
		resolved = root
	}
	resolved = filepath.Clean(resolved)
	parent := filepath.Dir(resolved)
	if parent == resolved {
		return ""
	}
	return parent
}

// ValidateLocation rejects absolute paths and traversal outside the
// repository; the caller supplies the repository-relative evidence path.
func ValidateLocation(p string) error {
	if p == "" || strings.Contains(p, "\x00") {
		return ErrInvalidLocation
	}
	if path.IsAbs(p) || path.Clean(p) != p || p == "." || strings.HasPrefix(p, "../") || strings.Contains(p, "/../") {
		return ErrInvalidLocation
	}
	return nil
}

// ResolveRevision verifies the pinned full commit id exists inside an
// existing mapped repository. It never resolves a branch: verification
// targets immutable evidence revisions only. A missing/non-repository root is
// a distinct typed fact (SN6).
func (r *Reader) ResolveRevision(ctx context.Context, root, revision string) error {
	if err := checkRoot(root); err != nil {
		return err
	}
	if !fullRevision.MatchString(revision) {
		return ErrObjectMissing
	}
	cmd := r.command(ctx, root, "cat-file", "-e", revision+"^{commit}")
	return commandError(ctx, cmd.Run(), ErrObjectMissing)
}

// absent reports whether a file-system error states that a path does not
// exist or is not a directory — the only facts that make a root invalid.
// Every other error (EIO, EACCES, …) is an I/O failure that propagates and
// never becomes a recorded source fact (S12, W3d).
func absent(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// isGitDir applies git's own repository-directory rule (setup.c
// is_git_directory): a valid HEAD in dir, and objects/ and refs/ directories
// in its common dir — the commondir target for a linked worktree, else dir
// itself (G4). An I/O error other than absence is returned.
func isGitDir(dir string) (bool, error) {
	if ok, err := validHead(filepath.Join(dir, "HEAD")); !ok || err != nil {
		return false, err
	}
	common := dir
	raw, err := os.ReadFile(filepath.Join(dir, "commondir"))
	if err == nil {
		target := strings.TrimSpace(string(raw))
		if target == "" {
			return false, nil
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(dir, target)
		}
		common = target
	} else if !absent(err) {
		return false, err
	}
	for _, name := range []string{"objects", "refs"} {
		if ok, err := isDir(filepath.Join(common, name)); !ok || err != nil {
			return false, err
		}
	}
	return true, nil
}

var headObjectID = regexp.MustCompile(`^(?:[0-9a-fA-F]{64}|[0-9a-fA-F]{40})(?:\s|$)`)

// validHead mirrors git's validate_headref: a "refs/…" symlink, a "ref:"
// symref naming refs/…, or a detached object id.
func validHead(name string) (bool, error) {
	info, err := os.Lstat(name)
	if err != nil {
		if absent(err) {
			return false, nil
		}
		return false, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(name)
		if err != nil {
			return false, err
		}
		return strings.HasPrefix(target, "refs/"), nil
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		return false, err
	}
	if ref, ok := strings.CutPrefix(string(raw), "ref:"); ok {
		return strings.HasPrefix(strings.TrimLeft(ref, " \t\n\v\f\r"), "refs/"), nil
	}
	return headObjectID.MatchString(string(raw)), nil
}

func isDir(name string) (bool, error) {
	info, err := os.Stat(name)
	if err != nil {
		if absent(err) {
			return false, nil
		}
		return false, err
	}
	return info.IsDir(), nil
}

// checkRoot accepts a normal repository directory (a .git dir that is a git
// directory), a gitfile checkout (a .git file naming one, such as a linked
// worktree or a submodule) and a bare root that itself is one; an empty or
// half-built .git directory, a stray HEAD file or any other invalid
// repository is missing_source (S10/G4) — never a path to a parent
// repository. An I/O error other than absence propagates (W3d).
func checkRoot(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		if absent(err) {
			return ErrInvalidRoot
		}
		return err
	}
	if !info.IsDir() {
		return ErrInvalidRoot
	}
	verdict := func(ok bool, err error) error {
		if err != nil {
			return err
		}
		if !ok {
			return ErrInvalidRoot
		}
		return nil
	}
	gitPath := filepath.Join(root, ".git")
	entry, err := os.Stat(gitPath)
	if err == nil {
		if entry.IsDir() {
			return verdict(isGitDir(gitPath))
		}
		if !entry.Mode().IsRegular() {
			return ErrInvalidRoot
		}
		content, readErr := os.ReadFile(gitPath)
		if readErr != nil {
			if absent(readErr) {
				return ErrInvalidRoot
			}
			return readErr
		}
		if !strings.HasPrefix(string(content), "gitdir:") {
			return ErrInvalidRoot
		}
		target := strings.TrimSpace(strings.TrimPrefix(string(content), "gitdir:"))
		if target == "" {
			return ErrInvalidRoot
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(root, target)
		}
		return verdict(isGitDir(target))
	}
	if !absent(err) {
		return err
	}
	// A bare root that itself is a git directory.
	return verdict(isGitDir(root))
}

// ResolveHead resolves the branch tip exactly once for an observation run.
// The caller persists the returned id before any dependent read.
func (r *Reader) ResolveHead(ctx context.Context, root string) (string, error) {
	if err := checkRoot(root); err != nil {
		return "", err
	}
	out, err := r.command(ctx, root, "rev-parse", "--verify", "--quiet", "HEAD^{commit}").Output()
	if err != nil {
		return "", commandError(ctx, err, ErrObjectMissing)
	}
	revision := strings.TrimSpace(string(out))
	if !fullRevision.MatchString(revision) {
		return "", ErrObjectMissing
	}
	return revision, nil
}

// ChangedPaths lists the paths whose bytes differ between two full commit
// ids, restricted to the cited paths when given. It compares trees with
// plumbing; no textconv is requested and no filters run. The raw output is
// NUL-separated (-z), so a path is never C-quoted and compares byte for byte
// with a cited path (W3b L10); an entry whose blob id is unchanged (a mode-only
// change) is no byte difference (plan r4 §7.6, W3d).
func (r *Reader) ChangedPaths(ctx context.Context, root, base, tip string, cited []string) ([]string, error) {
	args := []string{"diff-tree", "-r", "--no-renames", "--no-abbrev", "-z", base, tip}
	if len(cited) > 0 {
		args = append(args, "--")
		args = append(args, cited...)
	}
	out, err := r.output(ctx, root, args...)
	if err != nil {
		return nil, err
	}
	changed := []string{}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		// ":<old mode> <new mode> <old id> <new id> <status>" then the path.
		meta := strings.Fields(strings.TrimPrefix(fields[i], ":"))
		if len(meta) < 5 {
			return nil, fmt.Errorf("%w: git diff-tree: unexpected raw entry", ErrGitUnavailable)
		}
		if meta[2] == meta[3] {
			continue
		}
		changed = append(changed, fields[i+1])
	}
	return changed, nil
}

// ErrGitUnavailable marks a git-binary execution failure (missing binary,
// signal, I/O): an infrastructure condition, never a source fact about the
// target.
var ErrGitUnavailable = source.ErrGitUnavailable

// commandError maps only a normal git exit to a target fact. Signal and
// start failures are infrastructure errors; cancellation keeps its own error.
func commandError(ctx context.Context, err, targetError error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ProcessState.Exited() {
		return targetError
	}
	return fmt.Errorf("%w: git command: %v", ErrGitUnavailable, err)
}

func (r *Reader) output(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmd := r.command(ctx, root, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("%w: git %s", ErrGitUnavailable, args[0])
		}
		return nil, fmt.Errorf("%w: git %s: %v", ErrGitUnavailable, args[0], err)
	}
	return stdout.Bytes(), nil
}

// ReadBlob streams the complete blob bytes of path at the pinned revision.
// The stream is the exact object content; the bounded-bytes selection and
// hashing happen in the application layer.
func (r *Reader) ReadBlob(ctx context.Context, root, revision, evidencePath string) (io.ReadCloser, error) {
	if err := ValidateLocation(evidencePath); err != nil {
		return nil, err
	}
	if !fullRevision.MatchString(revision) {
		return nil, ErrObjectMissing
	}
	if err := r.command(ctx, root, "cat-file", "-e", revision+":"+evidencePath).Run(); err != nil {
		return nil, commandError(ctx, err, ErrObjectMissing)
	}
	cmd := r.command(ctx, root, "cat-file", "blob", revision+":"+evidencePath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, commandError(ctx, err, ErrObjectMissing)
	}
	return &blobStream{ctx: ctx, cmd: cmd, pipe: stdout}, nil
}

type blobStream struct {
	ctx    context.Context
	cmd    *exec.Cmd
	pipe   io.ReadCloser
	closed bool
}

func (b *blobStream) Read(p []byte) (int, error) { return b.pipe.Read(p) }

func (b *blobStream) Close() error {
	if b.closed {
		return nil
	}
	b.closed = true
	err := b.pipe.Close()
	if waitErr := commandError(b.ctx, b.cmd.Wait(), io.ErrUnexpectedEOF); waitErr != nil {
		return waitErr
	}
	return err
}
