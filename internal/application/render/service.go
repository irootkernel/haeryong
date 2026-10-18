// Package render owns renderer-origin delivery sessions, the lazily bound
// loopback listeners behind origins reserved through the per-user origins
// ledger, and verified version-content serving for those origins. It has no
// HTTP surface of its own: trusted-host routes live in adapters/http/v1 and
// the per-origin server is supplied by product wiring through Options.
//
// Locking discipline: render.mu guards only the in-memory session and
// listener maps and is never held across a store transaction; store
// transactions never call back into the mutex from another goroutine's
// renewal path. All cross-boundary reads use immutable snapshots.
//
// Current-authority discipline: every protected delivery read and the
// handoff recheck the remembered authority (owner session or scoped
// credential) plus the exact version's current lifecycle/anchors through
// auth.DeliveryAuthorityCurrent. Any definitive denial terminates the
// session permanently: an expired lease is not an explicit close, and a
// restored document never resurrects a delivery a lifecycle event ended.
package render

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	appauth "haeryong.local/haeryong/internal/application/auth"
	appconfig "haeryong.local/haeryong/internal/application/configuration"
	"haeryong.local/haeryong/internal/application/origins"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

// RendererVersion identifies the loader derivative family: a derivative is
// keyed by immutable content digest, exact page path and this version.
const RendererVersion = "haeryong-loader-v3"

// Binding is the immutable version identity one renderer origin serves.
type Binding struct {
	LibraryID     string
	VersionID     string
	DocumentID    string
	ContentSetID  string
	PayloadSHA256 string
	Origin        string
}

// Server serves one renderer origin; product wiring supplies the HTTP
// implementation so this package stays free of net/http.
type Server interface {
	Serve(net.Listener) error
}

const (
	defaultLease      = 30 * time.Minute
	defaultHandoffTTL = 2 * time.Minute
)

// Options carries the internal constructor dependencies. AppStateDir is the
// per-user Haeryong base for the origin ledger; empty resolves the fixed
// production account base. NewServer builds the per-origin server.
type Options struct {
	AppStateDir      string
	NewServer        func(origin string, binding Binding) Server
	Now              func() time.Time
	Lease            time.Duration
	HandoffTTL       time.Duration
	ManagementOrigin func() string
}

// Typed renderer conditions surfaced as honest reasons, never 500s.
type Error struct{ Reason string }

func (e *Error) Error() string { return "renderer: " + e.Reason }

const (
	ReasonUnsupportedAccount = "renderer_unavailable_account"
	ReasonRemoteIngress      = "renderer_remote_ingress"
	ReasonBindingMismatch    = "renderer_binding_mismatch"
	ReasonBindConflict       = "renderer_bind_conflict"
	ReasonExhausted          = "renderer_pool_exhausted"
	ReasonLedgerClosed       = "renderer_ledger_closed"
	ReasonUnknownSession     = "render_session_not_found"
	ReasonClosedSession      = "render_session_closed"
	ReasonStaleSession       = "render_session_stale"
	ReasonDeniedDelivery     = "delivery_denied"
	ReasonTransientOwner     = "owner_check_retryable"
)

type handoff struct {
	secretHash string
	value      string
	ExpiresAt  time.Time
	used       bool
	consuming  bool
}

type delivery struct {
	ID            string
	cookieName    string
	cookieValue   string
	secretHash    string
	Binding       Binding
	OwnerSession  string
	authorityKind string
	PrincipalID   string
	Generation    string
	LeaseExpires  time.Time
	closed        bool
	offer         *handoff
}

// snapshot is the immutable copy every cross-boundary read uses.
type snapshot struct {
	ID            string
	cookieName    string
	cookieValue   string
	secretHash    string
	binding       Binding
	ownerSession  string
	authorityKind string
	principalID   string
	generation    string
	leaseExpires  time.Time
	closed        bool
}

