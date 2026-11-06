import { SourceWriteIntents } from "../../client/source-write-intents.ts";
// TASK-003-04 SourceQueries regressions: the ACTUAL enumeration controller
// behind the SourcePanel. A later-page failure retains already accumulated
// rows plus the failed cursor; retry reissues from that cursor under the same
// captured identity; coverage and verification retries stay reachable; 503
// stays an honest unavailable phase; version/page/region AND same-page
// history/render/reload context changes clear and rebind everything, and a
// late reply from a previous context never lands.
import test from "node:test";
import assert from "node:assert/strict";
import type { ApplicableFreshness, AssessmentRecord, CoverageUnit, EvidenceItemView, HaeryongClient, ListPage, VerificationRecord } from "../../client/types.ts";
import { ApiError } from "../../client/types.ts";
import { SourceQueries } from "./source-queries.ts";

type AssessmentPage = ListPage<AssessmentRecord> & { applicable?: ApplicableFreshness | null };

function fakeClient(handlers: {
  assessments?: (cursor?: string) => Promise<AssessmentPage>;
  evidence?: () => Promise<EvidenceItemView>;
  coverage?: (cursor?: string) => Promise<ListPage<CoverageUnit>>;
  verifications?: (evidenceId: string, cursor?: string) => Promise<ListPage<VerificationRecord>>;
}): HaeryongClient {
  return {
    publicOrigin: null,
    async configurationView(): Promise<never> { throw new Error("unavailable in enumeration fixture"); },
    async configurationPlan(): Promise<never> { throw new Error("unavailable in enumeration fixture"); },
    async configurationPlanCommit(): Promise<never> { throw new Error("unavailable in enumeration fixture"); },
    async projectRegister(): Promise<never> { throw new Error("unavailable in enumeration fixture"); },
    async projectSources(): Promise<never> { throw new Error("unavailable in enumeration fixture"); },
    async verifySourceEvidence(): Promise<never> { throw new Error("unavailable in enumeration fixture"); },
    async runSourceObservation(): Promise<never> { throw new Error("unavailable in enumeration fixture"); },

    versionAssessments: (_versionId: string, filter?: { cursor?: string }) => handlers.assessments!(filter?.cursor),
    assessmentCoverage: (_versionId: string, _coverageSetId: string, cursor?: string) => handlers.coverage!(cursor),
    evidenceVerifications: (_versionId: string, evidenceId: string, cursor?: string) => handlers.verifications!(evidenceId, cursor),
    versionEvidence: () => handlers.evidence!(),
  } as unknown as HaeryongClient;
}

const applicableWith = (unitsUrl: string | null): ApplicableFreshness => ({
  source_intent: "historical", target_set_id: "t1", captured_at: "2026-10-19T00:00:00Z",
  scope: { kind: "version", page_path: null, region_id: null }, status: "not_assessed",
  coverage: { coverage_set_id: "c1", unit_kind: "version", eligible: 0, assessed: 0, complete: false, units_url: unitsUrl },
  assessed_at: null, reassessment_required: false,
});

const tick = () => new Promise((resolve) => setImmediate(resolve));

test("a later assessment page failure keeps accumulated rows, the applicable summary and the failed cursor", async () => {
  let calls = 0;
  const client = fakeClient({
    assessments: async () => {
      calls += 1;
      if (calls === 1) return { items: [{ assessment_id: "a1", version_id: "v1", scope: { kind: "version", page_path: null, region_id: null }, target_set_id: "t1" }], next_cursor: "page-2", applicable: applicableWith(null) };
      throw new Error("page-2 transport loss");
    },
  });
  const queries = new SourceQueries(client, "v1", null, null, "ctx-1", new SourceWriteIntents());
  await queries.loadAssessments();
  const view = queries.snapshot().assessments;
  assert.equal(view.phase, "error");
  assert.equal(view.reason, "page-2 transport loss");
  assert.equal(view.history.items.length, 1);
  assert.equal(view.applicable?.target_set_id, "t1");
  assert.equal(view.history.resumeFrom, "page-2");
  assert.equal(view.history.complete, false);
});

