package importer_test

import (
	"encoding/base64"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	importer "haeryong.local/haeryong/internal/application/import"
	sec "haeryong.local/haeryong/internal/security/session"
)

func encodeBindingHeader(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

var _ = Describe("Context binding header parsing", Label("unit"), func() {
	version := "33333333-3333-4333-8333-333333333333"
	render := "44444444-4444-4444-8444-444444444444"

	It("accepts the closed three-key form with explicit null members", func() {
		q, err := importer.ParseContextBinding(encodeBindingHeader(`{"version_id":"` + version + `","page_path":null,"render_instance_id":` + renderQuote(render) + `}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(q.VersionID).To(Equal(version))
		Expect(q.PagePath).To(BeNil())
		Expect(q.RenderInstanceID).NotTo(BeNil())
		Expect(*q.RenderInstanceID).To(Equal(render))
	})

	It("decodes escaped Unicode once with no re-normalization", func() {
		raw, qerr := importer.ParseContextBinding(encodeBindingHeader(`{"version_id":"` + version + `","page_path":"pages/한글.html","render_instance_id":null}`))
		escaped, eerr := importer.ParseContextBinding(encodeBindingHeader(`{"version_id":"` + version + `","page_path":"pages/\ud55c\uae00.html","render_instance_id":null}`))
		Expect(qerr).NotTo(HaveOccurred())
		Expect(eerr).NotTo(HaveOccurred())
		Expect(escaped.PagePath).NotTo(BeNil())
		Expect(*escaped.PagePath).To(Equal("pages/한글.html"))
		Expect(raw).To(Equal(escaped))
	})

	It("keeps the literal string null distinct from JSON null", func() {
		q, err := importer.ParseContextBinding(encodeBindingHeader(`{"version_id":"` + version + `","page_path":"null","render_instance_id":null}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(q.PagePath).NotTo(BeNil())
		Expect(*q.PagePath).To(Equal("null"))
	})

	It("rejects omitted, unknown, duplicate and mistyped members", func() {
		for _, value := range []string{
			`{"page_path":null,"render_instance_id":null}`,               // version_id omitted
			`{"version_id":"` + version + `","render_instance_id":null}`, // page_path omitted
			`{"version_id":"` + version + `","page_path":null}`,          // render_instance_id omitted
			`{"version_id":"` + version + `","page_path":null,"render_instance_id":null,"extra":1}`,
			`{"version_id":"` + version + `","version_id":"` + version + `","page_path":null,"render_instance_id":null}`,
			`{"version_id":null,"page_path":null,"render_instance_id":null}`,
			`{"version_id":"` + version + `","page_path":7,"render_instance_id":null}`,
			`{"version_id":"` + version + `","page_path":null,"render_instance_id":"not-a-uuid"}`,
			`{"version_id":"short","page_path":null,"render_instance_id":null}`,
			`{"version_id":"` + version + `","page_path":"../escape","render_instance_id":null}`,
			`{"version_id":"` + version + `","page_path":"","render_instance_id":null}`,
			`{}`,
			`[]`,
			`null`,
		} {
			_, err := importer.ParseContextBinding(encodeBindingHeader(value))
			Expect(err).To(MatchError(sec.ErrInvalid), "accepted invalid binding JSON: %s", value)
		}
	})

	It("rejects non-UTF-8, malformed and padded base64url", func() {
		for _, value := range []string{
			"",
			"!!!!",
			base64.RawURLEncoding.EncodeToString([]byte{0xff, '{', '}'}),
			encodeBindingHeader(`{"version_id":"`+version+`","page_path":null,"render_instance_id":null}`) + "=",
		} {
			_, err := importer.ParseContextBinding(value)
			Expect(err).To(MatchError(sec.ErrInvalid), "accepted invalid base64url: %q", value)
		}
	})

	It("rejects embedded CR/LF that the decoder would otherwise skip", func() {
		encoded := encodeBindingHeader(`{"version_id":"` + version + `","page_path":null,"render_instance_id":null}`)
		crlf := encoded[:len(encoded)/2] + "\r\n" + encoded[len(encoded)/2:]
		// Go's base64 decoder skips the whitespace on its own; the normative
		// parser must still reject the non-canonical value unconditionally.
		decoded, err := base64.RawURLEncoding.DecodeString(crlf)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded).To(Equal([]byte(`{"version_id":"` + version + `","page_path":null,"render_instance_id":null}`)))
		_, err = importer.ParseContextBinding(crlf)
		Expect(err).To(MatchError(sec.ErrInvalid), "accepted CR/LF inside the binding header")
	})

	It("rejects non-canonical trailing bits in an otherwise valid binding", func() {
		// A valid three-key binding whose JSON length is not a multiple of
		// three ends in a final group with don't-care bits. The canonical
		// form parses successfully first, so the rejection below proves the
		// trailing-bit rule rather than an unrelated defect.
		jsonText := `{"version_id":"` + version + `","page_path":"pg.html","render_instance_id":null}`
		Expect(len(jsonText) % 3).NotTo(Equal(0))
		encoded := encodeBindingHeader(jsonText)
		canonical, err := importer.ParseContextBinding(encoded)
		Expect(err).NotTo(HaveOccurred())
		Expect(canonical.VersionID).To(Equal(version))
		variant := nonCanonicalVariant(encoded)
		Expect(variant).NotTo(BeEmpty())
		Expect(variant).NotTo(Equal(encoded))
		decoded, err := base64.RawURLEncoding.DecodeString(variant)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded).To(Equal([]byte(jsonText)), "variant changed the decoded binding JSON")
		Expect(base64.RawURLEncoding.EncodeToString(decoded)).NotTo(Equal(variant))
		_, err = importer.ParseContextBinding(variant)
		Expect(err).To(MatchError(sec.ErrInvalid), "accepted non-canonical trailing bits")
	})
})

// nonCanonicalVariant returns a string that decodes to the same bytes as the
// canonical encoding but differs textually (non-zero trailing don't-care
// bits), or "" when the final group has no don't-care bits.
func nonCanonicalVariant(encoded string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	if len(encoded)%4 == 0 || encoded == "" {
		return ""
	}
	last := encoded[len(encoded)-1]
	idx := strings.IndexByte(alphabet, last)
	if idx < 0 || idx^1 >= len(alphabet) {
		return ""
	}
	sibling := alphabet[idx^1]
	if byte(sibling) == last {
		return ""
	}
	return encoded[:len(encoded)-1] + string(sibling)
}

func renderQuote(value string) string {
	return `"` + value + `"`
}
