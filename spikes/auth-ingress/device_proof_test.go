package authingress

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeviceProofUsesCurrentSessionAndStoresNoCredential(t *testing.T) {
	s := fixture(t)
	owner, next := trusted(t, s), trusted(t, s)
	h := handler(s, random(), map[string]bool{"fixture.test": true}, map[string]bool{"https://fixture.test": true})
	post := func(token, origin, body string) int {
		r := httptest.NewRequest(http.MethodPost, "https://fixture.test/private/device-proof", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Origin", origin)
		r.Header.Set("User-Agent", "Fixture browser")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	body := `{"phase":"connected","remembered":true,"private_read":true,"unpaired_denials":true,"device_id":"untrusted-id"}`
	if code := post("", "https://fixture.test", body); code != 401 {
		t.Fatal("unpaired proof accepted", code)
	}
	if code := post(next, "null", body); code != 403 {
		t.Fatal("opaque origin accepted", code)
	}
	if code := post(next, "https://fixture.test", body); code != 204 {
		t.Fatal("paired proof denied", code)
	}
	var id, payload string
	if err := s.db.QueryRow("SELECT device_id,payload FROM device_proofs").Scan(&id, &payload); err != nil {
		t.Fatal(err)
	}
	if id != deviceID(t, s, next) {
		t.Fatal("client selected observation owner")
	}
	for _, secret := range []string{owner, next, hash(owner), hash(next), "untrusted-id"} {
		if strings.Contains(payload, secret) {
			t.Fatal("observation retained a credential or untrusted device selector")
		}
	}
	var recorded struct {
		UserAgent string `json:"user_agent"`
	}
	if json.Unmarshal([]byte(payload), &recorded) != nil || recorded.UserAgent != "Fixture browser" {
		t.Fatal("browser metadata missing")
	}
	if code := post(next, "https://fixture.test", `{"phase":"invalid"}`); code != 400 {
		t.Fatal("unknown phase accepted", code)
	}
	if err := s.revoke(owner, id); err != nil {
		t.Fatal(err)
	}
	if code := post(next, "https://fixture.test", body); code != 401 {
		t.Fatal("revoked proof accepted", code)
	}
}
