import { ContextTransferHost, contextFailure } from "./context-transfer.ts";

export interface PageBinding { document_id: string; version_id: string; page_path: string; render_instance_id: string }
export type DisplayContext = Record<string, unknown>;
export interface ChannelOptions {
  frame: Window;
  origin: string;
  page: Omit<PageBinding, "render_instance_id">;
  guard(): void;
  contexts(binding: PageBinding, guard: () => void): ContextTransferHost;
  ready(payload: DisplayContext): void;
  display(type: "state.changed" | "region.selected" | "region.focused", payload: DisplayContext): void;
  error(code: string, message: string): void;
  position?(correlationId: string, snapshot: { scroll_x?: unknown; scroll_y?: unknown }): void;
  positionApplied?(correlationId: string, result: Record<string, unknown>): void;
  location?(report: Record<string, unknown>): void;
}
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === "object" && Object.getPrototypeOf(value) === Object.prototype;
const uuid = (value: unknown): value is string => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value);

// No field-count, string-size or nesting quotas. Transfer byte buffers are
// permitted only in their declared message, never in ordinary display JSON.
function plainJSON(value: unknown): boolean {
  const stack: { value: unknown; leave?: boolean }[] = [{ value }];
  const ancestors = new Set<object>();
  while (stack.length) {
    const item = stack.pop()!, current = item.value;
    if (item.leave) { ancestors.delete(current as object); continue; }
    if (current === null || typeof current === "string" || typeof current === "boolean" || (typeof current === "number" && Number.isFinite(current))) continue;
    if (!Array.isArray(current) && !object(current)) return false;
    if (ancestors.has(current)) return false;
    ancestors.add(current); stack.push({ value: current, leave: true });
    for (const child of Object.values(current)) stack.push({ value: child });
  }
  return true;
}

// Source and exact renderer origin are checked on the initial window message.
// Thereafter the private MessagePort and full binding identify one active page.
// The class has no generic RPC, management mutation or native binding surface.
export class ViewerChannel {
  readonly #options: ChannelOptions;
  #port: MessagePort | null = null;
  #binding: PageBinding | null = null;
  #contexts: ContextTransferHost | null = null;
  #generation = 0;
  #sequence = 0;
  #received = 0;
  #displaySequence = 0;
  #ready = false;
  #announcement: string | null = null;
  #reference: ((id: string) => void) | undefined;

