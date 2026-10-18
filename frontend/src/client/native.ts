// Narrow native transport boundary. The desktop companion (TASK-003-05) binds
// these named operations to a typed Go proxy. The port accepts no arbitrary
// URLs, filesystem paths or generic method names from imported content.
import type {
  Capabilities,
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
import { StaleResponseError } from "./types.ts";
import { eraGuardedStream } from "./stream.ts";

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
export type ListProjectsRequest = { cursor?: string };
export type ListDocumentsRequest = { project_id?: string; lifecycle?: string; cursor?: string };
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
  call(operation: "jobGet", payload: JobRequest): Promise<JobView>;
  call(operation: "jobCancel", payload: JobCancelCall): Promise<DecisionResult>;
  call(operation: "packageStream", payload: PackageStreamRequest): Promise<PackageStream>;
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
  "listProjects",
  "listDocuments",
  "documentDetail",
  "documentVersions",
  "versionDetail",
  "versionFiles",
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
  "jobCancel",
  "packageStream",
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

  async packageStream(versionId: string): Promise<PackageStream> {
    // Same freshness rule as the browser transport: the logical stream is
    // guarded chunk-by-chunk against the authority era captured here.
    const era = this.#era;
    const result = await this.#fresh(this.#port.call("packageStream", { version_id: versionId }), this.#era);
    return {
      body: eraGuardedStream(result.body, () => era !== this.#era),
      cancel: () => result.cancel(),
    };
  }

  listProjects(cursor?: string): Promise<ListPage<ProjectView>> {
    return this.#fresh(this.#port.call("listProjects", { cursor }), this.#era);
  }

  listDocuments(filter: { projectId?: string; lifecycle?: string; cursor?: string }): Promise<ListPage<DocumentView>> {
    return this.#fresh(this.#port.call("listDocuments", { project_id: filter.projectId, lifecycle: filter.lifecycle, cursor: filter.cursor }), this.#era);
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

  jobGet(jobId: string): Promise<JobView> {
    return this.#fresh(this.#port.call("jobGet", { job_id: jobId }), this.#era);
  }

  jobCancel(jobId: string, operationId: string): Promise<DecisionResult> {
    return this.#fresh(this.#port.call("jobCancel", { job_id: jobId, operation_id: operationId }), this.#era);
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
