package authingress

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fixtureRequest(h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "https://fixture.test"+path, bytes.NewReader(data))
	r.Header.Set("Origin", "https://fixture.test")
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestStoreFailuresRemainUnavailableWithoutConsumingCredentials(t *testing.T) {
	for _, fault := range []string{"begin", "query"} {
		t.Run(fault, func(t *testing.T) {
			s := fixture(t)
			owner := trusted(t, s)
			id := deviceID(t, s, owner)
			pending, err := s.createDevice("Still pending")
			if err != nil {
				t.Fatal(err)
			}
			approved, err := s.createDevice("Still approved")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.decide(approved.ID, approved.Code, "approved", owner, false); err != nil {
				t.Fatal(err)
			}
			h := handler(s, "", map[string]bool{"fixture.test": true}, map[string]bool{"https://fixture.test": true})
			if fault == "begin" {
				if err := s.db.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				// Preserve real rows while making both authorization and pairing queries fail.
				for _, table := range []string{"sessions", "pairing"} {
					if _, err := s.db.Exec("ALTER TABLE " + table + " RENAME TO held_" + table); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, path := range []string{"/session", "/devices", "/private/data", "/private/assets", "/private/download", "/private/events"} {
				w := fixtureRequest(h, "GET", path, owner, nil)
				if w.Code != 503 || w.Header().Get("Retry-After") != "2" {
					t.Fatalf("%s: %d", path, w.Code)
				}
			}
			for path, body := range map[string]any{
				"/pair/poll":            map[string]string{"ID": pending.ID, "Secret": pending.Secret},
				"/pair/decision":        map[string]string{"ID": pending.ID, "Code": pending.Code, "Decision": "approved"},
				"/pair/redeem":          map[string]string{"ID": approved.ID, "Secret": approved.Secret},
				"/devices/revoke":       map[string]string{"ID": id},
				"/private/action":       map[string]string{"OperationID": "uncommitted-failure"},
				"/private/device-proof": map[string]string{"phase": "connected"},
			} {
				w := fixtureRequest(h, "POST", path, owner, body)
				if w.Code != 503 || w.Header().Get("Retry-After") != "2" {
					t.Fatalf("%s: %d", path, w.Code)
				}
			}
			if fault == "query" {
				for _, table := range []string{"sessions", "pairing"} {
					if _, err := s.db.Exec("ALTER TABLE held_" + table + " RENAME TO " + table); err != nil {
						t.Fatal(err)
					}
				}
				if w := fixtureRequest(h, "GET", "/session", owner, nil); w.Code != 200 {
					t.Fatal("valid session destroyed", w.Code)
				}
				if value, err := s.poll(pending.ID, pending.Secret); err != nil || value != "pending" {
					t.Fatal("pending request consumed", err)
				}
				if _, err := s.redeem(approved.ID, approved.Secret, 0); err != nil {
					t.Fatal("approved request consumed", err)
				}
			}
		})
	}
}

func TestRedemptionLifetimeBoundsLeaveApprovedRequestIntact(t *testing.T) {
	s := fixture(t)
	r, err := s.create()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.decide(r.ID, r.Code, "approved", "", true); err != nil {
		t.Fatal(err)
	}
	h := handler(s, "", map[string]bool{"fixture.test": true}, map[string]bool{"https://fixture.test": true})
	for _, seconds := range []int64{-1, 86401} {
		w := fixtureRequest(h, "POST", "/pair/redeem", "", map[string]any{"ID": r.ID, "Secret": r.Secret, "LifetimeSeconds": seconds})
		if w.Code != 400 {
			t.Fatal("invalid lifetime accepted", seconds, w.Code)
		}
	}
	w := fixtureRequest(h, "POST", "/pair/redeem", "", map[string]any{"ID": r.ID, "Secret": r.Secret, "LifetimeSeconds": 0})
	if w.Code != 200 {
		t.Fatal("invalid lifetime consumed approval", w.Code)
	}
}

type heldPairBody struct {
	entered chan<- struct{}
	release <-chan struct{}
	body    io.Reader
	held    bool
}

func (b *heldPairBody) Read(p []byte) (int, error) {
	if !b.held {
		b.held = true
		b.entered <- struct{}{}
		<-b.release
	}
	return b.body.Read(p)
}
func (*heldPairBody) Close() error { return nil }

func TestConcurrentPairAttemptsReserveBudgetAndRollover(t *testing.T) {
	s := fixture(t)
	now := time.Now()
	// Requests sample a frozen clock; advance only after the old requests are held in their bodies.
	s.now = func() time.Time { return now }
	h := handler(s, "", map[string]bool{"fixture.test": true}, map[string]bool{"https://fixture.test": true})
	entered, release, done := make(chan struct{}, 8), make(chan struct{}), make(chan int, 8)
	for i := 0; i < 8; i++ {
		go func() {
			r := httptest.NewRequest("POST", "https://fixture.test/pair/poll", nil)
			r.Body = &heldPairBody{entered: entered, release: release, body: strings.NewReader("bad JSON")}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			done <- w.Code
		}()
	}
	for i := 0; i < 8; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("attempt did not enter handler")
		}
	}
	w := fixtureRequest(h, "POST", "/pair/redeem", "", nil)
	if w.Code != 429 || w.Header().Get("Retry-After") != "2" {
		t.Fatal("concurrent requests bypassed reservation", w.Code)
	}
	// The held bodies will fail decoding before consulting the store clock.
	now = now.Add(time.Minute)
	if w := fixtureRequest(h, "POST", "/pair/poll", "", nil); w.Code != 403 {
		t.Fatal("new window blocked", w.Code)
	}
	close(release)
	for i := 0; i < 8; i++ {
		if code := <-done; code != 400 {
			t.Fatal(code)
		}
	}
	for i := 0; i < 7; i++ {
		if w := fixtureRequest(h, "POST", "/pair/decision", "", nil); w.Code != 403 {
			t.Fatal("old in-flight window polluted new one", w.Code)
		}
	}
	if w := fixtureRequest(h, "POST", "/pair/redeem", "", nil); w.Code != 429 || w.Header().Get("Retry-After") != "60" {
		t.Fatal("completed failures bypassed budget", w.Code)
	}
}

