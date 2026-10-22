package search

import (
	"context"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"strings"
	"unicode/utf8"
)

var _ = Describe("SC02 normalized chunk diagnostic", Label("unit"), func() {
	It("retains both expanding maximal phrases at the exact overlap boundary (reversal: ChunkOverlap=4094 or final emit)", func() {
		for _, raw := range []string{"a" + strings.Repeat("ß", 2047) + "z", "b" + strings.Repeat("ﬃ", 1364) + "xyz"} {
			q, err := ParseQuery(`"` + raw + `"`)
			Expect(err).NotTo(HaveOccurred())
			literal := q.Clauses[0].Literal
			Expect(utf8.RuneCountInString(literal)).To(Equal(4096))
			chunks, err := ChunkText(context.Background(), strings.Repeat("q", 28673)+raw)
			Expect(err).NotTo(HaveOccurred())
			Expect(chunks).To(HaveLen(2))
			Expect(strings.Contains(chunks[0].Text, literal)).To(BeFalse())
			Expect(chunks[1].Text).To(Equal(literal))
		}
	})
	It("normalizes across reader buffers, omits no tail and propagates sink/cancellation failures (reversal: Chunks reader/error/final window)", func() {
		chunks, err := ChunkText(context.Background(), strings.Repeat("a", 32767)+"e\u0301 tail")
		Expect(err).NotTo(HaveOccurred())
		Expect(chunks).To(HaveLen(2))
		Expect(chunks[1].Text).To(HaveSuffix("é tail"))
		marker := errors.New("sink failed")
		Expect(Chunks(context.Background(), strings.NewReader("tail"), func(Chunk) error { return marker })).To(MatchError(marker))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = ChunkText(ctx, "x")
		Expect(err).To(MatchError(context.Canceled))
	})
})
