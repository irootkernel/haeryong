import { sha256 } from "@noble/hashes/sha2.js";
import { ApiError } from "../client/types.ts";
import type { UploadCheckpoint } from "../client/types.ts";
import type { ContextBinding, ContextClient, ContextReceipt } from "../client/context-types.ts";
import { hex } from "./snapshot.ts";

export interface ContextAuthority { libraryId: string; principalId: string; sessionId: string }
export interface ContextJournal {
  format: "haeryong-context-transfer/v1";
  authority: ContextAuthority;
  binding: ContextBinding;
  transferId: string;
  media: "application/json" | "text/plain";
  createKey: string;
  sealKey: string;
  consumeKey: string;
  uploadId: string | null;
  offset: number;
  digest: string | null;
  completeCheckpoint: UploadCheckpoint | null;
  receipt: ContextReceipt | null;
  aborted: boolean;
}
export interface ContextJournalStore {
  load(authority: ContextAuthority, binding: ContextBinding, transferId: string): ContextJournal | null;
  save(record: ContextJournal): void;
}

const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === "object" && !Array.isArray(value);
const sameAuthority = (a: unknown, b: ContextAuthority): boolean => object(a) && a.libraryId === b.libraryId && a.principalId === b.principalId && a.sessionId === b.sessionId;
const sameBinding = (a: unknown, b: ContextBinding): boolean => object(a) && a.version_id === b.version_id && a.page_path === b.page_path && a.render_instance_id === b.render_instance_id;
const digestPattern = /^[a-f0-9]{64}$/;
const operationPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const validReceipt = (value: unknown): value is ContextReceipt => object(value) && typeof value.context_ref === "string" && !!value.context_ref && typeof value.bytes_sha256 === "string" && digestPattern.test(value.bytes_sha256) && ["application/json", "text/plain"].includes(String(value.media_type)) && typeof value.created_at === "string" && !!value.created_at;
const validCheckpoint = (value: unknown): boolean => object(value) && Number.isSafeInteger(value.generation) && (value.generation as number) > 0 && typeof value.inventory_sha256 === "string" && digestPattern.test(value.inventory_sha256) && typeof value.accepted_bytes === "string" && /^(0|[1-9][0-9]*)$/.test(value.accepted_bytes) && value.complete_input === true;
const journalKey = (a: ContextAuthority, b: ContextBinding, id: string): string => "haeryong.context-transfer.v1:" + JSON.stringify([a.libraryId, a.principalId, a.sessionId, b.version_id, b.page_path, b.render_instance_id, id]);

export class BrowserContextJournal implements ContextJournalStore {
  readonly storage: Pick<Storage, "getItem" | "setItem">;
  constructor(storage: Pick<Storage, "getItem" | "setItem">) { this.storage = storage; }
  load(authority: ContextAuthority, binding: ContextBinding, transferId: string): ContextJournal | null {
    const raw = this.storage.getItem(journalKey(authority, binding, transferId));
    if (raw === null) return null;
    const record = JSON.parse(raw) as ContextJournal;
    const fields = ["format", "authority", "binding", "transferId", "media", "createKey", "sealKey", "consumeKey", "uploadId", "offset", "digest", "completeCheckpoint", "receipt", "aborted"];
    if (!object(record) || Object.keys(record).length !== fields.length || !fields.every((key) => Object.hasOwn(record, key)) || record.format !== "haeryong-context-transfer/v1" || !sameAuthority(record.authority, authority) || !sameBinding(record.binding, binding) || record.transferId !== transferId || !Number.isSafeInteger(record.offset) || record.offset < 0 || ![record.createKey, record.sealKey, record.consumeKey].every((key) => typeof key === "string" && operationPattern.test(key)) || !["application/json", "text/plain"].includes(record.media) || typeof record.aborted !== "boolean" || !(record.uploadId === null || (typeof record.uploadId === "string" && record.uploadId)) || !(record.digest === null || (typeof record.digest === "string" && digestPattern.test(record.digest))) || !(record.completeCheckpoint === null || validCheckpoint(record.completeCheckpoint)) || !(record.receipt === null || validReceipt(record.receipt))) throw new Error("Context recovery journal is invalid; dispatch blocked");
    return record;
  }
  save(record: ContextJournal): void {
    // A failure is deliberately propagated before every dependent dispatch.
    this.storage.setItem(journalKey(record.authority, record.binding, record.transferId), JSON.stringify(record));
  }
}

