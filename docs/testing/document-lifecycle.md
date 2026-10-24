# Document Archive, Trash and Restore

**Task:** [TASK-005-03](../tasks/EPIC-005/TASK-005-03.md), EPIC-005 — status `done`
**Implementation:** delivered: document-level archive/unarchive, trash and exact restore; impact plans, owner Trash UI and public CLI
**Source revision:** HEAD `24dd6882` plus the TASK-005-03 change set; the completion suite ran on the frozen final candidate equal to the committed tree; exact run bindings stay in private receipts
**Environment:** macOS arm64, prepared Go 1.27.1/Python 3.14.7/Node 26.7.0 and installed Chrome/Playwright CLI; no installs, provider calls or ingress changes
**Evidence status:** pass for the executed checks below, on the candidates stated; earlier failed attempts stay recorded as failures; no native Wails UI or physical-device evidence is claimed
**Implementer / independent reviewer:** Executor Writer; Task Evaluator, two non-authoring Executor reviewers and the Epic Evaluator; final project/product review and acceptance by the Coordinator

## Behavior and contracts

Archive remains eligible under explicit archived/all discovery. Trash records the
previous active/archived state, hides normal/personal/content reads and blocks new
personal writes with the existing lifecycle error. Restore returns the same document,
versions, references and personal records to that prior state. Review/Notes/Revisit,
original files, history and body/context ownership are preserved. Purge is unavailable.

The owner requests a 15-minute impact plan at the document revision, inspects all
metadata pages and explicitly confirms it. Selected document/config/permission,
explicit personal-state/history and incoming-reference changes require a new plan.
Automatic reading/page positions and preferences are informational and do not stale
an open plan. All current reader rows remain preserved. Incoming source visibility
is applied to nested items/counts on every GET page, keyed preview replay and operation
read; saved metadata cannot bypass a later source trash.

Closed request validation precedes receipt lookup. Invalid/missing/false confirmation
returns 400 even on committed replay; valid changed revision conflicts with 409.
Identical valid replay returns the original result even after expiry, before old
revision checks. Plan ID is the natural result identity; optional keys alias it without
replacing unrelated receipts. Result/audit/outbox/fences and receipt share one transaction.
The safe lifecycle result stays owner-reconcilable while ordinary personal receipts
remain hidden in trash. Runtime-generation changes never authorize automatic redispatch.

SQLite revision 12 is append-only and preserves migration bytes 1–11. Startup checks
all 8→9→10→11→12 declarations. Failed upgrades roll back and retain the verified prior
SQLite/object recovery set; older binaries refuse revision 12. Operator restore remains
TASK-005-05. Trash cancels captured import/derived work monotonically and advances a
retained delivery generation. Neither late results nor old delivery handles revive
after trash→restore. Renderer closure/resync clears protected views and preserves
pending owner intent without automatically sending it.

