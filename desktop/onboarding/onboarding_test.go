package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/localcontrol"
	"haeryong.local/haeryong/internal/application/service"
)

func privateTempDir(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBuildDaemonArgvUsesExactSelectionWithoutPathSearch(t *testing.T) {
	argv := buildDaemonArgv("/opt/haeryong/haeryong", Selection{Profile: "default", DataDir: "/tmp/hd", VolumeRoot: "/Volumes/Data", Port: 9270})
	want := "/opt/haeryong/haeryong|daemon|run|--profile|default|--data-dir|/tmp/hd|--volume-root|/Volumes/Data|--port|9270|--json|--no-input"
	if strings.Join(argv, "|") != want {
		t.Fatalf("argv drifted: %v", argv)
	}
	minimal := buildDaemonArgv("/bin/haeryong", Selection{Profile: "default"})
	if strings.Join(minimal, "|") != "/bin/haeryong|daemon|run|--profile|default|--json|--no-input" {
		t.Fatalf("minimal argv drifted: %v", minimal)
	}
}

func TestNewEnforcesDefaultProfileScopeAndRequiredFields(t *testing.T) {
	if _, err := New(Config{Selection: Selection{Profile: "other"}, CredentialDir: privateTempDir(t), ProductAPI: "1.0"}); err == nil {
		t.Fatal("non-default profile must fail closed")
	}
	if _, err := New(Config{CredentialDir: "", ProductAPI: "1.0"}); err == nil {
		t.Fatal("missing credential directory must fail")
	}
	if _, err := New(Config{CredentialDir: privateTempDir(t), ProductAPI: ""}); err == nil {
		t.Fatal("missing product API version must fail")
	}
	if _, err := New(Config{CredentialDir: privateTempDir(t), ProductAPI: "1.0"}); err != nil {
		t.Fatalf("default selection implied: %v", err)
	}
}

func TestDiscoverClassifiesUninitializedAndDaemonStopped(t *testing.T) {
	dir := privateTempDir(t)
	onb, err := New(Config{Selection: Selection{DataDir: filepath.Join(dir, "missing-profile")}, CredentialDir: filepath.Join(dir, "creds"), ProductAPI: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	result := onb.Discover(context.Background())
	if result.State != StateUninitialized {
		t.Fatalf("missing profile must be uninitialized, got %s (%s)", result.State, result.Detail)
	}
	// An initialized profile without a discovery record reports daemon_stopped.
	profile, err := service.Initialize(service.Selection{Profile: "default", DataDir: filepath.Join(dir, "profile")})
	if err != nil {
		t.Fatal(err)
	}
	if profile.LibraryID == "" {
		t.Fatal("initialize produced no library")
	}
	onb, err = New(Config{Selection: Selection{DataDir: filepath.Join(dir, "profile")}, CredentialDir: filepath.Join(dir, "creds"), ProductAPI: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	result = onb.Discover(context.Background())
	if result.State != StateDaemonStopped {
		t.Fatalf("initialized-but-stopped profile must be daemon_stopped, got %s (%s)", result.State, result.Detail)
	}
}

// A valid discovery record whose listener is gone is honest staleness: the
// explicit start offer must stay reachable (the spawn itself may then fail
// honestly), while malformed or foreign records fail closed and never spawn.
func TestDiscoverDistinguishesStaleListenerFromCorruptOrForeignRecords(t *testing.T) {
	dir := privateTempDir(t)
	profile, err := service.Initialize(service.Selection{Profile: "default", DataDir: filepath.Join(dir, "profile")})
	if err != nil {
		t.Fatal(err)
	}
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stalePort := free.Addr().(*net.TCPAddr).Port
	free.Close()
	instance, err := service.NewID()
	if err != nil {
		t.Fatal(err)
	}
	valid := map[string]any{
		"library_id":    profile.LibraryID,
		"profile_id":    profile.ProfileID,
		"instance_id":   instance,
		"api":           service.APIVersion,
		"endpoint":      fmt.Sprintf("http://127.0.0.1:%d", stalePort),
		"control_ready": true,
	}
	newOnboarding := func() *Onboarding {
		onb, err := New(Config{Selection: Selection{DataDir: filepath.Join(dir, "profile")}, CredentialDir: filepath.Join(dir, "creds"), ProductAPI: "1.0", DaemonBinary: filepath.Join(dir, "missing-daemon")})
		if err != nil {
			t.Fatal(err)
		}
		return onb
	}
	runtime := filepath.Join(dir, "profile", "runtime")
	write := func(t *testing.T, value any) {
		t.Helper()
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(runtime, "endpoint.json"), encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("stale-valid-record-permits-explicit-start-offer", func(t *testing.T) {
		write(t, valid)
		onb := newOnboarding()
		result := onb.Discover(context.Background())
		if result.State != StateDaemonStopped {
			t.Fatalf("stale listener must stay daemon_stopped, got %s (%s)", result.State, result.Detail)
		}
		// The gate passes and the spawn itself fails honestly on the missing
		// binary — proving the offer was not blocked by staleness.
		start, werr := onb.StartDaemonOffer(context.Background())
		if werr == nil || werr.Code != "NATIVE_START_SPAWN_FAILED" {
			t.Fatalf("stale discovery must reach the spawn, got %+v %v", start, werr)
		}
	})

	t.Run("malformed-record-fails-closed-and-never-spawns", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(runtime, "endpoint.json"), []byte("{not json"), 0600); err != nil {
			t.Fatal(err)
		}
		onb := newOnboarding()
		result := onb.Discover(context.Background())
		if result.State != StateDiscoveryCorrupt {
			t.Fatalf("malformed record must be discovery_corrupt, got %s (%s)", result.State, result.Detail)
		}
		start, werr := onb.StartDaemonOffer(context.Background())
		if werr != nil || start.Started != false || start.Discovery.State != StateDiscoveryCorrupt {
			t.Fatalf("corrupt discovery must refuse the start offer, got %+v %v", start, werr)
		}
	})

	t.Run("foreign-library-record-fails-closed", func(t *testing.T) {
		foreign, err := service.NewID()
		if err != nil {
			t.Fatal(err)
		}
		write(t, map[string]any{
			"library_id":    foreign,
			"profile_id":    profile.ProfileID,
			"instance_id":   instance,
			"api":           service.APIVersion,
			"endpoint":      fmt.Sprintf("http://127.0.0.1:%d", stalePort),
			"control_ready": true,
		})
		onb := newOnboarding()
		result := onb.Discover(context.Background())
		if result.State != StateDiscoveryCorrupt {
			t.Fatalf("foreign library record must be discovery_corrupt, got %s (%s)", result.State, result.Detail)
		}
		start, werr := onb.StartDaemonOffer(context.Background())
		if werr != nil || start.Started != false {
			t.Fatalf("foreign record must refuse the start offer, got %+v %v", start, werr)
		}
	})
}

func TestClassifyStoreOutcomes(t *testing.T) {
	want := client.Credential{Endpoint: "http://127.0.0.1:9270", Profile: "default", LibraryID: "lib-1", Token: strings.Repeat("ab", 32)}
	if classifyStore(nil, client.Credential{}, nil, want) != storeCommitted {
		t.Fatal("clean save must be committed")
	}
	if classifyStore(fmt.Errorf("dir sync failed"), want, nil, want) != storeDurabilityUnconfirmed {
		t.Fatal("exact-candidate readback after a save error stays committed")
	}
	if classifyStore(fmt.Errorf("create failed"), client.Credential{}, os.ErrNotExist, want) != storeFailed {
		t.Fatal("absent file after a save error is a definitive pre-rename failure")
	}
	if classifyStore(fmt.Errorf("create failed"), client.Credential{}, fmt.Errorf("unreadable"), want) != storeUncertain {
		t.Fatal("unreadable readback stays honestly unknown")
	}
	if classifyStore(fmt.Errorf("create failed"), client.Credential{Token: "other"}, nil, want) != storeUncertain {
		t.Fatal("a foreign file after a save error cannot be classified as failed")
	}
}

// fakePairingDaemon serves the exact pairing/session/capability routes the
// onboarding flow consumes. Every approved redemption issues a distinct
// protocol-valid bearer (64 lowercase hex), tests flip the approval state and
// may stall the first create POST, a poll, or the adopt-time session read to
// race adoption against stop/replacement.
type fakePairingDaemon struct {
	mu             sync.Mutex
	server         *httptest.Server
	approved       bool
	redemptions    int
	pollRequests   int
	createRequests int
	apiVersion     string
	capsLibrary    string
	sessionLibrary string
	failSession    bool
	failPoll       bool
	sessionStall   func()
	createStall    func()
	pollStall      func()
	seen           []string
}

// redeemToken returns a protocol-valid synthetic bearer (64 lowercase hex,
// 32 bytes) so client.SaveCredential/LoadCredential accept the fixtures.
func redeemToken(n int) string { return fmt.Sprintf("%064x", n) }

var firstRedeemedToken = redeemToken(1)

func (d *fakePairingDaemon) nextToken() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.redemptions++
	return fmt.Sprintf("%064x", d.redemptions)
}

func validBearer(auth string) bool {
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	raw := strings.TrimPrefix(auth, "Bearer ")
	if len(raw) != 64 {
		return false
	}
	for _, char := range raw {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func newFakePairingDaemon(t *testing.T) *fakePairingDaemon {
	t.Helper()
	daemon := &fakePairingDaemon{apiVersion: "1.0", capsLibrary: "lib-1", sessionLibrary: "lib-1"}
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"data": value})
	}
	mux.HandleFunc("/api/v1/auth/pairings", func(w http.ResponseWriter, r *http.Request) {
		daemon.mu.Lock()
		daemon.createRequests++
		first := daemon.createRequests == 1
		daemon.seen = append(daemon.seen, r.Method+" "+r.URL.Path)
		stall := daemon.createStall
		daemon.mu.Unlock()
		if r.Method != http.MethodPost {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Origin") == "" {
			t.Error("pairing create must carry the daemon origin")
		}
		// Only the FIRST create request may stall so a concurrent newer
		// request never blocks behind it.
		if stall != nil && first {
			stall()
		}
		reply(w, 201, map[string]any{"pairing_id": "pr-1", "code": "123456", "polling_secret": "sekrit", "expires_at": "2026-10-19T00:00:00Z"})
	})
	mux.HandleFunc("/api/v1/auth/pairings/pr-1/poll", func(w http.ResponseWriter, r *http.Request) {
		daemon.mu.Lock()
		daemon.pollRequests++
		first := daemon.pollRequests == 1
		approved := daemon.approved
		sessionLibrary := daemon.sessionLibrary
		daemon.seen = append(daemon.seen, r.Method+" "+r.URL.Path)
		stall := daemon.pollStall
		daemon.mu.Unlock()
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["polling_secret"] != "sekrit" {
			reply(w, 403, map[string]any{"error": map[string]any{"code": "POLL_SECRET_INVALID"}})
			return
		}
		if !approved {
			reply(w, 200, map[string]any{"state": "pending"})
			return
		}
		if stall != nil && first {
			stall()
		}
		daemon.mu.Lock()
		fail := daemon.failPoll
		daemon.mu.Unlock()
		if fail {
			w.WriteHeader(503)
			return
		}
		token := daemon.nextToken()
		reply(w, 200, map[string]any{"state": "approved", "token": token, "session": map[string]any{"session_id": "s-1", "principal_id": "owner", "library_id": sessionLibrary, "device_label": "Desktop"}})
	})
	mux.HandleFunc("/api/v1/session", func(w http.ResponseWriter, r *http.Request) {
		daemon.mu.Lock()
		daemon.seen = append(daemon.seen, r.Method+" "+r.URL.Path)
		library := daemon.sessionLibrary
		stall := daemon.sessionStall
		daemon.mu.Unlock()
		if !validBearer(r.Header.Get("Authorization")) {
			t.Error("session read must use a Go-held redeemed candidate bearer")
		}
		if stall != nil {
			stall()
		}
		daemon.mu.Lock()
		fail := daemon.failSession
		daemon.mu.Unlock()
		if fail {
			w.WriteHeader(503)
			return
		}
		reply(w, 200, map[string]any{"session_id": "s-1", "principal_id": "owner", "library_id": library, "device_label": "Desktop"})
	})
	mux.HandleFunc("/api/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		daemon.mu.Lock()
		daemon.seen = append(daemon.seen, r.Method+" "+r.URL.Path)
		library, api := daemon.capsLibrary, daemon.apiVersion
		daemon.mu.Unlock()
		reply(w, 200, map[string]any{"library_id": library, "api_version": api})
	})
	daemon.server = httptest.NewServer(mux)
	t.Cleanup(daemon.server.Close)
	return daemon
}

func (d *fakePairingDaemon) pollCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pollRequests
}

