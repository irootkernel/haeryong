---
id: "EPIC-004"
type: "epic"
title: "Connected wiki and search"
status: "planned"
depends_on: ["EPIC-003"]
completion_requires: ["TASK-004-01","TASK-004-02","TASK-004-03","TASK-004-04","TASK-004-05","TASK-004-06"]
---

# EPIC-004: Connected wiki and search

## Goal

Deliver scoped search, portable pinned references/backlinks, later imported-evidence verification and bounded source availability/change observation without rewriting provenance or claiming semantic verification.

## Entry gate

Complete and review EPIC-003, including its passing exit gate, before starting this Epic. Apply the [execution protocol](../execution-protocol.md) and current [personal-tool amendment](../changes/2026-10-10-personal-tool-revision.md) and [directory-results amendment](../changes/2026-10-10-directory-results-revision.md). No task depends on an unimplemented future Epic to meet its stated exit.

## Ordered tasks

| Order | Task | Outcome area | Dependency |
| --- | --- | --- | --- |
| 1 | [TASK-004-01](../tasks/EPIC-004/TASK-004-01.md) | Implement project scopes and bounded source observation | EPIC-003 |
| 2 | [TASK-004-02](../tasks/EPIC-004/TASK-004-02.md) | Implement text and region search | TASK-004-01 |
| 3 | [TASK-004-03](../tasks/EPIC-004/TASK-004-03.md) | Implement canonical pinned references | TASK-004-02 |
| 4 | [TASK-004-04](../tasks/EPIC-004/TASK-004-04.md) | Implement backlinks and separate related panels | TASK-004-03 |
| 5 | [TASK-004-05](../tasks/EPIC-004/TASK-004-05.md) | Implement browser deep links and version navigation | TASK-004-04 |
| 6 | [TASK-004-06](../tasks/EPIC-004/TASK-004-06.md) | Verify two-project knowledge navigation | TASK-004-05 |

Task IDs remain stable; use the explicit order and dependencies. Task count follows coherent outcomes, not a fixed per-Epic quota.

## Exit gate

**Two-project real-sample search and exact-region reuse work; canonical backlinks and metadata obey access/lifecycle policy. The approved exact-revision reader checks imported evidence without AI, preserves original evidence plus separate verification/history, and sets scoped reassessment_required for direct changes with indirect/missing-source limits.**

Every task listed in completion_requires must be done and reviewed under the shared definition of done. Record actual evidence with the canonical pass/fail/blocked/not_run status. Required failures or not-run checks prevent completion; a future Epic is not evidence for this exit.

## Acceptance contribution

C03, C05, C06, C08, M01, M02, M04, M08, M15, M16, M19. See [traceability](../testing/traceability.md) and the task-specific supplemental cases. Contribution does not certify an unexercised full scenario. Knowledge-related gates use [knowledge quality](../testing/knowledge-quality.md).

## Handoff

Complete source registry/minimal reader, imported-evidence checking/history and search/link contracts, Korean/code relevance worksheet, exact cross-project corpus and independent source-observation state. Managed-AI source integration remains EPIC-006; a broker is one implementation option. Use the [Epic gate template](../templates/epic-gate.md) as an index to actual artifacts and identify the exact next task.
