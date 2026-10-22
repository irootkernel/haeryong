package v1

import (
	"context"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"net/http/httptest"
	"time"
)

var _ = Describe("search HTTP contract", Label("unit"), func() {
	It("SC08 validates search-only empty q and closed parameters", func() {
		for _, path := range []string{"/api/v1/search", "/api/v1/search?q=", "/api/v1/search?q=%09%0A"} {
			_, e := searchParameters(httptest.NewRequest("GET", path, nil))
			Expect(e).NotTo(HaveOccurred())
		}
		for _, path := range []string{"/api/v1/search?q=x&q=y", "/api/v1/search?limit=0", "/api/v1/search?q=%ff", "/api/v1/search?q=%00", "/api/v1/search?kind=", "/api/v1/search?x=y"} {
			_, e := searchParameters(httptest.NewRequest("GET", path, nil))
			Expect(e).To(HaveOccurred())
		}
		_, e := parameters(httptest.NewRequest("GET", "/api/v1/documents?cursor=", nil), []string{"cursor"})
		Expect(e).To(HaveOccurred())
	})
	It("SC08 HEAD success and failure guards suppress every response byte", func() {
		for _, method := range []string{"GET", "HEAD"} {
			r := httptest.NewRequest(method, "/api/v1/search", nil)
			w := httptest.NewRecorder()
			respond(w, r, 200, map[string]string{"text": "content"})
			if method == "HEAD" {
				Expect(w.Body.Len()).To(BeZero())
			} else {
				Expect(w.Body.String()).To(ContainSubstring("content"))
			}
			w = httptest.NewRecorder()
			failure(w, r, &query.Error{Reason: "invalid_query"})
			Expect(w.Code).To(Equal(400))
			if method == "HEAD" {
				Expect(w.Body.Len()).To(BeZero())
			} else {
				Expect(w.Body.String()).To(ContainSubstring("invalid_query"))
			}
		}
	})
	It("SC08 search timeout and storage failures are 503 with Retry-After and bodyless HEAD", func() {
		for _, cause := range []error{context.DeadlineExceeded, errors.New("private database error")} {
			for _, method := range []string{"GET", "HEAD"} {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(method, "/api/v1/search", nil)
				searchFailure(w, r, &query.SearchUnavailable{Cause: cause})
				Expect(w.Code).To(Equal(503))
				Expect(w.Header().Get("Retry-After")).To(Equal("2"))
				initial := httptest.NewRecorder()
				failure(initial, r, cause)
				Expect(initial.Code).To(Equal(503))
				Expect(initial.Header().Get("Retry-After")).To(Equal("2"))
				if method == "HEAD" {
					Expect(initial.Body.Len()).To(BeZero())
				} else {
					Expect(initial.Body.String()).To(ContainSubstring("SERVICE_RECOVERING"))
				}
				if method == "HEAD" {
					Expect(w.Body.Len()).To(BeZero())
				} else {
					Expect(w.Body.String()).To(ContainSubstring(`"retryable":true`))
					Expect(w.Body.String()).NotTo(ContainSubstring("private database error"))
					if cause == context.DeadlineExceeded {
						Expect(w.Body.String()).To(ContainSubstring(`"reason":"query_timeout"`))
					}
				}
			}
		}
	})
})

type firstSearchAuth struct {
	app.Store
	deadline time.Time
	seen     bool
}

func (s *firstSearchAuth) Write(ctx context.Context, _ func(app.WriteTx) error) error {
	s.deadline, s.seen = ctx.Deadline()
	return context.DeadlineExceeded
}

var _ = It("H2 applies the search deadline before the first authentication", Label("unit"), func() {
	store := &firstSearchAuth{}
	h := New(query.New(store, appauth.New(store), nil), nil, nil)
	r := httptest.NewRequest("GET", "/api/v1/search?q=needle", nil)
	r.Header.Set("Authorization", "Bearer synthetic-owner")
	w := httptest.NewRecorder()
	before := time.Now()
	h.ServeHTTP(w, r)
	Expect(store.seen).To(BeTrue())
	Expect(store.deadline.Sub(before)).To(BeNumerically(">", 0))
	Expect(time.Until(store.deadline)).To(BeNumerically("<=", 30*time.Second))
	Expect(w.Code).To(Equal(503))
	Expect(w.Body.String()).To(ContainSubstring(`"reason":"query_timeout"`))
})
