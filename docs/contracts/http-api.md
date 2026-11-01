# HTTP API v1

TASK-006-01 implements owner-only `POST /api/v1/jobs/{id}/resume` for provider
jobs. It requires Idempotency-Key, the job's strong If-Match and the closed
`job_resume` body from the schema registry. The original response replays before
its original precondition is re-evaluated, subject to current authorization.
Hidden provider targets expose minimal owner operational status and permit only
reconcile-only recovery; source/result delivery and continue/retry retain normal
current visibility. [The foundation record](../testing/provider-attempts.md)
states current behavior and qualification limits. A permanently superseded
saved-result continuation returns 409 INVALID_TRANSITION, not retryable service
recovery. Authentication, current ETag and expected-generation checks retain
precedence. Safe saved continuation queues the job; it does not dispatch again.

Provider job detail/list exposes the scoped provider_attempts summary: count,
unknown, latest_state, reconciliation_pending and unknown_outcome_reason when
needed. A completed current job can still have older unknown attempts. No native
request/session IDs, source or output bodies enter that summary. The same fields
participate in job ETags across detail, resume and receipt replay. Explicit
reconcile_only may authorize a finite server-captured older-attempt set while
preserving terminal job state, current result and generation; it performs status
lookups only and cannot dispatch/apply again.

**Proposed contract, documentation baseline 1.6.1.** Common prefix `/api/v1`. JSON is UTF-8. The [shared registry](schema-registry.md) defines shared enums, errors, resource validators and browser routes; it delegates exact upload/import fields and composite-command semantics to [import workflow](import-workflow.md). Private responses use `Cache-Control: no-store`; trusted content-hashed UI assets may be cached, but private result pages/assets/source excerpts may not.

## Principal model

The owner is one person across devices; principal identity is distinct from a session. Scoped agents receive only approved projects/actions, including an explicit `scan.request` grant when post-work scans are authorized. They cannot modify personal review, sessions, security/deletion policy or grant themselves publication/disclosure authority. Workers are job-bound and submit only captured-scope results.

First-owner enrollment and recovery use local bootstrap. After pairing, every owner session can approve another device, manage remembered project/source/provider settings, archive/trash/restore, write personal state and confirm purge or retention changes. Consequential changes use the normal explicit impact-confirmation flow, with no separate administrator role or per-plan credential. Managed workflow credentials remain scoped to their requested tasks and do not gain personal review or purge authority.

One loopback listener and an optional private HTTPS proxy use the same per-device revocable bearer model for local and remote clients. Remember device defaults to origin-scoped localStorage so reload, new tab and browser restart reuse the session; memory-only storage is optional. Cookie authentication is not used for owner/agent management authority; a renderer-only scoped credential candidate follows authentication and G-VIEW/G-AUTH. Exact Host/Origin validation, TLS and credential handling follow [authentication](authentication.md). Never infer principal from User-Agent, a CLI flag, a loopback peer or a forwarded header.

The registry owns workflow scope names, including project.read, version.read, source.read, package.export, context.write, import.publish, generation.request, draft.write, draft.publish, scan.request, job.read and job.cancel. A listed scope is always limited to the captured project/resource/job set and current policy. The application derives the needed subset from the owner's requested workflow and remembered selections; it is not a new interactive grant step. The server allocates a stable workflow_binding_id/principal_id independent of credentials and mutable selections. Owner-authenticated local CLI/IPC, exposed by auth grant create/revoke and reusable by the trusted workflow launcher, issues/rotates/revokes credentials for that binding; no remote issuance route is required. Owner-only actions remain unavailable to workflow credentials. First local bootstrap acts as the library owner only for its documented enrollment/recovery flow; an arbitrary local peer or polling secret is not that principal.

## Endpoint catalog

In the last column, `K` means a required Idempotency-Key; `N` means a named natural/single-use identity; `O` means optional key for a repeatable preview/validation; `-` is read-only. The operation matrix below defines exceptions. A mutable item returned in any list carries its own ETag.

| Method and path | Principal / precondition | Result and semantics | Retry identity |
| --- | --- | --- | --- |
| GET /health/live | public, no metadata | Minimal liveness only | - |
| GET /capabilities | authenticated | Library/API identity, runtime_generation, stream_epoch, implemented html_js_v1/multi-page loader, streaming/resumable transfers and actual preview support; no secrets or artificial package/count ceilings | - |
| GET /status | owner | Redacted service/provider/backup status, staging byte/count summary and pending-upload link, available actions and actionable unavailable reason | - |
| POST /auth/pairings | bounded unauthenticated browser flow | Pending ID, display code and memory-held one-time polling secret | N |
| GET /auth/pairings | owner or first local bootstrap | Bounded pending queue with IDs, requesting origin, creation/expiry and untrusted device labels; no polling secrets | - |
| POST /auth/pairings/{id}/poll | polling secret in body, configured browser origin | pending/approved/rejected/expired; one approved redemption issues a revocable device bearer for the selected remembered/memory-only storage | N |
| POST /auth/pairings/{id}/approve | first local bootstrap or existing owner session + pairing If-Match | Body {code,origin}; verify exact comparison code and requesting device/origin; approve only that request | K |
| POST /auth/pairings/{id}/reject | first local bootstrap or existing owner session + pairing If-Match | Body {code,origin}; reject an unredeemed pending/approved request; polling returns rejected and issues no bearer | K |
| GET /session | authenticated | Owner/device/session identity, nullable expiry and current credential scope; no bearer secret echoed | - |
| DELETE /session | current authenticated session | Revoke current session and streams; clear its remembered client credential | N |
| GET /sessions | owner | Revocable device/session list, no raw secrets | - |
| DELETE /sessions/{id} | owner | Revoke exact device/session and protected streams | N |
| GET /projects | scoped authenticated | Visible registered projects | - |
| POST /projects | owner | Register logical identity; source/disclosure choices are explicit remembered configuration | K |
| GET /projects/{id}/sources | approved project visibility | Logical source identities, exact revisions, retention/disclosure policy and current availability; no arbitrary root paths | - |
| GET /documents | scoped authenticated | Filtered, paginated list with exact version/review/freshness/lifecycle summaries from registry | - |
| GET /documents/{id} | scoped authenticated | Metadata, latest published pointer, summary and allowed actions | - |
| GET /documents/{id}/versions | scoped authenticated | Immutable identities, source basis and review per version | - |
| GET /versions/{id} | scoped authenticated | Normalized metadata summary, immutable manifest/inventory references, actual source basis, default page and personal-state links; no requirement to inline the full directory | - |
| POST /uploads | owner or exact scope-by-purpose authority from import workflow | Create bound result/context/body/metadata staging; the canonical purpose/target table governs draft-only versus publication preparation | K |
| GET /uploads | owner | Cursor pending-upload discovery, including uploads whose originating workflow credential is gone; state/unadopted filters and actual bytes/activity/target/reuse/pins | - |
| GET /uploads/{id} | owner or originating workflow principal, with current target scope | Confirmed staging state/checkpoints, reusable/unavailable_reason and cursor-linked entry/pin progress; no publication | - |
| GET /uploads/{id}/pins | same current upload authority | Cursor pin/adoption references and reasons preventing reclamation, without secret material | - |
| GET /uploads/{id}/entries | same current upload authority | Cursor entry identity/kind and accepted progress; no whole-inventory requirement | - |
| GET /uploads/{id}/ranges | same current upload authority | Cursor accepted byte ranges for exact paths, optionally filtered by path, bound to the confirmed checkpoint | - |
| PUT /uploads/{id}/entries?path={path}&kind={kind} | owner or originating workflow principal, with current purpose/target scope | Stream regular-file bytes/chunks, archive transport bytes or typed directory/link entry data; resumable exact path/offset identity | N |
| POST /uploads/{id}/complete | owner or originating workflow principal, with current purpose/target scope | Seal an exact end-of-input checkpoint; expose sealing progress or sealed result and retain resumable open state on a recoverable failure | K |
| POST /uploads/{id}/abandon | owner or originating workflow principal, with current purpose/target scope | Stop further transfer/seal acceptance, retain existing adopted references and protect active/unknown work before reclaiming unreferenced staging | K |
| POST /imports | owner or current import.publish scope | Canonical import request requires upload_id and explicit document_id; publish 201, return identical explicit version 200, or commit a 202 receipt for job.kind=import pending atomic publication | K |
| POST /packages/validate | owner/scoped agent | Validate a sealed upload or stream a raw directory/archive/file into validation staging; optional metadata, no author manifest or arbitrary server path | O |
| GET /versions/{id}/render-bootstrap | authenticated host with version.read scope | Small selected-version/default-page/loader context and the selected delivery construction; no whole-directory HTML/asset envelope or owner bearer in the artifact. Availability is request-context honest: remote ingress (exact runtime Host/Origin classification) reports `renderer_available=false` with an optional `renderer_unavailable_reason=renderer_remote_ingress` and keeps the attachment fallback, while local callers keep the loopback construction | - |
| POST /versions/{id}/render-sessions | owner or current version.read for the exact version/project | Reserve/reuse its sticky renderer origin and return one ephemeral delivery session with a one-use form-POST handoff. Remote ingress is denied `503 FEATURE_NOT_READY` with `details.reason=renderer_remote_ingress` and retryable false (no Retry-After, no owner logout or retry loop) before any renderer construction, operation reservation or listener; remote construction belongs to EPIC-007 | K |
| POST /versions/{id}/render-sessions/{session_id}/renew | same current originating principal and exact known session | Reauthorize a live selection; lease expiry alone is not logout, and explicit close is terminal. Remote ingress carries the same 503 FEATURE_NOT_READY availability denial | N |
| POST /versions/{id}/render-sessions/{session_id}/close | current principal owning that known session | Idempotent terminal close, including after document visibility changes and from remote ingress; never release its origin reservation | N |
| GET /versions/{id}/files | owner or current version.read scope | Cursor listing/stream of exact file/page/directory/link entries, byte sizes, media/preview metadata and digests | - |
| GET /versions/{id}/file?path={path} | owner or current version.read scope | Stream an immutable regular file or return typed directory/link metadata; resolve_link=true explicitly resolves in-version links and reports exact requested/resolved paths and target validator | - |
| GET /versions/{id}/metadata?kind={kind} | owner or current version.read scope | Stream exact normalized or explicitly supplied metadata; absent supplied metadata is explicit, not an injected payload file | - |
| GET /versions/{id}/package | owner or current package.export scope | Stream an exact version's directory archive as an attachment, preserving payload paths/bytes; optional service metadata is separate | - |
| GET /versions/{id}/source-evidence/{evidence_id} | owner or version.read; exact source bytes require current source authority | Immutable authored capture_status plus separate service_verification/history and currently authorized excerpt or typed unavailable state | - |
| GET /versions/{id}/source-evidence?evidence_id={evidence_id} | same evidence-view authority | Lossless single-item alias for arbitrary nonempty Unicode local IDs; HEAD has the same authority and an empty body | - |
| POST /versions/{id}/source-evidence-verifications?evidence_id={evidence_id} | owner with source authority, or current version.read + source.read scopes | Explicit bounded exact-target check; optional expected digest/excerpt, separate durable result, no AI dispatch or provenance rewrite | K |
| GET /versions/{id}/source-evidence/{evidence_id}/verifications | same evidence-view authority | Cursor verification history, no new source check | - |
| GET /versions/{id}/source-evidence-verifications?evidence_id={evidence_id} | same evidence-view authority | Canonical lossless history locator with optional cursor/limit; implemented without a new source read | - |
| POST /versions/{id}/source-observations | owner only | Explicit current Git observation; durable run, fixed coverage and target-bound structural triggers; additive provisional route | K |
| GET /versions/{id}/assessments | authorized version/target scope | Published history, target_set_id and version/page/region scope filters, typed coverage and selected applicable summary; no unknown or zero-unit scope is silently aligned | - |
| GET /versions/{id}/assessments/coverage?coverage_set_id={id} | authorized version/target scope | Cursor fixed required-unit tuples, outcome/reason/discrepancy and contributing assessment_id/target_set_id; counts derive from these records | - |
| GET /search | scoped authenticated | Title/text/region/topic/source results with eligibility filtering before counts/snippets | - |
| GET/HEAD /versions/{id}/references | scoped authenticated | Source-only cursor collection of persisted declarations and guarded live resolution states | - |
| GET/HEAD /versions/{id}/references/resolve?reference_id={id} | scoped authenticated | One immutable query-form ID, optional bound source page_path/region_id; exact authorized target or minimal historical gone | - |
| GET/HEAD /versions/{id}/backlinks | scoped authenticated | Incoming canonical edges with visibility filtering | - |
| GET/HEAD /versions/{id}/indexes/validate?kind={kind} | owner | Read-only deterministic derived-index comparison; honest consistent/inconsistent/incomplete result | - |
| POST /versions/{id}/indexes/rebuild | owner | Exact-version same-revision replacement jobs; kind search/references/all; no job resume | K |
| GET /versions/{id}/related | scoped authenticated | Separate overlays with rationale, origin, current decision and individual validators | - |
| GET /topic-assignments | owner, exact document/version/page/region filters | Existing assignments including owner pin/rejection state and each ETag | - |
| PUT /topic-assignments/{id} | owner + individual If-Match | Set pinned/accepted-or-rejected decision; record rejection fingerprint and prevent silent AI override | K |
| GET/HEAD /versions/{id}/review | owner | State, exists, revision/etag or explicit create precondition | - |
| GET/HEAD /versions/{id}/review/history | owner | Cursor history of exact-version explicit transitions; no page completion telemetry | - |
| PUT /versions/{id}/review | owner + individual precondition | Body {action, state}; explicit start/complete/reopen/clear for exact version, with history | K |
| GET /documents/{id}/revisit | owner | Independent document flag and individual/create precondition | - |
| PUT /documents/{id}/revisit | owner + individual/create precondition | Body {flag}; independent of review | K |
| GET /versions/{id}/reading-position | owner | Version's last-opened page/navigation pointer and first/last-opened times, with its own validator/create precondition | - |
| PUT /versions/{id}/reading-position | owner + individual/create precondition | Save {page_path,page_query,page_fragment,opened}; pointer/observation only, no per-page position or review transition | K |
| GET /versions/{id}/page-position?page={path} | owner | Exact page's position/navigation state and its individual validator/create precondition | - |
| PUT /versions/{id}/page-position?page={path} | owner + individual/create precondition | Save {page_query,page_fragment,position} for this page only; does not move the last-opened pointer | K |
| GET /versions/{id}/reader-preference | owner | Version-specific personal preferred default page and individual validator/create precondition | - |
| PUT /versions/{id}/reader-preference | owner + individual/create precondition | Save {default_page:path-or-null}; null clears the personal override without editing immutable metadata | K |
| GET /versions/{id}/annotations | owner | Anchors/notes with each individual ETag | - |
| GET /annotations/{id} | owner | One original anchored note/current revision and allowed history | - |
| POST /contexts | owner/scoped context-writing workflow | Accept a direct JSON/text stream or a sealed purpose=context uploaded-object reference and return scoped context_ref; large transfer chunks/resumes automatically | K |
| GET /contexts/{id} | current authorized record/context scope | Stream exact context bytes; an opaque reference never authenticates the read by itself. An optional fresh-proof mode (below) returns a body-less 304 proof instead | - |
| POST /versions/{id}/annotations | owner | New version/page/region note with inline text/context or immutable body/context references; no total note/state size cap | K |
| PATCH /annotations/{id} | owner + individual If-Match | Body edit or explicit reattachment; stale edit fails, old anchor/history retained | K |
| DELETE /annotations/{id} | owner + individual If-Match | Explicit note deletion, separate from document lifecycle | K |
| POST /documents/{id}/lifecycle-plans | owner | Exact archive/trash/restore/purge impact at resource revision | O |
| POST /lifecycle-plans/{id}/commit | owner + expected revision + confirmed: true | One unexpired exact-impact plan, rechecked eligibility and resource/config revisions | N |
| POST /versions/{id}/questions | owner, remembered/default or explicit source mode and exact page/context | Records pinned/current choice, page/region, requested targets/read set and durable job; 202 with question_id/job_id; missing source cannot count as verified | K |
| GET /versions/{id}/questions | owner | Saved question summaries, exact context, job state and latest completed answer identity | - |
| GET /questions/{id} | owner | Original question, target/read context, completed answers/citations, limitations, separately labelled partial/error state | - |
| POST /generations | owner or current generation.request scope | Explicit scope and publication/draft intent; durable job | K |
| GET /drafts and GET /drafts/{id} | owner or current draft.write/draft.publish scope for the exact draft | Unpublished candidates/status with individual validators and captured base | - |
| PATCH /drafts/{id} | owner or current draft.write scope + draft If-Match | Reject preparing/ready work or replace a ready candidate from sealed result staging; retain prior candidate/history and captured base, never publish implicitly | K |
| POST /drafts/{id}/publish | owner or current draft.publish scope + draft If-Match | Publish ready candidate only if base is current; 409 on stale base, draft remains available | K |
| POST /scans | owner or project-scoped scan.request grant | Captured scope/depth/current target set/config_revision; no policy/budget expansion; durable 202 job | K |
| GET /jobs and GET /jobs/{id} | owner/scoped job principal | Confirmed state, coverage, attempts, cancellation/unknown-outcome reason and individual ETag | - |
| POST /jobs/{id}/cancel | owner or current job.cancel scope for the exact job | Durable cancel intent; late result application fenced; external cancellation may remain unknown | K |
| POST /jobs/{id}/resume | owner + current job If-Match + explicit reconciliation/retry decision | Safe resumed work only; new external attempt requires its recorded authorization | K |
| GET /jobs/{id}/findings | owner | Exact context, evidence, approval_required and individual finding/overlay validators | - |
| POST /findings/{id}/decisions | owner + finding If-Match | Body decision approve/reject; apply authorized overlay atomically, publication stays separate | K |
| POST /overlays/{id}/undo | owner + current overlay If-Match | Conflict-safe reversal, not replay of obsolete metadata | K |
| GET /configuration | owner | Effective redacted policy, config_revision and ETag | - |
| POST /configuration/plans | owner | {scope,expected_config_revision,changes}; library or project-scoped normalized diff, impact/restart effects and exact config revision | O |
| POST /configuration/plans/{id}/commit | owner + confirmed: true + expected config revision | One exact plan result; settings and impact confirmation use the same owner session locally or remotely | N |
| GET /events | authenticated, current event visibility | SSE-format notifications via bearer-authenticated fetch or polling; no private model reasoning | - |
| GET /operations/{operation_id} | original principal with current scopes | K/O-key receipt or committed natural-plan result; no cross-principal disclosure or fresh dispatch | - |

