import {test} from "node:test";
import assert from "node:assert/strict";
import {GenerationSubmission,requireOriginalRuntime,recoverGenerationResume,dispatchGenerationResume,acknowledgeGenerationResume,resumeRefusalText} from "./generation.ts";
import type {GenerationRecord} from "./generation.ts";
import {recoverDraftAction,dispatchDraftAction,acknowledgeDraftRefusal,captureDraftAuthority,DraftActionOwner,draftRefusalText} from "./draft-actions.ts";
import type {PendingDraft} from "./draft-actions.ts";
import {ApiError,NetworkError,DispatchBlockedError} from "./types.ts";
import type {HaeryongClient,DocumentView,ProjectSourceView,JobView,DraftView} from "./types.ts";
import {refreshGenerationMountedState,refreshDraftMountedState,generationSourceMode} from "../app/screens/generation-context.ts";
const journal=()=>{const rows=new Map<string,GenerationRecord>();return {read:()=>[...rows.values()].map(r=>structuredClone(r)),save:(r:GenerationRecord)=>{rows.set(r.operationId,structuredClone(r));}};};
const lock=async<T>(_name:string,action:()=>Promise<T>)=>action();
const accepted={operation_id:"K",job_id:"j",draft_id:"d",intent:"draft" as const,job_url:"/api/v1/jobs/j"};
const request={project_id:"p",document_id:null,intent:"draft" as const,source_selection:{selections:[]},body_ref:{object_id:"o",bytes_sha256:"a".repeat(64),byte_size:"3",media_type:"text/plain"}};
const missing=()=>{throw new ApiError(404,"NOT_FOUND",false);};
test("GCLIENT08 restored generation journal reads original receipt but404 never dispatches under changed or missing original runtime",async()=>{
 for(const original of ["old",undefined]){
  let sends=0;let found=false;const client={operation:async(id:string)=>{if(!found)return missing();return {operation_id:id,state:"completed",original_status:202,result_metadata:accepted};},generationStart:async()=>{sends++;return accepted;}} as unknown as HaeryongClient;
  const records=journal();const row:GenerationRecord={operationId:"K",request,promptSHA:"",promptSize:0,dispatchReady:true,runtimeGeneration:original};records.save(row);
  const submission=new GenerationSubmission(client,records,"l","p",undefined,lock,()=>"new");
  await assert.rejects(submission.send(row,"",{}),/세대/);assert.equal(sends,0);assert.equal(records.read()[0]!.runtimeGeneration,original);
  found=true;assert.equal((await submission.send(row,"",{})).job_id,"j");assert.equal(sends,0);
 }
});
test("GCLIENT09 same-runtime404 retries only exact frozen generation body/key while runtime is captured before dispatch",async()=>{
 const calls:unknown[]=[];const records=journal();const client={operation:async()=>missing(),generationStart:async(body:unknown,key:string)=>{calls.push([structuredClone(body),key]);return accepted;}} as unknown as HaeryongClient;
 const row:GenerationRecord={operationId:"K",request:structuredClone(request),promptSHA:"",promptSize:0,dispatchReady:true,runtimeGeneration:"same"};records.save(row);
 const submission=new GenerationSubmission(client,records,"l","p",undefined,lock,()=>"same");await submission.send(row,"ignored current text",{});assert.deepEqual(calls,[[request,"K"]]);
 const fresh=submission.capture({project_id:"p",document_id:null,intent:"draft",source_selection:{selections:[]}},"prompt",()=>{});assert.equal(records.read().find(r=>r.operationId===fresh.operationId)?.runtimeGeneration,"same");
 assert.throws(()=>requireOriginalRuntime(undefined,"same"),/세대/);
});
for(const refusal of [new ApiError(412,"PRECONDITION_FAILED",false),new ApiError(409,"VERSION_CONFLICT",false)])test(`GCLIENT10 definitive draft${refusal.status} keeps exact identity and marks refusal, while uncertainty stays pending`,async()=>{
 const record:PendingDraft={key:"K",draftId:"d",action:"publish",etag:'"old"',dispatchReady:false,runtimeGeneration:"r"};const saved:PendingDraft[]=[];
 const client={draftPublish:async()=>{throw refusal;}} as unknown as HaeryongClient;
 await assert.rejects(dispatchDraftAction(client,record,"r",r=>saved.push(structuredClone(r)),()=>{}));assert.deepEqual(record.refused,{status:refusal.status,code:refusal.code});assert.equal(record.key,"K");assert.equal(record.etag,'"old"');assert.equal(saved.at(-1)?.refused?.status,refusal.status);
 if(refusal.status===412)assert.match(draftRefusalText(refusal),/초안이 바뀌었습니다/);else{assert.match(draftRefusalText(refusal),/새 버전이 발행/);assert.match(draftRefusalText(refusal),/원래 기준을 유지/);assert.match(draftRefusalText(refusal),/새 연결 생성/);}

 const uncertain={...record,refused:undefined,key:"network"};await assert.rejects(dispatchDraftAction({draftPublish:async()=>{throw new NetworkError();}} as unknown as HaeryongClient,uncertain,"r",()=>{},()=>{}));assert.equal(uncertain.refused,undefined);assert.equal(uncertain.dispatchReady,true);
 for(const code of ["OPERATION_CONFLICT","OPERATION_RECONCILIATION_REQUIRED"]){
  const identityConflict={...record,refused:undefined,key:"identity"};
  await assert.rejects(dispatchDraftAction({draftPublish:async()=>{throw new ApiError(409,code,false);}} as unknown as HaeryongClient,identityConflict,"r",()=>{},()=>{}));
  assert.equal(identityConflict.refused,undefined);assert.equal(identityConflict.key,"identity");assert.equal(identityConflict.etag,'"old"');
 }

});
test("GCLIENT11 draft receipt failure is definitive; restore404 and legacy journal remain GET-only; same-runtime404 is eligible for exact dispatch",async()=>{
 for(const runtime of ["old",undefined,"same"]){
  let sends=0;const record:PendingDraft={key:"K",draftId:"d",action:"reject",body:{action:"reject"},etag:'"original"',dispatchReady:true,runtimeGeneration:runtime};
  const client={operation:async()=>missing(),draftPatch:async()=>{sends++;return {};}} as unknown as HaeryongClient;
  if(runtime==="same"){assert.equal(await recoverDraftAction(client,record,"same",()=>{},()=>{}),false);await dispatchDraftAction(client,record,"same",()=>{},()=>{});assert.equal(sends,1);}
  else{await assert.rejects(recoverDraftAction(client,record,"same",()=>{},()=>{}),/세대/);assert.equal(sends,0);}
 }
 const record:PendingDraft={key:"K",draftId:"d",action:"publish",etag:'"old"',dispatchReady:true,runtimeGeneration:"r"};
 const failed={operation:async()=>({operation_id:"K",state:"failed",original_status:409,result_metadata:{error:{code:"VERSION_CONFLICT"}}})} as unknown as HaeryongClient;
 await assert.rejects(recoverDraftAction(failed,record,"new",()=>{},()=>{}));assert.equal(record.refused?.code,"VERSION_CONFLICT");assert.equal(record.etag,'"old"');
});