func newTestOnboarding(t *testing.T, endpoint, credentialDir string) *Onboarding {
	t.Helper()
	onb, err := New(Config{CredentialDir: credentialDir, ProductAPI: "1.0", Endpoint: endpoint, ExpectedLibrary: "lib-1"})
	if err != nil {
		t.Fatal(err)
	}
	return onb
}

func pairAndApprove(t *testing.T, onb *Onboarding, daemon *fakePairingDaemon) {
	t.Helper()
	if _, werr := onb.CreatePairing(context.Background(), "Haeryong Desktop (default)"); werr != nil {
		t.Fatal(werr)
	}
	daemon.mu.Lock()
	daemon.approved = true
	daemon.mu.Unlock()
	if _, werr := onb.PollPairing(context.Background(), "pr-1"); werr != nil {
		t.Fatal(werr)
	}
}

func TestPairPollAdoptFlowHoldsSecretsAndPersistsExactCredential(t *testing.T) {
	daemon := newFakePairingDaemon(t)
	onb := newTestOnboarding(t, daemon.server.URL, privateTempDir(t))
	created, werr := onb.CreatePairing(context.Background(), "Haeryong Desktop (default)")
	if werr != nil {
		t.Fatal(werr)
	}
	if created["polling_secret"] != nil {
		t.Fatal("polling secret must never cross to JavaScript")
	}
	if created["pairing_id"] != "pr-1" || created["code"] != "123456" {
		t.Fatalf("unexpected pairing reply: %v", created)
	}
	pending, werr := onb.PollPairing(context.Background(), "pr-1")
	if werr != nil || pending["state"] != "pending" {
		t.Fatalf("pending poll failed: %v %v", pending, werr)
	}
	pairAndApprove(t, onb, daemon)
	activated := ""
	result, werr := onb.Adopt(context.Background(), "pr-1", func(credential client.Credential, attemptGen uint64) { activated = credential.Token })
	if werr != nil {
		t.Fatal(werr)
	}
	if result["adopted"] != true || result["durability"] != "confirmed" {
		t.Fatalf("unexpected adoption result: %v", result)
	}
	if activated != firstRedeemedToken {
		t.Fatal("activation callback never received the candidate")
	}
	credential, err := client.LoadCredential(onb.CredentialPath())
	if err != nil || credential.Token != firstRedeemedToken || credential.LibraryID != "lib-1" || credential.Profile != "default" {
		t.Fatalf("persisted credential mismatch: %+v %v", credential, err)
	}
	// Abandon after a committed adoption reports the truthful outcome.
	if _, werr := onb.Abandon("pr-1"); werr == nil || werr.Code != "NATIVE_PAIRING_ALREADY_ADOPTED" {
		t.Fatalf("post-adopt abandon must be already-adopted, got %v", werr)
	}
	// The remembered boot reads the exact persisted binding back.
	token, werr := onb.RememberedToken(context.Background())
	if werr != nil || token != firstRedeemedToken {
		t.Fatalf("remembered boot failed: %q %v", token, werr)
	}
}

