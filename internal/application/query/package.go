package query

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// GuardPackage renews export authority for the package route. It exercises
// the package.export scope branch, never the ordinary version.read guard, so
// an export-only credential can stream the archive while a read-only
// credential cannot.
func (s *Service) GuardPackage(ctx context.Context, token, version string) error {
	return s.read(ctx, token, "/api/v1/versions/"+version+"/package", func(tx app.WriteTx) error {
		_, err := visibleVersion(tx, version)
		return err
	})
}

// Package assembles one published version's complete original directory as a
// streamed deterministic ZIP archive: every stored entry in exact stored-path
// byte order, empty directories as directory entries, symlinks carrying their
// exact link text, and no service metadata inside the archive. Integrity of
// the exported payload is each entry's exact bytes (bytes_sha256); the
// version's payload_sha256 stays the canonical inventory identity and is
// never a digest of these archive wire bytes. check renews authority before
// every entry and between pages; a failing check or read aborts the archive.
// A failed assembly never closes the writer, so no source error finalizes a
// partial archive into a complete-looking one.
func (s *Service) Package(ctx context.Context, token, id string, sink io.Writer, check func() error) error {
	archive := zip.NewWriter(&packageSink{sink: sink, check: check})
	if err := s.walkPackage(ctx, token, id, check, archive); err != nil {
		return err
	}
	return archive.Close()
}

func (s *Service) walkPackage(ctx context.Context, token, id string, check func() error, archive *zip.Writer) error {
	if err := check(); err != nil {
		return err
	}
	target := "/api/v1/versions/" + id + "/package"
	after := ""
	for {
		var entries []domain.Entry
		err := s.read(ctx, token, target, func(tx app.WriteTx) error {
			v, err := visibleVersion(tx, id)
			if err != nil {
				return err
			}
			entries, err = tx.QueryEntries(v.ContentSetID, after, "", "", 256)
			return err
		})
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return nil
		}
		for _, e := range entries {
			after = e.Path
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := check(); err != nil {
				return err
			}
			err := appendEntry(archive, e, func(w io.Writer) error {
				return s.Bytes.Read(ctx, domain.Object{Hash: e.Hash, Size: e.Size}, domain.Pin{Kind: "read", OwnerID: id}, func(handle io.ReadSeeker) error {
					_, err := io.CopyBuffer(w, &packageReader{ReadSeeker: handle, check: check}, make([]byte, 64<<10))
					return err
				})
			})
			if err != nil {
				return err
			}
		}
	}
}

// appendEntry writes one inventory row as one archive record using the exact
// stored path. Directories keep a trailing-slash directory record so empty
// directories survive; symlinks store the exact link text as their body with
// symlink mode bits, mirroring the import-side archive conventions so the
// export round-trips through the same expansion.
func appendEntry(archive *zip.Writer, e domain.Entry, body func(io.Writer) error) error {
	switch e.Kind {
	case "directory":
		header := &zip.FileHeader{Name: e.Path + "/", Method: zip.Store}
		header.SetMode(os.ModeDir | 0o755)
		_, err := archive.CreateHeader(header)
		return err
	case "symlink":
		header := &zip.FileHeader{Name: e.Path, Method: zip.Store}
		header.SetMode(os.ModeSymlink | 0o777)
		w, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(e.LinkText)); err != nil {
			return err
		}
		return nil
	case "file":
		header := &zip.FileHeader{Name: e.Path, Method: zip.Deflate}
		header.SetMode(0o644)
		w, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		if body == nil {
			return nil
		}
		return body(w)
	default:
		return &Error{"unsupported_entry_kind"}
	}
}

// packageSink renews transfer authority around every archive write so a
// revoked or newly restricted session cannot keep streaming an export.
type packageSink struct {
	sink  io.Writer
	check func() error
}

func (w *packageSink) Write(p []byte) (int, error) {
	if err := w.check(); err != nil {
		return 0, err
	}
	return w.sink.Write(p)
}

// packageReader applies the same authority renewal to object reads inside one
// file entry and caps single read sizes like the guarded file delivery.
type packageReader struct {
	io.ReadSeeker
	check func() error
	err   error
}

func (r *packageReader) Read(p []byte) (int, error) {
	if err := r.check(); err != nil {
		r.err = err
		return 0, err
	}
	if len(p) > 32*1024 {
		p = p[:32*1024]
	}
	n, err := r.ReadSeeker.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = err
	}
	return n, err
}
