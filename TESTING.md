# Testing Haeryong's headless implementation

## Contract

Contract: aquarium-test-contract/v1

Profile: make

Enrollment covers the root Go implementation and its test/documentation tools.
The separately pinned `spikes/` modules and experiments retain their original
commands and historical evidence; they are not part of these common stages.
Frontend, native Wails, private-browser and real-provider acceptance remain with
their ordered implementing owners. Every current common stage has a subject.

The executable handlers are authoritative. Enrollment and static inspection do
not establish a passing gate: a complete authorized `make test` must finish on
the same candidate without unexpected mutations. An unexecuted required gate is
configured but unverified, never a successful skip.

## Canonical Commands

Prepare dependencies separately with the exact Go toolchain in `go.mod`, a
native C compiler and Python 3.11 or newer. From the repository root, run:

```sh
make test-deps
make test
```

`test-deps` creates a repository-local `.venv` when needed and installs
`requirements-test.txt` into it. It is an explicit setup command, outside the
aggregate and all common stages. `.venv/` is ignored by Git. Once prepared,
Make selects `.venv/bin/python` by default without shell activation; otherwise
it selects `python3`. An explicit `PYTHON` environment or Make variable takes
precedence, so an operator can select an existing prepared environment.

The Python requirements are minimum versions without an upper bound. Dependency
preparation may download public packages; test handlers never install
dependencies, upgrade Go or fetch modules. Go caches must already be prepared.

| Command | Handler |
| --- | --- |
| `make test-deps` | Explicit local virtual-environment/dependency preparation |
| `make test` | Recursive Make: prepare, unit, integration, E2E, once in that order |
| `make test-prepare` | `$(PYTHON) -B tests/run_checks.py prepare` |
| `make test-unit` | `$(PYTHON) -B tests/run_checks.py unit` |
| `make test-int` | `$(PYTHON) -B tests/run_checks.py integration` |
| `make test-e2e` | `make test-persistence`, then `$(PYTHON) -B tests/run_checks.py e2e` |
| `make test-persistence` | `$(PYTHON) -B tests/run_checks.py persistence`; additional internal storage gate |

Set `PYTHON=<venv>/bin/python` to select another prepared environment. The
existing `bash tests/integration/foundation/check-headless.sh` is an offline
adapter to the same aggregate and exposes its private run root to manual CI.
The shell adapter uses the same Make-selected Python. `make -j test` preserves stage order. The aggregate has no stage prerequisites
that Make could run concurrently, and no ignored failures or reverse edges.

## Continuing independent checks

Default `make test` and `KEEP_GOING=0` remain fail-fast. Use
`make test KEEP_GOING=1` for the native completion selection that continues after
ordinary failures. The stages are prepare, unit, integration, persistence, then
E2E, serially once even with `-j`. Standalone `make test-e2e KEEP_GOING=1`
runs persistence and E2E once each. Each other leaf accepts the same Make option;
`tests/run_checks.py STAGE --keep-going` selects it directly.

In this mode Ginkgo adds `--keep-going`, pytest omits `-x`, and a Go failure
cannot suppress the independent Python selection. Prepare checks `gofmt -l`
without rewriting and retains failures while checking independent syntax inputs,
contract digests, docs, layers, modules, vet, dependency boundaries and all four
cross-builds. Default prepare still applies `gofmt -w`.

Each command retains its raw exit, signal and timeout in private `.status.json`
beside its log. Leaf control exits are separate: 0 success, 1 completed check
failure, 2 fatal setup, and 128+signal cancellation. The aggregate returns nonzero
if any check failed. Cancellation and unsafe global setup stop later scheduling;
owned command process groups are terminated and awaited. An ordinary timeout may
continue only after cleanup. No retries, relaxed collection, new skips or budget
changes are implied. Missing/failed reports remain failures; unexecuted required
scenario steps remain obligations, not passes.

The [EPIC-005 verification profile](docs/testing/epic-harmony-verification-profile.md)
selects this native mode and the separately owned frontend/desktop/campaign checks.
Historical runtime adapters are not the post-change completion command.

## Console progress

Each child command prints `START` with its private log and existing timeout.
While waiting, the runner prints `WAIT` every 30 seconds with elapsed time and
how long that log has been unchanged. This confirms that the supervisor is
waiting, not that a silent child has made progress; it is not an ETA or a pass.

