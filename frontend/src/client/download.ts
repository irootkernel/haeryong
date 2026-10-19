// Package download destination engine. Owns the consent-gated acquisition
// boundary, the explicit conflict rules and the streamed write contract:
//
// - Consent always PRECEDES any picker/drop/write step; an existing target —
//   including an existing EMPTY file — may be replaced only under that
//   consent. Existence is never inferred from handle size or state.
// - Directory-handle targets expose an honest existence check
//   (getFileHandle without create); it is best-effort, FSA has no atomic
//   no-replace, and a created-after-check race is absorbed by the consented
//   replacement semantics. Save-picker targets report existence as
//   undetectable and never claim "no conflict occurred".
// - Completion requires a clean reader end AND a successful close(). Any
//   reader error, destination error or cancellation aborts the writable
//   (close is never called) and leaves the destination marked incomplete;
//   no partial transfer is ever labelled complete.
// - No whole-body Blob is used at any point: archive bytes stream from the
//   authenticated response body into the writable destination.
import type { HaeryongClient, PackageStream } from "./types.ts";

export class PackageDownloadIncomplete extends Error {
  readonly stage: "reader" | "write" | "close" | "cancelled";
  /** Preserved typed failure of the abort/cleanup attempt on the failed path, when the destination reported one. */
  cleanupError?: unknown;
  constructor(stage: PackageDownloadIncomplete["stage"], cause?: unknown) {
    super(`package download incomplete at ${stage}`);
    this.name = "PackageDownloadIncomplete";
    this.stage = stage;
    if (cause !== undefined) this.cause = cause;
  }
}

/** Minimal writable destination surface (FileSystemWritableFileStream-like). */
export interface WritableDestination {
  write(bytes: Uint8Array): Promise<void>;
  close(): Promise<void | { warning?: string }>;
  abort?(): Promise<void>;
}

function fsaWritable(stream: FileSystemWritableFileStream): WritableDestination {
  return {
    write: async (bytes: Uint8Array) => {
      await stream.write(bytes as unknown as FileSystemWriteChunkType);
    },
    close: async () => {
      await stream.close();
    },
    abort: async () => {
      await stream.abort();
    },
  };
}

export interface DownloadCallbacks {
  onProgress?: (bytes: number) => void;
  /** Polled between streamed chunks; false cancels (abort, never close). */
  shouldContinue?: () => boolean;
  /** Synchronous authority gate before the writable opens and every chunk. */
  guard?: () => void;
}

export interface DownloadTarget {
  /**
   * Honest existence probe. Directory-backed targets detect existence
   * exactly (never by size); save-picker targets return null — prior
   * existence is undetectable and never claimed either way.
   */
  exists(): Promise<boolean | null>;
  /**
   * Open one writable attempt. `intent` carries the owner's explicit
   * replace consent and, when the delivered representation provides one,
   * the strong expected validator; browser targets ignore it.
   */
  openWritable(intent?: { replace?: boolean; expected?: { sha256: string; byte_size: number } }): Promise<WritableDestination>;
  /** Human label for conflict dialogs and progress notices. */
  label: string;
}

/**
 * Stream the authenticated archive into a destination. The destination is
 * opened only after the caller's consent/acquisition/conflict steps; this
 * function never acquires handles itself. A stream that carries its own
 * strong validator (native file delivery) binds it to the writable attempt
 * so nothing unverified is published; the owner's replace consent travels
 * alongside. Completion may carry an honest durability warning.
 */
