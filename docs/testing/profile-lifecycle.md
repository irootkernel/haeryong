# TASK-002-01 profile and daemon lifecycle evidence

**Task:** [TASK-002-01](../tasks/EPIC-002/TASK-002-01.md)\
**Implementation status:** done\
**Evidence status:** pass for the required implementation cases, corrected-candidate independent technical review and independent Goal Judge assessment\
**Source revision:** the task-scoped commit containing this implementation, based on `caa9d8e09d9ec05fecbcb8698e5e766b88ea8af1`; this record does not claim a passed Epic or approved release\
**Executed:** 2026-10-16\
**Environment/toolchain:** macOS 27.0.1 (26A434), ARM64, Go 1.27.1; Linux ARM64 Docker 29.8.2 using the already-present Go 1.27.1 Bookworm image; Gaori 0.1.18 CLI\
**Implementer / independent reviewer:** implementing agent / independent agent reviewer; original request-changes, corrected-candidate technical review pass

## Actual outcome

The root headless Go module now implements explicit user/profile initialization and a foreground single-writer daemon. It has no Wails, SQLite driver, provider or frontend dependency. The predecessor is the accepted EPIC-001 closeout `167559cb401d0841349be611db38359c1e6d45c3`, together with the [stack](stack-spike.md), [auth/ingress](auth-ingress-spike.md) and [contract-freeze](contract-freeze.md) evidence. This implementation uses the verified OS lock/private-file pattern while deliberately replacing the experiments' automatic initialization, ephemeral listener and fixture authentication assumptions. The current personal-tool and directory-result amendments remain applicable; no result import, JavaScript restriction or author packaging requirement is introduced.

`init` persists canonical directory, library and profile UUIDs and a random 256-bit local-control key. `daemon run` requires that identity; it never initializes a missing library. A stable mode-0600 lock file is protected by nonblocking OS `flock` before opening the storage preparation seam. A symlink alias resolves to the same canonical profile and lock. Profile directories are mode 0700 and records are regular, current-user-owned mode-0600 files; key/discovery/lock file symlinks and hard links are rejected. Initialization never overwrites a library or adopts nonempty unrelated data. Profile metadata is synced, atomically renamed and its containing directory and parent are synced. An interrupted partial initialization is not auto-adopted or replaced: inspect the explicitly chosen directory before recovery.

Default profile locations are `~/Library/Application Support/Haeryong/profiles/<profile>` on macOS and `$XDG_DATA_HOME/haeryong/profiles/<profile>` on Linux (default `~/.local/share`). `--data-dir` selects the complete profile directory. Its parent must already exist. `--volume-root` binds a selected external location to a real mount point; macOS `/Volumes/<name>` paths bind automatically. Linux external selections use explicit `--volume-root` because an arbitrary path does not identify its intended mount point. Missing paths and unmounted bindings fail without creating a replacement mount hierarchy. Normal startup also checks the persisted canonical directory and mount binding. Library relocation/copy to a different canonical directory is rejected; no migration/rebind command is implemented here.

The listener defaults to `127.0.0.1:9270`; `--port` is an explicit ephemeral override and the actual endpoint is returned in verified local status. Ports outside 1024..65535 and non-loopback endpoints are rejected. Explicit valid selections outside the preferred 9000..9999 range emit a preference warning on stderr, while JSON stdout remains machine-readable. A collision fails without fallback or readiness publication. Protected discovery and authenticated responses identify library/profile/API/instance and endpoint. A fresh nonce and HMAC response authenticate the service before identity is trusted or Stop is sent; the private key is never transmitted. Replay, reflected request proofs and wrong identity fields fail verification. Client redirects and HTTP proxies are disabled. `/health/live` reveals only `{"live":true}`. Local-control routes reject browser Origin headers and alternate Host values. This is the authenticated OS-user control boundary from the initialization contract, not future browser pairing or owner/workflow bearer authority.

SIGINT, SIGTERM and verified `service stop` withdraw discovery, close/drain the listener, close prepared storage resources, then release the writer lock. Stop returns success only after writer release is confirmed. SIGKILL leaves stale discovery; a client still rejects it unless the live instance passes authenticated identity verification. Restart preserves library/profile identity and rotates the instance. No desktop lifetime, auto-start installer, private proxy or persistent external service is introduced.

## Storage readiness boundary

