# Haeryong shared frontend shell

This directory holds the shared React/TypeScript trusted shell, Home/Library
and result-transfer controls, the shared Reader, and their transport adapters. The compiled production bundle under
`../internal/webassets/dist` is committed and served by the Go daemon; no
development server and no service worker/private asset caching are involved.

## Layout

- `src/app/` — shell, registry router, connect screen, theme tokens.
- `src/app/screens/` — Home/Library, shared import and streamed download, and
  Settings/Storage upload discovery/recovery. The preselection harness is a
  separate verification entrypoint, excluded from the production bundle.
- `src/client/` — `HaeryongClient` contract, browser HTTP transport, narrow
  `NativePort` boundary (the production desktop binding ships with TASK-003-05),
  credential stores, and the connection state machine.
- `check_build.py` — validation/regeneration entrypoint (see below).
- `src/viewer/` — unprivileged page loader, immutable incremental snapshot,
  active-page channel, trusted host context transfer, render-session lifetime
  and the isolated Reader frame. TASK-003-03 owns the shared Reader and its
  bounded native harness integration; the production companion remains
  TASK-003-05.

## Dependency preparation (explicit, once)

```sh
cd frontend && npm ci --ignore-scripts
```

Preparation installs the exact `package-lock.json` pins with lifecycle scripts
disabled. Validation never installs dependencies; it fails with instructions
if `node_modules` is absent.

## Validation and regeneration (no installs)

```sh
python3 -B frontend/check_build.py              # typecheck, unit tests, scratch builds,
                                                # native/browser bundle separation, drift check
python3 -B frontend/check_build.py --regenerate # rebuild committed internal/webassets/dist + manifest
```

`--regenerate` is the only mode that writes into the repository. Run it after
any frontend source change and commit sources and `dist` together in the same
task commit: the canonical Go test in `internal/webassets` recomputes the
recorded input digest without Node, so `make test` fails if sources and the
committed bundle drift apart.

The browser entrypoint build must never contain native-bridge markers
(`wails`, `window.runtime`, `@wailsapp`, `NATIVE_TRANSPORT_NOT_IMPLEMENTED`);
the separate native entrypoint build (`npm run build:native`) is used by the
reused TASK-003-03 Wails harness and the later production companion. It is not
embedded in the daemon. The browser bundle remains independent of Wails.

The separate `vite.loader.config.ts` builds a self-contained page integration
IIFE at `internal/webassets/dist/renderer/loader.js`. Both regeneration and
normal validation include it; normal validation compares a fresh loader build
and the trusted browser bundle with the same committed manifest. The renderer
adapter delivers this script under its narrow cookie boundary. It carries no
management credential or native bridge. See the current
[Reader implementation record](../docs/testing/reader-viewer.md) for scope and
unfinished acceptance work.

## Daily scripts

- `npm run typecheck` — `tsc --noEmit`.
- `npm run build` — typecheck plus the browser bundle into
  `../internal/webassets/dist` (use `--regenerate` above to refresh the
  manifest; a plain build does not update `assets.manifest.json`).
- `npm run test` — `node --test` adapter/controller/transfer/download/router suites (normal exit; no
  force-exit).

## Authority-era contract (summary)

The controller binds the transport credential and starts an authority era on
credential, library or runtime-generation changes only. Routine
same-authority rechecks keep the current era so in-flight writes are not
discarded, keep the mounted screen state, and record uncertain writes
(original K operation identity or natural `session_id` target) until a
receipt/current-state reread resolves them under current authority.

## Result-transfer browser regression

After preparing the pinned frontend dependencies and an installed Playwright
CLI/browser, run the real auth/SQLite/import-worker fixture and compiled shell:

```sh
python3 -B internal/adapters/http/auth/browser/check_transfer.py \
  --browser-cli /absolute/path/to/playwright-cli \
  --receipt /private/existing-directory/result-transfer.json
```

The driver builds only into its private temporary directory. It creates and
closes uniquely named browser sessions and disposable profiles; it does not
change a user profile or install dependencies. Host-write scenarios transparently
pre-grant write permission to their loopback origin, then exercise trusted
host-directory drop and real writable streams. This does not qualify system
save-dialog UX, normal permission prompts, Wails or physical devices. Response
loss and labelled HTTP/transport faults are explicit test controls. Source
semantics and executed qualification limits are in
[the result-transfer record](../docs/testing/result-transfer.md).

## Reader browser regression

```sh
python3 -B internal/adapters/http/auth/browser/check_reader.py \
  --browser-cli /absolute/path/to/playwright-cli \
  --receipt /private/existing-directory/reader.json
```

