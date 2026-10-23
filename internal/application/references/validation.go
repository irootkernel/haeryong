package references

import (
	"context"
	app "haeryong.local/haeryong/internal/application/storage"
	"io"
)

// WalkDeclarations reuses the materializer's bounded immutable-manifest parser.
// Consumers run outside the serialized store gate.
func WalkDeclarations(ctx context.Context, reader io.ReadSeeker, version string, visit func(app.PreparedReference) error) error {
	var position manifestPosition
	for {
		items, next, done, err := manifestBatch(ctx, reader, version, position)
		if err != nil {
			return err
		}
		for _, item := range items {
			if err = visit(item.prepared); err != nil {
				return err
			}
		}
		if done {
			return nil
		}
		position = next
	}
}
