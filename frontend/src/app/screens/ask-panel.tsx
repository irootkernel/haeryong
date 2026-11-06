import {useEffect,useRef,useState} from "react";
import type {ShellController} from "../../client/controller.ts";
import type {AnswerView,GenerationSourceSelection,JobView,ProjectSourceView,QuestionRequest,QuestionView,RetainedSourceRead,VersionView} from "../../client/types.ts";
import {ApiError,DispatchBlockedError,StaleResponseError} from "../../client/types.ts";
import {BrowserQuestionJournal,QuestionSubmission,questionStreamText,questionUnresolved} from "../../client/questions.ts";
import type {QuestionRecord} from "../../client/questions.ts";
import {acknowledgeGenerationResume,dispatchGenerationResume,recoverGenerationResume,resumeRefusalText} from "../../client/generation.ts";
import {useReviewInvalidation} from "./review-refresh.ts";
import {SafeMarkdown} from "./safe-markdown.tsx";
import {QuestionHistory,revalidateInspectedQuestion} from "./question-history.ts";
import {pinnedReaderURL} from "../routes.ts";
import {prepareNoteContext,type NoteContextText} from "./notes-context.ts";

interface Anchor {version_id:string;page_path:string|null;region_id:string|null;page_query:string;page_fragment:string}
interface Selection {contextRef:string|null;context:unknown|null}
interface Draft {anchor:Anchor;project:string;document:string;provider:string;mode:"pinned"|"current";contextRef:string|null;context:unknown|null;sources:ProjectSourceView[]}
interface Control {stopped:boolean}
const message=(e:unknown)=>e instanceof ApiError?`${e.code}${e.details.reason?` · ${String(e.details.reason)}`:""}`:e instanceof Error?e.message:String(e);
const label=(anchor:Anchor)=>`${anchor.version_id} · ${anchor.page_path??"전체 디렉터리"}${anchor.region_id?` · 영역 ${anchor.region_id}`:""}${anchor.page_query?` ?${anchor.page_query}`:""}${anchor.page_fragment?` #${anchor.page_fragment}`:""}`;

