package authingress

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDeviceManagementUsesNoncredentialIDs(t *testing.T) {
	s := fixture(t)
	owner := trusted(t, s)
	next := trusted(t, s)
	list, err := s.devices(owner)
	if err != nil || len(list) != 2 {
		t.Fatal("device inventory", err)
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{owner, next, hash(owner), hash(next)} {
		if strings.Contains(string(encoded), value) {
			t.Fatal("device inventory exposed a credential or verifier")
		}
	}
	if err := s.revoke(owner, deviceID(t, s, next)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.devices(next); err == nil {
		t.Fatal("revoked inventory access")
	}
	if _, err := s.devices(""); err == nil {
		t.Fatal("unauthenticated inventory")
	}
	if _, err := s.session(owner); err != nil {
		t.Fatal("revoking another device invalidated the owner", err)
	}
}

func TestActiveEventStreamTerminatesAfterRevocation(t *testing.T) {
	s := fixture(t)
	owner := trusted(t, s)
	next := trusted(t, s)
	id := deviceID(t, s, next)
	srv := httptest.NewServer(handler(s, random(), map[string]bool{"fixture.test": true}, map[string]bool{}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/private/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "fixture.test"
	r.Header.Set("Authorization", "Bearer "+next)
	response, err := srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("private stream did not open")
	}
	if err := s.revoke(owner, id); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal("revoked stream failed to close before deadline", err)
	}
	if strings.Contains(string(data), "event: retryable") || strings.Contains(string(data), "store_unavailable") {
		t.Fatal("revoked stream was misreported as a retryable store outage")
	}
	if ctx.Err() != nil {
		t.Fatal("stream closed only because client timeout expired")
	}
}

func TestActiveEventStreamReportsRetryableStoreFailure(t *testing.T) {
	s := fixture(t)
	owner := trusted(t, s)
	srv := httptest.NewServer(handler(s, "", map[string]bool{"fixture.test": true}, nil))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/private/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "fixture.test"
	r.Header.Set("Authorization", "Bearer "+owner)
	response, err := srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	if _, err := s.db.Exec("ALTER TABLE sessions RENAME TO held_sessions"); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil || !strings.Contains(string(data), "event: retryable") || !strings.Contains(string(data), `"reason":"store_unavailable"`) || !strings.Contains(string(data), `"retry_after":2`) {
		t.Fatal("stream outage lacks retryable terminal event", err)
	}
	if _, err := s.db.Exec("ALTER TABLE held_sessions RENAME TO sessions"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.session(owner); err != nil {
		t.Fatal("stream outage invalidated bearer", err)
	}
}
