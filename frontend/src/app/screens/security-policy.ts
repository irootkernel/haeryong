import type { DispatchAuthority, PendingPolicyRecovery, ShellController, ShellState } from "../../client/controller.ts";
import type {
  ConfigurationCommitView,
  ConfigurationPlanView,
  ConfigurationView,
  DeviceSession,
  HaeryongClient,
  PairingView,
  SecurityConfiguration,
} from "../../client/types.ts";
import { ApiError, NetworkError, StaleResponseError, UncertainResponseError } from "../../client/types.ts";
import { PendingSourceWriteError, reconcileSourceWrite } from "../../client/source-operations.ts";
import { resolveWriteIntent } from "../../client/source-write-intents.ts";
import type { ConfigurationIntent, SourceWriteIntents } from "../../client/source-write-intents.ts";

export interface SecurityPolicyDraft {
  remember_device_default: boolean;
  session_idle_seconds: number | null;
  session_absolute_seconds: number | null;
}

export interface AffectedDeviceExpiry {
  session_id: string;
  device_label: string;
  isCurrentDevice: boolean;
  reasons: Array<"idle" | "absolute">;
}

export interface ImmediateExpiryPreview {
  affectedDevices: AffectedDeviceExpiry[];
  currentDeviceAffected: boolean;
  activeCount: number;
  summary: string;
  estimateNote: string;
  recoveryNote: string;
}

export const SECURITY_POLICY_COPY = {
  estimateNote: "미리보기 시점 기준 추정치이며, 실제 적용 전 다른 장치의 활동이나 경과 시간에 따라 즉시 만료 대상이 달라질 수 있습니다.",
  recoveryNote: "이 장치가 즉시 만료되더라도 다른 유효한 소유자 장치 또는 호스트 로컬 CLI 승인을 통해 다시 연결하고 정책을 복구할 수 있습니다.",
  confirmLabel: "세션 만료 영향 및 복구 경로를 확인했습니다",
  savedNotice: "보안 정책 변경이 저장되었습니다.",
  reconciledSavedNotice: "이전 세션이 만료되기 전에 보안 정책 변경이 적용된 것을 확인했습니다.",
  appliedUnreadableNotice: "정책 적용은 확인되었으나 현재 구성을 다시 읽지 못했습니다. 다시 불러오기로 최신 정책을 확인하세요.",
  staleNotice: "구성 리비전이 변경되었거나 계획이 만료되었습니다. 입력한 정책 변경안은 유지되므로 최신 리비전을 기준으로 다시 미리보기 후 확인·적용하세요.",
} as const;

const DEFAULT_DRAFT: SecurityPolicyDraft = {
  remember_device_default: true,
  session_idle_seconds: null,
  session_absolute_seconds: null,
};

export function parsePolicySecondsInput(raw: string): { ok: true; value: number | null } | { ok: false; reason: string } {
  const trimmed = raw.trim();
  if (trimmed === "") {
    return { ok: true, value: null };
  }
  if (!/^[1-9][0-9]*$/.test(trimmed)) {
    return { ok: false, reason: "만료 시간(초)은 비워 두거나(제한 없음) 1 이상의 양의 정수여야 합니다." };
  }
  const value = Number(trimmed);
  if (!Number.isSafeInteger(value) || value < 1) {
    return { ok: false, reason: "만료 시간(초)은 유효한 양의 정수여야 합니다." };
  }
  return { ok: true, value };
}

export function validateSecurityPolicyDraft(
  rememberDefault: boolean,
  idleInput: string,
  absoluteInput: string,
): { ok: true; draft: SecurityPolicyDraft } | { ok: false; reason: string } {
  const idle = parsePolicySecondsInput(idleInput);
  if (!idle.ok) return { ok: false, reason: `유휴 만료 시간: ${idle.reason}` };
  const absolute = parsePolicySecondsInput(absoluteInput);
  if (!absolute.ok) return { ok: false, reason: `절대 만료 시간: ${absolute.reason}` };
  return {
    ok: true,
    draft: {
      remember_device_default: rememberDefault,
      session_idle_seconds: idle.value,
      session_absolute_seconds: absolute.value,
    },
  };
}

export function extractSecurityDraft(view: ConfigurationView | null): SecurityPolicyDraft {
  const sec = view?.configuration?.security;
  return {
    remember_device_default: typeof sec?.remember_device_default === "boolean" ? sec.remember_device_default : true,
    session_idle_seconds: typeof sec?.session_idle_seconds === "number" ? sec.session_idle_seconds : null,
    session_absolute_seconds: typeof sec?.session_absolute_seconds === "number" ? sec.session_absolute_seconds : null,
  };
}

