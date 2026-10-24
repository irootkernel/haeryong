package main

import (
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

var _ = It("Revisit CLI retains the original automatic key and condition after an uncertain server outcome", Label("integration"), func() {
	const document = "11111111-1111-4111-8111-111111111111"
	var mu sync.Mutex
	var key string
	writes, lookups := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		data := func(v any) { json.NewEncoder(w).Encode(map[string]any{"data": v}) }
		switch {
		case r.URL.Path == "/api/v1/session":
			data(domain.Session{LibraryID: "revisit-library", PrincipalID: "owner", Scope: domain.CredentialScope{Kind: "owner"}})
		case r.URL.Path == "/api/v1/capabilities":
			data(map[string]any{"library_id": "revisit-library", "runtime_generation": "generation", "features": map[string]bool{"revisit": true}})
		case r.Method == "PUT" && r.URL.Path == "/api/v1/documents/"+document+"/revisit":
			writes++
			key = r.Header.Get("Idempotency-Key")
			Expect(r.Header.Get("If-Match")).To(Equal(`"original"`))
			w.WriteHeader(503)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "SERVICE_RECOVERING"}})
		case strings.HasPrefix(r.URL.Path, "/api/v1/operations/"):
			lookups++
			Expect(strings.TrimPrefix(r.URL.Path, "/api/v1/operations/")).To(Equal(key))
			data(map[string]any{"operation_id": key, "state": "completed", "original_status": 200, "result_metadata": map[string]any{"document_id": document, "exists": true, "value": map[string]bool{"flag": true}, "revision": 1, "etag": `"saved"`, "page_position_url": nil}})
		default:
			w.WriteHeader(404)
		}
	}))
	DeferCleanup(server.Close)
	token, err := sec.Secret()
	Expect(err).NotTo(HaveOccurred())
	root, flags := callerProfile(GinkgoT(), service.Selection{Profile: "revisit-recovery"}, domain.Session{LibraryID: "revisit-library"}, token, server.URL)
	args := append([]string{"revisit", "set", "--document", document, "--flag", "true", "--if-match", `"original"`}, flags...)
	first := failedOperation(GinkgoT(), cli(GinkgoT(), 5, args...))
	Expect(pendingOperations(GinkgoT(), root)).To(Equal([]string{first}))
	journal := readCallerJournal(GinkgoT(), root, first)
	Expect(string(journal.FinalBody)).To(Equal(`{"flag":true}`))
	Expect(journal.Preconditions).To(Equal(map[string]string{"If-Match": `"original"`}))
	second := failedOperation(GinkgoT(), cli(GinkgoT(), 0, args...))
	Expect(second).To(Equal(first))
	mu.Lock()
	Expect(writes).To(Equal(1))
	Expect(lookups).To(Equal(1))
	mu.Unlock()
	Expect(pendingOperations(GinkgoT(), root)).To(BeEmpty())
})
