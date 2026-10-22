package search

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

type ObjectReader interface {
	Read(context.Context, domain.Object, domain.Pin, func(io.ReadSeeker) error) error
}
type Worker struct {
	Store             app.Store
	Bytes             ObjectReader
	ExtractorRevision int64
	sweepAfter        string
}

func NewWorker(store app.Store, bytes ObjectReader) *Worker {
	return &Worker{Store: store, Bytes: bytes, ExtractorRevision: domain.SearchExtractorRevision}
}

// Sweep visits the complete published inventory, one bounded transaction per
// turn. Existing terminal identities are retained; only a new revision schedules
// replacement work and quarantines old rows before clearing them.
func (s *Worker) Sweep(ctx context.Context) (bool, error) {
	var versions []string
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		var err error
		versions, err = tx.SearchVersions(s.sweepAfter, 32)
		return err
	})
	if err != nil {
		return false, err
	}
	for _, v := range versions {
		if err = s.Store.Write(ctx, func(tx app.WriteTx) error { _, err := tx.EnqueueSearch(v, s.ExtractorRevision); return err }); err != nil {
			return false, err
		}
		s.sweepAfter = v
	}
	return len(versions) == 0, nil
}
func (s *Worker) batch(ctx context.Context, j domain.Job, apply func(app.WriteTx, domain.DerivedWork) error) error {
	return s.Store.Write(ctx, func(tx app.WriteTx) error {
		w, err := tx.SearchFence(j.ID, j.AttemptGeneration)
		if err != nil {
			return err
		}
		return apply(tx, w)
	})
}
func (s *Worker) merge(ctx context.Context, j domain.Job) error {
	for table := 0; table < 2; table++ {
		if err := s.batch(ctx, j, func(tx app.WriteTx, _ domain.DerivedWork) error {
			return tx.MergeSearch(table, domain.SearchMergePages)
		}); err != nil {
			return err
		}
	}
	return nil
}
func (s *Worker) posting(ctx context.Context, j domain.Job, c domain.SearchChunk) error {
	if err := s.batch(ctx, j, func(tx app.WriteTx, _ domain.DerivedWork) error { return tx.PutSearchChunk(c) }); err != nil {
		return err
	}
	return s.merge(ctx, j)
}
func (s *Worker) Process(ctx context.Context, id string) error {
	var j domain.Job
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		var err error
		j, err = tx.Job(id)
		if err != nil {
			return err
		}
		if j.Kind != "derived_index" {
			return domain.ErrTransition
		}
		if j.CancelRequested {
			return tx.ConfirmStop(id, j.AttemptGeneration)
		}
		if j.State == "paused" {
			err = tx.ResumeSearch(id, j.AttemptGeneration, false)
			if errors.Is(err, domain.ErrSearchRestricted) {
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
	// Settlement checks the current generation again. A stale or cancelled
	// callback cannot publish progress, failure or EOF for a newer attempt.
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
		if errors.Is(err, domain.ErrSearchRestricted) {
			return tx.SuspendSearch(id, j.AttemptGeneration)
		}
		var failed pageFailure
		if errors.As(err, &failed) {
			if e := tx.FailSearchPage(id, j.AttemptGeneration, failed.unitID, safeSearchFailure(err)); e != nil {
				return e
			}
		}
		return tx.FinishJob(id, j.AttemptGeneration, safeSearchFailure(err), nil)
	})
	return errors.Join(err, settle)
}

type objectReadFailure struct{ error }

func (e objectReadFailure) Unwrap() error { return e.error }

type pageFailure struct {
	unitID int64
	error
}

func (e pageFailure) Unwrap() error { return e.error }

