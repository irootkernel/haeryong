// Focused HTTP transport tests against the canonical wire envelopes: the
// durable-write 202 accepted reservation body produced by jsonBytesPending,
// terminal 200/201 applications, typed 409 reconciliation errors, and the
// stale-era discard. fetch is stubbed at the boundary; no network is used.
import test from "node:test";
import assert from "node:assert/strict";
import { HttpHaeryongClient } from "./http.ts";
import { ApiError, StaleResponseError, UncertainResponseError } from "./types.ts";
import type { PairingDecisionRequest } from "./types.ts";

function decisionRequest(): PairingDecisionRequest {
  return {
    pairing_id: "pair-1",
    code: "123456",
    origin: "http://127.0.0.1:9270",
    etag: '"pairing-pair-1-r1"',
    operation_id: "11111111-1111-4111-8111-111111111111",
    action: "approve",
  };
}

function stubFetch(handler: (input: RequestInfo, init?: RequestInit) => Promise<Response>) {
  const original = globalThis.fetch;
  globalThis.fetch = ((input: RequestInfo, init?: RequestInit) => handler(input, init)) as typeof fetch;
  return () => {
    globalThis.fetch = original;
  };
}

test("a terminal application maps to committed", async () => {
  const restore = stubFetch(async () => new Response(JSON.stringify({ data: { pairing_id: "pair-1", state: "approved" } }), { status: 200 }));
  try {
    const client = new HttpHaeryongClient();
    const result = await client.decidePairing(decisionRequest());
    assert.deepEqual(result.kind, "committed");
  } finally {
    restore();
  }
});

test("the canonical HTTP 202 accepted reservation envelope maps to accepted", async () => {
  // Exact jsonBytesPending shape from internal/application/storage/operations.go.
  const restore = stubFetch(
    async () =>
      new Response(
        JSON.stringify({
          data: {
            operation_id: "11111111-1111-4111-8111-111111111111",
            state: "accepted",
            operation_url: "/api/v1/operations/11111111-1111-4111-8111-111111111111",
          },
        }),
        { status: 202 },
      ),
  );
  try {
    const client = new HttpHaeryongClient();
    const result = await client.decidePairing(decisionRequest());
    assert.deepEqual(result.kind, "accepted");
    const body = result.body as { state: string };
    assert.equal(body.state, "accepted");
  } finally {
    restore();
  }
});

test("409 OPERATION_RECONCILIATION_REQUIRED propagates as a typed error", async () => {
  const restore = stubFetch(
    async () =>
      new Response(JSON.stringify({ error: { code: "OPERATION_RECONCILIATION_REQUIRED", message: "Conflict", retryable: false } }), { status: 409 }),
  );
  try {
    const client = new HttpHaeryongClient();
    await assert.rejects(
      client.decidePairing(decisionRequest()),
      (error: unknown): boolean => {
        assert.ok(error instanceof ApiError);
        assert.equal((error as ApiError).status, 409);
        assert.equal((error as ApiError).code, "OPERATION_RECONCILIATION_REQUIRED");
        return true;
      },
    );
  } finally {
    restore();
  }
});

test("a 200 response with an errored body stream is uncertain, not failure", async () => {
  const restore = stubFetch(
    async () =>
      new Response(
        new ReadableStream({
          start(controller) {
            controller.error(new Error("truncated transfer"));
          },
        }),
        { status: 200 },
      ),
  );
  try {
    const client = new HttpHaeryongClient();
    await assert.rejects(
      client.decidePairing(decisionRequest()),
      (error: unknown): boolean => {
        assert.ok(error instanceof UncertainResponseError, String(error));
        return true;
      },
    );
  } finally {
    restore();
  }
});

test("a 200 response with malformed JSON is uncertain, not failure", async () => {
  const restore = stubFetch(async () => new Response('{"data": {"pairing_', { status: 200 }));
  try {
    const client = new HttpHaeryongClient();
    await assert.rejects(
      client.decidePairing(decisionRequest()),
      (error: unknown): boolean => {
        assert.ok(error instanceof UncertainResponseError, String(error));
        return true;
      },
    );
  } finally {
    restore();
  }
});

test("a response landing after invalidate is discarded as stale", async () => {
  const gate = { resolve: null as null | ((value: Response) => void) };
  const restore = stubFetch(
    () =>
      new Promise<Response>((resolve) => {
        gate.resolve = resolve;
      }),
  );
  try {
    const client = new HttpHaeryongClient();
    const call = client.decidePairing(decisionRequest());
    client.invalidate();
    gate.resolve?.(new Response(JSON.stringify({ data: {} }), { status: 200 }));
    await assert.rejects(call, (error: unknown) => error instanceof StaleResponseError);
  } finally {
    restore();
  }
});

test("requests carry the bound bearer and required decision headers", async () => {
  const seen: RequestInit[] = [];
  const restore = stubFetch(async (_input, init) => {
    seen.push(init ?? {});
    return new Response(JSON.stringify({ data: {} }), { status: 200 });
  });
  try {
    const client = new HttpHaeryongClient();
    client.setCredential("token-header");
    await client.decidePairing(decisionRequest());
    assert.equal(seen.length, 1);
    const headers = new Headers(seen[0].headers);
    assert.equal(headers.get("Authorization"), "Bearer token-header");
    assert.equal(headers.get("If-Match"), '"pairing-pair-1-r1"');
    assert.equal(headers.get("Idempotency-Key"), "11111111-1111-4111-8111-111111111111");
    assert.equal(headers.get("Content-Type"), "application/json");
    assert.equal(seen[0].credentials, "omit");
    assert.equal(seen[0].redirect, "error");
  } finally {
    restore();
  }
});

