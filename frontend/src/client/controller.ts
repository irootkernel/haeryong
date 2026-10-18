// Shell connection state machine. Phases:
//   checking        - a credential exists and is being verified
//   unauthenticated - no usable credential; pair this device
//   verified        - current session AND understood capabilities confirmed
//   degraded        - credential retained but session/capabilities unconfirmed
//   incompatible    - service contract (api_version/library identity) not understood
// Writes are only offered in `verified`. Responses from an earlier
// credential/library/generation run are discarded, never applied.
import type { Capabilities, CredentialAware, DeviceSession, HaeryongClient, OperationView, PairingCreated, PairingDecisionRequest, PairingPoll, PairingView, SessionInfo } from "./types.ts";
import { ApiError, DispatchBlockedError, NetworkError, StaleResponseError, SUPPORTED_API_VERSION, UncertainResponseError } from "./types.ts";
import type { CredentialStore } from "./store.ts";

export type ShellPhase = "checking" | "unauthenticated" | "verified" | "degraded" | "incompatible";

export interface ShellState {
  phase: ShellPhase;
  /** Current confirmed session (verified) or last confirmed snapshot (degraded). */
  session: SessionInfo | null;
  capabilities: Capabilities | null;
  memoryOnly: boolean;
  storageAvailable: boolean;
  notice: string;
  /** Unresolved write intents kept in owner-scope memory across UI phase changes. */
  pendingDecision: PendingDecision | null;
  pendingRevokes: PendingRevoke[];
  localCleanupPending: boolean;
}

export const NOTICE = {
  unauthenticated: "이 브라우저는 아직 연결되지 않았습니다.",
  checking: "연결 상태를 확인하는 중입니다.",
  verifiedRemembered: "기억된 연결로 접속했습니다.",
  verifiedMemory: "이번 세션에만 연결되었습니다. 페이지를 다시 열면 재연결이 필요합니다.",
  disconnected: "서비스에 연결할 수 없습니다. 연결 자격은 유지됩니다. 다시 연결을 시도하세요.",
  serviceRecovering: "서비스가 일시적으로 응답하지 않습니다. 잠시 후 다시 확인합니다.",
  incompatible: "지원하지 않는 서비스 계약입니다. 이 서비스와는 안전하게 사용할 수 없습니다.",
  generationChanged: "서비스 세대가 변경되어 확인된 상태를 다시 불러옵니다.",
  revoked: "이 장치의 연결이 해제되었습니다. 다시 연결하세요.",
  expired: "연결이 만료되었습니다. 다시 연결하세요.",
  invalidCredential: "저장된 연결 자격이 유효하지 않습니다. 다시 연결하세요.",
  logoutLocal: "이 브라우저에서 로그아웃했습니다.",
  logoutConfirmed: "로그아웃이 서버에서 확인되었습니다.",
  logoutAlreadyRevoked: "서버 세션은 이미 해지되어 있었습니다.",
  logoutAckUnknown: "로그아웃했습니다. 서버 해지 여부는 확인되지 않았습니다.",
  logoutNativeLocal: "이 앱의 연결을 해제하고 저장된 연결 자격을 정리하는 중입니다.",
  logoutLocalFailure: "저장된 연결 자격을 지우지 못했습니다. 앱을 다시 시작하면 재접속될 수 있습니다. 연결 자격 정리를 다시 시도하세요. 서버 해지 여부는 확인되지 않았습니다.",
  externalRemoval: "이 브라우저의 저장된 연결이 제거되었습니다. 다시 연결하세요.",
} as const;

export type DecisionOutcome =
  | { outcome: "done" }
  | { outcome: "unknown"; operation: string }
  | { outcome: "reconciliation_required"; operation: string };
export type RevokeOutcome = "revoked" | "unknown";

/** An undecided K-operation pairing decision kept for receipt recovery. */
export interface PendingDecision {
  request: PairingDecisionRequest;
  libraryId: string;
  observed: number;
}

/** Authority identity a screen flow captured before its async preparation. */
export interface DispatchAuthority {
  nonce: number;
  libraryId: string;
  runtimeGeneration: string;
}

