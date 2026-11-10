package scan

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"haeryong.local/haeryong/internal/domain"
	"strings"
)

var _ = It("T-008-03 records discrepancies in partial scopes without aligning zero or blocked coverage", Label("unit"), func() {
	Expect(domain.AssessmentState(nil)).To(Equal("not_assessed"))
	Expect(domain.AssessmentState([]domain.AssessmentUnit{{Outcome: "assessed"}, {Outcome: "partial", Reason: "unexamined_dependency_scope"}})).To(Equal("not_assessed"))
	Expect(domain.AssessmentState([]domain.AssessmentUnit{{Outcome: "assessed", Discrepancy: "changed retry"}, {Outcome: "partial", Reason: "unexamined caller"}})).To(Equal("outdated_against_target"))
	Expect(domain.AssessmentState([]domain.AssessmentUnit{{Outcome: "blocked", Reason: "source_denied"}})).To(Equal("unable_to_verify"))
	Expect(domain.AssessmentState([]domain.AssessmentUnit{{Outcome: "assessed"}, {Outcome: "assessed"}})).To(Equal("aligned_within_scope"))
})

var _ = It("T-008-03 decodes only the complete closed freshness envelope", Label("unit"), func() {
	valid := `{"units":[{"kind":"page","page_path":"a.html","region_id":null,"outcome":"partial","discrepancy":"","reason":"unexamined dependency","rationale":"Source limits remain explicit","citations":[]}]}`
	_, e := decodeFreshnessResult(strings.NewReader(valid))
	Expect(e).NotTo(HaveOccurred())
	for _, bad := range []string{`{}`, `{"units":null}`, `{"units":[],"aligned":true}`, valid + ` {}`, strings.Replace(valid, `"reason":"unexamined dependency",`, "", 1), strings.Replace(valid, `"reason":"unexamined dependency"`, `"reason":null`, 1), strings.Replace(valid, `"citations":[]`, `"citations":[],"extra":true`, 1)} {
		_, e := decodeFreshnessResult(strings.NewReader(bad))
		Expect(e).To(HaveOccurred(), bad)
	}
})
