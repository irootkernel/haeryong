import {test} from "node:test";
import assert from "node:assert/strict";
import {ApiError,type HaeryongClient,type ReviewView} from "../../client/types.ts";
import {ReviewOperations,reviewRequest,type ReviewIntent,type ReviewJournal} from "./review-operations.ts";
const absent:ReviewView={version_id:"v3",exists:false,value:{state:"unreviewed",reviewed_at:null},revision:null,etag:null,page_position_url:null,required_precondition:"if_none_match_star"};
function journal():ReviewJournal {let value:ReviewIntent|null=null;return{read:()=>value,async change(expected,next){assert.deepEqual(value,expected);value=structuredClone(next);}};}
const guard=()=>{};
test("Review is inert until explicit submit; lost acknowledgement replays exact key and condition",async()=>{
 const calls:unknown[][]=[];let fail=true;
 const saved:ReviewView={...absent,exists:true,value:{state:"reviewed",reviewed_at:"time"},revision:1,etag:'"new"'};
 const client={reviewPut:async(...args:unknown[])=>{calls.push(args);if(fail)throw Error("lost acknowledgement");return structuredClone(saved);},reviewGet:async()=>structuredClone(saved)} as unknown as HaeryongClient;
 const store=journal(),op=new ReviewOperations(client,"v3",store);assert.equal(calls.length,0);
 await assert.rejects(op.submit("complete",absent,guard),/lost acknowledgement/);assert.ok(op.pending);
 const resumed=new ReviewOperations(client,"v3",store);fail=false;await resumed.retry(guard);assert.deepEqual(calls[0],calls[1]);assert.equal(resumed.pending,null);
});
test("Review 412 retains explicit intent and requires current-state choice; no automatic new key",async()=>{
 let calls=0;const client={reviewPut:async()=>{calls++;throw new ApiError(412,"PRECONDITION_FAILED",false);}} as unknown as HaeryongClient;
 const op=new ReviewOperations(client,"v3",journal());await assert.rejects(op.submit("complete",absent,guard),ApiError);
 assert.equal(op.pending?.body.action,"complete");assert.equal(op.pending?.rejected,true);await assert.rejects(op.retry(guard));assert.equal(calls,1);await op.discard(guard);assert.equal(op.pending,null);
});
test("Review cannot retarget a pending action or dispatch after authority loss",async()=>{
 const store=journal();await store.change(null,{generation:"",version:"v3",key:"key",body:reviewRequest("complete"),condition:{create:true}});
 assert.throws(()=>new ReviewOperations({} as HaeryongClient,"v4",store),/버전/);
 const op=new ReviewOperations({reviewPut:()=>{throw Error("must not dispatch");}} as unknown as HaeryongClient,"v3",store);
 await assert.rejects(op.retry(()=>{throw Error("authority lost");}),/authority lost/);assert.equal(op.pending?.key,"key");
});

test("Review runtime change keeps the old request visible and requires explicit reconciliation",async()=>{
 const store=journal();await store.change(null,{generation:"before",version:"v3",key:"original",body:reviewRequest("complete"),condition:{create:true}});
 let calls=0;const op=new ReviewOperations({reviewPut:()=>{calls++;throw Error("must not dispatch");}} as unknown as HaeryongClient,"v3",store,"after");
 assert.equal(op.generationChanged,true);await assert.rejects(op.retry(guard));await assert.rejects(op.submit("complete",absent,guard));assert.equal(calls,0);assert.equal(op.pending?.key,"original");await op.discard(guard);assert.equal(op.pending,null);
});

