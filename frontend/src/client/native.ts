// Narrow native transport boundary. The desktop companion (TASK-003-05) binds
// these named operations to a typed Go proxy. The port accepts no arbitrary
// URLs, filesystem paths or generic method names from imported content.
import type {
  Capabilities,
  CredentialAware,
  DecisionResult,
  DeviceSession,
  EventSubscription,
  HaeryongClient,
  OperationView,
  PairingCreated,
  PairingDecisionRequest,
  PairingPoll,
  PairingView,
  SessionInfo,
} from "./types.ts";
import { StaleResponseError } from "./types.ts";

export type PairRequest = { device_label: string };
export type PollPairingRequest = { pairing_id: string; polling_secret: string };
export type DecidePairingPayload = {
  code: string;
  origin: string;
  etag: string;
  operation_id: string;
  action: PairingDecisionRequest["action"];
};
export type RevokeSessionRequest = { session_id: string };
export type OperationRequest = { operation_id: string };

/** Named operations only; each overload pins an exact payload/result pair. */
export interface NativePort {
  call(operation: "pair", payload: PairRequest): Promise<PairingCreated>;
  call(operation: "pollPairing", payload: PollPairingRequest): Promise<PairingPoll>;
  call(operation: "decidePairing", payload: PairingDecisionRequest & DecidePairingPayload): Promise<DecisionResult>;
  call(operation: "currentSession", payload: {}): Promise<SessionInfo>;
  call(operation: "capabilities", payload: {}): Promise<Capabilities>;
  call(operation: "listSessions", payload: {}): Promise<DeviceSession[]>;
  call(operation: "pendingPairings", payload: {}): Promise<PairingView[]>;
  call(operation: "operation", payload: OperationRequest): Promise<OperationView>;
  call(operation: "revokeSession", payload: RevokeSessionRequest): Promise<void>;
  call(operation: "revokeOwnSession", payload: {}): Promise<void>;
  call(operation: "serviceStatus", payload: {}): Promise<unknown>;
  call(operation: "subscribeEvents", payload: {}): Promise<EventSubscription>;
}

export const NATIVE_OPERATIONS = [
  "pair",
  "pollPairing",
  "decidePairing",
  "currentSession",
  "capabilities",
  "listSessions",
  "pendingPairings",
  "operation",
  "revokeSession",
  "revokeOwnSession",
  "serviceStatus",
  "subscribeEvents",
] as const;

/** HaeryongClient view over an injected NativePort. */
export class NativeHaeryongClient implements HaeryongClient, CredentialAware {
  readonly #port: NativePort;
  #credential = "";
  #era = 0;

  constructor(port: NativePort) {
    this.#port = port;
  }

  /**
   * The desktop host owns credential binding (TASK-003-05). Binding the same
   * token again does not change the era; only invalidate() does.
   */
  setCredential(token: string): void {
    this.#credential = token;
  }

  get credential(): string {
    return this.#credential;
  }

  /**
   * Authority era change (credential/library/generation boundary). This never
   * erases the bound credential by itself; the controller drops it explicitly
   * with setCredential(""). Responses and rejections crossing an invalidate()
   * boundary are converted to StaleResponseError: a late success must not be
   * applied, and a late failure must not be mistaken for a current-outcome
   * network loss of a dispatched write.
   */
  invalidate(): void {
    this.#era += 1;
  }

  #fresh<T>(promise: Promise<T>, era: number): Promise<T> {
    return promise.then(
      (value) => {
        if (era !== this.#era) throw new StaleResponseError();
        return value;
      },
      (error) => {
        if (era !== this.#era) throw new StaleResponseError();
        throw error;
      },
    );
  }

  pair(deviceLabel: string): Promise<PairingCreated> {
    return this.#fresh(this.#port.call("pair", { device_label: deviceLabel }), this.#era);
  }

  pollPairing(pairingId: string, pollingSecret: string): Promise<PairingPoll> {
    return this.#fresh(this.#port.call("pollPairing", { pairing_id: pairingId, polling_secret: pollingSecret }), this.#era);
  }

  decidePairing(request: PairingDecisionRequest): Promise<DecisionResult> {
    return this.#fresh(
      this.#port.call("decidePairing", {
        pairing_id: request.pairing_id,
        code: request.code,
        origin: request.origin,
        etag: request.etag,
        operation_id: request.operation_id,
        action: request.action,
      }),
      this.#era,
    );
  }

  currentSession(): Promise<SessionInfo> {
    return this.#fresh(this.#port.call("currentSession", {}), this.#era);
  }

  capabilities(): Promise<Capabilities> {
    return this.#fresh(this.#port.call("capabilities", {}), this.#era);
  }

  listSessions(): Promise<DeviceSession[]> {
    return this.#fresh(this.#port.call("listSessions", {}), this.#era);
  }

  pendingPairings(): Promise<PairingView[]> {
    return this.#fresh(this.#port.call("pendingPairings", {}), this.#era);
  }

  operation(operationId: string): Promise<OperationView> {
    return this.#fresh(this.#port.call("operation", { operation_id: operationId }), this.#era);
  }

  revokeSession(sessionId: string): Promise<void> {
    return this.#fresh(this.#port.call("revokeSession", { session_id: sessionId }), this.#era);
  }

  revokeOwnSession(): Promise<void> {
    return this.#fresh(this.#port.call("revokeOwnSession", {}), this.#era);
  }

  serviceStatus(): Promise<unknown> {
    return this.#fresh(this.#port.call("serviceStatus", {}), this.#era);
  }

  subscribeEvents(): EventSubscription {
    let ended: () => void;
    const closed = new Promise<void>((resolve) => {
      ended = resolve;
    });
    let handle: EventSubscription | null = null;
    let finished = false;
    const era = this.#era;
    void this.#port
      .call("subscribeEvents", {})
      .then((subscription) => {
        if (finished) {
          subscription.close();
          return;
        }
        if (era !== this.#era) {
          // A subscription arriving from a dead authority era is disposed
          // immediately instead of being wired into the shell.
          subscription.close();
          ended();
          return;
        }
        handle = subscription;
        void subscription.closed.then(() => ended());
      })
      .catch(() => ended());
    return {
      closed,
      close: () => {
        finished = true;
        handle?.close();
        ended();
      },
    };
  }
}
