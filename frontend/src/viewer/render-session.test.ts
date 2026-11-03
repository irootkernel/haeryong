import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiError, DispatchBlockedError, NetworkError } from "../client/types.ts";
import type { RenderSessionView } from "../client/types.ts";
import { RenderSessionLease, renderDeliveryRefused } from "./render-session.ts";
const storage = () => {
  const values = new Map<string, string>();
  return { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, value); }, removeItem: (key: string) => { values.delete(key); }, values };
};
const view = (id = "delivery"): RenderSessionView => ({ version_id: "version", session_id: id, render_origin: "http://renderer.invalid", handoff: { url: "http://renderer.invalid/handoff", secret: "memory-only", expires_at: new Date(Date.now() + 60_000).toISOString() }, lease_expires_at: new Date(Date.now() + 600_000).toISOString() });
test("temporary delivery failures preserve host edits while confirmed refusal closes the Reader", () => {
  for (const error of [new NetworkError(), new DispatchBlockedError(), new Error("local handoff failed"), new ApiError(503, "UNAVAILABLE", true)]) {
    assert.equal(renderDeliveryRefused(error), false);
  }
  for (const status of [401, 403, 404, 409, 410]) {
    assert.equal(renderDeliveryRefused(new ApiError(status, "REFUSED", false)), true);
  }
});
test("an unknown open retries the exact journalled K and stores no handoff secret", async () => {
  const journal = storage(), keys: string[] = [];
  const client = {
    async renderSessionOpen(_version: string, key: string) { keys.push(key); assert.equal([...journal.values.values()][0], key); if (keys.length === 1) throw new Error("reply lost"); return view(); },
    async renderSessionRenew() {}, async renderSessionClose() {},
  };
  const lease = new RenderSessionLease({ client, version: "version", owner: "owner", context: "frame", storage: journal, guard() {}, failure() {} });
  await assert.rejects(lease.start(), /reply lost/);
  assert.equal(journal.values.size, 1);
  await lease.start();
  assert.equal(keys[0], keys[1]); assert.equal(journal.values.size, 0);
  await lease.stop();
});
test("a confirmed dead recovered delivery is closed before a new K is issued", async () => {
  const journal = storage(), keys: string[] = [], closed: string[] = [];
  const client = {
    async renderSessionOpen(_version: string, key: string) { keys.push(key); return view(keys.length === 1 ? "old" : "new"); },
    async renderSessionRenew(_version: string, id: string) { if (id === "old") throw new ApiError(404, "NOT_FOUND", false); },
    async renderSessionClose(_version: string, id: string) { closed.push(id); },
  };
  const lease = new RenderSessionLease({ client, version: "version", owner: "owner", context: "frame", storage: journal, guard() {}, failure() {} });
  assert.equal((await lease.start()).session_id, "new");
  assert.notEqual(keys[0], keys[1]); assert.deepEqual(closed, ["old"]);
  await lease.stop(); assert.deepEqual(closed, ["old", "new"]);
});
test("same-frame effect reacquisition shares an opening and does not close the live delivery", async () => {
  let complete!: (value: RenderSessionView) => void;
  const pending = new Promise<RenderSessionView>((resolve) => { complete = resolve; });
  const closed: string[] = []; let opens = 0;
  const lease = new RenderSessionLease({
    client: { async renderSessionOpen() { opens++; return pending; }, async renderSessionRenew() {}, async renderSessionClose(_version, id) { closed.push(id); } },
    version: "version", owner: "owner", context: "frame", storage: storage(), guard() {}, failure() {},
  });
  const first = lease.start(), stopping = lease.stop(), second = lease.start();
  complete(view()); await Promise.all([first, second, stopping]);
  assert.equal(opens, 1); assert.deepEqual(closed, []);
  await lease.stop(); assert.deepEqual(closed, ["delivery"]);
});

test("known delivery stop is shared and retains ownership until its close is confirmed", async () => {
  let release!: () => void, started!: () => void, closes = 0;
  const closing = new Promise<void>((resolve) => { release = resolve; });
  const dispatched = new Promise<void>((resolve) => { started = resolve; });
  const lease = new RenderSessionLease({
    client: { async renderSessionOpen() { return view("known"); }, async renderSessionRenew() {}, async renderSessionClose(_version, id) { assert.equal(id, "known"); closes++; started(); await closing; } },
    version: "version", owner: "owner", context: "frame", storage: storage(), guard() {}, failure() {},
  });
  await lease.start(); const first = lease.stop(); await dispatched; const second = lease.stop();
  assert.equal(first, second); assert.equal(closes, 1);
  release(); await Promise.all([first, second]);
  await lease.stop(); assert.equal(closes, 1);
});

