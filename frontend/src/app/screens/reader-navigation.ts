import { ApiError } from "../../client/types.ts";
import type { HaeryongClient, ReferenceView } from "../../client/types.ts";
import { parseRoute, safeReturn, regionFromHash } from "../routes.ts";

export interface RegionIntent { regionId: string }
export function parseRegionIntent(hash: string): RegionIntent | null {
  const regionId = regionFromHash(hash);
  return regionId === null ? null : { regionId };
}
export interface ReaderPageContext {
  versionId: string; pagePath: string; context: string; reload: number; renderGeneration: string;
}
export function readerChannelReady(page: ReaderPageContext, route: Pick<ReaderPageContext, "versionId" | "context" | "reload">): boolean {
  // A child's ready callback follows its location report in the same task;
  // React may not have committed that new page URL yet. Keep the captured page,
  // then require its exact page at dispatch/focus time, without retagging it.
  return page.versionId === route.versionId && page.context === route.context && page.reload === route.reload;
}
export function sameReaderPage(a: ReaderPageContext, b: ReaderPageContext | null): boolean {
  return b !== null && Object.keys(a).every(key => a[key as keyof ReaderPageContext] === b[key as keyof ReaderPageContext]);
}
type RegionPage = Omit<ReaderPageContext, "renderGeneration">;
export class HostRegionFocus {
  #intent: (RegionPage & RegionIntent) | null = null;
  #sent: string | null = null;
  bind(page: RegionPage | null, regionId: string | null): void {
    const next = page && regionId !== null ? { ...page, regionId } : null;
    if (JSON.stringify(next) !== JSON.stringify(this.#intent)) this.#sent = null;
    this.#intent = next;
  }
  selection(): (RegionPage & RegionIntent) | null { return this.#intent; }
  flush(ready: ReaderPageContext | null, focus: (region: string) => void): boolean {
    const intent = this.#intent;
    if (!intent || !ready || intent.versionId !== ready.versionId || intent.pagePath !== ready.pagePath || intent.context !== ready.context || intent.reload !== ready.reload) return false;
    const key = JSON.stringify([intent, ready.renderGeneration]);
    if (key === this.#sent) return false;
    this.#sent = key; focus(intent.regionId); return true;
  }
}
export function regionFocusMatches(result: ReaderPageContext & { regionId: string }, page: ReaderPageContext | null, regionId: string | null): boolean {
  return result.regionId === regionId && sameReaderPage(eventIdentity(result), page);
}
export interface ReaderPosition { scroll_x: number; scroll_y: number }
export interface ReferenceActivation extends ReaderPageContext {
  referenceId: string; position: ReaderPosition; guard(): void;
}
const readerRoute = (url: string): boolean => safeReturn(url) === url && parseRoute(new URL(url, location.origin).pathname)?.screen === "reader";
export interface ReferringPage { owner: string; url: string; context: string; position: ReaderPosition }
export function referringPage(value: unknown, owner: string): ReferringPage | null {
  if (!value || typeof value !== "object") return null;
  const item = value as ReferringPage;
  return item.owner === owner && typeof item.url === "string" && readerRoute(item.url)
    && typeof item.context === "string" && item.context !== ""
    && item.position && Number.isFinite(item.position.scroll_x) && Number.isFinite(item.position.scroll_y)
    ? { owner, url: item.url, context: item.context, position: { scroll_x: item.position.scroll_x, scroll_y: item.position.scroll_y } } : null;
}
export function pushReference(url: string, owner: string, position: ReaderPosition): void {
  if (!readerRoute(url)) throw new Error("참조가 올바른 Reader 경로를 제공하지 않았습니다.");
  const source = referringPage({ owner, url: location.pathname + location.search + location.hash, context: history.state?.haeryongReaderContext, position }, owner);
  if (!source) throw new Error("참조 페이지의 복귀 경로를 확인하지 못했습니다.");
  history.replaceState({ ...history.state, haeryongReaderPosition: source }, "", source.url);
  // A nested click replaces the immediate return context, never serializes it.
  history.pushState({ haeryongReaderContext: crypto.randomUUID(), haeryongReferenceReturn: source }, "", url);
  window.dispatchEvent(new PopStateEvent("popstate"));
}
export function returnToReferringPage(owner: string): boolean {
  const source = referringPage(history.state?.haeryongReferenceReturn, owner);
  if (!source) return false;
  // Child navigations share session history, so the source need not be the
  // preceding entry. Recreate the saved host context through one explicit route.
  history.pushState({ haeryongReaderContext: source.context, haeryongReaderPosition: source }, "", source.url);
  window.dispatchEvent(new PopStateEvent("popstate"));
  return true;
}

/** One opt-in activation, scoped to the captured page and capability lifetime. */
export class ReaderReferences {
  #binding: string | null = null;
  bind(binding: string | null): void {
    if (binding !== this.#binding) this.#generation++;
    this.#binding = binding;
  }
  #enabled = false;
  #generation = 0;
  private options: {
    client: Pick<HaeryongClient, "versionReferences" | "resolveReference">;
    current(): ReaderPageContext | null; guard(): void;
    navigate(url: string, position: ReaderPosition): void; notice(message: string): void;
  };
  constructor(options: ReaderReferences["options"]) { this.options = options; }
  enable(enabled: boolean): void {
    if (enabled !== this.#enabled) { this.#enabled = enabled; this.#generation++; }
  }
  async request(event: ReferenceActivation): Promise<void> {
    if (!this.#enabled) return;
    const own = ++this.#generation;
    const guard = () => {
      this.options.guard(); event.guard();
      if (!this.#enabled || own !== this.#generation || !sameReaderPage(eventIdentity(event), this.options.current())) throw new Error("이전 참조 요청입니다.");
    };
    try {
      guard();
      let cursor: string | undefined, declared: ReferenceView | undefined;
      do {
        const page = await this.options.client.versionReferences({ versionId: event.versionId, cursor, limit: 200 });
        guard(); declared = page.items.find(item => item.id === event.referenceId);
        cursor = page.next_cursor ?? undefined;
      } while (!declared && cursor);
      // Authored paths may be symlinks or unique Unicode-equivalent spellings.
      // The bound resolver canonicalizes both endpoints and validates placement.
      if (!declared) {
        this.options.notice("이 페이지에 선언된 참조를 확인하지 못했습니다."); return;
      }
      const result = await this.options.client.resolveReference({ versionId: event.versionId, referenceId: event.referenceId, pagePath: event.pagePath, regionId: declared.from.region_id ?? undefined });
      guard();
      if (result.resolution !== "resolved" || !result.reader_url || !result.target || result.id !== event.referenceId) {
        this.options.notice("참조 대상을 열 수 없습니다. 현재 페이지는 계속 읽을 수 있습니다."); return;
      }
      const path = `/documents/${result.target.document_id}/versions/${result.target.version_id}`;
      if (!readerRoute(result.reader_url) || new URL(result.reader_url, location.origin).pathname !== path) throw new Error("참조 경로를 확인하지 못했습니다.");
      this.options.navigate(result.reader_url, event.position);
    } catch (error) {
      try { guard(); } catch { return; }
      this.options.notice(error instanceof ApiError && [404, 410].includes(error.status)
        ? "참조 대상이 없거나 접근할 수 없습니다. 현재 페이지는 계속 읽을 수 있습니다."
        : "참조를 확인하지 못했습니다. 현재 페이지는 계속 읽을 수 있습니다.");
    }
  }
}
export function eventIdentity(event: ReaderPageContext): ReaderPageContext {
  const { versionId, pagePath, context, reload, renderGeneration } = event;
  return { versionId, pagePath, context, reload, renderGeneration };
}
