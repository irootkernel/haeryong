import {test} from "node:test";
import assert from "node:assert/strict";
import {HttpHaeryongClient} from "./http.ts";
import {NativeHaeryongClient,NATIVE_OPERATIONS,type NativePort} from "./native.ts";
import {ApiError,StaleResponseError} from "./types.ts";

test("Review HTTP preserves exact intent key condition and N3 review filter/cursor",async()=>{
 const original=globalThis.fetch;const calls:Array<[string,RequestInit|undefined]>=[];
 globalThis.fetch=(async(input,init)=>{calls.push([String(input),init]);return new Response(JSON.stringify({data:{version_id:"v/%2F",items:[],next_cursor:null},request_id:"r"}));}) as typeof fetch;
 try{
  const client=new HttpHaeryongClient();
  await client.reviewGet("v/%2F");await client.reviewPut("v/%2F",{action:"complete",state:"reviewed"},{ifMatch:'"opaque"'},"key");await client.reviewHistory("v/%2F","+/cursor");
  for(const review of ["reviewing","updated_since_review"] as const)await client.listDocuments({review,cursor:"next/+%"});
  assert.ok(calls[0]![0].endsWith("/versions/v%2F%252F/review"));
  assert.equal(new Headers(calls[1]![1]!.headers).get("If-Match"),'"opaque"');
  assert.equal(new Headers(calls[1]![1]!.headers).get("Idempotency-Key"),"key");
  assert.deepEqual(JSON.parse(calls[1]![1]!.body as string),{action:"complete",state:"reviewed"});
  for(const [url]of calls.slice(3)){const query=new URL(url,"http://local").searchParams;assert.ok(["reviewing","updated_since_review"].includes(query.get("review")!));assert.equal(query.get("cursor"),"next/+%");}
 }finally{globalThis.fetch=original;}
});
test("Review native typed requests and N3 filter next-page cursor parity",async()=>{
 const calls:Array<[string,unknown]>=[];const client=new NativeHaeryongClient({call:async(op:string,payload:unknown)=>{calls.push([op,payload]);return op==="reviewPut"?{kind:"committed",body:{version_id:"old"}}:{items:[],next_cursor:null};}} as unknown as NativePort);
 await client.reviewPut("old",{action:"complete",state:"reviewed"},{create:true},"same-key");
 for(const review of ["reviewing","updated_since_review"] as const)await client.listDocuments({review,cursor:"next/+%"});
 assert.deepEqual(calls[0],["reviewPut",{version_id:"old",body:{action:"complete",state:"reviewed"},precondition:{create:true},operation_id:"same-key"}]);
 assert.deepEqual(calls.slice(1).map(x=>x[1]),["reviewing","updated_since_review"].map(review=>({project_id:undefined,lifecycle:undefined,review,cursor:"next/+%"})));
 for(const op of ["reviewGet","reviewPut","reviewHistory"])assert.ok((NATIVE_OPERATIONS as readonly string[]).includes(op));
});
test("Review native stale authority suppresses delayed 412 responses",async()=>{
 let reject!:(error:unknown)=>void;const client=new NativeHaeryongClient({call:()=>new Promise((_,bad)=>{reject=bad;})} as unknown as NativePort);
 const pending=client.reviewPut("old",{action:"complete",state:"reviewed"},{create:true},"same");client.invalidate();reject(new ApiError(412,"PRECONDITION_FAILED",false));await assert.rejects(pending,StaleResponseError);
});
test("Review HTTP/native accepted receipt recovery keeps the original operation and does not redispatch",async()=>{
 const original=globalThis.fetch;
 try{
  const calls:string[]=[];
  globalThis.fetch=(async(input,init)=>{
   calls.push((init?.method??"GET")+" "+String(input));
   return new Response(JSON.stringify({data:init?.method==="PUT"?{operation_id:"key",state:"accepted",operation_url:"/api/v1/operations/key"}:{operation_id:"key",state:"completed",original_status:200,result_metadata:{version_id:"v3",operation_id:"key",value:{state:"reviewed"}}}}),{status:init?.method==="PUT"?202:200});
  }) as typeof fetch;
  const view=await new HttpHaeryongClient().reviewPut("v3",{action:"complete",state:"reviewed"},{create:true},"key");assert.equal(view.version_id,"v3");assert.equal(calls.length,2);assert.ok(calls[1]!.endsWith("/operations/key"));
  const nativeCalls:string[]=[];
  const native=new NativeHaeryongClient({call:async(op:string)=>{nativeCalls.push(op);return op==="reviewPut"?{kind:"accepted",body:{operation_id:"key",state:"accepted",operation_url:"/api/v1/operations/key"}}:{operation_id:"key",state:"completed",original_status:200,result_metadata:{version_id:"v3"}};}} as unknown as NativePort);
  assert.equal((await native.reviewPut("v3",{action:"complete",state:"reviewed"},{create:true},"key")).version_id,"v3");assert.deepEqual(nativeCalls,["reviewPut","operation"]);
 }finally{globalThis.fetch=original;}
});
