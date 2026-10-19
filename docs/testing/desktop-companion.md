# TASK-003-05 desktop companion implementation record

Task: [TASK-003-05](../tasks/EPIC-003/TASK-003-05.md). This records the M1–M4
implementation and scoped native qualification on the available Darwin host.
Independent whole-Task reviews and Main delegated acceptance establish the
Task delivery boundary. Overall Epic acceptance remains pending.

## Shared facade

The separate `desktop/` Go module owns the single `readerfacade` implementation
and Darwin sender guard. `spikes/viewer/desktop` imports it through a local
module replacement and keeps the fixed-origin harness, bridge bootstrap and
selfcheck/probe wiring. The root headless module remains Wails-free. The
bidirectional `operations_parity_test.go` compares the actual Go dispatch cases
with the TypeScript public and wire-only operation declarations; a fixed count
is not its acceptance criterion.

M1 repeated the existing default six-stage native Reader harness, including
typed operations, authored JavaScript/context, evidence presentation, remembered
attachment, labelled pending-write recovery and crash recovery. Its daemon was
started by the driver. This is preservation evidence, not proof that a
production companion starts an independent daemon. The M4 entrypoint separates
the production callable surface and diagnostic observers. Its packaged-fixture,
picker, lifetime and recovery qualification is described below; whole-Task
acceptance is a separate boundary.

## Onboarding and authority

`desktop/onboarding` retains one exact default-profile selection through
protected profile loading, signed local-control discovery/verification, the
credential namespace and the explicit start offer. Missing initialization,
stopped listener, corrupt discovery, identity mismatch and profile-read failure
are distinct states. The start offer uses only the configured absolute sibling
binary with the same profile/data/volume/port selection. It detaches the child,
uses independent stdio and reaps it asynchronously; it does not install,
initialize, migrate or stop the service.

The real native `pair` response contains only request ID, comparison code and
expiry. Go retains the polling secret and redeemed bearer. An approved poll
returns a reduced session identity, followed by explicit `adoptPairing` for the
still-owned attempt. Adoption verifies the product API and library binding,
persists a protected credential, and installs the captured endpoint/bearer pair.
Attachment checks the signed binding before sending its bearer and requires
session, capabilities and the expected library to agree before issuing an
attachment. Late attach results cannot replace a changed attachment era.

Stop, replacement, disposal and adoption use captured attempt generations.
Stopping before commit prevents activation; stopping after commit reports
already-adopted. A pre-publication store failure grants no authority. An error
after credential replacement with exact readback remains committed with
`durability: unconfirmed`; unreadable/indeterminate readback reports
`NATIVE_CREDENTIAL_STORE_UNCERTAIN` without claiming an empty store or activating
the candidate. Remembered boot validates profile, library and endpoint together.
Logout clears active authority and retains a retryable local-forget obligation;
cleanup cannot unlink a newer credential.

The shared Connect screen omits the browser Remember control in native mode
and explains protected persistent storage. It shows the service endpoint,
discovery state and explicit start offer. Native polls send only a request ID;
the browser transport still requires its own real polling secret. Stop displays
its actual outcome, and a stopped/unmounted flow cannot adopt a late response.

## M2 C2 foreground and pin-error corrections

Two required corrections came out of the independent M2 result review. First,
the shared App's foreground re-verify (`shell.tsx` visibility handler) reached
a native attach with no Go credential and mapped `NATIVE_NO_CREDENTIAL` — and,
after another device revoked a remembered credential, a repeated
`SESSION_REVOKED` from the retained dead token — through the full clearing
path. That invalidated the transport era on every recheck and discarded a
still-owned pending pairing poll as stale, ending it as a lost issuance and
forcing a fresh pairing. The controller now treats re-observed
`NATIVE_NO_CREDENTIAL`/`SESSION_REVOKED`/`SESSION_EXPIRED`/
`NATIVE_SESSION_LOGGED_OUT` as idempotent only while no effective
JavaScript-side authority exists (no live attachment, bearer token or stored
credential): the run retires and the honest unauthenticated notice stays, with
no era invalidation, disposal or store clearing. The first loss of real
authority, explicit logout, revocation of a live attachment, genuine library
and generation boundaries, and the local-cleanup guard on reentrant attaches
are unchanged and still invalidate fully.

