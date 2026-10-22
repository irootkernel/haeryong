package render

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// LoaderQuery identifies the integration resource without reserving an
// authored filename: ordinary requests for that path still serve its bytes.
const LoaderQuery = "__haeryong_loader=" + RendererVersion

// Derivative is one delivered HTML representation: either the loader splice
// over the verified descriptor or, on an honest no-integration fallback, the
// untouched original bytes with their raw object validator.
type Derivative struct {
	Body      io.ReadSeeker
	ETag      string
	MIME      string
	Integrate bool
}

// pageIdentity is the one exact page-encoding contract of the loader tag:
// Go url.PathEscape with the single literal '~' -> '%7E' post-step (Go
// preserves '~' unescaped, but '~' is not ASCII-stable in ISO-2022-JP Roman
// state). The automatic loader reverses it with one decodeURIComponent call;
// '+' is never a space and percent characters round-trip exactly.
func pageIdentity(page string) string {
	return strings.ReplaceAll(url.PathEscape(page), "~", "%7E")
}

func escapeAttr(value string) string {
	// CR/LF/TAB use numeric references so the HTML parser cannot normalize
	// them away; the parsed dataset must round-trip the exact identity.
	return strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;", "\r", "&#13;", "\n", "&#10;", "\t", "&#9;").Replace(value)
}

// LoaderPage builds the delivered representation of an authored HTML page
// from the already verified descriptor. Header scanning and range delivery
// stay bounded; the complete authored HTML is never materialized and the
// stored bytes are never rewritten. Encoding provenance follows the WHATWG
// rules a browser itself applies: a BOM wins, then the first valid meta
// declaration (charset attribute first, then an http-equiv pragma; duplicate
// attribute names keep their first occurrence; invalid candidates keep
// scanning), with meta x-user-defined normalized to windows-1252 and meta
// UTF-16 labels normalized to UTF-8. No declaration keeps the deliberate
// UTF-8 transport default. Stateful encodings integrate through dedicated
// lexical scanners (UTF-16 unit-aware, ISO-2022-JP designation aware) or
// fall back honestly to the raw original bytes with only the derivative
// marker omitted. Real I/O failures propagate; only malformed input falls
// back.
func LoaderPage(source io.ReadSeeker, size int64, digest, version, document, page string) (Derivative, error) {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return Derivative{}, err
	}
	var preview [1024]byte
	n, err := io.ReadFull(source, preview[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return Derivative{}, err
	}
	if int64(n) < min(size, int64(len(preview))) {
		return Derivative{}, io.ErrUnexpectedEOF
	}
	enc := detectEncoding(preview[:n])
	raw := func() (Derivative, error) {
		if _, err := source.Seek(0, io.SeekStart); err != nil {
			return Derivative{}, err
		}
		return Derivative{Body: source, ETag: `"sha256-` + digest + `"`, MIME: "text/html; charset=" + enc.label}, nil
	}
	if enc.family == familyUTF16 && size%2 != 0 {
		return raw()
	}
	start := int64(0)
	if enc.family == familyUTF16 {
		start = 2
	} else if bytes.HasPrefix(preview[:n], []byte{0xef, 0xbb, 0xbf}) {
		start = 3
	}
	if _, err := source.Seek(start, io.SeekStart); err != nil {
		return Derivative{}, err
	}
	units := &pageUnits{reader: bufio.NewReaderSize(source, 4096), offset: start, family: enc.family, little: enc.label == "utf-16le"}
	at, err := headOffset(units)
	if errors.Is(err, errMalformedEncoding) {
		return raw()
	}
	if err != nil {
		return Derivative{}, err
	}
	tag := []byte(`<script src="/__haeryong/loader.js?` + LoaderQuery + `" data-haeryong-version="` + escapeAttr(version) +
		`" data-haeryong-document="` + escapeAttr(document) + `" data-haeryong-page="` + escapeAttr(pageIdentity(page)) +
		`" data-haeryong-content="` + escapeAttr(digest) + `" data-haeryong-renderer="` + RendererVersion + `"></script>`)
	if enc.family == familyUTF16 {
		encoded := make([]byte, 0, 2*len(tag))
		for _, b := range tag {
			if units.little {
				encoded = append(encoded, b, 0)
			} else {
				encoded = append(encoded, 0, b)
			}
		}
		tag = encoded
	}
	return derivativeFor(&pageSplice{source: source, sourceSize: size, at: at, tag: tag}, digest, page, enc.label, tag, at), nil
}

