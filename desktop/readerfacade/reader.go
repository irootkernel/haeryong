// Package readerfacade is the single shared ReaderMode facade for the reused
// pinned beta28 harness and the future production desktop companion. It
// registers ONLY the typed ReaderService binding and rejects the legacy
// generic fixture bindings and message kinds: there is no arbitrary
// URL/path/method proxy. Every operation is a fixed production /api/v1 route
// built Go-side with the Go-held credential; JavaScript never receives a
// bearer. The separately pinned spike keeps only thin harness wiring
// (static origin, guard/bridge bootstrap, selfcheck page) around this
// package; harness-only diagnostics registration stays with the spike.
package readerfacade

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"haeryong.local/haeryong/desktop/onboarding"
	"haeryong.local/haeryong/internal/adapters/client"
)

// wireError is the closed error envelope the native wire expects.
type wireError struct {
	Status     int            `json:"status"`
	Code       string         `json:"code"`
	Retryable  bool           `json:"retryable"`
	RetryAfter float64        `json:"retry_after,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

func (e *wireError) Error() string {
	return fmt.Sprintf("native op failed: HTTP %d %s", e.Status, e.Code)
}

func networkError() *wireError { return &wireError{Status: 0, Code: "NETWORK_ERROR", Retryable: true} }

type readerStream struct {
	id     string
	body   io.ReadCloser
	cancel context.CancelFunc
	auth   authority
	// The exact version and the daemon stream epoch captured at open time;
	// both are reverified on every pull.
	versionID    string
	annotationID string
	epoch        string
}

type readerSubscription struct {
	id     string
	gen    uint64
	cursor string
}

type renderRef struct {
	VersionID string `json:"version_id"`
	SessionID string `json:"session_id"`
}

// outcome carries an HTTP status alongside the wire value so decision
// operations can map the canonical 202 reservation honestly.
type outcome struct {
	status int
	value  any
	werr   *wireError
}

type ReaderService struct {
	base, state string

	mu    sync.Mutex
	token string
	// The captured bearer of an explicit logout, kept ONLY for the original
	// revoke attempt and owned-cleanup closes; s.token is cleared at intent.
	logoutBearer       string
	forgetAttachmentID string
	attachmentID       string
	generation         uint64
	logoutSeen         bool
	// Local forget obligation: RAM is cleared at intent, the credential
	// FILE unlink is tracked separately and stays retryable until it truly
	// succeeds (or the file is proven already absent).
	forgetNeeded    bool
	forgetDone      bool
	forgetErr       string
	referenceReads  map[uint64]context.CancelFunc
	referenceReadID uint64
	streams         map[string]*readerStream
	subs            map[string]*readerSubscription
	renders         []renderRef
	diagnostics     ReceiptObserver
	client          *http.Client
	// M3 owner-selected filesystem handles (TASK-003-05): opaque,
	// purpose/attachment/generation-bound; never a path/URL proxy.
	sources map[string]*sourceHandle
	dests   map[string]*destHandle
	picker  *PickerPresenter
	// fsEpoch invalidates in-flight picker leases across owned cleanup and
	// activation transitions without touching session semantics: a picker
	// result that arrives after CloseOwned/activation cannot register.
	fsEpoch uint64
	// onboarding owns the Go-held pairing attempt, signed discovery, the
	// explicit start offer and the protected production credential. Nil (the
	// reused harness constructor) keeps pair/poll honestly unimplemented.
	onboarding *onboarding.Onboarding
	// logoutInvalidatedGen is the pairing attempt generation captured at the
	// logout intent: attempts at or before it can never activate afterwards.
	logoutInvalidatedGen uint64
	// testAfterRememberedLoad pauses the remembered-credential installation
	// between its load and its install lock hold. Same-package barrier tests
	// only; never set outside tests.
	testAfterRememberedLoad func()
}

// NewReaderService builds the shared typed facade over the exact trusted
// daemon origin with a Go-held bearer. The reused harness constructor keeps
// the historical receipt-file diagnostics (the six-stage native proof reads
// them as its evidence channel).
func NewReaderService(base, state, token string) *ReaderService {
	service := &ReaderService{
		base: base, state: state, token: token,
		streams: map[string]*readerStream{}, subs: map[string]*readerSubscription{},
		sources: map[string]*sourceHandle{}, dests: map[string]*destHandle{},
		// Browser parity: redirect:"error". The Go-held bearer is never
		// forwarded to any other listener through a redirect.
		client: &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return errRedirectRejected
		}},
	}
	service.installFileReceipts()
	return service
}

// NewReaderServiceWithOnboarding is the production companion constructor: the
// Go-held bearer may be empty (first boot — attach loads the remembered
// credential through the signed binding, or reports the typed
// NATIVE_NO_CREDENTIAL state) and the adopted credential's binding installs
// the base URL together with the bearer at activation. The onboarding
// instance carries the selection, credential namespace and product API pin.
// Production is DIAGNOSTIC-FREE: the receipt-file observer the harness
// constructor installs is removed here, so an unflagged production
// companion performs no diagnostic writes at all.
func NewReaderServiceWithOnboarding(base, state, token string, onboarding *onboarding.Onboarding) *ReaderService {
	service := NewReaderService(base, state, token)
	service.SetDiagnostics(nil)
	if onboarding != nil {
		service.onboarding = onboarding
	}
	return service
}

// ReceiptObserver receives one fully-built receipt entry (pid/event/at plus
// the event's rows). The facade invokes it while holding its own mutex, so
// an observer must never call back into the service. A nil observer means
// no diagnostic effect at all.
type ReceiptObserver func(entry map[string]any)

// SetDiagnostics installs (or, with nil, removes) the receipt observer. This
// is the Go-side configuration seam for harness and explicitly flagged test
// builds; production keeps the observer-free construction.
func (s *ReaderService) SetDiagnostics(observer ReceiptObserver) {
	s.mu.Lock()
	s.diagnostics = observer
	s.mu.Unlock()
}

// installFileReceipts installs the historical harness receipt persistence:
// the exact native-reader-receipts-<pid>.json array under the state
// directory, written after every entry like before.
func (s *ReaderService) installFileReceipts() {
	s.SetDiagnostics(FileReceiptObserver(s.state))
}

// FileReceiptObserver returns the historical harness receipt-file observer
// for explicitly flagged test builds of embedders (the companion test
// build). Production embedders never call it.
func FileReceiptObserver(stateDir string) ReceiptObserver {
	var (
		entriesMu sync.Mutex
		entries   []map[string]any
	)
	return func(entry map[string]any) {
		entriesMu.Lock()
		defer entriesMu.Unlock()
		entries = append(entries, entry)
		encoded, err := json.MarshalIndent(entries, "", " ")
		if err != nil {
			return
		}
		os.WriteFile(filepath.Join(stateDir, fmt.Sprintf("native-reader-receipts-%d.json", os.Getpid())), encoded, 0600)
	}
}

var errRedirectRejected = errors.New("native client rejects redirects")

func (s *ReaderService) receipt(event string, rows map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receiptLocked(event, rows)
}

// receiptLocked emits one receipt entry; the caller already holds s.mu.
func (s *ReaderService) receiptLocked(event string, rows map[string]any) {
	if s.diagnostics == nil {
		return
	}
	entry := map[string]any{"pid": os.Getpid(), "event": event, "at": time.Now().UTC()}
	for key, value := range rows {
		entry[key] = value
	}
	s.diagnostics(entry)
}

type authority struct {
	token string
	// endpoint travels WITH the bearer: an adoption activation installs a
	// new base and token together, and every request must use the exact pair
	// its caller captured — never a later mixed combination.
	endpoint     string
	attachmentID string
	gen          uint64
}

func (s *ReaderService) authority() (authority, *wireError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == "" {
		return authority{}, &wireError{Status: 401, Code: "NATIVE_SESSION_LOGGED_OUT", Retryable: false}
	}
	if s.attachmentID == "" {
		return authority{}, &wireError{Status: 401, Code: "NATIVE_SESSION_NOT_ATTACHED", Retryable: false}
	}
	return authority{token: s.token, endpoint: s.base, attachmentID: s.attachmentID, gen: s.generation}, nil
}

// snapshotAuthority captures the current token/base/generation under one lock
// hold for bootstrap and cleanup paths that do not require an attachment.
func (s *ReaderService) snapshotAuthority() authority {
	s.mu.Lock()
	defer s.mu.Unlock()
	return authority{token: s.token, endpoint: s.base, attachmentID: s.attachmentID, gen: s.generation}
}

func (s *ReaderService) currentGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

func (s *ReaderService) currentToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

// request performs one fixed production request. Routes are always built here.
func (s *ReaderService) request(method, path string, query url.Values, headers map[string]string, body []byte, auth authority) outcome {
	return s.requestContext(context.Background(), method, path, query, headers, body, auth)
}

func (s *ReaderService) requestContext(parent context.Context, method, path string, query url.Values, headers map[string]string, body []byte, auth authority) outcome {
	target := auth.endpoint + path
	if len(query) != 0 {
		target += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequest(method, target, reader)
	if err != nil {
		return outcome{werr: networkError()}
	}
	ctx, cancel := context.WithCancel(parent)
	request = request.WithContext(ctx)
	if auth.token != "" {
		request.Header.Set("Authorization", "Bearer "+auth.token)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	// The real daemon's strict decoder requires the JSON media type on every
	// JSON body; binary uploads set their own Content-Type explicitly.
	if body != nil && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.client.Do(request)
	if err != nil {
		cancel()
		return outcome{werr: networkError()}
	}
	defer func() { response.Body.Close(); cancel() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		// Headers arrived and the server may have committed the write; the
		// outcome is honestly unknown, never a definitive failure.
		return outcome{status: response.StatusCode, werr: uncertain(response.StatusCode)}
	}
	value, werr := typedResult(response.StatusCode, data)
	if werr != nil {
		// Retry-After is a real HTTP header, not an error-body field.
		if seconds, parseErr := strconv.ParseFloat(response.Header.Get("Retry-After"), 64); parseErr == nil && seconds >= 0 {
			werr.RetryAfter = seconds
		}
	}
	return outcome{status: response.StatusCode, value: value, werr: werr}
}

// uncertain is the closed wire signal for a successful status whose body
// could not be read or parsed; the native wire maps it to the same
// UncertainResponseError the browser transport raises, so the original K
// identity survives for receipt recovery.
func uncertain(status int) *wireError {
	return &wireError{Status: status, Code: "UNCERTAIN_RESPONSE", Retryable: true}
}

// typedResult classifies a daemon reply: 2xx bodies carry {data:...}; errors
// carry {error:{code,retryable,details}}; only 204 carries no body.
func typedResult(status int, data []byte) (any, *wireError) {
	if status >= 200 && status < 300 {
		if status == 204 {
			return nil, nil
		}
		if len(data) == 0 {
			// The browser transport fails to parse an empty 200/201 body and
			// keeps the original K recoverable; so must the native wire.
			return nil, uncertain(status)
		}
		var parsed struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(data, &parsed); err != nil {
			// A committed write with an unreadable body keeps its original K
			// recovery path (browser UncertainResponseError parity).
			return nil, uncertain(status)
		}
		if parsed.Data == nil {
			// Browser parity: a successful envelope without a data member is
			// a null success, not a transport failure.
			return nil, nil
		}
		var value any
		if err := json.Unmarshal(parsed.Data, &value); err != nil {
			return nil, uncertain(status)
		}
		return value, nil
	}
	code, retryable, details := "TRANSPORT_ERROR", true, map[string]any{}
	var parsed struct {
		Error struct {
			Code      string         `json:"code"`
			Retryable *bool          `json:"retryable"`
			Details   map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &parsed); err == nil {
		if parsed.Error.Code != "" {
			code = parsed.Error.Code
		}
		if parsed.Error.Retryable != nil {
			retryable = *parsed.Error.Retryable
		}
		if parsed.Error.Details != nil {
			details = parsed.Error.Details
		}
	}
	return nil, &wireError{Status: status, Code: code, Retryable: retryable, Details: details}
}

// forgetCredentialFile performs one honest local-forget attempt. Absence is
// already-forgotten; only a real removal or proven absence completes the
// obligation. A file holding a DIFFERENT credential is never unlinked (a late
// old retry cannot destroy a newer binding's credential). Callers hold mu;
// receipt() locks internally, so the receipt writes happen AFTER unlocking.
//
// Production (onboarding configured) forgets the protected selection-scoped
// client.Credential file: the stored bearer must match the forgotten one
// exactly before the unlink; a foreign or unreadable binding stays intact.
func (s *ReaderService) forgetCredentialFileLocked() {
	if s.onboarding != nil {
		outcome, err := s.onboarding.Forget(s.logoutBearer)
		switch {
		case err == nil && outcome == onboarding.ForgetForgotten:
			s.forgetDone = true
			s.forgetErr = ""
			s.receiptLocked("logoutLocalForget", map[string]any{"status": "forgotten"})
		case err == nil && outcome == onboarding.ForgetForeign:
			s.forgetDone = false
			s.forgetErr = "credential file now holds a different credential; left intact"
			s.receiptLocked("logoutLocalForget", map[string]any{"status": "foreign-credential-intact"})
		default:
			s.forgetDone = false
			if err != nil {
				s.forgetErr = err.Error()
			}
			s.receiptLocked("logoutLocalForget", map[string]any{"status": "local-forget-failed", "io_error": s.forgetErr})
		}
		return
	}
	path := filepath.Join(s.state, "native-session")
	raw, readErr := os.ReadFile(path)
	switch {
	case readErr == nil:
		if strings.TrimSpace(string(raw)) != s.logoutBearer {
			// The file no longer holds OUR forgotten credential; leave any
			// newer binding intact and record it truthfully.
			s.forgetDone = false
			s.forgetErr = "credential file now holds a different credential; left intact"
			s.receiptLocked("logoutLocalForget", map[string]any{"status": "foreign-credential-intact"})
			return
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			s.forgetDone = false
			s.forgetErr = err.Error()
			s.receiptLocked("logoutLocalForget", map[string]any{"status": "local-forget-failed", "io_error": err.Error()})
			return
		}
	case os.IsNotExist(readErr):
		// Already forgotten (an earlier attempt or pre-existing absence).
	default:
		s.forgetDone = false
		s.forgetErr = readErr.Error()
		s.receiptLocked("logoutLocalForget", map[string]any{"status": "local-forget-failed", "io_error": readErr.Error()})
		return
	}
	s.forgetDone = true
	s.forgetErr = ""
	s.receiptLocked("logoutLocalForget", map[string]any{"status": "forgotten"})
}

func randomID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	return hex.EncodeToString(raw)
}

func decodePayload(raw json.RawMessage, into any) *wireError {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID", Retryable: false}
	}
	return nil
}

// Reference and derived-index operations use this closed native request decoder.
func decodeReferencePayload(raw json.RawMessage, into any, required ...string) *wireError {
	invalid := &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID"}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return invalid
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return invalid
		}
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(into) != nil {
		return invalid
	}
	return nil
}

func (s *ReaderService) cancelReferenceReadsLocked() {
	for id, cancel := range s.referenceReads {
		cancel()
		delete(s.referenceReads, id)
	}
}

func (s *ReaderService) referenceRead(path string, params url.Values, auth authority) (any, *wireError) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.mu.Lock()
	if !s.contextCurrentLocked(auth) || s.attachmentID != auth.attachmentID {
		s.mu.Unlock()
		return nil, &wireError{Status: 409, Code: "NATIVE_SESSION_REPLACED"}
	}
	if s.referenceReads == nil {
		s.referenceReads = map[uint64]context.CancelFunc{}
	}
	s.referenceReadID++
	id := s.referenceReadID
	s.referenceReads[id] = cancel
	s.mu.Unlock()
	result := s.requestContext(ctx, "GET", path, params, nil, nil, auth)
	s.mu.Lock()
	delete(s.referenceReads, id)
	current := s.contextCurrentLocked(auth) && s.attachmentID == auth.attachmentID
	s.mu.Unlock()
	if !current {
		return nil, &wireError{Status: 409, Code: "NATIVE_SESSION_REPLACED"}
	}
	return result.value, result.werr
}

// Invoke dispatches one named typed operation. The switch is the whitelist.
func (s *ReaderService) Invoke(in struct {
	Operation string          `json:"Operation"`
	Payload   json.RawMessage `json:"Payload"`
}) (map[string]any, error) {
	value, werr := s.invoke(in.Operation, in.Payload)
	if werr != nil {
		return map[string]any{"ok": false, "error": werr}, nil
	}
	return map[string]any{"ok": true, "value": value}, nil
}

func (s *ReaderService) invoke(operation string, payload json.RawMessage) (any, *wireError) {
	switch operation {
	case "attachSession":
		return s.attach()
	case "disposeSession":
		var in struct {
			AttachmentID string `json:"attachment_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		werr := s.dispose(in.AttachmentID)
		if werr != nil {
			return nil, werr
		}
		return nil, nil
	case "pair", "pollPairing":
		// Without a configured onboarding instance (the reused harness
		// constructor) pairing stays honestly unimplemented.
		if s.onboarding == nil {
			return nil, &wireError{Status: 501, Code: "FEATURE_NOT_READY", Retryable: false}
		}
		if operation == "pair" {
			var in struct {
				DeviceLabel string `json:"device_label"`
			}
			if werr := decodePayload(payload, &in); werr != nil {
				return nil, werr
			}
			value, oerr := s.onboarding.CreatePairing(context.Background(), in.DeviceLabel)
			return value, onboardingWireError(oerr)
		}
		var in struct {
			PairingID string `json:"pairing_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		// The polling secret stays Go-side: JavaScript names only the
		// pairing it still owns.
		value, oerr := s.onboarding.PollPairing(context.Background(), in.PairingID)
		return value, onboardingWireError(oerr)
	case "serviceDiscovery":
		if s.onboarding == nil {
			return nil, &wireError{Status: 501, Code: "FEATURE_NOT_READY", Retryable: false}
		}
		return s.onboarding.Discover(context.Background()), nil
	case "startDaemonOffer":
		if s.onboarding == nil {
			return nil, &wireError{Status: 501, Code: "FEATURE_NOT_READY", Retryable: false}
		}
		value, oerr := s.onboarding.StartDaemonOffer(context.Background())
		if oerr != nil {
			return nil, onboardingWireError(oerr)
		}
		return value, nil
	case "adoptPairing":
		if s.onboarding == nil {
			return nil, &wireError{Status: 501, Code: "FEATURE_NOT_READY", Retryable: false}
		}
		var in struct {
			PairingID string `json:"pairing_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		value, oerr := s.onboarding.Adopt(context.Background(), in.PairingID, s.activateAdoptedCredential)
		if oerr != nil {
			return nil, onboardingWireError(oerr)
		}
		return value, nil
	case "abandonPairingAttempt":
		if s.onboarding == nil {
			return nil, &wireError{Status: 501, Code: "FEATURE_NOT_READY", Retryable: false}
		}
		var in struct {
			PairingID string `json:"pairing_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		value, oerr := s.onboarding.Abandon(in.PairingID)
		if oerr != nil {
			return nil, onboardingWireError(oerr)
		}
		return value, nil
	}

	auth, werr := s.authority()
	if werr != nil {
		return nil, werr
	}
	itemsOnly := func(result outcome) (any, *wireError) {
		if result.werr != nil {
			return nil, result.werr
		}
		page, ok := result.value.(map[string]any)
		if !ok {
			return nil, networkError()
		}
		return page["items"], nil
	}

	switch operation {
	case "currentSession":
		return plain(s.request("GET", "/api/v1/session", nil, nil, nil, auth))
	case "capabilities":
		return plain(s.request("GET", "/api/v1/capabilities", nil, nil, nil, auth))
	case "decidePairing":
		var in struct {
			PairingID   string `json:"pairing_id"`
			Code        string `json:"code"`
			Origin      string `json:"origin"`
			Etag        string `json:"etag"`
			OperationID string `json:"operation_id"`
			Action      string `json:"action"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		if in.Action != "approve" && in.Action != "reject" {
			return nil, &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID", Retryable: false}
		}
		return decision(s.request("POST", "/api/v1/auth/pairings/"+url.PathEscape(in.PairingID)+"/"+in.Action, nil,
			map[string]string{"If-Match": in.Etag, "Idempotency-Key": in.OperationID},
			jsonBody(map[string]string{"code": in.Code, "origin": in.Origin}), auth))
	case "pendingPairings":
		return itemsOnly(s.request("GET", "/api/v1/auth/pairings", nil, nil, nil, auth))
	case "listSessions":
		return itemsOnly(s.request("GET", "/api/v1/sessions", nil, nil, nil, auth))
	case "operation":
		var in struct {
			OperationID string `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/operations/"+url.PathEscape(in.OperationID), nil, nil, nil, auth))
	case "revokeSession":
		var in struct {
			SessionID string `json:"session_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("DELETE", "/api/v1/sessions/"+url.PathEscape(in.SessionID), nil, nil, nil, auth))
	case "revokeOwnSession":
		// Capture the explicit logout intent BEFORE the revoke is dispatched,
		// under the matching attachment guard. RAM forgets immediately; the
		// credential FILE unlink is attempted before the network call so a
		// local I/O failure can never be masked by the server outcome. The
		// captured bearer lives on only inside this revoke attempt and the
		// later owned cleanup; a late response can never mark a NEWER
		// attachment, and a new attach resets the intent flag. The pairing
		// attempt generation is captured at the intent so every in-flight
		// adoption of that era is invalidated (discarded after the lock) —
		// only attempts started AFTER the logout survive.
		var invalidatedAttemptGen uint64
		var discardInflight bool
		s.mu.Lock()
		mine := s.attachmentID != "" && s.attachmentID == auth.attachmentID && s.generation == auth.gen
		if mine {
			if s.onboarding != nil {
				invalidatedAttemptGen = s.onboarding.AttemptGeneration()
				discardInflight = true
			}
			s.logoutInvalidatedGen = invalidatedAttemptGen
			s.logoutSeen = true
			s.logoutBearer = s.token
			s.forgetAttachmentID = auth.attachmentID
			s.token = ""
			s.cancelReferenceReadsLocked()
			s.forgetNeeded = true
			s.forgetDone = false
			s.forgetCredentialFileLocked()
		}
		localFailed := s.forgetNeeded && !s.forgetDone
		forgetErr := s.forgetErr
		s.mu.Unlock()
		if discardInflight {
			// Quiesce activation and drop any attempt of the logged-out era
			// OUTSIDE the facade mutex (established facade.mu→o.mu only).
			s.onboarding.DiscardAttemptUpTo(invalidatedAttemptGen)
		}
		if localFailed {
			// The local error cannot wait for an unknown network outcome.
			// This one captured revoke continues independently; its eventual
			// receipt never updates a newer attachment or claims local success.
			go func() {
				result := s.request("DELETE", "/api/v1/session", nil, nil, nil, auth)
				rows := map[string]any{"status": result.status}
				if result.werr != nil {
					rows["error"] = result.werr.Code
				}
				s.receipt("revokeOwnSession", rows)
			}()
			return nil, &wireError{
				Status:    500,
				Code:      "NATIVE_LOCAL_FORGET_FAILED",
				Retryable: true,
				Details:   map[string]any{"server_status": nil, "server_outcome": "unknown", "io_error": forgetErr},
			}
		}
		result := s.request("DELETE", "/api/v1/session", nil, nil, nil, auth)
		if result.werr == nil {
			s.receipt("revokeOwnSession", map[string]any{"status": result.status})
		}
		return result.value, result.werr
	case "serviceStatus":
		return plain(s.request("GET", "/api/v1/status", nil, nil, nil, auth))
	case "listProjects":
		var in struct {
			Cursor string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/projects", collection(map[string]string{"cursor": in.Cursor}), nil, nil, auth))
	case "search":
		var in struct {
			Q         string  `json:"q"`
			ProjectID *string `json:"project_id"`
			Lifecycle *string `json:"lifecycle"`
			Versions  *string `json:"versions"`
			Kind      *string `json:"kind"`
			Cursor    *string `json:"cursor"`
			Limit     *int    `json:"limit"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		// Preserve supplied empty filters and zero limit for the same server
		// validation as HTTP. Omitting them would silently broaden a request.
		params := url.Values{"q": {in.Q}}
		for key, value := range map[string]*string{"project_id": in.ProjectID, "lifecycle": in.Lifecycle, "versions": in.Versions, "kind": in.Kind, "cursor": in.Cursor} {
			if value != nil {
				params.Set(key, *value)
			}
		}
		if in.Limit != nil {
			params.Set("limit", strconv.Itoa(*in.Limit))
		}
		return plain(s.request("GET", "/api/v1/search", params, nil, nil, auth))
	case "listDocuments":
		var in struct {
			ProjectID string `json:"project_id"`
			Lifecycle string `json:"lifecycle"`
			Cursor    string `json:"cursor"`
			Review    string `json:"review"`
			Revisit   string `json:"revisit"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/documents", collection(map[string]string{"project_id": in.ProjectID, "lifecycle": in.Lifecycle, "cursor": in.Cursor, "review": in.Review, "revisit": in.Revisit}), nil, nil, auth))
	case "documentDetail":
		var in struct {
			DocumentID string `json:"document_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/documents/"+url.PathEscape(in.DocumentID), nil, nil, nil, auth))
	case "documentVersions":
		var in struct {
			DocumentID string `json:"document_id"`
			Cursor     string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/documents/"+url.PathEscape(in.DocumentID)+"/versions", collection(map[string]string{"cursor": in.Cursor}), nil, nil, auth))
	case "versionDetail":
		var in struct {
			VersionID string `json:"version_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/versions/"+url.PathEscape(in.VersionID), nil, nil, nil, auth))
	case "versionBacklinks":
		var in struct {
			VersionID string  `json:"version_id"`
			Cursor    *string `json:"cursor"`
			Limit     *int    `json:"limit"`
			Page      *string `json:"page_path"`
			Region    *string `json:"region_id"`
		}
		if werr := decodeReferencePayload(payload, &in, "version_id"); werr != nil {
			return nil, werr
		}
		params := url.Values{}
		if in.Cursor != nil {
			params.Set("cursor", *in.Cursor)
		}
		if in.Limit != nil {
			params.Set("limit", strconv.Itoa(*in.Limit))
		}
		if in.Page != nil {
			params.Set("page_path", *in.Page)
		}
		if in.Region != nil {
			params.Set("region_id", *in.Region)
		}
		return s.referenceRead("/api/v1/versions/"+url.PathEscape(in.VersionID)+"/backlinks", params, auth)
	case "validateVersionIndexes":
		var in struct {
			VersionID string `json:"version_id"`
			Kind      string `json:"kind"`
		}
		if werr := decodeReferencePayload(payload, &in, "version_id", "kind"); werr != nil {
			return nil, werr
		}
		return s.referenceRead("/api/v1/versions/"+url.PathEscape(in.VersionID)+"/indexes/validate", url.Values{"kind": {in.Kind}}, auth)
	case "rebuildVersionIndexes":
		var in struct {
			VersionID   string `json:"version_id"`
			Kind        string `json:"kind"`
			OperationID string `json:"operation_id"`
		}
		if werr := decodeReferencePayload(payload, &in, "version_id", "kind", "operation_id"); werr != nil {
			return nil, werr
		}
		return decision(s.request("POST", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/indexes/rebuild", nil, map[string]string{"Idempotency-Key": in.OperationID}, jsonBody(map[string]string{"kind": in.Kind}), auth))
	case "versionReferences":
		var in struct {
			VersionID string  `json:"version_id"`
			Cursor    *string `json:"cursor"`
			Limit     *int    `json:"limit"`
		}
		if werr := decodeReferencePayload(payload, &in, "version_id"); werr != nil {
			return nil, werr
		}
		params := url.Values{}
		if in.Cursor != nil {
			params.Set("cursor", *in.Cursor)
		}
		if in.Limit != nil {
			params.Set("limit", strconv.Itoa(*in.Limit))
		}
		return s.referenceRead("/api/v1/versions/"+url.PathEscape(in.VersionID)+"/references", params, auth)
	case "resolveReference":
		var in struct {
			VersionID   string  `json:"version_id"`
			ReferenceID string  `json:"reference_id"`
			PagePath    *string `json:"page_path"`
			RegionID    *string `json:"region_id"`
		}
		if werr := decodeReferencePayload(payload, &in, "version_id", "reference_id"); werr != nil {
			return nil, werr
		}
		params := url.Values{"reference_id": {in.ReferenceID}}
		if in.PagePath != nil {
			params.Set("page_path", *in.PagePath)
		}
		if in.RegionID != nil {
			params.Set("region_id", *in.RegionID)
		}
		return s.referenceRead("/api/v1/versions/"+url.PathEscape(in.VersionID)+"/references/resolve", params, auth)
	case "versionFiles":
		var in struct {
			VersionID string `json:"version_id"`
			Cursor    string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/files", collection(map[string]string{"cursor": in.Cursor}), nil, nil, auth))
	case "versionSuppliedMetadata":
		var in struct {
			VersionID string `json:"version_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		query := url.Values{}
		query.Set("kind", "supplied")
		return s.openMetadataStream(in.VersionID, query, auth)
	case "configurationView":
		var in struct{}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/configuration", nil, nil, nil, auth))
	case "configurationPlan":
		var in struct {
			Request     json.RawMessage `json:"request"`
			OperationID string          `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return decision(s.request("POST", "/api/v1/configuration/plans", nil, map[string]string{"Idempotency-Key": in.OperationID}, in.Request, auth))
	case "configurationPlanCommit":
		var in struct {
			PlanID      string          `json:"plan_id"`
			Request     json.RawMessage `json:"request"`
			OperationID string          `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return decision(s.request("POST", "/api/v1/configuration/plans/"+url.PathEscape(in.PlanID)+"/commit", nil, map[string]string{"Idempotency-Key": in.OperationID}, in.Request, auth))
	case "projectRegister":
		var in struct {
			Name        string `json:"name"`
			OperationID string `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return decision(s.request("POST", "/api/v1/projects", nil, map[string]string{"Idempotency-Key": in.OperationID}, jsonBody(map[string]string{"name": in.Name}), auth))
	case "projectSources":
		var in struct {
			ProjectID string `json:"project_id"`
			Cursor    string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/projects/"+url.PathEscape(in.ProjectID)+"/sources", collection(map[string]string{"cursor": in.Cursor}), nil, nil, auth))
	case "verifySourceEvidence":
		var in struct {
			VersionID   string          `json:"version_id"`
			EvidenceID  string          `json:"evidence_id"`
			Request     json.RawMessage `json:"request"`
			OperationID string          `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return decision(s.request("POST", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/source-evidence-verifications", collection(map[string]string{"evidence_id": in.EvidenceID}), map[string]string{"Idempotency-Key": in.OperationID}, in.Request, auth))
	case "runSourceObservation":
		var in struct {
			VersionID   string          `json:"version_id"`
			Request     json.RawMessage `json:"request"`
			OperationID string          `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return decision(s.request("POST", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/source-observations", nil, map[string]string{"Idempotency-Key": in.OperationID}, in.Request, auth))
	case "versionEvidence":
		var in struct {
			VersionID  string `json:"version_id"`
			EvidenceID string `json:"evidence_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/source-evidence/"+url.PathEscape(in.EvidenceID), nil, nil, nil, auth))
	case "evidenceVerifications":
		var in struct {
			VersionID  string `json:"version_id"`
			EvidenceID string `json:"evidence_id"`
			Cursor     string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/source-evidence-verifications", collection(map[string]string{"evidence_id": in.EvidenceID, "cursor": in.Cursor}), nil, nil, auth))
	case "versionAssessments":
		var in struct {
			VersionID   string `json:"version_id"`
			TargetSetID string `json:"target_set_id"`
			ScopeKind   string `json:"scope_kind"`
			PagePath    string `json:"page_path"`
			RegionID    string `json:"region_id"`
			Cursor      string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		switch in.ScopeKind {
		case "", "version", "page", "region":
		default:
			return nil, &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID", Retryable: false}
		}
		return plain(s.request("GET", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/assessments", collection(map[string]string{"target_set_id": in.TargetSetID, "scope_kind": in.ScopeKind, "page_path": in.PagePath, "region_id": in.RegionID, "cursor": in.Cursor}), nil, nil, auth))
	case "assessmentCoverage":
		var in struct {
			VersionID     string `json:"version_id"`
			CoverageSetID string `json:"coverage_set_id"`
			AttemptRunID  string `json:"attempt_run_id"`
			Cursor        string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		if in.CoverageSetID == "" {
			return nil, &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID", Retryable: false}
		}
		return plain(s.request("GET", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/assessments/coverage", collection(map[string]string{"coverage_set_id": in.CoverageSetID, "attempt_run_id": in.AttemptRunID, "cursor": in.Cursor}), nil, nil, auth))
	case "renderBootstrap":
		var in struct {
			VersionID string  `json:"version_id"`
			PagePath  *string `json:"page_path"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		query := url.Values{}
		if in.PagePath != nil {
			query.Set("page_path", *in.PagePath)
		}
		return plain(s.request("GET", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/render-bootstrap", query, nil, nil, auth))
	case "renderSessionOpen":
		var in struct {
			VersionID   string `json:"version_id"`
			OperationID string `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		result := s.request("POST", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/render-sessions", nil,
			map[string]string{"Idempotency-Key": in.OperationID}, jsonBody(map[string]any{}), auth)
		if result.werr == nil {
			if view, ok := result.value.(map[string]any); ok {
				if session, ok := view["session_id"].(string); ok {
					s.mu.Lock()
					s.renders = append(s.renders, renderRef{VersionID: in.VersionID, SessionID: session})
					s.mu.Unlock()
					s.receipt("renderSessionOpen", map[string]any{"version_id": in.VersionID, "session_id": session})
				}
			}
		}
		return result.value, result.werr
	case "renderSessionRenew":
		var in struct {
			VersionID string `json:"version_id"`
			SessionID string `json:"session_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("POST", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/render-sessions/"+url.PathEscape(in.SessionID)+"/renew", nil, nil, jsonBody(map[string]any{}), auth))
	case "renderSessionClose":
		var in struct {
			VersionID string `json:"version_id"`
			SessionID string `json:"session_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		result := s.request("POST", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/render-sessions/"+url.PathEscape(in.SessionID)+"/close", nil, nil, jsonBody(map[string]any{}), auth)
		if result.werr == nil {
			s.mu.Lock()
			kept := s.renders[:0]
			for _, ref := range s.renders {
				if ref.SessionID != in.SessionID {
					kept = append(kept, ref)
				}
			}
			s.renders = kept
			s.mu.Unlock()
			s.receipt("renderSessionClose", map[string]any{"version_id": in.VersionID, "session_id": in.SessionID})
		}
		return result.value, result.werr
	case "annotationList", "annotationGet", "annotationHistory", "annotationCreate", "annotationPatch", "annotationDelete", "annotationBodyStream", "annotationContextStream", "revisitGet", "revisitPut":
		return s.annotationOperation(operation, payload, auth)
	case "reviewGet", "reviewHistory":
		var in struct {
			VersionID string `json:"version_id"`
			Cursor    string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		path := "/api/v1/versions/" + url.PathEscape(in.VersionID) + "/review"
		query := url.Values{}
		if operation == "reviewHistory" {
			path += "/history"
			if in.Cursor != "" {
				query.Set("cursor", in.Cursor)
			}
		}
		return plain(s.request("GET", path, query, nil, nil, auth))
	case "reviewPut":
		var in struct {
			VersionID    string          `json:"version_id"`
			Body         json.RawMessage `json:"body"`
			OperationID  string          `json:"operation_id"`
			Precondition struct {
				IfMatch *string `json:"if_match"`
				Create  *bool   `json:"create"`
			} `json:"precondition"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		headers := map[string]string{"Idempotency-Key": in.OperationID}
		if in.Precondition.IfMatch != nil && in.Precondition.Create == nil {
			headers["If-Match"] = *in.Precondition.IfMatch
		} else if in.Precondition.IfMatch == nil && in.Precondition.Create != nil && *in.Precondition.Create {
			headers["If-None-Match"] = "*"
		} else {
			return nil, &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID"}
		}
		return decision(s.request("PUT", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/review", nil, headers, in.Body, auth))
	case "personalGet":
		var in struct {
			Kind      string  `json:"kind"`
			VersionID string  `json:"version_id"`
			Page      *string `json:"page"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		query := url.Values{}
		if in.Kind == "page-position" && in.Page != nil {
			query.Set("page", *in.Page)
		}
		return plain(s.request("GET", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/"+in.Kind, query, nil, nil, auth))
	case "personalPut":
		var in struct {
			Kind         string          `json:"kind"`
			VersionID    string          `json:"version_id"`
			Page         *string         `json:"page"`
			Body         json.RawMessage `json:"body"`
			Precondition struct {
				IfMatch *string `json:"if_match"`
				Create  *bool   `json:"create"`
			} `json:"precondition"`
			OperationID string `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		headers := map[string]string{"Idempotency-Key": in.OperationID}
		if in.Precondition.IfMatch != nil {
			headers["If-Match"] = *in.Precondition.IfMatch
		} else {
			headers["If-None-Match"] = "*"
		}
		query := url.Values{}
		if in.Kind == "page-position" && in.Page != nil {
			query.Set("page", *in.Page)
		}
		result := s.request("PUT", "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/"+in.Kind, query, headers, in.Body, auth)
		if result.werr == nil {
			// Nonsecret request identity: a bounded operation-id prefix plus
			// the exact body digest and precondition kind let harnesses prove
			// a same-K replay dispatched the IDENTICAL request.
			keyPrefix := in.OperationID
			if len(keyPrefix) > 8 {
				keyPrefix = keyPrefix[:8]
			}
			digest := sha256.Sum256(in.Body)
			condition := "if_match"
			if in.Precondition.IfMatch == nil {
				condition = "create"
			}
			s.receipt("personalPut", map[string]any{"kind": in.Kind, "status": result.status, "operation_prefix": keyPrefix, "body_sha256": hex.EncodeToString(digest[:]), "condition": condition})
		}
		return result.value, result.werr
	case "listUploads":
		var in struct {
			State     *string `json:"state"`
			Unadopted *bool   `json:"unadopted"`
			Cursor    string  `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		params := map[string]string{"cursor": in.Cursor}
		if in.State != nil {
			params["state"] = *in.State
		}
		if in.Unadopted != nil {
			params["unadopted"] = fmt.Sprintf("%t", *in.Unadopted)
		}
		return plain(s.request("GET", "/api/v1/uploads", collection(params), nil, nil, auth))
	case "uploadDetail":
		var in struct {
			UploadID string `json:"upload_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/uploads/"+url.PathEscape(in.UploadID), nil, nil, nil, auth))
	case "uploadEntries":
		var in struct {
			UploadID string `json:"upload_id"`
			Cursor   string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/uploads/"+url.PathEscape(in.UploadID)+"/entries", collection(map[string]string{"cursor": in.Cursor}), nil, nil, auth))
	case "uploadRanges":
		var in struct {
			UploadID string `json:"upload_id"`
			Path     string `json:"path"`
			Cursor   string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/uploads/"+url.PathEscape(in.UploadID)+"/ranges", collection(map[string]string{"path": in.Path, "cursor": in.Cursor}), nil, nil, auth))
	case "uploadPins":
		var in struct {
			UploadID string `json:"upload_id"`
			Cursor   string `json:"cursor"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/uploads/"+url.PathEscape(in.UploadID)+"/pins", collection(map[string]string{"cursor": in.Cursor}), nil, nil, auth))
	case "createUpload":
		var in struct {
			Request     json.RawMessage `json:"request"`
			OperationID string          `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		result := s.request("POST", "/api/v1/uploads", nil, map[string]string{"Idempotency-Key": in.OperationID}, in.Request, auth)
		if result.werr == nil {
			s.receipt("createUpload", map[string]any{"status": result.status})
		}
		return result.value, result.werr
	case "createContextUpload":
		var in struct {
			Binding     json.RawMessage `json:"binding"`
			OperationID string          `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		body := map[string]any{"purpose": "context", "input_mode": "object"}
		var target any
		if err := json.Unmarshal(in.Binding, &target); err != nil {
			return nil, &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID", Retryable: false}
		}
		body["target"] = target
		result := s.request("POST", "/api/v1/uploads", nil, map[string]string{"Idempotency-Key": in.OperationID}, jsonBody(body), auth)
		if result.werr == nil {
			s.receipt("createContextUpload", map[string]any{"status": result.status})
		}
		return result.value, result.werr
	case "putUploadEntry":
		return s.putUploadEntry(payload, auth)
	case "completeUpload":
		var in struct {
			UploadID    string          `json:"upload_id"`
			Checkpoint  json.RawMessage `json:"checkpoint"`
			OperationID string          `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		result := s.request("POST", "/api/v1/uploads/"+url.PathEscape(in.UploadID)+"/complete", nil,
			map[string]string{"Idempotency-Key": in.OperationID}, jsonBody(map[string]any{"checkpoint": in.Checkpoint}), auth)
		if result.werr == nil {
			s.receipt("completeUpload", map[string]any{"status": result.status})
		}
		return decision(result)
	case "abandonUpload":
		var in struct {
			UploadID    string `json:"upload_id"`
			OperationID string `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return decision(s.request("POST", "/api/v1/uploads/"+url.PathEscape(in.UploadID)+"/abandon", nil,
			map[string]string{"Idempotency-Key": in.OperationID}, jsonBody(map[string]any{}), auth))
	case "importPublish":
		var in struct {
			Request     json.RawMessage `json:"request"`
			OperationID string          `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		result := s.request("POST", "/api/v1/imports", nil, map[string]string{"Idempotency-Key": in.OperationID}, in.Request, auth)
		if result.werr == nil {
			s.receipt("importPublish", map[string]any{"status": result.status})
		}
		return decision(result)
	case "jobGet":
		var in struct {
			JobID string `json:"job_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return plain(s.request("GET", "/api/v1/jobs/"+url.PathEscape(in.JobID), nil, nil, nil, auth))
	case "jobCancel":
		var in struct {
			JobID       string `json:"job_id"`
			OperationID string `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return decision(s.request("POST", "/api/v1/jobs/"+url.PathEscape(in.JobID)+"/cancel", nil,
			map[string]string{"Idempotency-Key": in.OperationID}, jsonBody(map[string]any{}), auth))
	case "consumeContext":
		var in struct {
			Binding struct {
				VersionID        string  `json:"version_id"`
				PagePath         *string `json:"page_path"`
				RenderInstanceID *string `json:"render_instance_id"`
			} `json:"binding"`
			UploadID    string `json:"upload_id"`
			OperationID string `json:"operation_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		query := url.Values{}
		query.Set("version_id", in.Binding.VersionID)
		query.Set("input", "upload")
		if in.Binding.PagePath != nil {
			query.Set("page_path", *in.Binding.PagePath)
		}
		if in.Binding.RenderInstanceID != nil {
			query.Set("render_instance_id", *in.Binding.RenderInstanceID)
		}
		result := s.request("POST", "/api/v1/contexts", query,
			map[string]string{"Idempotency-Key": in.OperationID}, jsonBody(map[string]string{"upload_id": in.UploadID}), auth)
		if result.werr == nil {
			receipt := map[string]any{"status": result.status}
			if body, ok := result.value.(map[string]any); ok {
				if digest, ok := body["bytes_sha256"].(string); ok {
					// Nonsecret exact-byte digest of the consumed context.
					receipt["bytes_sha256"] = digest
				}
			}
			s.receipt("consumeContext", receipt)
		}
		return decision(result)
	case "verifyContextFresh":
		return s.verifyContextFresh(payload, auth)
	case "packageStream":
		var in struct {
			VersionID string `json:"version_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		return s.openStream(in.VersionID, "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/package", nil, auth, false)
	case "fileStream":
		var in struct {
			VersionID string `json:"version_id"`
			Path      string `json:"path"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		query := url.Values{}
		query.Set("path", in.Path)
		// The delivered file representation carries its own strong validator
		// (Repr-Digest + Content-Length) beside the stream handle; rendering
		// behavior is unchanged, the download adapter consumes it.
		return s.openStream(in.VersionID, "/api/v1/versions/"+url.PathEscape(in.VersionID)+"/file", query, auth, true)
	case "chooseImportSource":
		return s.chooseImportSource(payload, auth)
	case "sourceEntries":
		return s.sourceEntries(payload)
	case "readSourceRange":
		return s.readSourceRange(payload)
	case "releaseSource":
		return s.releaseSource(payload)
	case "sourceVerifyStable":
		return s.sourceVerifyStable(payload)
	case "chooseDownloadDestination":
		return s.chooseDownloadDestination(payload, auth)
	case "destOpen":
		return s.destOpen(payload)
	case "destWrite":
		return s.destWrite(payload)
	case "destClose":
		return s.destClose(payload)
	case "destAbort":
		return s.destAbort(payload)
	case "streamRead":
		return s.streamRead(payload)
	case "streamCancel":
		var in struct {
			StreamID string `json:"stream_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		s.dropStream(in.StreamID)
		return nil, nil
	case "subscribeEvents":
		return s.subscribeEvents(auth)
	case "eventsPoll":
		return s.eventsPoll(payload, auth)
	case "eventsClose":
		var in struct {
			SubscriptionID string `json:"subscription_id"`
		}
		if werr := decodePayload(payload, &in); werr != nil {
			return nil, werr
		}
		s.mu.Lock()
		delete(s.subs, in.SubscriptionID)
		s.mu.Unlock()
		return nil, nil
	default:
		return nil, &wireError{Status: 400, Code: "NATIVE_UNKNOWN_OPERATION", Retryable: false}
	}
}

func (s *ReaderService) attach() (any, *wireError) {
	auth := s.snapshotAuthority()
	if auth.token == "" {
		if s.logoutPending() {
			// A forgotten (explicitly logged-out) credential must never
			// reauthenticate; a same-process reload or a pending logout cannot
			// restore it.
			return nil, &wireError{Status: 401, Code: "NATIVE_SESSION_LOGGED_OUT", Retryable: false}
		}
		if s.onboarding == nil {
			// The reused harness constructor without a credential is the
			// honest not-yet-paired state; only a real logout ever claims the
			// logged-out state.
			return nil, &wireError{Status: 401, Code: "NATIVE_NO_CREDENTIAL", Retryable: false}
		}
		// M4-A2 remembered boot/reattach: resolve the signed binding FIRST,
		// then install the exact stored endpoint+bearer pair. True absence
		// stays the honest typed first boot; a stopped daemon, a pin/endpoint
		// mismatch and an unreadable store all propagate typed instead of
		// being discarded into an empty boot.
		installed, werr := s.loadRememberedCredential()
		if werr != nil {
			return nil, werr
		}
		if !installed {
			return nil, &wireError{Status: 401, Code: "NATIVE_NO_CREDENTIAL", Retryable: false}
		}
		auth = s.snapshotAuthority()
	}
	// The signed onboarding binding resolves and validates FIRST — before any
	// bearer-bearing request leaves the process. A facade whose captured
	// endpoint does not match the signed binding, or whose pin cannot be
	// resolved, fails typed with ZERO bearer deliveries; the mismatched
	// listener never receives the credential.
	if s.onboarding != nil {
		bindingEndpoint, _, werr := s.onboarding.ResolveBinding(context.Background())
		if werr != nil {
			return nil, onboardingWireError(werr)
		}
		if bindingEndpoint != auth.endpoint {
			return nil, &wireError{Status: 409, Code: "NATIVE_CREDENTIAL_MISMATCHED", Retryable: false, Details: map[string]any{"captured_endpoint": auth.endpoint, "binding_endpoint": bindingEndpoint}}
		}
	}
	sessionResult := s.request("GET", "/api/v1/session", nil, nil, nil, auth)
	if sessionResult.werr != nil {
		return nil, sessionResult.werr
	}
	capsResult := s.request("GET", "/api/v1/capabilities", nil, nil, nil, auth)
	if capsResult.werr != nil {
		return nil, capsResult.werr
	}
	// The captured token/endpoint pair must still be current AFTER the async
	// session/capability reads: a concurrent adoption activation or logout
	// replaces both together, and this attach must never publish an
	// attachment that resurrects the replaced context.
	if !s.contextCurrent(auth) {
		return nil, &wireError{Status: 409, Code: "NATIVE_SESSION_REPLACED", Retryable: false}
	}
	// The resolved pin also gates the product API and the session/capabilities
	// library identity before any attachment is issued.
	if s.onboarding != nil {
		if werr := s.verifyBindingIdentity(sessionResult.value, capsResult.value); werr != nil {
			return nil, werr
		}
	}
	attachment := randomID()
	if attachment == "" {
		return nil, networkError()
	}
	s.mu.Lock()
	// Final ownership check before the era transition: the captured
	// generation and attachment must still own the process. A concurrent
	// attach or dispose of the same era cannot be overwritten or resurrected.
	if !s.contextCurrentLocked(auth) {
		s.mu.Unlock()
		return nil, &wireError{Status: 409, Code: "NATIVE_SESSION_REPLACED", Retryable: false}
	}
	previousAttachment := s.attachmentID
	s.generation++
	s.cancelReferenceReadsLocked()
	s.attachmentID = attachment
	// A successful re-attach is a filesystem-era boundary too: pending picker
	// leases die and the PREVIOUS attachment's owner-selected handles are
	// detached now (their anchored roots and unfinished temps drain outside
	// the lock) instead of silently outliving the era in the handle maps.
	s.fsEpoch++
	s.logoutSeen = false
	s.mu.Unlock()
	s.releaseOwnedHandles(previousAttachment)
	s.receipt("attachSession", map[string]any{"attachment_prefix": attachment[:8]})
	return map[string]any{
		"attachment_id": attachment,
		"session":       sessionResult.value,
		"capabilities":  capsResult.value,
		"public_origin": auth.endpoint,
	}, nil
}

// contextCurrent rechecks the captured context (token, endpoint, generation
// and attachment ownership) against the live service state.
func (s *ReaderService) contextCurrent(auth authority) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contextCurrentLocked(auth)
}

func (s *ReaderService) contextCurrentLocked(auth authority) bool {
	// The generation is bumped by attach publication, owned disposal and
	// adoption activation. A revokeOwnSession INTENT does not bump it, so
	// the logout flags (logoutSeen/forgetNeeded) gate the empty-token path
	// independently (M4-A2); token+base+generation together prove the
	// captured context still owns the process.
	return s.token == auth.token && s.base == auth.endpoint && s.generation == auth.gen
}

// loadRememberedCredential resolves the selection's remembered binding
// through the signed verified binding and installs the exact stored
// endpoint+bearer pair (M4-A2). The credential read runs outside the mutex;
// installation happens under one continuous lock hold that re-verifies the
// EXACT pre-load state: the token stayed empty (no adoption activation and
// no concurrent remembered install intervened), the generation stayed at
// its pre-load value (no attach publication, owned disposal or activation
// replaced the era) and no logout intent became visible. The logout flags
// are checked independently because a revokeOwnSession intent itself does
// NOT bump the generation. A late load that lost any of these races is
// rejected typed WITHOUT touching the current authority.
func (s *ReaderService) loadRememberedCredential() (bool, *wireError) {
	// Entry snapshot under ONE continuous lock hold (M4-A2): the empty-token
	// state, the generation and the logout flags are captured TOGETHER
	// before the credential read leaves the mutex, so the install guards
	// compare against an atomically captured precondition instead of the
	// caller's earlier snapshot.
	s.mu.Lock()
	preEmptyToken := s.token == ""
	preGen := s.generation
	preLogoutSeen := s.logoutSeen
	preForgetNeeded := s.forgetNeeded
	s.mu.Unlock()
	switch {
	case preLogoutSeen || preForgetNeeded:
		// A logout intent landed between the caller's check and this entry:
		// the honest answer is the logged-out state, never a load.
		return false, &wireError{Status: 401, Code: "NATIVE_SESSION_LOGGED_OUT", Retryable: false}
	case !preEmptyToken:
		// A concurrent installation already owns the process; this load is
		// stale and must not touch the current authority.
		return false, &wireError{Status: 409, Code: "NATIVE_SESSION_REPLACED", Retryable: false}
	}
	credential, found, oerr := s.onboarding.RememberedCredential(context.Background())
	if oerr != nil {
		return false, onboardingWireError(oerr)
	}
	if !found {
		return false, nil
	}
	if s.testAfterRememberedLoad != nil {
		s.testAfterRememberedLoad()
	}
	s.mu.Lock()
	if s.token != "" || s.generation != preGen || s.logoutSeen || s.forgetNeeded {
		s.mu.Unlock()
		return false, &wireError{Status: 409, Code: "NATIVE_SESSION_REPLACED", Retryable: false}
	}
	// The endpoint and the bearer travel together from the SAME credential
	// read; the base is never recomposed from later discovery state.
	s.base = credential.Endpoint
	s.token = credential.Token
	s.generation++
	s.cancelReferenceReadsLocked()
	// The installation is a filesystem-era boundary for picker leases too:
	// a panel result captured before the installation can never register.
	s.fsEpoch++
	s.mu.Unlock()
	s.receipt("rememberedActivation", map[string]any{"endpoint": credential.Endpoint})
	return true, nil
}

// logoutPending reports whether an explicit logout intent is currently
// outstanding (RAM bearer cleared and the local forget unresolved).
func (s *ReaderService) logoutPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logoutSeen || s.forgetNeeded
}

// verifyBindingIdentity enforces the product API pin and the library triple
// (session == capabilities == signed discovery) BEFORE effective authority.
func (s *ReaderService) verifyBindingIdentity(session, caps any) *wireError {
	sessionView, ok := session.(map[string]any)
	if !ok {
		return uncertain(0)
	}
	capsView, ok := caps.(map[string]any)
	if !ok {
		return uncertain(0)
	}
	sessionLibrary, _ := sessionView["library_id"].(string)
	capsLibrary, _ := capsView["library_id"].(string)
	apiVersion, _ := capsView["api_version"].(string)
	if apiVersion != s.onboarding.ProductAPI() {
		return &wireError{Status: 409, Code: "NATIVE_API_INCOMPATIBLE", Retryable: false, Details: map[string]any{"api_version": apiVersion, "expected": s.onboarding.ProductAPI()}}
	}
	if sessionLibrary != capsLibrary {
		return &wireError{Status: 409, Code: "NATIVE_LIBRARY_MISMATCH", Retryable: false, Details: map[string]any{"session_library": sessionLibrary, "caps_library": capsLibrary}}
	}
	if expected := s.onboarding.ExpectedLibrary(); expected == "" || capsLibrary != expected {
		return &wireError{Status: 409, Code: "NATIVE_LIBRARY_MISMATCH", Retryable: false, Details: map[string]any{"caps_library": capsLibrary, "expected_library": expected}}
	}
	return nil
}

// activateAdoptedCredential installs the ADOPTED credential exactly as
// captured at its commit: the endpoint and bearer travel together from the
// attempt's own binding — never recomposed from later discovery state. The
// onboarding package invokes it under its activation mutex only; it never
// takes the onboarding mutex and is never called from DiscardAttempt.
//
// Installing a new credential is an era transition: the old attachment and
// its owned streams are invalidated before the new pair becomes visible, so
// no old handle can act under the new token. An explicit logout whose
// captured bearer IS this credential wins over the late activation; a logout
// of a DIFFERENT (older) credential does not block a fresh authorized
// relogin, and the old local-forget obligation is preserved untouched.
func (s *ReaderService) activateAdoptedCredential(credential client.Credential, attemptGen uint64) {
	s.mu.Lock()
	if (s.logoutSeen || s.forgetNeeded) && (attemptGen <= s.logoutInvalidatedGen || s.logoutBearer == credential.Token) {
		// The owner explicitly logged out while this adoption's attempt was
		// in flight (its generation was invalidated at the intent) or logged
		// out exactly this credential; it must not be resurrected.
		s.mu.Unlock()
		s.receipt("adoptActivationSuppressed", map[string]any{"reason": "logout-invalidated-attempt"})
		return
	}
	previousToken := s.token
	previousBase := s.base
	s.base = credential.Endpoint
	s.token = credential.Token
	// Era transition: invalidate the old attachment and detach its owned
	// filesystem handles (their anchored roots and unfinished temps) so
	// nothing outlives the replaced context; drain them outside the lock.
	s.generation++
	s.cancelReferenceReadsLocked()
	previousAttachment := s.attachmentID
	s.attachmentID = ""
	s.fsEpoch++
	s.logoutSeen = false
	streams := make([]*readerStream, 0, len(s.streams))
	for _, owned := range s.streams {
		streams = append(streams, owned)
	}
	s.streams = map[string]*readerStream{}
	s.subs = map[string]*readerSubscription{}
	renders := s.renders
	s.renders = nil
	s.mu.Unlock()
	s.releaseOwnedHandles(previousAttachment)
	for _, stream := range streams {
		stream.cancel()
		stream.body.Close()
	}
	if len(renders) > 0 {
		// Renderer deliveries opened under the replaced credential close with
		// THAT credential's exact bearer and endpoint, both captured before
		// the replacement.
		auth := authority{token: previousToken, endpoint: previousBase}
		for _, ref := range renders {
			s.request("POST", "/api/v1/versions/"+url.PathEscape(ref.VersionID)+"/render-sessions/"+url.PathEscape(ref.SessionID)+"/close", nil, nil, []byte("{}"), auth)
		}
	}
	s.receipt("adoptActivation", map[string]any{"endpoint": credential.Endpoint})
}

// onboardingWireError maps the onboarding typed failure onto the closed wire
// envelope, preserving Retry-After for known non-consuming rate limits.
func onboardingWireError(oerr *onboarding.Error) *wireError {
	if oerr == nil {
		return nil
	}
	return &wireError{Status: oerr.Status, Code: oerr.Code, Retryable: oerr.Retryable, RetryAfter: oerr.RetryAfter, Details: oerr.Details}
}

func (s *ReaderService) dispose(captured string) *wireError {
	// Capture the pairing attempt generation BEFORE the transition so an
	// owned disposal discards only attempts of this era; a relogin attempt
	// created concurrently afterwards survives.
	var attemptGen uint64
	tracksAttempts := s.onboarding != nil
	if tracksAttempts {
		attemptGen = s.onboarding.AttemptGeneration()
	}
	s.mu.Lock()
	mine := s.attachmentID != "" && captured != "" && captured == s.attachmentID
	// A stale old-attachment callback must never erase or close handles that
	// belong to a newer attachment.
	var streams []*readerStream
	var renders []renderRef
	dropFile := false
	if mine {
		dropFile = s.logoutSeen
		s.attachmentID = ""
		s.generation++
		s.cancelReferenceReadsLocked()
		s.logoutSeen = false
		streams = make([]*readerStream, 0, len(s.streams))
		for _, stream := range s.streams {
			streams = append(streams, stream)
		}
		s.streams = map[string]*readerStream{}
		s.subs = map[string]*readerSubscription{}
		renders = s.renders
		s.renders = nil
	}
	cleanupBearer := s.token
	cleanupEndpoint := s.base
	ownsForget := captured != "" && captured == s.forgetAttachmentID
	forgetStillNeeded := false
	forgetErr := ""
	dropped := false
	// The local-forget obligation is retried under the same lock on EVERY
	// disposal while it is still needed and not honestly done — including
	// disposals after the attachment is gone (no no-op trap). The guarded
	// retry never unlinks a newer binding's credential file.
	if ownsForget && s.forgetNeeded && !s.forgetDone {
		s.forgetCredentialFileLocked()
	}
	if ownsForget && s.token == "" {
		// Owned-cleanup closes after an explicit logout use the captured
		// logout bearer only; the bearer is preserved until the owned
		// cleanup/captured revoke attempt finishes, never for attach.
		cleanupBearer = s.logoutBearer
	}
	if ownsForget {
		forgetStillNeeded = s.forgetNeeded && !s.forgetDone
		if !forgetStillNeeded {
			s.logoutBearer = ""
		}
		forgetErr = s.forgetErr
		dropped = s.forgetDone
	}
	s.mu.Unlock()
	// Disposal releases the owner-selected filesystem handles of the captured
	// attachment era (an anchored root, plus best-effort own-temp cleanup);
	// a stale old-attachment disposal never touches a newer era's handles.
	s.releaseOwnedHandles(captured)
	for _, stream := range streams {
		stream.cancel()
		stream.body.Close()
	}
	if len(renders) > 0 {
		// Disposal closes owned renderer deliveries explicitly under their
		// captured version/session identity; the endpoint travels with the
		// captured cleanup bearer.
		auth := authority{token: cleanupBearer, endpoint: cleanupEndpoint}
		for _, ref := range renders {
			s.request("POST", "/api/v1/versions/"+url.PathEscape(ref.VersionID)+"/render-sessions/"+url.PathEscape(ref.SessionID)+"/close", nil, nil, []byte("{}"), auth)
		}
		s.receipt("disposeClosedRenders", map[string]any{"count": len(renders)})
	}
	s.receipt("disposeSession", map[string]any{"captured_match": mine, "credential_file_dropped": dropped && dropFile})
	if mine && tracksAttempts {
		// Owned disposal quiesces any in-flight Go-held pairing attempt of
		// this era BEFORE the owned cleanup continues, outside the facade
		// mutex (established facade.mu→o.mu only; actMu is never taken
		// under the facade mutex).
		s.onboarding.DiscardAttemptUpTo(attemptGen)
	}
	if forgetStillNeeded {
		// A failed local forget is a CLOSED typed local failure: the disposal
		// cannot honestly claim completion while the credential file survives.
		return &wireError{
			Status:    500,
			Code:      "NATIVE_LOCAL_FORGET_FAILED",
			Retryable: true,
			Details:   map[string]any{"io_error": forgetErr},
		}
	}
	return nil
}

func (s *ReaderService) putUploadEntry(payload json.RawMessage, auth authority) (any, *wireError) {
	var in struct {
		UploadID string `json:"upload_id"`
		Chunk    struct {
			Path        string  `json:"path"`
			Kind        string  `json:"kind"`
			Offset      int64   `json:"offset"`
			BytesB64    string  `json:"bytes_b64"`
			ChunkSha256 string  `json:"chunkSha256"`
			TotalLength *int64  `json:"totalLength"`
			LinkTarget  *string `json:"linkTarget"`
			Final       bool    `json:"final"`
		} `json:"chunk"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	var body []byte
	headers := map[string]string{}
	switch in.Chunk.Kind {
	case "file":
		raw, err := base64.StdEncoding.DecodeString(in.Chunk.BytesB64)
		if err != nil {
			return nil, &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID", Retryable: false}
		}
		body = raw
		headers["Content-Type"] = "application/octet-stream"
		headers["Haeryong-Upload-Offset"] = fmt.Sprintf("%d", in.Chunk.Offset)
		headers["Haeryong-Upload-Chunk-SHA256"] = in.Chunk.ChunkSha256
		if in.Chunk.TotalLength != nil {
			headers["Haeryong-Upload-Length"] = fmt.Sprintf("%d", *in.Chunk.TotalLength)
		}
		if in.Chunk.Final {
			headers["Haeryong-Upload-Final"] = "true"
		}
	case "directory":
		body = []byte("{}")
		headers["Content-Type"] = "application/json"
	case "symlink":
		target := ""
		if in.Chunk.LinkTarget != nil {
			target = *in.Chunk.LinkTarget
		}
		headers["Content-Type"] = "application/json"
		body, _ = json.Marshal(map[string]string{"link_target": target})
	default:
		return nil, &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID", Retryable: false}
	}
	query := url.Values{}
	query.Set("path", in.Chunk.Path)
	query.Set("kind", in.Chunk.Kind)
	result := s.request("PUT", "/api/v1/uploads/"+url.PathEscape(in.UploadID)+"/entries", query, headers, body, auth)
	if result.werr == nil {
		s.receipt("putUploadEntry", map[string]any{"kind": in.Chunk.Kind, "path": in.Chunk.Path})
	}
	return result.value, result.werr
}

func (r outcome) unpack() (any, *wireError) { return r.value, r.werr }

// encodeContextBinding mirrors the canonical browser form: the exact UTF-8
// JSON of the three tuple keys (null for absent members), unpadded base64url.
func encodeContextBinding(versionID string, pagePath, renderInstanceID *string) string {
	encoded, _ := json.Marshal(struct {
		VersionID        string  `json:"version_id"`
		PagePath         *string `json:"page_path"`
		RenderInstanceID *string `json:"render_instance_id"`
	}{VersionID: versionID, PagePath: pagePath, RenderInstanceID: renderInstanceID})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func (s *ReaderService) verifyContextFresh(payload json.RawMessage, auth authority) (any, *wireError) {
	var in struct {
		ContextRef  string `json:"context_ref"`
		BytesSHA256 string `json:"bytes_sha256"`
		Binding     struct {
			VersionID        string  `json:"version_id"`
			PagePath         *string `json:"page_path"`
			RenderInstanceID *string `json:"render_instance_id"`
		} `json:"binding"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	etag := `"sha256-` + in.BytesSHA256 + `"`
	result := s.requestRaw("GET", "/api/v1/contexts/"+url.PathEscape(in.ContextRef), nil,
		map[string]string{
			"Haeryong-Context-Binding": encodeContextBinding(in.Binding.VersionID, in.Binding.PagePath, in.Binding.RenderInstanceID),
			"If-None-Match":            etag,
		}, auth, false)
	if result.werr != nil {
		return nil, result.werr
	}
	if result.status == http.StatusNotModified {
		if result.header.Get("ETag") != etag || result.header.Get("Haeryong-Context-Verified") != "fresh" {
			return nil, &wireError{Status: 304, Code: "CONTEXT_PROOF_INVALID", Retryable: false}
		}
		s.receipt("verifyContextFresh", map[string]any{"etag": etag})
		return map[string]any{"verified": true, "etag": etag}, nil
	}
	if result.status >= 200 && result.status < 300 {
		// A body-carrying response is never a fresh proof.
		return nil, &wireError{Status: result.status, Code: "CONTEXT_PROOF_INVALID", Retryable: false}
	}
	_, werr := typedResult(result.status, result.body)
	return nil, werr
}

// openMetadataStream opens the exact supplied-metadata object stream and
// returns the immutable object's strong ETag beside the stream handle. The
// validator travels with the open reply so JavaScript can verify the exact
// accumulated bytes before parsing them; per-pull attachment/daemon/version
// guards stay identical to every other stream.
func (s *ReaderService) openMetadataStream(versionID string, query url.Values, auth authority) (any, *wireError) {
	epoch, werr := s.currentEpoch(auth)
	if werr != nil {
		return nil, werr
	}
	result := s.requestRaw("GET", "/api/v1/versions/"+url.PathEscape(versionID)+"/metadata", query, nil, auth, true)
	if result.werr != nil {
		return nil, result.werr
	}
	if result.status != 200 {
		_, werr := typedResult(result.status, result.body)
		result.close()
		return nil, werr
	}
	etag := result.header.Get("ETag")
	id := randomID()
	if id == "" {
		result.close()
		return nil, networkError()
	}
	s.mu.Lock()
	s.streams[id] = &readerStream{id: id, body: result.response, cancel: result.cancel, auth: auth, versionID: versionID, epoch: epoch}
	s.mu.Unlock()
	return map[string]any{"stream_id": id, "etag": etag}, nil
}

func (s *ReaderService) openStream(versionID, path string, query url.Values, auth authority, withValidator bool) (any, *wireError) {
	// Capture the daemon stream epoch at open time; every pull reverifies it.
	epoch, werr := s.currentEpoch(auth)
	if werr != nil {
		return nil, werr
	}
	result := s.requestRaw("GET", path, query, nil, auth, true)
	if result.werr != nil {
		return nil, result.werr
	}
	if result.status != 200 {
		_, werr := typedResult(result.status, result.body)
		result.close()
		return nil, werr
	}
	id := randomID()
	if id == "" {
		result.close()
		return nil, networkError()
	}
	reply := map[string]any{}
	if withValidator {
		// Same delivered representation only: absent/malformed proof yields
		// no validator and the download adapter fails typed (never a silent
		// unverified save). Archive package streams never claim one.
		if sum, size, ok := parseFileValidator(result.header); ok {
			reply["validator"] = map[string]any{"sha256": sum, "byte_size": size}
		}
	}
	s.mu.Lock()
	s.streams[id] = &readerStream{id: id, body: result.response, cancel: result.cancel, auth: auth, versionID: versionID, epoch: epoch}
	s.mu.Unlock()
	reply["stream_id"] = id
	return reply, nil
}

// currentEpoch reads the liveness poll's server-era metadata. The poll stays
// liveness-only for the shell; its epoch guards the transport here.
func (s *ReaderService) currentEpoch(auth authority) (string, *wireError) {
	query := url.Values{}
	query.Set("transport", "poll")
	result := s.request("GET", "/api/v1/events", query, nil, nil, auth)
	if result.werr != nil {
		return "", result.werr
	}
	value, ok := result.value.(map[string]any)
	if !ok {
		return "", &wireError{Status: 0, Code: "UNCERTAIN_RESPONSE", Retryable: true}
	}
	epoch, _ := value["stream_epoch"].(string)
	if epoch == "" {
		return "", &wireError{Status: 0, Code: "UNCERTAIN_RESPONSE", Retryable: true}
	}
	return epoch, nil
}

// validatePull revalidates the stream's OWN captured attachment era, the live
// daemon authority and stream epoch, and the exact version's visibility
// before bytes are handed to JavaScript on every pull.
func (s *ReaderService) validatePull(stream *readerStream) *wireError {
	s.mu.Lock()
	current := s.attachmentID != "" && s.attachmentID == stream.auth.attachmentID && s.generation == stream.auth.gen
	s.mu.Unlock()
	if !current {
		return &wireError{Status: 409, Code: "STREAM_ATTACHMENT_REPLACED", Retryable: false}
	}
	epoch, werr := s.currentEpoch(stream.auth)
	if werr != nil {
		if werr.Status == 401 {
			// The credential is definitively dead server-side: no prefetched
			// byte may leak or complete cleanly after revocation.
			return &wireError{Status: 401, Code: werr.Code, Retryable: false, Details: werr.Details}
		}
		return werr
	}
	if epoch != stream.epoch {
		// A daemon restart/generation change invalidates prefetched bytes.
		return &wireError{Status: 409, Code: "STREAM_DAEMON_ERA_CHANGED", Retryable: false}
	}
	target := "/api/v1/versions/" + url.PathEscape(stream.versionID)
	if stream.annotationID != "" {
		target = "/api/v1/annotations/" + url.PathEscape(stream.annotationID)
	}
	detail := s.request("GET", target, nil, nil, nil, stream.auth)
	if detail.werr != nil {
		if detail.werr.Status == 404 {
			return &wireError{Status: 404, Code: "STREAM_TARGET_HIDDEN", Retryable: false, Details: map[string]any{"code": detail.werr.Code}}
		}
		return detail.werr
	}
	return nil
}

func (s *ReaderService) streamRead(payload json.RawMessage) (any, *wireError) {
	var in struct {
		StreamID string `json:"stream_id"`
		MaxBytes int64  `json:"max_bytes"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	if in.MaxBytes < 1 || in.MaxBytes > 4<<20 {
		return nil, &wireError{Status: 400, Code: "NATIVE_PAYLOAD_INVALID", Retryable: false}
	}
	s.mu.Lock()
	stream, owned := s.streams[in.StreamID]
	s.mu.Unlock()
	if !owned {
		return nil, &wireError{Status: 404, Code: "STREAM_NOT_FOUND", Retryable: false}
	}
	// Guard the stream's OWN captured attachment era, daemon epoch and exact
	// version visibility before the blocking read.
	if werr := s.validatePull(stream); werr != nil {
		s.dropStream(in.StreamID)
		return nil, werr
	}
	s.mu.Lock()
	stillOwned := s.streams[in.StreamID] == stream
	s.mu.Unlock()
	if !stillOwned {
		return nil, &wireError{Status: 404, Code: "STREAM_NOT_FOUND", Retryable: false}
	}
	buffer := make([]byte, in.MaxBytes)
	total, atEof := 0, false
	for attempts := 0; total == 0 && !atEof && attempts < 4; attempts++ {
		read, err := stream.body.Read(buffer[total:])
		total += read
		if err == io.EOF {
			atEof = true
		} else if err != nil {
			s.dropStream(in.StreamID)
			return nil, networkError()
		}
	}
	if total == 0 && !atEof {
		// A body that stalls without bytes or EOF fails typed, never a fake
		// clean end.
		s.dropStream(in.StreamID)
		return nil, networkError()
	}
	// Revalidate after the awaited read: a revocation, era replacement, daemon
	// restart or hidden target that landed while this pull was blocking must
	// still fail the pull.
	if werr := s.validatePull(stream); werr != nil {
		s.dropStream(in.StreamID)
		return nil, werr
	}
	if total > 0 {
		// At most max_bytes per reply, never an over-read lookahead. A final
		// n>0 read that also reports EOF delivers the bytes together with the
		// clean EOF in one reply.
		if atEof {
			s.dropStream(in.StreamID)
		}
		return map[string]any{"data_b64": base64.StdEncoding.EncodeToString(buffer[:total]), "eof": atEof}, nil
	}
	s.dropStream(in.StreamID)
	return map[string]any{"data_b64": nil, "eof": true}, nil
}

func (s *ReaderService) dropStream(id string) {
	s.mu.Lock()
	stream, owned := s.streams[id]
	if owned {
		delete(s.streams, id)
	}
	s.mu.Unlock()
	if owned {
		stream.cancel()
		stream.body.Close()
	}
}

func (s *ReaderService) subscribeEvents(auth authority) (any, *wireError) {
	query := url.Values{}
	query.Set("transport", "poll")
	result := s.request("GET", "/api/v1/events", query, nil, nil, auth)
	if result.werr != nil {
		return nil, result.werr
	}
	id := randomID()
	if id == "" {
		return nil, networkError()
	}
	s.mu.Lock()
	cursor := ""
	if page, ok := result.value.(map[string]any); ok {
		cursor, _ = page["next_cursor"].(string)
	}
	s.subs[id] = &readerSubscription{id: id, gen: auth.gen, cursor: cursor}
	s.mu.Unlock()
	return map[string]any{"subscription_id": id}, nil
}

func (s *ReaderService) eventsPoll(payload json.RawMessage, auth authority) (any, *wireError) {
	var in struct {
		SubscriptionID string `json:"subscription_id"`
	}
	if werr := decodePayload(payload, &in); werr != nil {
		return nil, werr
	}
	s.mu.Lock()
	subscription, known := s.subs[in.SubscriptionID]
	cursor := ""
	if known {
		cursor = subscription.cursor
	}
	current := s.generation
	s.mu.Unlock()
	if !known {
		return nil, &wireError{Status: 404, Code: "SUBSCRIPTION_NOT_FOUND", Retryable: false}
	}
	if current != auth.gen {
		return map[string]any{"closed": true}, nil
	}
	query := url.Values{}
	query.Set("transport", "poll")
	if epoch, after, ok := strings.Cut(cursor, ":"); ok {
		query.Set("stream_epoch", epoch)
		query.Set("after", after)
	}
	result := s.request("GET", "/api/v1/events", query, nil, nil, auth)
	if result.werr != nil {
		if result.werr.Status == 401 {
			// A definite session death closes the subscription for liveness.
			return map[string]any{"closed": true}, nil
		}
		return nil, result.werr
	}
	// Only a boolean hint crosses the native wire; never event resources/body.
	invalidated := false
	if page, ok := result.value.(map[string]any); ok {
		invalidated = page["resync"] != nil
		if items, ok := page["items"].([]any); ok {
			for _, item := range items {
				if event, ok := item.(map[string]any); ok {
					switch event["type"] {
					case "search.index_updated", "version.published", "library.invalidated", "resource.invalidated", "resync.required", "policy.changed", "document.archived", "document.trashed", "document.restored", "document.purged":
						invalidated = true
					}
				}
			}
		}
		next, _ := page["next_cursor"].(string)
		s.mu.Lock()
		if s.subs[in.SubscriptionID] == subscription && s.generation == auth.gen {
			subscription.cursor = next
		}
		s.mu.Unlock()
	}
	return map[string]any{"closed": false, "invalidated": invalidated}, nil
}

// CloseOwned is the normal window-close path: explicit typed closes for every
// renderer delivery and stream this attachment still owns.
func (s *ReaderService) CloseOwned() {
	s.mu.Lock()
	s.cancelReferenceReadsLocked()
	renders := s.renders
	s.renders = nil
	streams := make([]*readerStream, 0, len(s.streams))
	for _, stream := range s.streams {
		streams = append(streams, stream)
	}
	s.streams = map[string]*readerStream{}
	token := s.token
	endpoint := s.base
	// Window close also drops the owner-selected filesystem handles of the
	// current attachment era: anchored roots close and private destination
	// temps get best-effort own-temp cleanup (never publication), so a late
	// panel result cannot feed a replaced era.
	ownedAttachment := s.attachmentID
	// A picker held across window close must never register afterwards; the
	// epoch invalidates chooser leases without changing session semantics.
	s.fsEpoch++
	var attemptGen uint64
	tracksAttempts := s.onboarding != nil
	if tracksAttempts {
		attemptGen = s.onboarding.AttemptGeneration()
	}
	s.mu.Unlock()
	s.releaseOwnedHandles(ownedAttachment)
	if tracksAttempts {
		// Window close drops any unadopted Go-held candidate of this era
		// before the owned cleanup; called outside the facade mutex.
		s.onboarding.DiscardAttemptUpTo(attemptGen)
	}
	rows := []map[string]any{}
	for _, ref := range renders {
		result := s.request("POST", "/api/v1/versions/"+url.PathEscape(ref.VersionID)+"/render-sessions/"+url.PathEscape(ref.SessionID)+"/close", nil, nil, []byte("{}"), authority{token: token, endpoint: endpoint})
		rows = append(rows, map[string]any{"version_id": ref.VersionID, "session_id": ref.SessionID, "status": result.status})
	}
	for _, stream := range streams {
		stream.cancel()
		stream.body.Close()
	}
	s.receipt("windowClose", map[string]any{"rows": rows, "streams_closed": len(streams)})
}

// SelfcheckReport is registered ONLY in --reader-selfcheck mode: structured
// harness rows, never a generic transport.
func (s *ReaderService) SelfcheckReport(in struct {
	Rows json.RawMessage `json:"Rows"`
}) (map[string]any, error) {
	var rows []map[string]any
	if err := json.Unmarshal(in.Rows, &rows); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(map[string]any{"pid": os.Getpid(), "rows": rows}, "", " ")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(s.state, fmt.Sprintf("native-reader-selfcheck-%d.json", os.Getpid()))
	if err := os.WriteFile(path, append(encoded, '\n'), 0600); err != nil {
		return nil, err
	}
	return map[string]any{"written": true, "count": len(rows)}, nil
}

type rawResult struct {
	status   int
	body     []byte
	header   http.Header
	response io.ReadCloser
	cancel   context.CancelFunc
	werr     *wireError
}

func (r rawResult) close() {
	if r.response != nil {
		r.response.Close()
	}
	if r.cancel != nil {
		r.cancel()
	}
}

// requestRaw either keeps the response body open (stream opens) or reads it
// fully and exposes headers for exact fresh-proof verification.
func (s *ReaderService) requestRaw(method, path string, query url.Values, headers map[string]string, auth authority, keepBody bool) rawResult {
	target := auth.endpoint + path
	if len(query) != 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequest(method, target, nil)
	if err != nil {
		return rawResult{werr: networkError()}
	}
	ctx, cancel := context.WithCancel(context.Background())
	request = request.WithContext(ctx)
	if auth.token != "" {
		request.Header.Set("Authorization", "Bearer "+auth.token)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := s.client.Do(request)
	if err != nil {
		cancel()
		return rawResult{werr: networkError()}
	}
	if keepBody && response.StatusCode == 200 {
		return rawResult{status: response.StatusCode, header: response.Header, response: response.Body, cancel: cancel}
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	cancel()
	if err != nil {
		return rawResult{status: response.StatusCode, header: response.Header, werr: networkError()}
	}
	return rawResult{status: response.StatusCode, body: data, header: response.Header}
}

func jsonBody(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}
func plain(result outcome) (any, *wireError) { return result.value, result.werr }

// decision maps the canonical 202 reservation like the browser client.
func decision(result outcome) (any, *wireError) {
	if result.werr != nil {
		return nil, result.werr
	}
	kind := "committed"
	if result.status == 202 {
		kind = "accepted"
	}
	return map[string]any{"kind": kind, "body": result.value}, nil
}
func collection(params map[string]string) url.Values {
	query := url.Values{}
	for key, value := range params {
		if value != "" {
			query.Set(key, value)
		}
	}
	return query
}