test("GCLIENT13 explicit definitive-refusal acknowledgement releases only the active slot and preserves exact K/body/base validator history",()=>{
 const values=new Map<string,string>();const storage={getItem:(key:string)=>values.get(key)??null,setItem:(key:string,value:string)=>{values.set(key,value);},removeItem:(key:string)=>{values.delete(key);}};
 const record:PendingDraft={key:"oldK",draftId:"d",action:"replace",etag:'"original"',body:{action:"replace",upload_id:"sealed-original"},dispatchReady:true,runtimeGeneration:"old"};const prefix="scope:";
 storage.setItem(prefix+"d",JSON.stringify(record));assert.throws(()=>acknowledgeDraftRefusal(storage,prefix,record));assert.ok(storage.getItem(prefix+"d"));
 record.refused={status:412,code:"PRECONDITION_FAILED"};storage.setItem(prefix+"d",JSON.stringify(record));acknowledgeDraftRefusal(storage,prefix,record);
 assert.equal(storage.getItem(prefix+"d"),null);assert.deepEqual(JSON.parse(storage.getItem(prefix+"history:d:oldK")!),record);assert.equal(record.etag,'"original"');
 const next={...record,key:"newK",refused:undefined,etag:'"current"'};const encodedNext=JSON.stringify(next);storage.setItem(prefix+"d",encodedNext);assert.throws(()=>acknowledgeDraftRefusal(storage,prefix,record));assert.equal(storage.getItem(prefix+"d"),encodedNext);
});

