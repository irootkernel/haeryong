import {StaleResponseError} from "../../client/types.ts";
import { useEffect, useRef, useState } from "react";
import type { ShellController } from "../../client/controller.ts";
import type { DraftView, ProjectView, DocumentView } from "../../client/types.ts";
import { ApiError } from "../../client/types.ts";
import { recoverDraftAction, dispatchDraftAction, acknowledgeDraftRefusal, captureDraftAuthority, DraftActionOwner, draftRefusalText } from "../../client/draft-actions.ts";
import type { PendingDraft, DraftActionControl } from "../../client/draft-actions.ts";
import { BrowserJournalStore, TransferEngine, journalAuthority, journalKey, planFromArchive, planFromFolder } from "../../client/transfer.ts";
import type { PlannedInput } from "../../client/transfer.ts";
import { planFromNativeSource, verifyNativeSourceStable } from "../../client/native-fs.ts";
import type { NativeFsClient } from "../../client/native-fs.ts";
import { navigate } from "../router.tsx";
import { refreshDraftMountedState } from "./generation-context.ts";
import { useReviewInvalidation } from "./review-refresh.ts";

const text=(error:unknown)=>error instanceof ApiError&&(error.status===412||error.status===409&&error.code==="VERSION_CONFLICT")?draftRefusalText(error):error instanceof Error?error.message:String(error);
const stateLabel:Record<string,string>={preparing:"생성 중",ready:"검토 가능한 초안",published:"발행됨",rejected:"거부됨",failed:"생성 실패"};