test("F07: a successor never dispatches before an old held Open settles; the old reply closes nothing of the successor", async () => {
  const journal = storage();
  let releaseOld!: (value: RenderSessionView) => void;
  const heldOld = new Promise<RenderSessionView>((resolve) => { releaseOld = resolve; });
  let successorOpened = false;
  const opened: string[] = [];
  const client = {
    async renderSessionOpen(_version: string, key: string) {
      opened.push(key);
      if (opened.length === 1) return heldOld;
      successorOpened = true;
      return view("successor-s");
    },
    async renderSessionRenew() {},
    async renderSessionClose(_version: string, id: string) { closed.push(id); },
  };
  const closed: string[] = [];
  const old = new RenderSessionLease({ client, version: "version", owner: "owner", context: "frame", storage: journal, guard() {}, failure() {} });
  const oldStart = old.start();
  const stopping = old.stop();
  // The successor is a different instance of the same logical context.
  const successor = new RenderSessionLease({ client, version: "version", owner: "owner", context: "frame", storage: journal, guard() {}, failure() {} });
  const successorStart = successor.start();
  await new Promise((resolve) => setTimeout(resolve, 25));
  assert.equal(successorOpened, false, "successor dispatched Open before the old retirement settled");
  releaseOld(view("old-s"));
  await Promise.all([oldStart.catch(() => undefined), stopping]);
  const successorView = await successorStart;
  assert.equal(successorView.session_id, "successor-s");
  // The old instance's late reply settled into its retirement record: it
  // retired the journal K and closed its OWN captured S only.
  assert.deepEqual(closed, ["old-s"]);
  assert.equal(journal.values.size, 0);
  await successor.stop();
  assert.deepEqual(closed, ["old-s", "successor-s"]);
});

test("F07: a held FIRST renew (known S, journal K still stored) retires the K before Close; the successor uses a fresh K", async () => {
  const journal = storage();
  let releaseRenew!: () => void;
  const heldRenew = new Promise<void>((resolve) => { releaseRenew = resolve; });
  const opened: string[] = []; const closed: string[] = [];
  let renewHeld = false;
  const client = {
    async renderSessionOpen(_version: string, key: string) { opened.push(key); return view("old-s"); },
    async renderSessionRenew() { if (renewHeld) await heldRenew; },
    async renderSessionClose(_version: string, id: string) { closed.push(id); },
  };
  const old = new RenderSessionLease({ client, version: "version", owner: "owner", context: "frame", storage: journal, guard() {}, failure() {} });
  renewHeld = true;
  const oldStart = old.start();
  await new Promise((resolve) => setTimeout(resolve, 25));
  // #view is set (S learned) while the FIRST renew is held and the journal K
  // is still stored — the exact 131046 shape.
  const stopping = old.stop();
  const successor = new RenderSessionLease({ client, version: "version", owner: "owner", context: "frame", storage: journal, guard() {}, failure() {} });
  const successorStart = successor.start();
  await new Promise((resolve) => setTimeout(resolve, 25));
  assert.equal(opened.length, 1, "successor dispatched a fresh Open before the known-S retirement settled");
  releaseRenew();
  await Promise.all([oldStart, stopping]);
  const successorView = await successorStart;
  assert.equal(successorView.session_id, "old-s"); // fresh delivery id from its own open
  assert.notEqual(opened[1], opened[0], "successor must use a fresh K, never the retired one");
  assert.deepEqual(closed, ["old-s"]);
  await successor.stop();
  assert.deepEqual(closed, ["old-s", "old-s"]);
});

test("F07: unknown Open failure keeps the original K and dispatches no Close; the successor replays the same K", async () => {
  const journal = storage();
  const opened: string[] = []; const closed: string[] = [];
  const client = {
    async renderSessionOpen(_version: string, key: string) { opened.push(key); if (opened.length === 1) throw new Error("network lost"); return view("replayed-s"); },
    async renderSessionRenew() {},
    async renderSessionClose(_version: string, id: string) { closed.push(id); },
  };
  const old = new RenderSessionLease({ client, version: "version", owner: "owner", context: "frame", storage: journal, guard() {}, failure() {} });
  await assert.rejects(old.start(), /network lost/);
  await old.stop();
  assert.deepEqual(closed, [], "no Close is dispatched for an unknown Open with no S learned");
  const successor = new RenderSessionLease({ client, version: "version", owner: "owner", context: "frame", storage: journal, guard() {}, failure() {} });
  const successorView = await successor.start();
  assert.equal(opened[1], opened[0], "the successor must replay the original K");
  assert.equal(successorView.session_id, "replayed-s");
  await successor.stop();
});