Second, permanent Go binding pins (`NATIVE_API_INCOMPATIBLE`,
`NATIVE_LIBRARY_MISMATCH`, `NATIVE_CREDENTIAL_MISMATCHED`) now surface as the
blocked `incompatible` phase with the existing typed pairing notices (also
shown by the shell's incompatible panel/banner instead of the fixed generic
text), never as a transient recovering service. Network failures and generic
service errors keep the degraded/recovering mapping, and no pin error grants
effective authority or forces a new pairing.

Because one foreground event also makes Connect's own visibility listener
resume polling while the App re-verifies, a held pairing attempt can observe
an overlapping approved reply. Connect now keeps per-attempt adoption
ownership: exactly one adopt/attach owner per pending attempt, with Stop,
replacement and unmount behavior unchanged.

Regressions: Node controller tests drive the real `NativeHaeryongClient` era
machinery with held polls — a no-credential recheck and repeated revoked
rechecks leave the new pairing poll owned (single pair, single adoption,
verified attach), while the first revocation still disposes, invalidates the
captured authority and stales the old era's in-flight poll; the three pin
errors assert the typed incompatible notices with blocked writes and no
pairing; a logged-out recheck keeps the pending local-cleanup obligation. The
browser driver mounts the real production `App` (its own visibility handler)
over the labelled synthetic port: it prefers a real tab switch and, when
headless Chrome emits no real visibility events, dispatches a clearly labelled
`visibilitychange` DOM event through the real App handler — physical/native
focus is never claimed. The receipt records which visibility route ran.

## M3 native filesystem adapters

The ten new closed operations above live in `desktop/readerfacade` behind the
same single `Invoke` binding; the presenter seam (`SetPickerPresenter`) is the
only panel wiring; the M4 entrypoint installs real Wails presenters. Sources are
enumerated once with full identity capture (dev/ino via `os.SameFile`, exact
size, nanosecond mtime, kind, link text), paged to JavaScript as local cursor
metadata, and read back through `readSourceRange` with pre/post identity
re-verification per chunk; `sourceVerifyStable` re-stats every entry without
reading bodies. Hidden files, arbitrary Unicode names, represented empty
directories and symlink text are preserved exactly; a name or link target that
cannot survive JSON transport fails `NATIVE_PATH_INVALID` before any
replacement. Changes to captured identity answer `NATIVE_SOURCE_CHANGED`; the
honest bound is that a deliberate same-inode/size/mtime restoration is not
detectable and no OS-level atomic snapshot is claimed. The completion gate
(`verifyInputStable`) runs only before the FIRST completion intent
(`completeCheckpoint === null`), before anything is captured or persisted; a
stored-checkpoint recovery replays the same S with no input or handle, and a
known settlement resets the gate with the new S.

Destinations anchor to the selected directory's `os.Root`: bytes go only to a
private 0600 sibling temp with an attempt-unique nonce (a crashed attempt's
orphan never blocks a retry and is never glob-deleted), `destClose` fsyncs,
verifies the delivered validator when present, then publishes through an
FD-anchored `RenameatxNp`/`Renameat2` no-replace rename (relative names plus
the directory FD from `Root.Open(".")`), so a replaced parent path can never
retarget the publication and an existing target is a typed conflict. Explicit
replace requires the owner's consent through the shared conflict flow.
Post-publication directory-fsync failure is the success warning
`durability: unconfirmed`; an unobserved close is `NATIVE_CLOSE_UNCERTAIN`
with retry only through a new attempt's existence/conflict path. Disposal,
window close, activation and explicit release detach handles from the era map
and then drain each handle's operation lock, so a publication that
linearized first completes on live resources and a release that linearized
first leaves the racing operation a typed stale answer with no filesystem
effect; late picker results cannot register after their lease died.

The TypeScript side keeps ONE engine: `PlannedEntry.file` became the
structural `FileLike` (browser `File` satisfies it natively; the native
planner supplies lazy 1MiB-bounded ranges), the download target's
`openWritable(intent?)` carries the consent `replace` flag and the stream's
`expected` validator, `close()` may return an honest `{warning}`, and the
shared ImportControl/DownloadPanel gained native acquisition branches with
native-truthful consent copy while the browser flows and their notices stay
unchanged. The browser regression drives the real production App screens over
a labelled in-memory synthetic filesystem — that is React/adapter wiring
evidence, not actual Wails picker or secret-custody proof (M4).

## M3 C2 failure-path and lifetime corrections

