package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// generationFence supports the existing explicit saved-result continuation
// reservation as well as its original running attempt. It grants no Execute.
func generationFence(tx app.WriteTx, a domain.ProviderAttempt) error {
	current, e := tx.ProviderAttempt(a.ID)
	if e != nil {
		return e
	}
	if current.JobID != a.JobID || current.State != "result_saved" {
		return domain.ErrStaleWorker
	}
	j, e := tx.Job(a.JobID)
	if e != nil {
		return e
	}
	if j.Kind != "generation" {
		return domain.ErrInvalidTarget
	}
	d, e := tx.GenerationIntent(j.ID)
	if e != nil {
		return e
	}
	draft, e := tx.Draft(d.DraftID)
	if e != nil {
		return e
	}
	if draft.State != "preparing" {
		return domain.ErrStaleWorker
	}
	if j.State == "queued" {
		return app.GuardProviderContinuation(tx, current)
	}
	return app.GuardProvider(tx, current)
}

// PrepareGeneration consumes only the complete durable provider-result object.
// ZIP parsing, file verification and manifest construction stay outside the
// writer. Each short write rechecks current job, draft and accepted authority.
func (s *Service) PrepareGeneration(ctx context.Context, attempt domain.ProviderAttempt) error {
	var a domain.ProviderAttempt
	var intent domain.GenerationIntent
	var object domain.Object
	prepared := false
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		j, e := tx.Job(attempt.JobID)
		if e != nil {
			return e
		}
		if j.Kind != "generation" {
			return nil
		}
		a, e = tx.ProviderAttempt(attempt.ID)
		if e != nil {
			return e
		}
		intent, e = tx.GenerationIntent(j.ID)
		if e != nil {
			return e
		}
		if intent.PreparedVersion != nil {
			if intent.PreparationAttemptID != a.ID {
				return domain.ErrConflict
			}
			// Ready candidates are retained independently from execution. Application
			// still performs its current fence and lifecycle checks.
			prepared = true
			return nil
		}
		if e = generationFence(tx, a); e != nil {
			return e
		}
		object, e = tx.Object(a.ResultHash)
		return e
	})
	if e != nil || a.ID == "" || prepared {
		return e
	}
	if s.Codec == nil {
		return ErrPackageInvalid
	}
	// Like upload sealing, the complete set reuses its producing operation UUID.
	// The attempt identity stays stable for saved continuation and distinct for retry.
	set := a.ID
	check := func(tx app.WriteTx) error { return generationFence(tx, a) }
	e = s.Store.Write(ctx, func(tx app.WriteTx) error {
		if e := check(tx); e != nil {
			return e
		}
		if e := tx.DiscardContentSet(set); e != nil {
			return e
		}
		return tx.CreateContentSet(set, app.EmptyInventorySHA256)
	})
	if e != nil {
		return e
	}
	emit := func(input EntryInput, r io.Reader) error {
		if e := ValidatePath(input.Path); e != nil {
			return e
		}
		entry := domain.Entry{ContentSetID: set, Path: input.Path, Kind: input.Kind, LinkText: input.LinkTarget, Size: input.Size}
		if _, e := InventoryLine(entry); e != nil {
			return e
		}
		if e := s.Store.Write(ctx, func(tx app.WriteTx) error {
			if e := check(tx); e != nil {
				return e
			}
			for _, p := range parents(input.Path) {
				if e := tx.StageEntry(domain.Entry{ContentSetID: set, Path: p, Kind: "directory"}); e != nil {
					return e
				}
			}
			return nil
		}); e != nil {
			return e
		}
		if input.Kind == "file" {
			_, e := s.Bytes.Put(ctx, &generationReader{Reader: r, ctx: ctx, store: s.Store, check: check}, func(tx app.WriteTx, o domain.Object) error {
				if e := check(tx); e != nil {
					return e
				}
				if o.Size != input.Size {
					return ErrHashMismatch
				}
				entry.Hash = o.Hash
				return tx.StageEntry(entry)
			})
			return e
		}
		return s.Store.Write(ctx, func(tx app.WriteTx) error {
			if e := check(tx); e != nil {
				return e
			}
			return tx.StageEntry(entry)
		})
	}
	e = s.Bytes.Read(ctx, object, domain.Pin{Kind: "provider_result", OwnerID: a.ID}, func(r io.ReadSeeker) error {
		return s.Codec.Expand(ctx, &seekReaderAt{reader: r}, object.Size, emit)
	})
	if e != nil {
		return e
	}
	h := sha256.New()
	after := ""
	var defaultPage *string
	for {
		var rows []domain.Entry
		e = s.Store.Write(ctx, func(tx app.WriteTx) error {
			if e := check(tx); e != nil {
				return e
			}
			var e error
			rows, e = tx.Entries(set, after, 64)
			return e
		})
		if e != nil {
			return e
		}
		if len(rows) == 0 {
			break
		}
		for _, entry := range rows {
			line, e := InventoryLine(entry)
			if e != nil {
				return e
			}
			h.Write(line)
			after = entry.Path
			p := strings.ToLower(entry.Path)
			if defaultPage == nil && entry.Kind == "file" && (strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".htm")) {
				defaultPage = strptr(entry.Path)
			}
		}
	}
	intent.ContentSetID = set
	intent.PayloadSHA = hex.EncodeToString(h.Sum(nil))
	intent.PreparationAttemptID = a.ID
	i := generationImport(intent)
	v := normalizedVersion(i, defaultPage)
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { e := writeGenerated(writer, i, v, nil); _ = writer.CloseWithError(e); done <- e }()
	manifest, e := s.Bytes.Put(ctx, &generationReader{Reader: reader, ctx: ctx, store: s.Store, check: check}, func(tx app.WriteTx, o domain.Object) error {
		if e := check(tx); e != nil {
			return e
		}
		return tx.AcquirePin(domain.Pin{ID: "generation-manifest:" + a.ID, Kind: "job", OwnerID: a.JobID, Hash: o.Hash})
	})
	_ = reader.Close()
	e = errors.Join(e, <-done)
	if e != nil {
		return e
	}
	v.ManifestSHA = manifest.Hash
	intent.PreparedVersion = &v
	return s.Store.Write(ctx, func(tx app.WriteTx) error {
		if e := check(tx); e != nil {
			return e
		}
		if e := tx.SealContentSetDigest(set, intent.PayloadSHA); e != nil {
			return e
		}
		return tx.PrepareGenerationIntent(intent)
	})
}

