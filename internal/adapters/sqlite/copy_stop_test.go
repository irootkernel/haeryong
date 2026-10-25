package sqlite

import (
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func cr5OwnedStore() (*Store, *service.LockedProfile) {
	GinkgoHelper()
	s, core, w, _ := authFixture(GinkgoT())
	pairOwner(GinkgoT(), core, "CR5 owner")
	return s, w
}

// These are real SQLite ledger/ownership fixtures, not physical copy completion
// claims. The production dispatcher/drain consumers separately exercise Submit.
func cr5CopyJob(s *Store, kind, stage string) (domain.Job, domain.CopySet) {
	GinkgoHelper()
	j := domain.Job{ID: id(GinkgoT()), Kind: kind}
	set := domain.CopySet{ID: id(GinkgoT()), JobID: j.ID, Kind: kind, Stage: stage,
		Offset: 17, AfterHash: strings.Repeat("b", 64),
		Snapshot: "retained-snapshot", Capturing: stage == "capturing"}
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		lib, e := tx.Library()
		if e != nil {
			return e
		}
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		set.OwnerID = owner.OwnerID
		set.LibraryID, set.Runtime = lib.ID, lib.RuntimeGeneration
		j.Scope = jsonBytes(map[string]string{"principal_id": set.OwnerID, "copy_set_id": set.ID})
		if e = tx.CreateJob(j); e != nil {
			return e
		}
		if e = tx.PutCopySet(set); e != nil {
			return e
		}
		hash := strings.Repeat("a", 64)
		if e = tx.PutDurableObject(domain.Object{Hash: hash, Size: 3}); e != nil {
			return e
		}
		for _, pin := range []domain.Pin{{ID: "copy-pin", Kind: "backup", OwnerID: set.ID, Hash: hash}, {ID: "other-pin", Kind: "job", OwnerID: "independent", Hash: hash}} {
			if e = tx.AcquirePin(pin); e != nil {
				return e
			}
		}
		return nil
	})).To(Succeed())
	var e error
	j, e = s.Job(ctx, j.ID)
	Expect(e).NotTo(HaveOccurred())
	return j, set
}

// Compare complete rows, including progress, revisions, pin ownership and outbox.
func cr5StopRows(s *Store) map[string][][]any {
	GinkgoHelper()
	out := map[string][][]any{}
	for _, table := range []string{"jobs", "copy_sets", "object_pins", "events"} {
		rows, e := s.db.Query("SELECT * FROM " + table + " ORDER BY 1")
		Expect(e).NotTo(HaveOccurred())
		columns, e := rows.Columns()
		Expect(e).NotTo(HaveOccurred())
		out[table] = [][]any{}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			Expect(rows.Scan(pointers...)).To(Succeed())
			for i, value := range values {
				if b, ok := value.([]byte); ok {
					values[i] = append([]byte(nil), b...)
				}
			}
			out[table] = append(out[table], values)
		}
		Expect(rows.Err()).NotTo(HaveOccurred())
		Expect(rows.Close()).To(Succeed())
	}
	return out
}

func cr5Claim(s *Store, id string) domain.Job {
	GinkgoHelper()
	var j domain.Job
	Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; j, e = tx.ClaimJob(id); return e })).To(Succeed())
	return j
}

func cr5Cancel(s *Store, id string) domain.Job {
	GinkgoHelper()
	var j domain.Job
	Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; j, e = tx.CancelJob(id); return e })).To(Succeed())
	return j
}

func cr5CurrentEvent(s *Store, j domain.Job) {
	GinkgoHelper()
	var revision int64
	Expect(s.db.QueryRow("SELECT revision FROM events WHERE resource_kind='job' AND resource_id=? ORDER BY sequence DESC LIMIT 1", j.ID).Scan(&revision)).To(Succeed())
	Expect(revision).To(Equal(j.Revision))
}

