import test from "node:test";
import assert from "node:assert/strict";
import { NOTICE, ShellController } from "../../client/controller.ts";
import { MemoryCredentialStore } from "../../client/store.ts";
import { ApiError, StaleResponseError } from "../../client/types.ts";
import type {
  Capabilities,
  ConfigurationCommitView,
  ConfigurationPlanView,
  ConfigurationView,
  CredentialAware,
  DeviceSession,
  EventSubscription,
  HaeryongClient,
  OperationView,
  PairingView,
  SessionInfo,
} from "../../client/types.ts";
import {
  SECURITY_POLICY_COPY,
  SecurityPolicyFlow,
  calculateImmediateExpiryPreview,
  describeConnectionSecurity,
  describePairingDecisionConflict,
  parsePolicySecondsInput,
  securityPolicyAuthorityKey,
  validateSecurityPolicyDraft,
} from "./security-policy.ts";

function makeDevice(overrides: Partial<DeviceSession> = {}): DeviceSession {
  return {
    session_id: "sess-current",
    device_label: "Owner MacBook",
    origin: "http://127.0.0.1:9270",
    state: "active",
    created_at: "2026-11-05T00:00:00Z",
    last_seen_at: "2026-11-05T00:10:00Z",
    expires_at: null,
    revision: 1,
    etag: '"s1"',
    ...overrides,
  };
}

function makeConfig(revision = 4, idle: number | null = null, absolute: number | null = null, remember = true): ConfigurationView {
  return {
    config_revision: revision,
    etag: `"c${revision}"`,
    configuration: {
      projects: {},
      security: {
        remember_device_default: remember,
        session_idle_seconds: idle,
        session_absolute_seconds: absolute,
      },
    },
  };
}

test("parsePolicySecondsInput and validateSecurityPolicyDraft enforce null or positive integers", () => {
  assert.deepEqual(parsePolicySecondsInput(""), { ok: true, value: null });
  assert.deepEqual(parsePolicySecondsInput("   "), { ok: true, value: null });
  assert.deepEqual(parsePolicySecondsInput("600"), { ok: true, value: 600 });
  assert.equal(parsePolicySecondsInput("0").ok, false);
  assert.equal(parsePolicySecondsInput("-10").ok, false);
  assert.equal(parsePolicySecondsInput("12.5").ok, false);
  assert.equal(parsePolicySecondsInput("01").ok, false);
  assert.equal(parsePolicySecondsInput("abc").ok, false);

  const valid = validateSecurityPolicyDraft(false, "300", "");
  assert.deepEqual(valid, {
    ok: true,
    draft: {
      remember_device_default: false,
      session_idle_seconds: 300,
      session_absolute_seconds: null,
    },
  });

  const invalidIdle = validateSecurityPolicyDraft(true, "0", "3600");
  assert.equal(invalidIdle.ok, false);
  if (!invalidIdle.ok) assert.match(invalidIdle.reason, /유휴 만료 시간/);

  const invalidAbs = validateSecurityPolicyDraft(true, "", "-5");
  assert.equal(invalidAbs.ok, false);
  if (!invalidAbs.ok) assert.match(invalidAbs.reason, /절대 만료 시간/);
});

test("calculateImmediateExpiryPreview identifies current and peer active devices that would immediately expire", () => {
  const nowMs = Date.parse("2026-11-05T01:00:00Z");
  const devices: DeviceSession[] = [
    makeDevice({
      session_id: "sess-current",
      device_label: "Current Laptop",
      created_at: "2026-11-05T00:00:00Z", // age 3600s
      last_seen_at: "2026-11-05T00:55:00Z", // idle 300s
      state: "active",
    }),
    makeDevice({
      session_id: "sess-peer",
      device_label: "Peer Tablet",
      created_at: "2026-11-05T00:50:00Z", // age 600s
      last_seen_at: "2026-11-05T00:40:00Z", // idle 1200s
      state: "active",
    }),
    makeDevice({
      session_id: "sess-revoked",
      device_label: "Old Phone",
      created_at: "2026-11-04T00:00:00Z",
      last_seen_at: "2026-11-04T00:00:00Z",
      state: "revoked",
    }),
  ];

  // Peer expires by idle (600s <= 1200s), current stays valid (300s < 600s).
  const peerOnly = calculateImmediateExpiryPreview(
    devices,
    "sess-current",
    { remember_device_default: true, session_idle_seconds: 600, session_absolute_seconds: null },
    nowMs,
  );
  assert.equal(peerOnly.activeCount, 2);
  assert.equal(peerOnly.currentDeviceAffected, false);
  assert.equal(peerOnly.affectedDevices.length, 1);
  assert.equal(peerOnly.affectedDevices[0]?.session_id, "sess-peer");
  assert.deepEqual(peerOnly.affectedDevices[0]?.reasons, ["idle"]);
  assert.match(peerOnly.summary, /현재 장치는 유지/);

  // Current device expires by absolute (3600s >= 1800s) and peer expires by idle.
  const bothAffected = calculateImmediateExpiryPreview(
    devices,
    "sess-current",
    { remember_device_default: true, session_idle_seconds: 600, session_absolute_seconds: 1800 },
    nowMs,
  );
  assert.equal(bothAffected.currentDeviceAffected, true);
  assert.equal(bothAffected.affectedDevices.length, 2);
  const currentEntry = bothAffected.affectedDevices.find((d) => d.session_id === "sess-current");
  assert.equal(currentEntry?.isCurrentDevice, true);
  assert.deepEqual(currentEntry?.reasons, ["absolute"]);
  assert.equal(bothAffected.estimateNote, SECURITY_POLICY_COPY.estimateNote);
  assert.equal(bothAffected.recoveryNote, SECURITY_POLICY_COPY.recoveryNote);
});

