package readerfacade

// M4-A2 remembered boot/reattach focus: the deterministic ownership races
// (late load vs adoption, late load vs completed logout+disposal, concurrent
// empty-token attaches) and the cross-restart residual-credential outcomes
// (server revoke succeeded vs still-live session). The race tests assert the
// OUTCOME — the current authority is never replaced or resurrected — without
// pinning which install guard rejected the late load.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"haeryong.local/haeryong/desktop/onboarding"
	"haeryong.local/haeryong/internal/adapters/client"
)

// saveRememberedCredential writes a matching remembered binding without
// activating it, standing in for a credential persisted by an earlier
// companion run.
func saveRememberedCredential(t *testing.T, service *ReaderService, endpoint, token string) {
	t.Helper()
	if err := os.Chmod(filepath.Dir(service.onboarding.CredentialPath()), 0700); err != nil {
		t.Fatal(err)
	}
	credential := client.Credential{Endpoint: endpoint, Profile: "default", LibraryID: "lib-1", Token: token}
	if err := client.SaveCredential(service.onboarding.CredentialPath(), credential); err != nil {
		t.Fatal(err)
	}
}

func approveFakeDaemon(daemon *fakeOnboardingDaemon) {
	daemon.mu.Lock()
	daemon.approved = true
	daemon.rateLimitFirst = false
	daemon.mu.Unlock()
}

func pairingCreates(daemon *fakeOnboardingDaemon) int {
	daemon.mu.Lock()
	defer daemon.mu.Unlock()
	return daemon.pairingCreates
}

// A remembered token the fake daemon NEVER issues: lateA→newB proofs rely
// on A and B being distinct values.
const rememberedTokenA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa0001"

// A matching remembered credential attaches through the signed binding with
// ZERO forced pairings: the endpoint and bearer install together and the
// attach chain revalidates them.
func TestRememberedCredentialAttachesWithoutForcedPairing(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	dir := t.TempDir()
	service := newOnboardingFacade(t, daemon, dir)
	saveRememberedCredential(t, service, daemon.server.URL, rememberedTokenA)
	attached := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	if attached["public_origin"] != daemon.server.URL {
		t.Fatalf("remembered attach must publish the stored binding endpoint, got %v", attached["public_origin"])
	}
	session := attached["session"].(map[string]any)
	if session["session_id"] != "s-9" {
		t.Fatalf("remembered attach must attach the remembered session, got %v", session)
	}
	if creates := pairingCreates(daemon); creates != 0 {
		t.Fatalf("remembered reattach must not force a new pairing, saw %d", creates)
	}
	service.mu.Lock()
	token := service.token
	service.mu.Unlock()
	if token != rememberedTokenA {
		t.Fatal("the remembered bearer must be installed exactly once")
	}
}

