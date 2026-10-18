// Transfer engine for the shared import control. Owns the durable
// authority-scoped caller journal, distinct U/S/I operation identities, the
// acknowledgement-driven upload loop, server-range-anchored reselection
// verification and the import receipt reconciliation state machine.
//
// Contract anchors (docs/contracts/import-workflow.md, package.md):
// - The journal persists resolved target/base, U/S/I, phase, the pending
//   chunk intent and the exact completion checkpoint BEFORE each protected
//   dispatch; a journal save failure blocks that dispatch.
// - Reselection verifies EVERY individual server-confirmed range (holes are
//   legal server state) against the reselected bytes across all cursor
//   pages, re-verifying AFTER hashing when the upload advanced; an advanced
//   checkpoint is never accepted after the retry budget. Journal knowledge
//   never limits the checks.
// - No entry reset API exists: a mismatched/missing retained entry blocks
//   same-upload resume; only an explicit owner decision starts a new upload
//   (new U, new I) or abandons. Unresolved original records are preserved,
//   never overwritten by a new operation.
// - Completion replays the SAME S key with the SAME stored checkpoint after
//   response loss; a fresh checkpoint is never paired with an old key.
// - 202 receipts are durable accepted work, never publication. Publication
//   is marked only after the import job completes and the version view is
//   confirmed. The completed operation carries its original body in
//   result_metadata; the top-level job_id stays null on the real wire.
import type {
  DecisionResult,
  HaeryongClient,
  ImportTargetMode,
  JobView,
  OperationView,
  UploadCheckpoint,
  UploadConfirmedRange,
  UploadDetail,
} from "./types.ts";
import { ApiError } from "./types.ts";
import { sha256 } from "@noble/hashes/sha2.js";

export class JournalWriteError extends Error {
  constructor(cause: unknown) {
    super("import journal could not be saved; the dependent dispatch was blocked");
    this.name = "JournalWriteError";
    if (cause !== undefined) this.cause = cause;
  }
}

export class JournalReadError extends Error {
  constructor(cause: unknown) {
    super("import recovery records could not be read");
    this.name = "JournalReadError";
    if (cause !== undefined) this.cause = cause;
  }
}

/** The selected target does not match the document's actual project. */
export class TargetMismatchError extends Error {
  constructor(detail: string) {
    super(detail);
    this.name = "TargetMismatchError";
  }
}

/** A retained range no longer matches the reselected input; same-upload resume is blocked. */
export class ReselectionMismatchError extends Error {
  readonly detail: string;
  constructor(detail: string) {
    super("reselected input does not match retained staging; a new upload or abandon is required");
    this.name = "ReselectionMismatchError";
    this.detail = detail;
  }
}

/** The upload moved while it was being verified; nothing was sent. */
export class ConcurrentUploadError extends Error {
  constructor() {
    super("another writer kept advancing this upload; verification never reached a stable state");
    this.name = "ConcurrentUploadError";
  }
}

export class TransferCancelled extends Error {
  constructor() {
    super("transfer cancelled; staging, journal and intent are retained");
    this.name = "TransferCancelled";
  }
}

export class ImportJobError extends Error {
  readonly code: string;
  constructor(code: string) {
    super(`import job failed: ${code}`);
    this.name = "ImportJobError";
    this.code = code;
  }
}

/**
 * A protected preparation dispatch failed AFTER its journal record was
 * durably saved; the exact record is attached so the caller can offer the
 * original U/S/I recovery instead of starting a new operation.
 */
export class DispatchRecordError extends Error {
  readonly journal: ImportJournal;
  constructor(journal: ImportJournal, cause: unknown) {
    super("protected dispatch failed after the journal record was saved");
    this.name = "DispatchRecordError";
    this.journal = journal;
    if (cause !== undefined) this.cause = cause;
  }
}

export interface JournalChunk {
  offset: number;
  length: number;
  digest: string;
  /** False while the chunk is dispatched but unacknowledged (ack loss). */
  acked: boolean;
}

export interface JournalEntryRecord {
  kind: "file" | "directory" | "symlink";
  declaredSize?: number;
  declaredSha256?: string;
  linkTarget?: string;
  chunks: JournalChunk[];
  finalAcked: boolean;
}

export type JournalPhase =
  | "prepared"
  | "transferring"
  | "transfer-cancelled"
  | "completing"
  | "sealing"
  | "sealed"
  | "dispatching"
  | "published";

interface TransferJournalData {
  libraryId: string;
  principalId: string;
  uploadId: string | null;
  completeKey: string;
  inputMode: "directory" | "archive" | "file";
  selectionName: string | null;
  phase: JournalPhase;
  entries: Record<string, JournalEntryRecord>;
  createdAt: string;
  updatedAt: string;
  jobId: string | null;
  versionId: string | null;
  /** Exact checkpoint stored before the complete dispatch; replayed as-is. */
  completeCheckpoint: UploadCheckpoint | null;
}

