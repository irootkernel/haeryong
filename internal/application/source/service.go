// Package source owns project registration, the exact-revision source reader
// use cases, imported-evidence verification and the shared eligibility rule.
package source

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	appauth "haeryong.local/haeryong/internal/application/auth"
	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// Bytes verifies retained objects before use; the foundation store supplies
// it (digest verification produces domain.ErrContentIntegrity on corruption).
type Bytes interface {
	Read(ctx context.Context, object domain.Object, pin domain.Pin, consume func(io.ReadSeeker) error) error
}

// Reader is the exact-revision source reader port (system git adapter).
type Reader interface {
	ResolveRevision(ctx context.Context, root, revision string) error
	ResolveHead(ctx context.Context, root string) (string, error)
	ChangedPaths(ctx context.Context, root, base, tip string, cited []string) ([]string, error)
	ReadBlob(ctx context.Context, root, revision, path string) (io.ReadCloser, error)
}

type Service struct {
	Store  app.Store
	Auth   *appauth.Service
	Bytes  Bytes
	Reader Reader
	Now    func() time.Time
	// CheckBound overrides the detached check bound (tests, D4).
	CheckBound time.Duration
	// Fault injects staged-protocol boundaries for tests only.
	Fault func(stage string) error
}

func New(store app.Store, auth *appauth.Service, bytes Bytes, reader Reader) *Service {
	return &Service{Store: store, Auth: auth, Bytes: bytes, Reader: reader, Now: time.Now}
}

func (s *Service) now() string {
	// S13: fixed-width millisecond layout keeps text ordering of
	// checked_at/captured_at/observed_at correct within a second.
	return s.Now().UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z07:00")
}

func encoded(value any) []byte {
	b, _ := json.Marshal(value)
	return b
}

const nameMax = 128

// Register implements POST /projects: logical identity registration with
// remembered source/disclosure choices left to project-scoped configuration
// plans. The storage method emits project.updated, the implemented
// project-event kind.
func (s *Service) Register(ctx context.Context, token, key, name string) (domain.Response, error) {
	// Authentication precedes body validation, matching the existing
	// handlers (C1): an unauthenticated caller learns nothing about names.
	actor, err := s.Auth.Authenticate(ctx, token)
	if err != nil {
		return domain.Response{}, err
	}
	if strings.TrimSpace(name) == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > nameMax {
		return domain.Response{}, sec.ErrInvalid
	}
	request := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: "POST", Target: "/api/v1/projects", Body: encoded(map[string]string{"name": name})}
	var response domain.Response
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		if err := s.Auth.Authority(token).AuthorizeWrite(ctx, tx, request); err != nil {
			return err
		}
		receipt, existing, err := tx.ReserveOperation(request, func() ([]byte, error) {
			id, err := service.NewID()
			return encoded(map[string]string{"project_id": id}), err
		})
		if err != nil {
			return err
		}
		if existing {
			switch receipt.State {
			case "completed", "failed":
				response = *receipt.Response
				return nil
			case "reconciling":
				return domain.ErrReconciliation
			}
			response = PendingResponse(key)
			return nil
		}
		var normalization struct {
			ProjectID string `json:"project_id"`
		}
		if err := json.Unmarshal(receipt.Normalization, &normalization); err != nil {
			return err
		}
		if err := tx.RegisterProject(domain.Project{ID: normalization.ProjectID, Name: name}); err != nil {
			return err
		}
		row, err := tx.QueryProject(normalization.ProjectID)
		if err != nil {
			return err
		}
		response = domain.Response{Status: 201, Body: encoded(projectWire(row))}
		return tx.CompleteOperation(request.Key, response)
	})
	return response, err
}

// PendingResponse is the stable registry 202 envelope for an identical
// concurrent request; it never implies a second effect.
func PendingResponse(key string) domain.Response {
	return domain.Response{Status: 202, Body: encoded(map[string]any{"operation_id": key, "state": "accepted", "operation_url": "/api/v1/operations/" + key})}
}

// projectWire composes the shared projection with its validator (S25).
func projectWire(p domain.QueryProject) map[string]any {
	view := domain.ProjectView(p)
	view["etag"] = domain.ProjectETag(p)
	return view
}
