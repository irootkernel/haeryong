import {useEffect,useRef,useState} from "react";
import type {ShellController} from "../../client/controller.ts";
import type {ReviewCondition,RevisitView} from "../../client/types.ts";
import {ApiError,StaleResponseError} from "../../client/types.ts";
import {useReviewInvalidation} from "./review-refresh.ts";
import {definitiveNoteRejection} from "./notes-operations.ts";

interface Intent {key:string;document:string;flag:boolean;condition:ReviewCondition;generation:string;rejected?:boolean}
const flagLabel = (flag: boolean) => (flag ? "다시 볼 문서로 표시" : "다시 볼 문서에서 해제");

export function RevisitControl({controller,documentId}:{controller:ShellController;documentId:string}){
  const enabled=controller.state.capabilities?.features.revisit===true;
  const [view,setView]=useState<RevisitView|null>(null),[pending,setPending]=useState<Intent|null>(null),[issue,setIssue]=useState(""),[busy,setBusy]=useState(false),[refresh,setRefresh]=useState(0);const life=useRef(0),viewRead=useRef(0);
  const key="haeryong.revisit.v1:"+JSON.stringify([controller.state.capabilities?.library_id,controller.state.session?.principal_id,documentId]);
  const guardFor=()=>{const authority=controller.captureDispatchAuthority(),n=life.current;return()=>{controller.requireVerifiedDispatch(authority);if(n!==life.current)throw Error("이전 Revisit 화면입니다.");};};
  const journal=async(expected:Intent|null,next:Intent|null)=>{if(!navigator.locks)throw Error("Revisit 저장 잠금을 사용할 수 없습니다.");await navigator.locks.request(key,()=>{if(localStorage.getItem(key)!==(expected?JSON.stringify(expected):null))throw Error("다른 창의 Revisit 요청을 먼저 확인하세요.");if(next)localStorage.setItem(key,JSON.stringify(next));else localStorage.removeItem(key);});setPending(next);};
  useEffect(()=>{life.current++;setView(null);setIssue("");try{const raw=localStorage.getItem(key);const intent=raw?JSON.parse(raw) as Intent:null;if(intent&&(intent.document!==documentId||typeof intent.key!=="string"||typeof intent.flag!=="boolean"||!intent.condition))throw Error("Revisit 복구 기록을 확인할 수 없습니다.");setPending(intent);}catch(e){setIssue(String(e));}return()=>{life.current++;};},[key,documentId]);
  useEffect(()=>{if(!enabled)return;let active=true;const authority=controller.captureDispatchAuthority(),readGen=controller.readGeneration,n=life.current,viewRun=++viewRead.current;void controller.client.revisitGet(documentId).then(v=>{if(active&&viewRead.current===viewRun&&n===life.current&&controller.isCurrentReadAuthority(authority,readGen)){if(v.document_id!==documentId)throw Error("Revisit 응답 문서가 일치하지 않습니다.");setView(v);}}).catch(e=>{if(active&&viewRead.current===viewRun&&n===life.current&&controller.isCurrentReadAuthority(authority,readGen))setIssue(String(e));});return()=>{active=false;};},[controller,documentId,enabled,refresh,key]);
  useReviewInvalidation(
    controller,
    enabled,
    async (ev) => {
      const captured = controller.captureDispatchAuthority();
      const readGen = ev?.readGeneration ?? controller.readGeneration;
      const n = life.current, viewRun = ++viewRead.current;
      try {
        const v = await controller.passiveClient.revisitGet(documentId);
        if (n !== life.current || !controller.isCurrentReadAuthority(captured, readGen)) return;
        if (viewRead.current !== viewRun) { if (ev?.resync) throw new StaleResponseError(); return; }
        if (v.document_id !== documentId) throw Error("Revisit 응답 문서가 일치하지 않습니다.");
        setView(v);
      } catch (e) {
        if (!(e instanceof StaleResponseError) && viewRead.current === viewRun && n === life.current && controller.isCurrentReadAuthority(captured, readGen)) {
          setIssue(String(e));
        }
        if (ev?.resync) throw e;
      }
    },
    () => busy,
  );
  const adoptCurrentAfterWrite=async(guard:()=>void)=>{
    try{
      const fresh=await controller.client.revisitGet(documentId);
      guard();
      if(fresh.document_id!==documentId)throw Error("Revisit 응답 문서가 일치하지 않습니다.");
      controller.bumpReadGeneration();
      setView(fresh);
    }catch{
      guard();setView(null);
      throw Error("원래 Revisit 요청은 완료되었으나 현재 서버 상태를 불러오지 못했습니다. 다시 확인해 주세요.");
    }
  };
  const act=async(recover=false)=>{
    if((!view&&!recover)||busy)return;
    const guard=guardFor();
    const currentGen=controller.state.capabilities?.runtime_generation??"";
    const intent:Intent=recover?pending!:{key:crypto.randomUUID(),document:documentId,flag:!view!.value.flag,condition:view!.exists?{ifMatch:view!.etag!}:{create:true},generation:currentGen};
    controller.bumpReadGeneration();
    setBusy(true);setIssue("");
    try{
      guard();
      if(!recover&&intent.generation!==currentGen)throw Error("서비스 세대가 바뀌었습니다. 현재 상태와 원래 operation을 확인하세요.");
      if(!recover)await journal(pending,intent);
      guard();
      if(recover){
        let missingReceipt=false;
        let receipt=null;
        try{
          receipt=await controller.client.operation(intent.key);
        }catch(e){
          guard();
          if(e instanceof ApiError&&e.status===404){
            missingReceipt=true;
          }else{
            throw e;
          }
        }
        guard();
        if(missingReceipt){
          const genChanged=intent.generation===""?currentGen!=="":intent.generation!==currentGen;
          if(genChanged){
            const rejected={...intent,rejected:true};
            await journal(intent,rejected);
            try{const fresh=await controller.client.revisitGet(documentId);guard();controller.bumpReadGeneration();setView(fresh);}catch{}
            setIssue("서버 실행 세대가 변경되어(복원 또는 초기화) 이전 Revisit 요청 기록을 자동 재전송하지 않았습니다. 현재 서버 값과 비교해 선택하세요.");
            return;
          }
        }else if(receipt){
          if(receipt.state==="accepted"||receipt.state==="pending"||receipt.state==="reconciling"){
            throw Error("이전 Revisit 요청이 서버에서 아직 처리 중입니다. 잠시 후 다시 확인하세요.");
          }
          if(receipt.state==="completed"){
            await journal(intent,null);
            guard();
            try{
              const fresh=await controller.client.revisitGet(documentId);
              guard();
              if(fresh.document_id!==documentId)throw Error("Revisit 응답 문서가 일치하지 않습니다.");
              controller.bumpReadGeneration();
              setView(fresh);
              window.dispatchEvent(new Event("haeryong-revisit-changed"));
              return;
            }catch{
              setView(null);
              throw Error("원래 Revisit 요청은 완료되었으나 현재 서버 상태를 불러오지 못했습니다. 다시 확인해 주세요.");
            }
          }
          if(receipt.state==="failed"){
            const rejected={...intent,rejected:true};
            await journal(intent,rejected);
            try{const fresh=await controller.client.revisitGet(documentId);guard();controller.bumpReadGeneration();setView(fresh);}catch{}
            setIssue((receipt.error?.message||"이전 Revisit 요청이 서버에서 거절되었습니다.")+" 서버 값 유지 또는 내 값 다시 저장을 선택하세요.");
            return;
          }
        }
      }
      const saved=await controller.client.revisitPut(intent.document,intent.flag,intent.condition,intent.key);
      guard();
      if(saved.document_id!==documentId)throw Error("Revisit 응답 문서가 일치하지 않습니다.");
      await journal(intent,null);
      guard();
      // sourceWrite can reconcile a lost first response inside the transport;
      // a successful first-call DTO is not necessarily the current resource.
      await adoptCurrentAfterWrite(guard);
      window.dispatchEvent(new Event("haeryong-revisit-changed"));
    }catch(e){
      try{
        guard();
        setIssue(String(e));
        if(definitiveNoteRejection(e)){
          await journal(intent,{...intent,rejected:true});
          try{const fresh=await controller.client.revisitGet(documentId);guard();controller.bumpReadGeneration();setView(fresh);}catch{}
        }
      }catch{}
    }finally{
      try{guard();setBusy(false);}catch{}
    }
  };
  const applyLocal=async()=>{
    if(!pending||busy)return;
    const guard=guardFor();
    controller.bumpReadGeneration();
    setBusy(true);setIssue("");
    try{
      const fresh=await controller.client.revisitGet(documentId);
      guard();
      if(fresh.value.flag===pending.flag){
        await journal(pending,null);
        controller.bumpReadGeneration();
        setView(fresh);
        window.dispatchEvent(new Event("haeryong-revisit-changed"));
        return;
      }
      const nextIntent:Intent={
        key:crypto.randomUUID(),
        document:documentId,
        flag:pending.flag,
        condition:fresh.exists?{ifMatch:fresh.etag!}:{create:true},
        generation:controller.state.capabilities?.runtime_generation??"",
      };
      await journal(pending,nextIntent);
      guard();
      const saved=await controller.client.revisitPut(nextIntent.document,nextIntent.flag,nextIntent.condition,nextIntent.key);
      guard();
      if(saved.document_id!==documentId)throw Error("Revisit 응답 문서가 일치하지 않습니다.");
      await journal(nextIntent,null);
      await adoptCurrentAfterWrite(guard);
      window.dispatchEvent(new Event("haeryong-revisit-changed"));
    }catch(e){
      try{
        guard();
        setIssue(String(e));
        const currentRaw=localStorage.getItem(key);
        const currentIntent=currentRaw?JSON.parse(currentRaw) as Intent:null;
        if(currentIntent&&definitiveNoteRejection(e)){
          await journal(currentIntent,{...currentIntent,rejected:true});
          try{const fresh=await controller.client.revisitGet(documentId);guard();controller.bumpReadGeneration();setView(fresh);}catch{}
        }
      }catch{}
    }finally{
      try{guard();setBusy(false);}catch{}
    }
  };
  if(!enabled)return <p>Revisit: 아직 제공되지 않습니다.</p>;
  return <section aria-label="다시 볼 문서">
    <button id="reader-revisit" style={{minHeight:44,minWidth:44}} aria-pressed={view?.value.flag??false} disabled={!view||busy||!!pending} onClick={()=>void act()}>{view?.value.flag?"다시 볼 문서에서 해제":"다시 볼 문서로 표시"}</button>
    {issue&&<p role="alert">{issue}</p>}
    {pending&&!pending.rejected&&<button disabled={busy} onClick={()=>void act(true)}>원래 Revisit 요청 확인</button>}
    {pending?.rejected&&<div>
      <p className="hint" data-revisit-conflict="true">내 요청: {flagLabel(pending.flag)} · 서버 상태: {view ? flagLabel(view.value.flag) : "확인 중"}</p>
      <div className="form-row">
        <button disabled={busy} onClick={()=>void journal(pending,null).then(()=>{setIssue("");setRefresh(n=>n+1);}).catch(e=>setIssue(String(e)))}>서버 값 유지</button>
        <button disabled={busy} onClick={()=>void applyLocal()}>내 값 다시 저장</button>
      </div>
    </div>}
  </section>;
}
