// Focused transfer-engine regressions for the reviewed correctness rules:
// journal-blocks-dispatch, exact-gap resume over legal holes, per-chunk
// cancel, concurrent-verification stability after hashing, exact S/checkpoint
// replay, target/base preservation, and the actual operations wire shapes.
import assert from "node:assert/strict";
import test from "node:test";
import { createHash } from "node:crypto";
import {QuestionSubmission,questionStreamText,BrowserQuestionJournal} from "./questions.ts";
import type {QuestionRecord} from "./questions.ts";
import { GenerationSubmission } from "./generation.ts";
import type { GenerationRecord } from "./generation.ts";

import {
  BrowserJournalStore,
  ConcurrentUploadError,
  ImportJobError,
  JournalReadError,
  JournalWriteError,
  ReselectionMismatchError,
  TargetMismatchError,
  TransferCancelled,
  TransferEngine,
  gapsBetween,
  journalKey,
  mergeIntervals,
  planFromArchive,
  planFromFolder,
  reconcileImport,
  sha256Range,
  verifyReselection,
} from "./transfer.ts";
import type { ImportJournal, JournalStore } from "./transfer.ts";
import type { HaeryongClient, ListPage, OperationView, UploadCheckpoint, UploadDetail, GenerationRequest } from "./types.ts";
import { ApiError } from "./types.ts";
import type { FileLike, PlannedEntry, PlannedInput } from "./transfer.ts";

function fileFrom(path: string, bytes: Uint8Array): File {
  const file = new File([bytes as unknown as BlobPart], path.slice(path.lastIndexOf("/") + 1));
  Object.defineProperty(file, "webkitRelativePath", { value: `root/${path}` });
  return file;
}

function fakeDirectoryUpload(files: [string, Uint8Array][]): { input: ReturnType<typeof planFromFolder>; files: Map<string, FileLike> } {
  const picked = files.map(([path, bytes]) => fileFrom(path, bytes));
  const input = planFromFolder(picked, "root");
  const map = new Map<string, FileLike>();
  for (const entry of input.entries) if (entry.file) map.set(entry.path, entry.file);
  return { input, files: map };
}

interface PutCall {
  path: string;
  kind: string;
  offset: number;
  final: boolean;
  digest: string;
  chunkSha256?: string;
  bytes: Uint8Array;
}

class FakeJournal implements JournalStore {
  records = new Map<string, ImportJournal>();
  active: string | null = null;
  failNextSave = false;
  saves = 0;
  save(record: ImportJournal): void {
    if (this.failNextSave) throw new JournalWriteError(new Error("injected"));
    this.records.set(journalKey(record), structuredClone(record));
    this.saves += 1;
  }
  setActive(_authority: string, importKey: string | null): void {
    this.active = importKey;
  }
  load(): { active: string | null; records: ImportJournal[] } {
    return { active: this.active, records: [...this.records.values()] };
  }
  discard(_authority: string, importKey: string): void {
    this.records.delete(importKey);
    if (this.active === importKey) this.active = null;
  }
}

function emptyCheckpoint(generation = 1): UploadCheckpoint {
  return { generation, inventory_sha256: "", accepted_bytes: "0", complete_input: false };
}

class FakeClient {
  document: { document_id: string; project_id: string; latest_version_id: string | null } = { document_id: "doc-1", project_id: "proj-1", latest_version_id: "ver-base" };
  createdUploads = 0;
  puts: PutCall[] = [];
  ranges = new Map<string, { offset: string; byte_size: string; bytes_sha256: string }[]>();
  entriesPage: { path: string; kind: string }[] = [];
  detail: UploadDetail = {
    upload_id: "up-1",
    checkpoint: emptyCheckpoint(),
    revision: 4,
    state: "open",
    ranges_url: "",
    entries_url: "",
    error: null,
    seal_job_id: null,
    seal_operation_id: null,
    sealed_content_set_id: null,
    sealed_object_ref: null,
    originating_principal_id: "p1",
    originating_device_id: null,
    purpose: "result",
    input_mode: "directory",
    selection_name: "root",
    target: {},
    byte_size: "0",
    staging_bytes: "0",
    created_at: "",
    updated_at: "",
    reusable: true,
    unavailable_reason: null,
    pin_summary: {},
    pins_url: "",
    detail_url: "",
    etag: "",
  };
  completeCalls: { checkpoint: UploadCheckpoint; key: string }[] = [];
  importCalls = 0;
  operationView: OperationView | null = null;
  operation404 = false;
  versionConfirms: string[] = [];
  job: { state: string; result?: unknown; error?: string } = { state: "completed", result: { version_id: "ver-new" } };

  // -- transfer surface -----------------------------------------------------
  async documentDetail(id: string): Promise<typeof this.document> {
    return { ...this.document, document_id: id };
  }
  async createUpload(_request?:unknown,_key?:unknown): Promise<{ upload_id: string; checkpoint: UploadCheckpoint }> {
    this.createdUploads += 1;
    return { upload_id: "up-" + this.createdUploads, checkpoint: emptyCheckpoint() };
  }
  async putUploadEntry(_uploadId: string, chunk: PutCall & { totalLength?: number }): Promise<{ accepted_offset: string; checkpoint: UploadCheckpoint }> {
    this.puts.push(chunk);
    return { accepted_offset: String(chunk.offset + chunk.bytes.byteLength), checkpoint: emptyCheckpoint() };
  }
  async uploadDetail(): Promise<UploadDetail> {
    return structuredClone(this.detail);
  }
  async uploadEntries(): Promise<ListPage<{ path: string; kind: string }>> {
    return { items: structuredClone(this.entriesPage), next_cursor: null };
  }
  async uploadRanges(_uploadId: string, path?: string): Promise<ListPage<{ offset: string; byte_size: string; bytes_sha256: string }>> {
    if (path === undefined) return { items: [], next_cursor: null };
    return { items: structuredClone(this.ranges.get(path) ?? []), next_cursor: null };
  }
  async completeUpload(_uploadId: string, checkpoint: UploadCheckpoint, key: string): Promise<{ kind: "accepted" | "committed"; body: unknown }> {
    this.completeCalls.push({ checkpoint, key });
    return { kind: "accepted", body: { job_id: "seal-job" } };
  }
  async importPublish(): Promise<{ kind: "accepted" | "committed"; body: unknown }> {
    this.importCalls += 1;
    return { kind: "accepted", body: { job_id: "import-job" } };
  }
  async operation(): Promise<OperationView> {
    if (this.operation404) throw new ApiError(404, "NOT_FOUND", false, 0);
    return structuredClone(this.operationView as OperationView);
  }
  async versionDetail(id: string): Promise<{ version_id: string }> {
    this.versionConfirms.push(id);
    return { version_id: id };
  }
  async jobGet(): Promise<{ state: string; result?: unknown }> {
    return structuredClone(this.job);
  }
}

function client(fake: FakeClient): HaeryongClient {
  return fake as unknown as HaeryongClient;
}

test("planFromFolder drops the root segment, adds implied parents and discloses FileList limits", () => {
  const { input } = fakeDirectoryUpload([
    ["docs/a.html", new Uint8Array([1, 2])],
    ["docs/deep/b.txt", new Uint8Array([3])],
    ["top.md", new Uint8Array([4])],
  ]);
  assert.deepEqual(
    input.entries.map((entry) => `${entry.kind}:${entry.path}`),
    ["directory:docs", "file:docs/a.html", "directory:docs/deep", "file:docs/deep/b.txt", "file:top.md"],
  );
  assert.equal(input.selectionName, "root");
  assert.equal(input.limits.emptyDirectoriesRepresented, false);
  assert.equal(input.limits.symlinksRepresented, false);
});

