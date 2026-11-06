import type { GenerationReference, HaeryongClient, SearchHit, ProjectView, DocumentView, JobView, ProjectSourceView, DraftView } from "../../client/types.ts";

/** Pin the exact selected search page; never replace it with the document's latest version. */
export async function pinGenerationContext(client:HaeryongClient,hit:SearchHit):Promise<GenerationReference> {
 if(hit.page_path===null || !["page","region"].includes(hit.kind))throw new Error("설명 페이지를 선택해 주세요.");
 let cursor:string|undefined;
 do {
  const page=await client.versionFiles(hit.version_id,cursor);
  const file=page.items.find(file=>file.path===hit.page_path && file.kind==="file");
  if(file){if(!file.bytes_sha256 || !/^[a-f0-9]{64}$/.test(file.bytes_sha256))throw new Error("선택한 페이지의 바이트 확인이 필요합니다.");return {version_id:hit.version_id,page_path:hit.page_path,region_id:hit.region_id,bytes_sha256:file.bytes_sha256};}
  cursor=page.next_cursor??undefined;
 }while(cursor!==undefined);
 throw new Error("선택한 버전에서 페이지를 읽을 수 없습니다.");
}

/** Fill only an as-yet-unselected form after asynchronous Library discovery. */
export function initialGenerationProject(current:string,explicit:boolean,librarySelection:string,projects:ReadonlyArray<{project_id:string}>):string {
 return current || (explicit?"":librarySelection || projects[0]?.project_id || "");
}

export function generationSourceMode(project:string,overrides:Readonly<Record<string,"current"|"pinned">>,defaults:Readonly<Record<string,"current"|"pinned">>):"current"|"pinned"|null {
 return overrides[project] ?? defaults[project] ?? null;
}

/** Display names may change; every action still belongs to the saved request. */
export function savedGenerationScope(record:import("../../client/generation.ts").GenerationRecord,projects:ReadonlyArray<Pick<ProjectView,"project_id"|"name">>,documents:Readonly<Record<string,DocumentView|null|undefined>>) {
 const request=record.request,documentId=request.document_id??null;
 const detail=documentId===null?undefined:documents[documentId];
 const matching=detail&&detail.document_id===documentId&&detail.project_id===request.project_id?detail:null;
 return {
  projectId:request.project_id,
  projectName:projects.find(project=>project.project_id===request.project_id)?.name??"프로젝트 이름 확인 불가",
  documentId,
  documentTitle:documentId===null?"새 문서":matching?matching.title:detail===undefined?"문서 제목 읽는 중":"문서 제목 확인 불가",
  baseVersionId:request.base_version_id??null,
  intent:request.intent,
  operationId:record.operationId,
 };
}

/** A failed/stopped selection cannot put another request's job behind its actions. */
export function savedGenerationJob(record:import("../../client/generation.ts").GenerationRecord|null,job:JobView|null):JobView|null {
 return record?.accepted&&job?.job_id===record.accepted.job_id?job:null;
}

/** Enumerate all pages of project sources using the supplied active or passive client. */
export async function drainProjectSources(client:HaeryongClient,projectId:string):Promise<ProjectSourceView[]> {
 const page=await client.projectSources(projectId);
 const all=[...page.items];
 let cursor=page.next_cursor;
 while(cursor){
  const next=await client.projectSources(projectId,cursor);
  all.push(...next.items);
  cursor=next.next_cursor;
 }
 return all;
}

/** Preserve owner-edited git revisions while adopting fresh server last_observed_revision for unedited sources. */
export function mergeRefreshedSourceRevisions(
 currentRevisions:Readonly<Record<string,string>>,
 editedRevisions:Readonly<Record<string,boolean>>,
 sources:ReadonlyArray<ProjectSourceView>,
):Record<string,string> {
 return Object.fromEntries(
  sources.map(source=>[
   source.source_id,
   editedRevisions[source.source_id]&&currentRevisions[source.source_id]!==undefined
    ?currentRevisions[source.source_id]!
    :(source.last_observed_revision??""),
  ]),
 );
}

export interface GenerationMountedRefreshInput {
 passiveClient:HaeryongClient;
 projects:ReadonlyArray<Pick<ProjectView,"project_id"|"name">>;
 project:string;
 active:import("../../client/generation.ts").GenerationRecord|null;
 records:ReadonlyArray<import("../../client/generation.ts").GenerationRecord>;
 revisions:Readonly<Record<string,string>>;
 editedRevisions:Readonly<Record<string,boolean>>;
 canApply:()=>boolean;
 isCurrentProject:(projectId:string)=>boolean;
 isCurrentActive:(operationId:string|null,jobId:string|null)=>boolean;
 applyDefaults:(
  sourceDefaults:Record<string,"current"|"pinned">,
  providerDefaults:Record<string,string>,
 )=>void;
 applyProjectData:(data:{
  documents:DocumentView[];
  documentCursor:string|null;
  sources:ProjectSourceView[];
  revisions:Record<string,string>;
 })=>void;
 applyJob:(job:JobView)=>void;
 applyDocumentLabels?:(labels:Record<string,DocumentView|null>)=>void;
}

/**
 * Passive background/resync refresh for mounted GenerationControl server snapshots.
 * Uses passiveClient only; preserves local form inputs, overrides, references, retained
 * source choices, and journaled generation/resume records.
 */
