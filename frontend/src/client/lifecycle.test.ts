import {test} from "node:test";
import type {JobView} from "./types.ts";
import {canDismissPurgeJob,dismissPurgeJob,lookupPurgeJobs,purgeJobsKey,purgeJobsNeedPoll,rememberedPurgeJobs,rememberPurgeJobId} from "../app/screens/purge-jobs.ts";
import assert from "node:assert/strict";
import {HttpHaeryongClient} from "./http.ts";
import {NativeHaeryongClient,type NativePort,NATIVE_OPERATIONS} from "./native.ts";
import {ApiError} from "./types.ts";
import type {LifecycleCommitRequest} from "./types.ts";
import {finishLifecycleJournal,lifecycleResultFromReceipt,lifecycleJournalKey,pendingLifecycleJournals,reconcileLifecycle,updateLifecycleJournal} from "../app/screens/lifecycle-operations.ts";

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

test("purge 202 identity backup placeholder and paused job are preserved by HTTP and native",async()=>{
 const result={operation_id:"plan",plan_id:"plan",document_id:"doc",lifecycle:"purged",previous_lifecycle:null,trashed_at:null,revision:4,changed:true,job_id:"job",job_url:"/api/v1/jobs/job"};
 const original=globalThis.fetch;
 globalThis.fetch=(async()=>new Response(JSON.stringify({data:result}),{status:202,headers:{"Content-Type":"application/json"}})) as typeof fetch;
 try{const http=new HttpHaeryongClient();assert.deepEqual(await http.lifecyclePlanCommit("plan",{plan_id:"plan",expected_revision:3,confirmed:true}),result);}finally{globalThis.fetch=original;}
 const calls:unknown[]=[];const native=new NativeHaeryongClient({call:async(operation:string,payload:unknown)=>{calls.push([operation,payload]);return result;}} as never);
 assert.deepEqual(await native.lifecyclePlanCommit("plan",{plan_id:"plan",expected_revision:3,confirmed:true}),result);
 await native.lifecyclePlan("doc",{operation:"purge",expected_revision:3});
 assert.deepEqual(calls.at(-1),["lifecyclePlan",{document_id:"doc",body:{operation:"purge",expected_revision:3}}]);
});

test("lost purge acceptance is discovered without a Trash row and its job is durable before clearing",async()=>{
 const values=new Map<string,string>();
 const storage={get length(){return values.size;},key:(n:number)=>[...values.keys()][n]??null,getItem:(k:string)=>values.get(k)??null,setItem:(k:string,v:string)=>{values.set(k,v);},removeItem:(k:string)=>{values.delete(k);}};
 const intent={body:{plan_id:"original",expected_revision:2,confirmed:true as const},generation:"old-era",operation:"purge" as const};
 const k=lifecycleJournalKey("library","owner","gone-document");updateLifecycleJournal(storage,k,null,intent);
 updateLifecycleJournal(storage,lifecycleJournalKey("other-library","owner","elsewhere"),null,intent);
 assert.equal(pendingLifecycleJournals(storage,"library","other-owner").length,0);
 const rows=pendingLifecycleJournals(storage,"library","owner");assert.equal(rows.length,1);assert.equal(rows[0]!.documentId,"gone-document");
 let gets=0;const result={plan_id:"original",document_id:"gone-document",lifecycle:"purged",job_id:"same-job"};
 const client=new NativeHaeryongClient({call:async(op:string,payload:unknown)=>{assert.equal(op,"operation");assert.deepEqual(payload,{operation_id:"original"});gets++;return {state:"completed",result_metadata:result};}} as never);
 const recovered=await reconcileLifecycle(client,rows[0]!.intent);assert.equal(gets,1);
 assert.throws(()=>finishLifecycleJournal(storage,k,intent,recovered,()=>{throw Error("quota");}),/quota/);assert.equal(storage.getItem(k),JSON.stringify(intent));
 finishLifecycleJournal(storage,k,intent,recovered,id=>{assert.equal(storage.getItem(k),JSON.stringify(intent));storage.setItem("jobs",id);});
 assert.equal(storage.getItem("jobs"),"same-job");assert.equal(storage.getItem(k),null);assert.ok(storage.getItem(lifecycleJournalKey("other-library","owner","elsewhere")));
});