var _ = DescribeTable("CR5 stop unowned generic ledger retains cancellation recovery and exact noops", func(kind, mode, shape string) {
	_, w := profile(GinkgoT())
	s := openStore(GinkgoT(), w, nil)
	j := domain.Job{ID: id(GinkgoT()), Kind: kind}
	if shape == "captured" {
		j.Scope = []byte(`{"legacy_selection":"exact"}`)
	}
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.CreateJob(j) })).To(Succeed())
	if mode != "queued" {
		cr5Claim(s, j.ID)
	}
	cancelled := cr5Cancel(s, j.ID)
	if mode == "reopen" {
		Expect(s.Close()).To(Succeed())
		s = openStore(GinkgoT(), w, nil)
		recovered, e := s.Job(ctx, j.ID)
		Expect(e).NotTo(HaveOccurred())
		Expect(recovered.State).To(Equal("interrupted"))
		Expect(recovered.CancelRequested).To(BeTrue())
		Expect(recovered.AttemptGeneration).To(Equal(cancelled.AttemptGeneration + 1))
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ReconcileJob(j.ID) })).To(Succeed())
	} else if mode == "running" {
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(j.ID, cancelled.AttemptGeneration) })).To(Succeed())
	}
	current, e := s.Job(ctx, j.ID)
	Expect(e).NotTo(HaveOccurred())
	Expect(current.State).To(Equal("cancelled"))
	Expect(current.CancelRequested).To(BeTrue())
	cr5CurrentEvent(s, current)
	before := cr5StopRows(s)
	Expect(before["copy_sets"]).To(BeEmpty())
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(j.ID, current.AttemptGeneration) })).To(Succeed())
	cr5Cancel(s, j.ID)
	Expect(cr5StopRows(s)).To(Equal(before))
}, func() []TableEntry {
	var entries []TableEntry
	for _, kind := range []string{"backup", "export"} {
		for _, mode := range []string{"queued", "running", "reopen"} {
			for _, shape := range []string{"default", "captured"} {
				entries = append(entries, Entry(kind+" "+mode+" "+shape, kind, mode, shape))
			}
		}
	}
	return entries
}(), Label("persistence"))

var _ = DescribeTable("CR5 stop owned copy preserves checkpoints pins barriers and repeated stop is atomic noop", func(kind, stage string) {
	s, _ := cr5OwnedStore()
	j, set := cr5CopyJob(s, kind, stage)
	cr5Claim(s, j.ID)
	cancelling := cr5Cancel(s, j.ID)
	before := cr5StopRows(s)
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(j.ID, cancelling.AttemptGeneration) })).To(Succeed())
	changes := int64(1)
	if stage != "interrupted" && stage != "complete" && stage != "abandoned" && stage != "expired" && stage != "restored_paused" {
		set.Stage = "interrupted"
		changes++ // A real state transition and a real progress change.
	}
	var actual domain.CopySet
	Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; actual, e = tx.CopySet(set.ID); return e })).To(Succeed())
	Expect(actual).To(Equal(set))
	current, e := s.Job(ctx, j.ID)
	Expect(e).NotTo(HaveOccurred())
	Expect(current.State).To(Equal("cancelled"))
	Expect(current.Revision).To(Equal(cancelling.Revision + changes))
	Expect(current.AttemptGeneration).To(Equal(cancelling.AttemptGeneration))
	cr5CurrentEvent(s, current)
	after := cr5StopRows(s)
	Expect(after["object_pins"]).To(Equal(before["object_pins"]))
	Expect(len(after["events"]) - len(before["events"])).To(Equal(int(changes)))
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(j.ID, current.AttemptGeneration) })).To(Succeed())
	cr5Cancel(s, j.ID)
	Expect(cr5StopRows(s)).To(Equal(after))
}, func() []TableEntry {
	var entries []TableEntry
	for _, kind := range []string{"backup", "export"} {
		for _, stage := range []string{"queued", "capturing", "copying", "publishing", "failed", "interrupted", "complete", "abandoned", "expired", "restored_paused"} {
			entries = append(entries, Entry(kind+" "+stage, kind, stage))
		}
	}
	return entries
}(), Label("persistence"))

