package search

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SC01 identifier aliases", Label("unit"), func() {
	It("asserts exact component output before folding (reversal: GenerateIdentifierAliases boundary/emission)", func() {
		Expect(GenerateIdentifierAliases("readSource")).To(Equal([]string{"read", "source"}), "aliases_readSource_has_source")
		Expect(GenerateIdentifierAliases("HTTPServer")).To(Equal([]string{"http", "server"}))
		Expect(GenerateIdentifierAliases("source_access")).To(Equal([]string{"source", "access"}))
		Expect(GenerateIdentifierAliases("Cafe\u0301Source")).To(Equal([]string{"café", "source"}))
		Expect(GenerateIdentifierAliases("resourceful")).To(Equal([]string{"resourceful"}))
		Expect(Normalize("  Cafe\u0301 READSource ß ﬃ \t 검토  ")).To(Equal("café readsource ss ffi 검토"))
	})
})