Five review findings closed within the same direction. (1) A target that
appears between the exact probe and publication surfaces the explicit
conflict recovery (`NATIVE_DESTINATION_CONFLICT` at close routes back to the
conflict stage; only the owner's consent replaces). (2) A failed abort is
never silenced: the shared engine preserves the typed cleanup failure beside
the primary incomplete outcome with the exact surviving temp path; Go keeps
an unresolved abort retryable (`destAborting`) until the own-temp removal
actually succeeds, and only then is the attempt done (terminal tombstone,
anchored root closed) — publication likewise closes the root, while a
pending conflict decision keeps the exact selection anchor open for the
owner's explicit replace. (3) ANY unobserved close reply (network loss,
unreadable committed body, stale era, transport failure) classifies as the
typed `NATIVE_CLOSE_UNCERTAIN` with retry only through a new attempt's
existence/conflict path; typed Go answers stay definitive. (4) Late choices
after a panel left the screen never write, publish or resurrect UI: pick
epochs + StrictMode-safe mount guards gate both panels, the previous
selection is discarded (awaited, retry-on-failure) BEFORE a new chooser
opens so a second fresh handle is never created while an old obligation is
unresolved, and late source selections release their handles. (5) A
successful re-attach (the real facade attach over session/capabilities) is a
filesystem-era boundary: pending picker leases die (`fsEpoch`), and the
previous attachment's handles are detached and drained (roots closed,
unfinished temps cleaned). When a discarded destination's cleanup fails
after the owning panel is gone, the typed path routes to a small
runtime-only controller cleanup alert (deduplicated exact paths, honest
unknown-outcome wording, informational dismissal, cleared at every authority
transition, dropped for replaced authorities) rendered independently of the
auth banner.

Selection replacement also preserves one owner within a mounted panel. A
synchronous destination flight covers selection acquisition and the complete
download effect; repeated chooser or replace clicks cannot create another
handle, start a second transfer or reset an active cancellation. An internal
effect call reuses its acquisition owner's flight. Choosing a different name
also owns that flight throughout the previous selection's abort; both conflict
actions stay disabled until cleanup finishes. A delayed abort can clear only
its exact owned selection, and its response must still own the mounted intent
before changing the screen. Cleanup failure keeps the existing path and retry
obligation. Every new browser import
input, including an empty selection, invalidates an older native selection.
Native selection completion rechecks its intent after awaiting the previous
handle's release, and stale success and error replies cannot replace the
current input or clear another flow's handle or pending state.

The browser regressions hold the exact previous-handle release after a new
handle was installed, then exercise browser reselection and actual shell
navigation before resuming it. Separate cases cover an empty input, a late
fixture error and duplicate destination clicks with exactly one chooser,
open and close. A held abort reply also verifies duplicate discard and replace
rejection, then preservation and unmount cleanup of the next selected handle.
Browser folder changes use real disposable fixture-directory
FileLists; the NativePort and its source/destination replies remain labelled
synthetic state. These checks do not establish actual Wails panel behavior.

## M4 application implementation and scoped native qualification

`desktop/cmd/companion` now builds the thin production application. It serves
compiled native frontend assets on the fixed `127.0.0.1:9271` origin, refuses a
port collision without selecting another port, checks the exact Host and any
supplied Origin before serving bytes, emits no CORS permission, and marks all
responses `no-store`. SPA entry delivery also works at `/` and trailing-slash
routes. Both the WebKit sender guard and the closed raw-message dispatch remain
active: the trusted main frame, exact origin, `haeyong.native` kind, zero object
and method, empty method name, and the single `Invoke` ID are required.

The production binding receiver exports only `Invoke`. Production construction
has no receipt-file observer or probe/report flag. Tagged
`haeryong_test_state` builds add explicitly enabled diagnostic observers and
reporting; `haeryong_native_automation` additionally requires that state tag.
The automation seam uses public AppKit operations against this application's
own window, status item, menu and installed panels. It is absent from the
untagged application. Tagged traces observe selected real Invoke calls and
lifecycle events, excluding bearer values, source paths, authored content and
chunk bytes. They support checks; they do not themselves establish a passed
native scenario.

