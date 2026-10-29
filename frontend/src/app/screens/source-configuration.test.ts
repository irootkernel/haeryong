import { ShellController } from "../../client/controller.ts";
import { HttpHaeryongClient } from "../../client/http.ts";
import { MemoryCredentialStore } from "../../client/store.ts";
import { SourceWriteIntents } from "../../client/source-write-intents.ts";
import test from "node:test";
import assert from "node:assert/strict";
import type { ConfigurationCommitView, ConfigurationPlanView, HaeryongClient, SourceMapping } from "../../client/types.ts";
import { ApiError, DispatchBlockedError, StaleResponseError } from "../../client/types.ts";
import { PendingSourceWriteError } from "../../client/source-operations.ts";
import { ProjectRegistrationFlow, SourceConfigurationFlow, sourceConfirmation, expandsSourceScope, sourcesForPreview } from "./source-configuration.ts";

const mapping: SourceMapping = { source_id: "s", kind: "git", repo_id: "r", root_path: "/approved", retention_mode: "reference_only", provider_ids: [] };
const plan: ConfigurationPlanView = { plan_id: "plan", base_config_revision: 7, expires_at: "later", normalized_changes: { projects: { p: { sources: [mapping] } } }, affected_permissions_revisions: { p: 1 }, impact: { workflow_scope: "new capture", security: "immediate" } };
function fixture() {
  const calls: unknown[][] = [];
  const client = {
    configurationView: async () => ({ config_revision: 7, etag: "tag", configuration: { projects: { p: { sources: [mapping] } } } }),
    configurationPlan: async (...args: unknown[]) => { calls.push(["plan", ...args]); return { ...plan, normalized_changes: { projects: { p: { ...(args[0] as { changes: Record<string, unknown> }).changes } } } }; },
    configurationPlanCommit: async (...args: unknown[]) => { calls.push(["commit", ...args]); return { config_revision: 8 }; },
  } as unknown as HaeryongClient;
  const intents = new SourceWriteIntents();
  const authority = { sourceWriteIntents: () => intents, captureDispatchAuthority: () => null, requireVerifiedDispatch() {} };
  return { client, calls, authority, flow: new SourceConfigurationFlow(client, "p", authority) };
}

test("source settings preview binds the revision and scope, cancel never commits, and scope expansion requires confirmation", async () => {
  const { flow, calls } = fixture(); await flow.load();
  await flow.preview([{ ...mapping, provider_ids: ["new-provider"] }]);
  assert.equal(flow.snapshot().phase, "preview"); assert.equal(flow.snapshot().expandsScope, true);
  assert.deepEqual((calls[0]![1] as { scope: unknown }).scope, { kind: "project", project_id: "p" });
  assert.equal((calls[0]![1] as { expected_config_revision: number }).expected_config_revision, 7);
  await flow.commit(false); assert.equal(calls.length, 1); assert.match(flow.snapshot().reason!, /확인/);
  flow.cancel(); assert.equal(flow.snapshot().plan, null); assert.equal(flow.snapshot().phase, "editing");
  await flow.preview([{ ...mapping, root_path: "/new-root" }]); await flow.commit(true);
  assert.equal(flow.snapshot().phase, "done");
  assert.deepEqual(calls.at(-1), ["commit", "plan", { expected_config_revision: 7, confirmed: true }, "plan"]);
});

test("stale plans stay explicit and cannot be committed again until reloaded and previewed", async () => {
  const { flow, client, calls } = fixture(); await flow.load(); await flow.preview([mapping]);
  client.configurationPlanCommit = async () => { throw new ApiError(409, "STALE_PLAN", false); };
  await flow.commit(true); assert.equal(flow.snapshot().phase, "stale");
  await flow.commit(true); assert.equal(calls.length, 1);
  await flow.load(); assert.equal(flow.snapshot().phase, "editing"); assert.equal(flow.snapshot().plan, null);
});

