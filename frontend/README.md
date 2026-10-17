# Haeryong shared frontend shell

This directory holds the TASK-003-01 React/TypeScript trusted shell and its
transport adapters. The compiled production bundle under
`../internal/webassets/dist` is committed and served by the Go daemon; no
development server and no service worker/private asset caching are involved.

## Layout

- `src/app/` — shell, registry router, connect screen, theme tokens.
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
- `npm run test` — `node --test` adapter/controller suites (normal exit; no
  force-exit).

## Authority-era contract (summary)

The controller binds the transport credential and starts an authority era on
credential, library or runtime-generation changes only. Routine
same-authority rechecks keep the current era so in-flight writes are not
discarded, keep the mounted screen state, and record uncertain writes
(original K operation identity or natural `session_id` target) until a
receipt/current-state reread resolves them under current authority.
