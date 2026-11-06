import { useEffect, useRef, useState } from "react";
import type { ShellController } from "../../client/controller.ts";
import type { ReviewAction, ReviewEvent, ReviewView } from "../../client/types.ts";
import { StaleResponseError } from "../../client/types.ts";
import { ReviewOperations, reviewJournal, reviewLabel } from "./review-operations.ts";
import { useReviewInvalidation } from "./review-refresh.ts";

const STATE_LABELS={unreviewed:"미검토",reviewing:"검토 중",reviewed:"검토 완료"};
const localTime=(value:string)=>new Date(value).toLocaleString("ko-KR");
const ACTION_LABELS = { start: "검토 시작", complete: "검토 완료로 표시", reopen: "검토 다시 열기", clear: "검토 초기화" };
const errorText = (error: unknown) => error instanceof Error ? error.message : String(error);

export function ReviewControl({ controller, versionId, owner, versionLabel }: { controller: ShellController; versionId: string; owner: string; versionLabel?:string }) {
  const [view, setView] = useState<ReviewView | null>(null);
  const [issue, setIssue] = useState(""), [busy, setBusy] = useState(false), [revision, refresh] = useState(0);
  const [events, setEvents] = useState<ReviewEvent[]>([]), [historyOpen, setHistoryOpen] = useState(false);
  const [cursor, setCursor] = useState<string | null>(null), [historyBusy, setHistoryBusy] = useState(false);
  const operations = useRef<ReviewOperations | null>(null), lifetime = useRef(0);
  const viewRead = useRef(0), historyRead = useRef(0);
  const enabled = controller.state.capabilities?.features.review === true;
  useReviewInvalidation(
    controller,
    enabled,
    async (ev) => {
      const captured = controller.captureDispatchAuthority();
      const readGen = ev?.readGeneration ?? controller.readGeneration;
      const life = lifetime.current, viewRun = ++viewRead.current;
      const currentQuery = (current: boolean) => {
        if (lifetime.current !== life || !controller.isCurrentReadAuthority(captured, readGen)) return false;
        if (!current && ev?.resync) throw new StaleResponseError();
        return current;
      };
      try {
        const value = await controller.passiveClient.reviewGet(versionId);
        if (!currentQuery(viewRead.current === viewRun)) return;
        if (value.version_id !== versionId) throw Error("Review 응답 버전이 일치하지 않습니다.");
        setView(value);
        if (historyOpen) {
          const historyRun = ++historyRead.current;
          const page = await controller.passiveClient.reviewHistory(versionId);
          if (!currentQuery(historyRead.current === historyRun)) return;
          setEvents(page.items);
          setCursor(page.next_cursor);
        }
      } catch (error) {
        if (!(error instanceof StaleResponseError) && viewRead.current === viewRun && lifetime.current === life && controller.isCurrentReadAuthority(captured, readGen)) {
          setIssue(errorText(error));
        }
        if (ev?.resync) throw error;
      }
    },
    () => operations.current?.busy === true,
  );

  const guardFor = () => {
    const authority = controller.captureDispatchAuthority(), generation = lifetime.current;
    return () => {
      controller.requireVerifiedDispatch(authority);
      if (lifetime.current !== generation) throw Error("Review 화면이 변경되었습니다.");
    };
  };
  useEffect(() => {
    const generation = ++lifetime.current;
    setView(null); setEvents([]); setHistoryOpen(false); setCursor(null); setIssue(""); setBusy(false); setHistoryBusy(false);
    if (enabled) {
      try {
        const namespace = JSON.stringify([controller.state.capabilities?.library_id, controller.state.session?.principal_id, versionId]);
        operations.current = new ReviewOperations(controller.client, versionId, reviewJournal(localStorage, namespace), controller.state.capabilities?.runtime_generation ?? "");
      } catch (error) { setIssue(errorText(error)); }
    }
    return () => {
      if (lifetime.current === generation) lifetime.current++;
      if (operations.current) operations.current.disposed = true;
      operations.current = null;
    };
  }, [controller, versionId, owner, enabled]);

  // Routine invalidations refetch in place: they must not remove a focused
  // action button, abandon an in-flight request or collapse inspected history.
  useEffect(() => {
    let active = true;
    const authority = controller.captureDispatchAuthority();
    const readGen = controller.readGeneration;
    const life = lifetime.current, viewRun = ++viewRead.current;
    if (enabled) void controller.client.reviewGet(versionId).then(value => {
      if (!active || viewRead.current !== viewRun || lifetime.current !== life || !controller.isCurrentReadAuthority(authority, readGen)) return;
      if (value.version_id !== versionId) throw Error("Review 응답 버전이 일치하지 않습니다.");
      setView(value);
    }).catch(error => {
      if (active && viewRead.current === viewRun && lifetime.current === life && controller.isCurrentReadAuthority(authority, readGen)) {
        setIssue(errorText(error));
      }
    });
    return () => { active = false; };
  }, [controller, versionId, owner, enabled, revision]);
  useEffect(() => {
    const focus = () => {
      if (!enabled || operations.current?.busy === true) return;
      const authority = controller.captureDispatchAuthority();
      const readGen = controller.readGeneration;
      const life = lifetime.current, viewRun = ++viewRead.current;
      void controller.passiveClient.reviewGet(versionId).then(value => {
        if (viewRead.current !== viewRun || lifetime.current !== life || !controller.isCurrentReadAuthority(authority, readGen)) return;
        if (value.version_id !== versionId) throw Error("Review 응답 버전이 일치하지 않습니다.");
        setView(value);
      }).catch(error => {
        if (viewRead.current === viewRun && lifetime.current === life && controller.isCurrentReadAuthority(authority, readGen)) {
          setIssue(errorText(error));
        }
      });
    };
    window.addEventListener("focus", focus);
    return () => window.removeEventListener("focus", focus);
  }, [controller, versionId, enabled]);

  const act = async (action?: ReviewAction) => {
    const op = operations.current;
    if (!op || (!view && action)) return;
    const guard = guardFor(), generation = lifetime.current;
    controller.bumpReadGeneration();
    setBusy(true); setIssue("");
    try {
      const value = action ? await op.submit(action, view!, guard) : await op.retry(guard);
      guard();
      controller.bumpReadGeneration();
      setView(value); setEvents([]); setHistoryOpen(false);
      window.dispatchEvent(new Event("haeryong-review-changed"));
    } catch (error) {
      if (lifetime.current === generation) {
        setIssue(errorText(error));
        try { const current = await controller.client.reviewGet(versionId); guard(); controller.bumpReadGeneration(); setView(current); }
        catch { try { guard(); if (!op.pending) setView(null); } catch { /* Previous authority. */ } }
      }
    } finally { if (lifetime.current === generation) setBusy(false); }
  };
  const applyLocal = async () => {
    const op = operations.current;
    if (!op) return;
    const guard = guardFor(), generation = lifetime.current;
    controller.bumpReadGeneration();
    setBusy(true); setIssue("");
    try {
      const freshBase = await controller.client.reviewGet(versionId);
      guard();
      const value = await op.applyRejected(freshBase, guard);
      guard();
      controller.bumpReadGeneration();
      setView(value); setEvents([]); setHistoryOpen(false);
      window.dispatchEvent(new Event("haeryong-review-changed"));
    } catch (error) {
      if (lifetime.current === generation) {
        setIssue(errorText(error));
        try { const current = await controller.client.reviewGet(versionId); guard(); controller.bumpReadGeneration(); setView(current); }
        catch { try { guard(); if (!op.pending) setView(null); } catch { /* Previous authority. */ } }
      }
    } finally { if (lifetime.current === generation) setBusy(false); }
  };
  const history = async (more = false) => {
    const guard = guardFor(), readGen = controller.readGeneration, historyRun = ++historyRead.current; setHistoryBusy(true); setHistoryOpen(true);
    try {
      guard();
      const page = await controller.client.reviewHistory(versionId, more ? cursor ?? undefined : undefined);
      guard(); if (controller.readGeneration !== readGen || historyRead.current !== historyRun) return; setEvents(old => more ? [...old, ...page.items] : page.items); setCursor(page.next_cursor); setHistoryOpen(true);
    } catch (error) { try { guard(); if (controller.readGeneration === readGen && historyRead.current === historyRun) setIssue(errorText(error)); } catch { /* Previous authority. */ } }
    finally { try { guard(); setHistoryBusy(false); } catch { /* Previous authority. */ } }
  };
  if (!enabled) return <p>개인 Review: 아직 제공되지 않습니다.</p>;
  const current = view?.version_id === versionId ? view : null;
  const pending = operations.current?.pending, generationChanged = operations.current?.generationChanged;
  const actions: ReviewAction[] = !current ? [] : current.value.state === "unreviewed" ? ["start", "complete"] : current.value.state === "reviewing" ? ["complete", "clear"] : ["reopen", "clear"];
  return <section aria-label="내 검토" className="review-control" data-version-id={versionId}>
    <h2>내 검토</h2>
    <p id="review-state">{current ? reviewLabel(current) : "검토 상태 확인 중…"}{versionLabel?` · ${versionLabel}`:""}{current?.value.reviewed_at ? ` · ${localTime(current.value.reviewed_at)}` : ""}</p>
    <p className="hint">이 버전의 전체 결과 디렉터리에 대한 개인 검토입니다. 페이지 열기나 AI 평가는 검토를 완료하지 않습니다.</p>
    <div className="form-row">
      {actions.map(action => <button key={action} id={`review-${action}`} style={{ minHeight: 44, minWidth: 44 }} disabled={busy || !!pending || !operations.current} onClick={() => void act(action)}>{ACTION_LABELS[action]}</button>)}
      <button disabled={busy} onClick={() => refresh(n => n + 1)}>검토 상태 새로고침</button>
      <button disabled={!current || historyBusy} onClick={() => void history()}>검토 이력</button>
    </div>
    {pending && <p role="status">{pending.rejected ? (generationChanged ? "서비스 세대가 변경되었습니다. 이전 요청을 자동 재전송하지 않습니다. 현재 상태와 이력을 확인하세요." : "요청이 거절되었습니다. 현재 상태를 확인하고 동작을 다시 선택하세요.") : "저장 결과가 확인되지 않았습니다. 원래 요청으로 다시 확인하세요."}</p>}
    {pending?.rejected && <p className="hint" data-review-conflict="true">내 요청: {STATE_LABELS[pending.body.state]} · 서버 상태: {current ? reviewLabel(current) : "확인 중"}</p>}
    {pending && !pending.rejected && <button disabled={busy} onClick={() => void act()}>원래 Review 요청으로 다시 확인</button>}
    {pending?.rejected && <div className="form-row">
      <button disabled={busy} onClick={() => {
        const guard = guardFor();
        void operations.current?.discard(guard).then(() => { guard(); setIssue(""); refresh(n => n + 1); }).catch(error => setIssue(errorText(error)));
      }}>{generationChanged ? "현재 상태 확인 후 이전 요청 종료" : "서버 상태 유지"}</button>
      <button disabled={busy || !current} onClick={() => void applyLocal()}>내 검토 상태 다시 적용</button>
    </div>}
    {issue && <p role="alert">{issue}</p>}
    {historyOpen && <div>
      <ul>{events.map(event => <li key={event.event_id}>{localTime(event.created_at)} · {STATE_LABELS[event.old_state]} → {STATE_LABELS[event.new_state]} · {ACTION_LABELS[event.action]}</li>)}</ul>
      {events.length === 0 && <p>검토 이력이 없습니다.</p>}
      {cursor && <button disabled={historyBusy} onClick={() => void history(true)}>이력 더 불러오기</button>}
    </div>}
  </section>;
}
