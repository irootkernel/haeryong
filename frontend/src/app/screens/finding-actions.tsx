import { useEffect, useRef, useState } from "react";
import type { ShellController } from "../../client/controller.ts";
import type { FindingView, ManagementEffect, ScanDepth, ScanRequest } from "../../client/types.ts";
import { FindingActionState, effectChoices, findingChoices, reassessmentRequest } from "./finding-actions-state.ts";
import { freshnessProposal, scopeLabel, findingScope } from "./maintenance-state.ts";
import { navigate } from "../router.tsx";

function useActions(controller: ShellController, slot: string, onChanged: () => void | Promise<void>, onDenied?: () => void) {
  const [model] = useState(() => new FindingActionState(controller, slot));
  const [, redraw] = useState(0);
  const changed = useRef(onChanged); changed.current = onChanged;
  const accessLost = useRef(onDenied); accessLost.current = onDenied;
  const seen = useRef<unknown>(null);
  const denied = useRef(false);
  useEffect(() => {
    const off = model.subscribe(() => {
      redraw(n => n + 1);
      if (model.result && seen.current !== model.result) {
        seen.current = model.result;
        void Promise.resolve(changed.current()).catch(() => {});
      }
      if (model.denied && !denied.current) {
        denied.current = true;
        if (accessLost.current) accessLost.current();
        else void Promise.resolve(changed.current()).catch(() => {});
      }
    });
    model.connect();
    return () => { off(); model.dispose(); };
  }, [model]);
  return model;
}
function Outcome({ model, showRequest = true }: { model: FindingActionState; showRequest?: boolean }) {
  const intent = model.intent;
  return <>
    {model.error && <p role="alert">{model.error}</p>}
    {showRequest && intent && <details data-maintenance-intent><summary>보존한 원래 요청</summary><pre className="management-detail">{JSON.stringify({ operation_id: intent.operationId, request: intent.request }, null, 2)}</pre></details>}
    {intent?.outcome.state === "pending" && <p role="status">결과 미확인 · 자동 재전송하지 않습니다.<button type="button" data-maintenance-action="recover" disabled={model.busy} onClick={() => { void model.recover().catch(() => {}); }}>같은 요청 영수증 확인</button></p>}
    {model.result && <p role="status" data-maintenance-outcome>{"state" in model.result ? model.result.state === "repair_pending" ? "구조 복구 요청 수락 · 실제 복구 완료는 작업에서 확인하세요." : "결정 영수증 확인됨 · 현재 상태는 별도 새로고침으로 확인합니다." : "재평가 요청 수락 · 원래 항목은 자동 승인되지 않습니다."}
      {model.result.job_id && <button type="button" onClick={() => navigate(`/maintenance/jobs/${encodeURIComponent(model.result!.job_id!)}`)}>작업 열기</button>}</p>}
    {intent && intent.outcome.state !== "pending" && <button type="button" data-maintenance-action="acknowledge" disabled={model.busy} onClick={() => model.acknowledge()}>결과 확인 · 새 결정 준비</button>}
  </>;
}

/** A committed effect can disappear from the visible list before its lost reply
 * is recovered. Show only the operation ID until current receipt authority is
 * checked, without replaying the cached target/request on another selection. */
export function MaintenanceRecovery({ controller, onChanged }: { controller: ShellController; onChanged(): void | Promise<void> }) {
  if (controller.state.phase !== "verified") return null;
  const intents = controller.sourceWriteIntents().maintenance;
  if (intents.size === 0) return null;
  return <section aria-label="이전 관리 요청 영수증" data-maintenance-recovery>
    <h3>이전 관리 요청 결과</h3>
    {[...intents].map(([slot, intent]) => <RecoveredRequest key={JSON.stringify([slot, intent.operationId, controller.captureDispatchAuthority()])} controller={controller} slot={slot} operationId={intent.operationId} onChanged={onChanged} />)}
  </section>;
}
function RecoveredRequest({ controller, slot, operationId, onChanged }: { controller: ShellController; slot: string; operationId: string; onChanged(): void | Promise<void> }) {
  const model = useActions(controller, slot, onChanged);
  if (model.denied) return <p role="alert">이전 요청의 현재 접근 권한을 확인할 수 없습니다.</p>;
  return <div><p>작업 {operationId}</p>
    <button type="button" disabled={model.busy} data-maintenance-action="recover-retained" onClick={() => { void model.recover().catch(() => {}); }}>이전 요청 영수증 확인</button>
    <Outcome model={model} showRequest={false} />
  </div>;
}

