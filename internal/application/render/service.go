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
	"fmt"
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

// Fixed local native companion UI origin shared by the companion binary and
// the local renderer handoff boundary.
const (
	CompanionUIPort   = "9271"
	CompanionUIOrigin = "http://127.0.0.1:" + CompanionUIPort
)

// Ingress describes the trusted management endpoint context that initiated a
// render control operation.
type Ingress struct {
	Remote       bool
	ParentOrigin string
}

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

// RemoteEndpointServer is implemented by renderer HTTP servers that support
// activating a validated private HTTPS renderer origin and its trusted
// management parent origin.
type RemoteEndpointServer interface {
	SetRemoteEndpoint(origin, parent string)
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
	ID              string
	cookieName      string
	cookieValue     string
	secretHash      string
	Binding         Binding
	deliveryOrigin  string
	parentOrigin    string
	upstreamOrigin  string
	remote          bool
	OwnerSession    string
	authorityKind   string
	PrincipalID     string
	Generation      string
	TrashGeneration int64
	LeaseExpires    time.Time
	closed          bool
	offer           *handoff
}

// snapshot is the immutable copy every cross-boundary read uses.
type snapshot struct {
	ID              string
	cookieName      string
	cookieValue     string
	secretHash      string
	binding         Binding
	deliveryOrigin  string
	parentOrigin    string
	upstreamOrigin  string
	remote          bool
	ownerSession    string
	authorityKind   string
	principalID     string
	generation      string
	trashGeneration int64
	leaseExpires    time.Time
	closed          bool
}

