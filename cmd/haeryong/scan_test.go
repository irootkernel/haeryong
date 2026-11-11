package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("SCLI01 scan and findings CLI flag validation and request construction", Label("unit"), func() {
	validJob := "11111111-2222-4333-8444-555555555555"

	opt, err := parse([]string{"scan", "--project", "p1, p2", "--document", "d1", "--version", "v1", "--path", "docs/index.html", "--region", "sec-1", "--depth", "change-focused", "--source-mode", "pinned", "--capture", "cap-1", "--operation-id", validJob})
	Expect(err).NotTo(HaveOccurred())
	Expect(validateCommandFlags(opt)).To(Succeed())
	body, err := scanCLIInput(opt)
	Expect(err).NotTo(HaveOccurred())
	Expect(body["scope"]).To(Equal("selected_projects"))
	Expect(body["project_ids"]).To(Equal([]string{"p1", "p2"}))
	Expect(body["document_id"]).To(Equal("d1"))
	Expect(body["version_id"]).To(Equal("v1"))
	Expect(body["path_prefix"]).To(Equal("docs/index.html"))
	Expect(body["region_id"]).To(Equal("sec-1"))
	Expect(body["depth"]).To(Equal("change_focused"))
	Expect(body["source_mode"]).To(Equal("pinned"))
	Expect(body["target_set_id"]).To(Equal("cap-1"))

	optAll, err := parse([]string{"scan", "--all-allowed-projects", "--depth", "full-scope", "--target-set", "ts-1"})
	Expect(err).NotTo(HaveOccurred())
	Expect(validateCommandFlags(optAll)).To(Succeed())
	bodyAll, err := scanCLIInput(optAll)
	Expect(err).NotTo(HaveOccurred())
	Expect(bodyAll["scope"]).To(Equal("all_allowed_projects"))
	Expect(bodyAll["depth"]).To(Equal("full_scope"))
	Expect(bodyAll["target_set_id"]).To(Equal("ts-1"))

	for _, badArgs := range [][]string{
		{"scan", "extra"},
		{"scan", "--all-allowed-projects", "--project", "p1"},
		{"scan", "--region", "sec-1"},
		{"scan", "--target-set", "ts-1", "--capture", "cap-1"},
		{"scan", "--depth", "unknown-depth"},
		{"scan", "--source-mode", "live"},
		{"scan", "--operation-id", "not-a-uuid"},
		{"scan", "--job", validJob},
		{"findings", "list"},
		{"findings", "list", "--job", "not-a-uuid"},
		{"findings", "list", "--job", validJob, "--kind", "broken_reference"},
		{"findings", "list", "--job", validJob, "--status", "open"},
		{"findings", "list", "--job", validJob, "--status", "superseded"},
		{"findings", "list", "--job", validJob, "--status", "expired"},
		{"findings", "list", "--job", validJob, "--limit", "0"},
		{"findings", "list", "--job", validJob, "--limit", "201"},
		{"findings", "list", "--job", validJob, "--version", validJob},
	} {
		parsed, pErr := parse(badArgs)
		Expect(pErr).NotTo(HaveOccurred(), "parse %v", badArgs)
		vErr := validateCommandFlags(parsed)
		Expect(vErr).To(HaveOccurred(), "expected validation failure for %v", badArgs)
	}

	for _, kind := range []string{
		"backlink_index_repair",
		"related_explanation",
		"topic_assignment",
		"new_version_proposal",
		"new_document_proposal",
	} {
		for _, st := range []string{"proposed", "applied", "rejected", "stale", "blocked", "undone"} {
			optList, err := parse([]string{"findings", "list", "--job", validJob, "--kind", kind, "--status", st, "--limit", "25", "--cursor", "cur-1"})
			Expect(err).NotTo(HaveOccurred())
			Expect(validateCommandFlags(optList)).To(Succeed())
			route, err := findingsCLIInput(optList)
			Expect(err).NotTo(HaveOccurred())
			Expect(route).To(Equal("/api/v1/jobs/" + validJob + "/findings?cursor=cur-1&kind=" + kind + "&limit=25&status=" + st))
		}
	}

	for _, action := range []string{"approve", "reject"} {
		optAct, pErr := parse([]string{"findings", action, "f-1", "--if-match", `"etag"`, "--operation-id", validJob})
		Expect(pErr).NotTo(HaveOccurred())
		vErr := validateCommandFlags(optAct)
		Expect(vErr).NotTo(HaveOccurred())
		method, path, body, e := findingActionInput(optAct)
		Expect(e).NotTo(HaveOccurred())
		Expect(method).To(Equal("POST"))
		Expect(path).To(Equal("/api/v1/findings/f-1/decisions"))
		Expect(body).To(Equal(map[string]string{"decision": action}))
	}
})