export class ContextTransferError extends Error {
  readonly code: string;
  readonly retryable: boolean;
  constructor(code: string, retryable: boolean) { super(code); this.code = code; this.retryable = retryable; }
}
export type ContextReply = { type: "context.transfer.ack" | "context.transfer.ready"; payload: Record<string, unknown> };

// Real fresh-proof adapter connecting the host's mandatory verification
// callback to the shared client's closed server proof. It runs before the
// ready reply is emitted and again before every echo acceptance, after the
// caller's guard checks. A PRECONDITION_FAILED proof is the typed binding
// rejection; every other failure keeps the server's own retryability.
export function freshContextVerifier(client: ContextClient): (binding: ContextBinding, receipt: ContextReceipt) => Promise<void> {
  return async (binding, receipt) => {
    try {
      await client.verifyContextFresh(receipt.context_ref, binding, receipt.bytes_sha256);
    } catch (error) {
      if (error instanceof ApiError && error.status === 412) throw new ContextTransferError("CONTEXT_BINDING_REJECTED", false);
      throw error;
    }
  };
}

const receipt = (value: unknown): ContextReceipt => {
  if (!validReceipt(value)) throw new ContextTransferError("CONTEXT_OUTCOME_UNKNOWN", true);
  return value;
};

// One instance belongs to an active page channel. The journal survives channel
// loss; this object's disposal never abandons uploads or deletes context pins.
export class ContextTransferHost {
  readonly #client: ContextClient;
  readonly #journal: ContextJournalStore;
  readonly #authority: ContextAuthority;
  readonly #binding: ContextBinding;
  readonly #guard: () => void;
  readonly #verify: (binding: ContextBinding, receipt: ContextReceipt) => Promise<void>;
  readonly #wait: (ms: number) => Promise<void>;
  #records = new Map<string, ContextJournal>();
  #queues = new Map<string, Promise<unknown>>();

  constructor(client: ContextClient, journal: ContextJournalStore, authority: ContextAuthority, binding: ContextBinding, guard: () => void, verify: (binding: ContextBinding, receipt: ContextReceipt) => Promise<void>, wait: (ms: number) => Promise<void> = (ms) => new Promise((resolve) => setTimeout(resolve, ms))) {
    this.#client = client; this.#journal = journal;
    this.#authority = { ...authority };
    // A page-channel binding also carries document_id. The context target
    // has its own closed three-member wire schema; never spread that DTO.
    this.#binding = { version_id: binding.version_id, page_path: binding.page_path, render_instance_id: binding.render_instance_id };
    this.#guard = guard; this.#verify = verify; this.#wait = wait;
  }

