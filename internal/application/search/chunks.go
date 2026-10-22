package search

import (
	"bufio"
	"context"
	"golang.org/x/text/cases"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"strings"
	"unicode"
)

const ChunkSize = domain.SearchChunkSize
const ChunkOverlap = domain.SearchChunkOverlap

type Chunk struct {
	Ordinal int
	Text    string
}

// Chunks streams normalized scalar windows, including the final window. No input
// size ceiling or full-body copy is introduced. emit errors abort extraction.
func Chunks(ctx context.Context, input io.Reader, emit func(Chunk) error) error {
	r := bufio.NewReader(transform.NewReader(input, transform.Chain(norm.NFC, cases.Fold(), norm.NFC)))
	window := make([]rune, 0, ChunkSize)
	ordinal := 0
	pendingSpace := false
	haveText := false
	fresh := 0
	put := func(c rune) error {
		window = append(window, c)
		fresh++
		if len(window) == ChunkSize {
			if err := emit(Chunk{ordinal, string(window)}); err != nil {
				return err
			}
			ordinal++
			copy(window, window[ChunkSize-ChunkOverlap:])
			window = window[:ChunkOverlap]
			fresh = 0
		}
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c, _, err := r.ReadRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if unicode.IsSpace(c) {
			pendingSpace = haveText
			continue
		}
		if pendingSpace {
			if err := put(' '); err != nil {
				return err
			}
			pendingSpace = false
		}
		if err := put(c); err != nil {
			return err
		}
		haveText = true
	}
	if fresh > 0 {
		return emit(Chunk{ordinal, string(window)})
	}
	return nil
}

// ChunkText is a small-input diagnostic; the extractor uses streaming Chunks.
func ChunkText(ctx context.Context, s string) ([]Chunk, error) {
	out := []Chunk{}
	err := Chunks(ctx, strings.NewReader(s), func(c Chunk) error { out = append(out, c); return nil })
	return out, err
}
