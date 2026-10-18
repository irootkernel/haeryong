import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import type { ContextBinding, ContextClient, ContextReceipt } from "../client/context-types.ts";
import type { DecisionResult, EntryChunk, ListPage, UploadCheckpoint, UploadConfirmedRange, UploadDetail } from "../client/types.ts";
import { ApiError } from "../client/types.ts";
import { BrowserContextJournal, ContextTransferHost, freshContextVerifier, type ContextJournalStore } from "./context-transfer.ts";
import { SnapshotCursor } from "./snapshot.ts";

const sum = (bytes: Uint8Array) => createHash("sha256").update(bytes).digest("hex");
const binding: ContextBinding = { version_id: "00000000-0000-4000-8000-000000000001", page_path: "pages/한글.html", render_instance_id: "00000000-0000-4000-8000-000000000002" };
const authority = { libraryId: "library", principalId: "owner", sessionId: "device-session" };
const checkpoint = (): UploadCheckpoint => ({ generation: 1, inventory_sha256: "a".repeat(64), accepted_bytes: "0", complete_input: false });
function journal(): ContextJournalStore {
  const records = new Map<string, string>();
  return new BrowserContextJournal({ getItem: (key) => records.get(key) ?? null, setItem: (key, value) => { records.set(key, value); } });
}

class Server implements ContextClient {
  bytes: Uint8Array[] = [];
  size = 0;
  ranges: UploadConfirmedRange[] = [];
  state = "open";
  createKeys: string[] = [];
  sealKeys: string[] = [];
  consumeKeys: string[] = [];
  failCreate = false; failChunk = false; failSeal = false; failConsume = false;
  storedReceipt: ContextReceipt | null = null;
  freshFailures = 0;
  freshProofs: Array<{ ref: string; binding: ContextBinding; digest: string }> = [];
  async verifyContextFresh(contextRef: string, actual: ContextBinding, bytesSha256: string) {
    this.freshProofs.push({ ref: contextRef, binding: actual, digest: bytesSha256 });
    if (this.freshFailures > 0) {
      this.freshFailures--;
      throw new ApiError(412, "PRECONDITION_FAILED", false, 0, {});
    }
  }
  async createContextUpload(actual: ContextBinding, key: string) {
    assert.deepEqual(actual, binding); this.createKeys.push(key);
    if (this.failCreate) { this.failCreate = false; throw new Error("create reply lost after commit"); }
    return { upload_id: "upload", checkpoint: checkpoint() };
  }
  async uploadRanges(): Promise<ListPage<UploadConfirmedRange>> { return { items: this.ranges, next_cursor: null }; }
  async uploadDetail(): Promise<UploadDetail> {
    return { state: this.state, error: null, sealed_object_ref: this.state === "sealed" ? { bytes_sha256: this.digest(), byte_size: String(this.size) } : null } as UploadDetail;
  }
  async putUploadEntry(upload: string, chunk: EntryChunk) {
    assert.equal(upload, "upload"); assert.equal(chunk.path, "_input");
    if (chunk.bytes.length) {
      if (chunk.offset === this.size) {
        this.bytes.push(chunk.bytes.slice()); this.size += chunk.bytes.length;
        this.ranges.push({ path: "_input", offset: String(chunk.offset), byte_size: String(chunk.bytes.length), bytes_sha256: chunk.chunkSha256 });
      } else {
        const existing = Buffer.concat(this.bytes).subarray(chunk.offset, chunk.offset + chunk.bytes.length);
        assert.deepEqual(Buffer.from(chunk.bytes), existing, "changed replay rejected");
      }
    } else assert.equal(chunk.totalLength, this.size);
    if (this.failChunk) { this.failChunk = false; throw new Error("chunk reply lost after durable write"); }
    return { accepted_offset: String(chunk.offset + chunk.bytes.length), checkpoint: { ...checkpoint(), accepted_bytes: String(this.size) } };
  }
  async completeUpload(upload: string, cp: UploadCheckpoint, key: string): Promise<DecisionResult> {
    assert.equal(upload, "upload"); assert.equal(cp.complete_input, true); this.sealKeys.push(key); this.state = "sealed";
    if (this.failSeal) { this.failSeal = false; throw new Error("seal reply lost after commit"); }
    return { kind: "committed", body: { job_id: "seal-job" } };
  }
  async consumeContext(actual: ContextBinding, upload: string, key: string): Promise<DecisionResult> {
    assert.deepEqual(actual, binding); assert.equal(upload, "upload"); this.consumeKeys.push(key);
    this.storedReceipt ??= { context_ref: "sealed-context", bytes_sha256: this.digest(), media_type: "application/json", created_at: "2026-10-18T00:00:00Z" };
    if (this.failConsume) { this.failConsume = false; throw new Error("consumer reply lost after commit"); }
    return { kind: "committed", body: this.storedReceipt };
  }
  digest() { const hash = createHash("sha256"); for (const bytes of this.bytes) hash.update(bytes); return hash.digest("hex"); }
}