test("replaced/cancelled previews do not apply and unverified dispatch sends no plan", async () => {
  const { flow, client, authority, calls } = fixture(); await flow.load();
  authority.requireVerifiedDispatch = () => { throw new DispatchBlockedError(); };
  await assert.rejects(flow.preview([mapping]), DispatchBlockedError); assert.equal(calls.length, 0);
  authority.requireVerifiedDispatch = () => {};
  let release!: (value: ConfigurationPlanView) => void;
  client.configurationPlan = () => new Promise((resolve) => { release = resolve; });
  const pending = flow.preview([mapping]); flow.cancel(); release(plan); await pending;
  assert.equal(flow.snapshot().plan, null); assert.equal(flow.snapshot().phase, "editing");
});

test("uncertain settings writes recover the original plan/commit receipt without another mutation", async () => {
  const { flow, client, calls } = fixture(); await flow.load();
  let key = "";
  client.configurationPlan = async (_request, operationId) => { key = operationId; throw new PendingSourceWriteError(key); };
  client.operation = async (operationId) => { assert.equal(operationId, key); return { operation_id: key, state: "completed", original_status: 200, result_metadata: plan, result_ref: null, job_id: null }; };
  await flow.preview([mapping]); assert.equal(flow.snapshot().phase, "pending");
  await flow.recover(); assert.equal(flow.snapshot().phase, "preview"); assert.equal(calls.length, 0);
});

test("scope impact distinguishes added/root/repository/provider/retention access from removal", () => {
  assert.equal(expandsSourceScope([mapping], []), false);
  assert.equal(expandsSourceScope([mapping], [mapping]), false);
  for (const patch of [{ source_id: "new" }, { root_path: "/new" }, { repo_id: "new" }, { provider_ids: ["p"] }, { retention_mode: "selected_snapshot" as const }]) assert.equal(expandsSourceScope([mapping], [{ ...mapping, ...patch }]), true);
});

test("preview displays canonical server changes and derives impact from those changes", async () => {
  const { flow, client } = fixture(); await flow.load();
  client.configurationPlan = async () => ({ ...plan, normalized_changes: { projects: { p: { sources: [mapping] } } } });
  await flow.preview([{ ...mapping, root_path: "./alias" }]);
  assert.equal(flow.snapshot().previewSources[0]?.root_path, "/approved");
  assert.equal(flow.snapshot().expandsScope, false);
  client.configurationPlan = async () => ({ ...plan, normalized_changes: { projects: { p: { sources: [{ ...mapping, root_path: "/canonical/new" }] } } } });
  await flow.preview([mapping]);
  assert.equal(flow.snapshot().previewSources[0]?.root_path, "/canonical/new");
  assert.equal(flow.snapshot().expandsScope, true);
});

test("ordinary provider typing preserves commas until the plan request", async () => {
  const { flow, calls } = fixture(); await flow.load();
  const providerText: Record<string, string> = { s: "" };
  for (const character of "prov-a, prov-b") providerText.s += character;
  await flow.preview(sourcesForPreview([mapping], providerText));
  const request = calls[0]![1] as { changes: { sources: SourceMapping[] } };
  assert.deepEqual(request.changes.sources[0]?.provider_ids, ["prov-a", "prov-b"]);
});

test("stale applied registration reconciles its saved intent without another POST", async () => {
  const { client, authority } = fixture();
  let posts = 0, key = "";
  const receipts: string[] = [];
  const flow = new ProjectRegistrationFlow(client, authority);
  client.projectRegister = async (name, operationId) => {
    posts++; key = operationId;
    assert.deepEqual(flow.pending, { operationId, name, outcome: { state: "pending" } });
    throw new StaleResponseError();
  };
  client.operation = async (operationId) => { receipts.push(operationId); return { operation_id: operationId, state: "completed", original_status: 201, result_metadata: { project_id: "p" }, result_ref: null, job_id: null }; };
  await assert.rejects(flow.run("Original"), StaleResponseError);
  assert.equal(flow.pending?.operationId, key);
  await flow.run("Changed");
  assert.equal(posts, 1); assert.deepEqual(receipts, [key]); assert.equal(flow.pending, null);
});

