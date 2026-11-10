import {useEffect,useRef,useState} from "react";
import type {ShellController} from "../../client/controller.ts";
import type {ConfigurationView,ConfigurationPlanView} from "../../client/types.ts";
import {ApiError,StaleResponseError} from "../../client/types.ts";
import {PendingSourceWriteError,reconcileSourceWrite} from "../../client/source-operations.ts";
import {resolveWriteIntent} from "../../client/source-write-intents.ts";
import type {ConfigurationIntent} from "../../client/source-write-intents.ts";

import {ACTION_CATEGORIES,actionPolicyDraft,actionPolicyChanges,effectiveActionPolicy} from "./action-policy-state.ts";
import type {ActionPolicyDraft,ActionMode} from "./action-policy-state.ts";

export function ActionPolicySettings({controller,projectId}:{controller:ShellController;projectId?:string}){
 const [view,setView]=useState<ConfigurationView|null>(null),[draft,setDraft]=useState<ActionPolicyDraft|null>(null),[plan,setPlan]=useState<ConfigurationPlanView|null>(null);
 const [issue,setIssue]=useState<string|null>(null),[busy,setBusy]=useState(false),[confirmed,setConfirmed]=useState(false),[pending,setPending]=useState(false);
 const generation=useRef(0),mounted=useRef(true),key="maintenance:"+(projectId??"library");
 const intents=controller.sourceWriteIntents();
 const verified=controller.state.phase==="verified";
 const effective=projectId&&view&&draft?effectiveActionPolicy(view,draft):null;
 async function load(reset=false){
  const own=++generation.current,auth=controller.captureDispatchAuthority();setBusy(true);setIssue(null);
  try{controller.requireVerifiedDispatch(auth);const v=await controller.client.configurationView();controller.requireVerifiedDispatch(auth);if(!mounted.current||own!==generation.current)return;setView(v);setPlan(null);setConfirmed(false);if(reset||draft===null)setDraft(actionPolicyDraft(v,projectId));setPending(intents.configurations.has(key))}
  catch(e){if(mounted.current&&own===generation.current)setIssue(String(e))}
  finally{if(mounted.current&&own===generation.current)setBusy(false)}
 }
 useEffect(()=>{mounted.current=true;void load();const off=controller.subscribe(()=>{if(controller.state.phase!=="verified"){generation.current++;setView(null);setPlan(null);setConfirmed(false);setBusy(false)}});return()=>{mounted.current=false;generation.current++;off()}},[controller,projectId]);
 function edit(next:ActionPolicyDraft){setDraft(next);setPlan(null);setConfirmed(false);setIssue(null)}
 function failure(e:unknown,intent:ConfigurationIntent){
  const uncertain=e instanceof PendingSourceWriteError||e instanceof StaleResponseError;
  setPending(uncertain);if(!uncertain&&intents.configurations.get(key)===intent)intents.configurations.delete(key);
  setIssue((e instanceof ApiError&&(e.status===409||e.status===412)?"구성이 변경되었습니다. 로컬 입력은 유지됩니다. 현재 구성을 읽고 새 미리보기를 만드세요. ":"")+String(e));
 }
 async function preview(){
  if(!draft||!view||busy||intents.configurations.has(key))return;
  const own=++generation.current,auth=controller.captureDispatchAuthority(),intent:ConfigurationIntent={kind:"preview",operationId:crypto.randomUUID(),outcome:{state:"pending"}};
  try{controller.requireVerifiedDispatch(auth);const changes=actionPolicyChanges(draft);intents.configurations.set(key,intent);setBusy(true);setPlan(null);setConfirmed(false);setIssue(null);
   const result=await resolveWriteIntent(intent,()=>{controller.requireVerifiedDispatch(auth);return controller.client.configurationPlan({scope:projectId?{kind:"project",project_id:projectId}:{kind:"library"},expected_config_revision:view.config_revision,changes},intent.operationId)});
   controller.requireVerifiedDispatch(auth);if(mounted.current&&own===generation.current){setPlan(result as ConfigurationPlanView);intents.configurations.delete(key);setPending(false)}
  }catch(e){if(mounted.current&&own===generation.current)failure(e,intent)}
  finally{if(mounted.current&&own===generation.current)setBusy(false)}
 }
 async function commit(){
  if(!plan||!confirmed||busy||intents.configurations.has(key))return;
  const own=++generation.current,auth=controller.captureDispatchAuthority(),intent:ConfigurationIntent={kind:"commit",operationId:plan.plan_id,outcome:{state:"pending"},planId:plan.plan_id,baseRevision:plan.base_config_revision};
  try{controller.requireVerifiedDispatch(auth);intents.configurations.set(key,intent);setBusy(true);setIssue(null);
   await resolveWriteIntent(intent,()=>{controller.requireVerifiedDispatch(auth);return controller.client.configurationPlanCommit(plan.plan_id,{expected_config_revision:plan.base_config_revision,confirmed:true},intent.operationId)});
   controller.requireVerifiedDispatch(auth);if(mounted.current&&own===generation.current){intents.configurations.delete(key);setPending(false);setPlan(null);setConfirmed(false);await load(true)}
  }catch(e){if(mounted.current&&own===generation.current)failure(e,intent)}
  finally{if(mounted.current&&own===generation.current)setBusy(false)}
 }
 async function recover(){
  const intent=intents.configurations.get(key);if(!intent||busy)return;
  const own=++generation.current,auth=controller.captureDispatchAuthority();setBusy(true);
  try{controller.requireVerifiedDispatch(auth);const result=await resolveWriteIntent(intent,()=>reconcileSourceWrite(intent.operationId,id=>controller.client.operation(id)));controller.requireVerifiedDispatch(auth);
   if(mounted.current&&own===generation.current){intents.configurations.delete(key);setPending(false);if(intent.kind==="preview")setPlan(result as ConfigurationPlanView);else{setPlan(null);setConfirmed(false);await load(true)}}
  }catch(e){if(mounted.current&&own===generation.current)failure(e,intent)}
  finally{if(mounted.current&&own===generation.current)setBusy(false)}
 }
 return <section className="panel" aria-label="정비 적용 정책" data-action-policy>
  <h1>정비 적용 정책</h1><p>{projectId?"프로젝트 "+projectId+" · 서재 정책보다 넓힐 수 없습니다.":"서재 기본 정책"} · 리비전 {view?.config_revision??"확인 필요"}</p>
  <p>정책 변경은 원본 게시·개인 Review·새 출처 공개를 승인하지 않습니다. 일정 실행은 아직 제공하지 않습니다.</p>
  {!verified&&<p role="alert">연결을 확인한 뒤 정책을 편집할 수 있습니다.</p>}{issue&&<p role="alert">{issue}</p>}
  <button disabled={busy||!verified} onClick={()=>void load()}>현재 구성 다시 읽기 (입력 유지)</button>
  {draft&&<fieldset disabled={!verified||busy||pending}><legend>작업별 적용 방식</legend>
   {ACTION_CATEGORIES.map(category=><label key={category}>{category}<select aria-label={category} value={draft.auto_apply[category]} onChange={e=>edit({...draft,auto_apply:{...draft.auto_apply,[category]:e.target.value as ActionMode}})}>{["disabled","propose","auto"].map(mode=><option key={mode} value={mode}>{mode}</option>)}</select>{effective&&<span>현재 서재 상한 적용: {effective.auto_apply[category]}</span>}</label>)}
   <label>content_generation<select aria-label="content_generation" value={draft.content_generation} onChange={e=>edit({...draft,content_generation:e.target.value as ActionPolicyDraft["content_generation"]})}>{["disabled","propose","draft"].map(mode=><option key={mode} value={mode}>{mode}</option>)}</select>{effective&&<span>현재 서재 상한 적용: {effective.content_generation}</span>}</label>
   <button onClick={()=>void preview()}>영향 미리보기</button>
  </fieldset>}
  {plan&&<section aria-label="정책 영향 미리보기"><h2>적용 전 확인</h2><pre className="management-detail">{JSON.stringify(plan,null,2)}</pre><label><input type="checkbox" checked={confirmed} onChange={e=>setConfirmed(e.target.checked)}/>현재 리비전과 정책 변경 영향을 확인했습니다</label><button disabled={!confirmed||busy||!verified||pending} onClick={()=>void commit()}>확인한 정책 적용</button><button disabled={busy||pending} onClick={()=>{setPlan(null);setConfirmed(false)}}>미리보기 취소</button></section>}
  {pending&&<div role="status">이전 요청의 결과를 확인해야 합니다.<button disabled={busy||!verified} onClick={()=>void recover()}>동일 요청 결과 확인</button></div>}
 </section>;
}