test("a journal save failure blocks the dispatch entirely", async () => {
  const fake = new FakeClient();
  const journal = new FakeJournal();
  journal.failNextSave = true;
  const engine = new TransferEngine(client(fake), journal);
  await assert.rejects(
    engine.begin("lib", "p1", { kind: "new", projectId: "proj-1" }, planFromArchive(new File([new Uint8Array(4)], "a.zip"))),
    (error: unknown) => error instanceof JournalWriteError,
  );
  assert.equal(fake.createdUploads, 0);
});

test("begin preserves the owner-displayed base and rejects a project mismatch explicitly", async () => {
  const fake = new FakeClient();
  const journal = new FakeJournal();
  const engine = new TransferEngine(client(fake), journal);
  const record = await engine.begin("lib", "p1", { kind: "existing", projectId: "proj-1", documentId: "doc-1", baseVersionId: "ver-displayed" }, planFromArchive(new File([new Uint8Array(4)], "a.zip")));
  assert.equal(record.target.baseVersionId, "ver-displayed");
  assert.equal(record.target.documentId, "doc-1");
  await assert.rejects(
    engine.begin("lib", "p1", { kind: "existing", projectId: "proj-other", documentId: "doc-1" }, planFromArchive(new File([new Uint8Array(4)], "a.zip"))),
    (error: unknown) => error instanceof TargetMismatchError,
  );
});

test("transfer acks chunks, stores the exact checkpoint before completing and replays the same S/checkpoint", async () => {
  const fake = new FakeClient();
  const journal = new FakeJournal();
  const engine = new TransferEngine(client(fake), journal, { chunkSize: 2 });
  const { input } = fakeDirectoryUpload([
    ["a.bin", new Uint8Array([1, 2, 3, 4, 5])],
    ["empty.bin", new Uint8Array(0)],
  ]);
  const record = await engine.begin("lib", "p1", { kind: "new", projectId: "proj-1" }, input);
  await engine.transfer(record, input);
  const aPuts = fake.puts.filter((put) => put.path === "a.bin");
  assert.deepEqual(aPuts.map((put) => [put.offset, put.bytes.byteLength, put.final]), [[0, 2, false], [2, 2, false], [4, 1, true]]);
  const emptyPuts = fake.puts.filter((put) => put.path === "empty.bin");
  assert.equal(emptyPuts.length, 1);
  assert.equal(emptyPuts[0].final, true);
  assert.equal(emptyPuts[0].bytes.byteLength, 0);
  assert.equal(fake.completeCalls.length, 1);
  assert.equal(fake.completeCalls[0].checkpoint.generation, 2);
  assert.equal(fake.completeCalls[0].checkpoint.complete_input, true);
  // Response-loss replay: phase completing replays the exact stored checkpoint.
  record.phase = "completing";
  await engine.transfer(record, input);
  assert.equal(fake.completeCalls.length, 2);
  assert.deepEqual(fake.completeCalls[1].checkpoint, fake.completeCalls[0].checkpoint);
  assert.equal(fake.completeCalls[1].key, fake.completeCalls[0].key);
});

test("cancel inside one large file is observed before the next chunk and never abandons", async () => {
  const fake = new FakeClient();
  const journal = new FakeJournal();
  const engine = new TransferEngine(client(fake), journal, { chunkSize: 2 });
  const { input } = fakeDirectoryUpload([["big.bin", new Uint8Array(64)]]);
  const record = await engine.begin("lib", "p1", { kind: "new", projectId: "proj-1" }, input);
  let calls = 0;
  await assert.rejects(
    engine.transfer(record, input, { shouldContinue: () => (calls += 1) < 3 }),
    (error: unknown) => error instanceof TransferCancelled,
  );
  assert.equal(record.phase, "transfer-cancelled");
  assert.ok(fake.puts.length >= 1 && fake.puts.length < 32);
  assert.equal(fake.completeCalls.length, 0);
});

test("verifyReselection verifies every individual range across legal holes and resume sends exactly the gaps", async () => {
  const fake = new FakeClient();
  const { input, files } = fakeDirectoryUpload([["a.bin", new Uint8Array([10, 11, 12, 13, 14, 15, 16, 17])]]);
  fake.entriesPage = [{ path: "a.bin", kind: "file" }];
  const first = await sha256Range(files.get("a.bin") as File, 0, 2);
  const middle = await sha256Range(files.get("a.bin") as File, 4, 2);
  fake.ranges.set("a.bin", [
    { offset: "0", byte_size: "2", bytes_sha256: first },
    { offset: "4", byte_size: "2", bytes_sha256: middle },
  ]);
  const verification = await verifyReselection(client(fake), "up-1", input);
  assert.equal(verification.verifiedRanges, 2);
  assert.deepEqual(verification.coverage.get("a.bin"), [{ start: 0, end: 2 }, { start: 4, end: 6 }]);
  const journal = new FakeJournal();
  const engine = new TransferEngine(client(fake), journal, { chunkSize: 3 });
  const record: ImportJournal = {
    libraryId: "lib",
    principalId: "p1",
    uploadId: "up-1",
    createKey: "u",
    completeKey: "s",
    importKey: "i",
    stagingOnly: false,
    target: { projectId: "proj-1", documentId: null, baseVersionId: null },
    inputMode: "directory",
    selectionName: "root",
    phase: "transferring",
    entries: {},
    createdAt: "",
    updatedAt: "",
    jobId: null,
    versionId: null,
    completeCheckpoint: null,
  };
  await engine.transfer(record, input, {}, verification.coverage);
  // Exactly the gaps [2,4) and [6,8) are sent; retained regions untouched.
  assert.deepEqual(fake.puts.map((put) => [put.offset, put.bytes.byteLength, put.final]), [[2, 2, false], [6, 2, true]]);
});

test("a retained-range digest mismatch blocks same-upload resume before anything is sent", async () => {
  const fake = new FakeClient();
  const { input } = fakeDirectoryUpload([["a.bin", new Uint8Array([1, 2, 3, 4])]]);
  fake.entriesPage = [{ path: "a.bin", kind: "file" }];
  fake.ranges.set("a.bin", [{ offset: "0", byte_size: "4", bytes_sha256: "0".repeat(64) }]);
  await assert.rejects(
    verifyReselection(client(fake), "up-1", input),
    (error: unknown) => error instanceof ReselectionMismatchError,
  );
});

test("a retained entry missing from the reselection blocks resume", async () => {
  const fake = new FakeClient();
  const { input } = fakeDirectoryUpload([["a.bin", new Uint8Array([1])]]);
  fake.entriesPage = [
    { path: "a.bin", kind: "file" },
    { path: "gone.bin", kind: "file" },
  ];
  fake.ranges.set("gone.bin", []);
  await assert.rejects(
    verifyReselection(client(fake), "up-1", input),
    (error: unknown) => error instanceof ReselectionMismatchError,
  );
});

test("an upload advancing after every recheck is never accepted past the retry budget", async () => {
  const fake = new FakeClient();
  const { input } = fakeDirectoryUpload([["a.bin", new Uint8Array([1, 2])]]);
  fake.entriesPage = [{ path: "a.bin", kind: "file" }];
  fake.ranges.set("a.bin", []);
  let revision = 4;
  const originalDetail = fake.detail;
  let detailCalls = 0;
  (fake as unknown as { uploadDetail: () => Promise<UploadDetail> }).uploadDetail = async () => {
    detailCalls += 1;
    // Every "after" read (the odd detail call in each attempt) advances.
    if (detailCalls % 2 === 0) revision += 1;
    return { ...structuredClone(originalDetail), revision };
  };
  await assert.rejects(
    verifyReselection(client(fake), "up-1", input, undefined, 2),
    (error: unknown) => error instanceof ConcurrentUploadError,
  );
  assert.equal(fake.puts.length, 0);
});