Python stages also show collection and each test's `START` and result, including
fixture teardown, with completed position/total and duration. A long test keeps
its last start visible while the periodic wait lines continue. These milestones
are streamed from `pytest-<layer>.progress.log`; test parameter values and
captured product output stay out of that metadata. Full output remains in the
existing private log, and native exit/signal/timeout and structured reports
remain authoritative. Progress changes neither deadlines nor test selection,
fail-fast/keep-going behavior or process cleanup.

## Stage Mapping

| Stage | Concrete ownership and isolation |
| --- | --- |
| prepare | Apply `gofmt` to root `cmd/`, `internal/` and `tests/` Go source; parse Python syntax; check layer declarations, documentation and selected-contract digests; `go mod verify`; `go vet ./...`; domain/application dependency direction; four Darwin/Linux ARM64/AMD64 headless builds. No database or service starts. |
| unit | Ginkgo specs labelled `unit`: configuration, checkpoint/fingerprint and inventory identity, client snapshots, ZIP codec and the isolated filesystem object-store unit. Temporary files belong to each unit's interface. Pytest `tests/unit` verifies actual serial/fail-fast Make behavior and run-root refusal without starting a product/database. |
| integration | Ginkgo specs labelled `integration`: client/control adapters with loopback fakes and lifecycle/profile cooperation with fake storage/endpoint ports. No real SQLite, container, provider or separately managed service. Native pytest assertions execute `tests/contracts` fixture/model cooperation and `docs/testing/test_validate_docs.py` against disposable public repository fixtures. |
| additional persistence | Ginkgo specs labelled `persistence` retain internal SQLite/filesystem, migration, typed job/receipt/pin/GC and fault/recovery checks, including test-only process barriers and internal fixture state. These white-box results are explicitly distinct from black-box E2E evidence. They are an additional named gate inside the nearest common stage, not another aggregate stage. |
| E2E | Pytest `tests/e2e` builds the real `cmd/haeryong` binary, then drives public CLI, HTTP and lifecycle boundaries. The normal production initializer/daemon creates and migrates each private test SQLite database. Assertions use public responses, process outcomes and delivered file bytes. |

The additional persistence gate does not waive unit/integration isolation or
claim that its direct internal assertions are E2E. Public CLI orchestration is
migrated to pytest; low-level persistence and injected-boundary checks retain
their own evidence. No GC API, provider, future grant issuer or alternate
production coordinator is introduced for setup.

`tests/integration/owner-records` is persistence-labelled and is selected by
`make test-persistence`/`make test-e2e`, not `make test-int` alone. Its public
counterpart is `tests/e2e/test_owner_records.py`. Separate actual browser/Wails
commands and current limits are in [the Task6 record](docs/testing/owner-records.md).
`tests/integration/ai-workflows` also uses `persistence`: it composes real
foundation/SQLite, source/config authority, Generation/Question, saved-result
continuation, cancellation and protected backup/restore through normal services.
Its controlled provider makes no native/model call. [The AI workflow record](docs/testing/ai-workflows.md)
separates focused results from actual common, provider, browser/Wails and quality
gates. These scenarios are selected by `make test-persistence`/`make test-e2e`,
not `make test-int` alone.
Recovery/storage consumer specs use `unit`; a `persistence` filter does not cover
them. Required final gates retain complete registered layers.

Go leaf execution is `go tool ginkgo --race --fail-on-empty --fail-on-pending
--no-color --timeout=<budget> --label-filter=<layer> --json-report=<private-report>
<packages>` using only packages containing that layer. The budget is `1000s` for
unit and integration and `8400s` for the additional persistence gate. Ginkgo uses
one deadline for compilation and the complete selected package list, not that
budget per package. The persistence bound rounds about three times the slowest
retained successful complete persistence interval (about 2,754 seconds) upward;
the earlier 3,600-second exhaustion remains a failed run, not calibration for a
passing duration. These prior timings cover nineteen suites; the current full
selection, including AI workflow composition, still requires its own execution.
Ginkgo builds/runs its test executables without Go result caching. Required
selected specs must pass; other-layer filtered specs are not required skips.
A timed-out or unstarted suite remains failed. Its explicit Ginkgo failure reason
is retained even when Ginkgo emits a null `SpecReports` for a suite it could not
start; null reports without a terminal failure reason remain malformed.