`service.Storage.Prepare` runs under exclusive ownership, before publishing discovery. Its closer runs after endpoint shutdown and before lock release. A preparation failure publishes no readiness and releases ownership. TASK-002-02 will supply actual database migration/recovery and filesystem readiness. The present executable reports `control_ready=true`, `storage.ready=false`, `storage.state=pending_task_002_02`. It serves only minimal liveness and protected lifecycle control; `/api/v1/capabilities` and other product APIs remain unimplemented. No DB migration, coordinator checkpoint, import durability, remembered browser session or EPIC-002 gate is claimed.

## Case evidence

| Case | Fixture / source | Expected observable | Executed procedure | Result |
| --- | --- | --- | --- | --- |
| T-002-01-01 | Disposable actual profiles and separate daemon processes | A second writer, including a symlink alias, is rejected; storage prepares only while the writer lock is held | `TestExplicitInitializationAndAliasWriter`, `TestStorageReadinessAndShutdownOwnership` on macOS ARM64 and Linux ARM64 | pass |
| T-002-01-02 | Missing selected profile, unique nonexistent macOS volume path, existing ordinary directory explicitly declared a volume | Startup/init fail without creating substitute directories; available mounted scratch profiles still initialize | `TestUnavailableVolumeNoSubstitute` on both hosts, with the nonexistent `/Volumes` case on macOS; existing-directory unavailable-mount condition is injected, not a real unmount | pass |
| T-002-01-03 | Occupied exact listener, unrelated server at the previous daemon port, signed wrong identities and replayed replies | Collision fails without random fallback; unrelated server cannot impersonate or receive Stop | `TestListenerCollisionAndImpersonation`, `TestReplayAndIdentityMismatchRejected`, `TestEndpointValidationDoesNotFollowRedirectOrProxy` on both hosts | pass |
| Supplemental lifecycle | Real SIGINT/SIGTERM/SIGKILL subprocesses, stopped and restarted service | Clean stop withdraws discovery and releases ownership before success; crash/restart retains library and changes instance | `TestSignalsCrashAndRestart`, strengthened immediate writer-acquisition assertion in `TestExplicitInitializationAndAliasWriter` on both hosts | pass |
| Supplemental storage / protection | Application seam and private profile records | Preparation failure advertises nothing; endpoint closes before storage and lock; wrong profile and exposed/symlinked key records fail | `TestStorageFailureDoesNotAdvertiseOrRetainLock`, `TestProfilePrivateRecordsAndIdentityBinding` on both hosts | pass |
| Supplemental command / regression | Root module and frozen contract fixture matrix | Invalid random/zero ports and unavailable capabilities fail explicitly; existing product fixtures remain valid | `TestInvalidPortsAndUnsupportedCapabilities`; 29 existing contract tests, zero skipped | pass |

The initial candidate suite contained ten top-level tests. The Go race detector covered both test processes and their separately built real daemon/CLI children on macOS ARM64 and Linux ARM64. Linux was executed in a disposable, network-disabled, read-only container with executable temporary storage and no GUI; no image was pulled. The first Linux attempt could not execute any test binary because its tmpfs was `noexec`; it failed before application tests ran. Explicitly making only that disposable test tmpfs executable produced a passing full suite, followed by the focused stop-completion rerun. No owner volume was unmounted.

## Initial candidate commands and actual results

All checks ran through Gaori CLI with explicit canonical `--repo`, the existing `.gaori/tester.yaml`, tagged ad-hoc commands where appropriate and a verified mounted writable task scratch directory as `TMPDIR`. MCP launch binding was unverified, so CLI was selected. Config validation passed. Local Gaori artifacts were retained for orchestration; runtime paths/identities are not durable evidence references here. Passing raw logs were not opened. Only the failed Linux run's bounded Gaori failure excerpt was read.

| Selected child argv / procedure | Actual result |
| --- | --- |
| `go test -race -count=1 ./...` | pass on macOS ARM64 and Linux ARM64; root module only |
| `go test -race -count=1 -run TestExplicitInitializationAndAliasWriter ./cmd/haeryong` | pass on both hosts after adding the immediate post-Stop writer-acquisition assertion |
| `go vet ./...` | pass |
| `go build -o <task-scratch>/haeryong-<os>-<arch> ./cmd/haeryong` | pass for macOS ARM64, macOS AMD64, Linux ARM64 and Linux AMD64; AMD64 is cross-build proof only |
| `python3 -B tests/contracts/run.py --report <new-private-task-scratch>/contracts-task01.json` | pass, 29 tests, zero skips; pure contract regression, not new production storage proof |
| `python3 -B docs/testing/validate_docs.py` | pass, including the refreshed complete documentation manifest |

