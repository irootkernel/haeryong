# Guarded manual purge and content GC

**Task:** [TASK-005-04](../tasks/EPIC-005/TASK-005-04.md), EPIC-005 — status `done`
**Implementation:** delivered: guarded manual purge, terminal lifecycle intent and job, permanent version-ID reservation and pin-safe resumable GC
**Source revision:** HEAD `db2da1a8` plus the TASK-005-04 change set; the completion suite ran on the frozen final candidate equal to the committed tree; exact run bindings stay in private receipts
**Environment:** macOS arm64, prepared Go 1.27.1/Python 3.14.7/Node 26.7.0 and installed Chrome/Playwright CLI; synthetic disposable libraries; no installs, provider calls, ingress or source-repository changes
**Evidence status:** pass for the executed checks below, on the candidates stated; earlier failed attempts stay recorded as failures; no native Wails UI, physical-device or production backup-engine evidence is claimed
**Implementer / independent reviewer:** Executor Writer; Task Evaluator, two non-authoring Executor reviewers and the Epic Evaluator; final project/product review and acceptance by the Coordinator

## Delivered behavior

Owner-only purge extends lifecycle plan/create/get/commit with exact natural plan
identity, optional aliases and a stable 202 job-acceptance receipt. The transaction
makes the document terminal, clears its title/latest pointer, reserves all old
version IDs permanently and commits the job/intent/fences/outbox. Restore wins
first with no deletion, or loses clearly after terminal purge. Purge cancellation
is explicitly unsupported after acceptance.

The serial dispatcher executes bounded derived/personal/source/version cleanup and
GC. Every saved annotation revision's body/context references and lifetime pins
are counted, including deleted/reattached notes. Incoming historical edges survive;
outgoing rows and FTS children are removed before their parents. Live/shared
versions, resumable uploads, backups, drafts and active source/read/job/staging
owners retain their bytes. Real deletion uses the foundation reservation/token and
unlink/directory-sync protocol; source roots are never filesystem targets.

Recoverable I/O failure leaves paused io_retry, protected candidates leave paused
protected, and changed original guards leave paused guard_blocked. The same job
continues automatically with persisted backoff/checkpoints and fresh generation
checks. It completes only after metadata and candidates are settled; a retained
shared version or crash-leaked pin can keep it paused indefinitely. Terminal jobs
and the original 202 receipt are never restarted/reinterpreted as new work.

Private target copies in old receipts/plans, including saved preview page paths
and note identities, are minimized while original
fingerprints, identity, state and nonsecret comparison facts remain. Equivalent
redacted requests cannot dispatch and changed semantic identity still conflicts.
Purge is not forensic erasure of independent copies, WAL/free pages, FTS or hardware
history. The confirmation's backup status is honestly unavailable; a new backup
is not required. DF-02 remains with TASK-005-05; DF-03 read-drain behavior is unchanged.

## Verification mapping and bounds

| Requirement | Executable owner and scope |
| --- | --- |
| T-005-04-01; Obligation 5 | query/purge_test.go real parallel plan commits; import/lifecycle_race_test.go real accepted publication/derived-work barriers across actual trash→purge; public test_purge.py two-owner race and original receipt replay |
| T-005-04-02; X10 initial | query/purge_correction_test.go derives membership from a consistent SQLite snapshot under the writer gate, pins that object set, copies and byte/hash-verifies every member, and writes a snapshot-bound completion marker only after all copies; query/purge_test.go holds an interrupted copy across real purge turns and asserts its marker is absent; public shared referrer asset byte comparison. The fixture is not the future production backup engine |
| T-005-04-03 | Public original directory byte inventory, minimal D4 reference/410 and hidden histories/search; persistence empty document title, removed private version/note/history rows and minimized response copies |
| X14 initial; T2 ownership | Real Collect/adoption protocol with backup/read/active_read/draft/job/staging/source/evidence/upload-read pins, historical context/body ownership and verified copy; pin release permits later collection |
| PR1-F1 | Persistence injected collector error and generation/terminal checks; public live-daemon filesystem I/O failure, repair, nonterminal daemon restart while the sealed upload still protects candidates, explicit upload release, completion of the same job/original 202, and immutable completed job after another restart |
| PR1-F2 | Public same-ID import after purge and restart; persistence actual import rejection plus permanent inventory. A real staged import is held while a competing document publishes and purges its ID; after the version row is removed the held publication is rejected. Acceptance and final publication both check the tombstone reservation |
| T3 exact authority/freshness | Current owner/plan/alias validation; purge reader-row freshness regression; original 202 identity/replay and terminal fence; private receipt minimization preserves uncertainty |
| Obligation 9 | Authentic 12→13 rollback/recovery object bytes/startup refusal; existing declaration chain and supported old-schema fixtures; selected closed schema/fixture/digest/subset checks; browser/native/Go facade parity and compiled assets |
| C09 contribution | Actual archive/trash/restore predecessor regression followed by public purge, shared/source preservation and incoming tombstones |
| M13 contribution | Explicit manual action only, restore race and typed backup-pin protection. Timed purge and old-backup policy suspension remain TASK-009-05/TASK-005-05 |

