// Controlled unit DTOs only, not actual API/runtime/provider observations.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { ManagementAvailableEffect, ManagementEffect, TopicEvidenceBasis, ManagementUnavailableEffect } from "../../client/types.ts";
import { ManagementLinkRow } from "./management-links-row.ts";

const from = { document_id: "visible-document", version_id: "visible-version", page_path: "page.html", region_id: null };
const unavailable: ManagementUnavailableEffect = {
  availability: "unavailable", overlay_id: "opaque-unavailable", kind: "related_explanation", from,
  origin: "ai", decision: "accepted", revision: 2, etag: '"overlay-opaque-r2-unavailable"',
};
const available: ManagementAvailableEffect = {
  availability: "available", overlay_id: "visible-overlay", kind: "related_explanation", semantic_key: "semantic",
  finding_id: "finding", job_id: "job", from,
  target: { document_id: "target-document", version_id: "exact-target-version", page_path: "target.html", region_id: "section" },
  relation_type: "conceptual_analogy", mechanism_key: "mechanism", origin: "ai", evidence_basis: "thematic_analogy",
  evidence: [{ version_id: "visible-version" }], rationale: "<img src=x onerror=unsafe> is text", uncertainty: "source_unverified",
  evidence_fingerprint: "fingerprint", target_set_id: null, captured_config_revision: 2, applied_config_revision: 2,
  permissions_revision: 1, decision: "accepted", decision_actor: "owner-actor", pinned: false, revision: 1,
  etag: '"overlay-visible-r1"', created_at: "retained-created", updated_at: "retained-updated",
};
const render = (effect: ManagementEffect) => renderToStaticMarkup(createElement(ManagementLinkRow, { effect, onOpen: () => { throw Error("render must not navigate"); } }));

function assertUnavailableMarkup(html: string) {
  assert.match(html, /<li>/);
  assert.match(html, /AI 추가/);
  assert.match(html, /대상을 사용할 수 없습니다/);
  assert.match(html, /accepted/);
  assert.doesNotMatch(html, /<(?:button|a|details|pre)\b|data-overlay-id|href=/);
  assert.doesNotMatch(html, /opaque-unavailable|visible-document|visible-version|page\.html|owner-actor/);
}

test("closed unavailable DTO remains a visible nonclickable row without identity/details DOM", () => {
  assert.deepEqual(Object.keys(unavailable).sort(), ["availability", "overlay_id", "kind", "from", "origin", "decision", "revision", "etag"].sort());
  assertUnavailableMarkup(render(unavailable));
});

test("unavailable branch never reads retained hidden payload or serializes extra properties", () => {
  const row = { ...unavailable };
  for (const key of ["target", "evidence", "rationale", "decision_actor", "finding_id", "job_id", "semantic_key", "target_set_id", "relation_type"]) {
    Object.defineProperty(row, key, { enumerable: true, get() { throw Error("hidden field read: " + key); } });
  }
  assertUnavailableMarkup(render(row));
});

test("malformed availability cannot fall through to full payload rendering", () => {
  // Controlled malformed wire input; no third public availability enum added.
  for (const availability of [undefined, "future-unknown"]) {
    assertUnavailableMarkup(render({ ...unavailable, availability } as unknown as ManagementEffect));
  }
});

test("available relation retains exact navigation text, separate AI/actor and escaped details", () => {
  const html = render(available);
  assert.match(html, /data-overlay-id="visible-overlay"/);
  assert.match(html, /<button/);
  assert.match(html, /exact-target-version/);
  assert.match(html, /target\.html/);
  assert.match(html, /section/);
  assert.match(html, /AI 추가/);
  assert.match(html, /승인 주체 owner-actor/);
  assert.match(html, /<details>/);
  assert.match(html, /&lt;img/);
  assert.doesNotMatch(html, /<img/);
});

for (const [basis, label] of [
  ["explanation_text", "설명 본문 (explanation_text)"],
  ["source_verified", "검증된 소스 (source_verified)"],
  ["legacy_unknown", "이전 기록의 근거 유형 미상 (legacy_unknown)"],
] as const satisfies ReadonlyArray<readonly [TopicEvidenceBasis, string]>) {
  test("actual topic projection displays explicit basis: " + basis, () => {
    const topic: ManagementAvailableEffect = { ...available, kind: "topic_assignment", target: null,
      topic_id: "topic-id", topic: "retained-topic", evidence_basis: basis,
      rationale: "Retained meaning does not determine basis; source_verified here is narrative text." };
    const html = render(topic);
    assert.ok(html.includes("근거 " + label));
    assert.match(html, /retained-topic/);
    assert.match(html, /승인 주체 owner-actor/);
    assert.doesNotMatch(html, /<button/);
    assert.equal(topic.evidence_basis, basis);
    if (basis === "legacy_unknown") assert.doesNotMatch(html, /근거 검증된 소스/);
  });
}