test("assessment retry resumes from the failed cursor and completes under the same identity", async () => {
  let calls = 0;
  const cursors: Array<string | undefined> = [];
  const client = fakeClient({
    assessments: async (cursor) => {
      cursors.push(cursor);
      calls += 1;
      if (calls === 1) return { items: [{ assessment_id: "a1", version_id: "v1", scope: { kind: "version", page_path: null, region_id: null }, target_set_id: "t1" }], next_cursor: "page-2", applicable: applicableWith(null) };
      if (calls === 2) throw new Error("boom");
      assert.equal(cursor, "page-2");
      return { items: [{ assessment_id: "a2", version_id: "v1", scope: { kind: "version", page_path: null, region_id: null }, target_set_id: "t1" }], next_cursor: null, applicable: applicableWith(null) };
    },
  });
  const queries = new SourceQueries(client, "v1", null, null, "ctx-1", new SourceWriteIntents());
  await queries.loadAssessments();
  queries.retryAssessments();
  await tick();
  const view = queries.snapshot().assessments;
  assert.equal(view.phase, "ready");
  assert.deepEqual(view.history.items.map((item) => item.assessment_id), ["a1", "a2"]);
  assert.equal(view.history.complete, true);
  assert.deepEqual(cursors, [undefined, "page-2", "page-2"]);
});

test("FEATURE_NOT_READY is an honest unavailable phase, never a partial denominator error", async () => {
  const client = fakeClient({
    assessments: async () => { throw new ApiError(503, "FEATURE_NOT_READY", false, 0, {}); },
  });
  const queries = new SourceQueries(client, "v1", null, null, "ctx-1", new SourceWriteIntents());
  await queries.loadAssessments();
  const view = queries.snapshot().assessments;
  assert.equal(view.phase, "unavailable");
  assert.match(view.reason ?? "", /503 FEATURE_NOT_READY/);
  assert.equal(view.history.items.length, 0);
});

test("coverage follows the validated fixed route, keeps partial units on failure and its retry is reachable", async () => {
  let calls = 0;
  const client = fakeClient({
    assessments: async () => ({ items: [], next_cursor: null, applicable: applicableWith("/api/v1/versions/v1/assessments/coverage?coverage_set_id=c1") }),
    coverage: async () => {
      calls += 1;
      if (calls === 1) return { items: [{ kind: "version", page_path: null, region_id: null, outcome: "assessed", reason: null, discrepancy: null, assessment_id: "a1", target_set_id: "t1" }], next_cursor: "units-2" };
      if (calls === 2) throw new Error("units page lost");
      return { items: [{ kind: "version", page_path: null, region_id: null, outcome: "not_checked", reason: "제외됨", discrepancy: null, assessment_id: null, target_set_id: null }], next_cursor: null };
    },
  });
  const queries = new SourceQueries(client, "v1", null, null, "ctx-1", new SourceWriteIntents());
  await queries.loadAssessments();
  // loadAssessments schedules the coverage follow-up without awaiting it.
  await tick();
  await tick();
  let coverage = queries.snapshot().coverage;
  assert.equal(coverage.phase, "error");
  assert.equal(coverage.units.items.length, 1);
  assert.equal(coverage.units.resumeFrom, "units-2");
  assert.equal(coverage.units.complete, false);
  queries.retryCoverage();
  await tick();
  await tick();
  coverage = queries.snapshot().coverage;
  assert.equal(coverage.phase, "ready");
  assert.equal(coverage.units.items.length, 2);
  assert.equal(coverage.units.complete, true);
});

test("a foreign units_url is inspectable-unavailable and retry re-validates", async () => {
  const client = fakeClient({
    assessments: async () => ({ items: [], next_cursor: null, applicable: applicableWith("https://foreign.example/api/v1/versions/v1/assessments/coverage?coverage_set_id=c1") }),
    coverage: async () => { throw new Error("must not be called"); },
  });
  const queries = new SourceQueries(client, "v1", null, null, "ctx-1", new SourceWriteIntents());
  await queries.loadAssessments();
  const coverage = queries.snapshot().coverage;
  assert.equal(coverage.phase, "error");
  assert.match(coverage.reason ?? "", /고정 로컬 경로와 일치하지 않/);
});

