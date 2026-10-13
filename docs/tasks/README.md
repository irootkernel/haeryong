# Task execution index

**This revision contains 61 task specifications across ten sequential Epics.** This is an inventory, not a required task count. Canonical Task frontmatter owns current planning state. Split/merge tasks by coherent outcomes while updating dependencies and traceability. [TASK-001-01](EPIC-001/TASK-001-01.md#inspected-handler-metadata-mapping) records the inspected epic-handler mapping.

The [R5 interactive concept](../design/prototype/README.md) and [UI handoff](../design/prototype/HANDOFF.md) supply UI baseline inputs. [TASK-001-01](EPIC-001/TASK-001-01.md) records the initial execution choices; prototype review records describe sample behavior and do not establish Task completion or product-gate results. Dependencies and completion requirements remain with the canonical Tasks.

All task checks use the [D-08 personal-tool baseline](../changes/2026-10-10-personal-tool-revision.md), [D-09 directory-results baseline](../changes/2026-10-10-directory-results-revision.md) and [1.6.0 Grok reconciliation](../changes/2026-10-11-grok-reconciliation.md), including useful html_js_v1 JavaScript, unrestricted directory input/output, remembered one-owner access and D-10's approved extra private renderer-port experiment. The complete TASK-001-04 and TASK-001-05 checklists govern G-VIEW/G-AI; later product features do not become additional early closing conditions.

TASK-001-07 deliberately executes between TASK-001-05 and TASK-001-06 so real value evidence precedes contract freeze. Numeric IDs do not override the graph. Baseline review is the first row's human-readable entry condition, not a missing dependency ID; TASK-001-01 has an empty depends_on list. Its preliminary reader worksheet does not pass the later G-VALUE gate.

| Order | Task | Title | Depends on |
| --- | --- | --- | --- |
| 1 | [TASK-001-01](EPIC-001/TASK-001-01.md) | Ratify baseline, UI brief and execution conventions | Baseline review |
| 2 | [TASK-001-02](EPIC-001/TASK-001-02.md) | Prove headless stack and native companion | TASK-001-01 |
| 3 | [TASK-001-03](EPIC-001/TASK-001-03.md) | Validate pairing and private ingress design | TASK-001-02 |
| 4 | [TASK-001-04](EPIC-001/TASK-001-04.md) | Prove useful JavaScript and host separation | TASK-001-03 |
| 5 | [TASK-001-05](EPIC-001/TASK-001-05.md) | Validate subscription adapter and exact source reads | TASK-001-04 |
| 6 | [TASK-001-07](EPIC-001/TASK-001-07.md) | Validate the two-project understanding workflow | TASK-001-05 |
| 7 | [TASK-001-06](EPIC-001/TASK-001-06.md) | Freeze contracts and close readiness gate | TASK-001-07 |
| 8 | [TASK-002-01](EPIC-002/TASK-002-01.md) | Implement profile identity and daemon lifecycle | EPIC-001 |
| 9 | [TASK-002-02](EPIC-002/TASK-002-02.md) | Implement SQLite repositories and immutable object storage | TASK-002-01 |
| 10 | [TASK-002-03](EPIC-002/TASK-002-03.md) | Implement local bootstrap and authenticated API sessions | TASK-002-02 |
| 11 | [TASK-002-04](EPIC-002/TASK-002-04.md) | Implement atomic directory import and publication | TASK-002-03 |
| 12 | [TASK-002-05](EPIC-002/TASK-002-05.md) | Implement query API, CLI and configuration authority | TASK-002-04 |
| 13 | [TASK-002-06](EPIC-002/TASK-002-06.md) | Verify service crash boundaries and foundation gate | TASK-002-05 |
| 14 | [TASK-003-01](EPIC-003/TASK-003-01.md) | Build shared frontend shell and transport adapters | EPIC-002 |
| 15 | [TASK-003-02](EPIC-003/TASK-003-02.md) | Implement Home, Library and result transfer UI | TASK-003-01 |
| 16 | [TASK-003-03](EPIC-003/TASK-003-03.md) | Implement the multi-page interactive Reader | TASK-003-02 |
| 17 | [TASK-003-04](EPIC-003/TASK-003-04.md) | Implement exact version and evidence context | TASK-003-03 |
| 18 | [TASK-003-05](EPIC-003/TASK-003-05.md) | Implement the thin Wails desktop companion | TASK-003-04 |
| 19 | [TASK-003-06](EPIC-003/TASK-003-06.md) | Verify responsive reader and cross-client gate | TASK-003-05 |
| 20 | [TASK-004-01](EPIC-004/TASK-004-01.md) | Implement project scopes and bounded source observation | EPIC-003 |
| 21 | [TASK-004-02](EPIC-004/TASK-004-02.md) | Implement text and region search | TASK-004-01 |
| 22 | [TASK-004-03](EPIC-004/TASK-004-03.md) | Implement canonical pinned references | TASK-004-02 |
| 23 | [TASK-004-04](EPIC-004/TASK-004-04.md) | Implement backlinks and separate related panels | TASK-004-03 |
| 24 | [TASK-004-05](EPIC-004/TASK-004-05.md) | Implement browser deep links and version navigation | TASK-004-04 |
| 25 | [TASK-004-06](EPIC-004/TASK-004-06.md) | Verify two-project knowledge navigation | TASK-004-05 |
| 26 | [TASK-005-01](EPIC-005/TASK-005-01.md) | Implement explicit version-specific review | EPIC-004 |
| 27 | [TASK-005-02](EPIC-005/TASK-005-02.md) | Implement anchored notes and conflict handling | TASK-005-01 |
| 28 | [TASK-005-03](EPIC-005/TASK-005-03.md) | Implement archive, trash and restore | TASK-005-02 |
| 29 | [TASK-005-04](EPIC-005/TASK-005-04.md) | Implement guarded manual purge and content GC | TASK-005-03 |
| 30 | [TASK-005-05](EPIC-005/TASK-005-05.md) | Implement coherent backup, portability export and protected restore | TASK-005-04 |
| 31 | [TASK-005-06](EPIC-005/TASK-005-06.md) | Verify personal/lifecycle workflows and recovery gate | TASK-005-05 |
| 32 | [TASK-006-01](EPIC-006/TASK-006-01.md) | Extend local jobs with provider attempts and reconciliation | EPIC-005 |
| 33 | [TASK-006-02](EPIC-006/TASK-006-02.md) | Implement configured source integration and selected snapshots | TASK-006-01 |
| 34 | [TASK-006-03](EPIC-006/TASK-006-03.md) | Implement the verified provider adapter | TASK-006-02 |
| 35 | [TASK-006-04](EPIC-006/TASK-006-04.md) | Implement Haeryong-owned generation skill | TASK-006-03 |
| 36 | [TASK-006-05](EPIC-006/TASK-006-05.md) | Implement region-aware source-grounded questions | TASK-006-04 |
| 37 | [TASK-006-06](EPIC-006/TASK-006-06.md) | Verify AI workflow, interruption and allowance boundaries | TASK-006-05 |
| 38 | [TASK-007-01](EPIC-007/TASK-007-01.md) | Implement controlled private ingress and endpoint setup | EPIC-006 |
| 39 | [TASK-007-02](EPIC-007/TASK-007-02.md) | Complete browser pairing and session management UX | TASK-007-01 |
| 40 | [TASK-007-03](EPIC-007/TASK-007-03.md) | Complete mobile/tablet workflow parity | TASK-007-02 |
| 41 | [TASK-007-04](EPIC-007/TASK-007-04.md) | Implement multi-device reconciliation and conflict UI | TASK-007-03 |
| 42 | [TASK-007-05](EPIC-007/TASK-007-05.md) | Verify headless-host source and provider boundaries | TASK-007-04 |
| 43 | [TASK-007-06](EPIC-007/TASK-007-06.md) | Close Stage A remote connected-reader gate | TASK-007-05 |
| 44 | [TASK-008-01](EPIC-008/TASK-008-01.md) | Implement scan inventory and deterministic structure checks | EPIC-007 |
| 45 | [TASK-008-02](EPIC-008/TASK-008-02.md) | Implement bounded relationship/topic candidate discovery | TASK-008-01 |
| 46 | [TASK-008-03](EPIC-008/TASK-008-03.md) | Implement region-scoped freshness assessment | TASK-008-02 |
| 47 | [TASK-008-04](EPIC-008/TASK-008-04.md) | Implement management overlays and proposal presentation | TASK-008-03 |
| 48 | [TASK-008-05](EPIC-008/TASK-008-05.md) | Implement conflict-safe approval, rejection and undo | TASK-008-04 |
| 49 | [TASK-008-06](EPIC-008/TASK-008-06.md) | Implement content drafts, full report and manual-maintenance gate | TASK-008-05 |
| 50 | [TASK-009-01](EPIC-009/TASK-009-01.md) | Complete validated scheduling and policy controls | EPIC-008 |
| 51 | [TASK-009-02](EPIC-009/TASK-009-02.md) | Implement durable schedules and missed-run coalescing | TASK-009-01 |
| 52 | [TASK-009-03](EPIC-009/TASK-009-03.md) | Enforce per-run budgets and uncertain-outcome accounting | TASK-009-02 |
| 53 | [TASK-009-04](EPIC-009/TASK-009-04.md) | Enforce current restrictions during running jobs | TASK-009-03 |
| 54 | [TASK-009-05](EPIC-009/TASK-009-05.md) | Implement explicitly enabled retention execution | TASK-009-04 |
| 55 | [TASK-009-06](EPIC-009/TASK-009-06.md) | Verify unattended operation and close Stage C gate | TASK-009-05 |
| 56 | [TASK-010-01](EPIC-010/TASK-010-01.md) | Package reproducible service and desktop distributions | EPIC-009 |
| 57 | [TASK-010-02](EPIC-010/TASK-010-02.md) | Run migration, backup and destructive recovery qualification | TASK-010-01 |
| 58 | [TASK-010-03](EPIC-010/TASK-010-03.md) | Verify host boundaries and selected-device recovery | TASK-010-02 |
| 59 | [TASK-010-04](EPIC-010/TASK-010-04.md) | Qualify performance, Korean retrieval and resource behavior | TASK-010-03 |
| 60 | [TASK-010-05](EPIC-010/TASK-010-05.md) | Complete operator documentation and owner acceptance walkthrough | TASK-010-04 |
| 61 | [TASK-010-06](EPIC-010/TASK-010-06.md) | Close integrated release gate and publish handoff evidence | TASK-010-05 |

The [execution protocol](../execution-protocol.md) owns the shared definition of done and evidence semantics. Each task contains its unique checks; acceptance IDs indicate contribution until the complete owning-stage scenario passes.
