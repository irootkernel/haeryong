# Anchored Notes, conflicts and Revisit

**Task:** [TASK-005-02](../tasks/EPIC-005/TASK-005-02.md), EPIC-005 — status `done`
**Implementation:** delivered: exact anchored Notes with history, conflict-safe drafts, saved-context streaming, inert Markdown and independent Revisit
**Source revision:** HEAD `babd12c8` plus the TASK-005-02 change set; the completion suite ran on the frozen final candidate equal to the committed tree; exact run bindings stay in private receipts
**Environment:** macOS arm64, prepared Go 1.27.1/Python 3.14.7/Node 26.7.0 and installed Chrome/Playwright CLI; no installs, provider calls or ingress changes
**Evidence status:** pass for the executed checks below, on the candidates stated; earlier failed attempts stay recorded as failures; no native Wails UI or physical-device evidence is claimed
**Implementer / independent reviewer:** Executor Writer; Task Evaluator, two non-authoring Executor reviewers and the Epic Evaluator; final project/product review and acceptance by the Coordinator

## Behavior and contracts

Notes bind one exact immutable directory version and an optional resolved HTML page
and page-local region. Page query/fragment remain separate navigation context.
Original_anchor remains immutable. Reading a new version, reused path/slug or identical
region label never transfers an old note. Undeclared/missing region anchors remain
unresolved with selected-text/context fallback. Explicit same-document reattachment
retains history and requires a valid exact destination; an owner may deliberately
choose page/whole-version fallback rather than treating an ambiguous region as a match.

Save alone dispatches. Cancel discards unsent/rejected drafts, while unknown requests
keep their original key, conditions and captured context. Drafts and pending requests
are separate, owner/library/version-scoped local records. A 412 comparison retains
local text and the server's newer revision; reload or deliberate merge/new Save is
explicit. Refresh does not replace the local draft. Actual local storage failure is
reported without silently losing the on-screen text. Large bodies automatically use
the existing resumable body-upload protocol with distinct preparation keys.

State, immutable revision history, body/context adoption, redacted audit-bearing outbox
and operation receipt commit together. Every note has its own ETag. Receipt replay
precedes new-work CAS and does not duplicate history or object ownership. Soft deletion
hides current content but preserves permitted history. Context/history objects remain
owned across renderer disposal and original-session loss. Owner-authorized streams
select an annotation and saved revision, with exact bytes/digest/length/media; arbitrary
context handles or native URLs grant no authority. Delivery rechecks authority and
cancels after revocation. Fresh-frame context proof retains its separate body-less meaning.

Revisit is document-level and independent of Review. Absent false and stored false are
distinct. Home's Revisit filter runs before pagination and composes with existing filters.
Owner-only personal fields remain null for scoped readers. Capabilities signal availability,
not permission. Review, Home continuation and Versions defaults retain TASK-005-01 behavior.
Home omits raw version/project UUID display; ReviewControl uses readable version cues,
Korean history labels and localized timestamps.

