# Durable provider attempts

TASK-006-01 extends the existing serialized coordinator and SQLite/object store
at schema revision 15. This is the attempt/reconciliation foundation. The
production provider capability remains false; TASK-006-03 owns its transport,
TASK-006-04 owns generation/publication, and TASK-006-05 owns saved Q&A.

## Delivered boundary

Provider intent, exact request identity, captured target/read set and input pins
are durable before external dispatch. A driver must use the one-use dispatch
fence after native startup and immediately before sending. Its successful writer
commit is the logical dispatch boundary. A cancellation or policy restriction
that wins before that boundary prevents the send. External waits run outside
writer transactions. The same dispatcher owns one waiting provider phase and
collects it through shutdown; import/seal and other local batches keep advancing.
Shutdown cancels and joins that phase before its handles are considered drained,
while possible remote effects remain explicitly unknown.

Attempt state and job state are separate. Native request/session identities and
actual input observations are retained when available, including interrupted
outcomes. The driver acknowledges observed progress through a synchronous durable
callback before waiting for completion. A provider turn/operation ID is the
request identity, and its thread is the session identity; local JSON-RPC
correlation remains separate. A source capture alone is not an input observation. Complete output is
stored as a verified immutable object before application; partial output cannot
complete the job. Explicit verified backend completion is distinct from stream
EOF. If completion is known but the result cannot be retained or accessed,
`failed` with `result_unavailable` means a local unavailable result; it does not
claim provider failure/cancellation. Without backend terminal proof the outcome
stays unknown. Application rechecks the current generation, runtime, owner,
configuration, project authority and target lifecycle. Obsolete output can
reconcile its own attempt but cannot mutate the library.

Cancellation is monotonic. A local exit or cancellation acknowledgement cannot
establish remote cessation. Unknown attempts retain their accounting and pins;
they pause instead of redispatching. Explicit reconcile-only work may perform a
supported status lookup but cannot submit again. Result retrieval is permitted
only with current payload authority. A hidden-target status-only lookup can
settle definite backend completion without reading a forbidden result body. A supported terminal
observation may settle cancellation. An explicit bounded retry preserves older
uncertain attempts and records its generation-specific authority. Cancellation
is never cleared. Deliberate new linked work uses the internal trusted
`AuthorizeProviderRetry` seam with the complete finite predecessor-attempt set;
ParentID alone is insufficient and unnamed uncertainty still blocks. Public
generation/question admission remains with its later owner. Normal restart
can reuse a complete saved result after an explicit continuation and current
authorization checks, without another external call.

The owner job read returns minimal operational status when its original provider
target is no longer readable. It omits scope, result and source references. Only
reconcile-only admission receives this exception; continue/retry and protected
payload delivery still require normal current authority. A scoped count/state summary exposes older unknown
attempts even when the current result completed, and participates in job ETags.
Explicit reconcile-only captures a finite durable set of those attempts; each
status lookup is claimed once per admission and collected by the dispatcher.
Terminal reconciliation preserves current job state, generation and result,
never reading a result body, applying stale output or dispatching again. Receipt replay applies
the same current projection. Trash fences matching provider work; purge waits
for unresolved attempts rather than discarding their ownership facts.

## Recovery and compatibility

Migration 14 to 15 uses the existing verified pre-migration recovery set and
atomic migration transaction. Missing or altered provider DDL refuses startup.
Coherent backups include attempt/input/result ledgers and generic object pins.
Scoped exports continue to exclude operational provider records. Execution input
pins release only when the job is terminal and all its external effects are
definite. Input/read/hash history survives collection; independent selected
snapshots and other owners are untouched, and durable result ownership remains. Result hashes, like input hashes, remain
historical metadata after an explicit owner release/transfer and collection;
they are not implicit object lifetime outside generic pins/references. Reuse
requires the complete object to remain available.
Unknown orphans pin only input/result objects found by a requested-object
availability check in freshly rebuilt, verified staging, independently of an
older snapshot's temporary attempt pins. This inspects only referenced objects
and does not retain a whole-backup inventory in memory.
An unowned ready object row may exist in the older database while its bytes were
never included in that backup. Such rows remain metadata-only for orphan
retention; readiness alone cannot create a new missing-object owner.

