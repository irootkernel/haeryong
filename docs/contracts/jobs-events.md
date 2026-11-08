# Durable jobs, events and findings

The [TASK-006-01 foundation](../testing/provider-attempts.md) implements the
provider attempt ledger, one-use dispatch fence, cancellation/reconciliation and
saved-result application guards on the existing coordinator. It introduces no
production provider transport or generation/question submission endpoint.

**Proposal, documentation baseline 1.6.0:** one daemon-owned durable coordinator for generation, questions, scans, upload sealing, streamed directory import and local lifecycle/backup/export work. It is not a general-purpose orchestration platform. Shared enum values and resource fields are in the [wire registry](schema-registry.md).

EPIC-002 owns the generic local job ledger, operation receipts, resource revisions, pins and transactional outbox. EPIC-005 can use those foundations for lifecycle/copy work. EPIC-006 adds provider attempts, external ambiguity, cancellation and their integration; it must not retrofit missing receipts/outbox into earlier mutations.

## Job states and transitions

| From | Event / guard | To |
| --- | --- | --- |
| queued | Captured inputs/authority valid, no cancel intent and dispatch slot available | running |
| queued | Cancel before any external dispatch | cancelled |
| queued | Required input/validation definitively fails | failed |
| running | All stages have explicit results/coverage and no unresolved active application | completed |
| running | Definite unrecoverable failure | failed |
| running | Policy/budget/input pause without pretending completion | paused |
| running or paused | Accepted cancel intent while a local worker or external execution may still finalize | cancelling |
| paused | Explicit safe resume; cancel_requested_at is null, no unresolved attempt is implicitly redispatched, current restrictions checked | queued |
| paused | Cancel with no dispatched/unknown external execution | cancelled |
| paused | Explicit reconcile_only resume; retain any cancel intent and do not dispatch | reconciling |
| paused | Definite failure established by evidence | failed |
| cancelling | Stop/no-dispatch confirmed, or completed output retained but application suppressed after cancel | cancelled |
| cancelling | External outcome cannot be established | paused, with cancel_requested and outcome_unknown visible |
| interrupted | Begin durable attempt/input reconciliation | reconciling |
| reconciling | Complete saved valid result, current authority valid, no cancel intent and no stages remain | completed |
| reconciling | Complete saved valid result, current authority valid, no cancel intent and local stages remain | running |
| reconciling | Outcome resolved, no cancel intent and retry explicitly authorized | queued |
| reconciling | Outcome remains unknown or stricter policy prevents safe progress | paused |
| reconciling | Definite failure | failed |
| reconciling | Cancel requested with external execution potentially active | cancelling |
| reconciling | Cancel and no external execution remains unresolved | cancelled |
| running, paused, cancelling or reconciling | Crash/lost worker requires state reconstruction | interrupted |

A never-dispatched queued item may remain queued after restart once recovery verifies that fact. Terminal jobs are not rewritten as new work; retry of failed/cancelled jobs creates a new linked job. A completed scan may have incomplete semantic coverage if every exclusion/result is recorded; completed never means every inventory item received AI analysis. An import/export is different: completed requires every selected directory entry and byte to be captured/copied and verified. It cannot use semantic exclusions to declare a silently truncated directory complete.

Cancellation is monotonic intent. Record cancel_requested_at and advance the job's application/attempt generation before requesting external cancellation. If result application committed first, cancellation reports already completed; it does not undo that mutation. If cancellation committed first, later output may be preserved for diagnosis but cannot apply. A timeout or local child-process exit does not prove the remote provider stopped or consumed no allowance. Once cancel_requested_at is set, resume/reconciliation cannot clear it or return this job to queued/running/result application. Reconciliation may retain observations and resolve the job to cancelled/failed when supported by evidence; an unknown remote outcome remains visible. Deliberately new work uses a new linked job with its own authorization.

