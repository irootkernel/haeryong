import type { ShellController } from "../../client/controller.ts";
import type { FindingActionResult, FindingView, ManagementEffect, ScanAccepted, ScanDepth, ScanRequest } from "../../client/types.ts";
import { ApiError, DispatchBlockedError, NetworkError, StaleResponseError, UncertainResponseError } from "../../client/types.ts";
import { PendingSourceWriteError, reconcileSourceWrite } from "../../client/source-operations.ts";
import { resolveWriteIntent } from "../../client/source-write-intents.ts";
import type { MaintenanceIntent, MaintenanceRequest } from "../../client/source-write-intents.ts";
import { findingScope, freshnessProposal } from "./maintenance-state.ts";

export type ActionController = Pick<ShellController, "client" | "state" | "readGeneration" | "captureDispatchAuthority" | "requireVerifiedDispatch" | "isCurrentReadAuthority" | "isCurrentDispatchAuthority" | "sourceWriteIntents" | "subscribe">;
export function findingChoices(f: FindingView): ("approve" | "reject")[] {
  if (!["related_explanation", "topic_assignment", "freshness_assessment", "backlink_index_repair", "new_document_proposal", "new_version_proposal"].includes(f.kind) || !f.etag) return [];
  if (f.effect?.availability === "unavailable") return [];
  if (f.kind === "backlink_index_repair" && f.reason === "approved_repair_pending") return [];
  const content = f.kind === "new_document_proposal" || f.kind === "new_version_proposal";
  if (f.status === "proposed") return content ? ["reject"] : ["approve", "reject"];
  return f.status === "blocked" && !content ? ["approve"] : [];
}
export function effectChoices(effect: ManagementEffect): ("undo" | "pin" | "unpin" | "reject" | "reaccept")[] {
  if (effect.availability !== "available" || !effect.etag || !["related_explanation", "topic_assignment"].includes(effect.kind) || effect.decision === "undone") return [];
  if (effect.decision === "rejected") return effect.kind === "topic_assignment" ? ["reaccept"] : [];
  const choices: ("undo" | "pin" | "unpin" | "reject" | "reaccept")[] = effect.pinned ? [] : ["undo"];
  if (effect.kind === "topic_assignment") choices.push(effect.pinned ? "unpin" : "pin", "reject");
  return choices;
}
export function reassessmentRequest(f: FindingView, input: { version: string; depth: ScanDepth; sourceMode: "pinned" | "current"; targetSet: string; sources: ScanRequest["sources"] }): ScanRequest {
  const proposal = freshnessProposal(f);
  const scope = f.kind === "freshness_assessment" ? proposal && findingScope(proposal.scope) :
    f.region_id ? findingScope({ kind: "region", page_path: f.page_path, region_id: f.region_id }) :
    f.page_path ? findingScope({ kind: "page", page_path: f.page_path, region_id: null }) : findingScope({ kind: "version", page_path: null, region_id: null });
  if (!scope || !f.document_id || !input.version.trim() || !f.project_id || !Number.isInteger(f.revision) || f.revision <= 0) throw new Error("정확한 대상과 범위를 확인할 수 없습니다.");
  return { scope: "selected_projects", project_ids: [f.project_id], document_id: f.document_id, version_id: input.version.trim(),
    ...(scope.page_path ? { page_path: scope.page_path } : {}), ...(scope.region_id ? { region_id: scope.region_id } : {}),
    finding_id: f.finding_id, expected_finding_revision: f.revision, depth: input.depth, source_mode: input.sourceMode,
    ...(input.targetSet.trim() ? { target_set_id: input.targetSet.trim() } : {}), ...(input.sources ? { sources: input.sources } : {}) };
}
export function actionError(error: unknown): string {
  if (!(error instanceof ApiError)) return error instanceof Error ? error.message : String(error);
  const reason = typeof error.details.reason === "string" ? error.details.reason : "";
  const reasons: Record<string, string> = {
    owner_classification_changed: "현재 소유자 분류가 변경되었습니다. 현재 주제·고정 상태를 확인하고 정확한 범위로 재평가하세요.",
    owner_decision_changed: "소유자의 결정이 변경되었습니다. 원래 요청은 유지됩니다. 현재 결정을 확인하고 정확한 범위로 재평가하세요.",
    owner_rejection_unchanged: "거절한 제안과 근거가 같습니다. 기존 거절을 확인하고 근거와 범위를 명시해 재평가하세요.",
    owner_effect_conflict: "소유자가 고정하거나 거절한 관리 정보와 충돌합니다. 현재 고정·거절 상태를 확인한 뒤 별도로 결정하세요.",
    retained_evidence_unavailable: "보관된 근거를 확인할 수 없습니다. 소스 권한과 정확한 범위를 확인하고 새 평가를 요청하세요.",
    scan_cancelled: "원래 스캔이 취소되었습니다. 현재 대상을 확인하고 정확한 범위로 새 재평가를 요청하세요.",
    policy_disabled: "현재 정책에서 허용되지 않습니다. 관리 정책을 확인하세요.",
    owner_basis_unverified: "원래 소유자 결정 근거를 확인할 수 없습니다. 정확한 범위로 재평가하세요.",
    undo_requires_reassessment: "실행 취소 선택이 유지됩니다. 재평가 후 새 제안을 별도로 승인하세요.",
    undone_requires_reassessment_approve: "실행 취소한 주제는 직접 변경할 수 없습니다. 재평가 후 새 제안을 별도로 승인하세요.",
    source_authority_changed: "소스 권한이 변경되었습니다. 현재 소스 설정과 평가 범위를 확인하세요.",
    document_updated: "문서가 변경되었습니다. 원래 버전과 새 평가 대상을 명시적으로 검토하세요.",
    effect_not_undoable: "현재 관리 결정은 실행 취소할 수 없습니다. 최신 상태와 고정 여부를 확인하세요.",
    historical_finding: "과거 발견 항목은 다시 적용하지 않습니다. 새 평가를 요청하세요.",
    finding_draft_owned_by_task06: "콘텐츠 제안의 초안 생성은 아직 제공하지 않습니다.",
  };
  const guidance = error.status === 412 ? "상태가 변경되었습니다. 입력을 유지했습니다. 현재 상태를 불러온 뒤 검토하세요." : reasons[reason] ?? (
    error.status === 409 ? "요청이 충돌했습니다. 원래 요청을 유지했습니다. 현재 상태와 비교한 뒤 새 결정을 선택하세요." :
    error.status === 428 ? "개별 리비전을 먼저 확인하세요." :
    "요청을 적용하지 못했습니다.");
  return `${guidance} (${error.status} ${error.code}${reason ? ` · ${reason}` : ""})`;
}

