package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"haeryong.local/haeryong/internal/application/auth"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
	"sync"
	"syscall"
	"time"
)

// Run is one local serial executor over the existing durable jobs, not a second
// queue. Its owner must cancel and await it before closing storage.
func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		var jobs []domain.Job
		e := s.Store.Write(ctx, func(tx app.WriteTx) error { var e error; jobs, e = tx.WorkJobs(64); return e })
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		for _, j := range jobs {
			if ctx.Err() != nil {
				return nil
			}
			_ = s.Process(ctx, j.ID)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (s *Service) Process(ctx context.Context, id string) error {
	var j domain.Job
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		var e error
		j, e = tx.Job(id)
		if e != nil {
			return e
		}
		if j.Kind != "upload_seal" && j.Kind != "import" {
			return domain.ErrTransition
		}
		if j.CancelRequested {
			return tx.ConfirmStop(id, j.AttemptGeneration)
		}
		if j.State == "interrupted" {
			if e := tx.ReconcileJob(id); e != nil {
				return e
			}
		}
		j, e = tx.ClaimJob(id)
		return e
	})
	if e != nil {
		return e
	}
	if j.CancelRequested {
		return nil
	}
	if j.Kind == "upload_seal" {
		e = s.seal(ctx, j)
	} else {
		e = s.publish(ctx, j)
	}
	if e == nil || ctx.Err() != nil {
		return e
	}
	// This executor has stopped the attempt. Observe current fencing before any
	// failure/stop update; an abandoned upload can never be reopened by late work.
	settle := s.Store.Write(context.Background(), func(tx app.WriteTx) error {
		current, err := tx.Job(id)
		if err != nil {
			return err
		}
		if current.CancelRequested {
			if e := tx.ConfirmStop(id, current.AttemptGeneration); e != nil {
				return e
			}
			if j.Kind == "upload_seal" {
				if e := tx.DiscardContentSet(j.ID); e != nil && !errors.Is(e, domain.ErrPinned) {
					return e
				}
			}
			if e := tx.ReleaseOwnerPins("job", j.ID); e != nil {
				return e
			}
			return tx.ReclaimAbandonedUpload(j.UploadID)
		}
		if current.AttemptGeneration != j.AttemptGeneration {
			return domain.ErrStaleWorker
		}
		if e := tx.FinishJob(id, j.AttemptGeneration, safeFailure(e), nil); e != nil {
			return e
		}
		if j.Kind == "upload_seal" {
			if e := tx.DiscardContentSet(j.ID); e != nil {
				return e
			}
		}
		if e := tx.ReleaseOwnerPins("job", j.ID); e != nil {
			return e
		}
		return tx.ReclaimAbandonedUpload(j.UploadID)
	})
	return errors.Join(e, settle)
}
func safeFailure(err error) string {
	var metadata *MetadataError
	if errors.As(err, &metadata) {
		return metadata.Error()
	}
	if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.ENOMEM) {
		return "RESOURCE_EXHAUSTED"
	}
	if errors.Is(err, syscall.EIO) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EXDEV) {
		return "FILE_ACCESS_ERROR"
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "TRANSFER_INTERRUPTED"
	}
	for _, known := range []error{ErrInputChanged, ErrHashMismatch, ErrPackageInvalid, ErrVersionConflict, ErrImmutableConflict, domain.ErrDenied, domain.ErrNotFound, domain.ErrStaleWorker} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return "storage or input unavailable; retained for retry"
}
func (s *Service) jobFence(tx app.WriteTx, j domain.Job) error {
	current, e := tx.Job(j.ID)
	if e != nil {
		return e
	}
	if current.AttemptGeneration != j.AttemptGeneration || current.State != "running" || current.CancelRequested {
		return domain.ErrStaleWorker
	}
	var scope struct {
		PrincipalID     string                 `json:"principal_id"`
		AuthEpoch       string                 `json:"auth_epoch"`
		Purpose         string                 `json:"purpose"`
		Target          json.RawMessage        `json:"target"`
		CredentialScope domain.CredentialScope `json:"credential_scope"`
	}
	if json.Unmarshal(j.Scope, &scope) != nil {
		return domain.ErrDenied
	}
	if e := auth.AuthorizeAccepted(tx, scope.PrincipalID, scope.AuthEpoch, scope.Purpose, scope.Target, scope.CredentialScope); e != nil {
		return e
	}
	if e := targetAuthority(tx, scope.Purpose, scope.Target); e != nil {
		return e
	}
	for project, revision := range j.Permissions {
		p, e := tx.Scope(project)
		if e != nil {
			return e
		}
		if p.Project.PermissionsRevision != revision {
			return domain.ErrDenied
		}
	}
	if j.Kind == "upload_seal" {
		u, e := tx.Upload(j.UploadID)
		if e != nil {
			return e
		}
		if u.State != "sealing" || u.SealJobID != j.ID {
			return domain.ErrStaleWorker
		}
	}
	return nil
}
func (s *Service) fileReader(ctx context.Context, u, path string, expected int64) (io.ReadCloser, <-chan error) {
	r, w := io.Pipe()
	done := make(chan error, 1)
	go func() {
		var result error
		defer func() { _ = w.CloseWithError(result); done <- result; close(done) }()
		offset := int64(-1)
		consumed := int64(0)
		for {
			var ranges []domain.UploadRange
			result = s.Store.Write(ctx, func(tx app.WriteTx) error { var e error; ranges, e = tx.Ranges(u, path, offset, 64); return e })
			if result != nil {
				return
			}
			if len(ranges) == 0 {
				break
			}
			last := false
			for _, part := range ranges {
				if part.Path != path {
					last = true
					break
				}
				if part.Offset != consumed {
					result = ErrPackageInvalid
					return
				}
				result = s.Bytes.Read(ctx, domain.Object{Hash: part.Hash, Size: part.Size}, domain.Pin{Kind: "upload_read", OwnerID: u}, func(reader io.ReadSeeker) error {
					n, e := io.CopyBuffer(w, reader, make([]byte, 64<<10))
					if e == nil && n != part.Size {
						return ErrHashMismatch
					}
					return e
				})
				if result != nil {
					return
				}
				consumed += part.Size
				offset = part.Offset
			}
			if last || len(ranges) < 64 {
				break
			}
		}
		if consumed != expected {
			result = ErrPackageInvalid
		}
	}()
	return r, done
}
func (s *Service) streamObject(ctx context.Context, j domain.Job, u, path string, size int64, commit func(app.WriteTx, domain.Object) error) (domain.Object, error) {
	r, done := s.fileReader(ctx, u, path, size)
	obj, err := s.Bytes.Put(ctx, r, commit)
	_ = r.Close()
	readErr := <-done
	return obj, errors.Join(err, readErr)
}
func (s *Service) seal(ctx context.Context, j domain.Job) error {
	var u domain.Upload
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		if e := s.jobFence(tx, j); e != nil {
			return e
		}
		var e error
		u, e = tx.Upload(j.UploadID)
		if e != nil {
			return e
		}
		if e := tx.DiscardContentSet(j.ID); e != nil {
			return e
		}
		return tx.CreateContentSet(j.ID, app.EmptyInventorySHA256)
	})
	if e != nil {
		return e
	}
	emit := func(input EntryInput, r io.Reader) error {
		if e := ValidatePath(input.Path); e != nil {
			return e
		}
		for _, p := range parents(input.Path) {
			e := s.Store.Write(ctx, func(tx app.WriteTx) error {
				if e := s.jobFence(tx, j); e != nil {
					return e
				}
				return tx.StageEntry(domain.Entry{ContentSetID: j.ID, Path: p, Kind: "directory"})
			})
			if e != nil {
				return e
			}
		}
		entry := domain.Entry{ContentSetID: j.ID, Path: input.Path, Kind: input.Kind, LinkText: input.LinkTarget, Size: input.Size}
		if _, e := InventoryLine(entry); e != nil {
			return e
		}
		if input.Kind == "file" {
			_, e := s.Bytes.Put(ctx, &fencedReader{Reader: r, ctx: ctx, service: s, job: j}, func(tx app.WriteTx, o domain.Object) error {
				if e := s.jobFence(tx, j); e != nil {
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
			if e := s.jobFence(tx, j); e != nil {
				return e
			}
			return tx.StageEntry(entry)
		})
	}
	if u.InputMode == "directory" {
		after := ""
		for {
			var entries []domain.UploadEntry
			e = s.Store.Write(ctx, func(tx app.WriteTx) error { var e error; entries, e = tx.UploadEntries(u.ID, after, 64); return e })
			if e != nil {
				return e
			}
			if len(entries) == 0 {
				break
			}
			for _, entry := range entries {
				if entry.Kind == "file" {
					r, done := s.fileReader(ctx, u.ID, entry.Path, entry.Size)
					e = emit(EntryInput{Path: entry.Path, Kind: "file", Size: entry.Size}, r)
					_ = r.Close()
					e = errors.Join(e, <-done)
				} else {
					e = emit(EntryInput{Path: entry.Path, Kind: entry.Kind, LinkTarget: entry.LinkText}, nil)
				}
				if e != nil {
					return e
				}
				after = entry.Path
			}
		}
	} else {
		var entries []domain.UploadEntry
		e = s.Store.Write(ctx, func(tx app.WriteTx) error { var e error; entries, e = tx.UploadEntries(u.ID, "", 2); return e })
		if e != nil {
			return e
		}
		if len(entries) != 1 || entries[0].Path != "_input" {
			return ErrPackageInvalid
		}
		obj, e := s.streamObject(ctx, j, u.ID, "_input", entries[0].Size, func(tx app.WriteTx, o domain.Object) error {
			if e := s.jobFence(tx, j); e != nil {
				return e
			}
			return tx.AcquirePin(domain.Pin{ID: "seal-input:" + j.ID, Kind: "job", OwnerID: j.ID, Hash: o.Hash})
		})
		if e != nil {
			return e
		}
		e = s.Bytes.Read(ctx, obj, domain.Pin{Kind: "job", OwnerID: j.ID}, func(reader io.ReadSeeker) error {
			if u.InputMode == "archive" {
				if s.Codec == nil {
					return ErrPackageInvalid
				}
				return s.Codec.Expand(ctx, &seekReaderAt{reader: reader}, obj.Size, emit)
			}
			path := u.SelectionName
			if u.InputMode == "object" || path == "" {
				path = "_input"
			}
			return emit(EntryInput{Path: path, Kind: "file", Size: obj.Size}, reader)
		})
		if e != nil {
			return e
		}
		if u.InputMode == "object" {
			e = s.Store.Write(ctx, func(tx app.WriteTx) error {
				if e := s.jobFence(tx, j); e != nil {
					return e
				}
				return tx.PutUploadObject(u.ID, obj.Hash)
			})
			if e != nil {
				return e
			}
		}
	}
	h := sha256.New()
	after := ""
	for {
		var rows []domain.Entry
		e = s.Store.Write(ctx, func(tx app.WriteTx) error { var e error; rows, e = tx.Entries(j.ID, after, 64); return e })
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
		}
	}
	return s.Store.Write(ctx, func(tx app.WriteTx) error {
		if e := s.jobFence(tx, j); e != nil {
			return e
		}
		if e := tx.SealContentSetDigest(j.ID, hex.EncodeToString(h.Sum(nil))); e != nil {
			return e
		}
		if e := tx.FinishSeal(j.ID, j.AttemptGeneration, j.ID); e != nil {
			return e
		}
		if e := tx.SetJobResult(j.ID, encoded(map[string]any{"upload_id": u.ID, "content_set_id": j.ID})); e != nil {
			return e
		}
		return tx.ReleasePin("seal-input:" + j.ID)
	})
}

type seekReaderAt struct {
	mu     sync.Mutex
	reader io.ReadSeeker
}

func (r *seekReaderAt) ReadAt(p []byte, offset int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, e := r.reader.Seek(offset, io.SeekStart); e != nil {
		return 0, e
	}
	n, e := io.ReadFull(r.reader, p)
	if e == io.ErrUnexpectedEOF {
		e = io.EOF
	}
	return n, e
}

type fencedReader struct {
	io.Reader
	ctx     context.Context
	service *Service
	job     domain.Job
}

func (r *fencedReader) Read(p []byte) (int, error) {
	if e := r.service.Store.Write(r.ctx, func(tx app.WriteTx) error { return r.service.jobFence(tx, r.job) }); e != nil {
		return 0, e
	}
	return r.Reader.Read(p)
}