func (d *delivery) copy() snapshot {
	return snapshot{ID: d.ID, cookieName: d.cookieName, cookieValue: d.cookieValue, secretHash: d.secretHash, binding: d.Binding, ownerSession: d.OwnerSession, authorityKind: d.authorityKind, principalID: d.PrincipalID, generation: d.Generation, leaseExpires: d.LeaseExpires, closed: d.closed}
}

// Service is lazily constructed by product wiring on the first render use so
// ordinary daemon operation never touches per-user origin state.
type Service struct {
	store app.Store
	auth  *appauth.Service
	bytes byteReader
	opts  Options

	ledger *origins.Manager

	mu           sync.Mutex
	sessions     map[string]*delivery
	listeners    map[string]net.Listener
	servers      map[string]Server
	stopped      bool
	shutdownDone chan struct{}
	shutdownErr  error
}

type byteReader interface {
	Read(context.Context, domain.Object, domain.Pin, func(io.ReadSeeker) error) error
}

func randomToken() (string, string) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	value := hex.EncodeToString(raw)
	digest := sha256.Sum256([]byte(value))
	return value, hex.EncodeToString(digest[:])
}

func hashOf(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// New constructs the renderer service. The ledger manager binds to the
// injected app-state base (focused checks) or the fixed production account
// base; nothing is created until the first render-session open.
func New(store app.Store, auth *appauth.Service, bytes byteReader, opts Options) (*Service, error) {
	base := opts.AppStateDir
	if base == "" {
		resolved, err := origins.ProdBase(origins.AccountHome)
		if err != nil {
			return nil, &Error{Reason: ReasonUnsupportedAccount}
		}
		base = resolved
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Lease <= 0 {
		opts.Lease = defaultLease
	}
	if opts.HandoffTTL <= 0 {
		opts.HandoffTTL = defaultHandoffTTL
	}
	return &Service{store: store, auth: auth, bytes: bytes, opts: opts, ledger: origins.NewManager(base, origins.Faults{}), sessions: map[string]*delivery{}, listeners: map[string]net.Listener{}, servers: map[string]Server{}}, nil
}

// Open performs the durable K render-session open: it authorizes the exact
// version, checks the per-library ledger binding marker, reserves or reuses
// the sticky origin of the exact immutable tuple, binds the lazy loopback
// listener and returns the delivery session with a one-use handoff secret.
// A completed receipt replays its original response verbatim after restart:
// the old handoff is inactive and the host opens a new session under a new
// key once the original outcome is known; nothing here revives old state.
func (s *Service) Open(ctx context.Context, token, key, versionID string) (domain.Response, error) {
	if !validOperationKey(key) {
		return domain.Response{}, sec.ErrInvalid
	}
	actor, err := s.auth.Authenticate(ctx, token)
	if err != nil {
		return domain.Response{}, err
	}
	request := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: "POST", Target: "/api/v1/versions/" + versionID + "/render-sessions", Preconditions: map[string]string{}}
	return app.Execute(ctx, s.store, s.auth.Authority(token), request, nil, func(tx app.WriteTx, receipt domain.Receipt) (domain.Response, error) {
		writer, err := s.auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return domain.Response{}, err
		}
		version, err := tx.PublishedVersion(versionID)
		if err != nil {
			return domain.Response{}, err
		}
		document, err := tx.Document(version.DocumentID)
		if err != nil {
			return domain.Response{}, err
		}
		if document.Lifecycle == "trash" || document.Lifecycle == "purged" {
			return domain.Response{}, domain.ErrNotFound
		}
		library, err := tx.Library()
		if err != nil {
			return domain.Response{}, err
		}
		// Configuration is read through the same transaction: calling the
		// store's read API here would take the store lock the transaction
		// already holds.
		configuration, err := tx.Configuration()
		if err != nil {
			return domain.Response{}, err
		}
		pool, exclude, err := currentPool(configuration.Value)
		if err != nil {
			return domain.Response{}, err
		}
		configuredExclude := exclude
		managementPort := 0
		if s.opts.ManagementOrigin != nil {
			endpoint, err := url.Parse(s.opts.ManagementOrigin())
			if err != nil || endpoint.Port() == "" {
				return domain.Response{}, &Error{Reason: ReasonLedgerClosed}
			}
			managementPort, err = strconv.Atoi(endpoint.Port())
			if err != nil {
				return domain.Response{}, err
			}
		}
		exclude = func(port int) bool { return port == managementPort || configuredExclude(port) }
		reservation := origins.Reservation{LibraryID: library.ID, VersionID: version.ID, ContentSetID: version.ContentSetID, PayloadSHA256: version.PayloadSHA}
		bound := true
		marker, markerErr := tx.ResourceRow("origin_ledger_binding", library.ID)
		if markerErr != nil {
			if !errors.Is(markerErr, domain.ErrNotFound) {
				return domain.Response{}, markerErr
			}
			bound = false
		} else if err := s.ledger.BindingMatch(marker.Value); err != nil {
			// A remembered ledger that is no longer the opened one fails the
			// renderer closed for this library; ordinary flows continue.
			return domain.Response{}, &Error{Reason: ReasonBindingMismatch}
		}
		record, err := s.ledger.Reserve(reservation, pool, exclude)
		if err != nil {
			return domain.Response{}, mapLedgerError(err)
		}
		origin := origins.Origin(record.Port)
		binding := Binding{LibraryID: library.ID, VersionID: version.ID, DocumentID: version.DocumentID, ContentSetID: version.ContentSetID, PayloadSHA256: version.PayloadSHA, Origin: origin}
		if err := s.ensureListener(binding); err != nil {
			// The reservation stays recorded and sticky; only the bind failed.
			return domain.Response{}, err
		}
		view, err := s.newDelivery(writer, binding, library.RuntimeGeneration)
		if err != nil {
			return domain.Response{}, err
		}
		if !bound {
			identity, err := s.ledger.Identity()
			if err != nil {
				return domain.Response{}, mapLedgerError(err)
			}
			value, err := json.Marshal(origins.Identity{LedgerID: identity.LedgerID, Path: identity.Path})
			if err != nil {
				return domain.Response{}, err
			}
			if _, err := tx.MutateResource(domain.Resource{Kind: "origin_ledger_binding", ID: library.ID, Value: value}, 0, "origin.bound"); err != nil {
				return domain.Response{}, err
			}
		}
		body, err := json.Marshal(map[string]any{
			"version_id":       versionID,
			"render_origin":    origin,
			"session_id":       view.ID,
			"handoff":          map[string]any{"url": origin + "/__haeryong/handoff", "secret": view.OfferValue, "expires_at": view.OfferExpires.Format(time.RFC3339Nano)},
			"lease_expires_at": view.LeaseExpires.Format(time.RFC3339Nano),
		})
		if err != nil {
			return domain.Response{}, err
		}
		return domain.Response{Status: 201, Body: body}, nil
	})
}