test("F07: a settled Close failure rejects visibly and retries the same S without a new K", async () => {
  const journal = storage();
  let closeAttempts = 0; const notices: { message: string; retry?: () => void }[] = [];
  const client = {
    async renderSessionOpen() { return view("failing-s"); },
    async renderSessionRenew() {},
    async renderSessionClose() { closeAttempts++; if (closeAttempts === 1) throw new Error("transport lost"); },
  };
  const lease = new RenderSessionLease({ client, version: "version", owner: "owner", context: "frame", storage: journal, guard() {}, failure() {}, notice: (n) => notices.push(n) });
  await lease.start();
  await assert.rejects(lease.stop(), /transport lost/);
  assert.equal(journal.values.size, 0, "K must be durably retired before a possibly committed Close");
  await lease.stop();
  assert.equal(closeAttempts, 2);
});

test("F07: different logical contexts never wait on each other", async () => {
  let release!: (value: RenderSessionView) => void;
  const held = new Promise<RenderSessionView>((resolve) => { release = resolve; });
  const journalA = storage(); const journalB = storage();
  // Hold EXACTLY context A's known key. A suffix fixture would also capture
  // a random context-B key that happens to end in the same hex digit (1/16),
  // deadlocking B on A's barrier before the test releases it.
  const keyA = "00000000-0000-4000-8000-00000000000a";
  const client = {
    async renderSessionOpen(_version: string, key: string) { if (key === keyA) return held; return view("b-s"); },
    async renderSessionRenew() {}, async renderSessionClose() {},
  };
  const leaseA = new RenderSessionLease({ client, version: "version", owner: "owner", context: "a", storage: journalA, guard() {}, failure() {} });
  // Force a distinguishable K per context through the journal.
  journalA.values.set("haeryong.reader.open.v1:" + JSON.stringify(["owner", "version", "a"]), keyA);
  const startA = leaseA.start();
  const stoppingA = leaseA.stop();
  const leaseB = new RenderSessionLease({ client, version: "version", owner: "owner", context: "b", storage: journalB, guard() {}, failure() {} });
  try {
    const viewB = await leaseB.start();
    assert.equal(viewB.session_id, "b-s");
    release(view("a-s"));
  } finally {
    // Release every held barrier and stop both leases even when an assertion
    // fails: a failed assertion must never leave a hanging five-minute run.
    release(view("a-s"));
    await startA.catch(() => undefined);
    await stoppingA.catch(() => undefined);
    await leaseB.stop().catch(() => undefined);
  }
});

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const turn = () => new Promise<void>((resolve) => setImmediate(resolve));

test("a canceled waiting B cannot replace A's barrier or dispatch before C", async () => {
  const journal = storage(), pending = deferred<RenderSessionView>();
  const keys: string[] = [], closed: string[] = [];
  const client = {
    async renderSessionOpen(_v: string, key: string) { keys.push(key); return keys.length === 1 ? pending.promise : view("C"); },
    async renderSessionRenew() {}, async renderSessionClose(_v: string, id: string) { closed.push(id); },
  };
  const options = { client, version: "version", owner: crypto.randomUUID(), context: "chain", storage: journal, guard() {}, failure() {} };
  const a = new RenderSessionLease(options), aStart = a.start(), aStop = a.stop();
  const b = new RenderSessionLease(options), bStart = b.start();
  const canceled = assert.rejects(bStart, /이전 읽기/);
  await b.stop(); await canceled;
  const c = new RenderSessionLease(options), cStart = c.start();
  await turn(); assert.equal(keys.length, 1); assert.equal(journal.values.size, 1);
  pending.resolve(view("A")); await Promise.all([aStart, aStop]);
  assert.equal((await cStart).session_id, "C"); assert.deepEqual(closed, ["A"]);
  assert.notEqual(keys[0], keys[1]); await c.stop();
});

