import { test } from "node:test";
import assert from "node:assert/strict";
import { ReaderReferences, readerChannelReady, HostRegionFocus, parseRegionIntent, regionFocusMatches, pushReference, referringPage, returnToReferringPage, type ReaderPageContext, type ReferenceActivation } from "./reader-navigation.ts";
import { featureEnabled } from "./library-data.ts";
import { ApiError } from "../../client/types.ts";
import type { ReferenceView } from "../../client/types.ts";
import { ReaderLocationTracker } from "../../viewer/reader-location.ts";

const d = "11111111-1111-4111-8111-111111111111", v = "22222222-2222-4222-8222-222222222222";
const target = `/documents/${d}/versions/${v}?${new URLSearchParams({ page: "목표 %2F +.html", page_query: "q=%2F+ x", page_fragment: "%2F +" })}#region=${encodeURIComponent("대상/%2F +")}`;
const page: ReaderPageContext = { versionId: "source", pagePath: "source.html", context: "host-entry", reload: 0, renderGeneration: "channel-1" };
const view = (): ReferenceView => ({ id: "r", from: { page_path: page.pagePath, region_id: "from" }, relation: null, label: null, resolution: "resolved", reason: null, edge_id: "e", reader_url: target, target: { document_id: d, version_id: v, page_path: "목표 %2F +.html", region_id: "대상/%2F +", requested_path: null, resolved_path: "목표 %2F +.html", page_query: "q=%2F+ x", page_fragment: "%2F +" } });
test("RF11 repeated initial echoes retain opaque values only until a move; later return stays exact and reload resets", () => {
  const initial = { page: "설명/공백 %2F +.html", query: "q=%2F+ 공백/% 한글", fragment: "문서/%2F + 공백" };
  const browser = new URL("http://artifact.invalid/?" + initial.query + "#" + initial.fragment);
  const echo = { page: initial.page, query: browser.search.slice(1), fragment: browser.hash.slice(1) };
  assert.notDeepEqual(echo, initial);
  for (const moved of [{ ...echo, page: "sibling.html" }, { ...echo, query: "changed=%2F+" }, { ...echo, fragment: "changed/%2F +" }]) {
    const tracker = new ReaderLocationTracker(initial);
    assert.deepEqual(tracker.observe(echo), initial);
    assert.deepEqual(tracker.observe(echo), initial);
    assert.deepEqual(tracker.observe(moved), moved);
    assert.deepEqual(tracker.observe(echo), echo, "later return must retain reported spelling");
    assert.deepEqual(new ReaderLocationTracker(initial).observe(echo), initial, "reload starts fresh");
  }
});
const activation = (): ReferenceActivation => ({ ...page, referenceId: "r", position: { scroll_x: 3, scroll_y: 123 }, guard() {} });
function setup() {
  let current: ReaderPageContext | null = { ...page }, alive = true;
  const calls: unknown[] = [], navigations: string[] = [], notices: string[] = [];
  let result = view(), declared = view(), resolve: (() => Promise<ReferenceView>) | null = null;
  const flow = new ReaderReferences({ client: {
    async versionReferences(request) { calls.push(["list", request]); return { counts: { edge_count: "1", referenced_document_count: "1" }, items: [declared], next_cursor: null, materialization: { state: "ready", reason: null } }; },
    async resolveReference(request) { calls.push(["resolve", request]); return resolve ? resolve() : result; },
  }, current: () => current, guard: () => { if (!alive) throw Error("logout"); }, navigate(url) { navigations.push(url); }, notice(message) { notices.push(message); } });
  return { flow, calls, navigations, notices, enable(features?: Record<string, boolean>) { flow.enable(featureEnabled(features, "references")); }, move(next: ReaderPageContext | null) { current = next; }, logout() { alive = false; }, declared(value: ReferenceView) { declared = value; }, result(value: ReferenceView) { result = value; }, resolver(fn: () => Promise<ReferenceView>) { resolve = fn; } };
}
function environment() {
  const original = new Map<string, PropertyDescriptor | undefined>();
  const urls = [new URL(`/documents/${d}/versions/${v}?${new URLSearchParams({ page: "source.html", page_query: "q=%2F +", page_fragment: "%2F +" })}#region=${encodeURIComponent("source/%2F +")}`, "http://host.invalid")];
  const states: Record<string, unknown>[] = [{ haeryongReaderContext: "source-entry" }]; let index = 0;
  const hist = { get state() { return states[index]; }, replaceState(state: Record<string, unknown>, _unused: string, url: string) { states[index] = state; urls[index] = new URL(url, urls[index]); setLocation(); }, pushState(state: Record<string, unknown>, _unused: string, url: string) { index++; states.splice(index); urls.splice(index); states[index] = state; urls[index] = new URL(url, urls[index-1]); setLocation(); }, back() { if (index) { index--; setLocation(); } }, forward() { if (index + 1 < urls.length) { index++; setLocation(); } } };
  const setLocation = () => Object.defineProperty(globalThis, "location", { value: urls[index], configurable: true });
  for (const name of ["location", "history", "window", "PopStateEvent"]) original.set(name, Object.getOwnPropertyDescriptor(globalThis, name));
  setLocation(); Object.defineProperty(globalThis, "history", { value: hist, configurable: true }); Object.defineProperty(globalThis, "window", { value: { dispatchEvent() {} }, configurable: true }); Object.defineProperty(globalThis, "PopStateEvent", { value: Event, configurable: true });
  return { hist, urls, childMove(url: string) {
    // A child navigation contributes a joint session-history entry while the
    // host document remains live; Reader mirrors its new URL with replaceState.
    hist.pushState({ ...history.state }, "", location.href);
    hist.replaceState(history.state, "", url);
  }, close() { for (const [name, descriptor] of original) { if (descriptor) Object.defineProperty(globalThis, name, descriptor); else Reflect.deleteProperty(globalThis, name); } } };
}
test("RF09-G absent/false have zero calls; true has one bound resolver call and exact navigation (RV-FEATUREGATE)", async () => {
  const env = environment();
  try { for (const features of [undefined, {}, { references: false }, { references: true }] as (Record<string, boolean> | undefined)[]) {
    const f = setup(); f.enable(features); await f.flow.request(activation());
    const enabled = features?.references === true;
    assert.equal(f.calls.length, enabled ? 2 : 0); assert.deepEqual(f.navigations, enabled ? [target] : []);
    if (enabled) assert.deepEqual(f.calls[1], ["resolve", { versionId: page.versionId, referenceId: "r", pagePath: page.pagePath, regionId: "from" }]);
  } } finally { env.close(); }
});
test("RF09-G live true→false drops pending callbacks and makes no new calls, even after re-enable (RV-STALEFLAG)", async () => {
  const f = setup(); f.enable({ references: true }); let finish: (value: ReferenceView) => void = () => {};
  f.resolver(() => new Promise(resolve => { finish = resolve; }));
  const pending = f.flow.request(activation()); await Promise.resolve(); await Promise.resolve();
  assert.equal(f.calls.length, 2); f.enable({ references: false }); const afterLoss = f.flow.request(activation()); await Promise.resolve(); await Promise.resolve(); assert.equal(f.calls.length, 2); await afterLoss;
  f.enable({ references: true }); finish(view()); await pending; assert.deepEqual(f.navigations, []);
});
test("RF11 stale navigation, logout, reload and channel replacement discard in-flight resolution", async () => {
  for (const change of ["page", "version", "context", "reload", "channel", "logout"]) {
    const f = setup(); f.enable({ references: true }); let finish: (value: ReferenceView) => void = () => {};
    f.resolver(() => new Promise(resolve => { finish = resolve; })); const pending = f.flow.request(activation()); await Promise.resolve(); await Promise.resolve();
    if (change === "logout") f.logout(); else f.move({ ...page, ...(change === "page" ? { pagePath: "sibling.html" } : change === "version" ? { versionId: "new" } : change === "context" ? { context: "new" } : change === "reload" ? { reload: 1 } : { renderGeneration: "channel-2" }) });
    finish(view()); await pending; assert.deepEqual(f.navigations, [], change);
  }
});
test("RF11 fabricated IDs never resolve; unavailable/unresolved/gone/denied stay local", async () => {
  const env = environment();
  try {
    for (const bad of [{ ...view(), id: "other" }]) { const f = setup(); f.enable({ references: true }); f.declared(bad); await f.flow.request(activation()); assert.equal(f.calls.length, 1); assert.equal(f.notices.length, 1); }
    for (const status of ["unresolved", "unavailable", "tombstoned", 404, 410, 403] as const) {
      const f = setup(); f.enable({ references: true });
      if (typeof status === "number") f.resolver(async () => { throw new ApiError(status, "ERROR", false); }); else f.result({ ...view(), resolution: status, reader_url: null });
      await f.flow.request(activation()); assert.equal(f.navigations.length, 0); assert.equal(f.notices.length, 1);
    }
  } finally { env.close(); }
});
test("RF11 only a validated canonical target Reader route can navigate", async () => {
  const env = environment(); try { for (const url of ["https://evil.invalid/", "//evil.invalid/", "/settings", `/documents/${d}/versions/${v}?token=secret`, `/documents/${v}/versions/${d}`]) { const f = setup(); f.enable({ references: true }); f.result({ ...view(), reader_url: url }); await f.flow.request(activation()); assert.equal(f.navigations.length, 0); } } finally { env.close(); }
});
test("RF10 initial/hash-only focus waits for the exact ready page; sibling same ID, reload and stale acks remain bound", () => {
  const focus = new HostRegionFocus(), sent: string[] = [];
  const parsed = parseRegionIntent("#region=" + encodeURIComponent("영역/%2F +")); assert.deepEqual(parsed, { regionId: "영역/%2F +" });
  focus.bind(page, parsed!.regionId); assert.equal(focus.selection()!.pagePath, "source.html"); assert.equal(focus.flush(null, id => sent.push(id)), false);
  assert.equal(focus.flush({ ...page, pagePath: "sibling.html" }, id => sent.push(id)), false);
  assert.equal(focus.flush(page, id => sent.push(id)), true); assert.equal(focus.flush(page, id => sent.push(id)), false);
  focus.bind(page, "changed"); assert.equal(focus.flush(page, id => sent.push(id)), true);
  const sibling = { ...page, pagePath: "sibling.html", renderGeneration: "channel-2" }; focus.bind(sibling, "changed");
  assert.equal(focus.flush(page, id => sent.push(id)), false); assert.equal(focus.flush(sibling, id => sent.push(id)), true);
  assert.equal(regionFocusMatches({ ...page, regionId: "changed" }, sibling, "changed"), false);
  assert.equal(regionFocusMatches({ ...sibling, reload: 0, regionId: "changed" }, { ...sibling, reload: 1 }, "changed"), false);
  assert.deepEqual(sent, ["영역/%2F +", "changed", "changed"]);
});
test("RF10 no-region page clears old intent; failed anchor keeps an honest selected region", () => {
  const focus = new HostRegionFocus(); focus.bind(page, "missing"); focus.flush(page, () => {});
  const failed = { ...page, regionId: "missing", found: false, focused: false };
  assert.equal(regionFocusMatches(failed, page, focus.selection()!.regionId), true); assert.equal(failed.found, false);
  focus.bind({ ...page, pagePath: "no-region.html" }, null); assert.equal(focus.selection(), null); assert.equal(focus.flush(page, () => assert.fail("old region focused")), false);
  assert.equal(parseRegionIntent(""), null); assert.equal(parseRegionIntent("#region=a&region=b"), null);
});
test("RF11 push/return/Back/Forward preserve encoding and source position; nested clicks replace the return context", () => {
  const env = environment(), owner = "library/principal/runtime";
  try {
    const source = location.pathname + location.search + location.hash, position = { scroll_x: 7, scroll_y: 456 };
    pushReference(target, owner, position); assert.equal(env.urls.length, 2);
    assert.deepEqual(referringPage(history.state.haeryongReferenceReturn, owner), { url: source, owner, context: "source-entry", position });
    assert.equal(referringPage(history.state.haeryongReferenceReturn, "other-principal"), null);
    assert.equal(referringPage(history.state.haeryongReferenceReturn, "other-runtime"), null);
    assert.equal(new URL(location.href).searchParams.get("page_fragment"), "%2F +"); assert.equal(parseRegionIntent(location.hash)!.regionId, "대상/%2F +");
    env.hist.back(); assert.equal(location.pathname + location.search + location.hash, source); assert.deepEqual(history.state.haeryongReaderPosition.position, position);
    env.hist.forward(); assert.equal(location.pathname + location.search + location.hash, target);
    assert.equal(returnToReferringPage(owner), true); assert.equal(location.pathname + location.search + location.hash, source);
    assert.equal(history.state.haeryongReaderContext, "source-entry"); assert.deepEqual(history.state.haeryongReaderPosition.position, position);
    env.hist.back(); assert.equal(location.pathname + location.search + location.hash, target);
    env.hist.forward(); assert.equal(location.pathname + location.search + location.hash, source);
    env.hist.back();
    const targetContext = history.state.haeryongReaderContext;
    const nested = target.replace("page=", "page=nested-"); assert.notEqual(nested, target);
    pushReference(nested, owner, { scroll_x: 0, scroll_y: 99 });
    assert.equal(history.state.haeryongReferenceReturn.url, target); assert.equal(history.state.haeryongReferenceReturn.context, targetContext);
    assert.equal("haeryongReferenceReturn" in history.state.haeryongReferenceReturn, false);
    assert.equal(returnToReferringPage(owner), true); assert.equal(location.pathname + location.search + location.hash, target);
    assert.equal(history.state.haeryongReaderContext, targetContext); assert.deepEqual(history.state.haeryongReaderPosition.position, { scroll_x: 0, scroll_y: 99 });
    env.hist.back(); assert.equal(location.pathname + location.search + location.hash, nested);
    env.hist.forward(); assert.equal(location.pathname + location.search + location.hash, target);
    assert.equal(referringPage({ owner, url: "//evil.invalid", context: "source-entry", position }, owner), null);
    assert.equal(referringPage({ owner, url: source, position }, owner), null);
  } finally { env.close(); }
});


