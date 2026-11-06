import test from "node:test";
import assert from "node:assert/strict";
import { HttpHaeryongClient } from "./http.ts";
import { NativeHaeryongClient, NATIVE_OPERATIONS } from "./native.ts";
import type { NativePort } from "./native.ts";
import { ApiError, StaleResponseError } from "./types.ts";
import type { SearchRequest } from "./types.ts";
import { searchFixture } from "./search-fixture.ts";
const request: SearchRequest = { q: '검토 "source/path.go" %2F', project_id: "A", lifecycle: "all", versions: "all", kind: "region", cursor: "opaque/+%", limit: 17 };
for (const scoped of [false, true]) {
  test(`SC18 ${scoped ? "scoped" : "owner"} HTTP/native forwards original request and preserves safe reasons`, async () => {
    const original = globalThis.fetch;
    const page = searchFixture(scoped);
    globalThis.fetch = (async (input, init) => {
      const url = new URL(String(input), "http://fixture.invalid");
      assert.equal(url.pathname, "/api/v1/search"); assert.equal(init?.method, "GET"); assert.equal(init?.body, undefined);
      assert.deepEqual(Object.fromEntries(url.searchParams), Object.fromEntries(Object.entries(request).map(([k, v]) => [k, String(v)])));
      return Response.json({ data: page });
    }) as typeof fetch;
    try { assert.deepEqual(await new HttpHaeryongClient().search(request), page); } finally { globalThis.fetch = original; }
    const port = { call: async (name: string, payload: unknown) => { assert.equal(name, "search"); assert.deepEqual(payload, request); return JSON.parse(JSON.stringify(page)); } } as unknown as NativePort;
    const result = await new NativeHaeryongClient(port).search(request);
    assert.deepEqual(result, page); assert.equal(result.index.reasons[0]!.version_count, "1");
    assert.equal(result.index.reasons.some(r => r.code === "storage_failed"), !scoped);
    assert.ok(NATIVE_OPERATIONS.includes("search"));
  });
}
test("SC18 empty query remains q= and typed HTTP errors preserve cursor reason", async () => {
  const original = globalThis.fetch;
  globalThis.fetch = (async (input) => {
    assert.equal(new URL(String(input), "http://fixture.invalid").searchParams.get("q"), "");
    return Response.json({error:{code:"INVALID_REQUEST",retryable:false,details:{reason:"cursor_mismatch"}}},{status:400});
  }) as typeof fetch;
  try { await assert.rejects(new HttpHaeryongClient().search({q:""}), e => e instanceof ApiError && e.details.reason === "cursor_mismatch"); } finally { globalThis.fetch = original; }
});
for (const reject of [false, true]) test(`SC18 native search era discards late ${reject ? "failure" : "success"}`, async () => {
  let resolve!: (v: unknown) => void; let fail!: (e: unknown) => void;
  const pending = new Promise((yes, no) => { resolve = yes; fail = no; });
  const client = new NativeHaeryongClient({call: () => pending} as unknown as NativePort);
  const result = client.search(request); client.invalidate();
  if (reject) fail(new ApiError(401, "UNAUTHORIZED", false)); else resolve(searchFixture());
  await assert.rejects(result, StaleResponseError);
});

test("SC17 SSE split event names emit content-free search hints only", async () => {
  const { searchEventLines } = await import("./search-events.ts");
  let hints = 0; const feed = searchEventLines(() => hints++);
  feed(': heartbeat\n\nevent: search.index_'); assert.equal(hints, 0);
  feed('updated\ndata: {"private":"ignored"}\n\nevent: version.published\n\nevent: library.invalidated\n\n');
  assert.equal(hints, 3);
});

test("Task04 SSE incomplete frame after id never advances cursor or notifies before blank line", async () => {
  const { searchEventLines } = await import("./search-events.ts");
  let hints = 0;
  const cursors: string[] = [];
  const feed = searchEventLines(() => { hints++; }, { onCursor: (c) => cursors.push(c) });
  // Disconnect / partial chunk after id and event without terminating blank line:
  feed("id: epoch-1:41\nevent: review.changed\ndata: {}\n");
  assert.equal(hints, 0);
  assert.deepEqual(cursors, []);
  // Terminating blank line completes the frame and commits the cursor:
  feed("\n");
  assert.equal(hints, 1);
  assert.deepEqual(cursors, ["epoch-1:41"]);
});

