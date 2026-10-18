package render_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode/utf16"

	"golang.org/x/net/html"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/render"
)

type observedSource struct {
	io.ReadSeeker
	read, largest int
}

func (r *observedSource) Read(value []byte) (int, error) {
	r.largest = max(r.largest, len(value))
	n, err := r.ReadSeeker.Read(value)
	r.read += n
	return n, err
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

var _ = It("serves a tail range from an already verified large HTML handle without copying the whole page", Label("unit"), func() {
	page := "<!DOCTYPE html><html lang='ko'><head id='authored'><title>large</title></head><body>" + strings.Repeat("large authored data\n", 1<<20) + "</body></html>"
	source := &observedSource{ReadSeeker: strings.NewReader(page)}
	view, err := render.LoaderPage(source, int64(len(page)), digest(page), "version", "document", "pages/large.html")
	Expect(err).NotTo(HaveOccurred())
	Expect(source.read).To(BeNumerically("<=", 8192))
	Expect(source.largest).To(BeNumerically("<=", 4096))
	_, err = view.Body.Seek(-64, io.SeekEnd)
	Expect(err).NotTo(HaveOccurred())
	tail, err := io.ReadAll(view.Body)
	Expect(err).NotTo(HaveOccurred())
	Expect(string(tail)).To(Equal(page[len(page)-64:]))
	Expect(source.read).To(BeNumerically("<=", 8192+64))
})

var _ = It("preserves authored head attributes and exact ranges across the loader splice", Label("unit"), func() {
	for _, prefix := range []string{
		"<!DOCTYPE html><html lang='ko'><HEAD id='authored' data-x='>'>",
		"\xef\xbb\xbf<!--leading--!><!doctype html><html><head id='authored'>",
		"<!--> <!doctype html><html><head id='authored'>",
	} {
		page := prefix + "<title>한글</title></head><body>unchanged</body></html>"
		view, err := render.LoaderPage(strings.NewReader(page), int64(len(page)), digest(page), "version", "document", "pages/a&b\".html")
		Expect(err).NotTo(HaveOccurred())
		whole, err := io.ReadAll(view.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(whole)).To(HavePrefix(prefix + "<script "))
		Expect(string(whole)).To(ContainSubstring(`data-haeryong-page="pages%2Fa&amp;b%22.html"`))
		Expect(string(whole)).To(HaveSuffix(page[len(prefix):]))
		Expect(view.ETag).NotTo(BeEmpty())
		for _, start := range []int{0, len(prefix) - 2, len(prefix) + 10, len(whole) - 25} {
			_, err = view.Body.Seek(int64(start), io.SeekStart)
			Expect(err).NotTo(HaveOccurred())
			actual, err := io.ReadAll(io.LimitReader(view.Body, 20))
			Expect(err).NotTo(HaveOccurred())
			Expect(actual).To(Equal(whole[start : start+20]))
		}
		other, err := render.LoaderPage(strings.NewReader(page), int64(len(page)), digest(page), "version", "document", "different.html")
		Expect(err).NotTo(HaveOccurred())
		Expect(other.ETag).NotTo(Equal(view.ETag))
		_, err = other.Body.Seek(0, io.SeekStart)
		Expect(err).NotTo(HaveOccurred())
	}
})

// C2 regression: the integration tag's page identity round-trips exactly
// through a REAL HTML parser even when the page path carries CR/CRLF/LF/TAB,
// Unicode and reserved characters that the parser would otherwise normalize.
var _ = It("TestLoaderPageIdentityRoundTripsThroughRealHTMLParser", Label("unit"), func() {
	page := "dir/pa\tge\rge\nna\u00eeme \"q\"<&>.html"
	body := "<!doctype html><html><head><title>t</title></head><body>ok</body></html>"
	source := strings.NewReader(body)
	view, err := render.LoaderPage(source, int64(len(body)), hex64("a"), "rv1", "doc", page)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	Expect(view.ETag != "").To(BeTrue())
	derivative, err := io.ReadAll(view.Body)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	parsed, err := html.Parse(bytes.NewReader(derivative))
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	var found string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "script" {
			for _, attr := range n.Attr {
				if attr.Key == "data-haeryong-page" {
					found = attr.Val
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(parsed)
	// The automatic loader reverses the URI encoding with exactly one
	// decodeURIComponent call; PathUnescape is its Go twin (no plus-to-space).
	decoded, err := url.PathUnescape(found)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	Expect(decoded != page).To(BeFalse(), func() string { return fmt.Sprintf("parser normalized page identity: %q != %q", decoded, page) })
})

func hex64(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

// A UTF-16 document truncated exactly after "<html"/"<head" (EOF follower)
// must classify as ordinary without panicking, and malformed odd tails take
// the honest raw fallback with a byte-exact representation.
var _ = It("TestUTF16TruncatedPrefixAndOddTailNeverPanic", Label("unit"), func() {
	encode := func(text string, little bool) []byte {
		out := make([]byte, 0, len(text)*2+2)
		if little {
			out = append(out, 0xFF, 0xFE)
		} else {
			out = append(out, 0xFE, 0xFF)
		}
		for _, r := range text {
			if little {
				out = append(out, byte(r), 0)
			} else {
				out = append(out, 0, byte(r))
			}
		}
		return out
	}
	cases := []string{"<html", "<head", "", "<html>", "<head>", "<head></head><body>표</body>"}
	for _, little := range []bool{true, false} {
		for _, text := range cases {
			page := encode(text, little)
			view, err := render.LoaderPage(bytes.NewReader(page), int64(len(page)), hex64("u16"), "v", "d", "u16.html")
			Expect(err).NotTo(HaveOccurred(), func() string { return fmt.Sprintf("little=%v text=%q: %v", little, text, err) })
			_, readErr := io.ReadAll(view.Body)
			Expect(readErr).NotTo(HaveOccurred(), func() string { return fmt.Sprintf("little=%v text=%q read: %v", little, text, readErr) })
		}
		// Odd trailing unit: honest raw fallback, original bytes intact.
		odd := append(encode("<head></head>", little), 0x41)
		view, err := render.LoaderPage(bytes.NewReader(odd), int64(len(odd)), hex64("odd"), "v", "d", "odd.html")
		Expect(err).NotTo(HaveOccurred())
		Expect(view.Integrate).To(BeFalse(), "odd trailing unit must fall back to raw bytes")
		raw, readErr := io.ReadAll(view.Body)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(raw).To(Equal(odd), "raw fallback must preserve original odd-tailed bytes")
	}
})

// C4 encoding matrix: label determination follows WHATWG provenance exactly.
var _ = It("TestEncodingProvenanceMatrix", Label("unit"), func() {
	body := func(decl string) string {
		return "<!doctype html><html><head>" + decl + "<title>legacy</title></head><body>caf\xe9</body></html>"
	}
	cases := []struct {
		name string
		page string
		mime string
	}{
		{"meta charset windows-1252", body(`<meta charset="windows-1252">`), "text/html; charset=windows-1252"},
		{"self-closing meta", body("<meta charset='windows-1252'/>"), "text/html; charset=windows-1252"},
		{"spaced pragma", body("<meta http-equiv='content-type' content=\"text/html; CHARSET = 'windows-1252'; x=1\">"), "text/html; charset=windows-1252"},
		{"meta charset euc-kr", body(`<meta charset="euc-kr">`), "text/html; charset=euc-kr"},
		{"meta x-user-defined normalized to windows-1252", body(`<meta charset="x-user-defined">`), "text/html; charset=windows-1252"},
		{"meta utf-16le normalized to utf-8", body(`<meta charset="utf-16le">`), "text/html; charset=utf-8"},
		{"meta utf-16be normalized to utf-8", body(`<meta charset="utf-16be">`), "text/html; charset=utf-8"},
		{"pragma http-equiv content-type", body(`<meta http-equiv="Content-Type" content="text/html; charset=Shift_JIS">`), "text/html; charset=shift_jis"},
		{"first duplicate attribute wins", body(`<meta charset="euc-kr" charset="utf-16le">`), "text/html; charset=euc-kr"},
		{"invalid candidate continues to valid declaration", body(`<meta charset="not-a-real-encoding"><meta charset="gbk">`), "text/html; charset=gbk"},
		{"no declaration keeps product utf-8 default", body(""), "text/html; charset=utf-8"},
		{"late declaration does not change the 1024-byte prescan", "<!doctype html><head><!--" + strings.Repeat("x", 1024) + "--><meta charset=windows-1252></head><body>한글</body>", "text/html; charset=utf-8"},
		{"complete declaration at prescan boundary", strings.Repeat(" ", 1024-len(`<meta charset=windows-1252>`)) + `<meta charset=windows-1252>`, "text/html; charset=windows-1252"},
		{"truncated declaration at prescan boundary", strings.Repeat(" ", 1024-len(`<meta charset=windows-1252`)) + `<meta charset=windows-1252>`, "text/html; charset=utf-8"},
	}
	for _, item := range cases {
		view, err := render.LoaderPage(strings.NewReader(item.page), int64(len(item.page)), hex64("enc"), "v", "d", "enc.html")
		Expect(err).NotTo(HaveOccurred(), func() string { return item.name + ": " + fmt.Sprint(err) })
		Expect(view.MIME).To(Equal(item.mime), func() string { return item.name + " MIME: " + view.MIME })
		Expect(view.Integrate).To(BeTrue(), func() string { return item.name + " ASCII-compatible must integrate" })
	}
	// Stateful families.
	iso := body("<meta charset=\"iso-2022-jp\">")
	view, err := render.LoaderPage(strings.NewReader(iso), int64(len(iso)), hex64("iso"), "v", "d", "iso.html")
	Expect(err).NotTo(HaveOccurred())
	Expect(view.MIME).To(Equal("text/html; charset=iso-2022-jp"))
	// hz-gb-2312 maps to replacement per WHATWG; the label is unsupported
	// and the scan correctly continues to the utf-8 default.
	hz := body(`<meta charset="hz-gb-2312">`)
	view, err = render.LoaderPage(strings.NewReader(hz), int64(len(hz)), hex64("hz"), "v", "d", "hz.html")
	Expect(err).NotTo(HaveOccurred())
	Expect(view.MIME).To(Equal("text/html; charset=utf-8"), "unsupported hz-gb-2312 falls to the utf-8 default")
	// BOM provenance wins over meta.
	bom := "\xef\xbb\xbf" + body(`<meta charset="euc-kr">`)
	view, err = render.LoaderPage(strings.NewReader(bom), int64(len(bom)), hex64("bom"), "v", "d", "bom.html")
	Expect(err).NotTo(HaveOccurred())
	Expect(view.MIME).To(Equal("text/html; charset=utf-8"), "UTF-8 BOM wins over meta")
})

// C4 UTF-16: both endiannesses carry the early loader after the opening
// head, preserve doctype and serve exact ranges across the splice.
var _ = It("TestUTF16BothEndiannessesDoctypeLoaderAndRanges", Label("unit"), func() {
	for _, little := range []bool{true, false} {
		text := "<!doctype html><html><head id='authored'><title>제목😀</title></head><body>본문</body></html>"
		page := make([]byte, 0, len(text)*2+2)
		if little {
			page = append(page, 0xFF, 0xFE)
		} else {
			page = append(page, 0xFE, 0xFF)
		}
		for _, r := range utf16.Encode([]rune(text)) {
			if little {
				page = append(page, byte(r), byte(r>>8))
			} else {
				page = append(page, byte(r>>8), byte(r))
			}
		}
		view, err := render.LoaderPage(bytes.NewReader(page), int64(len(page)), hex64("u16"), "v", "d", "u16.html")
		Expect(err).NotTo(HaveOccurred())
		Expect(view.Integrate).To(BeTrue(), func() string { return fmt.Sprintf("little=%v", little) })
		Expect(view.MIME).To(Equal(func() string {
			if little {
				return "text/html; charset=utf-16le"
			}
			return "text/html; charset=utf-16be"
		}()))
		whole, readErr := io.ReadAll(view.Body)
		Expect(readErr).NotTo(HaveOccurred())
		// Remove only the injected UTF-16 tag and recover every source byte,
		// including the surrogate pair and original BOM/doctype prefix.
		encodeASCII := func(s string) []byte {
			var out []byte
			for _, b := range []byte(s) {
				if little {
					out = append(out, b, 0)
				} else {
					out = append(out, 0, b)
				}
			}
			return out
		}
		at := bytes.Index(whole, encodeASCII("<script src="))
		Expect(at).To(BeNumerically(">", 2))
		end := at + bytes.Index(whole[at:], encodeASCII("</script>")) + len(encodeASCII("</script>"))
		Expect(append(append([]byte{}, whole[:at]...), whole[end:]...)).To(Equal(page))
		Expect(len(whole)).To(BeNumerically(">", len(page)))
		for _, start := range []int64{0, 4, int64(len(whole)) / 2, int64(len(whole)) - 8} {
			_, err = view.Body.Seek(start, io.SeekStart)
			Expect(err).NotTo(HaveOccurred())
			chunk, chunkErr := io.ReadAll(io.LimitReader(view.Body, 8))
			Expect(chunkErr).NotTo(HaveOccurred())
			Expect(chunk).To(Equal(whole[start : start+8]))
		}
	}
})

// Valid fixtures use actual ISO-2022-JP bytes, not UTF-8 text in JIS mode.
var _ = It("TestISO2022IntegrationMatrix", Label("unit"), func() {
	const meta = "<meta charset='iso-2022-jp'>"
	const japanese = "\x1b$BF|K\\\x1b(B" // 日本
	const katakana = "\x1b(I1\x1b(B"     // ｱ
	cases := []struct{ name, before, after string }{
		{"roman-before-doctype", "\x1b(J<!doctype html><html><head data-yen='\\~'>", meta + "</head><body>\\~</body>"},
		{"jis-comment-before-doctype", "<!--" + japanese + "--><!doctype html><head>", meta + "</head>"},
		{"katakana-comment", "<!--" + katakana + "--><!doctype html><head>", meta + "</head>"},
		{"jis-punctuation-in-comment", "<!--\x1b$B-->!\x1b(B--><!doctype html><head>", meta + "</head>"},
		{"leading-jis", "", japanese + meta + "<script>authorFirst()</script>"},
		{"no-head-script", "", "<script>authorFirst()</script>" + meta},
	}
	for _, item := range cases {
		page := item.before + item.after
		view, err := render.LoaderPage(strings.NewReader(page), int64(len(page)), digest(page), "v", "d", "p/\u0080+%2F\\~&copy.html")
		Expect(err).NotTo(HaveOccurred(), item.name)
		Expect(view.Integrate).To(BeTrue(), item.name)
		Expect(view.MIME).To(Equal("text/html; charset=iso-2022-jp"), item.name)
		whole, err := io.ReadAll(view.Body)
		Expect(err).NotTo(HaveOccurred())
		start := bytes.Index(whole, []byte("<script src="))
		Expect(start).To(Equal(len(item.before)), item.name)
		end := bytes.Index(whole[start:], []byte("</script>")) + start + len("</script>")
		tag := whole[start:end]
		Expect(bytes.ContainsAny(tag, "\x1b\\~")).To(BeFalse(), item.name)
		restored := append(append([]byte{}, whole[:start]...), whole[end:]...)
		Expect(restored).To(Equal([]byte(page)), item.name)
		for _, at := range []int64{0, int64(start), int64(end) - 2, int64(len(whole)) - 6} {
			_, err = view.Body.Seek(at, io.SeekStart)
			Expect(err).NotTo(HaveOccurred())
			actual, err := io.ReadAll(io.LimitReader(view.Body, 6))
			Expect(err).NotTo(HaveOccurred())
			Expect(actual).To(Equal(whole[at:at+6]), item.name)
		}
	}
	for _, bad := range []string{"\x1b$X", "\x1b$(D", "\x1b(J\x1b(B", "\x1b$B!"} {
		page := "<!--" + bad + "--><!doctype html><head>" + meta + "</head>"
		view, err := render.LoaderPage(strings.NewReader(page), int64(len(page)), digest(page), "v", "d", "broken.html")
		Expect(err).NotTo(HaveOccurred())
		Expect(view.Integrate).To(BeFalse(), fmt.Sprintf("%q", bad))
		whole, err := io.ReadAll(view.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(whole).To(Equal([]byte(page)))
		Expect(view.ETag).To(Equal("\"sha256-" + digest(page) + "\""))
	}
})

var _ = It("preserves distinct C1, Unicode and literal URI identities through the actual HTML parser", Label("unit"), func() {
	for _, page := range []string{"p/\u0080.html", "p/€.html", "p/cr\r\nlf.html", "p/a+%2F\\~&copy.html", "p/cafe\u0301.html"} {
		source := "<!doctype html><head></head>"
		view, err := render.LoaderPage(strings.NewReader(source), int64(len(source)), digest(source), "v", "d", page)
		Expect(err).NotTo(HaveOccurred())
		whole, err := io.ReadAll(view.Body)
		Expect(err).NotTo(HaveOccurred())
		root, err := html.Parse(bytes.NewReader(whole))
		Expect(err).NotTo(HaveOccurred())
		var encoded string
		var visit func(*html.Node)
		visit = func(n *html.Node) {
			for _, a := range n.Attr {
				if a.Key == "data-haeryong-page" {
					encoded = a.Val
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				visit(c)
			}
		}
		visit(root)
		actual, err := url.PathUnescape(encoded)
		Expect(err).NotTo(HaveOccurred())
		Expect(actual).To(Equal(page))
	}
})

type failingHTMLSource struct {
	io.ReadSeeker
	reads, seeks       int
	failRead, failSeek int
	failure            error
}

func (r *failingHTMLSource) Read(p []byte) (int, error) {
	r.reads++
	if r.reads == r.failRead {
		return 0, r.failure
	}
	return r.ReadSeeker.Read(p)
}
func (r *failingHTMLSource) Seek(offset int64, whence int) (int64, error) {
	r.seeks++
	if r.seeks == r.failSeek {
		return 0, r.failure
	}
	return r.ReadSeeker.Seek(offset, whence)
}

var _ = It("propagates descriptor read and fallback-seek failures instead of disguising them as malformed content", Label("unit"), func() {
	injected := errors.New("descriptor I/O failed")
	page := strings.Repeat(" ", 2048)
	source := &failingHTMLSource{ReadSeeker: strings.NewReader(page), failRead: 2, failure: injected}
	_, err := render.LoaderPage(source, int64(len(page)), digest(page), "v", "d", "page.html")
	Expect(errors.Is(err, injected)).To(BeTrue())
	page = "<!--\x1b$X--><meta charset='iso-2022-jp'>"
	source = &failingHTMLSource{ReadSeeker: strings.NewReader(page), failSeek: 3, failure: injected}
	_, err = render.LoaderPage(source, int64(len(page)), digest(page), "v", "d", "page.html")
	Expect(errors.Is(err, injected)).To(BeTrue())
})

// F01: the charset attribute, when present, alone decides this meta element.
// An invalid charset value fails the meta entirely — the content pragma is
// never consulted regardless of attribute order — and the scan continues to
// the next declaration. Both attribute orders and the empty-value case are
// covered; the second meta always wins.
var _ = It("TestCharsetAttributePresenceBeatsPragmaInBothOrders", Label("unit"), func() {
	page := func(meta string) string {
		return "<!doctype html><html><head>" + meta + "<title>utf8 text</title></head><body>\xed\x95\x9c</body></html>"
	}
	second := `<meta charset="utf-8">`
	pragma := `http-equiv="Content-Type" content="text/html;charset=windows-1252"`
	cases := []struct {
		name string
		meta string
		mime string
	}{
		{"invalid charset then pragma then valid next", `<meta charset="invalid-label" ` + pragma + `>` + second, "text/html; charset=utf-8"},
		{"pragma then invalid charset then valid next", `<meta ` + pragma + ` charset="invalid-label">` + second, "text/html; charset=utf-8"},
		{"empty charset then pragma then valid next", `<meta charset="" ` + pragma + `>` + second, "text/html; charset=utf-8"},
		{"valid charset beats pragma", `<meta charset="euc-kr" ` + pragma + `>`, "text/html; charset=euc-kr"},
	}
	for _, item := range cases {
		full := page(item.meta)
		view, err := render.LoaderPage(strings.NewReader(full), int64(len(full)), hex64("f01"), "v", "d", "f01.html")
		Expect(err).NotTo(HaveOccurred(), func() string { return item.name + ": " + fmt.Sprint(err) })
		Expect(view.MIME).To(Equal(item.mime), func() string { return item.name + ": got " + view.MIME })
	}
})
