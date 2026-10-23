package importer_test

import (
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/domain"
	"strings"
)

const referenceDocument = "11111111-1111-4111-8111-111111111111"
const referenceVersion = "22222222-2222-4222-8222-222222222222"

var _ = Describe("RF01 reference decoder and identity", Label("unit"), func() {
	It("preserves legacy and enriched declarations without a page/region inventory (RV-FIELDS: optional schema fields/DecodeReference normalization)", func() {
		value := map[string]any{"id": "../참조/%2F", "document_id": referenceDocument, "version_id": referenceVersion, "page_path": nil, "region_id": nil, "resolution": "resolved"}
		raw, _ := json.Marshal(value)
		legacy, err := importer.DecodeReference(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(legacy.TargetPagePath).To(BeNil())
		Expect(legacy.FromPagePath).To(BeNil())
		Expect(legacy.PageQuery).To(BeEmpty())
		Expect(legacy.AuthoredJSON).To(Equal(raw))
		value["page_path"] = "설명/cafe\u0301 %2F +.html"
		value["region_id"] = "retry"
		value["from"] = map[string]any{"page_path": "source.html", "region_id": "retry"}
		value["relation"] = "explanatory analogy"
		value["page_query"] = "q=한글 +%2F"
		value["page_fragment"] = "artifact#fragment"
		value["label"] = "Authored label"
		raw, _ = json.MarshalIndent(value, "", "  ")
		decoded, err := importer.DecodeReference(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(*decoded.FromPagePath).To(Equal("source.html"))
		Expect(*decoded.FromRegionID).To(Equal("retry"))
		Expect(*decoded.TargetPagePath).To(Equal(value["page_path"]))
		Expect(*decoded.Relation).To(Equal("explanatory analogy"))
		Expect(decoded.PageQuery).To(Equal(value["page_query"]))
		Expect(decoded.PageFragment).To(Equal(value["page_fragment"]))
		Expect(decoded.AuthoredJSON).To(Equal(raw))
		raw[0] = '['
		Expect(decoded.AuthoredJSON[0]).To(Equal(byte('{')))
		// A source page does not need a region; null differs from omitted required key.
		value["from"] = map[string]any{"page_path": "source.html", "region_id": nil}
		raw, _ = json.Marshal(value)
		decoded, err = importer.DecodeReference(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded.FromRegionID).To(BeNil())
	})
	It("rejects closed/null/duplicate/malformed supplied fields with field pointers (RV-CLOSED: referenceShape closed and null checks)", func() {
		base := `{"id":"r","document_id":"` + referenceDocument + `","version_id":"` + referenceVersion + `","page_path":null,"region_id":null`
		for _, test := range []struct{ suffix, pointer string }{
			{`,"from":null}`, "/references/from"},
			{`,"from":[]}`, "/references/from"}, {`,"from":{}}`, "/references/from/page_path"},
			{`,"from":{"page_path":"p.html"}}`, "/references/from/region_id"},
			{`,"from":{"page_path":null,"region_id":null}}`, "/references/from/page_path"},
			{`,"from":{"page_path":"../p","region_id":null}}`, "/references/from/page_path"},
			{`,"from":{"page_path":"p","region_id":""}}`, "/references/from/region_id"},
			{`,"from":{"page_path":"p","region_id":null,"latest":true}}`, "/references/from/latest"},
			{`,"relation":null}`, "/references/relation"}, {`,"page_query":null}`, "/references/page_query"},
			{`,"page_fragment":null}`, "/references/page_fragment"}, {`,"latest":true}`, "/references/latest"},
		} {
			_, err := importer.DecodeReference([]byte(base + test.suffix))
			var metadata *importer.MetadataError
			Expect(errors.As(err, &metadata)).To(BeTrue(), test.suffix)
			Expect(metadata.Pointer).To(Equal(test.pointer))
		}
		for _, raw := range []string{base + `,"id":"duplicate"}`, base + `} {}`, strings.Replace(base+`}`, referenceDocument, "not-a-uuid", 1), strings.Replace(base+`}`, `"region_id":null`, `"region_id":"retry"`, 1), `null`, `[]`} {
			_, err := importer.DecodeReference([]byte(raw))
			Expect(errors.Is(err, importer.ErrPackageInvalid)).To(BeTrue(), raw)
		}
	})
	It("derives a stable namespace UUIDv5 over length-prefixed UTF-8 identities (RV-UUID: namespace/length encoding/version bits)", func() {
		id, err := domain.ReferenceEdgeID(referenceDocument, referenceVersion, "../참조/%2F")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal("713c19ff-0dcf-55ee-a35c-f6c7039f41c0"))
		again, err := domain.ReferenceEdgeID(referenceDocument, referenceVersion, "../참조/%2F")
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(id))
		other, err := domain.ReferenceEdgeID(referenceVersion, referenceDocument, "../참조/%2F")
		Expect(err).NotTo(HaveOccurred())
		Expect(other).NotTo(Equal(id))
		for _, parts := range [][3]string{{"bad", referenceVersion, "r"}, {referenceDocument, "bad", "r"}, {referenceDocument, referenceVersion, ""}, {referenceDocument, referenceVersion, string([]byte{255})}} {
			_, err := domain.ReferenceEdgeID(parts[0], parts[1], parts[2])
			Expect(err).To(MatchError(domain.ErrUnsupportedDerivedData))
		}
	})
})

var _ = It("RF01 full Validate and Import preserve nonfirst reference diagnostic indexes (RV-REFERENCE-POINTER)", Label("persistence"), func() {
	t := GinkgoT()
	f := newFixture(t)
	upload := f.sealed(t, nil, "<html>reference diagnostics</html>")
	ref := func(id string) string {
		return `{"id":"` + id + `","document_id":"` + referenceDocument + `","version_id":"` + referenceVersion + `","page_path":null,"region_id":null`
	}
	for _, test := range []struct{ metadata, pointer string }{
		{`{"references":[` + ref("first") + `},` + ref("second") + `,"from":{"page_path":"source.html","region_id":""}},` + ref("third") + `}]}`, "/references/1/from/region_id"},
		{`{"references":[` + ref("first") + `},` + ref("second") + `},` + ref("first") + `}]}`, "/references/2/id"},
	} {
		_, err := f.core.Validate(ctx, f.token, "", importer.ImportRequest{UploadID: upload, Metadata: strings.NewReader(test.metadata)})
		var diagnostic *importer.MetadataError
		Expect(errors.As(err, &diagnostic)).To(BeTrue())
		Expect(diagnostic.Pointer).To(Equal(test.pointer))
		_, err = f.core.Import(ctx, f.token, key(t), importer.ImportRequest{UploadID: upload, DocumentPresent: true, Metadata: strings.NewReader(test.metadata)})
		diagnostic = nil
		Expect(errors.As(err, &diagnostic)).To(BeTrue())
		Expect(diagnostic.Pointer).To(Equal(test.pointer))
	}
})
