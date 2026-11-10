// TASK-003-04 presentation derivation regressions: fixed required-unit
// denominators, registry status precedence (including overlay unable_to_verify
// over an all-assessed list and unavailable-target alignment), evidenced vs
// explicit-false discrepancies, units_url validation as data, typed
// page-local region tuples and selection/request-generation guards.
import test from "node:test";
import assert from "node:assert/strict";
import { renderToStaticMarkup } from "react-dom/server";
import type { ApplicableFreshness, AssessmentRecord, CoverageUnit } from "../../client/types.ts";
import {
  acceptRegionFocusResult, acceptRegionSelection, compareWithOverlay, coverageUnitsParams, deriveCoverageSummary, deriveStatus,
  enumerationComplete, initialEnumeration, mergeEnumerationPage, parseSuppliedMetadata,
  regionRequestStale, regionSelectionMatches, SelectionQueryGuard, toggledOpen, verifyMetadataBytes, assessmentDetailUnits, renderAssessmentText,
} from "./evidence-view.ts";

const storedAssessment = (): AssessmentRecord => ({
  assessment_id: "assessment", version_id: "version", target_set_id: "target", coverage_set_id: "contribution-set",
  scope: { kind: "page", page_path: "a.html", region_id: null }, assessed_at: "2026-11-01T00:00:01Z",
  evidence: { units: [{ kind: "page", page_path: "a.html", region_id: null, outcome: "partial", discrepancy: "caller changed <img src=x onerror=alert(1)>", reason: "another claim unexamined", rationale: "Full public rationale </dd><script>alert(1)</script>", citations: [{ basis: "observed_input", read_id: "read", verification_id: null, input_origin: "daemon_supplied", target_set_id: "target", bytes_sha256: "a".repeat(64), source: { kind: "git", repo_id: "repo", object_format: "sha1", exact_revision: "b".repeat(40) }, location: { path: "caller.go", line_start: 2, line_end: 3 } }] }] },
});

test("stored semantic details preserve full text/citation provenance and reject foreign tuple/target or legacy shapes", () => {
  const item = storedAssessment();
  const details = assessmentDetailUnits(item, "version");
  assert.equal(details?.[0].discrepancy, "caller changed <img src=x onerror=alert(1)>");
  assert.equal(details?.[0].rationale, "Full public rationale </dd><script>alert(1)</script>");
  assert.deepEqual(details?.[0].citations[0].location, { path: "caller.go", line_start: 2, line_end: 3 });
  assert.equal(details?.[0].citations[0].verification_id, null);
  assert.equal(assessmentDetailUnits(item, "different-version"), null);
  assert.equal(assessmentDetailUnits({ ...item, scope: { kind: "region", page_path: "a.html", region_id: "same" } }, "version"), null);
  assert.equal(assessmentDetailUnits({ ...item, scope: { kind: "page", page_path: "b.html", region_id: null } }, "version"), null);
  assert.equal(assessmentDetailUnits({ ...item, target_set_id: "other-target" }, "version"), null);
  assert.equal(assessmentDetailUnits({ ...item, evidence: {} }, "version"), null);
  assert.equal(assessmentDetailUnits({ ...item, evidence: { units: [{ kind: { toString: null } }] } }, "version"), null);
  const posthoc = storedAssessment();
  const raw = (posthoc.evidence as { units: Array<{ citations: Array<Record<string, unknown>> }> }).units[0].citations[0];
  Object.assign(raw, { basis: "posthoc_verified", read_id: null, input_origin: null, verification_id: "verification" });
  assert.equal(assessmentDetailUnits(posthoc, "version")?.[0].citations[0].basis, "posthoc_verified");
  raw.input_origin = "daemon_supplied";
  assert.equal(assessmentDetailUnits(posthoc, "version"), null);
});

