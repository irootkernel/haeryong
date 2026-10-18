//go:build !haeryong_test_state

package main

import "haeryong.local/haeryong/internal/application/origins"

// renderStateBase resolves the production per-user Haeryong base from real
// account metadata. Untagged production builds expose no app-state override:
// profile, --data-dir, environment and configuration selections can never
// reset the origin namespace, and HAERYONG_TEST_STATE_DIR is ignored.
func renderStateBase() (string, error) {
	return origins.ProdBase(origins.AccountHome)
}
