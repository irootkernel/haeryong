# Backup, restore, migration and crash recovery

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
