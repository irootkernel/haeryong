import type { DispatchAuthority, ShellController } from "../../client/controller.ts";
import { ApiError, DispatchBlockedError, StaleResponseError } from "../../client/types.ts";
import type { ApplicableFreshness, AssessmentRecord, AssessmentsView, ConfigurationView, CoverageUnit, FindingStatus, FindingView, HaeryongClient, JobFindingsFilter, JobView, ListPage, ProjectView, ScanAccepted, ScanRequest } from "../../client/types.ts";
import { coverageUnitsParams } from "./evidence-view.ts";
import { pinnedReaderURL, UUID } from "../routes.ts";

export type MaintenanceController = Pick<ShellController, "client" | "passiveClient" | "state" | "readGeneration" | "captureDispatchAuthority" | "requireVerifiedDispatch" | "isCurrentReadAuthority" | "isCurrentDispatchAuthority" | "subscribe">;
export const FINDING_GROUPS = [
  { id: "freshness", label: "최신성", kinds: ["freshness_assessment"] },
  { id: "relationships", label: "관계", kinds: ["related_explanation"] },
  { id: "topics", label: "주제", kinds: ["topic_assignment"] },
  { id: "content", label: "콘텐츠 제안 · 미게시", kinds: ["new_version_proposal", "new_document_proposal"] },
  { id: "structure", label: "구조 검사", kinds: ["backlink_index_repair"] },
] as const;
export const FINDING_STATUSES: FindingStatus[] = ["proposed", "applied", "rejected", "stale", "blocked", "undone"];
export const statusLabel = (status: string): string => ({ proposed: "제안", applied: "적용됨", rejected: "거절됨", stale: "적용 조건 변경", blocked: "차단됨", undone: "실행 취소됨" } as Record<string, string>)[status] ?? status;
export const object = (value: unknown): Record<string, unknown> | null => value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
const problem = (error: unknown) => error instanceof Error ? error.message : String(error);
const accessError = (error: unknown) => error instanceof ApiError && [401, 403, 404].includes(error.status);

export interface FindingScope { kind: "version" | "page" | "region"; page_path: string | null; region_id: string | null }
export function findingScope(value: unknown): FindingScope | null {
  const scope = object(value);
  if (!scope) return null;
  if (scope.kind === "version" && scope.page_path === null && scope.region_id === null) return { kind: "version", page_path: null, region_id: null };
  if (typeof scope.page_path !== "string" || !scope.page_path) return null;
  if (scope.kind === "page" && scope.region_id === null) return { kind: "page", page_path: scope.page_path, region_id: null };
  if (scope.kind === "region" && typeof scope.region_id === "string" && scope.region_id) return { kind: "region", page_path: scope.page_path, region_id: scope.region_id };
  return null;
}
export function freshnessProposal(finding: FindingView): AssessmentRecord | null {
  if (finding.kind !== "freshness_assessment") return null;
  const p = object(finding.proposal), scope = findingScope(p?.scope);
  if (!p || !scope || typeof p.assessment_id !== "string" || typeof p.version_id !== "string" || !p.version_id || typeof p.target_set_id !== "string" || !p.target_set_id || typeof p.state !== "string" || typeof p.assessed_at !== "string") return null;
  if (finding.version_id && finding.version_id !== p.version_id || finding.target_set_id && finding.target_set_id !== p.target_set_id) return null;
  return { ...p, assessment_id: p.assessment_id, version_id: p.version_id, target_set_id: p.target_set_id, scope, state: p.state, assessed_at: p.assessed_at };
}
export function scopeLabel(scope: FindingScope | null): string {
  if (!scope) return "범위 기록 없음/해석할 수 없음";
  return scope.kind === "version" ? "버전 전체" : scope.kind === "page" ? `페이지 ${scope.page_path}` : `영역 ${scope.page_path} · ${scope.region_id}`;
}
export function exactReaderLink(documentId: unknown, versionId: unknown, page: unknown, region: unknown): string | null {
  const uuid = new RegExp(`^${UUID}$`);
  if (typeof documentId !== "string" || !uuid.test(documentId) || typeof versionId !== "string" || !uuid.test(versionId)) return null;
  if (page !== null && (typeof page !== "string" || !page || page.startsWith("/") || /^[a-zA-Z]:/.test(page) || page.includes("\0") || page.split("/").some(part => !part || part === "." || part === ".."))) return null;
  if (region !== null && (typeof region !== "string" || !region || page === null)) return null;
  return pinnedReaderURL(documentId, versionId, page, "", "", region);
}
/** This is scan-level context, never a fabricated join to an assessment capture. */
export function scanContext(job: JobView | null, finding?: FindingView) {
  if (!job || job.kind !== "scan" || finding && job.job_id !== finding.job_id) return null;
  const report = object(job.result);
  if (!report || report.job_id !== undefined && report.job_id !== job.job_id) return null;
  return { report, requested: object(report.requested_scope), targets: Array.isArray(report.source_targets) ? report.source_targets : null };
}

