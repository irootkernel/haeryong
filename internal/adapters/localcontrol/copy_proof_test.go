package localcontrol

import (
	"bytes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/service"
	"net/http/httptest"
	"strings"
)

var _ = It("Copies local proof binds exact body bearer operation profile and instance and refuses unauthenticated peer input", Label("integration"), func() {
	p := service.Profile{ProfileID: "profile", LibraryID: "library", ControlKey: strings.Repeat("ab", 32)}
	id := service.Identity{InstanceID: "instance"}
	nonce := strings.Repeat("01", 32)
	body := []byte(`{"action":"backup.create"}`)
	original := copyRequestProof(p, id, nonce, "operation", "bearer", body)
	Expect(copyRequestProof(p, id, nonce, "other", "bearer", body)).NotTo(Equal(original))
	Expect(copyRequestProof(p, id, nonce, "operation", "other", body)).NotTo(Equal(original))
	Expect(copyRequestProof(p, id, nonce, "operation", "bearer", append(body, ' '))).NotTo(Equal(original))
	wrong := id
	wrong.InstanceID = "old"
	Expect(copyRequestProof(p, wrong, nonce, "operation", "bearer", body)).NotTo(Equal(original))
	wrongProfile := p
	wrongProfile.ProfileID = "other"
	Expect(copyRequestProof(wrongProfile, id, nonce, "operation", "bearer", body)).NotTo(Equal(original))
	h := &HTTP{}
	for _, mode := range []string{"proof", "peer", "forwarded", "duplicate", "unknown", "runtime"} {
		raw := body
		if mode == "duplicate" {
			raw = []byte(`{"action":"backup.create","action":"backup.create"}`)
		}
		if mode == "unknown" {
			raw = []byte(`{"action":"backup.create","input":"/etc/passwd"}`)
		}
		req := httptest.NewRequest("POST", copiesPath, bytes.NewReader(raw))
		req.RemoteAddr = "127.0.0.1:4000"
		req.Header.Set("X-Haeryong-Nonce", nonce)
		req.Header.Set("Idempotency-Key", "operation")
		req.Header.Set("Authorization", "Bearer bearer")
		req.Header.Set("X-Haeryong-Proof", copyRequestProof(p, id, nonce, "operation", "bearer", raw))
		if mode == "proof" {
			req.Header.Set("X-Haeryong-Proof", "bad")
		}
		if mode == "peer" {
			req.RemoteAddr = "192.0.2.1:1"
		}
		if mode == "forwarded" {
			req.Header.Set("Forwarded", "for=127.0.0.1")
		}
		response := httptest.NewRecorder()
		h.copyRequest(response, req, p, id)
		Expect(response.Code).To(BeNumerically(">=", 400), mode)
	}
})