// derivativeFor derives the strong opaque validator from the exact
// representation identity: verified source digest, canonical page,
// renderer version, label, insertion position and the encoded tag bytes.
// It is a representation validator, not a claim about delivered bytes.
func derivativeFor(body io.ReadSeeker, digest, page, label string, tag []byte, at int64) Derivative {
	hash := sha256.New()
	for _, part := range []string{digest, page, RendererVersion, label, strconv.FormatInt(at, 10)} {
		hash.Write([]byte(part))
		hash.Write([]byte{0})
	}
	hash.Write(tag)
	return Derivative{Body: body, ETag: `"hyr-` + hex.EncodeToString(hash.Sum(nil)) + `"`, MIME: `text/html; charset=` + label, Integrate: true}
}

type encFamily int

const (
	familySimple encFamily = iota
	familyUTF16
	familyISO2022
)

type pageEncoding struct {
	family encFamily
	label  string
}

// detectEncoding resolves the delivery encoding exactly as a browser would:
// BOM provenance first (always winning), then the first valid meta
// declaration inside the prescan window, then the deliberate UTF-8 default.
// The 1024-byte window is the prescan scope, never an author cap.
func detectEncoding(head []byte) pageEncoding {
	if len(head) >= 2 && head[0] == 0xFF && head[1] == 0xFE {
		return pageEncoding{family: familyUTF16, label: "utf-16le"}
	}
	if len(head) >= 2 && head[0] == 0xFE && head[1] == 0xFF {
		return pageEncoding{family: familyUTF16, label: "utf-16be"}
	}
	if len(head) >= 3 && head[0] == 0xEF && head[1] == 0xBB && head[2] == 0xBF {
		return pageEncoding{family: familySimple, label: "utf-8"}
	}
	if label, ok := declaredLabel(head); ok {
		return label
	}
	return pageEncoding{family: familySimple, label: "utf-8"}
}

// declaredLabel scans the prescan window with the real x/net/html tokenizer.
// Duplicate attribute names keep their FIRST occurrence via an explicit seen
// map (the tokenizer does not do this itself); invalid candidates —
// unsupported labels or the replacement encoding — keep scanning for the
// next declaration.
func declaredLabel(head []byte) (pageEncoding, bool) {
	z := html.NewTokenizer(bytes.NewReader(head))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return pageEncoding{}, false
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "meta" {
				continue
			}
			// HTML prescan: the charset attribute is authoritative when
			// present. An invalid charset value fails THIS meta element —
			// the content pragma is never consulted for it. The pragma is
			// only eligible when the charset attribute is entirely absent.
			seen := map[string]bool{}
			charsetSeen := false
			charsetLabel := "" // canonical label from charset attr
			pragmaLabel := ""  // canonical label from content pragma
			gotPragma := false
			for hasAttr {
				key, value, more := z.TagAttr()
				hasAttr = more
				k := string(key)
				if seen[k] {
					continue
				}
				seen[k] = true
				switch k {
				case "charset":
					charsetSeen = true
					_, charsetLabel = charset.Lookup(string(value))
				case "http-equiv":
					gotPragma = strings.EqualFold(string(value), "content-type")
				case "content":
					if candidate := pragmaCharset(string(value)); candidate != "" {
						_, pragmaLabel = charset.Lookup(candidate)
					}
				}
			}
			label := ""
			if charsetSeen {
				// charset attribute present: it alone decides this meta.
				label = charsetLabel
			} else if gotPragma && pragmaLabel != "" {
				label = pragmaLabel
			}
			if label == "" || label == "replacement" {
				continue
			}
			switch label {
			case "utf-16le", "utf-16be":
				label = "utf-8"
			case "x-user-defined":
				label = "windows-1252"
			}
			family := familySimple
			if label == "iso-2022-jp" {
				family = familyISO2022
			}
			return pageEncoding{family: family, label: label}, true
		}
	}
}

// pragmaCharset extracts the charset=... value of an http-equiv Content-Type
// pragma following the WHATWG prescan rules: the parameter name matching is
// ASCII case-insensitive, whitespace is allowed around '=' and values may be
// quoted; extraction stops at the parameter separator.
func pragmaCharset(content string) string {
	// The HTML prescan searches successive charset occurrences, trims only
	// ASCII whitespace, and treats quoted and unquoted values separately.
	remaining := strings.ToLower(content)
	for remaining != "" {
		i := strings.Index(remaining, "charset")
		if i < 0 {
			return ""
		}
		remaining = strings.TrimLeft(remaining[i+len("charset"):], " \t\n\f\r")
		if !strings.HasPrefix(remaining, "=") {
			continue
		}
		remaining = strings.TrimLeft(remaining[1:], " \t\n\f\r")
		if remaining == "" {
			return ""
		}
		if quote := remaining[0]; quote == '\'' || quote == '"' {
			if end := strings.IndexByte(remaining[1:], quote); end >= 0 {
				return remaining[1 : 1+end]
			}
			return ""
		}
		if end := strings.IndexAny(remaining, "; \t\n\f\r"); end >= 0 {
			return remaining[:end]
		}
		return remaining
	}
	return ""
}

