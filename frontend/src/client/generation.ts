// One durable caller record per explicit generation request. Uploading uses the
// existing ranged transfer engine; only the service owns generation and publish.
import { sha256 } from "@noble/hashes/sha2.js";
import { ApiError } from "./types.ts";
import type { GenerationRequest, GenerationAccepted, HaeryongClient } from "./types.ts";
import { BrowserJournalStore, JournalReadError, JournalWriteError, TransferEngine, TRANSPORT_ENTRY, journalAuthority, journalKey } from "./transfer.ts";
import type { ImportJournal, PlannedInput, JournalStore, TransferCallbacks } from "./transfer.ts";
import { reconcileSourceWrite } from "./source-operations.ts";

export interface GenerationResume {
  key:string;decision:"reconcile_only"|"continue"|"retry";generation:number;etag:string;
  /** Legacy pending records remain receipt-readable; their runtime is unknown. */
  runtimeGeneration?:string;dispatchReady?:boolean;refused?:{status:number;code:string};
}
export interface GenerationRecord {
  operationId: string;
  request: Omit<GenerationRequest,"prompt">;
  promptSHA: string;
  promptSize: number;
  upload?: ImportJournal;
  accepted?: GenerationAccepted;
  dispatchReady: boolean;
  /** Omitted only in pre-correction recovery records; never inferred from current auth. */
  runtimeGeneration?: string;
  resume?:GenerationResume;
  resumeHistory?:GenerationResume[];
}
export interface GenerationJournal {
  read(): GenerationRecord[];
  save(record: GenerationRecord): void;
}
export class BrowserGenerationJournal implements GenerationJournal {
  readonly authority:string;
  readonly storage:Storage;
  constructor(authority: string, storage: Storage = localStorage) {this.authority=authority;this.storage=storage;}
  #prefix():string {return "haeryong.generation.v1:"+this.authority+":";}
  read(): GenerationRecord[] {
    try {
      const rows:GenerationRecord[]=[];
      for(let i=0;i<this.storage.length;i++){
        const key=this.storage.key(i);if(!key?.startsWith(this.#prefix()))continue;
        const raw=this.storage.getItem(key);if(raw===null)throw new Error("recovery record changed");
        const row=JSON.parse(raw) as GenerationRecord;
        if(key!==this.#prefix()+row.operationId || !row.request || typeof row.dispatchReady!=="boolean")throw new Error("invalid generation record");
        rows.push(row);
      }
      return rows;
    } catch(cause){throw new JournalReadError(cause);}
  }
  save(record:GenerationRecord):void {
    try{this.storage.setItem(this.#prefix()+record.operationId,JSON.stringify(record));}catch(cause){throw new JournalWriteError(cause);}
  }
}
export type SubmissionLock=<T>(name:string,action:()=>Promise<T>)=>Promise<T>;
const browserLock:SubmissionLock=async(name,action)=>{
  if(!navigator.locks?.request)throw new Error("이 환경은 안전한 요청 잠금을 지원하지 않습니다. CLI에서 같은 작업을 복구해 주세요.");
  return navigator.locks.request(name,action);
};
const digest=(bytes:Uint8Array)=>Array.from(sha256(bytes),b=>b.toString(16).padStart(2,"0")).join("");
export function promptPlan(prompt:string):PlannedInput {
  const file=new Blob([prompt],{type:"text/plain"});
  return {mode:"object",selectionName:"generation request",entries:[{path:TRANSPORT_ENTRY,kind:"file",size:file.size,file}],limits:{emptyDirectoriesRepresented:true,symlinksRepresented:true}};
}
export class GenerationSubmission {
  readonly transfers:TransferEngine;
  readonly client:HaeryongClient;
  readonly journal:GenerationJournal;
  readonly libraryId:string;
  readonly principalId:string;
  readonly lock:SubmissionLock;
  readonly runtimeGeneration:()=>string;
  constructor(client:HaeryongClient,journal:GenerationJournal,libraryId:string,principalId:string,uploads:JournalStore=new BrowserJournalStore("haeryong.generation-uploads.v1"), lock:SubmissionLock=browserLock,runtimeGeneration:()=>string=()=>"") {
    this.client=client;this.journal=journal;this.libraryId=libraryId;this.principalId=principalId;this.lock=lock;this.runtimeGeneration=runtimeGeneration;
    this.transfers=new TransferEngine(client,uploads);
  }
  capture(request:GenerationRequest,prompt:string,guard:()=>void):GenerationRecord {
    if(!prompt.trim() || request.prompt!==undefined || request.body_ref!==undefined)throw new Error("새 요청에는 별도로 선택한 본문이 필요합니다.");
    const runtimeGeneration=this.runtimeGeneration();
    requireOriginalRuntime(runtimeGeneration,runtimeGeneration);
    const bytes=new TextEncoder().encode(prompt);
    const record:GenerationRecord={operationId:crypto.randomUUID(),request:structuredClone(request),promptSHA:digest(bytes),promptSize:bytes.byteLength,dispatchReady:false,runtimeGeneration};
    guard();this.journal.save(record);return record;
  }
  async send(record:GenerationRecord,prompt:string,callbacks:TransferCallbacks):Promise<GenerationAccepted> {
    return this.lock("haeryong.generation:"+this.libraryId+":"+this.principalId+":"+record.operationId,async()=>{
      const retained=this.journal.read().find(row=>row.operationId===record.operationId);
      if(!retained)throw new JournalReadError("generation record missing");
      Object.assign(record,retained);
      return this.#send(record,prompt,callbacks);
    });
  }
  async #send(record:GenerationRecord,prompt:string,callbacks:TransferCallbacks):Promise<GenerationAccepted> {
    if(record.accepted)return record.accepted;
    callbacks.guard?.();
    // Receipt-first after dispatch; no upload or current defaults are recreated.
    if(record.dispatchReady){
      try{const result=await reconcileSourceWrite<GenerationAccepted>(record.operationId,id=>this.client.operation(id),true);record.accepted=result;this.journal.save(record);return result;}
      catch(error){
        // An explicit GET 404 means the original durable request is not yet
        // observable. Retry may only reuse its exact saved key/body below.
        if(!(error instanceof Error && error.cause instanceof ApiError && error.cause.status===404))throw error;
      }
    }
    requireOriginalRuntime(record.runtimeGeneration,this.runtimeGeneration());
    if(!record.request.body_ref){
      const bytes=new TextEncoder().encode(prompt);
      if(bytes.byteLength!==record.promptSize || digest(bytes)!==record.promptSHA)throw new Error("원래 요청 본문을 다시 선택해 주세요. 작업 키는 유지됩니다.");
      const input=promptPlan(prompt);
      if(!record.upload){
        record.upload=await this.transfers.beginOwnedUpload(this.libraryId,this.principalId,{purpose:"body",input_mode:"object",target:{operation_id:record.operationId,action:"generation",project_id:record.request.project_id,document_id:record.request.document_id}},input,callbacks);
        this.journal.save(record);
      }
      const retained=this.transfers.journal.load(journalAuthority(this.libraryId,this.principalId)).records.find(row=>journalKey(row)===journalKey(record.upload!));
      if(!retained || !retained.uploadRequest || JSON.stringify(retained.uploadRequest)!==JSON.stringify(record.upload.uploadRequest))throw new JournalReadError("original upload preparation missing or changed");
      record.upload=retained;
      const upload=retained;
      await this.transfers.attachUpload(upload,callbacks);
      if(upload.phase==="completing" || upload.phase==="sealing" || upload.phase==="sealed")await this.transfers.complete(upload,callbacks);
      else await this.transfers.resume(upload,input,callbacks);
      // Accepted sealing is preparation, never generation/publication success.
      for(;;){
        callbacks.guard?.();if(callbacks.shouldContinue && !callbacks.shouldContinue())throw new Error("전송 중지됨. 원래 작업과 업로드가 보존됩니다.");
        const detail=await this.client.uploadDetail(upload.uploadId!);
        if(detail.state==="sealed" && detail.reusable && detail.sealed_object_ref){record.request.body_ref={...detail.sealed_object_ref,media_type:"text/plain"};break;}
        if(detail.state!=="sealing")throw new Error(detail.error??"업로드 상태를 확인해 주세요.");
        await new Promise(resolve=>setTimeout(resolve,200));
      }
      this.journal.save(record);
    }
    callbacks.guard?.();requireOriginalRuntime(record.runtimeGeneration,this.runtimeGeneration());record.dispatchReady=true;this.journal.save(record);
    const result=await this.client.generationStart(record.request,record.operationId);
    record.accepted=result;this.journal.save(record);return result;
  }
}

/** A missing receipt never grants a new effect across restore, even after fresh login. */
export function requireOriginalRuntime(original:string|undefined,current:string):void {
  if(!original || !current || original!==current)throw new Error("서비스 세대가 달라 원래 요청을 다시 전송할 수 없습니다. 원래 작업 결과를 확인해 주세요.");
}

/** A resume is a separate mutation: its parent's admission runtime is not its own. */
export async function recoverGenerationResume(client:HaeryongClient,record:GenerationRecord,runtime:string,save:()=>void,guard:()=>void):Promise<boolean> {
  const pending=record.resume;
  if(!pending)throw new JournalReadError("original resume missing");
  guard();
  try{await reconcileSourceWrite(pending.key,id=>{guard();return client.operation(id);});return true;}
  catch(error){
    // Only an actual failed receipt is an ApiError here. Lookup failures are
    // wrapped as pending and preserve the exact original request.
    if(error instanceof ApiError){pending.refused={status:error.status,code:error.code};save();throw error;}
    if(!(error instanceof Error && error.cause instanceof ApiError && error.cause.status===404))throw error;
  }
  if(pending.refused)throw new Error(resumeRefusalText(pending.refused));
  requireOriginalRuntime(pending.runtimeGeneration,runtime);
  return false;
}

export async function dispatchGenerationResume(client:HaeryongClient,record:GenerationRecord,runtime:string,save:()=>void,guard:()=>void):Promise<void> {
  const pending=record.resume;
  if(!pending||!record.accepted)throw new JournalReadError("original resume/job missing");
  guard();requireOriginalRuntime(pending.runtimeGeneration,runtime);
  if(pending.refused)throw new Error(resumeRefusalText(pending.refused));
  pending.dispatchReady=true;save();
  try{await client.jobResume(record.accepted.job_id,pending.decision,pending.generation,pending.etag,pending.key);}
  catch(error){
    if(error instanceof ApiError && (error.status===412&&error.code==="PRECONDITION_FAILED" || error.status===409&&error.code==="INVALID_TRANSITION")){
      pending.refused={status:error.status,code:error.code};save();
    }
    throw error;
  }
}

/** Caller holds the same journal lock as submission/recovery. One save retains
 * the original refusal and releases its slot; no failed write loses history. */
export function acknowledgeGenerationResume(journal:GenerationJournal,operationId:string,expected:GenerationResume):GenerationRecord {
  const current=journal.read().find(row=>row.operationId===operationId);
  if(!expected.refused||!current?.resume||JSON.stringify(current.resume)!==JSON.stringify(expected))throw new JournalReadError("original refused resume changed");
  const next=structuredClone(current);
  next.resumeHistory=[...(next.resumeHistory??[]),structuredClone(expected)];
  delete next.resume;
  journal.save(next);
  return next;
}

export function resumeRefusalText(refusal:{status:number;code:string}):string {
  if(refusal.status===412)return "작업 상태가 바뀌어 원래 재개 결정이 거부되었습니다. 거부를 확인하고 현재 작업에서 새 결정을 선택하세요.";
  if(refusal.status===409&&refusal.code==="INVALID_TRANSITION")return "현재 작업 상태에서는 원래 재개 결정을 실행할 수 없습니다. 거부를 확인한 뒤 현재 상태에서 가능한 새 결정을 선택하세요.";
  return "원래 재개 결정의 실패가 확정되었습니다. 원래 기록을 보존하고 현재 작업 상태를 확인하세요.";
}