const begin = (host: ContextTransferHost, id: string) => host.handle("context.transfer.begin", { transfer_id: id, media_type: "application/json" });
async function pump(host: ContextTransferHost, cursor: SnapshotCursor, id: string, offset = 0) {
  cursor.resume(offset);
  while (!cursor.done) {
    const start = cursor.offset, bytes = cursor.read(65536);
    if (bytes.length) await host.handle("context.transfer.chunk", { transfer_id: id, offset: start, bytes: bytes.buffer, bytes_sha256: sum(bytes) });
  }
  return host.handle("context.transfer.end", { transfer_id: id, bytes_sha256: cursor.digest() });
}

test("large immutable context uses distinct retained U/S/Q and server-confirmed offsets after response loss", async () => {
  const server = new Server(), store = journal(); let verified = 0;
  const make = () => new ContextTransferHost(server, store, authority, binding, () => {}, async (actual, receipt) => { assert.deepEqual(actual, binding); assert.equal(receipt.context_ref, "sealed-context"); verified++; });
  let host = make(); const id = crypto.randomUUID(); const cursor = new SnapshotCursor({ text: "한글🌊".repeat(1_000_000) });
  server.failCreate = true;
  await assert.rejects(begin(host, id), /reply lost/);
  host = make(); await begin(host, id);
  assert.equal(server.createKeys.length, 2); assert.equal(server.createKeys[0], server.createKeys[1]);
  const bytes = cursor.read(65536); server.failChunk = true;
  await assert.rejects(host.handle("context.transfer.chunk", { transfer_id: id, offset: 0, bytes: bytes.buffer, bytes_sha256: sum(bytes) }), /reply lost/);
  host = make(); const ack = await begin(host, id); assert.equal(ack.payload.next_offset, 65536);
  server.failSeal = true;
  await assert.rejects(pump(host, cursor, id, 65536), /seal reply lost/);
  server.failConsume = true;
  host = make(); await assert.rejects(begin(host, id), /consumer reply lost/);
  host = make(); const ready = await begin(host, id);
  assert.equal(ready.type, "context.transfer.ready"); assert.equal(ready.payload.bytes_sha256, cursor.digest());
  assert.equal(server.consumeKeys[0], server.consumeKeys[1]);
  assert.equal(new Set([server.createKeys[0], server.sealKeys[0], server.consumeKeys[0]]).size, 3);
  assert.equal(verified, 1); assert.equal(await host.acceptsEcho("sealed-context"), true); assert.equal(verified, 2);
  assert.equal(await host.acceptsEcho("foreign-context"), false);
  const otherSession = new ContextTransferHost(server, store, { ...authority, sessionId: "other-session" }, binding, () => {}, async () => {});
  assert.equal(await otherSession.acceptsEcho("sealed-context"), false);
});

test("ready and echo proofs run through the real fresh verifier adapter before any acceptance", async () => {
  const rejectedServer = new Server();
  const host = new ContextTransferHost(rejectedServer, journal(), authority, binding, () => {}, freshContextVerifier(rejectedServer));
  rejectedServer.freshFailures = 1;
  const rejectedId = crypto.randomUUID();
  await begin(host, rejectedId);
  await assert.rejects(pump(host, new SnapshotCursor({ x: 1 }), rejectedId), /CONTEXT_BINDING_REJECTED/);
  assert.equal(rejectedServer.freshProofs.length, 1);
  assert.deepEqual(rejectedServer.freshProofs[0]!.binding, binding);

  const server = new Server();
  const accepted = new ContextTransferHost(server, journal(), authority, binding, () => {}, freshContextVerifier(server));
  const acceptedId = crypto.randomUUID();
  await begin(accepted, acceptedId);
  const ready = await pump(accepted, new SnapshotCursor({ y: 2 }), acceptedId);
  assert.equal(ready.type, "context.transfer.ready");
  const proof = server.freshProofs.at(-1)!;
  assert.equal(proof.ref, "sealed-context");
  assert.equal(proof.digest, server.digest());
  assert.equal(await accepted.acceptsEcho("sealed-context"), true);
  assert.equal(server.freshProofs.length, 2);
  server.freshFailures = 1;
  await assert.rejects(accepted.acceptsEcho("sealed-context"), /CONTEXT_BINDING_REJECTED/);
  assert.equal(server.freshProofs.length, 3);
});

