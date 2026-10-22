import { useEffect, useRef, useState } from "react";
import type { HaeryongClient, SearchRequest } from "../../client/types.ts";
import { navigate } from "../router.tsx";
import { indexReasonText, LibrarySearchSession, matchingPageURL, searchStatus, searchURL } from "./search-state.ts";
import type { SearchViewState } from "./search-state.ts";

export function LibrarySearchForm({ request, projectId, lifecycle, onSearch }: { onSearch: () => void; request: SearchRequest | null; projectId: string; lifecycle: SearchRequest["lifecycle"] }) {
  const [text, setText] = useState(request?.q ?? "");
  const [versions, setVersions] = useState<SearchRequest["versions"]>(request?.versions ?? "latest");
  const [kind, setKind] = useState<SearchRequest["kind"]>(request?.kind);
  useEffect(() => { setText(request?.q ?? ""); setVersions(request?.versions ?? "latest"); setKind(request?.kind); }, [request?.q, request?.versions, request?.kind]);
  const submit = () => navigate(searchURL({ q: text, project_id: projectId || undefined, lifecycle, versions, kind }));
  return <form role="search" className="form-row library-search-form" onSubmit={event => { event.preventDefault(); submit(); onSearch(); }}>
    <label>검색어<input name="q" type="search" value={text} onChange={event => setText(event.target.value)} /></label>
    <label>검색 버전<select aria-label="검색 버전" value={versions} onChange={event => { const value = event.target.value as SearchRequest["versions"]; setVersions(value); if (request) navigate(searchURL({ ...request, versions: value })); }}>
      <option value="latest">최신 버전</option><option value="all">모든 버전</option>
    </select></label>
    <label>검색 종류<select aria-label="검색 종류" value={kind ?? ""} onChange={event => { const value = (event.target.value || undefined) as SearchRequest["kind"]; setKind(value); if (request) navigate(searchURL({ ...request, kind: value })); }}>
      <option value="">전체 종류</option><option value="document">문서</option><option value="page">페이지</option><option value="region">영역</option><option value="source">출처</option><option value="attachment">첨부 파일</option>
    </select></label>
    <button type="submit">검색</button>
    <button type="button" onClick={() => { setText(""); const p = new URLSearchParams(); if (projectId) p.set("project_id", projectId); p.set("lifecycle", lifecycle ?? "active"); navigate("/library?" + p); }}>검색 지우기</button>
  </form>;
}

export function LibrarySearch({ client, request }: { client: HaeryongClient; request: SearchRequest }) {
  const [state, setState] = useState<SearchViewState>({ loading: true, page: null, error: null, reset: false, accessLost: false });
  const session = useRef<LibrarySearchSession | null>(null);
  useEffect(() => {
    const active = new LibrarySearchSession(client, setState); session.current = active;
    void active.start(request);
    return () => { active.dispose(); session.current = null; };
  }, [client, request]);
  // Observe progress through bounded GET polling; no job reads
  // or maintenance operations are available from search presentation.
  useEffect(() => {
    const active = session.current;
    const partial = state.page && ["pending", "partial"].includes(state.page.index.state);
    if (!active || !partial || state.loading || state.error || state.page?.next_cursor) return;
    const timer = window.setTimeout(() => { void active.start(request); }, 5000);
    return () => window.clearTimeout(timer);
  }, [state, request]);
  useEffect(() => {
    const refresh = () => { if (!session.current?.state.loading && !session.current?.state.accessLost) void session.current?.start(request); };
    window.addEventListener("focus", refresh);
    return () => window.removeEventListener("focus", refresh);
  }, [request]);
  useEffect(() => {
    const events = client.subscribeEvents();
    let timer: number | undefined;
    let pending = false;
    let stopped = false;
    const refresh = () => {
      timer = undefined;
      if (stopped || !pending || session.current?.state.accessLost) return;
      if (session.current?.state.loading) { timer = window.setTimeout(refresh, 1000); return; }
      pending = false;
      // Every content-free hint revalidates authority, including accumulated
      // pages. Starting a fresh GET clears protected rows and their cursor.
      void session.current?.start(request);
    };
    const unsubscribe = events.onInvalidation?.(() => { pending = true; if (timer === undefined) timer = window.setTimeout(refresh, 1000); });
    return () => { stopped = true; window.clearTimeout(timer); unsubscribe?.(); events.close(); };
  }, [client, request]);
  const page = state.page;
  return <section aria-label="검색 결과" aria-busy={state.loading} className="library-search-results">
    {state.loading ? <p role="status">검색 중…</p> : null}
    {state.reset ? <p role="status">검색 대상이 변경되어 첫 페이지부터 다시 표시합니다.</p> : null}
    {state.error ? <p role="alert">{state.error}</p> : null}
    <button type="button" disabled={state.loading || state.accessLost} onClick={() => void session.current?.start(request)}>검색 새로고침</button>
    {page ? <>
      <p role="status" data-testid="search-status">{searchStatus(page)}</p>
      <p>HTML {page.index.html_complete ?? "?"}/{page.index.html_total ?? "?"}페이지 완료 · 대기 {page.index.html_pending ?? "?"} · 실패 {page.index.html_failed ?? "?"}</p>
      <ul aria-label="색인 상태 사유">{page.index.reasons.map(reason => <li key={reason.code}>{indexReasonText(reason)}</li>)}</ul>
      {page.index.reasons.some(reason => !["not_indexed", "unsupported_body"].includes(reason.code)) ? <p>완료된 원본은 계속 읽을 수 있습니다. 색인 복구 기능은 아직 제공되지 않습니다.</p> : null}
      <p className="hint">정적 텍스트 검색입니다. 실행 중 생성되는 텍스트와 지원되지 않는 파일 본문은 포함하지 않습니다.</p>
      <ul className="document-list">{page.items.map(hit => <li data-testid="search-hit" key={JSON.stringify([hit.version_id, hit.kind, hit.page_path, hit.region_id, hit.source_id])}>
        <strong>{hit.title}</strong>
        <p className="muted-code">프로젝트 {hit.project_id} · {hit.lifecycle} · 버전 #{hit.sequence} · {hit.version_id}</p>
        {hit.page_path !== null ? <p className="muted-code">{hit.page_path}</p> : null}
        {hit.region_id !== null ? <p>영역 {hit.region_id} · 앵커 {hit.anchor_state}</p> : null}
        {hit.source_id !== null ? <p>출처 {hit.source_id}</p> : null}
        {hit.snippet !== null ? <p>{hit.snippet}</p> : null}
        {hit.kind === "attachment" ? <p>첨부 파일: 정확한 버전의 페이지·파일 목록에서 내려받으세요.</p> : null}
        <a className="action-link" href={matchingPageURL(hit)} onClick={event => { if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return; event.preventDefault(); navigate(matchingPageURL(hit)); }}>{hit.kind === "attachment" ? "파일이 포함된 버전 열기" : hit.page_path === null ? "일치하는 버전 열기" : "일치하는 페이지 열기"}</a>
      </li>)}</ul>
      <p>불러온 검색 항목 {page.items.length}개</p>
      {page.next_cursor ? <button type="button" disabled={state.loading} onClick={() => void session.current?.more()}>검색 결과 더 보기</button> : null}
    </> : null}
  </section>;
}
