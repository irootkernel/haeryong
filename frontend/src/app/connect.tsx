// S01 Connect screen: pair/reconnect this device and, once verified, the
// current owner tools (exact-request approve/reject, device list/revoke,
// private status, logout). Writes are offered only in the verified phase;
// disconnected/degraded keeps the credential and shows disabled controls.
import { useCallback, useEffect, useRef, useState } from "react";
import { NOTICE, ShellController } from "../client/controller.ts";
import type { ShellState } from "../client/controller.ts";
import type { DeviceSession, PairingCreated } from "../client/types.ts";

export const CONNECT_COPY = {
  heading: "Haeryong 연결",
  intro: "이 신뢰 브라우저를 개인 서재에 연결합니다.",
  endpointLabel: "연결 대상",
  securityNote: "로컬 신뢰 연결입니다. 승인은 로컬 CLI 또는 이미 승인된 다른 장치에서 합니다.",
  deviceLabel: "장치 이름",
  rememberLabel: "이 장치 기억",
  requestButton: "연결 코드 요청",
  waitingButton: "승인 기다리는 중",
  stopWaiting: "대기 중단",
  stopped: "대기를 중단했습니다. 로컬 대기 정보만 폐기되며, 서버 요청은 만료되거나 거절될 때까지 유지됩니다.",
  waiting: "승인을 기다리는 중입니다. 아래 코드를 비교하세요.",
  expired: "연결 요청이 만료되었습니다. 새 코드를 요청하세요.",
  rejected: "연결 요청이 거절되었습니다. 확인 후 새 코드를 요청하세요.",
  issuanceLost: "연결이 확인되지 않았습니다. 일회용 발급은 자동으로 반복되지 않습니다. 다시 연결하세요.",
  ownerHeading: "연결된 소유자",
  statusButton: "비공개 상태 확인",
  logoutButton: "로그아웃",
  requestId: "요청 ID",
  comparisonCode: "비교 코드",
  approve: "장치 승인",
  reject: "장치 거절",
  refreshDevices: "장치 목록 새로고침",
  decisionDone: "요청이 처리되었습니다.",
  decisionUnknown: "결과가 확인되지 않았습니다. 이 요청의 작업 ID는 유지됩니다. 다시 연결 후 같은 요청으로 재시도하거나 결과를 확인하세요.",
  reconciliationRequired: "작업 확인이 필요합니다. 결과 확인으로 영수증 상태를 먼저 확인하세요. 자동 재발송은 수행하지 않습니다.",
  decisionPendingBlock: "미확정 승인/거절 요청이 있습니다. 아래 결과 확인 또는 같은 요청으로 재시도로 먼저 해소하세요.",
  reconcileButton: "결과 확인",
  retryButton: "같은 요청으로 재시도",
  revokePrefix: "해제: ",
  revokeUnknown: "해지 결과가 확인되지 않았습니다.",
  resolveButton: "다시 확인",
  revokedResolved: "해지가 확인되었습니다.",
  revokedStillPresent: "장치가 여전히 목록에 있습니다. 명시적으로 다시 시도하세요.",
  degradedTools: "서비스에 연결할 수 없어 관리 동작을 사용할 수 없습니다. 다시 연결 후 시도하세요.",
  pendingPreserved: "결과 미확정 요청이 유지되고 있습니다. 다시 연결 후 확인하세요.",
  lastConfirmed: "마지막 확인",
  reconnectButton: "다시 연결",
} as const;

interface PendingPairing {
  pairingId: string;
  pollingSecret: string;
  code: string;
  expiresAt: number;
}

function readRememberDefault(): boolean {
  const meta = document.querySelector('meta[name="haeryong-session-defaults"]');
  if (!meta) return true;
  const content = meta.getAttribute("content") ?? "";
  return content.includes("remember-device=false") ? false : true;
}

