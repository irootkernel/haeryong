package auth

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("Personal write lifecycle exceptions are limited to exact mutation routes", Label("unit"), func() {
	id := "11111111-1111-4111-8111-111111111111"
	for _, r := range []struct{ method, path string }{
		{"POST", "/api/v1/versions/" + id + "/annotations"}, {"PATCH", "/api/v1/annotations/" + id}, {"DELETE", "/api/v1/annotations/" + id}, {"PUT", "/api/v1/documents/" + id + "/revisit"},
		{"PUT", "/api/v1/versions/" + id + "/review"}, {"PUT", "/api/v1/versions/" + id + "/reader-preference"}, {"PUT", "/api/v1/versions/" + id + "/reading-position"}, {"PUT", "/api/v1/versions/" + id + "/page-position?page=hello.html"},
	} {
		Expect(personalWriteTarget(r.method, r.path)).To(BeTrue(), r.method+" "+r.path)
	}
	for _, r := range []struct{ method, path string }{
		{"GET", "/api/v1/annotations/" + id}, {"HEAD", "/api/v1/annotations/" + id}, {"GET", "/api/v1/documents/" + id + "/revisit"}, {"GET", "/api/v1/versions/" + id + "/annotations"}, {"GET", "/api/v1/operations/" + id},
		{"PATCH", "/api/v1/annotations/" + id + "/history"}, {"PUT", "/api/v1/annotations/" + id}, {"POST", "/api/v1/versions/not-an-id/annotations"}, {"DELETE", "/api/v1/documents/" + id + "/revisit"},
	} {
		Expect(personalWriteTarget(r.method, r.path)).To(BeFalse(), r.method+" "+r.path)
	}
})
