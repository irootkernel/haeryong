//go:build haeryong_test_state

package main

import (
	"errors"
	"os"
	"path/filepath"

	"haeryong.local/haeryong/internal/application/origins"
)

// renderStateBase honors the single internal test override for the app-state
// path. Only this tagged build accepts HAERYONG_TEST_STATE_DIR, and it is
// strictly path-only: an absolute directory for the origin ledger base; the
// daemon otherwise runs the real product runner, handlers, auth, storage and
// render code with no fake behavior.
func renderStateBase() (string, error) {
	if selected := os.Getenv("HAERYONG_TEST_STATE_DIR"); selected != "" {
		if !filepath.IsAbs(selected) {
			return "", errors.New("HAERYONG_TEST_STATE_DIR must be an absolute directory")
		}
		return filepath.Clean(selected), nil
	}
	return origins.ProdBase(origins.AccountHome)
}
