package imports

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"io"
	"os"
	"strings"
)

type repeatedByte byte

func (b repeatedByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(b)
	}
	return len(p), nil
}

var _ = It("TestInlineMetadataStreamsExactBytesAboveFormerEnvelope", Label("unit"), func() {
	t := GinkgoT()

	t.Setenv("TMPDIR", t.TempDir())
	const size = 20 * 1024 * 1024
	metadata := func() io.Reader {
		return io.MultiReader(strings.NewReader("{ \n\"title\": \""), io.LimitReader(repeatedByte('x'), size), strings.NewReader("\",\"question\":\"한글\" }"))
	}
	body := io.MultiReader(strings.NewReader(`{"upload_id":"11111111-1111-4111-8111-111111111111","metadata":`), metadata(), strings.NewReader(`,"document_id":null}`))
	input, cleanup, err := importInput(body, false)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	file, ok := input.Metadata.(*os.File)
	if !ok {
		cleanup()
		Fail(fmt.Sprint("metadata was not privately spooled"))
	}
	name := file.Name()
	info, err := file.Stat()
	if err != nil {
		cleanup()
		Fail(fmt.Sprint(err))
	}
	if info.Mode().Perm() != 0600 || info.Size() <= size {
		cleanup()
		Fail(fmt.Sprint("private metadata bytes missing"))
	}
	got, want := sha256.New(), sha256.New()
	if _, err := io.Copy(got, input.Metadata); err != nil {
		cleanup()
		Fail(fmt.Sprint(err))
	}
	_, _ = io.Copy(want, metadata())
	if hex.EncodeToString(got.Sum(nil)) != hex.EncodeToString(want.Sum(nil)) {
		cleanup()
		Fail(fmt.Sprint("metadata exact whitespace/Unicode/bytes changed"))
	}
	if !input.DocumentPresent || input.DocumentID != nil {
		cleanup()
		Fail(fmt.Sprint("explicit null intent lost"))
	}
	cleanup()
	{
		_, err := os.Lstat(name)
		Expect(!os.IsNotExist(err)).To(BeFalse(), func() string { return fmt.Sprint("transport spool not removed") })
	}

})
var _ = It("TestImportControlParsingRejectsAmbiguityAndCleansSpool", Label("unit"), func() {
	t := GinkgoT()

	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	for _, body := range []string{
		`{"upload_id":"u"}`,
		`{"upload_id":"u","document_id":null,"document_id":null}`,
		`{"upload_id":"u","document_id":null,}`,
		`{"upload_id":null,"document_id":null}`,
		`{"upload_id":"u","document_id":{}}`,
		`{"upload_id":"u","document_id":null,"metadata":{},"extra":true}`,
		`{"upload_id":"u","document_id":null,"metadata":{},"metadata_ref":{"object_id":"id","bytes_sha256":"hash","byte_size":"0","media_type":"application/json"}}`,
		`{"upload_id":"u","document_id":null,"metadata_ref":{"object_id":"id","object_id":"other","bytes_sha256":"hash","byte_size":"0","media_type":"application/json"}}`,
		`{"upload_id":"u","document_id":null} false`,
	} {
		_, cleanup, err := importInput(strings.NewReader(body), false)
		cleanup()
		Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprintf("ambiguous input accepted: %s", body) })

		files, err := os.ReadDir(dir)
		Expect(err != nil || len(files) != 0).To(BeFalse(), func() string { return fmt.Sprint("failed parsing left metadata spool") })

	}
})
var _ = It("TestValidationClosedInputAndImportOmissionMarkers", Label("unit"), func() {

	input, cleanup, err := importInput(strings.NewReader(`{"upload_id":"u","document_id":"d","base_version_id":null}`), false)
	defer cleanup()
	Expect(err != nil || !input.DocumentPresent || !input.BasePresent || input.BaseVersionID != nil || input.DocumentID == nil).To(BeFalse(), func() string { return fmt.Sprintf("explicit input lost: %+v %v", input, err) })

	input, cleanup, err = importInput(strings.NewReader(`{"upload_id":"u"}`), true)
	defer cleanup()
	Expect(err != nil || input.DocumentPresent).To(BeFalse(), func() string { return fmt.Sprint("validation required publication intent") })

	_, cleanup, err = importInput(strings.NewReader(`{"upload_id":"u","document_id":null}`), true)
	cleanup()
	Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("validation accepted hidden publication fields") })

})
