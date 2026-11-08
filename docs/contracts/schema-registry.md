# Shared wire schemas, states and routes

## Provider resume addition (TASK-006-01)

The selected machine definition `job_resume` is the closed object
`{decision,expected_attempt_generation}`. Decision is `reconcile_only`, `continue`
or `retry`; expected_attempt_generation is a positive integer. The mutation
requires the current job If-Match and Idempotency-Key, preserves cancellation
intent and cannot turn a status lookup into dispatch. Unknown fields fail.

Owner status for a provider job whose target is hidden retains its job identity,
state, revision, generation, cancellation and normalized error. Scope, result,
permission map and parent/upload references are withheld. Its opaque job ETag
still identifies the operational resource; it grants no payload access. Only
reconcile-only mutation and its receipt use that limited recovery projection.

**Normative v1 registry; selected initial freeze recorded by [G-CONTRACT](../testing/contract-freeze.md).** This Markdown registry owns semantic meanings and delegates selected typed fields, enums, defaults and error mappings to the single [machine contract](../../tests/contracts/schema.json), with [executable behavioral fixtures](../../tests/contracts/README.md). The same validator serves package, API/config examples and fixture client/models; no parallel client type definition is authoritative. Unselected future responses remain provisional. These files are not an executed migration or evidence of an implemented production capability. Runtime `schema_version: "1.0"` and API `/api/v1` identify the selected evidence-backed baseline; documentation baseline 1.6.2 incorporates the Grok contract reconciliation and D-10 private-renderer-port approval. Shared enums stay here; [import and transfer workflow](import-workflow.md) is the delegated normative definition of purpose/target fields, action authority, import requests, multi-request key mapping, seal cancellation and list/detail shapes. Normal author JavaScript and one remembered owner-session model remain unchanged.

## Identity and common fields

| Name | Wire rule |
| --- | --- |
| Service IDs | Opaque UUID-form strings for library, project, document, version, job, question, answer, finding, draft, plan, operation, upload, target-set, coverage-set, workflow-binding, read, verification and provider-attempt identities. A document sequence is display-only. |
| Metadata-local IDs | Optional sources, evidence and references have nonempty Unicode string IDs, unique within their own collection in one version. Resolve using `(version_id, local_id)`; different versions may reuse a local ID. They are not mandatory author boilerplate. |
| Page identity | `(version_id, page_path)`; page_path is the exact normalized relative UTF-8 path. Preserve case and Unicode code points; no ASCII/length/depth rule or automatic case-fold/rename. |
| Region identity | `(version_id, page_path, region_id)`; nonempty Unicode ID unique within that page. Optional declarations and runtime-discovered anchors may coexist; missing anchors remain unresolved without rejecting the directory. Cross-version/page correspondence is explicit. |
| Context reference | Opaque context_ref for streamed immutable JSON/text context, scoped to owner, version and optional page/render context. It is a data handle, not a credential; saved notes/questions adopt the referenced object under their own lifetime pins. A closed fresh-proof mode on the context GET (request `Haeryong-Context-Binding` pair, body-less `304` with strong ETag and `Haeryong-Context-Verified: fresh` marker, `412` mismatch) proves the current originating session, persisted tuple and digest for ready/echo; the ordinary recovery read stays session-independent. |
| File inventory | Cursor/stream resource of exact paths, entry kinds, file bytes/digests and optional media/preview metadata. A list page or transport chunk size is not a total file/content limit. |
| Time | UTC RFC 3339 strings; client display/schedule timezone does not change stored instants. |
| Mutable resource | `id`, integer `revision >= 1`, and opaque strong `etag`. The API and clients use the same resource identity. |
| Immutable resource | Stable ID plus declared digest(s); no mutation revision implying published bytes can be edited. |
| Collection item | Carries its own `revision` and `etag` when mutable. A list's ETag is never the validator for an individual item. |
| List response | `data.items`, nullable opaque `data.next_cursor`, `request_id`; stable order and filters follow the HTTP contract. |
| Source target set | Service-assigned `target_set_id`, actual `captured_at` and immutable `targets[]` of repository/object-format/full revision or snapshot identity, with exact `requested_selection` recorded separately. Only a real source-target capture creates this record; an imported declaration is not a checked target. A moving branch label is never the captured revision. |
| Captured read | Daemon-issued read_id, target_set_id, exact repository/revision or snapshot identity, source location, bytes_sha256 of actual captured bounded bytes, config_revision, permissions_revision and optional retained-object/pin reference. Record project-source versus explanation-context basis. A read_id alone proves neither provider delivery nor semantic claim support. |
| Provider input observation | A producing attempt records {read_id, input_origin, observed_at}. daemon_supplied requires actual delivery of those bytes by the adapter; native_observed requires supported exact-byte native-read telemetry. A model-written path, HEAD value or post-hoc re-read is not an input observation. |
| Citation target verification | Daemon-issued verification_id and outcome, exact declared source/location, nullable target_set_id/bytes_sha256, checked_at, nullable verified_at, config_revision/permissions_revision and optional retained-object reference. A successful captured/matched record has actual target/bytes and verified_at. It records a later check, never provider input observation or rewritten provenance. |
| Source/config revision | config_revision identifies the effective settings snapshot. permissions_revision is a project-scoped source/disclosure/output-authority generation, advanced only by access-affecting changes; it is not a synonym for config_revision. Reads/attempts retain both relevant values and recheck current restrictions. |
| Operation receipt | `operation_id`, `principal_id`, method, canonical target/route, `request_fingerprint`, state, result resource reference, original status and original result metadata. The HTTP Idempotency-Key equals the operation_id of that request's receipt. For a composite CLI command, --operation-id selects only the final domain request; preparation uses separate stored keys under the import workflow. Raw credentials are excluded. |

The owner_id is the stable owner principal_id across device sessions. A remembered managed workflow has a server-allocated workflow_binding_id and stable principal_id, separate from its revocable credential/session; rotating a token does not create a new receipt namespace. Reusing that explicit binding retains identity; a different remembered workflow allocates a new binding. Do not hash the scope/project set to infer identity. Each issued credential captures its own currently permitted scope, and later binding changes cannot expand it. Owner-authenticated local CLI/IPC issues/revokes these credentials as described by configuration. Local bootstrap records the owner principal with actor bootstrap. Scheduled daemon work records the originating owner/policy and a system actor on its job/audit; it does not introduce another human authority tier. Physical table layout is an implementation choice.

Clients echo an ETag exactly in `If-Match`; they do not construct it from a numeric revision or reuse another resource's validator. An absent personal state is represented by `exists: false`, its default visible value, `revision: null`, `etag: null` and `required_precondition: "if_none_match_star"`. The first write uses `If-None-Match: *`. An existing record whose value is the default still has `exists: true` and a real ETag. Every personal response carries `page_position_url` as a mandatory key: for the reading-position resource it is the once-encoded canonical page link or explicit null for a whole-version pointer, never lending the pointer validator; the other two families always carry it as null so the three closed response shapes stay family-stable. `required_precondition` appears exactly when `exists` is false. Renderer content responses percent-encode `Haeryong-Page-Path` and `Haeryong-Resolved-Path` exactly once, like the management file handler; receivers decode each value with exactly one QueryUnescape.


The selected absent personal values are exact, not omitted fields or defaults
inferred from a missing record:

| Resource | Absent value |
| --- | --- |
| reader-preference | {"default_page":null} |
| reading-position | {"page_path":null,"page_query":"","page_fragment":"","opened":false,"first_opened_at":null,"last_opened_at":null} |
| page-position | {"page_query":"","page_fragment":"","position":{}} |

In all three absent responses both revision and etag are null,
required_precondition is if_none_match_star and page_position_url is null.
A stored default value remains an existing resource with its actual validator.


## Enum registry

| Field | Values |
| --- | --- |
| personal_review.state | `unreviewed`, `reviewing`, `reviewed` |
| personal_review.action | `start`, `complete`, `reopen`, `clear` |
| document.lifecycle | `active`, `archived`, `trash`, `purged` |
| project.lifecycle | `active`, `archived`; registry categorization only, with no implicit document/source cascade |
| assessment.state | `not_assessed`, `reassessment_needed`, `aligned_within_scope`, `outdated_against_target`, `unable_to_verify` |
| finding.status | `proposed`, `applied`, `rejected`, `stale`, `blocked`, `undone` |
| finding.kind | `backlink_index_repair`, `related_explanation`, `topic_assignment`, `freshness_assessment`, `new_version_proposal`, `new_document_proposal` |
| finding.origin | `deterministic`, `ai` |
| topic_assignment.decision | `accepted`, `rejected`; accepted means the assignment is applied, whether by owner decision or configured auto policy. origin/decision actor records which; a rejected assignment cannot be pinned |
| pairing.state | `pending`, `approved`, `rejected`, `expired`; one approved redemption is single-use |
| session.state | `active`, `expired`, `revoked`; state is derived from current session expiry/revocation |
| draft.status | `preparing`, `ready`, `published`, `rejected`, `failed` |
| draft.action | `reject`, `replace` |
| finding.decision | `approve`, `reject` |
| job.state | `queued`, `running`, `paused`, `interrupted`, `reconciling`, `cancelling`, `completed`, `failed`, `cancelled` |
| job.kind | `upload_seal`, `import`, `generation`, `question`, `scan`, `lifecycle`, `backup`, `export`, `derived_index` |
| job.resume.decision | `reconcile_only`, `continue`, `retry`; request includes expected_attempt_generation and the job ETag |
| job_item.state | `pending`, `running`, `completed`, `skipped`, `blocked`, `failed`, `cancelled`; skipped/blocked items retain their exact coverage reason |
| provider_attempt.dispatch_state | `prepared`, `dispatching`, `running`, `result_saved`, `failed`, `cancelled`, `outcome_unknown` |
| operation_receipt.state | `accepted` is a durable reservation with no definitive response; `completed` is a committed definitive response, including a 202 job-creation response; `failed` is a definitive recorded failure; `reconciling` requires recovery of an uncertain effect. The referenced job's outcome is separate. |
| source_mode | `pinned`, `current` |
| citation.basis | `observed_input`, `posthoc_verified` |
| provider_input.input_origin | `daemon_supplied`, `native_observed`; null in a posthoc_verified citation |
| scope.kind / coverage.unit_kind | `version`, `page`, `region`; typed fields with the distinct scope/coverage rules below |
| coverage_unit.outcome | `assessed`, `partial`, `blocked`, `not_checked`; required exclusions retain their reason and remain in the denominator |
| source_mapping.kind | `git`, `files`; configured source selections, not captured provenance |
| source.kind | `git`, `snapshot`; committed Git captures use git; captures from files mappings and explicit working-copy snapshots use snapshot identity |
| source_intent | `historical`, `moving_target`, or null when no source intent was supplied |
| source_basis | `unverified`, `evidence_recorded`; recording evidence never certifies all content as factually correct |
| reference.resolution | `resolved`, `unresolved`, `unavailable`, `tombstoned`; disclose target metadata only within current visibility |
| file.kind | `file`, `directory`, `symlink`; unknown file extensions remain valid payload |
| upload.purpose | `result`, `context`, `body`, `metadata`; scope/target-bound transfer staging does not grant publication authority |
| upload.input_mode | `directory`, `archive`, `file`, `object`; purpose=result accepts directory/archive/file, while context/body/metadata require object. Invalid purpose/mode/target combinations fail before staging. |
| upload.state | `open`, `sealing`, `sealed`, `abandoned` |
| content_object.state | `staged`, `ready`, `deleting`; GC reservation and reference adoption are coordinated |
| object_pin.state | `active`, `released`; elapsed time alone does not release active/unknown work |
| plan.state | `open`, `committed`, `expired`; current-revision applicability is checked separately |
| lifecycle_plan.operation | `archive`, `trash`, `restore`, `purge`; restore applies the recorded archived/trash transition |
| workflow.scope | `project.read`, `source.read`, `version.read`, `package.export`, `context.write`, `import.publish`, `generation.request`, `draft.write`, `draft.publish`, `scan.request`, `job.read`, `job.cancel`; only scopes derived from remembered authorized work are issued |
| file.preview | `html`, `browser`, `text`, `download`, `unavailable`; actual implemented preview, not an import-format allowlist |
| source.retention_mode | `reference_only`, `selected_snapshot` |
| evidence.capture_status | `verified`, `reference_only`, `unavailable`; immutable author-declared capture information, never trusted as a service verification result, access grant or claim truth |
| verification.outcome | `captured`, `matched`, `mismatch`, `unavailable`; separate service check result defined below |
| source.availability | `available`, `unavailable`, `denied`; current access state, distinct from provenance and freshness |
| source.bytes_origin | `retained_snapshot`, `approved_source`, or null |
| viewer_profile | `html_js_v1`; normal author HTML/JavaScript, DOM, modules, libraries, WASM, workers and network under ordinary browser rules |
| maintenance.schedule.mode | `manual`, `interval`, `calendar` |
| maintenance.scope | `selected_projects`, `all_allowed_projects` |
| maintenance.depth | `change_focused`, `full_scope`, `deep_semantic` |
| maintenance.missed_runs | `coalesce_one`; no catch-up series in v1 |
| automatic action | `disabled`, `propose`, `auto` where the configuration matrix allows it |
| content_generation | `disabled`, `propose`, `draft`; no automatic publication value |

Unknown state/action/authority enum values fail the affected operation safely. Unknown request/config fields are rejected unless the contract names an extensible author/display-context field. Clients may ignore additive optional non-authority response fields; they cannot infer permission or a successful state from unknown authority-bearing fields. Authored file bytes and explicitly extensible display JSON are not a hidden application-config schema. Reusing a word in different typed enums does not make those enums interchangeable. A proposed finding requiring owner approval remains `status: proposed` with `approval_required: true` and an actionable reason; `needs_approval` is not a status.

## Personal state and lifecycle

