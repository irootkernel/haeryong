// Package references materializes authority-free structural references through
// the existing derived ledger. It never runs a second queue or reads Git.
package references

import (
	"context"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/application/search"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"time"
)

type Worker struct {
	Store             app.Store
	Bytes             search.ObjectReader
	ReferenceRevision int64
}

func NewWorker(store app.Store, bytes search.ObjectReader) *Worker {
	return &Worker{Store: store, Bytes: bytes, ReferenceRevision: domain.ReferenceRevision}
}

type checkpoint struct {
	Phase string `json:"phase"`
	manifestPosition
	Size    int64  `json:"size"`
	After   string `json:"after"`
	Version string `json:"version"`
}

func (s *Worker) batch(ctx context.Context, j domain.Job, apply func(app.WriteTx, domain.DerivedWork) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error {
		w, err := tx.ReferenceFence(j.ID, j.AttemptGeneration)
		if err != nil {
			return err
		}
		return apply(tx, w)
	})
}
func (s *Worker) Process(ctx context.Context, id string) error {
	var j domain.Job
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		var err error
		j, err = tx.Job(id)
		if err != nil {
			return err
		}
		w, err := tx.DerivedWork(id)
		if err != nil {
			return err
		}
		if j.Kind != "derived_index" || w.WorkKind != "references" {
			return domain.ErrTransition
		}
		if j.CancelRequested {
			return tx.ConfirmStop(id, j.AttemptGeneration)
		}
		ready, err := tx.ReferenceSearchTerminal(w.VersionID)
		if err != nil {
			return err
		}
		if !ready {
			j.State = "waiting"
			return nil
		}
		if j.State == "paused" {
			if err = tx.ResumeReferences(id, j.AttemptGeneration); errors.Is(err, domain.ErrSearchRestricted) {
				j.State = "waiting"
				return nil
			}
			if err != nil {
				return err
			}
			j.State = "queued"
		}
		if j.State == "interrupted" {
			if err = tx.ReconcileJob(id); err != nil {
				return err
			}
			j.State = "queued"
		}
		if j.State == "queued" {
			j, err = tx.ClaimJob(id)
		}
		return err
	})
	if err != nil || j.CancelRequested || j.State != "running" {
		return err
	}
	err = s.turn(ctx, j)
	if err == nil || ctx.Err() != nil {
		return err
	}
	settle := s.Store.Write(ctx, func(tx app.WriteTx) error {
		current, e := tx.Job(id)
		if e != nil {
			return e
		}
		if current.CancelRequested {
			return tx.ConfirmStop(id, current.AttemptGeneration)
		}
		if current.State != "running" || current.AttemptGeneration != j.AttemptGeneration {
			return domain.ErrStaleWorker
		}
		if errors.Is(err, domain.ErrStaleWorker) {
			return err
		}
		if errors.Is(err, domain.ErrSearchRestricted) {
			return tx.SuspendReferences(id, j.AttemptGeneration)
		}
		reason := "storage_failed"
		if errors.Is(err, domain.ErrContentIntegrity) {
			reason = "integrity_failed"
		}
		return tx.FinishJob(id, j.AttemptGeneration, reason, nil)
	})
	return errors.Join(err, settle)
}
func (s *Worker) save(ctx context.Context, j domain.Job, c checkpoint, apply func(app.WriteTx) error) error {
	// All encoding is detached from the serialized store callback.
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	stage := "inventory"
	if c.Phase == "outgoing" {
		stage = "extract"
	}
	if c.Phase == "incoming" {
		stage = "commit"
	}
	if c.Phase == "complete" {
		stage = "complete"
	}
	return s.batch(ctx, j, func(tx app.WriteTx, w domain.DerivedWork) error {
		if apply != nil {
			if err := apply(tx); err != nil {
				return err
			}
		}
		w.AfterPath = string(raw)
		if err := tx.PutDerivedWork(w); err != nil {
			return err
		}
		// Incoming cardinality is hidden target-dependent work. Persist its
		// private checkpoint without job notifications (which would otherwise
		// become one library.invalidated per hidden incoming declaration).
		if c.Phase == "incoming" {
			return nil
		}
		return tx.ReferenceProgress(j.ID, stage, string(raw), c.Offset, c.Size)
	})
}
func (s *Worker) turn(ctx context.Context, j domain.Job) error {
	var w domain.DerivedWork
	var v domain.Version
	err := s.batch(ctx, j, func(tx app.WriteTx, current domain.DerivedWork) error {
		w = current
		var err error
		v, err = tx.PublishedVersion(w.VersionID)
		return err
	})
	if err != nil {
		return err
	}
	c := checkpoint{Phase: "declarations"}
	if w.AfterPath != "" {
		if err = json.Unmarshal([]byte(w.AfterPath), &c); err != nil {
			return err
		}
	}
	switch c.Phase {
	case "declarations":
		var object domain.Object
		if err = s.batch(ctx, j, func(tx app.WriteTx, _ domain.DerivedWork) error {
			var err error
			object, err = tx.Object(v.ManifestSHA)
			return err
		}); err != nil {
			return err
		}
		var items []manifestItem
		var done bool
		err = s.Bytes.Read(ctx, object, domain.Pin{Kind: "active_read", OwnerID: j.ID}, func(r io.ReadSeeker) error {
			var err error
			items, c.manifestPosition, done, err = manifestBatch(ctx, r, v.ID, c.manifestPosition)
			return err
		})
		if err != nil {
			return err
		}
		c.Size = object.Size
		// Commit every declaration before a later turn may attempt outgoing work.
		endOffset := c.Offset
		for _, item := range items {
			c.Offset = item.offset
			if err = s.save(ctx, j, c, func(tx app.WriteTx) error { return tx.PutAuthoredReference(item.prepared) }); err != nil {
				return err
			}
		}
		c.Offset = endOffset
		if done {
			c.Phase = "outgoing"
			c.After = ""
		}
		return s.save(ctx, j, c, nil)
	case "outgoing", "incoming":
		var rows []domain.AuthoredReference
		err = s.batch(ctx, j, func(tx app.WriteTx, _ domain.DerivedWork) error {
			var err error
			if c.Phase == "outgoing" {
				rows, err = tx.AuthoredReferences(v.ID, c.After, 64)
			} else {
				rows, err = tx.IncomingReferences(v.ID, c.Version, c.After, 64)
			}
			return err
		})
		if err != nil {
			return err
		}
		deadline := time.Now().Add(8 * time.Millisecond)
		for _, r := range rows {
			candidate, err := s.resolve(ctx, r)
			if err != nil {
				return err
			}
			c.After = r.ID
			c.Version = r.FromVersionID
			if err = s.save(ctx, j, c, func(tx app.WriteTx) error {
				if candidate == nil {
					return nil
				}
				return candidate.commit(tx)
			}); err != nil {
				return err
			}
			if time.Now().After(deadline) {
				return nil
			}
		}
		if len(rows) > 0 {
			return nil
		}
		if c.Phase == "outgoing" {
			c.Phase = "incoming"
			c.After = ""
			c.Version = ""
			return s.save(ctx, j, c, func(tx app.WriteTx) error { return tx.CompleteReferences(j.ID) })
		}
		c.Phase = "complete"
		return s.save(ctx, j, c, func(tx app.WriteTx) error { return tx.FinishJob(j.ID, j.AttemptGeneration, "", nil) })
	default:
		return domain.ErrUnsupportedDerivedData
	}
}
