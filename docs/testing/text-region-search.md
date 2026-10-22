# Static text and region search evidence

**Task:** [TASK-004-02](../tasks/EPIC-004/TASK-004-02.md) (EPIC-004) — status `done`
**Implementation status:** M1–M6 search surface and evidence delivered with approved A1 ordering amendment
**Evidence status:** pass for the executed final-candidate check set below; earlier failed attempts are recorded and were not relabelled; Task acceptance is separate
**Source revision:** HEAD de006df plus the TASK-004-02 change set; the final evidence ran on the frozen candidate whose code, tests, frontend, dist and contract fixtures equal the committed tree (later changes are documentation only); exact product/driver bindings stay in private receipts
**Environment/toolchain:** macOS arm64; Go 1.27.1, Node 26.7.0, Python 3.14.7; pinned prepared root/frontend/desktop modules
**Implementer / independent reviewer:** Executor Writer; Task Evaluator, two non-authoring Executor reviewers and the Epic Evaluator; final project/product review and acceptance by the Supervisor

## Actual outcome

The service provides authenticated GET/HEAD static text/region search, the
`haeryong search` CLI and the shared Library search UI through HTTP, native and
closed Go transports. `capabilities.features.search` is enabled. Search covers
all eligible imported pages and preserved attachment paths, with exact returned
document/version/page/optional-region identity. The Library supports query,
project/lifecycle/version/kind filters, Search All, history, clear, refresh,
pagination, incomplete coverage, safe reasons, errors and access loss. It renders
text nodes and opens matching pages without retargeting latest or promising
region focus. Manual rebuild belongs to TASK-004-04; general region navigation
belongs to its ordered owners.

The [personal-tool](../changes/2026-10-10-personal-tool-revision.md) and
[directory-result](../changes/2026-10-10-directory-results-revision.md) directions
apply: ordinary complete multi-page directories, normal JavaScript and all
selected file paths/bytes remain supported without compulsory manifests,
index.html, region metadata, extension allowlists or artificial content ceilings.
Static indexing does not execute authored JavaScript or claim runtime-only text.

