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

/**
 * Native Go-held pairing creation: the polling secret stays in Go memory,
 * so the reduced native reply carries only the comparison-facing fields.
 * Browser clients keep receiving the full PairingCreated shape.
 */
export interface NativePairingCreated {
  pairing_id: string;
  code: string;
  expires_at: string;
}

export interface PairingPoll {
  state: string;
  session?: SessionInfo;
  token?: string;
}

/**
 * Native Go-held pairing poll: the redeemed bearer never crosses the wire
 * (no token member) and the approved session is the reduced Go-reported
 * identity subset, never a fabricated full SessionInfo.
 */
export interface NativePairingPoll {
  state: string;
  session?: {
    session_id: string;
    principal_id: string;
    library_id: string;
    device_label: string;
  };
}

/** Typed adoption outcome of the explicit native adoptPairing operation. */
export interface PairingAdoption {
  adopted: true;
  durability: "confirmed" | "unconfirmed";
}

/** Signed local service discovery states reported by the native facade. */
export type DaemonDiscoveryState =
  | "verified"
  | "uninitialized"
  | "daemon_stopped"
  | "discovery_corrupt"
  | "identity_mismatch"
  | "profile_error";

export interface DaemonDiscovery {
  state: DaemonDiscoveryState;
  profile: string;
  endpoint?: string;
  library_id?: string;
  instance_id?: string;
  detail?: string;
}

/** Explicit owner-requested sibling-daemon start through the exact argv. */
export interface DaemonStartOffer {
  started: boolean;
  discovery: DaemonDiscovery;
}

export interface Capabilities {
  library_id: string;
  api_version: string;
  runtime_generation: string;
  stream_epoch: string;
  unfiled_project_id: string;
  features: Record<string, boolean>;
  /** Present only when the renderer is unavailable for this request's ingress (remote callers see the loopback construction as unreachable). */
  renderer_unavailable_reason?: string;
}

/** Canonical receipt states: "completed" | "failed" (terminal), "reconciling" or pending (unresolved). */
export interface OperationView {
  operation_id: string;
  state: string;
  original_status: number | null;
  result_ref: unknown;
  job_id: string | null;
  result_metadata: unknown;
  result_etag?: string | null;
  error?: { code?: string; message?: string; retryable?: boolean; details?: Record<string, unknown> } | null;
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
  created_at?: string;
  expires_at?: string | null;
  revision?: number;
  etag?: string;
}

export interface EventStreamResync {
  reason: string;
  stream_epoch: string;
  head: string;
  resume_cursor: string;
}

export interface EventSubscription {
  /** Content-free change hint; callers must re-read under current authority. */
  onInvalidation?(listener: () => unknown): () => void;
  /** Explicit resync required signal when incremental event replay cannot continue or on bootstrap head capture. */
  onResync?(listener: (resync: EventStreamResync) => unknown): () => void;
  /** Consumer-acknowledged cursor update after an event or resync frame completes. */
  onCursor?(listener: (cursor: string) => void): () => void;
  closed: Promise<void>;
  close(): void;
}

/** Cursor list envelope shared by every collection route. */
export interface ListPage<T> {
  items: T[];
  next_cursor: string | null;
}

/** Owner configuration edits always preview a revision-bound project or library plan. */
export interface SourceMapping {
  source_id: string;
  kind: "git" | "files";
  root_path: string;
  repo_id: string | null;
  retention_mode: "reference_only" | "selected_snapshot";
  provider_ids: string[];
}
export interface ProjectConfiguration {
  sources: SourceMapping[];
  source_mode_default?: "pinned" | "current";
  provider_id?: string | null;
  [key: string]: unknown;
}
export interface SecurityConfiguration {
  remember_device_default: boolean;
  session_idle_seconds: number | null;
  session_absolute_seconds: number | null;
  pairing_expiry_seconds?: number;
  [key: string]: unknown;
}
export interface ConfigurationView {
  config_revision: number;
  etag: string;
  configuration: {
    projects: Record<string, ProjectConfiguration>;
    security?: SecurityConfiguration;
    [key: string]: unknown;
  };
}
export type ConfigurationScope =
  | { kind: "project"; project_id: string }
  | { kind: "library" };
