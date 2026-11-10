// Package auth supplies one-owner session/pairing use cases and current authority.
// Local bootstrap is invoked only by the authenticated protected-control adapter.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

type Settings = domain.AuthSettings
type Service struct {
	Store app.Store
	Now   func() time.Time
}

func New(store app.Store) *Service { return &Service{Store: store, Now: time.Now} }
func (s *Service) now() time.Time  { return s.Now().UTC().Truncate(time.Millisecond) }
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	var out Settings
	err := s.Store.Write(ctx, func(tx app.WriteTx) error { a, e := tx.AuthState(); out = a.Settings; return e })
	return out, err
}
func validOrigin(origin string) bool {
	u, e := url.Parse(origin)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.Opaque == "" && origin == u.Scheme+"://"+u.Host && !strings.ContainsAny(origin, "\r\n\t ")
}
func pairingETag(p domain.Pairing) string { return fmt.Sprintf("\"pairing-%s-r%d\"", p.ID, p.Revision) }
func (s *Service) CreatePairing(ctx context.Context, origin, label, client string) (domain.PairingCreated, error) {
	var out domain.PairingCreated
	if !validOrigin(origin) || !utf8.ValidString(label) || strings.TrimSpace(label) == "" || utf8.RuneCountInString(label) > 128 {
		return out, sec.ErrInvalid
	}
	id, err := service.NewID()
	if err != nil {
		return out, err
	}
	secret, err := sec.Secret()
	if err != nil {
		return out, err
	}
	code, err := sec.Code()
	if err != nil {
		return out, err
	}
	now := s.now()
	err = s.Store.Write(ctx, func(tx app.WriteTx) error {
		state, err := tx.AuthState()
		if err != nil {
			return err
		}
		clientHash := sec.Verifier("client", client)
		pending, created, err := tx.PairingStats(state.AuthEpoch, now.UnixMilli(), clientHash)
		if err != nil {
			return err
		}
		if client == "" {
			created = 0
		}
		if pending >= 5 || created >= 2 {
			return sec.ErrLimited
		}
		p := domain.Pairing{ID: id, DeviceLabel: label, Origin: origin, CreatedAt: now, ExpiresAt: time.Unix(now.Unix()+state.Settings.PairingExpirySeconds, int64(now.Nanosecond())).UTC(), State: "pending", Revision: 1, SecretHash: sec.Verifier("pairing", secret), CodeHash: sec.Verifier("code:"+id, code), ClientHash: clientHash, AuthEpoch: state.AuthEpoch, PollAfter: now}
		p.ETag = pairingETag(p)
		if err := tx.PutPairing(p); err != nil {
			return err
		}
		out = domain.PairingCreated{Pairing: p, Code: code, PollingSecret: secret}
		return nil
	})
	if err != nil {
		return domain.PairingCreated{}, err
	}
	return out, nil
}
func (s *Service) authenticateTx(tx app.WriteTx, token string, touch bool) (domain.Session, error) {
	if !sec.ValidSecret(token) {
		return domain.Session{}, sec.ErrRequired
	}
	state, err := tx.AuthState()
	if err != nil {
		return domain.Session{}, err
	}
	record, err := tx.SessionByHash(sec.Verifier("session", token))
	if errors.Is(err, domain.ErrNotFound) {
		return s.scoped(tx, token, state)
	}
	if err != nil {
		return record, err
	}
	record, err = sec.Check(record, state, s.now())
	if err != nil {
		return record, err
	}
	if touch {
		record.LastSeenAt = s.now()
		if err := tx.PutSession(record); err != nil {
			return record, err
		}
		record, err = tx.SessionByID(record.ID)
		if err != nil {
			return record, err
		}
		record, err = sec.Check(record, state, s.now())
	}
	return record, err
}

type passiveReadKey struct{}

// WithPassiveRead marks a read-only request context so current session validation
// suppresses last_seen_at / idle_expires_at renewal while still enforcing full
// current revocation, auth_epoch, idle-expiry, absolute-expiry and scope checks.
func WithPassiveRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, passiveReadKey{}, true)
}

// IsPassiveRead reports whether ctx suppresses session idle-window renewal.
func IsPassiveRead(ctx context.Context) bool {
	v, _ := ctx.Value(passiveReadKey{}).(bool)
	return v
}

