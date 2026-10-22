// Content-free hints only: every notification requires an authenticated GET.
const SEARCH_INVALIDATIONS = new Set(["search.index_updated", "version.published", "library.invalidated", "resource.invalidated", "resync.required", "policy.changed", "document.archived", "document.trashed", "document.restored", "document.purged"]);
export function searchInvalidated(type: string): boolean { return SEARCH_INVALIDATIONS.has(type); }
export function searchEventLines(notify: () => void): (text: string) => void {
  let pending = "";
  return text => {
    const lines = (pending + text).split("\n"); pending = lines.pop()!;
    for (const line of lines) if (line.startsWith("event:") && searchInvalidated(line.slice(6).trim())) notify();
  };
}