export function AskPanel({controller,documentId,projectId,sourceIntent,sourceReady,anchor,selection,active}:{controller:ShellController;documentId:string;projectId:string;sourceIntent:string|null;sourceReady:boolean;anchor:Anchor;selection:Selection;active:boolean}){
 const client=controller.client,library=controller.state.capabilities?.library_id??"",principal=controller.state.session?.principal_id??"";
 const enabled=controller.state.capabilities?.features.questions===true&&controller.state.session?.scope.kind==="owner";
 const canWrite=enabled&&controller.state.phase==="verified";
 const [journal]=useState(()=>new BrowserQuestionJournal(`${library}:${principal}`));
 const [submission]=useState(()=>new QuestionSubmission(client,journal,library,principal,undefined,undefined,()=>controller.state.capabilities?.runtime_generation??""));
 const [draft,setDraft]=useState<Draft|null>(null),[text,setText]=useState(""),[scope,setScope]=useState<"version"|"page"|"region">("page");
 const [provider,setProvider]=useState(""),[mode,setMode]=useState<"pinned"|"current"|null>(null),[configured,setConfigured]=useState(false);
 const [useDefaults,setUseDefaults]=useState(true),[sources,setSources]=useState<ProjectSourceView[]>([]),[paths,setPaths]=useState<Record<string,string>>({}),[revisions,setRevisions]=useState<Record<string,string>>({});
 const [retained,setRetained]=useState<Record<string,RetainedSourceRead[]>>({}),[retainedCursor,setRetainedCursor]=useState<Record<string,string|null>>({}),[picked,setPicked]=useState<Record<string,Record<string,string>>>({});
 const [records,setRecords]=useState<QuestionRecord[]>([]),[current,setCurrent]=useState<QuestionRecord|null>(null),[storedJob,setJob]=useState<JobView|null>(null);
 const job=current?.accepted&&storedJob?.job_id===current.accepted.job_id?storedJob:null;
 const [historyVersion,setHistoryVersion]=useState(anchor.version_id),[versions,setVersions]=useState<VersionView[]>([]),[versionCursor,setVersionCursor]=useState<string|null>(null);
 const [historyQuery]=useState(()=>new QuestionHistory(client));
 const [historyState,setHistoryState]=useState(historyQuery.state),[saved,setSaved]=useState<QuestionView|null>(null);
 const history=historyState.items,historyCursor=historyState.nextCursor;
 const [citationText,setCitationText]=useState<Record<string,string>>({});
 const [answerText,setAnswerText]=useState<Record<string,string>>({}),[savedQuestionText,setSavedQuestionText]=useState(""),[contextText,setContextText]=useState<NoteContextText|null>(null);
 const [busy,setBusy]=useState(false),[issue,setIssue]=useState(""),[notice,setNotice]=useState(""),[refresh,setRefresh]=useState(0);
 const reads=useRef({configuration:0,versions:0,job:0});
 const live=useRef(true),control=useRef<Control|null>(null),busyRef=useRef(false),selectionEra=useRef(0),savedId=useRef<string|null>(null);savedId.current=saved?.question_id??null;
 useEffect(()=>{live.current=true;try{setRecords(journal.read());}catch(e){setIssue(message(e));}return()=>{live.current=false;if(control.current)control.current.stopped=true;selectionEra.current++;};},[journal]);
 useEffect(()=>{let live=true;setConfigured(false);setSources([]);setMode(null);setProvider("");if(!enabled||!projectId)return;
  const authority=controller.captureDispatchAuthority(),read=++reads.current.configuration,gen=controller.readGeneration;
  void Promise.allSettled([client.configurationView(),(async()=>{const rows:ProjectSourceView[]=[];let cursor:string|undefined;do{const page=await client.projectSources(projectId,cursor);rows.push(...page.items);cursor=page.next_cursor??undefined;}while(cursor);return rows;})()]).then(([configuration,mappings])=>{
   if(!live||read!==reads.current.configuration||!controller.isCurrentReadAuthority(authority,gen))return;try{controller.requireVerifiedDispatch(authority);}catch{return;}
   if(configuration.status==="fulfilled"){const settings=configuration.value.configuration.projects[projectId];setProvider(settings?.provider_id??"");setMode(sourceIntent==="historical"?"pinned":settings?.source_mode_default??"pinned");setConfigured(true);}else setIssue(message(configuration.reason));
   if(mappings.status==="fulfilled")setSources(mappings.value);else setIssue(message(mappings.reason));
  });return()=>{live=false;};
 },[client,controller,enabled,projectId,sourceIntent,refresh]);
 useEffect(()=>{let live=true;const authority=controller.captureDispatchAuthority(),read=++reads.current.versions,gen=controller.readGeneration;if(enabled)void client.documentVersions(documentId).then(page=>{if(!live||read!==reads.current.versions||!controller.isCurrentReadAuthority(authority,gen))return;controller.requireVerifiedDispatch(authority);setVersions(page.items);setVersionCursor(page.next_cursor);}).catch(e=>{if(live&&read===reads.current.versions&&controller.isCurrentReadAuthority(authority,gen))setIssue(message(e));});return()=>{live=false;};},[client,controller,enabled,documentId]);
 useEffect(()=>historyQuery.subscribe(()=>setHistoryState(historyQuery.state)),[historyQuery]);
 useEffect(()=>{setSaved(null);setSavedQuestionText("");setAnswerText({});setCitationText({});setContextText(null);selectionEra.current++;const authority=controller.captureDispatchAuthority();if(enabled)void historyQuery.load(historyVersion,()=>controller.requireVerifiedDispatch(authority));else historyQuery.invalidate();return()=>historyQuery.invalidate();},[historyQuery,controller,enabled,historyVersion,refresh]);
 // Stream invalidation retires only server-derived reads, never saved consent
 // or an uncertain request. A subsequent explicit read obtains current citation
 // availability; permission/epoch loss immediately removes protected material.
 const clearSaved=()=>{selectionEra.current++;savedId.current=null;setSaved(null);setSavedQuestionText("");setAnswerText({});setCitationText({});setContextText(null);};
 useReviewInvalidation(controller,enabled,async(ev)=>{
  if(!enabled)return;
  const startReadGen=ev?.readGeneration??controller.readGeneration;
  const authority=controller.captureDispatchAuthority();
  const pClient=controller.passiveClient;
  const isResync=Boolean(ev?.resync);
  const read={configuration:++reads.current.configuration,versions:++reads.current.versions,job:++reads.current.job};
  const canApply=(kind?:keyof typeof reads.current)=>{
   if(!live.current||!controller.isCurrentReadAuthority(authority,startReadGen))return false;
   if(busyRef.current||(kind&&read[kind]!==reads.current[kind])){if(isResync)throw new StaleResponseError();return false;}
   return true;
  };
  const tasks:Promise<void>[]=[
   historyQuery.load(historyVersion,()=>{if(!canApply())throw Error("stale");},false,pClient,isResync),
  ];
  const inspectedId=savedId.current;
  if(inspectedId){
   const inspectedEra=selectionEra.current;
   tasks.push(revalidateInspectedQuestion(
    pClient,
    inspectedId,
    ()=>{if(!canApply())return false;const selected=savedId.current===inspectedId&&selectionEra.current===inspectedEra;if(!selected&&isResync&&savedId.current!==null)throw new StaleResponseError();return selected;},
    fresh=>setSaved(fresh),
    clearSaved,
    err=>setIssue(message(err)),
    isResync,
   ));
  }
  if(projectId){
   tasks.push(Promise.allSettled([pClient.configurationView(),(async()=>{const rows:ProjectSourceView[]=[];let cursor:string|undefined;do{const page=await pClient.projectSources(projectId,cursor);rows.push(...page.items);cursor=page.next_cursor??undefined;}while(cursor);return rows;})()]).then(([configuration,mappings])=>{
    if(!canApply("configuration"))return;
    if(configuration.status==="fulfilled"){const settings=configuration.value.configuration.projects[projectId];setProvider(settings?.provider_id??"");setMode(sourceIntent==="historical"?"pinned":settings?.source_mode_default??"pinned");setConfigured(true);}
    else setIssue(message(configuration.reason));
    if(mappings.status==="fulfilled")setSources(mappings.value);
    else setIssue(message(mappings.reason));
    if(isResync){
     if(configuration.status==="rejected")throw configuration.reason;
     if(mappings.status==="rejected")throw mappings.reason;
    }
   }));
  }
  if(documentId){
   tasks.push(pClient.documentVersions(documentId).then(page=>{if(!canApply("versions"))return;setVersions(page.items);setVersionCursor(page.next_cursor);}).catch(error=>{if(!canApply())return;setIssue(message(error));if(isResync)throw error;}));
  }
  const jobId=current?.accepted?.job_id;
  if(jobId){
   tasks.push(pClient.jobGet(jobId).then(value=>{if(!canApply("job"))return;setJob(value);}).catch(error=>{if(!canApply())return;if(error instanceof ApiError&&[401,403,404].includes(error.status)){setJob(null);clearSaved();return;}setIssue(message(error));if(isResync)throw error;}));
  }
  if(isResync)await Promise.all(tasks);
  else await Promise.allSettled(tasks);
 },()=>busyRef.current);
 useEffect(()=>{const authority=controller.captureDispatchAuthority();return controller.subscribe(()=>{try{controller.requireVerifiedDispatch(authority);}catch{clearSaved();historyQuery.invalidate();setJob(null);}});},[controller]);
 useEffect(()=>{const id=current?.accepted?.job_id;if(!enabled||!id)return;let live=true;const authority=controller.captureDispatchAuthority(),read=++reads.current.job,gen=controller.readGeneration;void client.jobGet(id).then(value=>{if(!live||read!==reads.current.job||!controller.isCurrentReadAuthority(authority,gen))return;controller.requireVerifiedDispatch(authority);setJob(value);}).catch(error=>{if(!live||read!==reads.current.job||!controller.isCurrentReadAuthority(authority,gen))return;if(error instanceof ApiError&&[401,403,404].includes(error.status)){setJob(null);clearSaved();}setIssue(message(error));});return()=>{live=false;};},[client,controller,enabled,current?.accepted?.job_id,refresh]);
 const guard=(own:Control,authority:ReturnType<ShellController["captureDispatchAuthority"]>)=>{if(!live.current||own.stopped||control.current!==own)throw Error("중지됨. 원래 질문과 작업 ID는 복구 기록에 유지됩니다.");controller.requireVerifiedDispatch(authority);if(!enabled)throw new DispatchBlockedError();};
 const perform=async(action:(own:Control,authority:ReturnType<ShellController["captureDispatchAuthority"]>)=>Promise<void>)=>{
  if(busyRef.current)return;const own={stopped:false},authority=controller.captureDispatchAuthority();control.current=own;controller.bumpReadGeneration();busyRef.current=true;setBusy(true);setIssue("");
  try{guard(own,authority);await action(own,authority);}catch(e){if(live.current){if(e instanceof ApiError&&[401,403,404].includes(e.status)){clearSaved();historyQuery.invalidate();setJob(null);}setIssue(message(e));}}finally{if(control.current===own){busyRef.current=false;if(live.current){setBusy(false);try{setRecords(journal.read());}catch(e){setIssue(message(e));}}}}
 };
 const newDraft=()=>{try{controller.requireVerifiedDispatch();if(!enabled||!configured||!sourceReady||!mode||!projectId)throw Error("프로젝트 기본값을 먼저 확인하세요.");if(journal.read().some(questionUnresolved))throw Error("결과가 확인되지 않은 원래 질문을 먼저 확인하세요.");
  const captured=structuredClone(anchor);if(scope==="version"){captured.page_path=null;captured.region_id=null;captured.page_query="";captured.page_fragment="";}else if(scope==="page")captured.region_id=null;
  if(scope!=="version"&&!captured.page_path||scope==="region"&&!captured.region_id)throw Error("현재 페이지와 영역을 확인하세요.");
  setDraft({anchor:captured,project:projectId,document:documentId,provider,mode,contextRef:scope==="version"?null:selection.contextRef,context:scope==="version"||selection.contextRef?null:structuredClone(selection.context),sources:structuredClone(sources)});setText("");setPaths({});setRevisions({});setPicked({});setUseDefaults(true);setNotice("질문 위치와 선택 문맥을 캡처했습니다. 전송은 질문 제출 버튼에서만 시작합니다.");
 }catch(e){setIssue(message(e));}};
 const selectedSources=():GenerationSourceSelection|undefined=>{
  if(useDefaults)return undefined;if(!draft)throw Error("질문 대상을 선택하세요.");
  return {source_mode:draft.mode,selections:draft.sources.map(source=>{
   if(draft.mode==="pinned"&&source.kind==="files"){return {source_id:source.source_id,paths:Object.entries(picked[source.source_id]??{}).map(([path,read_id])=>{const row=retained[source.source_id]?.find(r=>r.read_id===read_id&&r.location?.path===path);if(!row||row.availability!=="available"||row.retention_mode!=="selected_snapshot")throw Error("원래 보관 원본을 확인하세요. 현재 파일로 대체하지 않습니다.");return {path,read_id};})};}
   return {source_id:source.source_id,...(source.kind==="git"&&revisions[source.source_id]?{revision:revisions[source.source_id]}:{}),paths:(paths[source.source_id]??"").split(/\r?\n/).filter(Boolean).map(path=>({path}))};
  }).filter(source=>source.paths.length>0)};
 };
 const send=(record:QuestionRecord,own:Control,authority:ReturnType<ShellController["captureDispatchAuthority"]>)=>{
  guard(own,authority);setCurrent(record);setJob(null);setNotice("질문 본문·문맥 준비 및 원래 접수 결과 확인 중…");
  return submission.send(record,text,{guard:()=>guard(own,authority),shouldContinue:()=>!own.stopped&&live.current&&control.current===own,onProgress:p=>{if(live.current&&!own.stopped)setNotice(`준비 ${p.ackedBytes} 바이트 전송 확인`);}}).then(async accepted=>{guard(own,authority);const job=await client.jobGet(accepted.job_id);guard(own,authority);setCurrent(structuredClone(record));setJob(job);setNotice("질문이 접수되었습니다. 저장된 답변과 한계는 작업 상태로 확인하세요.");setRefresh(n=>n+1);});
 };
 const submit=()=>perform(async(own,authority)=>{if(!draft||!text.trim()||!draft.provider)throw Error("질문과 provider 선택을 확인하세요.");if(journal.read().some(questionUnresolved))throw Error("원래 질문 결과를 먼저 확인하세요.");
  const {version_id:versionId,...capturedAnchor}=draft.anchor;
  const request:QuestionRequest={...capturedAnchor,provider_id:draft.provider,source_mode:draft.mode,...(draft.contextRef?{context_ref:draft.contextRef}:draft.context===null?{}:{display_state:draft.context}),...(useDefaults?{}:{source_selection:selectedSources()})};
  const record=submission.capture(versionId,request,text,()=>guard(own,authority));setRecords(journal.read());await send(record,own,authority);
 });
 const refreshJob=()=>perform(async(own,authority)=>{if(!current?.accepted)throw Error("원래 접수 작업을 확인하세요.");const next=await client.jobGet(current.accepted.job_id);guard(own,authority);setJob(next);setRefresh(n=>n+1);});
 const resume=(decision?:"reconcile_only"|"continue"|"retry")=>perform(async(own,authority)=>{
  if(!current?.accepted||authority===null)throw new DispatchBlockedError();const operationId=current.operationId,jobId=current.accepted.job_id;
  await submission.lock("haeryong.questions:"+library+":"+principal+":"+operationId,async()=>{guard(own,authority);const record=journal.read().find(row=>row.operationId===operationId);if(!record?.accepted||record.accepted.job_id!==jobId)throw Error("원래 질문 재개 기록을 확인하세요.");let previous=JSON.stringify(record);
   const save=()=>{if(JSON.stringify(journal.read().find(row=>row.operationId===operationId))!==previous)throw Error("다른 창에서 원래 재개 기록이 바뀌었습니다.");journal.save(record);previous=JSON.stringify(record);if(live.current)setCurrent(structuredClone(record));};
   const recovering=record.resume!==undefined;if(!record.resume){if(!decision||!job?.etag||!job.attempt_generation||job.job_id!==jobId)throw Error("현재 질문 작업을 다시 확인하세요.");record.resume={key:crypto.randomUUID(),decision,generation:job.attempt_generation,etag:job.etag,runtimeGeneration:authority.runtimeGeneration,dispatchReady:false};save();}
   if(!recovering||!await recoverGenerationResume(client,record,authority.runtimeGeneration,save,()=>guard(own,authority)))await dispatchGenerationResume(client,record,authority.runtimeGeneration,save,()=>guard(own,authority));
   guard(own,authority);const next=await client.jobGet(jobId);guard(own,authority);delete record.resume;save();setJob(next);setRefresh(n=>n+1);
  });
 });
 const acknowledge=()=>perform(async(own,authority)=>{if(!current?.resume?.refused||!current.accepted)throw Error("확정된 원래 재개 거부를 확인하세요.");const operationId=current.operationId,expected=structuredClone(current.resume),jobId=current.accepted.job_id;
  await submission.lock("haeryong.questions:"+library+":"+principal+":"+operationId,async()=>{const next=await client.jobGet(jobId);guard(own,authority);const updated=acknowledgeGenerationResume(journal,operationId,expected);setCurrent(updated);setJob(next);setNotice("거부된 원래 결정을 이력에 보존했습니다. 현재 상태에서 새 결정을 선택하세요.");});
 });
 const loadQuestion=(id:string)=>perform(async(own,authority)=>{const ownSelection=++selectionEra.current;setSaved(null);setAnswerText({});setCitationText({});setContextText(null);setSavedQuestionText("");const value=await client.questionGet(id);guard(own,authority);if(ownSelection!==selectionEra.current)return;
  const question=await questionStreamText(await client.questionTextStream(id),()=>{guard(own,authority);if(ownSelection!==selectionEra.current)throw Error("이전 질문 읽기입니다.");});guard(own,authority);if(ownSelection!==selectionEra.current)return;setSaved(value);setSavedQuestionText(question);
 });
 const loadRetained=(source:string,more=false)=>perform(async(own,authority)=>{if(!draft)throw Error("질문 대상을 선택하세요.");const page=await client.retainedSourceReads(draft.project,source,more?retainedCursor[source]??undefined:undefined);guard(own,authority);setRetained(old=>({...old,[source]:more?[...(old[source]??[]),...page.items]:page.items}));setRetainedCursor(old=>({...old,[source]:page.next_cursor}));});
 const readCitation=(answer:AnswerView,index:number)=>perform(async(own,authority)=>{const id=saved?.question_id,era=selectionEra.current,citation=answer.citations[index],key=answer.answer_id+":"+index;if(!id||answer.question_id!==id||!citation)throw Error("저장된 인용을 확인하세요.");setCitationText(old=>{const next={...old};delete next[key];return next;});const stream=await client.answerCitationStream(answer.answer_id,index);if(stream.bytes_sha256!==citation.bytes_sha256){stream.cancel();throw Error("인용 원본의 정확한 digest가 다릅니다.");}const body=await questionStreamText(stream,()=>{guard(own,authority);if(savedId.current!==id||era!==selectionEra.current)throw Error("이전 질문 원본입니다.");});guard(own,authority);if(savedId.current===id&&era===selectionEra.current)setCitationText(old=>({...old,[key]:body}));});
 const readAnswer=(answer:AnswerView)=>perform(async(own,authority)=>{const captured=saved?.question_id,era=selectionEra.current;if(!captured||answer.question_id!==captured)throw Error("저장된 답변을 확인하세요.");const body=await questionStreamText(await client.answerBodyStream(answer.answer_id),()=>{guard(own,authority);if(savedId.current!==captured||era!==selectionEra.current)throw Error("이전 답변 읽기입니다.");});guard(own,authority);if(savedId.current===captured&&era===selectionEra.current)setAnswerText(old=>({...old,[answer.answer_id]:body}));});
 if(!enabled)return <p>Ask: 소유자 질문 기능은 아직 제공되지 않습니다.</p>;
 return <section aria-label="Ask" className="ask-panel"><h2>질문과 저장된 답변</h2><p>페이지 문맥 전송은 질문 접수가 아닙니다. 명시적인 질문 제출만 provider 작업을 시작합니다.</p>
  <label className="field"><span>새 질문 범위</span><select value={scope} disabled={busy} onChange={e=>setScope(e.target.value as typeof scope)}><option value="version">전체 디렉터리 버전</option><option value="page" disabled={!anchor.page_path}>현재 페이지</option><option value="region" disabled={!anchor.region_id}>현재 페이지 영역</option></select></label>
  <button id="ask-new" disabled={busy||!configured||!sourceReady||!active} onClick={newDraft}>현재 위치로 새 질문 준비</button>
  {draft&&<div className="ask-editor"><p aria-label="캡처한 질문 대상">{label(draft.anchor)}</p><p>프로젝트 {draft.project} · 문서 {draft.document}. 이동이나 기본값 변경은 이 질문을 바꾸지 않습니다.</p><p>{draft.anchor.page_path?"정확한 설명 페이지와 선택 문맥이 입력됩니다. 영역 질문도 그 페이지 전체를 설명 문맥으로 포함합니다.":"전체 디렉터리의 파일과 inventory가 설명 문맥으로 입력됩니다."} 설명 문맥은 프로젝트 원본 확인과 별개입니다.</p>
   <label className="field"><span>질문 provider</span><select disabled={busy} value={draft.provider} onChange={e=>setDraft({...draft,provider:e.target.value})}><option value="">명시적으로 선택하세요</option><option value="codex">개인 Codex · 읽기 전용</option></select></label>
   <p>provider 기능 지원과 계정·사용량 준비는 별개입니다. 이 질문의 provider 상태는 아직 확인하지 않았습니다. <a href="/settings/ai">명시적으로 provider 상태 확인</a></p>
   <label className="field"><span>질문 원본 기준</span><select disabled={busy} value={draft.mode} onChange={e=>{setDraft({...draft,mode:e.target.value as "pinned"|"current"});setPicked({});}}><option value="pinned">설명에 고정된 원본</option><option value="current">현재 원본을 한 번 캡처</option></select></label>
   <label className="choice"><input type="checkbox" checked={useDefaults} disabled={busy} onChange={e=>setUseDefaults(e.target.checked)}/>설명에 선언된 정확한 원본 선택 사용</label><p className="hint">원본을 찾지 못하면 한계로 남깁니다. 다른 원본이나 설명만으로 코드 확인 성공을 대신하지 않습니다.</p>
   {draft.sources.map(source=><p key={source.source_id}>준비할 때 기억된 원본 승인 {source.repo_id??source.source_id} · provider {source.provider_ids.join(", ")||"허용 없음"} · 보관 {source.retention_mode} · 마지막 가용성 {source.last_availability??"미관측"}{source.last_observed_at?` · ${source.last_observed_at}`:""}. 과거 관측은 이번 읽기 성공을 보장하지 않습니다.</p>)}
   {!useDefaults&&draft.sources.map(source=><fieldset key={source.source_id}><legend>{source.kind} · {source.repo_id??source.source_id}</legend>{draft.mode==="pinned"&&source.kind==="files"?<><button disabled={busy} onClick={()=>void loadRetained(source.source_id)}>보관 원본 목록 읽기</button>{(retained[source.source_id]??[]).map(row=><label className="choice" key={row.read_id}><input type="checkbox" disabled={busy||row.availability!=="available"||row.retention_mode!=="selected_snapshot"} checked={picked[source.source_id]?.[row.location?.path??""]===row.read_id} onChange={e=>{const path=row.location?.path;if(!path)return;setPicked(old=>{const next={...(old[source.source_id]??{})};if(e.target.checked)next[path]=row.read_id;else delete next[path];return {...old,[source.source_id]:next};});}}/>{row.location?.path??"경로 확인 불가"} · {row.captured_at} · {row.bytes_sha256.slice(0,12)}</label>)}{retainedCursor[source.source_id]&&<button disabled={busy} onClick={()=>void loadRetained(source.source_id,true)}>보관 원본 더 보기</button>}</>:<>{source.kind==="git"&&<label className="field"><span>정확한 Git revision · 선택</span><input type="text" disabled={busy} value={revisions[source.source_id]??""} onChange={e=>setRevisions(old=>({...old,[source.source_id]:e.target.value}))}/></label>}<label className="field"><span>읽을 원본 경로 · 한 줄에 하나</span><textarea disabled={busy} value={paths[source.source_id]??""} onChange={e=>setPaths(old=>({...old,[source.source_id]:e.target.value}))}/></label></>}</fieldset>)}
   <p>{draft.contextRef?"선택한 sealed context를 그대로 채택합니다.":draft.context!==null?"선택 문맥 전체를 별도 준비 작업으로 저장한 뒤 질문에 채택합니다.":"추가 표시 문맥 없음"}</p>
   <label className="field"><span>질문 본문</span><textarea id="ask-text" rows={6} disabled={busy} value={text} onChange={e=>setText(e.target.value)}/></label><button id="ask-submit" disabled={!canWrite||busy||!text.trim()||!draft.provider||!active} onClick={()=>void submit()}>질문 제출</button>
  </div>}
  <div id="ask-status-region" data-ask-status-region>{issue&&<p role="alert">{issue}</p>}{notice&&<p role="status">{notice}</p>}</div>
  {busy&&<button onClick={()=>{if(control.current)control.current.stopped=true;setNotice("전송 중지를 요청했습니다. 접수 여부는 원래 작업 ID로 확인합니다.");}}>질문 전송 중지</button>}
  <div aria-label="질문 복구 기록"><h3>원래 질문 작업</h3>{records.map(record=><article key={record.operationId}><p>{label({...record.request,version_id:record.versionId,page_query:record.request.page_query??"",page_fragment:record.request.page_fragment??""})}</p><p>원래 작업 ID {record.operationId} · {record.request.source_mode??"접수 시 기본값"}</p>{record.admissionRefusal?<><p>질문 접수 전 원본 정책 거부 · provider 작업이 시작되지 않았습니다. 원래 질문과 작업 ID는 보존됩니다.</p>{record.admissionRefusal.acknowledged?<p>접수 거부 확인됨 · 새 질문을 준비할 수 있습니다.</p>:<button disabled={!canWrite||busy} onClick={()=>void perform(async(own,authority)=>{const updated=await submission.acknowledgeRefusal(record,()=>guard(own,authority));guard(own,authority);setCurrent(updated);setNotice("접수 거부를 원래 이력에 보존했습니다. 새 질문을 명시적으로 준비하세요.");})}>질문 접수 거부 확인</button>}</>:<button disabled={!canWrite||busy} onClick={()=>void perform((own,authority)=>send(record,own,authority))}>{record.accepted?"원래 질문 작업 선택":"원래 질문 접수 확인·복구"}</button>}</article>)}</div>
  {current&&<div aria-label="선택한 원래 질문 작업"><p>{label({...current.request,version_id:current.versionId,page_query:current.request.page_query??"",page_fragment:current.request.page_fragment??""})} · {current.operationId}</p>{job&&<><p role="status">{job.state} {job.error}</p><button disabled={busy} onClick={()=>void refreshJob()}>질문 작업 상태 확인</button>{job.state==="paused"&&!current.resume&&<div className="form-row"><button disabled={!canWrite||busy} onClick={()=>void resume("reconcile_only")}>질문 원래 실행 상태만 확인</button><button disabled={!canWrite||busy} onClick={()=>void resume("continue")}>질문 저장 결과 계속 적용</button><button disabled={!canWrite||busy} onClick={()=>void resume("retry")}>질문 새 attempt 명시 재시도</button></div>}<button disabled={!canWrite||busy||job.cancel_requested||["completed","failed","cancelled"].includes(job.state)} onClick={()=>void perform(async(own,authority)=>{const accepted=current.accepted;if(!accepted||job.job_id!==accepted.job_id)throw Error("원래 질문 작업을 확인하세요.");await client.jobCancel(job.job_id,crypto.randomUUID());guard(own,authority);const next=await client.jobGet(job.job_id);guard(own,authority);setJob(next);})}>질문 작업 취소 요청</button></>}{current.resume&&<div role="status">{current.resume.refused?resumeRefusalText(current.resume.refused):"원래 재개 결정 결과가 확인되지 않았습니다."}<button disabled={!canWrite||busy} onClick={()=>void resume()}>원래 질문 재개 결과 확인</button>{current.resume.refused&&<button disabled={!canWrite||busy} onClick={()=>void acknowledge()}>질문 재개 거부 확인 · 현재 상태 읽기</button>}</div>}{current.accepted&&<button disabled={busy} onClick={()=>void loadQuestion(current.accepted!.question_id)}>이 질문의 저장된 답변 읽기</button>}</div>}
  <h3>저장된 질문 이력</h3><label className="field"><span>질문이 저장된 버전</span><select disabled={busy} value={historyVersion} onChange={e=>setHistoryVersion(e.target.value)}>{versions.map(v=><option key={v.version_id} value={v.version_id}>v{v.sequence} · {v.version_id}</option>)}</select></label>{versionCursor&&<button disabled={busy} onClick={()=>void perform(async(own,authority)=>{const page=await client.documentVersions(documentId,versionCursor);guard(own,authority);setVersions(old=>[...old,...page.items]);setVersionCursor(page.next_cursor);})}>이전 버전 더 읽기</button>}
  <button disabled={busy} onClick={()=>setRefresh(n=>n+1)}>질문 이력 새로고침</button>{historyState.phase==="loading"&&<p role="status">이 버전의 질문 이력을 읽는 중입니다.</p>}{historyState.phase==="error"&&<p role="alert">질문 이력을 읽지 못했습니다. {message(historyState.error)} 새로고침으로 다시 확인하세요.</p>}{historyState.phase==="ready"&&history.length===0&&<p>이 버전에 저장된 질문이 없습니다.</p>}{history.map(q=><article key={q.question_id}><p>{label(q)} · {q.created_at} · {q.job_state}</p><button disabled={busy} onClick={()=>void loadQuestion(q.question_id)}>저장된 질문과 답변 읽기</button></article>)}{historyCursor&&historyState.phase==="ready"&&<button disabled={busy} onClick={()=>void perform(async(own,authority)=>{await historyQuery.load(historyVersion,()=>guard(own,authority),true);})}>질문 이력 더 읽기</button>}
  {saved&&<article aria-label="저장된 질문 상세"><h3>저장된 질문</h3><p>{label(saved)}</p><a href={pinnedReaderURL(saved.document_id,saved.version_id,saved.page_path,saved.page_query,saved.page_fragment,saved.region_id)}>질문의 정확한 위치 열기</a><SafeMarkdown text={savedQuestionText}/><p>원본 기준 {saved.source_mode} · target {saved.target_set_id??"확인된 원본 target 없음"}</p><details><summary>실제로 캡처한 원본 target과 선택</summary><pre className="note-context-text">{JSON.stringify({targets:saved.targets,requested_selection:saved.requested_selection},null,2)}</pre></details>{saved.limitations.map((limit,i)=><p key={i}>{limit.code} · {limit.message}</p>)}
   {saved.context_ref&&<button disabled={busy} onClick={()=>void perform(async(own,authority)=>{const id=saved.question_id,era=selectionEra.current;const current=()=>{guard(own,authority);if(savedId.current!==id||era!==selectionEra.current)throw Error("이전 질문 문맥입니다.");};const body=await questionStreamText(await client.questionContextStream(id),current);const text=await prepareNoteContext(body,current);current();setContextText(text);})}>질문에 저장된 문맥 전체 읽기</button>}{contextText!==null&&<pre className="note-context-text" tabIndex={0} aria-label="저장된 질문 문맥 전체">{contextText.parts.map((part,index)=><span key={index}>{part}</span>)}</pre>}
   {saved.answers?.items.map(answer=><section key={answer.answer_id} aria-label={`저장된 ${answer.state} 답변`}><h4>{answer.state==="final"?"완료 답변":answer.state==="partial"?"부분 답변 · 완료 아님":"오류 기록 · 완료 답변 아님"}</h4><p>{answer.created_at} · producing attempt {answer.attempt_id}</p><button disabled={busy} onClick={()=>void readAnswer(answer)}>답변 전체 읽기</button>{answerText[answer.answer_id]!==undefined&&<SafeMarkdown text={answerText[answer.answer_id]!}/>}<p>근거 획득 기록과 주장에 대한 의미적 지지는 별개입니다.</p>{answer.citations.map((citation,index)=><div key={index}><p>{citation.basis==="observed_input"?"실제 공급자 입력 관측":"사후 바이트 확인 · 원래 공급자 입력 증명 아님"} · {citation.availability} {citation.reason??""}</p><p>input {citation.input_origin??"없음"} · observed {citation.observed_at??"없음"} · verified {citation.verified_at??"없음"} · bytes {citation.bytes_origin??"읽기 가능 origin 없음"}</p><button disabled={busy||citation.availability!=="available"||citation.bytes_origin!=="retained_snapshot"} onClick={()=>void readCitation(answer,index)}>인용한 보관 원본 전체 읽기</button>{citationText[answer.answer_id+":"+index]!==undefined&&<pre className="note-context-text" tabIndex={0} aria-label="인용한 정확한 보관 원본">{citationText[answer.answer_id+":"+index]}</pre>}<details><summary>정확한 source·location·digest</summary><pre className="note-context-text">{JSON.stringify({target_set_id:citation.target_set_id,source:citation.source,location:citation.location,bytes_sha256:citation.bytes_sha256,read_id:citation.read_id,verification_id:citation.verification_id},null,2)}</pre></details></div>)}{answer.explanation_references.map((ref,index)=><p key={index}>설명 참조 · {ref.version_id} · {ref.page_path??"전체 버전"} · {ref.bytes_sha256} · 프로젝트 원본 관측 아님</p>)}{answer.limitations.map((limit,index)=><p key={index}>{limit.code} · {limit.message}</p>)}</section>)}{saved.answers?.next_cursor&&<button disabled={busy} onClick={()=>void perform(async(own,authority)=>{const id=saved.question_id,era=selectionEra.current;const page=await client.questionAnswers(id,saved.answers!.next_cursor!);guard(own,authority);if(savedId.current===id&&era===selectionEra.current)setSaved({...saved,answers:{items:[...saved.answers!.items,...page.items],next_cursor:page.next_cursor}});})}>답변 이력 더 읽기</button>}
  </article>}
 </section>;
}
