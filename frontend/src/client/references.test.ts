import { test } from "node:test";
import assert from "node:assert/strict";
import { HttpHaeryongClient } from "./http.ts";
import { NativeHaeryongClient, NATIVE_OPERATIONS, type NativePort } from "./native.ts";
import { ApiError, StaleResponseError } from "./types.ts";

const request = { versionId: "v1", referenceId: "ref/%2F + 한글?#", pagePath: "nested/%2F + 한글.html", regionId: "영역/%2F +" };
test("RF09 HTTP reference operations encode raw IDs and navigation exactly once, preserving supplied empty/zero controls", async () => {
  const original = globalThis.fetch, observed: URL[] = [];
  globalThis.fetch = (async input => {
    observed.push(new URL(String(input), "http://host.invalid"));
    return new Response(JSON.stringify({ data: { items: [], next_cursor: null, materialization: { state: "ready", reason: null } } }));
  }) as typeof fetch;
  try {
    const client = new HttpHaeryongClient();
    await client.versionReferences({ versionId: "v1", cursor: "opaque/+%", limit: 0 });
    assert.equal(observed[0]!.pathname, "/api/v1/versions/v1/references");
    assert.deepEqual(Object.fromEntries(observed[0]!.searchParams), { cursor: "opaque/+%", limit: "0" });
    await client.resolveReference(request);
    assert.equal(observed[1]!.pathname, "/api/v1/versions/v1/references/resolve");
    assert.deepEqual(Object.fromEntries(observed[1]!.searchParams), { reference_id: request.referenceId, page_path: request.pagePath, region_id: request.regionId });
    await client.resolveReference({ versionId: "v1", referenceId: "", pagePath: "", regionId: "" });
    assert.deepEqual(Object.fromEntries(observed[2]!.searchParams), { reference_id: "", page_path: "", region_id: "" });
  } finally { globalThis.fetch = original; }
});
test("RF09 both named native operations forward closed fields and discard stale successes and errors", async () => {
  const calls: unknown[][] = []; let finish: (value: unknown) => void = () => {};
  const client = new NativeHaeryongClient({ call: async (name: string, payload: unknown) => { calls.push([name, payload]); return new Promise(resolve => { finish = resolve; }); } } as unknown as NativePort);
  assert.ok(NATIVE_OPERATIONS.includes("versionReferences")); assert.ok(NATIVE_OPERATIONS.includes("resolveReference"));
  const list = client.versionReferences({ versionId: "v1", cursor: "c/+%", limit: 17 });
  assert.deepEqual(calls[0], ["versionReferences", { version_id: "v1", cursor: "c/+%", limit: 17 }]);
  client.invalidate(); finish({}); await assert.rejects(list, StaleResponseError);
  const resolve = client.resolveReference(request);
  assert.deepEqual(calls[1], ["resolveReference", { version_id: "v1", reference_id: request.referenceId, page_path: request.pagePath, region_id: request.regionId }]);
  client.invalidate(); finish({}); await assert.rejects(resolve, StaleResponseError);
  for (const operation of ["versionReferences", "resolveReference"] as const) {
    let reject: (error: unknown) => void = () => {};
    const errors = new NativeHaeryongClient({ call: () => new Promise((_resolve, fail) => { reject = fail; }) } as unknown as NativePort);
    const pending = operation === "versionReferences" ? errors.versionReferences({ versionId: "v1" }) : errors.resolveReference(request);
    errors.invalidate(); reject(new ApiError(410, "TARGET_GONE", false)); await assert.rejects(pending, StaleResponseError);
  }
});
test("RF09 HTTP/native 404 and 410 preserve non-disclosing and minimal gone details", async () => {
  const original = globalThis.fetch;
  try {
    for (const status of [404, 410]) {
      const details = status === 410 ? { document_id: "d", version_id: "v" } : {};
      globalThis.fetch = (async () => new Response(JSON.stringify({ error: { code: status === 410 ? "TARGET_GONE" : "NOT_FOUND", retryable: false, details } }), { status })) as typeof fetch;
      const native = new NativeHaeryongClient({ call: async () => { throw new ApiError(status, status === 410 ? "TARGET_GONE" : "NOT_FOUND", false, 0, details); } } as unknown as NativePort);
      for (const client of [new HttpHaeryongClient(), native]) await assert.rejects(client.resolveReference(request), error => error instanceof ApiError && error.status === status && JSON.stringify(error.details) === JSON.stringify(details));
    }
  } finally { globalThis.fetch = original; }
});