// ValidatePassiveRead validates Haeryong-Passive-Read header values on an HTTP request.
// Only a single value "1" on GET or HEAD is valid; any other value, duplicate header,
// or non-GET/HEAD method returns sec.ErrInvalid before touching session state.
func ValidatePassiveRead(ctx context.Context, method string, values []string) (context.Context, error) {
	if len(values) == 0 {
		return ctx, nil
	}
	if len(values) != 1 || values[0] != "1" || (method != "GET" && method != "HEAD") {
		return ctx, sec.ErrInvalid
	}
	return WithPassiveRead(ctx), nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (domain.Session, error) {
	return s.checkSession(ctx, token, true)
}
func (s *Service) AuthenticatePassive(ctx context.Context, token string) (domain.Session, error) {
	return s.checkSession(ctx, token, false)
}
func (s *Service) checkSession(ctx context.Context, token string, touch bool) (domain.Session, error) {
	touch = touch && !IsPassiveRead(ctx)
	var record domain.Session
	err := s.Store.Write(ctx, func(tx app.WriteTx) error { var e error; record, e = s.authenticateTx(tx, token, touch); return e })
	return record, err
}
func (s *Service) Poll(ctx context.Context, id, secret, origin string) (domain.PairingPoll, error) {
	var out domain.PairingPoll
	if !sec.ValidSecret(secret) || !validOrigin(origin) {
		return out, sec.ErrRequired
	}
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		state, err := tx.AuthState()
		if err != nil {
			return err
		}
		p, err := tx.Pairing(id)
		if errors.Is(err, domain.ErrNotFound) {
			return sec.ErrRequired
		}
		if err != nil {
			return err
		}
		if p.Origin != origin || !sec.Equal(p.SecretHash, sec.Verifier("pairing", secret)) {
			return sec.ErrRequired
		}
		now := s.now()
		if !now.Before(p.ExpiresAt) || p.AuthEpoch != state.AuthEpoch {
			out.State = "expired"
			return nil
		}
		if p.Consumed {
			return domain.ErrTransition
		}
		if p.State == "rejected" {
			out.State = p.State
			return nil
		}
		if now.Before(p.PollAfter) {
			return sec.ErrLimited
		}
		p.PollAfter = now.Add(2 * time.Second)
		out.State = p.State
		if p.State == "approved" {
			token, err := sec.Secret()
			if err != nil {
				return err
			}
			sid, err := service.NewID()
			if err != nil {
				return err
			}
			record := domain.Session{ID: sid, PrincipalID: state.OwnerID, LibraryID: state.LibraryID, AuthEpoch: state.AuthEpoch, TokenHash: sec.Verifier("session", token), DeviceLabel: p.DeviceLabel, Origin: p.Origin, CreatedAt: now, LastSeenAt: now}
			if err := tx.PutSession(record); err != nil {
				return err
			}
			p.Consumed = true
			p.Revision++
			record, err = tx.SessionByID(record.ID)
			if err != nil {
				return err
			}
			record, err = sec.Check(record, state, now)
			if err != nil {
				return err
			}
			out.Session = &record
			out.Token = token
			if err := tx.AuthAudit(state.OwnerID, sid, "session_issued"); err != nil {
				return err
			}
		}
		return tx.PutPairing(p)
	})
	if err != nil {
		return domain.PairingPoll{}, err
	}
	return out, nil
}
func (s *Service) Pairings(ctx context.Context, token string) ([]domain.Pairing, error) {
	return s.pairings(ctx, token, false)
}
func (s *Service) BootstrapPairings(ctx context.Context) ([]domain.Pairing, error) {
	return s.pairings(ctx, "", true)
}
func (s *Service) pairings(ctx context.Context, token string, bootstrap bool) ([]domain.Pairing, error) {
	out := []domain.Pairing{}
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		if !bootstrap {
			if record, e := s.authenticateTx(tx, token, !IsPassiveRead(ctx)); e != nil {
				return e
			} else if record.Scope.Kind != "owner" {
				return domain.ErrDenied
			}
		}
		state, e := tx.AuthState()
		if e != nil {
			return e
		}
		rows, e := tx.Pairings(true)
		if e != nil {
			return e
		}
		for _, p := range rows {
			if p.AuthEpoch == state.AuthEpoch && s.now().Before(p.ExpiresAt) {
				out = append(out, p)
			}
		}
		return nil
	})
	return out, err
}
func (s *Service) Decide(ctx context.Context, token string, d domain.PairingDecision) (domain.Response, error) {
	return s.decide(ctx, token, d, false)
}
func (s *Service) BootstrapDecide(ctx context.Context, d domain.PairingDecision) (domain.Response, error) {
	return s.decide(ctx, "", d, true)
}
func (s *Service) decide(ctx context.Context, token string, d domain.PairingDecision, bootstrap bool) (domain.Response, error) {
	if d.Action != "approve" && d.Action != "reject" {
		return domain.Response{}, sec.ErrInvalid
	}
	var owner string
	if bootstrap {
		err := s.Store.Write(ctx, func(tx app.WriteTx) error { a, e := tx.AuthState(); owner = a.OwnerID; return e })
		if err != nil {
			return domain.Response{}, err
		}
	} else {
		record, err := s.Authenticate(ctx, token)
		if err != nil {
			return domain.Response{}, err
		}
		owner = record.PrincipalID
	}
	if d.OperationID == "" || d.ETag == "" {
		return domain.Response{}, sec.ErrMissing
	}
	if !canonicalID.MatchString(d.OperationID) || !canonicalID.MatchString(d.ID) || !pairingValidator.MatchString(d.ETag) {
		return domain.Response{}, sec.ErrInvalid
	}
	body, _ := json.Marshal(map[string]string{"code_verifier": sec.Verifier("code:"+d.ID, d.Code), "origin": d.Origin})
	request := domain.Request{Key: domain.OperationKey{PrincipalID: owner, OperationID: d.OperationID}, Method: "POST", Target: "/api/v1/auth/pairings/" + d.ID + "/" + d.Action, Body: body, Preconditions: map[string]string{"If-Match": d.ETag}}
	authority := &Authority{Service: s, Token: token, Bootstrap: bootstrap, Principal: owner}
	return app.Execute(ctx, s.Store, authority, request, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		if d.ETag == "" {
			return domain.Response{}, sec.ErrMissing
		}
		p, err := tx.Pairing(d.ID)
		if err != nil {
			return domain.Response{}, err
		}
		if p.ETag != d.ETag {
			return domain.Response{}, domain.ErrPrecondition
		}
		state, err := tx.AuthState()
		if err != nil {
			return domain.Response{}, err
		}
		if p.AuthEpoch != state.AuthEpoch || !s.now().Before(p.ExpiresAt) || p.Consumed {
			return domain.Response{}, domain.ErrTransition
		}
		if d.Origin != p.Origin || !sec.Equal(p.CodeHash, sec.Verifier("code:"+p.ID, d.Code)) {
			return domain.Response{}, sec.ErrInvalid
		}
		if (d.Action == "approve" && p.State != "pending") || (d.Action == "reject" && p.State != "pending" && p.State != "approved") {
			return domain.Response{}, domain.ErrTransition
		}
		if d.Action == "approve" {
			p.State = "approved"
		} else {
			p.State = "rejected"
		}
		p.Revision++
		p.ETag = pairingETag(p)
		if err := tx.PutPairing(p); err != nil {
			return domain.Response{}, err
		}
		actor := owner
		if bootstrap {
			actor = "bootstrap"
		}
		if err := tx.AuthAudit(actor, p.ID, "pairing_"+p.State); err != nil {
			return domain.Response{}, err
		}
		data, _ := json.Marshal(p)
		return domain.Response{Status: 200, Body: data, Headers: map[string]string{"ETag": p.ETag}}, nil
	})
}
func (s *Service) Sessions(ctx context.Context, token string) ([]domain.Session, error) {
	out := []domain.Session{}
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		if record, e := s.authenticateTx(tx, token, !IsPassiveRead(ctx)); e != nil {
			return e
		} else if record.Scope.Kind != "owner" {
			return domain.ErrDenied
		}
		a, e := tx.AuthState()
		if e != nil {
			return e
		}
		rows, e := tx.Sessions()
		if e != nil {
			return e
		}
		for _, r := range rows {
			r, err := sec.Check(r, a, s.now())
			if err != nil && !errors.Is(err, sec.ErrRevoked) && !errors.Is(err, sec.ErrExpired) {
				return err
			}
			out = append(out, r)
		}
		return nil
	})
	return out, err
}
func (s *Service) Revoke(ctx context.Context, token, id string) (domain.Session, error) {
	var out domain.Session
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		actor, e := s.authenticateTx(tx, token, true)
		if e != nil {
			return e
		}
		if actor.Scope.Kind != "owner" {
			return domain.ErrDenied
		}
		if id == "" {
			id = actor.ID
		}
		out, e = tx.SessionByID(id)
		if e != nil {
			return e
		}
		if out.Revoked {
			state, e := tx.AuthState()
			if e != nil {
				return e
			}
			var checkErr error
			out, checkErr = sec.Check(out, state, s.now())
			if checkErr != nil && !errors.Is(checkErr, sec.ErrRevoked) {
				return checkErr
			}
			return nil
		}
		out.Revoked = true
		out.State = "revoked"
		if e := tx.PutSession(out); e != nil {
			return e
		}
		if e := tx.AuthAudit(actor.PrincipalID, id, "session_revoked"); e != nil {
			return e
		}
		out, e = tx.SessionByID(id)
		if e != nil {
			return e
		}
		state, e := tx.AuthState()
		if e != nil {
			return e
		}
		var checkErr error
		out, checkErr = sec.Check(out, state, s.now())
		if checkErr != nil && !errors.Is(checkErr, sec.ErrRevoked) {
			return checkErr
		}
		return nil
	})
	return out, err
}

