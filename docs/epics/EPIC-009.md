---
id: "EPIC-009"
type: "epic"
title: "Periodic operation and configurable retention"
status: "planned"
depends_on: ["EPIC-008"]
completion_requires: ["TASK-009-01","TASK-009-02","TASK-009-03","TASK-009-04","TASK-009-05","TASK-009-06"]
---

# EPIC-009: Periodic operation and configurable retention

## Goal

Automate the already validated manual work path with persistent schedules, bounded attempts, current-policy enforcement and owner-confirmed retention.

## Entry gate

Complete and review EPIC-008, including its passing exit gate, before starting this Epic. Apply the [execution protocol](../execution-protocol.md) and current [personal-tool amendment](../changes/2026-10-10-personal-tool-revision.md) and [directory-results amendment](../changes/2026-10-10-directory-results-revision.md). No task depends on an unimplemented future Epic to meet its stated exit.

## Ordered tasks

| Order | Task | Outcome area | Dependency |
| --- | --- | --- | --- |
| 1 | [TASK-009-01](../tasks/EPIC-009/TASK-009-01.md) | Complete validated scheduling and policy controls | EPIC-008 |
| 2 | [TASK-009-02](../tasks/EPIC-009/TASK-009-02.md) | Implement durable schedules and missed-run coalescing | TASK-009-01 |
| 3 | [TASK-009-03](../tasks/EPIC-009/TASK-009-03.md) | Enforce per-run budgets and uncertain-outcome accounting | TASK-009-02 |
| 4 | [TASK-009-04](../tasks/EPIC-009/TASK-009-04.md) | Enforce current restrictions during running jobs | TASK-009-03 |
| 5 | [TASK-009-05](../tasks/EPIC-009/TASK-009-05.md) | Implement explicitly enabled retention execution | TASK-009-04 |
| 6 | [TASK-009-06](../tasks/EPIC-009/TASK-009-06.md) | Verify unattended operation and close Stage C gate | TASK-009-05 |

Task IDs remain stable; use the explicit order and dependencies. Task count follows coherent outcomes, not a fixed per-Epic quota.

## Exit gate

**Configured schedules, coalescing, budgets and retention run without desktop presence. Attempts remain accounted under cancellation/orphan uncertainty; current restrictions, source/read/backup pins and restore safety stay enforced.**

Every task listed in completion_requires must be done and reviewed under the shared definition of done. Record actual evidence with the canonical pass/fail/blocked/not_run status. Required failures or not-run checks prevent completion; a future Epic is not evidence for this exit.

## Acceptance contribution

C09, M04, M05, M06, M07, M09, M11, M12, M13, M20. See [traceability](../testing/traceability.md) and the task-specific supplemental cases. Contribution does not certify an unexercised full scenario. Knowledge-related gates use [knowledge quality](../testing/knowledge-quality.md).

## Handoff

Stage C operation/recovery evidence and complete effective policy/defaults ready for final distribution regression. Use the [Epic gate template](../templates/epic-gate.md) as an index to actual artifacts and identify the exact next task.
