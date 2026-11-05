package configuration

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func ingressValue() map[string]any {
	value := decodeDefault()
	network := value["network"].(map[string]any)
	network["remote_enabled"] = true
	network["external_base_url"] = "https://node.private:9271"
	network["allowed_origins"] = []any{"http://127.0.0.1:9270", "https://node.private:9271"}
	network["renderer_mappings"] = []any{map[string]any{
		"version_id": "00000000-0000-0000-0000-000000000001", "origin": "https://node.private:9291",
		"upstream": "http://127.0.0.1:9291", "reassignable": false,
	}}
	return value
}

var _ = It("TestConfigurationExposureCapturesEnableDisableMappingsAndForwarding", Label("unit"), func() {
	s := &Service{}
	before, after := decodeDefault(), ingressValue()
	Expect(Validate(after)).To(Succeed())
	impact := s.exposureImpact(before, after)
	Expect(impact.Changed).To(BeTrue())
	Expect(impact.Before.ManagementOrigin).To(BeNil())
	Expect(impact.Before.RendererMappings).To(BeEmpty())
	Expect(impact.After.RemoteEnabled).To(BeTrue())
	Expect(impact.After.ManagementOrigin).To(Equal("https://node.private:9271"))
	Expect(impact.After.RendererMappings).To(Equal([]RendererMapping{{"00000000-0000-0000-0000-000000000001", "https://node.private:9291", "http://127.0.0.1:9291"}}))
	Expect(impact.ProxyConfigurationChangedByCommit).To(BeFalse())
	Expect(impact.PrivateProxyAction).To(Equal("inspect_apply_and_verify_private_proxy_separately"))
	Expect(s.exposureImpact(after, before).Changed).To(BeTrue())

	for _, change := range []func(map[string]any){
		func(n map[string]any) {
			n["external_base_url"] = "https://node.private:9443"
			n["allowed_origins"] = []any{"http://127.0.0.1:9270", "https://node.private:9443"}
		},
		func(n map[string]any) { n["renderer_mappings"] = []any{} },
		func(n map[string]any) {
			n["renderer_mappings"].([]any)[0].(map[string]any)["upstream"] = "http://127.0.0.1:9491"
		},
		func(n map[string]any) {
			n["listener"].(map[string]any)["port"] = json.Number("9390")
			n["allowed_origins"] = []any{"http://127.0.0.1:9390", "https://node.private:9271"}
		},
	} {
		next := ingressValue()
		change(next["network"].(map[string]any))
		Expect(Validate(next)).To(Succeed())
		Expect(s.exposureImpact(after, next).Changed).To(BeTrue())
	}
	changed := ingressValue()
	changed["network"].(map[string]any)["listener"].(map[string]any)["port"] = json.Number("9390")
	Expect(s.exposureImpact(after, changed).After.LocalForwardingEndpoint).To(Equal("http://127.0.0.1:9390"))
	override := &Service{ListenerOverride: true, RuntimeEndpoint: "http://127.0.0.1:9490"}
	projected := override.exposureImpact(after, changed)
	Expect(projected.After.ConfiguredLocalEndpoint).To(Equal("http://127.0.0.1:9390"))
	Expect(projected.Before.LocalForwardingEndpoint).To(Equal("http://127.0.0.1:9490"))
	Expect(projected.After.LocalForwardingEndpoint).To(Equal("http://127.0.0.1:9490"))
})

var _ = It("TestConfigurationExposureIgnoresUnrelatedPolicyAndMappingOrder", Label("unit"), func() {
	s := &Service{}
	old, next := ingressValue(), ingressValue()
	next["security"].(map[string]any)["session_absolute_seconds"] = json.Number("3600")
	Expect(Validate(next)).To(Succeed())
	Expect(s.exposureImpact(old, next).Changed).To(BeFalse())
	Expect(s.exposureImpact(old, next).PrivateProxyAction).To(Equal("none"))
	second := map[string]any{"version_id": "00000000-0000-0000-0000-000000000002", "origin": "https://node.private:9292", "upstream": "http://127.0.0.1:9292", "reassignable": false}
	first := old["network"].(map[string]any)["renderer_mappings"].([]any)[0]
	old["network"].(map[string]any)["renderer_mappings"] = []any{first, second}
	next["network"].(map[string]any)["renderer_mappings"] = []any{second, first}
	Expect(s.exposureImpact(old, next).Changed).To(BeFalse())
})
