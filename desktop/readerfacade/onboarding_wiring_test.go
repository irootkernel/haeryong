package readerfacade

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"haeryong.local/haeryong/desktop/onboarding"
	"haeryong.local/haeryong/internal/adapters/client"
)

// fakeOnboardingDaemon serves the pairing/session/capability routes the
// onboarding ops consume, with scriptable rate limiting, session stalls and
// a revocable session route.
type fakeOnboardingDaemon struct {
	mu             sync.Mutex
	server         *httptest.Server
	approved       bool
	redemptions    int
	rateLimitFirst bool
	apiVersion     string
	capsLibrary    string
	sessionLibrary string
	failSession    bool
	// sessionUnauthorized makes GET /api/v1/session answer the typed 401
	// envelope (a server-side revoked remembered session).
	sessionUnauthorized bool
	// pairingCreates counts POST /api/v1/auth/pairings arrivals.
	pairingCreates int
	sessionStall   func()
	deleteSession  bool
	pollRequests   int
	approvedPolls  int
	seen           []string
}

func redeemBearer(n int) string { return fmt.Sprintf("%064x", n) }

func newFakeOnboardingDaemon(t *testing.T) *fakeOnboardingDaemon {
	t.Helper()
	daemon := &fakeOnboardingDaemon{apiVersion: "1.0", capsLibrary: "lib-1", sessionLibrary: "lib-1", rateLimitFirst: true}
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"data": value})
	}
	fail := func(w http.ResponseWriter, status int, code string, retryable bool) {
		w.Header().Set("Content-Type", "application/json")
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "2")
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "retryable": retryable}})
	}
	mux.HandleFunc("/api/v1/auth/pairings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(404)
			return
		}
		daemon.mu.Lock()
		daemon.pairingCreates++
		daemon.mu.Unlock()
		reply(w, 201, map[string]any{"pairing_id": "pr-9", "code": "654321", "polling_secret": "go-held", "expires_at": "2026-10-19T00:00:00Z"})
	})
	mux.HandleFunc("/api/v1/auth/pairings/pr-9/poll", func(w http.ResponseWriter, r *http.Request) {
		daemon.mu.Lock()
		daemon.pollRequests++
		approved := daemon.approved
		sessionLibrary := daemon.sessionLibrary
		limited := false
		if approved {
			daemon.approvedPolls++
			// The too-soon gate applies to the redemption-eligible poll: the
			// daemon rejects it BEFORE its consuming path.
			limited = daemon.rateLimitFirst && daemon.approvedPolls == 1
		}
		daemon.mu.Unlock()
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["polling_secret"] != "go-held" {
			fail(w, 403, "POLL_SECRET_INVALID", false)
			return
		}
		if !approved {
			reply(w, 200, map[string]any{"state": "pending"})
			return
		}
		if limited {
			fail(w, http.StatusTooManyRequests, "RATE_LIMITED", true)
			return
		}
		daemon.mu.Lock()
		daemon.redemptions++
		token := redeemBearer(daemon.redemptions)
		daemon.mu.Unlock()
		reply(w, 200, map[string]any{"state": "approved", "token": token, "session": map[string]any{"session_id": "s-9", "principal_id": "owner", "library_id": sessionLibrary, "device_label": "Desktop"}})
	})
	mux.HandleFunc("/api/v1/session", func(w http.ResponseWriter, r *http.Request) {
		daemon.mu.Lock()
		daemon.seen = append(daemon.seen, r.Method+" "+r.URL.Path)
		library := daemon.sessionLibrary
		failSession := daemon.failSession
		unauthorized := daemon.sessionUnauthorized
		stall := daemon.sessionStall
		deletable := daemon.deleteSession
		daemon.mu.Unlock()
		if r.Method == http.MethodDelete {
			if !deletable {
				w.WriteHeader(404)
				return
			}
			w.WriteHeader(204)
			return
		}
		if failSession {
			w.WriteHeader(503)
			return
		}
		if unauthorized {
			fail(w, 401, "SESSION_REVOKED", false)
			return
		}
		if stall != nil {
			stall()
		}
		reply(w, 200, map[string]any{"session_id": "s-9", "principal_id": "owner", "library_id": library, "device_label": "Desktop"})
	})
	mux.HandleFunc("/api/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		daemon.mu.Lock()
		library, api := daemon.capsLibrary, daemon.apiVersion
		daemon.mu.Unlock()
		reply(w, 200, map[string]any{"library_id": library, "api_version": api})
	})
	mux.HandleFunc("/api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"items": []any{}, "stream_epoch": "e", "head": "1", "next_cursor": "e:1", "resync": nil})
	})
	daemon.server = httptest.NewServer(mux)
	t.Cleanup(daemon.server.Close)
	return daemon
}

