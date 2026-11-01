// NativePort contract tests. The fake port accepts only declared operation
// names; NativeHaeryongClient must forward exact typed payloads and propagate
// typed errors without inventing arbitrary URLs, paths or method names.
import test from "node:test";
import assert from "node:assert/strict";
import { NATIVE_OPERATIONS, NativeHaeryongClient, type NativePort } from "./native.ts";
import { ApiError, StaleResponseError, UncertainResponseError, type EventSubscription } from "./types.ts";

class RecordingPort {
  calls: Array<[string, Record<string, unknown>]> = [];
  handlers = new Map<string, (payload: Record<string, unknown>) => unknown>();

  async call(operation: string, payload: Record<string, unknown>): Promise<unknown> {
    if (!(NATIVE_OPERATIONS as readonly string[]).includes(operation)) {
      throw new Error("undeclared native operation: " + operation);
    }
    this.calls.push([operation, payload]);
    const handler = this.handlers.get(operation);
    if (!handler) throw new Error("no handler bound for " + operation);
    return handler(payload);
  }
}

function port(): RecordingPort {
  return new RecordingPort();
}

test("pair forwards an exact typed payload", async () => {
  const fake = port();
  fake.handlers.set("pair", () => ({ pairing_id: "p1", code: "123", polling_secret: "s" }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  await client.pair("Desktop");
  assert.deepEqual(fake.calls, [["pair", { device_label: "Desktop" }]]);
});

test("native pair/poll reduced Go-held shapes never carry a secret or bearer", async () => {
  const fake = port();
  fake.handlers.set("pair", () => ({ pairing_id: "p1", code: "123", expires_at: "2026-10-19T00:00:00Z" }));
  fake.handlers.set("pollPairing", () => ({
    state: "approved",
    session: { session_id: "s1", principal_id: "owner", library_id: "lib", device_label: "Desktop" },
  }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  const created = await client.pair("Desktop");
  assert.equal("polling_secret" in created, false);
  // The native poll names only the pairing it still owns: no secret member.
  const approved = await client.pollPairing("p1");
  assert.equal("token" in approved, false);
  assert.equal(approved.state, "approved");
  assert.deepEqual(fake.calls[1], ["pollPairing", { pairing_id: "p1" }]);
});

test("adoptPairing, abandonPairing, serviceDiscovery and startDaemonOffer forward exact typed payloads", async () => {
  const fake = port();
  fake.handlers.set("adoptPairing", () => ({ adopted: true, durability: "confirmed" }));
  fake.handlers.set("abandonPairingAttempt", () => ({ abandoned: true }));
  fake.handlers.set("serviceDiscovery", () => ({ state: "daemon_stopped", profile: "default" }));
  fake.handlers.set("startDaemonOffer", () => ({ started: true, discovery: { state: "verified", profile: "default" } }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  const adoption = await client.adoptPairing("p1");
  assert.equal(adoption.adopted, true);
  await client.abandonPairing("p1");
  const discovery = await client.serviceDiscovery();
  assert.equal(discovery.state, "daemon_stopped");
  const offer = await client.startDaemonOffer();
  assert.equal(offer.started, true);
  assert.deepEqual(fake.calls, [
    ["adoptPairing", { pairing_id: "p1" }],
    ["abandonPairingAttempt", { pairing_id: "p1" }],
    ["serviceDiscovery", {}],
    ["startDaemonOffer", {}],
  ]);
});

test("onboarding operations become stale across an authority era change", async () => {
  const fake = port();
  fake.handlers.set("adoptPairing", () => ({ adopted: true, durability: "confirmed" }));
  fake.handlers.set("abandonPairingAttempt", () => ({ abandoned: true }));
  fake.handlers.set("serviceDiscovery", () => ({ state: "verified", profile: "default" }));
  fake.handlers.set("startDaemonOffer", () => ({ started: false, discovery: { state: "verified", profile: "default" } }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  const adoptions = client.adoptPairing("p1");
  const abandons = client.abandonPairing("p1");
  const discoveries = client.serviceDiscovery();
  const offers = client.startDaemonOffer();
  client.invalidate();
  await assert.rejects(adoptions, StaleResponseError);
  await assert.rejects(abandons, StaleResponseError);
  await assert.rejects(discoveries, StaleResponseError);
  await assert.rejects(offers, StaleResponseError);
});


test("native polling uses its Go-held secret and decisions carry their exact fields", async () => {
  const fake = port();
  fake.handlers.set("pollPairing", () => ({ state: "pending" }));
  fake.handlers.set("decidePairing", () => ({ kind: "committed", body: {} }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  await client.pollPairing("p1", "secret");
  await client.decidePairing({
    pairing_id: "p1",
    code: "123",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-p1-r1"',
    operation_id: "op-1",
    action: "approve",
  });
  assert.deepEqual(fake.calls[0], ["pollPairing", { pairing_id: "p1" }]);
  assert.deepEqual(fake.calls[1], [
    "decidePairing",
    {
      pairing_id: "p1",
      code: "123",
      origin: "http://127.0.0.1:9270",
      etag: '"pairing-p1-r1"',
      operation_id: "op-1",
      action: "approve",
    },
  ]);
});

test("every forwarded operation is a declared native operation name", async () => {
  const fake = port();
  const ok = () => ({});
  for (const name of NATIVE_OPERATIONS) fake.handlers.set(name, ok);
  fake.handlers.set("listSessions", () => []);
  fake.handlers.set("pendingPairings", () => []);
  fake.handlers.set("subscribeEvents", () => ({ closed: Promise.resolve(), close() {} }));
  fake.handlers.set("currentSession", () => ({}));
  fake.handlers.set("capabilities", () => ({ library_id: "l", api_version: "1.0", runtime_generation: "g", stream_epoch: "e", unfiled_project_id: "u", features: {} }));
  fake.handlers.set("operation", () => ({ operation_id: "op", state: "completed", original_status: 200, result_ref: null, job_id: null, result_metadata: null }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  await client.pair("d");
  await client.pollPairing("p", "s");
  await client.decidePairing({
    pairing_id: "p",
    code: "c",
    origin: "o",
    etag: "e",
    operation_id: "i",
    action: "reject",
  });
  await client.currentSession();
  await client.capabilities();
  await client.listSessions();
  await client.pendingPairings();
  await client.operation("i");
  await client.revokeSession("s");
  await client.revokeOwnSession();
  await client.serviceStatus();
  const subscription = client.subscribeEvents();
  subscription.close();
  const names = fake.calls.map(([name]) => name);
  for (const name of names) {
    assert.ok((NATIVE_OPERATIONS as readonly string[]).includes(name), "undeclared operation reached the port: " + name);
  }
  assert.equal(new Set(names).size, names.length);
});

test("context operations use closed named calls and discard a prior-era consumer response", async () => {
  const fake = port();
  fake.handlers.set("createContextUpload", () => ({ upload_id: "upload", checkpoint: {} }));
  const gate = { resolve: null as null | ((value: unknown) => void) };
  fake.handlers.set("consumeContext", () => new Promise((resolve) => { gate.resolve = resolve; }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  const binding = { version_id: "version", page_path: "한글.html", render_instance_id: "render" };
  await client.createContextUpload(binding, "U");
  assert.deepEqual(fake.calls[0], ["createContextUpload", { binding, operation_id: "U" }]);
  const pending = client.consumeContext(binding, "upload", "Q");
  client.invalidate(); gate.resolve?.({ kind: "committed", body: { context_ref: "old" } });
  await assert.rejects(pending, StaleResponseError);
  assert.deepEqual(fake.calls[1], ["consumeContext", { binding, upload_id: "upload", operation_id: "Q" }]);
});

test("typed errors propagate unchanged", async () => {
  const fake = port();
  fake.handlers.set("revokeSession", () => {
    throw new ApiError(409, "OPERATION_CONFLICT", false);
  });
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  await assert.rejects(
    client.revokeSession("s"),
    (error: unknown): boolean => {
      assert.ok(error instanceof ApiError);
      assert.equal((error as ApiError).status, 409);
      assert.equal((error as ApiError).code, "OPERATION_CONFLICT");
      return true;
    },
  );
});

test("subscribeEvents wires the port subscription lifecycle", async () => {
  const fake = port();
  let closedSignalled = false;
  let portClosed = false;
  const subscription: EventSubscription = {
    closed: new Promise<void>((resolve) => {
      setTimeout(() => {
        closedSignalled = true;
        resolve();
      }, 0);
    }),
    close() {
      portClosed = true;
    },
  };
  fake.handlers.set("subscribeEvents", () => subscription);
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  const wrapped = client.subscribeEvents();
  await wrapped.closed;
  assert.ok(closedSignalled);
  wrapped.close();
  assert.ok(portClosed);
});

test("native transport rejects JavaScript bearer binding", () => {
  const client = new NativeHaeryongClient(port() as unknown as NativePort);
  assert.throws(() => client.setCredential("token-1"), /Native sessions attach through Go/);
  client.setCredential(""); client.invalidate();
});

test("a late success crossing an invalidate boundary is discarded", async () => {
  const fake = port();
  const gate = { resolve: null as null | ((value: { pairing_id: string; code: string; polling_secret: string }) => void) };
  fake.handlers.set("pair", () => new Promise((resolve) => { gate.resolve = resolve; }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  const call = client.pair("late");
  client.invalidate();
  gate.resolve?.({ pairing_id: "p", code: "1", polling_secret: "s" });
  await assert.rejects(call, (error: unknown) => error instanceof StaleResponseError);
});

test("a late rejection crossing an invalidate boundary becomes a stale response", async () => {
  const fake = port();
  const gate = { reject: null as null | ((reason: Error) => void) };
  fake.handlers.set("revokeSession", () => new Promise<void>((_, reject) => { gate.reject = reject; }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  const call = client.revokeSession("s");
  client.invalidate();
  gate.reject?.(new Error("port transport failed"));
  await assert.rejects(call, (error: unknown) => error instanceof StaleResponseError);
});

test("a fresh-era operation still passes through", async () => {
  const fake = port();
  fake.handlers.set("serviceStatus", () => ({ ok: true }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  client.invalidate();
  assert.deepEqual(await client.serviceStatus(), { ok: true });
});

test("a subscription arriving from a dead era is closed, not wired", async () => {
  const fake = port();
  let portClosed = false;
  const gate = { resolve: null as null | ((subscription: EventSubscription) => void) };
  fake.handlers.set("subscribeEvents", () => new Promise<EventSubscription>((resolve) => { gate.resolve = resolve; }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  const wrapped = client.subscribeEvents();
  client.invalidate();
  gate.resolve?.({
    closed: new Promise<void>(() => {}),
    close() {
      portClosed = true;
    },
  });
  await wrapped.closed;
  assert.ok(portClosed, "stale-era subscription was disposed");
});

test("verifyContextFresh maps the closed proof operation and validates the typed result", async () => {
  const fake = port();
  const digest = "b".repeat(64);
  const binding = { version_id: "33333333-3333-4333-8333-333333333333", page_path: null, render_instance_id: null };
  fake.handlers.set("verifyContextFresh", () => ({ verified: true, etag: `"sha256-${digest}"` }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  await client.verifyContextFresh("ctx-9", binding, digest);
  assert.deepEqual(fake.calls[0], ["verifyContextFresh", { context_ref: "ctx-9", binding, bytes_sha256: digest }]);
  fake.handlers.set("verifyContextFresh", () => ({ verified: true, etag: '"sha256-other"' }));
  await assert.rejects(client.verifyContextFresh("ctx-9", binding, digest), /unverified result/);
  fake.handlers.set("verifyContextFresh", () => ({ verified: false, etag: `"sha256-${digest}"` }));
  await assert.rejects(client.verifyContextFresh("ctx-9", binding, digest), /unverified result/);
});

test("failed disposal retains its captured attachment for retry after live attachment is cleared", async () => {
  const fake = port(); let attempts = 0;
  fake.handlers.set("attachSession", () => ({ attachment_id: "old", public_origin: "http://127.0.0.1:9270", session: {}, capabilities: {} }));
  fake.handlers.set("disposeSession", () => { if (++attempts === 1) throw new ApiError(500, "NATIVE_LOCAL_FORGET_FAILED", true); });
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  await client.attachSession(); await assert.rejects(client.disposeSession(), ApiError);
  await client.disposeSession(); await client.disposeSession();
  assert.deepEqual(fake.calls.filter(([op]) => op === "disposeSession"), [["disposeSession", { attachment_id: "old" }], ["disposeSession", { attachment_id: "old" }]]);
});

// TASK-003-04 native operations: the metadata open carries the strong ETag
// beside the reified stream; evidence/history/assessments/coverage forward
// exact typed payloads to the closed Go facade routes.
test("versionSuppliedMetadata reifies the stream with the captured ETag", async () => {
  const fake = port();
  fake.handlers.set("versionSuppliedMetadata", () => ({ etag: '"sha256-abc"', body: new ReadableStream<Uint8Array>(), cancel() {} }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  const opened = await client.versionSuppliedMetadata("v1");
  assert.equal(opened.etag, '"sha256-abc"');
  assert.equal(typeof opened.stream.cancel, "function");
  assert.deepEqual(fake.calls[0], ["versionSuppliedMetadata", { version_id: "v1" }]);
});

test("a missing ETag travels as null while the stream stays guarded", async () => {
  const fake = port();
  fake.handlers.set("versionSuppliedMetadata", () => ({ etag: null, body: new ReadableStream<Uint8Array>(), cancel() {} }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  const opened = await client.versionSuppliedMetadata("v1");
  assert.equal(opened.etag, null);
});

test("evidence, verification history, assessments and coverage forward exact typed payloads", async () => {
  const fake = port();
  fake.handlers.set("versionEvidence", () => ({ version_id: "v1", evidence_id: "e1", authored: {}, source: {}, availability: "unavailable", bytes_origin: null, reason: "not_read_by_view", service_verification: null, verifications_url: "/x" }));
  fake.handlers.set("evidenceVerifications", () => ({ items: [], next_cursor: null }));
  fake.handlers.set("versionAssessments", () => ({ items: [], next_cursor: null, applicable: null }));
  fake.handlers.set("assessmentCoverage", () => ({ items: [], next_cursor: null }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  await client.versionEvidence("v1", "e1");
  await client.evidenceVerifications("v1", "e1", "cur");
  await client.versionAssessments("v1", { targetSetId: "t1", scope: { kind: "page", pagePath: "a.html" }, cursor: "c" });
  await client.assessmentCoverage("v1", "c1", "cur");
  assert.deepEqual(fake.calls[0], ["versionEvidence", { version_id: "v1", evidence_id: "e1" }]);
  assert.deepEqual(fake.calls[1], ["evidenceVerifications", { version_id: "v1", evidence_id: "e1", cursor: "cur" }]);
  assert.deepEqual(fake.calls[2], ["versionAssessments", { version_id: "v1", target_set_id: "t1", scope_kind: "page", page_path: "a.html", region_id: undefined, cursor: "c" }]);
  assert.deepEqual(fake.calls[3], ["assessmentCoverage", { version_id: "v1", coverage_set_id: "c1", cursor: "cur" }]);
});

test("a delayed metadata open in a stale era cancels its own stream and is never delivered", async () => {
  const fake = port();
  let releaseOpen: ((value: unknown) => void) | null = null;
  let cancelled = 0;
  fake.handlers.set("versionSuppliedMetadata", () => new Promise((resolve) => { releaseOpen = resolve; }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  const pending = client.versionSuppliedMetadata("v1");
  await new Promise((resolve) => setImmediate(resolve));
  const release = releaseOpen as ((value: unknown) => void) | null;
  if (release === null) throw new Error("open request was never dispatched");
  client.invalidate();
  release({ etag: '"sha256-x"', body: new ReadableStream<Uint8Array>(), cancel() { cancelled += 1; } });
  await assert.rejects(pending, StaleResponseError);
  assert.equal(cancelled, 1, "the stale handle must be cancelled exactly once");
});

test("a newly current metadata stream stays untouched", async () => {
  const fake = port();
  let cancelled = 0;
  fake.handlers.set("versionSuppliedMetadata", () => ({ etag: '"sha256-y"', body: new ReadableStream<Uint8Array>({ start(controller) { controller.enqueue(new TextEncoder().encode("{}")); controller.close(); } }), cancel() { cancelled += 1; } }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  const opened = await client.versionSuppliedMetadata("v1");
  assert.equal(opened.etag, '"sha256-y"');
  const read = await opened.stream.body.getReader().read();
  assert.deepEqual(new TextDecoder().decode(read.value), "{}");
  assert.equal(cancelled, 0, "a current stream must never be cancelled by the stale guard");
});

test("a delayed packageStream open in a stale era cancels its own stream; the next current open is untouched", async () => {
  const fake = port();
  let cancelled = 0;
  let currentCancelled = 0;
  let releases: Array<(value: { body: ReadableStream<Uint8Array>; cancel(): void }) => void> = [];
  fake.handlers.set("packageStream", () => new Promise((resolve) => { releases.push(resolve); }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  const stale = client.packageStream("v1");
  await new Promise((resolve) => setImmediate(resolve));
  client.invalidate();
  const release = releases[releases.length - 1] as ((value: { body: ReadableStream<Uint8Array>; cancel(): void }) => void) | null;
  if (release === null) throw new Error("open request was never dispatched");
  release({ body: new ReadableStream<Uint8Array>(), cancel() { cancelled += 1; } });
  await assert.rejects(stale, StaleResponseError);
  assert.equal(cancelled, 1, "the discarded package stream must be cancelled exactly once");
  fake.handlers.set("packageStream", () => ({ body: new ReadableStream<Uint8Array>({ start(controller) { controller.close(); } }), cancel() { currentCancelled += 1; } }));
  const current = await client.packageStream("v1");
  await current.body.getReader().read();
  assert.equal(currentCancelled, 0, "a newly current stream must stay untouched");
});

test("a delayed fileStream open in a stale era cancels its own stream", async () => {
  const fake = port();
  let cancelled = 0;
  let releases: Array<(value: { body: ReadableStream<Uint8Array>; cancel(): void }) => void> = [];
  fake.handlers.set("fileStream", () => new Promise((resolve) => { releases.push(resolve); }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  const stale = client.fileStream("v1", "a.bin");
  await new Promise((resolve) => setImmediate(resolve));
  client.invalidate();
  const release = releases[releases.length - 1] as ((value: { body: ReadableStream<Uint8Array>; cancel(): void }) => void) | null;
  if (release === null) throw new Error("open request was never dispatched");
  release({ body: new ReadableStream<Uint8Array>(), cancel() { cancelled += 1; } });
  await assert.rejects(stale, StaleResponseError);
  assert.equal(cancelled, 1, "the discarded file stream must be cancelled exactly once");
});

test("the new evidence operations are declared in the closed native list", () => {
  for (const name of ["versionSuppliedMetadata", "versionEvidence", "evidenceVerifications", "versionAssessments", "assessmentCoverage"]) {
    assert.ok((NATIVE_OPERATIONS as readonly string[]).includes(name), name);
  }
});

test("the ten M3 filesystem operations are declared and a delivered file validator survives the era wrapper", async () => {
  for (const name of ["chooseImportSource", "sourceEntries", "readSourceRange", "releaseSource", "sourceVerifyStable", "chooseDownloadDestination", "destOpen", "destWrite", "destClose", "destAbort"]) {
    assert.ok((NATIVE_OPERATIONS as readonly string[]).includes(name), name);
  }
  const fake = port();
  const sha = "c".repeat(64);
  fake.handlers.set("fileStream", () => ({ body: new ReadableStream<Uint8Array>(), cancel() {}, validator: { sha256: sha, byte_size: 7 } }));
  fake.handlers.set("destWrite", () => ({ written: 3 }));
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  const opened = await client.fileStream("v1", "a.bin");
  assert.deepEqual(opened.validator, { sha256: sha, byte_size: 7 });
  const written = await client.destWrite("dest-1", new Uint8Array([1, 2, 3]));
  assert.equal(written.written, 3);
  assert.deepEqual(fake.calls[fake.calls.length - 1], ["destWrite", { handle: "dest-1", bytes_b64: "AQID" }]);
});

test("all seven source/configuration operations forward typed payloads and reject stale replies", async () => {
  const fake = port();
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  const plan = { scope: { kind: "project" as const, project_id: "p" }, expected_config_revision: 2, changes: { sources: [] } };
  const commit = { expected_config_revision: 2, confirmed: true };
  const excerpt: import("./types.ts").EvidenceVerificationRequest = { expected_excerpt_ref: { upload_id: "11111111-2222-4333-8444-555555555555" } };
  const cases: Array<[string, unknown, () => Promise<unknown>]> = [
    ["configurationView", {}, () => client.configurationView()],
    ["configurationPlan", { request: plan, operation_id: "k" }, () => client.configurationPlan(plan, "k")],
    ["configurationPlanCommit", { plan_id: "plan", request: commit, operation_id: "k" }, () => client.configurationPlanCommit("plan", commit, "k")],
    ["projectRegister", { name: "name", operation_id: "k" }, () => client.projectRegister("name", "k")],
    ["projectSources", { project_id: "p", cursor: "c" }, () => client.projectSources("p", "c")],
    ["verifySourceEvidence", { version_id: "v", evidence_id: "../상세", request: excerpt, operation_id: "k" }, () => client.verifySourceEvidence("v", "../상세", excerpt, "k")],
    ["runSourceObservation", { version_id: "v", request: { scope: { kind: "page", page_path: "a.html" } }, operation_id: "k" }, () => client.runSourceObservation("v", { scope: { kind: "page", page_path: "a.html" } }, "k")],
  ];
  for (const [operation, payload, invoke] of cases) {
    const write = operation !== "configurationView" && operation !== "projectSources";
    fake.handlers.set(operation, () => write ? { kind: "committed", body: { marker: operation } } : { marker: operation });
    assert.deepEqual(await invoke(), { marker: operation });
    assert.deepEqual(fake.calls.at(-1), [operation, payload]);
    let release!: (value: unknown) => void;
    fake.handlers.set(operation, () => new Promise((resolve) => { release = resolve; }));
    const pending = invoke();
    client.invalidate();
    release(write ? { kind: "committed", body: {} } : {});
    await assert.rejects(pending, StaleResponseError);
  }
  fake.handlers.set("assessmentCoverage", () => ({ items: [], next_cursor: null }));
  await client.assessmentCoverage("v", "c", "next", "11111111-2222-4333-8444-555555555555");
  assert.deepEqual(fake.calls.at(-1), ["assessmentCoverage", { version_id: "v", coverage_set_id: "c", cursor: "next", attempt_run_id: "11111111-2222-4333-8444-555555555555" }]);
});

test("native verification and observation reconcile accepted and uncertain writes by their original operation", async () => {
  for (const op of ["verifySourceEvidence", "runSourceObservation"]) for (const uncertain of [false, true]) {
    const fake = port();
    fake.handlers.set(op, () => { if (uncertain) throw new UncertainResponseError(); return { kind: "accepted", body: { operation_id: "key" } }; });
    fake.handlers.set("operation", () => ({ operation_id: "key", state: "completed", original_status: 201, result_metadata: { operation_id: "key", outcome: "captured" } }));
    const client = new NativeHaeryongClient(fake as unknown as NativePort);
    const result = op === "verifySourceEvidence" ? await client.verifySourceEvidence("v", "e", {}, "key") : await client.runSourceObservation("v", {}, "key");
    assert.deepEqual(result, { operation_id: "key", outcome: "captured" });
    assert.equal(fake.calls.filter(([name]) => name === op).length, 1);
    assert.deepEqual(fake.calls.at(-1), ["operation", { operation_id: "key" }]);
  }
});

test("provider status uses a closed named operation without copying account credentials", async () => {
  const fake = port();
  const status = { provider_id: "codex", state: "auth_required", authentication_mode: "unknown", ordinary_usage_allowed: null, models: [], read_only: false, lookup_supported: false, cancellation_supported: false, catalog_proves_access: false };
  fake.handlers.set("providerStatus", () => status);
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  assert.deepEqual(await client.providerStatus(), status);
  assert.deepEqual(fake.calls, [["providerStatus", {}]]);
});


test("native provider status refuses unknown authority state and preserves an explicit false allowance",async()=>{
 const fake=port();const client=new NativeHaeryongClient(fake as unknown as NativePort);
 fake.handlers.set("providerStatus",()=>({state:"paused",ordinary_usage_allowed:false}));assert.equal((await client.providerStatus()).ordinary_usage_allowed,false);
 fake.handlers.set("providerStatus",()=>({state:"future_state",ordinary_usage_allowed:true}));await assert.rejects(client.providerStatus(),/지원되는 제공자 상태/);
 fake.handlers.set("providerStatus",()=>({state:"ready"}));await assert.rejects(client.providerStatus(),/지원되는 제공자 상태/);
});

test("GCLIENT02 generation/draft named calls retain identity, conditional writes and stale read refusal", async () => {
  const fake=port(), key="key", request={project_id:"p",document_id:null,intent:"draft" as const,prompt:"explain",source_selection:{selections:[]}};
  const accepted={operation_id:key,job_id:"job",draft_id:"draft",intent:"draft",job_url:"/api/v1/jobs/job"};
  fake.handlers.set("generationStart",()=>({kind:"accepted",body:accepted}));
  fake.handlers.set("operation",()=>({operation_id:key,state:"completed",original_status:202,result_metadata:accepted}));
  fake.handlers.set("draftPatch",()=>({kind:"committed",body:{draft_id:"d",state:"rejected"}}));
  fake.handlers.set("draftPublish",()=>({kind:"committed",body:{draft_id:"d",state:"published"}}));
  fake.handlers.set("draftList",()=>({items:[],next_cursor:null}));
  fake.handlers.set("draftGet",()=>({draft_id:"d",state:"ready"}));
  fake.handlers.set("draftHistory",()=>({items:[],next_cursor:null}));
  const client=new NativeHaeryongClient(fake as unknown as NativePort);
  assert.deepEqual(await client.generationStart(request,key),accepted);
  await client.draftPatch("d",{action:"reject"},'"exact"',key);
  await client.draftPublish("d",'"next"',key);
  await client.draftList("p","opaque");await client.draftHistory("d","next");
  assert.deepEqual(fake.calls.slice(0,6),[["generationStart",{request,operation_id:key}],["operation",{operation_id:key}],["draftPatch",{draft_id:"d",body:{action:"reject"},etag:'"exact"',operation_id:key}],["draftPublish",{draft_id:"d",etag:'"next"',operation_id:key}],["draftList",{project_id:"p",cursor:"opaque"}],["draftHistory",{draft_id:"d",cursor:"next"}]]);
  const pending=client.draftGet("d");client.invalidate();await assert.rejects(pending,StaleResponseError);
});


test("T4R2RESUME06 the actual native client keeps changed-runtime404 recovery on the operation port and consumes200 without jobResume",async()=>{
 const {recoverGenerationResume}=await import("./generation.ts");
 const fake=port();let found=false;
 fake.handlers.set("operation",()=>{if(!found)throw new ApiError(404,"NOT_FOUND",false);return{operation_id:"resume",state:"completed",original_status:200,result_metadata:{job_id:"job"}};});
 const client=new NativeHaeryongClient(fake as unknown as NativePort);
 const record={operationId:"parent",request:{project_id:"p",document_id:null,intent:"draft" as const,source_selection:{selections:[]}},promptSHA:"",promptSize:0,dispatchReady:true,accepted:{operation_id:"parent",job_id:"job",draft_id:"d",intent:"draft" as const,job_url:"/api/v1/jobs/job"},runtimeGeneration:"parent",resume:{key:"resume",decision:"retry" as const,generation:3,etag:'"original"',runtimeGeneration:"old",dispatchReady:true}};
 await assert.rejects(recoverGenerationResume(client,record,"changed",()=>{},()=>{}),/세대/);
 found=true;assert.equal(await recoverGenerationResume(client,record,"changed",()=>{},()=>{}),true);
 assert.deepEqual(fake.calls,[["operation",{operation_id:"resume"}],["operation",{operation_id:"resume"}]]);
});
