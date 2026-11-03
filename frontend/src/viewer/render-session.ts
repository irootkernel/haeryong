import type { HaeryongClient, RenderSessionView } from "../client/types.ts";
import { ApiError } from "../client/types.ts";

/** A refused delivery closes the host Reader; a transport failure only retires its frame. */
export const renderDeliveryRefused = (error: unknown): boolean => error instanceof ApiError && [401, 403, 404, 409, 410].includes(error.status);

type SessionClient = Pick<HaeryongClient, "renderSessionOpen" | "renderSessionRenew" | "renderSessionClose">;
type SessionStorage = Pick<Storage, "getItem" | "setItem" | "removeItem">;
interface Actor { client: SessionClient; guard(): void }
export interface RetirementNotice { message: string }
export type RetirementCloseStatus = "not-dispatched" | "in-flight" | "unconfirmed-failure" | "confirmed-closed" | "observed-unavailable";
interface Opening {
  active: boolean;
  dispatched: boolean;
  failed: boolean;
  ready: boolean;
  key: string | null;
  view: RenderSessionView | null;
  promise: Promise<RenderSessionView> | null;
  timer: ReturnType<typeof setTimeout> | null;
  abortWait: (() => void) | null;
  retirement: Retirement | null;
  validation: Promise<void> | null;
}
function unavailable(error: unknown): boolean {
  if (!(error instanceof ApiError)) return false;
  if (error.status === 404 && error.code === "NOT_FOUND") return true;
  return [409, 410].includes(error.status) && ["render_session_closed", "render_session_stale"].includes(String(error.details?.reason));
}

// A record owns captured work, including an Open reply that has not arrived
// yet. A waiting lease owns no work and never replaces this barrier.
const retirements = new Map<string, Retirement>();
class Retirement {
  readonly work: Opening;
  readonly predecessor: Retirement | null;
  readonly key: string;
  readonly storage: SessionStorage;
  actor: Actor;
  kRetired = false;
  closeStatus: RetirementCloseStatus = "not-dispatched";
  cancelled = false;
  released = false;
  #attempt: Promise<void> | null = null;