export async function downloadPackage(
  client: Pick<HaeryongClient, "packageStream">,
  versionId: string,
  target: DownloadTarget,
  callbacks: DownloadCallbacks = {},
  intent: { replace?: boolean } = {},
): Promise<{ bytes: number; warning?: string }> {
  let stream: PackageStream;
  try {
    stream = await client.packageStream(versionId);
  } catch (cause) {
    throw new PackageDownloadIncomplete("reader", cause);
  }
  let writable: WritableDestination;
  try {
    callbacks.guard?.();
    const openIntent: { replace?: boolean; expected?: { sha256: string; byte_size: number } } = { ...intent };
    if (stream.validator) openIntent.expected = stream.validator;
    writable = await target.openWritable(openIntent);
  } catch (cause) {
    stream.cancel();
    throw new PackageDownloadIncomplete("write", cause);
  }
  const reader = stream.body.getReader();
  let bytes = 0;
  try {
    for (;;) {
      const chunk = await reader.read();
      // Cancel/authority is re-checked after every awaited read: a cancel or
      // an authority change arriving during a pending read must not
      // acknowledge those bytes.
      if (callbacks.shouldContinue && !callbacks.shouldContinue()) {
        throw new PackageDownloadIncomplete("cancelled");
      }
      callbacks.guard?.();
      if (chunk.done) break;
      bytes += chunk.value.byteLength;
      try {
        await writable.write(chunk.value);
      } catch (cause) {
        throw new PackageDownloadIncomplete("write", cause);
      }
      callbacks.onProgress?.(bytes);
    }
    // ...and again immediately before close: a cancel that landed on the
    // clean end-of-stream may never still publish a completed destination.
    if (callbacks.shouldContinue && !callbacks.shouldContinue()) {
      throw new PackageDownloadIncomplete("cancelled");
    }
  } catch (cause) {
    // Reader or destination failure: abort the swap (never close), cancel the
    // stream and surface an explicitly incomplete outcome. A FAILED abort is
    // preserved beside the primary failure (its typed code/details carry the
    // exact surviving temp path) instead of being swallowed.
    let cleanupFailure: unknown;
    try {
      await writable.abort?.();
    } catch (abortError) {
      cleanupFailure = abortError;
    }
    stream.cancel();
    const incomplete = cause instanceof PackageDownloadIncomplete ? cause : new PackageDownloadIncomplete("reader", cause);
    if (cleanupFailure !== undefined) incomplete.cleanupError = cleanupFailure;
    throw incomplete;
  }
  try {
    const closed = await writable.close();
    // A committed publication may still report an honest durability warning
    // (for example a failed directory fsync); it is never a failure and
    // never retried as a close.
    return { bytes, warning: closed && typeof closed === "object" && typeof closed.warning === "string" ? closed.warning : undefined };
  } catch (cause) {
    // A failed close never counts as completion even after a clean stream end.
    throw new PackageDownloadIncomplete("close", cause);
  }
}

/** FSA availability for the streaming destination transport. */
export function destinationTransportSupport(): { savePicker: boolean; directoryDrop: boolean } {
  const globalObject = globalThis as { showSaveFilePicker?: unknown };
  const item = typeof DataTransferItem !== "undefined" ? (DataTransferItem.prototype as unknown as { getAsFileSystemHandle?: unknown }) : undefined;
  return {
    savePicker: typeof globalObject.showSaveFilePicker === "function",
    directoryDrop: typeof item?.getAsFileSystemHandle === "function",
  };
}

/** Wrap a save-picker file handle; existence is undetectable by design. */
export function savePickerTarget(handle: FileSystemFileHandle): DownloadTarget {
  return {
    label: handle.name,
    exists: async () => null,
    openWritable: async () => fsaWritable(await handle.createWritable()),
  };
}

/** Wrap an owner-dropped directory handle and a chosen file name. */
export function directoryTarget(directory: FileSystemDirectoryHandle, name: string): DownloadTarget {
  return {
    label: `${directory.name}/${name}`,
    exists: async () => {
      try {
        await directory.getFileHandle(name);
        return true;
      } catch (error) {
        if (error instanceof DOMException && error.name === "NotFoundError") return false;
        throw error;
      }
    },
    openWritable: async () => {
      const file = await directory.getFileHandle(name, { create: true });
      return fsaWritable(await file.createWritable());
    },
  };
}

/** Stable attachment file name for one exact version archive. */
export function packageFileName(versionId: string): string {
  return `haeryong-${versionId}.zip`;
}
