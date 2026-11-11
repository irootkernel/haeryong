import test from "node:test";
import assert from "node:assert/strict";
import { ApiError, DispatchBlockedError, NetworkError, StaleResponseError } from "../../client/types.ts";
import type { ApplicableFreshness, AssessmentRecord, AssessmentsView, FindingView, HaeryongClient, JobView, ListPage } from "../../client/types.ts";
import type { DispatchAuthority } from "../../client/controller.ts";
import { exactReaderLink, FINDING_GROUPS, findingScope, freshnessProposal, MaintenanceFreshnessState, MaintenanceState, scanContext } from "./maintenance-state.ts";
import type { MaintenanceController } from "./maintenance-state.ts";

const DOC = "11111111-1111-4111-8111-111111111111";
const VERSION = "22222222-2222-4222-8222-222222222222";
const scope = { kind: "page" as const, page_path: "nested/page.html", region_id: null };
const job = (id = "scan"): JobView => ({ job_id: id, kind: "scan", state: "completed", parent_job_id: "", upload_id: "", cancel_requested: false, error: "", revision: 1 });
const finding = (id = "f1", overrides: Partial<FindingView> = {}): FindingView => ({
  finding_id: id, job_id: "scan", kind: "related_explanation", origin: "ai", project_id: "project", document_id: DOC, version_id: VERSION,
  page_path: null, region_id: null, target_set_id: "target", evidence: { retained: "<script>read only</script>" },
  evidence_fingerprint: "fingerprint", rationale: "mechanism, not a dependency", uncertainty: "source unverified",
  captured_config_revision: 4, permissions_revision: 2, proposal: {}, status: "proposed", approval_required: true,
  applied_ref: null, revision: 1, etag: "finding-etag", created_at: "created", updated_at: "updated", ...overrides,
});
const applicable = (overrides: Partial<ApplicableFreshness> = {}): ApplicableFreshness => ({
  source_intent: "historical", target_set_id: "target", captured_at: "capture-time", assessed_at: "assessment-time", scope,
  status: "aligned_within_scope", reassessment_required: true,
  coverage: { coverage_set_id: "coverage", unit_kind: "page", eligible: 2, assessed: 1, complete: false,
    units_url: `/api/v1/versions/${VERSION}/assessments/coverage?coverage_set_id=coverage` }, ...overrides,
});
const historyRecord = (id: string): AssessmentRecord => ({ assessment_id: id, version_id: VERSION, target_set_id: "target", scope, state: "not_assessed" });
function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
/** These are offline unit fakes, never retained provider/owner-approval evidence. */
function harness(active: Partial<HaeryongClient> = {}, passive: Partial<HaeryongClient> = {}) {
  let generation = 0, nonce = 1, phase = "verified";
  const listeners = new Set<() => void>();
  let calls = 0;
  const base: Partial<HaeryongClient> = {
    jobsList: async () => ({ items: [job()], next_cursor: null }),
    jobGet: async id => job(id), jobFindingsList: async () => ({ items: [], next_cursor: null }),
    listProjects: async () => ({ items: [], next_cursor: null }),
    configurationView: async () => ({ config_revision: 4, etag: "config", configuration: { projects: {} } }),
    scanStart: async (_request, operation_id) => { calls++; return { job_id: "scan", operation_id, job_url: "/api/v1/jobs/scan", target_set_id: null, coalesced: true }; },
    versionAssessments: async () => ({ items: [], next_cursor: null, applicable: null }),
  };
  const client = { ...base, ...active } as HaeryongClient;
  const passiveClient = { ...base, ...passive } as HaeryongClient;
  const c = {
    client, passiveClient,
    get readGeneration() { return generation; },
    get state() { return { phase, capabilities: { features: { scans: true } } }; },
    captureDispatchAuthority: () => phase === "verified" ? { nonce, libraryId: "library", runtimeGeneration: "runtime" } : null,
    requireVerifiedDispatch: (authority?: DispatchAuthority | null) => { if (phase !== "verified" || !authority || authority.nonce !== nonce) throw new DispatchBlockedError(); },
    isCurrentReadAuthority: (authority: DispatchAuthority | null, readGeneration?: number) => phase === "verified" && authority?.nonce === nonce && (readGeneration === undefined || readGeneration === generation),
    isCurrentDispatchAuthority: (authority: DispatchAuthority | null) => phase === "verified" && authority?.nonce === nonce,
    subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
  } as unknown as MaintenanceController;
  return { controller: c, get starts() { return calls; }, get subscriptions() { return listeners.size; },
    lose() { phase = "unauthenticated"; nonce++; for (const listener of [...listeners]) listener(); },
    bump() { generation++; }, replace() { nonce++; for (const listener of [...listeners]) listener(); } };
}

