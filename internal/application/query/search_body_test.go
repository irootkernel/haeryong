package query

import (
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("K2 classifies long literal words and authored components without treating chunk boundaries as word boundaries", Label("unit"), func() {
	for _, row := range []struct {
		literal, display, term, before, after string
		want                                  bool
	}{
		{strings.Repeat("a", 32768), strings.Repeat("a", 32768), "aa", "a", "a", false},
		{"aaaa aa", "aaaa aa", "aa", "", "", true},
		{"재재재 재", "재재재 재", "재", "", "", true},
		{"foo.bar foo.bar", "foo.bar foo.bar", "foo.bar", "", "", true},
		{"source", "source", "source", "read", "", false},
		{"readsource", "readSource", "source", "", "", true},
		{"readsource", "readSource", "source", "", "ful", false},
		{"httpsclient", "HTTPsClient", "client", "", "", true},
		{"café", "cafe\u0301", "café", "", "", true},
		{"strasse", "Straße", "strasse", "", "", true},
		{"", "readSource", "source", "", "", true},
	} {
		Expect(bodyWordContains(row.literal, row.display, "", row.term, row.before, row.after, true)).To(Equal(row.want), row)
	}
	Expect(bodyWordContains("readsource", "readSource", "source", "source", "", "", false)).To(BeTrue(), "legacy normalized postings still validate their nominated authored alias")
})

// The same total text and window geometry as SC20's 40 MiB single token,
// measuring classification alone rather than asserting host-dependent time.
func BenchmarkSearchLongToken(b *testing.B) {
	text := strings.Repeat("a", 32768)
	b.ReportAllocs()
	b.SetBytes(40 * 1024 * 1024)
	for n := 0; n < b.N; n++ {
		for window := 0; window < 40*1024*1024/len(text); window++ {
			if bodyWordContains(text, text, "", "aa", "a", "a", false) || bodyWordContains("", text, "", "aa", "a", "a", true) {
				b.Fatal("a substring of the long token is not a complete word")
			}
		}
	}
}
