package search

import (
	"context"
	"io"
	"os"
)

// InspectMetadata reuses the extractor's revisioned windows without writing
// derived state. Empty text legitimately requires no metadata chunk.
func InspectMetadata(ctx context.Context, text string, emit func(int) error) error {
	return metadataWindows(ctx, text, func(n int, _, _ string) error { return emit(n) })
}

// InspectPage streams the same body/display ordinals and static anchors as the
// worker. Parsing and private spool IO stay outside the store gate; this does
// not certify FTS internals or change the extractor's revisioned output.
func InspectPage(ctx context.Context, input io.Reader, emit func(string, int) error) (Extraction, error) {
	spool, err := os.CreateTemp("", "haeryong-search-validation-*")
	if err != nil {
		return Extraction{}, err
	}
	if err = os.Remove(spool.Name()); err != nil {
		spool.Close()
		return Extraction{}, err
	}
	defer spool.Close()
	result, err := Extract(ctx, input, "text/html", spool, func(f Fragment) error {
		if f.Chunk.Ordinal == 0 {
			if err := displayWindows(ctx, io.NewSectionReader(spool, f.DisplayStart, f.DisplayEnd-f.DisplayStart), func(n int, _ string) error {
				return emit(f.AnchorID, 2*n+1)
			}); err != nil {
				return err
			}
		}
		return emit(f.AnchorID, 2*f.Chunk.Ordinal)
	})
	if err == nil && !result.Complete {
		err = io.ErrUnexpectedEOF
	}
	return result, err
}