func newOnboardingFacade(t *testing.T, daemon *fakeOnboardingDaemon, credentialDir string) *ReaderService {
	t.Helper()
	onb, err := onboarding.New(onboarding.Config{CredentialDir: credentialDir, ProductAPI: "1.0", Endpoint: daemon.server.URL, ExpectedLibrary: "lib-1"})
	if err != nil {
		t.Fatal(err)
	}
	return NewReaderServiceWithOnboarding("", filepath.Join(credentialDir, "receipts"), "", onb)
}

// adoptApproved pairs, polls and adopts through the facade, returning the
// adopted credential index (redemption number).
func adoptApproved(t *testing.T, service *ReaderService, daemon *fakeOnboardingDaemon) {
	t.Helper()
	invokeOK(t, service, "pair", `{"device_label":"Haeryong Desktop (default)"}`)
	invokeOK(t, service, "pollPairing", `{"pairing_id":"pr-9"}`)
	adopted := invokeOK(t, service, "adoptPairing", `{"pairing_id":"pr-9"}`)
	if adopted.(map[string]any)["adopted"] != true {
		t.Fatalf("adoption failed: %v", adopted)
	}
}

func (d *fakeOnboardingDaemon) seenPaths() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.seen...)
}

func (d *fakeOnboardingDaemon) saw(path string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, row := range d.seen {
		if strings.Contains(row, path) {
			return true
		}
	}
	return false
}

func TestUnconfiguredFacadeKeepsOnboardingHonestlyUnavailable(t *testing.T) {
	service, _, _ := newReaderFixture(t, nil)
	for _, operation := range []string{"pair", "pollPairing", "serviceDiscovery", "startDaemonOffer", "adoptPairing", "abandonPairingAttempt"} {
		werr := invokeFail(t, service, operation, `{"device_label":"x","pairing_id":"p"}`)
		if werr.Status != 501 || werr.Code != "FEATURE_NOT_READY" {
			t.Fatalf("%s must stay honestly unimplemented without onboarding, got %+v", operation, werr)
		}
	}
}