// openView is the immutable result of creating a delivery session.
type openView struct {
	ID           string
	LeaseExpires time.Time
	OfferValue   string
	OfferExpires time.Time
}

// Renew extends an active session's lease. It requires the caller to own the
// exact known session, the current version to remain visible (a restore
// after a lifecycle denial never resurrects a terminated delivery), and the
// session's captured library generation to still be current.
func (s *Service) Renew(ctx context.Context, token, versionID, sessionID string) (domain.Response, error) {
	current, err := s.controlKnownSession(ctx, token, versionID, sessionID)
	if err != nil {
		return domain.Response{}, err
	}
	if current.closed {
		return domain.Response{}, &Error{Reason: ReasonClosedSession}
	}
	switch s.authorizeCurrent(ctx, current) {
	case authorityOK:
	case authorityDefinite:
		s.terminate(sessionID)
		return domain.Response{}, &Error{Reason: ReasonDeniedDelivery}
	default:
		return domain.Response{}, &Error{Reason: ReasonTransientOwner}
	}
	if err := s.checkGeneration(ctx, current.generation); err != nil {
		return domain.Response{}, err
	}
	s.mu.Lock()
	session := s.sessions[sessionID]
	if session == nil || session.closed || session.Generation != current.generation {
		closed := session != nil && session.closed
		s.mu.Unlock()
		if closed {
			return domain.Response{}, &Error{Reason: ReasonClosedSession}
		}
		return domain.Response{}, &Error{Reason: ReasonUnknownSession}
	}
	session.LeaseExpires = s.opts.Now().Add(s.opts.Lease)
	s.mu.Unlock()
	body, err := json.Marshal(map[string]string{"session_id": sessionID, "state": "active"})
	if err != nil {
		return domain.Response{}, err
	}
	return domain.Response{Status: 200, Body: body}, nil
}

