package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("PA13 CLI resume preserves exact expected generation and requires an explicit decision and validator", Label("unit"), func() {
	for _, decision := range []string{"continue", "retry", "reconcile_only"} {
		opt, e := parse([]string{"jobs", "resume", "11111111-1111-4111-8111-111111111111", "--decision", decision, "--expected-attempt-generation", "7", "--if-match", `"captured-etag"`})
		Expect(e).NotTo(HaveOccurred())
		Expect(validateCommandFlags(opt)).To(Succeed())
		body, e := resumeProviderInput(opt)
		Expect(e).NotTo(HaveOccurred())
		Expect(body).To(Equal(map[string]any{"decision": decision, "expected_attempt_generation": int64(7)}))
	}
	for _, args := range [][]string{
		{"jobs", "resume", "11111111-1111-4111-8111-111111111111"},
		{"jobs", "resume", "11111111-1111-4111-8111-111111111111", "--decision", "automatic", "--expected-attempt-generation", "7", "--if-match", `"etag"`},
		{"jobs", "resume", "11111111-1111-4111-8111-111111111111", "--decision", "retry", "--expected-attempt-generation", "0", "--if-match", `"etag"`},
	} {
		opt, e := parse(args)
		Expect(e).NotTo(HaveOccurred())
		Expect(validateCommandFlags(opt)).NotTo(Succeed())
	}
})

var _ = It("C03CLI provider status is read-only and the binary selection belongs only to local daemon startup", Label("unit"), func() {
	opt, e := parse([]string{"providers", "status"})
	Expect(e).NotTo(HaveOccurred())
	Expect(validateCommandFlags(opt)).To(Succeed())
	Expect(isProductCommand(opt)).To(BeTrue())
	opt, e = parse([]string{"daemon", "run", "--provider-binary", "/selected/codex"})
	Expect(e).NotTo(HaveOccurred())
	binary, e := providerBinarySelection(opt)
	Expect(e).NotTo(HaveOccurred())
	Expect(binary).To(Equal("/selected/codex"))
	opt.command = []string{"providers", "status"}
	_, e = providerBinarySelection(opt)
	Expect(e).To(HaveOccurred())
	opt.command = []string{"daemon", "run"}
	opt.flags["provider-binary"] = "relative/codex"
	_, e = providerBinarySelection(opt)
	Expect(e).To(HaveOccurred())
})