The changed owner flow passed the existing bounded Chrome lifecycle driver:
keyboard/mouse plus 390×844 hasTouch taps, safe initial Enter/cancel, explicit purge
confirmation, truthful backup line, target sizing, renderer retirement and progress.
This is emulated touch; no physical device or native Wails UI evidence is claimed.

## Checks and earlier failures

Preliminary focused race/persistence checks passed on their recorded inputs; initial
new-test failures were an invalid whole-version anchor carrying page query/fragment,
a loop that called the intentionally immutable completed job again, and a new CLI
assertion that expected the HTTP code instead of the documented common CONFLICT
wrapper (the underlying immutable conflict remains separately asserted over HTTP). The inputs
and loop were repaired without weakening product assertions. Integration then
exposed an existing nullable `type` array in lifecycle_result that the selected
fixture validator does not implement; the same string/null values now use the
existing anyOf convention without changing or relaxing the validator. The queue
selection also excludes waits not yet due, preventing a pending prefix from
starving later cleanup. Exact private receipts
bind each run to its actual candidate; preliminary passes are not final-suite proof.

The first complete persistence and E2E stages passed on their recorded candidate;
that candidate still had four vet warnings from unkeyed literals after sharing the
lifecycle-conflict type. The fields are now explicitly named with the same values.
A read-only inspection also found saved preview metadata that still needed
minimization; it is now scrubbed without changing commit results, and a regression
asserts stable plan/status/fingerprint with no saved note/path. A later browser
run exposed an immediate cross-tab DOM assertion racing asynchronous event
delivery; it now waits for the same required renderer/host retirement used by the
existing lifecycle cases, without a retry or a changed timeout.

The public purge selection passed its three cases, including the live-daemon I/O
repair/upload-release/restart path. Focused results are not relabelled as
final-candidate proof; the completion suite on the final candidate is recorded under
Review and acceptance.

Policy F: old-schema fixtures drop the new dependent tables and exclude them from
prior-row byte comparisons, tracing to the approved append-only migration/recovery
requirement. Existing negative checks and authoritative prior-row comparisons remain.
New schema tables are separately checked at current revision. The existing Chrome
Trash text assertion follows S08 and the reviewed purge activation: it now expects
the enabled manual action instead of the former unavailable message. No assertion weakening,
skip, retry allowance or shortened timeout is introduced.

## Handoff

TASK-005-05 must preserve tombstone reservations, terminal intent/checkpoints,
candidates, receipt aliases/redaction facts and fences in coherent copies, wire real
backup status and decide DF-02. A changed restored runtime blocks prior cleanup
until that Task's explicit owner reconciliation. Timed purge remains excluded.

## RR1 correction coverage (C1)

