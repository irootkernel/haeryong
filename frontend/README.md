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