test("reconcileImport follows the actual operations wire: result_metadata carries the original body", async () => {
  const fake = new FakeClient();
  fake.operationView = { operation_id: "i", state: "completed", original_status: 202, result_ref: null, job_id: null, result_metadata: { job_id: "job-9" } };
  let outcome = await reconcileImport(client(fake), "i");
  assert.deepEqual(outcome, { kind: "sealing", jobId: "job-9" });
  fake.operationView = { operation_id: "i", state: "completed", original_status: 201, result_ref: null, job_id: null, result_metadata: { version_id: "ver-7" } };
  outcome = await reconcileImport(client(fake), "i");
  assert.deepEqual(outcome, { kind: "published", versionId: "ver-7" });
  assert.deepEqual(fake.versionConfirms, ["ver-7"]);
  fake.operationView = { operation_id: "i", state: "accepted", original_status: null, result_ref: null, job_id: null, result_metadata: null };
  outcome = await reconcileImport(client(fake), "i");
  assert.deepEqual(outcome, { kind: "accepted", operationId: "i" });
  fake.operation404 = true;
  outcome = await reconcileImport(client(fake), "i");
  assert.deepEqual(outcome, { kind: "unknown", operationId: "i" });
});

test("publish marks publication only after the job completes and the version view is confirmed", async () => {
  const fake = new FakeClient();
  fake.operationView = { operation_id: "i", state: "completed", original_status: 202, result_ref: null, job_id: null, result_metadata: { job_id: "job-9" } };
  const journal = new FakeJournal();
  const engine = new TransferEngine(client(fake), journal);
  const record: ImportJournal = {
    libraryId: "lib",
    principalId: "p1",
    uploadId: "up-1",
    createKey: "u",
    completeKey: "s",
    importKey: "i",
    stagingOnly: false,
    target: { projectId: "proj-1", documentId: null, baseVersionId: null },
    inputMode: "directory",
    selectionName: "root",
    phase: "sealed",
    entries: {},
    createdAt: "",
    updatedAt: "",
    jobId: null,
    versionId: null,
    completeCheckpoint: null,
  };
  const published = await engine.publish(record);
  assert.equal(published.phase, "published");
  assert.equal(published.versionId, "ver-new");
  assert.deepEqual(fake.versionConfirms, ["ver-new"]);
});

test("BrowserJournalStore blocks when storage is unavailable and preserves unresolved originals", () => {
  assert.equal(typeof globalThis.localStorage, "undefined");
  const store = new BrowserJournalStore();
  const record: ImportJournal = {
    libraryId: "lib",
    principalId: "p1",
    uploadId: "up-1",
    createKey: "u1",
    completeKey: "s1",
    importKey: "i-1",
    stagingOnly: false,
    target: { projectId: "p", documentId: null, baseVersionId: null },
    inputMode: "archive",
    selectionName: null,
    phase: "dispatching",
    entries: {},
    createdAt: "",
    updatedAt: "",
    jobId: null,
    versionId: null,
    completeCheckpoint: null,
  };
  assert.throws(() => store.save(record), (error: unknown) => error instanceof JournalWriteError);
  // An in-memory bucket mirrors the preservation rule for the new operation.
  const memory = new FakeJournal();
  memory.save(record);
  const second = { ...record, importKey: "i-2", createKey: "u2", completeKey: "s2" };
  memory.save(second);
  memory.setActive("lib:p1", "i-2");
  assert.equal(memory.records.size, 2);
  assert.equal(memory.active, "i-2");
  const unresolved = memory.load().records.find((entry) => entry.importKey === "i-1");
  assert.equal(unresolved?.phase, "dispatching");
});

test("fully retained bytes without the end marker are reaffirmed, not skipped", async () => {
  const fake = new FakeClient();
  const { input, files } = fakeDirectoryUpload([["a.bin", new Uint8Array([9, 8, 7, 6])]]);
  fake.entriesPage = [{ path: "a.bin", kind: "file" }];
  fake.ranges.set("a.bin", [
    { offset: "0", byte_size: "2", bytes_sha256: await sha256Range(files.get("a.bin") as File, 0, 2) },
    { offset: "2", byte_size: "2", bytes_sha256: await sha256Range(files.get("a.bin") as File, 2, 2) },
  ]);
  const journal = new FakeJournal();
  const engine = new TransferEngine(client(fake), journal, { chunkSize: 2 });
  const record: ImportJournal = {
    libraryId: "lib",
    principalId: "p1",
    uploadId: "up-1",
    createKey: "u",
    completeKey: "s",
    importKey: "i",
    stagingOnly: false,
    target: { projectId: "proj-1", documentId: null, baseVersionId: null },
    inputMode: "directory",
    selectionName: "root",
    phase: "transferring",
    entries: {},
    createdAt: "",
    updatedAt: "",
    jobId: null,
    versionId: null,
    completeCheckpoint: null,
  };
  // Resume path: the server-confirmed coverage is verified first, so no gap
  // is sent; a zero-byte EOF marker sets Final without replaying the file.
  await engine.resume(record, input);
  const puts = fake.puts.filter((put) => put.path === "a.bin");
  assert.equal(puts.length, 1);
  assert.equal(puts[0].offset, 4);
  assert.equal(puts[0].bytes.byteLength, 0);
  assert.equal(puts[0].final, true);
  assert.equal(fake.completeCalls.length, 1);
});

test("attachUpload replays the original U after a lost create acknowledgement", async () => {
  const fake = new FakeClient();
  const journal = new FakeJournal();
  const engine = new TransferEngine(client(fake), journal);
  const record: ImportJournal = {
    libraryId: "lib",
    principalId: "p1",
    uploadId: null,
    createKey: "u-original",
    completeKey: "s",
    importKey: "i",
    stagingOnly: false,
    target: { projectId: "proj-1", documentId: null, baseVersionId: null },
    inputMode: "archive",
    selectionName: "a.zip",
    phase: "prepared",
    entries: {},
    createdAt: "",
    updatedAt: "",
    jobId: null,
    versionId: null,
    completeCheckpoint: null,
  };
  const attached = await engine.attachUpload(record);
  assert.equal(attached.uploadId, "up-1");
  assert.equal(attached.phase, "transferring");
  // The same U is preserved: the journal record identity never changed.
  assert.equal(attached.createKey, "u-original");
});

test("interval helpers merge overlaps and report exact gaps", () => {
  assert.deepEqual(mergeIntervals([{ start: 4, end: 6 }, { start: 0, end: 2 }, { start: 2, end: 3 }]), [{ start: 0, end: 3 }, { start: 4, end: 6 }]);
  assert.deepEqual(gapsBetween([{ start: 0, end: 2 }, { start: 4, end: 6 }], 8), [{ start: 2, end: 4 }, { start: 6, end: 8 }]);
  assert.deepEqual(gapsBetween([{ start: 0, end: 8 }], 8), []);
});

