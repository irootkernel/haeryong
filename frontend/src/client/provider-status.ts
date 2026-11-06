import type { ProviderStatus, ProviderState } from "./types.ts";
const states = new Set<ProviderState>(["ready", "auth_required", "quota_unavailable", "quota_exhausted", "unsupported_version", "source_unavailable", "paused", "provider_error"]);
export function providerStatus(value: ProviderStatus): ProviderStatus {
  if (!value || !states.has(value.state) || (value.ordinary_usage_allowed !== null && typeof value.ordinary_usage_allowed !== "boolean")) {
    throw new Error("지원되는 제공자 상태 정보를 확인할 수 없습니다");
  }
  return value;
}

export interface ProviderReadinessInvalidation {
  status: ProviderStatus | null;
  error: string | null;
  staleAfterResync: boolean;
  sawDegraded: boolean;
}

/**
 * Passive reconnect/resync never dispatches GET /api/v1/providers (which spawns
 * a host Codex probe); instead it retires any previously displayed readiness
 * snapshot into an explicit manual-refresh-required state.
 */
export function invalidateProviderReadinessOnResync(current: {
  verified: boolean;
  busy: boolean;
  status: ProviderStatus | null;
  error: string | null;
  sawDegraded: boolean;
}, reason?: string): ProviderReadinessInvalidation | null {
  if (!current.verified || current.busy) return null;
  // The first healthy entry owns its explicit status read. Bootstrap only
  // establishes that initial stream; an actual degraded reconnect still
  // invalidates readiness even if its replacement stream bootstraps.
  if (reason === "bootstrap" && !current.sawDegraded) return null;
  if (current.status === null && current.error === null && current.sawDegraded) return null;
  return {
    status: null,
    error: null,
    staleAfterResync: true,
    sawDegraded: true,
  };
}