test("describeConnectionSecurity and describePairingDecisionConflict report truthful transport, storage, and conflict states", () => {
  const loopback = describeConnectionSecurity({
    origin: "http://127.0.0.1:9270",
    nativeVariant: false,
    storageAvailable: true,
    remember: true,
    memoryOnly: false,
    verified: true,
  });
  assert.match(loopback.transportNote, /로컬 루프백 신뢰 연결/);
  assert.match(loopback.storageNote, /기억된 장치 연결/);

  const privateHttpsMemory = describeConnectionSecurity({
    origin: "https://haeryong.tailnet.ts.net",
    nativeVariant: false,
    storageAvailable: true,
    remember: false,
    memoryOnly: true,
    verified: true,
  });
  assert.match(privateHttpsMemory.transportNote, /비공개 HTTPS 신뢰 연결/);
  assert.match(privateHttpsMemory.storageNote, /현재 메모리 전용으로 연결/);

  const noStorage = describeConnectionSecurity({
    origin: "https://haeryong.tailnet.ts.net",
    nativeVariant: false,
    storageAvailable: false,
    remember: true,
    memoryOnly: true,
    verified: false,
  });
  assert.match(noStorage.storageNote, /로컬 저장소를 사용할 수 없어/);

  const pairings: PairingView[] = [
    {
      pairing_id: "pair-1",
      device_label: "iPad",
      origin: "https://haeryong.tailnet.ts.net",
      state: "approved",
      created_at: "2026-11-05T01:50:00Z",
      expires_at: "2026-11-05T02:00:00Z",
      revision: 3,
      etag: '"p3"',
    },
  ];
  assert.match(describePairingDecisionConflict("pair-1", pairings), /승인됨\(미수령\).*리비전 3/);
  assert.match(describePairingDecisionConflict("pair-missing", pairings), /더 이상 활성 대기 목록에 없습니다/);
});

