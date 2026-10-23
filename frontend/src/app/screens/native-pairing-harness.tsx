// Labelled synthetic-NativePort Connect harness (TASK-003-05 M2). This is a
// verification entry point, NEVER the product navigation flow: it mounts the
// SAME production ConnectScreen with a ShellController over the real
// NativeHaeryongClient wired to an in-page scripted synthetic port, so real
// browser keyboard interaction can prove the native pairing/adoption UI
// behaviors (tokenless approved adoption, explicit Stop abandon, late-poll
// truthfulness, typed failure notices) before the packaged Wails companion
// exists (M4). Built only into the scratch harness page via
// vite.harness.config.ts (?mode=nativepairing).
import { useEffect, useMemo, useState } from "react";
import { ShellController } from "../../client/controller.ts";
import type { ShellState } from "../../client/controller.ts";
import { NativeHaeryongClient } from "../../client/native.ts";
import type { NativePort } from "../../client/native.ts";
import { MemoryCredentialStore } from "../../client/store.ts";
import { ApiError } from "../../client/types.ts";
import type { Capabilities, SessionInfo } from "../../client/types.ts";
import { App } from "../shell.tsx";
import { ConnectScreen } from "../connect.tsx";
import "../theme.css";

type ScriptState = {
  poll: "pending" | "approved" | "expired";
  adopt: "ok" | "store-failed" | "already-adopted";
  attachAttempts: number;
  abandonResults: string[];
  pollCalls: number;
  adoptCalls: number;
  abandonCalls: number;
  pairCalls: number;
  credential: boolean;
  holdPoll: boolean;
  pendingPoll: boolean;
  daemonRunning: boolean;
  /** Labelled attach/verify fault knobs for the App foreground/pin-error regressions. */
  attachStatus: "" | "no-credential" | "revoked" | "logged-out";
  sessionStatus: "" | "revoked";
  attachFailure: "" | "api-incompatible" | "library-mismatch" | "credential-mismatched" | "daemon-stopped";
  disposeCalls: number;
  /** Labelled in-memory filesystem for the M3 shared-panel native journeys. */
  fsTree: Array<{ path: string; kind: "file" | "directory" | "symlink"; size?: number; link_target?: string }>;
  fsFiles: Record<string, number[]>;
  fsStable: boolean;
  fsReleased: number;
  packageBytes: number[];
  /** M3 C2 labelled fault/hold knobs. */
  holdSourcePicker: boolean;
  holdDestPicker: boolean;
  destCloseConflictsOnce: boolean;
  destCloseUncertain: boolean;
  destAbortFailsTemp: string | null;
  sourceReleaseCalls: number;
  packageFailWrite: boolean;
  /** M3 C3: distinct per-call identities + targeted release barriers + ordered traces. */
  holdReleaseHandles: string[];
  sourceReleaseTrace: string[];
  sourceChooseTrace: string[];
  destChooseCalls: number;
  destExists: boolean;
  destDurability: "confirmed" | "unconfirmed";
  destIntents: Array<{ replace?: boolean; expected?: { sha256: string; byte_size: number } } | undefined>;
  destWriteCalls: number;
  destCloseCalls: number;
  destAbortCalls: number;
};

const session: SessionInfo = {
  session_id: "synthetic-native-1",
  principal_id: "owner",
  library_id: "lib-native",
  device_label: "Desktop",
  origin: "http://127.0.0.1:9271",
  state: "active",
  created_at: "2026-10-19T00:00:00Z",
  last_seen_at: "2026-10-19T00:00:00Z",
  expires_at: null,
  revision: 1,
  etag: '"synthetic-1"',
  scope: { kind: "owner" },
};

const caps: Capabilities = {
  library_id: "lib-native",
  api_version: "1.0",
  runtime_generation: "g1",
  stream_epoch: "e1",
  unfiled_project_id: "unfiled",
  features: {},
};