test("context staging keeps the exact normalized binding, header-only credential and separate U/Q intents", async () => {
  const calls: { url: URL; body: unknown; headers: Headers }[] = [];
  const restore = stubFetch(async (input, init) => {
    calls.push({ url: new URL(String(input), "http://trusted.invalid"), body: JSON.parse(String(init?.body)), headers: new Headers(init?.headers) });
    return new Response(JSON.stringify({ data: { context_ref: "context", bytes_sha256: "a".repeat(64), media_type: "application/json" } }), { status: 201 });
  });
  try {
    const client = new HttpHaeryongClient(); client.setCredential("owner-secret");
    const binding = { version_id: "version", page_path: "pages/한글%23.html", render_instance_id: "render" };
    await client.createContextUpload(binding, "U");
    await client.consumeContext(binding, "upload", "Q");
    assert.deepEqual(calls[0]!.body, { purpose: "context", input_mode: "object", target: binding });
    assert.deepEqual(calls[1]!.body, { upload_id: "upload" });
    assert.equal(calls[1]!.url.searchParams.get("page_path"), binding.page_path);
    assert.equal(calls[1]!.url.searchParams.get("input"), "upload");
    assert.equal(calls[1]!.url.searchParams.has("upload_id"), false);
    assert.equal(calls[0]!.headers.get("Idempotency-Key"), "U"); assert.equal(calls[1]!.headers.get("Idempotency-Key"), "Q");
    for (const call of calls) { assert.equal(call.headers.get("Authorization"), "Bearer owner-secret"); assert.equal(call.url.href.includes("owner-secret"), false); }
    await client.consumeContext({ ...binding, page_path: null, render_instance_id: null }, "upload", "Q2");
    assert.equal(calls[2]!.url.searchParams.has("page_path"), false); assert.equal(calls[2]!.url.searchParams.has("render_instance_id"), false);
  } finally { restore(); }
});

test("authorized failure details and explicit non-retryable 503 survive JSON, chunk and stream transports", async () => {
  const details = { reason: "renderer_unavailable_account", remedy: "use a supported account lookup" };
  const restore = stubFetch(async () => new Response(JSON.stringify({ error: { code: "FEATURE_NOT_READY", retryable: false, details } }), { status: 503 }));
  try {
    const client = new HttpHaeryongClient();
    const calls = [
      () => client.consumeContext({ version_id: "v", page_path: null, render_instance_id: null }, "upload", "Q"),
      () => client.putUploadEntry("upload", { path: "_input", kind: "file", offset: 0, bytes: new Uint8Array(0), chunkSha256: "0".repeat(64), final: true }),
      () => client.packageStream("v"),
    ];
    for (const call of calls) await assert.rejects(call(), (error: unknown) => {
      assert.ok(error instanceof ApiError); assert.equal(error.status, 503); assert.equal(error.retryable, false); assert.deepEqual(error.details, details); return true;
    });
  } finally { restore(); }
});

test("verifyContextFresh accepts only the exact 304 proof with marker and ETag", async () => {
  const digest = "a".repeat(64);
  const etag = `"sha256-${digest}"`;
  const binding = { version_id: "33333333-3333-4333-8333-333333333333", page_path: "pages/한글.html", render_instance_id: null };
  const expectedBinding = Buffer.from(JSON.stringify(binding), "utf8").toString("base64url");
  let restore = stubFetch(async (input: RequestInfo, init?: RequestInit) => {
    assert.equal(String(input), "/api/v1/contexts/ctx-1");
    const headers = new Headers(init?.headers);
    assert.equal(headers.get("Haeryong-Context-Binding"), expectedBinding);
    assert.equal(headers.get("If-None-Match"), etag);
    return new Response(null, { status: 304, headers: { ETag: etag, "Haeryong-Context-Verified": "fresh" } });
  });
  try {
    const client = new HttpHaeryongClient();
    await client.verifyContextFresh("ctx-1", binding, digest);
  } finally {
    restore();
  }
  for (const [name, response] of [
    ["wrong-marker", new Response(null, { status: 304, headers: { ETag: etag, "Haeryong-Context-Verified": "stale" } })],
    ["missing-marker", new Response(null, { status: 304, headers: { ETag: etag } })],
    ["wrong-etag", new Response(null, { status: 304, headers: { ETag: `"sha256-${"b".repeat(64)}"`, "Haeryong-Context-Verified": "fresh" } })],
    ["body-response", new Response(JSON.stringify({ data: {} }), { status: 200, headers: { "Content-Type": "application/json" } })],
  ] as const) {
    restore = stubFetch(async () => response.clone());
    try {
      const client = new HttpHaeryongClient();
      await assert.rejects(client.verifyContextFresh("ctx-1", binding, digest), (error: unknown) => error instanceof ApiError && error.code === "CONTEXT_PROOF_INVALID" && !error.retryable, name);
    } finally {
      restore();
    }
  }
});