test("first Renew rejection after retirement still closes the learned S, never replays its K", async () => {
  const journal = storage(), renew = deferred<void>(), entered = deferred<void>();
  const keys: string[] = [], closed: string[] = [];
  const client = {
    async renderSessionOpen(_v: string, key: string) { keys.push(key); return view(keys.length === 1 ? "A" : "B"); },
    async renderSessionRenew(_v: string, id: string) { if (id === "A") { entered.resolve(); await renew.promise; } },
    async renderSessionClose(_v: string, id: string) { assert.equal(journal.values.size, 0); closed.push(id); },
  };
  const options = { client, version: "version", owner: crypto.randomUUID(), context: "renew-failure", storage: journal, guard() {}, failure() {} };
  const a = new RenderSessionLease(options), failed = assert.rejects(a.start(), /renew failed/);
  await entered.promise; const stopped = a.stop();
  const b = new RenderSessionLease(options), next = b.start();
  await turn(); assert.equal(keys.length, 1);
  renew.reject(new Error("renew failed")); await Promise.all([failed, stopped]);
  assert.equal((await next).session_id, "B"); assert.deepEqual(closed, ["A"]); assert.notEqual(keys[0], keys[1]);
  await b.stop();
});

test("known-S journal failure sends zero Close and remains retryable by a successor", async () => {
  const journal = storage(); let deny = true, opens = 0, closes = 0;
  const store = { ...journal, removeItem(key: string) { if (deny) throw new Error("disk denied"); journal.removeItem(key); } };
  const client = {
    async renderSessionOpen() { return view(++opens === 1 ? "A" : "B"); },
    async renderSessionRenew(_v: string, id: string) { if (id === "A") throw new Error("first renew failed"); },
    async renderSessionClose() { closes++; },
  };
  const options = { client, version: "version", owner: crypto.randomUUID(), context: "storage", storage: store, guard() {}, failure() {} };
  const a = new RenderSessionLease(options); await assert.rejects(a.start(), /first renew/);
  await assert.rejects(a.stop(), /disk denied/); assert.equal(closes, 0); assert.equal(journal.values.size, 1);
  const b = new RenderSessionLease(options); await assert.rejects(b.start(), /disk denied/); assert.equal(opens, 1); assert.equal(closes, 0);
  deny = false; assert.equal((await b.start()).session_id, "B"); assert.equal(closes, 1); await b.stop();
});

test("same instance after irreversible K retirement waits for Close and opens a fresh S", async () => {
  const journal = storage(), close = deferred<void>(), entered = deferred<void>(); let opens = 0;
  const keys: string[] = [], closed: string[] = [];
  const client = {
    async renderSessionOpen(_v: string, key: string) { keys.push(key); return view(++opens === 1 ? "A" : "B"); },
    async renderSessionRenew() {},
    async renderSessionClose(_v: string, id: string) { closed.push(id); if (id === "A") { entered.resolve(); await close.promise; } },
  };
  const lease = new RenderSessionLease({ client, version: "version", owner: crypto.randomUUID(), context: "irreversible", storage: journal, guard() {}, failure() {} });
  await lease.start(); const stopping = lease.stop(); await entered.promise;
  const next = lease.start(); await turn(); assert.equal(opens, 1); assert.equal(journal.values.size, 0);
  close.resolve(); await stopping; assert.equal((await next).session_id, "B"); assert.notEqual(keys[0], keys[1]);
  await lease.stop(); assert.deepEqual(closed, ["A", "B"]);
});

test("same-owner authority replacement can safely leave an old Close unconfirmed after K retirement", async () => {
  const journal = storage(), close = deferred<void>(), entered = deferred<void>(); let oldValid = true, opens = 0;
  const keys: string[] = [], closed: string[] = [];
  const client = {
    async renderSessionOpen(_v: string, key: string) { keys.push(key); return view(++opens === 1 ? "A" : "B"); },
    async renderSessionRenew() {},
    async renderSessionClose(_v: string, id: string) { if (id === "A") { entered.resolve(); await close.promise; } closed.push(id); },
  };
  const options = { client, version: "version", owner: crypto.randomUUID(), context: "authority", storage: journal, failure() {} };
  const a = new RenderSessionLease({ ...options, guard() { if (!oldValid) throw new Error("old authority"); } });
  await a.start(); const stopping = a.stop(); await entered.promise; oldValid = false;
  const b = new RenderSessionLease({ ...options, guard() {} });
  assert.equal((await b.start()).session_id, "B"); assert.deepEqual(closed, []); assert.notEqual(keys[0], keys[1]);
  close.resolve(); await stopping; assert.deepEqual(closed, ["A"]); await b.stop();
});

