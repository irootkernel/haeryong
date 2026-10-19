//go:build !haeryong_test_state

package main

import "haeryong.local/haeryong/internal/application/origins"

// companionStateBase resolves the production per-user Haeryong base from
// real account metadata. Untagged production builds expose no state or
// credential override: HAERYONG_TEST_STATE_DIR is ignored, HOME/XDG never
// participate, and the protected desktop credential namespace always lives
// under the UID account-home app-state.
func companionStateBase() (string, error) {
	return origins.ProdBase(origins.AccountHome)
}
