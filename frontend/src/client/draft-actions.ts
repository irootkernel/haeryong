import type { DraftPatch, DraftView, HaeryongClient } from "./types.ts";
import type { ImportJournal } from "./transfer.ts";
import type { DispatchAuthority, ShellController } from "./controller.ts";
import { ApiError, DispatchBlockedError } from "./types.ts";
import { reconcileSourceWrite } from "./source-operations.ts";
import { requireOriginalRuntime } from "./generation.ts";

export interface PendingDraft {
 key:string;draftId:string;action:"reject"|"replace"|"publish";etag:string;
 upload?:ImportJournal;body?:DraftPatch;dispatchReady:boolean;runtimeGeneration?:string;
 refused?:{status:number;code:string};
}
const refused=(record:PendingDraft,error:ApiError,save:(record:PendingDraft)=>void)=>{
 record.refused={status:error.status,code:error.code};save(record);
};

/** Receipt-only recovery remains valid across runtimes; only exact same-runtime 404 may dispatch. */
export async function recoverDraftAction(client:HaeryongClient,record:PendingDraft,runtime:string,save:(record:PendingDraft)=>void,guard:()=>void):Promise<boolean> {
 guard();
 if(record.dispatchReady){
  try{await reconcileSourceWrite<DraftView>(record.key,id=>{guard();return client.operation(id);});return true;}
  catch(error){
   // ApiError here is a definitive failed receipt. Lookup failures are wrapped
   // by reconcileSourceWrite and must retain the original pending identity.
   if(error instanceof ApiError){refused(record,error,save);throw error;}
   if(!(error instanceof Error && error.cause instanceof ApiError && error.cause.status===404))throw error;
  }
 }
 if(record.refused)throw new Error("확정된 거부를 확인하고 새 동작을 선택해 주세요.");
 requireOriginalRuntime(record.runtimeGeneration,runtime);
 return false;
}

export async function dispatchDraftAction(client:HaeryongClient,record:PendingDraft,runtime:string,save:(record:PendingDraft)=>void,guard:()=>void):Promise<void> {
 guard();requireOriginalRuntime(record.runtimeGeneration,runtime);
 if(record.refused)throw new Error("확정된 거부를 확인하고 새 동작을 선택해 주세요.");
 record.dispatchReady=true;save(record);
 try{
  if(record.action==="publish")await client.draftPublish(record.draftId,record.etag,record.key);
  else await client.draftPatch(record.draftId,record.body!,record.etag,record.key);
 }catch(error){
  // Neither uncertain/network failures nor identity conflicts are permissions
  // to discard the original request. A stale validator/base is definitive.
  if(error instanceof ApiError && (error.status===412 || error.status===409&&error.code==="VERSION_CONFLICT"))refused(record,error,save);
  throw error;
 }
}

/** Release only an explicitly acknowledged definitive refusal, retaining its exact history. */
export function acknowledgeDraftRefusal(storage:Pick<Storage,"getItem"|"setItem"|"removeItem">,prefix:string,record:PendingDraft):void {
 if(!record.refused || storage.getItem(prefix+record.draftId)!==JSON.stringify(record))throw new Error("확정된 원래 거부를 먼저 확인해 주세요.");
 storage.setItem(prefix+"history:"+record.draftId+":"+record.key,JSON.stringify(record));
 storage.removeItem(prefix+record.draftId);
}

/** Capture once, explicitly narrow null and keep that exact identity across awaits. */
export function captureDraftAuthority(controller:Pick<ShellController,"captureDispatchAuthority"|"requireVerifiedDispatch">):DispatchAuthority {
 const authority=controller.captureDispatchAuthority();
 if(authority===null)throw new DispatchBlockedError();
 controller.requireVerifiedDispatch(authority);
 return authority;
}

export interface DraftActionControl {stopped:boolean}
/** UI action lifetime only; journals and server receipts remain the durable authority. */
export class DraftActionOwner {
 #current:DraftActionControl|null=null;
 begin():DraftActionControl {const control={stopped:false};this.#current=control;return control;}
 stop():void {if(this.#current)this.#current.stopped=true;}
 continues(control:DraftActionControl):boolean {return this.#current===control&&!control.stopped;}
 require(control:DraftActionControl):void {if(!this.continues(control))throw new Error("전송 중지됨. 원래 초안 작업을 유지합니다.");}
}

/** Explain the owner's next decision without implying that acknowledgement rebases a candidate. */
export function draftRefusalText(refusal:{status:number;code:string}):string {
 if(refusal.status===412)return "다른 동작으로 초안이 바뀌었습니다. 거부를 확인한 뒤 현재 초안을 다시 읽고 새 동작을 선택하세요.";
 if(refusal.status===409&&refusal.code==="VERSION_CONFLICT")return "대상 문서에 새 버전이 발행되어 이 초안의 원래 기준으로는 발행할 수 없습니다. 이 초안과 교체 결과는 원래 기준을 유지합니다. 현재 기준을 사용하려면 새 연결 생성/초안을 명시적으로 요청하세요.";
 return "원래 초안 동작의 실패가 확정되었습니다. 거부를 확인한 뒤 현재 초안에서 가능한 새 동작을 선택하세요.";
}