// Guard rechecks current authority before protected work and before its result is
// released. Streaming adapters also recheck between chunks/heartbeats.
func (s *Service) Guard(ctx context.Context, token string, fn func() error) error {
	if _, err := s.checkSession(ctx, token, false); err != nil {
		return err
	}
	if err := fn(); err != nil {
		return err
	}
	_, err := s.checkSession(ctx, token, false)
	return err
}
func (s *Service) Health(ctx context.Context, token string) (domain.Health, error) {
	if record, err := s.Authenticate(ctx, token); err != nil {
		return domain.Health{}, err
	} else if record.Scope.Kind != "owner" {
		return domain.Health{}, domain.ErrDenied
	}
	return s.Store.Health(ctx)
}
func (s *Service) Operation(ctx context.Context, token, id string) (domain.Receipt, error) {
	var out domain.Receipt
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		a, e := s.authenticateTx(tx, token, !IsPassiveRead(ctx))
		if e != nil {
			return e
		}
		out, e = tx.Receipt(domain.OperationKey{PrincipalID: a.PrincipalID, OperationID: id})
		if e != nil {
			return e
		}
		if e = authorizeScope(tx, a, out.Original); e != nil {
			return e
		}
		if ok, e := providerReconcileOnly(tx, a, out.Original); e != nil || ok {
			return e
		}
		if LifecycleRoute(out.Original.Target) {
			return lifecycleTarget(tx, out.Original)
		}
		return visibleRequest(tx, out.Original)
	})
	if err != nil {
		return out, err
	}
	if out.Response != nil {
		r, e := s.Authority(token).Filter(ctx, out.Original, *out.Response)
		if e != nil {
			return domain.Receipt{}, e
		}
		out.Response = &r
	}
	return out, nil
}
func (s *Service) Job(ctx context.Context, token, id string) (domain.Job, error) {
	var out domain.Job
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		record, e := s.authenticateTx(tx, token, !IsPassiveRead(ctx))
		if e != nil {
			return e
		}
		if e := authorizeScope(tx, record, domain.Request{Method: "GET", Target: "/api/v1/jobs/" + id}); e != nil {
			return e
		}
		out, e = tx.Job(id)
		if e == nil {
			out, e = projectProviderJob(tx, record, out)
		}
		return e
	})
	return out, err
}
func (s *Service) Jobs(ctx context.Context, token string) ([]domain.Job, error) {
	var out []domain.Job
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		record, e := s.authenticateTx(tx, token, !IsPassiveRead(ctx))
		if e != nil {
			return e
		}
		if e = authorizeScope(tx, record, domain.Request{Method: "GET", Target: "/api/v1/jobs"}); e != nil {
			return e
		}
		rows, e := tx.Jobs()
		if e != nil {
			return e
		}
		out = []domain.Job{}
		for _, j := range rows {
			e := authorizeScope(tx, record, domain.Request{Method: "GET", Target: "/api/v1/jobs/" + j.ID})
			if errors.Is(e, domain.ErrNotFound) || errors.Is(e, domain.ErrDenied) {
				continue
			}
			if e != nil {
				return e
			}
			j, e = projectProviderJob(tx, record, j)
			if errors.Is(e, domain.ErrNotFound) {
				continue
			}
			if e != nil {
				return e
			}
			out = append(out, j)
		}
		return nil
	})
	return out, err
}
func (s *Service) Events(ctx context.Context, token, epoch string, after int64) ([]domain.Event, error) {
	var out []domain.Event
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		record, e := s.authenticateTx(tx, token, false)
		if e != nil {
			return e
		}
		out, e = tx.Events(epoch, after, 64)
		if e != nil {
			return e
		}
		for i, event := range out {
			if event.ResourceKind == "question" {
				var e error
				out[i], e = visibleQuestionEvent(tx, record, event)
				if e != nil {
					return e
				}
				continue
			}
			if event.ResourceKind == "review" || event.ResourceKind == "annotation" || event.ResourceKind == "revisit" {
				if record.Scope.Kind != "owner" {
					out[i] = domain.Event{StreamEpoch: event.StreamEpoch, Sequence: event.Sequence, ResourceKind: "library", Kind: "library.invalidated"}
					continue
				}
				if e := visibleTarget(tx, personalEventTarget(event)); e != nil {
					if !errors.Is(e, domain.ErrNotFound) {
						return e
					}
					event = domain.Event{StreamEpoch: event.StreamEpoch, Sequence: event.Sequence, ResourceKind: "library", Kind: "library.invalidated"}
				}
				out[i] = event
				continue
			}
			if event.Kind == "search.index_updated" || event.Kind == "references.updated" {
				var err error
				out[i], err = visibleIndexEvent(tx, record, event)
				if err != nil {
					return err
				}
				continue
			}
			if record.Scope.Kind != "owner" {
				target := "/api/v1/" + event.ResourceKind + "s/" + event.ResourceID
				e := authorizeScope(tx, record, domain.Request{Method: "GET", Target: target})
				if errors.Is(e, domain.ErrDenied) || errors.Is(e, domain.ErrNotFound) {
					out[i] = domain.Event{StreamEpoch: event.StreamEpoch, Sequence: event.Sequence, ResourceKind: "library", Kind: "library.invalidated"}
					continue
				}
				if e != nil {
					return e
				}
			}

			if event.ResourceKind == "finding" {
				out[i] = visibleFindingEvent(tx, record, event)
				continue
			}
			if event.ResourceKind == "job" {
				job, err := tx.Job(event.ResourceID)
				if err == nil {
					err = visibleJob(tx, job)
				}
				if errors.Is(err, domain.ErrNotFound) {
					out[i].ResourceKind = "library"
					out[i].ResourceID = ""
					out[i].Kind = "library.invalidated"
					out[i].Body = nil
					out[i].Revision = 0
					continue
				}
				if err != nil {
					return err
				}
			}
			var target string
			if event.ResourceKind == "document" {
				target = "/api/v1/documents/" + event.ResourceID
			}
			if event.ResourceKind == "version" {
				target = "/api/v1/versions/" + event.ResourceID
			}
			if target != "" {
				err := visibleTarget(tx, target)
				if errors.Is(err, domain.ErrNotFound) {
					out[i].ResourceKind = "library"
					out[i].ResourceID = ""
					out[i].Kind = "library.invalidated"
					out[i].Body = nil
					out[i].Revision = 0
				} else if err != nil {
					return err
				}
			}
		}
		return nil
	})
	return out, err
}
func (s *Service) CancelJob(ctx context.Context, token, id, operationID string) (domain.Response, error) {
	record, err := s.Authenticate(ctx, token)
	if err != nil {
		return domain.Response{}, err
	}
	r := domain.Request{Key: domain.OperationKey{PrincipalID: record.PrincipalID, OperationID: operationID}, Method: "POST", Target: "/api/v1/jobs/" + id + "/cancel", Body: []byte(`{}`)}
	return app.Execute(ctx, s.Store, s.Authority(token), r, nil, func(tx app.WriteTx, _ domain.Receipt) (domain.Response, error) {
		current, err := s.authenticateTx(tx, token, false)
		if err != nil {
			return domain.Response{}, err
		}
		j, err := tx.CancelJob(id)
		if err != nil {
			return domain.Response{}, err
		}
		j, err = projectProviderJob(tx, current, j)
		if err != nil {
			return domain.Response{}, err
		}
		body, _ := json.Marshal(j)
		return domain.Response{Status: 200, Body: body}, nil
	})
}