// Close terminates a delivery session. Closing a known owned session stays
// possible after any lifecycle change (the host must be able to end delivery
// when a document becomes trash) and is idempotent; a session id unknown to
// this process (for example after restart, when delivery sessions are
// ephemeral) is reported absent without disclosure. The origin reservation
// is never released, and an explicitly closed selection is never reopened by
// renewal or recovery.
func (s *Service) Close(ctx context.Context, token, versionID, sessionID string) (domain.Response, error) {
	if _, err := s.controlKnownSession(ctx, token, versionID, sessionID); err != nil {
		return domain.Response{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[sessionID]
	if session == nil {
		return domain.Response{}, &Error{Reason: ReasonUnknownSession}
	}
	session.closed = true
	session.offer.used = true
	return domain.Response{Status: 204}, nil
}

// controlKnownSession authenticates the caller and verifies ownership of the
// exact known session without any document-lifecycle masking: closing and
// renewing a session the caller owns must not depend on the target still
// being visible. Unknown ids are nondisclosing.
func (s *Service) controlKnownSession(ctx context.Context, token, versionID, sessionID string) (snapshot, error) {
	var current snapshot
	err := s.store.Write(ctx, func(tx app.WriteTx) error {
		writer, err := s.auth.AuthenticateWrite(ctx, tx, token)
		if err != nil {
			return err
		}
		current = s.snapshot(sessionID)
		if current.ID == "" {
			return &Error{Reason: ReasonUnknownSession}
		}
		if current.principalID != writer.PrincipalID || current.binding.VersionID != versionID {
			return &Error{Reason: ReasonUnknownSession}
		}
		return nil
	})
	if err != nil {
		return snapshot{}, err
	}
	return current, nil
}

func (s *Service) snapshot(id string) snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session := s.sessions[id]; session != nil {
		return session.copy()
	}
	return snapshot{}
}

// terminate marks a session permanently closed after a definitive authority
// denial; a lease expiring is not an explicit close, but revocation, scope
// loss or a lifecycle denial ends the delivery session entirely.
func (s *Service) terminate(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session := s.sessions[id]; session != nil {
		session.closed = true
		session.offer.used = true
	}
}

// checkGeneration compares a session's captured library generation with the
// current one without holding render.mu across the store transaction.
func (s *Service) checkGeneration(ctx context.Context, generation string) error {
	return s.store.Write(ctx, func(tx app.WriteTx) error {
		library, err := tx.Library()
		if err != nil {
			return err
		}
		if library.RuntimeGeneration != generation {
			return &Error{Reason: ReasonStaleSession}
		}
		return nil
	})
}

// currentPool derives the effective renderer pool and excluded ports (the
// management listener and renderer_mappings upstreams) from a configuration
// value captured inside the caller's transaction.
func currentPool(raw []byte) (origins.Pool, func(int) bool, error) {
	value, err := appconfig.Decode(strings.NewReader(string(raw)), "json")
	if err != nil {
		return origins.Pool{}, nil, err
	}
	if err := appconfig.Validate(value); err != nil {
		return origins.Pool{}, nil, err
	}
	base, count := appconfig.RendererPool(value)
	excluded := map[int]bool{}
	network := value["network"].(map[string]any)
	listener := network["listener"].(map[string]any)
	if port, err := intValue(listener["port"]); err == nil {
		excluded[port] = true
	}
	if mappings, ok := network["renderer_mappings"].([]any); ok {
		for _, entry := range mappings {
			upstream, ok := entry.(map[string]any)["upstream"].(string)
			if !ok {
				continue
			}
			if host, portText, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(upstream, "https://"), "http://")); err == nil && host != "" {
				if port, err := strconv.Atoi(portText); err == nil {
					excluded[port] = true
				}
			}
		}
	}
	return origins.Pool{BasePort: base, Count: count}, func(port int) bool { return excluded[port] }, nil
}