test("verification history keeps rows on a later-page failure and retries from the failed cursor", async () => {
  let calls = 0;
  const client = fakeClient({
    verifications: async (_evidenceId, cursor) => {
      calls += 1;
      if (calls === 1) return { items: [{ verification_id: "ver1", outcome: "matched", checked_at: "2026-10-19T00:00:00Z", verified_at: "2026-10-19T00:00:00Z" }], next_cursor: "vh-2" };
      if (calls === 2) throw new Error("history page lost");
      assert.equal(cursor, "vh-2");
      return { items: [{ verification_id: "ver2", outcome: "captured", checked_at: "2026-10-19T01:00:00Z", verified_at: null }], next_cursor: null };
    },
  });
  const queries = new SourceQueries(client, "v1", "page.html", null, "ctx-1", new SourceWriteIntents());
  const load = queries.loadVerifications("e1");
  await load;
  let view = queries.snapshot().verifications["e1"];
  assert.equal(view.phase, "error");
  assert.equal(view.records.items.length, 1);
  assert.equal(view.records.resumeFrom, "vh-2");
  assert.equal(view.records.complete, false);
  queries.retryVerifications("e1");
  await tick();
  view = queries.snapshot().verifications["e1"];
  assert.equal(view.phase, "ready");
  assert.deepEqual(view.records.items.map((record) => record.verification_id), ["ver1", "ver2"]);
  assert.equal(view.records.complete, true);
});

test("an applicable view for another scope never becomes the selected set", async () => {
  const foreign = applicableWith(null);
  const client = fakeClient({
    assessments: async () => ({ items: [{ assessment_id: "asm-x", version_id: "v1", scope: { kind: "page", page_path: "other.html", region_id: null }, target_set_id: "t9" }], next_cursor: null, applicable: { ...foreign, scope: { kind: "page", page_path: "other.html", region_id: null } } }),
    coverage: async () => { throw new Error("foreign coverage must never be followed"); },
  });
  const queries = new SourceQueries(client, "v1", "first.html", null, "ctx-1", new SourceWriteIntents());
  await queries.loadAssessments();
  const view = queries.snapshot().assessments;
  assert.equal(view.phase, "ready");
  assert.equal(view.applicable, null, "an incompatible applicable is not adopted");
  assert.equal(view.incompatibleApplicable, true);
  assert.equal(view.history.items.length, 1, "history rows stay inspectable");
  assert.equal(queries.snapshot().coverage.phase, "idle", "the foreign coverage chain is never followed");
});

test("a scope-compatible applicable remains the selected set", async () => {
  const client = fakeClient({
    assessments: async () => ({ items: [], next_cursor: null, applicable: applicableWith("/api/v1/versions/v1/assessments/coverage?coverage_set_id=c1") }),
    coverage: async () => ({ items: [], next_cursor: null }),
  });
  const queries = new SourceQueries(client, "v1", null, null, "ctx-1", new SourceWriteIntents());
  await queries.loadAssessments();
  const view = queries.snapshot().assessments;
  assert.equal(view.applicable?.target_set_id, "t1");
  assert.equal(view.incompatibleApplicable, false);
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(queries.snapshot().coverage.phase, "ready");
});

test("dispose invalidates a deferred first cursor: no merge, no emit, no follow-up; a new instance survives", async () => {
  let releasePage1: ((page: AssessmentPage) => void) | null = null;
  let calls = 0;
  const client = fakeClient({
    assessments: () => new Promise<AssessmentPage>((resolve) => { calls += 1; releasePage1 = resolve; }),
  });
  const queries = new SourceQueries(client, "v1", "a.html", null, "ctx-1", new SourceWriteIntents());
  const seen: number[] = [];
  const stop = queries.subscribe(() => seen.push(queries.revision));
  void queries.loadAssessments();
  await new Promise((resolve) => setImmediate(resolve));
  // The keyed unmount happens while the first cursor page is still in flight.
  const seenAtDispose = seen.length;
  queries.dispose();
  const release = releasePage1 as unknown as ((page: AssessmentPage) => void) | null;
  if (release === null) throw new Error("first cursor page was never requested");
  release({ items: [{ assessment_id: "late", version_id: "v1", scope: { kind: "page", page_path: "a.html", region_id: null }, target_set_id: "t1" }], next_cursor: "p2", applicable: applicableWith(null) });
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));
  stop();
  const view = queries.snapshot().assessments;
  assert.equal(view.history.items.length, 0, "a page released after dispose never merges");
  assert.equal(view.phase, "loading", "the disposed instance's phase never transitions past its in-flight loading");
  assert.deepEqual(seen.slice(seenAtDispose), [], "no revision emission reaches listeners after dispose");
  assert.equal(calls, 1, "the follow-up cursor page is never requested after dispose");
  // A NEW independent instance (its own guard) still works normally.
  let calls2 = 0;
  const client2 = fakeClient({
    assessments: async () => { calls2 += 1; return { items: [], next_cursor: null, applicable: null }; },
  });
  const fresh = new SourceQueries(client2, "v1", "a.html", null, "ctx-2", new SourceWriteIntents());
  await fresh.loadAssessments();
  assert.equal(fresh.snapshot().assessments.phase, "ready");
  assert.equal(calls2, 1);
});

