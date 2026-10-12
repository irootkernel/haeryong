# Import and transfer workflow

**Documentation baseline 1.6.2; proposed, unfrozen API v1.** This is the single normative definition of upload purpose/target authorization, composite operation IDs, import request fields, sealing cancellation, and upload list/detail shapes. The [schema registry](schema-registry.md) owns shared enums and generic receipt/precondition rules; the [HTTP catalog](http-api.md) owns route availability. The [package contract](package.md) owns original bytes, path identity and optional author metadata. None of these transport fields is compulsory author-written packaging.

## Purpose, target and authority

Staging is preparation for an already authorized action, not publication or a new authority tier. No standalone upload-administrator scope is introduced. The owner uses existing owner authority; a workflow can use only the relevant existing action scopes within its selected resources. Check purpose, the complete target and current authority at creation, transfer, sealing and adoption. A handle or operation ID alone grants nothing.

| Purpose | Target shape and required meaning | Allowed workflow authority and consumer |
| --- | --- | --- |
| result | {project_id, document_id, draft_id?}; project_id is required. document_id is explicitly an existing ID or null for new-document intent. Omitted draft_id normalizes to null. A non-null draft_id must belong to this project/document intent. | With draft_id null: import.publish for that exact target, consumed only by POST /imports. With draft_id set: draft.write for that exact draft, consumed only by its replacement/preparation path, not direct import. Publication of a draft remains its separate authorized action. |
| context | JSON target {version_id, page_path, render_instance_id}; all three keys are explicit, with nullable page_path/render_instance_id. A non-null page names an exact page. POST /contexts query transport follows the normalization below without changing this stored target. | context.write within that version/page or existing owner authority. POST /contexts accepts the sealed bytes; a later Save/Ask must independently authorize its own action. A workflow context may have render_instance_id null. |
| body | One closed action-specific shape in the table below. operation_id is the future consuming operation's ID, not the upload-create or seal key. | The authority for that exact consuming action. context.write alone cannot stage a question, note, source verification or generation body. |
| metadata | One closed metadata-consumer shape below; operation_id is the future consuming operation's ID. | import.publish or draft.write for that exact action/target, not publication authority inferred from supplied metadata. |

A generation.request scope starts the requested generation; it does not grant arbitrary direct publication. Managed generation output uses its job-bound output authority and then the matching import/draft consumer. An external workflow that needs to upload its own result receives the existing import.publish or draft.write scope derived from the owner's requested work, without another per-file approval prompt.

All keys shown in the following target shapes are required. Nullable document_id/page_path must be sent as null when absent. Unknown action variants/fields fail INVALID_REQUEST. Paths and metadata-local evidence IDs follow the shared exact-identity rules.

| Purpose/action | Exact target object | Consuming route and authority |
| --- | --- | --- |
| body / question | {operation_id, action:"question", version_id, page_path} | POST /versions/{version_id}/questions; owner |
| body / annotation_create | {operation_id, action:"annotation_create", version_id, page_path} | POST /versions/{version_id}/annotations; owner |
| body / annotation_edit | {operation_id, action:"annotation_edit", annotation_id} | PATCH /annotations/{annotation_id}; owner, with the note's current version/page and edit precondition |
| body / generation | {operation_id, action:"generation", project_id, document_id} | POST /generations; owner or generation.request for that exact requested scope |
| body / evidence_verification | {operation_id, action:"evidence_verification", version_id, evidence_id} | POST /versions/{version_id}/source-evidence/{evidence_id}/verifications; current version/source-read authority |
| metadata / import | {operation_id, action:"import", project_id, document_id} | POST /imports; owner or import.publish |
| metadata / draft_replace | {operation_id, action:"draft_replace", draft_id} | PATCH /drafts/{draft_id} action replace; owner or draft.write |

