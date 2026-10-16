package localcontrol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"haeryong.local/haeryong/internal/application/service"
)

func TestReplayAndIdentityMismatchRejected(t *testing.T) {
	p := service.Profile{LibraryID: "library", ProfileID: "profile", ControlKey: strings.Repeat("ab", 32)}
	id := service.Identity{LibraryID: p.LibraryID, ProfileID: p.ProfileID, InstanceID: "instance", API: service.APIVersion, ControlReady: true, Storage: service.StorageState{State: "pending_task_002_02"}}
	var captured []byte
	var capturedProof string
	mode := "live"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actual := id
		if mode == "wrong_library" {
			actual.LibraryID = "other"
		}
		if mode == "wrong_profile" {
			actual.ProfileID = "other"
		}
		if mode == "wrong_api" {
			actual.API = "unsupported"
		}
		if mode == "wrong_instance" {
			actual.InstanceID = "old"
		}
		body, _ := json.Marshal(actual)
		proof := signature(p.ControlKey, "response", r.Header.Get("X-Haeryong-Nonce"), body)
		if mode == "replay" {
			body = captured
			proof = capturedProof
		} else {
			captured = body
			capturedProof = proof
		}
		w.Header().Set("X-Haeryong-Proof", proof)
		w.Write(body)
	}))
	defer server.Close()
	id.Endpoint = server.URL
	if _, err := Verify(context.Background(), p, id); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{"replay", "wrong_library", "wrong_profile", "wrong_api", "wrong_instance"} {
		mode = candidate
		if _, err := Verify(context.Background(), p, id); err == nil {
			t.Fatalf("accepted %s response", mode)
		}
	}
}

func TestEndpointValidationDoesNotFollowRedirectOrProxy(t *testing.T) {
	for _, endpoint := range []string{"https://127.0.0.1:9270", "http://localhost:9270", "http://127.0.0.1:0", "http://127.0.0.1:9270/", "http://user@127.0.0.1:9270", "http://127.0.0.1:9270?x=y", "http://127.0.0.1:9270#fragment"} {
		if _, err := endpointURL(endpoint); err == nil {
			t.Errorf("accepted invalid endpoint %s", endpoint)
		}
	}
	p := service.Profile{LibraryID: "library", ProfileID: "profile", ControlKey: strings.Repeat("ab", 32)}
	targetHit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetHit = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	t.Setenv("HTTP_PROXY", target.URL)
	id := service.Identity{LibraryID: p.LibraryID, ProfileID: p.ProfileID, InstanceID: "instance", API: service.APIVersion, Endpoint: redirect.URL, ControlReady: true}
	if _, err := Verify(context.Background(), p, id); err == nil {
		t.Fatal("redirect accepted")
	}
	if targetHit {
		t.Fatal("credential proof forwarded to proxy or redirect target")
	}
}

func TestLocalEndpointPortBoundaries(t *testing.T) {
	for _, test := range []struct {
		endpoint string
		valid    bool
	}{
		{"http://127.0.0.1:1", false}, {"http://127.0.0.1:1023", false},
		{"http://127.0.0.1:1024", true}, {"http://127.0.0.1:9000", true},
		{"http://127.0.0.1:9999", true}, {"http://127.0.0.1:65535", true},
		{"http://127.0.0.1:65536", false},
	} {
		if _, err := endpointURL(test.endpoint); (err == nil) != test.valid {
			t.Errorf("endpoint %s valid=%v error=%v", test.endpoint, test.valid, err)
		}
	}
}
