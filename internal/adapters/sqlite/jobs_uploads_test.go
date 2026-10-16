package sqlite

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func TestLocalJobReopenGenerationCancelAndLinkedRetry(t *testing.T) {
	_, writer := profile(t)
	s := openStore(t, writer, nil)
	jobID := id(t)
	var old domain.Job
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.CreateJob(domain.Job{ID: jobID, Kind: "import", Scope: []byte(`{"version":"exact"}`), Permissions: map[string]int64{"project": 1}}); err != nil {
			return err
		}
		var err error
		old, err = tx.ClaimJob(jobID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openStore(t, writer, nil)
	interrupted, err := s.Job(ctx, jobID)
	if err != nil || interrupted.State != "interrupted" || interrupted.AttemptGeneration <= old.AttemptGeneration || string(interrupted.Scope) != `{"version":"exact"}` || interrupted.Permissions["project"] != 1 || interrupted.ConfigRevision != 1 {
		t.Fatalf("restart fence/capture: %+v %v", interrupted, err)
	}
	staleSuccess := func(tx app.WriteTx) error {
		return tx.FinishJob(jobID, old.AttemptGeneration, "", func(tx app.WriteTx) error {
			_, err := tx.MutateResource(domain.Resource{Kind: "result", ID: "stale"}, 0, "result.applied")
			return err
		})
	}
	staleFailure := func(tx app.WriteTx) error { return tx.FinishJob(jobID, old.AttemptGeneration, "old failure", nil) }
	for _, finish := range []func(app.WriteTx) error{staleSuccess, staleFailure} {
		if err := s.Write(ctx, finish); !errors.Is(err, domain.ErrStaleWorker) {
			t.Fatalf("stale worker applied: %v", err)
		}
	}
	var fresh domain.Job
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.ReconcileJob(jobID); err != nil {
			return err
		}
		var err error
		fresh, err = tx.ClaimJob(jobID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if fresh.AttemptGeneration <= interrupted.AttemptGeneration {
		t.Fatal("reassignment generation not advanced")
	}
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		return tx.FinishJob(jobID, fresh.AttemptGeneration, "", func(tx app.WriteTx) error {
			_, err := tx.MutateResource(domain.Resource{Kind: "result", ID: "current"}, 0, "result.applied")
			return err
		})
	}); err != nil {
		t.Fatal(err)
	}
	retryID := id(t)
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		if _, err := tx.ClaimJob(jobID); !errors.Is(err, domain.ErrTransition) {
			return fmt.Errorf("terminal job resumed: %v", err)
		}
		next, err := tx.RetryJob(jobID, retryID)
		if err != nil {
			return err
		}
		if next.ParentID != jobID || next.ID == jobID {
			return errors.New("retry lacks new linked identity")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var running, cancelled domain.Job
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		var err error
		running, err = tx.ClaimJob(retryID)
		if err != nil {
			return err
		}
		cancelled, err = tx.CancelJob(retryID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if cancelled.State != "cancelling" || !cancelled.CancelRequested || cancelled.AttemptGeneration <= running.AttemptGeneration {
		t.Fatal("cancel fence missing")
	}
	for _, failure := range []string{"", "late failure"} {
		if err := s.Write(ctx, func(tx app.WriteTx) error { return tx.FinishJob(retryID, running.AttemptGeneration, failure, nil) }); !errors.Is(err, domain.ErrStaleWorker) {
			t.Fatal("cancelled worker finalized", err)
		}
	}
	if err := s.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(retryID, cancelled.AttemptGeneration) }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resource(ctx, "result", "stale"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("stale result visible")
	}
	final, _ := s.Job(ctx, retryID)
	if final.State != "cancelled" || !final.CancelRequested {
		t.Fatal("cancel intent lost")
	}
}
func createUpload(t *testing.T, s *Store) domain.Upload {
	t.Helper()
	u := domain.Upload{ID: id(t), PrincipalID: id(t), Purpose: "result", InputMode: "directory", SelectionName: "目录", Target: []byte(fmt.Sprintf(`{"project_id":%q,"document_id":null}`, id(t))), Checkpoint: domain.Checkpoint{Generation: 1, InventorySHA256: digest(nil), AcceptedBytes: "0"}}
	if err := s.Write(ctx, func(tx app.WriteTx) error { return tx.CreateUpload(u) }); err != nil {
		t.Fatal(err)
	}
	stored, err := s.Upload(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}
func appendFixtureRange(t *testing.T, s *Store, u domain.Upload) domain.Upload {
	t.Helper()
	bytes := []byte("labelled verified range fixture")
	object := domain.Object{Hash: digest(bytes), Size: int64(len(bytes))}
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.PutDurableObject(object); err != nil {
			return err
		}
		_, err := tx.RecordRange(domain.UploadRange{UploadID: u.ID, Path: "nested/한글.unknown", Size: object.Size, Hash: object.Hash}, u.Checkpoint.Generation, digest([]byte("inventory1")))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := s.Upload(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}
func completeFixture(t *testing.T, s *Store, u domain.Upload) domain.Checkpoint {
	t.Helper()
	var cp domain.Checkpoint
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		var err error
		cp, err = tx.CompleteCheckpoint(u.ID, u.Checkpoint.Generation, u.Checkpoint.InventorySHA256)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return cp
}
func completeRequest(t *testing.T, s *Store, u domain.Upload, cp domain.Checkpoint) (domain.Request, domain.Response) {
	t.Helper()
	r := request(t)
	r.Key.PrincipalID = u.PrincipalID
	r.Method = "POST"
	r.Target = "/api/v1/uploads/" + u.ID + "/complete"
	r.Body = jsonBytes(cp)
	response, err := app.Execute(ctx, s, &fixtureAuthority{}, r, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		return tx.CompleteUpload(u.ID, cp, id(t))
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, response
}
func TestUploadCheckpointCoalescingCancellationAbandonment(t *testing.T) {
	_, writer := profile(t)
	s := openStore(t, writer, nil)
	u := appendFixtureRange(t, s, createUpload(t, s))
	cp := completeFixture(t, s, u)
	first, r1 := completeRequest(t, s, u, cp)
	second, r2 := completeRequest(t, s, u, cp)
	if first.Key == second.Key || r1.Status != 202 || !reflect.DeepEqual(r1, r2) {
		t.Fatal("same-checkpoint requests did not own202/sharedjob")
	}
	stored, _ := s.Upload(ctx, u.ID)
	jobID := stored.SealJobID
	for _, key := range []domain.OperationKey{first.Key, second.Key} {
		receipt, err := s.Receipt(ctx, key)
		if err != nil || receipt.State != "completed" || receipt.Response.Status != 202 {
			t.Fatal("completion receipt not definitive", err)
		}
	}
	different := cp
	different.Generation++
	if err := s.Write(ctx, func(tx app.WriteTx) error { _, err := tx.CompleteUpload(u.ID, different, id(t)); return err }); !errors.Is(err, domain.ErrTransition) {
		t.Fatal("different active checkpoint accepted", err)
	}
	var running, cancelled domain.Job
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		var err error
		running, err = tx.ClaimJob(jobID)
		if err != nil {
			return err
		}
		cancelled, err = tx.CancelJob(jobID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.Upload(ctx, u.ID)
	if stored.State != "sealing" {
		t.Fatal("upload reopened before stop confirmation")
	}
	if err := s.Write(ctx, func(tx app.WriteTx) error { return tx.FinishSeal(jobID, running.AttemptGeneration, "no-set") }); !errors.Is(err, domain.ErrStaleWorker) {
		t.Fatal("late seal ignored fence", err)
	}
	s.Close()
	s = openStore(t, writer, nil)
	stored, _ = s.Upload(ctx, u.ID)
	if stored.State != "sealing" || stored.Checkpoint != cp {
		t.Fatal("restart lost coupled fence/input")
	}
	recoveredCancel, loadErr := s.Job(ctx, jobID)
	if loadErr != nil || recoveredCancel.State != "interrupted" || !recoveredCancel.CancelRequested {
		t.Fatalf("cancelled restart not recoverable: %+v %v", recoveredCancel, loadErr)
	}
	cancelled = recoveredCancel
	if err := s.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(jobID, cancelled.AttemptGeneration) }); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.Upload(ctx, u.ID)
	if stored.State != "open" || stored.Checkpoint != cp {
		t.Fatal("confirmed cancel lost bytes/checkpoint")
	}
	replay, err := app.Execute(ctx, s, &fixtureAuthority{}, first, nil, func(app.WriteTx, domain.Receipt) (domain.Response, error) {
		t.Fatal("old key redispatched")
		return domain.Response{}, nil
	})
	if err != nil || !reflect.DeepEqual(r1, replay) {
		t.Fatal("cancel rewrote original202", err)
	}
	_, newResponse := completeRequest(t, s, u, cp)
	if newResponse.Status != 202 {
		t.Fatal("new completion not accepted")
	}
	stored, _ = s.Upload(ctx, u.ID)
	if stored.SealJobID == jobID {
		t.Fatal("terminal seal job resumed")
	}
	newJobID := stored.SealJobID
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		j, err := tx.ClaimJob(newJobID)
		if err != nil {
			return err
		}
		running = j
		if err := tx.AcquirePin(domain.Pin{ID: "recovery", Kind: "recovery", OwnerID: newJobID, Hash: digest([]byte("labelled verified range fixture"))}); err != nil {
			return err
		}
		return tx.AbandonUpload(u.ID)
	}); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.Upload(ctx, u.ID)
	if stored.State != "abandoned" {
		t.Fatal("abandon not immediate")
	}
	for _, finish := range []func(app.WriteTx) error{
		func(tx app.WriteTx) error { return tx.FinishSeal(newJobID, running.AttemptGeneration, "no-set") },
		func(tx app.WriteTx) error {
			return tx.FinishJob(newJobID, running.AttemptGeneration, "late failure", nil)
		},
		func(tx app.WriteTx) error {
			return tx.AdoptUploadObject(u.ID, u.Purpose, u.Target, domain.Reference{Kind: "saved", ID: "late", Hash: digest([]byte("labelled verified range fixture"))}, "")
		},
	} {
		if err := s.Write(ctx, finish); err == nil {
			t.Fatal("abandon fence bypassed")
		}
	}
	current, _ := s.Job(ctx, newJobID)
	if err := s.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(newJobID, current.AttemptGeneration) }); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.Upload(ctx, u.ID)
	if stored.State != "abandoned" {
		t.Fatal("late stop reopened abandoned upload")
	}
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		_, err := tx.ReserveGC(digest([]byte("labelled verified range fixture")), "gc")
		if !errors.Is(err, domain.ErrPinned) {
			return fmt.Errorf("recovery pin removed by abandon: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestSealedIdenticalCheckpointAndTargetBoundAdoption(t *testing.T) {
	_, writer := profile(t)
	s := openStore(t, writer, nil)
	u := appendFixtureRange(t, s, createUpload(t, s))
	cp := completeFixture(t, s, u)
	_, creation := completeRequest(t, s, u, cp)
	stored, _ := s.Upload(ctx, u.ID)
	setID := id(t)
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		if err := tx.CreateContentSet(setID, cp.InventorySHA256); err != nil {
			return err
		}
		if err := tx.SealContentSet(setID); err != nil {
			return err
		}
		j, err := tx.ClaimJob(stored.SealJobID)
		if err != nil {
			return err
		}
		return tx.FinishSeal(j.ID, j.AttemptGeneration, setID)
	}); err != nil {
		t.Fatal(err)
	}
	_, already := completeRequest(t, s, u, cp)
	if creation.Status != 202 || already.Status != 200 {
		t.Fatal("sealed checkpoint response not200")
	}
	ranges, _ := s.Ranges(ctx, u.ID, "", -1, 1)
	if len(ranges) != 1 {
		t.Fatal("accepted range unavailable")
	}
	ref := domain.Reference{Kind: "consumer", ID: "saved", Hash: ranges[0].Hash}
	bad := []byte(fmt.Sprintf(`{"project_id":%q,"document_id":null}`, id(t)))
	if err := s.Write(ctx, func(tx app.WriteTx) error { return tx.AdoptUploadObject(u.ID, "result", bad, ref, "") }); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("upload retargeted", err)
	}
	if err := s.Write(ctx, func(tx app.WriteTx) error { return tx.AdoptUploadObject(u.ID, "result", u.Target, ref, "") }); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx app.WriteTx) error { return tx.AbandonUpload(u.ID) }); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		_, err := tx.ReserveGC(ref.Hash, "gc")
		if !errors.Is(err, domain.ErrPinned) {
			return fmt.Errorf("adopted consumer removed by abandon: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUploadPurposeModesEmptyCheckpointAndCancelRecoveryStates(t *testing.T) {
	_, writer := profile(t)
	s := openStore(t, writer, nil)
	base := createUpload(t, s)
	for _, test := range []struct {
		purpose, mode string
		valid         bool
	}{
		{"result", "directory", true}, {"result", "archive", true}, {"result", "file", true}, {"result", "object", false},
		{"context", "object", true}, {"context", "directory", false}, {"body", "directory", false}, {"metadata", "directory", false},
	} {
		u := base
		u.ID = id(t)
		u.Purpose = test.purpose
		u.InputMode = test.mode
		if u.Purpose == "context" {
			u.Target = []byte(fmt.Sprintf(`{"version_id":%q,"page_path":null,"render_instance_id":null}`, id(t)))
		}
		if u.Purpose == "body" {
			u.Target = []byte(fmt.Sprintf(`{"operation_id":%q,"action":"generation","project_id":%q,"document_id":null}`, id(t), id(t)))
		}
		if u.Purpose == "metadata" {
			u.Target = []byte(fmt.Sprintf(`{"operation_id":%q,"action":"draft_replace","draft_id":%q}`, id(t), id(t)))
		}
		err := s.Write(ctx, func(tx app.WriteTx) error { return tx.CreateUpload(u) })
		if (err == nil) != test.valid {
			t.Fatalf("purpose=%s mode=%s valid=%v err=%v", test.purpose, test.mode, test.valid, err)
		}
	}
	invalid := base
	invalid.ID = id(t)
	invalid.Checkpoint.InventorySHA256 = digest([]byte("not-empty"))
	if err := s.Write(ctx, func(tx app.WriteTx) error { return tx.CreateUpload(invalid) }); err == nil {
		t.Fatal("empty initial checkpoint accepted a nonempty digest")
	}
	for _, state := range []string{"running", "cancelling", "paused", "reconciling"} {
		jobID := id(t)
		if err := s.Write(ctx, func(tx app.WriteTx) error { return tx.CreateJob(domain.Job{ID: jobID, Kind: "import"}) }); err != nil {
			t.Fatal(err)
		}
		s.Close()
		raw := readDB(t, writer.Profile)
		if _, err := raw.Exec("UPDATE jobs SET state=?,attempt_generation=2,cancel_requested=1 WHERE job_id=?", state, jobID); err != nil {
			raw.Close()
			t.Fatal(err)
		}
		raw.Close()
		s = openStore(t, writer, nil)
		recovered, err := s.Job(ctx, jobID)
		if err != nil || recovered.State != "interrupted" || recovered.AttemptGeneration != 3 || !recovered.CancelRequested {
			t.Fatalf("recover %s: %+v %v", state, recovered, err)
		}
		if err := s.Write(ctx, func(tx app.WriteTx) error { return tx.ReconcileJob(jobID) }); err != nil {
			t.Fatalf("reconcile cancel %s: %v", state, err)
		}
		terminal, _ := s.Job(ctx, jobID)
		if terminal.State != "cancelled" || !terminal.CancelRequested {
			t.Fatal("reconciliation lost cancellation")
		}
	}
}
