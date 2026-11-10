import {useCallback,useEffect,useRef,useState} from "react";
import type {ShellController} from "../../client/controller.ts";
import type {ManagementEffect,ManagementAnchor,ListPage} from "../../client/types.ts";
import {useReviewInvalidation} from "./review-refresh.ts";
import {ManagementLinkRow} from "./management-links-row.ts";

export function managementReaderURL(a:ManagementAnchor):string {
 const q=new URLSearchParams();if(a.page_path!==null)q.set("page",a.page_path);
 return "/documents/"+a.document_id+"/versions/"+a.version_id+(q.size?"?"+q.toString():"")+(a.region_id!==null?"#region="+encodeURIComponent(a.region_id):"");
}
export function ManagementLinks({controller,versionId,pagePath,regionId,onNavigate}:{controller:ShellController;versionId:string;pagePath:string|null;regionId:string|null;onNavigate(url:string,active:()=>boolean):Promise<void>}){
 const [relations,setRelations]=useState<ListPage<ManagementEffect>|null>(null),[topics,setTopics]=useState<ListPage<ManagementEffect>|null>(null);
 const [error,setError]=useState<string|null>(null),[busy,setBusy]=useState(false);
 const generation=useRef(0),activation=useRef(0);
 const available=controller.state.capabilities?.features.management_overlays===true;
 const load=useCallback(async(kind?:"relation"|"topic",cursor?:string,passive=false)=>{
  if(!available)return;
  const own=++generation.current,authority=controller.captureDispatchAuthority(),read=controller.readGeneration;
  setBusy(true);setError(null);
  try{
   controller.requireVerifiedDispatch(authority);
   const client=passive?controller.passiveClient:controller.client;
   const filter={...(pagePath!==null?{page_path:pagePath}:{}),...(regionId!==null?{region_id:regionId}:{}),limit:25,cursor};
   const [r,t]=await Promise.all([kind==="topic"?null:client.versionRelated?.(versionId,filter),kind==="relation"?null:client.topicAssignmentsList?.({...filter,version_id:versionId})]);
   controller.requireVerifiedDispatch(authority);
   if(own!==generation.current || read!==controller.readGeneration)return;
   if(r)setRelations(old=>({...r,items:cursor&&old?[...old.items,...r.items]:r.items}));
   if(t)setTopics(old=>({...t,items:cursor&&old?[...old.items,...t.items]:t.items}));
  }catch(e){if(own===generation.current && controller.isCurrentReadAuthority(authority,read))setError(String(e));if(passive)throw e;}
  finally{if(own===generation.current)setBusy(false)}
 },[controller,versionId,pagePath,regionId,available]);
 useEffect(()=>{setRelations(null);setTopics(null);void load();return()=>{generation.current++;activation.current++}},[load]);
 useReviewInvalidation(controller,available,()=>load(undefined,undefined,true),()=>busy);
 useEffect(()=>controller.subscribe(()=>{if(controller.state.phase!=="verified"){generation.current++;activation.current++;setRelations(null);setTopics(null);setBusy(false)}}),[controller]);
 async function open(a:ManagementAnchor){const own=++activation.current,auth=controller.captureDispatchAuthority();try{controller.requireVerifiedDispatch(auth);await onNavigate(managementReaderURL(a),()=>own===activation.current)}catch(e){if(own===activation.current)setError(String(e))}}
 return <section aria-label="AI 관리 연결" data-management-links>
  <h3>AI 관련 연결 · 토픽</h3><p>작성자 참조와 별도로 저장한 관리 정보입니다. 제안 후보는 정비의 원래 작업에서 확인할 수 있습니다.</p>
  {!available?<p>이 서버는 관리 연결을 제공하지 않습니다.</p>:<>
   <button disabled={busy} onClick={()=>void load()}>관리 연결 새로고침</button>
   {busy&&<p role="status">관리 정보를 읽는 중…</p>}{error&&<p role="alert">{error} · 기존 결과는 유지됩니다.</p>}
   {relations?.items.length===0&&<p>현재 범위에 적용된 AI 연결 없음</p>}
   <ul>{relations?.items.map(m=><ManagementLinkRow key={m.overlay_id} effect={m} onOpen={a=>void open(a)}/>)}</ul>
   {relations?.next_cursor&&<button disabled={busy} onClick={()=>void load("relation",relations.next_cursor!)}>AI 연결 더 보기</button>}
   <h4>후속 토픽과 근거</h4>{topics?.items.length===0&&<p>현재 범위의 관리 토픽 없음</p>}
   <ul>{topics?.items.map(m=><ManagementLinkRow key={m.overlay_id} effect={m} onOpen={a=>void open(a)}/>)}</ul>
   {topics?.next_cursor&&<button disabled={busy} onClick={()=>void load("topic",topics.next_cursor!)}>토픽 더 보기</button>}
  </>}
 </section>;
}