Remembered attachment loads the signed binding and the stored endpoint/bearer
pair together. The facade checks ownership before the load and again before
installation, including independent logout/forget state; late loads cannot
replace adoption, logout or disposal. Deterministic tests cover distinct old
and newly adopted credentials, both loads waiting before one installation,
server revocation versus a still-valid credential, and diagnostic observer
absence in production construction. The shared shell also keeps Connect and
the explicit start offer reachable when an initial native boot has no session
or capabilities and the daemon is stopped. Its browser regression uses the
labelled synthetic NativePort; it is not real native activation evidence.

The application installs Wails directory/file open panels and save panels
as sheets attached to its trusted main window.
Window close hides the window; the status menu offers reopen and companion
quit. Quitting closes companion-owned resources and requests application exit;
it does not stop the daemon. The existing sibling-binary start offer still
owns no service data or lifecycle. The scoped native observations below cover
normal quit, remembered attachment, and the native filesystem qualification
scenarios.

### Automatic native filesystem qualification

A tagged build (`haeryong_test_state` plus the picker-fixture flag) drives
the real Wails `Invoke` through the real Go facade, filesystem, shared React
transfer/download engines and the real daemon/storage. The picker-fixture
presenter supplies explicitly labelled test selections — these are tagged
fixture choices, not owner OS selections. Real owner-operated OS callbacks
established directory-to-ZIP (earlier scoped run) and archive/single modes
(separate owner runs: one checker failed after the actual callbacks and
publication, another was intentionally stopped with only post-verification
passing; the actual native-mode behavior — archive planning, single-file
transport entry, exact API/tree/kinds/link/empty-directory/raw-bytes/
captured-project/null-base — is established by those genuine observations
together with the current tagged qualification, while whole-run checker
statuses are honestly preserved as not fully passed). The tagged scenarios:

- **Cancel** — the actual UI cancel button during a held real `destOpen`
  reply drives the real engine to observe cancel and call real `destAbort`:
  successful cleanup, incomplete display, no final target, no surviving
  temp.
- **Cancel with cleanup failure** — an owned parent directory is chmod 0555
  so the abort cleanup genuinely fails with `NATIVE_CLEANUP_FAILED` and the
  exact surviving temp path visible in the actual incomplete alert; the
  Retry button returns to acquire, and the next actual save-location
  selection triggers the same-handle `destAbort` retry before a fresh
  scripted choice; cleanup then succeeds, the temp disappears, and the fresh
  selection completes with exact payload bytes.
- **Destination conflict** — existing target bytes remain until the observed
  UI conflict and explicit replace consent; the final file has the exact new
  bytes.
- **Inaccessible path** — owned permission revoked after real selection and
  before actual start; the real `readSourceRange` returns its typed error
  surfaced in the UI.
- **Source mutation** — same-size content with changed mtime after real
  planning; the real `readSourceRange` captured file-identity check fails
  before the first-S stable gate is reached, surfacing the typed
  changed-source failure.
- **Validator missing** — a labelled synthetic response-level fault removes
  the fileStream validator (normal backend always supplies it): the UI shows
  the verification-information missing explanation and no `destOpen` occurs.
  This is a defensive-fault check, not a claim about the normal backend.

Gaori-gated automatic native filesystem proof passed on this available
Darwin host (the 50 SDK/linker warnings are known and degraded/truncated
in the builder); no all-platform or release guarantee is claimed.

### Accepted-job survival and first-S recovery

A focused actual native scenario established the accepted-job and first-S
recovery obligations: the pre-kill authenticated observation showed the seal
job queued (nonterminal); the first authenticated exact-job query after the
companion's kqueue-observed SIGKILL also showed it queued with no
intervening API call; sealing completed while the app was absent. The first
accepted completion-S native reply was surgically suppressed after Go
accepted it, with the actual stable response, call identity and original
S/C/U/job recorded. A same-bundle/origin/state restart drove the real
shared React recovery with the original S, stored checkpoint and I — no
chooser, reselection or `sourceVerifyStable` after restart — and the
original 202 replay linked the same seal job with exactly one seal job and
one publication. Authenticated range hashes covered the full payload with
exact final seven-entry path/kind/hash/bytes/symlink/empty-dir/hidden/
Unicode verification.

### X07 raw isolation, authored-error and stuck-recovery

