import {ApiError} from "../../client/types.ts";
import type {HaeryongClient,LifecycleCommitRequest,LifecycleResult,LifecycleAction,OperationView} from "../../client/types.ts";
export interface LifecycleIntent {body:LifecycleCommitRequest;generation:string;operation:LifecycleAction}
export const lifecycleJournalKey=(library:unknown,owner:unknown,document:string)=>"haeryong.lifecycle.v1:"+JSON.stringify([library,owner,document]);
export function pendingLifecycleJournals(storage:Pick<Storage,"length"|"key"|"getItem">,library:string,owner:string):Array<{key:string;documentId:string;intent:LifecycleIntent}>{
 const rows:Array<{key:string;documentId:string;intent:LifecycleIntent}>=[];
 for(let n=0;n<storage.length;n++){
  const key=storage.key(n);if(!key?.startsWith("haeryong.lifecycle.v1:"))continue;
  let identity:unknown;try{identity=JSON.parse(key.slice("haeryong.lifecycle.v1:".length));}catch{continue;}
  if(!Array.isArray(identity)||identity.length!==3||identity[0]!==library||identity[1]!==owner||typeof identity[2]!=="string")continue;
  const intent=JSON.parse(storage.getItem(key)??"null") as LifecycleIntent|null;
  if(!intent||typeof intent.body?.plan_id!=="string"||intent.body.confirmed!==true||!Number.isInteger(intent.body.expected_revision)||typeof intent.generation!=="string"||!["purge","trash","restore","archive"].includes(intent.operation))throw Error("수명주기 요청 복구 기록을 확인할 수 없습니다.");
  rows.push({key,documentId:identity[2],intent});
 }
 return rows;
}
// Called under the journal lock. Preserve the original intent until its job
// identity is durable; a quota/storage error must leave recovery possible.
export function finishLifecycleJournal(storage:Pick<Storage,"getItem"|"setItem"|"removeItem">,key:string,request:LifecycleIntent,result:LifecycleResult,remember:(id:string)=>void):void{
 if(storage.getItem(key)!==JSON.stringify(request))throw Error("다른 창의 수명주기 요청을 먼저 확인하세요.");
 if(result.plan_id!==request.body.plan_id)throw Error("계획 결과가 일치하지 않습니다.");
 if(request.operation==="purge"){
  if(result.lifecycle!=="purged"||!result.job_id)throw Error("원래 삭제 작업의 식별자를 확인할 수 없습니다.");
  remember(result.job_id);
 }
 updateLifecycleJournal(storage,key,request,null);
}
export async function reconcileLifecycle(client:HaeryongClient,intent:LifecycleIntent):Promise<LifecycleResult>{
 const receipt=await client.operation(intent.body.plan_id);
 const result=lifecycleResultFromReceipt(intent,receipt);
 if(!result)throw Error("원래 요청 결과가 아직 확인되지 않았습니다.");
 return result;
}
export function lifecycleResultFromReceipt(intent:LifecycleIntent,receipt:OperationView):LifecycleResult|null{
 if(receipt.state!=="completed")return null;
 const result=receipt.result_metadata as LifecycleResult;
 if(result?.plan_id!==intent.body.plan_id)throw Error("계획 결과가 일치하지 않습니다.");
 return result;
}
export function lifecycleRejected(cause:unknown):boolean{return cause instanceof ApiError&&[400,409,412,428].includes(cause.status)&&cause.code!=="OPERATION_RECONCILIATION_REQUIRED";}

// Called under the browser's per-document lock; an unrelated tab's pending
// request can never be replaced or cleared by a late acknowledgement.
export function updateLifecycleJournal(storage:Pick<Storage,"getItem"|"setItem"|"removeItem">,key:string,expected:LifecycleIntent|null,next:LifecycleIntent|null):void{
 if(storage.getItem(key)!==(expected?JSON.stringify(expected):null))throw Error("다른 창의 수명주기 요청을 먼저 확인하세요.");
 if(next)storage.setItem(key,JSON.stringify(next));else storage.removeItem(key);
}
