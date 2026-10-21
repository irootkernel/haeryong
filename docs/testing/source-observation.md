# Source verification and observation evidence

**Task:** [TASK-004-01](../tasks/EPIC-004/TASK-004-01.md) (EPIC-004) — status `done`
**Implementation status:** implemented bounded source/check/client capability
**Evidence status:** pass for the executed final-candidate check set below; Task acceptance is separate
**Source revision:** HEAD e6285b8 plus the TASK-004-01 change set; the final evidence ran on the frozen candidate whose code, tests, frontend, dist and contract fixtures equal the committed tree (later changes are documentation only).
**Environment/toolchain:** macOS arm64; Go 1.27.1, Node 26.7.0, Python 3.14.7; repository-pinned frontend/desktop modules and prepared Python environment
**Implementer / independent reviewer:** Executor Writers and the Supervisor; Task Evaluator, two non-authoring Executor reviewers and the Epic Evaluator; final project/product review and acceptance by the Supervisor

## Actual outcome

The approved reader now checks imported evidence at its authored exact revision
without AI, preserving authored provenance beside separate captured/matched/
mismatch/unavailable results and history. Owner-triggered observation resolves
approved mapped Git HEAD once, compares each citation's declared base, persists
availability/change observations and exact-target structural triggers, and exposes
fixed required-unit coverage. It does not publish semantic assessments or change
personal Review. All ordinary multi-page directories remain importable/viewable
without metadata, index.html, regions or author manifests; the
[personal-tool](../changes/2026-10-10-personal-tool-revision.md) and
[directory-result](../changes/2026-10-10-directory-results-revision.md) directions apply.

The shared browser/native/Go transports expose project registration, source
configuration through existing plans, logical source status and explicit evidence
checking behind capabilities.features.source_verification. Sources has no
observation button: the CLI/API is the observation entrypoint in this Task.
The SourcePanel shows "확인 전" for not_read_by_view, missing/denied states
explicitly, and direct/indirect/no-citation coverage limitations without a semantic
verdict. Retained bytes may remain readable when the original checkout is missing.

## Case evidence

The Supervisor executed the final checks below on one frozen code candidate;
these final-candidate runs supersede the earlier per-part implementation runs.
Logs, screenshots and receipts remain private. The check labels C1–C9 are local
cross-references within this record, not runtime identities. Documentation
integrity is repeated after the documentation-only record update.

| Check | Evidence label and fixture | Command or procedure | Actual result and limit |
| --- | --- | --- | --- |
| C1 Canonical gate | Synthetic Git fixtures; real SQLite/object storage; seeded persistence fixtures; real-daemon E2E | `make test` | pass: prepare includes gofmt, docs validation, go mod verify, go vet and four cross-builds; Go race specs unit 93, integration 33, persistence 339; pytest unit 10, integration 46, E2E 46; no failure or skip. These fixtures do not constitute real-project semantic or private-device acceptance. |
| C2 Documentation integrity | Static documentation | `python3 -B docs/testing/validate_docs.py` | pass; issues empty. No application behavior claim. |
| C3 Contract fixtures | Synthetic fixture clients, no listener/provider | `python3 -B tests/contracts/run.py --report "$EVIDENCE/contracts.json"` | pass: 40 tests. Selected machine shapes and fixture behavior, not production durability. |
| C4 Frontend build validation | Synthetic type/unit checks and scratch builds over actual frontend inputs | `python3 -B frontend/check_build.py` | pass: typecheck, unit and scratch builds; four committed dist files byte-identical. No asset regeneration was needed for these final checks. |
| C5 Frontend unit | Synthetic ports and models | `cd frontend && npm run test` | pass: 306 tests, three suites, zero failures. Models do not establish browser/native execution. |
| C6 Desktop module | Synthetic facade/backends | `go -C desktop test -race -count=1 ./...`; `go -C desktop vet ./...` | pass: three packages (companion, onboarding, readerfacade), including operation parity and forwarding; vet clean. Forwarding tests are not native UI evidence. |
| C7 General browser | Browser: real Chrome, production HTTP/service/SQLite fixture, loopback fault proxy and emulated touch | `python3 -B internal/adapters/http/auth/browser/check_browser.py --fixture-binary "$EVIDENCE/fixture" --browser-cli "$BROWSER_CLI" --receipt "$EVIDENCE/browser.json"` | pass: 93 true proof fields. The false issuance_observed.stored field records the observed storage fact, not a failed proof. S09 registration/preview/commit/cancel/stale/impact/remount/keyboard/touch and logical identities without host paths pass. No physical device or private ingress. |
| C8 Reader evidence browser | Browser: real service checks plus labelled seeded presentation proxy and scripted first-commit fixtures | `python3 -B internal/adapters/http/auth/browser/check_reader_evidence.py --browser-cli "$BROWSER_CLI" --receipt "$EVIDENCE/reader.json" --screenshot "$EVIDENCE/reader.png"` | pass: 58 of 58 named cases; seeded proxy errors zero; browser and daemon closed. Seeded counts/status/history presentation is distinct from a real source observation. |
| C9 Native Reader | Native: pinned Wails beta28 ReaderMode harness, typed facade and real tagged daemon; labelled seeded presentation backend | `python3 -B spikes/viewer/check_reader_native.py --stages app,evidence --receipt "$EVIDENCE/native.json"` | pass: three scenarios—compiled native Reader context; real-daemon App with enabled source check and durable unavailable outcome/history; labelled seeded presentation. Full qualification false; daemon closed. Only the selected stages are evidenced. |

