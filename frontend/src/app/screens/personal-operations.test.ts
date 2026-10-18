import test from "node:test";
import assert from "node:assert/strict";
import { ApiError, NetworkError } from "../../client/types.ts";
import type { HaeryongClient, PersonalView } from "../../client/types.ts";
import { BrowserPersonalJournal, ConcurrentJournalError, JournalLockUnavailableError, PersonalOperations } from "./personal-operations.ts";
import type { PersonalEntry, PersonalTarget } from "./personal-operations.ts";

const target: PersonalTarget = { version: "v1", kind: "reading-position", page: null };
const intent = (page: string) => ({ page_path: page, page_query: "", page_fragment: "", opened: true });
const value = (page: string, rev: number): PersonalView => ({
  exists: true, value: { ...intent(page), first_opened_at: "first", last_opened_at: "time-" + rev },
  revision: rev, etag: '"opaque-' + rev + '"', page_position_url: "/position",
});
class Journal {
  rows: PersonalEntry[] = [];
  denied = false;
  read() { return structuredClone(this.rows); }
  write(rows: PersonalEntry[]) { if (this.denied) throw new Error("storage denied"); this.rows = structuredClone(rows); }
}
/** Exclusive per-name serialization with the Web Locks request shape. */
class SerialLocks {
  #tails = new Map<string, Promise<unknown>>();
  request<R>(name: string, criticalSection: () => R): Promise<R> {
    const previous = this.#tails.get(name) ?? Promise.resolve();
    const run = previous.then(criticalSection);
    this.#tails.set(name, run.then(() => undefined, () => undefined));
    return run;
  }
}
class GatedJournal {
  rows: PersonalEntry[] = [];
  release!: () => void;
  readonly gate = new Promise<void>((resolve) => { this.release = resolve; });
  read() { return structuredClone(this.rows); }
  write(rows: PersonalEntry[]) { return this.gate.then(() => { this.rows = structuredClone(rows); }); }
}
const ownerStorage = (owner: string) => {
  const values = new Map<string, string>();
  return {
    storage: { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, value); } },
    bucket: () => values.get("haeryong.reader.personal.v1:" + owner) ?? null,
  };
};
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((accept) => { resolve = accept; });
  return { promise, resolve };
}

test("a lost reply restores the exact original request, key and precondition", async () => {
  const { storage } = ownerStorage("same-owner-session");
  const locks = new SerialLocks();
  const journal = new BrowserPersonalJournal(storage, "same-owner-session", locks.request.bind(locks));
  const requests: unknown[][] = [];
  let lost = true;
  const client: Pick<HaeryongClient, "personalGet" | "personalPut"> = {
    async personalGet() { return value("a.html", 1); },
    async personalPut(...args) { requests.push(structuredClone(args)); if (lost) { lost = false; throw new NetworkError(); } return value("b.html", 2); },
  };
  const first = new PersonalOperations(client, journal, () => {});
  await first.read(target);
  await first.save(target, intent("b.html"));
  assert.ok(first.entry(target)?.pending);
  first.dispose();
  const restored = new PersonalOperations(client, journal, () => {});
  await restored.retry(target);
  assert.equal(requests.length, 2);
  assert.deepEqual(requests[1], requests[0]);
  assert.equal(restored.entry(target)?.pending, undefined);
  assert.equal(restored.entry(target)?.acknowledged?.etag, '"opaque-2"');
  assert.deepEqual(journal.read(), []);
});

test("a later observation never mutates the body already in flight", async () => {
  const waiting = deferred<PersonalView>();
  const started = deferred<void>();
  const journal = new Journal();
  const bodies: unknown[] = [], keys: string[] = [];
  const client: Pick<HaeryongClient, "personalGet" | "personalPut"> = {
    async personalGet() { return value("a.html", 1); },
    async personalPut(_kind, _version, _page, body, _conditions, key) {
      bodies.push(structuredClone(body)); keys.push(key);
      if (bodies.length === 1) { started.resolve(); return waiting.promise; }
      return value("c.html", 3);
    },
  };
  const store = new PersonalOperations(client, journal, () => {});
  const first = store.save(target, intent("b.html"));
  await started.promise;
  const second = store.save(target, intent("c.html"));
  assert.deepEqual(store.entry(target)?.pending?.intent, intent("b.html"));
  assert.deepEqual(store.entry(target)?.queued?.intent, intent("c.html"));
  waiting.resolve(value("b.html", 2));
  await Promise.all([first, second]);
  assert.deepEqual(bodies, [intent("b.html"), intent("c.html")]);
  assert.notEqual(keys[0], keys[1]);
});