  constructor(work: Opening, key: string, storage: SessionStorage, actor: Actor) {
    this.work = work; this.key = key; this.storage = storage; this.actor = actor;
    this.predecessor = retirements.get(key) ?? null;
    retirements.set(key, this);
  }
  #release(): void {
    this.released = true;
    if (retirements.get(this.key) === this) retirements.delete(this.key);
  }
  reacquire(work: Opening): boolean {
    if (this.work !== work || this.kRetired || this.closeStatus !== "not-dispatched" || this.predecessor || this.released) return false;
    this.cancelled = true;
    this.#release();
    return true;
  }
  wait(actor: Actor): Promise<void> {
    actor.guard();
    let previousAuthorityValid = true;
    try { this.actor.guard(); } catch { previousAuthorityValid = false; }
    this.actor = actor;
    // With K durably retired, a fresh K/S cannot be reached by an old Close.
    // Replacing invalid authority may release the barrier without claiming
    // that the old in-flight RPC was cancelled or acknowledged.
    if (!previousAuthorityValid && this.kRetired) {
      this.#release();
      void this.settle().catch(() => { /* Captured S falls back to lease expiry. */ });
      return Promise.resolve();
    }
    return this.settle();
  }
  settle(): Promise<void> {
    if (this.cancelled || this.released && ["confirmed-closed", "observed-unavailable"].includes(this.closeStatus)) return Promise.resolve();
    if (!this.#attempt) {
      // A synchronous StrictMode cleanup/setup gets its reversible window.
      const attempt = Promise.resolve().then(() => this.#run());
      this.#attempt = attempt;
      void attempt.finally(() => { if (this.#attempt === attempt) this.#attempt = null; }).catch(() => undefined);
    }
    return this.#attempt;
  }
  async #run(): Promise<void> {
    if (this.predecessor && !this.predecessor.released) await this.predecessor.wait(this.actor);
    try { await this.work.promise; } catch { /* A failed first Renew can still leave known S. */ }
    if (this.cancelled) return;
    const view = this.work.view;
    if (!view) { this.#release(); return; } // Unknown Open: retain K, send no Close.
    if (!this.kRetired) {
      const stored = this.storage.getItem(this.key);
      if (stored !== null) {
        if (stored !== this.work.key) throw new Error("다른 페이지 열기 기록이 남아 있어 이전 연결을 정리하지 못했습니다.");
        this.storage.removeItem(this.key);
        if (this.storage.getItem(this.key) !== null) throw new Error("이전 페이지 열기 기록을 지우지 못했습니다.");
      }
      this.kRetired = true;
    }
    try {
      this.actor.guard();
      this.closeStatus = "in-flight";
      await this.actor.client.renderSessionClose(view.version_id, view.session_id);
      this.closeStatus = "confirmed-closed";
    } catch (error) {
      if (!unavailable(error)) { this.closeStatus = "unconfirmed-failure"; throw error; }
      this.closeStatus = "observed-unavailable";
    }
    this.#release();
  }
}

// Only uncertain Open K is durable. Handoff secrets and live generations stay
// in memory; the journal identity includes the complete owner/session.
export class RenderSessionLease {
  readonly #actor: Actor;
  readonly #version: string;
  readonly #storage: SessionStorage;
  readonly #key: string;
  readonly #failure: (error: unknown) => void;
  readonly #notice: (notice: RetirementNotice) => void;
  #work: Opening | null = null;
  constructor(options: {
    client: SessionClient; version: string; owner: string; context: string;
    storage: SessionStorage; guard(): void; failure(error: unknown): void;
    notice?(notice: RetirementNotice): void;
  }) {
    this.#actor = { client: options.client, guard: options.guard };
    this.#version = options.version; this.#storage = options.storage;
    this.#key = "haeryong.reader.open.v1:" + JSON.stringify([options.owner, options.version, options.context]);
    this.#failure = options.failure; this.#notice = options.notice ?? (() => undefined);
  }
  #guard(work: Opening): void {
    if (!work.active || this.#work !== work) throw new Error("이전 읽기 화면의 요청입니다.");
    this.#actor.guard();
  }
  start(): Promise<RenderSessionView> {
    const previous = this.#work;
    if (previous?.active && previous.promise && !previous.failed) return previous.promise;
    try { this.#actor.guard(); } catch (error) { return Promise.reject(error); }
    if (previous?.retirement && !previous.failed && previous.retirement.reacquire(previous)) {
      previous.active = true; previous.retirement = null;
      if (previous.view && previous.ready) this.#schedule(previous, previous.view);
      return previous.promise!;
    }
    if (previous?.active) {
      previous.active = false;
      if (previous.view) previous.retirement ??= new Retirement(previous, this.#key, this.#storage, this.#actor);
    }
    const work: Opening = { active: true, dispatched: false, failed: false, ready: false, key: null, view: null, promise: null, timer: null, abortWait: null, retirement: null, validation: null };
    this.#work = work;
    work.promise = this.#open(work).catch((error) => { work.failed = true; throw error; });
    return work.promise;
  }
  async #open(work: Opening): Promise<RenderSessionView> {
    for (;;) {
      const retirement = retirements.get(this.#key);
      if (!retirement) break;
      this.#guard(work);
      this.#notice({ message: "이전 페이지 연결의 정리 결과를 확인하는 중입니다." });
      const aborted = new Promise<never>((_resolve, reject) => { work.abortWait = () => reject(new Error("이전 읽기 화면의 요청입니다.")); });
      try { await Promise.race([retirement.wait(this.#actor), aborted]); }
      finally { work.abortWait = null; }
      this.#guard(work);
    }
    for (;;) {
      this.#guard(work);
      let key = this.#storage.getItem(this.#key);
      if (key !== null && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(key)) throw new Error("페이지 열기 요청 기록을 확인하지 못했습니다.");
      key ??= crypto.randomUUID();
      this.#storage.setItem(this.#key, key);
      work.key = key; work.dispatched = true;
      const view = await this.#actor.client.renderSessionOpen(this.#version, key);
      // Late replies update only captured work: retirement still needs its S.
      work.view = view;
      if (!work.active) return view;
      this.#guard(work);
      let stale = Date.parse(view.handoff.expires_at) <= Date.now();
      try { await this.#actor.client.renderSessionRenew(view.version_id, view.session_id); }
      catch (error) { if (unavailable(error)) stale = true; else throw error; }
      if (!work.active) return view;
      this.#guard(work);
      if (this.#storage.getItem(this.#key) !== key) throw new Error("페이지 열기 요청 기록이 변경되었습니다.");
      this.#storage.removeItem(this.#key);
      if (this.#storage.getItem(this.#key) !== null) throw new Error("페이지 열기 요청 기록을 지우지 못했습니다.");
      if (stale) {
        try { await this.#actor.client.renderSessionClose(view.version_id, view.session_id); }
        catch (error) { if (!unavailable(error)) throw error; }
        this.#guard(work);
        work.view = null;
        continue;
      }
      work.ready = true;
      this.#schedule(work, view);
      return view;
    }
  }
  #schedule(work: Opening, view: RenderSessionView): void {
    if (work.timer !== null) clearTimeout(work.timer);
    if (!work.active || work.view !== view) return;
    const interval = Math.max(25, Math.min(60_000, (Date.parse(view.lease_expires_at) - Date.now()) / 3));
    work.timer = setTimeout(() => {
      void this.revalidate().catch(() => { /* The captured delivery failure is reported once. */ });
    }, interval);
  }
  /** Check the EXISTING delivery after invalidation/reconnect, independently
   * of whether its document has already become readable again. Never Open. */
  revalidate(): Promise<void> {
    const work = this.#work, view = work?.view;
    if (!work?.active || !work.ready || !view) return Promise.resolve();
    if (work.validation) return work.validation;
    work.validation = (async () => {
      this.#guard(work);
      await this.#actor.client.renderSessionRenew(view.version_id, view.session_id);
      this.#guard(work);
      this.#schedule(work, view);
    })().catch((error) => {
      if (work.active && this.#work === work) {
        work.ready = false; work.failed = true;
        if (work.timer !== null) { clearTimeout(work.timer); work.timer = null; }
        this.#failure(error);
      }
      throw error;
    }).finally(() => { work.validation = null; });
    return work.validation;
  }
  stop(): Promise<void> {
    const work = this.#work;
    if (!work) return Promise.resolve();
    work.active = false; work.abortWait?.();
    if (work.timer !== null) { clearTimeout(work.timer); work.timer = null; }
    if (!work.dispatched) return Promise.resolve();
    work.retirement ??= new Retirement(work, this.#key, this.#storage, this.#actor);
    return work.retirement.settle();
  }
}
