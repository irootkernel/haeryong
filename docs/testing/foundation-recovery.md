# TASK-002-06 foundation recovery evidence

**Task:** [TASK-002-06](../tasks/EPIC-002/TASK-002-06.md)\
**Implementation status:** done\
**Evidence status:** current native gates, independent whole technical review and Task/Epic Judges approved; headless foundation scope complete\
**Base revision:** `84715aa967dc1d21ab621a1aa8ba7cb15da3308d` (completed TASK-002-05)\
**Executed:** 2026-10-17 UTC / Asia/Seoul\
**Environment:** macOS 27.0.1 ARM64 and cached Linux ARM64 container, Go 1.27.1, modernc.org/sqlite v1.60.1 / SQLite 3.53.4, Gaori 0.1.18 CLI

## Actual scope

The new integration package runs the existing profile lock, SQLite, immutable objects, owner authentication, import service and job/receipt/event ports. Production implementation and migrations remain unchanged. A child test binary receives its private descriptor on stdin and stops at an acknowledged pipe barrier supplied through existing filesystem fault hooks or a test-only typed Store/WriteTx decorator. No production environment switch, public termination API or alternate coordinator is introduced. The parent verifies writer exclusion, sends actual SIGKILL, checks the native signal result and reopens under the normal writer lock.

Separate tests build and run the unmodified production CLI/daemon. A creator process dies after its committed import acceptance; the daemon's real background worker completes that original job. A protected CLI process reads the original receipt and exits. The actual daemon is then killed and restarted, preserving library/profile/runtime/auth/stream identity while changing its process instance. Replaying the original HTTP request returns its original 202 job-creation receipt without another version or outbox event. Job outcome and committed creation receipt remain separate facts.

These are native process-termination and injected-error tests, not hardware power-cut proof. Existing write, FileSync, rename, directory-sync and SQLite commit error cases execute separately; process death cannot substitute for those error paths. Current authentication/configuration/scoped-permission and complete-byte regressions remain required in the whole native gates.

## Predeclared observables and focused results

| Boundary / contribution | Expected observable | Actual focused result |
| --- | --- | --- |
| X17 staging write, before FileSync, after FileSync/before close, before rename, after rename/before directory sync, after directory sync/before DB commit | No acknowledged checkpoint or READY metadata for uncommitted bytes; stable identities/lock and exact retained-input retry | pass; six actual child kills |
| X11 receipt reservation before commit | Reservation/effect rolls back; same original operation can be accepted once | pass; actual child kill |
| X11 committed 202 before return | Original durable response/job survives creator death and replays before stale checkpoint validation; changed semantic checkpoint conflicts | pass; actual child kill |
| X11 local claim and atomic version/outbox commit | Interrupted claim retains its generation fence; precommit publication is invisible and postcommit publication remains complete | pass; three actual child kills, one final version/publication event |
| X14 persisted GC reservation before deletion and after deletion/before directory sync | Deleting fence survives; late pins cannot bypass it; same reservation reconciles while independent owners remain protected | pass; two actual child kills |
| T-002-06-02 accepted work, actual CLI exit and actual production daemon restart | Original accepted job finishes independently; same logical library and original receipt, no duplicate mutation/event | pass; creator child kill and two production daemon SIGKILLs |
| X14 pin ownership and orphan adoption | Verified read outlives stopped-job/staging release; stale success and failure attempts cannot release/adopt or append events; a durable orphan never becomes false READY and can be verified and adopted later | pass; three actual SQLite/filesystem integration cases |
| Separate X17 I/O/SQLite faults | Actual injected write/flush/finalize/commit errors remain unpublished and recoverable | pass; existing import/foundation fault cases repeated in the focused command |
| Ready profile loses storage | Explicit unavailable result, no empty replacement library | pass; existing `TestReadyStorageLossNeverCreatesSubstitute` repeated |
| C02/X01 real generated input regression | Complete represented trees and immutable file bytes survive publication, cursor reads and reopen | pass; existing Gaori and Mulgae native-generated inputs |

The structured focused trace records fourteen child kills; the production-daemon case separately asserts two native SIGKILL results. The process-claim test establishes persisted generation/publication fencing; both of its late callback calls carry nonempty failure text. Distinct stale success (empty failure argument) and failure callbacks are proved by `TestPinLifecycleReassignmentFencesOwnershipAndOutboxEffects`, which also verifies that neither callback runs or changes the event head after reopen/reassignment. Gaori's current expanded focused command passed in 11.044s with all 184 captured input hashes unchanged. Earlier failures remain retained: the first attempt decoded a compound upload response as a string map and failed before any child crash; the next attempted checkpoint oracle assumed generation zero rather than comparing the actual pre-kill checkpoint. A later production-CLI fixture used an insufficiently private testing temporary-directory leaf; it was corrected to an explicit 0700 caller directory and bounded CLI diagnostics. None required a production behavior change.

