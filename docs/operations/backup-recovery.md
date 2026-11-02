# Backup, restore, migration and crash recovery

TASK-006-01 extends protected recovery with provider-attempt orphan retention.
Before replacement, the destination's external attempt identities, outcomes and
input/result references enter the protected older database. Missing bytes stay
unavailable. Repeated restore retains newer definite evidence or an unresolved
conflict. Global configuration review does not resolve uncertain provider effects
or grant a second submission. After review, explicit reconcile-only work may
clear its provider job's scheduling hold for supported status/output lookup;
it cannot read newly forbidden source or apply output under old authority.
Hidden-target reconciliation is status-only; verified remote completion can
settle external uncertainty with local result_unavailable accounting without
fetching a forbidden body. Orphan input references retain matching available
recovery objects independently of older temporary pins. A requested-object
availability callback checks only orphan inputs/results in freshly rebuilt,
verified staging, without a whole-backup in-memory index. A ready metadata row
may be unowned and have no object file in that backup. Missing input/result
bytes remain recorded but are not pinned as though they had been copied. Intentional linked new
work must name its complete predecessor set through trusted owner admission;
fresh IDs and ParentID alone do not bypass the restored uncertainty.
See [provider-attempt verification](../testing/provider-attempts.md).

Both incoming and retained conflict chains conserve their complete observations
through canonical duplicate-free replay, including available input/result owners.
Explicit never-dispatched outcomes can settle older prepared intents without
settling actual uncertainty. A terminal job whose pending status intent becomes
definite during restore may explicitly release its hold after global review and
drain that existing work locally; terminal result/state/generation remain intact.

## Backup unit

A usable backup includes a consistent SQLite snapshot, every immutable object referenced by it, complete result-directory inventories and file bytes, required drafts/notes/review history/rejections/lifecycle state and their saved context objects, configuration snapshot, schema/toolchain/profile metadata and a digest manifest. The default is a coherent set of private local files with checksums. `reference_only` source remains a reference and is reported as uncopied. Do not include raw provider credentials, native bootstrap secrets, owner/agent bearer tokens or content-delivery credentials. Restored credential records cannot be used as authority.

Keep default backup files in an owner-selected private local location with appropriate filesystem permissions. Checksums detect accidental corruption; they do not authenticate a backup replaced by another party. A verified coherent local set is a supported restore input without a mandatory encrypted container, manifest signature or recovery-key service. A scoped export remains distinct from a full backup because it may omit library state. A result-version export reproduces its complete selected directory, including all HTML pages, assets, unknown attachments, exact relative file names and represented directories/links under the [package contract](../contracts/package.md). Haeryong metadata is optional separate output and must not overwrite a same-named original file. Directory and ZIP output use the same complete content set; no flattening, silent exclusions or arbitrary output byte/file-count ceiling is allowed. Report an actual destination filesystem/path incompatibility before claiming a faithful complete export.

When exporting or sharing, the owner may choose encryption using an established supported format. Show whether the copy is encrypted and where it will be written; do not silently substitute an unencrypted copy if encryption fails. For that optional format, document key/passphrase handling and missing-key recovery limits and test its restore path. Do not invent custom cryptography or store the only recovery key inside the archive. Encryption/key-format selection is not a gate for the default local backup.

## Coherence protocol proposal

1. Create a backup job and acquire a backup coordination pin that prevents GC of currently relevant immutable objects.
2. Produce a consistent DB snapshot through the driver's verified supported backup mechanism. Extract its exact referenced object set from the snapshot, not from a later live query.
3. Maintain pins for that set while streaming immutable objects and checksums. Enumerate large inventories incrementally; internal batch sizes do not limit the backup's total files or bytes. Show progress and retain a resumable checkpoint for interrupted transfers. Newly published versions after the snapshot are not silently added to its logical state.
4. Verify every required object and size/digest; record unavailable reference-only source separately. Flush required backup files and directory metadata, finalize the files/archive and manifest as one complete set using the chosen storage protocol, then release live pins. A flush, checksum or selected-encryption failure cannot produce a successful complete-set record.
5. If interrupted, keep the set incomplete and resumable/cleanable. Never list it as a valid backup or expire the last complete set because a partial replacement exists.

