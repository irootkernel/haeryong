package query_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SC08 search foundation capability", Label("persistence"), func() {
	It("enables search with backend, CLI, three transports and Library UI delivery (reversal: Capabilities search flag)", func() {
		f := newFixture(GinkgoT())
		capabilities, err := f.queries.Capabilities(ctx, f.token)
		Expect(err).NotTo(HaveOccurred())
		features := capabilities["features"].(map[string]bool)
		value, present := features["search"]
		Expect(present).To(BeTrue())
		Expect(value).To(BeTrue())
		Expect(features["source_verification"]).To(BeTrue())
	})
})