// A unit carries its ORIGINAL byte span, including preceding zero-width
// designations. Lookahead never changes a previously buffered unit's state.
// Only HTML's ASCII syntax matters; Japanese characters need no charmap.
type pageUnit struct {
	ch                 rune
	start, end         int64
	safeStart, safeEnd bool
}
type isoMode uint8

const (
	isoASCII isoMode = iota
	isoRoman
	isoKatakana
	isoJIS
)

var errMalformedEncoding = errors.New("malformed HTML encoding prefix")

type pageUnits struct {
	reader *bufio.Reader
	offset int64
	family encFamily
	little bool
	mode   isoMode
	output bool // WHATWG ISO-2022-JP output flag, true after a designation
}

func (r *pageUnits) readByte() (byte, error) {
	b, err := r.reader.ReadByte()
	if err == nil {
		r.offset++
	}
	return b, err
}
func (r *pageUnits) continuation() (byte, error) {
	b, err := r.readByte()
	if errors.Is(err, io.EOF) {
		return 0, errMalformedEncoding
	}
	return b, err
}
func (r *pageUnits) next() (pageUnit, error) {
	start := r.offset
	safe := r.family != familyISO2022 || ((r.mode == isoASCII || r.mode == isoRoman) && !r.output)
	for {
		b, err := r.readByte()
		if err != nil {
			return pageUnit{}, err
		}
		if r.family == familyUTF16 {
			other, err := r.continuation()
			if err != nil {
				return pageUnit{}, err
			}
			value := uint16(b)<<8 | uint16(other)
			if r.little {
				value = uint16(other)<<8 | uint16(b)
			}
			return pageUnit{rune(value), start, r.offset, true, true}, nil
		}
		if r.family != familyISO2022 {
			return pageUnit{rune(b), start, r.offset, true, true}, nil
		}
		if b == 0x1b {
			a, err := r.continuation()
			if err != nil {
				return pageUnit{}, err
			}
			z, err := r.continuation()
			if err != nil {
				return pageUnit{}, err
			}
			switch {
			case a == '(' && z == 'B':
				r.mode = isoASCII
			case a == '(' && z == 'J':
				r.mode = isoRoman
			case a == '(' && z == 'I':
				r.mode = isoKatakana
			case a == '$' && (z == '@' || z == 'B'):
				r.mode = isoJIS
			default:
				return pageUnit{}, errMalformedEncoding
			}
			// Consecutive designations produce U+FFFD in the browser. They are
			// malformed input, not a license to inject a reset/restore sequence.
			if r.output {
				return pageUnit{}, errMalformedEncoding
			}
			r.output = true
			continue
		}
		var ch rune
		switch r.mode {
		case isoASCII, isoRoman:
			if b > 0x7f || b == 0x0e || b == 0x0f {
				return pageUnit{}, errMalformedEncoding
			}
			ch = rune(b)
			if r.mode == isoRoman {
				if b == 0x5c {
					ch = 0x00a5
				}
				if b == 0x7e {
					ch = 0x203e
				}
			}
		case isoKatakana:
			if b < 0x21 || b > 0x5f {
				return pageUnit{}, errMalformedEncoding
			}
			ch = 0xff61 + rune(b-0x21)
		case isoJIS:
			other, err := r.continuation()
			if err != nil {
				return pageUnit{}, err
			}
			if b < 0x21 || b > 0x7e || other < 0x21 || other > 0x7e {
				return pageUnit{}, errMalformedEncoding
			}
			ch = 0x110000 // Opaque non-ASCII character; never HTML punctuation.
		}
		r.output = false
		return pageUnit{ch, start, r.offset, safe, r.mode == isoASCII || r.mode == isoRoman}, nil
	}
}

