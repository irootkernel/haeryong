//go:build (darwin && osusergo) || (linux && (!cgo || osusergo)) || (!darwin && !linux)

package origins

import "errors"

// Builds where os/user's current() comes from lookup_stubs.go (selected by
// "(!cgo && !darwin && !windows && !plan9) || android || (osusergo &&
// !windows && !plan9)"): it tries the account database first, but for a UID
// missing from it — for example an NSS-only account on pure-Go linux — it
// synthesizes the user from $HOME/$USER, and LookupId returns that
// environment-derived home when the uid matches. That must never select the
// production origin namespace, so these builds resolve the renderer
// honestly unsupported.
func lookupAccountHome() (string, error) {
	return "", &ErrUnsupportedPlatform{Cause: errors.New("this build's os/user current() can fall back to $HOME/$USER; account metadata is not verifiable without it")}
}
