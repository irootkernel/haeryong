import test from "node:test";
import assert from "node:assert/strict";
import type {ConfigurationView} from "../../client/types.ts";
import {actionPolicyDraft, actionPolicyChanges, effectiveActionPolicy} from "./action-policy-state.ts";

function view(): ConfigurationView {
  return {config_revision: 7, configuration: {
    maintenance: {auto_apply: {backlink_index_repair: "auto", related_explanation: "propose", topic_assignment: "disabled", freshness_assessment: "auto"}, content_generation: "propose"},
    projects: {p: {maintenance: {auto_apply: {related_explanation: "auto", topic_assignment: "auto", freshness_assessment: "disabled"}, content_generation: "draft"}}},
  }} as unknown as ConfigurationView;
}

test("T00804 project saved overrides stay separate from current library eligibility", () => {
  const current = view(), before = structuredClone(current);
  const draft = actionPolicyDraft(current, "p");
  assert.equal(draft.auto_apply.related_explanation, "auto");
  assert.equal(draft.content_generation, "draft");
  assert.deepEqual(effectiveActionPolicy(current, draft), {
    auto_apply: {backlink_index_repair: "auto", related_explanation: "propose", topic_assignment: "disabled", freshness_assessment: "disabled"},
    content_generation: "propose",
  });
  assert.deepEqual(current, before);
});

test("T00804 policy changes contain only categories and cannot publish content or disclose sources", () => {
  const draft = actionPolicyDraft(view(), "p");
  const polluted = {...draft, sources: [{root_path: "/private"}], provider_id: "other"};
  const changes = actionPolicyChanges(polluted);
  assert.deepEqual(Object.keys(changes.maintenance).sort(), ["auto_apply", "content_generation"]);
  changes.maintenance.auto_apply.related_explanation = "disabled";
  assert.equal(draft.auto_apply.related_explanation, "auto");
  assert.throws(() => actionPolicyChanges({...draft, content_generation: "auto" as never}));
  assert.throws(() => actionPolicyChanges({...draft, auto_apply: {...draft.auto_apply, topic_assignment: "draft" as never}}));
});
