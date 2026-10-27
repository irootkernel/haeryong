package sqlite

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type fixtureAuthority struct {
	writeCheck  func(app.WriteTx) error
	onAuthorize func()
	deny        bool
	redact      bool
	calls       atomic.Int64
}

var _ = DescribeTable("Event replay excludes retained prior epochs", Label("persistence"), func(transactional bool) {
	t := GinkgoT()
	_, writer := profile(t)
	s := openStore(t, writer, nil)
	before, err := s.Health(ctx)
	Expect(err).NotTo(HaveOccurred())
	appendPosition := func(name string) {
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			_, err := tx.MutateResource(domain.Resource{Kind: "position", ID: name, Value: []byte(`{"where":"` + name + `"}`)}, 0, "position.changed")
			return err
		})).To(Succeed())
	}
	appendPosition("prior")
	// Protected restore rotates the library epoch while retaining event history.
	// Its real restore/reopen/poll integration is also exercised in copies_test.
	current := id(t)
	_, err = s.db.ExecContext(ctx, "UPDATE libraries SET stream_epoch=?", current)
	Expect(err).NotTo(HaveOccurred())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		epoch, head, err := tx.EventHead()
		Expect(err).NotTo(HaveOccurred())
		Expect(epoch).To(Equal(current))
		Expect(head).To(BeZero(), "retained old rows are not the current head")
		events, err := tx.Events("", 0, 64)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(BeEmpty(), "empty-epoch convention selects current history")
		_, err = tx.Events(before.Library.StreamEpoch, 0, 64)
		Expect(err).To(MatchError(domain.ErrPrecondition))
		return nil
	})).To(Succeed())
	appendPosition("current")
	var retained int
	Expect(s.db.QueryRowContext(ctx, "SELECT count(*) FROM events WHERE stream_epoch=?", before.Library.StreamEpoch).Scan(&retained)).To(Succeed())
	Expect(retained).To(BeNumerically(">", 0))
	var events []domain.Event
	if transactional {
		err = s.Write(ctx, func(tx app.WriteTx) error {
			var err error
			events, err = tx.Events(current, 0, 64)
			return err
		})
	} else {
		events, err = s.Events(ctx, current, 0, 64)
	}
	Expect(err).NotTo(HaveOccurred())
	var after int
	Expect(s.db.QueryRowContext(ctx, "SELECT count(*) FROM events WHERE stream_epoch=?", before.Library.StreamEpoch).Scan(&after)).To(Succeed())
	Expect(after).To(Equal(retained), "replay must not delete historical epochs")
	_, err = s.Events(ctx, before.Library.StreamEpoch, 0, 64)
	Expect(err).To(MatchError(domain.ErrPrecondition))
	Expect(events).To(ContainElement(HaveField("ResourceID", "current")))
	Expect(events).To(HaveEach(HaveField("StreamEpoch", current)))
	Expect(events).To(HaveLen(1))
	Expect(s.Close()).To(Succeed())
	s = openStore(t, writer, nil)
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		epoch, head, err := tx.EventHead()
		Expect(err).NotTo(HaveOccurred())
		Expect(epoch).To(Equal(current), "ordinary reopen retains the restored epoch")
		Expect(head).To(Equal(events[0].Sequence))
		replayed, err := tx.Events("", 0, 64)
		Expect(err).NotTo(HaveOccurred())
		Expect(replayed).To(Equal(events))
		return nil
	})).To(Succeed())
}, Entry("Store.Events", false), Entry("writeTx.Events", true))

func (a *fixtureAuthority) Authorize(context.Context, domain.Request) error {
	a.calls.Add(1)
	if a.deny {
		return domain.ErrDenied
	}
	if a.onAuthorize != nil {
		a.onAuthorize()
	}
	return nil
}
func (a *fixtureAuthority) Filter(_ context.Context, _ domain.Request, r domain.Response) (domain.Response, error) {
	if a.redact {
		r.Body = []byte("redacted")
	}
	return r, nil
}
func id(t GinkgoTInterface) string {
	GinkgoHelper()
	s, err := service.NewID()
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return s
}
func request(t GinkgoTInterface) domain.Request {
	return domain.Request{Key: domain.OperationKey{PrincipalID: id(t), OperationID: id(t)}, Method: "PUT", Target: "/api/v1/resource/exact", Body: []byte(`{"value":"first"}`), Preconditions: map[string]string{"If-None-Match": "*"}, Omitted: []string{"created_at", "version_id"}}
}

