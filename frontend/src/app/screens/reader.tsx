import {LifecycleControl} from "./lifecycle-control.tsx";
import {useReviewInvalidation} from "./review-refresh.ts";
import {AskPanel} from "./ask-panel.tsx";
import {NotesPanel,type NoteSelection} from "./notes-panel.tsx";
import {RevisitControl} from "./revisit-control.tsx";
import { ReviewControl } from "./review-control.tsx";
// Trusted shared Reader. Authored documents live only in IsolatedViewer;
// personal state, import controls and file destinations belong to this host.
import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import type { ShellController } from "../../client/controller.ts";
import { ApiError } from "../../client/types.ts";
import type { DocumentView, FileEntryView, PagePositionValue, ProjectView, ReadingPositionValue, RenderBootstrapView, VersionView } from "../../client/types.ts";
import { IsolatedViewer } from "../../viewer/isolated-viewer.tsx";
import type { ReaderLocation } from "../../viewer/isolated-viewer.tsx";
import { Sheet } from "../sheet.tsx";
import { BrowserPersonalJournal, PersonalOperations, personalKey } from "./personal-operations.ts";
import type { PersonalTarget } from "./personal-operations.ts";
import { ImportControl } from "./import-control.tsx";
import { DownloadPanel } from "./download-panel.tsx";
import { VersionHeader } from "./version-header.tsx";
import { LinksPanel } from "./links-panel.tsx";
import { ReaderLinksNavigation } from "./links-navigation.ts";
import { SourcePanel } from "./source-panel.tsx";
import { acceptRegionFocusResult, acceptRegionSelection, parseSuppliedMetadata, readStream, regionRequestStale, regionSelectionMatches, regionsOfPage, verifyMetadataBytes, type MetadataState } from "./evidence-view.ts";
import { featureEnabled } from "./library-data.ts";
import { ReaderReferences, readerChannelReady, HostRegionFocus, regionFocusMatches, pushReference, referringPage, returnToReferringPage, type ReaderPageContext, type RegionIntent } from "./reader-navigation.ts";
import { restoreVersionsFocus } from "./deep-links.ts";
import { ReaderVersionActions } from "./reader-version-actions.tsx";
import { navigate } from "../router.tsx";

interface ReaderProps {
  controller: ShellController;
  documentId: string;
  versionId: string;
  page: string | null;
  pageQuery: string;
  pageFragment: string;
  regionIntent?: RegionIntent | null;
}
type FileList = { items: FileEntryView[]; cursor: string | null; busy: boolean; issue: string | null };
type Selection = { context: string; bootstrap: RenderBootstrapView; location: ReaderLocation | null; restore?: { scroll_x?: unknown; scroll_y?: unknown } };
const errorText = (cause: unknown) => cause instanceof Error ? cause.message : String(cause);
const html = (path: string) => /\.html?$/i.test(path);
/** Local Files filter over ALREADY LOADED entries only; the cursor hint keeps
 *  incomplete coverage honest and this is never server Library search. */
const filterMatches = (item: FileEntryView, filter: string): boolean => { const query = filter.trim().toLowerCase(); return query === "" || item.path.toLowerCase().includes(query); };
const contextOf = (): string | null => typeof history.state?.haeryongReaderContext === "string" ? history.state.haeryongReaderContext as string : null;
const locationHash = () => globalThis.location.hash;
/** Live CSS breakpoint mirror; the layout itself stays pure CSS so no view is
 *  ever remounted by a resize — this only picks sheet-vs-column semantics. */
function useMedia(query: string): boolean {
  const [matches, setMatches] = useState(() => matchMedia(query).matches);
  useEffect(() => {
    const media = matchMedia(query);
    const onChange = () => setMatches(media.matches);
    media.addEventListener("change", onChange);
    return () => media.removeEventListener("change", onChange);
  }, [query]);
  return matches;
}
function readerURL(document: string, version: string, location: ReaderLocation): string {
  const query = new URLSearchParams({ page: location.page });
  if (location.query) query.set("page_query", location.query);
  if (location.fragment) query.set("page_fragment", location.fragment);
  return `/documents/${document}/versions/${version}?${query}`;
}