test("verifyContextFresh maps binding rejection and transport failures with their server retryability", async () => {
  const digest = "a".repeat(64);
  const binding = { version_id: "33333333-3333-4333-8333-333333333333", page_path: null, render_instance_id: null };
  let restore = stubFetch(
    async () => new Response(JSON.stringify({ error: { code: "PRECONDITION_FAILED", message: "Precondition Failed", retryable: false, details: {} } }), { status: 412, headers: { "Content-Type": "application/json" } }),
  );
  try {
    const client = new HttpHaeryongClient();
    await assert.rejects(client.verifyContextFresh("ctx-1", binding, digest), (error: unknown) => error instanceof ApiError && error.status === 412 && error.code === "PRECONDITION_FAILED" && error.retryable === false);
  } finally {
    restore();
  }
  restore = stubFetch(
    async () => new Response(JSON.stringify({ error: { code: "SERVICE_RECOVERING", message: "Service Unavailable", retryable: true, details: {} } }), { status: 503, headers: { "Content-Type": "application/json" } }),
  );
  try {
    const client = new HttpHaeryongClient();
    await assert.rejects(client.verifyContextFresh("ctx-1", binding, digest), (error: unknown) => error instanceof ApiError && error.retryable === true);
  } finally {
    restore();
  }
  restore = stubFetch(async () => new Response(null, { status: 304, headers: { ETag: `"sha256-${digest}"`, "Haeryong-Context-Verified": "fresh" } }));
  try {
    const client = new HttpHaeryongClient();
    const pending = client.verifyContextFresh("ctx-1", binding, digest);
    client.invalidate();
    await assert.rejects(pending, (error: unknown) => error instanceof StaleResponseError);
  } finally {
    restore();
  }
});

test("attachment bytes use the exact file query, header credential and streamed authority guard", async () => {
  const path = "nested/한글+%2F\\\r\n.bin", bytes = Uint8Array.from({ length: 256 }, (_, i) => i);
  const restore = stubFetch(async (input, init) => {
    const url = new URL(String(input), "http://trusted.invalid");
    assert.equal(url.pathname, "/api/v1/versions/version/file");
    assert.equal(url.searchParams.get("path"), path);
    assert.equal(new Headers(init?.headers).get("Authorization"), "Bearer private-test-token");
    assert.equal(init?.credentials, "omit"); assert.equal(init?.redirect, "error");
    return new Response(bytes);
  });
  try {
    const client = new HttpHaeryongClient(); client.setCredential("private-test-token");
    const stream = await client.fileStream("version", path);
    assert.deepEqual((await stream.body.getReader().read()).value, bytes);
    const stale = await client.fileStream("version", path);
    client.invalidate();
    await assert.rejects(stale.body.getReader().read(), StaleResponseError);
  } finally { restore(); }
});

// TASK-003-04 shared presentation adapter operations: fixed routes only,
// cursor/scope/target typed query bindings, the metadata stream's captured
// ETag and the envelope's separate applicable freshness summary.
test("versionSuppliedMetadata exposes the stream with the immutable object's ETag", async () => {
  const body = JSON.stringify({ version: { source_intent: "historical" } });
  let requested = "";
  const restore = stubFetch(async (input) => {
    requested = String(input);
    return new Response(body, { status: 200, headers: { ETag: '"sha256-abc"' } });
  });
  try {
    const client = new HttpHaeryongClient();
    const opened = await client.versionSuppliedMetadata("v1");
    assert.equal(opened.etag, '"sha256-abc"');
    const reader = opened.stream.body.getReader();
    const first = await reader.read();
    assert.deepEqual(new TextDecoder().decode(first.value), body);
    assert.equal(first.done, false);
    opened.stream.cancel();
    assert.ok(requested.includes("/api/v1/versions/v1/metadata?kind=supplied"), requested);
  } finally {
    restore();
  }
});

test("versionSuppliedMetadata propagates absent metadata as the typed 404", async () => {
  const restore = stubFetch(async () => new Response(JSON.stringify({ error: { code: "NOT_FOUND", message: "absent", retryable: false, details: { reason: "metadata_not_supplied" } } }), { status: 404 }));
  try {
    const client = new HttpHaeryongClient();
    await assert.rejects(client.versionSuppliedMetadata("v1"), (error: unknown) => error instanceof ApiError && error.status === 404);
  } finally {
    restore();
  }
});

test("versionEvidence preserves opaque IDs through actual URL parsing", async () => {
  const observed: string[] = [];
  const restore = stubFetch(async (input) => {
    const request = new Request(new URL(String(input), "http://127.0.0.1:9270"));
    const url = new URL(request.url);
    assert.equal(url.pathname, "/api/v1/versions/v1/source-evidence");
    const id = url.searchParams.get("evidence_id");
    observed.push(id!);
    const view = { version_id: "v1", evidence_id: id, authored: { id, capture_status: "reference_only" }, source: {}, availability: "unavailable", bytes_origin: null, reason: "not_read_by_view", service_verification: null, verifications_url: "/api/v1/versions/v1/source-evidence-verifications?" + url.searchParams };
    return new Response(JSON.stringify({ data: view }), { status: 200 });
  });
  try {
    const client = new HttpHaeryongClient();
    const ids = [".", "..", "%2e", "a.", "evidence/상세", "a +", "x?y#z", "verifications"];
    for (const id of ids) {
      const result = await client.versionEvidence("v1", id);
      assert.equal(result.evidence_id, id);
      assert.equal(result.authored.id, id);
      assert.equal(result.reason, "not_read_by_view");
      assert.equal(result.service_verification, null);
    }
    assert.deepEqual(observed, ids);
  } finally {
    restore();
  }
});

