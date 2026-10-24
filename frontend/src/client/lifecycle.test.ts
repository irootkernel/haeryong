import {test} from "node:test";
import assert from "node:assert/strict";
import {HttpHaeryongClient} from "./http.ts";
import {NativeHaeryongClient,type NativePort,NATIVE_OPERATIONS} from "./native.ts";
import {ApiError} from "./types.ts";
import type {LifecycleCommitRequest} from "./types.ts";
import {reconcileLifecycle,updateLifecycleJournal} from "../app/screens/lifecycle-operations.ts";

test("Lifecycle HTTP preserves closed body key omission exact identity and page arguments",async()=>{
 const old=globalThis.fetch,calls:Array<[string,RequestInit|undefined]>=[];
 globalThis.fetch=(async(url,init)=>{calls.push([String(url),init]);return new Response(JSON.stringify({data:{items:[],next_cursor:null}}));}) as typeof fetch;
 try{const c=new HttpHaeryongClient();await c.lifecyclePlan("d/+",{operation:"trash",expected_revision:4});await c.lifecyclePlanGet("p/+",{cursor:"next/+",limit:1});await c.lifecyclePlanCommit("p/+",{plan_id:"p/+",expected_revision:4,confirmed:true});await c.lifecyclePlanCommit("p/+",{plan_id:"p/+",expected_revision:4,confirmed:true},"alias");await c.trashList({project_id:"project/+",cursor:"next",limit:2});
  assert.equal(calls[0]![0],"/api/v1/documents/d%2F%2B/lifecycle-plans");assert.equal(new Headers(calls[0]![1]?.headers).has("Idempotency-Key"),false);assert.equal(new Headers(calls[2]![1]?.headers).has("Idempotency-Key"),false);assert.equal(new Headers(calls[3]![1]?.headers).get("Idempotency-Key"),"alias");assert.deepEqual(JSON.parse(calls[2]![1]!.body as string),{plan_id:"p/+",expected_revision:4,confirmed:true});
  assert.equal(new URL(calls[1]![0],"http://local").searchParams.get("cursor"),"next/+");assert.equal(new URL(calls[4]![0],"http://local").searchParams.get("project_id"),"project/+");
 }finally{globalThis.fetch=old;}
});
test("Lifecycle transports preserve confirmation matrix status and code",async()=>{
 for(const [status,code]of [[400,"INVALID_REQUEST"],[409,"OPERATION_CONFLICT"],[409,"STALE_PLAN"]] as const){
  const old=globalThis.fetch;globalThis.fetch=(async()=>new Response(JSON.stringify({error:{code,retryable:false}}),{status})) as typeof fetch;
  try{const http=new HttpHaeryongClient(),native=new NativeHaeryongClient({call:async()=>{throw new ApiError(status,code,false);}} as unknown as NativePort);
   for(const client of [http,native])await assert.rejects(client.lifecyclePlanCommit("plan",{plan_id:"plan",expected_revision:2,confirmed:true}),e=>e instanceof ApiError&&e.status===status&&e.code===code);
  }finally{globalThis.fetch=old;}
 }
});
test("Lifecycle native named operations preserve invalid confirmation bytes and optional alias",async()=>{
 const calls:Array<[string,unknown]>=[];const client=new NativeHaeryongClient({call:async(op:string,payload:unknown)=>{calls.push([op,payload]);return {};}} as unknown as NativePort);
 for(const confirmed of [false,undefined,"true"]){const body={plan_id:"p",expected_revision:2,...(confirmed===undefined?{}:{confirmed})} as LifecycleCommitRequest;await client.lifecyclePlanCommit("p",body);assert.deepEqual(calls.at(-1),["lifecyclePlanCommit",{plan_id:"p",body}]);}
 await client.lifecyclePlan("d",{operation:"restore",expected_revision:2},"key");await client.lifecyclePlanGet("p",{cursor:"next",limit:1});await client.trashList({project_id:"project"});
 assert.deepEqual(calls.at(-1),["trashList",{request:{project_id:"project"}}]);for(const op of ["lifecyclePlan","lifecyclePlanGet","lifecyclePlanCommit","trashList"])assert.ok((NATIVE_OPERATIONS as readonly string[]).includes(op));
});
test("Lifecycle recovery reads the original plan receipt without redispatch even across generation changes",async()=>{
 const original={plan_id:"plan",expected_revision:4,confirmed:true as const};
 const recovery=new NativeHaeryongClient({call:async(op:string)=>{assert.equal(op,"operation");return {state:"completed",result_metadata:{plan_id:"plan",changed:true}};}} as unknown as NativePort);
 assert.equal((await reconcileLifecycle(recovery,{body:original,generation:"old",operation:"trash"})).plan_id,"plan");
});

test("Lifecycle late acknowledgement cannot erase another tab's pending intent",()=>{
 const values=new Map<string,string>();const storage={getItem:(key:string)=>values.get(key)??null,setItem:(key:string,value:string)=>{values.set(key,value);},removeItem:(key:string)=>{values.delete(key);}};
 const first={body:{plan_id:"first",expected_revision:2,confirmed:true as const},generation:"g",operation:"trash" as const};
 const second={...first,body:{...first.body,plan_id:"second"}};
 updateLifecycleJournal(storage,"doc",null,first);
 assert.throws(()=>updateLifecycleJournal(storage,"doc",null,second),/다른 창/);
 assert.throws(()=>updateLifecycleJournal(storage,"doc",second,null),/다른 창/);
 assert.equal(storage.getItem("doc"),JSON.stringify(first));updateLifecycleJournal(storage,"doc",first,null);assert.equal(storage.getItem("doc"),null);
});