type Authority struct {
	Service   *Service
	Token     string
	Bootstrap bool
	Principal string
}

func (s *Service) Authority(token string) app.Authority { return &Authority{Service: s, Token: token} }
func (a *Authority) Authorize(ctx context.Context, r domain.Request) error {
	if a.Bootstrap {
		if !strings.HasPrefix(r.Target, "/api/v1/auth/pairings/") || (!strings.HasSuffix(r.Target, "/approve") && !strings.HasSuffix(r.Target, "/reject")) {
			return domain.ErrDenied
		}
		return nil
	}
	record, err := a.Service.Authenticate(ctx, a.Token)
	if err != nil {
		return err
	}
	if record.PrincipalID != r.Key.PrincipalID {
		return domain.ErrDenied
	}
	return nil
}
func (a *Authority) AuthorizeWrite(ctx context.Context, tx app.WriteTx, r domain.Request) error {
	if a.Bootstrap {
		state, err := tx.AuthState()
		if err != nil {
			return err
		}
		if state.OwnerID != r.Key.PrincipalID || state.OwnerID != a.Principal {
			return domain.ErrDenied
		}
		return nil
	}
	record, err := a.Service.authenticateTx(tx, a.Token, false)
	if err != nil {
		return err
	}
	if record.PrincipalID != r.Key.PrincipalID {
		return domain.ErrDenied
	}
	return authorizeAuthenticated(tx, record, r)
}

