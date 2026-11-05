// Controller-owned, in-memory write identities. Screen/runtime remounts do
// retain write identities and outcomes until the active consumer consumes them.
import { PendingSourceWriteError } from "./source-operations.ts";
import { StaleResponseError } from "./types.ts";
import type { ConfigurationPlanView, ConfigurationCommitView, EvidenceVerificationRequest, EvidenceVerificationResult } from "./types.ts";

export type WriteOutcome<T> =
  | { state: "pending" }
  | { state: "completed"; result: T }
  | { state: "failed"; error: unknown };

interface WriteIntent<T> {
  operationId: string;
  outcome: WriteOutcome<T>;
  inFlight?: Promise<T>;
}
export interface VerificationIntent extends WriteIntent<EvidenceVerificationResult> {
  request: EvidenceVerificationRequest;
}
export interface RegistrationIntent extends WriteIntent<unknown> {
  name: string;
}
export interface ConfigurationIntent extends WriteIntent<ConfigurationPlanView | ConfigurationCommitView> {
  kind: "preview" | "commit";
  planId?: string;
  baseRevision?: number;
}

/** Record the producer's outcome even if its original screen has gone away.
 * Consumers remove the entry only after applying/showing a definitive outcome. */
export async function resolveWriteIntent<T>(
  intent: WriteIntent<T>,
  dispatch: () => Promise<T>,
  isUncertainError?: (error: unknown) => boolean,
): Promise<T> {
  if (intent.outcome.state === "completed") return intent.outcome.result;
  if (intent.outcome.state === "failed") throw intent.outcome.error;
  intent.inFlight ??= (async () => {
    try {
      const result = await dispatch();
      intent.outcome = { state: "completed", result };
      return result;
    } catch (error) {
      const keepPending =
        error instanceof PendingSourceWriteError ||
        error instanceof StaleResponseError ||
        (isUncertainError ? isUncertainError(error) : false);
      intent.outcome = keepPending
        ? { state: "pending" } : { state: "failed", error };
      throw error;
    } finally { intent.inFlight = undefined; }
  })();
  return intent.inFlight;
}

/** One confirmed library/principal owns these operation-specific slots. */
export class SourceWriteIntents {
  verifications = new Map<string, VerificationIntent>();
  registration: RegistrationIntent | null = null;
  configurations = new Map<string, ConfigurationIntent>();
  securityPolicy: ConfigurationIntent | null = null;
}
