import { test } from "node:test";
import assert from "node:assert/strict";
import { HttpHaeryongClient } from "./http.ts";
import { NativeHaeryongClient, NATIVE_OPERATIONS, type NativePort } from "./native.ts";
import { ApiError, StaleResponseError } from "./types.ts";
import { backlinkScope, referenceStateLabel } from "../app/screens/links-state.ts";

const rebuild = { version_id: "v1", kind: "references", job_ids: ["j1"], operation_id: "op1" };
test("BL named HTTP operations preserve raw scoped fields and completed 202 creation", async () => {
 const saved = globalThis.fetch, seen: [string, RequestInit | undefined][] = [];
 globalThis.fetch = (async (url, init) => { seen.push([String(url), init]); return new Response(JSON.stringify({data: rebuild}), {status: init?.method === "POST" ? 202 : 200}); }) as typeof fetch;
 try {
  const client = new HttpHaeryongClient();
  await client.versionBacklinks({versionId:"v1", pagePath:"한글/%2F +.html", regionId:"retry/+%", limit:0, cursor:"cursor/+%"});
  const url = new URL(seen[0]![0], "http://host.invalid");
  assert.deepEqual(Object.fromEntries(url.searchParams), {page_path:"한글/%2F +.html",region_id:"retry/+%",limit:"0",cursor:"cursor/+%"});
  await client.validateVersionIndexes({versionId:"v1",kind:"references"});
  assert.match(seen[1]![0], /\/indexes\/validate\?kind=references$/);
  assert.deepEqual(await client.rebuildVersionIndexes({versionId:"v1",kind:"references",operationId:"op1"}),rebuild);
  assert.equal(seen.length,3);assert.equal(seen[2]![1]?.body,JSON.stringify({kind:"references"}));
  assert.equal(new Headers(seen[2]![1]?.headers).get("Idempotency-Key"),"op1");
 }finally{globalThis.fetch=saved;}
});
test("BL native parity, closed calls, job-creation receipts and stale denial", async () => {
 const seen: [string, unknown][]=[];
 const native = new NativeHaeryongClient({call:async (name:string,payload:unknown)=>{seen.push([name,payload]);return name === "rebuildVersionIndexes" ? {kind:"accepted",body:rebuild} : {};}} as unknown as NativePort);
 for(const name of ["versionBacklinks","validateVersionIndexes","rebuildVersionIndexes"] as const)assert.ok(NATIVE_OPERATIONS.includes(name));
 await native.versionBacklinks({versionId:"v1",pagePath:"p",regionId:"r",cursor:"c",limit:7});
 assert.deepEqual(seen[0],["versionBacklinks",{version_id:"v1",page_path:"p",region_id:"r",cursor:"c",limit:7}]);
 await native.validateVersionIndexes({versionId:"v1",kind:"search"});
 assert.deepEqual(seen[1],["validateVersionIndexes",{version_id:"v1",kind:"search"}]);
 assert.deepEqual(await native.rebuildVersionIndexes({versionId:"v1",kind:"references",operationId:"op1"}),rebuild);
 assert.deepEqual(seen[2],["rebuildVersionIndexes",{version_id:"v1",kind:"references",operation_id:"op1"}]);
 let reject: (error: unknown)=>void=()=>{};
 const stale=new NativeHaeryongClient({call:()=>new Promise((_done,fail)=>{reject=fail;})} as unknown as NativePort);
 const request=stale.versionBacklinks({versionId:"v1"});stale.invalidate();reject(new ApiError(404,"NOT_FOUND",false));await assert.rejects(request,StaleResponseError);
 const receipts=new NativeHaeryongClient({call:async(name:string)=>name==="rebuildVersionIndexes" ? {kind:"accepted",body:{operation_id:"op1",state:"accepted"}} : {operation_id:"op1",state:"completed",original_status:202,result_metadata:rebuild}} as unknown as NativePort);
 assert.deepEqual(await receipts.rebuildVersionIndexes({versionId:"v1",kind:"references",operationId:"op1"}),rebuild);
});
test("BL scope and tombstone text neither invent anchors nor echo private reasons", () => {
 assert.deepEqual(backlinkScope("version","p","r"),{});
 assert.deepEqual(backlinkScope("page","p","r"),{pagePath:"p"});
 assert.deepEqual(backlinkScope("region","p","r"),{pagePath:"p",regionId:"r"});
 assert.deepEqual(backlinkScope("region",null,"r"),{});
 assert.equal(referenceStateLabel({resolution:"tombstoned",reason:"F10 hidden canary"}),"삭제된 대상");
 assert.equal(referenceStateLabel({resolution:"unavailable",reason:"F10 hidden canary"}).includes("F10"),false);
});
