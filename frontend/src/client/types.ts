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

/** Cursor list envelope shared by every collection route. */
export interface ListPage<T> {
  items: T[];
  next_cursor: string | null;
}

export interface ProjectView {
  project_id: string;
  name: string;
  lifecycle: string;
  revision: number;
  permissions_revision: number;
  updated_at: string;
  etag: string;
}

export interface DocumentView {
  document_id: string;
  project_id: string;
  title: string;
  lifecycle: string;
  latest_version_id: string | null;
  revision: number;
  updated_at: string;
  versions_url: string;
  etag: string;
}

export interface VersionView {
  version_id: string;
  document_id: string;
  sequence: number;
  base_version_id: string | null;
  title_snapshot: string;
  default_page: string | null;
  viewer_profile: string;
  source_basis: string;
  payload_sha256: string;
  manifest_sha256: string;
  supplied_metadata_sha256: string | null;
  manifest_url: string;
  supplied_metadata_url: string | null;
  files_url: string;
  render_bootstrap_url: string;
  etag: string;
}

export interface FileEntryView {
  path: string;
  kind: "file" | "directory" | "symlink";
  byte_size: string;
  bytes_sha256?: string;
  link_target?: string;
  media_type?: string;
  preview: string;
  file_url: string;
  files_url?: string;
  etag: string;
}

/** Upload checkpoint; opaque beyond the fields the engine recomputes. */
export interface UploadCheckpoint {
  generation: number;
  inventory_sha256: string;
  accepted_bytes: string;
  complete_input: boolean;
}

export interface UploadSummary {
  upload_id: string;
  originating_principal_id: string;
  originating_device_id: string | null;
  purpose: string;
  input_mode: string;
  selection_name: string | null;
  target: unknown;
  state: "open" | "sealing" | "sealed" | "abandoned";
  byte_size: string;
  staging_bytes: string;
  created_at: string;
  updated_at: string;
  reusable: boolean;
  unavailable_reason: string | null;
  pin_summary: Record<string, number>;
  pins_url: string;
  detail_url: string;
  revision: number;
  etag: string;
}

export interface UploadDetail extends UploadSummary {
  checkpoint: UploadCheckpoint;
  ranges_url: string;
  entries_url: string;
  error: string | null;
  seal_job_id: string | null;
  seal_operation_id: string | null;
  sealed_content_set_id: string | null;
  sealed_object_ref: { object_id: string; bytes_sha256: string; byte_size: string; media_type: string } | null;
  transport_entry?: string | null;
}

export interface UploadEntryProgress {
  path: string;
  kind: "file" | "directory" | "symlink";
  link_target?: string;
  byte_size: string;
  accepted_bytes: string;
  complete: boolean;
}

export interface UploadConfirmedRange {
  path: string;
  offset: string;
  byte_size: string;
  bytes_sha256: string;
}

export interface UploadPin {
  pin_id: string;
  kind: string;
  owner_id: string;
  bytes_sha256: string;
}

export interface JobView {
  job_id: string;
  kind: string;
  state: string;
  parent_job_id: string;
  upload_id: string;
  cancel_requested: boolean;
  error: string;
  revision: number;
  result?: unknown;
}

export type ImportTargetMode =
  | { kind: "new"; projectId: string }
  | { kind: "existing"; projectId: string; documentId: string; baseVersionId?: string | null };

export interface CreateUploadRequest {
  purpose: "result";
  input_mode: "directory" | "archive" | "file";
  selection_name?: string;
  target: { project_id: string; document_id: string | null };
}

/** Binary payload for one streamed entry chunk. */
export interface EntryChunk {
  path: string;
  kind: "file" | "directory" | "symlink";
  offset: number;
  bytes: Uint8Array;
  chunkSha256: string;
  totalLength?: number;
  final: boolean;
  linkTarget?: string;
}

/** Result of one accepted chunk: the server-confirmed end offset. */
export interface ChunkAck {
  accepted_offset: string;
  checkpoint: UploadCheckpoint;
}

export interface ImportPublishRequest {
  upload_id: string;
  document_id: string | null;
  project_id?: string;
  base_version_id?: string | null;
}

/** Authenticated streaming archive; the only download transport shape. */
export interface PackageStream {
  /** Streamed archive bytes; completion requires a clean end plus close(). */
  body: ReadableStream<Uint8Array>;
  cancel(): void;
}

export type UploadStateFilter = "open" | "sealing" | "sealed" | "abandoned";

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
  // Library reads
  listProjects(cursor?: string): Promise<ListPage<ProjectView>>;
  listDocuments(filter: { projectId?: string; lifecycle?: string; cursor?: string }): Promise<ListPage<DocumentView>>;
  documentDetail(documentId: string): Promise<DocumentView>;
  documentVersions(documentId: string, cursor?: string): Promise<ListPage<VersionView>>;
  versionDetail(versionId: string): Promise<VersionView>;
  versionFiles(versionId: string, cursor?: string): Promise<ListPage<FileEntryView>>;
  // Upload discovery and transfer
  listUploads(filter: { state?: UploadStateFilter; unadopted?: boolean; cursor?: string }): Promise<ListPage<UploadSummary>>;
  uploadDetail(uploadId: string): Promise<UploadDetail>;
  uploadEntries(uploadId: string, cursor?: string): Promise<ListPage<UploadEntryProgress>>;
  uploadRanges(uploadId: string, path?: string, cursor?: string): Promise<ListPage<UploadConfirmedRange>>;
  uploadPins(uploadId: string, cursor?: string): Promise<ListPage<UploadPin>>;
  createUpload(request: CreateUploadRequest, operationId: string): Promise<{ upload_id: string; checkpoint: UploadCheckpoint }>;
  putUploadEntry(uploadId: string, chunk: EntryChunk): Promise<ChunkAck>;
  completeUpload(uploadId: string, checkpoint: UploadCheckpoint, operationId: string): Promise<DecisionResult>;
  abandonUpload(uploadId: string, operationId: string): Promise<DecisionResult>;
  importPublish(request: ImportPublishRequest, operationId: string): Promise<DecisionResult>;
  // Jobs and archive export
  jobGet(jobId: string): Promise<JobView>;
  jobCancel(jobId: string, operationId: string): Promise<DecisionResult>;
  packageStream(versionId: string): Promise<PackageStream>;
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
