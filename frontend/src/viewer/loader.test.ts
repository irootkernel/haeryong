// Node exercises the actual loader module and real MessagePorts with explicit
// DOM seams. This is protocol-unit evidence; browser/Wails proof is separate.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";

type Message = Record<string, unknown> & { payload: Record<string, unknown> };
const pause = () => new Promise((resolve) => setTimeout(resolve, 5));
async function until(check: () => boolean) { for (let i = 0; i < 1000; i++) { if (check()) return; await pause(); } assert.fail("loader protocol did not settle"); }

// Region-focus seam: fake in-page elements so the loader's honest
// found/focused result is observable without a real DOM.
const fakeRegions = new Map<string, { hasAttribute(): boolean; setAttribute(): void; focus(): void; scrollIntoView(): void }>();

async function page() {
  const handlers = new Map<string, ((event: unknown) => void)[]>();
  const announcements: Record<string, unknown>[] = [];
  const parent = { postMessage(message: Record<string, unknown>) { announcements.push(message); } };
  const document = {
    currentScript: { dataset: { haeryongDocument: "document", haeryongVersion: "version", haeryongPage: encodeURI("한글.html") } },
    readyState: "complete" as const,
    activeElement: null as unknown,
    getElementById(id: string) { return fakeRegions.get(id) ?? null; },
  };
  const replacements: Record<string, unknown> = {
    window: globalThis, parent, scrollX: 0, scrollY: 0,
    location: { search: "?scenario=%23", hash: "#한글", pathname: "/" + encodeURIComponent("한글.html"), origin: "http://renderer.invalid", replace() {} },
    history: { pushState() {}, replaceState() {} },
    document,
    addEventListener(name: string, handler: (event: unknown) => void) { const list = handlers.get(name) ?? []; list.push(handler); handlers.set(name, list); },
  };
  const originals = new Map<string, PropertyDescriptor | undefined>();
  for (const name of [...Object.keys(replacements), "__haeryong"]) originals.set(name, Object.getOwnPropertyDescriptor(globalThis, name));
  for (const [name, value] of Object.entries(replacements)) Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
  await import("./loader.ts?case=" + crypto.randomUUID());
  const announcement = announcements[0]!;
  assert.match(String(announcement.render_instance_id), /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
  const binding = { document_id: announcement.document_id, version_id: announcement.version_id, page_path: announcement.page_path, render_instance_id: announcement.render_instance_id };
  const messages: Message[] = [];
  const channel = new MessageChannel();
  channel.port1.onmessage = ({ data }) => { messages.push(data as Message); };
  channel.port1.start();
  const connect = () => handlers.get("message")![0]!({ source: parent, origin: "http://trusted.invalid", data: { type: "host.connect", announcement_id: announcement.announcement_id, binding }, ports: [channel.port2] });
  let sequence = 0;
  const send = (type: string, payload: Record<string, unknown>, overrides: Record<string, unknown> = {}) => channel.port1.postMessage({ protocol_version: "1.0", ...binding, sequence: ++sequence, type, payload, ...overrides });
  const api = (globalThis as typeof globalThis & { __haeryong: { state(value: unknown): Promise<void>; sendContext(value: unknown): Promise<Record<string, unknown>> } }).__haeryong;
  return { handlers, announcements, messages, connect, send, api, close() {
    for (const handler of handlers.get("pagehide") ?? []) handler({});
    channel.port1.close(); channel.port2.close();
    for (const [name, descriptor] of originals) { if (descriptor) Object.defineProperty(globalThis, name, descriptor); else Reflect.deleteProperty(globalThis, name); }
  } };
}

test("the actual loader snapshots large state, obeys credit, retries the same transfer and validates final readiness", async () => {
  const p = await page();
  try {
    p.handlers.get("error")![0]!({ message: "Initial authored script failed" });
    p.connect(); await until(() => p.messages.length === 2);
    assert.equal(p.messages[0]!.type, "viewer.ready"); assert.equal(p.messages[1]!.payload.message, "Initial authored script failed");
    const value = { text: "🌊한글".repeat(2_000_000) };
    const expected = createHash("sha256").update(JSON.stringify(value)).digest("hex");
    const work = p.api.state(value); value.text = "changed live state";
    let cursor = 2, offset = 0, transferId = "", begins = 0, interrupted = false, ended = false;
    const hash = createHash("sha256");
    for (let steps = 0; !ended && steps < 10000; steps++) {
      await until(() => p.messages.length > cursor);
      const message = p.messages[cursor++]!, payload = message.payload;
      if (message.type === "context.transfer.begin") {
        begins++;
        if (!transferId) transferId = String(payload.transfer_id);
        assert.equal(payload.transfer_id, transferId);
        // A wrong binding and impossible credit must not advance hostSequence.
        p.send("context.transfer.ack", { transfer_id: transferId, next_offset: offset, credit_bytes: 8192 }, { version_id: "foreign-version", sequence: 90000 });
        p.send("context.transfer.ack", { transfer_id: transferId, next_offset: offset, credit_bytes: 0 });
        p.send("context.transfer.ack", { transfer_id: transferId, next_offset: offset, credit_bytes: 8192 });
      } else if (message.type === "context.transfer.chunk") {
        assert.equal(payload.transfer_id, transferId); assert.equal(payload.offset, offset);
        assert.ok(payload.bytes instanceof ArrayBuffer);
        const bytes = new Uint8Array(payload.bytes); assert.ok(bytes.length <= 8192);
        assert.equal(payload.bytes_sha256, createHash("sha256").update(bytes).digest("hex"));
        hash.update(bytes); offset += bytes.length;
        if (!interrupted) { interrupted = true; p.send("context.transfer.error", { transfer_id: transferId, code: "TRANSIENT_BACKEND", retryable: true }); }
        else p.send("context.transfer.ack", { transfer_id: transferId, next_offset: offset, credit_bytes: 8192 });
      } else if (message.type === "context.transfer.end") {
        assert.equal(payload.bytes_sha256, expected); assert.equal(hash.digest("hex"), expected);
        p.send("context.transfer.ready", { transfer_id: transferId, context_ref: "wrong-digest", bytes_sha256: "0".repeat(64) });
        p.send("context.transfer.ready", { transfer_id: transferId, context_ref: "actual-sealed-ref", bytes_sha256: expected });
        ended = true;
      } else assert.fail("Unexpected loader transfer message " + message.type);
    }
    assert.equal(ended, true); assert.equal(begins, 2); assert.ok(offset > 17_825_760);
    await work; await until(() => p.messages.length > cursor);
    assert.equal(p.messages[cursor]!.type, "state.changed"); assert.equal(p.messages[cursor]!.payload.context_ref, "actual-sealed-ref");
    assert.equal(p.messages[cursor]!.payload.page_query, "scenario=%23");
  } finally { p.close(); }
});

test("page disposal rejects pending state and prevents its scheduled retry from reaching a new channel", async () => {
  const p = await page();
  try {
    p.connect(); await until(() => p.messages.length === 1);
    const work = p.api.sendContext({ text: "retained until page loss" });
    await until(() => p.messages.length === 2);
    const id = p.messages[1]!.payload.transfer_id;
    p.send("context.transfer.error", { transfer_id: id, code: "TRANSIENT_BACKEND", retryable: true });
    await pause();
    const rejected = assert.rejects(work, /interrupted/);
    p.handlers.get("pagehide")![0]!({}); await rejected;
    const count = p.messages.length;
    await new Promise((resolve) => setTimeout(resolve, 300));
    assert.equal(p.messages.length, count);
  } finally { p.close(); }
});

test("malformed v2 page metadata fails the integration bootstrap without guessing raw", async () => {
  const originals = new Map<string, PropertyDescriptor | undefined>();
  const received: Record<string, unknown>[] = [];
  const parent = { postMessage(message: Record<string, unknown>) { received.push(message); } };
  for (const name of ["window", "open", "parent", "scrollX", "scrollY", "location", "document", "addEventListener", "__haeryong"]) originals.set(name, Object.getOwnPropertyDescriptor(globalThis, name));
  const handlers = new Map<string, ((event: unknown) => void)[]>();
  for (const [name, value] of Object.entries({ window: globalThis, parent, scrollX: 0, scrollY: 0, location: { search: "", hash: "", pathname: "/test.html", origin: "http://renderer.invalid" }, addEventListener(name: string, handler: (event: unknown) => void) { const list = handlers.get(name) ?? []; list.push(handler); handlers.set(name, list); } })) Object.defineProperty(globalThis, name, { value, configurable: true, writable: true });
  for (const bad of ["%", "a%2", "%FF", "%zz"]) {
    received.length = 0;
    Object.defineProperty(globalThis, "document", { value: { currentScript: { dataset: { haeryongDocument: "d", haeryongVersion: "v", haeryongPage: bad } }, readyState: "complete", getElementById() { return null; } }, configurable: true, writable: true });
    await import("./loader.ts?malformed=" + encodeURIComponent(bad) + "&" + crypto.randomUUID());
    assert.equal(received.filter((m) => m.type === "viewer.loaded").length, 0, "malformed metadata must not announce: " + bad);
  }
  // Valid U+FFFD is a filename character, not malformed UTF-8. Percent
  // literals, C1 controls and URI-looking text each decode exactly once.
  for (const name of ["%FF", "\\uFFFD.html", "\\u0080.html", "€.html", "a+%2F\\\\~&copy.html", "a\\r\\nb.html"]) {
    received.length = 0;
    Object.defineProperty(globalThis, "document", { value: { currentScript: { dataset: { haeryongDocument: "d", haeryongVersion: "v", haeryongPage: encodeURIComponent(name) } }, readyState: "complete", getElementById() { return null; } }, configurable: true, writable: true });
    await import("./loader.ts?literal=" + encodeURIComponent(name) + "&" + crypto.randomUUID());
    const loaded = received.find((m) => m.type === "viewer.loaded") as { page_path?: string } | undefined;
    assert.ok(loaded, "legitimate encoded path must announce");
    assert.equal(loaded.page_path, name);
  }
  for (const [name, descriptor] of originals) { if (descriptor) Object.defineProperty(globalThis, name, descriptor); else Reflect.deleteProperty(globalThis, name); }
});


test("region.focus answers with the observed result: missing element, focused element and modal-blocked element", async () => {
  const p = await page();
  try {
    p.connect(); await until(() => p.messages.some((m) => m.type === "viewer.ready"));
    const acks = () => p.messages.filter((m) => m.type === "region.focused");
    p.send("region.focus", { region_id: "missing" });
    await until(() => acks().length === 1);
    assert.deepEqual({ ...acks()[0]!.payload }, { region_id: "missing", found: false, focused: false });
    const doc = (globalThis as { document: { activeElement: unknown } }).document;
    const region: { hasAttribute(): boolean; setAttribute(): void; focus(): void; scrollIntoView(): void } = {
      hasAttribute: () => false, setAttribute() {}, scrollIntoView() {}, focus() { doc.activeElement = region; },
    };
    fakeRegions.set("overview", region);
    p.send("region.focus", { region_id: "overview" });
    await until(() => acks().length === 2);
    assert.equal(acks()[1]!.payload.found, true);
    assert.equal(acks()[1]!.payload.focused, true);
    const blocked: typeof region = { hasAttribute: () => false, setAttribute() {}, scrollIntoView() {}, focus() {} };
    fakeRegions.set("blocked", blocked);
    p.send("region.focus", { region_id: "blocked" });
    await until(() => acks().length === 3);
    assert.equal(acks()[2]!.payload.found, true);
    assert.equal(acks()[2]!.payload.focused, false);
  } finally { fakeRegions.clear(); p.close(); }
});