type generationReader struct {
	io.Reader
	ctx   context.Context
	store app.Store
	check func(app.WriteTx) error
}

func (r *generationReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	if e := r.store.Write(r.ctx, r.check); e != nil {
		return 0, e
	}
	return r.Reader.Read(p)
}
func generationImport(g domain.GenerationIntent) domain.ImportIntent {
	revision := g.DocumentRevision
	if g.DocumentID == nil {
		revision = 1
	}
	base := ""
	if g.BaseVersionID != nil {
		base = *g.BaseVersionID
	}
	return domain.ImportIntent{ExplanationReferences: g.PriorReferences, JobID: g.JobID, ContentSetID: g.ContentSetID, PayloadSHA: g.PayloadSHA, MetadataHash: g.MetadataHash, ProjectID: g.ProjectID, DocumentID: g.AllocatedDocumentID, VersionID: g.VersionID, BaseVersionID: base, Title: g.Title, CreatedAt: g.CreatedAt, NewDocument: g.DocumentID == nil, DocumentRevision: revision}
}

// ApplyGeneration runs within ApplyProviderResult's existing transaction. A
// stale publication base retains the whole ready candidate and saved result.
func (s *Service) ApplyGeneration(tx app.WriteTx, a domain.ProviderAttempt) error {
	j, e := tx.Job(a.JobID)
	if e != nil {
		return e
	}
	if j.Kind != "generation" {
		return nil
	}
	current, e := tx.ProviderAttempt(a.ID)
	if e != nil {
		return e
	}
	if e = app.GuardProvider(tx, current); e != nil {
		return e
	}
	g, e := tx.GenerationIntent(j.ID)
	if e != nil {
		return e
	}
	if g.PreparedVersion == nil || g.PreparationAttemptID != a.ID {
		return domain.ErrReconciliation
	}
	d, e := tx.Draft(g.DraftID)
	if e != nil {
		return e
	}
	if d.State != "preparing" && d.State != "ready" {
		return domain.ErrStaleWorker
	}
	v := *g.PreparedVersion
	// An explicit replacement owns a different candidate. Saved continuation
	// never republishes the superseded provider tree through the new draft.
	if d.State == "ready" && (d.ProposedVersionID != v.ID || d.ContentSetID != v.ContentSetID || d.PayloadSHA != v.PayloadSHA || d.ManifestSHA != v.ManifestSHA || d.MetadataHash != v.MetadataHash) {
		return domain.ErrStaleWorker
	}
	if d.State == "preparing" {
		d.State = "ready"
		d.ContentSetID = g.ContentSetID
		d.PayloadSHA = g.PayloadSHA
		d.ManifestSHA = v.ManifestSHA
		d.MetadataHash = v.MetadataHash
		d.Metadata = v.Metadata
		d.UpdatedAt = s.now()
		if e = tx.UpdateDraft(d, d.Revision); e != nil {
			return e
		}
		d, e = tx.Draft(d.ID)
		if e != nil {
			return e
		}
	}
	result := map[string]any{"draft_id": d.ID, "draft_state": "ready", "attempt_id": a.ID, "result_sha256": current.ResultHash}
	if g.Intent == "publish" {
		current, e := generationBaseCurrent(tx, g)
		if e != nil {
			return e
		}
		if !current {
			result["publication_error"] = "VERSION_CONFLICT"
			if e = tx.SetJobResult(j.ID, encoded(result)); e != nil {
				return e
			}
			return tx.PauseProviderJob(j.ID, "VERSION_CONFLICT")
		}
		if e = tx.FinalizeImport(generationImport(g), v); e != nil {
			return e
		}
		d.State = "published"
		d.PublishedVersionID = v.ID
		d.UpdatedAt = s.now()
		if e = tx.UpdateDraft(d, d.Revision); e != nil {
			return e
		}
		result["draft_state"] = "published"
		result["version_id"] = v.ID
		result["document_id"] = v.DocumentID
	}
	if e = tx.SetJobResult(j.ID, encoded(result)); e != nil {
		return e
	}
	return tx.ReleaseOwnerPins("job", j.ID)
}
func generationBaseCurrent(tx app.WriteTx, g domain.GenerationIntent) (bool, error) {
	if g.DocumentID == nil {
		return true, nil
	}
	d, e := tx.Document(*g.DocumentID)
	if e != nil {
		return false, e
	}
	return g.BaseVersionID != nil && d.LatestVersionID == *g.BaseVersionID && d.Revision == g.DocumentRevision && d.Lifecycle != "trash" && d.Lifecycle != "purged", nil
}
