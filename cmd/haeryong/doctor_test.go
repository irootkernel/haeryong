package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/adapters/client"
	appauth "haeryong.local/haeryong/internal/application/auth"
	config "haeryong.local/haeryong/internal/application/configuration"
	app "haeryong.local/haeryong/internal/application/storage"
	sec "haeryong.local/haeryong/internal/security/session"
)

var _ = It("TestActualCLIPrivateIngressDoctorUsesCapturedOriginAndHonestSnapshot", Label("integration"), func() {
	t := GinkgoT()
	var trapRequests atomic.Int64
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { trapRequests.Add(1); w.WriteHeader(500) }))
	defer trap.Close()
	value, e := config.Decode(bytes.NewReader(app.DefaultConfiguration()), "json")
	Expect(e).NotTo(HaveOccurred())
	network := value["network"].(map[string]any)
	network["remote_enabled"] = true
	network["external_base_url"] = "https://unprobed.invalid:9271"
	network["allowed_origins"] = []any{"http://127.0.0.1:9270", "https://unprobed.invalid:9271"}
	network["renderer_mappings"] = []any{map[string]any{"version_id": "00000000-0000-0000-0000-000000000001", "origin": "https://unprobed.invalid:9291", "upstream": trap.URL, "reassignable": false}}
	Expect(config.Validate(value)).To(Succeed())
	token, e := sec.Secret()
	Expect(e).NotTo(HaveOccurred())
	var revision atomic.Int64
	revision.Store(7)
	var mu sync.Mutex
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/capabilities":
			w.Write([]byte(`{"data":{"library_id":"doctor-library","runtime_generation":"fixture"}}`))
		case "/api/v1/session":
			w.Write([]byte(`{"data":{"library_id":"doctor-library","principal_id":"owner","scope":{"kind":"owner"}}}`))
		case "/api/v1/status":
			w.Write([]byte(`{"data":{"config_revision":7,"listener":{"endpoint":"http://127.0.0.1:9490","ephemeral_override":true},"big_counter":9223372036854775807,"source":{"available":false,"reason":"not_observed"}}}`))
		case "/api/v1/configuration":
			json.NewEncoder(w).Encode(map[string]any{"data": config.View{ConfigRevision: revision.Load(), Value: config.Canonical(value)}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	root := t.TempDir()
	credential := filepath.Join(root, "owner.json")
	Expect(client.SaveCredential(credential, client.Credential{Endpoint: server.URL, Profile: "fixture", LibraryID: "doctor-library", Token: token})).To(Succeed())
	flags := []string{"--profile", "fixture", "--client-dir", root, "--credential", credential, "--json"}
	out := cli(t, 0, append([]string{"doctor"}, flags...)...)
	var result struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	Expect(json.Unmarshal(out, &result)).To(Succeed())
	Expect(string(result.Data["big_counter"])).To(Equal("9223372036854775807"))
	var checks doctorEndpoints
	Expect(json.Unmarshal(result.Data["endpoint_checks"], &checks)).To(Succeed())
	Expect(checks.ConnectedEndpoint).To(Equal(server.URL))
	Expect(checks.RuntimeLocalEndpoint).To(Equal("http://127.0.0.1:9490"))
	Expect(checks.ConfiguredLocalEndpoint).To(Equal("http://127.0.0.1:9270"))
	Expect(checks.EphemeralOverride).To(BeTrue())
	Expect(checks.RendererMappings).To(HaveLen(1))
	Expect(checks.RendererMappings[0].Supported).To(BeTrue())
	Expect(checks.Reachability).To(Equal(map[string]string{"connected_endpoint": "verified", "other_endpoints": "not_tested"}))
	Expect(trapRequests.Load()).To(BeZero())
	mu.Lock()
	observed := append([]string(nil), paths...)
	mu.Unlock()
	Expect(observed).To(Equal([]string{"GET /api/v1/capabilities", "GET /api/v1/session", "GET /api/v1/status", "GET /api/v1/configuration"}))
	cli(t, 3, append([]string{"doctor", "--endpoint", trap.URL}, flags...)...)
	Expect(trapRequests.Load()).To(BeZero())

	revision.Store(8)
	out = cli(t, 4, append([]string{"doctor"}, flags...)...)
	var failure struct {
		Data  any `json:"data"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	Expect(json.Unmarshal(out, &failure)).To(Succeed())
	Expect(failure.Data).To(BeNil())
	Expect(failure.Error.Message).To(ContainSubstring("retry doctor"))
	Expect(trapRequests.Load()).To(BeZero())

	// Status keeps its previous surface even when a doctor snapshot would retry.
	out = cli(t, 0, append([]string{"status"}, flags...)...)
	result.Data = nil
	Expect(json.Unmarshal(out, &result)).To(Succeed())
	Expect(result.Data).NotTo(HaveKey("endpoint_checks"))
})

var _ = It("TestProductHandlerHostSeparationExplicit443AndLocalDoctorGuidance", Label("unit"), func() {
	t := GinkgoT()
	base, err := config.Decode(bytes.NewReader(app.DefaultConfiguration()), "json")
	Expect(err).NotTo(HaveOccurred())

	// Explicit external HTTPS 443 on 127.0.0.1 and IP literal succeed when local listener is 127.0.0.1:9270.
	for _, validExternal := range []struct {
		ext, mapOrigin, mapUpstream string
	}{
		{"https://127.0.0.1:443", "https://127.0.0.1:9291", "http://127.0.0.1:9292"},
		{"https://100.64.0.1:443", "https://100.64.0.1:9291", "http://127.0.0.1:9291"},
		{"https://Node.Tailnet.ts.net:443", "https://Node.Tailnet.ts.net:09291", "http://127.0.0.1:9291"},
	} {
		candidate, err := config.Decode(bytes.NewReader(config.Canonical(base)), "json")
		Expect(err).NotTo(HaveOccurred())
		netCfg := candidate["network"].(map[string]any)
		netCfg["remote_enabled"] = true
		netCfg["external_base_url"] = validExternal.ext
		netCfg["allowed_origins"] = []any{"http://127.0.0.1:9270", validExternal.ext}
		netCfg["renderer_mappings"] = []any{
			map[string]any{"version_id": "00000000-0000-0000-0000-000000000001", "origin": validExternal.mapOrigin, "upstream": validExternal.mapUpstream, "reassignable": false},
		}
		_, err = productHandler(appauth.New(nil), "http://127.0.0.1:9270", config.Canonical(candidate))
		Expect(err).NotTo(HaveOccurred(), validExternal.ext)
	}

	// Collisions between local listener and external base URL or renderer mapping origin/upstream fail construction.
	for _, collide := range []func(map[string]any){
		func(netCfg map[string]any) {
			netCfg["remote_enabled"] = true
			netCfg["external_base_url"] = "https://127.0.0.1:09270"
			netCfg["allowed_origins"] = []any{"http://127.0.0.1:9270", "https://127.0.0.1:09270"}
		},
		func(netCfg map[string]any) {
			netCfg["remote_enabled"] = true
			netCfg["external_base_url"] = "https://node.tailnet.ts.net:443"
			netCfg["allowed_origins"] = []any{"http://127.0.0.1:9270", "https://node.tailnet.ts.net:443"}
			netCfg["renderer_mappings"] = []any{
				map[string]any{"version_id": "00000000-0000-0000-0000-000000000001", "origin": "https://NODE.tailnet.ts.net:00443", "upstream": "http://127.0.0.1:9291", "reassignable": false},
			}
		},
		func(netCfg map[string]any) {
			netCfg["renderer_mappings"] = []any{
				map[string]any{"version_id": "00000000-0000-0000-0000-000000000001", "origin": "https://node.tailnet.ts.net:9291", "upstream": "http://127.0.0.1:9270", "reassignable": false},
			}
		},
	} {
		candidate, err := config.Decode(bytes.NewReader(config.Canonical(base)), "json")
		Expect(err).NotTo(HaveOccurred())
		collide(candidate["network"].(map[string]any))
		_, err = productHandler(appauth.New(nil), "http://127.0.0.1:9270", config.Canonical(candidate))
		Expect(err).To(HaveOccurred())
	}

	// Local no-credential doctor failure returns exit code 3; endpointChecks includes local recovery instruction.
	root := t.TempDir()
	out := cli(t, 3, "doctor", "--profile", "missing-cred", "--client-dir", root, "--json")
	Expect(string(out)).To(ContainSubstring("credential.json"))
	checks, err := endpointChecks(
		"http://127.0.0.1:9490",
		map[string]json.RawMessage{
			"config_revision": json.RawMessage(`1`),
			"listener":        json.RawMessage(`{"endpoint":"http://127.0.0.1:9490","ephemeral_override":true}`),
		},
		config.View{ConfigRevision: 1, Value: config.Canonical(base)},
	)
	Expect(err).NotTo(HaveOccurred())
	Expect(checks.LocalRecovery["instruction"]).To(ContainSubstring("haeryong daemon run"))
})