Before protected replacement, the destination's provider facts are retained in
the protected older database as orphan records, even when that snapshot lacks
the newer job/receipt. Matching fresh-ID submission cannot erase their uncertain
effect. Repeated restoration preserves newer definite observations or explicit
conflict evidence. Global recovery review does not clear those records. Truly
new owner-authorized work with a different captured request remains possible.
This is application-managed restore protection, not detection of a complete
machine rollback or proof that the provider performed an operation exactly once.

Backups retain the existing exact-schema/fingerprint compatibility policy. A
revision-14 backup is not silently relabelled as a revision-15 recovery set;
unsupported sets are refused before replacement. The current migration recovery
set and supported backup/restore procedure remain the recovery owners.

## Verification and limits

The deterministic `PA01`–`PA09`, `PA11`–`PA22`, `PA24`–`PA29` and `PA31`–`PA32`
regressions cover deterministic dispatch/cancel/configuration ordering, obsolete
results, explicit retry, input observations on success and interruption, normal
restart, saved output reuse, restore orphan facts, migration rollback/DDL,
owner HTTP/CLI resume, hidden-target receipt projection and lifecycle safety.
They use synthetic provider outcomes and isolated real storage. Additional
scenarios cover terminal input GC, unrelated ownership, protected older restore,
cross-job equivalence and complete explicit predecessor sets, acknowledged
acceptance followed by loss of Execute, output-persistence failure, status-only
completion and slow-provider/local-work/shutdown composition. Terminal jobs with an older
unknown attempt and restored orphan uncertainty both block target metadata
removal until external resolution; verified completion needs no forbidden body.
HTTP/actual CLI status covers older uncertainty, bounded terminal reconciliation
and consistent validators without disclosing native identities. PA32 uses the
actual backup/protected-restore path with an owned copied object, an unowned ready
row whose bytes were excluded, and a newer object absent from the old database.
It preserves independent ownership and every input/result hash, while adding
orphan pins only for verified copied bytes and keeping restored health usable.
Legacy RF06 and
SC10 migration comparisons retain every prior row and separately require the
known schema-15 tables to be empty; unexpected new tables remain detectable.

The separately opted-in `PA10` proof used one actual locally authenticated
subscription turn with supplied synthetic fixture text. The existing spike's
native guards were rechecked against installed Codex 0.164.0. The real accepted
turn stayed active while its current ledger closed/reopened, retained unknown
outcome and orphan facts, and accepted cancellation. Its matching complete
response was saved but could not apply. Exact supplied-input readback matched
the recorded input observation. Native cleanup reaped its owned process after
SIGTERM/SIGKILL; this does not claim a graceful provider shutdown.

That proof tests the current ledger's external boundary, not source-semantic
quality, production adapter support, generation or saved-question behavior.
The original live ledger retained a JSON-RPC correlation ID and native thread;
its native turn ID remained in the private receipt. The later `PA23` replay maps
that actual prior turn/thread observation through the corrected durable progress
port and interrupts the local ledger. It makes zero new native calls and claims
no new input delivery. Later deterministic corrections retain the original live
observation and its limits; no claim is made that the old native execution ran
subsequently changed bytes. A whole-source manifest was not frozen at PA10
start; final candidate identities and subsequent correction evidence stay
separate from that original observation.
Private original evidence retains the actual inputs, terminal observation and
database snapshots. No credentials, reasoning events or raw provider wire are
required in tracked evidence.

