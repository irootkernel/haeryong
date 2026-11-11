import type {FindingActionResult,TopicAssignmentChange,ManagementEffect,ManagementFilter} from "./types.ts";
import type {ScanRequest,ScanAccepted,FindingView,JobFindingsFilter,QuestionRequest,QuestionAccepted,QuestionView,AnswerView,RetainedSourceRead,GenerationRequest,GenerationAccepted,DraftView,DraftPatch} from "./types.ts";
import { providerStatus as verifiedProviderStatus } from "./provider-status.ts";
import type { ProviderStatus, LifecyclePlanRequest,LifecycleCommitRequest,LifecyclePlanView,LifecycleResult,LifecyclePageRequest,TrashRequest,TrashItem, AnnotationCreate, AnnotationPatch, AnnotationView, AnnotationStream, RevisitView, ReviewView, ReviewRequest, ReviewCondition, ReviewEvent, DocumentFilter,
  BacklinksRequest, BacklinkPage, IndexValidation, IndexValidationRequest, IndexRebuild, IndexRebuildRequest,
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
  EventStreamResync,
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

interface HttpClientState {
  credential: string;
  era: number;
  eventCursor: string | null;
}

// Browser transport on the trusted origin. Header bearer only; cookies are
// omitted, redirects are errors and only same-origin /api/v1 paths are called.
// Every request captures the current era; responses from an earlier
// credential/library/generation era are discarded before being applied.
export class HttpHaeryongClient implements HaeryongClient {
  get publicOrigin(): string { return location.origin; }

  readonly #state: HttpClientState;
  readonly #passive: boolean;
  #passiveView: HttpHaeryongClient | null = null;

  constructor(state?: HttpClientState, passive = false) {
    this.#state = state ?? { credential: "", era: 0, eventCursor: null };
    this.#passive = passive;
  }

  get #credential(): string { return this.#state.credential; }
  get #era(): number { return this.#state.era; }

  passive(): HaeryongClient {
    if (this.#passive) return this;
    if (!this.#passiveView) {
      this.#passiveView = new HttpHaeryongClient(this.#state, true);
    }
    return this.#passiveView;
  }

  setCredential(token: string): void {
    if (this.#state.credential !== token) {
      this.#state.eventCursor = null;
    }
    this.#state.credential = token;
  }

  invalidate(): void {
    this.#state.era += 1;
    this.#state.eventCursor = null;
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
    if (this.#passive && method !== "GET" && method !== "HEAD") {
      throw new ApiError(400, "INVALID_REQUEST", false);
    }
    const era = this.#era;
    const allHeaders = new Headers(headers);
    if (this.#credential) allHeaders.set("Authorization", "Bearer " + this.#credential);
    if (this.#passive) allHeaders.set("Haeryong-Passive-Read", "1");
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
    if (this.#passive) {
      throw new ApiError(400, "INVALID_REQUEST", false);
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

  retainedSourceReads(projectId:string,sourceId:string,cursor?:string):Promise<ListPage<RetainedSourceRead>> {return this.#request("GET",this.#collection(`/api/v1/projects/${encodeURIComponent(projectId)}/source-reads`,{source_id:sourceId,cursor}));}

  findingDecision(id:string,decision:"approve"|"reject",etag:string,key:string):Promise<FindingActionResult>{
    return this.#findingWrite("POST",`/api/v1/findings/${encodeURIComponent(id)}/decisions`,{decision},etag,key);
  }
  topicAssignmentSet(id:string,body:TopicAssignmentChange,etag:string,key:string):Promise<FindingActionResult>{
    return this.#findingWrite("PUT",`/api/v1/topic-assignments/${encodeURIComponent(id)}`,body,etag,key);
  }
  overlayUndo(id:string,etag:string,key:string):Promise<FindingActionResult>{
    return this.#findingWrite("POST",`/api/v1/overlays/${encodeURIComponent(id)}/undo`,{},etag,key);
  }
  #findingWrite(method:string,path:string,body:unknown,etag:string,key:string):Promise<FindingActionResult>{
    const era=this.#era;
    return sourceWrite<FindingActionResult>(key,async()=>{
      const r=await this.#exchange(method,path,body,{"Idempotency-Key":key,"If-Match":etag});
      const queued=r.status===202;
      const result=r.data as Partial<FindingActionResult>;
      return {kind:queued&&!(result.operation_id===key&&result.state==="repair_pending")?"accepted":"committed",body:r.data};
    },id=>{if(era!==this.#era)throw new StaleResponseError();return this.operation(id);},true);
  }
  generationStart(request:GenerationRequest,key:string):Promise<GenerationAccepted>{return this.#sourceWrite("/api/v1/generations",request,key,true);}
  scanStart(request:ScanRequest,key:string):Promise<ScanAccepted>{return this.#sourceWrite("/api/v1/scans",request,key,true);}
  jobsList():Promise<ListPage<JobView>>{return this.#request("GET","/api/v1/jobs");}
  versionRelated(versionId:string,filter:ManagementFilter={}):Promise<ListPage<ManagementEffect>>{return this.#request("GET",this.#collection("/api/v1/versions/"+encodeURIComponent(versionId)+"/related",{page_path:filter.page_path,region_id:filter.region_id,cursor:filter.cursor,limit:filter.limit===undefined?undefined:String(filter.limit)}));}
  topicAssignmentsList(filter:ManagementFilter={}):Promise<ListPage<ManagementEffect>>{return this.#request("GET",this.#collection("/api/v1/topic-assignments",{document_id:filter.document_id,version_id:filter.version_id,page_path:filter.page_path,region_id:filter.region_id,cursor:filter.cursor,limit:filter.limit===undefined?undefined:String(filter.limit)}));}
  jobFindingsList(jobId:string,filter:JobFindingsFilter={}):Promise<ListPage<FindingView>>{return this.#request("GET",this.#collection(`/api/v1/jobs/${encodeURIComponent(jobId)}/findings`,{kind:filter.kind,status:filter.status,cursor:filter.cursor,limit:filter.limit===undefined?undefined:String(filter.limit)}));}
  questionStart(id:string,request:QuestionRequest,key:string):Promise<QuestionAccepted>{return this.#sourceWrite(`/api/v1/versions/${encodeURIComponent(id)}/questions`,request,key,true);}
  questionList(id:string,cursor?:string):Promise<ListPage<QuestionView>>{return this.#request("GET",this.#collection(`/api/v1/versions/${encodeURIComponent(id)}/questions`,{cursor}));}
  questionGet(id:string):Promise<QuestionView>{return this.#request("GET",`/api/v1/questions/${encodeURIComponent(id)}`);}
  questionAnswers(id:string,cursor?:string):Promise<ListPage<AnswerView>>{return this.#request("GET",this.#collection(`/api/v1/questions/${encodeURIComponent(id)}/answers`,{cursor}));}
  answerGet(id:string):Promise<AnswerView>{return this.#request("GET",`/api/v1/answers/${encodeURIComponent(id)}`);}
  questionTextStream(id:string):Promise<AnnotationStream>{return this.#savedTextStream(`/api/v1/questions/${encodeURIComponent(id)}/text`);}
  questionContextStream(id:string):Promise<AnnotationStream>{return this.#savedTextStream(`/api/v1/questions/${encodeURIComponent(id)}/context`);}
  answerBodyStream(id:string):Promise<AnnotationStream>{return this.#savedTextStream(`/api/v1/answers/${encodeURIComponent(id)}/body`);}
  answerCitationStream(id:string,index:number):Promise<AnnotationStream>{if(!Number.isSafeInteger(index)||index<0)throw Error("Invalid citation index");return this.#savedTextStream(`/api/v1/answers/${encodeURIComponent(id)}/citations/${index}/source`);}
  draftList(projectId?:string,cursor?:string):Promise<ListPage<DraftView>>{return this.#request("GET",this.#collection("/api/v1/drafts",{project_id:projectId,cursor}));}
  draftGet(id:string):Promise<DraftView>{return this.#request("GET",`/api/v1/drafts/${encodeURIComponent(id)}`);}
  draftHistory(id:string,cursor?:string):Promise<ListPage<DraftView>>{return this.#request("GET",this.#collection(`/api/v1/drafts/${encodeURIComponent(id)}/history`,{cursor}));}
  draftPatch(id:string,body:DraftPatch,etag:string,key:string):Promise<DraftView>{return this.#noteWrite("PATCH",`/api/v1/drafts/${encodeURIComponent(id)}`,body,key,{"If-Match":etag});}
  draftPublish(id:string,etag:string,key:string):Promise<DraftView>{return this.#noteWrite("POST",`/api/v1/drafts/${encodeURIComponent(id)}/publish`,{},key,{"If-Match":etag});}

  configurationView(): Promise<ConfigurationView> { return this.#request("GET", "/api/v1/configuration"); }

  #sourceWrite<T>(path: string, body: unknown, operationId: string, acceptCreatedJob = false): Promise<T> {
    const era = this.#era;
    return sourceWrite<T>(operationId, async () => {
      const outcome = await this.#exchange("POST", path, body, { "Idempotency-Key": operationId });
      return { kind: outcome.status === 202 ? "accepted" : "committed", body: outcome.data };
    }, (id) => { if (era !== this.#era) throw new StaleResponseError(); return this.operation(id); }, acceptCreatedJob);
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

  versionBacklinks(request: BacklinksRequest): Promise<BacklinkPage> {
    const params = new URLSearchParams();
    if (request.cursor !== undefined) params.set("cursor", request.cursor);
    if (request.limit !== undefined) params.set("limit", String(request.limit));
    if (request.pagePath !== undefined) params.set("page_path", request.pagePath);
    if (request.regionId !== undefined) params.set("region_id", request.regionId);
    return this.#request("GET", `/api/v1/versions/${encodeURIComponent(request.versionId)}/backlinks?${params}`);
  }
  validateVersionIndexes(request: IndexValidationRequest): Promise<IndexValidation> {
    return this.#request("GET", `/api/v1/versions/${encodeURIComponent(request.versionId)}/indexes/validate?${new URLSearchParams({ kind: request.kind })}`);
  }
  rebuildVersionIndexes(request: IndexRebuildRequest): Promise<IndexRebuild> {
    return this.#sourceWrite(`/api/v1/versions/${encodeURIComponent(request.versionId)}/indexes/rebuild`, { kind: request.kind }, request.operationId, true);
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

  listDocuments(filter: DocumentFilter): Promise<ListPage<DocumentView>> {
    return this.#request("GET", this.#collection("/api/v1/documents", { project_id: filter.projectId, lifecycle: filter.lifecycle, review: filter.review, revisit:filter.revisit, cursor: filter.cursor }));
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
    if (this.#passive) headers.set("Haeryong-Passive-Read", "1");
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


  annotationList(versionId:string,cursor?:string):Promise<ListPage<AnnotationView>> {return this.#request("GET",this.#collection(`/api/v1/versions/${encodeURIComponent(versionId)}/annotations`,{cursor}));}
  annotationGet(id:string):Promise<AnnotationView>{return this.#request("GET",`/api/v1/annotations/${encodeURIComponent(id)}`);}
  annotationHistory(id:string,cursor?:string):Promise<ListPage<AnnotationView>> {return this.#request("GET",this.#collection(`/api/v1/annotations/${encodeURIComponent(id)}/history`,{cursor}));}
  annotationCreate(versionId:string,body:AnnotationCreate,key:string):Promise<AnnotationView>{return this.#noteWrite("POST",`/api/v1/versions/${encodeURIComponent(versionId)}/annotations`,body,key);}
  annotationPatch(id:string,body:AnnotationPatch,etag:string,key:string):Promise<AnnotationView>{return this.#noteWrite("PATCH",`/api/v1/annotations/${encodeURIComponent(id)}`,body,key,{"If-Match":etag});}
  annotationDelete(id:string,etag:string,key:string):Promise<AnnotationView>{return this.#noteWrite("DELETE",`/api/v1/annotations/${encodeURIComponent(id)}`,undefined,key,{"If-Match":etag});}
  lifecyclePlan(id:string,body:LifecyclePlanRequest,key?:string):Promise<LifecyclePlanView>{return this.#request("POST",`/api/v1/documents/${encodeURIComponent(id)}/lifecycle-plans`,body,key?{"Idempotency-Key":key}:{});}
  lifecyclePlanGet(id:string,request:LifecyclePageRequest={}):Promise<LifecyclePlanView>{return this.#request("GET",this.#collection(`/api/v1/lifecycle-plans/${encodeURIComponent(id)}`,{cursor:request.cursor,limit:request.limit===undefined?undefined:String(request.limit)}));}
  lifecyclePlanCommit(id:string,body:LifecycleCommitRequest,key?:string):Promise<LifecycleResult>{return this.#request("POST",`/api/v1/lifecycle-plans/${encodeURIComponent(id)}/commit`,body,key?{"Idempotency-Key":key}:{});}
  trashList(request:TrashRequest={}):Promise<ListPage<TrashItem>>{return this.#request("GET",this.#collection("/api/v1/trash",{cursor:request.cursor,limit:request.limit===undefined?undefined:String(request.limit),project_id:request.project_id}));}
  revisitGet(id:string):Promise<RevisitView>{return this.#request("GET",`/api/v1/documents/${encodeURIComponent(id)}/revisit`);}
  revisitPut(id:string,flag:boolean,condition:ReviewCondition,key:string):Promise<RevisitView>{return this.#noteWrite("PUT",`/api/v1/documents/${encodeURIComponent(id)}/revisit`,{flag},key,"ifMatch" in condition?{"If-Match":condition.ifMatch}:{"If-None-Match":"*"});}
  #noteWrite<T>(method:string,path:string,body:unknown,key:string,conditions:Record<string,string>={}):Promise<T>{
    const era=this.#era;
    return sourceWrite<T>(key,async()=>{const r=await this.#exchange(method,path,body,{"Idempotency-Key":key,...conditions});return {kind:r.status===202?"accepted":"committed",body:r.data};},id=>{if(era!==this.#era)throw new StaleResponseError();return this.operation(id);});
  }
  annotationBodyStream(id:string,revision:number):Promise<AnnotationStream>{return this.#annotationStream(id,revision,"body");}
  annotationContextStream(id:string,revision:number):Promise<AnnotationStream>{return this.#annotationStream(id,revision,"context");}
  async #annotationStream(id:string,revision:number,kind:"body"|"context"):Promise<AnnotationStream>{
    return this.#savedTextStream(`/api/v1/annotations/${encodeURIComponent(id)}/${kind}?${new URLSearchParams({revision:String(revision)})}`);
  }
  async #savedTextStream(path:string):Promise<AnnotationStream>{
    const controller=new AbortController(),era=this.#era;
    const headers=new Headers();if(this.#credential)headers.set("Authorization","Bearer "+this.#credential);
    if(this.#passive)headers.set("Haeryong-Passive-Read","1");
    const r=await fetch(path,{headers,credentials:"omit",redirect:"error",signal:controller.signal});
    if(era!==this.#era){void r.body?.cancel();throw new StaleResponseError();}
    if(!r.ok||!r.body)throw await this.#apiError(r,era,false);
    const etag=r.headers.get("ETag")??"",size=r.headers.get("Content-Length")??"",media=r.headers.get("Content-Type")??"";
    if(!/^"sha256-[0-9a-f]{64}"$/.test(etag)||!/^\d+$/.test(size)||!media){controller.abort();throw new Error("Invalid saved-text stream metadata");}
    return {body:eraGuardedStream(r.body,()=>era!==this.#era),cancel:()=>controller.abort(),etag,bytes_sha256:etag.slice(8,-1),byte_size:size,media_type:media};
  }

  reviewGet(versionId: string): Promise<ReviewView> { return this.#request("GET", `/api/v1/versions/${encodeURIComponent(versionId)}/review`); }
  reviewPut(versionId: string, body: ReviewRequest, condition: ReviewCondition, operationId: string): Promise<ReviewView> {
    const headers: Record<string,string> = {"Idempotency-Key": operationId};
    if ("ifMatch" in condition) headers["If-Match"] = condition.ifMatch; else headers["If-None-Match"] = "*";
    const era=this.#era;
    return sourceWrite<ReviewView>(operationId,async()=>{
      const result=await this.#exchange("PUT", `/api/v1/versions/${encodeURIComponent(versionId)}/review`, body, headers);
      return {kind:result.status===202?"accepted":"committed",body:result.data};
    },id=>{if(era!==this.#era)throw new StaleResponseError();return this.operation(id);});
  }
  reviewHistory(versionId: string, cursor?: string): Promise<ListPage<ReviewEvent>> { return this.#request("GET", this.#collection(`/api/v1/versions/${encodeURIComponent(versionId)}/review/history`, {cursor})); }

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
    if (this.#passive) headers.set("Haeryong-Passive-Read", "1");
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

  jobResume(jobId:string,decision:"reconcile_only"|"continue"|"retry",expectedGeneration:number,etag:string,key:string):Promise<unknown>{return this.#noteWrite("POST",`/api/v1/jobs/${encodeURIComponent(jobId)}/resume`,{decision,expected_attempt_generation:expectedGeneration},key,{"If-Match":etag});}

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
    if (this.#passive) headers.set("Haeryong-Passive-Read", "1");
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

  providerStatus(): Promise<ProviderStatus> {
 return this.#request<ProviderStatus>("GET", "/api/v1/providers").then(verifiedProviderStatus);
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
    const listeners = new Set<() => unknown>();
    const resyncListeners = new Set<(resync: EventStreamResync) => unknown>();
    const decode = new TextDecoder();
    const controller = new AbortController();
    const era = this.#era;
    const lines = searchEventLines(
      async () => {
        if (era === this.#era && !controller.signal.aborted) {
          const active = [...listeners];
          await Promise.all(active.map((listener) => {
            try { return Promise.resolve(listener()); } catch (err) { return Promise.reject(err); }
          }));
        }
      },
      {
        onCursor: (cursor) => {
          if (era === this.#era && !controller.signal.aborted) {
            this.#state.eventCursor = cursor;
          }
        },
        onResync: async (resync) => {
          if (era === this.#era && !controller.signal.aborted) {
            if (resyncListeners.size > 0) {
              const active = [...resyncListeners];
              await Promise.all(active.map((listener) => {
                try { return Promise.resolve(listener(resync)); } catch (err) { return Promise.reject(err); }
              }));
            } else {
              const active = [...listeners];
              await Promise.all(active.map((listener) => {
                try { return Promise.resolve(listener()); } catch (err) { return Promise.reject(err); }
              }));
            }
          }
        },
      },
    );
    const headers = new Headers();
    if (this.#credential) headers.set("Authorization", "Bearer " + this.#credential);
    // No committed cursor means the existing no-cursor bootstrap contract.
    // A fabricated zero epoch incorrectly classifies first entry as an
    // epoch-change recovery and retires its fresh Provider readiness.
    if (this.#state.eventCursor) headers.set("Last-Event-ID", this.#state.eventCursor);
    let ended: () => void;
    const closed = new Promise<void>((resolve) => {
      ended = resolve;
    });
    void fetch("/api/v1/events", { headers, credentials: "omit", redirect: "error", signal: controller.signal })
      .then(async (response) => {
        if (!response.body) return;
        const reader = response.body.getReader();
        try {
          for (;;) {
            const part = await reader.read();
            if (era !== this.#era || controller.signal.aborted) break;
            if (part.done) break;
            // Only event names and bounded resync metadata become hints, never resource IDs or event bodies.
            await lines(decode.decode(part.value, { stream: true }));
            if (era !== this.#era || controller.signal.aborted) break;
          }
        } finally {
          controller.abort();
          await reader.cancel().catch(() => undefined);
        }
      })
      .catch(() => undefined)
      .finally(() => {
        controller.abort();
        ended();
      });
    return {
      closed,
      onInvalidation: (listener) => {
        listeners.add(listener);
        return () => {
          listeners.delete(listener);
        };
      },
      onResync: (listener) => {
        resyncListeners.add(listener);
        return () => {
          resyncListeners.delete(listener);
        };
      },
      close: () => {
        listeners.clear();
        resyncListeners.clear();
        controller.abort();
      },
    };
  }
}