// AuthenticateRequestWrite reuses the same authority checks without authenticating
// the same credential repeatedly inside a bounded search slice.
func (s *Service) AuthenticateRequestWrite(ctx context.Context, tx app.WriteTx, token string, r domain.Request) (domain.Session, error) {
	record, err := s.AuthenticateWrite(ctx, tx, token)
	if err != nil {
		return record, err
	}
	r.Key.PrincipalID = record.PrincipalID
	return record, authorizeAuthenticated(tx, record, r)
}
func authorizeAuthenticated(tx app.WriteTx, record domain.Session, r domain.Request) error {
	if err := authorizeScope(tx, record, r); err != nil {
		return err
	}
	if err := AuthorizeFreshWorkflowMutation(tx, record, r); err != nil {
		return err
	}
	if ok, e := providerReconcileOnly(tx, record, r); e != nil || ok {
		return e
	}
	// Only a current owner write may reach the personal service's explicit
	// lifecycle conflict. Saved responses and receipt reads retain strict
	// current visibility through visibleRequest.
	if record.Scope.Kind == "owner" && LifecycleRoute(r.Target) {
		return lifecycleTarget(tx, r)
	}
	if record.Scope.Kind == "owner" && personalWriteTarget(r.Method, r.Target) {
		parts := strings.Split(strings.Trim(strings.SplitN(r.Target, "?", 2)[0], "/"), "/")
		if len(parts) >= 4 && parts[2] == "versions" {
			gone, err := tx.TombstonedVersion(parts[3])
			if err != nil {
				return err
			}
			if gone {
				// Saved personal outcomes remain masked; a new owner write reports the
				// known terminal lifecycle even after private version rows are removed.
				if _, err = tx.Receipt(r.Key); err == nil {
					return domain.ErrNotFound
				} else if !errors.Is(err, domain.ErrNotFound) {
					return err
				}
				return &domain.LifecycleTransition{Lifecycle: "purged"}
			}
		}
		return visibleTargetWithoutLifecycleMask(tx, r.Target)
	}
	return visibleRequest(tx, r)
}
func (a *Authority) Filter(ctx context.Context, r domain.Request, response domain.Response) (domain.Response, error) {
	if err := a.Authorize(ctx, r); err != nil {
		return domain.Response{}, err
	}
	// Visibility comes from the persisted semantic target, independently of which
	// fields the saved response happens to include. Current explicit result IDs
	// are checked as well, including responses from linked domain operations.
	err := a.Service.Store.Write(ctx, func(tx app.WriteTx) error {
		var record domain.Session
		if !a.Bootstrap {
			var err error
			record, err = a.Service.authenticateTx(tx, a.Token, false)
			if err != nil {
				return err
			}
			if err := authorizeScope(tx, record, r); err != nil {
				return err
			}
			if ok, e := providerReconcileOnly(tx, record, r); e != nil {
				return e
			} else if ok {
				var job domain.Job
				if e = json.Unmarshal(response.Body, &job); e != nil {
					return e
				}
				current, e := tx.Job(job.ID)
				if e != nil {
					return e
				}
				projected, e := projectProviderJob(tx, record, current)
				if e != nil {
					return e
				}
				// The receipt keeps its operation-time snapshot and opaque validator.
				// Current authority may mask it further, never restore hidden fields.
				job = redactProviderInvocation(job)
				if projected.ProjectionETag != "" {
					job.Scope = json.RawMessage(`{}`)
					job.Result = nil
					job.Permissions = nil
					job.ParentID = ""
					job.UploadID = ""
				}
				if response.Headers["ETag"] == "" {
					return domain.ErrInvalidTarget
				}
				response.Body, _ = json.Marshal(job)
				return nil
			}
		}
		if LifecycleRoute(r.Target) {
			var err error
			response, err = filterLifecycleResponse(tx, r, response)
			return err
		}
		if err := visibleContextTarget(tx, r.Target, true); err != nil {
			return err
		}
		if err := visibleRequest(tx, r); err != nil {
			return err
		}
		var data struct {
			DocumentID string          `json:"document_id"`
			VersionID  string          `json:"version_id"`
			JobID      string          `json:"job_id"`
			Data       json.RawMessage `json:"data"`
		}
		if json.Unmarshal(response.Body, &data) != nil {
			return nil
		}
		if len(data.Data) > 0 {
			_ = json.Unmarshal(data.Data, &data)
		}
		if data.JobID != "" {
			job, err := tx.Job(data.JobID)
			if err != nil {
				return err
			}
			if !a.Bootstrap {
				if err := visibleScanJobForActor(tx, record, job); err != nil {
					return err
				}
			}
			if err := visibleJob(tx, job); err != nil {
				return err
			}
			var returned domain.Job
			if json.Unmarshal(response.Body, &returned) == nil && domain.ProviderJob(returned.Kind) {
				response.Body, _ = json.Marshal(redactProviderInvocation(returned))
				if response.Headers != nil && response.Headers["ETag"] != "" {
					response.Headers["ETag"] = JobETag(redactProviderInvocation(returned))
				}
			}
		}
		if data.VersionID != "" {
			if err := visibleTarget(tx, "/api/v1/versions/"+data.VersionID); err != nil {
				return err
			}
		}
		if data.DocumentID != "" {
			return visibleTarget(tx, "/api/v1/documents/"+data.DocumentID)
		}
		return nil
	})
	if err != nil {
		return domain.Response{}, err
	}
	return response, nil
}

var canonicalID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func visibleTarget(tx app.WriteTx, target string) error {
	return visibleTargetMasking(tx, target, true)
}

// visibleTargetWithoutLifecycleMask keeps exact identity, scope and unknown
// masking but lets an authorized writer observe the recorded lifecycle.
func visibleTargetWithoutLifecycleMask(tx app.WriteTx, target string) error {
	return visibleTargetMasking(tx, target, false)
}

