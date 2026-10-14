# TASK-001-02 stack experiment

**Execution date:** 2026-10-14 KST.
**Source revision:** Git tree `162dee14b25c5754b15ff567a77ada1874b4383d` for `spikes/stack/`.
Use `git ls-tree -r 162dee14b25c5754b15ff567a77ada1874b4383d` for the exact delivered source, modules, lockfile and checker blobs; compare a checkout with `git rev-parse HEAD:spikes/stack`. **Evidence kind:** executed synthetic fixtures,
real macOS Wails/WebKit and Chrome, and a real headless Linux ARM64 container.
**G-STACK:** `pass` for this declared combination. Independent Task review and
completion remain governed by the Task's canonical state. G-AUTH, G-VIEW,
G-AI, G-VALUE and G-CONTRACT remain `not_run`.

The [reproducible spike](../../spikes/stack/README.md) and tests are delivered in
the same Task commit as this record. Preparatory runs were validated after
TASK-001-01 completed; the results below describe the formal Task experiment.
Local runner/reviewer records are not durable evidence dependencies.

## Selected combination and separation

| Component | Tested version or boundary |
| --- | --- |
| Host | macOS 27.0.1, build 26A434, ARM64; Xcode 27.0 |
| Go | 1.27.1; headless builds use `CGO_ENABLED=0` |
| Native client | Wails v3.0.0-beta.28, separate `desktop/go.mod` and checksum file |
| System WebKit | Bundle 22625, build 22625.1.29.11.28; observed native UA advertises AppleWebKit/605.1.15 and wails.io |
| Frontend | React/ReactDOM 19.3.0, TypeScript 7.0.2, Vite 8.3.4; React type packages 19.3.0 |
| Node/npm | Node 26.7.0, npm 11.19.0; exact package lockfile, lifecycle scripts disabled |
| SQLite | `modernc.org/sqlite` v1.60.1; runtime engine 3.53.4 on macOS and Linux ARM64 |
| Headless host | Docker 29.4.0, OrbStack Linux ARM64 kernel 7.0.14-orbstack; existing Alpine 3.20 image |
| Linux image | `sha256:ab3fe4defd29ba6231229a4d41440ac8bde8218e85870e53876277faa24b35c4`; no image download |
| Browser | Headless Chrome 154.0.0.0, 1280 × 720; Browser plugin unavailable, existing Playwright CLI used |
| Browser CLI | `@playwright/cli` 0.1.1, Playwright 1.59.0-alpha-1771104257000; private per-run registry, profile and socket directories |

Select the pure-Go SQLite driver because the same pinned module executed WAL,
FTS5 and snapshot/restart checks on both available hosts without a C toolchain.
The native linker emits deployment-target warnings (objects newer than the linker target); execution on the declared host passed, and older macOS support is unverified. Wails remains a beta dependency: this exact tested build is the spike baseline,
not an assurance about another patch or a signed release. macOS/Linux AMD64
and release packaging remain initial-target obligations before advertised
support; Windows/native mobile are not added by this experiment.

The headless dependency list contains no Wails package. The Linux container ran
the actual service with no DISPLAY/WAYLAND_DISPLAY, no external network and a
read-only root; `/health` returned the SQLite engine and `/` returned the compiled
frontend. Native and browser clients loaded that same frontend from the same
loopback origin and reported the same service instance after a JavaScript health
read. No Vite development server was used. The native spike registers no Go
services and stores no credentials; the narrow production proxy and imported
renderer contexts remain TASK-001-04 decisions.

## Functional results

| Assertion | Procedure and expected result | Observed result |
| --- | --- | --- |
| T-001-02-01 | `check.py`: compile headless with CGO off, inspect dependencies, run HTTP and SQLite on headless Linux | Pass; compiled assets/health returned without Wails or a graphical session |
| T-001-02-02 | `check_lifecycle.py`: native JS reads health; actual `window.Close`; fresh native process SIGKILL; service identity unchanged | Pass; normal close exit 0, forced crash -9, same service available after each; no human close interaction claimed |
| T-001-02-03 | `go test -count=1 -v ./...` on macOS and compiled test binary on Linux | Pass; WAL/FTS5, rollback, concurrent reader, backup and abrupt-restart observations below |
| T-001-02-04 / X09 | Predeclared isolated rows and expected counts in `retrieval_test.go`; run before selecting driver/index baseline | Pass with explicit unsupported tokenizer behavior below; denied/trash excluded |