test("another device reopening A keeps pending B dirty, including a repeated explicit conflict", async () => {
  const journal = new Journal();
  let server = value("a.html", 1), calls = 0;
  const client: Pick<HaeryongClient, "personalGet" | "personalPut"> = {
    async personalGet() { return structuredClone(server); },
    async personalPut() { calls++; server = value("a.html", calls + 1); throw new ApiError(412, "PRECONDITION_FAILED", false); },
  };
  const store = new PersonalOperations(client, journal, () => {});
  await store.read(target);
  await store.save(target, intent("b.html"));
  assert.equal(calls, 1);
  assert.ok(store.entry(target)?.conflict);
  await store.resolve(target, "local");
  assert.equal(calls, 2);
  assert.ok(store.entry(target)?.conflict);
  assert.deepEqual(store.entry(target)?.conflict?.operation.intent, intent("b.html"));
  await store.resolve(target, "server");
  assert.equal(calls, 2);
  assert.equal(store.entry(target)?.conflict, undefined);
});

test("every confirmed unchanged-base 412 uses the objective rule, without a retry-count cutoff", async () => {
  const keys: string[] = [];
  const client: Pick<HaeryongClient, "personalGet" | "personalPut"> = {
    async personalGet() { return value("a.html", 1); },
    async personalPut(_kind, _version, _page, _body, _precondition, key) {
      keys.push(key);
      if (keys.length < 4) throw new ApiError(412, "PRECONDITION_FAILED", false);
      return value("b.html", 2);
    },
  };
  const store = new PersonalOperations(client, new Journal(), () => {});
  await store.save(target, intent("b.html"));
  assert.equal(keys.length, 4);
  assert.equal(new Set(keys).size, 4);
  assert.equal(store.entry(target)?.conflict, undefined);
  assert.equal(store.entry(target)?.pending, undefined);
});

test("converged fresh server intent needs no second write", async () => {
  let calls = 0;
  const client: Pick<HaeryongClient, "personalGet" | "personalPut"> = {
    async personalGet() { return calls ? value("b.html", 7) : value("a.html", 1); },
    async personalPut() { calls++; throw new ApiError(412, "PRECONDITION_FAILED", false); },
  };
  const store = new PersonalOperations(client, new Journal(), () => {});
  await store.save(target, intent("b.html"));
  assert.equal(calls, 1);
  assert.equal(store.entry(target)?.pending, undefined);
  assert.equal(store.entry(target)?.acknowledged?.etag, '"opaque-7"');
});

test("journal denial and authority replacement prevent dispatch", async () => {
  const journal = new Journal(); journal.denied = true;
  let writes = 0, current = true;
  const client: Pick<HaeryongClient, "personalGet" | "personalPut"> = {
    async personalGet() { return value("a.html", 1); },
    async personalPut() { writes++; return value("b.html", 2); },
  };
  const store = new PersonalOperations(client, journal, () => { assert.ok(current, "replaced authority"); });
  await store.save(target, intent("b.html"));
  assert.equal(writes, 0);
  journal.denied = false; current = false;
  await assert.rejects(store.retry(target));
  assert.equal(writes, 0);
});

test("queued observations and dirty records retain their exact version/page target", async () => {
  const journal = new Journal();
  const calls: unknown[][] = [];
  const a: PersonalTarget = { version: "old-version", kind: "page-position", page: "a:plus+%2F.html" };
  const b: PersonalTarget = { version: "new-version", kind: "page-position", page: "b.html" };
  const client: Pick<HaeryongClient, "personalGet" | "personalPut"> = {
    async personalGet() { return { ...value("a.html", 1), value: { position: {}, page_query: "", page_fragment: "" } }; },
    async personalPut(...args) { calls.push(structuredClone(args)); if (args[1] === a.version) throw new NetworkError(); return value("b.html", 2); },
  };
  const store = new PersonalOperations(client, journal, () => {});
  await store.save(a, { position: { scroll_y: 100 }, page_query: "", page_fragment: "" });
  await store.save(b, { position: { scroll_y: 200 }, page_query: "", page_fragment: "" });
  await store.retry(a);
  assert.equal(calls[0][1], "old-version");
  assert.equal(calls[1][1], "new-version");
  assert.deepEqual(calls[2], calls[0]);
});

