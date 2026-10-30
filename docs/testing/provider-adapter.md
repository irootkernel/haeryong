# Production provider transport

TASK-006-03 implements one personal host-local Codex transport on the existing
attempt/source foundations. SQLite revision 17 adds typed per-attempt audit;
HTTP, CLI and the shared browser/native Settings/AI screen expose actual native
status. This record describes the bounded implementation and its executed
feature checks. Root-owned common verification, independent technical review,
final assessment and delegated member acceptance remain separate requirements;
no whole-Epic or public generation/Q&A passage is claimed here.

## Delivered boundary

`internal/adapters/ai/codex` pins Codex 0.164.0 Darwin ARM64 and its release SHA.
The actual stable native schemas were regenerated from that installed binary.
Local daemon startup can select the existing executable with an absolute
`--provider-binary`; PATH discovery refuses an unverified newer release. No
installation, global environment/configuration change, login/logout, API key,
account switch or billing fallback is performed.

Preflight confirms the native ChatGPT/OpenAI route, effective read-only mode,
network-disabled sandbox, approvalPolicy=never and selected CWD. It inherits the
normal model/effort and owner instruction/plugin environment and records actual
returned values/counts. Ordinary effective default URLs are not mistaken for
custom overrides. Root OpenAI, selected-provider and ChatGPT endpoint overrides
are checked without recording credentials/raw configuration. Catalog availability
is separate from actual request access. Nullable ordinary usage remains unknown;
false alone does not establish exhausted allowance. Native rate/session/context
limits are distinct from actual usage-limit exhaustion.

The production dispatcher injects this driver into its existing one-phase
ProviderCoordinator. There is no second queue, receipt framework or request
ledger. Private invocation is trusted admission state, omitted from public job
views and both stored/replayed cancel/resume receipt bodies. Semantic identity
is derived from actual intent/options and exact source records. Canonical input
order is shared by identity, disclosure and actual frame; local bookkeeping IDs
and timestamps are excluded. The input/output consumer rechecks current runtime,
executing intent and saved/current source policy through each page/chunk.

Exact captured UTF-8 text is streamed through private spools and verified object
reads. Unsupported text encoding fails before dispatch without dropping the
source file or limiting directory storage. The before-send writer fence is
one-use. Complete frame write plus accepted provider turn ID precede durable
input observation; capture/spooling alone proves none. Only explicit public
final_answer items from a successful completed turn become a saved result.
Partial/commentary/reasoning/unknown-phase/EOF are not completion. Already
completed but unavailable output is result_unavailable, not backend failure.

Cancellation rechecks a request racing accepted-ID registration, targets only
the owned exact thread/turn, and bounds local waiting after an unsuccessful
interrupt too. Interrupt ACK, local EOF and process exit do not prove backend
cessation. TERM/KILL outcomes, same-user zombie/absent group metadata, direct
leader wait and reader drain establish only owned local cleanup. Signal/wait/
observer uncertainty stays explicit. Reconciliation looks up exact persisted
version/hash/thread/turn under a current finite intent; status-only pages use
notLoaded and never fetch source/output. A verified saved continuation never
falls through to Execute.

## Actual evidence and qualifications

| Checks | Observed boundary |
| --- | --- |
| C03 adapter cases (13) | Actual owned stdio child processes with fragmented frames, ACK/EOF, explicit final versus unknown phase, exact cancellation, cancellation-registration race, auth/usage controls, distinct native limits, current body guards, two-input canonical order and unsupported UTF-8 |
| C03 cleanup controls | Actual TERM-ignoring leader/descendant group drained by KILL; controlled KILL denial leaves bounded cleanup uncertainty and is explicitly cleaned by the test owner |
| C03R / PA12 (4) | Revision 16→17 preserves old attempt/input facts without invented audit; strict absent/altered DDL refusal; immutable accepted identity; real inventory/orphan retention; atomic earlier-schema migration/fault recovery |
| SCAP04 / C03COMPOSE | Exact capture-to-job pins; real SQLite plus production driver receiving transport EOF, visible paused uncertainty, one dispatch, exact-ID metadata reconciliation and no resend |
| C03HTTP / C03CLI / C03PROJECTION | Owner status/HEAD, provider-independent stored-file delivery and explicit Review, local-only executable selection, coherent public ETags and private-field-free stored/replayed receipts |
| Native facade | Closed empty shape, malformed/unknown payload refused before HTTP and operation-name parity |
| Chrome provider-only | Actual compiled daemon/UI, keyboard refresh, explicit auth/quota/unknown-usage controls, source-free Reader and Review, disabled future question action, 390 px hasTouch tap/reload and desktop/touch error observation |
| Wails providers-only | Actual compiled App/typed Go-held facade, native ready status on Codex 0.164.0, three acknowledged named reads including UI refresh, no owner token in JS, current guard and normal exit; trusted-host DOM action, not OS keyboard/touch evidence |

