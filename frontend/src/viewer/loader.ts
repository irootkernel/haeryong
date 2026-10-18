// Automatic artifact integration. This bundle has no host client, bearer,
// storage access or native bridge. Only a parent-bound MessagePort is exposed.
import { sha256 } from "@noble/hashes/sha2.js";
import { SnapshotCursor, hex } from "./snapshot.ts";

const script = document.currentScript as HTMLScriptElement | null;
// The Go tag encodes the exact page identity with url.PathEscape plus the
// literal '~' -> '%7E' post-step; reverse it with exactly one
// decodeURIComponent call (never plus-to-space, never double decoding).
// There is exactly one encoding: malformed percent escapes or invalid UTF-8
// in generated metadata cannot be this contract, so the integration bootstrap
// fails honestly (no viewer.loaded, no ready, no context dispatch) while the
// authored page itself still renders normally.
let identityPage: string | null = null;
try {
  identityPage = decodeURIComponent(script?.dataset.haeryongPage ?? "");
} catch {
  identityPage = null;
}
const identity = identityPage === null
  ? null
  : {
      document_id: script?.dataset.haeryongDocument ?? "",
      version_id: script?.dataset.haeryongVersion ?? "",
      page_path: identityPage,
    };
const PROTOCOL = "1.0";
let announcement = crypto.randomUUID();
let renderInstance = crypto.randomUUID();
let port: MessagePort | null = null;
let parentOrigin: string | null = null;
let sequence = 0, hostSequence = 0, generation = 0;
type Payload = Record<string, unknown>;
let latestError: Payload | null = null;
type Transfer = { id: string; cursor: SnapshotCursor; maxSent: number; resolve: (value: Payload) => void; reject: (error: Error) => void; completed: boolean; retries: number; retryTimer: ReturnType<typeof setTimeout> | null };
const transfers = new Map<string, Transfer>();
const binding = () => ({ ...identity, render_instance_id: renderInstance });
const object = (value: unknown): value is Payload => !!value && typeof value === "object" && !Array.isArray(value);

function send(type: string, payload: Payload): void {
  if (!port) throw new Error("Page channel interrupted; staged work is retained");
  const message = { protocol_version: PROTOCOL, ...binding(), sequence: ++sequence, type, payload };
  port.postMessage(message, type === "context.transfer.chunk" && payload.bytes instanceof ArrayBuffer ? [payload.bytes] : []);
}

function announce(): void {
  if (parent === window || identity === null) return;
  parent.postMessage({ type: "viewer.loaded", announcement_id: announcement, ...binding(), location: locationState() }, "*");
}

function locationState(): Payload {
  let page_path: string | undefined;
  try { page_path = decodeURIComponent(location.pathname.slice(1)); } catch { page_path = location.pathname.slice(1); }
  return { page_query: location.search.slice(1), page_fragment: location.hash.slice(1), scroll_x: scrollX, scroll_y: scrollY, page_path: identity?.page_path ?? "", navigation_path: page_path };
}

function dispose(): void {
  generation++;
  port?.close(); port = null; sequence = 0; hostSequence = 0;
  for (const transfer of transfers.values()) {
    if (transfer.retryTimer !== null) clearTimeout(transfer.retryTimer);
    transfer.reject(new Error("Page channel interrupted; incomplete staging was not abandoned"));
  }
  transfers.clear();
}

function begin(transfer: Transfer): void {
  send("context.transfer.begin", { transfer_id: transfer.id, media_type: "application/json" });
}

let logicalRouteTemplate: string | null = null;
let userScrolled = false;
const appliedCorrelations = new Map<string, Payload>();
const applied = (correlation: string, result: Payload) => { appliedCorrelations.set(correlation, result); send("reader.position.applied", { correlation_id: correlation, ...result }); };