func visibleTargetMasking(tx app.WriteTx, target string, mask bool) error {
	parsed, err := url.ParseRequestURI(target)
	if err != nil {
		return sec.ErrInvalid
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "v1" {
		return nil
	}
	var document string
	switch parts[2] {
	case "annotations":
		n, e := tx.Annotation(parts[3])
		if e != nil {
			return e
		}
		document = n.DocumentID
	case "documents":
		document = parts[3]
	case "versions":
		v, e := tx.Version(parts[3])
		if e != nil {
			return e
		}
		if !mask && v.Sequence == 0 {
			return domain.ErrNotFound
		}
		document = v.DocumentID
	case "projects":
		_, e := tx.Scope(parts[3])
		return e
	}
	if document != "" {
		d, e := tx.Document(document)
		if e != nil {
			return e
		}
		if mask && (d.Lifecycle == "trash" || d.Lifecycle == "purged") {
			return domain.ErrNotFound
		}
		_, e = tx.Scope(d.ProjectID)
		return e
	}
	return nil
}

func (s *Service) EventHead(ctx context.Context, token string) (string, int64, error) {
	var epoch string
	var head int64
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		if _, e := s.authenticateTx(tx, token, false); e != nil {
			return e
		}
		var e error
		epoch, head, e = tx.EventHead()
		return e
	})
	return epoch, head, err
}

// Refilter every notification immediately before its bounded socket write. A
// batch is a cursor convenience, not a grant lasting until the batch is drained.
func (s *Service) FilterEvent(ctx context.Context, token string, event domain.Event) (domain.Event, error) {
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		record, e := s.authenticateTx(tx, token, false)
		if e != nil {
			return e
		}
		epoch, _, e := tx.EventHead()
		if e != nil {
			return e
		}
		if event.StreamEpoch != epoch {
			return domain.ErrPrecondition
		}
		if event.ResourceKind == "question" {
			event, e = visibleQuestionEvent(tx, record, event)
			return e
		}
		if event.ResourceKind == "review" || event.ResourceKind == "annotation" || event.ResourceKind == "revisit" {
			if record.Scope.Kind != "owner" {
				event = domain.Event{StreamEpoch: event.StreamEpoch, Sequence: event.Sequence, ResourceKind: "library", Kind: "library.invalidated"}
				return nil
			}
			if e := visibleTarget(tx, personalEventTarget(event)); e != nil {
				if !errors.Is(e, domain.ErrNotFound) {
					return e
				}
				event = domain.Event{StreamEpoch: event.StreamEpoch, Sequence: event.Sequence, ResourceKind: "library", Kind: "library.invalidated"}
			}
			return nil
		}
		if event.Kind == "search.index_updated" || event.Kind == "references.updated" {
			var err error
			event, err = visibleIndexEvent(tx, record, event)
			return err
		}
		if record.Scope.Kind != "owner" {
			target := "/api/v1/" + event.ResourceKind + "s/" + event.ResourceID
			e := authorizeScope(tx, record, domain.Request{Method: "GET", Target: target})
			if errors.Is(e, domain.ErrDenied) || errors.Is(e, domain.ErrNotFound) {
				event = domain.Event{StreamEpoch: event.StreamEpoch, Sequence: event.Sequence, ResourceKind: "library", Kind: "library.invalidated"}
				return nil
			}
			if e != nil {
				return e
			}
		}
		target := ""
		if event.ResourceKind == "finding" {
			event = visibleFindingEvent(tx, record, event)
			return nil
		}
		if event.ResourceKind == "job" {
			job, err := tx.Job(event.ResourceID)
			if err == nil {
				err = visibleJob(tx, job)
			}
			if errors.Is(err, domain.ErrNotFound) {
				event.ResourceKind = "library"
				event.ResourceID = ""
				event.Kind = "library.invalidated"
				event.Body = nil
				event.Revision = 0
				return nil
			}
			if err != nil {
				return err
			}
		}
		if event.ResourceKind == "document" {
			target = "/api/v1/documents/" + event.ResourceID
		}
		if event.ResourceKind == "version" {
			target = "/api/v1/versions/" + event.ResourceID
		}
		if event.ResourceKind == "source" && event.Kind == "source.scope_changed" {
			// Scope changes name their project and have no version body.
			target = "/api/v1/projects/" + event.ResourceID
		} else if event.ResourceKind == "source" {
			// A source event names the observed version: it stays visible only
			// while that version does (lifecycle masking, W3d).
			var body struct {
				VersionID string `json:"version_id"`
			}
			if json.Unmarshal(event.Body, &body) != nil || body.VersionID == "" {
				event.ResourceKind = "library"
				event.ResourceID = ""
				event.Kind = "library.invalidated"
				event.Body = nil
				event.Revision = 0
				return nil
			}
			target = "/api/v1/versions/" + body.VersionID
		}
		if target != "" {
			e = visibleTarget(tx, target)
			if errors.Is(e, domain.ErrNotFound) {
				event.ResourceKind = "library"
				event.ResourceID = ""
				event.Kind = "library.invalidated"
				event.Body = nil
				event.Revision = 0
				return nil
			}
			return e
		}
		return nil
	})
	return event, err
}

var pairingValidator = regexp.MustCompile(`^"pairing-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}-r[1-9][0-9]*"$`)

// AuthenticateWrite supplies current bearer checks inside a caller-owned typed
// writer transaction without recursively acquiring the Store mutex.
func (s *Service) AuthenticateWrite(ctx context.Context, tx app.WriteTx, token string) (domain.Session, error) {
	return s.authenticateTx(tx, token, false)
}