This builds the current tagged daemon into a short, private scratch directory,
pairs a disposable owner and imports a complete multi-page fixture. It exercises
the compiled shared Reader and its real iframe/channel, ordinary scripts, local
modules and Workers, external Preact from `esm.sh/preact@10.27.2`, an exact large
context digest, child navigation/history, host selection, independent personal
resources, Continue, public-browser popup routing and the exact-document Import
control. External framework unavailability fails the check; it is not silently
counted as passed CDN coverage. It does not qualify Wails or private ingress.
The driver closes its browser and daemon and removes its own private state on
success; a failure receipt identifies retained scratch state for diagnosis.

## Reader evidence browser regression

```sh
python3 -B internal/adapters/http/auth/browser/check_reader_evidence.py \
  --browser-cli /absolute/path/to/playwright-cli \
  --receipt /private/existing-directory/reader-evidence.json \
  [--screenshot /private/existing-directory/evidence-panel.png]
```

TASK-003-04's evidence counterpart imports a plain directory plus a same-shape
directory with exact supplied metadata (an inherited-key evidence id and the
same region name on two pages). Against the real daemon it exercises the
VersionHeader identity, keyboard and emulated-touch activation of the changed
controls, authored/service separation, honest 503 future capabilities and the
disabled source-check; a labelled local seeded fixture backend proves seeded
assessment/verification/coverage presentation including later-cursor failure
and retry, presentation only. [The evidence record](../docs/testing/evidence-presentation.md)
owns the fixed contract and limits.

## Native session and JSON adapter foundation

`ShellController.attachNativeSession()` accepts a nonsecret Go attachment
identity, then verifies the real current session and capabilities through the
native client. Browser bearer binding is rejected by `NativeHaeryongClient`;
no token sentinel is used. Routine verification keeps the attachment, current
401s dispose it. Explicit logout clears Go's usable credential and attempts
local-file removal before seeking server revocation; disposal retains the
captured cleanup identity. The adapter is connected to the existing beta.28
Wails ReaderMode harness through its closed Go operation switch and guarded
bootstrap. A failed local forget is visible and retryable after detachment.