target.operation_id in body/metadata uploads binds the future consuming request; it is not the current upload request's receipt ID. For example, POST /uploads with key U may carry target.operation_id Q, followed by seal key S and consumer key Q. U, S and Q denote distinct UUIDs generated and retained by the client. The [receipt identity rule](schema-registry.md#operation-identity-and-preconditions) permits this difference, includes the target in retry identity and forbids changing Q while replaying U. At consumption, Q must match the actual consumer key and action/target. The consuming receipt need not exist yet: authorize the declared action and resource now, then check the actual request, operation ID, target, purpose and current authority again at adoption. A body/metadata object cannot be silently rebound to another action or operation. Preparing a new target may internally copy existing verified bytes into a separately authorized staging handle; that is not permission to mutate the old binding. Large expectations, notes, questions and metadata use the same automatic object transport without a total-byte limit.

A result upload excludes base_version_id from its byte-transfer target. Sealed bytes may be reused at a new base of the same document by a deliberately new authorized import operation. They cannot be retargeted to another project/document, switched between existing/new-document intent or moved between drafts. Abandoned handles cannot be reused. Reacquire pins and check actual byte availability before any reuse.

## Context binding transport normalization

The upload target is JSON and requires version_id, page_path and render_instance_id explicitly, with JSON null for absent page/render values. POST /contexts carries its binding in query parameters: version_id is required; omitted page_path or render_instance_id normalizes to null. A present parameter is one nonempty, once-decoded path or valid identifier. Empty or duplicate binding parameters fail INVALID_REQUEST. The text `null` is not a query null sentinel: for page_path it names that literal file, while a renderer value must be a valid render-instance identifier. Clients omit optional query parameters to represent null.

For input=upload, compare the entire normalized tuple with the sealed context target before adoption. A missing render parameter cannot clear a non-null stored binding or inherit the currently open page. For direct input, apply the same normalized tuple and current channel/workflow checks. The canonical tuple and exact bytes enter semantic request identity. Authorized recovery after channel loss retains the original persisted tuple; it does not rebind old data to a new renderer.

| Stored context render binding | POST /contexts render parameter | Binding result, subject to current authority and other fields |
| --- | --- | --- |
| null | omitted | Equal: no render-instance binding |
| R1 | R1 | Equal: the exact original instance |
| R1 | omitted | INVALID_REQUEST: null does not match R1 |
| null or R1 | a different instance R2 | INVALID_REQUEST: no implicit rebinding |

R1/R2 are explanatory labels, not new identifier syntax. Whole-version page null follows the same omitted-query normalization. These transport rules do not relax the required document_id field on POST /imports.

## Client association and import request

