// Controller state-machine tests. Pure Node: no DOM, no network; the fake
// client scripts session/capabilities outcomes and records every call.
import test from "node:test";
import assert from "node:assert/strict";
import { NOTICE, ShellController, nativeDaemonStateLabel, nativePairingNotice } from "./controller.ts";
import type { PendingDecision } from "./controller.ts";
import { NativeHaeryongClient } from "./native.ts";
import type { NativePort } from "./native.ts";
import { MemoryCredentialStore } from "./store.ts";
import { ApiError, DispatchBlockedError, NetworkError, StaleResponseError, UncertainResponseError } from "./types.ts";
import type {
  Capabilities,
  CredentialAware,
  DecisionResult,
  DeviceSession,
  EventSubscription,
  HaeryongClient,
  OperationView,
  PairingCreated,
  PairingDecisionRequest,
  PairingPoll,
  PairingView,
  SessionInfo,
} from "./types.ts";

function sessionInfo(libraryId = "lib-1", sessionId = "sess-1"): SessionInfo {
  return {
    session_id: sessionId,
    principal_id: "owner",
    library_id: libraryId,
    device_label: "tester",
    origin: "http://127.0.0.1:9270",
    state: "active",
    created_at: "2026-10-17T00:00:00Z",
    last_seen_at: "2026-10-17T00:00:00Z",
    expires_at: null,
    revision: 1,
    etag: '"s1"',
    scope: { kind: "owner" },
  };
}

function capabilities(overrides: Partial<Capabilities> = {}): Capabilities {
  return {
    library_id: "lib-1",
    api_version: "1.0",
    runtime_generation: "g1",
    stream_epoch: "e1",
    unfiled_project_id: "unfiled",
    features: {},
    ...overrides,
  };
}

function operationView(id: string, state = "completed"): OperationView {
  return { operation_id: id, state, original_status: 200, result_ref: null, job_id: null, result_metadata: null };
}

class FakeClient implements HaeryongClient, CredentialAware {
async lifecyclePlan():Promise<never>{throw new Error("not used in controller tests");}
async lifecyclePlanGet():Promise<never>{throw new Error("not used in controller tests");}
async lifecyclePlanCommit():Promise<never>{throw new Error("not used in controller tests");}
async trashList():Promise<never>{throw new Error("not used in controller tests");}

  async annotationList():Promise<never>{throw new Error("not used in controller tests");}
  async annotationGet():Promise<never>{throw new Error("not used in controller tests");}
  async annotationHistory():Promise<never>{throw new Error("not used in controller tests");}
  async annotationCreate():Promise<never>{throw new Error("not used in controller tests");}
  async annotationPatch():Promise<never>{throw new Error("not used in controller tests");}
  async annotationDelete():Promise<never>{throw new Error("not used in controller tests");}
  async annotationBodyStream():Promise<never>{throw new Error("not used in controller tests");}
  async annotationContextStream():Promise<never>{throw new Error("not used in controller tests");}
  async revisitGet():Promise<never>{throw new Error("not used in controller tests");}
  async revisitPut():Promise<never>{throw new Error("not used in controller tests");}

  async configurationView(): Promise<never> { throw new Error("not provided by fixture"); }
  async configurationPlan(): Promise<never> { throw new Error("not provided by fixture"); }
  async configurationPlanCommit(): Promise<never> { throw new Error("not provided by fixture"); }
  async projectRegister(): Promise<never> { throw new Error("not provided by fixture"); }
  async projectSources(): Promise<never> { throw new Error("not provided by fixture"); }
  async verifySourceEvidence(): Promise<never> { throw new Error("not provided by fixture"); }
  async runSourceObservation(): Promise<never> { throw new Error("not provided by fixture"); }
  calls: string[] = [];
  credential = "";
  sessionHandler: () => Promise<SessionInfo> = () => Promise.resolve(sessionInfo());
  capabilitiesHandler: () => Promise<Capabilities> = () => Promise.resolve(capabilities());
  listHandler: () => Promise<DeviceSession[]> = () => Promise.resolve([]);
  ownRevokeHandler: () => Promise<void> = () => Promise.resolve();
  // Real streams stay open until they end; tests resolve `closed` explicitly.
  eventsHandler: () => EventSubscription = () => ({ closed: new Promise<void>(() => {}), close() {} });

  setCredential(token: string): void {
    this.credential = token;
    this.calls.push("setCredential");
  }
  invalidate(): void {
    this.calls.push("invalidate");
  }
  pair(): Promise<PairingCreated> {
    this.calls.push("pair");
    throw new Error("not used in controller tests");
  }
  pollPairing(): Promise<PairingPoll> {
    this.calls.push("pollPairing");
    throw new Error("not used in controller tests");
  }
  decidePairing(request: PairingDecisionRequest): Promise<DecisionResult> {
    this.calls.push("decide:" + request.operation_id + ":" + request.action);
    return Promise.resolve({ kind: "committed", body: {} });
  }
  currentSession(): Promise<SessionInfo> {
    this.calls.push("session");
    return this.sessionHandler();
  }
  capabilities(): Promise<Capabilities> {
    this.calls.push("capabilities");
    return this.capabilitiesHandler();
  }
  listSessions(): Promise<DeviceSession[]> {
    this.calls.push("listSessions");
    return this.listHandler();
  }
  pendingPairings(): Promise<PairingView[]> {
    this.calls.push("pendingPairings");
    return Promise.resolve([]);
  }
  operation(id: string): Promise<OperationView> {
    this.calls.push("operation");
    return Promise.resolve(operationView(id));
  }
  revokeSession(): Promise<void> {
    this.calls.push("revokeSession");
    return Promise.resolve();
  }
  revokeOwnSession(): Promise<void> {
    this.calls.push("revokeOwnSession");
    return this.ownRevokeHandler();
  }
  serviceStatus(): Promise<unknown> {
    this.calls.push("serviceStatus");
    return Promise.resolve({});
  }
  // Library/transfer surface: recorded but unused in controller phase tests.
  listProjects(): Promise<never> {
    this.calls.push("listProjects");
    throw new Error("not used in controller tests");
  }
  versionBacklinks(): Promise<never> { throw new Error("unused backlinks fake"); }
  validateVersionIndexes(): Promise<never> { throw new Error("unused validate fake"); }
  rebuildVersionIndexes(): Promise<never> { throw new Error("unused rebuild fake"); }
  versionReferences(): Promise<never> { throw new Error("unused references fake"); }
  resolveReference(): Promise<never> { throw new Error("unused reference fake"); }
  search(): Promise<import("./types.ts").SearchPage> { throw new Error("unused search fake"); }
  listDocuments(): Promise<never> {
    this.calls.push("listDocuments");
    throw new Error("not used in controller tests");
  }
  documentDetail(): Promise<never> {
    this.calls.push("documentDetail");
    throw new Error("not used in controller tests");
  }
  documentVersions(): Promise<never> {
    this.calls.push("documentVersions");
    throw new Error("not used in controller tests");
  }
  versionDetail(): Promise<never> {
    this.calls.push("versionDetail");
    throw new Error("not used in controller tests");
  }
  versionFiles(): Promise<never> {
    this.calls.push("versionFiles");
    throw new Error("not used in controller tests");
  }
  versionSuppliedMetadata(): Promise<never> {
    this.calls.push("versionSuppliedMetadata");
    throw new Error("not used in controller tests");
  }
  versionEvidence(): Promise<never> {
    this.calls.push("versionEvidence");
    throw new Error("not used in controller tests");
  }
  evidenceVerifications(): Promise<never> {
    this.calls.push("evidenceVerifications");
    throw new Error("not used in controller tests");
  }
  versionAssessments(): Promise<never> {
    this.calls.push("versionAssessments");
    throw new Error("not used in controller tests");
  }
  assessmentCoverage(): Promise<never> {
    this.calls.push("assessmentCoverage");
    throw new Error("not used in controller tests");
  }
  listUploads(): Promise<never> {
    this.calls.push("listUploads");
    throw new Error("not used in controller tests");
  }
  uploadDetail(): Promise<never> {
    this.calls.push("uploadDetail");
    throw new Error("not used in controller tests");
  }
  uploadEntries(): Promise<never> {
    this.calls.push("uploadEntries");
    throw new Error("not used in controller tests");
  }
  uploadRanges(): Promise<never> {
    this.calls.push("uploadRanges");
    throw new Error("not used in controller tests");
  }
  uploadPins(): Promise<never> {
    this.calls.push("uploadPins");
    throw new Error("not used in controller tests");
  }
  createUpload(): Promise<never> {
    this.calls.push("createUpload");
    throw new Error("not used in controller tests");
  }
  putUploadEntry(): Promise<never> {
    this.calls.push("putUploadEntry");
    throw new Error("not used in controller tests");
  }
  completeUpload(): Promise<never> {
    this.calls.push("completeUpload");
    throw new Error("not used in controller tests");
  }
  abandonUpload(): Promise<never> {
    this.calls.push("abandonUpload");
    throw new Error("not used in controller tests");
  }
  importPublish(): Promise<never> {
    this.calls.push("importPublish");
    throw new Error("not used in controller tests");
  }
  jobGet(): Promise<never> {
    this.calls.push("jobGet");
    throw new Error("not used in controller tests");
  }
  jobCancel(): Promise<never> {
    this.calls.push("jobCancel");
    throw new Error("not used in controller tests");
  }
  packageStream(): Promise<never> {
    this.calls.push("packageStream");
    throw new Error("not used in controller tests");
  }
  renderBootstrap(): Promise<never> {
    throw new Error("not used in controller tests");
  }
  renderSessionOpen(): Promise<never> {
    throw new Error("not used in controller tests");
  }
  renderSessionRenew(): Promise<void> {
    return Promise.resolve();
  }
  renderSessionClose(): Promise<void> {
    return Promise.resolve();
  }
  reviewGet(): Promise<never> { throw new Error("not used in controller tests"); }
  reviewPut(): Promise<never> { throw new Error("not used in controller tests"); }
  reviewHistory(): Promise<never> { throw new Error("not used in controller tests"); }
  personalGet(): Promise<never> {
    throw new Error("not used in controller tests");
  }
  personalPut(): Promise<never> {
    throw new Error("not used in controller tests");
  }
  subscribeEvents(): EventSubscription {
    this.calls.push("subscribeEvents");
    return this.eventsHandler();
  }
}