function createControllerHarness() {
  let currentIdentity = {
    libraryId: "lib-1",
    principalId: "owner-1",
    sessionId: "sess-1",
    generation: "gen-1",
  };
  let configView: ConfigurationView = makeConfig(4, null, null, true);
  let devicesView: DeviceSession[] = [
    makeDevice({ session_id: "sess-1", device_label: "Owner MacBook", last_seen_at: "2026-11-05T00:55:00Z" }),
  ];
  const calls: Array<{ method: string; args: unknown[] }> = [];
  let boundToken = "";

  const client = {
    get credential() {
      return boundToken;
    },
    setCredential(token: string) {
      boundToken = token;
    },
    invalidate() {},
    subscribeEvents(): EventSubscription {
      return { closed: new Promise<void>(() => {}), close() {} };
    },
    async currentSession(): Promise<SessionInfo> {
      return {
        session_id: currentIdentity.sessionId,
        principal_id: currentIdentity.principalId,
        library_id: currentIdentity.libraryId,
        device_label: "Owner MacBook",
        origin: "http://127.0.0.1:9270",
        state: "active",
        created_at: "2026-11-05T00:00:00Z",
        last_seen_at: "2026-11-05T00:55:00Z",
        expires_at: null,
        revision: 1,
        etag: '"s1"',
        scope: { kind: "owner" },
      };
    },
    async capabilities(): Promise<Capabilities> {
      return {
        library_id: currentIdentity.libraryId,
        api_version: "1.0",
        runtime_generation: currentIdentity.generation,
        stream_epoch: "e1",
        unfiled_project_id: "unfiled",
        features: {},
      };
    },
    async revokeOwnSession(): Promise<void> {},
    async configurationView(): Promise<ConfigurationView> {
      calls.push({ method: "configurationView", args: [] });
      return configView;
    },
    async listSessions(): Promise<DeviceSession[]> {
      calls.push({ method: "listSessions", args: [] });
      return devicesView;
    },
    async configurationPlan(req: unknown, operationId: string): Promise<ConfigurationPlanView> {
      calls.push({ method: "configurationPlan", args: [req, operationId] });
      const changes = (req as { changes: { security: Record<string, unknown> } }).changes;
      return {
        plan_id: "plan-sec-1",
        base_config_revision: configView.config_revision,
        expires_at: "2026-11-05T01:15:00Z",
        normalized_changes: { security: changes.security },
        affected_permissions_revisions: {},
        impact: { security: "immediate" },
      };
    },
    async configurationPlanCommit(planId: string, req: unknown, operationId: string): Promise<ConfigurationCommitView> {
      calls.push({ method: "configurationPlanCommit", args: [planId, req, operationId] });
      configView = makeConfig(configView.config_revision + 1, 300, 3600, false);
      return { plan_id: planId, config_revision: configView.config_revision, impact: { security: "immediate" } };
    },
    async operation(operationId: string): Promise<OperationView> {
      calls.push({ method: "operation", args: [operationId] });
      return {
        operation_id: operationId,
        state: "completed",
        original_status: 200,
        result_ref: null,
        job_id: null,
        result_metadata: { plan_id: operationId, config_revision: configView.config_revision, impact: { security: "immediate" } },
      };
    },
  } as unknown as HaeryongClient & CredentialAware;

  const store = new MemoryCredentialStore();
  store.write("token-owner-1");
  const controller = new ShellController(client, store);

  function createFlow(nowMs = Date.parse("2026-11-05T01:00:00Z")) {
    return new SecurityPolicyFlow(
      client,
      {
        captureDispatchAuthority: () => controller.captureDispatchAuthority(),
        requireVerifiedDispatch: (captured) => controller.requireVerifiedDispatch(captured),
        isCurrentDispatchAuthority: (captured) => controller.isCurrentDispatchAuthority(captured),
        sourceWriteIntents: () => controller.sourceWriteIntents(),
        registerPolicyCommitIntent: (planId, baseRevision, operationId) =>
          controller.registerPolicyCommitIntent(planId, baseRevision, operationId),
        pendingPolicyRecovery: () => controller.pendingPolicyRecovery(),
        clearPolicyRecovery: (operationId) => controller.clearPolicyRecovery(operationId),
        recordPolicyCommitAuthLoss: (captured, operationId, code, confirmed) =>
          controller.recordPolicyCommitAuthLoss(captured, operationId, code, confirmed),
        currentSessionId: () => controller.state.session?.session_id ?? null,
      },
      () => nowMs,
    );
  }

  return {
    client,
    store,
    controller,
    calls,
    createFlow,
    setIdentity(next: Partial<typeof currentIdentity>) {
      currentIdentity = { ...currentIdentity, ...next };
    },
    setConfig(next: ConfigurationView) {
      configView = next;
    },
    setDevices(next: DeviceSession[]) {
      devicesView = next;
    },
  };
}

test("SecurityPolicyFlow previews library security scope, requires confirmation, and reaches saved after commit + reread", async () => {
  const harness = createControllerHarness();
  await harness.controller.verify();
  const flow = harness.createFlow();
  await flow.load();
  assert.equal(flow.snapshot().phase, "editing");

  await flow.preview({
    remember_device_default: false,
    session_idle_seconds: 300,
    session_absolute_seconds: 3600,
  });
  assert.equal(flow.snapshot().phase, "preview");
  assert.equal(flow.snapshot().expiryPreview?.currentDeviceAffected, true);

  // Commit without confirmation is refused locally without network dispatch.
  await flow.commit(false);
  assert.equal(flow.snapshot().phase, "preview");
  assert.match(flow.snapshot().reason ?? "", /확인/);
  assert.equal(harness.calls.filter((c) => c.method === "configurationPlanCommit").length, 0);

  await flow.commit(true);
  assert.equal(flow.snapshot().phase, "saved");
  assert.equal(flow.snapshot().notice, SECURITY_POLICY_COPY.savedNotice);
  assert.equal(flow.snapshot().configuration?.config_revision, 5);
  assert.equal(harness.controller.pendingPolicyRecovery(), null);
});

