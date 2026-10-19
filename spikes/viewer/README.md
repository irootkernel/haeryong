# Disposable multi-page viewer experiment

This TASK-001-04 fixture serves two immutable result directories and a trusted
receiver from one Go daemon. It is a feasibility harness; it saves no Notes,
Ask, Review or production library state. See the [Task](../../docs/tasks/EPIC-001/TASK-001-04.md)
and [ADR-006](../../docs/adr/ADR-006.md) for acceptance and authority.

The fixture reuses `../auth-ingress` for one-time pairing, remembered owner
access and revocation. Its own Wails module pins `v3.0.0-beta.28`; the Task02
module and downloaded dependency are unchanged. The native fixture uses the
actual registered Go binding dispatcher behind an Objective-C frame/origin
guard, with no default runtime HTTP transport. All `wails://` asset requests
are rejected. A popup uses the SDK-provided configuration copy with a new,
empty user-content controller. It cannot inherit the native message handler.

Since the TASK-003-05 facade lift, the shared typed ReaderService facade and
the Darwin guard live in the separately pinned repository `desktop/` module
(`haeryong.local/haeryong/desktop`, same `v3.0.0-beta.28` pin); this spike
keeps only the thin harness wiring — the fixed static origin, guard/bridge
bootstrap, selfcheck page and probe gating — and imports that package through
a local module `replace`. The root Go module stays Wails-free.

## Run an owned experiment

Create an owner-only state directory under the verified mounted RootKernel
scratch volume. Supply explicit paths; credentials must not enter arguments,
URLs, logs or author messages. The local control file and native session file
stay mode 0600. Build each module independently with the existing toolchain:

```sh
go -C spikes/viewer build -o /absolute/private/scratch/viewer ./cmd/fixture
go -C spikes/viewer/desktop build -o /absolute/private/scratch/native .
/absolute/private/scratch/viewer --state /absolute/private/scratch/state
/absolute/private/scratch/native --state /absolute/private/scratch/state
```

The default trusted origin is `http://127.0.0.1:9270`; content-only origins are
ports 9272 and 9273. Optional `--remote`, `--remote-a`, and `--remote-b` configure
three exact private HTTPS origins. They do not configure Tailscale or create a
listener beyond the three loopback listeners. An authorized operator must
preserve existing routes and keep new routes private.

Open the trusted origin with a nonsecret `?attempt=<test-label>` and approve
only that exact pending fixture request through local control. Owner bearers
stay in trusted browser storage or native Go memory. The trusted harness runs
its checks after approval and reports through its authenticated trusted origin.
Blocked popups require an actual gesture. The current early gate selects an
agent-operated actual Aside browser on private HTTPS; record its real engine
and transitions. Tab navigation does not substitute for a browser process restart.

## Delivery and recovery

A renderer receives a one-time POST handoff, then an HttpOnly, version/origin
bound narrow credential. Same-host ports share cookies. Protected reads check
Host, supplied Origin, browser Fetch Metadata, current owner access and delivery
lease. Cross-port classic scripts, modules, workers and navigation are exercised;
port separation alone grants no authority. An authored root Service Worker may
observe its own later handoff POST and its narrow renderer credential; it never
receives the owner bearer. Same-version authored responses remain ordinary
runtime behavior, distinct from verified stored-byte delivery.

Each origin is persisted against its immutable version and inventory digest.
Restart refuses a swapped mapping. Preserve that registry whenever preserving
an origin's browser storage; deleting a profile is not permission to reassign
its origin to another version. Files are hashed on the descriptor that is
subsequently served. Missing or changed stored bytes fail explicitly before
content consumption.

Separate downloads use a selected-object credential and lease. The test-only
`X-Fixture-Delivery` selector isolates one renderer grant for expiry assertions
when another valid same-version tab is present; it supplies no credential or
additional authority. Ordinary assets need no custom header. They survive
renderer close, renew while progressing and recheck owner access during writes.
Browser downloads use a separate HttpOnly selected-object cookie and a direct
Content-Disposition response; native downloads
stream to an owned file. Neither path creates a whole-file JavaScript Blob.
The synthetic trash/restore and auth-restore routes exist only to exercise the
contract. Auth restore requires a local Host, no Origin and local control;
never use it on a live physical-test database.

The receiver streams page-bound UTF-8 text/JSON into temporary files, validates
complete bytes and content before issuing an opaque sealed reference, and
rejects invalid/stale bindings and conflicting replay. Channel generations
cancel pending work on document replacement. The fixture does not adopt this
context into production personal state.

## Verification

