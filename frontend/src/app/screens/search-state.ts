import { ApiError, StaleResponseError } from "../../client/types.ts";
import type { HaeryongClient, IndexReasonCode, IndexReasonView, SearchHit, SearchPage, SearchRequest } from "../../client/types.ts";

export const INDEX_REASON_LABELS: Record<IndexReasonCode, string> = {
  not_indexed: "미색인 콘텐츠 포함", interrupted: "색인 작업 중단", cancelled: "색인 작업 취소됨",
  read_failed: "콘텐츠 읽기 실패", decode_failed: "텍스트 해석 실패", integrity_failed: "콘텐츠 무결성 확인 실패",
  resource_exhausted: "색인 자원 부족", storage_failed: "색인 저장 실패", unsupported_body: "일부 파일 본문 색인 미지원",
  failed_unknown: "색인 실패 · 원인 분류 불가",
};
export function indexReasonText(reason: IndexReasonView): string {
  return `${INDEX_REASON_LABELS[reason.code]} · ${reason.version_count}개 버전`;
}
export function searchFromURL(url: string): SearchRequest | null {
  const p = new URL(url, "http://library.invalid").searchParams;
  if (!p.has("q")) return null;
  const kind = p.get("kind");
  return { q: p.get("q")!, project_id: p.get("project_id") || undefined,
    lifecycle: p.get("lifecycle") === "all" ? "all" : p.get("lifecycle") === "archived" ? "archived" : "active",
    versions: p.get("versions") === "all" ? "all" : "latest",
    kind: kind && ["document", "page", "region", "source", "attachment"].includes(kind) ? kind as SearchRequest["kind"] : undefined };
}
export function searchURL(request: SearchRequest): string {
  const p = new URLSearchParams({ q: request.q, lifecycle: request.lifecycle ?? "active", versions: request.versions ?? "latest" });
  if (request.project_id) p.set("project_id", request.project_id);
  if (request.kind) p.set("kind", request.kind);
  return "/library?" + p.toString();
}
export function matchingPageURL(hit: SearchHit): string {
  const base = `/documents/${hit.document_id}/versions/${hit.version_id}`;
  return hit.kind === "attachment" || hit.page_path === null ? base : base + "?" + new URLSearchParams({ page: hit.page_path });
}
export type SearchViewState = {
  loading: boolean; page: SearchPage | null; error: string | null; reset: boolean; accessLost: boolean;
};
export function searchStatus(page: SearchPage): string {
  if (!page.query.normalized) return "검색어를 입력하세요.";
  if (page.index.versions_total === "0") return "선택한 필터에 읽을 수 있는 콘텐츠가 없습니다.";
  if (page.index.state !== "complete") return "색인이 진행 중이거나 일부 콘텐츠를 색인하지 못했습니다. 완료된 콘텐츠의 결과입니다.";
  return page.total_matches === "0" ? "색인된 텍스트에서 일치하는 결과가 없습니다." : `검색 결과 ${page.total_matches}개`;
}

// A single instance belongs to one confirmed authority epoch. Location changes
// supersede both first/more requests synchronously; dispose also fences remounts.
export class LibrarySearchSession {
  state: SearchViewState = { loading: false, page: null, error: null, reset: false, accessLost: false };
  #request = 0;
  #disposed = false;
  #filter: SearchRequest | null = null;
  #client: Pick<HaeryongClient, "search">;
  #changed: (state: SearchViewState) => void;
  constructor(client: Pick<HaeryongClient, "search">, changed: (state: SearchViewState) => void) { this.#client = client; this.#changed = changed; }
  #set(state: SearchViewState) { this.state = state; this.#changed(state); }
  dispose() { this.#disposed = true; this.#request++; }
  start(filter: SearchRequest, clientOverride?: Pick<HaeryongClient, "search">, options?: { rethrow?: boolean }) {
    this.#filter = { ...filter };
    return this.#load(null, false, clientOverride, Boolean(options?.rethrow));
  }
  more() {
    if (this.state.loading || !this.state.page?.next_cursor || this.state.accessLost) return Promise.resolve();
    return this.#load(this.state.page.next_cursor, false);
  }
  async #load(cursor: string | null, reset: boolean, clientOverride?: Pick<HaeryongClient, "search">, rethrow = false): Promise<void> {
    if (this.#disposed || !this.#filter) return;
    const request = ++this.#request;
    const previous = cursor === null ? null : this.state.page;
    this.#set({ loading: true, page: previous, error: null, reset, accessLost: false });
    const activeClient = clientOverride ?? this.#client;
    try {
      const page = await activeClient.search({ ...this.#filter, cursor: cursor ?? undefined });
      if (this.#disposed) return;
      if (request !== this.#request) { if (rethrow) throw new StaleResponseError(); return; }
      this.#set({ loading: false, page: { ...page, items: previous ? [...previous.items, ...page.items] : page.items }, error: null, reset, accessLost: false });
    } catch (cause) {
      if (this.#disposed) return;
      if (request !== this.#request) { if (rethrow) throw new StaleResponseError(); return; }
      if (cursor && cause instanceof ApiError && cause.status === 400 && cause.details.reason === "cursor_mismatch") {
        await this.#load(null, true, clientOverride, rethrow); return;
      }
      const accessLost = cause instanceof ApiError && [401, 403, 404].includes(cause.status);
      const error = cause instanceof ApiError ? `오류 ${cause.status} ${cause.code}` : "서비스에 연결할 수 없습니다. 다시 시도하세요.";
      this.#set({ loading: false, page: accessLost ? null : previous, error, reset, accessLost });
      if (rethrow) throw cause;
    }
  }
}
