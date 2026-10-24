import {test} from "node:test";
import assert from "node:assert/strict";
import {ApiError,type HaeryongClient,type ReviewView} from "../../client/types.ts";
import {ReviewOperations,reviewRequest,type ReviewIntent,type ReviewJournal} from "./review-operations.ts";
const absent:ReviewView={version_id:"v3",exists:false,value:{state:"unreviewed",reviewed_at:null},revision:null,etag:null,page_position_url:null,required_precondition:"if_none_match_star"};
function journal():ReviewJournal {let value:ReviewIntent|null=null;return{read:()=>value,async change(expected,next){assert.deepEqual(value,expected);value=structuredClone(next);}};}
const guard=()=>{};
test("Review is inert until explicit submit; lost acknowledgement replays exact key and condition",async()=>{
 const calls:unknown[][]=[];let fail=true;
 const client={reviewPut:async(...args:unknown[])=>{calls.push(args);if(fail)throw Error("lost acknowledgement");return {...absent,exists:true,value:{state:"reviewed",reviewed_at:"time"},revision:1,etag:'"new"'};}} as unknown as HaeryongClient;
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
