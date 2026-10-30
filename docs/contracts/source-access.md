# Source access, snapshots and disclosure

**Documentation baseline 1.6.0, proposed runtime v1.** **Inherited requirement:** exact source grounding from the owner's selected roots. **Proposed default:** `reference_only` retention with opt-in `selected_snapshot`. Project, source and provider selections are remembered rather than approved on every read. Identity, retained bytes, live availability and freshness remain separate facts.

## Registry and authority

A project registers logical repository/document identity independently from a host path. The owner selects canonical source roots, provider/disclosure use and an explanation-output destination. Record the selected actions so the daemon can recheck them automatically; a separate prompt or elevated grant for each read is not required. Changes that expand the selected source or provider disclosure show their impact for owner confirmation. A link, imported path or model-written citation does not expand the daemon's scope. The browser receives source identities and availability rather than an arbitrary filesystem interface.

EPIC-004 owns the minimal approved reader, exact revision resolution, deterministic source-change observation and explicit checks of imported evidence before the full EPIC-006 Q&A/source integration. TASK-004-01 implements the check operation, history and feature-gated Evidence action in the shared UI. TASK-004-05 presents observation versus semantic assessment versus personal Review; it does not own this capability flip. An absent/false features.source_verification leaves the action unavailable without blocking ordinary reading. These operations use the same remembered source authority and exact-revision rules. Registering source identity alone does not prove that bytes are reachable or permit a provider submission. Configured mapping kinds git/files are distinct from captured source kinds git/snapshot; a files mapping produces a snapshot when selected bytes are actually captured.

Haeryong may use source-reading functions such as `resolve_revision`, `list_paths_within_scope`, `read_file_at_revision`, `read_selected_snapshot`, `compare_revisions` and `locate_symbol`. These service functions check saved project/action scope and return typed evidence identities. A broker is an implementation option, not a mandatory replacement for normal provider tools. External agents use the owner's existing environment; managed adapters record their supported native permission mode and actual limits as described in [AI adapter](ai-adapter.md).

Haeryong's own Git reader uses incremental cancellable operations and argument arrays, preferably direct object reads that preserve the requested revision and do not modify the checkout. Paginate large path inventories and stream selected objects instead of imposing a source file-size or file-count ceiling. Avoid invoking checkout filters, external diff/textconv or hooks merely to read evidence, and record the chosen Git/library behavior. Automatic fetch/pull is outside the baseline. This does not require disabling the owner's ordinary Git configuration, agent hooks or plugins globally, or proving containment of their unrestricted tools.

## Identity and runtime availability

Source metadata is optional when importing or viewing a result directory. Without a captured basis, retain and render the files with unknown/unverified source status. The fields below describe evidence when present; they are not mandatory packaging requirements for every HTML page or attachment. Displaying an imported source claim does not itself verify that claim.