test("all actual typed groups remain distinct; content is a proposal group", () => {
  const kinds = FINDING_GROUPS.flatMap(group => [...group.kinds]);
  assert.equal(new Set(kinds).size, 6);
  assert.deepEqual(FINDING_GROUPS.find(group => group.id === "content")!.kinds, ["new_version_proposal", "new_document_proposal"]);
});

test("Assessment.state and proposal.scope win over null outer coordinates; no guessed version scope", () => {
  const row = finding("fresh", { kind: "freshness_assessment", proposal: {
    assessment_id: "assessment", version_id: VERSION, target_set_id: "target", scope,
    state: "not_assessed", assessed_at: "assessment-time", evidence: { units: [{ outcome: "partial" }] },
  } });
  const parsed = freshnessProposal(row)!;
  assert.deepEqual(parsed.scope, scope);
  assert.equal(parsed.state, "not_assessed");
  assert.equal("coverage" in parsed, false);
  assert.equal("captured_at" in parsed, false);
  assert.equal("reassessment_required" in parsed, false);
  assert.equal(freshnessProposal({ ...row, proposal: { ...row.proposal as object, scope: undefined } }), null);
  assert.equal(freshnessProposal({ ...row, proposal: { ...row.proposal as object, state: undefined, status: "aligned_within_scope" } }), null);
  assert.equal(freshnessProposal({ ...row, version_id: "different" }), null);
});

test("same region name is page-local; absent or malformed scope remains unknown", () => {
  assert.notDeepEqual(findingScope({ kind: "region", page_path: "one.html", region_id: "main" }), findingScope({ kind: "region", page_path: "two.html", region_id: "main" }));
  for (const value of [null, {}, { kind: "region", page_path: null, region_id: "main" }, { kind: "version", page_path: "one.html", region_id: null }]) assert.equal(findingScope(value), null);
});

test("Reader routes encode exact coordinates, retain valid filename characters and refuse foreign paths", () => {
  const url = exactReaderLink(DOC, VERSION, "하위/a\nfile.html", "main&other=1")!;
  const parsed = new URL(url, "http://local.invalid");
  assert.equal(parsed.searchParams.get("page"), "하위/a\nfile.html");
  assert.equal(new URLSearchParams(parsed.hash.slice(1)).get("region"), "main&other=1");
  for (const path of ["/outside.html", "../outside.html", "https://outside.invalid", "dir//page.html", "C:page.html"]) assert.equal(exactReaderLink(DOC, VERSION, path, null), null);
  assert.equal(exactReaderLink("../api", VERSION, null, null), null);
  assert.equal(exactReaderLink(DOC, VERSION, null, "main"), null);
});

test("scan source records are exact job context, not an assessment target/capture join", () => {
  const record = { ...job(), result: { job_id: "scan", requested_scope: { source_mode: "pinned" }, source_targets: [{ mode: "pinned", captured_at: "observation-time" }] } };
  assert.equal(scanContext(record, finding())!.requested!.source_mode, "pinned");
  assert.equal(scanContext(record, finding())!.requested!.source_intent, undefined);
  assert.equal(scanContext(record, finding("other", { job_id: "different" })), null);
  assert.equal(scanContext({ ...record, result: { job_id: "different" } }), null);
});