POST job resume takes {decision: reconcile_only|continue|retry, expected_attempt_generation} and the current job ETag. reconcile_only performs supported status/saved-output reconciliation without a new provider dispatch. continue requires no cancel intent, no unresolved provider effect and currently valid captured inputs/restrictions. retry must match the exact expected attempt generation and explicit or already configured bounded retry authority; retain the older uncertain attempt and possible duplicate allowance use. Stopping future job work uses the explicit cancel route, including job.kind upload_seal. Upload abandonment uses its separate upload route. The [coupled seal/upload transition table](import-workflow.md#cancelling-sealing-versus-abandoning-an-upload) is normative: cancellation retains staged bytes, returns a non-abandoned upload to open only after worker stop, and later verification uses a new job; abandonment fences reuse and releases only safe references. A winning seal commit is not undone by late cancel. Neither action invents proof that local or remote execution stopped or makes cancellation itself a failed outcome. Cancellation wins over every resume decision; unsupported transitions fail INVALID_TRANSITION.

## Captured job and input fields

Capture job_id, registry job.kind, principal/operation identity, permitted project/source scope, document/version/page inventory, requested source selection, exact target_set_id/targets, config_revision and relevant per-project permissions_revision, action modes, remembered disclosure configuration, optional applicable deadline, checkpoint, attempt_generation and item-level structural/semantic status/exclusion reasons. Semantic work persists the registry's fixed coverage_set_id, exact required unit tuples and each unit's outcome/reason/assessment linkage; totals are derived, never a replacement for those records. job_item.state uses the shared enum; stage is a stable named phase documented for its job kind/capability rather than an implicit second state enum. Questions/findings include page_path whenever region_id is present, plus artifact query/fragment and a captured context_ref when used.

Directory transfers capture upload/content-set identity, exact selected paths, accepted file/range checkpoints, optional known totals and per-entry status. Asynchronous upload completion uses job.kind upload_seal and completes only after the entire selected input is sealed; a seal job never publishes a version. A completed 202 response from POST /imports references job.kind import, whose completion requires atomic publication. Distinct completion keys at the same active seal checkpoint each commit their own receipt referencing the same seal job, without a duplicate worker. Incremental staging and inventory hashing avoid an all-directory RAM buffer. Counts/bytes remain unknown until enumerated rather than pretending a partial denominator is complete. Interrupted local transfers resume confirmed bytes under the same upload, without a new provider attempt. A changed source selection or finalized content identity is a distinct request, not an unnoticed continuation.

No HTML-byte, aggregate byte, file/page-count, depth or output cap is imposed. Backpressure, finite buffers and per-operation idle timeouts preserve progress and recoverability. A progressing directory transfer has no fixed total-duration ceiling inherited from maintenance budgets. Actual disk/memory/I/O failure produces a precise incomplete state with useful staging retained where possible, never a partial published version. User-configured provider/maintenance cost, concurrency and retry budgets remain independent.

Resolve the selected current commit or working-copy capture once while accepting work, before the job becomes ready for dispatch, and persist captured_at with the exact requested_selection. A queued question cannot silently adopt a later branch tip. Each daemon capture creates read_id bound to target_set_id, source identity/location, exact revision or snapshot, bytes_sha256, config_revision/permissions_revision and retained-object/pin reference where permitted. The read ledger distinguishes project source from explanation context. Provider attempts record input_observations {read_id, input_origin, observed_at} only for bytes actually supplied by the adapter or supported exact-byte native telemetry. Capturing a source-view response or recording HEAD is not by itself a provider-input observation.

Completed answer/assessment project-source citations use basis observed_input with that attempt's matching read_id/input_origin, or basis posthoc_verified with a separate verification_id and read_id/input_origin null. A post-hoc target check records its actual time/bytes and cannot backdate provider input or rewrite published generation provenance. Unobserved native reads stay unobserved. Both evidence variants still need semantic claim support and exact-scope coverage before a source-backed conclusion is applied. package_evidence_ref is optional only for an immutable source/location/bytes match. Merely having a digest does not make reference-only bytes available. Explicitly conceptual/illustrative commentary may identify explanation context, but missing requested pinned/current source remains unavailable/unable_to_verify; an empty target set or explanation text is not a successful source check. Raw result files with unverified evidence remain importable. Explanation-only references use the separate registry explanation_reference shape, not a fabricated project-source citation or verified source target.

Acquire input/result pins before their authorized retained or execution-temporary objects can be needed by queued/running/reconciling work. A reference-only read does not automatically become a permanent source snapshot; if unretained bytes are later unavailable, pause/fail rather than manufacture them. Stage publication and coherent backup/export copies use the same pin accounting. Ordinary retention and GC cannot remove active pinned bytes or unknown-outcome facts. Explicit source-redaction/cancellation may make further application impossible with an audited impact decision; it is not diagnostic cleanup.

## Ownership and fencing

The OS-backed profile lock permits one mutation-owning daemon. A distributed lease-renewal protocol is not required for this baseline. Keep a monotonic durable per-job attempt_generation and active attempt identity: advance it on dispatch assignment/retry, reassignment and cancellation as applicable.

Only the currently active attempt generation, allowed job state and captured authority may apply a result. A startup instance epoch alone is insufficient because a cancelled attempt may report after another attempt starts within the same daemon lifetime. Old-attempt observations may update that attempt's reconciliation record, never current library state.

All provider/user waits occur outside short write transactions. Reclaiming a local ownership record does not prove external execution stopped. Equivalent active scan identity includes library, authorized scope, captured target selection/set, config/action policy and depth. Coalesce only equivalent requests and return 202 with the existing job_id and coalesced true; a new operation receipt points to that existing job rather than dispatching another run. Do not silently widen scope or merge different target sets. An accepted receipt is only a durable reservation awaiting its definitive response. Job creation atomically commits the receipt as completed with original_status 202, operation_id and job_id; the referenced job may still be running/failed/cancelled. A concurrent identical pending request receives the registry's stable pending response, without another job or external dispatch. GET operation and GET job keep reservation, response commit and job outcome distinct. Import 201 versus durable 202 is a response-timing choice, never a content admission threshold; clients handle either and expose publication only after the final atomic commit.

## Provider attempt protocol

| Attempt state | Meaning / permitted next observations |
| --- | --- |
| prepared | Intent persisted; no dispatch has begun. May dispatch, fail locally or cancel safely. |
| dispatching | Boundary may have been crossed. May become running/result_saved/failed/outcome_unknown. |
| running | Provider acceptance/progress observed. May become result_saved, definitely failed/cancelled, or outcome_unknown. |
| result_saved | Complete usable output durably stored with digest; application is a separate guarded transaction. |
| failed | Definite failure observed; any new request gets a new attempt ID/generation. |
| cancelled | No dispatch or supported provider cancellation outcome is established; local transport termination alone is not sufficient proof. |
| outcome_unknown | Dispatch may have occurred without a definitive result. Only actual saved/provider evidence can resolve it to result_saved/failed/cancelled. |

1. Persist attempt intent, generation, request fingerprint and approved input set before dispatch.
2. Record dispatch observation and provider request/session IDs only when actually supplied. If transport uses subprocesses, own its process group, streamed output files, cancellation/termination and reaping; record local transport status separately from provider outcome. Output buffering has backpressure/checkpoints, not an arbitrary total-result cap or silent prefix truncation. Actual provider output/context limits and resource failures are recorded explicitly.
3. Persist complete output and digest before validation/application. Partial streamed text is labelled partial and cannot become a completed answer merely because the stream ended.
4. After ambiguous interruption, reconcile complete saved output or a genuinely supported provider status/output operation. Unsupported lookup/resume features remain unavailable; a session ID is not proof of exactly-once execution.
5. If outcome remains unknown, pause. Only explicit retry authorization or an already explicit bounded retry policy within the original scope permits a new attempt. Record possible duplicate allowance consumption and preserve the old attempt. No silent billing/account/provider fallback.
6. Apply validated output only after current permissions, lifecycle, source target, config restrictions, user decisions, resource revisions and active generation all pass. Receipt and outbox commit with the local effect.

Current stricter configured rules apply before every new submission and local application; later scope expansion does not widen the captured job. Reuse the owner's project/source/provider choices without per-read approval. A configured bounded retry policy avoids repeated prompts while preserving explicit unknown-outcome accounting. Resume can reuse completed deterministic items without reissuing their provider requests. No private model reasoning or raw credentials are stored in user reports.

## SSE and invalidation contract

GET /api/v1/events emits bounded notifications containing library_id, stream_epoch, sequence, resource type/ID when authorized, event type and applicable revision. stream_epoch is an opaque UUID-form string. Event IDs encode `stream_epoch:sequence`; sequence is a monotonically increasing integer within the epoch, serialized as a decimal string. Epoch is durable across ordinary restart and replaced after restore/history reset. Clients compare epochs for equality and sequence numerically, not by lexicographic string order.

| Event family | Covered changes |
| --- | --- |
| job.updated, job.progress | Confirmed job state and compact progress: current stage, accepted bytes/files, known/unknown totals and resumable checkpoint identity |
| document.published, document.lifecycle_changed | Publication, archive/trash/restore/purge |
| review.changed, revisit.changed, reading_position.changed, page_position.changed, reader_preference.changed, annotation.changed | Owner records, last-opened pointers, independent page positions and anchors |
| question.changed, upload.updated, project.updated | Saved question/answer or partial/error changes, resumable upload state/checkpoints, registered project/settings views |
| finding.created, finding.updated, overlay.changed, topic_assignment.changed | Proposals, decisions, apply/undo and pin/reject |
| draft.changed, assessment.published | Draft creation/status and applied freshness history |
| source.changed, source.availability_changed, source.scope_changed | Captured change observations or current source/read/disclosure eligibility |
| policy.changed | Current validated configuration revision |
| resource.invalidated, library.invalidated | Invalidate cached/rendered state without a newly forbidden private body |
| session.revoked | Revocation control; protected delivery ends immediately |

Persist the outbox event in the same DB transaction as the corresponding mutation. Deliver ID/revision notifications, then let clients fetch authorized state. Delivery can repeat; deduplicate by epoch/sequence and re-fetch on resource revision gaps. Notification order never overrides write preconditions. Each visible mutable resource has a documented refetch trigger: use its family above or resource.invalidated with that authorized identity/revision; a dedicated event name or endpoint for every widget is not required. Question changes invalidate completed-answer/partial/error composition; job updates do not fabricate a new answer. Pure transient context transfer may use its control acknowledgements and authenticated status polling. Domain publication and its job update are committed/ordered through the outbox so clients can fetch the published version after document.published.

Normal resource events are filtered using current session/scopes/lifecycle before each delivery or replay. Restrictive changes still require invalidation of already open reader/source/answer contexts. Use a content-free library invalidation for currently authenticated relevant sessions when a target-specific notification would disclose now-hidden metadata; or a minimal resource invalidation only where that identity remains authorized. Revocation terminates delivery and clears protected views through the auth contract. Do not suppress all invalidation merely because the resource's normal read is now denied.

### Replay and resync

Last-Event-ID may resume retained history only for the current library/epoch. An expired, malformed, future-sequence, foreign-library or old-epoch cursor produces a resync.required control message with safe reason, current epoch/head and a resume cursor; do not fabricate missing events or wait for a restored counter to catch up.

On resync, obtain the current head cursor, fetch fresh authorized lists/jobs/visible resources, then replay notifications since that head to cover concurrent changes. Apply resource revisions and repeat resync if that history also expired. The control message is not a historical domain event. An auth failure clears protected state instead of starting unauthorized replay.

Local and remote browser streams both use bearer-authenticated fetch with Authorization plus SSE parsing, or bounded authenticated polling. Remembered origin-scoped credentials survive reload/new tab/browser restart; optional memory-only sessions do not. Management-cookie authentication is not used. Polling reads the same confirmed state and never re-POSTs a job to recover progress. No secret appears in the event URL. Proposed heartbeat is 20 seconds and reconnect backoff 1..30 seconds; choose bounded stream writes/backpressure and retain normal request deadlines rather than applying an unlimited global server timeout.

## Findings and drafts

A finding contains finding_id, run/job ID, kind/origin, originating version/page/region, exact target_set_id, evidence, rationale, uncertainty, captured config_revision, proposal, status, approval_required, actionable reason and individual revision/etag.

Statuses are proposed/applied/rejected/stale/blocked/undone. Fresh proposals awaiting owner permission use proposed with approval_required: true, never an undefined needs_approval state. Rejection records evidence fingerprint/scope, so the same suggestion is suppressed until materially new evidence. Applied overlays stay origin-labelled and undo checks the current overlay revision.

Supported transitions are proposed -> applied/rejected/stale/blocked, blocked -> proposed only after explicit revalidation under current authority, and applied -> undone through a valid undo. A rejected/stale/undone finding remains historical; a materially new assessment creates a new linked finding. New content proposals authorize neither provider usage nor publication by themselves. Finding approve/reject applies only the authorized reversible overlay/decision. For a new-version/new-document proposal, explicit POST generations with finding_id, expected_finding_revision and intent draft creates the linked preparing draft/job under current budget/scope; publication stays separate. POST scans may name the same finding origin for a fresh reassessment. Record applied_ref to the actual overlay/draft/job when its action succeeds; a proposal alone is not applied. Retry/reassessment keeps the original finding historical and creates a new linked result. Only a ready draft with current base and publication authority can become an immutable version.

## Scan accounting and retention

Report captured document/version/page totals; structural checked/repaired/failed counts; semantic assessed/skipped/unresolved counts; inspected candidate relations; applied/proposed/rejected overlays; drafts; outdated version/page/region units with their coverage kind; affected reviewed versions; actual external attempt count and available usage observations. The fixed required-unit set and every unit outcome/reason remain inspectable with the contributing assessment_id and target_set_id; denominators/counts derive from those records. A required exclusion cannot be dropped from the denominator or treated as assessed. Changing the inventory/target set requires a new run.

File/page listings and issue details can be cursor/stream resources instead of oversized event payloads. Their batch sizes do not limit total result inventory or saved output. Saved notes, questions and provider inputs/results adopt referenced large context objects with appropriate lifetime pins before the transient renderer/upload is released.

Event-history retention may expire replay cursors. Completed job detail can be trimmed only while preserving published answers/assessment provenance, owner decisions, minimal operation receipts, unknown attempts and necessary recovery facts. Backup/export and source input pins have their own completion/release rules; a generic diagnostic timer cannot silently release them.

## Source check events (TASK-004-01)

Additive delivery uses the existing transactional outbox, not a new job kind.
Project registration/configuration emits **project.updated**, the implemented
project family; this replaces the earlier provisional spelling project.changed.
Source event identities and stored outbox bodies are:

| Event | Resource kind / ID | Stored body | FilterEvent visibility |
| --- | --- | --- | --- |
| source.scope_changed | source / project_id | nil (no version body) | Owner: project must remain visible; scoped principals retain source-event masking |
| source.changed | source / source_id | {version_id,run_id,classification} for direct/indirect changes | Owner: body version must remain visible; scoped principals retain source-event masking |
| source.availability_changed | source / source_id | {version_id,run_id,availability} on an availability flip | Same version-bound mask |

The same committed source-changing project plan emits project.updated with
resource project / project_id and nil body. The public SSE/poll projection
contains the authorized type/resource/revision notification, not these stored
bodies; clients refetch current state. A source.scope_changed project ID is not
interpreted as a version ID. Missing project/version targets are masked as
library.invalidated with no resource identity/body.
Session/scope/lifecycle changes are applied before replay and live delivery;
hidden version/source data is masked under the common invalidation rules.
Events neither start AI nor publish semantic assessments. Operation receipt
polling recovers a 202/uncertain check; no duplicate observation is dispatched.


## Additive derived-work foundation (TASK-004-02 M1/M2)

The selected closed job.kind registry adds `derived_index`; Go CreateJob admits it.
Migration 8 retains `(version_id,work_kind,extractor_revision)` uniqueness and a
path checkpoint, with current work_kind `search`. This intermediate foundation
has no enqueue/dispatcher/daemon wiring yet; existing import work selection stays
unchanged. Later milestones wire the family together before delivered release.
No new job-resume operation is selected; TASK-004-04 owns manual derived rebuild.

## Delivered derived maintenance (TASK-004-02 M3)

M3 supersedes the intermediate wiring limit above. Publication atomically inserts
one `derived_index` / `search` job per exact version and extractor revision.
Seal, import and search share one serial local dispatcher. Startup sweeps all
published versions in bounded batches and resumes interrupted checkpoints under
a new attempt generation. Failed/cancelled same-revision jobs remain terminal;
a new extractor revision quarantines and replaces the prior index while keeping
job history. Recovery: TASK-004-04 rebuild; no resume route is implemented here.

Only the owner can read/cancel maintenance jobs. The fixed job.result shape is
`{version_id,work_kind,stage,files_done,files_total,bytes_done,bytes_total,pages_done,pages_total,failed,checkpoint}`.
Stages are inventory/extract/commit/complete. All counters are decimal strings;
unknown totals are null. Files count inventoried entries, bytes count static HTML
input bytes, and pages count completed static HTML pages. Checkpoints are opaque
hashes containing no extracted text. Failure/cancellation retains this shape and
safe index reasons, and does not change original files or the published version.

`search.index_updated` uses resource kind `version`, ID version_id, index revision,
and no body. Replay and live FilterEvent use existing version.read plus exact
project/version anchors and current lifecycle visibility, matching Check. Hidden
or trash/purged identities become library.invalidated with no identity/body/revision.
This event grants no access to maintenance job bodies or extracted snippets.
`features.search` remains false; search HTTP/CLI/UI delivery remains later work.

Authority revalidation can pause derived maintenance without converting it to a
terminal extraction failure. Unrelated global setting changes allow the exact
captured static version to continue; lifecycle or narrowed permission revisions
fence the current attempt and preserve its resumable checkpoint. Restored
eligibility reconciles under captured permissions. An internal trusted maintenance
caller may explicitly reauthorize the same exact target under current revisions;
no HTTP resume route or new scope action is added. Definitive failures (including
wrapped EDQUOT as resource_exhausted) and cancellation remain terminal.

## Delivered reference maintenance foundation (TASK-004-03 M2)

`derived_work.work_kind=references` shares `derived_index` and the existing serial
local dispatcher. `internal/adapters/sqlite/import.go` enqueues its exact-version
work in the publication transaction. `internal/application/work/maintenance.go`
routes both kinds in the daemon and importer entrypoints. Its bounded startup
sweep registers each published version's current search before references; it
includes historical versions and reuses terminal same-key identities.

There is one reference job per `(version_id,work_kind,referenceRevision)`. The
selection query applies own-search terminality before ranking/LIMIT, and the
reference processor checks it again before claiming. Completed, failed and
cancelled search jobs are terminal; a genuinely absent search job is allowed.
Other search states wait without taking a reference turn. Cancellation remains
runnable while the gate is closed. Reference work never waits for another
reference job and never invokes a search fence.

`internal/application/references/worker.go` persists declaration byte offsets at
top-level member and reference-array boundaries,
outgoing local keys and incoming binary `(from_version_id,metadata_reference_id)`
keys in separate phases. Prepared declarations commit before structural attempts;
each row/checkpoint change shares the attempt-fenced transaction. Enumeration
uses at most 64 physical rows and yields after the 8 ms iterator slice. Immutable
object verification, JSON decoding and normalization occur outside the writer
lock. Non-reference manifest values share the importer streaming walker and yield
at top-level member boundaries after the deadline, without rescanning on resume.
Page-only pins use immutable inventory and the shared renderer HTML classification;
metadata page declarations alone are insufficient. Only region pins need
current completed extraction. In-version aliases resolve to canonical edge paths.
Current immutable identities and required anchor snapshots are checked again before
edge writes. Interrupted attempts resume under a new generation. Definitive
failure/cancellation keeps its same-key history; no public resume is added.

Reference progress retains the fixed result shape above. Files/bytes describe
its own generated manifest, with unknown totals null. It reuses indexed source
anchors without reading page bodies (`pages_done=0`, `pages_total=null`). Failed
or cancelled work preserves the last committed counters; a terminal stage does
not claim a completed manifest. Incoming outcomes never add target-derived
counts to the source's materialization or progress.

`references.updated` has version resource identity, source materialization
revision and no body. It fires only for that version's own ready/failed
transition. Incoming edge changes emit no referring-version event and never
advance its materialization revision. Live and replay filtering use current
source version eligibility, including lifecycle masking, through the existing
index-event projector. This is source state; it grants no target access.

The pinned unit mapping in `internal/application/references/revision_test.go`
requires `referenceRevision` to advance with search extraction, anchor algorithm
or reference parser changes. The derived-work milestone itself enabled no
reference route, CLI, Reader dispatch or `features.references` capability;
M3 supplies the read routes/CLI and M4 supplies client/Reader delivery and its
capability. The source-only event boundary above is unchanged.


### Reference read/event boundary (TASK-004-03 M3)

GET/HEAD list and resolve never write declarations, materializations, edges,
jobs, checkpoints, revisions or the outbox. Existing system maintenance alone
creates edges; a ready source without an edge reports `resolution_pending`.
Incoming processing persists only its private checkpoint and edge changes,
without a per-row job event. Otherwise hidden incoming cardinality would become
a count of replacement `library.invalidated` notifications. Ordinary source
materialization ready/failed and the target's own existing completion events
retain their documented semantics.

`references.updated` is source-only: source version identity, its materialization
revision and no body. Target incoming updates never emit a referring-version
notification or replacement invalidation, and never increment that source
revision. Live and replay use the same current source eligibility masking as
search-index notifications. Paired deterministic hidden-target/unknown-target
fixtures compare complete source-only live/replay objects with matched hidden
control work; incoming slices separately assert no appended event. Authorized
owner/target subscribers still receive the target's own events. Reference list
cursors therefore exclude every target/edge/global-outbox generation.

## Explicit derived-index recovery (TASK-004-04)

The owner validate/rebuild surface in [HTTP](http-api.md#backlinks-and-deterministic-index-recovery-task-004-04)
supersedes the earlier future-rebuild limitation. It keeps job.kind=derived_index,
work_kind search/references, current semantic extractor revisions, fixed decimal
progress shape and the single WorkJobs/Router. No /jobs/{id}/resume operation ships.
Owner rebuild rejects affected nonterminal work, atomically re-owns only terminal
current-revision slots under its operation receipt, and retains old jobs/results,
events, object_pins, declarations and historical canonical edges. Search/all queues
references behind that version's terminal search work before cleanup begins.

A reference replacement starts with a private repair phase. REINDEX and checkpoint
advance share one transaction; the worker then yields without finishing the job.
A durable cancel before that transaction prevents it; a durable cancel after it
stops later work while retaining the repaired index. A cancel arriving during the
SQL statement waits for the shared store gate and is not durable/acknowledged yet.
Terminal completion races retain the existing terminal no-op cancel semantics.
Daemon shutdown cancels the SQL context and rolls back an uncommitted repair and
checkpoint together. Restart fences prior attempt generations and resumes the
committed checkpoint. No lock-independent abort signal or new scheduling queue is
introduced. Failed/cancelled replacement jobs require a new explicit rebuild.

references.updated stays source-only; incoming work and global index reconstruction
emit no target-derived source revisions, per-incoming-row events or hidden counts.
Existing owner-only job visibility and event masking apply. No backlink event or
live-update promise is introduced; Links refetches on scope/open/explicit refresh.
backlink_index_repair policy/findings remain with EPIC-008/009 and no AI is called.


### Personal Review invalidation (TASK-005-01)

An actual Review transition appends `resource.invalidated` for resource type review,
id equal to the exact version ID and the personal resource revision. Its retained
outbox body supplies redacted audit actor/session/operation and state deltas; that
body is not a client mutation capability. History is a separate owner-only resource.
Scoped readers and currently hidden targets receive only the existing content-free
library invalidation. Replay/no-op creates no extra transition notification. Browser
and native consumers refetch authenticated state rather than infer completion.

## Document lifecycle invalidation (TASK-005-03)

Actual archive/trash/restore commits emit `document.lifecycle_changed` with document
revision in the same transaction as plan result/audit/receipt. Stored redacted audit
facts identify actor/session/plan/action/from/to. Notifications never include those
private bodies. No-op/replay emits no extra transition. Current live/replay visibility
masks a hidden target as content-free library.invalidated, including mixed batches.
Clients re-fetch current state, close a denied renderer/delivery and clear protected
views. Stream reconnect/resync also rechecks; late responses do not remount closed views.

Trash advances durable application/delivery fences. Accepted import/derived jobs are
cancelled monotonically, and captured lifecycle application revisions remain stale even
after explicit owner restore. Restore never clears cancellation or resumes old work.
Current deterministic recovery may explicitly authorize eligible work; production AI
and maintenance input/application integration remains with TASK-006-06/TASK-008-06.

## Purge lifecycle job (TASK-005-04)

The existing lifecycle kind now executes owner-confirmed manual purge. Acceptance
atomically records the terminal decision, intent/checkpoint, job and original 202
receipt. As an explicit exception to generic cancellation, an accepted purge job
returns INVALID_TRANSITION on cancel without recording cancellation. There is no
undo, automatic un-purge, external dispatch or general job-resume API.

The serial daemon resumes queued/interrupted work and automatically continues
paused purge work with a due persisted retry. Recoverable I/O failures use paused
io_retry; protected candidates use paused protected; mismatched original authority
uses paused guard_blocked. Each continuation checks current owner/library/runtime,
exact committed intent/target, document fence, revision and worker generation.
Retry scheduling backs off from one second to at most sixty seconds, rebased after
restart/clock rollback; clock passage never authorizes deletion. A guard-blocked
turn only rechecks and never adopts changed authority. Pins are never age-reclaimed.

Job results expose purge_plan_id, stage, reason, next_retry_at, remaining and
metadata_removed. Metadata removal can finish while the job stays paused for
independent/shared ownership, possibly indefinitely. Completed requires all metadata
phases, retained-set checkpoints settled and all candidates collected or safely already absent. Failed/completed/
cancelled remain immutable and are never reselected as work. The original acceptance
receipt stays completed/202 throughout. Existing job.updated and lifecycle_changed
outbox events retain current visibility masking, including library.invalidated.

## Copy and exclusive restore execution (TASK-005-05)

Backup/export use the existing queued/running/interrupted/cancelling and terminal jobs,
completed principal-wide 202 acceptance receipts, parent linkage and transactional
job.updated events. Progress has set_id/stage, verified_objects/current_object_bytes
as decimal strings, nullable total_objects/total_bytes and checkpoint_id. Stages are
capturing/copying/publishing/complete or interrupted/failed; plans are planned. Public
progress has no host paths. One object copy turn is bounded; formatting and verification
stream inventories without a whole-payload buffer. All checkpoint/publication writes
recheck owner/library/runtime/project permissions and attempt generation.

Cancellation retains incomplete ownership. Resume after terminal backup failure creates
a linked job; explicit abandon is required to discard protected incomplete bytes.
Restore is an additive job kind executed only under exclusive offline maintenance,
never selected by the normal dispatcher. Its natural plan receipt and optional aliases
record the health-checked result. Restore quarantines old nonterminal jobs and keeps
accepted/reconciling receipts outcome-unknown; a new generation cannot imply non-dispatch.

Copy cancellation retains the same copy-set identity, capture barrier and backup pins.
Queued cancellation is resumable through a linked job; a running copy's stop is
confirmed only after its owned turn drains. A terminal job row alone does not prove
that finalization or retention handles have closed. The serial dispatcher collects
the one copy turn before retry/abandonment or daemon storage/profile shutdown.

Copy publication authority and cessation ownership are distinct. When current project
permissions or the recovery hold revoke execution, the dispatcher cancels and drains
its owned turn, then records failed job/set state with current set/job/attempt/library/
runtime/owner fencing. This stop record does not publish output or release capture
barriers, checkpoints or backup pins. Explicit cancellation retains precedence. A stale
selected job cannot write through a newer linked job, or reopen a complete, abandoned,
expired or restored-paused set. A failed stop-record transaction does not discard the
drained slot or its result before durable acknowledgement; shutdown surfaces unresolved
persistence failure.

Protected restore distinguishes immutable terminal replay from admitting a new
operation binding. Natural/existing-alias terminal replay does not consult an
unrelated later dataset. New bindings inspect every available logically owned
current, prior and incoming database at the journal's physical phase, including
prior after incoming becomes active and saved incoming during rollback. Each side
is library/runtime/profile fenced and uses its preserved owner principal namespace;
no arbitrary historical-directory scan or global UUID reservation is implied.
Preview expiry applies before restore admission; a matching admitted staging
journal retains recoverability while original state/input/receipt guards remain.

## Current provider request identity and explicit retry admission

TASK-006-01's internal RequestSHA is a trusted semantic identity, not an opaque
client token. Later admission owners must construct it from stable principal
and authorized scope, the canonical semantic request (including selected
provider/model and meaningful options), and exact ordered input content/revision
identities. Fresh operation/job/read/target-set/session IDs, authentication
epochs, timestamps and unrelated configuration revision churn must not provide entropy that lets
an equivalent uncertain request evade reconciliation. Public clients cannot
choose an unchecked digest. Capture IDs still bind the attempt's exact readset;
they do not make equivalent external intent new.

Unresolved equivalent attempts and retained restore orphans block preparation
across job IDs. The current non-cancelled job may record an explicit generation
retry. New linked work requires authenticated current owner admission and the
internal AuthorizeProviderRetry port, naming the complete finite set of
predecessor attempts whose additional execution was deliberately approved.
ParentID alone grants nothing. That port preserves every predecessor and its
immutable cancellation fact; any unnamed equivalent uncertainty still blocks.
When an original orphan job is absent, the exact typed predecessor authorization
retains its historical linkage without inventing a parent row. The trusted
AuthorizeProviderReconciliation port separately grants status-only processing on
an existing owner-authorized carrier for a finite same-request/target orphan
set. Such carriers remain non-dispatchable after settlement, and cannot obtain
continue/retry authority from their lookup links. Later public admission owners
must provide normal receipt/current-owner validation before using either port.
Create a fresh carrier and admit its finite set atomically in the same writer
transaction; existing semantic attempts/results/retry work cannot be repurposed.
A genuinely changed authorized semantic request/source has its own identity and
can proceed without globally disabling new work after recovery review. Public
generation/question admission is not implemented by this foundation.

The driver persists accepted progress before a long wait: provider operation/turn
ID maps to provider_request_id, thread ID to provider_session_id, and local RPC
correlation is distinct. Verified backend completion must be explicit, never
inferred from transport shutdown or output EOF. A known completed backend with
definitively unsupported or forbidden output uses local
failed/result_unavailable accounting,
which does not assert provider failure or provider cancellation. Status-only
reconciliation can thereby settle external uncertainty without a source read,
forbidden result retrieval, library application or another dispatch.

Temporary full-history RPC/context/spool/object-persistence failure after known
completion keeps outcome_unknown with reason result_retrieval_pending. This
primary reason records the observed native completion and accepted exact IDs;
it does not assert a saved Body/result_hash. Explicit reconcile_only can recover
the full output and then continue without Execute. Later inconclusive metadata
cannot overwrite that known completion with generic transport_unknown. Restore
merges compatible completion facts monotonically and retains genuine terminal
contradictions; a reliable conflicting failure/interruption is not silently
replaced by the earlier completion. Actual writer failure cannot claim a durable
primary fact. A successful full response with unsupported view/phase/no public
final, status-only output suppression or actual payload-authority refusal still
uses failed/result_unavailable. These rules introduce no new queue or migration.

## Current saved continuation and operational carrier transitions

The original semantic job transitions above remain authoritative. Explicit
continue of eligible saved output follows paused -> queued -> running ->
completed, with no new provider submission. Application generation reserves the
exact next claim after current eligibility checks; producing generation stays
immutable. A pre-claim rollback/pause leaves that reservation unconsumed and
revalidatable. Explicit retry revokes that pending reservation, preserves the
old attempt and uses one finite generation-specific grant. Queued cached work
never falls through to dispatch when its bytes/authority become unavailable.
Interrupted continue/retry is refused until durable reconciliation establishes
an eligible paused state. All interrupted processing begins with reconciling.

The bounded pre-claim continuation/preparation path also permits queued ->
paused before any claim commits: unavailable transport or a local preparation
rollback, or failure to revalidate the explicitly selected saved continuation's
bytes/current authority, leaves a recoverable pause. A saved continuation keeps
its exact next-generation reservation unconsumed; it cannot fall through to a
new dispatch. This narrow recovery mapping does not replace the original
failed transition for definitively invalid newly submitted input. Ordinary caller
context stop before claim keeps the queued reservation unchanged and releases
only that invocation's reader pins. Current permission and generation checks
still precede any later authorized claim.

Successful normalization or partial finite status work stays reconciling while
admitted work remains. A failed normalization commit leaves interrupted intact.
Public cancellation of an interrupted provider job normalizes and records its
final cancellation/unknown disposition in the same writer/receipt transaction.
An already recorded cancel intent keeps its existing generation. The outbox
contains ordered bodyless job.updated sequence/revisions; it does not store an
intermediate state payload or commit a separate intermediate public snapshot.

The existing typed status-only carrier has these additional narrow operational
transitions; they grant no semantic output or provider dispatch authority:

| From | Event / guard | To |
| --- | --- | --- |
| queued | Fresh owner-authorized status carrier and exact finite orphan set admitted atomically; no semantic attempts/results/capture/retry work | reconciling |
| reconciling | All admitted status effects definitive and intents drained; persist actual terminal operational resolution proof | completed, with provider_reconciliation result only |

An interrupted carrier still goes through reconciling. Its operational completed
result cannot be interpreted as a generated answer, assessment or publication,
and does not rewrite the original historical job/state/cancel facts. Ordinary
terminal job reconcile-only preserves that job's state/result/generation; after
explicit recovery release it may also drain already-settled finite intents and
holds locally, with no provider lookup, body retrieval or application.

TASK-006-02 owns capture/selected-snapshot retention and availability semantics.
TASK-006-04/05 admission must acquire the authorized queued execution-input pins
atomically before work becomes ready, independently of any selected_snapshot
owner. TASK-006-03 owns actual adapter delivery and observed input evidence;
preparation pins are not a substitute for queued admission lifetime. Reference-
only bytes that are no longer available remain unavailable rather than becoming
a manufactured snapshot. These owners remain ordered future implementation.

## TASK-006-03 transport ownership

The concrete Codex adapter is injected into the existing ProviderCoordinator and
single waiting phase. Setup-thread identity and typed transport audit attach to
the original attempt before the send fence; accepted turn/thread IDs and exact
input observations use its synchronous durable progress callback. A cancellation
racing accepted-ID registration is rechecked after registration. An unsuccessful
interrupt still bounds local waiting and leaves remote outcome unknown unless a
matching native terminal observation arrives.

ProviderScope.invocation is trusted server admission state. The adapter derives
and checks semantic identity from actual intent/options and canonically ordered
exact source identities/locations/hashes, excluding local read/capture/snapshot
IDs and timestamps. Actual frame order is identical. Public job/list/cancel/
resume and stored/replayed receipt bodies omit invocation, and their ETags agree
with that projection. A client digest or job ID supplies no admission authority.

Definite pre-send preparation failures retain prepared and
may_have_dispatched=false, with their exact local reason on the paused job.
An internal native setup deadline with a live caller uses setup_timeout; actual
caller cancellation/deadline and dispatch-fence refusal remain distinct from
that local preparation timeout.
Explicit continue revalidates the undispatched semantic work and may retire
its old prepared local intent; it never resends an uncertain dispatch. Actual owner cancellation retains its distinct no-send
cancellation semantics. unsupported_input_format and
source_unavailable are bounded local preparation categories. Native
usageLimitExceeded maps to quota_exhausted, rateLimitExceeded to
provider_rate_limited, sessionBudgetExceeded to provider_budget_exhausted and
contextWindowExceeded to provider_context_limit. They are not interchangeable
quota observations. Unknown external effects retain outcome_unknown accounting.
The revision-17 audit records native backend state separately from normalized
result state, local drain and any later exact-ID lookup observation. Existing
saved-result, hidden-carrier, retry and pin rules remain in force.


The operation-time job response keeps its authoritative opaque ETag through
receipt serialization and replay, including hidden-target reconciliation.
Visibility filtering may mask fields further but never rebuilds an old header
from the current job while retaining the old body. Current GET reflects actual
later changes and supplies the validator for a new operation; replay preserves
the historical response. Private invocation is never restored for projection.

Complete authorized output and independently known native terminal facts commit
before required final audit. A selective required audit failure preserves those
facts while failing/pausing that query campaign visibly. The exact executing
runtime/job-generation owner may abandon the existing finite intent and its
pending siblings without completing a linked carrier. No remaining intent may
automatically retry the failed campaign. With no eligible external effect,
existing intent or restore hold, new reconcile_only fails the transition; the
existing already-admitted settled shortcut remains unchanged. A genuine primary
writer failure cannot claim a durable fact; persistently unavailable storage
leaves accounting uncertain rather than reporting successful persistence.

Accounting for an already-complete spool survives ordinary caller/daemon context
cancellation only while the current runtime, generation, source policy and
payload authority still permit it. Explicit owner cancel/revocation remains a
refusal. Current output guards apply on each chunk; no fixed size/output cap or
blanket total copy timeout is introduced. Handled provider accounting failure
keeps dispatcher service available, with a bounded first failure retained for
shutdown reporting after Poll collection. A durably handled error with positive
owned-transport drain evidence is a historical diagnostic only after collection.
A controlled restart may carry that first bounded diagnostic to final shutdown;
it does not treat current phase, unknown cleanup, missing drain evidence or
unrelated joined shutdown errors as safe. The first later unsafe accounting
failure is retained independently, so an earlier safe diagnostic cannot hide it.
The internal ProviderOutcome may carry a producer's explicit LocalClosed and
LocalCleanupReason independently of complete native Transport/Lookup metadata.
This means actual owned transport drain or known absence of an owned transport;
its zero value and arbitrary nil CleanupError remain unknown. The same known
successful cleanup reasons, nil CleanupError, durable settlement, collection and
whole-error-tree guards apply. This local observation never manufactures backend
status, accepted IDs, source delivery or complete native audit identity and adds
no stored/wire field or schema.

Recognized owner/policy refusal during result copy or Apply is ordinary refusal,
not provider_accounting_unavailable or a shutdown failure. Already saved output
and its pins survive; changed generation never permits the old worker to alter
its newer owner. Genuine independent storage/audit/cleanup errors remain visible.
Every post-start return, including initialization and undispatched preparation,
joins owned cleanup. Complete verified audit metadata may record that result;
incomplete setup never fabricates auth/model/CWD/disclosure/accepted identity.
Before a complete audit is permissible, cleanup uncertainty remains an explicit
bounded phase/shutdown diagnostic while the job retains its primary refusal.
The undispatched attempt remains prepared, and Poll/Reader/Review stay usable.


Explicit finite status admission binds current owner auth_epoch in the existing
typed resource-revision attachment, atomically with its intents. Existing intent
Claim supplies the current runtime fence after restart; no admission-runtime
permanent equality is added. This status-only authority can reconcile an older
producing epoch after explicit protected recovery, without rebinding payload
scope or assuming authority from a missing legacy attachment. Epoch revocation
still denies an executing old grant. Last successful intent completion or exact
campaign Abandon removes the status grant with its owned intent lifetime.

## Generation saved-tree application (TASK-006-04)

Public generation creates one existing generation job and a preparing typed
draft with explicit target/base/intent. Selected source capture is handed to that
job atomically; incomplete capture keeps the full failed selection and cannot
dispatch a partial request. Source-free input remains unverified. Exact prompt
and prior explanation objects are distinct from project-source job inputs.

Native completion is a primary fact; safe owned-process drain is additionally
required before the host captures the ordinary output directory. Its complete
service-derived ZIP is saved as the existing result object before validation or
application. Native final text identity is separate. Unknown output cannot be
replaced with a final-text prefix or an old partial tree. Resume/reconcile uses
existing finite job/intent authority; saved continuation never calls Execute.

New heavy ZIP/import preparation uses the caller/phase cancellation context
outside the writer. Required result/accounting writes retain their existing
bounded detached accounting behavior. Preparation error/cancellation preserves
the durable result and current generation/owner guards; it does not bypass typed
cleanup/protocol diagnostics or reverse a winning explicit cancel. Reader,
Review and unrelated dispatcher work stay usable after handled AI failures;
unsafe/current/joined shutdown failures remain visible at Stop.

The final short application transaction checks exact current candidate,
permissions/epoch, lifecycle, runtime/generation and base, then atomically records
publication or a ready draft plus receipt/outbox. Reject fences linked work;
ready replacement preserves base/history and prevents the old saved generation
from applying a different candidate. Stale-base publication is an explicit
conflict with the candidate retained, never silent rebasing or a new document.

### Caller recovery of a generation job-resume decision

A resume/retry/reconcile decision is a separate durable request from initial
generation admission. The shared caller journals its own runtime, key, decision,
expected attempt generation and job ETag before POST. A pending decision first
reads its original receipt with current authority. Completed receipts remain
readable across runtime change; a missing receipt under a changed or unknown
legacy runtime never permits POST. Same-runtime retry retains the exact saved
identity and still uses the existing owner/ETag/restore-hold/orphan guards.

Definitive PRECONDITION_FAILED 412 or INVALID_TRANSITION 409 and confirmed failed
receipts can be explicitly acknowledged after a current job read. One journal
write preserves the complete refused subrequest as history and releases only
the matching active slot under the original journal lock. A new decision uses
current state and a new key. Operation conflicts and uncertain outcomes stay
pending; no implicit rebase or paid retry is introduced. A stopped older action
cannot authorize a later callback, and a new explicit acknowledgement has its
own cancellation lifetime after the old promise settles. These caller records
change no job-resume route, decision union or server authority.

## Saved question ownership and events (TASK-006-05)

Question admission reserves its immutable captured scope and existing provider
job before source capture is adopted. Pending source selection retains actually
consumed snapshot ancestry so Trash/purge/cancellation cannot lose an in-flight
dependency. Initial admission, dispatch, incremental input, preparation and
answer application retain current accepted-principal/version/source/epoch
checks. No second queue or provider-attempt ledger is added.

The typed question result is prepared outside the writer and adopted in a short
guarded transaction. Its exact producing attempt and complete final/partial/error
record are retained. A prepared answer pauses safely if current authority cannot
apply it; retry refuses before mutating attempt/job/retry authorization, while a
permitted saved continuation consumes the owned result with no Execute. Unknown
work, restore orphans and explicit new-attempt decisions retain existing rules.

Answer settlement emits resource_kind=question, resource_id=question_id,
kind=answer.saved in the normal transactional outbox. Bulk replay and single
redelivery resolve the actual record owner and current version/project/lifecycle.
Hidden, missing or foreign records become content-free library.invalidated at
the same stream sequence/epoch rather than exposing question/answer IDs. Current
visible owner events retain their exact original event. Saved views refresh on
these invalidations without resubmitting drafts or clearing unresolved identity.

## Multi-device event cursor continuity and resync barrier (TASK-007-04)

Browser SSE (`GET /api/v1/events`) parses complete blank-line-terminated frames
(`id`, `event`, `data`), treats any non-empty domain event type as an authorized
invalidation hint, and advances the remembered `Last-Event-ID`
(`stream_epoch:sequence`) cursor only after the full frame is parsed and the
awaited invalidation or `resync.required` handler succeeds. A disconnect or
failed handler before completion leaves the prior cursor intact so the event is
replayed or resynced on reconnect.

Closed native event operations (`subscribeEvents` and `eventsPoll` in
`desktop/readerfacade`) accept an optional `cursor`. Opening `subscribeEvents`
without a cursor performs a passive bootstrap against `/api/v1/events`, captures
the initial `resync.required` (`reason: "bootstrap"`, `stream_epoch`, `head`,
`resume_cursor`), and returns `resync` and `cursor` alongside `subscription_id`.
The client native wire adapter holds bootstrap delivery behind a `listenerReady`
gate until the outer event listener is registered so no commit between initial
open and first `eventsPoll` can be missed.

On `resync.required` or `stream_epoch` rotation, `ShellController` executes a
single-flight full-state resync barrier: it performs a passive session/capability
recheck (`Haeryong-Passive-Read: 1` / `passive: true`), advances
`readGeneration` to fence pre-barrier in-flight reads without double-triggering
when the authority epoch already rotated, and awaits all mounted screen resync
sweeps. Superseded required reads cannot count as successful refreshes. A local
read-generation change during the sweep requires a new current sweep; loss of
verified connectivity also prevents acknowledgement. If authority is not yet
ready or any sweep fails, `#pendingResync`
remains armed with bounded retry rather than acknowledging the cursor past `H`.
See [multi-device reconciliation evidence](../testing/multi-device-reconciliation.md).

## Scan inventory, deterministic structure and repair child lifecycle (TASK-008-01)

`job.kind = "scan"` progresses through stages `inventory` -> `inspect` ->
`repair` (when deterministic `derived_index` (`references`) child repair jobs
are active) -> `complete`. Each worker step claims the scan job and materializes
or inspects at most 64 `job_items` (`document`, `version`, `page`, `region`,
`entry`, `source`, `index`) per transaction (`EnumVersionOrdinal`,
`EnumAfterPath`, `EnumAfterRegion`, `EntryAfterPath`, `SourceAfterIdx`) so large
result directories remain interruptible and resumable across process restarts.
Startup `recoverLocalJobs` transitions in-flight `running` scan jobs to
`interrupted`, increments `attempt_generation`, resets in-flight `running`
`job_items` to `pending`, and resumes from the committed cursor without
duplicating items or findings.

When structural inspection detects divergence between authored reference
declarations and stored `authored_references`, `authored_edges`, and
`reference_materializations` (`from_version_id`), it records a deterministic
`backlink_index_repair` finding. Under effective
`auto_apply.backlink_index_repair = "auto"`, the worker queues or reuses a
`derived_index` (`references`) child job via `RebuildScanReferences`, guarded by
the storage `ReferenceFence` (`scanRepairAuthority`). Before enqueuing or
applying rebuilt reference edges, the fence verifies that the parent scan job is
active and unheld (`restore_job_hold`), `attempt_generation` matches, current
principal/workflow/source authority and `permissions_revision` still hold, the
target document/project is not in Trash or purged (`document_trashed` /
`document_purged`), the stricter of captured and current repair policy remains
`"auto"`, and `max_run_seconds` has not expired; when a linked repair child
cannot complete or is interrupted, the parent scan records the version in
`result.incomplete_reference_repairs` with `rescan_recommended = true`.

The scan run's `max_run_seconds` deadline is evaluated against elapsed wall time
since the first `ClaimJob` (`StartedAt`), excluding only explicit
protected-restore hold duration (`restore_job_hold`). When `max_run_seconds` is
exhausted before completion, `result.budget_exhausted` is set to `true` and
remaining unfinished (`pending` / `running`) items transition to `skipped` with
reason `run_budget_exhausted` (`result.semantic.reason = "run_budget_exhausted"`).
See [scan inventory evidence](../testing/scan-inventory.md).