Chrome loaded the intended title and nonblank fixture without a `client` query. Keyboard activation of **Check fixture service** displayed `stack-spike: fixture-<pid>` for the same service used by the native checker; it created no attachment.
The post-click DOM and screenshot showed no framework overlay, clipping or
missing content. The corrected harness also rejected malformed/mismatched attachments, non-loopback native URL forms and early close, and verified fresh attachment IDs on repeated invocation. The normal-load console recorded zero errors and warnings. This is a fixture
interaction check, not reader usability, responsive layout, mobile Safari or
G-VIEW evidence. Both native cases were repeated against the same running service with fresh invocation IDs; the four native children, browser and owned service were
closed after checking; only the experiment's own disposable container was removed. The final repeatable browser procedure is `check_lifecycle.py --browser-cli /absolute/path/to/playwright-cli`: keyboard activation checks the exact instance, loading without `client` creates no attachment, controlled health/attach stalls show a timeout within a measured four-to-seven-second band around the configured five seconds. Health and attachment have separate status: a health check preserves failed attachment status, and **Retry attachment** repeats the actual POST and records the same service instance. The no-client baseline is sampled before browser launch and a request observer also rejects unsolicited attachment calls. The normal-load console has zero errors/warnings; intentionally stalled-request diagnostics are separate. Private-parent, symlink and existing-endpoint rejection checks also pass without overwriting their sentinels.

The browser also sends a mismatched instance through the actual attachment
endpoint: HTTP 400 leaves the observation map unchanged, identifies the mismatch
and keeps **Retry attachment** enabled. A health HTTP 503 preserves that
attachment failure and creates no attachment. A separate initial attachment
whose health read times out explicitly names **Retry attachment**; a later
successful health check preserves the failed attachment, and only the attachment
retry records the client. Both recovery paths passed on the same running fixture.
The Linux startup probe rereads the endpoint and retries HTTP health within its
five-attempt budget; file existence alone is not treated as readiness. Exhaustion reports the actual attempt count, endpoint and retained transport error before a nonzero exit. The delivered `check_checker.py` executes that actual loop with controlled failures and verifies the diagnostic and five failed reads; it also drives the actual browser-finally code with synthetic browser, preflight and close failures, preserving outcomes and nonzero errors.

Browser CLI registry, profiles and sockets use an exclusively created private
temporary namespace through process-only `TMPDIR`,
`PLAYWRIGHT_DAEMON_SESSION_DIR` and `PLAYWRIGHT_DAEMON_SOCKETS_DIR` overrides.
The browser command runs a real two-session isolation probe using the actual
controller setup and the same session name: each keeps its own page state, and
closing/removing one namespace leaves the other usable. The fixture browser
then runs in a third owned namespace. Successful close removes that namespace;
a close failure retains it and records `browser_directory` and `browser_session`
for recovery. The renderer/front-end source is unchanged by this check.

## Database consistency observations

The service uses one SQLite connection. `/health` reports its actual connection readback: WAL, `synchronous=2` (FULL), `foreign_keys=1` and `busy_timeout=5000`. Both native-host and Linux checkers assert those values and engine 3.53.4; they do not infer connection settings from a newly opened database handle. This is a measured spike configuration, not
the later production connection-pool/migration implementation.

- An explicit rollback left only the original row in a `VACUUM INTO` copy.
- A WAL read transaction kept generation 1 for both existing rows (sum 2) while a separate writer committed generation 2; the open reader still saw sum 2.
- While another write transaction held an uncommitted third generation, a
  separate connection completed `VACUUM INTO`. The copy retained the complete
  second generation, `integrity_check` returned `ok`, and the live database saw
  the third generation after commit. This compares updates to the same two row IDs; it does not exercise concurrent insertion of additional rows.
