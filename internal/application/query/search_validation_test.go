package query_test

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/query"
	app "haeryong.local/haeryong/internal/application/storage"
	"strings"
)

// A deliberately absent auth/store behind the spy makes SQL-before-validation fail.
type searchReadSpy struct {
	app.Store
	calls int
}

func (s *searchReadSpy) Write(_ context.Context, _ func(app.WriteTx) error) error {
	s.calls++
	Fail("invalid normalized query reached storage")
	return nil
}

var _ = It("SC02 SC08 rejects excessive normalized expansion before executing SQL", Label("unit"), func() {
	spy := &searchReadSpy{}
	q := query.New(spy, nil, nil)
	for _, text := range []string{`"a` + strings.Repeat("ß", 3000) + `z"`, strings.Repeat("a", 4097), "unclosed\"", string([]byte{255})} {
		_, e := q.Search(context.Background(), "unused", query.SearchOptions{Text: text})
		Expect(e).To(HaveOccurred())
	}
	Expect(spy.calls).To(BeZero())
})