test("the semantic detail renderer escapes actual hostile strings and keeps null, missing, zero and JSON distinct", () => {
  const details = assessmentDetailUnits(storedAssessment(), "version")!;
  const html = renderToStaticMarkup(renderAssessmentText(details[0].rationale));
  assert.ok(html.includes("&lt;script&gt;alert(1)&lt;/script&gt;"));
  assert.ok(!html.includes("<script>"));
  const source = renderToStaticMarkup(renderAssessmentText({ path: "<img src=x onerror=alert(1)>" }));
  assert.ok(!source.includes("<img"));
  assert.ok(source.includes("&lt;img"));
  assert.ok(renderToStaticMarkup(renderAssessmentText(null)).includes('data-value-kind="null"'));
  assert.ok(renderToStaticMarkup(renderAssessmentText(undefined)).includes('data-value-kind="missing"'));
  assert.ok(renderToStaticMarkup(renderAssessmentText(0)).endsWith(">0</span>"));
});

const unit = (over: Partial<CoverageUnit>): CoverageUnit => ({
  kind: "version", page_path: null, region_id: null, outcome: "assessed", reason: null,
  discrepancy: null, assessment_id: "a1", target_set_id: "t1", ...over,
});

const fresh = (over: Partial<ApplicableFreshness>): ApplicableFreshness => ({
  source_intent: "historical", target_set_id: "t1", captured_at: "2026-10-19T00:00:00Z",
  scope: { kind: "version", page_path: null, region_id: null }, status: "aligned_within_scope",
  coverage: { coverage_set_id: "c1", unit_kind: "version", eligible: 1, assessed: 1, complete: true, units_url: "/api/v1/versions/v1/assessments/coverage?coverage_set_id=c1" },
  assessed_at: "2026-10-19T00:00:00Z", reassessment_required: false, ...over,
});

