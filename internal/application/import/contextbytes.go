// Streaming context-byte validation. Context bodies are exact UTF-8 JSON or
// text; validation and media detection run in one bounded-memory pass with no
// whole-document buffering, preserving streaming staging over the same bytes.
package importer

import (
	"errors"
	"io"
	"unicode/utf8"
)

var errInvalidContextBytes = errors.New("invalid context bytes")

// validatedContextReader enforces a declared media type: UTF-8 always, and a
// complete single JSON value (plus trailing whitespace) for application/json.
// detected (optional) receives the logical media derived from the complete
// bytes, independent of the declared type, for symmetric retry identity.
func validatedContextReader(r io.Reader, media string, detected *string) io.Reader {
	// The JSON scan always runs so the logical media reflects the complete
	// bytes; requireJSON additionally validates the declared family.
	return &contextValidator{source: r, requireJSON: media == "application/json", jsonMode: true, detected: detected}
}

// detectContextMedia consumes the reader once, validating UTF-8 throughout;
// the media type is application/json only when the complete byte stream is
// exactly one valid JSON value, else text/plain.
func detectContextMedia(r io.Reader) (string, error) {
	validator := &contextValidator{source: r, jsonMode: true}
	if _, err := io.Copy(io.Discard, validator); err != nil {
		return "", err
	}
	if validator.jsonValid {
		return "application/json", nil
	}
	return "text/plain", nil
}

type contextValidator struct {
	source      io.Reader
	requireJSON bool
	jsonMode    bool // keep scanning JSON while not yet failed
	jsonValid   bool
	utf8Tail    []byte
	json        jsonScanner
	detected    *string
}

func (v *contextValidator) Read(out []byte) (int, error) {
	n, err := v.source.Read(out)
	if n > 0 {
		if feedErr := v.feedUTF8(out[:n]); feedErr != nil {
			return n, feedErr
		}
		if v.jsonMode {
			if v.jsonValid {
				if !jsonWhitespace(out[:n]) {
					if v.requireJSON {
						return n, errInvalidContextBytes
					}
					v.jsonValid = false
					v.jsonMode = false
				}
			} else if scanErr := v.json.feed(out[:n]); scanErr != nil {
				if v.requireJSON {
					return n, errInvalidContextBytes
				}
				v.jsonMode = false
			} else if v.json.finished() {
				v.jsonValid = true
			}
		}
	}
	if errors.Is(err, io.EOF) {
		if tailErr := v.finishUTF8(); tailErr != nil {
			return n, tailErr
		}
		if v.jsonMode && !v.jsonValid {
			if finishErr := v.json.finish(); finishErr != nil {
				v.jsonMode = false
			} else if v.json.finished() {
				v.jsonValid = true
			}
		}
		if v.requireJSON && !v.jsonValid {
			return n, errInvalidContextBytes
		}
	}
	if v.detected != nil {
		*v.detected = "text/plain"
		if v.jsonValid {
			*v.detected = "application/json"
		}
	}
	return n, err
}

func (v *contextValidator) feedUTF8(chunk []byte) error {
	buffer := chunk
	if len(v.utf8Tail) > 0 {
		buffer = append(v.utf8Tail, chunk...)
		v.utf8Tail = v.utf8Tail[:0]
	}
	for len(buffer) > 0 {
		r, size := utf8.DecodeRune(buffer)
		if r == utf8.RuneError && size == 1 {
			if len(buffer) < utf8.UTFMax {
				v.utf8Tail = append(v.utf8Tail, buffer...)
				return nil
			}
			return errInvalidContextBytes
		}
		buffer = buffer[size:]
	}
	return nil
}

func (v *contextValidator) finishUTF8() error {
	if len(v.utf8Tail) == 0 {
		return nil
	}
	r, size := utf8.DecodeRune(v.utf8Tail)
	if r == utf8.RuneError && size == 1 {
		return errInvalidContextBytes
	}
	return nil
}

func jsonWhitespace(chunk []byte) bool {
	for _, b := range chunk {
		switch b {
		case ' ', '\t', '\n', '\r':
		default:
			return false
		}
	}
	return true
}

