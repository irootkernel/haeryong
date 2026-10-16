package foundation

import (
	"context"
	"errors"
	"io"
	"testing"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

func TestReadNeverReleasesPreexistingOwnerPin(t *testing.T) {
	f := newFixture(t, nil)
	object := f.publish("backup stays pinned after a transient read", "stage")
	backup := domain.Pin{ID: "durable-backup", Kind: "backup", OwnerID: "backup", Hash: object.Hash}
	if err := f.write(func(tx app.WriteTx) error {
		if err := tx.AcquirePin(backup); err != nil {
			return err
		}
		return tx.ReleasePin("stage")
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Read(context.Background(), object, backup, func(io.ReadSeeker) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Collect(context.Background(), object); !errors.Is(err, domain.ErrPinned) {
		t.Fatalf("transient read removed its caller's durable backup pin: %v", err)
	}
}
func TestOverlappingReadsWithSameDescriptorRemainIndependentlyPinned(t *testing.T) {
	f := newFixture(t, nil)
	object := f.publish("two simultaneous readers have distinct lifetimes", "stage")
	descriptor := domain.Pin{ID: "same-descriptor", Kind: "active_read", OwnerID: "reader"}
	enteredA, enteredB := make(chan struct{}), make(chan struct{})
	releaseA, releaseB := make(chan struct{}), make(chan struct{})
	doneA, doneB := make(chan error, 1), make(chan error, 1)
	go func() {
		doneA <- f.s.Read(context.Background(), object, descriptor, func(io.ReadSeeker) error { close(enteredA); <-releaseA; return nil })
	}()
	awaitBarrier(t, enteredA)
	go func() {
		doneB <- f.s.Read(context.Background(), object, descriptor, func(io.ReadSeeker) error { close(enteredB); <-releaseB; return nil })
	}()
	awaitBarrier(t, enteredB)
	if err := f.write(func(tx app.WriteTx) error { return tx.ReleasePin("stage") }); err != nil {
		t.Error(err)
	}
	close(releaseA)
	if err := awaitResult(t, doneA); err != nil {
		t.Error(err)
	}
	if err := f.s.Collect(context.Background(), object); !errors.Is(err, domain.ErrPinned) {
		t.Errorf("first reader released another live reader's pin: %v", err)
	}
	close(releaseB)
	if err := awaitResult(t, doneB); err != nil {
		t.Error(err)
	}
	if err := f.s.Collect(context.Background(), object); err != nil {
		t.Errorf("last transient read leaked pin: %v", err)
	}
}