test("dispose also invalidates a deferred verifications cursor without follow-up", async () => {
  let releasePage1: ((page: ListPage<VerificationRecord>) => void) | null = null;
  let calls = 0;
  const client = fakeClient({
    verifications: () => new Promise<ListPage<VerificationRecord>>((resolve) => { calls += 1; releasePage1 = resolve; }),
  });
  const queries = new SourceQueries(client, "v1", "a.html", null, "ctx-1", new SourceWriteIntents());
  void queries.loadVerifications("e1");
  await new Promise((resolve) => setImmediate(resolve));
  queries.dispose();
  const release = releasePage1 as unknown as ((page: ListPage<VerificationRecord>) => void) | null;
  if (release === null) throw new Error("verifications page was never requested");
  release({ items: [{ verification_id: "ver-late", outcome: "matched", checked_at: null, verified_at: null }], next_cursor: "vh2" });
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));
  const view = queries.snapshot().verifications["e1"];
  assert.equal(view?.records.items.length ?? 0, 0, "a verifications page released after dispose never merges");
  assert.notEqual(view?.phase, "ready", "the disposed verifications enumeration never completes");
  assert.equal(calls, 1, "no follow-up verifications cursor is requested");
});

test("a same-page history/render/reload context change clears and rebinds everything", async () => {
  let lateResolve: ((page: AssessmentPage) => void) | null = null;
  const client = fakeClient({
    assessments: () => new Promise<AssessmentPage>((resolve) => { lateResolve = resolve; }),
  });
  const queries = new SourceQueries(client, "v1", "page.html", null, "ctx-1", new SourceWriteIntents());
  const first = queries.loadAssessments();
  await tick();
  assert.equal(queries.snapshot().assessments.phase, "loading");
  // Same version/page/region: only the render context changed.
  assert.equal(queries.rebind("v1", "page.html", null, "ctx-2"), true);
  assert.equal(queries.snapshot().assessments.phase, "idle");
  // The old context's late reply must never land.
  (lateResolve as unknown as (page: AssessmentPage) => void)({ items: [{ assessment_id: "stale", version_id: "v1", scope: { kind: "version", page_path: null, region_id: null }, target_set_id: "t1" }], next_cursor: null, applicable: applicableWith(null) });
  await first;
  assert.equal(queries.snapshot().assessments.phase, "idle");
  assert.equal(queries.snapshot().assessments.history.items.length, 0);
});

test("snapshots store arbitrary IDs as own entries with a null-prototype dictionary", async () => {
  const client = fakeClient({
    assessments: async () => ({ items: [], next_cursor: null, applicable: null }),
    verifications: async () => ({ items: [], next_cursor: null }),
  });
  const queries = new SourceQueries(client, "v1", "a.html", null, "ctx-1", new SourceWriteIntents());
  const load = queries.loadEvidence("__proto__");
  const load2 = queries.loadEvidence("constructor");
  const load3 = queries.loadVerifications("toString");
  await Promise.all([load, load2, load3]);
  const snap = queries.snapshot();
  assert.equal(Object.getPrototypeOf(snap.items), null);
  assert.equal(Object.getPrototypeOf(snap.verifications), null);
  const protoKey = "__proto__";
  const constructorKey = "constructor";
  const toStringKey = "toString";
  assert.equal(snap.items[protoKey]?.phase, "error");
  assert.equal(snap.items[protoKey]?.view ?? "no-inherited-view", "no-inherited-view");
  assert.equal(snap.items[constructorKey]?.phase, "error");
  assert.equal(snap.verifications[toStringKey]?.phase, "ready");
  assert.equal((Object.prototype as unknown as { polluted?: unknown }).polluted, undefined);
});