Two separately authorized pre-correction C03LIVE executions used the production factory, real
verified capture/object storage, trusted CaptureAndAdmit and normal coordinator/
ledger. Each executed one turn/start and returned inspectable source-based public
output with accepted native IDs, a durable daemon_supplied observation and saved
output hash. The historical success supplied a 1,311-byte retained snapshot of
the Go provider interface at that time; it differs from the current working tree.
A separate zero-inference canonical contentstore.VerifiedRead checked those exact
historical retained bytes. The later E01 execution used unchanged pre-correction code
and a 1,378-byte snapshot identical to that pre-correction provider interface; its retained
input/output objects and durable audit were separately read back. Full source
and index inventories matched before/after, and the historical proof was unchanged.
Runtime IDs/paths, credentials, source/output bodies and hidden reasoning are not
promoted into this tracked record.

The first live fixture failed before its send fence because the effective normal
ChatGPT backend URL was initially classified as a custom override. It remains a
failed no-dispatch attempt. The corrected historical request and later E01 request
are two separate successful actual requests; neither is a retry of uncertain work.
The historical proof's backend_state contains local result_saved, not native
TurnStatus. The pre-correction E01 audit separately records backend_state=completed and
normalized_outcome=result_saved, with local_transport_closed=true and
cleanup_state=drained (actual cleanup_reason=signal_error_drained). Its single
selected Ginkgo case passed once with race, fail-on-empty and fail-on-pending.
The saved source-based output passed the then-current completed/final_answer
consumer. This is runtime-path acceptance evidence; no separately retained raw
native phase or private history is claimed. Cleanup, ordering and decoder controls
made no model request; E01 was explicitly authorized to verify current output
acceptance. Both fixtures prove the factory/coordinator/trusted-admission seam;
public question admission, saved-Q&A resources and an entire daemon inference
workflow were not exercised.

Auth expiry/quota exhaustion are controlled native failures and HTTP/UI states;
the real account was not deliberately expired or exhausted. Native-observed
source telemetry is unsupported; no native path/HEAD report or post-hoc check is
promoted into an original input observation. Browser touch uses emulation, not
a physical private phone. Native status evidence is the current local Wails host;
additional platforms, hosted/commercial eligibility and vendor production support
are unverified.

Original negative executions remain failed: an overbroad focus selected unrelated
SC03 calibration and timed out; an anchored full-hierarchy focus selected zero
cases; two new audit assertions initially misdecoded heterogeneous inventory
JSONL; browser console observations initially lacked the exact supplied-metadata
query/response classification; native instrumentation initially omitted the new
named status operation. A later strict payload negative found a real new native
boundary defect (unknown fields ignored), corrected only for providerStatus with
the existing closed decoder. These test/driver failures and the actual product
correction have separate scope; none is relabelled as an original pass. A native
final campaign stopped at its build-input stability guard before App launch; its
negative is retained separately from the later stable-input campaign.

For the metadata-free browser fixture, exact GET metadata?kind=supplied returning 404 with
NOT_FOUND/metadata_not_supplied is the documented negative response. The driver
also verifies null version metadata URL/hash and visible honest absent UI before
classifying only that response as expected; all other desktop/touch errors fail.
The Reader/API contract was not changed to suppress console errors.

## Reproduction

Ordinary canonical tests are offline. Run the required common contract through
Gaori as selected by TESTING.md; a focused suite alone is not the complete gate.
The pre-correction final focused execution selected 22 Ginkgo It cases: 13 adapter, four SQLite,
two source composition, one HTTP and two CLI/projection. Each passed in one
attempt with race, fail-on-empty and fail-on-pending. The two native facade Go
tests passed with race; go vet ./... also passed. The compiled frontend
type/build checks passed before the final driver-only changes.
The actual fixture requires the existing haeryong_provider_live tag, exact
C03LIVE focus, an explicit installed pin and a private physical
HAERYONG_PROVIDER_LIVE_EVIDENCE directory. C03PROBE/C03ROUTE and C03RETAIN are
separate zero-inference diagnostics/retained-object verification. Running a live
fixture remains a separately scoped provider operation, never a common-test side
effect. The earlier test-only provider bridge remains historical foundation
evidence, not the production adapter.

