# Data model and transactional rules

## Provider attempt foundation (revision 15)

TASK-006-01 adds provider_attempts, provider_inputs,
provider_retry_authorizations, provider_linked_retry_authorizations and
provider_orphans, plus finite provider_reconcile_intents. The attempt's producing
generation is immutable; a separate application generation can be explicitly
bound to exactly the next claim for explicit saved continuation after current
checks. Ordinary-restart eligibility also covers output recovered later by
supported reconciliation; a rolled-back claim leaves the binding unconsumed.
Input objects receive ordinary active-attempt pins. Actual input_origin is
recorded separately from source capture; request/session identifiers are retained
only when observed. Complete output gets an immutable result-object reference
before any application transaction. Retry authorization is generation-specific
and does not delete the older attempt. Linked new-work authorization names a
finite predecessor-attempt set; ParentID alone cannot bypass uncertainty. Input/result
hashes are historical metadata independent of object-row lifetime. Only terminal
jobs with all effects settled release temporary input pins; result and unrelated
snapshot ownership remain. Orphan input references pin matching available
recovery objects without manufacturing missing bytes. The protected-restore
byte copier supplies a requested-object availability callback over fresh
verified staging; ready metadata is not availability evidence for unowned
objects omitted from the backup, and no whole-object inventory is buffered.

Orphan facts survive protected replacement by an older snapshot. They preserve
newer observed identities/outcomes or a visible conflict, without manufacturing
missing source/result bytes. They block an equivalent unacknowledged reissue
under a fresh identity. The [attempt record](testing/provider-attempts.md)
describes current checks and the later provider/generation/question owners.
Terminal jobs expose older uncertainty in a minimal public summary. Explicit
status-only reconciliation captures and durably claims a finite attempt set;
current terminal state/result/generation remain unchanged. Retry and lookup
admissions are separate, and no client-supplied native identifier selects work.

Reconciliation intents retain an exact foreign-key-backed ledger or orphan
target. An explicit trusted owner admission may bind an existing carrier job to
a finite same-principal, same-semantic-request and same-target orphan set, even
when the original job is absent. This link never fabricates the missing job or
grants dispatch. Status carriers remain non-dispatchable after lookup completion,
and their linkage cannot grant continue/retry. New linked work needs separate
finite retry authorization, preserving every older effect and cancel fact.
Same-ID ledger/orphan copies share supported terminal observations; a local
cancel-before-dispatch inference cannot override retained dispatch uncertainty.
Startup under exclusive profile ownership reclaims interrupted status lookups.
A recorded normal-restart transition may carry an eligible saved-result binding;
same-daemon reassignment cannot revive an obsolete result. Explicit continuation
is queued and binds the next claim only. Claim/application commits atomically
after verified byte reads outside the writer. Repeated unconsumed continuation
rechecks authority; explicit retry revokes only its pending cached binding.

Settled orphan-only status work has its own terminal operational carrier result.
Its retained resolution proof binds the actual resolver, principal, semantic
request, attempt, observed native identities, outcome and completion time. Original
job/state/cancel facts remain historical facts. A bare resolution_job_id cannot
release ownership. Complete proof and absence of a live original continuation
permit release of only that orphan's execution-input pins, including after an
older restore lacks the carrier job. Unknown/conflicting/partial work, result
ownership and independent snapshots remain protected.

The immutable may_have_dispatched flag describes the logical/possible boundary,
not provider receipt. Local prepared failures retain explicit no-dispatch
provenance; missing legacy provenance remains conservative unknown. Existing
ledger and orphan facts both participate in restore reconciliation, so local
failure cannot erase prior possible external effects. Supported correlated
backend terminal evidence remains usable. Fresh status-only carrier creation
and finite admission commit atomically; existing semantic work cannot be
repurposed as a completed status operation. The in-process provider_continuation
verified-reader pin is separate from execution ownership: normal Read releases
its unique pin, and exclusive startup reclaims that kind after process death.
It never releases attempt/orphan, snapshot or durable result owners.
Explicit local no-call facts converge
with older prepared rows without overriding possible dispatch. Both conflict
chains survive canonical merge and repeated inventory replay, retaining every
available input/result owner. Existing settled terminal reconciliation intents
can drain after explicit recovery release without changing the semantic result.

The logical `provider_attempts` inventory below includes later adapter metadata.
TASK-006-01 persists attempt/generation/state, exact request/input identities,
actual input observation times, observed request/session IDs, result hashes and
ownership. Provider identity, attempt lifecycle timestamps, disclosure-inventory
integration and allowance observations are TASK-006-03 responsibilities,
implemented by the revision-17 attachment below before enabling that capability.
Unknown allowance is not zero; no missing native input observation is inferred.

**Source:** Revision 4 storage/state separation. **Proposal:** logical schema v1, documentation baseline 1.6.1, clarified by [shared wire registry](contracts/schema-registry.md). [TASK-002-02](testing/storage-foundation.md) implements and exercises the current storage/local-operation subset. TASK-002-03 adds schema revision 3 owner/session/pairing records and a verified DB-plus-required-object pre-migration recovery set; current auth evidence is [separate](testing/authenticated-sessions.md). TASK-002-04 adds schema revision 4 upload entries, import intents and internal metadata preparation/index ownership, with [atomic-import evidence](testing/atomic-import.md). Complete generated metadata remains in an immutable manifest object; current version rows keep its hash and a compact summary, while supplied metadata retains its separate immutable identity. This logical model also lists future feature-owned tables; it does not assert that all of them exist.

## Identifiers and common columns

Use opaque UUID-form strings for service identities. Optional metadata source/evidence/reference IDs are nonempty Unicode strings local to one version, not service UUIDs. A logical document represents one AI result directory. Page identity is (version_id, page_path), using its exact relative UTF-8 path with case and Unicode preserved. Region identity is (version_id, page_path, region_id), with a nonempty page-local Unicode ID. Absence of metadata/regions does not prevent import or display. All API times are UTC RFC 3339. Mutable entities have integer `revision >= 1` and an individual opaque strong ETag. Public APIs never expose server absolute paths by default.

A document's display sequence (v1, v2) is assigned at publication under a transaction; `version_id` remains the stable key. Drafts have a candidate version ID but no published sequence until publication succeeds. `latest_version_id` never points to an unpublished draft. The canonical enums and personal/lifecycle transitions are in the shared registry.

## Tables and constraints