func TestOnboardingPairPollAdoptThroughFacadeHoldsSecretsGoSide(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	dir := t.TempDir()
	service := newOnboardingFacade(t, daemon, dir)
	created := invokeOK(t, service, "pair", `{"device_label":"Haeryong Desktop (default)"}`).(map[string]any)
	if created["polling_secret"] != nil {
		t.Fatal("polling secret must never cross the wire")
	}
	if created["pairing_id"] != "pr-9" || created["code"] != "654321" {
		t.Fatalf("unexpected pairing reply: %v", created)
	}
	pending := invokeOK(t, service, "pollPairing", `{"pairing_id":"pr-9"}`).(map[string]any)
	if pending["state"] != "pending" {
		t.Fatalf("expected pending, got %v", pending)
	}
	daemon.mu.Lock()
	daemon.approved = true
	daemon.mu.Unlock()
	// The first too-soon poll hits the known non-consuming 429 and keeps the
	// attempt pending; the carried Retry-After must reach the wire error.
	limited := invokeFail(t, service, "pollPairing", `{"pairing_id":"pr-9"}`)
	if limited.Status != 429 || limited.Code != "RATE_LIMITED" || !limited.Retryable || limited.RetryAfter != 2 {
		t.Fatalf("rate-limited poll must carry schedulable retry semantics, got %+v", limited)
	}
	approved := invokeOK(t, service, "pollPairing", `{"pairing_id":"pr-9"}`).(map[string]any)
	if approved["state"] != "approved" || approved["token"] != nil {
		t.Fatalf("approved reply must be tokenless, got %v", approved)
	}
	session := approved["session"].(map[string]any)
	if session["library_id"] != "lib-1" || session["session_id"] != "s-9" || len(session) != 4 {
		t.Fatalf("unexpected reduced session identity: %v", session)
	}
	adopted := invokeOK(t, service, "adoptPairing", `{"pairing_id":"pr-9"}`).(map[string]any)
	if adopted["adopted"] != true || adopted["durability"] != "confirmed" {
		t.Fatalf("unexpected adoption result: %v", adopted)
	}
	credential, err := client.LoadCredential(service.onboarding.CredentialPath())
	if err != nil || credential.Token != redeemBearer(1) || credential.LibraryID != "lib-1" || credential.Endpoint != daemon.server.URL {
		t.Fatalf("persisted credential mismatch: %+v %v", credential, err)
	}
	// The activated credential attaches through the binding identity pin.
	attached := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	if attached["public_origin"] != daemon.server.URL {
		t.Fatalf("activation must install the adopted binding's base, got %v", attached["public_origin"])
	}
	// Stop after a committed adoption reports the truthful outcome.
	if werr := invokeFail(t, service, "abandonPairingAttempt", `{"pairing_id":"pr-9"}`); werr.Code != "NATIVE_PAIRING_ALREADY_ADOPTED" {
		t.Fatalf("post-adopt abandon must be already-adopted, got %+v", werr)
	}
}

func TestFacadeAttachPinsProductAPIAndLibraryBeforeAuthority(t *testing.T) {
	for _, tc := range []struct {
		name        string
		apiVersion  string
		capsLibrary string
		wantCode    string
	}{
		{"api-incompatible", "2.0", "lib-1", "NATIVE_API_INCOMPATIBLE"},
		{"library-mismatch", "1.0", "lib-2", "NATIVE_LIBRARY_MISMATCH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			daemon := newFakeOnboardingDaemon(t)
			daemon.mu.Lock()
			daemon.approved = true
			daemon.rateLimitFirst = false
			daemon.mu.Unlock()
			dir := t.TempDir()
			service := newOnboardingFacade(t, daemon, dir)
			// Adoption happens against the HEALTHY daemon; the incompatible
			// shape arrives afterwards and the attach pin must refuse
			// effective authority before any attachment exists.
			adoptApproved(t, service, daemon)
			daemon.mu.Lock()
			daemon.apiVersion = tc.apiVersion
			daemon.capsLibrary = tc.capsLibrary
			daemon.sessionLibrary = tc.capsLibrary
			daemon.mu.Unlock()
			werr := invokeFail(t, service, "attachSession", "{}")
			if werr.Code != tc.wantCode {
				t.Fatalf("expected %s before authority, got %+v", tc.wantCode, werr)
			}
			if werr := invokeFail(t, service, "capabilities", "{}"); werr.Code != "NATIVE_SESSION_NOT_ATTACHED" {
				t.Fatalf("no attachment may exist after a pin failure, got %+v", werr)
			}
		})
	}
}

func TestFacadeFirstBootWithoutCredentialReportsNoCredential(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	service := newOnboardingFacade(t, daemon, t.TempDir())
	werr := invokeFail(t, service, "attachSession", "{}")
	if werr.Status != 401 || werr.Code != "NATIVE_NO_CREDENTIAL" {
		t.Fatalf("empty first boot must be the typed no-credential state, got %+v", werr)
	}
}

