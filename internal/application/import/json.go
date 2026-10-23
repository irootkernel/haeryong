package importer

import (
	"bufio"
	"errors"
	"haeryong.local/haeryong/internal/application/jsonstream"
	"io"
)

// CopyJSONValue retains importer error identity around the shared streaming grammar.
func CopyJSONValue(r *bufio.Reader, out io.Writer) error {
	err := jsonstream.CopyJSONValue(r, out)
	if errors.Is(err, jsonstream.ErrInvalid) {
		return ErrPackageInvalid
	}
	return err
}