async function settle(steps = 8): Promise<void> {
  for (let i = 0; i < steps; i++) await new Promise((resolve) => setTimeout(resolve, 0));
}

test("no credential starts unauthenticated", async () => {
  const controller = new ShellController(new FakeClient(), new MemoryCredentialStore());
  await controller.verify();
  assert.equal(controller.state.phase, "unauthenticated");
  assert.equal(controller.state.notice, NOTICE.unauthenticated);
});

test("remembered credential verifies session then capabilities", async () => {
  const client = new FakeClient();
  const store = new MemoryCredentialStore();
  store.write("token-1");
  const controller = new ShellController(client, store);
  await controller.verify();
  assert.equal(controller.state.phase, "verified");
  assert.equal(controller.state.memoryOnly, false);
  assert.equal(controller.state.notice, NOTICE.verifiedRemembered);
  assert.ok(client.calls.indexOf("session") < client.calls.indexOf("capabilities"));
  assert.deepEqual([...client.calls].slice(0, 3), ["setCredential", "invalidate", "session"]);
});

test("memory-only credential reports the memory-only notice", async () => {
  const controller = new ShellController(new FakeClient(), new MemoryCredentialStore());
  await controller.acceptPairing("token-2", false);
  assert.equal(controller.state.phase, "verified");
  assert.equal(controller.state.memoryOnly, true);
  assert.equal(controller.state.notice, NOTICE.verifiedMemory);
});

test("unsupported api_version is incompatible and keeps the credential", async () => {
  const client = new FakeClient();
  client.capabilitiesHandler = () => Promise.resolve(capabilities({ api_version: "2.0" }));
  const store = new MemoryCredentialStore();
  store.write("token-3");
  const controller = new ShellController(client, store);
  await controller.verify();
  assert.equal(controller.state.phase, "incompatible");
  assert.equal(controller.state.notice, NOTICE.incompatible);
  assert.equal(store.read(), "token-3");
  assert.equal(controller.state.session, null);
});

test("library identity mismatch is incompatible", async () => {
  const client = new FakeClient();
  client.capabilitiesHandler = () => Promise.resolve(capabilities({ library_id: "other" }));
  const store = new MemoryCredentialStore();
  store.write("token-4");
  const controller = new ShellController(client, store);
  await controller.verify();
  assert.equal(controller.state.phase, "incompatible");
});

test("network failure degrades, retains credential and last confirmed session", async () => {
  const client = new FakeClient();
  const store = new MemoryCredentialStore();
  store.write("token-5");
  const controller = new ShellController(client, store);
  await controller.verify();
  assert.equal(controller.state.phase, "verified");
  client.sessionHandler = () => Promise.reject(new NetworkError());
  await controller.verify();
  assert.equal(controller.state.phase, "degraded");
  assert.equal(controller.state.notice, NOTICE.disconnected);
  assert.equal(store.read(), "token-5");
  assert.ok(controller.state.session);
  assert.equal(controller.state.session?.session_id, "sess-1");
});

test("SESSION_REVOKED clears the credential as unusable", async () => {
  const client = new FakeClient();
  client.sessionHandler = () => Promise.reject(new ApiError(401, "SESSION_REVOKED", false));
  const store = new MemoryCredentialStore();
  store.write("token-6");
  const controller = new ShellController(client, store);
  await controller.verify();
  assert.equal(controller.state.phase, "unauthenticated");
  assert.equal(controller.state.notice, NOTICE.revoked);
  assert.equal(store.read(), null);
});

test("SESSION_EXPIRED clears the credential with the expiry notice", async () => {
  const client = new FakeClient();
  client.sessionHandler = () => Promise.reject(new ApiError(401, "SESSION_EXPIRED", false));
  const store = new MemoryCredentialStore();
  store.write("token-7");
  const controller = new ShellController(client, store);
  await controller.verify();
  assert.equal(controller.state.notice, NOTICE.expired);
  assert.equal(store.read(), null);
});

test("older verification runs never apply their responses", async () => {
  const client = new FakeClient();
  const gate = { release: null as null | ((value: SessionInfo) => void) };
  client.sessionHandler = () =>
    new Promise((resolve) => {
      gate.release = resolve;
    });
  const store = new MemoryCredentialStore();
  store.write("token-8");
  const controller = new ShellController(client, store);
  const first = controller.verify();
  await settle(2);
  client.sessionHandler = () => Promise.resolve(sessionInfo("lib-1", "sess-current"));
  const second = controller.verify();
  await second;
  gate.release?.(sessionInfo("lib-1", "sess-stale"));
  await first;
  await settle();
  assert.equal(controller.state.phase, "verified");
  assert.equal(controller.state.session?.session_id, "sess-current");
});

test("runtime generation change re-verifies confirmed state", async () => {
  const client = new FakeClient();
  const store = new MemoryCredentialStore();
  store.write("token-9");
  const controller = new ShellController(client, store);
  const notices: string[] = [];
  controller.subscribe((state) => notices.push(state.notice));
  await controller.verify();
  assert.equal(controller.state.capabilities?.runtime_generation, "g1");
  client.capabilitiesHandler = () => Promise.resolve(capabilities({ runtime_generation: "g2" }));
  await controller.verify();
  await settle();
  assert.equal(controller.state.phase, "verified");
  assert.equal(controller.state.capabilities?.runtime_generation, "g2");
  assert.ok(notices.includes(NOTICE.generationChanged));
  // verify#1 reads g1; the g2 observation re-verifies once (sessions 2 and 3).
  assert.equal(client.calls.filter((call) => call === "session").length, 3);
});

test("logout clears local credential first and reports an unknown server outcome honestly", async () => {
  const client = new FakeClient();
  const store = new MemoryCredentialStore();
  store.write("token-10");
  const controller = new ShellController(client, store);
  await controller.verify();
  client.ownRevokeHandler = () => Promise.reject(new NetworkError());
  client.calls = [];
  await controller.logout();
  assert.equal(controller.state.phase, "unauthenticated");
  assert.equal(store.read(), null);
  assert.equal(controller.state.notice, NOTICE.logoutAckUnknown);
  assert.deepEqual(client.calls, [
    "setCredential", // clearCredential drops the transport binding first
    "invalidate",
    "setCredential", // logout rebinds the snapshotted token for one best-effort revoke
    "revokeOwnSession",
    "setCredential", // guarded finally clears the binding again
    "invalidate",
  ]);
});

test("logout acknowledges a confirmed server revocation", async () => {
  const controller = new ShellController(new FakeClient(), new MemoryCredentialStore());
  await controller.acceptPairing("token-11", true);
  await controller.logout();
  assert.equal(controller.state.notice, NOTICE.logoutConfirmed);
});

test("logout treats an already-revoked server session as confirmed", async () => {
  const client = new FakeClient();
  client.ownRevokeHandler = () => Promise.reject(new ApiError(401, "SESSION_REVOKED", false));
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-12", false);
  await controller.logout();
  assert.equal(controller.state.notice, NOTICE.logoutAlreadyRevoked);
});

test("uncertain device revoke resolves through the session list", async () => {
  const client = new FakeClient();
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-13", false);
  client.calls = [];
  const outcome = await controller.revokeSession("sess-peer");
  assert.equal(outcome, "revoked");
  const sessions: DeviceSession[] = [
    {
      session_id: "sess-peer",
      device_label: "peer",
      state: "active",
      origin: "http://127.0.0.1:9270",
      last_seen_at: "2026-10-17T00:00:00Z",
    },
  ];
  client.listHandler = () => Promise.resolve(sessions);
  assert.equal(await controller.confirmRevokeResolved("sess-peer"), false);
  // Production lists keep revoked rows: a revoked row is confirmed completion.
  sessions[0] = { ...sessions[0], state: "revoked" };
  assert.equal(await controller.confirmRevokeResolved("sess-peer"), true);
  // An expired row is NOT proven revoked.
  client.listHandler = () =>
    Promise.resolve([
      {
        session_id: "sess-peer",
        device_label: "peer",
        state: "expired",
        origin: "http://127.0.0.1:9270",
        last_seen_at: "2026-10-17T00:00:00Z",
      },
    ]);
  assert.equal(await controller.confirmRevokeResolved("sess-peer"), false);
  client.listHandler = () => Promise.resolve([]);
  assert.equal(await controller.confirmRevokeResolved("sess-peer"), true);
});

