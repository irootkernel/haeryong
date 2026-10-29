import {backupLine} from "./backup-state.ts";
import {useEffect,useRef,useState} from "react";
import type {ShellController} from "../../client/controller.ts";
import type {LifecycleAction,LifecyclePlanView,LifecycleResult} from "../../client/types.ts";
import {ApiError} from "../../client/types.ts";
import {Sheet} from "../sheet.tsx";
import {lifecycleJournalKey,lifecycleRejected,reconcileLifecycle,updateLifecycleJournal,type LifecycleIntent} from "./lifecycle-operations.ts";
import {finishLifecycleRequest} from "./purge-progress.tsx";
const impactLabels:Record<string,string>={annotation_value:"노트 내용 식별",annotation_revision:"노트 이력 식별",context_object:"저장한 상황 객체",source_history:"소스 검증 이력",managed_source_snapshot:"보관한 소스 스냅샷",version:"버전",review:"내 검토",review_event:"검토 이력",annotation:"노트",annotation_history:"노트 이력",context:"저장한 상황",revisit:"다시 볼 문서",reading_position:"마지막 읽은 위치",page_position:"페이지 위치",reader_preference:"읽기 설정",incoming:"들어오는 참조",incoming_edges:"확인된 참조",referring_documents:"참조하는 문서"};
export const stateLabels:Record<string,string>={active:"활성",archived:"보관",trash:"휴지통",purged:"영구 삭제"};
const operationEffects:Record<LifecycleAction,string>={
 purge:"승인 즉시 복원과 취소가 불가능합니다. 버전과 노트·내 검토·개인 이력 및 저장한 상황을 영구 삭제합니다. 들어오는 참조는 최소 식별자만 남깁니다. 원본 저장소와 다른 버전·초안·소스·업로드·작업·백업·읽기에서 사용하는 공유 파일은 보존됩니다.",
 archive:"문서와 버전, 개인 기록은 보관 필터에서 계속 읽을 수 있습니다. 원본과 개인 기록은 보존되며 파일과 링크도 계속 사용할 수 있습니다. 다른 문서에서 들어오는 참조의 접근 상태는 유지됩니다.",
 trash:"복원 전까지 문서와 버전, 노트·내 검토·다시 볼 문서·이력은 일반 화면과 검색에서 숨겨지고 파일과 링크로도 열 수 없습니다. 다른 문서에서 이 문서를 가리키는 참조는 복원 전까지 사용할 수 없음으로 표시됩니다. 원본과 개인 기록은 보존됩니다.",
 restore:"문서와 버전, 노트·내 검토·다시 볼 문서·이력이 원래 위치에서 같은 버전과 기록으로 복원됩니다. 파일과 링크, 다른 문서에서 들어오는 참조도 이전 접근 상태로 돌아옵니다. 미해결 참조는 자동으로 연결되지 않으며 원본과 개인 기록은 그대로 유지됩니다.",
};
const recordEffects:Record<LifecycleAction,string>={purge:"영구 삭제 · 보호 중인 공유 바이트 제외",archive:"보관 필터에서 읽기 · 보존",trash:"복원 전까지 숨김 · 보존",restore:"같은 버전·기록으로 복원"};
const labels={purge:"영구 삭제",archive:"보관",trash:"휴지통으로 이동",restore:"복원"};
export function LifecycleControl({controller,documentId,lifecycle,onChanged}:{controller:ShellController;documentId:string;lifecycle:string;onChanged:(result:LifecycleResult)=>void}){
 const [open,setOpen]=useState(false),[plan,setPlan]=useState<LifecyclePlanView|null>(null),[intent,setIntent]=useState<LifecycleIntent|null>(null),[busy,setBusy]=useState(false),[issue,setIssue]=useState(""),[action,setAction]=useState<LifecycleAction>("trash"),[rejected,setRejected]=useState(false);
 useEffect(()=>{if(open&&!busy)document.getElementById("lifecycle-dialog-"+documentId)?.querySelector<HTMLButtonElement>("button")?.focus();},[open,busy,documentId]);
 const active=useRef(true);useEffect(()=>()=>{active.current=false;},[]);
 const purgeEnabled=controller.state.capabilities?.features.purge===true;
 const enabled=controller.state.capabilities?.features.lifecycle===true&&controller.state.session?.scope.kind==="owner";
 const storageKey=lifecycleJournalKey(controller.state.capabilities?.library_id,controller.state.session?.principal_id,documentId);
 const generation=controller.state.capabilities?.runtime_generation??"";
 const persist=async(expected:LifecycleIntent|null,value:LifecycleIntent|null)=>{if(!navigator.locks)throw Error("수명주기 요청 저장 잠금을 사용할 수 없습니다.");await navigator.locks.request(storageKey,()=>updateLifecycleJournal(localStorage,storageKey,expected,value));setIntent(value);};
 useEffect(()=>{try{const raw=localStorage.getItem(storageKey);if(raw){const saved=JSON.parse(raw) as LifecycleIntent;if(typeof saved.body?.plan_id!=="string"||saved.body.confirmed!==true||!Number.isInteger(saved.body.expected_revision)||typeof saved.generation!=="string")throw Error("수명주기 요청 복구 기록을 확인할 수 없습니다.");setIntent(saved);setAction(saved.operation);setOpen(true);}}catch(e){setIssue(String(e));}},[storageKey]);
 const guardFor=()=>{const authority=controller.captureDispatchAuthority();return()=>{controller.requireVerifiedDispatch(authority);if(!active.current)throw Error("이전 수명주기 화면입니다.");};};
 const prepare=async(next:LifecycleAction)=>{
  const guard=guardFor();setAction(next);setOpen(true);setIssue("");setBusy(true);setPlan(null);
  try{guard();let revision:number;if(lifecycle==="trash"){let cursor:string|undefined;let found=false;revision=0;do{const page=await controller.client.trashList({cursor});guard();const item=page.items.find(d=>d.document_id===documentId);if(item){revision=item.revision;found=true;break;}cursor=page.next_cursor??undefined;}while(cursor);if(!found)throw Error("휴지통 상태가 바뀌었습니다. 목록을 새로 고치세요.");}else{revision=(await controller.client.documentDetail(documentId)).revision;guard();}
   const preview=await controller.client.lifecyclePlan(documentId,{operation:next,expected_revision:revision});guard();setPlan(preview);setRejected(false);
  }catch(e){try{guard();setIssue(String(e));}catch{}}finally{if(active.current)setBusy(false);}
 };
 const finish=async(result:LifecycleResult,request:LifecycleIntent,guard:()=>void)=>{await finishLifecycleRequest(controller,storageKey,request,result,guard);guard();setIntent(null);setPlan(null);setOpen(false);onChanged(result);};
 const commit=async(retry=false)=>{
  if(busy||(!plan&&!intent)||(!retry&&plan?.state!=="open"))return;const guard=guardFor();const request=retry?intent!:{body:{plan_id:plan!.plan_id,expected_revision:plan!.expected_revision,confirmed:true as const},generation,operation:plan!.operation};
  setBusy(true);setIssue("");try{guard();if(request.generation!==generation)throw Error("서비스 세대가 바뀌었습니다. 원래 요청 결과를 확인하세요.");if(!retry)await persist(null,request);guard();const result=await controller.client.lifecyclePlanCommit(request.body.plan_id,request.body);guard();await finish(result,request,guard);}catch(e){try{guard();setIssue(String(e));if(lifecycleRejected(e))setRejected(true);}catch{}}finally{if(active.current)setBusy(false);}
 };
 const recover=async()=>{const guard=guardFor();setBusy(true);setIssue("");try{const result=await reconcileLifecycle(controller.client,intent!);guard();await finish(result,intent!,guard);}catch(e){try{guard();setIssue(e instanceof ApiError&&e.status===404?"원래 receipt가 없습니다. 같은 서비스 세대에서만 동일 계획으로 재시도할 수 있습니다.":String(e));}catch{}}finally{if(active.current)setBusy(false);}};
 const more=async()=>{if(!plan?.next_cursor||busy)return;const guard=guardFor();setBusy(true);try{const page=await controller.client.lifecyclePlanGet(plan.plan_id,{cursor:plan.next_cursor});guard();setPlan({...page,items:[...plan.items,...page.items]});}catch(e){setIssue(String(e));}finally{setBusy(false);}};
 const incomingEffect=plan?.operation==="purge"?"본문·제목 없이 삭제된 대상의 식별자만 유지":plan?.operation==="trash"?"복원 전까지 사용할 수 없음":plan?.from_lifecycle==="trash"?"이전 접근 상태로 복원":"참조 대상의 접근 상태 유지";
 if(!enabled)return null;
 return <section className="lifecycle-control"><details><summary id={"lifecycle-more-"+documentId}>더 보기</summary>{lifecycle==="trash"?<><button disabled={busy||!!intent} onClick={()=>void prepare("restore")}>복원</button>{purgeEnabled&&<button disabled={busy||!!intent} onClick={()=>void prepare("purge")}>영구 삭제</button>}</>:<><button disabled={busy||!!intent} onClick={()=>void prepare(lifecycle==="archived"?"restore":"archive")}>{lifecycle==="archived"?"보관 해제":"보관"}</button><button disabled={busy||!!intent} onClick={()=>void prepare("trash")}>휴지통으로 이동</button></>}</details>{intent&&!open&&<button onClick={()=>setOpen(true)}>원래 수명주기 요청 확인</button>}
 <Sheet open={open} modal label="수명주기 영향 확인" id={"lifecycle-dialog-"+documentId} openerId={"lifecycle-more-"+documentId} onClose={()=>{if(!busy)setOpen(false);}} className="lifecycle-dialog">
 <button autoFocus disabled={busy} onClick={()=>setOpen(false)}>닫기</button><h2>{plan?labels[plan.operation]:labels[action]} 영향 확인</h2>{busy&&<p role="status">확인 중…</p>}{issue&&<p role="alert">{issue}</p>}
 {plan&&<><p>{stateLabels[plan.from_lifecycle]} → {stateLabels[plan.to_lifecycle]} · 만료 {new Date(plan.expires_at).toLocaleString()}</p><p data-lifecycle-effects>{operationEffects[plan.operation]}</p>{plan.operation==="restore"&&plan.to_lifecycle==="archived"&&<p>복원 후에도 보관 필터에서 읽을 수 있습니다.</p>}{plan.operation==="purge"?<><p data-purge-backup>{backupLine(plan.backup)} 새 백업 생성은 삭제의 필수 조건이 아닙니다.</p><p>과거 인용·백업·내보내기·제공자 기록·디스크 기록에는 사본이 남을 수 있습니다. 질문·답변은 아직 제공되지 않습니다. 읽기 위치와 개인 이력도 삭제 대상입니다.</p></>:<p>읽기 위치는 계획 생성 시점 정보이며 이후 저장도 보존됩니다. 질문·답변은 아직 제공되지 않습니다.</p>}<dl>{Object.entries(plan.impact).map(([kind,count])=><div key={kind}><dt>{impactLabels[kind]??kind}</dt><dd>{count}</dd></div>)}</dl><ul>{plan.items.map(i=><li key={JSON.stringify([i.kind,i.id,i.version_id,i.page_path])}><details><summary>{impactLabels[i.kind]??i.kind}{i.page_path?" · "+i.page_path:""} · {i.kind==="incoming"?`${i.resolution==="unresolved"?"미해결 참조 · ":""}${incomingEffect}`:recordEffects[plan.operation]}</summary><p className="muted-code">{i.id}</p></details></li>)}</ul>{plan.next_cursor&&<button disabled={busy} onClick={()=>void more()}>영향 목록 더 보기</button>}
 {!intent&&<button id={"lifecycle-confirm-"+documentId} data-lifecycle-confirm="true" disabled={busy||!!plan.next_cursor||plan.state!=="open"} onClick={()=>void commit()}>이 계획으로 {labels[plan.operation]} 확인</button>}</>}
 {intent&&!rejected&&<><p>원래 요청의 결과를 확인할 때까지 새 작업을 시작하지 않습니다.</p><button disabled={busy} onClick={()=>void recover()}>원래 요청 결과 확인</button><button disabled={busy||intent.generation!==generation} onClick={()=>void commit(true)}>동일 계획으로 재시도</button></>}
 {rejected&&<button disabled={busy} onClick={()=>{void persist(intent,null).then(()=>prepare(action)).catch(e=>setIssue(String(e)));}}>변경된 상태에서 영향 다시 확인</button>}{!intent&&!plan&&!busy&&<button onClick={()=>void prepare(action)}>영향 다시 확인</button>}
 </Sheet></section>;
}
