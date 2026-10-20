import { test } from "node:test";
import assert from "node:assert/strict";
import { ViewerChannel, type DisplayContext, type PageBinding } from "./channel.ts";
import type { ContextTransferHost } from "./context-transfer.ts";

const page = { document_id: "document", version_id: "version", page_path: "pages/한글.html" };
const origin = "http://127.0.0.1:9281";
const wait = () => new Promise((resolve) => setTimeout(resolve, 5));
async function until(check: () => boolean) { for (let i = 0; i < 200; i++) { if (check()) return; await wait(); } assert.fail("page channel did not settle"); }

function fixture(verify: (ref: string) => Promise<boolean> = async () => false) {
  let child: MessagePort | null = null;
  let active = true, ready = 0, transfers = 0;
  const display: DisplayContext[] = [], errors: string[] = [];
  const connections: unknown[] = [];
  const frame = { postMessage(message: unknown, target: string, ports: MessagePort[]) {
    assert.equal(target, origin); connections.push(message); child = ports[0]!; child.start();
  } } as unknown as Window;
  const host = new ViewerChannel({ frame, origin, page, guard() { if (!active) throw new Error("authority replaced"); }, contexts(_binding, guard) {
    return { async acceptsEcho(ref: string) { return verify(ref); }, async handle() { guard(); transfers++; return { type: "context.transfer.ack", payload: { transfer_id: "transfer", next_offset: 0, credit_bytes: 65536 } }; } } as unknown as ContextTransferHost;
  }, ready() { ready++; }, display(_type, payload) { display.push(payload); }, error(code) { errors.push(code); } });
  const binding: PageBinding = { ...page, render_instance_id: crypto.randomUUID() };
  const announcement = { type: "viewer.loaded", announcement_id: crypto.randomUUID(), ...binding };
  const connect = () => host.accept({ source: frame, origin, data: announcement });
  let sequence = 0;
  const send = (type: string, payload: DisplayContext = {}, override: DisplayContext = {}) => child!.postMessage({ protocol_version: "1.0", ...binding, sequence: ++sequence, type, payload, ...override });
  return { host, frame, binding, announcement, connections, display, errors, connect, send, counts: () => ({ ready, transfers }), deactivate() { active = false; }, close() { host.dispose(); child?.close(); } };
}

test("window handshake requires the exact frame, origin and immutable page before assigning a private port", async () => {
  const f = fixture();
  try {
    assert.equal(f.host.accept({ source: {} as Window, origin, data: f.announcement }), false);
    assert.equal(f.host.accept({ source: f.frame, origin: "http://127.0.0.1:9282", data: f.announcement }), false);
    assert.equal(f.host.accept({ source: f.frame, origin, data: { ...f.announcement, page_path: "different.html" } }), false);
    assert.equal(f.connections.length, 0);
    assert.equal(f.connect(), true); assert.equal(f.connect(), true); assert.equal(f.connections.length, 1);
    f.send("viewer.ready", { profile: "html_js_v1" }); await until(() => f.counts().ready === 1);
    f.send("state.changed", { page_query: "q=%23", page_fragment: "region" }); await until(() => f.display.length === 1);
    assert.equal(f.display[0]!.page_query, "q=%23");
  } finally { f.close(); }
});

test("region.focused carries only the typed honest result and never a dispatch ack or upgrade", async () => {
  const f = fixture();
  try {
    f.connect();
    f.send("viewer.ready", { profile: "html_js_v1" }); await until(() => f.counts().ready === 1);
    f.send("region.focused", { region_id: "overview", found: true, focused: false });
    await until(() => f.display.length === 1);
    assert.equal(f.display[0]!.region_id, "overview");
    assert.equal(f.display[0]!.found, true);
    assert.equal(f.display[0]!.focused, false);
    // Missing region_id is not a typed region message and is ignored.
    f.send("region.focused", { found: true, focused: true });
    await wait();
    assert.equal(f.display.length, 1);
    // A disposed channel's port is closed: even a well-formed stale ack from
    // the old page cannot reach display after disposal.
    f.host.dispose();
    f.send("region.focused", { region_id: "stale", found: true, focused: true });
    await wait();
    assert.equal(f.display.length, 1);
  } finally { f.close(); }
});

test("forged management calls, buffers, wrong binding and stale sequence cannot update display or dispatch staging", async () => {
  const f = fixture();
  try {
    f.connect(); f.send("context.transfer.begin", { transfer_id: "transfer", media_type: "application/json" });
    f.send("viewer.ready", { profile: "html_js_v1" }); await until(() => f.counts().ready === 1);
    for (const type of ["review.complete", "delete", "policy.set", "fetch", "native.call"]) f.send(type, { target: "/api/v1/documents/document" });
    f.send("state.changed", { state: new ArrayBuffer(8) });
    f.send("state.changed", {}, { document_id: "another-document" });
    f.send("state.changed", {}, { render_instance_id: crypto.randomUUID() });
    f.send("state.changed", {}, { sequence: 1 });
    f.send("state.changed", { context_ref: "foreign-ref" });
    await wait(); await wait();
    assert.equal(f.display.length, 0); assert.equal(f.counts().transfers, 0);
    const state = Object.fromEntries(Array.from({ length: 1000 }, (_, i) => [`field${i}`, "한글".repeat(100)]));
    f.send("state.changed", { state }); await until(() => f.display.length === 1);
    assert.deepEqual(f.display[0]!.state, state);
    f.send("context.transfer.begin", { transfer_id: "transfer", media_type: "application/json" }); await until(() => f.counts().transfers === 1);
    f.host.dispose(); f.send("context.transfer.begin", { transfer_id: "transfer", media_type: "application/json" }); await wait();
    assert.equal(f.counts().transfers, 1);
  } finally { f.close(); }
});

test("late verified echoes cannot replace newer display state or survive channel disposal", async () => {
  let release: ((accepted: boolean) => void) | undefined;
  const f = fixture(() => new Promise<boolean>((resolve) => { release = resolve; }));
  try {
    f.connect(); f.send("viewer.ready", { profile: "html_js_v1" }); await until(() => f.counts().ready === 1);
    f.send("state.changed", { context_ref: "old-ref" }); await until(() => !!release);
    f.send("state.changed", { state: { latest: true } }); await until(() => f.display.length === 1);
    release!(true); await wait(); assert.equal(f.display.length, 1);
    release = undefined;
    f.send("state.changed", { context_ref: "other-ref" }); await until(() => !!release);
    f.host.dispose(); release!(true); await wait(); assert.equal(f.display.length, 1);
  } finally { f.close(); }
});