class SyntheticNativePort {
  readonly script: ScriptState = {
    poll: "pending",
    adopt: "ok",
    attachAttempts: 0,
    abandonResults: [],
    pollCalls: 0,
    adoptCalls: 0,
    abandonCalls: 0,
    pairCalls: 0,
    credential: false,
    holdPoll: false,
    pendingPoll: false,
    daemonRunning: false,
    attachStatus: "",
    sessionStatus: "",
    attachFailure: "",
    disposeCalls: 0,
    fsTree: [],
    fsFiles: {},
    fsStable: true,
    fsReleased: 0,
    packageBytes: [72, 105],
    holdSourcePicker: false,
    holdDestPicker: false,
    destCloseConflictsOnce: false,
    destCloseUncertain: false,
    destAbortFailsTemp: null as string | null,
    sourceReleaseCalls: 0,
    packageFailWrite: false,
    holdReleaseHandles: [] as string[],
    sourceReleaseTrace: [] as string[],
    sourceChooseTrace: [] as string[],
    destChooseCalls: 0,
    destExists: false,
    destDurability: "confirmed",
    destIntents: [],
    destWriteCalls: 0,
    destCloseCalls: 0,
    destAbortCalls: 0,
  };
  /** Per-call resolvers for EVERY held poll: a foreground event can legitimately hold a second poll while the first is still in flight. */
  releaseQueue: Array<() => void> = [];
  /** Held destination picker replies (late-choice barriers). */
  destPickerQueue: Array<() => void> = [];
  /** Held source picker replies (late-choice barriers). */
  sourcePickerQueue: Array<() => void> = [];
  /** Targeted per-handle release barriers (exact await-resume races). */
  releaseHolds: Array<{ handle: string; resolve: () => void }> = [];
  /** Distinct source handle identities per chooseImportSource call. */
  sourceHandleCounter = 0;

  /** Release a held releaseSource call by handle (exact barrier control). */
  releaseHeldRelease(handle: string): void {
    const remaining: Array<{ handle: string; resolve: () => void }> = [];
    for (const held of this.releaseHolds) {
      if (held.handle === handle) held.resolve();
      else remaining.push(held);
    }
    this.releaseHolds = remaining;
  }

  /** Release every held releaseSource call. */
  releaseAllHeldReleases(): void {
    const held = this.releaseHolds;
    this.releaseHolds = [];
    for (const item of held) item.resolve();
  }

  releaseAllPolls(): void {
    const queue = this.releaseQueue;
    this.releaseQueue = [];
    for (const release of queue) release();
  }

  /** Releases every held destination picker reply (late-choice barriers). */
  releaseDestPickers(): void {
    const queue = this.destPickerQueue;
    this.destPickerQueue = [];
    for (const release of queue) release();
  }

  /** Releases every held source picker reply (late-choice barriers). */
  releaseSourcePickers(): void {
    const queue = this.sourcePickerQueue;
    this.sourcePickerQueue = [];
    for (const release of queue) release();
  }