/** A remembered publication intent, or recovery of staging with no known intent. */
export type ImportJournal = TransferJournalData & (
  | {
      stagingOnly: false;
      createKey: string | null;
      importKey: string;
      target: { projectId: string; documentId: string | null; baseVersionId: string | null };
    }
  | {
      stagingOnly: true;
      uploadId: string;
      createKey: null;
      importKey: null;
      target: { projectId: string; documentId: string | null; baseVersionId?: never };
    }
);

/** Staging has its own local key; it never impersonates a publication operation. */
export function journalKey(record: ImportJournal): string {
  return record.importKey ?? `staging:${record.uploadId}`;
}

export interface AuthorityJournals {
  active: string | null;
  records: ImportJournal[];
}

/**
 * Small per-authority recovery records, not a workflow engine: unresolved
 * originals are preserved by operation identity when a new operation starts.
 */
export interface JournalStore {
  save(record: ImportJournal): void;
  setActive(authority: string, importKey: string | null): void;
  load(authority: string): AuthorityJournals;
  discard(authority: string, importKey: string): void;
}

export function journalAuthority(libraryId: string, principalId: string): string {
  return `${libraryId}:${principalId}`;
}

interface JournalBucket {
  active: string | null;
  records: Record<string, ImportJournal>;
}

/** localStorage-backed journal; any unavailable storage blocks dispatch. */
export class BrowserJournalStore implements JournalStore {
  #key(authority: string): string {
    return `haeryong.import-journal.v1:${authority}`;
  }
  #read(authority: string): JournalBucket {
    let raw: string | null | undefined;
    try {
      raw = globalThis.localStorage?.getItem(this.#key(authority));
    } catch (cause) {
      throw new JournalReadError(cause);
    }
    if (!raw) return { active: null, records: {} };
    try {
      const parsed = JSON.parse(raw) as JournalBucket;
      return { active: parsed.active ?? null, records: parsed.records ?? {} };
    } catch {
      return { active: null, records: {} };
    }
  }
  #write(authority: string, bucket: JournalBucket): void {
    try {
      const storage = globalThis.localStorage;
      if (!storage) throw new Error("localStorage unavailable");
      storage.setItem(this.#key(authority), JSON.stringify(bucket));
    } catch (cause) {
      throw new JournalWriteError(cause);
    }
  }
  save(record: ImportJournal): void {
    const authority = journalAuthority(record.libraryId, record.principalId);
    const bucket = this.#read(authority);
    bucket.records[journalKey(record)] = record;
    this.#write(authority, bucket);
  }
  setActive(authority: string, importKey: string | null): void {
    const bucket = this.#read(authority);
    bucket.active = importKey;
    this.#write(authority, bucket);
  }
  load(authority: string): AuthorityJournals {
    const bucket = this.#read(authority);
    return { active: bucket.active, records: Object.values(bucket.records) };
  }
  discard(authority: string, importKey: string): void {
    const bucket = this.#read(authority);
    delete bucket.records[importKey];
    if (bucket.active === importKey) bucket.active = null;
    this.#write(authority, bucket);
  }
}

/** Honest picker-surface representation limits disclosed by the planner. */
export interface InputLimits {
  emptyDirectoriesRepresented: boolean;
  symlinksRepresented: boolean;
}

export interface PlannedEntry {
  path: string;
  kind: "file" | "directory" | "symlink";
  size?: number;
  linkTarget?: string;
  file?: File;
}

export interface PlannedInput {
  mode: "directory" | "archive" | "file";
  selectionName: string;
  entries: PlannedEntry[];
  limits: InputLimits;
}

/**
 * Plan a directory payload from a real folder FileList. Payload paths are
 * relative to the selected result root (the folder itself), the selection
 * name preserves the folder name, and implied parent directories become
 * directory entries. A FileList cannot represent empty directories or
 * symlinks; that limit is carried for disclosure.
 */
export function planFromFolder(files: File[], folderName: string): PlannedInput {
  const planned = new Map<string, PlannedEntry>();
  const parents = new Set<string>();
  const addParents = (path: string) => {
    let index = path.indexOf("/");
    while (index > 0) {
      parents.add(path.slice(0, index));
      index = path.indexOf("/", index + 1);
    }
  };
  for (const file of files) {
    const relative = (file as File & { webkitRelativePath?: string }).webkitRelativePath || file.name;
    const withoutRoot = relative.includes("/") ? relative.slice(relative.indexOf("/") + 1) : file.name;
    if (withoutRoot === "") continue;
    addParents(withoutRoot);
    planned.set(withoutRoot, { path: withoutRoot, kind: "file", size: file.size, file });
  }
  for (const parent of parents) {
    if (!planned.has(parent)) planned.set(parent, { path: parent, kind: "directory" });
  }
  const entries = [...planned.values()].sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));
  return {
    mode: "directory",
    selectionName: folderName,
    entries,
    limits: { emptyDirectoriesRepresented: false, symlinksRepresented: false },
  };
}

/** Plan one streamed archive (ZIP) input under the service transport label. */
export function planFromArchive(file: File): PlannedInput {
  return {
    mode: "archive",
    selectionName: file.name,
    entries: [{ path: TRANSPORT_ENTRY, kind: "file", size: file.size, file }],
    limits: { emptyDirectoriesRepresented: true, symlinksRepresented: true },
  };
}

