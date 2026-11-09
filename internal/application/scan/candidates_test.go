package scan

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"haeryong.local/haeryong/internal/adapters/ai/codex"
	"haeryong.local/haeryong/internal/domain"
)

var _ = It("verifies directional code dependencies with module/repository binding and rejects keyword, comment, string, and cross-module coincidences (T-008-02-01)", Label("unit"), func() {
	targetPolicyGo := `package resilience
// RetryPool and exponential backoff circuit breaker notes for HTTP transport.
type CircuitPolicy struct {
	MaxAttempts int
	BackoffMs   int
}
func (c *CircuitPolicy) ReceiverOnlyMethod() error {
	return nil
}
func EvaluateBackoff() error {
	return nil
}
`
	targetGoMod := "module example.local/project\n\ngo 1.22\n"
	targetWithMod := []string{targetGoMod, targetPolicyGo}
	targetWithModPaths := []string{"go.mod", "pkg/resilience/policy.go"}

	// 1. Negative: shared keywords in prose/comments/identifiers alone.
	focalKeywordOnly := []string{
		`package retry
// RetryPool implements exponential backoff and circuit breaker semantics.
type RetryPool struct {
	MaxAttempts int
	BackoffMs   int
}
func (p *RetryPool) ExecuteWithBackoff() error {
	return nil
}
`,
	}
	ok, detail := verifySourceDependencyFact(focalKeywordOnly, []string{"internal/retry/pool.go"}, targetWithMod, targetWithModPaths)
	Expect(ok).To(BeFalse(), "expected keyword-only overlap to reject source_fact, got detail=%q", detail)

	// 2. Negative: commented import and commented call must not verify.
	focalCommented := []string{
		`package worker
// import "example.local/project/pkg/resilience"
/*
import (
	"example.local/project/pkg/resilience"
)
*/
func Run() error {
	// return resilience.EvaluateBackoff()
	/* return resilience.EvaluateBackoff() */
	return nil
}
`,
	}
	ok, detail = verifySourceDependencyFact(focalCommented, []string{"internal/worker/run.go"}, targetWithMod, targetWithModPaths)
	Expect(ok).To(BeFalse(), "expected commented import/call to reject source_fact, got detail=%q", detail)

	// 3. Negative: string literal containing import or call syntax must not verify.
	focalStringLiteral := []string{
		"package worker\nconst sample = `import \"example.local/project/pkg/resilience\"\nresilience.EvaluateBackoff()`\nconst single = \"pkg/resilience/policy.go\"\nfunc Run() string { return sample + single }\n",
	}
	ok, detail = verifySourceDependencyFact(focalStringLiteral, []string{"internal/worker/run.go"}, targetWithMod, targetWithModPaths)
	Expect(ok).To(BeFalse(), "expected string literal import/call to reject source_fact, got detail=%q", detail)

	// 4. Negative: same relative path (pkg/resilience) in a different module (github.com/other/pkg/resilience).
	focalSameRelDifferentMod := []string{
		`package worker
import (
	"github.com/other/pkg/resilience"
)
func Run() error {
	return resilience.EvaluateBackoff()
}
`,
	}
	ok, detail = verifySourceDependencyFact(focalSameRelDifferentMod, []string{"internal/worker/run.go"}, targetWithMod, targetWithModPaths)
	Expect(ok).To(BeFalse(), "expected same-relative-path different-module import to reject source_fact, got detail=%q", detail)

	// 5. Negative: selected verified inputs lacking module binding (only pkg/resilience/policy.go without go.mod or canonical import comment) must abstain on source_fact for module-qualified import.
	focalWithModuleImport := []string{
		`package worker
import (
	"context"
	"example.local/project/pkg/resilience"
)
func Run(ctx context.Context) error {
	return nil
}
`,
	}
	ok, detail = verifySourceDependencyFact(focalWithModuleImport, []string{"internal/worker/run.go"}, []string{targetPolicyGo}, []string{"pkg/resilience/policy.go"})
	Expect(ok).To(BeFalse(), "expected unverified module prefix without go.mod/canonical-import binding to abstain on source_fact, got detail=%q", detail)

	// 6. Negative: case-sensitive path mismatch (pkg/Resilience vs pkg/resilience) must not merge.
	focalWrongCase := []string{
		`package worker
import "example.local/project/pkg/Resilience"
func Run() error {
	return nil
}
`,
	}
	ok, detail = verifySourceDependencyFact(focalWrongCase, []string{"internal/worker/run.go"}, targetWithMod, targetWithModPaths)
	Expect(ok).To(BeFalse(), "expected case-mismatched import path to reject source_fact, got detail=%q", detail)

	// 7. Negative: package/file-stem coincidence without importing target must not verify.
	focalNoImportCall := []string{
		`package worker
func Run() error {
	return resilience.EvaluateBackoff()
}
`,
	}
	ok, detail = verifySourceDependencyFact(focalNoImportCall, []string{"internal/worker/run.go"}, targetWithMod, targetWithModPaths)
	Expect(ok).To(BeFalse(), "expected call without target import to reject source_fact, got detail=%q", detail)

	// 8. Negative: reverse dependency direction (target imports focal, focal does not import target).
	targetImportingFocal := []string{
		targetGoMod,
		`package resilience
import "example.local/project/internal/worker"
func CallWorker() { _ = worker.Run }
`,
	}
	focalPlain := []string{
		`package worker
func Run() error { return nil }
`,
	}
	ok, detail = verifySourceDependencyFact(focalPlain, []string{"internal/worker/run.go"}, targetImportingFocal, targetWithModPaths)
	Expect(ok).To(BeFalse(), "expected reverse dependency direction to reject focal->target dependency, got detail=%q", detail)

	// 9. Negative: local relative import across different repositories must not verify.
	focalRelImport := `package worker
import "../../pkg/resilience"
func Run() error { return resilience.EvaluateBackoff() }
`
	ok, detail = verifySourceDependencyFactWithRepo(
		[]verifiedSourceFile{{RepoKey: "src:proj-a:repo-1", Path: "internal/worker/run.go", Text: focalRelImport}},
		[]verifiedSourceFile{{RepoKey: "src:proj-b:repo-2", Path: "pkg/resilience/policy.go", Text: targetPolicyGo}},
	)
	Expect(ok).To(BeFalse(), "expected cross-repository relative import to reject source_fact, got detail=%q", detail)

	// 10. Positive: verified go.mod module declaration + explicit grouped import path resolving to target.
	ok, detail = verifySourceDependencyFact(focalWithModuleImport, []string{"internal/worker/run.go"}, targetWithMod, targetWithModPaths)
	Expect(ok).To(BeTrue())
	Expect(detail).To(Equal("import:example.local/project/pkg/resilience"))

	// 11. Positive: Go canonical import comment on target package + aliased import and top-level symbol call.
	targetCanonicalComment := []string{
		"package resilience // import \"example.local/project/pkg/resilience\"\nfunc EvaluateBackoff() error { return nil }\n",
	}
	focalWithAliasedSymbolCall := []string{
		`package worker
import res "example.local/project/pkg/resilience"
func Run() error {
	return res.EvaluateBackoff()
}
`,
	}
	ok, detail = verifySourceDependencyFact(focalWithAliasedSymbolCall, []string{"internal/worker/run.go"}, targetCanonicalComment, []string{"pkg/resilience/policy.go"})
	Expect(ok).To(BeTrue())
	Expect(detail).To(Equal("call:res.EvaluateBackoff"))

	// 12. Positive: actually resolved local relative import in the same verified repository (without needing go.mod).
	ok, detail = verifySourceDependencyFactWithRepo(
		[]verifiedSourceFile{{RepoKey: "src:proj-1:repo-1", Path: "internal/worker/run.go", Text: focalRelImport}},
		[]verifiedSourceFile{{RepoKey: "src:proj-1:repo-1", Path: "pkg/resilience/policy.go", Text: targetPolicyGo}},
	)
	Expect(ok).To(BeTrue())
	Expect(detail).To(Equal("call:resilience.EvaluateBackoff"))

	// 13. Non-Go (Python): comments/docstrings ignored; relative and root-relative imports in same repo verified; cross-repo rejected.
	pyTarget := []string{
		`def compute_quota():
    return 42
`,
	}
	pyTargetPaths := []string{"src/billing/quota.py"}
	pyFocalCommented := []string{
		`# from ..billing.quota import compute_quota
"""
from src.billing.quota import compute_quota
"""
def run():
    return 0
`,
	}
	ok, detail = verifySourceDependencyFact(pyFocalCommented, []string{"src/app/runner.py"}, pyTarget, pyTargetPaths)
	Expect(ok).To(BeFalse(), "expected commented/docstring Python import to reject, got detail=%q", detail)

	pyFocalRelative := []string{
		`from ..billing.quota import compute_quota
def run():
    return compute_quota()
`,
	}
	ok, detail = verifySourceDependencyFact(pyFocalRelative, []string{"src/app/runner.py"}, pyTarget, pyTargetPaths)
	Expect(ok).To(BeTrue())
	Expect(detail).To(Equal("call:..billing.quota.compute_quota"))

	ok, detail = verifySourceDependencyFactWithRepo(
		[]verifiedSourceFile{{RepoKey: "git:repo-a", Path: "src/app/runner.py", Text: pyFocalRelative[0]}},
		[]verifiedSourceFile{{RepoKey: "git:repo-b", Path: "src/billing/quota.py", Text: pyTarget[0]}},
	)
	Expect(ok).To(BeFalse(), "expected cross-repo Python relative import to reject, got detail=%q", detail)
})

