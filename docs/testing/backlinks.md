# Backlinks and deterministic index recovery

**Task:** [TASK-004-04](../tasks/EPIC-004/TASK-004-04.md) (EPIC-004) — status `done`
**Implementation status:** delivered: scoped backlinks and live counts, owner index validation/rebuild, Reader Links tab
**Evidence status:** pass for the executed final-candidate check set below; earlier failed attempts stay recorded as failures; Task acceptance is separate
**Source revision:** HEAD `56299c9` plus the TASK-004-04 change set; the completion suite ran on the frozen final candidate equal to the committed tree; exact run bindings stay in private receipts
**Environment/toolchain:** macOS arm64, prepared Go 1.27.1/Python 3.14.7/Node 26.7.0, Chrome with installed Playwright CLI; no installation/network/provider call
**Implementer / independent reviewer:** Executor Writer; Task Evaluator, two non-authoring Executor reviewers and the Epic Evaluator; final project/product review and acceptance by the Coordinator

## Actual outcome

The exact-version Reader now separates Evidence (default) and Links. Links has
Authored references, scoped Backlinks and an honest empty Suggestions section.
Notes/Ask and semantic suggestions retain their later owners. The named public
operations versionBacklinks, validateVersionIndexes and rebuildVersionIndexes have
HTTP/native transport and desktop facade parity; capabilities.features.backlinks
is true. `/related` remains FEATURE_NOT_READY.

Incoming edges and outgoing live counts reuse schema 9, exact canonical anchors,
source.Check on both endpoints and current lifecycle/scope/permissions checks.
Edge counts differ from distinct referring/target document counts across versions.
Filtering precedes labels/counts/cursors; unresolved declarations are not backlinks.
References rejoin projected rows and source binding after counting, restarting a
changed attempt within the read deadline while retaining source-only cursors.
D4 historical targets retain identifiers only, with no title/body/route or latest
replacement. Backlink cursors bind eligible state; references retain source-only
declaration cursors with separately live outgoing counts.

The existing reverse index supplies NULL-first physical ordering. Each ordinary
store turn consumes at most 64 raw edge rows before projection, including misses;
document ID sets contain admitted rows in the selected incoming/outgoing scope.
No library-wide traversal is added to Reader link navigation. Owner reverse-index
validation can traverse independent raw rowid slices before filtering. No migration/new
persistent index, content quota, AI or personal-state mutation is introduced.

## Case evidence

| Case | Fixture / expected observable | Actual focused check | Status |
| --- | --- | --- | --- |
| T-004-04-01 | Seeded canonical graph: outgoing/incoming IDs and anchors reconcile; unresolved rows add no edge; reads have no derived/outbox side effects | sqlite backlinks_test.go BL projections; pytest test_backlinks.py public HTTP/CLI | pass on executed candidates |
| T-004-04-02 | Multiple edges and referring versions deduplicate one document; exact page/region scopes and NULL-key pagination retain every edge | BL projections (including a second document and denied-only cursor stability) and physical-slice specs; public one-item cursor E2E; frontend payload/scope tests | pass on executed candidates |
| T-004-04-03 / F10 | Seeded current grants/trash/purge: hidden source contributes no rows/counts; historical D4 remains minimal after source rebuild | BL projection/recovery specs and existing RF03 reference regressions | pass on executed candidates; real lifecycle mutations remain TASK-005 |
| C06 contribution | Real SQL-only reverse-key divergence under exact restored DDL; normal startup succeeds, validate detects it and public owner rebuild repairs it | BL C06 spec: canonical versus covering-index result; receipt-backed replacement; shutdown-before-commit rollback and checkpoint restart; RR1 F4 adds same-source covering-key collision detection by canonical rowid | pass on executed candidates; whole two-project case remains TASK-004-06 |
| Recovery | Reject nonterminal re-entry; keep old terminal jobs/pins/canonical edges; honor queued/between-step cancel and restart stale attempts | BL owner recovery and C06 specs; HTTP/CLI rebuild/all E2E | pass on executed candidates |
| Changed UI | Keyboard tabs, actual source/target navigation and return, real empty state, narrow controls, Escape/focus return | check_links.py/JS Chrome driver, desktop 1440×1000 and narrow 390×844; six cases, including deferred personal-position responses after both click kinds | pass; error/tombstone screen projections are explicitly test-owned HTTP fixtures |
| Transport | Closed named calls, once-encoded fields, stale denial, 202 job creation/reconciliation and error parity | frontend backlinks.test.ts, desktop readerfacade tests/parity, exit-preserving desktop race/vet | pass on executed candidates; no new actual native-navigation claim |