  handle(type: string, payload: Record<string, unknown>): Promise<ContextReply> {
    const id = payload.transfer_id;
    if (typeof id !== "string" || !id) return Promise.reject(new ContextTransferError("INVALID_CONTEXT_TRANSFER", false));
    const previous = this.#queues.get(id) ?? Promise.resolve();
    const result = previous.catch(() => undefined).then(() => { this.#guard(); return this.#handle(type, payload, id); });
    this.#queues.set(id, result);
    void result.finally(() => { if (this.#queues.get(id) === result) this.#queues.delete(id); }).catch(() => undefined);
    return result;
  }

  #save(record: ContextJournal): void { this.#guard(); this.#journal.save(record); }
  #ack(record: ContextJournal): ContextReply { return { type: "context.transfer.ack", payload: { transfer_id: record.transferId, next_offset: record.offset, credit_bytes: 65536 } }; }

  async #handle(type: string, payload: Record<string, unknown>, id: string): Promise<ContextReply> {
    let record = this.#records.get(id);
    if (type === "context.transfer.begin") {
      if (payload.media_type !== "application/json" && payload.media_type !== "text/plain") throw new ContextTransferError("INVALID_CONTEXT_MEDIA", false);
      record ??= this.#journal.load(this.#authority, this.#binding, id) ?? undefined;
      if (record && record.media !== payload.media_type) throw new ContextTransferError("CONTEXT_INTENT_CHANGED", false);
      if (!record) {
        record = { format: "haeryong-context-transfer/v1", authority: { ...this.#authority }, binding: { ...this.#binding }, transferId: id, media: payload.media_type, createKey: crypto.randomUUID(), sealKey: crypto.randomUUID(), consumeKey: crypto.randomUUID(), uploadId: null, offset: 0, digest: null, completeCheckpoint: null, receipt: null, aborted: false };
        this.#save(record);
      }
      this.#records.set(id, record);
      if (record.aborted) throw new ContextTransferError("CONTEXT_TRANSFER_CLOSED", false);
      if (!record.uploadId) {
        this.#save(record);
        const created = await this.#client.createContextUpload(record.binding, record.createKey);
        this.#guard();
        if (!created || typeof created.upload_id !== "string" || !created.upload_id) throw new ContextTransferError("CONTEXT_CREATE_OUTCOME_UNKNOWN", true);
        record.uploadId = created.upload_id; this.#save(record);
      }
      if (record.digest) return this.#finish(record);
      // HTTP owns durable staging. Reconstruct only actual contiguous ranges,
      // never infer progress from the local journal or accepted-byte totals.
      let offset = 0, previousStart = -1, cursor: string | undefined;
      do {
        const page = await this.#client.uploadRanges(record.uploadId, "_input", cursor); this.#guard();
        for (const range of page.items) {
          const start = Number(range.offset), length = Number(range.byte_size);
          if (range.path !== "_input" || !Number.isSafeInteger(start) || !Number.isSafeInteger(length) || start < 0 || length < 0 || !Number.isSafeInteger(start + length)) throw new ContextTransferError("CONTEXT_STAGING_INVALID", false);
          // The authoritative cursor order is path/offset. Fold each page
          // without retaining a second in-memory ledger of all chunk ranges.
          if (start < previousStart || start > offset) throw new ContextTransferError("CONTEXT_STAGING_GAP", false);
          previousStart = start;
          offset = Math.max(offset, start + length);
        }
        cursor = page.next_cursor ?? undefined;
      } while (cursor);
      record.offset = offset; this.#save(record);
      return this.#ack(record);
    }
    if (!record || !record.uploadId) throw new ContextTransferError("CONTEXT_TRANSFER_INTERRUPTED", true);
    if (record.aborted) throw new ContextTransferError("CONTEXT_TRANSFER_CLOSED", false);
    if (type === "context.transfer.chunk") {
      if (!(payload.bytes instanceof ArrayBuffer) || !Number.isSafeInteger(payload.offset) || (payload.offset as number) < 0 || (payload.offset as number) > record.offset || typeof payload.bytes_sha256 !== "string") throw new ContextTransferError("CONTEXT_CHUNK_SEQUENCE", false);
      const bytes = new Uint8Array(payload.bytes);
      if (!bytes.length || bytes.length > 65536 || hex(sha256(bytes)) !== payload.bytes_sha256) throw new ContextTransferError("CONTEXT_CHUNK_DIGEST", false);
      this.#save(record);
      const start = payload.offset as number;
      const ack = await this.#client.putUploadEntry(record.uploadId, { path: "_input", kind: "file", offset: start, bytes, chunkSha256: payload.bytes_sha256, final: false });
      this.#guard();
      if (Number(ack.accepted_offset) !== start + bytes.length) throw new ContextTransferError("CONTEXT_ACK_MISMATCH", false);
      record.offset = Math.max(record.offset, start + bytes.length); this.#save(record); return this.#ack(record);
    }
    if (type === "context.transfer.end") {
      if (typeof payload.bytes_sha256 !== "string" || !/^[a-f0-9]{64}$/.test(payload.bytes_sha256)) throw new ContextTransferError("CONTEXT_FINAL_DIGEST", false);
      if (record.digest && record.digest !== payload.bytes_sha256) throw new ContextTransferError("CONTEXT_INTENT_CHANGED", false);
      record.digest = payload.bytes_sha256; this.#save(record);
      return this.#finish(record);
    }
    if (type === "context.transfer.abort") {
      // Stop the channel transfer only. Explicit staging abandonment is a
      // separate owner action; channel loss proves no authority to delete it.
      record.aborted = true; this.#save(record);
      return { type: "context.transfer.ack", payload: { transfer_id: id, closed: true } };
    }
    throw new ContextTransferError("INVALID_CONTEXT_MESSAGE", false);
  }

  async #finish(record: ContextJournal): Promise<ContextReply> {
    if (!record.uploadId || !record.digest) throw new ContextTransferError("CONTEXT_TRANSFER_INTERRUPTED", true);
    let detail = await this.#client.uploadDetail(record.uploadId); this.#guard();
    if (detail.state === "open") {
      if (!record.completeCheckpoint) {
        this.#save(record);
        const ack = await this.#client.putUploadEntry(record.uploadId, { path: "_input", kind: "file", offset: record.offset, bytes: new Uint8Array(0), chunkSha256: hex(sha256(new Uint8Array(0))), totalLength: record.offset, final: true });
        this.#guard();
        record.completeCheckpoint = { ...ack.checkpoint, generation: ack.checkpoint.generation + 1, complete_input: true }; this.#save(record);
      }
      this.#save(record);
      await this.#client.completeUpload(record.uploadId, record.completeCheckpoint, record.sealKey); this.#guard();
      detail = await this.#client.uploadDetail(record.uploadId); this.#guard();
    }
    while (detail.state === "sealing") {
      if (detail.error) throw new ContextTransferError(detail.error, false);
      await this.#wait(200); this.#guard();
      detail = await this.#client.uploadDetail(record.uploadId); this.#guard();
    }
    if (detail.state !== "sealed" || detail.sealed_object_ref?.bytes_sha256 !== record.digest || Number(detail.sealed_object_ref.byte_size) !== record.offset) throw new ContextTransferError("CONTEXT_SEAL_MISMATCH", false);
    this.#save(record);
    // Replaying Q verifies the original outcome; no new ID after response loss.
    const outcome = await this.#client.consumeContext(record.binding, record.uploadId, record.consumeKey); this.#guard();
    if (outcome.kind !== "committed") throw new ContextTransferError("CONTEXT_CONSUMER_PENDING", true);
    const ready = receipt(outcome.body);
    if (ready.bytes_sha256 !== record.digest || (record.media === "application/json" && ready.media_type !== "application/json")) throw new ContextTransferError("CONTEXT_RECEIPT_MISMATCH", false);
    await this.#verify(record.binding, ready); this.#guard();
    record.receipt = ready; this.#save(record);
    return { type: "context.transfer.ready", payload: { transfer_id: record.transferId, ...ready } };
  }

  async acceptsEcho(ref: string): Promise<boolean> {
    this.#guard();
    const record = Array.from(this.#records.values()).find((record) => record.receipt?.context_ref === ref && sameAuthority(record.authority, this.#authority) && sameBinding(record.binding, this.#binding));
    if (!record?.receipt) return false;
    await this.#verify(record.binding, record.receipt); this.#guard();
    return true;
  }
}

export function contextFailure(error: unknown): { code: string; retryable: boolean } {
  if (error instanceof ContextTransferError) return { code: error.code, retryable: error.retryable };
  if (error instanceof ApiError) return { code: error.code, retryable: error.retryable };
  return { code: "CONTEXT_TRANSFER_INTERRUPTED", retryable: true };
}