A checkpoint and copying one live DB file is not assumed to be a coherent backup. [TASK-001-02](../testing/stack-spike.md) demonstrated SQL `VACUUM INTO` snapshot consistency through `database/sql`; it did not verify a driver backup API, object pins or filesystem flush ordering. Production DB-plus-content backup and restore remain EPIC-005 evidence. Storage-level snapshots may be an alternative only with documented coordinated semantics.

## Routine backup visibility

The initial backup creation/verification path remains the local CLI. Report its actual mode as manual unless an owner-configured schedule is implemented or an external local scheduler has been explicitly installed. No daily backup, GFS policy, second destination or recurring full restore is silently enabled by this contract.

The authenticated backup status shows the last complete checksum-verified set identity/time and derived age, incomplete or interrupted work and a safe failure reason. Show an absent set as not yet created, and a failed attempt separately from an older usable set. Age and last verification are useful owner context, not a backup schedule, purge prerequisite or additional gate. A checksum-verified set and a successful trial restore are different facts: retain the last actual restore-verification result/time when performed, and show it as not run when absent. Manual or optional scheduled creation uses the same coherence protocol and reports actual destination unavailability or space failure. The owner can choose an independent copy; a copy on the live volume is not represented as protection from losing that whole volume.

## Restore protocol

Require the owner's confirmed restore action, a complete checksum-verified backup and an explicit destination profile. The impact flow identifies replaced data and session invalidation; it needs no separate administrative grant. Stop competing writers, acquire the normal profile lock and validate schema/profile compatibility. CLI management uses the daemon's maintenance path or an exclusive offline recovery mode, never a concurrent SQL writer. Validate incoming paths, stage on the destination filesystem, verify objects/database consistency and switch active state through the tested complete-state protocol. Preserve the prior directory or rollback path until health checks pass. Never modify source repositories.

Create fresh `runtime_generation`, `auth_epoch` and `stream_epoch` values while preserving the logical `library_id`. Old event cursors require resynchronization rather than replay into the new stream. Invalidate restored browser/native sessions, pairing requests, workflow-agent credentials and content-delivery sessions once, including old records that would otherwise validate. The owner reconnects through local bootstrap and remembered devices can then be paired normally. Subsequent normal daemon/browser restarts retain the new sessions. Results bound to the previous runtime generation cannot apply into the restored library. Reconnect provider authentication through its supported host mechanism without importing tokens into the UI.

Pause scheduled AI, timed purge and remote exposure during recovery. Compare restored source/provider selections, disclosure settings, automatic-apply modes and destructive retention policy with known current choices. Show revoked, missing or expanded selections in the restore impact flow and let the owner confirm the policy to resume. Preserve the more restrictive known state until resolved. This is one restore reconciliation, not approval on each later source read or normal restart. Notes and review history remain ordinary restored data, not implicit changes to source or execution policy.

Before replacing a live profile, preserve an incrementally recorded nonsecret reconciliation inventory of its outstanding provider attempts and operation identities where available. After restore reconcile both restored work and these possibly newer attempts, including orphans without a row in the older backup. Do not assume a restored queued job was never dispatched after that backup was taken. Unknown dispatch/result status remains outcome-unknown; receipt absence, a fresh lease or a rotated epoch is not permission to repeat the call. Automatic external redispatch remains disabled until supported reconciliation or explicit bounded retry authority resolves the ambiguity.

## Durability and integrity

For publication, the intended ordering is staging write, file-data flush, immutable publish/rename, required directory-metadata flush, then the referencing DB transaction commit and success response. Use destination-local staging when an atomic rename is required. Record filesystem publication guarantees in EPIC-002 and coherent backup/restore durability in EPIC-005; G-STACK records only the selected spike driver's measured SQLite settings and snapshot behavior. No software procedure can promise that storage hardware never loses acknowledged writes.

On startup/recovery, detect missing or mismatched referenced objects and quarantine their use with an explicit damaged/unavailable result rather than returning unverified executable HTML or a half-published document. Before rendering or serving retained source, verify the requested object's actual bytes incrementally against its recorded digest, using immutable verified delivery storage or recorded chunk digests before release. Stream or range-read the same verified bytes without whole-file RAM buffering or a complete-directory read for each page. Object pins protect that delivery lifetime. File permissions reduce accidental modification but do not defeat a compromised OS owner who can rewrite the DB and objects.

## External rollback limits

