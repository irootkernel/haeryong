import {ApiError} from "../../client/types.ts";
import type {AnnotationAnchor,AnnotationCreate,AnnotationPatch,AnnotationView,AnnotationStream,HaeryongClient,UploadCheckpoint} from "../../client/types.ts";
import {readStream,sha256Hex} from "./evidence-view.ts";
export interface NoteDraft {id:string;revision:number;anchor:AnnotationAnchor;text:string;selectedText:string|null;contextRef:string|null;context:unknown|null;base:AnnotationView|null}
export interface NoteIntent {kind:"create"|"patch"|"delete";target:string;key:string;generation:string;etag:string|null;body:AnnotationCreate|AnnotationPatch|null;draftSave?:{id:string;revision:number};rejected?:boolean;preparation?:{createKey:string;sealKey:string;uploadId:string|null;offset:number;complete:UploadCheckpoint|null}}
export interface NoteRecord {draft:NoteDraft|null;pending:NoteIntent|null}
// Ephemeral admission snapshot, never part of the persisted journal contract.
export function noteRecordIdentity(record:NoteRecord):string{return JSON.stringify([record.draft?.id??null,record.draft?.revision??null,record.pending]);}
export function requireNoteRecord(record:NoteRecord,expected:string):void{if(noteRecordIdentity(record)!==expected)throw Error("초안이나 저장 요청이 바뀌었습니다. 현재 내용을 확인하세요.");}
export function requireNotePending(record:NoteRecord,key:string,priorRejected:string|null):void{const pending=record.pending;if(pending&&pending.key!==key&&!(pending.rejected&&pending.key===priorRejected))throw Error("다른 저장 요청을 먼저 확인하세요.");}
export function noteBindingChanged(a:AnnotationAnchor,b:AnnotationAnchor):boolean{return a.version_id!==b.version_id||a.page_path!==b.page_path;}
export function noteAnchorChanged(a:AnnotationAnchor,b:AnnotationAnchor):boolean{return noteBindingChanged(a,b)||a.region_id!==b.region_id||a.page_query!==b.page_query||a.page_fragment!==b.page_fragment;}
export function dropNoteContext(draft:NoteDraft):NoteDraft{return {...draft,revision:draft.revision+1,selectedText:null,contextRef:null,context:null};}
export function reconcileNoteDraft(draft:NoteDraft,server:AnnotationView,context:"keep"|"drop"):NoteDraft {
 if(draft.base?.annotation_id!==server.annotation_id||server.deleted_at)throw Error("병합할 노트가 일치하지 않습니다.");
 if(noteAnchorChanged(draft.anchor,server.anchor)&&context!=="drop")throw Error("서버 위치가 바뀌었습니다. 이전 문맥을 제외하고 병합하세요.");
 const next=context==="drop"?dropNoteContext(draft):{...draft,revision:draft.revision+1};
 return {...next,anchor:server.anchor,base:server};
}
export function noteSaveIntent(draft:NoteDraft,generation:string,key:string):NoteIntent {
 const base=draft.base;
 if(base&&noteBindingChanged(draft.anchor,base.anchor))throw Error("기준 개정의 위치와 이전 초안 문맥을 먼저 조정하세요.");
 return {draftSave:{id:draft.id,revision:draft.revision},kind:base?"patch":"create",target:base?.annotation_id??draft.anchor.version_id,key,etag:base?.etag??null,generation,body:base?{action:"edit",body:draft.text,body_ref:null,selected_text:draft.selectedText,context:null,context_ref:draft.contextRef}:{page_path:draft.anchor.page_path,region_id:draft.anchor.region_id,page_query:draft.anchor.page_query,page_fragment:draft.anchor.page_fragment,body:draft.text,body_ref:null,selected_text:draft.selectedText,context:draft.contextRef?null:draft.context,context_ref:draft.contextRef}};
}
// Only a Save owns a draft. Delete/reattach and obsolete completions cannot
// clear it or replace its base, including when text changes during dispatch.
export function ownsNoteDraft(intent:NoteIntent,draft:NoteDraft|null):boolean {
 return !!draft&&intent.draftSave?.id===draft.id&&
  (intent.kind==="create"&&!draft.base&&intent.target===draft.anchor.version_id||
   intent.kind==="patch"&&intent.body!==null&&"action" in intent.body&&intent.body.action==="edit"&&intent.target===draft.base?.annotation_id);
}
export function completeNoteIntent(record:NoteRecord,intent:NoteIntent,value:AnnotationView):NoteRecord {
 if(record.pending?.key!==intent.key)return record;
 let draft=record.draft;
 if(ownsNoteDraft(intent,draft)) {
  if(intent.kind==="patch"&&value.annotation_id!==intent.target||intent.kind==="create"&&value.anchor.version_id!==intent.target)throw Error("노트 저장 대상이 일치하지 않습니다.");
  draft=draft!.revision===intent.draftSave!.revision?null:{...draft!,base:value};
 }
 return {draft,pending:null};
}
export async function noteStreamText(stream:AnnotationStream,guard:()=>void){try{const bytes=await readStream(stream.body,()=>{try{guard();return false;}catch{return true;}});guard();if(String(bytes.length)!==stream.byte_size||await sha256Hex(bytes)!==stream.bytes_sha256)throw Error("저장된 노트 바이트가 일치하지 않습니다.");guard();return new TextDecoder("utf-8",{fatal:true}).decode(bytes);}finally{stream.cancel();}}
export async function noteComparisonText(client:HaeryongClient,server:AnnotationView,guard:()=>void):Promise<string>{
 const text=server.body??(server.body_ref?await noteStreamText(await client.annotationBodyStream(server.annotation_id,server.revision),guard):"");guard();return text;
}
export class NoteJournal {
 readonly storage:Pick<Storage,"getItem"|"setItem">;readonly key:string;private snapshot:string|null;
 constructor(storage:Pick<Storage,"getItem"|"setItem">,namespace:string){this.storage=storage;this.key="haeryong.notes.v1:"+namespace;this.snapshot=storage.getItem(this.key);}
 read():NoteRecord{const raw=this.snapshot;if(!raw)return {draft:null,pending:null};const r=JSON.parse(raw) as NoteRecord;if(!r||!("draft" in r)||!("pending" in r)||r.draft&&(!r.draft.anchor||typeof r.draft.text!=="string")||r.pending&&(!["create","patch","delete"].includes(r.pending.kind)||typeof r.pending.key!=="string"))throw Error("노트 복구 기록을 확인할 수 없습니다.");if(r.draft){r.draft.id??=crypto.randomUUID();r.draft.revision??=0;}return r;}
 async write(record:NoteRecord,guard:()=>void=()=>{}){guard();if(!navigator.locks)throw Error("노트 동시 저장 잠금을 사용할 수 없습니다.");await navigator.locks.request(this.key,()=>{if(this.storage.getItem(this.key)!==this.snapshot)throw Error("다른 창의 노트 초안을 먼저 확인하세요.");const raw=JSON.stringify(record);guard();this.storage.setItem(this.key,raw);this.snapshot=raw;});}
 async dispatch<T>(intent:NoteIntent,guard:()=>void,start:()=>Promise<T>):Promise<T>{
  guard();if(!navigator.locks)throw Error("노트 동시 저장 잠금을 사용할 수 없습니다.");
  let outcome:Promise<{ok:true;value:T}|{ok:false;error:unknown}>|undefined;
  await navigator.locks.request(this.key,()=>{
   if(this.storage.getItem(this.key)!==this.snapshot)throw Error("다른 창의 노트 초안을 먼저 확인하세요.");
   if(JSON.stringify(this.read().pending)!==JSON.stringify(intent))throw Error("원래 저장 요청을 로컬에 보관한 뒤 다시 확인하세요.");
   guard();
   // Admit the real client call synchronously under the lock. Do not hold the
   // lock during network I/O: typing must still be durably journalled.
   outcome=start().then(value=>({ok:true as const,value}),error=>({ok:false as const,error}));
  });
  if(!outcome)throw Error("노트 요청이 시작되지 않았습니다.");
  const result=await outcome;if(!result.ok)throw result.error;return result.value;
 }
}
export function definitiveNoteRejection(error:unknown):boolean{return error instanceof ApiError&&[400,403,404,409,410,412,428].includes(error.status)&&error.code!=="OPERATION_RECONCILIATION_REQUIRED";}
export async function resolveRejectedNoteAction(
 admitted:{current:boolean},
 setBusy:(busy:boolean)=>void,
 pending:NoteIntent|null,
 runtimeGeneration:string,
 fetchLatest:(targetId:string)=>Promise<AnnotationView>,
 onDeleted:()=>Promise<void>,
 submitNext:(nextIntent:NoteIntent)=>Promise<void>,
 createKey:()=>string=()=>crypto.randomUUID(),
):Promise<boolean>{
 if(!pending||!pending.rejected||admitted.current)return false;
 admitted.current=true;
 setBusy(true);
 try{
  const fresh=await fetchLatest(pending.target);
  if(fresh.deleted_at){
   await onDeleted();
   return true;
  }
  const nextIntent:NoteIntent={
   ...structuredClone(pending),
   key:createKey(),
   etag:fresh.etag,
   generation:runtimeGeneration,
  };
  delete nextIntent.rejected;
  await submitNext(nextIntent);
  return true;
 }finally{
  admitted.current=false;
  setBusy(false);
 }
}
export async function dispatchNote(client:HaeryongClient,intent:NoteIntent):Promise<AnnotationView>{
 if(intent.kind==="create")return client.annotationCreate(intent.target,intent.body as AnnotationCreate,intent.key);
 if(intent.kind==="delete")return client.annotationDelete(intent.target,intent.etag!,intent.key);
 return client.annotationPatch(intent.target,intent.body as AnnotationPatch,intent.etag!,intent.key);
}

