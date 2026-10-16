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
		return record, sec.ErrRequired
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
func (s *Service) Authenticate(ctx context.Context, token string) (domain.Session, error) {
	return s.checkSession(ctx, token, true)
}
func (s *Service) checkSession(ctx context.Context, token string, touch bool) (domain.Session, error) {
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
			if _, e := s.authenticateTx(tx, token, true); e != nil {
				return e
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
		if _, e := s.authenticateTx(tx, token, true); e != nil {
			return e
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
	if _, err := s.Authenticate(ctx, token); err != nil {
		return domain.Health{}, err
	}
	return s.Store.Health(ctx)
}
func (s *Service) Operation(ctx context.Context, token, id string) (domain.Receipt, error) {
	var out domain.Receipt
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		a, e := s.authenticateTx(tx, token, true)
		if e != nil {
			return e
		}
		out, e = tx.Receipt(domain.OperationKey{PrincipalID: a.PrincipalID, OperationID: id})
		return e
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
		if _, e := s.authenticateTx(tx, token, true); e != nil {
			return e
		}
		var e error
		out, e = tx.Job(id)
		return e
	})
	return out, err
}
func (s *Service) Jobs(ctx context.Context, token string) ([]domain.Job, error) {
	var out []domain.Job
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		if _, e := s.authenticateTx(tx, token, true); e != nil {
			return e
		}
		var e error
		out, e = tx.Jobs()
		return e
	})
	return out, err
}
func (s *Service) Events(ctx context.Context, token, epoch string, after int64) ([]domain.Event, error) {
	var out []domain.Event
	err := s.Store.Write(ctx, func(tx app.WriteTx) error {
		if _, e := s.authenticateTx(tx, token, false); e != nil {
			return e
		}
		var e error
		out, e = tx.Events(epoch, after, 64)
		if e != nil {
			return e
		}
		for i, event := range out {
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
		j, err := tx.CancelJob(id)
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
	return visibleTarget(tx, r.Target)
}
func (a *Authority) Filter(ctx context.Context, r domain.Request, response domain.Response) (domain.Response, error) {
	if err := a.Authorize(ctx, r); err != nil {
		return domain.Response{}, err
	}
	// Visibility comes from the persisted semantic target, independently of which
	// fields the saved response happens to include. Current explicit result IDs
	// are checked as well, including responses from linked domain operations.
	err := a.Service.Store.Write(ctx, func(tx app.WriteTx) error {
		if !a.Bootstrap {
			if _, err := a.Service.authenticateTx(tx, a.Token, false); err != nil {
				return err
			}
		}
		if err := visibleTarget(tx, r.Target); err != nil {
			return err
		}
		var data struct {
			DocumentID string          `json:"document_id"`
			VersionID  string          `json:"version_id"`
			Data       json.RawMessage `json:"data"`
		}
		if json.Unmarshal(response.Body, &data) != nil {
			return nil
		}
		if len(data.Data) > 0 {
			_ = json.Unmarshal(data.Data, &data)
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
	case "documents":
		document = parts[3]
	case "versions":
		v, e := tx.Version(parts[3])
		if e != nil {
			return e
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
		if d.Lifecycle == "trash" || d.Lifecycle == "purged" {
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
		if _, e := s.authenticateTx(tx, token, true); e != nil {
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
		if _, e := s.authenticateTx(tx, token, false); e != nil {
			return e
		}
		epoch, _, e := tx.EventHead()
		if e != nil {
			return e
		}
		if event.StreamEpoch != epoch {
			return domain.ErrPrecondition
		}
		target := ""
		if event.ResourceKind == "document" {
			target = "/api/v1/documents/" + event.ResourceID
		}
		if event.ResourceKind == "version" {
			target = "/api/v1/versions/" + event.ResourceID
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
