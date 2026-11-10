import {ManagementLinks} from "./management-links.tsx";
import { useCallback, useEffect, useRef, useState } from "react";
import type { ShellController } from "../../client/controller.ts";
import type { BacklinkPage, ReferencePage, ReferenceView } from "../../client/types.ts";
import { ApiError, StaleResponseError } from "../../client/types.ts";
import { featureEnabled } from "./library-data.ts";
import { backlinkScope, referenceStateLabel } from "./links-state.ts";
import { useReviewInvalidation } from "./review-refresh.ts";

type Props = { controller: ShellController; versionId: string; pagePath: string | null; regionId: string | null; onNavigate(url: string, active: () => boolean): Promise<void> };
export function LinksPanel({ controller, versionId, pagePath, regionId, onNavigate }: Props) {
  const client = controller.client;
  const canReferences = featureEnabled(controller.state.capabilities?.features, "references");
  const canBacklinks = featureEnabled(controller.state.capabilities?.features, "backlinks");
  const [scope, setScope] = useState<"version" | "page" | "region">(regionId ? "region" : pagePath ? "page" : "version");
  const [references, setReferences] = useState<ReferencePage | null>(null);
  const [backlinks, setBacklinks] = useState<BacklinkPage | null>(null);
  const [referenceIssue, setReferenceIssue] = useState<string | null>(null);
  const [backlinkIssue, setBacklinkIssue] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [referenceBusy, setReferenceBusy] = useState(false), [backlinkBusy, setBacklinkBusy] = useState(false);
  const refGeneration = useRef(0), backGeneration = useRef(0), activation = useRef(0);
  async function loadReferences(cursor?: string) {
    if (!canReferences) return;
    const own = ++refGeneration.current, authority = controller.captureDispatchAuthority();
    setReferenceBusy(true); setReferenceIssue(null);
    try {
      controller.requireVerifiedDispatch(authority);
      const page = await client.versionReferences({ versionId, cursor, limit: 50 });
      controller.requireVerifiedDispatch(authority);
      if (own === refGeneration.current) setReferences(old => ({ ...page, items: cursor && old ? [...old.items, ...page.items] : page.items }));
    } catch (error) { if (own === refGeneration.current) setReferenceIssue(error instanceof Error ? error.message : "참조를 읽을 수 없습니다."); }
    finally { if (own === refGeneration.current) setReferenceBusy(false); }
  }
  async function loadBacklinks(cursor?: string) {
    if (!canBacklinks) return;
    const own = ++backGeneration.current, authority = controller.captureDispatchAuthority();
    setBacklinkBusy(true); setBacklinkIssue(null);
    try {
      controller.requireVerifiedDispatch(authority);
      const page = await client.versionBacklinks({ versionId, ...backlinkScope(scope, pagePath, regionId), cursor, limit: 50 });
      controller.requireVerifiedDispatch(authority);
      if (own === backGeneration.current) setBacklinks(old => ({ ...page, items: cursor && old ? [...old.items, ...page.items] : page.items }));
    } catch (error) { if (own === backGeneration.current) setBacklinkIssue(error instanceof Error ? error.message : "백링크를 읽을 수 없습니다."); }
    finally { if (own === backGeneration.current) setBacklinkBusy(false); }
  }
  useEffect(() => { void loadReferences(); return () => { refGeneration.current++; activation.current++; }; }, [controller, versionId, canReferences]);
  useEffect(() => { setBacklinks(null); void loadBacklinks(); return () => { backGeneration.current++; activation.current++; }; }, [controller, versionId, pagePath, regionId, scope, canBacklinks]);
  const refreshPassive = useCallback(async (ev?: { resync?: boolean }) => {
    const startReadGen = controller.readGeneration;
    const authority = controller.captureDispatchAuthority();
    const pClient = controller.passiveClient;
    const tasks: Promise<void>[] = [];
    if (canReferences && !referenceBusy) {
      const ownRef = ++refGeneration.current;
      tasks.push(pClient.versionReferences({ versionId, limit: 50 }).then(page => {
        if(controller.readGeneration!==startReadGen)return;
        if(ownRef!==refGeneration.current){if(ev?.resync)throw new StaleResponseError();return;}
        try { controller.requireVerifiedDispatch(authority); } catch { return; }
        setReferences(page);
        setReferenceIssue(null);
      }).catch(err => {
        if (ownRef === refGeneration.current && controller.isCurrentReadAuthority(authority, startReadGen)) {
          setReferenceIssue(err instanceof Error ? err.message : "참조를 새로고침하지 못했습니다.");
        }
        if (ev?.resync) throw err;
      }));
    }
    if (canBacklinks && !backlinkBusy) {
      const ownBack = ++backGeneration.current;
      tasks.push(pClient.versionBacklinks({ versionId, ...backlinkScope(scope, pagePath, regionId), limit: 50 }).then(page => {
        if(controller.readGeneration!==startReadGen)return;
        if(ownBack!==backGeneration.current){if(ev?.resync)throw new StaleResponseError();return;}
        try { controller.requireVerifiedDispatch(authority); } catch { return; }
        setBacklinks(page);
        setBacklinkIssue(null);
      }).catch(err => {
        if (ownBack === backGeneration.current && controller.isCurrentReadAuthority(authority, startReadGen)) {
          setBacklinkIssue(err instanceof Error ? err.message : "백링크를 새로고침하지 못했습니다.");
        }
        if (ev?.resync) throw err;
      }));
    }
    if (ev?.resync) {
      await Promise.all(tasks);
    } else {
      await Promise.allSettled(tasks);
    }
  }, [controller, versionId, pagePath, regionId, scope, canReferences, canBacklinks, referenceBusy, backlinkBusy]);
  useReviewInvalidation(controller, canReferences || canBacklinks, refreshPassive, () => referenceBusy || backlinkBusy);
  async function openReference(row: ReferenceView) {
    const own = ++activation.current, authority = controller.captureDispatchAuthority();
    setNotice(null);
    try {
      controller.requireVerifiedDispatch(authority);
      // Host declaration activation has no child-authored placement claim.
      const current = await client.resolveReference({ versionId, referenceId: row.id });
      controller.requireVerifiedDispatch(authority);
      if (own !== activation.current) return;
      if (current.resolution !== "resolved" || !current.reader_url) { setNotice(referenceStateLabel(current)); return; }
      await onNavigate(current.reader_url, () => own === activation.current);
    } catch (error) { if (own === activation.current) setNotice(error instanceof ApiError && error.status === 410 ? "삭제된 대상입니다." : "현재 권한으로 참조 대상을 열 수 없습니다."); }
  }
  async function openBacklink(url: string) {
    const own = ++activation.current;
    try { await onNavigate(url, () => own === activation.current); }
    catch { if (own === activation.current) setNotice("참조 페이지를 열 수 없습니다."); }
  }
  return <section aria-label="연결 패널">
    <p className="muted-code">{versionId} · {pagePath ?? "버전 전체"}{regionId ? ` · ${regionId}` : ""}</p>
    {notice && <p role="status">{notice}</p>}
    <section aria-label="Authored references"><h3>Authored references</h3>
      {!canReferences ? <p>이 서버에서 작성자 참조를 사용할 수 없습니다.</p> : <>
        <button disabled={referenceBusy} onClick={() => { void loadReferences(); }}>참조 새로고침</button>
        {referenceBusy && <p role="status">참조를 읽는 중…</p>}
        {referenceIssue && <p role="alert">{referenceIssue}</p>}
        {references && <><p>{references.counts.edge_count}개 연결 · {references.counts.referenced_document_count}개 대상 문서</p>
          {references.materialization.state !== "ready" && <p>참조 목록 {references.materialization.state === "failed" ? "구성 실패" : "구성 중"} · 현재 연결 수는 완성된 목록을 뜻하지 않습니다.</p>}
          {references.items.length === 0 && references.materialization.state === "ready" && <p>작성자 참조가 없습니다.</p>}
          <ul>{references.items.map(row => <li key={row.id}>
            {row.resolution === "resolved" ? <button className="link" onClick={() => { void openReference(row); }}>{row.label ?? row.id}</button> : <span>{referenceStateLabel(row)}</span>}
            <p>{row.from.page_path ?? "버전 전체"}{row.from.region_id ? ` · ${row.from.region_id}` : ""}{row.relation ? ` · 작성자 관계: ${row.relation}` : ""}</p>
          </li>)}</ul>
          {references.next_cursor && <button disabled={referenceBusy} onClick={() => { void loadReferences(references.next_cursor ?? undefined); }}>참조 더 보기</button>}
        </>}
      </>}
    </section>
    <section aria-label="Backlinks"><h3>Backlinks</h3>
      <label>백링크 범위 <select value={scope} onChange={event => setScope(event.target.value as typeof scope)}><option value="version">버전 전체</option><option value="page" disabled={!pagePath}>현재 페이지</option><option value="region" disabled={!pagePath || !regionId}>현재 영역</option></select></label>
      {!canBacklinks ? <p>이 서버에서 백링크를 사용할 수 없습니다.</p> : <>
        <button disabled={backlinkBusy} onClick={() => { void loadBacklinks(); }}>백링크 새로고침</button>
        {backlinkBusy && <p role="status">백링크를 읽는 중…</p>}
        {backlinkIssue && <p role="alert">{backlinkIssue}</p>}
        {backlinks && <><p>{backlinks.counts.edge_count}개 연결 · {backlinks.counts.referring_document_count}개 참조 문서</p>
          {backlinks.items.length === 0 && <p>현재 범위에 검증된 백링크가 없습니다.</p>}
          <ul>{backlinks.items.map(row => <li key={row.edge_id}><button className="link" onClick={() => { void openBacklink(row.reader_url); }}>{row.label ?? row.reference_id}</button><p>{row.from.version_id} · {row.from.page_path ?? "버전 전체"}{row.from.region_id ? ` · ${row.from.region_id}` : ""}{row.relation ? ` · 작성자 관계: ${row.relation}` : ""}</p></li>)}</ul>
          {backlinks.next_cursor && <button disabled={backlinkBusy} onClick={() => { void loadBacklinks(backlinks.next_cursor ?? undefined); }}>백링크 더 보기</button>}
        </>}
      </>}
    </section>
    <ManagementLinks controller={controller} versionId={versionId} pagePath={scope==="version"?null:pagePath} regionId={scope==="region"?regionId:null} onNavigate={onNavigate}/>
  </section>;
}
