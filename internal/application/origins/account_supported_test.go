//go:build (darwin && !osusergo) || (linux && cgo && !osusergo)

package origins

import (
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This build resolves account metadata through os/user's native account
// database path (darwin natively even without cgo; linux through
// getpwuid_r with cgo). The production resolver must therefore be
// independent of HOME/XDG inputs: those variables never select the
// namespace on a supported build.
var _ = Describe("Production account resolver", Label("unit"), func() {
	It("resolves the account home independently of HOME and XDG inputs", func() {
		first, err := AccountHome()
		Expect(err).NotTo(HaveOccurred())
		Expect(filepath.IsAbs(first)).To(BeTrue())
		restoreHome := withEnv("HOME", "/tmp/haeryong-fake-home")
		defer restoreHome()
		restoreXDG := withEnv("XDG_DATA_HOME", "/tmp/haeryong-fake-data")
		defer restoreXDG()
		second, err := AccountHome()
		Expect(err).NotTo(HaveOccurred())
		Expect(second).To(Equal(first))
		base, err := ProdBase(nil)
		Expect(err).NotTo(HaveOccurred())
		expected, err := baseDirFor(runtime.GOOS, first)
		Expect(err).NotTo(HaveOccurred())
		Expect(base).To(Equal(expected))
	})
})