for (const choice of ["local", "server"] as const) {
  test(`a deferred ${choice} choice preserves later observations and excludes a second resolver`, async () => {
    const gate = deferred<PersonalView>();
    let paused = false, server = value("a.html", 1), reads = 0;
    const bodies: unknown[] = [];
    const store = new PersonalOperations({
      async personalGet() { reads++; return paused ? gate.promise : structuredClone(server); },
      async personalPut(_kind, _version, _page, body) {
        bodies.push(structuredClone(body));
        if (bodies.length === 1) { server = value("a.html", 2); throw new ApiError(412, "PRECONDITION_FAILED", false); }
        server = value((body as { page_path: string }).page_path, bodies.length + 1); return structuredClone(server);
      },
    }, new Journal(), () => {});
    await store.read(target); await store.save(target, intent("b.html"));
    assert.ok(store.entry(target)?.conflict);
    paused = true; const resolution = store.resolve(target, choice);
    assert.equal(store.busy(target), true);
    const before = reads;
    await store.resolve(target, choice === "local" ? "server" : "local");
    assert.equal(reads, before, "a second choice must not enter the pending GET");
    await store.save(target, intent("c.html"));
    gate.resolve(structuredClone(server)); await resolution;
    assert.deepEqual(bodies, choice === "local" ? [intent("b.html"), intent("b.html"), intent("c.html")] : [intent("b.html"), intent("c.html")]);
    assert.equal(store.entry(target)?.queued, undefined); assert.equal(store.entry(target)?.conflict, undefined);
    assert.deepEqual(store.entry(target)?.acknowledged?.value, value("c.html", bodies.length + 1).value);
    assert.equal(store.busy(target), false);
  });
}

test("a pre-reply journal restored without an issue exposes original-request recovery for an inactive target", async () => {
  const started = deferred<void>(), pending = deferred<PersonalView>();
  const journal = new Journal(), requests: unknown[][] = [];
  const inactive: PersonalTarget = { version: "old-version", kind: "page-position", page: "inactive/한글.html" };
  const client: Pick<HaeryongClient, "personalGet" | "personalPut"> = {
    async personalGet() { return { ...value("a.html", 1), value: { position: {}, page_query: "", page_fragment: "" } }; },
    async personalPut(...args) { requests.push(structuredClone(args)); if (requests.length === 1) { started.resolve(); return pending.promise; } return value("a.html", 2); },
  };
  const first = new PersonalOperations(client, journal, () => {});
  const beforeReload = first.save(inactive, { page_query: "q=1", page_fragment: "part", position: { scroll_y: 731 } });
  await started.promise; assert.equal(journal.rows[0]!.issue, undefined); first.dispose();
  const restored = new PersonalOperations(client, journal, () => {});
  assert.equal(restored.recoverable(inactive), true); assert.equal(restored.busy(inactive), false);
  await restored.retry(inactive);
  assert.deepEqual(requests[1], requests[0]); assert.equal(restored.recoverable(inactive), false);
  pending.resolve(value("a.html", 2)); await beforeReload;
});

const absent: PersonalView = { exists: false, value: null, revision: null, etag: null, page_position_url: null };
const preference: PersonalTarget = { version: "v1", kind: "reader-preference", page: null };
const rowsOf = (raw: string | null): PersonalEntry[] => (raw === null ? [] : (JSON.parse(raw).entries as PersonalEntry[]));
const rowOf = (rows: PersonalEntry[], wanted: PersonalTarget) => rows.find((row) => JSON.stringify(row.target) === JSON.stringify(wanted));