M6 preserved all eight checked-in Mulgae/Gaori files / 107,448 bytes in two
projects with one version each and explicit public index completion. Its first
run stopped correctly at RC03 rank 6: equal-strength regions on another page
crowded M03 below the required top five. The Supervisor-approved A1 amendment
now places it fourth without changing any query/target, tier or weight. The
[fixed relevance worksheet](search-relevance-worksheet.md#m6-a1-repair-and-complete-rerun)
retains the original failure, before/after ranks and complete rerun. Every
required real-sample and additional check passes; owner usefulness is unmeasured.

## Case evidence

Proof keys S1–S11 are local record references, not runtime identities. Actual
receipts, full tuple identities and screenshots remain private. Prior gate results
are labelled separately from the new focused A1/M6 runs.

| Proof key | Fixture / client / command | Actual result and limit |
| --- | --- | --- |
| S1 Prior canonical gate | Synthetic root Go race layers and public production pytest; `make test` | Supplied accepted M5 baseline: Go unit 118, integration 33, persistence 398; pytest unit 10, integration 48, E2E 52, all passed. This is prior evidence; the Supervisor owns the new frozen candidate gate. |
| S2 Extraction and persistence | `internal/application/search/*_test.go`, `internal/adapters/sqlite/search_*_test.go`; focused `go tool ginkgo --race --label-filter=unit` / `--label-filter=persistence` with explicit package/focus | Passed within S1. Exact aliases, overlap, HTML boundaries, encoding, page-local anchors, migration rollback/retry, durable work, failure/cancellation, generation/visibility fences and restart. White-box seeded cases do not establish real lifecycle UI or credential issuance. |
| S3 Frozen ranked controls | `internal/adapters/sqlite/search_query_test.go`, fixed X09 and unchanged `tests/contracts/calibration-plan.json`; focused persistence command | A1 rerun: X09 18/18 (16 required and two declared unsupported negative controls); calibration40 40/40 on seeded 1,000 documents / 5,000 versions, every first relevant rank 1, zero returned topical false positives. No sibling repositories or public-import scale claim. |
| S4 Public search/jobs/large-body | `tests/e2e/test_search.py`, `test_search_jobs.py`, `test_f12.py`; strict prepared pytest with private basetemp/report | A1 rerun: search/search-jobs six cases passed in 439.32 s. Prior focused set including F12: eight passed in 1289.17 s. Separate SC16 HTML 44,040,248 bytes retained three boundary markers and the unique terminal marker with exact download bytes. Canonical F12 retains its own unchanged bytes and individual 600 s transfer budgets. |
| S5 Contract / docs | `python3 -B tests/contracts/run.py --report PATH`; `python3 -B docs/testing/validate_docs.py`; `python3 -B -m pytest docs/testing/test_validate_docs.py` | A1/M6 contract 42 and docs pytest six passed; documentation validation reports no issues. Fixture/schema checks do not prove ranking, durability or UI behavior. |
| S6 Frontend / facade | `python3 -B frontend/check_build.py --regenerate`, then validation; `cd frontend && npm run test`; `go -C desktop test -race -count=1 ./...`; desktop vet | M5 correction: 326 Node tests, four byte-matching committed assets. Prior desktop race/vet passed three packages; parity covers 70 public plus four internal wire operations. A1 changes server query ordering only; frontend/dist/facade remain unchanged. |
| S7 Chrome | `internal/adapters/http/auth/browser/check_search.py --browser-cli PATH --receipt PATH`; existing shell / Reader evidence drivers | Corrected search eight groups; shell 93 booleans and Reader evidence 58 cases previously passed. Keyboard, emulated 390x844 touch, filters/history, 55-row invalidation, three login returns and exact old Unicode page content. Fault/scoped presentation is labelled seeded, not real scoped issuance or physical-device qualification. |
| S8 Native | `python3 -B spikes/viewer/check_reader_native.py --stages app,search --receipt PATH`; `--stages app,evidence` | Corrected pinned Wails beta.28 actual App: search four groups / 21 rows; evidence three groups / 39 rows, normal closes. Selected stages retain full_qualification=false. Unique selected-page content digest and same-live-screen late HTTP200 rejection discriminate their guards; native scoped reasons are seeded presentation. |
| S9 Real-sample public corpus | `python3 -B tests/manual/check_search_corpus.py --receipt PATH`; checked-in `spikes/knowledge-value/samples/{mulgae,gaori}` | A1 rerun: all 14 required rows pass, worst rank 4 (RC03 6→4; RC13 stays 2; others 1). Cross-project 2/1/1; four exact pages, attachment and seeded logical source rank 1. Two projects / one version each, all eight paths/107,448 bytes round-tripped and both public derived jobs complete; frozen expectations and complete HTTP/CLI pages equal. |
| S10 Real-sample variants and UI | `internal/application/import/search_corpus_test.go`; Chrome `--corpus`; native `--stages app,corpus`; manual driver controls | Two lean race persistence specs retain deterministic one-of-two-page partial→complete after reopen plus actual-sample F10/trash, owner/scoped/denied, cursor/count/snippet/event checks. The explicit `manual_sc19` ordered pair retains all four indexed F10/trash canary copies and the unchanged 15-query matrix. Chrome and native each open both projects with exact actual-frame identities. Three temporary-copy controls fail as required and restore bytes. Cold-service and two warm worksheet repeats pass. |
| S11 Responsiveness and discrimination | Existing SC20 persistence/public F12 probes, temporary guard reversals and `make test-prepare` | Prior measured bounds and failures are retained below. A1 adds four unit and two persistence specs; M6 retains two lean real-sample persistence specs; its exhaustive ordered pair is manual-only. Query race 39 and SQLite race 91 pass; seven named A1 reversals fail and restore maps. Current independent checks are reported in the handoff; canonical gate remains Supervisor-owned. |

## Completion traceability

| Assertion / case | Proof keys and concrete covering checks | Actual result / limitation |
| --- | --- | --- |
| T-004-02-01; X09 | S2 aliases/chunks; S3 frozen X09/calibration; S8 transport; S9 RC01–14 | Synthetic controls and all fixed real-sample rows pass after A1; no broad recall guarantee. |
| T-004-02-02 | S2 `search_query_test.go` seeded Check/GET/HEAD/FTS/fallback/cursor exclusion and `auth/search_events_test.go`; S10 | Synthetic and seeded real-corpus denied/trash/count/snippet/cursor/event checks pass. Real trash producer remains TASK-005. |
| T-004-02-03 | S2 grammar/cursor/anchor/coverage; S4 public empty/long/malformed/HEAD/restart; S7/S8 | Bounded synthetic/public/client scope passes. M6 deterministic real-corpus partial→complete after store reopen passes. |
| SQLite FTS/search adapter | S2 schema8, derived worker, store/query visibility; S4/S11 | Implemented and exercised; A1 changes query tie order/cursor binding, with no storage/schema change. |
| Library UI; C03 and M15 contribution | S6–S8 labelled form, state/history, text rendering, matching-page action and transport parity | Implemented bounded desktop/phone-emulation/native scope; complete inherited cross-client/private-device acceptance remains separate. |
| Ranked query fixtures | S3 fixed controls and S9 canonical worksheet parsing | All fixed rows pass; real corpus and seeded controls remain bounded evidence. |
| SC01 | S2 `normalize_test.go`; S3 component_precedes_substring fixed z/a pair | Passed; direct alias component assertion is distinct from trigram membership. |
| SC02 | S2 chunks/normalized offset maps; S4 maximum 4,096-scalar expanded boundary phrases | Passed; C=32,768/O=4,095, original Unicode snippet retained. |
| SC03 | S3 18 X09 rows and calibration40; 35/40 negative admission control | Passed synthetic scale; fixed denominator and assertions unchanged. |
| SC04 | S2 query/auth GET/HEAD owner/scoped matrices, both exact anchors and version.read | Passed with seeded credentials; no new scope action/issuer. |
| SC05 | S2 raw-FTS-positive canaries, fallback, counts/snippets, explicit archive and live lifecycle | Passed seeded synthetic relation; S10 seeded real-corpus variants also pass. |
| SC06 | S2 `search_snapshot_test.go`, `search_visibility_test.go`, cursor binding | Hidden-only publication remains usable; visible state changes invalidate. |
| SC07 | S2 `auth/search_events_test.go` live/replay Check comparison | Passed; owner-only maintenance progress and version event masking remain distinct. |
| SC08 | S2 grammar/parameter validation; S4 HTTP/CLI/HEAD; S5 shapes | Passed; empty q allowed, malformed/duplicate/long inputs and unsupported CLI flags rejected. |
| SC09 | S2 extract/anchor specs; S4 exact Unicode paths/duplicate/missing anchors; S7/S8 | Passed synthetic/client scope; static anchors never imply runtime DOM or region-focus acceptance. |
| SC10 | S2 `search_migration_test.go`, populated schema7 reverse-construction recovery set / two projects / eight pages and ranked restart | Passed snapshot/object/commit faults, rollback/retry and FTS integrity; no claim of running an old binary. |
| SC11 | S2 `search_admission_test.go` closed typed admission / extensible stored enum checks | Passed; unsupported future rows fail safely when visible. |
| SC12 | S2 atomic publication/replay; S4 public import/job progress/file reads | Passed; exactly one durable derived job, ordinary import bytes preserved. |
| SC13 | S2 deterministic staged-body and metadata restart barriers; S4 public SIGKILL/restart | Passed; unfinished page reprocessed, completed pages/checkpoints retained, stale generation fenced. |
| SC14 | S2 ENOSPC/EDQUOT/read faults/revision selection and reasons; S4 labelled spool fault | Passed; failed bodies reported failed, original files readable, terminal failure not silently retried. |
| SC15 | S2 cancellation/lifecycle/cleanup with FTS integrity | Passed seeded scope; internal cleanup retains original files and historical jobs. |
| SC16 | S4 distinct 44,040,248-byte completeness fixture; S2 before-EOF failure | Passed markers/download identity; canonical repeated-a F12 is a separate responsiveness oracle. |
| SC17 | S6 state tests and S7 actual Chrome | Passed; safe reasons, cursor reset, access-loss clearing and every relevant invalidation after more. |
| SC18 | S6 three transport/facade parity and S7/S8 | Passed; exact page identity, required reason arrays and era/request guards. |
| SC19 | S9 canonical real-sample rows; S10 examples/variants/reversals | Original RC03 fail at 6 retained; approved A1 yields 4 and every required additional/cold/warm/UI/variant/control check passes. |
| SC20 | S11 bounded slices/transactions, unchanged F12 and measurements below | Prior corrected measurements/gates pass; initial stalls and the later completed-index two-character search deadline failure required repair. Not an arbitrary-load SLO. |
| A1 plan r3 amendment | S3 unchanged worksheets; S9 full corpus; S11 `query/search_order_test.go` and `sqlite/search_order_test.go`, race Ginkgo and seven named reversals | Alternation/traversal at sizes 1–5, old/missing/zero/negative cursor rejection, tier/score precedence/reset, exact-version groups and hidden-before-visible same-path invariance pass; each reversed guard fails. |

## Knowledge and user evidence

The [worksheet](search-relevance-worksheet.md) retains the frozen R/L policy,
all real-sample ordered aliases and the RC03 failure. **owner use unmeasured**:
no Master-authored queries were added. Agent-declared queries are not a human
usability study. Search success neither validates claims nor makes the
Gaori→Mulgae analogy a dependency. No provider, source verification, semantic
support, private ingress or physical-device result is inferred from retrieval.

A1 is the approved amendment to plan r3. It changes only equal-strength tie
ordering, adds ordinal cursor state and bumps the named ordering revision to 2.
X09 remains 18/18; calibration40 remains 40/40, with Q01–Q40 each 1→1 and zero
returned topical false positives. The component-over-substring pair and 35/40
negative admission control pass. No old cross-page-order assertion needed an
update. The full manual driver records all rows/misses instead of stopping at
the first relevance miss. Preparatory setup failures in source metadata inputs,
external-connection lifecycle writes and an overlong browser socket were fixed
in test setup through existing paths; no new product hook was added.

## Contract, state and recovery effects

A1 changes only server ordering/cursor compatibility; M6 adds evidence/records. Existing GET/HEAD search and
required SearchPage/index.reasons are defined in the
[HTTP contract](../contracts/http-api.md) and
[schema registry](../contracts/schema-registry.md); metadata/config remains 1.0.
Ranking compares five tiers, strongest first: (1) exact identity/title/path,
(2) literal title/region phrase, (3) literal path/topic/source phrase,
(4) all full components, then (5) substring. Within a tier, the descending
per-clause field score uses title=8, region=6, path=5, topic=4, source=4 and
body=1, counted once per clause/field. Repetition and hidden corpus frequency
add no score. For equal (tier, score), assign a one-based ordinal within each
exact (version_id, page_path) group in the existing tuple order, over eligible,
fully matched one-hit-per-unit results, before cursor and LIMIT. Counters reset
for every (tier, score) block. Compare ordinal ascending before the unchanged
project, document, sequence descending, kind, binary page_path, region_id and
source_id keys. This diversifies equal-strength evidence without collapsing
units or overriding strength. Cursor ordering revision 2 rejects pre-A1
bindings; strict decoding also rejects missing, zero or negative ordinals.

Append-only migration 8 owns derived work/index/unit/chunk, exact page anchors,
two external-content FTS tables, pinned shadows and maintenance triggers.
Previous migration strings and DB-plus-verified-object pre-DDL recovery remain.

Static extraction makes NFC/casefold copies and pre-fold identifier aliases,
streams bounded normalized/display windows and spooled anchor ranges, and never
changes authored bytes. Script/style/template are excluded; noscript and normal
HTML implied/body/html end-tag semantics are tested. Empty/ambiguous/static
anchors differ; supplied immutable region provenance wins exact tuple conflicts.
A single enormous HTML token or authored metadata label can still allocate in proportion to its size; no fixed content ceiling is imposed. Attachments retain paths with unsupported body status. Region/source metadata
uses normalized windows; configured host roots are not searchable identities.

Publication atomically enqueues work; one serial dispatcher processes bounded
inventory/extraction/commit turns. Read/verification/spooling occurs outside the
store writer. EOF activates page/regions in constant-time state transitions;
staged/ambiguous/incomplete bodies never become ready search data. Captured
version/project/permission/lifecycle and attempt/cancellation fences govern
commits. Narrowed eligibility pauses the same captured work; restored eligibility
or internal exact-target reconciliation resumes it. There is no public job-resume
operation. Definitive read/resource/storage failure or cancellation stays terminal;
new extractor revisions quarantine old data before bounded replacement.

Eligibility precedes candidate/count/snippet/coverage/cursor/event exposure and
matches Check with current version.read and both exact anchors. Actual latest is
selected before visibility with no fallback to older versions; archive is
explicit. Visible generations are Store-owned and selection-specific, survive
connection replacement and ignore touch-only reads, hidden same-document
publications, pending-page metadata and quarantined cleanup. Real authority,
lifecycle, EOF activation and selected index changes still invalidate. Slice
reads recheck authority and final consistency; one visible-change restart is
allowed before retryable 503 query_timeout. Authentication/permission outcomes
retain 401/403/404 rather than becoming storage 503. The request deadline starts
at authentication, includes cancellable lock waits and checks post-processing.

Clients retain request/era/dispose fences and required safe reasons. Content-free
event hints coalesce authenticated GET revalidation even after multiple pages;
starting it clears old protected rows/cursor. Login-return q preserves only
encoded TAB/LF/CR beyond the ordinary control policy. No mutation or maintenance
button is introduced. Native credentials remain in Go and imported frames remain
separate from host DOM/tokens/privileged bindings.

### SC19 persistence budget and explicit manual matrix

P1 corrected the added SC19 workload after a canonical persistence run exhausted
its unchanged global 1,000 s Ginkgo budget inside the full variant matrix.
The immediately preceding focused race measurement took 246.990 s for the two
specs (30.413 s partial and 216.549 s variants); one-minute host load rose from
17.95 to 28.95. The earlier M6 focused pair had taken about 190 s. Host load is
an observation, not a reason to change any budget or calibration workload.

Each ordered container now owns one `BeforeAll` fixture, owner session and two
workflow credentials. Partial interruption/reopen and variant checks share the
original imports. Explicit four-file lists replace Git discovery. The canonical
`persistence` pair imports each of the two complete sample trees exactly once,
uses the actual Gaori-only literal as a positive control, then checks F10
project denial without a masking project filter and default-active exclusion
after moving that same sample to trash. Owner/scoped baseline items, coverage,
counts and non-null cursors remain identical; decoded cursors and event bodies
contain no protected version. The denied principal still fails. Partial and
completed no-match states remain separately asserted for both trees.

The canonical pair measured 29.905 s under race (one-minute load 16.15→15.76),
below its 45 s target. The full four-copy, fifteen-query owner/scoped/denied
matrix retains every original assertion in the ordered `manual_sc19` pair,
excluded from all canonical layer filters. There each original tree and each
canary is imported/indexed once. `tests/manual/check_search_corpus.py` explicitly
selects that label, records its distinct scope, and fails if it fails. Its three
temporary-copy SC19 reversals remain unchanged. This split removes redundant
imports and repeated matrix scans from the common gate; it adds no product hook
and changes no layer, test budget or calibration workload.

The single requested `make test-persistence` attempt took 1,002.303 s wall time
and exhausted the unchanged global budget in the existing source observation
spec that attributes per-source reasons on a partial run. Fifteen packages
passed; the source suite timed out and the foundation process suite did not
start. The canonical SC19 pair passed in 24.984 s within that run. One-minute
host load was 17.17 at start and 17.48 at end. This is a failed persistence gate,
not a passed skip; no other suite, deadline or workload was changed.

### SC20 measurements and binding disposition

Initial measurements failed: the first whole-SQLite run passed 63/64, with the
raw transaction-time guard failing. A 40 MiB prepared worker recorded 2,973 writes:
non-race p50/p95/max 1.337/13.122/490.464 ms, race
38.616/208.804/3965.381 ms; index time 12.120/252.841 s. The initial
public two-character fallback held the shared connection for 6,934.654 ms;
original HTML reads reached 4,839.869 ms. An earlier observation placed before
the original final transfer also exceeded its unchanged transfer budget; that
experiment did not pass.

The Supervisor rejected these as a disclosed pure performance limitation and
required bounded lock holds/writes, deadlines from first authentication,
cancellable lock waits and deterministic race budgets with separate non-race
host measurements. Later intermediate failures included a 1,000 s persistence
suite timeout while calibration40 was still running, touch/hidden-write false
restarts (87 retries / 110 race samples), and a deadline failure in the subsequent
completed-index two-character search under race. The
`SC20_prepared_transactions` report entry was written after indexing completed,
about 30 s before the failure, identifying that subsequent search as the failing
call. K2 removed repeated body-classification work to improve the completed-index
search; the in-flight assertion is unchanged. None was relabelled pass or fixed
by weakening the query/fixture,
30 s request deadline or original 600 s F12 transfer budget.

The correction uses 64-row / 8 ms yielding, bounded snapshot work, light per-slice
authority checks, one posting / 4 MiB per transaction, bounded separate merge
work and O(1) EOF activation. A later native prepared probe recorded 8,839 writes:
p50/p95/max 0.166/1.959/73.238 ms, indexing 8.871 s; the non-race maximum is
below the 300 ms engineering guard. Its two-character query took 907.210 ms in
49 slices, maximum 64 examined rows. Race durations are observations; physical
slice/posting/merge bounds are the gate assertions.

| Repeated SQLite race run | Specs passed | Suite seconds | SC20 seconds | Completed two-character query, ms |
| --- | --- | --- | --- | --- |
| 1 | 88/88 | 535.714 | 123.580 | 10521.744 |
| 2 | 88/88 | 507.431 | 138.114 | 13066.593 |
| 3 | 88/88 | 507.862 | 113.889 | 10593.421 |

| Canonical F12 public observation | Non-race initial indexing | Race after transfer restart |
| --- | --- | --- |
| Original-read p50/p95/max, ms | 3.630 / 8.033 / 8.033 | 106.288 / 252.527 / 416.199 |
| Metadata-read p50/p95/max, ms | 1.877 / 4.707 / 4.707 | 41.368 / 92.873 / 114.775 |
| Search p50/p95/max, ms | 43.135 / 55.891 / 55.891 | 450.227 / 1557.010 / 1736.030 |
| Retryable searches / samples | 1 / 6 | 0 / 109 |
| Two-character query, ms | 700.114 | 14052.466 |
| Index observation, seconds | 10.420 | 314.295 |
| Maximum sampled RSS, bytes | 349,437,952 | 1,291,124,736 |
| DB / WAL bytes | 99,323,904 / 4,194,192 | 98,992,128 / 4,206,552 |

The later M5 race observation also recorded zero retries in 109 samples,
a 10,763.337 ms two-character query and 305.934 s resumed indexing. The unchanged
F12 archive was 12,768,177 bytes, 55,050,821 expanded bytes, 1,027 files.
Search percentiles include retryable responses; index observation has its
separate workload budget. Prior race persistence/E2E measurements overlapped on
this host, so these are bounded host observations, not isolation benchmarks.

### Review corrections retained in the delivered boundary

| Found issue | Fixed behavior and discriminating evidence |
| --- | --- |
| E1–E5 extraction/migration fixture | Nested template suppression, implied end tags, noscript fallback, two-project populated recovery; corrected body/html TAIL semantics. Immediate-close evidence was superseded; corrected reversals fail exact fragments/digests. |
| G1–G6 durable work | Exact-target revalidation/pause, metadata-free regions, UTF-8 authority, wrapped EDQUOT, honest terminal failure, cancellation during giant tokens and responsive service stop; public search-jobs/F12 and deterministic barriers. |
| H1–H5 lock/deadline/normalization | Sliced search and posting/merge bounds, cancellable lock waits, O(1) activation, failed-page counters, normalized NFC/NFD metadata windows; named reversals and labelled non-race probes. |
| I1–I5 snapshots/restart/authority | Bounded snapshot work, replay every derived region-label window, preserve 401/403/404, post-ranking cancellation, cheap fenced slices without weakening calibration40. |
| J1/J2 then K1/K2 | Search-specific Store generations across connection replacement; independent latest/all-version and pending-page visibility. K2 removes repeated body-classification work to improve completed-index search; the in-flight assertion is unchanged. Real touches/hidden writes preserve results; actual changes/revocation invalidate. |
| L1 quarantine | Invisible anchor/unit/chunk cleanup does not restart selected snapshots; quarantine exit does. Three named reversals fail and restore identical maps. |
| N1/N2 UI authority/login return | 55-row SSE invalidation clears protected state; q-only TAB/LF/CR returns preserve the route. N1-accumulated-invalidation and N2-login-return-whitespace reversals fail. |
| N3 native oracles | Unique actual old-page content digest; same mounted screen rejects real delayed HTTP200 with StaleResponseError. Dropped page_path keeps parent URL passing but fails content; removing search era guard yields resolved on the live screen and fails. |
| A1 required RC03 miss | Approved per-page ordinal after (tier, score), before unchanged tuple ties; named ordering revision 2 and strict positive ordinal cursor. Four unit/two persistence specs, seven named reversals, unchanged frozen X09/calibration/component controls and full SC19 rerun. |
| P1 SC19 persistence budget | Shared ordered setup and explicit paths; lean actual-sample persistence pair under 45 s; complete unchanged matrix explicitly selected as `manual_sc19` by the corpus driver. All partial/leak/cursor/event assertions and SC19 reversals remain covered. |

Critical reversals restore identical tracked/nonignored path and byte maps.
The N3 negative runs isolate the unchanged search assertions in a private driver
copy allowing only that stage; positive runs retain app/search and app/evidence.
Prior logout-only row absence was nondiscriminating and is superseded by the
live-screen oracle. Fixtures do not add native scoped issuance or product hooks.

## Review and handoff

The Task Evaluator and the two non-authoring Executor reviewers reviewed every
milestone candidate and the final result, and the Epic Evaluator reviewed the
plan and the final Task result. Their findings were resolved before acceptance:
- the review-corrections table above;
- the approved A1 ordering amendment;
- the record correction O1;
- the SC19 persistence budget fit P1.

The Supervisor completed the final project/product review and accepted the Task
under Master's delegated authority; overall EPIC-004 acceptance remains pending.

On the final candidate:
- The canonical `make test` passed: Go unit 122, integration 33 and persistence
  402 specs; pytest unit 10, integration 48 and E2E 52.
- Documentation integrity and the contract runner (42) passed.
- The Supervisor's final evidence set passed:
  - frontend build equality and unit tests;
  - desktop race and vet;
  - the Chrome shell, Reader-evidence, search and corpus drivers;
  - the native `app,evidence`, `app,search` and `app,corpus` stages;
  - the real-sample corpus driver, including the `manual_sc19` matrix.

Earlier canonical attempts failed on the SC19 persistence budget (P1), on
host-load timeouts and on a stray ignored local file in a sample tree. Each
failure was recorded and corrected or removed; none was relabelled, and no
budget or workload changed.

The existing import metadata parser rejects whitespace after the
`schema_version` colon (PACKAGE_INVALID); the seeded source row uses compact
JSON. That import defect predates this Task and lies outside it; it is held for
the owner's disposition. No source/provider verification, physical-device, broad
recall or owner-use qualification is claimed. Next: TASK-004-03.
