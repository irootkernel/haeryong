package sqlite

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var _ = Describe("SC09 exact page-local anchor storage", Label("persistence"), func() {
	It("keeps retry on NFC/NFD paths separate without changing supplied region labels (reversal: page_anchors composite key/PutPageAnchor conflict target)", func() {
		t := GinkgoT()
		_, w := profile(t)
		s := openStore(t, w, nil)
		version, err := searchSeed(s)
		Expect(err).NotTo(HaveOccurred())
		pages := []string{"pages/café.html", "pages/cafe\u0301.html"}
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			for i, path := range pages {
				label := []string{"First", "Second"}[i]
				if e := tx.PutPageAnchor(domain.PageAnchor{VersionID: version, PagePath: path, ID: "retry", LocatorKind: "id", Occurrences: 1, Label: label, TextSHA256: digest([]byte(label))}); e != nil {
					return e
				}
			}
			return nil
		})).To(Succeed())
		Expect(s.Close()).To(Succeed())
		s = openStore(t, w, nil)
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			for i, path := range pages {
				a, e := tx.PageAnchor(version, path, "retry")
				if e != nil {
					return e
				}
				Expect(a.Label).To(Equal([]string{"First", "Second"}[i]))
				Expect(a.PagePath).To(Equal(path))
				Expect(a.TextSHA256).To(Equal(digest([]byte(a.Label))))
			}
			return nil
		})).To(Succeed())
		for _, path := range pages {
			r, e := s.Region(ctx, version, path, "retry")
			Expect(e).NotTo(HaveOccurred())
			Expect(string(r.Metadata)).To(Equal(`{"label":"Supplied region"}`))
		}
	})
})
