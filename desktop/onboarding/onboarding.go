// Package onboarding owns the native desktop companion's authenticated local
// service discovery, the explicit sibling-daemon start offer, and the Go-held
// owner pairing attempt with delayed explicit adoption. The polling secret and
// the redeemed bearer never cross the wire to JavaScript: the facade exposes
// only pairing ids, states and adoption outcomes.
//
// Binding policy: one exact Selection (default profile only) is preserved
// through discovery, verification, the start-offer argv, the credential
// namespace (one protected credential file per selection digest) and the
// remembered boot. No PATH search, no auto-install, no auto-init, no
// migration and no daemon stop ever happen here.
//
// Concurrency: the single pairing attempt slot carries a generation counter.
// Adopt/Poll validate the captured attempt, do their async work, and then
// re-validate ownership under the mutex; the commit (credential save plus
// readback classification and the adopted transition) happens inside one
// continuous lock hold shared with Abandon and CreatePairing, so an abandon
// can never return a clean stop for an adoption that actually committed, and
// a stale adopt can never persist or activate. Failure bookkeeping mutates
// only the captured (still-current) attempt, never a replacement.
package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/localcontrol"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
)

// Error is the typed onboarding failure the facade maps onto its wire envelope.
// RetryAfter carries the daemon's real Retry-After seconds for known
// non-consuming rate limits (the poll-too-soon 429) so the trusted UI can
// schedule instead of terminating the wait.
type Error struct {
	Status     int
	Code       string
	Retryable  bool
	RetryAfter float64
	Details    map[string]any
}

func (e *Error) Error() string {
	return fmt.Sprintf("native onboarding failed: HTTP %d %s", e.Status, e.Code)
}

func errf(status int, code string, retryable bool, details map[string]any) *Error {
	return &Error{Status: status, Code: code, Retryable: retryable, Details: details}
}

// Discovery states reported to the trusted UI.
const (
	StateVerified         = "verified"
	StateUninitialized    = "uninitialized"
	StateDaemonStopped    = "daemon_stopped"
	StateDiscoveryCorrupt = "discovery_corrupt"
	StateIdentityMismatch = "identity_mismatch"
	StateProfileError     = "profile_error"
)

// Selection is the single exact service selection. Profile must stay default:
// non-default profiles are outside the companion v1 scope and fail closed.
type Selection struct {
	Profile    string
	DataDir    string
	VolumeRoot string
	Port       int // 0 keeps the daemon's configured listener port
}

func (s Selection) digest() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d", s.Profile, s.DataDir, s.VolumeRoot, s.Port)))
	return hex.EncodeToString(sum[:])[:16]
}

func (s Selection) toService() service.Selection {
	return service.Selection{Profile: s.Profile, DataDir: s.DataDir, VolumeRoot: s.VolumeRoot}
}

// Config wires one companion instance. Endpoint and ExpectedLibrary are a
// fixed binding used by fixtures/tests; production leaves them empty and the
// values are derived from signed discovery at runtime.
type Config struct {
	Selection       Selection
	CredentialDir   string // protected 0700 directory for the remembered binding
	ProductAPI      string // required product API version, e.g. "1.0"
	DaemonBinary    string // absolute sibling path; empty disables the start offer
	Endpoint        string
	ExpectedLibrary string
}