test("journal write failure blocks dispatch; channel disposal and explicit transfer abort retain staged bytes", async () => {
  const server = new Server();
  const blocked = new ContextTransferHost(server, { load: () => null, save: () => { throw new Error("storage denied"); } }, authority, binding, () => {}, async () => {});
  await assert.rejects(begin(blocked, "blocked"), /storage denied/); assert.equal(server.createKeys.length, 0);
  const store = journal(); let active = true;
  const host = new ContextTransferHost(server, store, authority, binding, () => { if (!active) throw new Error("inactive page"); }, async () => {});
  await begin(host, "retained"); const bytes = new TextEncoder().encode("{}");
  const chunk = { transfer_id: "retained", offset: 0, bytes: bytes.buffer, bytes_sha256: sum(bytes) };
  await host.handle("context.transfer.chunk", chunk);
  const replay = await host.handle("context.transfer.chunk", chunk); assert.equal(replay.payload.next_offset, 2);
  await host.handle("context.transfer.abort", { transfer_id: "retained" }); assert.equal(server.size, 2);
  await assert.rejects(begin(host, "retained"), /CLOSED/);
  active = false; await assert.rejects(host.handle("context.transfer.end", { transfer_id: "retained", bytes_sha256: sum(bytes) }), /inactive/);
  assert.equal(server.size, 2); assert.equal(server.consumeKeys.length, 0);
});

test("ready and echo wait for actual server binding verification and discard late inactive callbacks", async () => {
  const server = new Server(); let active = true; let resolve: (() => void) | undefined;
  const verified = new Promise<void>((done) => { resolve = done; });
  const host = new ContextTransferHost(server, journal(), authority, binding, () => { if (!active) throw new Error("inactive page"); }, () => verified);
  const id = crypto.randomUUID(); await begin(host, id);
  let settled = false;
  const result = pump(host, new SnapshotCursor({ x: 1 }), id).finally(() => { settled = true; });
  await new Promise((done) => setImmediate(done));
  assert.equal(server.consumeKeys.length, 1); assert.equal(settled, false);
  active = false; resolve!(); await assert.rejects(result, /inactive/);
});

test("corrupt recovery state cannot dispatch a replacement operation or borrow another session's intent", async () => {
  const server = new Server(), records = new Map<string, string>();
  const storage = { getItem: (key: string) => records.get(key) ?? null, setItem: (key: string, value: string) => { records.set(key, value); } };
  const store = new BrowserContextJournal(storage);
  const make = () => new ContextTransferHost(server, store, authority, binding, () => {}, async () => {});
  await begin(make(), "original");
  const entry = Array.from(records.entries())[0]!;
  const original = JSON.parse(entry[1]) as Record<string, unknown>;
  for (const damage of [
    { ...original, consumeKey: "invalid" },
    { ...original, completeCheckpoint: { complete_input: true } },
    { ...original, authority: { ...authority, sessionId: "other-session" } },
    { ...original, binding: { ...binding, page_path: "other.html" } },
    { ...original, extra: "unknown journal format" },
  ]) {
    records.set(entry[0], JSON.stringify(damage));
    await assert.rejects(begin(make(), "original"), /journal is invalid/);
  }
  assert.equal(server.createKeys.length, 1); assert.equal(server.consumeKeys.length, 0);
});

test("durable resume follows every cursor page, including an empty page, without inferring a prefix across a gap", async () => {
  const server = new Server(), store = journal();
  const make = () => new ContextTransferHost(server, store, authority, binding, () => {}, async () => {});
  const host = make(), id = crypto.randomUUID(); await begin(host, id);
  for (const offset of [0, 4]) {
    const bytes = new TextEncoder().encode("data");
    await host.handle("context.transfer.chunk", { transfer_id: id, offset, bytes: bytes.buffer, bytes_sha256: sum(bytes) });
  }
  let calls = 0;
  server.uploadRanges = async () => {
    calls++;
    return calls === 1 ? { items: [server.ranges[0]!], next_cursor: "second" } : calls === 2 ? { items: [], next_cursor: "third" } : { items: [server.ranges[1]!], next_cursor: null };
  };
  const resumed = await begin(make(), id); assert.equal(resumed.payload.next_offset, 8); assert.equal(calls, 3);
  server.uploadRanges = async () => ({ items: [{ ...server.ranges[0]!, offset: "12" }], next_cursor: null });
  await assert.rejects(begin(make(), id), /STAGING_GAP/);
  assert.equal(server.consumeKeys.length, 0);
});

test("a real page-channel binding is narrowed to the closed context target before dispatch", async () => {
  const server = new Server();
  const pageBinding = { document_id: "document", ...binding };
  const host = new ContextTransferHost(server, journal(), authority, pageBinding, () => {}, freshContextVerifier(server));
  const id = crypto.randomUUID();
  await begin(host, id);
  const receipt = await pump(host, new SnapshotCursor({ from: "Reader page channel" }), id);
  assert.equal(receipt.type, "context.transfer.ready");
  assert.deepEqual(server.freshProofs[0]!.binding, binding);
});