func safeSearchFailure(err error) string {
	var readFailure objectReadFailure
	switch {
	case errors.Is(err, domain.ErrContentIntegrity):
		return "integrity_failed"
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.ENOMEM), errors.Is(err, syscall.EDQUOT):
		return "resource_exhausted"
	case errors.Is(err, domain.ErrDenied), errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrStaleWorker):
		return "interrupted"
	case errors.Is(err, os.ErrNotExist), errors.Is(err, io.ErrUnexpectedEOF), errors.As(err, &readFailure):
		return "read_failed"
	default:
		return "storage_failed"
	}
}
func (s *Worker) turn(ctx context.Context, j domain.Job) error {
	var w domain.DerivedWork
	var index domain.SearchIndex
	var v domain.Version
	err := s.batch(ctx, j, func(tx app.WriteTx, work domain.DerivedWork) error {
		w = work
		var err error
		index, err = tx.SearchIndex(w.VersionID)
		if err != nil {
			return err
		}
		v, err = tx.PublishedVersion(w.VersionID)
		return err
	})
	if err != nil {
		return err
	}
	if index.State == "pending" {
		return s.batch(ctx, j, func(tx app.WriteTx, w domain.DerivedWork) error {
			done, err := tx.ClearSearchData(w.VersionID, "", 64)
			if err != nil || !done {
				return err
			}
			index.State = "indexing"
			index.Revision++
			if err = tx.PutSearchIndex(index); err != nil {
				return err
			}
			return tx.SearchProgress(j.ID, "inventory")
		})
	}
	if !index.DiscoveryComplete {
		return s.inventory(ctx, j, w, v, index)
	}
	var page domain.SearchUnit
	var noPage bool
	err = s.batch(ctx, j, func(tx app.WriteTx, w domain.DerivedWork) error {
		var err error
		page, err = tx.NextSearchPage(w.VersionID)
		if errors.Is(err, domain.ErrNotFound) {
			noPage = true
			return nil
		}
		return err
	})
	if err == nil && noPage {
		return s.batch(ctx, j, func(tx app.WriteTx, w domain.DerivedWork) error {
			index.State = "complete"
			index.Revision++
			if err := tx.PutSearchIndex(index); err != nil {
				return err
			}
			if err := tx.SearchProgress(j.ID, "complete"); err != nil {
				return err
			}
			if err := tx.SearchIndexUpdated(w.VersionID); err != nil {
				return err
			}
			return tx.FinishJob(j.ID, j.AttemptGeneration, "", nil)
		})
	}
	if err != nil {
		return err
	}
	if err := s.page(ctx, j, v, page); err != nil {
		return pageFailure{page.ID, err}
	}
	return nil
}
func (s *Worker) inventory(ctx context.Context, j domain.Job, w domain.DerivedWork, v domain.Version, index domain.SearchIndex) error {
	cursor := []string{"document", ""}
	if w.AfterPath != "" {
		if err := json.Unmarshal([]byte(w.AfterPath), &cursor); err != nil {
			return err
		}
	}
	kind, after := cursor[0], cursor[1]
	if kind == "document" {
		u := domain.SearchUnit{VersionID: v.ID, Kind: "document", Title: v.Title, State: "ready", BodyState: "not_applicable", AnchorState: "not_applicable"}
		if err := s.metadata(ctx, j, &u, v.Title); err != nil {
			return err
		}
		kind = "entry"
		after = ""
	}
	var items []domain.SearchInventoryItem
	err := s.batch(ctx, j, func(tx app.WriteTx, w domain.DerivedWork) error {
		var err error
		items, err = tx.SearchInventory(v.ID, kind, after, 32)
		return err
	})
	if err != nil {
		return err
	}
	for _, item := range items {
		text, err := MetadataText(item.Metadata, kind == "source")
		if err != nil {
			return err
		}
		u := item.Unit
		if kind == "entry" {
			text = strings.TrimSpace(u.PagePath + " " + text)
		} else {
			u.Title = text
			text = strings.Join([]string{u.PagePath, u.RegionID, u.SourceID, text}, " ")
		}
		if err = s.metadata(ctx, j, &u, text); err != nil {
			return err
		}
		after = item.Key
	}
	if len(items) == 0 {
		after = ""
		switch kind {
		case "entry":
			kind = "source"
		case "source":
			kind = "region"
		case "region":
			kind = "done"
		}
	}
	return s.batch(ctx, j, func(tx app.WriteTx, w domain.DerivedWork) error {
		raw, _ := json.Marshal([]string{kind, after})
		w.AfterPath = string(raw)
		if err := tx.PutDerivedWork(w); err != nil {
			return err
		}
		index.Revision++
		index.DiscoveryComplete = kind == "done"
		if err := tx.PutSearchIndex(index); err != nil {
			return err
		}
		stage := "inventory"
		if index.DiscoveryComplete {
			stage = "extract"
		}
		if err := tx.SearchProgress(j.ID, stage); err != nil {
			return err
		}
		return tx.SearchIndexUpdated(v.ID)
	})
}
func (s *Worker) metadata(ctx context.Context, j domain.Job, u *domain.SearchUnit, text string) error {
	// A replay reuses the exact tuple and overwrites deterministic chunk ordinals.
	u.State = "ready" // Metadata is independently ready while body/anchor coverage stays pending.
	if err := s.batch(ctx, j, func(tx app.WriteTx, _ domain.DerivedWork) error {
		var err error
		u.ID, err = tx.PutSearchUnit(*u)
		return err
	}); err != nil {
		return err
	}
	return metadataWindows(ctx, text, func(ordinal int, literal, display string) error {
		prepared := metadataPosting(u.ID, ordinal, literal, display)
		return s.posting(ctx, j, prepared)
	})
}

