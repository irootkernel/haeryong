import type {ConfigurationView} from "../../client/types.ts";

export const ACTION_CATEGORIES = ["backlink_index_repair", "related_explanation", "topic_assignment", "freshness_assessment"] as const;
export type ActionMode = "disabled" | "propose" | "auto";
export interface ActionPolicyDraft {
  auto_apply: Record<typeof ACTION_CATEGORIES[number], ActionMode>;
  content_generation: "disabled" | "propose" | "draft";
}

function record(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

export function actionPolicyDraft(view: ConfigurationView, projectId?: string): ActionPolicyDraft {
  const base = record(view.configuration.maintenance);
  const project = record(record(view.configuration.projects[projectId ?? ""]).maintenance);
  const defaults = record(base.auto_apply), overrides = record(project.auto_apply);
  const auto_apply = Object.fromEntries(ACTION_CATEGORIES.map(category => [category,
    overrides[category] ?? defaults[category] ?? (category === "backlink_index_repair" ? "auto" : "propose"),
  ])) as ActionPolicyDraft["auto_apply"];
  return {auto_apply, content_generation: (project.content_generation ?? base.content_generation ?? "propose") as ActionPolicyDraft["content_generation"]};
}

export function actionPolicyChanges(draft: ActionPolicyDraft): {maintenance: ActionPolicyDraft} {
  for (const category of ACTION_CATEGORIES) {
    if (!["disabled", "propose", "auto"].includes(draft.auto_apply[category])) throw Error("알 수 없는 자동 적용 정책입니다.");
  }
  if (!["disabled", "propose", "draft"].includes(draft.content_generation)) throw Error("콘텐츠 생성에는 자동 게시 모드가 없습니다.");
  return {maintenance: {
    auto_apply: Object.fromEntries(ACTION_CATEGORIES.map(category => [category, draft.auto_apply[category]])) as ActionPolicyDraft["auto_apply"],
    content_generation: draft.content_generation,
  }};
}

/** Show the existing service's stricter library/project policy separately from saved overrides. */
export function effectiveActionPolicy(view: ConfigurationView, draft: ActionPolicyDraft): ActionPolicyDraft {
  const base = actionPolicyDraft(view);
  const autoModes: ActionMode[] = ["disabled", "propose", "auto"];
  const contentModes: ActionPolicyDraft["content_generation"][] = ["disabled", "propose", "draft"];
  return {
    auto_apply: Object.fromEntries(ACTION_CATEGORIES.map(category => [category,
      autoModes[Math.min(autoModes.indexOf(base.auto_apply[category]), autoModes.indexOf(draft.auto_apply[category]))],
    ])) as ActionPolicyDraft["auto_apply"],
    content_generation: contentModes[Math.min(contentModes.indexOf(base.content_generation), contentModes.indexOf(draft.content_generation))]!,
  };
}
