---
id: "EPIC-001"
type: "epic"
title: "Value, contracts and critical-risk validation"
status: "planned"
depends_on: []
completion_requires: ["TASK-001-01","TASK-001-02","TASK-001-03","TASK-001-04","TASK-001-05","TASK-001-07","TASK-001-06"]
---

# EPIC-001: Value, contracts and critical-risk validation

## Goal

Prove the chosen provider, shared stack, remembered private authentication, useful ordinary JavaScript and real two-project understanding workflow before freezing current durable contracts.

## Entry gate

The current execution baseline is accepted or updated through recorded bounded changes. Use isolated profiles; required account/source/device authority must exist before the relevant experiment. Apply the [execution protocol](../execution-protocol.md) and current [personal-tool amendment](../changes/2026-10-10-personal-tool-revision.md) and [directory-results amendment](../changes/2026-10-10-directory-results-revision.md). No task depends on an unimplemented future Epic to meet its stated exit.

## Ordered tasks

| Order | Task | Outcome area | Dependency |
| --- | --- | --- | --- |
| 1 | [TASK-001-01](../tasks/EPIC-001/TASK-001-01.md) | Ratify baseline, UI brief and execution conventions | Baseline review |
| 2 | [TASK-001-02](../tasks/EPIC-001/TASK-001-02.md) | Prove headless stack and native companion | TASK-001-01 |
| 3 | [TASK-001-03](../tasks/EPIC-001/TASK-001-03.md) | Validate pairing and private ingress design | TASK-001-02 |
| 4 | [TASK-001-04](../tasks/EPIC-001/TASK-001-04.md) | Prove useful JavaScript and host separation | TASK-001-03 |
| 5 | [TASK-001-05](../tasks/EPIC-001/TASK-001-05.md) | Validate subscription adapter and exact source reads | TASK-001-04 |
| 6 | [TASK-001-07](../tasks/EPIC-001/TASK-001-07.md) | Validate the two-project understanding workflow | TASK-001-05 |
| 7 | [TASK-001-06](../tasks/EPIC-001/TASK-001-06.md) | Freeze contracts and close readiness gate | TASK-001-07 |

Task IDs remain stable; use the explicit order and dependencies. Task count follows coherent outcomes, not a fixed per-Epic quota.

## Exit gate

**G-STACK, G-AUTH, G-VIEW, G-AI, G-VALUE and G-CONTRACT all pass for the selected personal-tool implementation and recorded available environment, using the current owner-amended applicability. Evidence collection alone, a recorded failure, a blocked test or a fixture substituted for required real proof cannot close this Epic.**

Every task listed in completion_requires must be done and reviewed under the shared definition of done. Record actual evidence with the canonical pass/fail/blocked/not_run status. Required failures or not-run checks prevent completion; a future Epic is not evidence for this exit.

## Acceptance contribution

C01, C02, C03, C04, C05, C06, C07, C08, M01, M07, M08, M09, M10, M11, M14, M15, M16, M17, M19, M20. See [traceability](../testing/traceability.md) and the task-specific supplemental cases. Contribution does not certify an unexercised full scenario. Knowledge-related gates use [knowledge quality](../testing/knowledge-quality.md).

## Handoff

Passing spike evidence, the real-source quality corpus and draft Haeryong skill, current frozen/provisional contract boundaries and independently repeatable fixtures. Full production Stage A remains later work. Use the [Epic gate template](../templates/epic-gate.md) as an index to actual artifacts and identify the exact next task.
