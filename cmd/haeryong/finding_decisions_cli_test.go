package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("T00805 CLI preserves opaque finding identity, exact PUT/undo conditions and explicit reassessment scope", Label("unit"), func() {
	op := "11111111-1111-4111-8111-111111111111"
	for _, args := range [][]string{
		{"findings", "approve", "fnd-opaque", "--if-match", `"finding-fnd-opaque-r2"`, "--operation-id", op},
		{"topics", "set", "assignment", "--pinned", "false", "--decision", "accepted", "--if-match", `"overlay-assignment-r3"`},
		{"overlays", "undo", "overlay", "--if-match", `"overlay-overlay-r1"`},
	} {
		o, e := parse(args)
		Expect(e).NotTo(HaveOccurred())
		Expect(validateCommandFlags(o)).To(Succeed())
		method, path, _, e := findingActionInput(o)
		Expect(e).NotTo(HaveOccurred())
		Expect(path).NotTo(BeEmpty())
		if args[0] == "topics" {
			Expect(method).To(Equal("PUT"))
		} else {
			Expect(method).To(Equal("POST"))
		}
	}
	for _, args := range [][]string{{"topics", "set", "a", "--pinned", "true", "--decision", "rejected", "--if-match", `"x"`}, {"overlays", "undo", "a"}, {"findings", "approve", "fnd", "--if-match", "W/\"x\""}} {
		o, e := parse(args)
		if e == nil {
			Expect(validateCommandFlags(o)).NotTo(Succeed())
		}
	}
	o, e := parse([]string{"scan", "--finding", "fnd-opaque", "--expected-finding-revision", "2", "--document", op, "--version", op, "--depth", "full-scope", "--source-mode", "pinned", "--path", "page.html"})
	Expect(e).NotTo(HaveOccurred())
	Expect(validateCommandFlags(o)).To(Succeed())
	body, e := scanCLIInput(o)
	Expect(e).NotTo(HaveOccurred())
	Expect(body["page_path"]).To(Equal("page.html"))
	Expect(body).NotTo(HaveKey("path_prefix"))
	Expect(body["finding_id"]).To(Equal("fnd-opaque"))
	Expect(body["depth"]).To(Equal("full_scope"))
})