The application-managed restore protocol rotates credentials and pauses automatic behavior because it observes the restore. A full OS/VM snapshot may roll back the database, keys, counters and attempt history together, so universal rollback detection is not claimed. The personal baseline does not add an external rollback-detection service.

Document supported OS backup/snapshot behavior and prefer coherent Haeryong backup sets for restoration. Operators restoring a profile outside the application must use the local recovery procedure before normal remote exposure or automation resumes. No restored or missing local receipt establishes that an external provider call did not occur.

## Failure matrix

| Failure point | Required recovery |
| --- | --- |
| Import staged but files not finalized | Clean/resume staging; no visible version |
| Files finalized, DB transaction not committed | Identify orphan objects; retain until safe GC; no false publication |
| File flush/finalization fails | No publication success or visible new version; keep a recoverable staging record and report actual storage failure |
| DB committed, client response lost | Receipt lookup returns same version/job without duplication |
| Referenced object is missing or has a wrong digest | Do not render/submit it; expose damaged/unavailable state and verified restore path |
| Provider dispatch recorded, response absent | Outcome unknown; reconcile or request explicit bounded retry |
| Older backup lacks a possibly newer provider attempt | Preserve/discover available orphan attempt identity; do not auto-dispatch restored queued work |
| Restored source/policy exceeds known current choices | Keep affected access/actions paused until the owner resolves the restore impact |
| Finding saved, application interrupted | Apply once under current preconditions; never double-add an edge |
| Purge begins while restore requested | Lifecycle lock/revision chooses one valid transition, no partial resurrection |
| Backup copying during concurrent purge | Snapshot object pins protect required bytes |
| Schema upgrade fails | Recover from the verified pre-migration state under the profile lock; no concurrent older writer or speculative reverse SQL |
| External storage unmounted | Fail with profile unavailable; do not initialize an empty substitute |
| Staging or backup destination lacks space | Pause/fail affected writes with progress and recovery state; preserve the last complete set, accepted records and required pins; no partial success |
| Renderer explicitly closes during context transfer or Save/Ask | Release unneeded transient channel buffers; durable staging still requires authorized abandonment and current adoption/pin checks; an accepted record/job retains its context |
| Browser crashes, a phone backgrounds or a delivery lease expires | Preserve resumable result/context staging and persisted recovery pins; reconcile authenticated upload status on return, without treating missing heartbeat as abandonment |
| Restored settings enable old ingress | Remain disabled until the owner resumes the intended private route during recovery |

## Migration

Version schema migrations and checksum scripts. Before a state-changing migration, obtain a verified recoverable DB snapshot and protect the required immutable objects under the profile lock. Define when that local recovery set can be retired; it is not a substitute for an independent disaster-recovery backup. Test upgrades from every supported prior version and failures at intermediate steps. Preserve version IDs, authored references, user history and rejections, and separate metadata expansion from destructive cleanup. A newer client cannot migrate remotely. A downgrade uses only a documented compatible schema or tested restore, never speculative SQL reversal.

## Retention and erasure limitations

Backup deletion is separately configured and applies to whole coherent sets. Do not retire the last verified complete recovery set merely because a newer partial set exists. Keep the retention/generation policy and actual restore-verification result visible. An object's retention cannot be shortened while an active backup pin requires it. A new full backup is not a prerequisite for an otherwise authorized purge or cleanup, including recovery from disk exhaustion; current plans, eligibility and pins still govern deletion. No mandatory purge delay is added.

Purge removes eligible live records and unreferenced content under the owner's confirmed plan or confirmed timed-purge policy and current eligibility. It does not promise forensic erasure: remnants may remain in database free pages/WAL/FTS storage, filesystem snapshots, SSD history, historical quotations, exports, backups or provider records. Show these limits in confirmation. If the chosen SQLite build supports cleanup or FTS5 secure-delete controls, test them with their migration/performance consequences, without treating them as a guarantee about underlying storage. Haeryong never deletes the original repository to reduce its library or backup.

Timed purge remains separately opt-in. Recheck current eligibility, clock anomalies, cancellation and batch bounds before execution. A suspected forward time jump pauses destructive work for reconciliation. An item whose trash-entry time is later than the current wall clock is not yet retention-eligible; a backward correction cannot make it expire early. Monotonic process time alone does not persist across restart and must not be represented as a universal wall-clock rollback solution.

