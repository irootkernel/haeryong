// JSON-only native boundary. This reconstructs local JS stream/subscription
// handles; Go receives only closed operation names and JSON values.
import { NATIVE_OPERATIONS } from "./native.ts";
import type { NativePort } from "./native.ts";
import { ApiError, NetworkError, UncertainResponseError } from "./types.ts";
import type { EventSubscription, PackageStream } from "./types.ts";
type Operation = typeof NATIVE_OPERATIONS[number];
export type NativeWireOperation = Operation | "streamRead" | "streamCancel" | "eventsPoll" | "eventsClose";
export interface NativeWire {
  invoke(operation: NativeWireOperation, payload: unknown): Promise<unknown>;
}
const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === "object" && !Array.isArray(value);
const invalid = () => new UncertainResponseError(new Error("Invalid native transport response"));

export function encodeBytes(bytes: Uint8Array): string {
  let binary = "";
  for (let i = 0; i < bytes.length; i += 0x8000) binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(binary);
}
export function decodeBytes(value: string): Uint8Array {
  const binary = atob(value);
  if (btoa(binary) !== value) throw invalid();
  return Uint8Array.from(binary, (char) => char.charCodeAt(0));
}

export function createNativePort(wire: NativeWire): NativePort {
  const invoke = async (operation: NativeWireOperation, payload: unknown): Promise<unknown> => {
    let reply: unknown;
    try { reply = await wire.invoke(operation, payload); }
    catch { throw new NetworkError(); }
    if (!record(reply) || typeof reply.ok !== "boolean") throw invalid();
    if (reply.ok) return reply.value;
    if (!record(reply.error)) throw invalid();
    const error = reply.error;
    if (error.code === "NETWORK_ERROR") throw new NetworkError();
    // A committed server write whose body Go could not read keeps the
    // original K identity recoverable, exactly like the browser transport.
    if (error.code === "UNCERTAIN_RESPONSE") throw new UncertainResponseError(new Error("unreadable native success body"));
    if (!Number.isInteger(error.status) || typeof error.code !== "string" || typeof error.retryable !== "boolean") throw invalid();
    throw new ApiError(error.status as number, error.code, error.retryable, typeof error.retry_after === "number" ? error.retry_after : 0, record(error.details) ? error.details : {});
  };
  const stream = (value: unknown): PackageStream => {
    if (!record(value) || typeof value.stream_id !== "string" || !value.stream_id) throw invalid();
    const id = value.stream_id;
    let active = true, remoteClosed = false;
    let output: ReadableStreamDefaultController<Uint8Array> | null = null;
    const closeRemote = async () => {
      if (remoteClosed) return;
      remoteClosed = true;
      await invoke("streamCancel", { stream_id: id }).catch(() => undefined);
    };
    const body = new ReadableStream<Uint8Array>({
      start(controller) { output = controller; },
      async pull(controller) {
        if (!active) return;
        try {
          const part = await invoke("streamRead", { stream_id: id, max_bytes: 65536 });
          if (!active) return;
          if (!record(part) || typeof part.eof !== "boolean" || !(part.data_b64 === null || typeof part.data_b64 === "string")) throw invalid();
          const bytes = part.data_b64 === null ? new Uint8Array() : decodeBytes(part.data_b64);
          if (bytes.length > 65536 || (!bytes.length && !part.eof)) throw invalid();
          // Go's final Read may return (n>0, io.EOF). Deliver n bytes BEFORE
          // acknowledging the clean EOF; JSON must never turn them into text.
          if (bytes.length) controller.enqueue(bytes);
          if (part.eof) { active = false; controller.close(); await closeRemote(); }
        } catch (error) {
          if (active) { active = false; controller.error(error); }
          await closeRemote();
        }
      },
      async cancel() { active = false; await closeRemote(); },
    });
    return { body, cancel() {
      if (active) { active = false; output?.error(new DOMException("Transfer cancelled", "AbortError")); }
      void closeRemote();
    } };
  };
  const subscription = (value: unknown): EventSubscription => {
    if (!record(value) || typeof value.subscription_id !== "string" || !value.subscription_id) throw invalid();
    const id = value.subscription_id;
    let active = true, finish!: () => void;
    const closed = new Promise<void>((resolve) => { finish = resolve; });
    const close = () => {
      if (!active) return;
      active = false; finish();
      void invoke("eventsClose", { subscription_id: id }).catch(() => undefined);
    };
    void (async () => {
      try {
        while (active) {
          const next = await invoke("eventsPoll", { subscription_id: id });
          if (!record(next) || typeof next.closed !== "boolean") throw invalid();
          if (next.closed) break;
          // Polls are liveness hints only. No event body becomes UI authority.
          if (active) await new Promise((resolve) => setTimeout(resolve, 1000));
        }
      } finally { close(); }
    })().catch(() => undefined);
    return { closed, close };
  };
  const call = async (operation: Operation, payload: Record<string, unknown>): Promise<unknown> => {
    if (!(NATIVE_OPERATIONS as readonly string[]).includes(operation)) throw new Error("Undeclared native operation");
    let wirePayload = payload;
    if (operation === "putUploadEntry") {
      if (!record(payload.chunk) || !(payload.chunk.bytes instanceof Uint8Array)) throw new Error("Invalid native upload bytes");
      const { bytes, ...chunk } = payload.chunk;
      wirePayload = { ...payload, chunk: { ...chunk, bytes_b64: encodeBytes(bytes) } };
    }
    const value = await invoke(operation, wirePayload);
    if (operation === "packageStream" || operation === "fileStream") return stream(value);
    if (operation === "subscribeEvents") return subscription(value);
    return value;
  };
  return { call } as NativePort;
}
