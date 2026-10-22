package search

import (
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"strings"
)

var _ = Describe("SC08 literal grammar", Label("unit"), func() {
	It("preserves literal punctuation and doubled quotes (reversal: ParseQuery quote/state branches)", func() {
		q, err := ParseQuery(`Source "검토 승인" "say ""yes""" OR NEAR * - : /`)
		Expect(err).NotTo(HaveOccurred())
		Expect(q.Clauses).To(Equal([]Clause{{"source", false}, {"검토 승인", true}, {`say "yes"`, true}, {"or", false}, {"near", false}, {"*", false}, {"-", false}, {":", false}, {"/", false}}))
		for _, s := range []string{"", " \t\n ", `"  "`} {
			q, err := ParseQuery(s)
			Expect(err).NotTo(HaveOccurred())
			Expect(q.Clauses).To(BeEmpty())
		}
	})
	It("rejects syntax, controls and both length domains without truncation (reversal: ParseQuery normalized/raw/control guards)", func() {
		for _, row := range []struct{ s, reason string }{{string([]byte{0xff}), "invalid_query"}, {"a\x00b", "invalid_query"}, {"a\x01b", "invalid_query"}, {`"open`, "malformed_phrase"}, {`a"b`, "malformed_phrase"}, {`"a"b`, "malformed_phrase"}, {strings.Repeat("a", 4097), "query_too_long"}, {`"a` + strings.Repeat("ß", 3000) + `z"`, "query_too_long"}} {
			_, err := ParseQuery(row.s)
			var qe *QueryError
			Expect(errors.As(err, &qe)).To(BeTrue())
			Expect(qe.Reason).To(Equal(row.reason))
		}
		q, err := ParseQuery(strings.Repeat("x", 4096))
		Expect(err).NotTo(HaveOccurred())
		Expect(q.Normalized).To(HaveLen(4096))
	})
})
