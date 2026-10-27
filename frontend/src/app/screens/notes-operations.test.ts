import {test} from "node:test";
import assert from "node:assert/strict";
import {NoteJournal,prepareNoteBody,dispatchNote,definitiveNoteRejection,noteSaveIntent,completeNoteIntent,noteComparisonText,reconcileNoteDraft,noteBindingChanged,noteRecordIdentity,requireNoteRecord,requireNotePending,type NoteDraft,type NoteRecord,type NoteIntent} from "./notes-operations.ts";
import {ApiError,type HaeryongClient,type AnnotationView,type AnnotationStream} from "../../client/types.ts";
import {prepareNoteContext} from "./notes-context.ts";
const anchor={version_id:"original",page_path:"pages/한글.html",region_id:"retry",page_query:"q=+%2F",page_fragment:"part"};
test("Task6 complete context preserves boundary graphemes and literal code units without a content quota",async()=>{
 const samples=["","x","a".repeat(4095),"a".repeat(4096),"a".repeat(4097),"a".repeat(4095)+"🌊\r\ne\u0301👩‍👩‍👧‍👦🇰🇷", "x"+"\u0301".repeat(5000)+"z", "\r\n\r\n\t  \n", "مرحبا بالعالم אבג (abc)\u2067عربي\u2069", "<script>literal</script>\0\r", "\n".repeat(10000)];
 for(const text of samples){
  const value=await prepareNoteContext(text,()=>{});
  assert.equal(value.parts.join(""),text);assert.deepEqual(new TextEncoder().encode(value.parts.join("")),new TextEncoder().encode(text));
  let offset=0;const cuts=new Set([0,...[...new Intl.Segmenter(undefined,{granularity:"grapheme"}).segment(text)].map(s=>s.index+s.segment.length)]);
  for(const part of value.parts){offset+=part.length;assert.ok(cuts.has(offset));}
  assert.equal(offset,text.length);assert.equal(value.length,text.length);
 }
});
test("Task6 preparation yields to host tasks and retirement fences every preparation boundary",async()=>{
 const text="ab🌊".repeat(30000);
 let turn=false;setTimeout(()=>{turn=true;},0);await prepareNoteContext("hello",()=>{});assert.equal(turn,true);
 for(const retireAt of [0,1,2,3]){
  let retired=retireAt===0,yields=0,published=false;
  const guard=()=>{if(retired)throw Error("retired owned read");};
  await assert.rejects(async()=>{await prepareNoteContext(text,guard,async()=>{if(++yields===retireAt)retired=true;});published=true;},/retired owned read/);
  assert.equal(published,false);
 }
 let guards=0;await assert.rejects(prepareNoteContext("x",()=>{if(++guards===4)throw Error("retired before complete publication");},async()=>{}),/retired before complete publication/);
 const descriptor=Object.getOwnPropertyDescriptor(Intl,"Segmenter")!;
 try{Object.defineProperty(Intl,"Segmenter",{...descriptor,value:undefined});await assert.rejects(prepareNoteContext("x",()=>{}),/준비할 수 없습니다/);}finally{Object.defineProperty(Intl,"Segmenter",descriptor);}
});
test("T-005-02-02 draft and immutable pending request survive conflict and another window cannot overwrite them",async()=>{
 const original=Object.getOwnPropertyDescriptor(globalThis,"navigator");Object.defineProperty(globalThis,"navigator",{configurable:true,value:{locks:{request:async(_name:string,fn:()=>unknown)=>fn()}}});
 try{const values=new Map<string,string>();const storage={getItem:(k:string)=>values.get(k)??null,setItem:(k:string,v:string)=>{values.set(k,v);}};const first=new NoteJournal(storage,"owner/library/original"),other=new NoteJournal(storage,"owner/library/original");
 const intent:NoteIntent={kind:"patch",target:"note",key:"original-key",generation:"before",etag:'"original"',body:{action:"edit",body:"unsaved local",body_ref:null,selected_text:"text",context:null,context_ref:null}};
 const r:NoteRecord={draft:{id:"draft-a",revision:0,anchor,text:"unsaved local",selectedText:"text",contextRef:null,context:null,base:null},pending:intent};await first.write(r);
 const calls:unknown[][]=[];const client={annotationPatch:async(...args:unknown[])=>{calls.push(args);throw new ApiError(412,"PRECONDITION_FAILED",false);}} as unknown as HaeryongClient;
 await assert.rejects(dispatchNote(client,intent));assert.deepEqual(calls[0],["note",intent.body,'"original"',"original-key"]);
 const recovered=new NoteJournal(storage,"owner/library/original").read();assert.deepEqual(recovered,r);assert.equal(recovered.draft?.text,"unsaved local");assert.equal(recovered.pending?.etag,'"original"');await assert.rejects(other.write({draft:null,pending:null}),/다른 창/);
 assert.equal(definitiveNoteRejection(new ApiError(412,"PRECONDITION_FAILED",false)),true);assert.equal(definitiveNoteRejection(new ApiError(409,"OPERATION_RECONCILIATION_REQUIRED",true)),false);assert.equal(definitiveNoteRejection(Error("lost response")),false);
 }finally{if(original)Object.defineProperty(globalThis,"navigator",original);else Reflect.deleteProperty(globalThis,"navigator");}
});