// DeliveryView is the validated delivery session used by the renderer
// origin server for one request.
type DeliveryView struct {
	ID           string
	VersionID    string
	Origin       string
	OwnerSession string
	PrincipalID  string
}

// Cookie exposes the per-delivery narrow cookie of a session for the one
// handoff consumption that establishes it.
func (s *Service) Cookie(sessionID string) (name, value string, ok bool) {
	current := s.snapshot(sessionID)
	if current.ID == "" {
		return "", "", false
	}
	return current.cookieName, current.cookieValue, true
}

// AuthorizeDelivery validates a renderer-origin request's narrow cookie and
// the CURRENT delivery authority behind the session. Definitive denial ends
// the session and clears the cookie; transient failures are retryable.
func (s *Service) AuthorizeDelivery(ctx context.Context, versionID, origin, deliveryID, secret string) (*DeliveryView, error) {
	current := s.snapshot(deliveryID)
	if current.ID == "" || current.closed || s.opts.Now().After(current.leaseExpires) ||
		current.binding.VersionID != versionID || current.binding.Origin != origin ||
		subtle.ConstantTimeCompare([]byte(hashOf(secret)), []byte(current.secretHash)) != 1 {
		return nil, &Error{Reason: ReasonDeniedDelivery}
	}
	switch s.authorizeCurrent(ctx, current) {
	case authorityOK:
		return &DeliveryView{ID: current.ID, VersionID: current.binding.VersionID, Origin: current.binding.Origin, OwnerSession: current.ownerSession, PrincipalID: current.principalID}, nil
	case authorityDefinite:
		s.terminate(deliveryID)
		return nil, &Error{Reason: ReasonDeniedDelivery}
	default:
		return nil, &Error{Reason: ReasonTransientOwner}
	}
}

// GuardDelivery rechecks a delivery session's lease, its exact version and
// the current authority for per-write use during protected streaming.
func (s *Service) GuardDelivery(ctx context.Context, versionID, deliveryID string) error {
	current := s.snapshot(deliveryID)
	if current.ID == "" || current.closed || s.opts.Now().After(current.leaseExpires) || current.binding.VersionID != versionID {
		return &Error{Reason: ReasonDeniedDelivery}
	}
	switch s.authorizeCurrent(ctx, current) {
	case authorityOK:
		// Bounded reads and writes are progress. Idle sessions still expire;
		// an active transfer does not require a separate host renewal timer.
		s.mu.Lock()
		defer s.mu.Unlock()
		session := s.sessions[deliveryID]
		if session == nil || session.closed || s.stopped {
			return &Error{Reason: ReasonDeniedDelivery}
		}
		session.LeaseExpires = s.opts.Now().Add(s.opts.Lease)
		return nil
	case authorityDefinite:
		s.terminate(deliveryID)
		return &Error{Reason: ReasonDeniedDelivery}
	default:
		return &Error{Reason: ReasonTransientOwner}
	}
}

type authorityCheck int

const (
	authorityOK authorityCheck = iota
	authorityDefinite
	authorityTransient
)