test("evidenceVerifications, versionAssessments and assessmentCoverage use the typed query bindings", async () => {
  const requests: string[] = [];
  const restore = stubFetch(async (input) => {
    const url = String(input);
    requests.push(url);
    if (url.includes("/source-evidence-verifications")) return new Response(JSON.stringify({ data: { items: [], next_cursor: null } }), { status: 200 });
    if (url.includes("/assessments/coverage")) return new Response(JSON.stringify({ data: { items: [], next_cursor: null } }), { status: 200 });
    return new Response(JSON.stringify({ data: { items: [], next_cursor: null, applicable: null } }), { status: 200 });
  });
  try {
    const client = new HttpHaeryongClient();
    await client.evidenceVerifications("v1", "e/상세", "cursor-2");
    await client.versionAssessments("v1", { targetSetId: "t1", scope: { kind: "region", pagePath: "a.html", regionId: "개요" }, cursor: "c" });
    await client.assessmentCoverage("v1", "c1", "page-2");
    const history = new URL(new Request(new URL(requests[0], "http://127.0.0.1:9270")).url);
    assert.equal(history.pathname, "/api/v1/versions/v1/source-evidence-verifications");
    assert.equal(history.searchParams.get("evidence_id"), "e/상세");
    assert.equal(history.searchParams.get("cursor"), "cursor-2");
    assert.match(requests[1], /\/api\/v1\/versions\/v1\/assessments\?/);
    assert.ok(requests[1].includes("target_set_id=t1") && requests[1].includes("scope_kind=region") && requests[1].includes("page_path=a.html") && requests[1].includes("region_id="), requests[1]);
    assert.match(requests[2], /\/api\/v1\/versions\/v1\/assessments\/coverage\?coverage_set_id=c1&cursor=page-2$/);
  } finally {
    restore();
  }
});

test("versionAssessments keeps the separately selected applicable summary beside history", async () => {
  const applicable = { source_intent: "historical", target_set_id: "t1", captured_at: "2026-10-19T00:00:00Z", scope: { kind: "version", page_path: null, region_id: null }, status: "not_assessed", coverage: { coverage_set_id: null, unit_kind: "version", eligible: 0, assessed: 0, complete: false, units_url: null }, assessed_at: null, reassessment_required: false };
  const restore = stubFetch(async () => new Response(JSON.stringify({ data: { items: [], next_cursor: null, applicable } }), { status: 200 }));
  try {
    const client = new HttpHaeryongClient();
    const page = await client.versionAssessments("v1");
    assert.deepEqual(page.applicable, applicable);
    assert.deepEqual(page.items, []);
  } finally {
    restore();
  }
});

test("FEATURE_NOT_READY from the future catalog surfaces as a typed 503, not a transport failure", async () => {
  const restore = stubFetch(async () => new Response(JSON.stringify({ error: { code: "FEATURE_NOT_READY", message: "future capability", retryable: false } }), { status: 503 }));
  try {
    const client = new HttpHaeryongClient();
    for (const operation of [client.versionAssessments("v1"), client.assessmentCoverage("v1", "c1"), client.evidenceVerifications("v1", "e1")]) {
      await assert.rejects(operation, (error: unknown) => error instanceof ApiError && error.status === 503 && error.code === "FEATURE_NOT_READY");
    }
  } finally {
    restore();
  }
});

test("source writes reconcile 201, 202 and unreadable success with the original key and no second POST", async () => {
  for (const observation of [false, true]) for (const mode of ["created", "accepted", "uncertain"]) {
    const calls: Array<{ path: string; method: string; key: string | null; body: unknown }> = [];
    let polls = 0;
    const result = { operation_id: "original-key", outcome: "captured", run_id: "r1" };
    const restore = stubFetch(async (input, init) => {
      const path = String(input), method = init?.method ?? "GET";
      calls.push({ path, method, key: new Headers(init?.headers).get("Idempotency-Key"), body: init?.body ? JSON.parse(String(init.body)) : null });
      if (method === "POST") {
        assert.equal(calls.length, 1);
        if (mode === "uncertain") return new Response("{", { status: 201 });
        return new Response(JSON.stringify({ data: mode === "accepted" ? { operation_id: "original-key", state: "accepted" } : result }), { status: mode === "accepted" ? 202 : 201 });
      }
      polls++;
      assert.equal(path, "/api/v1/operations/original-key");
      return new Response(JSON.stringify({ data: { operation_id: "original-key", state: polls === 1 ? "accepted" : "completed", original_status: polls === 1 ? null : 201, result_metadata: polls === 1 ? null : result } }));
    });
    try {
      const client = new HttpHaeryongClient();
      const received = observation ? await client.runSourceObservation("v1", { scope: { kind: "version" } }, "original-key") : await client.verifySourceEvidence("v1", "../evidence/상세", { expected_bytes_sha256: "a".repeat(64) }, "original-key");
      assert.deepEqual(received, result);
      assert.equal(calls.filter((c) => c.method === "POST").length, 1);
      assert.equal(calls[0]!.key, "original-key");
      const url = new URL(calls[0]!.path, "http://127.0.0.1");
      assert.equal(url.pathname, `/api/v1/versions/v1/${observation ? "source-observations" : "source-evidence-verifications"}`);
      if (!observation) assert.equal(url.searchParams.get("evidence_id"), "../evidence/상세");
      assert.equal(polls, mode === "created" ? 0 : 2);
    } finally { restore(); }
  }
});