| Finding | Repair and executable proof |
| --- | --- |
| F1 exact reader ownership | purge.go matches the impact owner/version boundary. query/purge_correction_test.go preserves a real other-document position at notes/<target-uuid>.html and checks the target-only impact count |
| F2 actual content-set owners | Abandoned upload state is excluded, terminal target metadata indexes are removed first, active import/index/version owners remain protective, and retained_sets checkpoints are revisited. Query persistence covers abandon-before-purge with/without supplied metadata and payload collection; public E2E covers supplied metadata and completed cleanup. Existing release-after-purge coverage remains |
| F3 verification excerpts | Exact verification references enter candidates before release/deletion. source/purge_correction_test.go performs two real referenced-excerpt verifications, abandons their uploads, purges, collects the exclusive excerpt and preserves another retained version's shared excerpt plus an independent unknown pin |
| F4 observation settlement fence | Intermediate target/run persistence checks lifecycle and open intent; purge waits for open check intents. The real Git observation is held at reservation resolution and after intermediate capture, then actual purge is accepted, cleanup waits, observation settles and no run is recreated. Existing non-purge dangling-capture semantics remain |
| F5 lost acceptance/reload | Trash-independent journal enumeration reconciles original receipts. Job identity is stored under the shared job lock before the exact journal is cleared. Unit checks cover owner/library isolation and storage failure; Chrome loses the accepted 202, reloads, reads the original receipt and links the same plan/job with one acceptance POST |
| F6 pending restart | Persistence reopens a paused intent/checkpoint and refuses the prior worker generation. Public E2E repairs the I/O condition, restarts while the upload still protects candidates, then releases that upload and observes the same job complete, unchanged original 202 and one plan-bound decision |
| F7 X10 copy | Snapshot membership, transitive object copies and hash/length verification use a bounded test-only foundation fixture. An interrupted copy has no completion marker; the complete copy binds every object and the snapshot digest. This is not production backup-engine or protected-restore proof |

C1 retains all failures in the Writer run record. Its first frontend attempt failed
on an unused test argument; the argument now asserts the original operation ID.
The first nonterminal restart fixture attempted daemon startup with an intentionally
unwritable object directory, which correctly refused startup. The fixture now repairs
startup-required permissions while the sealed upload still guarantees a nonterminal
purge, then restarts before releasing that owner. No harness or assertion was relaxed.

Policy F sources for strengthened copy/restart/browser scenarios and the optional
supplied-metadata fixture are the assessed RR1 findings and Coordinator C1 assignment,
implementing the approved r2 obligations. Existing default fixture behavior is
unchanged. No skip, retry allowance or existing timeout change is introduced.

## Coordinator correction coverage (C2)

COORD-T4-1 adds explicit dismissal of observed terminal results and individually
unavailable job records. Pending/paused work cannot be dismissed; transient lookup
failures retain their records and keep polling. Each lookup settles independently,
so a 404/denial cannot hide a live job or stop its polling. Dismissal merges against
the latest owner/library bucket under the same shared lock used for remembering a
job. The observed dispatch authority is checked before changing storage. The
lost-202 journal recovery and job-before-journal-clear order are preserved.

The existing registered lifecycle client unit file verifies terminal dismissal,
owner/library isolation, preservation of newly remembered jobs, failed lookups
beside live work, transient retries, and pending/paused persistence across reloads.
The Chrome purge driver retains its lost-response and real hasTouch assertions;
it seeds one no-longer-existing remembered ID, observes a real 404 alongside the
real nonterminal purge, waits for another live poll, taps dismissal, then reloads
to verify that only the unavailable ID is removed. Terminal dismissal is unit
proof; no production completed-job fixture is fabricated for the browser.

Policy F: the added browser expectations trace to the Coordinator finding and
C2 assignment, agreed by the TE and EE. Existing assertions are preserved; no skip,
retry allowance or existing timeout is changed. No public contract changes.

## Review and acceptance

The Task Evaluator, the two non-authoring Executor reviewers and the Epic Evaluator
passed plan r2, after r1 gained a concrete recovery path for interrupted cleanup and a
permanent reservation of purged version IDs. The first result review returned rework
for an inexact reader-resource boundary, an abandoned upload that blocked collection,
unreleased verification excerpts, an unfenced source observation, lost-acceptance
recovery after reload and missing restart and snapshot-copy proof; C1 corrected all
seven. After a reassessment bound to the corrected candidate, the Task Evaluator, both
reviewers and the Epic Evaluator passed the full Task. The Coordinator's final review
found that the purge progress panel never pruned remembered jobs and that one failed
lookup blanked it; C2 corrected this, and the Task Evaluator, both reviewers and the
Epic Evaluator passed the result. The Coordinator completed the final project/product
review and accepted the Task under Master's delegated authority; overall EPIC-005
acceptance remains pending.

On the final candidate the whole completion suite passed: the canonical `make test`,
every stage through the no-fail-fast execution, frontend build equality and unit tests,
and the desktop race and vet run.