  constructor(options: ChannelOptions) { this.#options = options; }

  accept(event: Pick<MessageEvent, "source" | "origin" | "data">): boolean {
    const data: unknown = event.data;
    if (event.source !== this.#options.frame || event.origin !== this.#options.origin || !object(data) || data.type !== "viewer.loaded" || !uuid(data.announcement_id) || !uuid(data.render_instance_id) || !Object.entries(this.#options.page).every(([key, value]) => data[key] === value)) return false;
    this.#options.guard();
    if (data.announcement_id === this.#announcement) return true;
    this.dispose();
    this.#announcement = data.announcement_id;
    this.#binding = { ...this.#options.page, render_instance_id: data.render_instance_id };
    const generation = this.#generation;
    const guard = () => { this.#options.guard(); if (generation !== this.#generation || !this.#port) throw new Error("Inactive page channel"); };
    const channel = new MessageChannel(); this.#port = channel.port1;
    this.#contexts = this.#options.contexts(this.#binding, guard);
    channel.port1.onmessage = ({ data: message }) => { void this.#receive(message, generation).catch(() => undefined); };
    channel.port1.onmessageerror = () => { if (generation === this.#generation) this.#options.error("PAGE_CHANNEL_ERROR", "페이지 연결을 다시 열어 주세요."); };
    channel.port1.start();
    this.#options.frame.postMessage({ type: "host.connect", announcement_id: data.announcement_id, binding: this.#binding }, this.#options.origin, [channel.port2]);
    return true;
  }

  async #receive(data: unknown, generation: number): Promise<void> {
    if (generation !== this.#generation || !this.#binding || !this.#port) return;
    this.#options.guard();
    if (!object(data) || data.protocol_version !== "1.0" || !Object.entries(this.#binding).every(([key, value]) => data[key] === value) || !Number.isSafeInteger(data.sequence) || (data.sequence as number) <= this.#received || typeof data.type !== "string" || !object(data.payload)) return;
    const type = data.type, payload = data.payload;
    if (type !== "context.transfer.chunk" && !plainJSON(payload)) return;
    if (type === "viewer.ready") {
      if (payload.profile !== "html_js_v1" || this.#ready) return;
      this.#received = data.sequence as number; this.#ready = true; this.#options.ready(payload); return;
    }
    if (!this.#ready) return;
    if (["context.transfer.begin", "context.transfer.chunk", "context.transfer.end", "context.transfer.abort"].includes(type)) {
      if (typeof payload.transfer_id !== "string" || !payload.transfer_id) return;
      this.#received = data.sequence as number;
      try {
        const reply = await this.#contexts!.handle(type, payload);
        if (generation !== this.#generation) return;
        this.#options.guard(); this.#send(reply.type, reply.payload);
      } catch (error) {
        if (generation === this.#generation) {
          const failure = contextFailure(error);
          this.#options.guard(); this.#send("context.transfer.error", { transfer_id: payload.transfer_id, ...failure });
          this.#options.error(failure.code, failure.retryable ? "선택한 context 전송을 다시 시도합니다." : "선택한 context를 전송하지 못했습니다.");
        }
      }
      return;
    }
    if (type === "state.changed" || type === "region.selected" || type === "region.focused") {
      if (type !== "state.changed" && typeof payload.region_id !== "string") return;
      if (payload.context_ref !== undefined && typeof payload.context_ref !== "string") return;
      this.#received = data.sequence as number;
      this.#displaySequence = data.sequence as number;
      if (typeof payload.context_ref === "string" && !(await this.#contexts!.acceptsEcho(payload.context_ref))) return;
      if (generation !== this.#generation || this.#displaySequence !== data.sequence) return;
      this.#options.guard(); this.#options.display(type, payload); return;
    }
    if (type === "viewer.error" && typeof payload.code === "string" && typeof payload.message === "string") {
      this.#received = data.sequence as number; this.#options.error(payload.code, payload.message);
    } else if (type === "reader.position.snapshot" && typeof payload.correlation_id === "string" && object(payload.position)) {
      this.#received = data.sequence as number; this.#options.position?.(payload.correlation_id, payload.position as { scroll_x?: unknown; scroll_y?: unknown });
    } else if (type === "reader.position.applied" && typeof payload.correlation_id === "string") {
      this.#received = data.sequence as number; this.#options.positionApplied?.(payload.correlation_id, payload);
    } else if (type === "viewer.location" && payload.page_path === this.#binding.page_path) {
      this.#received = data.sequence as number; this.#options.location?.(payload);
    } else if (type === "reference.requested" && this.#reference && Object.keys(payload).length === 1 && typeof payload.reference_id === "string" && payload.reference_id !== "") {
      this.#received = data.sequence as number; this.#reference(payload.reference_id);
    }
  }

  #send(type: string, payload: DisplayContext): void {
    if (!this.#port || !this.#binding) return;
    this.#port.postMessage({ protocol_version: "1.0", ...this.#binding, sequence: ++this.#sequence, type, payload });
  }

  referenceNavigation(callback?: (id: string) => void): void {
    this.#options.guard(); this.#reference = callback;
    this.#send("reference.navigation", { enabled: callback !== undefined });
  }

  focus(region: string): void { this.#options.guard(); this.#send("region.focus", { region_id: region }); }

  /** Supply the host's public route template for new-window mapping. */
  route(template: string): void { this.#options.guard(); this.#send("reader.logical_route", { template }); }

  /** Closed initial position restore (correlated, idempotent duplicates re-ack). */
  applyPosition(payload: { correlation_id: string; position: { scroll_x?: unknown; scroll_y?: unknown }; fragment?: string }): void {
    this.#options.guard();
    this.#send("reader.position.apply", {
      correlation_id: payload.correlation_id,
      generation: this.#generation,
      position: { scroll_x: typeof payload.position.scroll_x === "number" ? payload.position.scroll_x : 0, scroll_y: typeof payload.position.scroll_y === "number" ? payload.position.scroll_y : 0 },
      ...(payload.fragment !== undefined ? { fragment: payload.fragment } : {}),
    });
  }

  /** Request the current page scroll snapshot for autosave. */
  requestSnapshot(correlationId: string): void { this.#options.guard(); this.#send("reader.position.request", { correlation_id: correlationId }); }

  dispose(): void {
    this.#generation++; this.#port?.close(); this.#port = null; this.#contexts = null;
    this.#binding = null; this.#ready = false; this.#reference = undefined; this.#announcement = null;
    this.#sequence = 0; this.#received = 0; this.#displaySequence = 0;
  }
}
