package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
)

const c2ReviewVersion = "11111111-1111-4111-8111-111111111111"

// This isolated loopback peer controls wire outcomes only. The production CLI,
// transport, journal, pending index and output acknowledgement run unchanged.
type c2CommandPeer struct {
	mu       sync.Mutex
	mode     string
	status   int
	code     string
	requests []c2CommandRequest
	lookups  []string
	effects  map[string]bool
}
type c2CommandRequest struct{ key, method, path, body, match, none string }

func newC2CommandPeer(mode string, status int, code string) *c2CommandPeer {
	return &c2CommandPeer{mode: mode, status: status, code: code, effects: map[string]bool{}}
}
func (p *c2CommandPeer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// A lost response uses a fresh connection, so net/http cannot retry a reused
	// idle socket before the command has a chance to recover its pending key.
	w.Header().Set("Connection", "close")
	w.Header().Set("Content-Type", "application/json")
	data := func(status int, value any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": value})
	}
	failure := func(status int, code string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code}})
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/v1/capabilities":
		data(200, map[string]any{"library_id": "c2-library", "unfiled_project_id": "c2-project", "runtime_generation": "c2-runtime", "features": map[string]bool{"review": true}})
	case r.Method == "GET" && r.URL.Path == "/api/v1/session":
		data(200, domain.Session{LibraryID: "c2-library", PrincipalID: "c2-owner", Scope: domain.CredentialScope{Kind: "owner"}})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/operations/"):
		key := strings.TrimPrefix(r.URL.Path, "/api/v1/operations/")
		p.lookups = append(p.lookups, key)
		if p.mode == "reject" || !p.hasRequest(key) {
			failure(404, "NOT_FOUND")
			return
		}
		if p.mode == "recorded" {
			data(200, map[string]any{"state": "failed", "original_status": 503, "result_metadata": map[string]any{"error": map[string]any{"code": "SERVICE_RECOVERING"}}})
			return
		}
		result := map[string]any{"operation_id": key, "version_id": c2ReviewVersion, "exists": true,
			"value":    map[string]any{"state": "reviewed", "reviewed_at": "2026-10-24T00:00:00.000Z"},
			"revision": 1, "etag": `"c2-review-1"`, "page_position_url": nil}
		status := 200
		for _, request := range p.requests {
			if request.key == key && request.method == "POST" {
				project := domain.QueryProject{Project: domain.Project{ID: "22222222-2222-4222-8222-222222222222", Name: "C2 project", Lifecycle: "active", Revision: 1, PermissionsRevision: 1}, UpdatedAt: "2026-10-24T00:00:00.000Z"}
				result = domain.ProjectView(project)
				result["etag"] = domain.ProjectETag(project)
				status = 201
			}
		}
		data(200, map[string]any{"state": "completed", "original_status": status, "result_metadata": result})
	case r.Method == "PUT" && r.URL.Path == "/api/v1/versions/"+c2ReviewVersion+"/review" || r.Method == "POST" && r.URL.Path == "/api/v1/projects":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			failure(400, "INVALID_REQUEST")
			return
		}
		key := r.Header.Get("Idempotency-Key")
		p.requests = append(p.requests, c2CommandRequest{key: key, method: r.Method, path: r.URL.Path, body: string(body), match: r.Header.Get("If-Match"), none: r.Header.Get("If-None-Match")})
		if p.mode == "reject" {
			failure(412, "PRECONDITION_FAILED")
			return
		}
		if p.mode == "recorded" {
			data(202, map[string]any{"operation_id": key, "state": "accepted", "operation_url": "/api/v1/operations/" + key})
			return
		}
		p.effects[key] = true
		switch p.mode {
		case "lost":
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close()
			}
		case "decode":
			_, _ = w.Write([]byte(`{"data":`))
		default:
			failure(p.status, p.code)
		}
	default:
		failure(404, "NOT_FOUND")
	}
}
func (p *c2CommandPeer) hasRequest(key string) bool {
	for _, request := range p.requests {
		if request.key == key {
			return true
		}
	}
	return false
}
func (p *c2CommandPeer) snapshot() ([]c2CommandRequest, []string, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]c2CommandRequest(nil), p.requests...), append([]string(nil), p.lookups...), len(p.effects)
}
func c2CommandCaller(t GinkgoTInterface, peer *c2CommandPeer) (string, []string) {
	GinkgoHelper()
	server := httptest.NewServer(peer)
	DeferCleanup(server.Close)
	token, err := sec.Secret()
	Expect(err).NotTo(HaveOccurred())
	return callerProfile(t, service.Selection{Profile: "c2-review"}, domain.Session{LibraryID: "c2-library"}, token, server.URL)
}
func c2ReviewArgs(flags []string) []string {
	return append([]string{"review", "set", "--version", c2ReviewVersion, "--action", "complete", "--if-match", `"original-validator"`}, flags...)
}