Actual input acknowledgements record `observed_at` as the first local durable
observation time in UTC, separately from source capture and provider acceptance.
Capture-only inputs have null origin/time in SQLite. Repeated acknowledgements
of the same origin preserve the first timestamp; a conflicting origin is rejected.
Drivers cannot preseed or backdate it. Restart and protected restore retain the
origin/time pair. Repeated orphan inventories compare valid RFC3339 timestamps
as instants, preserving the earliest supported observation and explicit category
conflicts. Timestamp-less historical evidence stays unknown; restore never
creates a date for it. PA33–PA35 and the extended PA18/PA20/PA32 cover these local
temporal and recovery boundaries. The prior native proof has no retrospective
observation timestamps and is not a fresh execution of this correction.

Common completion verification uses `make test KEEP_GOING=1`. The manual live
selection is `go tool ginkgo --race --no-color --tags=haeryong_provider_live
--focus PA10 ./internal/adapters/sqlite` with an explicit private physical
`HAERYONG_PROVIDER_LIVE_EVIDENCE` directory. The build tag excludes provider
calls from canonical layers. Re-execution consumes allowance and requires the
existing scoped workflow authority; startup-only proof cannot replace PA10.

## Recovery and interface corrections

Status-only orphan reconciliation uses the existing job coordinator. A trusted
owner admission must name a finite exact orphan set with the same principal,
semantic request and target; its carrier remains non-dispatchable even if the
selected effects settle before the lookup begins. Its typed links grant no retry.
Later public generation admission must integrate this internal seam with its
normal receipt and owner authorization; this Task adds no public provider
submission or orphan-ID API. Separate finite retry authorization supports an
absent or non-cancelled original orphan job without fabricating a parent row.
Older cancellation and uncertainty remain recorded.

Same-ID restored intent rows adopt actual retained dispatch uncertainty and
native identifiers. Repeated restore merges cancellation, terminal job facts,
identity conflicts and all known result hashes independently of outcome choice.
Only verified present result objects gain pins. A local `cancel_before_dispatch`
inference cannot settle a known possible call. An actual supported lookup may
settle matching ledger/orphan copies; conflicting external identities remain
unknown. Exclusive startup reclaims executing status intents, including those
on terminal jobs. Global recovery review and explicit job hold release precede
external lookup. `interrupted` advances through `reconciling` before pausing or
resolution; a normal-restart marker allows saved reuse without allowing results
superseded by same-daemon reassignment.

Identical or unrelated configuration commits do not revoke captured work.
Current project disclosure/permission generations still fence dispatch and
application. Maintenance budgets are separate from explicit owner generation
and questions; this foundation does not impose them on those jobs.
Source/provider-specific production policies retain their ordered implementing
owners. Durable missing/ineligible captured inputs pause with a
precise reason rather than repeatedly occupying the provider slot.

PA36–PA47 add isolated orphan-only protected restore and finite retry/status
admission, repeated restore/cancel/identifier preservation, conflicting result
ownership, between-save-and-apply cancellation/reassignment/authority barriers,
counted driver cancellation, configuration changes, missing inputs and nil
transport. They include actual SIGKILL/reopen of a claimed status lookup with
exclusive profile locking, terminal restore holds and dispatcher collection,
status-carrier settlement before lookup over repeated turns, normal HTTP request end/session
revocation and event replay/current protected projection, owner/nonowner cancel
and resume errors, and actual CLI lost-response journal recovery. PA03 isolates
old-result generation refusal while the newer job is running without cancel
intent; PA15 stores the actual late result before checking its ineligibility.
The root module excludes the desktop module, so current desktop race and vet
checks are separately required. The final whole gate remains mandatory.

Preparation's Python step checks syntax; its contract step checks selected
manifest digests. These do not execute Python behavioral contract tests, which
remain part of the complete gate. Historical live proof and its original command
remain unchanged, including their absence of a fail-on-empty/native-JSON report;
later deterministic reports do not retroactively add either property.

