# CLI contract

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
| haeryong review set --version ID --action complete --if-match ETAG | Owner start/complete/reopen/clear; CLI maps action to the registry's state; agent grants are rejected |
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

The current executable implements `auth connect [new]`, `auth sessions list/revoke`, `projects list`, `package validate PATH`, `import PATH`, `documents show ID [--version ID]`, `files list/get/download`, `uploads list/show/resume/abandon`, `operations show`, `jobs list/show/cancel`, `events`, `config show/plan/apply/export/import`, and authenticated `doctor`/`status`. Other catalog families remain FEATURE_NOT_READY; a current workflow credential issuer, native viewer, provider and scheduler are not implemented. Explicit protected local pairing approval remains separate from `auth connect`; connect never approves itself. Selected custom credential files determine the remembered endpoint before local discovery; an explicit endpoint still must match that selected credential.

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
