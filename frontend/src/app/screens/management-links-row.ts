import { createElement as h } from "react";
import type { ManagementAnchor, ManagementEffect } from "../../client/types.ts";
import { topicEvidenceBasisLabel } from "./management-presentation.ts";

type Props = { effect: ManagementEffect; onOpen(anchor: ManagementAnchor): void; actions?: import("react").ReactNode };

export function ManagementLinkRow({ effect: m, onOpen, actions }: Props) {
  // Return before reading target, actor, rationale or serializing details.
  // Unavailable has only the declared eight safe fields; unknown wire values
  // cannot fall through to full payload rendering either.
  if (m.availability !== "available") {
    return h("li", null, h("strong", null, "AI 추가"), " · ", m.decision, " · 대상을 사용할 수 없습니다.");
  }
  if (m.kind === "topic_assignment") {
    return h("li", null,
      h("strong", null, "AI 추가"), " · ", m.topic, " · ", m.decision, " · 승인 주체 ", m.decision_actor,
      h("p", null, m.rationale, " · ", m.uncertainty),
      h("p", null, "근거 ", topicEvidenceBasisLabel(m.evidence_basis)),
      h("details", null, h("summary", null, "토픽 근거·정확한 범위"),
        h("pre", { className: "management-detail" }, JSON.stringify(m, null, 2))), actions);
  }
  const target = m.target;
  return h("li", { "data-overlay-id": m.overlay_id },
    h("strong", null, "AI 추가"), " · ", m.relation_type, " · ", m.decision, " · 승인 주체 ", m.decision_actor,
    h("p", null, m.rationale), h("p", null, "근거 ", m.evidence_basis, " · 한계 ", m.uncertainty),
    target && h("button", { onClick: () => onOpen(target) }, "정확한 대상 열기 · ", target.version_id, " · ", target.page_path ?? "버전 전체", target.region_id ? " · " + target.region_id : ""),
    h("details", null, h("summary", null, "근거·리비전"),
      h("pre", { className: "management-detail" }, JSON.stringify(m, null, 2))), actions);
}
