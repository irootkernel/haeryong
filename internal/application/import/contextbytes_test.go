package importer

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type chunked struct {
	chunks [][]byte
	at     int
}

func (c *chunked) Read(out []byte) (int, error) {
	if c.at >= len(c.chunks) {
		return 0, io.EOF
	}
	n := copy(out, c.chunks[c.at])
	if n == len(c.chunks[c.at]) {
		c.at++
		return n, nil
	}
	c.chunks[c.at] = c.chunks[c.at][n:]
	return n, nil
}

func splits(input []byte) [][][]byte {
	var out [][][]byte
	for _, size := range []int{1, 2, 3, 5, 7, 13} {
		if size >= len(input) {
			continue
		}
		var chunks [][]byte
		for i := 0; i < len(input); i += size {
			end := i + size
			if end > len(input) {
				end = len(input)
			}
			chunks = append(chunks, input[i:end])
		}
		out = append(out, chunks)
	}
	if len(input) > 0 && len(input) <= 96 {
		for i := 1; i < len(input); i++ {
			out = append(out, [][]byte{input[:i], input[i:]})
		}
	}
	out = append(out, [][]byte{input})
	return out
}

type corpusCase struct {
	name      string
	input     string
	noOracle  bool // contract divergence from encoding/json (UTF-8/BOM strictness)
	detectErr bool // invalid UTF-8 fails detection outright regardless of media
}