Run checks through the repository's selected Gaori entrypoint. Go tests require
`HAERYONG_VIEWER_TEST_TMP_ROOT` pointing at the verified owned scratch directory.
`check_browser.py` launches the explicit installed Chrome binary with minimal
arguments and connects the existing Playwright module over CDP. It uses an owned
persistent profile, browser download events and actual process close/relaunch. Its explicit `--playwright-module` path does not install a dependency. `check_native.py` launches only
its explicit binary and verifies registered binding effects, actual lifecycle
and streamed destination bytes. It does not close a shared/default browser.

Runtime reports, downloaded private files, credentials and Gaori evidence stay
local. A successful fixture row alone does not pass G-VIEW. The full Task's five
criteria, all three required constructions and independent review are required.

The current local primary Chrome checker passes with minimal installed-browser
arguments, an owned profile and CDP download events/direct destination hashing.
The earlier default-launch failures remain retained without an established cause.
`check_lifecycle.py` collects focused current Wails storage/recovery observations;
`check_support.py` verifies exact owned executable/state before SIGTERM/relaunch
of that daemon. These checks preserve auth state and existing private routes.

Read-only A/B storage probes precede rewriting after reload/client/daemon restart.
The native UI restart lost localStorage/IndexedDB values and retained Cache
values; the fixture displays that actual limitation. Primary Chrome retained
all three stores in the recorded actual process and active-daemon restarts.
Actual Aside private HTTPS observations are recorded; at that earlier checkpoint
independent review remained pending. Its automatic report retains one popup-relay failure with a
separate actual popup boundary proof. Aside download UI destination is unverified;
a separate incremental browser ReadableStream into a temporary file matched
exact inventory bytes/digest after renderer close. Owned tab reopen is not an
Aside process restart. See the
[current evidence record](../../docs/testing/viewer-spike.md).

The earlier physical Android selection and mobile-only automatic daemon-restart
helper were superseded by the owner's Aside direction. The helper is removed;
its partial restart/checkpoint observation and two failed reopen checks remain
private historical evidence. No actual Android result was collected. The
existing later mobile checkpoint/download collector paths are fixture
preparation, not required early-gate evidence or a mobile support claim.
Reports are authenticated body-hash captures, not cryptographic signatures.


## Focused correction checks

`check_focus.py` drives an explicitly installed Chrome or pinned native binary
against the already running owned fixture. It configures no service/routes and
approves no pairing. The shared `fixtureFocus` check covers complete context,
invalid/replayed host responses, advertised credit, real navigation during a
held successful seal, exact repeated/resumed ranges, focused authority and manual
recovery. `--ui-only` selects only affected UI rows; native `--focus-ui-only`
requires `--focus`. These selectors do not claim a new full download/lifecycle run.
An exact pending local label still requires the authorized coordinator's approval.

Both host and loader validate full context reply identity and directional
sequence. Context IO is per-transfer serialized, preserving current-owner checks
and abort/seal/replay ordering while independent delivery progresses. Server
stream errors record their actual cause. The current selected direct mappings
are documented in deployment; the internal-prefix probe was a separate removed
wire experiment. The temporary receiver seal is early transport proof, distinct
from future production purpose=context upload identities, adoption and pins.
Original popup failures remain failures; identity-checked gesture observations
are separate. A no-opener popup explains its unavailable relay. Manual recovery,
visible authored errors, ended-session cleanup and download destination limits
are explicit. No sticky previous Run flag starts checks on a normal visit.


## Independent-review recovery correction

The first completed independent-review assessment requested three current fixes:
ordinary document reload lost the latest selection; overlapping renderer actions
could leave untracked frames, timers and grants; and the public full checker
incorrectly expected a healthy scenario after reopening an exact broken page.
The original reports and earlier positive/failed observations are retained.

Current accepted location messages update only the selected logical version's
trusted route with replaceState, preserving history state and unrelated URL
fields. Renderer operations use per-version latest-intent generations and a
serial queue. Close invalidates pending work immediately; late issued grants are
closed by their exact ID before discarded operations finish. Stale renewals
cannot recover or close a replacement. A persisted host whose pagehide disposed
its renderers reloads the preserved route rather than reusing a closing host.

`check_focus.py --recovery-only` selects the shared explicit recovery probe.
The native fixture accepts `--recovery-only` without changing its native guard or
registered authority. This probe holds real close/delivery responses, retains B
while A overlaps, checks current/stale renew responses, executes the public
checker's shared broken recovery helper, traverses actual authored back/forward,
and reloads the entire trusted document with exact A and explicit B selections.
The phase journal is temporary trusted tab sessionStorage, not personal state.
Only an explicit recovery_focus test route starts it; normal reader visits do
not run checks. The actual correction probe passed on primary Chrome, pinned Wails and Aside through private HTTPS. Chrome additionally passed an injected pending-owner-end case; it is not actual revocation. Nine held-response/renew subcases retain the same B delivery, binding, bytes and worker. The first Chrome driver failed only because its pageerror listener misattributed two intentional authored errors; the corrected driver preserves all stacks and requires exact renderer origin/source path to classify them. Unknown errors remain visible.

