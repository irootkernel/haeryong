# Coherent backup, portable export and protected restore

**Task:** [TASK-005-05](../tasks/EPIC-005/TASK-005-05.md), EPIC-005 — status `done`
**Source revision:** implementation base `faa848fd` plus the TASK-005-05 change set; the final completion suite and campaigns ran on the frozen final candidate equal to the committed tree. Exact execution bindings stay in private receipts.
**Construction:** SQLite revision 14, host-local owner submission, manual coherent backup, exact directory/ZIP export, exclusive protected restore and shared real backup status.
**Environment:** prepared macOS arm64 Go/Python/Node/Chrome; disposable synthetic owner libraries, no installs, provider calls, host ingress or source-repository changes.
**Evidence status:** pass for the final-candidate checks below; earlier failed attempts remain failures.
**Implementer / independent reviewer:** Executor Writer; Task Evaluator, two non-authoring Executor reviewers and Epic Evaluator; final project/product review and delegated Task acceptance by the Coordinator.

## Delivered construction

The [operations runbook](../operations/backup-recovery.md#implemented-host-local-copy-and-recovery-task-005-05)
and [CLI selection](../contracts/cli.md#host-local-backup-export-and-protected-restore-task-005-05)
own protocol and executable command details. Snapshot capture uses a dedicated SQLite
connection outside the live store gate, with a durable ReserveGC capture barrier and
snapshot-derived bounded pin handoff. Credential rows are removed before final hashing.
Objects copy with prefix validation, finite buffers, synced checkpoints and generation
checks. Final marker/rename/acknowledgement reconcile the same set after interruption.
Retention removes whole owned sets only after a new verified fallback and active-use
lock checks; default indefinite/manual behavior remains unchanged.

Export selection freezes exact published version membership; one version stays an
unchanged plain root and multiple versions have separate UUID roots. Optional personal
records and immutable context/body bytes remain outside every original payload root.
Verification compares full inventory/path/type/size/hash/link identity and selected
record/context data; it does not merge records or fetch external source repositories.
ZIP is emitted directly from the inventory. Actual directory incompatibility fails.

Restore uses an explicit offline profile and stable lock, destination-local staging,
nonsecret uncertainty inventory, early policy/job holds, fresh runtime/auth/stream epochs,
credential removal, canonical reverse-index recreation, recoverable storage/profile switch
and strict startup/object health. New bootstrap and remembered normal restart retain
fresh sessions. TASK-005-06 corrects current-epoch event replay/head selection while
retaining old event rows, including nonzero sequence starts and safe poll/SSE resync.
Its focused restore/replay regressions passed; complete native recovered body/context
proof and the fresh final gate remain pending in [owner composition](owner-records.md).
Existing configuration validation/plan/commit atomically reconciles
selected settings and explicitly enumerated purge jobs. Unknown external outcomes remain
unknown. No future provider-attempt table, ingress or scheduler is invented.

## Executed evidence and limits

| Requirement | Executable evidence owner |
| --- | --- |
| T-005-05-01; X10/X14; Obligation 6 | sqlite Copy snapshot dedicated VACUUM concurrency; query Copies backup production snapshot with actual purge; checkpoint failures/resume and cancelled ownership tests |
| T-005-05-02; X06/X13; Obligations 7/05; M06/M13/M20 contribution | query Copies protected restore and public tests/e2e/test_copies.py: old-session refusal, one rotation, new bootstrap, remembered restart and exact configuration review |
| T-005-05-03; Obligation 11/05; M09 contribution | full SQLite snapshot preserves operational/current/history rows; public Notes/Review/history/Revisit comparisons and byte reads after restore; purge production snapshot preservation |
| T-005-05-04; X18; Obligation 8 | query scoped export and public same-document two-version directory/ZIP/plain export; canonical F12 export verifies all original file bytes and represented directories |
| T-005-05-05 | corruption refusal and capture/object-sync/publication/after-rename injected ENOSPC failures retain prior complete set, then resume the same ID; encryption explicitly unavailable |
| Obligation 9; M20 contribution | frontend backup projection unit; existing browser/native status operation; bounded Chrome Settings keyboard and hasTouch tap/reload; canonical desktop checks |
| Revision/authority boundaries | thirteen-to-fourteen migration rollback/retry/exact declaration check; all prior-schema persistence fixtures; protected HMAC mutation/proof refusal tests and real current-owner local route |

This table identifies executable owners; the review and acceptance section records
the final checks and their limits. Synthetic records are fixtures, not real subscription or
provider dispatch proof. Prior Task source/native/browser proofs retain their original
candidate scope. A technical Writer pass does not establish Task/Epic acceptance.

## Writer runs

Focused driver concurrency, production copy/purge, restore, export and fault selections
were executed during implementation. The actual canonical F12 backup was killed and restarted, retained the original acceptance and passed full verification; its portable ZIP matched every unchanged golden file byte and represented empty directory. Header-only acceptance loss replayed one local copy job; changed bindings conflicted and a remote endpoint was refused. All focused restore switch/health/DDL, exact record/uncertainty and copy/retention/retry selections passed after the repairs described here. Initial failures included an invalid new auth-audit
fixture, configuration JSON number decoding, strict restore marker decoding, local
journal target admission and duplicate default selection normalization; each was repaired
and its affected behavior rerun. A partial contract-file selection was rejected by the
existing all-matrix collection guard; no collection rule or required case was relaxed.
The ordinary full integration selection is required instead. Its later complete
execution exposed the selected schema validator's missing minItems support and a
search contract's historical nine-kind assertion. The validator now checks the
nonempty export selection; the enum test preserves every original kind and the
approved additive offline restore kind. The full matrix is rerun, not weakened. The first full persistence run exposed old-schema comparison table-set drift and an auth-test import cycle; only new revision-14 tables were excluded from prior-schema comparisons, all original row assertions remained, and pure inventory/copy guards moved below authenticated services. The repaired affected selections passed. A new E2E fixture initially attached page query/fragment to a directory-level anchor and was corrected to an actual page.

Writer selections and their actual candidates remain separate from the Coordinator's
completion evidence. The final checks below apply to the frozen completion candidate;
preliminary passes are not relabelled as final-suite proof.

## Continued verification and recovery boundaries

The [EPIC-005 profile](epic-harmony-verification-profile.md) selects the native
keep-going gate. The common command contract remains in root TESTING.md. Selected
Writer checks and the Coordinator whole gate remain separate execution records.
Recovered backup/export publication re-verifies the exact output and re-establishes
payload and separate-metadata parent-directory durability before completion and pin
release. Failure at either recovered sync remains a failed recoverable attempt.
Restore already establishes switch/rollback parent durability through the protected
profile write; no duplicate sync is added merely to match a literal call.
Completed restore replay uses its recorded result before re-evaluating an expired
plan or unavailable original input. An unexecuted expired/stale plan still refuses.

The browser backup driver now uses real completed/failed/unavailable backup sets,
an actual isolated successful restore, and a real health-failed isolated restore
with rollback. Its test-only validator callback removes one staged object after
input copying to force health failure; no product route or release hook exists.
Keyboard and 390×844 hasTouch refresh/reload exercise each resulting status.
These driver capabilities are not an execution claim without their receipts.

## Compatibility and explicit exclusions

API v1 and package schema 1.0 stay unchanged. Format-1 local backup/export definitions,
new local-control action family and restore job kind are additive provisional choices;
status/purge placeholder widening is observable compatibility work. Embedded schemas
retain only their preselected shared definitions/enums. Migration 14 is append-only and
older binaries refuse it. Historical unavailable-backup fixtures now select genuine
not_created status, as required by the approved implementation contract; negative strict
schema checks remain present.

Encryption/key flags are refused; no encrypted-copy proof applies. DF-02 crash-leaked
read pins remain conservatively retained in their existing deferred-feedback owner;
backup/job pins are never reclaimed by heuristic age. DF-03 synchronous read release
and renderer draining remain unchanged. There is no scheduling installation, second
backup destination, physical-device/remote-CI/provider-call proof, full-VM rollback
detection, cross-machine source replication, personal-state merge or forensic erasure
claim. Checksums detect accidental corruption, not malicious replacement. Filesystem
fault injection and process interruption do not establish hardware power-loss immunity.
Directory-guide adoption coverage is absent; no unrelated guide backfill is added.

## First correction class verification

The first independent result review identified eight correctness classes despite the
complete early gate passing: interrupted rollback direction/identity, older-plan terminal
replay, frozen recovered-export identity, complete annotation-history closure, nested
metadata grammar/bindings, restored-output retention authority, dispatcher progress/drain,
and publication directory ancestry. These findings remain distinct from gate outcomes.

Correction regressions use real SQLite/filesystem backup/export/restore paths. They
exercise populated same-library/isolated rollback boundaries and repeated recovery,
terminal-record failure, sequential restores and older natural/alias replay, foreign
valid directory/ZIP/plain/separate-metadata substitutions, partial sidecar publication,
and missing expected publication bindings. Production-created A/B/C reattachments,
object-backed bodies, saved contexts and deletion are compared with independent retained
revision/byte expectations without widening payload membership. Semantic mutations
recompute metadata checksums. The typed reference-emitter fixture is explicitly synthetic.

Real Dispatcher.Run barriers hold snapshot/final transfer/verification/retention while an
actual queued upload-seal job completes. Cancellation, queued claim, stale-generation
polling, terminal-but-uncollected ownership and drain-before-exit have separate oracles.
A snapshot-stage cancellation is protected by its capture barrier before pin handoff;
zero installed backup pins at that boundary do not mean lost protection. Directory sync
failures retain exact resumable ownership; recovered source/destination parents are checked.

Private run/class records retain every original red witness and subsequent outcome,
including test preparation/compilation errors and their repairs. Selected passing checks
support correction review, not final whole-suite/campaign evidence or acceptance. The
Coordinator owns the fresh final gate and independent correction assessments. DF-02
and DF-03 retain their existing scope and guarantees.

## Second correction class verification

The follow-up class sweep separates portable declared relationships, restore operation
bindings and record durability, and current copy-attempt ownership from publication
authority. Focused regressions accompany each mechanism; these are author checks, with
independent correction assessment and the exact-final whole gate still separate.

Standalone export verification retains the selected version/document map until all
index entries have been read. It checks selected Note anchors (including original and
retained history), matching context descriptors, authored-reference/dependency tuples,
and included/excluded membership against the available facts. Whole-version anchors
retain empty navigation values; deleted Notes retain null body/body-object/context/
selected-text fields, while live Notes have exactly one body representation. Declared
Note action and actor/operation identities keep their existing schema meanings. Outside-selection anchors
remain exact without requiring unavailable payload or producer history. Review current
state/stamp coupling and effective history transitions follow the original Review
contract. Source dependencies retain their declared local ID, descriptor grammar and
payload-membership state; repeated identities cannot disagree. Semantic negative fixtures
recompute the containing checksums, while production-created complete/reopen/clear and
full retained A/B/C history remain positive oracles. This establishes internal integrity,
not independent producer authenticity, external source availability or factual correctness.

Protected restore checks natural plan, terminal, alias and available current-owner
receipt bindings before publishing aliases. New/staging applications also check the
preserved incoming owner receipt namespace in the verified backup; switching retries
check the already-protected incoming database. Completed replay does not require the
original input. It refuses unrelated work and uncertain
matching receipts without rebinding keys; principal scopes remain distinct. Existing
plan/active/terminal/alias records require renewed parent-directory sync before adoption.
Tests repeat actual post-rename sync failure, then retry the original outcome without
changing the later library, trial record or epochs. Receipt lookup also handles the
journalled interval when prior storage has moved outside the active path.

The copy dispatcher records authority loss only after owned byte work drains. Safe
cessation uses current set/job/attempt/library/runtime/owner fences without requiring
the revoked project permission or restore-hold publication authority. Failure keeps
capture/checkpoint/pin protection. Completion-acknowledgement failure retains the drained
slot for retry or surfaces an error. Stale cancelled selections cannot mutate a linked
retry or abandoned/completed/expired set, even with coincident attempt numbers. Real
configuration commits during held export, independent queued work, worker-first denial,
cancellation precedence, stale selection and persistence-failure cases cover these paths.

A damaged prior destination may correctly remain startup-refused after physical
rollback. Isolated trial failure projection now uses a narrow metadata transaction,
without treating strict startup as a prerequisite for recording an actual failure.
External failure terminalization allows a later explicit protected restore; it does
not repair the prior index online or claim that the prior is healthy. The compound
regression damages the prior canonical index and the actual incoming health check,
compares original authoritative rows/profile/epochs, retries the same failed plan, and
then applies a healthy backup. Trial/terminal write and post-rename sync faults remain
retryable errors. Callback faults and disposable database damage are labelled evidence,
with no hardware power-loss or real-provider claim. DF-02 and DF-03 are unchanged.

## Third correction class verification

The correction separates verified payload facts, immutable authored declarations
and first-party stored anchors. Selected-snapshot effective digests are checked
against actual asset bytes with the original explicit/fallback precedence. Selected
Note anchors use their exact version's canonical HTML payload root; reader pages
retain the same type rule. Rehashed metadata negatives cannot substitute agreement
among fields for payload membership. Authored references use exact document/version
dependency classification while retaining legitimate unresolved page/region/from
claims and wrong-document declarations verbatim. Full outside-selection Note history
remains supported without expanding payload membership.

Restore admission distinguishes new aliases from existing terminal bindings and
checks logical database sides across physical switch/rollback moves, with exact
library/runtime/profile identity and preserved owner principal scope. Production
post-snapshot prior-only and rollback incoming-only receipt cases cover the phase
boundaries. Old success/failure replay remains independent of later dataset receipts
and original input; new aliases retain conflict checks. Admitted staging can rebuild
after preview expiry while matching journal, destination state and input integrity
remain mandatory; unexecuted expired/invalid plans still refuse without admission.

Focused production-path regressions cover these classes and their consumers. Their
private execution records remain separate from Coordinator's exact-final whole gate,
independent review and serialized campaigns. Disposable faults and rehashed fixtures
establish the specific checked invariants, not hardware power-loss, malicious-producer
authentication or external-provider proof. No schema, public transport, dependency,
runner/profile, DF-02 or DF-03 change is part of this correction.

## Fourth correction class verification

The G5 corpus distinguishes exact canonical inventory facts from reachable host paths.
Production exports retain nested canonical HTML, directory symlinks, Unicode names,
attachments and user versions directories. Rehashed Note/history/context/dependency,
reader and selected-source claims cannot introduce ancestor-alias or alternate-case
members absent from the inventory. Directory/ZIP and plain/separate-metadata cases
use the shared proof. Retained-source missing/directory/link cases require failure
under both included and unavailable labels, while canonical bytes, explicit/fallback
digests and reference-only controls preserve their original meanings.

The inventory proof also checks exact canonical JSONL encoding and actual typed
namespace equality. Format fixtures derived from production envelopes cover empty
roots, implicit ZIP parent headers, explicit wrong-kind roots/parents, self-consistent
but impossible file/link-plus-child ZIP sets, count-compensated roots and one/many
versions-layout membership. They establish format invariants, not empty-input producer
admission. Complete permitted Note history/forward roots, unresolved authored claims,
recovered publication and formatted-output identity remain consumers.

Bookkeeping is linear in entry/path facts and implied ZIP directories; no complete
file/archive body is buffered and no product count/size/depth cap is added. Directory
root-parent listings are cached rather than rescanning the entire envelope per version.
Context cancellation remains checked during row, namespace and parent processing.
Private run records preserve all red witnesses and later outcomes; independent review,
exact-final whole verification and complete campaigns remain Coordinator-owned.

## Stop ownership and generic job compatibility

The local job ledger supports unowned foundation fixtures independently of physical
copy execution. Confirming their cancellation retains the original generation,
revision and transactional outbox rules. Copy-specific stop handling requires an
explicit copy identity or a current reverse copy-set association. Malformed or empty
declared identities, missing declared sets and inconsistent bindings fail atomically;
absence of scope alone cannot excuse a reverse-owned copy.

For the current owned backup/export, confirmed cancellation records interrupted
progress while retaining capture barriers, checkpoints and pins. Repeating that stop
when already interrupted changes no job/set/progress revision or event. Old linked
jobs and complete, abandoned, expired or restored-paused sets cannot mutate current
copy ownership. Definite failure keeps its precise failed stage/error. These rules do
not grant execution authority or replace the dispatcher's owned-turn drain.

The paired SQLite regressions cover generic cancellation/reopen/retry, owned active
and terminal stages, malformed ownership, stale jobs/attempts, commit rollback,
checkpoint/pin preservation and exact no-op rows/events. The original durable
job/session revision regression remains unchanged. Complete SQLite package and direct
copy/drain consumer checks support the repair; independent review and the final
whole/campaign backstop remain separate Coordinator obligations.

## Review and acceptance

The assessed plan and addendum received independent review. The first result review
required correctness repairs despite a complete early gate passing. Correction
panels verified the metadata, ownership, recovery, durability and dispatcher classes;
the Epic Evaluator confirmed its terminal-replay and admitted-staging findings and
the Task's cross-Task scope. A later whole execution exposed a generic local-job
cancellation regression. The correction restored the existing ledger contract and
made repeated owned-copy stop confirmation idempotent; the required panel and Epic
Evaluator verified both findings and their class sweep without altering the original
regression test. That failed whole execution and all earlier rework verdicts and
failed attempts remain preserved. The Coordinator completed its project/product
review and accepted
the Task under Master's delegated authority; overall EPIC-005 acceptance is pending.

On the exact final candidate, the complete `make test KEEP_GOING=1` passed all five
serial stages, including persistence and every canonical E2E/F12 case. Frontend
validation with `.venv/bin/python -B frontend/check_build.py` and the profile's
exit-preserving desktop race/vet command also passed. These are complete execution
records, not a composite of earlier selected candidates. The
[verification profile](epic-harmony-verification-profile.md) owns the exact selection
and environment; private receipts retain candidate, inputs, commands and raw outcomes.

The complete Chrome backup and lifecycle drivers passed separately from the heavy
gate, exercising keyboard and 390×844 hasTouch actions, real completed/failed/
unavailable backup status, successful protected restore and health-failed rollback.
Browser/daemon cleanup completed. Touch is emulated; Task-level native transport
parity/build does not certify an interactive Wails recovery journey or a physical
device. No production-provider, full-VM rollback or hardware power-loss claim applies.

Next is TASK-005-06: repeat production Note/Review/new-version/reopen/recovery with
two real projects and browser/native clients, stale-write and lifecycle races,
and full original-directory export/reimport with portable metadata checked separately.
DF-02 remains conservatively deferred and DF-03 retains its existing read-drain behavior.
