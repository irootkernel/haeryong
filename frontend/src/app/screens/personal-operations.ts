// Mutable personal resources use immutable, journalled requests. A later
// observation is queued separately from the request already sent to the server.
import type { HaeryongClient, PersonalKind, PersonalView } from "../../client/types.ts";
import { ApiError } from "../../client/types.ts";
import { reconcilePersonal412 } from "./personal-cas.ts";

export interface PersonalTarget { version: string; kind: PersonalKind; page: string | null }
type Conditions = { ifMatch: string } | { create: true };
interface Operation {
  target: PersonalTarget;
  key: string;
  intent: unknown;
  base: PersonalView;
  conditions: Conditions;
  phase: "write" | "reconcile";
}
export interface PersonalEntry {
  target: PersonalTarget;
  acknowledged?: PersonalView;
  pending?: Operation;
  queued?: { intent: unknown };
  conflict?: { operation: Operation; server: PersonalView };
  issue?: string;
}
interface Journal { read(): PersonalEntry[]; write(entries: PersonalEntry[]): void | Promise<void> }
const copy = <T,>(value: T): T => structuredClone(value);
export const personalKey = (target: PersonalTarget): string => JSON.stringify([target.version, target.kind, target.page]);

/** Another window of this owner changed a recovery record after we last read it. */
export class ConcurrentJournalError extends Error {
  constructor() { super("다른 창에서 읽기 상태 기록을 변경했습니다."); this.name = "ConcurrentJournalError"; }
}
/** This context cannot take the cross-window journal lock; saves fail closed. */
export class JournalLockUnavailableError extends Error {
  constructor() { super("읽기 상태 동시 저장 잠금을 사용할 수 없습니다."); this.name = "JournalLockUnavailableError"; }
}
type LockRequest = <R>(name: string, criticalSection: () => R) => Promise<R>;
const browserLocks = (): LockRequest | null => {
  const locks = (globalThis as { navigator?: { locks?: { request?: <R>(name: string, criticalSection: () => R) => Promise<R> } } }).navigator?.locks;
  return typeof locks?.request === "function" ? locks.request.bind(locks) : null;
};

