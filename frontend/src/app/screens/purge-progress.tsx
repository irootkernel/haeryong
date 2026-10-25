import {useEffect,useRef,useState} from "react";
import type {ShellController} from "../../client/controller.ts";
import {finishLifecycleJournal,pendingLifecycleJournals,reconcileLifecycle} from "./lifecycle-operations.ts";
import type {LifecycleIntent} from "./lifecycle-operations.ts";
import type {LifecycleResult} from "../../client/types.ts";
import {canDismissPurgeJob,dismissPurgeJob,lookupPurgeJobs,purgeJobsKey,purgeJobsNeedPoll,rememberedPurgeJobs,rememberPurgeJobId,type PurgeJobRow} from "./purge-jobs.ts";
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
 const mounted=useRef(true);useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;};},[]);
 const dismiss=async(row:PurgeJobRow)=>{if(!snapshot)return;const authority=snapshot.authority,storageKey=key(controller);try{
  controller.requireVerifiedDispatch(authority);
  if(!navigator.locks)throw Error("삭제 작업 저장 잠금을 사용할 수 없습니다.");
  await navigator.locks.request(storageKey,()=>{controller.requireVerifiedDispatch(authority);if(!mounted.current)throw Error("이전 삭제 작업 화면입니다.");dismissPurgeJob(localStorage,storageKey,row);});
  if(mounted.current)setRetry(n=>n+1);
 }catch(e){if(mounted.current)setIssue(String(e));}};
 useEffect(()=>{let active=true,timer:ReturnType<typeof setTimeout>|undefined;const authority=controller.captureDispatchAuthority();
 const load=async()=>{try{
 const library=controller.state.capabilities?.library_id,owner=controller.state.session?.principal_id;
 if(!library||!owner)return;
 const unresolved:string[]=[];
 for(const row of pendingLifecycleJournals(localStorage,library,owner)){
  try{
   const result=await reconcileLifecycle(controller.client,row.intent);controller.requireVerifiedDispatch(authority);if(!active)return;
   if(result.document_id!==row.documentId)throw Error("원래 요청의 문서가 일치하지 않습니다.");
   await finishLifecycleRequest(controller,row.key,row.intent,result,()=>{controller.requireVerifiedDispatch(authority);if(!active)throw Error("이전 복구 화면입니다.");});
  }catch(e){unresolved.push(`${row.intent.body.plan_id}: ${String(e)}`);}
 }
 controller.requireVerifiedDispatch(authority);if(!active)return;setPending(unresolved);
 const rows=await lookupPurgeJobs(controller.client,rememberedPurgeJobs(localStorage,key(controller)));controller.requireVerifiedDispatch(authority);
 if(active){const current=rememberedPurgeJobs(localStorage,key(controller)),visible=rows.filter(row=>current.includes(row.id));setSnapshot({rows:visible,authority});setIssue("");if(purgeJobsNeedPoll(visible)||current.some(id=>!visible.some(row=>row.id===id)))timer=setTimeout(()=>void load(),2000);}
 }catch(e){if(active){setSnapshot(null);setIssue(String(e));}}};
 void load();return()=>{active=false;if(timer)clearTimeout(timer);};},[controller,refresh,retry]);
 return <section aria-label="영구 삭제 작업">{pending.length>0&&<div><p>원래 수명주기 요청의 결과를 확인할 때까지 기록을 보존합니다.</p><ul>{pending.map(value=><li key={value}>{value}</li>)}</ul><button onClick={()=>setRetry(n=>n+1)}>원래 요청 결과 확인</button></div>}{issue&&<p role="alert">{issue}</p>}{snapshot?.rows.map(row=>{if(row.kind!=="job")return <div key={row.id} data-purge-job={row.id}><p role="status">{row.kind==="unavailable"?"영구 삭제 작업: 이 계정 또는 서비스에서 더 이상 조회할 수 없습니다.":"영구 삭제 작업: 조회하지 못했습니다. 자동으로 다시 확인합니다."}</p>{canDismissPurgeJob(row)&&<button data-purge-dismiss onClick={()=>void dismiss(row)}>작업 기록 닫기</button>}</div>;const job=row.job;const result=job.result as {reason?:string;remaining?:number;metadata_removed?:boolean;next_retry_at?:string}|undefined;return <div key={row.id} data-purge-job={row.id}><p role="status">영구 삭제: {job.state==="completed"?"영구 삭제 정리가 완료되었습니다.":job.state==="failed"?"정리가 중단되었습니다. 기록을 확인하세요.":result?.reason==="protected"?"다른 버전·초안·소스·업로드·작업·백업·읽기에서 보호 중인 파일을 보존합니다.":result?.reason==="io_retry"?"저장 장치 오류 후 자동으로 다시 확인합니다.":result?.reason==="guard_blocked"?"원래 승인 조건이 달라져 정리를 보류합니다.":"영구 삭제가 승인되었습니다. 취소와 복원은 불가능하며 정리 중입니다."}{result?.metadata_removed&&job.state!=="completed"?" 개인 기록 정리는 끝났으며 보호 중인 바이트는 아직 삭제되지 않았습니다.":""}{typeof result?.remaining==="number"?` 남은 객체: ${result.remaining}.`:""}{result?.next_retry_at?` 다음 확인: ${new Date(result.next_retry_at).toLocaleString()}`:""}</p>{canDismissPurgeJob(row)&&<button data-purge-dismiss onClick={()=>void dismiss(row)}>작업 기록 닫기</button>}</div>;})}</section>;
}
