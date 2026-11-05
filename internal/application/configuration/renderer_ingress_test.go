package configuration

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("TestPrivateIngressConfiguredSupportKeepsSameHostAndWireSeparation", Label("unit"), func() {
	mapping := RendererMappings(ingressValue())[0]
	Expect(PrivateRendererMappingReason("https://node.private:9271", mapping, "http://127.0.0.1:9270")).To(BeEmpty())
	Expect(PrivateRendererMappingReason("https://NODE.private:9271", mapping, "http://127.0.0.1:9270")).To(BeEmpty())
	for _, origin := range []string{"https://other.private:9291", "https://node.private:9271"} {
		other := mapping
		other.Origin = origin
		Expect(PrivateRendererMappingReason("https://node.private:9271", other, "http://127.0.0.1:9270")).To(Equal("renderer_remote_ingress"))
	}
	Expect(PrivateRendererMappingReason("https://node.private:9271", mapping, mapping.Upstream)).To(Equal("renderer_remote_ingress"))
	implicit := mapping
	implicit.Origin = "https://node.private:443"
	Expect(PrivateRendererMappingReason("https://node.private", implicit, "http://127.0.0.1:9270")).To(Equal("renderer_remote_ingress"))
	implicit.Origin = "https://node.private:00443"
	Expect(PrivateRendererMappingReason("https://node.private", implicit, "http://127.0.0.1:9270")).To(Equal("renderer_remote_ingress"))
	implicit.Origin = "https://node.private:444"
	Expect(PrivateRendererMappingReason("https://node.private", implicit, "http://127.0.0.1:9270")).To(BeEmpty())
	// A public HTTPS Host cannot also name that renderer's plain upstream.
	Expect(PrivateRendererMappingReason("https://127.0.0.1:9271", RendererMapping{Origin: "https://127.0.0.1:9291", Upstream: "http://127.0.0.1:9291"}, "http://127.0.0.1:9270")).To(Equal("renderer_remote_ingress"))
})

var _ = It("TestPrivateIngressRejectsMappingCollisionWithRuntimeOverride", Label("unit"), func() {
	value := ingressValue()
	Expect(Validate(value)).To(Succeed())
	s := &Service{ListenerOverride: true, RuntimeEndpoint: "http://127.0.0.1:9291"}
	Expect(s.validateEffectiveEndpoint(value)).To(MatchError(ContainSubstring("effective local management host")))
	s.RuntimeEndpoint = "http://127.0.0.1:9491"
	Expect(s.validateEffectiveEndpoint(value)).To(Succeed())
	value["network"].(map[string]any)["renderer_mappings"].([]any)[0].(map[string]any)["origin"] = "https://127.0.0.1:9491"
	Expect(s.validateEffectiveEndpoint(value)).To(MatchError(ContainSubstring("effective local management host")))
})

