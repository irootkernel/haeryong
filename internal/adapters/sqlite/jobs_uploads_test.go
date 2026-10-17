package sqlite

import (
	"errors"
	"fmt"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("TestLocalJobReopenGenerationCancelAndLinkedRetry", Label("persistence"), func() {
	t := GinkgoT()

	_, writer := profile(t)
	s := openStore(t, writer, nil)
	jobID := id(t)
	var old domain.Job
	{
		err := s.Write(ctx, func(tx app.WriteTx) error {
			if err := tx.CreateJob(domain.Job{ID: jobID, Kind: "import", Scope: []byte(`{"version":"exact"}`), Permissions: map[string]int64{"project": 1}}); err != nil {
				return err
			}
			var err error
			old, err = tx.ClaimJob(jobID)
			return err
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	s.Close()
	s = openStore(t, writer, nil)
	interrupted, err := s.Job(ctx, jobID)
	Expect(err != nil || interrupted.State != "interrupted" || interrupted.AttemptGeneration <= old.AttemptGeneration || string(interrupted.Scope) != `{"version":"exact"}` || interrupted.Permissions["project"] != 1 || interrupted.ConfigRevision != 1).To(BeFalse(), func() string { return fmt.Sprintf("restart fence/capture: %+v %v", interrupted, err) })

	staleSuccess := func(tx app.WriteTx) error {
		return tx.FinishJob(jobID, old.AttemptGeneration, "", func(tx app.WriteTx) error {
			_, err := tx.MutateResource(domain.Resource{Kind: "result", ID: "stale"}, 0, "result.applied")
			return err
		})
	}
	staleFailure := func(tx app.WriteTx) error { return tx.FinishJob(jobID, old.AttemptGeneration, "old failure", nil) }
	for _, finish := range []func(app.WriteTx) error{staleSuccess, staleFailure} {
		{
			err := s.Write(ctx, finish)
			Expect(!errors.Is(err, domain.ErrStaleWorker)).To(BeFalse(), func() string { return fmt.Sprintf("stale worker applied: %v", err) })
		}

	}
	var fresh domain.Job
	{
		err := s.Write(ctx, func(tx app.WriteTx) error {
			if err := tx.ReconcileJob(jobID); err != nil {
				return err
			}
			var err error
			fresh, err = tx.ClaimJob(jobID)
			return err
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	Expect(fresh.AttemptGeneration <= interrupted.AttemptGeneration).To(BeFalse(), func() string { return fmt.Sprint("reassignment generation not advanced") })
	{

		err := s.Write(ctx, func(tx app.WriteTx) error {
			return tx.FinishJob(jobID, fresh.AttemptGeneration, "", func(tx app.WriteTx) error {
				_, err := tx.MutateResource(domain.Resource{Kind: "result", ID: "current"}, 0, "result.applied")
				return err
			})
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	retryID := id(t)
	{
		err := s.Write(ctx, func(tx app.WriteTx) error {
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
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	var running, cancelled domain.Job
	{
		err := s.Write(ctx, func(tx app.WriteTx) error {
			var err error
			running, err = tx.ClaimJob(retryID)
			if err != nil {
				return err
			}
			cancelled, err = tx.CancelJob(retryID)
			return err
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	Expect(cancelled.State != "cancelling" || !cancelled.CancelRequested || cancelled.AttemptGeneration <= running.AttemptGeneration).To(BeFalse(), func() string { return fmt.Sprint("cancel fence missing") })

	for _, failure := range []string{"", "late failure"} {
		{
			err := s.Write(ctx, func(tx app.WriteTx) error { return tx.FinishJob(retryID, running.AttemptGeneration, failure, nil) })
			Expect(!errors.Is(err, domain.ErrStaleWorker)).To(BeFalse(), func() string { return fmt.Sprint("cancelled worker finalized", err) })
		}

	}
	{
		err := s.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(retryID, cancelled.AttemptGeneration) })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		_, err := s.Resource(ctx, "result", "stale")
		Expect(!errors.Is(err, domain.ErrNotFound)).To(BeFalse(), func() string { return fmt.Sprint("stale result visible") })
	}

	final, _ := s.Job(ctx, retryID)
	Expect(final.State != "cancelled" || !final.CancelRequested).To(BeFalse(), func() string { return fmt.Sprint("cancel intent lost") })

})

func createUpload(t GinkgoTInterface, s *Store) domain.Upload {
	GinkgoHelper()
	u := domain.Upload{ID: id(t), PrincipalID: id(t), Purpose: "result", InputMode: "directory", SelectionName: "目录", Target: []byte(fmt.Sprintf(`{"project_id":%q,"document_id":null}`, id(t))), Checkpoint: domain.Checkpoint{Generation: 1, InventorySHA256: digest(nil), AcceptedBytes: "0"}}
	{
		err := s.Write(ctx, func(tx app.WriteTx) error { return tx.CreateUpload(u) })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	stored, err := s.Upload(ctx, u.ID)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return stored
}
func appendFixtureRange(t GinkgoTInterface, s *Store, u domain.Upload) domain.Upload {
	GinkgoHelper()
	bytes := []byte("labelled verified range fixture")
	object := domain.Object{Hash: digest(bytes), Size: int64(len(bytes))}
	{
		err := s.Write(ctx, func(tx app.WriteTx) error {
			if err := tx.PutDurableObject(object); err != nil {
				return err
			}
			_, err := tx.RecordRange(domain.UploadRange{UploadID: u.ID, Path: "nested/한글.unknown", Size: object.Size, Hash: object.Hash}, u.Checkpoint.Generation, digest([]byte("inventory1")))
			return err
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	stored, err := s.Upload(ctx, u.ID)
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return stored
}
func completeFixture(t GinkgoTInterface, s *Store, u domain.Upload) domain.Checkpoint {
	GinkgoHelper()
	var cp domain.Checkpoint
	{
		err := s.Write(ctx, func(tx app.WriteTx) error {
			var err error
			cp, err = tx.CompleteCheckpoint(u.ID, u.Checkpoint.Generation, u.Checkpoint.InventorySHA256)
			return err
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	return cp
}
func completeRequest(t GinkgoTInterface, s *Store, u domain.Upload, cp domain.Checkpoint) (domain.Request, domain.Response) {
	GinkgoHelper()
	r := request(t)
	r.Key.PrincipalID = u.PrincipalID
	r.Method = "POST"
	r.Target = "/api/v1/uploads/" + u.ID + "/complete"
	r.Body = jsonBytes(cp)
	response, err := app.Execute(ctx, s, &fixtureAuthority{}, r, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		return tx.CompleteUpload(u.ID, cp, id(t))
	})
	Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })

	return r, response
}

var _ = It("TestUploadCheckpointCoalescingCancellationAbandonment", Label("persistence"), func() {
	t := GinkgoT()

	_, writer := profile(t)
	s := openStore(t, writer, nil)
	u := appendFixtureRange(t, s, createUpload(t, s))
	cp := completeFixture(t, s, u)
	first, r1 := completeRequest(t, s, u, cp)
	second, r2 := completeRequest(t, s, u, cp)
	Expect(first.Key == second.Key || r1.Status != 202 || !reflect.DeepEqual(r1, r2)).To(BeFalse(), func() string { return fmt.Sprint("same-checkpoint requests did not own202/sharedjob") })

	stored, _ := s.Upload(ctx, u.ID)
	jobID := stored.SealJobID
	for _, key := range []domain.OperationKey{first.Key, second.Key} {
		receipt, err := s.Receipt(ctx, key)
		Expect(err != nil || receipt.State != "completed" || receipt.Response.Status != 202).To(BeFalse(), func() string { return fmt.Sprint("completion receipt not definitive", err) })

	}
	different := cp
	different.Generation++
	{
		err := s.Write(ctx, func(tx app.WriteTx) error { _, err := tx.CompleteUpload(u.ID, different, id(t)); return err })
		Expect(!errors.Is(err, domain.ErrTransition)).To(BeFalse(), func() string { return fmt.Sprint("different active checkpoint accepted", err) })
	}

	var running, cancelled domain.Job
	{
		err := s.Write(ctx, func(tx app.WriteTx) error {
			var err error
			running, err = tx.ClaimJob(jobID)
			if err != nil {
				return err
			}
			cancelled, err = tx.CancelJob(jobID)
			return err
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	stored, _ = s.Upload(ctx, u.ID)
	Expect(stored.State != "sealing").To(BeFalse(), func() string { return fmt.Sprint("upload reopened before stop confirmation") })
	{

		err := s.Write(ctx, func(tx app.WriteTx) error { return tx.FinishSeal(jobID, running.AttemptGeneration, "no-set") })
		Expect(!errors.Is(err, domain.ErrStaleWorker)).To(BeFalse(), func() string { return fmt.Sprint("late seal ignored fence", err) })
	}

	s.Close()
	s = openStore(t, writer, nil)
	stored, _ = s.Upload(ctx, u.ID)
	Expect(stored.State != "sealing" || stored.Checkpoint != cp).To(BeFalse(), func() string { return fmt.Sprint("restart lost coupled fence/input") })

	recoveredCancel, loadErr := s.Job(ctx, jobID)
	Expect(loadErr != nil || recoveredCancel.State != "interrupted" || !recoveredCancel.CancelRequested).To(BeFalse(), func() string {
		return fmt.Sprintf("cancelled restart not recoverable: %+v %v", recoveredCancel, loadErr)
	})

	cancelled = recoveredCancel
	{
		err := s.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(jobID, cancelled.AttemptGeneration) })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	stored, _ = s.Upload(ctx, u.ID)
	Expect(stored.State != "open" || stored.Checkpoint != cp).To(BeFalse(), func() string { return fmt.Sprint("confirmed cancel lost bytes/checkpoint") })

	replay, err := app.Execute(ctx, s, &fixtureAuthority{}, first, nil, func(app.WriteTx, domain.Receipt) (domain.Response, error) {
		Fail(fmt.Sprint("old key redispatched"))
		return domain.Response{}, nil
	})
	Expect(err != nil || !reflect.DeepEqual(r1, replay)).To(BeFalse(), func() string { return fmt.Sprint("cancel rewrote original202", err) })

	_, newResponse := completeRequest(t, s, u, cp)
	Expect(newResponse.Status != 202).To(BeFalse(), func() string { return fmt.Sprint("new completion not accepted") })

	stored, _ = s.Upload(ctx, u.ID)
	Expect(stored.SealJobID == jobID).To(BeFalse(), func() string { return fmt.Sprint("terminal seal job resumed") })

	newJobID := stored.SealJobID
	{
		err := s.Write(ctx, func(tx app.WriteTx) error {
			j, err := tx.ClaimJob(newJobID)
			if err != nil {
				return err
			}
			running = j
			if err := tx.AcquirePin(domain.Pin{ID: "recovery", Kind: "recovery", OwnerID: newJobID, Hash: digest([]byte("labelled verified range fixture"))}); err != nil {
				return err
			}
			return tx.AbandonUpload(u.ID)
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	stored, _ = s.Upload(ctx, u.ID)
	Expect(stored.State != "abandoned").To(BeFalse(), func() string { return fmt.Sprint("abandon not immediate") })

	for _, finish := range []func(app.WriteTx) error{
		func(tx app.WriteTx) error { return tx.FinishSeal(newJobID, running.AttemptGeneration, "no-set") },
		func(tx app.WriteTx) error {
			return tx.FinishJob(newJobID, running.AttemptGeneration, "late failure", nil)
		},
		func(tx app.WriteTx) error {
			return tx.AdoptUploadObject(u.ID, u.Purpose, u.Target, domain.Reference{Kind: "saved", ID: "late", Hash: digest([]byte("labelled verified range fixture"))}, "")
		},
	} {
		{
			err := s.Write(ctx, finish)
			Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("abandon fence bypassed") })
		}

	}
	current, _ := s.Job(ctx, newJobID)
	{
		err := s.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(newJobID, current.AttemptGeneration) })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	stored, _ = s.Upload(ctx, u.ID)
	Expect(stored.State != "abandoned").To(BeFalse(), func() string { return fmt.Sprint("late stop reopened abandoned upload") })
	{

		err := s.Write(ctx, func(tx app.WriteTx) error {
			_, err := tx.ReserveGC(digest([]byte("labelled verified range fixture")), "gc")
			if !errors.Is(err, domain.ErrPinned) {
				return fmt.Errorf("recovery pin removed by abandon: %v", err)
			}
			return nil
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

})
var _ = It("TestSealedIdenticalCheckpointAndTargetBoundAdoption", Label("persistence"), func() {
	t := GinkgoT()

	_, writer := profile(t)
	s := openStore(t, writer, nil)
	u := appendFixtureRange(t, s, createUpload(t, s))
	cp := completeFixture(t, s, u)
	_, creation := completeRequest(t, s, u, cp)
	stored, _ := s.Upload(ctx, u.ID)
	setID := id(t)
	{
		err := s.Write(ctx, func(tx app.WriteTx) error {
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
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	_, already := completeRequest(t, s, u, cp)
	Expect(creation.Status != 202 || already.Status != 200).To(BeFalse(), func() string { return fmt.Sprint("sealed checkpoint response not200") })

	ranges, _ := s.Ranges(ctx, u.ID, "", -1, 1)
	Expect(len(ranges) != 1).To(BeFalse(), func() string { return fmt.Sprint("accepted range unavailable") })

	ref := domain.Reference{Kind: "consumer", ID: "saved", Hash: ranges[0].Hash}
	bad := []byte(fmt.Sprintf(`{"project_id":%q,"document_id":null}`, id(t)))
	{
		err := s.Write(ctx, func(tx app.WriteTx) error { return tx.AdoptUploadObject(u.ID, "result", bad, ref, "") })
		Expect(!errors.Is(err, domain.ErrConflict)).To(BeFalse(), func() string { return fmt.Sprint("upload retargeted", err) })
	}
	{

		err := s.Write(ctx, func(tx app.WriteTx) error { return tx.AdoptUploadObject(u.ID, "result", u.Target, ref, "") })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		err := s.Write(ctx, func(tx app.WriteTx) error { return tx.AbandonUpload(u.ID) })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		err := s.Write(ctx, func(tx app.WriteTx) error {
			_, err := tx.ReserveGC(ref.Hash, "gc")
			if !errors.Is(err, domain.ErrPinned) {
				return fmt.Errorf("adopted consumer removed by abandon: %v", err)
			}
			return nil
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

})
var _ = It("TestUploadPurposeModesEmptyCheckpointAndCancelRecoveryStates", Label("persistence"), func() {
	t := GinkgoT()

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
		Expect((err == nil) != test.valid).To(BeFalse(), func() string {
			return fmt.Sprintf("purpose=%s mode=%s valid=%v err=%v", test.purpose, test.mode, test.valid, err)
		})

	}
	invalid := base
	invalid.ID = id(t)
	invalid.Checkpoint.InventorySHA256 = digest([]byte("not-empty"))
	{
		err := s.Write(ctx, func(tx app.WriteTx) error { return tx.CreateUpload(invalid) })
		Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("empty initial checkpoint accepted a nonempty digest") })
	}

	for _, state := range []string{"running", "cancelling", "paused", "reconciling"} {
		jobID := id(t)
		{
			err := s.Write(ctx, func(tx app.WriteTx) error { return tx.CreateJob(domain.Job{ID: jobID, Kind: "import"}) })
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
		}

		s.Close()
		raw := readDB(t, writer.Profile)
		if _, err := raw.Exec("UPDATE jobs SET state=?,attempt_generation=2,cancel_requested=1 WHERE job_id=?", state, jobID); err != nil {
			raw.Close()
			Fail(fmt.Sprint(err))
		}
		raw.Close()
		s = openStore(t, writer, nil)
		recovered, err := s.Job(ctx, jobID)
		Expect(err != nil || recovered.State != "interrupted" || recovered.AttemptGeneration != 3 || !recovered.CancelRequested).To(BeFalse(), func() string { return fmt.Sprintf("recover %s: %+v %v", state, recovered, err) })
		{

			err := s.Write(ctx, func(tx app.WriteTx) error { return tx.ReconcileJob(jobID) })
			Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("reconcile cancel %s: %v", state, err) })
		}

		terminal, _ := s.Job(ctx, jobID)
		Expect(terminal.State != "cancelled" || !terminal.CancelRequested).To(BeFalse(), func() string { return fmt.Sprint("reconciliation lost cancellation") })

	}
})
