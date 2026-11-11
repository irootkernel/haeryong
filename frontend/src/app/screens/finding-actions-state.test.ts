import test from "node:test";
import assert from "node:assert/strict";
import { FindingActionState, effectChoices, findingChoices, reassessmentRequest } from "./finding-actions-state.ts";
import type { ActionController } from "./finding-actions-state.ts";
import { SourceWriteIntents } from "../../client/source-write-intents.ts";
import { PendingSourceWriteError } from "../../client/source-operations.ts";
import { ApiError, DispatchBlockedError } from "../../client/types.ts";
import type { FindingActionResult, FindingView, HaeryongClient, ManagementEffect, OperationView } from "../../client/types.ts";

const finding = (extra: Partial<FindingView> = {}) => ({ finding_id: "fnd-test", project_id: "project", document_id: "doc", version_id: "version", page_path: null, region_id: null,
  revision: 3, etag: '"finding-r3"', kind: "related_explanation", status: "proposed", proposal: {}, ...extra }) as FindingView;
const effect = (extra = {}) => ({ overlay_id: "effect", availability: "available", kind: "topic_assignment", decision: "accepted", pinned: false, revision: 8, etag: '"effect-r8"', ...extra }) as ManagementEffect;
const result = (operation_id = "key"): FindingActionResult => ({ operation_id, state: "completed", finding: null, effect: null, job_id: null });
function harness(client: Partial<HaeryongClient> = {}) {
  let phase = "verified", nonce = 1, generation = 0;
  const listeners = new Set<() => void>(), intents = new SourceWriteIntents(), requests: unknown[] = [];
  const controller = {
    get state() { return { phase, capabilities: { features: { scans: true, management_overlays: true } } }; },
    get readGeneration() { return generation; },
    captureDispatchAuthority: () => phase === "verified" ? { nonce, libraryId: "library", runtimeGeneration: "runtime" } : null,
    requireVerifiedDispatch: (auth: { nonce: number } | null) => { if (phase !== "verified" || auth?.nonce !== nonce) throw new DispatchBlockedError(); },
    isCurrentDispatchAuthority: (auth: { nonce: number } | null) => phase === "verified" && auth?.nonce === nonce,
    isCurrentReadAuthority: (auth: { nonce: number } | null, read: number) => phase === "verified" && auth?.nonce === nonce && read === generation,
    sourceWriteIntents: () => intents,
    subscribe: (fn: () => void) => { listeners.add(fn); return () => { listeners.delete(fn); }; },
    client: { findingDecision: async (...args: unknown[]) => { requests.push(args); return result(); }, ...client },
  } as unknown as ActionController;
  return { controller, intents, requests, lose() { phase = "unauthenticated"; nonce++; for (const fn of listeners) fn(); }, bump() { generation++; } };
}
function deferred<T>() { let resolve!: (x: T) => void; const promise = new Promise<T>(r => { resolve = r; }); return { promise, resolve }; }