var _ = It("C2 Review CLI retains the original automatic request through uncertain outcomes", Label("integration"), func() {
	for _, row := range []struct {
		mode   string
		status int
		code   string
		exit   int
	}{
		{"answer", 409, "OPERATION_RECONCILIATION_REQUIRED", 6},
		{"answer", 409, "PROVIDER_OUTCOME_UNKNOWN", 6},
		{"answer", 401, "SESSION_EXPIRED", 3},
		{"answer", 403, "FORBIDDEN", 3},
		{"answer", 503, "SERVICE_RECOVERING", 5},
		{"lost", 0, "", 5},
		{"decode", 0, "", 5},
	} {
		By(row.mode + " " + row.code)
		peer := newC2CommandPeer(row.mode, row.status, row.code)
		root, flags := c2CommandCaller(GinkgoT(), peer)
		args := c2ReviewArgs(flags)
		out := cli(GinkgoT(), row.exit, args...)
		first := failedOperation(GinkgoT(), out)
		var emitted struct {
			Data struct {
				OriginalStatus *int `json:"original_status"`
			} `json:"data"`
		}
		Expect(json.Unmarshal(out, &emitted)).To(Succeed())
		Expect(emitted.Data.OriginalStatus).To(BeNil())
		Expect(pendingOperations(GinkgoT(), root)).To(Equal([]string{first}))
		journal := readCallerJournal(GinkgoT(), root, first)
		Expect(journal.Phase).To(Equal("dispatch"))
		Expect(journal.Response).To(BeNil(), "an uncertain wire answer is not a terminal receipt")
		Expect(journal.Preconditions).To(Equal(map[string]string{"If-Match": `"original-validator"`}))
		Expect(string(journal.FinalBody)).To(Equal(`{"action":"complete","state":"reviewed"}`))
		if row.code == "OPERATION_RECONCILIATION_REQUIRED" {
			changed := append([]string(nil), args...)
			for i, arg := range changed {
				if arg == "--if-match" {
					changed[i+1] = `"changed-validator"`
				}
			}
			Expect(failedOperation(GinkgoT(), cli(GinkgoT(), 4, changed...))).To(Equal(first))
			Expect(pendingOperations(GinkgoT(), root)).To(Equal([]string{first}), "a local intent conflict does not release the original operation")
			Expect(readCallerJournal(GinkgoT(), root, first)).To(Equal(journal))
		}

		second := failedOperation(GinkgoT(), cli(GinkgoT(), 0, args...))
		Expect(second).To(Equal(first))
		requests, lookups, effects := peer.snapshot()
		Expect(requests).To(HaveLen(1), "recovery reads the receipt without another PUT")
		Expect(requests[0]).To(Equal(c2CommandRequest{key: first, method: "PUT", path: "/api/v1/versions/" + c2ReviewVersion + "/review", body: string(journal.FinalBody), match: `"original-validator"`}))
		Expect(lookups).To(Equal([]string{first}))
		Expect(effects).To(Equal(1))
		recovered := readCallerJournal(GinkgoT(), root, first)
		Expect(recovered.Phase).To(Equal("accepted"))
		Expect(recovered.Response.Status).To(Equal(200))
		Expect(recovered.FinalBody).To(Equal(journal.FinalBody))
		Expect(recovered.Preconditions).To(Equal(journal.Preconditions))
		Expect(pendingOperations(GinkgoT(), root)).To(BeEmpty())
	}
})

