# TASK-002-03 authenticated session evidence

**Task:** [TASK-002-03](../tasks/EPIC-002/TASK-002-03.md)\
**Implementation status:** done\
**Evidence status:** required scoped execution, independent technical review and Gemini task Judge passed; accepted by the delegated coordinator\
**Base revision:** `c55eae29c23464422069272d51f5ccc3f91abec0` (completed TASK-002-02)\
**Executed:** 2026-10-16 UTC / 2026-10-17 Asia/Seoul\
**Environment:** macOS 27.0.1 ARM64, Go 1.27.1; existing Docker Linux ARM64 Go 1.27.1 Bookworm image; modernc.org/sqlite v1.60.1 / SQLite 3.53.4; Gaori 0.1.18 CLI; actual desktop Chrome 154.0.8037.100

## Delivered boundary

The headless application listener now supplies the content-free trusted connect shell and production owner-session middleware. Explicit initialization still creates only the protected pending profile. The existing locked daemon bootstrap prepares SQLite/objects before serving the application. Protected local control and browser APIs have separate route policy on that same listener: local control requires exact loopback Host, absent browser Origin and profile-key HMAC; browser/API traffic uses configured exact Host/Origin and header bearers. Loopback reachability, cookies, absent Origin and forwarding headers grant no owner authority.

Every paired device has its own revocable session and shares one persistent owner principal. No administrator, plan-grant system, provider coordinator or production no-auth mode was added. Workflow credential issuance remains feature-owned. Current authenticated routes cover pairing/session/device management, local job read/cancel, operation reconciliation and SSE notifications. Known future catalog capabilities return FEATURE_NOT_READY, unknown routes return NOT_FOUND, and actual storage/service failure remains SERVICE_RECOVERING. Complete imports, product reader/render delivery, configuration plans and private ingress keep their ordered owners.

### Pairing, bootstrap and current authority

Pairing requests persist only polling-secret and comparison-code verifiers, origin, label, creation/expiry and state/revision. The pending secret stays in browser memory and the token is returned only by the one successful approved poll. No separate redeem route or public redeemed state was introduced. Unknown, duplicate, missing and null required control fields fail. Control protection is bounded to a 16 KiB authentication envelope, 128-codepoint label, five active pending requests, reliable-client creation rate of two/minute and two-second polling interval. These are not authored-content limits.

Approve/reject require `{code,origin}`, one lowercase canonical operation UUID and the exact pairing strong If-Match. Reject accepts pending or approved-but-unredeemed. Actual serialized races give one pending decision winner and stale 412 to the loser; redemption/rejection also cannot both consume the request. A later approved rejection uses a new key/current ETag. Matching committed responses replay before their original stale ETag after current authentication. Pre-acceptance code/state/precondition rejection rolls back both reservation and effect; no durable acceptance or definitive result is claimed. Existing accepted, saved failed/completed, committed-202 and reconciling receipts are retained under the predecessor's rules.

The protected local enrollment proof has a separate HMAC domain and binds instance, method, exact path, operation ID, If-Match and body digest. The response binds status and body. Lifecycle proofs and altered intent cannot approve a request. Verified local discovery still precedes the call. `auth pairings` reads the queue; `auth approve/reject <pairing_id>` takes `{code,origin,etag,operation_id}` on stdin. Browser clients never receive the bootstrap key, and comparison codes are absent from argv. Saved operation semantics substitute the pairing-bound code verifier for the raw code, preserving changed-code conflict without persisting the code. Actual stored auth/receipt/audit fields were scanned for generated tokens, polling secrets, codes and the profile key.

Authentication checks current library/principal/auth_epoch, revocation and configured idle/absolute expiry before private handlers and receipt lookup. The typed writer check repeats under serialized ownership and before definitive commit. Actual writer-wait revocation and precommit expiry fixtures prevent replay/application and roll back resource/revision/event/receipt together. Replay visibility uses the persisted canonical target, even when the saved body contains no target ID, and checks explicit result references as well. A now-hidden target cannot be recovered by retaining its operation ID.

### Sessions, revisions and streams

Normal daemon/browser restart retains valid device sessions and library/auth/stream identities. Expiry defaults to null. Every owner device can list/revoke devices or approve another exact request. Natural session DELETE requires no operation key or If-Match. Local bootstrap reconnects after all devices are revoked. A labelled auth-epoch mutation proves invalidation/reconnection without claiming the future full restore engine.

