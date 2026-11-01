package importer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

// prepareDraftCandidate streams the supplied metadata and manifest outside the
// writer. The staged inventory already has service-derived byte identity.
func (s *Service) prepareDraftCandidate(ctx context.Context, i domain.ImportIntent, metadata domain.Object, check func(app.WriteTx) error) (domain.Version, error) {
	var page *string
	after := ""
	for page == nil {
		var rows []domain.Entry
		e := s.Store.Write(ctx, func(tx app.WriteTx) error {
			if e := check(tx); e != nil {
				return e
			}
			var e error
			rows, e = tx.Entries(i.ContentSetID, after, 64)
			return e
		})
		if e != nil {
			return domain.Version{}, e
		}
		if len(rows) == 0 {
			break
		}
		for _, entry := range rows {
			after = entry.Path
			p := strings.ToLower(entry.Path)
			if entry.Kind == "file" && (strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".htm")) {
				page = strptr(entry.Path)
				break
			}
		}
	}
	v := normalizedVersion(i, page)
	if metadata.Hash != "" {
		e := s.Bytes.Read(ctx, metadata, domain.Pin{Kind: "import_prepare", OwnerID: i.JobID}, func(r io.ReadSeeker) error {
			return s.parseMetadata(ctx, bufio.NewReader(&generationReader{Reader: r, ctx: ctx, store: s.Store, check: check}), i, &v, nil)
		})
		if e != nil {
			return v, e
		}
	}
	i.Title = v.Title
	summary := normalizedVersion(i, v.DefaultPage)
	var value map[string]any
	if e := json.Unmarshal(summary.Metadata, &value); e != nil {
		return v, e
	}
	value["source_basis"] = v.SourceBasis
	v.Metadata = encoded(value)
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		var e error
		if metadata.Hash == "" {
			e = writeGenerated(writer, i, v, nil)
		} else {
			e = s.Bytes.Read(ctx, metadata, domain.Pin{Kind: "import_prepare", OwnerID: i.JobID}, func(r io.ReadSeeker) error { return writeGenerated(writer, i, v, r) })
		}
		_ = writer.CloseWithError(e)
		done <- e
	}()
	obj, e := s.Bytes.Put(ctx, &generationReader{Reader: reader, ctx: ctx, store: s.Store, check: check}, func(tx app.WriteTx, o domain.Object) error {
		if e := check(tx); e != nil {
			return e
		}
		return tx.AcquirePin(domain.Pin{ID: "metadata-request:" + v.ID, Kind: "import", OwnerID: v.ID, Hash: o.Hash})
	})
	_ = reader.Close()
	e = errors.Join(e, <-done)
	if e != nil {
		return v, e
	}
	v.ManifestSHA = obj.Hash
	return v, nil
}