A tagged observational fixture build running the actual companion entrypoint
and shared React on a real independently started daemon passed all six
stages: setup, corpus publish through the public CLI importer, X07 attacks,
authored broken-page error, stuck-page recovery and safe restart. An
authored child frame sent eight well-formed native `Invoke` envelopes
(including source/destination choosers and a management attempt), six
native-scheme fetches and a forged host message: the guard denied exactly
eight native messages and six scheme requests, and the exact public Reader
popup route with page/query/fragment opened. Five raw main-frame negatives
were silently dropped by the Go whitelist (they do not grow Objective-C
counters); 701101 under enabled diagnostics is allowed, not a negative.
Guard sender-row capacity was already filled by main-frame messages, so no
child sender row is recorded — the cap flag honestly reports this. Parent
DOM and storage reads were denied; the actual module, Worker and authored
button click executed. An authored `throw` produced the real
`AUTHORED_SCRIPT_ERROR` page-error notice, and the host then rendered a safe
sibling page. No authored-child attempt reached a tracked privileged
filesystem dispatch; the protected credential and all seven stored file
bytes were unchanged.

An authored stuck page self-clicked into a proven straight-line infinite
loop (a nonce-correlated collector marker delivered before the loop proves
loop entry). The scheduled trusted-host capabilities probe had no observed
post-loop terminal or response — rendering was blocked before the host
script could proceed, so actual dispatch to Go is **not** established and
no failed/returned Go request, automatic stuck notice or responsive host
controls are claimed. The public own-process AppKit tray Quit succeeded
with kqueue-observed normal exit 0 (no forced kill), and a same-bundle/
origin/state safe restart verified remembered attachment with the same
session, no new pairing, unchanged credential, and the specific completed
import-job record, document inventory and all seven stored file bytes
intact.

Scoped native qualification on this available Darwin host is established.
The scoped native contributions — tagged automatic filesystem scenarios,
X07/authored-error/stuck-recovery, accepted-job survival and first-S
recovery — have each passed their focused TaskJudge and two independent
Worker reviews, with the X07 stuck-host concern (unobserved Go dispatch)
accepted by all four reviewers as meeting the original documented
app-restart fallback. The dedicated Task Judge and Epic Judge approved the whole-Task result,
and Main accepted it after the final project/product review. Scoped agent-operated
packaged-fixture checks on macOS 27.0.1 ARM64 and Wails beta.28 have established:

- The actual shared UI start offer starts the selected independent daemon.
  Actual window close hides it, the real status menu reopens it, and the real
  companion-quit item terminates the exact app PID with observed exit 0. The
  daemon retains its PID, signed instance identity and held writer lock after
  each transition; compiled browser assets and an independently authenticated
  device's session/library HTTP routes remain reachable.
- A real UI-created pairing is approved through the protected CLI. In the
  originally occluded fixture, explicitly labelled setup calls use the real
  closed Go `pollPairing`/`adoptPairing` path; secrets never enter JavaScript.
  Restarting the same app/origin/state then exercises the actual shared React
  bootstrap: verified attachment, the same session, a positive remembered
  activation observation, no new pairing, and an unchanged credential file.
  That scoped result does not establish native foreground Connect automatic
  polling. Later trials did observe visibility change and native UI adoption;
  the fixture distinguishes actual UI ownership from setup calls rather than
  relabelling the earlier evidence.
- After verified remembered attachment, the actual native tray quit terminates
  the authenticated application with observed exit 0. Its daemon and session
  remain alive. A further restart of the same app/origin/state again verifies
  the same session through the shared React bootstrap, with a positive
  remembered-activation observation, no new pairing and an unchanged protected
  credential. This is attachment/lifetime evidence, not transfer or accepted-job
  survival evidence.

The initial direct-binary and LaunchServices attempts reported an inactive,
occluded WebView despite an attached visible window. The cause remains
unestablished. LaunchServices also refused an external-volume stdout path,
and that execution context waited in directory creation before diagnostics.
An owned, private normal user-system-temp fallback permits the packaged app
and isolated runtime to initialize; build artifacts and reports remain in the
preferred mounted-volume scratch area. No host privacy settings or permissions
were changed. LaunchServices metadata/cache writes are automatic OS effects.
Actual non-child application exits are observed with public kqueue APIs,
separately from the launcher exit. A Go `Run()` return is not required for
normal Cocoa termination; forced exits never substitute for real tray quit.

Automatic file-picker completion through the tested public AppKit actions
remains unqualified. On this OS the declared `NSSavePanel ok:` action raised a
runtime exception. Public installed-button and own-panel key-event attempts,
including attached sheets, did not produce a selected-path result. An
action-dispatched observation is not a completed selection.

