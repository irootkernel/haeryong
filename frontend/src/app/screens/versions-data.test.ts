import { test } from "node:test";
import assert from "node:assert/strict";
import type { FileEntryView, HaeryongClient, PackageStream, VersionView } from "../../client/types.ts";
import { ApiError } from "../../client/types.ts";
import { ComparisonReads, comparePath, defaultPair, FileComparison, loadComparisonMetadata, metadataDifferences } from "./versions-data.ts";

const file = (path: string, hash = "a"): FileEntryView => ({ path, kind: "file", bytes_sha256: hash, byte_size: "0", preview: "text", file_url: "", etag: hash });
test("S05 merges BINARY paths across independent cursors, yields before a delayed page and retries its cursor", async () => {
  const calls: string[] = []; let fail = true;
  const merge = new FileComparison(async (side, cursor) => {
    calls.push(`${side}:${cursor ?? "first"}`);
    if (side === 0) return { items: cursor ? [file("😀")] : [file("a"), file("é")], next_cursor: cursor ? null : "old2" };
    if (cursor && fail) { fail = false; throw Error("lost page"); }
    return { items: cursor ? [file("z"), file("\uE000")] : [file("a", "b")], next_cursor: cursor ? null : "new2" };
  });
  assert.deepEqual((await merge.next()).map(r => [r.path, r.change]), [["a", "changed"]]);
  assert.equal(merge.complete, false); assert.deepEqual(calls, ["0:first", "1:first"]);
  await assert.rejects(merge.next(), /lost page/);
  const rows = []; while (!merge.complete) rows.push(...await merge.next());
  assert.deepEqual(rows.map(r => [r.path, r.change]), [["z", "added"], ["é", "removed"], ["\uE000", "added"], ["😀", "removed"]]);
  assert.equal(calls.filter(c => c === "1:new2").length, 2);
  assert.ok(comparePath("\uE000", "😀") < 0, "UTF-16 comparison would give the opposite result");
});
test("S05 includes represented directories, link target and entry kind changes", async () => {
  const before = [{ ...file("directory"), kind: "directory" as const, bytes_sha256: undefined }, { ...file("link"), kind: "symlink" as const, link_target: "old" }];
  const after = [file("directory"), { ...before[1]!, link_target: "new" }];
  const merge = new FileComparison(async side => ({ items: side === 0 ? before : after, next_cursor: null }));
  assert.deepEqual((await merge.next()).map(r => r.change), ["changed", "changed"]); assert.equal(merge.complete, true);
  assert.deepEqual(metadataDifferences({ regions: { '["a.html","same"]': "old" } }, { regions: { '["b.html","same"]': "new" } }).map(r => r.field), ["regions"]);
});
test("S4 leaving a pair while inventory is pending cannot revive its result", async () => {
  let finish!: (value: { items: FileEntryView[]; next_cursor: null }) => void;
  const merge = new FileComparison(async side => side ? { items: [], next_cursor: null } : new Promise(resolve => { finish = resolve; }));
  const pending = merge.next(); merge.dispose(); finish({ items: [file("stale")], next_cursor: null });
  assert.deepEqual(await pending, []);
});
test("S4 pair/authority disposal cancels a pending reader and a late stream-open handle", async () => {
  for (const late of [false, true]) {
    let opened!: (value: PackageStream) => void, cancelled = 0;
    const stream: PackageStream = { body: new ReadableStream({ cancel() { cancelled++; } }), cancel() { cancelled++; } };
    const reads = new ComparisonReads(), chunks: string[] = [];
    const client = { fileStream: async () => late ? new Promise<PackageStream>(resolve => { opened = resolve; }) : stream } as unknown as HaeryongClient;
    const pending = reads.text(client, "v", file("a"), text => chunks.push(text));
    await Promise.resolve(); reads.dispose(); if (late) opened(stream);
    await pending.catch(error => assert.match(error.message, /이전 비교/));
    assert.ok(cancelled > 0); assert.deepEqual(chunks, []);
  }
});
test("S5 default is predecessor/latest or Reader-selected/latest; explicit foreign versions fail", () => {
  const versions = [1, 2, 3].map(n => ({ version_id: `v${n}`, sequence: n, document_id: "D" })) as VersionView[];
  assert.deepEqual(defaultPair(versions, null, null, "v3"), ["v2", "v3"]);
  assert.deepEqual(defaultPair(versions, "v1", null, "v3"), ["v1", "v3"]);
  assert.deepEqual(defaultPair(versions.slice(0, 1), null, null, "v1"), ["v1", "v1"]);
  assert.throws(() => defaultPair(versions, "foreign", "v3", "v3"));
  assert.equal(defaultPair([], null, null, null), null);
});
test("S05 text preview streams exact blocks with backpressure, then verifies complete bytes", async () => {
  const { sha256 } = await import('@noble/hashes/sha2.js');
  const bytes = new TextEncoder().encode('exact text\n'), digest = Array.from(sha256(bytes), b => b.toString(16).padStart(2, '0')).join('');
  let release!: () => void, cancelled = false;
  const reads = new ComparisonReads(), blocks: Array<[string, boolean]> = [];
  const client = { fileStream: async () => ({ body: new ReadableStream({ start(c) { c.enqueue(bytes); c.close(); } }), cancel() { cancelled = true; } }) } as unknown as HaeryongClient;
  const pending = reads.text(client, 'v', { ...file('a', digest), byte_size: String(bytes.length) }, (text, done) => blocks.push([text, done]), () => new Promise(resolve => { release = resolve; }));
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(blocks, [['exact text\n', false]]); release(); await pending;
  assert.deepEqual(blocks, [['exact text\n', false], ['', true]]); assert.equal(cancelled, false);
});
test("R1 text decode failure before EOF immediately cancels its reader and owned stream", async () => {
  let readerCancelled = 0, streamCancelled = 0;
  const stream: PackageStream = {
    body: new ReadableStream({ start(c) { c.enqueue(new Uint8Array([0xff])); }, cancel() { readerCancelled++; } }),
    cancel() { streamCancelled++; },
  };
  const reads = new ComparisonReads();
  const client = { fileStream: async () => stream } as unknown as HaeryongClient;
  await assert.rejects(reads.text(client, "v", file("malformed.txt"), () => assert.fail("malformed text was emitted")), TypeError);
  assert.equal(readerCancelled, 1);
  assert.equal(streamCancelled, 1);
  assert.equal(reads.live, true, "failure cleanup must not require disposing the pair");
});
test("R1 metadata-not-supplied is distinct from an unavailable version with a supplied digest", async () => {
  for (const [reason, expected] of [["metadata_not_supplied", "absent"], ["version_unavailable", "error"], [undefined, "error"]] as const) {
    const version = { version_id: "v", supplied_metadata_sha256: reason === "metadata_not_supplied" ? null : "a".repeat(64) } as VersionView;
    const client = { versionSuppliedMetadata: async () => { throw new ApiError(404, "NOT_FOUND", false, 0, reason ? { reason } : {}); } } as unknown as HaeryongClient;
    const box = await loadComparisonMetadata(client, version, new ComparisonReads());
    assert.equal(box.state.kind, expected);
    assert.equal(box.raw, null);
  }
});
