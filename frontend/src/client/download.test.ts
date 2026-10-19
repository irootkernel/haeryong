// Focused destination-contract regressions: multi-chunk streamed completion,
// reader/destination/cancel failure paths (abort, never close, never a false
// complete), cancel-during-pending-read and cancel-before-close, the era
// freshness guard over consumed streams, and honest existence semantics for
// directory-backed versus save-picker targets.
import assert from "node:assert/strict";
import test from "node:test";

import { directoryTarget, downloadPackage, PackageDownloadIncomplete, savePickerTarget } from "./download.ts";
import { eraGuardedStream } from "./stream.ts";
import { StaleResponseError } from "./types.ts";
import type { HaeryongClient, PackageStream } from "./types.ts";

class RecordingWritable {
  writes: Uint8Array[] = [];
  closed = false;
  aborted = false;
  failWrite = false;
  failClose = false;
  async write(bytes: Uint8Array): Promise<void> {
    if (this.failWrite) throw new Error("destination rejected write");
    this.writes.push(bytes);
  }
  async close(): Promise<void> {
    if (this.failClose) throw new Error("close failed");
    this.closed = true;
  }
  async abort(): Promise<void> {
    this.aborted = true;
  }
}

function streamOf(chunks: Uint8Array[], opts: { failAt?: number } = {}): PackageStream {
  let index = 0;
  const body = new ReadableStream<Uint8Array>({
    pull(controller) {
      if (opts.failAt !== undefined && index === opts.failAt) {
        controller.error(new Error("source failed mid-stream"));
        return;
      }
      if (index >= chunks.length) {
        controller.close();
        return;
      }
      controller.enqueue(chunks[index]);
      index += 1;
    },
  });
  return { body, cancel: () => undefined };
}

function clientWith(stream: PackageStream): HaeryongClient {
  return { packageStream: async () => stream } as unknown as HaeryongClient;
}

test("a clean multi-chunk stream completes with close and never aborts", async () => {
  const writable = new RecordingWritable();
  const chunks = [new Uint8Array(8), new Uint8Array(8), new Uint8Array(4)];
  const seen: number[] = [];
  const result = await downloadPackage(clientWith(streamOf(chunks)), "ver-1", {
    label: "t",
    exists: async () => false,
    openWritable: async () => writable,
  }, { onProgress: (bytes) => seen.push(bytes) });
  assert.equal(result.bytes, 20);
  assert.equal(writable.closed, true);
  assert.equal(writable.aborted, false);
  assert.deepEqual(seen, [8, 16, 20]);
});

test("a mid-stream reader failure aborts the writable and never closes", async () => {
  const writable = new RecordingWritable();
  await assert.rejects(
    downloadPackage(clientWith(streamOf([new Uint8Array(8), new Uint8Array(8)], { failAt: 1 })), "ver-1", {
      label: "t",
      exists: async () => false,
      openWritable: async () => writable,
    }),
    (error: unknown) => error instanceof PackageDownloadIncomplete && error.stage === "reader",
  );
  assert.equal(writable.aborted, true);
  assert.equal(writable.closed, false);
});

test("a destination write failure is staged as a write error, aborted and never closed", async () => {
  const writable = new RecordingWritable();
  writable.failWrite = true;
  await assert.rejects(
    downloadPackage(clientWith(streamOf([new Uint8Array(8)])), "ver-1", {
      label: "t",
      exists: async () => false,
      openWritable: async () => writable,
    }),
    (error: unknown) => error instanceof PackageDownloadIncomplete && error.stage === "write",
  );
  assert.equal(writable.aborted, true);
  assert.equal(writable.closed, false);
});

test("a failed close after a clean stream end is never a completion", async () => {
  const writable = new RecordingWritable();
  writable.failClose = true;
  await assert.rejects(
    downloadPackage(clientWith(streamOf([new Uint8Array(8)])), "ver-1", {
      label: "t",
      exists: async () => false,
      openWritable: async () => writable,
    }),
    (error: unknown) => error instanceof PackageDownloadIncomplete && error.stage === "close",
  );
});