func (d *delivery) copy() snapshot {
	deliveryOrigin := d.deliveryOrigin
	if deliveryOrigin == "" {
		deliveryOrigin = d.Binding.Origin
	}
	return snapshot{
		ID:              d.ID,
		cookieName:      d.cookieName,
		cookieValue:     d.cookieValue,
		secretHash:      d.secretHash,
		binding:         d.Binding,
		deliveryOrigin:  deliveryOrigin,
		parentOrigin:    d.parentOrigin,
		upstreamOrigin:  d.upstreamOrigin,
		remote:          d.remote,
		ownerSession:    d.OwnerSession,
		authorityKind:   d.authorityKind,
		principalID:     d.PrincipalID,
		generation:      d.Generation,
		leaseExpires:    d.LeaseExpires,
		closed:          d.closed,
		trashGeneration: d.TrashGeneration,
	}
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

// ManagementOrigin returns the configured local management origin when known.
func (s *Service) ManagementOrigin() string {
	if s == nil || s.opts.ManagementOrigin == nil {
		return ""
	}
	return s.opts.ManagementOrigin()
}

type remoteOpenAuthority struct {
	base         app.Authority
	service      *Service
	versionID    string
	parentOrigin string
	wantOrigin   string
	wantUpstream string
}

func (a remoteOpenAuthority) Authorize(ctx context.Context, request domain.Request) error {
	return a.base.Authorize(ctx, request)
}

func (a remoteOpenAuthority) AuthorizeWrite(ctx context.Context, tx app.WriteTx, request domain.Request) error {
	if err := a.base.AuthorizeWrite(ctx, tx, request); err != nil {
		return err
	}
	_, err := a.service.verifyRemoteRouteTx(tx, a.versionID, a.parentOrigin, a.wantOrigin, a.wantUpstream)
	return err
}

func (a remoteOpenAuthority) Filter(ctx context.Context, request domain.Request, response domain.Response) (domain.Response, error) {
	out, err := a.base.Filter(ctx, request, response)
	if err != nil {
		return out, err
	}
	if err := a.service.store.Write(ctx, func(tx app.WriteTx) error {
		_, err := a.service.verifyRemoteRouteTx(tx, a.versionID, a.parentOrigin, a.wantOrigin, a.wantUpstream)
		return err
	}); err != nil {
		return domain.Response{}, err
	}
	return out, nil
}

func sameExternalOrigin(left, right string) bool {
	l, lErr := origins.CanonicalExternalOrigin(left)
	r, rErr := origins.CanonicalExternalOrigin(right)
	return lErr == nil && rErr == nil && l == r
}

func sameDeliveryOrigin(left, right string) bool {
	if left == right && left != "" {
		return true
	}
	return sameExternalOrigin(left, right)
}

func (s *Service) verifyRemoteRouteTx(tx app.WriteTx, versionID, parentOrigin, wantOrigin, wantUpstream string) (appconfig.RendererMapping, error) {
	if parentOrigin == "" {
		return appconfig.RendererMapping{}, &Error{Reason: ReasonRemoteIngress}
	}
	configuration, err := tx.Configuration()
	if err != nil {
		return appconfig.RendererMapping{}, err
	}
	value, err := appconfig.Decode(strings.NewReader(string(configuration.Value)), "json")
	if err != nil {
		return appconfig.RendererMapping{}, err
	}
	network, _ := value["network"].(map[string]any)
	if network == nil || network["remote_enabled"] != true {
		return appconfig.RendererMapping{}, &Error{Reason: ReasonRemoteIngress}
	}
	external, _ := network["external_base_url"].(string)
	if external == "" || !sameExternalOrigin(external, parentOrigin) {
		return appconfig.RendererMapping{}, &Error{Reason: ReasonRemoteIngress}
	}
	localOrigin := s.ManagementOrigin()
	if localOrigin == "" {
		if listener, ok := network["listener"].(map[string]any); ok {
			localOrigin = "http://" + fmt.Sprint(listener["bind"]) + ":" + fmt.Sprint(listener["port"])
		}
	}
	for _, mapping := range appconfig.RendererMappings(value) {
		if mapping.VersionID != versionID {
			continue
		}
		if appconfig.PrivateRendererMappingReason(parentOrigin, mapping, localOrigin) != "" {
			return appconfig.RendererMapping{}, &Error{Reason: ReasonRemoteIngress}
		}
		if wantOrigin != "" && !sameExternalOrigin(mapping.Origin, wantOrigin) {
			return appconfig.RendererMapping{}, &Error{Reason: ReasonRemoteIngress}
		}
		if wantUpstream != "" && mapping.Upstream != wantUpstream {
			return appconfig.RendererMapping{}, &Error{Reason: ReasonRemoteIngress}
		}
		return mapping, nil
	}
	return appconfig.RendererMapping{}, &Error{Reason: ReasonRemoteIngress}
}

// Open performs the durable K render-session open: it authorizes the exact
// version, checks the per-library ledger binding marker, reserves or reuses
// the sticky origin of the exact immutable tuple, binds the lazy loopback
// listener and returns the delivery session with a one-use handoff secret.
// A completed receipt replays its original response verbatim after restart:
// the old handoff is inactive and the host opens a new session under a new
// key once the original outcome is known; nothing here revives old state.
func (s *Service) Open(ctx context.Context, token, key, versionID string) (domain.Response, error) {
	return s.OpenWithIngress(ctx, token, key, versionID, Ingress{})
}

// OpenWithIngress performs a render-session open under the trusted ingress
// context supplied by product wiring.
func (s *Service) OpenWithIngress(ctx context.Context, token, key, versionID string, ingress Ingress) (domain.Response, error) {
	if !validOperationKey(key) {
		return domain.Response{}, sec.ErrInvalid
	}
	actor, err := s.auth.Authenticate(ctx, token)
	if err != nil {
		return domain.Response{}, err
	}
	preconditions := map[string]string{}
	authority := s.auth.Authority(token)
	if ingress.Remote {
		var preMapping appconfig.RendererMapping
		if err := s.store.Write(ctx, func(tx app.WriteTx) error {
			writer, err := s.auth.AuthenticateWrite(ctx, tx, token)
			if err != nil {
				return err
			}
			if err := s.auth.Authority(token).AuthorizeWrite(ctx, tx, domain.Request{
				Key:    domain.OperationKey{PrincipalID: writer.PrincipalID, OperationID: key},
				Method: "POST",
				Target: "/api/v1/versions/" + versionID + "/render-sessions",
			}); err != nil {
				return err
			}
			if _, err := visibleVersion(tx, versionID); err != nil {
				return err
			}
			var routeErr error
			preMapping, routeErr = s.verifyRemoteRouteTx(tx, versionID, ingress.ParentOrigin, "", "")
			return routeErr
		}); err != nil {
			return domain.Response{}, err
		}
		canonicalParent, err := origins.CanonicalExternalOrigin(ingress.ParentOrigin)
		if err != nil {
			return domain.Response{}, &Error{Reason: ReasonRemoteIngress}
		}
		canonicalDelivery, err := origins.CanonicalExternalOrigin(preMapping.Origin)
		if err != nil {
			return domain.Response{}, &Error{Reason: ReasonRemoteIngress}
		}
		preconditions = map[string]string{
			"ingress_mode":    "remote",
			"parent_origin":   canonicalParent,
			"delivery_origin": canonicalDelivery,
		}
		authority = remoteOpenAuthority{
			base:         s.auth.Authority(token),
			service:      s,
			versionID:    versionID,
			parentOrigin: canonicalParent,
			wantOrigin:   canonicalDelivery,
			wantUpstream: preMapping.Upstream,
		}
	}
	request := domain.Request{Key: domain.OperationKey{PrincipalID: actor.PrincipalID, OperationID: key}, Method: "POST", Target: "/api/v1/versions/" + versionID + "/render-sessions", Preconditions: preconditions}
	return app.Execute(ctx, s.store, authority, request, nil, func(tx app.WriteTx, receipt domain.Receipt) (domain.Response, error) {
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
		remoteOrigin := ""
		remoteParent := ""
		canonicalRemoteOrigin := ""
		canonicalRemoteParent := ""
		upstreamOrigin := ""
		if ingress.Remote {
			mapping, err := s.verifyRemoteRouteTx(tx, versionID, ingress.ParentOrigin, "", "")
			if err != nil {
				return domain.Response{}, err
			}
			canonicalRemoteOrigin, err = origins.CanonicalExternalOrigin(mapping.Origin)
			if err != nil {
				return domain.Response{}, &Error{Reason: ReasonRemoteIngress}
			}
			canonicalRemoteParent, err = origins.CanonicalExternalOrigin(ingress.ParentOrigin)
			if err != nil {
				return domain.Response{}, &Error{Reason: ReasonRemoteIngress}
			}
			upstreamURL, err := url.Parse(mapping.Upstream)
			if err != nil || upstreamURL.Port() == "" {
				return domain.Response{}, &Error{Reason: ReasonRemoteIngress}
			}
			upstreamPort, err := strconv.Atoi(upstreamURL.Port())
			if err != nil {
				return domain.Response{}, &Error{Reason: ReasonRemoteIngress}
			}
			otherExclude, err := otherUpstreamsExclude(configuration.Value, version.ID, managementPort)
			if err != nil {
				return domain.Response{}, err
			}
			exclude = otherExclude
			reservation.PreferredPort = upstreamPort
			reservation.ExternalOrigin = canonicalRemoteOrigin
			remoteOrigin = mapping.Origin
			remoteParent = ingress.ParentOrigin
			upstreamOrigin = mapping.Upstream
		}
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
		if err := s.ensureListener(binding, remoteOrigin, remoteParent); err != nil {
			// The reservation stays recorded and sticky; only the bind failed.
			return domain.Response{}, err
		}
		lifecycle, err := tx.LifecycleDocument(document.ID)
		if err != nil {
			return domain.Response{}, err
		}
		deliveryOrigin := origin
		parentOrigin := s.ManagementOrigin()
		if parentOrigin == "" {
			if network, ok := func() (map[string]any, bool) {
				v, err := appconfig.Decode(strings.NewReader(string(configuration.Value)), "json")
				if err != nil {
					return nil, false
				}
				n, ok := v["network"].(map[string]any)
				return n, ok
			}(); ok {
				if listener, ok := network["listener"].(map[string]any); ok {
					parentOrigin = "http://" + fmt.Sprint(listener["bind"]) + ":" + fmt.Sprint(listener["port"])
				}
			}
		}
		if ingress.Remote {
			deliveryOrigin = canonicalRemoteOrigin
			parentOrigin = canonicalRemoteParent
		} else {
			upstreamOrigin = origin
		}
		view, err := s.newDelivery(writer, binding, deliveryOrigin, parentOrigin, upstreamOrigin, ingress.Remote, library.RuntimeGeneration, lifecycle.TrashGeneration)
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
			"render_origin":    deliveryOrigin,
			"session_id":       view.ID,
			"handoff":          map[string]any{"url": deliveryOrigin + "/__haeryong/handoff", "secret": view.OfferValue, "expires_at": view.OfferExpires.Format(time.RFC3339Nano)},
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
	return s.RenewWithIngress(ctx, token, versionID, sessionID, Ingress{})
}

// RenewWithIngress extends an active session's lease under the caller's
// trusted ingress context. Cross-ingress renewal is refused without mutating
// or terminating the existing session.
func (s *Service) RenewWithIngress(ctx context.Context, token, versionID, sessionID string, ingress Ingress) (domain.Response, error) {
	current, err := s.controlKnownSession(ctx, token, versionID, sessionID)
	if err != nil {
		return domain.Response{}, err
	}
	if current.closed {
		return domain.Response{}, &Error{Reason: ReasonClosedSession}
	}
	if current.remote != ingress.Remote || (ingress.Remote && !sameExternalOrigin(current.parentOrigin, ingress.ParentOrigin)) {
		return domain.Response{}, &Error{Reason: ReasonDeniedDelivery}
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

// OwnsSession reports whether the given principal owns a known in-memory
// session for the version.
func (s *Service) OwnsSession(principalID, versionID, sessionID string) bool {
	if s == nil {
		return false
	}
	current := s.snapshot(sessionID)
	return current.ID != "" && current.principalID == principalID && current.binding.VersionID == versionID
}

// TerminateRemoteSession marks a known remote session for the version closed
// when its remote route is disabled or removed.
func (s *Service) TerminateRemoteSession(principalID, versionID, sessionID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if session := s.sessions[sessionID]; session != nil && session.remote && session.PrincipalID == principalID && session.Binding.VersionID == versionID {
		session.closed = true
		session.offer.used = true
	}
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

// otherUpstreamsExclude excludes the configured/runtime management port and
// every renderer_mappings upstream except the selected version's own mapping.
func otherUpstreamsExclude(raw []byte, versionID string, managementPort int) (func(int) bool, error) {
	value, err := appconfig.Decode(strings.NewReader(string(raw)), "json")
	if err != nil {
		return nil, err
	}
	excluded := map[int]bool{}
	if managementPort > 0 {
		excluded[managementPort] = true
	}
	network, _ := value["network"].(map[string]any)
	if network != nil {
		if listener, ok := network["listener"].(map[string]any); ok {
			if port, err := intValue(listener["port"]); err == nil {
				excluded[port] = true
			}
		}
	}
	for _, mapping := range appconfig.RendererMappings(value) {
		if mapping.VersionID == versionID {
			continue
		}
		if u, err := url.Parse(mapping.Upstream); err == nil && u.Port() != "" {
			if port, err := strconv.Atoi(u.Port()); err == nil {
				excluded[port] = true
			}
		}
	}
	return func(port int) bool { return excluded[port] }, nil
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
		current.binding.VersionID != versionID || !sameDeliveryOrigin(current.deliveryOrigin, origin) ||
		subtle.ConstantTimeCompare([]byte(hashOf(secret)), []byte(current.secretHash)) != 1 {
		return nil, &Error{Reason: ReasonDeniedDelivery}
	}
	switch s.authorizeCurrent(ctx, current) {
	case authorityOK:
		return &DeliveryView{ID: current.ID, VersionID: current.binding.VersionID, Origin: current.deliveryOrigin, OwnerSession: current.ownerSession, PrincipalID: current.principalID}, nil
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
// version's current lifecycle, anchors, and (for remote sessions) the
// active supported private renderer route. Definitive denials (revocation,
// expiry, scope loss, hidden lifecycle, disabled/changed remote mapping) are
// terminal.
func (s *Service) authorizeCurrent(ctx context.Context, current snapshot) authorityCheck {
	var check authorityCheck
	err := s.store.Write(ctx, func(tx app.WriteTx) error {
		err := s.auth.DeliveryAuthorityCurrent(ctx, tx, current.authorityKind, current.ownerSession, current.principalID, current.binding.VersionID)
		if err == nil {
			d, e := tx.LifecycleDocument(current.binding.DocumentID)
			err = e
			if err == nil && d.TrashGeneration != current.trashGeneration {
				err = domain.ErrNotFound
			}
		}
		if err == nil && current.remote {
			if _, routeErr := s.verifyRemoteRouteTx(tx, current.binding.VersionID, current.parentOrigin, current.deliveryOrigin, current.upstreamOrigin); routeErr != nil {
				var renderErr *Error
				if errors.As(routeErr, &renderErr) && renderErr.Reason == ReasonRemoteIngress {
					err = domain.ErrDenied
				} else {
					err = routeErr
				}
			}
		}
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
		deliveryOrigin := session.deliveryOrigin
		if deliveryOrigin == "" {
			deliveryOrigin = session.Binding.Origin
		}
		if session.Binding.VersionID != versionID || !sameDeliveryOrigin(deliveryOrigin, origin) {
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
	return &DeliveryView{ID: current.ID, VersionID: current.binding.VersionID, Origin: current.deliveryOrigin, OwnerSession: current.ownerSession, PrincipalID: current.principalID}, nil
}

func (s *Service) newDelivery(writer domain.Session, binding Binding, deliveryOrigin, parentOrigin, upstreamOrigin string, remote bool, generation string, trashGeneration int64) (openView, error) {
	id := randomUUID()
	// The stored hash is over the SAME cookie secret issued at handoff.
	cookieSecret, secretHash := randomToken()
	handoffSecret, handoffHash := randomToken()
	now := s.opts.Now()
	if deliveryOrigin == "" {
		deliveryOrigin = binding.Origin
	}
	session := &delivery{
		ID:              id,
		cookieName:      "hyr_view_" + binding.VersionID + "_" + id,
		cookieValue:     id + "." + cookieSecret,
		secretHash:      secretHash,
		Binding:         binding,
		deliveryOrigin:  deliveryOrigin,
		parentOrigin:    parentOrigin,
		upstreamOrigin:  upstreamOrigin,
		remote:          remote,
		OwnerSession:    writer.ID,
		authorityKind:   writer.Scope.Kind,
		PrincipalID:     writer.PrincipalID,
		Generation:      generation,
		TrashGeneration: trashGeneration,
		LeaseExpires:    now.Add(s.opts.Lease),
		offer:           &handoff{secretHash: handoffHash, value: handoffSecret, ExpiresAt: now.Add(s.opts.HandoffTTL)},
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
// before the durable reservation. When a remote origin is activated, the
// server's remote endpoint is configured even if a local listener already
// exists.
func (s *Service) ensureListener(binding Binding, remoteOrigin, remoteParent string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return &Error{Reason: ReasonClosedSession}
	}
	if _, ok := s.listeners[binding.Origin]; ok {
		if remoteOrigin != "" {
			server, hasServer := s.servers[binding.Origin]
			remoteServer, supported := server.(RemoteEndpointServer)
			if !hasServer || !supported {
				return &Error{Reason: ReasonRemoteIngress}
			}
			remoteServer.SetRemoteEndpoint(remoteOrigin, remoteParent)
		}
		return nil
	}
	if remoteOrigin != "" && s.opts.NewServer == nil {
		return &Error{Reason: ReasonRemoteIngress}
	}
	var server Server
	if s.opts.NewServer != nil {
		server = s.opts.NewServer(binding.Origin, binding)
		if remoteOrigin != "" {
			remoteServer, supported := server.(RemoteEndpointServer)
			if !supported {
				return &Error{Reason: ReasonRemoteIngress}
			}
			remoteServer.SetRemoteEndpoint(remoteOrigin, remoteParent)
		}
	}
	listener, err := net.Listen("tcp", strings.TrimPrefix(binding.Origin, "http://"))
	if err != nil {
		return &Error{Reason: ReasonBindConflict}
	}
	s.listeners[binding.Origin] = listener
	if server != nil {
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
		case origins.ReasonOriginConflict:
			return &Error{Reason: ReasonBindConflict}
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
