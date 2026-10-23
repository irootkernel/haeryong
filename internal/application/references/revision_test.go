package references

import (
	"crypto/sha256"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/domain"
	"os"
	"path/filepath"
)

var _ = It("RF07-REV pins the search/anchor/parser mapping to referenceRevision (RV-COUPLING)", Label("unit"), func() {
	approved := map[int64]struct {
		search int64
		digest string
	}{1: {2, "fb4cfbe4c18ee2b158692ccf843ee354e4c00ff019262c59b49d7892c372c767"}, 2: {2, "85b7011db1b11bd0add1c440a7c6e3a5e9ba3e6e3121ab1f938ba4a36984fcb2"}, 3: {2, "8aef2bb9d031ec681fe76d09c6b4fab4869ea9a725a849aff5095cae7e9d50cb"}}
	pin, ok := approved[domain.ReferenceRevision]
	Expect(ok).To(BeTrue(), "a new reference revision needs its reviewed coupling entry")
	h := sha256.New()
	for _, p := range []string{"internal/application/search/extract.go", "internal/application/search/element_stack.go", "internal/application/search/normalize.go", "internal/application/referencecodec/decoder.go", "internal/application/references/manifest.go", "internal/application/jsonstream/json.go", "internal/domain/html.go"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(p)))
		Expect(err).NotTo(HaveOccurred())
		h.Write([]byte(p + "\x00"))
		h.Write(raw)
	}
	Expect(domain.SearchExtractorRevision).To(Equal(pin.search), "advance referenceRevision with extractor changes")
	Expect(fmt.Sprintf("%x", h.Sum(nil))).To(Equal(pin.digest), "advance referenceRevision with anchor/parser changes")
})
