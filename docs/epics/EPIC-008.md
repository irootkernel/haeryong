---
id: "EPIC-008"
type: "epic"
title: "Manual AI wiki maintenance"
status: "planned"
depends_on: ["EPIC-007"]
completion_requires: ["TASK-008-01","TASK-008-02","TASK-008-03","TASK-008-04","TASK-008-05","TASK-008-06"]
---

# EPIC-008: Manual AI wiki maintenance

## Goal

Provide full-scope manual structural/semantic maintenance with evaluated useful relations, scoped freshness and configurable reversible overlays/proposals.

## Entry gate

Complete and review EPIC-007, including its passing exit gate, before starting this Epic. Apply the [execution protocol](../execution-protocol.md) and current [personal-tool amendment](../changes/2026-10-10-personal-tool-revision.md) and [directory-results amendment](../changes/2026-10-10-directory-results-revision.md). No task depends on an unimplemented future Epic to meet its stated exit.

## Ordered tasks

| Order | Task | Outcome area | Dependency |
| --- | --- | --- | --- |
| 1 | [TASK-008-01](../tasks/EPIC-008/TASK-008-01.md) | Implement scan inventory and deterministic structure checks | EPIC-007 |
| 2 | [TASK-008-02](../tasks/EPIC-008/TASK-008-02.md) | Implement bounded relationship/topic candidate discovery | TASK-008-01 |
| 3 | [TASK-008-03](../tasks/EPIC-008/TASK-008-03.md) | Implement region-scoped freshness assessment | TASK-008-02 |
| 4 | [TASK-008-04](../tasks/EPIC-008/TASK-008-04.md) | Implement management overlays and proposal presentation | TASK-008-03 |
| 5 | [TASK-008-05](../tasks/EPIC-008/TASK-008-05.md) | Implement conflict-safe approval, rejection and undo | TASK-008-04 |
| 6 | [TASK-008-06](../tasks/EPIC-008/TASK-008-06.md) | Implement content drafts, full report and manual-maintenance gate | TASK-008-05 |

Task IDs remain stable; use the explicit order and dependencies. Task count follows coherent outcomes, not a fixed per-Epic quota.

## Exit gate

**Manual scans report actual captured structural/semantic coverage and pass the labeled knowledge-quality corpus. Only policy-authorized overlays apply; pinned/rejected decisions and old review/provenance remain intact, and content drafts are unpublished until explicitly authorized.**

Every task listed in completion_requires must be done and reviewed under the shared definition of done. Record actual evidence with the canonical pass/fail/blocked/not_run status. Required failures or not-run checks prevent completion; a future Epic is not evidence for this exit.

## Acceptance contribution

C06, M01, M02, M03, M04, M05, M11, M12, M19. See [traceability](../testing/traceability.md) and the task-specific supplemental cases. Contribution does not certify an unexercised full scenario. Knowledge-related gates use [knowledge quality](../testing/knowledge-quality.md).

## Handoff

Validated manual pipeline, held-out relationship/freshness evaluation, current-policy application/undo tests and coverage/usage limits ready for scheduling. Use the [Epic gate template](../templates/epic-gate.md) as an index to actual artifacts and identify the exact next task.