Unadopted page-context and transfer staging follow the [upload lifecycle](../contracts/http-api.md), with no automatic staging expiry by default. Expose pending uploads and their actual staged bytes through authenticated discovery/status so the owner can resume or explicitly abandon them. Persist active/recovery ownership and checkpoints; reclaim only authorized abandoned work after current pin/adoption checks. A stopped renderer or expired delivery lease is not proof that an upload or unknown-outcome Save/Ask was abandoned. Save/Ask adoption and cleanup must serialize their decision so a saved context is not collected when its renderer closes. An explicit ownership transfer changes the responsible record/job without discarding its required bytes. Diagnostic cleanup remains separate from permanent notes, answers, receipts, review history and source-retention policy. Storage pressure does not authorize removal of pinned or active library content.

## Required evidence

Use two projects with multiple directory versions and HTML pages, shared assets, nested/Unicode paths, unknown attachments, saved page/context objects, review stamps, a rejected suggestion, a trashed referenced target, retained/reference-only source and uncertain/orphan provider attempts. Include a directory and file beyond the removed historical limits; verify streamed backup/export/restore, exact path/byte fidelity, resumed transfer and honest disk exhaustion without partial-success claims. Back up to private local files while adding/purging content; test interrupted flush/finalization, checksum mismatch and missing objects. Restore to a separate test profile and verify data, old-session rejection, one successful reconnect followed by remembered restart, policy reconciliation, truthful missing-source status and no blind external redispatch. Exercise migration failure, recovery-set retention, space-failure recovery, pending-upload discovery after restart, background/lease interruption without lost staging, explicit abandonment racing with Save/Ask and clock-anomaly handling. Verify status distinguishes no backup, a prior complete set plus a failed/incomplete replacement, checksum verification and an actual trial restore; the status itself does not count as restore evidence. Test encrypted-copy corruption and missing-key behavior only when that optional format is implemented. These are planned evidence requirements, not tests performed by this bundle.

## Manual purge cleanup and the backup handoff

TASK-005-04 implements owner-confirmed irreversible purge and resumable local GC.
Its original backup line was explicitly unavailable; TASK-005-05 now supplies real status.
No new backup or delay is required; independent copies and protected objects may
remain. Recoverable cleanup errors and retained candidates leave the lifecycle job
paused with automatic bounded retry; job inspection distinguishes removed metadata
from protected bytes. No cancelled/failed/completed job is restarted as new work.

TASK-005-05 snapshots the minimal permanent version reservations, terminal
intent/checkpoint/candidates (including retained_sets), receipt aliases/redaction comparison facts and fences
coherently. Changed restore runtime/owner authority blocks old cleanup until the
protected restore flow explicitly reconciles it. DF-02 crash read pins remain
protected pending their owning Task; DF-03 synchronous read-drain behavior is unchanged.

## Implemented host-local copy and recovery (TASK-005-05)

