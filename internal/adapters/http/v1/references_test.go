package v1

import (
	"context"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/query"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"net/http/httptest"
	"time"
)

var _ = Describe("reference HTTP contract", Label("unit"), func() {
	It("RF08 routes additive reads and rejects closed parameters before dispatch", func() {
		h := New(nil, nil, nil)
		base := "/api/v1/versions/11111111-1111-4111-8111-111111111111/references"
		for _, method := range []string{"GET", "HEAD"} {
			Expect(h.Handles(httptest.NewRequest(method, base, nil))).To(BeTrue())
			Expect(h.Handles(httptest.NewRequest(method, base+"/resolve", nil))).To(BeTrue())
			for _, suffix := range []string{"?cursor=", "?limit=201", "?limit=0", "?limit=1&limit=2", "?target=x", "/resolve?reference_id=a&reference_id=b", "/resolve?target=x", "/resolve?reference_id=%zz"} {
				r := httptest.NewRequest(method, base+suffix, nil)
				w := httptest.NewRecorder()
				h.references(w, r, "unused", route(r))
				Expect(w.Code).To(Equal(400))
				if method == "HEAD" {
					Expect(w.Body.Len()).To(BeZero())
				} else {
					Expect(w.Body.String()).To(ContainSubstring("pointer"))
				}
			}
		}
		for _, method := range []string{"POST", "PUT", "DELETE"} {
			Expect(h.Handles(httptest.NewRequest(method, base+"/resolve", nil))).To(BeFalse())
		}
	})
	It("RF08 keeps every HEAD failure bodyless and nondisclosing", func() {
		for _, err := range []error{&query.ReferenceInvalid{Pointer: "/cursor", Reason: "cursor_mismatch"}, sec.ErrRequired, sec.ErrExpired, sec.ErrRevoked, domain.ErrDenied, domain.ErrNotFound, &query.TargetGone{Target: query.GoneTarget{DocumentID: "d", VersionID: "v"}}, &query.ReferenceUnavailable{Cause: context.DeadlineExceeded}, &query.ReferenceUnavailable{Cause: errors.New("private SQL")}} {
			for _, method := range []string{"GET", "HEAD"} {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(method, "/api/v1/versions/v/references", nil)
				failure(w, r, err)
				if method == "HEAD" {
					Expect(w.Body.Len()).To(BeZero())
				} else {
					Expect(w.Body.String()).NotTo(ContainSubstring("private SQL"))
				}
				if w.Code == 503 {
					Expect(w.Header().Get("Retry-After")).To(Equal("2"))
					if method == "GET" && errors.Is(err, context.DeadlineExceeded) {
						Expect(w.Body.String()).To(ContainSubstring("query_timeout"))
					}
				}
			}
		}
	})
	It("RF08 starts the reference deadline before first product authentication", func() {
		store := &firstSearchAuth{}
		h := New(query.New(store, appauth.New(store), nil), nil, nil)
		r := httptest.NewRequest("GET", "/api/v1/versions/v/references", nil)
		r.Header.Set("Authorization", "Bearer synthetic-owner")
		w := httptest.NewRecorder()
		before := time.Now()
		h.ServeHTTP(w, r)
		Expect(store.seen).To(BeTrue())
		Expect(store.deadline.Sub(before)).To(BeNumerically(">", 0))
		Expect(time.Until(store.deadline)).To(BeNumerically("<=", 30*time.Second))
		Expect(w.Code).To(Equal(503))
		Expect(w.Body.String()).To(ContainSubstring("query_timeout"))
	})
})