// authorizeCurrent applies the one shared current-delivery authorization:
// the remembered owner session or scoped credential plus the exact
// version's current lifecycle and anchors. Definitive denials (revocation,
// expiry, scope loss, hidden lifecycle) are terminal.
func (s *Service) authorizeCurrent(ctx context.Context, current snapshot) authorityCheck {
	var check authorityCheck
	err := s.store.Write(ctx, func(tx app.WriteTx) error {
		err := s.auth.DeliveryAuthorityCurrent(ctx, tx, current.authorityKind, current.ownerSession, current.principalID, current.binding.VersionID)
		if err == nil {
			check = authorityOK
			return nil
		}
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrDenied) || errors.Is(err, sec.ErrRevoked) || errors.Is(err, sec.ErrExpired) {
			check = authorityDefinite
			return nil
		}
		check = authorityTransient
		return nil
	})
	if err != nil {
		return authorityTransient
	}
	return check
}

// ConsumeHandoff atomically redeems the one-use handoff secret of a session
// and applies the full current delivery authority before any cookie is
// issued. A second use never re-establishes delivery; an expired secret is
// denied.
func (s *Service) ConsumeHandoff(ctx context.Context, versionID, origin, secret string) (*DeliveryView, error) {
	hash := hashOf(secret)
	var current snapshot
	s.mu.Lock()
	for _, session := range s.sessions {
		if session.Binding.VersionID != versionID || session.Binding.Origin != origin {
			continue
		}
		offer := session.offer
		if offer == nil || offer.used || subtle.ConstantTimeCompare([]byte(hash), []byte(offer.secretHash)) != 1 {
			continue
		}
		if offer.consuming {
			s.mu.Unlock()
			return nil, &Error{Reason: ReasonTransientOwner}
		}
		if s.opts.Now().After(offer.ExpiresAt) || session.closed {
			s.mu.Unlock()
			return nil, &Error{Reason: ReasonDeniedDelivery}
		}
		// Reserve the single consumer while current authority is checked.
		// Transient failure releases this claim without burning the secret.
		offer.consuming = true
		current = session.copy()
		break
	}
	s.mu.Unlock()
	if current.ID == "" {
		return nil, &Error{Reason: ReasonDeniedDelivery}
	}
	check := s.authorizeCurrent(ctx, current)
	s.mu.Lock()
	session := s.sessions[current.ID]
	if session == nil {
		s.mu.Unlock()
		return nil, &Error{Reason: ReasonDeniedDelivery}
	}
	session.offer.consuming = false
	if check == authorityTransient && !session.closed && !session.offer.used {
		s.mu.Unlock()
		return nil, &Error{Reason: ReasonTransientOwner}
	}
	if check != authorityOK || session.closed || session.offer.used || s.stopped || s.opts.Now().After(session.offer.ExpiresAt) {
		session.closed, session.offer.used = true, true
		s.mu.Unlock()
		return nil, &Error{Reason: ReasonDeniedDelivery}
	}
	session.offer.used = true
	s.mu.Unlock()
	return &DeliveryView{ID: current.ID, VersionID: current.binding.VersionID, Origin: current.binding.Origin, OwnerSession: current.ownerSession, PrincipalID: current.principalID}, nil
}

func (s *Service) newDelivery(writer domain.Session, binding Binding, generation string) (openView, error) {
	id := randomUUID()
	// The stored hash is over the SAME cookie secret issued at handoff.
	cookieSecret, secretHash := randomToken()
	handoffSecret, handoffHash := randomToken()
	now := s.opts.Now()
	session := &delivery{
		ID:            id,
		cookieName:    "hyr_view_" + binding.VersionID + "_" + id,
		cookieValue:   id + "." + cookieSecret,
		secretHash:    secretHash,
		Binding:       binding,
		OwnerSession:  writer.ID,
		authorityKind: writer.Scope.Kind,
		PrincipalID:   writer.PrincipalID,
		Generation:    generation,
		LeaseExpires:  now.Add(s.opts.Lease),
		offer:         &handoff{secretHash: handoffHash, value: handoffSecret, ExpiresAt: now.Add(s.opts.HandoffTTL)},
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return openView{}, &Error{Reason: ReasonClosedSession}
	}
	if existing, ok := s.sessions[id]; ok {
		return openView{ID: existing.ID, LeaseExpires: existing.LeaseExpires, OfferValue: existing.offer.value, OfferExpires: existing.offer.ExpiresAt}, nil
	}
	s.sessions[id] = session
	return openView{ID: session.ID, LeaseExpires: session.LeaseExpires, OfferValue: session.offer.value, OfferExpires: session.offer.ExpiresAt}, nil
}

