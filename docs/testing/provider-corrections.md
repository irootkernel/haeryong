# Provider transport corrections

TASK-006-03's first common round failed two stale schema-revision assertions.
R1 also identified transport/accounting, provenance, authorization and client
correctness gaps. This record preserves those negatives and the earlier real
proofs; none is relabelled as a current-candidate pass.

## Corrected behavior

The coordinator saves a complete authorized output before secondary required
audit and application. Known failed/live-interrupted native facts also commit
independently. A required audit failure remains visible and abandons only its
exact finite query campaign, including pending siblings, without completing a
linked carrier. Saved output can continue through the verified no-Execute path.
Primary writer failure cannot claim durable facts. Ordinary daemon cancellation
can account an already-complete spool under current payload guards; owner cancel
or revoked authority cannot read/save unauthorized bytes. Poll preserves service
availability; Stop retains the first handled accounting failure after collection.

No-send preparation remains prepared and recoverable with an exact local reason.
Effective config is read for the selected absolute CWD and null config refuses
dispatch. Setup/lookup/cancel and blocked native writes have bounded operation
ownership; normal model waits and progressing complete-output copies gain no
arbitrary total duration or size cap. Interrupt reservation is at most once.

Pinned history interrupted remains unresolved, regardless of request/ACK.
An interrupted cessation claim requires the matching live event. A successful
completed Summary event cannot supply a subset as complete output: recover the exact turn
with full history, include all supported final messages, or retain completed
with result_retrieval_pending while retrieval is transient, or result_unavailable
for definitively unsupported/forbidden output. An omitted turn-response
itemsView means the pinned
defaultFull; explicit null/unknown/non-full never passes. Requests select their
view explicitly. Foreign identities cannot poison output.