test("large note preparation resumes the same upload and seal identities without changing the final Save intent",async()=>{
 const text="🌊".repeat(20000),bytes=new TextEncoder().encode(text),digest=Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256",bytes))).map(n=>n.toString(16).padStart(2,"0")).join("");
 let saved:NoteIntent|undefined,sealed=false,lose=true;const calls:unknown[][]=[];
 const client={
  createUpload:async(...args:unknown[])=>{calls.push(["create",...args]);return {upload_id:"upload"};},
  uploadDetail:async()=>({state:sealed?"sealed":"open",sealed_object_ref:sealed?{bytes_sha256:digest,byte_size:String(bytes.length)}:null}),
  putUploadEntry:async(_id:string,chunk:{bytes:Uint8Array})=>{calls.push(["chunk",chunk.bytes]);return {accepted_offset:String(bytes.length),checkpoint:{generation:2,inventory_sha256:"hash",accepted_bytes:String(bytes.length),complete_input:false}};},
  completeUpload:async(...args:unknown[])=>{calls.push(["seal",...args]);if(lose){lose=false;throw Error("lost seal acknowledgement");}sealed=true;return {kind:"accepted",body:{}};},
 } as unknown as HaeryongClient;
 const intent:NoteIntent={kind:"create",target:"original",key:"final-key",generation:"before",etag:null,body:{page_path:anchor.page_path,region_id:anchor.region_id,page_query:anchor.page_query,page_fragment:anchor.page_fragment,body:text,body_ref:null,selected_text:null,context:null,context_ref:null}};
 await assert.rejects(prepareNoteBody(client,intent,()=>{},async next=>{saved=structuredClone(next);}),/lost seal/);
 const resumed=await prepareNoteBody(client,saved!,()=>{},async next=>{saved=structuredClone(next);});
 assert.equal(calls.filter(c=>c[0]==="create").length,1);assert.equal(calls.filter(c=>c[0]==="chunk").length,1);const seals=calls.filter(c=>c[0]==="seal");assert.equal(seals.length,2);assert.equal(seals[0]![3],seals[1]![3]);assert.notEqual(seals[0]![3],"final-key");assert.deepEqual((calls[0]![1] as {target:unknown}).target,{operation_id:"final-key",action:"annotation_create",version_id:"original",page_path:anchor.page_path});assert.equal(resumed.key,"final-key");assert.equal((resumed.body as {body:null}).body,null);assert.deepEqual((resumed.body as {body_ref:unknown}).body_ref,{upload_id:"upload"});assert.equal(intent.body&&"body" in intent.body?intent.body.body:null,text);
});


function note(id:string,revision=1):AnnotationView {
 return {annotation_id:id,document_id:"document",original_anchor:anchor,anchor,body:"saved "+id,body_ref:null,selected_text:null,context_ref:null,created_at:"now",updated_at:"now",deleted_at:null,revision,etag:'"'+id+revision+'"',history_url:"/history",resolution:{status:"resolved",reason:null},action:"edit",actor_session:"session"};
}
function draft(base:AnnotationView|null):NoteDraft{return {id:crypto.randomUUID(),revision:0,anchor,text:"draft A",selectedText:null,contextRef:null,context:null,base};}
function deferred<T>(){let resolve!:(value:T)=>void;const promise=new Promise<T>(done=>{resolve=done;});return {promise,resolve};}