test("stale applied plan and commit reconcile their original keys without another POST", async () => {
  const { flow, client } = fixture(); await flow.load();
  let plans = 0, commits = 0, key = "";
  const receipts: string[] = [];
  client.configurationPlan = async (_request, operationId) => { plans++; key = operationId; throw new StaleResponseError(); };
  client.operation = async (operationId) => { receipts.push(operationId); return { operation_id: operationId, state: "completed", original_status: 200, result_metadata: plan, result_ref: null, job_id: null }; };
  await flow.preview([mapping]);
  assert.equal(flow.snapshot().phase, "pending"); assert.equal(flow.snapshot().pending?.operationId, key);
  await flow.recover();
  assert.equal(plans, 1); assert.deepEqual(receipts, [key]); assert.equal(flow.snapshot().phase, "preview");
  assert.equal(flow.snapshot().previewSources[0]?.root_path, mapping.root_path);
  client.configurationPlanCommit = async () => { commits++; throw new StaleResponseError(); };
  await flow.commit(true);
  assert.equal(flow.snapshot().phase, "pending"); assert.equal(flow.snapshot().pending?.operationId, plan.plan_id);
  await flow.recover();
  assert.equal(commits, 1); assert.deepEqual(receipts, [key, plan.plan_id]); assert.equal(flow.snapshot().phase, "done");
});

// Real controller + HTTP freshness guard; only the HTTP server is scripted.
async function remountFixture() {
  const restoreFetch = globalThis.fetch;
  const identity = { library: "library-a", principal: "owner-a", generation: "g1" };
  const posts: Array<{ path: string; key: string }> = [], receipts: string[] = [];
  const results = new Map<string, unknown>();
  let hold = false, release!: () => void, started!: () => void;
  let dispatched = Promise.resolve();
  globalThis.fetch = async (input, init) => {
    const path = String(input), method = init?.method ?? "GET";
    let data: unknown;
    if (method === "POST") {
      const key = new Headers(init?.headers).get("Idempotency-Key")!;
      posts.push({ path, key });
      data = path.endsWith("/projects") ? { project_id: "registered" } : path.endsWith("/commit") ? { config_revision: 8 } : plan;
      results.set(key, data); // Applied before the acknowledgement is released.
      if (hold) { started(); await new Promise<void>((resolve) => { release = resolve; }); }
    } else if (path.includes("/operations/")) {
      const key = path.split("/").at(-1)!; receipts.push(key);
      data = { operation_id: key, state: "completed", original_status: 200, result_metadata: results.get(key), result_ref: null, job_id: null };
    } else if (path.endsWith("/session")) data = { library_id: identity.library, principal_id: identity.principal, session_id: "session", scope: { kind: "owner" } };
    else if (path.endsWith("/capabilities")) data = { library_id: identity.library, runtime_generation: identity.generation, api_version: "1.0", features: {} };
    else data = { config_revision: 7, etag: "tag", configuration: { projects: { p: { sources: [mapping] } } } };
    return new Response(JSON.stringify({ data }), { status: 200 });
  };
  const client = new HttpHaeryongClient();
  client.subscribeEvents = () => ({ closed: new Promise<void>(() => {}), close() {} });
  const credentials = new MemoryCredentialStore(); credentials.write("disposable-fixture");
  const controller = new ShellController(client, credentials);
  await controller.verify();
  return {
    client, controller, identity, posts, receipts,
    hold() { hold = true; dispatched = new Promise<void>((resolve) => { started = resolve; }); },
    started: () => dispatched,
    release() { hold = false; release(); },
    close() { controller.stop(); globalThis.fetch = restoreFetch; },
  };
}

