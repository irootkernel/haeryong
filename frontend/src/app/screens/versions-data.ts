import { sha256 } from "@noble/hashes/sha2.js";
import type { FileEntryView, HaeryongClient, ListPage, PackageStream, VersionView } from "../../client/types.ts";
import { ApiError } from "../../client/types.ts";
import { parseSuppliedMetadata, readStream, verifyMetadataBytes, type MetadataState } from "./evidence-view.ts";

export function comparePath(a: string, b: string): number {
  const encoder = new TextEncoder(), x = encoder.encode(a), y = encoder.encode(b);
  for (let i = 0; i < Math.min(x.length, y.length); i++) if (x[i] !== y[i]) return x[i]! - y[i]!;
  return x.length - y.length;
}
export interface FileDifference { path: string; change: "added" | "removed" | "changed" | "unchanged"; before: FileEntryView | null; after: FileEntryView | null }
interface CursorSide { items: FileEntryView[]; index: number; cursor: string | undefined; loaded: boolean }
const side = (): CursorSide => ({ items: [], index: 0, cursor: undefined, loaded: false });
const eof = (s: CursorSide) => s.loaded && s.cursor === undefined && s.index === s.items.length;
const needsPage = (s: CursorSide) => !eof(s) && s.index === s.items.length;
/** Bounded two-page merge. Return before crossing a page boundary when rows
 * exist, so a later slow/error page cannot hide earlier comparison results. */