/** A revoke whose server outcome is unknown; natural session_id identity. */
export interface PendingRevoke {
  sessionId: string;
  deviceLabel: string;
  libraryId: string;
  observed: number;
}

export interface ShellControllerOptions {
  recheckIntervalMs?: number;
}

export class ShellController {
  readonly #client: HaeryongClient & CredentialAware;
  readonly #store: CredentialStore;
  readonly #recheckIntervalMs: number;
  #listeners = new Set<(state: ShellState) => void>();
  #state: ShellState;
  #token = "";
  #nativeAttachment: string | null = null;
  #run = 0;
  #generation = "";
  #libraryId = "";
  #boundCredential = "";
  #authority = 0;
  #events: { close(): void } | null = null;
  #eventsRecheckAt = 0;
  #eventsTimer: ReturnType<typeof setTimeout> | null = null;
  #timer: ReturnType<typeof setInterval> | null = null;

  constructor(client: HaeryongClient & CredentialAware, store: CredentialStore, options: ShellControllerOptions = {}) {
    this.#client = client;
    this.#store = store;
    this.#recheckIntervalMs = options.recheckIntervalMs ?? 4000;
    this.#state = {
      phase: "unauthenticated",
      session: null,
      capabilities: null,
      memoryOnly: false,
      storageAvailable: store.storageAvailable,
      notice: NOTICE.unauthenticated,
      pendingDecision: null,
      pendingRevokes: [],
      localCleanupPending: false,
    };
  }

  get state(): ShellState {
    return this.#state;
  }

  /** Shared transport client for screen data flows (read-only handle). */
  get client(): HaeryongClient & CredentialAware {
    return this.#client;
  }

  /**
   * Synchronous verified-authority gate for screen-initiated protected
   * dispatches: never runs in a completion path, and an unverified or
   * replaced authority blocks the dispatch before anything is sent. A flow
   * that captured an authority token at its start must pass it back; a
   * verified phase under a DIFFERENT owner/library/generation does not
   * unblock the old flow.
   */
  requireVerifiedDispatch(captured?: DispatchAuthority | null): void {
    if (this.#state.phase !== "verified" || captured === null) {
      throw new DispatchBlockedError();
    }
    if (
      captured &&
      (captured.nonce !== this.#authority || captured.libraryId !== this.#libraryId || captured.runtimeGeneration !== this.#generation)
    ) {
      throw new DispatchBlockedError();
    }
  }

  /** Authority token to hold across one flow's asynchronous preparation. */
  captureDispatchAuthority(): DispatchAuthority | null {
    if (this.#state.phase !== "verified") return null;
    return { nonce: this.#authority, libraryId: this.#libraryId, runtimeGeneration: this.#generation };
  }

  hasCredential(): boolean {
    return this.#nativeAttachment !== null || this.#token !== "" || this.#store.read() !== null;
  }

  subscribe(listener: (state: ShellState) => void): () => void {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  #apply(patch: Partial<ShellState>): void {
    this.#state = { ...this.#state, ...patch };
    for (const listener of this.#listeners) listener(this.#state);
  }

  #clearCredential(notice: string, keepNativeTransport = false): void {
    const native = this.#nativeAttachment !== null;
    this.#nativeAttachment = null;
    this.#token = "";
    this.#store.clear();
    this.#generation = "";
    this.#libraryId = "";
    this.#boundCredential = "";
    // Drop the transport binding immediately: responses pending from the
    // cleared authority must be invalidated before anything else rebinds.
    let disposal: Promise<void> | undefined;
    if (!native || !keepNativeTransport) {
      this.#client.setCredential("");
      this.#client.invalidate();
      if (native) disposal = this.#client.disposeSession?.();
    }
    // Every pending intent belongs to the credential/library authority that
    // dispatched it; late failures from a replaced authority never reinsert.
    this.#authority += 1;
    const clearedAuthority = this.#authority;
    void disposal?.catch(() => {
      if (clearedAuthority === this.#authority) this.#apply({ localCleanupPending: true, notice: NOTICE.logoutLocalFailure });
    });
    this.#run += 1;
    this.#closeEvents();
    this.#apply({
      phase: "unauthenticated",
      session: null,
      capabilities: null,
      memoryOnly: false,
      notice,
      pendingDecision: null,
      pendingRevokes: [],
    });
  }

  #failure(error: unknown, run: number): void {
    if (run !== this.#run || error instanceof StaleResponseError) return;
    if (error instanceof NetworkError) {
      // Credential retained; last confirmed data stays visible as last-confirmed.
      this.#apply({ phase: "degraded", notice: NOTICE.disconnected });
      return;
    }
    if (error instanceof ApiError) {
      if (error.status === 401) {
        if (error.code === "SESSION_REVOKED") {
          this.#clearCredential(NOTICE.revoked);
          return;
        }
        if (error.code === "SESSION_EXPIRED") {
          this.#clearCredential(NOTICE.expired);
          return;
        }
        this.#clearCredential(NOTICE.invalidCredential);
        return;
      }
      this.#apply({ phase: "degraded", notice: NOTICE.serviceRecovering });
      return;
    }
    this.#apply({ phase: "degraded", notice: NOTICE.disconnected });
  }

  get nativeSessionAvailable(): boolean { return typeof this.#client.attachSession === "function"; }

  async attachNativeSession(): Promise<void> {
    if (!this.#client.attachSession) throw new Error("Native session attachment is unavailable.");
    const run = ++this.#run;
    try {
      const attached = await this.#client.attachSession();
      if (run !== this.#run) return;
      if (this.#nativeAttachment !== attached.attachment_id) {
        this.#nativeAttachment = attached.attachment_id;
        this.#token = ""; this.#boundCredential = "";
        this.#authority++; this.#client.invalidate(); this.#closeEvents();
        this.#apply({ phase: "checking", notice: NOTICE.checking, localCleanupPending: false });
      }
      await this.verify();
    } catch (error) { this.#failure(error, run); }
  }

  async verify(): Promise<void> {
    const native = this.#nativeAttachment !== null;
    const token = native ? "" : this.#token || this.#store.read() || "";
    if (!native && !token) {
      this.#run += 1;
      this.#clearEventsAndGeneration();
      this.#apply({
        phase: "unauthenticated",
        session: null,
        capabilities: null,
        memoryOnly: false,
        notice: this.#state.localCleanupPending ? NOTICE.logoutLocalFailure : NOTICE.unauthenticated,
      });
      return;
    }
    const remembered = native || this.#store.read() === token;
    this.#token = token;
    // The transport authority era changes only when the bound credential
    // actually changes. Routine same-authority rechecks keep the current era
    // so in-flight writes are not discarded mid-flight.
    const rebinding = !native && token !== this.#boundCredential;
    if (rebinding) {
      this.#client.setCredential(token);
      this.#client.invalidate();
      this.#boundCredential = token;
      this.#authority += 1;
    }
    const run = ++this.#run;
    // `checking` is an initial/new-authority loading state. A same-authority
    // recheck keeps the verified/degraded/incompatible screen mounted.
    if (this.#state.phase === "unauthenticated" || rebinding) {
      this.#apply({ phase: "checking", notice: NOTICE.checking });
    }
    let session: SessionInfo;
    try {
      session = await this.#client.currentSession();
    } catch (error) {
      this.#failure(error, run);
      return;
    }
    if (run !== this.#run) return;
    let capabilities: Capabilities;
    try {
      capabilities = await this.#client.capabilities();
    } catch (error) {
      this.#failure(error, run);
      return;
    }
    if (run !== this.#run) return;
    if (capabilities.api_version !== SUPPORTED_API_VERSION || capabilities.library_id !== session.library_id) {
      this.#closeEvents();
      this.#apply({
        phase: "incompatible",
        session: null,
        capabilities,
        notice: NOTICE.incompatible,
      });
      return;
    }
    const generationChanged = this.#generation !== "" && capabilities.runtime_generation !== this.#generation;
    // A different confirmed library may reuse the same runtime_generation
    // string, so either boundary invalidates pending transport responses.
    // Routine same-authority rechecks never invalidate in-flight writes.
    const libraryChanged = this.#libraryId !== "" && this.#libraryId !== capabilities.library_id;
    if (generationChanged || libraryChanged) {
      this.#client.invalidate();
    }
    if (libraryChanged) {
      this.#authority += 1;
    }
    this.#generation = capabilities.runtime_generation;
    this.#libraryId = capabilities.library_id;
    const pendingDecision =
      this.#state.pendingDecision && this.#state.pendingDecision.libraryId === capabilities.library_id
        ? this.#state.pendingDecision
        : null;
    const pendingRevokes = this.#state.pendingRevokes.filter((row) => row.libraryId === capabilities.library_id);
    this.#apply({
      phase: "verified",
      session,
      capabilities,
      memoryOnly: !remembered,
      notice: generationChanged ? NOTICE.generationChanged : remembered ? NOTICE.verifiedRemembered : NOTICE.verifiedMemory,
      pendingDecision,
      pendingRevokes,
    });
    if (generationChanged) {
      // Drop the previous generation's confirmed data by re-verifying once.
      this.#closeEvents();
      void this.verify();
      return;
    }
    if (!this.#events) {
      this.#openEvents();
    }
  }

  #clearEventsAndGeneration(): void {
    this.#closeEvents();
    this.#generation = "";
    this.#libraryId = "";
  }

  #openEvents(): void {
    this.#closeEvents();
    const subscription = this.#client.subscribeEvents();
    this.#events = subscription;
    // Guard by lifecycle identity, not the verification-run counter: routine
    // same-authority rechecks keep this subscription current, while a stop,
    // authority replacement or subscription replacement invalidates it.
    const authority = this.#authority;
    void subscription.closed.then(() => {
      if (this.#events !== subscription) return;
      this.#events = null;
      if (this.#state.phase !== "verified") return;
      if (authority !== this.#authority) return;
      // Liveness only: recheck confirmed state with a bounded backoff so a
      // service that closes streams instantly cannot create a busy loop.
      const wait = 2000 - (Date.now() - this.#eventsRecheckAt);
      if (wait <= 0) {
        this.#eventsRecheckAt = Date.now();
        void this.verify();
        return;
      }
      if (this.#eventsTimer !== null) return;
      this.#eventsTimer = setTimeout(() => {
        this.#eventsTimer = null;
        if (this.#events !== null && this.#events !== subscription) return;
        if (this.#state.phase !== "verified") return;
        if (authority !== this.#authority) return;
        this.#eventsRecheckAt = Date.now();
        void this.verify();
      }, wait);
    });
  }

  #closeEvents(): void {
    this.#events?.close();
    this.#events = null;
    if (this.#eventsTimer !== null) {
      clearTimeout(this.#eventsTimer);
      this.#eventsTimer = null;
    }
  }

  async acceptPairing(token: string, remember: boolean): Promise<void> {
    this.#token = token;
    if (remember) {
      // store.write removes any previously remembered token first: a failed
      // write must leave nothing behind to silently restore on reload.
      this.#store.write(token);
    } else {
      this.#store.clear();
    }
    await this.verify();
  }

  async externalCredential(token: string): Promise<void> {
    if (this.#token) return;
    this.#token = token;
    await this.verify();
  }

  externalRemoval(): void {
    if (!this.#store.read()) {
      this.#clearCredential(NOTICE.externalRemoval);
    }
  }

  /** Local logout: clear this browser immediately, then seek a server ack once. */
  async logout(): Promise<void> {
    const token = this.#token;
    const native = this.#nativeAttachment !== null;
    this.#clearCredential(native ? NOTICE.logoutNativeLocal : NOTICE.logoutLocal, native);
    // Guard post-await effects by authority identity, not the verification-run
    // counter: a no-token verify() during the pending revoke bumps #run without
    // replacing the session and must not leave the old token transport-bound.
    const authority = this.#authority;
    if (!native && !token) return;
    if (!native) this.#client.setCredential(token);
    try {
      await this.#client.revokeOwnSession();
      if (authority === this.#authority) {
        this.#apply({ notice: NOTICE.logoutConfirmed });
      }
    } catch (error) {
      if (authority === this.#authority) {
        // Only an explicit SESSION_REVOKED for this credential proves server-side
        // revocation. Expiry, generic 401s and lost responses stay honestly unknown.
        if (native && error instanceof ApiError && error.code === "NATIVE_LOCAL_FORGET_FAILED") {
          this.#apply({ notice: NOTICE.logoutLocalFailure, localCleanupPending: true });
        } else if (error instanceof ApiError && error.status === 401 && error.code === "SESSION_REVOKED") {
          this.#apply({ notice: NOTICE.logoutAlreadyRevoked });
        } else {
          this.#apply({ notice: NOTICE.logoutAckUnknown });
        }
      }
    } finally {
      // Drop the transport credential unless a genuinely new authority (a new
      // binding or another clear) has taken it over.
      if (authority === this.#authority) {
        if (native) {
          try {
            await this.#client.disposeSession?.();
            if (authority === this.#authority && this.#state.localCleanupPending) this.#apply({ localCleanupPending: false, notice: NOTICE.logoutAckUnknown });
          } catch {
            if (authority === this.#authority) this.#apply({ localCleanupPending: true, notice: NOTICE.logoutLocalFailure });
          }
        }
        if (authority !== this.#authority) return;
        this.#client.setCredential("");
        this.#client.invalidate();
      }
    }
  }

  async retryLocalCleanup(): Promise<void> {
    if (!this.#state.localCleanupPending) return;
    const authority = this.#authority;
    try {
      await this.#client.disposeSession?.();
      if (authority === this.#authority) this.#apply({ localCleanupPending: false, notice: NOTICE.logoutAckUnknown });
    } catch {
      if (authority === this.#authority) this.#apply({ notice: NOTICE.logoutLocalFailure });
    }
  }

  async decidePairing(request: PairingDecisionRequest): Promise<DecisionOutcome> {
    // Dispatch boundary: a NEW protected decision requires the currently
    // confirmed verified phase, including explicit retries through this entry.
    // Already-dispatched requests keep their recovery paths below; this guard
    // never runs in a completion path.
    if (this.#state.phase !== "verified") {
      throw new DispatchBlockedError();
    }
    // Capture the authority and the dispatch-time CONFIRMED library identity
    // before awaiting. #libraryId only advances on verified passes; the state
    // capabilities field can transiently hold unverified incompatible data.
    const authority = this.#authority;
    const libraryId = this.#libraryId;
    const recordPending = () => {
      if (authority !== this.#authority) {
        // No longer ours to track; the server-side receipt remains the only
        // recovery record for the replaced authority.
        return false;
      }
      this.#apply({
        pendingDecision: {
          request,
          libraryId,
          observed: Date.now(),
        },
      });
      return true;
    };
    try {
      const result = await this.#client.decidePairing(request);
      if (result.kind === "accepted") {
        // Canonical HTTP 202 pending reservation: the operation is accepted
        // but not applied; its outcome stays pending, never success.
        recordPending();
        return { outcome: "unknown", operation: request.operation_id };
      }
      if (this.#state.pendingDecision?.request.operation_id === request.operation_id) {
        this.#apply({ pendingDecision: null });
      }
      return { outcome: "done" };
    } catch (error) {
      if (error instanceof ApiError && error.status === 409 && error.code === "OPERATION_RECONCILIATION_REQUIRED") {
        // The receipt must be reconciled first; no automatic redispatch and
        // no new operation identity.
        recordPending();
        return { outcome: "reconciliation_required", operation: request.operation_id };
      }
      if (error instanceof NetworkError || error instanceof StaleResponseError || error instanceof UncertainResponseError) {
        // Keep the original K operation ID, request and preconditions for
        // receipt recovery or an explicit same-intent retry. A stale-era ack
        // or unreadable success body may still have committed server-side:
        // never claim failure or success.
        recordPending();
        return { outcome: "unknown", operation: request.operation_id };
      }
      throw error;
    }
  }

  /** Explicit same-intent retry: the same Idempotency-Key and original If-Match. */
  async retryPendingDecision(): Promise<DecisionOutcome | null> {
    const pending = this.#state.pendingDecision;
    if (!pending) return null;
    return this.decidePairing(pending.request);
  }

  /** Thin transport pass-throughs used by the pairing flow and owner tools. */
  pair(deviceLabel: string): Promise<PairingCreated> {
    return this.#client.pair(deviceLabel);
  }

  pollPairing(pairingId: string, pollingSecret: string): Promise<PairingPoll> {
    return this.#client.pollPairing(pairingId, pollingSecret);
  }

  listSessions(): Promise<DeviceSession[]> {
    return this.#client.listSessions();
  }

  pendingPairings(): Promise<PairingView[]> {
    return this.#client.pendingPairings();
  }

  serviceStatus(): Promise<unknown> {
    return this.#client.serviceStatus();
  }

  /** K-operation receipt reconciliation for an undecided pairing decision. */
  async reconcileDecision(
    operationId: string,
  ): Promise<{ operation: OperationView; pairings: PairingView[] } | null> {
    if (this.#state.phase !== "verified") return null;
    try {
      const [operation, pairings] = await Promise.all([this.#client.operation(operationId), this.#client.pendingPairings()]);
      // Canonical receipt states are "completed" and "failed" (terminal) and
      // "reconciling"/pending (still unresolved). Only a terminal receipt
      // resolves the uncertain intent.
      if (
        this.#state.pendingDecision?.request.operation_id === operationId &&
        (operation.state === "completed" || operation.state === "failed")
      ) {
        this.#apply({ pendingDecision: null });
      }
      return { operation, pairings };
    } catch {
      return null;
    }
  }

  async revokeSession(sessionId: string, deviceLabel = ""): Promise<RevokeOutcome> {
    // Dispatch boundary: same synchronous verified-phase requirement as
    // decidePairing; never applied in a completion path.
    if (this.#state.phase !== "verified") {
      throw new DispatchBlockedError();
    }
    // Dispatch-time confirmed library identity (#libraryId); see decidePairing.
    const authority = this.#authority;
    const libraryId = this.#libraryId;
    try {
      await this.#client.revokeSession(sessionId);
      this.#dropPendingRevoke(sessionId);
      return "revoked";
    } catch (error) {
      if (error instanceof NetworkError || error instanceof StaleResponseError || error instanceof UncertainResponseError) {
        if (authority !== this.#authority) {
          // Natural session_id identity survives, but a replaced authority
          // must not track the previous owner's uncertain revoke.
          return "unknown";
        }
        this.#apply({
          pendingRevokes: [
            ...this.#state.pendingRevokes.filter((row) => row.sessionId !== sessionId),
            { sessionId, deviceLabel, libraryId, observed: Date.now() },
          ],
        });
        return "unknown";
      }
      throw error;
    }
  }

  #dropPendingRevoke(sessionId: string): void {
    if (this.#state.pendingRevokes.some((row) => row.sessionId === sessionId)) {
      this.#apply({ pendingRevokes: this.#state.pendingRevokes.filter((row) => row.sessionId !== sessionId) });
    }
  }

  /**
   * Natural-identity resolution for an uncertain revoke under current verified
   * authority. Production session lists keep revoked/expired rows: the target
   * is confirmed revoked when it is absent or its row state is "revoked". An
   * "active" row stays unresolved, and "expired"/unknown states never count
   * as proven revoked.
   */
  async confirmRevokeResolved(sessionId: string): Promise<boolean | null> {
    if (this.#state.phase !== "verified") return null;
    const sessions = await this.#client.listSessions();
    const row = sessions.find((item) => item.session_id === sessionId);
    const resolved = row === undefined || row.state === "revoked";
    if (resolved) this.#dropPendingRevoke(sessionId);
    return resolved;
  }

  /** Periodic recheck used while a retained credential is unconfirmed. */
  start(): void {
    if (this.#timer !== null) return;
    this.#timer = setInterval(() => {
      if (this.#state.phase === "checking" || !this.hasCredential()) return;
      void this.verify();
    }, this.#recheckIntervalMs);
  }

  stop(): void {
    if (this.#timer !== null) {
      clearInterval(this.#timer);
      this.#timer = null;
    }
    this.#closeEvents();
  }
}