A separate, explicitly owner-operated packaged-fixture run completed the actual
directory chooser and ZIP save panel. Real callbacks created Go-owned handles;
the shared React import published the selected directory to its captured
project with a null initial base, and the shared download engine saved the
archive. All seven entries matched the selected tree and exact file bytes,
including a hidden binary file, nested Unicode paths, an empty directory and a
symlink. Authenticated raw-file delivery also matched the file bytes. The same
run verified remembered React attachment without a new pairing and closed its
owned app and daemon. This is directory-to-ZIP success evidence with real
owner OS callbacks, not automatic panel completion or a human usability gate.
Separate owner runs observed archive and single-file picker modes: one checker
failed after the actual callbacks and publication had completed, another was
intentionally stopped with only its post-verification passing. The actual
native-mode behavior is established by those genuine observations together
with the tagged automatic qualification above; the whole-run checker statuses
remain honestly labelled. No selected URL, callback or handle was fabricated.

The accepted-job-survival and first-S recovery results are recorded in the
[dedicated section above](#accepted-job-survival-and-first-s-recovery).
Corpus retry budgets are experiments, not product limits.

The asynchronous logout regression also waits for the observed best-effort
revoke completion during test cleanup, before fixture directories are removed.
Its main assertion still requires the local-forget error to return while the
remote revoke is held; production logout does not acquire a new wait.

## Verification and limits

Observed M2 checks on macOS 27.0.1 ARM64, Go 1.27.1 and pinned Wails
`v3.0.0-beta.28` include the complete desktop race suite and frontend Node tests.
The Go suite covers discovery identity, captured-target races, save/readback
outcomes, logout/relogin, attachment ownership and the operation-set parity gate.
The existing SDK deployment-target/linker warnings remain visible.

The browser driver `internal/adapters/http/auth/browser/check_native_connect.py`
uses the installed Playwright CLI and actual Chrome over the labelled synthetic
NativePort harness. It exercises keyboard request/Stop, an approved poll held
across Stop, typed store failure, uncertain Stop, already-adopted truthfulness,
relogin and explicit service-start display, the M2 C2 real-App foreground,
repeated-revoked-credential and pin-error regressions described above, and the
M3 shared-panel native journeys (ZIP download with durability warning, existing
target conflict through explicit replace consent, and the native import
selection planning the shared input) over the labelled in-memory synthetic
filesystem. It checks page/console health and closes its owned browser/server
before removing successful scratch state. Supply `--browser-cli`, `--receipt`
in an existing private directory and optional `--screenshot`. This is actual
React interaction with a synthetic transport, not actual Wails, secret-custody
or daemon-start evidence.

A late-arrival projects regression in the same driver holds the harness
`listProjects` reply before the Library mounts: with the import panel open on
an empty project list the genuine start stays disabled, the arrived default
target fills exactly once (an explicit owner choice or a preselected target is
never reset), and the dispatched `createUpload` carries the exact arrived
`target.project_id` with `document_id: null`. The synthetic port answers that
dispatch with its labelled `501 FEATURE_NOT_READY`, so the capture proves the
dispatch target without inventing a transfer outcome.

Frontend changes require `python3 -B frontend/check_build.py --regenerate`
followed by the normal command (the Node runner includes
`src/client/native-fs.test.ts`). Relevant module checks are
`go -C desktop test -race -count=1 ./...`, `go -C desktop vet ./...` and the
separate spike module race suite. The complete root `make test` passed on this
host (17.6 minutes); an earlier run with an unknown-cause authentication
failure is preserved without a cause-fixed or known-harmless claim, followed
by focused failure-retry and two-owner diagnostics that both passed.

Native qualification on this available Darwin host established the
automatic tagged filesystem scenarios, the X07/authored-error/stuck-recovery
contribution, accepted-job survival and first-S recovery, remembered
attachment, companion-started daemon survival, and owner-operated
directory-to-ZIP with actual OS archive/single callbacks, publications and
subsequent exact verification (whole-run checker statuses preserved as
historical not-pass). The scoped native contributions have each passed
focused independent reviews. Independent whole-Task reviews and Main delegated
acceptance establish this delivery boundary. Task006 owns the assembled responsive/cross-client reader gate; EPIC-007 retains the
private physical-device workflow. No owner usability study or user acceptance
is represented by these agent-operated observations.
