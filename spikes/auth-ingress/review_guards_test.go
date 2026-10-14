package authingress

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPExactComparisonSecretAndPollThrottle(t *testing.T) {
	s := fixture(t)
	owner := trusted(t, s)
	a, err := s.createDevice("Same label")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.createDevice("Same label")
	if err != nil {
		t.Fatal(err)
	}
	h := handler(s, random(), map[string]bool{"fixture.test": true}, map[string]bool{"https://fixture.test": true})
	post := func(path string, value any) *httptest.ResponseRecorder {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "https://fixture.test"+path, strings.NewReader(string(body)))
		r.Header.Set("Origin", "https://fixture.test")
		r.Header.Set("Authorization", "Bearer "+owner)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, wrong := range []string{b.Code, a.Label} {
		if w := post("/pair/decision", map[string]string{"ID": a.ID, "Code": wrong, "Decision": "approved"}); w.Code != 403 {
			t.Fatal("wrong comparison accepted", w.Code)
		}
	}
	if w := post("/pair/poll", map[string]string{"ID": a.ID, "Secret": a.Secret}); w.Code != 200 || !strings.Contains(w.Body.String(), "pending") {
		t.Fatal("denial changed pending", w.Code)
	}
	if w := post("/pair/poll", map[string]string{"ID": a.ID, "Secret": a.Secret}); w.Code != 429 || w.Header().Get("Retry-After") != "2" {
		t.Fatal("missing real HTTP poll throttle", w.Code, w.Header())
	}
	if w := post("/pair/decision", map[string]string{"ID": a.ID, "Code": a.Code, "Decision": "approved"}); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := post("/pair/redeem", map[string]string{"ID": a.ID, "Secret": b.Secret}); w.Code != 403 {
		t.Fatal("wrong secret accepted", w.Code)
	}
	if w := post("/pair/redeem", map[string]string{"ID": a.ID, "Secret": a.Secret}); w.Code != 200 {
		t.Fatal("wrong secret consumed redemption", w.Code)
	}
	if w := post("/pair/redeem", map[string]string{"ID": a.ID, "Secret": a.Secret}); w.Code != 403 {
		t.Fatal("second token minted", w.Code)
	}
}

func TestPairMetadataAndFailedAttemptBudget(t *testing.T) {
	s := fixture(t)
	h := handler(s, random(), map[string]bool{"fixture.test": true}, map[string]bool{"https://fixture.test": true})
	post := func(body, peer, forwarded string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://fixture.test/pair/poll", strings.NewReader(body))
		r.RemoteAddr = peer
		r.Header.Set("X-Forwarded-For", forwarded)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := post(`{"Secret":"`+strings.Repeat("x", 16384)+`"}`, "127.0.0.2:1", ""); w.Code != 400 {
		t.Fatal("oversized pairing metadata accepted", w.Code)
	}
	for i := 0; i < 8; i++ {
		if w := post(`{}`, "127.0.0.1:1", random()); w.Code != 403 {
			t.Fatal("unexpected failure budget", i, w.Code)
		}
	}
	if w := post(`{}`, "127.0.0.1:2", "203.0.113.1"); w.Code != 429 || w.Header().Get("Retry-After") != "60" {
		t.Fatal("forwarding/source port bypassed failure budget", w.Code)
	}
	if w := post(`{}`, "127.0.0.3:1", ""); w.Code != 403 {
		t.Fatal("independent peer blocked", w.Code)
	}
	now := s.now()
	s.now = func() time.Time { return now.Add(time.Minute) }
	if w := post(`{}`, "127.0.0.1:3", ""); w.Code != 403 {
		t.Fatal("failure window never reset", w.Code)
	}
}

func TestConnectPoliciesAndOpaqueServedChild(t *testing.T) {
	s := fixture(t)
	script := []byte("console.log('fixture')")
	page := append(append([]byte("<script>"), script...), []byte("</script>")...)
	h := browserHandler(s, random(), map[string]bool{"fixture.test": true}, map[string]bool{"https://fixture.test": true}, page)
	digest := sha256.Sum256(script)
	source := "'sha256-" + base64.StdEncoding.EncodeToString(digest[:]) + "'"
	for _, path := range []string{"/", "/connect", "/reader"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://fixture.test"+path, nil))
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("missing trusted shell policies", path, w.Header())
		}
		csp := w.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, source) || !strings.Contains(csp, "frame-ancestors 'none'") || strings.Contains(csp, "unsafe-inline") {
			t.Fatal("trusted script policy lost exact hash", csp)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://fixture.test/probe-child", nil))
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox allow-scripts;") {
		t.Fatal("served child can become trusted top-level content", w.Code, w.Header())
	}
}

func TestDeviceInventoryMarksExpiryWithServerClock(t *testing.T) {
	s := fixture(t)
	owner := trusted(t, s)
	r, err := s.createDevice("expiring")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.decide(r.ID, r.Code, "approved", owner, false); err != nil {
		t.Fatal(err)
	}
	token, err := s.redeem(r.ID, r.Secret, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	id := deviceID(t, s, token)
	now := s.now()
	s.now = func() time.Time { return now.Add(time.Second) }
	list, err := s.devices(owner)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range list {
		if d.ID == id {
			found = true
			if !d.Expired || d.Revoked {
				t.Fatal("expired device still shown active", d)
			}
		}
	}
	if !found {
		t.Fatal("expired device disappeared before owner revocation")
	}
	if _, err := s.session(token); err == nil {
		t.Fatal("expired device still authorized")
	}
	if err := s.revoke(owner, id); err != nil {
		t.Fatal("owner cannot revoke obsolete record", err)
	}
}
