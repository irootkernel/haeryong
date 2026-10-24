// Shell connection state machine. Phases:
//   checking        - a credential exists and is being verified
//   unauthenticated - no usable credential; pair this device
//   verified        - current session AND understood capabilities confirmed
//   degraded        - credential retained but session/capabilities unconfirmed
//   incompatible    - service contract (api_version/library identity) not understood
// Writes are only offered in `verified`. Responses from an earlier
// credential/library/generation run are discarded, never applied.
import type { Capabilities, CredentialAware, DaemonDiscovery, DaemonStartOffer, DeviceSession, HaeryongClient, NativePairingCreated, NativePairingPoll, OperationView, PairingAdoption, PairingCreated, PairingDecisionRequest, PairingPoll, PairingView, SessionInfo } from "./types.ts";
import { ApiError, DispatchBlockedError, NetworkError, StaleResponseError, SUPPORTED_API_VERSION, UncertainResponseError } from "./types.ts";
import { SourceWriteIntents } from "./source-write-intents.ts";
import type { CredentialStore } from "./store.ts";

export type ShellPhase = "checking" | "unauthenticated" | "verified" | "degraded" | "incompatible";

/** Truthful native pairing/adoption failure notices (Korean UI copy). */
export const NATIVE_PAIRING_NOTICES = {
  stale: "연결 시도가 무효화되었습니다. 다시 연결하세요.",
  alreadyAdopted: "연결이 이미 완료되었습니다.",
  adoptFailed: "연결 채택이 실패하여 중단으로 처리하지 못했습니다. 화면 상태를 확인하세요.",
  storeFailed: "연결 자격을 저장하지 못했습니다. 저장되지 않은 상태이니 새로 연결을 시작해야 합니다.",
  storeUncertain: "연결 자격 저장 결과를 확인할 수 없습니다. 앱을 다시 시작하면 자동으로 연결되며, 저장되지 않았다면 다시 연결해야 합니다.",
  libraryMismatch: "연결하려는 서재가 이 프로필과 일치하지 않아 연결하지 않았습니다.",
  apiIncompatible: "지원하지 않는 서비스 계약입니다. 이 서비스와는 안전하게 사용할 수 없습니다.",
  credentialMismatched: "저장된 연결 자격이 현재 서비스 결속과 일치하지 않습니다. 다시 연결하세요.",
  daemonUnavailable: "로컬 서비스에 연결할 수 없습니다. 데몬 상태를 확인한 뒤 다시 시도하세요.",
} as const;

/**
 * Maps a native pairing/poll/adoption ApiError to its truthful notice, at
 * BOTH poll and adoption time. Returns null for unmapped errors so callers
 * keep their existing generic handling (e.g. issuanceLost).
 */
export function nativePairingNotice(error: unknown): string | null {
  if (!(error instanceof ApiError)) return null;
  switch (error.code) {
    case "NATIVE_PAIRING_ATTEMPT_STALE": return NATIVE_PAIRING_NOTICES.stale;
    case "NATIVE_PAIRING_ALREADY_ADOPTED": return NATIVE_PAIRING_NOTICES.alreadyAdopted;
    case "NATIVE_PAIRING_ADOPT_FAILED": return NATIVE_PAIRING_NOTICES.adoptFailed;
    case "NATIVE_CREDENTIAL_STORE_FAILED": return NATIVE_PAIRING_NOTICES.storeFailed;
    case "NATIVE_CREDENTIAL_STORE_UNCERTAIN": return NATIVE_PAIRING_NOTICES.storeUncertain;
    case "NATIVE_LIBRARY_MISMATCH": return NATIVE_PAIRING_NOTICES.libraryMismatch;
    case "NATIVE_API_INCOMPATIBLE": return NATIVE_PAIRING_NOTICES.apiIncompatible;
    case "NATIVE_CREDENTIAL_MISMATCHED": return NATIVE_PAIRING_NOTICES.credentialMismatched;
    case "NATIVE_DAEMON_UNAVAILABLE": return NATIVE_PAIRING_NOTICES.daemonUnavailable;
    default: return null;
  }
}