test("runtime remount recovers registration, plan and commit through original receipts with the real stale guard", async () => {
  for (const kind of ["registration", "preview", "commit"] as const) {
    const f = await remountFixture();
    try {
      const oldRegistration = new ProjectRegistrationFlow(f.client, f.controller);
      const oldConfiguration = new SourceConfigurationFlow(f.client, "p", f.controller);
      await oldConfiguration.load();
      if (kind === "commit") await oldConfiguration.preview([mapping]);
      f.hold();
      const write = kind === "registration" ? oldRegistration.run("Original") : kind === "preview" ? oldConfiguration.preview([mapping]) : oldConfiguration.commit(true);
      // Attach the stale rejection handler before releasing the HTTP ack.
      const settled = kind === "registration" ? assert.rejects(write, StaleResponseError) : write;
      await f.started();
      const originalKey = f.posts.at(-1)!.key, postCount = f.posts.length;
      oldConfiguration.dispose();
      f.identity.generation = "g2"; await f.controller.verify();
      // These are the fresh flows constructed by the generation-keyed screen.
      const registration = new ProjectRegistrationFlow(f.client, f.controller);
      const configuration = new SourceConfigurationFlow(f.client, "p", f.controller);
      await configuration.load();
      if (kind === "registration") assert.equal(registration.pending?.operationId, originalKey);
      else assert.equal(configuration.snapshot().pending?.operationId, originalKey);
      f.release(); await settled;
      if (kind === "registration") await registration.run("Replacement");
      else await configuration.recover();
      assert.equal(f.posts.length, postCount); assert.deepEqual(f.receipts, [originalKey]);
      if (kind === "registration") assert.equal(registration.pending, null);
      else assert.equal(configuration.snapshot().phase, kind === "preview" ? "preview" : "done");
    } finally { f.close(); }
  }
});

test("registration and configuration intents are isolated across principals and libraries", async () => {
  for (const boundary of ["principal", "library"] as const) {
    const f = await remountFixture();
    try {
      const first = new ProjectRegistrationFlow(f.client, f.controller);
      f.hold(); const write = first.run("Original"); const stale = assert.rejects(write, StaleResponseError);
      await f.started(); f.identity.generation = "g2"; await f.controller.verify(); f.release(); await stale;
      const configuration = new SourceConfigurationFlow(f.client, "p", f.controller); await configuration.load();
      f.hold(); const preview = configuration.preview([mapping]); await f.started();
      f.identity.generation = "g3"; await f.controller.verify(); f.release(); await preview;
      f.controller.sourceWriteIntents().verifications.set(JSON.stringify(["v", "e"]), { operationId: "check-key", request: {}, outcome: { state: "pending" } });
      const original = [first.pending!.operationId, configuration.snapshot().pending!.operationId];
      f.identity[boundary] += "-other"; await f.controller.verify();
      const otherRegistration = new ProjectRegistrationFlow(f.client, f.controller);
      const otherConfiguration = new SourceConfigurationFlow(f.client, "p", f.controller); await otherConfiguration.load();
      assert.equal(f.controller.sourceWriteIntents().verifications.size, 0);
      assert.equal(otherRegistration.pending, null); assert.equal(otherConfiguration.snapshot().pending, null);
      await otherRegistration.run("Other"); await otherConfiguration.preview([mapping]);
      assert.equal(f.posts.length, 4); assert.deepEqual(f.receipts, []);
      assert.ok(f.posts.slice(2).every((post) => !original.includes(post.key)));
    } finally { f.close(); }
  }
});

for (const kind of ["registration", "preview", "commit"] as const) {
  test(`normal completion of ${kind} survives same-generation re-entry until consumed`, async () => {
    const f = await remountFixture();
    try {
      const oldRegistration = new ProjectRegistrationFlow(f.client, f.controller);
      const oldConfiguration = new SourceConfigurationFlow(f.client, "p", f.controller); await oldConfiguration.load();
      if (kind === "commit") await oldConfiguration.preview([mapping]);
      f.hold();
      const write = kind === "registration" ? oldRegistration.run("Original") : kind === "preview" ? oldConfiguration.preview([mapping]) : oldConfiguration.commit(true);
      await f.started();
      const key = f.posts.at(-1)!.key, count = f.posts.length;
      oldRegistration.dispose(); oldConfiguration.dispose();
      const registration = new ProjectRegistrationFlow(f.client, f.controller);
      const configuration = new SourceConfigurationFlow(f.client, "p", f.controller); await configuration.load();
      if (kind === "registration") assert.equal(registration.pending?.operationId, key);
      else assert.equal(configuration.snapshot().pending?.operationId, key);
      f.release(); await write;
      const intents = f.controller.sourceWriteIntents();
      const retained = kind === "registration" ? intents.registration : intents.configurations.get("p");
      assert.equal(retained?.outcome.state, "completed");
      if (kind === "registration") await registration.run("Changed"); else await configuration.recover();
      assert.equal(f.posts.length, count); assert.deepEqual(f.receipts, []);
      if (kind === "registration") assert.equal(registration.pending, null);
      else {
        assert.equal(configuration.snapshot().phase, kind === "preview" ? "preview" : "done");
        assert.equal(intents.configurations.size, 0);
      }
    } finally { f.close(); }
  });
}