test("mount/state construction never starts a scan or reads a provider/source", async () => {
  const h = harness(); const model = new MaintenanceState(h.controller);
  assert.equal(h.subscriptions, 0);
  model.connect(); await model.refresh(); await model.loadScanOptions();
  assert.equal(h.starts, 0); assert.equal(model.jobs.state.items.length, 1);
  model.dispose(); assert.equal(h.subscriptions, 0);
});

test("failed later finding page retains earlier rows/cursor; retry uses same cursor exactly once", async () => {
  const cursors: Array<string | undefined> = []; let fail = true;
  const h = harness({ jobFindingsList: async (_id, filter) => {
    cursors.push(filter?.cursor);
    if (!filter?.cursor) return { items: [finding()], next_cursor: "second" };
    if (fail) { fail = false; throw new NetworkError(); }
    return { items: [finding("f2")], next_cursor: null };
  } });
  const model = new MaintenanceState(h.controller, "scan");
  await model.findings.more(); await model.findings.more();
  assert.deepEqual(model.findings.state.items.map(row => row.finding_id), ["f1"]);
  assert.equal(model.findings.state.nextCursor, "second"); assert.ok(model.findings.state.error);
  await model.findings.more();
  assert.deepEqual(cursors, [undefined, "second", "second"]);
  assert.deepEqual(model.findings.state.items.map(row => row.finding_id), ["f1", "f2"]);
  assert.equal(model.findings.state.complete, true); model.dispose();
});

test("empty page with next cursor is incomplete, repeated cursor/duplicate record is an error", async () => {
  let page = 0;
  const h = harness({ jobFindingsList: async () => ++page === 1 ? { items: [], next_cursor: "next" } : { items: [finding()], next_cursor: "next" } });
  const model = new MaintenanceState(h.controller, "scan");
  await model.findings.more(); assert.equal(model.findings.state.complete, false);
  await model.findings.more(); assert.ok(model.findings.state.error); assert.equal(model.findings.state.items.length, 0);
  model.dispose();
});

test("filter changes retire a pending old page without leaking rows or hiding current loading", async () => {
  const old = deferred<ListPage<FindingView>>();
  const h = harness({ jobFindingsList: async (_id, filter) => filter?.kind === "topic_assignment" ? { items: [finding("topic", { kind: "topic_assignment" })], next_cursor: null } : old.promise });
  const model = new MaintenanceState(h.controller, "scan");
  const first = model.findings.more(); await model.setFilter({ kind: "topic_assignment" });
  old.resolve({ items: [finding("late")], next_cursor: null }); await first;
  assert.deepEqual(model.findings.state.items.map(row => row.finding_id), ["topic"]);
  assert.equal(model.findings.state.loading, false); model.dispose();
});

test("disposed job lifetime cannot emit or resurrect protected rows", async () => {
  const pending = deferred<ListPage<FindingView>>(); const h = harness({ jobFindingsList: () => pending.promise });
  const old = new MaintenanceState(h.controller, "old"); let emits = 0; old.subscribe(() => emits++);
  const request = old.findings.more(); old.dispose(); const atDispose = emits;
  pending.resolve({ items: [finding("late", { job_id: "old" })], next_cursor: null }); await request;
  assert.equal(emits, atDispose); assert.equal(old.findings.state.items.length, 0);
});

test("readGeneration change rejects a late response and required resync remains unconfirmed", async () => {
  const pending = deferred<ListPage<FindingView>>(); const h = harness({ jobFindingsList: () => pending.promise });
  const model = new MaintenanceState(h.controller, "scan");
  const request = model.findings.refresh(false, true); h.bump(); pending.resolve({ items: [finding()], next_cursor: null });
  await assert.rejects(request, StaleResponseError);
  assert.equal(model.findings.state.items.length, 0); assert.equal(model.findings.state.loading, false); model.dispose();
});