The real inputs are the public native-generated corpus from [the accepted bounded G-VALUE experiment](knowledge-value-spike.md), not R5 sample state or synthetic corruption. Gaori contains four files / 52,259 bytes and seven represented entries; Mulgae contains four files / 55,189 bytes and six entries. Both are imported into distinct projects, then every path/type/size/hash is checked after reopen and each file is read through the verified pinned delivery path. This repeats byte/persistence behavior; it does not invent new human approval or repeat provider generation/usefulness assessment.

## Native headless entrypoint and verification basis

`bash tests/integration/foundation/check-headless.sh` is the new offline native entrypoint. It requires the exact Go version from go.mod, a native macOS/Linux ARM64 C toolchain, Python with PyYAML and cached modules. It does not install tools, fetch modules, enroll a common Aquarium test contract or migrate test frameworks. It checks docs, selected Python contracts, cached module integrity, vet and production dependency direction, executes the root race suite including foundation and canonical CLI F12, and builds four CGO-disabled Darwin/Linux ARM64/AMD64 headless binaries. AMD64 compilation does not establish runtime support.

The root test JSON and private profiles stay local. A small parser checks native result/skip status and the required F12 case without publishing arbitrary test output. Safe structured workload, process-boundary, real-input and environment reports accompany the original Gaori summary/status and exact source-input captures. The GitHub workflow is a manual definition for explicitly provisioned self-hosted native runners; no runner registration or remote CI execution is claimed.

Current native results are:

| Actual command / environment | Result |
| --- | --- |
| `bash tests/integration/foundation/check-headless.sh`, native macOS ARM64 | pass, 491.126s: docs, all 32 selected contracts, module verification, vet, dependency direction, root race including canonical CLI/HTTP F12 and foundation, four compilation targets |
| `go test -race -count=1 -timeout 1000s ./...`, pinned cached Linux ARM64 image, network disabled | pass, 405.183s: whole root race including canonical CLI/HTTP F12 and foundation |

The Mac entrypoint recorded 344 named pass events, including subtests, with no named test skip or failure. Eight package-level no-test-file statuses are separate metadata. Each native run records fourteen child-kill boundaries, and the production-daemon test independently asserts its two SIGKILL results. Both runs retained all 455 captured repository inputs unchanged. The execution-input array SHA-256 is `12c0d3d9ba6fa334a40431091b5f8d8c013f5aff84c2582491ee1972f1b7dba7`; the final factual documentation update follows those runs and does not alter executable inputs.

The first full Mac entrypoint failed in 495.746s: its private `umask 077` made an existing public-permission fixture's requested 0644 creation actually 0600. Explicit fixture chmod restores the intended unsafe-mode oracle without changing the production 0600 check. The new runner also initially counted package-level no-test-file statuses as skipped tests; it now distinguishes them while still rejecting named skips, failures, nonzero exits and missing F12 completion. A scratch-only classifier check first failed before Go execution because of a Python quoting error. The corrected focused command passed in 1.972s under the same private umask and verified five classifier cases before the complete Mac successor. Every original failed result remains retained. Four builds were not reached by the failed full entrypoint.

Mac and Linux canonical file maps match for all 1,027 files, and both real-input reports match. The canonical F12 regression retains its exact 12,768,177 ZIP bytes, 55,050,821 expanded bytes, 40MiB HTML, 12MiB seeded poorly compressible attachment, 1,024 ordinary files and 69-byte first HTML: 1,027 files plus four represented directories. Its 600-second fixture budget and complete transfer/resume/cursor/range/download/restart assertions are unchanged. Finite test budgets and page/chunk sizes are not product content quotas.

## Contracts, recovery effects and handoff

No schema, API, immutable identity, configured policy, session authority, receipt semantics or pin/reclamation policy changes are introduced by this Task's tests and CI definition. Current schema5 migrations and prior-schema recovery/fault cases repeat in the required root suite. The new tests use a disposable profile and real production verifier; typed job/project fixtures remain labelled infrastructure fixtures. They do not implement provider attempts, backup/export/restore engines, grant issuance, a renderer or future consumer CRUD.

T-002-06-01 contributes current X11/X14/X17 exactly as listed above. T-002-06-02 covers durable local work, creator/client lifetime, actual daemon identity/lock recovery and honest interrupted generations. T-002-06-03 requires current prerequisite/future-error regressions plus canonical X01/X18 and native headless gates. C02's backend import/persistence boundary is exercised; M05/M06/M09/M10 are the implemented foundation contributions, not the complete later AI, restore, desktop or release scenarios.

Final source/candidate captures, original native receipts, failure lineage and structured observations remain in the local handoff index. Passing raw logs and arbitrary passing root-test output remain closed. Only selected failed-test diagnostics from the first failed root JSON and derived status aggregates were inspected. Current native checks, independent whole technical review and the Task/Epic Judges are approved. TASK-002-06 and EPIC-002 are complete for the headless foundation scope. The ordered successor TASK-003-01 is eligible but not started; later UI/renderer, provider, backup/restore and private-device obligations remain with their implementing owners. See the [EPIC-002 foundation handoff](epic-002-foundation-gate.md).
