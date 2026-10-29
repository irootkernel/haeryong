package source_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/foundation"
	adapter "haeryong.local/haeryong/internal/adapters/source"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/application/work"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func managedFixture(retention string) (*vFixture, string, string) {
	f := newVerificationFixture(GinkgoT())
	h, e := f.store.Health(context.Background())
	Expect(e).NotTo(HaveOccurred())
	f.project = h.Library.UnfiledProjectID
	root := GinkgoT().TempDir()
	id := "managed-source"
	mapping, _ := json.Marshal(domain.ManagedSourceMapping{SourceID: id, Kind: "files", RootPath: root, RetentionMode: retention, ProviderIDs: []string{"codex"}})
	f.configure(string(mapping))
	f.core.Files = adapter.Files{ExcludedPaths: []string{f.dataDir}}
	return f, root, id
}
func managedRequest(id string, paths ...string) source.CaptureRequest {
	selected := []source.CapturePath{}
	for _, path := range paths {
		selected = append(selected, source.CapturePath{Path: path})
	}
	return source.CaptureRequest{SourceMode: "current", Selections: []source.CaptureSelection{{SourceID: id, Paths: selected}}}
}
func captureID(r domain.Response) string {
	var v map[string]any
	Expect(json.Unmarshal(r.Body, &v)).To(Succeed())
	return v["capture_id"].(string)
}

var _ = It("SCAP02 selected snapshots preserve exact streamed bytes offline without producing any input observation", Label("persistence"), func() {
	f, root, id := managedFixture("selected_snapshot")
	body := strings.Repeat("arbitrary source\x00\xff\r\n", 100000)
	Expect(os.WriteFile(filepath.Join(root, "file.unknown"), []byte(body), 0600)).To(Succeed())
	r, e := f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, "file.unknown"))
	Expect(e).NotTo(HaveOccurred())
	view, e := f.core.CaptureView(context.Background(), f.token, captureID(r), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(view["complete"]).To(BeTrue())
	rows := view["items"].([]domain.SourceCaptureRead)
	Expect(rows).To(HaveLen(1))
	Expect(rows[0].Hash).To(Equal(vsha([]byte(body))))
	Expect(os.RemoveAll(root)).To(Succeed())
	var got []byte
	Expect(f.core.ReadCaptured(context.Background(), f.token, rows[0].ID, func(_ domain.SourceCaptureRead, r io.ReadSeeker) error { var e error; got, e = io.ReadAll(r); return e })).To(Succeed())
	Expect(got).To(Equal([]byte(body)))
	pinned := managedRequest(id, "file.unknown")
	pinned.SourceMode = "pinned"
	pinned.Selections[0].Paths[0].ReadID = rows[0].ID
	reused, e := f.core.Capture(context.Background(), f.token, sid(), f.project, pinned)
	Expect(e).NotTo(HaveOccurred())
	reusedView, e := f.core.CaptureView(context.Background(), f.token, captureID(reused), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(reusedView["complete"]).To(BeTrue())
	Expect(reusedView["items"].([]domain.SourceCaptureRead)[0].Hash).To(Equal(rows[0].Hash))
	db, e := sql.Open("sqlite", "file:"+filepath.Join(f.dataDir, "storage", "library.sqlite")+"?mode=ro")
	Expect(e).NotTo(HaveOccurred())
	var observed int
	Expect(db.QueryRow(`SELECT count(*) FROM provider_inputs`).Scan(&observed)).To(Succeed())
	Expect(observed).To(BeZero(), "capturing and rereading bytes create no provider input observation rows")
	Expect(db.Close()).To(Succeed())
	r, e = f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, "file.unknown"))
	Expect(e).NotTo(HaveOccurred())
	view, e = f.core.CaptureView(context.Background(), f.token, captureID(r), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(view["complete"]).To(BeFalse())
	Expect(view["target_set_id"]).To(BeNil())
})
var _ = It("SCAP03 detached policy change fails atomic publication and preserves the full selected denominator", Label("persistence"), func() {
	f, root, id := managedFixture("reference_only")
	Expect(os.WriteFile(filepath.Join(root, "one"), []byte("one"), 0600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(root, "two"), []byte("two"), 0600)).To(Succeed())
	f.core.Fault = func(stage string) error {
		if stage == "capture_before_commit" {
			mapping, _ := json.Marshal(domain.ManagedSourceMapping{SourceID: id, Kind: "files", RootPath: root, RetentionMode: "reference_only", ProviderIDs: []string{}})
			f.configure(string(mapping))
		}
		return nil
	}
	key := sid()
	response, e := f.core.Capture(context.Background(), f.token, key, f.project, managedRequest(id, "one", "two"))
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(412))
	owner, e := f.auth.Authenticate(context.Background(), f.token)
	Expect(e).NotTo(HaveOccurred())
	receipt, e := f.store.Receipt(context.Background(), domain.OperationKey{PrincipalID: owner.PrincipalID, OperationID: key})
	Expect(e).NotTo(HaveOccurred())
	var normalized map[string]string
	Expect(json.Unmarshal(receipt.Normalization, &normalized)).To(Succeed())
	view, e := f.core.CaptureView(context.Background(), f.token, normalized["capture_id"], "")
	Expect(e).NotTo(HaveOccurred())
	Expect(view["complete"]).To(BeFalse())
	Expect(view["target_set_id"]).To(BeNil())
	Expect(view["reason"]).To(Equal("config_changed"))
	rows := view["items"].([]domain.SourceCaptureRead)
	Expect(rows).To(HaveLen(2))
	paths := []string{}
	for _, r := range rows {
		var location struct {
			Path string `json:"path"`
		}
		Expect(json.Unmarshal(r.LocationJSON, &location)).To(Succeed())
		paths = append(paths, location.Path)
		Expect(r.Availability).To(Equal("unavailable"))
		Expect(r.Reason).To(Equal("capture_not_completed"))
	}
	Expect(paths).To(ConsistOf("one", "two"))
	denied := managedRequest(id, "one")
	denied.ProviderID = "codex"
	_, e = f.core.Capture(context.Background(), f.token, sid(), f.project, denied)
	Expect(e).To(MatchError(domain.ErrDenied))
})

