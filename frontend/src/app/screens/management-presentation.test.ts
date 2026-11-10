import { test } from "node:test";
import assert from "node:assert/strict";
import { topicEvidenceBasisLabel } from "./management-presentation.ts";

test("topic labels preserve the three actual read-projected provenance states", () => {
  assert.equal(topicEvidenceBasisLabel("explanation_text"), "설명 본문 (explanation_text)");
  assert.equal(topicEvidenceBasisLabel("source_verified"), "검증된 소스 (source_verified)");
  assert.equal(topicEvidenceBasisLabel("legacy_unknown"), "이전 기록의 근거 유형 미상 (legacy_unknown)");
});

test("missing or noncanonical client data does not manufacture verified or legacy provenance", () => {
  for (const basis of [undefined, null, "", "explanation_context", "source_intent", { evidence_basis: "source_verified" }]) {
    assert.equal(topicEvidenceBasisLabel(basis), "근거 유형을 확인할 수 없습니다");
  }
});
