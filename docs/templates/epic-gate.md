# Epic exit gate template

**Epic:** EPIC-XXX
**Decision:** not_run
**Reviewed revision/environment:** not recorded
**Independent reviewer:** not assigned

## Required tasks

Copy the exact IDs from the Epic's completion_requires list; task count is not fixed.

| Task | Evidence link | Required checks pass | Review decision |
| --- | --- | --- | --- |
| Not populated | None | not_run | not_run |

## Gate observables

Record the actual end-to-end demonstration, capability boundaries, exact source/toolchain/profile/device and limitations. Every required current task and Epic exit must pass under the [personal-tool baseline](../changes/2026-10-10-personal-tool-revision.md) and [directory-results baseline](../changes/2026-10-10-directory-results-revision.md); evidence collection alone cannot close the gate. Additional unselected platforms or superseded security assumptions do not become hidden requirements.

For EPIC-001, record G-STACK, G-AUTH, G-VIEW, G-AI, G-VALUE and G-CONTRACT individually. All six must pass their narrow practical checks for the chosen baseline. G-VALUE is a thin real-generation/reader experiment, not complete production Stage A.

## Acceptance and quality coverage

Identify complete owning-stage scenarios separately from early bounded contributions and later release regression. M08/M14/M20 complete their selected Stage A scope by EPIC-007; EPIC-010 does not supply missing evidence for that earlier exit. Stage A requires C01–C09, M08, M10, M14 and M15–M20 under current applicability plus the real knowledge-quality corpus and actual private second-device use. Later maintenance/release gates add their full obligations. Link initial feature proof and relevant regression rather than assuming the latter can be deferred.

## Decision

Record `pass`, `fail`, `blocked` or `not_run`, rationale, reviewer, date and exact next allowed Epic/task under the execution protocol's canonical evidence enum. Use `fail` plus a repair reason for needs-rework. A failed invariant or required not_run result blocks completion. No owner approval or actual test success is inferred from writing this template.
