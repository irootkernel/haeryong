# Haeryong shared frontend shell

This directory holds the shared React/TypeScript trusted shell, Home/Library
and result-transfer controls, and their transport adapters. The compiled production bundle under
`../internal/webassets/dist` is committed and served by the Go daemon; no
development server and no service worker/private asset caching are involved.

## Layout

- `src/app/` — shell, registry router, connect screen, theme tokens.
- `src/app/screens/` — Home/Library, shared import and streamed download, and
  Settings/Storage upload discovery/recovery. The preselection harness is a
  separate verification entrypoint, excluded from the production bundle.
- `src/client/` — `HaeryongClient` contract, browser HTTP transport, narrow
  `NativePort` boundary (the real desktop binding ships with TASK-003-05),
  credential stores, and the connection state machine.
- `check_build.py` — validation/regeneration entrypoint (see below).

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
the separate native entrypoint build (`npm run build:native`) is a scratch
artefact for TASK-003-05 and is not embedded in the daemon.

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
