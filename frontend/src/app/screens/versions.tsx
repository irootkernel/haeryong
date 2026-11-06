import {useReviewInvalidation} from "./review-refresh.ts";
import {StaleResponseError} from "../../client/types.ts";
import type { ReviewView } from "../../client/types.ts";
import { exactReview } from "./review-summary.ts";
import { reviewLabel } from "./review-operations.ts";
import { useEffect, useRef, useState } from "react";
import type { ShellController } from "../../client/controller.ts";
import type { DocumentView, FileEntryView, VersionView } from "../../client/types.ts";
import { navigate } from "../router.tsx";
import { pinnedReaderURL } from "../routes.ts";
import { ImportControl } from "./import-control.tsx";
import { DownloadPanel } from "./download-panel.tsx";
import { SourcePanel } from "./source-panel.tsx";
import { sourceObservationLabel } from "./source-presentation.ts";
import { referringPage } from "./reader-navigation.ts";
import { returnFromVersions } from "./deep-links.ts";
import { ComparisonReads, defaultPair, FileComparison, loadComparisonMetadata, metadataDifferences, type ComparisonMetadata, type FileDifference } from "./versions-data.ts";

const message = (error: unknown) => error instanceof Error ? error.message : String(error);
export function VersionsScreen({ controller, documentId }: { controller: ShellController; documentId: string }) {
  const [reviews,setReviews]=useState<Record<string,ReviewView>>({});
  const reviewEnabled=controller.state.capabilities?.features.review===true;
  const [doc, setDoc] = useState<DocumentView | null>(null), [versions, setVersions] = useState<VersionView[]>([]);
  const [loaded, setLoaded] = useState(false), [issue, setIssue] = useState<string | null>(null), [refresh, setRefresh] = useState(0);
  const [importOpen, setImportOpen] = useState(false);
  const [actionIssue, setActionIssue] = useState<string | null>(null);
  const [pair, setPair] = useState<[string, string] | null>(null);
  const [sourceStatus, setSourceStatus] = useState("관측 기록 확인 중…");
  const client = controller.client;
  const lifetime = useRef(0),viewRead=useRef(0),statusRead=useRef(0);
  useEffect(() => { lifetime.current++; return () => { lifetime.current++; }; }, []);
  const adoptVersions=(detail:DocumentView,all:VersionView[])=>{
    setDoc(detail);setVersions([...all]);
    const query=new URLSearchParams(location.search);
    const selected=defaultPair(all,query.get("from_version"),query.get("to_version"),detail.latest_version_id,reviewEnabled?detail.last_reviewed_version_id??null:null);
    if(selected&&(!query.has("from_version")||!query.has("to_version"))){
      query.set("from_version",selected[0]);query.set("to_version",selected[1]);
      history.replaceState(history.state,"",`/documents/${documentId}/versions?${query}`);
      window.dispatchEvent(new PopStateEvent("popstate"));
    }
    setPair(current=>current??selected);setLoaded(true);
  };
  useReviewInvalidation(
    controller,
    true,
    async (ev) => {
      const captured = controller.captureDispatchAuthority();
      const readGen = ev?.readGeneration ?? controller.readGeneration;
      const gen = lifetime.current,run=++viewRead.current,statusRun=++statusRead.current;
      const current=()=>{
        if(gen!==lifetime.current||!controller.isCurrentReadAuthority(captured,readGen))return false;
        if(run!==viewRead.current){if(ev?.resync)throw new StaleResponseError();return false;}
        return true;
      };
      const passive = controller.passiveClient;
      try {
        const detail = await passive.documentDetail(documentId);
        if(!current())return;
        let cursor: string | undefined;
        const all: VersionView[] = [];
        do {
          const page = await passive.documentVersions(documentId, cursor);
          if(!current())return;
          if (page.items.some(v => v.document_id !== documentId)) throw Error("문서와 버전이 일치하지 않습니다.");
          all.push(...page.items);
          cursor = page.next_cursor ?? undefined;
        } while (cursor);
        adoptVersions(detail,all);
        if (reviewEnabled) {
          const nextReviews: Record<string, ReviewView> = {};
          for (const v of all) {
            const r = await passive.reviewGet(v.version_id);
            if(!current())return;
            if (r.version_id !== v.version_id) throw Error("검토 버전이 일치하지 않습니다.");
            nextReviews[v.version_id] = r;
          }
          setReviews(old => ({ ...old, ...nextReviews }));
        }
        try{
          const value=await passive.serviceStatus();
          if(!current())return;
          if(statusRun!==statusRead.current){if(ev?.resync)throw new StaleResponseError();return;}
          const source=(value as {source?:{available?:boolean;reason?:string|null;last_observed_at?:string|null}}).source;
          setSourceStatus(sourceObservationLabel(source));
        }catch(error){
          if(gen===lifetime.current&&run===viewRead.current&&statusRun===statusRead.current)setSourceStatus("관측 기록을 사용할 수 없습니다.");
          if(ev?.resync)throw error;
        }
        setIssue(null);
      } catch (error) {
        if (gen === lifetime.current && run===viewRead.current&&controller.isCurrentReadAuthority(captured, readGen)) {
          setIssue(message(error));
        }
        if (ev?.resync) throw error;
      }
    },
  );
  const action = (work: (guard: () => void) => void | Promise<void>) => {
    const authority = controller.captureDispatchAuthority(), route = location.href, entry = history.state?.haeryongReaderContext, generation = lifetime.current;
    const current = () => lifetime.current === generation && location.href === route && history.state?.haeryongReaderContext === entry;
    const guard = () => {
      controller.requireVerifiedDispatch(authority);
      if (!current()) throw Error("버전 화면이 변경되었습니다.");
    };
    setActionIssue(null);
    void Promise.resolve().then(() => { guard(); return work(guard); }).catch(error => { if (current()) setActionIssue(message(error)); });
  };
  const owner = JSON.stringify([controller.state.capabilities?.library_id, controller.state.session?.principal_id, controller.state.capabilities?.runtime_generation]);
  useEffect(() => {
    let active = true;const run=++viewRead.current;
    const current=()=>active&&run===viewRead.current;
    setIssue(null); setLoaded(false);
    void (async () => {
      const detail = await client.documentDetail(documentId);
      if (!current()) return; setDoc(detail);
      let cursor: string | undefined; const all: VersionView[] = [];
      do {
        const page = await client.documentVersions(documentId, cursor);
        if (!current()) return;
        if (page.items.some(v => v.document_id !== documentId)) throw Error("문서와 버전이 일치하지 않습니다.");
        all.push(...page.items); setVersions([...all]); cursor = page.next_cursor ?? undefined;
      } while (cursor);
      if(!current())return;adoptVersions(detail,all);
      if(reviewEnabled) for(const v of all){
        const review=await client.reviewGet(v.version_id);if(!current())return;
        if(review.version_id!==v.version_id)throw Error("검토 버전이 일치하지 않습니다.");
        setReviews(old=>({...old,[v.version_id]:review}));
      }
    })().catch(error => { if (current()) setIssue(message(error)); });
    return () => { active = false; };
  }, [client, documentId, refresh, reviewEnabled]);
  useEffect(() => {
    let active = true;const run=++statusRead.current;
    void client.serviceStatus().then(value => {
      const source = (value as { source?: { available?: boolean; reason?: string | null; last_observed_at?: string | null } }).source;
      if (active&&run===statusRead.current) setSourceStatus(sourceObservationLabel(source));
    }).catch(() => { if (active&&run===statusRead.current) setSourceStatus("관측 기록을 사용할 수 없습니다."); });
    return () => { active = false; };
  }, [client, refresh]);
  const change = (which: number, value: string) => {
    const next = [...pair!] as [string, string]; next[which] = value;
    const query = new URLSearchParams({ from_version: next[0], to_version: next[1] });
    history.replaceState({ ...history.state, haeryongReaderContext: crypto.randomUUID() }, "", `/documents/${documentId}/versions?${query}`);
    window.dispatchEvent(new PopStateEvent("popstate"));
  };
  return <section className="panel versions-screen" aria-label="버전 및 변경">
    <h1>버전 및 변경 — {doc?.title ?? "문서 확인 중"}</h1>
    <div className="form-row">
      {referringPage(history.state?.haeryongVersionsReturn?.source, owner) && <button id="versions-return" onClick={() => action(() => { returnFromVersions(owner); })}>읽던 페이지로 돌아가기</button>}
      <button id="versions-open-latest" onClick={() => action(async guard => { const current = await client.documentDetail(documentId); guard(); if (current.latest_version_id) navigate(pinnedReaderURL(documentId, current.latest_version_id)); })}>최신 버전 열기</button>
      <button disabled={!doc} onClick={() => setImportOpen(value => !value)}>새 버전 가져오기</button>
    </div>
    <p>{reviewEnabled ? "개인 Review는 아래 각 버전의 실제 상태입니다. 마지막 검토 완료 버전은 기본 비교 선택에 사용됩니다." : "개인 Review: 아직 제공되지 않습니다. 정확한 버전을 직접 선택할 수 있습니다."}</p>
    <section aria-label="원본 가용성 및 변경 관측"><h2>원본 가용성 및 변경 관측</h2><p>{sourceStatus}</p><p>서재의 마지막 관측 기록입니다. 의미 평가나 개인 Review 완료를 뜻하지 않습니다. 이 화면은 새 소스 검사를 시작하지 않습니다.</p></section>
    {actionIssue && <p role="alert">{actionIssue}</p>}
    {issue && <div role="alert"><p>비교를 사용할 수 없습니다: {issue}</p><button onClick={() => action(() => setRefresh(n => n + 1))}>버전 이력 다시 불러오기</button></div>}
    {!loaded && !issue && <p role="status">버전 이력 확인 중… {versions.length}건(미완료)</p>}
    <h2>정확한 버전 이력</h2>
    <ul>{versions.map(version => <li key={version.version_id}><a href={pinnedReaderURL(documentId, version.version_id)} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return; event.preventDefault(); navigate(event.currentTarget.getAttribute("href")!); }}>{version.sequence} · {version.title_snapshot} · {version.version_id}</a> · 출처 {version.source_basis}{reviewEnabled ? <span data-review-version={version.version_id}> · 내 검토: {exactReview(reviews,version.version_id) ? reviewLabel(exactReview(reviews,version.version_id)!) : "확인 중"}{exactReview(reviews,version.version_id)?.value.reviewed_at ? ` · ${exactReview(reviews,version.version_id)!.value.reviewed_at}` : ""}</span> : null}{version.version_id === doc?.latest_version_id ? " · 최신 버전" : ""}</li>)}</ul>
    {loaded && !pair && <p>발행된 버전이 없습니다.</p>}
    {loaded && pair && <>
      <div className="form-row">{["이전 비교 버전", "대상 비교 버전"].map((label, i) => <label className="field" key={label}>{label}<select id={i === 0 ? "versions-from" : "versions-to"} value={pair[i]} onChange={event => { const value = event.target.value; action(() => change(i, value)); }}>{versions.map(v => <option key={v.version_id} value={v.version_id}>{v.sequence} · {v.title_snapshot} · {v.version_id}</option>)}</select></label>)}</div>
      <VersionComparison key={JSON.stringify(pair)} controller={controller} documentId={documentId} before={versions.find(v => v.version_id === pair[0])!} after={versions.find(v => v.version_id === pair[1])!} />
    </>}
    {importOpen && doc && <ImportControl controller={controller} projects={[]} documents={[doc]} preselected={{ projectId: doc.project_id, documentId }} onFinished={() => { setRefresh(n => n + 1); }} onClose={() => setImportOpen(false)} />}
  </section>;
}