The pinned [thread processor](https://github.com/openai/codex/blob/rust-v0.164.0/codex-rs/app-server/src/request_processors/thread_processor.rs#L6246)
normalizes inactive unfinished history to interrupted. Its view projection is
separate from a live terminal event. The pinned [item-view projector](https://github.com/openai/codex/blob/rust-v0.164.0/codex-rs/app-server-protocol/src/protocol/turn_items_view.rs#L6)
selects the first user message and last agent message for Summary. It preserves
the complete selected message text, while other messages may be absent; it does
not truncate that text to a prefix. These are version-specific source facts,
not inferred properties of later CLI releases.

Restore preserves compatible newer lookup/core facts independently of attempt
state selection and retains genuine contradictions. Pre-send audit distinguishes
configured web-search mode from observed initialized MCP counts and unknown app
availability; missing/incomplete telemetry is unknown, without names or secrets.
Hidden response replay preserves operation-time data/ETag while current GET and
new conditional operations reflect actual later changes. Provider query keys,
owner revocation and both clients' unknown authority values fail safely.

The AI screen keeps bounded reason/authentication labels, false/null allowance,
keyboard focus, a stable polite status region and shared 44 px SPA links.
The browser driver correlates every expected supplied-metadata 404 individually
and retains unrelated errors. Its exact JavaScript SHA joins the wrapper receipt.

## R2 corrections

R2 reported six Low findings that the Coordinator adjudicated Valid/Medium;
its seventh wording preference remained Invalid. The existing 31 R1/common
rows, four prior Invalid rows and independent DF-07/DF-08 deferrals are preserved.
Work4 corrects the six current defects without a new queue, table or migration:

- A known completed turn with interrupted full-history/context/spool/Put remains
  outcome_unknown/result_retrieval_pending, with exact accepted IDs. This primary
  fact survives secondary audit failure, inconclusive lookup and compatible
  restore. Explicit full recovery plus saved continuation never Execute again.
  Definite unsupported view/phase/no final or forbidden output remains unavailable;
  genuine reliable terminal conflicts stay explicit, and writer failure cannot
  claim persistence.
- Matching buffered ACK survives simultaneous cancellation/EOF after the complete
  frame; missing/malformed/error ACK and incomplete writes never imply delivery.
- Copy/Apply cancel or current policy refusal settles ordinarily and retains any
  complete saved object/pins. Genuine storage/audit/cleanup error remains visible
  at Stop while Poll/Reader/Review continue.
- Every post-start return joins cleanup. Complete available audit facts are saved;
  early auth/setup/disclosure failures do not invent identity. No-send stays
  prepared with its primary reason and an independent cleanup diagnostic.
- The denied-KILL cleanup case preserves its error/reason and uses a 20-second
  hang guard, calibrated against the observed approximately 5.5-second pass.
- Empty Settings and /settings/general again share the existing General landing,
  with explicit AI and Connect navigation. AI also offers General and Connect;
  no new General/Sessions editor is claimed.

The Work3 real fixture below remains historical execution, not current-byte
identity. Its source/frame/pin and successful full-history/public-final path are
unchanged in meaning; Work4 changes retrieval/refusal/failure/ACK/cleanup edges
and Settings navigation. No Work4 model inference has run. Current offline checks
and changed-route browser/native observations are recorded separately; current
common/R3/final acceptance remain pending.

## Work4 verification

The current offline selection passed 136 Ginkgo It cases once with race,
fail-on-empty/pending, no dry run or programmatic focus. A separate three-case
source/migration/restore supplement passed once; their 139 distinct cases include
all 123 previously required provider cases plus 16 additions. The new controls
cover native full-RPC/spool interruption, complete-write ACK/EOF/cancel/error
boundaries, real SQLite cancellation/epoch/persistence separation, known-completed
primary facts despite selective audit failure, monotonic/conflicting restore,
and production-driver plus verified-byte-store cold reopen/recovery/continuation
without another turn/start.

Current preflight passed go vet, both whitespace checks, two named native-facade
Go cases with race, and 66 HTTP/native client/router cases. Canonical root
cmd/internal/tests formatting passed. The initial broader formatting span also
included unrelated desktop-module files and failed; that original result is
retained, and no unrelated source was reformatted. Prepared frontend asset
regeneration and a fresh verification passed, with JS syntax/Python observer
checks and no dependency installation.

The original red/failed observations remain separate: four old SQLite behaviors
failed before the corrected settlement; subsequent checks caught a real normal
fence error misclassified as accounting, obsolete permanent-failure/refusal
assertions, and an immediate reader-drain observation before asynchronous close
joined. The corrected case joins actual cleanup without dropping its error,
once-only interruption or drain assertions. A partial-ID selection also admitted
an unrelated calibration workload; final native membership uses exact ID word
boundaries. An early report consumer mishandled absent reports after a prior
suite failed; the retained native reports preserve that distinction from product
failures. None is relabelled as a pass.

The separately authorized Work4 Chrome campaign passed on the current compiled
shell: sidebar Settings and explicit General were equal, General made zero
provider requests, and keyboard/touch AI and Connect navigation, SPA/history,
refresh focus and the same polite busy/final region passed. All six controlled
states retained Reader/Review and two exact supplied-metadata404 replies each,
12 total, with six negative controls and no unexpected console/page errors.
Four actual desktop/390 px General/AI screenshots were directly inspected and
were readable without clipping. Modified-link default handling was a synthetic
browser event control; touch was emulated, not a physical-device qualification.

The Work4 providers-only Wails App passed 16 exact whitelisted rows, including
General→Connect→General→AI and AI→Connect, plus the preserved status/refresh and
no-JS-owner-token checks. Three actual providerStatus operations were observed;
the App exited normally, the daemon closed, cleanup had no errors and all 712
native inputs stayed unchanged. Input was trusted-host DOM. Private failure-row
retention was enabled; no credentials/protocol traces/reasoning were consumed.
Both campaigns kept all 1,086 actual source files, the declared old-bundle deletion
and index unchanged. They used metadata/non-inference setup only, with no model
turn, provider source disclosure, provider account/global configuration change or
runtime retry. They do not
qualify an entire native companion, physical/OS input or current model inference.

Work4's code/asset/driver closure is covered by these actual campaigns and 139
focused cases. The final documentation-only projection changes verification/ownership prose;
code, assets and driver bytes remain unchanged. Current full common3, R3, final assessment, acceptance
and commit remain pending; common2 remains a passed prior-candidate observation.

## Work3 verification and remaining gates

| Executed check | Actual boundary |
| --- | --- |
| Broader offline provider regression | 122 Ginkgo It cases passed once with race and fail-on-empty/pending: 29 adapter, 72 SQLite, two source, eight auth, three HTTP and eight CLI. This precedes the late Stop refinement below. |
| Late Stop refinement | The new real two-phase case first failed because Stop returned nil. After correction, 18 SQLite and three HTTP It cases passed once with race, including that new case, active-phase collection, one retained error occurrence and stable repeated Stop. |
| Shared client/build preflight | 52 HTTP/native client cases passed, including unknown authority refusal and false/null preservation; prepared frontend fresh-build verification and regenerated assets passed. Two exact named native facade Go cases passed with race. gofmt, whitespace and go vet passed. |
| New corrected real fixture | One separately authorized source-based request used the production factory, trusted CaptureAndAdmit, normal coordinator and real SQLite/verified object storage. The complete retained 1,454-byte current ProviderTx source was acknowledged daemon_supplied; its 20-byte public method answer was durably saved and verified. |
| Chrome provider-only | Actual compiled shared UI plus six controlled auth/API-key, quota/unknown-usage and paused route/mode/false-usage states. Shift+Tab/Tab, Enter, retained focus and the same polite status node through busy/final were observed; Reader/Review remained usable in every state. Each state observed two exact supplied-metadata 404 replies, 12 total, plus six unrelated/reused-response negative controls. 390 px emulated touch tap/reload, both error listeners, 44 px Settings links and SPA navigation passed. The actual JavaScript SHA is in the wrapper receipt. |
| Wails providers-only | The actual compiled App reported 12 safe passing probe rows and three acknowledged providerStatus operations, rendered current native status and completed trusted-host DOM refresh. No owner token was stored in JS. The App exited normally and owned cleanup completed; source inputs were stable. Named stages, pre-click transition observation and whitelisted failure/last-row retention were active; the private runtime-retention flag was explicit. |

The new live audit separately records native backend_state=completed,
backend_observation_source=live_event, driver_outcome=result_ready and durable
attempt state=result_saved. Its exact CWD/pin/account/mode/input identity and
local drained cleanup were checked without changing account/configuration or
billing. Bounded inherited-capability metadata retained unknowns honestly.
Runtime acceptance used the corrected full-history/explicit-final consumer; no
separately retained raw phase/itemsView, hidden reasoning or native exact-input
telemetry is claimed. Public generation/question admission and an entire daemon
inference workflow were not exercised.

The real fixture ran direct Process. Both UI campaigns exercised status and
reading/Review with no running provider phase. After those campaigns, final
refinement found that Stop could lose an earlier collected accounting error when
a later healthy phase remained active. Stop now drains/collects that phase
before merging the retained first error on every return, avoiding duplicate
attachment. This production delta changes shutdown error aggregation; the
provider/input/output/permission and normal healthy behavior observed by those
campaigns remains unchanged. The affected two-phase behavior has the new offline
control above. The live and UI binaries were not rerun for this late delta, and
whole-core byte identity with their earlier execution is not claimed.

Focused offline controls also cover accepted-input fallback, primary versus
required audit failure, finite intent abandonment, full-history recovery,
current payload/status guards, monotonic restore, hidden historical ETag replay,
dispatcher/Reader/Review availability and actual schema-17 migration/crash paths.
Canonical tests stay offline. None of these feature checks is the complete
common gate.

### Preserved failed observations

Initial focused executions exposed stale assertions, fixture setup mistakes,
compiler/test-envelope errors and real persistence/restore/decoder gaps. Their
original failures remain failed; corrected inputs have separate results. In
particular, known completed output survives uncertain Put responses, and current
finite status authority does not rewrite producing source authority after
protected restore.

The first Chrome campaign failed at an overlong private UNIX socket path. Its
owned Chrome group received one verified TERM; a subsequent leader-identity
guard refused another signal. That command remains failed. Separate fresh
process observations then established both owned groups absent, with no further
signal or evidence/cache removal. A short-parent campaign subsequently failed
Session closed: the failed CLI daemon's bounded public exception confirmed
ReferenceError: URL is not defined in its isolated VM. The response matcher now
uses VM-safe string operations while preserving exact query/method/status/
reason/device matching and individual response accounting. Its successful
changed-verifier campaign above does not relabel either original failure.

The first Wails campaign failed an unlabelled native provider wait despite normal
App exit and cleanup. Its old default cleanup removed detailed runtime rows;
the original failed stage remains unknown. The later named waits, pre-click
transition observation and safe rows saved before assertions provide a new
successful observation, not a reconstruction of that old wait's cause.

### Pending owners and limits

The earlier historical and E01 real successes remain qualified in
[the original implementation record](provider-adapter.md). Common2 subsequently
passed 1,430 selected checks/15 child stages for the prior candidate; R2 completed
REQUEST_CHANGES with six current defects. These do not pass Work4's corrected
bytes. At the Work4 checkpoint, current common/R3/final assessment, acceptance and
commit remained pending. R3 subsequently completed as recorded below.
Private runtime paths/identities and credentials are not promoted into
this record. Chrome touch is emulation, and native refresh is trusted-host DOM
input; physical private-device/OS-input and whole-Epic gates remain unexecuted.
Public generation/skill publication, saved Q&A/context/citations and the assembled
Epic retain TASK-006-04/05/06 ownership and all original compound requirements.
Optional pin-replacement and repeated-probe refinements remain separately scoped
in [deferred feedback](../deferred-feedback/README.md).


The current finite status-authority attachment retains only the explicit
admission's owner auth_epoch alongside existing intents; runtime ownership still
comes from Claim. Real SQLite controls cover same-runtime epoch revocation,
explicit current-epoch release, actual close/reopen, continued status-only
lookup with payload refusal, and completion/Abandon removal. A failed output
Put that already committed result_saved cannot demote that saved fact.

## R3 corrections (Work5)

R3 completed with two reported-Low CODE findings, independently classified by
Root as Valid/effective Medium. All R1/common and R2 source rows, opinions and
failed observations remain preserved. This correction does not reopen the
Invalid F-G preference or change DF-07/DF-08 ownership.

CODE-1 now distinguishes an already-collected historical accounting diagnostic
from current shutdown health. A positive typed transport/lookup drain observation
and successful durable settlement are both required. At that Work5 checkpoint,
only complete Transport/Lookup attachments carried this positive fact; the
pre-audit producer-fact omission is corrected by local6 below. Default or missing
positive local facts still never prove cleanup.
Poll and direct Process retain the first accounting
failure, plus the first later unsafe failure. A known signal_error_drained
diagnostic remains safe only with positive local drain and nil CleanupError; unknown cleanup reasons stay unsafe. Only an
entire error tree of safe historical diagnostics permits the production daemon
restart decision to continue. Current phase errors, unknown cleanup and joined endpoint, dependent,
storage or output failures still stop it. The first safe diagnostic survives
repeated controlled restarts and remains visible at terminal shutdown, without
printing native private diagnostics or changing saved output. The three early
profile/client-directory/profile-root startup failures also join the retained
first diagnostic. Their direct returns were inspected statically; the production
restart-decision helper is exercised with retained and newly joined errors.

CODE-2 keeps the production setup bound at 30 seconds. An internal-only setup
deadline records setup_timeout with prepared/may_have_dispatched=false; actual
caller cancellation and dispatch-fence refusal retain their authority semantics.
No send, accepted ID or incomplete typed transport identity is fabricated.

Offline race-enabled controls first reproduced both defects (3 selected: one
negative passed, two failed). The first corrected run passed four of six but
its two restart fixtures produced no expected diagnostic. Those failures are
retained. Source inspection established that the original CLI fixture also ran
an independent import dispatcher with a nil-driver provider coordinator over
the shared job ledger; the exact failed job reason was not retained. An isolated
real Foundation fixture with explicit phase collection removed that competing
owner and subsequently passed. The six correction controls then passed in one strict attempt. The coherent focused selection passed all 187 selected It cases, retaining
all prior 139 plus the six new cases and directly affected query/capture
consumers, with strict nonempty/no-pending, race and one-attempt settings.
After the bounded signal-drain/all-leaf Stop correction, 12 affected cases
passed; after the three early startup-return joins, four CLI cases passed.
Their union is 190 distinct It cases with all prior 139 retained. The broad
187-case run remains bound to its earlier bytes; the changed branches are covered
by those later controls, not by a claimed repeat full selection. Mixed benign
cancellation/refusal plus independent I/O, actual fallback-writer failure and
unknown/missing drain observations remain unsafe.
The deterministic cases cover the production restart decision, repeated and
terminal stops, mixed/current/unsafe errors, later unsafe retention, unchanged
saved-result hash/no duplicate Execute, missing drain evidence and durable exact
setup-timeout/caller/fence reasons. go vet, affected gofmt and both diff checks
passed separately. No passing raw log was opened.

There were zero new provider/status/model/browser/Wails calls. The historical
real request retains its exact source/frame/ACK/full-history/spool/output proof
for the executed bytes; these edits add failure classification and daemon
restart handling, not a new live execution. The actual Work4 General/AI/Connect,
Reader/Review and metadata-only Chrome/Wails observations remain applicable to
the unchanged UI, assets, status and native facade paths. They do not execute the
new restart or internal-timeout branches. At that Work5 checkpoint, current full
common, bounded R4 confirmation, final assessment, acceptance and commit were
pending. The later actual common3/R4 and local6 boundaries are recorded below.
No review budget resets
and no whole-Epic or physical-device claims are made.

## R4 eligible local corrections (local6)

Common3 completed on unchanged Work5 with 1,454 passing checks across 15 stages,
including all 190 focused cases. R4 also completed the fourth and final authorized
technical assessment. Its REQ-1/VER-1 source opinions remain Low/nonblocking; Root
and the independent evidence Evaluator classified both as Valid/effective Medium
and the entire finite set as local-verified-fix eligible. This is an admitted
local correction, with no policy change, new review or budget reset. The source
review predates this delta and is not relabelled a post-correction review.

REQ-1 now carries an explicit producer LocalClosed/LocalCleanupReason through the
internal ProviderOutcome independently of optional complete native Transport or
Lookup. Execute/Reconcile set it from actual close results or known absence of
an owned transport. A default outcome or arbitrary nil CleanupError stays unknown.
Only genuine successful local drain/no-owned-transport observations and known
safe reasons can join successful durable settlement, actual collection and the
unchanged whole-error-tree restart rule. Failed/unknown cleanup, fallback writer
failure, current phases and independent mixed errors remain unsafe. No complete
audit/backend/accepted ID/source-observation fact is manufactured, and no public
contract shape, migration, queue, stored field or auth/concurrency policy changes.

VER-1 now enters actual owned-child account inspect, thread start and MCP stages
before internal setup expiry. The live caller keeps setup_timeout; caller cancel
and an actual caller deadline after MCP entry do not become an internal timeout.
Append-only synthetic method traces, the actual close result and reaped child
PID checks prove stage and local drain, while assertions reject send fence, ACK,
turn and invented identity.
The exported production Execute keeps its 30-second setup bound. Its real
Foundation/Store/coordinator/trusted CaptureAndAdmit composition exercises that
actual bound, plus late caller cancellation/deadline. Durable jobs remain
paused with the exact reason, prepared/may_have_dispatched=false, unobserved
inputs and no complete native audit/accepted IDs. An actual no-observation
reconciliation with a required accounting failure also retains safe local drain
without another Execute or invented backend observation.

The behavioral red case used the real producer/Store, reached MCP then a
pre-audit disclosure fault and failed only the historical restart predicate.
After the fix, six direct adapter/real-store cases passed in the first combined
run, but its SQLite addition failed compilation because an embedded driver's
pointer receiver required a pointer literal. That original failed run is retained;
the test-only correction did not weaken any behavior assertion. The coherent focused run passed 197 distinct It cases, including all prior
190 and seven new cases, with race/nonempty/no-pending/single-attempt checks.
After that pass, vet detected a value receiver copying the embedded test atomic
field. Changing only that receiver to a pointer left production bytes unchanged;
one fresh affected SQLite case and final vet passed on the exact delta. The
197-case source binding and this one-character test delta are recorded separately,
with no repeat broad run or substituted full common pass.
The unchanged prior190 includes the actual denied-KILL cleanup uncertainty,
current/later unsafe phases, fallback writer failure and independent joined
shutdown controls. These remain distinct from the new explicit positive local
observation; nil cleanup error alone is never promoted to safety. go vet,
affected gofmt and both diff checks passed. No passing raw log was opened.

There were no real provider/status/model/native-client/browser/Wails calls.
Owned Python stdio peers and all source/output data are offline synthetic
fixtures. Historical Work3 real inference and Work4 metadata/UI observations
retain their exact earlier scope and unchanged healthy-path applicability;
they do not prove this local delta executed on a real provider or full current
native binary. The required same-final-code full common4 subsequently passed all
1,461 checks across 15 child stages: Go unit 158/integration 83/persistence 973
and pytest unit 91/integration 64/E2E 92. All 197 exact feature members passed
once under the applicable strict/race settings. Source, index and configuration
matched before/after/current at the run, with no nonignored untracked output.

The subsequent three-file factual documentation overlay changes no Go, UI,
assets or tests. Full4 proves the identical code before that overlay; the later
docs-integrity/diff check is separate, and no repeated full run is claimed.
R4 still predates the local correction and is not a post-correction review.
During that same final assessment, DOC1 identified obsolete pending-common/review
wording in the root documentation index. Its source severity was not specified;
Root classified the mandatory factual gap as Medium. Exact README/MANIFEST
correction and actual docs/diff proof preceded the completed final report. The
initial negative observation and earlier source-time Root done-met record remain
preserved, separately from the corrected current proof.

The final independent assessment passed all 24 current Task03 criteria and four
native Goal criteria, with zero mandatory unmet/unverified rows; the native Task
Goal is achieved. The global catalog remains 7 met/15 unmet/2 unverified under
its ordered future owners. Final independent assessment and delegated member
acceptance establish this bounded Task03 delivery. The normal completion commit
and native finalization follow under the same authority; this record claims no
commit or Master overall Epic acceptance. All original global compounds,
R1/common31, R2seven/F-G Invalid, R3two and DF-07/08 future ownership remain unchanged;
TASK-006-04 follows Task03 closeout, with Task05/06 and the whole Epic gate later.