```sh
haeryong daemon run --provider-binary /absolute/pinned/codex --profile personal
haeryong providers status --profile personal
python3 -B frontend/check_build.py
python3 -B internal/adapters/http/auth/browser/check_reader.py --provider-only \
  --provider-binary /absolute/pinned/codex --scratch-root /private/task/root \
  --browser-cli /absolute/playwright-cli --receipt /private/task/root/browser.json
python3 -B spikes/viewer/check_reader_native.py --stages providers \
  --provider-binary /absolute/pinned/codex --scratch-root /private/task/root \
  --receipt /private/task/root/native.json
```

Migration 17 is additive and preserves immutable earlier migration checksums.
Use the existing verified pre-migration recovery set/matching binary for rollback;
an older binary must not operate a newer schema. Protected restore retains newer
attempt audit/uncertainty through the existing inventory/orphan mechanism and
current policy review. No missing source/result bytes or native identity are
invented by migration or restore.

TASK-006-04 retains generation skill/publication; TASK-006-05 retains public
question/context admission, saved answers/citations and their reader flows;
TASK-006-06 retains assembled client-exit/restart/cancel/corpus qualification.
M07's schedule/coalescing/budget obligations retain the ordered EPIC-009 owners,
including TASK-009-02/03. C04,
M06/M19, X12, shared done and the Epic exit remain full compound requirements;
these bounded contributions do not close their later or unexecuted parts.
No new directory AGENTS guide is created in the currently unadopted area.


## R2 correction applicability

Work4 preserves accepted IDs and known completion through transient full-history,
context, spool or object-persistence failure using the existing recoverable
attempt state and bounded result_retrieval_pending reason. Explicit current-owner
lookup and saved continuation do not call Execute. Definitive unsupported output
and actual authority refusal remain unavailable; storage/audit/cleanup failures
stay separate and visible. A buffered matching ACK is consumed only after a full
frame write. Every post-start exit joins cleanup without inventing incomplete
audit identity. The denied-KILL test keeps its exact error/reason with a 20-second
hang guard. Settings returns to canonical General, with explicit AI and Connect
navigation. Current verification and historical live applicability are recorded
in [provider corrections](provider-corrections.md).

## R1 correction applicability

The two real successes and earlier browser/native checks above remain historical
observations of their executed bytes. They do not prove the corrected exact-CWD,
full-history recovery, audit settlement or capability-disclosure path. In
particular, R1 established that the live Summary view can omit earlier final
messages and history interrupted does not prove cessation. Current correction
semantics, the separately executed new real/browser/native feature observations
and the qualified late Stop refinement are recorded in
[provider corrections](provider-corrections.md). The first complete
common round failed two stale schema-16 literal assertions; that failure and R1
are retained rather than relabelled as passes. The subsequent common2 passed
1,430 selected checks across 15 child stages for
that prior candidate. Independent R2 completed with six current correctness
findings requiring correction. Neither result passes the corrected Work4 bytes.
R3 subsequently completed REQUEST_CHANGES with two reported-Low findings that
Root classified as Valid/effective Medium: controlled restart after a handled
accounting diagnostic and internal setup-timeout labelling. Their bounded Work5
corrections and offline controls are recorded in provider-corrections.md.
Common3 subsequently passed 1,454 checks across 15 stages on Work5, including
all 190 focused cases. R4 completed as ordinal4; Root classified its two Low
source findings as mandatory effective-Medium local corrections. The local6
producer-fact and late-stage proof updates postdate that review and common pass.
The same-code full common4 subsequently passed 1,461 checks across 15 stages,
including all 197 feature cases once. The later three-file factual documentation
projection changes no Go/UI/assets/tests; full4 covers the identical code before
that doc overlay, with docs-integrity/diff verified separately. The final independent
assessment subsequently passed all 24 current criteria and four native Goal
criteria, with zero mandatory unmet/unverified rows; the native Task Goal is
achieved. Final independent assessment and delegated member acceptance establish
this bounded Task03 delivery. The normal completion commit and native
finalization follow under the same authority; this record claims no commit or
Master overall Epic acceptance. All original review authority is consumed; no
further provider review is implied. These feature observations do not claim whole-Epic
passage.

To regenerate the stable pinned native schemas without experimental fields, use
an existing approved executable and a fresh private physical output directory:

```sh
schema_scratch=$(mktemp -d /tmp/haeryong-codex-schema.XXXXXX)
schema_physical=$(cd "$schema_scratch" && pwd -P)
/absolute/pinned/codex app-server generate-json-schema --out "$schema_physical"
```

This is schema generation, not authentication, installation or provider inference.
Its exact binary/version/hash and output applicability must still be verified.