test("Task6 consent snapshots reject new drafts, newer revisions and pending changes without altering records",()=>{
 const original:NoteRecord={draft:null,pending:null},empty=noteRecordIdentity(original);
 requireNoteRecord(original,empty);
 const local=draft(null),opened:NoteRecord={draft:local,pending:null},captured=noteRecordIdentity(opened);
 assert.throws(()=>requireNoteRecord(opened,empty),/초안이나 저장 요청/);
 for(const changed of [{...opened,draft:{...local,revision:1,text:"new text"}},{...opened,draft:draft(null)},{...opened,pending:noteSaveIntent(local,"era","unknown")}]){
  const before=structuredClone(changed);assert.throws(()=>requireNoteRecord(changed,captured),/초안이나 저장 요청/);assert.deepEqual(changed,before);
 }
 const pending={...opened,pending:noteSaveIntent(local,"era","original")};
 assert.throws(()=>requireNoteRecord({...pending,pending:{...pending.pending,rejected:true}},noteRecordIdentity(pending)),/초안이나 저장 요청/);
});

test("Task6 new confirmed work may replace only the captured definitive rejection, never another unknown intent",()=>{
 const intent=noteSaveIntent(draft(null),"era","rejected-region");
 requireNotePending({draft:null,pending:{...intent,rejected:true}},"explicit-page-fallback",intent.key);
 requireNotePending({draft:null,pending:{...intent,key:"explicit-page-fallback"}},"explicit-page-fallback",intent.key);
 requireNotePending({draft:null,pending:null},"explicit-page-fallback",intent.key);
 for(const pending of [intent,{...intent,key:"peer-unknown"},{...intent,key:"peer-rejected",rejected:true}])assert.throws(()=>requireNotePending({draft:null,pending},"explicit-page-fallback",intent.key),/다른 저장 요청/);
});

test("Task6 guarded journal rechecks inside the actual delayed WebLock and preserves bytes on refusal/CAS/storage failure",async()=>{
 const original=Object.getOwnPropertyDescriptor(globalThis,"navigator");
 try{
  for(const boundary of ["authority","draft","new-pending","other-window","storage"] as const){
   const gate=deferred<void>();let entered=false,writes=0,valid=true,failStorage=false;
   Object.defineProperty(globalThis,"navigator",{configurable:true,value:{locks:{request:async(_key:string,fn:()=>unknown)=>{entered=true;await gate.promise;return fn();}}}});
   const values=new Map<string,string>(),storage={getItem:(key:string)=>values.get(key)??null,setItem:(key:string,value:string)=>{if(failStorage)throw Error("storage unavailable");writes++;values.set(key,value);}};
   const journal=new NoteJournal(storage,"guarded-"+boundary),current:NoteRecord={draft:null,pending:null},expected=noteRecordIdentity(current);
   const replacement:NoteRecord={draft:draft(null),pending:null},check=()=>{if(!valid)throw Error("authority expired");requireNoteRecord(current,expected);};
   const writing=journal.write(replacement,check);assert.equal(entered,true);assert.equal(writes,0);
   if(boundary==="authority")valid=false;
   if(boundary==="draft")current.draft=draft(null);
   if(boundary==="new-pending")current.pending=noteSaveIntent(draft(null),"era","peer-unknown");
   if(boundary==="other-window")values.set(journal.key,JSON.stringify({draft:draft(null),pending:null}));
   if(boundary==="storage")failStorage=true;
   const before=storage.getItem(journal.key);gate.resolve();await assert.rejects(writing);assert.equal(writes,0);assert.equal(storage.getItem(journal.key),before);
  }
 }finally{if(original)Object.defineProperty(globalThis,"navigator",original);else Reflect.deleteProperty(globalThis,"navigator");}
});