for (const [status, code] of [[401, "SESSION_REVOKED"], [403, "FORBIDDEN"], [409, "INVALID_TRANSITION"]] as const) {
  test(`Close ${status}/${code} is not unavailable proof; a new instance retries the captured S`, async () => {
    const journal = storage(); let blocked = true, opens = 0; const closed: string[] = [];
    const client = {
      async renderSessionOpen() { return view(++opens === 1 ? "A" : "B"); },
      async renderSessionRenew() {}, async renderSessionClose(_v: string, id: string) { closed.push(id); if (blocked) throw new ApiError(status, code, false); },
    };
    const options = { client, version: "version", owner: crypto.randomUUID(), context: "status", storage: journal, guard() {}, failure() {} };
    const a = new RenderSessionLease(options); await a.start(); await assert.rejects(a.stop(), (e) => e instanceof ApiError && e.status === status);
    const b = new RenderSessionLease(options); await assert.rejects(b.start(), (e) => e instanceof ApiError && e.status === status);
    assert.equal(opens, 1); blocked = false; assert.equal((await b.start()).session_id, "B"); assert.deepEqual(closed, ["A", "A", "A"]); await b.stop();
  });
}

test("lifecycle: a readable document cannot revive its refused existing delivery", async () => {
  const journal = storage(); let opens = 0, renews = 0, revoked = false;
  const failures: unknown[] = [];
  const client = {
    async renderSessionOpen() { opens++; return view("before-trash"); },
    async renderSessionRenew(_v: string, id: string) {
      renews++; assert.equal(id, "before-trash");
      if (revoked) throw new ApiError(404, "NOT_FOUND", false);
    },
    async renderSessionClose() {},
  };
  const lease = new RenderSessionLease({client, version:"version", owner:crypto.randomUUID(), context:"fast-restore", storage:journal, guard(){}, failure:e=>failures.push(e)});
  try {
    await lease.start();
    // Another session has already completed trash -> restore. Version GET is
    // readable again, but Renew must still check the original delivery S.
    revoked = true;
    await assert.rejects(lease.revalidate(), (e:unknown)=>e instanceof ApiError && e.status===404);
    await lease.revalidate();
    assert.equal(failures.length,1); assert.equal(opens,1); assert.equal(renews,2);
  } finally { await lease.stop(); }
});

test("lifecycle: reconnect and resync share one existing-delivery probe and never Open", async () => {
  const journal = storage(), pending = deferred<void>(); let opens=0, renews=0, failures=0;
  const client = {
    async renderSessionOpen() { opens++; return view("missed-trash"); },
    async renderSessionRenew() { if(++renews>1)await pending.promise; }, async renderSessionClose(){},
  };
  const lease=new RenderSessionLease({client,version:"version",owner:crypto.randomUUID(),context:"resync",storage:journal,guard(){},failure(){failures++;}});
  try {
    await lease.start();
    const reconnect=lease.revalidate(),resync=lease.revalidate();
    assert.equal(reconnect,resync);assert.equal(renews,2);
    const denied=assert.rejects(reconnect,(e:unknown)=>e instanceof ApiError && e.status===410);
    pending.reject(new ApiError(410,"INVALID_TRANSITION",false,0,{reason:"render_session_stale"}));await denied;
    await lease.revalidate();assert.equal(failures,1);assert.equal(opens,1);
  } finally { await lease.stop(); }
});

test("lifecycle: scheduled renewal refusal is terminal until explicit navigation", {timeout:5000}, async () => {
  let opens=0,renews=0,failed!:()=>void;
  const failure=new Promise<void>(resolve=>{failed=resolve;});
  const client={
    async renderSessionOpen(){opens++;return {...view("lease-refused"),lease_expires_at:new Date(Date.now()+90).toISOString()};},
    async renderSessionRenew(){if(++renews>1)throw new ApiError(403,"FORBIDDEN",false);},async renderSessionClose(){},
  };
  const lease=new RenderSessionLease({client,version:"version",owner:crypto.randomUUID(),context:"lease",storage:storage(),guard(){},failure:failed});
  try {await lease.start();await failure;await lease.revalidate();assert.equal(opens,1);assert.equal(renews,2);}finally{await lease.stop();}
});
