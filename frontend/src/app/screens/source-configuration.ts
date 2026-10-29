import type { ShellController } from "../../client/controller.ts";
import type { ConfigurationPlanView, ConfigurationView, HaeryongClient, SourceMapping } from "../../client/types.ts";
import { resolveWriteIntent } from "../../client/source-write-intents.ts";
import type { ConfigurationIntent, RegistrationIntent, SourceWriteIntents } from "../../client/source-write-intents.ts";
import { ApiError, StaleResponseError } from "../../client/types.ts";
import { PendingSourceWriteError, reconcileSourceWrite } from "../../client/source-operations.ts";

export type SourceDefaults = { source_mode_default: "pinned" | "current"; provider_id: string | null };

type Authority = Pick<ShellController, "captureDispatchAuthority" | "requireVerifiedDispatch" | "sourceWriteIntents">;
export interface SourceConfigurationState {
  phase: "loading" | "editing" | "previewing" | "preview" | "committing" | "done" | "stale" | "error" | "pending";
  configuration: ConfigurationView | null;
  plan: ConfigurationPlanView | null;
  previewSources: SourceMapping[];
  previewDefaults: SourceDefaults;
  expandsScope: boolean;
  providerChange: { before: string | null; after: string | null } | null;
  reason: string | null;
  pending: { kind: "preview" | "commit"; operationId: string } | null;
}

export function expandsSourceScope(before: SourceMapping[], after: SourceMapping[]): boolean {
  return after.some((source) => {
    const old = before.find((entry) => entry.source_id === source.source_id);
    return !old || old.kind !== source.kind || old.root_path !== source.root_path || old.repo_id !== source.repo_id
      || (source.retention_mode === "selected_snapshot" && old.retention_mode !== source.retention_mode)
      || source.provider_ids.some((id) => !old.provider_ids.includes(id));
  });
}

/** One owner confirmation names the actual planned effects. Clearing a default
 * keeps the existing explicit Apply flow without granting new provider use. */
export function sourceConfirmation(state: Pick<SourceConfigurationState, "expandsScope" | "providerChange">): string | null {
  const changesProvider = state.providerChange !== null && state.providerChange.after !== null;
  if (state.expandsScope && changesProvider) return "출처/공개 범위 확대와 기본 Provider 변경 영향을 확인했습니다";
  if (state.expandsScope) return "출처/공개 범위 확대 영향을 확인했습니다";
  return changesProvider ? "기본 Provider 변경 영향을 확인했습니다" : null;
}