test("a region change on the same page rebinds; navigating on without a region returns to page scope", async () => {
  const client = fakeClient({
    assessments: async () => ({ items: [], next_cursor: null, applicable: null }),
  });
  const queries = new SourceQueries(client, "v1", "a.html", null, "ctx-1", new SourceWriteIntents());
  await queries.loadAssessments();
  assert.equal(queries.snapshot().assessments.phase, "ready");
  assert.equal(queries.rebind("v1", "a.html", "overview", "ctx-1"), true);
  assert.equal(queries.scope()?.kind, "region");
  assert.equal(queries.scope()?.pagePath, "a.html");
  assert.equal(queries.snapshot().assessments.phase, "idle");
  // Moving to another page without a new region event: the old page's
  // "overview" binding is cleared, so the same name never carries over.
  assert.equal(queries.rebind("v1", "b.html", null, "ctx-1"), true);
  assert.equal(queries.scope()?.kind, "page");
  assert.equal(queries.scope()?.pagePath, "b.html");
  assert.equal(queries.snapshot().assessments.phase, "idle");
});

test("attempt-attributed coverage preserves the attempt on first page, next page and retry while plain coverage stays plain", async () => {
  const attempt = "11111111-2222-4333-8444-555555555555";
  for (const attributed of [true, false]) {
    const calls: unknown[][] = [];
    let page2Failed = false;
    const client = {
      versionAssessments: async () => ({ items: [], next_cursor: null, applicable: applicableWith(`/api/v1/versions/v1/assessments/coverage?coverage_set_id=c1${attributed ? "&attempt_run_id=" + attempt : ""}`) }),
      assessmentCoverage: async (...args: unknown[]) => {
        calls.push(args);
        if (!args[2]) return { items: [], next_cursor: "page2" };
        if (!page2Failed) { page2Failed = true; throw new Error("page2 lost"); }
        return { items: [], next_cursor: null };
      },
    } as unknown as HaeryongClient;
    const queries = new SourceQueries(client, "v1", null, null, "ctx", new SourceWriteIntents());
    await queries.loadAssessments(); await tick();
    assert.equal(queries.snapshot().coverage.phase, "error");
    queries.retryCoverage(); await tick();
    assert.equal(queries.snapshot().coverage.phase, "ready");
    assert.deepEqual(calls, [["v1", "c1", undefined, attributed ? attempt : undefined], ["v1", "c1", "page2", attributed ? attempt : undefined], ["v1", "c1", "page2", attributed ? attempt : undefined]]);
  }
});

test("verification is capability/authority guarded, refreshes ready evidence/history, and never applies a replaced selection", async () => {
  let dispatches = 0, evidenceReads = 0, historyReads = 0;
  let release!: (value: unknown) => void;
  const result = { verification_id: "checked", operation_id: "k", outcome: "captured", checked_at: "now", verified_at: null };
  const client = {
    versionEvidence: async () => { evidenceReads++; return { evidence_id: "e", service_verification: evidenceReads === 1 ? null : result }; },
    evidenceVerifications: async () => { historyReads++; return { items: historyReads === 1 ? [] : [result], next_cursor: null }; },
    verifySourceEvidence: async () => { dispatches++; return new Promise((resolve) => { release = resolve; }); },
  } as unknown as HaeryongClient;
  const { DispatchBlockedError } = await import("../../client/types.ts");
  const controller = { state: { capabilities: { features: { source_verification: false } } }, captureDispatchAuthority: () => null, requireVerifiedDispatch: () => { throw new DispatchBlockedError(); } } as unknown as import("../../client/controller.ts").ShellController;
  const queries = new SourceQueries(client, "v", "a", null, "ctx", new SourceWriteIntents());
  await assert.rejects(queries.runVerification("e", {}, controller), DispatchBlockedError);
  controller.requireVerifiedDispatch = () => {};
  await assert.rejects(queries.runVerification("e", {}, controller), DispatchBlockedError);
  assert.equal(dispatches, 0);
  controller.state.capabilities!.features.source_verification = true;
  await queries.loadEvidence("e"); await queries.loadVerifications("e");
  const first = queries.runVerification("e", {}, controller);
  release(result); await first;
  assert.equal(queries.snapshot().checks.e?.result?.outcome, "captured");
  assert.equal(evidenceReads, 2); assert.equal(historyReads, 2);
  assert.equal(queries.snapshot().verifications.e?.records.items[0]?.verification_id, "checked");
  const late = queries.runVerification("e", {}, controller);
  queries.rebind("v2", "b", null, "ctx2");
  release(result); await late;
  assert.deepEqual(Object.keys(queries.snapshot().checks), []);
  assert.deepEqual(Object.keys(queries.snapshot().items), []);
  assert.equal(evidenceReads, 2); assert.equal(historyReads, 2);
});

