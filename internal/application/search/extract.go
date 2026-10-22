package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"haeryong.local/haeryong/internal/application/render"
)

// TextSpool belongs to the caller (normally a private temporary file). Descendant
// text is stored once; nested anchors retain byte ranges rather than subtree strings.
// The caller starts with an empty spool and releases it on every success/error path.
type TextSpool interface {
	io.Writer
	io.ReaderAt
}
type Anchor struct {
	ID, LocatorKind, Label, TextSHA256 string
	Occurrences                        int
}
type Extraction struct {
	Title, Keywords string
	Anchors         []Anchor
	Complete        bool
}
type Fragment struct {
	AnchorLabel              string
	AnchorID                 string // empty means page body
	Chunk                    Chunk
	DisplayStart, DisplayEnd int64 // exact collapsed display text in the caller's spool
}
type element struct {
	tag            string
	start, end     int64
	ids            []string
	labelledBy     string
	heading        bool
	sequence, last int
}
type openElement struct {
	tag   string
	index int
}
type anchorRange struct {
	element int
	kind    string
}

// AnchorState never turns absence into missing until successful EOF.
func (x Extraction) AnchorState(id string) string {
	if !x.Complete {
		return "pending"
	}
	for _, a := range x.Anchors {
		if a.ID == id {
			if a.Occurrences > 1 {
				return "ambiguous"
			}
			return "present"
		}
	}
	return "missing"
}