- A child committed one row with WAL/FULL, opened an uncommitted second write and
  exited abruptly with code 23 without closing the database. Reopening retained
  only the committed row on both hosts. The parent always creates its own temporary DB and passes its path as an explicit child-only test argument; an inherited restart environment variable cannot redirect either process to another database. The delivered restart test supplies a preexisting SQLite sentinel through the child environment and requires its bytes, schema and rows to remain unchanged while the intended restart DB retains only the committed row. The full host check also passed with an inherited path pointing at a separate sentinel.

The [SQLite VACUUM contract](https://www.sqlite.org/lang_vacuum.html) distinguishes
a consistent completed snapshot from an interrupted copy. These checks do not
prove power-loss durability, filesystem publication ordering, content-object
pins, production restore or migration recovery. EPIC-002 owns publication and
schema foundations; EPIC-005 owns coherent DB-plus-content backup/restore. The
full M09/M10/M14/M15 scenarios remain with their primary production owners.

## X09 expected and observed worksheet

Rows are isolated so snake_case/path tokens cannot masquerade as camelCase
splitting. They contain decoded precomposed Hangul and synthetic code names.
Each expectation below was declared in the test source before its execution;
the observed count equalled that expectation on both hosts.

| Row and query | unicode61 expected / observed | trigram expected / observed | Limit or selected handling |
| --- | --- | --- | --- |
| `검토 승인 재시도`, `검토` | 1 / 1 | 0 / 0 | Two-character substring uses authorized-corpus fallback |
| Same row, `검` | 0 / 0 | Not a candidate | One-character fallback returns the two eligible rows; `검*` prefix also returns 1 |
| Same row, `재시도` | 1 / 1 | 1 / 1 | Both handle the three-character term |
| Same row, quoted `검토 승인` | 1 / 1 | Not exercised | Longer phrase preserved |
| Unspaced row, quoted `재 시도` | 0 / 0 | Not exercised | Spacing normalization is unsupported, not silently claimed |
| Separate `재 시도` row, same phrase | 1 / 1 | Not exercised | Exact spaced phrase works |
| `source_access`, `source` or quoted `source_access` | 1 / 1 for each | Not exercised | Token/phrase behavior, not literal punctuation identity |
| Capability-probe row containing `source_access` and `readSource`, `Source` | 1 / 1 | Not exercised | Default unicode61 ASCII case folding; this combined-row count does not establish camelCase splitting |
| `readSource`, `readSource` | 1 / 1 | Not exercised | Whole camelCase token works |
| `readSource`, `source` | 0 / 0 | 1 / 1 | unicode61 does not split camelCase; substring candidate covers this query |
| `source/path.go`, quoted `source/path.go` or `go` | 1 / 1 for each | Not exercised | Word/phrase matching does not establish arbitrary symbol equivalence |
| `source/path.go`, quoted `path.go` | Not exercised | 1 / 1 | Literal substring candidate includes the dot |
| `승인 검토`, quoted `검토 승인` / unquoted `검토 승인` | 0 / 0; 1 / 1 | Not exercised | Phrase order differs from unordered token AND |
| `source/path.go`, quoted `source-path.go` | 1 / 1 | 0 / 0 | Word tokenizer treats separators alike; literal substring does not |

Both FTS candidate paths join the authorized, non-trash corpus: `재시도` returns
one eligible row, while denied `비공개` and trashed `휴지통` each have an unfiltered FTS hit and return zero after filtering. The tests assert both counts on each candidate index. The
one/two-character `instr` fallback returns the two eligible rows containing `검토` from that same filtered corpus directly,
including the eligible row absent from trigram results. The fallback asserts the exact corpus IDs `1` and `8`, so an equally sized denied/trash result cannot pass; it does not search an
empty FTS result set. This checks exclusions without asserting production auth.

Select unicode61 for word/phrase candidates, trigram for substring candidates of
at least three characters, and a paginated/bounded-work authorized-corpus scan
for shorter literal queries. These are indexing choices for TASK-004-02, not a
production search implementation, input cap, ranking benchmark or promise of
spacing/camelCase normalization. The [FTS5 tokenizer documentation](https://www.sqlite.org/fts5.html#tokenizers)
and measured failures support this split. Decomposed Unicode, broader owner
questions and larger-corpus latency remain TASK-004-02 tuning/regression work.