Linux runs used `docker run --rm --pull=never --network none --read-only --tmpfs /tmp:rw,nosuid,nodev,exec`, a read-only repository bind, workdir `/src`, `GOCACHE=/tmp/go-cache`, `GOMODCACHE=/tmp/go-mod`, `GOTOOLCHAIN=local` and `TMPDIR=/tmp`, followed by the same Go argv. The image was the locally inspected Linux ARM64 `golang:1.27.1-bookworm` image. Containers, daemon children and their test-created profile directories exited and were removed. The task scratch retains only reviewable build/contract artifacts until orchestration completes.

## Implemented local commands

Build with `go build -o <chosen-output>/haeryong ./cmd/haeryong`. The available command families are `version`, `init`, `daemon run`, `service status`, `service stop`, `status` and `doctor`. `status` and `doctor` currently verify only this implemented local identity/readiness boundary. Global selection supports `--profile`, `--data-dir`, `--volume-root`, `--endpoint`, `--json` and `--no-input`; `--port` selects a foreground daemon's loopback listener. `--endpoint` is only a local control client override: it still requires protected discovery and the exact authenticated library/profile/API/instance identity. Initialization and startup reject endpoint overrides. The JSON envelope has `data`, `error` and `request_id`; diagnostics use stderr. OS service install/start and other feature commands fail visibly as unavailable.

```sh
haeryong init --profile default --data-dir /existing/parent/library --json
haeryong daemon run --profile default --data-dir /existing/parent/library --port 9270 --json
haeryong service status --profile default --data-dir /existing/parent/library --json
haeryong service stop --profile default --data-dir /existing/parent/library --json
```

The paths above are operator placeholders, not executed owner data locations. Default startup remains loopback-only. There is no provider authentication/call, exposure configuration, service installation, staging or publication side effect.

## Contract, state and recovery effects