test("another window's acknowledged save keeps this window's unknown pending record and its original key", async () => {
  const { storage, bucket } = ownerStorage("owner-f04-foreign");
  const locks = new SerialLocks();
  const journal = () => new BrowserPersonalJournal(storage, "owner-f04-foreign", locks.request.bind(locks));
  const requests: unknown[][] = [];
  const client: Pick<HaeryongClient, "personalGet" | "personalPut"> = {
    async personalGet(kind) { return kind === "reader-preference" ? absent : value("a.html", 1); },
    async personalPut(...args) { requests.push(structuredClone(args)); if (requests.length === 1) throw new NetworkError(); return value("b.html", 2); },
  };
  // The second window opens while the bucket is still empty: the first
  // window's later pending record is foreign-unknown to it.
  const firstWindow = new PersonalOperations(client, journal(), () => {});
  const secondWindow = new PersonalOperations(client, journal(), () => {});
  await firstWindow.save(target, intent("b.html"));
  assert.ok(firstWindow.entry(target)?.pending);
  await secondWindow.save(preference, { default_page: "b.html" });
  const afterForeign = rowsOf(bucket());
  assert.equal(afterForeign.length, 1, "an acknowledged foreign save deleted another window's pending record");
  assert.equal(rowOf(afterForeign, target)?.pending?.key, (requests[0] as unknown[])[5]);
  assert.deepEqual(rowOf(afterForeign, target)?.pending?.intent, intent("b.html"));
  await firstWindow.retry(target);
  assert.equal(requests.length, 3);
  assert.deepEqual(requests[2], requests[0]);
  assert.equal(rowsOf(bucket()).length, 0);
});

test("an unchanged re-proposal never absorbs a foreign version as local knowledge", async () => {
  const { storage, bucket } = ownerStorage("owner-f04-reacquire");
  const locks = new SerialLocks();
  const journal = () => new BrowserPersonalJournal(storage, "owner-f04-reacquire", locks.request.bind(locks));
  const operation = (key: string, page: string) => ({ target, key, intent: intent(page), base: value("a.html", 1), conditions: { ifMatch: '"opaque-1"' } as { ifMatch: string }, phase: "write" as const });
  const a1: PersonalEntry = { target, pending: operation("k-original", "b.html") };
  const b2: PersonalEntry = { target, pending: operation("k-foreign", "c.html") };
  const first = journal();
  first.read();
  await first.write([a1]);
  const foreign = journal();
  foreign.read();
  await foreign.write([b2]);
  // An unrelated save re-proposes A unchanged; the foreign version must stay.
  const unrelated: PersonalEntry = { target: preference, queued: { intent: { default_page: "x.html" } } };
  await first.write([a1, unrelated]);
  let rows = rowsOf(bucket());
  assert.equal(rows.length, 2);
  assert.equal(rowOf(rows, target)?.pending?.key, "k-foreign");
  assert.deepEqual(rowOf(rows, preference)?.queued?.intent, { default_page: "x.html" });
  // A later LOCAL change of the same record must collide and preserve the
  // foreign record instead of silently reacquiring it.
  const a3: PersonalEntry = { target, pending: operation("k-original", "d.html") };
  await assert.rejects(first.write([a3, unrelated]), ConcurrentJournalError);
  rows = rowsOf(bucket());
  assert.equal(rowOf(rows, target)?.pending?.key, "k-foreign");
  assert.equal(rows.length, 2);
});

test("a stale same-resource save is blocked before dispatch and keeps the original request in memory", async () => {
  const { storage, bucket } = ownerStorage("owner-f04-stale");
  const locks = new SerialLocks();
  const journal = () => new BrowserPersonalJournal(storage, "owner-f04-stale", locks.request.bind(locks));
  const requests: unknown[][] = [];
  const client: Pick<HaeryongClient, "personalGet" | "personalPut"> = {
    async personalGet(kind) { return kind === "reader-preference" ? absent : value("a.html", 1); },
    async personalPut(...args) { requests.push(structuredClone(args)); if (requests.length === 1) throw new NetworkError(); return value("b.html", 2); },
  };
  const windowA = new PersonalOperations(client, journal(), () => {});
  await windowA.save(target, intent("b.html"));
  const windowB = new PersonalOperations(client, journal(), () => {});
  await windowB.retry(target);
  assert.equal(rowsOf(bucket()).length, 0);
  // windowA still holds the old pending record; a new local intent on the
  // same resource must not dispatch over the concurrently changed history.
  await windowA.save(target, intent("d.html"));
  assert.equal(requests.length, 2, "a stale same-resource save dispatched over a foreign change");
  const entry = windowA.entry(target)!;
  assert.deepEqual(entry.pending?.intent, intent("b.html"));
  assert.equal(entry.pending?.key, (requests[0] as unknown[])[5]);
  assert.deepEqual(entry.queued?.intent, intent("d.html"));
  assert.match(entry.issue ?? "", /다른 창/);
});

