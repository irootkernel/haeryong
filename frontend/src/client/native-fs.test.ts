// Native filesystem adapter regressions (TASK-003-05 M3): the source
// planner collects every metadata cursor page into ONE shared-engine
// PlannedInput whose file entries are lazy 1MiB-bounded range readers, the
// first-completion gate maps instability to the typed NATIVE_SOURCE_CHANGED
// error, and the destination adapter binds the owner consent intent and the
// delivered validator, mapping close outcomes (durability warning, unknown
// close) honestly.
import assert from "node:assert/strict";
import test from "node:test";

import { NativeCloseUncertainError, nativeDownloadTarget, planFromNativeSource, verifyNativeSourceStable } from "./native-fs.ts";
import { TRANSPORT_ENTRY } from "./transfer.ts";
import { ApiError, NetworkError, StaleResponseError, UncertainResponseError } from "./types.ts";
import type { NativeDestinationSelection, NativeSourceEntry } from "./types.ts";

class FakeFsPort {
  calls: Array<[string, Record<string, unknown>]> = [];
  tree: NativeSourceEntry[] = [];
  pageSize = 2;
  files = new Map<string, Uint8Array>();
  stable = true;
  changedPath: string | undefined;
  canceled = false;
  destExists = false;
  destDurability: "confirmed" | "unconfirmed" = "confirmed";
  destFailCloseWithNetwork = false;
  destCloseImpl: (() => Promise<{ published: true; durability: "confirmed" | "unconfirmed" }>) | null = null;
  destIntents: Array<{ replace?: boolean; expected?: { sha256: string; byte_size: number } } | undefined> = [];
  destWrites: Uint8Array[] = [];
  destClosed = 0;
  destAborted = 0;
  releaseCalls: string[] = [];

  private page(items: NativeSourceEntry[], cursor?: string): { items: NativeSourceEntry[]; next_cursor: string | null } {
    const start = cursor ? Number(cursor) : 0;
    const end = Math.min(items.length, start + this.pageSize);
    return { items: items.slice(start, end), next_cursor: end < items.length ? String(end) : null };
  }

  async chooseImportSource(mode: "directory" | "archive" | "file") {
    this.calls.push(["chooseImportSource", { mode }]);
    if (this.canceled) return { handle: "", selection_name: "", mode, entries: { items: [], next_cursor: null }, limits: { empty_directories_represented: true, symlinks_represented: true }, canceled: true as const };
    return {
      handle: "src-1",
      selection_name: "result",
      mode,
      entries: this.page(this.tree),
      limits: { empty_directories_represented: true, symlinks_represented: true },
    };
  }
  sourceEntriesCalls = 0;
  async sourceEntries(_handle: string, cursor?: string) {
    this.calls.push(["sourceEntries", { cursor }]);
    this.sourceEntriesCalls += 1;
    if (this.sourceEntriesCalls >= this.failSourceEntriesFrom) throw new Error("paging failed");
    return this.page(this.tree, cursor);
  }
  async readSourceRange(_handle: string, path: string, offset: number, length: number) {
    this.calls.push(["readSourceRange", { path, offset, length }]);
    const bytes = this.files.get(path);
    if (!bytes) throw new Error("unknown file " + path);
    const slice = bytes.slice(offset, offset + length);
    return { bytes_b64: Buffer.from(slice).toString("base64") };
  }
  async releaseSource(handle: string) {
    this.releaseCalls.push(handle);
  }
  failSourceEntriesFrom = Infinity;
  async sourceVerifyStable(_handle: string) {
    return { stable: this.stable, ...(this.changedPath ? { changed_path: this.changedPath } : {}) };
  }
  async chooseDownloadDestination(suggestedName: string, _mode: "directory" | "save-file") {
    this.calls.push(["chooseDownloadDestination", { suggestedName }]);
    if (this.canceled) return { handle: "", label: "", exists: false, canceled: true as const };
    return { handle: "dest-1", label: `Downloads/${suggestedName}`, exists: this.destExists };
  }
  async destOpen(_handle: string, intent?: { replace?: boolean; expected?: { sha256: string; byte_size: number } }) {
    this.destIntents.push(intent);
    return { opened: true as const };
  }
  async destWrite(_handle: string, bytes: Uint8Array) {
    this.destWrites.push(bytes);
    return { written: bytes.byteLength };
  }
  async destClose(_handle: string) {
    this.destClosed += 1;
    if (this.destCloseImpl) return this.destCloseImpl();
    if (this.destFailCloseWithNetwork) throw new NetworkError();
    return { published: true as const, durability: this.destDurability };
  }
  async destAbort(_handle: string) {
    this.destAborted += 1;
    return { aborted: true };
  }
}