const (
	mValue = iota
	mElem
	mKey
	mKeyAfter
	mColon
	mObjAfter
	mArrAfter
	mTop
	mString
	mNumber
	mLiteral
)

const (
	nStart = iota
	nNeg
	nZero
	nInt
	nFrac
	nFracDigit
	nExp
	nExpSign
	nExpDigit
)

// jsonScanner validates exactly one JSON value followed only by whitespace,
// tracking grammar position and nesting without buffering values.
type jsonScanner struct {
	frames    []byte // 'o' object / 'a' array
	mode      int
	inString  bool
	stringKey bool
	escape    bool
	unicode   int
	literal   string
	literalAt int
	number    int
}

func (s *jsonScanner) finished() bool { return s.mode == mTop }

// finish terminates an in-progress number at EOF; a bare top-level number
// with no trailing whitespace stays valid JSON.
func (s *jsonScanner) finish() error {
	if s.mode == mNumber {
		if err := s.endNumber(); err != nil {
			return err
		}
	}
	return nil
}

func (s *jsonScanner) valueDone() {
	if n := len(s.frames); n == 0 {
		s.mode = mTop
	} else if s.frames[n-1] == 'a' {
		s.mode = mArrAfter
	} else {
		s.mode = mObjAfter
	}
}

func (s *jsonScanner) feed(chunk []byte) error {
	for i := 0; i < len(chunk); i++ {
		b := chunk[i]
		if s.inString {
			if err := s.feedString(b); err != nil {
				return err
			}
			continue
		}
		if s.mode == mNumber {
			if b >= '0' && b <= '9' || b == '.' || b == 'e' || b == 'E' || b == '+' || b == '-' {
				if err := s.feedNumber(b); err != nil {
					return err
				}
				continue
			}
			if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
				if err := s.endNumber(); err != nil {
					return err
				}
				continue
			}
			if err := s.terminateNumber(b); err != nil {
				return err
			}
			continue
		}
		if s.mode == mLiteral {
			if err := s.feedLiteral(b); err != nil {
				return err
			}
			continue
		}
		switch b {
		case ' ', '\t', '\n', '\r':
			// Whitespace is insignificant between tokens.
		case '"':
			if s.mode != mValue && s.mode != mElem && s.mode != mKey && s.mode != mKeyAfter {
				return errInvalidContextBytes
			}
			s.inString = true
			s.stringKey = s.mode == mKey || s.mode == mKeyAfter
			s.mode = mString
		case '{':
			if s.mode != mValue && s.mode != mElem {
				return errInvalidContextBytes
			}
			s.frames = append(s.frames, 'o')
			s.mode = mKey
		case '[':
			if s.mode != mValue && s.mode != mElem {
				return errInvalidContextBytes
			}
			s.frames = append(s.frames, 'a')
			s.mode = mElem
		case '}':
			// Only the initial key position (an empty object) or a completed
			// member may close an object; a trailing comma waits for a key.
			if s.mode != mKey && s.mode != mObjAfter {
				return errInvalidContextBytes
			}
			n := len(s.frames)
			if n == 0 || s.frames[n-1] != 'o' {
				return errInvalidContextBytes
			}
			s.frames = s.frames[:n-1]
			s.valueDone()
		case ']':
			if s.mode != mElem && s.mode != mArrAfter {
				return errInvalidContextBytes
			}
			n := len(s.frames)
			if n == 0 || s.frames[n-1] != 'a' {
				return errInvalidContextBytes
			}
			s.frames = s.frames[:n-1]
			s.valueDone()
		case ',':
			switch s.mode {
			case mObjAfter:
				s.mode = mKeyAfter
			case mArrAfter:
				s.mode = mValue
			default:
				return errInvalidContextBytes
			}
		case ':':
			if s.mode != mColon {
				return errInvalidContextBytes
			}
			s.mode = mValue
		case 't':
			if err := s.beginLiteral("true", b); err != nil {
				return err
			}
		case 'f':
			if err := s.beginLiteral("false", b); err != nil {
				return err
			}
		case 'n':
			if err := s.beginLiteral("null", b); err != nil {
				return err
			}
		case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			if s.mode != mValue && s.mode != mElem {
				return errInvalidContextBytes
			}
			s.mode = mNumber
			s.number = nStart
			if err := s.feedNumber(b); err != nil {
				return err
			}
		default:
			return errInvalidContextBytes
		}
	}
	return nil
}