test("Task6 dispatch admits only the exact durable pending intent and releases the lock before awaiting the unchanged network result",async()=>{
 const original=Object.getOwnPropertyDescriptor(globalThis,"navigator");let held=false;
 Object.defineProperty(globalThis,"navigator",{configurable:true,value:{locks:{request:async(_key:string,fn:()=>unknown)=>{assert.equal(held,false);held=true;try{return fn();}finally{held=false;}}}}});
 try{
  const values=new Map<string,string>(),storage={getItem:(key:string)=>values.get(key)??null,setItem:(key:string,value:string)=>{values.set(key,value);}},journal=new NoteJournal(storage,"dispatch-admission");
  const local=draft(null),intent=noteSaveIntent(local,"era","original-key");let calls=0;
  const start=()=>{assert.equal(held,true);calls++;return Promise.resolve(note("saved"));};
  await assert.rejects(journal.dispatch(intent,()=>{},start),/로컬/);assert.equal(calls,0);
  await journal.write({draft:local,pending:intent});
  for(const changed of [{...intent,target:"other-note"},{...intent,key:"new-key"},{...intent,etag:'"different"'},{...intent,generation:"another-era"},{...intent,body:{...intent.body!,body:"changed"}}] as NoteIntent[])await assert.rejects(journal.dispatch(changed,()=>{},start));
  assert.equal(calls,0);
  const response=deferred<AnnotationView>();const running=journal.dispatch(intent,()=>{},()=>{assert.equal(held,true);calls++;return response.promise;});
  await Promise.resolve();const newer={...local,revision:1,text:"typing while dispatch waits"};await journal.write({draft:newer,pending:intent});assert.equal(held,false);assert.equal(journal.read().draft?.text,newer.text);
  const result=note("saved");response.resolve(result);assert.equal(await running,result);assert.equal(calls,1);
  const error=new ApiError(412,"PRECONDITION_FAILED",false);await assert.rejects(journal.dispatch(intent,()=>{},()=>Promise.reject(error)),value=>value===error);
  values.set(journal.key,JSON.stringify({draft:newer,pending:{...intent,key:"other-window-unknown"}}));await assert.rejects(journal.dispatch(intent,()=>{},start),/다른 창/);assert.equal(calls,1);
 }finally{if(original)Object.defineProperty(globalThis,"navigator",original);else Reflect.deleteProperty(globalThis,"navigator");}
});

test("Task6 actual delayed dispatch lock rechecks authority before starting the protected call",async()=>{
 const original=Object.getOwnPropertyDescriptor(globalThis,"navigator");
 try{Object.defineProperty(globalThis,"navigator",{configurable:true,value:{locks:{request:async(_key:string,fn:()=>unknown)=>fn()}}});
  const values=new Map<string,string>(),storage={getItem:(key:string)=>values.get(key)??null,setItem:(key:string,value:string)=>{values.set(key,value);}},journal=new NoteJournal(storage,"delayed-dispatch"),intent=noteSaveIntent(draft(null),"era","key");
  await journal.write({draft:null,pending:intent});const gate=deferred<void>();let valid=true,calls=0,callbacks=0;
  Object.defineProperty(globalThis,"navigator",{configurable:true,value:{locks:{request:async(_key:string,fn:()=>unknown)=>{await gate.promise;callbacks++;return fn();}}}});
  const running=journal.dispatch(intent,()=>{if(!valid)throw Error("authority changed");},async()=>{calls++;return note("never");});valid=false;gate.resolve();await assert.rejects(running,/authority changed/);assert.equal(callbacks,1);assert.equal(calls,0);assert.equal(journal.read().pending?.key,intent.key);
 }finally{if(original)Object.defineProperty(globalThis,"navigator",original);else Reflect.deleteProperty(globalThis,"navigator");}
});

test("Task6 a newly durable unknown intent while dispatch waits cannot be replaced or dispatched",async()=>{
 const original=Object.getOwnPropertyDescriptor(globalThis,"navigator");
 try{Object.defineProperty(globalThis,"navigator",{configurable:true,value:{locks:{request:async(_key:string,fn:()=>unknown)=>fn()}}});
  const values=new Map<string,string>(),storage={getItem:(key:string)=>values.get(key)??null,setItem:(key:string,value:string)=>{values.set(key,value);}},journal=new NoteJournal(storage,"new-unknown-at-dispatch"),intent=noteSaveIntent(draft(null),"era","approved");
  await journal.write({draft:null,pending:intent});const gate=deferred<void>();let calls=0,callbacks=0;
  Object.defineProperty(globalThis,"navigator",{configurable:true,value:{locks:{request:async(_key:string,fn:()=>unknown)=>{await gate.promise;callbacks++;return fn();}}}});
  const running=journal.dispatch(intent,()=>{},async()=>{calls++;return note("never");});
  const competing=JSON.stringify({draft:draft(null),pending:{...intent,key:"another-window-unknown"}});values.set(journal.key,competing);gate.resolve();await assert.rejects(running,/다른 창/);assert.equal(callbacks,1);assert.equal(calls,0);assert.equal(storage.getItem(journal.key),competing);
 }finally{if(original)Object.defineProperty(globalThis,"navigator",original);else Reflect.deleteProperty(globalThis,"navigator");}
});