test("RF11 child page ready before the host URL commit retains its own identity for the next reference", async () => {
  const env = environment();
  try {
    const sibling = { ...page, pagePath: "sibling.html", renderGeneration: "channel-2" };
    assert.equal(readerChannelReady(sibling, page), true);
    assert.equal(readerChannelReady(sibling, { ...page, context: "new-entry" }), false);
    const f = setup(); f.enable({ references: true }); f.move(sibling);
    f.declared({ ...view(), from: { page_path: sibling.pagePath, region_id: null } });
    await f.flow.request({ ...activation(), ...sibling });
    assert.deepEqual(f.navigations, [target]);
    assert.deepEqual(f.calls[1], ["resolve", { versionId: sibling.versionId, referenceId: "r", pagePath: "sibling.html", regionId: undefined }]);
  } finally { env.close(); }
});


test("M4a-1 bound resolver accepts source aliases and unique Unicode-equivalent declarations (RV-SOURCEALIAS)", async () => {
  const env = environment();
  try {
    for (const [authored, canonical] of [["alias.html", "real.html"], ["cafe\u0301.html", "café.html"]]) {
      assert.notEqual(authored, canonical);
      const f = setup(), captured = { ...page, pagePath: canonical! };
      f.enable({ references: true }); f.move(captured);
      f.declared({ ...view(), from: { page_path: authored!, region_id: "from" } });
      f.result({ ...view(), from: { page_path: authored!, region_id: "from" } });
      await f.flow.request({ ...activation(), ...captured });
      assert.deepEqual(f.calls[1], ["resolve", { versionId: captured.versionId, referenceId: "r", pagePath: canonical, regionId: "from" }], authored);
      assert.deepEqual(f.navigations, [target], authored);
    }
  } finally { env.close(); }
});
test("M4a-1 a real sibling is rejected by bound source_context_mismatch without navigation", async () => {
  const env = environment();
  try {
    const f = setup(); f.enable({ references: true });
    f.declared({ ...view(), from: { page_path: "sibling.html", region_id: "from" } });
    f.result({ ...view(), from: { page_path: "sibling.html", region_id: "from" }, resolution: "unresolved", reason: "source_context_mismatch", reader_url: null, target: null, edge_id: null, label: null });
    await f.flow.request(activation());
    assert.deepEqual(f.calls[1], ["resolve", { versionId: page.versionId, referenceId: "r", pagePath: page.pagePath, regionId: "from" }]);
    assert.deepEqual(f.navigations, []); assert.equal(f.notices.length, 1);
  } finally { env.close(); }
});
test("M4a-2 return restores saved A after ordinary target child navigation and hash history (RV-RETURNHISTORY)", () => {
  const env = environment(), owner = "library/principal/runtime";
  try {
    const source = location.pathname + location.search + location.hash, position = { scroll_x: 11, scroll_y: 789 };
    pushReference(target, owner, position); assert.equal(env.urls.length, 2, "one target entry");
    const moved = new URL(target, location.origin); moved.searchParams.set("page", "ordinary-target.html"); moved.hash = "";
    const movedURL = moved.pathname + moved.search;
    env.childMove(movedURL); env.childMove(movedURL + "#region=target-scroll");
    assert.equal(env.urls.length, 4);
    assert.equal(returnToReferringPage(owner), true);
    assert.equal(location.pathname + location.search + location.hash, source, "return must reach saved A, not a preceding target child entry");
    assert.equal(history.state.haeryongReaderContext, "source-entry");
    assert.deepEqual(history.state.haeryongReaderPosition, { owner, url: source, context: "source-entry", position });
    env.hist.back(); assert.equal(location.pathname + location.search + location.hash, movedURL + "#region=target-scroll");
    env.hist.forward(); assert.equal(location.pathname + location.search + location.hash, source);
  } finally { env.close(); }
});