type DiscoveryResult struct {
	State      string `json:"state"`
	Profile    string `json:"profile"`
	Endpoint   string `json:"endpoint,omitempty"`
	LibraryID  string `json:"library_id,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

type StartResult struct {
	Started   bool            `json:"started"`
	Discovery DiscoveryResult `json:"discovery"`
}

// Attempt states: pending → adopting (adoption in flight) → adopted | failed |
// store_failed | store_uncertain. Only "adopting" transitions to "adopted"
// inside the commit lock hold; Abandon may interleave by failing a captured
// "adopting" attempt before its commit, which forces the stale path.
type attempt struct {
	gen           uint64
	pairingID     string
	endpoint      string
	library       string
	pollingSecret string
	candidate     string
	session       map[string]any
	state         string
	detail        string
}

// Onboarding is safe for concurrent use; the pairing attempt slot and the
// discovered binding are guarded by one mutex.
type Onboarding struct {
	cfg  Config
	http *http.Client

	mu           sync.Mutex
	attempt      *attempt
	attemptGen   uint64
	committedGen uint64
	endpoint     string
	library      string

	// actMu serializes generation-checked activation callbacks so a stale
	// post-commit activation can never overwrite a newer committed adoption.
	// It is only ever acquired before mu, never the other way around.
	actMu sync.Mutex

	// testBeforeCommit pauses Adopt between its async validation and the
	// commit lock hold; testAfterCommit pauses between the commit and the
	// activation callback. Same-package barrier tests only.
	testBeforeCommit   func()
	testAfterCommit    func()
	testSaveCredential func(string, client.Credential) error
	testLoadCredential func(string) (client.Credential, error)
}

// New validates the selection policy and returns an offline onboarding
// instance. Discovery runs lazily; nothing here starts or installs anything.
func New(cfg Config) (*Onboarding, error) {
	if cfg.Selection.Profile == "" {
		cfg.Selection.Profile = "default"
	}
	if cfg.Selection.Profile != "default" {
		return nil, fmt.Errorf("non-default profiles are outside the companion v1 scope: %s", cfg.Selection.Profile)
	}
	if cfg.CredentialDir == "" {
		return nil, fmt.Errorf("credential directory required")
	}
	if cfg.ProductAPI == "" {
		return nil, fmt.Errorf("product API version required")
	}
	return &Onboarding{
		cfg: cfg,
		http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("onboarding redirect rejected")
		}},
	}, nil
}

// ProductAPI is the product API version every effective authority requires.
func (o *Onboarding) ProductAPI() string { return o.cfg.ProductAPI }

// ExpectedLibrary is the signed-discovery library this selection binds to.
func (o *Onboarding) ExpectedLibrary() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.libraryLocked()
}

func (o *Onboarding) libraryLocked() string {
	if o.library != "" {
		return o.library
	}
	return o.cfg.ExpectedLibrary
}

// CredentialPath is the selection-namespaced protected credential file.
func (o *Onboarding) CredentialPath() string {
	return filepath.Join(o.cfg.CredentialDir, "credential-"+o.cfg.Selection.digest()+".json")
}

func transportUnavailable(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// Discover classifies the exact selected service without mutating anything.
// A missing discovery record or an unreachable listener is an honest stopped
// daemon (the explicit start offer stays reachable); a malformed, foreign or
// signature-inconsistent record or reply fails closed as corruption or an
// identity mismatch and never triggers a spawn.
func (o *Onboarding) Discover(ctx context.Context) DiscoveryResult {
	profile := o.cfg.Selection.Profile
	loaded, err := service.Load(o.cfg.Selection.toService())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DiscoveryResult{State: StateUninitialized, Profile: profile, Detail: err.Error()}
		}
		return DiscoveryResult{State: StateProfileError, Profile: profile, Detail: err.Error()}
	}
	discovered, err := service.ReadDiscovery(loaded)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DiscoveryResult{State: StateDaemonStopped, Profile: profile, Detail: err.Error()}
		}
		return DiscoveryResult{State: StateDiscoveryCorrupt, Profile: profile, Detail: err.Error()}
	}
	actual, err := localcontrol.Verify(ctx, loaded, discovered)
	if err != nil {
		if transportUnavailable(err) {
			return DiscoveryResult{State: StateDaemonStopped, Profile: profile, Detail: "stale discovery record; listener unavailable: " + err.Error()}
		}
		return DiscoveryResult{State: StateIdentityMismatch, Profile: profile, Detail: err.Error()}
	}
	o.mu.Lock()
	o.endpoint, o.library = actual.Endpoint, actual.LibraryID
	o.mu.Unlock()
	return DiscoveryResult{State: StateVerified, Profile: profile, Endpoint: actual.Endpoint, LibraryID: actual.LibraryID, InstanceID: actual.InstanceID}
}

// buildDaemonArgv returns the exact detached daemon argv for the selection.
// The binary must already be absolute; no PATH search ever happens.
func buildDaemonArgv(binary string, selection Selection) []string {
	argv := []string{binary, "daemon", "run", "--profile", selection.Profile}
	if selection.DataDir != "" {
		argv = append(argv, "--data-dir", selection.DataDir)
	}
	if selection.VolumeRoot != "" {
		argv = append(argv, "--volume-root", selection.VolumeRoot)
	}
	if selection.Port != 0 {
		argv = append(argv, "--port", fmt.Sprintf("%d", selection.Port))
	}
	return append(argv, "--json", "--no-input")
}

// StartDaemonOffer performs the explicit owner-requested start: it refuses to
// spawn unless discovery currently reports daemon_stopped, launches the exact
// sibling binary detached (asynchronously reaped; the daemon owns its own
// lifetime and is never stopped or killed from here), and polls signed
// discovery until it verifies.
func (o *Onboarding) StartDaemonOffer(ctx context.Context) (StartResult, *Error) {
	current := o.Discover(ctx)
	if current.State != StateDaemonStopped {
		// verified, corrupt, mismatched and uninitialized states never spawn.
		return StartResult{Started: false, Discovery: current}, nil
	}
	if o.cfg.DaemonBinary == "" {
		return StartResult{}, errf(503, "NATIVE_START_OFFER_UNAVAILABLE", false, map[string]any{"state": current.State})
	}
	if !filepath.IsAbs(o.cfg.DaemonBinary) {
		return StartResult{}, errf(500, "NATIVE_START_OFFER_MISCONFIGURED", false, nil)
	}
	argv := buildDaemonArgv(o.cfg.DaemonBinary, o.cfg.Selection)
	command := exec.Command(argv[0], argv[1:]...)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return StartResult{}, errf(500, "NATIVE_START_SPAWN_FAILED", false, map[string]any{"io_error": err.Error()})
	}
	defer devNull.Close()
	command.Stdin, command.Stdout, command.Stderr = devNull, devNull, devNull
	if err := command.Start(); err != nil {
		return StartResult{}, errf(500, "NATIVE_START_SPAWN_FAILED", false, map[string]any{"io_error": err.Error()})
	}
	// Reap asynchronously so a daemon exit never leaves a zombie attached to
	// the companion; waiting does not couple the daemon's lifetime to ours.
	go func() { _ = command.Wait() }()
	deadline := time.Now().Add(20 * time.Second)
	for {
		next := o.Discover(ctx)
		if next.State == StateVerified {
			return StartResult{Started: true, Discovery: next}, nil
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			next.Detail = fmt.Sprintf("daemon did not become verifiable: %s", next.Detail)
			return StartResult{Started: true, Discovery: next}, nil
		}
		select {
		case <-ctx.Done():
			next.Detail = fmt.Sprintf("daemon did not become verifiable: %s", next.Detail)
			return StartResult{Started: true, Discovery: next}, nil
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// call performs one fixed JSON request against the verified daemon endpoint,
// mirroring the CLI pairing transport (Origin header, redirect rejection and
// the shared envelope).
func (o *Onboarding) call(ctx context.Context, endpoint, method, path, bearer string, body any, out any) *Error {
	if endpoint == "" {
		return errf(503, "NATIVE_DAEMON_UNAVAILABLE", true, nil)
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return errf(500, "NATIVE_ONBOARDING_REQUEST_INVALID", false, map[string]any{"io_error": err.Error()})
		}
		reader = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint+path, reader)
	if err != nil {
		return errf(500, "NATIVE_ONBOARDING_REQUEST_INVALID", false, map[string]any{"io_error": err.Error()})
	}
	request.Header.Set("Origin", endpoint)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := o.http.Do(request)
	if err != nil {
		return errf(0, "NATIVE_ONBOARDING_TRANSPORT", true, map[string]any{"io_error": err.Error()})
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return errf(response.StatusCode, "NATIVE_PAIRING_POLL_UNAVAILABLE", true, map[string]any{"io_error": err.Error()})
	}
	if response.StatusCode >= 400 {
		var failure struct {
			Error struct {
				Code      string         `json:"code"`
				Retryable *bool          `json:"retryable"`
				Details   map[string]any `json:"details"`
			} `json:"error"`
		}
		retryable := false
		code := "NATIVE_ONBOARDING_HTTP"
		details := map[string]any{}
		if json.Unmarshal(data, &failure) == nil && failure.Error.Code != "" {
			code = failure.Error.Code
			if failure.Error.Retryable != nil {
				retryable = *failure.Error.Retryable
			}
			details = failure.Error.Details
		}
		werr := errf(response.StatusCode, code, retryable, details)
		// Retry-After is a real HTTP header (not an error-body field); carry
		// its seconds so known non-consuming rate limits stay schedulable.
		if seconds, parseErr := strconv.ParseFloat(response.Header.Get("Retry-After"), 64); parseErr == nil && seconds >= 0 {
			werr.RetryAfter = seconds
		}
		return werr
	}
	if out == nil {
		return nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || len(envelope.Data) == 0 {
		return errf(response.StatusCode, "NATIVE_PAIRING_POLL_UNAVAILABLE", true, nil)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return errf(response.StatusCode, "NATIVE_PAIRING_POLL_UNAVAILABLE", true, map[string]any{"io_error": err.Error()})
	}
	return nil
}

// ensureBinding resolves the endpoint/library pin, preferring signed
// discovery unless a fixed fixture binding is configured.
func (o *Onboarding) ensureBinding(ctx context.Context) *Error {
	if o.cfg.Endpoint != "" {
		o.mu.Lock()
		if o.endpoint == "" {
			o.endpoint = o.cfg.Endpoint
		}
		o.mu.Unlock()
		return nil
	}
	result := o.Discover(ctx)
	if result.State != StateVerified {
		return errf(503, "NATIVE_DAEMON_UNAVAILABLE", result.State == StateDaemonStopped, map[string]any{"state": result.State, "detail": result.Detail})
	}
	return nil
}

// bindingEndpoint returns the endpoint the remembered credential must match.
// In production this is always derived from a verified binding; without one
// the boot cannot silently skip endpoint validation.
func (o *Onboarding) bindingEndpoint(ctx context.Context) (string, *Error) {
	if o.cfg.Endpoint != "" {
		return o.cfg.Endpoint, nil
	}
	result := o.Discover(ctx)
	if result.State != StateVerified {
		return "", errf(503, "NATIVE_DAEMON_UNAVAILABLE", result.State == StateDaemonStopped, map[string]any{"state": result.State, "detail": result.Detail})
	}
	return result.Endpoint, nil
}

// capturedStillCurrent reports whether the captured attempt is still the
// current slot in exactly the expected state.
func (o *Onboarding) capturedStillCurrent(at *attempt, state string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.attempt == at && at.state == state
}

// failCaptured records a failure on the captured attempt only while it is
// still the current slot in the SAME phase the failing operation captured. It
// never erases an already-held redemption (an overlapping poll's success) and
// never touches a committed adoption, so a late failure cannot turn a
// committed binding into a false ADOPT_FAILED.
func (o *Onboarding) failCaptured(at *attempt, expected string, detail string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.attempt != at || at.state != expected {
		return
	}
	if expected == "pending" && at.candidate != "" {
		return
	}
	at.state = "failed"
	at.detail = detail
	at.candidate = ""
	at.pollingSecret = ""
}

// CreatePairing starts the single pending attempt, replacing any previous
// one (its held candidate is discarded Go-side). The request generation is
// reserved BEFORE the async create so a slower older request can never
// publish over a newer intent. The polling secret stays in Go memory;
// JavaScript receives only the comparison-facing reply.
func (o *Onboarding) CreatePairing(ctx context.Context, deviceLabel string) (map[string]any, *Error) {
	o.mu.Lock()
	o.attemptGen++
	gen := o.attemptGen
	o.attempt = nil
	o.mu.Unlock()
	if err := o.ensureBinding(ctx); err != nil {
		return nil, err
	}
	o.mu.Lock()
	if o.attemptGen != gen {
		o.mu.Unlock()
		return nil, errf(409, "NATIVE_PAIRING_ATTEMPT_STALE", false, nil)
	}
	endpoint, library := o.endpoint, o.libraryLocked()
	o.mu.Unlock()
	var created domain.PairingCreated
	payload := map[string]string{"device_label": deviceLabel}
	if err := o.call(ctx, endpoint, http.MethodPost, "/api/v1/auth/pairings", "", payload, &created); err != nil {
		return nil, err
	}
	if created.ID == "" || created.PollingSecret == "" {
		return nil, errf(502, "NATIVE_PAIRING_ISSUANCE_INCOMPLETE", true, nil)
	}
	o.mu.Lock()
	if o.attemptGen != gen || o.endpoint != endpoint || o.libraryLocked() != library {
		o.mu.Unlock()
		// A newer pairing request already reserved the slot: the older
		// response must not replace the newer intent.
		return nil, errf(409, "NATIVE_PAIRING_ATTEMPT_STALE", false, nil)
	}
	o.attempt = &attempt{gen: gen, pairingID: created.ID, endpoint: endpoint, library: library, pollingSecret: created.PollingSecret, state: "pending"}
	o.mu.Unlock()
	return map[string]any{
		"pairing_id": created.ID,
		"code":       created.Code,
		"expires_at": created.ExpiresAt.UTC().Format(time.RFC3339),
	}, nil
}

// PollPairing drives the single-use redemption Go-side. A repeat poll while
// a candidate is already held is idempotent (no second server redemption).
// On approval the consumed bearer becomes the held candidate — first writer
// wins — and the reply NEVER carries it. The captured attempt is revalidated
// after the async request so a stop, replacement, adoption or overlapping
// poll racing this poll can never redeem into or erase authority.
func (o *Onboarding) PollPairing(ctx context.Context, pairingID string) (map[string]any, *Error) {
	o.mu.Lock()
	at := o.attempt
	if at == nil || at.pairingID != pairingID || at.state != "pending" {
		o.mu.Unlock()
		return nil, errf(409, "NATIVE_PAIRING_ATTEMPT_STALE", false, nil)
	}
	if at.candidate != "" {
		session := at.session
		o.mu.Unlock()
		return map[string]any{"state": "approved", "session": session}, nil
	}
	secret := at.pollingSecret
	o.mu.Unlock()
	var polled domain.PairingPoll
	body := map[string]string{"polling_secret": secret}
	if err := o.call(ctx, at.endpoint, http.MethodPost, "/api/v1/auth/pairings/"+at.pairingID+"/poll", "", body, &polled); err != nil {
		// A known non-consuming poll-too-soon 429 keeps the pending attempt
		// intact: the daemon rejects the poll BEFORE its redemption path, so
		// the trusted UI must reschedule using the carried Retry-After. Every
		// other failure leaves the single-use redemption uncertain and stays
		// new-pairing-only; the failure is recorded only if the attempt still
		// owns the slot, is still pending and holds no candidate.
		if err.Status != http.StatusTooManyRequests {
			o.failCaptured(at, "pending", "poll transport failure")
		}
		return nil, err
	}
	if !o.capturedStillCurrent(at, "pending") {
		return nil, errf(409, "NATIVE_PAIRING_ATTEMPT_STALE", false, nil)
	}
	if polled.Token == "" {
		return map[string]any{"state": polled.State}, nil
	}
	if polled.Session == nil || polled.Session.LibraryID == "" {
		o.failCaptured(at, "pending", "redemption outcome incomplete")
		return nil, errf(502, "NATIVE_PAIRING_ISSUANCE_INCOMPLETE", false, nil)
	}
	if expected := at.library; expected != "" && polled.Session.LibraryID != expected {
		o.failCaptured(at, "pending", "library mismatch")
		return nil, errf(409, "NATIVE_LIBRARY_MISMATCH", false, map[string]any{"session_library": polled.Session.LibraryID, "expected_library": expected})
	}
	session := map[string]any{
		"session_id":   polled.Session.ID,
		"principal_id": polled.Session.PrincipalID,
		"library_id":   polled.Session.LibraryID,
		"device_label": polled.Session.DeviceLabel,
	}
	o.mu.Lock()
	if o.attempt != at || at.state != "pending" {
		o.mu.Unlock()
		return nil, errf(409, "NATIVE_PAIRING_ATTEMPT_STALE", false, nil)
	}
	if at.candidate == "" {
		at.candidate = polled.Token
		at.session = session
	}
	effective := at.session
	o.mu.Unlock()
	return map[string]any{"state": "approved", "session": effective}, nil
}

// storeOutcome classifies a SaveCredential failure through an exact-candidate
// readback: rename-then-sync failures stay committed (durability unconfirmed),
// a missing file is a definitive pre-rename failure, and an unreadable
// readback is honestly unknown.
type storeOutcome int

const (
	storeCommitted storeOutcome = iota
	storeDurabilityUnconfirmed
	storeFailed
	storeUncertain
)

func classifyStore(saveErr error, loaded client.Credential, loadErr error, want client.Credential) storeOutcome {
	if saveErr == nil {
		return storeCommitted
	}
	switch {
	case loadErr == nil && loaded == want:
		return storeDurabilityUnconfirmed
	case errors.Is(loadErr, os.ErrNotExist):
		return storeFailed
	default:
		return storeUncertain
	}
}

// Adopt linearizes the explicit adoption. Phase A validates and reserves the
// captured attempt ("adopting"); phase B performs the async product API and
// library checks; phase C revalidates ownership and then holds the mutex
// across the credential save, readback classification and the adopted
// transition, so Abandon and CreatePairing can never interleave between the
// ownership check and the commit. Activation happens after the commit with
// the EXACT captured credential (endpoint and bearer together), and a stale
// adopt never persists or activates anything.
func (o *Onboarding) Adopt(ctx context.Context, pairingID string, activate func(credential client.Credential, attemptGen uint64)) (map[string]any, *Error) {
	o.mu.Lock()
	at := o.attempt
	if at == nil || at.pairingID != pairingID {
		o.mu.Unlock()
		return nil, errf(409, "NATIVE_PAIRING_ATTEMPT_STALE", false, nil)
	}
	if at.state == "adopted" {
		o.mu.Unlock()
		return nil, errf(409, "NATIVE_PAIRING_ALREADY_ADOPTED", false, nil)
	}
	if at.state != "pending" || at.candidate == "" {
		state, detail := at.state, at.detail
		o.mu.Unlock()
		return nil, errf(409, "NATIVE_PAIRING_ADOPT_FAILED", false, map[string]any{"reason": state, "detail": detail})
	}
	if o.endpoint != at.endpoint || o.libraryLocked() != at.library {
		at.state, at.detail, at.candidate = "failed", "service binding changed", ""
		o.mu.Unlock()
		return nil, errf(409, "NATIVE_CREDENTIAL_MISMATCHED", false, nil)
	}
	at.state = "adopting"
	candidate := at.candidate
	o.mu.Unlock()

	var session domain.Session
	if err := o.call(ctx, at.endpoint, http.MethodGet, "/api/v1/session", candidate, nil, &session); err != nil {
		o.failCaptured(at, "adopting", "session verification failed")
		return nil, err
	}
	var caps struct {
		LibraryID  string `json:"library_id"`
		APIVersion string `json:"api_version"`
	}
	if err := o.call(ctx, at.endpoint, http.MethodGet, "/api/v1/capabilities", candidate, nil, &caps); err != nil {
		o.failCaptured(at, "adopting", "capability verification failed")
		return nil, err
	}
	expected := at.library
	switch {
	case caps.APIVersion != o.cfg.ProductAPI:
		o.failCaptured(at, "adopting", "api incompatible")
		return nil, errf(409, "NATIVE_API_INCOMPATIBLE", false, map[string]any{"api_version": caps.APIVersion, "expected": o.cfg.ProductAPI})
	case session.LibraryID != caps.LibraryID || (expected != "" && caps.LibraryID != expected):
		o.failCaptured(at, "adopting", "library mismatch")
		return nil, errf(409, "NATIVE_LIBRARY_MISMATCH", false, map[string]any{"session_library": session.LibraryID, "caps_library": caps.LibraryID, "expected_library": expected})
	}

	if o.testBeforeCommit != nil {
		o.testBeforeCommit()
	}

	credential := client.Credential{Endpoint: at.endpoint, Profile: o.cfg.Selection.Profile, LibraryID: caps.LibraryID, Token: candidate}
	if err := os.MkdirAll(o.cfg.CredentialDir, 0700); err != nil {
		o.failCaptured(at, "adopting", "credential directory unavailable")
		return nil, errf(500, "NATIVE_CREDENTIAL_STORE_FAILED", false, map[string]any{"io_error": err.Error()})
	}
	if err := os.Chmod(o.cfg.CredentialDir, 0700); err != nil {
		o.failCaptured(at, "adopting", "credential directory unavailable")
		return nil, errf(500, "NATIVE_CREDENTIAL_STORE_FAILED", false, map[string]any{"io_error": err.Error()})
	}
	// Commit: the ownership recheck, the credential save, the exact-candidate
	// readback classification and the adopted transition share ONE continuous
	// lock hold with Abandon/CreatePairing, so a stop can never return a
	// clean abandoned result for an adoption that actually commits here.
	result, werr := func() (map[string]any, *Error) {
		o.mu.Lock()
		defer o.mu.Unlock()
		if o.attempt != at || at.state != "adopting" {
			return nil, errf(409, "NATIVE_PAIRING_ATTEMPT_STALE", false, nil)
		}
		if o.endpoint != at.endpoint || o.libraryLocked() != at.library {
			at.state, at.detail, at.candidate = "failed", "service binding changed", ""
			return nil, errf(409, "NATIVE_CREDENTIAL_MISMATCHED", false, nil)
		}
		save, load := client.SaveCredential, client.LoadCredential
		if o.testSaveCredential != nil {
			save = o.testSaveCredential
		}
		if o.testLoadCredential != nil {
			load = o.testLoadCredential
		}
		saveErr := save(o.CredentialPath(), credential)
		loaded, loadErr := load(o.CredentialPath())
		switch classifyStore(saveErr, loaded, loadErr, credential) {
		case storeCommitted, storeDurabilityUnconfirmed:
			at.state = "adopted"
			at.pollingSecret = ""
			o.committedGen = at.gen
			durability := "confirmed"
			if saveErr != nil {
				durability = "unconfirmed"
			}
			return map[string]any{"adopted": true, "durability": durability}, nil
		case storeFailed:
			at.state, at.detail = "store_failed", saveErr.Error()
			return nil, errf(500, "NATIVE_CREDENTIAL_STORE_FAILED", false, map[string]any{"io_error": saveErr.Error()})
		default:
			details := map[string]any{"io_error": saveErr.Error()}
			if loadErr != nil {
				details["readback_error"] = loadErr.Error()
			}
			at.state, at.detail = "store_uncertain", saveErr.Error()
			return nil, errf(500, "NATIVE_CREDENTIAL_STORE_UNCERTAIN", true, details)
		}
	}()
	if werr != nil {
		return nil, werr
	}
	if o.testAfterCommit != nil {
		o.testAfterCommit()
	}
	// Activation is serialized and generation-checked: actMu orders every
	// activation callback, and the committed generation is revalidated under
	// mu inside the same actMu hold, so a paused older activation can never
	// install its bearer after a newer adoption committed and activated. The
	// facade-side callback runs without holding mu (no lock inversion).
	o.actMu.Lock()
	o.mu.Lock()
	current := o.attempt == at && at.state == "adopted" && o.committedGen == at.gen
	o.mu.Unlock()
	if current {
		// The exact CAPTURED credential (endpoint and bearer together, as
		// validated at commit) and the attempt generation reach the
		// activation; the facade never recomposes it from later discovery
		// state and can distinguish pre-logout from fresh attempts.
		activate(credential, at.gen)
	}
	o.actMu.Unlock()
	return result, nil
}

// Abandon is the local Stop-waiting cancellation. It never revokes the server
// request and never deletes a newer valid binding; its result distinguishes a
// real stop from an adoption that already committed, failed, or is committing
// right now (a commit in progress blocks until it resolves).
func (o *Onboarding) Abandon(pairingID string) (map[string]any, *Error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	at := o.attempt
	if at == nil || at.pairingID != pairingID {
		return nil, errf(409, "NATIVE_PAIRING_ATTEMPT_STALE", false, nil)
	}
	switch at.state {
	case "pending":
		o.attempt = nil
		return map[string]any{"abandoned": true}, nil
	case "adopting":
		// The adoption has not committed (the commit holds this same mutex);
		// failing the captured attempt forces its stale path, so nothing can
		// be persisted or activated after this truthful stop.
		at.state = "failed"
		at.detail = "stopped during adoption"
		at.candidate = ""
		at.pollingSecret = ""
		o.attempt = nil
		return map[string]any{"abandoned": true}, nil
	case "adopted":
		return nil, errf(409, "NATIVE_PAIRING_ALREADY_ADOPTED", false, nil)
	default:
		return nil, errf(409, "NATIVE_PAIRING_ADOPT_FAILED", false, map[string]any{"reason": at.state, "detail": at.detail})
	}
}

// AttemptGeneration returns the current pairing attempt generation.
func (o *Onboarding) AttemptGeneration() uint64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.attemptGen
}

// DiscardAttemptUpTo quiesces activation and discards the current attempt
// only when it belongs to a generation at or before the captured one. A
// newer intent (created after the capturing transition began) survives, so
// a stale disposal or logout can never cancel a fresh relogin attempt.
func (o *Onboarding) DiscardAttemptUpTo(gen uint64) bool {
	o.actMu.Lock()
	defer o.actMu.Unlock()
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.attempt != nil && o.attempt.gen > gen {
		return false
	}
	o.attemptGen++
	o.attempt = nil
	return true
}

// DiscardAttempt drops any held candidate (window close, disposal, restart).
func (o *Onboarding) DiscardAttempt() {
	// Quiesce activation before the caller clears its own attachment state;
	// callbacks run without mu and may use the facade's separate mutex.
	o.actMu.Lock()
	defer o.actMu.Unlock()
	o.mu.Lock()
	o.attemptGen++
	o.attempt = nil
	o.mu.Unlock()
}

// ResolveBinding resolves the exact endpoint/library binding for authority
// gating: a fixed fixture binding resolves directly, production resolves
// through signed discovery. It fails typed when no verified binding exists.
func (o *Onboarding) ResolveBinding(ctx context.Context) (string, string, *Error) {
	if err := o.ensureBinding(ctx); err != nil {
		return "", "", err
	}
	endpoint, library := o.BindingSnapshot()
	return endpoint, library, nil
}

// BindingSnapshot returns the current runtime endpoint/library binding. The
// facade validates its captured endpoint against it BEFORE any bearer leaves
// the process, so a mismatched listener can never receive the credential.
func (o *Onboarding) BindingSnapshot() (endpoint, library string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.endpoint, o.libraryLocked()
}

// RememberedCredential loads the selection's remembered binding for boot as
// the exact endpoint+bearer pair. found=false is the honest empty boot. The
// VERIFIED binding is resolved FIRST (which also populates the signed
// library pin in production), and only then the stored profile, library and
// normalized endpoint are compared together, so a credential from a
// different library or listener can never silently attach. Without a
// verified binding the boot fails typed instead of skipping validation.
// The endpoint travels WITH the bearer: callers install the pair together
// and never recompose the base from later discovery state (M4-A2).
func (o *Onboarding) RememberedCredential(ctx context.Context) (client.Credential, bool, *Error) {
	endpoint, werr := o.bindingEndpoint(ctx)
	if werr != nil {
		return client.Credential{}, false, werr
	}
	credential, err := client.LoadCredential(o.CredentialPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return client.Credential{}, false, nil
		}
		return client.Credential{}, false, errf(500, "NATIVE_CREDENTIAL_UNREADABLE", true, map[string]any{"io_error": err.Error()})
	}
	if credential.Profile != o.cfg.Selection.Profile {
		return client.Credential{}, false, errf(409, "NATIVE_CREDENTIAL_MISMATCHED", false, map[string]any{"profile": credential.Profile})
	}
	if expected := o.ExpectedLibrary(); expected != "" && credential.LibraryID != expected {
		return client.Credential{}, false, errf(409, "NATIVE_CREDENTIAL_MISMATCHED", false, map[string]any{"library_id": credential.LibraryID, "expected_library": expected})
	}
	stored, err := client.Origin(credential.Endpoint)
	if err != nil || stored != endpoint {
		return client.Credential{}, false, errf(409, "NATIVE_CREDENTIAL_MISMATCHED", false, map[string]any{"stored_endpoint": credential.Endpoint, "binding_endpoint": endpoint})
	}
	return credential, true, nil
}

// RememberedToken loads the selection's remembered binding for boot. Absence
// is an honest empty boot. The VERIFIED binding is resolved FIRST (which also
// populates the signed library pin in production), and only then the stored
// profile, library and normalized endpoint are compared together, so a
// credential from a different library or listener can never silently attach.
// Without a verified binding the boot fails typed instead of skipping
// validation.
func (o *Onboarding) RememberedToken(ctx context.Context) (string, *Error) {
	credential, found, werr := o.RememberedCredential(ctx)
	if werr != nil || !found {
		return "", werr
	}
	return credential.Token, nil
}

// ForgetOutcome reports the local-forget decision for the logout bearer.
type ForgetOutcome int

const (
	ForgetForgotten ForgetOutcome = iota
	ForgetForeign
	ForgetFailed
)

// Forget removes the remembered binding only when it still holds exactly the
// forgotten bearer. A different or unreadable binding is never unlinked.
func (o *Onboarding) Forget(token string) (ForgetOutcome, error) {
	// Serialize unlink with this companion's atomic adoption saves. A logout
	// of an older bearer must never remove a newer remembered binding.
	o.mu.Lock()
	defer o.mu.Unlock()
	credential, err := client.LoadCredential(o.CredentialPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ForgetForgotten, nil
		}
		return ForgetFailed, err
	}
	if credential.Token != token {
		return ForgetForeign, nil
	}
	if err := os.Remove(o.CredentialPath()); err != nil && !os.IsNotExist(err) {
		return ForgetFailed, err
	}
	return ForgetForgotten, nil
}
