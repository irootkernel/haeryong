---
id: "EPIC-005"
type: "epic"
title: "Personal review, annotations and lifecycle"
status: "planned"
depends_on: ["EPIC-004"]
completion_requires: ["TASK-005-01","TASK-005-02","TASK-005-03","TASK-005-04","TASK-005-05","TASK-005-06"]
---

# EPIC-005: Personal review, annotations and lifecycle

## Goal

Persist whole-version Review and exact page/region notes, manage archive/trash/purge and deliver coherent backup, complete directory export and protected restore using the existing local job foundation.

## Entry gate

Complete and review EPIC-004, including its passing exit gate, before starting this Epic. Apply the [execution protocol](../execution-protocol.md) and current [personal-tool amendment](../changes/2026-10-10-personal-tool-revision.md) and [directory-results amendment](../changes/2026-10-10-directory-results-revision.md). No task depends on an unimplemented future Epic to meet its stated exit.

## Ordered tasks

| Order | Task | Outcome area | Dependency |
| --- | --- | --- | --- |
| 1 | [TASK-005-01](../tasks/EPIC-005/TASK-005-01.md) | Implement explicit version-specific review | EPIC-004 |
| 2 | [TASK-005-02](../tasks/EPIC-005/TASK-005-02.md) | Implement anchored notes and conflict handling | TASK-005-01 |
| 3 | [TASK-005-03](../tasks/EPIC-005/TASK-005-03.md) | Implement archive, trash and restore | TASK-005-02 |
| 4 | [TASK-005-04](../tasks/EPIC-005/TASK-005-04.md) | Implement guarded manual purge and content GC | TASK-005-03 |
| 5 | [TASK-005-05](../tasks/EPIC-005/TASK-005-05.md) | Implement coherent backup, portability export and protected restore | TASK-005-04 |
| 6 | [TASK-005-06](../tasks/EPIC-005/TASK-005-06.md) | Verify personal/lifecycle workflows and recovery gate | TASK-005-05 |

Task IDs remain stable; use the explicit order and dependencies. Task count follows coherent outcomes, not a fixed per-Epic quota.

## Exit gate

**Original page-exact notes, whole-version Review and pinned links survive new versions, restart and protected recovery. Purge preserves active/shared/source/read-pinned objects; complete directory export preserves every selected path/file byte and exact anchors. Restored device tokens remain invalid and restored exposure, source/provider and automation preferences receive normal owner review before affected work resumes.**

Every task listed in completion_requires must be done and reviewed under the shared definition of done. Record actual evidence with the canonical pass/fail/blocked/not_run status. Required failures or not-run checks prevent completion; a future Epic is not evidence for this exit.

## Acceptance contribution

C07, C08, C09, M04, M06, M08, M09, M13, M18, M20. See [traceability](../testing/traceability.md) and the task-specific supplemental cases. Contribution does not certify an unexercised full scenario. Knowledge-related gates use [knowledge quality](../testing/knowledge-quality.md).

## Handoff

Production lifecycle/review evidence, local backup/export commands, pin/accounting fixtures and restored receipt/epoch ambiguity cases ready for provider-specific extension. Use the [Epic gate template](../templates/epic-gate.md) as an index to actual artifacts and identify the exact next task.