Commands use the existing prepared Python environment and existing approved
Playwright CLI. EVIDENCE is an operator-selected private output directory and
BROWSER_CLI names that installed CLI; neither denotes a retained runtime path.
Output filenames must be new. The browser fixture is built with
`go build -o "$EVIDENCE/fixture" ./internal/adapters/http/auth/browser/cmd/fixture`.
The canonical gate is the serial prepare/unit/integration/persistence/E2E
contract in [TESTING.md](../../TESTING.md); the desktop module is separate.

## Completion traceability

The assertion IDs and three deliverables below come from
[TASK-004-01](../tasks/EPIC-004/TASK-004-01.md). Each named spec body and driver
scenario was read to confirm its actual assertions; the final check column
identifies the check that executed it. Driver scenario names are proof keys,
not receipt identifiers. Seeded presentation supports the displayed limitation,
not the underlying source fact. The table reports bounded implementation proof;
it does not independently change Task lifecycle or accept a broader Epic gate.

| Completion assertion or required deliverable | Covering specs (file and exact Describe/It or test title) | Driver scenarios | Layer and final check | Scope of proof |
| --- | --- | --- | --- | --- |
| T-004-01-01 | `internal/application/source/observation_test.go`, Describe("observation runs"): It("sets target-scoped reassessment for a direct change and isolates families"), It("discloses an unexamined dependency scope without ever claiming outdated"), It("keeps captures and triggers stable across restart"); `internal/application/source/observation_attribution_test.go`, Describe("observation records"): It("captures the resolution-time HEAD once") | C8 real_assessments displays the real service composition; it is not the direct-change oracle | Persistence with synthetic Git and real storage: C1; browser consumption: C8 | X15 supplemental contribution: direct/indirect classification, exact capture/time/target, target-bound triggers and restart stability. F05 contributes indirect disclosure only, without a daemon-set semantic trigger. No assessment-completion producer or semantic currentness is claimed. |
| T-004-01-02 | `internal/application/source/observation_test.go`, Describe("observation runs"): It("records an all-unavailable first run without fabricating a target"), It("attributes unit reasons per source on a partial run"), It("captures page and region scopes without fabricating anchors"); `internal/application/query/evidence_view_test.go`, Describe("evidence view states (W2c)"): It("returns state 1 denied for a scoped reader without source.read"); `frontend/src/app/screens/source-presentation.test.ts`: test("not-read is neutral; missing source F07, denied F10, retained missing and corrupt stay explicit") | C8 fixture_multicursor_coverage_complete and fixture_proxy_no_handler_failures; C9 seeded.unit-reasons-visible | Persistence: C1; synthetic presentation: C5; browser/seeded: C8; native/seeded: C9 | Fixed page/version/optional-region denominator and explicit unavailable/denied reasons; neither incomplete nor indirect coverage can establish verified current source. Seeded native/browser presentation is not a real semantic assessment. |
| T-004-01-03 | `internal/application/auth/scope_routes_test.go`, Describe("scoped route authorization for source operations"): It("answers nondisclosing 404 for out-of-scope versions (C3)"); `internal/application/source/verification_test.go`, Describe("staged imported-evidence verification"): It("runs the four outcomes without touching authored evidence") repeats approved checks; `internal/adapters/gitreader/reader_test.go`, Describe("system git reader"): It("keeps reading identical bytes while the working tree and branch move"); `internal/application/source/verify_more_test.go`, Describe("verification corrections"): It("fails typed when the configuration generation advanced between stages"), It("records the registry requested selection and per-source target shape (C13)"); `internal/application/query/assessments_test.go`, Describe("assessments selection"): It("selects a verification-created first capture for the pinned family"); `cmd/haeryong/source_cli_workflow_test.go`: It("TestActualCLISourcesApproveMergesAndKeepsSources") | C7 s09_scope_impact_confirmation, s09_stale_plan and s09_runtime_remount_registration_plan_commit | Integration Git reader and persistence authorization/configuration/CLI: C1; browser: C7 | Saved project/action anchors remain authoritative, not a link or citation; repeated approved checks need no per-read approval. Pinned default/selection, preserved provider choices and fixed exact bytes are exercised. A mid-check policy change fails rather than retargets. Cross-project navigation acceptance and unrestricted external-agent containment are not claimed. |
| T-004-01-04 | `internal/application/source/verification_test.go`, Describe("staged imported-evidence verification"): It("runs the four outcomes without touching authored evidence"), It("replays receipts, normalizes expectations and returns the stable 202 pending envelope"); `internal/application/source/restart_sweep_test.go`, Describe("restart sweeps resolve crashed staged checks (S11)"): It("completes a receipt for a committed verification after restart"); `internal/adapters/sqlite/source_migration_test.go`, Describe("schema7 source records"): It("persists captures, reads and verifications across restart including null unavailable fields") | C8 source_check_captured_history_and_digest, authored_service_separation and remounted_source_check_uses_original_receipt; C9 source-check.enabled and source-check.real-outcome-and-history | Persistence: C1; browser real service: C8; native real daemon: C9 | Four outcomes, exact expectations, unchanged author bytes, replay and restart durability; actual UI check/history. No provider call, semantic support or observed generation input follows. |
| Deliverable: approved source registry and read models | `internal/application/source/verify_more_test.go`, Describe("project registration"): It("registers, replays and rejects invalid names"); `cmd/haeryong/source_cli_workflow_test.go`: It("TestActualCLISourcesApproveMergesAndKeepsSources"); `internal/application/source/observation_test.go`, Describe("observation runs"): It("records an all-unavailable first run without fabricating a target") asserts recorded ProjectSources fields | C7 s09_registration, s09_preview_confirm_commit, s09_logical_identity_no_host_path | Persistence registry/configuration/read models: C1; browser: C7 | Durable logical registration, remembered roots/provider choices and recorded source availability; no remote filesystem browser. |
| Deliverable: minimal exact-revision reader/change observer, verification/history, enabled Evidence action and reader status API | `internal/adapters/gitreader/reader_test.go`, Describe("system git reader"): It("streams exact blob bytes at the pinned revision"); source verification and observation specs in T-004-01-01/-04 above; `internal/application/query/assessments_test.go`, Describe("assessments selection"): It("composes the status source section with the reason precedence"); `desktop/readerfacade/operations_parity_test.go`: TestReaderNativeOperationsParity | C8 source_check_captured_history_and_digest and real_assessments; C9 source-check.enabled and source-check.real-outcome-and-history | Integration/persistence: C1; synthetic desktop: C6; browser: C8; native: C9 | Exact bytes and recorded source status through the shared transports; real enabled action/history, with a truthful unavailable native result rather than a fabricated successful source read. |
| Deliverable: S09/source context states and direct/indirect/missing evidence fixtures | Direct/indirect/all-unavailable specs in T-004-01-01/-02 above; `frontend/src/app/screens/source-configuration.test.ts`: test("source settings preview binds the revision and scope, cancel never commits, and scope expansion requires confirmation"), test("stale plans stay explicit and cannot be committed again until reloaded and previewed"); `frontend/src/app/screens/source-presentation.test.ts`: test("coverage reason presentation discloses dependency scope and preserves source/reassessment/no-citation reasons") | C7 s09_cancel_no_commit, s09_stale_plan, s09_keyboard and s09_touch; C8 authored_service_separation and seeded_history_labels_truthful; C9 seeded.authored-not-upgraded | Persistence source facts: C1; synthetic presentation: C5; browser and seeded presentation: C7/C8; native seeded presentation: C9 | Direct, indirect, missing and denied states remain distinct; F05 is candidate impact disclosure only. Touch is emulated and seeded history is presentation evidence. |