// displayWindows retains authored case for snippets and component aliases in a
// separate bounded stream. Literal body phrases use normalized body chunks only.
func displayWindows(ctx context.Context, r io.Reader, emit func(int, string) error) error {
	reader := bufio.NewReader(r)
	window := make([]rune, 0, ChunkSize)
	ordinal := 0
	fresh := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c, _, err := reader.ReadRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		window = append(window, c)
		fresh++
		if len(window) == ChunkSize {
			if err = emit(ordinal, string(window)); err != nil {
				return err
			}
			ordinal++
			copy(window, window[ChunkSize-ChunkOverlap:])
			window = window[:ChunkOverlap]
			fresh = 0
		}
	}
	if fresh > 0 {
		return emit(ordinal, string(window))
	}
	return nil
}
func (s *Worker) page(ctx context.Context, j domain.Job, v domain.Version, u domain.SearchUnit) error {
	// An interrupted page is reparsed after bounded removal of staged body/anchors;
	// completed pages are never selected here and remain intact on restart.
	var cleared bool
	if err := s.batch(ctx, j, func(tx app.WriteTx, _ domain.DerivedWork) error {
		var err error
		cleared, err = tx.ClearSearchData(v.ID, u.PagePath, 64)
		return err
	}); err != nil || !cleared {
		return err
	}
	spool, err := os.CreateTemp("", "haeryong-search-*")
	if err != nil {
		return err
	}
	// Keep only the open private handle: a process crash leaves no named text
	// spool behind. Supported local targets allow unlinking an open file.
	if err = os.Remove(spool.Name()); err != nil {
		spool.Close()
		return err
	}
	defer spool.Close()
	var object domain.Object
	if err = s.batch(ctx, j, func(tx app.WriteTx, _ domain.DerivedWork) error {
		var err error
		object, err = tx.Object(u.InputHash)
		return err
	}); err != nil {
		return err
	}
	var extracted Extraction
	entered := false
	err = s.Bytes.Read(ctx, object, domain.Pin{Kind: "active_read", OwnerID: j.ID}, func(r io.ReadSeeker) error {
		entered = true
		var err error
		extracted, err = Extract(ctx, r, "text/html", spool, func(f Fragment) error {
			target := u
			if f.AnchorID != "" {
				var err error
				target, err = s.region(ctx, j, u, f.AnchorID, f.AnchorLabel)
				if err != nil {
					return err
				}
			}

			if f.Chunk.Ordinal == 0 {
				err = displayWindows(ctx, io.NewSectionReader(spool, f.DisplayStart, f.DisplayEnd-f.DisplayStart), func(n int, display string) error {
					prepared := domain.SearchChunk{UnitID: target.ID, Part: "body", Ordinal: 2*n + 1, Body: nomination(Normalize(display)), Terms: wordNomination(Normalize(display)), DisplayBody: display}
					return s.posting(ctx, j, prepared)
				})
				if err != nil {
					return err
				}
			}
			return s.posting(ctx, j, domain.SearchChunk{UnitID: target.ID, Part: "body", Ordinal: 2 * f.Chunk.Ordinal, Body: nomination(f.Chunk.Text), Terms: wordNomination(f.Chunk.Text), DisplayBody: f.Chunk.Text})
		})
		return err
	})
	if err != nil && !entered {
		return objectReadFailure{err}
	}
	if err != nil {
		return err
	}
	if !extracted.Complete {
		return io.ErrUnexpectedEOF
	}
	if err = spool.Close(); err != nil {
		return err
	}
	page, err := s.Store.Page(ctx, v.ID, u.PagePath)
	if err != nil {
		return err
	}
	supplied, err := MetadataText(page.Metadata, false)
	if err != nil {
		return err
	}
	if extracted.Title != "" {
		u.Title = extracted.Title
	} else if supplied != "" {
		u.Title = supplied
	}
	if err = s.metadata(ctx, j, &u, strings.Join([]string{u.PagePath, supplied, u.Title, extracted.Keywords}, " ")); err != nil {
		return err
	}
	// All object verification, parsing and spool IO has succeeded. Anchor batches
	// remain hidden behind pending body_state until the final EOF transaction.
	for _, a := range extracted.Anchors {
		if a.Occurrences == 1 {
			if _, err = s.region(ctx, j, u, a.ID, a.Label); err != nil {
				return err
			}
		}
	}
	for start := 0; start < len(extracted.Anchors); start += 32 {
		end := start + 32
		if end > len(extracted.Anchors) {
			end = len(extracted.Anchors)
		}
		if err = s.batch(ctx, j, func(tx app.WriteTx, _ domain.DerivedWork) error {
			for _, a := range extracted.Anchors[start:end] {
				if err := tx.PutPageAnchor(domain.PageAnchor{VersionID: v.ID, PagePath: u.PagePath, ID: a.ID, LocatorKind: a.LocatorKind, Occurrences: a.Occurrences, Label: a.Label, TextSHA256: a.TextSHA256}); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return s.batch(ctx, j, func(tx app.WriteTx, w domain.DerivedWork) error {
		if err := tx.CompleteSearchPage(u.ID); err != nil {
			return err
		}
		index, err := tx.SearchIndex(v.ID)
		if err != nil {
			return err
		}
		index.Revision++
		if err = tx.PutSearchIndex(index); err != nil {
			return err
		}
		if err = tx.SearchProgress(j.ID, "commit"); err != nil {
			return err
		}
		return tx.SearchIndexUpdated(v.ID)
	})
}

// Derived DOM regions remain pending until their page commits verified EOF.
// Supplied region metadata/provenance wins when the exact tuple already exists.
func (s *Worker) region(ctx context.Context, j domain.Job, page domain.SearchUnit, id, label string) (domain.SearchUnit, error) {
	var target domain.SearchUnit
	created := false
	err := metadataWindows(ctx, strings.Join([]string{page.PagePath, id, label}, " "), func(n int, literal, display string) error {
		prepared := metadataPosting(0, n, literal, display)
		err := s.batch(ctx, j, func(tx app.WriteTx, _ domain.DerivedWork) error {
			if n == 0 {
				var err error
				target, err = tx.SearchRegionUnit(page.VersionID, page.PagePath, id)
				created = err == nil && target.State == "pending"
				if errors.Is(err, domain.ErrNotFound) {
					target = domain.SearchUnit{VersionID: page.VersionID, Kind: "region", PagePath: page.PagePath, RegionID: id, Title: label, InputHash: page.InputHash, State: "pending", BodyState: "pending", AnchorState: "pending"}
					target.ID, err = tx.PutSearchUnit(target)
					created = true
				}
				if err != nil {
					return err
				}
			}
			if !created {
				return nil
			}
			// Replaying a pending derived region rewrites every window by its
			// stable ordinal. Supplied ready metadata keeps its provenance.
			// The first metadata window and its new unit are atomic. Further
			// windows yield, and remain hidden until their parent's EOF gate.
			prepared.UnitID = target.ID
			return tx.PutSearchChunk(prepared)
		})
		if err != nil || !created {
			return err
		}
		return s.merge(ctx, j)
	})
	return target, err
}