export async function refreshGenerationMountedState(input:GenerationMountedRefreshInput):Promise<boolean> {
 const projectSnap=input.project;
 const activeOpId=input.active?.operationId??null;
 const activeJobId=input.active?.accepted?.job_id??null;
 const labelIds=[...new Set([...input.records,...(input.active?[input.active]:[])].map(r=>r.request.document_id).filter((id):id is string=>!!id))].sort();
 const [configView,projectResult,jobResult,labelResults]=await Promise.all([
  input.passiveClient.configurationView(),
  projectSnap
   ?Promise.all([input.passiveClient.listDocuments({projectId:projectSnap}),drainProjectSources(input.passiveClient,projectSnap)])
   :Promise.resolve(null),
  activeJobId?input.passiveClient.jobGet(activeJobId):Promise.resolve(null),
  labelIds.length>0&&input.applyDocumentLabels
   ?Promise.allSettled(labelIds.map(id=>input.passiveClient.documentDetail(id)))
   :Promise.resolve(null),
 ]);
 if(!input.canApply())return false;
 const projectIds=new Set<string>([
  ...input.projects.map(p=>p.project_id),
  ...Object.keys(configView.configuration.projects??{}),
  ...(projectSnap?[projectSnap]:[]),
 ]);
 const sourceDefaults=Object.fromEntries(
  [...projectIds].map(id=>[id,configView.configuration.projects?.[id]?.source_mode_default??"pinned"]),
 );
 const providerDefaults=Object.fromEntries(
  [...projectIds].map(id=>[id,configView.configuration.projects?.[id]?.provider_id??""]),
 );
 input.applyDefaults(sourceDefaults,providerDefaults);
 if(projectResult!==null&&input.isCurrentProject(projectSnap)){
  const [docs,allSources]=projectResult;
  input.applyProjectData({
   documents:docs.items,
   documentCursor:docs.next_cursor,
   sources:allSources,
   revisions:mergeRefreshedSourceRevisions(input.revisions,input.editedRevisions,allSources),
  });
 }
 if(jobResult!==null&&input.isCurrentActive(activeOpId,activeJobId)){
  input.applyJob(jobResult);
 }
 if(labelResults!==null&&input.applyDocumentLabels){
  const items:Record<string,DocumentView|null>={};
  labelResults.forEach((res,idx)=>{items[labelIds[idx]!]=res.status==="fulfilled"?res.value:null;});
  input.applyDocumentLabels(items);
 }
 return true;
}

export interface DraftMountedRefreshInput {
 passiveClient:HaeryongClient;
 project:string;
 selectedId:string|null;
 includeHistory:boolean;
 canApply:()=>boolean;
 isCurrentProject:(projectId:string)=>boolean;
 isCurrentSelected:(draftId:string|null)=>boolean;
 isHistoryOpen:()=>boolean;
 applyList:(items:DraftView[],nextCursor:string|null)=>void;
 applySelected:(draft:DraftView)=>void;
 applyHistory:(items:DraftView[],nextCursor:string|null)=>void;
 applyDocumentLabels?:(labels:Record<string,DocumentView|null>)=>void;
}

/**
 * Passive background/resync refresh for mounted DraftControl server snapshots.
 * Uses passiveClient only; preserves selected replacement input, native source handle,
 * and pending/refused localStorage draft action records (original K, body, and ETag).
 */
export async function refreshDraftMountedState(input:DraftMountedRefreshInput):Promise<boolean> {
 const projectSnap=input.project;
 const selectedIdSnap=input.selectedId;
 const historySnap=input.includeHistory&&selectedIdSnap!==null;
 const [listPage,selectedDraft,historyPage]=await Promise.all([
  input.passiveClient.draftList(projectSnap||undefined),
  selectedIdSnap!==null?input.passiveClient.draftGet(selectedIdSnap):Promise.resolve(null),
  historySnap&&selectedIdSnap!==null?input.passiveClient.draftHistory(selectedIdSnap):Promise.resolve(null),
 ]);
 if(!input.canApply())return false;
 const keepSelected=selectedDraft!==null&&input.isCurrentSelected(selectedIdSnap);
 if(input.isCurrentProject(projectSnap)){
  const rows=keepSelected
   ?listPage.items.map(row=>row.draft_id===selectedDraft.draft_id?selectedDraft:row)
   :listPage.items;
  input.applyList(rows,listPage.next_cursor);
 }
 if(keepSelected){
  input.applySelected(selectedDraft);
 }
 if(historyPage!==null&&keepSelected&&input.isHistoryOpen()){
  input.applyHistory(historyPage.items,historyPage.next_cursor);
 }
 if(input.applyDocumentLabels){
  const docIds=[...new Set([...listPage.items.map(r=>r.document_id),selectedDraft?.document_id].filter((id):id is string=>!!id))].sort();
  if(docIds.length>0){
   const labelResults=await Promise.allSettled(docIds.map(id=>input.passiveClient.documentDetail(id)));
   if(!input.canApply())return false;
   const items:Record<string,DocumentView|null>={};
   labelResults.forEach((res,idx)=>{items[docIds[idx]!]=res.status==="fulfilled"?res.value:null;});
   input.applyDocumentLabels(items);
  }else{
   input.applyDocumentLabels({});
  }
 }
 return true;
}