  async call(operation: string, _payload: Record<string, unknown>): Promise<unknown> {
    switch (operation) {
      case "pair":
        this.script.pairCalls++;
        return { pairing_id: "synthetic-pair-1", code: "246810", expires_at: new Date(Date.now() + 5 * 60_000).toISOString() };
      case "pollPairing": {
        this.script.pollCalls++;
        if (this.script.holdPoll) {
          this.script.pendingPoll = true;
          await new Promise<void>((resolve) => { this.releaseQueue.push(resolve); });
          this.script.pendingPoll = this.releaseQueue.length > 0;
        }
        if (this.script.poll === "pending") return { state: "pending" };
        if (this.script.poll === "expired") return { state: "expired" };
        return {
          state: "approved",
          session: { session_id: session.session_id, principal_id: session.principal_id, library_id: session.library_id, device_label: session.device_label },
        };
      }
      case "adoptPairing": {
        this.script.adoptCalls++;
        if (this.script.adopt === "store-failed") throw new ApiError(500, "NATIVE_CREDENTIAL_STORE_FAILED", false, 0, {});
        if (this.script.adopt === "already-adopted") throw new ApiError(409, "NATIVE_PAIRING_ALREADY_ADOPTED", false, 0, {});
        this.script.credential = true;
        // Adoption installs the captured endpoint/bearer pair together: a
        // previously revoked/absent remembered credential no longer answers
        // the next attach.
        this.script.attachStatus = "";
        this.script.sessionStatus = "";
        return { adopted: true, durability: "confirmed" };
      }
      case "versionReferences": return { items: [], next_cursor: null, materialization: { state: "ready", reason: null } };
      case "resolveReference": throw new ApiError(404, "NOT_FOUND", false);
      case "abandonPairingAttempt": {
        this.script.abandonCalls++;
        const outcome = this.script.abandonResults.shift() ?? "abandoned";
        if (outcome === "already-adopted") {
          this.script.credential = true;
          throw new ApiError(409, "NATIVE_PAIRING_ALREADY_ADOPTED", false, 0, {});
        }
        if (outcome === "adopt-failed") throw new ApiError(409, "NATIVE_PAIRING_ADOPT_FAILED", false, 0, {});
        if (outcome === "network-failed") throw new ApiError(0, "NETWORK_ERROR", true, 0, {});
        return { abandoned: true };
      }
      case "attachSession": {
        this.script.attachAttempts++;
        if (this.script.attachFailure === "daemon-stopped" && !this.script.daemonRunning) throw new ApiError(503, "NATIVE_DAEMON_UNAVAILABLE", true, 0, {});
        if (this.script.attachFailure === "api-incompatible") throw new ApiError(409, "NATIVE_API_INCOMPATIBLE", false, 0, {});
        if (this.script.attachFailure === "library-mismatch") throw new ApiError(409, "NATIVE_LIBRARY_MISMATCH", false, 0, {});
        if (this.script.attachFailure === "credential-mismatched") throw new ApiError(409, "NATIVE_CREDENTIAL_MISMATCHED", false, 0, {});
        if (this.script.attachStatus === "revoked") throw new ApiError(401, "SESSION_REVOKED", false, 0, {});
        if (this.script.attachStatus === "logged-out") throw new ApiError(401, "NATIVE_SESSION_LOGGED_OUT", false, 0, {});
        if (this.script.attachStatus === "no-credential" || !this.script.credential) throw new ApiError(401, "NATIVE_NO_CREDENTIAL", false, 0, {});
        return { attachment_id: `synthetic-attachment-${this.script.attachAttempts}`, session, capabilities: caps, public_origin: "http://127.0.0.1:9270" };
      }
      case "disposeSession":
        this.script.disposeCalls++;
        return;
      case "revokeOwnSession":
        this.script.credential = false;
        return;
      case "currentSession":
        // Models another device revoking the Go-held credential while the
        // attachment is live: Go itself keeps the token and file until an
        // explicit logout forgets them.
        if (this.script.sessionStatus === "revoked") throw new ApiError(401, "SESSION_REVOKED", false, 0, {});
        return session;
      case "capabilities":
        return caps;
      case "serviceDiscovery":
        return this.script.daemonRunning ? { state: "verified", profile: "default", endpoint: "http://127.0.0.1:9270", library_id: "lib-native" } : { state: "daemon_stopped", profile: "default" };
      case "startDaemonOffer":
        this.script.daemonRunning = true;
        return { started: true, discovery: { state: "verified", profile: "default", endpoint: "http://127.0.0.1:9270", library_id: "lib-native" } };
      case "listSessions":
        return [];
      case "pendingPairings":
        return [];
      case "serviceStatus":
        return { ok: true };
      case "configurationView":
      case "configurationPlan":
      case "configurationPlanCommit":
      case "projectRegister":
      case "projectSources":
      case "verifySourceEvidence":
      case "runSourceObservation":
        throw new Error("unavailable in pairing fixture");
      case "listProjects":
        return { items: [{ project_id: "proj-1", name: "네이티브 결과", lifecycle: "active", revision: 1, permissions_revision: 1, updated_at: "2026-10-19T00:00:00Z", etag: '"p1"' }], next_cursor: null };
      case "listDocuments":
        return { items: [{ document_id: "doc-1", project_id: "proj-1", title: "네이티브 결과 문서", lifecycle: "active", latest_version_id: "ver-1", revision: 1, updated_at: "2026-10-19T00:00:00Z", versions_url: "", etag: '"d1"' }], next_cursor: null };
      case "chooseImportSource": {
        this.script.pairCalls++;
        if (this.script.holdSourcePicker) {
          await new Promise<void>((resolve) => { this.sourcePickerQueue.push(resolve); });
        }
        this.sourceHandleCounter += 1;
        const handle = `fs-src-${this.sourceHandleCounter}`;
        this.script.sourceChooseTrace.push(handle);
        return {
          handle,
          selection_name: "native-result",
          mode: "directory",
          entries: this.fsPage(0),
          limits: { empty_directories_represented: true, symlinks_represented: true },
        };
      }
      case "sourceEntries": {
        const start = Number((_payload as { cursor?: string }).cursor ?? 0) || 0;
        return this.fsPage(start);
      }
      case "readSourceRange": {
        const request = _payload as { path: string; offset: number; length: number };
        const bytes = this.script.fsFiles[request.path] ?? [];
        const slice = bytes.slice(request.offset, request.offset + request.length);
        if (slice.length !== request.length) throw new ApiError(409, "NATIVE_SOURCE_CHANGED", false, 0, { path: request.path });
        return { bytes_b64: btoa(String.fromCharCode(...slice)) };
      }
      case "releaseSource": {
        const releasedHandle = (_payload as { handle: string }).handle;
        this.script.fsReleased++;
        this.script.sourceReleaseCalls++;
        this.script.sourceReleaseTrace.push(releasedHandle);
        if (this.script.holdReleaseHandles.includes(releasedHandle)) {
          await new Promise<void>((resolve) => { this.releaseHolds.push({ handle: releasedHandle, resolve }); });
        }
        return {};
      }
      case "sourceVerifyStable":
        return this.script.fsStable ? { stable: true } : { stable: false, changed_path: "nested/page.html" };
      case "chooseDownloadDestination": {
        if (this.script.holdDestPicker) {
          await new Promise<void>((resolve) => { this.destPickerQueue.push(resolve); });
        }
        this.script.destChooseCalls += 1;
        return { handle: "fs-dest", label: `Downloads/${(_payload as { suggested_name: string }).suggested_name}`, exists: this.script.destExists };
      }
      case "destOpen":
        this.script.destIntents.push((_payload as { intent?: { replace?: boolean; expected?: { sha256: string; byte_size: number } } }).intent);
        return { opened: true };
      case "destWrite": {
        this.script.destWriteCalls++;
        if (this.script.packageFailWrite) throw new ApiError(500, "NATIVE_DESTINATION_IO_FAILED", false, 0, {});
        return { written: (_payload as { bytes_b64: string }).bytes_b64.length };
      }
      case "destClose": {
        this.script.destCloseCalls++;
        if (this.script.destCloseUncertain) throw new Error("bridge reply vanished");
        if (this.script.destCloseConflictsOnce) {
          this.script.destCloseConflictsOnce = false;
          throw new ApiError(409, "NATIVE_DESTINATION_CONFLICT", false, 0, { target: "Downloads/a.zip" });
        }
        return { published: true, durability: this.script.destDurability };
      }
      case "destAbort": {
        this.script.destAbortCalls++;
        const failsTemp = this.script.destAbortFailsTemp;
        if (failsTemp !== null) {
          throw new ApiError(500, "NATIVE_CLEANUP_FAILED", false, 0, { temp: failsTemp });
        }
        return { aborted: true };
      }
      case "packageStream": {
        // Reified PackageStream shape (the synthetic port stands in for the
        // JSON wire): two streamed chunks then a clean end.
        const source = this.script.packageBytes;
        let index = 0;
        const body = new ReadableStream<Uint8Array>({
          pull(controller) {
            if (index >= source.length) {
              controller.close();
              return;
            }
            controller.enqueue(new Uint8Array([source[index]]));
            index += 1;
          },
        });
        return { body, cancel() {} };
      }
      case "subscribeEvents":
        // Liveness-only subscription that never closes on its own.
        return { closed: new Promise<void>(() => {}), close() {} };
      default:
        throw new ApiError(501, "FEATURE_NOT_READY", false, 0, {});
    }
  }

