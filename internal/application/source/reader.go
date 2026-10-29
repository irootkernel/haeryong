package source

import "errors"

// Typed reader facts (SN4/SN6). The application port owns the sentinels so
// both the adapter and the service classify identical errors; the registry's
// unavailable reasons derive from them.
var (
	ErrSourceChanged = errors.New("selected source changed during open")
	ErrSourceDenied  = errors.New("selected source is outside managed collection policy")
	// ErrObjectMissing marks a pinned revision or its path that cannot be
	// read at an existing mapped repository.
	ErrObjectMissing = errors.New("git object missing")

	// ErrInvalidLocation marks an evidence location that is not a clean
	// repository-relative path.
	ErrInvalidLocation = errors.New("invalid repository-relative location")

	// ErrInvalidRoot marks a mapped root that is missing or not a git
	// repository: the source-not-target fact behind reason missing_source.
	ErrInvalidRoot = errors.New("mapped source root missing")

	// ErrGitUnavailable marks a git-binary execution failure: an
	// infrastructure condition, never a source fact about the target.
	ErrGitUnavailable = errors.New("git reader unavailable")
)