// Without a verified binding (an unresolvable selection: daemon stopped and
// no signed discovery) the remembered boot fails typed instead of silently
// skipping endpoint validation, and no bearer leaves the process.
func TestRememberedCredentialStoppedDaemonPropagatesTyped(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	dir := t.TempDir()
	onb, err := onboarding.New(onboarding.Config{CredentialDir: dir, ProductAPI: "1.0", Selection: onboarding.Selection{DataDir: filepath.Join(dir, "missing-profile")}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewReaderServiceWithOnboarding(daemon.server.URL, filepath.Join(dir, "receipts"), "", onb)
	if werr := invokeFail(t, service, "attachSession", "{}"); werr.Status != 503 || werr.Code != "NATIVE_DAEMON_UNAVAILABLE" {
		t.Fatalf("stopped daemon must propagate typed through the remembered path, got %+v", werr)
	}
	if daemon.saw("/api/v1/session") || daemon.saw("/api/v1/capabilities") {
		t.Fatalf("an unresolvable binding must send zero bearer requests, saw %v", daemon.seenPaths())
	}
}

// A stored endpoint that does not match the signed binding never receives
// the bearer: the mismatch fails typed before any request is dispatched.
func TestRememberedCredentialEndpointMismatchSendsZeroBearers(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	dir := t.TempDir()
	service := newOnboardingFacade(t, daemon, dir)
	foreign := client.Credential{Endpoint: "http://127.0.0.1:1", Profile: "default", LibraryID: "lib-1", Token: rememberedTokenA}
	if err := os.Chmod(filepath.Dir(service.onboarding.CredentialPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := client.SaveCredential(service.onboarding.CredentialPath(), foreign); err != nil {
		t.Fatal(err)
	}
	if werr := invokeFail(t, service, "attachSession", "{}"); werr.Status != 409 || werr.Code != "NATIVE_CREDENTIAL_MISMATCHED" {
		t.Fatalf("foreign stored endpoint must fail typed, got %+v", werr)
	}
	if daemon.saw("/api/v1/session") {
		t.Fatal("the signed daemon binding saw no request; the foreign listener must receive zero bearers")
	}
}

// R1: a late remembered load cannot replace an adoption that activated
// while the load was in flight. A is a token the daemon never issues; B is
// the fake's first real redemption, so the final authority must be exactly
// B — the proof catches A replacing B as well as B staying installed.
func TestLateRememberedLoadCannotReplaceAdoptedAuthority(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	dir := t.TempDir()
	service := newOnboardingFacade(t, daemon, dir)
	saveRememberedCredential(t, service, daemon.server.URL, rememberedTokenA)
	arrived, release := make(chan struct{}), make(chan struct{})
	service.testAfterRememberedLoad = func() { close(arrived); <-release }
	attached := make(chan *wireError, 1)
	go func() {
		_, werr := service.invoke("attachSession", nil)
		attached <- werr
	}()
	<-arrived
	// While the remembered load is parked, a full pairing adopts and
	// activates credential B (the fake's first real redemption).
	approveFakeDaemon(daemon)
	adoptApproved(t, service, daemon)
	close(release)
	werr := <-attached
	if werr == nil {
		t.Fatal("a late remembered load must not attach over the adopted authority")
	}
	if werr.Code != "NATIVE_SESSION_REPLACED" {
		t.Fatalf("unexpected typed rejection, got %+v", werr)
	}
	// The adopted authority B stays intact: the exact endpoint/bearer pair
	// of the activation, never the remembered A.
	service.mu.Lock()
	token, endpoint := service.token, service.base
	service.mu.Unlock()
	if token != redeemBearer(1) || token == rememberedTokenA {
		t.Fatalf("the adopted bearer B must stay installed, got %q", token)
	}
	if endpoint != daemon.server.URL {
		t.Fatalf("the adopted endpoint must stay installed, got %q", endpoint)
	}
	// B still attaches normally and serves requests: the rejected late load
	// corrupted nothing.
	invokeOK(t, service, "attachSession", "{}")
	if value := invokeOK(t, service, "capabilities", "{}"); value == nil {
		t.Fatal("the adopted authority must still serve requests")
	}
}

// R2: a late remembered load after a completed logout and owned disposal
// never resurrects the logged-out state. The assertion is the outcome (no
// authority, honest logged-out answers), not which install guard fired.
func TestLateRememberedLoadAfterLogoutAndDisposeNeverResurrects(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	daemon.mu.Lock()
	daemon.deleteSession = true
	daemon.mu.Unlock()
	dir := t.TempDir()
	service := newOnboardingFacade(t, daemon, dir)
	saveRememberedCredential(t, service, daemon.server.URL, rememberedTokenA)
	arrived, release := make(chan struct{}), make(chan struct{})
	service.testAfterRememberedLoad = func() { close(arrived); <-release }
	attached := make(chan *wireError, 1)
	go func() {
		_, werr := service.invoke("attachSession", nil)
		attached <- werr
	}()
	<-arrived
	// While parked: adopt B (a real redemption distinct from remembered A),
	// attach it, log out of it (local forget completes) and dispose the
	// attachment (the completed cleanup clears logoutBearer — bearer
	// equality alone must not be the guard).
	approveFakeDaemon(daemon)
	adoptApproved(t, service, daemon)
	publication := invokeOK(t, service, "attachSession", "{}").(map[string]any)
	invokeOK(t, service, "revokeOwnSession", "{}")
	invokeOK(t, service, "disposeSession", fmt.Sprintf(`{"attachment_id":%q}`, publication["attachment_id"]))
	if _, err := os.Stat(service.onboarding.CredentialPath()); !os.IsNotExist(err) {
		t.Fatal("the completed logout must have forgotten the credential file")
	}
	close(release)
	werr := <-attached
	if werr == nil {
		t.Fatal("a late remembered load after a completed logout must fail")
	}
	service.mu.Lock()
	resurrected := service.token != ""
	service.mu.Unlock()
	if resurrected {
		t.Fatal("no authority may be resurrected by the late load")
	}
	if werr := invokeFail(t, service, "capabilities", "{}"); werr.Code != "NATIVE_SESSION_NOT_ATTACHED" && werr.Code != "NATIVE_SESSION_LOGGED_OUT" {
		t.Fatalf("post-rejection operations must fail closed, got %+v", werr)
	}
	if werr := invokeFail(t, service, "attachSession", "{}"); werr.Code != "NATIVE_SESSION_LOGGED_OUT" {
		t.Fatalf("the logged-out state must stay honest after the rejected load, got %+v", werr)
	}
}

// R3: two concurrent empty-token attaches through the remembered path
// publish exactly one era. BOTH loads are held at the post-load barrier
// (each captured the empty-token/pre-load state and completed its
// credential read) before either install is released, so the race is
// deterministic rather than an accident of scheduling: the loser can only
// be rejected by the install guard, with the final authority unchanged.
func TestConcurrentRememberedAttachesPublishExactlyOneEra(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	dir := t.TempDir()
	service := newOnboardingFacade(t, daemon, dir)
	saveRememberedCredential(t, service, daemon.server.URL, rememberedTokenA)
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	service.testAfterRememberedLoad = func() {
		arrived <- struct{}{}
		<-release
	}
	results := make(chan *wireError, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, werr := service.invoke("attachSession", nil)
			results <- werr
		}()
	}
	// Both calls parked between their completed load and their install.
	<-arrived
	<-arrived
	service.mu.Lock()
	stillEmpty := service.token == ""
	service.mu.Unlock()
	if !stillEmpty {
		t.Fatal("both parked loads must have captured the empty-token state")
	}
	close(release)
	published, replaced := 0, 0
	var loser *wireError
	for i := 0; i < 2; i++ {
		werr := <-results
		switch {
		case werr == nil:
			published++
		case werr.Code == "NATIVE_SESSION_REPLACED":
			replaced++
			loser = werr
		default:
			t.Fatalf("unexpected concurrent outcome, got %+v", werr)
		}
	}
	if published != 1 || replaced != 1 || loser == nil {
		t.Fatalf("exactly one concurrent remembered attach may publish, got %d published / %d replaced", published, replaced)
	}
	service.mu.Lock()
	token, attachment := service.token, service.attachmentID
	service.mu.Unlock()
	if token != rememberedTokenA || attachment == "" {
		t.Fatalf("the single published era must hold the remembered pair, got token set=%v attachment=%q", token != rememberedTokenA, attachment)
	}
}

// S1: a residual credential whose server session was already revoked (the
// logout's revoke succeeded but the local forget had failed) autoloads and
// fails the first session read typed 401: no authority exists and no forced
// pairing happened.
func TestResidualCredentialWithRevokedSessionFailsTyped(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	dir := t.TempDir()
	first := newOnboardingFacade(t, daemon, dir)
	approveFakeDaemon(daemon)
	adoptApproved(t, first, daemon)
	// Simulate the cross-restart residue: the credential file survives in a
	// FRESH facade (a new companion boot) while the server session is dead.
	daemon.mu.Lock()
	daemon.sessionUnauthorized = true
	daemon.mu.Unlock()
	restarted := newOnboardingFacade(t, daemon, dir)
	werr := invokeFail(t, restarted, "attachSession", "{}")
	if werr.Status != 401 {
		t.Fatalf("a server-revoked remembered session must fail typed 401, got %+v", werr)
	}
	if werr := invokeFail(t, restarted, "capabilities", "{}"); werr.Code != "NATIVE_SESSION_NOT_ATTACHED" {
		t.Fatalf("no authority may exist after the revoked residual load, got %+v", werr)
	}
	if creates := pairingCreates(daemon); creates != 1 {
		t.Fatalf("only the original adoption may create a pairing, saw %d", creates)
	}
}

// S2: a residual credential whose revoke never completed still identifies a
// live server session: the restart reattaches it without a new pairing and
// a retry logout completes the forget and the revoke.
func TestResidualCredentialWithLiveSessionReattachesAndLogoutRetries(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	dir := t.TempDir()
	first := newOnboardingFacade(t, daemon, dir)
	approveFakeDaemon(daemon)
	adoptApproved(t, first, daemon)
	restarted := newOnboardingFacade(t, daemon, dir)
	attached := invokeOK(t, restarted, "attachSession", "{}").(map[string]any)
	if attached["attachment_id"] == "" || attached["public_origin"] != daemon.server.URL {
		t.Fatalf("the live residual credential must reattach, got %v", attached)
	}
	if creates := pairingCreates(daemon); creates != 1 {
		t.Fatalf("the residual reattach must not force a new pairing, saw %d", creates)
	}
	// The retry logout now completes both the local forget and the revoke.
	daemon.mu.Lock()
	daemon.deleteSession = true
	daemon.mu.Unlock()
	invokeOK(t, restarted, "revokeOwnSession", "{}")
	if _, err := os.Stat(restarted.onboarding.CredentialPath()); !os.IsNotExist(err) {
		t.Fatal("the retry logout must complete the local forget")
	}
}

// The production onboarding constructor stays diagnostic-free: no receipt
// file appears, while the harness constructor keeps writing it.
func TestProductionConstructorWritesNoReceiptFiles(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	dir := t.TempDir()
	service := newOnboardingFacade(t, daemon, dir)
	approveFakeDaemon(daemon)
	adoptApproved(t, service, daemon)
	invokeOK(t, service, "attachSession", "{}")
	matches, err := filepath.Glob(filepath.Join(dir, "receipts", "native-reader-receipts-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("production construction must not write receipt files, found %v", matches)
	}
}

// The harness constructor keeps the historical receipt file with the
// historical name and array shape through its built-in observer.
func TestHarnessConstructorKeepsReceiptFile(t *testing.T) {
	daemon := newFakeOnboardingDaemon(t)
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	onb, err := onboarding.New(onboarding.Config{CredentialDir: dir, ProductAPI: "1.0", Endpoint: daemon.server.URL, ExpectedLibrary: "lib-1"})
	if err != nil {
		t.Fatal(err)
	}
	service := NewReaderService(daemon.server.URL, state, "")
	service.onboarding = onb
	approveFakeDaemon(daemon)
	adoptApproved(t, service, daemon)
	raw, err := os.ReadFile(filepath.Join(state, fmt.Sprintf("native-reader-receipts-%d.json", os.Getpid())))
	if err != nil {
		t.Fatal("the harness constructor must keep the receipt file", err)
	}
	if len(raw) == 0 || raw[0] != '[' {
		t.Fatalf("the receipt file must stay a JSON array, got %q", string(raw[:min(32, len(raw))]))
	}
}