test("a re-entered registration, preview or commit shows the original definitive failure before consuming it", async () => {
  for (const kind of ["registration", "preview", "commit"] as const) {
    const { client, authority } = fixture();
    const firstRegistration = new ProjectRegistrationFlow(client, authority);
    const firstConfiguration = new SourceConfigurationFlow(client, "p", authority); await firstConfiguration.load();
    if (kind === "commit") await firstConfiguration.preview([mapping]);
    let reject!: (error: Error) => void, posts = 0;
    const failure = new ApiError(403, "DENIED", false);
    const dispatch = async (): Promise<never> => { posts++; return new Promise((_resolve, no) => { reject = no; }); };
    if (kind === "registration") client.projectRegister = dispatch;
    else if (kind === "preview") client.configurationPlan = dispatch;
    else client.configurationPlanCommit = dispatch;
    const write = kind === "registration" ? firstRegistration.run("Original") : kind === "preview" ? firstConfiguration.preview([mapping]) : firstConfiguration.commit(true);
    const settled = kind === "registration" ? assert.rejects(write, (error) => error === failure) : write;
    firstRegistration.dispose(); firstConfiguration.dispose();
    const registration = new ProjectRegistrationFlow(client, authority);
    const configuration = new SourceConfigurationFlow(client, "p", authority); await configuration.load();
    reject(failure); await settled;
    const intents = authority.sourceWriteIntents();
    assert.equal((kind === "registration" ? intents.registration : intents.configurations.get("p"))?.outcome.state, "failed");
    if (kind === "registration") {
      await assert.rejects(registration.run("Changed"), (error) => error === failure);
      assert.equal(intents.registration, null);
    } else {
      await configuration.recover(); assert.equal(configuration.snapshot().phase, "error");
      assert.equal(configuration.snapshot().reason, failure.message); assert.equal(intents.configurations.size, 0);
    }
    assert.equal(posts, 1);
  }
});

for (const outcome of ["completed", "failed"] as const) {
  test(`cancelled in-flight preview ${outcome} leaves no intent and a new preview dispatches independently`, async () => {
    const { flow, client, authority } = fixture(); await flow.load();
    const keys: string[] = [];
    let resolve!: (value: ConfigurationPlanView) => void, reject!: (error: Error) => void;
    client.configurationPlan = async (_request, key) => {
      keys.push(key);
      return new Promise((yes, no) => { resolve = yes; reject = no; });
    };
    const first = flow.preview([mapping]);
    assert.equal(flow.snapshot().phase, "previewing");
    flow.cancel();
    assert.equal(flow.snapshot().phase, "editing");
    assert.equal(flow.snapshot().pending, null);
    assert.equal(authority.sourceWriteIntents().configurations.size, 0);
    if (outcome === "completed") resolve(plan); else reject(new ApiError(403, "DENIED", false));
    await first;
    assert.equal(authority.sourceWriteIntents().configurations.size, 0);
    assert.equal(flow.snapshot().plan, null); assert.equal(flow.snapshot().reason, null);
    const nextPlan = { ...plan, plan_id: "next-plan" };
    client.configurationPlan = async (_request, key) => { keys.push(key); return nextPlan; };
    await flow.preview([mapping]);
    assert.equal(keys.length, 2); assert.notEqual(keys[0], keys[1]);
    assert.equal(flow.snapshot().phase, "preview"); assert.equal(flow.snapshot().plan?.plan_id, "next-plan");
  });
}