test("commit 401 SESSION_EXPIRED retains UNKNOWN recovery intent, scrubs private state, blocks cross-owner access, keeps 404 receipt UNKNOWN, and reconciles under same-owner replacement session", async () => {
  const harness = createControllerHarness();
  await harness.controller.verify();
  const flow1 = harness.createFlow();
  await flow1.load();
  await flow1.preview({
    remember_device_default: true,
    session_idle_seconds: 60,
    session_absolute_seconds: null,
  });

  // Simulate commit applying in SQLite and then response guard returning 401 SESSION_EXPIRED.
  harness.client.configurationPlanCommit = async () => {
    harness.setConfig(makeConfig(5, 60, null, true));
    throw new ApiError(401, "SESSION_EXPIRED", false, 0, {});
  };

  await flow1.commit(true);

  // 1. Flow state is pending (UNKNOWN) and all private policy/device/draft/plan fields are scrubbed.
  const snapAfter401 = flow1.snapshot();
  assert.equal(snapAfter401.phase, "pending");
  assert.deepEqual(snapAfter401.pending, { kind: "commit", operationId: "plan-sec-1" });
  assert.equal(snapAfter401.configuration, null);
  assert.deepEqual(snapAfter401.devices, []);
  assert.deepEqual(snapAfter401.draft, {
    remember_device_default: true,
    session_idle_seconds: null,
    session_absolute_seconds: null,
  });
  assert.equal(snapAfter401.plan, null);
  assert.equal(snapAfter401.normalizedDraft, null);
  assert.equal(snapAfter401.expiryPreview, null);

  // 2. Controller cleared credential and surfaced content-free unknown-commit notice.
  assert.equal(harness.controller.state.phase, "unauthenticated");
  assert.equal(harness.controller.state.notice, NOTICE.policyCommitUnknownExpired);
  assert.equal(harness.controller.pendingPolicyRecovery(), null);

  // 3. Cross-owner isolation: a different owner/library cannot see or reconcile the recovery intent.
  harness.setIdentity({ libraryId: "lib-2", principalId: "owner-2", sessionId: "sess-owner-2" });
  await harness.controller.acceptPairing("token-owner-2", true);
  assert.equal(harness.controller.state.phase, "verified");
  assert.equal(harness.controller.pendingPolicyRecovery(), null);
  const flowOtherOwner = harness.createFlow();
  await flowOtherOwner.load();
  assert.equal(flowOtherOwner.snapshot().phase, "editing");
  assert.equal(flowOtherOwner.snapshot().pending, null);

  // 4. Same-owner replacement session sees the pending recovery intent.
  harness.setIdentity({ libraryId: "lib-1", principalId: "owner-1", sessionId: "sess-replacement" });
  await harness.controller.acceptPairing("token-owner-1-replacement", true);
  assert.equal(harness.controller.state.phase, "verified");
  assert.equal(harness.controller.pendingPolicyRecovery()?.operationId, "plan-sec-1");
  assert.equal(harness.controller.pendingPolicyRecovery()?.state, "unknown");

  const flowReplacement = harness.createFlow();
  await flowReplacement.load();
  assert.equal(flowReplacement.snapshot().phase, "pending");
  assert.deepEqual(flowReplacement.snapshot().pending, { kind: "commit", operationId: "plan-sec-1" });

  // 5. Absent/inaccessible receipt (404) remains UNKNOWN (phase: "pending") with original operationId preserved; never claims not-applied.
  harness.client.operation = async () => {
    throw new ApiError(404, "NOT_FOUND", false, 0, {});
  };
  await flowReplacement.recover();
  assert.equal(flowReplacement.snapshot().phase, "pending");
  assert.deepEqual(flowReplacement.snapshot().pending, { kind: "commit", operationId: "plan-sec-1" });
  assert.equal(harness.controller.pendingPolicyRecovery()?.operationId, "plan-sec-1");

  // 6. When GET /api/v1/operations/{id} returns completed receipt, recover() transitions to saved without re-POSTing commit.
  const commitCallsBeforeRecover = harness.calls.filter((c) => c.method === "configurationPlanCommit").length;
  harness.client.operation = async (operationId: string) => ({
    operation_id: operationId,
    state: "completed",
    original_status: 200,
    result_ref: null,
    job_id: null,
    result_metadata: { config_revision: 5 },
  });
  await flowReplacement.recover();
  assert.equal(
    harness.calls.filter((c) => c.method === "configurationPlanCommit").length,
    commitCallsBeforeRecover,
  );
  assert.equal(flowReplacement.snapshot().phase, "saved");
  assert.equal(flowReplacement.snapshot().notice, SECURITY_POLICY_COPY.reconciledSavedNotice);
  assert.equal(flowReplacement.snapshot().configuration?.config_revision, 5);
  assert.equal(harness.controller.pendingPolicyRecovery(), null);
});