| Table | Key and important columns | Constraints / behavior |
| --- | --- | --- |
| libraries | library_id, created_at, schema_revision, runtime_generation, stream_epoch, auth_epoch, unfiled_project_id | One library per profile. Stable Unfiled project is resolved/created atomically for owner imports without a selected project and remains a normal configurable project; workflows use their selected authorized project. Normal restart preserves stream history; restore rotates runtime/auth/stream generations while preserving logical library identity. |
| projects | project_id, name, lifecycle, revision, permissions_revision | Registry exists before publication, including the automatically resolved owner Unfiled default. permissions_revision is this project's source/disclosure generation, separate from global config_revision. active/archived categorization does not cascade to documents or source repositories. |
| source_mappings | mapping_id, project_id, source_id, kind, repo_id, approved_root, retention_mode, effective_config_revision | Derived/indexed source choices from the project's effective configuration; source/read/export views omit unnecessary host paths. kind uses source_mapping.kind git/files; captured files become source.kind snapshot, while committed Git captures use git and explicit working-copy captures use snapshot. Access-affecting edits advance the project's permissions_revision. Recheck current bounds at use without per-read approval. |
| documents | document_id, project_id, title, lifecycle, previous_lifecycle, trashed_at, latest_version_id, revision | Lifecycle is document-level; previous_lifecycle is active/archived for restore from trash. project_id is stable for the document in v1; no move operation is introduced. title is the latest published display title, updated atomically with latest_version_id; earlier titles stay in immutable version metadata. |
| document_versions | version_id, document_id, sequence, base_version_id nullable, content_set_id, generated_metadata_ref, supplied_metadata_ref nullable, manifest_sha256, supplied_metadata_sha256 nullable, payload_sha256, title_snapshot, default_page nullable, viewer_profile, source_basis, source_intent nullable, created_at, published_at | Unique (document_id, sequence); one complete immutable directory snapshot after publication. Same ID with different normalized metadata/payload returns IMMUTABLE_VERSION_CONFLICT. Immutable generated metadata owns question, generation and external-dependency fields; implementations may index them without duplicating every field into a required SQL column. Author metadata remains optional and separate from payload. |
| version_sources | version_id, source_id, kind, repo_id, object_format, exact_revision, snapshot_sha256, release_label, retention_mode, asset_path nullable, retained_asset_hash, capture_context_ref nullable | Composite PK (version_id, source_id). Maps exactly to immutable supplied/normalized source metadata; capture context may be held in that referenced metadata. A raw directory can have no source rows and source_basis unverified. Payload and source references may point to the same immutable object; neither silently overrides the other's lifetime/current access rules. |
| version_pages / regions | version_id, page_path, optional label/preview metadata; version_id, page_path, region_id, label, summary, optional state_schema | Page PK (version_id, page_path); region PK (version_id, page_path, region_id). Page discovery does not require author declarations. Optional/static/runtime anchors may be unresolved; missing or duplicate anchors do not reject the directory. |
| evidence | version_id, evidence_id, source_id, path, symbol, line_start/end, section, capture_status, retained_asset_hash | Composite PK (version_id, evidence_id); FK (version_id, source_id). capture_status is immutable author-declared metadata. Current availability and separate service-verification outcomes are derived from real checks/bytes/permissions; no later check rewrites this row or the generated manifest. |
| region_evidence | version_id, page_path, region_id, evidence_id | Composite FKs bind the exact page-local region and same version's optional evidence. |
| authored_references / authored_edges | from_version_id, metadata_reference_id, from_page_path nullable, from_region_id nullable, exact target_document_id/version_id/page_path/region_id, supplied relation string; edge_id, resolution | Original optional reference, including its free-text authored relation label, is immutable even when unresolved; a supplied label is not proof of a code dependency. Derived eligible edges/backlinks resolve only exact authorized targets; no hidden target metadata, guessed latest version or full-import failure for an unresolved target. Server edge_id is separate from the version-local reference ID. |
| overlay_relations | relation_id, from_version_id, target_version_id, type, rationale, origin, evidence_json, decision, revision | Later semantic information is not authored provenance. |
| topics / topic_assignments | topic_id; assignment_id, target {document_id, version_id nullable, page_path nullable, region_id nullable}, origin, decision, decision_actor, pinned, revision | Exact typed target; region requires page/version and page requires version. accepted means applied by the recorded owner/auto actor; owner pin/reject and evidence-sensitive rejection rules apply. |
| assessments | assessment_id, version_id, page_path nullable, region_id nullable, target_set_id, coverage_set_id, state, scope_json, evidence_json, config_revision, permissions_revision, origin, created_at | Append published assessment history for exact scope/target with persistent unit contributions; coverage counts are derived. Optional regions never create a zero denominator for all results. Unapproved candidates remain findings. |
| coverage_sets / required_units | coverage_set_id, version_id, target_set_id, scope_json, unit_kind, captured_at; coverage_set_id, kind, page_path nullable, region_id nullable | One immutable non-overlapping required tuple set per captured scope/target. Version scope requires all discovered HTML pages or one whole-version unit when no HTML; page scope defaults to one page, region scope to the exact region. Required exclusions cannot remove units. |
| assessment_units | coverage_set_id, assessment_id, target_set_id, kind, page_path nullable, region_id nullable, outcome, discrepancy, reason nullable | Link each exact required tuple to its assessment and target. outcome is assessed/partial/blocked/not_checked; skipped/excluded units retain a reason. Persist enough records to select the latest applicable contribution for every tuple and derive eligible/assessed/complete, never counts alone. The registry permits reuse across coverage sets only for matching version/target/unit scope; retain the originating assessment and coverage-set IDs so a later page-only check can update one whole-version summary unit without summing overlaps. |
| findings | finding_id, job_id, kind, origin, version_id, page_path nullable, region_id nullable, target_set_id, evidence_json, evidence_fingerprint, proposal, status, approval_required, reason, config_revision, revision | Canonical statuses follow registry; evidence uses the same typed citation union as assessments, preserving observed input and later verification separately. Preserve rejection history and exact applicability, with optional applied overlay/draft/job reference. Equal region names on other pages are different targets. |
| review_states | owner_id, version_id, state, reviewed_at nullable, revision, event_sequence | Composite PK; applies to the complete exact directory snapshot, not merely the currently opened page. Only owner action mutates. Reopen/clear clears current reviewed_at, not historical events. |
| review_events | sequence, event_id, owner_id, version_id, action, old_state, new_state, actor_session, operation_id, created_at | State-change history is preserved except explicit target purge; receipt replay/no-op adds no duplicate transition. |
| reading_positions | owner_id, version_id, page_path nullable, page_query/page_fragment, first_opened_at, last_opened_at, revision | PK (owner_id, version_id), one mutable last-opened pointer/observation. It does not own per-page progress or reuse a page-position ETag. Opening never changes Review. |
| page_positions | owner_id, version_id, page_path, page_query/page_fragment, position, updated_at, revision | PK (owner_id, version_id, page_path), independent per-page ETag. Position writes do not move the last-opened pointer; no page count cap. |
| reader_preferences | owner_id, version_id, default_page nullable, revision | PK (owner_id, version_id), mutable reader preference; non-null names an existing HTML page. null clears it without modifying immutable version.default_page or payload. |
| revisit_flags | owner_id, document_id, flag, revision | One document-level independent owner flag, regardless of review status. |
| annotations / annotation_history | annotation_id, version_id, page_path nullable, region_id nullable, page_query/page_fragment, selected_text/context object references, body/body_ref, created_at, updated_at, deleted_at nullable, revision; prior anchor/body revision and deletion event | Persist original version/page/region and navigation/state context; a region requires page_path. Large notes/context are streamed immutable objects. Explicit reattachment/deletion records history; normal note deletion hides the current note without silently moving its anchor or erasing permitted history. Target purge/explicit retention rules govern eligible removal. |
| questions / answers | question_id, version_id, page_path nullable, region_id nullable, page_query/page_fragment, source_mode, target_set_id, requested_selection, question text/ref, display_state/context_ref, job_id, created_at; answer_id, question_id, body/body_ref, typed_citations, explanation_references, limitations, created_at | Exact navigation context and resolved targets through captured_target_sets. Completed citations use observed_input/read_id plus actual attempt input observation, or posthoc_verified/verification_id with read_id/input_origin null, exact target/revision/bytes_sha256 and optional matching package_evidence_ref. Explanation references are separate exact stored version/page/hash records, not project-source citation variants. Current excerpts/unavailable state may be composed. Large objects are streamed; partial output is not a completed answer. |
| captured_target_sets / captured_reads / job_inputs | target_set_id, targets_json, requested_selection, captured_at; read_id, target_set_id, source identity/location and basis, exact revision/snapshot, bytes_sha256, config_revision, permissions_revision, optional package_evidence_ref, content_hash/pin_id nullable; job_id, read_id | Only actual source-target capture creates a target set; import-only declarations do not. Select default targets by latest captured_at for the same requested_selection, never by unrelated assessment age. read_id identifies actual captured bounded source/explanation bytes. Provider delivery is separately recorded by an attempt input observation; a source read alone does not establish it. Old package evidence is linked only on exact identity/location/bytes match. Reference-only identity does not imply retained bytes. |
| citation_verifications | verification_id, version_id/evidence_id nullable, target_set_id nullable, source identity/location, exact revision/snapshot, bytes_sha256 nullable, outcome, expected_bytes_sha256/expected_excerpt_ref nullable, config_revision, permissions_revision, checked_at, verified_at nullable, reason nullable, attempt_id nullable, content_hash/pin_id nullable | Append separate service check history: captured/matched/mismatch/unavailable. Inline expectations are preserved exactly in the same logical record or referenced immutable bytes. captured without expectation proves limited exact-target resolution/capture only; matched additionally satisfies supplied byte expectations. verified_at is populated only for captured/matched, and only those records can back posthoc_verified citations. No backdating, observed_input conversion or original metadata rewrite. Standalone imported-evidence checks use the HTTP verification operation under current source authority with temporary pins. |
| drafts | draft_id, proposed_version_id, document_id nullable, content_set_id, manifest_sha256, payload_sha256, intent, status, base_version_id, revision | preparing/ready/published/rejected/failed transitions follow registry; captures a whole directory candidate. Stale base cannot silently publish. |
| jobs / job_items | job_id, kind, scope_hash, config_revision, permissions_revisions, target_set_id, state, attempt_generation, deadline, cancel_requested_at; item_id, captured_version, stage, state, coverage/reason | Shared job.kind/state and job_item.state enums; named stages follow the job-kind contract. cancel_requested_at is monotonic; no resume decision can restart application after accepted cancel. Persist captured scope, per-item coverage and equivalent-active-run identity. |
| provider_attempts | attempt_id, job_id, attempt_generation, provider, provider_request_id, provider_session_id, dispatch_state, created_at, started_at/finished_at nullable, disclosure_inventory_ref, input_observations, result_pointer, result_hash, allowance_observation | input_observations stores or references streamed {read_id, input_origin, observed_at} records for actual daemon_supplied or native_observed inputs. Unsupported/unobserved native reads remain unobserved; post-hoc target checks use citation_verifications. Provider IDs stay server-side; no raw credentials. Unknown outcomes remain explicit. |
| operation_receipts | principal_id, operation_id, method, canonical_target, original_request, normalization_context, request_fingerprint, result_ref, original_status, result_metadata, state, created_at | Composite PK across all route families. Import normalization_context persists resolved project/document/base, generated IDs/timestamps/defaults/normalizer version once; an equivalent upload reuses it without masking changed original caller fields/preconditions. Different semantic request with the same key conflicts. Composite commands use distinct final and preparation keys per import-workflow; a final domain receipt is not fabricated before dispatch. New completion keys at the same active checkpoint can each reference one existing seal job. accepted is only the reservation with null original_status/result; a committed durable job-creation response sets completed/original_status 202 while the job runs independently. Concurrent pending duplicates return the stable registry 202 response without dispatch; reconciling denotes actual uncertainty. GET by operation ID is unambiguous for the authenticated principal. Keyless plan commits use plan_id; optional supplied keys link to the same outcome without overwriting any unrelated receipt. |
| events / audit_entries | stream_epoch, sequence, library_id, resource_type/id, kind, revision; actor/run/reason and redacted deltas | Composite event identity; transactional outbox. Delivery checks current authorization and can emit non-content invalidation. |
| config_revisions | config_revision, validated_json, actor, created_at | Authoritative effective configuration; export is derivative. |
| sessions / pairing_requests | hashed_secret, owner_id, device_id, nullable idle/absolute expiry, revoked_at, auth_epoch; request/display fields | One owner authority with per-device revocable bearer sessions for local and remote clients. Remembered browser storage is client-side; raw bearer secrets are not stored server-side. Restore rules apply. |
| workflow_bindings / workflow_grants | workflow_binding_id, owner_id, principal_id, remembered selections; grant_id, workflow_binding_id, principal_id, hashed_secret, captured scopes/project generations, revoked_at, auth_epoch | Server-allocated binding and principal persist across credential rotation and are not hashes of project/scope sets. A different workflow gets a new binding. Owner-authenticated local CLI/IPC issues/revokes derived current-scope credentials; no required principals table or remote issuance route. Changed remembered choices never enlarge an issued credential or give it personal review/purge rights. |
| lifecycle_plans / configuration_plans | plan_id, owner_id, operation, state, target_revision, config_revision, eligible_versions, expires_at, confirmed_by, confirmed_at, commit_result_ref | Natural commit identity: at most one result per plan. Current owner authentication plus ordinary exact-impact confirmation; a plan is not an authentication credential or separate authority tier. |
| uploads / content_sets / version_entries | upload_id, originating_principal_id, originating_device_id nullable, owner/scope, purpose, input_mode, selection_name, exact target, sealed_content_set_id nullable, checkpoint, state, byte_size, current staging-byte/reference accounting, error nullable, created_at, updated_at, abandoned_at nullable; content_set_id, payload_sha256, sealed_at; content_set_id, relative_path, kind, content_hash nullable, exact_link_text nullable | State is open/sealing/sealed/abandoned. Completion seals only verified complete input; async completion references an upload_seal job. Exact purpose/target variants and summary/detail views are delegated to import-workflow. Cancel fences that job and returns a non-abandoned upload to open only after confirmed stop, preserving all bytes; abandon alone prevents reuse. Persist the active seal job/generation so crash recovery can settle both states together. Preserve owner-listable rows and reference/pin linkage so lost-ID or vanished-workflow uploads can be resumed or explicitly abandoned under current owner authority. Derive reusable/unavailable_reason from state, full purpose/target, actual available bytes and pinnability after staging transfer. Sealed bytes may be adopted repeatedly only for that exact target; adoption records references rather than mutating/consuming the set. Abandon fences new adoption/writes and safely releases only upload staging references. Staged rows are incremental; exact path storage preserves case/Unicode, represented directories and link text without creating every path on the host filesystem. Lookup is exact-first, then unique canonical-equivalent Unicode only; ambiguous fallback fails explicitly without changing stored names or hashes. FileList-only input cannot invent missing empty directories/link metadata. |
| content_objects / context_objects | content_hash, byte_size, media_type, state; context_ref, owner/scope, version_id, page_path nullable, render_instance_id nullable, content_hash | Verified immutable bytes shared by reference. context_ref is not a credential; saved records/jobs adopt context with lifetime pins so closing the page cannot destroy their context. Object adoption and GC reservation are coordinated. |
| object_pins | pin_id, owner_kind, owner_id, content_hash, state, created_at | Pins cover staged publication, drafts, active source/evidence checks, active job inputs/results, coherent backups/exports and reconciliation needs. Expiry alone cannot release active/unknown work. |
| tombstones | minimal document/version identifiers, deleted_at | Preserve authorized historical reference meaning without retaining a sensitive body/title by default. |

