package foundation

import (
	"context"
	"errors"
	"io"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("TestReadNeverReleasesPreexistingOwnerPin", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t, nil)
	object := f.publish("backup stays pinned after a transient read", "stage")
	backup := domain.Pin{ID: "durable-backup", Kind: "backup", OwnerID: "backup", Hash: object.Hash}
	{
		err := f.write(func(tx app.WriteTx) error {
			if err := tx.AcquirePin(backup); err != nil {
				return err
			}
			return tx.ReleasePin("stage")
		})
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		err := f.s.Read(context.Background(), object, backup, func(io.ReadSeeker) error { return nil })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		err := f.s.Collect(context.Background(), object)
		Expect(!errors.Is(err, domain.ErrPinned)).To(BeFalse(), func() string { return fmt.Sprintf("transient read removed its caller's durable backup pin: %v", err) })
	}

})
var _ = It("TestOverlappingReadsWithSameDescriptorRemainIndependentlyPinned", Label("persistence"), func() {
	t := GinkgoT()

	f := newFixture(t, nil)
	object := f.publish("two simultaneous readers have distinct lifetimes", "stage")
	descriptor := domain.Pin{ID: "same-descriptor", Kind: "active_read", OwnerID: "reader"}
	enteredA, enteredB := make(chan struct{}), make(chan struct{})
	releaseA, releaseB := make(chan struct{}), make(chan struct{})
	doneA, doneB := make(chan error, 1), make(chan error, 1)
	go func() {
		defer GinkgoRecover()
		doneA <- f.s.Read(context.Background(), object, descriptor, func(io.ReadSeeker) error { close(enteredA); <-releaseA; return nil })
	}()
	awaitBarrier(t, enteredA)
	go func() {
		defer GinkgoRecover()
		doneB <- f.s.Read(context.Background(), object, descriptor, func(io.ReadSeeker) error { close(enteredB); <-releaseB; return nil })
	}()
	awaitBarrier(t, enteredB)
	{
		err := f.write(func(tx app.WriteTx) error { return tx.ReleasePin("stage") })
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	close(releaseA)
	{
		err := awaitResult(t, doneA)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		err := f.s.Collect(context.Background(), object)
		Expect(!errors.Is(err, domain.ErrPinned)).To(BeFalse(), func() string { return fmt.Sprintf("first reader released another live reader's pin: %v", err) })
	}

	close(releaseB)
	{
		err := awaitResult(t, doneB)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}
	{

		err := f.s.Collect(context.Background(), object)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprintf("last transient read leaked pin: %v", err) })
	}

})
