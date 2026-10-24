import {useEffect,useRef,useState} from "react";
import type {ShellController} from "../../client/controller.ts";
import type {DocumentView,ReviewFilter} from "../../client/types.ts";
import {accumulateDocuments,homeSectionsUnavailable} from "./library-data.ts";
import {reviewTarget} from "./review-summary.ts";
import {useReviewInvalidation} from "./review-refresh.ts";
import {navigate} from "../router.tsx";
import {pinnedReaderURL} from "../routes.ts";

type HomeKind=ReviewFilter|"revisit";
const STATE_LABELS={unreviewed:"미검토",reviewing:"검토 중",reviewed:"검토 완료"};
function HomeSection({controller,kind,title}:{controller:ShellController;kind:HomeKind;title:string}){
  const [rows,setRows]=useState<DocumentView[]>([]);
  const [cursor,setCursor]=useState<string|null>(null);
  const [busy,setBusy]=useState(false);
  const [issue,setIssue]=useState("");
  const [refresh,reload]=useState(0);
  const generation=useRef(0),flight=useRef(false);
  const enabled=controller.state.capabilities?.features[kind==="revisit"?"revisit":"review"]===true;
  const filter=()=>kind==="revisit"?{revisit:"true" as const}:{review:kind};

  useEffect(()=>{
    const run=++generation.current;
    setRows([]);setCursor(null);setIssue("");setBusy(enabled);flight.current=false;
    if(enabled){
      const authority=controller.captureDispatchAuthority();
      void controller.client.listDocuments(filter()).then(page=>{
        controller.requireVerifiedDispatch(authority);
        if(generation.current===run){setRows(page.items);setCursor(page.next_cursor);}
      }).catch(error=>{if(generation.current===run)setIssue(String(error));})
        .finally(()=>{if(generation.current===run)setBusy(false);});
    }
    return()=>{generation.current++;};
  },[controller,kind,enabled,refresh,controller.state.phase]);

  useEffect(()=>{
    const update=()=>reload(n=>n+1);
    for(const event of ["focus","haeryong-review-changed","haeryong-revisit-changed"])window.addEventListener(event,update);
    return()=>{for(const event of ["focus","haeryong-review-changed","haeryong-revisit-changed"])window.removeEventListener(event,update);};
  },[]);

  const more=async()=>{
    if(busy||flight.current||!cursor)return;
    const authority=controller.captureDispatchAuthority(),run=generation.current;
    flight.current=true;setBusy(true);
    try{
      controller.requireVerifiedDispatch(authority);
      const page=await controller.client.listDocuments({...filter(),cursor});
      controller.requireVerifiedDispatch(authority);
      if(generation.current!==run)return;
      setRows(old=>accumulateDocuments({documents:old,exhausted:false},page).documents);
      setCursor(page.next_cursor);
    }catch(error){if(generation.current===run)setIssue(String(error));}
    finally{if(generation.current===run){flight.current=false;setBusy(false);}}
  };

  return <div className="home-section"><h2>{title}</h2>{!enabled?<p>{homeSectionsUnavailable().reason}</p>:<>
    <p>{rows.length}건 불러옴{cursor?" · 나머지 있음":""}</p>
    {busy&&<p role="status">목록 확인 중…</p>}
    {issue&&<p role="alert">{issue}</p>}
    {!busy&&!issue&&rows.length===0&&<p>해당하는 문서가 없습니다.</p>}
    <ul>{rows.map(row=>{
      const version=kind==="revisit"?row.latest_version_id:reviewTarget(row,kind);
      return <li key={row.document_id}>
        {version?<a href={pinnedReaderURL(row.document_id,version)}>{row.title}</a>:row.title}
        <p>{kind==="reviewing"?"검토 중인 버전":kind==="revisit"?"다시 볼 문서 · 최신 버전":"검토한 이후 업데이트됨 · 최신 버전"} · 최신 버전의 내 검토: {row.review_state?STATE_LABELS[row.review_state]:"확인 불가"}</p>
        <p className="hint">원본 변경과 의미 평가 상태는 근거 화면에서 별도로 확인하세요.</p>
      </li>;
    })}</ul>
    {cursor&&<button disabled={busy} onClick={()=>void more()}>더 불러오기</button>}
    <button disabled={busy} onClick={()=>reload(n=>n+1)}>목록 새로고침</button>
  </>}</div>;
}

export function HomeScreen({controller}:{controller:ShellController}){
  const state=controller.state;
  useReviewInvalidation(controller.client,state.capabilities?.features.review===true||state.capabilities?.features.revisit===true,()=>window.dispatchEvent(new Event("haeryong-review-changed")));
  return <section className="panel">
    <h1>홈</h1>
    <p className="hint" data-testid="home-session">{state.session?`${state.session.device_label}로 연결됨 · 라이브러리 ${state.capabilities?.library_id??""}`:"연결 정보를 확인하는 중입니다."}</p>
    <HomeSection controller={controller} kind="reviewing" title="검토 이어가기"/>
    <HomeSection controller={controller} kind="updated_since_review" title="검토한 이후 업데이트됨"/>
    <HomeSection controller={controller} kind="revisit" title="다시 볼 문서"/>
    <div className="home-section"><h2>주의 필요</h2><p>{homeSectionsUnavailable().reason}</p></div>
    <div className="form-row"><button className="primary" onClick={()=>navigate("/library")}>서재 열기</button><button onClick={()=>navigate("/settings/storage")}>저장소·업로드 확인</button></div>
  </section>;
}