/** Revision-bound plan controller used by the normal project settings flow. */
export class SourceConfigurationFlow {
  #state: SourceConfigurationState = { phase: "loading", configuration: null, plan: null, previewSources: [], previewDefaults: { source_mode_default: "pinned", provider_id: null }, expandsScope: false, providerChange: null, reason: null, pending: null };
  #epoch = 0;
  #previewIntent: ConfigurationIntent | null = null;
  readonly #intents: SourceWriteIntents;
  #listeners = new Set<() => void>();
  readonly client: HaeryongClient;
  readonly projectId: string;
  readonly authority: Authority;
  constructor(client: HaeryongClient, projectId: string, authority: Authority) {
    this.client = client; this.projectId = projectId; this.authority = authority;
    this.#intents = authority.sourceWriteIntents();
  }
  snapshot = (): SourceConfigurationState => this.#state;
  subscribe = (listener: () => void): (() => void) => { this.#listeners.add(listener); return () => { this.#listeners.delete(listener); }; };
  #apply(patch: Partial<SourceConfigurationState>): void { this.#state = { ...this.#state, ...patch }; for (const listener of this.#listeners) listener(); }
  dispose(): void { this.#epoch++; this.#listeners.clear(); }
  async load(): Promise<void> {
    const epoch = ++this.#epoch;
    this.#apply({ phase: "loading", plan: null, pending: null, reason: null });
    try {
      const configuration = await this.client.configurationView();
      if (epoch === this.#epoch) {
        const pending = this.#intents.configurations.get(this.projectId) ?? null;
        this.#previewIntent = pending?.kind === "preview" ? pending : null;
        this.#apply({ phase: pending ? "pending" : "editing", pending, configuration, expandsScope: false, providerChange: null });
      }
    } catch (error) {
      if (epoch === this.#epoch) {
        this.#fail(error);
        // No configuration has been read, so there is no editable current
        // state or mutation receipt to reconcile. Retry the actual read.
        if (this.#state.configuration === null) this.#apply({ phase: "error" });
      }
    }
  }
  cancel(): void {
    const current = this.#intents.configurations.get(this.projectId);
    if (this.#state.phase === "committing" || this.#state.pending?.kind === "commit" || current?.kind === "commit") return;
    if (this.#previewIntent) this.#clearIntent(this.#previewIntent);
    this.#epoch++;
    this.#apply({ phase: "editing", plan: null, previewSources: [], previewDefaults: { source_mode_default: "pinned", provider_id: null }, expandsScope: false, providerChange: null, reason: null, pending: null });
  }
  #fail(error: unknown, pending?: { kind: "preview" | "commit"; operationId: string }): void {
    const uncertain = error instanceof PendingSourceWriteError || error instanceof StaleResponseError;
    this.#apply({ phase: uncertain ? "pending" : error instanceof ApiError && (error.code === "STALE_PLAN" || error.status === 412) ? "stale" : "error", reason: error instanceof Error ? error.message : String(error), pending: uncertain && pending ? pending : null });
  }
  #clearIntent(intent: ConfigurationIntent): void {
    if (this.#previewIntent === intent) this.#previewIntent = null;
    if (this.#intents.configurations.get(this.projectId) === intent) this.#intents.configurations.delete(this.projectId);
  }
  #showFailure(error: unknown, intent: ConfigurationIntent, epoch: number): void {
    if (epoch !== this.#epoch) return;
    this.#fail(error, intent);
    if (epoch === this.#epoch && intent.outcome.state === "failed") this.#clearIntent(intent);
  }
  #acceptPlan(plan: ConfigurationPlanView): void {
    const projects = plan.normalized_changes.projects as Record<string, { sources: SourceMapping[]; source_mode_default?: "pinned" | "current"; provider_id?: string | null }>;
    const previewSources = projects[this.projectId]!.sources;
    const before = this.#state.configuration!.configuration.projects[this.projectId]?.sources ?? [];
    const prior = this.#state.configuration!.configuration.projects[this.projectId];
    const normalized = projects[this.projectId]!;
    const previewDefaults: SourceDefaults = { source_mode_default: normalized.source_mode_default ?? prior?.source_mode_default ?? "pinned", provider_id: "provider_id" in normalized ? normalized.provider_id ?? null : prior?.provider_id ?? null };
    const previousProvider = prior?.provider_id ?? null;
    const providerChange = previewDefaults.provider_id === previousProvider ? null : { before: previousProvider, after: previewDefaults.provider_id };
    this.#apply({ phase: "preview", plan, previewSources, previewDefaults, expandsScope: expandsSourceScope(before, previewSources), providerChange, pending: null });
  }
  async preview(sources: SourceMapping[], defaults?: SourceDefaults): Promise<void> {
    const configuration = this.#state.configuration;
    if (!configuration || this.#intents.configurations.has(this.projectId) || ["previewing", "committing", "pending"].includes(this.#state.phase)) return;
    const authority = this.authority.captureDispatchAuthority();
    this.authority.requireVerifiedDispatch(authority);
    const epoch = ++this.#epoch, operationId = crypto.randomUUID();
    const intent: ConfigurationIntent = { kind: "preview", operationId, outcome: { state: "pending" } };
    this.#previewIntent = intent;
    this.#intents.configurations.set(this.projectId, intent);
    this.#apply({ phase: "previewing", plan: null, reason: null, previewSources: [], previewDefaults: { source_mode_default: "pinned", provider_id: null }, expandsScope: false });
    try {
      const plan = await resolveWriteIntent(intent, () => {
        this.authority.requireVerifiedDispatch(authority);
        return this.client.configurationPlan({ scope: { kind: "project", project_id: this.projectId }, expected_config_revision: configuration.config_revision, changes: { sources: structuredClone(sources), ...(defaults ?? {}) } }, operationId);
      });
      if (epoch === this.#epoch) {
        this.#acceptPlan(plan as ConfigurationPlanView);
        if (epoch === this.#epoch) this.#clearIntent(intent);
      }
    } catch (error) { this.#showFailure(error, intent, epoch); }
  }
  async commit(confirmedImpact: boolean): Promise<void> {
    const plan = this.#state.plan;
    if (!plan || this.#state.phase !== "preview") return;
    if (sourceConfirmation(this.#state) && !confirmedImpact) { this.#apply({ reason: "설정 변경 영향을 확인해 주세요." }); return; }
    const authority = this.authority.captureDispatchAuthority();
    this.authority.requireVerifiedDispatch(authority);
    const epoch = ++this.#epoch, operationId = plan.plan_id;
    const intent: ConfigurationIntent = { kind: "commit", operationId, outcome: { state: "pending" } };
    this.#intents.configurations.set(this.projectId, intent);
    this.#apply({ phase: "committing", reason: null });
    try {
      await resolveWriteIntent(intent, () => {
        this.authority.requireVerifiedDispatch(authority);
        return this.client.configurationPlanCommit(plan.plan_id, { expected_config_revision: plan.base_config_revision, confirmed: true }, operationId);
      });
      if (epoch === this.#epoch) {
        this.#apply({ phase: "done", pending: null });
        if (epoch === this.#epoch) this.#clearIntent(intent);
      }
    } catch (error) { this.#showFailure(error, intent, epoch); }
  }
  async recover(): Promise<void> {
    const pending = this.#intents.configurations.get(this.projectId);
    if (!pending || this.#state.phase !== "pending") return;
    const epoch = ++this.#epoch;
    this.#apply({ phase: pending.kind === "preview" ? "previewing" : "committing", reason: null });
    try {
      const result = await resolveWriteIntent(pending, () => reconcileSourceWrite<ConfigurationPlanView>(pending.operationId, (id) => this.client.operation(id)));
      if (epoch === this.#epoch) {
        if (pending.kind === "preview") this.#acceptPlan(result as ConfigurationPlanView);
        else this.#apply({ phase: "done", pending: null });
        if (epoch === this.#epoch) this.#clearIntent(pending);
      }
    } catch (error) { this.#showFailure(error, pending, epoch); }
  }
}

/** Preserve the editable text until the preview boundary, including a trailing comma. */
export function sourcesForPreview(sources: SourceMapping[], providerText: Record<string, string>): SourceMapping[] {
  return sources.map((source) => ({ ...source, provider_ids: (providerText[source.source_id] ?? source.provider_ids.join(", ")).split(",").map((id) => id.trim()).filter(Boolean) }));
}

/** A registration intent outlives a transport acknowledgement invalidation. */
export class ProjectRegistrationFlow {
  #epoch = 0;
  dispose(): void { this.#epoch++; }
  readonly #intents: SourceWriteIntents;
  get pending(): RegistrationIntent | null { return this.#intents.registration; }
  readonly client: HaeryongClient;
  readonly authority: Authority;
  constructor(client: HaeryongClient, authority: Authority) { this.client = client; this.authority = authority; this.#intents = authority.sourceWriteIntents(); }
  async run(name: string): Promise<void> {
    const authority = this.authority.captureDispatchAuthority();
    this.authority.requireVerifiedDispatch(authority);
    const epoch = this.#epoch;
    const prior = this.pending;
    const intent: RegistrationIntent = prior ?? { operationId: crypto.randomUUID(), name, outcome: { state: "pending" } };
    this.#intents.registration = intent;
    try {
      await resolveWriteIntent(intent, () => prior ? reconcileSourceWrite(intent.operationId, (id) => this.client.operation(id)) : this.client.projectRegister(intent.name, intent.operationId));
      if (epoch === this.#epoch && this.#intents.registration === intent) this.#intents.registration = null;
    } catch (error) {
      if (epoch === this.#epoch && intent.outcome.state === "failed" && this.#intents.registration === intent) this.#intents.registration = null;
      throw error;
    }
  }
}