/** Plan one single-file (HTML) convenience input. */
export function planFromSingleFile(file: File): PlannedInput {
  return {
    mode: "file",
    selectionName: file.name,
    entries: [{ path: TRANSPORT_ENTRY, kind: "file", size: file.size, file }],
    limits: { emptyDirectoriesRepresented: false, symlinksRepresented: false },
  };
}

/** Fixed transport entry label used by archive/file/object input modes. */
export const TRANSPORT_ENTRY = "_input";

export async function sha256Bytes(bytes: Uint8Array): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", bytes as unknown as ArrayBuffer);
  return toHex(new Uint8Array(digest));
}

export async function sha256Range(file: File, offset: number, length: number, check?: () => void): Promise<string> {
  if (!Number.isSafeInteger(offset) || !Number.isSafeInteger(length) || offset < 0 || length < 0 || offset > file.size || length > file.size - offset) {
    throw new ReselectionMismatchError("the requested byte range cannot be read exactly from the selected file");
  }
  const hash = sha256.create();
  const end = offset + length;
  for (let at = offset; at < end; at += 1024 * 1024) {
    check?.();
    const size = Math.min(1024 * 1024, end - at);
    const bytes = new Uint8Array(await file.slice(at, at + size).arrayBuffer());
    check?.();
    if (bytes.byteLength !== size) throw new ReselectionMismatchError("the selected file returned a truncated byte range");
    hash.update(bytes);
  }
  check?.();
  return toHex(hash.digest());
}

function toHex(bytes: Uint8Array): string {
  let out = "";
  for (const byte of bytes) out += byte.toString(16).padStart(2, "0");
  return out;
}

export interface Interval {
  start: number;
  end: number;
}

export function mergeIntervals(list: Interval[]): Interval[] {
  const sorted = [...list].sort((a, b) => a.start - b.start);
  const merged: Interval[] = [];
  for (const item of sorted) {
    const last = merged[merged.length - 1];
    if (last && item.start <= last.end) last.end = Math.max(last.end, item.end);
    else merged.push({ ...item });
  }
  return merged;
}

/** Exact unsent gaps given confirmed coverage and the total size. */
export function gapsBetween(covered: Interval[], total: number): Interval[] {
  const gaps: Interval[] = [];
  let cursor = 0;
  for (const item of mergeIntervals(covered)) {
    if (item.start > cursor) gaps.push({ start: cursor, end: Math.min(item.start, total) });
    cursor = Math.max(cursor, item.end);
  }
  if (cursor < total) gaps.push({ start: cursor, end: total });
  return gaps.filter((gap) => gap.end > gap.start || (gap.start === 0 && total === 0));
}

export interface TransferProgress {
  phase: JournalPhase | "verifying";
  ackedBytes: number;
  knownTotalBytes: number | null;
  entriesDone: number;
  entriesTotal: number | null;
  note: string | null;
}

export interface TransferCallbacks {
  onProgress?: (progress: TransferProgress) => void;
  onNotice?: (notice: string) => void;
  /** Polled before every chunk; returning false cancels without abandoning. */
  shouldContinue?: () => boolean;
  /** Synchronous verified-authority gate invoked before every protected dispatch. */
  guard?: () => void;
}

export interface EngineOptions {
  chunkSize?: number;
}

export class TransferEngine {
  readonly #client: HaeryongClient;
  readonly #journal: JournalStore;
  readonly #chunkSize: number;

  constructor(client: HaeryongClient, journal: JournalStore, options: EngineOptions = {}) {
    this.#client = client;
    this.#journal = journal;
    this.#chunkSize = options.chunkSize ?? 1024 * 1024;
  }

  get journal(): JournalStore {
    return this.#journal;
  }