test("configuration, registration, sources and attempt-attributed coverage use fixed HTTP routes", async () => {
  const calls: Array<[string, string, unknown, string | null]> = [];
  const restore = stubFetch(async (input, init) => {
    calls.push([String(input), init?.method ?? "GET", init?.body ? JSON.parse(String(init.body)) : null, new Headers(init?.headers).get("Idempotency-Key")]);
    return new Response(JSON.stringify({ data: { items: [], next_cursor: null } }), { status: 200 });
  });
  try {
    const client = new HttpHaeryongClient();
    await client.configurationView();
    await client.configurationPlan({ scope: { kind: "project", project_id: "p" }, expected_config_revision: 3, changes: { sources: [] } }, "plan-key");
    await client.configurationPlanCommit("plan/1", { expected_config_revision: 3, confirmed: true }, "commit-key");
    await client.projectRegister("Project", "project-key");
    await client.projectSources("p", "page2");
    await client.assessmentCoverage("v", "set", "page2", "11111111-2222-4333-8444-555555555555");
    assert.deepEqual(calls.map((c) => c[0]), ["/api/v1/configuration", "/api/v1/configuration/plans", "/api/v1/configuration/plans/plan%2F1/commit", "/api/v1/projects", "/api/v1/projects/p/sources?cursor=page2", "/api/v1/versions/v/assessments/coverage?coverage_set_id=set&attempt_run_id=11111111-2222-4333-8444-555555555555&cursor=page2"]);
    assert.deepEqual(calls.slice(1,4).map((c) => c[3]), ["plan-key", "commit-key", "project-key"]);
    assert.deepEqual(calls[3]![2], { name: "Project" });
  } finally { restore(); }
});

test("source receipt failure is definitive while a missing/reconciling receipt retains the original key", async () => {
  const { PendingSourceWriteError } = await import("./source-operations.ts");
  for (const state of ["failed", "missing", "reconciling"]) {
    let writes = 0;
    const restore = stubFetch(async (_input, init) => {
      if (init?.method === "POST") { writes++; return new Response("{", { status: 201 }); }
      if (state === "missing") return new Response(JSON.stringify({ error: { code: "NOT_FOUND", retryable: false } }), { status: 404 });
      return new Response(JSON.stringify({ data: { operation_id: "original", state, original_status: state === "failed" ? 503 : null, result_metadata: { error: { code: "FILE_ACCESS_ERROR", retryable: true, details: { reason: "source_read_failed" } } } } }));
    });
    try {
      const client = new HttpHaeryongClient();
      await assert.rejects(client.verifySourceEvidence("v", "e", {}, "original"), (error: unknown) => state === "failed" ? error instanceof ApiError && error.code === "FILE_ACCESS_ERROR" && error.details.reason === "source_read_failed" : error instanceof PendingSourceWriteError && error.operationId === "original");
      assert.equal(writes, 1);
    } finally { restore(); }
  }
});

test("HTTP verification forwards a typed excerpt upload reference object", async () => {
  const request: import("./types.ts").EvidenceVerificationRequest = { expected_excerpt_ref: { upload_id: "11111111-2222-4333-8444-555555555555" } };
  let received: unknown;
  const restore = stubFetch(async (_input, init) => {
    received = JSON.parse(String(init?.body));
    return new Response(JSON.stringify({ data: { outcome: "matched" } }), { status: 201 });
  });
  try {
    await new HttpHaeryongClient().verifySourceEvidence("v", "e", request, "k");
    assert.deepEqual(received, request);
  } finally { restore(); }
});


test("HTTP provider status rejects unknown authority state or missing allowance and preserves false/null",async()=>{
 for(const [state,allowance,valid] of [["paused",false,true],["quota_unavailable",null,true],["future_state",true,false],["ready",undefined,false]] as const){
  const restore=stubFetch(async()=>new Response(JSON.stringify({data:{provider_id:"codex",state,ordinary_usage_allowed:allowance}})));
  try{const request=new HttpHaeryongClient().providerStatus();if(valid){assert.equal((await request).ordinary_usage_allowed,allowance)}else{await assert.rejects(request,/지원되는 제공자 상태/);}}finally{restore()}
 }
});

test("GCLIENT01 generation response loss reconciles the original key and draft mutations retain exact validators", async () => {
  const seen: Array<{path:string; method:string; headers:Headers; body:unknown}> = [];
  const key = "11111111-1111-4111-8111-111111111111";
  const accepted = { operation_id:key,job_id:"job",draft_id:"draft",intent:"draft",job_url:"/api/v1/jobs/job" };
  const restore = stubFetch(async (input, init) => {
    const path=String(input); seen.push({path,method:init?.method??"GET",headers:new Headers(init?.headers),body:init?.body?JSON.parse(String(init.body)):null});
    if(path==="/api/v1/generations") throw new TypeError("response lost after dispatch");
    if(path===`/api/v1/operations/${key}`) return new Response(JSON.stringify({data:{operation_id:key,state:"completed",original_status:202,result_metadata:accepted}}));
    return new Response(JSON.stringify({data:{draft_id:"draft",state:"ready"}}));
  });
  try {
    const client=new HttpHaeryongClient();
    assert.deepEqual(await client.generationStart({project_id:"p",document_id:null,intent:"draft",prompt:"explain",source_selection:{selections:[]}},key),accepted);
    assert.deepEqual(seen.map(x=>[x.path,x.method]),[["/api/v1/generations","POST"],[`/api/v1/operations/${key}`,"GET"]]);
    assert.equal(seen[0]!.headers.get("Idempotency-Key"),key);
    await client.draftPatch("d/+%",{action:"reject"},'"exact"',key);
    assert.equal(seen[2]!.path,"/api/v1/drafts/d%2F%2B%25");
    assert.equal(seen[2]!.headers.get("If-Match"),'"exact"');
    assert.deepEqual(seen[2]!.body,{action:"reject"});
    await client.draftPublish("d/+%",'"next"',key);
    assert.equal(seen[3]!.method,"POST");assert.deepEqual(seen[3]!.body,{});
    assert.equal(seen[3]!.headers.get("If-Match"),'"next"');
    await client.draftList("p/+%","c/+%");
    const url=new URL(seen[4]!.path,"http://local.invalid");
    assert.equal(url.searchParams.get("project_id"),"p/+%");assert.equal(url.searchParams.get("cursor"),"c/+%");
  } finally {restore();}
});