test("an unreadable success body preserves K recovery without claiming success", async () => {
  const client = new FakeClient();
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-32", false);
  // (a) A 2xx body that cannot be read keeps the operation uncertain.
  client.decidePairing = () => Promise.reject(new UncertainResponseError(new TypeError("stream error")));
  const outcome = await controller.decidePairing({
    pairing_id: "pair-11",
    code: "777777",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-11-r1"',
    operation_id: "op-11",
    action: "approve",
  });
  assert.deepEqual(outcome, { outcome: "unknown", operation: "op-11" });
  assert.equal(controller.state.pendingDecision?.request.operation_id, "op-11");
  // The pending intent is recoverable through the receipt.
  client.operation = () => Promise.resolve(operationView("op-11", "completed"));
  const reconciled = await controller.reconcileDecision("op-11");
  assert.ok(reconciled);
  assert.equal(controller.state.pendingDecision, null);
  // (b) The same failure crossing an authority replacement never reinserts.
  const gate = { reject: null as null | ((reason: Error) => void) };
  client.decidePairing = () =>
    new Promise((_resolve, reject) => {
      gate.reject = reject;
    });
  const late = controller.decidePairing({
    pairing_id: "pair-12",
    code: "666001",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-12-r1"',
    operation_id: "op-12",
    action: "approve",
  });
  await settle(2);
  await controller.logout();
  gate.reject?.(new UncertainResponseError());
  assert.deepEqual(await late, { outcome: "unknown", operation: "op-12" });
  assert.equal(controller.state.pendingDecision, null);
});

test("network-lost revoke stays unknown until resolved under current authority", async () => {
  const client = new FakeClient();
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-14", false);
  const original = client.revokeSession.bind(client);
  client.revokeSession = () => Promise.reject(new NetworkError());
  assert.equal(await controller.revokeSession("sess-peer"), "unknown");
  client.revokeSession = original;
  client.listHandler = () => Promise.resolve([]);
  assert.equal(await controller.confirmRevokeResolved("sess-peer"), true);
});

test("pairing decision loss reconciles through the operation receipt and a fresh pairing list", async () => {
  const client = new FakeClient();
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-15", false);
  client.decidePairing = () => Promise.reject(new NetworkError());
  const outcome = await controller.decidePairing({
    pairing_id: "pair-1",
    code: "123456",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-1-r1"',
    operation_id: "op-1",
    action: "approve",
  });
  assert.deepEqual(outcome, { outcome: "unknown", operation: "op-1" });
  const reconciled = await controller.reconcileDecision("op-1");
  assert.ok(reconciled);
  assert.equal(reconciled.operation.operation_id, "op-1");
  assert.equal(reconciled.operation.state, "completed");
  assert.ok(client.calls.includes("pendingPairings"));
  assert.equal(controller.state.pendingDecision, null, "terminal completed receipt clears the pending intent");
});

test("only terminal receipts resolve an uncertain decision intent", async () => {
  const client = new FakeClient();
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-15b", false);
  client.decidePairing = () => Promise.reject(new NetworkError());
  await controller.decidePairing({
    pairing_id: "pair-1b",
    code: "123456",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-1b-r1"',
    operation_id: "op-1b",
    action: "approve",
  });
  assert.ok(controller.state.pendingDecision);
  client.operation = () => Promise.resolve(operationView("op-1b", "reconciling"));
  let reconciled = await controller.reconcileDecision("op-1b");
  assert.ok(reconciled);
  assert.equal(reconciled.operation.state, "reconciling");
  assert.ok(controller.state.pendingDecision, "non-terminal receipt keeps the intent");
  client.operation = () => Promise.resolve(operationView("op-1b", "failed"));
  reconciled = await controller.reconcileDecision("op-1b");
  assert.ok(reconciled);
  assert.equal(controller.state.pendingDecision, null, "terminal failed receipt also resolves the intent");
});

test("the canonical 202 accepted envelope stays pending, never success", async () => {
  const client = new FakeClient();
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-29", false);
  // Actual jsonBytesPending envelope delivered with HTTP 202.
  client.decidePairing = () =>
    Promise.resolve<DecisionResult>({
      kind: "accepted",
      body: { operation_id: "op-9", state: "accepted", operation_url: "/api/v1/operations/op-9" },
    });
  const outcome = await controller.decidePairing({
    pairing_id: "pair-9",
    code: "999999",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-9-r1"',
    operation_id: "op-9",
    action: "approve",
  });
  assert.deepEqual(outcome, { outcome: "unknown", operation: "op-9" });
  assert.equal(controller.state.pendingDecision?.request.operation_id, "op-9", "accepted reservation keeps the intent");
  // A later committed replay of the same operation resolves it.
  client.decidePairing = () => Promise.resolve<DecisionResult>({ kind: "committed", body: {} });
  assert.deepEqual(await controller.retryPendingDecision(), { outcome: "done" });
  assert.equal(controller.state.pendingDecision, null);
});

test("OPERATION_RECONCILIATION_REQUIRED keeps the identity and demands receipt recovery", async () => {
  const client = new FakeClient();
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-30", false);
  client.decidePairing = () => Promise.reject(new ApiError(409, "OPERATION_RECONCILIATION_REQUIRED", false));
  const outcome = await controller.decidePairing({
    pairing_id: "pair-10",
    code: "888888",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-10-r1"',
    operation_id: "op-10",
    action: "approve",
  });
  assert.deepEqual(outcome, { outcome: "reconciliation_required", operation: "op-10" });
  assert.equal(controller.state.pendingDecision?.request.operation_id, "op-10");
  client.operation = () => Promise.resolve(operationView("op-10", "completed"));
  const reconciled = await controller.reconcileDecision("op-10");
  assert.ok(reconciled);
  assert.equal(controller.state.pendingDecision, null, "receipt recovery resolves without a new operation");
});

test("unknown decision intent survives phase changes and retries with the same identity", async () => {
  const client = new FakeClient();
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-18", false);
  const request = {
    pairing_id: "pair-2",
    code: "654321",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-2-r3"',
    operation_id: "op-2",
    action: "reject" as const,
  };
  const decided: unknown[] = [];
  client.decidePairing = (input: PairingDecisionRequest) => {
    decided.push(input);
    return Promise.reject(new NetworkError());
  };
  await controller.decidePairing(request);
  assert.ok(controller.state.pendingDecision);
  assert.equal(controller.state.pendingDecision?.request.operation_id, "op-2");
  // A disconnect/reconnect cycle (degraded then verified) keeps the intent.
  client.sessionHandler = () => Promise.reject(new NetworkError());
  await controller.verify();
  assert.equal(controller.state.phase, "degraded");
  assert.equal(controller.state.pendingDecision?.request.operation_id, "op-2");
  client.sessionHandler = () => Promise.resolve(sessionInfo());
  await controller.verify();
  assert.equal(controller.state.phase, "verified");
  assert.equal(controller.state.pendingDecision?.request.operation_id, "op-2");
  // The explicit retry replays the exact original request and preconditions.
  client.decidePairing = (input: PairingDecisionRequest) => {
    decided.push(input);
    return Promise.resolve<DecisionResult>({ kind: "committed", body: {} });
  };
  const retried = await controller.retryPendingDecision();
  assert.deepEqual(retried, { outcome: "done" });
  assert.equal(decided.length, 2);
  assert.deepEqual(decided[0], decided[1]);
  assert.equal(controller.state.pendingDecision, null);
});

test("unknown revoke intent keeps its session target and clears honestly", async () => {
  const client = new FakeClient();
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-19", false);
  client.revokeSession = () => Promise.reject(new NetworkError());
  assert.equal(await controller.revokeSession("sess-peer", "peer"), "unknown");
  assert.equal(controller.state.pendingRevokes.length, 1);
  assert.equal(controller.state.pendingRevokes[0].sessionId, "sess-peer");
  // Degraded then verified keeps the natural-identity target.
  client.sessionHandler = () => Promise.reject(new NetworkError());
  await controller.verify();
  client.sessionHandler = () => Promise.resolve(sessionInfo());
  client.revokeSession = () => Promise.resolve();
  await controller.verify();
  assert.equal(controller.state.pendingRevokes[0].sessionId, "sess-peer");
  assert.equal(await controller.confirmRevokeResolved("sess-peer"), true);
  assert.equal(controller.state.pendingRevokes.length, 0);
  // A successful explicit same-id retry also clears the pending row.
  client.revokeSession = () => Promise.reject(new NetworkError());
  await controller.revokeSession("sess-peer", "peer");
  client.revokeSession = () => Promise.resolve();
  assert.equal(await controller.revokeSession("sess-peer", "peer"), "revoked");
  assert.equal(controller.state.pendingRevokes.length, 0);
});

