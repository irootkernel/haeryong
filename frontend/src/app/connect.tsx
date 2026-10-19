// S01 Connect screen: pair/reconnect this device and, once verified, the
// current owner tools (exact-request approve/reject, device list/revoke,
// private status, logout). Writes are offered only in the verified phase;
// disconnected/degraded keeps the credential and shows disabled controls.
import { useCallback, useEffect, useRef, useState } from "react";
import { NATIVE_PAIRING_NOTICES, NOTICE, ShellController, nativeDaemonStateLabel, nativePairingNotice } from "../client/controller.ts";
import type { ShellState } from "../client/controller.ts";
import type { DaemonDiscovery, DeviceSession } from "../client/types.ts";
import { ApiError } from "../client/types.ts";

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
  stopping: "대기 중단 결과를 확인하는 중입니다.",
  stopUnknown: "대기 중단 여부를 확인하지 못했습니다. 연결 상태를 다시 확인하거나 새 코드를 요청하세요.",
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
  /** Browser pairing only: the native Go-held flow never carries a secret. */
  pollingSecret?: string;
  code: string;
  expiresAt: number;
}

function readRememberDefault(): boolean {
  const meta = document.querySelector('meta[name="haeryong-session-defaults"]');
  if (!meta) return true;
  const content = meta.getAttribute("content") ?? "";
  return content.includes("remember-device=false") ? false : true;
}

export const NATIVE_CONNECT_COPY = {
  storageNote: "이 앱은 보호된 로컬 저장소에 연결 자격을 저장합니다. 앱을 다시 실행해도 연결이 유지되며, 로그아웃하면 이 앱에서 정리됩니다.",
  daemonHeading: "데몬 상태",
  refreshDaemon: "데몬 상태 새로고침",
  startDaemon: "데몬 시작",
  startingDaemon: "데몬을 시작하는 중…",
} as const;