The [wire additions](../contracts/schema-registry.md#document-lifecycle-additions-task-005-03)
are additive provisional v1 routes/shapes, capability, CLI and named transport operations.
Shared embedded request definitions equal the selected machine subset; fixtures/digest
and committed browser/renderer assets are updated with their inputs.

## Verification mapping

| Requirement | Executable evidence / bounded scope |
| --- | --- |
| T-005-03-01; Obligation 4 | `query/lifecycle_test.go` labelled synthetic AI read through source.Check; public `tests/e2e/test_lifecycle.py` real archive/trash/restore with search, files/Range/HEAD, reference/backlink counts, personal/history/receipt visibility and exact restoration, including an asset shared by another retained document. EPIC-004 seeded target exclusion is repeated through real actions |
| T-005-03-02; Obligation 03/10; C09/M04 contribution | Public exact Note anchor/context bytes, Review/history, Revisit, pinned references, file bytes and restart with active/archived restore. Lifecycle changes leave personal records untouched; full purge/shared-GC and complete C09 remain TASK-005-04 |
| T-005-03-03; M04 contribution | Synthetic maintenance uses production typed target/application predicates before trash and after owner restore. Captured revision stays stale and no replacement/restore is applied; production provider/link repair belongs to TASK-006-06/TASK-008-06 |
| Plan/receipt | Persistence fake-clock expiry, first/replay invalid confirmation, changed valid revision, alias collision, parallel duplicates and injected document/outbox/plan/receipt rollback; public keyless/keyed matrix and CLI plan/show/commit/Trash |
| Current visibility | Public source trash after planning, paginated plan GET, keyed preview replay and operation read all exclude nested source metadata/counts; commit reports changed impact. Owner/scoped live event masking has persistence checks |
| Stream/renderer; M08 contribution | `v1/lifecycle_test.go` interrupts a real saved-context HTTP stream after real trash. Bounded Chrome lifecycle driver covers four viewports, safe Enter/cancel, explicit keyboard confirmation, delayed automatic position acknowledgement, two-session renderer closure and fresh-session restoration. Existing forged management-message regression remains in viewer/channel.test.ts |
| Obligation 9 | Authentic 11→12 migration rollback/recovery-byte/startup/declaration test and existing declaration checks at current revision; closed contract fixtures/embedded subset/digest, frontend operation tests and desktop facade/parity race checks |

Focused checks ran on the candidates stated in their receipts and are not relabelled as
final-candidate proof; the completion suite on the final candidate is recorded under
Review and acceptance.

## Failures and intended-behavior changes

Initial new-test repairs: Review GET has no mutation operation_id, so preservation
compares the stored GET projection rather than claiming a new mutation identity.
Type checking required explicit numeric-limit serialization into the HTTP collection
query. E2E imports use the harness's existing relative module convention. These failures
are retained in private receipts; no passing label is inferred from extracted counts.

Policy F: the existing pre-search reverse fixture removes revision-12 dependent
plan tables/index/columns before constructing older schemas. The source is the approved
append-only migration/old-schema test obligation, not an expectation changed to match
a defective live downgrade. The first complete persistence stage also exposed prior-row comparators that included revision-12 additions in schema7/8 identities. They now compare every original document column and prior table byte exactly, while separate assertions require empty lifecycle plan tables and null/zero lifecycle defaults. Original negative declaration and checksum tests remain.
Browser setup initially treated a focused dialog container as a destructive button; safe close focus and the button-identity assertion are now explicit. Same-context sibling Reader readiness exposed redundant per-widget event subscriptions; visible resources now share the controller stream, with an authority-bound fan-out regression and a same-context sibling browser check. Nullable response schemas use the fixture validator's existing anyOf convention; no validator relaxation was needed.

The controller test double adds only the new interface methods with failing unused
stubs. No assertion weakening, skip, timeout or retry allowance is introduced.

## Reproduction and limits

Use authorized Gaori execution with prepared offline dependencies. Focused Ginkgo
uses race detection and Lifecycle focus; public selection is `tests/e2e/test_lifecycle.py`.
Selected fixtures use the entire `tests/contracts` suite for its coverage hook. New
frontend tests are registered in both package.json and check_build.py. Desktop selection
is `go -C desktop test -race -count=1 -run 'TestLifecycle|TestReaderNativeOperationsParity' ./readerfacade`.

The bounded existing Chrome driver adds `--lifecycle-only`:
`python3 -B internal/adapters/http/auth/browser/check_reader.py --lifecycle-only --browser-cli <installed-playwright-cli> --receipt <private-json> [--screenshot <private-png>]`.
It owns its profiles/ports, fingerprints production inputs and closes its browser/daemon.
Pre-C1 lifecycle receipts covered viewports, mouse and keyboard only; their touch claim was incorrect. The C1 driver adds an explicit hasTouch phone context with trusted tap input through More, impact cancel/confirmation, Trash and Restore. This is emulated touch, not a physical device. No native Wails UI, physical second device, private ingress,
real-provider, every-platform, backup/purge or release qualification is claimed.

## C1 correction evidence

RR1-F1 separates current document readability from the existing delivery's authority.
Invalidation/reconnect/resync also renews the captured delivery; a refused lease
retires its channel and frame and clears protected host state, without opening a
replacement. A fresh explicit navigation is required. Browser barriers complete a
second owner's trash→restore before a successful version recheck, then observe the
old delivery's real renewal refusal. A dropped event stream and real resync repeat
that closure; lease unit tests also cover scheduled refusal and shared rechecks.

RR1-F2 compares complete new column declarations and rejects added/changed constraints
at startup for all three revision-12 columns. Historical declaration checks remain.
RR1-F3 holds an accepted production import after generated-byte publication and a
production derived worker at its object read, outside the SQLite gate. Actual owner
plan/commit trash→restore advances cancellation/attempt and trash generations;
resumed attempts must not publish a replacement version or apply body postings.
RR1-F4 corrects the historical touch attribution and adds the explicit tap flow above.

## C2 presentation corrections

The repository/documentation indexes place this Task beside Notes/Revisit. The
impact dialog explains each operation's visibility and preservation effects;
incoming references show access-state effects rather than preservation, and unresolved references stay visibly unresolved.
Trash uses the existing Korean lifecycle labels and localizes both deletion and
informational purge-eligibility timestamps consistently. No wire or lifecycle
contract changes. The Chrome fixture imports a real referring document and sets
one-day retention through public configuration plan/commit, then checks the
operation text, incoming rows and active/archived restore labels with localized
timestamps. The existing keyboard, renderer/resync and hasTouch tap cases remain.

## Review and acceptance

The Task Evaluator, the two non-authoring Executor reviewers and the Epic Evaluator
passed plan r2, after r1's conflicting 400/409 rule for a changed confirmation on a
committed replay was settled. The first result review returned rework for a renderer
that survived a fast trash→restore, a substring declaration check, the missing real
import and derived-work races and a touch claim without touch input; C1 corrected all
four. The Task Evaluator, both reviewers and the Epic Evaluator then passed the full
Task. The Coordinator's final review found misplaced index entries, an impact dialog
that did not state each operation's effects and raw Trash list values; C2 corrected
them, and the Task Evaluator, both reviewers and the Epic Evaluator passed the result.
The Coordinator completed the final project/product review and accepted the Task under
Master's delegated authority; overall EPIC-005 acceptance remains pending.

On the final candidate the whole completion suite passed: the canonical `make test`,
every stage through the no-fail-fast execution, frontend build equality and unit tests,
and the desktop race and vet run.
