import { test } from "node:test";
import assert from "node:assert/strict";
import { HttpHaeryongClient } from "./http.ts";
import { NativeHaeryongClient, NATIVE_OPERATIONS, type NativePort } from "./native.ts";
import { ApiError, StaleResponseError } from "./types.ts";

const request = { versionId: "v1", referenceId: "ref/%2F + 한글?#", pagePath: "nested/%2F + 한글.html", regionId: "영역/%2F +" };
test("RF09 HTTP reference operations encode raw IDs and navigation exactly once, preserving supplied empty/zero controls", async () => {
  const original = globalThis.fetch, observed: URL[] = [];
  globalThis.fetch = (async input => {
    observed.push(new URL(String(input), "http://host.invalid"));
    return new Response(JSON.stringify({ data: { items: [], next_cursor: null, materialization: { state: "ready", reason: null } } }));
  }) as typeof fetch;
  try {
    const client = new HttpHaeryongClient();
    await client.versionReferences({ versionId: "v1", cursor: "opaque/+%", limit: 0 });
    assert.equal(observed[0]!.pathname, "/api/v1/versions/v1/references");
    assert.deepEqual(Object.fromEntries(observed[0]!.searchParams), { cursor: "opaque/+%", limit: "0" });
    await client.resolveReference(request);
    assert.equal(observed[1]!.pathname, "/api/v1/versions/v1/references/resolve");
    assert.deepEqual(Object.fromEntries(observed[1]!.searchParams), { reference_id: request.referenceId, page_path: request.pagePath, region_id: request.regionId });
    await client.resolveReference({ versionId: "v1", referenceId: "", pagePath: "", regionId: "" });
    assert.deepEqual(Object.fromEntries(observed[2]!.searchParams), { reference_id: "", page_path: "", region_id: "" });
  } finally { globalThis.fetch = original; }
});
test("RF09 both named native operations forward closed fields and discard stale successes and errors", async () => {
  const calls: unknown[][] = []; let finish: (value: unknown) => void = () => {};
  const client = new NativeHaeryongClient({ call: async (name: string, payload: unknown) => { calls.push([name, payload]); return new Promise(resolve => { finish = resolve; }); } } as unknown as NativePort);
  assert.ok(NATIVE_OPERATIONS.includes("versionReferences")); assert.ok(NATIVE_OPERATIONS.includes("resolveReference"));
  const list = client.versionReferences({ versionId: "v1", cursor: "c/+%", limit: 17 });
  assert.deepEqual(calls[0], ["versionReferences", { version_id: "v1", cursor: "c/+%", limit: 17 }]);
  client.invalidate(); finish({}); await assert.rejects(list, StaleResponseError);
  const resolve = client.resolveReference(request);
  assert.deepEqual(calls[1], ["resolveReference", { version_id: "v1", reference_id: request.referenceId, page_path: request.pagePath, region_id: request.regionId }]);
  client.invalidate(); finish({}); await assert.rejects(resolve, StaleResponseError);
  for (const operation of ["versionReferences", "resolveReference"] as const) {
    let reject: (error: unknown) => void = () => {};
    const errors = new NativeHaeryongClient({ call: () => new Promise((_resolve, fail) => { reject = fail; }) } as unknown as NativePort);
    const pending = operation === "versionReferences" ? errors.versionReferences({ versionId: "v1" }) : errors.resolveReference(request);
    errors.invalidate(); reject(new ApiError(410, "TARGET_GONE", false)); await assert.rejects(pending, StaleResponseError);
  }
});
test("RF09 HTTP/native 404 and 410 preserve non-disclosing and minimal gone details", async () => {
  const original = globalThis.fetch;
  try {
    for (const status of [404, 410]) {
      const details = status === 410 ? { document_id: "d", version_id: "v" } : {};
      globalThis.fetch = (async () => new Response(JSON.stringify({ error: { code: status === 410 ? "TARGET_GONE" : "NOT_FOUND", retryable: false, details } }), { status })) as typeof fetch;
      const native = new NativeHaeryongClient({ call: async () => { throw new ApiError(status, status === 410 ? "TARGET_GONE" : "NOT_FOUND", false, 0, details); } } as unknown as NativePort);
      for (const client of [new HttpHaeryongClient(), native]) await assert.rejects(client.resolveReference(request), error => error instanceof ApiError && error.status === status && JSON.stringify(error.details) === JSON.stringify(details));
    }
  } finally { globalThis.fetch = original; }
});

