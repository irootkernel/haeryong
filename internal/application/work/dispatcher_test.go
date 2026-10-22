package work_test

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
)

type store struct {
	app.Store
	tx app.WriteTx
}

func (s store) Write(_ context.Context, f func(app.WriteTx) error) error { return f(s.tx) }

type tx struct{ app.WriteTx }

func (tx) WorkJobs(int) ([]domain.Job, error) {
	return []domain.Job{{ID: "seal", Kind: "upload_seal"}, {ID: "derived", Kind: "derived_index"}, {ID: "import", Kind: "import"}}, nil
}

type processor struct {
	calls  *[]string
	cancel context.CancelFunc
}

func (p processor) Process(_ context.Context, id string) error {
	*p.calls = append(*p.calls, id)
	if id == "import" {
		p.cancel()
	}
	return nil
}
func (p processor) Sweep(context.Context) (bool, error) {
	*p.calls = append(*p.calls, "sweep")
	return true, nil
}

var _ = It("routes all three work kinds serially through the existing ledger and awaits cancellation", Label("unit"), func() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := []string{}
	p := processor{&calls, cancel}
	d := work.Dispatcher{Store: store{tx: tx{}}, Imports: p, Derived: p}
	Expect(d.Run(ctx)).To(Succeed())
	Expect(calls).To(Equal([]string{"sweep", "seal", "derived", "import"}))
})