This is an additive root production scaffold and local identity format 1; it does not migrate any spike database or change the frozen product API/package schemas. The [schema registry](../contracts/schema-registry.md#implemented-local-control-identity-task-002-01) records the local-control shape. Lock and private-file primitives are initial macOS/Linux implementations. Runtime proof covers ARM64 macOS APFS scratch and Linux temporary storage; AMD64 builds are compile proof, not runtime or packaging acceptance. Network filesystems, storage crash/flush fault guarantees, Wails lifetime integration and release installation remain outside this task's measured scope. SQLite/filesystem compatibility must be evaluated by TASK-002-02 before product storage can become ready.

## Review and handoff

The three required implementation cases passed on the original candidate. Its independent review returned request-changes; the bounded corrections and successor checks are recorded below. The corrected candidate subsequently passed independent technical re-review and the independent Goal Judge assessment. TASK-002-01 is done; EPIC-002 remains `in_progress`. The task-scoped commit and native message/identity verification belong to orchestration. TASK-002-02 is the next ordered owner; its storage seam is explicit, without making current profile/lifecycle acceptance depend on future production storage.

## Independent review corrections (2026-10-16)

The original independent reviewer completed a read-only review of the frozen twenty-file candidate, whose inventory digest was `582c53b65b83d33db43a2d96888d8f7fe7a27a2712c7b15eb320c591156bc030`. The decision was **request-changes**, with two P2 findings: the minimum port/preference-warning contract was not implemented, and TestMain could delete an inherited caller-owned TMPDIR when its basename matched the test prefix. Original execution results above describe that original source; they are not reclassified as checks of these successor bytes, and the original review does not approve the successor candidate.

The authorized correction changes only CLI/Runner/local-endpoint port validation, CLI preference warnings, test-owned scratch cleanup and their focused tests. Valid ports are now 1024..65535 in all three validation boundaries. An explicit valid `--port` outside 9000..9999 warns on stderr and remains permitted; the default and preferred range produce no preference warning. The CLI test checks one complete JSON envelope with no extra stdout and covers 1, 1023, 1024, 8999, 9000, 9270, 9999, 10000, 65535 and 65536. Runner and endpoint tests independently check their boundary behavior.

TestMain now returns through a function with deferred cleanup registered against each successfully created scratch path, before the outer `os.Exit`. It never derives cleanup ownership from an inherited TMPDIR name. The regression creates a caller-owned directory matching the former prefix and a sentinel, launches an actual child test binary through TestMain, then verifies the sentinel and absence of child-created leftovers. It passed on actual Linux ARM64, where the original cleanup behavior was unsafe, and macOS ARM64. Normal completion and initialization/build error returns share the same owned-path cleanup; a forcibly killed process still cannot execute deferred cleanup.

The corrected candidate adds four top-level tests. Five selected top-level tests (four new boundary/scratch regressions and the existing unsupported-command check) ran on each host. Both focused race runs instrumented the actual child CLI/test binaries and passed. All successor checks used the same verified Gaori CLI/repository/config and task-scratch selection as the initial checks. Every successor check below returned child exit 0, Gaori status `passed` and extractor status `no_match`; no passing raw logs were opened.

| Successor command / procedure | Result |
| --- | --- |
| `go test -race -count=1 -run 'TestPortBoundariesAndWarnings\|TestRunnerPortBoundaries\|TestLocalEndpointPortBoundaries\|TestTestMainPreservesCallerTemporaryDirectory\|TestInvalidPortsAndUnsupportedCapabilities' ./...` on macOS ARM64 | pass |
| The identical focused Go argv in the already-present Linux ARM64 Go 1.27.1 Bookworm image, using the previously documented read-only, network-disabled, executable-tmpfs container invocation | pass |
| `go vet ./...` | pass |
| `go build -o <task-scratch>/haeryong-<os>-<arch>-v2 ./cmd/haeryong` | pass for macOS ARM64/AMD64 and Linux ARM64/AMD64; AMD64 remains compile proof only |
| `python3 -B docs/testing/validate_docs.py` after this correction record and manifest refresh | pass, with the corrected candidate and refreshed complete manifest |

No product API/schema, package contract or contract fixture was changed, so the unchanged 29-case contract suite was not repeated. The original full lifecycle suites and their initial Linux noexec failure remain preserved as historical evidence; successor focused checks address the changed boundaries. There is no new feature, migration, service installation, provider call or exposure change. At this correction checkpoint the Task/Epic were `in_review`/`in_progress`; the corrected bytes subsequently passed independent technical re-review while Goal Judge acceptance and commit were still pending.

## Independent technical review close (2026-10-16)

The same independent agent reviewer returned **pass** on the corrected frozen twenty-file candidate, inventory digest `bd1f77d958fbec0aff98ff08b9ab1ef5926ccc1d2baab6e99e5b2943c8a99dd4`. Both original P2 findings were resolved. The reviewer verified that exactly the eight authorized correction files changed, and checked the eight successor Gaori summary/status artifacts, selected command argv and summary hashes. The review reported no remaining actionable finding or evidence gap. The original request-changes and its source/check scope remain preserved above.

This was an agent-only independent technical review. It does not establish human usability, AMD64 runtime support, production storage/sessions, Wails integration, private ingress or release readiness beyond the existing measured limits. It is separate from Goal Judge acceptance and any Master acceptance. This documentation-only close recorded the actual review result; no implementation or test bytes changed after that reviewed candidate. At this checkpoint Task `in_review` and Epic `in_progress` were unchanged, with Goal Judge acceptance and the task-scoped commit still pending.

The documentation-only close was checked with `python3 -B docs/testing/validate_docs.py` through the existing Gaori `docs-integrity` command after regenerating the manifest; result: pass. No code tests were repeated for this three-file documentation delta. Native review details and local check artifacts remain local, without treating their live identities or runtime paths as durable tracked evidence.

## Goal Judge close and next owner (2026-10-16)

An independent report-only Goal Judge inspected the original authorities, complete candidate, technical review composition and original execution artifacts. The assessment returned **pass** with zero mandatory current-task unmet or unverified criteria. It covered T-002-01-01 through T-002-01-03 and the shared initialization, identity, local-control, port, shutdown, recovery, privacy and evidence obligations. This was an agent-only assessment; future storage, session, import, UI, ingress and release obligations retain their recorded owners and unverified global state.

Root accepted the assessment under the authorized direct execution. The final close changes only Task completion/handoff wording and the derived documentation manifest; production and test bytes remain those of the independently reviewed and assessed implementation. TASK-002-02 consumes the profile lock and storage preparation seam next. EPIC-002 remains in progress.