One-time source/provider setup and local filesystem operations may use the owner CLI; their remembered configuration is reused for routine work. Service installation, coherent backup/restore and portable-export execution run where the daemon's local files are accessible, without a mandatory new remote endpoint or a separate Haeryong administrator identity. Import/content/source-read endpoints address explicit logical resources rather than arbitrary server paths. One package download is the complete selected directory version, not a full backup. Included source material follows the configured read/export scope.

## Transfers and on-demand files

No endpoint imposes a Haeryong maximum on HTML bytes, result-directory bytes, file/page count, nesting, note/state text or output size. Clients use staged uploads, cursor/stream inventories and per-file reads; no giant manifest, JSON body, Blob or render envelope is required. UI/CLI preparation is automatic and does not ask the author to split a result into multiple documents. Actual filesystem, browser, network/proxy or provider failures identify their component and preserve useful staging; finite chunk/buffer sizes are transport mechanics.

Uploads declare purpose result/context/body/metadata and the authorized target scope. purpose=result uses directory/archive/file input; context/body/metadata use the same resumable single-object transport without publishing a document. The service/clients choose internal transfer labels automatically, not author payload filename requirements. Sealed uploaded-object references may supply POST contexts, metadata_ref inputs or large body_ref inputs to their owning mutation; that operation validates purpose, digest and current target authority before adopting the object. A context-only workflow cannot reinterpret its staging handle as a publishable result.

Large context/text/metadata must not depend on one uninterrupted HTTP body fitting a proxy's request ceiling. Clients can use the same checkpointed smaller-chunk upload path, then send a small reference-bearing request to the original operation. Direct-stream and uploaded-reference submissions normalize to the same exact object bytes and semantic context for retry identity; operation receipts still govern lost-response recovery. Finite proxy/chunk sizes constrain a transport step, not total saved input/output.

Upload path is an exact normalized relative UTF-8 payload path, percent-encoded once in transport. The client preserves the original selection_name separately from payload paths; all protocol preparation is automatic. The following v1 shapes are frozen only after their G-CONTRACT fixtures pass, and are the proposed shared HTTP/CLI semantics.

