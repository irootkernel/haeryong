package query

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/domain"
)

// The export writer must not carry the classic ZIP limits: the contract
// imposes no total byte or entry ceiling, so the shared per-entry writer is
// proven across the ZIP64 entry boundary with cheap zero-byte records.
var _ = It("TestPackageWriterCrossesZip64EntryBoundaryWithoutCaps", Label("unit"), func() {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	const beyond = 65535 + 2
	for i := 0; i < beyond; i++ {
		err := appendEntry(archive, domain.Entry{Path: fmt.Sprintf("entries/%06d", i), Kind: "file", Size: 0}, nil)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	err := appendEntry(archive, domain.Entry{Path: "entries/tail.bin", Kind: "file", Size: 3}, func(w io.Writer) error {
		_, err := w.Write([]byte{1, 2, 3})
		return err
	})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	err = archive.Close()
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	Expect(len(reader.File) != beyond+1).To(BeFalse(), func() string {
		return fmt.Sprintf("writer capped entries below the ZIP64 boundary: %d", len(reader.File))
	})
	last := reader.File[len(reader.File)-1]
	body, err := io.ReadAll(mustOpen(last))
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	Expect(len(body) != 3).To(BeFalse(), func() string { return "tail entry bytes changed across the ZIP64 boundary" })
})

func mustOpen(file *zip.File) io.ReadCloser {
	body, err := file.Open()
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	return body
}