func TestFacadeAbandonAndOwnedDisposalDropPendingAttempt(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	daemon.mu.Lock()
	daemon.approved = true
	daemon.rateLimitFirst = false
	daemon.mu.Unlock()
	dir := t.TempDir()
	service := newOnboardingFacade(t, daemon, dir)
	invokeOK(t, service, "pair", `{"device_label":"Desktop"}`)
	// An explicit user stop abandons Go-side; the attempt cannot redeem after.
	invokeOK(t, service, "abandonPairingAttempt", `{"pairing_id":"pr-9"}`)
	if werr := invokeFail(t, service, "pollPairing", `{"pairing_id":"pr-9"}`); werr.Code != "NATIVE_PAIRING_ATTEMPT_STALE" {
		t.Fatalf("abandoned attempt must be stale, got %+v", werr)
	}
	// A stale dispose must NOT discard the newer pending attempt.
	invokeOK(t, service, "pair", `{"device_label":"Desktop"}`)
	invokeOK(t, service, "disposeSession", `{"attachment_id":"not-current"}`)
	approved := invokeOK(t, service, "pollPairing", `{"pairing_id":"pr-9"}`).(map[string]any)
	if approved["state"] != "approved" {
		t.Fatalf("stale dispose must leave the pending attempt usable, got %v", approved)
	}
	invokeOK(t, service, "adoptPairing", `{"pairing_id":"pr-9"}`)
	attachment := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	invokeOK(t, service, "pair", `{"device_label":"Desktop"}`)
	invokeOK(t, service, "disposeSession", fmt.Sprintf(`{"attachment_id":%q}`, attachment["attachment_id"]))
	if werr := invokeFail(t, service, "pollPairing", `{"pairing_id":"pr-9"}`); werr.Code != "NATIVE_PAIRING_ATTEMPT_STALE" {
		t.Fatalf("owned disposal must discard the pending attempt, got %+v", werr)
	}
}

// A late attach whose captured token/endpoint pair was replaced by an
// adoption activation must never publish an attachment for the replaced
// context — the mixed-endpoint race.
func TestFacadeAttachRacingCredentialReplacementFailsTyped(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	daemon.mu.Lock()
	daemon.approved = true
	daemon.rateLimitFirst = false
	daemon.deleteSession = true
	daemon.mu.Unlock()
	dir := t.TempDir()
	service := newOnboardingFacade(t, daemon, dir)
	// First adoption installs credential A; attach starts and stalls inside
	// the session read.
	adoptApproved(t, service, daemon)
	stallArrived, stallRelease := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(stallRelease) }) }
	defer releaseAll()
	daemon.mu.Lock()
	daemon.sessionStall = func() { close(stallArrived); <-stallRelease }
	daemon.mu.Unlock()
	t.Cleanup(func() {
		daemon.mu.Lock()
		daemon.sessionStall = nil
		daemon.mu.Unlock()
	})
	attached := make(chan *wireError, 1)
	go func() {
		_, werr := service.invoke("attachSession", nil)
		attached <- werr
	}()
	<-stallArrived
	// Clear the stall for the relogin's own session reads; the stalled attach
	// stays parked inside its handler until releaseAll.
	daemon.mu.Lock()
	daemon.sessionStall = nil
	daemon.mu.Unlock()
	// While the attach is stalled, a full relogin replaces the credential.
	invokeOK(t, service, "pair", `{"device_label":"Desktop"}`)
	invokeOK(t, service, "pollPairing", `{"pairing_id":"pr-9"}`)
	invokeOK(t, service, "adoptPairing", `{"pairing_id":"pr-9"}`)
	releaseAll()
	if werr := <-attached; werr == nil || werr.Code != "NATIVE_SESSION_REPLACED" {
		t.Fatalf("attach under a replaced credential must fail typed, got %+v", werr)
	}
	if werr := invokeFail(t, service, "capabilities", "{}"); werr.Code != "NATIVE_SESSION_NOT_ATTACHED" {
		t.Fatalf("no attachment may survive the replaced-context attach, got %+v", werr)
	}
	// The NEW credential attaches normally.
	fresh := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	if fresh["attachment_id"] == "" {
		t.Fatal("the new credential must attach")
	}
}