The current implementation uses SQLite revision 15 and version-1 backup/export
formats. Revision 14 was the TASK-005-05 delivery baseline. Backup admission
requires the exact current schema revision and migration fingerprint: a
revision-14 backup, or an incompatible earlier revision-15 layout, is refused
before replacement. A format version of 1 does not imply cross-schema restore
compatibility. TASK-006-01 verifies atomic migration of an existing revision-14
database with the existing pre-migration DB/object recovery set, and verifies
current revision-15 backup/protected restore. It does not execute or establish
an old-binary restore of revision-14 backups. Preserve older backups and their
matching software; using that software for recovery requires its applicable
verified procedure before upgrading. The current binary offers no automatic
older-backup conversion or relabelling.
Backup/export submission uses the current owner's credential plus protected profile
proof on the exact loopback daemon. The daemon retains its single writer lock;
no remote arbitrary-path endpoint exists. See [CLI](../contracts/cli.md#host-local-backup-export-and-protected-restore-task-005-05)
for the executable commands. Encryption/key flags are rejected; no encrypted format
or automatic backup schedule is implemented.

A backup starts with a durable capture barrier checked by ReserveGC. It drains an
already running collector, then releases the live store gate before a dedicated
SQLite connection executes VACUUM INTO. The snapshot's typed references and pins
supply the required object closure. Required missing/deleting object metadata fails
capture. Bounded live backup pins replace the barrier before copying; ordinary writes
and reads can progress. Objects copy in 1 MiB turns with fsync and durable DB cursors.
Resume checks the existing prefix and final complete hashes, preserving the snapshot.
Crash-leaked read pins remain conservative owners (DF-02 remains deferred); backup/job
pins are never reclaimed by age or guessed process death. DF-03 read-drain behavior
is unchanged.

The private output is `<destination>/.<set_id>.incomplete/` until publication, containing
`database.sqlite`, `objects/<sha256>`, `inventory.jsonl` and `manifest.json`.
The DB stores the authoritative per-object checkpoint. Manifest format 1 binds set,
library, producing Go toolchain, origin profile, schema/migration digest, database and
inventory digests/count, creation time and reference-only-source limitation. Credential
rows (device sessions, pairings and scoped credentials) are removed and SQLite vacuumed
before final hashing. Host profile/control/provider files are never backup inputs.
File/directory sync precedes COMPLETE.json and a destination-local rename to `<set_id>`.
The completion marker, final rename and live completion transaction serialize against
cancellation; an interrupted acknowledgement re-verifies that same final set. A partial
set cannot pass `backup verify`. Checksums do not authenticate a malicious replacement.

`backup resume SET_ID` creates a linked job after a terminal failure/cancellation; it
never clears the former job's cancellation. `backup abandon SET_ID --confirm` requires
that worker to have stopped, releases only the set's backup pins and discards its owned
incomplete directory. After restore review, abandoning a quarantined copied record releases only restored ownership and does not delete the source host's output. Keep an inaccessible destination mounted at its recorded path
before resuming. ENOSPC, missing bytes, integrity, sync and rename failures leave a
failed attempt and required ownership rather than false success. A failure during
capture can conservatively delay GC until resume or explicit abandon.

After a new usable complete set, validated `retention.backups` may expire older whole
owned sets. The default indefinite value removes none. Active verify/restore directory
locks prevent retention deletion; the new verified fallback is checked again before
each deletion. Retention errors retain bytes. A newer incomplete set never expires the
last usable set. External removal/unmount is reported as destination_unavailable.

Protected restore runs offline under the stable profile lock. First stop the daemon
normally; do not kill another writer. `restore plan` verifies input, binds destination
profile/database+WAL state and a 15-minute expiry, and shows session/policy effects.
`restore apply PLAN_ID --confirm` stages complete DB/object bytes on the destination
filesystem, preserves a nonsecret outstanding-operation inventory and the previous
storage/profile, installs an early recovery hold, recreates canonical reverse-index DDL,
rotates runtime/auth/stream epochs and replaces storage through a synced phase journal.
Retry the same plan after interruption, including admitted staging that outlasts the
preview expiry. Its matching journal, destination state and immutable input are still
checked; expiry is not a transfer-duration limit. An unexecuted expired or malformed
plan still refuses without publishing a journal or alias. Staging is rebuilt from the immutable input and
switch/health phases resume without rotating an already switched generation. A strict
startup/object health failure rolls back to the matched prior storage and profile.
An unfinished switch blocks normal startup with recoverable prior/staged state.

All restored sessions, pairings, workflow verifiers and delivery authority are invalid.
Bootstrap a new owner device and restart normally to retain that new session. Source
access, copied jobs and destructive work remain held; listener/exposure settings are
safe, maintenance manual and timed purge disabled. `restore review` shows original and
known-current preferences. Supply a JSON choices file with `expected_config_revision`,
`configuration` and `resume_cleanup_job_ids`, then use `--choices FILE --confirm` while
stopped and authenticated with the new owner credential. Existing configuration
validation/plan/commit and hold release are atomic. Normal configuration commits cannot
bypass a pending hold. Only enumerated guarded purge intents may continue; all other
old jobs remain quarantined. Unknown external outcomes never authorize redispatch.

A completed restore has one maintenance job and natural plan receipt; an optional
operation ID aliases that result. An isolated destination-library replacement records
an actual trial success only after strict DB/object startup health. Same-library
recovery and read-only checksum verification do not claim a new isolated trial.
The current trial observation lives in the restored destination, not by writing into
an unrelated source profile. Full OS/VM rollback and missing reference-only source
repositories remain outside application recovery guarantees.

Portable exports select exactly one version/document/project, freeze published version
membership, and optionally include exact Review, Revisit, reader, Notes/history/context
and reference records. Unsupported decisions production is reported unavailable. Single
plain outputs retain the original root unchanged. Multiple versions use
`versions/<version_id>/` plus an outer export-index.json; optional metadata is separate
from every original root. Directory incompatibilities fail; ZIP is written directly
from canonical inventories, preserving names even when a host filesystem cannot.
Separately retained verification expectation excerpts use their real typed ownership and require --include-retained-sources; their inclusion is not a fresh source read. Metadata and dependency states do not claim source availability or personal-state merge.
See [the implementation evidence](../testing/backup-recovery.md) for actual checks and
remaining verification limits; required Task/Epic acceptance remains separate.

### Recovery identity and owned long phases

The serial dispatcher schedules and collects one asynchronous copy turn at a time.
Its child context and actual claimed job generation cover snapshot copying, prefix
hashing, directory/ZIP formatting, final verification and retention. Independent
queued import/seal/derived/purge work continues through the same dispatcher while
those operations run outside the store gate. The capture barrier still blocks new
GC reservations until snapshot pins are installed. Cancellation fences the attempt;
stop confirmation, linked retry and abandonment wait for actual handle closure.
Daemon shutdown drains that turn before dependent/storage closure and profile unlock.
A completed job can still own retention handles until its turn is collected.

Directory durability includes newly created backup destination entries in their
parents, object entries before checkpoint acknowledgement, export payload/metadata
subdirectories, and both source and destination parents of cross-directory output
renames. Recovered publication repeats the required syncs before completion/pin
release. Export privately records the verified expected index and complete formatted-tree digests before any
publication rename; recovery compares the actual output to that frozen identity
and verifies all referenced bytes. A foreign valid export or missing expected
publication binding is refused. This digest does not replace the plan digest.

Restore journals rollback direction before moving incoming storage aside. Each
move and profile restoration can resume idempotently; ambiguous directory identities
remain maintenance-blocked. The existing protected profile write syncs the storage
rename parent. Success requires the intended library/profile/runtime/auth/stream
identity and restore hold. After health succeeds, completion-record I/O failure
resumes terminalization instead of rolling back committed success. A rolled-back
plan returns a retained failure outcome. Per-plan terminal results and alias bindings
live outside replaced storage independently of the active-switch journal: a later
restore cannot erase an earlier result, and older replay does not mutate the later
library, trial status or epochs.

Restored complete-copy rows remain historical observations. Paths and matching
checksums do not adopt those outputs for retention deletion. Only sets created by
the current library/profile/runtime are eligible; even same-profile restoration
rotates runtime and conservatively leaves earlier outputs independent. Current owned
retention still requires an exact verified fallback and an exclusive active-use lock.

With Notes selected, any original/current/retained-history anchor in frozen payload
membership selects the authorized annotation identity and its complete permitted
retained current/history/body/context closure. Outside-payload anchors retain their
actual version/page/navigation and context binding; they do not expand payload
membership. Their dependency rows use kind note_anchor and the existing dependency
states. Unrelated identities and legitimately purged/redacted history are excluded;
missing bytes required by retained selected records fail integrity verification.
Nested index/record grammar and per-family owner/document/version/resource/local-ID
bindings are checked, including Note context descriptors. Revisit flags are JSON
Booleans. Checksums neither authenticate rewritten exports nor independently prove
that a producer emitted every historical revision.

Restore operation identity is shared by natural plans, retained terminal outcomes and
optional aliases under the protected local profile. External binding consistency is
checked for every key. Natural or already-bound alias replay returns the exact old
success/failure even when a later dataset contains unrelated receipts with those IDs;
it does not open that later database or change its trial/epochs. New aliases still
require current admission. Latest-success database alias projection retains its own
identity/receipt checks.

Before new/in-progress binding, inspect all available logically owned database sides,
not only the path currently named storage: current/prior plus verified input during
staging; protected incoming and prior across switching/health/committing; saved
incoming and matched prior during rollback. Exact journal paths and library/runtime/
profile identities fence the roles. Each database's preserved owner receipt namespace
remains separate from unrelated workflow principals. A matching uncertain receipt
requires reconciliation; absence never proves non-dispatch. No arbitrary older
recovery directories are scanned. Existing private records require renewed parent
sync before acknowledgement, including retries after a visible rename whose sync
failed. Older terminal replay needs no original backup input.

If incoming health fails after switching away from a startup-refused prior database,
rollback preserves the matched prior storage/profile and records the actual failure.
The isolated-trial projection is a metadata transaction, not permission to weaken normal
startup checks or repair prior DDL online. After failure terminalization, the same plan
replays failure and a later explicit healthy protected restore remains available. Actual
trial/terminal write or sync failures remain visible, retryable maintenance errors.

Export verification first checks canonical inventory rows and streamed bytes, then
compares the exact typed payload namespace before making path/type/hash facts
available to Note, reader and selected-source claims. A directory alias, alternative
case spelling or compensated entry count cannot create a canonical inventory member.
Directory roots must actually be directories even with empty inventories. ZIP roots
and parents may be implicit, but an explicit file/link cannot occupy their directory
role. Reserved versions-layout membership follows the index descriptors at every
cardinality; it does not reserve an ordinary versions path in original payloads.

Missing or non-file selected_snapshot assets fail verification regardless of an
unavailable dependency label. Reference-only digests still do not imply retained
bytes. The verifier retains only entry identity/type/hash facts and small root-parent
listings; file contents and inventories are processed incrementally with cancellation.
Actual ZIP entries remain the authority for the existing frozen formatted-output
binding, so adding logical implied-directory facts does not change that binding's
encoding. Initial, standalone, separate-metadata and recovered verification share the
same payload proof. No new metadata-object format is inferred from this rule.

## Managed source copy projection (TASK-006-02 implementation)

Schema-16 full backup uses a source-specific projection of its private coherent
SQLite snapshot. Reference-only capture rows keep their exact identity and
selection but become unavailable/reference_only_uncopied. Only their temporary
capture, job-input, attempt-input and proven reference-only orphan-input owners
are omitted. A same-hash independently retained snapshot, payload, result,
context or other owner still requires its actual object bytes. The general
required-object integrity/fingerprint checks remain strict. Unclassified legacy
or conflicting input ownership is retained conservatively; no missing bytes are
manufactured from object readiness or a digest.

Restore preserves retention/source-version facts with provider uncertainty and
retains its normal policy-review barrier. A missing reference-only checkout after
review remains unavailable; selected snapshots can verify/read offline under
current access. Explicit retained-source export adds only selected additional
snapshots bound to the exported version. It does not expand project-only source
captures or include provider operational history. See the [managed-source record](../testing/managed-sources.md)
for actual copy, protected restore and ownership tests and their limits.

The uncopied projection changes only reference-only rows whose stored capture
availability was available. Existing missing, denied and not_read reasons remain
unchanged across protected restore and a subsequent backup. A copied independent
same-hash owner never upgrades a reference-only capture's retention claim.

### Managed capture history after source omission

The source-specific reference_only_uncopied projection marks only previously
available reference-only reads. Supported rows retain their exact captured source,
location and byte identity. Metadata views and byte-digest diffs interpret that
marker as historical capture availability, while current bytes remain unavailable
and unretained. This does not authorize source reads, restore omitted objects or
create an attempt observation. Missing, denied, unconsumed and unsupported legacy
rows retain conservative facts; an ambiguous marker alone cannot invent history.
Independent payload, selected snapshot, result and other owners keep their normal
coherent object-copy and retention requirements.

### Saved question dependencies (TASK-006-05)

Coherent backup includes question/answer records and their full owned text,
display context, explanation input and saved answer bodies. Existing typed job,
attempt and orphan input pins remain separate from source-retention ownership.
Restore preserves original producing-attempt/citation/audit facts and rotates
runtime/auth/stream epochs normally. Current owner saved reads recover after
bootstrap and applicable source policy review; an old execution epoch does not
permanently deny saved citations. Review does not redispatch paused work or
fabricate an original observation. Source bytes remain subject to current
read authority and their actual retention mode.

Scoped export accepts the explicit `records:["questions"]` selection and keeps
those records/objects outside original payload roots. It includes saved records,
not unpublished PreparedAnswer candidates, operational provider history or
credentials. Question/answer identities, anchors, body/context descriptors and
linked dependencies are verified before accepting the copy grammar. Source
bytes are not implicitly included; explicit retained-source selection retains
its existing scope/ancestry rules. Plain export stays the original result tree.
Trash and purge retain their current confirmed plans, reusable-owner protections
and resumable pin-safe deletion behavior for the new record family.