func TestFailedAttemptBudgetSharedAcrossPairingRoutes(t *testing.T) {
	for _, source := range []string{"/pair/poll", "/pair/decision", "/pair/redeem"} {
		t.Run(source, func(t *testing.T) {
			s := fixture(t)
			h := handler(s, "", map[string]bool{"fixture.test": true}, map[string]bool{"https://fixture.test": true})
			for i := 0; i < 8; i++ {
				if w := fixtureRequest(h, "POST", source, "", map[string]string{}); w.Code != 403 {
					t.Fatal(w.Code)
				}
			}
			for _, target := range []string{"/pair/poll", "/pair/decision", "/pair/redeem"} {
				if w := fixtureRequest(h, "POST", target, "", nil); w.Code != 429 {
					t.Fatal("route bypassed shared budget", target, w.Code)
				}
			}
		})
	}
}

func TestNonfailurePairResponsesReleaseBudget(t *testing.T) {
	for _, status := range []int{200, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s := fixture(t)
			now := time.Now()
			s.now = func() time.Time { return now }
			r, err := s.create()
			if err != nil {
				t.Fatal(err)
			}
			h := handler(s, "", map[string]bool{"fixture.test": true}, map[string]bool{"https://fixture.test": true})
			body := map[string]string{"ID": r.ID, "Secret": r.Secret}
			if status == 429 {
				if w := fixtureRequest(h, "POST", "/pair/poll", "", body); w.Code != 200 {
					t.Fatal(w.Code)
				}
			}
			if status == 503 {
				if _, err := s.db.Exec("ALTER TABLE pairing RENAME TO held_pairing"); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 8; i++ {
				if status == 200 {
					now = now.Add(2 * time.Second)
				}
				w := fixtureRequest(h, "POST", "/pair/poll", "", body)
				if w.Code != status {
					t.Fatal("nonfailure consumed strike", w.Code)
				}
				if status == 429 && w.Header().Get("Retry-After") != "2" {
					t.Fatal("poll throttle became failure lockout")
				}
			}
			if status == 503 {
				if _, err := s.db.Exec("ALTER TABLE held_pairing RENAME TO pairing"); err != nil {
					t.Fatal(err)
				}
			}
			now = now.Add(2 * time.Second)
			if w := fixtureRequest(h, "POST", "/pair/poll", "", body); w.Code != 200 {
				t.Fatal("ordinary responses exhausted budget", w.Code)
			}
		})
	}
}

