package auth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"strings"
)

type ResumeJobRequest struct {
	Decision           string `json:"decision"`
	ExpectedGeneration int64  `json:"expected_attempt_generation"`
}

func JobETag(j domain.Job) string {
	b, _ := json.Marshal(j)
	h := sha256.Sum256(b)
	return fmt.Sprintf(`"job-%s-%x"`, j.ID, h[:])
}

// Provider operational status remains owner-readable when its original payload
// becomes hidden. It carries no scope, result, source identity or private text.
func projectProviderJob(tx app.WriteTx, actor domain.Session, j domain.Job) (domain.Job, error) {
	e := visibleJob(tx, j)
	if e == nil {
		return j, nil
	}
	if !domain.ProviderJob(j.Kind) || actor.Scope.Kind != "owner" || (!errors.Is(e, domain.ErrNotFound) && !errors.Is(e, domain.ErrDenied)) {
		return j, e
	}
	var scope domain.ProviderScope
	if json.Unmarshal(j.Scope, &scope) != nil || scope.PrincipalID != actor.PrincipalID {
		return j, domain.ErrNotFound
	}
	j.ProjectionETag = JobETag(j)
	j.Scope = json.RawMessage(`{}`)
	j.Result = nil
	j.Permissions = nil
	j.ParentID = ""
	j.UploadID = ""
	return j, nil
}
func providerReconcileOnly(tx app.WriteTx, actor domain.Session, r domain.Request) (bool, error) {
	parts := strings.Split(strings.Trim(r.Target, "/"), "/")
	if len(parts) != 5 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "jobs" || parts[4] != "resume" || r.Method != "POST" {
		return false, nil
	}
	var input ResumeJobRequest
	if json.Unmarshal(r.Body, &input) != nil || input.Decision != "reconcile_only" {
		return false, nil
	}
	if actor.Scope.Kind != "owner" || actor.PrincipalID != r.Key.PrincipalID {
		return false, domain.ErrDenied
	}
	j, e := tx.Job(parts[3])
	if e != nil {
		return false, e
	}
	if !domain.ProviderJob(j.Kind) {
		return false, nil
	}
	var scope domain.ProviderScope
	if json.Unmarshal(j.Scope, &scope) != nil || scope.PrincipalID != actor.PrincipalID {
		return false, domain.ErrNotFound
	}
	return true, nil
}
func (s *Service) ResumeJob(ctx context.Context, token, id, key, match string, input ResumeJobRequest) (domain.Response, error) {
	if match == "" {
		return domain.Response{}, sec.ErrMissing
	}
	if input.ExpectedGeneration < 1 || (input.Decision != "continue" && input.Decision != "retry" && input.Decision != "reconcile_only") {
		return domain.Response{}, sec.ErrInvalid
	}
	actor, e := s.Authenticate(ctx, token)
	if e != nil {
		return domain.Response{}, e
	}
	if actor.Scope.Kind != "owner" {
		return domain.Response{}, domain.ErrDenied
	}
	b, _ := json.Marshal(input)
	r := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: "POST", Target: "/api/v1/jobs/" + id + "/resume", Body: b, Preconditions: map[string]string{"If-Match": match}}
	return app.Execute(ctx, s.Store, s.Authority(token), r, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		j, e := tx.Job(id)
		if e != nil {
			return domain.Response{}, e
		}
		if JobETag(j) != match {
			return domain.Response{}, domain.ErrPrecondition
		}
		h, e := tx.RecoveryHold()
		if e != nil {
			return domain.Response{}, e
		}
		if h != nil && h.State != "reviewed" {
			return domain.Response{}, domain.ErrReconciliation
		}
		j, e = tx.ResumeProviderJob(id, input.ExpectedGeneration, input.Decision)
		if errors.Is(e, domain.ErrStaleWorker) {
			e = domain.ErrTransition
		}
		if e != nil {
			return domain.Response{}, e
		}
		body, _ := json.Marshal(j)
		return domain.Response{Status: 200, Body: body, Headers: map[string]string{"ETag": JobETag(j)}}, nil
	})
}
