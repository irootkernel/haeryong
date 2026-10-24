package gitreader_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/gitreader"
	"haeryong.local/haeryong/internal/application/source"
)

// repo builds a disposable git repository through the test repository itself
// and records its actual hashes, never invented sample values.
type repo struct {
	dir string
}

func git(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	ExpectWithOffset(1, cmd.Run()).To(Succeed(), "git %v: %s", args, out.String())
	return strings.TrimSpace(out.String())
}

func newRepo() *repo {
	dir := GinkgoT().TempDir()
	git(dir, "init", "--initial-branch=main")
	return &repo{dir: dir}
}

func (r *repo) commit(files map[string]string) string {
	for name, content := range files {
		ExpectWithOffset(1, os.MkdirAll(filepath.Dir(filepath.Join(r.dir, name)), 0o755)).To(Succeed())
		ExpectWithOffset(1, os.WriteFile(filepath.Join(r.dir, name), []byte(content), 0o644)).To(Succeed())
	}
	git(r.dir, "add", "-A")
	git(r.dir, "commit", "-m", "fixture")
	return git(r.dir, "rev-parse", "HEAD")
}

func sha(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

var _ = Describe("system git reader", Label("integration"), func() {
	var reader *gitreader.Reader
	var ctx context.Context
	BeforeEach(func() {
		if _, err := exec.LookPath("git"); err != nil {
			Skip("git binary unavailable")
		}
		reader = gitreader.New()
		ctx = context.Background()
	})

	It("verifies a pinned commit exists and rejects unknown revisions", func() {
		r := newRepo()
		id := r.commit(map[string]string{"a.txt": "one\n"})
		Expect(reader.ResolveRevision(ctx, r.dir, id)).To(Succeed())
		Expect(reader.ResolveRevision(ctx, r.dir, strings.Repeat("0", len(id)))).To(MatchError(gitreader.ErrObjectMissing))
		Expect(reader.ResolveRevision(ctx, r.dir, "HEAD")).To(MatchError(gitreader.ErrObjectMissing), "branch labels are never accepted")
	})

	It("streams exact blob bytes at the pinned revision", func() {
		r := newRepo()
		content := "package main\n\nfunc main() {}\n"
		id := r.commit(map[string]string{"pkg/main.go": content})
		stream, err := reader.ReadBlob(ctx, r.dir, id, "pkg/main.go")
		Expect(err).NotTo(HaveOccurred())
		blob, err := io.ReadAll(stream)
		Expect(err).NotTo(HaveOccurred())
		Expect(stream.Close()).To(Succeed())
		Expect(blob).To(Equal([]byte(content)))
	})

	It("keeps reading identical bytes while the working tree and branch move", func() {
		r := newRepo()
		id := r.commit(map[string]string{"svc/retry.go": "v1 line\n"})
		hashes := map[string]bool{}
		for i := 0; i < 3; i++ {
			// Mutate the working tree and advance the branch between reads.
			Expect(os.WriteFile(filepath.Join(r.dir, "svc/retry.go"), []byte("working copy mutation\n"), 0o644)).To(Succeed())
			r.commit(map[string]string{"ui/unrelated.html": "change " + strings.Repeat("x", i+1) + "\n"})
			stream, err := reader.ReadBlob(ctx, r.dir, id, "svc/retry.go")
			Expect(err).NotTo(HaveOccurred())
			result, err := source.SelectBounded(stream, 0, 0, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(stream.Close()).To(Succeed())
			hashes[result.SHA] = true
		}
		Expect(hashes).To(HaveLen(1), "pinned reads must stay byte-identical under races")
		Expect(hashes).To(HaveKey(sha([]byte("v1 line\n"))))
	})

	It("reports missing paths as object-missing facts", func() {
		r := newRepo()
		id := r.commit(map[string]string{"a.txt": "one\n"})
		_, err := reader.ReadBlob(ctx, r.dir, id, "missing.txt")
		Expect(err).To(MatchError(gitreader.ErrObjectMissing))
	})

	It("rejects unclean repository-relative locations", func() {
		Expect(gitreader.ValidateLocation("../escape.go")).To(MatchError(gitreader.ErrInvalidLocation))
		Expect(gitreader.ValidateLocation("a/../b.go")).To(MatchError(gitreader.ErrInvalidLocation))
		Expect(gitreader.ValidateLocation("/abs.go")).To(MatchError(gitreader.ErrInvalidLocation))
		Expect(gitreader.ValidateLocation("")).To(MatchError(gitreader.ErrInvalidLocation))
		Expect(gitreader.ValidateLocation("pkg/main.go")).To(Succeed())
		Expect(gitreader.ValidateLocation("유니코드/파일.go")).To(Succeed())
	})

	DescribeTable("reports a missing HEAD commit as ErrObjectMissing", func(unborn bool) {
		r := newRepo()
		if !unborn {
			head := r.commit(map[string]string{"f.txt": "fixture\n"})
			Expect(os.Remove(filepath.Join(r.dir, ".git", "objects", head[:2], head[2:]))).To(Succeed())
		}
		head, err := reader.ResolveHead(ctx, r.dir)
		Expect(err).To(MatchError(gitreader.ErrObjectMissing))
		Expect(head).To(BeEmpty())
	},
		Entry("unborn HEAD", true),
		Entry("missing HEAD object", false),
	)

	It("keeps HEAD execution failures distinct from missing objects", func() {
		r := newRepo()
		unavailable := &gitreader.Reader{Binary: filepath.Join(r.dir, "absent-git")}
		head, err := unavailable.ResolveHead(ctx, r.dir)
		Expect(err).To(MatchError(gitreader.ErrGitUnavailable))
		Expect(err).NotTo(MatchError(gitreader.ErrObjectMissing))
		Expect(head).To(BeEmpty())
	})

	It("resolves the branch head once and diffs cited paths only", func() {
		r := newRepo()
		base := r.commit(map[string]string{"svc/retry.go": "v1\n", "ui/page.html": "<p>v1</p>\n"})
		tip := r.commit(map[string]string{"svc/retry.go": "v2 selective\n", "ui/page.html": "<p>v2</p>\n"})
		head, err := reader.ResolveHead(ctx, r.dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(head).To(Equal(tip))
		cited, err := reader.ChangedPaths(ctx, r.dir, base, tip, []string{"svc/retry.go"})
		Expect(err).NotTo(HaveOccurred())
		Expect(cited).To(ConsistOf("svc/retry.go"))
		all, err := reader.ChangedPaths(ctx, r.dir, base, tip, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(ConsistOf("svc/retry.go", "ui/page.html"))
	})
})

var _ = Describe("hardened reader environment", Label("integration"), func() {
	var reader *gitreader.Reader
	var ctx context.Context
	BeforeEach(func() {
		if _, err := exec.LookPath("git"); err != nil {
			Skip("git binary unavailable")
		}
		reader = gitreader.New()
		ctx = context.Background()
	})

	It("ignores replace refs that would change exact-revision bytes", func() {
		r := newRepo()
		v1 := r.commit(map[string]string{"svc/a.go": "original bytes\n"})
		v2 := r.commit(map[string]string{"svc/a.go": "replacement bytes\n"})
		// A replace ref redirects v1 to v2; the reader must read v1's own
		// object bytes (GIT_NO_REPLACE_OBJECTS=1, SN5).
		git(r.dir, "update-ref", "refs/replace/"+v1, v2)
		stream, err := reader.ReadBlob(ctx, r.dir, v1, "svc/a.go")
		Expect(err).NotTo(HaveOccurred())
		blob, err := io.ReadAll(stream)
		Expect(err).NotTo(HaveOccurred())
		Expect(stream.Close()).To(Succeed())
		Expect(string(blob)).To(Equal("original bytes\n"))
	})

	It("treats cited paths as literal pathspecs, never magic", func() {
		r := newRepo()
		base := r.commit(map[string]string{"svc/a.go": "one\n"})
		tip := r.commit(map[string]string{"svc/a.go": "two\n", "ui/b.html": "x\n"})
		// A glob-magic pathspec must not silently widen the cited set
		// (GIT_LITERAL_PATHSPECS=1, SN5).
		changed, err := reader.ChangedPaths(ctx, r.dir, base, tip, []string{":(glob)svc/*.go"})
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeEmpty(), "magic pathspec was treated literally, matching no path")
		literal, err := reader.ChangedPaths(ctx, r.dir, base, tip, []string{"svc/a.go"})
		Expect(err).NotTo(HaveOccurred())
		Expect(literal).To(ConsistOf("svc/a.go"))
	})
})

var _ = Describe("infrastructure failures are never source facts", Label("integration"), func() {
	It("classifies a missing binary as ErrGitUnavailable (C12)", func() {
		t := GinkgoT()
		reader := &gitreader.Reader{Binary: "/nonexistent/haeryong-git-probe"}
		root := t.TempDir()
		// A repository-shaped root isolates the binary failure from the
		// missing-root fact (a real git directory layout).
		Expect(os.MkdirAll(filepath.Join(root, ".git", "objects"), 0o755)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(root, ".git", "refs"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)).To(Succeed())
		err := reader.ResolveRevision(context.Background(), root, strings.Repeat("a1b2c3d4", 5))
		Expect(err).To(MatchError(gitreader.ErrGitUnavailable))
		Expect(err).NotTo(MatchError(gitreader.ErrObjectMissing))
		_, err = reader.ReadBlob(context.Background(), t.TempDir(), strings.Repeat("a1b2c3d4", 5), "f.txt")
		Expect(err).To(MatchError(gitreader.ErrGitUnavailable))
		Expect(err).NotTo(MatchError(gitreader.ErrObjectMissing))
	})
})

var _ = Describe("ceiling and invalid repositories (S10)", Label("integration"), func() {
	var reader *gitreader.Reader
	var ctx context.Context
	BeforeEach(func() {
		if _, err := exec.LookPath("git"); err != nil {
			Skip("git binary unavailable")
		}
		reader = gitreader.New()
		ctx = context.Background()
	})

	It("never discovers a parent repository above the approved root", func() {
		parent := newRepo()
		git(parent.dir, "commit", "--allow-empty", "-m", "parent")
		child := filepath.Join(parent.dir, "child")
		Expect(os.MkdirAll(child, 0o755)).To(Succeed())
		// An invalid .git entry inside the child must not walk upward to the
		// parent repository (S10): the read is missing_source, never the
		// parent's objects.
		Expect(os.WriteFile(filepath.Join(child, ".git"), []byte("not a gitfile"), 0o644)).To(Succeed())
		err := reader.ResolveRevision(ctx, child, git(parent.dir, "rev-parse", "HEAD"))
		Expect(err).To(MatchError(gitreader.ErrInvalidRoot))
	})

	It("supports gitfile checkouts (.git file naming a gitdir)", func() {
		parent := newRepo()
		Expect(os.WriteFile(filepath.Join(parent.dir, "f.txt"), []byte("x\n"), 0o644)).To(Succeed())
		git(parent.dir, "add", "-A")
		git(parent.dir, "commit", "-m", "base")
		head := git(parent.dir, "rev-parse", "HEAD")
		work := filepath.Join(GinkgoT().TempDir(), "wt")
		git(parent.dir, "worktree", "add", "--detach", work, head)
		Expect(reader.ResolveRevision(ctx, work, head)).To(Succeed())
	})
})

var _ = Describe("repository detection and the discovery ceiling", Label("integration"), func() {
	var reader *gitreader.Reader
	var ctx context.Context
	BeforeEach(func() {
		if _, err := exec.LookPath("git"); err != nil {
			Skip("git binary unavailable")
		}
		reader = gitreader.New()
		ctx = context.Background()
	})

	It("never discovers the parent repository through an empty .git directory", func() {
		parent := newRepo()
		Expect(os.WriteFile(filepath.Join(parent.dir, "p.txt"), []byte("p\n"), 0o644)).To(Succeed())
		git(parent.dir, "add", "-A")
		git(parent.dir, "commit", "-m", "parent")
		head := git(parent.dir, "rev-parse", "HEAD")
		child := filepath.Join(parent.dir, "child")
		Expect(os.MkdirAll(filepath.Join(child, ".git"), 0o755)).To(Succeed())
		err := reader.ResolveRevision(ctx, child, head)
		Expect(err).To(MatchError(gitreader.ErrInvalidRoot), "missing_source, never the parent's objects")
	})

	It("never discovers the parent repository through a stray HEAD file", func() {
		parent := newRepo()
		Expect(os.WriteFile(filepath.Join(parent.dir, "p.txt"), []byte("p\n"), 0o644)).To(Succeed())
		git(parent.dir, "add", "-A")
		git(parent.dir, "commit", "-m", "parent")
		head := git(parent.dir, "rev-parse", "HEAD")
		child := filepath.Join(parent.dir, "child2")
		Expect(os.MkdirAll(child, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(child, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)).To(Succeed())
		err := reader.ResolveRevision(ctx, child, head)
		Expect(err).To(MatchError(gitreader.ErrInvalidRoot))
	})

	It("rejects a half-built .git directory that git itself would not accept", func() {
		parent := newRepo()
		head := parent.commit(map[string]string{"p.txt": "p\n"})
		// HEAD and objects/ without refs/: not a git directory by git's own
		// rule, so the root is missing_source.
		child := filepath.Join(parent.dir, "half")
		Expect(os.MkdirAll(filepath.Join(child, ".git", "objects"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(child, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)).To(Succeed())
		Expect(reader.ResolveRevision(ctx, child, head)).To(MatchError(gitreader.ErrInvalidRoot))
	})

	It("stops discovery at the resolved parent of a symlinked root", func() {
		parent := newRepo()
		head := parent.commit(map[string]string{"p.txt": "parent bytes\n"})
		child := filepath.Join(parent.dir, "half")
		Expect(os.MkdirAll(filepath.Join(child, ".git", "objects"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(child, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)).To(Succeed())
		// The approved root is a symlink outside the parent repository; git
		// resolves its working directory, so the ceiling must be the real
		// parent or discovery reaches the parent's objects.
		link := filepath.Join(GinkgoT().TempDir(), "link")
		Expect(os.Symlink(child, link)).To(Succeed())
		Expect(reader.ResolveRevision(ctx, link, head)).To(MatchError(gitreader.ErrInvalidRoot))
		stream, err := reader.ReadBlob(ctx, link, head, "p.txt")
		Expect(err).To(MatchError(gitreader.ErrObjectMissing), "the blob read never reaches the parent repository")
		Expect(stream).To(BeNil())
	})

	It("resolves a linked worktree through its common dir", func() {
		parent := newRepo()
		head := parent.commit(map[string]string{"f.txt": "x\n"})
		work := filepath.Join(GinkgoT().TempDir(), "wt")
		git(parent.dir, "worktree", "add", "--detach", work, head)
		// The worktree admin dir has no objects/ of its own, and git creates
		// its refs/ only on demand: both come from the commondir target.
		admin := strings.TrimSpace(strings.TrimPrefix(readFile(filepath.Join(work, ".git")), "gitdir:"))
		Expect(os.RemoveAll(filepath.Join(admin, "refs"))).To(Succeed())
		Expect(filepath.Join(admin, "objects")).NotTo(BeADirectory())
		Expect(reader.ResolveRevision(ctx, work, head)).To(Succeed())
	})

	It("accepts a bare repository root", func() {
		origin := newRepo()
		head := origin.commit(map[string]string{"f.txt": "x\n"})
		bare := filepath.Join(GinkgoT().TempDir(), "bare.git")
		git(origin.dir, "clone", "--bare", "--quiet", origin.dir, bare)
		Expect(reader.ResolveRevision(ctx, bare, head)).To(Succeed())
	})
})

func readFile(name string) string {
	raw, err := os.ReadFile(name)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return string(raw)
}
