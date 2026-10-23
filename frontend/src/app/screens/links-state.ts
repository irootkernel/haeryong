import type { ReferenceView } from "../../client/types.ts";
export function backlinkScope(scope: "version" | "page" | "region", page: string | null, region: string | null): { pagePath?: string; regionId?: string } {
  if (scope === "version" || !page) return {};
  return scope === "region" && region ? { pagePath: page, regionId: region } : { pagePath: page };
}
export function referenceStateLabel(row: Pick<ReferenceView, "resolution" | "reason">): string {
  switch (row.resolution) {
    case "tombstoned": return "삭제된 대상";
    case "unavailable": return "대상을 사용할 수 없습니다.";
    case "unresolved": return row.reason === "resolution_pending" ? "연결 확인 중" : "미해결 참조";
    case "resolved": return "검증된 작성자 참조";
  }
}