export function DraftControl({controller,projects}:{controller:ShellController;projects:ProjectView[]}) {
 const client=controller.client,library=controller.state.capabilities!.library_id,principal=controller.state.session!.principal_id;
 const canWrite=controller.state.phase==="verified"&&controller.state.session?.scope.kind==="owner"&&controller.state.capabilities?.features.generation===true;
 const prefix=`haeryong.draft-action.v1:${library}:${principal}:`;
 const [store]=useState(()=>new BrowserJournalStore("haeryong.draft-uploads.v1"));
 const [engine]=useState(()=>new TransferEngine(client,store));
 const [project,setProject]=useState(""),[rows,setRows]=useState<DraftView[]>([]),[cursor,setCursor]=useState<string|null>(null),[selected,setSelected]=useState<DraftView|null>(null);
 const [history,setHistory]=useState<DraftView[]>([]),[historyCursor,setHistoryCursor]=useState<string|null>(null),[historyLoaded,setHistoryLoaded]=useState(false);
 const reads=useRef({labels:0,project:0});
 const labelAuthority=controller.captureDispatchAuthority();
 const labelAuthorityKey=labelAuthority===null?"":JSON.stringify(labelAuthority);
 const documentIds=JSON.stringify([...new Set([...rows.map(row=>row.document_id),selected?.document_id].filter((id):id is string=>!!id))].sort());
 const [documentLabels,setDocumentLabels]=useState<{authority:string;items:Record<string,DocumentView|null>}>({authority:"",items:{}});
 useEffect(()=>{
  let live=true;setDocumentLabels({authority:labelAuthorityKey,items:{}});
  if(labelAuthority===null)return()=>{live=false;};
  const readGen=controller.readGeneration,read=++reads.current.labels;
  const ids=JSON.parse(documentIds) as string[];
  void Promise.allSettled(ids.map(async id=>({id,detail:await controller.passiveClient.documentDetail(id)}))).then(results=>{
   if(!live||read!==reads.current.labels||!controller.isCurrentReadAuthority(labelAuthority,readGen))return;
   const items:Record<string,DocumentView|null>={};
   results.forEach((result,index)=>{items[ids[index]!]=result.status==="fulfilled"?result.value.detail:null;});
   setDocumentLabels({authority:labelAuthorityKey,items});
  });
  return()=>{live=false;};
 },[controller,documentIds,labelAuthorityKey]);
 const projectLabel=(row:DraftView)=>projects.find(p=>p.project_id===row.project_id)?.name??"프로젝트 이름 확인 불가";
 const documentLabel=(row:DraftView)=>{
  if(row.document_id===null)return "새 문서";
  const detail=documentLabels.authority===labelAuthorityKey?documentLabels.items[row.document_id]:undefined;
  return detail&&detail.document_id===row.document_id&&detail.project_id===row.project_id?detail.title:detail===undefined?"문서 제목 읽는 중":"문서 제목 확인 불가";
 };
 const baseLabel=(row:DraftView)=>row.base_version_id??"없음 · 새 문서 기준";
 const [pending,setPending]=useState<PendingDraft|null>(null),[input,setInput]=useState<PlannedInput|null>(null);
 const [busy,setBusy]=useState(false),[error,setError]=useState<string|null>(null),[notice,setNotice]=useState("");
 const [actionOwner]=useState(()=>new DraftActionOwner());
 const alive=useRef(true),handle=useRef<string|null>(null),busyRef=useRef(false),actionSeq=useRef(0);
 const projectRef=useRef(project);projectRef.current=project;
 const selectedRef=useRef(selected);selectedRef.current=selected;
 const historyLoadedRef=useRef(historyLoaded);historyLoadedRef.current=historyLoaded;
 const native=typeof (client as unknown as Partial<NativeFsClient>).chooseImportSource==="function"?client as unknown as NativeFsClient:null;
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;actionOwner.stop();if(native&&handle.current)void native.releaseSource(handle.current).catch(()=>{});};},[native,actionOwner]);
 useEffect(()=>{
  let live=true;actionSeq.current+=1;controller.bumpReadGeneration();
  const auth=controller.captureDispatchAuthority(),readGen=controller.readGeneration,act=actionSeq.current,read=++reads.current.project;
  setRows([]);setCursor(null);setError(null);
  void client.draftList(project||undefined).then(page=>{if(live&&read===reads.current.project&&actionSeq.current===act&&controller.isCurrentReadAuthority(auth,readGen)){setRows(page.items);setCursor(page.next_cursor);}}).catch(e=>{if(live&&read===reads.current.project&&actionSeq.current===act&&controller.isCurrentReadAuthority(auth,readGen))setError(text(e));});
  return()=>{live=false;};
 },[client,controller,project]);
 const read=(id:string):PendingDraft|null=>{const raw=localStorage.getItem(prefix+id);if(raw===null)return null;const record=JSON.parse(raw) as PendingDraft;if(record.draftId!==id || !record.key || !record.etag)throw new Error("초안 복구 기록을 확인해 주세요.");return record;};
 const save=(record:PendingDraft)=>{localStorage.setItem(prefix+record.draftId,JSON.stringify(record));if(alive.current)setPending(structuredClone(record));};
 useReviewInvalidation(
  controller,
  true,
  async(ev)=>{
   const auth=controller.captureDispatchAuthority();
   if(auth===null)return;
   const readGen=ev?.readGeneration??controller.readGeneration,act=actionSeq.current,authKey=JSON.stringify(auth);
   const read={labels:++reads.current.labels,project:++reads.current.project};
   try{
    const applied=await refreshDraftMountedState({
     passiveClient:controller.passiveClient,
     project:projectRef.current,
     selectedId:selectedRef.current?.draft_id??null,
     includeHistory:historyLoadedRef.current,
     canApply:()=>alive.current&&!busyRef.current&&actionSeq.current===act&&read.labels===reads.current.labels&&read.project===reads.current.project&&controller.isCurrentReadAuthority(auth,readGen),
     isCurrentProject:(pid)=>projectRef.current===pid,
     isCurrentSelected:(did)=>(selectedRef.current?.draft_id??null)===did,
     isHistoryOpen:()=>historyLoadedRef.current,
     applyList:(items,nextCursor)=>{
      setRows(items);
      setCursor(nextCursor);
     },
     applySelected:(draft)=>{
      setSelected(draft);
     },
     applyHistory:(items,nextCursor)=>{
      setHistory(items);
      setHistoryCursor(nextCursor);
     },
     applyDocumentLabels:(items)=>{
      setDocumentLabels({authority:authKey,items});
     },
    });
    if(!applied&&ev?.resync&&alive.current)throw new StaleResponseError();
   }catch(e){
    if(alive.current&&!busyRef.current&&actionSeq.current===act&&controller.isCurrentReadAuthority(auth,readGen)){
     setError(text(e));
    }
    if(ev?.resync&&alive.current)throw e;
   }
  },
  ()=>busyRef.current,
 );
 const act=async(action:(control:DraftActionControl)=>Promise<void>)=>{
  if(busyRef.current)return;
  // A new explicit action starts only after the previous busy promise settles.
  // Its fresh control never clears the prior action's stopped state.
  actionSeq.current+=1;controller.bumpReadGeneration();
  const control=actionOwner.begin();busyRef.current=true;setBusy(true);setError(null);
  try{await action(control);}catch(e){if(alive.current)setError(text(e));}
  finally{busyRef.current=false;if(alive.current)setBusy(false);}
 };
 const guard=(control:DraftActionControl)=>{if(!alive.current)throw new Error("이전 초안 화면입니다.");actionOwner.require(control);controller.requireVerifiedDispatch();};
 const select=async(row:DraftView,control:DraftActionControl)=>{
  const current=await client.draftGet(row.draft_id);guard(control);
  setSelected(current);setHistory([]);setHistoryCursor(null);setHistoryLoaded(false);setInput(null);setPending(read(current.draft_id));
  if(native&&handle.current){await native.releaseSource(handle.current);handle.current=null;}
 };
 const chooseNative=async(control:DraftActionControl)=>{
  if(!native)return;const planned=await planFromNativeSource(native,"directory");
  if("canceled" in planned)return;
  try{guard(control);}catch(error){await native.releaseSource(planned.handle);throw error;}
  if(handle.current)await native.releaseSource(handle.current);
  try{guard(control);}catch(error){await native.releaseSource(planned.handle);throw error;}
  handle.current=planned.handle;setInput(planned.input);
 };
 const run=async(control:DraftActionControl,action:PendingDraft["action"],recover=false)=>{
  if(!selected)return;guard(control);
  const authority=captureDraftAuthority(controller);const currentGuard=()=>{guard(control);controller.requireVerifiedDispatch(authority);};
  if(!navigator.locks?.request)throw new Error("이 환경은 안전한 초안 작업 잠금을 지원하지 않습니다. CLI를 이용해 주세요.");
  await navigator.locks.request(prefix+selected.draft_id,async()=>{
   currentGuard();let record=read(selected.draft_id);
   if(record&&!recover)throw new Error("기존 초안 작업 결과를 먼저 확인해 주세요.");
   if(!record){record={key:crypto.randomUUID(),draftId:selected.draft_id,action,etag:selected.etag,dispatchReady:false,runtimeGeneration:authority.runtimeGeneration};save(record);}
   const original=record;
   if(await recoverDraftAction(client,original,authority.runtimeGeneration,save,currentGuard)){await finish(original,currentGuard);return;}
   if(original.action==="replace"&&!original.body){
    if(!input)throw new Error("원래 선택한 결과 폴더 또는 ZIP을 다시 선택해 주세요.");
    if(input.mode==="object")throw new Error("완전한 결과 디렉터리를 선택해 주세요.");
    if(!original.upload){
     original.upload=await engine.beginOwnedUpload(library,principal,{purpose:"result",input_mode:input.mode,selection_name:input.selectionName,target:{draft_id:original.draftId,project_id:selected.project_id,document_id:selected.document_id}},input,{guard:currentGuard});save(original);
    }
    const retained=store.load(journalAuthority(library,principal)).records.find(row=>journalKey(row)===journalKey(original.upload!));
    if(!retained || JSON.stringify(retained.uploadRequest)!==JSON.stringify(original.upload.uploadRequest))throw new Error("원래 업로드 복구 기록이 없습니다.");
    original.upload=retained;
    const callbacks={guard:currentGuard,shouldContinue:()=>alive.current&&actionOwner.continues(control),verifyInputStable:native&&handle.current?()=>verifyNativeSourceStable(native,handle.current!):undefined};
    await engine.attachUpload(retained,callbacks);
    if(["completing","sealing","sealed"].includes(retained.phase))await engine.complete(retained,callbacks);else await engine.resume(retained,input,callbacks);
    for(;;){
     currentGuard();const detail=await client.uploadDetail(retained.uploadId!);
     if(detail.state==="sealed"&&detail.reusable)break;
     if(detail.state!=="sealing")throw new Error(detail.error??"업로드 봉인 상태를 확인해 주세요.");
     await new Promise(resolve=>setTimeout(resolve,200));
    }
    original.body={action:"replace",upload_id:retained.uploadId!};save(original);
   }
   if(original.action==="reject")original.body={action:"reject"};
   await dispatchDraftAction(client,original,authority.runtimeGeneration,save,currentGuard);
   await finish(original,currentGuard);
  });
 };
 const acknowledgeRefusal=async(control:DraftActionControl)=>{
  if(!selected)return;guard(control);
  const authority=captureDraftAuthority(controller);
  await navigator.locks.request(prefix+selected.draft_id,async()=>{
   guard(control);controller.requireVerifiedDispatch(authority);
   const record=read(selected.draft_id);
   if(!record?.refused || record.key!==pending?.key)throw new Error("확정된 원래 거부를 먼저 확인해 주세요.");
   const current=await client.draftGet(record.draftId);guard(control);controller.requireVerifiedDispatch(authority);
   // Retain the old exact key/body/validator as history before releasing the
   // active slot. This explicit decision does not rebase or retry that request.
   acknowledgeDraftRefusal(localStorage,prefix,record);setPending(null);setSelected(current);setInput(null);
   setRows(old=>old.map(row=>row.draft_id===current.draft_id?current:row));
   setNotice("원래 거부를 확인했습니다. 현재 초안에서 새 동작을 선택하세요. 기준 변경은 새 연결 생성이 필요합니다.");
  });
 };
 const finish=async(record:PendingDraft,currentGuard:()=>void)=>{
  // Clear only after a durable receipt and current read both succeed. A lost
  // response/reload preserves its exact K and the validator the owner selected.
  const current=await client.draftGet(record.draftId);currentGuard();
  localStorage.removeItem(prefix+record.draftId);setPending(null);setSelected(current);setRows(old=>old.map(row=>row.draft_id===current.draft_id?current:row));setNotice(`${stateLabel[current.state]} · 원래 작업 결과 확인됨`);
 };
 return <section aria-label="초안 관리" aria-busy={busy}>
  <h3>초안 관리</h3><p>생성과 교체는 개인 Review를 바꾸지 않습니다. 발행은 초안에 저장된 원래 문서 기준을 사용합니다.</p>
  <label className="field"><span>초안 프로젝트</span><select value={project} disabled={busy} onChange={e=>{actionSeq.current+=1;setProject(e.target.value);setSelected(null);setHistory([]);setHistoryCursor(null);setHistoryLoaded(false);setPending(null);}}><option value="">전체</option>{projects.map(p=><option key={p.project_id} value={p.project_id}>{p.name}</option>)}</select></label>
  {error?<p role="alert">{error}</p>:null}<p role="status" aria-live="polite">{notice}</p>
  <button type="button" disabled={busy} onClick={()=>void act(async()=>{const page=await client.draftList(project||undefined);setRows(page.items);setCursor(page.next_cursor);})}>초안 새로고침</button>
  <ul>{rows.map(row=><li key={row.draft_id}><button className="draft-choice" type="button" disabled={busy} onClick={()=>void act(control=>select(row,control))}><strong>{projectLabel(row)} · {documentLabel(row)}</strong><br/>{row.document_id??"새 문서 생성"}<br/>초안 {row.draft_id} · 기준 {baseLabel(row)}<br/>{stateLabel[row.state]} · {new Date(row.created_at).toLocaleString()}</button></li>)}</ul>
  {cursor?<button type="button" disabled={busy} onClick={()=>void act(async()=>{const page=await client.draftList(project||undefined,cursor);setRows(old=>[...old,...page.items]);setCursor(page.next_cursor);})}>초안 더 보기</button>:null}
  {selected?<section aria-label="선택한 초안"><h4>{stateLabel[selected.state]}</h4>
   <dl className="draft-target" aria-label="초안 대상과 저장된 기준">
    <dt>프로젝트</dt><dd>{projectLabel(selected)} · {selected.project_id}</dd>
    <dt>대상 문서</dt><dd>{documentLabel(selected)} · {selected.document_id??"새 문서 생성"}</dd>
    <dt>저장된 기준 버전</dt><dd>{baseLabel(selected)}</dd>
    <dt>초안</dt><dd>{selected.draft_id} · revision {selected.revision}</dd>
    <dt>요청 의도</dt><dd>{selected.intent==="publish"?"발행 요청":"초안 요청"}</dd>
   </dl><p>문서 제목은 현재 표시 이름입니다. 저장된 기준 버전은 바뀌지 않습니다.</p><p>{selected.failure_reason}</p>
   {selected.published_version_id?<button type="button" disabled={busy} onClick={()=>void act(async control=>{const version=await client.versionDetail(selected.published_version_id!);guard(control);navigate(`/documents/${version.document_id}/versions/${version.version_id}`);})}>발행 버전 읽기</button>:null}
   <button type="button" disabled={busy} onClick={()=>void act(control=>select(selected,control))}>초안 상태 확인</button>
   <button type="button" disabled={busy} onClick={()=>void act(async()=>{const page=await client.draftHistory(selected.draft_id);setHistoryLoaded(true);setHistory(page.items);setHistoryCursor(page.next_cursor);})}>초안 이력</button>
   <ul>{history.map(row=><li key={row.revision}>#{row.revision} · {stateLabel[row.state]} · {new Date(row.updated_at).toLocaleString()} · 초안 {row.draft_id} · 문서 {row.document_id??"새 문서"} · 기준 {baseLabel(row)}</li>)}</ul>
   {historyCursor?<button type="button" disabled={busy} onClick={()=>void act(async()=>{const page=await client.draftHistory(selected.draft_id,historyCursor);setHistory(old=>[...old,...page.items]);setHistoryCursor(page.next_cursor);})}>이력 더 보기</button>:null}
   {pending?<><p>원래 {pending.action} 작업을 확인해야 합니다. 새 작업을 자동으로 만들지 않습니다.</p>{pending.refused?<><p>{draftRefusalText(pending.refused)}</p><button type="button" disabled={!canWrite||busy} onClick={()=>void act(acknowledgeRefusal)}>거부 확인 · 새 동작 선택</button></>:<button type="button" disabled={!canWrite||busy} onClick={()=>void act(control=>run(control,pending.action,true))}>원래 초안 작업 복구</button>}</>:null}
   {["preparing","ready"].includes(selected.state)?<>
    <button type="button" disabled={!canWrite||busy||pending!==null} onClick={()=>void act(control=>run(control,"reject"))}>초안 거부</button>
    {selected.state==="ready"?<fieldset disabled={!canWrite||busy}><legend>완전한 교체 결과 선택</legend>
     {native?<button type="button" onClick={()=>void act(chooseNative)}>호스트 결과 폴더 선택</button>:<><label className="field"><span>결과 ZIP</span><input type="file" onChange={e=>{const file=e.target.files?.[0];if(file)setInput(planFromArchive(file));}}/></label><label className="field"><span>결과 폴더</span><input type="file" {...{webkitdirectory:""} as object} multiple onChange={e=>{const files=Array.from(e.target.files??[]);if(files.length)setInput(planFromFolder(files,files[0]!.webkitRelativePath.split("/")[0]!));}}/></label></>}
     {input?<p>{input.selectionName} · {input.entries.length}개 항목. {!input.limits.emptyDirectoriesRepresented||!input.limits.symlinksRepresented?"브라우저 폴더 선택은 빈 디렉터리·심볼릭 링크를 표현하지 못합니다. 보존하려면 ZIP을 사용하세요.":"선택한 전체 트리를 전송합니다."}</p>:null}
     <button type="button" disabled={pending!==null||!input} onClick={()=>void act(control=>run(control,"replace"))}>선택한 전체 결과로 교체</button>
    </fieldset>:null}
   </>:null}
   {selected.state==="ready"?<button className="primary" type="button" disabled={!canWrite||busy||pending!==null} onClick={()=>void act(control=>run(control,"publish"))}>저장된 기준으로 발행</button>:null}
   {busy?<button type="button" onClick={()=>{actionOwner.stop();}}>전송 중지 · 원래 작업 유지</button>:null}
  </section>:null}
 </section>;
}