func (s *jsonScanner) feedString(b byte) error {
	if s.escape {
		s.escape = false
		switch b {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			return nil
		case 'u':
			s.unicode = 4
			return nil
		default:
			return errInvalidContextBytes
		}
	}
	if s.unicode > 0 {
		if b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F' {
			s.unicode--
			return nil
		}
		return errInvalidContextBytes
	}
	switch b {
	case '\\':
		s.escape = true
		return nil
	case '"':
		s.inString = false
		if s.stringKey {
			s.stringKey = false
			s.mode = mColon
			return nil
		}
		s.valueDone()
	default:
		if b < 0x20 {
			return errInvalidContextBytes
		}
	}
	return nil
}

func (s *jsonScanner) beginLiteral(word string, b byte) error {
	if s.mode != mValue && s.mode != mElem {
		return errInvalidContextBytes
	}
	s.mode = mLiteral
	s.literal = word
	s.literalAt = 0
	return s.feedLiteral(b)
}

func (s *jsonScanner) feedLiteral(b byte) error {
	if s.literalAt >= len(s.literal) || b != s.literal[s.literalAt] {
		return errInvalidContextBytes
	}
	s.literalAt++
	if s.literalAt == len(s.literal) {
		s.valueDone()
	}
	return nil
}

func (s *jsonScanner) feedNumber(b byte) error {
	switch s.number {
	case nStart:
		if b == '-' {
			s.number = nNeg
			return nil
		}
		if b == '0' {
			s.number = nZero
			return nil
		}
		if b >= '1' && b <= '9' {
			s.number = nInt
			return nil
		}
		return errInvalidContextBytes
	case nNeg:
		if b == '0' {
			s.number = nZero
			return nil
		}
		if b >= '1' && b <= '9' {
			s.number = nInt
			return nil
		}
		return errInvalidContextBytes
	case nZero:
		switch b {
		case '.':
			s.number = nFrac
			return nil
		case 'e', 'E':
			s.number = nExp
			return nil
		}
		return s.terminateNumber(b)
	case nInt:
		if b >= '0' && b <= '9' {
			return nil
		}
		switch b {
		case '.':
			s.number = nFrac
			return nil
		case 'e', 'E':
			s.number = nExp
			return nil
		}
		return s.terminateNumber(b)
	case nFrac:
		if b >= '0' && b <= '9' {
			s.number = nFracDigit
			return nil
		}
		return errInvalidContextBytes
	case nFracDigit:
		if b >= '0' && b <= '9' {
			return nil
		}
		switch b {
		case 'e', 'E':
			s.number = nExp
			return nil
		}
		return s.terminateNumber(b)
	case nExp:
		if b == '+' || b == '-' {
			s.number = nExpSign
			return nil
		}
		if b >= '0' && b <= '9' {
			s.number = nExpDigit
			return nil
		}
		return errInvalidContextBytes
	case nExpSign:
		if b >= '0' && b <= '9' {
			s.number = nExpDigit
			return nil
		}
		return errInvalidContextBytes
	case nExpDigit:
		if b >= '0' && b <= '9' {
			return nil
		}
		return s.terminateNumber(b)
	}
	return errInvalidContextBytes
}

// terminateNumber ends a complete number and reprocesses the byte
// structurally.
func (s *jsonScanner) terminateNumber(b byte) error {
	if err := s.endNumber(); err != nil {
		return err
	}
	return s.feed([]byte{b})
}

func (s *jsonScanner) endNumber() error {
	switch s.number {
	case nStart, nNeg, nFrac, nExp, nExpSign:
		return errInvalidContextBytes
	}
	s.valueDone()
	return nil
}
