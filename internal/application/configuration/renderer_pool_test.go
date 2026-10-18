package configuration

import (
	"bytes"
	"encoding/json"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
)

func appDefault() []byte { return app.DefaultConfiguration() }

func decodeDefault() map[string]any {
	value, e := Decode(bytes.NewReader(appDefault()), "json")
	ExpectWithOffset(1, e).NotTo(HaveOccurred())
	return value
}

// poolValue mirrors the numeric form the Decode path produces.
func poolValue(base, count int) map[string]any {
	return map[string]any{"base_port": json.Number(strconv.Itoa(base)), "count": json.Number(strconv.Itoa(count))}
}

var _ = Describe("Renderer pool configuration", Label("unit"), func() {
	It("ships the default pool in defaults.json for new libraries", func() {
		value := decodeDefault()
		Expect(Validate(value)).To(Succeed())
		base, count := RendererPool(value)
		Expect(base).To(Equal(9281))
		Expect(count).To(Equal(8))
	})

	It("resolves the code default for stored profiles lacking the key", func() {
		value := decodeDefault()
		delete(value["network"].(map[string]any), "renderer_pool")
		Expect(Validate(value)).To(Succeed())
		base, count := RendererPool(value)
		Expect(base).To(Equal(9281))
		Expect(count).To(Equal(8))
	})

	It("accepts an explicitly configured pool inside the port space", func() {
		value := decodeDefault()
		value["network"].(map[string]any)["renderer_pool"] = poolValue(9400, 4)
		Expect(Validate(value)).To(Succeed())
		base, count := RendererPool(value)
		Expect(base).To(Equal(9400))
		Expect(count).To(Equal(4))
	})

	It("rejects a range intersecting the frozen legacy fixture origins", func() {
		for _, pool := range []map[string]any{poolValue(9271, 2), poolValue(9273, 2), poolValue(9270, 10)} {
			value := decodeDefault()
			value["network"].(map[string]any)["renderer_pool"] = pool
			err := Validate(value)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("legacy"))
		}
	})

	It("rejects a range covering the management listener or a mapping upstream", func() {
		// 9269..9270 isolates the management-listener collision: the range
		// stays clear of the frozen legacy fixture origins 9272/9273.
		value := decodeDefault()
		value["network"].(map[string]any)["renderer_pool"] = poolValue(9269, 2)
		err := Validate(value)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("management listener"))

		value = decodeDefault()
		network := value["network"].(map[string]any)
		network["renderer_pool"] = poolValue(9400, 8)
		network["renderer_mappings"] = []any{map[string]any{"origin": "https://render.example:9443", "upstream": "http://127.0.0.1:9402", "version_id": "00000000-0000-0000-0000-000000000001", "reassignable": false}}
		err = Validate(value)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("upstream"))

		network["renderer_mappings"] = []any{map[string]any{"origin": "https://render.example:9443", "upstream": "http://127.0.0.1:9502", "version_id": "00000000-0000-0000-0000-000000000001", "reassignable": false}}
		Expect(Validate(value)).To(Succeed())
	})

	It("rejects a range exceeding the port space and unknown fields", func() {
		value := decodeDefault()
		value["network"].(map[string]any)["renderer_pool"] = poolValue(65534, 4)
		err := Validate(value)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("port space"))

		value = decodeDefault()
		value["network"].(map[string]any)["renderer_pool"] = map[string]any{"base_port": json.Number("9400"), "count": json.Number("4"), "extra": true}
		err = Validate(value)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("renderer_pool"))
	})
})

var _ = Describe("External endpoint collision", Label("unit"), func() {
	remoteValue := func(port int, external string) map[string]any {
		value := decodeDefault()
		network := value["network"].(map[string]any)
		listener := network["listener"].(map[string]any)
		listener["port"] = json.Number(strconv.Itoa(port))
		network["remote_enabled"] = true
		network["external_base_url"] = external
		network["allowed_origins"] = []any{"http://127.0.0.1:" + strconv.Itoa(port), external}
		return value
	}

	It("statically rejects an external host equal to the configured listener host", func() {
		err := Validate(remoteValue(9270, "https://127.0.0.1:9270"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("external host"))
		// Scheme differences do not make the tuple distinguishable on the
		// wire; a distinct host stays valid.
		Expect(Validate(remoteValue(9270, "https://127.0.0.1:9443"))).To(Succeed())
		Expect(Validate(remoteValue(9270, "https://haeryong.private:9443"))).To(Succeed())
	})

	It("checks the candidate restart endpoint without a listener override", func() {
		service := &Service{}
		Expect(service.validateEffectiveEndpoint(remoteValue(9270, "https://127.0.0.1:9443"))).To(Succeed())
		// A candidate that moves its own listener onto the external host
		// fails against its own restart destination, not just the stored one.
		err := service.validateEffectiveEndpoint(remoteValue(9443, "https://127.0.0.1:9443"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("effective local management host"))
		// Remote disabled and non-network candidates skip the check.
		Expect(service.validateEffectiveEndpoint(decodeDefault())).To(Succeed())
	})

	It("uses the trusted runtime endpoint under a CLI listener override", func() {
		overridden := &Service{ListenerOverride: true, RuntimeEndpoint: "http://127.0.0.1:9443"}
		// Configuration listener 9270 with external https://127.0.0.1:9443
		// passes the static rule but collides with the actual --port 9443.
		err := overridden.validateEffectiveEndpoint(remoteValue(9270, "https://127.0.0.1:9443"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("effective local management host"))
		// An external host matching only the stored listener port, not the
		// actual override endpoint, stays usable under the override.
		Expect(overridden.validateEffectiveEndpoint(remoteValue(9270, "https://127.0.0.1:9270"))).To(Succeed())
		Expect(overridden.validateEffectiveEndpoint(remoteValue(9270, "https://haeryong.private:9443"))).To(Succeed())
	})
})
