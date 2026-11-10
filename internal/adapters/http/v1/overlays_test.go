package v1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http/httptest"
)

var _ = It("T00804 management reads reject unknown, duplicate and empty query fields before query dispatch", Label("integration"), func() {
	h := New(nil, nil, nil)
	for _, base := range []string{"/api/v1/versions/11111111-1111-4111-8111-111111111111/related", "/api/v1/topic-assignments"} {
		for _, method := range []string{"GET", "HEAD"} {
			Expect(h.Handles(httptest.NewRequest(method, base, nil))).To(BeTrue())
			for _, suffix := range []string{"?unknown=private", "?cursor=", "?limit=0", "?limit=201", "?limit=1&limit=2", "?page_path=a&page_path=b", "?region_id=", "?page_path=%zz"} {
				r := httptest.NewRequest(method, base+suffix, nil)
				w := httptest.NewRecorder()
				h.management(w, r, "unused", route(r))
				Expect(w.Code).To(Equal(400), suffix)
				if method == "HEAD" {
					Expect(w.Body.Len()).To(BeZero())
				}
			}
		}
		for _, method := range []string{"POST", "PUT", "DELETE"} {
			Expect(h.Handles(httptest.NewRequest(method, base, nil))).To(BeFalse())
		}
	}
	for _, suffix := range []string{"?document_id=d", "?version_id=v"} {
		r := httptest.NewRequest("GET", "/api/v1/versions/11111111-1111-4111-8111-111111111111/related"+suffix, nil)
		w := httptest.NewRecorder()
		h.management(w, r, "unused", route(r))
		Expect(w.Code).To(Equal(400))
	}
})
