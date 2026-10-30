import type { ProviderStatus, ProviderState } from "./types.ts";
const states = new Set<ProviderState>(["ready", "auth_required", "quota_unavailable", "quota_exhausted", "unsupported_version", "source_unavailable", "paused", "provider_error"]);
export function providerStatus(value: ProviderStatus): ProviderStatus {
  if (!value || !states.has(value.state) || (value.ordinary_usage_allowed !== null && typeof value.ordinary_usage_allowed !== "boolean")) {
    throw new Error("지원되는 제공자 상태 정보를 확인할 수 없습니다");
  }
  return value;
}