## Knowledge and user evidence

These checks are post-hoc byte/target checks, never observed_input citations or
proof of provider inputs, semantic support or currentness. The
[knowledge-quality worksheet](knowledge-quality.md) and its prior qualifications
are preserved. No provider call, human usability study, physical-device test,
private ingress or real two-project search/navigation gate is claimed by this
check set. Later Epic owners retain those broader observables.

## Contract, state and recovery effects

The [HTTP contract](../contracts/http-api.md#source-observation-delivery-task-004-01)
and [registry](../contracts/schema-registry.md#source-and-assessment-views) define
additive catalogued routes, result fields/errors/events and capability flag;
source-observations and the selected source projection are additive provisional
contracts. Runtime metadata/config schema stays 1.0. The
[data model](../data-model.md#source-observation-persistence-task-004-01-migration-7)
records exact migration-7 columns, required run attribution and nullable
all-unavailable captures. No job_inputs or new job kind is created.

A latest matching committed attempt A supplies status/availability reasons;
latest matching valid capture C supplies the exact target/time/denominator and
trigger visibility. Only C's target/anchors expose triggers. Explicit targets
never substitute another capture; pinned verification/current observation
families remain separate. `units_url` preserves attempt_run_id when A differs;
plain set reads preserve stored rows. Clients retain that ID across pages/retries.

Reserve/pin, detached read and short authority-rechecked commit keep the store
writer non-reentrant. Recovery reconstructs a committed result or settles an
interruption; contradictory receipt/history prevents guessed success. Stored
failure envelopes retain request_id and retryable false, including terminal 503;
unrecorded adapter 503 failures use retryable true. Clients reconcile the
original operation across 201/202/unknown outcomes. The controller-owned intent
store survives selection/component/runtime remounts and retains completed/failed
results for the active consumer; stale selections cannot apply late results.
The CLI coverage listing enumerates all cursors before emitting a full set.

## Review and handoff

The executed final-candidate checks above passed. The Task Evaluator and two
non-authoring Executor reviews passed the final result; the Epic Evaluator then
approved this Task in its Epic context, and the Supervisor accepted it after the
final project/product review under Master's delegated authority. Overall Epic
acceptance remains pending. Later changes were documentation only and preserve
the tested code, tests, frontend, dist and contract fixtures; the canonical gate
was repeated on the committed tree.

Browser emulated touch is not a physical-device test. Loopback production
fixtures are not private-ingress or two-project real-sample product acceptance.
The native run exercises the reused pinned ReaderMode harness and selected
app/evidence stages with full qualification false, not full companion/platform
qualification. Desktop forwarding tests are synthetic. Seeded presentation
cannot prove an actual source observation or semantic verdict. No provider or
release claim is added by these results.
