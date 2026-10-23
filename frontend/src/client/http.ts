import type {
  VersionReferencesRequest, ResolveReferenceRequest, ReferencePage, ReferenceView,
  SearchRequest, SearchPage,
  ConfigurationView, ConfigurationPlanRequest, ConfigurationPlanView, ConfigurationCommitRequest, ConfigurationCommitView,
  ProjectSourceView, EvidenceVerificationRequest, EvidenceVerificationResult, SourceObservationRequest, SourceObservationResult,
  AssessmentFilter,
  AssessmentsView,
  Capabilities,
  ChunkAck,
  CoverageUnit,
  CreateUploadRequest,
  DecisionResult,
  DeviceSession,
  EntryChunk,
  EventSubscription,
  EvidenceItemView,
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
  SuppliedMetadataStream,
  UploadCheckpoint,
  UploadConfirmedRange,
  UploadDetail,
  UploadEntryProgress,
  UploadPin,
  UploadStateFilter,
  UploadSummary,
  VerificationRecord,
} from "./types.ts";
import type {
  DocumentView,
  FileEntryView,
  VersionView,
  RenderBootstrapView,
  RenderSessionView,
  PersonalKind,
  PersonalView,
} from "./types.ts";
import { ApiError, NetworkError, StaleResponseError, UncertainResponseError } from "./types.ts";
import { searchEventLines } from "./search-events.ts";
import { sourceWrite } from "./source-operations.ts";
import { eraGuardedStream } from "./stream.ts";
import type { ContextBinding } from "./context-types.ts";