test("GCLIENT14 draft capture rejects null and retains exact verified authority object rather than inferring runtime",()=>{
 let checks=0;
 assert.throws(()=>captureDraftAuthority({captureDispatchAuthority:()=>null,requireVerifiedDispatch:()=>{checks++;}}),DispatchBlockedError);assert.equal(checks,0);
 const captured={nonce:7,libraryId:"library",runtimeGeneration:"original"};
 const actual=captureDraftAuthority({captureDispatchAuthority:()=>captured,requireVerifiedDispatch:authority=>{assert.equal(authority,captured);checks++;}});
 assert.equal(actual,captured);assert.equal(actual.runtimeGeneration,"original");assert.equal(checks,1);
 assert.throws(()=>captureDraftAuthority({captureDispatchAuthority:()=>captured,requireVerifiedDispatch:()=>{throw new DispatchBlockedError();}}),DispatchBlockedError);
});
for(const refusal of [new ApiError(412,"PRECONDITION_FAILED",false),new ApiError(409,"VERSION_CONFLICT",false)])test(`GCLIENT15 stopped draft receiving definite${refusal.status} can acknowledge with a fresh action while old callbacks stay stopped`,async()=>{
 const owner=new DraftActionOwner();const originalControl=owner.begin();
 const captured={nonce:1,libraryId:"library",runtimeGeneration:"r"};
 const authority=captureDraftAuthority({captureDispatchAuthority:()=>captured,requireVerifiedDispatch:a=>assert.equal(a,captured)});
 const record:PendingDraft={key:"oldK",draftId:"d",action:"publish",etag:'"old"',dispatchReady:false,runtimeGeneration:authority.runtimeGeneration};
 const values=new Map<string,string>();const storage={getItem:(key:string)=>values.get(key)??null,setItem:(key:string,value:string)=>{values.set(key,value);},removeItem:(key:string)=>{values.delete(key);}};
 const save=(row:PendingDraft)=>storage.setItem("scope:"+row.draftId,JSON.stringify(row));
 let reject!:(cause:unknown)=>void;const client={draftPublish:()=>new Promise((_resolve,fail)=>{reject=fail;})} as unknown as HaeryongClient;
 const pending=dispatchDraftAction(client,record,authority.runtimeGeneration,save,()=>owner.require(originalControl));
 owner.stop();reject(refusal);await assert.rejects(pending);assert.equal(record.refused?.status,refusal.status);assert.equal(record.key,"oldK");assert.throws(()=>owner.require(originalControl));
 // The previous busy promise is now settled: the next explicit acknowledgement
 // receives a new control, with its current captured authority checked separately.
 const nextControl=owner.begin();owner.require(nextControl);
 const nextAuthority=captureDraftAuthority({captureDispatchAuthority:()=>captured,requireVerifiedDispatch:a=>assert.equal(a,captured)});
 assert.equal(nextAuthority,captured);acknowledgeDraftRefusal(storage,"scope:",record);
 assert.equal(storage.getItem("scope:d"),null);assert.deepEqual(JSON.parse(storage.getItem("scope:history:d:oldK")!),record);
 assert.equal(originalControl.stopped,true);assert.throws(()=>owner.require(originalControl));assert.equal(owner.continues(originalControl),false);assert.equal(owner.continues(nextControl),true);
});

const resumeRecord=():GenerationRecord=>({operationId:"parent",request:structuredClone(request),promptSHA:"",promptSize:0,dispatchReady:true,accepted:{...accepted,operation_id:"parent"},runtimeGeneration:"parent-old",resume:{key:"resume-K",decision:"continue",generation:7,etag:'"original-job"',runtimeGeneration:"resume-current",dispatchReady:true}});

