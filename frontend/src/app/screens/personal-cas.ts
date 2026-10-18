// Pure three-way reconciliation for a CONFIRMED 412 on one personal reader
// resource family (approved M2 A2). No retries, storage, UI, counters or
// backoff: unknown transport outcomes never reach this helper — the caller
// retains the original operation key, target and preconditions.
export type PersonalFamily = "reader-preference" | "reading-position" | "page-position";

export interface PersonalSnapshot {
  readonly exists: boolean;
  readonly value: unknown;
  readonly etag: string | null;
}

export interface ReconcileInput {
  readonly family: PersonalFamily;
  readonly base: PersonalSnapshot;
  readonly intent: unknown;
  readonly fresh: PersonalSnapshot;
}

export type ReconcileDecision = "converged" | "retry" | "conflict";

/** Structural JSON equality: object key order-insensitive, array order-sensitive, arbitrary nesting. */
export function structuralEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((item, index) => structuralEqual(item, b[index]));
  }
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false;
  const keysA = Object.keys(a), keysB = Object.keys(b);
  if (keysA.length !== keysB.length) return false;
  return keysA.every((key) => Object.hasOwn(b, key) && structuralEqual((a as Record<string, unknown>)[key], (b as Record<string, unknown>)[key]));
}

/** Client-intent projection strips ONLY the reading pointer's server-assigned timestamps. */
export function clientIntent(family: PersonalFamily, value: unknown): unknown {
  if (family !== "reading-position" || typeof value !== "object" || value === null || Array.isArray(value)) return value;
  const projected: Record<string, unknown> = {};
  for (const [key, entry] of Object.entries(value as Record<string, unknown>)) {
    if (key !== "first_opened_at" && key !== "last_opened_at") projected[key] = entry;
  }
  return projected;
}

export function reconcilePersonal412(input: ReconcileInput): ReconcileDecision {
  const { family, base, intent, fresh } = input;
  // S = P: convergence compares the canonical client-intent projection only.
  if (structuralEqual(clientIntent(family, fresh.value), clientIntent(family, intent))) return "converged";
  // S = B0: existence + FULL stored value (pointer timestamps included) + the
  // ORIGINAL opaque ETag compared as an exact string — never parsed, never
  // ordered by time. Only this permits the automatic new-K resubmission.
  const unchangedBase = fresh.exists === base.exists
    && structuralEqual(fresh.value, base.value)
    && fresh.etag === base.etag;
  return unchangedBase ? "retry" : "conflict";
}
