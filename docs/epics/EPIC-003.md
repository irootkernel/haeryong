---
id: "EPIC-003"
type: "epic"
title: "Shared reader and Wails client"
status: "planned"
depends_on: ["EPIC-002"]
completion_requires: ["TASK-003-01","TASK-003-02","TASK-003-03","TASK-003-04","TASK-003-05","TASK-003-06"]
---

# EPIC-003: Shared reader and Wails client

## Goal

Make multi-page result directories and useful html_js_v1 JavaScript readable in shared browser and independent Wails clients, preserving local URL behavior, on-demand files, separate host authority and practical page recovery.

## Entry gate

Complete and review EPIC-002, including its passing exit gate, before starting this Epic. Apply the [execution protocol](../execution-protocol.md) and current [personal-tool amendment](../changes/2026-10-10-personal-tool-revision.md) and [directory-results amendment](../changes/2026-10-10-directory-results-revision.md). No task depends on an unimplemented future Epic to meet its stated exit.

## Ordered tasks

| Order | Task | Outcome area | Dependency |
| --- | --- | --- | --- |
| 1 | [TASK-003-01](../tasks/EPIC-003/TASK-003-01.md) | Build shared frontend shell and transport adapters | EPIC-002 |
| 2 | [TASK-003-02](../tasks/EPIC-003/TASK-003-02.md) | Implement Home, Library and result transfer UI | TASK-003-01 |
| 3 | [TASK-003-03](../tasks/EPIC-003/TASK-003-03.md) | Implement the multi-page interactive Reader | TASK-003-02 |
| 4 | [TASK-003-04](../tasks/EPIC-003/TASK-003-04.md) | Implement exact version and evidence context | TASK-003-03 |
| 5 | [TASK-003-05](../tasks/EPIC-003/TASK-003-05.md) | Implement the thin Wails desktop companion | TASK-003-04 |
| 6 | [TASK-003-06](../tasks/EPIC-003/TASK-003-06.md) | Verify responsive reader and cross-client gate | TASK-003-05 |

Task IDs remain stable; use the explicit order and dependencies. Task count follows coherent outcomes, not a fixed per-Epic quota.

## Exit gate

**The shared UI imports selected result directories/ZIP/HTML and streams complete original-directory downloads on the existing service transport, then navigates every page of an exact directory version and runs useful html_js_v1 JavaScript/modules in browser and Wails. Nested relative links, page-local context and on-demand files work without a whole-directory render envelope. Generated frames have no host DOM/token/native authority; browser-permitted networking works and broken pages have a recovery path.**

Every task listed in completion_requires must be done and reviewed under the shared definition of done. Record actual evidence with the canonical pass/fail/blocked/not_run status. Required failures or not-run checks prevent completion; a future Epic is not evidence for this exit.

## Acceptance contribution

C02, C03, C04, M08, M10, M14, M15, M17, M19. See [traceability](../testing/traceability.md) and the task-specific supplemental cases. Contribution does not certify an unexercised full scenario. Knowledge-related gates use [knowledge quality](../testing/knowledge-quality.md).

## Handoff

Production html_js_v1 compatibility, local render-byte integrity and evidence-navigation/JavaScript walkthrough, with external dependencies and untested devices stated explicitly. Use the [Epic gate template](../templates/epic-gate.md) as an index to actual artifacts and identify the exact next task.
