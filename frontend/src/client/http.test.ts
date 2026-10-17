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
