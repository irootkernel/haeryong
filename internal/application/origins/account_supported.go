//go:build (darwin && !osusergo) || (linux && cgo && !osusergo)

package origins

// Verified account metadata for this build. Go 1.27.1 selects os/user's
// cgo_lookup_unix.go ("(cgo || darwin) && !osusergo && unix && !android")
// here: darwin resolves natively even in CGO_ENABLED=0 builds, and linux
// with cgo resolves through getpwuid_r/NSS. LookupUserId's Current()
// shortcut (lookup.go:48-52) is account-database-backed in exactly these
// builds, so no environment-derived home can reach the result.
func lookupAccountHome() (string, error) { return lookupViaUser() }