func TestAbandonThenAdoptIsStaleAndDelayedPollCannotAdoptWrongAttempt(t *testing.T) {
	daemon := newFakePairingDaemon(t)
	onb := newTestOnboarding(t, daemon.server.URL, privateTempDir(t))
	if _, werr := onb.CreatePairing(context.Background(), "Haeryong Desktop (default)"); werr != nil {
		t.Fatal(werr)
	}
	if _, werr := onb.Abandon("pr-1"); werr != nil {
		t.Fatal(werr)
	}
	if _, werr := onb.PollPairing(context.Background(), "pr-1"); werr == nil || werr.Code != "NATIVE_PAIRING_ATTEMPT_STALE" {
		t.Fatalf("poll after abandon must be stale, got %v", werr)
	}
	if _, werr := onb.Adopt(context.Background(), "pr-1", func(client.Credential, uint64) {}); werr == nil || werr.Code != "NATIVE_PAIRING_ATTEMPT_STALE" {
		t.Fatalf("adopt after abandon must be stale, got %v", werr)
	}
	// A replacement attempt invalidates the previous one even after approval.
	if _, werr := onb.CreatePairing(context.Background(), "Haeryong Desktop (default)"); werr != nil {
		t.Fatal(werr)
	}
	if _, werr := onb.Abandon("pr-1"); werr != nil {
		t.Fatal(werr)
	}
	daemon.mu.Lock()
	daemon.approved = true
	daemon.mu.Unlock()
	approved, werr := onb.PollPairing(context.Background(), "pr-1")
	if werr == nil && approved["state"] == "approved" {
		t.Fatal("an abandoned attempt must never redeem into authority")
	}
	if werr == nil || werr.Code != "NATIVE_PAIRING_ATTEMPT_STALE" {
		t.Fatalf("delayed approved poll after replacement must be stale, got %v %v", approved, werr)
	}
}