const bytes = (text: string): Uint8Array => new TextEncoder().encode(text);
const digestOf = async (text: string): Promise<string> => {
  const raw = await crypto.subtle.digest("SHA-256", bytes(text) as BufferSource);
  return [...new Uint8Array(raw)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
};

test("parseSuppliedMetadata reads the author's source intent from the version object", () => {
  const parsed = parseSuppliedMetadata({
    version: { source_intent: "historical" },
    sources: [{ id: "s1", kind: "git", retention_mode: "reference_only", repo_id: "r", object_format: "sha1", exact_revision: "0".repeat(40) }],
    evidence: [{ id: "e1", source_id: "s1", capture_status: "reference_only", path: "hello.html" }],
    regions: [{ id: "overview", page_path: "hello.html", evidence_ids: ["e1"] }, { id: "overview", page_path: "other.html", evidence_ids: [] }],
  });
  assert.ok(parsed !== null);
  assert.equal(parsed.sourceIntent, "historical");
  assert.equal(parsed.sources.length, 1);
  // Same region name on different pages stays two distinct page-local records.
  assert.equal(parsed.regions.length, 2);
  assert.deepEqual(parsed.regions.map((region) => region.page_path), ["hello.html", "other.html"]);
});

test("parseSuppliedMetadata rejects malformed optional metadata", () => {
  assert.equal(parseSuppliedMetadata({ regions: [{ id: "r" }] }), null);
  assert.equal(parseSuppliedMetadata({ evidence: "nope" }), null);
  assert.equal(parseSuppliedMetadata(null), null);
  // A flattened root source_intent is not the stored shape; intent stays null.
  assert.equal(parseSuppliedMetadata({ source_intent: "historical" })?.sourceIntent, null);
});

test("verifyMetadataBytes compares every supplied validator and rejects missing validators", async () => {
  const text = JSON.stringify({ version: { source_intent: "historical" } });
  const digest = await digestOf(text);
  assert.deepEqual(await verifyMetadataBytes(bytes(text), { etag: `"sha256-${digest}"`, expectedSha256: null }), { ok: true, digest, problems: [] });
  assert.deepEqual(await verifyMetadataBytes(bytes(text), { etag: `"sha256-${digest}"`, expectedSha256: digest }), { ok: true, digest, problems: [] });
  const bad = await verifyMetadataBytes(bytes(text + " "), { etag: `"sha256-${digest}"`, expectedSha256: null });
  assert.equal(bad.ok, false);
  assert.deepEqual(bad.problems, ["etag-mismatch"]);
  const crossed = await verifyMetadataBytes(bytes(text), { etag: `"sha256-${digest}"`, expectedSha256: "0".repeat(64) });
  assert.deepEqual(crossed.problems, ["supplied-sha-mismatch"]);
  const none = await verifyMetadataBytes(bytes(text), { etag: null, expectedSha256: null });
  assert.equal(none.ok, false);
  assert.deepEqual(none.problems, ["no-usable-validator"]);
});

test("coverage counts keep the fixed denominator with partial/blocked/not-checked units", () => {
  const summary = deriveCoverageSummary([
    unit({ kind: "page", page_path: "a.html" }),
    unit({ kind: "page", page_path: "b.html", outcome: "partial", reason: "일부만 확인" }),
    unit({ kind: "page", page_path: "c.html", outcome: "blocked", reason: "읽기 거부" }),
    unit({ kind: "page", page_path: "d.html", outcome: "not_checked", reason: "제외" }),
  ], { targetSetId: "t1", scope: { kind: "version" } });
  assert.equal(summary.eligible, 4);
  assert.equal(summary.assessed, 1);
  assert.equal(summary.partial, 1);
  assert.equal(summary.blocked, 1);
  assert.equal(summary.notChecked, 1);
  assert.equal(summary.aggregationProblem, null);
  assert.equal(summary.aligned, false);
  assert.equal(summary.derivedComplete, false);
});

test("contributions from another target or outside the selected scope never count as assessed", () => {
  const foreign = deriveCoverageSummary([unit({ kind: "page", page_path: "a.html" }), unit({ kind: "page", page_path: "b.html", target_set_id: "t2", outcome: "blocked", discrepancy: true })], { targetSetId: "t1", scope: { kind: "version" } });
  assert.equal(foreign.foreignTarget, 1);
  assert.equal(foreign.assessed, 1);
  assert.equal(foreign.blocked, 0, "a foreign-target row cannot drive this target's status");
  assert.equal(foreign.discrepancyUnits, 0, "a foreign-target discrepancy cannot relabel the selected target");
  assert.equal(foreign.aligned, false);
  const scoped = deriveCoverageSummary([unit({ kind: "page", page_path: "a.html" }), unit({ kind: "page", page_path: "z.html", discrepancy: true })], { targetSetId: "t1", scope: { kind: "page", pagePath: "a.html" } });
  assert.equal(scoped.outOfScope, 1);
  assert.equal(scoped.discrepancyUnits, 0, "an out-of-scope discrepancy cannot relabel the selected scope");
  assert.equal(scoped.aligned, false);
});

test("a page scope's fixed set may be that page's distinct region units", () => {
  const regionUnits = ["overview", "r2", "r3", "r4"].map((regionId, index) => unit({
    kind: "region", page_path: "first.html", region_id: regionId,
    outcome: index === 0 ? "assessed" : index === 1 ? "assessed" : index === 2 ? "partial" : "blocked",
    discrepancy: index === 1 ? true : null,
  }));
  const summary = deriveCoverageSummary(regionUnits, { targetSetId: "t1", scope: { kind: "page", pagePath: "first.html" } });
  assert.equal(summary.eligible, 4);
  assert.equal(summary.assessed, 2);
  assert.equal(summary.partial, 1);
  assert.equal(summary.blocked, 1);
  assert.equal(summary.outOfScope, 0);
  assert.equal(summary.aggregationProblem, null);
  assert.equal(summary.aligned, false);
  // Another page's region units stay out of this page's fixed set.
  const foreign = deriveCoverageSummary([...regionUnits, unit({ kind: "region", page_path: "other.html", region_id: "overview" })], { targetSetId: "t1", scope: { kind: "page", pagePath: "first.html" } });
  assert.equal(foreign.outOfScope, 1);
  assert.equal(foreign.eligible, 5);
});

test("duplicate tuples across cursor pages stay an aggregation error, never a deduplicated denominator", () => {
  const duplicate = deriveCoverageSummary([
    unit({ kind: "region", page_path: "first.html", region_id: "overview" }),
    unit({ kind: "region", page_path: "first.html", region_id: "overview" }),
  ], { targetSetId: "t1", scope: { kind: "page", pagePath: "first.html" } });
  assert.equal(duplicate.invalidUnits, 1);
  assert.equal(duplicate.aggregationProblem, "duplicate-unit-tuple");
  assert.equal(duplicate.derivedComplete, false);
  assert.equal(duplicate.aligned, false);
});

test("impossible null/path combinations are invalid units", () => {
  const invalid = deriveCoverageSummary([
    unit({ kind: "region", page_path: "first.html", region_id: null }),
    unit({ kind: "page", page_path: null, region_id: null }),
    unit({ kind: "version", page_path: "first.html", region_id: null }),
  ]);
  assert.equal(invalid.invalidUnits, 3);
  assert.equal(invalid.aggregationProblem, "invalid-unit-shape");
  assert.equal(invalid.aligned, false);
});

test("mixed unit kinds and page/region overlap are aggregation errors", () => {
  const mixed = deriveCoverageSummary([
    unit({ kind: "version" }),
    unit({ kind: "page", page_path: "a.html" }),
  ]);
  assert.equal(mixed.aggregationProblem, "mixed-unit-kinds");
  const overlap = deriveCoverageSummary([
    unit({ kind: "page", page_path: "first.html" }),
    unit({ kind: "region", page_path: "first.html", region_id: "overview" }),
  ], { targetSetId: "t1", scope: { kind: "page", pagePath: "first.html" } });
  assert.equal(overlap.aggregationProblem, "mixed-unit-kinds,overlapping-page-region-aggregation");
  assert.equal(overlap.aligned, false);
  // A version scope accepts distinct HTML page units but not region units.
  const versionScoped = deriveCoverageSummary([
    unit({ kind: "page", page_path: "a.html" }),
    unit({ kind: "region", page_path: "a.html", region_id: "overview" }),
  ], { targetSetId: "t1", scope: { kind: "version" } });
  assert.equal(versionScoped.outOfScope, 1);
});

test("an explicit discrepancy false is not an evidenced discrepancy; unrecognized values stay unknown", () => {
  const noDiscrepancy = deriveCoverageSummary([unit({ kind: "page", page_path: "a.html", discrepancy: false })]);
  assert.equal(noDiscrepancy.discrepancyUnits, 0);
  assert.equal(noDiscrepancy.unknownOutcome, 0);
  const evidenced = deriveCoverageSummary([unit({ kind: "page", page_path: "a.html", discrepancy: true }), unit({ kind: "page", page_path: "b.html", discrepancy: "행 이동" })], { targetSetId: null, scope: { kind: "version" } });
  assert.equal(evidenced.discrepancyUnits, 2);
  const unrecognized = deriveCoverageSummary([unit({ kind: "page", page_path: "a.html", discrepancy: 7 })]);
  assert.equal(unrecognized.discrepancyUnits, 0);
  assert.equal(unrecognized.unknownOutcome, 1);
});

test("zero/unknown eligibility never derives aligned", () => {
  assert.equal(deriveStatus(deriveCoverageSummary([]), { overlay: fresh({}), targetSetId: "t1" }), "not_assessed");
  const unknownOutcomes = deriveCoverageSummary([unit({ outcome: "weird" })]);
  assert.equal(deriveStatus(unknownOutcomes, { overlay: fresh({}), targetSetId: "t1" }), "not_assessed");
});

test("an all-assessed list alone never aligns without an available exact target", () => {
  const all = deriveCoverageSummary([unit({ kind: "page", page_path: "a.html" }), unit({ kind: "page", page_path: "b.html" })], { targetSetId: "t1", scope: { kind: "version" } });
  assert.equal(all.aligned, true);
  assert.equal(deriveStatus(all, { overlay: null, targetSetId: null }), "not_assessed");
  assert.equal(deriveStatus(all, { overlay: fresh({ target_set_id: null, captured_at: null }), targetSetId: null }), "not_assessed");
  assert.equal(deriveStatus(all, { overlay: fresh({}), targetSetId: "t1" }), "aligned_within_scope");
  // Capture ID/time alone is not availability: an unavailable target set id never aligns.
  assert.equal(deriveStatus(all, { overlay: fresh({}), targetSetId: "t9" }), "not_assessed");
});

test("an unknown/not_assessed overlay never upgrades to aligned from ID/time plus an all-assessed list", () => {
  const all = deriveCoverageSummary([unit({ kind: "page", page_path: "a.html" }), unit({ kind: "page", page_path: "b.html" })], { targetSetId: "t1", scope: { kind: "version" } });
  assert.equal(all.aligned, true);
  assert.equal(deriveStatus(all, { overlay: fresh({ status: "not_assessed" }), targetSetId: "t1" }), "not_assessed");
  assert.equal(deriveStatus(all, { overlay: fresh({ status: "unknown-outcome" }), targetSetId: "t1" }), "not_assessed");
  // Only an explicitly positive applicable assessment derives aligned.
  assert.equal(deriveStatus(all, { overlay: fresh({}), targetSetId: "t1" }), "aligned_within_scope");
});

test("null-link contributions cannot drive assessed, discrepancy or status", () => {
  // A null-link not_checked row is the honest unassessed placeholder.
  const placeholder = deriveCoverageSummary([unit({ kind: "page", page_path: "a.html", outcome: "not_checked", assessment_id: null, target_set_id: null })], { targetSetId: "t1", scope: { kind: "version" } });
  assert.equal(placeholder.notChecked, 1);
  assert.equal(placeholder.aggregationProblem, null);
  assert.equal(placeholder.aligned, false);
  // A null-link row claiming an assessed outcome is a missing/contradictory
  // linkage: aggregation error, never aligned even under a positive overlay.
  const nullAssessed = deriveCoverageSummary([unit({ kind: "page", page_path: "a.html", outcome: "assessed", assessment_id: null, target_set_id: "t1" })], { targetSetId: "t1", scope: { kind: "version" } });
  assert.equal(nullAssessed.aggregationProblem, "missing-contribution-link");
  assert.equal(nullAssessed.assessed, 0);
  assert.equal(deriveStatus(nullAssessed, { overlay: fresh({}), targetSetId: "t1" }), "not_assessed");
  // A null-link evidenced discrepancy cannot drive outdated.
  const nullDiscrepancy = deriveCoverageSummary([unit({ kind: "page", page_path: "a.html", outcome: "assessed", discrepancy: true, assessment_id: "a1", target_set_id: null })], { targetSetId: "t1", scope: { kind: "version" } });
  assert.equal(nullDiscrepancy.discrepancyUnits, 0);
  assert.equal(nullDiscrepancy.aggregationProblem, "missing-contribution-link");
  assert.equal(deriveStatus(nullDiscrepancy, { overlay: fresh({}), targetSetId: "t1" }), "not_assessed");
  // Linked matching rows keep driving normally beside a null-link placeholder.
  const mixed = deriveCoverageSummary([
    unit({ kind: "page", page_path: "a.html" }),
    unit({ kind: "page", page_path: "b.html", outcome: "not_checked", reason: "제외", assessment_id: null, target_set_id: null }),
  ], { targetSetId: "t1", scope: { kind: "version" } });
  assert.equal(mixed.assessed, 1);
  assert.equal(mixed.notChecked, 1);
  assert.equal(mixed.eligible, 2, "the denominator keeps both required units");
});

test("an undefined caller scope is the version scope: region coverage never aligns it", () => {
  // Actual SourceQueries.scope() caller shape: filter.scope is undefined for
  // a version selection. Admission must reject region units, so a positive
  // overlay with a matching region unit_kind cannot falsely align, and a
  // foreign region discrepancy cannot drive outdated.
  const regionUnits = [unit({ kind: "region", page_path: "first.html", region_id: "overview" })];
  const summary = deriveCoverageSummary(regionUnits, { targetSetId: "t1", unitKind: "region" });
  assert.equal(summary.outOfScope, 1);
  assert.equal(summary.assessed, 0);
  assert.equal(summary.aligned, false);
  assert.equal(deriveStatus(summary, { overlay: fresh({}), targetSetId: "t1" }), "not_assessed");
  const withDiscrepancy = deriveCoverageSummary([unit({ kind: "region", page_path: "first.html", region_id: "overview", discrepancy: true })], { targetSetId: "t1", unitKind: "region" });
  assert.equal(withDiscrepancy.discrepancyUnits, 0);
  assert.equal(deriveStatus(withDiscrepancy, { overlay: fresh({}), targetSetId: "t1" }), "not_assessed");
});

test("a homogeneous set of the wrong recorded unit kind is an aggregation error", () => {
  const pageUnits = [unit({ kind: "page", page_path: "a.html" }), unit({ kind: "page", page_path: "b.html" })];
  const mismatch = deriveCoverageSummary(pageUnits, { targetSetId: "t1", scope: { kind: "version" }, unitKind: "region" });
  assert.equal(mismatch.aggregationProblem, "unit-kind-mismatch");
  assert.equal(mismatch.aligned, false);
  assert.equal(mismatch.derivedComplete, false);
  assert.equal(mismatch.eligible, 2, "rows stay inspectable");
  const matching = deriveCoverageSummary(pageUnits, { targetSetId: "t1", scope: { kind: "version" }, unitKind: "page" });
  assert.equal(matching.aggregationProblem, null);
  assert.equal(matching.aligned, true);
});

test("a unit-kind-mismatched row is never admitted: no counts, discrepancy or status", () => {
  const summary = deriveCoverageSummary([
    unit({ kind: "region", page_path: "first.html", region_id: "overview" }),
    unit({ kind: "page", page_path: "first.html", outcome: "blocked", reason: "거부", discrepancy: true }),
  ], { targetSetId: "t1", scope: { kind: "page", pagePath: "first.html" }, unitKind: "region" });
  assert.equal(summary.aggregationProblem, "unit-kind-mismatch,mixed-unit-kinds");
  assert.equal(summary.assessed, 1);
  assert.equal(summary.blocked, 0, "a rejected wrong-kind row cannot drive blocked");
  assert.equal(summary.discrepancyUnits, 0, "a rejected wrong-kind row cannot drive discrepancy");
  assert.equal(summary.aligned, false);
  assert.equal(summary.eligible, 2, "the rejected row stays visible in the list");
  assert.equal(deriveStatus(summary, { overlay: fresh({}), targetSetId: "t1", scope: { kind: "page", pagePath: "first.html" } }), "not_assessed");
});

test("recorded unable_to_verify outranks an all-assessed list as completed-coverage-unavailable", () => {
  const all = deriveCoverageSummary([unit({ kind: "page", page_path: "a.html" }), unit({ kind: "page", page_path: "b.html" })], { targetSetId: "t1", scope: { kind: "version" } });
  assert.equal(all.aligned, true);
  const status = deriveStatus(all, { overlay: fresh({ status: "unable_to_verify" }), targetSetId: "t1" });
  assert.equal(status, "unable_to_verify");
});

test("registry precedence preserves the latest availability-blocked attempt exception", () => {
  const withDiscrepancy = deriveCoverageSummary([unit({ discrepancy: true, outcome: "blocked" })]);
  assert.equal(deriveStatus(withDiscrepancy, { overlay: fresh({ reassessment_required: true, status: "unable_to_verify" }), targetSetId: "t1" }), "unable_to_verify");
  assert.equal(withDiscrepancy.discrepancyUnits, 1, "unavailable status retains the recorded discrepancy");
  assert.equal(deriveStatus(withDiscrepancy, { overlay: fresh({ reassessment_required: true }), targetSetId: "t1" }), "outdated_against_target");
  const withTrigger = deriveCoverageSummary([unit({ outcome: "blocked", reason: "거부" })]);
  assert.equal(deriveStatus(withTrigger, { overlay: fresh({ reassessment_required: true }), targetSetId: "t1" }), "reassessment_needed");
  const blocked = deriveCoverageSummary([unit({ outcome: "blocked", reason: "거부" })]);
  assert.equal(deriveStatus(blocked, { overlay: fresh({}), targetSetId: "t1" }), "unable_to_verify");
});

test("the overlay badge is displayed but never trusted", () => {
  const partial = deriveCoverageSummary([unit({ kind: "page", page_path: "a.html" }), unit({ kind: "page", page_path: "b.html", outcome: "partial", reason: "일부" })], { targetSetId: "t1", scope: { kind: "version" } });
  const comparison = compareWithOverlay(partial, fresh({ coverage: { coverage_set_id: "c1", unit_kind: "page", eligible: 2, assessed: 2, complete: true, units_url: null } }), "t1");
  assert.equal(comparison.overlayStatus, "aligned_within_scope");
  assert.equal(comparison.overlayComplete, true);
  assert.equal(comparison.derived, "not_assessed");
  assert.equal(comparison.conflict, true);
});

test("units_url is validated as data: only the exact fixed local route for this version and set", () => {
  const good = coverageUnitsParams("/api/v1/versions/v1/assessments/coverage?coverage_set_id=c1", "v1", "c1", null);
  assert.deepEqual(good, { coverageSetId: "c1" });
  assert.equal(coverageUnitsParams("https://foreign.example/api/v1/versions/v1/assessments/coverage?coverage_set_id=c1", "v1", "c1", "https://daemon.local"), null);
  assert.deepEqual(coverageUnitsParams("https://daemon.local/api/v1/versions/v1/assessments/coverage?coverage_set_id=c1", "v1", "c1", "https://daemon.local"), { coverageSetId: "c1" });
  assert.equal(coverageUnitsParams("//daemon.local/api/v1/versions/v1/assessments/coverage?coverage_set_id=c1", "v1", "c1", null), null);
  assert.equal(coverageUnitsParams("/api/v1/versions/v2/assessments/coverage?coverage_set_id=c1", "v1", "c1", null), null);
  assert.equal(coverageUnitsParams("/api/v1/versions/v1/assessments/coverage?coverage_set_id=c2", "v1", "c1", null), null);
  assert.equal(coverageUnitsParams("/api/v1/versions/v1/assessments/coverage?coverage_set_id=c1&cursor=x", "v1", "c1", null), null);
  assert.equal(coverageUnitsParams(null, "v1", "c1", null), null);
  // An unknown coverage set id in the URL may still be named; it must match itself.
  assert.deepEqual(coverageUnitsParams("/api/v1/versions/v1/assessments/coverage?coverage_set_id=c9", "v1", null, null), { coverageSetId: "c9" });
});

test("region selection events match the captured version/page tuple only", () => {
  assert.equal(acceptRegionSelection({ versionId: "v1", pagePath: "a.html", regionId: "overview" }, { versionId: "v1", pagePath: "a.html" }), true);
  assert.equal(acceptRegionSelection({ versionId: "v1", pagePath: "b.html", regionId: "overview" }, { versionId: "v1", pagePath: "a.html" }), false);
  assert.equal(acceptRegionSelection({ versionId: "v2", pagePath: "a.html", regionId: "overview" }, { versionId: "v1", pagePath: "a.html" }), false);
  assert.equal(acceptRegionSelection({ versionId: "v1", pagePath: "a.html", regionId: "" }, { versionId: "v1", pagePath: "a.html" }), false);
  assert.equal(acceptRegionSelection({ versionId: "v1", pagePath: "a.html", regionId: "overview" }, { versionId: "v1", pagePath: null }), false);
});

test("region focus acks reject a late A-page ack under the B selection and empty identities", () => {
  assert.equal(acceptRegionFocusResult({ versionId: "v1", pagePath: "a.html", regionId: "overview" }, { versionId: "v1", pagePath: "a.html" }), true);
  // In-flight A ack arrives after the host mirrored/routed to page B.
  assert.equal(acceptRegionFocusResult({ versionId: "v1", pagePath: "a.html", regionId: "overview" }, { versionId: "v1", pagePath: "b.html" }), false);
  assert.equal(acceptRegionFocusResult({ versionId: "v2", pagePath: "a.html", regionId: "overview" }, { versionId: "v1", pagePath: "a.html" }), false);
  assert.equal(acceptRegionFocusResult({ versionId: "v1", pagePath: "a.html", regionId: "overview" }, { versionId: "v1", pagePath: null }), false);
  assert.equal(acceptRegionFocusResult({ versionId: "v1", pagePath: "a.html", regionId: "" }, { versionId: "v1", pagePath: "a.html" }), false);
});

test("pending region-focus requests cancel on unmount, route/context/page change, reload bump or replaced channel", () => {
  const handleA = { channel: "a" }, handleB = { channel: "b" };
  const captured = { versionId: "v1", context: "ctx-1", page: "a.html", reload: 3, handle: handleA };
  const now = (overrides: Partial<{ live: boolean; versionId: string; context: string | null; page: string | null; reload: number; handle: unknown }>) =>
    ({ live: true, versionId: "v1", context: "ctx-1", page: "a.html", reload: 3, handle: handleA, ...overrides });
  assert.equal(regionRequestStale(captured, now({})), false);
  // Unmounted Reader: the delayed attempt must never fire.
  assert.equal(regionRequestStale(captured, now({ live: false })), true);
  // Route/version/context moved on while the modal sheet was closing.
  assert.equal(regionRequestStale(captured, now({ versionId: "v2" })), true);
  assert.equal(regionRequestStale(captured, now({ context: "ctx-2" })), true);
  assert.equal(regionRequestStale(captured, now({ page: "b.html" })), true);
  assert.equal(regionRequestStale(captured, now({ page: null })), true);
  // Explicit same-context reload bumped the viewer generation.
  assert.equal(regionRequestStale(captured, now({ reload: 4 })), true);
  // The live channel handle was replaced (remounted viewer, same context).
  assert.equal(regionRequestStale(captured, now({ handle: handleB })), true);
});

test("stored region ownership renders unselected on version/page/context/reload mismatch", () => {
  const selection = { versionId: "v1", pagePath: "a.html", context: "ctx-1", reload: 0, regionId: "overview" };
  const current = { versionId: "v1", pagePath: "a.html", context: "ctx-1", reload: 0 };
  assert.equal(regionSelectionMatches(selection, current), true);
  assert.equal(regionSelectionMatches(selection, { ...current, versionId: "v2" }), false, "version switch clears");
  assert.equal(regionSelectionMatches(selection, { ...current, pagePath: "b.html" }), false, "page switch clears");
  assert.equal(regionSelectionMatches(selection, { ...current, pagePath: null }), false, "page-less context clears");
  assert.equal(regionSelectionMatches(selection, { ...current, context: "ctx-2" }), false, "new host context clears");
  assert.equal(regionSelectionMatches(selection, { ...current, reload: 1 }), false, "explicit same-context reload clears");
});

test("selection guards reject stale selections and order-reversed replies", () => {
  const guard = new SelectionQueryGuard("v1", "a.html", null);
  const first = guard.begin("assessments");
  const second = guard.begin("assessments");
  assert.equal(guard.accept("assessments", second), true);
  // The older request resolving after the newer one never applies.
  assert.equal(guard.accept("assessments", first), false);
  guard.rebind("v1", "b.html", null);
  assert.equal(guard.accept("assessments", second), false);
  const next = guard.begin("assessments");
  assert.equal(guard.accept("assessments", next), true);
  // An explicit reselection of the same page bumps the generation.
  guard.bump();
  assert.equal(guard.accept("assessments", next), false);
});

test("cursor enumeration accumulates until complete and keeps its resume point on failure", () => {
  let state = initialEnumeration<{ id: string }>();
  state = mergeEnumerationPage(state, { items: [{ id: "1" }], next_cursor: "c2" });
  assert.equal(enumerationComplete(state), false);
  state = mergeEnumerationPage(state, { items: [{ id: "2" }], next_cursor: null });
  assert.equal(enumerationComplete(state), true);
  assert.deepEqual(state.items.map((item) => item.id), ["1", "2"]);
  const failed = mergeEnumerationPage({ items: state.items, nextCursor: "c9", failed: false, resumeFrom: null }, null);
  assert.equal(failed.failed, true);
  assert.equal(failed.resumeFrom, "c9");
  assert.equal(enumerationComplete(failed), false);
});

test("panel toggles treat inherited-key IDs as absent on first click", () => {
  for (const id of ["constructor", "toString", "valueOf", "__proto__", "hasOwnProperty"]) {
    const first: Record<string, boolean> = {};
    assert.equal(toggledOpen(first, id), true, id + " must expand on first click");
    const once: Record<string, boolean> = { [id]: true };
    assert.equal(toggledOpen(once, id), false, id + " must collapse after opening");
    const twice: Record<string, boolean> = { [id]: false };
    assert.equal(toggledOpen(twice, id), true, id + " must reopen after collapse");
  }
});

test("latest unavailable attempt supplies status while the captured target's reassessment trigger remains visible", () => {
  const summary = deriveCoverageSummary([unit({ outcome: "not_checked", assessment_id: null, reason: "missing_source" })]);
  const applicable = fresh({ status: "unable_to_verify", reassessment_required: true });
  assert.equal(deriveStatus(summary, { overlay: applicable, targetSetId: "t1" }), "unable_to_verify");
  assert.equal(applicable.reassessment_required, true);
});
