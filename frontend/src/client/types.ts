// Shared transport-level contract for the trusted shell. Shared UI components
// depend on HaeryongClient only; the browser implementation calls the same
// origin /api/v1 and the native implementation forwards named operations
// through a narrow desktop port (TASK-003-05). No Wails import lives here.

export const SUPPORTED_API_VERSION = "1.0";
export const SESSION_STORAGE_KEY = "haeryong.owner-session.v1";

export interface CredentialScope {
  kind: string;
  actions?: string[];
  project_ids?: string[];
}

export interface SessionInfo {
  session_id: string;
  principal_id: string;
  library_id: string;
  device_label: string;
  origin: string;
  state: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string | null;
  revision: number;
  etag: string;
  scope: CredentialScope;
}

export interface PairingView {
  pairing_id: string;
  device_label: string;
  origin: string;
  created_at: string;
  expires_at: string;
  state: string;
  revision: number;
  etag: string;
}

export interface PairingCreated extends PairingView {
  code: string;
  polling_secret: string;
}

export interface PairingPoll {
  state: string;
  session?: SessionInfo;
  token?: string;
}

export interface Capabilities {
  library_id: string;
  api_version: string;
  runtime_generation: string;
  stream_epoch: string;
  unfiled_project_id: string;
  features: Record<string, boolean>;
}

/** Canonical receipt states: "completed" | "failed" (terminal), "reconciling" or pending (unresolved). */
export interface OperationView {
  operation_id: string;
  state: string;
  original_status: number | null;
  result_ref: unknown;
  job_id: string | null;
  result_metadata: unknown;
}

export type PairingDecisionAction = "approve" | "reject";

/**
 * Typed outcome of a dispatched pairing decision write.
 * `committed` maps a terminal 200/201 application; `accepted` maps the
 * canonical HTTP 202 pending reservation envelope
 * `{operation_id, state:"accepted", operation_url}`. Errors propagate as
 * ApiError (e.g. 409 OPERATION_RECONCILIATION_REQUIRED) or transport errors.
 */
export type DecisionResult =
  | { kind: "committed"; body: unknown }
  | { kind: "accepted"; body: unknown };
export interface PairingDecisionRequest {
  pairing_id: string;
  code: string;
  origin: string;
  etag: string;
  operation_id: string;
  action: PairingDecisionAction;
}

export interface DeviceSession {
  session_id: string;
  device_label: string;
  state: string;
  origin: string;
  last_seen_at: string;
}

export interface EventSubscription {
  closed: Promise<void>;
  close(): void;
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryable: boolean;
  readonly retryAfter: number;
  constructor(status: number, code: string, retryable: boolean, retryAfter = 0) {
    super(`HTTP ${status} ${code}`);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.retryable = retryable;
    this.retryAfter = retryAfter;
  }
}

export class NetworkError extends Error {
  constructor(message = "network unreachable") {
    super(message);
    this.name = "NetworkError";
  }
}

/** A response from an earlier credential/generation era; never applied. */
export class StaleResponseError extends Error {
  constructor() {
    super("previous era response ignored");
    this.name = "StaleResponseError";
  }
}

/**
 * A successful response whose body could not be read or parsed. The server may
 * already have applied the request, so callers must treat the outcome as
 * uncertain and preserve their recovery identity; this is never a failure or
 * success claim.
 */
export class UncertainResponseError extends Error {
  constructor(cause?: unknown) {
    super("successful response body could not be read");
    this.name = "UncertainResponseError";
    if (cause !== undefined) {
      this.cause = cause;
    }
  }
}

/** A write whose server outcome is unknown (no response was received). */
export class UnknownOutcomeError extends Error {
  constructor(operation: string) {
    super(`outcome unknown: ${operation}`);
    this.name = "UnknownOutcomeError";
    this.operation = operation;
  }
  readonly operation: string;
}

export interface HaeryongClient {
  pair(deviceLabel: string): Promise<PairingCreated>;
  pollPairing(pairingId: string, pollingSecret: string): Promise<PairingPoll>;
  decidePairing(request: PairingDecisionRequest): Promise<DecisionResult>;
  currentSession(): Promise<SessionInfo>;
  capabilities(): Promise<Capabilities>;
  listSessions(): Promise<DeviceSession[]>;
  pendingPairings(): Promise<PairingView[]>;
  operation(operationId: string): Promise<OperationView>;
  revokeSession(sessionId: string): Promise<void>;
  revokeOwnSession(): Promise<void>;
  serviceStatus(): Promise<unknown>;
  subscribeEvents(): EventSubscription;
}

/** Transport facet the shell controller uses to bind and invalidate eras. */
export interface CredentialAware {
  setCredential(token: string): void;
  invalidate(): void;
}

/**
 * A protected write (pairing decision or session revoke) was NOT dispatched
 * because the controller is not in the verified phase. This is an honest
 * local result: nothing reached the server, so no unknown outcome, receipt
 * recovery identity or pending intent is created, overwritten or cleared.
 */
export class DispatchBlockedError extends Error {
  constructor() {
    super("현재 확인된 연결 상태에서는 이 요청을 보낼 수 없습니다. 다시 연결 후 시도하세요.");
    this.name = "DispatchBlockedError";
  }
}

/** True when the failure means no response arrived from the service. */
export function isNetworkFailure(error: unknown): boolean {
  return error instanceof NetworkError;
}
