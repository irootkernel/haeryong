import type { GenerationReference, HaeryongClient, SearchHit, ProjectView, DocumentView, JobView } from "../../client/types.ts";

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
