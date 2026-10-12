---
id: "EPIC-002"
type: "epic"
title: "Go service and storage foundation"
status: "planned"
depends_on: ["EPIC-001"]
completion_requires: ["TASK-002-01","TASK-002-02","TASK-002-03","TASK-002-04","TASK-002-05","TASK-002-06"]
---

# EPIC-002: Go service and storage foundation

## Goal

Deliver the independent authenticated service, atomic directory import without arbitrary input quotas, and minimal durable local job/receipt/event/revision/pin infrastructure consumed by later features.

## Entry gate

Complete and review EPIC-001, including its passing exit gate, before starting this Epic. Apply the [execution protocol](../execution-protocol.md) and current [personal-tool amendment](../changes/2026-10-10-personal-tool-revision.md) and [directory-results amendment](../changes/2026-10-10-directory-results-revision.md). No task depends on an unimplemented future Epic to meet its stated exit.

## Ordered tasks

| Order | Task | Outcome area | Dependency |
| --- | --- | --- | --- |
| 1 | [TASK-002-01](../tasks/EPIC-002/TASK-002-01.md) | Implement profile identity and daemon lifecycle | EPIC-001 |
| 2 | [TASK-002-02](../tasks/EPIC-002/TASK-002-02.md) | Implement SQLite repositories and immutable object storage | TASK-002-01 |
| 3 | [TASK-002-03](../tasks/EPIC-002/TASK-002-03.md) | Implement local bootstrap and authenticated API sessions | TASK-002-02 |
| 4 | [TASK-002-04](../tasks/EPIC-002/TASK-002-04.md) | Implement atomic directory import and publication | TASK-002-03 |
| 5 | [TASK-002-05](../tasks/EPIC-002/TASK-002-05.md) | Implement query API, CLI and configuration authority | TASK-002-04 |
| 6 | [TASK-002-06](../tasks/EPIC-002/TASK-002-06.md) | Verify service crash boundaries and foundation gate | TASK-002-05 |

Task IDs remain stable; use the explicit order and dependencies. Task count follows coherent outcomes, not a fixed per-Epic quota.

## Internal implementation slices

Within the ordered Tasks, first build single-session streamed staging/sealing with the minimum receipt, pin/adoption and durability work needed for one atomic directory publication. Next complete interruption/resume, owner upload discovery and explicit abandon. Then extend the same foundations to other upload purposes and their adoption races. Use a small imported real result for bounded service-level inspection as soon as the first slice works; this remains EPIC-002 evidence and does not make EPIC-003 Tasks ready or done. All listed foundations and failure checks remain required for this Epic's exit.

## Exit gate

**A selected directory with multiple HTML pages imports without compulsory metadata or arbitrary size/count caps and reopens with all file paths/bytes intact. Principal-scoped receipts, streamed/resumable transport, local jobs, revisioned events, object pins and auth/config recovery are real; crash/flush faults cannot publish incomplete data, duplicate mutations or expose unauthorized results.**

Every task listed in completion_requires must be done and reviewed under the shared definition of done. Record actual evidence with the canonical pass/fail/blocked/not_run status. Required failures or not-run checks prevent completion; a future Epic is not evidence for this exit.

## Acceptance contribution

C02, M05, M06, M08, M09, M10, M11, M16, M17, M20. See [traceability](../testing/traceability.md) and the task-specific supplemental cases. Contribution does not certify an unexercised full scenario. Knowledge-related gates use [knowledge quality](../testing/knowledge-quality.md).

## Handoff

Runnable headless service and explicit local job/receipt/status/event APIs, current migrations and pin ownership. EPIC-005 can implement backup/export without waiting for the EPIC-006 provider coordinator. Use the [Epic gate template](../templates/epic-gate.md) as an index to actual artifacts and identify the exact next task.