test("T4R2RESUME01 changed and legacy resume runtime404 remain GET-only while a completed receipt is readable across runtime",async()=>{
 for(const runtime of ["old",undefined]){
  const record=resumeRecord();record.resume!.runtimeGeneration=runtime;
  const saved=journal();saved.save(record);let posts=0;const gets:string[]=[];let found=false;
  const client={operation:async(id:string)=>{gets.push(id);if(!found)return missing();return{operation_id:id,state:"completed",original_status:200,result_metadata:{job_id:"j"}};},jobResume:async()=>{posts++;}} as unknown as HaeryongClient;
  const original=JSON.stringify(record.resume);
  await assert.rejects(recoverGenerationResume(client,record,"new",()=>saved.save(record),()=>{}),/세대/);
  assert.deepEqual(gets,["resume-K"]);assert.equal(posts,0);assert.equal(JSON.stringify(record.resume),original);
  found=true;assert.equal(await recoverGenerationResume(client,record,"new",()=>saved.save(record),()=>{}),true);assert.equal(posts,0);
 }
});

test("T4R2RESUME02 same-runtime404 reuses exact resume identity and ignores the independently older parent runtime",async()=>{
 const record=resumeRecord();const saved=journal();saved.save(record);const calls:unknown[]=[];
 const client={operation:async(id:string)=>{calls.push(["GET",id]);return missing();},jobResume:async(...args:unknown[])=>{calls.push(["POST",...args]);}} as unknown as HaeryongClient;
 assert.equal(await recoverGenerationResume(client,record,"resume-current",()=>saved.save(record),()=>{}),false);
 await dispatchGenerationResume(client,record,"resume-current",()=>saved.save(record),()=>{});
 assert.deepEqual(calls,[["GET","resume-K"],["POST","j","continue",7,'"original-job"',"resume-K"]]);
 assert.equal(saved.read()[0]!.resume!.runtimeGeneration,"resume-current");assert.equal(record.runtimeGeneration,"parent-old");
});

for(const error of [new ApiError(412,"PRECONDITION_FAILED",false),new ApiError(409,"INVALID_TRANSITION",false)])test(`T4R2RESUME03 definitive resume${error.status} retains exact refusal and acknowledgement archives once without rebasing`,async()=>{
 const record=resumeRecord();const saved=journal();saved.save(record);const original=structuredClone(record.resume!);
 await assert.rejects(dispatchGenerationResume({jobResume:async()=>{throw error;}} as unknown as HaeryongClient,record,"resume-current",()=>saved.save(record),()=>{}));
 assert.deepEqual(record.resume,{...original,refused:{status:error.status,code:error.code}});assert.match(resumeRefusalText(record.resume!.refused!),/거부|실행할 수 없습니다/);
 const expected=structuredClone(record.resume!);const next=acknowledgeGenerationResume(saved,record.operationId,expected);
 assert.equal(next.resume,undefined);assert.deepEqual(next.resumeHistory,[expected]);assert.deepEqual(saved.read()[0]!.resumeHistory,[expected]);
 next.resume={key:"new-K",decision:"reconcile_only",generation:8,etag:'"current-job"',runtimeGeneration:"new-runtime",dispatchReady:false};saved.save(next);
 const encoded=JSON.stringify(saved.read());assert.throws(()=>acknowledgeGenerationResume(saved,record.operationId,expected));assert.equal(JSON.stringify(saved.read()),encoded);
});

test("T4R2RESUME04 sustained uncertainty and operation conflicts keep the old slot; failed receipts alone are definitive",async()=>{
 for(const failure of [new NetworkError(),new ApiError(409,"OPERATION_CONFLICT",false),new ApiError(409,"OPERATION_RECONCILIATION_REQUIRED",true),new ApiError(503,"SERVICE_RECOVERING",true)]){
  const record=resumeRecord();const saved=journal();saved.save(record);
  await assert.rejects(dispatchGenerationResume({jobResume:async()=>{throw failure;}} as unknown as HaeryongClient,record,"resume-current",()=>saved.save(record),()=>{}));
  assert.equal(record.resume!.refused,undefined);assert.equal(record.resume!.key,"resume-K");
  const original=JSON.stringify(record.resume);
  await assert.rejects(recoverGenerationResume({operation:async()=>{throw new ApiError(503,"SERVICE_RECOVERING",true);}} as unknown as HaeryongClient,record,"resume-current",()=>saved.save(record),()=>{}));
  assert.equal(JSON.stringify(record.resume),original);
 }
 const record=resumeRecord();const saved=journal();saved.save(record);
 const client={operation:async(id:string)=>({operation_id:id,state:"failed",original_status:412,result_metadata:{error:{code:"PRECONDITION_FAILED",retryable:false}}})} as unknown as HaeryongClient;
 await assert.rejects(recoverGenerationResume(client,record,"changed",()=>saved.save(record),()=>{}));assert.equal(record.resume!.refused!.status,412);
});

