---
id: "EPIC-007"
type: "epic"
title: "Private remote web and multi-device operation"
status: "planned"
depends_on: ["EPIC-006"]
completion_requires: ["TASK-007-01","TASK-007-02","TASK-007-03","TASK-007-04","TASK-007-05","TASK-007-06"]
---

# EPIC-007: Private remote web and multi-device operation

## Goal

Complete Stage A by delivering the real connected understanding workflow from authenticated mobile/desktop browsers while Wails is closed.

## Entry gate

Complete and review EPIC-006, including its passing exit gate, before starting this Epic. Apply the [execution protocol](../execution-protocol.md) and current [personal-tool amendment](../changes/2026-10-10-personal-tool-revision.md) and [directory-results amendment](../changes/2026-10-10-directory-results-revision.md). No task depends on an unimplemented future Epic to meet its stated exit.

## Ordered tasks

| Order | Task | Outcome area | Dependency |
| --- | --- | --- | --- |
| 1 | [TASK-007-01](../tasks/EPIC-007/TASK-007-01.md) | Implement controlled private ingress and endpoint setup | EPIC-006 |
| 2 | [TASK-007-02](../tasks/EPIC-007/TASK-007-02.md) | Complete browser pairing and session management UX | TASK-007-01 |
| 3 | [TASK-007-03](../tasks/EPIC-007/TASK-007-03.md) | Complete mobile/tablet workflow parity | TASK-007-02 |
| 4 | [TASK-007-04](../tasks/EPIC-007/TASK-007-04.md) | Implement multi-device reconciliation and conflict UI | TASK-007-03 |
| 5 | [TASK-007-05](../tasks/EPIC-007/TASK-007-05.md) | Verify headless-host source and provider boundaries | TASK-007-04 |
| 6 | [TASK-007-06](../tasks/EPIC-007/TASK-007-06.md) | Close Stage A remote connected-reader gate | TASK-007-05 |

Task IDs remain stable; use the explicit order and dependencies. Task count follows coherent outcomes, not a fixed per-Epic quota.

## Exit gate

**C01–C09, M08, M10, M14 and M15–M20 pass under current owner-amended applicability for the selected Stage A environment and real knowledge-quality corpus. Actual private second-device reading/notes/review/Q&A, useful JavaScript, host/native separation, headless lifetime and reconnect consistency are demonstrated.**

Every task listed in completion_requires must be done and reviewed under the shared definition of done. Record actual evidence with the canonical pass/fail/blocked/not_run status. Required failures or not-run checks prevent completion; a future Epic is not evidence for this exit.

## Acceptance contribution

C01, C02, C03, C04, C05, C06, C07, C08, C09, M08, M10, M14, M15, M16, M17, M18, M19, M20. See [traceability](../testing/traceability.md) and the task-specific supplemental cases. Contribution does not certify an unexercised full scenario. Knowledge-related gates use [knowledge quality](../testing/knowledge-quality.md).

## Handoff

Stage A passing record with actual provider, primary browser/WebView, second device, source and private route; real two-project reader outcomes. Manual/periodic maintenance is not claimed complete. Use the [Epic gate template](../templates/epic-gate.md) as an index to actual artifacts and identify the exact next task.