// Straight-line relogin: logout of credential A must not block adopting and
// attaching credential B afterwards.
func TestFacadeReloginAfterLogoutAdoptsAndAttaches(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	daemon.mu.Lock()
	daemon.approved = true
	daemon.rateLimitFirst = false
	daemon.deleteSession = true
	daemon.mu.Unlock()
	dir := t.TempDir()
	service := newOnboardingFacade(t, daemon, dir)
	adoptApproved(t, service, daemon)
	invokeOK(t, service, "attachSession", "{}")
	invokeOK(t, service, "revokeOwnSession", "{}")
	if _, err := os.Stat(service.onboarding.CredentialPath()); !os.IsNotExist(err) {
		t.Fatal("logout must forget the adopted protected credential file")
	}
	// A fresh authorized attempt after the logout adopts the new credential.
	invokeOK(t, service, "pair", `{"device_label":"Desktop"}`)
	invokeOK(t, service, "pollPairing", `{"pairing_id":"pr-9"}`)
	adopted := invokeOK(t, service, "adoptPairing", `{"pairing_id":"pr-9"}`).(map[string]any)
	if adopted["adopted"] != true {
		t.Fatalf("relogin adoption must not be suppressed: %v", adopted)
	}
	attached := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	if attached["attachment_id"] == "" {
		t.Fatal("relogin must attach the new credential")
	}
	credential, err := client.LoadCredential(service.onboarding.CredentialPath())
	if err != nil || credential.Token != redeemBearer(2) {
		t.Fatalf("relogin credential mismatch: %+v %v", credential, err)
	}
}

// A logout of EXACTLY the committing credential wins over its late
// activation: the adoption saved the file but authority stays dropped.
func TestFacadeLogoutOfCommittingCredentialSuppressesLateActivation(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	daemon.mu.Lock()
	daemon.approved = true
	daemon.rateLimitFirst = false
	daemon.deleteSession = true
	daemon.mu.Unlock()
	dir := t.TempDir()
	service := newOnboardingFacade(t, daemon, dir)
	invokeOK(t, service, "pair", `{"device_label":"Desktop"}`)
	invokeOK(t, service, "pollPairing", `{"pairing_id":"pr-9"}`)
	// Simulate the logout landing between the adoption commit and the
	// activation callback: the exact captured credential is known Go-side.
	committed := client.Credential{Endpoint: daemon.server.URL, Profile: "default", LibraryID: "lib-1", Token: redeemBearer(1)}
	service.mu.Lock()
	service.token = ""
	service.logoutSeen = true
	service.forgetNeeded = true
	service.logoutBearer = committed.Token
	service.mu.Unlock()
	service.activateAdoptedCredential(committed, 1)
	service.mu.Lock()
	resurrected := service.token == committed.Token
	service.mu.Unlock()
	if resurrected {
		t.Fatal("a logout of exactly this credential must suppress the late activation")
	}
}

func TestProductionLogoutForgetsAdoptedCredentialFile(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	daemon.mu.Lock()
	daemon.approved = true
	daemon.rateLimitFirst = false
	daemon.deleteSession = true
	daemon.mu.Unlock()
	dir := t.TempDir()
	service := newOnboardingFacade(t, daemon, dir)
	adoptApproved(t, service, daemon)
	invokeOK(t, service, "attachSession", "{}")
	invokeOK(t, service, "revokeOwnSession", "{}")
	if _, err := os.Stat(service.onboarding.CredentialPath()); !os.IsNotExist(err) {
		t.Fatal("logout must forget the adopted protected credential file")
	}
	// A foreign binding is never unlinked by a late forget retry.
	foreign := client.Credential{Endpoint: daemon.server.URL, Profile: "default", LibraryID: "lib-1", Token: strings.Repeat("cd", 32)}
	if err := client.SaveCredential(service.onboarding.CredentialPath(), foreign); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	service.forgetNeeded = true
	service.forgetDone = false
	service.logoutBearer = redeemBearer(1)
	service.forgetAttachmentID = "retry"
	service.mu.Unlock()
	invokeFail(t, service, "disposeSession", `{"attachment_id":"retry"}`)
	if _, err := os.Stat(service.onboarding.CredentialPath()); err != nil {
		t.Fatal("a foreign credential must never be unlinked by a late forget")
	}
}