test("a cancel observed after a pending read aborts instead of acknowledging the bytes", async () => {
  const writable = new RecordingWritable();
  let reads = 0;
  await assert.rejects(
    downloadPackage(clientWith(streamOf([new Uint8Array(8), new Uint8Array(8)])), "ver-1", {
      label: "t",
      exists: async () => false,
      openWritable: async () => writable,
    }, { shouldContinue: () => (reads += 1) === 1 }),
    (error: unknown) => error instanceof PackageDownloadIncomplete && error.stage === "cancelled",
  );
  assert.equal(writable.aborted, true);
  assert.equal(writable.closed, false);
  // The second chunk arrived but was never acknowledged into the destination.
  assert.equal(writable.writes.length, 1);
});

test("a cancel landing between the clean end and close never publishes completion", async () => {
  const writable = new RecordingWritable();
  let calls = 0;
  await assert.rejects(
    downloadPackage(clientWith(streamOf([new Uint8Array(8)])), "ver-1", {
      label: "t",
      exists: async () => false,
      openWritable: async () => writable,
    }, { shouldContinue: () => (calls += 1) === 1 }),
    (error: unknown) => error instanceof PackageDownloadIncomplete && error.stage === "cancelled",
  );
  assert.equal(writable.aborted, true);
  assert.equal(writable.closed, false);
});

test("eraGuardedStream keeps one reader across pulls and errors on a stale era without closing", async () => {
  let era = 1;
  const source = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(new Uint8Array([1]));
      controller.enqueue(new Uint8Array([2]));
      controller.enqueue(new Uint8Array([3]));
    },
  });
  const guarded = eraGuardedStream(source, () => era !== 1);
  const reader = guarded.getReader();
  const first = await reader.read();
  assert.equal(first.done, false);
  assert.equal(first.value[0], 1);
  const second = await reader.read();
  assert.equal(second.done, false);
  assert.equal(second.value[0], 2);
  era = 2; // invalidate() happened while the third read was pending.
  await assert.rejects(
    reader.read(),
    (error: unknown) => error instanceof StaleResponseError,
  );
});

test("eraGuardedStream propagates cancellation through the single reader", async () => {
  let cancelled = false;
  const source = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(new Uint8Array([1]));
    },
    cancel() {
      cancelled = true;
    },
  });
  const guarded = eraGuardedStream(source, () => false);
  const reader = guarded.getReader();
  const chunk = await reader.read();
  assert.equal(chunk.done, false);
  assert.equal(chunk.value?.[0], 1);
  await reader.cancel("owner cancelled");
  assert.equal(cancelled, true);
});

test("a stale stream surfaces as a reader failure inside downloadPackage", async () => {
  const writable = new RecordingWritable();
  let era = 1;
  const source = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(new Uint8Array([1]));
      controller.enqueue(new Uint8Array([2]));
    },
  });
  const guarded = eraGuardedStream(source, () => era !== 1);
  era = 2;
  await assert.rejects(
    downloadPackage(clientWith({ body: guarded, cancel: () => undefined }), "ver-1", {
      label: "t",
      exists: async () => false,
      openWritable: async () => writable,
    }),
    (error: unknown) => error instanceof PackageDownloadIncomplete && error.stage === "reader",
  );
  assert.equal(writable.aborted, true);
  assert.equal(writable.closed, false);
});

test("directory targets detect existence exactly; save-picker targets never claim it", async () => {
  const present = {
    name: "dest",
    getFileHandle: async (name: string) => (name === "haeryong-ver-1.zip" ? {} : Promise.reject(new DOMException("nf", "NotFoundError"))),
  } as unknown as FileSystemDirectoryHandle;
  const target = directoryTarget(present, "haeryong-ver-1.zip");
  assert.equal(await target.exists(), true);
  assert.equal(await target.exists(), true);
  const absent = {
    name: "dest",
    getFileHandle: async () => Promise.reject(new DOMException("nf", "NotFoundError")),
  } as unknown as FileSystemDirectoryHandle;
  assert.equal(await directoryTarget(absent, "other.zip").exists(), false);
  const savePicker = savePickerTarget({ name: "x.zip" } as unknown as FileSystemFileHandle);
  assert.equal(await savePicker.exists(), null);
});