  /**
   * Resolve the target, preserve the owner-displayed pre-transfer base (or
   * capture it once), reject a project/document mismatch explicitly and
   * persist the journal BEFORE creating the upload. A journal write failure
   * leaves nothing dispatched.
   */
  async begin(libraryId: string, principalId: string, target: ImportTargetMode, input: PlannedInput, callbacks: TransferCallbacks = {}): Promise<ImportJournal> {
    let documentId: string | null = null;
    let baseVersionId: string | null = null;
    if (target.kind === "existing") {
      callbacks.guard?.();
      const document = await this.#client.documentDetail(target.documentId);
      if (document.project_id !== target.projectId) {
        throw new TargetMismatchError(
          `selected document ${document.document_id} belongs to project ${document.project_id}, not the chosen project ${target.projectId}`,
        );
      }
      documentId = document.document_id;
      // The base the owner saw before upload wins; capture once otherwise.
      baseVersionId = target.baseVersionId !== undefined ? target.baseVersionId : document.latest_version_id;
      if (baseVersionId === null) throw new TargetMismatchError("the selected existing document has no published base version");
    }
    const now = new Date().toISOString();
    const createKey = crypto.randomUUID();
    const record: ImportJournal = {
      stagingOnly: false,
      libraryId,
      principalId,
      uploadId: null,
      createKey,
      completeKey: crypto.randomUUID(),
      importKey: crypto.randomUUID(),
      target: { projectId: target.projectId, documentId, baseVersionId },
      inputMode: input.mode,
      selectionName: input.selectionName,
      phase: "prepared",
      entries: {},
      createdAt: now,
      updatedAt: now,
      jobId: null,
      versionId: null,
      completeCheckpoint: null,
    };
    // Blocking save: a journal failure must leave nothing dispatched. The
    // synchronous guard runs again after the async document read.
    callbacks.guard?.();
    this.#journal.save(record);
    this.#journal.setActive(journalAuthority(libraryId, principalId), record.importKey);
    try {
      const created = await this.#client.createUpload(
        {
          purpose: "result",
          input_mode: input.mode,
          selection_name: input.selectionName,
          target: { project_id: target.projectId, document_id: documentId },
        },
        createKey,
      );
      record.uploadId = created.upload_id;
      record.phase = "transferring";
      this.#journal.save(record);
      return record;
    } catch (error) {
      // The exact saved prepared record travels with the failure so the
      // caller offers the ORIGINAL U replay, never a new begin/identity.
      throw new DispatchRecordError(record, error);
    }
  }

  /** Continue or restart the acknowledgement-driven transfer loop. */
  async transfer(record: ImportJournal, input: PlannedInput, callbacks: TransferCallbacks = {}, coverage?: Map<string, Interval[]>): Promise<ImportJournal> {
    if (record.uploadId === null) throw new Error("transfer requires an upload");
    if (record.phase === "completing" || record.phase === "sealing" || record.phase === "sealed") {
      // Response-loss replay: the exact stored checkpoint with the same S.
      return this.complete(record, callbacks);
    }
    const guard = async <T>(action: () => Promise<T>): Promise<T> => {
      callbacks.guard?.();
      return action();
    };
    let ackedBytes = [...(coverage?.values() ?? [])].reduce((total, ranges) => total + mergeIntervals(ranges).reduce((sum, range) => sum + range.end - range.start, 0), 0);
    const knownTotalBytes = input.entries.reduce((sum, entry) => sum + (entry.size ?? 0), 0);
    const report = (entriesDone: number, note: string | null = null) =>
      callbacks.onProgress?.({ phase: record.phase, ackedBytes, knownTotalBytes, entriesDone, entriesTotal: input.entries.length, note });
    let entriesDone = 0;
    report(entriesDone);
    for (const entry of input.entries) {
      if (callbacks.shouldContinue && !callbacks.shouldContinue()) {
        record.phase = "transfer-cancelled";
        this.#journal.save(record);
        report(entriesDone);
        throw new TransferCancelled();
      }
      if (entry.kind === "file" && entry.file) {
        await this.#sendFile(record, entry, coverage, guard, callbacks, (chunkBytes) => {
          ackedBytes += chunkBytes;
          report(entriesDone);
        });
      } else {
        await guard(() =>
          this.#client.putUploadEntry(record.uploadId as string, {
            path: entry.path,
            kind: entry.kind,
            offset: 0,
            bytes: new Uint8Array(),
            chunkSha256: "",
            final: false,
            linkTarget: entry.linkTarget,
          }),
        );
        this.#recordEntry(record, entry, [], false);
      }
      entriesDone += 1;
      report(entriesDone);
    }
    if (callbacks.shouldContinue && !callbacks.shouldContinue()) {
      record.phase = "transfer-cancelled";
      this.#journal.save(record);
      throw new TransferCancelled();
    }
    return this.complete(record, callbacks);
  }

  /**
   * Send exactly the unsent gaps of one file, then reaffirm a missing
   * end-of-input marker with a zero-byte EOF PUT. Confirmed coverage is never
   * re-sent with different bytes or skipped over, chunk intent is journaled
   * before dispatch, and progress counts acked bytes only.
   */
  async #sendFile(
    record: ImportJournal,
    entry: PlannedEntry,
    coverage: Map<string, Interval[]> | undefined,
    guard: <T>(action: () => Promise<T>) => Promise<T>,
    callbacks: TransferCallbacks,
    onAck: (bytes: number) => void,
  ): Promise<void> {
    const file = entry.file as File;
    const confirmed: Interval[] = [...(coverage?.get(entry.path) ?? [])];
    const gaps = gapsBetween(confirmed, file.size);
    // One state per exact range intent. A cumulative array appended to the
    // previous saved array duplicates every ACK on each persist.
    const chunkKey = (chunk: JournalChunk) => `${chunk.offset}:${chunk.length}:${chunk.digest}`;
    const chunks = new Map<string, JournalChunk>(
      (record.entries[entry.path]?.chunks ?? []).filter((chunk) => chunk.acked).map((chunk) => [chunkKey(chunk), chunk]),
    );
    const persist = () => {
      record.entries[entry.path] = {
        ...record.entries[entry.path],
        kind: "file",
        declaredSize: file.size,
        chunks: [...chunks.values()],
        finalAcked: record.entries[entry.path]?.finalAcked ?? false,
      };
      record.updatedAt = new Date().toISOString();
      this.#journal.save(record);
    };
    for (const gap of gaps) {
      for (let offset = gap.start; offset < gap.end; offset += this.#chunkSize) {
        if (callbacks.shouldContinue && !callbacks.shouldContinue()) {
          record.phase = "transfer-cancelled";
          this.#journal.save(record);
          throw new TransferCancelled();
        }
        const length = Math.min(this.#chunkSize, gap.end - offset);
        const slice = new Uint8Array(await file.slice(offset, offset + length).arrayBuffer());
        const digest = await sha256Bytes(slice);
        const final = offset + length >= file.size;
        // Persist the dispatch intent before the chunk leaves the browser.
        const key = chunkKey({ offset, length, digest, acked: false });
        chunks.set(key, { offset, length, digest, acked: false });
        persist();
        await guard(() =>
          this.#client.putUploadEntry(record.uploadId as string, {
            path: entry.path,
            kind: "file",
            offset,
            bytes: slice,
            chunkSha256: digest,
            totalLength: file.size,
            final,
          }),
        );
        chunks.set(key, { offset, length, digest, acked: true });
        if (final) record.entries[entry.path].finalAcked = true;
        persist();
        onAck(length);
      }
    }
    // A zero-byte EOF marker changes no retained bytes or range boundaries.
    // Replaying merged/subdivided retained ranges would conflict with the
    // service's exact offset/size/hash replay contract.
    {
      if (!record.entries[entry.path]?.finalAcked) {
        const offset = file.size;
        const length = 0;
        const slice = new Uint8Array();
        const digest = await sha256Bytes(slice);
        if (callbacks.shouldContinue && !callbacks.shouldContinue()) {
          record.phase = "transfer-cancelled";
          this.#journal.save(record);
          throw new TransferCancelled();
        }
        const key = chunkKey({ offset, length, digest, acked: false });
        chunks.set(key, { offset, length, digest, acked: false });
        persist();
        await guard(() =>
          this.#client.putUploadEntry(record.uploadId as string, {
            path: entry.path,
            kind: "file",
            offset,
            bytes: slice,
            chunkSha256: digest,
            totalLength: file.size,
            final: true,
          }),
        );
        chunks.set(key, { offset, length, digest, acked: true });
        record.entries[entry.path].finalAcked = true;
        persist();
        onAck(0);
      }
    }
  }

  #recordEntry(record: ImportJournal, entry: PlannedEntry, chunks: JournalChunk[], finalAcked: boolean): void {
    record.entries[entry.path] = { kind: entry.kind, declaredSize: entry.size, linkTarget: entry.linkTarget, chunks, finalAcked };
    record.updatedAt = new Date().toISOString();
    this.#journal.save(record);
  }

  /**
   * Store the exact completion checkpoint, then dispatch complete with the
   * same S key. After response loss the stored checkpoint is replayed
   * as-is; a fresh generation is never paired with the original key.
   */
  async complete(record: ImportJournal, callbacks: TransferCallbacks): Promise<ImportJournal> {
    if (record.uploadId === null) throw new Error("completion requires an upload");
    if (record.completeCheckpoint === null) {
      const detail = await this.#client.uploadDetail(record.uploadId);
      record.completeCheckpoint = nextCheckpoint(detail.checkpoint);
      record.phase = "completing";
      this.#journal.save(record);
    }
    if (callbacks.shouldContinue && !callbacks.shouldContinue()) throw new TransferCancelled();
    callbacks.guard?.();
    const outcome = await this.#client.completeUpload(record.uploadId, record.completeCheckpoint, record.completeKey);
    callbacks.guard?.();
    record.jobId = extractJobId(outcome);
    record.phase = outcome.kind === "accepted" ? "sealing" : "sealed";
    this.#journal.save(record);
    return record;
  }

  /**
   * A KNOWN seal settlement (cancel confirmed with the upload back open, or
   * an observed recoverable failure) resets completion for a deliberate new
   * completion: the old S key kept its old outcome, so the retry uses a NEW
   * S with a freshly captured checkpoint. Only an UNKNOWN completion outcome
   * (response loss) replays the same S with the same stored checkpoint.
   */
  markSealSettled(record: ImportJournal): ImportJournal {
    record.phase = "transfer-cancelled";
    record.completeCheckpoint = null;
    record.completeKey = crypto.randomUUID();
    record.jobId = null;
    record.updatedAt = new Date().toISOString();
    this.#journal.save(record);
    return record;
  }

  /** Explicit retry after a failed S receipt, never an unknown outcome. */
  async prepareCompletionRetry(record: ImportJournal, callbacks: TransferCallbacks = {}): Promise<ImportJournal> {
    callbacks.guard?.();
    const operation = await this.#client.operation(record.completeKey);
    callbacks.guard?.();
    if (operation.state !== "failed" || !record.uploadId) throw new Error("the original completion outcome is not confirmed failed; keep its original key");
    const detail = await this.#client.uploadDetail(record.uploadId);
    callbacks.guard?.();
    if (detail.state !== "open") throw new Error("the upload is not confirmed open; keep its original completion key");
    return this.markSealSettled(record);
  }

  /**
   * Replay the original U for a journal whose create acknowledgement was
   * lost (phase prepared, no upload id): the server receipt replays the
   * same upload instead of minting a second one. Never generates a new U
   * or a new operation identity here.
   */
  async attachUpload(record: ImportJournal, callbacks: TransferCallbacks = {}): Promise<ImportJournal> {
    if (record.uploadId !== null) return record;
    if (record.stagingOnly || record.createKey === null) throw new Error("this record has no original upload creation intent");
    callbacks.guard?.();
    const created = await this.#client.createUpload(
      {
        purpose: "result",
        input_mode: record.inputMode,
        selection_name: record.selectionName ?? undefined,
        target: { project_id: record.target.projectId, document_id: record.target.documentId },
      },
      record.createKey,
    );
    record.uploadId = created.upload_id;
    record.phase = "transferring";
    this.#journal.save(record);
    return record;
  }

  /**
   * Resume a retained upload with a reselected input: verify every actual
   * server-confirmed range first, then send only the exact gaps. A mismatch
   * blocks same-upload resume (no entry reset exists).
   */
  async resume(record: ImportJournal, input: PlannedInput, callbacks: TransferCallbacks = {}): Promise<ImportJournal> {
    if (record.uploadId === null) throw new Error("resume requires an upload");
    if (record.inputMode !== input.mode) throw new ReselectionMismatchError("the reselected input mode differs from the retained upload");
    callbacks.onProgress?.({ phase: "verifying", ackedBytes: 0, knownTotalBytes: null, entriesDone: 0, entriesTotal: input.entries.length, note: null });
    const verification = await verifyReselection(this.#client, record.uploadId, input, record.entries, 2, () => {
      callbacks.guard?.();
      if (callbacks.shouldContinue && !callbacks.shouldContinue()) throw new TransferCancelled();
    });
    for (const entry of input.entries) {
      if (entry.kind !== "file") continue;
      record.entries[entry.path] = {
        ...record.entries[entry.path],
        kind: "file",
        declaredSize: record.entries[entry.path]?.declaredSize ?? entry.size,
        chunks: record.entries[entry.path]?.chunks ?? [],
        finalAcked: verification.completedPaths.has(entry.path),
      };
    }
    const note = [
      verification.concurrentWriter ? "another writer progressed this upload; verified against the refreshed state" : null,
      ...verification.warnings,
    ]
      .filter((value): value is string => value !== null)
      .join(" · ") || null;
    if (note) callbacks.onNotice?.(note);
    record.phase = "transferring";
    this.#journal.save(record);
    callbacks.onProgress?.({ phase: "transferring", ackedBytes: 0, knownTotalBytes: null, entriesDone: 0, entriesTotal: input.entries.length, note });
    return this.transfer(record, input, callbacks, verification.coverage);
  }

  /**
   * Dispatch POST /imports with the original I and pre-captured base, then
   * reconcile: 202 receipts are durable accepted work; publication is marked
   * only when the import job completes and the version view is confirmed.
   */
  async publish(record: ImportJournal, callbacks: TransferCallbacks = {}, waitMs: (ms: number) => Promise<void> = (ms) => new Promise((resolve) => setTimeout(resolve, ms))): Promise<ImportJournal> {
    if (record.uploadId === null) throw new Error("publish requires an upload");
    if (record.stagingOnly || record.importKey === null) {
      // Staging-only recovery has no publication intent: publishing it would
      // reconstruct an unknown I/base by declaration, which is prohibited.
      throw new Error("staging-only recovery carries no publication intent; publication requires an explicit new operation");
    }
    record.phase = "dispatching";
    this.#journal.save(record);
    this.#journal.setActive(journalAuthority(record.libraryId, record.principalId), record.importKey);
    const request =
      record.target.documentId === null
        ? { upload_id: record.uploadId, document_id: null }
        : { upload_id: record.uploadId, document_id: record.target.documentId, base_version_id: record.target.baseVersionId };
    callbacks.guard?.();
    const outcome = await this.#client.importPublish(request, record.importKey);
    if (outcome.kind === "committed") {
      const versionId = extractVersionId(outcome.body);
      if (versionId) {
        await this.#client.versionDetail(versionId);
        record.phase = "published";
        record.versionId = versionId;
        this.#journal.save(record);
        return record;
      }
    }
    const reconciliation = await reconcileImport(this.#client, record.importKey);
    if (reconciliation.kind === "published") {
      record.phase = "published";
      record.versionId = reconciliation.versionId;
      this.#journal.save(record);
      return record;
    }
    if (reconciliation.kind === "sealing") {
      record.jobId = reconciliation.jobId;
      this.#journal.save(record);
      const job = await awaitImportJob(this.#client, reconciliation.jobId, waitMs);
      if (job.state === "completed") {
        const versionId = extractVersionId(job.result);
        if (!versionId) throw new Error("completed import job did not identify a version");
        await this.#client.versionDetail(versionId);
        record.phase = "published";
        record.versionId = versionId;
        this.#journal.save(record);
        return record;
      }
      throw new ImportJobError(job.error || `job_${job.state}`);
    }
    if (reconciliation.kind === "accepted") {
      // Reservation with no job yet stays an honest in-progress state.
      record.phase = "dispatching";
      this.#journal.save(record);
      return record;
    }
    if (reconciliation.kind === "failed") {
      throw new Error(`import failed: ${reconciliation.error}`);
    }
    throw new Error("import outcome unresolved; the original operation identity is retained");
  }
}

function extractVersionId(value: unknown): string | null {
  if (value && typeof value === "object" && "version_id" in value) {
    const id = (value as { version_id?: unknown }).version_id;
    if (typeof id === "string" && id) return id;
  }
  return null;
}

/** The completion checkpoint: same confirmed state, generation + 1, complete. */
export function nextCheckpoint(checkpoint: UploadCheckpoint): UploadCheckpoint {
  return {
    generation: checkpoint.generation + 1,
    inventory_sha256: checkpoint.inventory_sha256,
    accepted_bytes: checkpoint.accepted_bytes,
    complete_input: true,
  };
}

function extractJobId(outcome: DecisionResult): string | null {
  const body = outcome.body as { job_id?: string } | null;
  return body?.job_id ?? null;
}

export type ImportOutcome =
  | { kind: "published"; versionId: string }
  | { kind: "accepted"; operationId: string }
  | { kind: "sealing"; jobId: string }
  | { kind: "reconciling"; operationId: string }
  | { kind: "failed"; operationId: string; error: string }
  | { kind: "unknown"; operationId: string }
  | { kind: "completed-receipt"; operationId: string };

/**
 * Receipt reconciliation state machine for POST /imports response loss, on
 * the ACTUAL operations wire: the completed operation carries its original
 * response body inside result_metadata (top-level job_id/result_ref stay
 * null). - pending/accepted reservation: no job exists yet, poll the
 * operation; - completed 202 body links the import job: only a completed job
 * plus a confirmed version view marks publication; - completed 200/201 are
 * terminal results, still confirmed against the version view; -
 * reconciling/failed are distinct states; 404 is not proof of non-execution,
 * so the same-K replay remains the caller's recovery.
 */
export async function reconcileImport(client: HaeryongClient, operationId: string): Promise<ImportOutcome> {
  let operation: OperationView;
  try {
    operation = await client.operation(operationId);
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) {
      return { kind: "unknown", operationId };
    }
    throw error;
  }
  if (operation.state === "pending" || operation.state === "accepted") {
    return { kind: "accepted", operationId };
  }
  if (operation.state === "reconciling") {
    return { kind: "reconciling", operationId };
  }
  if (operation.state === "failed") {
    return { kind: "failed", operationId, error: JSON.stringify(operation.result_metadata ?? null) };
  }
  if (operation.state === "completed") {
    const body = operation.result_metadata as { job_id?: string; version_id?: string } | null;
    if (operation.original_status === 200 || operation.original_status === 201) {
      const versionId = typeof body?.version_id === "string" ? body.version_id : null;
      if (versionId) {
        await client.versionDetail(versionId);
        return { kind: "published", versionId };
      }
      return { kind: "completed-receipt", operationId };
    }
    if (operation.original_status === 202 && typeof body?.job_id === "string") {
      return { kind: "sealing", jobId: body.job_id };
    }
  }
  return { kind: "unknown", operationId };
}