test("Task04 SSE owner-record *.changed events and resync.required barrier ordering", async () => {
  const { searchEventLines } = await import("./search-events.ts");
  const events = [
    "review.changed",
    "annotation.changed",
    "revisit.changed",
    "reading_position.changed",
    "page_position.changed",
    "reader_preference.changed",
    "question.changed",
  ];
  let hints = 0;
  const cursors: string[] = [];
  const resyncs: unknown[] = [];
  let failResync = false;
  const feed = searchEventLines(
    () => { hints++; },
    {
      onCursor: (c) => cursors.push(c),
      onResync: async (r) => {
        resyncs.push(r);
        if (failResync) throw new Error("resync sweep failed");
      },
    },
  );
  for (let i = 0; i < events.length; i++) {
    await feed(`id: ep:${i + 1}\nevent: ${events[i]}\ndata: {}\n\n`);
  }
  assert.equal(hints, events.length);
  assert.equal(cursors.length, events.length);
  assert.equal(cursors.at(-1), `ep:${events.length}`);

  // Split resync.required frame across chunks:
  const p1 = feed('id: ep:100\nevent: resync.required\ndata: {"reason":"cursor_expired",');
  assert.equal( p1, undefined);
  assert.equal(cursors.at(-1), `ep:${events.length}`);
  await feed('"stream_epoch":"ep","head":"100","resume_cursor":"ep:100"}\n\n');
  assert.equal(resyncs.length, 1);
  assert.equal(cursors.at(-1), "ep:100");

  // Failed resync must reject and must not advance cursor for resync or trailing frame in same chunk:
  failResync = true;
  await assert.rejects(
    async () => {
      await feed(
        'id: ep:200\nevent: resync.required\ndata: {"reason":"gap","stream_epoch":"ep","head":"200","resume_cursor":"ep:200"}\n\n' +
        "id: ep:201\nevent: review.changed\ndata: {}\n\n",
      );
    },
    /resync sweep failed/,
  );
  assert.equal(cursors.at(-1), "ep:100");
});
test("SC17 HTTP event hints stop after credential era invalidation", async () => {
  const original=globalThis.fetch;let push!: ReadableStreamDefaultController<Uint8Array>;
  globalThis.fetch=(async()=>new Response(new ReadableStream({start(c){push=c;}}))) as typeof fetch;
  try {
    const client=new HttpHaeryongClient();const subscription=client.subscribeEvents();let hints=0;subscription.onInvalidation?.(()=>hints++);
    await new Promise(r=>setTimeout(r,0));push.enqueue(new TextEncoder().encode('event: search.index_updated\n\n'));
    await new Promise(r=>setTimeout(r,0));assert.equal(hints,1);client.invalidate();push.enqueue(new TextEncoder().encode('event: version.published\n\n'));
    await subscription.closed;assert.equal(hints,1);subscription.close();
  } finally {globalThis.fetch=original;}
});
test("SC18 native change hints preserve the era and disposal guard", async () => {
  let notify:()=>void=()=>{}; let removed=false;
  const client=new NativeHaeryongClient({call:async()=>({closed:new Promise(()=>{}),close(){},onInvalidation(listener:()=>void){notify=listener;return()=>{removed=true;};}})} as unknown as NativePort);
  const subscription=client.subscribeEvents();let hints=0;subscription.onInvalidation?.(()=>hints++);
  await new Promise(r=>setTimeout(r,0));notify();assert.equal(hints,1);client.invalidate();notify();assert.equal(hints,1);subscription.close();assert.equal(removed,true);notify();assert.equal(hints,1);
});
test("SC18 JSON event polls reify a boolean hint without resource bodies", async () => {
  const { createNativePort }=await import('./native-wire.ts');
  let release!: (value:unknown)=>void;
  const port=createNativePort({invoke:async name=>name==='subscribeEvents'?{ok:true,value:{subscription_id:'s'}}:name==='eventsPoll'?new Promise(r=>{release=r;}):{ok:true,value:null}});
  const subscription=await port.call('subscribeEvents',{});let hints=0;subscription.onInvalidation?.(()=>hints++);
  await new Promise(r=>setTimeout(r,0));release({ok:true,value:{closed:false,invalidated:true}});
  await new Promise(r=>setTimeout(r,0));assert.equal(hints,1);subscription.close();await subscription.closed;
});

test("SC18 explicit empty filters and zero limit retain HTTP/native error semantics", async () => {
  const request: SearchRequest = {q:"",project_id:"",cursor:"",limit:0};const original=globalThis.fetch;
  globalThis.fetch=(async input=>{const params=new URL(String(input),'http://fixture.invalid').searchParams;assert.deepEqual(Object.fromEntries(params),{q:"",project_id:"",cursor:"",limit:"0"});return Response.json({error:{code:'INVALID_REQUEST',retryable:false,details:{reason:'unknown_duplicate_or_empty_query'}}},{status:400});}) as typeof fetch;
  try {await assert.rejects(new HttpHaeryongClient().search(request),e=>e instanceof ApiError&&e.status===400);}finally{globalThis.fetch=original;}
  const client=new NativeHaeryongClient({call:async(name:string,payload:unknown)=>{assert.equal(name,'search');assert.deepEqual(payload,request);throw new ApiError(400,'INVALID_REQUEST',false);}} as unknown as NativePort);
  await assert.rejects(client.search(request),e=>e instanceof ApiError&&e.status===400);
});