var corpus = []corpusCase{
	{"empty", "", false, false},
	{"whitespace", "   \t\r\n ", false, false},
	{"bare-int", "42", false, false},
	{"bare-neg-fraction-exp", "-0.5e2", false, false},
	{"bare-exp", "1e3", false, false},
	{"bare-true", "true", false, false},
	{"bare-null", "null", false, false},
	{"bare-string", "\"s\"", false, false},
	{"string-escapes", "\"a\\\"\\\\\\/\\b\\f\\n\\r\\t\\u0041\\ud800\"", false, false},
	{"object", `{"a":1,"b":[true,null,"x"],"c":{"d":-2.5E-10}}`, false, false},
	{"empty-object", "{}", false, false},
	{"empty-array", "[]", false, false},
	{"nested-ws", " [ 1 , { \"k\" : [ ] } ] ", false, false},
	{"unicode-keys", `{"키":"값","🌊":"spawn"}`, false, false},
	{"leading-minus-dot", "[-.5]", false, false},
	{"bare-minus", "-", false, false},
	{"array-bare-minus", "[-]", false, false},
	{"object-bare-minus", `{"a":-}`, false, false},
	{"minus-trailing-space", "- ", false, false},
	{"array-minus-space", "[- ]", false, false},
	{"object-minus-space", `{"a":- }`, false, false},
	{"leading-minus-e", "[-e5]", false, false},
	{"double-sign", "[--5]", false, false},
	{"exp-double-sign", "[1e+-5]", false, false},
	{"leading-zero", "01", false, false},
	{"dot-no-digits", "[1.]", false, false},
	{"frac-empty", "[1.e5]", false, false},
	{"exp-no-digits", "[1e]", false, false},
	{"plus-int", "[+1]", false, false},
	{"trailing-object-comma", `{"a":1,}`, false, false},
	{"leading-object-comma", "{,}", false, false},
	{"missing-colon", `{"a" 1}`, false, false},
	{"trailing-array-comma", "[1,]", false, false},
	{"leading-array-comma", "[,1]", false, false},
	{"two-top-values", "1 2", false, false},
	{"two-top-strings", `"a" "b"`, false, false},
	{"trailing-garbage", `{}x`, false, false},
	{"truncated-prefix-object", `{"a":`, false, false},
	{"truncated-prefix-array", `[1,2`, false, false},
	{"truncated-literal", "tru", false, false},
	{"truncated-string", "\"abc", false, false},
	{"unterminated-escape", `"a\`, false, false},
	{"bad-escape", `"a\q"`, false, false},
	{"bad-unicode-escape", `"a\uZZZZ"`, false, false},
	{"control-in-string", "\"a\x01b\"", false, false},
	{"bare-colon", ":", false, false},
	{"bare-comma", ",", false, false},
	{"brace-mismatch", "[}", false, false},
	{"bracket-mismatch", "{]", false, false},
	{"object-in-array-comma", `[{"a":1},]`, false, false},
	{"key-nonstring", `{1:2}`, false, false},
	{"bom", "\xef\xbb\xbf{}", true, false},
	{"invalid-utf8", "\"a\xffb\"", true, true},
	{"invalid-utf8-tail", "\"\xe2\x82\"", true, true},
}

func cloneChunks(chunks [][]byte) [][]byte {
	out := make([][]byte, len(chunks))
	for i, chunk := range chunks {
		out[i] = append([]byte(nil), chunk...)
	}
	return out
}

func scanChunks(chunks [][]byte) error {
	reader := validatedContextReader(&chunked{chunks: cloneChunks(chunks)}, "application/json", nil)
	_, err := io.Copy(io.Discard, reader)
	return err
}

// Differential corpus against encoding/json's validator under every
// deterministic split, asserting both accept/reject parity (except explicit
// contract divergences) and split-invariant media detection.
var _ = It("TestContextJSONScannerMatchesJSONValidAcrossSplits", Label("unit"), func() {
	for _, item := range corpus {
		input := []byte(item.input)
		want := json.Valid(input)
		if item.noOracle {
			want = false
		}
		for _, chunks := range splits(input) {
			err := scanChunks(chunks)
			Expect((err == nil) == want).To(BeTrue(), func() string {
				return item.name + " accept=" + boolText(err == nil) + " want=" + boolText(want) + " err=" + errText(err)
			})
			media, mediaErr := detectContextMedia(&chunked{chunks: cloneChunks(chunks)})
			if item.detectErr {
				Expect(mediaErr != nil).To(BeTrue(), func() string { return item.name + " detection should fail on invalid UTF-8" })
			} else {
				Expect(mediaErr != nil).To(BeFalse(), func() string { return item.name + " detect failed: " + errText(mediaErr) })
				wantMedia := "text/plain"
				if want {
					wantMedia = "application/json"
				}
				Expect(media != wantMedia).To(BeFalse(), func() string { return item.name + " media=" + media + " want=" + wantMedia })
			}
		}
	}
})

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

var _ = It("TestContextJSONScannerEOFMatrix", Label("unit"), func() {
	for _, item := range []string{"42", "true", "\"s\"", "-0.5e2", "1e3", "0", "-0"} {
		Expect(scanChunks([][]byte{[]byte(item)})).To(BeNil(), func() string { return "EOF scalar " + item + " rejected" })
		media, err := detectContextMedia(strings.NewReader(item))
		Expect(err != nil || media != "application/json").To(BeFalse(), func() string { return "EOF scalar " + item + " media=" + media })
	}
})

// encoding/json caps nesting depth at 10000; the context contract imposes no
// author-side depth quota, so this case asserts our scanner only.
var _ = It("TestContextJSONScannerDeepNestingWithoutAuthorQuota", Label("unit"), func() {
	deep := strings.Repeat("[", 20000) + "1" + strings.Repeat("]", 20000)
	Expect(scanChunks([][]byte{[]byte(deep)})).To(BeNil(), func() string { return "deep nesting rejected" })
	media, err := detectContextMedia(strings.NewReader(deep))
	Expect(err != nil || media != "application/json").To(BeFalse(), func() string { return "deep nesting media=" + media })
})

var _ = It("TestContextJSONScannerLargeInput", Label("unit"), func() {
	var builder strings.Builder
	builder.WriteString(`{"rows":[`)
	for i := 0; i < 20000; i++ {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(`{"id":` + strconv.Itoa(i) + `,"name":"유니코드-행","score":-0.5e2}`)
	}
	builder.WriteString(`],"done":true}`)
	large := []byte(builder.String())
	Expect(json.Valid(large)).To(BeTrue(), func() string { return "oracle rejects generated large input" })
	for _, chunks := range [][][]byte{{large}, splitAt(large, len(large)/2), splitAt(large, len(large)/3)} {
		Expect(scanChunks(chunks)).To(BeNil(), func() string { return "large input rejected" })
	}
	// A truncated variant must fail under a declared JSON media type and
	// normalize to text under detection (never application/json).
	broken := large[:len(large)-10]
	Expect(scanChunks([][]byte{broken})).NotTo(BeNil(), func() string { return "truncated large input accepted" })
	media, err := detectContextMedia(bytes.NewReader(broken))
	Expect(err != nil || media != "text/plain").To(BeFalse(), func() string { return "truncated large input media=" + media })
})

func splitAt(input []byte, at int) [][]byte {
	return [][]byte{append([]byte(nil), input[:at]...), append([]byte(nil), input[at:]...)}
}