| Fact | Required meaning |
| --- | --- |
| Source identity | Stable logical repository/document ID plus object format and full revision, or an explicitly captured snapshot ID |
| Author capture declaration | Immutable supplied `capture_status`: `verified`, `reference_only` or `unavailable`; not a service check, current access grant or proof that a claim is correct |
| Service verification | A separate check outcome and timestamp under the [shared registry](schema-registry.md#source-and-assessment-views); never an overwrite of the author's declaration |
| Runtime availability | `available`, `unavailable` or `denied` for this principal, action and exact target |
| Bytes origin | `retained_snapshot`, `approved_source` or null when no bytes can be supplied |
| Freshness | Assessment against a stated target and checked scope; inability to read source is not an outdated verdict |

Resolve these fields independently. A missing checkout may coexist with available retained evidence. A known commit with missing objects remains the same source identity but is unavailable for that read. Revocation yields denied access without changing historical provenance. A changed source identity triggers reassessment within policy; it does not establish semantic outdatedness. Canonical enums and typed errors are recorded in [schema registry](schema-registry.md).

## Source modes and captured bytes

The registry owns mode precedence: explicit source_mode wins; otherwise historical intent uses pinned, and other intents use the remembered project default (pinned when absent). moving_target is explanatory intent, not an instruction to force current or perform source reads while listing. Explanation-only references identify exact stored explanation bytes separately from project-source citations; they do not establish observed_input or posthoc_verified project evidence.

| Mode or retention | Capture | Meaning |
| --- | --- | --- |
| `pinned` | Package-recorded commit/snapshot identity | Explain that exact basis; missing bytes remain unavailable |
| `current` | Resolve the owner-selected release/branch/working-copy target at request start | Explain the captured state without silently following a branch that moves during the response |
| `selected_snapshot` retention | Explicitly approved selected file bytes plus path, digest and capture context | Evidence remains readable when the original checkout is unavailable |

For Git record repository identity, object format, full commit ID, optional release label, relative file path and relevant symbol/line range. For uncommitted material capture selected file bytes or a reconstructible base plus full patch and metadata. A digest alone cannot reconstruct content. Multi-file working-copy reads are not an atomic whole-repository snapshot unless a verified capture mechanism provides that guarantee.

Open approved content safely, capture the actual selected bytes incrementally, calculate their digest and issue a daemon-controlled read_id bound to target_set_id, exact repository/revision or snapshot identity, location and bytes_sha256. This identifies the captured bytes; a source-view read alone does not prove that a provider received them. When an adapter actually supplies those bytes to a provider, record an attempt-linked input observation with input_origin daemon_supplied. A native tool read may use input_origin native_observed only when supported telemetry identifies the actual bytes, exact revision/snapshot and relevant attempt; a reported path, HEAD value or tool-success label alone is insufficient. These observations record delivery/read evidence, not what the model understood. The [schema registry](schema-registry.md) defines the citation union and provider-attempt input observations.

The [shared registry](schema-registry.md#source-and-assessment-views) owns the observed_input/posthoc_verified citation union. Actual input observation requires evidence of the exact bytes delivered in the producing attempt. Direct adapter-supplied excerpts and supported source-tool responses can establish this; exact native output may be correlated with a separately captured revision/snapshot when the correlation is proven. Merely making a captured directory available to the agent does not prove that every file entered its input, and telemetry need not contain a revision field if another verified record establishes that identity. If the actual input cannot be observed, use a later exact-target check or leave the claim unverified, without disabling the owner's normal tools.

## Checking imported evidence

An owner can run Check source evidence for an imported evidence item through the [HTTP verification operation](http-api.md#imported-evidence-verification). It reads the immutable declared target and location under remembered source authority, not today's HEAD, and records a separate verification result. GET evidence/history composes that result without starting another read or AI call. Raw import and viewing never wait for this action; a failed check does not discard the directory or rewrite authored metadata.

The registry defines captured, matched, mismatch and unavailable outcomes. Expected digest/excerpt is optional. With no expectation, captured means only that the exact target was located and its bytes captured; it is not a comparison with unseen author output. With supplied expectations, compare the exact bounded bytes under the HTTP rules, without whitespace or Unicode normalization. Mismatch and unavailable do not qualify as service-verified citations. The author must not invent a quote or read_id to satisfy an admission rule.

Acquire permitted existing/temporary object pins before checking, complete the check against those same bytes before cleanup, then persist checked_at, the applicable successful verified_at and allowed record/object references. No service can reconstruct already removed unretained bytes from a digest; report unavailable instead. Saved verification is not automatic permanent source retention and never backdates provider-input observation. Semantic support and actual assessment coverage remain independent. Optional package_evidence_ref links only an exact matching immutable evidence item.

## Captured-byte lifetime

Validate, render or submit the same captured bytes rather than verifying one path and reopening a potentially changed file. Retained content-object reads verify their expected digest before use. A large object may be streamed into pinned verified capture storage or checked against recorded chunk digests before release, without keeping the entire file in RAM. Current-source target capture remains at request acceptance; a later native read of a moved checkout cannot inherit that earlier target label without byte/revision evidence. Exact revision, byte digest, observation time and current authorization travel together; a filename or HEAD stamp is not a snapshot of uncommitted bytes.

Current-source reads at a newer revision may have different paths or lines from the original package. Save them in the captured-read context of the question/job; never rewrite the old generated version metadata or present its historical evidence as if it described the new revision.

Ephemeral request capture is not automatic durable source retention. Execution-temporary or retained bytes may receive active-job pins only when that lifetime is authorized by source policy; a pin or `reference_only` digest cannot reconstruct absent bytes. If a queued/resumed job cannot access its captured revision, it pauses or fails honestly rather than silently choosing current source. Apply approved cleanup after use while preserving required nonsecret audit/recovery identity. Persistent source bytes require `selected_snapshot` policy or another explicit source-retention decision.

## Selected content and actual processing limits

The daemon's source reader resolves paths within the selected canonical roots and checks the actual opened target. Report traversal outside the selected root, unreadable content and unsupported filesystem special objects with typed results. Do not reject ordinary files because they are large, binary or have an unfamiliar extension. Read selected ranges or stream the complete object as required, retaining exact capture identity. Use those captured bytes for digesting and evidence so a later reopen cannot silently change the answer's source. This service check does not claim to confine an external agent's OS access.

A provider may support only some media types or a finite context/attachment size. Expose that actual capability, choose explicit relevant ranges or supported derived representations and record what was supplied. Unread or omitted portions are not verified evidence. A provider limitation does not reject import, remove an attachment, truncate stored source or prevent the viewer from opening the available result. Report unavailable processing or let the owner choose another provider/workflow when the request cannot be fulfilled.

Haeryong's managed source collection does not deliberately include its live profile/bootstrap material or provider authentication stores as ordinary evidence. The selected collection/disclosure inventory stays visible and may be adjusted through remembered source choices; it does not require approval of every line or read. File-name heuristics cannot establish whether arbitrary source contains a secret. These source-disclosure rules do not silently filter files from an explicitly selected result directory. Submodules and linked repositories keep distinct identity and must fall within the selected roots.

Automatically recheck current saved source/disclosure policy before new daemon reads, provider submissions and result application. This is a policy lookup, not an interactive approval request. A later scope expansion does not silently broaden a captured job. Already submitted material cannot be recalled from a provider; request cancellation where supported and record the limitation. Haeryong does not autonomously modify source repositories.

## Remote source behavior

The daemon can use only an approved host-side checkout, retained snapshot or explicitly implemented connector. A phone browser has no implicit access to a laptop repository. If the daemon is remote and the laptop is offline, saved explanations remain readable and available retained evidence may still answer pinned questions. A current-source request without the selected current target returns honest unavailable/denied evidence and `unable_to_verify` assessment where applicable; it does not silently substitute an older snapshot as current. Missing requested pinned/current evidence cannot become a verified empty target set or an unlabelled explanation-only answer.

Automatic Git fetch/pull, cross-machine source synchronization and provider-token forwarding are outside the baseline. The selected source identity and the actual bytes origin are visible whenever evidence is used.

## Retention and deletion

Selected source snapshots are content objects governed by source/privacy policy and package/version references. An original payload file may also support a selected_snapshot record without becoming a mutable file. Plain export preserves original payload; separately retained additional snapshots remain opt-in. Restricting source availability changes the authorized evidence view, not published metadata or payload bytes. An explicit source-redaction/purge plan must identify every affected payload/version, record and active pin; it may make the affected complete version unavailable or include its document in an explicit existing lifecycle purge, but cannot silently remove one file and claim the original payload digest still describes a complete export. Shared references/pins prevent physical GC until their permitted lifetime or explicit coordinated cleanup ends. No new source-policy bypass is created merely because bytes also belong to payload. Ordinary diagnostic retention cannot remove active evidence. Repository deletion is never a side effect of Haeryong lifecycle operations. Backups and previously submitted provider data may retain historical copies under separate policies.

An application-managed restore reconciles source/disclosure selections once in its recovery flow so an older backup does not silently revive revoked or expanded access. After the owner confirms the relevant restore impact, ordinary use and restart reuse those selections. See [backup and recovery](../operations/backup-recovery.md).

## Focused source evidence

[The bounded provider/source experiment](../testing/provider-source-spike.md) records G-AI `pass` against the entire [TASK-001-05 checklist](../tasks/EPIC-001/TASK-001-05.md#observable-completion-and-tests). The following source-reader cases guide feature/regression evidence, not an additional early closing list. Check selected text and binary files, a large streamed capture, an out-of-scope service read, a moved branch, missing retained/reference-only bytes, an unavailable remote checkout and remembered source selection. Check a provider's actual unsupported format/context outcome without dropping the stored file or claiming complete evidence coverage. Demonstrate exact captured revisions, same-byte digest/citation handling, current-policy rechecks and no Haeryong-initiated source modifications. Include daemon-supplied inputs, a genuinely observed native read when supported, and an unobserved native read followed by post-hoc target verification; the last case must not acquire an observed_input citation or alter original provenance. Record the source backend and provider's actual native permission/configuration limits rather than claiming a universal worker sandbox.

The minimal EPIC-004 reader also verifies imported evidence without an AI call: exercise captured without expectations, matched with exact expectations, mismatch, unavailable and a later policy restriction. Check immutable author declarations and persisted history separately. It cannot close the later real-provider integration condition by itself. Normal use must not interrupt the owner with a new approval for each unchanged source read.

## Implemented bounded reader (TASK-004-01)

This additive implementation uses the system Git object reader with argument
arrays, no checkout/filter/textconv/hooks and no fetch/pull. It resolves the
approved symlink-resolved root, accepts ordinary, bare, linked-worktree and
submodule Git roots, isolates inherited GIT_* settings and prevents parent
repository discovery. Verification reads the authored exact revision and
inclusive line bounds (terminators preserved); observation resolves each mapped
HEAD once, proves every declared base and compares raw NUL-delimited paths and
blob identities. Missing checkout/current/base objects remain missing_source;
actual Git/I/O failure settles its typed failure rather than an empty capture.

Reservation pins expectation/retained objects; detached reads run outside the
single writer, then current session/project/source/config authority is rechecked
before atomic result/receipt/outbox commit. Policy change settles 412. Recovery
reconstructs a committed result from its rows or fails an interrupted intent;
contradictory receipt/history blocks recovery rather than guessing success.
Current GET views perform no new Git read. Retained-byte readability is checked
independently; not_read_by_view is an unperformed read, not proof of missing bytes.

Direct changes produce target-bound structural reassessment triggers at exact
page-local anchors. Indirect changes disclose unexamined_dependency_scope as
candidate impact only. No dependency completeness, semantic verdict, provider
input observation or personal Review follows from these post-hoc checks.
See [source observation evidence](../testing/source-observation.md) for measured
boundaries; the dated source-access decision remains historical evidence.

## Managed capture implementation (TASK-006-02)

The additive migration-16 implementation extends the existing source service;
it does not enable the production provider, generation or saved-Q&A capabilities.
The [managed-source record](../testing/managed-sources.md) separates actual local
source/permission evidence from the later subscription adapter's obligations.

An explicit selected-source request captures a full Git revision once or streams
selected working-copy/file bytes into immutable storage. Each selected path has
a durable pending/result row, including missing/denied/unread units. An incomplete
capture publishes no target set and returns complete false with null target/time;
its original selection and per-unit reasons remain inspectable. Metadata views,
receipt replay and captured-byte diff perform no Git, filesystem or AI read.
Captured read and diff inventories use bounded keyset pages. Branch movement
cannot retarget a read after the request has resolved its full revision. Files
are opened beneath the approved root and checked against the actual regular-file
identity; binary, hidden, large and unfamiliar ordinary files remain valid.

The service rechecks current source, project, session and captured configuration
before each further read and before publication. The provider coordinator reuses
that current-policy guard before dispatch and result application. An explicit
provider requires a managed capture binding and inclusion in each source's
provider_ids; an empty list denies disclosure. Legacy foundation/post-hoc
hash-only records cannot enter that managed path. Read-only source capture does
not infer provider disclosure from the project's remembered default provider.
No read prompts are added for an unchanged approved source. Normal Sources
settings plan/confirm/commit now includes source_mode_default and nullable
provider_id, independently of per-source disclosure lists.

Capture and trusted job admission can commit in one writer transaction:
CaptureAndAdmit publishes the exact completed capture, creates the authorized
queued job and acquires job_inputs pins before releasing temporary capture
ownership. A selected_snapshot reference already adopted from that same verified
capture is also an eligible input owner. Readiness metadata alone does not grant
reference-only attachment. Future admission owners must authenticate the semantic
work and choose its provider/scope; a public job ID or digest is not authority.
Execution input ownership survives queued work and uncertain effects and ends
only at the existing terminal/all-effects-definite boundary. Original hashes,
observations and conflict history remain after eligible physical GC.

Pinned retained reuse rechecks the original capture's project before any verified
byte consumer. The same logical source ID/path in another project cannot borrow
its provider choice. Scoped foreign and missing read IDs receive the same
nondisclosing refusal without foreign identity, digests or body reads.

Standalone/project selected snapshots are independent explicitly retained source
owners. A snapshot captured with version_id is version-context ownership: the
existing purge impact includes it, and bounded purge detaches that reference only
after in-flight captures/jobs/uncertain effects settle. Current version authority
fences reads and provider use after Trash/Purge. Temporary readers and other
snapshot/payload/result/backup owners continue protecting the same bytes; a
protected purge reports remaining ownership rather than claiming deletion.
Standalone source snapshots stay readable within their current approved project
scope and are not silently deleted by another document's purge. No general
source-redaction UI is introduced.

A standalone reference_only capture retains identity, not permanent bytes. An
explicit read can reread the same approved exact Git/file target and verify its
recorded digest before supplying it; changed or absent bytes remain unavailable.
It cannot reuse an incidental same-hash object as retention. Selected snapshots
can be read offline after digest verification under current authority. Source
reads return inert octet-stream attachments with nosniff; collected HTML/JS is
not executed as a trusted-host document.

Full backup projects reference-only source owners as uncopied/unavailable while
retaining exact selection, attempt observations and uncertain-effect metadata.
It omits only those source-specific temporary input pins, preserving independent
owners of the same hash and the unchanged coherent required-object checks.
Explicit retained-source export includes only additional selected snapshots
bound to the selected version; ordinary payload remains complete unconditionally.
Restore cannot manufacture bytes from a digest or revive source policy before
its normal recovery review. Input/orphan inventory preserves retention and source
version association for these guards even when an older snapshot lacks the job.

Known live profile/bootstrap and configured provider-authentication store paths
are excluded by explicit host-owned locations, including opened-file aliases;
this is not a filename secret detector. The collector neither edits nor fetches
source. External owner agents retain their normal host tools/environment. The
future production adapter must record its actual working directory, permission
mode and inherited capabilities. Current deterministic supplied-byte observations
prove only their labelled local boundary; capture, a native path/HEAD report and
a later post-hoc check are not original producing-attempt input observations.

The production managed-file factory excludes the live profile directory, its
known profile.json bootstrap file, configured Codex auth.json and the default
CLI client-store directory. Main also supplies the selected profile's concrete
credential.json location. Existing ancestor identities and known-file identities
deny symlink, case-spelling and hard-link aliases without scanning stores or
classifying arbitrary secret copies. Git-object reads apply the same location
preflight before opening the blob. These exclusions do not constrain an external
agent's independent filesystem tools or create an OS read jail.

Accepted capture work keeps its existing WithoutCancel acknowledgement-loss
semantics. There is no implicit aggregate duration quota for progressing source
collection. An explicitly configured CheckBound remains a hang guard. Current
authority failures settle with the existing typed status/reason; an already
settled failed operation is not automatically retryable under the same identity.
Capture completion and replay do not append source.changed: captured source
bytes are distinct from a fresh owner source observation.

### Managed capture lifetime and recovery clarification

Accepted captures survive caller cancellation while daemon lifetime cancellation
remains authoritative. Verified stop cancels that lifetime before HTTP drain;
controlled restart uses the same owning cancellation. Cooperative reads settle
with the canonical nonretryable interrupted_before_commit envelope and release
only their capture ownership before storage closes. An explicit CheckBound is an
internal caller-selected bound, not an implicit production duration quota. A
blocked OS regular-file syscall is not forcibly interrupted by this contract.

The daemon also excludes the canonical default profiles root resolved by the
existing service layout, including other profiles and directory aliases. It
neither enumerates profiles nor discovers other custom data directories. Known
selected custom paths remain excluded; arbitrary copied secrets and unknown
custom profiles are not classified by filename or content.

Exclusive startup settles interrupted captures with the same complete error
envelope, including a request_id generated once for the stored response. Replay
returns that exact persisted response without re-reading source.

## TASK-006-03 provider input boundary

The production adapter reads the exact already captured objects through verified
contentstore reads and execution pins, under current source/project/provider
policy before further chunks. It never reopens a moving source path to reinterpret
an old capture. Canonical identity order matches the actual transmitted text
items. Complete native acceptance precedes an attempt-linked daemon_supplied
observation; capture, spooling, local exit and model catalog are insufficient.

The initial text adapter explicitly refuses invalid UTF-8 before dispatch. It
does not omit the file or reject/modify its stored source or result-directory
payload. Provider context/rate/session limits are affected-operation outcomes,
not storage size/count/depth limits. Exact native-read telemetry is unsupported;
ambient/native reads remain unobserved. Reasoning is neither result material nor
retained evidence. Current executing-intent guards apply to every reconciliation
page and output consumer; hidden/status-only carriers perform no body read.