test("logout and library change drop pending intents", async () => {
  const client = new FakeClient();
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-20", false);
  client.decidePairing = () => Promise.reject(new NetworkError());
  client.revokeSession = () => Promise.reject(new NetworkError());
  await controller.decidePairing({
    pairing_id: "pair-3",
    code: "111111",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-3-r1"',
    operation_id: "op-3",
    action: "approve",
  });
  await controller.revokeSession("sess-peer", "peer");
  assert.ok(controller.state.pendingDecision);
  assert.equal(controller.state.pendingRevokes.length, 1);
  // A different confirmed library identity drops the intents on verify; the
  // library switch also invalidates the transport even when the runtime
  // generation string is identical.
  const invalidatesBefore = client.calls.filter((call) => call === "invalidate").length;
  client.sessionHandler = () => Promise.resolve(sessionInfo("lib-2"));
  client.capabilitiesHandler = () => Promise.resolve(capabilities({ library_id: "lib-2", runtime_generation: "g1" }));
  await controller.verify();
  assert.equal(controller.state.phase, "verified");
  assert.equal(controller.state.pendingDecision, null);
  assert.equal(controller.state.pendingRevokes.length, 0);
  assert.ok(
    client.calls.filter((call) => call === "invalidate").length > invalidatesBefore,
    "library change must invalidate pending transport responses",
  );
  client.sessionHandler = () => Promise.resolve(sessionInfo());
  client.capabilitiesHandler = () => Promise.resolve(capabilities());
  await controller.verify();
  // Logout clears any newly recorded intents with the credential boundary.
  client.decidePairing = () => Promise.reject(new NetworkError());
  await controller.decidePairing({
    pairing_id: "pair-4",
    code: "222222",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-4-r1"',
    operation_id: "op-4",
    action: "approve",
  });
  await controller.logout();
  assert.equal(controller.state.pendingDecision, null);
});

test("routine same-authority recheck does not invalidate the transport", async () => {
  const client = new FakeClient();
  const store = new MemoryCredentialStore();
  store.write("token-21");
  const controller = new ShellController(client, store);
  await controller.verify();
  const invalidates = client.calls.filter((call) => call === "invalidate").length;
  assert.equal(invalidates, 1, "initial binding invalidates once");
  await controller.verify();
  await controller.verify();
  assert.equal(client.calls.filter((call) => call === "invalidate").length, invalidates);
  // Replacing the credential is an authority boundary.
  await controller.acceptPairing("token-22", false);
  assert.ok(client.calls.filter((call) => call === "invalidate").length > invalidates);
});

test("same-authority recheck keeps the verified screen phase mounted", async () => {
  const client = new FakeClient();
  const store = new MemoryCredentialStore();
  store.write("token-23");
  const controller = new ShellController(client, store);
  await controller.verify();
  assert.equal(controller.state.phase, "verified");
  const gate = { resolve: null as null | ((value: SessionInfo) => void) };
  client.sessionHandler = () =>
    new Promise((resolve) => {
      gate.resolve = resolve;
    });
  const recheck = controller.verify();
  await settle(2);
  assert.equal(controller.state.phase, "verified", "routine recheck must not flip to checking");
  gate.resolve?.(sessionInfo());
  await recheck;
  assert.equal(controller.state.phase, "verified");
});

test("delayed write success survives a routine recheck", async () => {
  const client = new FakeClient();
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-24", false);
  const gate = { resolve: null as null | ((value: DecisionResult) => void) };
  client.decidePairing = () =>
    new Promise((resolve) => {
      gate.resolve = resolve;
    });
  const decision = controller.decidePairing({
    pairing_id: "pair-5",
    code: "333333",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-5-r1"',
    operation_id: "op-5",
    action: "approve",
  });
  await controller.verify();
  gate.resolve?.({ kind: "committed", body: {} });
  assert.deepEqual(await decision, { outcome: "done" });
  assert.equal(controller.state.pendingDecision, null);
});

test("stale-generation ack or failure records the uncertain write while still authorized", async () => {
  const client = new FakeClient();
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-25", false);
  client.decidePairing = () => Promise.reject(new StaleResponseError());
  const outcome = await controller.decidePairing({
    pairing_id: "pair-6",
    code: "444444",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-6-r1"',
    operation_id: "op-6",
    action: "approve",
  });
  assert.deepEqual(outcome, { outcome: "unknown", operation: "op-6" });
  assert.equal(controller.state.pendingDecision?.request.operation_id, "op-6");
});

test("late stale failures after logout or replacement never reinsert pending intents", async () => {
  const client = new FakeClient();
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-26", false);
  const gate = { reject: null as null | ((reason: Error) => void) };
  client.decidePairing = () =>
    new Promise((_, reject) => {
      gate.reject = reject;
    });
  const decision = controller.decidePairing({
    pairing_id: "pair-7",
    code: "555555",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-7-r1"',
    operation_id: "op-7",
    action: "approve",
  });
  await settle(2);
  await controller.logout();
  assert.equal(client.credential, "", "logout drops the transport credential binding");
  gate.reject?.(new StaleResponseError());
  assert.deepEqual(await decision, { outcome: "unknown", operation: "op-7" });
  assert.equal(controller.state.pendingDecision, null);

  // The same guard holds for a replaced credential authority.
  await controller.acceptPairing("token-27", false);
  const gate2 = { reject: null as null | ((reason: Error) => void) };
  client.decidePairing = () =>
    new Promise((_, reject) => {
      gate2.reject = reject;
    });
  const decision2 = controller.decidePairing({
    pairing_id: "pair-8",
    code: "666666",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-8-r1"',
    operation_id: "op-8",
    action: "approve",
  });
  await settle(2);
  await controller.acceptPairing("token-28", false);
  gate2.reject?.(new NetworkError());
  assert.deepEqual(await decision2, { outcome: "unknown", operation: "op-8" });
  assert.equal(controller.state.pendingDecision, null);
});

test("logout with a non-SESSION_REVOKED 401 stays honestly unknown", async () => {
  const client = new FakeClient();
  client.ownRevokeHandler = () => Promise.reject(new ApiError(401, "SESSION_EXPIRED", false));
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-12a", false);
  await controller.logout();
  assert.equal(controller.state.notice, NOTICE.logoutAckUnknown);
});

test("a no-token verify during a pending logout cannot strand the old credential", async () => {
  const client = new FakeClient();
  const gate = { resolve: null as null | ((value: void) => void) };
  client.ownRevokeHandler = () =>
    new Promise<void>((resolve) => {
      gate.resolve = resolve;
    });
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-31", false);
  const logoutRun = controller.logout();
  await settle(2);
  // A visibility-triggered verify with no stored credential bumps the
  // verification-run counter without replacing any session.
  await controller.verify();
  assert.equal(controller.state.phase, "unauthenticated");
  gate.resolve?.();
  await logoutRun;
  await settle();
  assert.equal(controller.state.phase, "unauthenticated");
  assert.equal(client.credential, "", "shared transport must not retain the revoked token");
});

test("a late logout completion cannot clobber a newly connected session", async () => {
  const client = new FakeClient();
  const gate = { resolve: null as null | ((value: void) => void) };
  client.ownRevokeHandler = () =>
    new Promise<void>((resolve) => {
      gate.resolve = resolve;
    });
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-old", false);
  const logoutRun = controller.logout();
  await settle(2);
  // The user re-pairs and reaches a verified session while the old DELETE is pending.
  await controller.acceptPairing("token-new", false);
  assert.equal(controller.state.phase, "verified");
  const noticeBefore = controller.state.notice;
  gate.resolve?.();
  await logoutRun;
  await settle();
  assert.equal(controller.state.phase, "verified");
  assert.equal(controller.state.notice, noticeBefore);
  assert.equal(client.credential, "token-new");
  const sets = client.calls.filter((call) => call === "setCredential").length;
  assert.ok(sets >= 3, "setCredential was called for old token, cleared, and new token");
});

test("a failed remember-write leaves no old token to silently restore", async () => {
  const client = new FakeClient();
  const store = {
    token: "token-old" as string | null,
    storageAvailable: true,
    read() {
      return this.token;
    },
    write() {
      // removeItem succeeded, setItem failed: nothing remains stored.
      this.token = null;
      return false;
    },
    clear() {
      this.token = null;
    },
  };
  const first = new ShellController(client, store);
  await first.acceptPairing("token-new", true);
  assert.equal(first.state.phase, "verified");
  assert.equal(first.state.memoryOnly, true);
  assert.equal(store.read(), null);
  // A reload constructs a fresh controller; the old credential must not return.
  const second = new ShellController(new FakeClient(), store);
  await second.verify();
  assert.equal(second.state.phase, "unauthenticated");
  assert.equal(second.hasCredential(), false);
});

function eventGates(client: FakeClient) {
  const gates: { end: null | (() => void) }[] = [];
  client.eventsHandler = () => {
    const gate = { end: null as null | (() => void) };
    gates.push(gate);
    return {
      closed: new Promise<void>((resolve) => {
        gate.end = resolve;
      }),
      close() {},
    };
  };
  return gates;
}