export class FileComparison {
  readonly sides = [side(), side()];
  #live = true;
  #busy = false;
  private load: (side: number, cursor?: string) => Promise<ListPage<FileEntryView>>;
  constructor(load: (side: number, cursor?: string) => Promise<ListPage<FileEntryView>>) { this.load = load; }
  dispose(): void { this.#live = false; }
  get complete(): boolean { return this.sides.every(eof); }
  async next(): Promise<FileDifference[]> {
    if (!this.#live || this.#busy) return [];
    this.#busy = true;
    const rows: FileDifference[] = [];
    try {
      // At most one outstanding request per side. Failed requests retain the
      // exact cursor, while an independently successful page remains buffered.
      const pages = await Promise.allSettled(this.sides.map(async (s, i) => {
        if (!needsPage(s)) return;
        const page = await this.load(i, s.cursor);
        if (!this.#live) return;
        s.items = page.items; s.index = 0; s.cursor = page.next_cursor ?? undefined; s.loaded = true;
      }));
      if (!this.#live) return [];
      for (const result of pages) if (result.status === "rejected") throw result.reason;
      while (rows.length < 50 && !this.complete && !this.sides.some(needsPage)) {
        const a = this.sides[0]!, b = this.sides[1]!, before = a.items[a.index] ?? null, after = b.items[b.index] ?? null;
        const order = before === null ? 1 : after === null ? -1 : comparePath(before.path, after.path);
        if (order < 0) { rows.push({ path: before!.path, change: "removed", before, after: null }); a.index++; }
        else if (order > 0) { rows.push({ path: after!.path, change: "added", before: null, after }); b.index++; }
        else {
          const equal = before!.kind === after!.kind && before!.bytes_sha256 === after!.bytes_sha256 && before!.link_target === after!.link_target;
          rows.push({ path: before!.path, change: equal ? "unchanged" : "changed", before, after }); a.index++; b.index++;
        }
      }
      return rows;
    } finally { this.#busy = false; }
  }
}
/** Owned streams are cancelled immediately on pair/authority/unmount changes,
 * even if read() or the open request is still pending. */
export class ComparisonReads {
  #live = true;
  #cancel = new Set<() => void>();
  get live(): boolean { return this.#live; }
  own(stream: PackageStream): void {
    if (!this.#live) { stream.cancel(); throw Error("이전 비교입니다."); }
    this.#cancel.add(() => stream.cancel());
  }
  async text(client: HaeryongClient, version: string, entry: FileEntryView, notify: (text: string, complete: boolean) => void, more?: () => Promise<void>): Promise<void> {
    if (!client.fileStream) throw Error("텍스트 미리보기를 제공하지 않습니다.");
    const stream = await client.fileStream(version, entry.path); this.own(stream);
    const reader = stream.body.getReader(), cancel = () => { void reader.cancel().catch(() => undefined); };
    this.#cancel.add(cancel);
    const hash = sha256.create(), decoder = new TextDecoder("utf-8", { fatal: true });
    let size = 0, cleanEOF = false;
    try {
      while (this.#live) {
        const item = await reader.read(); if (!this.#live) return;
        if (item.done) { cleanEOF = true; break; }
        hash.update(item.value); size += item.value.length; notify(decoder.decode(item.value, { stream: true }), false);
        await more?.();
      }
      if (!this.#live) return;
      const digest = Array.from(hash.digest(), b => b.toString(16).padStart(2, "0")).join("");
      if (digest !== entry.bytes_sha256 || String(size) !== entry.byte_size) throw Error("파일 무결성 확인 실패");
      notify(decoder.decode(), true);
    } catch (error) {
      if (!cleanEOF) { cancel(); stream.cancel(); }
      throw error;
    } finally { this.#cancel.delete(cancel); reader.releaseLock(); }
  }
  dispose(): void { this.#live = false; for (const cancel of this.#cancel) cancel(); this.#cancel.clear(); }
}
export interface ComparisonMetadata { state: MetadataState; raw: Record<string, unknown> | null }
export async function loadComparisonMetadata(client: HaeryongClient, version: VersionView, reads: ComparisonReads): Promise<ComparisonMetadata> {
  try {
    const opened = await client.versionSuppliedMetadata(version.version_id); reads.own(opened.stream);
    const bytes = await readStream(opened.stream.body, () => !reads.live);
    const result = await verifyMetadataBytes(bytes, { etag: opened.etag, expectedSha256: version.supplied_metadata_sha256 });
    if (!reads.live) throw Error("이전 비교입니다.");
    if (!result.ok) throw Error("메타데이터 무결성 확인 실패");
    const raw = JSON.parse(new TextDecoder().decode(bytes)), parsed = parseSuppliedMetadata(raw);
    if (!parsed) throw Error("메타데이터 형식 오류");
    return { state: { kind: "ready", metadata: parsed }, raw };
  } catch (error) {
    return { state: error instanceof ApiError && error.status === 404 && error.details.reason === "metadata_not_supplied"
      ? { kind: "absent" } : { kind: "error", problem: error instanceof Error ? error.message : String(error) }, raw: null };
  }
}
export function defaultPair(versions: VersionView[], from: string | null, to: string | null, latest: string | null, lastReviewed: string | null = null): [string, string] | null {
  if (!latest) return null;
  const target = to ?? latest;
  const selected = versions.find(v => v.version_id === target);
  if (!selected) throw Error("대상 버전을 사용할 수 없습니다.");
  const preceding = versions.filter(v => v.sequence < selected.sequence).reduce<VersionView | null>((best, v) => best === null || v.sequence > best.sequence ? v : best, null);
  const source = from ?? (lastReviewed && versions.some(v => v.version_id === lastReviewed) ? lastReviewed : null) ?? preceding?.version_id ?? target;
  if (!versions.some(v => v.version_id === source)) throw Error("비교 버전을 사용할 수 없습니다.");
  return [source, target];
}
export function metadataDifferences(a: Record<string, unknown>, b: Record<string, unknown>): Array<{ field: string; before: string; after: string }> {
  const stable = (v: unknown): unknown => Array.isArray(v) ? v.map(stable) : v && typeof v === "object" ? Object.fromEntries(Object.entries(v).sort(([x], [y]) => comparePath(x, y)).map(([k, value]) => [k, stable(value)])) : v;
  return [...new Set([...Object.keys(a), ...Object.keys(b)])].sort(comparePath).flatMap(field => {
    const before = JSON.stringify(stable(a[field])) ?? "없음", after = JSON.stringify(stable(b[field])) ?? "없음";
    return before === after ? [] : [{ field, before, after }];
  });
}