Review PUT body is `{action, state}`; the service verifies the corresponding transition, owner, exact version, lifecycle and precondition. Unknown actions fail INVALID_ENUM; a mismatched action/state pair fails INVALID_REQUEST and an undefined transition fails INVALID_TRANSITION. A new request checks its individual precondition even if the requested value would be a no-op; stale remains 412. Accepted no-change requests return confirmed state without another review transition or revision increment. Matching receipt replay precedes the original now-stale precondition and does not append history again.

| Action | Allowed state change |
| --- | --- |
| start | unreviewed -> reviewing; already reviewing is a no-op |
| complete | unreviewed or reviewing -> reviewed; already reviewed is a no-op |
| reopen | reviewed -> reviewing |
| clear | any state -> unreviewed; already unreviewed is a no-op |

Review, Revisit, notes, reading-position, page-position and reader-preference writes are allowed for active/archived documents. An authorized request in trash/purged state fails INVALID_TRANSITION with details.lifecycle; unknown/unauthorized targets still use nondisclosing NOT_FOUND. Completion sets the current `reviewed_at`; reopen/clear clears that current stamp while preserving prior completion in `review_events`. Opening and position saves never change review state. The reviewed version is the complete result-directory snapshot, including all its pages; opening one HTML page cannot complete the entire result. Page/region anchors and the last opened page remain separately recorded. Revisit is one independent owner flag per document, available regardless of review status.

| Lifecycle action | Allowed transition and result |
| --- | --- |
| archive | active -> archived; already archived is a no-op |
| restore from archive | archived -> active |
| trash | active or archived -> trash; record `previous_lifecycle` and `trashed_at`; already trash is a no-op |
| restore from trash | trash -> recorded previous active/archived state, preserving identities and personal records |
| purge | trash -> purged under a current authorized plan or confirmed timed-purge policy |
| any action after purge | No resurrection. Authorized historical reads may return a minimal tombstone. |

Lifecycle is document-level in v1. Purging a document covers its eligible versions/derivatives; independent version purge and project-wide cascading deletion are not v1 operations. An intentional new explanation uses a new document identity. No-op plan replay returns the original receipt, not a newly authorized destructive action.