var _ = It("TestOperationAtomicContentionReplayAndOmissions", Label("persistence"), func() {
	t := GinkgoT()

	_, writer := profile(t)
	s := openStore(t, writer, nil)
	auth := &fixtureAuthority{}
	r := request(t)
	var normalized, applied atomic.Int64
	normalize := func() ([]byte, error) {
		normalized.Add(1)
		return []byte(`{"generated_id":"once","base":"captured-before-transfer"}`), nil
	}
	apply := func(tx app.WriteTx, receipt domain.Receipt) (domain.Response, error) {
		applied.Add(1)
		if string(receipt.Normalization) != `{"generated_id":"once","base":"captured-before-transfer"}` {
			return domain.Response{}, errors.New("normalization lost")
		}
		updated, err := tx.MutateResource(domain.Resource{Kind: "position", ID: "exact", Value: []byte("state")}, 0, "position.changed")
		if err != nil {
			return domain.Response{}, err
		}
		return domain.Response{Status: 200, Body: jsonBytes(updated)}, nil
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	responses := make(chan domain.Response, 12)
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer GinkgoRecover()
			defer wg.Done()
			<-start
			response, err := app.Execute(ctx, s, auth, r, normalize, apply)
			if err != nil {
				failures <- err
			} else {
				responses <- response
			}
		}()
	}
	close(start)
	wg.Wait()
	close(failures)
	close(responses)
	for err := range failures {
		Fail(fmt.Sprint(err))
	}
	var first domain.Response
	for response := range responses {
		if first.Status == 0 {
			first = response
		}
		Expect(!reflect.DeepEqual(response, first)).To(BeFalse(), func() string { return fmt.Sprint("parallel response changed") })

	}
	Expect(applied.Load() != 1 || normalized.Load() != 1 || auth.calls.Load() != 12).To(BeFalse(), func() string {
		return fmt.Sprintf("counts apply=%d normalize=%d auth=%d", applied.Load(), normalized.Load(), auth.calls.Load())
	})

	h, _ := s.Health(ctx)
	events, err := s.Events(ctx, h.Library.StreamEpoch, 0, 2)
	Expect(err != nil || len(events) != 1 || events[0].Revision != 1).To(BeFalse(), func() string { return fmt.Sprintf("atomic outbox: %v %+v", err, events) })

	originalReceipt, err := s.Receipt(ctx, r.Key)
	Expect(err != nil || originalReceipt.State != "completed").To(BeFalse(), func() string { return fmt.Sprint(err) })

	for _, change := range []string{"method", "target", "body", "precondition", "omission"} {
		changed := r
		changed.Preconditions = map[string]string{"If-None-Match": "*"}
		changed.Omitted = append([]string{}, r.Omitted...)
		switch change {
		case "method":
			changed.Method = "POST"
		case "target":
			changed.Target = "/api/v1/resource/other"
		case "body":
			changed.Body = []byte(`{"value":"changed"}`)
		case "precondition":
			changed.Preconditions = map[string]string{"If-Match": "now-stale"}
		case "omission":
			changed.Omitted = []string{"created_at"}
		}
		{
			_, err := app.Execute(ctx, s, auth, changed, normalize, apply)
			Expect(!errors.Is(err, domain.ErrConflict)).To(BeFalse(), func() string { return fmt.Sprintf("changed %s accepted: %v", change, err) })
		}

	}
	r.Omitted = []string{"version_id", "created_at"}
	{
		_, err := app.Execute(ctx, s, auth, r, normalize, apply)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint("omission order changed identity", err) })
	}

	s.Close()
	s = openStore(t, writer, nil)
	replay, err := app.Execute(ctx, s, auth, r, normalize, apply)
	Expect(err != nil || !reflect.DeepEqual(first, replay)).To(BeFalse(), func() string { return fmt.Sprintf("reopen replay/stale precondition: %v", err) })

	redacted, err := app.Execute(ctx, s, &fixtureAuthority{redact: true}, r, normalize, apply)
	Expect(err != nil || string(redacted.Body) != "redacted").To(BeFalse(), func() string { return fmt.Sprint("current visibility filter bypassed") })

	deny := &fixtureAuthority{deny: true}
	s.Close()
	{
		_, err := app.Execute(ctx, s, deny, r, normalize, apply)
		Expect(!errors.Is(err, domain.ErrDenied)).To(BeFalse(), func() string { return fmt.Sprintf("denial did not precede even closed-store lookup: %v", err) })
	}

})
var _ = It("TestOperationCommitFaultRollsBackAllAndPrincipalNamespace", Label("persistence"), func() {
	t := GinkgoT()

	_, writer := profile(t)
	var fail atomic.Bool
	s := openStore(t, writer, func(b Boundary) error {
		if b == BeforeCommit && fail.Load() {
			return errors.New("injected commit failure")
		}
		return nil
	})
	r := request(t)
	auth := &fixtureAuthority{}
	apply := func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		_, err := tx.MutateResource(domain.Resource{Kind: "note", ID: r.Key.PrincipalID, Value: []byte("content")}, 0, "note.changed")
		return domain.Response{Status: 201, Body: []byte("saved")}, err
	}
	fail.Store(true)
	{
		_, err := app.Execute(ctx, s, auth, r, nil, apply)
		Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("commit fault passed") })
	}

	fail.Store(false)
	{
		_, err := s.Receipt(ctx, r.Key)
		Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("rolled-back receipt retained") })
	}
	{

		_, err := s.Resource(ctx, "note", r.Key.PrincipalID)
		Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("rolled-back mutation retained") })
	}

	h, _ := s.Health(ctx)
	events, _ := s.Events(ctx, h.Library.StreamEpoch, 0, 100)
	Expect(len(events) != 0).To(BeFalse(), func() string { return fmt.Sprint("rolled-back event retained") })

	s.Close()
	s = openStore(t, writer, nil)
	{
		_, err := app.Execute(ctx, s, auth, r, nil, apply)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	r.Key.PrincipalID = id(t)
	{
		_, err := app.Execute(ctx, s, auth, r, nil, apply)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint("principal namespaces collided", err) })
	}

	events, _ = s.Events(ctx, h.Library.StreamEpoch, 0, 100)
	Expect(len(events) != 2).To(BeFalse(), func() string { return fmt.Sprint("distinct principals did not independently commit") })
	{

		_, err := s.Events(ctx, "old-restored-epoch", 0, 1)
		Expect(!errors.Is(err, domain.ErrPrecondition)).To(BeFalse(), func() string { return fmt.Sprint("wrong epoch replayed") })
	}

})
var _ = It("TestReceiptReservationDefinitiveFailureAndCompleted202", Label("persistence"), func() {
	t := GinkgoT()

	_, writer := profile(t)
	s := openStore(t, writer, nil)
	r := request(t)
	auth := &fixtureAuthority{}
	{
		err := s.Write(ctx, func(tx app.WriteTx) error { _, _, err := tx.ReserveOperation(r, nil); return err })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	receipt, err := s.Receipt(ctx, r.Key)
	Expect(err != nil || receipt.State != "accepted" || receipt.Response != nil).To(BeFalse(), func() string { return fmt.Sprintf("reservation is not definitive: %v %+v", err, receipt) })

	unexpected := func(app.WriteTx, domain.Receipt) (domain.Response, error) {
		Fail(fmt.Sprint("pending/replay redispatched"))
		return domain.Response{}, nil
	}
	pending, err := app.Execute(ctx, s, auth, r, nil, unexpected)
	Expect(err != nil || pending.Status != 202).To(BeFalse(), func() string { return fmt.Sprint(err) })

	var body map[string]string
	{
		err := json.Unmarshal(pending.Body, &body)
		Expect(err != nil || body["operation_id"] != r.Key.OperationID || body["operation_url"] != "/api/v1/operations/"+r.Key.OperationID).To(BeFalse(), func() string { return fmt.Sprint("pending recovery identity missing") })
	}
	{

		err := s.Write(ctx, func(tx app.WriteTx) error { return tx.ReconcileOperation(r.Key) })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		_, err := app.Execute(ctx, s, auth, r, nil, unexpected)
		Expect(!errors.Is(err, domain.ErrReconciliation)).To(BeFalse(), func() string { return fmt.Sprintf("uncertainty mislabelled pending: %v", err) })
	}

	failed := request(t)
	expected := domain.Response{Status: 412, Body: []byte(`{"error":"original stale precondition"}`)}
	{
		_, err := app.Execute(ctx, s, auth, failed, nil, func(app.WriteTx, domain.Receipt) (domain.Response, error) { return expected, nil })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	receipt, _ = s.Receipt(ctx, failed.Key)
	Expect(receipt.State != "failed").To(BeFalse(), func() string { return fmt.Sprintf("failure not labelled: %+v", receipt) })

	replay, err := app.Execute(ctx, s, auth, failed, nil, unexpected)
	Expect(err != nil || !reflect.DeepEqual(expected, replay)).To(BeFalse(), func() string { return fmt.Sprint("failure response not replayed") })

	creation := request(t)
	jobID := id(t)
	response, err := app.Execute(ctx, s, auth, creation, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		return domain.Response{Status: 202, Body: []byte(jobID)}, tx.CreateJob(domain.Job{ID: jobID, Kind: "import"})
	})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	receipt, _ = s.Receipt(ctx, creation.Key)
	Expect(receipt.State != "completed" || receipt.Response.Status != 202).To(BeFalse(), func() string { return fmt.Sprint("job creation mislabelled accepted reservation") })
	{

		err := s.Write(ctx, func(tx app.WriteTx) error {
			j, err := tx.ClaimJob(jobID)
			if err != nil {
				return err
			}
			return tx.FinishJob(jobID, j.AttemptGeneration, "local failure", nil)
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	again, err := app.Execute(ctx, s, auth, creation, nil, unexpected)
	Expect(err != nil || !reflect.DeepEqual(response, again)).To(BeFalse(), func() string { return fmt.Sprint("later job failure rewrote completed202") })

})
var _ = It("TestAuthorityDenialPreventsObjectAdoption", Label("persistence"), func() {
	t := GinkgoT()

	_, writer := profile(t)
	s := openStore(t, writer, nil)
	object := domain.Object{Hash: digest([]byte("labelled metadata fixture")), Size: 25}
	{
		err := s.Write(ctx, func(tx app.WriteTx) error {
			if err := tx.PutDurableObject(object); err != nil {
				return err
			}
			return tx.AcquirePin(domain.Pin{ID: "staging", Kind: "upload", OwnerID: "upload", Hash: object.Hash})
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	r := request(t)
	applied := false
	_, err := app.Execute(ctx, s, &fixtureAuthority{deny: true}, r, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		applied = true
		return domain.Response{Status: 201}, tx.Adopt(domain.Reference{Kind: "consumer", ID: "saved", Hash: object.Hash}, "staging")
	})
	Expect(!errors.Is(err, domain.ErrDenied) || applied).To(BeFalse(), func() string { return fmt.Sprint("unauthorized adoption entered transaction") })
	{

		err := s.Write(ctx, func(tx app.WriteTx) error {
			_, err := tx.ReserveGC(object.Hash, "gc")
			if !errors.Is(err, domain.ErrPinned) {
				return fmt.Errorf("staging pin lost after denial: %v", err)
			}
			return nil
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

})

func (a *fixtureAuthority) AuthorizeWrite(_ context.Context, tx app.WriteTx, _ domain.Request) error {
	if a.deny {
		return domain.ErrDenied
	}
	if a.writeCheck != nil {
		return a.writeCheck(tx)
	}
	return nil
}

var _ = It("TestAuthorityRechecksWriterWaitAndRollsBackPrecommitRevocation", Label("persistence"), func() {
	t := GinkgoT()

	_, writer := profile(t)
	s := openStore(t, writer, nil)
	request := request(t)
	held, release := make(chan struct{}), make(chan struct{})
	ownerDone := make(chan error, 1)
	go func() {
		defer GinkgoRecover()
		ownerDone <- s.Write(ctx, func(app.WriteTx) error { close(held); <-release; return nil })
	}()
	<-held
	authorized := make(chan struct{})
	var revoked atomic.Bool
	auth := &fixtureAuthority{onAuthorize: func() { close(authorized) }, writeCheck: func(app.WriteTx) error {
		if revoked.Load() {
			return domain.ErrDenied
		}
		return nil
	}}
	result := make(chan error, 1)
	var applied atomic.Bool
	go func() {
		defer GinkgoRecover()
		_, err := app.Execute(ctx, s, auth, request, nil, func(app.WriteTx, domain.Receipt) (domain.Response, error) {
			applied.Store(true)
			return domain.Response{Status: 201}, nil
		})
		result <- err
	}()
	<-authorized
	revoked.Store(true)
	close(release)
	{
		err := <-ownerDone
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		err := <-result
		Expect(!errors.Is(err, domain.ErrDenied) || applied.Load()).To(BeFalse(), func() string { return fmt.Sprintf("writer-wait revocation bypassed: %v apply=%v", err, applied.Load()) })
	}
	{

		_, err := s.Receipt(ctx, request.Key)
		Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("revoked lookup created a receipt") })
	}

	revoked.Store(false)
	h, _ := s.Health(ctx)
	auth = &fixtureAuthority{writeCheck: func(tx app.WriteTx) error {
		state, err := tx.Scope(h.Library.UnfiledProjectID)
		if err != nil {
			return err
		}
		if state.ConfigRevision != 1 || state.Project.PermissionsRevision != 1 || state.Project.Lifecycle != "active" {
			return domain.ErrDenied
		}
		if revoked.Load() {
			return domain.ErrDenied
		}
		return nil
	}}
	_, err := app.Execute(ctx, s, auth, request, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		_, err := tx.MutateResource(domain.Resource{Kind: "restricted", ID: "exact", Value: []byte("private")}, 0, "changed")
		revoked.Store(true)
		return domain.Response{Status: 201}, err
	})
	Expect(!errors.Is(err, domain.ErrDenied)).To(BeFalse(), func() string { return fmt.Sprintf("precommit authority revocation ignored: %v", err) })
	{

		_, err := s.Resource(ctx, "restricted", "exact")
		Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("revoked mutation committed") })
	}
	{

		_, err := s.Receipt(ctx, request.Key)
		Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("revoked definitive receipt committed") })
	}

	events, _ := s.Events(ctx, h.Library.StreamEpoch, 0, 100)
	Expect(len(events) != 0).To(BeFalse(), func() string { return fmt.Sprint("revoked outbox committed") })

})