function reply(data: unknown): void {
  if (!object(data) || data.protocol_version !== PROTOCOL || !Object.entries(binding()).every(([key, value]) => data[key] === value) || !Number.isSafeInteger(data.sequence) || (data.sequence as number) <= hostSequence || !object(data.payload)) return;
  const payload = data.payload;
  if (data.type === "region.focus" && typeof payload.region_id === "string") {
    hostSequence = data.sequence as number;
    document.getElementById(payload.region_id)?.scrollIntoView(); return;
  }
  if (data.type === "reader.logical_route" && typeof payload.template === "string") {
    hostSequence = data.sequence as number;
    logicalRouteTemplate = payload.template; return;
  }
  if (data.type === "reader.position.apply" && typeof payload.correlation_id === "string" && payload.correlation_id) {
    hostSequence = data.sequence as number;
    const correlation = payload.correlation_id;
    if (appliedCorrelations.has(correlation)) {
      send("reader.position.applied", { correlation_id: correlation, ...appliedCorrelations.get(correlation) }); return;
    }
    if (userScrolled) {
      // Live user intent wins over a late restore; data is never lost.
      applied(correlation, { applied: "none", reason: "user_scrolled" }); return;
    }
    const position = object(payload.position) ? payload.position : {};
    const fragment = typeof payload.fragment === "string" ? payload.fragment : null;
    if (fragment !== null && fragment) {
      const anchor = document.getElementById(fragment);
      if (anchor) { anchor.scrollIntoView(); applied(correlation, { applied: "fragment" }); return; }
    }
    const x = Number(position.scroll_x), y = Number(position.scroll_y);
    if (Number.isFinite(x) && Number.isFinite(y)) {
      scrollTo(x, y);
      applied(correlation, { applied: "scroll" }); return;
    }
    applied(correlation, { applied: "none", reason: "invalid_position" }); return;
  }
  if (data.type === "reader.position.request" && typeof payload.correlation_id === "string" && payload.correlation_id) {
    hostSequence = data.sequence as number;
    send("reader.position.snapshot", { correlation_id: payload.correlation_id, position: { scroll_x: scrollX, scroll_y: scrollY, scroll_height: document.documentElement.scrollHeight, client_height: document.documentElement.clientHeight } }); return;
  }
  if (typeof payload.transfer_id !== "string") return;
  const transfer = transfers.get(payload.transfer_id);
  if (!transfer) return;
  if (data.type === "context.transfer.ack") {
    const offset = payload.next_offset, credit = payload.credit_bytes;
    if (!Number.isSafeInteger(offset) || (offset as number) < 0 || (offset as number) > transfer.maxSent || !Number.isSafeInteger(credit) || (credit as number) <= 0 || transfer.completed) return;
    hostSequence = data.sequence as number;
    if (transfer.retryTimer !== null) clearTimeout(transfer.retryTimer);
    transfer.retryTimer = null; transfer.retries = 0;
    try {
      transfer.cursor.resume(offset as number);
      const bytes = transfer.cursor.read(credit as number);
      if (bytes.length) {
        transfer.maxSent = Math.max(transfer.maxSent, transfer.cursor.offset);
        send("context.transfer.chunk", { transfer_id: transfer.id, offset, bytes_sha256: hex(sha256(bytes)), bytes: bytes.buffer });
      } else {
        send("context.transfer.end", { transfer_id: transfer.id, bytes_sha256: transfer.cursor.digest() });
      }
    } catch (error) {
      transfer.reject(error instanceof Error ? error : new Error(String(error)));
      send("context.transfer.abort", { transfer_id: transfer.id });
      transfers.delete(transfer.id);
    }
  } else if (data.type === "context.transfer.ready") {
    if (!transfer.cursor.done || typeof payload.context_ref !== "string" || !payload.context_ref || payload.bytes_sha256 !== transfer.cursor.digest()) return;
    hostSequence = data.sequence as number; transfer.completed = true;
    if (transfer.retryTimer !== null) clearTimeout(transfer.retryTimer);
    transfer.resolve(payload); transfers.delete(transfer.id);
  } else if (data.type === "context.transfer.error" && typeof payload.code === "string" && typeof payload.retryable === "boolean") {
    hostSequence = data.sequence as number;
    // Retry the original immutable snapshot and identity with backpressure.
    // There is no total transfer deadline or new operation on response loss.
    if (transfer.retryTimer !== null) clearTimeout(transfer.retryTimer);
    if (payload.retryable) {
      const active = generation;
      transfer.retryTimer = setTimeout(() => {
        transfer.retryTimer = null;
        if (active === generation && transfers.get(transfer.id) === transfer && port) begin(transfer);
      }, Math.min(5000, 250 * 2 ** Math.min(transfer.retries++, 5)));
    } else {
      transfer.reject(new Error(`${payload.code}; incomplete staging was retained`));
      transfers.delete(transfer.id);
    }
  }
}

