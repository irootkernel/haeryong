# Explicit version Review

**Task:** [TASK-005-01](../tasks/EPIC-005/TASK-005-01.md), EPIC-005 — status `done`
**Implementation:** delivered: explicit whole-version Review, history, Home summaries and the last-Reviewed comparison default
**Source revision:** HEAD `f710238` plus the TASK-005-01 change set; the completion suite ran on the frozen final candidate equal to the committed tree; exact run bindings stay in private receipts
**Environment:** macOS arm64, prepared Go 1.27.1/Python 3.14.7/pytest 9.1.1/Node 26.7.0 and installed Chrome/Playwright CLI; no installs, provider calls or ingress changes
**Evidence status:** pass for the executed checks below, on the candidates stated; earlier failed attempts stay recorded as failures; no native Wails UI or physical-device evidence is claimed
**Implementer / independent reviewer:** Executor Writer; Task Evaluator, two non-authoring Executor reviewers and the Epic Evaluator; final project/product review and acceptance by the Coordinator

## Delivered behavior

Review is an explicit owner action on one complete immutable directory version.
Start, complete, reopen and clear follow the existing transition table. Completion
time is server-assigned; no-op/replay preserves it. Reading a page, scrolling,
visiting every page, synthetic AI assessment and generated-frame messages do not
mutate Review. New versions begin unreviewed while old completion/history survives.

State, history, redacted audit/outbox and the receipt share one transaction. The
individual ETag/create control belongs to this owner/version. A new stale write is
412, including a no-op; matching committed replay returns the original result before
rechecking its old condition. The CLI journal and browser/native clients preserve
original keys and conditions through response loss. Runtime-generation changes require
explicit reconciliation, not an automatic new key. Review is owner-only; scoped
summary fields are null and hidden event/receipt output is redacted.

Home uses pre-pagination owner Review filters and exact links. Continue can name an
older reviewing version; Updated compares latest sequence with the most recent valid
explicit completion. Reopen/clear removes current completion eligibility without
erasing events. Source freshness and semantic assessment remain separate. Revisit
stays with TASK-005-02. Versions honors explicit pair IDs, otherwise uses last-Reviewed
with predecessor fallback, freezes the resolved pair, and reads every row's exact
version Review. Missing capability is unavailable, never an inferred unreviewed state.