// Canonical base64url (RFC 4648 §5, unpadded) of the exact UTF-8 JSON form
// the server's strict parser requires: all three tuple keys with JSON null
// for absent members.
function encodeContextBinding(binding: ContextBinding): string {
  const json = JSON.stringify({ version_id: binding.version_id, page_path: binding.page_path, render_instance_id: binding.render_instance_id });
  const bytes = new TextEncoder().encode(json);
  let binary = "";
  for (let i = 0; i < bytes.length; i += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  }
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// Browser transport on the trusted origin. Header bearer only; cookies are
// omitted, redirects are errors and only same-origin /api/v1 paths are called.
// Every request captures the current era; responses from an earlier
// credential/library/generation era are discarded before being applied.
export class HttpHaeryongClient implements HaeryongClient {
  get publicOrigin(): string { return location.origin; }

  #credential = "";
  #era = 0;

  setCredential(token: string): void {
    this.#credential = token;
  }

  invalidate(): void {
    this.#era += 1;
  }

  async #apiError(response: Response, era: number, fallbackRetryable: boolean): Promise<ApiError> {
    let code = "TRANSPORT_ERROR", retryable = fallbackRetryable;
    let details: Record<string, unknown> = {};
    try {
      const parsed = (await response.json()) as { error?: { code?: unknown; retryable?: unknown; details?: unknown } };
      if (typeof parsed.error?.code === "string") code = parsed.error.code;
      if (typeof parsed.error?.retryable === "boolean") retryable = parsed.error.retryable;
      if (parsed.error?.details && typeof parsed.error.details === "object" && !Array.isArray(parsed.error.details)) details = parsed.error.details as Record<string, unknown>;
    } catch {
      // A non-JSON response retains the transport fallback classification.
    }
    if (era !== this.#era) throw new StaleResponseError();
    return new ApiError(response.status, code, retryable, Number(response.headers.get("Retry-After")) || 0, details);
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
      throw await this.#apiError(response, era, true);
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
      throw await this.#apiError(response, era, response.status === 503);
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

  configurationView(): Promise<ConfigurationView> { return this.#request("GET", "/api/v1/configuration"); }

  #sourceWrite<T>(path: string, body: unknown, operationId: string): Promise<T> {
    const era = this.#era;
    return sourceWrite<T>(operationId, async () => {
      const outcome = await this.#exchange("POST", path, body, { "Idempotency-Key": operationId });
      return { kind: outcome.status === 202 ? "accepted" : "committed", body: outcome.data };
    }, (id) => { if (era !== this.#era) throw new StaleResponseError(); return this.operation(id); });
  }

  configurationPlan(request: ConfigurationPlanRequest, operationId: string): Promise<ConfigurationPlanView> {
    return this.#sourceWrite("/api/v1/configuration/plans", request, operationId);
  }
  configurationPlanCommit(planId: string, request: ConfigurationCommitRequest, operationId: string): Promise<ConfigurationCommitView> {
    return this.#sourceWrite(`/api/v1/configuration/plans/${encodeURIComponent(planId)}/commit`, request, operationId);
  }
  projectRegister(name: string, operationId: string): Promise<ProjectView> { return this.#sourceWrite("/api/v1/projects", { name }, operationId); }
  projectSources(projectId: string, cursor?: string): Promise<ListPage<ProjectSourceView>> {
    return this.#request("GET", this.#collection(`/api/v1/projects/${encodeURIComponent(projectId)}/sources`, { cursor }));
  }
  verifySourceEvidence(versionId: string, evidenceId: string, request: EvidenceVerificationRequest, operationId: string): Promise<EvidenceVerificationResult> {
    return this.#sourceWrite(this.#collection(`/api/v1/versions/${encodeURIComponent(versionId)}/source-evidence-verifications`, { evidence_id: evidenceId }), request, operationId);
  }
  runSourceObservation(versionId: string, request: SourceObservationRequest, operationId: string): Promise<SourceObservationResult> {
    return this.#sourceWrite(`/api/v1/versions/${encodeURIComponent(versionId)}/source-observations`, request, operationId);
  }

  versionReferences(request: VersionReferencesRequest): Promise<ReferencePage> {
    const params = new URLSearchParams();
    if (request.cursor !== undefined) params.set("cursor", request.cursor);
    if (request.limit !== undefined) params.set("limit", String(request.limit));
    return this.#request("GET", `/api/v1/versions/${encodeURIComponent(request.versionId)}/references?${params}`);
  }
  resolveReference(request: ResolveReferenceRequest): Promise<ReferenceView> {
    const params = new URLSearchParams({ reference_id: request.referenceId });
    if (request.pagePath !== undefined) params.set("page_path", request.pagePath);
    if (request.regionId !== undefined) params.set("region_id", request.regionId);
    return this.#request("GET", `/api/v1/versions/${encodeURIComponent(request.versionId)}/references/resolve?${params}`);
  }

  search(request: SearchRequest): Promise<SearchPage> {
    // q= is meaningful here; the ordinary collection helper omits empty values.
    const params = new URLSearchParams({ q: request.q });
    for (const key of ["project_id", "lifecycle", "versions", "kind", "cursor", "limit"] as const) {
      if (request[key] !== undefined) params.set(key, String(request[key]));
    }
    return this.#request("GET", "/api/v1/search?" + params.toString());
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

  async versionSuppliedMetadata(versionId: string): Promise<SuppliedMetadataStream> {
    // Same fixed authenticated stream path as file delivery, plus the
    // immutable object's strong ETag captured at open for byte verification
    // before any parse.
    const controller = new AbortController();
    const era = this.#era;
    const headers = new Headers();
    if (this.#credential) headers.set("Authorization", "Bearer " + this.#credential);
    const response = await fetch(`/api/v1/versions/${encodeURIComponent(versionId)}/metadata?kind=supplied`, { credentials: "omit", redirect: "error", headers, signal: controller.signal });
    if (era !== this.#era) {
      response.body?.cancel().catch(() => undefined);
      throw new StaleResponseError();
    }
    if (!response.ok || !response.body) {
      throw await this.#apiError(response, era, response.status === 503);
    }
    return {
      etag: response.headers.get("ETag"),
      stream: {
        body: eraGuardedStream(response.body, () => era !== this.#era),
        cancel: () => controller.abort(),
      },
    };
  }

  versionEvidence(versionId: string, evidenceId: string): Promise<EvidenceItemView> {
    return this.#request("GET", this.#collection(`/api/v1/versions/${encodeURIComponent(versionId)}/source-evidence`, { evidence_id: evidenceId }));
  }

  evidenceVerifications(versionId: string, evidenceId: string, cursor?: string): Promise<ListPage<VerificationRecord>> {
    return this.#request("GET", this.#collection(`/api/v1/versions/${encodeURIComponent(versionId)}/source-evidence-verifications`, { evidence_id: evidenceId, cursor }));
  }

  versionAssessments(versionId: string, filter?: AssessmentFilter & { cursor?: string }): Promise<AssessmentsView> {
    const params: Record<string, string | undefined> = { target_set_id: filter?.targetSetId, cursor: filter?.cursor };
    if (filter?.scope) {
      params.scope_kind = filter.scope.kind;
      if (filter.scope.pagePath !== undefined) params.page_path = filter.scope.pagePath;
      if (filter.scope.regionId !== undefined) params.region_id = filter.scope.regionId;
    }
    return this.#request("GET", this.#collection(`/api/v1/versions/${encodeURIComponent(versionId)}/assessments`, params));
  }

  assessmentCoverage(versionId: string, coverageSetId: string, cursor?: string, attemptRunId?: string): Promise<ListPage<CoverageUnit>> {
    return this.#request("GET", this.#collection(`/api/v1/versions/${encodeURIComponent(versionId)}/assessments/coverage`, { coverage_set_id: coverageSetId, ...(attemptRunId === undefined ? {} : { attempt_run_id: attemptRunId }), cursor }));
  }

  renderBootstrap(versionId: string, pagePath?: string): Promise<RenderBootstrapView> {
    const query = pagePath === undefined ? "" : `?page_path=${encodeURIComponent(pagePath)}`;
    return this.#request("GET", `/api/v1/versions/${encodeURIComponent(versionId)}/render-bootstrap${query}`);
  }

  renderSessionOpen(versionId: string, operationId: string): Promise<RenderSessionView> {
    return this.#request("POST", `/api/v1/versions/${encodeURIComponent(versionId)}/render-sessions`, {}, { "Idempotency-Key": operationId });
  }

  async renderSessionRenew(versionId: string, sessionId: string): Promise<void> {
    await this.#request("POST", `/api/v1/versions/${encodeURIComponent(versionId)}/render-sessions/${encodeURIComponent(sessionId)}/renew`, {});
  }

  async renderSessionClose(versionId: string, sessionId: string): Promise<void> {
    await this.#request("POST", `/api/v1/versions/${encodeURIComponent(versionId)}/render-sessions/${encodeURIComponent(sessionId)}/close`, {});
  }

  personalGet(kind: PersonalKind, versionId: string, page?: string): Promise<PersonalView> {
    const prefix = `/api/v1/versions/${encodeURIComponent(versionId)}/${kind}`;
    const query = kind === "page-position" && page ? `?page=${encodeURIComponent(page)}` : "";
    return this.#request("GET", prefix + query);
  }

  personalPut(kind: PersonalKind, versionId: string, page: string | null, body: unknown, precondition: { ifMatch: string } | { create: true }, operationId: string): Promise<PersonalView> {
    const prefix = `/api/v1/versions/${encodeURIComponent(versionId)}/${kind}`;
    const query = kind === "page-position" && page ? `?page=${encodeURIComponent(page)}` : "";
    const headers: Record<string, string> = { "Content-Type": "application/json", "Idempotency-Key": operationId };
    if ("ifMatch" in precondition) headers["If-Match"] = precondition.ifMatch;
    else headers["If-None-Match"] = "*";
    return this.#request("PUT", prefix + query, body, headers);
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

  createContextUpload(binding: ContextBinding, operationId: string): Promise<{ upload_id: string; checkpoint: UploadCheckpoint }> {
    return this.#request("POST", "/api/v1/uploads", { purpose: "context", input_mode: "object", target: binding }, { "Idempotency-Key": operationId });
  }

  async consumeContext(binding: ContextBinding, uploadId: string, operationId: string): Promise<DecisionResult> {
    const query = new URLSearchParams({ version_id: binding.version_id, input: "upload" });
    if (binding.page_path !== null) query.set("page_path", binding.page_path);
    if (binding.render_instance_id !== null) query.set("render_instance_id", binding.render_instance_id);
    const outcome = await this.#exchange<unknown>("POST", "/api/v1/contexts?" + query.toString(), { upload_id: uploadId }, { "Idempotency-Key": operationId });
    return { kind: outcome.status === 202 ? "accepted" : "committed", body: outcome.data };
  }

  async verifyContextFresh(contextRef: string, binding: ContextBinding, bytesSha256: string): Promise<void> {
    const path = `/api/v1/contexts/${encodeURIComponent(contextRef)}`;
    const etag = `"sha256-${bytesSha256}"`;
    const era = this.#era;
    const headers = new Headers({ "Haeryong-Context-Binding": encodeContextBinding(binding), "If-None-Match": etag });
    if (this.#credential) headers.set("Authorization", "Bearer " + this.#credential);
    let response: Response;
    try {
      response = await fetch(path, { method: "GET", credentials: "omit", redirect: "error", headers });
    } catch (cause) {
      if (cause instanceof DOMException && cause.name === "AbortError") throw cause;
      throw new NetworkError();
    }
    if (era !== this.#era) throw new StaleResponseError();
    if (response.status === 304) {
      // A 304 is not response.ok: the proof is the exact status plus the
      // strong ETag and the fresh-verification marker, with no body.
      if (response.headers.get("ETag") !== etag || response.headers.get("Haeryong-Context-Verified") !== "fresh") {
        throw new ApiError(304, "CONTEXT_PROOF_INVALID", false, 0, {});
      }
      return;
    }
    if (!response.ok) {
      throw await this.#apiError(response, era, response.status === 503);
    }
    // A body-carrying response is never a fresh proof; the server keeps
    // ordinary byte recovery on the binding-absent path only.
    response.body?.cancel().catch(() => undefined);
    throw new ApiError(response.status, "CONTEXT_PROOF_INVALID", false, 0, {});
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
    return this.#stream(`/api/v1/versions/${encodeURIComponent(versionId)}/package`);
  }

  fileStream(versionId: string, path: string): Promise<PackageStream> {
    return this.#stream(`/api/v1/versions/${encodeURIComponent(versionId)}/file?${new URLSearchParams({ path })}`);
  }

  async #stream(path: string): Promise<PackageStream> {
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
      throw await this.#apiError(response, era, response.status === 503);
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

  /** The browser validates its actual polling secret on every poll. */
  pollPairing(pairingId: string, pollingSecret: string | undefined): Promise<PairingPoll> {
    if (pollingSecret === undefined) {
      throw new Error("browser pairing requires its polling secret");
    }
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
    const listeners = new Set<() => void>();
    const decode = new TextDecoder();
    const lines = searchEventLines(() => { if (era === this.#era && !controller.signal.aborted) for (const listener of listeners) listener(); });
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
        for (;;) {
          const part = await reader.read();
          if (era !== this.#era || controller.signal.aborted) { await reader.cancel(); break; }
          if (part.done) break;
          // Only event names become hints, never resource IDs or event bodies.
          lines(decode.decode(part.value, { stream: true }));
        }
      })
      .catch(() => undefined)
      .finally(() => ended());
    return { closed, onInvalidation: listener => { listeners.add(listener); return () => { listeners.delete(listener); }; }, close: () => { listeners.clear(); controller.abort(); } };
  }
}