test("Task04 Review receipt-first retry fetches fresh current state after completed receipt and applyRejected issues fresh K on current base", async () => {
  const store = journal();
  await store.change(null, {
    generation: "gen-1",
    version: "v3",
    key: "orig-key",
    body: reviewRequest("start"),
    condition: { create: true },
  });
  let puts = 0;
  // Server completed "start", then a peer advanced to "reviewed" at revision 2:
  let currentServer: ReviewView = {
    version_id: "v3",
    exists: true,
    value: { state: "reviewed", reviewed_at: "2026-11-06T00:00:00Z" },
    revision: 2,
    etag: '"rev-2"',
    page_position_url: null,
  };
  const client = {
    operation: async (id: string) => ({
      operation_id: id,
      state: "completed",
      original_status: 200,
      result_ref: null,
      job_id: null,
      result_metadata: {
        version_id: "v3",
        exists: true,
        value: { state: "reviewing", reviewed_at: null },
        revision: 1,
        etag: '"rev-1"',
      },
    }),
    reviewGet: async () => structuredClone(currentServer),
    reviewPut: async () => {
      puts++;
      throw new Error("should not call reviewPut when receipt is completed");
    },
  } as unknown as HaeryongClient;

  const op = new ReviewOperations(client, "v3", store, "gen-1");
  const resolved = await op.retry(guard);
  assert.equal(puts, 0);
  assert.equal(op.pending, null);
  // Fresh GET state ("reviewed", revision 2) is returned, not the stale historical receipt ("reviewing", revision 1):
  assert.equal(resolved.value.state, "reviewed");
  assert.equal(resolved.etag, '"rev-2"');

  // Now test failed receipt + applyRejected:
  await store.change(null, {
    generation: "gen-1",
    version: "v3",
    key: "failed-key",
    body: reviewRequest("start"),
    condition: { ifMatch: '"rev-1"' },
  });
  const putArgs: unknown[][] = [];
  const client2 = {
    operation: async (id: string) => ({
      operation_id: id,
      state: "failed",
      original_status: 412,
      result_ref: null,
      job_id: null,
      result_metadata: null,
      error: { code: "PRECONDITION_FAILED", message: "412 conflict" },
    }),
    reviewGet: async () => structuredClone(currentServer),
    reviewPut: async (...args: unknown[]) => {
      putArgs.push(args);
      currentServer = {
        version_id: "v3",
        exists: true,
        value: { state: "reviewing", reviewed_at: "2026-11-06T00:00:00Z" },
        revision: 3,
        etag: '"rev-3"',
        page_position_url: null,
      };
      return structuredClone(currentServer);
    },
  } as unknown as HaeryongClient;
  const op2 = new ReviewOperations(client2, "v3", store, "gen-1");
  await assert.rejects(op2.retry(guard), /412 conflict/);
  assert.equal(op2.pending?.rejected, true);
  // Explicit applyRejected against currentServer ("reviewed", '"rev-2"') uses "reopen" and fresh key:
  const applied = await op2.applyRejected(currentServer, guard);
  assert.equal(putArgs.length, 1);
  assert.deepEqual(putArgs[0]![1], { action: "reopen", state: "reviewing" });
  assert.deepEqual(putArgs[0]![2], { ifMatch: '"rev-2"' });
  assert.notEqual(putArgs[0]![3], "failed-key");
  assert.equal(applied.etag, '"rev-3"');
  assert.equal(op2.pending, null);
});

test("first Review dispatch reads current state when the transport returns a historical recovered result", async () => {
  const historical:ReviewView={...absent,exists:true,value:{state:"reviewing",reviewed_at:null},revision:1,etag:'"old"'};
  const current:ReviewView={...historical,value:{state:"reviewed",reviewed_at:"peer"},revision:2,etag:'"peer"'};
  const calls:string[]=[];
  const client={reviewPut:async()=>{calls.push("put");return historical;},reviewGet:async()=>{calls.push("get");return current;}} as unknown as HaeryongClient;
  const op=new ReviewOperations(client,"v3",journal(),"gen-1");
  assert.deepEqual(await op.submit("start",absent,guard),current);
  assert.deepEqual(calls,["put","get"]);
  assert.equal(op.pending,null);
});

test("acknowledged Review with unavailable current state clears only the historical pending request", async () => {
  const client={reviewPut:async()=>({...absent,exists:true,etag:'"old"'}),reviewGet:async()=>{throw Error("read offline");}} as unknown as HaeryongClient;
  const store=journal(),op=new ReviewOperations(client,"v3",store,"gen-1");
  await assert.rejects(op.submit("start",absent,guard),/원래 Review 요청은 완료되었으나 현재 서버 상태/);
  assert.equal(op.pending,null);assert.equal(store.read(),null);
});