test("GCLIENT05 prior explanation selection pins the exact selected old page through file cursor pages without looking up latest",async()=>{
 const {pinGenerationContext}=await import("../app/screens/generation-context.ts");
 const calls:unknown[]=[];const hash="a".repeat(64);
 const fake={versionFiles:async(version:string,cursor?:string)=>{calls.push([version,cursor]);return cursor?{items:[{kind:"file",path:"nested/old.html",bytes_sha256:hash}],next_cursor:null}:{items:[{kind:"directory",path:"nested"}],next_cursor:"opaque"};},documentDetail:()=>{throw new Error("latest lookup forbidden");}};
 const hit={kind:"page",version_id:"selected-old",page_path:"nested/old.html",region_id:null} as import("./types.ts").SearchHit;
 assert.deepEqual(await pinGenerationContext(fake as unknown as import("./types.ts").HaeryongClient,hit),{version_id:"selected-old",page_path:"nested/old.html",region_id:null,bytes_sha256:hash});
 assert.deepEqual(calls,[["selected-old",undefined],["selected-old","opaque"]]);
 await assert.rejects(pinGenerationContext(fake as unknown as import("./types.ts").HaeryongClient,{...hit,page_path:"missing.html"}),/버전에서 페이지/);
});

test("GCLIENT07 async Library/project defaults fill an untouched generation form while explicit project/mode and frozen pending request stay unchanged",async()=>{
 const {initialGenerationProject,generationSourceMode}=await import("../app/screens/generation-context.ts");
 const projects=[{project_id:"first"},{project_id:"library-selected"}];
 assert.equal(initialGenerationProject("",false,"library-selected",projects),"library-selected");
 assert.equal(initialGenerationProject("",false,"",[]),"");
 assert.equal(initialGenerationProject("",false,"",projects),"first");
 assert.equal(initialGenerationProject("explicit",true,"library-selected",projects),"explicit");
 const frozen={project_id:"original",source_selection:{source_mode:"pinned",selections:[]}};
 const before=JSON.stringify(frozen);
 assert.equal(generationSourceMode("p",{},{}),null);
 assert.equal(generationSourceMode("p",{},{p:"pinned"}),"pinned");
 assert.equal(generationSourceMode("p",{p:"current"},{p:"pinned"}),"current");
 assert.equal(generationSourceMode("other",{p:"current"},{other:"pinned"}),"pinned");
 assert.equal(JSON.stringify(frozen),before);
});


test("GCLIENT12 retained source chooser HTTP and named native encode project/source/cursor exactly and reject stale responses",async()=>{
 const original=globalThis.fetch;const calls:URL[]=[];
 globalThis.fetch=(async input=>{calls.push(new URL(String(input),"http://fixture.invalid"));return new Response(JSON.stringify({data:{items:[],next_cursor:null}}));}) as typeof fetch;
 try{await new HttpHaeryongClient().retainedSourceReads("p/+","files/+","opaque/+");assert.equal(calls[0]!.pathname,"/api/v1/projects/p%2F%2B/source-reads");assert.deepEqual(Object.fromEntries(calls[0]!.searchParams),{source_id:"files/+",cursor:"opaque/+"});}finally{globalThis.fetch=original;}
 let finish!:(value:unknown)=>void;const named:unknown[]=[];const client=new NativeHaeryongClient({call:(name:string,payload:unknown)=>{named.push([name,payload]);return new Promise(resolve=>{finish=resolve;});}} as unknown as NativePort);
 const pending=client.retainedSourceReads("p","files","cursor");assert.deepEqual(named,[["retainedSourceReads",{project_id:"p",source_id:"files",cursor:"cursor"}]]);client.invalidate();finish({items:[],next_cursor:null});await assert.rejects(pending,StaleResponseError);
});