`client/native-wire.ts` turns the closed JSON reply `{ok,value}` or
`{ok:false,error:{status,code,retryable,retry_after?,details?}}` into typed client
results. Upload chunks carry `bytes_b64`. Package/file opens return `stream_id`;
`streamRead` takes that ID and `max_bytes` and returns nullable `data_b64` plus
`eof`. The adapter consumes a nonempty final EOF chunk before closing and
cancels only its captured stream. Subscription opens return `subscription_id`;
`eventsPoll`/`eventsClose` reconstruct a local liveness/change-hint handle, not a generic
API subscription authority. Unit tests force JSON round trips, arbitrary
0–255 byte hashes, final EOF bytes, typed failures and late-read cancellation.
These unit tests isolate adapter behavior. Actual Go/Wails execution through
the same compiled Reader, closed operations, bootstrap, sender guard and stream
path is covered by the [native Reader regression](../spikes/viewer/README.md#shared-reader-regression-task-003-03)
and [Reader implementation record](../docs/testing/reader-viewer.md). Production
desktop onboarding and native file dialogs remain with TASK-003-05; the wider
responsive and cross-client gate remains TASK-003-06.

## Static Library search (TASK-004-02 M5)

The explicit `features.search` capability enables the labelled Library form.
`search(SearchRequest): Promise<SearchPage>` is a named HTTP/native operation;
empty q, all filters, cursor and limit are forwarded without retargeting. Native
responses use the existing era guard. Required safe `index.reasons` remain intact.
`search-state.ts` owns request/dispose fences, single-flight more, cursor reset,
reason labels and exact once-encoded page URLs. The shared UI uses text nodes,
URL filters and bounded GET progress polling; no job-resume or maintenance action
is added. `search.test.ts` and `search-state.test.ts` run in both explicit TS test
lists. The owned Chrome driver is `internal/adapters/http/auth/browser/check_search.py`;
the pinned native harness adds `--stages app,search`. Seeded reason/fault
presentation is labelled separately from production extraction and eligibility.

Invalidation hints always start a fresh authenticated search GET, including
when multiple result pages have accumulated. Starting that GET clears old
protected rows and pagination state. The registered login-return `q` preserves
encoded TAB/LF/CR accepted by the search grammar; other controls and other
query keys retain their existing restrictions. The Chrome regression exercises
55 accumulated rows, a content-free `library.invalidated` notice, immediate
row/cursor clearing and the revalidated hidden state, plus three login returns.

The same Chrome driver accepts `--corpus` for complete checked-in Mulgae/Gaori
imports and exact actual-frame search/open proofs from both projects. The native
counterpart is `--stages app,corpus`; `tests/manual/check_search_corpus.py`
coordinates these examples with the fixed HTTP/CLI worksheet and seeded variants.
All eight canonical sample files round-trip unchanged; ignored tool state is not
sample content. The corpus helpers add no product hook or new frontend assets.

## Owner-records composition (TASK-005-06)

The explicit browser `check_owner_records.py` and standalone native
`check_reader_native.py --stages owner-records` exercise existing host controls.
Their [record](../docs/testing/owner-records.md) separates actual actions from
supporting setup and describes current qualifications. The shared Notes repair replaces synchronous confirmation with captured trusted
HTML consent and guarded local replacement; browser assets are regenerated with
those inputs. The common Sheet API and persisted/API contracts are unchanged.
The completed owner-records campaigns and Task checks have the bounded scope
recorded there; they do not qualify a physical device or every companion stage.

Consequential Notes actions require current verified owner authority. Existing
synchronous scope/owner guards succeed before save activity is shown, so a new
already-disconnected activation creates no false save-pending presentation. Local
draft, namespace, unmount and original receipt ownership remain unchanged; the
[current R35 record](../docs/testing/ai-workflows.md#honest-disconnected-notes-correction)
separates actual browser/common proof, independent member review and the later
whole-Epic acceptance boundary.

## Saved-context presentation boundary

NotesPanel currently retains complete saved-revision text in ordinary inline
grapheme-boundary spans in a labelled, focusable, noneditable wrapping viewport.
Start/End use mounted native text offsets; Select/Close remain reading controls.
A separate owned context read lifetime cancels/fences obsolete reads without
editing the Notes journal or consent. No Copy override or normalized replacement
is used. The three named terminal workloads record rendered strings separately;
all selection intervals require exact associated Range text/global offsets and
all other strings remain strict. Finite mounted-component browser cases cover
read cancellation, supersession, scope retirement and unrelated typing/resize.
The standalone Wails owner campaign completed its selected saved-context,
consent and recovery cases. Broader native qualification retains its stage
boundaries; see [owner records](../docs/testing/owner-records.md).

## Provider status

TASK-006-03 adds Settings/AI at /settings/ai. Both /settings and
/settings/general retain the existing General placeholder with an explicit AI
link and the existing /connect owner-tools action. AI also links back to General
and Connect. General entry does not request provider status; that operation is
limited to explicit AI entry/refresh. General/Sessions editing remains future
work.
HTTP and the closed native providerStatus operation share its credential-free
status. Keyboard/touch refresh, unknown allowance, auth/quota failures and
provider-independent Reader/Review behavior are covered by the provider-only
browser campaign. At the TASK-006-03 transport checkpoint,
features.questions remained false; transport installation alone did not enable
question admission. Current generation and Ask delivery are documented in
[generation workflows](../docs/testing/generation-workflow.md) and
[saved questions](../docs/testing/saved-questions.md). See
[the implementation record](../docs/testing/provider-adapter.md).


The provider correction uses the closed ProviderState union in both transports.
Unknown states/missing usage allowance fail safely; nullable usage is unknown,
and explicit false remains unavailable rather than becoming zero allowance.
Bounded reason/authentication labels distinguish paused route/mode refusal from
confirmed exhaustion. Refresh retains focus with aria-disabled/busy suppression,
a stable polite region announces state, and Settings links reuse SPA navigation
and existing 44 px targets. The browser observer correlates each expected exact
supplied-metadata GET 404 to one device/request/response; unrelated query/method,
status, reason and duplicate reuse controls fail. See
[correction evidence](../docs/testing/provider-corrections.md).

## AI actions during disconnection

Generation, Draft and Ask consequential controls require the current verified
owner session and their generation/questions capability. Existing dispatch and
captured-authority guards remain authoritative. A same-authority outage keeps
unsent editor state and original recovery records; reconnection confirms both
session and capabilities before those controls become available again.

The Reader retains its last confirmed authority nonce only as a mount identity
during that outage. Actual session/owner/library/runtime or newly confirmed
authority changes still retire the previous Reader. A failed renderer renewal
retires that delivery; only an explicit API refusal closes the protected host
Reader. Network or dispatch-gate failure leaves unsent host edits available.
Sources directs the owner to AI settings for provider readiness without probing
an account on Sources entry. See the
[assembled correction record](../docs/testing/ai-workflows.md#whole-epic-client-availability-correction).

## TASK-007-03 responsive workflow boundary

The phone sticky row contains only the existing Files/Context toggles. Version,
default-page and import actions remain in normal flow; the same native-dialog
sheets and viewer/channel survive resizing. Shared inputs/textarea/disclosures
and cover surfaces receive bounded typography/44px/safe-area adjustments. Ask
progress is outside the conditional draft. The shell retains outage history
across keyed owner-screen replacement so Provider does not auto-probe on
reconnection; initial healthy AI entry and explicit refresh retain their roles.

The existing private driver selects Task03 with `--task03-only`; optional
`--task03-stages` runs affected independent stages without Task01/02 cohorts.
[The current record](../docs/testing/mobile-reader-workflows.md) separates real
Chrome/Tailscale transfers and controls, HTTP presentation peers, focused checks,
failed collections, the separately approved completed one-call partial Ask and
unmeasured physical/native routes. Input, inherited-capability and cleanup
qualifications remain explicit; member closeout is still pending.

## TASK-007-04 multi-device reconciliation and conflict UI

Background invalidation, `resync.required` barrier sweeps, `visibilitychange`
rechecks and background status polls use `controller.passiveClient`
(`client.passive()`), sending `Haeryong-Passive-Read: 1` on `GET`/`HEAD` or
`passive: true` on closed read-only native operations so background activity
never resets server `last_seen_at`. Browser SSE (`client/search-events.ts`)
parses complete blank-line-terminated frames, treats any non-empty domain event
type as an invalidation hint, and advances `Last-Event-ID` only after the full
frame and awaited callback succeed. Native `subscribeEvents` and `eventsPoll`
(`client/native-wire.ts`) propagate optional `cursor` and hold no-cursor
bootstrap `resync` delivery behind `listenerReady` until the outer listener is
registered. `ShellController` (`client/controller.ts`) runs a single-flight
`#runResyncBarrier` bound to `#resyncLifetime` (retired on authority/credential
replacement or `stop()`) with `readGeneration` fencing and bounded retry when
authority or any screen sweep is incomplete. Mounted `GenerationControl` and
`DraftControl` (`app/screens/generation-control.tsx`,
`app/screens/generation-drafts.tsx`, `app/screens/generation-context.ts`)
participate in the passive invalidation/resync sweep while preserving local
prompts, overrides, selected replacement inputs/handles and journaled
recovery records.

`PersonalOperations` (`app/screens/personal-operations.ts`),
`ReviewOperations` (`app/screens/review-operations.ts`) and
`NotesPanel` (`app/screens/notes-panel.tsx`) query `GET /api/v1/operations/{id}`
first after an uncertain mutation and follow a `completed` receipt with a fresh
authorized resource `GET`, preserving any queued local edit as an explicit conflict
if a peer modified the resource after the recovered operation. `reader-preference`,
`reading-position` and per-page `page-position` maintain independent validators,
and stale `412`/`409` mutations present local-vs-server conflict comparison with
explicit server reload or overwrite actions. The private browser driver adds
`--task04-only` and `--task04-stages` (`personal-and-conflicts`,
`receipt-and-lost-ack`, `durable-job-browser-close`, `resync-and-passive-idle`,
`reader-page-recovery`, `reader-pending-resync`).
Mandatory refresh failures and locally superseded reads keep the full-state
barrier pending. A global read-generation change causes another sweep; verified
connectivity is checked after the sweep too. Initial/explicit and passive reads
share resource ordering. Ordinary Library hints coalesce while refreshing the
loaded page window. With no committed HTTP event cursor the initial subscription
uses server bootstrap. Healthy initial Provider bootstrap preserves its entry
read; gap/degraded recovery clears readiness and requires explicit refresh
without a passive probe. A local generation bump schedules passive successors
for retired sibling reads; required personal skips stay unconfirmed. Reader
refreshes position only for the current confirmed HTML page, leaving invalid
page/file recovery separate from document access. A required personal skip keeps
its existing pending banner without falsely reporting a document-read failure.
Inspected Ask remains selected
while current server data is revalidated. Known unfinished lifecycle receipts
remain unresolved without masquerading as failed reads.

See [multi-device reconciliation evidence](../docs/testing/multi-device-reconciliation.md).

## TASK-008-01 scan admission and findings read transport

The shared client contract (`src/client/types.ts`, `src/client/http-client.ts`,
`src/client/native-client.ts`, `src/client/native-wire.ts`) and desktop
`readerfacade` expose closed `scanStart` (`POST /api/v1/scans` with
`Idempotency-Key`) and `jobFindingsList` (`GET /api/v1/jobs/{id}/findings` with
optional `kind`, `status`, `limit`, `cursor`, and `passive` flag) gated by
`capabilities.features.scans`. `src/client/scan-client.test.ts` verifies browser
HTTP and native wire transport behavior, and the regenerated embedded bundle
passes `python3 -B frontend/check_build.py`. See
[scan inventory evidence](../docs/testing/scan-inventory.md).
