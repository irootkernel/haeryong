package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("SC08 search admits only consumed query flags including explicit empty query", Label("unit"), func() {
	for _, args := range [][]string{{"search"}, {"search", "--query", ""}, {"search", "--query", "검 source", "--project", "11111111-1111-4111-8111-111111111111", "--versions", "all", "--lifecycle", "all", "--kind", "page", "--limit", "1", "--cursor", "opaque", "--json"}} {
		o, e := parse(args)
		Expect(e).NotTo(HaveOccurred())
		Expect(validateCommandFlags(o)).To(Succeed())
		Expect(isProductCommand(o)).To(BeTrue())
	}
	for _, args := range [][]string{{"search", "--version", "v"}, {"search", "--new-document"}, {"search", "--operation-id", "o"}, {"search", "--scope", "project"}, {"search", "--query", "x", "--query", "y"}} {
		o, e := parse(args)
		if e == nil {
			e = validateCommandFlags(o)
		}
		Expect(e).To(HaveOccurred())
	}
})