export function ConnectScreen({ controller, state }: { controller: ShellController; state: ShellState }) {
  const [label, setLabel] = useState("Browser");
  const [remember, setRemember] = useState(readRememberDefault);
  const [pending, setPending] = useState<PendingPairing | null>(null);
  const [flowMessage, setFlowMessage] = useState("");
  const [remaining, setRemaining] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [restoreFocusAfterPending, setRestoreFocusAfterPending] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pendingRef = useRef<PendingPairing | null>(null);
  const submitButtonRef = useRef<HTMLButtonElement | null>(null);
  const stopButtonRef = useRef<HTMLButtonElement | null>(null);
  const rememberRef = useRef(remember);
  rememberRef.current = remember;

  const clearTimer = () => {
    if (timer.current !== null) {
      clearTimeout(timer.current);
      timer.current = null;
    }
  };
  const stopWaiting = useCallback((message: string) => {
    clearTimer();
    pendingRef.current = null;
    setPending(null);
    setRemaining("");
    setFlowMessage(message);
    setRestoreFocusAfterPending(true);
  }, []);

  useEffect(() => () => clearTimer(), []);

  useEffect(() => {
    // Disabling the focused submit button during pairing would strand keyboard
    // focus on <body>; move it explicitly to the new Stop waiting control so
    // Escape remains reachable from inside the section.
    if (pending) stopButtonRef.current?.focus();
  }, [pending]);

  useEffect(() => {
    // Focus returns to the request control only after the enabling render.
    if (!pending && restoreFocusAfterPending) {
      setRestoreFocusAfterPending(false);
      submitButtonRef.current?.focus();
    }
  }, [pending, restoreFocusAfterPending]);

  const poll = useCallback(
    async (delay: number) => {
      clearTimer();
      const schedule = (ms: number) => {
        clearTimer();
        timer.current = setTimeout(() => void poll(0), Math.max(2100, ms));
      };
      const current = pendingRef.current;
      if (!current || document.hidden) {
        if (current) schedule(delay);
        return;
      }
      try {
        const result = await controller.pollPairing(current.pairingId, current.pollingSecret);
        if (pendingRef.current !== current) return;
        if (result.token) {
          stopWaiting("");
          await controller.acceptPairing(result.token, rememberRef.current);
        } else if (result.state === "pending") {
          schedule(2100);
        } else if (result.state === "expired") {
          stopWaiting(CONNECT_COPY.expired);
        } else {
          stopWaiting(CONNECT_COPY.rejected);
        }
      } catch (error) {
        if (pendingRef.current !== current) return;
        const retryAfter = errorRetryAfter(error);
        if (retryAfter !== null) {
          schedule(retryAfter * 1000);
          return;
        }
        stopWaiting(CONNECT_COPY.issuanceLost);
      }
    },
    [controller, stopWaiting],
  );

  useEffect(() => {
    const onVisibility = () => {
      if (!document.hidden && pendingRef.current) void poll(0);
    };
    document.addEventListener("visibilitychange", onVisibility);
    return () => document.removeEventListener("visibilitychange", onVisibility);
  }, [poll]);

  useEffect(() => {
    if (!pending) return;
    const tick = () => {
      const left = Math.floor((pending.expiresAt - Date.now()) / 1000);
      setRemaining(left > 0 ? `남은 시간 ${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}` : "만료됨");
    };
    tick();
    const interval = setInterval(tick, 1000);
    return () => clearInterval(interval);
  }, [pending]);

  const onSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (submitting || pending || controller.hasCredential()) return;
    setSubmitting(true);
    setFlowMessage("");
    try {
      const created: PairingCreated = await controller.pair(label);
      const entry: PendingPairing = {
        pairingId: created.pairing_id,
        pollingSecret: created.polling_secret,
        code: created.code,
        expiresAt: Date.parse(created.expires_at),
      };
      pendingRef.current = entry;
      setPending(entry);
      setFlowMessage(CONNECT_COPY.waiting);
      void poll(2100);
    } catch (error) {
      setFlowMessage(errorMessage(error));
    } finally {
      setSubmitting(false);
    }
  };

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "Escape" && pending) {
      event.preventDefault();
      stopWaiting(CONNECT_COPY.stopped);
    }
  };

  const needsPairing = state.phase === "unauthenticated" || state.phase === "checking";
  const ownerTools = state.phase === "verified";
  const degradedTools = state.phase === "degraded" || state.phase === "incompatible";

  return (
    <section className="panel" onKeyDown={onKeyDown}>
      <h1>{CONNECT_COPY.heading}</h1>
      <p className="hint">{CONNECT_COPY.intro}</p>
      <p className="hint">
        {CONNECT_COPY.endpointLabel}: <span className="muted-code">{location.origin}</span>
      </p>
      <p className="hint">{CONNECT_COPY.securityNote}</p>
      <p className="status" role="status" aria-label="연결 상태">
        {state.notice}
      </p>

      {needsPairing && (
        <form onSubmit={onSubmit}>
          <label className="field">
            <span>{CONNECT_COPY.deviceLabel}</span>
            <input
              type="text"
              value={label}
              maxLength={128}
              required
              aria-label={CONNECT_COPY.deviceLabel}
              disabled={submitting || pending !== null}
              onChange={(event) => setLabel(event.target.value)}
            />
          </label>
          <label className="choice">
            <input
              type="checkbox"
              checked={remember}
              aria-label={CONNECT_COPY.rememberLabel}
              disabled={submitting || pending !== null}
              onChange={(event) => setRemember(event.target.checked)}
            />
            <span>{CONNECT_COPY.rememberLabel}</span>
          </label>
          <div className="form-row">
            <button
              type="submit"
              className="primary"
              ref={submitButtonRef}
              disabled={submitting || pending !== null || state.phase === "checking"}
            >
              {pending || submitting ? CONNECT_COPY.waitingButton : CONNECT_COPY.requestButton}
            </button>
            {pending && (
              <button type="button" ref={stopButtonRef} onClick={() => stopWaiting(CONNECT_COPY.stopped)}>
                {CONNECT_COPY.stopWaiting}
              </button>
            )}
          </div>
        </form>
      )}

      {pending && (
        <div>
          <p className="hint">
            요청 ID <span className="muted-code">{pending.pairingId}</span> · {remaining}
          </p>
          <p className="code-display" aria-label="비교 코드">
            {pending.code}
          </p>
        </div>
      )}
      {flowMessage && (
        <p className="status" role="status" aria-label="페어링 진행">
          {flowMessage}
        </p>
      )}

      {ownerTools && <OwnerTools controller={controller} state={state} />}
      {degradedTools && <DegradedTools state={state} />}
    </section>
  );
}

