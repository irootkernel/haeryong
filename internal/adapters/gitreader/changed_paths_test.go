package gitreader_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/gitreader"
)

var _ = Describe("changed path listing", Label("integration"), func() {
	// W3b L10: the listing is NUL-separated, so names with non-ASCII bytes,
	// spaces or quotes are never C-quoted and compare byte for byte with a
	// cited path.
	It("reports changed paths exactly, without C-quoting", func() {
		if _, err := exec.LookPath("git"); err != nil {
			Skip("git binary unavailable")
		}
		r := newRepo()
		base := r.commit(map[string]string{"plain.txt": "one\n"})
		tip := r.commit(map[string]string{"문서 a.txt": "two\n", "quote\"d.txt": "three\n", "dir/tab\tname.txt": "four\n"})
		paths, err := gitreader.New().ChangedPaths(context.Background(), r.dir, base, tip, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(paths).To(ConsistOf("문서 a.txt", "quote\"d.txt", "dir/tab\tname.txt"))
		cited, err := gitreader.New().ChangedPaths(context.Background(), r.dir, base, tip, []string{"문서 a.txt"})
		Expect(err).NotTo(HaveOccurred())
		Expect(cited).To(Equal([]string{"문서 a.txt"}))
	})

	// W3d (plan r4 §7.6): a mode-only change keeps the blob, so it is no byte
	// difference.
	It("ignores a mode-only change", func() {
		if _, err := exec.LookPath("git"); err != nil {
			Skip("git binary unavailable")
		}
		r := newRepo()
		base := r.commit(map[string]string{"run.sh": "echo\n"})
		Expect(os.Chmod(filepath.Join(r.dir, "run.sh"), 0o755)).To(Succeed())
		git(r.dir, "add", "-A")
		git(r.dir, "commit", "-m", "mode only")
		tip := git(r.dir, "rev-parse", "HEAD")
		Expect(tip).NotTo(Equal(base))
		paths, err := gitreader.New().ChangedPaths(context.Background(), r.dir, base, tip, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(paths).To(BeEmpty())
		changed := r.commit(map[string]string{"run.sh": "echo changed\n"})
		paths, err = gitreader.New().ChangedPaths(context.Background(), r.dir, tip, changed, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(paths).To(Equal([]string{"run.sh"}), "a content change still counts")
	})
})