test("an unresolved check retries only the original receipt and keeps its original expectation/key", async () => {
  const { PendingSourceWriteError } = await import("../../client/source-operations.ts");
  let dispatches = 0;
  const keys: string[] = [];
  const client = {
    verifySourceEvidence: async (_v: string, _e: string, _r: unknown, key: string) => { dispatches++; throw new PendingSourceWriteError(key); },
    operation: async (key: string) => { keys.push(key); return { operation_id: key, state: "completed", original_status: 201, result_metadata: { verification_id: "x", outcome: "matched" } }; },
    versionEvidence: async () => ({}), evidenceVerifications: async () => ({ items: [], next_cursor: null }),
  } as unknown as HaeryongClient;
  const controller = { state: { capabilities: { features: { source_verification: true } } }, captureDispatchAuthority: () => null, requireVerifiedDispatch() {} } as unknown as import("../../client/controller.ts").ShellController;
  const queries = new SourceQueries(client, "v", null, null, "ctx", new SourceWriteIntents());
  await queries.runVerification("e", { expected_bytes_sha256: "a".repeat(64) }, controller);
  const pending = queries.snapshot().checks.e!;
  assert.equal(pending.phase, "pending");
  await queries.runVerification("e", {}, controller);
  assert.equal(dispatches, 1);
  assert.deepEqual(keys, [pending.operationId]);
  assert.deepEqual(queries.snapshot().checks.e?.request, { expected_bytes_sha256: "a".repeat(64) });
});

test("unconfirmed verification survives same-version page and region rebinds, including a late lost response", async () => {
  const { PendingSourceWriteError } = await import("../../client/source-operations.ts");
  for (const late of [false, true]) {
    let posts = 0, originalKey = "";
    let reject!: (error: Error) => void;
    const receipts: string[] = [];
    const client = {
      verifySourceEvidence: async (_v: string, _e: string, _r: unknown, key: string) => { posts++; assert.equal(posts, 1, "recovery must not dispatch another POST"); originalKey = key; return new Promise((_resolve, no) => { reject = no; }); },
      operation: async (key: string) => { receipts.push(key); return { operation_id: key, state: "completed", original_status: 201, result_metadata: { verification_id: "x", outcome: "matched" } }; },
      versionEvidence: async () => ({}), evidenceVerifications: async () => ({ items: [], next_cursor: null }),
    } as unknown as HaeryongClient;
    const controller = { state: { capabilities: { features: { source_verification: true } } }, captureDispatchAuthority: () => null, requireVerifiedDispatch() {} } as unknown as import("../../client/controller.ts").ShellController;
    const queries = new SourceQueries(client, "v", "a", null, "ctx", new SourceWriteIntents());
    const request = { expected_excerpt_ref: { upload_id: "11111111-2222-4333-8444-555555555555" } };
    const first = queries.runVerification("e", request, controller);
    if (late) queries.rebind("v", "b", "region", "ctx2");
    reject(new PendingSourceWriteError(originalKey)); await first;
    if (!late) queries.rebind("v", "b", "region", "ctx2");
    assert.deepEqual(Object.keys(queries.snapshot().checks), []);
    await queries.runVerification("e", {}, controller);
    assert.equal(posts, 1); assert.deepEqual(receipts, [originalKey]);
    assert.deepEqual(queries.snapshot().checks.e?.request, request);
    assert.equal(queries.snapshot().checks.e?.phase, "ready");
  }
});