/** Poll an import job to a terminal state; a completed job yields the version. */
export async function awaitImportJob(client: HaeryongClient, jobId: string, waitMs: (ms: number) => Promise<void>, intervalMs = 200, deadlineMs = 120000): Promise<JobView> {
  const deadline = Date.now() + deadlineMs;
  for (;;) {
    const job = await client.jobGet(jobId);
    if (job.state === "completed" || job.state === "failed" || job.state === "cancelled") return job;
    if (Date.now() > deadline) throw new Error("import job did not settle");
    await waitMs(intervalMs);
  }
}

export interface VerificationResult {
  verifiedRanges: number;
  /** Another writer moved the upload during an earlier verification pass. */
  concurrentWriter: boolean;
  /** Merged confirmed coverage per file path; only exact gaps get sent. */
  coverage: Map<string, Interval[]>;
  /** Honest identity warnings surfaced to the owner before resuming. */
  warnings: string[];
  completedPaths: Set<string>;
}

/**
 * Reselection verification: enumerate EVERY actual server-confirmed
 * entry/range across all cursor pages (including pages with empty items and
 * a next cursor) and compare each individual range — holes are legal server
 * state and are verified, not contiguity-assumed — against the reselected
 * bytes. The stability re-read happens AFTER the asynchronous hashing; if
 * the upload still advanced, verification restarts, and an advanced
 * checkpoint is never accepted once the retry budget is spent. Journal
 * knowledge never limits the checks.
 */