interface ReadTicket { era: number; authority: DispatchAuthority | null; generation: number }
class ReadFence {
  era = 0;
  disposed = false;
  readonly controller: MaintenanceController;
  readonly denied: () => void;
  constructor(controller: MaintenanceController, denied: () => void) { this.controller = controller; this.denied = denied; }
  capture(): ReadTicket {
    if (this.disposed) throw new StaleResponseError();
    const authority = this.controller.captureDispatchAuthority();
    this.controller.requireVerifiedDispatch(authority);
    return { era: this.era, authority, generation: this.controller.readGeneration };
  }
  current(ticket: ReadTicket): boolean {
    return !this.disposed && ticket.era === this.era && this.controller.isCurrentReadAuthority(ticket.authority, ticket.generation);
  }
  retire() { this.era++; }
}

export interface PageState<T> { items: T[]; nextCursor: string | null; complete: boolean; loading: boolean; error: string | null; loadedPages: number }
const emptyPage = <T>(): PageState<T> => ({ items: [], nextCursor: null, complete: false, loading: false, error: null, loadedPages: 0 });
/** One query/filter lifetime. No automatic retry; failed cursor stays unchanged. */
class Pages<T> {
  state = emptyPage<T>();
  #request = 0;
  #seen = new Set<string>();
  #failedRefresh = false;
  readonly fence: ReadFence;
  readonly changed: () => void;
  readonly read: (client: HaeryongClient, cursor: string | null) => Promise<ListPage<T>>;
  readonly identity: (item: T) => string;
  readonly commit?: (pages: ListPage<T>[]) => void;
  constructor(fence: ReadFence, changed: () => void, read: (client: HaeryongClient, cursor: string | null) => Promise<ListPage<T>>, identity: (item: T) => string, commit?: (pages: ListPage<T>[]) => void) {
    this.fence = fence; this.changed = changed; this.read = read; this.identity = identity; this.commit = commit;
  }
  clear() { this.#request++; this.#seen.clear(); this.#failedRefresh = false; this.state = emptyPage<T>(); }
  async more(passive = false, rethrow = false) {
    if (this.state.loading) return;
    if (this.state.error && this.#failedRefresh) return this.#load(true, passive, rethrow);
    if (this.state.complete) return;
    return this.#load(false, passive, rethrow);
  }
  async refresh(passive = false, rethrow = false) { return this.#load(true, passive, rethrow); }
  async #load(refresh: boolean, passive: boolean, rethrow: boolean) {
    let ticket: ReadTicket;
    try { ticket = this.fence.capture(); } catch (error) { if (rethrow) throw error; return; }
    const run = ++this.#request, before = this.state;
    const client = passive ? this.fence.controller.passiveClient : this.fence.controller.client;
    const current = () => run === this.#request && this.fence.current(ticket);
    this.state = { ...before, loading: true, error: null }; this.changed();
    try {
      let cursor = refresh ? null : before.nextCursor;
      const items = refresh ? [] : [...before.items];
      const seen = refresh ? new Set<string>() : new Set(this.#seen);
      let loadedPages = refresh ? 0 : before.loadedPages;
      const pages = refresh ? Math.max(1, before.loadedPages) : 1;
      const received: ListPage<T>[] = [];
      for (let index = 0; index < pages; index++) {
        if (!current()) throw new StaleResponseError();
        this.fence.controller.requireVerifiedDispatch(ticket.authority);
        const page = await this.read(client, cursor);
        if (!current()) throw new StaleResponseError();
        if (page.next_cursor !== null && (typeof page.next_cursor !== "string" || seen.has(page.next_cursor))) throw new Error("반복되거나 잘못된 커서입니다. 앞선 결과를 유지합니다.");
        if (page.next_cursor !== null) seen.add(page.next_cursor);
        for (const item of page.items) {
          const at = items.findIndex(old => this.identity(old) === this.identity(item));
          if (at >= 0) throw new Error("응답에 중복된 기록이 있습니다. 기존 행과 커버리지 분모를 유지합니다.");
          items.push(item);
        }
        received.push(page);
        cursor = page.next_cursor; loadedPages++;
        if (cursor === null) break;
      }
      this.commit?.(received);
      this.#seen = seen;
      this.#failedRefresh = false;
      this.state = { items, nextCursor: cursor, complete: cursor === null, loading: false, error: null, loadedPages };
      this.changed();
    } catch (error) {
      if (current()) {
        if (accessError(error)) this.fence.denied();
        else { this.#failedRefresh = refresh; this.state = { ...before, loading: false, error: problem(error) }; this.changed(); }
      } else if (run === this.#request) {
        this.state = { ...before, loading: false }; this.changed();
      }
      if (rethrow) throw error;
    }
  }
}

export class MaintenanceState {
  readonly jobs: Pages<JobView>;
  readonly findings: Pages<FindingView>;
  readonly projects: Pages<ProjectView>;
  job: JobView | null = null;
  jobLoading = false;
  jobError: string | null = null;
  configuration: ConfigurationView | null = null;
  optionsError: string | null = null;
  optionsLoading = false;
  scanBusy = false;
  scanError: string | null = null;
  // Recovery may separately hydrate the current job after reading its receipt.
  accepted: (Pick<ScanAccepted, "operation_id" | "job_id"> & Partial<Omit<ScanAccepted, "operation_id" | "job_id">> & { job?: JobView }) | null = null;
  unconfirmedOperation: string | null = null;
  filter: Pick<JobFindingsFilter, "kind" | "status"> = {};
  accessLost = false;
  #fence: ReadFence;
  #listeners = new Set<() => void>();
  #off: (() => void) | null = null;
  #jobRequest = 0;
  #optionsRequest = 0;
  #authority: DispatchAuthority | null;
  readonly controller: MaintenanceController;
  readonly jobId?: string;
  constructor(controller: MaintenanceController, jobId?: string) {
    this.controller = controller; this.jobId = jobId;
    this.#fence = new ReadFence(controller, () => this.clearProtected());
    this.#authority = controller.captureDispatchAuthority();
    this.jobs = new Pages(this.#fence, this.#emit, async (client, cursor) => {
      if (!client.jobsList || cursor !== null) throw new Error("이 서비스는 작업 목록의 다음 페이지 조회를 제공하지 않습니다.");
      const page = await client.jobsList();
      return { ...page, items: page.items.filter(job => job.kind === "scan") };
    }, job => job.job_id);
    this.findings = new Pages(this.#fence, this.#emit, (client, cursor) => {
      if (!jobId || !client.jobFindingsList) throw new Error("발견 항목 조회를 제공하지 않습니다.");
      return client.jobFindingsList(jobId, { ...this.filter, limit: 30, cursor: cursor ?? undefined }).then(page => {
        if (page.items.some(row => row.job_id !== jobId)) throw new Error("다른 작업의 발견 항목 응답을 적용하지 않았습니다.");
        return page;
      });
    }, finding => finding.finding_id);
    this.projects = new Pages(this.#fence, this.#emit, (client, cursor) => client.listProjects(cursor ?? undefined), p => p.project_id);
  }
  connect() {
    if (this.#off) return;
    this.#fence.disposed = false;
    this.#authority = this.controller.captureDispatchAuthority();
    this.#off = this.controller.subscribe(() => {
      const controller = this.controller;
      if (!controller.isCurrentDispatchAuthority(this.#authority)) {
        this.clearProtected();
        this.#authority = controller.captureDispatchAuthority();
        if (this.#authority) void this.refresh(true);
      }
    });
  }
  subscribe(listener: () => void) { this.#listeners.add(listener); return () => { this.#listeners.delete(listener); }; }
  #emit = () => { for (const listener of this.#listeners) listener(); };
  snapshot() {
    return { jobs: this.jobs.state, findings: this.findings.state, projects: this.projects.state,
      job: this.job, jobLoading: this.jobLoading, jobError: this.jobError, configuration: this.configuration,
      optionsError: this.optionsError, optionsLoading: this.optionsLoading, scanBusy: this.scanBusy,
      scanError: this.scanError, accepted: this.accepted, unconfirmedOperation: this.unconfirmedOperation,
      filter: this.filter, accessLost: this.accessLost };
  }
  dispose() { this.#off?.(); this.#off = null; this.#fence.disposed = true; this.#fence.retire(); this.#listeners.clear(); }
  clearProtected() {
    this.#fence.retire(); this.#jobRequest++; this.#optionsRequest++;
    this.jobs.clear(); this.findings.clear(); this.projects.clear(); this.job = null;
    this.configuration = null; this.accepted = null; this.jobLoading = false; this.optionsLoading = false;
    this.jobError = null; this.optionsError = null; this.scanError = null; this.scanBusy = false;
    this.unconfirmedOperation = null; this.accessLost = true; this.#emit();
  }
  setFilter(filter: Pick<JobFindingsFilter, "kind" | "status">) {
    this.filter = { ...filter }; this.findings.clear(); this.#emit();
    return this.findings.more();
  }
  async refresh(passive = false, rethrow = false) {
    if (this.controller.state.capabilities?.features.scans !== true) return;
    this.accessLost = false;
    if (!this.jobId) return this.jobs.refresh(passive, rethrow);
    await Promise.all([this.#loadJob(passive, rethrow), this.findings.refresh(passive, rethrow)]);
  }
  async #loadJob(passive: boolean, rethrow: boolean) {
    let ticket: ReadTicket;
    try { ticket = this.#fence.capture(); } catch (error) { if (rethrow) throw error; return; }
    const run = ++this.#jobRequest;
    this.jobLoading = true; this.jobError = null; this.#emit();
    try {
      const job = await (passive ? this.controller.passiveClient : this.controller.client).jobGet(this.jobId!);
      if (run !== this.#jobRequest || !this.#fence.current(ticket)) throw new StaleResponseError();
      if (job.job_id !== this.jobId || job.kind !== "scan") throw new Error("요청한 스캔 작업과 응답이 일치하지 않습니다.");
      this.job = job;
    } catch (error) {
      if (run === this.#jobRequest && this.#fence.current(ticket)) {
        if (accessError(error)) this.clearProtected(); else this.jobError = problem(error);
      }
      if (rethrow) throw error;
    } finally { if (run === this.#jobRequest) { this.jobLoading = false; this.#emit(); } }
  }
  async loadScanOptions() {
    let ticket: ReadTicket;
    try { ticket = this.#fence.capture(); } catch { return; }
    const run = ++this.#optionsRequest;
    this.optionsLoading = true; this.optionsError = null; this.#emit();
    try {
      const [configuration] = await Promise.all([this.controller.client.configurationView(), this.projects.refresh(false, true)]);
      if (run !== this.#optionsRequest || !this.#fence.current(ticket)) return;
      this.configuration = configuration;
    } catch (error) {
      if (run === this.#optionsRequest && this.#fence.current(ticket)) {
        if (accessError(error)) this.clearProtected(); else this.optionsError = problem(error);
      }
    } finally { if (run === this.#optionsRequest) { this.optionsLoading = false; this.#emit(); } }
  }
  async startScan(request: ScanRequest, operationId: string) {
    if (this.scanBusy || this.unconfirmedOperation) return;
    const ticket = this.#fence.capture();
    if (this.controller.state.capabilities?.features.scans !== true || !this.controller.client.scanStart) throw new DispatchBlockedError();
    this.scanBusy = true; this.scanError = null; this.unconfirmedOperation = operationId; this.#emit();
    try {
      const accepted = await this.controller.client.scanStart(request, operationId);
      if (!this.#fence.current(ticket)) return;
      this.accepted = accepted; this.unconfirmedOperation = null;
    } catch (error) {
      if (!this.#fence.current(ticket)) return;
      if (accessError(error)) this.clearProtected();
      else {
        const refused = error instanceof ApiError && error.status >= 400 && error.status < 500 && error.code !== "OPERATION_RECONCILIATION_REQUIRED";
        if (refused) this.unconfirmedOperation = null;
        this.scanError = `${problem(error)} · ${refused ? "요청이 거부되었습니다." : "요청 결과를 확인하지 못했습니다."} 자동 재전송하지 않습니다.`;
      }
    } finally { if (this.#fence.current(ticket)) { this.scanBusy = false; this.#emit(); } }
  }
  async recoverScan() {
    if (!this.unconfirmedOperation || this.scanBusy) return;
    const ticket = this.#fence.capture(), key = this.unconfirmedOperation;
    this.scanBusy = true; this.scanError = null; this.#emit();
    try {
      const receipt = await this.controller.client.operation(key);
      if (!this.#fence.current(ticket)) return;
      if (receipt.operation_id !== key) throw new Error("다른 요청의 영수증입니다.");
      if (receipt.state === "failed") { this.unconfirmedOperation = null; this.scanError = `요청 실패 확인: ${receipt.error?.code ?? "사유 기록 없음"}`; }
      else if (receipt.state === "completed" && receipt.job_id) {
        const job = await this.controller.client.jobGet(receipt.job_id);
        if (!this.#fence.current(ticket)) return;
        if (job.job_id !== receipt.job_id || job.kind !== "scan") throw new Error("영수증의 스캔 작업을 확인할 수 없습니다.");
        this.accepted = { operation_id: key, job_id: job.job_id, job };
        this.unconfirmedOperation = null;
      } else this.scanError = "요청 결과가 아직 확정되지 않았습니다. 같은 영수증을 다시 확인할 수 있습니다.";
    } catch (error) {
      if (this.#fence.current(ticket)) { if (accessError(error)) this.clearProtected(); else this.scanError = problem(error); }
    } finally { if (this.#fence.current(ticket)) { this.scanBusy = false; this.#emit(); } }
  }
}

/** Published query state is never populated from a proposed Assessment. */
export class MaintenanceFreshnessState {
  readonly history: Pages<AssessmentRecord>;
  readonly coverage: Pages<CoverageUnit>;
  applicable: ApplicableFreshness | null = null;
  accessLost = false;
  #fence: ReadFence;
  #listeners = new Set<() => void>();
  #off: (() => void) | null = null;
  #authority: DispatchAuthority | null;
  readonly controller: MaintenanceController;
  readonly versionId: string;
  readonly scope: FindingScope;
  readonly targetSetId: string;
  constructor(controller: MaintenanceController, versionId: string, scope: FindingScope, targetSetId: string) {
    this.controller = controller; this.versionId = versionId; this.scope = scope; this.targetSetId = targetSetId;
    this.#fence = new ReadFence(controller, () => this.clear());
    this.#authority = controller.captureDispatchAuthority();
    this.history = new Pages(this.#fence, this.#emit, async (client, cursor) => {
      const page = await client.versionAssessments(versionId, { targetSetId, cursor: cursor ?? undefined,
        scope: { kind: scope.kind, pagePath: scope.page_path ?? undefined, regionId: scope.region_id ?? undefined } });
      if (page.items.some(item => item.version_id !== versionId || item.target_set_id !== targetSetId ||
        scope.kind !== "version" && item.scope.page_path !== scope.page_path ||
        scope.kind === "region" && item.scope.region_id !== scope.region_id)) throw new Error("다른 버전/대상/페이지의 평가 이력을 적용하지 않았습니다.");
      const applicable = page.applicable;
      if (applicable && (applicable.target_set_id !== targetSetId || JSON.stringify(findingScope(applicable.scope)) !== JSON.stringify(scope))) throw new Error("선택한 대상/범위와 현재 적용 평가가 일치하지 않습니다.");
      return page;
    }, assessment => assessment.assessment_id, pages => {
      const applicable = (pages[0] as AssessmentsView).applicable;
      if (JSON.stringify(this.applicable?.coverage) !== JSON.stringify(applicable?.coverage)) this.coverage.clear();
      this.applicable = applicable;
    });
    this.coverage = new Pages(this.#fence, this.#emit, async (client, cursor) => {
      const applicable = this.applicable;
      const params = applicable && coverageUnitsParams(applicable.coverage.units_url, versionId, applicable.coverage.coverage_set_id, client.publicOrigin ?? null);
      if (!params) throw new Error("선택한 버전/대상의 고정 커버리지 주소가 없습니다. 단위를 만들어 표시하지 않습니다.");
      return client.assessmentCoverage(versionId, params.coverageSetId, cursor ?? undefined, params.attemptRunId);
    }, unit => JSON.stringify([unit.kind, unit.page_path, unit.region_id]));
  }
  connect() {
    if (this.#off) return;
    this.#fence.disposed = false;
    this.#authority = this.controller.captureDispatchAuthority();
    this.#off = this.controller.subscribe(() => { if (!this.controller.isCurrentDispatchAuthority(this.#authority)) { this.clear(); this.#authority = this.controller.captureDispatchAuthority(); } });
  }
  subscribe(listener: () => void) { this.#listeners.add(listener); return () => { this.#listeners.delete(listener); }; }
  #emit = () => { for (const listener of this.#listeners) listener(); };
  clear() { this.#fence.retire(); this.history.clear(); this.coverage.clear(); this.applicable = null; this.accessLost = true; this.#emit(); }
  dispose() { this.#off?.(); this.#off = null; this.#fence.disposed = true; this.#fence.retire(); this.#listeners.clear(); }
  snapshot() { return { applicable: this.applicable, history: this.history.state, coverage: this.coverage.state, accessLost: this.accessLost }; }
  async refresh(passive = false, rethrow = false) {
    const hadCoverage = this.coverage.state.loadedPages > 0;
    this.accessLost = false;
    await this.history.refresh(passive, rethrow);
    if (!this.accessLost && hadCoverage && this.applicable?.coverage.units_url) await this.coverage.refresh(passive, rethrow);
  }
}