var _ = It("TestPrivateIngressRejectsAmbiguousMappingsAndInvalidUpstreams", Label("unit"), func() {
	for _, mutate := range []func(map[string]any){
		func(n map[string]any) {
			first := n["renderer_mappings"].([]any)[0].(map[string]any)
			second := map[string]any{"version_id": first["version_id"], "origin": "https://node.private:9292", "upstream": "http://127.0.0.1:9292", "reassignable": false}
			n["renderer_mappings"] = []any{first, second}
		},
		func(n map[string]any) {
			first := n["renderer_mappings"].([]any)[0].(map[string]any)
			second := map[string]any{"version_id": "00000000-0000-0000-0000-000000000002", "origin": "https://node.private:9292", "upstream": first["upstream"], "reassignable": false}
			n["renderer_mappings"] = []any{first, second}
		},
		func(n map[string]any) {
			first := n["renderer_mappings"].([]any)[0].(map[string]any)
			second := map[string]any{"version_id": "00000000-0000-0000-0000-000000000002", "origin": "https://NODE.private:09291", "upstream": "http://127.0.0.1:9292", "reassignable": false}
			n["renderer_mappings"] = []any{first, second}
		},
		func(n map[string]any) {
			n["renderer_mappings"] = []any{
				map[string]any{"version_id": "00000000-0000-0000-0000-000000000001", "origin": "https://node.private", "upstream": "http://127.0.0.1:9291", "reassignable": false},
				map[string]any{"version_id": "00000000-0000-0000-0000-000000000002", "origin": "https://node.private:443", "upstream": "http://127.0.0.1:9292", "reassignable": false},
			}
		},
		func(n map[string]any) {
			n["renderer_mappings"].([]any)[0].(map[string]any)["upstream"] = "http://127.0.0.1:9270"
		},
		func(n map[string]any) {
			n["renderer_mappings"].([]any)[0].(map[string]any)["upstream"] = "http://127.0.0.1:9272"
		},
		func(n map[string]any) {
			n["renderer_mappings"].([]any)[0].(map[string]any)["upstream"] = "http://127.0.0.1:80"
		},
		func(n map[string]any) {
			n["renderer_mappings"].([]any)[0].(map[string]any)["origin"] = "https://127.0.0.1:9291"
		},
		func(n map[string]any) {
			n["renderer_mappings"].([]any)[0].(map[string]any)["origin"] = "https://node.private:09271"
		},
	} {
		candidate := ingressValue()
		mutate(candidate["network"].(map[string]any))
		Expect(Validate(candidate)).To(HaveOccurred())
	}
	validImplicit := ingressValue()
	validImplicit["network"].(map[string]any)["renderer_mappings"].([]any)[0].(map[string]any)["origin"] = "https://node.private"
	Expect(Validate(validImplicit)).To(Succeed())

	for _, external := range []struct {
		base, rendererOrigin, upstream string
	}{
		{"https://127.0.0.1:443", "https://127.0.0.1:9291", "http://127.0.0.1:9292"},
		{"https://100.64.0.1:443", "https://100.64.0.1:9291", "http://127.0.0.1:9291"},
	} {
		validExplicit443 := ingressValue()
		netCfg := validExplicit443["network"].(map[string]any)
		netCfg["external_base_url"] = external.base
		netCfg["allowed_origins"] = []any{"http://127.0.0.1:9270", external.base}
		mapping := netCfg["renderer_mappings"].([]any)[0].(map[string]any)
		mapping["origin"] = external.rendererOrigin
		mapping["upstream"] = external.upstream
		Expect(Validate(validExplicit443)).To(Succeed(), external.base)
		Expect(PrivateRendererMappingReason(external.base, RendererMappings(validExplicit443)[0], "http://127.0.0.1:9270")).To(BeEmpty(), external.base)
	}
})

var _ = It("TestCanonicalOriginNormalizesBrowserOrigins", Label("unit"), func() {
	origin, host, auth, ok := CanonicalOrigin("http://127.0.0.1:9270")
	Expect(ok).To(BeTrue())
	Expect(origin).To(Equal("http://127.0.0.1:9270"))
	Expect(host).To(Equal("127.0.0.1:9270"))
	Expect(auth).To(Equal("127.0.0.1:9270"))

	for _, raw := range []string{
		"https://Node.Tailnet.ts.net:443",
		"https://node.tailnet.ts.net:00443",
		"https://node.tailnet.ts.net:",
		"https://node.tailnet.ts.net",
	} {
		origin, host, auth, ok = CanonicalOrigin(raw)
		Expect(ok).To(BeTrue(), raw)
		Expect(origin).To(Equal("https://node.tailnet.ts.net"))
		Expect(host).To(Equal("node.tailnet.ts.net"))
		Expect(auth).To(Equal("node.tailnet.ts.net:443"))
	}

	origin, host, auth, ok = CanonicalOrigin("https://Node.Tailnet.ts.net:09291")
	Expect(ok).To(BeTrue())
	Expect(origin).To(Equal("https://node.tailnet.ts.net:9291"))
	Expect(host).To(Equal("node.tailnet.ts.net:9291"))
	Expect(auth).To(Equal("node.tailnet.ts.net:9291"))

	for _, invalid := range []string{
		"",
		"https://node.tailnet.ts.net/",
		"https://node.tailnet.ts.net/path",
		"https://node.tailnet.ts.net?query=1",
		"https://node.tailnet.ts.net#frag",
		"ftp://node.tailnet.ts.net",
		"https://node.tailnet.ts.net:0",
		"https://node.tailnet.ts.net:70000",
	} {
		_, _, _, ok = CanonicalOrigin(invalid)
		Expect(ok).To(BeFalse(), invalid)
	}
})
