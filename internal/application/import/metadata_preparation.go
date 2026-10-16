package importer

import (
	"bufio"
	"context"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	"io"
)

// A preparation owns a private incremental index, never a fabricated job.
// Parsing and verified file reads stay outside the short writer callbacks.
func (s *Service) prepareMetadata(ctx context.Context, token string, object domain.Object, set string) (id string, err error) {
	if object.Hash == "" {
		return "", nil
	}
	id, err = service.NewID()
	if err != nil {
		return id, err
	}
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		record, e := s.Auth.AuthenticateWrite(ctx, tx, token)
		if e != nil {
			return e
		}
		actual, e := tx.Object(object.Hash)
		if e != nil {
			return e
		}
		if actual.State != "ready" {
			return domain.ErrDeleting
		}
		return tx.CreateMetadataIndex(domain.MetadataIndex{ID: id, PrincipalID: record.PrincipalID, Hash: object.Hash, ContentSetID: set})
	})
	if err != nil {
		return id, err
	}
	defer func() {
		if err != nil {
			_ = s.releaseMetadataPreparation(id)
		}
	}()
	err = s.Bytes.Read(ctx, object, domain.Pin{Kind: "import_prepare", OwnerID: id}, func(reader io.ReadSeeker) error {
		sink := func(kind, key string, raw []byte) error {
			return s.Store.Write(ctx, func(tx app.WriteTx) error {
				if _, e := s.Auth.AuthenticateWrite(ctx, tx, token); e != nil {
					return e
				}
				return tx.PutImportMetadata(id, kind, key, raw)
			})
		}
		var version domain.Version
		return s.parseMetadata(ctx, bufio.NewReader(reader), domain.ImportIntent{}, &version, sink)
	})
	if err != nil {
		return id, err
	}
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		if _, e := s.Auth.AuthenticateWrite(ctx, tx, token); e != nil {
			return e
		}
		field, e := tx.ValidateImportMetadata(id, set)
		if e != nil {
			return e
		}
		if field != "" {
			return invalidMeta(field)
		}
		return tx.CompleteMetadataIndex(id)
	})
	return id, err
}
func (s *Service) releaseMetadataPreparation(id string) error {
	if id == "" {
		return nil
	}
	return s.Store.Write(context.Background(), func(tx app.WriteTx) error { return tx.ReleaseMetadataPreparation(id) })
}
func validatePreparedMetadata(tx app.WriteTx, id string, object domain.Object, set, principal string) error {
	if object.Hash == "" {
		return nil
	}
	index, err := tx.MetadataIndex(id)
	if err != nil {
		return err
	}
	if !index.Complete || index.JobID != "" || index.Hash != object.Hash || index.ContentSetID != set || index.PrincipalID != principal {
		return domain.ErrTransition
	}
	actual, err := tx.Object(object.Hash)
	if err != nil {
		return err
	}
	if actual.State != "ready" {
		return domain.ErrDeleting
	}
	return nil
}
