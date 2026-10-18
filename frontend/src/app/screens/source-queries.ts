// TASK-003-04 selection-scoped evidence queries: the actual enumeration
// controller behind the SourcePanel. Later-page failures RETAIN the already
// accumulated rows and the exact failed cursor; retry reissues from that
// cursor under the SAME captured target/scope/query identity (order-reversed
// replies never merge), coverage/verifications retries stay reachable, and a
// changed version/page/region or same-page history/render/reload context
// clears and rebinds everything. No derived denominator exists while an
// enumeration is incomplete.
import type { HaeryongClient, ApplicableFreshness, AssessmentRecord, CoverageUnit, EvidenceItemView, ListPage, VerificationRecord } from "../../client/types.ts";
import { ApiError } from "../../client/types.ts";
import { SelectionQueryGuard, coverageUnitsParams, enumerationComplete, initialEnumeration, mergeEnumerationPage, scopeMatchesSelection, type EnumerationState, type CoverageScopeFilter, type SelectionKey } from "./evidence-view.ts";

const errorText = (cause: unknown) => cause instanceof Error ? cause.message : String(cause);
const unavailableText = "서비스가 아직 이 기능을 제공하지 않습니다(503 FEATURE_NOT_READY).";
const isFeatureNotReady = (error: unknown): boolean => error instanceof ApiError && error.status === 503 && error.code === "FEATURE_NOT_READY";

export type Phase = "idle" | "loading" | "unavailable" | "error" | "ready";

/** One enumeration-driven view: partial rows survive failures; `resumeFrom` names the failed cursor. */
export interface EnumView<T> {
  phase: Phase;
  reason: string | null;
  items: T[];
  nextCursor: string | null;
  resumeFrom: string | null;
  complete: boolean;
}

export interface AssessmentsView {
  phase: Phase;
  reason: string | null;
  /** The separately selected applicable summary, retained across later-page failures; null when absent or incompatible with the selected scope. */
  applicable: ApplicableFreshness | null;
  /** True when a returned applicable view describes a different scope than the selection; it stays history-inspectable but never becomes the selected set. */
  incompatibleApplicable: boolean;
  history: EnumView<AssessmentRecord>;
}

export interface EvidenceItemView0 {
  phase: Phase;
  reason: string | null;
  view: EvidenceItemView | null;
}

export interface VerificationsView {
  phase: Phase;
  reason: string | null;
  records: EnumView<VerificationRecord>;
}

export interface CoverageView {
  phase: Phase;
  reason: string | null;
  units: EnumView<CoverageUnit>;
}

export interface SourceQuerySnapshot {
  assessments: AssessmentsView;
  coverage: CoverageView;
  items: Record<string, EvidenceItemView0>;
  verifications: Record<string, VerificationsView>;
}

function fromState<T>(phase: Phase, state: EnumerationState<T>, reason: string | null = null): EnumView<T> {
  return { phase, reason, items: state.items, nextCursor: state.nextCursor, resumeFrom: state.resumeFrom, complete: enumerationComplete(state) && phase === "ready" };
}

/**
 * Drives every evidence/assessment query for ONE exact selection. The panel
 * renders snapshots via subscribe/revision; all methods are idempotent and
 * safe to call from event handlers.
 */
export class SourceQueries {
  readonly #client: HaeryongClient;
  readonly #guard: SelectionQueryGuard;
  #contextKey: string;
  #listeners = new Set<() => void>();
  #revision = 0;
  #assessmentsState: EnumerationState<AssessmentRecord> = initialEnumeration();
  #assessmentsPhase: Phase = "idle";
  #assessmentsReason: string | null = null;
  #applicable: ApplicableFreshness | null = null;
  #foreignApplicable: ApplicableFreshness | null = null;
  #coverageState: EnumerationState<CoverageUnit> = initialEnumeration();
  #coveragePhase: Phase = "idle";
  #coverageReason: string | null = null;
  #items = new Map<string, { phase: Phase; reason: string | null; view: EvidenceItemView | null }>();
  #verifications = new Map<string, { phase: Phase; reason: string | null; state: EnumerationState<VerificationRecord> }>();
  #disposed = false;