test("late in-flight commit 401 catch after switching to a newly verified different owner preserves old owner recovery without clearing the new owner session or leaking old private state", async () => {
  const harness = createControllerHarness();
  await harness.controller.verify();

  // Single flow instance reused across authority change (not relying on dispose()).
  const sharedFlow = harness.createFlow();
  await sharedFlow.load();
  await sharedFlow.preview({
    remember_device_default: false,
    session_idle_seconds: 120,
    session_absolute_seconds: 7200,
  });

  let rejectOldCommit!: (err: unknown) => void;
  harness.client.configurationPlanCommit = () =>
    new Promise((_resolve, reject) => {
      rejectOldCommit = reject;
    });

  // Start commit under owner-1 (lib-1).
  const oldCommitPromise = sharedFlow.commit(true);
  assert.equal(sharedFlow.snapshot().phase, "committing");

  // Before old commit rejects, switch controller to owner-2 (lib-2) and load owner-2 into the SAME flow instance.
  harness.setIdentity({ libraryId: "lib-2", principalId: "owner-2", sessionId: "sess-owner-2" });
  harness.setConfig(makeConfig(10, 900, null, true));
  harness.setDevices([makeDevice({ session_id: "sess-owner-2", device_label: "Owner 2 Workstation" })]);
  await harness.controller.acceptPairing("token-owner-2", true);
  assert.equal(harness.controller.state.phase, "verified");
  await sharedFlow.load();
  assert.equal(sharedFlow.snapshot().phase, "editing");
  assert.equal(sharedFlow.snapshot().configuration?.config_revision, 10);

  // Now reject the old in-flight commit from owner-1 with 401 SESSION_EXPIRED.
  rejectOldCommit(new ApiError(401, "SESSION_EXPIRED", false, 0, {}));
  await oldCommitPromise;

  // Owner-2 session on controller must remain verified and untouched!
  assert.equal(harness.controller.state.phase, "verified");
  assert.equal(harness.controller.state.session?.principal_id, "owner-2");
  assert.equal(harness.controller.state.capabilities?.library_id, "lib-2");
  assert.equal(harness.controller.hasCredential(), true);

  // Shared flow must still show owner-2's editing state, never overwritten by owner-1's late catch.
  assert.equal(sharedFlow.snapshot().phase, "editing");
  assert.equal(sharedFlow.snapshot().configuration?.config_revision, 10);
  assert.equal(sharedFlow.snapshot().devices[0]?.device_label, "Owner 2 Workstation");
  assert.equal(sharedFlow.snapshot().pending, null);

  // Meanwhile, owner-1's minimal recovery intent was preserved in owner-1's scope.
  harness.setIdentity({ libraryId: "lib-1", principalId: "owner-1", sessionId: "sess-owner-1-new" });
  await harness.controller.acceptPairing("token-owner-1-new", true);
  assert.equal(harness.controller.pendingPolicyRecovery()?.operationId, "plan-sec-1");
  assert.equal(harness.controller.pendingPolicyRecovery()?.state, "unknown");
});

test("late in-flight commit catch on an un-reloaded, un-disposed flow after authority change scrubs old private data without clearing the new session", async () => {
  const harness = createControllerHarness();
  await harness.controller.verify();
  const oldFlow = harness.createFlow();
  await oldFlow.load();
  await oldFlow.preview({
    remember_device_default: false,
    session_idle_seconds: 180,
    session_absolute_seconds: null,
  });

  let rejectOldCommit!: (err: unknown) => void;
  harness.client.configurationPlanCommit = () =>
    new Promise((_resolve, reject) => {
      rejectOldCommit = reject;
    });

  const oldCommitPromise = oldFlow.commit(true);

  // Switch controller to owner-2 without calling oldFlow.dispose() or oldFlow.load().
  harness.setIdentity({ libraryId: "lib-2", principalId: "owner-2", sessionId: "sess-owner-2" });
  await harness.controller.acceptPairing("token-owner-2", true);
  assert.equal(harness.controller.state.phase, "verified");

  rejectOldCommit(new StaleResponseError());
  await oldCommitPromise;

  // Controller stays verified as owner-2.
  assert.equal(harness.controller.state.phase, "verified");
  assert.equal(harness.controller.state.session?.principal_id, "owner-2");

  // Old flow scrubbed all private configuration, device, and draft state.
  assert.equal(oldFlow.snapshot().phase, "pending");
  assert.equal(oldFlow.snapshot().configuration, null);
  assert.deepEqual(oldFlow.snapshot().devices, []);
  assert.deepEqual(oldFlow.snapshot().draft, {
    remember_device_default: true,
    session_idle_seconds: null,
    session_absolute_seconds: null,
  });
});