test("T4R2RESUME05 journal write failure cannot release a refused resume or lose its original history",()=>{
 const record=resumeRecord();record.resume!.refused={status:412,code:"PRECONDITION_FAILED"};const saved=journal();saved.save(record);const original=JSON.stringify(saved.read());
 assert.throws(()=>acknowledgeGenerationResume({read:saved.read,save:()=>{throw new Error("storage denied");}},record.operationId,structuredClone(record.resume!)),/storage denied/);
 assert.equal(JSON.stringify(saved.read()),original);
});

test("TASK00704GEN01 mounted Generation passive refresh updates defaults/documents/sources/job and labels while preserving local overrides/edits and fencing stale responses",async()=>{
 const calls:string[]=[];
 const passiveClient={
  configurationView:async()=>{
   calls.push("configurationView");
   return {
    config_revision:3,
    etag:'"cfg-3"',
    configuration:{
     projects:{
      p1:{source_mode_default:"current" as const,provider_id:"codex"},
      p2:{source_mode_default:"pinned" as const,provider_id:"codex"},
     },
    },
   };
  },
  listDocuments:async({projectId}:{projectId?:string})=>{
   calls.push(`listDocuments:${projectId}`);
   return {
    items:[{document_id:"doc-1",project_id:"p1",title:"Refreshed Doc",lifecycle_state:"active",latest_version_id:"v-2",created_at:"2026-11-06T00:00:00Z",updated_at:"2026-11-06T00:00:00Z"}] as unknown as DocumentView[],
    next_cursor:"doc-cursor-2",
   };
  },
  projectSources:async(projectId:string,cursor?:string)=>{
   calls.push(`projectSources:${projectId}:${cursor??"first"}`);
   if(!cursor){
    return {
     items:[{source_id:"src-git-edited",project_id:"p1",kind:"git",repo_id:"repo-a",last_observed_revision:"server-rev-new-a"}] as unknown as ProjectSourceView[],
     next_cursor:"src-page-2",
    };
   }
   return {
    items:[{source_id:"src-git-unedited",project_id:"p1",kind:"git",repo_id:"repo-b",last_observed_revision:"server-rev-new-b"}] as unknown as ProjectSourceView[],
    next_cursor:null,
   };
  },
  jobGet:async(jobId:string)=>{
   calls.push(`jobGet:${jobId}`);
   return {job_id:jobId,kind:"generation",state:"paused",attempt_generation:4,etag:'"job-4"'} as unknown as JobView;
  },
  documentDetail:async(docId:string)=>{
   calls.push(`documentDetail:${docId}`);
   return {document_id:docId,project_id:"p1",title:`Title-${docId}`} as unknown as DocumentView;
  },
 } as unknown as HaeryongClient;

 const activeRecord:GenerationRecord={
  operationId:"op-active",
  request:{...request,project_id:"p1",document_id:"doc-saved"},
  promptSHA:"sha",
  promptSize:10,
  dispatchReady:true,
  accepted:{operation_id:"op-active",job_id:"job-active",draft_id:"draft-1",intent:"draft",job_url:"/api/v1/jobs/job-active"},
  runtimeGeneration:"rt-1",
  resume:{key:"res-k",decision:"continue",generation:3,etag:'"job-3"',runtimeGeneration:"rt-1",dispatchReady:true},
 };

 let sourceDefaults:Record<string,"current"|"pinned">={p1:"pinned"};
 let providerDefaults:Record<string,string>={p1:""};
 const sourceOverrides:Record<string,"current"|"pinned">={p1:"pinned"};
 let documents:DocumentView[]=[];
 let documentCursor:string|null=null;
 let sources:ProjectSourceView[]=[];
 let revisions:Record<string,string>={"src-git-edited":"user-custom-sha","src-git-unedited":"server-rev-old-b"};
 const editedRevisions:Record<string,boolean>={"src-git-edited":true};
 let job:JobView|null=null;
 let labels:Record<string,DocumentView|null>={};

 const applied=await refreshGenerationMountedState({
  passiveClient,
  projects:[{project_id:"p1",name:"Project 1"}],
  project:"p1",
  active:activeRecord,
  records:[activeRecord],
  revisions,
  editedRevisions,
  canApply:()=>true,
  isCurrentProject:(pid)=>pid==="p1",
  isCurrentActive:(opId,jobId)=>opId==="op-active"&&jobId==="job-active",
  applyDefaults:(nextSrc,nextProv)=>{sourceDefaults=nextSrc;providerDefaults=nextProv;},
  applyProjectData:(data)=>{documents=data.documents;documentCursor=data.documentCursor;sources=data.sources;revisions=data.revisions;},
  applyJob:(nextJob)=>{job=nextJob;},
  applyDocumentLabels:(nextLabels)=>{labels=nextLabels;},
 });

 assert.equal(applied,true);
 assert.deepEqual(calls,["configurationView","listDocuments:p1","projectSources:p1:first","jobGet:job-active","documentDetail:doc-saved","projectSources:p1:src-page-2"]);
 assert.equal(sourceDefaults.p1,"current");
 assert.equal(providerDefaults.p1,"codex");
 // Local explicit sourceOverride on p1 still takes precedence over refreshed default:
 assert.equal(generationSourceMode("p1",sourceOverrides,sourceDefaults),"pinned");
 assert.equal(documents.length,1);
 assert.equal(documents[0]!.title,"Refreshed Doc");
 assert.equal(documentCursor,"doc-cursor-2");
 assert.equal(sources.length,2);
 // User-edited git revision is preserved while unedited git revision updates to server's new last_observed_revision:
 assert.deepEqual(revisions,{"src-git-edited":"user-custom-sha","src-git-unedited":"server-rev-new-b"});
 assert.equal((job as JobView|null)?.state,"paused");
 assert.equal((job as JobView|null)?.etag,'"job-4"');
 // Active record's pending resume journal slot is untouched:
 assert.equal(activeRecord.resume?.key,"res-k");
 assert.equal(activeRecord.resume?.etag,'"job-3"');
 assert.equal(labels["doc-saved"]?.title,"Title-doc-saved");

 // Fencing: if readGeneration/own-write invalidates canApply() before completion, nothing is overwritten:
 let fencedMutated=false;
 const fenced=await refreshGenerationMountedState({
  passiveClient,
  projects:[{project_id:"p1",name:"Project 1"}],
  project:"p1",
  active:activeRecord,
  records:[activeRecord],
  revisions,
  editedRevisions,
  canApply:()=>false,
  isCurrentProject:()=>true,
  isCurrentActive:()=>true,
  applyDefaults:()=>{fencedMutated=true;},
  applyProjectData:()=>{fencedMutated=true;},
  applyJob:()=>{fencedMutated=true;},
  applyDocumentLabels:()=>{fencedMutated=true;},
 });
 assert.equal(fenced,false);
 assert.equal(fencedMutated,false);
});