// The attach pin requires a SIGNED library and resolves it BEFORE any
// bearer-bearing request: an unresolvable pin sends the mismatched listener
// zero credentials.
func TestFacadeAttachRequiresResolvedSignedLibraryPin(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	daemon.mu.Lock()
	daemon.approved = true
	daemon.rateLimitFirst = false
	daemon.deleteSession = true
	daemon.mu.Unlock()
	dir := t.TempDir()
	// No fixture endpoint, no expected library, no resolvable selection: the
	// signed pin cannot resolve and the attach must fail closed without any
	// session/capabilities request.
	onb, err := onboarding.New(onboarding.Config{CredentialDir: dir, ProductAPI: "1.0", Selection: onboarding.Selection{DataDir: filepath.Join(dir, "missing-profile")}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewReaderServiceWithOnboarding(daemon.server.URL, filepath.Join(dir, "receipts2"), redeemBearer(7), onb)
	werr := invokeFail(t, service, "attachSession", "{}")
	if werr.Code != "NATIVE_DAEMON_UNAVAILABLE" {
		t.Fatalf("an unresolved signed pin must fail closed at attach, got %+v", werr)
	}
	if daemon.saw("/api/v1/session") || daemon.saw("/api/v1/capabilities") {
		t.Fatalf("an unresolved pin must send zero bearer requests, saw %v", daemon.seenPaths())
	}
}

// A captured endpoint that differs from the signed binding never receives
// the bearer: the mismatch fails typed before any request is dispatched.
func TestFacadeEndpointMismatchSendsZeroBearer(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	daemon.mu.Lock()
	daemon.approved = true
	daemon.rateLimitFirst = false
	daemon.deleteSession = true
	daemon.mu.Unlock()
	dir := t.TempDir()
	// The onboarding binding points at the daemon, but the facade's captured
	// base is a foreign listener.
	onb, err := onboarding.New(onboarding.Config{CredentialDir: dir, ProductAPI: "1.0", Endpoint: daemon.server.URL, ExpectedLibrary: "lib-1"})
	if err != nil {
		t.Fatal(err)
	}
	service := NewReaderServiceWithOnboarding("http://127.0.0.1:1", filepath.Join(dir, "receipts3"), redeemBearer(8), onb)
	werr := invokeFail(t, service, "attachSession", "{}")
	if werr.Code != "NATIVE_CREDENTIAL_MISMATCHED" {
		t.Fatalf("a mismatched captured endpoint must fail typed, got %+v", werr)
	}
	if daemon.saw("/api/v1/session") {
		t.Fatal("the signed daemon binding saw no request; the foreign listener must receive zero bearers")
	}
}

// Exactly one of two concurrent attaches publishes its era; the loser fails
// typed without an attachment.
func TestFacadeConcurrentAttachPublishesExactlyOneEra(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	daemon.mu.Lock()
	daemon.approved = true
	daemon.rateLimitFirst = false
	daemon.deleteSession = true
	daemon.mu.Unlock()
	dir := t.TempDir()
	service := newOnboardingFacade(t, daemon, dir)
	adoptApproved(t, service, daemon)
	results := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		go func() {
			value, werr := service.invoke("attachSession", nil)
			results <- werr == nil && value != nil
		}()
	}
	published, failed := 0, 0
	for i := 0; i < 2; i++ {
		if <-results {
			published++
		} else {
			failed++
		}
	}
	if published != 1 || failed != 1 {
		t.Fatalf("exactly one concurrent attach may publish, got %d published / %d failed", published, failed)
	}
}

// An attach stalled in its session read while its attachment is disposed must
// fail typed instead of resurrecting the disposed context.
func TestFacadeAttachDuringOwnedDisposeFailsTyped(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	daemon.mu.Lock()
	daemon.approved = true
	daemon.rateLimitFirst = false
	daemon.deleteSession = true
	daemon.mu.Unlock()
	dir := t.TempDir()
	service := newOnboardingFacade(t, daemon, dir)
	adoptApproved(t, service, daemon)
	first := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	stallArrived, stallRelease := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(stallRelease) }) }
	defer releaseAll()
	daemon.mu.Lock()
	daemon.sessionStall = func() { close(stallArrived); <-stallRelease }
	daemon.mu.Unlock()
	t.Cleanup(func() {
		daemon.mu.Lock()
		daemon.sessionStall = nil
		daemon.mu.Unlock()
	})
	attached := make(chan *wireError, 1)
	go func() {
		_, werr := service.invoke("attachSession", nil)
		attached <- werr
	}()
	<-stallArrived
	daemon.mu.Lock()
	daemon.sessionStall = nil
	daemon.mu.Unlock()
	invokeOK(t, service, "disposeSession", fmt.Sprintf(`{"attachment_id":%q}`, first["attachment_id"]))
	releaseAll()
	if werr := <-attached; werr == nil || werr.Code != "NATIVE_SESSION_REPLACED" {
		t.Fatalf("attach over a disposed era must fail typed, got %+v", werr)
	}
	if werr := invokeFail(t, service, "capabilities", "{}"); werr.Code != "NATIVE_SESSION_NOT_ATTACHED" {
		t.Fatalf("the disposed context must stay disposed, got %+v", werr)
	}
}

