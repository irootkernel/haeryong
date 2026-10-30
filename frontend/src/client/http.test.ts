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
