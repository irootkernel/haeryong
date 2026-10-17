# EPIC-002 foundation handoff

**Epic:** [EPIC-002](../epics/EPIC-002.md)\
**Decision:** pass\
**Reviewed revision/environment:** TASK-002-05 base `84715aa967dc1d21ab621a1aa8ba7cb15da3308d` plus current TASK-002-06 candidate; current macOS/Linux ARM64 native gates pass\
**Independent reviewer:** agent-operated whole-candidate technical review and Task/Epic report-only Judge assessments approved for the headless foundation scope

## Required tasks

| Task | Committed foundation | Canonical execution evidence | Required checks / review |
| --- | --- | --- | --- |
| [TASK-002-01](../tasks/EPIC-002/TASK-002-01.md) | `db6ba79b528aa97096ab1bb6d3141b62a90a991a` | [Profile and daemon lifecycle](profile-lifecycle.md) | pass / accepted predecessor |
| [TASK-002-02](../tasks/EPIC-002/TASK-002-02.md) | `c55eae29c23464422069272d51f5ccc3f91abec0` | [SQLite/object/operation foundations](storage-foundation.md) | pass / accepted predecessor |
| [TASK-002-03](../tasks/EPIC-002/TASK-002-03.md) | `1bb66bf1cc9982533511ad22895140966f7d1a2a` | [Owner authentication and sessions](authenticated-sessions.md) | pass / accepted predecessor |
| [TASK-002-04](../tasks/EPIC-002/TASK-002-04.md) | `6be8f86806dfa51bd35c99c33bf64a9ecdce598c` | [Atomic complete-directory import](atomic-import.md) | pass / accepted predecessor |
| [TASK-002-05](../tasks/EPIC-002/TASK-002-05.md) | `84715aa967dc1d21ab621a1aa8ba7cb15da3308d` | [Queries, durable CLI and configuration](query-cli-configuration.md) | pass / accepted predecessor |
| [TASK-002-06](../tasks/EPIC-002/TASK-002-06.md) | completed candidate, uncommitted | [Current crash/fault and headless evidence](foundation-recovery.md) | pass / approved Task |

## Gate observables

The headless Go service has one profile/writer, SQLite and immutable-byte storage, owner sessions and genuine scoped credential enforcement. Complete directory staging, asynchronous seal/import jobs, principal-scoped operation receipts, current-authority reads, revisioned configuration, numeric event streams and verified cursor/range/file delivery are implemented. The actual core CLI preserves preparation/final operation identities, captured input/target/base and recovery phases through response loss. Ordinary result directories need no compulsory manifest or index and have no artificial total content/count/depth limit.

The final foundation regression repeats complete multi-page native-generated Gaori/Mulgae inputs and the exact canonical above-former-limit F12. Process barriers and separate injected file/SQLite errors must leave either no visible mutation or a complete durable immutable publication. Original receipt replay cannot duplicate a version or event, stale attempts cannot apply success/failure, active/shared/staging pins prevent GC, and dead collector reservations remain fenced and resumable. Actual production daemon restart preserves the logical library/auth/event epochs and replaces its process instance; ready storage loss must not initialize a substitute.

## Acceptance and capability limits

C02 is the delivered backend import/persistence boundary. M05/M06/M09 and the relevant M10 behavior are early foundation contributions, with current X01/X11/X14/X17/X18 repetitions indexed in Task06. Later real AI application/reconciliation, coherent backup/export/restore engines, Wails lifecycle/renderer and private-device release scenarios retain their owning tasks. Native SIGKILL and injected flush errors are not hardware power-loss certification.

The existing trusted connect UI and protected session/token boundary retain their recorded predecessor browser evidence. This handoff does not claim a production reader, executable generated HTML delivery, native bridge, real provider execution, scheduler, public/private ingress deployment or a physical-device gate. Capability-disabled endpoints remain explicit. The new CI workflow is configuration only; local native execution and remote CI are distinct evidence classes.

## Decision and next dependency

All six required Tasks are done. Current native required checks, independent whole technical review, the fresh Task06 Judge and the standing Epic Judge are approved; Decision is `pass` and EPIC-002 is done for the headless foundation scope. These reviews are agent-operated and do not certify later human, device, provider or release scenarios. No required failure or not-run check is waived by this handoff.

The next ordered implementation is [TASK-003-01](../tasks/EPIC-003/TASK-003-01.md), shared frontend shell and transport adapters. Its EPIC-002 dependency is satisfied, so it is eligible for ordered execution; it is not started or marked ready by writing this document. Existing byte/receipt/configuration/job/event/pin ports are its current service boundary; future implementation consumes those ports without replacing durability or captured-intent rules.