var _ = It("strictly validates maintenance output envelopes and rejects unknown fields, missing arrays, trailing tokens, and invalid enums", Label("unit"), func() {
	raw := []byte(`{
		"relations": [
			{
				"target_version_id": "ver-2",
				"target_page_path": "arch.html",
				"target_region_id": "overview",
				"relation_type": "related",
				"rationale": "Both units explain bounded retry coordination.",
				"uncertainty": "none"
			}
		],
		"topics": [
			{
				"topic": "retry-coordination",
				"rationale": "Explicitly covered in the region heading and body.",
				"uncertainty": "none"
			}
		],
		"content_gaps": [
			{
				"kind": "new_version_proposal",
				"theme": "distributed-deadlock-detection",
				"rationale": "Referenced in scope themes but absent from shortlisted explanations.",
				"uncertainty": "bounded_shortlist_only"
			}
		],
		"limitations": [
			{
				"code": "source_unverified",
				"reason": "No source capture attached to target."
			}
		]
	}`)
	env, err := decodeMaintenanceOutput(raw)
	Expect(err).NotTo(HaveOccurred())
	Expect(env.Relations).To(HaveLen(1))
	Expect(env.Relations[0].TargetVersionID).To(Equal("ver-2"))
	Expect(env.Topics).To(HaveLen(1))
	Expect(env.Topics[0].Topic).To(Equal("retry-coordination"))
	Expect(env.ContentGaps).To(HaveLen(1))
	Expect(env.ContentGaps[0].Theme).To(Equal("distributed-deadlock-detection"))
	Expect(env.Limitations).To(HaveLen(1))
	Expect(env.Limitations[0].Code).To(Equal("source_unverified"))
	Expect(tokenizeNormalized("retry-backoff")).To(Equal([]string{"retry", "backoff"}))
	Expect(tokenizeNormalized("retry_quantum_telepathy")).To(Equal([]string{"retry", "quantum", "telepathy"}))

	invalidPayloads := []struct {
		name string
		raw  string
	}{
		{name: "not-json", raw: `not-json`},
		{name: "null-top-level", raw: `null`},
		{name: "empty-object-missing-arrays", raw: `{}`},
		{name: "missing-limitations-array", raw: `{"relations":[],"topics":[],"content_gaps":[]}`},
		{name: "unknown-top-level-field", raw: `{"relations":[],"topics":[],"content_gaps":[],"limitations":[],"extra":true}`},
		{name: "unknown-nested-field", raw: `{"relations":[{"target_version_id":"v2","relation_type":"related","rationale":"ok","uncertainty":"none","unknown":1}],"topics":[],"content_gaps":[],"limitations":[]}`},
		{name: "trailing-json-token", raw: `{"relations":[],"topics":[],"content_gaps":[],"limitations":[]} {"trailing":1}`},
		{name: "invalid-relation-type", raw: `{"relations":[{"target_version_id":"v2","relation_type":"invented_edge","rationale":"ok","uncertainty":"none"}],"topics":[],"content_gaps":[],"limitations":[]}`},
		{name: "invalid-relation-uncertainty", raw: `{"relations":[{"target_version_id":"v2","relation_type":"related","rationale":"ok","uncertainty":"maybe"}],"topics":[],"content_gaps":[],"limitations":[]}`},
		{name: "blank-relation-rationale", raw: `{"relations":[{"target_version_id":"v2","relation_type":"related","rationale":"   ","uncertainty":"none"}],"topics":[],"content_gaps":[],"limitations":[]}`},
		{name: "blank-topic", raw: `{"relations":[],"topics":[{"topic":" ","rationale":"ok","uncertainty":"none"}],"content_gaps":[],"limitations":[]}`},
		{name: "invalid-gap-kind", raw: `{"relations":[],"topics":[],"content_gaps":[{"kind":"invented_gap","theme":"x","rationale":"ok","uncertainty":"none"}],"limitations":[]}`},
		{name: "blank-limitation-detail", raw: `{"relations":[],"topics":[],"content_gaps":[],"limitations":[{"reason":"  "}]}`},
	}
	for _, tc := range invalidPayloads {
		_, err := decodeMaintenanceOutput([]byte(tc.raw))
		Expect(err).To(HaveOccurred(), "expected decodeMaintenanceOutput to reject %s (%s)", tc.name, tc.raw)
	}
})

var _ = It("normalizes maintenance SemanticRequestSHA across runtime scan/item/target_set IDs and keeps region-scoped dependency verification isolated across same-version sibling regions (B02, B04)", Label("unit"), func() {
	p1 := "arch.html"
	rA := "reg-a"
	rB := "reg-b"
	expFocal := domain.MaintenanceExplanation{
		Role:        "focal",
		ProjectID:   "proj-1",
		DocumentID:  "doc-1",
		VersionID:   "ver-1",
		PagePath:    &p1,
		RegionID:    &rA,
		ObjectHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Size:        128,
		TargetSetID: "ts-run-1",
	}
	expCandA := domain.MaintenanceExplanation{
		Role:        "candidate",
		ProjectID:   "proj-1",
		DocumentID:  "doc-1",
		VersionID:   "ver-1",
		PagePath:    &p1,
		RegionID:    &rA,
		ObjectHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Size:        128,
		TargetSetID: "ts-run-1",
	}
	expCandB := domain.MaintenanceExplanation{
		Role:        "candidate",
		ProjectID:   "proj-1",
		DocumentID:  "doc-1",
		VersionID:   "ver-1",
		PagePath:    &p1,
		RegionID:    &rB,
		ObjectHash:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Size:        128,
		TargetSetID: "ts-run-1",
	}

	inv1 := domain.ProviderInvocation{
		Prompt: `{"unit_kind":"region","project_id":"proj-1","document_id":"doc-1","version_id":"ver-1","page_path":"arch.html","region_id":"reg-a","scan_job_id":"scan-1","item_id":"item-1","target_set_id":"ts-1","candidates":[{"project_id":"proj-1","document_id":"doc-1","version_id":"ver-1","page_path":"arch.html","region_id":"reg-b","target_set_id":"ts-1","score":12},{"project_id":"proj-1","document_id":"doc-1","version_id":"ver-1","page_path":"arch.html","region_id":"reg-a","target_set_id":"ts-1","score":10}],"requested_scope":{"scope":"selected_projects","project_ids":["proj-1"],"target_set_id":"ts-1"}}`,
		Maintenance: &domain.MaintenanceInvocation{
			ScanJobID:    "scan-1",
			ItemID:       "item-1",
			UnitKind:     "region",
			ProjectID:    "proj-1",
			DocumentID:   "doc-1",
			VersionID:    "ver-1",
			PagePath:     &p1,
			RegionID:     &rA,
			SourceMode:   "pinned",
			Explanations: []domain.MaintenanceExplanation{expFocal, expCandB, expCandA},
			ReadBindings: []domain.MaintenanceReadBinding{{ReadID: "r-1", ProjectID: "proj-1", VersionID: "ver-1", TargetSetID: "ts-1"}},
		},
	}
	inv2 := domain.ProviderInvocation{
		Prompt: `{"unit_kind":"region","project_id":"proj-1","document_id":"doc-1","version_id":"ver-1","page_path":"arch.html","region_id":"reg-a","scan_job_id":"scan-2","item_id":"item-99","target_set_id":"ts-2","candidates":[{"project_id":"proj-1","document_id":"doc-1","version_id":"ver-1","page_path":"arch.html","region_id":"reg-a","target_set_id":"ts-2","score":10},{"project_id":"proj-1","document_id":"doc-1","version_id":"ver-1","page_path":"arch.html","region_id":"reg-b","target_set_id":"ts-2","score":12}],"requested_scope":{"scope":"selected_projects","project_ids":["proj-1"],"target_set_id":"ts-2"}}`,
		Maintenance: &domain.MaintenanceInvocation{
			ScanJobID:    "scan-2",
			ItemID:       "item-99",
			UnitKind:     "region",
			ProjectID:    "proj-1",
			DocumentID:   "doc-1",
			VersionID:    "ver-1",
			PagePath:     &p1,
			RegionID:     &rA,
			SourceMode:   "pinned",
			Explanations: []domain.MaintenanceExplanation{expCandA, expFocal, expCandB},
			ReadBindings: []domain.MaintenanceReadBinding{{ReadID: "r-2", ProjectID: "proj-1", VersionID: "ver-1", TargetSetID: "ts-2"}},
		},
	}
	target1 := []byte(`{"action":"maintenance","project_id":"proj-1","document_id":"doc-1","version_id":"ver-1","page_path":"arch.html","region_id":"reg-a","scan_job_id":"scan-1","item_id":"item-1"}`)
	target2 := []byte(`{"action":"maintenance","project_id":"proj-1","document_id":"doc-1","version_id":"ver-1","page_path":"arch.html","region_id":"reg-a","scan_job_id":"scan-2","item_id":"item-99"}`)
	sha1, err := codex.SemanticRequestSHA("owner-1", target1, inv1, nil)
	Expect(err).NotTo(HaveOccurred())
	sha2, err := codex.SemanticRequestSHA("owner-1", target2, inv2, nil)
	Expect(err).NotTo(HaveOccurred())
	Expect(sha1).To(Equal(sha2))

	// B02: same-version Region A (a.go, no import of C) vs Region B (b.go, imports C) vs Region C (c.go).
	goModFile := verifiedSourceFile{RepoKey: "src:proj-1:src-1", Path: "go.mod", Text: "module example.local/multi\n\ngo 1.22\n"}
	regionAFile := verifiedSourceFile{RepoKey: "src:proj-1:src-1", Path: "pkg/alpha/a.go", Text: "package alpha\nfunc Alpha() int { return 1 }\n"}
	regionBFile := verifiedSourceFile{RepoKey: "src:proj-1:src-1", Path: "pkg/beta/b.go", Text: "package beta\nimport \"example.local/multi/pkg/gamma\"\nfunc Beta() int { return gamma.Gamma() }\n"}
	regionCFile := verifiedSourceFile{RepoKey: "src:proj-1:src-1", Path: "pkg/gamma/c.go", Text: "package gamma\nfunc Gamma() int { return 3 }\n"}

	okAC, detailAC := verifySourceDependencyFactWithRepo(
		[]verifiedSourceFile{goModFile, regionAFile},
		[]verifiedSourceFile{goModFile, regionCFile},
	)
	Expect(okAC).To(BeFalse(), "expected Region A without import of C to reject source_fact, got %q", detailAC)

	okBC, detailBC := verifySourceDependencyFactWithRepo(
		[]verifiedSourceFile{goModFile, regionBFile},
		[]verifiedSourceFile{goModFile, regionCFile},
	)
	Expect(okBC).To(BeTrue())
	Expect(detailBC).To(Equal("call:gamma.Gamma"))
})