The [wire registry](../contracts/schema-registry.md#explicit-version-review-additions-task-005-01)
selects additive provisional shapes and optional document-summary fields. PUT results
include operation_id; GET does not invent a mutation identity. Schema/API versions stay
1.0/v1. SQLite migration 10 is append-only; startup verifies its exact tables/indexes.
Older binaries refuse the new revision. Recovery uses the existing verified
pre-migration SQLite/object set; application protected restore remains TASK-005-05.

## Verification map and scope

| Requirement | Executable evidence and assertion |
| --- | --- |
| T-005-01-01 / C08 contribution | `internal/application/query/review_test.go` unit/persistence transition and reading independence; frontend `review-operations.test.ts`; bounded Chrome Reader proof of forged frame/synthetic minor-assessment messages, multi-page reading, explicit keyboard Start and 390×844 emulated-touch completion |
| T-005-01-02 | `tests/e2e/test_review.py`: real public imports to v3/v4, independent positions, old completion/time/history and new absent state through daemon restart; summary last-completion fallback and old Reviewing continuation |
| T-005-01-03 | Two owner devices and public 412 current metadata; persistence stale/no-op/concurrent duplication and injected history/audit-outbox/receipt failures roll back all durable pieces |
| X11 | HTTP/CLI key/control validation, same-key replay after stale ETag, changed body/precondition conflict, parallel duplicates, lost committed response with original-key recovery; scoped/lifecycle receipt visibility and event redaction in persistence |
| N2 | Current screens/flows, schema registry, HTTP/CLI contracts and root status paragraphs describe Review; earlier TASK-004-05 evidence remains explicitly historical |
| N3 | `frontend/src/client/review.test.ts` and `desktop/readerfacade/review_test.go` assert both review filters and next-page cursor plus exact action/body/precondition/key; operation parity separately checks closed names |
| N4 | `review-summary.test.ts` and the actual Chrome Versions proof display exact v3 Reviewed/v4 Unreviewed and last-Reviewed default; an explicit source always wins |
| Migration | `sqlite/review_migration_test.go`: authentic revision-9 startup, injected rollback, preserved prior manifest/object recovery bytes, restart and missing-declaration refusal; prior reference migration/index regressions use current SchemaRevision while retaining original old migration bytes |
| Contracts | `tests/contracts/test_review_contracts.py` validates closed requests, exact absent/default/existing values and history; the full selected fixture suite checks embedded-schema equality and unchanged selected case coverage |

New frontend unit files are registered in both package.json and check_build.py.
SQLite tests use Ginkgo `persistence`; pure transitions/CLI arguments use `unit`.
Contract fixtures use pytest `integration`, public daemon scenarios use pytest `e2e`.
No production test endpoint or new dependency is introduced.

Focused implementation runs on 2026-10-24 include race Ginkgo Review/personal/reference
checks, public pytest Review E2E, selected contract fixtures, frontend type/unit/build
regeneration, desktop Review request tests and bounded Chrome Review/Home/Versions.
They passed on the candidates stated in their private receipts; they are not
substituted for the final-candidate completion suite.

Earlier failures remain disclosed: missing methods on the controller test double,
a duplicate local declaration and an immutable scoped-credential fixture update;
HTTP test header assertions initially treated Headers as a plain object. Contract
parity exposed an unsynchronized embedded schema. Reference DDL tests needed the
current SchemaRevision instead of revision 9, so their negative tampering assertions
remain effective. A browser socket path exceeded the macOS limit; the installed
CLI's supported socket-directory environment setting keeps it inside short task scratch.
Browser keyboard proof then exposed routine invalidations removing a focused Review
button; in-place authenticated refetch now preserves focus, pending intent and history.

## C1 correction verification

Result review identified a batch-filter early return, permissive personal If-Match
syntax and a missing reconciliation error mapping. Event replay now filters every
item and matches individual filtering for mixed visible/hidden Review and later
hidden document/version events, for owner and scoped readers. Personal PUT accepts
one quoted individual validator (including an opaque comma), or create-if-absent
`If-None-Match: *`; malformed/list/duplicate controls return 400, missing controls
428 and well-formed stale controls 412. This follows the existing individual
precondition rules in the registry and HTTP contract; no wire contract changes.
A reconciling Review receipt returns 409 OPERATION_RECONCILIATION_REQUIRED and
retains CLI exit 6 without mutating Review or history.

Migration 10 adds Review tables to current-schema fixtures. The shared test-only
reverse constructor now removes those dependent tables before building a pre-search
schema, including revisions 2/4/6/7. Production migration bytes, schema validation,
rollback fault assertions and immutable recovery comparisons remain unchanged.
The existing migration contract is the intended-behavior source for that fixture
change. New regressions are `auth/review_events_test.go`, `v1/review_test.go`, the
CLI classification spec and the public HTTP precondition case in `test_review.py`.
The first completion run had stopped at those four persistence failures, leaving
seven persistence suites and the E2E stage unexecuted; after C1 the complete
persistence stage (458 specs) and the E2E stage (60 tests) passed on the corrected
candidate.

## C2 correction verification

The compiled Review CLI now uses the existing `afterDefinitive` classification.
Direct API errors do not invent a terminal receipt or mark the journal failed.
Reconciliation/unknown-provider demands, authority denials, unrecorded 5xx,
transport loss and local decoding errors retain the automatic key and original
request. A later invocation reads the same receipt without another PUT. Recorded
terminal failures and ordinary 4xx rejections release the key only after emission.
This follows the existing [CLI recovery contract](../contracts/cli.md#global-behavior).

`cmd/haeryong/review_cli_recovery_test.go` runs the compiled CLI against an isolated
loopback peer with controlled wire outcomes and receipt responses. Its integration
specs verify pending/journal/condition preservation, one simulated effect, failed
output acknowledgement, recorded terminal 503 release and unchanged non-Review
project-registration recovery. These are controlled-peer integration tests, not
additional live-provider or real-daemon qualification. Existing public Review E2E
and contract inputs are unchanged.

## Reproduction

Use authorized Gaori execution with the prepared offline toolchain. Focused Go uses
`go tool ginkgo --race --keep-going --fail-on-empty --no-color --timeout=300s
--focus='Review|Personal|RF06' ./internal/application/query ./internal/adapters/sqlite
./cmd/haeryong`. Public checks are pytest `tests/e2e/test_review.py`; selected fixtures
are the whole `tests/contracts` directory so its coverage hook can verify original IDs.
Use strict pytest config/markers/warnings, plugin autoload disabled and unique private
basetemp/report paths under the current test run.

The bounded interaction command is `python3 -B
internal/adapters/http/auth/browser/check_reader.py --review-only --browser-cli
<installed-playwright-cli> --receipt <new-private-json> [--screenshot <private-png>]`.
It owns isolated profiles/ports, closes its browser session/daemon, fingerprints
production inputs and labels synthetic assessment data. It requires no external CDN.
The two Review JavaScript driver hashes are retained with its private receipt.

## Review and acceptance

The Task Evaluator, the two non-authoring Executor reviewers and the Epic Evaluator
passed the plan. The first result review returned rework for the event batch filter,
the If-Match syntax and the reconciliation mapping; the first completion run added the
prior-schema fixtures. C1 corrected them and the full-Task reassessment confirmed the
repairs, with one further finding on CLI key retention that C2 corrected and the Task
Evaluator and a reviewer confirmed. The Epic Evaluator assessed the final product
candidate. The Coordinator completed the final project/product review and accepted the
Task under Master's delegated authority; overall EPIC-005 acceptance remains pending.

On the final candidate the whole completion suite passed: every `make test` stage,
frontend build equality and unit tests, and the desktop race and vet run. The focused
and browser runs above executed on the candidates stated. These results do not certify
the complete C08/M04/M18 scenarios, EPIC-005 recovery, native Wails UI, a private
second device, other host platforms or release installation. Revisit, notes and the
Home Revisit list remain TASK-005-02, which also carries the display polish found in
final review: readable version cues and project names in Home rows, Korean history
labels and localized times in ReviewControl, and Home formatting. Real lifecycle,
protected restore and the owner-records gate remain TASK-005-03..06.