function DegradedTools({ state }: { state: ShellState }) {
  return (
    <div>
      <h2>{CONNECT_COPY.ownerHeading}</h2>
      <p className="status" role="alert">
        {state.phase === "incompatible" ? NOTICE.incompatible : CONNECT_COPY.degradedTools}
      </p>
      <div className="form-row">
        <button type="button" disabled>
          {CONNECT_COPY.statusButton}
        </button>
        <button type="button" disabled>
          {CONNECT_COPY.logoutButton}
        </button>
        <button type="button" disabled>
          {CONNECT_COPY.refreshDevices}
        </button>
      </div>
      {state.session && (
        <p className="hint">
          {CONNECT_COPY.lastConfirmed}: {state.session.device_label} ({state.session.session_id.slice(0, 8)})
        </p>
      )}
      {(state.pendingDecision || state.pendingRevokes.length > 0) && (
        <p className="unknown-note" role="status">
          {CONNECT_COPY.pendingPreserved}
          {state.pendingDecision ? ` — ${CONNECT_COPY.approve}/${CONNECT_COPY.reject} ${state.pendingDecision.request.pairing_id}` : ""}
          {state.pendingRevokes.map((row) => ` — ${CONNECT_COPY.revokePrefix}${row.deviceLabel}`).join("")}
        </p>
      )}
    </div>
  );
}

