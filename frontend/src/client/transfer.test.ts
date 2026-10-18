// Focused transfer-engine regressions for the reviewed correctness rules:
// journal-blocks-dispatch, exact-gap resume over legal holes, per-chunk
// cancel, concurrent-verification stability after hashing, exact S/checkpoint
// replay, target/base preservation, and the actual operations wire shapes.
import assert from "node:assert/strict";
import test from "node:test";
import { createHash } from "node:crypto";

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
import type { HaeryongClient, ListPage, OperationView, UploadCheckpoint, UploadDetail } from "./types.ts";
import { ApiError } from "./types.ts";

function fileFrom(path: string, bytes: Uint8Array): File {
  const file = new File([bytes as unknown as BlobPart], path.slice(path.lastIndexOf("/") + 1));
  Object.defineProperty(file, "webkitRelativePath", { value: `root/${path}` });
  return file;
}

function fakeDirectoryUpload(files: [string, Uint8Array][]): { input: ReturnType<typeof planFromFolder>; files: Map<string, File> } {
  const picked = files.map(([path, bytes]) => fileFrom(path, bytes));
  const input = planFromFolder(picked, "root");
  const map = new Map<string, File>();
  for (const entry of input.entries) if (entry.file) map.set(entry.path, entry.file);
  return { input, files: map };
}

interface PutCall {
  path: string;
  kind: string;
  offset: number;
  final: boolean;
  digest: string;
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
  async createUpload(): Promise<{ upload_id: string; checkpoint: UploadCheckpoint }> {
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
