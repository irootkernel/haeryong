//go:build haeryong_provider_live

package codex

import (
	"context"
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/domain"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var _ = It("C03START retains actual owned initialization and cleanup facts without account or inference calls", Label("integration"), func() {
	evidence := os.Getenv("HAERYONG_PROVIDER_LIVE_EVIDENCE")
	binary := os.Getenv("HAERYONG_PROVIDER_LIVE_BINARY")
	Expect(filepath.IsAbs(evidence) && filepath.IsAbs(binary)).To(BeTrue())
	physical, err := filepath.EvalSymlinks(evidence)
	Expect(err).NotTo(HaveOccurred())
	Expect(physical).To(Equal(evidence))
	info, err := os.Stat(evidence)
	Expect(err).NotTo(HaveOccurred())
	Expect(info.Mode().Perm() & 0077).To(BeZero())
	cwd := filepath.Join(evidence, "cwd")
	Expect(os.Mkdir(cwd, 0700)).To(Succeed())
	previous, err := os.Getwd()
	Expect(err).NotTo(HaveOccurred())
	Expect(os.Chdir(cwd)).To(Succeed())
	defer os.Chdir(previous)
	d := New(nil, nil, evidence)
	a := domain.ProviderAttempt{ID: "aa3cff4d-5eb7-4210-a56a-e6570a5504a1"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t, startErr := startPrepared(ctx, binary, func(fact ownedNativeProcess) error {
		return d.retainPreparation(a, "preparation-process-", fact)
	})
	if startErr != nil {
		var failure *initializationFailure
		out := map[string]any{"initialized": false, "local_closed": !errors.Is(startErr, errCleanup), "inference_calls": 0}
		if errors.As(startErr, &failure) {
			out["native_process"], out["native_error_class"] = failure.owned, failure.errorClass
			out["native_exit_code"], out["cleanup_reason"] = failure.exitCode, failure.cleanupReason
		}
		Expect(d.retainPreparation(a, "preparation-startup-", out)).To(Succeed())
		Fail("Native initialization unavailable; inspect content-free private preparation facts")
	}
	cleanup := t.close()
	fact := map[string]any{"initialized": true, "native_process": t.owned, "local_closed": cleanup == nil, "cleanup_reason": t.cleanupReason, "inference_calls": 0, "native_error_class": t.setupErrors.Class()}
	if t.cmd.ProcessState != nil && t.cmd.ProcessState.Exited() {
		fact["native_exit_code"] = t.cmd.ProcessState.ExitCode()
	}
	Expect(d.retainPreparation(a, "preparation-startup-", fact)).To(Succeed())
	Expect(t.owned.Observed).To(BeTrue(), "Owned process inventory unavailable")
	Expect(cleanup == nil).To(BeTrue(), "Owned group cleanup unverified")
})

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
