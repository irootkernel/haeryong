package search

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/domain"
)

type textSpool struct {
	data []byte
	fail bool
}

func (s *textSpool) Write(p []byte) (int, error) {
	if s.fail {
		return 0, io.ErrClosedPipe
	}
	s.data = append(s.data, p...)
	return len(p), nil
}
func (s *textSpool) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(s.data)) {
		return 0, io.EOF
	}
	n := copy(p, s.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, errors.New("injected read failure") }

var _ = Describe("SC09 static extraction", Label("unit"), func() {
	It("keeps exact aliases, page-local Unicode anchors, ambiguity and EOF status (reversal: Extract per-element dedup/AnchorState complete guard)", func() {
		input := `<!doctype html><html><head><title>Reader Title</title><meta name="keywords" content="한글, Retry"></head><body><section id="retry" data-haeryong-region="retry"><h2 id="label">Retry Label</h2><p>read<b>Source</b> 검토 &amp; 승인</p><pre>retry.go</pre><table><tr><td>cell</td></tr></table><a href="https://example.invalid">Link</a> <button>Static control</button> <noscript>Fallback</noscript><script>SECRET</script><style>HIDDEN</style><template><div>OMIT</div></template></section><div id="dup">one</div><div data-haeryong-region="dup">two</div><div id="é" data-haeryong-region="é">alias</div><div aria-labelledby="label" id="aria">body</div><span id="empty"></span><div id="emptyLabel" aria-labelledby="empty">body</div></body></html>`
		var fragments []Fragment
		spool := &textSpool{}
		result, err := Extract(context.Background(), strings.NewReader(input), "text/html; charset=utf-8", spool, func(f Fragment) error { fragments = append(fragments, f); return nil })
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Complete).To(BeTrue())
		Expect(result.Title).To(Equal("Reader Title"))
		Expect(result.Keywords).To(Equal("한글, Retry"))
		Expect(result.AnchorState("retry")).To(Equal("present"))
		Expect(result.AnchorState("dup")).To(Equal("ambiguous"))
		Expect(result.AnchorState("missing")).To(Equal("missing"))
		Expect((Extraction{}).AnchorState("missing")).To(Equal("pending"))
		byID := map[string]Anchor{}
		for _, a := range result.Anchors {
			byID[a.ID] = a
		}
		Expect(byID["retry"].Occurrences).To(Equal(1))
		Expect(byID["retry"].LocatorKind).To(Equal("both"))
		Expect(byID["é"].Occurrences).To(Equal(1))
		Expect(byID["é"].Occurrences).To(Equal(1))
		Expect(byID["aria"].Label).To(Equal("Retry Label"))
		Expect(byID["dup"].TextSHA256).To(BeEmpty())
		Expect(byID["emptyLabel"].Label).To(Equal("emptyLabel"))
		Expect(fragments[0].AnchorID).To(BeEmpty())
		body := fragments[0].Chunk.Text
		Expect(body).To(ContainSubstring("readsource 검토 & 승인 retry.go cell link static control fallback"))
		for _, excluded := range []string{"secret", "hidden", "omit", "reader title"} {
			Expect(body).NotTo(ContainSubstring(excluded))
		}
		Expect(string(spool.data)).To(ContainSubstring("readSource"), "display spelling survives separately")
		Expect(byID["retry"].TextSHA256).To(Equal(fmt.Sprintf("%x", sha256.Sum256([]byte(body[:strings.Index(body, " one")])))))
		other, err := Extract(context.Background(), strings.NewReader(`<p id="retry">other page</p>`), "text/html; charset=utf-8", &textSpool{}, func(Fragment) error { return nil })
		Expect(err).NotTo(HaveOccurred())
		Expect(other.AnchorState("retry")).To(Equal("present"))
		Expect(other.Anchors[0].TextSHA256).NotTo(Equal(byID["retry"].TextSHA256))
	})
	It("SC09 E1 ends template suppression at its matching tag despite implied inner endings (reversal: matching template suppression)", func() {
		for _, input := range []string{
			`<template><p>hidden</template><p id="visible">Visible</p>`,
			`<template><p>hidden<template><li>nested hidden</template>still hidden</template><p id="visible">Visible</p>`,
			`<template><script>"</template>"</script><p>hidden</template><p id="visible">Visible</p>`,
		} {
			var body string
			x, e := Extract(context.Background(), strings.NewReader(input), "text/html; charset=utf-8", &textSpool{}, func(f Fragment) error {
				if f.AnchorID == "" {
					body += f.Chunk.Text
				}
				return nil
			})
			Expect(e).NotTo(HaveOccurred())
			Expect(x.Complete).To(BeTrue())
			Expect(body).To(Equal("visible"))
			Expect(x.AnchorState("visible")).To(Equal("present"))
		}
	})
	It("SC09 E2/E5 bounds fragments and digests at implied siblings and EOF after body/html ends (reversal: implied starts/body-html stack preservation)", func() {
		for _, row := range []struct {
			html string
			want map[string]string
		}{
			{`<p id=a>A<p id=b>B</p>`, map[string]string{"a": "a", "b": "b"}},
			{`<p id=a>A<div id=b>B</div>`, map[string]string{"a": "a", "b": "b"}},
			{`<ul><li id=a>A<li id=b>B</ul>`, map[string]string{"a": "a", "b": "b"}},
			{`<dl><dt id=a>A<dd id=b>B<dt id=c>C</dl>`, map[string]string{"a": "a", "b": "b", "c": "c"}},
			{`<select><option id=a>A<option id=b>B</select>`, map[string]string{"a": "a", "b": "b"}},
			{`<select><optgroup id=a><option>A<optgroup id=b><option>B</select>`, map[string]string{"a": "a", "b": "b"}},
			{`<table><tbody><tr><td id=a>A<td id=b>B</tr></tbody></table>`, map[string]string{"a": "a", "b": "b"}},
			{`<table><tr id=a><td>A<tr id=b><td>B</table>`, map[string]string{"a": "a", "b": "b"}},
			{`<table><thead id=a><tr><th>A<tbody id=b><tr><td>B<tfoot id=c><tr><td>C</table>`, map[string]string{"a": "a", "b": "b", "c": "c"}},
			{`<ruby><rb id=a>A<rb id=b>B</ruby>`, map[string]string{"a": "a", "b": "b"}},
			{`<ruby>base<rt id=a>A<rt id=b>B</ruby>`, map[string]string{"a": "a", "b": "b"}},
			{`<ruby><rp id=a>A<rt id=b>B<rp id=c>C</ruby>`, map[string]string{"a": "a", "b": "b", "c": "c"}},
			{`<ruby><rtc id=a><rt>A<rtc id=b><rt>B</ruby>`, map[string]string{"a": "a", "b": "b"}},
			{`<ruby><rtc id=a><rt id=b>A<rt id=c>B</ruby>`, map[string]string{"a": "ab", "b": "a", "c": "b"}},
			{`<p id=a>A</body>TAIL`, map[string]string{"a": "atail"}},
			{`<body><p id=a>A</body>TAIL`, map[string]string{"a": "atail"}},
			{`<p id=a>A</html>TAIL`, map[string]string{"a": "atail"}},
			{`<html><body><p id=a>A</body></html>TAIL`, map[string]string{"a": "atail"}},
			{"<p id=a>A</body> \t\r\n", map[string]string{"a": "a"}},
			{"<html><body><p id=a>A</body></html> \t\r\n", map[string]string{"a": "a"}},
			{`<body><p id=a>A</body><p id=b>B</p>`, map[string]string{"a": "a", "b": "b"}},
			{`<html><body><p id=a>A</body></html><p id=b>B</p>`, map[string]string{"a": "a", "b": "b"}},
			{`<ul><li id=a>A<ul><li id=b>B<li id=c>C</ul>D<li id=d>E</ul>`, map[string]string{"a": "a b c d", "b": "b", "c": "c", "d": "e"}},
			{`<table><tr><td id=a>A<table><tr><td id=b>B<td id=c>C</table>D</table>`, map[string]string{"a": "a b c d", "b": "b", "c": "c"}},
		} {
			By(row.html)
			spool := &textSpool{}
			fragments := map[string]string{}
			display := map[string]string{}
			x, e := Extract(context.Background(), strings.NewReader(row.html), "text/html; charset=utf-8", spool, func(f Fragment) error {
				fragments[f.AnchorID] += f.Chunk.Text
				display[f.AnchorID] = Normalize(string(spool.data[f.DisplayStart:f.DisplayEnd]))
				return nil
			})
			Expect(e).NotTo(HaveOccurred())
			Expect(x.Complete).To(BeTrue())
			anchors := map[string]Anchor{}
			for _, a := range x.Anchors {
				anchors[a.ID] = a
			}
			for id, want := range row.want {
				Expect(x.AnchorState(id)).To(Equal("present"))
				Expect(fragments[id]).To(Equal(want), "fragment "+id)
				Expect(display[id]).To(Equal(want), "display range "+id)
				Expect(anchors[id].TextSHA256).To(Equal(fmt.Sprintf("%x", sha256.Sum256([]byte(want)))), "digest "+id)
			}
		}
	})
	It("SC09 E3 tokenizes noscript fallback markup, inline phrase and nested anchor (reversal: NextIsNotRawText)", func() {
		fragments := map[string]string{}
		x, e := Extract(context.Background(), strings.NewReader(`<noscript>read<b id=n>Source</b> 승인</noscript>`), "text/html; charset=utf-8", &textSpool{}, func(f Fragment) error { fragments[f.AnchorID] += f.Chunk.Text; return nil })
		Expect(e).NotTo(HaveOccurred())
		Expect(x.Complete).To(BeTrue())
		Expect(fragments[""]).To(Equal("readsource 승인"))
		Expect(fragments["n"]).To(Equal("source"))
		Expect(x.AnchorState("n")).To(Equal("present"))
		Expect(x.Anchors[0].TextSHA256).To(Equal(fmt.Sprintf("%x", sha256.Sum256([]byte("source")))))
	})

	It("includes tail and nested descendants and never completes on read/spool/sink errors (reversal: Extract EOF/error guards)", func() {
		input := `<section id="outer"><p id="inner">` + strings.Repeat("prose ", 12000) + `UniqueTail</p></section>`
		fragments := map[string][]Chunk{}
		result, err := Extract(context.Background(), strings.NewReader(input), "text/html; charset=utf-8", &textSpool{}, func(f Fragment) error { fragments[f.AnchorID] = append(fragments[f.AnchorID], f.Chunk); return nil })
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Complete).To(BeTrue())
		for _, id := range []string{"", "outer", "inner"} {
			Expect(fragments[id]).To(HaveLen(3))
			Expect(fragments[id][2].Text).To(HaveSuffix("uniquetail"))
		}
		for _, row := range []struct {
			reader io.Reader
			spool  *textSpool
			sink   func(Fragment) error
		}{
			{io.MultiReader(strings.NewReader(input), failedReader{}), &textSpool{}, func(Fragment) error { return nil }},
			{strings.NewReader(input), &textSpool{fail: true}, func(Fragment) error { return nil }},
			{strings.NewReader(input), &textSpool{}, func(Fragment) error { return io.ErrClosedPipe }},
		} {
			r, e := Extract(context.Background(), row.reader, "text/html; charset=utf-8", row.spool, row.sink)
			Expect(e).To(HaveOccurred())
			Expect(r.Complete).To(BeFalse())
			Expect(r.AnchorState("outer")).To(Equal("pending"))
		}
	})
	It("handles an omitted head end and preserves adjacent inline text (reversal: Extract implicit head closure/block separators)", func() {
		var body string
		x, e := Extract(context.Background(), strings.NewReader(`<head><title>Title</title><body><p>read<b>Source</b></p><p>tail</p>`), "text/html; charset=utf-8", &textSpool{}, func(f Fragment) error {
			if f.AnchorID == "" {
				body += f.Chunk.Text
			}
			return nil
		})
		Expect(e).NotTo(HaveOccurred())
		Expect(x.Complete).To(BeTrue())
		Expect(x.Title).To(Equal("Title"))
		Expect(body).To(Equal("readsource tail"))
	})

	It("retains unsupported inventory paths and logical metadata without source roots (reversal: InventoryUnit/MetadataText allowlist)", func() {
		for _, e := range []domain.Entry{{Path: "opaque.weird", Kind: "file"}, {Path: "nested", Kind: "directory"}, {Path: "alias", Kind: "symlink", LinkText: "outside"}} {
			u := InventoryUnit("v", e, false)
			Expect(u.PagePath).To(Equal(e.Path))
			Expect(u.Kind).To(Equal("attachment"))
			Expect(u.BodyState).To(Equal("unsupported"))
		}
		Expect(InventoryUnit("v", domain.Entry{Path: "supplied.unknown", Kind: "file"}, true).BodyState).To(Equal("pending"))
		value, err := MetadataText([]byte(`{"id":"source","kind":"git","repo_id":"logical","exact_revision":"abc","root_path":"PRIVATE","bytes":"SECRET"}`), true)
		Expect(err).NotTo(HaveOccurred())
		Expect(value).To(Equal("source git logical abc"))
		value, err = MetadataText([]byte(`{"id":"retry","label":"Supplied Label","summary":"unselected"}`), false)
		Expect(err).NotTo(HaveOccurred())
		Expect(value).To(Equal("Supplied Label retry"))
	})
})