test("range SHA-256 matches independent digests with bounded reads, offsets and empty ranges", async () => {
  const bytes = new Uint8Array(3 * 1024 * 1024 + 137);
  for (let i = 0; i < bytes.length; i += 1) bytes[i] = (i * 31) % 251;
  const file = new File([bytes], "large.bin");
  const reads: number[] = [];
  const slice = file.slice.bind(file);
  Object.defineProperty(file, "slice", { value: (start: number, end: number) => {
    reads.push(end - start);
    return slice(start, end);
  } });
  for (const [offset, length] of [[0, 0], [0, 1], [7, 63], [7, 64], [7, 65], [19, bytes.length - 23]]) {
    const expected = createHash("sha256").update(bytes.subarray(offset, offset + length)).digest("hex");
    assert.equal(await sha256Range(file, offset, length), expected);
  }
  assert.ok(reads.length > 3);
  assert.ok(reads.every((size) => size <= 1024 * 1024));
  await assert.rejects(sha256Range(file, 1, file.size), ReselectionMismatchError);
});

test("range hashing rejects short reads and stops before consuming more bytes after cancellation", async () => {
  const short = new File([new Uint8Array(10)], "short.bin");
  Object.defineProperty(short, "slice", { value: () => new Blob([new Uint8Array(1)]) });
  await assert.rejects(sha256Range(short, 0, 10), ReselectionMismatchError);
  const file = new File([new Uint8Array(2 * 1024 * 1024)], "cancel.bin");
  let checks = 0;
  await assert.rejects(sha256Range(file, 0, file.size, () => {
    if (++checks === 2) throw new TransferCancelled();
  }), TransferCancelled);
  assert.equal(checks, 2);
});

test("staging-only recovery has no publication identity and stays distinct after journal reload", async () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
  const values = new Map<string, string>();
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
  } });
  try {
    const store = new BrowserJournalStore();
    const record: ImportJournal = {
      stagingOnly: true, libraryId: "lib", principalId: "owner", uploadId: "up-1",
      createKey: null, completeKey: "recovery-S", importKey: null,
      target: { projectId: "project", documentId: null }, inputMode: "directory", selectionName: "result",
      phase: "sealed", entries: {}, createdAt: "", updatedAt: "", jobId: null, versionId: null, completeCheckpoint: null,
    };
    store.save(record);
    store.save({ ...record, uploadId: "up-2" });
    const records = new BrowserJournalStore().load("lib:owner").records;
    assert.equal(records.length, 2);
    assert.equal(records[0].importKey, null);
    assert.equal(records[0].target.baseVersionId, undefined);
    const fake = new FakeClient();
    const engine = new TransferEngine(client(fake), store);
    await assert.rejects(engine.publish(records[0]), /no publication intent/);
    assert.equal(fake.importCalls, 0);
  } finally {
    if (previous) Object.defineProperty(globalThis, "localStorage", previous);
    else Reflect.deleteProperty(globalThis, "localStorage");
  }
});

test("a new S requires a failed receipt and an open upload; unknown outcomes preserve S and I", async () => {
  const fake = new FakeClient();
  const engine = new TransferEngine(client(fake), new FakeJournal());
  const { input } = fakeDirectoryUpload([["a.bin", new Uint8Array([1, 2])]]);
  const record = await engine.begin("lib", "p1", { kind: "new", projectId: "proj-1" }, input);
  const original = { s: record.completeKey, i: record.importKey, target: structuredClone(record.target) };
  record.phase = "completing";
  record.completeCheckpoint = { ...emptyCheckpoint(5), complete_input: true };
  fake.operationView = { operation_id: original.s, state: "accepted", original_status: null, result_ref: null, job_id: null, result_metadata: null };
  await assert.rejects(engine.prepareCompletionRetry(record), /not confirmed failed/);
  assert.equal(record.completeKey, original.s);
  fake.operationView.state = "failed";
  fake.detail.state = "sealing";
  await assert.rejects(engine.prepareCompletionRetry(record), /not confirmed open/);
  assert.equal(record.completeKey, original.s);
  fake.detail.state = "open";
  await engine.prepareCompletionRetry(record);
  assert.notEqual(record.completeKey, original.s);
  assert.equal(record.completeCheckpoint, null);
  assert.equal(record.importKey, original.i);
  assert.deepEqual(record.target, original.target);
});

test("independent existing-document operations capture a fresh base and reject explicit null before upload", async () => {
  const fake = new FakeClient();
  const engine = new TransferEngine(client(fake), new FakeJournal());
  const { input } = fakeDirectoryUpload([["a.bin", new Uint8Array([1])]]);
  const target = { kind: "existing" as const, projectId: "proj-1", documentId: "doc-1" };
  await assert.rejects(engine.begin("lib", "p1", { ...target, baseVersionId: null }, input), TargetMismatchError);
  assert.equal(fake.createdUploads, 0);
  const first = await engine.begin("lib", "p1", target, input);
  fake.document.latest_version_id = "ver-next";
  const next = await engine.begin("lib", "p1", target, input);
  assert.equal(first.target.baseVersionId, "ver-base");
  assert.equal(next.target.baseVersionId, "ver-next");
  assert.notEqual(first.importKey, next.importKey);
});

test("an asynchronous VERSION_CONFLICT remains a typed job failure and preserves the original intent", async () => {
  const fake = new FakeClient();
  const engine = new TransferEngine(client(fake), new FakeJournal());
  const { input } = fakeDirectoryUpload([["a.bin", new Uint8Array([1])]]);
  const record = await engine.begin("lib", "p1", { kind: "existing", projectId: "proj-1", documentId: "doc-1" }, input);
  record.phase = "sealed";
  fake.operationView = { operation_id: record.importKey as string, state: "completed", original_status: 202, result_ref: null, job_id: null, result_metadata: { job_id: "job-9" } };
  fake.job = { state: "failed", error: "VERSION_CONFLICT" };
  const identity = { i: record.importKey, target: structuredClone(record.target) };
  await assert.rejects(engine.publish(record), (cause: unknown) => cause instanceof ImportJobError && cause.code === "VERSION_CONFLICT");
  assert.equal(record.importKey, identity.i);
  assert.deepEqual(record.target, identity.target);
  assert.equal(record.versionId, null);
});

test("resume uses server coverage rather than journal ACKs and discloses unknown gaps before sending", async () => {
  const fake = new FakeClient();
  const engine = new TransferEngine(client(fake), new FakeJournal(), { chunkSize: 2 });
  const { input, files } = fakeDirectoryUpload([["a.bin", new Uint8Array([9, 8, 7, 6])]]);
  const file = files.get("a.bin") as File;
  const record = await engine.begin("lib", "p1", { kind: "new", projectId: "proj-1" }, input);
  record.entries["a.bin"] = { kind: "file", declaredSize: 4, chunks: [{ offset: 0, length: 2, digest: await sha256Range(file, 0, 2), acked: true }], finalAcked: false };
  fake.entriesPage = [{ path: "a.bin", kind: "file" }];
  fake.ranges.set("a.bin", [{ offset: "2", byte_size: "2", bytes_sha256: await sha256Range(file, 2, 2) }]);
  let noticed = false;
  const put = fake.putUploadEntry.bind(fake);
  fake.putUploadEntry = async (id, chunk) => { assert.ok(noticed); return put(id, chunk); };
  await engine.resume(record, input, { onNotice: (notice) => { noticed = notice.includes("혼합 입력"); } });
  assert.equal(fake.puts[0].offset, 0);
  assert.deepEqual([...fake.puts[0].bytes], [9, 8]);
  assert.equal(fake.puts[1].offset, 4);
  assert.equal(fake.puts[1].bytes.byteLength, 0);
});