test("confirmed commit followed by failed configurationView read enters applied_unreadable without false Saved or duplicate commit", async () => {
  const harness = createControllerHarness();
  await harness.controller.verify();
  const flow = harness.createFlow();
  await flow.load();
  await flow.preview({
    remember_device_default: true,
    session_idle_seconds: 600,
    session_absolute_seconds: null,
  });

  harness.client.configurationView = async () => {
    throw new ApiError(503, "SERVICE_UNAVAILABLE", true, 0, {});
  };

  await flow.commit(true);
  assert.equal(flow.snapshot().phase, "applied_unreadable");
  assert.equal(flow.snapshot().notice, SECURITY_POLICY_COPY.appliedUnreadableNotice);
  assert.equal(flow.snapshot().pending, null);
  assert.equal(harness.controller.pendingPolicyRecovery(), null);
});

test("stale-plan/revision 412 reread preserves the user-proposed policy draft without resetting to server values or auto-applying, and requires fresh explicit preview/confirmation", async () => {
  const harness = createControllerHarness();
  await harness.controller.verify();
  const flow = harness.createFlow();
  await flow.load();
  assert.equal(flow.snapshot().configuration?.config_revision, 4);

  const userDraft = {
    remember_device_default: false,
    session_idle_seconds: 240,
    session_absolute_seconds: 14400,
  };
  await flow.preview(userDraft);
  assert.equal(flow.snapshot().phase, "preview");

  // Simulate another owner session advancing config_revision to 7 on server, causing commit to fail with 412 STALE_PLAN.
  harness.setConfig(makeConfig(7, 1800, 86400, true));
  let failCommitOnce = true;
  const origCommit = harness.client.configurationPlanCommit.bind(harness.client);
  harness.client.configurationPlanCommit = async (
    planId: string,
    req: Parameters<HaeryongClient["configurationPlanCommit"]>[1],
    operationId: string,
  ) => {
    if (failCommitOnce) {
      failCommitOnce = false;
      throw new ApiError(412, "STALE_PLAN", false, 0, {});
    }
    return origCommit(planId, req, operationId);
  };

  await flow.commit(true);

  // 1. Phase is stale, server revision is refreshed to 7, and plan is cleared (no auto-apply).
  const staleSnap = flow.snapshot();
  assert.equal(staleSnap.phase, "stale");
  assert.equal(staleSnap.reason, SECURITY_POLICY_COPY.staleNotice);
  assert.equal(staleSnap.configuration?.config_revision, 7);
  assert.equal(staleSnap.plan, null);
  assert.equal(staleSnap.expiryPreview, null);

  // 2. Crucially, the user-proposed draft is preserved intact, not overwritten by server defaults (1800 / 86400 / true).
  assert.deepEqual(staleSnap.draft, userDraft);

  // 3. Explicit re-preview with the preserved draft uses the refreshed base_config_revision (7).
  await flow.preview(staleSnap.draft);
  assert.equal(flow.snapshot().phase, "preview");
  assert.equal(flow.snapshot().plan?.base_config_revision, 7);
  assert.deepEqual(flow.snapshot().draft, userDraft);
});

test("post-dispatch 503 SERVICE_RECOVERING after durable commit retains original operation and minimal recovery without logout or duplicate POST, and reconciles via receipt", async () => {
  const harness = createControllerHarness();
  await harness.controller.verify();
  const flow = harness.createFlow();
  await flow.load();

  const nextDraft = {
    remember_device_default: false,
    session_idle_seconds: 300,
    session_absolute_seconds: 3600,
  };
  await flow.preview(nextDraft);
  assert.equal(flow.snapshot().phase, "preview");

  let commitCalls = 0;
  harness.client.configurationPlanCommit = async (
    _planId: string,
    _req: Parameters<HaeryongClient["configurationPlanCommit"]>[1],
    _operationId: string,
  ) => {
    commitCalls += 1;
    // Simulate durable SQLite application + receipt completion followed by a
    // final Auth.Guard store failure returning 503 SERVICE_RECOVERING.
    harness.setConfig(makeConfig(5, 300, 3600, false));
    throw new ApiError(503, "SERVICE_RECOVERING", true, 0, {});
  };

  await flow.commit(true);

  const pendingSnap = flow.snapshot();
  assert.equal(commitCalls, 1);
  assert.equal(pendingSnap.phase, "pending");
  assert.deepEqual(pendingSnap.pending, { kind: "commit", operationId: "plan-sec-1" });
  assert.equal(harness.controller.state.phase, "verified");
  assert.equal(harness.controller.hasCredential(), true);
  assert.equal(harness.controller.sourceWriteIntents().securityPolicy?.operationId, "plan-sec-1");
  assert.deepEqual(harness.controller.sourceWriteIntents().securityPolicy?.outcome, { state: "pending" });
  assert.equal(harness.controller.pendingPolicyRecovery()?.operationId, "plan-sec-1");

  // Reconcile via GET /api/v1/operations/plan-sec-1 without re-dispatching POST commit.
  await flow.recover();
  const reconciledSnap = flow.snapshot();
  assert.equal(commitCalls, 1);
  assert.equal(reconciledSnap.phase, "saved");
  assert.equal(reconciledSnap.notice, SECURITY_POLICY_COPY.reconciledSavedNotice);
  assert.equal(reconciledSnap.configuration?.config_revision, 5);
  assert.deepEqual(reconciledSnap.draft, nextDraft);
  assert.equal(reconciledSnap.pending, null);
  assert.equal(harness.controller.sourceWriteIntents().securityPolicy, null);
  assert.equal(harness.controller.pendingPolicyRecovery(), null);
});