// --- TASK-003-05 M3: consent intent, delivered validator and honest warnings ---

class IntentRecordingTarget {
  openedIntents: Array<{ replace?: boolean; expected?: { sha256: string; byte_size: number } } | undefined> = [];
  writable: RecordingWritable & { closeWarning?: string };
  constructor(closeWarning?: string) {
    this.writable = Object.assign(new RecordingWritable(), {
      close: async (): Promise<void | { warning?: string }> => {
        if (this.writable.failClose) throw new Error("close failed");
        this.writable.closed = true;
        return closeWarning ? { warning: closeWarning } : undefined;
      },
    });
  }
  label = "recorded";
  async exists(): Promise<boolean | null> {
    return false;
  }
  async openWritable(intent?: { replace?: boolean; expected?: { sha256: string; byte_size: number } }) {
    this.openedIntents.push(intent);
    return this.writable;
  }
}

test("the stream's own validator and the owner's replace consent reach the writable attempt", async () => {
  const validator = { sha256: "b".repeat(64), byte_size: 4 };
  const stream = { ...streamOf([new Uint8Array([1, 2, 3, 4])]), validator };
  const target = new IntentRecordingTarget();
  const result = await downloadPackage(clientWith(stream as PackageStream), "ver-1", target, {}, { replace: true });
  assert.equal(result.bytes, 4);
  assert.equal(result.warning, undefined);
  assert.deepEqual(target.openedIntents, [{ replace: true, expected: validator }]);
  assert.equal(target.writable.closed, true);
});

test("a stream without a validator opens without an expected proof; browser targets ignore the intent", async () => {
  const target = new IntentRecordingTarget();
  const result = await downloadPackage(clientWith(streamOf([new Uint8Array([9])])), "ver-1", target);
  assert.equal(result.bytes, 1);
  assert.deepEqual(target.openedIntents, [{}]);
});

test("a committed close warning travels to the done result as an honest non-failure", async () => {
  const warning = "게시는 완료되었으나 디스크 동기화 확인에 실패했습니다.";
  const target = new IntentRecordingTarget(warning);
  const result = await downloadPackage(clientWith(streamOf([new Uint8Array([5, 6])])), "ver-1", target);
  assert.equal(result.warning, warning);
  assert.equal(target.writable.closed, true);
  assert.equal(target.writable.aborted, false);
});

test("a failed abort on the failure path is preserved beside the primary failure", async () => {
  const target = new IntentRecordingTarget();
  target.writable.failWrite = true;
  target.writable.abort = async () => {
    target.writable.aborted = true;
    const failure = new Error("cleanup failed") as Error & { code?: string; details?: { temp?: string } };
    failure.code = "NATIVE_CLEANUP_FAILED";
    failure.details = { temp: "/tmp/.haeryong-zzz.partial" };
    throw failure;
  };
  let caught: PackageDownloadIncomplete | undefined;
  await downloadPackage(clientWith(streamOf([new Uint8Array([1])])), "ver-1", target).catch((error) => {
    caught = error as PackageDownloadIncomplete;
  });
  assert.ok(caught);
  assert.equal(caught.stage, "write");
  const cleanup = caught.cleanupError as { code?: string; details?: { temp?: string } };
  assert.equal(cleanup.code, "NATIVE_CLEANUP_FAILED");
  assert.equal(cleanup.details?.temp, "/tmp/.haeryong-zzz.partial");
  assert.equal(target.writable.closed, false);
  assert.equal(target.writable.aborted, true);
});
