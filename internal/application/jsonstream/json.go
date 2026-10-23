package jsonstream

import (
	"bufio"
	"errors"
	"io"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid JSON value")

// CopyJSONValue validates and copies exactly one JSON value with finite buffers.
// It leaves a following delimiter unread; it never materializes authored strings.
func CopyJSONValue(r *bufio.Reader, out io.Writer) error {
	w := bufio.NewWriter(out)
	if err := copyJSON(r, w); err != nil {
		return err
	}
	return w.Flush()
}
func jsonSpace(r *bufio.Reader, w *bufio.Writer) error {
	for {
		b, e := r.Peek(1)
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		if b[0] != ' ' && b[0] != '\t' && b[0] != '\n' && b[0] != '\r' {
			return nil
		}
		v, _ := r.ReadByte()
		if e := w.WriteByte(v); e != nil {
			return e
		}
	}
}
func jsonByte(r *bufio.Reader, w *bufio.Writer, want byte) error {
	b, e := r.ReadByte()
	if e != nil || b != want {
		return ErrInvalid
	}
	return w.WriteByte(b)
}
func jsonString(r *bufio.Reader, w *bufio.Writer) error {
	if e := jsonByte(r, w, '"'); e != nil {
		return e
	}
	for {
		b, e := r.ReadByte()
		if e != nil {
			return ErrInvalid
		}
		if b < 0x20 {
			return ErrInvalid
		}
		if e := w.WriteByte(b); e != nil {
			return e
		}
		if b == '"' {
			return nil
		}
		if b == '\\' {
			x, e := r.ReadByte()
			if e != nil {
				return ErrInvalid
			}
			if e := w.WriteByte(x); e != nil {
				return e
			}
			switch x {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			case 'u':
				hex4 := func() (uint16, error) {
					var value uint16
					for n := 0; n < 4; n++ {
						h, e := r.ReadByte()
						if e != nil {
							return 0, ErrInvalid
						}
						var digit byte
						switch {
						case h >= '0' && h <= '9':
							digit = h - '0'
						case h >= 'a' && h <= 'f':
							digit = h - 'a' + 10
						case h >= 'A' && h <= 'F':
							digit = h - 'A' + 10
						default:
							return 0, ErrInvalid
						}
						if e := w.WriteByte(h); e != nil {
							return 0, e
						}
						value = value*16 + uint16(digit)
					}
					return value, nil
				}
				value, e := hex4()
				if e != nil {
					return e
				}
				if value >= 0xdc00 && value <= 0xdfff {
					return ErrInvalid
				}
				if value >= 0xd800 && value <= 0xdbff {
					if e := jsonByte(r, w, '\\'); e != nil {
						return e
					}
					if e := jsonByte(r, w, 'u'); e != nil {
						return e
					}
					low, e := hex4()
					if e != nil || low < 0xdc00 || low > 0xdfff {
						return ErrInvalid
					}
				}

			default:
				return ErrInvalid
			}
			continue
		}
		if b >= utf8.RuneSelf {
			if e := r.UnreadByte(); e != nil {
				return e
			}
			rune, n, e := r.ReadRune()
			if e != nil || rune == utf8.RuneError && n == 1 {
				return ErrInvalid
			}
			encoded := []byte(string(rune))
			if _, e := w.Write(encoded[1:]); e != nil {
				return e
			}
		}
	}
}
func copyJSON(r *bufio.Reader, w *bufio.Writer) error {
	if e := jsonSpace(r, w); e != nil {
		return e
	}
	b, e := r.Peek(1)
	if e != nil {
		return ErrInvalid
	}
	switch b[0] {
	case '"':
		return jsonString(r, w)
	case '{':
		if e := jsonByte(r, w, '{'); e != nil {
			return e
		}
		if e := jsonSpace(r, w); e != nil {
			return e
		}
		p, e := r.Peek(1)
		if e != nil {
			return ErrInvalid
		}
		if p[0] == '}' {
			return jsonByte(r, w, '}')
		}
		for {
			if e := jsonString(r, w); e != nil {
				return e
			}
			if e := jsonSpace(r, w); e != nil {
				return e
			}
			if e := jsonByte(r, w, ':'); e != nil {
				return e
			}
			if e := copyJSON(r, w); e != nil {
				return e
			}
			if e := jsonSpace(r, w); e != nil {
				return e
			}
			p, e := r.Peek(1)
			if e != nil {
				return ErrInvalid
			}
			if p[0] == '}' {
				return jsonByte(r, w, '}')
			}
			if e := jsonByte(r, w, ','); e != nil {
				return e
			}
			if e := jsonSpace(r, w); e != nil {
				return e
			}
		}
	case '[':
		if e := jsonByte(r, w, '['); e != nil {
			return e
		}
		if e := jsonSpace(r, w); e != nil {
			return e
		}
		p, e := r.Peek(1)
		if e != nil {
			return ErrInvalid
		}
		if p[0] == ']' {
			return jsonByte(r, w, ']')
		}
		for {
			if e := copyJSON(r, w); e != nil {
				return e
			}
			if e := jsonSpace(r, w); e != nil {
				return e
			}
			p, e := r.Peek(1)
			if e != nil {
				return ErrInvalid
			}
			if p[0] == ']' {
				return jsonByte(r, w, ']')
			}
			if e := jsonByte(r, w, ','); e != nil {
				return e
			}
		}
	case 't':
		for _, x := range []byte("true") {
			if e := jsonByte(r, w, x); e != nil {
				return e
			}
		}
	case 'f':
		for _, x := range []byte("false") {
			if e := jsonByte(r, w, x); e != nil {
				return e
			}
		}
	case 'n':
		for _, x := range []byte("null") {
			if e := jsonByte(r, w, x); e != nil {
				return e
			}
		}
	default:
		take := func() error {
			v, e := r.ReadByte()
			if e != nil {
				return e
			}
			return w.WriteByte(v)
		}
		peek := func() byte {
			v, e := r.Peek(1)
			if e != nil {
				return 0
			}
			return v[0]
		}
		if peek() == '-' {
			if e := take(); e != nil {
				return e
			}
		}
		p := peek()
		if p == '0' {
			if e := take(); e != nil {
				return e
			}
		} else {
			if p < '1' || p > '9' {
				return ErrInvalid
			}
			for peek() >= '0' && peek() <= '9' {
				if e := take(); e != nil {
					return e
				}
			}
		}
		if peek() == '.' {
			if e := take(); e != nil {
				return e
			}
			if peek() < '0' || peek() > '9' {
				return ErrInvalid
			}
			for peek() >= '0' && peek() <= '9' {
				if e := take(); e != nil {
					return e
				}
			}
		}
		if peek() == 'e' || peek() == 'E' {
			if e := take(); e != nil {
				return e
			}
			if peek() == '+' || peek() == '-' {
				if e := take(); e != nil {
					return e
				}
			}
			if peek() < '0' || peek() > '9' {
				return ErrInvalid
			}
			for peek() >= '0' && peek() <= '9' {
				if e := take(); e != nil {
					return e
				}
			}
		}
	}
	b, e = r.Peek(1)
	if e == io.EOF {
		return nil
	}
	if e != nil {
		return e
	}
	switch b[0] {
	case ' ', '\t', '\r', '\n', ',', '}', ']':
		return nil
	}
	return ErrInvalid
}
