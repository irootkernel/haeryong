import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { CONNECT_COPY } from "../connect.tsx";
import { navigate } from "../router.tsx";
import type { ShellController, ShellState } from "../../client/controller.ts";
import type { DeviceSession } from "../../client/types.ts";
import {
  SECURITY_POLICY_COPY,
  SecurityPolicyFlow,
  describeConnectionSecurity,
  securityPolicyAuthorityKey,
  validateSecurityPolicyDraft,
} from "./security-policy.ts";
import type { SecurityPolicyState } from "./security-policy.ts";

function formatSeconds(value: number | null | undefined): string {
  if (value === null || value === undefined) return "제한 없음";
  return `${value}초`;
}

export function SessionsScreen({ controller }: { controller: ShellController }) {
  const [shellState, setShellState] = useState<ShellState>(controller.state);
  useEffect(() => controller.subscribe(setShellState), [controller]);

  const authorityKey = securityPolicyAuthorityKey(shellState);
  const flow = useMemo(
    () =>
      new SecurityPolicyFlow(controller.client, {
        captureDispatchAuthority: () => controller.captureDispatchAuthority(),
        requireVerifiedDispatch: (captured) => controller.requireVerifiedDispatch(captured),
        isCurrentDispatchAuthority: (captured) => controller.isCurrentDispatchAuthority(captured),
        sourceWriteIntents: () => controller.sourceWriteIntents(),
        registerPolicyCommitIntent: (planId, baseRevision, operationId) =>
          controller.registerPolicyCommitIntent(planId, baseRevision, operationId),
        pendingPolicyRecovery: () => controller.pendingPolicyRecovery(),
        clearPolicyRecovery: (operationId) => controller.clearPolicyRecovery(operationId),
        recordPolicyCommitAuthLoss: (captured, operationId, code, confirmed) =>
          controller.recordPolicyCommitAuthLoss(captured, operationId, code, confirmed),
        currentSessionId: () => controller.state.session?.session_id ?? null,
      }),
    [controller, authorityKey],
  );

  const [flowState, setFlowState] = useState<SecurityPolicyState>(flow.snapshot);
  const [rememberDefault, setRememberDefault] = useState(true);
  const [idleInput, setIdleInput] = useState("");
  const [absoluteInput, setAbsoluteInput] = useState("");
  const [confirmedImpact, setConfirmedImpact] = useState(false);
  const [inputError, setInputError] = useState<string | null>(null);
  const [deviceMessage, setDeviceMessage] = useState("");
  const [revokeBusy, setRevokeBusy] = useState<string | null>(null);
  const syncedRevisionRef = useRef<number | null>(null);

  useEffect(() => {
    const off = flow.subscribe(() => {
      const next = flow.snapshot();
      setFlowState(next);
      const rev = next.configuration?.config_revision ?? null;
      if (rev !== null && rev !== syncedRevisionRef.current && (next.phase === "editing" || next.phase === "saved")) {
        syncedRevisionRef.current = rev;
        setRememberDefault(next.draft.remember_device_default);
        setIdleInput(next.draft.session_idle_seconds === null ? "" : String(next.draft.session_idle_seconds));
        setAbsoluteInput(next.draft.session_absolute_seconds === null ? "" : String(next.draft.session_absolute_seconds));
      }
    });
    void flow.load();
    return () => {
      off();
      flow.dispose();
    };
  }, [flow]);

  const refreshAll = useCallback(async () => {
    setDeviceMessage("");
    setInputError(null);
    await flow.load();
  }, [flow]);

  const onPreview = async (event: React.FormEvent) => {
    event.preventDefault();
    setInputError(null);
    setConfirmedImpact(false);
    const validated = validateSecurityPolicyDraft(rememberDefault, idleInput, absoluteInput);
    if (!validated.ok) {
      setInputError(validated.reason);
      return;
    }
    await flow.preview(validated.draft);
  };

  const onCommit = async () => {
    setInputError(null);
    await flow.commit(confirmedImpact);
  };

  const revokePeer = async (device: DeviceSession) => {
    if (revokeBusy !== null) return;
    setDeviceMessage("");
    setRevokeBusy(device.session_id);
    try {
      const outcome = await controller.revokeSession(device.session_id, device.device_label);
      if (outcome === "unknown") {
        setDeviceMessage(`${device.device_label}: ${CONNECT_COPY.revokeUnknown}`);
        return;
      }
      await flow.load(flowState.phase === "saved" ? flowState.notice : null);
      setDeviceMessage(`${device.device_label}: ${CONNECT_COPY.revokedResolved}`);
    } catch (error) {
      setDeviceMessage(error instanceof Error ? error.message : String(error));
    } finally {
      setRevokeBusy(null);
    }
  };

  const resolvePendingRevoke = async (sessionId: string) => {
    try {
      const resolved = await controller.confirmRevokeResolved(sessionId);
      if (resolved === null) {
        setDeviceMessage(CONNECT_COPY.revokeUnknown);
        return;
      }
      if (resolved) {
        setDeviceMessage(CONNECT_COPY.revokedResolved);
        await flow.load(flowState.phase === "saved" ? flowState.notice : null);
      } else {
        setDeviceMessage(CONNECT_COPY.revokedStillPresent);
      }
    } catch (error) {
      setDeviceMessage(error instanceof Error ? error.message : String(error));
    }
  };

  const nativeVariant = controller.nativeSessionAvailable;
  const endpointOrigin = nativeVariant ? controller.client.publicOrigin ?? location.origin : location.origin;
  const securityNotes = describeConnectionSecurity({
    origin: endpointOrigin,
    nativeVariant,
    storageAvailable: shellState.storageAvailable,
    remember: !shellState.memoryOnly,
    memoryOnly: shellState.memoryOnly,
    verified: shellState.phase === "verified",
  });
  const verified = shellState.phase === "verified";
  const currentSession = shellState.session;

  return (
    <section className="panel" data-sessions-screen>
      <h1>세션 및 보안 정책</h1>
      <p className="hint">
        연결된 소유자 장치 세션을 확인·해제하고, 서재 전체의 세션 만료 및 기본 장치 기억 정책을 리비전 기반으로 관리합니다.
      </p>

      <div data-testid="current-session-summary">
        <h2>현재 연결된 이 장치</h2>
        {currentSession ? (
          <dl>
            <dt>장치 이름</dt>
            <dd>{currentSession.device_label} (이 장치)</dd>
            <dt>세션 ID</dt>
            <dd className="muted-code">{currentSession.session_id}</dd>
            <dt>연결 출처</dt>
            <dd className="muted-code">{currentSession.origin}</dd>
            <dt>상태</dt>
            <dd>{currentSession.state}</dd>
            <dt>생성 시각</dt>
            <dd className="muted-code">{currentSession.created_at}</dd>
            <dt>최근 소유자 활동 시각</dt>
            <dd className="muted-code" data-testid="current-session-last-seen">{currentSession.last_seen_at}</dd>
            <dt>파생 만료 시각</dt>
            <dd className="muted-code" data-testid="current-session-expires-at">{currentSession.expires_at ?? "제한 없음"}</dd>
          </dl>
        ) : (
          <p className="hint">현재 세션 정보를 불러오는 중입니다.</p>
        )}
        <p className="hint" data-testid="sessions-transport-note">{securityNotes.transportNote}</p>
        <p className="hint" data-testid="sessions-storage-note">{securityNotes.storageNote}</p>
        <div className="form-row">
          <button
            type="button"
            data-testid="logout-current-device"
            disabled={!verified}
            onClick={() => void controller.logout()}
          >
            이 장치 로그아웃(해제)
          </button>
          <button type="button" data-testid="sessions-refresh-button" onClick={() => void refreshAll()}>
            세션 및 정책 새로고침
          </button>
          <button type="button" onClick={() => navigate("/connect")}>
            연결 및 승인 도구로 이동
          </button>
        </div>
      </div>

      <div data-testid="security-policy-section">
        <h2>서재 보안 정책 (리비전 기반 계획·적용)</h2>
        <p className="hint">
          문서 읽기·쓰기·파일 내려받기·장치 관리 등 실제 소유자 활동만 최근 활동 시각을 갱신합니다.
          백그라운드 연결 상태 확인, 알림 대기, 뷰어 표시 시간 자동 연장은 유휴 만료 시계를 연장하지 않습니다.
        </p>
        <p className="hint">
          세션이 만료되거나 해지되면 진행 중인 파일 내려받기나 뷰어 자산 전달은 다음 권한 확인 경계에서 중단되며,
          이미 브라우저 화면에 표시된 내용은 회수할 수 없습니다.
        </p>
        {flowState.configuration && (
          <p className="hint" data-testid="policy-current-summary">
            현재 구성 리비전 <span className="muted-code">{flowState.configuration.config_revision}</span> ·
            기본 기억: <strong>{flowState.configuration.configuration.security?.remember_device_default !== false ? "예" : "아니오"}</strong> ·
            유휴 만료: <strong>{formatSeconds(flowState.configuration.configuration.security?.session_idle_seconds)}</strong> ·
            절대 만료: <strong>{formatSeconds(flowState.configuration.configuration.security?.session_absolute_seconds)}</strong>
          </p>
        )}

        {flowState.phase === "saved" && flowState.notice && (
          <p className="status" role="status" data-testid="policy-saved-notice">
            {flowState.notice}
          </p>
        )}

        {flowState.phase === "applied_unreadable" && (
          <div role="alert" data-testid="policy-applied-unreadable-notice">
            <p className="status">{flowState.notice ?? SECURITY_POLICY_COPY.appliedUnreadableNotice}</p>
            {flowState.reason && <p className="hint">{flowState.reason}</p>}
            <button type="button" onClick={() => void refreshAll()}>
              구성 다시 불러오기
            </button>
          </div>
        )}

        {(flowState.phase === "error" || flowState.phase === "stale") && flowState.reason && (
          <div role="alert" data-testid="policy-error-notice">
            <p className="status">{flowState.reason}</p>
            <button type="button" onClick={() => void refreshAll()}>
              최신 구성 다시 불러오기
            </button>
          </div>
        )}

        {inputError && (
          <p className="status" role="alert" data-testid="policy-input-error">
            {inputError}
          </p>
        )}

        {flowState.phase === "pending" && flowState.pending && (
          <div className="unknown-note" role="status" data-testid="policy-pending-panel">
            <p>
              미확정 보안 정책 {flowState.pending.kind === "commit" ? "적용" : "미리보기"} 작업(
              <span className="muted-code">{flowState.pending.operationId}</span>)이 있습니다.
              중복 제출 대신 먼저 영수증 상태를 확인하세요.
            </p>
            {flowState.reason && <p className="hint">{flowState.reason}</p>}
            <button
              type="button"
              className="primary"
              data-testid="policy-recover-button"
              disabled={!verified}
              onClick={() => void flow.recover()}
            >
              작업 결과 확인
            </button>
          </div>
        )}

        <form onSubmit={(event) => void onPreview(event)}>
          <label className="choice">
            <input
              type="checkbox"
              checked={rememberDefault}
              aria-label="기본으로 이 장치 기억"
              data-testid="policy-remember-default"
              disabled={!verified || flowState.phase === "previewing" || flowState.phase === "committing" || flowState.phase === "pending"}
              onChange={(event) => setRememberDefault(event.target.checked)}
            />
            <span>새 페어링 시 기본으로 이 장치 기억 선택</span>
          </label>
          <label className="field">
            <span>유휴 만료 시간 (초, 비우면 제한 없음)</span>
            <input
              type="text"
              inputMode="numeric"
              value={idleInput}
              placeholder="제한 없음"
              aria-label="유휴 만료 시간 (초)"
              data-testid="policy-idle-seconds"
              disabled={!verified || flowState.phase === "previewing" || flowState.phase === "committing" || flowState.phase === "pending"}
              onChange={(event) => setIdleInput(event.target.value)}
            />
          </label>
          <label className="field">
            <span>절대 만료 시간 (초, 비우면 제한 없음)</span>
            <input
              type="text"
              inputMode="numeric"
              value={absoluteInput}
              placeholder="제한 없음"
              aria-label="절대 만료 시간 (초)"
              data-testid="policy-absolute-seconds"
              disabled={!verified || flowState.phase === "previewing" || flowState.phase === "committing" || flowState.phase === "pending"}
              onChange={(event) => setAbsoluteInput(event.target.value)}
            />
          </label>
          <div className="form-row">
            <button
              type="submit"
              className="primary"
              data-testid="policy-preview-button"
              disabled={
                !verified ||
                !flowState.configuration ||
                flowState.phase === "previewing" ||
                flowState.phase === "committing" ||
                flowState.phase === "pending"
              }
            >
              {flowState.phase === "previewing" ? "계획 계산 중…" : "변경 영향 미리보기"}
            </button>
          </div>
        </form>

        {flowState.phase === "preview" && flowState.plan && flowState.normalizedDraft && flowState.expiryPreview && (
          <div data-testid="policy-preview-panel">
            <h3>보안 정책 변경 계획 미리보기</h3>
            <p className="hint">
              계획 ID <span className="muted-code">{flowState.plan.plan_id}</span> · 기준 리비전{" "}
              <span className="muted-code">{flowState.plan.base_config_revision}</span> · 계획 만료{" "}
              <span className="muted-code">{flowState.plan.expires_at}</span>
            </p>
            <p data-testid="policy-normalized-summary">
              적용될 정규화 정책 — 기본 기억:{" "}
              <strong>{flowState.normalizedDraft.remember_device_default ? "예" : "아니오"}</strong> · 유휴 만료:{" "}
              <strong>{formatSeconds(flowState.normalizedDraft.session_idle_seconds)}</strong> · 절대 만료:{" "}
              <strong>{formatSeconds(flowState.normalizedDraft.session_absolute_seconds)}</strong>
            </p>
            <div data-testid="policy-expiry-preview">
              <p>
                <strong>{flowState.expiryPreview.summary}</strong>
              </p>
              {flowState.expiryPreview.affectedDevices.length > 0 && (
                <ul data-testid="policy-affected-devices">
                  {flowState.expiryPreview.affectedDevices.map((item) => (
                    <li key={item.session_id} data-testid={`policy-affected-${item.session_id}`}>
                      {item.device_label} (<span className="muted-code">{item.session_id.slice(0, 8)}</span>)
                      {item.isCurrentDevice ? " [이 장치 — 적용 즉시 현재 세션 만료]" : ""} — 사유:{" "}
                      {item.reasons.map((r) => (r === "idle" ? "유휴 시간 초과" : "절대 수명 초과")).join(", ")}
                    </li>
                  ))}
                </ul>
              )}
              <p className="hint">{flowState.expiryPreview.estimateNote}</p>
              <p className="hint">{flowState.expiryPreview.recoveryNote}</p>
            </div>
            {flowState.reason && (
              <p className="status" role="alert">
                {flowState.reason}
              </p>
            )}
            <label className="choice">
              <input
                type="checkbox"
                checked={confirmedImpact}
                aria-label={SECURITY_POLICY_COPY.confirmLabel}
                data-testid="policy-confirm-checkbox"
                onChange={(event) => setConfirmedImpact(event.target.checked)}
              />
              <span>{SECURITY_POLICY_COPY.confirmLabel}</span>
            </label>
            <div className="form-row">
              <button
                type="button"
                className="primary"
                data-testid="policy-commit-button"
                disabled={!verified || !confirmedImpact}
                onClick={() => void onCommit()}
              >
                정책 변경 적용
              </button>
              <button type="button" data-testid="policy-cancel-button" onClick={() => flow.cancel()}>
                미리보기 취소
              </button>
            </div>
          </div>
        )}
      </div>

      <div data-testid="sessions-device-list-section">
        <h2>장치 세션 목록</h2>
        <p className="hint">
          이 장치의 연결 해제는 <strong>이 장치 로그아웃</strong>으로 수행하며, 다른 소유자 장치는 개별 <strong>장치 해제</strong>로 즉시 해지할 수 있습니다.
        </p>
        {deviceMessage && (
          <p className="status" role="status" data-testid="sessions-device-message">
            {deviceMessage}
          </p>
        )}
        <ul className="device-list" aria-label="장치 세션 목록" data-testid="sessions-device-list">
          {flowState.devices.map((device) => {
            const isCurrent = currentSession !== null && device.session_id === currentSession.session_id;
            const pendingRevoke = shellState.pendingRevokes.find((row) => row.sessionId === device.session_id);
            return (
              <li key={device.session_id} data-testid={`session-row-${device.session_id}`}>
                <div>
                  <strong>{device.device_label}</strong>
                  {isCurrent ? " (이 장치)" : ""} · 상태: <span data-testid={`session-state-${device.session_id}`}>{device.state}</span> · 세션{" "}
                  <span className="muted-code">{device.session_id.slice(0, 8)}</span> · 출처{" "}
                  <span className="muted-code">{device.origin}</span>
                  <div className="hint">
                    생성: <span className="muted-code">{device.created_at ?? "-"}</span> · 최근 활동:{" "}
                    <span className="muted-code">{device.last_seen_at}</span> · 만료:{" "}
                    <span className="muted-code">{device.expires_at ?? "제한 없음"}</span>
                    {pendingRevoke ? ` — ${CONNECT_COPY.revokeUnknown}` : ""}
                  </div>
                </div>
                <div className="form-row">
                  {pendingRevoke && (
                    <button
                      type="button"
                      disabled={!verified || revokeBusy !== null}
                      onClick={() => void resolvePendingRevoke(device.session_id)}
                    >
                      {CONNECT_COPY.resolveButton}
                    </button>
                  )}
                  {isCurrent ? (
                    <button
                      type="button"
                      disabled={!verified}
                      onClick={() => void controller.logout()}
                    >
                      이 장치 로그아웃
                    </button>
                  ) : (
                    <button
                      type="button"
                      data-testid={`revoke-device-${device.session_id}`}
                      disabled={!verified || revokeBusy !== null || device.state === "revoked"}
                      onClick={() => void revokePeer(device)}
                    >
                      {CONNECT_COPY.revokePrefix}
                      {device.device_label}
                    </button>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      </div>

      <div data-testid="private-exposure-guidance">
        <h2>비공개 네트워크 노출 안내 (호스트 CLI 관리)</h2>
        <p className="hint">
          비공개 HTTPS 원격 접속 주소, 뷰어 출처 매핑, Tailscale Serve 프록시 경로는 브라우저 화면이 아닌 호스트 로컬 CLI 구성 계획 및
          Tailscale Serve 설정으로 관리합니다. 공개 Funnel은 사용하지 않으며, 기존 무관한 Tailscale Serve 경로는 보존해야 합니다.
        </p>
      </div>

      <nav aria-label="다른 설정" className="form-row">
        {[
          ["/connect", "연결 및 장치 승인"],
          ["/settings/ai", "AI 제공자"],
          ["/settings/projects", "프로젝트 및 소스"],
          ["/settings/backup", "백업 및 내보내기"],
          ["/settings/storage", "저장 공간"],
        ].map(([path, label]) => (
          <a
            className="action-link"
            key={path}
            href={path}
            onClick={(event) => {
              if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
              event.preventDefault();
              navigate(path);
            }}
          >
            {label}
          </a>
        ))}
      </nav>
    </section>
  );
}
