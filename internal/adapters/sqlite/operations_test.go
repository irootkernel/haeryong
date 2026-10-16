package sqlite

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"context"
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
func id(t *testing.T) string {
	t.Helper()
	s, err := service.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func request(t *testing.T) domain.Request {
	return domain.Request{Key: domain.OperationKey{PrincipalID: id(t), OperationID: id(t)}, Method: "PUT", Target: "/api/v1/resource/exact", Body: []byte(`{"value":"first"}`), Preconditions: map[string]string{"If-None-Match": "*"}, Omitted: []string{"created_at", "version_id"}}
}
func TestOperationAtomicContentionReplayAndOmissions(t *testing.T) {
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
		t.Error(err)
	}
	var first domain.Response
	for response := range responses {
		if first.Status == 0 {
			first = response
		}
		if !reflect.DeepEqual(response, first) {
			t.Error("parallel response changed")
		}
	}
	if applied.Load() != 1 || normalized.Load() != 1 || auth.calls.Load() != 12 {
		t.Fatalf("counts apply=%d normalize=%d auth=%d", applied.Load(), normalized.Load(), auth.calls.Load())
	}
	h, _ := s.Health(ctx)
	events, err := s.Events(ctx, h.Library.StreamEpoch, 0, 2)
	if err != nil || len(events) != 1 || events[0].Revision != 1 {
		t.Fatalf("atomic outbox: %v %+v", err, events)
	}
	originalReceipt, err := s.Receipt(ctx, r.Key)
	if err != nil || originalReceipt.State != "completed" {
		t.Fatal(err)
	}
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
		if _, err := app.Execute(ctx, s, auth, changed, normalize, apply); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("changed %s accepted: %v", change, err)
		}
	}
	r.Omitted = []string{"version_id", "created_at"}
	if _, err := app.Execute(ctx, s, auth, r, normalize, apply); err != nil {
		t.Fatal("omission order changed identity", err)
	}
	s.Close()
	s = openStore(t, writer, nil)
	replay, err := app.Execute(ctx, s, auth, r, normalize, apply)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("reopen replay/stale precondition: %v", err)
	}
	redacted, err := app.Execute(ctx, s, &fixtureAuthority{redact: true}, r, normalize, apply)
	if err != nil || string(redacted.Body) != "redacted" {
		t.Fatal("current visibility filter bypassed")
	}
	deny := &fixtureAuthority{deny: true}
	s.Close()
	if _, err := app.Execute(ctx, s, deny, r, normalize, apply); !errors.Is(err, domain.ErrDenied) {
		t.Fatalf("denial did not precede even closed-store lookup: %v", err)
	}
}
func TestOperationCommitFaultRollsBackAllAndPrincipalNamespace(t *testing.T) {
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
	if _, err := app.Execute(ctx, s, auth, r, nil, apply); err == nil {
		t.Fatal("commit fault passed")
	}
	fail.Store(false)
	if _, err := s.Receipt(ctx, r.Key); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("rolled-back receipt retained")
	}
	if _, err := s.Resource(ctx, "note", r.Key.PrincipalID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("rolled-back mutation retained")
	}
	h, _ := s.Health(ctx)
	events, _ := s.Events(ctx, h.Library.StreamEpoch, 0, 100)
	if len(events) != 0 {
		t.Fatal("rolled-back event retained")
	}
	s.Close()
	s = openStore(t, writer, nil)
	if _, err := app.Execute(ctx, s, auth, r, nil, apply); err != nil {
		t.Fatal(err)
	}
	r.Key.PrincipalID = id(t)
	if _, err := app.Execute(ctx, s, auth, r, nil, apply); err != nil {
		t.Fatal("principal namespaces collided", err)
	}
	events, _ = s.Events(ctx, h.Library.StreamEpoch, 0, 100)
	if len(events) != 2 {
		t.Fatal("distinct principals did not independently commit")
	}
	if _, err := s.Events(ctx, "old-restored-epoch", 0, 1); !errors.Is(err, domain.ErrPrecondition) {
		t.Fatal("wrong epoch replayed")
	}
}
func TestReceiptReservationDefinitiveFailureAndCompleted202(t *testing.T) {
	_, writer := profile(t)
	s := openStore(t, writer, nil)
	r := request(t)
	auth := &fixtureAuthority{}
	if err := s.Write(ctx, func(tx app.WriteTx) error { _, _, err := tx.ReserveOperation(r, nil); return err }); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.Receipt(ctx, r.Key)
	if err != nil || receipt.State != "accepted" || receipt.Response != nil {
		t.Fatalf("reservation is not definitive: %v %+v", err, receipt)
	}
	unexpected := func(app.WriteTx, domain.Receipt) (domain.Response, error) {
		t.Error("pending/replay redispatched")
		return domain.Response{}, nil
	}
	pending, err := app.Execute(ctx, s, auth, r, nil, unexpected)
	if err != nil || pending.Status != 202 {
		t.Fatal(err)
	}
	var body map[string]string
	if err := json.Unmarshal(pending.Body, &body); err != nil || body["operation_id"] != r.Key.OperationID || body["operation_url"] != "/api/v1/operations/"+r.Key.OperationID {
		t.Fatal("pending recovery identity missing")
	}
	if err := s.Write(ctx, func(tx app.WriteTx) error { return tx.ReconcileOperation(r.Key) }); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Execute(ctx, s, auth, r, nil, unexpected); !errors.Is(err, domain.ErrReconciliation) {
		t.Fatalf("uncertainty mislabelled pending: %v", err)
	}
	failed := request(t)
	expected := domain.Response{Status: 412, Body: []byte(`{"error":"original stale precondition"}`)}
	if _, err := app.Execute(ctx, s, auth, failed, nil, func(app.WriteTx, domain.Receipt) (domain.Response, error) { return expected, nil }); err != nil {
		t.Fatal(err)
	}
	receipt, _ = s.Receipt(ctx, failed.Key)
	if receipt.State != "failed" {
		t.Fatalf("failure not labelled: %+v", receipt)
	}
	replay, err := app.Execute(ctx, s, auth, failed, nil, unexpected)
	if err != nil || !reflect.DeepEqual(expected, replay) {
		t.Fatal("failure response not replayed")
	}
	creation := request(t)
	jobID := id(t)
	response, err := app.Execute(ctx, s, auth, creation, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		return domain.Response{Status: 202, Body: []byte(jobID)}, tx.CreateJob(domain.Job{ID: jobID, Kind: "import"})
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ = s.Receipt(ctx, creation.Key)
	if receipt.State != "completed" || receipt.Response.Status != 202 {
		t.Fatal("job creation mislabelled accepted reservation")
	}
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		j, err := tx.ClaimJob(jobID)
		if err != nil {
			return err
		}
		return tx.FinishJob(jobID, j.AttemptGeneration, "local failure", nil)
	}); err != nil {
		t.Fatal(err)
	}
	again, err := app.Execute(ctx, s, auth, creation, nil, unexpected)
	if err != nil || !reflect.DeepEqual(response, again) {
		t.Fatal("later job failure rewrote completed202")
	}
}
func TestAuthorityDenialPreventsObjectAdoption(t *testing.T) {
	_, writer := profile(t)
	s := openStore(t, writer, nil)
	object := domain.Object{Hash: digest([]byte("labelled metadata fixture")), Size: 25}
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.PutDurableObject(object); err != nil {
			return err
		}
		return tx.AcquirePin(domain.Pin{ID: "staging", Kind: "upload", OwnerID: "upload", Hash: object.Hash})
	}); err != nil {
		t.Fatal(err)
	}
	r := request(t)
	applied := false
	_, err := app.Execute(ctx, s, &fixtureAuthority{deny: true}, r, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		applied = true
		return domain.Response{Status: 201}, tx.Adopt(domain.Reference{Kind: "consumer", ID: "saved", Hash: object.Hash}, "staging")
	})
	if !errors.Is(err, domain.ErrDenied) || applied {
		t.Fatal("unauthorized adoption entered transaction")
	}
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		_, err := tx.ReserveGC(object.Hash, "gc")
		if !errors.Is(err, domain.ErrPinned) {
			return fmt.Errorf("staging pin lost after denial: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func (a *fixtureAuthority) AuthorizeWrite(_ context.Context, tx app.WriteTx, _ domain.Request) error {
	if a.deny {
		return domain.ErrDenied
	}
	if a.writeCheck != nil {
		return a.writeCheck(tx)
	}
	return nil
}

func TestAuthorityRechecksWriterWaitAndRollsBackPrecommitRevocation(t *testing.T) {
	_, writer := profile(t)
	s := openStore(t, writer, nil)
	request := request(t)
	held, release := make(chan struct{}), make(chan struct{})
	ownerDone := make(chan error, 1)
	go func() { ownerDone <- s.Write(ctx, func(app.WriteTx) error { close(held); <-release; return nil }) }()
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
		_, err := app.Execute(ctx, s, auth, request, nil, func(app.WriteTx, domain.Receipt) (domain.Response, error) {
			applied.Store(true)
			return domain.Response{Status: 201}, nil
		})
		result <- err
	}()
	<-authorized
	revoked.Store(true)
	close(release)
	if err := <-ownerDone; err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, domain.ErrDenied) || applied.Load() {
		t.Fatalf("writer-wait revocation bypassed: %v apply=%v", err, applied.Load())
	}
	if _, err := s.Receipt(ctx, request.Key); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("revoked lookup created a receipt")
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
	if !errors.Is(err, domain.ErrDenied) {
		t.Fatalf("precommit authority revocation ignored: %v", err)
	}
	if _, err := s.Resource(ctx, "restricted", "exact"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("revoked mutation committed")
	}
	if _, err := s.Receipt(ctx, request.Key); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("revoked definitive receipt committed")
	}
	events, _ := s.Events(ctx, h.Library.StreamEpoch, 0, 100)
	if len(events) != 0 {
		t.Fatal("revoked outbox committed")
	}
}