test("Task04 G1 Review receipt recovery across changed runtime generation and 404 precondition check before same-K replay", async () => {
  let currentServer: ReviewView = {
    version_id: "v3",
    exists: true,
    value: { state: "reviewed", reviewed_at: "2026-11-06T00:00:00Z" },
    revision: 2,
    etag: '"rev-2"',
    page_position_url: null,
  };

  // 1. Changed runtime generation + completed receipt: retry(guard) queries operation(K), clears journal, and returns fresh reviewGet without PUT:
  {
    const store = journal();
    await store.change(null, {
      generation: "gen-old",
      version: "v3",
      key: "k-completed-across-gen",
      body: reviewRequest("start"),
      condition: { create: true },
    });
    const opCalls: string[] = [];
    const client = {
      operation: async (id: string) => {
        opCalls.push(id);
        return {
          operation_id: id,
          state: "completed",
          original_status: 200,
          result_ref: null,
          job_id: null,
          result_metadata: {
            version_id: "v3",
            exists: true,
            value: { state: "reviewing", reviewed_at: null },
            revision: 1,
            etag: '"rev-1"',
          },
        };
      },
      reviewGet: async () => structuredClone(currentServer),
      reviewPut: async () => {
        throw new Error("must not call reviewPut when receipt is completed");
      },
    } as unknown as HaeryongClient;
    const op = new ReviewOperations(client, "v3", store, "gen-new");
    assert.equal(op.generationChanged, true);
    // Before retry() checks the receipt, discard and applyRejected are blocked because outcome is still unknown:
    await assert.rejects(op.discard(guard), /확인되지 않은 요청/);
    await assert.rejects(op.applyRejected(currentServer, guard), /확인되지 않은 요청/);
    const view = await op.retry(guard);
    assert.deepEqual(opCalls, ["k-completed-across-gen"]);
    assert.equal(op.pending, null);
    assert.equal(store.read(), null);
    assert.equal(view.value.state, "reviewed");
    assert.equal(view.etag, '"rev-2"');
  }

  // 2. Changed runtime generation + pending/reconciling receipt: keeps pending unknown (rejected remains undefined) and blocks discard/applyRejected:
  {
    const store = journal();
    await store.change(null, {
      generation: "gen-old",
      version: "v3",
      key: "k-pending-across-gen",
      body: reviewRequest("complete"),
      condition: { ifMatch: '"rev-1"' },
    });
    const client = {
      operation: async (id: string) => ({
        operation_id: id,
        state: "reconciling",
        original_status: null,
        result_ref: null,
        job_id: null,
        result_metadata: null,
      }),
      reviewGet: async () => structuredClone(currentServer),
      reviewPut: async () => {
        throw new Error("must not call reviewPut while receipt is reconciling");
      },
    } as unknown as HaeryongClient;
    const op = new ReviewOperations(client, "v3", store, "gen-new");
    await assert.rejects(op.retry(guard), /아직 처리 중/);
    assert.equal(op.pending?.key, "k-pending-across-gen");
    assert.equal(op.pending?.rejected, undefined);
    await assert.rejects(op.discard(guard), /확인되지 않은 요청/);
    await assert.rejects(op.applyRejected(currentServer, guard), /확인되지 않은 요청/);
  }

  // 3. Changed runtime generation + 404 receipt: marks rejected=true without PUT and enables discard / applyRejected:
  {
    const store = journal();
    await store.change(null, {
      generation: "gen-old",
      version: "v3",
      key: "k-404-across-gen",
      body: reviewRequest("start"),
      condition: { ifMatch: '"rev-1"' },
    });
    let puts = 0;
    const client = {
      operation: async () => {
        throw new ApiError(404, "NOT_FOUND", false);
      },
      reviewGet: async () => structuredClone(currentServer),
      reviewPut: async () => {
        puts++;
        currentServer = {
          ...currentServer,
          value: { state: "reviewing", reviewed_at: "2026-11-06T00:00:00Z" },
          revision: 3,
          etag: '"rev-3"',
        };
        return structuredClone(currentServer);
      },
    } as unknown as HaeryongClient;
    const op = new ReviewOperations(client, "v3", store, "gen-new");
    await assert.rejects(op.retry(guard), /서비스 세대가 변경되어 이전 Review 요청을 자동 재전송하지 않습니다/);
    assert.equal(puts, 0);
    assert.equal(op.pending?.rejected, true);
    const applied = await op.applyRejected(currentServer, guard);
    assert.equal(puts, 1);
    assert.equal(applied.etag, '"rev-3"');
    assert.equal(op.pending, null);
  }

  currentServer = { ...currentServer, etag: '"rev-2"', revision: 2 };
  // 4. Unchanged generation + 404 receipt + moved ETag: rechecks reviewGet precondition first and marks rejected=true without replaying PUT:
  {
    const store = journal();
    await store.change(null, {
      generation: "gen-same",
      version: "v3",
      key: "k-404-same-gen-stale",
      body: reviewRequest("complete"),
      condition: { ifMatch: '"rev-1"' },
    });
    let puts = 0;
    const client = {
      operation: async () => {
        throw new ApiError(404, "NOT_FOUND", false);
      },
      reviewGet: async () => structuredClone(currentServer), // etag is '"rev-2"', not '"rev-1"'
      reviewPut: async () => {
        puts++;
        throw new Error("must not replay reviewPut when precondition no longer matches on 404 receipt");
      },
    } as unknown as HaeryongClient;
    const op = new ReviewOperations(client, "v3", store, "gen-same");
    await assert.rejects(op.retry(guard), /이전 Review 요청이 반영되기 전에 서버 상태가 변경되었습니다/);
    assert.equal(puts, 0);
    assert.equal(op.pending?.rejected, true);
  }
});