The [wire additions](../contracts/schema-registry.md#anchored-notes-and-revisit-additions-task-005-02)
are **additive provisional**: closed note CRUD/history/body/context shapes and routes,
Revisit shapes/filter/summary field, named transports and capability flags. Schema/API
versions stay 1.0/v1. Embedded definitions equal their selected machine copies by
subset equality; the machine schema also owns response-only definitions. Applicable
selected fixtures and digests stay synchronized. SQLite migration 11 is append-only; it preserves previous migration
bytes and verifies search/references/Review/new declarations. A failed upgrade rolls back;
recovery uses the verified pre-migration DB/object set. Older binaries refuse revision 11.

## Verification mapping

| Requirement | Executable evidence and scope |
| --- | --- |
| T-005-02-01 / X19 / C07 contribution | `query/annotations_test.go`, public `tests/e2e/test_annotations.py` and bounded Chrome Notes: same-named regions on two pages, no-region and directory notes, query/fragment, changed v2, restart, original/unresolved views and explicit reattachment/history. No automatic mapping engine or later TASK-007-04 acceptance is claimed. |
| T-005-02-02 | Persistence concurrent CAS/replay/rollback; public two-session stale edit with current validator; `notes-operations.test.ts` draft/journal and resumable preparation; actual Chrome local/server comparison and deliberate merge. |
| T-005-02-03 | `safe-markdown.test.ts` plus actual Chrome saved hostile markup: script, image/event-handler, SVG and executable-link inputs remain inert. Saved, history and comparison previews share one renderer. |
| B-1 | Public exact current/history context bytes before/after restart and originating-session revocation, another authorized owner session and absent/deleted-revision rejection. `v1/annotations_test.go` stops a real HTTP stream after revocation. HTTP/native tests and `desktop/readerfacade/annotations_test.go` verify closed annotation/revision payload, metadata, exact bytes, stale-authority cancellation and operation parity. |
| Transaction and lifetime | `query/annotations_test.go` injects history/pin/outbox/receipt failures and barriers around Save versus collection; accepted adoption retains exact bytes and failed mutations leave no partial note/history/receipt. |
| Revisit / COORD-1..3 | Public Revisit CLI and filtered Home data; persistence absent/stored-false, owner/scoped/event/receipt visibility and Review independence; compiled CLI uncertain-outcome key retention; Chrome touch toggle/Home and readable existing Review/Home presentation. |
| Migration/contracts | `sqlite/annotation_migration_test.go` authentic 10→11 rollback, retained recovery bytes, restart/declaration refusal. Existing search/reference/Review tests retain their assertions at current SchemaRevision. Closed contract cases and `tests/fixtures/annotations/conflict.json` remain synthetic inputs, not execution proof. |

Focused runs on 2026-10-24 established the public Notes cases, client type/unit/build
checks, closed contract fixture suite, facade request/stream checks and the bounded
Chrome Notes workflow on their stated candidates. No inherited TASK-005-01 receipt is
relabelled as TASK-005-02 proof.

Earlier failed attempts are preserved in private receipts: a mistaken transaction
method name, copied CLI condition syntax/type, missing Revisit command-flag registration,
an extra undefined native payload field affecting an existing exact test, split binding
of a test-only multi-statement migration constructor, finite JSON-number support in the
fixture validator, and use of the existing closed native decoder. Driver-only repairs
used valid synthetic credentials, case-insensitive HTTP header lookup, page-side UUID
creation and explicit CLI dialog handling. No failing assertion was weakened to make
the implementation pass.

Policy F changed-test meaning: old-schema fixture helpers now remove new dependent
tables and compare the original prior-table set; the prior Review declaration test uses
current SchemaRevision. This follows the reviewed append-only migration/declaration
requirements. The earlier annotation upload fixture now seeds the real note table,
preserving its valid/deleted-target assertions, as required by the real AnnotationTarget
handoff. The fixture validator accepts finite JSON numbers under the new context schema
because viewer context permits ordinary JSON. Existing Review browser history assertions
use the Korean labels required by COORD-2; raw wire enums remain unchanged. No new skip,
retry allowance or shortened timeout is introduced. Controlled peers are labelled fixtures.

## First result-review correction (C1)

RR1 identified three blocking gaps despite the initial passing Writer checks.
Only an intent tagged as the open draft's Save may clear that draft or advance
its base. Draft identity and edit revision survive journal/recovery; typing during
Save retains the newer text, while delete/reattach leave any open draft untouched.
A 412 server body_ref is read through its exact annotation/revision body stream
into separate comparison state. Neither reading nor retrying that comparison
replaces local text or the rejected pending intent; reload and merge remain explicit.

Receipt normalization now retains sealed body operation/target and referenced
context version/page/render/session binding as well as content/media identity.
Equivalent handles with the same binding replay; equal bytes under a different
binding conflict before saved success can replay. Inline body identity includes
its inferred operation/target. Inline context uses its actual stored binding, captured
as a receipt default; its saved context_ref normalizes identically (C2).
This repairs the reviewed identity contract without adding wire fields or migrations.

C1 adds frontend deferred-response draft tests, exact streamed large-body comparison,
persistence/public create-and-edit alternate-handle and changed-binding tests, and
extends the existing Chrome conflict flow to a large referenced server body. The
Chrome Save button is actually focused and activated with Enter. Initial receipts
remain evidence only for their original candidate.

## Second result-review correction (C2)

RR2-A-1 completes the F3 lineage: inline context and its stored reference share
bytes, media and version/page/render/session binding. Receipt defaults retain the
original implicit binding after reattachment, restart or another owner session.
A concurrent caller that prepared before the first receipt commit may recompare
that captured default through read-only Replay; it cannot apply a second mutation.
Different binding or bytes still conflict. The common receipt engine is unchanged.

RR2-B-1 adds explicit reconciliation after a concurrent reattach. Comparison shows
the prior and server locations, old selection and authorized prior saved context.
The owner can prepare a merge at the server anchor while excluding old context and
selection; local note text remains. Same-location merges retain their local context.
An edit cannot silently combine an old page/version draft with a new base validator;
previously rebased local drafts have an explicit align-and-drop action as well.
Reattachment itself remains a separate confirmed operation.

C2 adds frontend page/version merge and subsequent Save regressions, persistence
and public create/edit representation-switch tests, retained defaults after a move
and session/restart, changed intent rejection and a deterministic two-session race.
The bounded Chrome Notes flow also exercises a stale edit after another page
reattach, reading old context, explicit exclusion and successful merged Save.
Earlier candidates' receipts remain historical evidence.

## Third correction bundle (C3)

EE1-1 restores the existing personal-write lifecycle contract for Notes and Revisit.
New owner create/edit/reattach/delete and Revisit PUT requests succeed on active or
archived documents; known trash/purged targets return 409 INVALID_TRANSITION with
exact details.lifecycle. Unknown or unauthorized write targets remain nondisclosing
404. Inline preparation checks a new request's lifecycle before staging context;
rejected writes leave note/Revisit state, history, object/context records, pins,
references, events and receipts unchanged. GET and saved receipt lookup/replay keep
current visibility masking. Existing Review/reader write exceptions remain intact.

Persistence and public HTTP cases seed lifecycle states and a valid read-only
workflow credential, exercising the real authenticator and stores. These fixtures
do not implement lifecycle actions or claim TASK-005-03 acceptance.

COORD-T2-1 removes exactly 18 unreferenced EPIC-004 response-only definitions from
the embedded configuration schema. Its 12 TASK-005-02 definitions and query_document
selection remain; RF01 and the Go schema-closure test enforce subset equality with
the unchanged machine schema. The selected-contract manifest does not cover the
embedded file and remains byte-unchanged.

## Reproduction and limits

TASK-005-06 repairs the selected Wails client's unusable synchronous JavaScript
confirmation through the shared trusted-host HTML Sheet. All five consent branches
(including Edit/server reload) use an ephemeral captured intent and explicit named
action. The confirmation is portalled outside the Reader context dialog; Reader
and draft namespace remain mounted. Scope/authority invalidation, synchronous
admission, after-stream/inside-WebLock/pre-dispatch guards and storage-first local
replacement preserve current draft/pending/CAS semantics. Serialized journals,
transports, API, capability and schema contracts are unchanged. Current integrated
execution status belongs to the [Task6 record](owner-records.md); predecessor
passes above are not relabelled as native or Task6 proof.

The current Task6 context presentation uses complete literal inline spans
in a sized, wrapping scroll viewport with explicit Start/End/Select/Close.
Its read handle and captured revision/navigation scope are independent of Notes
mutations and consent. The three named terminal-workload rendered strings are
separate observations; associated Range text/global endpoints and decoded data
remain exact for all intervals. Finite browser cases exercise read cancellation,
supersession, scope retirement and unrelated typing/resize. The complete actual native owner campaign exercised the required paint/input
and saved-context cases within the [Task6 scope](owner-records.md). Historical
blocked attempts retain their outcomes; completed Task proof and the separately
reviewed assembled Epic gate have distinct candidate and execution bindings.

Use the prepared offline toolchain and authorized Gaori execution. Focused Ginkgo uses
race detection and the appropriate unit/integration/persistence label; new specs are named
Annotations or Revisit. The strict pytest selection is `tests/e2e/test_annotations.py`
and the complete `tests/contracts` fixture directory. Frontend test files are registered
in both package.json and check_build.py. Desktop request checks are `go -C desktop test
-race -count=1 -run 'TestAnnotation|TestReaderNativeOperationsParity' ./readerfacade`.

The existing Chrome driver adds `--notes-only`:
`python3 -B internal/adapters/http/auth/browser/check_reader.py --notes-only --browser-cli <installed-playwright-cli> --receipt <private-json> [--screenshot <private-png>]`.
It owns isolated profiles/ports, closes its browser/daemon, fingerprints production inputs,
and exercises keyboard Save, named trusted HTML confirmations and 390×844 emulated touch. The source
script and private receipt identify the exact selected actions. Frontend compiled browser,
renderer and native assets are regenerated with their inputs and verified for equality.

No native Wails UI, physical device, provider, private-ingress, cross-platform or release
proof is claimed. Lifecycle actions, purge/history retention policy, coherent backup/export
and protected restore retain their ordered TASK-005-03..06 owners. Their handoff must
include annotation_history object references/pins and revision-scoped context ownership.

## Review and acceptance

The Task Evaluator, the two non-authoring Executor reviewers and the Epic Evaluator
passed plan r2, after r1's missing saved-context read path was added. The first result
review returned rework for draft ownership, the 412 body_ref comparison and the receipt
binding identity; C1 corrected them and made the Chrome Save keyboard-activated. The
full-Task reassessment then found the inline/reference context equivalence and the
merge after a concurrent reattach, which C2 corrected. The Epic Evaluator's first Task
assessment found the lifecycle error contract for Note and Revisit writes, and the
Coordinator's final review found unrelated embedded schema definitions; C3 corrected
both, and the Task Evaluator, both reviewers and the Epic Evaluator passed the result.
The Coordinator completed the final project/product review and accepted the Task under
Master's delegated authority; overall EPIC-005 acceptance remains pending.

On the final candidate the whole completion suite passed: the canonical `make test`,
every stage through the no-fail-fast execution, frontend build equality and unit tests,
and the desktop race and vet run.
