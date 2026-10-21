import type { EvidenceItemView } from "../../client/types.ts";

export function evidenceAvailability(view: Pick<EvidenceItemView, "availability" | "reason">): string {
  if (view.availability === "denied") return "거부됨 — 이 보기에는 출처 읽기 권한이 없습니다";
  switch (view.reason) {
    case "not_read_by_view": return "확인 전 — 이 화면은 새 소스 읽기를 시작하지 않습니다";
    case "missing_source": return "마지막 확인 시점에 원본 없음(확인 시각은 별도 기록)";
    case "source_not_registered": return "등록되지 않은 출처";
    case "missing_retained_bytes": return "보관된 스냅샷 없음(확인 불가)";
    case "retained_bytes_corrupt": return "보관된 스냅샷 무결성 오류(내용 없음)";
    default: return view.availability === "available" ? "사용 가능" : "사용 불가";
  }
}

export function coverageReason(reason: string): string {
  const labels: Record<string, string> = {
    unexamined_dependency_scope: "의존 범위 미검사 — 인용 바이트 외의 변경이며 재평가 판정이 아닙니다",
    direct_source_change: "직접 인용한 소스 변경 — 이 대상/범위의 재평가 필요",
    no_cited_evidence: "인용된 근거 없음",
    source_denied: "출처 읽기 권한 없음",
    source_not_registered: "등록되지 않은 출처",
    missing_source: "마지막 확인 시점에 원본 없음",
    missing_retained_bytes: "보관된 스냅샷 없음",
    retained_bytes_corrupt: "보관된 스냅샷 무결성 오류",
    not_observed: "관측 기록 없음",
  };
  return labels[reason] ? `${reason} · ${labels[reason]}` : reason;
}

/** Present retained, service-verified bytes as text when UTF-8, otherwise Base64. */
export function evidenceExcerpt(excerpt: EvidenceItemView["excerpt"]): string | null {
  if (!excerpt?.data_base64) return null;
  try {
    const bytes = Uint8Array.from(atob(excerpt.data_base64), (character) => character.charCodeAt(0));
    try { return new TextDecoder("utf-8", { fatal: true }).decode(bytes); }
    catch { return `Base64: ${excerpt.data_base64}`; }
  } catch { return "보관된 내용을 표시할 수 없습니다."; }
}