| Step | Proposed wire shape and required behavior |
| --- | --- |
| Create | POST /uploads with {purpose,target,input_mode,selection_name?}. result accepts directory/archive/file; other purposes use object. Validate the exact [purpose/target and authority table](import-workflow.md#purpose-target-and-authority), including draft-specific targets and body/metadata action variants. Return upload_id, state=open and checkpoint. This preparation has its own key, distinct from the final consumer key. |
| Directory file chunk | PUT /uploads/{id}/entries?path={encoded_path}&kind=file with raw application/octet-stream bytes, Haeryong-Upload-Offset (nonnegative decimal), Haeryong-Upload-Chunk-SHA256 (hex digest), optional Haeryong-Upload-Length (total entry length) and Haeryong-Upload-Final:true on the last chunk. A zero-length final chunk represents an empty file. The Haeryong client computes headers, not the artifact author. |
| Directory/link entry | The same PUT uses kind=directory with JSON {} or kind=symlink with JSON {link_target}. Entry identity is upload_id plus exact path/kind; a repeated identical entry succeeds and changed kind/link data fails 409 INPUT_CHANGED. No external link target is copied. |
| Archive/file/object mode | The service returns an automatically chosen transport entry label and the client uses the same file-chunk protocol for that one stream. The label is not an author filename requirement or an injected payload entry. Archive expansion produces the directory inventory; file mode preserves the selected filename; object mode seals opaque context/body/metadata bytes. |
| Chunk acknowledgement | Return accepted offset/ranges and the confirmed checkpoint only after persisting the accepted bytes and checkpoint. A byte offset and length describe bytes, never Unicode characters. Identical accepted bytes replay; changed bytes or incompatible overlap at an accepted offset fail 409 INPUT_CHANGED. Chunk digest mismatch is 422 HASH_MISMATCH. Overlap/gap rules never mark unreceived bytes complete. |
| Complete | POST /uploads/{id}/complete with {checkpoint}. Missing ranges/end markers fail 409 INVALID_TRANSITION with the required progress. Reserve complete input, enter sealing and forbid writes. A completed 200 receipt describes sealed staging; a completed 202 receipt describes upload_seal work, never publication. The same active checkpoint under a different valid key commits that request's own 202 receipt pointing to the existing job. Same-key replay returns its original outcome; a different checkpoint while sealing conflicts. See [completion recovery](import-workflow.md#composite-operation-ids-and-recovery). |
| Failure/recovery | Retain checkpoints and the actual error. Under the [coupled seal rules](import-workflow.md#cancelling-sealing-versus-abandoning-an-upload), return to open only after confirmed recoverable failure/cancel and worker stop, and only if the upload is not abandoned. Reconciliation may retain an already committed seal but cannot let a fenced attempt finalize or reopen an abandoned upload. A deliberate new completion uses a new key; a lost-response retry retains its original key/checkpoint. No content or progressing-transfer deadline is added. |
| Abandon | POST /uploads/{id}/abandon with {} atomically records abandoned and fences new writes/adoption/seal finalization. Record cancel intent on an active upload_seal job; finish it as cancelled only after stop is confirmed. A completed seal job is not rewritten. Preserve adopted objects and active/recovery pins; later failed-worker results cannot reopen the upload. Replay returns the same effect under current authority. |

upload.state is open/sealing/sealed/abandoned; adoption remains a separate reference relationship. GET /uploads defaults to open/sealing/sealed and accepts state filters and unadopted=true. The exact [summary/detail field table](import-workflow.md#upload-list-and-detail-shapes) governs all clients: list items have detail_url and summaries, while checkpoint, error and ranges/entries links are detail-only. Owner recovery can inspect/resume/complete/abandon after originating credential loss under current target authority. No original bearer is required.

[Import workflow](import-workflow.md) defines exact-target reuse, pin acquisition and seal cancel/abandon coupling. A sealed result may support a deliberately new base of the same document, never a changed project/document/draft target. reusable depends on actual bytes and current authority, not state alone. Cancel an upload_seal job to stop verification while retaining the input; only explicit upload abandon discards its future reuse. Do not reopen writes until the cancelled worker is stopped/reconciled. Detail/pin lists remain cursor-capable.

An idle connection may close and resume from its checkpoint without abandoning the upload. Staging cleanup requires proven abandonment or completed ownership transfer plus no active, unknown, saved or pending-adoption reference. Simple age, a lost connection, page close or expired renderer-delivery lease cannot establish upload abandonment, release such pins, discard progressing work or delete a completed library result. The selected janitor trigger and recoverable idle policy are implementation choices, not an artifact quota.

POST imports consumes a sealed snapshot and semantic intent. It does not read a still-changing client directory at commit. Optional Haeryong metadata is supplied explicitly inline or by uploaded immutable metadata_ref; any payload manifest.json is an ordinary file. Missing service IDs are assigned once to the import operation. A 202 receipt means durable accepted work, not a published partial directory; the job/operation result identifies the complete version only after atomic publication. Cancellation/integrity failure leaves no visible prefix version.

POST /imports uses the complete [canonical request and acceptance rules](import-workflow.md#client-association-and-import-request). Clients resolve remembered choices before transfer and send document_id explicitly; missing is INVALID_REQUEST and null selects a new document. project_id, when omitted, comes only from the sealed upload target. Ordinary UI/CLI send their pre-transfer base explicitly; low-level omission captures latest once at the first valid POST /imports reservation. Receipt replay reuses that captured base. No handler consults hidden workflow defaults, changes the target or captures a fresh base on retry.

GET file preserves exact content bytes and returns its strong content validator. Path lookup follows the [package exact-first rule](package.md#directory-paths-and-page-identity): only a unique canonical-equivalent Unicode fallback is allowed, ambiguity is INVALID_REQUEST with reason ambiguous_path, and case-folding/renaming never occurs. Report actual resolved_path and preserve requested_path/navigation_path when spelling differs. Range reads and resumptions use the same immutable object; a mismatched If-Range falls back to the complete current immutable object response, never stitches different versions. The loader verifies requested bytes incrementally or through verified chunk/object delivery as specified by the package/viewer contracts. Cursor file lists may filter by directory/page/preview type without excluding unlisted files from the stored snapshot. The renderer uses an unprivileged version-specific content root with these same exact path semantics, including root-relative links and concurrent pages; a mutable global current-version asset root is insufficient.

Without resolve_link, a directory or symlink returns typed metadata {path,kind,link_target?,files_url?}. With resolve_link=true, follow only links resolved inside the immutable version with that same exact-first lookup and report requested_path, resolved_path and the resolved file's validator. A directory still returns listing metadata; cycles, outside-tree targets and missing targets return NOT_FOUND with reason=unresolved_link to an authorized caller, without filesystem access outside the result. An unprivileged loader applies the same resolution. Canonical page/region identity uses the actual resolved HTML entry's exact page_path; optional navigation_path preserves an authored alias as navigation context, never as a new page or source identity. Range is defined for regular-file bytes, not directory/link metadata; unsatisfiable byte ranges return 416 without a success body.

Large JSON/text fields use an immutable body_ref, metadata_ref or context_ref plus digest and streamed retrieval. References are bound to the authorized record/scope and normalized by content identity for semantic request fingerprints. Saved notes/questions/jobs adopt context objects with their own pins, so disposing a render channel does not erase saved context. Control messages carry the handle; callbacks/native handles/owner credentials are never serialized as management authority. There is no total serialized-author-state cap or silent prefix truncation. Actual provider context/output windows constrain a selected AI call separately, not storage or rendering.

POST /contexts uses an explicit input selector and binding: input=direct (default) accepts the exact UTF-8 JSON or text request bytes under its Content-Type; input=upload accepts a small JSON {upload_id} naming a sealed purpose=context object. The input selector and binding use query parameters under [context binding normalization](import-workflow.md#context-binding-transport-normalization): version_id is required, and omitted page_path/render_instance_id becomes null. The upload's JSON target still requires all three keys explicitly. Fresh submissions must match the current channel/workflow and the complete normalized uploaded target; omission never clears or inherits a non-null render binding. After channel loss, an authorized owner recovery may resume/adopt the original persisted context/action binding under current version/target authority; it does not retarget the object to a new render_instance_id or claim that a new page supplied old data. A handle alone grants no recovery authority. Both forms normalize to identical context bytes/digest and binding for retry identity. The response supplies context_ref, bytes_sha256 and media_type; GET /contexts/{id} uses that exact context_ref as id. The host, not page code, performs this authenticated operation after the sender-bound context.transfer protocol. A ready context is not yet a saved note or question; an accepted Save/Ask transaction adopts it and its pins. A transient data reference is never an authentication credential.

GET /contexts/{id} also carries a closed **fresh-binding proof mode**, selected only by the presence of the `Haeryong-Context-Binding` request header: canonical unpadded base64url of one UTF-8 JSON object with exactly `version_id`, `page_path` and `render_instance_id` (JSON null as the only absent form; the same [context binding normalization](import-workflow.md#context-binding-transport-normalization) family). The mode additionally requires exactly one strong `If-None-Match: "sha256-<hash>"` naming the expected object digest from the host's receipt, and rejects a combined `Range`. In one transaction the server proves current record authorization, that the caller is the exact originating session, and that the persisted tuple and digest match; success is a `304` with no body and no Content-Length, carrying the record's strong `ETag` and the `Haeryong-Context-Verified: fresh` response marker (exposed to permitted cross-origin trusted callers). A mismatch returns `412 PRECONDITION_FAILED` (retryable false); malformed, duplicate, padded, CR/LF-carrying, non-canonically-encoded or structurally invalid header values return `400 INVALID_REQUEST`. This private mode redefines If-None-Match semantics relative to a standard conditional GET (match is a 304 success, mismatch a 412 rejection) and never streams bytes; the staged object is not opened for the metadata proof. Absent the binding header, the ordinary authorized recovery GET keeps its exact current behavior — including ignoring standalone If-None-Match/If-Match/Range headers with a full-byte 200 — and never emits the marker; `HEAD` remains unrouted. The planned M2 native transport maps a closed `verifyContextFresh` named operation (exact context id, immutable tuple and bytes_sha256, typed verified/ETag result, no byte-stream fabrication and no generic header passthrough) to this same header pair Go-side; until that bridge exists a native call fails honestly as an unimplemented operation.

## Concrete composed response shapes

Successful responses use `data`, `request_id` and applicable resource identity/revision/etag. Durable K responses and plan commits retain operation_id, including committed 202 job-creation and replay responses; the operation is not identified only by job_id. A read of absent review/Revisit/reading state follows the registry's explicit exists/create-precondition shape. Lists return each mutable item's validator, never require a client to use the collection validator for a member.

A saved question contains question_id, version_id, nullable page_path/region_id, artifact page_query/page_fragment, optional navigation_path, question text or body_ref, inline display_state or context_ref, source_mode, target_set_id, resolved targets, created_at and job_id. A non-null region_id always includes its exact canonical page_path. Each completed answer has answer_id, body or body_ref, created_at, project-source citations, separate explanation_references and limitations. explanation_references follow the registry's exact version/page/hash object and never claim project-source observation.

Completed citations follow the normative [shared citation union](schema-registry.md#source-and-assessment-views). observed_input requires an actual producing-attempt observation; posthoc_verified requires a successful captured/matched exact-target verification and does not prove original provider input. Model-authored IDs, paths or labels cannot create those records. Compose current availability and excerpt access without rewriting the original evidence.

These basis values describe evidence acquisition, not semantic claim support or complete coverage. Record the claim-support evaluation and its limits separately. Unobserved/unverified authored citations may remain in raw imported metadata or labelled incomplete output, but cannot be represented as a service-verified citation. An optional package_evidence_ref {version_id,evidence_id} is valid only where that immutable entry matches the same source/location/bytes; current-source work at B cannot reinterpret A's evidence or append evidence to its immutable version.

GET /questions/{id} composes authorized recorded excerpts or explicit current availability/bytes_origin/reason with citations. A citation does not authorize a fresh arbitrary source read. If requested pinned/current source is unavailable, persist the limitation or unable_to_verify outcome; never silently substitute explanation-only success or an empty verified target set. Clearly labelled explanation-only commentary may cite supplied explanation bytes without claiming project source was checked. Partial output remains distinct from a complete answer and the end of a stream does not upgrade it. Provider session/request IDs remain server-side in attempt records.

Assessment history returns assessment_id, version_id, scope {kind:version|page|region,page_path,region_id}, target_set_id, coverage, state, evidence/rationale/limitations, origin, config_revision and time. Applicable freshness has source_intent, target_set_id, captured_at, scope, status, coverage {coverage_set_id,unit_kind:version|page|region,eligible,assessed,complete,units_url}, assessed_at and reassessment_required. The coverage resource enumerates the fixed required tuples {kind,page_path,region_id} with outcome, discrepancy, reason and nullable assessment_id plus target_set_id. Summary counts derive from these persisted records; omitted/blocked units cannot disappear from the denominator. Counts are nonnegative integers from an explicit eligible scope; complete is false for zero/unknown eligibility, missing target or partial/unavailable required coverage. The registry defines aggregation and target precedence, including compatible same-target page contributions from separate coverage sets without overlapping counts. Return each contribution's source assessment/coverage-set identity. Unapproved proposals remain findings. The reassessment flag can coexist with an outdated status and is not a synonym for one status value.

Topic-assignment PUT accepts {pinned,decision}, with decision accepted/rejected; pinned plus rejected is invalid. Rejected applied assignments retain prior evidence/history. Reading-position PUT accepts {page_path,page_query,page_fragment,opened:boolean} and changes only the owner/version last-opened pointer and service-assigned first/last-opened timestamps. A null page_path is the whole-version/listing context. GET links the selected page-position resource without lending its pointer ETag to that record. Page-position PUT accepts {page_query,page_fragment,position} for the exact query page and uses its own owner/version/page validator; position may carry page-local region and offset/progress. Saving it never silently moves the version pointer. Reader-preference PUT accepts {default_page:path-or-null}; a valid selected HTML path overrides the immutable default for that owner/version, and null clears the preference. Explicit page/deep links remain first priority. Continue follows the version's last-opened pointer, whereas open-without-page chooses personal default, immutable default, root index.html, sole HTML, then Files/Pages in that order. None of these actions publishes a version or changes Review. Clients coalesce and serialize autosaves per resource using the latest acknowledged validator and one ID per submitted save, not one receipt per scroll event. On 412, retain unsaved state, re-read and reconcile navigation intent before another write.

Source status uses the registry's immutable source identity, retention_mode, availability, bytes_origin and typed reason. GET /status composes service (readiness/runtime_generation), provider (implemented capabilities and normalized availability/limits without account secrets) and backup. Backup exposes mode=manual when no schedule is configured, last_complete_verified {set_id,completed_at,verified_at,age_seconds} or null, last_attempt {job_id,state,stage,completed_at,error?} or null, and last_trial_restore {state:not_run|passed|failed,completed_at,error?}. A not_run trial has completed_at:null; errors are sanitized and no raw path is returned. A latest failed/incomplete attempt does not erase the previous complete verified set. No complete set is shown as not yet created; checksum verification cannot imply a trial restore ran. Storage reports staging_bytes, pending_upload_count and pending_uploads_url from upload/reference accounting, without a quota or expiry policy. Unsupported sections say unavailable with capability/reason, not fabricated healthy status.

Pairing rejection acts only on pending or approved-but-unredeemed requests, atomically prevents redemption and produces rejected polling status. Reject/approve use the normal K receipt rule; an already issued session is revoked through the session route. Client Cancel merely stops polling and forgets the polling secret, leaving server expiry or owner rejection to close the request.

Draft PATCH {action:reject} is valid for preparing/ready and records rejection plus durable cancellation/fencing of linked work. {action:replace,upload_id,metadata_ref?} requires ready and a reusable sealed purpose=result upload whose target.draft_id equals the draft in the route and whose project/document intent also matches. Null or another draft_id is INVALID_REQUEST; owner authority does not retarget the handle. Any metadata_ref must match the metadata/draft_replace target and this operation ID under [upload purpose and authority](import-workflow.md#purpose-target-and-authority). Replacement creates a new draft revision while preserving its captured base and prior candidate/history, and does not publish. Published drafts cannot be edited. Changing a stale base requires an explicit new linked generation/draft workflow, not overwriting base_version_id through replacement.

A finding's Create draft action uses POST /generations with finding_id, expected_finding_revision, intent=draft and the explicit captured generation scope. Request reassessment uses POST /scans with that originating finding identity/revision when applicable and its captured scope/depth/target selection. The service checks current finding applicability, source/provider authority and budgets. Neither action approves other findings, changes policy or publishes content. Version comparison composes the two authorized path-ordered file inventories and metadata; no separate diff endpoint or manual topic CRUD is implied by the UI.

POST /jobs/{id}/resume carries {decision:reconcile_only|continue|retry,expected_attempt_generation}. Reconcile_only observes existing work without a new dispatch; continue resumes only an uncancelled attempt with a confirmed safe continuation; retry requires the explicit retry decision and current scope/budget. Cancel intent is monotonic: a cancel-marked job cannot dispatch or apply through resume, including via paused to queued. Further requested work uses a new linked job. See the job contract for unknown/orphan accounting.

### Versions client composition (TASK-004-05)

The shared S05 screen reuses document/version reads, two exact path-ordered file
inventories, verified supplied metadata, file streams and assessment coverage. It
merges finite cursor pages in unsigned UTF-8 path order, exposes unfinished/error scope
and retries the failed cursor. Completion requires both inventories to end. File kind,
digest and symlink target changes remain structural observations; metadata/region
comparison retains exact page-local identities. Unavailable metadata is not an empty
successful comparison. Text previews read blocks on demand and verify complete bytes;
unsupported content retains original-file download. TASK-004-05 added no diff or
Review API; TASK-005-01 now supplies Review/history and the last-Reviewed default.
The [route registry](schema-registry.md#deep-link-and-versions-consumption-task-004-05)
owns additive S05 selection queries, copied-link base resolution and login return.

## Imported evidence verification

POST /versions/{id}/source-evidence-verifications?evidence_id={evidence_id} accepts JSON {} or optional {expected_bytes_sha256,expected_excerpt,expected_excerpt_ref}, plus the required operation key. It checks the immutable authored evidence's exact source identity and bounded location under current version/source authority. It never resolves that evidence to a newer branch revision, dispatches AI or changes capture_status. Expectations are optional: the digest covers the exact bounded captured bytes, and an expected_excerpt must equal those entire bytes encoded as UTF-8, without trimming or Unicode/line-ending normalization. All supplied expectations must match. Binary expectations can use a digest without text. expected_excerpt and expected_excerpt_ref are mutually exclusive; expected_excerpt_ref uses the existing purpose=body immutable body_ref transfer bound to this exact evidence/check operation. Both forms compare the same exact UTF-8 bytes and normalize identically for retry identity.

The synchronous operation creates a durable check record and returns 201 with data {operation_id,verification_id,version_id,evidence_id,outcome,target_set_id,source,location,bytes_sha256,checked_at,verified_at,reason}. outcome is captured when exact target bytes were obtained without an expectation, matched when every supplied expectation matched, mismatch when any comparison failed, or unavailable when the exact target could not be obtained. target_set_id/bytes_sha256 may be null when unavailable; verified_at is populated only for captured/matched, while checked_at records every actual check. A 201 means the check record was saved, not that source verification succeeded; clients display its outcome. Invalid input or unauthorized identity fails with ordinary registry errors before disclosure. The record retains supplied expectations or their immutable referenced bytes, permissions/config revisions and any retained-object linkage, without placing a required quote in author metadata.

GET /versions/{id}/source-evidence/{evidence_id} returns immutable authored metadata and a separate nullable service_verification (latest checked_at, then ID) with verifications_url. GET that /verifications URL lists prior records with cursors. These reads report current availability and disclose excerpts only under current source authority; they start no new source/AI work. A later restriction may make bytes unavailable without rewriting any earlier result or original generation manifest. No record becomes observed_input or proves semantic claim truth. Mismatch/unavailable records do not qualify as verified citations; captured means only limited exact-target resolution/capture. Acquire allowed existing/temporary source pins before checking, verify the same pinned bytes, recheck current authority through completion and release only when the owning check/reference no longer needs them, following source-retention policy.

## Preconditions, receipts and retry matrix

The receipt primary key is `(principal_id, operation_id)` across route families. The fingerprint includes method, actual canonical route/target, validated body and original semantic preconditions; see the shared registry for upload identity and excluded transport fields. The caller can retrieve it using just its operation ID and principal. Import receipts retain their original caller intent and one normalization_context for generated IDs, created_at, defaults and normalizer/schema version. An existing operation reuses that context before deriving a metadata digest, including when an equivalent sealed upload has a new upload_id. Incoming caller fields/metadata/content/preconditions are still compared as originally supplied; saved generated values cannot mask changed input. Transport timestamps and freshly guessed defaults are not part of retry identity.

Order: authenticate/current authorization; receipt lookup and fingerprint comparison; matching receipt replay/reconciliation; only then check original preconditions for new work. This permits recovery after a successful PUT's response was lost while rejecting a new stale edit. A replay never returns a now-hidden body or bypasses permission revocation. Completed receipt metadata is kept sufficient to reconcile without rerunning domain effects. accepted means only a durable reservation: an identical concurrent request receives the registry's stable pending response and polls operations, without a second effect. Durable job creation atomically commits a completed receipt with original_status 202 and operation_id/job_id; subsequent job failure/completion never rewrites that response. reconciling is reserved for recovery uncertainty, not ordinary in-progress work.

| Operation class | Rule |
| --- | --- |
| K mutations/jobs/import/publication | Each request's Idempotency-Key equals its own receipt operation_id. For a composite CLI command, --operation-id selects only the final domain request; upload creation, sealing and other preparation use distinct persisted UUIDs under [composite IDs](import-workflow.md#composite-operation-ids-and-recovery). Keys use one unquoted lowercase canonical UUID per request intent. Missing key is 428 PRECONDITION_REQUIRED (details.missing Idempotency-Key); invalid/quoted/multiple keys are 400 INVALID_REQUEST. An identical accepted reservation returns stable 202 pending data {operation_id,state:accepted,operation_url}; a committed response replays its original outcome. Changed request is 409 OPERATION_CONFLICT. PUT has the same receipt semantics. |
| Lifecycle/config plan commit | plan_id is the natural operation/result lookup ID when no key is supplied. Identical committed replay returns its original result after current authorization. An optional key aliases the same plan/request outcome and cannot overwrite a receipt for unrelated work. A new commit of an expired unconsumed plan is STALE_PLAN; changed inputs conflict. |
| Pairing request/redemption | No operation receipt is created; use pairing_id and polling status. The polling secret is a single-use redemption capability. Lost approved-redemption response never mints a second credential; reconnect starts a fresh request. Approve/reject remain K operations. |
| Session revocation | No separate operation receipt; session_id is the natural identity. Already revoked returns confirmed revocation if current authority permits it; revoking the caller's own credential may produce 401 on repeated logout. |
| Validation and plan preview | A supplied key creates a retrievable operation receipt with ordinary fingerprint rules. Without a key, use the returned preview/plan identity or request a new bounded preview; no hidden mutation receipt or domain effect is implied. |
| GET/status/replay | Never redispatch an AI request or apply a mutation just to reconstruct a response. |

Use individual If-Match for mutable resources and If-None-Match: * for create-if-absent personal state. Require 428 when a listed precondition is missing and 412 when it is stale. For a new commit, plan_id, expected revision and confirmed: true describe the exact owner-approved change; the service rechecks current target/config revision and owner session. Confirmation is not an authentication token. Receipt replay is not a new commit.

Unfinished/unknown external receipts cannot expire through diagnostic retention. A missing receipt after restore does not authorize repeating uncertain work. A client observing a changed runtime_generation fetches confirmed state and uses explicit reconciliation; it does not invent a new operation ID to retry automatically. Never return mutation success before durable commit. SSE disconnection is not proof of failure.

## Response, pagination and errors

The proposed list batch size is 50 and may adapt to response buffering; it is not a total-result or imported-file limit. All entries remain reachable through cursors/streaming. Mutable library lists use updated time plus ID; immutable file lists use exact path order and bind the cursor to the version/content set. Every cursor is bound to filters and runtime_generation. Reject a filter/cursor mismatch. Permission/lifecycle filtering precedes pagination/counts/snippets. Concurrent updates may require a fresh list; do not claim a frozen snapshot unless a snapshot token is explicitly returned.

The [error registry](schema-registry.md#error-registry) is shared by HTTP and CLI. Errors have code, a readable message, retryable and authorized details, with a cursor/object reference for additional issue data when needed. 412 preserves the client's unsaved draft; provider auth/quota errors do not pretend the owner's browser session expired. A typed unavailable evidence view may return 200 when its identity is authorized, while a new action requiring those unavailable bytes fails with the registered reason. The explicit verification operation instead records a typed unavailable check outcome; creating that record does not claim a successful source read.

## Events, downloads and shell routing

Composite commands follow [distinct preparation and final operation IDs](import-workflow.md#composite-operation-ids-and-recovery); a final import/question key is never reused for upload creation or sealing.

Both local and remote browser event delivery use fetch with Authorization and an SSE parser, or authenticated polling. The management bearer comes from trusted origin storage or the device's optional memory-only choice; it never appears in EventSource, asset, download or copied-document URLs. Cookie streams are not used. Large downloads use streaming handoff to destination storage without an all-in-memory Blob requirement. Browser-initiated asset/download transport follows the normative [content-delivery authority](authentication.md#content-delivery-authority), including any explicitly selected limited credential, repeated Range requests, automatic recovery and access termination. Management bearers never enter the renderer. G-VIEW/G-AUTH must demonstrate the selected Wails/private-browser construction; this API summary does not create a second credential policy. The unprivileged loader preserves normal HTML/JavaScript/module/worker/network behavior without receiving the owner bearer.

[Jobs/events](jobs-events.md) defines stream_epoch/sequence replay, restore resync and invalidation for already displayed resources. New restrictive policy, trash, purge or access loss must invalidate a renderer even when normal subsequent resource reads/events are denied.

The content-free shell serves routes from the [canonical route registry](schema-registry.md#browser-route-registry), including direct deep links. It checks session and capabilities before private metadata. Preserve exact internal version/page/region and encoded page_query/page_fragment through login; reject external/protocol-relative shell return routes. A local-only copied URL may use the configured loopback base but never makes that address canonical identity. An old client rechecks capabilities/runtime_generation and fails safely on incompatible contracts; it cannot migrate the DB.

## Current owner-session implementation

[TASK-002-03 evidence](../testing/authenticated-sessions.md) records the implemented pairing/session/device list/revocation, local job read/cancel, receipt reconciliation and authenticated SSE subset. The content-free trusted connect shell runs on the same listener. Private future feature routes remain authenticated but unavailable. Status currently reports the storage/identity foundation, not fabricated provider/backup capability. Pairing creation/polling/redemption and session revocation retain their natural identities; only approve/reject and job cancellation use K receipts here. DELETE session routes add no mandatory Idempotency-Key or If-Match.

The selected auth request/response shapes and existing authentication error mappings are additive machine-contract selections. Mandatory individual If-Match on approve/reject is an owner-approved breaking requirement for clients that omitted that header; no earlier production auth API was delivered. The old G-AUTH spike's short routes remain historical experiment adapters and were not rewritten. Successful poll is the single-use redemption boundary; no new `/redeem` endpoint or wire `redeemed` state is introduced.

## Current upload/import implementation (TASK-002-04)

The same authenticated listener now implements upload creation/list/detail, entry/range/pin discovery, streamed entry transfer, completion, abandon, POST /imports and sealed-upload POST /packages/validate. [The transfer contract](import-workflow.md#implemented-backend-transfer-boundary-task-002-04) defines end-input checkpoint promotion and replay/admission ordering. Jobs/events/operations retain current authorization and result visibility. New upload, inline metadata and file bytes are streamed independently of the bounded authentication-control JSON envelope; no authored-content quota is introduced. Known future capabilities remain FEATURE_NOT_READY, and unknown routes remain NOT_FOUND. Query/file delivery, final import/validate CLI and renderer integration are not supplied by this subset.

## Current query and configuration delivery (TASK-002-05)

Current implemented read routes include capabilities, projects/documents/versions and individual items, version files/file/metadata/render-bootstrap/source-evidence, and existing principal-scoped operations/jobs/session/events. Configuration/status use the existing same-listener owner boundary. Unknown routes remain404; known unavailable catalog capabilities return503 FEATURE_NOT_READY, while actual storage/service failures retain their typed failure codes. Capabilities report actual query/stream/resume support; stored viewer_profile html_js_v1 does not advertise an implemented renderer or loader.

Project/document cursor order is observed `updated_at, id`, ascending, with exact lifecycle/current scope predicates applied before pagination. Migration backfill is observed migration time. Semantic changes update ordering; no-op/replayed operations do not. Cursors bind library runtime identity, route/filters, principal/effective scope and current project permissions; normal restart preserves runtime identity, while mismatch requires fresh reads. Version lists order immutable sequence; file inventories order exact UTF-8 path. File `directory` filters include all descendants after exact-first/unique canonical lookup. Page size1..200 is a transport selection, not a total count limit.

The selected concrete read DTO fields are in the [registry](schema-registry.md#selected-current-query-and-configuration-shapes-task-002-05) and machine definitions. Normalized metadata streams the verified full generated manifest; `kind=supplied` streams exact supplied metadata or404 with reason metadata_not_supplied. Source evidence returns authored identity plus separate nullable service verification and the principal-specific retained/recorded/not_read_by_view states delivered by TASK-004-01; it performs no fresh Git read.

Regular-file delivery uses one verified pinned handle for full/Range/HEAD/conditional reads. Strong ETag and Repr-Digest identify the full immutable representation. Content-Digest is present for the complete200 response body, and omitted for206/HEAD; Content-Range describes206. Empty/unsatisfiable ranges return416. Requested/resolved-path headers use URL-query escaping and preserve literal names. Current authority/visibility is rechecked during delivery; bytes are withheld on stored integrity failure and read-owned pins are released. Attachment, nosniff and sandbox policy prevent imported HTML from executing in the trusted host origin.

Poll and SSE validate the entire available batch's sequence continuity before emitting domain events or advancing a cursor. A first/interior gap, foreign epoch or invalid/future cursor returns content-free resync with the current head. A hidden target is a content-free invalidation. Resync does not acknowledge unsaved client work. Status storage fields report distinct upload-owned retained-object bytes, pending open/sealing/sealed upload count and the pending-upload URL; deduplicated hash accounting is not a filesystem free-space estimate. Unavailable provider/scheduler/backup sections give explicit reasons.

## Reader foundation delivery (TASK-003-03, in progress)

The renderer-session routes above are implemented alongside the independent
reader-preference, reading-position and page-position resources. This is an
implementation boundary, not Task acceptance or browser/native qualification;
[the Reader record](../testing/reader-viewer.md) tracks unfinished work.
Render control accepts an empty body or `{}` only. Null, unknown fields and
trailing values fail before construction or operation reservation. The open K
replays its original response; renew/close use the exact natural session ID.
Closing is terminal and idempotent, including when its document is hidden.

Open returns `{version_id,render_origin,session_id,handoff:{url,secret,expires_at},lease_expires_at}`.
The one-use secret is submitted by form POST to the renderer, never placed in a
URL. The renderer-only HttpOnly cookie grants no management authority. Origin
ownership remains durable after delivery close and daemon restart. A completed
open receipt does not resurrect its ephemeral handoff after restart; an active
selection obtains a new session under current owner authority.

Capabilities and bootstrap reflect the actual wired renderer facade without
constructing it during a read. An unavailable facade retains authenticated
attachment delivery. Bootstrap's loopback construction adds `render_sessions_url`
and `renderer_construction`; it contains no authored HTML, bulk assets or bearer.
Selection-specific bind/exhaustion failures remain explicit open failures.

Renderer failures use the existing error registry and `details.reason`:
unknown session is `NOT_FOUND`; closed/stale delivery, pool exhaustion, bind
conflict or definitive delivery denial is `INVALID_TRANSITION`; unsupported
account lookup is `FEATURE_NOT_READY`; ledger/binding repair or transient owner
lookup uses `SERVICE_RECOVERING`. Only the transient owner check is automatically
retryable among these renderer conditions. Pool exhaustion supplies the explicit
pool-expansion/restart remedy. A hidden document's delivery denial does not
revoke a still-valid owner session or become an indefinite transient retry.

## Evidence presentation consumption (TASK-003-04)

The single-item query alias uses the same evidence read, version visibility and
post-read authority guard as the legacy path form. It accepts exactly one
nonempty `evidence_id`, decoded once with ordinary URL-query rules; unknown,
duplicate, empty or malformed query values are 400, and a missing ID is 400
`evidence_id_required`. The legacy item path continues to reject all query keys.
Thus `.`, `..`, literal `%2e`, slash-containing and Unicode IDs remain distinct
without an author naming restriction or double decoding.

The browser uses this item alias; the native Go item read retains the exact
escaped legacy path. Both clients use the fixed history query alias, and every
service-composed `verifications_url` names it. This changes a derived locator,
not stored authored metadata. TASK-004-01 now implements GET history and the
canonical query-form POST on that alias. The legacy path-form check/history
also remain available; clients use the query alias for lossless local IDs.
Both history forms remain GET-only: HEAD is unknown (404) with no response body.
The POST on the query alias is the separate explicit verification action.

The implemented `GET /versions/{id}/metadata?kind=supplied` and
`GET /versions/{id}/source-evidence/{evidence_id}` now also serve the shared
Reader's evidence presentation. The supplied-metadata stream carries the
immutable object's strong ETag beside the exact bytes; verifying every supplied
validator over the complete bytes before parsing is the documented client
obligation in the
[registry selection](schema-registry.md#selected-current-evidence-presentation-shapes-task-003-04).
Evidence item views keep the authored records separate from the current service
truth, including retained-byte availability and the explicit `not_read_by_view`
state described below. They never start a new Git read or upgrade `capture_status`.
A successful authorized retained-byte view optionally includes
`excerpt:{bytes_sha256,data_base64?}`. The digest covers the exact bounded bytes;
data_base64 carries their complete encoding only for a nonempty selection of at
most 64 KiB. Larger or empty selections keep the digest and omit data_base64.
No excerpt is emitted for denied/unavailable/not_read_by_view states, and no
truncated payload or source/content quota follows from this inline display bound.

TASK-004-01 implements verification history, assessments and coverage reads as
specified below. Assessment producers and semantic verdicts remain future work.

## Source observation delivery (TASK-004-01)

Compatibility: implementing the catalogued check/history/assessment routes, the
`bytes_origin` result field, recorded source status, error details and
`features.source_verification` is **additive** within runtime schema 1.0.
`POST /versions/{id}/source-observations` and the selected project-source view
are **additive provisional** contracts. They introduce no config schema change,
AI call, job kind, import gate or semantic assessment producer.

| Route (under /api/v1) | Current authority and result |
| --- | --- |
| POST /projects | Owner; K; `{name}` (nonblank UTF-8, at most 128 characters), 201 shared project projection including ETag; generated identity is replay-stable |
| GET /projects/{id}/sources | Owner or current project.read in this project; `{items,next_cursor}`, sorted by source_id; logical identities only |
| POST /versions/{id}/source-evidence-verifications?evidence_id=... | Owner or current version.read + source.read for the target; K; 201 check result or stable 202 |
| GET /versions/{id}/source-evidence-verifications?evidence_id=... | Same evidence-view authority; `{items,next_cursor}`; optional cursor/limit; legacy path history supports both too |
| POST /versions/{id}/source-observations | Owner only; K; 201 run result or stable 202; scoped callers receive FORBIDDEN |
| GET /versions/{id}/assessments | Current version/anchor authority; target_set_id, scope_kind, page_path, region_id, cursor, limit; `{items:[],next_cursor:null,applicable}` in this Task |
| GET /versions/{id}/assessments/coverage | Current version/scope authority; required coverage_set_id, optional attempt_run_id, cursor, limit; `{items,next_cursor}` |

The verification POST accepts `{}` or optional `expected_bytes_sha256`,
`expected_excerpt`, `expected_excerpt_ref:{upload_id}`. Excerpt and reference
are mutually exclusive; null expectations are invalid. Inline UTF-8 excerpt
bytes are bounded to 64 KiB; larger text uses the immutable body upload bound
to this exact operation, version and evidence. Inclusive 1-based line bounds
retain line terminators, CR bytes and a missing final terminator. A digest
covers exactly those selected bytes. No text normalization is performed.

The 201 `data` is `{operation_id,verification_id,version_id,evidence_id,outcome,
target_set_id,source,location,bytes_sha256,bytes_origin,checked_at,verified_at,reason}`.
Target, hash, origin, verified_at and reason are nullable; verified_at is set
only for captured/matched. Outcome is captured, matched, mismatch or unavailable.
`source` preserves the exact recorded kind/retention/repository/revision or
snapshot identity; `location` is path with optional line_start/line_end.
History rows are `{verification_id,outcome,checked_at,verified_at}` with reason
only when applicable. Stored errors replay their original request_id.

The observation request is one closed JSON object: `{}` or optional
`source_ids:[source_id,...]`, `scope:{kind,page_path?,region_id?}`. Scope is also
closed and non-null. Unused anchors may be omitted or empty strings (never
null); page requires page_path and region requires both. Unknown keys and null values fail 400 before reservation.
Omitted scope means version. Selection is cited Git sources linked to approved
mappings, optionally narrowed by source_ids, and must be nonempty.
The 201 `data` is `{operation_id,run_id,target_set_id,captured_at,observed_at,
scope,coverage_set_id,sources}`. Its **five-field** scope is
`{kind,page_path:null|string,region_id:null|string,declared:boolean,reason:null|string}`,
identical to the copy stored on the run and coverage set. An undeclared region
records declared false/reason region_not_declared and fabricates no unit.
The fixed three-field applicable.scope does not gain these two fields.
`sources`, ordered by source_id (including recovery), contains
`{source_id,observation_id,availability,classification,reason,changed_paths:[],
dependency_scope:[],reassessment_required}`. Availability is available,
unavailable or denied; classification is direct, indirect, unchanged,
unavailable or denied. Nullable target/capture time denotes no valid capture;
an attempted denominator may still have a non-null coverage_set_id.

A missing checkout, current object **or declared base object** records
missing_source. Ambiguous approved linkage fails pre-reservation with 409
SOURCE_UNAVAILABLE, details.reason mapping_ambiguous. A nonexistent requested
page fails pre-reservation with 400 INVALID_REQUEST, details.page_path. Neither
creates a receipt/intent. Invalid or mismatched cursors and page sizes return
400 (cursor_mismatch / invalid_page_size). Authorized missing target/set/attempt
reads return 404 NOT_FOUND with respectively details.target_set_id,
details.coverage_set_id or details.attempt_run_id (alongside the existing reason);
otherwise retain nondisclosing 404 behavior.

Stored post-reservation error envelopes have **retryable:false**, even 503:
400 INVALID_REQUEST / expected_excerpt_ref_not_utf8; 412 PRECONDITION_FAILED
for policy changes; 503 SERVICE_RECOVERING / stage_failure,
check_bound_exceeded or interrupted_before_commit; 503 FILE_ACCESS_ERROR /
git_unavailable, source_read_failed, expectation_read_failed or
missing_retained_bytes. Authority invalidation settles its actual 401/403/404
class. The pre-reservation 409/400 errors above also have retryable false.
An adapter-level infrastructure error that could not be stored returns 503
with retryable true and Retry-After: 2; do not reinterpret a stored 503 as
permission to dispatch a new operation. Reconcile its receipt first.

Identical in-flight K requests return stable 202
`{operation_id,state:"accepted",operation_url}`. Browser/native/desktop and CLI
keep the **original key** for 201/202 and uncertain responses, read the named
operation until definitive, and never repeat the POST merely to recover a lost
response. The shared controller-owned intent store binds library, principal,
version and evidence; it survives panel/route/runtime remounts and retains
pending/completed/failed outcomes until the active consumer consumes them.
A replaced selection cannot receive the old result. Refresh uses the current
selection guard. Registration/configuration flows retain the same intent
ownership through remounts; cancelling a preview discards only that flow's
preview, while an in-flight commit cannot be cancelled as an unsent preview.

`GET /status` adds source
`{available,reason:null|string,configured_source_count,last_observed_at:null|time}`.
It uses configuration and the latest per-source **observation-run** rows, never
Git/filesystem probes or verification checks. All configured sources must have
an available observation for available true. No configured/observed source
means available false/reason not_observed. Failure precedence, strongest first:
source_not_registered, missing_source, missing_retained_bytes,
retained_bytes_corrupt, source_denied, not_observed. A later weaker observation
replaces that source's prior status; aggregate precedence applies after this
replacement. Storage failure returns 503 SERVICE_RECOVERING, never fabricated
not_observed health. See [selection and scope rules](schema-registry.md#source-and-assessment-views).


## Provisional static search selection (TASK-004-02 M1)

M4 implements this additive provisional selection. M5 ships the shared Library
UI and all three transports together and enables `features.search=true`. GET/HEAD `/api/v1/search` accepts only q, project_id, lifecycle
(active default/archived/all), versions (latest default/all), optional kind
(document/page/region/source/attachment), cursor and limit (50 default, 1–200).
Only q may be empty. Duplicate, unknown or other empty fields are invalid. HEAD
uses the same status/headers and no body. Successful GET uses the closed
`search_response` `{data,request_id}` schema; see the [registry](schema-registry.md#selected-static-search-foundation-task-004-02-m1m2).
Empty q produces no MATCH, zero items/count and honest index status/reasons.
Pending/partial is HTTP 200 rather than a false complete no-match.

Selected 400 INVALID_REQUEST details.reason values are invalid_query,
query_too_long, malformed_phrase, invalid_lifecycle, invalid_versions, invalid_kind,
invalid_page_size, cursor_mismatch and unknown_duplicate_or_empty_query.
Authentication remains 401; absent version.read is 403; hidden targets are
nondisclosing 404. Recovering/timeouts use 503 SERVICE_RECOVERING, retryable=true,
Retry-After: 2. A query deadline has details.reason=query_timeout; other
search storage failures have empty details. Both use the same bodyless HEAD guard.
Eligibility reuses source.Check unchanged: owner or existing version.read with
both project and exact-version scope anchors, before results/counts/snippets/
coverage/cursors/events. No search.read, source.read or project.read is added.

M4 search evaluates literal AND clauses, unicode61/trigram candidates and a direct
1–2-character fallback over the eligible ready corpus. Long phrases nominate a
bounded prefix candidate superset, then verify the entire normalized literal in
one source chunk. Aliases may satisfy unquoted components only. Repetition and
hidden corpus frequency do not increase rank. Order is exact identity/title/path,
literal title/region phrase, literal path/topic/source phrase, full components,
then substring. Descending per-clause field score weights are title=8, region=6,
path=5, topic=4, source=4 and body=1, once per clause/field. Equal (tier, score)
results compare a one-based ordinal within exact (version_id, page_path) groups,
computed in the existing tuple order over eligible fully matched one-hit-per-unit
results before cursor/LIMIT, resetting per strength block. Unchanged keys follow:
project, document, sequence descending, kind, binary page_path, region_id, source_id.
Ordering revision 2 invalidates pre-A1 cursor bindings; the strict decoder rejects
missing, zero and negative ordinals.
Candidate discovery and snapshot streams examine at most 64 physical rows per
transaction, including misses, and yield after the first completed row at an
8 ms row-iteration budget. These are work budgets, not author input limits or a hard
wall-clock promise for an individual SQLite operation. SQL preparation and
nomination have the independent 64-row work bound; their time is recorded
separately from each iterator's elapsed budget. Snapshot versions, page
and file inventory, unit facts and metadata chunks are paged separately; counts,
validation projections, encoding and hashing run outside the store lock. Selected Store-owned visibility generations bracket the complete snapshot.
Visible changes between its pages require a restart; hidden publications, staged
pages and quarantined unit/chunk/anchor cleanup do not. Physical connection
replacement preserves those generations. Each candidate slice checks current authority
generations and applies the captured eligibility relation; full authentication
runs at the start and end, and a changed authority generation requires the
ordinary full check. Authentication, permission and explicit-target failures
retain their normal error classes. Start/end selected visible-state bindings
must match. A visible change restarts once; a second change returns retryable
503 query_timeout rather than mixed results. Hidden-only changes between
complete snapshots preserve the selected binding. The 30-second request
deadline starts before the first authentication, store-lock waits observe
cancellation, and post-processing checks cancellation before successful return.
Empty queries return current corpus coverage without matching. `kind` narrows
hits rather than coverage. Coverage reasons count distinct selected versions and
never expose job errors or private diagnostics.

Search cursors bind the normalized parsed clauses, filters/page size, principal,
library/runtime/auth/stream identity and selected visible version/index/unit
projection. In all-version mode they exclude mutable document revision/title/
latest and hidden publications. Visible changes return 400 cursor_mismatch;
normal restart with unchanged selected state preserves continuation. Search GET
never enqueues work, reads configured sources or invokes AI. Unsupported methods
retain ordinary protected route handling. No maintenance/retry route is added.


## Implemented pinned-reference reads (TASK-004-03 M3)

The product router selects `GET/HEAD /api/v1/versions/{source_version}/references`
and its `/resolve` child before the preserved future-route catalogue. List accepts
only `cursor` and `limit` (default 50, range 1–200), ordered by the binary immutable
metadata reference ID. Data is exactly `{items,next_cursor,materialization:{state,reason}}`.
Materialization is `pending/materialization_pending`, `ready/null` or
`failed/materialization_failed`. Ready describes source declaration completeness,
not target resolution. Single resolution requires `reference_id`, decoded once
as a query value; optional `page_path` and `region_id` describe the source context.
A region requires its page. No supplied target or latest-version switch is accepted.

Authenticate first, then apply `source.Check` to the referring version and target.
Current intersected `version.read` and exact project/version anchors suffice;
`source.read` is not required. Active and archived targets are eligible. Unknown,
mismatched, denied and trash targets give an unavailable list row, with null
label/target/edge/route, or a generic 404 with empty details for single resolution.
D4 historical gone requires an existing consistent structural edge, a retained
purged document, source eligibility, and current target authority (owner or exact
project/version anchors with `version.read`). List returns a minimal tombstoned
target; single returns 410 `TARGET_GONE` with only `document_id` and `version_id`
in details. No prior read witness is required; no purged bytes/title is inspected.

Read-only projections never create edges, enqueue work or update reference/job/
checkpoint/revision/outbox state. Eligible but incomplete structural work returns
200 `unresolved` with `resolution_pending`; missing/ambiguous/pending/drifting
anchors keep their specific safe reason. A bound click first proves the static
marked source context in verified bytes outside the writer transaction. Reference
placement shares the search extractor's HTML element scope/implied-end grammar;
valid omitted end tags stay valid, while links in later paragraph/list/table
siblings fail `source_context_mismatch` before any target projection. Detached
work rejoins current authority and the same immutable inventory/anchor snapshots
before output. Target lifecycle is checked before target anchor diagnostics.

Cursors bind library/runtime, current principal/scope, source manifest/reference
revision/materialization readiness revision, limit and last binary ID. Target
existence, indexes, edges and outbox heads are excluded. A target-only update may
change a row's live resolution, never its membership or cursor bytes. Enumeration
uses at most 64 rows per transaction and yields after 8 ms of cooperative work.
The 30-second request deadline starts before outer authentication. The resolver's
own store operations and lock waits observe cancellation, and successful output
checks the context again. The existing foundation reader's pin release after a
source-context read is the exception: `foundation.Store.Read` uses
`context.Background()` for that release, which is not cancellable, so a cancelled
or expired request returns once the store gate frees.

Every HEAD path is bodyless, including errors, with the GET status and headers.
400 `INVALID_REQUEST` details contain the offending `pointer` and safe `reason`;
this covers malformed UUID/query, duplicate/unknown/empty parameters, absent ID,
region without page, invalid page size and cursor mismatch. Authentication uses
existing 401 mappings; missing `version.read` is 403. Storage/deadline failures
are retryable 503 `SERVICE_RECOVERING`, with `Retry-After: 2` and
`details.reason=query_timeout` for deadline/cancellation. M4 delivers both named
reference operations through HTTP, the native transport/facade and the shared
Reader, and advertises `features.references=true`. The clients still require
explicit true: absent/false installs no reference callback or interception and
performs no resolver call. Ordinary href fallback remains usable. The
[pinned-reference record](../testing/pinned-references.md) distinguishes actual
browser/native proof from test-owned capability and error projections.

## Backlinks and deterministic index recovery (TASK-004-04)

These additive provisional operations reuse schema revision 9. `/related` remains
catalogued as 503 FEATURE_NOT_READY; no related-operation client or AI producer is enabled.
`capabilities.features.backlinks=true` advertises the complete shared Links delivery.

`GET/HEAD /api/v1/versions/{id}/backlinks` accepts only `page_path`, `region_id`,
`cursor`, `limit` (default 50, 1–200). Region requires page. Paths/IDs are decoded
once; the selected page is resolved through the existing exact-first path rules.
Omitted page selects the exact version; page selects that target page including
its regions; page+region selects the exact page-local region. Whole-version edges
are never attributed to every page. Data is the closed `backlink_page` registry
shape: `{scope,items,next_cursor,counts:{edge_count,referring_document_count}}`.
Items contain edge/reference IDs, exact source/target document/version/page/region,
nullable authored relation/label and a relative Reader URL to the referring anchor.
Counts are decimal strings over all matching currently validated visible edges;
referring documents are distinct across their versions. A free-text authored
relation is not proof of a code dependency or an AI recommendation.

Read-time two-endpoint source.Check, current intersected scopes/permissions,
lifecycle and live anchor/hash validation precede rows, labels, counts and cursor
binding. Active/archived are eligible; unknown, denied, trash and purged incoming
endpoints contribute nothing. A direct backlink request for a hidden/trash/purged
selected version is 404. An authorized referring document still uses the existing
D4 minimal historical tombstone/410 rule; no purged title/body or latest target is
substituted. Optional unresolved declarations remain source-context information,
never validated backlinks.

Incoming iteration uses the existing target index's NULL-first page/region,
source-version/local-reference order, at most 64 raw rows per store turn before
projection. Document deduplication uses a request-local set of admitted IDs.
Work scales with that selected incoming scope; outgoing reference counts scan only
the selected source version. Memory scales with admitted distinct documents plus
the finite slice/result page. Hidden raw positions never enter a public cursor.
Backlink cursors bind current principal/grant/scope, library/runtime, selected
manifest/filter/limit and visible projection/count state. Visible changes require
a fresh first page; denied-only mutations cannot invalidate the binding. Reads
recheck detached snapshots and perform no indexing or durable writes.

`GET/HEAD .../references` adds live decimal
`counts:{edge_count,referenced_document_count}` for resolved eligible canonical
outgoing edges/distinct target documents across target versions. Its declaration
membership, order and source-only cursor binding are unchanged. Live counts may
change between requests independently of that declaration cursor.

Input/cursor errors are 400 INVALID_REQUEST with safe pointer/reason, authentication
uses the existing 401/403 registry, and hidden identities are non-disclosing 404.
Every HEAD, including errors, is bodyless with GET status/headers. Storage/deadline
errors are retryable 503 SERVICE_RECOVERING with Retry-After: 2 and safe
query_timeout for deadline/cancellation. The read deadline begins before authentication.

Owner-only `GET/HEAD /versions/{id}/indexes/validate?kind=search|references`
returns `{version_id,kind,state,counts:{checked,missing,unexpected,mismatched},reasons}`.
Counts are decimal structural check observations, not semantic coverage or global
library totals. A completed 200 can be consistent, inconsistent or incomplete.
Reference validation checks exact migration-9 reverse-index DDL first, manifest
versus declarations, expected structural resolution, and canonical NOT INDEXED
versus forced covering-index tuples with bounded independent traversals. Safe
missing/changed-definition reasons replace raw SQLite diagnostics. Search validation
checks current index ownership/revision/readiness and compares immutable inventory
with required units, deterministic metadata/body chunk ordinals and static anchors.
Traversal uses selected-version/unit prefixes, with at most 64 raw rows per turn;
missing required units/chunks/anchors are inconsistent even if completion markers
remain. Empty extraction legitimately requires no body chunks. Pending admitted
anchor prerequisites are incomplete with `anchor_prerequisite_pending`; completed
missing/ambiguous anchors remain legitimate structural observations. Reverse
covering tuples are compared with a canonical `NOT INDEXED` rowid point lookup
after both physical and canonical endpoint eligibility checks; an admitted
mismatch is retryable SERVICE_RECOVERING on public reads and `mismatched` in owner
validation. Returned references and live counts are rejoined after counting;
changes restart within the deadline without changing source-only cursors. It does not rerun ranking,
certify arbitrary FTS posting bytes, read Git or establish semantic correctness.

Owner-only K `POST /versions/{id}/indexes/rebuild`, body
`{kind:"search"|"references"|"all"}`, requires Idempotency-Key and returns durable
202 `{version_id,kind,job_ids,operation_id}`. Unknown/duplicate body members are
invalid. It rejects affected nonterminal work with 409 OPERATION_CONFLICT. In one
current-owner receipt transaction it replaces terminal current-revision derived_work
slots with new queued derived_index jobs, retaining historical jobs/results/events,
object_pins, intact declarations and canonical/D4 edges. Same-key replay returns the
same job IDs; a different body conflicts. Search/all queues search before references;
reference-only replays declarations/outgoing/incoming without destructive cleanup.

Physical reverse repair is only REINDEX main.authored_edges_target under the exact
intact DDL, with its private checkpoint committed atomically. This explicit owner
maintenance step is the sole full-index store-gate exception. Durable owner cancel
is honored at pre/post-step boundaries; an arriving cancel waits for the gate and
cannot interrupt running SQL or become durable before its transaction commits.
Daemon-context cancellation rolls back uncommitted SQL/checkpoint changes; restart
fences the old attempt and resumes its last committed checkpoint. No resume route,
new migration, offline repair command, DROP/CREATE or sqlite_master edit is exposed.

Online recovery covers reconstructible failed/partial/stale derived rows and reverse
content divergence under an intact definition while the library can open. Missing
or altered DDL remains strictly startup-refused and rebuild-refused. There is no
supported repair for that startup-blocking damage before EPIC-005's verified restore;
preserve the library and do not bypass validation or claim an existing snapshot is
a general restore guarantee. Conflicting authoritative hashes/identities or lost
historical edge evidence are not repaired by inventing provenance.


## Implemented explicit Review (TASK-005-01)

Review GET/HEAD, PUT and cursor history use the closed
[Review registry](schema-registry.md#explicit-version-review-additions-task-005-01)
shapes. GET returns the exact absent/default or existing resource; PUT returns 200
with the confirmed view and operation_id, and an individual ETag when existing.
Both use the ordinary data/request_id envelope and no-store owner authority. The
shared receipt order and 412/428/409 mappings above apply, including no-op writes.
Workflow credentials cannot read/write personal Review. Authorized trash/purged new
writes report INVALID_TRANSITION with lifecycle; current hidden targets/receipt
reads remain nondisclosing. No renderer message is a Review mutation.

Document list/detail add latest review_state/reviewed_at, last_reviewed_version_id,
updated_since_review and continue_review_version_id, null for scoped readers.
Owner list filters review=reviewing|updated_since_review apply before cursor paging.
These are additive provisional selections under API v1, advertised by features.review;
Revisit, notes and real lifecycle mutations remain with their owning Tasks.

## Anchored notes and Revisit (TASK-005-02)

The [selected additions](schema-registry.md#anchored-notes-and-revisit-additions-task-005-02)
are additive provisional contracts, implemented behind features.annotations/revisit.
Owner-only GET/HEAD annotation list/one and Revisit reads, POST annotation create,
PATCH edit/explicit reattach, DELETE soft deletion and Revisit PUT follow those closed
shapes. New owner-only read routes are:

| Route | Result |
| --- | --- |
| GET/HEAD /annotations/{id}/history | Cursor revision views, including permitted original anchors/content and explicit reattachment/deletion |
| GET/HEAD /annotations/{id}/body?revision=N | Exact referenced Markdown bytes of that saved revision |
| GET/HEAD /annotations/{id}/context?revision=N | Exact adopted context bytes, selected from that current/history revision's context_ref |

Stream reads carry Content-Type, Content-Length and the immutable object's strong ETag,
with attachment disposition and a restrictive document policy. Neither the content ETag
nor context_ref authorizes a note edit. Current owner/document/history authorization
is checked before and throughout delivery; pins retain the selected object. No live
renderer or originating session is needed for saved context recovery. A missing context
or revision is 404 and never falls back to a different revision. Annotation history
cursors bind the note, owner and runtime generation; list cursors bind the exact version.

Create returns 201, edit/reattach/delete and Revisit PUT return 200, with operation_id
and applicable individual ETag. Every mutation is K; malformed/missing/stale controls
remain 400/428/412 and replay uses the saved key/conditions before new-work CAS. Revisit
GET exposes its own individual ETag or explicit absence. GET documents accepts the
additional owner-only revisit=true|false filter, including in combination with review.

## Document lifecycle (TASK-005-03)

Additive provisional v1 implementation: `features.lifecycle` advertises availability;
all lifecycle administration requires a current owner. Ordinary document, version,
personal-record and asset reads continue to hide trash. Workflow credentials cannot
create/read/commit plans or list Trash. No generated-frame message is a management action.

- POST `/documents/{id}/lifecycle-plans`: closed `{operation,expected_revision}`;
  operations archive/trash/restore use the document revision. Optional Idempotency-Key
  uses preview receipt semantics; a keyless request creates only the returned plan.
  Returns 201 `lifecycle_plan_page`.
- GET `/lifecycle-plans/{id}?cursor=&limit=` returns 200 `lifecycle_plan_page`.
  The saved item inventory is immutable. Every page/replay/operation response rechecks
  incoming-source eligibility and recomputes authorized counts; newly hidden source
  identities/details never leave via nested items. New references require a new plan.
- POST `/lifecycle-plans/{id}/commit`: closed
  `{plan_id,expected_revision,confirmed:true}`, with matching body/path identity.
  Returns 200 `lifecycle_result`. Plan ID is the natural commit/operation identity;
  an optional key aliases the same definitive outcome without overwriting another receipt.
- GET `/trash?project_id=&cursor=&limit=` returns 200 `trash_page`: owner metadata only,
  with deletion time, previous lifecycle and nullable informational purge eligibility.
  This does not enable purge. GET/HEAD do not apply domain effects.

Plans last 15 minutes. Strict closed-body/type/confirmation validation precedes
receipt lookup. Missing, false, null or non-boolean confirmation is always 400
INVALID_REQUEST, including keyless/keyed committed replays. Missing expected_revision
is 428 PRECONDITION_REQUIRED. For valid requests: authenticate/current authorization,
receipt fingerprint/replay, then new-work revision/freshness/transition checks.
A changed valid expected_revision on an already committed plan is 409 OPERATION_CONFLICT.
An identical committed replay after expiry returns its original result (200 for preserving actions; 202 for purge). First
commit of an expired plan is 409 STALE_PLAN; new stale document revision is 412
PRECONDITION_FAILED; changed config/permissions/impact is STALE_PLAN. Invalid transitions
return 409 INVALID_TRANSITION with authorized details.lifecycle. TASK-005-04 implements purge as described below. Unknown/unauthorized identities remain nondisclosing.

For archive/trash/restore, freshness binds document revision/versions, config and permission generations, explicit
Notes/history/bindings, Review/history, Revisit and eligible incoming references.
Automatic reading/page positions and reader preferences are as-of-plan information:
values/revisions/new rows do not invalidate a plan and all current rows remain preserved.
Archive/Trash never changes Review or removes personal data/object ownership. Restore
returns the exact same identities to prior active/archived state. Old accepted jobs and
renderer sessions cannot regain application/delivery authority after trash→restore.
The safe lifecycle result remains owner-reconcilable while trash; ordinary saved personal
responses remain masked. No generic exception exposes hidden personal bodies.

## Guarded manual purge (TASK-005-04)

Additive provisional v1: the existing lifecycle plan endpoints now implement
`operation:purge` for trash only. Their owner, exact confirmation, natural plan ID,
optional alias, current-authority and replay/error precedence remain unchanged.
Purge alone returns a completed **202** acceptance receipt with the existing
lifecycle result plus `job_id` and `job_url`. This response never changes to a
completion receipt. The atomic decision makes restore and cancellation impossible;
restore-first instead stales the purge plan without any deletion.

Purge plans include the registry's real `purge_backup` projection: not_created, verified or unavailable, with separate attempt and trial observations. Independent copies outside the local ledger are not inferred. TASK-005-05 supplies this status.
The owner need not create a new backup before confirming. The impact separates
versions, notes/history, Review history, distinct referring documents and edges.
Unlike preserving lifecycle actions, purge freshness includes current reader rows.

`features.purge` gates the action independently of lifecycle. Ordinary owner job
GET/list and existing CLI jobs show reveal only safe purge progress, including
paused reason, retry time, remaining objects and metadata-removal status. No new
resume route is implemented. Current/scoped data reads stay hidden; incoming
historical references retain the D4 minimal IDs/410 result. A purged version ID is
permanently reserved at import acceptance and final publication.

## Current local-copy and backup status (TASK-005-05)

GET /api/v1/status now reads real backup status and the purge impact projects the same
source. It distinguishes no completed set, prior complete plus failed attempt,
inaccessible destination and actual isolated trial. It does not add a backup prerequisite.
The earlier TASK-005-04 placeholder is superseded by the registry's widened purge_backup.

POST /local/v1/copy-requests is protected loopback control, not a product/browser API.
Profile discovery proof precedes submission; domain-separated HMAC binds nonce, exact
body, bearer digest, operation ID, profile/library and daemon instance. The body carries
runtime_generation. A current owner session is checked before receipt replay and commit.
Wrong Host, Origin, forwarding headers, peer, profile, instance, proof and non-owner
credentials are rejected. Client proxying and redirects are disabled. Responses bind
nonce/status/body. The closed actions and semantic conditions are in the registry;
there is no /api/v1 counterpart or browser/native host-path submission method.

## Implemented managed source capture transport (TASK-006-02)

These additive authenticated operations use only configured logical source IDs
and clean relative selected paths. They add no server-filesystem browsing API,
provider call or generation/question admission endpoint.

| Method and route | Implemented behavior |
| --- | --- |
| POST /projects/{project_id}/source-captures | Strict CaptureRequest JSON and Idempotency-Key. Reserve receipt/selection, capture outside writer, recheck current authority, then atomically publish the complete target/read set or retain an incomplete selection. 201 carries the capture projection, whose complete flag and nullable target distinguish partial capture. Policy/config failure returns its retained typed error response. |
| GET/HEAD /source-captures/{capture_id} | Current-authorized metadata and first read page; optional cursor enumerates reads. No source/Git/AI work. |
| GET/HEAD /source-captures/{capture_id}/reads | The same capture/read projection and cursor semantics. |
| GET/HEAD /source-captures/{capture_id}/diff?other={capture_id} | Persisted exact-byte/availability comparison with optional cursor bound to that pair. No source or AI read. |
| GET/HEAD /source-reads/{read_id} | Explicit verified retained read or exact reference-only source reread. Current source/session/version authority precedes delivery and each consumer read. Inert octet-stream attachment with nosniff, exact digest ETag and private no-store. HEAD performs the same verification without delivering bytes. |

Capture mode/default/intent, selected-path shape and pagination are in the
[registry](schema-registry.md#additive-managed-capture-dtos-task-006-02). A scoped
principal requires version.read and source.read for its exact project/version,
and can inspect only its own captures. The owner may inspect permitted project
history, while byte reads still obey current source policy and target lifecycle.
Replay through both POST and operation receipts reapplies current visibility.
Partial/missing source is never represented as a successful empty target set.
Canonical 409 SOURCE_UNAVAILABLE covers missing or changed exact source, and
503 CONTENT_INTEGRITY_ERROR withholds corrupt stored bytes. Ordinary authority
errors remain actual read failures; capture/history alone never promises current byte access.
No raw source, provider credentials or host root paths enter diagnostic output.

Capture operation failures preserve the existing authority error code/status
(including 401 SESSION_REVOKED), 412 configuration fencing, and a settled
nonretryable receipt. Detached stage/deadline/interruption failures use 503
SERVICE_RECOVERING; storage exhaustion uses 503 RESOURCE_EXHAUSTED. Exclusive
restart settles interrupted unpublished capture as interrupted_before_commit,
retaining its original selection and receipt. These failures cannot be retried
automatically under the original operation ID. Metadata view and both diff sides
use the registry's same current-availability projection without reading bytes.

## Provider transport status (TASK-006-03)

GET/HEAD /api/v1/providers returns the owner-only credential-free
[provider status](schema-registry.md#provider-status-addition-task-006-03).
The service authenticates before native preflight and rechecks the current
owner session before response. It reads native account/effective mode/catalog/
allowance without inference, source disclosure, login/logout or configuration
changes. HEAD returns the same status/headers without a body. Existing Reader,
file and personal Review routes do not depend on provider availability.

/api/v1/status distinguishes installed transport from as-yet unobserved native
availability and points to /providers. Task03 did not implement generation or
question admission. Task04 now separately exposes generation_available through
its wired factory; question_available remains false. Transport support alone
does not establish actual availability or grant an unimplemented paid action.

## Generation and drafts implementation (TASK-006-04)

POST /generations accepts the closed JSON object
`{project_id,document_id,base_version_id?,intent,prompt?|body_ref?,title?,provider_id?,model?,effort?,source_selection,explanation_references?}`.
`document_id` is required and explicitly nullable. A non-null document requires
its captured existing published base; a new document cannot name another base.
`intent` is exactly `publish` or `draft`. Exactly one prompt selector is required;
blank/non-UTF-8 body input is invalid. `source_selection` uses the existing
capture selection/mode/intent/provider shape with no `version_id` override.
An empty selection is supported and never establishes source verification.
Unknown, duplicate and invalid-null fields fail before admission.

Large text is automatically staged with purpose=body, input_mode=object and
`target={operation_id,action:generation,project_id,document_id}` under distinct
preparation keys. `body_ref={object_id,bytes_sha256,byte_size,media_type:text/plain}`
names sealed exact UTF-8 bytes. Inline and body_ref normalize by actual content
identity, not handle. Receipt-first replay uses the original key/body before
fresh preparation; current authority is still checked. Accepted work retains its
captured principal/binding/current epoch and project/source restrictions; ordinary
bearer expiry after acceptance is not inferred revocation. Invalid workflow
authentication never falls back to an owner credential.

A locally issued workflow credential privately captures its selected mutation
project independently of prior-explanation read projects in the public scope.
Fresh generation, generation-body staging, result/import-metadata staging,
source capture and import admission require both that captured project and the
current remembered project selection. Changing selections without revoking an
older credential cannot expand its output authority. Missing captured facts
refuse fresh effects; they are not guessed from the current selection or a
union of read projects. Exact prior-version reads and draft-specific anchors
keep their own scope rules. Existing receipts retain fingerprint/current-scope
checks, and accepted work retains its captured authority and current
principal/epoch/project restrictions. A remembered document remains a default,
not an additional immutable credential target.

The creation response is 202 with
`{operation_id,job_id,draft_id,intent,job_url}`. It means durable admission, not
publication or completed AI work. The existing operation/job routes own status,
unknown effects, cancellation and explicit resume decisions. features.generation
is set only by the wired production factory; it is not provider entitlement or
quota availability. features.questions remains false.

GET /drafts supports optional project_id/cursor; GET /drafts/{id}/history supports
cursor. Lists are `{items,next_cursor}` with cursors bound to library, runtime,
principal/current scope, current visible-project permission generations, filter
and resource. Individual drafts expose the registry projection and strong ETag;
GET/HEAD share the representation media type. Missing required If-Match is 428;
malformed/weak/multiple/wildcard validators are 400, while an exact stale
individual validator is 412. Reject/replace/publish retain their existing exact
upload-purpose/target, current authority and lifecycle rules. A ready candidate
replacement fences its old linked generation: continuing that job cannot publish
the old prepared candidate or label the replacement published. History retains
prior candidate inventories. Publication is atomic with its receipt and outbox.

Closed shared native operations are generationStart, draftList, draftGet,
draftHistory, draftPatch, draftPublish and jobResume. Their operation-specific
payloads contain no arbitrary URL/method. Unknown/duplicate/malformed fields do
not reach HTTP. Owner-only resume retains the existing job ETag, exact expected
attempt generation and original operation key; no new resume authority is given
to a generation workflow bearer.


### Generation caller recovery and source chooser

The shared generation/draft clients journal the original runtime generation
before dispatch. A job-resume subrequest captures its own runtime, exact decision,
expected generation, key and job ETag before POST; it does not inherit the parent
generation's runtime. Pending resume recovery first GETs its original receipt.
Current-authority receipt lookup remains available after a runtime change;
receipt 404 permits no POST/PATCH when that subrequest's original runtime is
different or absent. Same-runtime recovery reuses its exact saved key/body and
validator. Completed receipt recovery followed by a current job read sends no
new mutation. Pending/refused resume remains visible when the job is no longer
paused.

Draft PRECONDITION_FAILED 412 or VERSION_CONFLICT 409, job-resume
PRECONDITION_FAILED 412 or INVALID_TRANSITION 409, and confirmed failed receipts
permit explicit acknowledgement/new action. The old request is retained as
history before only its exact active slot is released under the journal lock.
Operation-identity conflicts and network/lookup uncertainty retain the original
request. A newer journal slot cannot be cleared by an older callback. Replacing
a ready result retains its original base; changing base requires explicit new
linked generation/draft work, never automatic rebasing.

`GET/HEAD /projects/{id}/source-reads` requires a single nonempty source_id and
optional cursor, with no other query fields. It projects the existing completed
managed capture inventory as `{items,next_cursor}` using the existing read DTO
plus its original capture timestamp `captured_at`, under current capture/source
authority. The timestamp is copied from that capture, not invented at listing. It does not read source/object bytes or
create observed_input. Pages contain at most 64 records; the opaque continuation
is bound to current library/runtime/auth/principal/scope/project/configuration
and source selection. GET/HEAD share the representation media type; HEAD has no
body. The closed native operation is retainedSourceReads with only
`{project_id,source_id,cursor?}`. Unknown/duplicate payload fields do not reach
HTTP. Its selection feeds the already defined exact pinned files read_id
contract rather than changing capture or generation authority.

The owner-authenticated local workflow-grant route carries complete source and
prior-context selections, unlike the bounded pairing/session control DTOs. It
authenticates the owner before complete-body processing and verifies the exact
nonce/request/body proof; complete responses retain their nonce/status/body
proof. It imposes no total selection-length limit. Pairing/session control
limits remain unchanged. Logical-body tamper, changed-body/same-key replay, scoped-current
authority and private verifier/credential rules remain in force.