The native command watchdog remains the selected Go budget plus 500 seconds.
Configured Gaori `test` and `test-e2e` outer bounds are 18,900 seconds, rounded
from about three times the slowest retained complete common duration (about
6,286 seconds). The configured persistence outer bound is 9,000 seconds so its
8,900-second native watchdog and evidence finalization can settle. These outer
bounds do not replace or override inner Ginkgo guards. The canonical Make/helper
entrypoint exposes no ambient or per-invocation timeout override; changing a
budget requires a reviewed canonical change and separately bound execution.

Every finite test time limit, from the Go and pytest leaf bounds to scenario
waits such as F12's, is a hang guard for a shared and usually heavily loaded
host. A limit that a correct run can reach under load is raised to about three
times the slowest observed passing duration. Elapsed time is informational and
never an acceptance criterion.

Python leaf execution is `python3 -B -m pytest -x --strict-config --strict-markers
-W error --haeryong-progress <private-milestones> --basetemp <new-private-path>
--junitxml=<private-report> <suite-paths>`.
Ambient `PYTEST_ADDOPTS` must be unset; it cannot replace a required run with
collection-only or partial-selection behavior. The unit path is `tests/unit`; integration selects `tests/contracts` and the
documentation-validator regression file; E2E selects `tests/e2e`. Each selected
test has one layer marker. Empty collection, skips and xfail/xpass do not pass a
required stage. Selected contract rows/groups must each resolve to one actual
collected test, with source-bound JSON reports through the pytest contract hook.

## Test Frameworks

| Language/layer | Framework and dependency authority | Runner |
| --- | --- | --- |
| Go unit/integration/additional persistence | Ginkgo v2.33.0 and Gomega v1.43.1 pinned in root `go.mod` and `go.sum`; the Ginkgo CLI is a module-managed Go tool. Native `testing.T` remains only for suite bootstrap and the existing subprocess fixture dispatcher, not a second scenario runner. | Ginkgo CLI above; native Gomega assertions and Ginkgo specs |
| Python unit/integration/E2E | `pytest>=9.1.1` and existing `PyYAML>=6.0.3` in `requirements-test.txt`; strict root `pytest.ini`; no Python lockfile or upper framework bound is introduced. | Native pytest collection, fixtures and assertions |

Existing unittest scenario bodies are rewritten rather than merely collected by
pytest's unittest adapter. Go scenario bodies become Ginkgo specs; no wrapper
runs the old native test suite. The migration preserves selected contract IDs
and the unchanged F12 generator and file identities; the F12 CLI scenario keeps
an `1800s` transfer hang guard. Test budgets and chunk/cursor sizes are not
product content limits.

The structural inspector is conservative: it does not follow the Python leaf
dispatcher into `go tool ginkgo`, and its source scan can see excluded historical
unittest experiments under `spikes/`. Such findings require inspection of the
actual scoped commands/imports/reports; they do not authorize a waiver or prove
the common handlers noncanonical.

## Gaori Mapping

Gaori is optional and never changes the underlying command result.

| Gaori command | Wrapped command | Output family | Parser |
| --- | --- | --- | --- |
| docs-integrity | `python3 -B docs/testing/validate_docs.py` | Documentation validator text/summary | generic |
| test | `make test` | Make, stage and check summaries | generic |
| test-prepare | `make test-prepare` | Formatter/static/build summaries | generic |
| test-unit | `make test-unit` | Ginkgo and pytest stage summaries | generic |
| test-int | `make test-int` | Ginkgo, pytest and contract summaries | generic |
| test-e2e | `make test-e2e` | Additional persistence and E2E summaries | generic |
| test-persistence | `make test-persistence` | Driver and internal-storage summaries | generic |

Native detailed reports remain private. A specialized parser is not selected
from the source language or from Ginkgo/pytest labels alone. Installed parser
availability does not establish extraction correctness or remote CI execution.

## E2E Environment

- Artifact: the unchanged production headless command built with race
  instrumentation, without test routes, environment-enabled production hooks or
  a replacement worker. A separate F12 setup executable only generates the
  established synthetic ZIP/manifest bytes; it cannot perform product actions.