for (const restored of [false, true]) {
  test(`cancelling an uncertain preview ${restored ? "after re-entry" : "in its original flow"} consumes it without receipt recovery`, async () => {
    const { flow, client, authority } = fixture(); await flow.load();
    const keys: string[] = [];
    client.configurationPlan = async (_request, key) => { keys.push(key); throw new PendingSourceWriteError(key); };
    client.operation = async () => { throw Error("cancelled previews must not be recovered"); };
    await flow.preview([mapping]); assert.equal(flow.snapshot().phase, "pending");
    let current = flow;
    if (restored) {
      flow.dispose(); current = new SourceConfigurationFlow(client, "p", authority); await current.load();
      assert.equal(current.snapshot().phase, "pending");
    }
    current.cancel();
    assert.equal(current.snapshot().phase, "editing"); assert.equal(current.snapshot().pending, null);
    assert.equal(authority.sourceWriteIntents().configurations.size, 0);
    client.configurationPlan = async (_request, key) => { keys.push(key); return plan; };
    await current.preview([mapping]);
    assert.equal(keys.length, 2); assert.notEqual(keys[0], keys[1]); assert.equal(current.snapshot().phase, "preview");
  });
}

for (const phase of ["committing", "pending"] as const) {
  test(`cancel refuses a ${phase} commit and keeps its original intent recoverable`, async () => {
    const { flow, client, authority } = fixture(); await flow.load(); await flow.preview([mapping]);
    let resolve!: (value: ConfigurationCommitView) => void;
    client.configurationPlanCommit = async (_plan, _request, key) => {
      if (phase === "pending") throw new PendingSourceWriteError(key);
      return new Promise((yes) => { resolve = yes; });
    };
    const write = flow.commit(true);
    if (phase === "pending") await write;
    const intent = authority.sourceWriteIntents().configurations.get("p");
    assert.equal(flow.snapshot().phase, phase);
    flow.cancel();
    assert.equal(flow.snapshot().phase, phase);
    assert.equal(authority.sourceWriteIntents().configurations.get("p"), intent);
    if (phase === "committing") { resolve({ plan_id: plan.plan_id, config_revision: 8, impact: {} }); await write; }
    else {
      client.operation = async (key) => {
        assert.equal(key, plan.plan_id);
        return { operation_id: key, state: "completed", original_status: 200, result_metadata: { config_revision: 8 }, result_ref: null, job_id: null };
      };
      await flow.recover();
    }
    assert.equal(flow.snapshot().phase, "done");
    assert.equal(authority.sourceWriteIntents().configurations.size, 0);
  });
}


test("normal source settings preserve pinned/current and provider defaults through preview and explicit confirmation", async () => {
  const { flow, calls } = fixture(); await flow.load();
  await flow.preview([mapping], { source_mode_default: "current", provider_id: "chosen-provider" });
  assert.deepEqual((calls[0]![1] as { changes: unknown }).changes, { sources: [mapping], source_mode_default: "current", provider_id: "chosen-provider" });
  assert.deepEqual(flow.snapshot().previewDefaults, { source_mode_default: "current", provider_id: "chosen-provider" });
  assert.equal(flow.snapshot().expandsScope, false);
  assert.deepEqual(flow.snapshot().providerChange, { before: null, after: "chosen-provider" });
  await flow.commit(false); assert.equal(calls.length, 1);
  await flow.commit(true); assert.equal(flow.snapshot().phase, "done");
  assert.deepEqual(flow.snapshot().previewSources[0]!.provider_ids, [], "a default does not grant disclosure on the source");
});


for (const kind of ["provider", "source", "both"] as const) {
  test(`${kind} changes name their real impact and use one explicit confirmation`, async () => {
    const { flow, client, calls } = fixture();
    client.configurationView = async () => ({ config_revision: 7, etag: "tag", configuration: { projects: { p: { sources: [mapping], provider_id: "old-provider" } } } });
    await flow.load();
    const sources = kind === "provider" ? [mapping] : [{ ...mapping, provider_ids: ["allowed-provider"] }];
    const provider = kind === "source" ? "old-provider" : "next-provider";
    await flow.preview(sources, { source_mode_default: "current", provider_id: provider });
    assert.equal(flow.snapshot().expandsScope, kind !== "provider");
    assert.deepEqual(flow.snapshot().providerChange, kind === "source" ? null : { before: "old-provider", after: "next-provider" });
    const expected = kind === "provider" ? "기본 Provider 변경 영향을 확인했습니다" : kind === "source" ? "출처/공개 범위 확대 영향을 확인했습니다" : "출처/공개 범위 확대와 기본 Provider 변경 영향을 확인했습니다";
    assert.equal(sourceConfirmation(flow.snapshot()), expected);
    await flow.commit(false); assert.equal(calls.length, 1, "no commit before impact confirmation");
    await flow.commit(true); assert.equal(flow.snapshot().phase, "done");
    assert.deepEqual(flow.snapshot().previewSources, sources, "a default never edits the source allow list");
  });
}

