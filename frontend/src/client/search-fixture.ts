// Labelled response fixtures: transport/presentation only; server eligibility is SC14.
import type { SearchPage } from "./types.ts";
export function searchFixture(scoped = false): SearchPage {
  return {
    query: { text: '검토 "readSource"', normalized: '검토 "readsource"', mode: "static" },
    filters: { project_id: "project-a", lifecycle: "all", versions: "all", kind: "region" },
    items: [{ kind: "region", project_id: "project-a", document_id: "doc", version_id: "exact-old", sequence: 1,
      title: "<img src=x onerror=alert(1)>", lifecycle: "archived", page_path: "nested/검토 %2F #?.html", region_id: "retry", source_id: null,
      anchor_state: "ambiguous", body_state: "complete", match_fields: ["body"], snippet: "<script>private text only</script>" }],
    next_cursor: "cursor/+%", total_matches: "2",
    index: { state: "partial", versions_total: scoped ? "1" : "2", versions_complete: "0", files_total: "3", files_discovered: "3", html_total: "3", html_complete: "1", html_pending: "0", html_failed: "2", unsupported_bodies: "0", discovery_complete: true, limitations: ["static_text_only"],
      reasons: scoped ? [{code: "read_failed", version_count: "1"}] : [{code: "read_failed", version_count: "1"}, {code: "storage_failed", version_count: "1"}] },
  };
}
