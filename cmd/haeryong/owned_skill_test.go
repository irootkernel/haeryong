package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/skills"
)

var _ = It("GSKILL01 actual CLI version binds the same single embedded portable skill bytes without loading a provider", Label("integration"), func() {
	raw := cli(GinkgoT(), 0, "version", "--json")
	var value struct{ Data map[string]string }
	Expect(json.Unmarshal(raw, &value)).To(Succeed())
	descriptor, e := skills.Owned()
	Expect(e).NotTo(HaveOccurred())
	digest := sha256.Sum256(skills.Content())
	Expect(value.Data["skill"]).To(Equal(descriptor.Version))
	Expect(value.Data["skill_sha256"]).To(Equal(hex.EncodeToString(digest[:])))
	Expect(value.Data["skill_sha256"]).To(Equal(descriptor.SHA256))
})
