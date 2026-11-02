package importer_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/questions"
	"haeryong.local/haeryong/internal/application/source"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// The first request has completed its owned input preparation and holds no
// writer transaction. Its live server context deliberately survives the client
// losing its response. Later requests can reach the actual admission writer.
type heldQuestionPreparation struct {
	source.CaptureBytes
	first   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (b *heldQuestionPreparation) Put(ctx context.Context, reader io.Reader, commit func(app.WriteTx, domain.Object) error) (domain.Object, error) {
	object, err := b.CaptureBytes.Put(ctx, reader, commit)
	if err != nil {
		return object, err
	}
	if b.first.CompareAndSwap(false, true) {
		close(b.entered)
		select {
		case <-b.release:
		case <-ctx.Done():
			return object, ctx.Err()
		}
	}
	return object, nil
}

var _ = It("T5QA22 terminal source refusal fences a surviving same-key preparation after disclosure restores and only new explicit intent admits", Label("persistence"), func() {
	f := newFixture(GinkgoT())
	s, version, input := askFixture(f)
	sourceID, root := questionSource(f, s, &input)
	counted := &countingQuestionFiles{FileReader: s.Sources.Files}
	s.Sources.Files = counted
	held := &heldQuestionPreparation{CaptureBytes: s.Bytes, entered: make(chan struct{}), release: make(chan struct{})}
	s.Bytes = held
	originalKey := key(GinkgoT())
	originalContext, cancel := context.WithCancel(ctx)
	type outcome struct {
		response domain.Response
		err      error
	}
	result := make(chan outcome, 1)
	done := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(held.release) }) }
	go func() {
		defer GinkgoRecover()
		defer close(done)
		response, err := s.Start(originalContext, f.token, originalKey, version, input)
		result <- outcome{response: response, err: err}
	}()
	defer func() {
		release()
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			Fail("held original Question request did not drain during test cleanup")
		}
	}()
	select {
	case <-held.entered:
	case <-time.After(10 * time.Second):
		Fail("original Question request did not reach completed outside-writer preparation")
	}
	// The old HTTP request is still live; a second owner request with the same
	// immutable K/body encounters withdrawn source disclosure first.
	questionMapping(f, sourceID, root, false)
	refusedResponse, refusedError := s.Start(ctx, f.token, originalKey, version, input)
	var refusal *questions.AdmissionRefusal
	Expect(errors.As(refusedError, &refusal)).To(BeTrue())
	Expect(refusal.OperationID).To(Equal(originalKey))
	Expect(counted.opens).To(BeZero())

	// This is the acknowledgement's authoritative receipt read. Save its facts
	// without failing yet, so RED also observes the original delayed admission.
	refusedReceipt, receiptError := f.auth.Operation(ctx, f.token, originalKey)
	questionMapping(f, sourceID, root, true)
	release()
	var original outcome
	select {
	case original = <-result:
	case <-time.After(10 * time.Second):
		Fail("original Question request did not finish after preparation release")
	}
	Expect(original.response.Status).To(Equal(403), "the old in-flight request must never become an accepted job after source disclosure restores")
	Expect(receiptError).NotTo(HaveOccurred())
	Expect(refusedReceipt.State).To(Equal("failed"))
	Expect(refusedReceipt.Response).NotTo(BeNil())
	Expect(refusedReceipt.Response.Status).To(Equal(403))
	Expect(refusedResponse.Status).To(Equal(403))
	Expect(original.response.Body).To(Equal(refusedResponse.Body))
	var replayRefusal *questions.AdmissionRefusal
	Expect(errors.As(original.err, &replayRefusal)).To(BeTrue())
	Expect(replayRefusal.OperationID).To(Equal(originalKey))
	Expect(counted.opens).To(BeZero(), "refused old K must not collect source")
	questionJobs := func() []domain.Job {
		var selected []domain.Job
		Expect(f.store.Write(ctx, func(tx app.WriteTx) error {
			jobs, err := tx.Jobs()
			if err != nil {
				return err
			}
			for _, job := range jobs {
				if job.Kind == "question" {
					selected = append(selected, job)
				}
			}
			return nil
		})).To(Succeed())
		return selected
	}
	Expect(questionJobs()).To(BeEmpty(), "a terminal refusal must not leave an orphan Question job")

	saved, err := s.Questions(ctx, f.token, version, "")
	Expect(err).NotTo(HaveOccurred())
	Expect(saved.Items).To(BeEmpty())

	// The old identity remains terminal. A fresh explicit owner intent alone
	// may create the one Question/job and its one exact source capture.
	newKey := key(GinkgoT())
	accepted, err := s.Start(ctx, f.token, newKey, version, input)
	Expect(err).NotTo(HaveOccurred())
	Expect(accepted.Status).To(Equal(202))
	Expect(counted.opens).To(Equal(1))
	saved, err = s.Questions(ctx, f.token, version, "")
	Expect(err).NotTo(HaveOccurred())
	Expect(saved.Items).To(HaveLen(1))
	Expect(saved.Items[0].OperationID).To(Equal(newKey))
	jobs := questionJobs()
	Expect(jobs).To(HaveLen(1))
	Expect(jobs[0].ID).To(Equal(saved.Items[0].JobID))

	terminal, err := f.auth.Operation(ctx, f.token, originalKey)
	Expect(err).NotTo(HaveOccurred())
	Expect(terminal.State).To(Equal("failed"))
	Expect(terminal.Response.Body).To(Equal(refusedReceipt.Response.Body))
})