test("GCLIENT04 generation submission freezes prompt identity, preserves original K after uncertainty and blocks changed re-selection before upload",async()=>{
 const {GenerationSubmission}=await import("./generation.ts");
 const records=new Map<string,import("./generation.ts").GenerationRecord>();
 const journal={read:()=>[...records.values()].map(v=>structuredClone(v)),save:(r:import("./generation.ts").GenerationRecord)=>{records.set(r.operationId,structuredClone(r));}};
 const uploads={save:()=>{},load:()=>({active:null,records:[]}),setActive:()=>{},discard:()=>{}};
 let dispatches=0;
 const accepted={operation_id:"",job_id:"j",draft_id:"d",intent:"draft" as const,job_url:"/api/v1/jobs/j"};
 const fake={generationStart:async()=>{dispatches++;throw new Error("unexpected new dispatch");},operation:async(id:string)=>({operation_id:id,state:"completed",original_status:202,result_metadata:{...accepted,operation_id:id}})};
 const submit=new GenerationSubmission(fake as unknown as import("./types.ts").HaeryongClient,journal,"lib","owner",uploads,async(_name,action)=>action(),()=>"runtime-before");
 const request={project_id:"p",document_id:null,intent:"draft" as const,source_selection:{selections:[]}};
 const record=submit.capture(request,"original prompt",()=>{});
 assert.equal(JSON.stringify(records.get(record.operationId)).includes("original prompt"),false);
 await assert.rejects(submit.send(record,"changed prompt",{}),/원래 요청/);assert.equal(dispatches,0);
 record.dispatchReady=true;journal.save(record);
 const result=await submit.send(record,"",{});assert.equal(result.operation_id,record.operationId);assert.equal(dispatches,0);
 assert.equal(records.get(record.operationId)!.accepted!.job_id,"j");
 let saves=0;const failing={read:()=>[],save:()=>{saves++;throw new Error("storage unavailable");}};
 const blocked=new GenerationSubmission(fake as unknown as import("./types.ts").HaeryongClient,failing,"lib","owner",uploads,async(_name,action)=>action(),()=>"runtime-before");
 assert.throws(()=>blocked.capture(request,"request",()=>{}),/storage unavailable/);assert.equal(saves,1);assert.equal(dispatches,0);
});


test("GCLIENT06 resume uses only the saved job decision, generation, opaque validator and original key",async()=>{
 const seen:unknown[]=[];const restore=stubFetch(async(input,init)=>{seen.push([String(input),init?.method,new Headers(init?.headers).get("If-Match"),new Headers(init?.headers).get("Idempotency-Key"),JSON.parse(String(init?.body))]);return new Response(JSON.stringify({data:{job_id:"j",decision:"continue"}}));});
 try{await new HttpHaeryongClient().jobResume("j/+%","continue",7,'"opaque-job"',"K");assert.deepEqual(seen,[["/api/v1/jobs/j%2F%2B%25/resume","POST",'"opaque-job"',"K",{decision:"continue",expected_attempt_generation:7}]]);}finally{restore();}
});


test("T4R2RESUME07 the actual HTTP client recovers a resume receipt across runtime with no resume POST",async()=>{
 const {recoverGenerationResume}=await import("./generation.ts");
 const seen:unknown[]=[];let found=false;
 const restore=stubFetch(async(input,init)=>{
  seen.push([String(input),init?.method??"GET"]);
  return found?new Response(JSON.stringify({data:{operation_id:"resume",state:"completed",original_status:200,result_metadata:{job_id:"job"}}})):new Response(JSON.stringify({error:{code:"NOT_FOUND",retryable:false}}),{status:404});
 });
 const record={operationId:"parent",request:{project_id:"p",document_id:null,intent:"draft" as const,source_selection:{selections:[]}},promptSHA:"",promptSize:0,dispatchReady:true,accepted:{operation_id:"parent",job_id:"job",draft_id:"d",intent:"draft" as const,job_url:"/api/v1/jobs/job"},resume:{key:"resume",decision:"retry" as const,generation:3,etag:'"original"',runtimeGeneration:"old",dispatchReady:true}};
 try{
  const client=new HttpHaeryongClient();await assert.rejects(recoverGenerationResume(client,record,"new",()=>{},()=>{}),/세대/);
  found=true;assert.equal(await recoverGenerationResume(client,record,"new",()=>{},()=>{}),true);
  assert.deepEqual(seen,[["/api/v1/operations/resume","GET"],["/api/v1/operations/resume","GET"]]);
 }finally{restore();}
});

test("T5QCITHTTP01 citation source uses only its exact answer/index with integrity metadata and no mutation",async()=>{
 const calls:string[]=[];const hash="a".repeat(64);const restore=stubFetch(async(input,init)=>{calls.push(String(input));assert.equal(init?.method,undefined);return new Response("exact captured B",{headers:{"ETag":`"sha256-${hash}"`,"Content-Length":"16","Content-Type":"application/octet-stream"}});});
 try{const client=new HttpHaeryongClient();const stream=await client.answerCitationStream("answer/+",2);assert.deepEqual(calls,["/api/v1/answers/answer%2F%2B/citations/2/source"]);assert.equal(stream.bytes_sha256,hash);assert.equal(stream.byte_size,"16");stream.cancel();assert.throws(()=>client.answerCitationStream("answer",-1));}finally{restore();}
});