export interface ConfigurationPlanRequest {
  scope: ConfigurationScope;
  expected_config_revision: number;
  changes: Record<string, unknown>;
}
export interface ConfigurationPlanView {
  plan_id: string;
  base_config_revision: number;
  expires_at: string;
  normalized_changes: Record<string, unknown>;
  affected_permissions_revisions: Record<string, number>;
  impact: Record<string, unknown>;
  [key: string]: unknown;
}
export interface ConfigurationCommitRequest { expected_config_revision: number; confirmed: boolean }
export interface ConfigurationCommitView { plan_id: string; config_revision: number; impact: Record<string, unknown> }
export interface ProjectSourceView {
  source_id: string;
  kind: string;
  repo_id: string | null;
  retention_mode: string;
  provider_ids: string[];
  last_observed_revision: string | null;
  last_observed_at: string | null;
  last_availability: string | null;
  last_observation_reason: string | null;
}
export interface EvidenceVerificationRequest {
  expected_bytes_sha256?: string;
  expected_excerpt?: string;
  expected_excerpt_ref?: { upload_id: string };
}
export interface EvidenceVerificationResult extends VerificationRecord {
  operation_id: string;
  version_id: string;
  evidence_id: string;
  target_set_id: string | null;
  source: Record<string, unknown>;
  location: Record<string, unknown>;
  bytes_sha256: string | null;
  bytes_origin: "approved_source" | "retained_snapshot" | null;
}
export interface SourceObservationRequest {
  source_ids?: string[];
  scope?: { kind: "version" | "page" | "region"; page_path?: string; region_id?: string };
}
export interface SourceObservationResult {
  operation_id: string;
  run_id: string;
  target_set_id: string | null;
  captured_at: string | null;
  observed_at: string;
  coverage_set_id: string | null;
  scope: { kind: "version" | "page" | "region"; page_path: string | null; region_id: string | null; declared: boolean; reason: string | null };
  sources: Array<{
    source_id: string;
    observation_id: string;
    availability: "available" | "unavailable" | "denied";
    classification: "direct" | "indirect" | "unchanged" | "unavailable" | "denied";
    reason: string | null;
    changed_paths: string[];
    dependency_scope: string[];
    reassessment_required: boolean;
  }>;
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


export interface AnnotationAnchor { version_id:string; page_path:string|null; region_id:string|null; page_query:string; page_fragment:string }
export interface AnnotationContent { body:string|null; body_ref:{upload_id:string}|null; selected_text:string|null; context:unknown|null; context_ref:string|null }
export type AnnotationCreate = Omit<AnnotationAnchor,"version_id"> & AnnotationContent;
export type AnnotationPatch = ({action:"edit"}&AnnotationContent) | {action:"reattach";anchor:AnnotationAnchor;selected_text:string|null;context:unknown|null;context_ref:string|null};
export interface AnnotationView { annotation_id:string; document_id:string; original_anchor:AnnotationAnchor; anchor:AnnotationAnchor; body:string|null; body_ref:{bytes_sha256:string;byte_size:string;media_type:string;content_url:string}|null; selected_text:string|null; context_ref:string|null;created_at:string;updated_at:string;deleted_at:string|null;revision:number;etag:string;history_url:string;resolution:{status:"resolved"|"unresolved";reason:string|null};action:"create"|"edit"|"reattach"|"delete";actor_session:string;operation_id?:string }
export interface AnnotationStream extends PackageStream {etag:string;bytes_sha256:string;byte_size:string;media_type:string}
export type LifecycleAction="archive"|"trash"|"restore"|"purge";
export interface LifecyclePlanRequest {operation:LifecycleAction;expected_revision:number}
export interface LifecycleCommitRequest {plan_id:string;expected_revision:number;confirmed:true}
export interface LifecycleItem {kind:string;id:string;version_id:string|null;page_path:string|null;region_id:string|null;revision:number;source_document_id:string|null;resolution:string|null}
export interface LifecycleResult {operation_id:string;plan_id:string;document_id:string;lifecycle:string;previous_lifecycle:string|null;trashed_at:string|null;revision:number;changed:boolean;job_id?:string;job_url?:string}
export interface LifecyclePlanView {plan_id:string;document_id:string;operation:LifecycleAction;state:"open"|"committed"|"expired";from_lifecycle:string;to_lifecycle:string;expected_revision:number;config_revision:number;expires_at:string;impact:Record<string,number>;result:LifecycleResult|null;items:LifecycleItem[];next_cursor:string|null;backup?:PurgeBackup}
export interface TrashItem {document_id:string;project_id:string;title:string;latest_version_id:string|null;revision:number;lifecycle:"trash";previous_lifecycle:"active"|"archived"|null;trashed_at:string|null;purge_eligible_at:string|null}
export interface LifecyclePageRequest {cursor?:string;limit?:number}
export interface TrashRequest extends LifecyclePageRequest {project_id?:string}
export interface RevisitView extends PersonalView {document_id:string;value:{flag:boolean};operation_id?:string}

export type ReviewState = "unreviewed" | "reviewing" | "reviewed";
export type ReviewAction = "start" | "complete" | "reopen" | "clear";
export type ReviewCondition = { ifMatch: string } | { create: true };
export interface ReviewRequest { action: ReviewAction; state: ReviewState }
export interface ReviewView {
  operation_id?: string;
  version_id: string; exists: boolean; value: { state: ReviewState; reviewed_at: string | null };
  revision: number | null; etag: string | null; page_position_url: null; required_precondition?: "if_none_match_star";
}
export interface ReviewEvent { event_id: string; version_id: string; action: ReviewAction; old_state: ReviewState; new_state: ReviewState; actor_session: string; operation_id: string; created_at: string }
export type ReviewFilter = "reviewing" | "updated_since_review";
export interface DocumentFilter { projectId?: string; lifecycle?: string; cursor?: string; revisit?: "true"|"false"; review?: ReviewFilter }

export interface DocumentView {
  document_id: string;
  project_id: string;
  title: string;
  lifecycle: string;
  latest_version_id: string | null;
  revision: number;
  updated_at: string;
  review_state?: ReviewState | null;
 revisit?: boolean|null;
  reviewed_at?: string | null;
  last_reviewed_version_id?: string | null;
  updated_since_review?: boolean | null;
  continue_review_version_id?: string | null;
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

/** Render bootstrap: selected-version context and delivery construction. */
export interface RenderBootstrapView {
  version_id: string;
  viewer_profile: string;
  default_page: string | null;
  selected_page: string | null;
  requested_page: string | null;
  navigation_path: string | null;
  files_url: string;
  selected_file_url: string | null;
  delivery: string;
  renderer_available: boolean;
  render_sessions_url: string;
  renderer_construction: string;
  renderer_unavailable_reason?: string;
}

/** Actual open wire shape (render.Service.Open): one-use handoff offer. */
export interface RenderSessionView {
  version_id: string;
  render_origin: string;
  session_id: string;
  handoff: { url: string; secret: string; expires_at: string };
  lease_expires_at: string;
}

export type PersonalKind = "reader-preference" | "reading-position" | "page-position";

/** Closed personal wire shape: mandatory nullable page_position_url on every family. */
export interface PersonalView {
  exists: boolean;
  value: unknown;
  revision: number | null;
  etag: string | null;
  required_precondition?: "if_none_match_star";
  page_position_url: string | null;
}

export interface ReadingPositionValue {
  page_path: string | null;
  page_query: string;
  page_fragment: string;
  opened: boolean;
  first_opened_at: string | null;
  last_opened_at: string | null;
}

export interface PagePositionValue {
  page_query: string;
  page_fragment: string;
  position: Record<string, unknown>;
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
 etag?:string;
 attempt_generation?:number;
 provider_attempts?:{count:number;unknown:number;latest_state:string;reconciliation_pending:number};
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

export interface ResultUploadRequest {
  purpose: "result";
  input_mode: "directory" | "archive" | "file";
  selection_name?: string;
  target: { project_id: string; document_id: string | null; draft_id?: string };
}

export type GenerationBodyUpload = {purpose:"body";input_mode:"object";target:{operation_id:string;action:"generation";project_id:string;document_id:string|null}};
export type DraftMetadataUpload = {purpose:"metadata";input_mode:"object";target:{operation_id:string;action:"draft_replace";draft_id:string}};
export type QuestionBodyUpload={purpose:"body";input_mode:"object";target:{operation_id:string;action:"question";version_id:string;page_path:string|null}};
export type DisplayContextUpload={purpose:"context";input_mode:"object";target:{version_id:string;page_path:string|null;render_instance_id:string|null}};
export type CreateUploadRequest = ResultUploadRequest | GenerationBodyUpload | DraftMetadataUpload | QuestionBodyUpload | DisplayContextUpload | {purpose:"body";input_mode:"object";target:({operation_id:string;action:"annotation_create";version_id:string;page_path:string|null}|{operation_id:string;action:"annotation_edit";annotation_id:string})};

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

/**
 * Authenticated streaming archive; the only download transport shape.
 * `validator` is present only when the delivered representation itself
 * carried a strong validator (file GET Repr-Digest + Content-Length through
 * the native facade); the browser transport never sets it.
 */
export interface PackageStream {
  /** Streamed archive bytes; completion requires a clean end plus close(). */
  body: ReadableStream<Uint8Array>;
  cancel(): void;
  /** Strong content validator of the SAME delivered response, when provided. */
  validator?: { sha256: string; byte_size: number };
}

/** One captured entry of an owner-selected native source tree. */
export interface NativeSourceEntry {
  path: string;
  kind: "file" | "directory" | "symlink";
  size?: number;
  link_target?: string;
}

/** Local cursor page of the captured source enumeration. */
export interface NativeSourceEntryPage {
  items: NativeSourceEntry[];
  next_cursor: string | null;
}

/** Owner-selected native import source (opaque, purpose/era-bound handle). */
export interface NativeSourceSelection {
  handle: string;
  selection_name: string;
  mode: "directory" | "archive" | "file";
  entries: NativeSourceEntryPage;
  limits: { empty_directories_represented: boolean; symlinks_represented: boolean };
  canceled?: true;
}

/** Owner-selected native download destination (exact existence probe). */
export interface NativeDestinationSelection {
  handle: string;
  label: string;
  exists: boolean;
  canceled?: true;
}

/** Typed publication outcome of the native destination close. */
export interface NativeDestCloseResult {
  published: true;
  durability: "confirmed" | "unconfirmed";
}

/** Owner-consented write intent for one destination attempt. */
export interface NativeDestIntent {
  replace?: boolean;
  expected?: { sha256: string; byte_size: number };
}

/**
 * Exact supplied-metadata stream plus the immutable object's strong validator.
 * The bytes are verified against this ETag (and the version's supplied
 * metadata digest when present) before anything parses them.
 */
export interface SuppliedMetadataStream {
  etag: string | null;
  stream: PackageStream;
}

/**
 * Immutable authored evidence item plus the separately composed service view.
 * `authored`/`source` are the exact stored metadata records; availability,
 * bytes_origin and reason describe the authorized service view (including
 * the neutral not_read_by_view state), and
 * service_verification/verifications_url expose the separate later checks
 * without ever upgrading the author's capture_status.
 */
export interface EvidenceItemView {
  version_id: string;
  evidence_id: string;
  authored: Record<string, unknown>;
  excerpt?: { bytes_sha256: string; data_base64?: string };
  source: Record<string, unknown>;
  availability: string;
  bytes_origin: string | null;
  reason: string | null;
  service_verification: Record<string, unknown> | null;
  verifications_url: string;
}

/**
 * One durable verification history record (registry shape; every field beyond
 * the identity set is presented only when actually present).
 */
export interface VerificationRecord {
  verification_id: string;
  outcome: string;
  checked_at: string | null;
  verified_at: string | null;
  reason?: string | null;
  [key: string]: unknown;
}

/** One published assessment history record (registry shape). */
export interface AssessmentRecord {
  assessment_id: string;
  version_id: string;
  scope: { kind: string; page_path: string | null; region_id: string | null };
  target_set_id: string | null;
  state?: string;
  [key: string]: unknown;
}

/**
 * One fixed required coverage unit tuple with its latest applicable outcome.
 * Counts and aggregate status derive ONLY from these records.
 */
export interface CoverageUnit {
  kind: string;
  page_path: string | null;
  region_id: string | null;
  outcome: string;
  reason: string | null;
  discrepancy?: unknown;
  assessment_id: string | null;
  target_set_id: string | null;
}

/** Scope filter for the shared assessment-history presentation adapter. */
export interface AssessmentScope {
  kind: "version" | "page" | "region";
  pagePath?: string;
  regionId?: string;
}

export interface AssessmentFilter {
  targetSetId?: string;
  scope?: AssessmentScope;
}

/**
 * Fixed required-coverage summary carried with the applicable freshness view.
 * `complete` is the recorded overlay badge only — the UI always re-derives
 * counts from the enumerated unit records and never turns zero/unknown
 * coverage aligned.
 */
export interface FreshnessCoverage {
  coverage_set_id: string | null;
  unit_kind: string;
  eligible: number;
  assessed: number;
  complete: boolean;
  units_url: string | null;
}

/**
 * The separately selected applicable freshness summary from the assessments
 * resource. Never inferred from an unrelated latest history item; with no
 * actual captured target the service reports target_set_id/captured_at/
 * coverage_set_id null with status not_assessed and complete false.
 */
export interface ApplicableFreshness {
  source_intent: string | null;
  target_set_id: string | null;
  captured_at: string | null;
  scope: { kind: string; page_path: string | null; region_id: string | null };
  status: string;
  coverage: FreshnessCoverage;
  assessed_at: string | null;
  reassessment_required: boolean;
}

/**
 * Assessment-history envelope: cursor pages of published records PLUS the
 * selected applicable summary as its own typed member. The summary is absent
 * (null) only when the response does not carry one; the UI then shows an
 * honest unknown state rather than deriving one from history items.
 */
export interface AssessmentsView extends ListPage<AssessmentRecord> {
  applicable: ApplicableFreshness | null;
}

export type UploadStateFilter = "open" | "sealing" | "sealed" | "abandoned";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryable: boolean;
  readonly retryAfter: number;
  readonly details: Record<string, unknown>;
  constructor(status: number, code: string, retryable: boolean, retryAfter = 0, details: Record<string, unknown> = {}) {
    super(`HTTP ${status} ${code}`);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.retryable = retryable;
    this.retryAfter = retryAfter;
    this.details = details;
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

// Static search preserves exact imported identities and safe aggregate reasons.
export type SearchKind = "document" | "page" | "region" | "source" | "attachment";
export interface SearchRequest {
  q: string;
  project_id?: string;
  lifecycle?: "active" | "archived" | "all";
  versions?: "latest" | "all";
  kind?: SearchKind;
  cursor?: string;
  limit?: number;
}
export type IndexReasonCode = "not_indexed" | "interrupted" | "cancelled" | "read_failed" | "decode_failed" | "integrity_failed" | "resource_exhausted" | "storage_failed" | "unsupported_body" | "failed_unknown";
export interface IndexReasonView { code: IndexReasonCode; version_count: string }
export interface SearchHit {
  kind: SearchKind; project_id: string; document_id: string; version_id: string;
  sequence: number; title: string; lifecycle: string;
  page_path: string | null; region_id: string | null; source_id: string | null;
  anchor_state: string; body_state: string; match_fields: string[]; snippet: string | null;
}
export interface SearchPage {
  query: { text: string; normalized: string; mode: string };
  filters: { project_id: string | null; lifecycle: string; versions: string; kind: SearchKind | null };
  items: SearchHit[]; next_cursor: string | null; total_matches: string;
  index: {
    state: "empty" | "pending" | "partial" | "complete" | "failed";
    versions_total: string | null; versions_complete: string | null; files_total: string | null; files_discovered: string | null;
    html_total: string | null; html_complete: string | null; html_pending: string | null; html_failed: string | null;
    unsupported_bodies: string | null; discovery_complete: boolean; limitations: string[]; reasons: IndexReasonView[];
  };
}

export interface VersionReferencesRequest { versionId: string; cursor?: string; limit?: number }
export interface ResolveReferenceRequest { versionId: string; referenceId: string; pagePath?: string; regionId?: string }
export interface ReferenceTarget {
  document_id: string; version_id: string; page_path: string | null; region_id: string | null;
  requested_path: string | null; resolved_path: string | null; page_query: string; page_fragment: string;
}
export interface ReferenceView {
  id: string; from: { page_path: string | null; region_id: string | null };
  relation: string | null; label: string | null;
  resolution: "resolved" | "unresolved" | "unavailable" | "tombstoned"; reason: string | null;
  edge_id: string | null; target: ReferenceTarget | Pick<ReferenceTarget, "document_id" | "version_id"> | null;
  reader_url: string | null;
}
export interface ReferencePage {
  counts: { edge_count: string; referenced_document_count: string };
  items: ReferenceView[]; next_cursor: string | null;
  materialization: { state: "pending" | "ready" | "failed"; reason: string | null };
}

export interface BacklinksRequest extends VersionReferencesRequest { pagePath?: string; regionId?: string }
export interface LinkEndpoint { document_id: string; version_id: string; page_path: string | null; region_id: string | null }
export interface Backlink { edge_id: string; reference_id: string; from: LinkEndpoint; target: LinkEndpoint; relation: string | null; label: string | null; reader_url: string }
export interface BacklinkPage { scope: { version_id: string; page_path: string | null; region_id: string | null }; items: Backlink[]; next_cursor: string | null; counts: { edge_count: string; referring_document_count: string } }
export type IndexValidationReason = "reverse_index_definition_missing" | "reverse_index_definition_changed" | "search_index_missing" | "search_owner_missing" | "search_index_incomplete" | "validation_changed" | "search_index_divergence" | "materialization_pending" | "materialization_incomplete" | "anchor_prerequisite_pending" | "reference_index_divergence";
export interface IndexValidation { version_id: string; kind: "search" | "references"; state: "consistent" | "inconsistent" | "incomplete"; counts: { checked: string; missing: string; unexpected: string; mismatched: string }; reasons: IndexValidationReason[] }
export interface IndexRebuild { version_id: string; kind: "search" | "references" | "all"; job_ids: string[]; operation_id: string }
export interface IndexValidationRequest { versionId: string; kind: "search" | "references" }
export interface IndexRebuildRequest { versionId: string; kind: "search" | "references" | "all"; operationId: string }

/** Metadata-only historical read; enumeration is not byte delivery or observed_input. */
export interface RetainedSourceRead {
 read_id:string;capture_id:string;captured_at:string;source_id:string;source:Record<string,unknown>|null;
 location:{path:string}|null;bytes_sha256:string;byte_size:number;retention_mode:string;
 availability:string;capture_availability?:string;read_state?:string;bytes_origin?:string;reason?:string;
}
export interface GenerationReference {version_id:string;page_path:string|null;region_id:string|null;bytes_sha256:string}
export interface BodyReference {object_id:string;bytes_sha256:string;byte_size:string;media_type:string}
export interface GenerationSourceSelection {
 source_mode?:"pinned"|"current";source_intent?:"historical"|"moving_target";provider_id?:string;
 selections:{source_id:string;revision?:string;working_copy?:boolean;paths:{path:string;read_id?:string}[]}[];
}
export interface GenerationRequest {
 project_id:string;document_id:string|null;base_version_id?:string|null;intent:"publish"|"draft";
 prompt?:string;body_ref?:BodyReference;title?:string;provider_id?:string;model?:string;effort?:string;
 source_selection:GenerationSourceSelection;explanation_references?:GenerationReference[];
}
export interface GenerationAccepted {operation_id:string;job_id:string;draft_id:string;intent:"publish"|"draft";job_url:string}
export interface DraftView {
 draft_id:string;project_id:string;document_id:string|null;base_version_id:string|null;job_id:string;
 state:"preparing"|"ready"|"published"|"rejected"|"failed";intent:"draft"|"publish";revision:number;
 created_at:string;updated_at:string;payload_sha256:string|null;metadata_sha256:string|null;
 published_version_id:string|null;failure_reason:string|null;etag:string;
}
export type DraftPatch={action:"reject"}|{action:"replace";upload_id:string;metadata_ref?:BodyReference};

export interface QuestionRequest {
 question?:string;body_ref?:BodyReference;page_path:string|null;region_id:string|null;
 page_query?:string;page_fragment?:string;context_ref?:string;display_state?:unknown;
 source_mode?:"pinned"|"current";source_selection?:GenerationSourceSelection;
 provider_id?:string;model?:string;effort?:string;
}
export interface QuestionAccepted {operation_id:string;question_id:string;job_id:string;job_url:string}
export interface QuestionLimitation {code:string;message:string}
export interface QuestionCitation {
 basis:"observed_input"|"posthoc_verified";read_id:string|null;verification_id:string|null;
 input_origin:"daemon_supplied"|"native_observed"|null;target_set_id:string;
 source:Record<string,unknown>;location:Record<string,unknown>;bytes_sha256:string;
 package_evidence_ref?:{version_id:string;evidence_id:string};
 availability:"available"|"unavailable"|"denied";bytes_origin:string|null;reason:string|null;
 observed_at:string|null;verified_at:string|null;
}
export interface AnswerView {
 answer_id:string;question_id:string;attempt_id:string;state:"final"|"partial"|"error";created_at:string;
 body_ref:BodyReference;body_url:string;citations:QuestionCitation[];
 explanation_references:GenerationReference[];limitations:QuestionLimitation[];
}
export interface QuestionView {
 question_id:string;project_id:string;document_id:string;version_id:string;job_id:string;
 job_state:string;job_error:string|null;latest_answer_id:string|null;operation_id:string;created_at:string;
 page_path:string|null;region_id:string|null;page_query:string;page_fragment:string;
 body_ref:BodyReference;text_url:string;context_ref:string|null;
 display_context:{context_ref:string;bytes_sha256:string;byte_size:string;media_type:string;render_instance_id:string|null}|null;
 explanation_reference:GenerationReference;source_mode:"pinned"|"current";target_set_id:string|null;
 targets:unknown[];requested_selection:GenerationSourceSelection;limitations:QuestionLimitation[];
 answers?:ListPage<AnswerView>;
}

export type ScanDepth = "change_focused" | "full_scope" | "deep_semantic";
export type FindingKind = "backlink_index_repair" | "related_explanation" | "topic_assignment" | "freshness_assessment" | "new_version_proposal" | "new_document_proposal";
export type FindingStatus = "proposed" | "applied" | "rejected" | "stale" | "blocked" | "undone";
export interface ScanSourceSelector {
  project_id?: string;
  source_id?: string;
  source_ids?: string[];
  mode?: "pinned" | "current";
  revision?: string;
  branch?: string;
  snapshot_id?: string;
  target_set_id?: string;
}
export interface ScanRequest {
  scope?: "selected_projects" | "all_allowed_projects";
  project_id?: string;
  project_ids?: string[];
  document_id?: string;
  document_ids?: string[];
  version_id?: string;
  path_prefix?: string;
  page_path?: string;
  region_id?: string;
  depth?: ScanDepth;
  source_mode?: "pinned" | "current";
  target_set_id?: string;
  sources?: ScanSourceSelector[];
  finding_id?: string;
  expected_finding_revision?: number;
}
export interface ScanAccepted {
  operation_id: string;
  job_id: string;
  coalesced: boolean;
  job: JobView;
}
export interface FindingView {
  finding_id: string;
  job_id: string;
  kind: FindingKind;
  origin: "deterministic" | "ai";
  project_id: string;
  document_id?: string | null;
  version_id?: string | null;
  page_path?: string | null;
  region_id?: string | null;
  target_set_id?: string | null;
  evidence: unknown;
  evidence_fingerprint: string;
  rationale: string;
  uncertainty: string;
  captured_config_revision: number;
  permissions_revision: number;
  proposal: unknown;
  status: FindingStatus;
  approval_required: boolean;
  reason?: string;
  applied_ref: unknown;
  decided_by?: string | null;
  decided_at?: string | null;
  revision: number;
  etag: string;
  created_at: string;
  updated_at: string;
}
export interface JobFindingsFilter {
  kind?: FindingKind;
  status?: FindingStatus;
  cursor?: string;
  limit?: number;
}

export interface HaeryongClient {
  /** Read-only view that suppresses session idle-window renewal while preserving current authority checks. */
  passive?(): HaeryongClient;
  scanStart?(request: ScanRequest, operationId: string): Promise<ScanAccepted>;
  jobFindingsList?(jobId: string, filter?: JobFindingsFilter): Promise<ListPage<FindingView>>;
  consumeContext(binding:{version_id:string;page_path:string|null;render_instance_id:string|null},uploadId:string,operationId:string):Promise<DecisionResult>;
  questionStart(versionId:string,request:QuestionRequest,operationId:string):Promise<QuestionAccepted>;
  questionList(versionId:string,cursor?:string):Promise<ListPage<QuestionView>>;
  questionGet(id:string):Promise<QuestionView>;
  questionAnswers(id:string,cursor?:string):Promise<ListPage<AnswerView>>;
  answerGet(id:string):Promise<AnswerView>;
  questionTextStream(id:string):Promise<AnnotationStream>;
  questionContextStream(id:string):Promise<AnnotationStream>;
  answerBodyStream(id:string):Promise<AnnotationStream>;
  answerCitationStream(id:string,index:number):Promise<AnnotationStream>;
  retainedSourceReads(projectId:string,sourceId:string,cursor?:string):Promise<ListPage<RetainedSourceRead>>;
  generationStart(request:GenerationRequest,operationId:string):Promise<GenerationAccepted>;
  draftList(projectId?:string,cursor?:string):Promise<ListPage<DraftView>>;
  draftGet(id:string):Promise<DraftView>;
  draftHistory(id:string,cursor?:string):Promise<ListPage<DraftView>>;
  draftPatch(id:string,body:DraftPatch,etag:string,operationId:string):Promise<DraftView>;
  draftPublish(id:string,etag:string,operationId:string):Promise<DraftView>;
  /** Trusted public browser origin, never a child-supplied navigation host. */
  readonly publicOrigin?: string | null;
  /** Browser clients receive the full creation reply; the native Go-held flow returns the reduced secret-free shape. */
  pair(deviceLabel: string): Promise<PairingCreated | NativePairingCreated>;
  /** The browser validates its actual polling secret; the native flow names only the pairing it still owns. */
  pollPairing(pairingId: string, pollingSecret?: string): Promise<PairingPoll | NativePairingPoll>;
  decidePairing(request: PairingDecisionRequest): Promise<DecisionResult>;
  currentSession(): Promise<SessionInfo>;
  capabilities(): Promise<Capabilities>;
  providerStatus(): Promise<ProviderStatus>;
  listSessions(): Promise<DeviceSession[]>;
  pendingPairings(): Promise<PairingView[]>;
  operation(operationId: string): Promise<OperationView>;
  revokeSession(sessionId: string): Promise<void>;
  revokeOwnSession(): Promise<void>;
  serviceStatus(): Promise<unknown>;
  subscribeEvents(): EventSubscription;
  // Revision-bound source/configuration operations
  configurationView(): Promise<ConfigurationView>;
  configurationPlan(request: ConfigurationPlanRequest, operationId: string): Promise<ConfigurationPlanView>;
  configurationPlanCommit(planId: string, request: ConfigurationCommitRequest, operationId: string): Promise<ConfigurationCommitView>;
  projectRegister(name: string, operationId: string): Promise<ProjectView>;
  projectSources(projectId: string, cursor?: string): Promise<ListPage<ProjectSourceView>>;
  verifySourceEvidence(versionId: string, evidenceId: string, request: EvidenceVerificationRequest, operationId: string): Promise<EvidenceVerificationResult>;
  runSourceObservation(versionId: string, request: SourceObservationRequest, operationId: string): Promise<SourceObservationResult>;
  versionBacklinks(request: BacklinksRequest): Promise<BacklinkPage>;
  validateVersionIndexes(request: IndexValidationRequest): Promise<IndexValidation>;
  rebuildVersionIndexes(request: IndexRebuildRequest): Promise<IndexRebuild>;
  versionReferences(request: VersionReferencesRequest): Promise<ReferencePage>;
  resolveReference(request: ResolveReferenceRequest): Promise<ReferenceView>;
  // Library reads
  search(request: SearchRequest): Promise<SearchPage>;
  listProjects(cursor?: string): Promise<ListPage<ProjectView>>;
  listDocuments(filter: DocumentFilter): Promise<ListPage<DocumentView>>;
  documentDetail(documentId: string): Promise<DocumentView>;
  documentVersions(documentId: string, cursor?: string): Promise<ListPage<VersionView>>;
  versionDetail(versionId: string): Promise<VersionView>;
  versionFiles(versionId: string, cursor?: string): Promise<ListPage<FileEntryView>>;
  /** Exact supplied-metadata bytes with their strong validator; absent metadata rejects 404 metadata_not_supplied. */
  versionSuppliedMetadata(versionId: string): Promise<SuppliedMetadataStream>;
  versionEvidence(versionId: string, evidenceId: string): Promise<EvidenceItemView>;
  evidenceVerifications(versionId: string, evidenceId: string, cursor?: string): Promise<ListPage<VerificationRecord>>;
  versionAssessments(versionId: string, filter?: AssessmentFilter & { cursor?: string }): Promise<AssessmentsView>;
  assessmentCoverage(versionId: string, coverageSetId: string, cursor?: string, attemptRunId?: string): Promise<ListPage<CoverageUnit>>;
  renderBootstrap(versionId: string, pagePath?: string): Promise<RenderBootstrapView>;
  renderSessionOpen(versionId: string, operationId: string): Promise<RenderSessionView>;
  renderSessionRenew(versionId: string, sessionId: string): Promise<void>;
  renderSessionClose(versionId: string, sessionId: string): Promise<void>;

  annotationList(versionId:string,cursor?:string):Promise<ListPage<AnnotationView>>;
  annotationGet(annotationId:string):Promise<AnnotationView>;
  annotationCreate(versionId:string,body:AnnotationCreate,operationId:string):Promise<AnnotationView>;
  annotationPatch(annotationId:string,body:AnnotationPatch,etag:string,operationId:string):Promise<AnnotationView>;
  annotationDelete(annotationId:string,etag:string,operationId:string):Promise<AnnotationView>;
  annotationHistory(annotationId:string,cursor?:string):Promise<ListPage<AnnotationView>>;
  annotationBodyStream(annotationId:string,revision:number):Promise<AnnotationStream>;
  annotationContextStream(annotationId:string,revision:number):Promise<AnnotationStream>;
  lifecyclePlan(documentId:string,body:LifecyclePlanRequest,key?:string):Promise<LifecyclePlanView>;
  lifecyclePlanGet(id:string,request?:LifecyclePageRequest):Promise<LifecyclePlanView>;
  lifecyclePlanCommit(id:string,body:LifecycleCommitRequest,key?:string):Promise<LifecycleResult>;
  trashList(request?:TrashRequest):Promise<ListPage<TrashItem>>;
  revisitGet(documentId:string):Promise<RevisitView>;
  revisitPut(documentId:string,flag:boolean,condition:ReviewCondition,operationId:string):Promise<RevisitView>;
  reviewGet(versionId: string): Promise<ReviewView>;
  reviewPut(versionId: string, body: ReviewRequest, condition: ReviewCondition, operationId: string): Promise<ReviewView>;
  reviewHistory(versionId: string, cursor?: string): Promise<ListPage<ReviewEvent>>;
  personalGet(kind: PersonalKind, versionId: string, page?: string): Promise<PersonalView>;
  personalPut(kind: PersonalKind, versionId: string, page: string | null, body: unknown, precondition: { ifMatch: string } | { create: true }, operationId: string): Promise<PersonalView>;
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
  jobResume(jobId:string,decision:"reconcile_only"|"continue"|"retry",expectedGeneration:number,etag:string,operationId:string):Promise<unknown>;
  jobCancel(jobId: string, operationId: string): Promise<DecisionResult>;
  packageStream(versionId: string): Promise<PackageStream>;
  fileStream?(versionId: string, path: string): Promise<PackageStream>;
}

/** Public metadata from an authenticated Go-held session, never its bearer. */
export interface NativeAttachment {
  attachment_id: string;
  session: SessionInfo;
  capabilities: Capabilities;
  public_origin: string;
}

/** Transport facet the shell controller uses to bind and invalidate eras. */
export interface CredentialAware {
  attachSession?(): Promise<NativeAttachment>;
  disposeSession?(): Promise<void>;
  setCredential(token: string): void;
  invalidate(): void;
  /** Native-only onboarding operations; the browser client never implements them. */
  adoptPairing?(pairingId: string): Promise<PairingAdoption>;
  abandonPairing?(pairingId: string): Promise<void>;
  serviceDiscovery?(): Promise<DaemonDiscovery>;
  startDaemonOffer?(): Promise<DaemonStartOffer>;
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

export interface BackupComplete {set_id:string;completed_at:string;verified_at:string;age_seconds:number}
export interface BackupAttempt {job_id:string;state:string;stage:string;completed_at:string|null;error?:string}
export interface TrialRestore {state:"not_run"|"passed"|"failed";completed_at:string|null;error?:string}
export interface BackupStatus {mode:"manual";available:boolean;reason?:string;last_complete_verified:BackupComplete|null;last_attempt:BackupAttempt|null;last_trial_restore:TrialRestore}
export interface PurgeBackup {state:"not_created"|"verified"|"unavailable";set_id:string|null;last_complete_at:string|null;reason:string|null;last_attempt:BackupAttempt|null;last_trial_restore:TrialRestore}

export type ProviderState = "ready" | "auth_required" | "quota_unavailable" | "quota_exhausted" | "unsupported_version" | "source_unavailable" | "paused" | "provider_error";
export interface ProviderStatus {
 input_formats: string[] | null;
 output_format: string;
 native_input_telemetry: boolean;
 reason?: string;
 provider_id: string;
 state: ProviderState;
 version?: string;
 authentication_mode: string;
 model?: string;
 effort?: string;
 ordinary_usage_allowed: boolean | null;
 read_only: boolean;
 lookup_supported: boolean;
 cancellation_supported: boolean;
 models: string[];
 catalog_proves_access: false;
}
