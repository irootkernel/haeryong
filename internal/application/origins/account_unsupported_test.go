//go:build (darwin && osusergo) || (linux && (!cgo || osusergo)) || (!darwin && !linux)

package origins

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// On builds where os/user's current() can fall back to $HOME/$USER
// (pure-Go linux without a local account-database entry, or osusergo
// builds), the production resolver must refuse honestly instead of ever
// selecting a namespace from the environment.
var _ = Describe("Production account resolver on unsupported builds", Label("unit"), func() {
	It("fails closed even when an absolute HOME could satisfy the stub", func() {
		restoreHome := withEnv("HOME", "/tmp/haeryong-fake-home")
		defer restoreHome()
		restoreUser := withEnv("USER", "fake-owner")
		defer restoreUser()
		_, err := AccountHome()
		Expect(err).To(BeAssignableToTypeOf(&ErrUnsupportedPlatform{}))
		_, err = ProdBase(nil)
		Expect(err).To(BeAssignableToTypeOf(&ErrUnsupportedPlatform{}))
	})
})