test("Task04 HTTP passive() attaches Haeryong-Passive-Read: 1 on reads and refuses writes", async () => {
  const seen: Array<{ path: string; method: string; passive: string | null }> = [];
  const restore = stubFetch(async (input, init) => {
    const headers = new Headers(init?.headers);
    seen.push({
      path: String(input),
      method: init?.method ?? "GET",
      passive: headers.get("Haeryong-Passive-Read"),
    });
    return new Response(
      JSON.stringify({
        data: {
          version_id: "v1",
          exists: false,
          value: { state: "unreviewed", reviewed_at: null },
          revision: null,
          etag: null,
          page_position_url: null,
        },
      }),
      { status: 200 },
    );
  });
  try {
    const client = new HttpHaeryongClient();
    client.setCredential("tok");
    const passive = client.passive();
    await passive.reviewGet("v1");
    await client.reviewGet("v1");
    assert.equal(seen[0]!.passive, "1");
    assert.equal(seen[1]!.passive, null);
    await assert.rejects(
      passive.reviewPut("v1", { action: "start", state: "reviewing" }, { create: true }, "11111111-1111-4111-8111-111111111111"),
      (err: unknown) => err instanceof ApiError && err.status === 400 && err.code === "INVALID_REQUEST",
    );
  } finally {
    restore();
  }
});

test("Task04 HTTP subscribeEvents tracks Last-Event-ID only after frame/resync completion", async () => {
  const seenLastIds: Array<string | null> = [];
  const streams: ReadableStreamDefaultController<Uint8Array>[] = [];
  const restore = stubFetch(async (_input, init) => {
    seenLastIds.push(new Headers(init?.headers).get("Last-Event-ID"));
    return new Response(
      new ReadableStream({
        start(c) {
          streams.push(c);
        },
      }),
    );
  });
  try {
    const client = new HttpHaeryongClient();
    const sub1 = client.subscribeEvents();
    let resyncCount = 0;
    sub1.onResync?.(async () => {
      resyncCount++;
      if (resyncCount === 2) throw new Error("resync failed");
    });
    await new Promise((r) => setTimeout(r, 0));
    assert.equal(seenLastIds[0], null, "no committed cursor uses actual server bootstrap");
    streams[0]!.enqueue(
      new TextEncoder().encode(
        'id: ep1:10\nevent: resync.required\ndata: {"reason":"bootstrap","stream_epoch":"ep1","head":"10","resume_cursor":"ep1:10"}\n\n',
      ),
    );
    await new Promise((r) => setTimeout(r, 10));
    // Now trigger a failing resync at ep1:20; cursor must remain ep1:10:
    streams[0]!.enqueue(
      new TextEncoder().encode(
        'id: ep1:20\nevent: resync.required\ndata: {"reason":"gap","stream_epoch":"ep1","head":"20","resume_cursor":"ep1:20"}\n\n',
      ),
    );
    await sub1.closed;
    const sub2 = client.subscribeEvents();
    await new Promise((r) => setTimeout(r, 0));
    assert.equal(seenLastIds[1], "ep1:10");
    sub2.close();
  } finally {
    restore();
  }
});

test("Task04 G3+G6 invalidateProviderReadinessOnResync retires readiness without probing and subscribeEvents aborts/cancels stream before closed resolves", async () => {
  const { invalidateProviderReadinessOnResync } = await import("./provider-status.ts");
  const current: Parameters<typeof invalidateProviderReadinessOnResync>[0] = {
    verified: true,
    busy: false,
    status: {
      provider_id: "codex",
      state: "ready",
      authentication_mode: "chatgpt",
      ordinary_usage_allowed: true,
      read_only: true,
      model: "gpt-5",
      effort: "high",
      input_formats: null,
      output_format: "json",
      native_input_telemetry: false,
      lookup_supported: false,
      cancellation_supported: true,
      models: [],
      catalog_proves_access: false,
    },
    error: null,
    sawDegraded: false,
  };
  const invalidated = invalidateProviderReadinessOnResync(current);
  assert.equal(invalidateProviderReadinessOnResync(current, "bootstrap"), null, "healthy initial entry keeps its own probe");
  assert.equal(invalidateProviderReadinessOnResync({ ...current, sawDegraded: true }, "bootstrap")?.staleAfterResync, true);
  assert.equal(invalidateProviderReadinessOnResync(current, "trimmed_cursor")?.staleAfterResync, true);
  assert.deepEqual(invalidated, {
    status: null,
    error: null,
    staleAfterResync: true,
    sawDegraded: true,
  });
  assert.equal(
    invalidateProviderReadinessOnResync({
      verified: true,
      busy: false,
      status: null,
      error: null,
      sawDegraded: true,
    }),
    null,
  );

  // G6: when onResync rejects, subscribeEvents aborts the fetch signal and cancels the reader BEFORE closed resolves:
  let fetchSignal: AbortSignal | null = null;
  let readerCancelled = false;
  let streamCtrl!: ReadableStreamDefaultController<Uint8Array>;
  const restore = stubFetch(async (_input, init) => {
    fetchSignal = init?.signal ?? null;
    return new Response(
      new ReadableStream<Uint8Array>({
        start(c) {
          streamCtrl = c;
        },
        cancel() {
          readerCancelled = true;
        },
      }),
    );
  });
  try {
    const client = new HttpHaeryongClient();
    const sub = client.subscribeEvents();
    sub.onResync?.(async () => {
      throw new Error("mandatory resync failed");
    });
    await new Promise((r) => setTimeout(r, 0));
    streamCtrl.enqueue(
      new TextEncoder().encode(
        'id: ep1:1\nevent: resync.required\ndata: {"reason":"bootstrap","stream_epoch":"ep1","head":"1","resume_cursor":"ep1:1"}\n\n',
      ),
    );
    await sub.closed;
    assert.equal((fetchSignal as AbortSignal | null)?.aborted, true, "fetch AbortSignal must be aborted before closed resolves");
    assert.equal(readerCancelled, true, "ReadableStream reader must be cancelled before closed resolves");
  } finally {
    restore();
  }
});