A status-only carrier completes with an operational reconciliation result only
after every admitted effect is settled and all its status intents are drained.
It produces no semantic answer or application. The orphan retains the actual
terminal resolver identity, principal/request/attempt/native-ID binding, outcome
and completion time separately from the immutable original job/state/cancel facts.
A carrier ID alone is not terminal proof. This evidence survives older restores
that lack both jobs and prevents re-pinning settled execution inputs. Unknown,
conflicting and partial status work retains its input pins. A still-live original
job keeps needed ownership until its own explicit terminal transition; independent
snapshot and result pins are unaffected. PA48 verifies physical object collection,
restart and repeated older inventory restoration with that proof, and PA49 covers
the same-ID live-owner boundary. These are local deterministic provider fixtures,
with no new native subscription turn.

The immutable `may_have_dispatched` ledger flag records entry into the logical
send boundary or conservatively retained possibility of dispatch. It proves
neither provider receipt/input delivery nor remote cessation. An explicit false
on a current local prepared failure cannot settle a same-ID dispatched/unknown
effect. Legacy inventory lacking this provenance stays unknown unless supported
correlated terminal evidence exists; its unverified reported terminal fact is
retained. Merge also considers the older database's existing ledger, not only
prior orphan rows. PA50 retains an actual local-failure red witness and checks
all four local error categories, both inventory orders and reverse-lineage
re-export, plus correlated backend-failure resolution.

Status carriers must be newly created and admitted with their exact finite set
inside the same writer transaction before the dispatcher can see them. Admission
rejects jobs with their own attempts, results, capture/read work, parent linkage
or retry authority. PA51 covers the generation-first semantic-work refusal,
existing results and linked retry, plus successful same-transaction admission.
PA62 separately isolates parent, capture/read and unconsumed generation-retry
guards, exact errors and rollback after partial finite admission. No production
admission caller is implemented by these transaction-composition tests.
Ordinary owner reconcile-only on paused or terminal jobs remains separate.

## Saved continuation and restore convergence

Explicit continuation queues a saved result instead of changing a paused job
directly to running. Its existing application generation reserves exactly the
next claim generation; the producing generation remains immutable. The queued
coordinator selects that binding before driver availability or new preparation.
It verifies the exact captured input objects and result through the existing
foundation Read port outside the writer. The verified result read remains alive
through the atomic claim/application callback, which rechecks generation,
current authority, captured identities and object metadata. Missing or corrupt
bytes pause this continuation; they never fall through to a new request even if
an earlier unconsumed retry authorization exists. Synthetic SQLite byte adapters
remain fixture cooperation, distinct from the real foundation verifier in PA58.

If the claim rolls back, the still-unconsumed next-generation binding can be
explicitly continued again after current checks. An explicit retry replaces that
pending binding with its finite generation-specific new-attempt authorization;
the last owner choice determines the path. Repeated retry before any committed
claim reuses one authorization row. Once a new claim advances the generation,
that old grant cannot authorize another generation. Ordinary startup carries only
currently eligible application/restart bindings, including output obtained later
by explicit reconciliation. A queued continuation survives ordinary reopen;
a later assignment, cancellation, revoked current authority or protected-restore
runtime cannot revive an old result. The waiting phase owns the observed job
generation until its own new-attempt claim actually commits. Old verified-read
callbacks cannot apply or pause a newer owner during failure collection.

Definitive explicit no-call outcomes also settle older raw prepared rows and
execution ownership. Missing legacy provenance, actual possible dispatch or any
conflict prevents that inference. Orphan-only inputs still require terminal
finite carrier proof and no live original continuation before release. Restore
conserves both incoming and prior conflict chains as a canonical set of complete
facts. Identical replay does not append duplicate facts indefinitely. Every
verified available branch input/result remains owned; unavailable bytes remain
metadata, and a supported observation of one branch cannot settle every conflict.
After global recovery review, explicit terminal reconcile-only can drain existing
already-settled intents/holds without any external operation or semantic rewrite.