function OwnerTools({ controller, state }: { controller: ShellController; state: ShellState }) {
  const [privateView, setPrivateView] = useState("");
  const [devices, setDevices] = useState<DeviceSession[]>([]);
  const [resultMessage, setResultMessage] = useState("");
  const [requestId, setRequestId] = useState("");
  const [code, setCode] = useState("");
  const [decisionBusy, setDecisionBusy] = useState(false);
  const [pendingBusy, setPendingBusy] = useState(false);
  const [revokeBusy, setRevokeBusy] = useState<string | null>(null);

  const refreshDevices = useCallback(async () => {
    try {
      setDevices(await controller.listSessions());
    } catch (error) {
      if (controller.state.phase === "verified") setResultMessage(errorMessage(error));
    }
  }, [controller]);

  useEffect(() => {
    if (state.phase === "verified") void refreshDevices();
  }, [state.phase, refreshDevices]);

  const readStatus = async () => {
    try {
      const value = await controller.serviceStatus();
      setPrivateView(JSON.stringify(value, null, 2));
    } catch (error) {
      if (controller.state.phase === "verified") setResultMessage(errorMessage(error));
    }
  };

  const decide = async (action: "approve" | "reject") => {
    if (decisionBusy) return;
    setResultMessage("");
    const trimmedId = requestId.trim();
    if (!trimmedId || !code.trim()) return;
    if (state.pendingDecision) {
      // Preserve the dispatched operation; a new decision must not overwrite it.
      setResultMessage(CONNECT_COPY.decisionPendingBlock);
      return;
    }
    setDecisionBusy(true);
    try {
      const pairings = await controller.pendingPairings();
      const target = pairings.find((item) => item.pairing_id === trimmedId);
      if (!target) throw new Error("Pending request unavailable");
      const operation = crypto.randomUUID();
      const outcome = await controller.decidePairing({
        pairing_id: trimmedId,
        code: code.trim(),
        origin: target.origin,
        etag: target.etag,
        operation_id: operation,
        action,
      });
      if (outcome.outcome === "unknown") {
        setResultMessage(CONNECT_COPY.decisionUnknown);
        return;
      }
      if (outcome.outcome === "reconciliation_required") {
        setResultMessage(CONNECT_COPY.reconciliationRequired);
        return;
      }
      setRequestId("");
      setCode("");
      setResultMessage(CONNECT_COPY.decisionDone);
    } catch (error) {
      setResultMessage(errorMessage(error));
    } finally {
      setDecisionBusy(false);
    }
  };

  const reconcilePending = async () => {
    const pending = controller.state.pendingDecision;
    if (!pending || pendingBusy) return;
    setPendingBusy(true);
    try {
      const result = await controller.reconcileDecision(pending.request.operation_id);
      if (!result) {
        setResultMessage(CONNECT_COPY.decisionUnknown);
        return;
      }
      setResultMessage(
        `${CONNECT_COPY.reconcileButton}: 작업 ${result.operation.state}${result.operation.original_status !== null ? ` (HTTP ${result.operation.original_status})` : ""}`,
      );
    } catch (error) {
      setResultMessage(errorMessage(error));
    } finally {
      setPendingBusy(false);
    }
  };

  const retryPending = async () => {
    if (pendingBusy) return;
    setPendingBusy(true);
    try {
      const outcome = await controller.retryPendingDecision();
      if (!outcome) return;
      if (outcome.outcome === "done") {
        setResultMessage(CONNECT_COPY.decisionDone);
      } else if (outcome.outcome === "reconciliation_required") {
        setResultMessage(CONNECT_COPY.reconciliationRequired);
      } else {
        setResultMessage(CONNECT_COPY.decisionUnknown);
      }
    } catch (error) {
      setResultMessage(errorMessage(error));
    } finally {
      setPendingBusy(false);
    }
  };

  const revoke = async (device: DeviceSession) => {
    if (revokeBusy !== null) return;
    setResultMessage("");
    setRevokeBusy(device.session_id);
    try {
      const outcome = await controller.revokeSession(device.session_id, device.device_label);
      if (outcome === "unknown") {
        setResultMessage(`${device.device_label}: ${CONNECT_COPY.revokeUnknown}`);
        return;
      }
      await refreshDevices();
      setResultMessage(`${device.device_label}: ${CONNECT_COPY.revokedResolved}`);
    } catch (error) {
      setResultMessage(errorMessage(error));
    } finally {
      setRevokeBusy(null);
    }
  };

  const resolveRevoke = async (sessionId: string) => {
    try {
      const resolved = await controller.confirmRevokeResolved(sessionId);
      if (resolved === null) {
        setResultMessage(CONNECT_COPY.revokeUnknown);
        return;
      }
      if (resolved) {
        setResultMessage(CONNECT_COPY.revokedResolved);
        await refreshDevices();
      } else {
        setResultMessage(CONNECT_COPY.revokedStillPresent);
      }
    } catch (error) {
      setResultMessage(errorMessage(error));
    }
  };

  return (
    <div>
      <h2>{CONNECT_COPY.ownerHeading}</h2>
      <p className="hint">
        {state.session?.device_label} · 세션 <span className="muted-code">{state.session?.session_id.slice(0, 8)}</span> ·
        서재 <span className="muted-code">{state.capabilities?.library_id.slice(0, 8)}</span>
      </p>
      <div className="form-row">
        <button type="button" onClick={() => void readStatus()}>
          {CONNECT_COPY.statusButton}
        </button>
        <button type="button" onClick={() => void controller.logout()}>
          {CONNECT_COPY.logoutButton}
        </button>
        <button type="button" onClick={() => void refreshDevices()}>
          {CONNECT_COPY.refreshDevices}
        </button>
      </div>
      {privateView && (
        <pre className="data-readout" aria-label="비공개 상태">
          {privateView}
        </pre>
      )}
      <form
        onSubmit={(event) => {
          event.preventDefault();
        }}
      >
        <h2>정확한 요청 비교</h2>
        <label className="field">
          <span>{CONNECT_COPY.requestId}</span>
          <input
            type="text"
            value={requestId}
            required
            aria-label={CONNECT_COPY.requestId}
            disabled={decisionBusy || !!state.pendingDecision}
            onChange={(event) => setRequestId(event.target.value)}
          />
        </label>
        <label className="field">
          <span>{CONNECT_COPY.comparisonCode}</span>
          <input
            type="text"
            value={code}
            required
            aria-label={CONNECT_COPY.comparisonCode}
            disabled={decisionBusy || !!state.pendingDecision}
            onChange={(event) => setCode(event.target.value)}
          />
        </label>
        <div className="form-row">
          <button type="button" className="primary" disabled={decisionBusy || !!state.pendingDecision} onClick={() => void decide("approve")}>
            {CONNECT_COPY.approve}
          </button>
          <button type="button" disabled={decisionBusy || !!state.pendingDecision} onClick={() => void decide("reject")}>
            {CONNECT_COPY.reject}
          </button>
        </div>
      </form>
      {state.pendingDecision && (
        <p className="unknown-note">
          {state.pendingDecision.request.pairing_id}: {CONNECT_COPY.decisionUnknown}{" "}
          <button type="button" disabled={pendingBusy} onClick={() => void reconcilePending()}>
            {CONNECT_COPY.reconcileButton}
          </button>{" "}
          <button type="button" disabled={pendingBusy} onClick={() => void retryPending()}>
            {CONNECT_COPY.retryButton}
          </button>
        </p>
      )}
      <ul className="device-list" aria-label="연결된 장치">
        {devices.map((device) => {
          const pendingRevoke = state.pendingRevokes.find((row) => row.sessionId === device.session_id);
          return (
            <li key={device.session_id}>
              <span>
                {device.device_label} ({device.state}){pendingRevoke ? ` — ${CONNECT_COPY.revokeUnknown}` : ""}
              </span>
              <span>
                {pendingRevoke && (
                  <button type="button" disabled={revokeBusy !== null} onClick={() => void resolveRevoke(device.session_id)}>
                    {CONNECT_COPY.resolveButton}
                  </button>
                )}{" "}
                <button type="button" disabled={revokeBusy !== null} onClick={() => void revoke(device)}>
                  {CONNECT_COPY.revokePrefix}
                  {device.device_label}
                </button>
              </span>
            </li>
          );
        })}
      </ul>
      {resultMessage && (
        <p className="status" role="status" aria-label="소유자 도구 결과">
          {resultMessage}
        </p>
      )}
    </div>
  );
}

function errorRetryAfter(error: unknown): number | null {
  if (error && typeof error === "object" && "status" in error && (error as { status: number }).status === 429) {
    const retryAfter = (error as { retryAfter?: number }).retryAfter ?? 0;
    return Math.max(retryAfter, 2);
  }
  return null;
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  return "요청이 실패했습니다.";
}