function useMetadata(controller: ShellController, version: VersionView): ComparisonMetadata {
  const [box, setBox] = useState<ComparisonMetadata>({ state: { kind: "loading" }, raw: null });
  useEffect(() => {
    const reads = new ComparisonReads();
    void loadComparisonMetadata(controller.client, version, reads).then(value => { if (reads.live) setBox(value); });
    return () => reads.dispose();
  }, [controller, version]);
  return box;
}
function VersionComparison({ controller, documentId, before, after }: { controller: ShellController; documentId: string; before: VersionView; after: VersionView }) {
  const mergeRef = useRef<FileComparison | null>(null);
  const [rows, setRows] = useState<FileDifference[]>([]), [count, setCount] = useState(0), [busy, setBusy] = useState(false), [complete, setComplete] = useState(false), [error, setError] = useState<string | null>(null);
  const [download, setDownload] = useState<{ version: string; path: string } | null>(null);
  const [previews, setPreviews] = useState<Array<{ version: string; entry: FileEntryView; generation: number } | null>>([null, null]);
  const [page, setPage] = useState(""), [region, setRegion] = useState(""), [refresh, setRefresh] = useState(0);
  const a = useMetadata(controller, before), b = useMetadata(controller, after);
  const load = async () => {
    const merge = mergeRef.current; if (!merge) return;
    setBusy(true); setError(null);
    try { const result = await merge.next(); if (mergeRef.current !== merge) return; setRows(result); setCount(n => n + result.length); setComplete(merge.complete); }
    catch (cause) { if (mergeRef.current === merge) setError(message(cause)); }
    finally { if (mergeRef.current === merge) setBusy(false); }
  };
  useEffect(() => {
    const merge = new FileComparison((side, cursor) => controller.client.versionFiles(side === 0 ? before.version_id : after.version_id, cursor));
    mergeRef.current = merge; void load();
    return () => { merge.dispose(); if (mergeRef.current === merge) mergeRef.current = null; };
  }, [controller, before.version_id, after.version_id]);
  const fields = (v: VersionView) => ({ title: v.title_snapshot, default_page: v.default_page, source_basis: v.source_basis });
  const regions = (box: ReturnType<typeof useMetadata>) => Object.fromEntries((box.state.kind === "ready" ? box.state.metadata.regions : []).map(r => [JSON.stringify([r.page_path, r.id]), r]));
  const metadataReady = [a, b].every(box => box.state.kind === "ready" || box.state.kind === "absent");
  const changes = metadataReady ? metadataDifferences({ ...fields(before), ...(a.raw ?? {}), regions: regions(a) }, { ...fields(after), ...(b.raw ?? {}), regions: regions(b) }) : metadataDifferences(fields(before), fields(after));
  return <section aria-label="정확한 버전 비교">
    <h2>파일 및 메타데이터 비교</h2>
    <p id="versions-progress" role="status">{complete ? "비교 완료 · 두 버전의 모든 경로 확인" : "비교 미완료"} · 확인한 경로 {count}개{busy ? " · 나머지 경로 확인 중…" : ""} · 지금 표시하는 묶음 {rows.length}개</p>
    {error && <p role="alert">파일 비교 페이지 오류: {error}</p>}
    {!complete && <button id="versions-more" disabled={busy} onClick={() => void load()}>{error ? "실패한 지점부터 다시 시도" : "다음 비교 경로"}</button>}
    <table aria-label="파일 차이"><thead><tr><th>경로 / 변화</th><th>이전 버전</th><th>대상 버전</th></tr></thead><tbody>{rows.map(row => <tr key={row.path}><th scope="row">{row.path} · {row.change}</th>{[row.before, row.after].map((entry, i) => <td key={i}>{entry ? <><p>{entry.kind} · {entry.bytes_sha256 ?? entry.link_target ?? "디렉터리"}</p>{entry.kind === "file" && <><a href={pinnedReaderURL(documentId, i === 0 ? before.version_id : after.version_id, entry.path)}>정확한 파일 열기</a><button onClick={() => setPreviews(previous => previous.map((value, index) => index === i ? { version: i === 0 ? before.version_id : after.version_id, entry, generation: (value?.generation ?? 0) + 1 } : value))}>텍스트 보기 · {i === 0 ? "이전" : "대상"}</button><button onClick={() => setDownload({ version: i === 0 ? before.version_id : after.version_id, path: entry.path })}>원본 내려받기</button></>}</> : "없음(다른 버전으로 대체하지 않음)"}</td>)}</tr>)}</tbody></table>
    <div className="versions-texts">{previews.map((preview, i) => preview && <TextPreview key={JSON.stringify([i, preview.version, preview.entry.path, preview.generation])} controller={controller} version={preview.version} entry={preview.entry} />)}</div>
    {download && <DownloadPanel key={JSON.stringify([download.version, download.path])} controller={controller} versionId={download.version} filePath={download.path} />}
    <h3>문서 메타데이터 / 페이지별 영역 차이</h3>
    {[a, b].map((box, i) => <p key={i}>{i === 0 ? "이전" : "대상"} 메타데이터: {box.state.kind}{box.state.kind === "error" ? " · " + box.state.problem : ""} · 선언되지 않거나 아직 색인되지 않은 영역은 의미 비교가 불가능합니다.</p>)}
    {!metadataReady && <p>공급 메타데이터/영역 비교 미완료 또는 사용 불가 — 차이를 확정하지 않습니다.</p>}
    <ul id="versions-metadata">{changes.map(change => <li key={change.field}>{change.field}<pre>{change.before}</pre><pre>{change.after}</pre></li>)}</ul>
    <p>바이트·메타데이터 차이는 의미상 동등함을 보장하지 않습니다. 같은 영역 이름도 개인 기록을 옮기지 않습니다.</p>
    <h3>대상 버전 의미 평가(개인 Review와 별도)</h3>
    <label className="field">평가 페이지(비우면 버전 전체)<input value={page} onChange={e => { setPage(e.target.value); setRegion(""); }} /></label>
    <label className="field">페이지의 영역(선택)<input value={region} disabled={!page} onChange={e => setRegion(e.target.value)} /></label>
    <button onClick={() => setRefresh(n => n + 1)}>선택한 대상 평가 새로고침</button>
    <SourcePanel controller={controller} version={after} versionId={after.version_id} pagePath={page || null} regionId={page && region ? region : null} contextKey={JSON.stringify([before.version_id, after.version_id, refresh])} metadata={b.state} />
  </section>;
}
function TextPreview({ controller, version, entry }: { controller: ShellController; version: string; entry: FileEntryView }) {
  const [text, setText] = useState(""), [complete, setComplete] = useState(false), [error, setError] = useState<string | null>(null);
  const [block, setBlock] = useState(0), [waiting, setWaiting] = useState(false);
  const next = useRef<(() => void) | null>(null);
  useEffect(() => {
    const reads = new ComparisonReads();
    void reads.text(controller.client, version, entry, (chunk, done) => { if (!done || chunk) setText(chunk); if (!done) setBlock(n => n + 1); setComplete(done); }, () => new Promise<void>(resolve => { next.current = resolve; setWaiting(true); })).catch(cause => { if (reads.live) { setText(""); setError(message(cause)); } });
    return () => { next.current?.(); reads.dispose(); };
  }, [controller, version, entry]);
  return <section aria-label="선택한 파일 텍스트"><h3>{entry.path} · {version}</h3><p>{complete ? "텍스트 열거 완료 · 무결성 확인됨" : "텍스트 미완료 · 무결성 확인 전"} · 표시 블록 {block} (이전 블록은 원본 내려받기 또는 다시 열기로 확인)</p>{!complete && !error && <button disabled={!waiting} onClick={() => { setWaiting(false); next.current?.(); }}>다음 텍스트 블록</button>}{error ? <p role="alert">{error} · 원본 내려받기를 사용할 수 있습니다.</p> : <pre className="evidence-excerpt">{text}</pre>}</section>;
}