test("a fresh publication activates and retains its own intent before an unknown response", async () => {
  const fake = new FakeClient();
  const journal = new FakeJournal();
  const engine = new TransferEngine(client(fake), journal);
  const { input } = fakeDirectoryUpload([["a.bin", new Uint8Array([1])]]);
  const original = await engine.begin("lib", "p1", { kind: "existing", projectId: "proj-1", documentId: "doc-1" }, input);
  original.phase = "sealed";
  journal.save(original);
  const fresh: ImportJournal = { ...original, stagingOnly: false, importKey: "fresh-i", target: { ...original.target, baseVersionId: "next-base" } };
  let calls = 0;
  fake.importPublish = async () => {
    calls += 1;
    assert.equal(journal.active, fresh.importKey);
    assert.equal(journal.records.get("fresh-i")?.phase, "dispatching");
    assert.equal(journal.records.get(original.importKey as string)?.target.baseVersionId, "ver-base");
    if (calls === 1) throw new Error("accepted response was lost");
    return { kind: "committed", body: { version_id: "new-version" } };
  };
  await assert.rejects(engine.publish(fresh), /response was lost/);
  const retained = journal.load().records.find((record) => record.importKey === journal.active) as ImportJournal;
  assert.equal(retained.importKey, "fresh-i");
  assert.equal(retained.target.baseVersionId, "next-base");
  assert.equal((await engine.publish(retained)).versionId, "new-version");
  assert.equal(journal.records.size, 2);
});


test("journal getter and getItem denial block upload dispatch without hiding the cause", async () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
  try {
    for (const mode of ["getter", "getItem"] as const) {
      const denied = new DOMException(`denied ${mode}`, "SecurityError");
      Object.defineProperty(globalThis, "localStorage", mode === "getter"
        ? { configurable: true, get: () => { throw denied; } }
        : { configurable: true, value: { getItem: () => { throw denied; }, setItem: () => { assert.fail("must not overwrite inaccessible recovery records"); } } });
      const store = new BrowserJournalStore();
      assert.throws(() => store.load("lib:p1"), (error: unknown) => error instanceof JournalReadError && error.cause === denied);
      const fake = new FakeClient();
      await assert.rejects(new TransferEngine(client(fake), store).begin("lib", "p1", { kind: "new", projectId: "proj-1" }, planFromArchive(new File([new Uint8Array([1])], "a.zip"))), JournalReadError);
      assert.equal(fake.createdUploads, 0);
      assert.equal(fake.puts.length, 0);
      assert.equal(fake.completeCalls.length, 0);
      assert.equal(fake.importCalls, 0);
    }
  } finally {
    if (previous) Object.defineProperty(globalThis, "localStorage", previous);
    else Reflect.deleteProperty(globalThis, "localStorage");
  }
});

test("journal write denial blocks the dependent create instead of using memory-only persistence", async () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
  try {
    Object.defineProperty(globalThis, "localStorage", { configurable: true, value: {
      getItem: () => null,
      setItem: () => { throw new DOMException("labelled write denial", "QuotaExceededError"); },
    } });
    const fake = new FakeClient();
    await assert.rejects(new TransferEngine(client(fake), new BrowserJournalStore()).begin("lib", "p1", { kind: "new", projectId: "proj-1" }, planFromArchive(new File([new Uint8Array([1])], "a.zip"))), JournalWriteError);
    assert.equal(fake.createdUploads, 0);
  } finally {
    if (previous) Object.defineProperty(globalThis, "localStorage", previous);
    else Reflect.deleteProperty(globalThis, "localStorage");
  }
});

test("multi-chunk and resumed journals keep one ACK state per actual range intent", async () => {
  const fake = new FakeClient();
  const journal = new FakeJournal();
  const engine = new TransferEngine(client(fake), journal, { chunkSize: 2 });
  const { input, files } = fakeDirectoryUpload([["a.bin", new Uint8Array([0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11])]]);
  const record = await engine.begin("lib", "p1", { kind: "new", projectId: "proj-1" }, input);
  const original = await sha256Range(files.get("a.bin") as File, 0, 12);
  record.entries["a.bin"] = { kind: "file", declaredSize: 12, declaredSha256: original, chunks: [], finalAcked: false };
  let maxIntents = 0;
  const save = journal.save.bind(journal);
  journal.save = (current) => {
    const chunks = current.entries["a.bin"]?.chunks ?? [];
    assert.equal(new Set(chunks.map(c => `${c.offset}:${c.length}:${c.digest}`)).size, chunks.length);
    maxIntents = Math.max(maxIntents, chunks.length);
    save(current);
  };
  await assert.rejects(engine.transfer(record, input, { shouldContinue: () => fake.puts.length < 3 }), TransferCancelled);
  assert.equal(record.entries["a.bin"].chunks.length, 3);
  assert.ok(record.entries["a.bin"].chunks.every(c => c.acked));
  fake.entriesPage = [{ path: "a.bin", kind: "file" }];
  fake.ranges.set("a.bin", record.entries["a.bin"].chunks.map(c => ({ offset: String(c.offset), byte_size: String(c.length), bytes_sha256: c.digest })));
  await engine.resume(record, input);
  const stored = journal.records.get(record.importKey as string) as ImportJournal;
  assert.equal(maxIntents, 6);
  assert.equal(stored.entries["a.bin"].chunks.length, 6);
  assert.ok(stored.entries["a.bin"].chunks.every(c => c.acked));
  assert.equal(stored.entries["a.bin"].declaredSha256, original);
  assert.deepEqual(fake.puts.map(p => p.offset), [0, 2, 4, 6, 8, 10]);
});

// --- TASK-003-05 M3: structural FileLike inputs and the first-S gate ---

function structuralEntry(path: string, bytes: Uint8Array): PlannedEntry {
  const like = {
    size: bytes.byteLength,
    slice(start: number, end: number) {
      return {
        arrayBuffer: async (): Promise<ArrayBuffer> => {
          const view = bytes.slice(start, end);
          return view.buffer.slice(view.byteOffset, view.byteOffset + view.byteLength) as ArrayBuffer;
        },
      };
    },
  };
  return { path, kind: "file", size: bytes.byteLength, file: like };
}

test("a structural (non-File) native entry flows through the same shared engine byte-for-byte", async () => {
  const fake = new FakeClient();
  const journal = new FakeJournal();
  const engine = new TransferEngine(client(fake), journal, { chunkSize: 4 });
  const bytes = new Uint8Array([10, 20, 30, 40, 50, 60, 70]);
  const input: PlannedInput = {
    mode: "file",
    selectionName: "native.html",
    entries: [structuralEntry("_input", bytes)],
    limits: { emptyDirectoriesRepresented: true, symlinksRepresented: true },
  };
  const record = await engine.begin("lib", "p1", { kind: "new", projectId: "proj-1" }, input);
  const done = await engine.transfer(record, input);
  assert.equal(done.phase, "sealing");
  const chunks = fake.puts.filter((put) => put.kind === "file" && put.bytes.byteLength > 0);
  assert.deepEqual(chunks.map((put) => put.offset), [0, 4]);
  const joined = new Uint8Array([...chunks.flatMap((put) => [...put.bytes])]);
  assert.deepEqual([...joined], [...bytes]);
  for (const put of chunks) {
    const digest = createHash("sha256").update(put.bytes).digest("hex");
    assert.equal(put.chunkSha256, digest);
  }
});