export function ConnectScreen({ controller, state }: { controller: ShellController; state: ShellState }) {
  const nativeVariant = controller.nativeSessionAvailable;
  const [label, setLabel] = useState(nativeVariant ? "Desktop" : "Browser");
  const [remember, setRemember] = useState(readRememberDefault);
  const [pending, setPending] = useState<PendingPairing | null>(null);
  const [flowMessage, setFlowMessage] = useState("");
  const [remaining, setRemaining] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [restoreFocusAfterPending, setRestoreFocusAfterPending] = useState(false);
  const [daemon, setDaemon] = useState<DaemonDiscovery | null>(null);
  const [daemonBusy, setDaemonBusy] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pendingRef = useRef<PendingPairing | null>(null);
  const stoppingRef = useRef<PendingPairing | null>(null);
  const adoptingRef = useRef<PendingPairing | null>(null);
  const createIntent = useRef(0);
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

  useEffect(() => () => {
    clearTimer();
    pendingRef.current = null;
    stoppingRef.current = null;
    adoptingRef.current = null;
    createIntent.current++;
  }, []);

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

  const clearPending = useCallback((message: string) => {
    clearTimer();
    pendingRef.current = null;
    setPending(null);
    setRemaining("");
    setFlowMessage(message);
    setRestoreFocusAfterPending(true);
  }, []);

  // An explicit USER stop abandons the Go-held attempt truthfully; local
  // clears (expiry, rejection, completed adoption) never fire an abandon.
  const stopWaiting = useCallback(
    async (message: string) => {
      const current = pendingRef.current;
      if (!current || stoppingRef.current) return;
      clearTimer();
      pendingRef.current = null;
      if (!controller.nativeSessionAvailable) {
        clearPending(message);
        return;
      }
      stoppingRef.current = current;
      setStopping(true);
      setFlowMessage(CONNECT_COPY.stopping);
      try {
        const outcome = await controller.abandonPairing(current.pairingId);
        if (stoppingRef.current !== current) return;
        if (outcome === "already-adopted") {
          clearPending(NATIVE_PAIRING_NOTICES.alreadyAdopted);
          await controller.attachNativeSession();
        } else {
          clearPending(outcome === "adopt-failed" ? NATIVE_PAIRING_NOTICES.adoptFailed : message);
        }
      } catch (error) {
        if (stoppingRef.current === current) clearPending(nativePairingNotice(error) ?? CONNECT_COPY.stopUnknown);
      } finally {
        if (stoppingRef.current === current) {
          stoppingRef.current = null;
          setStopping(false);
        }
      }
    },
    [controller, clearPending],
  );

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
        if ("token" in result && result.token) {
          clearPending("");
          await controller.acceptPairing(result.token, rememberRef.current);
        } else if (result.state === "approved" && controller.nativeSessionAvailable) {
          // Native Go-held redemption: the bearer never crossed the wire, so
          // the still-owned attempt is adopted explicitly and then attaches.
          // A foreground event can legitimately hold a SECOND poll of the
          // same attempt while the first is still in flight; per-attempt
          // adoption ownership keeps exactly one adopt/attach owner so an
          // overlapping approved reply cannot race a duplicate adoption or
          // overwrite the first reply's truthful outcome.
          if (adoptingRef.current === current) return;
          adoptingRef.current = current;
          try {
            await controller.adoptPairing(current.pairingId);
            if (pendingRef.current !== current) return;
            clearPending("");
            await controller.attachNativeSession();
          } catch (adoptError) {
            if (pendingRef.current !== current) return;
            if (adoptError instanceof ApiError && adoptError.code === "NATIVE_PAIRING_ALREADY_ADOPTED") {
              clearPending("");
              await controller.attachNativeSession();
              return;
            }
            clearPending(nativePairingNotice(adoptError) ?? CONNECT_COPY.issuanceLost);
          } finally {
            if (adoptingRef.current === current) adoptingRef.current = null;
          }
        } else if (result.state === "pending") {
          schedule(2100);
        } else if (result.state === "expired") {
          clearPending(CONNECT_COPY.expired);
        } else {
          clearPending(CONNECT_COPY.rejected);
        }
      } catch (error) {
        if (pendingRef.current !== current) return;
        const retryAfter = errorRetryAfter(error);
        if (retryAfter !== null) {
          schedule(retryAfter * 1000);
          return;
        }
        clearPending(nativePairingNotice(error) ?? CONNECT_COPY.issuanceLost);
      }
    },
    [controller, clearPending],
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
    if (submitting || stopping || pending || controller.hasCredential()) return;
    const intent = ++createIntent.current;
    setSubmitting(true);
    setFlowMessage("");
    try {
      const created = await controller.pair(label);
      if (intent !== createIntent.current) return;
      const entry: PendingPairing = {
        pairingId: created.pairing_id,
        // The native Go-held flow carries no polling secret to JavaScript.
        pollingSecret: "polling_secret" in created ? created.polling_secret : undefined,
        code: created.code,
        expiresAt: Date.parse(created.expires_at),
      };
      pendingRef.current = entry;
      setPending(entry);
      setFlowMessage(CONNECT_COPY.waiting);
      void poll(2100);
    } catch (error) {
      if (intent === createIntent.current) setFlowMessage(nativePairingNotice(error) ?? errorMessage(error));
    } finally {
      if (intent === createIntent.current) setSubmitting(false);
    }
  };

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "Escape" && pending) {
      event.preventDefault();
      void stopWaiting(CONNECT_COPY.stopped);
    }
  };

  const needsPairing = state.phase === "unauthenticated" || state.phase === "checking";
  const ownerTools = state.phase === "verified";
  const degradedTools = state.phase === "degraded" || state.phase === "incompatible";

  const refreshDaemon = useCallback(async () => {
    if (!controller.nativeSessionAvailable) return;
    try {
      setDaemon(await controller.serviceDiscovery());
    } catch {
      setDaemon(null);
    }
  }, [controller]);

  useEffect(() => {
    if (nativeVariant) void refreshDaemon();
  }, [nativeVariant, refreshDaemon]);

  const startDaemon = async () => {
    if (daemonBusy) return;
    setDaemonBusy(true);
    try {
      const result = await controller.startDaemonOffer();
      setDaemon(result.discovery);
      if (result.discovery.state === "verified") {
        await controller.attachNativeSession();
      }
    } catch (error) {
      setFlowMessage(nativePairingNotice(error) ?? errorMessage(error));
    } finally {
      setDaemonBusy(false);
    }
  };

  return (
    <section className="panel" onKeyDown={onKeyDown}>
      <h1>{CONNECT_COPY.heading}</h1>
      <p className="hint">{nativeVariant ? "이 앱을 개인 서재에 연결합니다." : CONNECT_COPY.intro}</p>
      <p className="hint">
        {CONNECT_COPY.endpointLabel}: <span className="muted-code" data-testid="connect-endpoint">{nativeVariant ? controller.client.publicOrigin ?? daemon?.endpoint ?? "서비스 연결 정보를 확인하세요." : location.origin}</span>
      </p>
      <p className="hint">{nativeVariant ? NATIVE_CONNECT_COPY.storageNote : CONNECT_COPY.securityNote}</p>
      <p className="status" role="status" aria-label="연결 상태">
        {state.notice}
      </p>
      {state.localCleanupPending && <button type="button" onClick={() => void controller.retryLocalCleanup()}>연결 자격 정리 다시 시도</button>}

      {nativeVariant && (
        <div>
          <h2>{NATIVE_CONNECT_COPY.daemonHeading}</h2>
          <p className="hint" role="status" data-testid="daemon-state">
            {daemon ? nativeDaemonStateLabel(daemon.state) : "데몬 상태를 알 수 없습니다."}
          </p>
          <div className="form-row">
            <button type="button" onClick={() => void refreshDaemon()} disabled={daemonBusy}>
              {NATIVE_CONNECT_COPY.refreshDaemon}
            </button>
            {daemon?.state === "daemon_stopped" && (
              <button type="button" className="primary" onClick={() => void startDaemon()} disabled={daemonBusy}>
                {daemonBusy ? NATIVE_CONNECT_COPY.startingDaemon : NATIVE_CONNECT_COPY.startDaemon}
              </button>
            )}
          </div>
        </div>
      )}

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
          {!nativeVariant && (
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
          )}
          <div className="form-row">
            <button
              type="submit"
              className="primary"
              ref={submitButtonRef}
              disabled={submitting || stopping || pending !== null || state.phase === "checking"}
            >
              {pending || submitting ? CONNECT_COPY.waitingButton : CONNECT_COPY.requestButton}
            </button>
            {pending && (
              <button type="button" ref={stopButtonRef} disabled={stopping} onClick={() => void stopWaiting(CONNECT_COPY.stopped)}>
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
