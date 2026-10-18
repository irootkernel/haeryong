// Trusted shared Reader. Authored documents live only in IsolatedViewer;
// personal state, import controls and file destinations belong to this host.
import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { ShellController } from "../../client/controller.ts";
import { ApiError } from "../../client/types.ts";
import type { DocumentView, FileEntryView, PagePositionValue, ProjectView, ReadingPositionValue, RenderBootstrapView, VersionView } from "../../client/types.ts";
import { IsolatedViewer } from "../../viewer/isolated-viewer.tsx";
import type { ReaderLocation } from "../../viewer/isolated-viewer.tsx";
import { BrowserPersonalJournal, PersonalOperations, personalKey } from "./personal-operations.ts";
import type { PersonalTarget } from "./personal-operations.ts";
import { ImportControl } from "./import-control.tsx";
import { DownloadPanel } from "./download-panel.tsx";
import { VersionHeader } from "./version-header.tsx";
import { SourcePanel } from "./source-panel.tsx";
import { acceptRegionSelection, parseSuppliedMetadata, readStream, regionSelectionMatches, verifyMetadataBytes, type MetadataState } from "./evidence-view.ts";
import { navigate } from "../router.tsx";

interface ReaderProps {
  controller: ShellController;
  documentId: string;
  versionId: string;
  page: string | null;
  pageQuery: string;
  pageFragment: string;
}
type FileList = { items: FileEntryView[]; cursor: string | null; busy: boolean; issue: string | null };
type Selection = { context: string; bootstrap: RenderBootstrapView; location: ReaderLocation | null; restore?: { scroll_x?: unknown; scroll_y?: unknown } };
const errorText = (cause: unknown) => cause instanceof Error ? cause.message : String(cause);
const html = (path: string) => /\.html?$/i.test(path);
const contextOf = (): string | null => typeof history.state?.haeryongReaderContext === "string" ? history.state.haeryongReaderContext as string : null;
const locationHash = () => globalThis.location.hash;
function readerURL(document: string, version: string, location: ReaderLocation): string {
  const query = new URLSearchParams({ page: location.page });
  if (location.query) query.set("page_query", location.query);
  if (location.fragment) query.set("page_fragment", location.fragment);
  return `/documents/${document}/versions/${version}?${query}`;
}

export function ReaderScreen(props: ReaderProps) {
  const state = props.controller.state, authority = props.controller.captureDispatchAuthority();
  const owner = JSON.stringify([state.capabilities?.library_id, state.session?.principal_id, state.session?.session_id]);
  // Ordinary rechecks leave this epoch stable. A replaced credential or
  // daemon generation cannot reactivate the preceding Reader's callbacks.
  const epoch = JSON.stringify([owner, authority?.nonce, state.capabilities?.runtime_generation]);
  return <ReaderOwner key={epoch + ":" + props.documentId} {...props} owner={owner} epoch={epoch} />;
}

function ReaderOwner(props: ReaderProps & { owner: string; epoch: string }) {
  const { controller, owner } = props;
  const client = controller.client;
  const [result] = useState(() => {
    try {
      const authority = controller.captureDispatchAuthority();
      return { personal: new PersonalOperations(client, new BrowserPersonalJournal(localStorage, owner), () => controller.requireVerifiedDispatch(authority)), error: null };
    } catch (error) { return { personal: null, error: errorText(error) }; }
  });
  if (!result.personal) return <section className="panel" role="alert"><h1>읽기 상태 기록을 열지 못했습니다.</h1><p>{result.error}</p><button onClick={() => location.reload()}>다시 불러오기</button></section>;
  return <ReaderContent {...props} personal={result.personal} />;
}