test("the first-completion gate runs only for a new first S and blocks before any persistence or dispatch", async () => {
  const fake = new FakeClient();
  const journal = new FakeJournal();
  const engine = new TransferEngine(client(fake), journal, { chunkSize: 4 });
  const bytes = new Uint8Array([1, 2, 3, 4, 5]);
  const input: PlannedInput = {
    mode: "file",
    selectionName: "native.html",
    entries: [structuralEntry("_input", bytes)],
    limits: { emptyDirectoriesRepresented: true, symlinksRepresented: true },
  };
  const record = await engine.begin("lib", "p1", { kind: "new", projectId: "proj-1" }, input);
  let gateCalls = 0;
  const failingGate = async (): Promise<void> => {
    gateCalls += 1;
    throw new ApiError(409, "NATIVE_SOURCE_CHANGED", false, 0, { changed_path: "_input" });
  };
  await assert.rejects(
    engine.transfer(record, input, { verifyInputStable: failingGate }),
    (error: unknown) => error instanceof ApiError && error.code === "NATIVE_SOURCE_CHANGED",
  );
  assert.equal(gateCalls, 1);
  assert.equal(fake.completeCalls.length, 0);
  const stored = journal.records.get(record.importKey as string) as ImportJournal;
  assert.equal(stored.completeCheckpoint, null);
  assert.equal(stored.phase, "transferring");

  // A stored-checkpoint recovery (response loss) replays the SAME S with no
  // input, no handle and no gate call.
  stored.phase = "completing";
  stored.completeCheckpoint = emptyCheckpoint(2);
  stored.completeKey = "s-kept";
  journal.save(stored);
  const beforeGate = gateCalls;
  const sealed = await engine.complete(stored, { verifyInputStable: failingGate });
  assert.equal(gateCalls, beforeGate);
  assert.equal(fake.completeCalls.length, 1);
  assert.equal(fake.completeCalls[0].key, "s-kept");
  assert.equal(sealed.phase, "sealing");

  // A known settlement resets to a fresh first S: the gate runs again.
  engine.markSealSettled(stored);
  const passingGate = async (): Promise<void> => {
    gateCalls += 1;
  };
  await engine.complete(stored, { verifyInputStable: passingGate });
  assert.equal(gateCalls, beforeGate + 1);
  assert.equal(fake.completeCalls.length, 2);
  assert.notEqual(fake.completeCalls[1].key, "s-kept");
});

test("GCLIENT03 generation/draft preparation journals before upload, replays exact U and never enters the import publisher", async()=>{
 const fake=new FakeClient(),journal=new FakeJournal(),engine=new TransferEngine(client(fake),journal);
 const bytes=new Uint8Array([1,2,3]),file=new Blob([bytes]);
 const input:PlannedInput={mode:"object",selectionName:"request",entries:[{path:"request.txt",kind:"file",size:3,file}],limits:{emptyDirectoriesRepresented:true,symlinksRepresented:true}};
 const request={purpose:"body" as const,input_mode:"object" as const,target:{operation_id:"generation-K",action:"generation" as const,project_id:"p",document_id:null}};
 const record=await engine.beginOwnedUpload("l","p",request,input);
 assert.equal(fake.createdUploads,0);assert.deepEqual(journal.records.get(record.importKey!)!.uploadRequest,request);
 const seen:unknown[]=[];fake.createUpload=async(request:unknown,key:unknown)=>{seen.push([request,key]);return {upload_id:"u",checkpoint:emptyCheckpoint()};};
 await engine.attachUpload(record);assert.deepEqual(seen,[[request,record.createKey]]);
 await assert.rejects(engine.publish(record),/no import publication intent/);
 journal.failNextSave=true;
 await assert.rejects(engine.beginOwnedUpload("l","p",request,input),JournalWriteError);
 assert.equal(seen.length,1);
});

// The fake below enforces the service's opaque-object transport contract;
// GenerationSubmission and TransferEngine remain the real production clients.
test("GCLIENT16 fresh generation seals exact UTF-8 object bytes before original-key dispatch and accepted replay sends nothing", async () => {
  const fake = new FakeClient(), uploads = new FakeJournal();
  const rows = new Map<string, GenerationRecord>();
  const journal = {
    read: () => [...rows.values()].map(row => structuredClone(row)),
    save: (row: GenerationRecord) => { rows.set(row.operationId, structuredClone(row)); },
  };
  const prompt = "Explain α\nkeep exact \u0000 bytes";
  const expected = new TextEncoder().encode(prompt);
  const hash = createHash("sha256").update(expected).digest("hex");
  const creates: { request: unknown; key: unknown }[] = [];
  const dispatched: { body: GenerationRequest; key: string }[] = [];
  let received = new Uint8Array();
  const port = client(fake);
  const submission = new GenerationSubmission(port, journal, "l1", "p1", uploads,
    async <T>(_name: string, action: () => Promise<T>) => action(), () => "original-runtime");
  const record = submission.capture({project_id: "proj-1", document_id: null, intent: "draft", source_selection: {selections: []}}, prompt, () => {});
  const originalRequest = structuredClone(record.request);
  const target = {operation_id: record.operationId, action: "generation", project_id: "proj-1", document_id: null};
  fake.createUpload = async (request, key) => {
    assert.deepEqual(request, {purpose: "body", input_mode: "object", target});
    creates.push({request: structuredClone(request), key});
    fake.createdUploads++;
    fake.detail = {...fake.detail, purpose: "body", input_mode: "object", target,
      selection_name: null, reusable: false, transport_entry: "_input"};
    return {upload_id: fake.detail.upload_id, checkpoint: structuredClone(fake.detail.checkpoint)};
  };
  fake.putUploadEntry = async (id, chunk) => {
    assert.equal(id, fake.detail.upload_id);
    // Matches the real importer/HTTP boundary, independently of promptPlan.
    if (fake.detail.input_mode !== "directory" && (chunk.kind !== "file" || chunk.path !== fake.detail.transport_entry)) {
      throw new ApiError(400, "INVALID_REQUEST", false);
    }
    assert.equal(fake.detail.state, "open");
    assert.equal(chunk.offset, 0);assert.equal(chunk.totalLength, expected.byteLength);assert.equal(chunk.final, true);
    assert.equal(chunk.chunkSha256, createHash("sha256").update(chunk.bytes).digest("hex"));
    received = new Uint8Array(chunk.bytes);
    fake.puts.push(chunk);
    fake.detail.checkpoint = {generation: 2, inventory_sha256: hash, accepted_bytes: String(received.byteLength), complete_input: false};
    return {accepted_offset: String(received.byteLength), checkpoint: structuredClone(fake.detail.checkpoint)};
  };
  fake.completeUpload = async (id, checkpoint, key) => {
    assert.equal(id, fake.detail.upload_id);
    assert.deepEqual(checkpoint, {...fake.detail.checkpoint, generation: fake.detail.checkpoint.generation + 1, complete_input: true});
    assert.deepEqual(received, expected);
    fake.completeCalls.push({checkpoint: structuredClone(checkpoint), key});
    fake.detail = {...fake.detail, state: "sealed", reusable: true, checkpoint,
      sealed_object_ref: {object_id: id, bytes_sha256: hash, byte_size: String(received.byteLength), media_type: "application/octet-stream"}};
    return {kind: "accepted", body: {job_id: "seal-job"}};
  };
  port.generationStart = async (body, key) => {
    assert.equal(fake.detail.state, "sealed");
    assert.equal(key, record.operationId);
    assert.deepEqual(body, {...originalRequest, body_ref: {object_id: fake.detail.upload_id, bytes_sha256: hash, byte_size: String(expected.byteLength), media_type: "text/plain"}});
    assert.equal(Object.hasOwn(body, "prompt"), false);
    dispatched.push({body: structuredClone(body), key});
    return {operation_id: key, job_id: "generation-job", draft_id: "draft", intent: "draft", job_url: "/api/v1/jobs/generation-job"};
  };
  assert.equal((await submission.send(record, prompt, {})).job_id, "generation-job");
  assert.equal(record.promptSHA, hash);assert.equal(record.promptSize, expected.byteLength);
  assert.deepEqual(received, expected);assert.equal(fake.puts.length, 1);
  assert.equal(creates.length, 1);assert.equal(fake.completeCalls.length, 1);assert.equal(dispatched.length, 1);
  assert.equal(new Set([creates[0]!.key, fake.completeCalls[0]!.key, record.operationId]).size, 3);
  assert.equal(fake.importCalls, 0);assert.equal(rows.get(record.operationId)?.accepted?.job_id, "generation-job");
  await submission.send(record, "accepted replay ignores current text", {});
  assert.equal(creates.length, 1);assert.equal(fake.puts.length, 1);assert.equal(fake.completeCalls.length, 1);assert.equal(dispatched.length, 1);
});