func sid() string {
	GinkgoHelper()
	id, e := service.NewID()
	Expect(e).NotTo(HaveOccurred())
	return id
}

type suppliedCaptureDriver struct {
	beforeObserve func(string, string) error
	store         *foundation.Store
	observed      bool
	calls         int
	entries       int
	attemptIDs    []string
	supplied      [][]byte
}

func (d *suppliedCaptureDriver) Execute(ctx context.Context, a domain.ProviderAttempt, inputs []domain.ProviderInput, send func() error, observe func(work.ProviderProgress) error) (work.ProviderOutcome, error) {
	d.entries++
	if e := send(); e != nil {
		return work.ProviderOutcome{}, e
	}
	d.calls++
	d.attemptIDs = append(d.attemptIDs, a.ID)
	if d.observed && len(inputs) > 0 {
		input := inputs[0]
		var object domain.Object
		if e := d.store.Write(ctx, func(tx app.WriteTx) error { var e error; object, e = tx.Object(input.Hash); return e }); e != nil {
			return work.ProviderOutcome{}, e
		}
		var body []byte
		if e := d.store.Read(ctx, object, domain.Pin{Kind: "fixture_provider_input", OwnerID: a.ID}, func(r io.ReadSeeker) error { var e error; body, e = io.ReadAll(r); return e }); e != nil {
			return work.ProviderOutcome{}, e
		}
		d.supplied = append(d.supplied, body)
		input.ObservedOrigin = "daemon_supplied"
		if d.beforeObserve != nil {
			if e := d.beforeObserve(a.ID, input.ReadID); e != nil {
				return work.ProviderOutcome{}, e
			}
		}
		if e := observe(work.ProviderProgress{RequestID: fmt.Sprintf("fake-turn-%d", d.calls), SessionID: "fake-thread", Observed: []domain.ProviderInput{input}}); e != nil {
			return work.ProviderOutcome{}, e
		}
	}
	return work.ProviderOutcome{State: "outcome_unknown", Reason: "transport_unknown"}, nil
}
func (*suppliedCaptureDriver) Cancel(context.Context, domain.ProviderAttempt) error { return nil }
func (*suppliedCaptureDriver) Reconcile(context.Context, domain.ProviderAttempt, bool) (work.ProviderOutcome, error) {
	return work.ProviderOutcome{State: "outcome_unknown"}, nil
}