test("a current stream stays the liveness owner across routine same-authority rechecks", async () => {
  const client = new FakeClient();
  const gates = eventGates(client);
  const store = new MemoryCredentialStore();
  store.write("token-33");
  const controller = new ShellController(client, store);
  try {
    await controller.verify();
    assert.equal(gates.length, 1);
    // Routine same-authority rechecks advance the verification-run counter but
    // must not invalidate the live subscription.
    await controller.verify();
    await controller.verify();
    assert.equal(gates.length, 1, "routine recheck reopened the live stream");
    const checks = client.calls.filter((call) => call === "session").length;
    gates[0].end?.();
    await settle();
    assert.ok(
      client.calls.filter((call) => call === "session").length > checks,
      "current stream close triggers a confirmed-state recheck without periodic polling",
    );
    assert.equal(controller.state.phase, "verified");
  } finally {
    controller.stop();
  }
});

test("late stream closes after stop, replacement or an authority change trigger no recheck", async () => {
  const client = new FakeClient();
  const gates = eventGates(client);
  const store = new MemoryCredentialStore();
  store.write("token-34");
  const controller = new ShellController(client, store);
  try {
    await controller.verify();
    assert.equal(gates.length, 1);
    controller.stop();
    let checks = client.calls.filter((call) => call === "session").length;
    gates[0].end?.();
    await settle();
    assert.equal(
      client.calls.filter((call) => call === "session").length,
      checks,
      "stopped subscription close must not recheck",
    );
    // A generation change replaces the subscription; the old one's late close
    // must not trigger a recheck.
    client.capabilitiesHandler = () => Promise.resolve(capabilities({ runtime_generation: "g9" }));
    await controller.verify();
    await settle();
    assert.equal(gates.length, 2, "replacement subscription expected");
    checks = client.calls.filter((call) => call === "session").length;
    gates[0].end?.();
    await settle();
    assert.equal(
      client.calls.filter((call) => call === "session").length,
      checks,
      "replaced subscription close must not recheck",
    );
    // An authority change (logout) closes the current subscription; its late
    // close must not recheck either.
    await controller.logout();
    checks = client.calls.filter((call) => call === "session").length;
    gates[1].end?.();
    await settle();
    assert.equal(
      client.calls.filter((call) => call === "session").length,
      checks,
      "authority-changed subscription close must not recheck",
    );
    assert.equal(controller.state.phase, "unauthenticated");
  } finally {
    controller.stop();
  }
});

test("an unverified mixed-library observation never retags a dispatched intent", async () => {
  const client = new FakeClient();
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-35", false);
  assert.equal(controller.state.capabilities?.library_id, "lib-1");
  // Dispatch a decision under confirmed lib-1, then let a routine verify see a
  // mixed session/capabilities observation (unverified, incompatible).
  const pendingRejects: ((reason: Error) => void)[] = [];
  client.decidePairing = () =>
    new Promise((_resolve, reject) => {
      pendingRejects.push(reject);
    });
  const decision = controller.decidePairing({
    pairing_id: "pair-13",
    code: "135713",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-13-r1"',
    operation_id: "op-13",
    action: "approve",
  });
  await settle(2);
  client.capabilitiesHandler = () => Promise.resolve(capabilities({ library_id: "lib-mixed" }));
  await controller.verify();
  assert.equal(controller.state.phase, "incompatible");
  // A NEW protected dispatch while incompatible is blocked locally: nothing is
  // sent, no unknown outcome is recorded, and the existing dispatched intent
  // is neither overwritten nor cleared.
  const decideCalls = client.calls.filter((call) => call.startsWith("decide:")).length;
  await assert.rejects(
    controller.decidePairing({
      pairing_id: "pair-13b",
      code: "135714",
      origin: "http://127.0.0.1:9270",
      etag: '"pairing-pair-13b-r1"',
      operation_id: "op-13b",
      action: "approve",
    }),
    (error: unknown): boolean => error instanceof DispatchBlockedError,
  );
  const readPending = (): PendingDecision | null => controller.state.pendingDecision;
  assert.equal(client.calls.filter((call) => call.startsWith("decide:")).length, decideCalls, "blocked dispatch reached the client");
  assert.ok(!readPending(), "no pending intent exists yet for the in-flight dispatch");
  // The ALREADY-DISPATCHED request keeps its recovery scope regardless of the
  // later phase change: its late failure records the original K identity.
  for (const reject of pendingRejects.splice(0)) reject(new NetworkError());
  assert.deepEqual(await decision, { outcome: "unknown", operation: "op-13" });
  const recorded = readPending();
  assert.ok(recorded, "dispatched request lost its recovery intent");
  assert.equal(recorded.request.operation_id, "op-13");
  assert.equal(recorded.libraryId, "lib-1", "intent must keep the dispatch-time confirmed library");
  // While still incompatible, an explicit retry of the recorded intent is
  // blocked locally without clearing or overwriting it.
  const decideCallsAfter = client.calls.filter((call) => call.startsWith("decide:")).length;
  await assert.rejects(controller.retryPendingDecision(), (error: unknown): boolean => error instanceof DispatchBlockedError);
  assert.equal(client.calls.filter((call) => call.startsWith("decide:")).length, decideCallsAfter);
  assert.equal(readPending()?.request.operation_id, "op-13", "blocked retry clobbered the pending intent");
  // Recovery on the genuine lib-1 authority retains and resolves the intent.
  client.capabilitiesHandler = () => Promise.resolve(capabilities());
  await controller.verify();
  assert.equal(controller.state.phase, "verified");
  const retained = readPending();
  assert.ok(retained, "confirmed-library intent lost across mixed observation");
  assert.equal(retained.request.operation_id, "op-13");
  client.operation = () => Promise.resolve(operationView("op-13", "completed"));
  const reconciled = await controller.reconcileDecision("op-13");
  assert.ok(reconciled);
  assert.equal(controller.state.pendingDecision, null);
});

test("protected dispatches are blocked outside the verified phase without client calls", async () => {
  const client = new FakeClient();
  const store = new MemoryCredentialStore();
  store.write("token-37");
  const controller = new ShellController(client, store);
  const request: PairingDecisionRequest = {
    pairing_id: "pair-block",
    code: "246810",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-block-r1"',
    operation_id: "op-block",
    action: "approve",
  };
  const expectBlocked = (phase: string) =>
    (async () => {
      const before = client.calls.length;
      await assert.rejects(controller.decidePairing(request), (error: unknown): boolean => {
        assert.ok(error instanceof DispatchBlockedError, phase + ": " + String(error));
        return true;
      });
      await assert.rejects(controller.revokeSession("sess-peer", "peer"), (error: unknown): boolean => {
        assert.ok(error instanceof DispatchBlockedError, phase + " revoke: " + String(error));
        return true;
      });
      assert.equal(client.calls.length, before, phase + ": blocked dispatch reached the client");
      assert.equal(controller.state.pendingDecision, null, phase + ": blocked dispatch created a pending intent");
      assert.equal(controller.state.pendingRevokes.length, 0, phase + ": blocked dispatch created a pending revoke");
    })();
  // checking: an in-flight initial verification.
  const gate = { resolve: null as null | ((value: SessionInfo) => void) };
  client.sessionHandler = () =>
    new Promise((resolve) => {
      gate.resolve = resolve;
    });
  const boot = controller.verify();
  await settle(2);
  assert.equal(controller.state.phase, "checking");
  await expectBlocked("checking");
  gate.resolve?.(sessionInfo());
  await boot;
  assert.equal(controller.state.phase, "verified");
  // degraded.
  client.sessionHandler = () => Promise.reject(new NetworkError());
  await controller.verify();
  assert.equal(controller.state.phase, "degraded");
  await expectBlocked("degraded");
  // incompatible.
  client.capabilitiesHandler = () => Promise.resolve(capabilities({ api_version: "2.0" }));
  client.sessionHandler = () => Promise.resolve(sessionInfo());
  await controller.verify();
  assert.equal(controller.state.phase, "incompatible");
  await expectBlocked("incompatible");
  // unauthenticated.
  await controller.logout();
  assert.equal(controller.state.phase, "unauthenticated");
  await expectBlocked("unauthenticated");
});

test("a blocked retry leaves an existing pending intent untouched", async () => {
  const client = new FakeClient();
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-38", false);
  client.decidePairing = () => Promise.reject(new NetworkError());
  await controller.decidePairing({
    pairing_id: "pair-keep",
    code: "112233",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-keep-r1"',
    operation_id: "op-keep",
    action: "approve",
  });
  assert.ok(controller.state.pendingDecision);
  client.sessionHandler = () => Promise.reject(new NetworkError());
  await controller.verify();
  assert.equal(controller.state.phase, "degraded");
  await assert.rejects(controller.retryPendingDecision(), (error: unknown): boolean => error instanceof DispatchBlockedError);
  assert.equal(controller.state.pendingDecision?.request.operation_id, "op-keep", "blocked retry clobbered the pending intent");
});