type c2RejectedOutput struct{}

func (c2RejectedOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

var _ = It("C2 Review CLI releases ordinary 4xx only after emission", Label("integration"), func() {
	peer := newC2CommandPeer("reject", 412, "PRECONDITION_FAILED")
	root, flags := c2CommandCaller(GinkgoT(), peer)
	args := c2ReviewArgs(flags)
	Expect(run(context.Background(), args, c2RejectedOutput{}, io.Discard)).To(Equal(4))
	pending := pendingOperations(GinkgoT(), root)
	Expect(pending).To(HaveLen(1), "failed emission cannot acknowledge the rejection")
	first := failedOperation(GinkgoT(), cli(GinkgoT(), 4, args...))
	Expect(first).To(Equal(pending[0]))
	Expect(pendingOperations(GinkgoT(), root)).To(BeEmpty())
	second := failedOperation(GinkgoT(), cli(GinkgoT(), 4, args...))
	Expect(second).NotTo(Equal(first), "an emitted ordinary rejection releases the automatic key")
	Expect(pendingOperations(GinkgoT(), root)).To(BeEmpty())
	requests, lookups, effects := peer.snapshot()
	Expect(requests).To(HaveLen(3))
	Expect(requests[0].key).To(Equal(first))
	Expect(requests[1]).To(Equal(requests[0]))
	Expect(requests[2].key).To(Equal(second))
	Expect(lookups).To(Equal([]string{first}))
	Expect(effects).To(BeZero())
})

var _ = It("C2 Review CLI releases a recorded terminal 503 failure after emission", Label("integration"), func() {
	peer := newC2CommandPeer("recorded", 503, "SERVICE_RECOVERING")
	root, flags := c2CommandCaller(GinkgoT(), peer)
	args := c2ReviewArgs(flags)
	first := failedOperation(GinkgoT(), cli(GinkgoT(), 5, args...))
	Expect(pendingOperations(GinkgoT(), root)).To(BeEmpty())
	journal := readCallerJournal(GinkgoT(), root, first)
	Expect(journal.Phase).To(Equal("failed"))
	Expect(journal.Response.Status).To(Equal(503))
	Expect(string(journal.Response.Body)).To(ContainSubstring("SERVICE_RECOVERING"))
	second := failedOperation(GinkgoT(), cli(GinkgoT(), 5, args...))
	Expect(second).NotTo(Equal(first))
	Expect(pendingOperations(GinkgoT(), root)).To(BeEmpty())
	requests, _, effects := peer.snapshot()
	Expect(requests).To(HaveLen(2))
	Expect(effects).To(BeZero())
})

var _ = It("C2 shared mutation keeps non Review project registration recovery unchanged", Label("integration"), func() {
	peer := newC2CommandPeer("answer", 503, "SERVICE_RECOVERING")
	root, flags := c2CommandCaller(GinkgoT(), peer)
	args := append([]string{"projects", "register", "--name", "C2 project"}, flags...)
	cli(GinkgoT(), 5, args...)
	pending := pendingOperations(GinkgoT(), root)
	Expect(pending).To(HaveLen(1))
	journal := readCallerJournal(GinkgoT(), root, pending[0])
	Expect(journal.Phase).To(Equal("dispatch"))
	Expect(journal.Response).To(BeNil())
	Expect(journal.Preconditions).To(BeEmpty())
	recovered := failedOperation(GinkgoT(), cli(GinkgoT(), 0, args...))
	Expect(recovered).To(Equal(pending[0]))
	Expect(pendingOperations(GinkgoT(), root)).To(BeEmpty())
	requests, lookups, effects := peer.snapshot()
	Expect(requests).To(Equal([]c2CommandRequest{{key: recovered, method: "POST", path: "/api/v1/projects", body: `{"name":"C2 project"}`}}))
	Expect(lookups).To(Equal([]string{recovered}))
	Expect(effects).To(Equal(1))
})