The full public browser checker calls the same `fixtureBrokenRecovery` helper:
Close/Reopen must restore a new binding for the exact broken page and its error,
then a separate explicit healthy selection must clear the error. Historical
full-download/restart results are carried only with affected-source analysis;
this correction does not claim a new full slow-matrix run.


## Independent-review ordinal 2 correction

The second completed independent assessment identified three existing-contract
gaps after the preceding correction: loading Reload lost a requested deep page;
a failed pre-replacement config refresh stranded the live renderer's automatic
renewal generation; and ordinary pushState/replaceState updates were masked by
the fixture command's explicit state message. The original findings and earlier
positive/failed observations remain separate evidence.

A view now records its requested exact selection at registration and its latest
accepted complete location. Reload/Reopen and loading Close use that common
selection. Failed config refresh restores renewal eligibility only for the same
live view and current intent; Close, newer intent, owner end and pagehide retain
invalidation. The derived loader observes successful native History API changes
without an authored SDK call, preserving receiver, arguments, return and throws.
Observer failure cannot turn a successful native call into an authored exception.
The immutable corpus bytes and persistent origin/version assignments are unchanged.

The explicit recovery probe holds actual initial A port envelopes in order,
retaining a null trusted binding/state while actual authored worker readiness is
observed. It exercises repeated loading Reload and Close/Reopen; natural eight-
second renewal after controlled503/transport failure; automatic404 retry after
failed config refresh; and stale Close/newer/owner/pagehide nonresurrection.
Renewal observation/injection is bound to exact A delivery IDs; B retains its
delivery, binding and current protected read. Normal renewal succeeds with204.
Ordinary push AND replace use no fixture state.changed message, followed by
actual document reload, back/forward and exact B logical tabs. The preceding nine
overlap cases and the public checker's shared broken recovery block are rechecked.
These are focused restoration proofs, not a new full slow download/storage/
daemon-restart matrix. Source comparisons retain exact context, storage, range,
download and native-guard implementations outside the changed location observer.


The corrected focused probe passed on actual primary Chrome, pinned Wails and
Aside through private HTTPS. Chrome passed eight rows, including actual logical
B tab interaction and a final injected-owner-end case. Wails passed seven rows,
including a real unprivileged logical B popup, and exited normally at the explicit
ninety-second bound; its closing hook closed the observed current grant with204.
Aside passed six shared rows and a separate actual logical-link click/new-tab/
close observation with the parent B grant, binding and protected read unchanged.
These counts describe different composed observations, not three identical full
runs. Current Aside high-entropy data reports Chromium153.0.8010.53 on macOS27.0.1,
mobile false. No Android or Safari execution is claimed. At that earlier
checkpoint, final independent assessment and acceptance remained required.


## Independent-review ordinal 3 pathname correction

The automatic derivative invalidates the previous page channel/pending context
at a different pathname before host metadata lookup. The existing disposing
payload supplies next_location and a fresh announcement_id. The loaded announcement,
host.connect and viewer.ready share that identity and the complete page/query/
fragment; current-source, port, generation and full binding checks remain active.
This implementation wire is an internal disposable integration seam for Task06
preparation, not a compulsory author SDK or new public management API. At this
Task04 checkpoint, Task06 was planned to reconcile the actual mechanisms after
Task05 and Task07. The [current contract record](../../docs/testing/contract-freeze.md)
records the completed selected freeze and its production limits.

`check_focus.py --pathname-only` and `?pathname_focus=1` retain the original whole
checker, including every Back/Forward assertion. Its native query-only Back after
trusted reload remains an unresolved failed observation with unknown cause.
All original whole failures remain failures. Removed experimental no-op/history
suppression/load-gate/frame-name/order variants did not fix it; no engine or
platform certification is claimed. The three-case minimal native diagnostic was
prepared but not executed.

`check_focus.py --required-pathname` selects a distinct required-pathname report
and journal, using the existing native `--lifecycle required-pathname` entry.
Its browser URL is `?lifecycle=required-pathname`. It shares twelve actual F1/
recovery/reload rows, then checks fresh exact context, ordinary query push/replace,
native History semantics, same binding/context, exact host/logical route and no
extra step. Native additionally opens a real exact-path unprivileged logical
popup; browser drivers use the real logical-reader UI. This required subset does
not execute or claim the extra Back-after-reload combination. Current Wails14,
Chrome15 and Aside13 plus separate actual Aside logical-tab observations are
composed evidence, not three full whole-checker passes.