function questionJournal(){
 const rows=new Map<string,QuestionRecord>();
 return {read:()=>[...rows.values()].map(row=>structuredClone(row)),save:(row:QuestionRecord)=>{rows.set(row.operationId,structuredClone(row));}};
}
const questionLock=async<T>(_name:string,action:()=>Promise<T>)=>action();
function questionBoundary(){
 const fake=new FakeClient(),port=client(fake),uploads=new FakeJournal();
 fake.operation404=true; // Fresh question keys have no durable receipt yet.
 const prepared=new Map<string,{detail:UploadDetail;bytes:Uint8Array}>();
 const starts:{version:string;body:unknown;key:string}[]=[],contexts:{binding:unknown;upload:string;key:string}[]=[],keys:string[]=[];
 port.createUpload=async(request,key)=>{
  assert.ok(request.purpose==="body"||request.purpose==="context");assert.equal(request.input_mode,"object");
  const id="u-"+(prepared.size+1);keys.push(key);prepared.set(id,{detail:{...structuredClone(fake.detail),upload_id:id,purpose:request.purpose,input_mode:"object",target:request.target,transport_entry:"_input"},bytes:new Uint8Array()});return {upload_id:id,checkpoint:emptyCheckpoint()};
 };
 port.uploadDetail=async id=>structuredClone(prepared.get(id)!.detail);
 port.uploadRanges=async()=>({items:[],next_cursor:null});
 port.putUploadEntry=async(id,chunk)=>{
  const row=prepared.get(id)!;
  // Enforce the real service's object-upload contract, independently from
  // the production promptPlan/TRANSPORT_ENTRY implementation.
  assert.equal(chunk.path,"_input");assert.equal(chunk.kind,"file");assert.equal(row.detail.state,"open");assert.equal(chunk.offset,row.bytes.byteLength);
  const joined=new Uint8Array(row.bytes.byteLength+chunk.bytes.byteLength);joined.set(row.bytes);joined.set(chunk.bytes,row.bytes.byteLength);row.bytes=joined;
  row.detail.checkpoint={generation:row.detail.checkpoint.generation+1,inventory_sha256:createHash("sha256").update(joined).digest("hex"),accepted_bytes:String(joined.length),complete_input:false};
  return {accepted_offset:String(joined.length),checkpoint:structuredClone(row.detail.checkpoint)};
 };
 port.completeUpload=async(id,checkpoint,key)=>{const row=prepared.get(id)!;assert.equal(checkpoint.complete_input,true);assert.equal(checkpoint.accepted_bytes,String(row.bytes.length));keys.push(key);row.detail={...row.detail,state:"sealed",reusable:true,checkpoint,sealed_object_ref:{object_id:id,bytes_sha256:createHash("sha256").update(row.bytes).digest("hex"),byte_size:String(row.bytes.length),media_type:"application/octet-stream"}};return {kind:"accepted",body:{job_id:"controlled-seal"}};};
 port.consumeContext=async(binding,upload,key)=>{const row=prepared.get(upload)!;assert.equal(row.detail.purpose,"context");assert.equal(row.detail.state,"sealed");assert.deepEqual(row.detail.target,binding);contexts.push({binding:structuredClone(binding),upload,key});keys.push(key);return {kind:"committed",body:{context_ref:"context-original",bytes_sha256:row.detail.sealed_object_ref!.bytes_sha256,media_type:"application/json",created_at:"controlled"}};};
 port.questionStart=async(version,body,key)=>{assert.ok(body.body_ref);const row=prepared.get(body.body_ref.object_id)!;assert.equal(row.detail.state,"sealed");assert.deepEqual(body.body_ref,{...row.detail.sealed_object_ref,media_type:"text/plain"});assert.deepEqual(row.detail.target,{operation_id:key,action:"question",version_id:version,page_path:body.page_path});assert.equal(Object.hasOwn(body,"question"),false);assert.equal(Object.hasOwn(body,"display_state"),false);starts.push({version,body:structuredClone(body),key});return {question_id:"saved-q",job_id:"saved-job",operation_id:key,job_url:"/api/v1/jobs/saved-job"};};
 return {fake,port,uploads,prepared,starts,contexts,keys};
}

test("T5QCLIENT01 real question transfer freezes complete anchor and large context, seals separate exact objects and dispatches only original K",async()=>{
 const boundary=questionBoundary(),journal=questionJournal(),submission=new QuestionSubmission(boundary.port,journal,"l","p",boundary.uploads,questionLock,()=>"runtime-original");
 const context={selection:"한글".repeat(40000),nested:{scroll:37}},text="Question original α\n";
 const request={page_path:"nested/a.html",region_id:"exact-region",page_query:"mode=A&x=1",page_fragment:"original",provider_id:"codex",source_mode:"pinned" as const,source_selection:{selections:[{source_id:"source-A",paths:[{path:"a.go",read_id:"read-A"}]}]},display_state:context};
 const record=submission.capture("version-A",request,text,()=>{});assert.equal(boundary.prepared.size,0);assert.equal(boundary.starts.length,0);
 request.page_path="changed.html";request.page_query="later";request.source_selection.selections[0]!.paths[0]!.read_id="read-B";context.nested.scroll=99;
 await submission.send(record,text,{});
 assert.equal(boundary.prepared.size,2);assert.equal(boundary.contexts.length,1);assert.equal(boundary.starts.length,1);
 assert.equal(boundary.starts[0]!.version,"version-A");assert.equal(boundary.starts[0]!.key,record.operationId);assert.deepEqual(boundary.starts[0]!.body,{page_path:"nested/a.html",region_id:"exact-region",page_query:"mode=A&x=1",page_fragment:"original",provider_id:"codex",source_mode:"pinned",source_selection:{selections:[{source_id:"source-A",paths:[{path:"a.go",read_id:"read-A"}]}]},body_ref:record.request.body_ref,context_ref:"context-original"});
 const [body,display]=[...boundary.prepared.values()];assert.equal(new TextDecoder().decode(body!.bytes),text);assert.equal(new TextDecoder().decode(display!.bytes),record.contextJSON);assert.equal(JSON.parse(record.contextJSON!).nested.scroll,37);
 assert.deepEqual(boundary.contexts[0]!.binding,{version_id:"version-A",page_path:"nested/a.html",render_instance_id:null});
 assert.equal(new Set([...boundary.keys,record.operationId]).size,6);assert.equal(boundary.fake.importCalls,0);
 await submission.send(record,"changed current editor",{});assert.equal(boundary.starts.length,1);assert.equal(boundary.prepared.size,2);
});