export function ReaderScreen(props: ReaderProps) {
  const state = props.controller.state, authority = props.controller.captureDispatchAuthority();
  const confirmedNonce = useRef(authority?.nonce);
  if (authority !== null) confirmedNonce.current = authority.nonce;
  const owner = JSON.stringify([state.capabilities?.library_id, state.session?.principal_id, state.session?.session_id]);
  // Keep unsent drafts mounted through a same-authority outage. This retained
  // nonce is only a mount key; dispatch still requires verified authority.
  // A replaced credential or daemon generation creates a new Reader.
  const epoch = JSON.stringify([owner, confirmedNonce.current, state.capabilities?.runtime_generation]);
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
 const [lifecycleBlocked,setLifecycleBlocked]=useState(false);const visibilityEra=useRef(0);
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
  const [download, setDownload] = useState<{ versionId: string; path: string } | null>(null);
  const downloadRegion = useRef<HTMLDivElement | null>(null);
  // A file choice and its consent belong to the selected version. Discard it
  // on a version switch; the render guard also covers the first B commit.
  useEffect(() => { setDownload(null); }, [versionId]);
  useEffect(() => {
    if (download?.versionId !== versionId) return;
    const region = downloadRegion.current;
    const files = document.getElementById("reader-files-panel");
    const focus = () => { region?.focus(); region?.scrollIntoView({ block: "nearest" }); };
    // The Files sheet closes through its native dialog effect. If it still
    // owns the top layer, wait for the actual close event before moving focus.
    if (files instanceof HTMLDialogElement && files.matches(":modal")) {
      files.addEventListener("close", focus, { once: true });
      return () => files.removeEventListener("close", focus);
    }
    focus();
  }, [download, versionId]);
  const [reload, setReload] = useState(0);
  const [suppliedMetaBox, setSuppliedMetaBox] = useState<{ versionId: string; state: MetadataState }>(() => ({ versionId, state: { kind: "loading" } }));
  const [suppliedMetaReload, setSuppliedMetaReload] = useState(0);
  const [regionSelection, setRegionSelection] = useState<{ versionId: string; pagePath: string; context: string | null; reload: number; regionId: string; intentKey: string | null } | null>(null);
  const [contextTab, setContextTab] = useState<"evidence" | "notes" | "links" | "ask">("evidence");
  const [noteContext,setNoteContext]=useState<(ReaderPageContext & {payload:Record<string,unknown>})|null>(null);
  const [contextOpen, setContextOpen] = useState(false);
  const [filesOpen, setFilesOpen] = useState(false);
  const [filesFilter, setFilesFilter] = useState("");
  // ≥1280px: Context is the declared 340px third column while open. 768–1279:
  // Context overlays the reader. <768: single column; Files and Context are
  // full-height sheets. One state drives every mode, so existing panel
  // drivers keep their click-to-open semantics at any viewport.
  const wideLayout = useMedia("(min-width: 1280px)");
  const phoneLayout = useMedia("(max-width: 767px)");
  const filesVisible = !phoneLayout || filesOpen;
  const filesModal = phoneLayout && filesOpen;
  const contextVisible = contextOpen;
  const contextModal = !wideLayout && contextOpen;
  // Exactly ONE modal at a time: opening either panel closes the other, and a
  // breakpoint crossing that would make both modal resolves by closing Files.
  useEffect(() => { if (phoneLayout && filesOpen && contextOpen) setFilesOpen(false); }, [phoneLayout, filesOpen, contextOpen]);
  // Host region outline → real in-frame region focus through the live channel.
  const regionFocus = useRef<((regionId: string) => boolean) | null>(null);
  const frameSelection = useRef<Selection | null>(null);
  const locationRef = useRef<ReaderLocation | null>(null);
  const restoreRef = useRef<{ context: string; position: Selection["restore"] } | null>(null);
  const filesGeneration = useRef(0);
  const live = useRef(true);
  useEffect(() => { live.current = true; return () => { live.current = false; queueMicrotask(() => { if (!live.current) personal.dispose(); }); }; }, [personal]);
  const context = contextOf();
  const routeRegion = props.regionIntent?.regionId ?? null;
  const [hashFocus] = useState(() => new HostRegionFocus());
  const [channelReady, setChannelReady] = useState<ReaderPageContext | null>(null);
  const [linksNavigation] = useState(() => new ReaderLinksNavigation());
  const channelRef = useRef<ReaderPageContext | null>(null);
  const referenceRoute = useRef({ versionId, context, page: props.page, reload });
  const referenceOwner = JSON.stringify([controller.state.capabilities?.library_id, controller.state.session?.principal_id, controller.state.capabilities?.runtime_generation]);
  const references = useMemo(() => {
    const authority = controller.captureDispatchAuthority();
    return new ReaderReferences({ client,
      guard: () => controller.requireVerifiedDispatch(authority),
      current: () => {
        const channel = channelRef.current, route = referenceRoute.current;
        return channel && live.current && channel.versionId === route.versionId && channel.pagePath === route.page && channel.context === route.context && channel.reload === route.reload ? channel : null;
      },
      navigate: (url, position) => pushReference(url, referenceOwner, position), notice: setNotice,
    });
  }, [client, controller, referenceOwner]);
  const referencesEnabled = featureEnabled(controller.state.capabilities?.features, "references");
  references.enable(referencesEnabled);
  // The route identity a host-side flow must still match after its awaits.
  // A null context means the router effect has not stamped this initial
  // history entry yet; no bootstrap, open or mount may proceed against it.
  const currentRoute = useRef<{ versionId: string; context: string | null }>({ versionId, context });
  currentRoute.current = { versionId, context };
  const target = (kind: PersonalTarget["kind"], page: string | null = null): PersonalTarget => ({ version: versionId, kind, page });

  useEffect(() => {
    if (lifecycleBlocked) return;
    let cancelled = false;
    const era = visibilityEra.current, valid = () => !cancelled && era === visibilityEra.current;
    setDetailIssue(null);
    void (async () => {
      const version = await client.versionDetail(versionId);
      if (version.document_id !== documentId) throw new Error("문서와 버전이 일치하지 않습니다.");
      // The version identity is bound to its exact version ID at store time:
      // after an A→B switch inside one document, a failing document/project
      // lookup must never leave A's sequence/hashes displayed (or used as the
      // metadata validator) under B's identity.
      if (valid()) setVersionDetailState({ versionId, version });
      const document = await client.documentDetail(documentId);
      // Fetch only the project cursor needed to identify this document's
      // project; never preload the complete library or file inventory.
      const projects: ProjectView[] = [];
      let cursor: string | undefined;
      do {
        const page = await client.listProjects(cursor);
        if (!valid()) return;
        projects.push(...page.items);
        if (projects.some((project) => project.project_id === document.project_id)) break;
        cursor = page.next_cursor ?? undefined;
      } while (cursor);
      if (valid()) setDocInfo({ documentId, document, projects });
    })().catch((error) => { if (valid()) setDetailIssue(errorText(error)); });
    return () => { cancelled = true; };
    // A detail retry re-runs exactly this effect; it never touches the
    // viewer, its logical context or the import mount.
  }, [client, documentId, versionId, detailGeneration, lifecycleBlocked]);
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
    if (lifecycleBlocked) return;
    let cancelled = false;
    let active: Awaited<ReturnType<typeof client.versionSuppliedMetadata>> | null = null;
    const era = visibilityEra.current;
    const live = () => !cancelled && era === visibilityEra.current;
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
  }, [client, versionId, currentVersion?.supplied_metadata_sha256, suppliedMetaReload, lifecycleBlocked]);
  // Render-time identity guard: header, error banner and panel all derive
  // from this guarded state, so a stale box renders as honest loading.
  const suppliedMeta: MetadataState = suppliedMetaBox.versionId === versionId ? suppliedMetaBox.state : { kind: "loading" };

  useEffect(() => {
    // Explicit readiness gate: the initial history entry must carry its
    // stamped context before any selection bootstrap runs. This checks the
    // actual current context value (and the viewer mount gate below checks it
    // again), never an effect-order assumption.
    if (lifecycleBlocked || context === null) return;
    // A mirrored child URL keeps its existing browsing context and session.
    if (frameSelection.current?.context === context && frameSelection.current.bootstrap.version_id === versionId && reload === 0) return;
    let cancelled = false;
    setIssue(null); setSelection(null); setRegionSelection(null);
    const captured = controller.captureDispatchAuthority(), visibleEra=visibilityEra.current;
    const guard = () => { if (cancelled||visibleEra!==visibilityEra.current) throw new Error("이전 페이지 선택입니다."); controller.requireVerifiedDispatch(captured); };
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
      const retained = referringPage(history.state?.haeryongReaderPosition, referenceOwner);
      const currentURL = locationHash();
      const restore = restoreRef.current?.context === context ? restoreRef.current.position
        : location !== null && retained?.context === context && retained.url === readerURL(documentId, versionId, location) + currentURL ? retained.position : undefined;
      const next: Selection = { context, bootstrap, location, restore };
      frameSelection.current = next; locationRef.current = location;
      setSelection(next);
      if (location === null) void personal.save(target("reading-position"), { page_path: null, page_query: "", page_fragment: "", opened: true }).catch((error) => { if (!cancelled) setNotice(errorText(error)); });
    })().catch((error) => { if (!cancelled) setIssue(errorText(error)); });
    return () => { cancelled = true; };
    // Page/query/hash are intentionally read only at a new HOST context.
    // Child observations update the URL without reopening the delivery.
  }, [client, controller, documentId, versionId, context, personal, reload, lifecycleBlocked]);

  useEffect(() => {
    if (lifecycleBlocked) return;
    const own = ++filesGeneration.current;
    setFiles({ items: [], cursor: null, busy: true, issue: null });
    void client.versionFiles(versionId).then((page) => {
      if (filesGeneration.current === own) setFiles({ items: page.items, cursor: page.next_cursor, busy: false, issue: null });
    }).catch((error) => { if (filesGeneration.current === own) setFiles({ items: [], cursor: null, busy: false, issue: errorText(error) }); });
    return () => { filesGeneration.current++; };
  }, [client, versionId, lifecycleBlocked]);

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
    const changedPage = locationRef.current?.page !== location.page;
    locationRef.current = location;
    if (changedPage) setRegionSelection(null);
    history.replaceState(history.state, "", readerURL(documentId, versionId, location) + (changedPage ? "" : locationHash()));
    window.dispatchEvent(new PopStateEvent("popstate"));
  };
  const captureRegion = (event: ReaderPageContext & { regionId: string }) => {
    if (event.context !== context || event.reload !== reload || event.renderGeneration !== channelRef.current?.renderGeneration) return;
    // Late or cross-page/cross-context region events are matched against the
    // event's OWN captured tuple (the viewer channel's context) and the
    // current selection; an old callback can never retag its region as a new
    // context's selection.
    if (acceptRegionSelection({ versionId: event.versionId, pagePath: event.pagePath, regionId: event.regionId }, { versionId, pagePath: locationRef.current?.page ?? null })) {
      setRegionSelection({ versionId: event.versionId, pagePath: event.pagePath, context: event.context, reload, regionId: event.regionId, intentKey: routeRegion });
    }
  };
  // Host outline selection: bind the exact current page's region context
  // first (no source/evidence privilege), then attempt REAL in-frame focus.
  // While the Context sheet is modal the background is inert, so the focus
  // request waits for the dialog's ACTUAL close event (never elapsed
  // frames); a stale route, an explicit same-context reload, a replaced
  // channel handle or an unmounted Reader cancels the pending request.
  const reloadRef = useRef(reload);
  reloadRef.current = reload;
  const selectRegion = (region: { id: string; label?: unknown }) => {
    if (currentPage === null) { setNotice("페이지가 열려 있지 않아 영역을 선택할 수 없습니다."); return; }
    selectedRegionRef.current = region.id;
    setRegionSelection({ versionId, pagePath: currentPage, context, reload, regionId: region.id, intentKey: routeRegion });
    const captured = { versionId, context, page: currentPage, reload, handle: regionFocus.current as unknown };
    const cancelled = () => selectedRegionRef.current !== region.id || regionRequestStale(captured, {
      live: live.current,
      versionId: currentRoute.current.versionId,
      context: currentRoute.current.context,
      page: locationRef.current?.page ?? null,
      reload: reloadRef.current,
      handle: regionFocus.current as unknown,
    });
    const attempt = () => {
      if (cancelled()) return;
      if (regionFocus.current?.(region.id) !== true) setNotice("페이지 채널이 아직 준비되지 않아 영역 초점을 요청하지 못했습니다.");
    };
    if (contextModal) {
      setContextOpen(false);
      const node = document.getElementById("reader-context-panel");
      if (node === null) return;
      const onClosed = () => { cleanupClose(); if (!cancelled()) attempt(); };
      const cleanupClose = () => { node.removeEventListener("close", onClosed); clearTimeout(failsafe); };
      node.addEventListener("close", onClosed);
      // Bounded failsafe only: a dialog that never emits close must not leak
      // the listener forever; cancellation still governs any late attempt.
      const failsafe = setTimeout(() => {
        cleanupClose();
        if (!cancelled() && node instanceof HTMLDialogElement && !node.open) attempt();
      }, 2000);
    } else attempt();
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
  referenceRoute.current = { versionId, context, page: currentPage, reload };
  hashFocus.bind(currentPage !== null && context !== null ? { versionId, pagePath: currentPage, context, reload } : null, routeRegion);
  const regionId = regionSelection !== null && regionSelection.intentKey === routeRegion
    && regionSelectionMatches(regionSelection, { versionId, pagePath: currentPage, context, reload })
    ? regionSelection.regionId : hashFocus.selection()?.regionId ?? null;
  const selectedRegionRef = useRef(regionId);
  selectedRegionRef.current = regionId;
  useEffect(() => {
    // Selection belongs to this route immediately; focus waits for the exact
    // page's explicit ready callback. The outline path keeps modal-close rules.
    hashFocus.flush(channelReady, id => selectRegion({ id }));
  }, [routeRegion, versionId, currentPage, context, reload, channelReady]);
  const returnContext = referringPage(history.state?.haeryongReferenceReturn, referenceOwner);
  const navigationContext = JSON.stringify([epoch, documentId, versionId, currentPage, regionId, context, reload, props.pageQuery, props.pageFragment]);
  useEffect(() => { references.bind(navigationContext); return () => references.bind(null); }, [references, navigationContext]);
  const linksContext = contextVisible && contextTab === "links" ? navigationContext : null;
  useEffect(() => { linksNavigation.bind(linksContext); return () => linksNavigation.bind(null); }, [linksNavigation, linksContext]);
  useEffect(() => {
    // A settled frameless Reader cannot produce channel/region readiness.
    // It still owns the S05 opener and must consume the return-focus intent.
    if (context !== null && (issue !== null || (selection?.context === context && selection.bootstrap.version_id === versionId
      && (!selection.bootstrap.renderer_available || selection.location === null)))) restoreVersionsFocus();
  }, [selection, issue, context, versionId]);
  const closeProtectedReader = () => {
    visibilityEra.current++; filesGeneration.current++;
    frameSelection.current = null; locationRef.current = null; restoreRef.current = null;
    channelRef.current = null; regionFocus.current = null;
    references.bind(null); linksNavigation.bind(null); hashFocus.bind(null, null);
    setSelection(null); setChannelReady(null); setNoteContext(null); setRegionSelection(null);
    setFiles({items:[],cursor:null,busy:false,issue:null});
    setDocInfo(null); setVersionDetailState(null); setSuppliedMetaBox({versionId,state:{kind:"absent"}});
    setNotice(null); setIssue(null); setDetailIssue(null); setDownload(null); setImportOpen(false);
    setFilesOpen(false); setContextOpen(false); setLifecycleBlocked(true);
    restoreVersionsFocus();
  };
  // A fresh host navigation, never a notification/restore, can open a new delivery.
  useEffect(() => { setLifecycleBlocked(false); }, [versionId, context]);
  useReviewInvalidation(controller,true,()=>{
    const auth=controller.captureDispatchAuthority(), era=visibilityEra.current, route={...referenceRoute.current};
    void client.versionDetail(versionId).then(()=>{controller.requireVerifiedDispatch(auth);}).catch(e=>{
      const current=referenceRoute.current;
      if(era!==visibilityEra.current||route.versionId!==current.versionId||route.context!==current.context||route.reload!==current.reload)return;
      if(e instanceof ApiError&&[401,403,404,410].includes(e.status)){
        closeProtectedReader();
      }
    });
  });
  if(lifecycleBlocked)return <section role="alert"><h1>이 문서를 더 이상 표시할 수 없습니다.</h1><p>문서 상태 또는 접근 권한이 바뀌어 페이지를 닫았습니다.</p><a href="/trash">휴지통 열기</a><a href="/library">서재 열기</a></section>;
  return <div className="reader">
    <header className="reader-header"><div><h1>{currentVersion?.title_snapshot ?? docInfo?.document.title ?? "결과 읽기"}</h1><p className="muted-code">{versionId}</p>
        <VersionHeader version={currentVersion} pagePath={currentPage} metadata={suppliedMeta.kind === "ready" ? suppliedMeta.metadata : null} />
        {suppliedMeta.kind === "error" && <div className="banner" data-tone="warning" role="alert"><p>공급 메타데이터를 확인하지 못했습니다: {suppliedMeta.problem} — 결과 읽기에는 영향이 없습니다.</p><button onClick={() => setSuppliedMetaReload((value) => value + 1)}>메타데이터 다시 불러오기</button></div>}
      </div></header>
    <div className="form-row reader-actions"><ReaderVersionActions key={JSON.stringify([epoch, versionId, currentPage, props.pageQuery, props.pageFragment, regionId, context])} controller={controller} documentId={documentId} versionId={versionId} page={currentPage} query={props.pageQuery} fragment={props.pageFragment} region={regionId} owner={referenceOwner} readPosition={async () => {
      const saved = currentPage ? await personal.read(target("page-position", currentPage)) : null;
      const position = saved?.exists ? (saved.value as PagePositionValue).position : null;
      return { scroll_x: Number(position?.scroll_x) || 0, scroll_y: Number(position?.scroll_y) || 0 };
    }} />{returnContext && <button onClick={() => { controller.requireVerifiedDispatch(controller.captureDispatchAuthority()); returnToReferringPage(referenceOwner); }}>참조한 페이지로 돌아가기</button>}<button onClick={() => void continueReading()}>이어서 읽기</button><button disabled={!selection?.location} onClick={() => void setDefault().catch((error) => setNotice(errorText(error)))}>기본 페이지로 지정</button><button onClick={() => void setDefault(true).catch((error) => setNotice(errorText(error)))}>기본 페이지 설정 해제</button><button id="reader-files-toggle" className="narrow-only" aria-expanded={filesOpen} aria-controls="reader-files-panel" onClick={() => { setContextOpen(false); setFilesOpen((value) => !value); }}>파일 목록</button><button id="reader-context-toggle" aria-expanded={contextOpen} aria-controls="reader-context-panel" onClick={() => { setFilesOpen(false); setContextOpen((value) => !value); }}>근거 패널</button><button disabled={!currentDocInfo} onClick={() => setImportOpen(true)}>새 버전 가져오기</button></div>
    <ReviewControl key={JSON.stringify([referenceOwner,versionId])} controller={controller} versionId={versionId} owner={referenceOwner} versionLabel={currentVersion?`v${currentVersion.sequence}`:undefined} />
    <LifecycleControl controller={controller} documentId={documentId} lifecycle={currentDocInfo?.document.lifecycle??"active"} onChanged={result=>{if(result.lifecycle==="trash"){closeProtectedReader();}else{setDetailGeneration(n=>n+1);}}}/>
    <RevisitControl key={JSON.stringify([referenceOwner,documentId])} controller={controller} documentId={documentId} />
    {notice && <p role="status">{notice}</p>}
    {entries.map((entry) => <div className="banner" data-tone="warning" key={personalKey(entry.target)}>
      <p>{entry.target.version.slice(0, 8)} · {{ "reader-preference": "기본 페이지", "reading-position": "마지막 연 페이지", "page-position": "페이지 위치" }[entry.target.kind]}{entry.target.page ? ` · ${entry.target.page}` : ""}: {entry.issue ?? (personal.recoverable(entry.target) ? "이전 화면의 저장 결과가 확인되지 않았습니다. 원래 요청으로 다시 확인하세요." : entry.conflict ? "다른 기기에서 변경된 상태입니다. 유지할 값을 선택하세요." : "저장 결과를 확인하는 중입니다.")}</p>
      {entry.conflict && !entry.pending ? <div className="form-row"><button disabled={personal.busy(entry.target)} onClick={() => void personal.resolve(entry.target, "server").catch((error) => setNotice(errorText(error)))}>서버 값 유지</button><button disabled={personal.busy(entry.target)} onClick={() => void personal.resolve(entry.target, "local").catch((error) => setNotice(errorText(error)))}>현재 상태 다시 저장</button></div> : entry.issue || personal.recoverable(entry.target) ? <button disabled={personal.busy(entry.target)} onClick={() => void personal.retry(entry.target).catch((error) => setNotice(errorText(error)))}>원래 요청으로 다시 확인</button> : null}
    </div>)}
    {detailIssue && <div className="banner" data-tone="warning" role="alert"><p>문서 정보를 불러오지 못했습니다: {detailIssue}</p><button onClick={() => setDetailGeneration((value) => value + 1)}>문서 정보 다시 불러오기</button></div>}
    {issue && <div className="banner" role="alert"><p>페이지를 열지 못했습니다: {issue}</p><button onClick={() => { frameSelection.current = null; setReload((value) => value + 1); }}>다시 불러오기</button></div>}
    {selection && !selection.bootstrap.renderer_available && <div className="banner" role="alert"><p>렌더러를 사용할 수 없습니다: {selection.bootstrap.renderer_unavailable_reason}</p><p>페이지와 원본 파일은 아래 목록에서 확인할 수 있습니다.</p></div>}
    <div className="reader-body"><Sheet id="reader-files-panel" openerId="reader-files-toggle" className="reader-side" closeOnEscape={phoneLayout} open={filesVisible} modal={filesModal} label="페이지 및 파일 목록" onClose={() => setFilesOpen(false)}><h2>페이지 및 파일</h2>
      <label className="field" htmlFor="reader-files-filter"><span>불러온 항목 필터</span><input id="reader-files-filter" type="text" value={filesFilter} onChange={(event) => setFilesFilter(event.target.value)} placeholder="페이지·파일 이름으로 좁히기" /></label>
      {files.cursor !== null && filesFilter.trim() !== "" && <p className="hint" role="note">필터는 지금까지 불러온 항목에만 적용됩니다. ‘더 불러오기’로 나머지 항목을 먼저 확인하세요.</p>}
      {phoneLayout && <button onClick={() => setFilesOpen(false)}>파일 목록 닫기</button>}
      <h3>페이지</h3><ul>{files.items.filter((item) => item.kind === "file" && html(item.path) && filterMatches(item, filesFilter)).map((item) => <li key={item.path}><a aria-current={item.path === currentPage ? "page" : undefined} href={readerURL(documentId, versionId, { page: item.path, query: "", fragment: "" })} onClick={(event) => { if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return; event.preventDefault(); navigate(readerURL(documentId, versionId, { page: item.path, query: "", fragment: "" })); if (filesModal) setFilesOpen(false); }}>{item.path}</a></li>)}</ul>
      <h3>파일</h3><ul>{files.items.filter((item) => !(item.kind === "file" && html(item.path)) && filterMatches(item, filesFilter)).map((item) => <li key={item.path}>{item.kind === "file" ? <button className="link" onClick={() => { setDownload({ versionId, path: item.path }); setFilesOpen(false); }}>{item.path}</button> : <span>{item.path} ({item.kind})</span>}</li>)}</ul>
      {files.issue && <div role="alert"><p>파일 목록 오류: {files.issue}</p><button onClick={() => { if (files.cursor) void more(); else { const own = ++filesGeneration.current; setFiles((old) => ({ ...old, busy: true })); void client.versionFiles(versionId).then((page) => { if (own === filesGeneration.current) setFiles({ items: page.items, cursor: page.next_cursor, busy: false, issue: null }); }).catch((error) => { if (own === filesGeneration.current) setFiles((old) => ({ ...old, busy: false, issue: errorText(error) })); }); } }}>목록 다시 불러오기</button></div>}{files.busy && <p role="status">목록을 불러오는 중…</p>}{files.cursor && <button disabled={files.busy} onClick={() => void more()}>더 불러오기</button>}
    </Sheet>
    {selection?.bootstrap.renderer_available && selection.location && context !== null && selection.context === context ? <IsolatedViewer key={JSON.stringify([versionId, selection.context, reload])} controller={controller} owner={owner} context={selection.context} documentId={documentId} versionId={versionId} initial={selection.location} restore={selection.restore} initialRegion={routeRegion ?? undefined} reload={reload} personal={personal}
      onChannelReady={(page) => {
        const route = referenceRoute.current;
        if (!readerChannelReady(page, { versionId: route.versionId, context: route.context ?? "", reload: route.reload })) return;
        channelRef.current = page; setChannelReady(page); if (routeRegion === null) restoreVersionsFocus();
      }}
      onChannelClosed={() => { channelRef.current = null; setChannelReady(null); }}
      onDeliveryRevoked={(failed) => {
        const route = referenceRoute.current;
        if (live.current && failed.versionId === route.versionId && failed.context === route.context && failed.reload === route.reload && contextOf() === failed.context) closeProtectedReader();
      }}
      onConnectionFailure={(failed) => {
        const route = referenceRoute.current;
        if (failed.versionId === route.versionId && failed.context === route.context && failed.reload === route.reload
          && contextOf() === failed.context) restoreVersionsFocus();
      }}
      onNoteContext={event=>{if(event.versionId===versionId&&event.pagePath===currentPage&&event.context===context&&event.reload===reload&&event.renderGeneration===channelRef.current?.renderGeneration)setNoteContext(event);}} onReference={referencesEnabled ? event => { void references.request(event); } : undefined} focusHandle={regionFocus} onLocation={mirror} onNotice={setNotice} onRegion={captureRegion} onRegionFocus={(result) => { if (result.context !== context || result.reload !== reload || !regionFocusMatches(result, channelRef.current, selectedRegionRef.current)) return; if (!acceptRegionFocusResult(result, { versionId, pagePath: currentPage })) return; restoreVersionsFocus(); if (!result.found) setNotice(`선언된 영역 ${result.regionId}에 대응하는 문서 요소를 찾지 못했습니다. 영역 ID는 메타데이터의 논리 식별자이며 읽기에는 영향이 없습니다.`); else if (!result.focused) setNotice(`영역 ${result.regionId}에 초점을 맞추지 못했습니다(오버레이가 열려 있거나 페이지가 초점을 받을 수 없습니다). 본문에서 방향키로 읽은 뒤 영역을 다시 선택하면 초점이 이동하는 경우가 있습니다.`); else setNotice(`영역 ${result.regionId}에 초점을 맞췄습니다.`); }} /> : !issue && !selection ? <p role="status">결과를 확인하는 중…</p> : selection?.location === null ? <p>페이지를 선택하거나 원본 파일을 내려받으세요.</p> : null}
    <Sheet id="reader-context-panel" openerId="reader-context-toggle" className="reader-context" open={contextVisible} modal={contextModal} label="문맥 및 근거 패널" onClose={() => setContextOpen(false)}>
      {!wideLayout && <button onClick={() => setContextOpen(false)}>문맥 닫기</button>}
      {suppliedMeta.kind === "ready" && currentPage !== null && regionsOfPage(suppliedMeta.metadata, currentPage).length > 0 && <div className="region-outline"><h3>영역 개요(현재 페이지)</h3><ul>{regionsOfPage(suppliedMeta.metadata, currentPage).map((region) => <li key={region.id}><button className="link" data-selected={regionId === region.id ? "true" : undefined} aria-current={regionId === region.id ? "true" : undefined} onClick={() => selectRegion(region)}>{typeof region.label === "string" && region.label !== "" ? region.label : region.id}</button></li>)}</ul><p className="hint">영역을 선택하면 해당 페이지의 문맥이 그 영역에 묶이고, 페이지 요소에 실제 초점을 시도해 결과를 알립니다. 좁은 화면에서는 문맥 패널이 닫힌 뒤 초점이 시도됩니다. 선언된 영역이 문서 요소와 연결되지 않아도 읽기·페이지 문맥은 그대로 유지됩니다.</p></div>}
      <div role="tablist" aria-label="문맥 탭" onKeyDown={event => {
        const tabs = ["evidence", "notes", "ask", "links"] as const;
        let next = tabs.indexOf(contextTab);
        if (event.key === "ArrowRight" || event.key === "ArrowLeft") next = (next+(event.key==="ArrowRight"?1:tabs.length-1))%tabs.length;
        else if (event.key === "Home") next = 0; else if (event.key === "End") next = tabs.length-1; else return;
        event.preventDefault(); const selected = tabs[next]!; setContextTab(selected); document.getElementById(`reader-${selected}-tab`)?.focus();
      }}>{(["evidence", "notes", "ask", "links"] as const).map(tab => <button key={tab} id={`reader-${tab}-tab`} role="tab" aria-selected={contextTab === tab} aria-controls={`reader-${tab}-content`} tabIndex={contextTab === tab ? 0 : -1} onClick={() => setContextTab(tab)}>{tab === "evidence" ? "Evidence" : tab==="notes"?"Notes":tab==="links"?"Links":"Ask"}</button>)}</div>
      <div role="tabpanel" id="reader-evidence-content" aria-labelledby="reader-evidence-tab" hidden={contextTab !== "evidence"} className="reader-evidence">
      <SourcePanel controller={controller} version={currentVersion} versionId={versionId} pagePath={currentPage} regionId={regionId} contextKey={`${context ?? "initial"}:${reload}`} metadata={suppliedMeta} />
      </div>
      <div role="tabpanel" id="reader-notes-content" aria-labelledby="reader-notes-tab" hidden={contextTab!=="notes"}><NotesPanel key={JSON.stringify([referenceOwner,versionId])} controller={controller} documentId={documentId} owner={referenceOwner} active={contextVisible&&contextTab==="notes"} modal={contextModal} navigationContext={navigationContext} anchor={{version_id:versionId,page_path:currentPage,region_id:regionId,page_query:props.pageQuery,page_fragment:props.pageFragment}} selection={noteContext&&noteContext.versionId===versionId&&noteContext.pagePath===currentPage&&noteContext.context===context&&noteContext.reload===reload&&noteContext.renderGeneration===channelRef.current?.renderGeneration?{selectedText:typeof noteContext.payload.selected_text==="string"?noteContext.payload.selected_text:null,contextRef:typeof noteContext.payload.context_ref==="string"?noteContext.payload.context_ref:null,context:noteContext.payload.context??noteContext.payload.state??null} satisfies NoteSelection:undefined}/></div>
      <div role="tabpanel" id="reader-ask-content" aria-labelledby="reader-ask-tab" hidden={contextTab!=="ask"}>{currentDocInfo&&<AskPanel key={JSON.stringify([referenceOwner,documentId])} controller={controller} documentId={documentId} projectId={currentDocInfo.document.project_id} sourceIntent={suppliedMeta.kind==="ready"?suppliedMeta.metadata.sourceIntent:null} sourceReady={suppliedMeta.kind==="ready"||suppliedMeta.kind==="absent"} active={contextVisible&&contextTab==="ask"} anchor={{version_id:versionId,page_path:currentPage,region_id:regionId,page_query:props.pageQuery,page_fragment:props.pageFragment}} selection={noteContext&&noteContext.versionId===versionId&&noteContext.pagePath===currentPage&&noteContext.context===context&&noteContext.reload===reload&&noteContext.renderGeneration===channelRef.current?.renderGeneration?{contextRef:typeof noteContext.payload.context_ref==="string"?noteContext.payload.context_ref:null,context:noteContext.payload.context??noteContext.payload.state??null}:{contextRef:null,context:null}}/>}</div>
      {contextVisible && contextTab === "links" && <div role="tabpanel" id="reader-links-content" aria-labelledby="reader-links-tab"><LinksPanel key={`${referenceOwner}:${versionId}:${currentPage}:${regionId}:${context}:${reload}`} controller={controller} versionId={versionId} pagePath={currentPage} regionId={regionId} onNavigate={async (url, active) => {
        const authority = controller.captureDispatchAuthority();
        await linksNavigation.request({
          active: () => live.current && active(),
          guard: () => controller.requireVerifiedDispatch(authority),
          route: () => location.pathname + location.search + location.hash,
          readPosition: async () => {
            const saved = currentPage ? await personal.read(target("page-position", currentPage)) : null;
            const position = saved?.exists ? (saved.value as PagePositionValue).position : null;
            return { scroll_x: Number(position?.scroll_x) || 0, scroll_y: Number(position?.scroll_y) || 0 };
          },
          navigate: position => pushReference(url, referenceOwner, position),
        });
      }} /></div>}
    </Sheet>
    </div>
    {download?.versionId === versionId && <div ref={downloadRegion} role="region" aria-label="선택한 파일 내려받기" tabIndex={-1}><button onClick={() => setDownload(null)}>파일 내려받기 닫기</button><DownloadPanel key={download.versionId + ":" + download.path} controller={controller} versionId={download.versionId} filePath={download.path} /></div>}
    {importOpen && currentDocInfo && <section><button onClick={() => setImportOpen(false)}>가져오기 패널 닫기</button><ImportControl key={`${epoch}|${currentDocInfo.document.project_id}|${documentId}`} controller={controller} projects={currentDocInfo.projects} documents={[currentDocInfo.document]} preselected={{ projectId: currentDocInfo.document.project_id, documentId }} onFinished={() => setImportOpen(false)} onClose={() => setImportOpen(false)} /></section>}
  </div>;
}