// Stop between the async validation and the commit: the truthful stop wins,
// nothing is persisted and no authority activates.
func TestStopBetweenValidationAndCommitBlocksStaleAdoption(t *testing.T) {
	daemon := newFakePairingDaemon(t)
	onb := newTestOnboarding(t, daemon.server.URL, privateTempDir(t))
	pairAndApprove(t, onb, daemon)
	arrived, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()
	onb.testBeforeCommit = func() { close(arrived); <-release }
	t.Cleanup(func() { onb.testBeforeCommit = nil })
	adopted := make(chan *Error, 1)
	activated := make(chan string, 1)
	go func() {
		_, werr := onb.Adopt(context.Background(), "pr-1", func(credential client.Credential, attemptGen uint64) { activated <- credential.Token })
		adopted <- werr
	}()
	<-arrived
	if outcome, werr := onb.Abandon("pr-1"); werr != nil || outcome["abandoned"] != true {
		t.Fatalf("stop during adoption must truthfully abandon, got %v %v", outcome, werr)
	}
	releaseAll()
	if werr := <-adopted; werr == nil || werr.Code != "NATIVE_PAIRING_ATTEMPT_STALE" {
		t.Fatalf("stale adoption must fail typed, got %v", werr)
	}
	select {
	case token := <-activated:
		t.Fatalf("stopped adoption activated authority with %q", token)
	default:
	}
	if _, err := os.Stat(onb.CredentialPath()); !os.IsNotExist(err) {
		t.Fatal("stopped adoption must not persist a credential")
	}
}

// Replacement between the async validation and the commit: the old adoption
// goes stale without persisting, and the new attempt stays fully usable.
func TestReplaceBetweenValidationAndCommitKeepsNewAttemptUsable(t *testing.T) {
	daemon := newFakePairingDaemon(t)
	onb := newTestOnboarding(t, daemon.server.URL, privateTempDir(t))
	pairAndApprove(t, onb, daemon)
	arrived, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()
	onb.testBeforeCommit = func() { close(arrived); <-release }
	t.Cleanup(func() { onb.testBeforeCommit = nil })
	adopted := make(chan *Error, 1)
	go func() {
		_, werr := onb.Adopt(context.Background(), "pr-1", func(client.Credential, uint64) {})
		adopted <- werr
	}()
	<-arrived
	if _, werr := onb.CreatePairing(context.Background(), "Haeryong Desktop (default)"); werr != nil {
		t.Fatal(werr)
	}
	releaseAll()
	if werr := <-adopted; werr == nil || werr.Code != "NATIVE_PAIRING_ATTEMPT_STALE" {
		t.Fatalf("replaced adoption must fail stale, got %v", werr)
	}
	if _, err := os.Stat(onb.CredentialPath()); !os.IsNotExist(err) {
		t.Fatal("replaced adoption must not persist a credential")
	}
	// The new attempt is untouched: it still polls and stops normally.
	if outcome, werr := onb.PollPairing(context.Background(), "pr-1"); werr != nil || outcome["state"] != "approved" {
		t.Fatalf("new attempt must remain usable, got %v %v", outcome, werr)
	}
	if outcome, werr := onb.Abandon("pr-1"); werr != nil || outcome["abandoned"] != true {
		t.Fatalf("new attempt must stop cleanly, got %v %v", outcome, werr)
	}
}

// A late old-adopt failure must never poison the replacement attempt: the
// stalled poll lets the test replace the attempt before the adopt's session
// read fails.
func TestOldAdoptionFailureNeverPoisonsReplacementAttempt(t *testing.T) {
	daemon := newFakePairingDaemon(t)
	onb := newTestOnboarding(t, daemon.server.URL, privateTempDir(t))
	pairAndApprove(t, onb, daemon)
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
	adopted := make(chan *Error, 1)
	go func() {
		_, werr := onb.Adopt(context.Background(), "pr-1", func(client.Credential, uint64) {})
		adopted <- werr
	}()
	<-stallArrived
	// Replace the attempt, then make the old adopt's session read fail.
	if _, werr := onb.CreatePairing(context.Background(), "Haeryong Desktop (default)"); werr != nil {
		t.Fatal(werr)
	}
	daemon.mu.Lock()
	daemon.failSession = true
	daemon.sessionStall = nil
	daemon.mu.Unlock()
	releaseAll()
	if werr := <-adopted; werr == nil {
		t.Fatal("old adoption should fail on the failed session read")
	}
	// The replacement attempt is still pending and usable.
	if outcome, werr := onb.PollPairing(context.Background(), "pr-1"); werr != nil || outcome["state"] != "approved" {
		t.Fatalf("replacement attempt was poisoned: %v %v", outcome, werr)
	}
}

