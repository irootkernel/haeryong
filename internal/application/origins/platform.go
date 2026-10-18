// Package origins owns the per-user app-scoped renderer-origin reservation
// ledger: one durable ownership authority spanning every library and profile
// of a single OS user account (TASK-003-03 origin amendment A3+C1). The
// ledger lives outside every library database, is created lazily on first
// renderer use, and never reassigns a reserved origin.
package origins

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
)

// HomeResolver returns the absolute account-database home directory of the
// process's effective OS user. Production uses real account metadata only.
type HomeResolver func() (string, error)

// ErrUnsupportedPlatform marks a process that cannot honestly resolve the
// current account's home from account metadata. The renderer subsystem stays
// unavailable while ordinary library flows keep working.
type ErrUnsupportedPlatform struct{ Cause error }

func (e *ErrUnsupportedPlatform) Error() string {
	return "renderer origin state requires account metadata this build cannot resolve: " + e.Cause.Error()
}
func (e *ErrUnsupportedPlatform) Unwrap() error { return e.Cause }

// lookupAccountHome resolves the effective UID's account-database home using
// only real account metadata. It is implemented by the platform adapters:
//
//   - darwin without osusergo, including CGO_ENABLED=0: the Go toolchain's
//     native account lookup through os/user.
//   - linux with cgo and without osusergo: getpwuid_r through os/user.
//   - other configurations fail closed. In particular, Linux's pure-Go
//     os/user fallback may synthesize Current() from HOME for an unknown UID;
//     it is not used as a production account authority here.
//
// The shared os/user path below is only reached from adapters whose build
// configuration guarantees account-database results; LookupUserId's
// Current() shortcut is therefore account metadata in exactly those builds.
func lookupViaUser() (string, error) {
	entry, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil {
		return "", &ErrUnsupportedPlatform{Cause: err}
	}
	if entry.HomeDir == "" || !filepath.IsAbs(entry.HomeDir) {
		return "", &ErrUnsupportedPlatform{Cause: fmt.Errorf("account home %q is not an absolute path", entry.HomeDir)}
	}
	return filepath.Clean(entry.HomeDir), nil
}

// AccountHome resolves the effective UID's account-database home directory
// with no environment fallback. Lookup failure, a relative/empty home or an
// unsupported build fails closed.
func AccountHome() (string, error) { return lookupAccountHome() }

// baseDirFor derives the per-user Haeryong base directory for a platform
// from a resolved account home, independently of XDG_DATA_HOME, HOME,
// profiles and --data-dir.
func baseDirFor(goos, home string) (string, error) {
	if home == "" || !filepath.IsAbs(home) {
		return "", &ErrUnsupportedPlatform{Cause: fmt.Errorf("account home %q is not an absolute path", home)}
	}
	clean := filepath.Clean(home)
	switch goos {
	case "darwin":
		return filepath.Join(clean, "Library", "Application Support", "Haeryong"), nil
	case "linux":
		return filepath.Join(clean, ".local", "share", "haeryong"), nil
	default:
		return "", &ErrUnsupportedPlatform{Cause: fmt.Errorf("origin state layout is not supported on %s", goos)}
	}
}

// BaseDir derives the fixed per-user Haeryong base from a resolved account
// home on the current platform.
func BaseDir(home string) (string, error) { return baseDirFor(runtime.GOOS, home) }

// ProdBase resolves the production per-user base directory. The resolver is
// injectable for focused checks; production passes AccountHome. Environment
// inputs never participate in the resolution.
func ProdBase(resolve HomeResolver) (string, error) {
	if resolve == nil {
		resolve = AccountHome
	}
	home, err := resolve()
	if err != nil {
		return "", err
	}
	return BaseDir(home)
}