test("authority loss clears job/config/findings/accepted state and late replies stay cleared", async () => {
  const pending = deferred<ListPage<FindingView>>(); let call = 0;
  const h = harness({ jobFindingsList: () => ++call === 1 ? Promise.resolve({ items: [finding()], next_cursor: null }) : pending.promise });
  const model = new MaintenanceState(h.controller, "scan"); model.connect();
  await model.refresh(); await model.loadScanOptions();
  const late = model.findings.refresh(); h.lose(); pending.resolve({ items: [finding("late")], next_cursor: null }); await late;
  const state = model.snapshot(); assert.equal(state.job, null); assert.equal(state.configuration, null);
  assert.deepEqual(state.findings.items, []); assert.equal(state.accessLost, true); model.dispose();
});

test("401/403/404 removes protected rows instead of preserving a forbidden page", async () => {
  for (const status of [401, 403, 404]) {
    let first = true; const h = harness({ jobFindingsList: async () => { if (first) { first = false; return { items: [finding()], next_cursor: "next" }; } throw new ApiError(status, "DENIED", false); } });
    const model = new MaintenanceState(h.controller, "scan"); await model.findings.more(); await model.findings.more();
    assert.deepEqual(model.findings.state.items, []); assert.equal(model.accessLost, true); model.dispose();
  }
});

test("passive invalidation refreshes the loaded window using only passive client", async () => {
  const passiveCursors: Array<string | undefined> = [];
  const page = (cursor?: string) => ({ items: [finding(cursor ? "f2" : "f1")], next_cursor: cursor ? null : "next" });
  const h = harness({ jobFindingsList: async (_id, filter) => page(filter?.cursor) }, { jobFindingsList: async (_id, filter) => { passiveCursors.push(filter?.cursor); return page(filter?.cursor); } });
  const model = new MaintenanceState(h.controller, "scan"); await model.findings.more(); await model.findings.more();
  await model.refresh(true, true); assert.deepEqual(passiveCursors, [undefined, "next"]);
  assert.equal(model.findings.state.items.length, 2); assert.equal(h.starts, 0); model.dispose();
});

test("failed passive window refresh preserves loaded rows and propagates required failure", async () => {
  const h = harness({ jobFindingsList: async () => ({ items: [finding()], next_cursor: null }) }, { jobFindingsList: async () => { throw new NetworkError(); } });
  const model = new MaintenanceState(h.controller, "scan"); await model.findings.more();
  await assert.rejects(model.refresh(true, true), NetworkError);
  assert.equal(model.findings.state.items[0]!.finding_id, "f1"); assert.ok(model.findings.state.error); model.dispose();
});

test("completed window with failed refresh still has an explicit refresh retry", async () => {
  let failed = true;
  const h = harness({ jobFindingsList: async () => ({ items: [finding("current")], next_cursor: null }) }, { jobFindingsList: async () => { if (failed) { failed = false; throw new NetworkError(); } return { items: [finding("unused")], next_cursor: null }; } });
  const model = new MaintenanceState(h.controller, "scan"); await model.findings.more();
  await model.findings.refresh(true); assert.ok(model.findings.state.error); assert.equal(model.findings.state.complete, true);
  await model.findings.more(); assert.equal(model.findings.state.error, null); assert.equal(model.findings.state.items[0]!.finding_id, "current"); model.dispose();
});

test("effect cleanup/reconnect retires prior requests without duplicating controller subscriptions", async () => {
  const h = harness(); const model = new MaintenanceState(h.controller); model.connect(); model.connect();
  assert.equal(h.subscriptions, 1); model.dispose(); assert.equal(h.subscriptions, 0);
  model.connect(); await model.refresh(); assert.equal(h.subscriptions, 1); assert.equal(model.jobs.state.items.length, 1); model.dispose();
});

test("scan dispatch requires verified authority before a pending presentation or request", async () => {
  const h = harness(); const model = new MaintenanceState(h.controller); h.lose();
  await assert.rejects(model.startScan({ project_ids: ["project"] }, "key"), DispatchBlockedError);
  assert.equal(h.starts, 0); assert.equal(model.scanBusy, false); assert.equal(model.unconfirmedOperation, null); model.dispose();
});