test("an unverified mixed-library observation never retags a dispatched revoke target", async () => {
  const client = new FakeClient();
  const controller = new ShellController(client, new MemoryCredentialStore());
  await controller.acceptPairing("token-36", false);
  const gate = { reject: null as null | ((reason: Error) => void) };
  client.revokeSession = () =>
    new Promise((_resolve, reject) => {
      gate.reject = reject;
    });
  const revoke = controller.revokeSession("sess-peer", "peer");
  await settle(2);
  client.capabilitiesHandler = () => Promise.resolve(capabilities({ library_id: "lib-mixed" }));
  await controller.verify();
  assert.equal(controller.state.phase, "incompatible");
  gate.reject?.(new NetworkError());
  assert.equal(await revoke, "unknown");
  assert.equal(controller.state.pendingRevokes.length, 1);
  assert.equal(controller.state.pendingRevokes[0].libraryId, "lib-1");
  assert.equal(controller.state.pendingRevokes[0].sessionId, "sess-peer");
  client.capabilitiesHandler = () => Promise.resolve(capabilities());
  await controller.verify();
  assert.equal(controller.state.phase, "verified");
  assert.equal(controller.state.pendingRevokes.length, 1, "natural revoke target lost across mixed observation");
  client.listHandler = () => Promise.resolve([]);
  assert.equal(await controller.confirmRevokeResolved("sess-peer"), true);
  assert.equal(controller.state.pendingRevokes.length, 0);
});

test("event stream termination triggers a confirmed-state recheck", async () => {
  const client = new FakeClient();
  const gate = { end: null as null | (() => void) };
  client.eventsHandler = () => ({
    closed: new Promise<void>((resolve) => {
      gate.end = resolve;
    }),
    close() {},
  });
  const store = new MemoryCredentialStore();
  store.write("token-16");
  const controller = new ShellController(client, store);
  try {
    await controller.verify();
    assert.ok(client.calls.includes("subscribeEvents"));
    client.sessionHandler = () => Promise.reject(new NetworkError());
    gate.end?.();
    await settle();
    assert.equal(controller.state.phase, "degraded");
  } finally {
    controller.stop();
  }
});

test("instantly failing event streams recover with a bounded backoff", async () => {
  const client = new FakeClient();
  // Every stream closes immediately: the hazard that previously starved timers.
  client.eventsHandler = () => ({ closed: Promise.resolve(), close() {} });
  const store = new MemoryCredentialStore();
  store.write("token-17");
  const controller = new ShellController(client, store);
  try {
    await controller.verify();
    await settle(6);
    assert.equal(controller.state.phase, "verified");
    await new Promise((resolve) => setTimeout(resolve, 2600));
    assert.equal(controller.state.phase, "verified");
    const subscribes = client.calls.filter((call) => call === "subscribeEvents").length;
    assert.ok(subscribes >= 2, "liveness watcher recovers after a failed stream");
    assert.ok(subscribes <= 4, "rapid stream failure is bounded to a slow recheck pace, got " + subscribes);
  } finally {
    controller.stop();
  }
});

test("native attachment verifies and rechecks without a JavaScript credential; logout revokes before disposing Go authority", async () => {
  const fake = new FakeClient(), store = new MemoryCredentialStore();
  const native = Object.assign(fake, {
    async attachSession() { fake.calls.push("attachNative"); return { attachment_id: "public-attachment", session: sessionInfo(), capabilities: capabilities(), public_origin: "http://127.0.0.1:9270" }; },
    async disposeSession() { fake.calls.push("disposeNative"); },
  });
  const controller = new ShellController(native, store);
  await controller.attachNativeSession();
  assert.equal(controller.state.phase, "verified"); assert.equal(controller.hasCredential(), true);
  assert.equal(fake.credential, ""); assert.equal(store.read(), null);
  const authority = controller.captureDispatchAuthority();
  await controller.verify(); controller.requireVerifiedDispatch(authority);
  assert.equal(fake.calls.filter((call) => call === "setCredential").length, 0);
  await controller.logout();
  assert.equal(controller.state.phase, "unauthenticated"); assert.equal(controller.hasCredential(), false);
  assert.ok(fake.calls.indexOf("revokeOwnSession") < fake.calls.indexOf("disposeNative"));
  assert.equal(controller.state.notice, NOTICE.logoutConfirmed);
  controller.stop();
});

test("native disconnection retains its attachment, but a current-session 401 disposes it and invalidates captured authority", async () => {
  const fake = new FakeClient(); let disposed = false;
  const native = Object.assign(fake, {
    async attachSession() { return { attachment_id: "public-attachment", session: sessionInfo(), capabilities: capabilities(), public_origin: "http://127.0.0.1:9270" }; },
    async disposeSession() { disposed = true; },
  });
  const controller = new ShellController(native, new MemoryCredentialStore());
  await controller.attachNativeSession(); const authority = controller.captureDispatchAuthority();
  fake.sessionHandler = async () => { throw new NetworkError(); };
  await controller.verify(); assert.equal(controller.state.phase, "degraded"); assert.equal(controller.hasCredential(), true); assert.equal(disposed, false);
  fake.sessionHandler = async () => { throw new ApiError(401, "SESSION_REVOKED", false); };
  await controller.verify(); assert.equal(controller.state.phase, "unauthenticated"); assert.equal(controller.hasCredential(), false); assert.equal(disposed, true);
  assert.throws(() => controller.requireVerifiedDispatch(authority), DispatchBlockedError);
  assert.equal(fake.credential, ""); controller.stop();
});

test("a native approve whose committed body is unreadable keeps the original K pending", async () => {
  const fake = new FakeClient(), store = new MemoryCredentialStore();
  fake.decidePairing = () => Promise.reject(new UncertainResponseError(new Error("unreadable native success body")));
  const controller = new ShellController(fake, store);
  await controller.acceptPairing("owner-token", true);
  assert.equal(controller.state.phase, "verified");
  const request = {
    pairing_id: "pair-native",
    code: "424242",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-native-r1"',
    operation_id: "op-native-1",
    action: "approve" as const,
  };
  const outcome = await controller.decidePairing(request);
  assert.deepEqual(outcome, { outcome: "unknown", operation: "op-native-1" });
  assert.deepEqual(controller.state.pendingDecision?.request, request);
  const retried = await controller.retryPendingDecision();
  assert.deepEqual(retried, { outcome: "unknown", operation: "op-native-1" });
  assert.deepEqual(controller.state.pendingDecision?.request, request);
  controller.stop();
});

test("native local forget failure stays visible through disposal and no-token verify; cleanup retry clears warning", async () => {
  const fake = new FakeClient(); let allow = false, disposals = 0;
  fake.ownRevokeHandler = async () => { throw new ApiError(500, "NATIVE_LOCAL_FORGET_FAILED", true); };
  const native = Object.assign(fake, {
    async attachSession() { return { attachment_id: "native", session: sessionInfo(), capabilities: capabilities(), public_origin: "http://127.0.0.1:9270" }; },
    async disposeSession() { disposals++; if (!allow) throw new ApiError(500, "NATIVE_LOCAL_FORGET_FAILED", true); },
  });
  const controller = new ShellController(native, new MemoryCredentialStore());
  await controller.attachNativeSession(); await controller.logout();
  assert.equal(controller.state.phase, "unauthenticated"); assert.equal(controller.hasCredential(), false);
  assert.equal(controller.state.localCleanupPending, true); assert.equal(controller.state.notice, NOTICE.logoutLocalFailure);
  await controller.verify(); assert.equal(controller.state.notice, NOTICE.logoutLocalFailure);
  await controller.retryLocalCleanup(); assert.equal(controller.state.localCleanupPending, true);
  allow = true; await controller.retryLocalCleanup();
  assert.equal(controller.state.localCleanupPending, false); assert.equal(controller.state.notice, NOTICE.logoutAckUnknown); assert.equal(disposals, 3);
  controller.stop();
});

test("nativePairingNotice maps every typed onboarding failure at poll and adoption time", () => {
  const codes: Array<[string, RegExp]> = [
    ["NATIVE_PAIRING_ATTEMPT_STALE", /무효화/],
    ["NATIVE_PAIRING_ALREADY_ADOPTED", /이미 완료/],
    ["NATIVE_PAIRING_ADOPT_FAILED", /채택이 실패/],
    ["NATIVE_CREDENTIAL_STORE_FAILED", /저장하지 못했습니다/],
    ["NATIVE_CREDENTIAL_STORE_UNCERTAIN", /확인할 수 없습니다/],
    ["NATIVE_LIBRARY_MISMATCH", /서재.*일치하지 않/],
    ["NATIVE_API_INCOMPATIBLE", /지원하지 않는 서비스 계약/],
    ["NATIVE_CREDENTIAL_MISMATCHED", /결속과 일치하지 않/],
    ["NATIVE_DAEMON_UNAVAILABLE", /로컬 서비스에 연결할 수 없/],
  ];
  for (const [code, pattern] of codes) {
    assert.match(nativePairingNotice(new ApiError(409, code, false)) ?? "", pattern);
  }
  assert.equal(nativePairingNotice(new ApiError(0, "NETWORK_ERROR", true)), null);
  assert.equal(nativePairingNotice(new Error("not typed")), null);
});