PA52–PA69 add these deterministic compositions. PA52 and PA67 use actual Store
close/reopen; PA42 separately kills and reaps a real process. PA53 is retention
plus re-export; PA59 adds literal incoming orders and a same-ID terminal ledger
with a conflict orphan through real inventory export and older snapshots. PA54
starts with two unknown conflict chains. PA58 uses actual foundation files and
Schedule/collection for the read-to-writer ownership boundary. Its direct ClaimJob
assignment control, and PA67's claimed control, are synthetic typed-port proxies;
they do not establish a newer production-reachable bare claim. Public resume
accepts paused rather than queued work. PA61 establishes
cancel while fake Execute is blocked. PA60 drains a restored settled terminal
intent, PA63 drives actual WorkJobs/Dispatcher progress after an injected writer
rollback, PA64/PA65 check real HTTP/CLI errors, and PA66 collects no-call orphan
bytes while independent ownership survives. These are internal persistence and
synthetic-provider checks, not a production adapter or a new subscription turn.
PA69 kills and reaps a process inside an actual foundation verified Read, then
reacquires the profile lock and reopens. Only the dead provider_continuation
reader pin is reclaimed at exclusive startup. Attempt/orphan, independent
snapshot and result pins remain; execution inputs can collect only after their
legitimate job ownership ends. Live Read defers still release their own unique
pins normally; startup cleanup does not claim remote provider cessation.
PA04 covers identical-config dispatch and uncaptured-read lookup; PA05/PA24 are
compositions with an independent same-ID/ledger barrier. PA36 remains the isolated
orphan-only replay proof. Required final aggregate and independent assessment
retain their separate candidate and execution identities.

PA70–PA83 add bounded correction controls. Actual Poll and Dispatcher preserve
successful reconciling normalization and progress between finite lookups; retry
is refused while that work remains reconciling. Cancel after actual Store
close/reopen normalizes inside the existing transaction. Post-commit assertions
use ordered bodyless event sequence/revisions, not reconstructed state payloads.
A one-shot normalization commit failure leaves the original interrupted row
unchanged. Orphan-only saved output with independently owned ready bytes refuses
continuation permanently; it never gains semantic application authority.

Local continuation errors distinguish missing/corrupt captured inputs, unavailable
saved result bytes, changed authority and application rollback. PA75 uses the
real foundation verifier for successful exact-byte claim/application and normal
reader-pin release; PA76 stops the context during that real Read and verifies
unchanged queued ownership followed by successful reuse. PA79 captures an actual
before-send prepared backup, records explicit no-call continuation, restores the
older prepared snapshot, then uses normal owner cancellation to release both
execution pin kinds and physically collect input bytes. Independent snapshot,
result and unknown ownership survive. PA78 supplies only one plain terminal
inventory record to an existing conflict and checks retained result pins and
bounded identical replay; no second exported orphan record masks the assertion.
PA80–PA82 exercise actual HTTP/CLI cancellation/refusal and receipt/journal
behavior with bounded typed fixture setup. PA83 establishes the deduplicated
orphan-only condition through real foundation backup/protected restore, then
explicitly releases the restore job hold before the permanent typed refusal.
These remain internal persistence, public-request and synthetic-provider
cooperation checks. They add no production subscription execution, and Store
close/reopen is distinct from the separately labelled process-kill controls.

The separate `haeryong_provider_replay` build tag selects `PA23` using an
explicit retained `HAERYONG_PROVIDER_REPLAY_RECEIPT` and private absolute
`HAERYONG_PROVIDER_REPLAY_EVIDENCE` output path. It never opens a native client.

The implementation and test candidate passed `make test KEEP_GOING=1` and the
focused checks described above. Task completion binds the selected original
independent technical review to its exact, directly verified corrections and the
final independent assessment. The original review judgments and qualified live
boundary retain their stated provenance and limits. Subsequent completion
metadata is covered by the documentation integrity check.

TASK-006-01 is complete. EPIC-006 remains in progress; TASK-006-02 is the next
implementing owner. The production provider, generation and saved-question
capabilities retain their ordered Task owners.