// Concurrent polls and adopts on one approved attempt stay race-free and
// linearize to a valid outcome set (run under -race by the module suite).
func TestConcurrentPollAndAdoptLinearize(t *testing.T) {
	daemon := newFakePairingDaemon(t)
	onb := newTestOnboarding(t, daemon.server.URL, privateTempDir(t))
	pairAndApprove(t, onb, daemon)
	var wait sync.WaitGroup
	outcomes := make(chan string, 32)
	activation := make(chan string, 32)
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, werr := onb.PollPairing(context.Background(), "pr-1")
			switch {
			case werr != nil:
				outcomes <- "poll:" + werr.Code
			case result["state"] == "approved":
				outcomes <- "poll:approved"
			default:
				outcomes <- "poll:" + result["state"].(string)
			}
		}()
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, werr := onb.Adopt(context.Background(), "pr-1", func(credential client.Credential, attemptGen uint64) { activation <- credential.Token })
			switch {
			case werr != nil:
				outcomes <- "adopt:" + werr.Code
			case result["adopted"] == true:
				outcomes <- "adopt:committed"
			default:
				outcomes <- "adopt:unexpected"
			}
		}()
	}
	wait.Wait()
	close(outcomes)
	close(activation)
	activations := 0
	for range activation {
		activations++
	}
	if activations > 1 {
		t.Fatalf("adoption activated %d times", activations)
	}
	credential, err := client.LoadCredential(onb.CredentialPath())
	if err != nil || !validBearer("Bearer "+credential.Token) || credential.Token == "" {
		t.Fatalf("credential must be consistent after the race: %+v %v", credential, err)
	}
}

// A late poll failure arriving after an overlapping poll's success (or a
// committed adoption) must never erase the held candidate: the phase guard
// keeps the redemption, adoption still commits and Stop reports the truthful
// already-adopted outcome.
func TestLatePollFailureCannotEraseHeldCandidateOrCommittedAdoption(t *testing.T) {
	daemon := newFakePairingDaemon(t)
	onb := newTestOnboarding(t, daemon.server.URL, privateTempDir(t))
	if _, werr := onb.CreatePairing(context.Background(), "Haeryong Desktop (default)"); werr != nil {
		t.Fatal(werr)
	}
	daemon.mu.Lock()
	daemon.approved = true
	daemon.mu.Unlock()
	stallArrived, stallRelease := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(stallRelease) }) }
	defer releaseAll()
	daemon.mu.Lock()
	daemon.pollStall = func() { close(stallArrived); <-stallRelease }
	daemon.mu.Unlock()
	t.Cleanup(func() {
		daemon.mu.Lock()
		daemon.pollStall = nil
		daemon.mu.Unlock()
	})
	latePoll := make(chan *Error, 1)
	go func() {
		_, werr := onb.PollPairing(context.Background(), "pr-1")
		latePoll <- werr
	}()
	<-stallArrived
	// The overlapping poll succeeds while the first is stalled in-handler.
	approved, werr := onb.PollPairing(context.Background(), "pr-1")
	if werr != nil || approved["state"] != "approved" {
		t.Fatalf("overlapping poll must redeem, got %v %v", approved, werr)
	}
	// Fail the stalled poll after the candidate is held.
	daemon.mu.Lock()
	daemon.failPoll = true
	daemon.pollStall = nil
	daemon.mu.Unlock()
	releaseAll()
	if werr := <-latePoll; werr == nil {
		t.Fatal("the stalled poll must surface its failure honestly")
	}
	// The held candidate survived the late failure: adoption commits.
	activated := ""
	if result, werr := onb.Adopt(context.Background(), "pr-1", func(credential client.Credential, attemptGen uint64) { activated = credential.Token }); werr != nil || result["adopted"] != true {
		t.Fatalf("adoption after a late overlapping poll failure must commit, got %v %v", result, werr)
	}
	if activated != firstRedeemedToken {
		t.Fatalf("activation must use the held candidate, got %q", activated)
	}
	// A post-commit poll never consumes again and never corrupts the state.
	before := daemon.pollCount()
	if _, werr := onb.PollPairing(context.Background(), "pr-1"); werr == nil || werr.Code != "NATIVE_PAIRING_ATTEMPT_STALE" {
		t.Fatalf("poll after adoption must be stale, got %v", werr)
	}
	if daemon.pollCount() != before {
		t.Fatal("a post-adoption poll must not reach the server")
	}
	if _, werr := onb.Abandon("pr-1"); werr == nil || werr.Code != "NATIVE_PAIRING_ALREADY_ADOPTED" {
		t.Fatalf("stop after the committed adoption must be already-adopted, got %v", werr)
	}
}

// A repeat poll while the candidate is already held is idempotent: it never
// contacts the server for a second redemption of the consumed request.
func TestRepeatPollWithHeldCandidateNeverRedeemsAgain(t *testing.T) {
	daemon := newFakePairingDaemon(t)
	onb := newTestOnboarding(t, daemon.server.URL, privateTempDir(t))
	if _, werr := onb.CreatePairing(context.Background(), "Haeryong Desktop (default)"); werr != nil {
		t.Fatal(werr)
	}
	daemon.mu.Lock()
	daemon.approved = true
	daemon.mu.Unlock()
	first, werr := onb.PollPairing(context.Background(), "pr-1")
	if werr != nil || first["state"] != "approved" {
		t.Fatalf("first poll failed: %v %v", first, werr)
	}
	second, werr := onb.PollPairing(context.Background(), "pr-1")
	if werr != nil || second["state"] != "approved" {
		t.Fatalf("repeat poll must replay the held approval, got %v %v", second, werr)
	}
	if daemon.pollCount() != 1 {
		t.Fatalf("the consumed request must not be redeemed twice: %d polls", daemon.pollCount())
	}
}

