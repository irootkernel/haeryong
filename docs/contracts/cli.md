# CLI contract

TASK-006-01 adds `haeryong jobs resume JOB_ID --decision
reconcile_only|continue|retry --expected-attempt-generation N --if-match ETAG`
with the standard optional `--operation-id`. It journals the exact body and
validator through the existing mutation recovery path. It never infers retry
authority from a lost response, new operation ID or local process exit. The
command resumes an existing eligible provider job. TASK-006-04 now implements
generation creation; TASK-006-05 adds saved-question creation and history below.
jobs show/list JSON retains the
scoped provider_attempts counts and unknown-outcome reason, including older
uncertain attempts on a completed current job. Explicit reconcile_only can
settle those attempts without changing the terminal state/current result or
creating another external execution. The server captures the finite target set
under the supplied validator; the CLI cannot choose arbitrary native IDs.
Permanent saved-result supersession is INVALID_TRANSITION (exit 4); the normal
definitive-response journal acknowledgement applies, rather than a transient
service-recovery retry. Replayed accepted decisions still use their original key.

**Command contract, documentation baseline 1.6.0.** TASK-002-01 implements the bounded local lifecycle subset described in [profile/lifecycle evidence](../testing/profile-lifecycle.md): init, daemon run, service status/stop, status, doctor and version. [TASK-002-02](../testing/storage-foundation.md) adds real storage preparation to these lifecycle commands. TASK-002-03 adds protected `auth pairings` and stdin-intent `auth approve/reject <pairing_id>` bootstrap commands as described in [authentication](authentication.md#implemented-headless-scope-task-002-03). TASK-002-04 wires the upload/seal/import worker into daemon mode. TASK-002-05 implements the bounded authenticated query/transfer/configuration command subset below; future families in the catalog remain unavailable. Local control is protected OS-user authority, separate from current owner bearer sessions and future workflow credential issuance. One haeryong headless executable provides CLI and daemon mode; the desktop companion is separate. Shared wire fields/enums/errors are defined by the [registry](schema-registry.md) and [HTTP API](http-api.md).

## Global behavior

Support --profile, --endpoint, --json, --no-input and explicit credential selection from protected storage. Never accept raw long-lived tokens in argv or infer owner authority from a flag. JSON has stable data/error/request fields; diagnostics go to stderr. --no-input honors remembered configuration and an explicitly chosen plan; it never silently expands scope or approves a new destructive/uncertain retry decision. Unimplemented capabilities fail visibly.

Every command mapped to a K operation in HTTP accepts --operation-id. For a composite command it identifies only the final requested domain operation: import maps it to POST /imports, not to upload creation or completion. Preparation requests use automatically generated, distinct persisted keys and a caller-side progress journal under [composite operation IDs](import-workflow.md#composite-operation-ids-and-recovery). For its owning request, the value is exactly the HTTP Idempotency-Key and receipt operation_id, using the registry's unquoted lowercase canonical UUID syntax. Invalid/quoted IDs fail as invalid input; the CLI does not remove quotes or silently substitute another ID. When omitted, generate an ID once, record it with the pending normalized request in protected caller-profile state before submission and return it in machine output. Preserve it for transport retries. A later recovery invocation uses that same ID and original semantic preconditions; a new ID denotes deliberately new work. Do not regenerate an ID on timeout or silently replace a stale expected revision. An identical concurrently pending operation returns 202 with operation_id/state accepted/operation_url; poll that operation without issuing new work. A pending reservation is distinct from unknown external execution. A committed 202 receipt identifies job creation, and the CLI follows job state separately.

Use --if-match with the exact individual ETag returned by a read, or explicit --if-none-match '*' for an absent personal record. Do not turn a list validator or guessed numeric revision into an item's validator. Receipt replay performs current authentication/authorization before returning safe original results, and occurs before rechecking original now-stale preconditions. Plan IDs are natural commit identities; without a separate key, operation_id is plan_id. An optional operation ID is an additional lookup binding to the same single plan result, never a new commit. It conflicts if already bound to unrelated work. Authenticated identical committed-plan replay returns the original result even after the old plan expiry/revision has passed.

Exit classes are 0 success, 2 invalid input/package, 3 authentication/permission, 4 conflict/precondition, 5 unavailable/feature/local-budget/provider-quota state and 6 uncertain external/reconciliation outcome. Precise code mapping is in the shared registry. Provider auth failure is not misreported as loss of the owner's session.

## Command families

| Command family | Intended behavior |
| --- | --- |
| haeryong version --json | Binary, API/package/skill compatibility and build versions |
| haeryong init --profile default | Explicit isolated initialization; never overwrite another library |
| haeryong daemon run | Foreground Go service using validated profile settings |
| haeryong service install/start/stop/status | Local OS service operation by the owner; user service by default |
| haeryong doctor | Check identity, single listener/private HTTPS proxy, DB/schema, source availability, provider capabilities and configured origins |
| haeryong status --json | Redacted service/provider/backup composed status |
| haeryong auth pair list/approve/reject | First local bootstrap or an existing owner session; verify exact comparison code |
| haeryong auth sessions list/revoke | Owner-authorized device/session management |
| haeryong auth grant create/revoke | Owner-authenticated local CLI/IPC issues/rotates/revokes scoped credentials. --workflow-binding ID reuses a server-allocated remembered binding/principal; a new workflow allocates one. Trusted launchers derive requested scope automatically from remembered choices, without handing the owner bearer to the agent |
| haeryong projects list/register | Logical project registration with remembered source/provider choices configured explicitly |
| haeryong sources approve/status/observe | Owner configures roots/disclosure once; reuse for routine reads. Status shows logical identities, exact revisions, retention and availability |
| haeryong search --project ID --query TEXT | Eligible existing documents/regions and bounded match context |
| haeryong package validate PATH | Stream a directory, ZIP or single file into validation; optional --metadata PATH, no required author manifest or size/count cap |
| haeryong uploads list/show/resume/abandon | Discover owner-visible pending uploads even after a lost ID/originating credential; inspect bytes/activity/purpose/exact target/reusable/reasons/pins. resume ID --input PATH verifies the original binding/checkpoints and reconciles or completes that upload; abandon uses the existing K operation |
| haeryong import PATH --operation-id ID | Import the complete selected directory/ZIP/file with automatic resumable transport, inventory and missing service IDs; optional --project, --document or --new-document, --base-version and --metadata; stale base returns VERSION_CONFLICT, changed content under an existing explicit version ID returns IMMUTABLE_VERSION_CONFLICT |
| haeryong documents show ID --version ID | Exact result-directory metadata, page/file listing links, source basis and individual resource validators |
| haeryong files list --version ID | Cursor/stream every selected entry, optionally scoped to a subdirectory or preview type |
| haeryong files get --version ID --path PATH --output PATH | Stream exact immutable bytes with package exact-first/unique canonical-equivalent lookup; ambiguous fallback fails. Directory/link requests return typed metadata. --resolve-link follows only in-version links and reports requested/resolved paths; resume against the resolved object's digest |
| haeryong reader preference show/set --version ID | Inspect/set owner/version default_page or clear it with null; independent/create precondition, no published metadata rewrite |
| haeryong reading-position show/set --version ID | Last-opened pointer only: page_path, page_query, page_fragment and opened; individual/create precondition |
| haeryong page-position show/set --version ID --page PATH | Save page_query, page_fragment and position for the exact page under its own individual/create precondition; do not move the last-opened pointer |
| haeryong review show/history --version ID | Owner state/create precondition or cursor transition history |
| haeryong review set --version ID --action complete --if-match ETAG | Owner start/complete/reopen/clear; CLI maps action to the registry's state; agent grants are rejected |
| haeryong revisit show --document ID | Owner document flag with individual/create precondition |
| haeryong revisit set --document ID --flag true --if-match ETAG | Independent document-level flag |
| haeryong topics list/set | Inspect assignments or set an owner pin/accepted-or-rejected decision with the individual validator |
| haeryong generate --project ID --request-file PATH | Scoped request to create an explanation/draft with stable operation identity |
| haeryong ask --version ID --request-file PATH | Whole-version question by default, optional --page PATH and --region ID with that page, plus remembered or explicit --source-mode pinned/current; large text/state is staged automatically and the job reports actual grounding limits |
| haeryong questions list/show | Read saved exact-version questions, completed answers/citations and separately labelled partial/error state |
| haeryong evidence show/verify --version ID --evidence ID | show composes immutable authored capture_status and separate service verification/history. verify calls the explicit K verification endpoint; --expected-sha256 and --expected-excerpt-file are optional exact-byte expectations, never required author metadata |
| haeryong assessments list --version ID | Published history, captured_at and applicable target/scope/coverage; --coverage-set ID follows the cursor required-unit view with each outcome/reason/assessment linkage |
| haeryong jobs list/show/cancel/resume | Inspect/cancel/reconcile; resume carries current job ETag and any explicit uncertain-attempt retry decision |
| haeryong operations show OPERATION_ID | Query the original principal's receipt before retrying |
| haeryong scan --project ID --depth change-focused | Scoped manual scan; owner or a workflow scope scan.request derived from remembered authorized work, no separate grant prompt or budget/disclosure expansion |
| haeryong findings list/approve/reject | Exact finding context and individual precondition |
| haeryong overlays undo ID --if-match ETAG | Reverse only a still-compatible applied overlay |
| haeryong config show/plan/apply/export/import | Same authoritative validator/revision/plan flow as UI; export is a redacted configuration snapshot |
| haeryong lifecycle plan/commit | Same owner session handles archive/trash/restore/purge with exact plan/revision and ordinary impact confirmation |
| haeryong backup create/verify | Local coherent full backup with checksums, optional encryption and missing-reference-only report |
| haeryong restore plan/apply | Local maintenance-mode restore; rotate generations, invalidate grants/sessions, reconcile and pause exposure/automation |
| haeryong export plan/create/verify | Faithful streamed directory/archive output; plain selected-version files by default, optional separate metadata/personal records, distinct from operator backup |

CLI hyphenated depth values change-focused, full-scope and deep-semantic map explicitly to JSON change_focused, full_scope and deep_semantic. JSON output always uses the canonical underscore enum. --all-allowed-projects maps to scope all_allowed_projects within remembered owner configuration or a managed workflow's explicit scope; it does not expand discovery/disclosure. Config CLI apply maps to the API plan commit operation.

Paths are local to the invoking CLI. A remote CLI streams selected bytes; the server never interprets the same textual client path as an arbitrary host path. Remote CLI uses a configured HTTPS endpoint and its stored owner or scoped workflow bearer, with no separate remote owner role. Credentials are not forwarded to arbitrary hosts; first-owner bootstrap still runs locally. Commands requiring local execution fail clearly when only a remote endpoint is available.

## Directory input and ordinary file output

PATH may name an ordinary directory, ZIP or single HTML/file. No required index.html, manifest, author-assigned service IDs, digest inventory, source metadata, region declarations or extension allowlist is imposed. Resolve association using the [package binding table](package.md#remembered-association-and-operation-base): --document selects its actual project, a conflicting explicit --project fails, --project alone clears the remembered document selection, and --new-document sends document_id null and cannot be combined with --document. No explicit flags reuse the remembered exact binding. The owner without a project uses configurable Unfiled; a workflow uses its selected authorized project. Persistent association never stores a frozen base. Each new existing-document command uses --base-version or captures latest before upload and sends that explicit base; every same-operation retry retains the original journal/context/base. Only a low-level POST /imports with an omitted base selects latest at that request's first reservation. New-document input has a null base. Submit document_id explicitly as an existing ID or null, plus the resolved project matching the upload; a missing HTTP document_id is invalid and never triggers server-side workflow-memory lookup. Preserve the original request; stale, missing or denied targets never become new-document fallback. Haeryong generates missing IDs/defaults once per intended operation; matching paths/titles/hashes do not infer association or merge separate intents. --metadata selects a separate optional input; a payload manifest.json remains ordinary content. Malformed supplied metadata returns diagnostics while retaining sealed payload for a corrected or metadata-free new operation; absent metadata is valid.

Capture every selected relative path and byte, including hidden files, nested directories and represented empty directories/link text. No default ignore list silently discards .git, node_modules or an unknown asset type. Native/entry-aware input and ZIP preserve represented directories/link text; a browser FileList contains only the offered files and implied parents, so it cannot prove absent empty directories/links were captured. Report the actual selection/adapter capabilities and offer a suitable complete-tree path when needed. Explicit owner-selected exclusions define a smaller input selection and are reported before capture; do not invent exclusions from file count, size or presumed usefulness. Do not follow a symlink outside the selected snapshot by default. A single HTML import does not automatically scan its parent directory; select the directory when local dependencies are intended.

Prepare and reuse a resumable upload automatically, with progress, accepted-file/range checkpoints and cancellation. The HTTP input_mode, selection_name and exact chunk fields are transport preparation, not author metadata. Stopping file transfer retains resumable staging. jobs cancel also supports upload_seal: stop and fence verification, then return the non-abandoned upload to open only after worker stop is confirmed; a new completion can verify the retained bytes. Explicit discard maps to upload abandon and respects active/adopted/unknown-work pins. See [cancel versus abandon](import-workflow.md#cancelling-sealing-versus-abandoning-an-upload). Report open/sealing/sealed/abandoned and any recoverable transfer/seal error distinctly. uploads list/show recovers staging when the original invocation/credential is gone. Owner resume/abandon rechecks current authority and preserves the original exact target; byte reuse at a new base is allowed only for that same document. reusable/unavailable_reason report whether bytes remain available after staging transfer; a sealed state alone is no availability promise. No fixed HTML/total byte, file/page count or depth limit exists. A changed local input, actual disk/I/O failure or corrupt archive remains an explicit recoverable/incomplete result; it cannot publish a prefix. The CLI does not force the entire result into one RAM buffer, JSON body or compressed buffer. Reusing the original operation recovers its captured upload/receipt before attempting to recapture a changed directory.

For lost-response recovery, the caller journal exists before the final import receipt. Persist separate upload-create and complete keys before dispatch, and retain their upload/job references alongside the final import ID. Do not infer that publication never ran from a missing server receipt or lose the original pre-transfer base. Listing abandoned/staged data is not publication reconciliation. All this bookkeeping is automatic, not additional owner work.

Return logical document/version identity, chosen default or directory listing, all-page/file access and the exact actual source basis. For a page link use the registry's page, page_query, page_fragment and optional #region parameters. The user may set/clear a mutable reader preference for this exact version, or supply a different immutable default_page when creating a subsequent version. The reader preference precedes immutable default_page, root index.html, sole HTML and directory listing only when no explicit page link was requested; no original payload or published metadata is rewritten. Version reading-position stores the last-opened pointer/observations, while each page-position owns its progress and ETag independently. Autosave clients coalesce and serialize writes per resource with the latest acknowledged ETag; each submitted save has one operation ID, not one per scroll event. Preserve unsaved context and reconcile a 412 before sending another save. Appending a new AI result to the same document produces a new complete directory snapshot; it does not silently modify the published old version.

evidence verify maps optional --expected-sha256 to expected_bytes_sha256 and --expected-excerpt-file to the exact UTF-8 expected_excerpt (or mutually exclusive expected_excerpt_ref through purpose=body staging). The operation targets the immutable evidence's exact revision/location and never selects a newer branch. It records captured, matched, mismatch or unavailable separately from the author's capture_status; only the first two are successful target checks, and captured without an expectation means limited exact-target resolution/capture. Display checked_at, nullable verified_at, current availability/reason and the verification ID/history. It never claims observed provider input or factual claim truth, and starts no AI call. The [HTTP operation](http-api.md#imported-evidence-verification) owns the response shape and current-authority/pin rules.

## Scoped portable export

Portable export serves the owner's ability to inspect and retain selected explanations and their anchored personal context. It is distinct from the full operator backup/restore contract and does not automatically include operational secrets, full source repositories or a working library database.

1. Resolve an explicit project/document/version selection and record exact version IDs, chosen record kinds, estimated bytes/counts, configured source/export scope, dependency states and output format. A plain selected-version export can prepare this plan automatically within the requested command; no extra permission ceremony is needed for a normal read/copy.
2. Run using the owner's normal remembered CLI/session credential. A managed workflow may export only explicitly granted records and never inherits personal-record export access merely from publishing scope. Export does not expand source disclosure. Execution uses the local CLI/IPC path initially; no new remote export endpoint or separate administrator identity is required.
3. Choose a local output destination and directory/archive format. A plain version export preserves its original payload directory by default, with no mandatory service manifest/index injected among the author's files. Encryption is optional and can be remembered as an owner preference. If enabled, use an established supported format and report encryption failure without silently switching that export to plaintext. Overwriting an existing destination requires the normal explicit file-operation choice.
4. Capture one consistent metadata/object set, pin required existing objects and stream to an incomplete staging output. Preserve file bytes, relative paths, Unicode/case and represented directories/links where the destination supports them. Verify each included object and any selected export index before finalization; no all-memory archive/Blob, file-count/size cap, automatic flattening or asset rewriting is required. A platform name collision or unsupported output operation is reported before overwriting, without claiming a completed lossless copy.
5. Return the finalized export ID/index, exact selected scope, omitted/unavailable dependencies and verification result. Interrupted output is incomplete, not a successful export.

When Haeryong metadata/personal records are explicitly selected, a separate export index records export/schema version, originating library ID, exact document/version/page/region identities, normalized/supplied metadata and payload hashes, and a streamed/cursor-capable file inventory. Metadata lives in a separate destination or explicit outer export envelope, never overwriting a payload filename. Include only the selected review history/current state, Revisit, reading positions, anchored notes/history, completed questions/answers/citations, authored references and eligible overlay/topic/assessment decisions. Personal records retain owner/version/page/region references and may reference large immutable context objects; they are not inserted into a new library merely by unpacking. Plain directory/file export requires no such records or index file.

Dependencies are marked included, external_reference, tombstoned, unavailable or excluded_by_scope. Do not recursively export another project or disclose a denied target title/body just to make the archive self-contained. Preserve known authored IDs and minimal authorized tombstones without retargeting to latest. An exact package export requires authority for all its contained bytes, including retained source assets; otherwise exclude/fail that package explicitly, rather than silently removing files and claiming the original digest still matches.

Ordinary source-code, binary or data files already inside the selected result directory are original payload and are preserved by a plain complete export. A source-like extension or missing provenance does not cause omission. Additional separately retained source snapshots are included only when explicitly selected and authorized. Copy only already retained, authorized additional source assets. Reference-only sources remain references, and unavailable source is reported honestly. Export must not fetch/pull/scan a repository or create new snapshots as a side effect. Live job/unknown-attempt ledgers, grant/session secrets, bootstrap/provider credentials and full instance operating policy belong to operator recovery, not this narrow export.

verify checks archive protection when used, exact file/inventory digests and any explicitly selected metadata/local-ID/anchored-record bindings, reporting intentional external/tombstoned dependencies separately from corruption. It proves archive integrity/structure, not factual correctness, source availability or successful restoration. General cross-library merge and re-import of personal records are outside this v1 export contract; original package import remains subject to normal identity/permission/conflict rules.

## Agent handoff

The skill probes capabilities and uses the scoped credential/context issued for the selected workflow. Routine issuance derives from remembered owner choices rather than asking the owner to manage a grant before each invocation. It searches eligible knowledge, reads exact authorized evidence, validates its package and imports using a stable operation ID and explicit publication intent. When the selected project's remembered post_work_scan_enabled setting permits it, a post-work scan uses project-scoped scan.request derived automatically from that workflow selection, capturing current policy/targets without broadening them. Explicit scope is not a separate per-invocation manual grant step.

It never installs/exposes a service, approves a device, creates owner grants, changes source/disclosure authority, completes review, purges content or performs personal-data export as incidental explanation work. Generation writes to the configured artifact destination or staging, including an owner-selected directory inside the project, without overwriting unrelated source files. Unsupported provider/status capabilities and source absence remain visible; no silent API-key/account/provider fallback.

## Examples for future implementation

```sh
haeryong doctor --profile default --json
haeryong search --project PROJECT_ID --query "selective retry" --json
haeryong package validate ./explanation --json
haeryong import ./explanation --project PROJECT_ID --operation-id OPERATION_ID --json
haeryong files list --version VERSION_ID --json
haeryong export create --version VERSION_ID --format directory --destination ./saved-explanation --json
haeryong operations show OPERATION_ID --json
haeryong jobs show JOB_ID --json
haeryong questions show QUESTION_ID --json
```

IDs are placeholders; ./explanation may contain several HTML pages and arbitrary supporting files without a manifest. Exact supported flags, output schemas and optional encryption format must be verified by the owning implementation tasks before these commands are presented as an operational runbook. This specification has not executed them.

## Current headless commands (TASK-002-05)

The current executable implements `auth connect [new]`, `auth sessions list/revoke`, `projects list`, `package validate PATH`, `import PATH`, `documents show ID [--version ID]`, `files list/get/download`, `uploads list/show/resume/abandon`, `operations show`, `jobs list/show/cancel`, `events`, `config show/plan/apply/export/import`, `review show/history/set`, `revisit show/set`, and authenticated `doctor`/`status`. Other catalog families remain FEATURE_NOT_READY; a current workflow credential issuer, native viewer, provider and scheduler are not implemented. Explicit protected local pairing approval remains separate from `auth connect`; connect never approves itself. Selected custom credential files determine the remembered endpoint before local discovery; an explicit endpoint still must match that selected credential.

`--credential PATH` selects a protected credential bound to the exact endpoint origin, profile and library; no raw bearer flag is accepted. `--client-dir PATH` selects protected journals/snapshots independently of daemon profile files. Journals precede dispatch and retain original I/U/S, exact target, pre-transfer base and local capture identity. The optional metadata transfer has its own persisted create/completion keys and targets final I. Retried create, payload/metadata completion and final dispatch reconcile those original identities before rereading local input. Pending K responses poll the original operation; reconciling returns exit6; definitive failed receipts preserve their original error/exit. A completed202 receipt remains job creation, with a separately reported job outcome. A response guard denial after a committed configuration change retains the pending journal until its original outcome can be read with current authority.

`files list --version ID [--cursor C --limit N --directory PATH --kind KIND]` writes one JSON envelope per entry and follows every cursor. `files get --version ID --path PATH --output PATH [--resolve-link]` preserves a verified partial and resumes the same resolved representation with Range/If-Range. `files download --version ID --output DIRECTORY` reconstructs the complete original tree through those same cursors/verified streams, including empty directories and literal link text; this is a selected-version transport helper, not the future operator export engine. Output publication never silently overwrites an existing path.

`uploads resume ID [--input PATH --operation-id S]` completes staging only. It checks each confirmed range against the retained captured input and sends missing intervals, regardless of the originating client's chunk size. An omitted S is generated and retained once. Existing target/purpose/selection name remain unchanged. Cancellation retains input; `uploads abandon ID` explicitly ends reuse under its own K.

`config plan PATH` and `config import PATH` both validate and create an impact plan; neither silently commits it. `--format json|yaml`, `--project ID`/`--scope project`, `--expected-config-revision REV` and optional `--operation-id I` bind the selected request. Automatic plan identity is persisted before file/revision capture. A retry uses the journal before opening the current file or recapturing revision. `config apply PLAN --expected-config-revision REV --confirmed` uses the natural plan identity; an optional operation key aliases the same outcome. `config export [--output PATH]` exports the current validated YAML snapshot without overwriting an existing file. Recognized flags unsupported by a current command fail instead of being ignored.

`events [--cursor EPOCH:SEQUENCE]` exposes an authenticated polling batch or explicit resync control. Resync does not save pending edits or mutate caller journals. Clients read current authorized state after resync, retain that head, then consume newer notifications. [Current execution evidence](../testing/query-cli-configuration.md) distinguishes actual checks from future native/private-device/provider gates.

## Source checks and observation delivery (TASK-004-01)

These are additive implemented commands; source observation's HTTP route and
project-source projection are provisional additive selections. No AI is called.

```sh
haeryong projects register --name NAME [--operation-id ID]
haeryong sources approve --project ID --source-id ID --kind git --root-path PATH --repo-id REPO --retention-mode reference_only [--provider-ids IDS] [--confirmed]
haeryong sources status --project ID
haeryong sources observe --version ID [--page PATH | --page PATH --region ID] [--operation-id ID]
haeryong evidence show --version ID --evidence LOCAL_ID
haeryong evidence verify --version ID --evidence LOCAL_ID [--expected-sha256 HEX | --expected-excerpt-file PATH] [--operation-id ID]
haeryong assessments list --version ID [--coverage-set ID]
```

Projects register and sources approve/observe are owner actions. Approve prints
a project-scoped configuration plan; --confirmed commits via existing plan
routes, and --plan-id commits a recorded preview. Status lists logical identity,
retention/disclosure and recorded last_observed_* without probing the host.
Evidence show/verify uses the canonical query alias, preserving arbitrary local
IDs. Excerpt files are staged through the operation-bound body transport as
needed. The CLI accepts at most one expectation form: digest or excerpt file;
supplying both is exit 2. The HTTP verification request separately permits a
digest together with one inline or referenced excerpt expectation.

Automatically generated keys are journaled before dispatch; interrupted reruns
resume the original key. Explicit keys replay completed outcomes. Plan and commit
have separate stable identities. 202 and uncertain transport responses poll the
original operation, preserving the exact request/expectation fingerprint until
the result or definitive failure has been emitted. A recorded terminal failure,
including a stored 503 replay, is definitive and releases the automatic key only
after that failure is emitted. An ordinary 4xx rejection also releases it after
emission, except authority denials (401/403) and reconciliation/unknown-provider
outcome demands. An unrecorded 5xx, such a denial or demand, transport loss or a
local error keeps the pending key for recovery of the original outcome; none
authorizes a second effect.

`assessments list --coverage-set ID` follows every next_cursor and emits one
combined `{items,next_cursor:null}` only after all pages succeed. A failed page
fails the command without emitting a partial denominator. The default form
returns the current history/applicable composition. Coverage HTTP clients may
also use attempt_run_id from units_url as specified by the registry; this CLI
flag selects the stored set and introduces no attempt-run flag.

## Static search delivery (TASK-004-02 M4)

`haeryong search [--query TEXT] [--project ID] [--lifecycle active|archived|all]
[--versions latest|all] [--kind document|page|region|source|attachment]
[--cursor TOKEN] [--limit N] --json` performs one authenticated GET page.
An omitted or explicitly empty query returns safe coverage, no hits and exit 0.
The JSON data is exactly SearchPage, including required `index.reasons`, decimal
counts and `next_cursor`; pagination never silently accumulates or changes scope.
Foreign flags, including `--version`, `--operation-id` and `--new-document`, fail
with exit 2. Query/filter errors use exit 2, authorization exit 3, unavailable
service exit 5; healthy zero-match, pending or partial coverage use exit 0.
No receipt, implicit indexing, retry or rebuild action is selected. Recovery:
TASK-004-04 rebuild. The public route/CLI work while `features.search` remains
false pending M5's complete client/UI delivery.


## Implemented reference commands (TASK-004-03 M3)

`haeryong references list --version ID [--cursor C --limit N] --json` emits the
closed reference collection. `haeryong references resolve --version ID
--reference-id ID [--page PATH --region ID] --json` emits one typed reference
view. These use the selected protected credential and normal library/profile
identity preflight, then exactly one corresponding GET. Query-form IDs are
encoded once, preserving slash, dot, percent and Unicode. No implicit retry,
rebuild, target selection or operation ID is added. Foreign flags fail before
credential/network access; region requires page and limit is 1–200.

Exit 0 includes honest 200 unresolved states. Invalid flags/API input exit 2;
authentication/authorization and hidden or unknown target, source or reference
(`NOT_FOUND`) follow the shared registry's exit 3; `TARGET_GONE` exits 4 and
service unavailability exits 5. These additive commands remain
read-only. M4 separately delivers the shared clients/Reader flow and enables
`features.references`; the CLI never changes that capability.

## Backlinks and owner index recovery (TASK-004-04)

`haeryong backlinks list --version ID [--page PATH --region ID --cursor C --limit N]
--json` emits one closed backlink_page, with complete visible scoped edge/distinct
referring-document counts and an optional next_cursor. Region requires page;
limit is 1–200. Existing `references list` adds live outgoing counts while retaining
its declaration pagination. Query encoding and protected credential preflight
remain unchanged; foreign flags/invalid command shapes fail before network access.

`haeryong indexes validate --version ID --kind search|references --json` emits the
read-only validation result. Every completed HTTP 200 exits 0, including
inconsistent/incomplete; only registry error responses produce nonzero exits.
`haeryong indexes rebuild --version ID --kind search|references|all
[--operation-id ID] --json` uses the standard durable mutation journal and receipt
reconciliation and returns its operation_id/result with created job_ids. Poll the
original receipt/job identities to inspect the independent outcome; do not create a
new key after a transport timeout. Current owner authority is required for both
index commands, and rebuilding never happens implicitly during reading/search.

Rebuild replaces terminal same-revision work slots while retaining job history and
D4 edges. Ordinary same-key enqueue remains terminal/idempotent. Job cancellation
is effective at atomic REINDEX boundaries, not a mid-statement abort. Missing or
altered reverse-index DDL remains startup-refused: no offline repair CLI is added;
supported recovery awaits EPIC-005 verified restore. The physical repair scope is
an intact-definition reverse index's contents, as specified in the HTTP contract.


## Explicit personal Review (TASK-005-01)

`review show --version ID` returns the exact-version Review resource, including
explicit absent defaults/create precondition. `review history --version ID
[--cursor CURSOR --limit N]` returns a history page. `review set --version ID
--action start|complete|reopen|clear (--if-match ETAG | --if-none-match '*')
[--operation-id UUID]` maps action to the corresponding state; it accepts no
separate state override or page/region target. Controls and operation IDs are
validated before dispatch. This additive provisional family is implemented;
other future catalog families remain unavailable.

The protected journal persists the original target, action/state body and semantic
precondition before dispatch. Response loss reuses that same operation and original
condition even after its ETag became stale; changed input cannot claim its result.
Missing receipts after a runtime-generation change require explicit reconciliation.
JSON mutation output contains operation_id/result, and each PUT result also carries
operation_id. Exit classes remain 2 invalid, 3 denied/session, 4 conflict/precondition,
5 unavailable and 6 uncertain reconciliation. Owner credentials are required; no
workflow grant permits personal completion. Schema revision 10 requires a capable
binary, not an attempt to downgrade the live database.


TASK-005-02 Revisit uses exactly `show --document ID` or
`set --document ID --flag true|false`, with one `--if-match ETAG` or
`--if-none-match '*'` for set and an optional final `--operation-id`.
It uses the shared afterDefinitive outcome classification: transport/decoding loss,
authority denial, unrecorded 5xx and reconciliation demands retain the automatic key
and original conditions. Only a definitive outcome acknowledged to output releases
that key. No notes CLI is selected; Notes is a shared Reader action.

## Current document lifecycle commands (TASK-005-03)

Additive provisional commands use the same current owner, exact plan and receipt rules:

```sh
haeryong lifecycle plan --document DOCUMENT_ID --action trash --expected-revision REVISION --json
haeryong lifecycle show PLAN_ID --json
haeryong lifecycle commit PLAN_ID --expected-revision REVISION --confirm --json
haeryong trash list --json
```

Action is archive/trash/restore; restore also exits Archive. Plan permits an optional
--operation-id; show/list support --cursor and --limit, and Trash supports --project.
Commit confirmation is explicit. Without a separate alias its durable operation ID is
PLAN_ID; retries reuse it and the original body. Optional --operation-id aliases that
same result. Unknown outcomes retain the plan identity and require reconciliation,
never a fresh generated commit key. Invalid/stale plans preserve owner intent.
Purge, backup and operator restore remain unavailable under their ordered Tasks.

## Manual purge (TASK-005-04)

`haeryong lifecycle plan --document ID --action purge --expected-revision N --json`
uses the same show/commit --confirm flow and ordinary current owner session.
Purge commit returns acceptance/job identity; `haeryong jobs show JOB_ID --json`
reports independent cleanup, paused retention and automatic retry. No resume
command is added. Reuse the exact plan/body after a lost response; never create a
new destructive request to recover the old result. Acceptance is irreversible,
including when I/O cleanup is paused; cancellation returns INVALID_TRANSITION.

## Host-local backup, export and protected restore (TASK-005-05)

The following commands are implemented. Backup/export create requires a running local
daemon, its exact protected profile and a current owner session. Restore requires an
explicit destination `--profile` or `--data-dir` and the daemon stopped. `--json`,
credential/client-directory selection and existing exit classes remain applicable.
Paths belong to the host executing this CLI; a remote endpoint cannot submit them.

```sh
haeryong backup create --destination DIR [--operation-id UUID]
haeryong backup verify --input SET
haeryong backup resume SET_ID [--operation-id UUID]
haeryong backup abandon SET_ID --confirm [--operation-id UUID]
haeryong export plan (--version ID | --document ID | --project ID) --format directory|zip --destination PATH [--versions latest|all] [--lifecycle active|archived|trash|all] [--records review,revisit,reader,notes,references] [--metadata-destination DIR] [--include-retained-sources] [--operation-id UUID]
haeryong export show PLAN_ID [--cursor UUID]
haeryong export create PLAN_ID [--operation-id UUID]
haeryong export create --version ID --format directory|zip --destination PATH
haeryong export verify --input PATH [--index EXPORT_INDEX]
haeryong restore plan --input SET --profile DEST
haeryong restore apply PLAN_ID --profile DEST --confirm [--operation-id UUID]
haeryong restore review --profile DEST [--choices FILE --confirm]
```

`--versions` applies only to document/project and defaults to latest per document;
all selects all published versions. `--lifecycle` defaults to active. Selectors are
exclusive; empty selection, unavailable version and later authority loss fail without
substitution. A plan retains its selected version IDs; later publications never join.
Directory/ZIP export never overwrites an existing final output. A plain one-version
copy adds no service file. Multiple versions have distinct UUID roots and an outer
index. Metadata opt-in uses a separate destination or an outer payload/metadata envelope.
Plain export verification requires its separately retained index; checksum verification
cannot claim a merge or source correctness. Encryption/key flags are visibly rejected.

Submission journals the original operation before effects and reuses the same acceptance
on response loss. Jobs use existing inspection/cancel commands. An explicit `export create PLAN_ID` after a failed/stopped attempt creates a linked retry of that same frozen snapshot; the original key always replays its original acceptance. `backup abandon SET_ID --confirm` can discard either named kind of owned incomplete copy set after its worker stops. It never selects arbitrary host input or deletes another set. Cancelled/failed backup
resume creates a linked job for the same set; it does not clear cancellation. Host paths
stay in protected local requests/plans, never public job/status output. Restore's natural
plan identity and optional aliases bind one switch, with durable results outside the
replaced storage. On a runtime change, uncertain client requests reconcile rather than
automatically receiving new operation IDs. `backup verify` is read-only; the verified_at
status of creation is not silently relabelled as a later trial restore.

For the implemented Notes record kind, a selected original/current/retained historical
anchor selects that annotation identity's complete permitted retained current state,
history and body/context bytes. Revisions keep exact anchors outside the payload
selection; payload membership does not grow. Verification checks identity/revision
consistency and each context_ref's actual version/page descriptor, alongside the
other record families' inner owner/document/version/resource/local-reference bindings.
A completed restore plan replays its durable external terminal outcome even after
later restores and expiry/input unavailability; older replay never changes the later
library or rotates its epochs. A rolled-back plan retains its failure outcome. Natural and already-bound
alias replay checks the protected external binding and renews its directory durability;
a later dataset's unrelated receipts cannot invalidate that outcome. A new alias is
new admission and must not conflict with any applicable owned current/prior/incoming
owner receipt namespace. Latest-generation database alias projection retains its
identity checks. No original backup is required merely to replay an old result.
An admitted staging restore retries the same plan after preview expiry, rechecking
its matching journal, original destination state and immutable input. An unexecuted
expired or malformed plan still refuses before journal or alias publication.

## Implemented managed source inspection (TASK-006-02)

The authenticated CLI adds the following narrow operations. IDs and source paths
refer to the managed capture API, never an arbitrary server filesystem root.

```sh
haeryong sources capture --project PROJECT_ID --input request.json
haeryong sources captured --capture CAPTURE_ID
haeryong sources captured --capture CAPTURE_ID --cursor CURSOR
haeryong sources diff --capture CAPTURE_ID --other OTHER_CAPTURE_ID
haeryong sources read --read READ_ID --output NEW_LOCAL_FILE
```

Capture accepts optional --operation-id and uses the existing durable command
journal/receipt recovery. The strict request file contains the selected logical
source IDs, mode/intent/revision and relative paths described in the registry.
A missing/partial capture remains inspectable with complete false and its full
original selection. Captured/diff reads never start Git or AI. Diff also accepts
--cursor. Source bytes stream to an exclusively created private output file;
the client verifies the returned SHA-256, syncs completion, preserves existing
files and removes only its own incomplete output. It never prints raw source as
JSON diagnostics or reports an input observation. Current scope/visibility is
rechecked by the service on every operation, including retained reads and replay.

Source/default/provider settings continue through normal configuration
plan/confirm/commit. The default provider does not widen any source provider_ids.
Production provider transport and source-grounded answer/generation commands
retain their ordered later implementing owners.

## Provider transport selection and status (TASK-006-03)

haeryong providers status performs the authenticated owner GET /api/v1/providers
and returns its normalized status/nullable allowance. It cannot log in, switch
billing, start an inference or choose a source. Existing job show/list/cancel/
resume continue through the same receipts and exact preconditions.

Only local daemon run accepts --provider-binary with an absolute path to an
already installed pinned executable. The factory checks release version and
SHA before executing it. Missing/unverified binaries leave AI unavailable while
normal local functions remain usable. PATH discovery does not substitute an
unverified newer release or automatically downgrade it. This is an ephemeral
startup selection, not a new effective-configuration field or account change.

## Generation and draft CLI (TASK-006-04)

The current executable additionally implements:

```sh
haeryong generate --request-file request.json [--project PROJECT] [--operation-id K]
haeryong drafts list [--project PROJECT] [--cursor CURSOR]
haeryong drafts show DRAFT
haeryong drafts history DRAFT [--cursor CURSOR]
haeryong drafts reject DRAFT [--if-match ETAG] [--operation-id K]
haeryong drafts replace DRAFT --input WHOLE_DIRECTORY_OR_ZIP [--metadata METADATA_JSON] [--if-match ETAG] [--operation-id K]
haeryong drafts publish DRAFT [--if-match ETAG] [--operation-id K]
haeryong auth grant create --input selection.json [--operation-id K]
haeryong auth grant rotate --input selection.json [--operation-id K]
haeryong auth grant show BINDING
haeryong auth grant revoke BINDING [--operation-id K]
```

Generation's strict request file uses the HTTP generation shape; omitted
project/document fields resolve through the selected credential's authorized
remembered association. Explicit file targets are preserved and checked against
an explicit --project. Existing-document base is captured once before staging.
Large prompt bytes use the existing protected snapshot and resumable object
transfer; raw prompt is absent from the final small control journal. U/S keys
are distinct from K. Normal local owner launch automatically reuses its remembered
finite workflow grant, fixing final principal/library before preparation/final
submission. Explicit owner-authenticated remote API submission remains supported;
workflow refusal never falls back to owner or Unfiled. Providers/artifacts receive
no bearer or management authority. A generation grant includes its own job
read/cancel, not draft publish, owner resume or arbitrary other jobs.

An import grant includes `job.read` so the normal CLI can follow its accepted
import to completion. The job must belong to that same workflow principal and
retain an allowed upload purpose and project/target. This grants no job cancel,
resume or personal Review/lifecycle authority. Credentials captured before this
action was added retain their original scope; normal fresh issuance or explicit
rotation is required to obtain the new action. Reentering the same import K
recovers its original receipt/job/version rather than publishing another result.

Initial workflow issuance reloads the protected frozen request before its first
send, so JSON serialization of optional selection fields cannot make the later
acknowledgement conflict with the saved intent. This adds no retry or credential
rotation and does not relax immutable journal comparison.
The unchanged-selection comparison uses that same serialized representation for
optional source selection, so omitted/null or insignificant JSON whitespace do not
rotate an otherwise unchanged remembered credential. All other selected fields
and current authentication checks still apply.

The protected caller journal preserves original target/base, prompt snapshot,
source/prior selections, final principal and preparation/final keys. Same-key
reentry looks up the original receipt before reopening local files. Unknown or
paused generation retains its implicit pending K; it cannot turn the same
interrupted command into a new generation. Output distinguishes admission, current
job state, publication and required reconciliation. Existing owner jobs resume
keeps current ETag/expected-generation/decision; new usage needs its explicit
retry authority.

Draft commands use the explicitly selected owner or exact draft-scoped
credential. Missing draft authority fails without owner fallback; a generation
grant does not imply knowledge of a future draft ID or permission to publish it.
When If-Match is omitted, the command captures the current individual validator
once, before preparation, and journals it. Replacement transfers the entire tree
and optional metadata under exact draft targets. Original operation replay does
not require the old local directory to remain. Stale base/validator conflicts
retain the candidate and original intent; replacement cannot alter its base.
No command automatically completes personal Review.

## Saved Ask and question history (TASK-006-05)

```sh
haeryong ask --version VERSION --request-file REQUEST [--page PATH] [--region ID] [--source-mode pinned|current] [--operation-id K]
haeryong questions list --version VERSION [--cursor CURSOR]
haeryong questions show QUESTION
```

Ask defaults to the whole exact version; a non-null region requires its page.
The strict request file uses the question creation shape, with omitted nullable
anchors filled only by the CLI's captured whole-version/page selection. Explicit
flags and file selectors must agree. Current owner authentication is required;
there is no provider/agent owner fallback or implicit workflow scope expansion.

Before preparation, the caller records its automatic or explicit K, original
launcher principal/library/runtime, complete request snapshot and captured
selectors. It snapshots full question text and serialized display_state, then
uses separate durable object-upload/seal and context-create identities. The
final question body holds body_ref/context_ref rather than duplicating large
text/state. An existing context_ref retains its original binding. Independent
preparation keys never replace K, and byte limits in a selected provider are not
a storage/author-state cap.

Same-key reentry reads the original receipt before reopening request files or
resolving later defaults. A missing receipt across runtime change requires
reconciliation; no automatic POST or new Execute follows. Unknown results keep
the original pending key in machine error metadata. Definitive results release
an automatic pending entry only after successful caller output, using the
original launcher and intent. Output failure retains exact recovery identity.
Saved question show/list and completed Ask output enumerate saved answer history
and stream full question/context/body under current authority. Reopening saved
history never executes the provider. Partial/error output is not a completed
verified answer. Jobs cancel/resume retains the existing explicit decision,
ETag, expected-generation, restore-hold and receipt-first semantics.

## Endpoint doctor (TASK-007-01)

Authenticated `haeryong doctor` preserves the status envelope and adds
`endpoint_checks` from coherent current status/configuration reads. It reports
the selected `connected_endpoint`, persisted `configured_local_endpoint`, actual
`runtime_local_endpoint`, `ephemeral_override`, `remote_enabled`, nullable
`external_base_url` and each configured renderer mapping's
`configured`/`supported`/nullable `reason`. Support describes configuration,
not an allocated listener or successful private route. `reachability` verifies
only the already selected authenticated endpoint and marks other endpoints
`not_tested`; doctor neither probes arbitrary configured URLs nor opens an origin
ledger/listener. A revision change between reads requires retry. Local recovery
shows the same profile/data directory selection and an explicit unused `--port`
override, which remains ephemeral. Missing discovery/identity errors give local
startup/recovery guidance; no random listener fallback or credential forwarding
is performed. The status API itself is unchanged.

## Scan and findings CLI (TASK-008-01)

```sh
haeryong scan [--all-allowed-projects | --project PROJECT_ID[,PROJECT_ID...]] [--document DOCUMENT_ID] [--version VERSION_ID] [--path PATH [--region ID]] [--depth change-focused|full-scope|deep-semantic] [--source-mode pinned|current] [--target-set TARGET_SET_ID | --capture TARGET_SET_ID] [--operation-id K]
haeryong findings list --job JOB_ID [--kind KIND] [--status STATUS] [--limit N] [--cursor CURSOR]
haeryong findings approve FINDING_ID [--if-match ETAG] [--operation-id K]
haeryong findings reject FINDING_ID [--if-match ETAG] [--operation-id K]
```

`haeryong scan` validates `--depth` (`change-focused`, `full-scope`, or
`deep-semantic`, normalized to `change_focused`, `full_scope`, `deep_semantic`),
`--source-mode` (`pinned` or `current`), and `--region` (which requires
`--path`), and rejects conflicting `--all-allowed-projects`/`--project` or
`--target-set`/`--capture` flags (`exit 2`). Before sending `POST /api/v1/scans`,
the CLI persists a local caller journal entry binding `operation_id`,
`runtime_generation`, `library_id`, `principal_id`, and the canonical request
payload. If the `202` response is lost, the CLI exits `5` with the pending
`operation_id` in error data; same-intent retry queries
`GET /api/v1/operations/{id}` first and reuses the committed scan receipt, or
resends the exact saved request only when the receipt is absent under the same
`runtime_generation`. `haeryong findings list --job` enumerates job findings for
the owner (`--limit` `1..200`). TASK-008-05 implements approve/reject with
required individual --if-match and the existing durable mutation journal.
Opaque finding IDs are accepted; changed preconditions require a new deliberate
intent, never an automatic refreshed-validator overwrite.
See [scan inventory evidence](../testing/scan-inventory.md).

## Task04 management reads

`haeryong related list --version ID [--path PATH --region REGION]
[--cursor CURSOR --limit N]` reads separate applied AI relations.
`haeryong topics list [--document ID --version ID --path PATH --region REGION]
[--cursor CURSOR --limit N]` reads owner topic assignments. Page requires version;
region requires page; limit is 1..200. Paths and opaque cursors preserve exact
characters. Findings list remains the proposal read, with captured evidence and
status. TASK-008-05 adds the owner operations below.
Existing config read/plan/commit handles category settings; neither reads nor mode
changes produce a new document or provider operation.
Related rows share the closed available/unavailable DTO: unavailable includes only
the opaque overlay ID, authorized focal anchor, AI origin, decision and revision/
validator, without hidden target/evidence details or a distinguishing cause.
Topic finding/effect legacy missing basis reads as legacy_unknown; new records
carry explanation_text or source_verified from established evidence.

## Owner decisions and reassessment (TASK-008-05)

```sh
haeryong topics set ASSIGNMENT_ID --pinned true|false --decision accepted|rejected --if-match ETAG [--operation-id K]
haeryong overlays undo OVERLAY_ID --if-match ETAG [--operation-id K]
haeryong scan --finding FINDING_ID --expected-finding-revision N --document DOCUMENT_ID --version VERSION_ID --depth change-focused|full-scope|deep-semantic --source-mode pinned|current [--path EXACT_PAGE --region ID] [--target-set TARGET_SET_ID] [--operation-id K]
```

Topic set sends actual conditioned PUT. Current undone Topic PUT is refused;
reassessment and separate new-finding approval are required. Findings approve/
reject and overlay Undo retain the original operation/request/preconditions
through the existing journal and receipt recovery. Missing --if-match is a usage
error; definite API conflicts preserve their actual code/reason. Repair202 means
pending repair, not application. Scan --path is page_path for a finding-bound
request; explicit origin revision/document/version/depth/source mode are required.
This does not add content draft publication or a force option. See
[finding decision evidence](../testing/finding-decisions.md).
