package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/url"
)

var _ = It("T00804 CLI exact management reads reject widening and preserve opaque scopes", Label("unit"), func() {
	id := "11111111-2222-4333-8444-555555555555"
	opt, e := parse([]string{"related", "list", "--version", id, "--path", "nested/검토 %2F #?.html", "--region", "main", "--cursor", "opaque/+%", "--limit", "1"})
	Expect(e).NotTo(HaveOccurred())
	Expect(validateCommandFlags(opt)).To(Succeed())
	path, e := managementCLIPath(opt)
	Expect(e).NotTo(HaveOccurred())
	u, e := url.Parse(path)
	Expect(e).NotTo(HaveOccurred())
	Expect(u.Path).To(Equal("/api/v1/versions/" + id + "/related"))
	Expect(u.Query().Get("page_path")).To(Equal("nested/검토 %2F #?.html"))
	Expect(u.Query().Get("region_id")).To(Equal("main"))
	Expect(u.Query().Get("cursor")).To(Equal("opaque/+%"))
	Expect(u.Query().Has("version_id")).To(BeFalse())
	for _, args := range [][]string{{"related", "list"}, {"related", "list", "--version", id, "--document", id}, {"topics", "list", "--region", "main"}, {"topics", "list", "--version", id, "--limit", "0"}, {"topics", "list", "--version", "bad"}} {
		bad, e := parse(args)
		Expect(e).NotTo(HaveOccurred())
		Expect(validateCommandFlags(bad)).NotTo(Succeed())
	}
})
