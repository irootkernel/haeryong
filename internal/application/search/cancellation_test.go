package search

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"io"
)

type cancellingToken struct {
	cancel context.CancelFunc
	bytes  int
}

func (r *cancellingToken) Read(p []byte) (int, error) {
	if r.bytes >= 16<<20 {
		return 0, io.EOF
	}
	n := min(len(p), (16<<20)-r.bytes)
	for i := range p[:n] {
		p[i] = 'a'
	}
	r.bytes += n
	if r.bytes >= 8192 {
		r.cancel()
	}
	return n, nil
}

var _ = It("G6 observes cancellation inside one unlimited HTML token, before Next returns (reversal: cancellableInput context check)", Label("unit"), func() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := &cancellingToken{cancel: cancel}
	result, err := Extract(ctx, input, "text/html", &textSpool{}, func(Fragment) error { Fail("cancelled token emitted a fragment"); return nil })
	Expect(err).To(MatchError(context.Canceled))
	Expect(result.Complete).To(BeFalse())
	Expect(input.bytes).To(BeNumerically("<=", 12288))
})