func TestPendingPairingNeverRetargetsItsSecretOrBearer(t *testing.T) {
	issuedBy, replacement := newFakePairingDaemon(t), newFakePairingDaemon(t)
	onb := newTestOnboarding(t, issuedBy.server.URL, privateTempDir(t))
	if _, failure := onb.CreatePairing(context.Background(), "original binding"); failure != nil {
		t.Fatal(failure)
	}
	issuedBy.mu.Lock()
	issuedBy.approved = true
	issuedBy.mu.Unlock()
	// Model a later successful discovery choosing another listener. The
	// already-issued attempt keeps its original transport and library pin.
	onb.mu.Lock()
	onb.endpoint = replacement.server.URL
	onb.mu.Unlock()
	if result, failure := onb.PollPairing(context.Background(), "pr-1"); failure != nil || result["state"] != "approved" {
		t.Fatalf("poll must still address the issuer: %v %v", result, failure)
	}
	activated := false
	if _, failure := onb.Adopt(context.Background(), "pr-1", func(client.Credential, uint64) { activated = true }); failure == nil || failure.Code != "NATIVE_CREDENTIAL_MISMATCHED" || activated {
		t.Fatalf("a changed live binding must refuse old adoption: %v", failure)
	}
	replacement.mu.Lock()
	requests := append([]string(nil), replacement.seen...)
	replacement.mu.Unlock()
	if len(requests) != 0 {
		t.Fatalf("the replacement listener received an old attempt's authority: %v", requests)
	}
	if _, err := os.Stat(onb.CredentialPath()); !os.IsNotExist(err) {
		t.Fatal("refused adoption must not create a remembered binding")
	}
}

func TestAdoptRejectsIncompatibleAPIAndLibraryMismatchWithoutStoring(t *testing.T) {
	for _, tc := range []struct {
		name           string
		apiVersion     string
		capsLibrary    string
		sessionLibrary string
		wantCode       string
	}{
		{"api-incompatible", "2.0", "lib-1", "lib-1", "NATIVE_API_INCOMPATIBLE"},
		{"library-mismatch", "1.0", "lib-2", "lib-1", "NATIVE_LIBRARY_MISMATCH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			daemon := newFakePairingDaemon(t)
			daemon.mu.Lock()
			daemon.apiVersion = tc.apiVersion
			daemon.capsLibrary = tc.capsLibrary
			daemon.sessionLibrary = tc.sessionLibrary
			daemon.approved = true
			daemon.mu.Unlock()
			dir := privateTempDir(t)
			onb := newTestOnboarding(t, daemon.server.URL, dir)
			if _, werr := onb.CreatePairing(context.Background(), "Haeryong Desktop (default)"); werr != nil {
				t.Fatal(werr)
			}
			if _, werr := onb.PollPairing(context.Background(), "pr-1"); werr != nil {
				t.Fatal(werr)
			}
			activated := false
			if _, werr := onb.Adopt(context.Background(), "pr-1", func(client.Credential, uint64) { activated = true }); werr == nil || werr.Code != tc.wantCode {
				t.Fatalf("expected %s, got %v", tc.wantCode, werr)
			}
			if activated {
				t.Fatal("incompatible daemon must never activate authority")
			}
			if _, err := os.Stat(onb.CredentialPath()); !os.IsNotExist(err) {
				t.Fatal("incompatible daemon must not persist a credential")
			}
		})
	}
}

func TestAdoptStoreFailureSurfacesTypedWithoutActivationOrFile(t *testing.T) {
	daemon := newFakePairingDaemon(t)
	dir := filepath.Join(privateTempDir(t), "blocked-store")
	if err := os.WriteFile(dir, []byte("owned obstruction"), 0600); err != nil {
		t.Fatal(err)
	}
	onb := newTestOnboarding(t, daemon.server.URL, dir)
	pairAndApprove(t, onb, daemon)
	activated := false
	_, werr := onb.Adopt(context.Background(), "pr-1", func(client.Credential, uint64) { activated = true })
	if werr == nil || werr.Code != "NATIVE_CREDENTIAL_STORE_FAILED" {
		t.Fatalf("read-only store must fail typed, got %v", werr)
	}
	if activated {
		t.Fatal("a failed store must not activate authority")
	}
	if content, err := os.ReadFile(dir); err != nil || string(content) != "owned obstruction" {
		t.Fatal("failed store must preserve the existing obstruction")
	}
	// The consumed attempt reports adoption failure, never a clean stop.
	if _, werr := onb.Abandon("pr-1"); werr == nil || werr.Code != "NATIVE_PAIRING_ADOPT_FAILED" {
		t.Fatalf("abandon after failed adoption must report adopt-failed, got %v", werr)
	}
}

func TestAdoptionStoreOutcomesPreserveTruthfulAuthority(t *testing.T) {
	for _, mode := range []string{"before-save", "after-rename", "unreadable-readback"} {
		t.Run(mode, func(t *testing.T) {
			daemon := newFakePairingDaemon(t)
			onb := newTestOnboarding(t, daemon.server.URL, privateTempDir(t))
			pairAndApprove(t, onb, daemon)
			onb.testSaveCredential = func(path string, credential client.Credential) error {
				if mode == "before-save" {
					return os.ErrPermission
				}
				if err := client.SaveCredential(path, credential); err != nil {
					t.Fatal(err)
				}
				return fmt.Errorf("injected post-rename error")
			}
			if mode == "unreadable-readback" {
				onb.testLoadCredential = func(string) (client.Credential, error) {
					return client.Credential{}, os.ErrPermission
				}
			}
			activated := false
			result, failure := onb.Adopt(context.Background(), "pr-1", func(client.Credential, uint64) { activated = true })
			switch mode {
			case "after-rename":
				if failure != nil || !activated || result["durability"] != "unconfirmed" {
					t.Fatalf("committed readback must activate with a durability warning: %v %v", result, failure)
				}
			case "before-save", "unreadable-readback":
				code := "NATIVE_CREDENTIAL_STORE_FAILED"
				if mode == "unreadable-readback" {
					code = "NATIVE_CREDENTIAL_STORE_UNCERTAIN"
				}
				if activated || failure == nil || failure.Code != code {
					t.Fatalf("%s must not activate and must report %s: %v", mode, code, failure)
				}
				if _, abandoned := onb.Abandon("pr-1"); abandoned == nil || abandoned.Code != "NATIVE_PAIRING_ADOPT_FAILED" {
					t.Fatalf("failed/uncertain adoption must never report a clean stop: %v", abandoned)
				}
			}
			_, err := client.LoadCredential(onb.CredentialPath())
			if mode == "before-save" && !os.IsNotExist(err) {
				t.Fatal("pre-save failure must leave no binding")
			}
			if mode != "before-save" && err != nil {
				t.Fatalf("post-rename failure must preserve the committed binding: %v", err)
			}
		})
	}
}

