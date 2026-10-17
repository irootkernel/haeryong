import type {
  Capabilities,
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
import { ApiError, NetworkError, StaleResponseError, UncertainResponseError } from "./types.ts";

// Browser transport on the trusted origin. Header bearer only; cookies are
// omitted, redirects are errors and only same-origin /api/v1 paths are called.
// Every request captures the current era; responses from an earlier
// credential/library/generation era are discarded before being applied.
export class HttpHaeryongClient implements HaeryongClient {
  #credential = "";
  #era = 0;

  setCredential(token: string): void {
    this.#credential = token;
  }

  invalidate(): void {
    this.#era += 1;
  }

  async #exchange<T>(method: string, path: string, body?: unknown, headers?: Record<string, string>): Promise<{ status: number; data: T }> {
    if (!path.startsWith("/api/v1/") || path.startsWith("//")) {
      throw new Error("Invalid API route");
    }
    const era = this.#era;
    const allHeaders = new Headers(headers);
    if (this.#credential) allHeaders.set("Authorization", "Bearer " + this.#credential);
    const init: RequestInit = { method, credentials: "omit", redirect: "error", headers: allHeaders };
    if (body !== undefined) {
      allHeaders.set("Content-Type", "application/json");
      init.body = JSON.stringify(body);
    }
    let response: Response;
    try {
      response = await fetch(path, init);
    } catch (cause) {
      if (cause instanceof DOMException && cause.name === "AbortError") throw cause;
      throw new NetworkError();
    }
    if (era !== this.#era) throw new StaleResponseError();
    if (!response.ok) {
      let code = "TRANSPORT_ERROR";
      let retryable = true;
      let retryAfter = Number(response.headers.get("Retry-After")) || 0;
      try {
        const parsed = (await response.json()) as { error?: { code?: string; retryable?: boolean } };
        if (parsed.error?.code) code = parsed.error.code;
        if (parsed.error?.retryable === false) retryable = false;
      } catch {
        // Non-JSON error bodies keep the transport fallback code.
      }
      if (era !== this.#era) throw new StaleResponseError();
      throw new ApiError(response.status, code, retryable, retryAfter);
    }
    if (response.status === 204) return { status: response.status, data: null as T };
    let parsed: { data?: T };
    try {
      parsed = (await response.json()) as { data?: T };
    } catch (cause) {
      // A successful status with an unreadable or malformed body leaves the
      // server outcome unknown (the write may already be applied).
      throw new UncertainResponseError(cause);
    }
    if (era !== this.#era) throw new StaleResponseError();
    return { status: response.status, data: (parsed.data ?? null) as T };
  }

  async #request<T>(method: string, path: string, body?: unknown, headers?: Record<string, string>): Promise<T> {
    return (await this.#exchange<T>(method, path, body, headers)).data;
  }

  pair(deviceLabel: string): Promise<PairingCreated> {
    return this.#request("POST", "/api/v1/auth/pairings", { device_label: deviceLabel });
  }

  pollPairing(pairingId: string, pollingSecret: string): Promise<PairingPoll> {
    return this.#request("POST", `/api/v1/auth/pairings/${encodeURIComponent(pairingId)}/poll`, {
      polling_secret: pollingSecret,
    });
  }

  async decidePairing(request: PairingDecisionRequest): Promise<DecisionResult> {
    const outcome = await this.#exchange<unknown>(
      "POST",
      `/api/v1/auth/pairings/${encodeURIComponent(request.pairing_id)}/${request.action}`,
      { code: request.code, origin: request.origin },
      { "If-Match": request.etag, "Idempotency-Key": request.operation_id },
    );
    // The durable write path classifies the canonical wire outcomes: a
    // terminal 200/201 application versus the HTTP 202 accepted reservation
    // envelope {operation_id, state:"accepted", operation_url}.
    if (outcome.status === 202) {
      return { kind: "accepted", body: outcome.data };
    }
    return { kind: "committed", body: outcome.data };
  }

  currentSession(): Promise<SessionInfo> {
    return this.#request("GET", "/api/v1/session");
  }

  capabilities(): Promise<Capabilities> {
    return this.#request("GET", "/api/v1/capabilities");
  }

  async listSessions(): Promise<DeviceSession[]> {
    const page = await this.#request<{ items: DeviceSession[] }>("GET", "/api/v1/sessions");
    return page.items;
  }

  async pendingPairings(): Promise<PairingView[]> {
    const page = await this.#request<{ items: PairingView[] }>("GET", "/api/v1/auth/pairings");
    return page.items;
  }

  operation(operationId: string): Promise<OperationView> {
    return this.#request("GET", `/api/v1/operations/${encodeURIComponent(operationId)}`);
  }

  async revokeSession(sessionId: string): Promise<void> {
    await this.#request("DELETE", `/api/v1/sessions/${encodeURIComponent(sessionId)}`);
  }

  async revokeOwnSession(): Promise<void> {
    await this.#request("DELETE", "/api/v1/session");
  }

  serviceStatus(): Promise<unknown> {
    return this.#request("GET", "/api/v1/status");
  }

  subscribeEvents(): EventSubscription {
    const controller = new AbortController();
    const era = this.#era;
    const headers = new Headers();
    if (this.#credential) headers.set("Authorization", "Bearer " + this.#credential);
    let ended: () => void;
    const closed = new Promise<void>((resolve) => {
      ended = resolve;
    });
    void fetch("/api/v1/events", { headers, credentials: "omit", redirect: "error", signal: controller.signal })
      .then(async (response) => {
        if (!response.body) return;
        const reader = response.body.getReader();
        // Liveness only: the shell does not parse SSE frames or cursors.
        while (!(await reader.read()).done) {
          if (era !== this.#era) break;
        }
      })
      .catch(() => undefined)
      .finally(() => ended());
    return { closed, close: () => controller.abort() };
  }
}
