package source_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/source"
)

func sha(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

var _ = Describe("exact bounded bytes rule", Label("unit"), func() {
	lf := []byte("alpha\nbeta\ngamma\n")
	crlf := []byte("alpha\r\nbeta\r\ngamma\r\n")
	noFinal := []byte("alpha\nbeta\ngamma")
	empty := []byte{}
	blankLines := []byte("\n\n\n")

	It("hashes the whole file when no line numbers are supplied", func() {
		for _, blob := range [][]byte{lf, crlf, noFinal, empty, blankLines} {
			r, err := source.SelectBounded(bytes.NewReader(blob), 0, 0, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.SHA).To(Equal(sha(blob)))
			Expect(r.Bounded).To(Equal(int64(len(blob))))
		}
	})
	It("selects inclusive bounds including each terminator", func() {
		r, err := source.SelectBounded(bytes.NewReader(lf), 2, 3, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.SHA).To(Equal(sha([]byte("beta\ngamma\n"))))
		Expect(r.Bounded).To(Equal(int64(11)))
		Expect(r.Lines).To(Equal(int64(3)))
	})
	It("includes the terminator of a single selected line", func() {
		r, err := source.SelectBounded(bytes.NewReader(lf), 1, 1, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.SHA).To(Equal(sha([]byte("alpha\n"))))
	})
	It("preserves a missing final terminator as absent", func() {
		r, err := source.SelectBounded(bytes.NewReader(noFinal), 3, 3, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.SHA).To(Equal(sha([]byte("gamma"))))
		Expect(r.Bounded).To(Equal(int64(5)))
		Expect(r.Lines).To(Equal(int64(3)))
	})
	It("treats CR as content, never a terminator", func() {
		r, err := source.SelectBounded(bytes.NewReader(crlf), 1, 2, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.SHA).To(Equal(sha([]byte("alpha\r\nbeta\r\n"))))
		Expect(r.SHA).NotTo(Equal(sha([]byte("alpha\nbeta\n"))))
		Expect(r.Lines).To(Equal(int64(3)))
	})
	It("selects an empty line as exactly its terminator", func() {
		r, err := source.SelectBounded(bytes.NewReader(blankLines), 2, 2, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.SHA).To(Equal(sha([]byte("\n"))))
		Expect(r.Bounded).To(Equal(int64(1)))
	})
	It("rejects out-of-range bounds as a typed target fact", func() {
		_, err := source.SelectBounded(bytes.NewReader(lf), 4, 4, 0)
		Expect(err).To(MatchError(source.ErrLineRangeOutOfBounds))
		_, err = source.SelectBounded(bytes.NewReader(lf), 1, 4, 0)
		Expect(err).To(MatchError(source.ErrLineRangeOutOfBounds))
		_, err = source.SelectBounded(bytes.NewReader(empty), 1, 1, 0)
		Expect(err).To(MatchError(source.ErrLineRangeOutOfBounds))
	})
	It("distinguishes a range ending at EOF with and without a terminator", func() {
		withTerm, err := source.SelectBounded(bytes.NewReader(lf), 2, 3, 0)
		Expect(err).NotTo(HaveOccurred())
		without, err := source.SelectBounded(bytes.NewReader(noFinal), 2, 3, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(withTerm.SHA).NotTo(Equal(without.SHA))
	})
	It("compares digest and excerpt expectations exactly", func() {
		blob := []byte("package main\n\nfunc main() {}\n")
		r, err := source.SelectBounded(bytes.NewReader(blob), 3, 3, int64(len("func main() {}\n")))
		Expect(err).NotTo(HaveOccurred())
		Expect(source.ExcerptMatches(r, []byte("func main() {}\n"))).To(BeTrue())
		Expect(source.ExcerptMatches(r, []byte("func main() {}"))).To(BeFalse(), "trimmed excerpt must not match")
		Expect(source.ExcerptMatches(r, []byte("func main() {}\r\n"))).To(BeFalse(), "normalized excerpt must not match")
		long, err := source.SelectBounded(bytes.NewReader(blob), 3, 3, 64)
		Expect(err).NotTo(HaveOccurred())
		Expect(source.ExcerptMatches(long, []byte(strings.Repeat("x", 64)))).To(BeFalse(), "longer expectation matched")
	})
	It("never matches an empty expectation against non-empty bounded bytes", func() {
		r, err := source.SelectBounded(bytes.NewReader(lf), 1, 1, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Bounded).To(Equal(int64(6)))
		Expect(source.ExcerptMatches(r, []byte(""))).To(BeFalse(), "empty expectation must not match non-empty bytes")
		Expect(source.ExcerptMatches(BoundedEmpty(), []byte(""))).To(BeTrue(), "empty expectation matches empty bounded bytes")
	})

	It("hashes a large stream with constant line bookkeeping", func() {
		blob := bytes.Repeat([]byte("line of source text\n"), 50_000)
		r, err := source.SelectBounded(bytes.NewReader(blob), 2, 49_999, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Lines).To(Equal(int64(50_000)))
		Expect(r.Bounded).To(Equal(int64(20 * 49_998)))
	})
})

func BoundedEmpty() source.BoundedResult {
	return source.BoundedResult{SHA: sha(nil), Bounded: 0, Buffered: nil}
}
