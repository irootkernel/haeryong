---
id: "EPIC-010"
type: "epic"
title: "Integrated verification and distribution"
status: "planned"
depends_on: ["EPIC-009"]
completion_requires: ["TASK-010-01","TASK-010-02","TASK-010-03","TASK-010-04","TASK-010-05","TASK-010-06"]
---

# EPIC-010: Integrated verification and distribution

## Goal

Qualify the implemented personal-tool scope on the selected release setup and deliver installation/local-recovery evidence with honest limits.

## Entry gate

Complete and review EPIC-009, including its passing exit gate, before starting this Epic. Apply the [execution protocol](../execution-protocol.md) and current [personal-tool amendment](../changes/2026-10-10-personal-tool-revision.md) and [directory-results amendment](../changes/2026-10-10-directory-results-revision.md). No task depends on an unimplemented future Epic to meet its stated exit.

## Ordered tasks

| Order | Task | Outcome area | Dependency |
| --- | --- | --- | --- |
| 1 | [TASK-010-01](../tasks/EPIC-010/TASK-010-01.md) | Package reproducible service and desktop distributions | EPIC-009 |
| 2 | [TASK-010-02](../tasks/EPIC-010/TASK-010-02.md) | Run migration, backup and destructive recovery qualification | TASK-010-01 |
| 3 | [TASK-010-03](../tasks/EPIC-010/TASK-010-03.md) | Verify host boundaries and selected-device recovery | TASK-010-02 |
| 4 | [TASK-010-04](../tasks/EPIC-010/TASK-010-04.md) | Qualify performance, Korean retrieval and resource behavior | TASK-010-03 |
| 5 | [TASK-010-05](../tasks/EPIC-010/TASK-010-05.md) | Complete operator documentation and owner acceptance walkthrough | TASK-010-04 |
| 6 | [TASK-010-06](../tasks/EPIC-010/TASK-010-06.md) | Close integrated release gate and publish handoff evidence | TASK-010-05 |

Task IDs remain stable; use the explicit order and dependencies. Task count follows coherent outcomes, not a fixed per-Epic quota.

## Exit gate

**Inherited scenarios under current owner-amended applicability, supplemental cases and knowledge-quality gates pass for the selected release environment. Relevant earlier checks repeat as regression; required host/session, data-loss and quality failures block release without adding unselected-platform certification.**

Every task listed in completion_requires must be done and reviewed under the shared definition of done. Record actual evidence with the canonical pass/fail/blocked/not_run status. Required failures or not-run checks prevent completion; a future Epic is not evidence for this exit.

## Acceptance contribution

C01, C02, C03, C04, C05, C06, C07, C08, C09, M01, M02, M03, M04, M05, M06, M07, M08, M09, M10, M11, M12, M13, M14, M15, M16, M17, M18, M19, M20. See [traceability](../testing/traceability.md) and the task-specific supplemental cases. Contribution does not certify an unexercised full scenario. Knowledge-related gates use [knowledge quality](../testing/knowledge-quality.md).

## Handoff

Verified artifacts, exact versions/hashes, install/upgrade/recovery/export instructions, evaluated corpus and explicit distribution decision. No public publication or environment change is implied by this document. Use the [Epic gate template](../templates/epic-gate.md) as an index to actual artifacts and identify the exact next task.