export function securityPolicyAuthorityKey(
  state: Pick<ShellState, "capabilities" | "session">,
): string {
  return `${state.capabilities?.library_id ?? ""}:${state.session?.principal_id ?? ""}`;
}

export function calculateImmediateExpiryPreview(
  devices: DeviceSession[],
  currentSessionId: string | null,
  proposed: SecurityPolicyDraft,
  nowMs = Date.now(),
): ImmediateExpiryPreview {
  const activeDevices = devices.filter((item) => item.state === "active");
  const affectedDevices: AffectedDeviceExpiry[] = [];
  for (const device of activeDevices) {
    const reasons: Array<"idle" | "absolute"> = [];
    if (proposed.session_idle_seconds !== null && proposed.session_idle_seconds > 0) {
      const lastSeenMs = Date.parse(device.last_seen_at);
      if (Number.isFinite(lastSeenMs) && nowMs - lastSeenMs >= proposed.session_idle_seconds * 1000) {
        reasons.push("idle");
      }
    }
    if (proposed.session_absolute_seconds !== null && proposed.session_absolute_seconds > 0 && typeof device.created_at === "string") {
      const createdMs = Date.parse(device.created_at);
      if (Number.isFinite(createdMs) && nowMs - createdMs >= proposed.session_absolute_seconds * 1000) {
        reasons.push("absolute");
      }
    }
    if (reasons.length > 0) {
      affectedDevices.push({
        session_id: device.session_id,
        device_label: device.device_label,
        isCurrentDevice: currentSessionId !== null && device.session_id === currentSessionId,
        reasons,
      });
    }
  }
  const currentDeviceAffected = affectedDevices.some((item) => item.isCurrentDevice);
  let summary: string;
  if (affectedDevices.length === 0) {
    summary = `현재 활성 장치 ${activeDevices.length}대 중 미리보기 시점에 즉시 만료되는 장치는 없습니다.`;
  } else if (currentDeviceAffected) {
    summary = `현재 편집 중인 이 장치를 포함해 활성 장치 ${affectedDevices.length}대(전체 ${activeDevices.length}대)가 적용 즉시 만료될 것으로 추정됩니다.`;
  } else {
    summary = `다른 활성 장치 ${affectedDevices.length}대(전체 ${activeDevices.length}대)가 적용 즉시 만료될 것으로 추정됩니다(현재 장치는 유지).`;
  }
  return {
    affectedDevices,
    currentDeviceAffected,
    activeCount: activeDevices.length,
    summary,
    estimateNote: SECURITY_POLICY_COPY.estimateNote,
    recoveryNote: SECURITY_POLICY_COPY.recoveryNote,
  };
}

export function describeConnectionSecurity(params: {
  origin: string;
  nativeVariant: boolean;
  storageAvailable: boolean;
  remember: boolean;
  memoryOnly: boolean;
  verified: boolean;
}): { transportNote: string; storageNote: string } {
  const { origin, nativeVariant, storageAvailable, remember, memoryOnly, verified } = params;
  if (nativeVariant) {
    return {
      transportNote: "네이티브 데스크톱 연결입니다. 로컬 데몬과 서명된 발견 정보를 통해 통신합니다.",
      storageNote: "이 앱은 보호된 로컬 저장소에 연결 자격을 저장합니다. 앱을 다시 실행해도 연결이 유지되며, 로그아웃하면 이 앱에서 정리됩니다.",
    };
  }
  let transportNote: string;
  try {
    const parsed = new URL(origin);
    const isLoopback =
      parsed.protocol === "http:" &&
      (parsed.hostname === "127.0.0.1" || parsed.hostname === "localhost" || parsed.hostname === "[::1]" || parsed.hostname === "::1");
    if (isLoopback) {
      transportNote = "로컬 루프백 신뢰 연결입니다. 승인은 호스트 로컬 CLI 또는 이미 승인된 다른 소유자 장치에서 합니다.";
    } else if (parsed.protocol === "https:") {
      transportNote = "비공개 HTTPS 신뢰 연결입니다. 승인은 호스트 로컬 CLI 또는 이미 승인된 다른 소유자 장치에서 합니다.";
    } else {
      transportNote = "지원되지 않거나 안전하지 않은 연결 출처입니다. 로컬 루프백(127.0.0.1) 또는 설정된 비공개 HTTPS 주소로 접속하세요.";
    }
  } catch {
    transportNote = "연결 출처를 확인할 수 없습니다. 로컬 루프백 또는 설정된 비공개 HTTPS 주소로 접속하세요.";
  }

  let storageNote: string;
  if (!storageAvailable) {
    storageNote = "이 브라우저에서는 로컬 저장소를 사용할 수 없어 현재 탭 메모리에만 연결 자격이 유지됩니다. 페이지를 새로고침하거나 탭을 닫으면 이 브라우저에서 잊혀지며, 서버 세션 해지는 로그아웃이나 장치 해제로 수행합니다.";
  } else if (verified) {
    storageNote = memoryOnly
      ? "현재 메모리 전용으로 연결되어 있습니다. 페이지를 새로고침하거나 닫으면 이 브라우저의 자격은 사라지지만 서버 세션이 자동 해지되지는 않습니다. 연결을 완전히 끊으려면 로그아웃을 사용하세요."
      : "기억된 장치 연결입니다. 브라우저를 다시 열어도 연결이 유지되며, 이 장치 연결 해제는 로그아웃으로 수행합니다.";
  } else if (remember) {
    storageNote = "이 장치 기억을 선택하면 브라우저 로컬 저장소에 자격이 보관되어 새로고침·재실행 후에도 유지됩니다(저장소 쓰기 실패 시 메모리 전용으로 전환).";
  } else {
    storageNote = "이 장치 기억을 해제하면 현재 탭 메모리에만 자격이 유지되어 새로고침하거나 탭을 닫으면 이 브라우저에서 잊혀집니다(서버 세션은 자동 해지되지 않으며 로그아웃·장치 해제로 종료합니다).";
  }
  return { transportNote, storageNote };
}

