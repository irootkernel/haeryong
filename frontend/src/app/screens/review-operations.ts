import type { HaeryongClient, ReviewAction, ReviewCondition, ReviewRequest, ReviewView } from "../../client/types.ts";
import { ApiError } from "../../client/types.ts";

export const reviewRequest = (action: ReviewAction): ReviewRequest => ({ action, state: ({ start: "reviewing", complete: "reviewed", reopen: "reviewing", clear: "unreviewed" } as const)[action] });
export const reviewLabel = (view: ReviewView): string => ({unreviewed:"미검토", reviewing:"검토 중", reviewed:"검토 완료"})[view.value.state];
export interface ReviewIntent { generation: string; version: string; key: string; body: ReviewRequest; condition: ReviewCondition; rejected?: boolean }
export interface ReviewJournal { read(): ReviewIntent | null; change(expected: ReviewIntent | null, next: ReviewIntent | null): Promise<void> }

/** The lock and compare prevent another window's pending action from being overwritten. */
export function reviewJournal(storage: Pick<Storage,"getItem"|"setItem"|"removeItem">, namespace: string): ReviewJournal {
  const name = "haeryong.review.v1:" + namespace;
  const read = (): ReviewIntent | null => {
    const raw = storage.getItem(name); if (raw === null) return null;
    const value = JSON.parse(raw) as ReviewIntent;
    if (!value || typeof value.generation !== "string" || typeof value.version !== "string" || typeof value.key !== "string" || !value.condition ||
      !("ifMatch" in value.condition && typeof value.condition.ifMatch === "string" || "create" in value.condition && value.condition.create === true) ||
      !value.body || !["start","complete","reopen","clear"].includes(value.body.action) || value.body.state !== reviewRequest(value.body.action).state) throw Error("Review 요청 기록을 확인할 수 없습니다.");
    return value;
  };
  return { read, async change(expected, next) {
    if (!navigator.locks) throw Error("Review 요청 기록의 동시 저장 잠금을 사용할 수 없습니다.");
    await navigator.locks.request(name, () => {
      if (JSON.stringify(read()) !== JSON.stringify(expected)) throw Error("다른 창의 Review 요청을 먼저 확인하세요.");
      if (next) storage.setItem(name, JSON.stringify(next)); else storage.removeItem(name);
    });
  }};
}

