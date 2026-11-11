import test from "node:test";
import assert from "node:assert/strict";
import { HttpHaeryongClient } from "./http.ts";
import { NativeHaeryongClient, NATIVE_OPERATIONS } from "./native.ts";
import type { NativePort } from "./native.ts";
import { ApiError, StaleResponseError } from "./types.ts";
const key = "11111111-1111-4111-8111-111111111111";
const result = { operation_id: key, state: "completed", finding: null, effect: null, job_id: null };

test("HTTP exact finding/conditional Topic PUT/empty Undo dispatches retain per-resource ETag and key", async () => {
  const old = globalThis.fetch, calls: { url: string; method: string; headers: Headers; body: unknown }[] = [];
  globalThis.fetch = (async (url: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: String(url), method: init!.method!, headers: new Headers(init!.headers), body: JSON.parse(init!.body as string) });
    return new Response(JSON.stringify({ data: result }), { status: 200 });
  }) as typeof fetch;
  try {
    const c = new HttpHaeryongClient();
    await c.findingDecision("fnd-opaque", "approve", '"finding-r3"', key);
    await c.topicAssignmentSet("topic", { pinned: false, decision: "rejected" }, '"overlay-r8"', key);
    await c.overlayUndo("effect", '"overlay-r9"', key);
    assert.deepEqual(calls.map(c => [c.url, c.method, c.body, c.headers.get("If-Match"), c.headers.get("Idempotency-Key")]), [
      ["/api/v1/findings/fnd-opaque/decisions", "POST", { decision: "approve" }, '"finding-r3"', key],
      ["/api/v1/topic-assignments/topic", "PUT", { pinned: false, decision: "rejected" }, '"overlay-r8"', key],
      ["/api/v1/overlays/effect/undo", "POST", {}, '"overlay-r9"', key],
    ]);
  } finally { globalThis.fetch = old; }
});
test("HTTP lost acknowledgement reconciles original receipt with no second mutation; queued repair stays pending repair", async () => {
  const old = globalThis.fetch, calls: string[] = [];
  globalThis.fetch = (async (url: string | URL | Request, init?: RequestInit) => {
    calls.push(`${init?.method} ${String(url)}`);
    if (init?.method === "POST") throw new TypeError("response lost after upstream commit");
    return new Response(JSON.stringify({ data: { operation_id: key, state: "completed", original_status: 202, result_metadata: { ...result, state: "repair_pending", job_id: "repair" } } }), { status: 200 });
  }) as typeof fetch;
  try {
    const c = new HttpHaeryongClient(); const r = await c.findingDecision("fnd-opaque", "approve", '"r3"', key);
    assert.equal(r.state, "repair_pending"); assert.deepEqual(calls, ["POST /api/v1/findings/fnd-opaque/decisions", `GET /api/v1/operations/${key}`]);
  } finally { globalThis.fetch = old; }
});
for (const status of [409, 412]) test(`HTTP definite ${status} propagates exact reason without a receipt lookup or write retry`, async () => {
  const old = globalThis.fetch; let calls = 0;
  globalThis.fetch = (async () => { calls++; return new Response(JSON.stringify({ error: { code: "INVALID_TRANSITION", details: { reason: "undone_requires_reassessment_approve" }, retryable: false } }), { status }); }) as typeof fetch;
  try {
    await assert.rejects(new HttpHaeryongClient().topicAssignmentSet("undone", { pinned: false, decision: "accepted" }, '"r8"', key), (e: unknown) => e instanceof ApiError && e.status === status && e.details.reason === "undone_requires_reassessment_approve");
    assert.equal(calls, 1);
  } finally { globalThis.fetch = old; }
});
test("native only named exact payloads and existing scanStart preserve conditional controls", async () => {
  const calls: unknown[] = [];
  const port = { call: async (name: string, payload: unknown) => {
    assert.ok((NATIVE_OPERATIONS as readonly string[]).includes(name)); calls.push([name, payload]);
    return { kind: "committed", body: result };
  } } as unknown as NativePort;
  const c = new NativeHaeryongClient(port);
  await c.findingDecision("fnd-x", "reject", '"f-r3"', key);
  await c.topicAssignmentSet("topic", { pinned: true, decision: "accepted" }, '"o-r8"', key);
  await c.overlayUndo("effect", '"o-r9"', key);
  assert.deepEqual(calls, [
    ["findingDecision", { finding_id: "fnd-x", decision: "reject", etag: '"f-r3"', operation_id: key }],
    ["topicAssignmentSet", { assignment_id: "topic", pinned: true, decision: "accepted", etag: '"o-r8"', operation_id: key }],
    ["overlayUndo", { overlay_id: "effect", etag: '"o-r9"', operation_id: key }],
  ]);
  assert.ok(!(NATIVE_OPERATIONS as readonly string[]).includes("extendedscanStart"));
});
test("native terminal repair response remains repair_pending and retired-era response is discarded", async () => {
  let release!: (value: unknown) => void;
  const port = { call: () => new Promise(r => { release = r; }) } as unknown as NativePort;
  const c = new NativeHaeryongClient(port);
  const p = c.findingDecision("fnd-x", "approve", '"f-r3"', key);
  c.invalidate(); release({ kind: "committed", body: result }); await assert.rejects(p, StaleResponseError);
  const repair = new NativeHaeryongClient({ call: async () => ({ kind: "accepted", body: { ...result, state: "repair_pending", job_id: "repair" } }) } as unknown as NativePort);
  assert.equal((await repair.findingDecision("fnd-x", "approve", '"f-r3"', key)).state, "repair_pending");
});
