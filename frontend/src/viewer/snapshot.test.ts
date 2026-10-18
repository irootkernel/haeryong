import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { SnapshotCursor } from "./snapshot.ts";

test("large individual strings serialize incrementally and preserve the begin snapshot", () => {
  const state = { text: "한글🌊\n\"\\\ud800".repeat(200_000), many: Array.from({ length: 300 }, (_, i) => ({ i })) };
  const expected = Buffer.from(JSON.stringify(state));
  const cursor = new SnapshotCursor(state);
  state.text = "changed after begin"; state.many[0]!.i = -1;
  const hash = createHash("sha256"); let offset = 0;
  while (!cursor.done) {
    const bytes = cursor.read(8191);
    assert.ok(bytes.length <= 8191);
    assert.deepEqual(Buffer.from(bytes), expected.subarray(offset, offset + bytes.length));
    offset += bytes.length; hash.update(bytes);
  }
  assert.equal(offset, expected.length);
  assert.equal(cursor.digest(), hash.digest("hex"));
});

test("resume reconstructs identical UTF-8 bytes at arbitrary accepted offsets and credits", () => {
  const state = { "long key🌊": "a🌊\n\ud800".repeat(50_000), value: [true, null, 2, { x: "end" }] };
  const expected = Buffer.from(JSON.stringify(state)); const cursor = new SnapshotCursor(state);
  for (const start of [1, 65_537, 8191, expected.length - 5, 0]) {
    cursor.resume(start); let offset = start;
    while (!cursor.done) {
      const bytes = cursor.read(17_021);
      assert.deepEqual(Buffer.from(bytes), expected.subarray(offset, offset + bytes.length));
      offset += bytes.length;
    }
    assert.equal(offset, expected.length);
    assert.equal(cursor.digest(), createHash("sha256").update(expected).digest("hex"));
  }
  assert.throws(() => cursor.resume(expected.length + 1), /exceeds/);
});

test("cycles and non-JSON state fail explicitly rather than producing a truncated success", () => {
  const cycle: { self?: unknown } = {}; cycle.self = cycle;
  for (const value of [cycle, new Map([["key", "value"]]), { bigint: 1n }]) {
    const cursor = new SnapshotCursor(value);
    assert.throws(() => { while (!cursor.done) cursor.read(65536); }, TypeError);
    assert.throws(() => cursor.digest(), /not complete/);
  }
});
