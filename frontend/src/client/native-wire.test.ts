import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { createNativePort, encodeBytes } from "./native-wire.ts";
import { ApiError, UncertainResponseError } from "./types.ts";

test("Wails JSON preserves every byte including a nonempty final EOF chunk", async () => {
  const expected = Uint8Array.from({ length: 256 * 513 }, (_, i) => i % 256), cancelled: string[] = [];
  let offset = 0;
  const port = createNativePort({ async invoke(operation, payload) {
    // Force a real JSON round trip rather than handing typed arrays/functions
    // through a mock which could hide the actual Wails serialization boundary.
    const args = JSON.parse(JSON.stringify(payload));
    if (operation === "packageStream") return { ok: true, value: { stream_id: "owned" } };
    if (operation === "streamCancel") { cancelled.push(args.stream_id); return { ok: true, value: null }; }
    assert.equal(operation, "streamRead"); assert.equal(args.stream_id, "owned"); assert.ok(args.max_bytes >= 1 && args.max_bytes <= 4 * 1024 * 1024);
    const bytes = expected.subarray(offset, offset + args.max_bytes); offset += bytes.length;
    return JSON.parse(JSON.stringify({ ok: true, value: { data_b64: encodeBytes(bytes), eof: offset === expected.length } }));
  } });
  const stream = await port.call("packageStream", { version_id: "version" });
  const hash = createHash("sha256"), reader = stream.body.getReader(); let total = 0;
  for (;;) { const part = await reader.read(); if (part.done) break; hash.update(part.value); total += part.value.length; }
  assert.equal(total, expected.length); assert.equal(hash.digest("hex"), createHash("sha256").update(expected).digest("hex"));
  stream.cancel(); assert.deepEqual(cancelled, ["owned"]);
});

test("native upload byte chunks are canonical base64 JSON, never numeric-key objects or text", async () => {
  const bytes = Uint8Array.from({ length: 256 }, (_, i) => i);
  const port = createNativePort({ async invoke(operation, payload) {
    assert.equal(operation, "putUploadEntry");
    const wire = JSON.parse(JSON.stringify(payload));
    assert.equal(wire.chunk.bytes, undefined);
    assert.equal(wire.chunk.bytes_b64, Buffer.from(bytes).toString("base64"));
    return { ok: true, value: { accepted_offset: "256" } };
  } });
  await port.call("putUploadEntry", { upload_id: "upload", chunk: { path: "_input", kind: "file", offset: 0, bytes, chunkSha256: "digest", final: false } });
});

test("native midstream API errors remain typed and close the owned stream", async () => {
  let closed = 0;
  const port = createNativePort({ async invoke(operation) {
    if (operation === "fileStream") return { ok: true, value: { stream_id: "owned" } };
    if (operation === "streamCancel") { closed++; return { ok: true, value: null }; }
    return { ok: false, error: { status: 401, code: "SESSION_REVOKED", retryable: false } };
  } });
  const stream = await port.call("fileStream", { version_id: "version", path: "arbitrary.bin" });
  await assert.rejects(stream.body.getReader().read(), (error) => error instanceof ApiError && error.code === "SESSION_REVOKED");
  await new Promise((resolve) => setImmediate(resolve)); assert.equal(closed, 1);
});

test("an unreadable committed native body maps to the uncertain K-recovery error", async () => {
  const port = createNativePort({ async invoke(operation) {
    if (operation === "decidePairing") return { ok: false, error: { status: 200, code: "UNCERTAIN_RESPONSE", retryable: true } };
    throw new Error("unexpected operation " + operation);
  } });
  await assert.rejects(
    port.call("decidePairing", { pairing_id: "p", code: "1", origin: "http://127.0.0.1:9270", etag: '"e"', operation_id: "k", action: "approve" }),
    (error) => error instanceof UncertainResponseError,
  );
});

test("cancelling a pending native read closes only its stream and discards its late EOF bytes", async () => {
  let finish!: (value: unknown) => void, closed = 0;
  const pending = new Promise((resolve) => { finish = resolve; });
  const port = createNativePort({ async invoke(operation) {
    if (operation === "fileStream") return { ok: true, value: { stream_id: "captured" } };
    if (operation === "streamCancel") { closed++; return { ok: true, value: null }; }
    return pending;
  } });
  const stream = await port.call("fileStream", { version_id: "version", path: "file.bin" });
  const read = stream.body.getReader().read();
  const rejected = assert.rejects(read, (error) => error instanceof DOMException && error.name === "AbortError");
  stream.cancel(); finish({ ok: true, value: { data_b64: "AAEC", eof: true } });
  await rejected; await new Promise((resolve) => setImmediate(resolve)); assert.equal(closed, 1);
});

// TASK-003-04: the metadata open reply carries the strong ETag beside the
// stream handle; the wire reifies both together and rejects malformed values.
test("versionSuppliedMetadata converts {stream_id, etag} into a guarded stream plus validator", async () => {
  const port = createNativePort({ async invoke(operation) {
    assert.equal(operation, "versionSuppliedMetadata");
    return { ok: true, value: { stream_id: "s1", etag: '"sha256-abc"' } };
  } });
  const opened = await port.call("versionSuppliedMetadata", { version_id: "v1" });
  assert.equal(opened.etag, '"sha256-abc"');
  assert.equal(typeof opened.cancel, "function");
  opened.cancel();
});

test("versionSuppliedMetadata rejects a malformed validator and keeps null explicit", async () => {
  const bad = createNativePort({ async invoke() { return { ok: true, value: { stream_id: "s1", etag: 7 } }; } });
  await assert.rejects(bad.call("versionSuppliedMetadata", { version_id: "v1" }), UncertainResponseError);
  const empty = createNativePort({ async invoke() { return { ok: true, value: { stream_id: "s1", etag: null } }; } });
  const opened = await empty.call("versionSuppliedMetadata", { version_id: "v1" });
  assert.equal(opened.etag, null);
  opened.cancel();
});