var _ = DescribeTable("CR5 stop malformed declared or reverse ownership rolls back all effects", func(kind, damage string) {
	s, _ := cr5OwnedStore()
	j, set := cr5CopyJob(s, kind, "copying")
	raw := string(j.Scope)
	if strings.HasSuffix(damage, " unowned") {
		_, e := s.db.Exec("DELETE FROM copy_sets WHERE set_id=?", set.ID)
		Expect(e).NotTo(HaveOccurred())
		damage = strings.TrimSuffix(damage, " unowned")
	}
	switch damage {
	case "missing":
		raw = `{}`
	case "empty":
		raw = `{"copy_set_id":""}`
	case "null identity":
		raw = `{"copy_set_id":null}`
	case "number identity":
		raw = `{"copy_set_id":7}`
	case "malformed":
		raw = `{`
	case "null scope":
		raw = `null`
	case "array scope":
		raw = `[]`
	case "value identity":
		_, e := s.db.Exec("UPDATE copy_sets SET value=json_set(value,'$.ID',?) WHERE set_id=?", id(GinkgoT()), set.ID)
		Expect(e).NotTo(HaveOccurred())
	case "value binding":
		_, e := s.db.Exec("UPDATE copy_sets SET value=json_set(value,'$.JobID',?) WHERE set_id=?", id(GinkgoT()), set.ID)
		Expect(e).NotTo(HaveOccurred())
	case "indexed binding":
		_, e := s.db.Exec("UPDATE copy_sets SET job_id=NULL WHERE set_id=?", set.ID)
		Expect(e).NotTo(HaveOccurred())
	case "duplicate reverse":
		other := set
		other.ID = id(GinkgoT())
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutCopySet(other) })).To(Succeed())
	case "dangling":
		_, e := s.db.Exec("DELETE FROM copy_sets WHERE set_id=?", set.ID)
		Expect(e).NotTo(HaveOccurred())
	case "foreign kind":
		set.Kind = "backup"
		if kind == "backup" {
			set.Kind = "export"
		}
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutCopySet(set) })).To(Succeed())
	case "foreign binding":
		other := set
		other.ID, other.JobID = id(GinkgoT()), ""
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.PutCopySet(other) })).To(Succeed())
		raw = string(jsonBytes(map[string]string{"copy_set_id": other.ID}))
	}
	_, e := s.db.Exec("UPDATE jobs SET scope=? WHERE job_id=?", []byte(raw), j.ID)
	Expect(e).NotTo(HaveOccurred())
	before := cr5StopRows(s)
	Expect(s.Write(ctx, func(tx app.WriteTx) error { _, e := tx.CancelJob(j.ID); return e })).NotTo(Succeed())
	Expect(cr5StopRows(s)).To(Equal(before))
	cr5Claim(s, j.ID)
	cancelling := cr5Cancel(s, j.ID)
	before = cr5StopRows(s)
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(j.ID, cancelling.AttemptGeneration) })).NotTo(Succeed())
	Expect(cr5StopRows(s)).To(Equal(before))
}, func() []TableEntry {
	var entries []TableEntry
	for _, kind := range []string{"backup", "export"} {
		for _, damage := range []string{"missing", "empty", "null identity", "number identity", "malformed", "null scope", "array scope", "dangling", "foreign kind", "foreign binding", "indexed binding", "value identity", "value binding", "duplicate reverse", "empty unowned", "null identity unowned", "number identity unowned", "malformed unowned", "null scope unowned", "array scope unowned"} {
			entries = append(entries, Entry(kind+" "+damage, kind, damage))
		}
	}
	return entries
}(), Label("persistence"))

var _ = DescribeTable("CR5 stop stale attempts and old linked jobs cannot mutate current copy", func(kind string) {
	s, _ := cr5OwnedStore()
	j, set := cr5CopyJob(s, kind, "copying")
	running := cr5Claim(s, j.ID)
	cancelling := cr5Cancel(s, j.ID)
	before := cr5StopRows(s)
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(j.ID, running.AttemptGeneration) })).To(MatchError(domain.ErrStaleWorker))
	Expect(cr5StopRows(s)).To(Equal(before))
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(j.ID, cancelling.AttemptGeneration) })).To(Succeed())
	for _, stage := range []string{"copying", "abandoned"} {
		Expect(s.Write(ctx, func(tx app.WriteTx) error {
			if stage == "copying" {
				next, e := tx.RetryJob(j.ID, id(GinkgoT()))
				if e != nil {
					return e
				}
				set.JobID = next.ID
			}
			set.Stage = stage
			return tx.PutCopySet(set)
		})).To(Succeed())
		before = cr5StopRows(s)
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(j.ID, cancelling.AttemptGeneration) })).To(Succeed())
		Expect(cr5StopRows(s)).To(Equal(before))
	}
}, Entry("backup", "backup"), Entry("export", "export"), Label("persistence"))

var _ = DescribeTable("CR5 stop commit rollback and reopen retain ownership until durable confirmation", func(kind string) {
	s, w := cr5OwnedStore()
	j, _ := cr5CopyJob(s, kind, "capturing")
	cr5Claim(s, j.ID)
	cancelling := cr5Cancel(s, j.ID)
	before := cr5StopRows(s)
	fault := errors.New("CR5 stop commit failure")
	s.fault = func(b Boundary) error {
		if b == BeforeCommit {
			return fault
		}
		return nil
	}
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(j.ID, cancelling.AttemptGeneration) })).To(MatchError(fault))
	s.fault = nil
	Expect(cr5StopRows(s)).To(Equal(before))
	Expect(s.Close()).To(Succeed())
	s = openStore(GinkgoT(), w, nil)
	recovered, e := s.Job(ctx, j.ID)
	Expect(e).NotTo(HaveOccurred())
	Expect(recovered.State).To(Equal("interrupted"))
	Expect(recovered.CancelRequested).To(BeTrue())
	Expect(recovered.AttemptGeneration).To(Equal(cancelling.AttemptGeneration + 1))
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ReconcileJob(j.ID) })).To(Succeed())
	after := cr5StopRows(s)
	Expect(after["object_pins"]).To(Equal(before["object_pins"]))
	current, e := s.Job(ctx, j.ID)
	Expect(e).NotTo(HaveOccurred())
	cr5CurrentEvent(s, current)
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.ConfirmStop(j.ID, current.AttemptGeneration) })).To(Succeed())
	Expect(cr5StopRows(s)).To(Equal(after))
}, Entry("backup", "backup"), Entry("export", "export"), Label("persistence"))