  constructor(client: HaeryongClient, versionId: string, pagePath: string | null, regionId: string | null, contextKey: string) {
    this.#client = client;
    this.#guard = new SelectionQueryGuard(versionId, pagePath, regionId);
    this.#contextKey = contextKey;
  }

  get revision(): number { return this.#revision; }
  subscribe = (listener: () => void): (() => void) => {
    this.#listeners.add(listener);
    return () => { this.#listeners.delete(listener); };
  };

  #emit(): void {
    this.#revision++;
    for (const listener of this.#listeners) listener();
  }

  dispose(): void {
    // Ownership cleanup: besides marking disposed, invalidate every pending
    // guard token so an in-flight cursor page released after the keyed
    // unmount can never merge, emit or request its next cursor. A new
    // independent instance (its own guard) is unaffected.
    this.#disposed = true;
    this.#guard.bump();
  }

  /** Clear/rebind on version/page/region changes AND same-page history/render/reload context changes. */
  rebind(versionId: string, pagePath: string | null, regionId: string | null, contextKey: string): boolean {
    if (this.#disposed) return false;
    const selectionChanged = this.#guard.rebind(versionId, pagePath, regionId) !== null;
    const contextChanged = contextKey !== this.#contextKey;
    if (!selectionChanged && !contextChanged) return false;
    if (contextChanged && !selectionChanged) this.#guard.bump();
    this.#contextKey = contextKey;
    this.#assessmentsState = initialEnumeration();
    this.#assessmentsPhase = "idle";
    this.#assessmentsReason = null;
    this.#applicable = null;
    this.#foreignApplicable = null;
    this.#coverageState = initialEnumeration();
    this.#coveragePhase = "idle";
    this.#coverageReason = null;
    this.#items.clear();
    this.#verifications.clear();
    this.#emit();
    return true;
  }

  snapshot(): SourceQuerySnapshot {
    // Null-prototype dictionaries: arbitrary valid nonempty local IDs
    // (including "__proto__", "constructor" or "toString") store and read as
    // own entries only — a snapshot can never inherit another item's phase
    // or leak onto Object.prototype.
    const items: Record<string, EvidenceItemView0> = Object.create(null);
    for (const [id, value] of this.#items) items[id] = { phase: value.phase, reason: value.reason, view: value.view };
    const verifications: Record<string, VerificationsView> = Object.create(null);
    for (const [id, value] of this.#verifications) verifications[id] = { phase: value.phase, reason: value.reason, records: fromState(value.phase, value.state, value.reason) };
    return {
      assessments: { phase: this.#assessmentsPhase, reason: this.#assessmentsReason, applicable: this.#applicable, incompatibleApplicable: this.#foreignApplicable !== null, history: fromState(this.#assessmentsPhase, this.#assessmentsState, this.#assessmentsReason) },
      coverage: { phase: this.#coveragePhase, reason: this.#coverageReason, units: fromState(this.#coveragePhase, this.#coverageState, this.#coverageReason) },
      items,
      verifications,
    };
  }

  scope(): CoverageScopeFilter | undefined {
    const key = this.#guard.key;
    if (key.regionId !== null && key.pagePath !== null) return { kind: "region", pagePath: key.pagePath, regionId: key.regionId };
    if (key.pagePath !== null) return { kind: "page", pagePath: key.pagePath };
    return undefined;
  }

  #cursorToResume(view: EnumView<unknown>): string | undefined {
    if (view.resumeFrom !== null) return view.resumeFrom;
    if (view.nextCursor !== null) return view.nextCursor;
    return undefined;
  }

  /** Loads (or retries) assessment history from the last complete cursor. Idempotent while a request is live. */
  async loadAssessments(): Promise<void> {
    if (this.#disposed || this.#assessmentsPhase === "loading" || this.#assessmentsPhase === "ready") return;
    const resume = this.#cursorToResume(this.snapshot().assessments.history);
    this.#assessmentsPhase = "loading";
    this.#assessmentsReason = null;
    this.#emit();
    let cursor = resume;
    for (;;) {
      const token = this.#guard.begin("assessments");
      let page: ListPage<AssessmentRecord> & { applicable?: ApplicableFreshness | null };
      try {
        page = await this.#client.versionAssessments(this.#guard.key.versionId, { scope: this.scope(), cursor });
      } catch (error) {
        this.#failAssessments(error, token);
        return;
      }
      if (!this.#guard.accept("assessments", token)) return;
      if (this.#applicable === null && page.applicable != null) {
        // Boundary validation before a returned applicable becomes the
        // SELECTED set: its scope must describe this exact selection
        // (undefined selection = version scope). An incompatible view never
        // supplies the selected summary or its coverage chain; it stays
        // flagged and history rows remain inspectable.
        if (scopeMatchesSelection(page.applicable.scope, this.scope())) {
          this.#applicable = page.applicable;
        } else {
          this.#foreignApplicable = page.applicable;
        }
      }
      this.#assessmentsState = mergeEnumerationPage(this.#assessmentsState, page);
      if (page.next_cursor === null) break;
      cursor = page.next_cursor;
    }
    this.#assessmentsPhase = "ready";
    this.#assessmentsReason = null;
    this.#emit();
    void this.#maybeLoadCoverage();
  }

  retryAssessments(): void {
    if (this.#assessmentsPhase === "error" || this.#assessmentsPhase === "unavailable") {
      this.#assessmentsPhase = "idle";
      void this.loadAssessments();
    }
  }

  #failAssessments(error: unknown, token: { key: SelectionKey; seq: number }): void {
    if (!this.#guard.accept("assessments", token)) return;
    if (isFeatureNotReady(error)) {
      this.#assessmentsPhase = "unavailable";
      this.#assessmentsReason = unavailableText;
    } else {
      // The partial history and applicable summary stay; resumeFrom keeps the failed cursor.
      this.#assessmentsState = { ...this.#assessmentsState, failed: true, resumeFrom: this.#assessmentsState.nextCursor };
      this.#assessmentsPhase = "error";
      this.#assessmentsReason = errorText(error);
    }
    this.#emit();
  }

  /** Follows only a validated fixed local units route; retries from the failed cursor. */
  async #maybeLoadCoverage(): Promise<void> {
    const applicable = this.#applicable;
    if (applicable === null || applicable.coverage.coverage_set_id === null || this.#coveragePhase === "loading" || this.#coveragePhase === "ready") {
      if (applicable === null || applicable.coverage.coverage_set_id === null) {
        this.#coveragePhase = "idle";
        this.#coverageReason = null;
      }
      return;
    }
    const params = this.#coverageParams(applicable);
    if (params === null) {
      this.#coveragePhase = "error";
      this.#coverageReason = "units_url이 이 버전/커버리지 집합의 고정 로컬 경로와 일치하지 않아 검사할 수 없습니다.";
      this.#emit();
      return;
    }
    const resume = this.#cursorToResume(this.snapshot().coverage.units);
    this.#coveragePhase = "loading";
    this.#coverageReason = null;
    this.#emit();
    let cursor = resume;
    for (;;) {
      const token = this.#guard.begin("coverage");
      let page: ListPage<CoverageUnit>;
      try {
        page = await this.#client.assessmentCoverage(this.#guard.key.versionId, params.coverageSetId, cursor);
      } catch (error) {
        if (!this.#guard.accept("coverage", token)) return;
        if (isFeatureNotReady(error)) {
          this.#coveragePhase = "unavailable";
          this.#coverageReason = unavailableText;
        } else {
          this.#coverageState = { ...this.#coverageState, failed: true, resumeFrom: this.#coverageState.nextCursor };
          this.#coveragePhase = "error";
          this.#coverageReason = errorText(error);
        }
        this.#emit();
        return;
      }
      if (!this.#guard.accept("coverage", token)) return;
      this.#coverageState = mergeEnumerationPage(this.#coverageState, page);
      if (page.next_cursor === null) break;
      cursor = page.next_cursor;
    }
    this.#coveragePhase = "ready";
    this.#coverageReason = null;
    this.#emit();
  }

  retryCoverage(): void {
    if (this.#coveragePhase === "error" || this.#coveragePhase === "unavailable") {
      this.#coveragePhase = "idle";
      void this.#maybeLoadCoverage();
    }
  }

  #coverageParams(applicable: ApplicableFreshness): { coverageSetId: string } | null {
    const origin = this.#client.publicOrigin ?? (typeof location !== "undefined" ? location.origin : null);
    const unitsUrl = applicable.coverage.units_url;
    const versionId = this.#guard.key.versionId;
    // Only the fixed local route form is followed; foreign absolute origins
    // never reach a request.
    return coverageUnitsParams(typeof unitsUrl === "string" ? unitsUrl : null, versionId, applicable.coverage.coverage_set_id, origin);
  }

  async loadEvidence(evidenceId: string): Promise<void> {
    if (this.#disposed) return;
    const existing = this.#items.get(evidenceId);
    if (existing && (existing.phase === "loading" || existing.phase === "ready")) return;
    this.#items.set(evidenceId, { phase: "loading", reason: null, view: null });
    this.#emit();
    const token = this.#guard.begin(`evidence:${evidenceId}`);
    try {
      const view = await this.#client.versionEvidence(this.#guard.key.versionId, evidenceId);
      if (!this.#guard.accept(`evidence:${evidenceId}`, token)) return;
      this.#items.set(evidenceId, { phase: "ready", reason: null, view });
    } catch (error) {
      if (!this.#guard.accept(`evidence:${evidenceId}`, token)) return;
      this.#items.set(evidenceId, { phase: isFeatureNotReady(error) ? "unavailable" : "error", reason: isFeatureNotReady(error) ? unavailableText : errorText(error), view: null });
    }
    this.#emit();
  }

  retryEvidence(evidenceId: string): void {
    const existing = this.#items.get(evidenceId);
    if (existing && (existing.phase === "error" || existing.phase === "unavailable")) void this.loadEvidence(evidenceId);
  }

  async loadVerifications(evidenceId: string): Promise<void> {
    if (this.#disposed) return;
    const existing = this.#verifications.get(evidenceId);
    if (existing && (existing.phase === "loading" || existing.phase === "ready")) return;
    this.#verifications.set(evidenceId, { phase: "loading", reason: null, state: existing?.state ?? initialEnumeration<VerificationRecord>() });
    this.#emit();
    const resume = existing ? this.#cursorToResume(fromState(existing.phase, existing.state)) : undefined;
    let cursor = resume;
    for (;;) {
      const token = this.#guard.begin(`verifications:${evidenceId}`);
      let page: ListPage<VerificationRecord>;
      try {
        page = await this.#client.evidenceVerifications(this.#guard.key.versionId, evidenceId, cursor);
      } catch (error) {
        if (!this.#guard.accept(`verifications:${evidenceId}`, token)) return;
        const prior = this.#verifications.get(evidenceId);
        const priorState = prior?.state ?? initialEnumeration<VerificationRecord>();
        if (isFeatureNotReady(error)) {
          this.#verifications.set(evidenceId, { phase: "unavailable", reason: unavailableText, state: priorState });
        } else {
          this.#verifications.set(evidenceId, { phase: "error", reason: errorText(error), state: { ...priorState, failed: true, resumeFrom: priorState.nextCursor } });
        }
        this.#emit();
        return;
      }
      if (!this.#guard.accept(`verifications:${evidenceId}`, token)) return;
      const current = this.#verifications.get(evidenceId);
      const state = mergeEnumerationPage<VerificationRecord>(current?.state ?? initialEnumeration<VerificationRecord>(), page);
      this.#verifications.set(evidenceId, { phase: "loading", reason: null, state });
      if (page.next_cursor === null) break;
      cursor = page.next_cursor;
    }
    const final = this.#verifications.get(evidenceId);
    if (final) this.#verifications.set(evidenceId, { phase: "ready", reason: null, state: final.state });
    this.#emit();
  }

  retryVerifications(evidenceId: string): void {
    const existing = this.#verifications.get(evidenceId);
    if (existing && (existing.phase === "error" || existing.phase === "unavailable")) void this.loadVerifications(evidenceId);
  }
}
