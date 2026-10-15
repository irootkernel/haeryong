# TASK-001-04 multi-page viewer experiment

**Task:** TASK-001-04. **Implementation:** complete for the bounded baseline. **G-VIEW:** `pass`.
**Independent review:** complete; no remaining findings against the original fourteen criteria. Runtime evidence remains private and local.

The [disposable fixture](../../spikes/viewer/README.md) implements the selected
html_js_v1 construction. It reuses authentication, preserves the original
TASK-001-02 module and pins its own Wails module to v3.0.0-beta.28. The
[owner amendment](../decisions.md#owner-amendment-2026-10-15-agent-operated-private-browser-gate) selects
an agent-operated actual Aside browser on private HTTPS for the initial gate.
Android Chrome and Safari remain later platform obligations. This experiment does not implement production import, personal
context adoption, saved Review or arbitrary-code containment.

## Original closing criteria

| Criterion | Pinned Wails | Primary Chrome | Aside private HTTPS |
| --- | --- | --- | --- |
| T-001-04-01: useful JavaScript, multi-page/module/framework/network loading and bounded X18/X19 | Original runtime rows carried with archived source comparisons; current A/B module/worker and lifecycle observations | Historical complete checker passed interaction, framework, workers/WASM and lazy loading; current focused navigation evidence composes with unchanged component bytes | Actual UI input 9 to 18; A/B modules/workers/WASM/CDN and exact nested navigation recorded; independently confirmed |
| T-001-04-02: focused host/native authority attempts | Current registered-binding positive and child/navigation denial counters observed; original popup/top-navigation proof retained with unchanged guard/proxy | Current host DOM/storage/management and cross-port attempts passed | Host/cross-port negatives recorded; separate actual popup had no opener/native bridge and denied four scheme attempts; independently confirmed |
| T-001-04-03: broken-page recovery with retained daemon/Review state | Actual broken-script close/reopen and normal close/kill/UI restart retained; current lifecycle uses the same daemon/Review invariants | Current shared broken-script recovery block passed; earlier complete checker remains source-scoped | Broken-script close/reopen retained exact selection and daemon/Review state; independently confirmed |
| T-001-04-04: concurrent immutable versions, exact navigation, delivery/storage/worker/origin lifecycle | Current active-daemon rebind, reload and UI restart observed; authored storage loss is recorded below | Historical download/close, middle click and actual browser/daemon restart passed; current affected exact-history/logical-tab/reload checks are separate | Range/retry, root SW replacement, expiry recovery, exact logical tab, active-daemon restart and storage first reads recorded; incremental browser stream matched disk digest; download UI destination unverified; independently confirmed |
| T-001-04-05: exact page-bound context, stale rejection and interruption | Earlier full exact snapshot and host-reply negatives retain unchanged transfer/validation scope; current pathname lifetime has separate exact fresh-context proof | Earlier full snapshot, 296 invalid/replayed host replies and 4096-byte credit retain exact mechanism scope; current pathname lifetime and successful late-seal rejection are checked separately | Earlier 17,825,760-byte exact context, reply/credit/error and interruption evidence retain unchanged mechanism scope; current pathname lifetime has separate fresh-context and late-seal rejection proof; independently confirmed |

All five criteria are complete for the bounded required baseline. Independent
confirmation covers the original five assertions and nine parent constraints.
The actual Aside private HTTPS observations are composed below; the original
automatic report remains 15 passing rows and one failed popup-relay row, not a
rewritten 16-pass report. Exact source snapshots and original reports distinguish each candidate;
a matching native binary alone is not the basis for carrying daemon behavior.

## Executed observations

The toolchain is Go 1.27.1 on macOS 27.0.1, build 26A434, ARM64. The installed
primary browser is Chrome 154.0.8037.100. Native WebKit reported
AppleWebKit 605.1.15; this is not a separate Safari version. The preceding restored candidate removed only mobile-only lifecycle automation
and matched the complete local execution source. The current bounded correction
changes the page-channel replies, focused predicates, recovery UI and temporary
context serialization; earlier results retain only their unaffected scope. Aside CLI 1.26.1010.1739 and native daemon 1.26.1014.2349 operated the actual
private browser. High-entropy UA Data reported Chromium 153.0.8010.53,
macOS 27.0.1 and mobile false. The reduced UA alone was not used for the patch
version. Android OS/model/browser versions remain unobserved.

Each immutable directory has 312 files totaling 37,756,695 bytes, including
300 unused entries and a 36 MiB attachment. The versions differ at identical
root-relative fetch, module and worker paths. Request observations confirm that
unused entries were not fetched. The finite fixture is not an admission cap.
Stored objects are checked against persisted byte counts/SHA-256 and served
from the same verified descriptor. Missing or corrupt bytes fail before parsing;
authored same-version Service Worker responses remain ordinary runtime behavior.

The receiver streamed the entire 17,825,760-byte UTF-8 snapshot with SHA-256
`faa01fca42b0699f5c52c19606c9552d5a34653cbdbd5893bc9db777a6ce43c7`.
Current browser checks cover malformed typed payloads, foreign handles,
wrong owner/document/version/page/instance, resumed offsets, idempotent seal and
a seal held across actual document replacement. Old-channel completion cannot
attach context to the new document. The temporary receiver does not adopt
context into Notes, Ask or Review.

Focused Go race checks passed for handoff scope/replay, cookie sender checks,
independent selected-object downloads, immutable-byte failures, complete
UTF-8/JSON validation, offset/digest conflicts, interruption, protected-stream
owner revocation, trash/restore, local-control auth restore and persisted origin
reassignment rejection. These common-handler negatives need not be duplicated
as a full GUI cross product. The original auth-ingress race suite passed after
the fixture-only restore wrapper; that helper and its transaction are unchanged.

## Current local lifecycle and interaction

The primary checker launches the installed Chrome binary with an owned 0700
profile, minimal arguments and loopback CDP attachment. It uses browser download
events and the actual GUID destination file, without Playwright `saveAs` or a
whole-file Blob. Download start, completed Close VersionA action and download
completion occurred in that order. The 37,748,736-byte destination has SHA-256
`9eda8c4947322cab7c1541638786dddee7746b934d32af25d15012d158f68639`,
matching the immutable inventory. Progress outlasted the initial 30-second lease.
The authenticated server close observation and stream completion corroborate
this separate download lifetime.

Actual keyboard input changed the retry control to 9, Tab/Enter activated
Simulate retry, the result became 18 retry units and focus remained on that
button. An actual middle click on the authored nested-page link opened the same
immutable version at `pages/scenario.html?from=nested#result`. Logical reader
new-tab navigation separately retained the selected path, query and fragment.
The injected same-version popup proof link explicitly retains its artifact
opener; it conveys no trusted host or native authority.

For each local construction, the owned daemon actually received SIGTERM and was
relaunched with the same binary, state and exact origin arguments while A/B were
selected. Its instance changed, the owner session remained the same, old
renderer renewal returned 404, and automatic renewal reissued exact A/B delivery
with new identities. Fetch/module/worker version identity and page/query/fragment
remained correct. The exposed current config now follows the same object used
by the recovery handler. No live auth restore or route change was involved.

Authored localStorage, IndexedDB and Cache Storage use unique A/B sentinels.
Read-only probes run before rewriting after reload, actual UI/browser restart
and active-daemon restart. Chrome retained all three stores in these observations.
Wails retained them across reload and daemon restart. After actual Wails UI
process exit/relaunch, both versions lost localStorage and IndexedDB values;
Cache Storage retained its exact sentinels. This observed loss is visible in the
fixture and does not promise persistence. Its cause is not inferred. An absent
IndexedDB read aborts database creation; writes repair an owned empty legacy
fixture database through an actual version upgrade. Real Chrome regression
checks cover both paths, so helper-created empty databases are not called a
platform limitation.

Native normal window close explicitly closed its observed current renderer
grants; forced kill produced no close receipt and those grants expired instead.
Old ephemeral grants destroyed by daemon restart can return 404 at later native
cleanup. Browser force termination is a lost connection unless an authenticated
close is actually observed; lease expiry is not an explicit-close receipt.

## Construction and preserved incidents

[ADR-006](../adr/ADR-006.md#selected-task-001-04-construction) records exact
trusted/content listener mappings, credential delivery, native filtering,
quarantine and rollback. Cookies share same-host ports, so Host, actual Origin,
Fetch Metadata, owner, version and lease are checked rather than assuming cookie
port isolation. The sandbox form handoff can have `Origin: null`; only its
one-time narrow route accepts it. An authored root Service Worker may observe
its own later renderer handoff. No owner bearer enters author code or URLs.

A separate normal-TLS synthetic prefix experiment passed prefix joining,
escaped paths, Host forwarding, two exact ranges and redirect behavior. Direct
upstreams were selected to keep trusted and content-only handlers distinct;
prefix forwarding was viable. Only owned routes changed, and the temporary
prefix route was removed with the complete prior configuration restored.

The native guard checks actual WK sender origin/main-frame metadata before
Wails' special message branch. Trusted positive calls use its real registered
MessageProcessor binding. Popups use the supplied configuration with a fresh
empty user-content controller. This SDK rejected unrelated popup configuration
and scheme-handler removal, including an abort. Current middleware denies every
copied native-scheme request; it does not remove that scheme handler. Those
failed experiments and native timer/timeout incidents remain original failures.
Timed fixture orchestration uses a native wait callback without promising that
hidden authored JavaScript timers continue normally.

Earlier default Playwright launches terminated Chrome after download began while
the driver awaited Close; actual click dispatch was not proven. Both headed and
headless failures remain retained. CrBrowserMain EXC_BAD_ACCESS with mostly
unsymbolized ChromeMain offsets does not establish the root cause. The passing
minimal-arguments/CDP construction changes both launch and download collection,
so it does not isolate one causal factor. Earlier held-route, popup-opener,
missing-phase and premature-process-wait checker errors remain failures too.
No browser, package or tool was installed.

## Actual Aside private-browser observations

The owner explicitly superseded the initial physical Android prerequisite with
agent-operated Aside private HTTPS proof. No actual Android visit, pairing or
result was collected, and none is inferred from desktop evidence. Android and
Safari remain later support/release obligations. Stage A's actual physical
second-device workflow remains with its later owning Task.

The trusted host was restored byte-for-byte to the preceding complete local
candidate, retaining read-before-rewrite checkpoints and existing delivery and
storage behavior. The subsequently added mobile-only automatic daemon-restart
helper, its triggers and optional wait were withdrawn. Its first desktop helper
component had observed an actual restart/rebind/checkpoint and same-document
skip; a combined reopen observer then timed out at 100 seconds, and one narrowed
followup timed out at 20 seconds. These original partial/failing captures remain
immutable historical evidence, not a passing replacement or a platform limit.
The earlier complete primary Chrome and Wails lifecycle evidence remains bound
to its unchanged source and scope.

Root directly operated task-owned Aside tabs on the real private HTTPS route
with normal TLS, no model/provider call, installation, emulation or owner action.
Actual UI input changed 9 to 18 retry units; a screenshot and snapshot record
the result. The automatic report has 15 passing rows and one failed authored
popup opener-relay row. A separate actual popup retained the immutable A page,
query and fragment, had null opener and no native bridge, and denied all four
native-scheme attempts. This separate boundary observation composes with the
report; it does not turn that original failed relay into a pass.

An actual trusted logical-tab action reopened the same owner/version at
`pages/scenario.html?from=nested#result`. While both versions were selected, the
owned daemon received SIGTERM and was relaunched with the same restored binary,
state and origins. Its instance changed; the owner stayed the same, old A/B
delivery renewal returned 404, new grants retained exact A/B page/query/fragment
and data/module/worker bytes, and all three authored stores were read before
rewriting. Owned tab close/reopen separately retained those stores. This is not
an Aside application process restart. Existing primary Chrome and Wails actual
process restart evidence retains its scoped applicability; no mobile background
or force-stop result is inferred.

A controlled trusted `Storage.prototype.setItem` exception exercised an
unavailable write and retained current in-memory access. Separately, actual
trusted-origin `localStorage.clear()` produced empty storage; a new exact
pairing was approved locally once, remembered access returned, A's worker bytes
were correct and Review remained false. Author origins were unchanged. These
are separate unavailable-write, storage-loss/re-pair and remembered-access
observations, not simulated platform support.

The actual Stream attachment button started an Aside browser download, and
server first-byte, authenticated renderer-close and complete 37,748,736-byte
server write were observed. Aside's download-path waiter timed out, so that
browser-download destination and client completion are unverified. A separate
trusted fetch in the same actual browser consumed a ReadableStream incrementally
into a temporary file receiver: 180 parts, actual reader completion, 37,748,736
bytes and SHA-256 `9eda8c4947322cab7c1541638786dddee7746b934d32af25d15012d158f68639`,
matching inventory. Server first byte preceded actual authenticated A close,
which preceded successful finish after 130.218 seconds, including a real paused
receiver/resume. No whole-file Blob was used. This proves the observed browser
stream/temporary destination; it does not inspect the download UI's destination,
prove production export or establish constant browser buffer memory.

The automatic report also records root-scope Service Worker replacement,
Range/retry, lease expiry with actual Reload recovery, a broken-script
close/reopen, the complete 17,825,760-byte snapshot, 16 malformed/misbound
rejections and interruption without prefix adoption. Shared Go negatives and
unchanged native boundary proof retain their earlier explicit applicability.
A Gaori-run original-evidence validator checked the captured Aside observations
and source inventory; its pass is not a new browser run or gate acceptance.
Earlier CDP wait, popup relay, isolated frame-global and transient pre-pair poll
errors remain original failures. A probe's process exit zero with a JavaScript
failure is not a semantic pass. All task-owned Aside tabs/REPL were closed;
shared user tabs and existing private routes were preserved.

At that earlier checkpoint, TASK-001-04 was `in_progress`, G-VIEW was `not_run`,
and TASK-001-05 had not started. The original five assertions and selected independent review remain
required under the current owner amendment; recorded evidence is not acceptance. Runtime credentials, original
reports, screenshots and paths stay private; the current handoff preserves
all preceding assemblies and their failures without rewriting them.

## Current bounded review correction

The first selected review operation was incomplete, with missing security and
artist coverage; its accepted original reports and failed recovery remain intact.
Available review findings and an independent Judge check admitted corrections
inside the original Task04 criteria. No Task/gate acceptance follows from that
classification, and a new selected review must inspect the corrected candidate.

Host context ACK/ready/error now carry the complete protocol/current binding and
an independent monotonic host sequence. The loader validates binding, current
port/generation, sequence, typed reply and pending transfer before advancing or
resolving it, and obeys positive advertised credit. Initial authored errors wait
for their current page channel, reset with a new document and appear with recovery
actions. Original popup relay failures remain unchanged; a separately identified
popup gesture must retain the exact version/page/query/fragment and actual source.
A popup without an opener displays its relay limitation. Native post counts do
not prove privileged execution; actual registered-binding counters and native
denial receipts remain the boundary oracle.

Manual Close/Reopen retains exact selection; renewal/remount failures are visible.
Ended owner access clears memory/stored bearer, sections and pending work, disables
actions and preserves the exact route for Reload to Connect. Checks start only
from explicit Run or `autorun=1`; a stored earlier Run flag no longer starts them.
Physical Chrome confirmation is hidden outside its actual Android fixture, and
storage recovery is labelled fresh pairing only when a new pairing occurred.
Server download first-byte/finish/error observations remain distinct from an
unverified browser destination. Selected direct upstreams and temporary early
service seal versus future durable upload/adoption are now explicit.

Temporary context IO serializes per transfer rather than holding the global
delivery mutex. Map membership remains brief; abort/seal/replay and current-owner
checks retain their transfer lifetime ordering. Focused Go race checks exercise a
held context read while independent renewal, stream writes and another context
progress, and deny bytes when owner access ends during the held read. Stream
interruptions record the actual owner/closure error, including early returns.

The focused current runtime proof covers complete exact context, malformed and
replayed host replies, advertised credit, a stale generation and a real held
successful seal across document navigation. It also checks symmetric A/B identity,
exact repeated/resumed range slices, post-navigation authority and sibling state,
manual recovery and visible current errors. Large attachment downloads, storage
and daemon/client lifecycle are carried only where unaffected; they are not
silently rerun or relabelled a complete new candidate. At that earlier correction checkpoint, focused runtime reports were recorded
below while final independent review and acceptance remained pending.


The corrected selected candidate passed ten focused rows on actual primary
Chrome and ten on actual Aside private HTTPS. Each transferred the complete
17,825,760-byte snapshot/digest, rejected 296 injected malformed/misbound/replayed
host replies, observed 274 strictly increasing host replies and honored a
4096-byte advertised credit. A real old-document seal response completed HTTP200
with a temporary 36,864-byte exact object/digest after navigation; the new document
retained neither its handle nor a ready result. The tests preserve the actual
old/new render IDs and completed service response, not a timed assumption.

Pinned Wails recorded those seven core context/range/boundary rows before a
manual-Reopen test race failed. The failing original remains intact. Its current
core code is carried byte-for-byte into the next candidate; only a UI selector
and the active-Reopen wait changed. A bounded three-row Wails UI followup then
passed exact nested/query/fragment Close/Reopen and active Reopen, current broken
page error/display/reset, and truthful explicit-run/confirmation behavior, with
actual normal window-close cleanup. Native child post counts were four per
attempt; the real binding counter stayed at one before the trusted positive and
became two afterward while twelve child messages were denied. Sending was not
misrepresented as privileged execution.

Chrome separately visited the normal root with the obsolete stored Run flag set:
no views, phase or full report appeared and Run remained available. Its controlled
HTTP401 UI check ran last, cleared stored/memory token and sections, disabled
actions and retained exact navigation for reload-to-Connect. That is an injected
ended-access UI observation, separate from actual common-handler revocation race
checks. Final pass was published only after applicable checks finished.

An earlier local Chrome readiness observer timed out before collecting any core
rows, and the Wails Reopen oracle read the old ready view before its asynchronous
replacement. Both remain failed originals. The repaired wait requires a different
delivery and render identity. No attachment download, storage or daemon/client
restart matrix was repeated for these focused changes; earlier successful
transport/lifecycle observations retain their explicit unchanged scope and limits.


## Independent-review ordinal 1 correction

The first completed independent-review assessment returned REQUEST_CHANGES.
Its complete three semantic Medium findings remain current Task04 work: accepted
location was absent from the trusted reload route; overlapping renderer actions
could orphan a frame, renewal or grant; and the public full-checker broken-page
oracle conflicted with exact Close/Reopen behavior. Earlier WORK readiness and
focused successes do not settle these subsequently identified gaps.

The correction synchronizes the selected logical version, including an explicit
B route, using history replacement without adding reader history entries.
Per-version intent generations and serialization invalidate pending mount work;
actual late issued grants are closed by exact ID, and stale renew completions
cannot affect a replacement. A disposed BFCache-restored host reloads its exact
route. This conditional recovery is not a claim that every runtime used BFCache.

Focused required checks now cover real held close/delivery overlap from open and
closed A, Reload followed by Reopen, and pending Close while B's delivery,
binding, data and worker remain unchanged. Actual document reload after authored
navigation/client history, back/forward, unrelated URL fields/history state and
explicit B bootstrap are distinct from the older Reload-version-A button proof.
The public checker's actual shared broken-recovery block verifies a fresh broken
binding/error before explicitly selecting a healthy page. Current runtime proof passed on actual primary Chrome, pinned Wails and Aside through private HTTPS. All three exercised nine held-response/renew subcases with the same B grant/binding/current-read/worker, the exact broken-page helper, actual authored back/forward, and real A/B trusted-document reload with changed document/render identity and unchanged owner, URL fields and history state/length. No unchanged slow download/storage/restart matrix was rerun.

At that earlier checkpoint, Task04 remained in_progress, its five boxes unchecked
and G-VIEW not_run pending the next selected assessment and acceptance. All original negative criterion rows, prior native review failures,
route-switch authority and historical evidence limits remain applicable.


The corrected Chrome driver also passed pending delivery completion after an
injected owner-end HTTP401: no view/section returned, owner UI/token stayed ended,
and the test's exact issued grant was cleaned using its captured trusted test
authority. The real backend owner was not revoked by this injection, and its
credential was not restored into application state. Common-handler actual
revocation evidence retains its prior unchanged scope.

The first current Chrome run completed all five component rows but the driver
failed after classifying two intentional authored broken-page exceptions as host
errors. That whole failed run remains failed. A driver-only followup retained
message/stack for every exception, attributed only the exact configured renderer
origin and broken.html source with the expected intentional error, and reported
no trusted errors. Unknown/unattributed errors are not ignored. The native probe
passed four rows and exited normally at its explicit ninety-second bound; the
actual window-closing hook closed its current grant with204. Aside passed four
rows without the optional pending-owner injection. These are composed focused
results, not a new full slow-matrix pass. BFCache-specific persisted pageshow
recovery is implemented; no actual BFCache hit is claimed from ordinary reload
or authored history traversal.


## Independent-review ordinal 2 correction

The second completed assessment requested three further existing-contract fixes:
retain exact selection while the new registered iframe has no accepted binding;
restore same-view automatic renewal after pre-replacement config refresh failure;
and observe ordinary History pushState/replaceState without a manual fixture
state message. The complete original three-report set is preserved, including
the approving report; findings were classified by contract evidence rather than
vote. The mixed generation/location set requires the next independent assessment.

Requested and accepted selections now have explicit lifetimes. Failure repair
checks the same current live view and latest intent, retaining Close, supersession,
ended-owner and actual pagehide invalidation. The derived location observer keeps
native History return/receiver/argument/exception behavior and leaves successful
authored calls successful even if observation fails. The original corpus bytes,
Go handlers/auth/storage and native guard remain unchanged.

The current explicit probe holds real A port envelopes through the current
handler in original order. It observes authored worker readiness while trusted
binding/state remain null, then performs repeated Reload and loading Close/Reopen
at an exact nested path/query/fragment. Controlled503 and transport failures are
followed by real eight-second timer renewals, with exact A ID attribution and
normal204 responses. Controlled404 plus a failed config refresh is retried by the
next natural timer; it is not labelled a new actual daemon restart. Every case
retains B's delivery/binding/page and protected200 read. Close, newer intent and
actual pagehide cannot revive a failed old operation. Chrome also exercises
injected owner end during failed refresh and exact test-grant cleanup, without
claiming actual backend revocation.

Normal authored push AND replace automatically synchronize current A/B routes.
Actual back/forward adds no duplicate steps; actual trusted-document reload changes
document/render identity while retaining owner and exact selection. Logical B tabs
retain the final ordinary-history location. Native History invalid receiver,
uncloneable state and cross-origin URL throw their native errors, and successful
replace returns undefined. The nine existing overlap/renew subcases and public
broken-page recovery helper are rerun for the affected generation boundary.

The first new native probe failed after the initial-ready hook allowed later
messages to overtake its held ready. The first Chrome probe reached natural
renewal but incorrectly waited for200 instead of the handler's normal204; its
additional authored scenario fetch error remains in the original failed report.
The helper now preserves port order and waits for actual same-render worker
readiness before deliberately closing loading frames. No unknown error filtering
was relaxed and no failed report was rewritten. Current successful proof and
operational settlement are recorded separately after execution.

At that earlier checkpoint, Task04 remained in_progress, its five criteria
unchecked and G-VIEW not_run pending independent assessment and acceptance. Prior
full-download, storage, actual browser/daemon restart and context results retain
their exact source applicability; no unchanged slow matrix is repeated. Android
and Safari remain untested later platform obligations.


The corrected current candidate passed eight focused rows on primary Chrome,
seven on pinned Wails, and six shared rows on actual Aside private HTTPS. Aside
additionally used the actual logical-reader link to open B with the exact query/
fragment, same owner and protected200 read; closing only that tab retained the
parent's same B grant/binding/read. These are separately composed observations.
Current high-entropy Aside browser data reobserved Chromium153.0.8010.53,
macOS27.0.1 and mobile false. Chrome has no unknown/trusted page errors; its two
intentional broken-page exceptions retain exact renderer source stacks.

Native actual document reload retained the observed history.state value: A was
already null before reload and stayed null, while B retained its explicit marker.
It is not evidence that A's earlier marker survived every joint frame-history
traversal. Wails exited normally at the explicit ninety-second bound and its
window-closing hook closed its current observed grant with204. No new context,
slow download, storage or real daemon-restart matrix was run for this narrow
correction. Those original source-bound observations and limitations remain.


## Independent-review ordinal 3 pathname correction

The third completed independent assessment identified one Medium finding:
ordinary History changes to a different pathname retained the old page channel
while page metadata and the replacement ready message were pending. Exact
Reload/Close/Reopen could therefore select the preceding path, and a late old
context seal could be attached to the new location. The three authentic reports,
including both approving reports, and the complete earlier negative rows remain
preserved. Classification follows the contract, not the number of approvals.

The derived loader now invalidates its old port, generation and pending context
at the pathname boundary. Its existing disposing message carries the complete
next location and a fresh announcement identity. The host retains that exact
requested tuple and disposes its old channel/context before awaiting page
metadata. Loaded/connect/ready admission checks the current announcement and
complete page/query/fragment, rejecting stale metadata, ready and old-connect
attempts, including repeated A-to-B-to-A transitions. Query/fragment-only changes
retain their ordinary current-page channel. Native History receiver, arguments,
return values and exceptions remain native; observer failure does not turn a
successful authored call into an exception. This is automatic derivative
integration, with no compulsory author SDK, manifest or metadata.

Actual push AND replace to a different pathname were checked with real successful
page metadata held, real candidate ready held, controlled metadata503 and a
controlled stale ready. Loading repeated Reload and Close/Reopen retained the
exact new nested page/query/fragment. The late-seal case held the trusted request
before dispatch, then actually sent it after the transition: the server returned
HTTP200 and an exact 36,864-byte temporary seal. The old host handler completed,
but no old successful ready was sent or adopted and the old transfer was
interrupted. Fresh current-page transfers matched their exact byte count and
SHA-256. This does not claim an already server-inflight response hold or a saved
production context. B's exact binding/grant and protected200 reads were checked
through the focused A cases.

The original whole pathname checker remains available with every Back/Forward
assertion and its original default behavior. Its native runs failed at an extra
query-only Back sequence after an actual trusted-document reload; those whole
runs remain FAIL. Later diagnostic runs preserved twelve completed positive rows
and separately observed native normal exit and the real window-closing hook.
The observed Back restored about:blank and the old child emitted pagehide with
persisted false. No later pageshow was observed. The cause is UNKNOWN; this is
not BFCache proof, platform certification, a fixed issue or a reclassified pass.
Skipping redundant parent replaceState, gating initial load, suppressing parent
query writes, constant frame names and stable initial DOM order did not resolve
the observation. Those experimental changes were removed. A prepared three-case
minimal native reproduction was not executed and is not evidence of behavior.

The retained Judge's scope interpretation applies the pre-existing single
closing list and additional-case rule in the viewer contract. It does not add
this particular reload-plus-query-Back combination as an early closing
assertion, nor does the present evidence connect it to failure of the required
pathname/context guarantee. General Back/Forward requirements remain unchanged.
The previous IR2 actual Back/Forward proof is carried only for its observed
before-reload sequence. This interpretation is not WORK approval, evidence
admission or final acceptance.

A separate explicitly named required-pathname closing mode has its own journal,
report kind and collection flag. It shares the twelve real pathname/recovery/
actual-reload observations, then checks fresh exact context, ordinary query push
and replace, native History semantics, unchanged current binding/context, no
extra history step, and exact host URL plus logical-reader URL. It does not
execute or claim the additional after-reload Back combination. The original
whole checker and all failed originals remain intact.

The current required subset passed fourteen rows on pinned Wails and fifteen
on primary Chrome with no trusted page errors. Wails opened a real unprivileged
logical popup at the final exact nested page/query/fragment, retained B's current
binding/read, exited normally, and its authentic closing hook closed both current
grants with204. Chrome opened the actual exact logical tab and separately tested
an injected owner-end/config failure; that injection is not actual backend
revocation. Aside passed thirteen shared rows on private HTTPS, then an actual
logical-reader UI click opened exactly one new trusted reader with the exact
selection, remembered owner, new binding/grant and protected200 read. Closing
only that tab left the parent A/B reads200 and B's binding/grant unchanged. These
are composed required observations, not identical whole runs. Aside high-entropy
data reobserved Chromium153.0.8010.53, macOS27.0.1 and mobile false.

The Aside logical tab's framework state was still pending at collection; this is
not a new full-Run/CDN pass. Its logical screenshot captured the main two-column
screen and is unusable as logical visual evidence. The actual UI action,
logical DOM snapshot, exact identity and protected read remain separate valid
observations; the inspected main screenshot proves only the main UI. A bounded
pairing watcher found no new matching request because Chrome reused remembered
owner access; it made no approval or mutation. Its empty receipt reservation is
not a successful pairing receipt or a product failure.

### Current fourteen-criterion evidence mapping

All rows below describe the composed evidence independently confirmed against
the unchanged original fourteen criteria.

| Original criterion | Current contribution and retained scope |
| --- | --- |
| T-001-04-01 | Current A/B modules/workers and exact multi-page selection; unchanged authored assets retain prior useful JS, framework/CDN, WASM and requested-only above-former-limit loading proof. |
| T-001-04-02 | Host/native separation retains exact guard, real registered-binding positive, denial counters, HTTP403 and separate actual popup proof; native sends are attempts, not privileged execution. |
| T-001-04-03 | Current shared public-checker broken-page recovery and intent/renewal regressions compose with prior actual UI close/kill/restart and unchanged daemon/Review recovery. |
| T-001-04-04 | Current three constructions prove exact pending-path document reload, concurrent B independence and actual logical new tab; earlier byte-bound delivery, SW/origin quarantine, storage and real browser/daemon lifecycle evidence retains its stated source scope. |
| T-001-04-05 | Current pathname invalidation, stale metadata/ready/connect rejection, actual old HTTP200 seal without old ready/adoption, interruption and fresh exact context; earlier large transfer/credit/digest/reply and receiver checks carry only unchanged mechanics. |
| DELIVERABLE-VIEWER | Disposable viewer, separate pinned Wails module, complete immutable corpus and exact source/binary capture remain available. |
| DELIVERABLE-EVIDENCE | Current required subset, all whole failures, partial rows, injected faults, unexecuted preparation and unusable visual evidence remain distinguishable. |
| DELIVERABLE-ADR | ADR records the selected construction, new page lifetime and unresolved extra Back observation without platform or whole-pass claims. |
| OWNER-BASELINE | Actual agent-operated Aside private HTTPS, primary Chrome and pinned Wails remain the early constructions; Android/Safari are untested later obligations. |
| DEPENDENCIES | Task04 records done, five boxes checked and G-VIEW pass; EPIC-001 remains in_progress and Task05 then07 then06 remain planned. |
| UI-ACTION | Actual named recovery actions, logical-reader clicks/popups and prior desktop keyboard/middle-click observations retain exact applicable scope. |
| AFFECTED-CHECKS | Fresh three-construction pathname/context/reload proof and current IR1/IR2 renewal/overlap/broken regressions; no unchanged slow-matrix repetition. |
| FIDELITY/PRIVACY | Original failed reports are not rewritten; credentials and runtime identities remain private, and local evidence is not promoted as durable tracked proof. |
| BOUNDED-SCOPE | Original five closing assertions and nine parent constraints remain unchanged; additional executed cases retain only their bounded result. |

The complete earlier 17,825,760-byte transfer, reply/credit/digest checks and
streamed attachment destination proofs are carried through exact mechanism
comparisons, not a blanket unchanged-context-lifetime claim. The current lifetime
race is independently checked above. Prior download evidence includes actual
first-byte/renderer-close/finish ordering and exact desktop destination bytes;
the Aside incremental stream proof does not establish its native download UI
file or constant browser-buffer memory. Production Review, Notes/Ask adoption,
import/export and durable context promotion are not implemented by this fixture.
No new auth/source/guard/content change requires another auth race or full slow
lifecycle run for this correction.

All owned runtime processes/tabs and the three private foreground routes were
settled, the original Serve/Funnel configuration was restored exactly, and the
owned listeners are absent. Private originals, source snapshots, state and
binaries remain retained. There is no current live test URL. The complete Worker
handoff, retained WORK and final frozen-finding independent confirmation support
the bounded Task04 done/G-VIEW pass record. EPIC-001 remains in_progress;
G-AI, G-VALUE and G-CONTRACT are not_run and Task05 then07 then06 remain planned.


## Bounded closure and retained limits

The final independent frozen-finding confirmation comprised three fresh complete
reports against the same unchanged candidate. Each assessed all fourteen original
criteria, approved, and reported no findings; the evidence assessment confirmed
those reports and their exact target. No Low finding, local correction or
hardening deferral remains. The selected review budget is exhausted; this record
does not authorize another review or a future Task. This static confirmation is
not a browser rerun and does not rewrite prior negative checkpoints.

Task04 records done with its five original assertions checked, G-VIEW pass and
ADR-006 accepted for this bounded construction. The closure composes current
required Wails, primary Chrome and actual Aside observations with precisely
unchanged prior mechanics and retained limits. All whole native Back-after-reload
runs remain failed with unknown cause; their additional sequence is not silently
turned into required-subset success. The separate required report does not claim
that Back sequence. Aside's unusable logical PNG, pending logical framework,
native storage losses, original guard-artifact loss and download UI/destination
limits remain as recorded above. Chrome's driver exit was observed as zero; its
browser was stopped/reaped by the driver, but no separate numeric browser exit
was recorded. Native exit zero and the authentic two-grant window-closing result
were independently observed. Actual/injected distinctions and large-mechanism
carry remain unchanged.

There is no production application release, promoted-evidence package, release
notes enrollment or new hardening record. Runtime evidence remains private and
local; no live test URL remains. EPIC-001 is still in progress with G-AI, G-VALUE
and G-CONTRACT not_run. Its remaining Task order is05, then07, then06; none has
formally started through this closure record.
