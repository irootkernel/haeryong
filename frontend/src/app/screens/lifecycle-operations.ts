import {ApiError} from "../../client/types.ts";
import type {HaeryongClient,LifecycleCommitRequest,LifecycleResult,LifecycleAction} from "../../client/types.ts";
export interface LifecycleIntent {body:LifecycleCommitRequest;generation:string;operation:LifecycleAction}
export async function reconcileLifecycle(client:HaeryongClient,intent:LifecycleIntent):Promise<LifecycleResult>{
 const receipt=await client.operation(intent.body.plan_id);
 if(receipt.state!=="completed")throw Error("원래 요청 결과가 아직 확인되지 않았습니다.");
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
