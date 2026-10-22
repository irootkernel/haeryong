import test from "node:test";
import assert from "node:assert/strict";
import { ApiError } from "../../client/types.ts";
import type { SearchPage, SearchRequest } from "../../client/types.ts";
import { searchFixture } from "../../client/search-fixture.ts";
import { featureEnabled } from "./library-data.ts";
import { safeReturn } from "../routes.ts";
import { indexReasonText, LibrarySearchSession, matchingPageURL, searchFromURL, searchStatus, searchURL } from "./search-state.ts";
function deferred<T>() { let resolve!: (x: T) => void; let reject!: (x: unknown) => void; const promise = new Promise<T>((yes,no) => {resolve=yes;reject=no;}); return {promise,resolve,reject}; }
test("SC17 no/empty query and URL filters survive safe return, Back and Search All", () => {
  Object.defineProperty(globalThis,"location",{value:{origin:"http://fixture.invalid"},configurable:true});
  assert.equal(searchFromURL("/library"),null);
  const r: SearchRequest = {q:'검토 "readSource"',project_id:"A",lifecycle:"all",versions:"all",kind:"region"};
  const url=searchURL(r); assert.deepEqual(searchFromURL(url),r); assert.equal(safeReturn(url),url);
  assert.equal(searchFromURL(searchURL({q:""}))?.q,"");
  assert.equal(featureEnabled({search:true},"search"),true); assert.equal(featureEnabled({search:false},"search"),false);
});
test("SC17 separate empty/corpus/zero/partial/match states and safe owner/scoped reasons", () => {
  const page=searchFixture(); assert.match(searchStatus(page),/일부/);
  page.index.state="complete"; assert.match(searchStatus(page),/검색 결과 2/);
  page.total_matches="0"; assert.match(searchStatus(page),/일치하는 결과가 없습니다/);
  page.index.versions_total="0"; assert.match(searchStatus(page),/읽을 수 있는 콘텐츠가 없습니다/);
  page.query.normalized=""; assert.equal(searchStatus(page),"검색어를 입력하세요.");
  for(const scoped of [false,true]) {
    const texts=searchFixture(scoped).index.reasons.map(indexReasonText);
    assert.ok(texts.includes("콘텐츠 읽기 실패 · 1개 버전")); assert.equal(texts.includes("색인 저장 실패 · 1개 버전"),!scoped);
  }
});
test("SC18 matching page retains exact old version, once-encoded Unicode path and no region focus", () => {
  const hit=searchFixture().items[0]!; const url=new URL(matchingPageURL(hit),"http://fixture.invalid");
  assert.equal(url.pathname,"/documents/doc/versions/exact-old"); assert.equal(url.searchParams.get("page"),hit.page_path); assert.equal(url.hash,"");
});
test("SC17 replaced first/more responses and disposed remount cannot populate protected rows", async () => {
  const calls: ReturnType<typeof deferred<SearchPage>>[]=[];
  const session=new LibrarySearchSession({search:()=>{const d=deferred<SearchPage>();calls.push(d);return d.promise;}},()=>{});
  const old=session.start({q:"old"}); const current=session.start({q:"new"}); calls[1]!.resolve(searchFixture(true)); await current;
  calls[0]!.resolve(searchFixture()); await old; assert.equal(session.state.page?.index.versions_total,"1");
  const more=session.more(); const newer=session.start({q:"newer"}); calls[3]!.resolve(searchFixture(true)); await newer; calls[2]!.resolve(searchFixture());await more;
  assert.equal(session.state.page?.items.length,1); assert.equal(session.state.page?.index.versions_total,"1");
  const late=session.start({q:"late"}); session.dispose();calls[4]!.resolve(searchFixture());await late;assert.equal(session.state.page,null);
});
test("SC17 double more is single flight; binding change visibly resets first page and reasons", async () => {
  const calls: SearchRequest[]=[]; const next=deferred<SearchPage>();
  const session=new LibrarySearchSession({search:async r=>{calls.push(r);if(calls.length===2)return next.promise;return searchFixture(true);}},()=>{});
  await session.start({q:"needle",lifecycle:"all"});const more=session.more();await session.more(); assert.equal(calls.length,2);
  next.reject(new ApiError(400,"INVALID_REQUEST",false,0,{reason:"cursor_mismatch"}));await more;
  assert.equal(calls.length,3); assert.equal(calls[2]!.cursor,undefined);assert.equal(session.state.reset,true);assert.equal(session.state.page?.items.length,1);
  assert.deepEqual(session.state.page?.index.reasons,[{code:"read_failed",version_count:"1"}]);
});
for(const status of [401,403,404,503]) test(`SC17 more failure ${status} clears access-loss rows without false zero`, async()=>{
  let count=0;const session=new LibrarySearchSession({search:async()=>{if(++count===1)return searchFixture();throw new ApiError(status,"ERROR",true);}},()=>{});
  await session.start({q:"needle"});await session.more();assert.ok(session.state.error);assert.equal(session.state.page===null,status!==503);assert.equal(session.state.accessLost,status!==503);
});

test("N2 login return preserves only TAB/LF/CR search whitespace", () => {
  Object.defineProperty(globalThis,"location",{value:{origin:"http://fixture.invalid"},configurable:true});
  for (const whitespace of ["\t", "\n", "\r"]) {
    const target = "/library?" + new URLSearchParams({q:"검토" + whitespace + "승인",versions:"all"});
    const login = new URL("/connect?" + new URLSearchParams({return:target}), location.origin);
    assert.equal(safeReturn(login.searchParams.get("return")), target);
    assert.equal(searchFromURL(safeReturn(login.searchParams.get("return")))?.q, "검토" + whitespace + "승인");
    assert.equal(safeReturn("/library?" + new URLSearchParams({project_id:"A"+whitespace})), "/");
  }
  for (let code = 0; code <= 127; code++) {
    if ((code > 31 && code !== 127) || [9,10,13].includes(code)) continue;
    assert.equal(safeReturn("/library?" + new URLSearchParams({q:"검토"+String.fromCharCode(code)})), "/");
  }
});