func TestRememberedBootBindsTheExactEndpoint(t *testing.T) {
	daemon := newFakePairingDaemon(t)
	dir := privateTempDir(t)
	onb := newTestOnboarding(t, daemon.server.URL, dir)
	pairAndApprove(t, onb, daemon)
	if _, werr := onb.Adopt(context.Background(), "pr-1", func(client.Credential, uint64) {}); werr != nil {
		t.Fatal(werr)
	}
	// A same-selection boot against a DIFFERENT endpoint (e.g. a changed
	// listener port) must refuse the stored bearer instead of silently
	// reusing the pinned credential at the new endpoint.
	other, err := New(Config{CredentialDir: dir, ProductAPI: "1.0", Endpoint: "http://127.0.0.1:9271", ExpectedLibrary: "lib-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, werr := other.RememberedToken(context.Background()); werr == nil || werr.Code != "NATIVE_CREDENTIAL_MISMATCHED" {
		t.Fatalf("endpoint drift must refuse the credential, got %v", werr)
	}
	same, err := New(Config{CredentialDir: dir, ProductAPI: "1.0", Endpoint: daemon.server.URL, ExpectedLibrary: "lib-1"})
	if err != nil {
		t.Fatal(err)
	}
	token, werr := same.RememberedToken(context.Background())
	if werr != nil || token != firstRedeemedToken {
		t.Fatalf("matching binding must attach remembered, got %q %v", token, werr)
	}
}

// Without a fixed fixture binding the production boot must not silently skip
// endpoint validation: it resolves a verified binding or fails typed.
func TestRememberedBootWithoutBindingFailsTyped(t *testing.T) {
	dir := privateTempDir(t)
	onb, err := New(Config{Selection: Selection{DataDir: filepath.Join(dir, "missing-profile")}, CredentialDir: dir, ProductAPI: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if werr := client.SaveCredential(onb.CredentialPath(), client.Credential{Endpoint: "http://127.0.0.1:9270", Profile: "default", LibraryID: "lib-1", Token: strings.Repeat("ab", 32)}); werr != nil {
		t.Fatal(werr)
	}
	if _, werr := onb.RememberedToken(context.Background()); werr == nil || werr.Code != "NATIVE_DAEMON_UNAVAILABLE" {
		t.Fatalf("unbound boot must fail typed, got %v", werr)
	}
}

func TestForgetDecisionsProtectNewerBindings(t *testing.T) {
	daemon := newFakePairingDaemon(t)
	dir := privateTempDir(t)
	onb := newTestOnboarding(t, daemon.server.URL, dir)
	pairAndApprove(t, onb, daemon)
	if _, werr := onb.Adopt(context.Background(), "pr-1", func(client.Credential, uint64) {}); werr != nil {
		t.Fatal(werr)
	}
	outcome, err := onb.Forget("other-token")
	if err != nil || outcome != ForgetForeign {
		t.Fatalf("foreign bearer must never unlink, got %v %v", outcome, err)
	}
	if _, err := os.Stat(onb.CredentialPath()); err != nil {
		t.Fatal("foreign forget must retain the file")
	}
	outcome, err = onb.Forget(firstRedeemedToken)
	if err != nil || outcome != ForgetForgotten {
		t.Fatalf("exact bearer must be forgotten, got %v %v", outcome, err)
	}
	if _, err := os.Stat(onb.CredentialPath()); !os.IsNotExist(err) {
		t.Fatal("forgotten binding must be gone")
	}
}

func TestRememberedBootHonestAbsence(t *testing.T) {
	onb := newTestOnboarding(t, "http://127.0.0.1:1", privateTempDir(t))
	token, werr := onb.RememberedToken(context.Background())
	if werr != nil || token != "" {
		t.Fatalf("absent credential must be an honest empty boot, got %q %v", token, werr)
	}
}

// A paused post-commit activation must never install an older bearer after a
// newer adoption committed and activated: the generation check inside the
// serialized activation hold skips the superseded callback.
func TestSupersededPostCommitActivationNeverInstallsOldBearer(t *testing.T) {
	daemon := newFakePairingDaemon(t)
	onb := newTestOnboarding(t, daemon.server.URL, privateTempDir(t))
	pairAndApprove(t, onb, daemon)
	paused, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()
	onb.testAfterCommit = func() { close(paused); <-release }
	t.Cleanup(func() { onb.testAfterCommit = nil })
	activations := make(chan string, 2)
	firstDone := make(chan *Error, 1)
	go func() {
		_, werr := onb.Adopt(context.Background(), "pr-1", func(credential client.Credential, attemptGen uint64) { activations <- credential.Token })
		firstDone <- werr
	}()
	<-paused
	// A complete second adoption (new attempt, new redemption) commits and
	// activates while the first activation is paused.
	onb.testAfterCommit = nil
	if _, werr := onb.CreatePairing(context.Background(), "Haeryong Desktop (default)"); werr != nil {
		t.Fatal(werr)
	}
	if _, werr := onb.PollPairing(context.Background(), "pr-1"); werr != nil {
		t.Fatal(werr)
	}
	if _, werr := onb.Adopt(context.Background(), "pr-1", func(credential client.Credential, attemptGen uint64) { activations <- credential.Token }); werr != nil {
		t.Fatal(werr)
	}
	releaseAll()
	if werr := <-firstDone; werr != nil {
		t.Fatalf("first adoption committed earlier and must report success: %v", werr)
	}
	close(activations)
	installed := []string{}
	for token := range activations {
		installed = append(installed, token)
	}
	if len(installed) != 1 || installed[0] != redeemToken(2) {
		t.Fatalf("only the newest committed bearer may activate, got %v", installed)
	}
	credential, err := client.LoadCredential(onb.CredentialPath())
	if err != nil || credential.Token != redeemToken(2) {
		t.Fatalf("disk must hold the newest adoption: %+v %v", credential, err)
	}
}

// A slower older pairing create must never replace a newer request's slot:
// the generation is reserved before the async POST and revalidated before
// publishing.
func TestDelayedOlderCreateNeverReplacesNewerPairing(t *testing.T) {
	daemon := newFakePairingDaemon(t)
	onb := newTestOnboarding(t, daemon.server.URL, privateTempDir(t))
	stallArrived, stallRelease := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(stallRelease) }) }
	defer releaseAll()
	daemon.mu.Lock()
	daemon.createStall = func() { close(stallArrived); <-stallRelease }
	daemon.mu.Unlock()
	t.Cleanup(func() {
		daemon.mu.Lock()
		daemon.createStall = nil
		daemon.mu.Unlock()
	})
	older := make(chan *Error, 1)
	go func() {
		_, werr := onb.CreatePairing(context.Background(), "Haeryong Desktop (older)")
		older <- werr
	}()
	<-stallArrived
	if _, werr := onb.CreatePairing(context.Background(), "Haeryong Desktop (newer)"); werr != nil {
		t.Fatal(werr)
	}
	releaseAll()
	if werr := <-older; werr == nil || werr.Code != "NATIVE_PAIRING_ATTEMPT_STALE" {
		t.Fatalf("delayed older create must be stale, got %v", werr)
	}
	// The newer intent owns the slot and keeps working.
	daemon.mu.Lock()
	daemon.approved = true
	daemon.mu.Unlock()
	approved, werr := onb.PollPairing(context.Background(), "pr-1")
	if werr != nil || approved["state"] != "approved" {
		t.Fatalf("newer attempt must redeem, got %v %v", approved, werr)
	}
}