// DeliveryAuthorityCurrent rechecks a remembered render-delivery authority
// against current state inside the caller's transaction: for an owner device
// session, current epoch/revocation/expiry of that exact session; for a
// scoped workflow credential, current revocation/epoch/expiry, active
// principal, current permission revisions, and the captured version.read
// action with the exact version/project anchors. Both kinds then require the
// version to remain published under a non-trash lifecycle. Definitive
// denials use the ordinary nondisclosing errors; anything else is transient.
func (s *Service) DeliveryAuthorityCurrent(ctx context.Context, tx app.WriteTx, kind, authorityID, principalID, versionID string) error {
	state, err := tx.AuthState()
	if err != nil {
		return err
	}
	scope := domain.CredentialScope{}
	switch kind {
	case "owner":
		record, err := tx.SessionByID(authorityID)
		if err != nil {
			return err
		}
		if record.PrincipalID != principalID || record.PrincipalID != state.OwnerID {
			return domain.ErrDenied
		}
		if _, err := sec.Check(record, state, s.now()); err != nil {
			return err
		}
	case "workflow":
		credential, err := tx.ScopedCredential(authorityID)
		if err != nil {
			return err
		}
		if credential.PrincipalID != principalID {
			return domain.ErrDenied
		}
		principal, err := tx.WorkflowPrincipal(credential.PrincipalID)
		if err != nil {
			return err
		}
		if credential.Revoked || !principal.Active || credential.AuthEpoch != state.AuthEpoch {
			return sec.ErrRevoked
		}
		if credential.ExpiresAt != nil && !s.now().Before(*credential.ExpiresAt) {
			return sec.ErrExpired
		}
		for id, revision := range credential.Permissions {
			current, err := tx.Scope(id)
			if err != nil {
				return err
			}
			if current.Project.PermissionsRevision != revision {
				return domain.ErrDenied
			}
		}
		scope = RestrictScope(credential.Scope, principal.Scope)
	default:
		return domain.ErrDenied
	}
	version, err := tx.PublishedVersion(versionID)
	if err != nil {
		return err
	}
	document, err := tx.Document(version.DocumentID)
	if err != nil {
		return err
	}
	if document.Lifecycle == "trash" || document.Lifecycle == "purged" {
		return domain.ErrNotFound
	}
	if kind == "workflow" {
		if !contains(scope.Actions, "version.read") || !contains(scope.VersionIDs, versionID) || !contains(scope.ProjectIDs, document.ProjectID) {
			return domain.ErrNotFound
		}
	}
	return nil
}

// personalWriteTarget identifies owner personal writes that report an exact
// lifecycle conflict. GET and saved-response filtering keep strict masking.
func personalWriteTarget(method, target string) bool {
	if method == "POST" && target == "/local/v1/copy-requests" {
		return true
	}
	if method == "POST" && LifecycleRoute(target) {
		return true
	}
	path := strings.SplitN(target, "?", 2)[0]
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "v1" || !canonicalID.MatchString(parts[3]) {
		return false
	}
	if len(parts) == 4 && parts[2] == "annotations" {
		return method == "PATCH" || method == "DELETE"
	}
	if len(parts) != 5 {
		return false
	}
	if parts[2] == "documents" {
		return method == "PUT" && parts[4] == "revisit"
	}
	if parts[2] != "versions" {
		return false
	}
	if method == "POST" && parts[4] == "annotations" {
		return true
	}
	return method == "PUT" && (parts[4] == "review" || parts[4] == "reading-position" || parts[4] == "page-position" || parts[4] == "reader-preference")
}

// Receipt authority follows the saved action target, including replay.
// visibleContextTarget enforces the saved context semantic target's CURRENT
// authority in the auth path before any receipt read, replay or filter
// response leaves the service: exact version/project scope and a present
// page binding always, with trash/purged hidden nondisclosing. The explicit
// lifecycle transition list in schema-registry covers the personal reader
// families, not new context creation.
func visibleContextTarget(tx app.WriteTx, target string, strict bool) error {
	parsed, err := url.ParseRequestURI(target)
	if err != nil {
		return sec.ErrInvalid
	}
	if parsed.Path != "/api/v1/contexts" {
		return nil
	}
	version := parsed.Query().Get("version_id")
	if !canonicalID.MatchString(version) {
		return sec.ErrInvalid
	}
	v, err := tx.Version(version)
	if err != nil {
		return err
	}
	d, err := tx.Document(v.DocumentID)
	if err != nil {
		return err
	}
	if strict && (d.Lifecycle == "trash" || d.Lifecycle == "purged") {
		return domain.ErrNotFound
	}
	if _, err := tx.Scope(d.ProjectID); err != nil {
		return err
	}
	if page := parsed.Query().Get("page_path"); page != "" {
		if _, err := tx.Page(version, page); err != nil {
			return err
		}
	}
	return nil
}

func visibleRequest(tx app.WriteTx, r domain.Request) error {
	if err := visibleContextTarget(tx, r.Target, true); err != nil {
		return err
	}

	if err := visibleTarget(tx, r.Target); err != nil {
		return err
	}
	parts := strings.Split(strings.Trim(strings.SplitN(r.Target, "?", 2)[0], "/"), "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "v1" {
		return nil
	}
	if parts[2] == "jobs" && len(parts) > 3 {
		j, err := tx.Job(parts[3])
		if err != nil {
			return err
		}
		return visibleJob(tx, j)
	}
	if parts[2] == "uploads" {
		if len(parts) > 3 {
			u, err := tx.Upload(parts[3])
			if err != nil {
				return err
			}
			return visibleUploadTarget(tx, u.Target)
		}
		var b struct {
			Target json.RawMessage `json:"target"`
		}
		if json.Unmarshal(r.Body, &b) != nil {
			return sec.ErrInvalid
		}
		return visibleUploadTarget(tx, b.Target)
	}
	if parts[2] == "imports" || parts[2] == "packages" {
		var b struct {
			Target json.RawMessage `json:"target"`
		}
		if json.Unmarshal(r.Body, &b) == nil && len(b.Target) > 0 {
			return visibleUploadTarget(tx, b.Target)
		}
	}
	return nil
}
func visibleUploadTarget(tx app.WriteTx, raw []byte) error { return app.AuthorizeTarget(tx, raw) }

