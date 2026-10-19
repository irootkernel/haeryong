//go:build haeryong_test_state

package main

import (
	"errors"
	"os"
	"path/filepath"

	"haeryong.local/haeryong/internal/application/origins"
)

// companionStateBase honors the single internal test override for the
// companion's own state/credential base. Only this tagged build accepts
// HAERYONG_TEST_STATE_DIR, and it is strictly path-only: an absolute
// directory for the desktop credential namespace and diagnostics state. The
// daemon otherwise runs the real product code with no fake behavior, and no
// production build exposes any override.
func companionStateBase() (string, error) {
	if selected := os.Getenv("HAERYONG_TEST_STATE_DIR"); selected != "" {
		if !filepath.IsAbs(selected) {
			return "", errors.New("HAERYONG_TEST_STATE_DIR must be an absolute directory")
		}
		return filepath.Clean(selected), nil
	}
	return origins.ProdBase(origins.AccountHome)
}
