import test from "node:test";
import assert from "node:assert/strict";
import { evidenceAvailability, evidenceExcerpt, coverageReason, sourceObservationLabel } from "./source-presentation.ts";
import { coverageUnitsParams } from "./evidence-view.ts";

test("not-read is neutral; missing source F07, denied F10, retained missing and corrupt stay explicit", () => {
  assert.match(evidenceAvailability({ availability: "unavailable", reason: "not_read_by_view" }), /^확인 전/);
  assert.doesNotMatch(evidenceAvailability({ availability: "unavailable", reason: "not_read_by_view" }), /사용 불가|원본 없음/);
  assert.match(evidenceAvailability({ availability: "unavailable", reason: "missing_source" }), /마지막 확인 시점에 원본 없음/);
  assert.match(evidenceAvailability({ availability: "denied", reason: null }), /읽기 권한이 없습니다/);
  assert.match(evidenceAvailability({ availability: "unavailable", reason: "missing_retained_bytes" }), /스냅샷 없음/);
  assert.match(evidenceAvailability({ availability: "unavailable", reason: "retained_bytes_corrupt" }), /무결성 오류/);
  assert.equal(evidenceAvailability({ availability: "available", reason: null }), "사용 가능");
});

test("coverage reason presentation discloses dependency scope and preserves source/reassessment/no-citation reasons", () => {
  assert.match(coverageReason("unexamined_dependency_scope"), /재평가 판정이 아닙니다/);
  assert.match(coverageReason("direct_source_change"), /이 대상\/범위의 재평가 필요/);
  assert.match(coverageReason("no_cited_evidence"), /인용된 근거 없음/);
  assert.match(coverageReason("source_denied"), /권한 없음/);
  assert.equal(coverageReason("future-reason"), "future-reason");
});

test("coverage URL accepts exactly the fixed set plus at most one canonical UUID attempt", () => {
  const base = "/api/v1/versions/v/assessments/coverage?coverage_set_id=c";
  const attempt = "11111111-2222-4333-8444-555555555555";
  assert.deepEqual(coverageUnitsParams(base + "&attempt_run_id=" + attempt, "v", "c", null), { coverageSetId: "c", attemptRunId: attempt });
  for (const extra of ["&attempt_run_id=", "&attempt_run_id=bad", "&attempt_run_id=" + attempt + "&attempt_run_id=" + attempt, "&cursor=x", "&extra=x", "&coverage_set_id=c"]) assert.equal(coverageUnitsParams(base + extra, "v", "c", null), null);
  assert.equal(coverageUnitsParams(base + "&attempt_run_id=" + attempt.toUpperCase().replace("11111111", "AAAAAAAA"), "v", "c", null), null);
});

test("retained evidence excerpts preserve UTF-8 and binary bytes without rendering markup", () => {
  assert.equal(evidenceExcerpt({ bytes_sha256: "digest", data_base64: btoa("<script>literal</script>\r\n") }), "<script>literal</script>\r\n");
  assert.equal(evidenceExcerpt({ bytes_sha256: "digest", data_base64: "/w==" }), "Base64: /w==");
  assert.equal(evidenceExcerpt({ bytes_sha256: "digest" }), null);
});

test("T5 an available recorded source with null reason is distinct from no observation", () => {
  const text = sourceObservationLabel({ available: true, reason: null, last_observed_at: "2026-10-24T00:00:00Z" });
  assert.match(text, /사용 가능/); assert.match(text, /2026-10-24T00:00:00Z/);
  assert.doesNotMatch(text, /not_observed|관측 기록 없음/);
  assert.match(sourceObservationLabel({ available: false, reason: "not_observed", last_observed_at: null }), /관측 기록 없음/);
  assert.match(sourceObservationLabel(undefined), /제공하지 않습니다/);
});