  /** Two entries per page so the browser journey proves cursor collection. */
  fsPage(start: number): { items: Array<{ path: string; kind: "file" | "directory" | "symlink"; size?: number; link_target?: string }>; next_cursor: string | null } {
    const end = Math.min(this.script.fsTree.length, start + 2);
    return { items: this.script.fsTree.slice(start, end), next_cursor: end < this.script.fsTree.length ? String(end) : null };
  }
}

export function NativePairingHarness() {
  const port = useMemo(() => new SyntheticNativePort(), []);
  const [controller] = useState(() => new ShellController(new NativeHaeryongClient(port as unknown as NativePort), new MemoryCredentialStore()));
  const [state, setState] = useState<ShellState>(controller.state);
  useEffect(() => {
    // Labelled fixture instrumentation for the browser proof only.
    (window as unknown as { __nativePairingHarness: SyntheticNativePort }).__nativePairingHarness = port;
    const stop = controller.subscribe(setState);
    void controller.attachNativeSession();
    return () => {
      stop();
      controller.stop();
    };
  }, [controller, port]);
  return (
    <main className="shell-content">
      <section className="panel" data-testid="native-pairing-harness" data-phase={state.phase}>
        <h1>네이티브 페어링 harness (검증 전용)</h1>
        <p className="hint">합성 NativePort로 실제 Connect 화면을 구동하는 라벨 검증 진입점입니다. 제품 흐름이 아닙니다.</p>
      </section>
      <ConnectScreen controller={controller} state={state} />
    </main>
  );
}

