# Headless source and provider boundaries

**Task:** TASK-007-05

**Epic:** EPIC-007

**Implementation status:** done for the owner-amended bounded web member

**Evidence status:** pass for the bounded composition below

**Source revision:** normal product build at `7078175602fb569dc5ed0430dfc8ea560479284c`; new opt-in driver and documentation are assessed separately

**Environment/toolchain:** macOS 27.0.1 arm64, Go 1.27.1, prepared browser CLI selecting Chrome, browser API version 157.0.8103.38

**Implementer / reader:** assigned Argon Task Operator and Coordinator; agent-operated web observations

## Actual outcome

The normal untagged Go service served stored complete multi-page directories
through actual same-Mac Tailscale private HTTPS after both selected source
checkouts and both original import directories were removed and the daemon was
restarted. One version supplied Git and retained-snapshot metadata; the other
had no source metadata. Both rendered ordinary HTML/CSS, JavaScript interaction,
module/worker and root-relative assets, then navigated to the exact nested page,
query and fragment. Each version used a distinct isolated renderer origin.
The generated frames could not read the trusted parent's DOM or storage.

The daemon, CLI and browser ran from an owned non-repository working directory;
Git fixture commands used a separate newly created source checkout. Explicit
profile, data and client directories avoided user-default changes. The supplied
normal binary's SHA-256 was
`2895ff99403a900d07e016efaac6ef5f6d28f7b022e4d18e09d1f7c90033700d`.
Its actual build metadata recorded no build tags or Wails dependency; linked
libraries contained no AppKit, WebKit or Cocoa GUI framework. This proves the
selected service's runtime independence, not absence of a desktop installation
or graphical login on this Mac.