test("nativeDaemonStateLabel labels every signed discovery state honestly", () => {
  assert.match(nativeDaemonStateLabel("verified"), /확인되었습니다/);
  assert.match(nativeDaemonStateLabel("uninitialized"), /초기화되지 않/);
  assert.match(nativeDaemonStateLabel("daemon_stopped"), /실행 중이 아닙니다/);
  assert.match(nativeDaemonStateLabel("discovery_corrupt"), /신뢰할 수 없/);
  assert.match(nativeDaemonStateLabel("identity_mismatch"), /일치하지 않/);
  assert.match(nativeDaemonStateLabel("profile_error"), /읽을 수 없/);
  assert.match(nativeDaemonStateLabel("FEATURE_NOT_READY"), /제공하지 않/);
  assert.match(nativeDaemonStateLabel("anything-else"), /알 수 없/);
});

test("native no-credential boot maps to the plain unauthenticated state", async () => {
  const fake = new FakeClient();
  fake.sessionHandler = async () => { throw new ApiError(401, "NATIVE_NO_CREDENTIAL", false); };
  fake.capabilitiesHandler = async () => { throw new ApiError(401, "NATIVE_NO_CREDENTIAL", false); };
  const native = Object.assign(fake, {
    async attachSession() { throw new ApiError(401, "NATIVE_NO_CREDENTIAL", false); },
  });
  const controller = new ShellController(native, new MemoryCredentialStore());
  await controller.attachNativeSession();
  assert.equal(controller.state.phase, "unauthenticated");
  assert.equal(controller.state.notice, NOTICE.unauthenticated);
  controller.stop();
});

test("verify on a detached native transport reattaches instead of dead-ending", async () => {
  const fake = new FakeClient();
  let attaches = 0;
  const native = Object.assign(fake, {
    async attachSession() { attaches++; return { attachment_id: "native-" + attaches, session: sessionInfo(), capabilities: capabilities(), public_origin: "http://127.0.0.1:9270" }; },
  });
  const controller = new ShellController(native, new MemoryCredentialStore());
  await controller.verify();
  assert.equal(attaches, 1);
  assert.equal(controller.state.phase, "verified");
  controller.stop();
});

test("controller adoptPairing and abandonPairing map truthful outcomes", async () => {
  const fake = new FakeClient();
  const adopted: string[] = [];
  const abandoned: string[] = [];
  let abandonOutcome: "ok" | "already" | "failed" | "stale" = "ok";
  const native = Object.assign(fake, {
    async attachSession() { return { attachment_id: "native", session: sessionInfo(), capabilities: capabilities(), public_origin: "http://127.0.0.1:9270" }; },
    async adoptPairing(pairingId: string) { adopted.push(pairingId); return { adopted: true as const, durability: "confirmed" as const }; },
    async abandonPairing(pairingId: string) {
      abandoned.push(pairingId);
      if (abandonOutcome === "already") throw new ApiError(409, "NATIVE_PAIRING_ALREADY_ADOPTED", false);
      if (abandonOutcome === "failed") throw new ApiError(409, "NATIVE_PAIRING_ADOPT_FAILED", false);
      if (abandonOutcome === "stale") throw new ApiError(409, "NATIVE_PAIRING_ATTEMPT_STALE", false);
    },
  });
  const controller = new ShellController(native, new MemoryCredentialStore());
  const adoption = await controller.adoptPairing("p1");
  assert.equal(adoption.adopted, true);
  assert.deepEqual(adopted, ["p1"]);
  assert.equal(await controller.abandonPairing("p1"), "abandoned");
  abandonOutcome = "already";
  assert.equal(await controller.abandonPairing("p1"), "already-adopted");
  abandonOutcome = "failed";
  assert.equal(await controller.abandonPairing("p1"), "adopt-failed");
  abandonOutcome = "stale";
  assert.equal(await controller.abandonPairing("p1"), "abandoned");
  assert.deepEqual(abandoned, ["p1", "p1", "p1", "p1"]);
  controller.stop();
});

// --- TASK-003-05 M2 C2: native foreground recheck idempotence and pin errors ---
// These drive the REAL NativeHaeryongClient era machinery (not just helper
// mappings): a held poll captures its era at call time and survives only when
// a no-authority recheck stops invalidating the transport.

class NativeScenarioPort {
  calls: string[] = [];
  credentialState: "none" | "live" | "revoked" = "none";
  attachOverride: "" | "logged-out" = "";
  attachFailure: unknown | null = null;
  forgetFails = false;
  pollGate: Promise<void> | null = null;
  pollCalls = 0;

  async call(operation: string, _payload: Record<string, unknown>): Promise<unknown> {
    this.calls.push(operation);
    switch (operation) {
      case "attachSession": {
        if (this.attachFailure instanceof Error || this.attachFailure instanceof ApiError) throw this.attachFailure;
        if (this.attachOverride === "logged-out") throw new ApiError(401, "NATIVE_SESSION_LOGGED_OUT", false);
        if (this.credentialState === "none") throw new ApiError(401, "NATIVE_NO_CREDENTIAL", false);
        if (this.credentialState === "revoked") throw new ApiError(401, "SESSION_REVOKED", false);
        return { attachment_id: "native-1", session: sessionInfo(), capabilities: capabilities(), public_origin: "http://127.0.0.1:9270" };
      }
      case "currentSession":
        // Models another device revoking the Go-held credential while the
        // attachment is live; the token and file stay until an explicit
        // logout forgets them.
        if (this.credentialState === "revoked") throw new ApiError(401, "SESSION_REVOKED", false);
        return sessionInfo();
      case "capabilities":
        return capabilities();
      case "pair":
        return { pairing_id: "pair-c2", code: "246810", expires_at: new Date(Date.now() + 300_000).toISOString() };
      case "pollPairing": {
        this.pollCalls++;
        if (this.pollGate) await this.pollGate;
        return { state: "approved", session: { session_id: "sess-1", principal_id: "owner", library_id: "lib-1", device_label: "tester" } };
      }
      case "adoptPairing":
        // Adoption installs the captured endpoint/bearer pair together.
        this.credentialState = "live";
        return { adopted: true, durability: "confirmed" };
      case "disposeSession":
        if (this.forgetFails) throw new ApiError(500, "NATIVE_LOCAL_FORGET_FAILED", true);
        return;
      case "revokeOwnSession":
        return;
      case "subscribeEvents":
        return { closed: new Promise<void>(() => {}), close() {} };
      default:
        throw new Error("unused operation in scenario: " + operation);
    }
  }
}

function nativeScenarioController(port: NativeScenarioPort): ShellController {
  return new ShellController(new NativeHaeryongClient(port as unknown as NativePort), new MemoryCredentialStore());
}

test("native startup stays checking until the actual remembered-session attachment settles", async () => {
  const port = new NativeScenarioPort();
  port.credentialState = "live";
  let release!: () => void;
  const held = new Promise<void>((resolve) => { release = resolve; });
  const call = port.call.bind(port);
  port.call = async (operation, payload) => {
    if (operation === "attachSession") await held;
    return call(operation, payload);
  };
  const controller = nativeScenarioController(port);
  assert.equal(controller.state.phase, "checking");
  assert.equal(controller.state.notice, NOTICE.checking);
  const attaching = controller.attachNativeSession();
  assert.equal(controller.state.phase, "checking");
  assert.throws(() => controller.requireVerifiedDispatch(), DispatchBlockedError);
  release();
  await attaching;
  assert.equal(controller.state.phase, "verified");
  controller.stop();
});

test("a native no-credential foreground recheck keeps a still-owned held poll alive", async () => {
  const port = new NativeScenarioPort();
  const controller = nativeScenarioController(port);
  await controller.attachNativeSession();
  assert.equal(controller.state.phase, "unauthenticated");
  assert.equal(controller.state.notice, NOTICE.unauthenticated);
  // A REAL era-captured native poll, held in flight like a foreground switch.
  const gate = { resolve: null as null | (() => void) };
  port.pollGate = new Promise<void>((resolve) => { gate.resolve = resolve; });
  const poll = controller.pollPairing("pair-c2");
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(port.pollCalls, 1);
  // The App foreground handler path calls verify() while the poll is held.
  await controller.verify();
  assert.equal(controller.state.phase, "unauthenticated");
  assert.equal(port.calls.filter((call) => call === "attachSession").length, 2);
  port.pollGate = null;
  gate.resolve!();
  const result = await poll;
  assert.equal(result.state, "approved");
  await controller.adoptPairing("pair-c2");
  await controller.attachNativeSession();
  assert.equal(controller.state.phase, "verified");
  assert.equal(port.calls.includes("disposeSession"), false);
  controller.stop();
});