export function EffectActions({ controller, effect, onChanged, onDenied }: { controller: ShellController; effect: ManagementEffect; onChanged(): void | Promise<void>; onDenied?(): void }) {
  const model = useActions(controller, `effect:${effect.overlay_id}`, onChanged, onDenied);
  const enabled = controller.state.phase === "verified" && controller.state.capabilities?.features.management_overlays === true;
  if (!enabled || model.denied || effect.availability !== "available") return model.denied ? <p role="alert">현재 권한으로 관리 작업을 사용할 수 없습니다.</p> : null;
  const choices = effectChoices(effect), disabled = model.busy || Boolean(model.intent);
  const labels = { undo: "실행 취소", pin: "주제 고정", unpin: "주제 고정 해제", reject: effect.pinned ? "고정 해제하고 주제 거절" : "주제 거절", reaccept: "주제 다시 수락" };
  return <section aria-label={`관리 결정 ${effect.overlay_id}`} data-effect-actions={effect.overlay_id}>
    <p>현재 관리 결정 {effect.decision} · 리비전 {effect.revision}{effect.pinned && " · 고정됨"}</p>
    {effect.decision === "undone" && <p>실행 취소한 선택이 유지됩니다. 정비의 원래 발견 항목에서 명시적으로 재평가하고 새 제안을 별도로 승인하세요.</p>}
    <div className="form-row">{choices.map(action => <button type="button" key={action} data-maintenance-action={action} disabled={disabled || (action === "undo" ? !controller.client.overlayUndo : !controller.client.topicAssignmentSet)} onClick={() => void model.start(action === "undo" ? { kind: "undo", id: effect.overlay_id, etag: effect.etag } : {
      kind: "topic", id: effect.overlay_id, etag: effect.etag, body: { pinned: action === "pin", decision: action === "reject" ? "rejected" : "accepted" },
    }, crypto.randomUUID()).catch(() => {})}>{labels[action]}</button>)}</div>
    <Outcome model={model} />
  </section>;
}

export function FindingActions({ controller, finding, onChanged, onDenied }: { controller: ShellController; finding: FindingView; onChanged(): void | Promise<void>; onDenied?(): void }) {
  const model = useActions(controller, `finding:${finding.finding_id}`, onChanged, onDenied);
  const [dialog, setDialog] = useState(false);
  const authority = controller.captureDispatchAuthority();
  const enabled = controller.state.phase === "verified" && controller.state.capabilities?.features.management_overlays === true;
  useEffect(() => { if (!enabled || model.denied) setDialog(false); }, [enabled, model.denied]);
  useEffect(() => { setDialog(false); }, [authority?.nonce, authority?.libraryId, authority?.runtimeGeneration]);
  if (!enabled || model.denied) return <p role="status">현재 권한/연결에서 결정 작업을 사용할 수 없습니다.</p>;
  const choices = findingChoices(finding), disabled = model.busy || Boolean(model.intent);
  const unavailable = finding.effect?.availability === "unavailable";
  return <section aria-label={`발견 항목 결정 ${finding.finding_id}`} data-finding-actions={finding.finding_id}>
    {finding.owner_decision && <p data-owner-decision>현재 소유자 선택 {finding.owner_decision.action} · 결정 리비전 {finding.owner_decision.revision}{finding.owner_decision.action === "undo" && " · 이 선택을 바꾸려면 새 제안을 별도로 승인해야 합니다."}</p>}
    {finding.status !== "applied" && finding.applied_ref !== null && <p>보관된 applied_ref는 과거 기록이며 현재 적용을 뜻하지 않습니다.</p>}
    {finding.reason && <p>현재 사유: {finding.reason}</p>}
    <div className="form-row">{choices.map(decision => <button type="button" key={decision} data-maintenance-action={decision} disabled={disabled || !controller.client.findingDecision} onClick={() => void model.start({ kind: "finding", id: finding.finding_id, decision, etag: finding.etag }, crypto.randomUUID()).catch(() => {})}>{decision === "reject" ? "제안 거절" : finding.status === "blocked" ? "현재 조건 재검토 후 승인" : "제안 승인"}</button>)}
      {!unavailable && <button type="button" data-maintenance-action="reassess" disabled={disabled || controller.state.capabilities?.features.scans !== true || !controller.client.scanStart} onClick={() => { controller.requireVerifiedDispatch(controller.captureDispatchAuthority()); setDialog(true); }}>정확한 범위로 재평가</button>}
      <button type="button" data-maintenance-action="reload" disabled={model.busy} onClick={() => void onChanged()}>현재 상태 불러오기 · 입력 유지</button>
    </div>
    {(finding.kind === "new_version_proposal" || finding.kind === "new_document_proposal") && <p>콘텐츠 제안은 아직 초안이 아닙니다. 초안 생성·게시는 후속 기능입니다.</p>}
    <Outcome model={model} />
    {finding.effect && <EffectActions key={finding.effect.overlay_id} controller={controller} effect={finding.effect} onChanged={onChanged} onDenied={onDenied} />}
    {dialog && <ReassessmentDialog key={JSON.stringify([finding.finding_id, finding.revision, authority])} controller={controller} finding={finding} model={model} onClose={() => setDialog(false)} />}
  </section>;
}