// A logout invalidates the pairing attempt of its era: the in-flight
// approval can adopt nothing afterwards, while a pairing started AFTER the
// logout works normally (covered by the relogin test).
func TestLogoutInvalidatesInflightPairingAttempt(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	daemon.mu.Lock()
	daemon.approved = true
	daemon.rateLimitFirst = false
	daemon.deleteSession = true
	daemon.mu.Unlock()
	dir := t.TempDir()
	service := newOnboardingFacade(t, daemon, dir)
	adoptApproved(t, service, daemon)
	invokeOK(t, service, "attachSession", "{}")
	// A new pending attempt exists when the owner logs out.
	invokeOK(t, service, "pair", `{"device_label":"Desktop"}`)
	invokeOK(t, service, "revokeOwnSession", "{}")
	// The logged-out era's attempt is discarded: its late approval/adopt
	// path is stale, never authority.
	if werr := invokeFail(t, service, "pollPairing", `{"pairing_id":"pr-9"}`); werr.Code != "NATIVE_PAIRING_ATTEMPT_STALE" {
		t.Fatalf("logout must invalidate the in-flight attempt, got %+v", werr)
	}
	if werr := invokeFail(t, service, "adoptPairing", `{"pairing_id":"pr-9"}`); werr.Code != "NATIVE_PAIRING_ATTEMPT_STALE" {
		t.Fatalf("the invalidated attempt must not adopt, got %+v", werr)
	}
}

// A pairing attempt created AFTER an owned disposal survives the disposal's
// era-scoped discard.
func TestFreshAttemptAfterOwnedDisposalSurvives(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	daemon.mu.Lock()
	daemon.approved = true
	daemon.rateLimitFirst = false
	daemon.deleteSession = true
	daemon.mu.Unlock()
	dir := t.TempDir()
	service := newOnboardingFacade(t, daemon, dir)
	adoptApproved(t, service, daemon)
	attachment := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	invokeOK(t, service, "disposeSession", fmt.Sprintf(`{"attachment_id":%q}`, attachment["attachment_id"]))
	// A fresh relogin attempt AFTER the disposal redeems and adopts.
	invokeOK(t, service, "pair", `{"device_label":"Desktop"}`)
	approved := invokeOK(t, service, "pollPairing", `{"pairing_id":"pr-9"}`).(map[string]any)
	if approved["state"] != "approved" {
		t.Fatalf("fresh attempt must redeem after disposal, got %v", approved)
	}
	adopted := invokeOK(t, service, "adoptPairing", `{"pairing_id":"pr-9"}`).(map[string]any)
	if adopted["adopted"] != true {
		t.Fatalf("fresh attempt must adopt after disposal, got %v", adopted)
	}
}