test("a repeated revoked remembered credential recheck keeps the new pairing poll alive; the first loss still cleans and stales old work", async () => {
  const port = new NativeScenarioPort();
  port.credentialState = "live";
  const controller = nativeScenarioController(port);
  await controller.attachNativeSession();
  assert.equal(controller.state.phase, "verified");
  const authority = controller.captureDispatchAuthority();
  // An in-flight poll from the live era, then another device revokes A; the
  // retained dead credential keeps answering attach with SESSION_REVOKED.
  let release!: () => void;
  port.pollGate = new Promise<void>((resolve) => { release = resolve; });
  const oldPoll = controller.pollPairing("pair-old");
  await new Promise((resolve) => setTimeout(resolve, 0));
  port.credentialState = "revoked";
  await controller.verify();
  assert.equal(controller.state.phase, "unauthenticated");
  assert.equal(controller.hasCredential(), false);
  assert.equal(port.calls.includes("disposeSession"), true);
  assert.throws(() => controller.requireVerifiedDispatch(authority), DispatchBlockedError);
  port.pollGate = null;
  release();
  await assert.rejects(() => oldPoll, StaleResponseError);
  // The owner pairs again; repeated no-authority rechecks of the retained
  // revoked A must be idempotent and leave the new held poll owned.
  let releaseB!: () => void;
  port.pollGate = new Promise<void>((resolve) => { releaseB = resolve; });
  const newPoll = controller.pollPairing("pair-new");
  await new Promise((resolve) => setTimeout(resolve, 0));
  const disposesBefore = port.calls.filter((call) => call === "disposeSession").length;
  await controller.verify();
  assert.equal(controller.state.phase, "unauthenticated");
  assert.equal(controller.state.notice, NOTICE.revoked);
  assert.equal(port.calls.filter((call) => call === "disposeSession").length, disposesBefore);
  port.pollGate = null;
  releaseB();
  const approved = await newPoll;
  assert.equal(approved.state, "approved");
  await controller.adoptPairing("pair-new");
  await controller.attachNativeSession();
  assert.equal(controller.state.phase, "verified");
  controller.stop();
});

test("permanent native attach pin errors surface an accurate incompatible state without pairing or authority", async () => {
  const cases: Array<[string, RegExp]> = [
    ["NATIVE_API_INCOMPATIBLE", /지원하지 않는 서비스 계약/],
    ["NATIVE_LIBRARY_MISMATCH", /서재가 이 프로필과 일치하지 않/],
    ["NATIVE_CREDENTIAL_MISMATCHED", /결속과 일치하지 않/],
  ];
  for (const [code, pattern] of cases) {
    const port = new NativeScenarioPort();
    port.attachFailure = new ApiError(409, code, false);
    const controller = nativeScenarioController(port);
    await controller.attachNativeSession();
    assert.equal(controller.state.phase, "incompatible");
    assert.match(controller.state.notice, pattern);
    assert.equal(controller.hasCredential(), false);
    assert.throws(() => controller.requireVerifiedDispatch(), DispatchBlockedError);
    assert.equal(port.calls.includes("pair"), false);
    controller.stop();
  }
});

test("native attach network failure and generic service errors keep the transient degraded mapping", async () => {
  const port = new NativeScenarioPort();
  port.attachFailure = new NetworkError();
  const controller = nativeScenarioController(port);
  await controller.attachNativeSession();
  assert.equal(controller.state.phase, "degraded");
  assert.equal(controller.state.notice, NOTICE.disconnected);
  port.attachFailure = new ApiError(500, "INTERNAL", false);
  await controller.verify();
  assert.equal(controller.state.phase, "degraded");
  assert.equal(controller.state.notice, NOTICE.serviceRecovering);
  controller.stop();
});

test("an already-no-authority logged-out recheck keeps the local-cleanup obligation visible and never reattaches over it", async () => {
  const port = new NativeScenarioPort();
  port.credentialState = "live";
  const controller = nativeScenarioController(port);
  await controller.attachNativeSession();
  assert.equal(controller.state.phase, "verified");
  port.forgetFails = true;
  port.credentialState = "none";
  await controller.logout();
  assert.equal(controller.state.localCleanupPending, true);
  // verify() is guarded by the cleanup obligation: no attach happens at all.
  const attachesBefore = port.calls.filter((call) => call === "attachSession").length;
  await controller.verify();
  assert.equal(port.calls.filter((call) => call === "attachSession").length, attachesBefore);
  // A direct attach observes the Go logged-out state; the idempotent branch
  // keeps the pending obligation and its truthful notice.
  port.attachOverride = "logged-out";
  await controller.attachNativeSession();
  assert.equal(controller.state.phase, "unauthenticated");
  assert.equal(controller.state.localCleanupPending, true);
  assert.equal(controller.state.notice, NOTICE.logoutLocalFailure);
  controller.stop();
});

// --- TASK-003-05 M3 C2: runtime cleanup alerts ---

test("runtime cleanup alerts record exact paths, dedupe, dismiss and survive a same-authority degraded phase", async () => {
  const fake = new FakeClient();
  const controller = new ShellController(fake, new MemoryCredentialStore());
  await controller.acceptPairing("owner-token", true);
  assert.equal(controller.state.phase, "verified");
  const authority = controller.captureDispatchAuthority();
  const reported = controller.reportRuntimeCleanupFailure(
    { code: "NATIVE_CLEANUP_FAILED", details: { temp: "/tmp/.haeryong-abc.partial" } },
    authority,
  );
  assert.equal(reported, true);
  assert.deepEqual(controller.state.cleanupAlerts, ["/tmp/.haeryong-abc.partial"]);
  // A different path joins without a fixed limit; the same path dedupes.
  controller.reportRuntimeCleanupFailure({ code: "NATIVE_CLEANUP_FAILED", details: { temp: "/tmp/.haeryong-def.partial" } }, authority);
  controller.reportRuntimeCleanupFailure({ code: "NATIVE_CLEANUP_FAILED", details: { temp: "/tmp/.haeryong-abc.partial" } }, authority);
  assert.deepEqual(controller.state.cleanupAlerts, ["/tmp/.haeryong-abc.partial", "/tmp/.haeryong-def.partial"]);
  // An unobserved abort reply (no path) still records an honest entry.
  controller.reportRuntimeCleanupFailure({ code: "NETWORK_ERROR" }, authority);
  assert.equal(controller.state.cleanupAlerts.length, 3);
  // A temporarily degraded phase under the SAME authority keeps the alerts.
  fake.sessionHandler = async () => { throw new NetworkError(); };
  await controller.verify();
  assert.equal(controller.state.phase, "degraded");
  assert.equal(controller.state.cleanupAlerts.length, 3);
  // Dismissal is informational only and never claims the cleanup done.
  controller.dismissRuntimeCleanupAlert("/tmp/.haeryong-abc.partial");
  assert.deepEqual(controller.state.cleanupAlerts, ["/tmp/.haeryong-def.partial", ""]);
  controller.stop();
});

test("a replaced authority drops late cleanup reports and every authority transition clears shown alerts", async () => {
  const fake = new FakeClient();
  const controller = new ShellController(fake, new MemoryCredentialStore());
  await controller.acceptPairing("owner-token", true);
  const oldAuthority = controller.captureDispatchAuthority();
  controller.reportRuntimeCleanupFailure({ code: "NATIVE_CLEANUP_FAILED", details: { temp: "/tmp/.haeryong-old.partial" } }, oldAuthority);
  assert.equal(controller.state.cleanupAlerts.length, 1);
  // A generation change clears the shown alerts with the old generation's data.
  fake.capabilitiesHandler = async () => capabilities({ runtime_generation: "g2" });
  await controller.verify();
  assert.equal(controller.state.cleanupAlerts.length, 0);
  // A late report from the replaced identity is dropped, never reinserted.
  assert.equal(controller.reportRuntimeCleanupFailure({ code: "NATIVE_CLEANUP_FAILED", details: { temp: "/tmp/.haeryong-old.partial" } }, oldAuthority), false);
  assert.equal(controller.state.cleanupAlerts.length, 0);
  // The fresh authority can report its own path.
  const fresh = controller.captureDispatchAuthority();
  assert.equal(controller.reportRuntimeCleanupFailure({ code: "NATIVE_CLEANUP_FAILED", details: { temp: "/tmp/.haeryong-new.partial" } }, fresh), true);
  // A credential rebinding clears the alerts again.
  await controller.acceptPairing("other-token", true);
  assert.equal(controller.state.cleanupAlerts.length, 0);
  assert.equal(controller.reportRuntimeCleanupFailure({ code: "NATIVE_CLEANUP_FAILED", details: { temp: "/x" } }, fresh), false);
  controller.stop();
});

test("Lifecycle consumers share the controller event stream and old authority hints are discarded", async () => {
 const client=new FakeClient();let hint:(()=>void)|undefined,streams=0;
 client.eventsHandler=()=>{streams++;return {closed:new Promise<void>(()=>{}),onInvalidation(listener){hint=listener;return()=>{};},close(){}};};
 const store=new MemoryCredentialStore();store.write("token");
 const controller=new ShellController(client,store);await controller.verify();
 let a=0,b=0;const offA=controller.subscribeInvalidation(()=>a++),offB=controller.subscribeInvalidation(()=>b++);
 hint?.();assert.equal(a,1);assert.equal(b,1);assert.equal(streams,1);
 offA();hint?.();assert.equal(a,1);assert.equal(b,2);offB();controller.stop();hint?.();assert.equal(b,2);
});
