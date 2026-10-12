# Durable jobs, events and findings

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
| question.changed, upload.updated, project.changed | Saved question/answer or partial/error changes, resumable upload state/checkpoints, registered project/settings views |
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