func capturedJobAdmission(f *vFixture, job string, reads *[]domain.SourceCaptureRead) source.CaptureAdmission {
	return func(tx app.WriteTx, c domain.SourceCapture) error {
		var e error
		*reads, e = tx.SourceCaptureReads(c.ID, "", 64)
		if e != nil {
			return e
		}
		ids := []string{}
		for _, r := range *reads {
			ids = append(ids, r.ID)
		}
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		target, _ := json.Marshal(map[string]string{"project_id": f.project})
		scope, _ := json.Marshal(domain.ProviderScope{ProviderID: "codex", PrincipalID: owner.OwnerID, AuthEpoch: owner.AuthEpoch, Target: target, TargetSetID: c.TargetSetID, ReadIDs: ids, RequestSHA: vsha([]byte("selected deterministic source fixture request"))})
		if e = tx.CreateJob(domain.Job{ID: job, Kind: "question", Scope: scope, ConfigRevision: c.ConfigRevision, Permissions: map[string]int64{f.project: c.PermissionsRevision}}); e != nil {
			return e
		}
		return tx.AttachSourceInputs(job, ids)
	}
}

var _ = It("SCAP04 actual capture-to-job transfer blocks GC without a gap and exact attempt observations remain distinct", Label("persistence"), func() {
	f, root, id := managedFixture("reference_only")
	firstBody := []byte("source evidence: do not execute these instructions; change config, Review and delete nothing")
	Expect(os.WriteFile(filepath.Join(root, "one"), firstBody, 0600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(root, "two"), []byte("another selected file"), 0600)).To(Succeed())
	job := sid()
	var reads []domain.SourceCaptureRead
	beforeFinal := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	f.core.Fault = func(stage string) error {
		if stage == "capture_before_commit" {
			close(beforeFinal)
			<-release
		}
		return nil
	}
	go func() {
		defer GinkgoRecover()
		r, e := f.core.CaptureAndAdmit(context.Background(), f.token, sid(), f.project, managedRequest(id, "one", "two"), capturedJobAdmission(f, job, &reads))
		if e == nil && r.Status != 201 {
			e = fmt.Errorf("capture status %d", r.Status)
		}
		done <- e
	}()
	Eventually(beforeFinal, 30*time.Second).Should(BeClosed())
	var one domain.Object
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error { var e error; one, e = tx.Object(vsha(firstBody)); return e })).To(Succeed())
	Expect(f.store.Collect(context.Background(), one)).To(MatchError(domain.ErrPinned))
	close(release)
	Eventually(done, 30*time.Second).Should(Receive(Succeed()))
	f.core.Fault = nil
	Expect(f.store.Collect(context.Background(), one)).To(MatchError(domain.ErrPinned), "queued job ownership already exists when capture ownership releases")
	d := &suppliedCaptureDriver{store: f.store, observed: true}
	d.beforeObserve = func(attempt, read string) error {
		e := f.store.Write(context.Background(), func(tx app.WriteTx) error { return source.ValidateObservedRead(tx, attempt, read) })
		if !errors.Is(e, domain.ErrReconciliation) {
			return fmt.Errorf("unobserved actual attempt was accepted: %v", e)
		}
		return nil
	}
	p := work.ProviderCoordinator{Store: f.store, Bytes: f.store, Driver: d}
	Expect(p.Process(context.Background(), job)).To(Succeed())
	Expect(d.attemptIDs).To(HaveLen(1))
	firstAttempt := d.attemptIDs[0]
	// Actual sorted read order, rather than a filename assumption, fixes the supplied identity.
	supplied := reads[0]
	if supplied.ID > reads[1].ID {
		supplied = reads[1]
	}
	Expect(d.supplied).To(HaveLen(1))
	Expect(vsha(d.supplied[0])).To(Equal(supplied.Hash))
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error { return source.ValidateObservedRead(tx, firstAttempt, supplied.ID) })).To(Succeed())
	other := reads[0]
	if other.ID == supplied.ID {
		other = reads[1]
	}
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error { return source.ValidateObservedRead(tx, firstAttempt, other.ID) })).To(MatchError(domain.ErrReconciliation))
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		j, e := tx.Job(job)
		if e != nil {
			return e
		}
		_, e = tx.ResumeProviderJob(job, j.AttemptGeneration, "retry")
		return e
	})).To(Succeed())
	d.observed = false
	Expect(p.Process(context.Background(), job)).To(Succeed())
	Expect(d.attemptIDs).To(HaveLen(2))
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error { return source.ValidateObservedRead(tx, d.attemptIDs[1], supplied.ID) })).To(MatchError(domain.ErrReconciliation))
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error { _, e := tx.CancelJob(job); return e })).To(Succeed())
	Expect(f.store.Collect(context.Background(), one)).To(MatchError(domain.ErrPinned), "cancel does not release unknown effects")
	Expect(f.store.Write(context.Background(), func(tx app.WriteTx) error {
		for _, a := range d.attemptIDs {
			if e := tx.ObserveProviderAttempt(a, "cancelled", "", "", "cancel_confirmed"); e != nil {
				return e
			}
		}
		j, e := tx.Job(job)
		if e != nil {
			return e
		}
		return tx.ConfirmStop(job, j.AttemptGeneration)
	})).To(Succeed())
	Expect(f.store.Collect(context.Background(), one)).To(Succeed())
})