test("legal finding and effect choices exclude applied reject/content approve/blocked reject and every undone Topic PUT", () => {
  assert.deepEqual(findingChoices(finding()), ["approve", "reject"]);
  assert.deepEqual(findingChoices(finding({ status: "applied" })), []);
  assert.deepEqual(findingChoices(finding({ status: "blocked" })), ["approve"]);
  assert.deepEqual(findingChoices(finding({ kind: "new_version_proposal" })), ["reject"]);
  assert.deepEqual(findingChoices(finding({ kind: "backlink_index_repair", reason: "approved_repair_pending" })), []);
  assert.deepEqual(findingChoices(finding({ kind: "future_kind" as FindingView["kind"] })), []);
  for (const status of ["undone", "stale", "rejected"] as const) assert.deepEqual(findingChoices(finding({ status })), []);
  assert.deepEqual(effectChoices(effect({ decision: "undone" })), []);
  assert.deepEqual(effectChoices(effect({ availability: "unavailable" })), []);
  assert.deepEqual(effectChoices(effect({ pinned: true })), ["unpin", "reject"]);
  assert.deepEqual(effectChoices(effect({ decision: "rejected" })), ["reaccept"]);
});
test("freshness exact reassessment scope comes from proposal.scope, retains origin revision and explicit successor/source selection", () => {
  const row = finding({ kind: "freshness_assessment", proposal: { assessment_id: "a", version_id: "version", target_set_id: "target", state: "aligned_within_scope", assessed_at: "then", scope: { kind: "region", page_path: "nested.html", region_id: "main" } } });
  const request = reassessmentRequest(row, { version: "explicit-successor", depth: "change_focused", sourceMode: "current", targetSet: "new-target", sources: [{ source_id: "s", mode: "current" }] });
  assert.deepEqual(request, { scope: "selected_projects", project_ids: ["project"], document_id: "doc", version_id: "explicit-successor", page_path: "nested.html", region_id: "main", finding_id: "fnd-test", expected_finding_revision: 3, depth: "change_focused", source_mode: "current", target_set_id: "new-target", sources: [{ source_id: "s", mode: "current" }] });
  assert.throws(() => reassessmentRequest({ ...row, proposal: {} }, { version: "version", depth: "full_scope", sourceMode: "pinned", targetSet: "", sources: undefined }));
});
test("single dispatch saves exact original request and validator before awaiting; duplicate action is blocked", async () => {
  const wait = deferred<FindingActionResult>(); const h = harness({ findingDecision: async (...args) => { h.requests.push(args); return wait.promise; } });
  const m = new FindingActionState(h.controller, "finding:fnd-test"); m.connect();
  const request = { kind: "finding" as const, id: "fnd-test", decision: "approve" as const, etag: '"r3"' };
  const pending = m.start(request, "key");
  request.etag = '"r99"'; await m.start(request, "other");
  assert.deepEqual(h.requests, [["fnd-test", "approve", '"r3"', "key"]]);
  wait.resolve(result()); await pending; assert.equal(m.result?.operation_id, "key"); m.dispose();
});
test("pending remount recovers same receipt only; completed202 remains repair_pending rather than applied", async () => {
  let writes = 0, reads = 0;
  const repair: FindingActionResult = { ...result(), state: "repair_pending", job_id: "repair-job" };
  const h = harness({ findingDecision: async () => { writes++; throw new PendingSourceWriteError("key"); }, operation: async id => { reads++; return { operation_id: id, state: "completed", original_status: 202, result_metadata: repair } as OperationView; } });
  const original = new FindingActionState(h.controller, "finding:fnd-test"); original.connect();
  await original.start({ kind: "finding", id: "fnd-test", decision: "approve", etag: '"r3"' }, "key"); original.dispose();
  const remount = new FindingActionState(h.controller, "finding:fnd-test"); remount.connect();
  assert.equal(remount.intent?.operationId, "key"); await remount.recover();
  assert.equal(writes, 1); assert.equal(reads, 1); assert.equal((remount.result as FindingActionResult).state, "repair_pending"); remount.dispose();
});
for (const status of [409, 412]) test(`definite ${status} retains request until deliberate acknowledgement with zero automatic reload/retry`, async () => {
  const h = harness({ findingDecision: async () => { h.requests.push("write"); throw new ApiError(status, status === 412 ? "PRECONDITION_FAILED" : "INVALID_TRANSITION", false, 0, { reason: "owner_decision_changed" }); } });
  const m = new FindingActionState(h.controller, "finding:fnd-test");
  await m.start({ kind: "finding", id: "fnd-test", decision: "approve", etag: '"old"' }, "key");
  await m.start({ kind: "undo", id: "effect", etag: '"new"' }, "other");
  assert.equal(h.requests.length, 1); assert.equal(m.intent?.outcome.state, "failed"); assert.equal(m.intent?.request.kind, "finding"); assert.match(m.error!, /입력|원래 요청/);
  m.acknowledge(); assert.equal(m.intent, undefined);
});
for (const status of [401, 403, 404]) test(`denied ${status} clears protected request/result`, async () => {
  const h = harness({ overlayUndo: async () => { throw new ApiError(status, "NOT_FOUND", false); } }); const m = new FindingActionState(h.controller, "effect:effect");
  await m.start({ kind: "undo", id: "effect", etag: '"r8"' }, "key");
  assert.equal(m.denied, true); assert.equal(m.result, null); assert.equal(m.intent, undefined); assert.equal(h.intents.maintenance.size, 0);
});
test("auth loss clears action presentation and late response cannot revive it", async () => {
  const wait = deferred<FindingActionResult>(), h = harness({ findingDecision: () => wait.promise });
  const m = new FindingActionState(h.controller, "finding:fnd-test"); m.connect(); const pending = m.start({ kind: "finding", id: "fnd-test", decision: "approve", etag: '"r3"' }, "key");
  h.lose(); wait.resolve(result()); await pending;
  assert.equal(m.result, null); assert.equal(m.intent, undefined); assert.equal(m.busy, false); m.dispose();
});
test("selection read-generation loss suppresses late result but retains receipt recovery and releases local busy", async () => {
  const wait = deferred<FindingActionResult>(), h = harness({ findingDecision: () => wait.promise });
  const m = new FindingActionState(h.controller, "finding:fnd-test"); const pending = m.start({ kind: "finding", id: "fnd-test", decision: "approve", etag: '"r3"' }, "key");
  h.bump(); wait.resolve(result()); await pending;
  assert.equal(m.result, null); assert.equal(m.intent?.outcome.state, "pending"); assert.equal(m.busy, false);
});
test("unverified start has no registry or dispatch effects", async () => {
  const h = harness(); h.lose(); const m = new FindingActionState(h.controller, "finding:fnd-test");
  await assert.rejects(m.start({ kind: "undo", id: "effect", etag: '"r8"' }, "key"), DispatchBlockedError);
  assert.equal(h.intents.maintenance.size, 0); assert.equal(h.requests.length, 0);
});
test("Topic/Undo dispatch use their current individual validator and explicit reassessment only starts scan", async () => {
  const calls: unknown[] = [], h = harness({
    topicAssignmentSet: async (...args) => { calls.push(["topic", ...args]); return result("topic-key"); },
    overlayUndo: async (...args) => { calls.push(["undo", ...args]); return result("undo-key"); },
    scanStart: async (...args) => { calls.push(["scan", ...args]); return { operation_id: "scan-key", job_id: "new-job", coalesced: false } as Awaited<ReturnType<NonNullable<HaeryongClient["scanStart"]>>>; },
  });
  const topic = new FindingActionState(h.controller, "effect:topic");
  await topic.start({ kind: "topic", id: "topic", body: { pinned: false, decision: "rejected" }, etag: '"current-topic"' }, "topic-key");
  const undo = new FindingActionState(h.controller, "effect:relation");
  await undo.start({ kind: "undo", id: "relation", etag: '"current-relation"' }, "undo-key");
  const scan = new FindingActionState(h.controller, "finding:fnd-test"), request = { finding_id: "fnd-test", expected_finding_revision: 3, document_id: "doc", version_id: "version", depth: "full_scope" as const, source_mode: "pinned" as const };
  await scan.start({ kind: "scan", request }, "scan-key");
  assert.deepEqual(calls, [["topic", "topic", { pinned: false, decision: "rejected" }, '"current-topic"', "topic-key"], ["undo", "relation", '"current-relation"', "undo-key"], ["scan", request, "scan-key"]]);
  assert.equal(h.requests.length, 0); assert.equal(scan.result?.job_id, "new-job");
});
test("failed receipt keeps original 412 request and exact reason; denied receipt removes protected presentation without losing unknown key", async () => {
  let status = 412;
  const h = harness({ findingDecision: async () => { throw new PendingSourceWriteError("key"); }, operation: async id => {
    if (status === 403) throw new ApiError(403, "FORBIDDEN", false);
    return { operation_id: id, state: "failed", original_status: 412, result_metadata: { error: { code: "PRECONDITION_FAILED", details: { reason: "owner_decision_changed" } } } } as OperationView;
  } });
  const m = new FindingActionState(h.controller, "finding:fnd-test");
  await m.start({ kind: "finding", id: "fnd-test", decision: "approve", etag: '"old"' }, "key");
  await m.recover(); assert.equal(m.intent?.outcome.state, "failed"); assert.match(m.error!, /owner_decision_changed/);
  status = 403; await m.recover(); assert.equal(m.denied, true); assert.equal(m.intent, undefined); assert.equal(m.result, null);
  assert.equal(h.intents.maintenance.get("finding:fnd-test")?.operationId, "key");
  assert.equal(h.intents.maintenance.get("finding:fnd-test")?.outcome.state, "pending");
});
test("dispose during dispatch retains the original intent; new selection recovers by receipt without another Execute", async () => {
  const wait = deferred<FindingActionResult>(); let calls = 0, reads = 0;
  const h = harness({ findingDecision: async () => { calls++; return wait.promise; }, operation: async id => { reads++; return { operation_id: id, state: "completed", original_status: 200, result_metadata: result() } as OperationView; } });
  const old = new FindingActionState(h.controller, "finding:fnd-test");
  const pending = old.start({ kind: "finding", id: "fnd-test", decision: "approve", etag: '"r3"' }, "key"); old.dispose();
  wait.resolve(result()); await pending; assert.equal(old.result, null);
  const current = new FindingActionState(h.controller, "finding:fnd-test"); await current.recover();
  assert.equal(current.result?.operation_id, "key"); assert.equal(calls, 1); assert.equal(reads, 1);
});
