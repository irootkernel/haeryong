package auth

import (
	"context"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appauth "haeryong.local/haeryong/internal/application/auth"
	app "haeryong.local/haeryong/internal/application/storage"
)

type firstReferenceAuth struct {
	app.Store
	deadline time.Time
	seen     bool
}

func (s *firstReferenceAuth) Write(ctx context.Context, _ func(app.WriteTx) error) error {
	var ok bool
	s.deadline, ok = ctx.Deadline()
	Expect(ok).To(BeTrue())
	s.seen = true
	return context.DeadlineExceeded
}

var _ = It("RF08 starts the reference deadline before the outer auth Guard and suppresses HEAD errors", Label("unit"), func() {
	for _, path := range []string{"v/references", "v%2Fmalformed/references", "v/references/resolve"} {
		for _, method := range []string{"GET", "HEAD"} {
			store := &firstReferenceAuth{}
			h, err := New(appauth.New(store), Policy{Hosts: []string{"127.0.0.1:9270"}, Origins: []string{"http://127.0.0.1:9270"}}, nil)
			Expect(err).NotTo(HaveOccurred())
			r := httptest.NewRequest(method, "http://127.0.0.1:9270/api/v1/versions/"+path, nil)
			r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
			w := httptest.NewRecorder()
			before := time.Now()
			h.ServeHTTP(w, r)
			Expect(store.seen).To(BeTrue())
			Expect(store.deadline.Sub(before)).To(BeNumerically(">", 0))
			Expect(time.Until(store.deadline)).To(BeNumerically("<=", 30*time.Second))
			Expect(w.Code).To(Equal(503))
			Expect(w.Header().Get("Retry-After")).To(Equal("2"))
			Expect(w.Header().Get("Content-Length")).NotTo(BeEmpty())
			if method == "HEAD" {
				Expect(w.Body.Len()).To(BeZero())
			} else {
				Expect(w.Body.String()).To(ContainSubstring("query_timeout"))
			}
		}
	}
})