var _ = It("SCAP07 incomplete selection preserves every path and reason and inspection/replay performs no new source reads", Label("persistence"), func() {
	f, root, id := managedFixture("selected_snapshot")
	Expect(os.WriteFile(filepath.Join(root, "present"), []byte("actual selected bytes"), 0600)).To(Succeed())
	key := sid()
	request := managedRequest(id, "present", "missing")
	response, e := f.core.Capture(context.Background(), f.token, key, f.project, request)
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(201))
	capture := captureID(response)
	Expect(os.RemoveAll(root)).To(Succeed())
	view, e := f.core.CaptureView(context.Background(), f.token, capture, "")
	Expect(e).NotTo(HaveOccurred())
	Expect(view["complete"]).To(BeFalse())
	Expect(view["target_set_id"]).To(BeNil())
	rows := view["items"].([]domain.SourceCaptureRead)
	Expect(rows).To(HaveLen(2))
	reasons := map[string]string{}
	for _, row := range rows {
		var loc struct {
			Path string `json:"path"`
		}
		Expect(json.Unmarshal(row.LocationJSON, &loc)).To(Succeed())
		reasons[loc.Path] = row.Reason
		Expect(row.Availability).To(Equal("unavailable"))
	}
	Expect(reasons["missing"]).To(Equal("missing_source"))
	Expect(reasons["present"]).To(Equal("capture_not_completed"))
	replay, e := f.core.Capture(context.Background(), f.token, key, f.project, request)
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(response))
})

type inspectedCaptureFiles struct {
	open func(context.Context, string, string) (io.ReadCloser, error)
}

func (r inspectedCaptureFiles) Open(ctx context.Context, root, path string) (io.ReadCloser, error) {
	return r.open(ctx, root, path)
}

var _ = It("SCAP27 an accepted unconfigured capture has no aggregate deadline and survives lost caller context", Label("persistence"), func() {
	f, _, id := managedFixture("selected_snapshot")
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	f.core.Files = inspectedCaptureFiles{open: func(ctx context.Context, _, _ string) (io.ReadCloser, error) {
		_, bounded := ctx.Deadline()
		Expect(bounded).To(BeFalse(), "ordinary selected bytes have no implicit total work deadline")
		cancel()
		Expect(ctx.Err()).NotTo(HaveOccurred(), "accepted work is detached from lost caller acknowledgement")
		calls++
		return io.NopCloser(strings.NewReader("exact detached capture")), nil
	}}
	key := sid()
	result, e := f.core.Capture(caller, f.token, key, f.project, managedRequest(id, "one"))
	Expect(e).NotTo(HaveOccurred())
	Expect(result.Status).To(Equal(201))
	var c map[string]any
	Expect(json.Unmarshal(result.Body, &c)).To(Succeed())
	Expect(c["complete"]).To(BeTrue())
	replay, e := f.core.Capture(context.Background(), f.token, key, f.project, managedRequest(id, "one"))
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(result))
	Expect(calls).To(Equal(1))
})

var _ = It("SCAP28 an incomplete capture view never labels unowned bytes as a retained snapshot", Label("persistence"), func() {
	f, root, id := managedFixture("selected_snapshot")
	Expect(os.WriteFile(filepath.Join(root, "one"), []byte("captured before missing"), 0600)).To(Succeed())
	response, e := f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, "one", "missing"))
	Expect(e).NotTo(HaveOccurred())
	view, e := f.core.CaptureView(context.Background(), f.token, captureID(response), "")
	Expect(e).NotTo(HaveOccurred())
	Expect(view["complete"]).To(BeFalse())
	rows := view["items"].([]domain.SourceCaptureRead)
	Expect(rows).To(HaveLen(2))
	for _, row := range rows {
		Expect(row.Availability).To(Equal("unavailable"))
		Expect(row.BytesOrigin).To(BeEmpty(), "unavailable capture bytes have no retained byte origin")
		Expect(row.ReadState).To(Equal("not_read_by_view"))
	}
})