test("explicit scan forwards exact request once; uncertain outcome recovery reads receipt before job and never repeats scan", async () => {
  const calls: string[] = []; const request = { scope: "selected_projects" as const, project_ids: ["project"], depth: "full_scope" as const, source_mode: "pinned" as const };
  const h = harness({ scanStart: async (actual, key) => { assert.deepEqual(actual, request); calls.push(`scan:${key}`); throw new NetworkError(); },
    operation: async key => { calls.push(`operation:${key}`); return { operation_id: key, state: "completed", job_id: "scan", original_status: 202, result_ref: null, result_metadata: null }; },
    jobGet: async id => { calls.push(`job:${id}`); return job(id); } });
  const model = new MaintenanceState(h.controller); await model.startScan(request, "key");
  assert.equal(model.unconfirmedOperation, "key"); assert.ok(model.scanError);
  await model.startScan(request, "other-key"); await model.recoverScan();
  assert.deepEqual(calls, ["scan:key", "operation:key", "job:scan"]);
  assert.equal(model.accepted!.job_id, "scan"); assert.equal(model.accepted!.coalesced, undefined);
  assert.equal(model.unconfirmedOperation, null); model.dispose();
});

test("published freshness starts empty and uses exact target/page query, separately from proposal", async () => {
  const calls: unknown[] = []; const h = harness({ versionAssessments: async (id, filter) => { calls.push([id, filter]); return { items: [historyRecord("published")], next_cursor: null, applicable: applicable() }; } });
  const model = new MaintenanceFreshnessState(h.controller, VERSION, scope, "target");
  assert.equal(model.applicable, null); await model.refresh();
  assert.deepEqual(calls, [[VERSION, { targetSetId: "target", cursor: undefined, scope: { kind: "page", pagePath: "nested/page.html", regionId: undefined } }]]);
  assert.equal(model.applicable!.coverage.complete, false); assert.equal(model.applicable!.reassessment_required, true);
  assert.equal(model.applicable!.captured_at, "capture-time"); assert.equal(model.applicable!.assessed_at, "assessment-time"); model.dispose();
});

test("same-scope older history response cannot regress newer applicable/history", async () => {
  const older = deferred<AssessmentsView>(); let n = 0;
  const h = harness({ versionAssessments: async () => ++n === 1 ? older.promise : { items: [historyRecord("new")], next_cursor: null, applicable: applicable({ assessed_at: "new-time" }) } });
  const model = new MaintenanceFreshnessState(h.controller, VERSION, scope, "target");
  const old = model.refresh(); await model.refresh(); older.resolve({ items: [historyRecord("old")], next_cursor: null, applicable: applicable({ assessed_at: "old-time" }) }); await old;
  assert.equal(model.applicable!.assessed_at, "new-time"); assert.deepEqual(model.history.state.items.map(r => r.assessment_id), ["new"]); model.dispose();
});

test("history cursor failure retains earlier published rows and explicit retry cursor", async () => {
  const cursors: unknown[] = []; let fail = true;
  const h = harness({ versionAssessments: async (_id, filter) => {
    cursors.push(filter?.cursor); if (!filter?.cursor) return { items: [historyRecord("first")], next_cursor: "next", applicable: applicable() };
    if (fail) { fail = false; throw new NetworkError(); }
    return { items: [historyRecord("second")], next_cursor: null, applicable: applicable() };
  } });
  const model = new MaintenanceFreshnessState(h.controller, VERSION, scope, "target");
  await model.history.more(); await model.history.more(); assert.equal(model.history.state.items.length, 1);
  await model.history.more(); assert.deepEqual(cursors, [undefined, "next", "next"]); assert.equal(model.history.state.items.length, 2); model.dispose();
});