test("TASK00704DRAFT01 mounted Draft passive refresh updates list/selected/history and labels without clearing pending K/ETag or replacement input",async()=>{
 const calls:string[]=[];
 const refreshedDraft:DraftView={
  draft_id:"d-1",
  project_id:"p1",
  document_id:"doc-1",
  base_version_id:"v-1",
  job_id:"j-1",
  state:"ready",
  intent:"draft",
  revision:2,
  created_at:"2026-11-06T00:00:00Z",
  updated_at:"2026-11-06T01:00:00Z",
  payload_sha256:"a".repeat(64),
  metadata_sha256:null,
  published_version_id:null,
  failure_reason:null,
  etag:'"draft-rev-2"',
 };
 const passiveClient={
  draftList:async(projectId?:string)=>{
   calls.push(`draftList:${projectId??"all"}`);
   return {
    items:[{...refreshedDraft,revision:1,etag:'"draft-rev-1"'},{...refreshedDraft,draft_id:"d-2",document_id:null}],
    next_cursor:"draft-cursor-2",
   };
  },
  draftGet:async(draftId:string)=>{
   calls.push(`draftGet:${draftId}`);
   return refreshedDraft;
  },
  draftHistory:async(draftId:string)=>{
   calls.push(`draftHistory:${draftId}`);
   return {
    items:[refreshedDraft,{...refreshedDraft,revision:1,etag:'"draft-rev-1"'}],
    next_cursor:"hist-cursor-2",
   };
  },
  documentDetail:async(docId:string)=>{
   calls.push(`documentDetail:${docId}`);
   return {document_id:docId,project_id:"p1",title:"Draft Target Doc"} as unknown as DocumentView;
  },
 } as unknown as HaeryongClient;

 const pendingSlot:PendingDraft={
  key:"orig-draft-K",
  draftId:"d-1",
  action:"replace",
  etag:'"captured-rev-1"',
  body:{action:"replace",upload_id:"up-1"},
  dispatchReady:true,
  runtimeGeneration:"rt-1",
 };
 const localInput={mode:"archive" as const,selectionName:"bundle.zip",entries:[]};

 let rows:DraftView[]=[];
 let cursor:string|null=null;
 let selected:DraftView|null={...refreshedDraft,state:"preparing",revision:1,etag:'"draft-rev-1"'};
 let history:DraftView[]=[];
 let historyCursor:string|null=null;
 let labels:Record<string,DocumentView|null>={};

 const applied=await refreshDraftMountedState({
  passiveClient,
  project:"p1",
  selectedId:"d-1",
  includeHistory:true,
  canApply:()=>true,
  isCurrentProject:(pid)=>pid==="p1",
  isCurrentSelected:(did)=>did==="d-1",
  isHistoryOpen:()=>true,
  applyList:(items,next)=>{rows=items;cursor=next;},
  applySelected:(draft)=>{selected=draft;},
  applyHistory:(items,next)=>{history=items;historyCursor=next;},
  applyDocumentLabels:(nextLabels)=>{labels=nextLabels;},
 });

 assert.equal(applied,true);
 assert.deepEqual(calls,["draftList:p1","draftGet:d-1","draftHistory:d-1","documentDetail:doc-1"]);
 assert.equal(rows.length,2);
 // Selected row in list is synchronized with the fresh draftGet result:
 assert.equal(rows[0]!.revision,2);
 assert.equal(rows[0]!.etag,'"draft-rev-2"');
 assert.equal(cursor,"draft-cursor-2");
 assert.equal(selected?.state,"ready");
 assert.equal(selected?.revision,2);
 assert.equal(selected?.etag,'"draft-rev-2"');
 assert.equal(history.length,2);
 assert.equal(historyCursor,"hist-cursor-2");
 assert.equal(labels["doc-1"]?.title,"Draft Target Doc");
 // Pending slot K/ETag and local replacement input remain completely untouched:
 assert.equal(pendingSlot.key,"orig-draft-K");
 assert.equal(pendingSlot.etag,'"captured-rev-1"');
 assert.equal(localInput.selectionName,"bundle.zip");

 // Target-selection guard: if selected draft changed to "d-2" while refresh for "d-1" was in flight, "d-1" does not overwrite selected or history:
 calls.length=0;
 let selectedOverwritten=false;
 let historyOverwritten=false;
 await refreshDraftMountedState({
  passiveClient,
  project:"p1",
  selectedId:"d-1",
  includeHistory:true,
  canApply:()=>true,
  isCurrentProject:(pid)=>pid==="p1",
  isCurrentSelected:(did)=>did==="d-2",
  isHistoryOpen:()=>true,
  applyList:(items)=>{rows=items;},
  applySelected:()=>{selectedOverwritten=true;},
  applyHistory:()=>{historyOverwritten=true;},
 });
 assert.equal(selectedOverwritten,false);
 assert.equal(historyOverwritten,false);
});
