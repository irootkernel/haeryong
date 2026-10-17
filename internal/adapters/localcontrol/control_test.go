package localcontrol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/service"
)

var _ = It("TestReplayAndIdentityMismatchRejected", Label("integration"), func() {

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
	{
		_, err := Verify(context.Background(), p, id)
		Expect(err != nil).To(BeFalse(), func() string { return fmt.Sprint(err) })
	}

	for _, candidate := range []string{"replay", "wrong_library", "wrong_profile", "wrong_api", "wrong_instance"} {
		mode = candidate
		{
			_, err := Verify(context.Background(), p, id)
			Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprintf("accepted %s response", mode) })
		}

	}
})
var _ = It("TestEndpointValidationDoesNotFollowRedirectOrProxy", Label("integration"), func() {
	t := GinkgoT()

	for _, endpoint := range []string{"https://127.0.0.1:9270", "http://localhost:9270", "http://127.0.0.1:0", "http://127.0.0.1:9270/", "http://user@127.0.0.1:9270", "http://127.0.0.1:9270?x=y", "http://127.0.0.1:9270#fragment"} {
		{
			_, err := endpointURL(endpoint)
			Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprintf("accepted invalid endpoint %s", endpoint) })
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
	{
		_, err := Verify(context.Background(), p, id)
		Expect(err == nil).To(BeFalse(), func() string { return fmt.Sprint("redirect accepted") })
	}
	Expect(targetHit).To(BeFalse(), func() string { return fmt.Sprint("credential proof forwarded to proxy or redirect target") })

})
var _ = It("TestLocalEndpointPortBoundaries", Label("integration"), func() {

	for _, test := range []struct {
		endpoint string
		valid    bool
	}{
		{"http://127.0.0.1:1", false}, {"http://127.0.0.1:1023", false},
		{"http://127.0.0.1:1024", true}, {"http://127.0.0.1:9000", true},
		{"http://127.0.0.1:9999", true}, {"http://127.0.0.1:65535", true},
		{"http://127.0.0.1:65536", false},
	} {
		{
			_, err := endpointURL(test.endpoint)
			Expect((err == nil) != test.valid).To(BeFalse(), func() string { return fmt.Sprintf("endpoint %s valid=%v error=%v", test.endpoint, test.valid, err) })
		}

	}
})
