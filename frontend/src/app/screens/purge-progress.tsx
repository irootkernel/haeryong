import {useCallback,useEffect,useRef,useState} from "react";
import type {ShellController} from "../../client/controller.ts";
import {finishLifecycleJournal,pendingLifecycleJournals,lifecycleResultFromReceipt} from "./lifecycle-operations.ts";
import type {LifecycleIntent} from "./lifecycle-operations.ts";
import {StaleResponseError} from "../../client/types.ts";
import type {LifecycleResult} from "../../client/types.ts";
import {canDismissPurgeJob,dismissPurgeJob,lookupPurgeJobs,purgeJobsKey,purgeJobsNeedPoll,rememberedPurgeJobs,rememberPurgeJobId,type PurgeJobRow} from "./purge-jobs.ts";
import {useReviewInvalidation} from "./review-refresh.ts";
const key=(c:ShellController)=>purgeJobsKey(c.state.capabilities?.library_id,c.state.session?.principal_id);
export function rememberPurgeJob(controller:ShellController,id:string):void {
 rememberPurgeJobId(localStorage,key(controller),id);
}
export async function finishLifecycleRequest(controller:ShellController,journalKey:string,intent:LifecycleIntent,result:LifecycleResult,guard:()=>void):Promise<void>{
 if(!navigator.locks)throw Error("수명주기 요청 저장 잠금을 사용할 수 없습니다.");
 // Serialize the shared job bucket as well as the exact per-document journal.
 await navigator.locks.request(key(controller),()=>navigator.locks.request(journalKey,()=>{guard();finishLifecycleJournal(localStorage,journalKey,intent,result,id=>rememberPurgeJob(controller,id));}));
}
export function PurgeProgress({controller,refresh}:{controller:ShellController;refresh:number}){
 const [snapshot,setSnapshot]=useState<{rows:PurgeJobRow[];authority:ReturnType<ShellController["captureDispatchAuthority"]>}|null>(null),[issue,setIssue]=useState(""),[pending,setPending]=useState<string[]>([]),[retry,setRetry]=useState(0);
 const mounted=useRef(true),timerRef=useRef<ReturnType<typeof setTimeout>|undefined>(undefined),seqRef=useRef(0);
 useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;if(timerRef.current)clearTimeout(timerRef.current);};},[]);
 const dismiss=async(row:PurgeJobRow)=>{if(!snapshot)return;const authority=snapshot.authority,storageKey=key(controller);try{
  controller.requireVerifiedDispatch(authority);
  if(!navigator.locks)throw Error("삭제 작업 저장 잠금을 사용할 수 없습니다.");
  await navigator.locks.request(storageKey,()=>{controller.requireVerifiedDispatch(authority);if(!mounted.current)throw Error("이전 삭제 작업 화면입니다.");dismissPurgeJob(localStorage,storageKey,row);});
  if(mounted.current)setRetry(n=>n+1);
 }catch(e){if(mounted.current)setIssue(String(e));}};
 const runLoad=useCallback(async(passive:boolean,resync=false,expectedReadGen?:number):Promise<void>=>{
  if(timerRef.current){clearTimeout(timerRef.current);timerRef.current=undefined;}
  const seq=++seqRef.current;
  const authority=controller.captureDispatchAuthority();
  const readGen=expectedReadGen??controller.readGeneration;
  const reader=passive?controller.passiveClient:controller.client;
  const isCurrent=()=>mounted.current&&seqRef.current===seq&&controller.isCurrentReadAuthority(authority,readGen);
  const canAdopt=()=>{
   if(!mounted.current||!controller.isCurrentReadAuthority(authority,readGen))return false;
   if(seqRef.current!==seq){if(resync)throw new StaleResponseError();return false;}
   return true;
  };
  try{
   const library=controller.state.capabilities?.library_id,owner=controller.state.session?.principal_id;
   if(!library||!owner)return;
   const unresolved:string[]=[];let failedReads=false;
   for(const row of pendingLifecycleJournals(localStorage,library,owner)){
    try{
     const receipt=await reader.operation(row.intent.body.plan_id);
     if(!canAdopt())return;
     const result=lifecycleResultFromReceipt(row.intent,receipt);
     if(!result){unresolved.push(`${row.intent.body.plan_id}: ${receipt.state}`);continue;}
     if(result.document_id!==row.documentId)throw Error("원래 요청의 문서가 일치하지 않습니다.");
     await finishLifecycleRequest(controller,row.key,row.intent,result,()=>{if(!canAdopt())throw new StaleResponseError();});
    }catch(e){if(e instanceof StaleResponseError)throw e;failedReads=true;unresolved.push(`${row.intent.body.plan_id}: ${String(e)}`);}
   }
   if(!canAdopt())return;
   setPending(unresolved);
   const rows=await lookupPurgeJobs(reader,rememberedPurgeJobs(localStorage,key(controller)));
   if(!canAdopt())return;
   const current=rememberedPurgeJobs(localStorage,key(controller)),visible=rows.filter(row=>current.includes(row.id));
   setSnapshot({rows:visible,authority});setIssue("");
   if(resync&&(failedReads||visible.some(row=>row.kind==="retry"))){
    throw Error("영구 삭제 또는 수명주기 작업 상태를 아직 확인하지 못했습니다.");
   }
   if(purgeJobsNeedPoll(visible)||current.some(id=>!visible.some(row=>row.id===id))){
    timerRef.current=setTimeout(()=>void runLoad(true),2000);
   }
  }catch(e){
   if(isCurrent()){setSnapshot(null);setIssue(String(e));}
   if(resync)throw e;
  }
 },[controller]);
 useEffect(()=>{void runLoad(false);return()=>{seqRef.current+=1;if(timerRef.current){clearTimeout(timerRef.current);timerRef.current=undefined;}};},[runLoad,refresh,retry]);
 useReviewInvalidation(controller,true,async(ev)=>{await runLoad(true,Boolean(ev?.resync),ev?.readGeneration);});
 return <section aria-label="영구 삭제 작업">{pending.length>0&&<div><p>원래 수명주기 요청의 결과를 확인할 때까지 기록을 보존합니다.</p><ul>{pending.map(value=><li key={value}>{value}</li>)}</ul><button onClick={()=>setRetry(n=>n+1)}>원래 요청 결과 확인</button></div>}{issue&&<p role="alert">{issue}</p>}{snapshot?.rows.map(row=>{if(row.kind!=="job")return <div key={row.id} data-purge-job={row.id}><p role="status">{row.kind==="unavailable"?"영구 삭제 작업: 이 계정 또는 서비스에서 더 이상 조회할 수 없습니다.":"영구 삭제 작업: 조회하지 못했습니다. 자동으로 다시 확인합니다."}</p>{canDismissPurgeJob(row)&&<button data-purge-dismiss onClick={()=>void dismiss(row)}>작업 기록 닫기</button>}</div>;const job=row.job;const result=job.result as {reason?:string;remaining?:number;metadata_removed?:boolean;next_retry_at?:string}|undefined;return <div key={row.id} data-purge-job={row.id}><p role="status">영구 삭제: {job.state==="completed"?"영구 삭제 정리가 완료되었습니다.":job.state==="failed"?"정리가 중단되었습니다. 기록을 확인하세요.":result?.reason==="protected"?"다른 버전·초안·소스·업로드·작업·백업·읽기에서 보호 중인 파일을 보존합니다.":result?.reason==="io_retry"?"저장 장치 오류 후 자동으로 다시 확인합니다.":result?.reason==="guard_blocked"?"원래 승인 조건이 달라져 정리를 보류합니다.":"영구 삭제가 승인되었습니다. 취소와 복원은 불가능하며 정리 중입니다."}{result?.metadata_removed&&job.state!=="completed"?" 개인 기록 정리는 끝났으며 보호 중인 바이트는 아직 삭제되지 않았습니다.":""}{typeof result?.remaining==="number"?` 남은 객체: ${result.remaining}.`:""}{result?.next_retry_at?` 다음 확인: ${new Date(result.next_retry_at).toLocaleString()}`:""}</p>{canDismissPurgeJob(row)&&<button data-purge-dismiss onClick={()=>void dismiss(row)}>작업 기록 닫기</button>}</div>;})}</section>;
}