var _ = It("SCAP29 metadata diffs use the same reference-only availability projection without source rereads", Label("persistence"), func() {
	f, root, id := managedFixture("reference_only")
	Expect(os.WriteFile(filepath.Join(root, "one"), []byte("first"), 0600)).To(Succeed())
	first, e := f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, "one"))
	Expect(e).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(root, "one"), []byte("second"), 0600)).To(Succeed())
	second, e := f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, "one"))
	Expect(e).NotTo(HaveOccurred())
	Expect(os.RemoveAll(root)).To(Succeed())
	diff, e := f.core.CompareCaptures(context.Background(), f.token, captureID(first), captureID(second), "")
	Expect(e).NotTo(HaveOccurred())
	changes := diff["changes"].([]domain.SourceCaptureDelta)
	Expect(changes).To(HaveLen(1))
	for _, row := range []*domain.SourceCaptureRead{changes[0].Before, changes[0].After} {
		Expect(row).NotTo(BeNil())
		Expect(row.Availability).To(Equal("unavailable"))
		Expect(row.CaptureAvailability).To(Equal("available"))
		Expect(row.Reason).To(Equal("not_retained"))
		Expect(row.BytesOrigin).To(BeEmpty())
		Expect(row.ReadState).To(Equal("not_read_by_view"))
	}
})

var _ = It("SCAP30 a settled post-reservation capture fault is nonretryable and replays the same terminal response", Label("persistence"), func() {
	f, root, id := managedFixture("selected_snapshot")
	Expect(os.WriteFile(filepath.Join(root, "one"), []byte("captured bytes"), 0600)).To(Succeed())
	f.core.Fault = func(string) error { return context.DeadlineExceeded }
	key := sid()
	response, e := f.core.Capture(context.Background(), f.token, key, f.project, managedRequest(id, "one"))
	Expect(e).NotTo(HaveOccurred())
	Expect(response.Status).To(Equal(503))
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	Expect(json.Unmarshal(response.Body, &body)).To(Succeed())
	Expect(body.Error.Code).To(Equal("SERVICE_RECOVERING"))
	Expect(body.Error.Retryable).To(BeFalse(), "a terminal receipt never suggests rerunning the same operation")
	f.core.Fault = nil
	replay, e := f.core.Capture(context.Background(), f.token, key, f.project, managedRequest(id, "one"))
	Expect(e).NotTo(HaveOccurred())
	Expect(replay).To(Equal(response))
})

var _ = It("SCAP31 capture and replay do not emit an unsupported source observation event", Label("persistence"), func() {
	f, root, id := managedFixture("selected_snapshot")
	Expect(os.WriteFile(filepath.Join(root, "one"), []byte("ordinary source"), 0600)).To(Succeed())
	h, e := f.store.Health(context.Background())
	Expect(e).NotTo(HaveOccurred())
	before, e := f.store.Events(context.Background(), h.Library.StreamEpoch, 0, 1000)
	Expect(e).NotTo(HaveOccurred())
	key := sid()
	first, e := f.core.Capture(context.Background(), f.token, key, f.project, managedRequest(id, "one"))
	Expect(e).NotTo(HaveOccurred())
	_, e = f.core.Capture(context.Background(), f.token, key, f.project, managedRequest(id, "one"))
	Expect(e).NotTo(HaveOccurred())
	Expect(first.Status).To(Equal(201))
	failed, e := f.core.Capture(context.Background(), f.token, sid(), f.project, managedRequest(id, "missing"))
	Expect(e).NotTo(HaveOccurred())
	var incomplete map[string]any
	Expect(json.Unmarshal(failed.Body, &incomplete)).To(Succeed())
	Expect(incomplete["complete"]).To(BeFalse())
	after, e := f.store.Events(context.Background(), h.Library.StreamEpoch, 0, 1000)
	Expect(e).NotTo(HaveOccurred())
	Expect(after).To(Equal(before), "capture is an exact read receipt, not a source freshness observation")
})