test("Task6 authority or unknown pending change during body preparation prevents later checkpoints and mutation",async()=>{
 for(const boundary of ["authority","unknown"] as const){
  const local=draft(null);local.text="🌊".repeat(20000);const intent=noteSaveIntent(local,"era","approved");let current:NoteRecord={draft:local,pending:intent},valid=true,calls=0,saves=0;
  const entered=deferred<void>(),release=deferred<void>();
  const client={createUpload:async()=>{calls++;entered.resolve();await release.promise;return {upload_id:"prepared-only"};}} as unknown as HaeryongClient;
  const guard=()=>{if(!valid)throw Error("authority expired");requireNotePending(current,intent.key,null);};
  const running=prepareNoteBody(client,intent,guard,async next=>{guard();saves++;current={...current,pending:next};});await entered.promise;
  if(boundary==="authority")valid=false;else current={...current,pending:{...intent,key:"new-unknown"}};
  const retained=structuredClone(current);release.resolve();await assert.rejects(running);assert.equal(calls,1);assert.equal(saves,1);assert.deepEqual(current,retained);
 }
});

test("RR1-F1 delete and reattach preserve unrelated new/existing drafts, including typing in flight and later Save target",async()=>{
 for(const existing of [false,true])for(const kind of ["delete","reattach"] as const)for(const typing of [false,true]){
  const a=note("note-A"),b=note("note-B"),open=draft(existing?a:null);
  const intent:NoteIntent={kind:kind==="delete"?"delete":"patch",target:b.annotation_id,key:crypto.randomUUID(),generation:"era",etag:b.etag,body:kind==="delete"?null:{action:"reattach",anchor,selected_text:null,context:null,context_ref:null}};
  let record:NoteRecord={draft:open,pending:intent};const response=deferred<AnnotationView>();
  const client={annotationDelete:()=>response.promise,annotationPatch:()=>response.promise} as unknown as HaeryongClient;
  const inFlight=dispatchNote(client,intent).then(value=>{record=completeNoteIntent(record,intent,value);});
  if(typing)record={...record,draft:{...open,revision:1,text:"typed while B changes"}};
  const expected=record.draft;response.resolve({...b,revision:2});await inFlight;
  assert.equal(record.draft,expected);assert.equal(record.draft?.base,existing?a:null);assert.equal(record.pending,null);
  const later=noteSaveIntent(record.draft!,"era","later-save");assert.equal(later.kind,existing?"patch":"create");assert.equal(later.target,existing?a.annotation_id:anchor.version_id);
  const calls:unknown[][]=[];await dispatchNote({annotationPatch:async(...args:unknown[])=>{calls.push(args);return a;},annotationCreate:async(...args:unknown[])=>{calls.push(args);return a;}} as unknown as HaeryongClient,later);
  assert.equal(calls[0]![0],later.target);assert.notEqual(calls[0]![0],b.annotation_id);
 }
});

test("RR1-F1 only the draft's Save may clear it or advance its base; recovered intents retain the original draft revision",async()=>{
 for(const existing of [false,true])for(const typing of [false,true]){
  const a=note("note-A"),open=draft(existing?a:null),intent=noteSaveIntent(open,"era","save-A");
  let record:NoteRecord={draft:open,pending:intent};const response=deferred<AnnotationView>();
  const client={annotationPatch:()=>response.promise,annotationCreate:()=>response.promise} as unknown as HaeryongClient;
  const inFlight=dispatchNote(client,intent).then(value=>{record=completeNoteIntent(record,intent,value);});
  if(typing)record={...record,draft:{...open,revision:1,text:"typed during own Save"}};
  response.resolve(note("note-A",2));await inFlight;
  assert.equal(record.pending,null);
  if(typing){assert.equal(record.draft?.text,"typed during own Save");assert.equal(record.draft?.base?.annotation_id,"note-A");assert.equal(record.draft?.base?.revision,2);assert.equal(noteSaveIntent(record.draft!,"era","next").target,"note-A");}else assert.equal(record.draft,null);
  const replacement=draft(note("note-C"));const replaced={draft:replacement,pending:intent};assert.equal(completeNoteIntent(replaced,intent,note("note-A",2)).draft,replacement);
  const recovered=JSON.parse(JSON.stringify({draft:{...open,revision:2,text:"typed before receipt recovery"},pending:intent})) as NoteRecord;
  const completed=completeNoteIntent(recovered,recovered.pending!,note("note-A",2));assert.equal(completed.draft?.text,"typed before receipt recovery");assert.equal(completed.draft?.base?.annotation_id,"note-A");
 }
});