Focused checks map each case; the final completion suite is recorded under
[Review and acceptance](#review-and-acceptance).

## Contract, state and recovery effects

The provisional additive [HTTP](../contracts/http-api.md#backlinks-and-deterministic-index-recovery-task-004-04),
[registry](../contracts/schema-registry.md#backlinks-and-index-recovery-wire-additions-task-004-04),
[CLI](../contracts/cli.md#backlinks-and-owner-index-recovery-task-004-04),
[job/event](../contracts/jobs-events.md#explicit-derived-index-recovery-task-004-04)
and [data-model](../data-model.md#backlink-projection-and-non-destructive-rebuild-task-004-04)
sections own exact shapes, counts, error/HEAD semantics and recovery scope.
Every completed validation HTTP 200 exits CLI 0, including inconsistent/incomplete.
Only registry errors produce nonzero exit classes. Same-key receipt replay returns
the original created jobs. New jobs re-own terminal current-revision slots without
re-entering terminal jobs; older revision slots/history, pins and D4 edges remain.
Search/all queues search before reference replay; reference rebuild never invokes
permanent-source deletion cleanup. No target-derived reference event is added.

The repair SQL/checkpoint is one atomic store-gate step. Owner cancel applies at
its boundaries; an in-flight cancel waits for the gate. Daemon-context shutdown
rolls back the step, then restart fences/resumes the last committed checkpoint.
Normal index construction does not run this manual repair phase.

SQL-only fixtures build one wrong key or a same-source reference-key collision at
unchanged cardinality, then restore exact original DDL before normal startup.
Public owner validation/rebuild detects and repairs real index/base divergence;
canonical rows/hashes are preserved and production has no writable_schema surface.

Missing/changed DDL is deliberately outside online recovery and remains strictly
startup/refused and rebuild/refused. No offline repair surface ships; preserve the
library pending EPIC-005's verified restore. Structural search validation compares
immutable inventory, deterministic required
metadata/body ordinals and static anchors with existing selected-version units,
chunks and posting records. All search traversal uses existing version/unit
prefixes; unrelated versions do not enlarge it. It does not certify every possible
FTS byte corruption, ranking or semantic truth.
The existing backlink_index_repair policy/finding owners remain EPIC-008/009.

## Review and handoff limits

M02 contributes honest structural completeness, not a full semantic scan. M04
contributes preservation/no lifecycle revival; full decision/rejection/rollback
behavior remains its owning Epic. Seeded purge/grants are not production lifecycle
APIs. Chrome's expected test-owned 503 messages and existing isolated-frame sandbox
warnings are recorded separately from uncaught application errors (none observed).
Agent-operated narrow pointer checks are not physical-device usability evidence.
No provider, private-ingress, native navigation, every-platform or release claim is
added. Actual native navigation belongs to TASK-004-05/06; copied links/login return
to TASK-004-05; the real two-project C06 gate reuses these exact interfaces in
TASK-004-06. Only root AGENTS.md is adopted here; no directory-guide coverage policy
was found and unrelated guide backfill was not undertaken.

## Review and acceptance

Result-review findings and their deterministic regressions:

| Finding | Correction / deterministic regression |
| --- | --- |
| F1 | Compare every returned view with a fresh projection after both count passes, including unavailable rows; the same store-turn barrier covers active → trash and trash → active, matching rows/counts while retaining the source-only declaration cursor |
| F2 / COORD-1/2 | Compare immutable expected inventory and extraction ordinals/anchors with derived rows; delete required units/body chunks/anchors under complete markers, detect inconsistency and rebuild; preserve empty bodies; propagate typed-read errors and record unchanged selected-prefix traversal after unrelated graph growth |
| F3 | Admitted anchor_pending becomes incomplete with safe anchor_prerequisite_pending; stop target rebuild at actual extraction-pending turns, then complete it; missing/ambiguous anchors remain legitimate completed observations |
| F4 | Compare covering keys with a NOT INDEXED canonical rowid point read after endpoint filtering; real SQL-only same-source key collision cannot return duplicate edge IDs and is reported mismatched before owner repair |
| F5 | Both click kinds carry activation and exact route/version/page/region/tab generation through the final personal read; deferred frontend promises and actual Chrome response barriers prove no stale navigation/history/return position |

The Task Evaluator and the two non-authoring Executor reviewers reviewed the
plan, the first result and both correction rounds; F1 needed a second round for
the unavailable → resolved direction. The Epic Evaluator reviewed the plan and
the final result. The Coordinator completed the final project/product review and
accepted the Task under Master's delegated authority; overall EPIC-004
acceptance remains pending.

On the final candidate:
- The canonical `make test`, frontend build equality and unit tests, and the
  desktop race and vet run passed.
- The focused race-enabled BL/RF persistence specs, RF unit specs and the
  references/backlinks HTTP/CLI E2E tests passed on the final source.
- Chrome's six Links scenarios passed on the final candidate.

Earlier failed attempts (fixture construction, an unused import, a contract route
pattern, the parser byte pin, Chrome readiness and an F3 turn boundary) remain
recorded privately as failures. Their repairs kept strict schema, startup and
parser pins and added no skip, timeout increase or workaround sleep.