/**
 * Labelled REAL-App harness: mounts the production `App` (including its own
 * visibility/foreground re-verify wiring) over the same synthetic NativePort,
 * so the browser proof can exercise the actual foreground handler path — not
 * a copied hook. `?attachFailure=` presets a permanent attach pin error for
 * the incompatible/blocked-state regressions. Verification entrypoint only,
 * never a product navigation flow.
 */
export function NativeAppHarness() {
  const port = useMemo(() => {
    const created = new SyntheticNativePort();
    const requested = new URLSearchParams(location.search).get("attachFailure") ?? "";
    if (requested === "api-incompatible" || requested === "library-mismatch" || requested === "credential-mismatched" || requested === "daemon-stopped") {
      created.script.attachFailure = requested;
    }
    return created;
  }, []);
  const [controller] = useState(() => new ShellController(new NativeHaeryongClient(port as unknown as NativePort), new MemoryCredentialStore()));
  useEffect(() => {
    (window as unknown as { __nativePairingHarness: SyntheticNativePort }).__nativePairingHarness = port;
    (window as unknown as { __nativePairingState: () => { phase: string } }).__nativePairingState = () => controller.state;
    // The production native entrypoint attaches once after mounting; the App
    // itself owns start/stop and the visibility handler under test.
    void controller.attachNativeSession();
    return () => controller.stop();
  }, [controller, port]);
  return <App controller={controller} />;
}