test("T5QCLIENT02 restore or legacy missing receipt stays GET-only, completed receipt remains readable and same-runtime retry preserves exact frozen body",async()=>{
 for(const original of [undefined,"before","same"]){
  const journal=questionJournal();let posts=0,found=false;const reads:string[]=[];const accepted={question_id:"q",job_id:"j",operation_id:"K",job_url:"/j"};
  const request={page_path:null,region_id:null,body_ref:{object_id:"o",bytes_sha256:"a".repeat(64),byte_size:"1",media_type:"text/plain"},context_ref:"ctx"};
  const port={operation:async(id:string)=>{reads.push(id);if(!found)throw new ApiError(404,"NOT_FOUND",false);return {operation_id:id,state:"completed",original_status:202,result_metadata:accepted};},questionStart:async(version:string,body:unknown,key:string)=>{posts++;assert.equal(version,"original-version");assert.equal(key,"K");assert.deepEqual(body,request);return accepted;}} as unknown as HaeryongClient;
  const record:QuestionRecord={operationId:"K",versionId:"original-version",request:structuredClone(request),textSHA:"",textSize:0,runtimeGeneration:original,dispatchReady:true};journal.save(record);
  const submission=new QuestionSubmission(port,journal,"l","p",new FakeJournal(),questionLock,()=>"same");
  if(original==="same"){await submission.send(record,"different editor",{});assert.equal(posts,1);}else{await assert.rejects(submission.send(record,"",{}),/세대/);assert.equal(posts,0);assert.deepEqual(journal.read()[0],record);found=true;await submission.send(record,"",{});assert.equal(posts,0);}
  assert.ok(reads.every(id=>id==="K"));assert.equal(journal.read()[0]!.accepted!.question_id,"q");
 }
});

test("T5QCLIENT03 local journal failure and stopped preparation cannot dispatch, while changed text never replaces the original consent",async()=>{
 const boundary=questionBoundary(),journal=questionJournal();const submission=new QuestionSubmission(boundary.port,journal,"l","p",boundary.uploads,questionLock,()=>"r");
 const request={page_path:"a.html",region_id:null};
 const stopped=submission.capture("v",request,"original",()=>{});
 await assert.rejects(submission.send(stopped,"original",{guard:()=>{throw Error("stopped");}}),/stopped/);
 assert.equal(boundary.prepared.size,0);assert.equal(boundary.starts.length,0);assert.equal(journal.read()[0]!.operationId,stopped.operationId);
 await assert.rejects(submission.send(stopped,"different",{}),/원래 질문 본문/);assert.equal(boundary.prepared.size,0);
 const failing=new QuestionSubmission(boundary.port,{read:()=>[],save:()=>{throw new JournalWriteError(Error("full"));}},"l","p",boundary.uploads,questionLock,()=>"r");
 assert.throws(()=>failing.capture("v",request,"original",()=>{}),JournalWriteError);assert.equal(boundary.prepared.size,0);
 await submission.send(stopped,"original",{});assert.equal(boundary.starts[0]!.key,stopped.operationId);
});

test("T5QCLIENT04 sealed context digest mismatch blocks question admission while preserving all preparation identity",async()=>{
 const boundary=questionBoundary(),journal=questionJournal();boundary.port.consumeContext=async()=>({kind:"committed",body:{context_ref:"wrong",bytes_sha256:"f".repeat(64),media_type:"application/json"}});
 const submission=new QuestionSubmission(boundary.port,journal,"l","p",boundary.uploads,questionLock,()=>"r");const row=submission.capture("v",{page_path:null,region_id:null,display_state:{full:"original"}},"question",()=>{});
 await assert.rejects(submission.send(row,"question",{}),/정확한 바이트/);assert.equal(boundary.starts.length,0);assert.equal(boundary.prepared.size,2);const saved=journal.read()[0]!;assert.equal(saved.operationId,row.operationId);assert.ok(saved.contextOperation);assert.equal(saved.contextDispatchReady,true);assert.equal(saved.request.context_ref,undefined);
});

test("T5QCLIENT05 saved text consumes complete verified UTF-8 and rejects truncation or digest mismatch with cancellation",async()=>{
 const text="saved source-independent answer 한글\n".repeat(5000),bytes=new TextEncoder().encode(text),hash=createHash("sha256").update(bytes).digest("hex");
 for(const broken of [false,true]){let cancelled=0;const stream={body:new ReadableStream<Uint8Array>({start(c){c.enqueue(bytes.slice(0,13));c.enqueue(bytes.slice(13));c.close();}}),bytes_sha256:broken?"0".repeat(64):hash,byte_size:String(bytes.length),media_type:"text/plain",etag:`"sha256-${hash}"`,cancel:()=>{cancelled++;}};
 if(broken)await assert.rejects(questionStreamText(stream,()=>{}),/무결성/);else assert.equal(await questionStreamText(stream,()=>{}),text);assert.equal(cancelled,1);}
});

test("T5QCLIENT06 malformed local question history fails closed rather than silently dropping or rewriting pending requests",()=>{
 const map=new Map([["haeryong.questions.v1:owner:K",'{"operationId":"K"}']]);const storage={get length(){return map.size;},key:(i:number)=>[...map.keys()][i]??null,getItem:(key:string)=>map.get(key)??null,setItem:(key:string,value:string)=>{map.set(key,value);}} as Storage;
 const original=map.get("haeryong.questions.v1:owner:K");assert.throws(()=>new BrowserQuestionJournal("owner",storage).read(),JournalReadError);assert.equal(map.get("haeryong.questions.v1:owner:K"),original);
});

test("T5QCLIENT07 initial question journal recovers its original receipt before missing-body preparation across runtime changes, without writes",async()=>{
 for(const [runtime,outcome]of [["original","found"],["changed","found"],["changed","missing"],["original","unavailable"]]as const){
  const boundary=questionBoundary(),map=new Map<string,string>();
  const storage={get length(){return map.size;},key:(i:number)=>[...map.keys()][i]??null,getItem:(key:string)=>map.get(key)??null,setItem:(key:string,value:string)=>{map.set(key,value);}}as Storage;
  const journal=new BrowserQuestionJournal("owner",storage);let current="original";
  const submission=new QuestionSubmission(boundary.port,journal,"l","p",boundary.uploads,questionLock,()=>current);
  const captured=submission.capture("original-version",{page_path:"explain.html",region_id:"risk",context_ref:"original-context"},"original question",()=>{});
  const record=journal.read()[0]!;assert.equal(record.operationId,captured.operationId);assert.equal(record.dispatchReady,false);assert.equal(record.request.body_ref,undefined);current=runtime;
  const reads:string[]=[],accepted={question_id:"original-question",job_id:"original-job",operation_id:record.operationId,job_url:"/api/v1/jobs/original-job"};
  boundary.port.operation=async key=>{reads.push(key);if(outcome!=="found")throw new ApiError(outcome==="missing"?404:503,outcome==="missing"?"NOT_FOUND":"SERVICE_RECOVERING",false);return {operation_id:key,state:"completed",original_status:202,result_ref:null,job_id:null,result_metadata:accepted};};
  if(outcome==="found"){
   assert.deepEqual(await submission.send(record,"",{}),accepted);assert.deepEqual(journal.read()[0]!.accepted,accepted);
   await submission.send(record,"",{});assert.deepEqual(reads,[captured.operationId]);
  }else{
   await assert.rejects(submission.send(record,"",{}),outcome==="missing"?/세대/:/결과 확인 필요/);assert.equal(journal.read()[0]!.accepted,undefined);assert.deepEqual(reads,[captured.operationId]);
  }
  assert.equal(record.operationId,captured.operationId);assert.equal(record.request.body_ref,undefined);assert.equal(record.request.context_ref,"original-context");
  assert.equal(boundary.prepared.size,0);assert.equal(boundary.uploads.saves,0);assert.equal(boundary.contexts.length,0);assert.equal(boundary.starts.length,0);assert.deepEqual(boundary.keys,[]);
 }
});
