import type {
  Capabilities,
  ChunkAck,
  CreateUploadRequest,
  DecisionResult,
  DeviceSession,
  EntryChunk,
  EventSubscription,
  HaeryongClient,
  ImportPublishRequest,
  JobView,
  ListPage,
  OperationView,
  PackageStream,
  PairingCreated,
  PairingDecisionRequest,
  PairingPoll,
  PairingView,
  ProjectView,
  SessionInfo,
  UploadCheckpoint,
  UploadConfirmedRange,
  UploadDetail,
  UploadEntryProgress,
  UploadPin,
  UploadStateFilter,
  UploadSummary,
} from "./types.ts";
import type {
  DocumentView,
  FileEntryView,
  VersionView,
} from "./types.ts";
import { ApiError, NetworkError, StaleResponseError, UncertainResponseError } from "./types.ts";
import { eraGuardedStream } from "./stream.ts";

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

  async #rawExchange(method: string, path: string, bytes: Uint8Array, headers: Record<string, string>): Promise<{ status: number; data: unknown }> {
    if (!path.startsWith("/api/v1/") || path.startsWith("//")) {
      throw new Error("Invalid API route");
    }
    const era = this.#era;
    const allHeaders = new Headers(headers);
    if (this.#credential) allHeaders.set("Authorization", "Bearer " + this.#credential);
    let response: Response;
    try {
      response = await fetch(path, { method, credentials: "omit", redirect: "error", headers: allHeaders, body: bytes as BodyInit });
    } catch (cause) {
      if (cause instanceof DOMException && cause.name === "AbortError") throw cause;
      throw new NetworkError();
    }
    if (era !== this.#era) throw new StaleResponseError();
    if (!response.ok) {
      let code = "TRANSPORT_ERROR";
      try {
        const parsed = (await response.json()) as { error?: { code?: string } };
        if (parsed.error?.code) code = parsed.error.code;
      } catch {
        // Non-JSON error bodies keep the transport fallback code.
      }
      if (era !== this.#era) throw new StaleResponseError();
      throw new ApiError(response.status, code, response.status === 503, 0);
    }
    let parsed: { data?: unknown };
    try {
      parsed = (await response.json()) as { data?: unknown };
    } catch (cause) {
      throw new UncertainResponseError(cause);
    }
    if (era !== this.#era) throw new StaleResponseError();
    return { status: response.status, data: parsed.data ?? null };
  }

  #collection(path: string, params: Record<string, string | undefined>): string {
    const search = new URLSearchParams();
    for (const [key, value] of Object.entries(params)) {
      if (value !== undefined && value !== "") search.set(key, value);
    }
    const query = search.toString();
    return query ? path + "?" + query : path;
  }

  listProjects(cursor?: string): Promise<ListPage<ProjectView>> {
    return this.#request("GET", this.#collection("/api/v1/projects", { cursor }));
  }

  listDocuments(filter: { projectId?: string; lifecycle?: string; cursor?: string }): Promise<ListPage<DocumentView>> {
    return this.#request("GET", this.#collection("/api/v1/documents", { project_id: filter.projectId, lifecycle: filter.lifecycle, cursor: filter.cursor }));
  }

  documentDetail(documentId: string): Promise<DocumentView> {
    return this.#request("GET", `/api/v1/documents/${encodeURIComponent(documentId)}`);
  }

  documentVersions(documentId: string, cursor?: string): Promise<ListPage<VersionView>> {
    return this.#request("GET", this.#collection(`/api/v1/documents/${encodeURIComponent(documentId)}/versions`, { cursor }));
  }

  versionDetail(versionId: string): Promise<VersionView> {
    return this.#request("GET", `/api/v1/versions/${encodeURIComponent(versionId)}`);
  }

  versionFiles(versionId: string, cursor?: string): Promise<ListPage<FileEntryView>> {
    return this.#request("GET", this.#collection(`/api/v1/versions/${encodeURIComponent(versionId)}/files`, { cursor }));
  }

  listUploads(filter: { state?: UploadStateFilter; unadopted?: boolean; cursor?: string }): Promise<ListPage<UploadSummary>> {
    return this.#request("GET", this.#collection("/api/v1/uploads", { state: filter.state, unadopted: filter.unadopted === undefined ? undefined : filter.unadopted ? "true" : "false", cursor: filter.cursor }));
  }

  uploadDetail(uploadId: string): Promise<UploadDetail> {
    return this.#request("GET", `/api/v1/uploads/${encodeURIComponent(uploadId)}`);
  }

  uploadEntries(uploadId: string, cursor?: string): Promise<ListPage<UploadEntryProgress>> {
    return this.#request("GET", this.#collection(`/api/v1/uploads/${encodeURIComponent(uploadId)}/entries`, { cursor }));
  }

  uploadRanges(uploadId: string, path?: string, cursor?: string): Promise<ListPage<UploadConfirmedRange>> {
    return this.#request("GET", this.#collection(`/api/v1/uploads/${encodeURIComponent(uploadId)}/ranges`, { path, cursor }));
  }

  uploadPins(uploadId: string, cursor?: string): Promise<ListPage<UploadPin>> {
    return this.#request("GET", this.#collection(`/api/v1/uploads/${encodeURIComponent(uploadId)}/pins`, { cursor }));
  }

  async createUpload(request: CreateUploadRequest, operationId: string): Promise<{ upload_id: string; checkpoint: UploadCheckpoint }> {
    return this.#request("POST", "/api/v1/uploads", request, { "Idempotency-Key": operationId });
  }

  async putUploadEntry(uploadId: string, chunk: EntryChunk): Promise<ChunkAck> {
    const headers: Record<string, string> = {
      "Content-Type": chunk.kind === "file" ? "application/octet-stream" : "application/json",
    };
    let bytes = chunk.bytes;
    if (chunk.kind !== "file") {
      const body = chunk.kind === "directory" ? {} : { link_target: chunk.linkTarget ?? "" };
      bytes = new TextEncoder().encode(JSON.stringify(body));
    } else {
      headers["Haeryong-Upload-Offset"] = String(chunk.offset);
      headers["Haeryong-Upload-Chunk-SHA256"] = chunk.chunkSha256;
      if (chunk.totalLength !== undefined) headers["Haeryong-Upload-Length"] = String(chunk.totalLength);
      if (chunk.final) headers["Haeryong-Upload-Final"] = "true";
    }
    return (await this.#rawExchange(
      "PUT",
      `/api/v1/uploads/${encodeURIComponent(uploadId)}/entries?path=${encodeURIComponent(chunk.path)}&kind=${chunk.kind}`,
      bytes,
      headers,
    )).data as ChunkAck;
  }

  async completeUpload(uploadId: string, checkpoint: UploadCheckpoint, operationId: string): Promise<DecisionResult> {
    const outcome = await this.#exchange<unknown>("POST", `/api/v1/uploads/${encodeURIComponent(uploadId)}/complete`, { checkpoint }, { "Idempotency-Key": operationId });
    if (outcome.status === 202) return { kind: "accepted", body: outcome.data };
    return { kind: "committed", body: outcome.data };
  }

  async abandonUpload(uploadId: string, operationId: string): Promise<DecisionResult> {
    const outcome = await this.#exchange<unknown>("POST", `/api/v1/uploads/${encodeURIComponent(uploadId)}/abandon`, {}, { "Idempotency-Key": operationId });
    if (outcome.status === 202) return { kind: "accepted", body: outcome.data };
    return { kind: "committed", body: outcome.data };
  }

  async importPublish(request: ImportPublishRequest, operationId: string): Promise<DecisionResult> {
    const outcome = await this.#exchange<unknown>("POST", "/api/v1/imports", request, { "Idempotency-Key": operationId });
    if (outcome.status === 202) return { kind: "accepted", body: outcome.data };
    return { kind: "committed", body: outcome.data };
  }

  jobGet(jobId: string): Promise<JobView> {
    return this.#request("GET", `/api/v1/jobs/${encodeURIComponent(jobId)}`);
  }

  async jobCancel(jobId: string, operationId: string): Promise<DecisionResult> {
    const outcome = await this.#exchange<unknown>("POST", `/api/v1/jobs/${encodeURIComponent(jobId)}/cancel`, {}, { "Idempotency-Key": operationId });
    if (outcome.status === 202) return { kind: "accepted", body: outcome.data };
    return { kind: "committed", body: outcome.data };
  }

  async packageStream(versionId: string): Promise<PackageStream> {
    const path = `/api/v1/versions/${encodeURIComponent(versionId)}/package`;
    const controller = new AbortController();
    const era = this.#era;
    const headers = new Headers();
    if (this.#credential) headers.set("Authorization", "Bearer " + this.#credential);
    const response = await fetch(path, { credentials: "omit", redirect: "error", headers, signal: controller.signal });
    if (era !== this.#era) {
      response.body?.cancel().catch(() => undefined);
      throw new StaleResponseError();
    }
    if (!response.ok || !response.body) {
      let code = "TRANSPORT_ERROR";
      try {
        const parsed = (await response.json()) as { error?: { code?: string } };
        if (parsed.error?.code) code = parsed.error.code;
      } catch {
        // Non-JSON error bodies keep the transport fallback code.
      }
      if (era !== this.#era) throw new StaleResponseError();
      throw new ApiError(response.status, code, response.status === 503, 0);
    }
    return {
      // The stream stays era-guarded throughout consumption: a chunk read
      // after invalidate() cancels the download and errors instead of
      // feeding a replaced authority's bytes into a destination.
      body: eraGuardedStream(response.body, () => era !== this.#era),
      cancel: () => controller.abort(),
    };
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