CLI/UI/trusted workflow launchers resolve remembered project/document choices using the [package binding table](package.md#remembered-association-and-operation-base). The HTTP handler never interprets an omitted document_id as the last document or a workflow default. POST /imports requires document_id: an existing ID for a new version, or JSON null for a new document. Missing document_id is 400 INVALID_REQUEST, distinct from explicit null. This protocol requirement adds no manual input: clients prepare it automatically.

Before creating a result upload, resolve its project_id. For a new owner document with no project choice, the client resolves or creates the library's stable Unfiled project using the existing project operations; concurrent creation resolves the same library-owned default. A workflow uses only its authorized selected project. For an existing document, use its actual project. POST /imports can omit project_id only because it can take that exact value from the sealed upload target; it never creates another project or consults hidden preferences at publication.

| POST /imports field | Rule |
| --- | --- |
| upload_id | Required sealed, reusable purpose=result upload with draft_id null. Its target and every included byte must be currently authorized. |
| document_id | Required existing ID or explicit null; must match the upload's target. Null selects new-document intent; the service allocates the resulting ID once in normalization_context. |
| project_id | Optional exact project ID. If omitted, use the sealed upload target's required project ID. A supplied mismatch is INVALID_REQUEST. |
| base_version_id | New document: absent or null only. Existing document: explicit published base ID, or omit for a once-only capture at the first valid POST /imports reservation. Explicit null for an existing document is INVALID_REQUEST. |
| version_id | Optional explicit immutable version identity. Otherwise allocate once for this operation. Existing same identity/content follows the package equality rule; different content is IMMUTABLE_VERSION_CONFLICT. |
| metadata / metadata_ref | Optional, mutually exclusive inputs with the same meaning after normalization. metadata_ref comes from the matching metadata/import target above. No metadata is required; payload manifest.json remains ordinary content. |

The request is the fields above plus the Idempotency-Key header. Title, default page and evidence enrichments are in the optional metadata rather than competing top-level title/source overrides. Conflicting explicit IDs in optional metadata fail rather than overriding the request. Resolve generated IDs, omitted-field defaults and timestamps only once under the generic receipt rules.

**Normal UI and CLI capture the expected base before transfer and send it explicitly.** Reader/Versions show that value before upload. If another task publishes while bytes transfer, the original base becomes stale and publication returns VERSION_CONFLICT; it does not silently attach the result to the newer base. Persistent workflow association stores document/project, never an indefinitely frozen base.

The low-level omitted-base variant is different: while durably reserving the first valid POST /imports, resolve latest for that exact existing document and store it atomically with the original request's omission markers and normalization_context. That acceptance is not POST /uploads, seal completion or provider generation start. Retries load the saved base instead of resolving latest again. Publication rechecks the captured base. Choosing a different base later is a new operation, not a retry of the old request.

A completed 202 receipt from POST /imports records creation/linkage of job.kind=import. It is not published content. GET job identifies the final version only after atomic publication. A synchronous successful publication returns 201, or 200 for an already identical authorized explicit version. Response timing does not impose a content-size threshold. job.kind=upload_seal only verifies/seals staging and never publishes a version.

## Composite operation IDs and recovery

One user command can perform several different K requests. Its user-facing operation ID belongs to the final requested domain operation, not to every preparation call. For `haeryong import --operation-id I`, I is exclusively the POST /imports Idempotency-Key. Client-created upload, seal and other preparation keys are distinct. Each retains the shared principal-scoped receipt rule; reusing I for another route is OPERATION_CONFLICT.

| Step | Key / recovery record |
| --- | --- |
| Prepare import intent | Persist I, resolved project/document, explicit pre-transfer base, original metadata/input intent and phase before network work. I is a symbol here; actual keys use the shared UUID syntax. |
| POST /uploads | Generate U distinct from I, persist U before sending, then record returned upload_id/checkpoint. Replay U after response loss. |
| File chunks | Use the existing natural upload/path/offset identity and durable checkpoints, not a new UUID per chunk. |
| POST /uploads/{id}/complete | Generate S distinct from I/U, persist S and its exact checkpoint before sending, then track the same seal receipt/job. |
| POST /imports | Send I with the original resolved intent and sealed upload. Persist that dispatch phase before sending. Follow its receipt and then any import job. |
| Deliberate changed request | Record a new domain operation ID and the reason, for example a changed base. Reuse sealed result bytes only within their unchanged target. |

The caller-profile journal holds these associations before a final domain receipt exists. It is a small client recovery record, not another general workflow engine or a fabricated server receipt. A server GET operations 404 does not prove that a lost request never ran. Recover the caller phase, preparation receipts, upload checkpoints and final request under current runtime generation; preserve ambiguity when those facts are insufficient. Owner-visible upload discovery recovers bytes/targets after client loss but cannot alone prove that publication was never requested.

Apply the same rule to a question/note/verification/generation with a staged body, and to POST /contexts backed by a context upload: the final consumer retains its own key; create/seal calls each get stored distinct keys. Internal IDs, hashing, resumptions and journal bookkeeping remain automatic. No author-maintained manifest or manual series of IDs is required.

If a second authorized completion request uses a new key at an already-active identical seal checkpoint, commit that request's own completed 202 receipt referencing the existing seal job; do not dispatch another worker. An already sealed identical checkpoint returns a completed 200 receipt for that sealed result. A different checkpoint while sealing fails INVALID_TRANSITION. Replaying any of these keys returns its recorded response after current authorization, regardless of the job's later outcome. After a cancelled/failed seal, retrying its old key still returns its old outcome; a deliberate new completion uses a new key/job.

## Cancelling sealing versus abandoning an upload

`POST /jobs/{id}/cancel` is supported for upload_seal. Cancel means stop this verification, not discard its input. The job contract owns general monotonic cancellation; the coupled rules here prevent a cancelled job from leaving an upload stuck or writable while its worker still runs.

| Race / action | Required outcome |
| --- | --- |
| Cancel wins before sealing commits | Fence the seal attempt and enter cancelling (or cancelled if no worker started). Keep upload sealing while a worker could still finalize. Once stop/reconciliation is confirmed, atomically finish job cancelled and return the non-abandoned upload to open with all accepted bytes/checkpoints retained. |
| Seal commit wins before cancel | Job remains completed and upload sealed; report already completed. Cancellation never reverses the committed seal or a publication. |
| Abandon while sealing | Atomically set upload abandoned and fence new writes/adoption/finalization. Record cancel intent on the active seal job; settle it cancelled only after stop is confirmed. Keep pins while its worker/recovery still needs them. |
| Abandon after sealing/adoption | Prevent further reuse of this upload and release only its safe staging references. Never delete an adopted version, saved context or unknown-work pins. A completed seal job is not rewritten as cancelled. |
| Crash during cancel/abandon | Recover the persisted fence, upload state and job together before reopening writes or releasing pins. No unverified early transition to open. |
| Resume verification after cancellation | New completion request/key creates a new upload_seal job on the preserved open upload. A terminal cancelled job is not resumed. |

A confirmed recoverable verification failure may also return a non-abandoned upload to open, with the failed job and exact error retained. All transitions preserve the existing rule that mobile suspension, idle time or delivery lease expiry does not abandon an upload. No automatic staging TTL or total deadline for progressing transfers is introduced.

## Upload list and detail shapes

The owner can list/recover authorized uploads even when the originating device or workflow credential is gone. A workflow may inspect/change only its own currently scoped uploads. List replies use the common cursor envelope; large ranges or pin inventories never become compulsory inline arrays.

| Response | Exact field allocation |
| --- | --- |
| GET /uploads list item | upload_id, originating_principal_id, nullable originating_device_id, purpose, input_mode, selection_name, target, state, byte_size, staging_bytes, created_at, updated_at, reusable, nullable unavailable_reason, pin_summary, pins_url, detail_url |
| GET /uploads/{id} detail | All summary fields plus checkpoint, ranges_url, entries_url, nullable error, nullable seal_job_id, nullable seal_operation_id, nullable sealed_content_set_id and nullable sealed_object_ref. Ranges/entries remain paginated resources. |
| GET /uploads/{id}/pins | Cursor pin/adoption references, kinds and reasons preventing reclamation under the current upload authority. |

byte_size counts accepted input bytes; staging_bytes measures retained staging, not guaranteed reclaimable bytes. Error, checkpoint and detailed ranges are not list-item fields; detail_url retrieves them. pin_summary is compact counts by pin category, not raw tokens. sealed describes completed verification, while reusable additionally requires current authority and every original byte to remain available and pinnable. Failed availability does not invent another upload.state.

## Required contract fixtures

TASK-001-06 freezes these proposed shapes with deterministic request/response fixtures. TASK-002-02/04/05 implement their generic ledger, import and transfer/API slices; existing X11/X14/X17/X18 cover regression. Include draft.write-only staging with publication denied, missing versus null document_id, distinct I/U/S keys and response loss before final dispatch, explicit pre-transfer base versus omitted API base, same-checkpoint multi-key sealing, all cancel/seal/abandon races, summary/detail fields and explicit-version content conflict. Include the context normalization table, exact draft replacement binding and a late failed seal after abandonment; none may silently retarget or reopen data. Fixtures are planned until run; this document does not prove a service exists.