test("clearing a provider default names old to none and retains the ordinary Apply flow", async () => {
  const { flow, client, calls } = fixture();
  client.configurationView = async () => ({ config_revision: 7, etag: "tag", configuration: { projects: { p: { sources: [mapping], provider_id: "old-provider" } } } });
  await flow.load(); await flow.preview([mapping], { source_mode_default: "pinned", provider_id: null });
  assert.deepEqual(flow.snapshot().providerChange, { before: "old-provider", after: null });
  assert.equal(flow.snapshot().expandsScope, false); assert.equal(sourceConfirmation(flow.snapshot()), null);
  await flow.commit(false); assert.equal(flow.snapshot().phase, "done");
  assert.deepEqual((calls[0]![1] as { changes: unknown }).changes, { sources: [mapping], source_mode_default: "pinned", provider_id: null });
});

test("initial configuration read failure has no current values or mutation and retries the actual read", async () => {
  const { flow, client, calls } = fixture(); let reads = 0;
  client.configurationView = async () => { reads++; throw new ApiError(503, "SERVICE_RECOVERING", true); };
  await flow.load();
  assert.equal(flow.snapshot().phase, "error"); assert.equal(flow.snapshot().configuration, null);
  await flow.preview([mapping]); assert.equal(calls.length, 0);
  client.configurationView = async () => { reads++; return { config_revision: 9, etag: "actual", configuration: { projects: { p: { sources: [mapping], source_mode_default: "current", provider_id: "remembered-provider" } } } }; };
  await flow.load();
  assert.equal(reads, 2); assert.equal(flow.snapshot().phase, "editing");
  assert.equal(flow.snapshot().configuration!.configuration.projects.p!.provider_id, "remembered-provider");
  await flow.preview([mapping]); assert.equal((calls[0]![1] as { expected_config_revision: number }).expected_config_revision, 9);
});

test("failed reread preserves loaded configuration and a failed preview keeps its editable basis", async () => {
  const { flow, client } = fixture(); await flow.load(); const loaded = flow.snapshot().configuration;
  client.configurationPlan = async () => { throw new ApiError(422, "INVALID_REQUEST", false); };
  await flow.preview([{ ...mapping, root_path: "/owner-draft" }]);
  assert.equal(flow.snapshot().phase, "error"); assert.equal(flow.snapshot().configuration, loaded);
  client.configurationView = async () => { throw new ApiError(503, "SERVICE_RECOVERING", true); };
  await flow.load(); assert.equal(flow.snapshot().phase, "error"); assert.equal(flow.snapshot().configuration, loaded);
});


test("changing the provider while narrowing disclosure is a provider change without source expansion", async () => {
  const { flow, client } = fixture();
  client.configurationView = async () => ({ config_revision: 7, etag: "tag", configuration: { projects: { p: { sources: [{ ...mapping, provider_ids: ["old-allowed"] }], provider_id: "old-provider" } } } });
  await flow.load(); await flow.preview([mapping], { source_mode_default: "pinned", provider_id: "new-provider" });
  assert.equal(flow.snapshot().expandsScope, false);
  assert.deepEqual(flow.snapshot().providerChange, { before: "old-provider", after: "new-provider" });
  assert.equal(sourceConfirmation(flow.snapshot()), "기본 Provider 변경 영향을 확인했습니다");
  assert.deepEqual(flow.snapshot().previewSources[0]!.provider_ids, [], "the visible source plan narrows disclosure independently of its provider default");
});
