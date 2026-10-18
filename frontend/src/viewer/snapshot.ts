import { sha256 } from "@noble/hashes/sha2.js";

const utf8 = new TextEncoder();
export const hex = (bytes: Uint8Array): string => Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("");

// Escape bounded pieces of even a single large string. Keeping a surrogate
// pair together makes the result identical to JSON.stringify's UTF-8 bytes.
function* quoted(value: string): Generator<string> {
  yield '"';
  for (let start = 0; start < value.length;) {
    let end = Math.min(start + 1024, value.length);
    if (end < value.length && value.charCodeAt(end - 1) >= 0xd800 && value.charCodeAt(end - 1) <= 0xdbff && value.charCodeAt(end) >= 0xdc00 && value.charCodeAt(end) <= 0xdfff) end--;
    yield JSON.stringify(value.slice(start, end)).slice(1, -1);
    start = end;
  }
  yield '"';
}

type Work = { kind: "value"; value: unknown } | { kind: "text"; value: string } | { kind: "leave"; value: object } | { kind: "array"; value: unknown[]; index: number } | { kind: "object"; value: Record<string, unknown>; keys: string[]; index: number };

// An explicit traversal stack avoids a serializer-imposed nesting limit. The
// immutable graph remains in memory; its complete serialized form never does.
function* pieces(snapshot: unknown): Generator<Uint8Array> {
  const stack: Work[] = [{ kind: "value", value: snapshot }];
  const ancestors = new Set<object>();
  while (stack.length) {
    const item = stack.pop()!;
    if (item.kind === "leave") { ancestors.delete(item.value); continue; }
    if (item.kind === "text") { yield utf8.encode(item.value); continue; }
    if (item.kind === "array") {
      if (item.index === item.value.length) { yield utf8.encode("]"); continue; }
      if (item.index) yield utf8.encode(",");
      stack.push({ ...item, index: item.index + 1 }, { kind: "value", value: item.value[item.index] ?? null });
      continue;
    }
    if (item.kind === "object") {
      if (item.index === item.keys.length) { yield utf8.encode("}"); continue; }
      if (item.index) yield utf8.encode(",");
      const key = item.keys[item.index]!;
      for (const text of quoted(key)) yield utf8.encode(text);
      yield utf8.encode(":");
      stack.push({ ...item, index: item.index + 1 }, { kind: "value", value: item.value[key] });
      continue;
    }
    const value = item.value;
    if (typeof value === "string") { for (const text of quoted(value)) yield utf8.encode(text); continue; }
    if (value === null || typeof value === "boolean" || typeof value === "number") { yield utf8.encode(JSON.stringify(value)); continue; }
    if (typeof value !== "object") throw new TypeError("Context must contain JSON serializable values");
    if (value instanceof Date) { yield utf8.encode(JSON.stringify(value.toJSON())); continue; }
    if (ancestors.has(value)) throw new TypeError("Context contains a cycle");
    if (!Array.isArray(value) && Object.getPrototypeOf(value) !== Object.prototype && Object.getPrototypeOf(value) !== null) throw new TypeError("Context must contain JSON objects or arrays");
    ancestors.add(value);
    stack.push({ kind: "leave", value });
    if (Array.isArray(value)) { yield utf8.encode("["); stack.push({ kind: "array", value, index: 0 }); }
    else {
      const record = value as Record<string, unknown>;
      const keys = Object.keys(record).filter((key) => record[key] !== undefined);
      yield utf8.encode("{"); stack.push({ kind: "object", value: record, keys, index: 0 });
    }
  }
}

export class SnapshotCursor {
  readonly #snapshot: unknown;
  #iterator: Generator<Uint8Array>;
  #pending: Uint8Array = new Uint8Array(0);
  #hash = sha256.create();
  offset = 0;
  done = false;

  constructor(value: unknown) {
    this.#snapshot = structuredClone(value);
    this.#iterator = pieces(this.#snapshot);
  }

  read(credit: number): Uint8Array {
    if (!Number.isSafeInteger(credit) || credit <= 0) throw new RangeError("Invalid context credit");
    const output = new Uint8Array(Math.min(credit, 65536));
    let used = 0;
    while (used < output.length) {
      if (!this.#pending.length) {
        const next = this.#iterator.next();
        if (next.done) { this.done = true; break; }
        this.#pending = next.value;
      }
      const length = Math.min(output.length - used, this.#pending.length);
      output.set(this.#pending.subarray(0, length), used);
      this.#pending = this.#pending.subarray(length);
      used += length;
    }
    const bytes = used === output.length ? output : output.slice(0, used);
    this.#hash.update(bytes);
    this.offset += bytes.length;
    return bytes;
  }

  resume(offset: number): void {
    if (!Number.isSafeInteger(offset) || offset < 0) throw new RangeError("Invalid context offset");
    if (offset === this.offset) return;
    this.#iterator = pieces(this.#snapshot); this.#pending = new Uint8Array(0);
    this.#hash.destroy(); this.#hash = sha256.create(); this.offset = 0; this.done = false;
    while (this.offset < offset) {
      if (!this.read(Math.min(65536, offset - this.offset)).length) throw new RangeError("Context offset exceeds the immutable snapshot");
    }
  }

  digest(): string {
    if (!this.done) throw new Error("Context is not complete");
    const copy = this.#hash.clone();
    try { return hex(copy.digest()); } finally { copy.destroy(); }
  }
}