/** Only an explicit host action calls submit. Unknown responses retain the original immutable request. */
export class ReviewOperations {
  readonly client: HaeryongClient; readonly version: string; readonly generation:string; readonly journal: ReviewJournal;
  pending: ReviewIntent | null; busy = false; disposed = false;
  constructor(client: HaeryongClient, version: string, journal: ReviewJournal, generation="") {
    this.generation=generation;
    this.client=client; this.version=version; this.journal=journal; this.pending=journal.read();
    if (this.pending && this.pending.version !== version) throw Error("Review 요청 버전이 일치하지 않습니다.");
  }
  async submit(action: ReviewAction, base: ReviewView, guard: () => void): Promise<ReviewView> {
    if (this.busy || this.pending) throw Error("기존 Review 요청을 먼저 확인하세요.");
    guard();
    const intent: ReviewIntent = { generation:this.generation, version:this.version, key:crypto.randomUUID(), body:reviewRequest(action), condition:base.exists ? {ifMatch:base.etag!} : {create:true} };
    this.busy=true;
    try { await this.journal.change(null,intent); this.pending=intent; return await this.dispatch(guard); }
    finally { this.busy=false; }
  }
  get generationChanged():boolean {return !!this.pending && (this.pending.generation==="" ? this.generation!=="" : this.pending.generation!==this.generation);}
  async retry(guard: () => void): Promise<ReviewView> {
    if (this.busy || !this.pending || this.pending.rejected) throw Error("Review의 현재 상태를 확인하고 동작을 다시 선택하세요.");
    this.busy=true;
    try {
      guard(); if (this.disposed) throw Error("이전 Review 화면입니다.");
      const intent = this.pending;
      let missing = false;
      if (typeof this.client.operation === "function") {
        let receipt = null;
        try {
          receipt = await this.client.operation(intent.key);
        } catch (error) {
          guard();
          if (error instanceof ApiError && error.status === 404) {
            missing = true;
          } else {
            throw error;
          }
        }
        guard();
        if (!missing && receipt) {
          if (receipt.state === "accepted" || receipt.state === "pending" || receipt.state === "reconciling") {
            throw Error("이전 Review 요청이 서버에서 아직 처리 중입니다. 잠시 후 다시 확인하세요.");
          }
          if (receipt.state === "completed") {
            await this.journal.change(intent, null);
            this.pending = null;
            guard();
            try {
              const fresh = await this.client.reviewGet(this.version);
              if (fresh.version_id !== this.version) throw Error("Review 응답 버전이 일치하지 않습니다.");
              guard();
              return fresh;
            } catch {
              throw Error("원래 Review 요청은 완료되었으나 현재 서버 상태를 불러오지 못했습니다. 다시 확인해 주세요.");
            }
          }
          if (receipt.state === "failed") {
            const rejected = { ...intent, rejected: true };
            await this.journal.change(intent, rejected);
            this.pending = rejected;
            throw Error(receipt.error?.message || "이전 Review 요청이 서버에서 거절되었습니다. 현재 상태를 확인하고 선택하세요.");
          }
        }
      }
      if (this.generationChanged) {
        const rejected = { ...intent, rejected: true };
        await this.journal.change(intent, rejected);
        this.pending = rejected;
        throw Error("서비스 세대가 변경되어 이전 Review 요청을 자동 재전송하지 않습니다. 현재 상태를 확인하고 동작을 다시 선택하세요.");
      }
      if (missing && typeof this.client.reviewGet === "function") {
        const current = await this.client.reviewGet(this.version);
        guard();
        if (current.version_id !== this.version) throw Error("Review 응답 버전이 일치하지 않습니다.");
        const conditionMatched = "create" in intent.condition
          ? !current.exists
          : current.exists && current.etag === intent.condition.ifMatch;
        if (!conditionMatched) {
          const rejected = { ...intent, rejected: true };
          await this.journal.change(intent, rejected);
          this.pending = rejected;
          throw Error("이전 Review 요청이 반영되기 전에 서버 상태가 변경되었습니다. 현재 상태를 확인하고 동작을 다시 선택하세요.");
        }
      }
      return await this.dispatch(guard);
    } finally { this.busy=false; }
  }
  async applyRejected(base: ReviewView, guard: () => void): Promise<ReviewView> {
    if (this.busy || !this.pending || !this.pending.rejected) {
      throw Error("확인되지 않은 요청은 원래 요청으로 다시 확인하세요.");
    }
    guard();
    const oldIntent = this.pending;
    if (base.value.state === oldIntent.body.state) {
      await this.journal.change(oldIntent, null);
      this.pending = null;
      return base;
    }
    const targetState = oldIntent.body.state;
    const action: ReviewAction =
      targetState === "reviewed"
        ? "complete"
        : targetState === "reviewing"
          ? (base.value.state === "reviewed" ? "reopen" : "start")
          : "clear";
    const nextIntent: ReviewIntent = {
      generation: this.generation,
      version: this.version,
      key: crypto.randomUUID(),
      body: reviewRequest(action),
      condition: base.exists ? { ifMatch: base.etag! } : { create: true },
    };
    this.busy = true;
    try {
      await this.journal.change(oldIntent, nextIntent);
      this.pending = nextIntent;
      return await this.dispatch(guard);
    } finally {
      this.busy = false;
    }
  }
  async dispatch(guard: () => void): Promise<ReviewView> {
    guard(); if (this.disposed) throw Error("이전 Review 화면입니다.");
    const intent=this.pending!;
    try {
      const view=await this.client.reviewPut(intent.version,intent.body,intent.condition,intent.key);
      // A response for an old route may acknowledge its journal, but never supplies another route's UI.
      if (view.version_id !== this.version) throw Error("Review 응답 버전이 일치하지 않습니다.");
      await this.journal.change(intent,null); this.pending=null; guard();
      // The transport may have recovered a lost first response internally via
      // sourceWrite. Its successful DTO can therefore be historical even when
      // this controller has not entered its explicit retry path.
      try {
        const fresh = await this.client.reviewGet(this.version);
        if (fresh.version_id !== this.version) throw Error("Review 응답 버전이 일치하지 않습니다.");
        guard();
        return fresh;
      } catch {
        throw Error("원래 Review 요청은 완료되었으나 현재 서버 상태를 불러오지 못했습니다. 다시 확인해 주세요.");
      }
    } catch(error) {
      if (error instanceof ApiError && [400,403,404,409,412,428].includes(error.status) && error.code!=="OPERATION_RECONCILIATION_REQUIRED") {
        const rejected={...intent,rejected:true}; await this.journal.change(intent,rejected); this.pending=rejected;
      }
      throw error;
    }
  }
  async discard(guard: () => void): Promise<void> {
    guard(); if (this.busy || !this.pending || !this.pending.rejected) throw Error("확인되지 않은 요청은 원래 요청으로 다시 확인하세요.");
    await this.journal.change(this.pending,null); this.pending=null;
  }
}
