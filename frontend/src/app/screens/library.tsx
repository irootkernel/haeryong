// Library screen: real document list with project/lifecycle filters in
// registered query keys, honest loaded-vs-total counts, one-directory-one-
// document rows with page/file inventory entry, the shared import control,
// the labelled Reader/Versions preselection harness, and the original-
// directory archive download entry for the latest version. Static search uses
// exact-version results only when the service explicitly advertises it.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ShellController } from "../../client/controller.ts";
import { ApiError } from "../../client/types.ts";
import type { DocumentView, FileEntryView, ListPage, ProjectView } from "../../client/types.ts";
import { navigate, useShellRoute } from "../router.tsx";
import { accumulateDocuments, countLabel, featureEnabled, filterFromSearch, filterToSearch } from "./library-data.ts";
import type { DocumentAccumulation, LibraryFilter } from "./library-data.ts";
import { LibrarySearch, LibrarySearchForm } from "./library-search.tsx";
import { searchFromURL, searchURL } from "./search-state.ts";
import { DownloadPanel } from "./download-panel.tsx";
import { ImportControl } from "./import-control.tsx";

interface LibraryScreenProps {
  controller: ShellController;
}

interface InventoryState {
  document: DocumentView;
  entries: FileEntryState;
}

type FileEntryState =
  | { kind: "loading" }
  | { kind: "loaded"; files: FileEntryView[]; exhausted: boolean; cursor: string | null; loadingMore?: boolean; error?: string }
  | { kind: "error"; message: string };