test("mismatching published target/scope stays unavailable rather than borrowing a candidate", async () => {
  for (const value of [applicable({ target_set_id: "other" }), applicable({ scope: { kind: "version", page_path: null, region_id: null } })]) {
    const h = harness({ versionAssessments: async () => ({ items: [], next_cursor: null, applicable: value }) });
    const model = new MaintenanceFreshnessState(h.controller, VERSION, scope, "target"); await model.refresh();
    assert.equal(model.applicable, null); assert.ok(model.history.state.error); model.dispose();
  }
});

test("coverage never follows a foreign URL or invents units for absent locator", async () => {
  let reads = 0;
  const h = harness({ versionAssessments: async () => ({ items: [], next_cursor: null, applicable: applicable({ coverage: { ...applicable().coverage, units_url: "https://outside.invalid/capture" } }) }),
    assessmentCoverage: async () => { reads++; return { items: [], next_cursor: null }; } });
  const model = new MaintenanceFreshnessState(h.controller, VERSION, scope, "target"); await model.refresh(); await model.coverage.more();
  assert.equal(reads, 0); assert.deepEqual(model.coverage.state.items, []); assert.ok(model.coverage.state.error); model.dispose();
});

test("coverage later page failure/retry retains partial units and does not claim completeness", async () => {
  const cursors: unknown[] = []; let fail = true;
  const h = harness({ versionAssessments: async () => ({ items: [], next_cursor: null, applicable: applicable() }),
    assessmentCoverage: async (_id, _set, cursor) => {
      cursors.push(cursor);
      if (cursor && fail) { fail = false; throw new NetworkError(); }
      return { items: [{ kind: "page", page_path: cursor ? "two.html" : "one.html", region_id: null, outcome: cursor ? "not_checked" : "assessed", reason: cursor ? "scan_cancelled_before_unit_assessment" : null, assessment_id: cursor ? null : "assessment", target_set_id: "target" }], next_cursor: cursor ? null : "next" };
    } });
  const model = new MaintenanceFreshnessState(h.controller, VERSION, scope, "target"); await model.refresh(); await model.coverage.more(); await model.coverage.more();
  assert.equal(model.coverage.state.complete, false); assert.equal(model.coverage.state.items.length, 1);
  await model.coverage.more(); assert.deepEqual(cursors, [undefined, "next", "next"]);
  assert.equal(model.coverage.state.items[1]!.outcome, "not_checked"); assert.equal(model.coverage.state.complete, true); model.dispose();
});

test("duplicate required unit is an error, never a silently reduced denominator", async () => {
  const unit = { kind: "page", page_path: "one.html", region_id: null, outcome: "assessed", reason: null, assessment_id: "a", target_set_id: "target" };
  const h = harness({ versionAssessments: async () => ({ items: [], next_cursor: null, applicable: applicable() }), assessmentCoverage: async () => ({ items: [unit, unit], next_cursor: null }) });
  const model = new MaintenanceFreshnessState(h.controller, VERSION, scope, "target"); await model.refresh(); await model.coverage.more();
  assert.ok(model.coverage.state.error); assert.equal(model.coverage.state.complete, false); assert.deepEqual(model.coverage.state.items, []); model.dispose();
});

test("published data also clears on authority loss; disconnected late reply cannot restore it", async () => {
  const pending = deferred<AssessmentsView>(); const h = harness({ versionAssessments: () => pending.promise });
  const model = new MaintenanceFreshnessState(h.controller, VERSION, scope, "target"); model.connect();
  const request = model.refresh(); h.lose(); pending.resolve({ items: [historyRecord("late")], next_cursor: null, applicable: applicable() }); await request;
  assert.equal(model.applicable, null); assert.deepEqual(model.history.state.items, []); model.dispose();
});

test("foreign target history row does not merge even with a matching applicable badge", async () => {
  const h = harness({ versionAssessments: async () => ({ items: [{ ...historyRecord("wrong"), target_set_id: "other" }], next_cursor: null, applicable: applicable() }) });
  const model = new MaintenanceFreshnessState(h.controller, VERSION, scope, "target"); await model.refresh();
  assert.ok(model.history.state.error); assert.equal(model.applicable, null); assert.deepEqual(model.history.state.items, []); model.dispose();
});