function ReassessmentDialog({ controller, finding, model, onClose }: { controller: ShellController; finding: FindingView; model: FindingActionState; onClose(): void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const proposal = freshnessProposal(finding);
  const [version, setVersion] = useState(proposal?.version_id ?? finding.version_id ?? "");
  const [depth, setDepth] = useState<"" | ScanDepth>("");
  const [sourceMode, setSourceMode] = useState<"" | "pinned" | "current">("");
  const [targetSet, setTargetSet] = useState(proposal?.target_set_id ?? finding.target_set_id ?? "");
  const [sources, setSources] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const scope = proposal ? findingScope(proposal.scope) : findingScope({ kind: finding.region_id ? "region" : finding.page_path ? "page" : "version", page_path: finding.page_path ?? null, region_id: finding.region_id ?? null });
  useEffect(() => { dialog.current?.showModal(); }, []);
  const disabled = model.busy || Boolean(model.intent) || controller.state.phase !== "verified";
  return <dialog ref={dialog} aria-label="정확한 범위 재평가" data-finding-reassessment={finding.finding_id} onCancel={onClose} onClose={onClose}>
    <h2>정확한 범위 재평가</h2>
    <p>새 소스 읽기와 평가 작업을 시작할 수 있습니다. 원래 결과를 적용하거나 게시하지 않으며, 새 제안의 승인은 별도입니다.</p>
    <p>{finding.finding_id} · 리비전 {finding.revision} · 문서 {finding.document_id ?? "확인 불가"} · {scopeLabel(scope)}</p>
    <form onSubmit={event => { event.preventDefault(); try {
      controller.requireVerifiedDispatch(controller.captureDispatchAuthority());
      if (!confirmed || !depth || !sourceMode) throw new Error("검사 깊이·소스 모드·요청 범위를 명시적으로 확인하세요.");
      const selectors: ScanRequest["sources"] = sources.trim() ? sources.split(",").map(id => ({ source_id: id.trim(), mode: sourceMode })) : undefined;
      if (selectors?.some(source => !source.source_id)) throw new Error("선택한 소스 ID를 확인하세요.");
      const request = reassessmentRequest(finding, { version, depth, sourceMode, targetSet, sources: selectors });
      setError(null); void model.start({ kind: "scan", request }, crypto.randomUUID()).catch(() => {});
    } catch (e) { setError(e instanceof Error ? e.message : String(e)); } }}>
      <fieldset disabled={disabled}>
        <legend>명시적으로 선택한 평가 요청</legend>
        <label>정확한 버전 ID<input value={version} onChange={e => { setVersion(e.target.value); setConfirmed(false); }} /></label>
        <p>같은 문서의 후속 버전은 직접 선택할 때만 사용합니다. 페이지·영역은 자동 매핑하지 않습니다.</p>
        <label>검사 깊이<select value={depth} onChange={e => { setDepth(e.target.value as ScanDepth); setConfirmed(false); }}><option value="">선택하세요</option><option value="change_focused">변경 중심</option><option value="full_scope">전체 범위</option><option value="deep_semantic">깊은 의미 평가</option></select></label>
        <label>소스 모드<select value={sourceMode} onChange={e => { setSourceMode(e.target.value as "pinned" | "current"); setConfirmed(false); }}><option value="">선택하세요</option><option value="pinned">고정 소스</option><option value="current">현재 소스</option></select></label>
        <label>정확한 대상 집합 ID (선택)<input value={targetSet} onChange={e => { setTargetSet(e.target.value); setConfirmed(false); }} /></label>
        <label>소스 ID (선택, 쉼표로 구분)<input value={sources} onChange={e => { setSources(e.target.value); setConfirmed(false); }} /></label>
        <label><input type="checkbox" checked={confirmed} onChange={e => setConfirmed(e.target.checked)} />위 대상·범위·깊이·소스 선택으로 새 평가를 요청합니다</label>
        <button type="submit" data-maintenance-action="confirm-reassessment" disabled={!confirmed || !depth || !sourceMode || !scope || !version}>확인한 범위로 재평가 시작</button>
      </fieldset>
      {error && <p role="alert">{error}</p>}
      <Outcome model={model} />
      <button type="button" onClick={onClose}>닫기</button>
    </form>
  </dialog>;
}
