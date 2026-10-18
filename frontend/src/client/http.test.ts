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
