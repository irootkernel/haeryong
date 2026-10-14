package authingress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrivateGuardsAndCurrentRevocation(t *testing.T) {
	s := fixture(t)
	owner := trusted(t, s)
	device := trusted(t, s)
	h := handler(s, random(), map[string]bool{"127.0.0.1:9270": true}, map[string]bool{"http://127.0.0.1:9270": true})
	do := func(path, token, host, origin string) int {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9270"+path, nil)
		r.Host = host
		r.Header.Set("Origin", origin)
		r.Header.Set("Authorization", "Bearer "+token)
		ctx, cancel := context.WithTimeout(r.Context(), 150*time.Millisecond)
		defer cancel()
		r = r.WithContext(ctx)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for _, path := range []string{"/session", "/private/data", "/private/assets", "/private/events", "/private/download"} {
		if code := do(path, device, "127.0.0.1:9270", "http://127.0.0.1:9270"); code != 200 {
			t.Fatal(path, code)
		}
		if code := do(path, "", "127.0.0.1:9270", ""); code != 401 {
			t.Fatal("absent origin granted access", path, code)
		}
		if code := do(path, device, "127.0.0.1:9270", "null"); code != 403 {
			t.Fatal("opaque origin", path, code)
		}
		if code := do(path, device, "other.test", ""); code != 403 {
			t.Fatal("unexpected host", path, code)
		}
	}
	if err := s.revoke(owner, deviceID(t, s, device)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/private/data", "/private/events", "/private/assets", "/private/download"} {
		if code := do(path, device, "127.0.0.1:9270", ""); code != 401 {
			t.Fatal("revoked", path, code)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9270/pair", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("anonymous pairing without expected Origin", w.Code)
	}
}

func TestBootstrapCredentialAndBrowserOrigin(t *testing.T) {
	for _, c := range []struct {
		name, configured, supplied, origin string
		want                               int
	}{
		{"missing", "protected-secret", "", "", 403},
		{"wrong", "protected-secret", "wrong", "", 403},
		{"unconfigured", "", "", "", 403},
		{"browser cannot use bootstrap", "protected-secret", "protected-secret", "http://127.0.0.1:9270", 403},
		{"native credential", "protected-secret", "protected-secret", "", 200},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := fixture(t)
			pending, err := s.create()
			if err != nil {
				t.Fatal(err)
			}
			h := handler(s, c.configured, map[string]bool{"127.0.0.1:9270": true}, map[string]bool{"http://127.0.0.1:9270": true})
			body, err := json.Marshal(map[string]string{"ID": pending.ID, "Code": pending.Code, "Decision": "approved"})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9270/pair/decision", strings.NewReader(string(body)))
			r.Header.Set("Authorization", "Bearer "+c.supplied)
			r.Header.Set("Origin", c.origin)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != c.want {
				t.Fatal(w.Code, c.want)
			}
		})
	}
}

func TestPairCreationLimitsIgnoreForwardedIdentity(t *testing.T) {
	s := fixture(t)
	h := handler(s, random(), map[string]bool{"127.0.0.1:9270": true}, map[string]bool{"http://127.0.0.1:9270": true})
	create := func(remote, forwarded string) int {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9270/pair", strings.NewReader("{}"))
		r.RemoteAddr = remote
		r.Header.Set("Origin", "http://127.0.0.1:9270")
		r.Header.Set("X-Forwarded-For", forwarded)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for _, forwarded := range []string{"203.0.113.1", "203.0.113.2"} {
		if status := create("127.0.0.1:10000", forwarded); status != 201 {
			t.Fatal(status)
		}
	}
	if status := create("127.0.0.1:10001", "203.0.113.3"); status != 429 {
		t.Fatal("forwarded identity or source port bypassed limit", status)
	}
	if status := create("127.0.0.2:10001", "203.0.113.3"); status != 201 {
		t.Fatal("independent socket peer rejected", status)
	}
	now := s.now()
	s.now = func() time.Time { return now.Add(time.Minute) }
	if status := create("127.0.0.1:10002", "203.0.113.4"); status != 201 {
		t.Fatal("expired creation window retained", status)
	}
}
