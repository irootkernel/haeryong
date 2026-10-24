package gitreader_test

import (
	"context"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/gitreader"
)

func commandReader(script string) *gitreader.Reader {
	binary := filepath.Join(GinkgoT().TempDir(), "git-probe")
	ExpectWithOffset(1, os.WriteFile(binary, []byte("#!/bin/sh\n"+script), 0o700)).To(Succeed())
	return &gitreader.Reader{Binary: binary}
}

var _ = Describe("git command termination", Label("integration"), func() {
	It("keeps signal termination distinct from missing targets", func() {
		r := newRepo()
		revision := r.commit(map[string]string{"f.txt": "fixture\n"})
		reader := commandReader("kill -KILL \"$$\"\n")
		ctx := context.Background()
		head, err := reader.ResolveHead(ctx, r.dir)
		Expect(err).To(MatchError(gitreader.ErrGitUnavailable))
		Expect(head).To(BeEmpty())
		Expect(reader.ResolveRevision(ctx, r.dir, revision)).To(MatchError(gitreader.ErrGitUnavailable))
		stream, err := reader.ReadBlob(ctx, r.dir, revision, "f.txt")
		Expect(err).To(MatchError(gitreader.ErrGitUnavailable))
		Expect(stream).To(BeNil())
		Expect(ctx.Err()).NotTo(HaveOccurred(), "the process ended without context cancellation")
	})

	DescribeTable("classifies the blob process when closing its stream", func(termination string, cancelled bool, expected error) {
		r := newRepo()
		revision := r.commit(map[string]string{"f.txt": "fixture\n"})
		// The pre-check uses real git; only the subsequent blob process fails.
		reader := commandReader("if [ \"$2\" = \"-e\" ]; then exec git \"$@\"; fi\n" + termination + "\n")
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		stream, err := reader.ReadBlob(ctx, r.dir, revision, "f.txt")
		Expect(err).NotTo(HaveOccurred())
		_, err = io.ReadAll(stream)
		Expect(err).NotTo(HaveOccurred())
		if cancelled {
			cancel()
		}
		Expect(stream.Close()).To(MatchError(expected))
	},
		Entry("a signal is infrastructure failure", "kill -KILL \"$$\"", false, gitreader.ErrGitUnavailable),
		Entry("a normal failure remains an early-ended stream", "exit 1", false, io.ErrUnexpectedEOF),
		Entry("cancellation takes precedence over the process outcome", "kill -KILL \"$$\"", true, context.Canceled),
	)

	It("preserves cancellation for target resolution and the blob pre-check", func() {
		r := newRepo()
		revision := r.commit(map[string]string{"f.txt": "fixture\n"})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		reader := gitreader.New()
		_, err := reader.ResolveHead(ctx, r.dir)
		Expect(err).To(MatchError(context.Canceled))
		Expect(reader.ResolveRevision(ctx, r.dir, revision)).To(MatchError(context.Canceled))
		stream, err := reader.ReadBlob(ctx, r.dir, revision, "f.txt")
		Expect(err).To(MatchError(context.Canceled))
		Expect(stream).To(BeNil())
	})
})