var _ = It("evaluates QR1..QR3 content-gap direction, distinct same-pair mechanism signatures vs true-equivalent deduplication, and stem/paraphrase topic normalization", Label("unit"), func() {
	// QR3: topic normalization and morphological stem matching.
	Expect(normalizeTopicLabel("  Retry   Coordination & Coordination!! ")).To(Equal("retry-coordination"))
	Expect(canonicalStem("reconciliation")).To(Equal(canonicalStem("reconcile")))
	Expect(canonicalStem("isolation")).To(Equal(canonicalStem("isolated")))
	Expect(canonicalStem("isolation")).To(Equal(canonicalStem("isolate")))
	Expect(canonicalStem("coordination")).To(Equal(canonicalStem("coordinating")))
	Expect(canonicalStem("recovery")).To(Equal(canonicalStem("recovered")))

	ratio, shared := overlapRatioWithStems(
		map[string]bool{"reconcile": true, "checkpoint": true},
		map[string]bool{"reconciliation": true, "unrelated": true},
		map[string]bool{canonicalStem("reconciliation"): true, canonicalStem("unrelated"): true},
	)
	Expect(ratio).To(BeNumerically(">", 0))
	Expect(shared).To(Equal([]string{"reconcile"}))

	// QR2 + EE advice: only conservative case/whitespace normalization of the full ordered rationale with matching dependency/qualification deduplicates;
	// operators/symbols, reordered vocabulary, negations, and strict supersets on the same (source, target, relation_type) remain distinct and order-independent.
	r1 := "Both units enforce bounded retry backoff before marking a child job terminal."
	r1Dup := "  BOTH units   enforce bounded retry backoff\n\tbefore marking a child job terminal.  "
	r1Reordered := "Before marking a child job terminal, both units enforce bounded retry backoff."
	r1Negated := "Both units never enforce bounded retry backoff before marking a child job terminal."
	r1Superset := "Both units enforce bounded retry backoff before marking a child job terminal, and also isolate read-only SQLite snapshot transactions from concurrent writer mutations."
	rOpLess := "Enforces attempt_count < max_attempts && revision == expected_revision before dispatch."
	rOpGreater := "Enforces attempt_count > max_attempts && revision != expected_revision before dispatch."

	k1, n1 := relationMechanismSignature("conceptual_analogy", r1, "", "", "source_unverified")
	k1Dup, n1Dup := relationMechanismSignature("conceptual_analogy", r1Dup, "", "", "source_unverified")
	kReord, nReord := relationMechanismSignature("conceptual_analogy", r1Reordered, "", "", "source_unverified")
	kNeg, nNeg := relationMechanismSignature("conceptual_analogy", r1Negated, "", "", "source_unverified")
	kSup, nSup := relationMechanismSignature("conceptual_analogy", r1Superset, "", "", "source_unverified")
	kLess, nLess := relationMechanismSignature("conceptual_analogy", rOpLess, "", "", "source_unverified")
	kGreater, nGreater := relationMechanismSignature("conceptual_analogy", rOpGreater, "", "", "source_unverified")

	rec1 := acceptedRelationRecord{rationale: r1, normOrdered: n1, uncertainty: "source_unverified", mechKey: k1}
	rec1Dup := acceptedRelationRecord{rationale: r1Dup, normOrdered: n1Dup, uncertainty: "source_unverified", mechKey: k1Dup}
	recReord := acceptedRelationRecord{rationale: r1Reordered, normOrdered: nReord, uncertainty: "source_unverified", mechKey: kReord}
	recNeg := acceptedRelationRecord{rationale: r1Negated, normOrdered: nNeg, uncertainty: "source_unverified", mechKey: kNeg}
	recSup := acceptedRelationRecord{rationale: r1Superset, normOrdered: nSup, uncertainty: "source_unverified", mechKey: kSup}
	recLess := acceptedRelationRecord{rationale: rOpLess, normOrdered: nLess, uncertainty: "source_unverified", mechKey: kLess}
	recGreater := acceptedRelationRecord{rationale: rOpGreater, normOrdered: nGreater, uncertainty: "source_unverified", mechKey: kGreater}

	Expect(areRelationMechanismsEquivalent(rec1, rec1Dup)).To(BeTrue(), "expected case/whitespace-only difference of identical ordered text and qualification to deduplicate")
	Expect(k1).To(Equal(k1Dup))
	Expect(areRelationMechanismsEquivalent(rec1, recReord)).To(BeFalse(), "expected different word/role order not to collapse")
	Expect(k1).NotTo(Equal(kReord))
	Expect(areRelationMechanismsEquivalent(rec1, recNeg)).To(BeFalse(), "expected negated rationale not to collapse")
	Expect(k1).NotTo(Equal(kNeg))
	Expect(areRelationMechanismsEquivalent(rec1, recSup)).To(BeFalse(), "expected strict superset rationale containing an additional mechanism not to collapse")
	Expect(k1).NotTo(Equal(kSup))
	Expect(areRelationMechanismsEquivalent(recLess, recGreater)).To(BeFalse(), "expected same-words/different-operator rationale (< vs >, == vs !=) not to collapse")
	Expect(kLess).NotTo(Equal(kGreater))

	// QR1: neighbor-absence rationale on focal-covered theme vs genuine missing focal detail, plus rejection/explicit withdrawal of unsupported universal/source claims.
	Expect(rationaleDefinesNeighborAbsence("The focal region explains retry backoff in detail, but shortlisted candidate neighbors omit retry backoff.")).To(BeTrue())
	Expect(rationaleDefinesNeighborAbsence("The focal region mentions deadlock recovery in its heading but this section does not explain the lock-ordering recovery algorithm.")).To(BeFalse())
	Expect(rationaleIdentifiesMissingFocalDetail("This section mentions lease fencing without explaining how stale epoch tokens are rejected during failover.")).To(BeTrue())

	cleanUnsupportedOnly, withdrewOnly, rejectOnly := evaluateGapUnsupportedClaims("Inspected source code confirms this protocol is absent from the entire wiki.", false, false)
	Expect(withdrewOnly).To(BeTrue())
	Expect(rejectOnly).To(Equal("unsupported_source_or_universal_absence_claim"))
	Expect(cleanUnsupportedOnly).To(BeEmpty())

	cleanMixed, withdrewMixed, rejectMixed := evaluateGapUnsupportedClaims("Inspected source code confirms this protocol is absent from the entire wiki. This section mentions lease fencing without explaining how stale epoch tokens are rejected during failover.", false, false)
	Expect(withdrewMixed).To(BeTrue())
	Expect(rejectMixed).To(BeEmpty())
	Expect(cleanMixed).To(ContainSubstring("Unsupported universal-scope or uninspected-source claim withdrawn; retained only for focal scope:"))
	Expect(cleanMixed).NotTo(ContainSubstring("entire wiki"))
	Expect(cleanMixed).NotTo(ContainSubstring("Inspected source code"))

	// OR2 deterministic stem-representative deduplication: focal {publish, published} vs candidate b={published}, bStems={publish}
	// must never double-count or vary with Go map iteration order.
	for iter := 0; iter < 32; iter++ {
		focalPub := map[string]bool{"publish": true, "published": true}
		candPub := map[string]bool{"published": true}
		candPubStems := map[string]bool{canonicalStem("published"): true}
		dRatio, dShared, dSpec := discriminativeOverlapWithStems(focalPub, candPub, candPubStems, 4, map[string]int{"publish": 1})
		Expect(dShared).To(Equal([]string{"publish"}))
		Expect(dSpec).To(Equal(1))
		Expect(dRatio).To(BeNumerically("~", 1.0, 1e-9))

		oRatio, oShared := overlapRatioWithStems(focalPub, candPub, candPubStems)
		Expect(oShared).To(Equal([]string{"publish"}))
		Expect(oRatio).To(BeNumerically("~", 1.0, 1e-9))
	}

	// OR2 discriminative content ranking: specific rare mechanism overlap strictly outranks generic-only overlap.
	focalMech := map[string]bool{"protocol": true, "architecture": true, "checkpoint": true, "quorum": true}
	candGenericOnly := map[string]bool{"protocol": true, "architecture": true}
	candGenericStems := map[string]bool{canonicalStem("protocol"): true, canonicalStem("architecture"): true}
	candSpecificMech := map[string]bool{"checkpoint": true, "quorum": true}
	candSpecificStems := map[string]bool{canonicalStem("checkpoint"): true, canonicalStem("quorum"): true}
	poolDF := map[string]int{
		canonicalStem("protocol"):     6,
		canonicalStem("architecture"): 6,
		canonicalStem("checkpoint"):   1,
		canonicalStem("quorum"):       1,
	}
	genScore, _, genSpec := discriminativeOverlapWithStems(focalMech, candGenericOnly, candGenericStems, 6, poolDF)
	specScore, specShared, specCnt := discriminativeOverlapWithStems(focalMech, candSpecificMech, candSpecificStems, 6, poolDF)
	Expect(genSpec).To(Equal(0))
	Expect(specCnt).To(Equal(2))
	Expect(specShared).To(Equal([]string{"checkpoint", "quorum"}))
	Expect(specScore).To(BeNumerically(">", genScore*4))

	// OR3: negated source-absence phrase preservation and declared missing-theme conversion (convertDeclaredGap) without limitation-to-gap synthesis.
	focalUnitSig := unitSignals{
		HeadingTerms:  map[string]bool{"checkpoint": true, "protocol": true},
		HeadingStems:  map[string]bool{canonicalStem("checkpoint"): true, canonicalStem("protocol"): true},
		BodyTerms:     map[string]bool{"checkpoint": true, "quorum": true, "lease": true, "protocol": true},
		BodyStems:     map[string]bool{canonicalStem("checkpoint"): true, canonicalStem("quorum"): true, canonicalStem("lease"): true, canonicalStem("protocol"): true},
		DeclaredTerms: map[string]bool{"deadlock": true, "arbitration": true},
		DeclaredStems: map[string]bool{canonicalStem("deadlock"): true, canonicalStem("arbitration"): true},
		Themes:        []string{"deadlock-arbitration"},
	}
	cleanNegatedSrc, withdrewNegatedSrc, rejectNegatedSrc := evaluateGapUnsupportedClaims(
		"Without supplied source files, this section leaves the internal guarantee around stale lease epoch rejection unspecified.",
		false,
		false,
	)
	Expect(withdrewNegatedSrc).To(BeFalse())
	Expect(rejectNegatedSrc).To(BeEmpty())
	Expect(cleanNegatedSrc).To(ContainSubstring("Without supplied source files"))

	groundedDeclGap, usedParaDeclGap, convDeclGap := evaluateTopicFocalGrounding(
		"deadlock-arbitration",
		"deadlock-arbitration",
		[]string{"deadlock", "arbitration"},
		"Declared in scope themes but absent from the focal region body.",
		focalUnitSig,
		nil,
		nil,
	)
	Expect(groundedDeclGap).To(BeFalse())
	Expect(usedParaDeclGap).To(BeFalse())
	Expect(convDeclGap).To(BeTrue(), "expected declared theme absent from focal body to set convertDeclaredGap=true")

	// OR4: source-grounded topic paraphrase without every-slug-token literal stem overlap vs ungrounded multi-token hallucination.
	groundedPara, usedPara, convPara := evaluateTopicFocalGrounding(
		"quorum-lease-durability",
		"quorum-lease-durability",
		[]string{"quorum", "lease", "durability"},
		"The focal unit explains how quorum lease validation preserves checkpoint state across restarts.",
		focalUnitSig,
		nil,
		nil,
	)
	Expect(groundedPara).To(BeTrue())
	Expect(usedPara).To(BeTrue())
	Expect(convPara).To(BeFalse())

	groundedHalluc, _, convHalluc := evaluateTopicFocalGrounding(
		"quorum-quantum-telepathy",
		"quorum-quantum-telepathy",
		[]string{"quorum", "quantum", "telepathy"},
		"The focal unit explains quorum lease validation.",
		focalUnitSig,
		nil,
		nil,
	)
	Expect(groundedHalluc).To(BeFalse())
	Expect(convHalluc).To(BeFalse())

	// EE-OR4-01: Opposite-meaning qualifiers must not be grounded via root/compound matching or re-accepted via paraphrase/generic fallback.
	boundedBodySig := unitSignals{
		HeadingTerms: map[string]bool{"checkpoint": true, "protocol": true},
		HeadingStems: map[string]bool{canonicalStem("checkpoint"): true, canonicalStem("protocol"): true},
		BodyTerms:    map[string]bool{"bounded": true, "retry": true, "backoff": true, "checkpoint": true, "blocking": true, "stateful": true},
		BodyStems: map[string]bool{
			canonicalStem("bounded"):    true,
			canonicalStem("retry"):      true,
			canonicalStem("backoff"):    true,
			canonicalStem("checkpoint"): true,
			canonicalStem("blocking"):   true,
			canonicalStem("stateful"):   true,
		},
	}
	for _, negTokens := range [][]string{
		{"unbounded", "retry"},
		{"unbounded", "retry", "backoff"},
		{"nonblocking", "checkpoint"},
		{"stateless", "checkpoint"},
	} {
		slug := strings.Join(negTokens, "-")
		gNeg, _, cNeg := evaluateTopicFocalGrounding(
			slug,
			slug,
			negTokens,
			"The focal unit coordinates bounded retry backoff and blocking stateful checkpoint execution.",
			boundedBodySig,
			nil,
			nil,
		)
		Expect(gNeg).To(BeFalse(), "expected contradicted qualifier topic %q to be rejected", slug)
		Expect(cNeg).To(BeFalse())
	}

	// Reverse contradiction: when focal body only describes "unbounded", "nonblocking", and "stateless", positive counterparts ("bounded", "blocking", "stateful") must be rejected even when "bounded" is in genericMechanismTokens.
	unboundedOnlySig := unitSignals{
		BodyTerms: map[string]bool{"unbounded": true, "retry": true, "backoff": true, "nonblocking": true, "stateless": true, "checkpoint": true},
		BodyStems: map[string]bool{
			canonicalStem("unbounded"):   true,
			canonicalStem("retry"):       true,
			canonicalStem("backoff"):     true,
			canonicalStem("nonblocking"): true,
			canonicalStem("stateless"):   true,
			canonicalStem("checkpoint"):  true,
		},
	}
	for _, revTokens := range [][]string{
		{"bounded", "retry"},
		{"bounded", "retry", "backoff"},
		{"blocking", "checkpoint"},
		{"stateful", "checkpoint"},
	} {
		slug := strings.Join(revTokens, "-")
		gRev, _, cRev := evaluateTopicFocalGrounding(
			slug,
			slug,
			revTokens,
			"The focal unit uses unbounded retry backoff and nonblocking stateless checkpoint queues.",
			unboundedOnlySig,
			nil,
			nil,
		)
		Expect(gRev).To(BeFalse(), "expected reverse-contradicted topic %q to be rejected", slug)
		Expect(cRev).To(BeFalse())
	}

	// Positive preservation: when the focal body genuinely describes the negative qualifier itself, or uses a non-contradicting compound ("leaseholder"), grounding succeeds.
	for _, posTokens := range [][]string{
		{"unbounded", "retry"},
		{"nonblocking", "checkpoint"},
		{"stateless", "checkpoint"},
	} {
		slug := strings.Join(posTokens, "-")
		gPos, _, cPos := evaluateTopicFocalGrounding(
			slug,
			slug,
			posTokens,
			"The focal unit uses unbounded retry backoff and nonblocking stateless checkpoint queues.",
			unboundedOnlySig,
			nil,
			nil,
		)
		Expect(gPos).To(BeTrue(), "expected genuinely present negative qualifier topic %q to be accepted", slug)
		Expect(cPos).To(BeFalse())
	}
	gComp, _, cComp := evaluateTopicFocalGrounding(
		"leaseholder-quorum",
		"leaseholder-quorum",
		[]string{"leaseholder", "quorum"},
		"The focal unit explains how quorum lease validation preserves checkpoint state.",
		focalUnitSig,
		nil,
		nil,
	)
	Expect(gComp).To(BeTrue())
	Expect(cComp).To(BeFalse())
})