func TestDiscardInvalidatesAnInFlightPairingCreate(t *testing.T) {
	daemon := newFakePairingDaemon(t)
	onb := newTestOnboarding(t, daemon.server.URL, privateTempDir(t))
	arrived, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	releaseAll := func() { once.Do(func() { close(release) }) }
	defer releaseAll()
	daemon.mu.Lock()
	daemon.createStall = func() { close(arrived); <-release }
	daemon.mu.Unlock()
	done := make(chan *Error, 1)
	go func() {
		_, failure := onb.CreatePairing(context.Background(), "closing window")
		done <- failure
	}()
	<-arrived
	onb.DiscardAttempt()
	releaseAll()
	if failure := <-done; failure == nil || failure.Code != "NATIVE_PAIRING_ATTEMPT_STALE" {
		t.Fatalf("a create response after disposal must stay stale: %v", failure)
	}
	if _, failure := onb.PollPairing(context.Background(), "pr-1"); failure == nil || failure.Code != "NATIVE_PAIRING_ATTEMPT_STALE" {
		t.Fatalf("disposal must not resurrect a pending attempt: %v", failure)
	}
}

// Production-order remembered boot: the verified binding resolves first and
// populates the library pin, so a stored foreign library at the SAME endpoint
// is refused instead of silently skipped by an empty pre-discovery pin.
func TestRememberedBootResolvesBindingBeforeLibraryComparison(t *testing.T) {
	dir := privateTempDir(t)
	profile, err := service.Initialize(service.Selection{Profile: "default", DataDir: filepath.Join(dir, "profile")})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := service.NewID()
	if err != nil {
		t.Fatal(err)
	}
	control := &localcontrol.HTTP{}
	identity, err := control.Start(context.Background(), profile, service.Identity{LibraryID: profile.LibraryID, ProfileID: profile.ProfileID, InstanceID: instance, API: service.APIVersion}, 0, func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := control.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	encoded, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile.DataDir, "runtime", "endpoint.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(dir, "credentials")
	if err := os.Mkdir(credentials, 0700); err != nil {
		t.Fatal(err)
	}
	onb, err := New(Config{Selection: Selection{DataDir: profile.DataDir}, CredentialDir: credentials, ProductAPI: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if onb.ExpectedLibrary() != "" {
		t.Fatal("the library must be unknown until signed discovery runs")
	}
	foreign := client.Credential{Endpoint: identity.Endpoint, Profile: "default", LibraryID: "lib-foreign", Token: strings.Repeat("ab", 32)}
	if err := client.SaveCredential(onb.CredentialPath(), foreign); err != nil {
		t.Fatal(err)
	}
	if _, werr := onb.RememberedToken(context.Background()); werr == nil || werr.Code != "NATIVE_CREDENTIAL_MISMATCHED" {
		t.Fatalf("foreign stored library at the same endpoint must be refused, got %v", werr)
	}
	matching := client.Credential{Endpoint: identity.Endpoint, Profile: "default", LibraryID: profile.LibraryID, Token: strings.Repeat("ab", 32)}
	if err := client.SaveCredential(onb.CredentialPath(), matching); err != nil {
		t.Fatal(err)
	}
	token, werr := onb.RememberedToken(context.Background())
	if werr != nil || token != strings.Repeat("ab", 32) {
		t.Fatalf("matching binding must attach, got %q %v", token, werr)
	}
}