const purgeJob=(id:string,state:JobView["state"]):JobView=>({job_id:id,state} as JobView);
function purgeStorage(){const values=new Map<string,string>();return {getItem:(k:string)=>values.get(k)??null,setItem:(k:string,v:string)=>{values.set(k,v);}};}
test("completed and failed purge results can be dismissed without losing new jobs or another owner/library",async()=>{
 const storage=purgeStorage(),key=purgeJobsKey("library","owner"),foreign=purgeJobsKey("other-library","owner"),otherOwner=purgeJobsKey("library","other-owner");
 for(const id of ["done","failed"])rememberPurgeJobId(storage,key,id);
 rememberPurgeJobId(storage,foreign,"done");rememberPurgeJobId(storage,otherOwner,"done");
 const rows=await lookupPurgeJobs({jobGet:async id=>purgeJob(id,id==="done"?"completed":"failed")},rememberedPurgeJobs(storage,key));
 assert.equal(purgeJobsNeedPoll(rows),false);
 rememberPurgeJobId(storage,key,"new-live");
 for(const row of rows){assert.equal(canDismissPurgeJob(row),true);dismissPurgeJob(storage,key,row);}
 assert.deepEqual(rememberedPurgeJobs(storage,key),["new-live"]);assert.deepEqual(rememberedPurgeJobs(storage,foreign),["done"]);assert.deepEqual(rememberedPurgeJobs(storage,otherOwner),["done"]);
});
test("a failed lookup stays beside a live purge and does not stop subsequent polling",async()=>{
 const client={jobGet:async(id:string)=>{if(id==="missing")throw new ApiError(404,"NOT_FOUND",false);if(id==="denied")throw new ApiError(403,"DENIED",false);if(id==="transient")throw new ApiError(503,"SERVICE_RECOVERING",true);return purgeJob(id,"running");}};
 const rows=await lookupPurgeJobs(client,["missing","live","denied","transient"]);
 assert.deepEqual(rows.map(row=>row.kind),["unavailable","job","unavailable","retry"]);assert.equal(purgeJobsNeedPoll(rows),true);
 assert.equal(canDismissPurgeJob(rows[0]!),true);assert.equal(canDismissPurgeJob(rows[1]!),false);assert.equal(canDismissPurgeJob(rows[3]!),false);
 const again=await lookupPurgeJobs({jobGet:async id=>purgeJob(id,"completed")},["live"]);assert.equal(again[0]!.kind,"job");assert.equal(purgeJobsNeedPoll(again),false);
});
test("pending and paused purge IDs persist across reloads and cannot be dismissed",async()=>{
 const storage=purgeStorage(),key=purgeJobsKey("library","owner");for(const id of ["pending","paused"])rememberPurgeJobId(storage,key,id);
 for(let reload=0;reload<2;reload++){
  const rows=await lookupPurgeJobs({jobGet:async id=>purgeJob(id,id==="pending"?"queued":"paused")},rememberedPurgeJobs(storage,key));
  assert.equal(purgeJobsNeedPoll(rows),true);for(const row of rows)assert.throws(()=>dismissPurgeJob(storage,key,row),/진행/);
  assert.deepEqual(rememberedPurgeJobs(storage,key),["pending","paused"]);
 }
});

test("known unfinished lifecycle receipts preserve intent without claiming a read failure or completion",()=>{
 const intent={body:{plan_id:"plan",expected_revision:4,confirmed:true as const},generation:"old",operation:"purge" as const};
 const receipt={operation_id:"plan",state:"pending",original_status:null,result_ref:null,job_id:null,result_metadata:null};
 for(const state of ["pending","reconciling","failed"])assert.equal(lifecycleResultFromReceipt(intent,{...receipt,state}),null);
 assert.throws(()=>lifecycleResultFromReceipt(intent,{...receipt,state:"completed",result_metadata:{plan_id:"other"}}),/일치/);
 const result={plan_id:"plan",document_id:"doc",lifecycle:"purged",job_id:"original-job"};
 assert.equal(lifecycleResultFromReceipt(intent,{...receipt,state:"completed",result_metadata:result}),result);
 assert.equal(intent.body.plan_id,"plan");
});
