// Use the current read projection only; do not infer provenance from other fields.
export function topicEvidenceBasisLabel(basis: unknown): string {
  switch (basis) {
    case "explanation_text": return "설명 본문 (explanation_text)";
    case "source_verified": return "검증된 소스 (source_verified)";
    case "legacy_unknown": return "이전 기록의 근거 유형 미상 (legacy_unknown)";
    default: return "근거 유형을 확인할 수 없습니다";
  }
}