/** Korean labels for the signed local service discovery states. */
export function nativeDaemonStateLabel(state: string): string {
  switch (state) {
    case "verified": return "로컬 서비스가 확인되었습니다.";
    case "uninitialized": return "이 프로필은 아직 초기화되지 않았습니다. CLI로 `haeryong init`을 실행한 뒤 다시 시도하세요.";
    case "daemon_stopped": return "로컬 데몬이 실행 중이 아닙니다.";
    case "discovery_corrupt": return "로컬 서비스 기록을 신뢰할 수 없습니다. 프로필을 확인하세요.";
    case "identity_mismatch": return "로컬 서비스 신원이 서명된 결속과 일치하지 않습니다.";
    case "profile_error": return "프로필을 읽을 수 없습니다.";
    case "FEATURE_NOT_READY": return "이 빌드에서는 데몬 상태 확인을 제공하지 않습니다.";
    default: return "데몬 상태를 알 수 없습니다.";
  }
}

export interface ShellState {
  phase: ShellPhase;
  /** Current confirmed session (verified) or last confirmed snapshot (degraded). */
  session: SessionInfo | null;
  capabilities: Capabilities | null;
  memoryOnly: boolean;
  storageAvailable: boolean;
  notice: string;
  /**
   * Runtime-only informational cleanup alerts (M3 C2): exact surviving
   * destination temp paths whose cleanup failed after the owning panel was
   * gone. Deduplicated by path, never persisted, never claiming the cleanup
   * done; cleared at every authority transition.
   */
  cleanupAlerts: string[];
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
  readonly #sourceWrites = new Map<string, SourceWriteIntents>();
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
  #invalidationListeners = new Set<() => void>();
  #eventInvalidationOff: (() => void) | undefined;
  #eventsRecheckAt = 0;
  #eventsTimer: ReturnType<typeof setTimeout> | null = null;
  #timer: ReturnType<typeof setInterval> | null = null;
  #attaching = false;

  /**
   * Informational runtime cleanup alert (M3 C2): a destination temp whose
   * cleanup failed after the owning panel was already gone. NOT a protected
   * dispatch and NOT an authority grant: the check compares the captured
   * authority identity (nonce/library/generation) NON-THROWINGLY and simply
   * drops a report whose era was replaced, so a fire-and-forget catch can
   * never turn into an unhandled rejection. A temporarily degraded or
   * checking phase under the SAME authority still receives the alert — the
   * surviving path must stay visible to the owner. Duplicate paths dedupe;
   * no fixed content limit, no handles, no transfer state.
   */
  reportRuntimeCleanupFailure(failure: { code?: string; details?: { temp?: string } }, captured: DispatchAuthority | null): boolean {
    if (captured === null) return false;
    if (
      captured.nonce !== this.#authority ||
      captured.libraryId !== this.#libraryId ||
      captured.runtimeGeneration !== this.#generation
    ) {
      return false;
    }
    const tempPath = typeof failure?.details?.temp === "string" && failure.details.temp !== "" ? failure.details.temp : "";
    if (!this.#state.cleanupAlerts.includes(tempPath)) {
      this.#apply({ cleanupAlerts: [...this.#state.cleanupAlerts, tempPath] });
    }
    return true;
  }

  /** Owner dismissal of one informational cleanup alert; never claims the cleanup done. */
  dismissRuntimeCleanupAlert(tempPath: string): void {
    if (this.#state.cleanupAlerts.includes(tempPath)) {
      this.#apply({ cleanupAlerts: this.#state.cleanupAlerts.filter((path) => path !== tempPath) });
    }
  }

