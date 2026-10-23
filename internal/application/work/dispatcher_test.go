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

type routerTx struct {
	app.WriteTx
	calls *[]string
}

func (t routerTx) DerivedWork(id string) (domain.DerivedWork, error) {
	return domain.DerivedWork{JobID: id, VersionID: "v", WorkKind: id}, nil
}
func (t routerTx) Job(id string) (domain.Job, error) {
	return domain.Job{ID: id, Kind: "derived_index", State: "completed"}, nil
}
func (t routerTx) ReferenceSearchTerminal(string) (bool, error) {
	*t.calls = append(*t.calls, "gate")
	return false, nil
}
func (t routerTx) SearchVersions(after string, _ int) ([]string, error) {
	if after == "" {
		return []string{"old", "new"}, nil
	}
	return nil, nil
}
func (t routerTx) EnqueueSearch(v string, _ int64) (string, error) {
	*t.calls = append(*t.calls, "search:"+v)
	return "", nil
}
func (t routerTx) EnqueueReferences(v string, _ int64) (string, error) {
	*t.calls = append(*t.calls, "references:"+v)
	return "", nil
}

var _ = It("RF07 router selects each maintenance kind and sweeps all versions search-first", Label("unit"), func() {
	calls := []string{}
	r := work.NewMaintenance(store{tx: routerTx{calls: &calls}}, nil)
	Expect(r.Process(context.Background(), "search")).To(Succeed())
	Expect(calls).To(BeEmpty())
	Expect(r.Process(context.Background(), "references")).To(Succeed())
	Expect(calls).To(Equal([]string{"gate"}))
	calls = nil
	done, err := r.Sweep(context.Background())
	Expect(err).NotTo(HaveOccurred())
	Expect(done).To(BeFalse())
	Expect(calls).To(Equal([]string{"search:old", "references:old", "search:new", "references:new"}))
	done, err = r.Sweep(context.Background())
	Expect(err).NotTo(HaveOccurred())
	Expect(done).To(BeTrue())
})