addEventListener("message", (event: MessageEvent) => {
  const data: unknown = event.data;
  if (event.source !== parent || parent === window || identity === null || !object(data) || data.type !== "host.connect" || data.announcement_id !== announcement || !object(data.binding) || !event.ports[0] || (parentOrigin !== null && event.origin !== parentOrigin)) return;
  if (!Object.entries(binding()).every(([key, value]) => (data.binding as Payload)[key] === value)) { event.ports[0].close(); return; }
  dispose(); parentOrigin = event.origin; port = event.ports[0];
  const active = generation;
  port.onmessage = ({ data: message }) => { if (active === generation) reply(message); };
  port.start();
  send("viewer.ready", { profile: "html_js_v1", ...locationState() });
  // The host supplies the public route template for new-window mapping via
  // reader.logical_route after this announcement; no separate request cycle.
  if (latestError) send("viewer.error", latestError);
});

const integration = {
  sendContext(value: unknown): Promise<Payload> {
    if (!port) return Promise.reject(new Error("Page channel is not ready"));
    let cursor: SnapshotCursor;
    try { cursor = new SnapshotCursor(value); } catch (error) { return Promise.reject(error); }
    return new Promise((resolve, reject) => {
      const transfer: Transfer = { id: crypto.randomUUID(), cursor, maxSent: 0, resolve, reject, completed: false, retries: 0, retryTimer: null };
      transfers.set(transfer.id, transfer); begin(transfer);
    });
  },
  retryContext(transferId: string): Promise<Payload> {
    const transfer = transfers.get(transferId);
    if (!transfer || !port) return Promise.reject(new Error("Original snapshot is unavailable; staging remains interrupted"));
    if (transfer.retryTimer !== null) clearTimeout(transfer.retryTimer);
    transfer.retryTimer = null;
    return new Promise((resolve, reject) => { transfer.resolve = resolve; transfer.reject = reject; begin(transfer); });
  },
  async state(value: Payload = {}): Promise<void> {
    const active = generation;
    const ready = await integration.sendContext(value);
    if (active !== generation) throw new Error("Previous page context interrupted");
    send("state.changed", { context_ref: ready.context_ref, ...locationState() });
  },
  async select(regionId: string, value: Payload = {}): Promise<void> {
    const active = generation;
    const ready = await integration.sendContext(value);
    if (active !== generation) throw new Error("Previous page context interrupted");
    send("region.selected", { context_ref: ready.context_ref, region_id: regionId, ...locationState() });
  },
};
Object.defineProperty(window, "__haeryong", { value: integration, configurable: true });

// ---- Navigation mirroring: the child owns ordinary navigation/hash/history;
// the host mirrors with replaceState only. Wrappers preserve the original
// result and errors; integration report failures never change the author's
// original return or exception contract.
const reportLocation = () => {
  if (!port) return;
  try { send("viewer.location", locationState()); } catch { /* Closed integration cannot change author history semantics. */ }
};
if (typeof history !== "undefined" && typeof history.pushState === "function") {
  for (const method of ["pushState", "replaceState"] as const) {
    const original = history[method].bind(history);
    history[method] = ((data: unknown, unused: string, url?: string | URL | null) => {
      const result = original(data, unused, url);
      reportLocation();
      return result;
    }) as typeof history.pushState;
  }
  addEventListener("popstate", reportLocation);
  addEventListener("hashchange", reportLocation);
}