/** Intent ownership stays in the existing controller's library/principal slots. */
export class FindingActionState {
  readonly controller: ActionController;
  readonly slot: string;
  busy = false;
  denied = false;
  error: string | null = null;
  result: FindingActionResult | ScanAccepted | null = null;
  #epoch = 0;
  #disposed = false;
  #off: (() => void) | null = null;
  #listeners = new Set<() => void>();
  #authority: ReturnType<ActionController["captureDispatchAuthority"]>;
  constructor(controller: ActionController, slot: string) { this.controller = controller; this.slot = slot; this.#authority = controller.captureDispatchAuthority(); }
  get intent(): MaintenanceIntent | undefined {
    if (this.denied || this.controller.state.phase !== "verified") return undefined;
    try { return this.controller.sourceWriteIntents().maintenance.get(this.slot); } catch { return undefined; }
  }
  subscribe(fn: () => void) { this.#listeners.add(fn); return () => { this.#listeners.delete(fn); }; }
  #emit() { for (const fn of this.#listeners) fn(); }
  connect() {
    if (this.#off) return;
    this.#disposed = false;
    this.#off = this.controller.subscribe(() => {
      if (!this.controller.isCurrentDispatchAuthority(this.#authority)) {
        this.#epoch++; this.busy = false; this.error = null; this.result = null;
        this.#authority = this.controller.captureDispatchAuthority(); this.denied = !this.#authority; this.#emit();
      }
    });
  }
  dispose() { this.#disposed = true; this.#epoch++; this.#off?.(); this.#off = null; this.#listeners.clear(); }
  acknowledge() {
    const intent = this.intent;
    if (this.busy || intent?.outcome.state === "pending") return;
    this.controller.requireVerifiedDispatch(this.controller.captureDispatchAuthority());
    this.controller.sourceWriteIntents().maintenance.delete(this.slot); this.error = null; this.result = null; this.#emit();
  }
  async start(request: MaintenanceRequest, operationId: string) {
    if (this.busy || this.intent) return;
    const authority = this.controller.captureDispatchAuthority(); this.controller.requireVerifiedDispatch(authority);
    const features = this.controller.state.capabilities?.features;
    if (request.kind === "scan" ? features?.scans !== true : features?.management_overlays !== true) throw new DispatchBlockedError();
    const intent: MaintenanceIntent = { request: structuredClone(request), operationId, outcome: { state: "pending" } };
    this.controller.sourceWriteIntents().maintenance.set(this.slot, intent);
    return this.#run(intent, false);
  }
  async recover() {
    const intent = this.intent;
    if (!intent || this.busy) return;
    return this.#run(intent, true);
  }
  async #run(intent: MaintenanceIntent, recover: boolean) {
    const epoch = ++this.#epoch, authority = this.controller.captureDispatchAuthority(), read = this.controller.readGeneration;
    this.controller.requireVerifiedDispatch(authority);
    const current = () => !this.#disposed && epoch === this.#epoch && this.controller.isCurrentReadAuthority(authority, read);
    this.busy = true; this.error = null; this.result = null; this.#emit();
    const c = this.controller.client, r = intent.request;
    try {
      const value = recover ? await reconcileSourceWrite<FindingActionResult | ScanAccepted>(intent.operationId, id => {
        this.controller.requireVerifiedDispatch(authority); return c.operation(id);
      }, true) : await resolveWriteIntent(intent, () => {
        this.controller.requireVerifiedDispatch(authority);
        switch (r.kind) {
          case "finding": if (c.findingDecision) return c.findingDecision(r.id, r.decision, r.etag, intent.operationId); break;
          case "topic": if (c.topicAssignmentSet) return c.topicAssignmentSet(r.id, r.body, r.etag, intent.operationId); break;
          case "undo": if (c.overlayUndo) return c.overlayUndo(r.id, r.etag, intent.operationId); break;
          case "scan": if (c.scanStart) return c.scanStart(r.request, intent.operationId); break;
        }
        throw new ApiError(503, "FEATURE_NOT_READY", false);
      }, error => error instanceof NetworkError || error instanceof UncertainResponseError || error instanceof ApiError && error.code === "OPERATION_RECONCILIATION_REQUIRED");
      if (!current()) {
        // A settled transport result is not a current-authority presentation.
        intent.outcome = { state: "pending" };
        return;
      }
      if (value.operation_id !== intent.operationId) throw new PendingSourceWriteError(intent.operationId);
      intent.outcome = { state: "completed", result: value }; this.result = value;
    } catch (error) {
      if (!current()) return;
      const receiptDenial = error instanceof PendingSourceWriteError && error.cause instanceof ApiError && [401, 403, 404].includes(error.cause.status);
      if (receiptDenial || error instanceof ApiError && [401, 403, 404].includes(error.status)) {
        this.denied = true; this.result = null; this.error = "현재 권한으로 이 항목을 사용할 수 없어 보호된 요청·결과 표시를 지웠습니다.";
        if (receiptDenial) intent.outcome = { state: "pending" };
        else this.controller.sourceWriteIntents().maintenance.delete(this.slot);
      } else {
        const pending = error instanceof PendingSourceWriteError || error instanceof NetworkError || error instanceof UncertainResponseError || error instanceof StaleResponseError;
        intent.outcome = pending ? { state: "pending" } : { state: "failed", error };
        this.error = pending ? "결과 미확인 · 같은 요청 영수증을 확인하세요. 자동 재전송하지 않습니다." : actionError(error);
      }
    } finally {
      if (!this.#disposed && epoch === this.#epoch) {
        this.busy = false;
        this.#emit();
      }
    }
  }
}