test("RR1-F2 large referenced 412 server body streams into a separate exact-revision comparison without replacing draft or intent",async()=>{
 const local=draft(note("note-A")),intent=noteSaveIntent(local,"era","stale-key"),record:NoteRecord={draft:local,pending:{...intent,rejected:true}},before=structuredClone(record);
 const text="server Markdown 🌊 ".repeat(6000),bytes=new TextEncoder().encode(text),digest=Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256",bytes))).map(n=>n.toString(16).padStart(2,"0")).join("");
 const server={...note("note-A",4),body:null,body_ref:{bytes_sha256:digest,byte_size:String(bytes.length),media_type:"text/markdown",content_url:"/body?revision=4"}};
 const gate=deferred<void>(),calls:unknown[][]=[];let cancelled=false;
 const client={annotationBodyStream:async(...args:unknown[])=>{calls.push(args);return {body:new ReadableStream<Uint8Array>({async start(c){await gate.promise;c.enqueue(bytes.subarray(0,100));c.enqueue(bytes.subarray(100));c.close();}}),etag:'"body"',bytes_sha256:digest,byte_size:String(bytes.length),media_type:"text/markdown",cancel:()=>{cancelled=true;}} as AnnotationStream;}} as unknown as HaeryongClient;
 const reading=noteComparisonText(client,server,()=>{});assert.deepEqual(record,before);assert.deepEqual(calls,[["note-A",4]]);gate.resolve();assert.equal(await reading,text);assert.equal(cancelled,true);assert.deepEqual(record,before);
 // Reading/comparing has no journal write path; neither a reload nor a merge
 // is implicit, and a stale authority cancels before exposing the text.
 cancelled=false;await assert.rejects(noteComparisonText(client,server,()=>{throw Error("authority changed");}),/이전 선택의 응답입니다/);assert.equal(cancelled,true);assert.deepEqual(record,before);
});


test("RR2-B-1 stale edit after page/version reattach explicitly drops old context and saves local text at the server anchor",async()=>{
 for(const destination of [{...anchor,page_path:"other.html"},{...anchor,version_id:"new-version"}]){
  const base={...note("note-A"),context_ref:"old-context"},local={...draft(base),contextRef:"old-context",selectedText:"old page selection",text:"local text to preserve"};
  const remote={...base,anchor:destination,revision:2,etag:'"reattached"',context_ref:"server-context",selected_text:"new page selection"};
  assert.equal(noteBindingChanged(local.anchor,remote.anchor),true);
  assert.throws(()=>reconcileNoteDraft(local,remote,"keep"),/이전 문맥/);
  const stale=noteSaveIntent(local,"era","stale-save");
  let sent:unknown[]|undefined;
  const client={annotationPatch:async(...args:unknown[])=>{sent=args;const [,body,etag]=args as [string,{body:string;context_ref:string|null;context:null;selected_text:string|null},string];if(etag!==remote.etag)throw new ApiError(412,"PRECONDITION_FAILED",false);if(body.context_ref==="old-context")throw new ApiError(400,"INVALID_REQUEST",false,0,{reason:"context_binding_mismatch"});return {...remote,body:body.body,context_ref:body.context_ref,selected_text:body.selected_text,revision:3,etag:'"merged"'};}} as unknown as HaeryongClient;
  await assert.rejects(dispatchNote(client,stale),e=>e instanceof ApiError&&e.status===412);
  const merged=reconcileNoteDraft(local,remote,"drop");assert.equal(merged.text,local.text);assert.deepEqual(merged.anchor,remote.anchor);assert.equal(merged.base,remote);assert.equal(merged.contextRef,null);assert.equal(merged.context,null);assert.equal(merged.selectedText,null);assert.equal(local.contextRef,"old-context");
  const intent=noteSaveIntent(merged,"era","merged-save"),saved=await dispatchNote(client,intent);
  assert.deepEqual(sent,["note-A",{action:"edit",body:local.text,body_ref:null,selected_text:null,context:null,context_ref:null},remote.etag,"merged-save"]);assert.equal(saved.body,local.text);assert.deepEqual(saved.anchor,destination);assert.equal(completeNoteIntent({draft:merged,pending:intent},intent,saved).draft,null);
  // A draft already rebased by the prior UI cannot silently send the old binding.
  const legacy={...local,base:remote};assert.throws(()=>noteSaveIntent(legacy,"era","blocked"),/위치/);assert.equal(noteSaveIntent(reconcileNoteDraft(legacy,remote,"drop"),"era","reconciled").body?.context_ref,null);
 }
 const local=draft(note("note-A"));local.contextRef="same-page-context";const server=note("note-A",2);const merged=reconcileNoteDraft(local,server,"keep");assert.equal(merged.contextRef,local.contextRef);assert.equal(merged.text,local.text);
});