func visibleJob(tx app.WriteTx, j domain.Job) error {
	if j.Kind == "backup" || j.Kind == "export" || j.Kind == "restore" {
		var scope struct {
			Principal string `json:"principal_id"`
			Set       string `json:"copy_set_id"`
		}
		if json.Unmarshal(j.Scope, &scope) != nil || scope.Principal == "" {
			return domain.ErrDenied
		}
		owner, e := tx.AuthState()
		if e != nil {
			return e
		}
		if scope.Principal != owner.OwnerID {
			return domain.ErrDenied
		}
		if j.Kind == "export" {
			set, e := tx.CopySet(scope.Set)
			if e != nil {
				return e
			}
			for project := range set.Permissions {
				if _, e = tx.Scope(project); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if j.Kind == "lifecycle" {
		i, err := tx.PurgeIntent(j.ID)
		if err != nil {
			return err
		}
		d, err := tx.Document(i.DocumentID)
		if err != nil {
			return err
		}
		_, err = tx.Scope(d.ProjectID)
		return err
	}
	if j.Kind == "scan" {
		var scanScope struct {
			PrincipalID string   `json:"principal_id"`
			ProjectIDs  []string `json:"project_ids"`
			DocumentID  string   `json:"document_id"`
			DocumentIDs []string `json:"document_ids"`
			VersionID   string   `json:"version_id"`
		}
		if json.Unmarshal(j.Scope, &scanScope) != nil || scanScope.PrincipalID == "" {
			return domain.ErrNotFound
		}
		for _, pid := range scanScope.ProjectIDs {
			if _, err := tx.Scope(pid); err != nil {
				return err
			}
		}
		if scanScope.VersionID != "" {
			if err := visibleTarget(tx, "/api/v1/versions/"+scanScope.VersionID); err != nil {
				return err
			}
		}
		singleDoc := scanScope.DocumentID
		if singleDoc == "" && len(scanScope.DocumentIDs) == 1 {
			singleDoc = scanScope.DocumentIDs[0]
		}
		if singleDoc != "" {
			if err := visibleTarget(tx, "/api/v1/documents/"+singleDoc); err != nil {
				return err
			}
		}
		return nil
	}
	if j.Kind == "maintenance" {
		var scope domain.ProviderScope
		if json.Unmarshal(j.Scope, &scope) != nil || scope.Invocation == nil || scope.Invocation.Maintenance == nil {
			return domain.ErrNotFound
		}
		for _, exp := range scope.Invocation.Maintenance.Explanations {
			if exp.ProjectID != "" {
				if _, err := tx.Scope(exp.ProjectID); err != nil {
					return err
				}
			}
			if exp.VersionID != "" {
				if err := visibleTarget(tx, "/api/v1/versions/"+exp.VersionID); err != nil {
					return err
				}
			}
		}
		if j.ParentID != "" {
			parent, err := tx.Job(j.ParentID)
			if err != nil {
				return err
			}
			if err := visibleJob(tx, parent); err != nil {
				return err
			}
		}
		return nil
	}

	var scope struct {
		Target json.RawMessage `json:"target"`
	}
	if json.Unmarshal(j.Scope, &scope) == nil && len(scope.Target) > 0 {
		if err := visibleUploadTarget(tx, scope.Target); err != nil {
			return err
		}
	}
	var result struct {
		DocumentID string `json:"document_id"`
		VersionID  string `json:"version_id"`
	}
	if json.Unmarshal(j.Result, &result) == nil {
		if result.DocumentID != "" {
			if err := visibleTarget(tx, "/api/v1/documents/"+result.DocumentID); err != nil {
				return err
			}
		}
		if result.VersionID != "" {
			return visibleTarget(tx, "/api/v1/versions/"+result.VersionID)
		}
	}
	return nil
}

// SearchAuthorityCurrent checks generations without re-decoding a large captured
// scope. A changed stamp or reached expiry requires the ordinary full check.
func (s *Service) SearchAuthorityCurrent(tx app.WriteTx, record domain.Session, stamp string) (bool, error) {
	current, err := tx.SearchAuthorityStamp(record)
	if err != nil {
		return false, err
	}
	if current != stamp {
		return false, nil
	}
	if record.ExpiresAt == nil || s.now().Before(*record.ExpiresAt) {
		return true, nil
	}
	// An ordinary HTTP read may extend owner idle expiry without changing any
	// authority. Re-evaluate the current session only when the captured expiry is
	// reached; a touch alone must never restart an otherwise valid search.
	if record.Scope.Kind == "owner" {
		state, e := tx.AuthState()
		if e != nil {
			return false, e
		}
		session, e := tx.SessionByID(record.ID)
		if e != nil {
			return false, e
		}
		_, e = sec.Check(session, state, s.now())
		return e == nil, e
	}
	return false, nil
}

func personalEventTarget(e domain.Event) string {
	switch e.ResourceKind {
	case "annotation":
		return "/api/v1/annotations/" + e.ResourceID
	case "revisit":
		return "/api/v1/documents/" + e.ResourceID
	default:
		return "/api/v1/versions/" + e.ResourceID
	}
}

// Saved Q&A events are personal invalidations. Recheck the current record's
// owner and version at BOTH batch replay and each final socket delivery.
func visibleQuestionEvent(tx app.WriteTx, actor domain.Session, event domain.Event) (domain.Event, error) {
	hidden := domain.Event{StreamEpoch: event.StreamEpoch, Sequence: event.Sequence, ResourceKind: "library", Kind: "library.invalidated"}
	if actor.Scope.Kind != "owner" {
		return hidden, nil
	}
	q, e := tx.Question(event.ResourceID)
	if errors.Is(e, domain.ErrNotFound) || errors.Is(e, domain.ErrDenied) {
		return hidden, nil
	}
	if e != nil {
		return event, e
	}
	if q.OwnerID != actor.PrincipalID {
		return hidden, nil
	}
	if e = visibleTarget(tx, "/api/v1/versions/"+q.VersionID); errors.Is(e, domain.ErrNotFound) || errors.Is(e, domain.ErrDenied) {
		return hidden, nil
	}
	if e != nil {
		return event, e
	}
	return event, nil
}
