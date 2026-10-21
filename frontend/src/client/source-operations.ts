// Source/configuration writes share the durable operation receipt protocol.
// Reconciliation always uses the caller's original key and never sends a POST.
import type { DecisionResult, OperationView } from "./types.ts";
import { ApiError, NetworkError, StaleResponseError, UncertainResponseError } from "./types.ts";

export class PendingSourceWriteError extends Error {
  readonly operationId: string;
  constructor(operationId: string, cause?: unknown) {
    super(`결과 확인 필요 · 작업 ${operationId}`);
    this.name = "PendingSourceWriteError";
    this.operationId = operationId;
    this.cause = cause;
  }
}

export async function reconcileSourceWrite<T>(operationId: string, operation: (id: string) => Promise<OperationView>): Promise<T> {
  for (;;) {
    let receipt: OperationView;
    try { receipt = await operation(operationId); }
    catch (cause) {
      if (cause instanceof StaleResponseError) throw cause;
      throw new PendingSourceWriteError(operationId, cause);
    }
    if (receipt.operation_id !== operationId) throw new PendingSourceWriteError(operationId);
    if (receipt.state === "completed" && (receipt.original_status === 200 || receipt.original_status === 201)) {
      return receipt.result_metadata as T;
    }
    if (receipt.state === "failed") {
      const body = receipt.result_metadata as { error?: { code?: string; retryable?: boolean; details?: Record<string, unknown> } } | null;
      throw new ApiError(receipt.original_status ?? 503, body?.error?.code ?? "SOURCE_OPERATION_FAILED", body?.error?.retryable ?? false, 0, body?.error?.details ?? {});
    }
    if (receipt.state !== "accepted" && receipt.state !== "pending") throw new PendingSourceWriteError(operationId);
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
}

export async function sourceWrite<T>(operationId: string, dispatch: () => Promise<DecisionResult>, operation: (id: string) => Promise<OperationView>): Promise<T> {
  try {
    const result = await dispatch();
    if (result.kind === "committed") return result.body as T;
  } catch (cause) {
    if (!(cause instanceof UncertainResponseError || cause instanceof NetworkError || (cause instanceof ApiError && cause.code === "OPERATION_RECONCILIATION_REQUIRED"))) throw cause;
  }
  return reconcileSourceWrite<T>(operationId, operation);
}