var _ = It("enforces <=8192B full-fit-first adaptive nomination cards without average quota, UTF-8 head/tail truncation with tiny-budget and unspliced iterative bounds, <=144KiB batch prompt budget, two-pass >64 batch planning, and strict closed-schema nomination decoding (TASK-008-02)", Label("unit"), func() {
	// 1. Single oversized card (>8192B raw/encoded) enforces <= 8192 serialized and encoded JSON bytes via UTF-8 head/tail windowing, preserves service identity, and records ExcerptCoverage=="truncated" + exact original TotalBodyBytes.
	oversizedBody := "HEAD-ANCHOR-가나다 " + strings.Repeat("한국어-다중바이트-아키텍처-설명-", 220) + " TAIL-ANCHOR-마바사" // > 10,000 UTF-8 bytes
	var manyHeadings, manyTopics, manyPaths, manyRefs, manyShared []string
	for i := 0; i < 12; i++ {
		manyHeadings = append(manyHeadings, fmt.Sprintf("Heading-%02d-%s", i, strings.Repeat("H", 130)))
		manyTopics = append(manyTopics, fmt.Sprintf("topic-%02d-%s", i, strings.Repeat("t", 90)))
		manyPaths = append(manyPaths, fmt.Sprintf("internal/module/%02d/%s.go", i, strings.Repeat("p", 160)))
		manyRefs = append(manyRefs, fmt.Sprintf("u%02d", (i%10)+2))
		manyShared = append(manyShared, fmt.Sprintf("u%02d", (i%10)+2))
	}
	rawCard := domain.MaintenanceNominationCard{
		UnitID:              "u01",
		ItemID:              "item-000001",
		UnitKind:            "region",
		ProjectID:           "00000000-0000-4000-8000-000000000001",
		DocumentID:          "00000000-0000-4000-8000-000000000002",
		VersionID:           "00000000-0000-4000-8000-000000000003",
		PagePath:            "private/secret/page.html",
		RegionID:            "private-region-anchor",
		TargetSetID:         "00000000-0000-4000-8000-000000000004",
		ObjectHash:          strings.Repeat("a", 64),
		ByteSize:            16384,
		Title:               "Oversized Title " + strings.Repeat("T", 240),
		Headings:            manyHeadings,
		DeclaredTopics:      manyTopics,
		SourcePaths:         manyPaths,
		BodyExcerpt:         oversizedBody,
		ExcerptCoverage:     "full",
		TotalBodyBytes:      len(oversizedBody),
		AuthoredRefUnitIDs:  manyRefs,
		SharedSourceUnitIDs: manyShared,
	}
	projCard, storedCard, err := ProjectNominationCard(rawCard)
	Expect(err).NotTo(HaveOccurred())
	projJSON, err := json.Marshal(projCard)
	Expect(err).NotTo(HaveOccurred())
	Expect(len(projJSON)).To(BeNumerically("<=", MaxNominationCardJSONBytes))
	Expect(encodedJSONFragmentBytes(projJSON)).To(BeNumerically("<=", MaxNominationCardJSONBytes))
	Expect(projCard.ExcerptCoverage).To(Equal("truncated"))
	Expect(projCard.TotalBodyBytes).To(Equal(len(oversizedBody)))
	Expect(utf8.ValidString(projCard.BodyExcerpt)).To(BeTrue())
	Expect(strings.Count(projCard.BodyExcerpt, "\n...\n")).To(Equal(1))
	Expect(projCard.BodyExcerpt).To(HavePrefix("HEAD-ANCHOR-가나다"))
	Expect(projCard.BodyExcerpt).To(HaveSuffix("TAIL-ANCHOR-마바사"))
	Expect(storedCard.ItemID).To(Equal("item-000001"))
	Expect(storedCard.DocumentID).To(Equal(rawCard.DocumentID))
	Expect(storedCard.PagePath).To(Equal("private/secret/page.html"))
	Expect(storedCard.RegionID).To(Equal("private-region-anchor"))
	Expect(storedCard.ObjectHash).To(Equal(rawCard.ObjectHash))
	Expect(storedCard.TotalBodyBytes).To(Equal(len(oversizedBody)))
	// Private coordinates must never appear in the provider-projected card JSON.
	Expect(string(projJSON)).NotTo(ContainSubstring("item-000001"))
	Expect(string(projJSON)).NotTo(ContainSubstring("private/secret/page.html"))
	Expect(string(projJSON)).NotTo(ContainSubstring("private-region-anchor"))
	Expect(string(projJSON)).NotTo(ContainSubstring(rawCard.ObjectHash))

	// 1a. Full-fit-first preservation (>900B and >2048B) without any average-per-card quota:
	// when the batch fits within 144 KiB (with 8 KiB reserve), larger cards (1.5 KiB..5.8 KiB) remain ExcerptCoverage=="full" with 100% of their body intact.
	fullFitRaw24 := make([]domain.MaintenanceNominationCard, 24)
	largeBodies := map[int]string{
		0:  "LARGE-0-START " + strings.Repeat("alpha-reconciliation-barrier ", 55) + "LARGE-0-END", // ~1600B (>900B)
		5:  "LARGE-5-START " + strings.Repeat("beta-quorum-epoch-fencing ", 110) + "LARGE-5-END",   // ~2880B (>2048B)
		12: "LARGE-12-START " + strings.Repeat("gamma-snapshot-isolation ", 165) + "LARGE-12-END",  // ~4150B (>2048B)
		23: "LARGE-23-START " + strings.Repeat("delta-journal-recovery ", 240) + "LARGE-23-END",    // ~5550B (>2048B, <=8192B)
	}
	for i := 0; i < 24; i++ {
		body := fmt.Sprintf("Small region %02d body explaining bounded checkpoint coordination. %s", i+1, strings.Repeat("steady-state ", 28))
		if lb, ok := largeBodies[i]; ok {
			body = lb
		}
		fullFitRaw24[i] = domain.MaintenanceNominationCard{
			UnitID:          fmt.Sprintf("u%02d", i+1),
			ItemID:          fmt.Sprintf("item-%06d", i+1),
			UnitKind:        "region",
			ProjectID:       rawCard.ProjectID,
			DocumentID:      rawCard.DocumentID,
			VersionID:       rawCard.VersionID,
			PagePath:        fmt.Sprintf("page-%02d.html", i+1),
			RegionID:        fmt.Sprintf("reg-%02d", i+1),
			TargetSetID:     rawCard.TargetSetID,
			ObjectHash:      rawCard.ObjectHash,
			ByteSize:        8192,
			Title:           fmt.Sprintf("Region %02d Title", i+1),
			Headings:        []string{fmt.Sprintf("Section %02d", i+1)},
			DeclaredTopics:  []string{"checkpoint-coordination"},
			SourcePaths:     []string{"internal/application/scan/nomination.go"},
			BodyExcerpt:     body,
			ExcerptCoverage: "full",
			TotalBodyBytes:  len(body),
		}
	}
	storedFull24, projFull24, promptFull24, digestFull24, err := ProjectNominationBatchCards(fullFitRaw24)
	Expect(err).NotTo(HaveOccurred())
	Expect(len(promptFull24) + NominationFixedFrameReserve).To(BeNumerically("<=", MaxNominationFrameBytes))
	Expect(encodedJSONTextBytes([]byte(promptFull24)) + NominationFixedFrameReserve).To(BeNumerically("<=", MaxNominationFrameBytes))
	Expect(digestFull24).To(HavePrefix("sha256:"))
	for i := 0; i < 24; i++ {
		Expect(projFull24[i].ExcerptCoverage).To(Equal("full"), "card %d must remain full when batch fits frame budget", i)
		Expect(projFull24[i].BodyExcerpt).To(Equal(fullFitRaw24[i].BodyExcerpt))
		Expect(projFull24[i].TotalBodyBytes).To(Equal(len(fullFitRaw24[i].BodyExcerpt)))
		Expect(storedFull24[i].ExcerptCoverage).To(Equal("full"))
		Expect(storedFull24[i].BodyExcerpt).To(Equal(fullFitRaw24[i].BodyExcerpt))
		Expect(strings.Contains(projFull24[i].BodyExcerpt, "\n...\n")).To(BeFalse())
	}

	// Single >8KiB card paired with a 3.4KiB card in a 2-card batch: only the >8KiB card is head/tail truncated while the 3.4KiB sibling stays "full".
	pairBatch := []domain.MaintenanceNominationCard{
		rawCard,
		{
			UnitID:          "u02",
			ItemID:          "item-000002",
			UnitKind:        "region",
			ProjectID:       rawCard.ProjectID,
			DocumentID:      rawCard.DocumentID,
			VersionID:       rawCard.VersionID,
			PagePath:        "page-02.html",
			RegionID:        "reg-02",
			TargetSetID:     rawCard.TargetSetID,
			ObjectHash:      rawCard.ObjectHash,
			ByteSize:        8192,
			Title:           "Sibling 3.4KiB Card",
			Headings:        []string{"Sibling Heading"},
			BodyExcerpt:     largeBodies[12],
			ExcerptCoverage: "full",
			TotalBodyBytes:  len(largeBodies[12]),
		},
	}
	_, projPair, _, _, err := ProjectNominationBatchCards(pairBatch)
	Expect(err).NotTo(HaveOccurred())
	Expect(projPair[0].ExcerptCoverage).To(Equal("truncated"))
	Expect(projPair[0].TotalBodyBytes).To(Equal(len(oversizedBody)))
	Expect(strings.Count(projPair[0].BodyExcerpt, "\n...\n")).To(Equal(1))
	Expect(projPair[1].ExcerptCoverage).To(Equal("full"))
	Expect(projPair[1].BodyExcerpt).To(Equal(largeBodies[12]))
	Expect(projPair[1].TotalBodyBytes).To(Equal(len(largeBodies[12])))

	// 1b. Tiny-budget (maxBytes <= 5) termination and UTF-8 rune boundary safety without nested or overflowing separators.
	for _, sample := range []string{
		"HEAD-가나다라마바사-MIDDLE-아자차카타파하-TAIL",
		"0123456789abcdefghijklmnopqrstuvwxyz",
	} {
		for _, limit := range []int{-1, 0, 1, 2, 3, 4, 5, 6, 7, 8, 12, 24, 30} {
			out, wasTrunc := truncateUTF8HeadTailBytes(sample, limit)
			Expect(wasTrunc).To(BeTrue(), "expected sample len=%d to report truncated at limit=%d", len(sample), limit)
			Expect(utf8.ValidString(out)).To(BeTrue(), "expected valid UTF-8 at limit=%d", limit)
			if limit <= 0 {
				Expect(out).To(BeEmpty())
			} else {
				Expect(len(out)).To(BeNumerically("<=", limit), "expected len(out)<=%d, got %d (%q)", limit, len(out), out)
			}
			if limit <= len(nominationHeadTailSeparator) {
				Expect(strings.Contains(out, nominationHeadTailSeparator)).To(BeFalse(), "tiny budget <=5 must not emit separator")
			} else {
				Expect(strings.Count(out, nominationHeadTailSeparator)).To(BeNumerically("<=", 1))
			}
		}
	}

	// 1c. Escape-heavy + multi-byte UTF-8 64-card batch projection: deterministic water-filling
	// shrinks largest encoded cards first from the unspliced original excerpt (never nesting "\n...\n"),
	// accounts for Driver.frame's second-level JSON quoting BEFORE final cards/digest/storage,
	// retains all 64 cards and full service identities, and preserves exact original TotalBodyBytes.
	escapeHeavyBody := "START-가나다 " + strings.Repeat(`"quoted\path\segment"	<tag>&한국어\n`, 70) + " END-마바사"
	rawEscape64 := make([]domain.MaintenanceNominationCard, 64)
	initEsc64Projected := make([]domain.MaintenanceProjectedCard, 64)
	for i := 0; i < 64; i++ {
		c := rawCard
		c.UnitID = fmt.Sprintf("u%02d", i+1)
		c.ItemID = fmt.Sprintf("item-%06d", i+1)
		c.PagePath = fmt.Sprintf("private/page-%02d.html", i+1)
		c.RegionID = fmt.Sprintf("region-%02d", i+1)
		c.Title = fmt.Sprintf(`Title "%02d" \\ 한국어`, i+1)
		c.Headings = []string{
			strings.Repeat(`"H1\escape"`, 10),
			strings.Repeat(`"H2\escape"`, 10),
			strings.Repeat(`"H3\escape"`, 10),
		}
		c.DeclaredTopics = []string{"lease-fencing", "quorum-epoch"}
		c.SourcePaths = []string{"internal/application/scan/nomination.go"}
		c.AuthoredRefUnitIDs = []string{"u02"}
		c.SharedSourceUnitIDs = []string{"u03"}
		c.BodyExcerpt = escapeHeavyBody
		c.TotalBodyBytes = len(escapeHeavyBody)
		rawEscape64[i] = c

		singleEscProj, _, pErr := ProjectNominationCard(c)
		Expect(pErr).NotTo(HaveOccurred())
		Expect(singleEscProj.ExcerptCoverage).To(Equal("full"))
		rawSingleEsc, mErr := json.Marshal(singleEscProj)
		Expect(mErr).NotTo(HaveOccurred())
		Expect(len(rawSingleEsc)).To(BeNumerically("<=", MaxNominationCardJSONBytes))
		Expect(encodedJSONFragmentBytes(rawSingleEsc)).To(BeNumerically("<=", MaxNominationCardJSONBytes))
		initEsc64Projected[i] = singleEscProj

		metaOnly := singleEscProj
		metaOnly.BodyExcerpt = ""
		rawMetaOnly, mErr := json.Marshal(metaOnly)
		Expect(mErr).NotTo(HaveOccurred())
		Expect(encodedJSONFragmentBytes(rawMetaOnly)).To(BeNumerically("<", 1400), "non-body metadata must leave a feasible body budget under 64-card water-filling")
	}
	initEsc64Prompt, err := marshalNominationEnvelope(initEsc64Projected)
	Expect(err).NotTo(HaveOccurred())
	Expect(len(initEsc64Prompt) + NominationFixedFrameReserve).To(BeNumerically(">", MaxNominationFrameBytes))
	Expect(encodedJSONTextBytes(initEsc64Prompt) + NominationFixedFrameReserve).To(BeNumerically(">", MaxNominationFrameBytes))

	storedEsc64, projEsc64, promptEsc64, digestEsc64, err := ProjectNominationBatchCards(rawEscape64)
	Expect(err).NotTo(HaveOccurred())
	Expect(storedEsc64).To(HaveLen(64))
	Expect(projEsc64).To(HaveLen(64))
	Expect(len(promptEsc64) + NominationFixedFrameReserve).To(BeNumerically("<=", MaxNominationFrameBytes))
	Expect(encodedJSONTextBytes([]byte(promptEsc64)) + NominationFixedFrameReserve).To(BeNumerically("<=", MaxNominationFrameBytes))
	expectedDigestEsc64, err := ComputeCardPayloadDigest(projEsc64)
	Expect(err).NotTo(HaveOccurred())
	Expect(digestEsc64).To(Equal(expectedDigestEsc64))
	for i := 0; i < 64; i++ {
		Expect(storedEsc64[i].UnitID).To(Equal(fmt.Sprintf("u%02d", i+1)))
		Expect(storedEsc64[i].ItemID).To(Equal(fmt.Sprintf("item-%06d", i+1)))
		Expect(storedEsc64[i].DocumentID).To(Equal(rawCard.DocumentID))
		Expect(storedEsc64[i].PagePath).To(Equal(fmt.Sprintf("private/page-%02d.html", i+1)))
		Expect(storedEsc64[i].RegionID).To(Equal(fmt.Sprintf("region-%02d", i+1)))
		Expect(storedEsc64[i].TotalBodyBytes).To(Equal(len(escapeHeavyBody)))
		Expect(storedEsc64[i].ExcerptCoverage).To(Equal("truncated"))
		Expect(toProjectedNominationCard(storedEsc64[i])).To(Equal(projEsc64[i]))
		Expect(utf8.ValidString(projEsc64[i].BodyExcerpt)).To(BeTrue())
		Expect(strings.Count(projEsc64[i].BodyExcerpt, "\n...\n")).To(Equal(1), "iterative shrink must slice unspliced original excerpt without nested separators")
		Expect(projEsc64[i].BodyExcerpt).To(HavePrefix("START-가나다"))
		Expect(projEsc64[i].BodyExcerpt).To(HaveSuffix("END-마바사"))
	}
	prompt64 := promptEsc64
	digest64 := digestEsc64

	// Mixed 64-card pressure (24 large ~6.7KiB cards + 40 small ~420B cards whose initial canonical prompt genuinely exceeds 136KiB):
	// each individual card fits within <=8192B raw and encoded before batch projection, while the 64-card batch exceeds the 144KiB (8KiB reserve) frame budget;
	// water-filling shrinks only the 24 large cards while all 40 small cards remain ExcerptCoverage=="full".
	mixed64 := make([]domain.MaintenanceNominationCard, 64)
	initMixedProjected := make([]domain.MaintenanceProjectedCard, 64)
	largeMixedBody := "LARGE-HEAD-앵커 " + strings.Repeat("distributed-consensus-lease-barrier-protocol ", 140) + " LARGE-TAIL-앵커"
	smallMixedBody := "Small card body that fits completely without truncation when large cards absorb water-filling."
	for i := 0; i < 64; i++ {
		body := smallMixedBody
		if i < 24 {
			body = largeMixedBody
		}
		mixed64[i] = domain.MaintenanceNominationCard{
			UnitID:          fmt.Sprintf("u%02d", i+1),
			ItemID:          fmt.Sprintf("item-%06d", i+1),
			UnitKind:        "region",
			ProjectID:       rawCard.ProjectID,
			DocumentID:      rawCard.DocumentID,
			VersionID:       rawCard.VersionID,
			PagePath:        fmt.Sprintf("page-%02d.html", i+1),
			RegionID:        fmt.Sprintf("reg-%02d", i+1),
			TargetSetID:     rawCard.TargetSetID,
			ObjectHash:      rawCard.ObjectHash,
			ByteSize:        8192,
			Title:           fmt.Sprintf("Mixed Card %02d", i+1),
			BodyExcerpt:     body,
			ExcerptCoverage: "full",
			TotalBodyBytes:  len(body),
		}
		singleProj, _, pErr := ProjectNominationCard(mixed64[i])
		Expect(pErr).NotTo(HaveOccurred())
		Expect(singleProj.ExcerptCoverage).To(Equal("full"), "each individual card in mixed64 must fit <=8192B raw and encoded before batch water-filling")
		rawSingle, mErr := json.Marshal(singleProj)
		Expect(mErr).NotTo(HaveOccurred())
		Expect(len(rawSingle)).To(BeNumerically("<=", MaxNominationCardJSONBytes))
		Expect(encodedJSONFragmentBytes(rawSingle)).To(BeNumerically("<=", MaxNominationCardJSONBytes))
		initMixedProjected[i] = singleProj
	}
	initMixedPrompt, err := marshalNominationEnvelope(initMixedProjected)
	Expect(err).NotTo(HaveOccurred())
	Expect(len(initMixedPrompt) + NominationFixedFrameReserve).To(BeNumerically(">", MaxNominationFrameBytes))
	Expect(encodedJSONTextBytes(initMixedPrompt) + NominationFixedFrameReserve).To(BeNumerically(">", MaxNominationFrameBytes))

	_, projMixed64, promptMixed64, _, err := ProjectNominationBatchCards(mixed64)
	Expect(err).NotTo(HaveOccurred())
	Expect(len(promptMixed64) + NominationFixedFrameReserve).To(BeNumerically("<=", MaxNominationFrameBytes))
	Expect(encodedJSONTextBytes([]byte(promptMixed64)) + NominationFixedFrameReserve).To(BeNumerically("<=", MaxNominationFrameBytes))
	for i := 0; i < 24; i++ {
		Expect(projMixed64[i].ExcerptCoverage).To(Equal("truncated"))
		Expect(projMixed64[i].TotalBodyBytes).To(Equal(len(largeMixedBody)))
		Expect(strings.Count(projMixed64[i].BodyExcerpt, "\n...\n")).To(Equal(1))
		Expect(projMixed64[i].BodyExcerpt).To(HavePrefix("LARGE-HEAD-앵커"))
		Expect(projMixed64[i].BodyExcerpt).To(HaveSuffix("LARGE-TAIL-앵커"))
	}
	for i := 24; i < 64; i++ {
		Expect(projMixed64[i].ExcerptCoverage).To(Equal("full"))
		Expect(projMixed64[i].BodyExcerpt).To(Equal(smallMixedBody))
		Expect(projMixed64[i].TotalBodyBytes).To(Equal(len(smallMixedBody)))
	}

	// 1d. BuildNominationBatchPrompt rejects: (i) a card with raw JSON >8192B, (ii) a card with raw <=8192B but encoded fragment >8192B, and (iii) 64 cards that each pass <=8192B raw+encoded and whole-prompt raw <=136KiB but overflow 144KiB when JSON-string re-encoded.
	rawOverCard := domain.MaintenanceProjectedCard{
		UnitID:          "u01",
		UnitKind:        "region",
		ProjectID:       rawCard.ProjectID,
		VersionID:       rawCard.VersionID,
		Title:           "raw-over-8192",
		BodyExcerpt:     strings.Repeat("x", 8200),
		ExcerptCoverage: "truncated",
		TotalBodyBytes:  9000,
	}
	rawOverBytes, mErr := json.Marshal(rawOverCard)
	Expect(mErr).NotTo(HaveOccurred())
	Expect(len(rawOverBytes)).To(BeNumerically(">", MaxNominationCardJSONBytes))
	_, _, err = BuildNominationBatchPrompt([]domain.MaintenanceProjectedCard{rawOverCard})
	Expect(err).To(MatchError(ErrMaintenanceInvalid))

	encOverCard := domain.MaintenanceProjectedCard{
		UnitID:          "u01",
		UnitKind:        "region",
		ProjectID:       rawCard.ProjectID,
		VersionID:       rawCard.VersionID,
		Title:           "encoded-over-8192",
		BodyExcerpt:     strings.Repeat(`\"`, 1500), // raw card ~4700B (<=8192B), encoded fragment >9000B (>8192B)
		ExcerptCoverage: "truncated",
		TotalBodyBytes:  6000,
	}
	rawEncOver, mErr := json.Marshal(encOverCard)
	Expect(mErr).NotTo(HaveOccurred())
	Expect(len(rawEncOver)).To(BeNumerically("<=", MaxNominationCardJSONBytes))
	Expect(encodedJSONFragmentBytes(rawEncOver)).To(BeNumerically(">", MaxNominationCardJSONBytes))
	_, _, err = BuildNominationBatchPrompt([]domain.MaintenanceProjectedCard{encOverCard})
	Expect(err).To(MatchError(ErrMaintenanceInvalid))

	unshrunkEsc64 := make([]domain.MaintenanceProjectedCard, 64)
	for i := 0; i < 64; i++ {
		unshrunkEsc64[i] = domain.MaintenanceProjectedCard{
			UnitID:          fmt.Sprintf("u%02d", i+1),
			UnitKind:        "region",
			ProjectID:       rawCard.ProjectID,
			VersionID:       rawCard.VersionID,
			Title:           "escape-overflow",
			BodyExcerpt:     strings.Repeat(`\"`, 400), // raw card ~1820B (<=8192B) and encoded fragment ~3420B (<=8192B), whole raw <=136KiB, but encoded prompt + 8KiB reserve > 144KiB
			ExcerptCoverage: "truncated",
			TotalBodyBytes:  4000,
		}
		rawC, mErr := json.Marshal(unshrunkEsc64[i])
		Expect(mErr).NotTo(HaveOccurred())
		Expect(len(rawC)).To(BeNumerically("<=", MaxNominationCardJSONBytes))
		Expect(encodedJSONFragmentBytes(rawC)).To(BeNumerically("<=", MaxNominationCardJSONBytes))
	}
	initUnshrunkPrompt, mErr := marshalNominationEnvelope(unshrunkEsc64)
	Expect(mErr).NotTo(HaveOccurred())
	Expect(len(initUnshrunkPrompt) + NominationFixedFrameReserve).To(BeNumerically("<=", MaxNominationFrameBytes))
	Expect(encodedJSONTextBytes(initUnshrunkPrompt) + NominationFixedFrameReserve).To(BeNumerically(">", MaxNominationFrameBytes))
	_, _, err = BuildNominationBatchPrompt(unshrunkEsc64)
	Expect(err).To(MatchError(ErrMaintenanceInvalid))

	// 2. PlanNominationBatches: <= 64 produces 1 batch (nomination:p0:b0000); > 64 produces <= 2*ceil(N/64) 2-pass cross-project interleaved batches.
	var items12 []domain.JobItem
	for i := 1; i <= 12; i++ {
		items12 = append(items12, domain.JobItem{
			ItemID:    fmt.Sprintf("item-%06d", i),
			Ordinal:   i,
			UnitKind:  "region",
			ProjectID: fmt.Sprintf("proj-%d", i%2),
			VersionID: fmt.Sprintf("ver-%d", i%3),
			PagePath:  "arch.html",
			RegionID:  fmt.Sprintf("reg-%02d", i),
		})
	}
	batches12 := PlanNominationBatches(items12)
	Expect(batches12).To(HaveLen(1))
	Expect(batches12[0].BatchID).To(Equal("nomination:p0:b0000"))
	Expect(batches12[0].Items).To(HaveLen(12))

	var items150 []domain.JobItem
	for i := 1; i <= 150; i++ {
		items150 = append(items150, domain.JobItem{
			ItemID:    fmt.Sprintf("item-%06d", i),
			Ordinal:   i,
			UnitKind:  "region",
			ProjectID: fmt.Sprintf("proj-%d", i%3),
			VersionID: fmt.Sprintf("ver-%d", i%6),
			PagePath:  "arch.html",
			RegionID:  fmt.Sprintf("reg-%03d", i),
		})
	}
	batches150 := PlanNominationBatches(items150)
	// ceil(150/64) = 3 batches per pass -> 6 batches total (<= 2*ceil(150/64)).
	Expect(batches150).To(HaveLen(6))
	Expect(batches150[0].BatchID).To(Equal("nomination:p0:b0000"))
	Expect(batches150[1].BatchID).To(Equal("nomination:p0:b0001"))
	Expect(batches150[2].BatchID).To(Equal("nomination:p0:b0002"))
	Expect(batches150[3].BatchID).To(Equal("nomination:p1:b0000"))
	Expect(batches150[4].BatchID).To(Equal("nomination:p1:b0001"))
	Expect(batches150[5].BatchID).To(Equal("nomination:p1:b0002"))
	for _, b := range batches150 {
		Expect(len(b.Items)).To(BeNumerically(">=", 2))
		Expect(len(b.Items)).To(BeNumerically("<=", 64))
	}

	// 3. Strict closed-schema nomination decoding (including CO interim observation 1: required abstained boolean presence and abstention semantics).
	testIntent := domain.MaintenanceIntent{
		Mode:    domain.MaintenanceModeSemanticNomination,
		BatchID: "nomination:p0:b0000",
		Cards: []domain.MaintenanceNominationCard{
			{UnitID: "u01", ItemID: "item-000001"},
			{UnitID: "u02", ItemID: "item-000002"},
			{UnitID: "u03", ItemID: "item-000003"},
		},
	}

	validOut := `{
		"nominations": [
			{
				"focal_unit_id": "u01",
				"abstained": false,
				"candidates": [
					{"candidate_unit_id": "u02", "rationale": "Shares bounded retry coordination mechanism across distinct vocabulary."}
				]
			},
			{
				"focal_unit_id": "u02",
				"abstained": true,
				"abstain_reason": "No non-trivial counterpart in this batch.",
				"candidates": []
			},
			{
				"focal_unit_id": "u03",
				"abstained": false,
				"abstain_reason": "",
				"candidates": [
					{"candidate_unit_id": "u01", "rationale": "Complements retry fencing with epoch barriers."}
				]
			}
		]
	}`
	env, resolved, err := decodeMaintenanceNominationOutput([]byte(validOut), testIntent.Cards)
	Expect(err).NotTo(HaveOccurred())
	Expect(env.Nominations).To(HaveLen(3))
	Expect(resolved["item-000001"].Abstained).To(BeFalse())
	Expect(resolved["item-000001"].Candidates).To(Equal([]domain.MaintenanceNominatedCandidate{
		{CandidateUnitID: "item-000002", Rationale: "Shares bounded retry coordination mechanism across distinct vocabulary."},
	}))
	Expect(resolved["item-000002"].Abstained).To(BeTrue())
	Expect(resolved["item-000002"].AbstainReason).To(Equal("No non-trivial counterpart in this batch."))
	Expect(resolved["item-000002"].Candidates).To(BeEmpty())
	Expect(resolved["item-000003"].Abstained).To(BeFalse())
	Expect(resolved["item-000003"].AbstainReason).To(BeEmpty())
	Expect(resolved["item-000003"].Candidates).To(Equal([]domain.MaintenanceNominatedCandidate{
		{CandidateUnitID: "item-000001", Rationale: "Complements retry fencing with epoch barriers."},
	}))
	Expect(formatUnverifiedNominationHint("Shares bounded retry coordination mechanism across distinct vocabulary.")).To(Equal(
		"unverified_card_routing_hint: Shares bounded retry coordination mechanism across distinct vocabulary.",
	))
	Expect(formatUnverifiedNominationHint("unverified_card_routing_hint: Already labeled.")).To(Equal(
		"unverified_card_routing_hint: Already labeled.",
	))

	const (
		validRow1 = `{"focal_unit_id":"u01","abstained":false,"candidates":[{"candidate_unit_id":"u02","rationale":"valid rationale"}]}`
		validRow2 = `{"focal_unit_id":"u02","abstained":true,"abstain_reason":"No non-trivial counterpart in this batch.","candidates":[]}`
		validRow3 = `{"focal_unit_id":"u03","abstained":false,"candidates":[{"candidate_unit_id":"u01","rationale":"Complements retry fencing."}]}`
	)
	for _, invalidCase := range []struct {
		name string
		raw  string
	}{
		{
			name: "missing focal unit row (only 2 of 3 supplied cards)",
			raw:  `{"nominations":[` + validRow1 + `,` + validRow2 + `]}`,
		},
		{
			name: "missing required abstained boolean",
			raw:  `{"nominations":[{"focal_unit_id":"u01","candidates":[{"candidate_unit_id":"u02","rationale":"valid rationale"}]},` + validRow2 + `,` + validRow3 + `]}`,
		},
		{
			name: "abstained true with missing abstain_reason",
			raw:  `{"nominations":[{"focal_unit_id":"u01","abstained":true,"candidates":[]},` + validRow2 + `,` + validRow3 + `]}`,
		},
		{
			name: "abstained true with blank abstain_reason",
			raw:  `{"nominations":[{"focal_unit_id":"u01","abstained":true,"abstain_reason":"   ","candidates":[]},` + validRow2 + `,` + validRow3 + `]}`,
		},
		{
			name: "abstained true with non-empty candidates",
			raw:  `{"nominations":[{"focal_unit_id":"u01","abstained":true,"abstain_reason":"Non-blank reason.","candidates":[{"candidate_unit_id":"u02","rationale":"contradicts abstained"}]},` + validRow2 + `,` + validRow3 + `]}`,
		},
		{
			name: "abstained false with empty candidates",
			raw:  `{"nominations":[{"focal_unit_id":"u01","abstained":false,"candidates":[]},` + validRow2 + `,` + validRow3 + `]}`,
		},
		{
			name: "abstained false with non-empty abstain_reason",
			raw:  `{"nominations":[{"focal_unit_id":"u01","abstained":false,"abstain_reason":"should be omitted","candidates":[{"candidate_unit_id":"u02","rationale":"ok"}]},` + validRow2 + `,` + validRow3 + `]}`,
		},
		{
			name: "out-of-batch candidate_unit_id",
			raw:  `{"nominations":[{"focal_unit_id":"u01","abstained":false,"candidates":[{"candidate_unit_id":"u99","rationale":"out of batch"}]},` + validRow2 + `,` + validRow3 + `]}`,
		},
		{
			name: "unknown focal_unit_id",
			raw:  `{"nominations":[{"focal_unit_id":"u09","abstained":true,"abstain_reason":"unknown focal","candidates":[]},` + validRow2 + `,` + validRow3 + `]}`,
		},
		{
			name: "self nomination",
			raw:  `{"nominations":[{"focal_unit_id":"u01","abstained":false,"candidates":[{"candidate_unit_id":"u01","rationale":"self"}]},` + validRow2 + `,` + validRow3 + `]}`,
		},
		{
			name: "duplicate focal_unit_id",
			raw:  `{"nominations":[{"focal_unit_id":"u01","abstained":true,"abstain_reason":"first","candidates":[]},{"focal_unit_id":"u01","abstained":true,"abstain_reason":"dup","candidates":[]},` + validRow3 + `]}`,
		},
		{
			name: "duplicate candidate_unit_id for same focal",
			raw:  `{"nominations":[{"focal_unit_id":"u01","abstained":false,"candidates":[{"candidate_unit_id":"u02","rationale":"first"},{"candidate_unit_id":"u02","rationale":"dup"}]},` + validRow2 + `,` + validRow3 + `]}`,
		},
		{
			name: "empty rationale",
			raw:  `{"nominations":[{"focal_unit_id":"u01","abstained":false,"candidates":[{"candidate_unit_id":"u02","rationale":"   "}]},` + validRow2 + `,` + validRow3 + `]}`,
		},
		{
			name: "unknown field in envelope",
			raw:  `{"nominations":[` + validRow1 + `,` + validRow2 + `,` + validRow3 + `],"extra_field":true}`,
		},
	} {
		_, _, decErr := decodeMaintenanceNominationOutput([]byte(invalidCase.raw), testIntent.Cards)
		Expect(decErr).To(MatchError(ErrMaintenanceInvalid), "expected %s to fail with ErrMaintenanceInvalid", invalidCase.name)
	}

	// Isolated K<=6 check with 8 known unique cards and exact 8 focal results: 6 distinct candidates on u01 is accepted, 7 distinct candidates on u01 is rejected.
	cards8 := []domain.MaintenanceNominationCard{
		{UnitID: "u01", ItemID: "item-000001"},
		{UnitID: "u02", ItemID: "item-000002"},
		{UnitID: "u03", ItemID: "item-000003"},
		{UnitID: "u04", ItemID: "item-000004"},
		{UnitID: "u05", ItemID: "item-000005"},
		{UnitID: "u06", ItemID: "item-000006"},
		{UnitID: "u07", ItemID: "item-000007"},
		{UnitID: "u08", ItemID: "item-000008"},
	}
	const abstainedRows2To8 = `{"focal_unit_id":"u02","abstained":true,"abstain_reason":"No counterpart for u02.","candidates":[]},` +
		`{"focal_unit_id":"u03","abstained":true,"abstain_reason":"No counterpart for u03.","candidates":[]},` +
		`{"focal_unit_id":"u04","abstained":true,"abstain_reason":"No counterpart for u04.","candidates":[]},` +
		`{"focal_unit_id":"u05","abstained":true,"abstain_reason":"No counterpart for u05.","candidates":[]},` +
		`{"focal_unit_id":"u06","abstained":true,"abstain_reason":"No counterpart for u06.","candidates":[]},` +
		`{"focal_unit_id":"u07","abstained":true,"abstain_reason":"No counterpart for u07.","candidates":[]},` +
		`{"focal_unit_id":"u08","abstained":true,"abstain_reason":"No counterpart for u08.","candidates":[]}`
	valid6Cands := `{"nominations":[{"focal_unit_id":"u01","abstained":false,"candidates":[` +
		`{"candidate_unit_id":"u02","rationale":"distinct 1"},` +
		`{"candidate_unit_id":"u03","rationale":"distinct 2"},` +
		`{"candidate_unit_id":"u04","rationale":"distinct 3"},` +
		`{"candidate_unit_id":"u05","rationale":"distinct 4"},` +
		`{"candidate_unit_id":"u06","rationale":"distinct 5"},` +
		`{"candidate_unit_id":"u07","rationale":"distinct 6"}` +
		`]},` + abstainedRows2To8 + `]}`
	env6, resolved6, err6 := decodeMaintenanceNominationOutput([]byte(valid6Cands), cards8)
	Expect(err6).NotTo(HaveOccurred())
	Expect(env6.Nominations).To(HaveLen(8))
	Expect(resolved6["item-000001"].Candidates).To(HaveLen(6))

	invalid7Cands := `{"nominations":[{"focal_unit_id":"u01","abstained":false,"candidates":[` +
		`{"candidate_unit_id":"u02","rationale":"distinct 1"},` +
		`{"candidate_unit_id":"u03","rationale":"distinct 2"},` +
		`{"candidate_unit_id":"u04","rationale":"distinct 3"},` +
		`{"candidate_unit_id":"u05","rationale":"distinct 4"},` +
		`{"candidate_unit_id":"u06","rationale":"distinct 5"},` +
		`{"candidate_unit_id":"u07","rationale":"distinct 6"},` +
		`{"candidate_unit_id":"u08","rationale":"distinct 7"}` +
		`]},` + abstainedRows2To8 + `]}`
	_, _, err7 := decodeMaintenanceNominationOutput([]byte(invalid7Cands), cards8)
	Expect(err7).To(MatchError(ErrMaintenanceInvalid))

	// 4. SemanticRequestSHA normalizes volatile batch/scan IDs while binding Mode and CardPayloadDigest.
	invA := domain.ProviderInvocation{
		Prompt: prompt64,
		Maintenance: &domain.MaintenanceInvocation{
			Mode:              domain.MaintenanceModeSemanticNomination,
			ScanJobID:         "scan-job-alpha",
			ItemID:            "nomination:p0:b0000",
			BatchID:           "nomination:p0:b0000",
			BatchItemIDs:      []string{"item-000001", "item-000002"},
			CardPayloadDigest: digest64,
			UnitKind:          "region",
			ProjectID:         rawCard.ProjectID,
			DocumentID:        rawCard.DocumentID,
			VersionID:         rawCard.VersionID,
			SourceMode:        "pinned",
			Explanations: []domain.MaintenanceExplanation{
				{Role: "card", UnitID: "u01", UnitKind: "region", ItemID: "item-000001", ProjectID: rawCard.ProjectID, DocumentID: rawCard.DocumentID, VersionID: rawCard.VersionID, ObjectHash: rawCard.ObjectHash, Size: rawCard.ByteSize},
			},
		},
	}
	invB := invA
	maintB := *invA.Maintenance
	maintB.ScanJobID = "scan-job-beta"
	maintB.ItemID = "nomination:p1:b0002"
	maintB.BatchID = "nomination:p1:b0002"
	maintB.BatchItemIDs = []string{"item-000099", "item-000100"}
	maintB.Explanations = []domain.MaintenanceExplanation{
		{Role: "card", UnitID: "u01", UnitKind: "region", ItemID: "item-000099", ProjectID: rawCard.ProjectID, DocumentID: rawCard.DocumentID, VersionID: rawCard.VersionID, ObjectHash: rawCard.ObjectHash, Size: rawCard.ByteSize},
	}
	invB.Maintenance = &maintB

	shaA, err := codex.SemanticRequestSHA("owner-1", []byte(`{"action":"maintenance_nomination","scan_job_id":"scan-job-alpha","item_id":"nomination:p0:b0000","batch_id":"nomination:p0:b0000"}`), invA, nil)
	Expect(err).NotTo(HaveOccurred())
	shaB, err := codex.SemanticRequestSHA("owner-1", []byte(`{"action":"maintenance_nomination","scan_job_id":"scan-job-beta","item_id":"nomination:p1:b0002","batch_id":"nomination:p1:b0002"}`), invB, nil)
	Expect(err).NotTo(HaveOccurred())
	Expect(shaA).To(Equal(shaB))

	maintChangedDigest := maintB
	maintChangedDigest.CardPayloadDigest = "sha256:" + strings.Repeat("f", 64)
	invChanged := invB
	invChanged.Maintenance = &maintChangedDigest
	shaChanged, err := codex.SemanticRequestSHA("owner-1", []byte(`{"action":"maintenance_nomination","scan_job_id":"scan-job-beta","item_id":"nomination:p1:b0002","batch_id":"nomination:p1:b0002"}`), invChanged, nil)
	Expect(err).NotTo(HaveOccurred())
	Expect(shaChanged).NotTo(Equal(shaA))
})
