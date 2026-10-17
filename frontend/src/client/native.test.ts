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

test("invalidate changes the era without erasing the bound credential", () => {
  const fake = port();
  const client = new NativeHaeryongClient(fake as unknown as NativePort);
  client.setCredential("token-1");
  client.invalidate();
  assert.equal(client.credential, "token-1");
  client.setCredential("");
  assert.equal(client.credential, "");
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