test("planFromNativeSource collects every cursor page into one shared-engine input with lazy ranges", async () => {
  const port = new FakeFsPort();
  port.tree = [
    { path: ".hidden", kind: "file", size: 3 },
    { path: "empty", kind: "directory" },
    { path: "link", kind: "symlink", link_target: "../elsewhere" },
    { path: "nested/page.html", kind: "file", size: 5 },
  ];
  port.files.set(".hidden", new Uint8Array([1, 2, 3]));
  port.files.set("nested/page.html", new Uint8Array([9, 8, 7, 6, 5]));
  const planned = await planFromNativeSource(port, "directory");
  assert.equal("canceled" in planned, false);
  if ("canceled" in planned) return;
  assert.equal(planned.handle, "src-1");
  assert.equal(planned.input.mode, "directory");
  assert.equal(planned.input.selectionName, "result");
  assert.deepEqual(planned.input.limits, { emptyDirectoriesRepresented: true, symlinksRepresented: true });
  assert.deepEqual(planned.input.entries.map((entry) => [entry.path, entry.kind]), [
    [".hidden", "file"],
    ["empty", "directory"],
    ["link", "symlink"],
    ["nested/page.html", "file"],
  ]);
  assert.equal(planned.input.entries[2].linkTarget, "../elsewhere");
  // Lazy FileLike reads exact ranges through the closed operation only.
  const file = planned.input.entries[3].file!;
  assert.equal(file.size, 5);
  const middle = new Uint8Array(await file.slice(1, 4).arrayBuffer());
  assert.deepEqual([...middle], [8, 7, 6]);
  assert.deepEqual(port.calls.filter(([operation]) => operation === "readSourceRange"), [["readSourceRange", { path: "nested/page.html", offset: 1, length: 3 }]]);
  // Archive/file modes map to the shared transport entry exactly like the
  // browser planners.
  const archive = await planFromNativeSource(port, "archive");
  if ("canceled" in archive) throw new Error("unreachable");
  assert.deepEqual(archive.input.entries.map((entry) => entry.path), [TRANSPORT_ENTRY]);
  // A canceled panel is an honest owner cancel.
  port.canceled = true;
  const canceled = await planFromNativeSource(port, "directory");
  assert.equal("canceled" in canceled, true);
});

test("metadata paging failure releases the owned handle instead of leaking it", async () => {
  const port = new FakeFsPort();
  port.tree = [
    { path: "a", kind: "file", size: 1 },
    { path: "b", kind: "file", size: 1 },
    { path: "c", kind: "file", size: 1 },
  ];
  port.pageSize = 1;
  port.failSourceEntriesFrom = 2;
  await assert.rejects(planFromNativeSource(port, "directory"), /paging failed/);
  assert.deepEqual(port.releaseCalls, ["src-1"]);
});

test("verifyNativeSourceStable maps instability to the typed changed-source failure", async () => {
  const port = new FakeFsPort();
  port.stable = false;
  port.changedPath = "nested/page.html";
  await assert.rejects(
    verifyNativeSourceStable(port, "src-1"),
    (error: unknown) => error instanceof ApiError && error.code === "NATIVE_SOURCE_CHANGED" && error.details.changed_path === "nested/page.html",
  );
  port.stable = true;
  await verifyNativeSourceStable(port, "src-1");
});

test("nativeDownloadTarget binds consent intent and validator, and maps close outcomes honestly", async () => {
  const port = new FakeFsPort();
  const selection: NativeDestinationSelection = { handle: "dest-1", label: "Downloads/a.zip", exists: true };
  const target = nativeDownloadTarget(port, selection);
  assert.equal(await target.exists(), true);
  assert.equal(target.label, "Downloads/a.zip");

  const expected = { sha256: "a".repeat(64), byte_size: 12 };
  const writable = await target.openWritable({ replace: true, expected });
  assert.deepEqual(port.destIntents, [{ replace: true, expected }]);
  await writable.write(new Uint8Array([1, 2, 3]));
  assert.equal(port.destWrites.length, 1);
  // Confirmed publication closes without a warning.
  const clean = await writable.close();
  assert.equal(clean, undefined);
  assert.equal(port.destClosed, 1);

  // A post-publication durability failure is a SUCCESS warning, never an
  // incomplete claim.
  port.destDurability = "unconfirmed";
  const warnedWritable = await target.openWritable();
  const warned = await warnedWritable.close();
  assert.equal(warned?.warning, "게시는 완료되었으나 디스크 동기화 확인에 실패했습니다.");

  // An unobserved close is the typed uncertain outcome; the adapter never
  // silently invents success or failure.
  port.destFailCloseWithNetwork = true;
  const uncertainWritable = await target.openWritable();
  await assert.rejects(
    uncertainWritable.close(),
    (error: unknown) => error instanceof Error && (error as Error & { code?: string }).code === "NATIVE_CLOSE_UNCERTAIN",
  );
  // Abort reaches the destination attempt exactly once.
  const aborting = await target.openWritable();
  await aborting.abort?.();
  assert.equal(port.destAborted, 1);
});

test("ANY unobserved close reply classifies uncertain; only typed ApiErrors stay definitive", async () => {
  const port = new FakeFsPort();
  const selection: NativeDestinationSelection = { handle: "dest-1", label: "Downloads/a.zip", exists: false };
  const target = nativeDownloadTarget(port, selection);
  for (const unobserved of [new UncertainResponseError(new Error("unreadable native success body")), new StaleResponseError(), new Error("bridge vanished")]) {
    port.destCloseImpl = () => Promise.reject(unobserved);
    const writable = await target.openWritable();
    await assert.rejects(
      writable.close(),
      (error: unknown) => error instanceof NativeCloseUncertainError && error.cause === unobserved,
    );
  }
  // A typed conflict stays the definitive conflict, never re-labelled.
  port.destCloseImpl = () => Promise.reject(new ApiError(409, "NATIVE_DESTINATION_CONFLICT", false));
  const conflictWritable = await target.openWritable();
  await assert.rejects(
    conflictWritable.close(),
    (error: unknown) => error instanceof ApiError && error.code === "NATIVE_DESTINATION_CONFLICT",
  );
});