- Identity: each case has a unique profile and private data directory beneath
  the current test-owned root, a loopback-only endpoint and run-owned processes.
  The handler refuses an unmarked, shared, foreign-owner or symlinked run root.
  Tests verify storage readiness and library/profile continuity across restart.
  Existing profiles, ambient endpoints and owner credentials are not accepted.
- Setup/seed: public `init`, normal migrations, public pairing creation and
  protected CLI approval create synthetic owner sessions. Public import/config
  commands create the scenario state. No production dataset is copied.
- Ports and network: ephemeral loopback ports only, including disposable fault
  proxies; no container, cloud resource, live provider or paid request. Network
  configuration tests affect only their own disposable product profile.
- Credentials: no external credential environment variables are required.
  Synthetic session credentials are private test files and are redacted from
  fixture representations. Private raw logs/databases are not CI upload inputs.
- Observation/assertions: public job/operation/session/query endpoints,
  CLI results, signal exit status, complete inventory and byte hashes. Loopback
  proxies lose committed acknowledgements or interrupt delivery without granting
  authority. Test-owned caller snapshots may be damaged/restored as input setup;
  success/failure is still asserted through public commands.
- Evidence/teardown: bounded summaries, contract/F12 identities and native test
  reports remain in the unique run root. Every fixture terminates and awaits its
  own children and closes fake listeners; test profiles are deleted by temporary
  fixture cleanup. Only this run's reports/build artifacts remain for explicit
  retention or deletion. Cleanup never deletes shared paths or unrelated runs.

`HAERYONG_REQUIRED_TEST_STAGE` is set by the leaf handler to require an actual
completed call for every selected pytest item, rather than only collection.
`HAERYONG_TEST_RUN_ROOT` may select only a root created by
`$(PYTHON) -B tests/run_checks.py new-run` for this repository/user. Without it,
each leaf creates its own unique root. On hosts with a mounted writable
`/Volumes/RootKernel`, scratch uses that volume's `tmp`; otherwise it uses the
normal system temporary parent. `TMPDIR`, `HAERYONG_DOCS_TEST_TMP_ROOT`,
`HAERYONG_CONTRACT_REPORT`, `HAERYONG_TASK005_WORKLOAD_REPORT`,
`HAERYONG_TASK004_WORKLOAD_REPORT`, `HAERYONG_TASK004_CROSSVOLUME_REPORT`,
`HAERYONG_TASK006_CRASH_REPORT` and `HAERYONG_TASK006_REAL_INPUT_REPORT` are
test-owned artifact locations, never credential values.

Required toolchain/cache/compiler/port/mount-input failures fail the gate. The
actual cross-filesystem regression cannot silently skip when its required input
is absent. Effectful database/process E2E and the internal persistence gate need
execution authorization separate from approval of setup files.

## Language Diagnostics

Use the exact root Go version with `GOTOOLCHAIN=local`, `GOPROXY=off`,
`GOSUMDB=off` and readonly modules. Applicable native suites use race detection;
prepare uses vet and production dependency checks. Python uses strict config,
markers, warnings and bounded child-process/readiness waits; no parallel pytest
plugin or automatically loaded external plugin is required.

The current complete native headless gate targets macOS/Linux ARM64 with a C
compiler. Darwin/Linux AMD64 builds establish compilation only. A macOS pass
does not establish a Linux pass, remote CI execution, native Wails, a private
second device, provider behavior or a whole-product acceptance gate.

## Legacy Waivers

None. Canonical frameworks apply to the enrolled root layers. Excluded spikes
retain their separate historical identity; they are not newly enrolled layers.

## Task05 focused decision checks

The persistence-labelled T00805 specs exercise owner decisions, all-linked Undo,
rollback, pin/classification conflicts, preparation policy races, origin-aware
coalescing and actual deterministic repair completion/restriction. Existing
RF06/SC10 selections include every schema24 table and meaningful historical
empty-table assertions. HTTP/CLI loopback and Go-native facade selections remain
separate from actual GUI proof. The two new frontend action specs are registered
in package.json and check_build.py. The opt-in maintenance_saved.py and
maintenance_task05_probe.js require exact CO-bound controlled cases, immutable
consumer binaries/prepared bundles and a separate runtime grant; ordinary
checks do not run them. See [finding decisions](docs/testing/finding-decisions.md).
