package origins

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Per-user base layout", Label("unit"), func() {
	It("derives the darwin base from the account home", func() {
		base, err := baseDirFor("darwin", "/Users/owner")
		Expect(err).NotTo(HaveOccurred())
		Expect(base).To(Equal(filepath.Join("/Users", "owner", "Library", "Application Support", "Haeryong")))
	})
	It("derives the linux base from the account home", func() {
		base, err := baseDirFor("linux", "/home/owner")
		Expect(err).NotTo(HaveOccurred())
		Expect(base).To(Equal(filepath.Join("/home", "owner", ".local", "share", "haeryong")))
	})
	It("rejects unsupported platforms and non-absolute homes", func() {
		_, err := baseDirFor("windows", `C:\Users\owner`)
		Expect(err).To(BeAssignableToTypeOf(&ErrUnsupportedPlatform{}))
		_, err = baseDirFor("darwin", "relative/path")
		Expect(err).To(BeAssignableToTypeOf(&ErrUnsupportedPlatform{}))
		_, err = baseDirFor("linux", "")
		Expect(err).To(BeAssignableToTypeOf(&ErrUnsupportedPlatform{}))
	})
	It("fails closed when the injected resolver cannot produce an account home", func() {
		_, err := ProdBase(func() (string, error) { return "", &ErrUnsupportedPlatform{} })
		Expect(err).To(BeAssignableToTypeOf(&ErrUnsupportedPlatform{}))
		_, err = ProdBase(func() (string, error) { return "relative", nil })
		Expect(err).To(BeAssignableToTypeOf(&ErrUnsupportedPlatform{}))
	})
})

// withEnv restores the previous environment around a mutation.
func withEnv(key, value string) func() {
	previous, had := os.LookupEnv(key)
	Expect(os.Setenv(key, value)).To(Succeed())
	return func() {
		if had {
			Expect(os.Setenv(key, previous)).To(Succeed())
		} else {
			Expect(os.Unsetenv(key)).To(Succeed())
		}
	}
}
