import {useEffect,useState} from "react";
import type {ShellController} from "../../client/controller.ts";
import type {TrashItem} from "../../client/types.ts";
import {LifecycleControl,stateLabels} from "./lifecycle-control.tsx";
import {PurgeProgress} from "./purge-progress.tsx";
import {navigate} from "../router.tsx";
export function TrashScreen({controller}:{controller:ShellController}){
 const [items,setItems]=useState<TrashItem[]>([]),[cursor,setCursor]=useState<string|null>(null),[busy,setBusy]=useState(false),[issue,setIssue]=useState(""),[refresh,setRefresh]=useState(0);
 const enabled=controller.state.capabilities?.features.lifecycle===true&&controller.state.session?.scope.kind==="owner";
 const load=async(next?:string)=>{const auth=controller.captureDispatchAuthority();setBusy(true);setIssue("");try{const page=await controller.client.trashList({cursor:next});controller.requireVerifiedDispatch(auth);setItems(old=>next?[...old,...page.items]:page.items);setCursor(page.next_cursor);}catch(e){setIssue(String(e));}finally{setBusy(false);}};
 useEffect(()=>{if(enabled)void load();},[controller,enabled,refresh]);
 if(!enabled)return <section><h1>휴지통</h1><p>현재 계정 또는 서비스에서 제공되지 않습니다.</p></section>;
 return <section><h1>휴지통</h1><PurgeProgress controller={controller} refresh={refresh}/><button disabled={busy} onClick={()=>setRefresh(n=>n+1)}>목록 새로 고침</button>{busy&&<p role="status">불러오는 중…</p>}{issue&&<p role="alert">{issue}</p>}{!busy&&!items.length&&<p>휴지통이 비어 있습니다.</p>}<ul>{items.map(item=><li key={item.document_id}><h2>{item.title}</h2><p>삭제: {item.trashed_at?new Date(item.trashed_at).toLocaleString():"기록 없음"} · 복원 위치: {item.previous_lifecycle?stateLabels[item.previous_lifecycle]:"확인 필요"}</p><p>영구 삭제 가능 시점: {item.purge_eligible_at?new Date(item.purge_eligible_at).toLocaleString():"보관 기간 제한 없음"}. 표시한 시점과 무관하게 수동 영구 삭제는 영향 확인 후 선택할 수 있습니다.</p><LifecycleControl controller={controller} documentId={item.document_id} lifecycle="trash" onChanged={result=>{setRefresh(n=>n+1);if(result.lifecycle==="active")navigate("/library");else if(result.lifecycle==="archived")navigate("/library?lifecycle=archived");}}/></li>)}</ul>{cursor&&<button disabled={busy} onClick={()=>void load(cursor)}>더 보기</button>}</section>;
}