func TestMalformedPairingBodiesConsumeFailureBudget(t *testing.T) {
	s := fixture(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	h := handler(s, "", map[string]bool{"fixture.test": true}, map[string]bool{"https://fixture.test": true})
	for i := 0; i < 8; i++ {
		r := httptest.NewRequest("POST", "https://fixture.test/pair/poll", strings.NewReader("{"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal("malformed request did not use the expected strike", w.Code)
		}
	}
	w := fixtureRequest(h, "POST", "/pair/redeem", "", map[string]string{})
	if w.Code != 429 || w.Header().Get("Retry-After") != "60" {
		t.Fatal("eight same-window400 responses did not exhaust shared failure budget", w.Code)
	}
}

func TestFailedCreatesReleaseSlotsAndPendingCapExplainsRecovery(t *testing.T) {
	s := fixture(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	h := handler(s, "", map[string]bool{"fixture.test": true}, map[string]bool{"https://fixture.test": true})
	if _, err := s.db.Exec("ALTER TABLE pairing RENAME TO held_pairing"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if w := fixtureRequest(h, "POST", "/pair", "", map[string]string{}); w.Code != 503 || w.Header().Get("Retry-After") != "2" {
			t.Fatal("failed create spent slot", w.Code)
		}
	}
	if _, err := s.db.Exec("ALTER TABLE held_pairing RENAME TO pairing"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := s.create(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		w := fixtureRequest(h, "POST", "/pair", "", map[string]string{})
		if w.Code != 429 || !strings.Contains(w.Body.String(), "five unexpired") || !strings.Contains(w.Body.String(), "cannot cancel") || !strings.Contains(w.Body.String(), "finish or expire") {
			t.Fatal("pending cap lost recovery explanation", w.Code)
		}
	}
	now = now.Add(5 * time.Minute)
	if w := fixtureRequest(h, "POST", "/pair", "", map[string]string{}); w.Code != 201 {
		t.Fatal("expired pending cap blocked create", w.Code)
	}
}

func TestDecisionMetadataMatchesEachSameLabelRequest(t *testing.T) {
	s := fixture(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	owner := trusted(t, s)
	h := handler(s, "", map[string]bool{"fixture.test": true}, map[string]bool{"https://fixture.test": true})
	var previous int64
	for i := 0; i < 2; i++ {
		now = now.Add(time.Second)
		r, err := s.createDevice("Same descriptive label")
		if err != nil {
			t.Fatal(err)
		}
		w := fixtureRequest(h, "POST", "/pair/decision", owner, map[string]string{"ID": r.ID, "Code": r.Code, "Decision": "approved"})
		var accepted decisionRecord
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &accepted) != nil {
			t.Fatal("decision descriptor missing", w.Code)
		}
		if accepted.ID != r.ID || accepted.Label != r.Label || accepted.Decision != "approved" || accepted.Created != r.Created || accepted.Expires != r.Expires || accepted.Expires-accepted.Created != int64(pairingLifetime) || accepted.Created <= previous {
			t.Fatal("decision did not preserve exact request context")
		}
		previous = accepted.Created
	}
}

func TestSensitiveAPIResponsePolicies(t *testing.T) {
	s := fixture(t)
	owner := trusted(t, s)
	h := handler(s, "", map[string]bool{"fixture.test": true}, map[string]bool{"https://fixture.test": true})
	policy := func(w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("API response lost private policies")
		}
	}
	private := fixtureRequest(h, "GET", "/private/data", owner, nil)
	if private.Code != 200 {
		t.Fatal(private.Code)
	}
	policy(private)
	w := fixtureRequest(h, "POST", "/pair", "", map[string]string{})
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	policy(w)
	var r request
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.decide(r.ID, r.Code, "approved", owner, false); err != nil {
		t.Fatal(err)
	}
	w = fixtureRequest(h, "POST", "/pair/redeem", "", map[string]string{"ID": r.ID, "Secret": r.Secret})
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	policy(w)
}

func TestProofWriteFailureIsRetryable(t *testing.T) {
	s := fixture(t)
	owner := trusted(t, s)
	h := handler(s, "", map[string]bool{"fixture.test": true}, map[string]bool{"https://fixture.test": true})
	if _, err := s.db.Exec("ALTER TABLE device_proofs RENAME TO held_proofs"); err != nil {
		t.Fatal(err)
	}
	w := fixtureRequest(h, "POST", "/private/device-proof", owner, map[string]string{"phase": "connected"})
	if w.Code != 503 || w.Header().Get("Retry-After") != "2" {
		t.Fatal("proof-only store failure lost retry hint", w.Code)
	}
	if _, err := s.db.Exec("ALTER TABLE held_proofs RENAME TO device_proofs"); err != nil {
		t.Fatal(err)
	}
	if w := fixtureRequest(h, "POST", "/private/device-proof", owner, map[string]string{"phase": "connected"}); w.Code != 204 {
		t.Fatal("proof failure destroyed session", w.Code)
	}
}
