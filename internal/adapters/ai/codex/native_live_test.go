//go:build haeryong_provider_live

package codex

import (
	"context"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var _ = It("C03ROUTE inspects only credential-free route predicates without inference", Label("integration"), func() {
	evidence := os.Getenv("HAERYONG_PROVIDER_LIVE_EVIDENCE")
	Expect(filepath.IsAbs(evidence)).To(BeTrue())
	d := New(nil, nil, evidence)
	d.Binary = os.Getenv("HAERYONG_PROVIDER_LIVE_BINARY")
	binary, _, e := d.binary(context.Background())
	Expect(e).NotTo(HaveOccurred())
	t, e := start(context.Background(), binary)
	Expect(e).NotTo(HaveOccurred())
	defer t.close()
	var response struct {
		Config struct {
			Provider  *string `json:"model_provider"`
			Providers map[string]struct {
				BaseURL *string `json:"base_url"`
				EnvKey  *string `json:"env_key"`
				Auth    *bool   `json:"requires_openai_auth"`
			} `json:"model_providers"`
			ChatGPTBaseURL *string `json:"chatgpt_base_url"`
			OpenAIBaseURL  *string `json:"openai_base_url"`
		} `json:"config"`
	}
	Expect(t.call(context.Background(), "config/read", map[string]bool{"includeLayers": false}, &response)).To(Succeed())
	native := response.Config
	route, exists := native.Providers["openai"]
	defaultChatGPT := false
	if native.ChatGPTBaseURL != nil {
		u, e := url.Parse(*native.ChatGPTBaseURL)
		defaultChatGPT = e == nil && u.Scheme == "https" && u.Host == "chatgpt.com" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && strings.TrimSuffix(u.Path, "/") == "/backend-api"
	}
	facts := map[string]any{"provider_unspecified": native.Provider == nil, "provider_is_openai": native.Provider != nil && *native.Provider == "openai", "openai_override_present": exists, "model_openai_base_url_set": route.BaseURL != nil, "model_openai_base_url_standard": route.BaseURL != nil && (*route.BaseURL == "https://api.openai.com/v1" || *route.BaseURL == "https://chatgpt.com/backend-api/codex"), "openai_env_key_set": route.EnvKey != nil, "openai_requires_auth": route.Auth, "chatgpt_base_url_standard": defaultChatGPT, "root_openai_base_url_set": native.OpenAIBaseURL != nil, "root_openai_base_url_standard": native.OpenAIBaseURL != nil && *native.OpenAIBaseURL == "https://api.openai.com/v1", "chatgpt_base_url_set": native.ChatGPTBaseURL != nil}
	raw, e := json.MarshalIndent(facts, "", "  ")
	Expect(e).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(evidence, "route-predicates.json"), append(raw, '\n'), 0600)).To(Succeed())
})