Current pairing/device/job list responses use `data.items` and nullable `data.next_cursor`, with no silent cap. Mutable sessions and jobs have independent durable revisions. Real session activity advancement/revocation and semantic job mutations advance their row revision; no-op activity/revocation/stop does not. Job generation remains a worker fence and is not a revision alias. Full public-representation ETags include derived session expiry/state; reading a changed clock/policy projection does not invent a stored mutation. List, single and cancel views use the same representation, and current outbox/audit references use the affected row revision.

SSE uses header authentication, decimal sequence strings and epoch:sequence IDs. Malformed, old-epoch and future cursors receive safe resync control data. Batches are refiltered per event immediately before writing; a target hidden while an earlier event is blocked yields content-free library invalidation. Passive stream checks/heartbeats do not renew idle activity. Revocation/expiry ends delivery. Writes have bounded deadlines, and shutdown cancels request contexts, rejects new handler work and drains existing handlers before storage closes/profile ownership is released. Real CLI Stop with an active stream, writer reacquisition and remembered restart were checked.

### Migration recovery

Schema 3 adds owner/session/pairing records and independent job revisions. Original migration 1/2 bytes are unchanged. Before upgrading an already-ready schema-2 DB, startup retains a coherent SQLite snapshot under the profile writer lock, checks identity, quick_check and foreign_key_check, and enumerates the snapshot's exact required object set in batches. Referenced, pinned and entry-backed immutable objects are verified and copied with streamed digests/file/directory sync into the private recovery set before its complete descriptor or migration commit. Copies retain the recovery bytes independently of later live GC. The set is retained; no automatic retirement policy was added.

Actual schema-2 referenced-byte fixtures cover object-copy/sync and migration-commit faults, retained recovery sets, normal epochs and reopening. A dangling object pin whose row is absent from enumeration now prevents both complete-descriptor publication and schema/history advancement. This is startup migration recovery; scheduled full backup, application-managed restore and disaster-recovery qualification remain EPIC-005 work.

## Actual verification and qualifications

| Required case | Actual observations and representative tests | Status |
| --- | --- | --- |
| T-002-03-01 / X03, X04 | Actual HTTP/SQLite single-use poll, approve/reject CAS, later approved rejection, origin/secret mismatch and all-revoked protected bootstrap recovery. Actual trusted-client remembered reload/new tab/browser and daemon restart, owner peer approval, memory-only mode, storage failure/loss and optional expiry. | pass on final combined source |
| T-002-03-02 | Unpaired/revoked/expired private assets, receipts, jobs and events denied. Active streams end; generated isolated frame cannot read parent DOM/storage/script, get management credentials or invoke management through messages. Native renderer/Wails is not exercised. | pass on final combined source |
| T-002-03-03 / X11 | Current session before lookup, writer-wait revocation, expiry rollback, committed matching replay before stale ETag and current canonical-target/result visibility. `TestCurrentSessionRecheckedAfterRealWriterWaitBeforeReceipt`, `TestSessionExpiryPrecommitRollbackAndCurrentReplayVisibility`. | pass on final combined source |
| Per-event visibility | `TestEventDeliveryRechecksLaterBatchTargetAfterDelayedWrite`: first actual event write barrier, real SQLite lifecycle change, later batch item becomes content-free invalidation. An isolated captured variant removing only the refilter call fails the same privacy oracle. | pass on final combined source |
| Revision/representation | `TestDurableJobSessionRevisionsAndAtomicCurrentEvents`, actual HTTP session/job validator tests: same-generation Finish/ConfirmStop, normal startup recovery, touch/no-op/revoke, derived expiry, complete list/single/cancel equality and atomic event rollback. | pass on final combined source |
| Migration recovery | `TestReadySchemaTwoRecoveryIncludesExactImmutableCopies`, `TestSchemaTwoForeignKeyDamageCannotProduceCompleteRecoveryOrMigrate`; copied immutable bytes, interrupted durable boundaries, foreign-key damage fails before complete/DDL. | pass on final combined source |
| Lifecycle | `TestBootstrapHMACBindsExactDecisionIntent`, `TestProductionBootstrapSSEStopRestartAndAllRevokedRecovery`; intent/header/body/instance substitution denied, real stdin CLI bootstrap and active-stream Stop/restart. | pass on final combined source |

The browser harness uses the actual production middleware, application core and SQLite with two same-host loopback origins to model local/remote contexts. Initial enrollment and policy/child probes are labelled test adapters, not alternative production no-auth routes. Real Chrome exercised the actual trusted client; desktop/mobile viewport captures were inspected after comparison controls were cleared. A mobile viewport is not a physical device. No private HTTPS ingress, physical second-device UX, Safari/Home Screen or complete Wails/native renderer pass is asserted. Browser storage retention outside the observed runs is not guaranteed.