test("Task08-01 scanStart and jobFindingsList round-trip closed DTOs, operation reconciliation, 503 FEATURE_NOT_READY and passive header", async () => {
  const calls: Array<{ path: string; method: string; key: string | null; passive: string | null; body: unknown }> = [];
  const accepted = {
    operation_id: "11111111-1111-4111-8111-111111111111",
    job_id: "22222222-2222-4222-8222-222222222222",
    coalesced: false,
    job: { job_id: "22222222-2222-4222-8222-222222222222", kind: "scan", state: "queued" },
  };
  const finding = {
    finding_id: "33333333-3333-4333-8333-333333333333",
    job_id: accepted.job_id,
    kind: "backlink_index_repair",
    origin: "deterministic",
    project_id: "44444444-4444-4444-8444-444444444444",
    document_id: "55555555-5555-4555-8555-555555555555",
    version_id: "66666666-6666-4666-8666-666666666666",
    page_path: null,
    region_id: null,
    target_set_id: null,
    evidence: [{ kind: "index_drift" }],
    evidence_fingerprint: "a".repeat(64),
    rationale: "Reference index diverged from authored declarations.",
    uncertainty: "low",
    captured_config_revision: 1,
    permissions_revision: 1,
    proposal: { action: "reindex_references" },
    status: "proposed",
    approval_required: true,
    applied_ref: null,
    decided_by: null,
    decided_at: null,
    revision: 1,
    etag: '"finding-33333333-3333-4333-8333-333333333333-r1"',
    created_at: "2026-11-08T00:00:00Z",
    updated_at: "2026-11-08T00:00:00Z",
  };
  const restore = stubFetch(async (input, init) => {
    const path = String(input);
    const method = init?.method ?? "GET";
    const headers = new Headers(init?.headers);
    const body = init?.body ? JSON.parse(String(init.body)) : null;
    calls.push({ path, method, key: headers.get("Idempotency-Key"), passive: headers.get("Haeryong-Passive-Read"), body });
    if (method === "POST" && path === "/api/v1/scans") {
      if ((body as Record<string, unknown>)?.finding_id || (body as Record<string, unknown>)?.expected_finding_revision) {
        return new Response(
          JSON.stringify({ error: { code: "FEATURE_NOT_READY", message: "not ready", retryable: false, details: { reason: "finding_reassessment_not_ready" } } }),
          { status: 503 },
        );
      }
      return new Response(JSON.stringify({ data: accepted }), { status: 202 });
    }
    if (method === "GET" && path === `/api/v1/operations/${accepted.operation_id}`) {
      return new Response(
        JSON.stringify({ data: { operation_id: accepted.operation_id, state: "completed", original_status: 202, result_metadata: accepted } }),
        { status: 200 },
      );
    }
    if (method === "GET" && path.startsWith(`/api/v1/jobs/${accepted.job_id}/findings`)) {
      return new Response(JSON.stringify({ data: { items: [finding], next_cursor: null } }), { status: 200 });
    }
    return new Response(JSON.stringify({ data: null }), { status: 404 });
  });
  try {
    const client = new HttpHaeryongClient();
    client.setCredential("tok");
    const res = await client.scanStart({ project_id: "44444444-4444-4444-8444-444444444444", depth: "change_focused" }, accepted.operation_id);
    assert.deepEqual(res, accepted);
    assert.equal(calls[0]!.path, "/api/v1/scans");
    assert.equal(calls[0]!.key, accepted.operation_id);
    assert.equal(calls[1]!.path, `/api/v1/operations/${accepted.operation_id}`);

    await assert.rejects(
      client.scanStart({ finding_id: finding.finding_id, expected_finding_revision: 1 }, "77777777-7777-4777-8777-777777777777"),
      (err: unknown) => err instanceof ApiError && err.status === 503 && err.code === "FEATURE_NOT_READY" && err.details?.reason === "finding_reassessment_not_ready",
    );

    const page = await client.jobFindingsList(accepted.job_id, { kind: "backlink_index_repair", status: "proposed", limit: 10 });
    assert.equal(page.items.length, 1);
    assert.equal(page.items[0]!.finding_id, finding.finding_id);
    assert.equal(calls[3]!.path, `/api/v1/jobs/${accepted.job_id}/findings?kind=backlink_index_repair&status=proposed&limit=10`);
    assert.equal(calls[3]!.passive, null);

    await client.passive().jobFindingsList!(accepted.job_id, { cursor: "c1" });
    assert.equal(calls[4]!.path, `/api/v1/jobs/${accepted.job_id}/findings?cursor=c1`);
    assert.equal(calls[4]!.passive, "1");
  } finally {
    restore();
  }
});
