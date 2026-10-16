// Package foundation composes durable filesystem bytes and typed SQLite metadata
// without exposing either adapter's handles to application use cases.
package foundation

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"sync"

	"haeryong.local/haeryong/internal/adapters/contentstore"
	"haeryong.local/haeryong/internal/adapters/sqlite"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type Store struct {
	gcMu sync.Mutex
	*sqlite.Store
	Objects *contentstore.Store
}

func New(fault sqlite.Fault) *Store { return &Store{Store: sqlite.New(fault)} }
func (s *Store) Prepare(ctx context.Context, p service.Profile) (service.StorageState, func() error, error) {
	state, closeDB, err := s.Store.Prepare(ctx, p)
	if err != nil {
		return state, nil, err
	}
	objects, err := contentstore.New(filepath.Join(p.DataDir, "storage"))
	if err != nil {
		closeDB()
		return service.StorageState{}, nil, err
	}
	s.Objects = objects
	closeAll := func() error { s.gcMu.Lock(); defer s.gcMu.Unlock(); return errors.Join(objects.Close(), closeDB()) }
	// Iterate bounded metadata batches and verify referenced/pinned bytes before
	// advertising recovery readiness. No whole-library inventory/RAM envelope.
	after := ""
	for {
		rows, err := s.ReferencedObjects(ctx, after, 64)
		if err != nil {
			closeAll()
			return service.StorageState{}, nil, err
		}
		if len(rows) == 0 {
			break
		}
		for _, o := range rows {
			if o.State != "ready" {
				closeAll()
				return service.StorageState{}, nil, domain.ErrDeleting
			}
			if err := objects.VerifiedRead(ctx, contentstore.Object{Hash: o.Hash, Size: o.Size}, func(io.ReadSeeker) error { return nil }); err != nil {
				closeAll()
				return service.StorageState{}, nil, err
			}
			after = o.Hash
		}
	}
	return state, closeAll, nil
}

// Publish owns the checked transaction callback: a publication error returns
// before object/pin insertion or DB commit. A durable orphan may remain safely.
func (s *Store) Publish(ctx context.Context, staged *contentstore.Staged, pin domain.Pin) (domain.Object, error) {
	var result domain.Object
	_, err := s.Objects.Finalize(ctx, staged, func(object contentstore.Object, publish func() error) error {
		return s.Write(ctx, func(tx app.WriteTx) error {
			current, err := tx.Object(object.Hash)
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return err
			}
			if err == nil && current.State == "deleting" {
				return domain.ErrDeleting
			}
			if err := publish(); err != nil {
				return err
			}
			result = domain.Object{Hash: object.Hash, Size: object.Size, State: "ready"}
			if err := tx.PutDurableObject(result); err != nil {
				return err
			}
			pin.Hash = object.Hash
			return tx.AcquirePin(pin)
		})
	})
	if err != nil {
		return domain.Object{}, err
	}
	return result, nil
}

// Read treats the supplied pin as owner provenance, never as borrowed pin
// ownership. Each invocation acquires/releases its own unique durable row.
func (s *Store) Read(ctx context.Context, object domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) (result error) {
	readID, err := service.NewID()
	if err != nil {
		return err
	}
	pin.ID = readID
	pin.Hash = object.Hash
	if err := s.Write(ctx, func(tx app.WriteTx) error { return tx.AcquirePin(pin) }); err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, s.Write(context.Background(), func(tx app.WriteTx) error { return tx.ReleasePin(pin.ID) }))
	}()
	return s.Objects.VerifiedRead(ctx, contentstore.Object{Hash: object.Hash, Size: object.Size}, consume)
}
func (s *Store) Collect(ctx context.Context, object domain.Object) error {
	s.gcMu.Lock()
	defer s.gcMu.Unlock()
	var reservation domain.GCReservation
	if err := s.Write(ctx, func(tx app.WriteTx) error {
		current, err := tx.Object(object.Hash)
		if err != nil {
			return err
		}
		if current.State == "deleting" {
			reservation = domain.GCReservation{Hash: current.Hash, Token: current.GCToken}
			return nil
		}
		token, err := service.NewID()
		if err != nil {
			return err
		}
		reservation, err = tx.ReserveGC(object.Hash, token)
		return err
	}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.Objects.Delete(contentstore.Object{Hash: object.Hash, Size: object.Size}); err != nil {
		return err
	}
	return s.Write(ctx, func(tx app.WriteTx) error { return tx.FinishGC(reservation) })
}