## Findings and evidence freshness

Initial agent technical audit identified missing list envelopes, missing durable session/job validators and a snapshot foreign-key coherence gap; these are current Task corrections, not deferred work. Earlier per-event scope and header/parser findings were corrected with actual regression checks. All original negative runs remain local, alongside their successor receipts and exact source bindings.

The foreign-key regression first exposed a test URI interpretation mistake, then failed for the intended reason: the old source advanced schema/history to 3 and wrote a complete descriptor for a damaged prior snapshot. The unchanged regression passed after snapshot foreign-key validation. A compile-variable correction has its own retained failure. During revision integration, recursive startup mutex acquisition was identified; its two scoped checks were explicitly stopped and retained as killed/extraction-degraded, with no pass claimed. The corrected path starts its transaction under the already-owned startup lock. Final review requested a direct startup commit-failure check in addition to the existing normal recovery and ordinary job-write rollback cases. `TestStartupRecoveryCommitFailureRollsBackAndRetryAppliesOnce` injects the actual private startup transaction BeforeCommit failure, verifies closed adapter and unchanged prior running state/generation/revision/events through a direct SQL observation of the disposable fixture, then proves next reopen applies recovery once and another reopen adds no mutation. This is a test-only successor; the captured production source and final browser binary inputs remain unchanged.

Original browser harness failures covered active-stream shutdown and restricted Playwright execution-scope assumptions. Their corrected runs are preserved. The per-event negative is an intentionally modified isolated captured variant; it is not represented as a historical original production run. Predecessor storage regressions remain in the current root suite. The first full source candidate passed its then-current Go gates, but list, validator and recovery corrections superseded those source bytes. A new combined freeze was captured before fresh full macOS/Linux race, four-target build, vet and selected contract checks; all passed without further source changes. Two stopped intermediate revision checks remain killed, not normal failed test results. A fresh production browser fixture was built from the final combined source, captured before the build and rechecked after execution; the actual browser run passed. Documentation integrity and whitespace checks are recorded separately.

## Commands and actual results

Gaori used the explicit canonical repository, unchanged existing tester configuration and tagged ad-hoc argv. MCP launch binding was unverified, so the inspected CLI ran checks. Native status/summary/argv/hashes are retained locally for orchestration; passing raw logs were not opened. Historical failed/killed runs are not overwritten as passes.

| Selected check | Result |
| --- | --- |
| Final combined `go test -race -count=1 -timeout 90s ./...` on macOS and actual Linux ARM64 | pass; 76 root tests; 28.912 s / 35.970 s |
| Test-only successor `go test -race -count=1 -timeout 90s ./internal/adapters/sqlite -run '^TestStartupRecoveryCommitFailureRollsBackAndRetryAppliesOnce$'` on macOS/Linux ARM64 | pass; one new actual recovery rollback/retry case; production source unchanged |
| Final combined `go vet ./...` | pass |
| Final combined macOS/Linux ARM64/AMD64 headless builds | pass; AMD64 is cross-build only |
| Actual production browser harness on final rebuilt fixture | pass; real Chrome, 76.619 s; current source captured before build and unchanged after execution |
| `python3 -B tests/contracts/run.py --report <new-private-report>` | pass; 30 selected pure contract tests, no production runtime claim |
| Domain/application/security Go dependency inspection | final pass; no HTTP, SQL, adapter, SQLite or Wails dependency |
| Documentation manifest regeneration, docs-integrity and every new-file whitespace check | pass; derived manifest refreshed and all new files checked separately |

Linux uses the already-present inspected ARM64 image with network disabled, read-only repository/module cache and disposable executable temporary storage; no pull or installation is needed. Host scratch was selected only after mounted/writable RootKernel verification. Temporary test listeners/containers are confined to the authorised fixture scope. Runtime artifact identities and paths remain local and are not promoted durable evidence packages.

## Compatibility and handoff

Selected auth shapes/error mappings are additive. Individual If-Match on approve/reject is an owner-approved breaking obligation for clients that omitted it; no earlier production auth API shipped. The old G-AUTH spike retains its original adapters and proof. No expected_state/admin/confirmation field, extra redeem route, compulsory content metadata or content quota was added.

Task is done after required scoped execution, independent final technical review and Gemini task Judge approval. The delegated coordinator accepted the implementation; bounded completion metadata and its documentation check are part of the task-scoped commit. Epic remains in_progress, and TASK-002-04 owns the next implementation step. This is agent assessment and delegated acceptance, not a claim of owner-performed functional testing. Future feature, provider, native/private-device and EPIC-002 exit gates are not passed by this Task's foundation evidence.
