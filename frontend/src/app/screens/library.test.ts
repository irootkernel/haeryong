// Honest-counting and filter regressions for the library list, including
// empty-items pages that still carry a next cursor (never a total), and the
// absent-feature-key disabled rule for search/AI actions.
import assert from "node:assert/strict";
import test from "node:test";

import { accumulateDocuments, countLabel, featureEnabled, filterFromSearch, filterToSearch } from "./library-data.ts";
import type { DocumentView } from "../../client/types.ts";

function document(id: string, updatedAt: string): DocumentView {
  return {
    document_id: id,
    project_id: "p1",
    title: id,
    lifecycle: "active",
    latest_version_id: null,
    revision: 1,
    updated_at: updatedAt,
    versions_url: `/api/v1/documents/${id}/versions`,
    etag: id,
  };
}

test("a count becomes a total only after the cursor chain is exhausted", () => {
  let state = accumulateDocuments({ documents: [], exhausted: false }, { items: [document("a", "1")], next_cursor: "c1" });
  assert.deepEqual(countLabel(state), { loaded: 1, total: false });
  // An empty-items page that still has a next cursor is not exhaustion.
  state = accumulateDocuments(state, { items: [], next_cursor: "c2" });
  assert.deepEqual(countLabel(state), { loaded: 1, total: false });
  state = accumulateDocuments(state, { items: [document("b", "2")], next_cursor: null });
  assert.deepEqual(countLabel(state), { loaded: 2, total: true });
});

test("accumulation deduplicates documents by identity", () => {
  const state = accumulateDocuments({ documents: [], exhausted: false }, { items: [document("a", "2"), document("a", "1")], next_cursor: null });
  assert.equal(state.documents.length, 1);
  assert.equal(state.documents[0].updated_at, "2");
});

test("filters round-trip through registered query keys; absent lifecycle defaults to active", () => {
  assert.equal(filterToSearch({ projectId: "p1", lifecycle: "active" }), "/library?project_id=p1");
  assert.equal(filterToSearch({ projectId: "", lifecycle: "all" }), "/library?lifecycle=all");
  assert.equal(filterToSearch({ projectId: "", lifecycle: "active" }), "/library");
  assert.deepEqual(filterFromSearch("?project_id=p1&lifecycle=archived"), { projectId: "p1", lifecycle: "archived" });
  assert.deepEqual(filterFromSearch(""), { projectId: "", lifecycle: "active" });
  assert.deepEqual(filterFromSearch("?lifecycle=all"), { projectId: "", lifecycle: "all" });
  // Unknown lifecycle values fall back to the default active view.
  assert.deepEqual(filterFromSearch("?lifecycle=trash"), { projectId: "", lifecycle: "active" });
});

test("absent feature keys stay disabled; only explicit true enables", () => {
  assert.equal(featureEnabled(undefined, "search"), false);
  assert.equal(featureEnabled({}, "search"), false);
  assert.equal(featureEnabled({ search: false }, "search"), false);
  assert.equal(featureEnabled({ search: true }, "search"), true);
});