export async function verifyReselection(client: HaeryongClient, uploadId: string, input: PlannedInput, journalEntries?: Record<string, JournalEntryRecord>, retries = 2, check?: () => void): Promise<VerificationResult> {
  const files = new Map<string, File>();
  for (const entry of input.entries) {
    if (entry.kind === "file" && entry.file) files.set(entry.path, entry.file);
  }
  let observedChange = false;
  for (let attempt = 0; ; attempt += 1) {
    check?.();
    const before = await client.uploadDetail(uploadId);
    const entries = await collectAll((cursor) => client.uploadEntries(uploadId, cursor));
    const rangesByPath = new Map<string, UploadConfirmedRange[]>();
    for (const entry of entries) {
      if (entry.kind !== "file") continue;
      rangesByPath.set(entry.path, await collectAll((cursor) => client.uploadRanges(uploadId, entry.path, cursor)));
    }
    let verified = 0;
    const coverage = new Map<string, Interval[]>();
    const warnings: string[] = [];
    for (const [path, ranges] of rangesByPath) {
      const file = files.get(path);
      if (!file) {
        throw new ReselectionMismatchError(`retained entry ${JSON.stringify(path)} is missing from the reselected input`);
      }
      // Known-identity exact checks from this browser's own journal: the
      // declared size compares exactly (never size >= offset); the unsent
      // tail is NOT hashed — its identity stays the server's sealed truth.
      const known = journalEntries?.[path];
      if (known?.declaredSize !== undefined && known.declaredSize !== file.size) {
        throw new ReselectionMismatchError(`reselected input size ${file.size} differs from the journaled size ${known.declaredSize} for ${JSON.stringify(path)}`);
      }
      if (known?.declaredSha256 && await sha256Range(file, 0, file.size, check) !== known.declaredSha256) {
        throw new ReselectionMismatchError(`reselected input differs from the original journaled digest for ${JSON.stringify(path)}`);
      }
      const intervals: Interval[] = [];
      for (const range of ranges) {
        const offset = Number(range.offset);
        const length = Number(range.byte_size);
        if (offset < 0 || length < 0 || offset + length > file.size) {
          throw new ReselectionMismatchError(`reselected input is shorter than the retained range for ${JSON.stringify(path)}:${offset}`);
        }
        const digest = await sha256Range(file, offset, length, check);
        if (digest !== range.bytes_sha256) {
          throw new ReselectionMismatchError(`reselected bytes differ from the retained range at ${JSON.stringify(path)}:${offset}`);
        }
        intervals.push({ start: offset, end: offset + length });
        verified += 1;
      }
      // Disclosure follows the ACTUAL uncovered intervals (a tail range that
      // reaches file.size can still hide an earlier unsent gap) and the
      // missing ORIGINAL DIGEST, including owned journals that know only the
      // size: without the digest a mixed input cannot be excluded. The
      // unsent tail is never hashed to manufacture certainty.
      const mergedCoverage = mergeIntervals(intervals);
      const uncovered = gapsBetween(mergedCoverage, file.size);
      if (uncovered.length > 0 && !known?.declaredSha256) {
        warnings.push(`${JSON.stringify(path)}: 미확정 구간 ${uncovered.length}곳이 있고 원본 전체 다이제스트를 알지 못해, 혼합 입력 가능성을 배제할 수 없습니다.`);
      }
      coverage.set(path, mergedCoverage);
    }
    for (const entry of entries) {
      const planned = input.entries.find((candidate) => candidate.path === entry.path);
      if (!planned || planned.kind !== entry.kind) {
        throw new ReselectionMismatchError(`retained entry ${JSON.stringify(entry.path)} has no matching reselected entry`);
      }
      if (entry.kind === "symlink" && planned.linkTarget !== entry.link_target) {
        throw new ReselectionMismatchError(`reselected link target differs for ${JSON.stringify(entry.path)}`);
      }
    }
    // Stability is re-checked only AFTER the asynchronous hashing above.
    const after = await client.uploadDetail(uploadId);
    if (before.revision !== after.revision || before.state !== after.state) {
      observedChange = true;
      if (attempt < retries) continue;
      throw new ConcurrentUploadError();
    }
    return { verifiedRanges: verified, concurrentWriter: observedChange, coverage, warnings, completedPaths: new Set(entries.filter((entry) => entry.complete).map((entry) => entry.path)) };
  }
}

async function collectAll<T>(page: (cursor?: string) => Promise<{ items: T[]; next_cursor: string | null }>): Promise<T[]> {
  const all: T[] = [];
  let cursor: string | undefined;
  do {
    const result = await page(cursor);
    all.push(...result.items);
    cursor = result.next_cursor ?? undefined;
  } while (cursor);
  return all;
}

/**
 * Cancel verification on the sealing job and wait until the worker stop is
 * confirmed: the upload stays sealing/cancelling until then, and only a
 * non-abandoned upload returns open with its retained bytes.
 */
export async function cancelSealingAndWait(client: HaeryongClient, detail: UploadDetail, operationId: string, waitMs: (ms: number) => Promise<void>, intervalMs = 200, deadlineMs = 120000): Promise<UploadDetail> {
  if (!detail.seal_job_id) return detail;
  await client.jobCancel(detail.seal_job_id, operationId);
  const deadline = Date.now() + deadlineMs;
  for (;;) {
    const current = await client.uploadDetail(detail.upload_id);
    if (current.state === "open" || current.state === "sealed" || current.state === "abandoned") return current;
    if (Date.now() > deadline) return current;
    await waitMs(intervalMs);
  }
}