A null page_path is an explicitly whole-version anchor. A non-null region_id requires its page_path; code must never infer another page from a repeated region label. Artifact query/fragment/state are captured navigation context, separate from immutable page identity. Optional metadata may enrich any of these records but is not required to store/render raw files.

The CLI/UI caller profile separately keeps a small composite-operation journal: final consumer ID, distinct create/seal keys, upload/checkpoint/job linkage, original pre-transfer target/base and dispatch phase. It exists before a final server receipt; server upload discovery does not manufacture the missing dispatch history. This is not another daemon-owned orchestration engine. Follow [import workflow](contracts/import-workflow.md#composite-operation-ids-and-recovery).

These are logical schema names and preservation responsibilities, not a requirement to create every feature table or duplicate every metadata field into SQL before its owning Epic. Referenced immutable records and normalized columns are both valid if the HTTP shapes, exact identities and lifetime rules round-trip without loss. EPIC-002 owns generic jobs, receipts, revisions, object-pin accounting and transactional outbox; later Epics add provider attempts, scheduler behavior and their event types. Unavailable capabilities return FEATURE_NOT_READY, not fixture success.

## Reference, source and deletion integrity

Foreign keys from authored edges cannot cascade-delete the originating explanation when a target is purged. Preserve minimal target identity/tombstone while removing eligible target content. Original reference text may name an unresolved target without blocking import. Derived validated edges distinguish new eligible targets from unresolved/inaccessible targets and already-authored edges whose targets are now gone. Guessing IDs never manufactures access to hidden/trash metadata or content.

Store the canonical edge once. Backlinks are reverse lookups, not AI-authored duplicate rows. Apply permission/lifecycle filtering before counts and snippets. Ordinary owner UI may show authorized cross-project links; AI discovery uses its narrower approved scope.

Original optional metadata capture, current source availability and freshness are different facts. The service can answer available/unavailable/denied from current source mappings and retained objects without mutating published provenance. A retained digest alone is insufficient to read/reconstruct bytes. Capture current targets once at request acceptance and store captured_at with exact requested_selection; later dispatch/resume cannot reinterpret a moving branch. Import declarations alone do not manufacture such a capture. A list chooses only the latest actual capture for the same requested selection and performs no source/AI work. Raw content may have no source/evidence rows and remains source_basis unverified. Managed generation and answers require evidence for material project-code claims; import itself does not semantically classify arbitrary prose as an admission gate. Missing requested pinned/current source is unavailable/unable_to_verify; supplied explanation context or an empty target set does not prove a source check succeeded.

## Transaction patterns

### Idempotent publication

Authenticate and check current access. Look up (principal_id, operation_id) using the shared request fingerprint. An existing matching receipt is replayed/reconciled before rechecking its original If-Match/base precondition; changed request identity conflicts.

Before POST /imports, the caller resolves remembered association, captures the normal pre-transfer base and journals distinct upload-create, seal and final-import keys under the [import workflow](contracts/import-workflow.md). Upload preparation streams, inventories, hashes and durably seals the complete selected bytes with staging pins. The publication handler consumes that sealed result; it does not perform client association or start another transfer.

For a new POST /imports reservation, require document_id as an existing ID or explicit JSON null and validate it against the sealed upload target. An omitted project_id comes only from that target; no server-side last-document lookup occurs. Reserve the original request and normalization_context once, allocating a document ID only for explicit null and a version ID only when omitted. Preserve the client's explicit base, or capture latest atomically at this first valid reservation only for the low-level omitted-base variant. Save omission markers, created_at, permitted defaults and normalizer version. Existing operations reuse their saved context before normalized metadata/fingerprint derivation, independently comparing original fields and preconditions. An equivalent sealed handle cannot introduce a fresh timestamp, recapture the base or mask a changed request.

Validate optional metadata against the sealed result and prepare eligible indexed rows while still invisible. Recheck the captured base, current authority, cancellation and resource preconditions at commit. Atomically adopt the completed set and eligible metadata/edges, publish the version/latest pointer and display title, record the publication outcome and append the outbox event. A prior completed 202 creation receipt continues to identify its import job; later job failure does not rewrite that response. No whole-directory RAM buffer or transaction proportional to all file bytes is required. Unresolved optional links/regions remain explicit without blocking the directory. Stale base returns VERSION_CONFLICT without publication. Identical explicit version identity/content follows the package equality rule; different immutable bytes return IMMUTABLE_VERSION_CONFLICT.

Pin acquisition/content adoption and GC reservation share short serialized DB coordination. GC cannot reserve a pinned/referenced object, and publication cannot attach to an object already reserved for deletion without reestablishing verified durable bytes. Release staging pins only after committed references or safe abort cleanup exist. A crash after file finalization but before DB commit leaves pinned/recoverable staging or collectible orphan files, never a visible half-version.

### Personal state

PUT review requires If-Match or If-None-Match: * for absence. Validate owner, action/state transition, exact version and active/archived lifecycle. Update current state, append review event, audit and outbox in one transaction. A new stale write returns 412 with safe current revision metadata. Completing v3 leaves v4 untouched.

Reading-position PUT captures the exact last-opened page, optional artifact query/fragment and server first/last-opened timestamps. Per-page progress is a separate page_positions mutation with its own ETag; it does not move that pointer. Reader default preference is also separate from immutable version metadata. Clients coalesce progress and serialize each resource's saves using its last acknowledged ETag; one submitted save has one operation ID, not one receipt per scroll event. A 412 preserves unsaved context and requires a read/reconciliation before another write. Idempotent replay does not advance timestamps. Revisit is document-level. Annotation lists expose each note's ETag; a list ETag never authorizes an individual note edit. Reattachment preserves old anchor/history.

### Apply finding or provider result

Compare captured version/target set, current permission and policy restrictions, lifecycle, owner decisions, current resource revision and active job/attempt generation. Increment generation on retry/reassignment and accepted cancellation so late results from the same daemon lifetime cannot apply. Attempt observations/output may still be retained for reconciliation without mutating the library.

Append a validated assessment/overlay, finding decision, audit and outbox atomically. Failed applicability leaves the finding stale/blocked, or proposed with approval_required: true when an explicit owner decision is needed. Undo checks the current overlay revision and cannot overwrite intervening edits. Draft publication is a distinct explicit operation.

### Purge, pins and coherent copies

Acquire document lifecycle coordination, verify the exact owner-confirmed plan or confirmed timed-purge policy, current revision and eligibility, commit terminal purge intent, remove eligible private records, create minimal tombstones and enqueue unreferenced content GC. Concurrent restore either commits first or gets a clear conflict; it cannot resurrect partially deleted bytes. Source roots are never deletion targets.

Trash/purge invalidates affected job application authority and visible renderer contexts. Physical GC honors other retained versions, drafts, active input/result pins and coherent backup/export pins. An explicit source-redaction/purge workflow may cancel affected work and replace unavailable private inputs with minimal recovery facts; it must explain the impact and cannot use diagnostic expiry to discard unknown-operation history silently.

Backup/export derives its referenced object set from one consistent snapshot and holds pins through streamed checksum verification/finalization. Stream indexed files and represented directories/links without requiring a complete in-memory archive; preserve original paths/bytes and report actual destination conflicts. No HTML/file-count/total-size ceiling or automatic file omission is introduced. New publications after the snapshot do not silently join that copy. Incomplete copies are not reported as complete. Full operator restore follows [backup recovery](operations/backup-recovery.md); a scoped portable export is not a general merge/restore protocol.

## Derived views

Use the shared registry's summary derivation for latest-version review, last-reviewed identity, updated-since-review, historical/current freshness, exact target sets and version/page/region coverage. A captured coverage_set_id fixes non-overlapping exact required tuples and retains each applicable assessment/target/outcome/reason. Counts are derived from that persistent record; regionless pages and no-HTML versions remain valid. Zero/unknown coverage or no applicable captured target cannot imply aligned_within_scope, and reassessment_required remains a distinct outstanding-change flag. Do not persist one lossy document freshness flag as authority over independent page/region/target assessments. File listings, search extraction and thumbnails may update incrementally with explicit coverage; an unfinished index is not a reason to hide the stored directory or claim every page was analyzed. Ordinary scope restriction may make evidence unavailable without proving an explanation outdated.

## SQLite settings and recovery

Use foreign-key enforcement, recorded migration revision/checksum/applied_at history, bounded busy handling and a tested WAL configuration. schema_revision and compatibility/readiness are exposed through authenticated capabilities/status; an incompatible newer database or migration recovery is explicit SERVICE_RECOVERING, not request UNSUPPORTED_SCHEMA or a client-triggered migration. All provider/user waits occur outside short write transactions. Configure durability deliberately and benchmark without silently weakening it. G-STACK establishes the selected spike driver and measured connection settings below; filesystem flush ordering still requires the EPIC-002 storage and EPIC-005 recovery evidence.

The embedded DB lives on local storage; remote clients use the API. Restore rotates runtime/auth/stream generations, invalidates old sessions/workflow credentials according to the recovery contract, pauses automation/exposure, and reconciles uncertain external outcomes before any retry. A restored missing receipt does not prove an operation never ran. [EXT-SQLITE](references.md) supplies narrow external background, not proof of this schema's correctness.

### Initial driver and retrieval selection

[TASK-001-02](testing/stack-spike.md) selected modernc.org/sqlite v1.60.1 (engine 3.53.4) from actual macOS/Linux ARM64 WAL, restart and snapshot tests. Its fixture service uses one connection, WAL/FULL, foreign keys and a 5000 ms busy timeout. TASK-002-02 now applies and verifies these settings on initial and replacement connections and records real migrations; full backup/restore and production indexing remain with their feature owners. X09 selects unicode61 word/phrase candidates, trigram substring candidates for three or more characters, and a filtered authorized-corpus short-query fallback; measured spacing/camelCase limitations remain explicit. TASK-004-02 owns production indexing, pagination, ranking and tuning. This spike does not implement DB-plus-content backup, pins or power-loss recovery.

## Current schema5 additions (TASK-002-05)

Schema5 appends observed updated_at ordering keys to projects/documents, stable workflow principal/binding rows, hashed scoped credential rows and immutable configuration impact plans. Migrations1 through4 retain their committed source/checksums. Existing rows receive a documented observed migration timestamp; historical modification times are not invented. Semantic row changes advance the ordering timestamp; receipt replay/no-op updates do not. Time-plus-ID pagination is deterministic over the current authorized view, not a frozen snapshot across concurrent mutation; event resync requires fresh reads.

Scoped credentials capture only actions/anchors within their active stable binding, current auth epoch and every selected project's permissions generation. A rotation does not invent a new principal. Current restriction intersects that captured scope; an expansion does not enlarge old tokens or accepted jobs. Credential lifetime is distinct from already accepted durable work, whose attempt/target/lifecycle/current binding/project generations remain fenced. No issuer or future consumer table/CRUD is introduced by this bounded enforcement.

Before schema4-to5 DDL, the existing coherent prior-migration recovery set includes the verified SQLite snapshot plus retained/copied referenced immutable objects. Labelled prior-schema fixtures remove only schema5 additions, preserve prior rows/checksums and test normal owner session/object/library/config/stream/auth identity preservation, failures and retry. They are reverse-constructed fixtures, not execution of an old binary or a full future backup/restore engine. [Task05 evidence](testing/query-cli-configuration.md) records the executed scope and remaining gates.

## Source observation persistence (TASK-004-01, migration 7)

These are additive storage implementations within the logical model above;
the new observation relations support the provisional additive HTTP route.
The normative logical-model rows above remain unchanged for later implementing
owners. This section records only the exact implemented migration-7 subset and
its differences; it does not remove future columns, assessment reuse or provider
input linkage from the logical model.

| Table | Exact migration-7 columns | Constraints and meaning |
| --- | --- | --- |
| assessments | assessment_id, version_id, page_path, region_id, target_set_id, coverage_set_id, state, scope_json, evidence_json nullable, config_revision, permissions_revision, origin, created_at | Migration 7 storage only; no assessment producer in TASK-004-01. Empty-string anchor sentinels map to wire null. |
| coverage_sets / required_units | coverage_set_id, version_id, target_set_id nullable, run_id nullable, scope_json, unit_kind, captured_at, origin; coverage_set_id, kind, page_path, region_id, reason, target_set_id nullable | Migration 7: source_observation origin requires run_id; only this origin permits null target. Required-unit PK is (coverage_set_id,kind,page_path,region_id); anchor sentinels are empty strings. Membership is immutable and exclusions retain the denominator. |
| assessment_units | coverage_set_id, assessment_id, target_set_id, kind, page_path, region_id, outcome, discrepancy nullable, reason nullable | Migration 7 PK (coverage_set_id,assessment_id,kind,page_path,region_id). No producer yet. Anchors use empty-string sentinels; outcomes are assessed/partial/blocked/not_checked. |
| captured_target_sets / captured_reads | target_set_id, project_id, requested_selection, targets_json, captured_at; read_id, target_set_id, basis, source_json, location_json, bytes_sha256 nullable, config_revision, permissions_revision, created_at | Migration 7 creates captured_target_sets and captured_reads only. captured_reads.target_set_id references the capture and basis is project_source. job_inputs is future work, not a created table or provider-input observation. A capture alone never proves provider delivery. |
| citation_verifications | verification_id, principal_id, version_id, evidence_id, read_id nullable, target_set_id nullable, source_json, location_json, bytes_sha256 nullable, bytes_origin nullable, outcome, expected_bytes_sha256 nullable, expected_excerpt nullable, expected_excerpt_ref nullable, config_revision, permissions_revision, checked_at, verified_at nullable, reason, operation_id | Migration 7 exact columns: read_id and target_set_id are FKs; unique (principal_id,operation_id). verified_at only for captured/matched; reason uses empty string for no limitation. expected_excerpt is exact BLOB bytes; expected_excerpt_ref stores the staged expectation content hash (the wire request instead names upload_id). Logical attempt_id remains null in EPIC-004 and is not a migration-7 column; no content_hash/pin_id columns are invented here. |
| observation_runs | run_id, version_id, principal_id, operation_id, requested_selection, scope_json, target_set_id nullable, created_at | Unique (principal_id,operation_id), nullable capture FK. Durable exact selection and five-field scope even for all-unavailable runs. Created before dependent relations. |
| source_observations | observation_id, run_id, version_id, target_set_id nullable, source_id, repo_id, base_revision, observed_revision nullable, availability, classification, changed_paths_json nullable, dependency_scope_json nullable, reason, config_revision, permissions_revision, observed_at, operation_id | Non-null run FK; repo_id/base_revision/reason default to empty strings. Availability available/unavailable/denied; classification direct/indirect/unchanged/unavailable/denied. No synthetic revision for a failed source in a partially captured run. |
| reassessment_triggers | trigger_id, run_id, version_id, target_set_id, source_id, anchor_kind, page_path, region_id, reason, changed_paths_json nullable, observation_id, observed_at, created_at | Non-null run/capture FKs. Version anchor uses both empty strings, page uses only page_path, region uses both; wire absence is null. Bound to exact capture and page-local anchor, never unioned by selection alone. |
| check_intents | intent_id, operation_id, principal_id, kind, canonical_target, validated_body nullable, expectation_upload nullable, config_revision, permissions_revision, state, failure_reason, created_at, resolved_at nullable | Unique (principal_id,operation_id); kind verification/observation; state open/completed/failed. Durable reservation and pins precede detached checks; restart settles only from consistent receipt/result history. |

The migration-7 table above includes its coverage/assessment relations.
Compared with the logical model, coverage_sets adds origin/run_id and permits
null target_set_id only for source_observation; captured_target_sets has project_id;
citation_verifications has read_id and operation_id; reassessment_triggers has
target_set_id. Logical nullable page/region anchors use empty-string storage
sentinels. EPIC-004 produces no assessment contributions and leaves logical
attempt_id null. Future job_inputs/package-evidence/provider input records and
assessment reuse retain their later owners.
It does not add job_inputs or a job kind. Existing evidence_observations remain
separate imported-metadata state. Only committed observation runs and committed
verification captures are eligible for reads. The registry's conditional A/C
selection overrides the general capture-only summary rule for observation-backed
current selection: A supplies availability; C supplies the target, denominator
and exact-target triggers. Old rows remain immutable. All-unavailable attempts
retain coverage while target/captured_at stay null. No observation writes a
semantic assessment, observed_input citation or personal Review.


## Migration 8: static search structural foundation

TASK-004-02 M2 appends migration 8 without changing migrations 1–7 or the normative
logical model above. `derived_work` binds jobs/exact versions/revision/checkpoints;
`search_indexes` owns discovery/completion/reason state; `search_units` preserves
exact document/page/region/source/attachment identity; `search_chunks` stores
revisioned nomination postings separately from full normalized literals and
authored display text. External-content unicode61
`search_words` and trigram `search_grams` reference those chunks; insert/update/
delete triggers maintain both indexes. `page_anchors` independently records exact
page-local static IDs, locator kinds, occurrence counts, labels and descendant
text digests, without overwriting supplied immutable regions.

All new enum-like SQL columns enforce nonempty values, allowing future work kinds;
current Go admission/readback rejects unknown values safely. Structural positivity,
boolean, uniqueness, region-page and FK constraints remain enforced. The adapter
admits exactly each FTS table's `_data`, `_idx`, `_docsize`, `_config` shadows with
virtual/shadow types and exact declarations/triggers; arbitrary suffixes and
`_content` are rejected. Migration creates structure only, with no backfill or
worker startup. Existing verified pre-DDL DB/object recovery and transactional
history/user_version/library revision remain authoritative. Search capability is
false. Later milestones own fencing, indexing jobs, querying and presentation.

M3 now uses these structures at runtime: publication atomically creates one
version/work/revision job, the startup sweep covers prior publications, and a
single serial dispatcher advances bounded inventory/page checkpoints. Pending
revision replacements quarantine old rows before bounded clearing. Each write
fences immutable version identity/hashes, captured system scope and current
policy/permission generations, lifecycle, cancellation and worker generation.
Only verified EOF activates body/anchor coverage; restart retains completed pages
and reparses incomplete pages. Definitive failure/cancellation is terminal for
that revision. Typed pre-deletion cleanup removes chunks/FTS, anchors, units and
index/work references while retaining job history. Original files stay immutable;
no new migration, query API or presentation is introduced by this runtime step.

M4 correction uses extractor revision 2 for the changed derived payload format.
FTS postings retain unique trigrams and short words rather than every repeated
position; full literals remain authoritative for phrase checks and snippets.
The normal revision sweep quarantines and rebuilds older derived rows. Metadata
windows use normalized scalar offsets mapped back to authored bytes. One parent
page row activates verified EOF; region read state derives from that page and
its page-local anchors, without a bulk region update. Actual body-read failure
sets the page's failed body state and safe reason under the current worker
generation fence. No schema migration or original-content mutation is needed.

## Reference storage foundation (TASK-004-03 M1, migration 9)

This additive foundation implements the logical authored-reference storage rows.
TASK-004-03 delivered materialization jobs in M2, list/resolve APIs and CLI in M3,
and clients and Reader navigation in M4; `features.references` is true.
Migration 9 is DDL only and preserves the immutable revision-8 manifests,
payloads and prior rows.

`reference_materializations` is keyed by the published `from_version_id` with a
real FK to `document_versions`. It stores manifest SHA-256, reference revision,
job identity, source-only revision and state. SQL checks nonempty state; Go
write/read admission permits only `building`, `ready`, `failed`. A future nonempty
SQL enum is unsupported by the current typed reader. There is no pre-publication
reference staging or import-actor authority reconstruction.

`authored_references` uses `(from_version_id, metadata_reference_id)` and preserves
submitted source/target paths, optional source region, target document/version/
page/region, relation, label, navigation query/fragment and exact authored JSON.
`authored_references_target(target_version_id, from_version_id,
metadata_reference_id)` supports bounded incoming scans. Unknown target IDs have
no target FK and do not prevent preserving the declaration. Composite outgoing
and incoming cursors use exact binary identities; one internal slice is at most
64 rows, not a reference-count/content limit.

`authored_edges` has a separate deterministic UUIDv5 ID derived from length-prefixed
UTF-8 library/version/local-reference identities in a fixed application namespace.
It stores canonical resolved source/target anchors and page/anchor hashes, with
one edge per authored reference and a target lookup index. Existing pins and
recorded hashes cannot be overwritten by a conflicting put. Only identical
canonical identities/hashes can advance the anchor revision. Structural resolution
is independent of the publisher or read history; every future public view must
apply current eligibility/lifecycle to both endpoints. GET/HEAD will not create
edges. Target trash/purge must retain incoming edges and minimal lifecycle facts;
new references to already-purged targets cannot gain a structural edge. The
logical tombstones row above does not require a physical tombstones table: the
selected historical-gone policy uses retained `documents.lifecycle='purged'`.

The typed implementation is in `internal/domain/references.go`,
`internal/application/storage/references.go` and
`internal/adapters/sqlite/references.go`. Exact table/index declaration validation
extends the existing schema inventory without weakening its FTS/shadow checks.
Upgrade failure uses the existing verified pre-migration DB-plus-required-object
recovery set and transactional rollback; old binaries refuse schema 9. Restore
the verified database and required objects together; never edit a revision number
to downgrade a live database. This foundation has no automatic backfill yet.

### Delivered reference work foundation (TASK-004-03 M2)

Publication atomically registers the references work key after search. Startup
registers both kinds search-first for every published version. The combined
reference revision has a pinned extractor/anchor/parser mapping; changed code
requires a new reference revision, while terminal same-key jobs retain history.

The three derived passes commit immutable declarations, attempt outgoing exact
pins, then use `authored_references_target` for incoming pending pins. The own
search terminal gate fixes source anchors before its reference turn. Either the
source's outgoing pass sees final target anchors, or the target's later incoming
pass sees the already-committed declaration. Neither job waits on other reference
work. Unknown targets and unavailable/ambiguous anchors create no edge. Page-only
pins require the canonical regular HTML entry under the shared renderer
classification and its inventory hash, independently of extraction; region pins additionally require current page
EOF and matching page/anchor hashes;
recorded hash disagreement preserves the original edge as anchor drift.

`internal/adapters/sqlite/reference_work.go` owns reference attempt/system-input
fences and source-only materialization transitions. Incoming commits recheck the
source declaration and both endpoints' immutable identities/current anchors;
they change neither the referring materialization revision nor its events.
`ClearReferenceData` removes only rows owned by the permanently removed referring
source, in bounded batches. Cleanup of a target never cascades through other
sources' incoming edges. No target lifecycle workflow is added here.

All structural writes remain authority-free derived maintenance. Read projection
and historical-gone eligibility remain with TASK-004-03 M3; M2 enables no reference
read API or capability and does not change immutable manifests or directory bytes.

## Backlink projection and non-destructive rebuild (TASK-004-04)

SchemaRevision stays 9. The canonical authored_edges_target B-tree is the reverse
access path; there is no duplicated authored_backlinks relation. Incoming traversal
uses its NULL-first target-page/region/source-version/local-reference key, at most
64 raw entries before read-time eligibility/anchor projection. Outgoing counts
use only the selected source-version UNIQUE prefix. Request-local admitted document
ID sets give exact distinct-document counts without a library-wide reader traversal.
Owner reverse-index validation may traverse canonical physical rowid slices before
filtering; each admitted covering tuple is compared with its NOT INDEXED base row
by rowid. Search validation uses version/unit-prefixed seeks and compares immutable
inventory and deterministic chunk/anchor expectations with required derived rows,
including their absence. Legitimate empty extraction requires no body chunks.

Explicit owner rebuild re-owns terminal current-revision derived_work slots in a
single receipt transaction with new queued jobs and strictly increasing index/
materialization revisions. Ordinary puts/enqueue retain their strict transitions;
old jobs, results, events, object_pins and older revision slots remain retained.
Reference replay preserves declarations and canonical pins, including historical
edges to purged targets; it never invokes source-deletion cleanup. Search cleanup
is bounded and references remain gated on that exact version's terminal search job.

The only physical reverse repair is atomic REINDEX of the intact migration-9 index
with its checkpoint, followed by a yield/cancellation check. SQL-context shutdown
rolls back the transaction; restart fences/resumes nonterminal work. Missing/changed
DDL is not repaired online or by a new offline surface; strict startup rejection
and the EPIC-005 verified-restore dependency remain. A corrupt/missing historical
canonical identity/hash cannot be fabricated from a currently purged target.


## Implemented personal Review storage (TASK-005-01)

Append-only migration 10 adds review_states and review_events; migrations 1–9 keep
their original checksum-locked bytes. Current state has composite owner/version PK,
FKs to the owner and immutable published version, revision>=1 and a CHECK coupling
reviewed state with non-null reviewed_at. event_sequence references the latest
transition; committed sequence orders summaries even when timestamps tie. Each
history event has an opaque UUID, owner/version, explicit action/delta, actor session,
operation ID and UTC time. Indexes bound per-version history and owner/state lookups.
Exact new table/index declarations join validateTables startup validation after the
existing search/reference checks; missing/changed DDL refuses startup.

ReviewPut uses the existing serialized receipt transaction. State and review history
are saved with an events row containing redacted owner/session/operation/action/delta
audit facts and a content-free invalidation; receipt completion shares that commit.
No-op and replay preserve state/time/history; the receipt still confirms their result.
Document summaries select current completion/continuation by committed event order
inside the same transaction as list eligibility/filtering. Page/reader state is never
consulted or mutated. Publication leaves new Review absent and old records intact.

Upgrade uses the existing verified pre-migration SQLite plus required-object snapshot.
Fault-before-commit keeps revision 9 intact; normal restart accepts revision 10. Recover
by stopping the writer and returning to the verified pre-migration recovery set with
its matching objects, following the storage recovery procedure. There is no SQL down-
migration or promise that an older binary can open revision 10. Protected application
restore remains TASK-005-05; Review does not implement it early. Execution and limits
are recorded in [version-review](testing/version-review.md).

## Anchored note storage (TASK-005-02)

Append-only SQLite revision 11 adds annotations, annotation_history and revisit_flags.
Annotations index owner/version/ID and keep relational owner/document/original-version/
current-version identity, page/region, revision, soft-deletion marker and the typed
revision snapshot. History is keyed by annotation_id/revision and retains original
and prior anchor/body/context. Revisit is keyed by owner/document and records explicit
false independently from absence. Startup validates all declared tables/indexes while
retaining reachable search, reference and Review declaration checks. Older binaries
refuse revision 11. Failed migration rolls back; recovery uses the verified pre-migration
SQLite/object set. Protected restore remains TASK-005-05.

A note mutation adopts referenced body/context objects and their history ownership,
updates current state, appends history and audit-bearing outbox facts, and completes
the operation receipt atomically. Large byte preparation occurs outside the short
transaction under exact-attempt preparation pins. Saved history owns immutable references
and context lifetime pins; ordinary edit/reattach/delete does not release historical
objects. Read streams acquire independent read pins. Navigation/renderer disposal does
not release accepted note ownership. Later purge/retention/backup implementations must
account for annotation_history references and pins, including contexts from old anchors.

## Document lifecycle storage (TASK-005-03, revision 12)

Append-only migration 12 adds documents.previous_lifecycle (nullable active/archived),
trashed_at and nonnegative trash_generation. Trash captures the former state/time and
advances trash_generation; restore clears current trash metadata but retains that fence.
All versions inherit document lifecycle. Neither archive nor trash edits immutable
versions, personal records, incoming canonical edges, object references or lifetime pins.
Legacy rows with unknown prior state cannot guess their restore destination.

lifecycle_plans stores owner/document/action, open/committed state, captured document/
config/permission revisions, expiry, selected impact digest and the safe result.
lifecycle_plan_items stores its ordered metadata snapshot. Expired is a derived read
state. Current eligibility is reapplied to every projected item and count on GET/replay/
operation reads. The digest excludes informational reader positions/preferences while
binding explicit personal decisions/history and incoming-reference impact. Commit atomically
checks those facts, updates the document/fences, records plan/audit/outbox and completes
the natural receipt plus optional alias. No owner wait or file-body I/O holds the transaction.

Declaration validation keeps 8→9→10→11→12 reachable and checks new tables/indexes/columns.
Old binaries refuse revision 12. Failed upgrades roll back and retain the verified
pre-migration SQLite plus required objects; recovery restores that matched set while
stopped. No SQL downgrade or protected operator-restore engine is introduced here.

## Implemented guarded purge storage (TASK-005-04)

Append-only revision 13 rebuilds lifecycle_plans and its child to admit purge,
without changing migrations 1–12. purged_versions permanently reserves globally
unique version IDs, retaining document/deletion identity. purge_intents binds one
job/plan/document to owner/library/runtime, terminal document revision, bounded
phase/checkpoint (including retained_sets awaiting other live owners) and retry timing. purge_candidates persists candidate hash/size
before ownership is released. All declaration checks 8 through 13 remain reachable;
failed upgrades retain the verified prior DB/object recovery set.

Acceptance clears documents.title/latest pointer and commits the terminal lifecycle
fence, tombstone inventory, job and completed 202 receipt atomically. Child-first
cleanup covers every annotation_history revision's body/context ownership, derived
FTS/outgoing reference rows, owner records and target-only source history before
version deletion. Incoming edges from retained explanations remain historical
witnesses. Shared content sets, resumable uploads, active/unknown work and every
independent pin remain owners. Collection uses serialized reservation/deleting,
unlink/directory-sync and token-checked finalization; source roots are never targets.

Ordinary note deletion still preserves history. Explicit document purge minimizes
private copies in old receipts/plans while keeping original fingerprints, identity,
state and comparison digests. Equivalent redacted requests cannot redispatch;
changed request identity still conflicts. This is owner-confirmed retention,
not forensic erasure of independent copies, WAL, FTS/free pages or hardware history.

Reader-resource deletion uses the same exact owner/version boundary as impact
preview; a UUID appearing in another page's path establishes no ownership.
Abandoned uploads are no longer retaining owners. Terminal target metadata indexes
are removed before set eligibility is checked, and retained sets are revisited after
their last live version/upload/import/index owner releases them. Verification excerpt
references are recorded as candidates and released by exact verification identity
before those records disappear; independent pins/references remain authoritative.
Purge waits for open target check intents to settle before removing their ownership
or source rows. Observation intermediate saves apply the shared lifecycle check,
so reserved work cannot create a new target set/run after terminal purge.

## Coherent copies and protected recovery (TASK-005-05, revision 14)

Migration 14 appends copy_sets, its kind index, copy_versions and recovery_holds;
migrations 1–13 remain byte-identical. Exact declaration validation chains through 14,
including older search/reference/personal/lifecycle/purge declarations. Old binaries
refuse the new revision. Failed migration rolls back and retains the verified prior
DB/object recovery set.

copy_sets holds private owner/library/runtime-bound scope, destination, stage and
checkpoint JSON with a normal backup/export job identity. copy_versions freezes one
ordered selection's document/version/content-set and payload/manifest/supplied hashes.
The capture flag conservatively prevents new GC reservations until snapshot-derived
backup pins are durable. It does not block ordinary publication or personal writes.
For an export that explicitly includes retained sources, copy_sets checkpoint
JSON retains the exact snapshot-selected RetainedReadIDs. Current read ancestry
is checked from these IDs at copy checkpoints and publication, without expanding
selection from later captures. A resumed export resolves the same saved snapshot;
whole backups retain their existing policy.
The snapshot contains all operational receipts, lifecycle/purge checkpoints and permanent
version reservations. Sessions/pairings/workflow verifiers are sanitized before hashing.

recovery_holds stores one exact restore reconciliation, original preferences and known
current preferences. Restore rotates generations, retains cancellation, fences nonterminal
jobs with restore_job_hold resources and safe config, and pauses incomplete copy records.
Only explicitly selected cleanup jobs can leave that quarantine under current guards.
Completed restore jobs/receipts and independent backup_trial_restore observations use
existing typed ledger/resources, not a second task framework. No provider-attempt schema
or automatic external redispatch is introduced.

Copy state also retains a private expected export-index and full formatted-tree digests at publication readiness,
separate from the original plan digest. This binds recovered output without changing
frozen payload membership. Restored complete-copy rows retain observation history;
their older runtime does not authorize current retention deletion. The protected
runtime directory retains per-plan restore terminal outcomes independently of the
single active-switch journal and the replaceable library receipt tables.

## Migration-16 managed source capture implementation

This additive implementation preserves the logical requirements and earlier
migration records above. TASK-006-02 adds these exact relations:

| Relation | Stored fields and ownership |
| --- | --- |
| source_captures | capture_id, principal_id, project_id FK, preallocated target_set_id, state capturing/completed/failed, requested_selection JSON, config_revision, permissions_revision, captured_at, operation_id, reason; unique principal/operation. Only completed full capture publishes its target set; partial wire target/time are null. |
| source_capture_reads | read_id PK, capture_id FK, source_id, source_json, location_json, content_hash, byte_size, retention_mode, availability, reason. Every selected path has a pending/result row. Historical hash has no object FK and cannot retain or reconstruct bytes. |
| job_inputs | job_id FK and read_id FK to captured_reads, composite PK. Trusted atomic admission acquires exact generic job_input pins; this relation alone is not an attempt input observation. |

The source_capture owner protects each verified staged object. Completion adopts
selected_snapshot references by read_id; reference_only staging ends after
atomic job handoff or standalone completion. Snapshot version context is the
immutable version_id inside requested_selection: existing document purge releases
only those snapshot references, while project-only captures and every independent
owner survive. source_capture_read is a unique invocation-owned verified reader;
exclusive locked startup reclaims only provably dead capture/reader ownership
and settles interrupted captures without altering job/attempt/orphan owners.

Provider input/orphan inventory derives optional retention_mode and
source_version_id from the exact managed read binding. source_version_id keeps
the immediate capture's literal context. Optional source_version_ids is the
sorted, deduplicated set of inherited version-context owners, including any
known immediate context. Merge retains this ownership union independently of
which input_origin/observed_at observation or conflict fact is selected; missing
historical ancestry is not invented. These are lifetime/scope facts, separate
from actual input observation. Full backup marks
reference-only source reads uncopied and removes only their temporary source/input
owners. Independent same-hash payload, selected snapshots, result and other
owners remain required objects. Protected restore retains that metadata and
uncertainty; it does not infer available bytes from ready object rows.

Migration 15 to 16 uses the verified pre-migration recovery set and one atomic
migration transaction. Missing/altered migration-16 DDL refuses startup. Old
hash-only captured_reads are preserved without invented content objects,
job_inputs, managed bindings or input observations. Backup compatibility retains
the exact schema/fingerprint requirement; an older software backup is not
silently relabelled as schema 16. See [managed-source evidence](testing/managed-sources.md).

## Provider transport audit (revision 17)

The existing provider_attempts reason value result_retrieval_pending means a
native completed turn was observed but its complete authorized public output is
not yet saved because local retrieval/persistence was interrupted. State remains
outcome_unknown; accepted IDs and this primary fact commit independently of the
secondary transport/lookup audit. No result_hash or Body is fabricated. An
inconclusive later lookup cannot erase it. Compatible copy/restore preserves it
in either order, a saved result is stronger, definitive output refusal remains
failed/result_unavailable, and genuine native contradictions remain explicit.
This uses existing fields and preserves revision 17 DDL and migration checksums.

Migration 17 adds provider_transport(attempt_id,value) as a one-to-one typed JSON
audit attachment to provider_attempts, not another request ledger or queue.
Migrations 1–16 retain their bytes/checksums. Existing attempts receive no invented
provider identities, timestamps, input observations or transport facts.

The attachment retains verified provider/version/binary hash, setup-thread ID,
actual model/effort/CWD/approval/sandbox, inherited instruction and disabled-plugin
counts, exact disclosure identities/locations/hashes/sizes, and nullable ordinary
usage permission with known/unknown disclosure. frame_sha256 identifies the exact
serialized turn/start **params** bytes; RPC envelope/correlation is excluded.
prepared_at, accepted_at and finished_at are local observed lifecycle times,
not invented provider timestamps. backend_state uses native TurnStatus;
backend_observation_source distinguishes live_event from history.
New driver_outcome describes the driver result (result_ready before persistence),
independent from the durable attempt state. Existing normalized_outcome is
retained as historical typed audit, never upgraded into new evidence. local_transport_closed/cleanup_state report only
verified owned local drain. last_lookup separately records a later native state,
local observation time, observation_source and that lookup client's cleanup result. History interrupted is not reliable cessation.
An optional inherited_capabilities snapshot records configured web-search mode,
observed connected MCP server/tool counts with enumeration completeness, nullable
app availability and local observation time; failed/incomplete telemetry stays
unknown. It never retains names, descriptions, credentials or raw configuration.

Identity/disclosure facts cannot be rewritten after capture; lifecycle fields
advance without backdating actual input observation. Public operational jobs
omit the attachment and private invocation. Restore outstanding inventory and
orphan/conflict retention carry this audit with its existing attempt facts,
including metadata-only cases where source/result bytes are absent. A status
lookup never acquires source/result permission merely because the audit exists.
Strict declaration validation refuses missing/altered transport DDL. Atomic
migration/recovery and the existing protected-restore review barrier remain the
rollback boundary. See [provider adapter verification](testing/provider-adapter.md).


Protected restore merges compatible transport observations independently of
attempt-state precedence: absent core observations cannot dominate actual later
observations, reliable completed/failed facts dominate uncertain history views,
and compatible newer lookups survive both restore directions. Genuine identity
or terminal contradictions retain their original conflict facts. An absent/null
conflict link is not conflict evidence. The pre-send capability snapshot is not
refreshed by a later lookup, and replay is idempotent. No migration or invented
legacy telemetry is required for these additive typed audit fields.


The existing resource_revisions table also holds one typed
provider_reconciliation_authority attachment per admitted finite job campaign.
It records the current owner auth_epoch at explicit Resume/status admission in
the same transaction as its intents. The immutable producing scope/epoch stays
unchanged. Current executing intent/runtime plus this current-epoch grant may
account an old effect after explicit protected-recovery release; it grants no
source/output access. A missing legacy attachment does not invent restore
authority; an already matching producing epoch retains its existing allowance.
The grant survives ordinary restart with the finite intents, while a changed
auth epoch revokes it. Completing the last intent or abandoning the campaign
removes it atomically. No schema migration or new retry ledger is introduced.

## Schema revision 18 generation ownership (TASK-006-04)

Migration 18 appends exact validated generation declarations without changing
historical migrations: generation_intents keyed by existing job_id, drafts keyed
by draft_id, revisioned draft_history, and workflow_selections keyed by the
existing workflow_binding/principal. Intent/draft/history content_set_id foreign
keys and their indices retain candidate inventory, including non-file entries.
Each typed draft keeps original project/document/base, linked job, private
allocated IDs, current candidate/revision, optional metadata and publication or
failure facts. Prior snapshots retain their complete candidate ownership.

Generation intent freezes source selection, requested target/base, publication
intent, operation/request identity, exact owned skill and optional pinned prior
contexts. Its private SourceReadIDs records only retained snapshot reads actually
selected under the captured effective mode/mapping. A capturing source selection
similarly records private _retained_read_ids before adoption; public requested_selection
omits that internal field. Both use existing JSON storage, without a new author
field, relation or schema revision. Original request/receipt identity is unchanged. The existing job/provider ledgers still own execution, finite dispatch,
ACK, result/uncertainty, retry and restore; no second queue is added. Complete
saved output uses the existing result object and pins. Imported candidate
preparation is separately recorded so restart/continue can consume it without
Execute; ready draft replacement must match candidate identity before an old
job can apply anything.

Prompt/context admission adopts ready exact objects with existing job_input
pins, without creating source job_inputs rows. Executing attempts and restored
orphans retain their typed owned input pins. Existing terminal-job/settled-attempt
and conflict/unresolved-orphan predicates govern release; source input rows and
observations remain independent. Abandon/discard/purge ownership checks include
generation intent, drafts and draft_history before any destructive batch, not
only at the final foreign-key delete. Workflow selection stores remembered
resource choices, not credentials or a base to reuse for new operations.

The existing scoped_credentials.scope JSON additionally retains a private,
immutable mutation_project_id captured by the protected issuer. The public
CredentialScope/action union and SQLite declarations are unchanged. Private
session projection carries this fact only for server-side fresh admission;
API session/scope responses and provider frames do not serialize it. Current
selection and the captured mutation project must both match a fresh output or
source-capture project. Rows without this fact remain usable for their existing
read/receipt/accepted-work semantics but cannot acquire fresh mutation authority
from remembered defaults. Import jobs now capture credential_scope just as seal
jobs do, so accepted publication checks the actual captured/current binding
instead of an absent scope. No new queue, table or retry mechanism is added.

Generation transport extends the typed attempt audit with skill version/hash,
admitted/discovered skill paths, fresh output directory, prompt/context exact
identities and matching-frame ACK flags. Native public-final SHA/size and the
service-derived captured directory ZIP remain distinct. These private audit
paths are absent from public draft/status projections. Backups/restore preserve
typed facts without inventing newer input observations or authority.

## Schema revision 19 saved questions (TASK-006-05)

Migration 19 adds `questions` and `answers` with their exact validated indices;
all earlier migration declarations and checksums remain unchanged. A question
binds its owner/project/document/version and unique existing job, while its
immutable value retains the original operation/request identity, exact nullable
page/region, artifact query/fragment, text descriptor, adopted display context,
explanation reference, resolved source mode, requested selection and actual
retained-read ancestry. The columns and value must agree. No second provider
queue or operation ledger is introduced.

An answer binds one question and actual producing attempt. Its immutable value
contains final/partial/error state, complete body descriptor, source citations,
separate explanation references and limitations. `(attempt_id,state)` is unique.
A partial/error record cannot replace the latest completed final answer. The
prepared answer retains its exact output object and identity before application;
saved continuation applies that object without another Execute. Retry refuses a
prepared answer before changing attempt/job/authorization state.

Question text, full display context and explanation input use existing object
pins independently of project-source input rows. The accepted job adopts those
pins before transient context can be abandoned; executing/restored orphan work
retains its typed input dependencies. Saved answers own their complete body
objects. Trash hides current records and streams, Restore exposes the same
record under current authority, and purge detaches question/answer ownership
before parent deletion. Whole backup/protected restore and explicitly selected
question export preserve the adopted objects and original audit facts. None of
these operations fabricate provider observations or change personal Review.

## Schema revision 20 scan inventory and deterministic structure checks (TASK-008-01)

Migration 20 (`internal/adapters/sqlite/scan_schema.go`, wired by
`internal/adapters/sqlite/schema.go`) adds `scan_runs`,
`scan_captured_versions`, `job_items`, and `findings` with indexes
`scan_runs_by_scope_hash`, `scan_captured_versions_by_job_doc`,
`job_items_by_job_state_ordinal`, `job_items_by_document`,
`job_items_by_version`, `job_items_by_repair_job`, `findings_by_job_order`,
`findings_by_version_status`, `findings_by_project_status`, and
`findings_by_fingerprint`; all earlier migrations and checksums remain
unchanged. `scan_runs` binds the existing `jobs(job_id)` row (`kind = 'scan'`,
whose `jobs.scope` JSON stores the captured `credential_scope` projected into
`ScanRun`) to the admitting `principal_id`, `auth_kind`, principal-bound
`scope_hash`, nullable primary `target_set_id`, captured `requested_scope`,
narrowed `effective_policy` snapshot, `config_revision`,
`permissions_revisions`, worker `stage` (`inventory`, `inspect`, `repair`,
`complete`), batch enumeration cursor (`enum_version_ordinal`, `enum_after_path`,
`enum_after_region`, `enumeration_complete`, `next_item_ordinal`), first-claim
`started_at` and `elapsed_ms`, immutable `denominator`, and current `report`.

`scan_captured_versions` stores the ordered version roster captured at admission
(`ORDER BY document_id COLLATE BINARY`), including excluded document rows
(`exclusion_reason` such as `document_trashed`, `no_published_version`, or
`authority_denied`; archived documents remain eligible and are not excluded),
per-version source selection (`selected_mode`, `selection_source`,
`target_set_id`, `requested_selection`, `targets_json`), and per-version
incremental enumeration cursors (`enum_state`, `enum_after_path`,
`enum_after_region`, `entry_after_path`, `source_after_idx`,
`materialized_units`, `entries_checked`). `job_items` materializes `document`,
`version`, `page`, `region`, `entry`, `source`, and `index` units in bounded
64-unit batches (`state` in `pending`, `running`, `completed`, `skipped`,
`blocked`, `failed`, `cancelled`). Entry inspection verifies stored `file`
objects, `directory` rows, and `symlink` rows (`content_hash IS NULL` with
preserved `exact_link_text` under `domain.InventoryLine`, without reading an
empty object hash or following external/dangling link targets). `findings`
stores deterministic (`backlink_index_repair`) and later semantic maintenance
findings with `evidence_fingerprint`, `config_revision` (projected as
`captured_config_revision`), `permissions_revision`, `revision` (from which
`"finding-{id}-r{revision}"` `etag` is computed on read), and explicit `status`
(`proposed`, `applied`, `rejected`, `stale`, `blocked`, `undone`).

When a document moves to Trash or is manually purged, the lifecycle transaction
transitions unfinished `job_items` (`document_trashed` / `document_purged`),
marks `proposed` findings `stale` or `blocked` as the path dictates, records
`incomplete_reference_repairs` (`rescan_recommended = true`) if a linked
`derived_index` (`references`) repair child is interrupted, and refreshes
`scan_runs.report`. Manual purge scrubs private paths, region IDs, source
selections, and finding payloads (`{"purged":true}`) while retaining minimal
opaque identifiers and immutable denominator counts per the permanent minimal
reservation policy, and preserves healthy sibling documents, versions, and
`source_targets` in multi-target scans. Shared scope intersection is centralized
in `domain.RestrictCredentialScope` across authentication and SQLite
job/operation/event visibility guards. See
[scan inventory evidence](testing/scan-inventory.md).

## Schema revision 21 maintenance intents and candidate discovery (TASK-008-02)

Migration 21 (`internal/adapters/sqlite/scan_schema.go`, wired by
`internal/adapters/sqlite/schema.go`) advances `SchemaRevision` from 20 to 21 by
adding `maintenance_intents(job_id TEXT PRIMARY KEY REFERENCES jobs(job_id), scan_job_id TEXT NOT NULL REFERENCES jobs(job_id), item_id TEXT NOT NULL, value BLOB NOT NULL CHECK(json_valid(value)))`
and index `maintenance_intents_by_scan_item(scan_job_id, item_id)`.
`maintenance_source_contexts` is a query-local CTE in
`internal/adapters/sqlite/source_capture.go`, not a persisted view or migration
object. It derives version dependencies from invocation explanations,
`$.source_read_ids` and `$.captures`. All earlier migrations and checksums
(1–20) remain unchanged.

Each `maintenance_intents` row binds one child provider job
(`jobs.kind = 'maintenance'`) to its parent `scan` job (`scan_job_id`) and
unit `job_items` row (`item_id`). Its JSON `value` (`domain.MaintenanceIntent`)
freezes the focal project/document/version/page/region coordinates, the
`CandidateShortlist` (`K <= 6`), per-step capture outcomes (`captures`), exact
`source_read_ids`, multi-version/multi-project `read_bindings`
(`MaintenanceReadBinding`) and explicit `limitations`. The optional
`prepared_output_json` holds the validated `PreparedMaintenancePayload`
(envelope, input digest, verified dependencies/evidence and partial-source
unit/version facts), with `prepared_attempt_id` identifying its attempt.
`PrepareMaintenance` prepares this result before `ApplyMaintenance` settles the
parent item. Object GC, backup/restore orphan accounting
(`provider_restore.go`, `copy_restore.go`; the restored-scan query uses existing
`rowid,job_id` rather than a nonexistent creation-time column, `B10`), Trash, and manual purge
(`lifecycle.go`, `purge.go`, including `$.origin_unit` and `evidence_json`
version fencing/redaction, `B03`, `B07`) include `maintenance_intents` and
`findings` so active execution pins, retained source snapshots, and candidate
references are protected during execution and scrubbed (`{"purged":true}`) or
deleted on manual target purge without leaking denied or trashed targets.

## Schema revision 22 scoped freshness (TASK-008-03)

Migration 22 adds `assessment_trigger_resolutions(trigger_id PRIMARY KEY,
assessment_id)` with foreign keys to the existing triggers and assessments,
plus version/time and exact-unit lookup indexes. Earlier migration checksums
remain unchanged. The existing assessment and coverage tables now have an
actual guarded producer. A published contribution contains the complete fixed
membership of its own exact scope and target; a proposed finding is not one.
Latest matching contributions retain their original assessment/coverage IDs.
Only fully assessed matching units clear covered triggers at the same target.
The latest availability-blocked attempt exception retains prior discrepancy,
coverage and trigger history while reporting current inability to verify.

Freshness maintenance intents carry their unit coverage set and the full-scope
fixed set separately. Each child assesses one actual page/region/version unit;
the full set projects these contributions without storing an invented aggregate
assessment. Source capture C2 and observation C retain separate target IDs and
triggers. Scan-owned temporary source pins protect the shared exact capture
between children and are released by ordinary parent completion/cancellation.
Manual purge removes trigger resolutions before their referenced records.
Provider attempts, delivered-input observations and saved results remain in
their existing ledgers; semantic publication does not manufacture those facts.
See [scoped freshness evidence](testing/region-freshness.md).

## Management records (TASK-008-04, schema revision 23)

Separate relation/topic tables store typed exact anchors, semantic key, immutable
AI origin, rationale/evidence/uncertainty, finding/job provenance, captured and
applied configuration, permission generation, decision actor and revision.
`management_effect_history` retains metadata revisions;
`finding_effects` retains every finding-to-effect ID/kind/revision binding.
Active semantic-key unique indexes prevent duplicate effects across findings,
runs and concurrent children. Reuse changes no existing effect actor/revision.
Findings remain proposals; prior proposed rows are not migrated into effects.
The repository revision seam preserves origin and immutable fields; Task05 owns
its guarded owner-facing decision/undo service and all-linked reversal rules.

Current reads check focal, endpoint and evidence authority. An inaccessible,
Trash or purged dependency leaves only a nondisclosing unavailable relation for
an authorized focal scope. Purge removes target/evidence payload, the old semantic
digest, history and finding linkage, retaining the closed minimal focal marker
in the existing relation table; focal purge removes it. Topic dependencies still
require full visibility. New topic effects require canonical explanation_text or
source_verified basis; legacy missing basis remains unknown in read projections
without changing stored semantic history. Lifecycle impact, coherent backup and
protected restore include these records and minimal markers. Optional decisions export stays unavailable. Payload, authored
references and Review stay independent. See [management evidence](testing/management-overlays.md).

## Owner decision memory (TASK-008-05, schema revision 24)

The additive management_decisions and management_decision_history tables retain
current scoped choice and immutable revisions, algorithm/material digest, exact
version dependencies, owner actor/time and previous linked application refs.
finding_decision_bases fixes each proposal's original exact semantic-key owner
revision (kind, source/target anchors and topic/mechanism), not a sum over every
decision mentioning the same version. Current decision material may contain all
actually reversed active linked applications; immutable original effect provenance
does not replace their material. New manual effects retain distinct capture and
final applied configuration revisions and an owner-approved finding reason;
unchanged canonical-effect reuse retains its original provenance.
management_link_states fixes current active status and finding revision
beside finding_effects. No synthetic legacy owner choice is backfilled.
Old migration bytes/DDL remain unchanged. Strict known-table, historical RF06/
SC10 and empty-table oracles include all four new tables.

Scan requested_scope durably carries origin finding/revision and captured owner
semantic-key revisions. Final admission and candidate application compare the
relevant exact decision revision; unrelated keys never impose a blanket
version/category ban. All actual dependency project permission generations
are compared independently in the final application transaction. Undo,
Topic PUT and owner decision/history/link/receipt writes are atomic; historical
terminal records remain separate from current effect state. Coherent backups
include the new tables; purge removes dependent decision/history/bases/current
links and scrubs origin/scope associations. Inert unavailable markers cannot be
reaccepted. See [finding decision evidence](testing/finding-decisions.md).
