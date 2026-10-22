package search

import (
	"context"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Metadata windows use normalized scalar positions. Each scalar keeps its
// original normalization-segment range, so contraction/expansion never loses an
// authored snippet or a boundary phrase. Identity fields are never rewritten.
func metadataWindows(ctx context.Context, text string, emit func(int, string, string) error) error {
	display := strings.Join(strings.Fields(text), " ")
	var iterator norm.Iter
	iterator.InitString(norm.NFC, display)
	literal := make([]rune, 0, ChunkSize)
	starts, ends := make([]int, 0, ChunkSize), make([]int, 0, ChunkSize)
	ordinal, fresh := 0, 0
	flush := func() error {
		return emit(ordinal, string(literal), display[starts[0]:ends[len(ends)-1]])
	}
	fold := cases.Fold()
	for !iterator.Done() {
		if err := ctx.Err(); err != nil {
			return err
		}
		start := iterator.Pos()
		segment := iterator.Next()
		end := iterator.Pos()
		for _, r := range norm.NFC.String(fold.String(string(segment))) {
			literal = append(literal, r)
			starts = append(starts, start)
			ends = append(ends, end)
			fresh++
			if len(literal) == ChunkSize {
				if err := flush(); err != nil {
					return err
				}
				ordinal++
				copy(literal, literal[ChunkSize-ChunkOverlap:])
				literal = literal[:ChunkOverlap]
				copy(starts, starts[ChunkSize-ChunkOverlap:])
				starts = starts[:ChunkOverlap]
				copy(ends, ends[ChunkSize-ChunkOverlap:])
				ends = ends[:ChunkOverlap]
				fresh = 0
			}
		}
	}
	if fresh > 0 {
		return flush()
	}
	return nil
}
