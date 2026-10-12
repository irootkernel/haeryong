---
id: "EPIC-006"
type: "epic"
title: "Haeryong skill, durable AI work and Q&A"
status: "planned"
depends_on: ["EPIC-005"]
completion_requires: ["TASK-006-01","TASK-006-02","TASK-006-03","TASK-006-04","TASK-006-05","TASK-006-06"]
---

# EPIC-006: Haeryong skill, durable AI work and Q&A

## Goal

Promote the tested Haeryong skill and source-grounded questions to production while extending existing local jobs with bounded provider attempts and reconciliation.

## Entry gate

Complete and review EPIC-005, including its passing exit gate, before starting this Epic. Apply the [execution protocol](../execution-protocol.md) and current [personal-tool amendment](../changes/2026-10-10-personal-tool-revision.md) and [directory-results amendment](../changes/2026-10-10-directory-results-revision.md). No task depends on an unimplemented future Epic to meet its stated exit.

## Ordered tasks

| Order | Task | Outcome area | Dependency |
| --- | --- | --- | --- |
| 1 | [TASK-006-01](../tasks/EPIC-006/TASK-006-01.md) | Extend local jobs with provider attempts and reconciliation | EPIC-005 |
| 2 | [TASK-006-02](../tasks/EPIC-006/TASK-006-02.md) | Implement configured source integration and selected snapshots | TASK-006-01 |
| 3 | [TASK-006-03](../tasks/EPIC-006/TASK-006-03.md) | Implement the verified provider adapter | TASK-006-02 |
| 4 | [TASK-006-04](../tasks/EPIC-006/TASK-006-04.md) | Implement Haeryong-owned generation skill | TASK-006-03 |
| 5 | [TASK-006-05](../tasks/EPIC-006/TASK-006-05.md) | Implement region-aware source-grounded questions | TASK-006-04 |
| 6 | [TASK-006-06](../tasks/EPIC-006/TASK-006-06.md) | Verify AI workflow, interruption and allowance boundaries | TASK-006-05 |

Task IDs remain stable; use the explicit order and dependencies. Task count follows coherent outcomes, not a fixed per-Epic quota.

## Exit gate

**One supported real subscription provider generates/imports explanations and answers with exact evidence through the knowledge-quality workflow. Cancellation, same-daemon attempt fencing, orphan/unknown outcomes, remembered source/provider choices and explicit personal-state boundaries pass; external-agent permissions are described honestly.**

Every task listed in completion_requires must be done and reviewed under the shared definition of done. Record actual evidence with the canonical pass/fail/blocked/not_run status. Required failures or not-run checks prevent completion; a future Epic is not evidence for this exit.

## Acceptance contribution

C01, C02, C04, C05, M05, M06, M07, M08, M18, M19, M20. See [traceability](../testing/traceability.md) and the task-specific supplemental cases. Contribution does not certify an unexercised full scenario. Knowledge-related gates use [knowledge quality](../testing/knowledge-quality.md).

## Handoff

Real supported provider compatibility, evaluated explanation/Q&A corpus and complete durable attempt reconciliation on the existing receipt/event/pin foundation. Use the [Epic gate template](../templates/epic-gate.md) as an index to actual artifacts and identify the exact next task.