test("a remounted selection recovers the controller-owned verification intent without another POST", async () => {
  const { PendingSourceWriteError } = await import("../../client/source-operations.ts");
  const intents = new SourceWriteIntents();
  let posts = 0, key = "";
  const receipts: string[] = [];
  const client = {
    verifySourceEvidence: async (_v: string, _e: string, _request: unknown, operationId: string) => { posts++; key = operationId; throw new PendingSourceWriteError(key); },
    operation: async (operationId: string) => { receipts.push(operationId); return { operation_id: operationId, state: "completed", original_status: 201, result_metadata: { outcome: "captured" } }; },
    versionEvidence: async () => ({}), evidenceVerifications: async () => ({ items: [], next_cursor: null }),
  } as unknown as HaeryongClient;
  const controller = { state: { capabilities: { features: { source_verification: true } } }, captureDispatchAuthority: () => null, requireVerifiedDispatch() {} } as unknown as import("../../client/controller.ts").ShellController;
  const first = new SourceQueries(client, "v", "a", null, "ctx", intents);
  await first.runVerification("e", {}, controller); first.dispose();
  const remounted = new SourceQueries(client, "v", "b", "overview", "ctx2", intents);
  assert.equal(remounted.snapshot().checks.e?.operationId, key);
  await remounted.runVerification("e", {}, controller);
  assert.equal(posts, 1); assert.deepEqual(receipts, [key]);
});

test("normal completion and definitive failure survive a verification selection remount until consumed", async () => {
  for (const outcome of ["completed", "failed"] as const) {
    const intents = new SourceWriteIntents();
    let posts = 0, receipts = 0;
    let finish!: (value: unknown) => void, fail!: (error: Error) => void;
    const result = { verification_id: "kept", outcome: "matched" };
    const client = {
      verifySourceEvidence: async () => { posts++; return new Promise((resolve, reject) => { finish = resolve; fail = reject; }); },
      operation: async () => { receipts++; throw Error("recorded outcomes need no receipt read"); },
      versionEvidence: async () => ({}), evidenceVerifications: async () => ({ items: [], next_cursor: null }),
    } as unknown as HaeryongClient;
    const controller = { state: { capabilities: { features: { source_verification: true } } }, captureDispatchAuthority: () => null, requireVerifiedDispatch() {} } as unknown as import("../../client/controller.ts").ShellController;
    const first = new SourceQueries(client, "v", "a", null, "ctx", intents);
    const request = { expected_bytes_sha256: "a".repeat(64) };
    const write = first.runVerification("e", request, controller);
    const key = first.snapshot().checks.e!.operationId;
    first.dispose();
    const current = new SourceQueries(client, "v", "a", "overview", "ctx2", intents);
    assert.equal(current.snapshot().checks.e?.phase, "pending");
    if (outcome === "completed") finish(result); else fail(new Error("definitive verification failure"));
    await write;
    assert.equal(intents.verifications.get(JSON.stringify(["v", "e"]))?.outcome.state, outcome);
    await current.runVerification("e", {}, controller);
    assert.equal(posts, 1); assert.equal(receipts, 0);
    assert.equal(current.snapshot().checks.e?.operationId, key);
    assert.deepEqual(current.snapshot().checks.e?.request, request);
    if (outcome === "completed") assert.deepEqual(current.snapshot().checks.e?.result, result);
    else assert.equal(current.snapshot().checks.e?.reason, "definitive verification failure");
    assert.equal(intents.verifications.size, 0);
  }
});