No product Go, TypeScript, compiled asset, schema or provider implementation
changed in this Task. The opt-in drivers are
`internal/adapters/http/auth/browser/check_headless_sources.py`
and `internal/adapters/http/auth/browser/check_headless_sources.js`.
The [current owner direction](../decisions.md#owner-amendment-2026-11-05-web-only-epic-007-execution)
selects web verification and excludes physical-device proof.

## Case evidence

| Case | Exact fixture and expected observable | Procedure | Actual result |
| --- | --- | --- | --- |
| T-007-05-01 | One sourced and one metadata-absent multi-page directory; originals unavailable | Import through production CLI, remove only owned originals, ordinary service stop/start, actual private-web Reader | pass: both pages, CSS, control, module, worker, root asset and exact query/fragment navigation; distinct isolated origins |
| T-007-05-02 | Explicit Git/files mappings with `codex` disclosure; missing host source and unavailable provider executable | Current project Sources, source settings, Reader Evidence/Ask and AI-provider screens; authenticated private provider API | pass: recorded-only observation and host-native authority notices remain visible; both settings fieldsets retain the exact approved root and `codex` disclosure; no automatic laptop source/credential or OS-confinement claim |
| T-007-05-03: current gap | Explicit `current` selection of a file in the removed Git checkout | Normal CLI Ask and saved-question/browser reads | pass: `paused`, `source_capture_unavailable`, null target set, empty targets/answers and `unable_to_verify`; saved-question API and opened UI details preserve the exact requested source ID/path and derived mode/version/provider context; job summary count/unknown/reconciliation-pending all zero |
| T-007-05-03: retained bytes | Explicit previously captured `selected_snapshot` read and separately embedded snapshot evidence | CLI retained read and actual private HTTP body/ETag read | pass: complete exact bytes and digest remain available; CLI reports `input_observation: false` |
| T-007-05-03: posthoc | Exact Git revision without retained bytes versus exact embedded snapshot | Owner observation, production verification and private Evidence checks/history | pass: Git `unavailable/missing_source`, snapshot `matched/retained_snapshot`; authored declarations remain unchanged; posthoc never becomes original attempt input |
| Source identity/freshness | A successful capture followed by a failed observation | Read the project source projection before/after explicit observation | pass: latest availability/reason become unavailable/missing_source; the last actually captured revision remains historical, with no newly captured target or semantic outdated verdict |
| Private authentication and cleanup | Same private route; network identity is not an owner credential | Unauthenticated/forged-identity requests, normal Connect pairing, owned-route removal | pass: expected 401/401/403; browser and daemon exit cleanly; complete original Serve configuration restored |

The closed current fixture contained one intentionally paused Question, zero
provider attempts and zero provider input rows, counted through a read-only
SQLite connection after service shutdown. Public job summary independently
reported zero attempts. These are actual fixture records, not a model-execution
counter inferred from a browser label. No SQL writes or real provider execution
were used.

## Evidence composition and retained failures

This is an affected evidence composition, not a claimed fresh success of the
entire default driver invocation. The final normal acquisition completed the
source, retained-byte, posthoc, missing-question and unavailable-provider backend
checks. Its browser continuation initially failed. The same retained profile,
library, immutable versions, approved policy, source absence, binary and origin
tuples were then reused for the current successful private-web continuation.
Current job/source reads and the final closed database counts establish its
applicability. Earlier failures remain failures:

1. The first acquisition expected a failed source observation to erase the last
   captured revision. The contract preserves that identity; only the driver
   expectation was corrected. No browser or Serve route had started.
2. The next acquisition used an unimplemented attempts-detail route. It was
   replaced with the existing job's `provider_attempts` summary. Backend source
   and verification checks had passed; the daemon exited normally and no route
   had been applied.
3. Private browser execution reached the stored interactive page but timed out
   without naming its screen predicate. Exact driver bytes and the closed
   fixture database were retained before recovery; stage/DOM/screenshot
   diagnostics were added without weakening failure handling.
4. Same-fixture recovery identified an obsolete generic unavailable-text
   expectation. The actual UI correctly said that the source was absent at the
   last recorded check. That expectation was aligned with the existing
   presentation contract. The following affected browser continuation passed
   with natural browser/daemon cleanup and complete Serve restoration.

The first independent result assessment accepted that composition but identified
missing public observations of the exact requested selection and approved roots.
A finite continuation read the same saved Question through its public API and
opened details, then checked both root/provider fields in Source Settings. It
passed. Its first preparation failed before browser/routes because the harness
omitted the service-derived intent/version/provider context from its expected
selection; the complete exact selection expectation was corrected. Both native
receipts remain separate, with no new question or provider attempt.

No product repair, new model call, timeout increase, whole common rerun or
unrelated historical browser campaign followed these harness failures. Current
syntax and documentation checks retain their own exact input scope. The
[Task04 record](multi-device-reconciliation.md) owns the passing corrected-product
common and its separately qualified completion overlay; unchanged production
inputs preserve that evidence rather than retargeting it to later full-tree bytes.

## Current versus historical provider evidence

| Evidence | What it establishes | Limit |
| --- | --- | --- |
| Current absent-executable fixture | Public provider status and UI report `provider_error/transport_unavailable`, `authentication_mode: unknown`; saved reading continues | The explicit nonexistent executable is refused before native startup. This is not a fresh account, subscription, native-permission or live-provider qualification. |
| [Task03 actual application Ask](mobile-reader-workflows.md) | One actual host-local Codex 0.164.0, `gpt-6.1-sol/high`, read-only/never, ChatGPT-mode attempt; exact acknowledged daemon-supplied source and useful saved partial answer | Its one-call approval is consumed. Broader inherited capability availability, unknowns and `signal_error_drained` cleanup keep their original qualifications. No fresh invocation is claimed here. |
| [Task006-05 source-absence answer](saved-questions.md#supplementary-actual-project-source-absence-observation) | Independently assessed useful real answer with explicitly empty project-source selection | Actual semantic source absence; it does not prove failed filesystem capture. Current Task05 supplies that separate zero-dispatch case. |
| [Task006-06 assembled AI workflow](ai-workflows.md) | Original real pinned-region, exact retained-input/citation and saved-read observations | Historical source/provider/semantic scope remains intact; current capture or later verification cannot retroactively establish an attempt's input. |

All 61 selected source/question/Codex/skill/generation/module inputs matched the
accepted Task03 baseline during applicability inspection. Task04 changed shared
client/auth behavior, so current web presentation was actually exercised here.
Source equality does not prove an unchanged external account or model environment.

## Reproduction and operational boundaries

Build the normal binary with `go build -o <private-dir>/haeryong ./cmd/haeryong`.
The driver accepts `--binary` for that inspected artifact, `--browser-cli` for
the already prepared browser wrapper, `--scratch-root`, `--receipt` and optional
`--screenshot`/`--keep-work`. For actual private HTTPS also provide:

```text
--management-port <loopback-port>
--renderer-pool-base <ten-unused-consecutive-loopback-ports-start>
--external-base-url https://<existing-node>:<management-port>
--renderer-origin-1 https://<existing-node>:<renderer-port-1>
--renderer-origin-2 https://<existing-node>:<renderer-port-2>
--renderer-upstream-1 http://127.0.0.1:<pool-base>
--renderer-upstream-2 http://127.0.0.1:<pool-base-plus-1>
--ready-file <private-dir>/ready.json
--serve-ready-file <private-dir>/serve-ready.json
--web-complete-file <private-dir>/web-complete.json
--serve-removed-file <private-dir>/serve-removed.json
```

The initial renderer-pool probe requires ten consecutive free loopback ports,
including the two static upstreams. Live mode then selects the separate fixed
dynamic pool `9281`–`9288`; reserve that range too. These are this opt-in driver's
reproduction settings, not product port requirements.

The Coordinator inspects existing routes/listeners before running the driver,
verifies the ready process, normal binary, non-repository CWD and mappings,
applies only its approved tailnet routes, then supplies the same-session
serve-ready signal. After web-complete it removes only unchanged owned routes,
compares the complete original Serve configuration and supplies serve-removed.
The default driver's subsequent remote-unreachability/local-health check was
not reached in the retained acquisition or affected browser continuations.
Actual browser closure, daemon exit and full Serve restoration are recorded
separately; they do not claim that unexecuted check. Never reset Serve or delete/reassign the per-user origin ledger.
Fresh versions require unowned origin tuples; recovery reuses the same exact
retained tuple. Permanent ownership is not temporary route ownership. This run added the two
approved permanent origin tuples to the account's existing ledger. That intended
ownership effect remains; it is separate from unchanged user-default settings.

Only newly owned source/import directories are removed. No working repository,
prior real-provider evidence, global credentials/defaults or other route is
modified. Retained fixture/evidence outputs remain under Coordinator custody for
the Task06 and Epic consumers; runtime paths and identities are not durable links.
No provider token forwarding, connector, provisioning, OS service installation
or release effect is included.

## Review, handoff and limits

The original independent plan review identified two missing proof descriptions;
the same Task Evaluator confirmed their finite clarification. Independent Task
Operator first coverage and the scoped Epic plan assessment remain distinct.
The first Task Operator and scoped Epic contribution assessments passed. The
first Task Evaluator identified the finite selection/root proof gaps above, an
inaccurate port reproduction description and an embedded home-path default.
The driver now requires an explicit prepared browser CLI, the port description
matches its probes, and the missing observations have actual affected proof.
Original assessments and failures remain intact. The same Task Evaluator confirmed all four findings resolved on the exact current
source. Original independent coverage, finite closures, current checks and
Coordinator product review establish a strict ready composition and exact
delegated member acceptance. Normal completion commit/native finalization remain
separate effects before TASK-007-06; this record grants none.

Physical second-device/mobile, Wails GUI, a genuinely nongraphical login session,
clean-machine desktop-installation absence and Linux runtime are `not_run`.
Owner participation and long-term usefulness are unmeasured. The source fixtures
are controlled material, not a new real-corpus semantic assessment. Task06 owns
the assembled original corpus/gate and portable internal package. Whole original
Stage A and Master overall Epic acceptance are not established here.
