// Native filesystem transfer adapters (TASK-003-05 M3). These bind the
// opaque, era-bound Go handles to the SAME shared transfer/download engines:
// the source planner produces one PlannedInput whose file entries are lazy
// structural FileLike ranges (no second import engine), and the destination
// adapter is one DownloadTarget writing through the private sibling temp
// with pre-publication verification. There is no arbitrary path/URL proxy:
// every byte and name travels through the closed named operations.
import { TRANSPORT_ENTRY } from "./transfer.ts";
import type { PlannedEntry, PlannedInput } from "./transfer.ts";
import type { DownloadTarget, WritableDestination } from "./download.ts";
import { ApiError } from "./types.ts";
import type { NativeDestinationSelection, NativeDestIntent, NativeSourceEntry, NativeSourceSelection } from "./types.ts";
import { decodeBytes } from "./native-wire.ts";

/** Narrow facet the adapters need; NativeHaeryongClient satisfies it. */
export interface NativeFsClient {
  chooseImportSource(mode: "directory" | "archive" | "file"): Promise<NativeSourceSelection>;
  sourceEntries(handle: string, cursor?: string): Promise<{ items: NativeSourceEntry[]; next_cursor: string | null }>;
  readSourceRange(handle: string, path: string, offset: number, length: number): Promise<{ bytes_b64: string }>;
  releaseSource(handle: string): Promise<void>;
  sourceVerifyStable(handle: string): Promise<{ stable: boolean; changed_path?: string }>;
  chooseDownloadDestination(suggestedName: string, mode: "directory" | "save-file"): Promise<NativeDestinationSelection>;
  destOpen(handle: string, intent?: NativeDestIntent): Promise<{ opened: true }>;
  destWrite(handle: string, bytes: Uint8Array): Promise<{ written: number }>;
  destClose(handle: string): Promise<{ published: true; durability: "confirmed" | "unconfirmed" }>;
  destAbort(handle: string): Promise<{ aborted: boolean; published?: boolean }>;
}

/** A native source selection plus the handle owning it until explicit release. */
export interface PlannedNativeSource {
  input: PlannedInput;
  handle: string;
}

/** One lazy range view over a captured native file entry. */
function lazyNativeFile(client: NativeFsClient, handle: string, path: string, size: number) {
  return {
    size,
    slice(start: number, end: number) {
      return {
        arrayBuffer: async (): Promise<ArrayBuffer> => {
          if (!Number.isSafeInteger(start) || !Number.isSafeInteger(end) || start < 0 || end < start || end > size) {
            throw new Error(`native range ${start}:${end} is outside the captured file`);
          }
          const parts: Uint8Array[] = [];
          let at = start;
          while (at < end) {
            const length = Math.min(1024 * 1024, end - at);
            const reply = await client.readSourceRange(handle, path, at, length);
            const bytes = decodeBytes(reply.bytes_b64);
            if (bytes.byteLength !== length) {
              throw new Error("native source returned a truncated range");
            }
            parts.push(bytes);
            at += length;
          }
          const joined = new Uint8Array(end - start);
          let offset = 0;
          for (const part of parts) {
            joined.set(part, offset);
            offset += part.byteLength;
          }
          return joined.buffer as ArrayBuffer;
        },
      };
    },
  };
}

/**
 * Plan the owner-selected native source into ONE shared-engine PlannedInput.
 * Every metadata cursor page is collected up front (paging is local
 * granularity, not a tree cap); file entries become lazy range readers.
 * Archive/file modes map to the shared transport entry exactly like the
 * browser planners.
 */