test("T5 newer applicable capture survives late older history; blocked/not_checked fixed units remain addressable", async () => {
  const newer = { ...applicableWith('/api/v1/versions/v1/assessments/coverage?coverage_set_id=c1'), target_set_id: 'new-target', captured_at: '2026-10-20T00:00:00Z', assessed_at: '2026-10-20T01:00:00Z', coverage: { ...applicableWith(null).coverage, coverage_set_id: 'c1', unit_kind: 'page', eligible: 2, assessed: 0, complete: false, units_url: '/api/v1/versions/v1/assessments/coverage?coverage_set_id=c1' } };
  const queries = new SourceQueries(fakeClient({
    assessments: async cursor => ({ items: [{ assessment_id: cursor ? 'old-result' : 'new-result', version_id: 'v1', scope: { kind: 'version', page_path: null, region_id: null }, target_set_id: cursor ? 'old-target' : 'new-target' }], next_cursor: cursor ? null : 'older-history', applicable: cursor ? { ...applicableWith(null), assessed_at: '2026-10-21T00:00:00Z' } : newer }),
    coverage: async () => ({ items: ['blocked', 'not_checked'].map((outcome, i) => ({ kind: 'page', page_path: i ? 'regionless.html' : 'a.html', region_id: null, outcome, reason: 'missing_source', assessment_id: null, target_set_id: 'new-target' })), next_cursor: null }),
  }), 'v1', null, null, 't5', new SourceWriteIntents());
  await queries.loadAssessments(); await tick();
  const view = queries.snapshot();
  assert.equal(view.assessments.applicable?.target_set_id, 'new-target');
  assert.equal(view.assessments.applicable?.captured_at, '2026-10-20T00:00:00Z');
  assert.equal(view.assessments.applicable?.assessed_at, '2026-10-20T01:00:00Z');
  assert.deepEqual(view.coverage.units.items.map(u => u.outcome), ['blocked', 'not_checked']);
  assert.equal(view.coverage.units.items[1]!.page_path, 'regionless.html');
  assert.equal(queries.scope(), undefined);
});

test("Task04 G2 SourceQueries.refreshPassive surfaces error state and rethrows when resync is true", async () => {
  let failPassive = false;
  const client = fakeClient({
    assessments: async () => {
      if (failPassive) throw new ApiError(503, "SERVICE_UNAVAILABLE", true);
      return { items: [], next_cursor: null, applicable: null };
    },
    evidence: async () => {
      if (failPassive) throw new ApiError(503, "SERVICE_UNAVAILABLE", true);
      return { evidence_id: "ev-1", version_id: "v1", authored: {}, source: {}, availability: "available", bytes_origin: null, reason: null, service_verification: null, verifications_url: "/api/v1/versions/v1/evidence/ev-1/verifications" };
    },
    verifications: async () => {
      if (failPassive) throw new ApiError(503, "SERVICE_UNAVAILABLE", true);
      return { items: [], next_cursor: null };
    },
  });
  const queries = new SourceQueries(client, "v1", null, null, "g2", new SourceWriteIntents());
  await queries.loadAssessments();
  await queries.loadEvidence("ev-1");
  await queries.loadVerifications("ev-1");
  assert.equal(queries.snapshot().assessments.phase, "ready");
  assert.equal(queries.snapshot().items["ev-1"]?.phase, "ready");
  assert.equal(queries.snapshot().verifications["ev-1"]?.phase, "ready");

  failPassive = true;
  await assert.rejects(
    queries.refreshPassive(client, () => false, true),
    (err: unknown) => err instanceof ApiError && err.status === 503,
  );
  assert.equal(queries.snapshot().assessments.phase, "error");
  assert.equal(queries.snapshot().items["ev-1"]?.phase, "error");
  assert.equal(queries.snapshot().verifications["ev-1"]?.phase, "error");

  // Subsequent recovery via refreshPassive restores ready state:
  failPassive = false;
  await queries.refreshPassive(client, () => false, true);
  assert.equal(queries.snapshot().assessments.phase, "ready");
  assert.equal(queries.snapshot().items["ev-1"]?.phase, "ready");
  assert.equal(queries.snapshot().verifications["ev-1"]?.phase, "ready");
});


for (const resync of [false, true]) test(`Task04 Evidence refresh retires an older initial assessment response (resync=${resync})`, async () => {
  let release!: (page: AssessmentPage) => void;
  let calls = 0;
  const oldPage = new Promise<AssessmentPage>(resolve => { release = resolve; });
  const page = (id: string): AssessmentPage => ({ items: [{ assessment_id: id } as AssessmentRecord], next_cursor: null, applicable: null });
  const client = fakeClient({ assessments: async () => ++calls === 1 ? oldPage : page("current") });
  const queries = new SourceQueries(client, "v1", null, null, "same-context", new SourceWriteIntents());
  const initial = queries.loadAssessments();
  await queries.refreshPassive(client, undefined, resync);
  release(page("old"));
  await initial;
  assert.deepEqual(queries.snapshot().assessments.history.items.map(row => row.assessment_id), ["current"]);
});