export function describePairingDecisionConflict(pairingId: string, pairings: PairingView[]): string {
  const current = pairings.find((item) => item.pairing_id === pairingId);
  if (current) {
    const stateLabel = current.state === "approved" ? "승인됨(미수령)" : current.state === "pending" ? "승인 대기" : current.state;
    return `요청 ${pairingId}의 서버 상태가 변경되었습니다(현재 상태: ${stateLabel}, 리비전 ${current.revision}). 최신 상태를 확인한 뒤 다시 시도하세요.`;
  }
  return `요청 ${pairingId}이(가) 더 이상 활성 대기 목록에 없습니다(이미 수령되었거나 거절·만료됨). 최신 대기 요청과 연결된 장치 목록을 새로고침했습니다.`;
}

export type SecurityPolicyAuthority = Pick<
  ShellController,
  | "captureDispatchAuthority"
  | "requireVerifiedDispatch"
  | "sourceWriteIntents"
  | "registerPolicyCommitIntent"
  | "pendingPolicyRecovery"
  | "clearPolicyRecovery"
  | "recordPolicyCommitAuthLoss"
> & {
  isCurrentDispatchAuthority?: (captured: DispatchAuthority | null) => boolean;
  currentSessionId?: () => string | null;
};

export interface SecurityPolicyState {
  phase:
    | "loading"
    | "editing"
    | "previewing"
    | "preview"
    | "committing"
    | "saved"
    | "applied_unreadable"
    | "stale"
    | "error"
    | "pending";
  configuration: ConfigurationView | null;
  devices: DeviceSession[];
  draft: SecurityPolicyDraft;
  plan: ConfigurationPlanView | null;
  normalizedDraft: SecurityPolicyDraft | null;
  expiryPreview: ImmediateExpiryPreview | null;
  reason: string | null;
  notice: string | null;
  pending: { kind: "preview" | "commit"; operationId: string } | null;
}

function isCommitAuthLossError(error: unknown): error is ApiError {
  return error instanceof ApiError && error.status === 401 && (error.code === "SESSION_EXPIRED" || error.code === "SESSION_REVOKED");
}

function isAmbiguousPolicyWriteError(error: unknown): boolean {
  if (
    error instanceof PendingSourceWriteError ||
    error instanceof StaleResponseError ||
    error instanceof NetworkError ||
    error instanceof UncertainResponseError
  ) {
    return true;
  }
  if (error instanceof ApiError) {
    return (
      error.status >= 500 ||
      error.retryable ||
      error.code === "SERVICE_RECOVERING" ||
      error.code === "OPERATION_RECONCILIATION_REQUIRED"
    );
  }
  return false;
}

function isUncertainPolicyCommitError(error: unknown): boolean {
  return isCommitAuthLossError(error) || isAmbiguousPolicyWriteError(error);
}

function isStalePlanError(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    (error.status === 412 ||
      error.code === "PRECONDITION_FAILED" ||
      error.code === "STALE_PLAN" ||
      error.code === "INVALID_TRANSITION" ||
      error.code === "OPERATION_CONFLICT")
  );
}

