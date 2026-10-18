// Unit regressions for the pure three-way personal CAS decision helper.
import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { clientIntent, reconcilePersonal412, structuralEqual } from "./personal-cas.ts";

const snapshot = (exists: boolean, value: unknown, etag: string | null) => ({ exists, value, etag });
const pointer = (over: Record<string, unknown> = {}) => ({ page_path: "a.html", page_query: "s=1", page_fragment: "f", opened: true, first_opened_at: "2026-10-18T00:00:00Z", last_opened_at: "2026-10-18T01:00:00Z", ...over });

describe("structuralEqual", () => {
  it("treats object key order as insignificant including nested position values", () => {
    assert.equal(structuralEqual({ page_query: "", position: { top: 1, left: 2 } }, { position: { left: 2, top: 1 }, page_query: "" }), true);
    assert.equal(structuralEqual({ a: [1, { x: 1, y: 2 }] }, { a: [1, { y: 2, x: 1 }] }), true);
    assert.equal(structuralEqual({ a: 1 }, { a: 1, b: undefined }), false);
  });
});

describe("reconcilePersonal412", () => {
  it("keeps a cross-device pointer change a conflict even with identical client fields", () => {
    const b0 = snapshot(true, pointer(), "\"e1\"");
    const fresh = snapshot(true, pointer({ last_opened_at: "2026-10-18T02:00:00Z" }), "\"e2\"");
    assert.equal(reconcilePersonal412({ family: "reading-position", base: b0, intent: pointer({ page_path: "b.html" }), fresh }), "conflict");
  });
  it("converges when the fresh projection equals the intent regardless of timestamps", () => {
    const fresh = snapshot(true, pointer({ first_opened_at: "x", last_opened_at: "y" }), "\"e9\"");
    assert.equal(reconcilePersonal412({ family: "reading-position", base: snapshot(true, pointer(), "\"e1\""), intent: pointer(), fresh }), "converged");
  });
  it("retries only on a genuinely unchanged base with the original opaque ETag", () => {
    const b0 = snapshot(true, pointer(), "\"e1\"");
    assert.equal(reconcilePersonal412({ family: "reading-position", base: b0, intent: pointer({ page_path: "b.html" }), fresh: snapshot(true, pointer(), "\"e1\"") }), "retry");
    assert.equal(reconcilePersonal412({ family: "reading-position", base: b0, intent: pointer({ page_path: "b.html" }), fresh: snapshot(true, pointer(), "\"e3\"") }), "conflict");
  });
  it("keeps other families on the full-state contract with no projection", () => {
    const value = { page_query: "q", page_fragment: "", position: { top: 3 } };
    assert.equal(reconcilePersonal412({ family: "page-position", base: snapshot(true, value, "\"p1\""), intent: { ...value, position: { top: 4 } }, fresh: snapshot(true, value, "\"p1\"") }), "retry");
    assert.equal(reconcilePersonal412({ family: "reader-preference", base: snapshot(true, { default_page: null }, "\"r1\""), intent: { default_page: "x.html" }, fresh: snapshot(true, { default_page: "y.html" }, "\"r2\"") }), "conflict");
  });
  it("handles missing-resource create precondition outcomes", () => {
    const absent = snapshot(false, { default_page: null }, null);
    assert.equal(reconcilePersonal412({ family: "reader-preference", base: absent, intent: { default_page: "x.html" }, fresh: snapshot(true, { default_page: "x.html" }, "\"n1\"") }), "converged");
    assert.equal(reconcilePersonal412({ family: "reader-preference", base: absent, intent: { default_page: "x.html" }, fresh: snapshot(true, { default_page: "z.html" }, "\"n2\"") }), "conflict");
  });
});

describe("clientIntent", () => {
  it("strips only the pointer timestamps and preserves every other key", () => {
    const projected = clientIntent("reading-position", pointer()) as Record<string, unknown>;
    assert.deepEqual(Object.keys(projected).sort(), ["opened", "page_fragment", "page_path", "page_query"]);
    assert.deepEqual(clientIntent("page-position", { a: 1 }), { a: 1 });
  });
});