test("preview refuses to substitute stale cached device inventory when listSessions fails, preserves draft without phantom UNKNOWN, and includes newly joined device on retry", async () => {
  const harness = createControllerHarness();
  await harness.controller.verify();
  const flow = harness.createFlow(Date.parse("2026-11-05T01:00:00Z"));
  await flow.load();
  assert.equal(flow.snapshot().devices.length, 1);

  // Another device joins after initial load and has already exceeded a 600s idle limit.
  const newlyJoinedPeer = makeDevice({
    session_id: "sess-newly-joined",
    device_label: "New Peer Tablet",
    created_at: "2026-11-05T00:30:00Z",
    last_seen_at: "2026-11-05T00:40:00Z", // idle 1200s >= 600s
    state: "active",
  });
  harness.setDevices([
    makeDevice({ session_id: "sess-1", device_label: "Owner MacBook", last_seen_at: "2026-11-05T00:55:00Z" }),
    newlyJoinedPeer,
  ]);

  let failListOnce = true;
  const origList = harness.client.listSessions.bind(harness.client);
  harness.client.listSessions = async () => {
    if (failListOnce) {
      failListOnce = false;
      throw new ApiError(503, "SERVICE_UNAVAILABLE", true, 0, {});
    }
    return origList();
  };

  const draft = {
    remember_device_default: true,
    session_idle_seconds: 600,
    session_absolute_seconds: null,
  };
  const planCallsBefore = harness.calls.filter((c) => c.method === "configurationPlan").length;
  await flow.preview(draft);

  // 1. Must NOT silently use stale cached devices to present a confirmable preview,
  // and must NOT leave an undispatched plan as UNKNOWN ("pending").
  const failedSnap = flow.snapshot();
  assert.equal(failedSnap.phase, "error");
  assert.equal(failedSnap.plan, null);
  assert.equal(failedSnap.expiryPreview, null);
  assert.equal(failedSnap.pending, null);
  assert.deepEqual(failedSnap.draft, draft);
  assert.ok(failedSnap.reason);
  assert.equal(harness.controller.sourceWriteIntents().securityPolicy, null);
  assert.equal(harness.calls.filter((c) => c.method === "configurationPlan").length, planCallsBefore);

  // Commit is blocked while preview is absent.
  await flow.commit(true);
  assert.equal(harness.calls.filter((c) => c.method === "configurationPlanCommit").length, 0);

  // 2. Explicit retry reads current authorized inventory and includes the newly joined device.
  await flow.preview(failedSnap.draft);
  const previewSnap = flow.snapshot();
  assert.equal(previewSnap.phase, "preview");
  assert.equal(previewSnap.devices.length, 2);
  assert.equal(previewSnap.expiryPreview?.affectedDevices.length, 1);
  assert.equal(previewSnap.expiryPreview?.affectedDevices[0]?.session_id, "sess-newly-joined");
  assert.equal(harness.calls.filter((c) => c.method === "configurationPlan").length, planCallsBefore + 1);
});