// ---- Closed position protocol state: a user scroll before a late restore
// wins; duplicates are idempotent. The apply/request handlers live in the
// host-message dispatch above.

// ---- New-window interception: only NEW-WINDOW intents (target="_blank" or
// a default/empty target from a modifier/middle activation) map the
// TARGET-resolved URL — including authored <base> and a valid empty href,
// which resolves to the current URL — to the credential-free public browser
// Reader. Named in-frame targets, _self/_parent/_top and external URLs keep
// ordinary behavior untouched; the daemon's raw-top fallback handles a new
// named window.
const resolveLogicalUrl = (destination: URL): string | null => {
  if (logicalRouteTemplate === null || destination.origin !== location.origin) return null;
  const template = new URL(logicalRouteTemplate, location.origin);
  const query = new URLSearchParams();
  let page: string;
  try { page = decodeURIComponent(destination.pathname.slice(1)); } catch { return null; }
  query.set("page", page);
  if (destination.search.length > 1) query.set("page_query", destination.search.slice(1));
  if (destination.hash.length > 1) query.set("page_fragment", destination.hash.slice(1));
  return template.origin + template.pathname + "?" + query.toString();
};
const interceptNewWindow = (event: Event): void => {
  if (event.defaultPrevented) return;
  const anchor = (event.target as Element | null)?.closest?.("a");
  if (!anchor) return;
  // A non-blank explicit target (named frame, _self, _parent, _top) is not a
  // new-window intent; only _blank or no target qualifies.
  const target = anchor.target;
  if (target && target !== "_blank") return;
  // An empty href is valid and resolves to the current URL by ordinary rules.
  const href = anchor.getAttribute("href") ?? "";
  let resolved: URL;
  try { resolved = new URL(href, document.baseURI); } catch { return; }
  const logical = resolveLogicalUrl(resolved);
  if (logical === null) return;
  event.preventDefault();
  window.open(logical, "_blank", "noopener");
};
addEventListener("click", (event) => {
  const mouse = event as MouseEvent;
  if (mouse.button !== 0 && mouse.button !== 1) return;
  if (mouse.button === 0 && !mouse.metaKey && !mouse.ctrlKey && !mouse.shiftKey && !(event.target as Element | null)?.closest?.("a[target='_blank']")) return;
  interceptNewWindow(event);
}, true);
addEventListener("auxclick", (event) => { if ((event as MouseEvent).button === 1) interceptNewWindow(event); }, true);
const originalOpen = typeof window.open === "function" ? window.open.bind(window) : null;
window.open = ((url?: string | URL, target?: string, features?: string) => {
  // Map only a genuine new-window intent (no explicit named/non-blank
  // target); every other call keeps the author's target and features.
  if ((!target || target === "_blank") && (typeof url === "string" || url instanceof URL)) {
    let resolved: URL;
    try { resolved = new URL(url, document.baseURI); } catch { resolved = null as unknown as URL; }
    if (resolved && originalOpen) {
      const logical = resolveLogicalUrl(resolved);
      if (logical !== null) return originalOpen(logical, target || "_blank", features ? features + ",noopener" : "noopener");
    }
  }
  return originalOpen ? originalOpen(url, target, features) : null;
}) as typeof window.open;

addEventListener("error", (event: ErrorEvent) => {
  latestError = { code: "AUTHORED_SCRIPT_ERROR", message: event.message || "Page resource failed" };
  if (port) send("viewer.error", latestError);
}, true);
addEventListener("unhandledrejection", () => {
  latestError = { code: "AUTHORED_PROMISE_ERROR", message: "Page script failed" };
  if (port) send("viewer.error", latestError);
});
for (const type of ["wheel", "touchmove", "pointerdown", "keydown"]) addEventListener(type, () => { userScrolled = true; }, { passive: true });
addEventListener("pagehide", dispose);
addEventListener("pageshow", (event: PageTransitionEvent) => { if (event.persisted) { dispose(); announcement = crypto.randomUUID(); renderInstance = crypto.randomUUID(); announce(); } });
if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", announce, { once: true }); else announce();