// These are transport chunk choices, never note content limits.
export async function prepareNoteBody(client:HaeryongClient,intent:NoteIntent,guard:()=>void,save:(next:NoteIntent)=>Promise<void>):Promise<NoteIntent>{
 if(intent.kind==="delete"||!intent.body||!("body" in intent.body)||intent.body.body===null||new TextEncoder().encode(intent.body.body).length<65536)return intent;
 const text=intent.body.body,bytes=new TextEncoder().encode(text);
 let current:NoteIntent=structuredClone(intent);
 if(!current.preparation){current.preparation={createKey:crypto.randomUUID(),sealKey:crypto.randomUUID(),uploadId:null,offset:0,complete:null};await save(current);}
 const preparation=current.preparation!;
 const checkpoint=async()=>{current={...current,preparation:{...preparation}};await save(current);guard();};
 if(!preparation.uploadId){
  guard();const target=intent.kind==="create"?{operation_id:intent.key,action:"annotation_create" as const,version_id:intent.target,page_path:(intent.body as AnnotationCreate).page_path}:{operation_id:intent.key,action:"annotation_edit" as const,annotation_id:intent.target};
  const created=await client.createUpload({purpose:"body",input_mode:"object",target},preparation.createKey);guard();preparation.uploadId=created.upload_id;await checkpoint();
 }
 let detail=await client.uploadDetail(preparation.uploadId!);guard();
 if(detail.state==="open"){
  while(preparation.offset<bytes.length){const segment=bytes.subarray(preparation.offset,Math.min(preparation.offset+262144,bytes.length));const digest=Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256",segment))).map(n=>n.toString(16).padStart(2,"0")).join("");guard();const ack=await client.putUploadEntry(preparation.uploadId!,{path:"_input",kind:"file",offset:preparation.offset,bytes:segment,chunkSha256:digest,totalLength:bytes.length,final:preparation.offset+segment.length===bytes.length});guard();preparation.offset=Number(ack.accepted_offset);if(preparation.offset===bytes.length)preparation.complete={...ack.checkpoint,generation:ack.checkpoint.generation+1,complete_input:true};await checkpoint();}
  if(!preparation.complete)throw Error("노트 업로드 완료 지점을 확인할 수 없습니다.");
  await client.completeUpload(preparation.uploadId!,preparation.complete,preparation.sealKey);guard();detail=await client.uploadDetail(preparation.uploadId!);guard();
 }
 while(detail.state==="sealing"){await new Promise(resolve=>setTimeout(resolve,200));guard();detail=await client.uploadDetail(preparation.uploadId!);guard();}
 if(detail.state!=="sealed"||detail.sealed_object_ref?.byte_size!==String(bytes.length))throw Error("노트 본문 준비가 완료되지 않았습니다.");
 const digest=Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256",bytes))).map(n=>n.toString(16).padStart(2,"0")).join("");if(detail.sealed_object_ref.bytes_sha256!==digest)throw Error("노트 본문 바이트가 일치하지 않습니다.");
 guard();current={...current,body:{...current.body!,body:null,body_ref:{upload_id:preparation.uploadId!}} as AnnotationCreate|AnnotationPatch};await save(current);guard();return current;
}
