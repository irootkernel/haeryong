import type {RetainedSourceRead,GenerationRequest,GenerationAccepted,DraftView,DraftPatch} from "./types.ts";
import { providerStatus as verifiedProviderStatus } from "./provider-status.ts";
// Narrow native transport boundary. The desktop companion (TASK-003-05) binds
// these named operations to a typed Go proxy. The port accepts no arbitrary
// URLs, filesystem paths or generic method names from imported content.
import type { ProviderStatus, LifecyclePlanRequest,LifecycleCommitRequest,LifecyclePlanView,LifecycleResult,LifecyclePageRequest,TrashRequest,TrashItem, AnnotationCreate, AnnotationPatch, AnnotationView, AnnotationStream, RevisitView, ReviewView, ReviewRequest, ReviewCondition, ReviewEvent, DocumentFilter,
  BacklinksRequest, BacklinkPage, IndexValidation, IndexValidationRequest, IndexRebuild, IndexRebuildRequest,
  VersionReferencesRequest, ResolveReferenceRequest, ReferencePage, ReferenceView,
  SearchRequest, SearchPage,
  ConfigurationView, ConfigurationPlanRequest, ConfigurationPlanView, ConfigurationCommitRequest, ConfigurationCommitView,
  ProjectSourceView, EvidenceVerificationRequest, EvidenceVerificationResult, SourceObservationRequest, SourceObservationResult,
  AssessmentFilter,
  AssessmentsView,
  Capabilities,
  CoverageUnit,
  DaemonDiscovery,
  DaemonStartOffer,
  EvidenceItemView,
  NativeAttachment,
  ChunkAck,
  CreateUploadRequest,
  CredentialAware,
  DecisionResult,
  DeviceSession,
  EntryChunk,
  EventSubscription,
  HaeryongClient,
  ImportPublishRequest,
  JobView,
  ListPage,
  NativePairingCreated,
  NativePairingPoll,
  NativeDestCloseResult,
  NativeDestIntent,
  NativeDestinationSelection,
  NativeSourceEntryPage,
  NativeSourceSelection,
  OperationView,
  PackageStream,
  PairingAdoption,
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
import { sourceWrite } from "./source-operations.ts";
import { StaleResponseError } from "./types.ts";
import { encodeBytes } from "./native-wire.ts";
import { eraGuardedStream } from "./stream.ts";
import type { ContextBinding } from "./context-types.ts";

export type ConfigurationViewCall = {};
export type ConfigurationPlanCall = { request: ConfigurationPlanRequest; operation_id: string };
export type ConfigurationPlanCommitCall = { plan_id: string; request: ConfigurationCommitRequest; operation_id: string };
export type ProjectRegisterCall = { name: string; operation_id: string };
export type ProjectSourcesCall = { project_id: string; cursor?: string };
export type VerifySourceEvidenceCall = { version_id: string; evidence_id: string; request: EvidenceVerificationRequest; operation_id: string };
export type RunSourceObservationCall = { version_id: string; request: SourceObservationRequest; operation_id: string };
export type PairRequest = { device_label: string };
export type PollPairingRequest = { pairing_id: string };
export type ChooseImportSourceRequest = { mode: "directory" | "archive" | "file" };
export type SourceEntriesRequest = { handle: string; cursor?: string };
export type ReadSourceRangeRequest = { handle: string; path: string; offset: number; length: number };
export type ReleaseSourceRequest = { handle: string };
export type SourceVerifyStableRequest = { handle: string };
export type ChooseDownloadDestinationRequest = { suggested_name: string; mode: "directory" | "save-file" };
export type DestOpenRequest = { handle: string; intent?: NativeDestIntent };
export type DestWriteRequest = { handle: string; bytes_b64: string };
export type DestHandleRequest = { handle: string };
export type DecidePairingPayload = {
  code: string;
  origin: string;
  etag: string;
  operation_id: string;
  action: PairingDecisionRequest["action"];
};
export type RevokeSessionRequest = { session_id: string };
export type OperationRequest = { operation_id: string };
export type BacklinksCall = { version_id: string; cursor?: string; limit?: number; page_path?: string; region_id?: string };
export type IndexValidationCall = { version_id: string; kind: "search" | "references" };
export type IndexRebuildCall = { version_id: string; kind: "search" | "references" | "all"; operation_id: string };
export type VersionReferencesCall = { version_id: string; cursor?: string; limit?: number };
export type ResolveReferenceCall = { version_id: string; reference_id: string; page_path?: string; region_id?: string };
export type SearchCall = SearchRequest;
export type ListProjectsRequest = { cursor?: string };
export type ListDocumentsRequest = { project_id?: string; lifecycle?: string; cursor?: string; revisit?:DocumentFilter["revisit"]; review?: DocumentFilter["review"] };
export type DocumentRequest = { document_id: string };
export type DocumentVersionsRequest = { document_id: string; cursor?: string };
export type VersionRequest = { version_id: string };
export type VersionFilesRequest = { version_id: string; cursor?: string };
export type ListUploadsRequest = { state?: UploadStateFilter; unadopted?: boolean; cursor?: string };
export type UploadRequest = { upload_id: string };
export type UploadEntriesRequest = { upload_id: string; cursor?: string };
export type UploadRangesRequest = { upload_id: string; path?: string; cursor?: string };
export type UploadPinsRequest = { upload_id: string; cursor?: string };
export type CreateUploadCall = { request: CreateUploadRequest; operation_id: string };
export type PutUploadEntryRequest = { upload_id: string; chunk: EntryChunk };
export type CompleteUploadCall = { upload_id: string; checkpoint: UploadCheckpoint; operation_id: string };
export type AbandonUploadCall = { upload_id: string; operation_id: string };
export type ImportPublishCall = { request: ImportPublishRequest; operation_id: string };
export type JobRequest = { job_id: string };
export type JobCancelCall = { job_id: string; operation_id: string };
export type PackageStreamRequest = { version_id: string };
export type VersionEvidenceRequest = { version_id: string; evidence_id: string };
export type EvidenceVerificationsRequest = { version_id: string; evidence_id: string; cursor?: string };
export type VersionAssessmentsRequest = { version_id: string; target_set_id?: string; scope_kind?: "version" | "page" | "region"; page_path?: string; region_id?: string; cursor?: string };
export type AssessmentCoverageRequest = { version_id: string; coverage_set_id: string; attempt_run_id?: string; cursor?: string };
export type SuppliedMetadataProof = { etag: string | null };
export type CreateContextUploadCall = { binding: ContextBinding; operation_id: string };
export type ConsumeContextCall = { binding: ContextBinding; upload_id: string; operation_id: string };
export type VerifyContextFreshCall = { context_ref: string; binding: ContextBinding; bytes_sha256: string };
export type ContextFreshProof = { verified: true; etag: string };
export type RenderBootstrapRequest = { version_id: string; page_path?: string };
export type RenderSessionOpenCall = { version_id: string; operation_id: string };
export type RenderSessionRouteCall = { version_id: string; session_id: string };
export type PersonalGetRequest = { kind: PersonalKind; version_id: string; page?: string };
export type PersonalPutCall = { kind: PersonalKind; version_id: string; page: string | null; body: unknown; precondition: { if_match: string } | { create: true }; operation_id: string };

/** Named operations only; each overload pins an exact payload/result pair. */
export interface NativePort {
  call(operation:"retainedSourceReads",payload:{project_id:string;source_id:string;cursor?:string}):Promise<ListPage<RetainedSourceRead>>;
  call(operation:"generationStart",payload:{request:GenerationRequest;operation_id:string}):Promise<DecisionResult>;
  call(operation:"draftList",payload:{project_id?:string;cursor?:string}):Promise<ListPage<DraftView>>;
  call(operation:"draftGet",payload:{draft_id:string}):Promise<DraftView>;
  call(operation:"draftHistory",payload:{draft_id:string;cursor?:string}):Promise<ListPage<DraftView>>;
  call(operation:"draftPatch",payload:{draft_id:string;body:DraftPatch;etag:string;operation_id:string}):Promise<DecisionResult>;
  call(operation:"draftPublish",payload:{draft_id:string;etag:string;operation_id:string}):Promise<DecisionResult>;
  call(operation:"lifecyclePlan",payload:{document_id:string;body:LifecyclePlanRequest;operation_id?:string}):Promise<LifecyclePlanView>;
  call(operation:"lifecyclePlanGet",payload:{plan_id:string;request:LifecyclePageRequest}):Promise<LifecyclePlanView>;
  call(operation:"lifecyclePlanCommit",payload:{plan_id:string;body:LifecycleCommitRequest;operation_id?:string}):Promise<LifecycleResult>;
  call(operation:"trashList",payload:{request:TrashRequest}):Promise<ListPage<TrashItem>>;

  call(operation:"annotationList",payload:{version_id:string;cursor?:string}):Promise<ListPage<AnnotationView>>;
  call(operation:"annotationGet",payload:{annotation_id:string}):Promise<AnnotationView>;
  call(operation:"annotationHistory",payload:{annotation_id:string;cursor?:string}):Promise<ListPage<AnnotationView>>;
  call(operation:"annotationCreate",payload:{version_id:string;body:AnnotationCreate;operation_id:string}):Promise<DecisionResult>;
  call(operation:"annotationPatch",payload:{annotation_id:string;body:AnnotationPatch;etag:string;operation_id:string}):Promise<DecisionResult>;
  call(operation:"annotationDelete",payload:{annotation_id:string;etag:string;operation_id:string}):Promise<DecisionResult>;
  call(operation:"annotationBodyStream"|"annotationContextStream",payload:{annotation_id:string;revision:number}):Promise<AnnotationStream>;
  call(operation:"revisitGet",payload:{document_id:string}):Promise<RevisitView>;
  call(operation:"revisitPut",payload:{document_id:string;flag:boolean;precondition:{if_match:string}|{create:true};operation_id:string}):Promise<DecisionResult>;

  call(operation: "configurationView", payload: ConfigurationViewCall): Promise<ConfigurationView>;
  call(operation: "configurationPlan", payload: ConfigurationPlanCall): Promise<DecisionResult>;
  call(operation: "configurationPlanCommit", payload: ConfigurationPlanCommitCall): Promise<DecisionResult>;
  call(operation: "projectRegister", payload: ProjectRegisterCall): Promise<DecisionResult>;
  call(operation: "projectSources", payload: ProjectSourcesCall): Promise<ListPage<ProjectSourceView>>;
  call(operation: "verifySourceEvidence", payload: VerifySourceEvidenceCall): Promise<DecisionResult>;
  call(operation: "runSourceObservation", payload: RunSourceObservationCall): Promise<DecisionResult>;
  call(operation: "attachSession", payload: {}): Promise<NativeAttachment>;
  call(operation: "disposeSession", payload: { attachment_id: string }): Promise<void>;
  call(operation: "fileStream", payload: { version_id: string; path: string }): Promise<PackageStream>;
  call(operation: "versionSuppliedMetadata", payload: { version_id: string }): Promise<PackageStream & SuppliedMetadataProof>;
  call(operation: "versionEvidence", payload: VersionEvidenceRequest): Promise<EvidenceItemView>;
  call(operation: "evidenceVerifications", payload: EvidenceVerificationsRequest): Promise<ListPage<VerificationRecord>>;
  call(operation: "versionAssessments", payload: VersionAssessmentsRequest): Promise<AssessmentsView>;
  call(operation: "assessmentCoverage", payload: AssessmentCoverageRequest): Promise<ListPage<CoverageUnit>>;
  call(operation: "createContextUpload", payload: CreateContextUploadCall): Promise<{ upload_id: string; checkpoint: UploadCheckpoint }>;
  call(operation: "consumeContext", payload: ConsumeContextCall): Promise<DecisionResult>;
  call(operation: "verifyContextFresh", payload: VerifyContextFreshCall): Promise<ContextFreshProof>;
  call(operation: "pair", payload: PairRequest): Promise<NativePairingCreated>;
  call(operation: "pollPairing", payload: PollPairingRequest): Promise<NativePairingPoll>;
  call(operation: "adoptPairing", payload: { pairing_id: string }): Promise<PairingAdoption>;
  call(operation: "abandonPairingAttempt", payload: { pairing_id: string }): Promise<{ abandoned: true }>;
  call(operation: "serviceDiscovery", payload: {}): Promise<DaemonDiscovery>;
  call(operation: "startDaemonOffer", payload: {}): Promise<DaemonStartOffer>;
  call(operation: "decidePairing", payload: PairingDecisionRequest & DecidePairingPayload): Promise<DecisionResult>;
  call(operation: "currentSession", payload: {}): Promise<SessionInfo>;
  call(operation: "capabilities", payload: {}): Promise<Capabilities>;
  call(operation: "listSessions", payload: {}): Promise<DeviceSession[]>;
  call(operation: "pendingPairings", payload: {}): Promise<PairingView[]>;
  call(operation: "operation", payload: OperationRequest): Promise<OperationView>;
  call(operation: "revokeSession", payload: RevokeSessionRequest): Promise<void>;
  call(operation: "revokeOwnSession", payload: {}): Promise<void>;
  call(operation: "serviceStatus", payload: {}): Promise<unknown>;
  call(operation: "providerStatus", payload: {}): Promise<ProviderStatus>;
  call(operation: "subscribeEvents", payload: {}): Promise<EventSubscription>;
  call(operation: "versionBacklinks", payload: BacklinksCall): Promise<BacklinkPage>;
  call(operation: "validateVersionIndexes", payload: IndexValidationCall): Promise<IndexValidation>;
  call(operation: "rebuildVersionIndexes", payload: IndexRebuildCall): Promise<DecisionResult>;
  call(operation: "versionReferences", payload: VersionReferencesCall): Promise<ReferencePage>;
  call(operation: "resolveReference", payload: ResolveReferenceCall): Promise<ReferenceView>;
  call(operation: "search", payload: SearchCall): Promise<SearchPage>;
  call(operation: "listProjects", payload: ListProjectsRequest): Promise<ListPage<ProjectView>>;
  call(operation: "listDocuments", payload: ListDocumentsRequest): Promise<ListPage<DocumentView>>;
  call(operation: "documentDetail", payload: DocumentRequest): Promise<DocumentView>;
  call(operation: "documentVersions", payload: DocumentVersionsRequest): Promise<ListPage<VersionView>>;
  call(operation: "versionDetail", payload: VersionRequest): Promise<VersionView>;
  call(operation: "versionFiles", payload: VersionFilesRequest): Promise<ListPage<FileEntryView>>;
  call(operation: "listUploads", payload: ListUploadsRequest): Promise<ListPage<UploadSummary>>;
  call(operation: "uploadDetail", payload: UploadRequest): Promise<UploadDetail>;
  call(operation: "uploadEntries", payload: UploadEntriesRequest): Promise<ListPage<UploadEntryProgress>>;
  call(operation: "uploadRanges", payload: UploadRangesRequest): Promise<ListPage<UploadConfirmedRange>>;
  call(operation: "uploadPins", payload: UploadPinsRequest): Promise<ListPage<UploadPin>>;
  call(operation: "createUpload", payload: CreateUploadCall): Promise<{ upload_id: string; checkpoint: UploadCheckpoint }>;
  call(operation: "putUploadEntry", payload: PutUploadEntryRequest): Promise<ChunkAck>;
  call(operation: "completeUpload", payload: CompleteUploadCall): Promise<DecisionResult>;
  call(operation: "abandonUpload", payload: AbandonUploadCall): Promise<DecisionResult>;
  call(operation: "importPublish", payload: ImportPublishCall): Promise<DecisionResult>;
  call(operation:"jobResume",payload:{job_id:string;decision:"reconcile_only"|"continue"|"retry";expected_attempt_generation:number;etag:string;operation_id:string}):Promise<DecisionResult>;
  call(operation: "jobGet", payload: JobRequest): Promise<JobView>;
  call(operation: "jobCancel", payload: JobCancelCall): Promise<DecisionResult>;
  call(operation: "packageStream", payload: PackageStreamRequest): Promise<PackageStream>;
  call(operation: "chooseImportSource", payload: ChooseImportSourceRequest): Promise<NativeSourceSelection>;
  call(operation: "sourceEntries", payload: SourceEntriesRequest): Promise<NativeSourceEntryPage>;
  call(operation: "readSourceRange", payload: ReadSourceRangeRequest): Promise<{ bytes_b64: string }>;
  call(operation: "releaseSource", payload: ReleaseSourceRequest): Promise<void>;
  call(operation: "sourceVerifyStable", payload: SourceVerifyStableRequest): Promise<{ stable: boolean; changed_path?: string }>;
  call(operation: "chooseDownloadDestination", payload: ChooseDownloadDestinationRequest): Promise<NativeDestinationSelection>;
  call(operation: "destOpen", payload: DestOpenRequest): Promise<{ opened: true }>;
  call(operation: "destWrite", payload: DestWriteRequest): Promise<{ written: number }>;
  call(operation: "destClose", payload: DestHandleRequest): Promise<NativeDestCloseResult>;
  call(operation: "destAbort", payload: DestHandleRequest): Promise<{ aborted: boolean; published?: boolean }>;
  call(operation: "renderBootstrap", payload: RenderBootstrapRequest): Promise<RenderBootstrapView>;
  call(operation: "renderSessionOpen", payload: RenderSessionOpenCall): Promise<RenderSessionView>;
  call(operation: "renderSessionRenew", payload: RenderSessionRouteCall): Promise<void>;
  call(operation: "renderSessionClose", payload: RenderSessionRouteCall): Promise<void>;
  call(operation: "reviewGet", payload: {version_id: string}): Promise<ReviewView>;
  call(operation: "reviewPut", payload: {version_id: string; body: ReviewRequest; precondition: {if_match: string} | {create: true}; operation_id: string}): Promise<DecisionResult>;
  call(operation: "reviewHistory", payload: {version_id: string; cursor?: string}): Promise<ListPage<ReviewEvent>>;
  call(operation: "personalGet", payload: PersonalGetRequest): Promise<PersonalView>;
  call(operation: "personalPut", payload: PersonalPutCall): Promise<PersonalView>;
}

export const NATIVE_OPERATIONS = [
 "retainedSourceReads","generationStart","draftList","draftGet","draftHistory","draftPatch","draftPublish",
  "lifecyclePlan", "lifecyclePlanGet", "lifecyclePlanCommit", "trashList",
  "annotationList", "annotationGet", "annotationHistory", "annotationCreate", "annotationPatch", "annotationDelete", "annotationBodyStream", "annotationContextStream", "revisitGet", "revisitPut",
  "reviewGet", "reviewPut", "reviewHistory",
  "configurationView",
  "configurationPlan",
  "configurationPlanCommit",
  "projectRegister",
  "projectSources",
  "verifySourceEvidence",
  "runSourceObservation",

  "attachSession", "disposeSession", "fileStream",
  "createContextUpload",
  "consumeContext",
  "verifyContextFresh",
  "pair",
  "pollPairing",
  "adoptPairing",
  "abandonPairingAttempt",
  "serviceDiscovery",
  "startDaemonOffer",
  "decidePairing",
  "currentSession",
  "capabilities",
  "listSessions",
  "pendingPairings",
  "operation",
  "revokeSession",
  "revokeOwnSession",
  "serviceStatus",
  "providerStatus",
  "subscribeEvents",
  "versionBacklinks", "validateVersionIndexes", "rebuildVersionIndexes",
  "versionReferences", "resolveReference",
  "search", "listProjects",
  "listDocuments",
  "documentDetail",
  "documentVersions",
  "versionDetail",
  "versionFiles",
  "versionSuppliedMetadata",
  "versionEvidence",
  "evidenceVerifications",
  "versionAssessments",
  "assessmentCoverage",
  "listUploads",
  "uploadDetail",
  "uploadEntries",
  "uploadRanges",
  "uploadPins",
  "createUpload",
  "putUploadEntry",
  "completeUpload",
  "abandonUpload",
  "importPublish",
  "jobGet",
  "jobResume",
  "jobCancel",
  "packageStream",
  "chooseImportSource",
  "sourceEntries",
  "readSourceRange",
  "releaseSource",
  "sourceVerifyStable",
  "chooseDownloadDestination",
  "destOpen",
  "destWrite",
  "destClose",
  "destAbort",
  "renderBootstrap",
  "renderSessionOpen",
  "renderSessionRenew",
  "renderSessionClose",
  "personalGet",
  "personalPut",
] as const;

/** HaeryongClient view over an injected NativePort. */
export class NativeHaeryongClient implements HaeryongClient, CredentialAware {
  readonly #port: NativePort;
  #attachment: string | null = null;
  #pendingDisposal: string | null = null;
  #publicOrigin: string | null = null;
  #era = 0;

  constructor(port: NativePort) {
    this.#port = port;
  }

  get publicOrigin(): string | null { return this.#publicOrigin; }

  // Native session authority stays in Go. A browser bearer or a made-up
  // token sentinel cannot bind this adapter.
  setCredential(token: string): void {
    if (token !== "") throw new Error("Native sessions attach through Go, not a JavaScript credential.");
  }

  async attachSession(): Promise<NativeAttachment> {
    const value = await this.#fresh(this.#port.call("attachSession", {}), this.#era);
    const origin = new URL(value.public_origin);
    if (!value.attachment_id || origin.origin !== value.public_origin || !["http:", "https:"].includes(origin.protocol)) throw new Error("Invalid native attachment metadata");
    if (value.attachment_id !== this.#attachment) { this.#era++; this.#pendingDisposal = null; }
    this.#attachment = value.attachment_id; this.#publicOrigin = value.public_origin;
    return value;
  }

  async disposeSession(): Promise<void> {
    const attachment = this.#attachment ?? this.#pendingDisposal;
    this.#attachment = null; this.#publicOrigin = null; this.#era++;
    if (attachment) {
      this.#pendingDisposal = attachment;
      await this.#port.call("disposeSession", { attachment_id: attachment });
      if (this.#pendingDisposal === attachment) this.#pendingDisposal = null;
    }
  }

  /**
   * Authority era change (credential/library/generation boundary). This never
   * erases the Go attachment by itself; explicit disposal carries its captured
   * nonsecret identity to Go. Responses and rejections crossing an invalidate()
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

  /**
   * Stream-open freshness with immediate local cleanup: an open that resolves
   * in a stale client era (for example a capabilities recheck invalidated the
   * client while the open was in flight) cancels its OWN acquired stream
   * before raising, so the Go-side record is released right away instead of
   * waiting for an unscheduled disposal/window close. The late handle is
   * never delivered to the caller.
   */
  async #freshStream<T extends { cancel(): void }>(open: () => Promise<T>, era: number): Promise<T> {
    const opened = await open().then(
      (value) => value,
      (error) => {
        if (era !== this.#era) throw new StaleResponseError();
        throw error;
      },
    );
    if (era !== this.#era) {
      opened.cancel();
      throw new StaleResponseError();
    }
    return opened;
  }

  pair(deviceLabel: string): Promise<PairingCreated | NativePairingCreated> {
    return this.#fresh(this.#port.call("pair", { device_label: deviceLabel }), this.#era);
  }

  pollPairing(pairingId: string, _pollingSecret?: string): Promise<PairingPoll | NativePairingPoll> {
    // The polling secret stays Go-side: the native flow names only the
    // pairing it still owns and never fabricates a secret.
    return this.#fresh(this.#port.call("pollPairing", { pairing_id: pairingId }), this.#era);
  }

  adoptPairing(pairingId: string): Promise<PairingAdoption> {
    return this.#fresh(this.#port.call("adoptPairing", { pairing_id: pairingId }), this.#era);
  }

  abandonPairing(pairingId: string): Promise<void> {
    return this.#fresh(this.#port.call("abandonPairingAttempt", { pairing_id: pairingId }), this.#era).then(() => undefined);
  }

  serviceDiscovery(): Promise<DaemonDiscovery> {
    return this.#fresh(this.#port.call("serviceDiscovery", {}), this.#era);
  }

  startDaemonOffer(): Promise<DaemonStartOffer> {
    return this.#fresh(this.#port.call("startDaemonOffer", {}), this.#era);
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

  providerStatus(): Promise<ProviderStatus> {
 return this.#fresh(this.#port.call("providerStatus", {}), this.#era).then(verifiedProviderStatus);
 }

  serviceStatus(): Promise<unknown> {
    return this.#fresh(this.#port.call("serviceStatus", {}), this.#era);
  }

  async packageStream(versionId: string): Promise<PackageStream> {
    // Same freshness rule as the browser transport: the logical stream is
    // guarded chunk-by-chunk against the authority era captured here, and a
    // stale-era open releases its Go-side stream record immediately.
    const era = this.#era;
    const result = await this.#freshStream(() => this.#port.call("packageStream", { version_id: versionId }), era);
    return {
      body: eraGuardedStream(result.body, () => era !== this.#era),
      cancel: () => result.cancel(),
    };
  }

  async fileStream(versionId: string, path: string): Promise<PackageStream> {
    const era = this.#era;
    const stream = await this.#freshStream(() => this.#port.call("fileStream", { version_id: versionId, path }), era);
    // The delivered representation's strong validator (when present)
    // survives the era wrapper beside the guarded body.
    return {
      body: eraGuardedStream(stream.body, () => era !== this.#era),
      cancel: () => stream.cancel(),
      ...(stream.validator ? { validator: stream.validator } : {}),
    };
  }

  renderBootstrap(versionId: string, pagePath?: string): Promise<RenderBootstrapView> {
    return this.#fresh(this.#port.call("renderBootstrap", { version_id: versionId, page_path: pagePath }), this.#era);
  }

  renderSessionOpen(versionId: string, operationId: string): Promise<RenderSessionView> {
    return this.#fresh(this.#port.call("renderSessionOpen", { version_id: versionId, operation_id: operationId }), this.#era);
  }

  renderSessionRenew(versionId: string, sessionId: string): Promise<void> {
    return this.#fresh(this.#port.call("renderSessionRenew", { version_id: versionId, session_id: sessionId }), this.#era);
  }

  renderSessionClose(versionId: string, sessionId: string): Promise<void> {
    return this.#fresh(this.#port.call("renderSessionClose", { version_id: versionId, session_id: sessionId }), this.#era);
  }


  annotationList(versionId:string,cursor?:string):Promise<ListPage<AnnotationView>>{return this.#fresh(this.#port.call("annotationList",{version_id:versionId,cursor}),this.#era);}
  annotationGet(id:string):Promise<AnnotationView>{return this.#fresh(this.#port.call("annotationGet",{annotation_id:id}),this.#era);}
  annotationHistory(id:string,cursor?:string):Promise<ListPage<AnnotationView>>{return this.#fresh(this.#port.call("annotationHistory",{annotation_id:id,cursor}),this.#era);}
  annotationCreate(versionId:string,body:AnnotationCreate,key:string):Promise<AnnotationView>{return this.#savedWrite(key,()=>this.#port.call("annotationCreate",{version_id:versionId,body,operation_id:key}));}
  annotationPatch(id:string,body:AnnotationPatch,etag:string,key:string):Promise<AnnotationView>{return this.#savedWrite(key,()=>this.#port.call("annotationPatch",{annotation_id:id,body,etag,operation_id:key}));}
  annotationDelete(id:string,etag:string,key:string):Promise<AnnotationView>{return this.#savedWrite(key,()=>this.#port.call("annotationDelete",{annotation_id:id,etag,operation_id:key}));}
  lifecyclePlan(id:string,body:LifecyclePlanRequest,key?:string):Promise<LifecyclePlanView>{return this.#fresh(this.#port.call("lifecyclePlan",{document_id:id,body,...(key?{operation_id:key}:{})}),this.#era);}
  lifecyclePlanGet(id:string,request:LifecyclePageRequest={}):Promise<LifecyclePlanView>{return this.#fresh(this.#port.call("lifecyclePlanGet",{plan_id:id,request}),this.#era);}
  lifecyclePlanCommit(id:string,body:LifecycleCommitRequest,key?:string):Promise<LifecycleResult>{return this.#fresh(this.#port.call("lifecyclePlanCommit",{plan_id:id,body,...(key?{operation_id:key}:{})}),this.#era);}
  trashList(request:TrashRequest={}):Promise<ListPage<TrashItem>>{return this.#fresh(this.#port.call("trashList",{request}),this.#era);}
  revisitGet(id:string):Promise<RevisitView>{return this.#fresh(this.#port.call("revisitGet",{document_id:id}),this.#era);}
  revisitPut(id:string,flag:boolean,condition:ReviewCondition,key:string):Promise<RevisitView>{return this.#savedWrite(key,()=>this.#port.call("revisitPut",{document_id:id,flag,precondition:"ifMatch" in condition?{if_match:condition.ifMatch}:{create:true},operation_id:key}));}
  #savedWrite<T>(key:string,dispatch:()=>Promise<DecisionResult>):Promise<T>{const era=this.#era;return sourceWrite<T>(key,()=>this.#fresh(dispatch(),era),id=>{if(era!==this.#era)throw new StaleResponseError();return this.operation(id);});}
  annotationBodyStream(id:string,revision:number):Promise<AnnotationStream>{return this.#savedStream("annotationBodyStream",id,revision);}
  annotationContextStream(id:string,revision:number):Promise<AnnotationStream>{return this.#savedStream("annotationContextStream",id,revision);}
  async #savedStream(operation:"annotationBodyStream"|"annotationContextStream",id:string,revision:number):Promise<AnnotationStream>{const era=this.#era;const s=await this.#freshStream(()=>this.#port.call(operation,{annotation_id:id,revision}),era);return {...s,body:eraGuardedStream(s.body,()=>era!==this.#era)};}

  generationStart(request:GenerationRequest,key:string):Promise<GenerationAccepted>{const era=this.#era;return sourceWrite<GenerationAccepted>(key,()=>this.#fresh(this.#port.call("generationStart",{request,operation_id:key}),era),id=>{if(era!==this.#era)throw new StaleResponseError();return this.operation(id);},true);}
  draftList(projectId?:string,cursor?:string):Promise<ListPage<DraftView>>{return this.#fresh(this.#port.call("draftList",{...(projectId?{project_id:projectId}:{}),...(cursor?{cursor}:{})}),this.#era);}
  draftGet(id:string):Promise<DraftView>{return this.#fresh(this.#port.call("draftGet",{draft_id:id}),this.#era);}
  draftHistory(id:string,cursor?:string):Promise<ListPage<DraftView>>{return this.#fresh(this.#port.call("draftHistory",{draft_id:id,...(cursor?{cursor}:{})}),this.#era);}
  draftPatch(id:string,body:DraftPatch,etag:string,key:string):Promise<DraftView>{return this.#savedWrite(key,()=>this.#port.call("draftPatch",{draft_id:id,body,etag,operation_id:key}));}
  draftPublish(id:string,etag:string,key:string):Promise<DraftView>{return this.#savedWrite(key,()=>this.#port.call("draftPublish",{draft_id:id,etag,operation_id:key}));}

  reviewGet(versionId: string): Promise<ReviewView> { return this.#fresh(this.#port.call("reviewGet", {version_id: versionId}), this.#era); }
  reviewPut(versionId: string, body: ReviewRequest, condition: ReviewCondition, operationId: string): Promise<ReviewView> {
    const precondition = "ifMatch" in condition ? {if_match: condition.ifMatch} : {create: true as const};
    const era=this.#era;
    return sourceWrite<ReviewView>(operationId,()=>this.#fresh(this.#port.call("reviewPut", {version_id: versionId, body, precondition, operation_id: operationId}),era),id=>{if(era!==this.#era)throw new StaleResponseError();return this.operation(id);});
  }
  reviewHistory(versionId: string, cursor?: string): Promise<ListPage<ReviewEvent>> { return this.#fresh(this.#port.call("reviewHistory", {version_id: versionId, cursor}), this.#era); }

  personalGet(kind: PersonalKind, versionId: string, page?: string): Promise<PersonalView> {
    return this.#fresh(this.#port.call("personalGet", { kind, version_id: versionId, page }), this.#era);
  }

  personalPut(kind: PersonalKind, versionId: string, page: string | null, body: unknown, precondition: { ifMatch: string } | { create: true }, operationId: string): Promise<PersonalView> {
    const wire = "ifMatch" in precondition ? { if_match: precondition.ifMatch } : { create: true as const };
    return this.#fresh(this.#port.call("personalPut", { kind, version_id: versionId, page, body, precondition: wire, operation_id: operationId }), this.#era);
  }

  configurationView(): Promise<ConfigurationView> { return this.#fresh(this.#port.call("configurationView", {}), this.#era); }
  retainedSourceReads(projectId:string,sourceId:string,cursor?:string):Promise<ListPage<RetainedSourceRead>> {return this.#fresh(this.#port.call("retainedSourceReads",{project_id:projectId,source_id:sourceId,cursor}),this.#era);}

  projectSources(projectId: string, cursor?: string): Promise<ListPage<ProjectSourceView>> {
    return this.#fresh(this.#port.call("projectSources", { project_id: projectId, cursor }), this.#era);
  }
  configurationPlan(request: ConfigurationPlanRequest, operationId: string): Promise<ConfigurationPlanView> {
    const era = this.#era;
    return sourceWrite<ConfigurationPlanView>(operationId, () => this.#fresh(this.#port.call("configurationPlan", { request, operation_id: operationId }), era), (id) => { if (era !== this.#era) throw new StaleResponseError(); return this.operation(id); });
  }
  configurationPlanCommit(planId: string, request: ConfigurationCommitRequest, operationId: string): Promise<ConfigurationCommitView> {
    const era = this.#era;
    return sourceWrite<ConfigurationCommitView>(operationId, () => this.#fresh(this.#port.call("configurationPlanCommit", { plan_id: planId, request, operation_id: operationId }), era), (id) => { if (era !== this.#era) throw new StaleResponseError(); return this.operation(id); });
  }
  projectRegister(name: string, operationId: string): Promise<ProjectView> {
    const era = this.#era;
    return sourceWrite<ProjectView>(operationId, () => this.#fresh(this.#port.call("projectRegister", { name, operation_id: operationId }), era), (id) => { if (era !== this.#era) throw new StaleResponseError(); return this.operation(id); });
  }
  verifySourceEvidence(versionId: string, evidenceId: string, request: EvidenceVerificationRequest, operationId: string): Promise<EvidenceVerificationResult> {
    const era = this.#era;
    return sourceWrite<EvidenceVerificationResult>(operationId, () => this.#fresh(this.#port.call("verifySourceEvidence", { version_id: versionId, evidence_id: evidenceId, request, operation_id: operationId }), era), (id) => { if (era !== this.#era) throw new StaleResponseError(); return this.operation(id); });
  }
  runSourceObservation(versionId: string, request: SourceObservationRequest, operationId: string): Promise<SourceObservationResult> {
    const era = this.#era;
    return sourceWrite<SourceObservationResult>(operationId, () => this.#fresh(this.#port.call("runSourceObservation", { version_id: versionId, request, operation_id: operationId }), era), (id) => { if (era !== this.#era) throw new StaleResponseError(); return this.operation(id); });
  }

  versionBacklinks(request: BacklinksRequest): Promise<BacklinkPage> {
    return this.#fresh(this.#port.call("versionBacklinks", { version_id: request.versionId, cursor: request.cursor, limit: request.limit, page_path: request.pagePath, region_id: request.regionId }), this.#era);
  }
  validateVersionIndexes(request: IndexValidationRequest): Promise<IndexValidation> {
    return this.#fresh(this.#port.call("validateVersionIndexes", { version_id: request.versionId, kind: request.kind }), this.#era);
  }
  rebuildVersionIndexes(request: IndexRebuildRequest): Promise<IndexRebuild> {
    const era = this.#era;
    return sourceWrite<IndexRebuild>(request.operationId, () => this.#fresh(this.#port.call("rebuildVersionIndexes", { version_id: request.versionId, kind: request.kind, operation_id: request.operationId }), era), id => { if (era !== this.#era) throw new StaleResponseError(); return this.operation(id); }, true);
  }
  versionReferences(request: VersionReferencesRequest): Promise<ReferencePage> {
    return this.#fresh(this.#port.call("versionReferences", { version_id: request.versionId, cursor: request.cursor, limit: request.limit }), this.#era);
  }
  resolveReference(request: ResolveReferenceRequest): Promise<ReferenceView> {
    return this.#fresh(this.#port.call("resolveReference", { version_id: request.versionId, reference_id: request.referenceId, page_path: request.pagePath, region_id: request.regionId }), this.#era);
  }

  search(request: SearchRequest): Promise<SearchPage> {
    return this.#fresh(this.#port.call("search", { ...request }), this.#era);
  }

  listProjects(cursor?: string): Promise<ListPage<ProjectView>> {
    return this.#fresh(this.#port.call("listProjects", { cursor }), this.#era);
  }

  listDocuments(filter: DocumentFilter): Promise<ListPage<DocumentView>> {
    return this.#fresh(this.#port.call("listDocuments", { project_id: filter.projectId, lifecycle: filter.lifecycle, review: filter.review, ...(filter.revisit!==undefined?{revisit:filter.revisit}:{}), cursor: filter.cursor }), this.#era);
  }

  documentDetail(documentId: string): Promise<DocumentView> {
    return this.#fresh(this.#port.call("documentDetail", { document_id: documentId }), this.#era);
  }

  documentVersions(documentId: string, cursor?: string): Promise<ListPage<VersionView>> {
    return this.#fresh(this.#port.call("documentVersions", { document_id: documentId, cursor }), this.#era);
  }

  versionDetail(versionId: string): Promise<VersionView> {
    return this.#fresh(this.#port.call("versionDetail", { version_id: versionId }), this.#era);
  }

  versionFiles(versionId: string, cursor?: string): Promise<ListPage<FileEntryView>> {
    return this.#fresh(this.#port.call("versionFiles", { version_id: versionId, cursor }), this.#era);
  }

  async versionSuppliedMetadata(versionId: string): Promise<SuppliedMetadataStream> {
    const era = this.#era;
    const opened = await this.#freshStream(() => this.#port.call("versionSuppliedMetadata", { version_id: versionId }), era);
    const { etag, ...stream } = opened;
    return { etag, stream: { body: eraGuardedStream(stream.body, () => era !== this.#era), cancel: stream.cancel } };
  }

  versionEvidence(versionId: string, evidenceId: string): Promise<EvidenceItemView> {
    return this.#fresh(this.#port.call("versionEvidence", { version_id: versionId, evidence_id: evidenceId }), this.#era);
  }

  evidenceVerifications(versionId: string, evidenceId: string, cursor?: string): Promise<ListPage<VerificationRecord>> {
    return this.#fresh(this.#port.call("evidenceVerifications", { version_id: versionId, evidence_id: evidenceId, cursor }), this.#era);
  }

  versionAssessments(versionId: string, filter?: AssessmentFilter & { cursor?: string }): Promise<AssessmentsView> {
    return this.#fresh(this.#port.call("versionAssessments", {
      version_id: versionId,
      target_set_id: filter?.targetSetId,
      scope_kind: filter?.scope?.kind,
      page_path: filter?.scope?.pagePath,
      region_id: filter?.scope?.regionId,
      cursor: filter?.cursor,
    }), this.#era);
  }

  assessmentCoverage(versionId: string, coverageSetId: string, cursor?: string, attemptRunId?: string): Promise<ListPage<CoverageUnit>> {
    return this.#fresh(this.#port.call("assessmentCoverage", { version_id: versionId, coverage_set_id: coverageSetId, ...(attemptRunId === undefined ? {} : { attempt_run_id: attemptRunId }), cursor }), this.#era);
  }

  listUploads(filter: { state?: UploadStateFilter; unadopted?: boolean; cursor?: string }): Promise<ListPage<UploadSummary>> {
    return this.#fresh(this.#port.call("listUploads", { state: filter.state, unadopted: filter.unadopted, cursor: filter.cursor }), this.#era);
  }

  uploadDetail(uploadId: string): Promise<UploadDetail> {
    return this.#fresh(this.#port.call("uploadDetail", { upload_id: uploadId }), this.#era);
  }

  uploadEntries(uploadId: string, cursor?: string): Promise<ListPage<UploadEntryProgress>> {
    return this.#fresh(this.#port.call("uploadEntries", { upload_id: uploadId, cursor }), this.#era);
  }

  uploadRanges(uploadId: string, path?: string, cursor?: string): Promise<ListPage<UploadConfirmedRange>> {
    return this.#fresh(this.#port.call("uploadRanges", { upload_id: uploadId, path, cursor }), this.#era);
  }

  uploadPins(uploadId: string, cursor?: string): Promise<ListPage<UploadPin>> {
    return this.#fresh(this.#port.call("uploadPins", { upload_id: uploadId, cursor }), this.#era);
  }

  createUpload(request: CreateUploadRequest, operationId: string): Promise<{ upload_id: string; checkpoint: UploadCheckpoint }> {
    return this.#fresh(this.#port.call("createUpload", { request, operation_id: operationId }), this.#era);
  }

  createContextUpload(binding: ContextBinding, operationId: string): Promise<{ upload_id: string; checkpoint: UploadCheckpoint }> {
    return this.#fresh(this.#port.call("createContextUpload", { binding, operation_id: operationId }), this.#era);
  }

  consumeContext(binding: ContextBinding, uploadId: string, operationId: string): Promise<DecisionResult> {
    return this.#fresh(this.#port.call("consumeContext", { binding, upload_id: uploadId, operation_id: operationId }), this.#era);
  }

  // Typed fresh-binding proof (the closed named native operation maps to the
  // same HTTP header pair Go-side; no byte stream is fabricated here). The
  // typed result must carry the exact expected digest as its ETag.
  async verifyContextFresh(contextRef: string, binding: ContextBinding, bytesSha256: string): Promise<void> {
    const proof = await this.#fresh(this.#port.call("verifyContextFresh", { context_ref: contextRef, binding, bytes_sha256: bytesSha256 }), this.#era);
    if (proof?.verified !== true || typeof proof.etag !== "string" || proof.etag !== `"sha256-${bytesSha256}"`) {
      throw new Error("native context proof returned an unverified result");
    }
  }

  putUploadEntry(uploadId: string, chunk: EntryChunk): Promise<ChunkAck> {
    return this.#fresh(this.#port.call("putUploadEntry", { upload_id: uploadId, chunk }), this.#era);
  }

  completeUpload(uploadId: string, checkpoint: UploadCheckpoint, operationId: string): Promise<DecisionResult> {
    return this.#fresh(this.#port.call("completeUpload", { upload_id: uploadId, checkpoint, operation_id: operationId }), this.#era);
  }

  abandonUpload(uploadId: string, operationId: string): Promise<DecisionResult> {
    return this.#fresh(this.#port.call("abandonUpload", { upload_id: uploadId, operation_id: operationId }), this.#era);
  }

  importPublish(request: ImportPublishRequest, operationId: string): Promise<DecisionResult> {
    return this.#fresh(this.#port.call("importPublish", { request, operation_id: operationId }), this.#era);
  }

  jobResume(jobId:string,decision:"reconcile_only"|"continue"|"retry",expectedGeneration:number,etag:string,key:string):Promise<unknown>{return this.#savedWrite(key,()=>this.#port.call("jobResume",{job_id:jobId,decision,expected_attempt_generation:expectedGeneration,etag,operation_id:key}));}

  jobGet(jobId: string): Promise<JobView> {
    return this.#fresh(this.#port.call("jobGet", { job_id: jobId }), this.#era);
  }

  jobCancel(jobId: string, operationId: string): Promise<DecisionResult> {
    return this.#fresh(this.#port.call("jobCancel", { job_id: jobId, operation_id: operationId }), this.#era);
  }

  // Owner-selected filesystem operations (TASK-003-05 M3). Every call is
  // era-guarded like the rest of the transport; the Go facade additionally
  // binds each opaque handle to its attachment/generation and purpose.
  chooseImportSource(mode: "directory" | "archive" | "file"): Promise<NativeSourceSelection> {
    return this.#fresh(this.#port.call("chooseImportSource", { mode }), this.#era);
  }

  sourceEntries(handle: string, cursor?: string): Promise<NativeSourceEntryPage> {
    return this.#fresh(this.#port.call("sourceEntries", { handle, cursor }), this.#era);
  }

  readSourceRange(handle: string, path: string, offset: number, length: number): Promise<{ bytes_b64: string }> {
    return this.#fresh(this.#port.call("readSourceRange", { handle, path, offset, length }), this.#era);
  }

  releaseSource(handle: string): Promise<void> {
    return this.#fresh(this.#port.call("releaseSource", { handle }), this.#era);
  }

  sourceVerifyStable(handle: string): Promise<{ stable: boolean; changed_path?: string }> {
    return this.#fresh(this.#port.call("sourceVerifyStable", { handle }), this.#era);
  }

  chooseDownloadDestination(suggestedName: string, mode: "directory" | "save-file"): Promise<NativeDestinationSelection> {
    return this.#fresh(this.#port.call("chooseDownloadDestination", { suggested_name: suggestedName, mode }), this.#era);
  }

  destOpen(handle: string, intent?: NativeDestIntent): Promise<{ opened: true }> {
    return this.#fresh(this.#port.call("destOpen", { handle, intent }), this.#era);
  }

  destWrite(handle: string, bytes: Uint8Array): Promise<{ written: number }> {
    return this.#fresh(this.#port.call("destWrite", { handle, bytes_b64: encodeBytes(bytes) }), this.#era);
  }

  destClose(handle: string): Promise<NativeDestCloseResult> {
    return this.#fresh(this.#port.call("destClose", { handle }), this.#era);
  }

  destAbort(handle: string): Promise<{ aborted: boolean; published?: boolean }> {
    return this.#fresh(this.#port.call("destAbort", { handle }), this.#era);
  }

  subscribeEvents(): EventSubscription {
    const listeners = new Set<() => void>();
    let unlisten: (() => void) | undefined;
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
        unlisten = subscription.onInvalidation?.(() => { if (!finished && era === this.#era) for (const listener of listeners) listener(); });
        void subscription.closed.then(() => ended());
      })
      .catch(() => ended());
    return {
      closed,
      onInvalidation: listener => { listeners.add(listener); return () => { listeners.delete(listener); }; },
      close: () => {
        finished = true;
        unlisten?.(); listeners.clear();
        handle?.close();
        ended();
      },
    };
  }
}