  constructor(client: HaeryongClient & CredentialAware, store: CredentialStore, options: ShellControllerOptions = {}) {
    this.#client = client;
    this.#store = store;
    this.#recheckIntervalMs = options.recheckIntervalMs ?? 4000;
    // Native startup must await the Go-held session before declaring an
    // authentication loss. Otherwise a reload becomes a new Connect entry.
    const checkingNativeSession = typeof client.attachSession === "function";
    this.#state = {
      phase: checkingNativeSession ? "checking" : "unauthenticated",
      session: null,
      capabilities: null,
      memoryOnly: false,
      storageAvailable: store.storageAvailable,
      notice: checkingNativeSession ? NOTICE.checking : NOTICE.unauthenticated,
      cleanupAlerts: [],
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

  /** Same library/principal survives a runtime generation, never crosses owners. */
  sourceWriteIntents(): SourceWriteIntents {
    const libraryId = this.#state.capabilities?.library_id;
    const principalId = this.#state.session?.principal_id;
    if (!libraryId || !principalId || this.#state.session?.library_id !== libraryId) throw new DispatchBlockedError();
    const key = JSON.stringify([libraryId, principalId]);
    let intents = this.#sourceWrites.get(key);
    if (!intents) { intents = new SourceWriteIntents(); this.#sourceWrites.set(key, intents); }
    return intents;
  }

  hasCredential(): boolean {
    return this.#nativeAttachment !== null || this.#token !== "" || this.#store.read() !== null;
  }

  subscribe(listener: (state: ShellState) => void): () => void {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  subscribeInvalidation(listener: () => void): () => void {
    this.#invalidationListeners.add(listener);
    return () => this.#invalidationListeners.delete(listener);
  }

  #notifyInvalidation(): void {
    for (const listener of this.#invalidationListeners) listener();
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
      // An authority transition retires every shown cleanup alert: reports
      // from the replaced era are dropped by the identity check anyway, and
      // the old era's temps belong to its Go-side teardown now.
      cleanupAlerts: [],
      pendingDecision: null,
      pendingRevokes: [],
    });
  }

  /**
   * True when a native transport re-observes an already-processed
   * authentication-loss outcome while NO effective JavaScript-side authority
   * exists (no live attachment, no bearer token, no stored credential). Go
   * retains a revoked remembered credential and its file across a normal
   * disposal, so a foreground recheck can observe the same 401 repeatedly
   * while the owner is already pairing again. Such a recheck is idempotent:
   * it must not invalidate the transport era — a still-owned native pairing
   * poll would otherwise be discarded as stale and the owner forced into a
   * fresh pairing. The FIRST loss of real authority (an active attachment, a
   * retained token or a stored credential) keeps the full clearing path
   * below, as do genuine library/generation boundaries and explicit logout.
   */
  #nativeAuthRecheckIdempotent(code: string): boolean {
    if (typeof this.#client.attachSession !== "function") return false;
    if (this.#nativeAttachment !== null || this.#token !== "" || this.#store.read() !== null) return false;
    return (
      code === "NATIVE_NO_CREDENTIAL" || code === "SESSION_REVOKED" ||
      code === "SESSION_EXPIRED" || code === "NATIVE_SESSION_LOGGED_OUT"
    );
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
        if (this.#nativeAuthRecheckIdempotent(error.code)) {
          // Retire this verification run without touching the transport era,
          // the Go-held attempt slot, the store or a pending local-cleanup
          // obligation; the visible state stays the plain unauthenticated
          // truth for the observed outcome.
          this.#run += 1;
          this.#closeEvents();
          this.#apply({
            phase: "unauthenticated",
            session: null,
            capabilities: null,
            notice: this.#state.localCleanupPending
              ? NOTICE.logoutLocalFailure
              : error.code === "SESSION_REVOKED"
                ? NOTICE.revoked
                : error.code === "SESSION_EXPIRED"
                  ? NOTICE.expired
                  : NOTICE.unauthenticated,
          });
          return;
        }
        if (error.code === "SESSION_REVOKED") {
          this.#clearCredential(NOTICE.revoked);
          return;
        }
        if (error.code === "SESSION_EXPIRED") {
          this.#clearCredential(NOTICE.expired);
          return;
        }
        if (error.code === "NATIVE_NO_CREDENTIAL") {
          // A native first boot without a remembered credential is the plain
          // unauthenticated state — not an invalid stored credential.
          this.#clearCredential(NOTICE.unauthenticated);
          return;
        }
        this.#clearCredential(NOTICE.invalidCredential);
        return;
      }
      if (
        error.status === 409 &&
        (error.code === "NATIVE_API_INCOMPATIBLE" || error.code === "NATIVE_LIBRARY_MISMATCH" || error.code === "NATIVE_CREDENTIAL_MISMATCHED")
      ) {
        // A permanent Go binding pin is a blocked/incompatible state, never a
        // transient service problem: Go refused the attachment, so no
        // effective authority exists and no write may proceed. The truthful
        // typed notice reuses the existing native pairing notice map.
        this.#closeEvents();
        this.#apply({
          phase: "incompatible",
          session: null,
          capabilities: null,
          notice: nativePairingNotice(error) ?? NOTICE.incompatible,
        });
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
    if (this.#attaching) return;
    this.#attaching = true;
    try {
      const run = ++this.#run;
      try {
        const attached = await this.#client.attachSession();
        if (run !== this.#run) return;
        if (this.#nativeAttachment !== attached.attachment_id) {
          this.#nativeAttachment = attached.attachment_id;
          this.#token = ""; this.#boundCredential = "";
          this.#authority++; this.#client.invalidate(); this.#closeEvents();
          this.#apply({ phase: "checking", notice: NOTICE.checking, localCleanupPending: false, cleanupAlerts: [] });
        }
        await this.verify();
      } catch (error) { this.#failure(error, run); }
    } finally {
      this.#attaching = false;
    }
  }

  async verify(): Promise<void> {
    // A native transport with no live attachment reattaches first: an
    // explicit retry, the periodic recheck and post-recovery reconnects must
    // all reach the Go-held credential again instead of a dead end. The
    // reentrancy guard keeps the in-flight attach's own verify() call from
    // starting a second attach, and an outstanding logout's local-cleanup
    // obligation is never silently reauthenticated over.
    if (
      this.#nativeAttachment === null && typeof this.#client.attachSession === "function" &&
      !this.#attaching && !this.#state.localCleanupPending
    ) {
      await this.attachNativeSession();
      return;
    }
    if (this.#attaching && this.#nativeAttachment === null) return;
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
      this.#apply({ cleanupAlerts: [] });
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
      this.#apply({ cleanupAlerts: [] });
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
      // A runtime-generation boundary retires shown cleanup alerts with the
      // rest of the previous generation's confirmed data.
      ...(generationChanged ? { cleanupAlerts: [] } : {}),
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
    this.#eventInvalidationOff = subscription.onInvalidation?.(() => {
      if (this.#events === subscription && authority === this.#authority && this.#state.phase === "verified") this.#notifyInvalidation();
    });
    this.#notifyInvalidation();
    void subscription.closed.then(() => {
      if (this.#events !== subscription) return;
      this.#events = null;
      if (this.#state.phase !== "verified") return;
      if (authority !== this.#authority) return;
      this.#notifyInvalidation();
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
    this.#eventInvalidationOff?.(); this.#eventInvalidationOff = undefined;
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
  pair(deviceLabel: string): Promise<PairingCreated | NativePairingCreated> {
    return this.#client.pair(deviceLabel);
  }

  pollPairing(pairingId: string, pollingSecret?: string): Promise<PairingPoll | NativePairingPoll> {
    return this.#client.pollPairing(pairingId, pollingSecret);
  }

  /** Explicit native adoption of the still-owned approved pairing attempt. */
  async adoptPairing(pairingId: string): Promise<PairingAdoption> {
    if (!this.#client.adoptPairing) throw new Error("Native pairing adoption is unavailable.");
    return this.#client.adoptPairing(pairingId);
  }

  /** Explicit user Stop: the local Go-held attempt is abandoned truthfully. */
  async abandonPairing(pairingId: string): Promise<"abandoned" | "already-adopted" | "adopt-failed"> {
    if (!this.#client.abandonPairing) throw new Error("Native pairing cancellation is unavailable.");
    try {
      await this.#client.abandonPairing(pairingId);
      return "abandoned";
    } catch (error) {
      if (error instanceof ApiError && error.code === "NATIVE_PAIRING_ALREADY_ADOPTED") return "already-adopted";
      if (error instanceof ApiError && error.code === "NATIVE_PAIRING_ADOPT_FAILED") return "adopt-failed";
      // A stale attempt (replaced/expired server-side) is already gone
      // locally; the stop is still truthful.
      if (error instanceof ApiError && error.code === "NATIVE_PAIRING_ATTEMPT_STALE") return "abandoned";
      throw error;
    }
  }

  /** Signed local service discovery through the native facade. */
  async serviceDiscovery(): Promise<DaemonDiscovery> {
    if (!this.#client.serviceDiscovery) throw new Error("Native service discovery is unavailable.");
    return this.#client.serviceDiscovery();
  }

  /** Explicit owner-requested sibling-daemon start; never silent. */
  async startDaemonOffer(): Promise<DaemonStartOffer> {
    if (!this.#client.startDaemonOffer) throw new Error("Native daemon start offer is unavailable.");
    return this.#client.startDaemonOffer();
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