Drafts transition preparing -> ready/failed/rejected and ready -> published/rejected. PATCH draft action reject is allowed while preparing/ready, requires its ETag and fences any linked preparation job before a late result can replace the decision. PATCH action replace is ready-only and adopts a sealed result whose draft_id, project and document intent match that exact draft under [purpose/target authority](import-workflow.md#purpose-target-and-authority). Optional metadata_ref matches the draft_replace action, draft and consuming operation. Replacement retains draft/base identity and prior candidate history while creating a new revision; no upload is rebound. Publication remains a separate explicit operation. Published drafts refer to their immutable publication and cannot be edited. Retry of failed/rejected work creates a new linked draft/job. Publication with a stale base_version_id returns VERSION_CONFLICT and leaves the draft ready for explicit resolution.

## Source and assessment views

Ordinary directory/file import does not require source/evidence metadata or semantic claim classification. Missing metadata yields source_basis unverified and source_intent null; all selected content remains viewable. Optional supplied metadata is validated for structural consistency and any actual checks performed. Malformed explicitly supplied metadata returns field-specific diagnostics while preserving sealed payload for a corrected or metadata-free new operation; absent optional metadata is not a rejection. Managed source-grounded generation/answers still require evidence for material source claims; missing evidence does not make a claim conceptual or verified. Each present source entry carries local `id`, kind, `retention_mode`, optional `release_label` and exact Git identity (`repo_id`, `object_format`, `exact_revision`) or `snapshot_sha256`. `selected_snapshot` requires an inventoried `asset_path` and matching digest. `reference_only` may record a digest without bytes. Evidence binds a source local ID, path/location and `capture_status`; service validation does not trust an author's `verified` label as proof of actual bytes.

Ordinary links remain payload bytes. Optional authored library references retain exact document/version/page/region targets and an actual resolution state. Unresolved/absent/inaccessible targets do not reject the directory or fabricate validated backlinks, and no hidden target detail is returned. When an exact target becomes eligible, derived resolution can change without rewriting the immutable authored reference.

Runtime evidence responses add `availability`, `bytes_origin`, nullable reason code and the actual revision/digest served. Permission-denied details are returned only where existence is already authorized; otherwise use nondisclosing 404. A hash, release label or historical verified capture does not imply bytes are currently available. Runtime availability changes never rewrite published provenance.

Explicit imported-evidence checking uses POST /versions/{id}/source-evidence-verifications?evidence_id={evidence_id} under current version/source authority. The optional expected_bytes_sha256 and expected_excerpt (or mutually exclusive immutable expected_excerpt_ref) are expectations about the exact bounded target bytes: compare SHA-256 and exact UTF-8 excerpt bytes without whitespace/Unicode normalization, with all supplied expectations required to match. No expected digest/excerpt is mandatory. With no expectation, outcome captured records exact target resolution/capture only; matched means supplied expectations matched; mismatch retains the failed comparison; unavailable retains its actual reason. Persist checked_at for every check and verified_at only for captured/matched. The immutable author's capture_status is never upgraded or overwritten. GET source-evidence composes a separate latest service_verification and its history link; history exposes check results without starting another read or AI request. Invalid request fields fail normally, and the recorded outcome never proves source truth or what the producing agent read. The HTTP contract owns the exact operation/response shape. Verification acquires existing/temporary object pins before reading, verifies the same pinned bytes, rechecks current authority through completion and preserves permitted record references under the source-retention rules.

Every project-source citation in a completed AI answer/assessment has {basis, read_id, verification_id, input_origin, target_set_id, exact source identity/location, bytes_sha256} and optional package_evidence_ref {version_id, evidence_id}. For basis observed_input, read_id is required, verification_id is null, and input_origin is daemon_supplied or native_observed with a matching actual observation in the producing attempt. For basis posthoc_verified, verification_id is required and read_id/input_origin are null; the daemon checked the cited target bytes later under current authority using a captured or matched verification outcome. It never implies that the provider received those bytes, backdates a capture or changes the original generation provenance. The verification record contains verified_at. A native read that was not observed cannot be converted into an observed_input citation by re-reading its path afterward. Source-only capture without a producing-attempt input observation also cannot establish observed_input.

Both variants require actual captured target bytes and current applicable scope; path shape, author labels and digests without captured bytes are insufficient. A captured post-hoc record with no supplied expectation proves only exact-target resolution/capture; matched additionally proves that the supplied byte expectation matched. Mismatch/unavailable checks cannot be promoted to service-verified citations. An optional package_evidence_ref is valid only when the immutable package record matches the same source identity/location and bytes. Current-source B never reinterprets package evidence at A or appends evidence to published A metadata. Responses compose current availability, bytes_origin and nullable authorized reason, with an excerpt only when those bytes remain readable. Neither record promises permanent byte retention. Target-byte validity, provider-input observation and semantic support for the claim are distinct facts; an assessment also needs truthful scope/coverage. Missing requested pinned/current source is unavailable/unable_to_verify, never an empty verified target set. Clearly labelled explanation-only commentary may cite supplied explanation bytes without claiming project source was checked. Unverified author citations/raw imported files remain preserved and viewable; this output validation does not become an import gate.

An explanation-only reference is a separate explanation_reference object {version_id,page_path,region_id,bytes_sha256}, with nullable page_path/region_id (region requires page). A page reference hashes that page's exact stored bytes; a whole-version reference hashes its canonical payload inventory. This object is not a project-source citation and has no observed_input/posthoc_verified basis or fabricated source target. Answers may return explanation_references separately from their project-source citations, with an explicit unable-to-verify limitation when requested code evidence is unavailable.

A document list summary includes latest_version_id, its personal review state, nullable last_reviewed_version_id, updated_since_review, Revisit, lifecycle and freshness. latest_version_id is the canonical wire name for the latest published pointer; the former display_version_id label is not a second field. Last reviewed means the most recent explicit completion event that has not been cleared/reopened for that version; historical events remain inspectable. Updated since review is true only when that reviewed version's publication sequence precedes latest.

freshness has {source_intent, target_set_id, captured_at, scope, status, coverage, assessed_at, reassessment_required}. captured_at is the selected target set's actual capture time, not a claim that a moving branch is still at that revision. scope is {kind:version|page|region,page_path:null|string,region_id:null|string}: version has both paths null, page has page_path and null region_id, and region has both. coverage is {coverage_set_id:null|ID,unit_kind:version|page|region,eligible:integer,assessed:integer,complete:boolean,units_url:null|string}; counts derive from persisted unit records, with 0 <= assessed <= eligible. assessed_at is nullable.

An explicit target_set_id selects that exact captured target. Otherwise choose the latest captured_at (then target_set_id for a stable tie break) among applicable service-captured sets for the same exact requested_selection: project/source IDs, source mode and requested revision/branch/snapshot selection. An explicit source_mode wins. Without it, historical source_intent uses pinned; otherwise use the remembered project source_mode_default, defaulting to pinned. moving_target describes intended use and never by itself forces current mode, performs a read or overrides explicit pinned selection. Do not select an unrelated latest assessment. A status/list read never resolves a branch, reads source or starts AI. With no actual capture for that selection, return target_set_id/captured_at/coverage_set_id null, status not_assessed and complete false. Import-only source declarations do not create captured target sets. A later explicit source check can create the first captured set without retroactively claiming import-time verification.

For observation-backed current selection, conditionally retain the latest matching
committed attempt A (created_at, then run_id) and latest matching committed valid
capture C separately (newest capture run by created_at, then run_id). Match exact
project, effective source IDs and selection family; A supplies status and
availability-level unit reasons
(missing_source, source_not_registered, source_denied), while C supplies target,
captured_at, coverage set and trigger visibility. If no C exists, preserve the
no-capture target/time nulls above, but the attempted denominator and reasons may
come from A's persisted coverage set. All-unavailable attempts still persist their
requested_selection/scope and reasons. Explicit targets bypass this search and
select exactly their committed capture and owning run scope; a scope mismatch
returns applicable null. Pinned/historical selections choose only exact pinned
verification captures (latest captured_at, then target_set_id), without HEAD
triggers or observation coverage; the two capture families never substitute for
each other. Dangling/uncommitted captures and attempts are ineligible.

Capture one immutable coverage set for the exact version, target and requested scope before assessment. It records coverage_set_id, version_id, target_set_id, scope, unit_kind, captured_at and the complete non-overlapping required tuple set {kind,page_path,region_id}. A version scope deterministically requires every discovered HTML page as a page unit; a version without HTML has one whole-version unit. A page scope defaults to its single page unit, or uses an explicitly selected fixed set of that page's region units. A region scope uses that one exact region. Missing/duplicate/runtime anchors never fabricate regions or impose a region requirement. Supporting files and unread content stay in the unit's stated limitations. Changing required membership/kind produces a new set; outcomes/exclusions never shrink the captured denominator.

Persist each assessment contribution as {assessment_id,target_set_id,kind,page_path,region_id,outcome,discrepancy,reason}, linked to its coverage_set_id. reason is nullable only when no limitation applies. outcome is assessed, partial, blocked or not_checked; a required exclusion is not_checked with its actual reason. discrepancy records an evidenced discrepancy even in a partial unit. The required-unit view includes every tuple and its latest applicable published assessment linkage, or assessment_id null/outcome not_checked when none exists. Store or stream these records; counts alone cannot support aggregation. Broader assessments contribute only where their recorded required tuples and full coverage explicitly match, never by summing overlapping page/region counts.

For each tuple in the selected coverage set, choose the newest applicable published contribution by assessment time then ID. Contributions may originate in another coverage set only when version_id, target_set_id, unit kind, exact page/region identity and the unit's defined full scope match. A later page-only assessment can therefore update that page in a whole-version page-unit summary without double-counting; a region-only check cannot claim the entire page was assessed. Keep the source assessment/coverage-set linkage visible. A changed target_set_id requires a new assessment or an explicit evidence-backed revalidation, not automatic reuse. Unapproved candidates remain findings. assessed counts only fully assessed required units. complete requires eligible > 0, an available exact target, every required unit assessed and no unrecorded exclusion. An evidenced discrepancy yields outdated_against_target; otherwise an outstanding relevant change trigger yields reassessment_needed; otherwise a blocked required unit yields unable_to_verify; complete coverage without discrepancy yields aligned_within_scope; remaining partial/no coverage yields not_assessed. An explicitly attempted unavailable source check records its reason/unable_to_verify, never a successful empty check.

When the latest matching observation run is availability-blocked, the status is
unable_to_verify even though an earlier capture supplies the target and coverage;
reassessment_required remains derived from the selected capture's uncleared
triggers. Only C.target_set_id and its exact anchors determine trigger visibility;
never union triggers from other same-selection captures. Version scope counts all
triggers of C; page scope counts its region triggers; region scope counts only
that page/region tuple. Thus an unavailable A
can preserve C's trigger, while a later new capture without a direct change does
not inherit an old target's trigger. The old explicit target still exposes it.

reassessment_required records an outstanding relevant structural-change trigger for this target/scope. It is independent of the selected status: it can remain true while an evidenced discrepancy makes status outdated_against_target. A new applicable assessment clears only triggers that its target/scope and observation cover. Setting the flag authorizes neither an AI call nor a semantic verdict. Display historical intent/target explicitly and never infer outdatedness merely from a newer unrelated commit.

## Reader metadata and transfer lifetimes

The immutable version.default_page is separate from the mutable owner/version reader-preference resource {default_page: null|string, revision, etag}. A non-null preference must name an existing HTML page in that version; null clears the preference without editing payload or generated version metadata. Initial-page precedence is reader preference, immutable valid default_page, root index.html, sole HTML page, then the service directory listing. No HTML or region is required. Each explicit page link still opens its exact requested page rather than a preference.

reading_positions has one owner/version last-opened pointer/observation resource with its own ETag; PUT accepts {page_path, page_query, page_fragment, opened}. It does not own per-page progress. page_positions has one owner/version/page_path position resource with an independent ETag; its GET/PUT uses the exact page path and PUT accepts {page_query, page_fragment, position}. Page-position writes do not silently change the last-opened pointer. A reading-position response may link the selected page-position resource, but its ETag cannot validate that resource. Clients coalesce page progress and serialize saves for each resource, using the last acknowledged ETag and one operation ID per submitted save, not per scroll event. Save the pointer on meaningful navigation/open separately. A 412 retains local unsaved position, fetches the current resource and reconciles the newer local navigation intent before another save; it never blindly replays a stale event or silently overwrites another device. opened is a boolean observation; timestamps are service-assigned. Neither write changes Review. Absent preference/position resources use the common exists/create-precondition shape. Saved note/question navigation context contains page_query/page_fragment and a nullable context_ref, with timestamps and original anchor history as specified by the HTTP resource; storage may use typed columns or an immutable referenced record.

Upload states use open, sealing, sealed and abandoned. [Seal cancellation and upload coupling](import-workflow.md#cancelling-sealing-versus-abandoning-an-upload) is normative: verification cancel preserves bytes and returns a non-abandoned upload to open only after its worker is stopped; abandonment prevents reuse and releases only safe references. An asynchronous seal is job.kind upload_seal; final publication is a separate import job. [Completion-key recovery](import-workflow.md#composite-operation-ids-and-recovery) specifies identical and new keys at the same active checkpoint.

Exact upload targets and scope-by-purpose authority are defined once in [purpose, target and authority](import-workflow.md#purpose-target-and-authority). Result uploads distinguish publication from a draft-specific consumer. Context JSON targets bind explicit version/page/render fields; the [query normalization](import-workflow.md#context-binding-transport-normalization) maps omitted page/render query parameters to null and requires exact tuple equality. Body and metadata variants bind a named consuming operation/action. Preparation never creates publication authority. Owner recovery after channel loss retains the original persisted binding. [Import request rules](import-workflow.md#client-association-and-import-request) require explicit document_id and distinguish pre-transfer client base capture from low-level API omission; handlers never consult hidden client/workflow defaults.

Owner recovery remains available after originating credential loss under current target authority; workflows remain limited to their own authorized uploads. The [upload list and detail shapes](import-workflow.md#upload-list-and-detail-shapes) are normative: lists contain summaries and detail_url, while checkpoints, errors and detailed range links belong to detail. No original bearer is required for owner recovery.

sealed describes successful sealing, not indefinite byte availability. reusable is derived from sealed/non-abandoned state, current target authority and all original bytes being durably available and pinnable. After staging ownership transfer, durable adopted content may still permit reuse; if bytes have actually become unavailable, reusable is false with an explicit reason while state remains sealed. Reuse reestablishes pins before adoption and never resurrects abandoned handles. Abandon fences new writes/adoption and releases only safe staging references; completed adoptions and active/pending/unknown-outcome pins survive. Already abandoned returns confirmed state under current authority.

Unadopted context/body/metadata objects and abandoned staging become collectible only after no durable reference, active operation or unknown-outcome/recovery pin requires them. A lost connection, closed page, expired renderer-delivery lease or elapsed age is not abandonment. Coordinate adoption and GC reservation in short transactions. No automatic staging TTL, total transfer deadline or size/count quota is introduced. Cleanup requires proven abandonment or completed ownership transfer and safe reference/pin checks.

## Operation identity and preconditions

A composite CLI/UI command does not reuse one key across different K routes. Its user-facing ID belongs to its final domain request; preparation calls use distinct automatically persisted keys as defined by [composite operation IDs](import-workflow.md#composite-operation-ids-and-recovery). The caller journal exists before the final server receipt and preserves the original pre-transfer target/base and dispatch phase.

For each K request, its HTTP Idempotency-Key and receipt/result operation_id are the same unquoted lowercase canonical UUID string (8-4-4-4-12 hexadecimal digits, no braces). CLI --operation-id selects the final domain request only; each preparation request uses its own separately persisted UUID. This Haeryong header is not a claim of compatibility with a Structured Field idempotency draft. Missing a required key fails 428 PRECONDITION_REQUIRED with details.missing="Idempotency-Key"; quoted, malformed or multiple values fail 400 INVALID_REQUEST. Service IDs remain opaque; clients echo the chosen value exactly. A body field claiming this request's own receipt identity must not conflict with its Idempotency-Key. This restriction does not apply to the declared target.operation_id of a body/metadata upload: that field identifies the future consuming operation and may differ from the upload request's key. It is semantic target data under [purpose and consumer binding](import-workflow.md#purpose-target-and-authority), not a second claim to this request's receipt. Changing that target with the same upload-create key is OPERATION_CONFLICT. Unknown request fields remain subject to the existing schema rules. Receipt uniqueness is `(principal_id, operation_id)` across all route families. The request fingerprint covers the HTTP method, canonical route with actual target IDs, validated body and original semantic preconditions, including If-Match/If-None-Match, base version and captured plan/config identity where supplied. Bearer secrets, transport request IDs and header ordering are excluded. For a sealed upload, identity uses normalized metadata/payload digests and optional submitted-metadata digest plus semantic import fields, not ZIP compression/order, transfer chunking or upload-resource identity alone. On the first valid import reservation, persist the original request and normalization_context under the [import request rules](import-workflow.md#client-association-and-import-request). Require document_id as an existing ID or explicit null; only null allocates a document ID. Allocate version_id only when omitted, take an omitted project_id only from the sealed upload target, and retain the explicit pre-transfer base or the once-captured low-level omitted base, generated created_at, permitted defaults and normalizer/schema version. Normalization never repairs a missing document_id or consults remembered targets. When that operation ID already exists, load its saved normalization context before deriving normalized metadata or comparing the final fingerprint. Compare the incoming original caller fields, supplied-metadata/payload digests and original semantic preconditions first, retaining which fields the caller omitted; only unchanged omitted generated fields reuse the saved values. A different sealed upload containing the same bytes/metadata is the same semantic input, while changed title, selection name used for a default, explicit IDs, metadata, base or preconditions still returns OPERATION_CONFLICT. Never assign a fresh timestamp/default or overwrite changed caller input with stored values merely to make a retry match. Publication-time observations are result metadata, not fresh caller-input entropy. Clients need not author a manifest or calculate digests. Upload chunks use their natural upload/path/offset identity and verified bytes; changed bytes at an already accepted offset conflict. G-CONTRACT fixtures fix deterministic serialization; clients reuse the original logical request and need not calculate the fingerprint.

Authenticate the current owner or scoped workflow credential and check current scope/lifecycle visibility first. Then look up the receipt: an identical completed/failed request returns the original safe definitive response before re-evaluating its now-stale precondition. While the identical request has only an accepted durable reservation, a concurrent duplicate returns 202 with data {operation_id,state:"accepted",operation_url} and request_id, without a second execution. Those pending data fields are stable; request_id may differ. original_status/result_ref remain null until a definitive response commits. Poll the operation URL; pending status is not evidence of an unknown external outcome. A reconciling receipt instead exposes OPERATION_RECONCILIATION_REQUIRED or its recorded external uncertainty and never authorizes redispatch. A changed method/target/body/semantic precondition with the same operation ID returns OPERATION_CONFLICT. New work checks current preconditions before committing. Replaying a receipt never bypasses a newly revoked permission or re-serves a now-hidden body.

The supplied `operation_id` is a new-operation intent, not an inference that a lost receipt proves nothing ran. Restore advances the library runtime generation; stale clients enter reconciliation on generation change. Unknown outcomes and missing post-snapshot receipts require an explicit bounded retry decision. Normal first-use creation with a newly generated ID remains supported.

Plan IDs are natural commit identities: one plan has at most one committed result. Lifecycle/config plan commits carry plan_id, expected revision and ordinary owner impact confirmation (confirmed: true). Without a separate Idempotency-Key, operation_id is plan_id. With a key, that key and plan_id resolve to the same single committed outcome; neither a second key nor a keyless replay creates another effect. An alias already bound to an unrelated method/target/request returns OPERATION_CONFLICT; it can never overwrite an existing principal-wide receipt. Retain enough receipt linkage for authenticated GET operations reconciliation through the accepted operation identity. Current authorization precedes identical committed-plan replay, which returns the original result before treating its old revision or elapsed expiry as a new commit error. An uncommitted expired plan fails STALE_PLAN; changed commit input conflicts. A plan is an exact change description, not an authentication credential or separate owner authority tier.

Pairing request/redemption and session revocation reconcile through their natural request/session resources and do not mint separate operation receipts. Polling-secret redemption is intentionally single-use and never an idempotent way to mint a second session. O preview/validation requests retain a receipt when an Idempotency-Key is supplied; without it, any returned preview/plan/resource ID is its lookup identity and repetition may create another preview, never apply a mutation. The HTTP operation matrix owns endpoint mapping. The operations response exposes operation_id, state, nullable result_ref/original_status/job_id and safe original result metadata. accepted reserves work only. Committing a durable job and its response atomically sets the receipt completed with original_status 202 and its operation_id/job_id; synchronous results similarly commit their definitive status. Later job completion/failure never rewrites that original creation response. failed means a definitive operation failure; reconciling means unresolved recovery, not a merely pending concurrent request.

## Error registry

HTTP uses `error: {code, message, retryable, details}` and `request_id`. Details contain only authorized metadata. A retryable error never grants a new provider attempt or destructive action. A pending reservation's stable 202 response is not OPERATION_RECONCILIATION_REQUIRED. CLI exit 0 is success; other exits below are shared across commands.

| Codes | HTTP | CLI exit | Recovery |
| --- | --- | --- | --- |
| INVALID_REQUEST, UNSUPPORTED_SCHEMA, INVALID_ENUM, INVALID_STATE_SCHEMA | 400 | 2 | Correct request/compatible client |
| AUTH_REQUIRED, SESSION_EXPIRED, SESSION_REVOKED | 401 | 3 | Reauthenticate; clear protected client state |
| FORBIDDEN, ORIGIN_REJECTED | 403 | 3 | Use the permitted operation and configured origin; no automatic scope expansion |
| NOT_FOUND | 404 | 3 | Unknown/unauthorized target is indistinguishable |
| VERSION_CONFLICT, OPERATION_CONFLICT, STALE_PLAN, INVALID_TRANSITION, INPUT_CHANGED, DESTINATION_CONFLICT | 409 | 4 | Fetch confirmed state, re-capture changed input or resolve the exact destination conflict without silently overwriting |
| IMMUTABLE_VERSION_CONFLICT | 409 | 4 | Existing explicit version_id has different immutable content/metadata; do not overwrite it or reuse that ID for changed content. Choose a new version identity for deliberate new content. |
| PROVIDER_OUTCOME_UNKNOWN, OPERATION_RECONCILIATION_REQUIRED | 409 | 6 | Reconcile; no automatic new external attempt |
| SOURCE_UNAVAILABLE, REVISION_UNAVAILABLE | 409 | 5 | Missing required source for a new action; evidence views and explicit verification-result records may instead expose typed unavailable outcomes |
| TARGET_GONE | 410 | 4 | Only an authorized historical-reference context |
| PRECONDITION_FAILED / PRECONDITION_REQUIRED | 412 / 428 | 4 | Obtain the individual current ETag; retain unsaved edit |
| RANGE_NOT_SATISFIABLE | 416 | 2 | Request a valid range of the same immutable object; no cross-version stitching |
| PACKAGE_INVALID, UNSAFE_PATH, HASH_MISMATCH, INVALID_EVIDENCE, VIEWER_PROFILE_UNSUPPORTED | 422 | 2 | Correct corrupt input, malformed explicitly supplied metadata or actual addressing/compatibility issue; stream/cursor additional issues |
| RESOURCE_EXHAUSTED, TRANSFER_INTERRUPTED, FILE_ACCESS_ERROR | 503 | 5 | Show the affected resource/path and retained staging; recover actual disk/memory/I/O conditions or resume transfer |
| RATE_LIMITED, BUDGET_EXHAUSTED, PROVIDER_QUOTA_EXHAUSTED | 429 | 5 | Respect actual limit/retry guidance; distinguish local budget from provider quota |
| FEATURE_NOT_READY, PROVIDER_UNAVAILABLE, SERVICE_RECOVERING, CONTENT_INTEGRITY_ERROR | 503 | 5 | Capability/recovery or verified content required; do not fabricate success |

The error code, not HTTP status alone, selects the CLI recovery class. Distinct 409 reasons may intentionally map to conflict, unavailable or uncertain-outcome exits. Stored/library incompatibility or migration recovery reports SERVICE_RECOVERING with reason restore|migration|schema_too_new, readiness false and the recorded schema revision; an older client cannot migrate the database. Request UNSUPPORTED_SCHEMA remains a 400 for an incompatible request contract.

There is no Haeryong SIZE_LIMIT error for HTML bytes, package bytes, entry count, directory depth or serialized author state. A proxy/provider/platform limit is reported as that component's actual constraint, with streaming/chunking or an explicit incomplete/unavailable outcome where possible. Finite transport buffers and cursor page sizes do not truncate the whole input or output. Missing optional regions/references use unresolved data states, not package rejection. Exact-path lookup followed by more than one Unicode canonical-equivalent candidate fails INVALID_REQUEST with details.reason ambiguous_path; it never case-folds or renames a stored entry.

A hash mismatch in a submitted package is a 422 package-validation issue. A stored object whose expected bytes fail verification produces CONTENT_INTEGRITY_ERROR and no render/source/provider payload; do not misreport this as a successful read or automatically trust replacement bytes.

Adapter status quota_unavailable means quota telemetry is unknown, not that execution is forbidden or allowance unlimited. quota_exhausted maps to PROVIDER_QUOTA_EXHAUSTED. auth_required, unsupported_version and provider_error map to PROVIDER_UNAVAILABLE with their distinct provider reason; they are not an owner-session 401. source_unavailable maps to SOURCE_UNAVAILABLE/REVISION_UNAVAILABLE as appropriate. paused is a job state/reason with explicit resume requirements, not a fabricated successful answer. An accepted job reports these reasons in its persisted outcome rather than changing its original 202 creation receipt.

## Browser route registry

These are content-free trusted-shell routes, not API routes. Metadata loads only after the bearer-session check. A version that does not belong to the route's document returns nondisclosing NOT_FOUND; never replace it with latest. Remembered local and remote owner sessions use the same model; the browser origin selects its own stored credential. API routing refers to this registry; UI screen documentation describes these same routes.

| Route | Meaning |
| --- | --- |
| / | Home |
| /connect | Pair/reconnect and validated return route |
| /library | Search/filter library; Archive is /library?lifecycle=archived |
| /projects and /projects/{project_id} | Project listing and scoped library |
| /projects/{project_id}/sources | Safe approved-source status |
| /documents/{document_id}/versions | Version history |
| /documents/{document_id}/versions/{version_id} | Exact result-directory reader; optional page, page_query and page_fragment query parameters, and #region={region_id} as defined below |
| /maintenance and /maintenance/jobs/{job_id} | Run list and exact report |
| /trash | Owner-visible trash and lifecycle actions |
| /settings and /settings/{section} | Settings; empty section resolves to general |

The page parameter is the percent-encoded exact page_path. page_query is the separately percent-encoded artifact query string without its leading ?, and page_fragment is the separately percent-encoded artifact fragment without its leading #. The host fragment #region={percent-encoded region_id} identifies a Haeryong page-local anchor without consuming the artifact's own fragment. Encode/decode each value once; a literal ? or # within a filename is encoded as part of page, never reinterpreted as navigation syntax. Preserve all supplied navigation context through login, reload, copied links and back/forward. No page parameter opens the configured/conventional default or service directory listing; it never silently selects latest version. Large authored application state uses context_ref rather than an enormous URL. context_ref/body_ref/metadata_ref inputs can be built through purpose-bound resumable uploads and adopted by their authorized operation; a direct-stream body is not the only supported transfer path.

Settings sections are general, connection, sessions, projects, ai, maintenance, storage, retention, backup and about. Validate logical path IDs, registered sections, known shell filter/query keys and the decoded page's containment in the selected version. Artifact query/fragment values are data for that page, not shell routes or host management commands. Reject external/protocol-relative URLs, credentials, malformed shell control characters and paths outside this registry as login returns, while preserving representable percent-encoded filename characters. Ordinary browser URLs may contain the trusted loopback transport address when remote exposure is disabled; label copied URLs local-only then. Canonical identity is logical document/version plus exact page/region where used, never the hostname or port.

### Deep-link and Versions consumption (TASK-004-05)

Copy link constructs only logical document/version/page/query/fragment/region identity.
Resolve its transport base from authenticated enabled `network.external_base_url`,
otherwise the client's trusted service origin (the attached daemon origin in Wails).
Label disabled-remote links local-only. No credential, renderer handoff, attachment or
reference-return state enters that URL. Clipboard denial leaves selectable text.
A host fragment is absent or one nonempty `region`; unknown/duplicate fields and
malformed escaping are invalid login returns. Decode values once, preserving literal
percent/plus/Unicode filename and artifact navigation data. A region is nonempty
Unicode, including percent-encoded control data; literal raw shell controls remain
invalid. Automatic Connect entry
captures the validated route after definitive credential loss; each verified return
replaces that Connect entry with a fresh Reader context. Checking/offline is not logout.
Stamped history entries retain their context; hash-only unstamped navigation retains
the live frame. Reference-return state remains owner/runtime-bound history data.

S05 alone additionally accepts optional UUID `from_version`/`to_version`. Both must
belong to the route document before comparison titles, inventories or metadata appear.
Absent `to_version` freezes latest on entry; absent `from_version` uses that target's
last still-personally-Reviewed version when features.review is enabled, otherwise the
immediate sequence predecessor, or itself when none exists. Reader passes its exact
selected version as `from_version`. With no published versions there is no pair.
Resolved defaults replace the current entry's URL with both exact IDs; they add no
history entry. Same-authority degraded/verified changes retain the mounted S05 state.
An invalid/foreign explicit choice is unavailable, never a default fallback. Import
publication refreshes history/latest notice without replacing a chosen pair. Open
latest is an explicit fresh lookup and drops the previous version's region selection.
TASK-005-01 adds the last-Reviewed default and exact per-version Review reads behind
features.review. The original TASK-004-05 selection rules were additive shell consumers;
the Review wire/data additions are defined below. Existing coverage consumption keeps
`attempt_run_id` on its first, next and retried cursor requests unchanged.

## Implemented local-control identity (TASK-002-01)

This protected OS-user protocol is separate from the proposed `/api/v1` owner/workflow API and grants no browser or content authority. `api` is exactly `local-control-v1`. Authenticated identity and protected discovery share `library_id`, `profile_id`, `instance_id` (canonical lowercase UUIDs), `api`, `endpoint` (exact loopback HTTP origin), `control_ready` (boolean) and `storage` (`ready` boolean and `state` string). TASK-002-01 originally used `pending_task_002_02` with `ready=false`. [TASK-002-02](../testing/storage-foundation.md) now publishes `ready=true`, `state=ready` only after durable profile/DB bootstrap, identity/health and referenced-object checks. Explicit initialization itself remains profile format 2/storage_state pending with no DB. This readiness does not assert owner/browser sessions or product API availability.

`GET /health/live` returns only `{"live":true}` without private metadata. `GET /local/v1/identity` and `POST /local/v1/stop` require the protected profile key's per-request HMAC proof and return a nonce-bound response proof. Identity verification compares the library, profile, API, endpoint and current instance before Stop. The key never travels in HTTP, argv or output. These local routes reject browser Origin headers and alternate Host values. Remote ingress remains unimplemented. TASK-002-03 now implements pairing and bearer sessions through the separate current owner-session boundary below; [the authenticated-session evidence](../testing/authenticated-sessions.md) records its checks and limits. [The lifecycle evidence](../testing/profile-lifecycle.md) owns local-control reproduction and platform limits.

## Selected current owner-session shapes (TASK-002-03)

The machine definitions `pairing_create`, `pairing_poll`, `pairing_decision`, `pairing`, `pairing_created`, `device_session`, `pairing_list` and `device_session_list` select the current headless authentication fields. Pairing create is `{device_label}`; poll is `{polling_secret}`; approve/reject are `{code,origin}` with a required single canonical Idempotency-Key and strong item If-Match. The ETag binds pairing ID and revision. No expected_state, admin or confirmation field is added. Rejection applies to pending or approved-but-unredeemed, and approved polling consumes the internal single-use flag without inventing another public pairing state. Device scope is currently `{kind:"owner"}`; workflow issuance remains feature-owned.

Closed control JSON rejects unknown, duplicate, missing and null required fields. The modest 16 KiB authentication-control envelope, 128-codepoint device label, five active pending requests, reliable-client creation rate of two/minute and two-second polling interval are control protections, not content quotas. Bearers/secret hashes are internal fields excluded from DTO JSON. Safe saved approve/reject request metadata uses a pairing-bound code verifier rather than a raw code. HTTP details.missing remains a string naming the missing header. Public trusted shell/liveness disclose no private library metadata; private API and SSE delivery require current session authority. [Authentication evidence](../testing/authenticated-sessions.md) records actual checks and compatibility limits.

## Selected current upload shapes (TASK-002-04)

Upload summary/detail now require independent positive integer revision and a strong etag, alongside the frozen purpose/target/checkpoint fields. Detail may include transport_entry for the generated single opaque transfer entry; omitted selection_name is represented as null. These are additive selections on the previously provisional production response shape; strict clients must accept the selected fields. No content-size/count/depth restriction or additional authored manifest is introduced. The exact machine forms live in tests/contracts/schema.json, with [actual backend evidence](../testing/atomic-import.md). The application metadata-schema.json is the selected author-metadata definition subset of that machine authority, not a second schema owner.

## Selected current query and configuration shapes (TASK-002-05)

These additive selections make previously provisional production read shapes concrete. Existing author metadata, request fields, operation identities, enums, schema1.0 and APIv1 are unchanged. Strict response fixtures now include these selected fields; adding a validator does not add a K or If-Match obligation to natural operations. The single machine definitions are `query_project`, `query_document`, `query_version`, `query_file`, their `*_list` forms, `render_bootstrap`, `configuration_view` and `staging_summary` in tests/contracts/schema.json. Production HTTP/CLI/SQLite regressions complement the pure selected-shape fixture; the fixture does not prove production execution.

| View | Selected fields |
| --- | --- |
| Project | project_id, name, lifecycle, revision, permissions_revision, updated_at, etag |
| Document | document_id, project_id, title, lifecycle, nullable latest_version_id, revision, updated_at, versions_url, etag |
| Version | version_id, document_id, sequence, nullable base_version_id, title_snapshot, nullable default_page, viewer_profile, source_basis, payload_sha256, manifest_sha256, nullable supplied_metadata_sha256, manifest_url, nullable supplied_metadata_url, files_url, render_bootstrap_url, etag |
| File entry | path, kind, decimal byte_size, preview, file_url, etag; regular files add bytes_sha256/media_type, links add link_target, directories add files_url |
| Render bootstrap | version_id, viewer_profile, nullable default_page/selected_page/requested_page/navigation_path/selected_file_url, files_url, delivery (authenticated_attachment, loopback_renderer_origin or private_renderer_origin), renderer_available, render_sessions_url, renderer_construction, optional nullable renderer_unavailable_reason (renderer_remote_ingress for unsupported remote ingress); machine shape in the selected contract schema |
| Configuration | config_revision, etag, configuration (the selected effective config shape) |
| Status staging | decimal staging_bytes, nonnegative pending_upload_count, pending_uploads_url |

Lists retain data.items and nullable data.next_cursor. Version manifests hold full generated authored metadata; compact version summaries do not omit metadata from that stream. Independent mutable revision is distinct from timestamps, immutable sequences and auth/project access generations. Current config plans expose plan_id, base_config_revision, expires_at, scope, normalized_changes, affected_permissions_revisions and impact; internal effective policy/principal ownership are not credential-bearing public fields.

## Selected current evidence-presentation shapes (TASK-003-04)

Evidence item reads support the fixed
`/versions/{id}/source-evidence?evidence_id=...` alias with exactly one nonempty,
once-decoded ID and the same authority as the legacy path. Both SDKs' history
operation and all returned `verifications_url` locators use
`/versions/{id}/source-evidence-verifications?evidence_id=...` with optional
`cursor`, preserving dot, literal-percent, slash and Unicode identities. This
history alias now implements GET and the canonical lossless POST verification
operation under TASK-004-01. HEAD remains unknown (404); legacy path forms remain.
These are additive implementations of the selected contract within schema 1.0.
The observation route and project-source projection are additive provisional
selections; assessment producers remain owned by later Tasks.

The assessments consumer query uses exactly `target_set_id`, `scope_kind`
(`version`|`page`|`region`), `page_path`, `region_id`, `cursor` and `limit`; the coverage
consumer query uses exactly `coverage_set_id`, optional `attempt_run_id`, `cursor`
and `limit`. Each assessments
response page is the ordinary list envelope plus one typed `applicable` member:
the service-selected applicable freshness view, or explicit null when the
response carries none. Consumers never infer the summary from history items; a
null member is an honest unknown, and the recorded coverage summary and status
badge are displayed but re-derived from the enumerated fixed required-unit
records that match the coverage set's selected `unit_kind` and scope structure.

For source-observation coverage, conditionally permit a non-null coverage_set_id
with null target_set_id when an actual attempt captured no source. Its persisted
required units remain not_checked with actual availability reasons; this is not
an empty successful target or a semantic assessment. If A differs from C's owning
run, coverage.units_url additionally carries attempt_run_id=A.run_id. Without
that parameter, coverage returns exactly the set's stored units. With it, retain
C's denominator/membership/target and overlay only A's availability reasons on
units citing its failed sources; other units retain their captured reasons.
A must be a committed run of the same version/project/requested_selection as the
coverage set's owning run; otherwise return 404 with details.attempt_run_id.
Cursors bind both IDs and permission generations. This never rewrites stored
units. Clients forward attempt_run_id on every first page, next page and retry.
Default selection does not search older runs to obtain a matching scope: expose
C's (or no-C A's) coverage only when its stored scope matches the requested scope
exactly, otherwise use null coverage with zero counts. A requested page/region
that cannot project onto the version, or a version without any evidence and
without any captures/observations, returns applicable null. These differ from
the honest no-capture composition for a valid selection.

| View | Selected fields |
| --- | --- |
| Assessments page | `data.items` (history: `assessment_id`, `version_id`, `scope {kind,page_path,region_id}`, `target_set_id`, optional `state`), `data.next_cursor`, nullable `data.applicable` |
| Applicable freshness | `source_intent`, `target_set_id`, `captured_at`, `scope {kind,page_path,region_id}`, `status`, `coverage {coverage_set_id,unit_kind,eligible,assessed,complete,units_url}`, `assessed_at`, `reassessment_required` |
| Coverage unit row | `kind`, `page_path`, `region_id`, `outcome`, `reason`, `discrepancy`, `assessment_id`, `target_set_id` |
| Verification history row | `verification_id`, `outcome`, `checked_at`, `verified_at`, `reason` when applicable |
| Evidence item view | `version_id`, `evidence_id`, `authored`, `source`, `availability`, `bytes_origin`, `reason`, `service_verification`, `verifications_url`, optional `excerpt {bytes_sha256,data_base64?}` |
| Supplied metadata stream | exact bytes plus the immutable object's strong ETag (`"sha256-<64 hex>"`) |

Evidence-item service truth is principal-specific: no source.read yields denied
with null reason/origin and no excerpt; readable digest-checked retained bytes
win over missing checkout observations. Absent retained bytes give
missing_retained_bytes, corrupt bytes retained_bytes_corrupt, both unavailable
without excerpts. Unmapped Git gives source_not_registered; recorded missing
source gives missing_source. Otherwise mapped Git is unavailable with reason
not_read_by_view, null origin and no excerpt: this view performs no new Git read,
and the UI labels it "확인 전" rather than declaring source failure. Current
service_verification is composed separately; authored capture_status never changes.
A successful authorized retained-byte read adds optional excerpt with the SHA-256
of the exact bounded bytes. data_base64 contains those bytes only when the bound
is at most 64 KiB and its encoding is nonempty; a larger bound (or empty byte
selection) has digest-only excerpt with data_base64 omitted, not truncated text.
Denied/unavailable/not_read_by_view views have no excerpt. This inline-view bound
is not a source/content size limit and never starts a new Git read.
`units_url` is consumed as data: the request is
rebuilt from typed fields only when it names the exact fixed local route for
the same version and coverage set; foreign or protocol-relative references are
rejected, and an absolute URL is accepted only on the current service origin.
Capture ID/time are recorded facts; an unknown or `not_assessed` view cannot
become aligned from them alone.

The supplied-metadata validator bridge is named per transport layer: the raw
HTTP `GET /versions/{id}/metadata?kind=supplied` carries exact bytes with the
ETag response header; the closed native operation `versionSuppliedMetadata`
returns `{stream_id, etag}` from the Go facade; the JSON native wire reifies
that handle into a lower-level `NativePort` result of `{body, cancel, etag}`
(final bytes delivered before the clean-EOF acknowledgement) which
`NativeHaeryongClient` narrows to the public `HaeryongClient` shape
`{etag, stream}`. Clients compare every supplied validator (that ETag, plus
`version.supplied_metadata_sha256` when present) over the fully received bytes
before parsing; with no usable validator the bytes are rejected as an
independent metadata error. The parsed supplied object keeps the author's
source intent on `version.source_intent`; region identity stays page-local
(`page_path` plus nonempty id); and local IDs are arbitrary nonempty Unicode
strings preserved exactly, with inherited object-key names read as absent.

## Selected current native onboarding shapes (TASK-003-05)

These are closed native transport operations, not additional public HTTP routes.
The trusted main frame names an operation; Go retains selection, polling secret
and bearer authority. The native `pair` request is `{device_label}` and its result
is `{pairing_id, code, expires_at}`. Native `pollPairing` accepts `{pairing_id}`
and returns `{state, session?}`; an approved result contains only
`session_id`, `principal_id`, `library_id` and `device_label` in that session.
The browser's existing full pairing responses and real polling-secret request
remain separate. A native client never forwards a browser polling-secret argument.

| Operation | Request | Result |
| --- | --- | --- |
| serviceDiscovery | `{}` | `{state, profile, endpoint?, library_id?, instance_id?, detail?}` |
| startDaemonOffer | `{}` | `{started, discovery}` using the same discovery shape |
| adoptPairing | `{pairing_id}` | `{adopted: true, durability: "confirmed"\|"unconfirmed"}` |
| abandonPairingAttempt | `{pairing_id}` | `{abandoned: true}` or the typed committed/failed outcome |

Discovery states are `verified`, `uninitialized`, `daemon_stopped`,
`discovery_corrupt`, `identity_mismatch` and `profile_error`. Starting is an
explicit offer only for `daemon_stopped`, preserving the exact default-profile
selection and absolute sibling binary; it never initializes or migrates a library.
Attachment requires the product API and signed library/endpoint binding, with
`NATIVE_NO_CREDENTIAL` distinguishing an unpaired boot from explicit logout.

The local attempt transitions from `pending` through `adopting` to `adopted`,
`failed`, `store_failed` or `store_uncertain`. Stop before commit prevents
activation. A committed attempt reports `NATIVE_PAIRING_ALREADY_ADOPTED`;
failed/uncertain adoption reports `NATIVE_PAIRING_ADOPT_FAILED`, rather than a
false successful Stop. Exact readback after a credential replacement error is
committed with unconfirmed durability. An indeterminate readback reports
`NATIVE_CREDENTIAL_STORE_UNCERTAIN` with no active candidate and no assertion
that the remembered store is empty. A known non-consuming poll rate limit
preserves the attempt and its real Retry-After; uncertain single-use redemption
requires a new pairing. See [the implementation record](../testing/desktop-companion.md)
for current verification bounds and remaining native delivery work.

## Selected current native filesystem shapes (TASK-003-05)

The M3 operations are closed native transport operations over opaque,
purpose/attachment/generation-bound handles; they add no public HTTP route and
no arbitrary JavaScript path/URL/filesystem proxy. Every handle resolves
relative names inside the owner-selected `os.Root`; source entries capture the
exact dev/ino, size, nanosecond mtime, kind and link text at enumeration, and
file reads re-verify that identity before and after each ranged read.

| Operation | Request | Result |
| --- | --- | --- |
| chooseImportSource | `{mode: directory\|archive\|file}` | `{handle, selection_name, mode, entries, limits}` or `{canceled: true}` |
| sourceEntries | `{handle, cursor?}` | `{items: [{path, kind, size?, link_target?}], next_cursor}` (local cursoring of the captured enumeration) |
| readSourceRange | `{handle, path, offset, length}` | `{bytes_b64}`; the 1MiB bound is chunk granularity |
| releaseSource | `{handle}` | idempotent same-era success |
| sourceVerifyStable | `{handle}` | `{stable}` plus `changed_path` when unstable |
| chooseDownloadDestination | `{suggested_name, mode: directory\|save-file}` | `{handle, label, exists}` (exact probe) or `{canceled: true}` |
| destOpen | `{handle, intent?}` | `{opened: true}`; intent carries `replace` and `expected {sha256, byte_size}` |
| destWrite | `{handle, bytes_b64}` | `{written}` |
| destClose | `{handle}` | `{published: true, durability: confirmed\|unconfirmed}` |
| destAbort | `{handle}` | `{aborted: true}`, idempotent; `{aborted: false, published: true}` after publication |

Typed failures stay closed: `NATIVE_HANDLE_STALE` (unknown/replaced era),
`NATIVE_SESSION_REPLACED` (a late picker result after its era or picker lease
was invalidated), `NATIVE_SOURCE_CHANGED` (captured-identity mismatch),
`NATIVE_DESTINATION_CONFLICT` (existing target under the default no-replace
publication), `NATIVE_DESTINATION_VERIFICATION_FAILED` (pre-publication
digest/size mismatch), `NATIVE_VALIDATOR_MISSING` (a file download whose
delivered representation carried no usable Repr-Digest),
`NATIVE_CLOSE_UNCERTAIN` (unobserved close), `NATIVE_CLEANUP_FAILED` (an
own-temp cleanup failure, disclosed with the exact surviving path even beside
a primary failure) and `NATIVE_PATH_INVALID` (unrepresentable names or link
targets, rejected before any JSON replacement).

The delivered file validator travels per transport layer exactly like the
supplied-metadata one: the raw file GET response carries `Repr-Digest` and
`Content-Length`; the native `fileStream` open returns
`{stream_id, validator?}` where a malformed or absent proof is omitted; the
JSON native wire reifies it beside the guarded stream; the shared
`PackageStream` exposes it as an optional member that the browser transport
never sets, and the download engine binds it to the writable attempt as the
pre-publication `expected`. Archive package streams never claim a validator;
their completion claim stays a clean end plus byte count.

## Selected source observation shapes (TASK-004-01)

The [HTTP delivery](http-api.md#source-observation-delivery-task-004-01) fixes
request/result and error shapes, including the five-field observation scope
versus frozen three-field applicable.scope. Machine fixture definitions live in
`tests/contracts/schema.json`; these are additive selections, with the new
observation route and project-source projection provisional. Metadata/config
schema versions remain 1.0.

Project-source items have source_id, kind (git/files), repo_id (nullable),
retention_mode, provider_ids, last_observed_revision, last_observed_at,
last_availability and last_observation_reason. All four last_* fields are null
before an observation. Time/availability/reason come from the latest per-source
observation-run row; revision comes from the latest row that actually captured
that source, even when a later attempt fails. Verification checks never count.
The array is sorted by source_id and cursor-bound to project permissions.

Observation source rows carry nullable reason and arrays changed_paths and
dependency_scope. Direct cited-path byte change records direct_source_change;
advanced revision with unchanged cited paths discloses
unexamined_dependency_scope, without a daemon-set semantic reassessment trigger.
A mode-only Git change with identical blob bytes has no changed cited byte
path. If its revision advances, classification is indirect with
unexamined_dependency_scope and no direct-change trigger. Unit reasons use
availability first (source_not_registered > missing_source >
missing_retained_bytes > retained_bytes_corrupt > source_denied),
then direct_source_change, then unexamined_dependency_scope, then
no_cited_evidence for a unit with no cited evidence; otherwise null.
Version scope enumerates every HTML page or one version unit with no HTML;
page/region scope uses its exact anchor. Region IDs stay page-local.
Every current unit is `{kind,page_path,region_id,outcome:"not_checked",reason,
discrepancy:null,assessment_id:null,target_set_id}`. Assessments items stay empty,
next_cursor null, assessed_at null, assessed 0 and complete false; observations
alone never prove aligned_within_scope or outdated_against_target.


## Selected static search foundation (TASK-004-02 M1/M2)

**Classification:** additive `job.kind=derived_index`; provisional selected search
shapes under runtime schema 1.0/API v1. M1/M2 supply fixtures and internal storage,
not a search route, CLI or enabled UI. `features.search=false` remains explicit.
The existing `workflow.scope` and author-metadata subset are unchanged.
At this TASK-004-02 boundary, `tests/contracts/schema.json` and the complete
embedded configuration schema were identical. The schema defines `search_query`,
`search_hit`, `search_filters`,
`search_page`, `search_response`, `search_index` and `search_index_reason` as closed
objects. The page requires `query`, `filters`, `items`, nullable `next_cursor`,
decimal-string `total_matches` and `index`. Every hit retains its exact immutable
project/document/version, sequence, kind, title/lifecycle, nullable page/region/source,
anchor/body states, match fields and nullable plain-text snippet.

`index` requires state (empty/pending/partial/complete/failed), decimal-string or
unknown-null versions_total/versions_complete/files_total/files_discovered/
html_total/html_complete/html_pending/html_failed/unsupported_bodies,
discovery_complete, limitations (`static_only`), and **reasons**, always an array.
Each reason is exactly `{code,version_count}`; version_count is a positive canonical
decimal string. Safe codes: `not_indexed`, `interrupted`, `cancelled`, `read_failed`,
`decode_failed`, `integrity_failed`, `resource_exhausted`, `storage_failed`,
`unsupported_body`, `failed_unknown`. No diagnostic, path or job identity belongs
in this projection. Later aggregation counts distinct eligible versions per code,
uniques/sorts codes by ASCII, and never requires job.read. Multiple failures of
one version count once per code; counts across codes need not sum to the denominator.
Unknown terminal failures map to failed_unknown; unsupported bodies are neutral
limitations, and a known child failure does not invent another unknown cause.

The pure query grammar permits empty q, whitespace AND clauses, and balanced ASCII
double-quoted literal phrases with doubled quotes inside. SQL/FTS operators remain
ordinary text. Raw decoded input and normalized semantic text each have a 4,096
Unicode-scalar control limit. Semantic length sums normalized literal clauses and
one separator per clause boundary; delimiters are syntax. Invalid UTF-8, controls
except tab/newline/carriage return, malformed phrases or expansion overflow fail
before SQL, without truncation. NFC/casefold changes search copies only.

Ranking compares five tiers, strongest first: (1) exact identity/title/path,
(2) literal title/region phrase, (3) literal path/topic/source phrase,
(4) all full components, then (5) substring. Within a tier, the descending
per-clause field score uses title=8, region=6, path=5, topic=4, source=4 and
body=1, counted once per clause/field. Repetition and hidden corpus frequency
add no score. For equal (tier, score), assign a one-based ordinal within each
exact (version_id, page_path) group in the existing tuple order, over eligible,
fully matched one-hit-per-unit results, before cursor and LIMIT. Counters reset
for every (tier, score) block. Compare ordinal ascending before the unchanged
project, document, sequence descending, kind, binary page_path, region_id and
source_id keys. This diversifies equal-strength evidence without collapsing
units or overriding strength. Cursor ordering revision 2 rejects pre-A1
bindings; strict decoding also rejects missing, zero or negative ordinals.

The fixed [worksheet](../testing/search-relevance-worksheet.md) owns predeclared
X09, calibration40, alias pair and approved sample targets. Fixture validation is
not execution of these searches. Worker wiring, query, transports and UI remain
subsequent milestones; no new resume route or scope is selected.

### Derived maintenance progress (TASK-004-02 M3)

This additive intermediate delivery uses the existing job list/show and cancel
operations, with owner-only visibility for derived_index. Its required job.result
is `{version_id,work_kind,stage,files_done,files_total,bytes_done,bytes_total,pages_done,pages_total,failed,checkpoint}`;
work_kind is search, stage is inventory/extract/commit/complete, counters are
nonnegative decimal strings and unknown totals are null. Files count inventoried
entries; bytes count static HTML input; pages count completed static HTML pages.
Checkpoint is an opaque hash. The same shape survives completion/failure/cancel.
`search.index_updated` has version resource identity and no body; existing
version.read with exact project/version anchors controls replay/live visibility.
The [jobs contract](jobs-events.md#delivered-derived-maintenance-task-004-02-m3)
owns terminal/restart/revision behavior. No new scope, resume or search route is
implemented here; search capability remains false.

M4 implements the selected search HTTP and CLI projections without changing the
selected schema or workflow actions. Search reads use existing `version.read`
and both exact project/version anchors, not `project.read` from ordinary lists.
The required reasons array is sorted by code, uses distinct selected-version
counts and maps unknown persisted failure classes to `failed_unknown`. Pending
bodies and quarantined indexes contribute no body candidates. The matched unit
identity remains `(version_id, kind, page_path, region_id, source_id)`; page and
region hits are independent. M5 delivers the typed `search(SearchRequest): Promise<SearchPage>` operation in
HTTP, the native adapter and the closed Go facade, plus Library presentation and
capability enablement. Library safe-return query keys include `q`, `project_id`,
`lifecycle`, `versions` and `kind`. Search All forwards `lifecycle=all` explicitly.
Requests preserve every supplied filter, cursor and page size; responses retain
required safe reasons and exact imported identities. There is no job-resume operation.

## Selected reference metadata foundation (TASK-004-03 M1)

Compatibility is **additive** within runtime schema 1.0 and API v1. The closed
`reference` metadata definition gains only optional `from`, `relation`,
`page_query`, `page_fragment`; the existing required id/document_id/version_id/
page_path/region_id and optional label/resolution remain accepted. Embedded
`internal/application/import/metadata-schema.json` definitions equal the
corresponding `tests/contracts/schema.json` definitions.

When present, `from` is a non-null closed object requiring a non-null relative
`page_path` and nullable nonempty `region_id`. Omission means version-level
provenance, without choosing a default page. Missing required nested keys,
unknown fields, malformed paths/IDs and explicit null for the new optional
fields are invalid. `relation` is free authored text, not a source-dependency
verdict. Navigation fields are raw strings; omission normalizes to empty strings.
They do not change immutable page identity. Neither an author-supplied resolution
nor a display label proves service resolution or authority.

The shared pure `internal/application/referencecodec` decoder, exposed by the
importer `DecodeReference` facade, validates these structures without querying a
target or requiring a supplied pages/regions inventory. The original reference
JSON remains unchanged. Missing/unavailable target or DOM anchors are deferred
resolution states, not structural metadata errors. Duplicate version-local
reference IDs remain invalid. The new contract fixtures are synthetic metadata
checks; they do not establish a delivered resolution API.

Migration 9 adds typed structural-reference repositories and Go admission for
`derived_work.work_kind=references`; it introduces no new job kind/scope or
runtime enqueue in M1. At that milestone `features.references` was absent, and routes, transports, jobs,
events and Reader delivery remained with later milestones. The accepted intended
read contract remains safe GET/HEAD, source-only cursors/events and eligibility
checks on both endpoints. M1 does not claim those unimplemented surfaces pass.

M4 now advertises the additive boolean `features.references=true` alongside
the implemented list/resolve operations and shared Reader delivery. Its absence
or false value still disables marked-link dispatch and interception in all
clients; it does not disable ordinary artifact href navigation. The closed
`reference.navigation {enabled:boolean}` message is bound to the current page
channel as specified in [the viewer protocol](viewer-protocol.md). No new
management/native passthrough or author-required metadata is introduced.

M1 also selects the provisional closed `reference_view`, exact/minimal-gone target,
materialization, page and response schemas for the later GET/HEAD consumers.
A resolved view requires an edge UUID, exact target and relative Reader URL;
unresolved has an explicit safe reason and no edge/route; unavailable masks target,
edge, label and route; tombstoned carries only minimal document/version identity
and no usable edge. `resolution_pending` distinguishes asynchronous edge work
from a pending DOM anchor. Region requires page on source and target. List state
is pending/ready/failed with its corresponding safe reason and nullable cursor.
Synthetic response fixtures test shape invariants, not implemented authorization,
lifecycle, resolution, events or HTTP endpoints.

M2 wires `derived_work.work_kind=references` into the existing `derived_index`
family in `internal/application/work/maintenance.go`. Its job result retains the
fixed derived progress keys, with `work_kind=references` and source-manifest
counters; terminal failure preserves committed progress. `references.updated`
contains only source version identity/materialization revision and no body.
Incoming edge changes emit no source event/revision. These additions select no
reference read route, CLI operation or capability; the M1 response shapes remain
provisional until their implementing milestone.


## Implemented reference projections (TASK-004-03 M3)

The selected closed `reference_from_view`, `reference_view`,
`reference_exact_target`, `reference_gone_target`, `reference_materialization_view`,
`reference_page`, `reference_response` and `reference_resolution_response`
definitions in `tests/contracts/schema.json` now describe implemented API
responses. Every definition embedded in the configuration and metadata schemas
equals its machine copy, as RF01 enforces by subset equality; the machine copy
additionally holds these reference response shapes.
The earlier M1/M2 delivery statements describe those milestone boundaries.

`ReferenceView` has exactly `id`, `from`, `relation`, `label`, `resolution`,
`reason`, `edge_id`, `target` and `reader_url`. `from` has nullable `page_path`
and `region_id`. Resolution is `resolved`, `unresolved`, `unavailable` or
`tombstoned`. Resolved requires a persisted UUID edge, authorized `ExactTarget`,
relative host Reader URL and null reason. Unresolved has a safe reason and no
edge; an authorized exact-page/version fallback route is allowed, without a
successful region selection. A bound source-context rejection can have null
target/route. Unavailable masks label/target/edge/route. Tombstoned exposes only
`GoneTarget` and no edge/route. Relation/from remain source-authored provenance.

`ExactTarget` contains exactly `document_id`, `version_id`, nullable `page_path`,
`region_id`, `requested_path`, `resolved_path`, and raw string `page_query` and
`page_fragment`. Requested spelling and resolved immutable inventory spelling
are distinct. `GoneTarget` contains only `document_id` and `version_id`.
Region requires page. Reader URLs use the existing document/exact-version route,
query-encoded page/artifact navigation, and host `#region` only for a validated
resolved region. A page-less reference stays page-less.

Reasons are null or `target_unavailable`, `page_missing`, `anchor_missing`,
`anchor_ambiguous`, `anchor_pending`, `resolution_pending`, `anchor_drift`,
`source_context_mismatch`, `ambiguous_reference_context` or `ambiguous_path`.
Explicit resolution of an ambiguous canonical path is 400; list keeps its typed
unresolved row. Collection materialization state is independent of these live
target states. List has no target-derived total or filter. Current source-only
cursor binding and D4 historical authority are defined in the HTTP contract.

## Backlinks and index recovery wire additions (TASK-004-04)

The additive provisional machine definitions in `tests/contracts/schema.json` are
`backlink_scope`, `backlink_endpoint`, `backlink_view`, `backlink_page`,
`backlink_response`, `index_validation`, `index_validation_response`,
`index_rebuild_request`, `index_rebuild` and `index_rebuild_response`.
All objects are closed. IDs are service UUIDs except immutable local reference IDs;
optional page/region/relation/label and cursor absences are explicit nulls. Region
requires its page, and Reader URLs are relative exact-version routes.

Backlink data is `{scope:{version_id,page_path,region_id},items,next_cursor,
counts:{edge_count,referring_document_count}}`. Each item has `edge_id`,
`reference_id`, `from`, `target`, `relation`, `label`, `reader_url`; endpoint fields
are document_id/version_id/page_path/region_id. Counts include only currently
eligible resolved canonical edges; distinct referring documents span referring
versions. Incoming order follows authored_edges_target, with NULL-first anchors.
The existing `reference_page` gains required live
`counts:{edge_count,referenced_document_count}`; target documents deduplicate across
versions and existing source-only declaration cursors remain unchanged.

Index validation returns version_id, kind search/references, state
consistent/inconsistent/incomplete, four decimal check counters and the closed safe
reason set in `index_validation` (including `anchor_prerequisite_pending` for
admitted pending anchor extraction). Missing required inventory-derived rows
are inconsistent; legitimate empty extraction and completed missing/ambiguous
anchors do not imply missing derived work. Reader rows/counts rejoin current
eligibility after counting; declaration cursors remain source-only.
Index rebuild accepts only kind search/references/all and returns version_id,
kind, job_ids and operation_id; committed 202 means job creation, separately from
job outcome. New shared operations are versionBacklinks, validateVersionIndexes
and rebuildVersionIndexes. The [HTTP contract](http-api.md#backlinks-and-deterministic-index-recovery-task-004-04)
owns filtering, cursor, error, cancellation and supported recovery boundaries.
No job.kind/workflow.scope enum or database migration changes. The existing
backlink_index_repair configuration/finding names retain EPIC-008/009 ownership.


## Explicit version Review additions (TASK-005-01)

Additive provisional v1 shapes are `review_request`, `review_value`, `review_view`,
`review_response`, `review_event`, `review_history_page` and
`review_history_response` in the machine contract. The embedded configuration schema
mirrors the affected selected definitions; the author-metadata subset is unchanged.
Review enums and existing reader resources retain their meanings. API/schema versions
remain v1/1.0; the physical SQLite migration advances to revision 10.

Review is owner-only and keyed by the stable owner principal and exact published
version, covering the complete directory. GET/HEAD and PUT use `/versions/{id}/review`.
The closed view has version_id, exists, value {state,reviewed_at}, revision, etag,
and page_position_url:null. Absent is exactly exists:false, value
{state:unreviewed,reviewed_at:null}, revision:null, etag:null and
required_precondition:if_none_match_star. Existing values omit required_precondition
and carry actual validators. Only reviewed has a non-null reviewed_at. PUT responses
also carry operation_id, including no-ops and receipt replay; GET does not invent one.
Clear on absent stays absent; clearing stored state preserves an existing default row.

PUT requires closed {action,state}, the canonical Idempotency-Key and exactly one
If-Match or If-None-Match:*. Missing controls are 428, malformed controls 400, stale
new writes 412 with safe current revision/etag, changed receipt identity 409
OPERATION_CONFLICT. Unknown enums are INVALID_ENUM, mismatched action/state is
INVALID_REQUEST and undefined transitions are INVALID_TRANSITION. Current authority
precedes fingerprint/replay; matching committed replay precedes the original now-stale
precondition. No-op checks its condition but preserves state revision/time/history.
History, state, redacted audit/outbox and receipt commit atomically. Current visibility
also filters saved receipt responses; a restored uncertain result is never a new key.

Owner GET/HEAD `/versions/{id}/review/history?cursor=&limit=` returns items and
next_cursor, ordered by committed internal event sequence. Closed events contain
UUID event_id/version_id/actor_session/operation_id, action, old_state, new_state and
UTC created_at. Cursor binds owner/version/filter/runtime generation; timestamps do
not order ties. Reopen/clear preserve prior events until explicit target purge.

`query_document` gains optional additive review_state/reviewed_at for latest,
last_reviewed_version_id, updated_since_review and continue_review_version_id. Modern
owner responses populate them; scoped responses carry explicit nulls for every personal
field. No latest means null state/time, and no valid completion means updated=false.
Last reviewed selects the most recent completion still in reviewed state by commit
order, not the highest publication sequence. Updated compares its sequence to latest.
Continue selects the most recently started/reopened version still reviewing, including
an older version. No reading, source freshness or AI observation contributes.

Owner-only `GET /documents?review=reviewing|updated_since_review` composes existing
project/lifecycle/cursor controls. Review and visibility predicates precede paging,
with the new filter in the cursor binding and summary ETags derived from the same
transaction snapshot. Home links the exact continued version or updated latest;
counts describe loaded rows, not an inferred total. Revisit remains TASK-005-02.
`features.review` gates consumers; missing/false is unavailable, never unreviewed.
Review invalidations carry no trusted state and become generic invalidations for
scoped readers or targets no longer visible. Consumers refetch authenticated state.

## Anchored notes and Revisit additions (TASK-005-02)

These are **additive provisional** selections in schema/API 1.0/v1. The executable
closed definitions are annotation_anchor, annotation_create, annotation_patch,
annotation_view, annotation_page, annotation_history_item/annotation_history_page,
annotation_stream_request/annotation_stream_metadata and revisit_request/revisit_view.
The embedded schema, selected fixture schema and manifest digests change together.
The synthetic conflict fixture is `tests/fixtures/annotations/conflict.json`.

An anchor has exactly version_id, page_path, region_id, page_query and page_fragment.
Whole-version anchors have null page/region and empty query/fragment. A region requires
its exact resolved HTML page; identifiers are page-local. Query/fragment are raw
navigation values, encoded once in a link. Original_anchor is immutable; anchor is
changed only by action=reattach. Selected_text and the saved context_ref preserve
fallback context when optional region metadata or an element is unavailable.
Resolution has status=resolved|unresolved and nullable reason. Unproved regions remain
unresolved. A newer version or repeated slug/path/region never retargets an old note.

POST creates a note under the route's exact version, with the four remaining anchor
keys, selected_text, context, context_ref, body and body_ref. Exactly one body string
or body_ref={upload_id} is non-null. Context is inline JSON/text or a context_ref,
with explicit null absence; the alternatives cannot both be non-null. PATCH is a
closed union: action=edit replaces content without anchor fields; action=reattach
provides the complete same-document destination anchor and replacement fallback/context
but no body. Reattachment requires a reliable exact destination; an unprovable region
is rejected, allowing an explicit page/whole-version fallback. Both mutations retain
old body/anchor/context revisions. DELETE is keyed, body-less, individually conditional
soft deletion. Its tombstone exposes anchors/revision but null body/body_ref/context_ref/
selected_text; nondeleted lists exclude it. Permitted history remains owner-readable.

Note views include annotation_id/document_id, original/current anchor, body/body_ref,
selected_text/context_ref, created_at/updated_at/deleted_at, revision/etag, history_url,
resolution, action and actor_session. Mutations and history rows also carry operation_id.
Lists/history use items and next_cursor; an empty list has [] and null. Unknown note
IDs are 404, not fabricated absent notes. Every list item has its individual strong
ETag; collection validators never authorize edits. POST is a keyed collection create,
not a conditional upsert. PATCH/DELETE require one quoted If-Match; Revisit uses the
existing individual If-Match or If-None-Match:* pattern. Malformed controls are 400,
missing controls/key 428, stale controls 412 with safe current revision/etag, changed
same-key intent 409. Authorized receipt replay precedes new-work CAS. Inline and sealed
body/context alternatives fingerprint exact content and semantic binding rather than
handle identity. A body binds its operation and annotation-create/edit target; inline
body uses the same inferred binding. Context binds version, nullable page, nullable
render instance and originating session. Inline context uses the binding it will have
when stored (render instance null), captured as an immutable receipt default. Its saved
context_ref and equivalent handles normalize to the same bytes/media/binding identity.
Replay retains that original default after another session or later reattachment;
a genuinely changed binding or bytes conflicts before receipt replay. No mutation acknowledges
success before durable commit.

Referenced bodies use an annotation/revision-bound content_url with bytes_sha256,
decimal byte_size and media_type. GET /annotations/{id}/body?revision=N streams that
saved Markdown object. The additive saved-context route is
GET /annotations/{id}/context?revision=N: N is a required positive current or permitted
history revision, and the server resolves its adopted context_ref. There is no arbitrary
URL/context-handle selector. It returns exact JSON/text bytes, Content-Type, Content-Length
and a strong sha256 ETag. Current owner/note/document authority, read pins and delivery
rechecks apply. Renderer disposal and originating-session revocation do not revoke another
currently authorized owner session's saved-record access. Null context, unknown revision
or a deleted current context is 404; permitted pre-deletion history remains readable.
The closed browser/native/desktop annotationContextStream operation takes only
annotation_id/revision and returns the cancellable stream plus etag, bytes_sha256,
byte_size and media_type. It is distinct from the body-less fresh-frame proof.

Revisit is independent document state. An absent GET is exactly
`{document_id,exists:false,value:{flag:false},revision:null,etag:null,page_position_url:null,required_precondition:"if_none_match_star"}`.
An explicit first false write creates an existing revision. Stored responses have a
real revision/etag and no required_precondition. PUT takes only flag:boolean; its result
adds operation_id. Optional additive query_document.revisit is false for an owner's
absent state and null for scoped readers. The owner-only revisit=true|false list filter
composes with project/lifecycle/Review filters before pagination and binds cursors.
features.annotations and features.revisit signal availability, never authority. Missing
or false flags show unavailable. Notes/Revisit invalidations carry redacted audit facts;
scoped or hidden-target delivery becomes generic library invalidation. Reading, notes,
Revisit and AI output never start or complete whole-version Review.

## Document lifecycle additions (TASK-005-03)

These are additive provisional v1 shapes selected in `tests/contracts/schema.json`:
`lifecycle_plan_request`, `lifecycle_commit_request`, `lifecycle_impact_item`,
`lifecycle_plan_page`, `lifecycle_result`, `trash_item` and `trash_page`.
Only shared request selections join the embedded configuration schema, with subset
equality. API/schema versions stay 1.0/v1. The [HTTP lifecycle section](http-api.md#document-lifecycle-task-005-03)
owns route, error and confirmation/replay precedence. The statement “changed commit
input conflicts” applies after closed request validation; invalid confirmation remains
400 before receipt comparison. Valid changed revision is 409; identical expired replay
keeps its original outcome. The synthetic confirmation fixture records this distinction.

Plan pages contain plan/document/action, open/committed/expired state, from/to lifecycle,
expected document/config revisions, expiry, current-authorized impact counts, nullable
committed result, ordered item page and next_cursor. Saved context references have kind context and their original context ID; no bytes are copied.
Items identify kind/id/version/
page/region/revision, nullable referring document and unresolved/pinned resolution.
Counts distinguish incoming declarations from structural edges and distinct source
documents. They never disclose currently hidden incoming sources. Reader-state rows
are labelled as-of-plan and excluded from freshness; explicit personal decisions and
incoming-reference impact changes require a new plan.

Result fields are operation_id (natural plan identity), plan_id, document_id, lifecycle,
previous_lifecycle, trashed_at, revision and changed. Aliased receipts resolve to that
same result. Trash rows add title/project/latest identity and nullable purge_eligible_at;
no file/note body is returned. Archive→active uses restore; trash→previous active/archived
clears current trash metadata. Already archived/archive and already trash/trash are
confirmed no-ops. No-op/replay adds no transition or event. `features.lifecycle` is an
optional availability flag; absence/false disables these controls. TASK-005-04 adds purge under the section below.

## Manual purge additions (TASK-005-04)

Additive provisional v1 widens implemented lifecycle plan operation/to-state and
result to purge/purged. Only purge results add job_id/job_url and use 202; existing
200 lifecycle results remain unchanged. `lifecycle_plan_page.backup` selects the
closed `purge_backup` projection, now widened by TASK-005-05 to actual not_created, verified or unavailable state with separate attempt/trial records. Shared requests stay in the embedded
subset; response-only definitions remain in the selected machine schema.

Purge impact binds every saved annotation revision/body/context, source records,
references and reader-resource changes. Its existing pagination/visibility rules
remain. The optional features.purge flag signals availability, never authority.
A document's purged lifecycle and minimal permanently reserved version IDs are
terminal. The original 202 response/plan/aliases stay stable while the independent
lifecycle job reports cleanup. Explicit target purge minimizes private plan and
receipt copies while preserving operation identity, original fingerprints and
uncertain outcomes; it does not authorize redispatch or expose hidden personal data.

## Host-local copy additions (TASK-005-05)

Additive provisional format-1 definitions in the selected machine schema are
backup_status/backup_complete/backup_attempt/trial_restore, purge_backup, copy_progress,
copy_acceptance, copy_plan_result, copy_version, export_index and local_copy_request.
The API remains v1/package schema 1.0. job.kind adds restore; backup/export gain execution.
The embedded configuration schema retains only its existing selected definitions/enums.

`backup_status` is `{mode:"manual",available,reason?,last_complete_verified,last_attempt,
last_trial_restore}`. Complete carries set_id/completed_at/verified_at/age_seconds;
counts are nonnegative, copy bytes/counts are decimal strings and unknown totals null.
Attempt includes job_id/state/stage/completed_at/error?; active completion time is null.
Trial states are not_run/passed/failed; not_run has a null time. No host path is public.
Purge's closed projection selects not_created/verified/unavailable, nullable set_id/time/
reason and the same separate attempt/trial records. It is informational, outside impact
freshness. The TASK-005-04 unavailable placeholder above is historical and superseded.

The exact closed local request names backup.create/resume/abandon and
export.plan/plan.show/create, carrying current runtime_generation. One export selector
is mandatory; id-bound actions cannot change destination/options. No arbitrary input
read or remote route is added. Export index format 1 binds library, distinct version IDs,
document IDs, exact payload roots, canonical inventory paths/digests and nullable separate
metadata roots, with selected-record/dependency file hashes. Metadata is outside original
payload roots; this is portability, not cross-library personal-state import.

Export verification enforces the declared closed copy_version shape and nested
personal-record structures before discarding JSON key/presence information. Review,
Revisit, reader resources and references bind their inner identities to the selected
wrapper; Notes bind complete qualified annotation histories and actual context
version/page descriptors, including anchors outside selected payload membership.
Revisit flag is Boolean. note_anchor dependencies retain the existing five dependency
states and do not widen payload selection. No portable personal-state merge is added.

## Current provider operational summary

The selected executable provider_attempt_summary schema describes the minimal
provider_attempts member on provider Job responses. Counts distinguish total
attempts, unresolved external outcomes and admitted reconciliation records;
latest_state does not erase older uncertainty. Native request/session IDs and
payloads remain server-side. Existing job_resume captures the finite eligible
attempt set on the server; terminal reconcile_only changes operational accounting
without reopening the terminal job or changing its current result/generation.

## Additive managed-capture DTOs (TASK-006-02)

The original source/citation and provider-attempt unions above remain normative.
The migration-16 local managed capture API implements this additional bounded
surface; it does not return a completed AI answer or create observed_input.

- CaptureRequest: optional version_id, source_intent (historical/moving_target),
  source_mode (pinned/current), provider_id, and a nonempty selections array.
  Each selection names source_id, optional revision and working_copy, and explicit
  paths {path,read_id?}. Pinned file/snapshot reuse requires its existing exact
  retained read_id; current files are actually captured. No caller host root or
  job_id is accepted. Explicit mode wins; otherwise historical uses pinned,
  otherwise the remembered default applies, falling back to pinned.
- Capture projection: capture_id, project_id, state, requested_selection,
  complete, nullable target_set_id/captured_at, reason and reads_url. Complete
  means every selected capture was committed, not provider delivery or semantic
  assessment. Failed/partial selections preserve the original denominator.
- Read projection: read_id, capture_id, source_id, source/location identity,
  bytes_sha256, byte_size, retention_mode, availability and optional reason.
  Metadata-only views add capture_availability and read_state. Its value is
  not_read_by_view. A reference-only identity is reported not_retained; retained_snapshot availability
  remains distinct from an actual verified_read. An explicit byte read reports
  retained_snapshot or approved_source internally only after verification.
  Its internal byte-consumer record sets read_state:verified_read; the HTTP
  response remains a byte stream. Neither read_state value establishes an
  attempt input observation.
- Captured diff: left_capture_id, right_capture_id, changes (source_id, location,
  before/after read records), nullable next_cursor and semantic_verdict:null.
  This compares persisted byte digests and capture availability/reasons, not
  semantic source truth. The cursor is bound to that exact pair of captures.

Pinned read_id reuse is bound to its original project before byte consumption;
source ID/path equality alone cannot authorize a cross-project provider policy.
Foreign and missing scoped read IDs have the same nondisclosing error. Byte
errors reuse canonical 409 SOURCE_UNAVAILABLE and 503 CONTENT_INTEGRITY_ERROR;
no success payload is sent for changed, absent or corrupt exact bytes.

Read and diff pages contain at most 64 records per response, with keyset
continuation; page size is not a source/file-count limit. Scoped responses require
that principal's capture plus current exact project/version/source access before
returning rows, counts or bytes. Known IDs cannot reveal another principal's
capture, revoked source or hidden version. Current authority also filters saved
operation responses. Owner history can retain its original selection without
returning revoked byte payload. Denied existence uses nondisclosing 404 where
appropriate. Byte delivery uses application/octet-stream, attachment and nosniff.

Private provider input/orphan metadata additionally carries optional
retention_mode and source_version_id for lifetime/restore/lifecycle guards.
source_version_id retains the immediate capture's literal context; optional
source_version_ids is a sorted/deduplicated array of known inherited context
version IDs. Ownership merges independently of observed-origin/time conflict
selection, preserving unknown historical facts. Neither field substitutes for
the required producing-attempt input observation or grants dispatch authority.

Private accepted-generation SourceReadIDs, capturing-selection _retained_read_ids
and export RetainedReadIDs freeze actual retained-read dependencies in existing
JSON storage. They are not accepted public request fields, new source facts or
new observed_input rows. Capture requested_selection omits its private projection.
Ignored current/non-working Git read_id values do not create ownership edges. Managed
ProviderScope requires an explicitly selected provider ID and a valid managed
capture binding for each selected read. The earlier blank-provider internal
foundation fixture seam is not public managed admission authority.

Scoped exports can add a typed managed_source_snapshot dependency only for the
selected version's separately retained capture. It records read/source/version,
exact source/location, SHA-256, bytes and included/excluded_by_scope. Included
objects must verify their exact size/hash and snapshot/revision identity. This
neither changes original payload nor exports provider operational history.

Managed metadata view and both diff sides share the same read projection.
capture_availability preserves original capture facts, interpreting only the
supported copied reference-only marker described below. Raw copy projection and
current permitted byte availability remain separate; neither requires another
source read.
An incomplete capture projects captured units unavailable/capture_not_completed,
preserves actual missing/denied/unread reasons, and never claims bytes_origin.
A completed reference-only row projects unavailable/not_retained without an
origin. Only a permitted completed retained row claims retained_snapshot; it
still says not_read_by_view and is not an actual verified read or observation.
Revoked owner-history rows redact source/location/hash and origin. Scoped history
and diff retain their existing nondisclosing authority refusals.

An ordinary incomplete selection has capture-level reason
incomplete_source_capture; its per-unit reasons and requested denominator are
retained. Version-context retention cleanup can leave version_purged as a stored
read lifecycle reason. This does not grant visibility of a purged context or
permission to read its bytes.

The additional capture reason vocabulary is location_invalid, source_changed,
source_unreadable, missing_source, source_denied and source_read_failed for
per-unit failures; not_read remains an unconsumed selected unit. Operation-level
reasons reuse current authority/configuration failures and add stage_failure,
check_bound_exceeded, interrupted_before_commit and resource_exhausted. They are
settled operation facts, not permission to replay or dispatch semantic work.

The supported reference_only_uncopied copy marker preserves a previously
available captured source/location/byte identity. Managed metadata projection
reports historical capture_availability:available for that unambiguous identity;
current availability remains unavailable/not_retained and read_state remains
not_read_by_view. Metadata-only diffs compare the same normalized capture facts.
Unsupported legacy or missing-identity markers do not establish past availability.

## Provider status addition (TASK-006-03)

GET/HEAD /api/v1/providers is an authenticated owner-only read. It creates no
inference, source capture or account/configuration mutation. Its data object is:

| Field | Wire meaning |
| --- | --- |
| provider_id, version | codex and the verified pin when known |
| state | ready, auth_required, quota_unavailable, quota_exhausted, unsupported_version, source_unavailable, paused or provider_error |
| reason | Optional bounded cause; never native error prose |
| authentication_mode | Native account mode or unknown; no email, token or account-store path |
| model, effort, models | Effective native choice when verified and fresh catalog; never entitlement proof |
| ordinary_usage_allowed | Backend-disclosed boolean or null; false alone does not identify the cause of unavailable usage |
| read_only | Supported native mode was verified; no universal OS containment claim |
| lookup_supported, cancellation_supported | Pinned protocol supports the operation; interrupt acknowledgement is not terminal proof |
| catalog_proves_access | Always false |
| input_formats, output_format, native_input_telemetry | Initial adapter supports utf8_text/public_final_text and no exact native-read telemetry; unavailable wiring may report no format knowledge |

features.provider indicates installed production transport support, not account
readiness or public question/generation admission. Task03 did not expose generation or questions. Task04 now separately wires
features.generation; Task05 separately wires features.questions when its service is composed. The shared native operation providerStatus
accepts exactly an empty JSON object and forwards only this GET. Unknown or
malformed payload cannot reach HTTP. No native IDs, CWD, private invocation,
source bodies, raw configuration, credentials or reasoning enter this projection.
The existing job summary/resume and source/citation unions are unchanged.


The provider status route accepts no query parameters. Unknown cursor/limit or
other query keys fail GET/HEAD before a native probe; unsupported methods retain
the ordinary dispatcher refusal. Authentication and current owner authority are
checked before and after a potentially slow probe. Both shared clients reject
unknown authority-bearing state values and a missing/non-boolean/non-null usage
allowance. False remains false; null remains unknown. The UI renders bounded
reason labels and supported authentication modes without exposing native prose.
The refresh action retains keyboard focus while busy and a stable polite status
region announces changes. Settings links use shared SPA navigation and existing
44 px action targets.

## Generation implementation projection (TASK-006-04)

Generation request and body staging follow the [HTTP generation contract](http-api.md#generation-and-drafts-implementation-task-006-04).
Each explanation reference is exactly
`{version_id,page_path,region_id,bytes_sha256}` with explicit nullable page/region;
a non-null region requires its page. For a page reference, bytes_sha256 is the
exact selected HTML file digest. A whole-version reference uses the canonical
payload inventory digest and retains the service manifest as its separately
identified supplied object. That header does not replace the complete context:
every immutable file is supplied with the complete directory inventory, exact
UTF-8 text or labelled base64 binary content, and directory/link metadata.
Inventory cursors continue to completion. Current attempt/read authority, owned
read pins and partial-frame cleanup remain enforced throughout delivery.
Acknowledgement requires the complete written frame and matching native ACK.
Neither form creates a project-source capture/read/input observation or proves
semantic support. When useful, service-derived manifest references preserve the
selected immutable navigation target without rewriting author file bytes.
The reference DTO, payload/manifest identities and public schema remain unchanged.

The draft projection is
`{draft_id,project_id,document_id,base_version_id,job_id,state,intent,revision,created_at,updated_at,payload_sha256,metadata_sha256,published_version_id,failure_reason,etag}`.
Document/base, output digests, published version and failure reason are nullable.
The reserved new-document identity and private output paths are not exposed.
Revision is a positive integer and the validator covers the complete projected
snapshot. Existing draft state/action unions and ready-only replacement remain
unchanged. History returns these snapshots in revision order.

GenerationStart native payload is `{request,operation_id}`. Draft list accepts
only optional project_id/cursor; get takes draft_id; history adds optional cursor.
DraftPatch takes `{draft_id,body,etag,operation_id}`; DraftPublish takes
`{draft_id,etag,operation_id}`. JobResume takes
`{job_id,decision,expected_attempt_generation,etag,operation_id}` with the existing
resume decision union and positive generation. No payload permits a generic
route, native credential or source-management handle.

Task04 enables features.generation only when its real factory is wired. The
provider status input/output format describes the native provider transport;
the generation service's separately captured directory ZIP does not redefine
native public-final text as ZIP output. Questions remain unavailable.


### Generation caller recovery and retained-file selection

Generation and draft caller journals persist the original runtime generation
before dispatch, in addition to their existing operation key, original body and
semantic validator. Each job-resume subrequest captures its own runtime before
POST; the parent generation's older runtime cannot substitute for that fact.
Pending resume recovery reads its original operation receipt first, even when
the current job is no longer paused. Receipt recovery uses current authority and may read an
existing receipt across restore. A missing receipt is not authority to resend
under a different runtime; a legacy record without the original runtime remains
receipt-only. A same-runtime missing receipt permits only the exact saved
request. Current login or a freshly captured authority cannot fill the old fact.

Definitive stale-validator/base refusal or a confirmed failed receipt is distinct
from network uncertainty. An explicit acknowledgement may retain the refused
request as caller history and release its active slot for a new explicit action
with a new key. Uncertain requests retain their original key/body/validator.
Acknowledgement does not retry or rebase: ready replacement retains its original
base, and a changed base requires an explicit new linked generation/draft.
For job resume, direct PRECONDITION_FAILED 412 and INVALID_TRANSITION 409 are
definitive refusals. Operation-identity/reconciliation conflicts remain pending.
The explicit resume acknowledgement rereads current state and atomically archives
the old subrequest before releasing only that exact journal slot. A fresh decision
gets its own key/current validator. A missing receipt under a changed or unknown
original runtime permits no POST.

The generation retained-files chooser projects existing completed capture/read
metadata through `GET/HEAD /api/v1/projects/{id}/source-reads?source_id=...`.
Its `{items,next_cursor}` uses the existing read projection above plus
`captured_at` from the original capture. It is not a new observation time. The cursor
binds library/runtime/auth epoch, current principal/scope and generations,
project permissions, configuration and source filter. It is not a capture or
provider observation. Current source access and that principal's capture access
are required before rows are returned. Page size does not limit total selection.
The chooser uses exact retained path/read identities; missing or unavailable
bytes never select current files implicitly. Existing pinned-byte admission
rechecks project/source/path and actual retained bytes.

## Saved question implementation projection (TASK-006-05)

The API remains v1 and package schema 1.0; SQLite revision 19 adds saved question
and answer ownership. The typed provider invocation has mutually exclusive
`generation` and `question` consumers. A question uses the existing job,
provider-attempt, result, receipt and resume state machines in read-only mode.

The closed creation object is
`{question?|body_ref?,page_path,region_id,page_query?,page_fragment?,source_mode?,context_ref?|display_state?,provider_id?,model?,effort?,source_selection?}`.
Exactly one text selector is present. HTTP requires explicit nullable
page_path/region_id; region requires its exact page. Inline display_state and
context_ref are mutually exclusive. Empty/null/unknown selectors do not select
another page or default silently. `body_ref` identifies the complete sealed
question text as `{object_id,bytes_sha256,byte_size,media_type}`; byte_size is a
nonnegative decimal string and media_type is text/plain. The body upload uses
`purpose=body,input_mode=object,target={operation_id,action:question,version_id,page_path}`; page_path is required and explicitly nullable.
The real object transfer entry is the existing fixed `_input`, not a user path.
Display state uses its separate purpose=context upload and exact
`{version_id,page_path,render_instance_id}` target before POST /contexts.

Creation returns `{operation_id,question_id,job_id,job_url}` with status 202.
A question view carries question_id, project_id, document_id, version_id, job_id,
job_state, nullable job_error/latest_answer_id, operation_id, created_at, nullable
page_path/region_id, page_query/page_fragment, body_ref/text_url, nullable
context_ref/display_context, explanation_reference, source_mode, nullable
target_set_id, resolved targets, requested_selection and limitations. Detail also
includes the first answers page. Public display_context is
`{context_ref,bytes_sha256,byte_size,media_type,render_instance_id}` with decimal
byte_size and explicitly nullable render_instance_id. Saved body_ref is
`{object_id,bytes_sha256,byte_size,media_type}`. Lists use `{items,next_cursor}`;
service page size is a cursor transport detail, not an author/content quota.

An answer view is
`{answer_id,question_id,attempt_id,state,created_at,body_ref,body_url,citations,explanation_references,limitations}`.
Each citation requires the original union's explicit nullable read_id,
verification_id and input_origin, target_set_id, canonical source/location and
bytes_sha256; package_evidence_ref remains optional. Runtime views add current
availability and nullable bytes_origin/reason/observed_at/verified_at. Missing
nullable members are malformed, not an implicit observation basis. Explanation
references require version_id, nullable page_path/region_id and bytes_sha256.
Limitations are `{code,message}`. Typed final output is exactly
`{state:final|partial,body,citations,explanation_references,limitations}`; error
answers are service settlement records, not model claims of completed support.

Canonical Git source identity compares kind/repo_id/object_format/exact_revision;
snapshot identity compares kind/snapshot_sha256. Authored metadata wrappers and
local retention labels cannot create a different source identity, but exact
location/hash, accepted selection, owner/version and current read guards still
must match. Original published/verification JSON stays unchanged. Saved citation
availability uses the current owner's source-read policy, independently of old
attempt auth_epoch and provider_ids disclosure. Revoking only AI disclosure does
not revoke owner retained reads; removing current source authority does. Restore
review can make source bytes unavailable while saved answer text remains readable.
Original observations, verification time and attempt scope are never rewritten.


Question display regions are optional page-local runtime anchors. Admission and
ongoing authority validate the exact visible immutable page/object/hash while
retaining the original region/query/fragment/context without requiring authored
region metadata. The provider explanation_region frame marks metadata_status as
`declared` or `not_declared`; missing optional metadata is null and does not prove
a project-source region or observation. Generation reference policy is unchanged.

The closed native operation `answerCitationStream` accepts exactly
`{answer_id,citation_index}` where the index is a nonnegative integer. It opens
only the matching saved-answer citation source route and reuses the existing
verified stream descriptor/byte bridge. Its per-pull HEAD guard covers both
current answer visibility and current source authority. No arbitrary route,
source root or fresh-source selector is accepted. The public citation union
retains every original nullable observation member unchanged.


Question source-policy refusal is an existing operation-ledger failed response,
not a Question/job or a new schema/queue. Its closed typed refusal normalization
commits 403/error metadata before publication of the refusal proof. The original
semantic request fingerprint and terminal receipt immutability prevent delayed
same-key admission. The error's question_admission marker alone and a missing
receipt cannot authorize acknowledgement; the client requires the matching
current-authorized failed receipt under the original runtime and owner.

## Multi-device passive read and native event cursor projections (TASK-007-04)

The API remains v1 and package schema 1.0 at SQLite revision 19. Touching
`GET`/`HEAD` routes accept the optional header `Haeryong-Passive-Read: 1`;
any other header value, multiple values or any non-`GET`/`HEAD` method fails
`400 INVALID_REQUEST`. Closed read-only native operations accept optional
`passive?: boolean` (`true` forwards `Haeryong-Passive-Read: 1`).

Closed native `subscribeEvents` accepts `{cursor?: string}` and returns
`{subscription_id, cursor?, resync?: {reason, stream_epoch, head, resume_cursor}}`.
Closed native `eventsPoll` accepts `{subscription_id}` and returns
`{closed: boolean, invalidated: boolean, cursor?: string, resync?: {reason, stream_epoch, head, resume_cursor}}`.
See [multi-device reconciliation evidence](../testing/multi-device-reconciliation.md).

## Scan and finding projections (TASK-008-01)

The API remains v1 and package schema 1.0 at SQLite revision 20. Selected
contract definitions in `tests/contracts/schema.json` add `scan_request`,
`scan_accepted`, `scan_report`, `finding_view`, and `finding_page`, with
`capabilities.features.scans = true` when the scan service is wired:

- `ScanRequest`: closed object with optional `scope` (`selected_projects` |
  `all_allowed_projects`), `project_id`, `project_ids`, `document_id`,
  `document_ids`, `version_id`, `path_prefix`, `page_path`, `region_id`, `depth`
  (`change_focused` | `full_scope` | `deep_semantic`, accepting hyphenated
  aliases `change-focused` | `full-scope` | `deep-semantic`), `source_mode`
  (`pinned` | `current`), `target_set_id`, `sources`, `finding_id`, and
  `expected_finding_revision`.
- `ScanAccepted`: `{operation_id, job_id, job_url, coalesced, target_set_id}`.
- `ScanReport` (`job.result` for `kind = "scan"`): carries `job_id`, `stage`
  (`inventory` | `inspect` | `repair` | `complete`), `enumeration_complete`,
  `budget_exhausted`, `rescan_recommended`, `incomplete_reference_repairs`,
  nullable `target_set_id`, `config_revision`, `permissions_revisions`,
  `requested_scope`, `effective_policy`, `source_targets` (`kind` in `files` |
  `git`), immutable `captured` denominator (`projects`, `documents`, `versions`,
  `pages`, `regions`, `entries`, `sources`, `indexes`, `excluded_documents`,
  `total_units`), `structural` (`checked`, `checked_index_items`, `repaired`,
  `failed`, `anomalies_reported`, `excluded`, `blocked`, `skipped`,
  `regionless_versions`, `metadata_less_versions`), `semantic` (`eligible`,
  `assessed`, `skipped`, `unresolved`, `complete`, `status` in `not_assessed` |
  `unable_to_verify`, `reason` in `semantic_assessment_not_run` |
  `structural_or_source_blocked` | `run_budget_exhausted`), top-level
  `provider_attempts`, and `findings` (`total`, `proposed`, `applied`,
  `rejected`, `stale`, `blocked`, `undone`).
- `Finding` (`finding_view`) and `FindingPage` (`finding_page`): each finding
  carries `finding_id`, `job_id`, `kind`, `origin` (`deterministic` | `ai`),
  `project_id`, nullable `document_id`/`version_id`/`page_path`/`region_id`/`target_set_id`,
  `evidence`, `evidence_fingerprint`, `rationale`, `uncertainty`,
  `captured_config_revision`, `permissions_revision`, `proposal`, `status`
  (`proposed` | `applied` | `rejected` | `stale` | `blocked` | `undone`),
  `approval_required`, optional `reason`, nullable `applied_ref`/`decided_by`/`decided_at`,
  `revision`, strong `etag` (`"finding-{id}-r{revision}"`), `created_at`, and
  `updated_at`.
- Closed native operations in `desktop/readerfacade` and `frontend/src/client`:
  `scanStart` (`{request, operation_id}`) and `jobFindingsList`
  (`{job_id, kind?, status?, limit?, cursor?, passive?}`). See
  [scan inventory evidence](../testing/scan-inventory.md).
