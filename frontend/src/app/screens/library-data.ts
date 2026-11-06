// Pure library-list logic shared by the Home and Library screens and their
// node tests: cursor-aware honest counting (a count is a total only after the
// cursor is fully exhausted, including pages whose filtered items are empty
// while a next cursor exists), filter round-tripping through registered query
// keys, and truthful no-data section states for Home.
import type { DocumentView } from "../../client/types.ts";

export interface LibraryFilter {
  projectId: string;
  lifecycle: "active" | "archived" | "all";
}

/** S03 default: the absent-parameter library view is the active lifecycle. */
export const DEFAULT_FILTER: LibraryFilter = { projectId: "", lifecycle: "active" };

/** Registered query keys only; history restores the exact filter context. */
export function filterToSearch(filter: LibraryFilter): string {
  const params = new URLSearchParams();
  if (filter.projectId) params.set("project_id", filter.projectId);
  // The client-side "all" state is carried explicitly so Back restores it;
  // the server filter parameter is simply omitted for it.
  if (filter.lifecycle !== "active") params.set("lifecycle", filter.lifecycle);
  const query = params.toString();
  return query ? `/library?${query}` : "/library";
}

export function filterFromSearch(search: string): LibraryFilter {
  const params = new URLSearchParams(search);
  const lifecycle = params.get("lifecycle");
  return {
    projectId: params.get("project_id") ?? "",
    lifecycle: lifecycle === "archived" || lifecycle === "all" ? lifecycle : "active",
  };
}

export interface DocumentAccumulation {
  documents: DocumentView[];
  /** A count is labelled total only after the cursor chain is exhausted. */
  exhausted: boolean;
}

export function accumulateDocuments(previous: DocumentAccumulation, page: { items: DocumentView[]; next_cursor: string | null }): DocumentAccumulation {
  const known = new Map(previous.documents.map((document) => [document.document_id, document]));
  for (const item of page.items) {
    const existing = known.get(item.document_id);
    if (!existing || item.updated_at >= existing.updated_at) known.set(item.document_id, item);
  }
  return {
    documents: [...known.values()].sort((a, b) => (a.updated_at < b.updated_at ? 1 : a.updated_at > b.updated_at ? -1 : a.document_id < b.document_id ? -1 : 1)),
    // An empty-items page with a next cursor is NOT exhaustion.
    exhausted: page.next_cursor === null,
  };
}

export type CountLabel = { loaded: number; total: false } | { loaded: number; total: true };

export function countLabel(accumulation: DocumentAccumulation): CountLabel {
  return { loaded: accumulation.documents.length, total: accumulation.exhausted };
}

/**
 * Home priority sections stay truthful no-data states while the review and
 * freshness APIs do not exist; updated_at ordering never implies review
 * priority or "needs attention".
 */
export interface HomeSections {
  continueReading: DocumentView[];
  updated: DocumentView[];
  revisit: DocumentView[];
  needsAttention: DocumentView[];
}

export function emptyHomeSections(): HomeSections {
  return { continueReading: [], updated: [], revisit: [], needsAttention: [] };
}

export function homeSectionsUnavailable(): { available: false; reason: string } {
  return {
    available: false,
    reason: "이 항목을 계산하는 서비스 API가 아직 제공되지 않습니다. 임의로 추정한 값을 표시하지 않습니다.",
  };
}

/** X05: an absent feature key is disabled; only an explicit true enables. */
export function featureEnabled(features: Record<string, boolean> | undefined, key: string): boolean {
  return features?.[key] === true;
}

export async function refreshLoadedDocumentPages(
  reader: {
    listDocuments(filter: {
      projectId?: string;
      lifecycle?: "active" | "archived";
      cursor?: string;
    }): Promise<{ items: DocumentView[]; next_cursor: string | null }>;
  },
  filter: LibraryFilter,
  pageCount: number,
  isStale?: () => boolean,
): Promise<{ accumulation: DocumentAccumulation; cursor: string | null; loadedPages: number } | null> {
  const targetPages = Math.max(1, pageCount);
  let accumulation: DocumentAccumulation = { documents: [], exhausted: false };
  let cursor: string | null = null;
  let loadedPages = 0;
  for (let i = 0; i < targetPages; i++) {
    const page = await reader.listDocuments({
      projectId: filter.projectId || undefined,
      lifecycle: filter.lifecycle === "all" ? undefined : filter.lifecycle,
      cursor: cursor ?? undefined,
    });
    if (isStale?.()) return null;
    accumulation = accumulateDocuments(accumulation, page);
    cursor = page.next_cursor;
    loadedPages += 1;
    if (cursor === null) break;
  }
  return { accumulation, cursor, loadedPages };
}