test("T4R3UI01 saved generation scopes disambiguate blank titles across project target base intent and K despite current label changes",async()=>{
 const {savedGenerationScope}=await import("../app/screens/generation-context.ts");
 const a={operationId:"operation-A",request:{project_id:"project-A",document_id:"document-A",base_version_id:"frozen-A",intent:"publish",title:""},promptSHA:"a",promptSize:1,dispatchReady:true} as import("./generation.ts").GenerationRecord;
 const b={...structuredClone(a),operationId:"operation-B",request:{...a.request,project_id:"project-B",document_id:null,base_version_id:null,intent:"draft" as const}};
 const before=JSON.stringify([a,b]);const projects=[{project_id:"project-A",name:"Alpha"},{project_id:"project-B",name:"Beta"}];
 const doc={document_id:"document-A",project_id:"project-A",title:"Current display title",latest_version_id:"newer-unrelated-base"} as import("./types.ts").DocumentView;
 assert.deepEqual(savedGenerationScope(a,projects,{"document-A":doc}),{projectId:"project-A",projectName:"Alpha",documentId:"document-A",documentTitle:"Current display title",baseVersionId:"frozen-A",intent:"publish",operationId:"operation-A"});
 const newScope=savedGenerationScope(b,projects,{"document-A":doc});assert.equal(newScope.documentId,null);assert.equal(newScope.baseVersionId,null);assert.equal(newScope.intent,"draft");assert.equal(newScope.projectId,"project-B");assert.equal(newScope.operationId,"operation-B");
 const changedForm={project_id:"project-C",document_id:"document-C",base_version_id:"base-C",intent:"publish"};assert.notEqual(newScope.projectId,changedForm.project_id);
 assert.equal(savedGenerationScope(a,projects,{"document-A":{...doc,project_id:"other"}}).documentTitle,"문서 제목 확인 불가");
 assert.equal(savedGenerationScope(a,projects,{"document-A":null}).documentTitle,"문서 제목 확인 불가");
 assert.equal(savedGenerationScope(a,[],{}).projectId,"project-A");assert.equal(savedGenerationScope(a,projects,{"document-A":{...doc,title:"Renamed"}}).baseVersionId,"frozen-A");assert.equal(JSON.stringify([a,b]),before);
});

test("T4R3UI02 switching failed or stopped generation recovery never associates an old job with the new saved request",async()=>{
 const {savedGenerationJob}=await import("../app/screens/generation-context.ts");
 const a={operationId:"op-A",accepted:{job_id:"job-A"}} as import("./generation.ts").GenerationRecord;
 const b={operationId:"op-B"} as import("./generation.ts").GenerationRecord;
 const oldJob={job_id:"job-A",state:"paused"} as import("./types.ts").JobView;
 assert.equal(savedGenerationJob(a,oldJob),oldJob);
 // Selecting B, then failure/Stop before admission or job GET, leaves no A action.
 assert.equal(savedGenerationJob(b,oldJob),null);assert.equal(savedGenerationJob(b,null),null);
 b.accepted={job_id:"job-B"} as import("./types.ts").GenerationAccepted;
 assert.equal(savedGenerationJob(b,oldJob),null);
 const newJob={job_id:"job-B",state:"running"} as import("./types.ts").JobView;
 assert.equal(savedGenerationJob(b,newJob),newJob);assert.equal(savedGenerationJob(a,newJob),null);
 assert.equal(savedGenerationJob(a,oldJob),oldJob);
});
