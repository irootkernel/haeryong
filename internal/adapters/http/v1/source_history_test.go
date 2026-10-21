package v1

import (
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("keeps both source verification history aliases GET-only without changing other HEAD reads", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	upload := f.create()
	f.put(upload, "page.html", "file", []byte("<html>history</html>"))
	version := f.publish(upload, map[string]any{
		"sources":  []any{map[string]any{"id": "source", "kind": "snapshot", "retention_mode": "reference_only", "snapshot_sha256": strings.Repeat("a", 64)}},
		"evidence": []any{map[string]any{"id": "simple", "source_id": "source", "capture_status": "reference_only", "path": "page.html"}},
	})
	handler := New(f.query, nil, nil)
	for _, path := range []string{
		"/api/v1/versions/" + version.ID + "/source-evidence-verifications?evidence_id=simple",
		"/api/v1/versions/" + version.ID + "/source-evidence/simple/verifications",
	} {
		for _, method := range []string{"GET", "HEAD"} {
			request := httptest.NewRequest(method, path, nil)
			request.Header.Set("Authorization", "Bearer "+f.token)
			reply := httptest.NewRecorder()
			handler.ServeHTTP(reply, request)
			if method == "HEAD" {
				Expect(handler.Handles(request)).To(BeFalse())
				Expect(reply.Code).To(Equal(404))
				Expect(reply.Body.Len()).To(BeZero())
			} else {
				Expect(handler.Handles(request)).To(BeTrue())
				Expect(reply.Code).To(Equal(200), reply.Body.String())
				Expect(reply.Body.String()).To(ContainSubstring(`"items":[]`))
			}
		}
	}
	request := httptest.NewRequest("HEAD", "/api/v1/capabilities", nil)
	request.Header.Set("Authorization", "Bearer "+f.token)
	reply := httptest.NewRecorder()
	handler.ServeHTTP(reply, request)
	Expect(handler.Handles(request)).To(BeTrue())
	Expect(reply.Code).To(Equal(200))
	Expect(reply.Body.Len()).To(BeZero())
})
