// NativePort contract tests. The fake port accepts only declared operation
// names; NativeHaeryongClient must forward exact typed payloads and propagate
// typed errors without inventing arbitrary URLs, paths or method names.
import test from "node:test";
import assert from "node:assert/strict";
import { NATIVE_OPERATIONS, NativeHaeryongClient, type NativePort } from "./native.ts";
import { ApiError, StaleResponseError, type EventSubscription } from "./types.ts";

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

test("pollPairing and decidePairing carry their exact fields", async () => {
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
  assert.deepEqual(fake.calls[0], ["pollPairing", { pairing_id: "p1", polling_secret: "secret" }]);
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
  fake.handlers.set("versionEvidence", () => ({ version_id: "v1", evidence_id: "e1", authored: {}, source: {}, availability: "unavailable", bytes_origin: null, reason: "source_read_not_implemented", service_verification: null, verifications_url: "/x" }));
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
