package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The TASK-004-01 CLI surface: every new command parses with its flags and
// rejects foreign flags (C4).
var _ = Describe("source CLI flag validation", Label("unit"), func() {
	parseArgs := func(args ...string) (options, error) {
		opt, err := parse(args)
		if err != nil {
			return opt, err
		}
		return opt, validateCommandFlags(opt)
	}

	It("accepts projects register with its flags", func() {
		_, err := parseArgs("projects", "register", "--name", "Sample", "--operation-id", "10000000-0000-4000-8000-000000000001")
		Expect(err).NotTo(HaveOccurred())
		_, err = parseArgs("projects", "register", "--name", "Sample")
		Expect(err).NotTo(HaveOccurred())
	})

	It("accepts sources status and approve with their flag sets", func() {
		_, err := parseArgs("sources", "status", "--project", "10000000-0000-4000-8000-000000000002")
		Expect(err).NotTo(HaveOccurred())
		_, err = parseArgs("sources", "approve", "--project", "10000000-0000-4000-8000-000000000002", "--source-id", "git-1", "--kind", "git", "--root-path", "/repo", "--repo-id", "10000000-0000-4000-8000-000000000003", "--retention-mode", "reference_only", "--provider-ids", "a,b", "--confirmed")
		Expect(err).NotTo(HaveOccurred())
		// Flag validation accepts any subset; missing required values are
		// rejected by the command itself once dispatched.
		_, err = parseArgs("sources", "approve", "--project", "10000000-0000-4000-8000-000000000002")
		Expect(err).NotTo(HaveOccurred())
	})

	It("accepts evidence show and verify with their flags", func() {
		_, err := parseArgs("evidence", "show", "--version", "10000000-0000-4000-8000-000000000004", "--evidence", "ev-1")
		Expect(err).NotTo(HaveOccurred())
		_, err = parseArgs("evidence", "verify", "--version", "10000000-0000-4000-8000-000000000004", "--evidence", "ev-1", "--expected-sha256", "aa")
		Expect(err).NotTo(HaveOccurred())
		_, err = parseArgs("evidence", "verify", "--version", "10000000-0000-4000-8000-000000000004", "--evidence", "ev-1", "--expected-excerpt-file", "/tmp/excerpt.txt", "--operation-id", "10000000-0000-4000-8000-000000000005")
		Expect(err).NotTo(HaveOccurred())
	})

	It("rejects foreign flags on the new commands", func() {
		_, err := parseArgs("projects", "register", "--cursor", "x")
		Expect(err).To(HaveOccurred())
		_, err = parseArgs("evidence", "verify", "--version", "10000000-0000-4000-8000-000000000004", "--evidence", "ev-1", "--metadata", "m.json")
		Expect(err).To(HaveOccurred())
		_, err = parseArgs("sources", "status", "--name", "x")
		Expect(err).To(HaveOccurred())
	})

	It("rejects unknown flags in the global parser", func() {
		_, err := parseArgs("evidence", "verify", "--version", "10000000-0000-4000-8000-000000000004", "--evidence", "ev-1", "--no-such-flag", "1")
		Expect(err).To(HaveOccurred())
	})
})