test("recover of a pending preview requires fresh listSessions before confirmable preview and reuses the reconciled plan on retry without redispatching configurationPlan", async () => {
  const harness = createControllerHarness();
  await harness.controller.verify();
  const flow = harness.createFlow(Date.parse("2026-11-05T01:00:00Z"));
  await flow.load();

  const draft = {
    remember_device_default: false,
    session_idle_seconds: 600,
    session_absolute_seconds: null,
  };

  // Initial preview dispatches configurationPlan, which fails with ambiguous 503.
  let failPlanOnce = true;
  const origPlan = harness.client.configurationPlan.bind(harness.client);
  harness.client.configurationPlan = async (
    req: Parameters<HaeryongClient["configurationPlan"]>[0],
    operationId: string,
  ) => {
    if (failPlanOnce) {
      failPlanOnce = false;
      harness.calls.push({ method: "configurationPlan", args: [req, operationId] });
      throw new ApiError(503, "SERVICE_RECOVERING", true, 0, {});
    }
    return origPlan(req, operationId);
  };

  await flow.preview(draft);
  assert.equal(flow.snapshot().phase, "pending");
  const opId = flow.snapshot().pending?.operationId ?? "";
  assert.ok(opId);

  // Operation receipt returns the completed ConfigurationPlanView, while a newly
  // joined device appears on the server and listSessions transiently fails.
  const newlyJoinedPeer = makeDevice({
    session_id: "sess-joined-during-pending",
    device_label: "Joined During Pending",
    created_at: "2026-11-05T00:30:00Z",
    last_seen_at: "2026-11-05T00:35:00Z", // idle 1500s >= 600s
    state: "active",
  });
  harness.setDevices([
    makeDevice({ session_id: "sess-1", device_label: "Owner MacBook", last_seen_at: "2026-11-05T00:55:00Z" }),
    newlyJoinedPeer,
  ]);
  harness.client.operation = async (id: string) => {
    harness.calls.push({ method: "operation", args: [id] });
    return {
      operation_id: id,
      state: "completed",
      original_status: 200,
      result_ref: null,
      job_id: null,
      result_metadata: {
        plan_id: "plan-recovered-1",
        base_config_revision: 4,
        expires_at: "2026-11-05T01:15:00Z",
        normalized_changes: {
          security: {
            remember_device_default: false,
            session_idle_seconds: 600,
            session_absolute_seconds: null,
          },
        },
        affected_permissions_revisions: {},
        impact: { security: "immediate" },
      },
    };
  };

  let failListOnce = true;
  const origList = harness.client.listSessions.bind(harness.client);
  harness.client.listSessions = async () => {
    if (failListOnce) {
      failListOnce = false;
      throw new ApiError(503, "SERVICE_UNAVAILABLE", true, 0, {});
    }
    return origList();
  };

  const planCallsBeforeRecover = harness.calls.filter((c) => c.method === "configurationPlan").length;
  await flow.recover();

  // Must surface the inventory failure rather than confirming from stale state.devices.
  const afterFailedRecover = flow.snapshot();
  assert.equal(afterFailedRecover.phase, "error");
  assert.equal(afterFailedRecover.plan, null);
  assert.equal(afterFailedRecover.expiryPreview, null);
  assert.deepEqual(afterFailedRecover.draft, draft);
  assert.ok(afterFailedRecover.reason);

  // Retrying preview (or recover) reuses the already-reconciled plan without a second configurationPlan POST.
  await flow.preview(afterFailedRecover.draft);
  assert.equal(harness.calls.filter((c) => c.method === "configurationPlan").length, planCallsBeforeRecover);
  assert.equal(flow.snapshot().phase, "preview");
  assert.equal(flow.snapshot().plan?.plan_id, "plan-recovered-1");
  assert.equal(flow.snapshot().expiryPreview?.affectedDevices.length, 1);
  assert.equal(flow.snapshot().expiryPreview?.affectedDevices[0]?.session_id, "sess-joined-during-pending");
});

test("securityPolicyAuthorityKey and SecurityPolicyFlow do not issue touching reads on SSE stream open, invalidation, close, or routine verify rechecks", async () => {
  const harness = createControllerHarness();
  let triggerInvalidation: (() => void) | undefined;
  let closeStream: (() => void) | undefined;
  harness.client.subscribeEvents = (): EventSubscription => ({
    onInvalidation(fn: () => void) {
      triggerInvalidation = fn;
      return () => {
        if (triggerInvalidation === fn) triggerInvalidation = undefined;
      };
    },
    closed: new Promise<void>((resolve) => {
      closeStream = resolve;
    }),
    close() {},
  });

  await harness.controller.verify();
  const initialKey = securityPolicyAuthorityKey(harness.controller.state);
  assert.equal(initialKey, "lib-1:owner-1");

  const flow = harness.createFlow();
  await flow.load();
  const touchingReadsAfterLoad = harness.calls.filter(
    (c) => c.method === "configurationView" || c.method === "listSessions",
  ).length;
  assert.equal(touchingReadsAfterLoad, 2);

  // Fire SSE invalidation and routine same-authority verify() rechecks.
  triggerInvalidation?.();
  await harness.controller.verify();
  assert.equal(securityPolicyAuthorityKey(harness.controller.state), initialKey);
  closeStream?.();
  await Promise.resolve();
  await harness.controller.verify();
  assert.equal(securityPolicyAuthorityKey(harness.controller.state), initialKey);

  // Zero automatic touching reads were triggered by stream open/invalidation/close or passive verify().
  const touchingReadsAfterEvents = harness.calls.filter(
    (c) => c.method === "configurationView" || c.method === "listSessions",
  ).length;
  assert.equal(touchingReadsAfterEvents, touchingReadsAfterLoad);
});