The [current evidence record](../../docs/testing/viewer-spike.md#independent-review-ordinal-3-pathname-correction)
preserves fourteen-criterion scope, failed originals, actual/injected distinctions,
exact mechanism carry and visual/download limits. Runtime is settled, with no
live test URL. At that Task04 completion checkpoint, Task04 recorded done and
G-VIEW pass for the bounded construction; EPIC-001 remained in_progress and
Task05 then07 then06 were planned. The [current contract record](../../docs/testing/contract-freeze.md)
records their subsequent completion; whole-Epic validation and actual Master
overall acceptance remain separate.


The final independent frozen confirmation approved the unchanged candidate in
three complete reports with no findings across the original fourteen criteria.
The bounded closure combines current required observations and exact unaffected
evidence carry; it does not relabel any original whole failure or remove its
unknown Back-after-reload observation. There is no release, promoted evidence or
hardening deferral. The browser driver exit zero is distinct from the unrecorded
separate numeric Chrome-process exit; actual native normal zero and two-grant
window-closing results are retained. Runtime and owned private routes are settled.

## Whole-Epic remembered-access correction

The first whole-Epic static review found that retryable authentication failures
were treated as ended sessions by viewer consumers and native startup. Current
authorization now retains the real handler's unavailable status and Retry-After
instead of converting it to401. Existing host/origin403 boundaries remain.
A valid renderer cookie, one-use handoff or remembered bearer is retained during
an outage; successful handoff consumption still has one atomic winner.

Late context checks preserve exact bytes and reject adoption during unavailable
authority. A new context whose late initial check fails is removed before a
retry can mistake it for initialized storage. Streaming stops before further
bytes on unavailable or denied authority; an already-started response cannot
change its HTTP status and records a distinct interruption reason. Native
startup reuses a saved valid session, permits pairing after definite401, and
returns a retryable error without changing the saved file on transport/other
HTTP failure. Native guard, registered dispatch and proxy boundaries are
unchanged.

Direct Go tests use the real disposable auth-store query failure and restoration,
including cookie/handoff retention, concurrent single-use retries, late context
checks and both streaming guards. `test_host_auth.cjs` executes the complete
actual host.js in a deterministic VM boundary; its nine cases distinguish
503/transport/403 from definite401. Original browser503/transport branches fail
that test; corrected branches pass. Native startup has five status/transport/
saved-file cases. These are direct fixture tests; actual private-browser/native
observations are recorded in the [viewer evidence](../../docs/testing/viewer-spike.md).
No original whole-matrix failure or earlier review verdict is rewritten.

The current [viewer evidence record](../../docs/testing/viewer-spike.md#whole-epic-ir1-remembered-access-correction)
also records actual private-HTTPS Aside503/reload/restoration with the same
credential, real401 revocation, and native outage/restoration using unchanged
saved-session bytes. The actual three-row native check and pointer interaction
are bounded; physical Android/Safari, keyboard activation and full browser-console
coverage are not established by this correction.

## Shared Reader regression (TASK-003-03)

The existing beta.28 desktop harness also has a `--reader` mode using the
shared compiled frontend, a fixed trusted static origin and the closed Go
`ReaderService` transport. Its prepaired credential stays in Go. This reuses
G-VIEW's actual main-frame/sender guard; production onboarding, native pickers,
tray and service discovery belong to TASK-003-05.

With the repository's prepared Go/Node dependencies and native macOS SDK, run:

```sh
python3 -B spikes/viewer/check_reader_native.py --receipt /absolute/private/native-reader.json
```

The driver builds into an owned temporary directory, creates a disposable real
daemon/library and app-state ledger, and closes its processes. It checks the
raw typed facade, the actual shared Reader, a labelled seeded personal request
replayed after restart at the same origin, and forced-window-crash recovery.
The actual Reader executes local modules/Worker, external Preact 10.27.2, WASM
and an authored button handler invoked programmatically. Their live results and
large text are captured through `context_ref`; an independently constructed
expected JSON byte digest must match the server receipt. External Preact
availability is a real network prerequisite for that observation.

The native report distinguishes repeated native-message/scheme denials from
plain popups; repetitions are not distinct attack classes. Popup target mapping
and removal of privileged handlers are covered; popup DOM authentication and
measured lease expiry are separate observations. The seeded restart checks
persistence and actual UI replay, rather than a native network-loss experiment.
The report's `full_qualification` describes completion of these declared harness
stages, not the production companion, full M14 or the Epic gate.

`--stages selfcheck` selects an affected facade check and records the restricted
scope. Use a distinct receipt path for each run. Shared browser checks and
remaining Task acceptance are documented in
[the Reader implementation record](../../docs/testing/reader-viewer.md).
