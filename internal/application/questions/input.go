package questions

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

func textObject(value []byte) domain.Object {
	hash := sha256.Sum256(value)
	return domain.Object{Hash: hex.EncodeToString(hash[:]), Size: int64(len(value))}
}

func (s *Service) textIdentity(ctx context.Context, token, key, version string, input Request) (domain.Object, error) {
	if input.BodyRef == nil {
		if strings.TrimSpace(input.Question) == "" || !utf8.ValidString(input.Question) {
			return domain.Object{}, sec.ErrInvalid
		}
		return textObject([]byte(input.Question)), nil
	}
	ref := input.BodyRef
	size, e := strconv.ParseInt(ref.Size, 10, 64)
	if e != nil || size < 0 || strconv.FormatInt(size, 10) != ref.Size || ref.MediaType != "text/plain" || input.Question != "" {
		return domain.Object{}, sec.ErrInvalid
	}
	object := domain.Object{Hash: ref.Hash, Size: size}
	e = s.Store.Write(ctx, func(tx app.WriteTx) error {
		if _, e := s.owner(ctx, tx, token, key, version, input.PagePath); e != nil {
			return e
		}
		u, e := tx.Upload(ref.ID)
		if e != nil {
			return e
		}
		target, e := app.NormalizeTarget("body", textTarget(key, version, input.PagePath))
		if e != nil {
			return e
		}
		if u.Purpose != "body" || string(u.Target) != string(target) || u.ObjectHash != ref.Hash || u.Checkpoint.AcceptedBytes != ref.Size {
			return sec.ErrInvalid
		}
		return nil
	})
	return object, e
}

func (s *Service) temporaryPin(hash string) (domain.Pin, func(), error) {
	id, e := service.NewID()
	if e != nil {
		return domain.Pin{}, func() {}, e
	}
	pin := domain.Pin{ID: "question-prepare:" + id, Kind: "question_prepare", OwnerID: id, Hash: hash}
	release := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Store.Write(ctx, func(tx app.WriteTx) error { return tx.ReleasePin(pin.ID) })
	}
	return pin, release, nil
}

func (s *Service) prepareText(ctx context.Context, token, key, version string, input Request, expected domain.Object) (domain.Object, func(), error) {
	pin, release, e := s.temporaryPin(expected.Hash)
	if e != nil {
		return domain.Object{}, release, e
	}
	check := func(tx app.WriteTx) error {
		if _, e := s.owner(ctx, tx, token, key, version, input.PagePath); e != nil {
			return e
		}
		if input.BodyRef != nil {
			u, e := tx.Upload(input.BodyRef.ID)
			if e != nil {
				return e
			}
			if u.State != "sealed" || u.ObjectHash != expected.Hash || u.Checkpoint.AcceptedBytes != input.BodyRef.Size {
				return domain.ErrTransition
			}
		}
		return nil
	}
	if s.Bytes == nil {
		return domain.Object{}, release, domain.ErrReconciliation
	}
	if input.BodyRef == nil {
		object, e := s.Bytes.Put(ctx, strings.NewReader(input.Question), func(tx app.WriteTx, object domain.Object) error {
			if e := check(tx); e != nil {
				return e
			}
			if object.Hash != expected.Hash || object.Size != expected.Size {
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
		object, e := tx.Object(expected.Hash)
		if e != nil {
			return e
		}
		if object.State != "ready" || object.Size != expected.Size {
			return domain.ErrContentIntegrity
		}
		return tx.AcquirePin(pin)
	})
	if e == nil {
		e = s.Bytes.Read(ctx, expected, domain.Pin{Kind: pin.Kind, OwnerID: pin.OwnerID}, func(r io.ReadSeeker) error {
			reader := bufio.NewReader(&inputReader{reader: r, ctx: ctx, store: s.Store, check: check})
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

func (s *Service) contextIdentity(ctx context.Context, token, version string, input Request) (*domain.QuestionContext, error) {
	if input.DisplayState != nil {
		if !utf8.Valid(input.DisplayState) || !json.Valid(input.DisplayState) {
			return nil, sec.ErrInvalid
		}
		object := textObject(input.DisplayState)
		return &domain.QuestionContext{Hash: object.Hash, Size: object.Size, MediaType: "application/json"}, nil
	}
	if input.ContextRef == nil {
		return nil, nil
	}
	if *input.ContextRef == "" {
		return nil, sec.ErrInvalid
	}
	var out domain.QuestionContext
	e := s.Store.Write(ctx, func(tx app.WriteTx) error {
		actor, e := s.owner(ctx, tx, token, "", version, input.PagePath)
		if e != nil {
			return e
		}
		c, e := tx.Context(*input.ContextRef)
		if e != nil {
			return e
		}
		if c.PrincipalID != actor.PrincipalID || c.VersionID != version || !reflect.DeepEqual(c.PagePath, input.PagePath) {
			return domain.ErrDenied
		}
		out = domain.QuestionContext{ID: c.ID, Hash: c.Object.Hash, Size: c.Object.Size, MediaType: c.MediaType, RenderInstanceID: c.RenderInstanceID}
		return nil
	})
	return &out, e
}

func (s *Service) prepareContext(ctx context.Context, token, key, version string, input Request, expected *domain.QuestionContext) (*domain.QuestionContext, func(), error) {
	if expected == nil {
		return nil, func() {}, nil
	}
	pin, release, e := s.temporaryPin(expected.Hash)
	if e != nil {
		return nil, release, e
	}
	if s.Bytes == nil {
		return nil, release, domain.ErrReconciliation
	}
	check := func(tx app.WriteTx, object domain.Object) error {
		actor, e := s.owner(ctx, tx, token, key, version, input.PagePath)
		if e != nil {
			return e
		}
		if expected.ID != "" {
			if e = checkContext(tx, actor, version, input.PagePath, *expected); e != nil {
				return e
			}
		}
		if object.Hash != expected.Hash || object.Size != expected.Size || object.State != "ready" {
			return domain.ErrContentIntegrity
		}
		return tx.AcquirePin(pin)
	}
	if expected.ID == "" {
		_, e = s.Bytes.Put(ctx, bytes.NewReader(input.DisplayState), check)
	} else {
		e = s.Store.Write(ctx, func(tx app.WriteTx) error {
			object, e := tx.Object(expected.Hash)
			if e != nil {
				return e
			}
			return check(tx, object)
		})
	}
	return expected, release, e
}

type inputReader struct {
	reader io.Reader
	ctx    context.Context
	store  app.Store
	check  func(app.WriteTx) error
}

func (r *inputReader) Read(value []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	if e := r.store.Write(r.ctx, r.check); e != nil {
		return 0, e
	}
	return r.reader.Read(value)
}