var _ = DescribeTable("CR5 definite failure and stale finish keep precise copy failure without cancellation", func(kind string) {
	s, _ := cr5OwnedStore()
	j, set := cr5CopyJob(s, kind, "copying")
	running := cr5Claim(s, j.ID)
	before := cr5StopRows(s)
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return app.FailCopy(tx, set, j.ID, running.AttemptGeneration, "destination_full")
	})).To(Succeed())
	current, e := s.Job(ctx, j.ID)
	Expect(e).NotTo(HaveOccurred())
	Expect(current.State).To(Equal("failed"))
	Expect(current.Error).To(Equal("destination_full"))
	Expect(current.CancelRequested).To(BeFalse())
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		actual, e := tx.CopySet(set.ID)
		Expect(actual.Stage).To(Equal("failed"))
		Expect(actual.Error).To(Equal("destination_full"))
		Expect(actual.Capturing).To(Equal(set.Capturing))
		Expect(actual.AfterHash).To(Equal(set.AfterHash))
		Expect(actual.Offset).To(Equal(set.Offset))
		return e
	})).To(Succeed())
	cr5CurrentEvent(s, current)
	after := cr5StopRows(s)
	Expect(after["object_pins"]).To(Equal(before["object_pins"]))
	for _, failure := range []string{"", "stale failure"} {
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.FinishJob(j.ID, running.AttemptGeneration, failure, nil) })).To(MatchError(domain.ErrStaleWorker))
	}
	cr5Cancel(s, j.ID)
	Expect(cr5StopRows(s)).To(Equal(after))
}, Entry("backup", "backup"), Entry("export", "export"), Label("persistence"))

var _ = DescribeTable("CR5 generic finish and linked retry retain kernel revisions without copy ownership", func(kind string) {
	_, w := profile(GinkgoT())
	s := openStore(GinkgoT(), w, nil)
	j := domain.Job{ID: id(GinkgoT()), Kind: kind}
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.CreateJob(j) })).To(Succeed())
	running := cr5Claim(s, j.ID)
	Expect(s.Write(ctx, func(tx app.WriteTx) error {
		return tx.FinishJob(j.ID, running.AttemptGeneration, "definite local failure", nil)
	})).To(Succeed())
	failed, e := s.Job(ctx, j.ID)
	Expect(e).NotTo(HaveOccurred())
	Expect(failed.State).To(Equal("failed"))
	Expect(failed.Revision).To(Equal(running.Revision + 1))
	cr5CurrentEvent(s, failed)
	var next domain.Job
	Expect(s.Write(ctx, func(tx app.WriteTx) error { var e error; next, e = tx.RetryJob(j.ID, id(GinkgoT())); return e })).To(Succeed())
	Expect(next.ParentID).To(Equal(j.ID))
	Expect(next.ID).NotTo(Equal(j.ID))
	running = cr5Claim(s, next.ID)
	Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.FinishJob(next.ID, running.AttemptGeneration, "", nil) })).To(Succeed())
	completed, e := s.Job(ctx, next.ID)
	Expect(e).NotTo(HaveOccurred())
	Expect(completed.State).To(Equal("completed"))
	Expect(completed.Revision).To(Equal(running.Revision + 1))
	cr5CurrentEvent(s, completed)
	before := cr5StopRows(s)
	Expect(before["copy_sets"]).To(BeEmpty())
	for _, failure := range []string{"", "late failure"} {
		Expect(s.Write(ctx, func(tx app.WriteTx) error { return tx.FinishJob(next.ID, running.AttemptGeneration, failure, nil) })).To(MatchError(domain.ErrStaleWorker))
	}
	cr5Cancel(s, next.ID)
	Expect(cr5StopRows(s)).To(Equal(before))
}, Entry("backup", "backup"), Entry("export", "export"), Label("persistence"))
