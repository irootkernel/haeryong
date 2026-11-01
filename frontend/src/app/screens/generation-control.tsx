import { useEffect, useRef, useState } from "react";
import type { ShellController } from "../../client/controller.ts";
import type { DocumentView, GenerationReference, GenerationRequest, JobView, ProjectSourceView, ProjectView, SearchHit, RetainedSourceRead } from "../../client/types.ts";
import { BrowserGenerationJournal, GenerationSubmission, recoverGenerationResume, dispatchGenerationResume, acknowledgeGenerationResume, resumeRefusalText } from "../../client/generation.ts";
import { ApiError, DispatchBlockedError } from "../../client/types.ts";
import type { GenerationRecord } from "../../client/generation.ts";
import { Sheet } from "../sheet.tsx";
import { pinGenerationContext, initialGenerationProject, generationSourceMode, savedGenerationScope, savedGenerationJob } from "./generation-context.ts";
import { DraftControl } from "./generation-drafts.tsx";

const message=(error:unknown)=>error instanceof ApiError&&(error.status===412||error.status===409&&error.code==="INVALID_TRANSITION")?resumeRefusalText(error):error instanceof Error?error.message:String(error);
const stateName=(job:JobView)=>({queued:"대기",running:"생성 중",paused:"확인 또는 재개 필요",completed:"생성 완료",failed:"생성 실패",cancelled:"취소됨"}[job.state]??job.state);

function SavedGenerationScope({record,projects,documents}:{record:GenerationRecord;projects:ProjectView[];documents:Readonly<Record<string,DocumentView|null|undefined>>}) {
 const scope=savedGenerationScope(record,projects,documents);
 return <dl className="draft-target" aria-label="저장된 생성 대상">
  <dt>프로젝트 표시 이름 · ID</dt><dd>{scope.projectName} · {scope.projectId}</dd>
  <dt>문서 표시 이름 · ID</dt><dd>{scope.documentTitle}{scope.documentId?` · ${scope.documentId}`:""}</dd>
  <dt>캡처한 발행 기준</dt><dd>{scope.baseVersionId??(scope.documentId?"기록 없음":"없음 · 새 문서 기준")}</dd>
  <dt>원래 요청 의도</dt><dd>{scope.intent==="publish"?"발행 요청":"초안으로 보관"}</dd>
  <dt>원래 작업 ID</dt><dd>{scope.operationId}</dd>
 </dl>;
}