// headOffset shares the original insertion rules across byte, UTF-16 and
// ISO-2022-JP units. Its fixed lookahead and integer comment tail keep memory
// bounded even for arbitrarily long authored comments or attributes.
func headOffset(source *pageUnits) (int64, error) {
	var queue [10]pageUnit
	count := 0
	cursor := source.offset
	safeCursor := true
	peek := func(n int) ([]pageUnit, error) {
		for count < n {
			unit, err := source.next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			queue[count] = unit
			count++
		}
		return queue[:count], nil
	}
	consume := func(n int) {
		cursor, safeCursor = queue[n-1].end, queue[n-1].safeEnd
		copy(queue[:], queue[n:count])
		count -= n
	}
	boundary := func(unit pageUnit) (int64, error) {
		if !unit.safeStart {
			return 0, errMalformedEncoding
		}
		return unit.start, nil
	}
	match := func(units []pageUnit, text string, fold bool) bool {
		if len(units) < len(text) {
			return false
		}
		for i := range text {
			ch := units[i].ch
			if fold && ch >= 'A' && ch <= 'Z' {
				ch += 'a' - 'A'
			}
			if ch != rune(text[i]) {
				return false
			}
		}
		return true
	}
	for {
		units, err := peek(1)
		if err != nil {
			return 0, err
		}
		if len(units) == 0 {
			if !safeCursor {
				return 0, errMalformedEncoding
			}
			return cursor, nil
		}
		if spaceUnit(units[0].ch) {
			consume(1)
			continue
		}
		units, err = peek(10)
		if err != nil {
			return 0, err
		}
		first := units[0]
		comment := match(units, "<!--", false)
		doctype := match(units, "<!doctype", true) && (len(units) == 9 || spaceUnit(units[9].ch) || units[9].ch == '>')
		htmlTag := match(units, "<html", true) && len(units) > 5 && (spaceUnit(units[5].ch) || units[5].ch == '>' || units[5].ch == '/')
		headTag := match(units, "<head", true) && len(units) > 5 && (spaceUnit(units[5].ch) || units[5].ch == '>' || units[5].ch == '/')
		bogus := match(units, "<!", false) || match(units, "<?", false)
		if !comment && !doctype && !htmlTag && !headTag && !bogus {
			return boundary(first)
		}
		prefix := 2
		if comment {
			prefix = 4
		} else if doctype {
			prefix = 9
		} else if htmlTag || headTag {
			prefix = 5
		}
		consume(prefix)
		var tail uint32
		var commentBytes int64
		var quote rune
		for {
			units, err = peek(1)
			if err != nil {
				return 0, err
			}
			if len(units) == 0 {
				return boundary(first)
			}
			ch := units[0].ch
			consume(1)
			if comment {
				commentBytes++
				if ch >= 0 && ch <= 0x7f {
					tail = tail<<8 | uint32(ch)
				} else {
					tail = 0
				}
				if tail&0xffffff == 0x2d2d3e || tail == 0x2d2d213e || (commentBytes == 1 && ch == '>') || (commentBytes == 2 && tail == 0x2d3e) {
					break
				}
				continue
			}
			if (doctype || htmlTag || headTag) && (ch == '\'' || ch == '"') {
				if quote == 0 {
					quote = ch
				} else if quote == ch {
					quote = 0
				}
			}
			if ch == '>' && quote == 0 {
				break
			}
		}
		if headTag {
			if !safeCursor {
				return 0, errMalformedEncoding
			}
			return cursor, nil
		}
	}
}
func spaceUnit(ch rune) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', '\f':
		return true
	}
	return false
}

type pageSplice struct {
	source     io.ReadSeeker
	sourceSize int64
	at         int64
	tag        []byte
	position   int64
}

func (r *pageSplice) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	if r.position >= r.sourceSize+int64(len(r.tag)) {
		return 0, io.EOF
	}
	if r.position >= r.at && r.position < r.at+int64(len(r.tag)) {
		n := copy(out, r.tag[r.position-r.at:])
		r.position += int64(n)
		return n, nil
	}
	physical := r.position
	if r.position >= r.at+int64(len(r.tag)) {
		physical -= int64(len(r.tag))
	} else if int64(len(out)) > r.at-r.position {
		out = out[:r.at-r.position]
	}
	if _, err := r.source.Seek(physical, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := r.source.Read(out)
	r.position += int64(n)
	if errors.Is(err, io.EOF) && r.position < r.sourceSize+int64(len(r.tag)) {
		if physical+int64(n) < r.sourceSize {
			return n, io.ErrUnexpectedEOF
		}
		return n, nil
	}
	return n, err
}

func (r *pageSplice) Seek(offset int64, whence int) (int64, error) {
	target := offset
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		target += r.position
	case io.SeekEnd:
		target += r.sourceSize + int64(len(r.tag))
	default:
		return 0, errors.New("invalid seek origin")
	}
	if target < 0 {
		return 0, errors.New("negative seek position")
	}
	r.position = target
	return target, nil
}

// HTMLReader uses the same encoding authority as LoaderPage, including its
// deliberate UTF-8 default. The prescan window bounds detection, not input.
func HTMLReader(input io.Reader) (io.Reader, error) {
	r := bufio.NewReader(input)
	head, err := r.Peek(1024)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	enc := detectEncoding(head)
	bom := 0
	if bytes.HasPrefix(head, []byte{0xef, 0xbb, 0xbf}) {
		bom = 3
	} else if bytes.HasPrefix(head, []byte{0xff, 0xfe}) || bytes.HasPrefix(head, []byte{0xfe, 0xff}) {
		bom = 2
	}
	if bom > 0 {
		if _, err = r.Discard(bom); err != nil {
			return nil, err
		}
	}
	return charset.NewReaderLabel(enc.label, r)
}
