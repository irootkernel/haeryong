package generation

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// BodyRef is the existing sealed body transfer descriptor. The handle is an
// exact operation/target capability; only its verified bytes are request identity.
type BodyRef struct {
	ID        string `json:"object_id"`
	Hash      string `json:"bytes_sha256"`
	Size      string `json:"byte_size"`
	MediaType string `json:"media_type"`
}

func (s *Service) promptTarget(key string, input Request) []byte {
	return encode(map[string]any{"action": "generation", "operation_id": key, "project_id": input.ProjectID, "document_id": input.DocumentID})
}
func (s *Service) promptAuthority(ctx context.Context, tx app.WriteTx, token, key string, input Request) error {
	actor, e := s.Auth.AuthenticateWrite(ctx, tx, token)
	if e != nil {
		return e
	}
	r := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: "POST", Target: "/api/v1/generations", Body: encode(map[string]any{"project_id": input.ProjectID, "document_id": input.DocumentID})}
	if e = s.Auth.Authority(token).AuthorizeWrite(ctx, tx, r); e != nil {
		return e
	}
	return app.AuthorizeTarget(tx, s.promptTarget(key, input))
}
func (s *Service) promptIdentity(ctx context.Context, token, key string, input Request) (domain.Object, error) {
	if input.BodyRef == nil {
		if strings.TrimSpace(input.Prompt) == "" || !utf8.ValidString(input.Prompt) {
			return domain.Object{}, sec.ErrInvalid
		}
		h := sha256.Sum256([]byte(input.Prompt))
		return domain.Object{Hash: hex.EncodeToString(h[:]), Size: int64(len(input.Prompt))}, nil
	}
	if input.Prompt != "" || input.BodyRef.MediaType != "text/plain" {
		return domain.Object{}, sec.ErrInvalid
	}
	size, e := strconv.ParseInt(input.BodyRef.Size, 10, 64)
	if e != nil || size < 0 || strconv.FormatInt(size, 10) != input.BodyRef.Size {
		return domain.Object{}, sec.ErrInvalid
	}
	object := domain.Object{Hash: input.BodyRef.Hash, Size: size}
	e = s.Store.Write(ctx, func(tx app.WriteTx) error {
		if e := s.promptAuthority(ctx, tx, token, key, input); e != nil {
			return e
		}
		u, e := tx.Upload(input.BodyRef.ID)
		if e != nil {
			return e
		}
		target, e := app.NormalizeTarget("body", s.promptTarget(key, input))
		if e != nil {
			return e
		}
		if u.Purpose != "body" || string(u.Target) != string(target) || u.ObjectHash != object.Hash || u.Checkpoint.AcceptedBytes != input.BodyRef.Size {
			return sec.ErrInvalid
		}
		// A completed exact receipt can replay after its transport handle is no
		// longer reusable. Fresh adoption below still requires sealed ready bytes.
		return nil
	})
	return object, e
}
func (s *Service) preparePrompt(ctx context.Context, token, key string, input Request, expected domain.Object) (domain.Object, func(), error) {
	id, e := service.NewID()
	if e != nil {
		return domain.Object{}, func() {}, e
	}
	pin := domain.Pin{ID: "generation-request:" + id, Kind: "generation_prepare", OwnerID: id, Hash: expected.Hash}
	release := func() {
		_ = s.Store.Write(context.Background(), func(tx app.WriteTx) error { return tx.ReleasePin(pin.ID) })
	}
	check := func(tx app.WriteTx) error {
		if e := s.promptAuthority(ctx, tx, token, key, input); e != nil {
			return e
		}
		if input.BodyRef != nil {
			u, e := tx.Upload(input.BodyRef.ID)
			if e != nil {
				return e
			}
			if u.State != "sealed" || u.ObjectHash != expected.Hash || u.Checkpoint.AcceptedBytes != strconv.FormatInt(expected.Size, 10) {
				return domain.ErrTransition
			}
		}
		return nil
	}
	if s.Bytes == nil {
		return domain.Object{}, release, domain.ErrReconciliation
	}
	if input.BodyRef == nil {
		object, e := s.Bytes.Put(ctx, strings.NewReader(input.Prompt), func(tx app.WriteTx, o domain.Object) error {
			if e := check(tx); e != nil {
				return e
			}
			if o.Hash != expected.Hash || o.Size != expected.Size {
				return domain.ErrContentIntegrity
			}
			return tx.AcquirePin(pin)
		})
		return object, release, e
	}
	e = s.Store.Write(ctx, func(tx app.WriteTx) error {
		if e := check(tx); e != nil {
			return e
		}
		o, e := tx.Object(expected.Hash)
		if e != nil {
			return e
		}
		if o.Size != expected.Size || o.State != "ready" {
			return domain.ErrContentIntegrity
		}
		return tx.AcquirePin(pin)
	})
	if e == nil {
		e = s.Bytes.Read(ctx, expected, domain.Pin{Kind: "generation_prepare", OwnerID: id}, func(r io.ReadSeeker) error {
			reader := bufio.NewReader(&promptReader{reader: r, ctx: ctx, store: s.Store, check: check})
			nonspace := false
			for {
				value, size, e := reader.ReadRune()
				if e == io.EOF {
					break
				}
				if e != nil {
					return e
				}
				if value == utf8.RuneError && size == 1 {
					return sec.ErrInvalid
				}
				nonspace = nonspace || !unicode.IsSpace(value)
			}
			if !nonspace {
				return sec.ErrInvalid
			}
			return nil
		})
	}
	return expected, release, e
}

type promptReader struct {
	reader io.Reader
	ctx    context.Context
	store  app.Store
	check  func(app.WriteTx) error
}

func (r *promptReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	if e := r.store.Write(r.ctx, r.check); e != nil {
		return 0, e
	}
	return r.reader.Read(p)
}

// Keep the literal JSON body out of job and operation normalization. All input
// selectors normalize to the exact retained UTF-8 digest and length.
func promptDescriptor(object domain.Object) *domain.ProviderText {
	return &domain.ProviderText{Hash: object.Hash, Size: object.Size}
}
