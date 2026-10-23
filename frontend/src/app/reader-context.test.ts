import { test } from "node:test";
import assert from "node:assert/strict";
import { readerContext } from "./reader-context.ts";

const documentURL = "/documents/doc/versions/ver?page=a.html";
test("hash-only absent-state entry keeps the live Reader context", () => {
  const before = new URL(documentURL + "#region=first", "http://host.invalid");
  const after = new URL(documentURL + "#region=second", before);
  assert.equal(readerContext(before.pathname + before.search, after.pathname + after.search, "live", undefined, () => "new"), "live");
});
test("different document with absent state establishes a new Reader context", () => {
  let calls = 0;
  assert.equal(readerContext(documentURL, "/connect?return=reader", "live", undefined, () => { calls++; return "new"; }), "new");
  assert.equal(calls, 1);
});
test("stamped entry retains its own Reader context", () => {
  for (const next of [documentURL, "/other-document"]) assert.equal(readerContext(documentURL, next, "live", "saved", () => { throw Error("unexpected new context"); }), "saved");
});