export function LibraryScreen({ controller }: LibraryScreenProps) {
  const client = controller.client;
  const { url } = useShellRoute();
  const [searchRun, setSearchRun] = useState(0);
  const [projects, setProjects] = useState<ProjectView[]>([]);
  const [accumulation, setAccumulation] = useState<DocumentAccumulation>({ documents: [], exhausted: false });
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [cursor, setCursor] = useState<string | null>(null);
  const [inventory, setInventory] = useState<InventoryState | null>(null);
  const [download, setDownload] = useState<{ document: DocumentView } | null>(null);
  const [importOpen, setImportOpen] = useState(false);
  // Only the newest in-flight filter response is applied; a stale response
  // from a superseded filter or Back navigation never overwrites the list.
  const requestRef = useRef(0);
  const inventoryRequestRef = useRef(0);
  useEffect(() => () => { inventoryRequestRef.current += 1; }, []);
  const filter = useMemo(() => filterFromSearch(url.slice(url.indexOf("?"))), [url]);
  const searchRequest = useMemo(() => searchFromURL(url), [url]);
  // Loaded protected state is scoped to the stable confirmed authority epoch
  // (library + principal + runtime generation). A genuine epoch change —
  // never a routine same-authority recheck — clears the loaded rows,
  // inventory, download panel and pending callbacks; the degraded view under
  // the SAME authority stays as last-confirmed.
  const epoch = `${controller.state.capabilities?.library_id ?? ""}|${controller.state.session?.principal_id ?? ""}|${controller.state.capabilities?.runtime_generation ?? ""}`;
  const epochRef = useRef(epoch);

  const loadPage = useCallback(
    async (nextFilter: LibraryFilter, nextCursor: string | null) => {
      const request = requestRef.current + 1;
      requestRef.current = request;
      setLoading(true);
      setError(null);
      // A fresh filter/page-1 load clears the previous filter's rows and
      // cursor first, so a denial or failure never leaves the old filter's
      // eligible data on screen under the new filter.
      if (nextCursor === null) {
        inventoryRequestRef.current += 1;
        setInventory(null);
        setAccumulation({ documents: [], exhausted: false });
        setCursor(null);
      }
      try {
        const page = await client.listDocuments({
          projectId: nextFilter.projectId || undefined,
          lifecycle: nextFilter.lifecycle === "all" ? undefined : nextFilter.lifecycle,
          cursor: nextCursor ?? undefined,
        });
        if (requestRef.current !== request) return;
        setAccumulation((previous) => accumulateDocuments(nextCursor === null ? { documents: [], exhausted: false } : previous, page));
        setCursor(page.next_cursor);
      } catch (cause) {
        if (requestRef.current !== request) return;
        setError(cause instanceof ApiError ? `오류 ${cause.status} ${cause.code}` : cause instanceof Error ? cause.message : String(cause));
      } finally {
        if (requestRef.current === request) setLoading(false);
      }
    },
    [client],
  );

  // Every project stays selectable: the selector paginates the whole set
  // instead of stopping at the first cursor page.
  useEffect(() => {
    const collected: ProjectView[] = [];
    const drain = async (cursor?: string): Promise<void> => {
      const page: ListPage<ProjectView> = await client.listProjects(cursor);
      collected.push(...page.items);
      if (page.next_cursor !== null) await drain(page.next_cursor);
    };
    void drain()
      .then(() => setProjects(collected))
      .catch(() => setProjects([]));
  }, [client]);

  // Re-run on every actual location/search change (navigate, filter, Back)
  // while the component stays mounted.
  useEffect(() => {
    void loadPage(filter, null);
  }, [filter, loadPage]);

  // A confirmed authority epoch change resets the loaded protected state and
  // invalidates pending responses; a routine same-authority recheck does not.
  useEffect(() => {
    if (epochRef.current === epoch) return;
    epochRef.current = epoch;
    requestRef.current += 1;
    setAccumulation({ documents: [], exhausted: false });
    setCursor(null);
    setInventory(null);
    setDownload(null);
    setImportOpen(false);
    setLoading(false);
    void loadPage(filterFromSearch(location.search), null);
    // epochRef is the guard; loadPage/filter re-derived fresh above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [epoch]);

  const applyFilter = useCallback((next: LibraryFilter) => {
    navigate(searchRequest ? searchURL({ ...searchRequest, project_id: next.projectId || undefined, lifecycle: next.lifecycle }) : filterToSearch(next));
  }, [searchRequest]);

  const loadMore = useCallback(() => {
    if (cursor !== null) void loadPage(filter, cursor);
  }, [cursor, filter, loadPage]);

  // A transfer may finish after the owner changes filters or uses Back.
  // Refresh the current URL context, rather than the render that started it.
  const refreshPublished = useCallback(() => {
    void loadPage(filterFromSearch(location.search), null);
  }, [loadPage]);

  const openInventory = useCallback(
    async (document: DocumentView) => {
      if (!document.latest_version_id) return;
      const request = ++inventoryRequestRef.current;
      setInventory({ document, entries: { kind: "loading" } });
      try {
        const files = await client.versionFiles(document.latest_version_id, undefined);
        if (inventoryRequestRef.current !== request) return;
        setInventory({ document, entries: { kind: "loaded", files: files.items, exhausted: files.next_cursor === null, cursor: files.next_cursor } });
      } catch (cause) {
        if (inventoryRequestRef.current !== request) return;
        setInventory({ document, entries: { kind: "error", message: cause instanceof Error ? cause.message : String(cause) } });
      }
    },
    [client],
  );

  const loadMoreFiles = useCallback(async () => {
    if (!inventory || inventory.entries.kind !== "loaded" || inventory.entries.cursor === null || inventory.entries.loadingMore) return;
    const versionId = inventory.document.latest_version_id;
    if (!versionId) return;
    const request = ++inventoryRequestRef.current;
    const cursor = inventory.entries.cursor;
    const entries = inventory.entries;
    setInventory({ document: inventory.document, entries: { ...entries, loadingMore: true, error: undefined } });
    try {
      const page = await client.versionFiles(versionId, cursor);
      if (inventoryRequestRef.current !== request) return;
      setInventory({
        document: inventory.document,
        entries: { kind: "loaded", files: [...entries.files, ...page.items], exhausted: page.next_cursor === null, cursor: page.next_cursor },
      });
    } catch (cause) {
      if (inventoryRequestRef.current !== request) return;
      const message = cause instanceof Error ? cause.message : String(cause);
      // An access-loss response invalidates protected content immediately;
      // only a transient transport/server failure keeps the confirmed page.
      setInventory({ document: inventory.document, entries: cause instanceof ApiError && [401, 403, 404].includes(cause.status)
        ? { kind: "error", message }
        : { ...entries, loadingMore: false, error: message } });
    }
  }, [client, inventory]);

  const label = countLabel(accumulation);
  const features = controller.state.capabilities?.features;
  // Protected rows stay hidden while the service contract is incompatible.
  const protectedVisible = controller.state.phase !== "incompatible";

  return (
    <>
      <section className="panel">
        <h1>서재</h1>
        <div className="form-row">
          <label>
            프로젝트
            <select aria-label="프로젝트" value={filter.projectId} onChange={(event) => applyFilter({ ...filter, projectId: event.target.value })}>
              <option value="">전체 프로젝트</option>
              {projects.map((project) => (
                <option key={project.project_id} value={project.project_id}>
                  {project.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            수명 주기
            <select aria-label="수명 주기" value={filter.lifecycle} onChange={(event) => applyFilter({ ...filter, lifecycle: event.target.value as LibraryFilter["lifecycle"] })}>
              <option value="active">활성</option>
              <option value="archived">보관</option>
              <option value="all">전체</option>
            </select>
          </label>
          <button type="button" disabled={!featureEnabled(features, "questions")} title={featureEnabled(features, "questions") ? undefined : "AI 기능은 아직 제공되지 않습니다."}>
            AI 질문
          </button>
          <button type="button" className="primary" onClick={() => setImportOpen(true)}>
            가져오기
          </button>
        </div>
        {featureEnabled(features, "search") ? <LibrarySearchForm onSearch={() => setSearchRun(run => run + 1)} request={searchRequest} projectId={filter.projectId} lifecycle={filter.lifecycle} /> : <p>검색은 아직 제공되지 않습니다.</p>}
        {error && !searchRequest ? (
          <p className="hint" role="alert">
            {error}
          </p>
        ) : null}
        {!protectedVisible ? (
          <p className="hint" role="alert">
            서비스 계약이 호환되지 않아 보호된 문서 목록을 표시하지 않습니다.
          </p>
        ) : searchRequest && featureEnabled(features, "search") ? (
          <LibrarySearch key={url + "|" + epoch + "|" + searchRun} client={client} request={searchRequest} />
        ) : (
          <>
            <p className="hint" data-testid="library-count">
          {error ? "문서 수를 확인할 수 없습니다." : label.total ? `총 문서 ${label.loaded}개` : `불러온 문서 ${label.loaded}개${loading ? " (불러오는 중)" : " · 목록이 더 있습니다"}`}
            </p>
            {accumulation.documents.length === 0 && !loading && !error ? (
              <p className="hint" data-testid="library-empty">
                아직 문서가 없습니다. AI 도구의 결과 폴더, ZIP 또는 단일 HTML을 가져오세요.
              </p>
            ) : null}
            <ul className="document-list">
              {accumulation.documents.map((document) => (
                <li key={document.document_id} data-testid="library-document" data-document={document.document_id}>
                  <div>
                    <strong>{document.title}</strong>{featureEnabled(features,"review") && document.review_state && <span> · 내 검토: {{unreviewed:"미검토",reviewing:"검토 중",reviewed:"검토 완료"}[document.review_state]}{document.updated_since_review ? " · 검토한 이후 업데이트됨" : ""}</span>}
                    <span className="hint"> · 갱신 {document.updated_at}</span>
                  </div>
                  <div className="form-row">
                    {document.latest_version_id ? (
                      <a className="action-link" data-testid="library-read" href={`/documents/${document.document_id}/versions/${document.latest_version_id}`}
                         onClick={(event) => { if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return; event.preventDefault(); navigate(`/documents/${document.document_id}/versions/${document.latest_version_id}`); }}>
                        결과 읽기
                      </a>
                    ) : (
                      <>
                        <button type="button" disabled aria-describedby={`read-hint-${document.document_id}`}>결과 읽기</button>
                        <span className="hint" id={`read-hint-${document.document_id}`}>현재 읽을 최신 버전이 없습니다.</span>
                      </>
                    )}
                    <button type="button" onClick={() => navigate(`/documents/${document.document_id}/versions`)}>
                      버전 보기
                    </button>
                    <button type="button" disabled={!document.latest_version_id} onClick={() => void openInventory(document)}>
                      페이지·파일 목록
                    </button>
                    <button type="button" disabled={!document.latest_version_id} onClick={() => setDownload({ document })}>
                      원본 디렉터리 내려받기
                    </button>
                  </div>
                </li>
              ))}
            </ul>
            {cursor !== null ? (
              <div className="form-row">
                <button type="button" onClick={loadMore} disabled={loading}>
                  더 불러오기
                </button>
              </div>
            ) : null}
            {inventory ? (
              <div className="data-readout" data-testid="inventory-list">
                <div>
                  {inventory.document.title} · 페이지/파일 목록 (하나의 디렉터리 = 하나의 문서)
                </div>
                {inventory.entries.kind === "loading" ? <div>불러오는 중…</div> : null}
                {inventory.entries.kind === "error" ? <div>{inventory.entries.message}</div> : null}
                {inventory.entries.kind === "loaded" ? (
                  <>
                    <div>
                      {inventory.entries.files.map((entry) => `${entry.kind === "directory" ? "d" : entry.kind === "symlink" ? "l" : "f"} ${entry.path}${entry.bytes_sha256 ? ` (${entry.byte_size}B)` : ""}`).join("\n")}
                    </div>
                    {inventory.entries.error ? <p role="alert">{inventory.entries.error}</p> : null}
                    {inventory.entries.cursor !== null ? (
                      <button type="button" disabled={inventory.entries.loadingMore} onClick={() => void loadMoreFiles()}>
                        항목 더 보기
                      </button>
                    ) : null}
                  </>
                ) : null}
              </div>
            ) : null}
          </>
        )}
      </section>
      {protectedVisible && importOpen ? <ImportControl controller={controller} projects={projects} documents={accumulation.documents} onFinished={refreshPublished} onClose={() => setImportOpen(false)} /> : null}
      {protectedVisible && download?.document.latest_version_id ? <DownloadPanel key={download.document.latest_version_id} controller={controller} versionId={download.document.latest_version_id} /> : null}
    </>
  );
}
