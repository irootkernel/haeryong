package query

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/domain"
	"net/url"
	"strings"
)

var _ = Describe("reference projections", Label("unit"), func() {
	It("RF08 exposes pending/failed honestly and requires the current manifest/revision", func() {
		v := domain.Version{ManifestSHA: "manifest"}
		for _, state := range []string{"", "building", "ready", "failed"} {
			m := domain.ReferenceMaterialization{State: state, ManifestSHA: v.ManifestSHA, ReferenceRevision: domain.ReferenceRevision}
			got := referenceState(m, v)
			if state == "ready" {
				Expect(got.State).To(Equal("ready"))
				Expect(got.Reason).To(BeNil())
			} else if state == "failed" {
				Expect(*got.Reason).To(Equal("materialization_failed"))
			} else {
				Expect(*got.Reason).To(Equal("materialization_pending"))
			}
			m.ReferenceRevision++
			Expect(referenceState(m, v).State).To(Equal("pending"))
			m.ReferenceRevision--
			m.ManifestSHA = "other"
			Expect(referenceState(m, v).State).To(Equal("pending"))
		}
	})
	It("RF05 exact Reader URLs encode artifact navigation once and keep fallback region-free", func() {
		page, region := "한글/%2F +.html", "선택 +%/"
		t := ExactTarget{DocumentID: "d", VersionID: "v", PagePath: &page, ResolvedPath: &page, RegionID: &region, PageQuery: "q=+%2F", PageFragment: "artifact#fragment"}
		for _, resolved := range []bool{false, true} {
			raw := referenceReaderURL(t, resolved)
			parsed, err := url.Parse(*raw)
			Expect(err).NotTo(HaveOccurred())
			Expect(parsed.Path).To(Equal("/documents/d/versions/v"))
			Expect(parsed.Query().Get("page")).To(Equal(page))
			Expect(parsed.Query().Get("page_query")).To(Equal(t.PageQuery))
			Expect(parsed.Query().Get("page_fragment")).To(Equal(t.PageFragment))
			if resolved {
				value, err := url.QueryUnescape(strings.TrimPrefix(strings.SplitN(*raw, "#", 2)[1], "region="))
				Expect(err).NotTo(HaveOccurred())
				Expect(value).To(Equal(region))
			} else {
				Expect(parsed.Fragment).To(BeEmpty())
			}
		}
	})
})