export async function planFromNativeSource(client: NativeFsClient, mode: "directory" | "archive" | "file"): Promise<PlannedNativeSource | { canceled: true }> {
  const selection = await client.chooseImportSource(mode);
  if (selection.canceled) return { canceled: true };
  try {
    const captured: NativeSourceEntry[] = [...selection.entries.items];
    let cursor = selection.entries.next_cursor;
    while (cursor !== null) {
      const page = await client.sourceEntries(selection.handle, cursor ?? undefined);
      captured.push(...page.items);
      cursor = page.next_cursor;
    }
    const entries: PlannedEntry[] =
      selection.mode === "directory"
        ? captured.map((entry) => ({
            path: entry.path,
            kind: entry.kind,
            size: entry.size,
            linkTarget: entry.link_target,
            file: entry.kind === "file" ? lazyNativeFile(client, selection.handle, entry.path, entry.size ?? 0) : undefined,
          }))
        : [
            {
              path: TRANSPORT_ENTRY,
              kind: "file",
              size: captured[0]?.size ?? 0,
              file: lazyNativeFile(client, selection.handle, captured[0]?.path ?? TRANSPORT_ENTRY, captured[0]?.size ?? 0),
            },
          ];
    const input: PlannedInput = {
      mode: selection.mode,
      selectionName: selection.selection_name,
      entries,
      limits: {
        emptyDirectoriesRepresented: selection.limits.empty_directories_represented,
        symlinksRepresented: selection.limits.symlinks_represented,
      },
    };
    return { input, handle: selection.handle };
  } catch (error) {
    // Metadata paging must not leak the owned handle: an honest release
    // before rethrowing leaves no orphaned Go-side source.
    try {
      await client.releaseSource(selection.handle);
    } catch {
      // A failed release is typed by Go; the handle dies with its era.
    }
    throw error;
  }
}

/**
 * The first-completion source stability gate: throws the typed
 * NATIVE_SOURCE_CHANGED error (with the changed path when known) so the
 * shared engine blocks the completion dispatch BEFORE persisting the first
 * completion intent, while staging stays retained.
 */
export async function verifyNativeSourceStable(client: NativeFsClient, handle: string): Promise<void> {
  const result = await client.sourceVerifyStable(handle);
  if (!result.stable) {
    throw new ApiError(409, "NATIVE_SOURCE_CHANGED", false, 0, result.changed_path ? { changed_path: result.changed_path } : {});
  }
}

/** Typed error when a delivered file representation carries no usable validator. */
export class NativeValidatorMissingError extends Error {
  constructor() {
    super("이 파일의 검증 정보(Repr-Digest)가 없어 검증된 저장을 진행할 수 없습니다.");
    this.name = "NativeValidatorMissingError";
    this.code = "NATIVE_VALIDATOR_MISSING";
  }
  readonly code: string;
}

/** Typed error when the destination close outcome could not be observed. */
export class NativeCloseUncertainError extends Error {
  constructor(cause?: unknown) {
    super("완료 여부를 확인할 수 없습니다. 대상에 이미 파일이 있을 수 있으니 확인 후 다시 시도하세요.");
    this.name = "NativeCloseUncertainError";
    this.code = "NATIVE_CLOSE_UNCERTAIN";
    if (cause !== undefined) this.cause = cause;
  }
  readonly code: string;
}

const DURABILITY_WARNING = "게시는 완료되었으나 디스크 동기화 확인에 실패했습니다.";

/**
 * Wrap one owner-selected native destination as the shared DownloadTarget.
 * Existence is exact (the Go probe stat'ed the final target); opening binds
 * the explicit consent intent (replace) and the delivered representation's
 * expected digest/size; close verifies before publication and returns the
 * honest durability warning instead of a false incomplete claim.
 */
export function nativeDownloadTarget(client: NativeFsClient, selection: NativeDestinationSelection): DownloadTarget {
  const handle = selection.handle;
  return {
    label: selection.label,
    exists: async () => selection.exists,
    openWritable: async (intent?: { replace?: boolean; expected?: { sha256: string; byte_size: number } }) => {
      await client.destOpen(handle, intent);
      const writable: WritableDestination = {
        write: async (bytes: Uint8Array) => {
          await client.destWrite(handle, bytes);
        },
        close: async () => {
          try {
            const result = await client.destClose(handle);
            if (result.durability === "unconfirmed") return { warning: DURABILITY_WARNING };
            return;
          } catch (error) {
            if (error instanceof ApiError) {
              // A typed Go answer is definitive (conflict, verification
              // failure, cleanup failure): it propagates unchanged.
              throw error;
            }
            // ANY unobserved reply — network loss, an unreadable committed
            // body (UncertainResponseError), a stale-era discard or another
            // transport failure — leaves the publication outcome unknown.
            // Retrying means a NEW attempt through the exact existence/
            // conflict/explicit-replace path; never a blind re-close.
            throw new NativeCloseUncertainError(error);
          }
        },
        abort: async () => {
          await client.destAbort(handle);
        },
      };
      return writable;
    },
  };
}