// Extract streams static HTML, then reads spooled ranges for chunks and digests.
// It follows no links and evaluates no script, CSS or runtime DOM. Read, spool,
// cancellation and callback failures leave Complete false, including after tokens.
func Extract(ctx context.Context, input io.Reader, _ string, spool TextSpool, emit func(Fragment) error) (result Extraction, err error) {
	decoded, err := render.HTMLReader(cancellableInput{ctx: ctx, reader: input})
	if err != nil {
		return result, err
	}
	z := html.NewTokenizer(decoded) // No SetMaxBuf: an oversized token is not false EOF.
	var offset int64
	var pending, haveText bool
	write := func(s string) error {
		n, e := io.WriteString(spool, s)
		offset += int64(n)
		if e == nil && n != len(s) {
			e = io.ErrShortWrite
		}
		return e
	}
	text := func(s string) error {
		var b strings.Builder
		runes := 0
		for _, c := range s {
			runes++
			if runes%4096 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if unicode.IsSpace(c) {
				pending = haveText
				continue
			}
			if pending {
				b.WriteByte(' ')
				pending = false
			}
			b.WriteRune(c)
			haveText = true
			if b.Len() >= 32768 {
				if err := write(b.String()); err != nil {
					return err
				}
				b.Reset()
			}
		}
		return write(b.String())
	}
	elements := []element{}
	sequence := 0
	anchors := map[string][]anchorRange{}
	order := []string{}
	var title strings.Builder
	inTitle := false
	head := false
	suppressed := ""
	templateDepth := 0
	closeAt := func(frame openElement) {
		if frame.index >= 0 {
			e := &elements[frame.index]
			e.end = offset
			e.last = sequence
		}
		if isBlock(frame.tag) {
			pending = haveText
		}
	}
	stack := elementStack{close: closeAt}
	for {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		token := z.Next()
		if token == html.ErrorToken {
			if z.Err() != io.EOF {
				return result, z.Err()
			}
			break
		}
		switch token {
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			tag := t.Data
			if tag == "noscript" {
				z.NextIsNotRawText()
			}
			if suppressed != "" {
				if suppressed == "template" && tag == "template" {
					templateDepth++
				}
				continue
			}
			if tag == "script" || tag == "style" || tag == "template" {
				if token != html.SelfClosingTagToken {
					suppressed = tag
					templateDepth = 1
				}
				continue
			}
			if tag == "head" {
				head = true
			}
			// HTML permits omitting </head>; a body element closes it implicitly.
			if head && !strings.Contains(" head base basefont bgsound link meta title noframes noscript ", " "+tag+" ") {
				head = false
			}
			if tag == "title" {
				inTitle = true
			}
			if tag == "meta" {
				name, content := "", ""
				for _, a := range t.Attr {
					if a.Key == "name" {
						name = a.Val
					}
					if a.Key == "content" {
						content = a.Val
					}
				}
				if strings.EqualFold(name, "keywords") {
					result.Keywords += " " + content
				}
			}
			if head || tag == "title" {
				continue
			}
			stack.start(tag)
			if isBlock(tag) {
				pending = haveText
			}
			sequence++
			e := element{tag: tag, start: offset, sequence: sequence, heading: len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6'}
			kinds := map[string]string{}
			for _, a := range t.Attr {
				if a.Key == "aria-labelledby" {
					e.labelledBy = a.Val
				}
				if (a.Key == "id" || a.Key == "data-haeryong-region") && a.Val != "" {
					kind := "id"
					if a.Key != "id" {
						kind = "data_region"
					}
					if prior, ok := kinds[a.Val]; ok {
						if prior != kind {
							kinds[a.Val] = "both"
						}
					} else {
						e.ids = append(e.ids, a.Val)
						kinds[a.Val] = kind
					}
				}
			}
			index := -1
			if len(e.ids) > 0 || e.heading {
				index = len(elements)
				elements = append(elements, e)
				for _, id := range e.ids {
					if _, ok := anchors[id]; !ok {
						order = append(order, id)
					}
					anchors[id] = append(anchors[id], anchorRange{index, kinds[id]})
				}
			}
			frame := openElement{tag, index}
			if token == html.SelfClosingTagToken || isVoid(tag) {
				closeAt(frame)
			} else {
				stack.open = append(stack.open, frame)
			}
		case html.EndTagToken:
			tag := z.Token().Data
			if suppressed != "" {
				if tag == suppressed {
					if suppressed == "template" {
						templateDepth--
					}
					if suppressed != "template" || templateDepth == 0 {
						suppressed = ""
					}
				}
				continue
			}
			if tag == "title" {
				inTitle = false
				continue
			}
			if tag == "head" {
				head = false
				continue
			}
			stack.end(tag)
		case html.TextToken:
			if suppressed != "" {
				continue
			}
			s := string(z.Text())
			if inTitle {
				title.WriteString(s)
				continue
			}
			if head {
				continue
			}
			if err = text(s); err != nil {
				return result, err
			}
		}
	}
	stack.popFrom(0)
	result.Title = strings.Join(strings.Fields(title.String()), " ")
	result.Keywords = strings.Join(strings.Fields(result.Keywords), " ")
	normalized := func(e element, visit func(Chunk) error) error {
		return Chunks(ctx, io.NewSectionReader(spool, e.start, e.end-e.start), visit)
	}
	labelText := func(e element) (string, error) {
		raw, err := io.ReadAll(io.NewSectionReader(spool, e.start, e.end-e.start))
		return strings.Join(strings.Fields(string(raw)), " "), err
	}
	for _, id := range order {
		ranges := anchors[id]
		a := Anchor{ID: id, Occurrences: len(ranges), LocatorKind: ranges[0].kind, Label: id}
		if len(ranges) == 1 {
			e := elements[ranges[0].element]
			h := sha256.New()
			err = normalized(e, func(c Chunk) error {
				r := []rune(c.Text)
				if c.Ordinal > 0 {
					r = r[ChunkOverlap:]
				}
				_, er := io.WriteString(h, string(r))
				return er
			})
			if err != nil {
				return result, err
			}
			a.TextSHA256 = hex.EncodeToString(h.Sum(nil))
			var labels []string
			for _, ref := range strings.Fields(e.labelledBy) {
				if rr := anchors[ref]; len(rr) == 1 {
					v, er := labelText(elements[rr[0].element])
					if er != nil {
						return result, er
					}
					if v != "" {
						labels = append(labels, v)
					}
				}
			}
			if len(labels) > 0 {
				a.Label = strings.Join(labels, " ")
			} else {
				for j := ranges[0].element; j < len(elements); j++ {
					he := elements[j]
					if he.sequence > e.last {
						break
					}
					if he.heading && he.sequence >= e.sequence && he.last <= e.last {
						v, er := labelText(he)
						if er != nil {
							return result, er
						}
						if v != "" {
							a.Label = v
						}
						break
					}
				}
			}
		} else {
			for _, r := range ranges[1:] {
				if r.kind != a.LocatorKind {
					a.LocatorKind = "both"
				}
			}
		}
		result.Anchors = append(result.Anchors, a)
	}
	labels := map[string]string{}
	for _, a := range result.Anchors {
		labels[a.ID] = a.Label
	}
	emitRange := func(id string, e element) error {
		return normalized(e, func(c Chunk) error {
			return emit(Fragment{AnchorID: id, AnchorLabel: labels[id], Chunk: c, DisplayStart: e.start, DisplayEnd: e.end})
		})
	}
	if err = emitRange("", element{start: 0, end: offset}); err != nil {
		return result, err
	}
	for _, id := range order {
		rs := anchors[id]
		if len(rs) == 1 {
			if err = emitRange(id, elements[rs[0].element]); err != nil {
				return result, err
			}
		}
	}
	result.Complete = true
	return result, nil
}

func isVoid(tag string) bool {
	return strings.Contains(" area base br col embed hr img input link meta param source track wbr ", " "+tag+" ")
}
func isBlock(tag string) bool {
	return strings.Contains(" address article aside blockquote br caption dd details div dl dt fieldset figcaption figure footer form h1 h2 h3 h4 h5 h6 header hr li main nav ol p pre section table tbody td tfoot th thead tr ul ", " "+tag+" ")
}

// A single HTML token can span the complete input. Observe cancellation inside
// tokenizer reads, rather than waiting for Next to return that enormous token.
// This is a read quantum, never a content limit or an invented EOF.
type cancellableInput struct {
	ctx    context.Context
	reader io.Reader
}

func (r cancellableInput) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > 4096 {
		p = p[:4096]
	}
	return r.reader.Read(p)
}