function ReaderContent(props: ReaderProps & { owner: string; epoch: string; personal: PersonalOperations }) {
  const { controller, documentId, versionId, owner, epoch, personal } = props;
  const client = controller.client;
  useSyncExternalStore(personal.subscribe, personal.revision);
  const [versionDetailState, setVersionDetailState] = useState<{ versionId: string; version: VersionView } | null>(null);
  const [docInfo, setDocInfo] = useState<{ documentId: string; document: DocumentView; projects: ProjectView[] } | null>(null);
  const [detailIssue, setDetailIssue] = useState<string | null>(null);
  const [detailGeneration, setDetailGeneration] = useState(0);
  const [files, setFiles] = useState<FileList>({ items: [], cursor: null, busy: true, issue: null });
  const [selection, setSelection] = useState<Selection | null>(null);
  const [issue, setIssue] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [importOpen, setImportOpen] = useState(false);
  const [download, setDownload] = useState<string | null>(null);
  const [reload, setReload] = useState(0);
  const [suppliedMetaBox, setSuppliedMetaBox] = useState<{ versionId: string; state: MetadataState }>(() => ({ versionId, state: { kind: "loading" } }));
  const [suppliedMetaReload, setSuppliedMetaReload] = useState(0);
  const [regionSelection, setRegionSelection] = useState<{ versionId: string; pagePath: string; context: string | null; reload: number; regionId: string } | null>(null);
  const [evidenceOpen, setEvidenceOpen] = useState(false);
  const frameSelection = useRef<Selection | null>(null);
  const locationRef = useRef<ReaderLocation | null>(null);
  const restoreRef = useRef<{ context: string; position: Selection["restore"] } | null>(null);
  const filesGeneration = useRef(0);
  const live = useRef(true);
  useEffect(() => { live.current = true; return () => { live.current = false; queueMicrotask(() => { if (!live.current) personal.dispose(); }); }; }, [personal]);
  const context = contextOf();
  // The route identity a host-side flow must still match after its awaits.
  // A null context means the router effect has not stamped this initial
  // history entry yet; no bootstrap, open or mount may proceed against it.
  const currentRoute = useRef<{ versionId: string; context: string | null }>({ versionId, context });
  currentRoute.current = { versionId, context };
  const target = (kind: PersonalTarget["kind"], page: string | null = null): PersonalTarget => ({ version: versionId, kind, page });

  useEffect(() => {
    let cancelled = false;
    setDetailIssue(null);
    void (async () => {
      const version = await client.versionDetail(versionId);
      if (version.document_id !== documentId) throw new Error("문서와 버전이 일치하지 않습니다.");
      // The version identity is bound to its exact version ID at store time:
      // after an A→B switch inside one document, a failing document/project
      // lookup must never leave A's sequence/hashes displayed (or used as the
      // metadata validator) under B's identity.
      if (!cancelled) setVersionDetailState({ versionId, version });
      const document = await client.documentDetail(documentId);
      // Fetch only the project cursor needed to identify this document's
      // project; never preload the complete library or file inventory.
      const projects: ProjectView[] = [];
      let cursor: string | undefined;
      do {
        const page = await client.listProjects(cursor);
        if (cancelled) return;
        projects.push(...page.items);
        if (projects.some((project) => project.project_id === document.project_id)) break;
        cursor = page.next_cursor ?? undefined;
      } while (cursor);
      if (!cancelled) setDocInfo({ documentId, document, projects });
    })().catch((error) => { if (!cancelled) setDetailIssue(errorText(error)); });
    return () => { cancelled = true; };
    // A detail retry re-runs exactly this effect; it never touches the
    // viewer, its logical context or the import mount.
  }, [client, documentId, versionId, detailGeneration]);
  // Render/use-time guards: only the CURRENT version's detail may feed the
  // header or the metadata expected digest; a stale document info object
  // never re-enables the exact-document import mount.
  const currentVersion = versionDetailState?.versionId === versionId ? versionDetailState.version : null;
  const currentDocInfo = docInfo?.documentId === documentId ? docInfo : null;

  // Version-scoped supplied metadata: exact bytes verified against every
  // supplied validator (strong ETag plus the version digest when known)
  // before anything parses. Absent optional metadata is an honest absent
  // state; every failure stays independent of the Reader canvas, import
  // mount and session state (T-003-04-01). Every result state is stored with
  // the version it was captured for, and the render-time guard below renders
  // an honest loading state on identity mismatch — a same-document version
  // switch never shows (or verifies against) the previous version's
  // metadata, header, error or panel content on its FIRST commit.
  const setMetaState = (state: MetadataState) => setSuppliedMetaBox({ versionId, state });
  useEffect(() => {
    let cancelled = false;
    let active: Awaited<ReturnType<typeof client.versionSuppliedMetadata>> | null = null;
    const live = () => !cancelled;
    const expectedSha256 = currentVersion?.supplied_metadata_sha256 ?? null;
    setMetaState({ kind: "loading" });
    void (async () => {
      let opened: Awaited<ReturnType<typeof client.versionSuppliedMetadata>>;
      try {
        opened = await client.versionSuppliedMetadata(versionId);
      } catch (error) {
        if (!live()) return;
        if (error instanceof ApiError && error.status === 404) setMetaState({ kind: "absent" });
        else setMetaState({ kind: "error", problem: errorText(error) });
        return;
      }
      if (!live()) { opened.stream.cancel(); return; }
      active = opened;
      try {
        const bytes = await readStream(opened.stream.body, () => !live());
        // Only the CURRENT version's supplied digest is a valid expected
        // validator; without it the strong ETag alone still verifies the
        // exact bytes, and a previous version's digest is never compared
        // against this version's valid bytes.
        const verification = await verifyMetadataBytes(bytes, { etag: opened.etag, expectedSha256 });
        if (!live()) return;
        if (!verification.ok) { setMetaState({ kind: "error", problem: `공급 메타데이터 무결성 확인 실패(${verification.problems.join(", ")})` }); return; }
        const parsed = parseSuppliedMetadata(JSON.parse(new TextDecoder().decode(bytes)));
        if (!live()) return;
        if (parsed === null) { setMetaState({ kind: "error", problem: "공급 메타데이터 파싱 실패" }); return; }
        setMetaState({ kind: "ready", metadata: parsed });
      } catch (error) {
        if (live()) setMetaState({ kind: "error", problem: errorText(error) });
      }
    })().catch(() => { if (live()) setMetaState({ kind: "error", problem: "메타데이터 확인이 중단되었습니다." }); });
    // A read may be parked before the next chunk/EOF. Release this effect's
    // own handle immediately rather than waiting for a chunk to detect staleness.
    return () => { cancelled = true; active?.stream.cancel(); };
  }, [client, versionId, currentVersion?.supplied_metadata_sha256, suppliedMetaReload]);
  // Render-time identity guard: header, error banner and panel all derive
  // from this guarded state, so a stale box renders as honest loading.
  const suppliedMeta: MetadataState = suppliedMetaBox.versionId === versionId ? suppliedMetaBox.state : { kind: "loading" };

  useEffect(() => {
    // Explicit readiness gate: the initial history entry must carry its
    // stamped context before any selection bootstrap runs. This checks the
    // actual current context value (and the viewer mount gate below checks it
    // again), never an effect-order assumption.
    if (context === null) return;
    // A mirrored child URL keeps its existing browsing context and session.
    if (frameSelection.current?.context === context && frameSelection.current.bootstrap.version_id === versionId && reload === 0) return;
    let cancelled = false;
    setIssue(null); setSelection(null); setRegionSelection(null);
    const captured = controller.captureDispatchAuthority();
    const guard = () => { if (cancelled) throw new Error("이전 페이지 선택입니다."); controller.requireVerifiedDispatch(captured); };
    void (async () => {
      guard();
      const version = await client.versionDetail(versionId);
      guard(); if (version.document_id !== documentId) throw new Error("문서와 버전이 일치하지 않습니다.");
      let selected = props.page;
      if (selected === null) {
        const preference = await personal.read(target("reader-preference"));
        guard();
        const preferred = preference.exists ? (preference.value as { default_page: string | null }).default_page : null;
        if (preferred !== null) {
          try {
            const boot = await client.renderBootstrap(versionId, preferred);
            guard(); if (boot.selected_page !== null) selected = boot.selected_page;
          } catch (error) { if (!(error instanceof ApiError && [404, 422].includes(error.status))) throw error; }
        }
      }
      const bootstrap = await client.renderBootstrap(versionId, selected ?? undefined);
      guard();
      const location = bootstrap.selected_page === null ? null : { page: bootstrap.selected_page, query: props.pageQuery, fragment: props.pageFragment };
      const restore = restoreRef.current?.context === context ? restoreRef.current.position : undefined;
      const next: Selection = { context, bootstrap, location, restore };
      frameSelection.current = next; locationRef.current = location;
      setSelection(next);
      if (location === null) void personal.save(target("reading-position"), { page_path: null, page_query: "", page_fragment: "", opened: true }).catch((error) => { if (!cancelled) setNotice(errorText(error)); });
    })().catch((error) => { if (!cancelled) setIssue(errorText(error)); });
    return () => { cancelled = true; };
    // Page/query/hash are intentionally read only at a new HOST context.
    // Child observations update the URL without reopening the delivery.
  }, [client, controller, documentId, versionId, context, personal, reload]);

  useEffect(() => {
    const own = ++filesGeneration.current;
    setFiles({ items: [], cursor: null, busy: true, issue: null });
    void client.versionFiles(versionId).then((page) => {
      if (filesGeneration.current === own) setFiles({ items: page.items, cursor: page.next_cursor, busy: false, issue: null });
    }).catch((error) => { if (filesGeneration.current === own) setFiles({ items: [], cursor: null, busy: false, issue: errorText(error) }); });
    return () => { filesGeneration.current++; };
  }, [client, versionId]);

  const more = async () => {
    if (!files.cursor || files.busy) return;
    const own = filesGeneration.current, cursor = files.cursor;
    setFiles((old) => ({ ...old, busy: true, issue: null }));
    try {
      const page = await client.versionFiles(versionId, cursor);
      if (own === filesGeneration.current) setFiles((old) => ({ items: [...old.items, ...page.items], cursor: page.next_cursor, busy: false, issue: null }));
    } catch (error) { if (own === filesGeneration.current) setFiles((old) => ({ ...old, busy: false, issue: errorText(error) })); }
  };
  const mirror = (location: ReaderLocation) => {
    locationRef.current = location;
    // Region selection is page-local: navigation clears it until the new
    // page's own region event arrives with its captured tuple.
    setRegionSelection(null);
    history.replaceState(history.state, "", readerURL(documentId, versionId, location) + locationHash());
    window.dispatchEvent(new PopStateEvent("popstate"));
  };
  const captureRegion = (event: { versionId: string; pagePath: string; context: string | null; regionId: string }) => {
    // Late or cross-page/cross-context region events are matched against the
    // event's OWN captured tuple (the viewer channel's context) and the
    // current selection; an old callback can never retag its region as a new
    // context's selection.
    if (acceptRegionSelection({ versionId: event.versionId, pagePath: event.pagePath, regionId: event.regionId }, { versionId, pagePath: locationRef.current?.page ?? null })) {
      setRegionSelection({ versionId: event.versionId, pagePath: event.pagePath, context: event.context, reload, regionId: event.regionId });
    }
  };
  const continueReading = async () => {
    const authority = controller.captureDispatchAuthority();
    const route = currentRoute.current;
    try {
      const pointer = await personal.read(target("reading-position"));
      if (!pointer.exists || !(pointer.value as ReadingPositionValue).page_path) { setNotice("이어서 읽을 페이지가 아직 저장되지 않았습니다."); return; }
      const value = pointer.value as ReadingPositionValue;
      const page = value.page_path!;
      const position = await personal.read(target("page-position", page));
      controller.requireVerifiedDispatch(authority);
      if (currentRoute.current.versionId !== route.versionId || currentRoute.current.context !== route.context || !live.current) return;
      const location = { page, query: value.page_query, fragment: value.page_fragment };
      const nextContext = crypto.randomUUID();
      restoreRef.current = { context: nextContext, position: position.exists ? (position.value as PagePositionValue).position : undefined };
      history.pushState({ haeryongReaderContext: nextContext }, "", readerURL(documentId, versionId, location));
      window.dispatchEvent(new PopStateEvent("popstate"));
    } catch (error) { setNotice(errorText(error)); }
  };
  const setDefault = async (clear = false) => {
    const current = locationRef.current;
    if (!current && !clear) return;
    await personal.save(target("reader-preference"), { default_page: clear ? null : current!.page });
    const entry = personal.entry(target("reader-preference"));
    setNotice(entry?.pending || entry?.conflict || entry?.issue ? "기본 페이지 저장이 확인되지 않았습니다. 아래 저장 상태를 확인하세요." : clear ? "기본 페이지 설정을 해제했습니다." : "기본 페이지를 저장했습니다.");
  };
  const entries = personal.entries().filter((entry) => entry.pending || entry.conflict || entry.issue || entry.queued);
  // Page fallback belongs ONLY to a selection of the CURRENT version and
  // CURRENT host context: a page-less route never inherits the previous
  // selection's page on its first commit (the bootstrap default resolves
  // normally once it belongs to this identity).
  const currentPage = props.page ?? (selection !== null && selection.bootstrap.version_id === versionId && selection.context === context ? selection.location?.page ?? null : null);
  // Region ownership is render-guarded on the full [version,page,context,
  // reload] identity: any mismatch renders as unselected, so a stale scalar
  // region cannot reach the panel (or a first query) under a new selection.
  const regionId = regionSelection !== null
    && regionSelectionMatches(regionSelection, { versionId, pagePath: currentPage, context, reload })
    ? regionSelection.regionId : null;
  return <div className="reader">
    <header className="reader-header"><div><h1>{currentVersion?.title_snapshot ?? docInfo?.document.title ?? "결과 읽기"}</h1><p className="muted-code">{versionId}</p>
        <VersionHeader version={currentVersion} pagePath={currentPage} metadata={suppliedMeta.kind === "ready" ? suppliedMeta.metadata : null} />
        {suppliedMeta.kind === "error" && <div className="banner" data-tone="warning" role="alert"><p>공급 메타데이터를 확인하지 못했습니다: {suppliedMeta.problem} — 결과 읽기에는 영향이 없습니다.</p><button onClick={() => setSuppliedMetaReload((value) => value + 1)}>메타데이터 다시 불러오기</button></div>}
      </div>
      <div className="form-row"><button onClick={() => void continueReading()}>이어서 읽기</button><button disabled={!selection?.location} onClick={() => void setDefault().catch((error) => setNotice(errorText(error)))}>기본 페이지로 지정</button><button onClick={() => void setDefault(true).catch((error) => setNotice(errorText(error)))}>기본 페이지 설정 해제</button><button aria-expanded={evidenceOpen} onClick={() => setEvidenceOpen((value) => !value)}>근거 패널</button><button disabled={!currentDocInfo} onClick={() => setImportOpen(true)}>새 버전 가져오기</button></div>
    </header>
    {notice && <p role="status">{notice}</p>}
    {entries.map((entry) => <div className="banner" data-tone="warning" key={personalKey(entry.target)}>
      <p>{entry.target.version.slice(0, 8)} · {{ "reader-preference": "기본 페이지", "reading-position": "마지막 연 페이지", "page-position": "페이지 위치" }[entry.target.kind]}{entry.target.page ? ` · ${entry.target.page}` : ""}: {entry.issue ?? (personal.recoverable(entry.target) ? "이전 화면의 저장 결과가 확인되지 않았습니다. 원래 요청으로 다시 확인하세요." : entry.conflict ? "다른 기기에서 변경된 상태입니다. 유지할 값을 선택하세요." : "저장 결과를 확인하는 중입니다.")}</p>
      {entry.conflict && !entry.pending ? <div className="form-row"><button disabled={personal.busy(entry.target)} onClick={() => void personal.resolve(entry.target, "server").catch((error) => setNotice(errorText(error)))}>서버 값 유지</button><button disabled={personal.busy(entry.target)} onClick={() => void personal.resolve(entry.target, "local").catch((error) => setNotice(errorText(error)))}>현재 상태 다시 저장</button></div> : entry.issue || personal.recoverable(entry.target) ? <button disabled={personal.busy(entry.target)} onClick={() => void personal.retry(entry.target).catch((error) => setNotice(errorText(error)))}>원래 요청으로 다시 확인</button> : null}
    </div>)}
    {detailIssue && <div className="banner" data-tone="warning" role="alert"><p>문서 정보를 불러오지 못했습니다: {detailIssue}</p><button onClick={() => setDetailGeneration((value) => value + 1)}>문서 정보 다시 불러오기</button></div>}
    {issue && <div className="banner" role="alert"><p>페이지를 열지 못했습니다: {issue}</p><button onClick={() => { frameSelection.current = null; setReload((value) => value + 1); }}>다시 불러오기</button></div>}
    {selection && !selection.bootstrap.renderer_available && <div className="banner" role="alert"><p>렌더러를 사용할 수 없습니다: {selection.bootstrap.renderer_unavailable_reason}</p><p>페이지와 원본 파일은 아래 목록에서 확인할 수 있습니다.</p></div>}
    <div className="reader-body"><aside className="reader-side" aria-label="페이지 및 파일 목록"><h2>페이지</h2><ul>{files.items.filter((item) => item.kind === "file" && html(item.path)).map((item) => <li key={item.path}><a href={readerURL(documentId, versionId, { page: item.path, query: "", fragment: "" })} onClick={(event) => { if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return; event.preventDefault(); navigate(readerURL(documentId, versionId, { page: item.path, query: "", fragment: "" })); }}>{item.path}</a></li>)}</ul>
      <h2>파일</h2><ul>{files.items.filter((item) => !(item.kind === "file" && html(item.path))).map((item) => <li key={item.path}>{item.kind === "file" ? <button className="link" onClick={() => setDownload(item.path)}>{item.path}</button> : <span>{item.path} ({item.kind})</span>}</li>)}</ul>
      {files.issue && <div role="alert"><p>파일 목록 오류: {files.issue}</p><button onClick={() => { if (files.cursor) void more(); else { const own = ++filesGeneration.current; setFiles((old) => ({ ...old, busy: true })); void client.versionFiles(versionId).then((page) => { if (own === filesGeneration.current) setFiles({ items: page.items, cursor: page.next_cursor, busy: false, issue: null }); }).catch((error) => { if (own === filesGeneration.current) setFiles((old) => ({ ...old, busy: false, issue: errorText(error) })); }); } }}>목록 다시 불러오기</button></div>}{files.busy && <p role="status">목록을 불러오는 중…</p>}{files.cursor && <button disabled={files.busy} onClick={() => void more()}>더 불러오기</button>}
    </aside>
    {selection?.bootstrap.renderer_available && selection.location && context !== null && selection.context === context ? <IsolatedViewer key={JSON.stringify([versionId, selection.context, reload])} controller={controller} owner={owner} context={selection.context} documentId={documentId} versionId={versionId} initial={selection.location} restore={selection.restore} personal={personal} onLocation={mirror} onNotice={setNotice} onRegion={captureRegion} /> : !issue && !selection ? <p role="status">결과를 확인하는 중…</p> : selection?.location === null ? <p>페이지를 선택하거나 원본 파일을 내려받으세요.</p> : null}
    </div>
    {evidenceOpen && <section className="reader-evidence" aria-label="근거 패널">
      <SourcePanel controller={controller} version={currentVersion} versionId={versionId} pagePath={currentPage} regionId={regionId} contextKey={`${context ?? "initial"}:${reload}`} metadata={suppliedMeta} />
    </section>}
    {download !== null && <div><button onClick={() => setDownload(null)}>파일 내려받기 닫기</button><DownloadPanel key={versionId + ":" + download} controller={controller} versionId={versionId} filePath={download} /></div>}
    {importOpen && currentDocInfo && <section><button onClick={() => setImportOpen(false)}>가져오기 패널 닫기</button><ImportControl key={`${epoch}|${currentDocInfo.document.project_id}|${documentId}`} controller={controller} projects={currentDocInfo.projects} documents={[currentDocInfo.document]} preselected={{ projectId: currentDocInfo.document.project_id, documentId }} onFinished={() => setImportOpen(false)} onClose={() => setImportOpen(false)} /></section>}
  </div>;
}