// ensureListener lazily binds the loopback listener of a reserved origin.
// A bind collision fails visibly; the reservation stays sticky and a
// same-selection retry under current authority may re-attempt when the port
// is actually free. No listener, delivery session or handoff secret exists
// before the durable reservation.
func (s *Service) ensureListener(binding Binding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return &Error{Reason: ReasonClosedSession}
	}
	if _, ok := s.listeners[binding.Origin]; ok {
		return nil
	}
	listener, err := net.Listen("tcp", strings.TrimPrefix(binding.Origin, "http://"))
	if err != nil {
		return &Error{Reason: ReasonBindConflict}
	}
	s.listeners[binding.Origin] = listener
	if s.opts.NewServer != nil {
		server := s.opts.NewServer(binding.Origin, binding)
		s.servers[binding.Origin] = server
		go func() { _ = server.Serve(listener) }()
	}
	return nil
}

// Shutdown ends this daemon generation's delivery authority and listeners.
// Origin ownership remains durable. The composition root calls it on both
// process stop and controlled in-process restart before constructing a new
// facade, so an old listener cannot occupy the replacement's sticky port.
func (s *Service) Shutdown() error {
	s.mu.Lock()
	if s.shutdownDone != nil {
		done := s.shutdownDone
		s.mu.Unlock()
		<-done
		return s.shutdownErr
	}
	s.shutdownDone = make(chan struct{})
	s.stopped = true
	for _, session := range s.sessions {
		session.closed = true
		session.offer.used = true
	}
	listeners, servers := s.listeners, s.servers
	s.listeners = map[string]net.Listener{}
	s.servers = map[string]Server{}
	s.mu.Unlock()
	var result error
	for _, server := range servers {
		if closer, ok := server.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				result = errors.Join(result, err)
			}
		}
	}
	for _, listener := range listeners {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			result = errors.Join(result, err)
		}
	}
	s.mu.Lock()
	s.shutdownErr = result
	close(s.shutdownDone)
	s.mu.Unlock()
	return result
}

// BindingFor returns the binding served for a version, when a session
// exists.
func (s *Service) BindingFor(versionID string) (Binding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, session := range s.sessions {
		if session.Binding.VersionID == versionID {
			return session.Binding, true
		}
	}
	return Binding{}, false
}

func mapLedgerError(err error) error {
	var ledgerErr *origins.Error
	if errors.As(err, &ledgerErr) {
		switch ledgerErr.Reason {
		case origins.ReasonExhausted:
			return &Error{Reason: ReasonExhausted}
		case origins.ReasonBindingMismatch:
			return &Error{Reason: ReasonBindingMismatch}
		case origins.ReasonRetiredIdentity:
			return &Error{Reason: ReasonLedgerClosed}
		default:
			return &Error{Reason: ReasonLedgerClosed}
		}
	}
	return err
}

func validOperationKey(key string) bool {
	if len(key) != 36 {
		return false
	}
	for i, r := range key {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
				return false
			}
		}
	}
	return true
}

func randomUUID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return hex.EncodeToString(raw[:4]) + "-" + hex.EncodeToString(raw[4:6]) + "-" + hex.EncodeToString(raw[6:8]) + "-" + hex.EncodeToString(raw[8:10]) + "-" + hex.EncodeToString(raw[10:])
}

func intValue(v any) (int, error) {
	switch number := v.(type) {
	case interface{ String() string }:
		return strconv.Atoi(number.String())
	}
	return 0, errors.New("integer required")
}