export function GenerationControl({controller,projects,initialProjectId,onClose}:{controller:ShellController;projects:ProjectView[];initialProjectId:string;onClose:()=>void}) {
 const client=controller.client;
 const library=controller.state.capabilities!.library_id,principal=controller.state.session!.principal_id;
 const [journal]=useState(()=>new BrowserGenerationJournal(`${library}:${principal}`));
 const [submission]=useState(()=>new GenerationSubmission(client,journal,library,principal,undefined,undefined,()=>controller.state.capabilities?.runtime_generation??""));
 const [project,setProject]=useState(initialProjectId);
 const [explicitProject,setExplicitProject]=useState(false);
 const [documents,setDocuments]=useState<DocumentView[]>([]),[documentCursor,setDocumentCursor]=useState<string|null>(null),[documentId,setDocumentId]=useState("");
 const [sources,setSources]=useState<ProjectSourceView[]>([]),[paths,setPaths]=useState<Record<string,string>>({}),[revisions,setRevisions]=useState<Record<string,string>>({});
 const [sourceOverrides,setSourceOverrides]=useState<Record<string,"current"|"pinned">>({});
 const [sourceDefaults,setSourceDefaults]=useState<Record<string,"current"|"pinned">>({});
 const sourceMode=generationSourceMode(project,sourceOverrides,sourceDefaults);
 const [providerDefaults,setProviderDefaults]=useState<Record<string,string>>({}),[providerOverrides,setProviderOverrides]=useState<Record<string,string>>({});
 const provider=providerOverrides[project]??providerDefaults[project]??"";
 const [prompt,setPrompt]=useState(""),[title,setTitle]=useState(""),[intent,setIntent]=useState<"publish"|"draft">("draft");
 const [searchOverride,setSearchOverride]=useState<string|null>(null),[query,setQuery]=useState(""),[hits,setHits]=useState<SearchHit[]>([]),[searchCursor,setSearchCursor]=useState<string|null>(null),[hitsScope,setHitsScope]=useState("");
 const searchProject=searchOverride??project;
 const searchPending=searchOverride===null&&!project;
 const [retained,setRetained]=useState<Record<string,RetainedSourceRead[]>>({}),[readCursor,setReadCursor]=useState<Record<string,string|null>>({}),[selectedReads,setSelectedReads]=useState<Record<string,Record<string,string>>>({});
 const [references,setReferences]=useState<Array<{label:string;reference:GenerationReference}>>([]);
 const [records,setRecords]=useState<GenerationRecord[]>([]),[storedJob,setJob]=useState<JobView|null>(null),[active,setActive]=useState<GenerationRecord|null>(null);
 const job=savedGenerationJob(active,storedJob);
 const labelAuthority=controller.captureDispatchAuthority(),labelAuthorityKey=labelAuthority===null?"":JSON.stringify(labelAuthority);
 const labelIds=JSON.stringify([...new Set([...records, ...(active?[active]:[])].map(record=>record.request.document_id).filter((id):id is string=>!!id))].sort());
 const [documentLabels,setDocumentLabels]=useState<{authority:string;items:Record<string,DocumentView|null>}>({authority:"",items:{}});
 const visibleLabels=documentLabels.authority===labelAuthorityKey?documentLabels.items:{};
 useEffect(()=>{
  let live=true;setDocumentLabels({authority:labelAuthorityKey,items:{}});
  if(labelAuthority===null)return()=>{live=false;};
  const ids=JSON.parse(labelIds) as string[];
  void Promise.allSettled(ids.map(id=>client.documentDetail(id))).then(results=>{
   if(!live)return;
   try{controller.requireVerifiedDispatch(labelAuthority);}catch{return;}
   const items:Record<string,DocumentView|null>={};
   results.forEach((result,index)=>{items[ids[index]!]=result.status==="fulfilled"?result.value:null;});
   setDocumentLabels({authority:labelAuthorityKey,items});
  });
  return()=>{live=false;};
 },[client,controller,labelIds,labelAuthorityKey]);
 const [busy,setBusy]=useState(false),[error,setError]=useState<string|null>(null),[notice,setNotice]=useState(""),[drafts,setDrafts]=useState(false);
 const searchScope=useRef(searchProject);searchScope.current=searchProject;
 const alive=useRef(true),operation=useRef<{stopped:boolean}|null>(null),searchRun=useRef(0),busyRef=useRef(false);
 useEffect(()=>{alive.current=true;try{setRecords(journal.read());}catch(e){setError(message(e));}return()=>{alive.current=false;if(operation.current)operation.current.stopped=true;searchRun.current++;};},[journal]);
 useEffect(()=>{setProject(current=>initialGenerationProject(current,explicitProject,initialProjectId,projects));},[explicitProject,initialProjectId,projects]);
 useEffect(()=>{
  let live=true;
  void client.configurationView().then(view=>{
   if(!live)return;
   setSourceDefaults(Object.fromEntries(projects.map(p=>[p.project_id,view.configuration.projects[p.project_id]?.source_mode_default??"pinned"])));
   setProviderDefaults(Object.fromEntries(projects.map(p=>[p.project_id,view.configuration.projects[p.project_id]?.provider_id??""])));
  }).catch(e=>{if(live)setError(message(e));});
  return()=>{live=false;};
 },[client,projects]);
 useEffect(()=>{
  let live=true;setDocuments([]);setSources([]);setDocumentId("");setPaths({});setRevisions({});setDocumentCursor(null);setRetained({});setReadCursor({});setSelectedReads({});
  if(!project)return;
  void Promise.all([client.listDocuments({projectId:project}),client.projectSources(project)]).then(async([docs,page])=>{
   const all=[...page.items];let cursor=page.next_cursor;
   while(cursor){const next=await client.projectSources(project,cursor);all.push(...next.items);cursor=next.next_cursor;}
   if(live){setDocuments(docs.items);setDocumentCursor(docs.next_cursor);setSources(all);setRevisions(Object.fromEntries(all.map(source=>[source.source_id,source.last_observed_revision??""])));}
  }).catch(e=>{if(live)setError(message(e));});return()=>{live=false;};
 },[client,project]);
 // The explicit user action owns its cancellation token. Replacing the active
 // token never clears a stopped older upload's token or authorizes its callbacks.
 const perform=async(action:(control:{stopped:boolean})=>Promise<void>)=>{
  if(busyRef.current)return;const control={stopped:false};operation.current=control;
  busyRef.current=true;setBusy(true);setError(null);
  try{await action(control);}catch(e){if(alive.current)setError(message(e));}
  finally{if(operation.current===control){busyRef.current=false;if(alive.current){setBusy(false);try{setRecords(journal.read());}catch(e){setError(message(e));}}}}
 };
 const guard=(control:{stopped:boolean})=>{if(!alive.current || control.stopped || operation.current!==control)throw new Error("중지됨. 원래 작업은 복구 기록에 유지됩니다.");controller.requireVerifiedDispatch();};
 const send=async(record:GenerationRecord,control:{stopped:boolean})=>{
  const authority=controller.captureDispatchAuthority();guard(control);setJob(null);setActive(record);setNotice("요청 본문 전송 및 생성 접수 확인 중…");
  const accepted=await submission.send(record,prompt,{guard:()=>{guard(control);controller.requireVerifiedDispatch(authority);},shouldContinue:()=>!control.stopped&&alive.current&&operation.current===control,onProgress:p=>{if(alive.current&&operation.current===control&&!control.stopped)setNotice(`요청 본문 ${p.ackedBytes} 바이트 전송 확인`);}});
  guard(control);const current=await client.jobGet(accepted.job_id);guard(control);setJob(current);setNotice("접수됨. 문서 발행 여부는 아래 작업과 초안의 현재 상태로 확인하세요.");
 };
 const start=()=>perform(async control=>{
  guard(control);let base:string|null=null;
  if(documentId){const doc=await client.documentDetail(documentId);guard(control);if(doc.project_id!==project || doc.latest_version_id===null)throw new Error("선택한 문서의 현재 발행 버전을 확인해 주세요.");base=doc.latest_version_id;}
  const selections=sources.map(source=>{
   if(sourceMode==="pinned"&&source.kind==="files"){
    const picked=selectedReads[source.source_id]??{};
    return {source_id:source.source_id,paths:Object.entries(picked).map(([path,read_id])=>{
     const read=retained[source.source_id]?.find(row=>row.read_id===read_id&&row.location?.path===path);
     if(!read || read.availability!=="available" || read.retention_mode!=="selected_snapshot")throw new Error("선택한 보관 원본을 다시 확인해 주세요. 현재 파일로 대체하지 않습니다.");
     return {path,read_id};
    })};
   }
   return {source_id:source.source_id,...(sourceMode==="pinned"&&source.kind==="git"?{revision:revisions[source.source_id]}:{}),paths:(paths[source.source_id]??"").split(/\r?\n/).filter(path=>path!=="").map(path=>({path}))};
  }).filter(selection=>selection.paths.length>0);
  if(selections.length>0 && sourceMode===null)throw new Error("프로젝트 원본 기본값을 읽거나 명시적인 원본 기준을 선택해 주세요.");
  const request:GenerationRequest={project_id:project,document_id:documentId||null,base_version_id:base,intent,title,provider_id:provider,source_selection:{...(selections.length>0&&sourceMode?{source_mode:sourceMode,source_intent:sourceMode==="pinned"?"historical" as const:"moving_target" as const}:{}),selections},explanation_references:references.map(item=>item.reference)};
  const record=submission.capture(request,prompt,()=>guard(control));setRecords(journal.read());await send(record,control);
 });
 const loadRetained=(sourceId:string,more=false)=>perform(async control=>{
  const authority=controller.captureDispatchAuthority();guard(control);
  const page=await client.retainedSourceReads(project,sourceId,more?readCursor[sourceId]??undefined:undefined);
  guard(control);controller.requireVerifiedDispatch(authority);
  setRetained(old=>({...old,[sourceId]:more?[...(old[sourceId]??[]),...page.items]:page.items}));
  setReadCursor(old=>({...old,[sourceId]:page.next_cursor}));
 });
 const resume=(decision?:"reconcile_only"|"continue"|"retry")=>perform(async control=>{
  if(!active?.accepted)throw new Error("원래 접수 작업을 확인해 주세요.");
  const operationId=active.operationId,jobId=active.accepted.job_id;
  const authority=controller.captureDispatchAuthority();
  if(authority===null)throw new DispatchBlockedError();
  const currentGuard=()=>{guard(control);controller.requireVerifiedDispatch(authority);};
  currentGuard();
  await submission.lock("haeryong.generation:"+library+":"+principal+":"+operationId,async()=>{
   currentGuard();
   const record=journal.read().find(row=>row.operationId===operationId);
   if(!record?.accepted||record.accepted.job_id!==jobId)throw new Error("원래 생성 작업의 복구 기록을 확인해 주세요.");
   let persisted=JSON.stringify(record);
   const save=()=>{
    const stored=journal.read().find(row=>row.operationId===operationId);
    if(JSON.stringify(stored)!==persisted)throw new Error("다른 창에서 원래 재개 기록이 바뀌었습니다. 다시 확인해 주세요.");
    journal.save(record);persisted=JSON.stringify(record);
    if(alive.current&&operation.current===control)setActive(structuredClone(record));
   };
   const recovering=record.resume!==undefined;
   if(!record.resume){
    if(!decision||!job?.etag||!job.attempt_generation||job.job_id!==jobId)throw new Error("현재 작업 상태를 다시 확인해 주세요.");
    record.resume={key:crypto.randomUUID(),decision,generation:job.attempt_generation,etag:job.etag,runtimeGeneration:authority.runtimeGeneration,dispatchReady:false};
    save();
   }
   if(!recovering||!await recoverGenerationResume(client,record,authority.runtimeGeneration,save,currentGuard)){
    await dispatchGenerationResume(client,record,authority.runtimeGeneration,save,currentGuard);
   }
   currentGuard();const current=await client.jobGet(jobId);currentGuard();
   delete record.resume;save();setJob(current);setActive(structuredClone(record));
   setNotice("원래 재개 결정의 결과를 확인했습니다.");
  });
 });
 const acknowledgeResume=()=>perform(async control=>{
  if(!active?.accepted||!active.resume?.refused)throw new Error("확정된 원래 재개 거부를 확인해 주세요.");
  const operationId=active.operationId,jobId=active.accepted.job_id,expected=structuredClone(active.resume);
  const authority=controller.captureDispatchAuthority();if(authority===null)throw new DispatchBlockedError();
  const currentGuard=()=>{guard(control);controller.requireVerifiedDispatch(authority);};
  await submission.lock("haeryong.generation:"+library+":"+principal+":"+operationId,async()=>{
   currentGuard();const current=await client.jobGet(jobId);currentGuard();
   const record=acknowledgeGenerationResume(journal,operationId,expected);
   setActive(record);setJob(current);setNotice("원래 재개 거부를 이력에 보존했습니다. 현재 작업에서 새 결정을 선택하세요.");
  });
 });

 // Automatic search follows the current generation project until an explicit
 // All/other-project selection; discard cursors and late results on any change.
 useEffect(()=>{searchRun.current++;setHits([]);setSearchCursor(null);},[searchProject]);
 const search=async(more=false)=>{
  if(searchPending){setError("검색할 현재 프로젝트를 읽는 중입니다.");return;}
  const run=++searchRun.current,scope=searchProject;setError(null);
  try{const page=await client.search({q:query,project_id:searchProject||undefined,lifecycle:"all",versions:"all",kind:"page",cursor:more&&hitsScope===scope?searchCursor??undefined:undefined});if(alive.current&&run===searchRun.current&&searchScope.current===scope){setHitsScope(scope);setHits(old=>more&&hitsScope===scope?[...old,...page.items]:page.items);setSearchCursor(page.next_cursor);}}
  catch(e){if(alive.current&&run===searchRun.current&&searchScope.current===scope){setHits([]);setSearchCursor(null);setError(message(e));}}
 };
 return <Sheet open modal label="설명 생성" openerId="generation-open" onClose={()=>{if(operation.current)operation.current.stopped=true;onClose();}} className="generation-sheet">
  <header><h2>설명 생성</h2><button type="button" onClick={()=>{if(operation.current)operation.current.stopped=true;onClose();}}>닫기</button></header>
  <div className="form-row"><button type="button" aria-pressed={!drafts} onClick={()=>setDrafts(false)}>새 설명</button><button type="button" aria-pressed={drafts} onClick={()=>setDrafts(true)}>초안 관리</button></div>
  {error?<p role="alert">{error}</p>:null}
  {drafts?<DraftControl controller={controller} projects={projects}/>:<>
   <form onSubmit={event=>{event.preventDefault();void start();}} aria-busy={busy}>
    <label className="field"><span>프로젝트</span><select value={project} onChange={e=>{setExplicitProject(true);setProject(e.target.value);}} disabled={busy}>{projects.map(p=><option key={p.project_id} value={p.project_id}>{p.name}</option>)}</select></label>
    <label className="field"><span>AI 제공 방식</span><select value={provider} disabled={busy} onChange={e=>setProviderOverrides(old=>({...old,[project]:e.target.value}))}><option value="" disabled>프로젝트 기본값을 선택하거나 제공 방식을 지정하세요</option>{provider&&provider!=="codex"?<option value={provider} disabled>현재 기본값 {provider} · 이 구현에서 지원되지 않음</option>:null}<option value="codex">Codex · 개인 로컬 구독</option></select></label>
    <label className="field"><span>대상 문서</span><select value={documentId} onChange={e=>setDocumentId(e.target.value)} disabled={busy}><option value="">새 문서</option>{documents.map(d=><option key={d.document_id} value={d.document_id}>{d.title}</option>)}</select></label>
    {documentCursor?<button type="button" disabled={busy} onClick={()=>void perform(async()=>{const page=await client.listDocuments({projectId:project,cursor:documentCursor});setDocuments(old=>[...old,...page.items]);setDocumentCursor(page.next_cursor);})}>문서 더 보기</button>:null}
    <label className="field"><span>설명 제목</span><input type="text" value={title} onChange={e=>setTitle(e.target.value)} disabled={busy}/></label>
    <label className="field"><span>요청</span><textarea value={prompt} onChange={e=>setPrompt(e.target.value)} rows={7} disabled={busy} required/></label>
    <label className="field"><span>완료 후 처리</span><select value={intent} onChange={e=>setIntent(e.target.value as "draft"|"publish")} disabled={busy}><option value="draft">초안으로 보관</option><option value="publish">선택한 기준 버전에서 발행</option></select></label>
    <p className="hint">발행 중 기준 버전이 바뀌면 결과는 초안으로 남습니다. 개인 Review는 변경되지 않습니다.</p>
    <fieldset disabled={busy}><legend>확인할 원본 파일 (선택)</legend>
     <label className="field"><span>원본 기준</span><select value={sourceMode??""} onChange={e=>setSourceOverrides(old=>({...old,[project]:e.target.value as "current"|"pinned"}))}>{sourceMode===null?<option value="" disabled>프로젝트 설정 읽는 중</option>:null}<option value="current">현재 작업 상태를 캡처</option><option value="pinned">지정한 이력 버전</option></select></label>
     {sources.map(source=><div key={source.source_id}>{sourceMode==="pinned"&&source.kind==="files"?<>
      <h4>{source.repo_id??`파일 원본 · ${source.source_id.slice(0,8)}`} · 보관한 파일 선택</h4>
      <button type="button" onClick={()=>void loadRetained(source.source_id)}>보관 원본 목록 확인</button>
      {retained[source.source_id]?.length===0?<p>이 원본에서 선택할 수 있는 보관 이력이 없습니다. 현재 파일로 자동 대체하지 않습니다.</p>:null}
      <ul>{retained[source.source_id]?.map(read=><li key={read.read_id}><label className="choice"><span>{read.location?.path??"사용할 수 없는 원본"} · <time dateTime={read.captured_at}>{new Date(read.captured_at).toLocaleString()}</time> · {read.bytes_sha256.slice(0,12)} · {read.availability==="available"?"보관됨":read.reason??"사용 불가"}</span><input type="checkbox" checked={selectedReads[source.source_id]?.[read.location?.path??""]===read.read_id} disabled={read.availability!=="available"||read.retention_mode!=="selected_snapshot"||!read.location?.path} onChange={e=>{const checked=e.target.checked,path=read.location!.path;setSelectedReads(old=>{const selected={...(old[source.source_id]??{})};if(checked)selected[path]=read.read_id;else delete selected[path];return {...old,[source.source_id]:selected};});}}/></label></li>)}</ul>
      {readCursor[source.source_id]?<button type="button" onClick={()=>void loadRetained(source.source_id,true)}>보관 원본 더 보기</button>:null}
     </>:<label className="field"><span>{source.repo_id??source.kind} · 상대 파일 경로, 한 줄에 하나</span><textarea value={paths[source.source_id]??""} onChange={e=>setPaths(old=>({...old,[source.source_id]:e.target.value}))} rows={2}/></label>}{sourceMode==="pinned"&&source.kind==="git"?<label className="field"><span>이력 버전</span><input type="text" value={revisions[source.source_id]??""} onChange={e=>setRevisions(old=>({...old,[source.source_id]:e.target.value}))}/></label>:null}</div>)}
     <p className="hint">원본을 선택하지 않은 설명은 출처 미확인으로 보관됩니다. 기존 설명 선택은 원본 확인을 대신하지 않습니다.</p>
    </fieldset>
    <button type="submit" className="primary" disabled={busy||!project||provider!=="codex"||!prompt.trim()}>생성 요청</button>
    {busy?<button type="button" onClick={()=>{if(operation.current)operation.current.stopped=true;}}>전송 중지 · 복구 기록 유지</button>:null}
   </form>
   <section aria-label="기존 설명 선택"><h3>관련 설명 찾기</h3>
    <label className="field"><span>검색 프로젝트</span><select value={searchPending?"__project_pending__":searchProject} onChange={e=>{searchRun.current++;setSearchOverride(e.target.value);setHits([]);setSearchCursor(null);}}>{searchPending?<option value="__project_pending__" disabled>현재 프로젝트 읽는 중</option>:null}<option value="">모든 접근 가능한 프로젝트</option>{projects.map(p=><option key={p.project_id} value={p.project_id}>{p.name}</option>)}</select></label>
    <label className="field"><span>검색어</span><input type="search" value={query} onChange={e=>{searchRun.current++;setQuery(e.target.value);setHits([]);setSearchCursor(null);}}/></label><button type="button" disabled={!query.trim()||busy||searchPending} onClick={()=>void search()}>설명 검색</button>
    <ul>{(hitsScope===searchProject?hits:[]).map((hit,index)=><li key={`${hit.version_id}:${hit.page_path}:${index}`}><strong>{hit.title}</strong> · {hit.page_path}<p>{hit.snippet}</p><a href={`/documents/${hit.document_id}/versions/${hit.version_id}?page=${encodeURIComponent(hit.page_path??"")}`} target="_blank" rel="noopener noreferrer">선택한 설명 읽기</a><button type="button" disabled={busy} onClick={()=>void perform(async control=>{const reference=await pinGenerationContext(client,hit);guard(control);setReferences(old=>old.some(item=>JSON.stringify(item.reference)===JSON.stringify(reference))?old:[...old,{label:`${hit.title} · ${hit.page_path}`,reference}]);})}>이 버전 참고</button></li>)}</ul>
    {hitsScope===searchProject&&searchCursor?<button type="button" onClick={()=>void search(true)}>검색 결과 더 보기</button>:null}
    <ul>{references.map((item,index)=><li key={JSON.stringify(item.reference)}>{item.label}<button type="button" disabled={busy} onClick={()=>setReferences(old=>old.filter((_,i)=>i!==index))}>참고 제외</button></li>)}</ul>
    <p className="hint">선택한 페이지의 고정 버전과 실제 바이트를 전달합니다. 이 생성 환경에서 라이브러리 전체 자동 검색을 했다는 뜻은 아닙니다.</p>
   </section>
   <p role="status" aria-live="polite">{notice}</p>
   {active?<section aria-label="선택한 원래 생성 요청" id={`generation-active-${active.operationId}`}><h3>선택한 원래 생성 요청</h3><SavedGenerationScope record={active} projects={projects} documents={visibleLabels}/></section>:null}
   {job?<section aria-label="생성 작업" aria-describedby={active?`generation-active-${active.operationId}`:undefined}><h3>{stateName(job)}</h3><p>{job.error}</p>
    <button type="button" disabled={busy} onClick={()=>void perform(async control=>{if(!active?.accepted)return;const current=await client.jobGet(active.accepted.job_id);guard(control);setJob(current);})}>작업 상태 새로고침</button>
    {!["completed","failed","cancelled"].includes(job.state)?<button type="button" disabled={busy} onClick={()=>void perform(async()=>{controller.requireVerifiedDispatch();await client.jobCancel(job.job_id,crypto.randomUUID());setJob(await client.jobGet(job.job_id));})}>생성 작업 취소</button>:null}
    {active?.resume?<section aria-label="원래 재개 결정"><p>원래 재개 결정의 결과를 확인해야 합니다. 새 결정을 자동으로 만들지 않습니다.</p>
     {active.resume.refused?<><p>{resumeRefusalText(active.resume.refused)}</p><button type="button" disabled={busy} onClick={()=>void acknowledgeResume()}>재개 거부 확인 · 새 결정 선택</button></>:<button type="button" disabled={busy} onClick={()=>void resume()}>원래 재개 결정 결과 확인</button>}
    </section>:null}
    {controller.state.session?.scope.kind==="owner"&&job.state==="paused"?<>
     <button type="button" disabled={busy||!!active?.resume} onClick={()=>void resume("reconcile_only")}>상태만 재조정</button>
     <button type="button" disabled={busy||!!active?.resume} onClick={()=>void resume("continue")}>원래 작업 이어가기</button>
     <button type="button" disabled={busy||!!active?.resume} onClick={()=>void resume("retry")}>추가 사용량을 허용하고 재시도</button>
    </>:null}
    <p>이미 완성된 후보가 있는 작업은 추가 생성으로 재시도하지 않습니다. 초안 관리에서 후보를 확인하거나 별도의 새 생성 요청을 선택하세요.</p>
    <p>결과는 초안 관리에서 확인할 수 있습니다. 불확실한 작업은 새 생성 요청을 자동으로 만들지 않습니다.</p></section>:null}

   <section aria-label="생성 복구 기록"><h3>이전 요청 확인</h3>{records.length===0?<p>복구 기록이 없습니다.</p>:records.map(record=><article key={record.operationId} data-operation-id={record.operationId}><h4>{record.request.title||"생성 요청"} · {record.accepted?"접수 확인됨":"확인 필요"}</h4><SavedGenerationScope record={record} projects={projects} documents={visibleLabels}/><button type="button" disabled={busy} onClick={()=>void perform(control=>send(record,control))}>{record.accepted?"작업 확인":"원래 요청 복구"}</button></article>)}</section>
  </>}
 </Sheet>;
}
