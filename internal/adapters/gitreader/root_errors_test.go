package gitreader_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/gitreader"
)

var _ = Describe("checkout read failures", Label("integration"), func() {
	BeforeEach(func() {
		if _, err := exec.LookPath("git"); err != nil {
			Skip("git binary unavailable")
		}
		if os.Geteuid() == 0 {
			Skip("permission bits do not bind the superuser")
		}
	})

	// W3d (S12): only absence or an invalid structure makes a root invalid;
	// an unreadable checkout is an I/O failure that propagates.
	It("propagates an unreadable checkout instead of calling it missing", func() {
		r := newRepo()
		r.commit(map[string]string{"a.txt": "one\n"})
		gitDir := filepath.Join(r.dir, ".git")
		Expect(os.Chmod(gitDir, 0)).To(Succeed())
		DeferCleanup(func() { _ = os.Chmod(gitDir, 0o755) })
		_, err := gitreader.New().ResolveHead(context.Background(), r.dir)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, gitreader.ErrInvalidRoot)).To(BeFalse(), "an unreadable checkout is no missing source")
		Expect(errors.Is(err, fs.ErrPermission)).To(BeTrue(), "the I/O error propagates")
		_, err = gitreader.New().ResolveHead(context.Background(), filepath.Join(r.dir, "absent"))
		Expect(err).To(MatchError(gitreader.ErrInvalidRoot), "an absent checkout stays invalid")
	})
})
