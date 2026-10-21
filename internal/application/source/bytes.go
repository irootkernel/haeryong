// Package source owns project registration, the exact-revision source reader
// use cases, imported-evidence verification and the shared eligibility rule.
package source

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
)

// ErrLineRangeOutOfBounds marks an authored line range that exceeds the actual
// line count of the target bytes: a typed fact about the target, not a request
// error.
var ErrLineRangeOutOfBounds = errors.New("line range out of bounds")

// MaxInlineExcerpt bounds the inline expected_excerpt request field. It is a
// transport bound on one request field, never an author content quota; larger
// expectations use the sealed expected_excerpt_ref form.
const MaxInlineExcerpt = 64 * 1024

// BoundedResult carries the §6 exact-bytes selection over one blob stream.
type BoundedResult struct {
	// SHA is the SHA-256 of exactly the selected bounded bytes.
	SHA string
	// Bounded is the selected byte count.
	Bounded int64
	// Lines is the blob's total line count under the §6 line model.
	Lines int64
	// Buffered holds selected bytes when a comparison cap was supplied,
	// truncated to cap+1 bytes.
	Buffered []byte
	// Overflow marks Buffered having exceeded the comparison cap.
	Overflow bool
}

// boundedSelector streams the §6 line model over a blob: a line is a maximal
// byte sequence terminated by LF (0x0A); bytes after the final LF form a last
// line without terminator; empty lines are zero-byte lines; CR (0x0D) is
// ordinary line content. start==0 selects the complete byte stream; otherwise
// the inclusive 1-based line range [start,end] selects whole lines including
// their terminating LF when present.
type boundedSelector struct {
	start, end int64
	lfCount    int64
	count      int64
	afterLast  int64
	hasher     hash.Hash
	cap        int64
	buffered   []byte
	overflow   bool
}

// SelectBounded hashes the bounded bytes of r under the §6 model. compareCap
// > 0 additionally collects the bounded bytes (capped at compareCap+1) for
// exact byte-equality checks.
func SelectBounded(r io.Reader, start, end int64, compareCap int64) (BoundedResult, error) {
	if start != 0 && start > end {
		return BoundedResult{}, ErrLineRangeOutOfBounds
	}
	s := &boundedSelector{start: start, end: end, hasher: sha256.New(), cap: compareCap}
	if _, err := io.Copy(s, r); err != nil {
		return BoundedResult{}, err
	}
	lines := s.totalLines()
	if start != 0 && (lines < start || lines < end) {
		return BoundedResult{Lines: lines}, ErrLineRangeOutOfBounds
	}
	return BoundedResult{SHA: hex.EncodeToString(s.hasher.Sum(nil)), Bounded: s.count, Lines: lines, Buffered: s.buffered, Overflow: s.overflow}, nil
}

func (s *boundedSelector) currentLine() int64 { return s.lfCount + 1 }

func (s *boundedSelector) selected() bool {
	return s.start == 0 || (s.currentLine() >= s.start && s.currentLine() <= s.end)
}

func (s *boundedSelector) Write(p []byte) (int, error) {
	offset := 0
	for offset < len(p) {
		// Advance in whole spans, stopping after the next line
		// terminator so the LF stays with its own line.
		next := -1
		for i := offset; i < len(p); i++ {
			if p[i] == '\n' {
				next = i
				break
			}
		}
		end := len(p)
		if next >= 0 {
			end = next + 1
		}
		if s.selected() {
			span := p[offset:end]
			s.count += int64(len(span))
			if _, err := s.hasher.Write(span); err != nil {
				return 0, err
			}
			if s.cap > 0 && !s.overflow {
				room := s.cap + 1 - int64(len(s.buffered))
				if int64(len(span)) > room {
					if room > 0 {
						s.buffered = append(s.buffered, span[:room]...)
					}
					s.overflow = true
				} else {
					s.buffered = append(s.buffered, span...)
				}
			}
		}
		if next >= 0 {
			s.lfCount++
			s.afterLast = 0
		} else {
			s.afterLast += int64(end - offset)
		}
		offset = end
	}
	return len(p), nil
}

// totalLines counts a trailing unterminated final line even when unselected.
func (s *boundedSelector) totalLines() int64 {
	if s.afterLast > 0 {
		return s.lfCount + 1
	}
	return s.lfCount
}

// ExcerptMatches reports exact byte equality of the bounded bytes against
// the supplied expectation without any normalization. A comparison overflow
// alone proves inequality, and the bounded byte count must equal the
// expectation length so an empty expectation never matches non-empty bytes
// (SN3).
func ExcerptMatches(result BoundedResult, expected []byte) bool {
	if result.Overflow || result.Bounded != int64(len(expected)) || len(result.Buffered) != len(expected) {
		return false
	}
	for i := range expected {
		if result.Buffered[i] != expected[i] {
			return false
		}
	}
	return true
}