export class SecurityPolicyFlow {
  #state: SecurityPolicyState = {
    phase: "loading",
    configuration: null,
    devices: [],
    draft: { ...DEFAULT_DRAFT },
    plan: null,
    normalizedDraft: null,
    expiryPreview: null,
    reason: null,
    notice: null,
    pending: null,
  };
  #epoch = 0;
  #previewIntent: ConfigurationIntent | null = null;
  #resolvedPreviewPlan: {
    draft: SecurityPolicyDraft;
    baseRevision: number;
    plan: ConfigurationPlanView;
  } | null = null;
  #listeners = new Set<() => void>();
  readonly client: HaeryongClient;
  readonly authority: SecurityPolicyAuthority;
  readonly now: () => number;

  constructor(client: HaeryongClient, authority: SecurityPolicyAuthority, now: () => number = () => Date.now()) {
    this.client = client;
    this.authority = authority;
    this.now = now;
  }

  snapshot = (): SecurityPolicyState => this.#state;

  subscribe = (listener: () => void): (() => void) => {
    this.#listeners.add(listener);
    return () => {
      this.#listeners.delete(listener);
    };
  };

  #apply(patch: Partial<SecurityPolicyState>): void {
    this.#state = { ...this.#state, ...patch };
    for (const listener of this.#listeners) listener();
  }

  dispose(): void {
    this.#epoch += 1;
    this.#resolvedPreviewPlan = null;
    this.#listeners.clear();
    this.#state = {
      ...this.#state,
      configuration: null,
      devices: [],
      draft: { ...DEFAULT_DRAFT },
      plan: null,
      normalizedDraft: null,
      expiryPreview: null,
      reason: null,
      notice: null,
    };
  }

  #matchesResolvedPreview(draft: SecurityPolicyDraft, baseRevision: number): ConfigurationPlanView | null {
    const cached = this.#resolvedPreviewPlan;
    if (!cached) return null;
    if (
      cached.baseRevision === baseRevision &&
      cached.draft.remember_device_default === draft.remember_device_default &&
      cached.draft.session_idle_seconds === draft.session_idle_seconds &&
      cached.draft.session_absolute_seconds === draft.session_absolute_seconds
    ) {
      return cached.plan;
    }
    return null;
  }

  #currentSessionId(): string | null {
    return this.authority.currentSessionId?.() ?? null;
  }

  #isSameAuthority(captured: DispatchAuthority | null): boolean {
    if (captured === null) return false;
    if (this.authority.isCurrentDispatchAuthority) {
      return this.authority.isCurrentDispatchAuthority(captured);
    }
    try {
      this.authority.requireVerifiedDispatch(captured);
      return true;
    } catch {
      return false;
    }
  }

  #currentIntents(): SourceWriteIntents | null {
    try {
      return this.authority.sourceWriteIntents();
    } catch {
      return null;
    }
  }

  #clearIntent(intents: SourceWriteIntents | null, intent: ConfigurationIntent): void {
    if (this.#previewIntent === intent) this.#previewIntent = null;
    if (intents && intents.securityPolicy === intent) intents.securityPolicy = null;
    this.authority.clearPolicyRecovery(intent.operationId);
  }

  async load(preserveNotice: string | null = null): Promise<void> {
    const epoch = ++this.#epoch;
    this.#resolvedPreviewPlan = null;
    this.#apply({ phase: "loading", plan: null, normalizedDraft: null, expiryPreview: null, reason: null, notice: preserveNotice });
    try {
      const [configuration, devices] = await Promise.all([this.client.configurationView(), this.client.listSessions()]);
      if (epoch !== this.#epoch) return;
      const draft = extractSecurityDraft(configuration);
      const intents = this.#currentIntents();
      const recovery: PendingPolicyRecovery | null = this.authority.pendingPolicyRecovery();
      let pending = intents?.securityPolicy ?? null;
      if (!pending && recovery && intents) {
        pending = {
          kind: "commit",
          operationId: recovery.operationId,
          planId: recovery.planId,
          baseRevision: recovery.baseRevision,
          outcome: { state: "pending" },
        };
        intents.securityPolicy = pending;
      }
      this.#previewIntent = pending?.kind === "preview" ? pending : null;
      this.#apply({
        phase: pending ? "pending" : preserveNotice === SECURITY_POLICY_COPY.savedNotice || preserveNotice === SECURITY_POLICY_COPY.reconciledSavedNotice ? "saved" : "editing",
        configuration,
        devices,
        draft,
        pending: pending ? { kind: pending.kind, operationId: pending.operationId } : null,
        reason: null,
        notice: preserveNotice,
      });
    } catch (error) {
      if (epoch !== this.#epoch) return;
      this.#apply({
        phase: "error",
        reason: error instanceof Error ? error.message : String(error),
      });
    }
  }

  cancel(): void {
    const intents = this.#currentIntents();
    const current = intents?.securityPolicy ?? null;
    if (this.#state.phase === "committing" || this.#state.pending?.kind === "commit" || current?.kind === "commit") return;
    if (this.#previewIntent) this.#clearIntent(intents, this.#previewIntent);
    this.#resolvedPreviewPlan = null;
    this.#epoch += 1;
    this.#apply({
      phase: "editing",
      plan: null,
      normalizedDraft: null,
      expiryPreview: null,
      reason: null,
      notice: null,
      pending: null,
    });
  }

  #acceptPlan(plan: ConfigurationPlanView, devices: DeviceSession[]): void {
    this.#resolvedPreviewPlan = null;
    const rawSecurity = (plan.normalized_changes?.security ?? {}) as Partial<SecurityConfiguration>;
    const fallback = this.#state.draft;
    const normalizedDraft: SecurityPolicyDraft = {
      remember_device_default:
        typeof rawSecurity.remember_device_default === "boolean"
          ? rawSecurity.remember_device_default
          : fallback.remember_device_default,
      session_idle_seconds:
        "session_idle_seconds" in rawSecurity
          ? (typeof rawSecurity.session_idle_seconds === "number" ? rawSecurity.session_idle_seconds : null)
          : fallback.session_idle_seconds,
      session_absolute_seconds:
        "session_absolute_seconds" in rawSecurity
          ? (typeof rawSecurity.session_absolute_seconds === "number" ? rawSecurity.session_absolute_seconds : null)
          : fallback.session_absolute_seconds,
    };
    const expiryPreview = calculateImmediateExpiryPreview(devices, this.#currentSessionId(), normalizedDraft, this.now());
    this.#apply({
      phase: "preview",
      plan,
      devices,
      normalizedDraft,
      expiryPreview,
      pending: null,
      reason: null,
      notice: null,
    });
  }

  async preview(draft: SecurityPolicyDraft): Promise<void> {
    const configuration = this.#state.configuration;
    const ownerIntents = this.#currentIntents();
    if (!configuration || !ownerIntents || ownerIntents.securityPolicy !== null || ["previewing", "committing", "pending"].includes(this.#state.phase)) {
      return;
    }
    const captured: DispatchAuthority | null = this.authority.captureDispatchAuthority();
    this.authority.requireVerifiedDispatch(captured);
    const epoch = ++this.#epoch;
    const reusedPlan = this.#matchesResolvedPreview(draft, configuration.config_revision);
    if (!reusedPlan) {
      this.#resolvedPreviewPlan = null;
    }
    this.#apply({
      phase: "previewing",
      draft,
      plan: null,
      normalizedDraft: null,
      expiryPreview: null,
      reason: null,
      notice: null,
    });
    let latestDevices: DeviceSession[];
    try {
      this.authority.requireVerifiedDispatch(captured);
      latestDevices = await this.client.listSessions();
    } catch (deviceError) {
      if (epoch !== this.#epoch) return;
      const sameAuthority = this.#isSameAuthority(captured);
      if (!sameAuthority) {
        this.#resolvedPreviewPlan = null;
      }
      const scrubbed = sameAuthority
        ? {}
        : {
            configuration: null,
            devices: [],
            draft: { ...DEFAULT_DRAFT },
            plan: null,
            normalizedDraft: null,
            expiryPreview: null,
          };
      this.#apply({
        ...scrubbed,
        phase: "error",
        plan: null,
        normalizedDraft: null,
        expiryPreview: null,
        reason: sameAuthority ? (deviceError instanceof Error ? deviceError.message : String(deviceError)) : null,
        pending: null,
      });
      return;
    }
    if (epoch !== this.#epoch || !this.#isSameAuthority(captured)) return;
    if (reusedPlan) {
      this.#acceptPlan(reusedPlan, latestDevices);
      return;
    }
    const operationId = crypto.randomUUID();
    const intent: ConfigurationIntent = { kind: "preview", operationId, outcome: { state: "pending" } };
    this.#previewIntent = intent;
    ownerIntents.securityPolicy = intent;
    try {
      const plan = await resolveWriteIntent(
        intent,
        () => {
          this.authority.requireVerifiedDispatch(captured);
          return this.client.configurationPlan(
            {
              scope: { kind: "library" },
              expected_config_revision: configuration.config_revision,
              changes: {
                security: {
                  remember_device_default: draft.remember_device_default,
                  session_idle_seconds: draft.session_idle_seconds,
                  session_absolute_seconds: draft.session_absolute_seconds,
                },
              },
            },
            operationId,
          );
        },
        isAmbiguousPolicyWriteError,
      );
      if (epoch !== this.#epoch || !this.#isSameAuthority(captured)) return;
      this.#acceptPlan(plan as ConfigurationPlanView, latestDevices);
      if (epoch === this.#epoch) this.#clearIntent(ownerIntents, intent);
    } catch (error) {
      if (epoch !== this.#epoch) return;
      const sameAuthority = this.#isSameAuthority(captured);
      const scrubbed = sameAuthority
        ? {}
        : {
            configuration: null,
            devices: [],
            draft: { ...DEFAULT_DRAFT },
            plan: null,
            normalizedDraft: null,
            expiryPreview: null,
          };
      if (isAmbiguousPolicyWriteError(error)) {
        this.#apply({
          ...scrubbed,
          phase: "pending",
          reason: sameAuthority ? (error instanceof Error ? error.message : String(error)) : null,
          pending: { kind: "preview", operationId },
        });
        return;
      }
      this.#clearIntent(ownerIntents, intent);
      if (sameAuthority && isStalePlanError(error)) {
        await this.#refreshStaleAfterFailure();
        return;
      }
      this.#apply({
        ...scrubbed,
        phase: "error",
        reason: sameAuthority ? (error instanceof Error ? error.message : String(error)) : null,
        pending: null,
      });
    }
  }

  async #refreshStaleAfterFailure(): Promise<void> {
    this.#resolvedPreviewPlan = null;
    const epoch = this.#epoch;
    try {
      const [configuration, devices] = await Promise.all([this.client.configurationView(), this.client.listSessions()]);
      if (epoch !== this.#epoch) return;
      // Preserve the user-proposed draft across a stale revision/plan reread so
      // the user can explicitly preview and confirm their proposed policy
      // against the refreshed server revision without losing their edits.
      this.#apply({
        phase: "stale",
        configuration,
        devices,
        plan: null,
        normalizedDraft: null,
        expiryPreview: null,
        reason: SECURITY_POLICY_COPY.staleNotice,
        pending: null,
      });
    } catch {
      if (epoch !== this.#epoch) return;
      this.#apply({
        phase: "stale",
        plan: null,
        normalizedDraft: null,
        expiryPreview: null,
        reason: SECURITY_POLICY_COPY.staleNotice,
        pending: null,
      });
    }
  }

  async commit(confirmed: boolean): Promise<void> {
    const plan = this.#state.plan;
    if (!plan || this.#state.phase !== "preview") return;
    if (!confirmed) {
      this.#apply({ reason: "세션 만료 영향 및 복구 경로 확인에 동의해 주세요." });
      return;
    }
    const captured: DispatchAuthority | null = this.authority.captureDispatchAuthority();
    this.authority.requireVerifiedDispatch(captured);
    const ownerIntents = this.authority.sourceWriteIntents();
    this.#resolvedPreviewPlan = null;
    const epoch = ++this.#epoch;
    const operationId = plan.plan_id;
    const intent: ConfigurationIntent = {
      kind: "commit",
      operationId,
      planId: plan.plan_id,
      baseRevision: plan.base_config_revision,
      outcome: { state: "pending" },
    };
    // Pre-dispatch registration: record the library/principal-bound intent and
    // token-free recovery record BEFORE dispatching configurationPlanCommit.
    ownerIntents.securityPolicy = intent;
    this.authority.registerPolicyCommitIntent(plan.plan_id, plan.base_config_revision, operationId);
    this.#apply({ phase: "committing", reason: null, notice: null });
    try {
      await resolveWriteIntent(
        intent,
        () => {
          this.authority.requireVerifiedDispatch(captured);
          return this.client.configurationPlanCommit(
            plan.plan_id,
            { expected_config_revision: plan.base_config_revision, confirmed: true },
            operationId,
          );
        },
        isUncertainPolicyCommitError,
      );
    } catch (error) {
      if (isCommitAuthLossError(error)) {
        // TE-P05 v3: commit may have durably applied in SQLite before the final
        // response guard returned 401 SESSION_EXPIRED/REVOKED, or failed before
        // commit. Retain the minimal token-free intent as UNKNOWN in the
        // dispatching owner/library scope, and clear the current epoch's private
        // policy/device/draft data without disturbing a newer epoch/authority.
        this.authority.recordPolicyCommitAuthLoss(captured, operationId, error.code, false);
        if (epoch !== this.#epoch) return;
        this.#apply({
          phase: "pending",
          configuration: null,
          devices: [],
          draft: { ...DEFAULT_DRAFT },
          plan: null,
          normalizedDraft: null,
          expiryPreview: null,
          reason: null,
          notice: null,
          pending: { kind: "commit", operationId },
        });
        return;
      }
      if (epoch !== this.#epoch) return;
      const sameAuthority = this.#isSameAuthority(captured);
      const scrubbed = sameAuthority
        ? {}
        : {
            configuration: null,
            devices: [],
            draft: { ...DEFAULT_DRAFT },
            plan: null,
            normalizedDraft: null,
            expiryPreview: null,
          };
      if (isAmbiguousPolicyWriteError(error)) {
        this.#apply({
          ...scrubbed,
          phase: "pending",
          reason: sameAuthority ? (error instanceof Error ? error.message : String(error)) : null,
          pending: { kind: "commit", operationId },
        });
        return;
      }
      this.#clearIntent(ownerIntents, intent);
      if (sameAuthority && isStalePlanError(error)) {
        await this.#refreshStaleAfterFailure();
        return;
      }
      this.#apply({
        ...scrubbed,
        phase: "error",
        reason: sameAuthority ? (error instanceof Error ? error.message : String(error)) : null,
        pending: null,
      });
      return;
    }

    // Commit receipt (200 OK) is definitively confirmed! Clear the commit intent
    // so duplicate commit is never offered, then read the updated configuration.
    this.#clearIntent(ownerIntents, intent);
    if (epoch !== this.#epoch || !this.#isSameAuthority(captured)) return;
    await this.#readAfterConfirmedCommit(epoch, captured, operationId, SECURITY_POLICY_COPY.savedNotice);
  }

  async #readAfterConfirmedCommit(
    epoch: number,
    captured: DispatchAuthority | null,
    operationId: string,
    savedNotice: string,
  ): Promise<void> {
    try {
      this.authority.requireVerifiedDispatch(captured);
      const [configuration, devices] = await Promise.all([this.client.configurationView(), this.client.listSessions()]);
      if (epoch !== this.#epoch || !this.#isSameAuthority(captured)) return;
      this.#apply({
        phase: "saved",
        configuration,
        devices,
        draft: extractSecurityDraft(configuration),
        plan: null,
        normalizedDraft: null,
        expiryPreview: null,
        pending: null,
        reason: null,
        notice: savedNotice,
      });
    } catch (readError) {
      if (isCommitAuthLossError(readError)) {
        this.authority.recordPolicyCommitAuthLoss(captured, operationId, readError.code, true);
        if (epoch !== this.#epoch) return;
        this.#apply({
          phase: "applied_unreadable",
          configuration: null,
          devices: [],
          draft: { ...DEFAULT_DRAFT },
          plan: null,
          normalizedDraft: null,
          expiryPreview: null,
          pending: null,
          reason: null,
          notice: null,
        });
        return;
      }
      if (epoch !== this.#epoch) return;
      const sameAuthority = this.#isSameAuthority(captured);
      // Never show false Saved when the follow-up configuration read fails.
      this.#apply({
        ...(sameAuthority
          ? {}
          : {
              configuration: null,
              devices: [],
              draft: { ...DEFAULT_DRAFT },
            }),
        phase: "applied_unreadable",
        plan: null,
        normalizedDraft: null,
        expiryPreview: null,
        pending: null,
        reason: sameAuthority ? (readError instanceof Error ? readError.message : String(readError)) : null,
        notice: sameAuthority ? SECURITY_POLICY_COPY.appliedUnreadableNotice : null,
      });
    }
  }

  /**
   * Reconciles a pending preview or commit intent under current verified
   * same-owner authority via GET /api/v1/operations/{id}.
   * Absent (404) or inaccessible receipts remain UNKNOWN (phase: "pending")
   * with the original operationId preserved; never claims not-applied from 404.
   */
  async recover(): Promise<void> {
    const ownerIntents = this.#currentIntents();
    if (!ownerIntents) return;
    const recovery = this.authority.pendingPolicyRecovery();
    let pending = ownerIntents.securityPolicy;
    if (!pending && recovery) {
      pending = {
        kind: "commit",
        operationId: recovery.operationId,
        planId: recovery.planId,
        baseRevision: recovery.baseRevision,
        outcome: { state: "pending" },
      };
      ownerIntents.securityPolicy = pending;
    }
    if (!pending && this.#state.phase === "error" && this.#resolvedPreviewPlan !== null) {
      const cached = this.#resolvedPreviewPlan;
      const captured: DispatchAuthority | null = this.authority.captureDispatchAuthority();
      this.authority.requireVerifiedDispatch(captured);
      const epoch = ++this.#epoch;
      this.#apply({ phase: "previewing", reason: null, notice: null });
      try {
        this.authority.requireVerifiedDispatch(captured);
        const devices = await this.client.listSessions();
        if (epoch !== this.#epoch || !this.#isSameAuthority(captured)) return;
        this.#acceptPlan(cached.plan, devices);
      } catch (deviceError) {
        if (epoch !== this.#epoch) return;
        const sameAuthority = this.#isSameAuthority(captured);
        if (!sameAuthority) this.#resolvedPreviewPlan = null;
        const scrubbed = sameAuthority
          ? {}
          : {
              configuration: null,
              devices: [],
              draft: { ...DEFAULT_DRAFT },
              plan: null,
              normalizedDraft: null,
              expiryPreview: null,
            };
        this.#apply({
          ...scrubbed,
          phase: "error",
          plan: null,
          normalizedDraft: null,
          expiryPreview: null,
          reason: sameAuthority ? (deviceError instanceof Error ? deviceError.message : String(deviceError)) : null,
          pending: null,
        });
      }
      return;
    }
    if (!pending || this.#state.phase !== "pending") return;
    const captured: DispatchAuthority | null = this.authority.captureDispatchAuthority();
    this.authority.requireVerifiedDispatch(captured);
    const epoch = ++this.#epoch;
    this.#apply({
      phase: pending.kind === "preview" ? "previewing" : "committing",
      reason: null,
      notice: null,
    });
    try {
      const result = await resolveWriteIntent(
        pending,
        () => {
          this.authority.requireVerifiedDispatch(captured);
          return reconcileSourceWrite<ConfigurationPlanView | ConfigurationCommitView>(
            pending.operationId,
            (id) => this.client.operation(id),
          );
        },
        isUncertainPolicyCommitError,
      );
      if (epoch !== this.#epoch || !this.#isSameAuthority(captured)) return;
      if (pending.kind === "preview") {
        const plan = result as ConfigurationPlanView;
        this.#resolvedPreviewPlan = {
          draft: { ...this.#state.draft },
          baseRevision: plan.base_config_revision,
          plan,
        };
        let devices: DeviceSession[];
        try {
          this.authority.requireVerifiedDispatch(captured);
          devices = await this.client.listSessions();
        } catch (deviceError) {
          if (epoch !== this.#epoch) return;
          const sameAuthority = this.#isSameAuthority(captured);
          if (!sameAuthority) {
            this.#resolvedPreviewPlan = null;
          }
          this.#clearIntent(ownerIntents, pending);
          const scrubbed = sameAuthority
            ? {}
            : {
                configuration: null,
                devices: [],
                draft: { ...DEFAULT_DRAFT },
                plan: null,
                normalizedDraft: null,
                expiryPreview: null,
              };
          this.#apply({
            ...scrubbed,
            phase: "error",
            plan: null,
            normalizedDraft: null,
            expiryPreview: null,
            reason: sameAuthority ? (deviceError instanceof Error ? deviceError.message : String(deviceError)) : null,
            pending: null,
          });
          return;
        }
        if (epoch !== this.#epoch || !this.#isSameAuthority(captured)) return;
        this.#acceptPlan(plan, devices);
        if (epoch === this.#epoch) this.#clearIntent(ownerIntents, pending);
        return;
      }
      this.#clearIntent(ownerIntents, pending);
      await this.#readAfterConfirmedCommit(epoch, captured, pending.operationId, SECURITY_POLICY_COPY.reconciledSavedNotice);
    } catch (error) {
      const cause = error instanceof PendingSourceWriteError ? error.cause : error;
      if (pending.kind === "commit" && isCommitAuthLossError(cause)) {
        this.authority.recordPolicyCommitAuthLoss(captured, pending.operationId, cause.code, false);
        if (epoch !== this.#epoch) return;
        this.#apply({
          phase: "pending",
          configuration: null,
          devices: [],
          draft: { ...DEFAULT_DRAFT },
          plan: null,
          normalizedDraft: null,
          expiryPreview: null,
          reason: null,
          notice: null,
          pending: { kind: pending.kind, operationId: pending.operationId },
        });
        return;
      }
      if (epoch !== this.#epoch) return;
      const sameAuthority = this.#isSameAuthority(captured);
      const scrubbed = sameAuthority
        ? {}
        : {
            configuration: null,
            devices: [],
            draft: { ...DEFAULT_DRAFT },
            plan: null,
            normalizedDraft: null,
            expiryPreview: null,
          };
      if (isAmbiguousPolicyWriteError(error)) {
        this.#apply({
          ...scrubbed,
          phase: "pending",
          reason: sameAuthority ? (error instanceof Error ? error.message : String(error)) : null,
          pending: { kind: pending.kind, operationId: pending.operationId },
        });
        return;
      }
      this.#clearIntent(ownerIntents, pending);
      if (sameAuthority && isStalePlanError(error)) {
        await this.#refreshStaleAfterFailure();
        return;
      }
      this.#apply({
        ...scrubbed,
        phase: "error",
        reason: sameAuthority ? (error instanceof Error ? error.message : String(error)) : null,
        pending: null,
      });
    }
  }
}