test("a same-K second completion after a foreign completion reports a conservative conflict", async () => {
  const { storage, bucket } = ownerStorage("owner-f04-second-ack");
  const locks = new SerialLocks();
  const journal = () => new BrowserPersonalJournal(storage, "owner-f04-second-ack", locks.request.bind(locks));
  const requests: unknown[][] = [];
  const client: Pick<HaeryongClient, "personalGet" | "personalPut"> = {
    async personalGet(kind) { return kind === "reader-preference" ? absent : value("a.html", 1); },
    async personalPut(...args) { requests.push(structuredClone(args)); if (requests.length === 1) throw new NetworkError(); return value("b.html", 2); },
  };
  const windowA = new PersonalOperations(client, journal(), () => {});
  await windowA.save(target, intent("b.html"));
  const windowB = new PersonalOperations(client, journal(), () => {});
  await windowA.retry(target);
  assert.equal(rowsOf(bucket()).length, 0);
  // The second window replays the SAME K; its completion is conservatively
  // refused as a journal conflict (reload converges; no heuristic rewrites).
  await windowB.retry(target);
  assert.equal(requests.length, 2, "the stale same-K completion dispatched again");
  assert.match(windowB.entry(target)?.issue ?? "", /다른 창/);
  assert.equal(rowsOf(bucket()).length, 0);
});

test("an unavailable cross-window lock fails saves closed without blocking reads", async () => {
  const { storage } = ownerStorage("owner-f04-locks");
  // An explicit null models a context whose Web Locks implementation is
  // unavailable; saves must fail closed rather than fall back.
  const journal = new BrowserPersonalJournal(storage, "owner-f04-locks", null);
  await assert.rejects(journal.write([]), JournalLockUnavailableError);
  let puts = 0;
  const store = new PersonalOperations({
    async personalGet() { return value("a.html", 1); },
    async personalPut() { puts++; return value("b.html", 2); },
  }, journal, () => {});
  assert.equal((await store.read(target)).etag, '"opaque-1"');
  await store.save(target, intent("b.html"));
  assert.equal(puts, 0);
  assert.match(store.entry(target)?.issue ?? "", /동시 저장 잠금/);
});

test("authority replaced during the persistence wait never dispatches", async () => {
  const journal = new GatedJournal();
  let current = true, puts = 0;
  const store = new PersonalOperations({
    async personalGet() { return value("a.html", 1); },
    async personalPut() { puts++; return value("b.html", 2); },
  }, journal, () => { if (!current) throw new Error("replaced authority"); });
  const saving = store.save(target, intent("b.html"));
  await new Promise((resolve) => setImmediate(resolve));
  current = false;
  journal.release();
  await assert.rejects(saving, /replaced authority/);
  assert.equal(puts, 0);
  assert.ok(journal.rows[0]?.queued);
});

test("this instance's overlapping saves serialize through the lock without a false conflict", async () => {
  const { storage, bucket } = ownerStorage("owner-f04-serial");
  const locks = new SerialLocks();
  const journal = new BrowserPersonalJournal(storage, "owner-f04-serial", locks.request.bind(locks));
  const bodies: unknown[] = [];
  const store = new PersonalOperations({
    async personalGet() { return value("a.html", 1); },
    async personalPut(_kind, _version, _page, body) { bodies.push(structuredClone(body)); return value((body as { page_path: string }).page_path, bodies.length + 1); },
  }, journal, () => {});
  await Promise.all([store.save(target, intent("b.html")), store.save(target, intent("c.html"))]);
  assert.deepEqual(bodies, [intent("c.html")]);
  assert.equal(store.entry(target)?.pending, undefined);
  assert.equal(rowsOf(bucket()).length, 0);
});
