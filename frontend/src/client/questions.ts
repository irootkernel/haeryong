import {sha256} from "@noble/hashes/sha2.js";
import {ApiError} from "./types.ts";
import type {QuestionRequest,QuestionAccepted,HaeryongClient,AnnotationStream} from "./types.ts";
import {BrowserJournalStore,JournalReadError,JournalWriteError,TransferEngine,journalAuthority,journalKey} from "./transfer.ts";
import type {ImportJournal,JournalStore,TransferCallbacks} from "./transfer.ts";
import {promptPlan,requireOriginalRuntime} from "./generation.ts";
import type {ProviderResumeRecord} from "./generation.ts";
import {reconcileSourceWrite,sourceWrite} from "./source-operations.ts";
import type {ContextReceipt} from "./context-types.ts";

export interface QuestionRecord extends ProviderResumeRecord {
 operationId:string;versionId:string;request:Omit<QuestionRequest,"question">;
 textSHA:string;textSize:number;runtimeGeneration?:string;dispatchReady:boolean;
 upload?:ImportJournal;contextUpload?:ImportJournal;contextOperation?:string;
 /** Complete selected state remains local until its independent context staging settles. */
 contextJSON?:string;accepted?:QuestionAccepted;
 contextDispatchReady?:boolean;
 admissionRefusal?:{status:number;code:string;runtimeGeneration:string;acknowledged:boolean};
}
export const questionUnresolved=(record:QuestionRecord)=>!record.accepted&&record.dispatchReady&&!(record.admissionRefusal?.acknowledged===true&&record.admissionRefusal.status===403&&record.admissionRefusal.code==="FORBIDDEN"&&!!record.runtimeGeneration&&record.admissionRefusal.runtimeGeneration===record.runtimeGeneration);
export interface QuestionJournal {read():QuestionRecord[];save(record:QuestionRecord):void}
export class BrowserQuestionJournal implements QuestionJournal {
 readonly authority:string;readonly storage:Storage;
 constructor(authority:string,storage:Storage=localStorage){this.authority=authority;this.storage=storage;}
 #prefix(){return "haeryong.questions.v1:"+this.authority+":";}
 read():QuestionRecord[]{try{const rows:QuestionRecord[]=[];for(let i=0;i<this.storage.length;i++){const key=this.storage.key(i);if(!key?.startsWith(this.#prefix()))continue;const raw=this.storage.getItem(key);if(raw===null)throw Error("question journal changed");const row=JSON.parse(raw) as QuestionRecord;if(key!==this.#prefix()+row.operationId||!row.versionId||!row.request||typeof row.dispatchReady!=="boolean")throw Error("invalid question journal");if(row.admissionRefusal&&(row.admissionRefusal.status!==403||row.admissionRefusal.code!=="FORBIDDEN"||typeof row.admissionRefusal.acknowledged!=="boolean"||!row.runtimeGeneration||row.admissionRefusal.runtimeGeneration!==row.runtimeGeneration))throw Error("invalid question admission refusal");rows.push(row);}return rows;}catch(cause){throw new JournalReadError(cause);}}
 save(record:QuestionRecord){try{this.storage.setItem(this.#prefix()+record.operationId,JSON.stringify(record));}catch(cause){throw new JournalWriteError(cause);}}
}
type Lock=<T>(name:string,action:()=>Promise<T>)=>Promise<T>;
const browserLock:Lock=(name,action)=>{if(!navigator.locks?.request)throw Error("안전한 질문 복구 잠금을 사용할 수 없습니다. CLI에서 원래 작업을 확인하세요.");return navigator.locks.request(name,action);};
const digest=(bytes:Uint8Array)=>Array.from(sha256(bytes),b=>b.toString(16).padStart(2,"0")).join("");

export class QuestionSubmission {
 readonly transfers:TransferEngine;readonly client:HaeryongClient;readonly journal:QuestionJournal;readonly libraryId:string;readonly principalId:string;readonly lock:Lock;readonly runtime:()=>string;
 constructor(client:HaeryongClient,journal:QuestionJournal,libraryId:string,principalId:string,uploads:JournalStore=new BrowserJournalStore("haeryong.question-uploads.v1"),lock:Lock=browserLock,runtime:()=>string=()=>""){this.client=client;this.journal=journal;this.libraryId=libraryId;this.principalId=principalId;this.lock=lock;this.runtime=runtime;this.transfers=new TransferEngine(client,uploads);}
 capture(versionId:string,request:QuestionRequest,text:string,guard:()=>void):QuestionRecord{
  if(!text.trim()||request.question!==undefined||request.body_ref!==undefined||request.context_ref!==undefined&&request.display_state!==undefined)throw Error("질문과 선택 문맥을 확인하세요.");
  const runtimeGeneration=this.runtime();requireOriginalRuntime(runtimeGeneration,runtimeGeneration);
  const frozen=structuredClone(request),bytes=new TextEncoder().encode(text);
  const contextJSON=frozen.display_state===undefined?undefined:JSON.stringify(frozen.display_state);
  if(frozen.display_state!==undefined&&contextJSON===undefined)throw Error("선택 문맥을 직렬화할 수 없습니다.");
  delete frozen.display_state;
  const record:QuestionRecord={operationId:crypto.randomUUID(),versionId,request:frozen,textSHA:digest(bytes),textSize:bytes.byteLength,runtimeGeneration,dispatchReady:false,...(contextJSON===undefined?{}:{contextJSON})};guard();this.journal.save(record);return record;
 }
 async send(record:QuestionRecord,text:string,callbacks:TransferCallbacks):Promise<QuestionAccepted>{
  return this.lock("haeryong.questions:"+this.libraryId+":"+this.principalId+":"+record.operationId,async()=>{
   const saved=this.journal.read().find(row=>row.operationId===record.operationId);if(!saved)throw new JournalReadError("question missing");Object.assign(record,saved);
   if(record.accepted)return record.accepted;
   if(record.admissionRefusal)throw Error("접수되지 않은 원래 질문입니다. 거부 확인 후 새 질문을 명시적으로 준비하세요.");
   callbacks.guard?.();
   // A local preparation prefix may survive after the original request completed.
   try{const accepted=await reconcileSourceWrite<QuestionAccepted>(record.operationId,id=>this.client.operation(id),true);record.accepted=accepted;this.journal.save(record);return accepted;}catch(error){if(!(error instanceof Error&&error.cause instanceof ApiError&&error.cause.status===404)){await this.#rememberRefusal(record,error,()=>callbacks.guard?.());throw error;}}
   requireOriginalRuntime(record.runtimeGeneration,this.runtime());
   if(!record.request.body_ref){
    const bytes=new TextEncoder().encode(text);if(bytes.byteLength!==record.textSize||digest(bytes)!==record.textSHA)throw Error("원래 질문 본문을 다시 선택하세요. 원래 작업 ID는 유지됩니다.");
    const plan=promptPlan(text);plan.selectionName="question text";
    if(!record.upload){record.upload=await this.transfers.beginOwnedUpload(this.libraryId,this.principalId,{purpose:"body",input_mode:"object",target:{operation_id:record.operationId,action:"question",version_id:record.versionId,page_path:record.request.page_path}},plan,callbacks);this.journal.save(record);}
    record.upload=await this.#upload(record.upload,plan,callbacks);
    const detail=await this.#sealed(record.upload,callbacks);if(!detail.sealed_object_ref)throw Error("질문 본문 객체를 확인할 수 없습니다.");record.request.body_ref={...detail.sealed_object_ref,media_type:"text/plain"};this.journal.save(record);
   }
   if(record.contextJSON!==undefined&&!record.request.context_ref){
    const plan=promptPlan(record.contextJSON);plan.selectionName="question display state";
    if(!record.contextUpload){record.contextUpload=await this.transfers.beginOwnedUpload(this.libraryId,this.principalId,{purpose:"context",input_mode:"object",target:{version_id:record.versionId,page_path:record.request.page_path,render_instance_id:null}},plan,callbacks);this.journal.save(record);}
    record.contextUpload=await this.#upload(record.contextUpload,plan,callbacks);
    await this.#sealed(record.contextUpload,callbacks);
    if(!record.contextOperation){record.contextOperation=crypto.randomUUID();this.journal.save(record);}
    callbacks.guard?.();requireOriginalRuntime(record.runtimeGeneration,this.runtime());
    const contextKey=record.contextOperation;
    let context:ContextReceipt|null=null;
    if(record.contextDispatchReady){try{context=await reconcileSourceWrite<ContextReceipt>(contextKey,id=>this.client.operation(id));}catch(error){if(!(error instanceof Error&&error.cause instanceof ApiError&&error.cause.status===404))throw error;}}
    if(!context){
     callbacks.guard?.();requireOriginalRuntime(record.runtimeGeneration,this.runtime());record.contextDispatchReady=true;this.journal.save(record);
     context=await sourceWrite<ContextReceipt>(contextKey,()=>this.client.consumeContext({version_id:record.versionId,page_path:record.request.page_path,render_instance_id:null},record.contextUpload!.uploadId!,contextKey),id=>this.client.operation(id));
    }
    const expected=new TextEncoder().encode(record.contextJSON);
    if(context.bytes_sha256!==digest(expected)||context.media_type!=="application/json")throw Error("저장된 문맥의 정확한 바이트를 확인할 수 없습니다.");
    callbacks.guard?.();record.request.context_ref=context.context_ref;this.journal.save(record);
   }
   callbacks.guard?.();requireOriginalRuntime(record.runtimeGeneration,this.runtime());record.dispatchReady=true;this.journal.save(record);
   try{const accepted=await this.client.questionStart(record.versionId,record.request,record.operationId);record.accepted=accepted;this.journal.save(record);return accepted;}
   catch(error){
    // A loose403/404, lost202 or permission failure is not non-admission proof.
    // Only a stable failed receipt can fence an earlier server request that
    // survived a lost response. GET404 never proves that request has drained.
    await this.#rememberRefusal(record,error,()=>callbacks.guard?.());
    throw error;
   }
  });
 }
 #matchesRefusal(record:QuestionRecord,details:Record<string,unknown>){return details.question_admission==="refused_before_admission"&&details.operation_id===record.operationId&&details.principal_id===this.principalId&&details.runtime_generation===record.runtimeGeneration;}
 async #terminalRefusal(record:QuestionRecord,guard:()=>void){
  guard();requireOriginalRuntime(record.runtimeGeneration,this.runtime());
  const receipt=await this.client.operation(record.operationId);
  guard();requireOriginalRuntime(record.runtimeGeneration,this.runtime());
  const body=receipt.result_metadata as {error?:{code?:string;retryable?:boolean;details?:Record<string,unknown>}}|null;
  if(receipt.operation_id!==record.operationId||receipt.state!=="failed"||receipt.original_status!==403||body?.error?.code!=="FORBIDDEN"||body.error.retryable!==false||!body.error.details||!this.#matchesRefusal(record,body.error.details))throw Error("원래 질문의 최종 접수 거부 기록을 확인할 수 없습니다. 원래 작업 ID는 유지됩니다.");
 }
 async #rememberRefusal(record:QuestionRecord,error:unknown,guard:()=>void){
  if(!(error instanceof ApiError&&error.status===403&&error.code==="FORBIDDEN"&&this.#matchesRefusal(record,error.details)))return;
  guard();requireOriginalRuntime(record.runtimeGeneration,this.runtime());
  // A retained preparation prefix may predate the observed server request.
  // Keep it fenced if the authoritative readback now loses permission/network.
  record.dispatchReady=true;this.journal.save(record);
  await this.#terminalRefusal(record,guard);
  record.admissionRefusal={status:error.status,code:error.code,runtimeGeneration:record.runtimeGeneration!,acknowledged:false};this.journal.save(record);
 }
 async acknowledgeRefusal(record:QuestionRecord,guard:()=>void):Promise<QuestionRecord>{
  return this.lock("haeryong.questions:"+this.libraryId+":"+this.principalId+":"+record.operationId,async()=>{
   const saved=this.journal.read().find(row=>row.operationId===record.operationId);
   if(!saved||saved.accepted||!saved.admissionRefusal||saved.admissionRefusal.acknowledged||JSON.stringify(saved)!==JSON.stringify(record))throw Error("원래 질문 거부 기록이 바뀌었습니다.");
   await this.#terminalRefusal(saved,guard);
   saved.admissionRefusal!.acknowledged=true;this.journal.save(saved);return saved;
  });
 }
 async #upload(upload:ImportJournal,plan:ReturnType<typeof promptPlan>,callbacks:TransferCallbacks){
  const retained=this.transfers.journal.load(journalAuthority(this.libraryId,this.principalId)).records.find(row=>journalKey(row)===journalKey(upload));
  if(!retained||!retained.uploadRequest||JSON.stringify(retained.uploadRequest)!==JSON.stringify(upload.uploadRequest))throw new JournalReadError("question upload identity changed");
  await this.transfers.attachUpload(retained,callbacks);
  if(retained.phase==="completing"||retained.phase==="sealing"||retained.phase==="sealed")await this.transfers.complete(retained,callbacks);else await this.transfers.resume(retained,plan,callbacks);
  return retained;
 }
 async #sealed(upload:ImportJournal,callbacks:TransferCallbacks){
  for(;;){callbacks.guard?.();if(callbacks.shouldContinue&&!callbacks.shouldContinue())throw Error("중지됨. 원래 질문과 업로드를 유지합니다.");const detail=await this.client.uploadDetail(upload.uploadId!);if(detail.state==="sealed"&&detail.reusable)return detail;if(detail.state!=="sealing")throw Error(detail.error??"질문 준비 업로드 상태를 확인하세요.");await new Promise(resolve=>setTimeout(resolve,200));}
 }
}

/** Full verified text consumption. This imposes no saved-context prefix quota. */
export async function questionStreamText(stream:AnnotationStream,guard:()=>void):Promise<string>{
 const reader=stream.body.getReader(),decoder=new TextDecoder("utf-8",{fatal:true}),hash=sha256.create();let text="",size=0n;
 try{for(;;){guard();const part=await reader.read();guard();if(part.done)break;size+=BigInt(part.value.byteLength);hash.update(part.value);text+=decoder.decode(part.value,{stream:true});}text+=decoder.decode();guard();if(size!==BigInt(stream.byte_size)||Array.from(hash.digest(),b=>b.toString(16).padStart(2,"0")).join("")!==stream.bytes_sha256)throw Error("저장된 본문 무결성 확인에 실패했습니다.");return text;}finally{reader.releaseLock();stream.cancel();}
}