export class BrowserPersonalJournal implements Journal {
  readonly #storage: Pick<Storage, "getItem" | "setItem">;
  readonly #key: string;
  readonly #locks: LockRequest | null | undefined;
  /** Exact serialized per-resource records this instance last read or wrote. */
  #snapshots = new Map<string, string>();
  constructor(storage: Pick<Storage, "getItem" | "setItem">, owner: string, locks?: LockRequest | null) {
    this.#storage = storage;
    this.#key = "haeryong.reader.personal.v1:" + owner;
    this.#locks = locks;
  }
  #parse(raw: string | null): Map<string, { entry: PersonalEntry; serialized: string }> {
    if (raw === null) return new Map();
    const data: unknown = JSON.parse(raw);
    if (!data || typeof data !== "object" || (data as { version?: unknown }).version !== 1 || !Array.isArray((data as { entries?: unknown }).entries)) throw new Error("읽기 상태 요청 기록을 확인할 수 없습니다.");
    const rows = new Map<string, { entry: PersonalEntry; serialized: string }>();
    for (const entry of (data as { entries: PersonalEntry[] }).entries) {
      if (!entry.target || typeof entry.target.version !== "string" || !["reader-preference", "reading-position", "page-position"].includes(entry.target.kind) ||
          (entry.target.page !== null && typeof entry.target.page !== "string") ||
          (entry.pending && (personalKey(entry.pending.target) !== personalKey(entry.target) || typeof entry.pending.key !== "string" || !["write", "reconcile"].includes(entry.pending.phase)))) {
        throw new Error("읽기 상태 요청 기록의 대상이 올바르지 않습니다.");
      }
      rows.set(personalKey(entry.target), { entry, serialized: JSON.stringify(entry) });
    }
    return rows;
  }
  read(): PersonalEntry[] {
    const rows = this.#parse(this.#storage.getItem(this.#key));
    this.#snapshots = new Map([...rows].map(([key, row]) => [key, row.serialized]));
    return [...rows.values()].map((row) => row.entry);
  }
  #merge(proposed: Map<string, string>): void {
    // Synchronous critical section: read the latest bucket, compare against
    // this instance's known snapshots, apply ONLY its changed/deleted records
    // and keep every foreign record exactly current. A concurrent change of a
    // record this instance would touch fails before any write happens.
    const rows = this.#parse(this.#storage.getItem(this.#key));
    const committed = new Map<string, string>();
    const removed = new Set<string>();
    for (const [key, serialized] of proposed) {
      if (this.#snapshots.get(key) === serialized) continue;
      const latest = rows.get(key);
      const known = this.#snapshots.get(key);
      if (known === undefined ? latest !== undefined : latest?.serialized !== known) throw new ConcurrentJournalError();
      rows.set(key, { entry: JSON.parse(serialized) as PersonalEntry, serialized });
      committed.set(key, serialized);
    }
    for (const key of this.#snapshots.keys()) {
      if (proposed.has(key)) continue;
      const latest = rows.get(key);
      if (latest?.serialized !== this.#snapshots.get(key)) throw new ConcurrentJournalError();
      rows.delete(key);
      removed.add(key);
    }
    const serialized = JSON.stringify({ version: 1, entries: [...rows.values()].map((row) => row.entry) });
    if (serialized !== this.#storage.getItem(this.#key)) this.#storage.setItem(this.#key, serialized);
    // Local knowledge advances ONLY through records this instance itself
    // committed or removed. A foreign version of a record this instance
    // merely re-proposed unchanged is never absorbed as local knowledge: a
    // later local change of that record must collide honestly instead of
    // silently reacquiring it.
    for (const [key, value] of committed) this.#snapshots.set(key, value);
    for (const key of removed) this.#snapshots.delete(key);
  }
  async write(entries: PersonalEntry[]): Promise<void> {
    // Snapshot the incoming records before any await; later in-flight intent
    // mutations can never enter this transaction after it is captured.
    const proposed = new Map(entries.map((entry) => [personalKey(entry.target), JSON.stringify(copy(entry))]));
    const locks = this.#locks !== undefined ? this.#locks : browserLocks();
    // Without the cross-window lock there is no atomic read/compare/merge/
    // write: saves fail closed instead of falling back to a whole-array
    // non-atomic overwrite.
    if (locks === null) throw new JournalLockUnavailableError();
    await locks(this.#key, () => this.#merge(proposed));
  }
}

/** Honest issue copy for a failed journal transaction; never claims a save. */
const persistIssue = (error: unknown): string =>
  error instanceof JournalLockUnavailableError ? "동시 저장 잠금을 사용할 수 없어 저장하지 못했습니다. 이 화면을 새로고침한 뒤 다시 시도하세요."
  : error instanceof ConcurrentJournalError ? "다른 창에서 읽기 상태 기록을 변경했습니다. 이 화면을 새로고침한 뒤 원래 요청으로 다시 확인하세요."
  : "읽기 상태 요청을 기기에 기록하지 못했습니다. 저장소를 확인한 후 다시 시도하세요.";

export class PersonalOperations {
  readonly #entries = new Map<string, PersonalEntry>();
  readonly #running = new Map<string, Promise<void>>();
  readonly #listeners = new Set<() => void>();
  readonly #resolving = new Set<string>();
  readonly #recovered = new Set<string>();
  #active = true;
  #revision = 0;

  readonly #client: Pick<HaeryongClient, "personalGet" | "personalPut">;
  readonly #journal: Journal;
  readonly #guard: () => void;
  constructor(client: Pick<HaeryongClient, "personalGet" | "personalPut">, journal: Journal, guard: () => void) {
    this.#client = client; this.#journal = journal; this.#guard = guard;
    for (const entry of journal.read()) {
      const key = personalKey(entry.target);
      this.#entries.set(key, entry);
      if (entry.pending || entry.queued) this.#recovered.add(key);
    }
  }

  subscribe = (listener: () => void): (() => void) => { this.#listeners.add(listener); return () => this.#listeners.delete(listener); };
  revision = (): number => this.#revision;
  busy(target: PersonalTarget): boolean { const key = personalKey(target); return this.#running.has(key) || this.#resolving.has(key); }
  recoverable(target: PersonalTarget): boolean { return this.#recovered.has(personalKey(target)); }
  entries(): PersonalEntry[] { return [...this.#entries.values()].map(copy); }
  entry(target: PersonalTarget): PersonalEntry | undefined { const value = this.#entries.get(personalKey(target)); return value && copy(value); }
  #changed(): void { this.#revision++; for (const listener of this.#listeners) listener(); }
  // Persistence is awaited BEFORE any effect that depends on it. The records
  // are cloned synchronously here, so no in-flight mutation can enter the
  // transaction after this point.
  async #persist(): Promise<void> {
    const proposed = this.entries().filter((entry) => entry.pending || entry.conflict || entry.queued);
    await this.#journal.write(proposed);
    this.#changed();
  }
  // Durability gate: returns false only after recording an honest issue; the
  // caller must not dispatch or apply effects when it does.
  async #persistEntry(entry: PersonalEntry): Promise<boolean> {
    try { await this.#persist(); return true; }
    catch (error) {
      if (this.#active) { entry.issue = persistIssue(error); this.#changed(); }
      return false;
    }
  }
  #check(): void { if (!this.#active) throw new Error("이전 읽기 화면의 요청입니다."); this.#guard(); }
  #get(target: PersonalTarget): PersonalEntry {
    const key = personalKey(target);
    let entry = this.#entries.get(key);
    if (!entry) { entry = { target: copy(target) }; this.#entries.set(key, entry); }
    return entry;
  }

  async read(target: PersonalTarget): Promise<PersonalView> {
    this.#check();
    const value = await this.#client.personalGet(target.kind, target.version, target.page ?? undefined);
    this.#check();
    const entry = this.#get(target);
    entry.acknowledged = copy(value);
    this.#changed();
    return value;
  }

  async save(target: PersonalTarget, intent: unknown): Promise<void> {
    this.#check();
    const entry = this.#get(target);
    entry.queued = { intent: copy(intent) };
    if (!(await this.#persistEntry(entry))) return;
    // The authority and screen must still be current after the persistence
    // wait before anything is dispatched.
    this.#check();
    // A new scroll never rewrites an in-flight request, resolves a conflict,
    // or replaces the key of an outcome which is still unknown.
    if (entry.conflict || entry.issue) return;
    await this.#run(entry);
  }

  async retry(target: PersonalTarget): Promise<void> {
    this.#check();
    const entry = this.#get(target);
    if (entry.conflict && !entry.pending) return;
    delete entry.issue;
    if (!(await this.#persistEntry(entry))) return;
    this.#check();
    await this.#run(entry);
  }

  async resolve(target: PersonalTarget, choice: "server" | "local"): Promise<void> {
    this.#check();
    const entry = this.#get(target);
    const key = personalKey(target);
    if (!entry.conflict || entry.pending || this.busy(target)) return;
    this.#resolving.add(key); this.#changed();
    const chosenQueue = entry.queued;
    const selected = copy(chosenQueue ? chosenQueue.intent : entry.conflict.operation.intent);
    try {
      const fresh = await this.#client.personalGet(target.kind, target.version, target.page ?? undefined);
      this.#check();
      entry.acknowledged = copy(fresh);
      if (choice === "server") {
        delete entry.conflict; delete entry.issue;
        if (entry.queued === chosenQueue) delete entry.queued;
        if (!(await this.#persistEntry(entry))) return;
        this.#check();
        // A later observation is a distinct intent after the server choice.
        if (entry.queued) await this.#run(entry);
        return;
      }
      entry.pending = this.#operation(target, selected, fresh);
      if (entry.queued === chosenQueue) delete entry.queued;
      delete entry.issue;
      if (!(await this.#persistEntry(entry))) return;
      this.#check();
      await this.#run(entry);
    } catch {
      entry.issue = "선택한 읽기 상태를 확인하지 못했습니다. 연결 후 다시 선택해 주세요.";
      this.#changed();
    } finally { this.#resolving.delete(key); this.#changed(); }
  }

  #operation(target: PersonalTarget, intent: unknown, base: PersonalView): Operation {
    return { target: copy(target), key: crypto.randomUUID(), intent: copy(intent), base: copy(base), conditions: base.exists ? { ifMatch: base.etag! } : { create: true }, phase: "write" };
  }

  #run(entry: PersonalEntry): Promise<void> {
    const key = personalKey(entry.target);
    const active = this.#running.get(key);
    if (active) return active;
    const work = this.#drain(entry).finally(() => { this.#running.delete(key); this.#changed(); });
    this.#running.set(key, work);
    this.#recovered.delete(key); this.#changed();
    return work;
  }

  async #drain(entry: PersonalEntry): Promise<void> {
    try {
      while (this.#active) {
        this.#check();
        if (!entry.pending) {
          if (entry.conflict || !entry.queued) return;
          const base = entry.acknowledged ?? await this.read(entry.target);
          this.#check();
          entry.pending = this.#operation(entry.target, entry.queued.intent, base);
          delete entry.queued;
        }
        // This record is durable BEFORE every effect, including a rebase. A
        // persistence failure (lock unavailable, concurrent window change or
        // storage denial) stops the drain before any dispatch; the original
        // K/body/conditions stay exactly as they were.
        if (!(await this.#persistEntry(entry))) return;
        this.#check();
        const operation = entry.pending;
        if (operation.phase === "reconcile") {
          const fresh = await this.#client.personalGet(operation.target.kind, operation.target.version, operation.target.page ?? undefined);
          this.#check();
          const decision = reconcilePersonal412({ family: operation.target.kind, base: operation.base, intent: operation.intent, fresh });
          if (decision === "conflict") {
            entry.conflict = { operation: copy(operation), server: copy(fresh) };
            delete entry.pending;
            if (!(await this.#persistEntry(entry))) return;
            return;
          }
          if (decision === "converged") {
            entry.acknowledged = copy(fresh); delete entry.pending; delete entry.conflict; delete entry.issue;
            if (!(await this.#persistEntry(entry))) return;
            this.#check();
            continue;
          }
          // Only an objectively unchanged base permits another key. The
          // same decision is applied after EVERY later confirmed 412.
          entry.pending = this.#operation(operation.target, operation.intent, fresh);
          if (!(await this.#persistEntry(entry))) return;
          this.#check();
          await new Promise((resolve) => setTimeout(resolve, 25));
          continue;
        }
        let saved: PersonalView;
        try {
          saved = await this.#client.personalPut(operation.target.kind, operation.target.version, operation.target.page, operation.intent, operation.conditions, operation.key);
        } catch (error) {
          this.#check();
          if (error instanceof ApiError && error.status === 412) {
            entry.pending = { ...operation, phase: "reconcile" };
            if (!(await this.#persistEntry(entry))) return;
            this.#check();
            continue;
          }
          // The original body/key/preconditions remain intact. In particular
          // a transport error never enters the new-key reconciliation path.
          entry.issue = error instanceof ApiError && !error.retryable ? "저장을 확인하지 못했습니다: " + error.code : "저장 결과가 확인되지 않았습니다. 원래 요청으로 다시 확인하세요.";
          await this.#persistEntry(entry);
          return;
        }
        this.#check();
        entry.acknowledged = copy(saved); delete entry.pending; delete entry.conflict; delete entry.issue;
        // The PUT has committed; if the journal cannot record that, the older
        // durable pending record honestly stays for same-K replay.
        if (!(await this.#persistEntry(entry))) return;
        this.#check();
        // A queued observation is a separate request, even if its visible
        // value matches (reopening a page is still a pointer observation).
      }
    } catch (error) {
      if (this.#active) {
        entry.issue = error instanceof Error ? error.message : "읽기 상태 요청을 확인하지 못했습니다.";
        this.#changed();
      }
    }
  }

  dispose(): void { this.#active = false; this.#listeners.clear(); }
}
