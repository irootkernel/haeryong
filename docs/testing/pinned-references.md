# Pinned reference implementation evidence

**Task:** [TASK-004-03](../tasks/EPIC-004/TASK-004-03.md) (EPIC-004) — status `done`
**Implementation status:** M1–M5 delivered: metadata, migration and repositories; publication and derived work; API, CLI and authority; shared clients and Reader; real-sample and final evidence
**Evidence status:** pass for the executed final-candidate check set below; earlier failed attempts are recorded and were not relabelled; Task acceptance is separate
**Source revision:** HEAD 5f94cd1 plus the TASK-004-03 change set; the final evidence ran on the frozen candidate whose code, tests, frontend, dist and contract fixtures equal the committed tree (later changes are documentation only); exact product/driver bindings stay in private receipts
**Environment/toolchain:** macOS arm64; Go 1.27.1, Node 26.7.0, Python 3.14.7; pinned prepared root/frontend/desktop modules; the installed Playwright CLI with Chrome; the reused pinned Wails beta28 ReaderMode harness
**Implementer / independent reviewer:** Executor Writer; Task Evaluator, two non-authoring Executor reviewers and the Epic Evaluator; final project/product review and acceptance by the Supervisor

## M1 implemented boundary

Optional from/relation/navigation metadata is additive in the embedded and
machine schemas. `DecodeReference` uses the auth-free shared `internal/application/referencecodec` validator; it retains
supplied JSON/identities and uses no target authority or DOM lookup. Existing
metadata fields and raw-directory imports retain their structural contract.

Migration 9 creates source-FK materializations, immutable authored declarations,
structural edge records and both target indexes. Go write/read typed-column admission rejects
unknown nonempty states/work kinds even though the SQL enums are extensible.
UUIDv5 edge identity is separate from the local reference ID. Typed repository
puts preserve immutable tuples/hashes and bound outgoing/incoming enumeration.
`storage.PrepareAuthoredReference` validates JSON and raw/typed equality before
`Store.Write`, retaining immutable text and detached optional fields. The typed
put accepts only this prepared input. Locked reads scan/validate typed columns
without re-decoding raw JSON; raw integrity is checked during preparation from
the immutable manifest, not by repeatedly parsing stored declarations.
An authentic schema-8 fixture applies only the original eight migrations and
holds a reference-bearing immutable manifest; the upgrade checks compare every
prior authoritative table's rows (FTS engine merge cookies are not authored data) and independently verified recovery DB/object bytes.

No reference route, job enqueue/worker, client operation, Reader behavior, event,
backlink panel or capability enablement is delivered by M1. T-004-03-01–03,
C05/C06 and X19 remain unpassed at this milestone. Lifecycle scenarios here are
seeded persistence facts, not production trash/purge operations.

## Verification ownership

Feature specs are `internal/application/import/references_test.go` (RF01 unit),
`internal/adapters/sqlite/references_test.go` and
`internal/adapters/sqlite/reference_migration_test.go` (RF06 persistence), plus
`tests/contracts/test_reference_contracts.py` (integration). Native Ginkgo runs
retain race detection and explicit layer filters; selected cases in other layers
are filtered rather than counted as required skips. Synthetic fixtures use one
small shared setup per scenario, not repeated public imports.

Named reversals cover optional/closed metadata fields and embedded-schema parity,
UUID namespace/length/version encoding, source FK, extensible SQL enums, Go
write/read admission, target index/declaration inventory and recovery integrity.
The Writer restores each reversal byte-identically before final checks. Private
native reports retain exact commands, durations, counts and failures; no runtime
path, run identity, credential or candidate digest is promoted into this record.

M1 focused execution and preparation results are appended only after completion.
The Supervisor's aggregate gate and independent review remain separate.

## M1 focused results

| Check | Observed result and scope |
| --- | --- |
| RF01 decoder/UUID identity, race Ginkgo `--label-filter=unit --focus=RF01` | 3 passed; legacy/enriched metadata, closed/null/malformed fields, raw-byte preservation and stable UUIDv5 golden value |
| RF06 plus existing schema/migration regressions, race Ginkgo `--label-filter=persistence --focus='RF06\|SC10\|Migration\|schema7 source'` | 16 passed, including 4 new RF06 specs; internal persistence only |
| Existing metadata/whitespace regressions, race Ginkgo `--label-filter=persistence --focus='Metadata\|metadata\|Whitespace\|whitespace'` | 15 passed; includes the FIX1 whitespace baseline and existing large supplied metadata behavior |
| Prepared Python selected-contract handler `tests/contracts/run.py --report <new private path>` | 45 passed, including metadata parity and provisional response-state fixtures; no implemented HTTP claim |

The four new persistence specs took 2.312 seconds in the combined focused run
(sum of native spec runtimes; compilation and unrelated specs excluded). This
measures the added canonical spec workload, not a full before/after aggregate
measurement. The Supervisor's frozen aggregate run remains necessary to establish
the complete persistence budget. The global timeout stays unchanged.

Earlier attempts failed in the authentic revision-8 fixture (an implicit project
column list, then comparison of mutable FTS engine cookies), and in the provisional
Reader-URL fixture's full-match pattern. Those fixtures were corrected and rerun;
no product assertion or gate was waived. An initial system-Python contract command
lacked pytest; the successful command used the existing prepared `.venv` interpreter
without installing anything. Full failed-attempt logs stay private.

All 13 named reversal executions discriminated through actual assertion failures:
RV-FIELDS, RV-CLOSED, RV-SCHEMA-PARITY, RV-UUID, RV-FK, RV-ENUM,
RV-ADMISSION-READ/WRITE/WORK, RV-TARGETINDEX, RV-DECLARATIONS,
RV-TABLEINVENTORY and RV-RECOVERY. Every affected file was restored byte-identically.
The first closed-object mutation changed an unused schema switch and did not
change parser admission; the final mutation actually admitted an unknown field.
The initial parity run selected too few matrix items for the contract collection
guard; the final run used the complete selected-contract handler and failed the
parity assertion itself. These unsuccessful reversal attempts remain in private
logs and are not counted as discriminating executions.

An initial full vet/prepare attempt detected an auth-test dependency cycle when
SQLite imported the complete importer package for decoding. The codec now lives
in the small auth-free `internal/application/referencecodec` package; the import
facade maps field diagnostics, and SQLite uses the same decoder directly. This
changes no metadata contract or storage model. Full vet/preparation and the
focused/reversal checks are rerun after this dependency correction.


## M1 round-one correction boundary

The first canonical unit run found an omitted configuration-schema copy of the
reference definition (G1). All repository JSON copies were enumerated: machine,
import metadata and configuration definitions now agree. The original full
configuration closure assertion is unchanged; the Python parity fixture also
checks both embedded schemas.

A2 moves declaration JSON validation and typed-value equality out of the store
mutex into an immutable prepared input. RV-PREPARE-LOCK checks concurrent small
read admission during actual large-declaration preparation, preparation rejection
of typed/raw divergence, and typed reads which do not invoke the decoder. Its
reversal restores JSON validation in the locked scanner and must fail.

B2 attaches zero-based array positions to decoder diagnostics and duplicate-ID
errors. RV-REFERENCE-POINTER exercises full Validate and Import paths with three
items: invalid source region at index 1 and duplicate ID at index 2. The decoder
itself retains its item-relative pointer for standalone use. RV-SCHEMA-CLOSURE
removes a configuration-only optional field and must fail the unchanged closure
assertion. Reversal files are restored byte-identically before final verification.


### M1 correction checks

| Check | Observed result |
| --- | --- |
| Complete `make test-unit` | 125 race Ginkgo specs and 10 pytest tests passed |
| Complete `make test-int` | 33 race Ginkgo specs and 51 pytest tests passed |
| Focused race persistence, `RF06\|SC10\|Migration\|schema7 source\|RF01 full` | 18 passed: 17 SQLite reference/migration specs and 1 full metadata Validate/Import diagnostic spec |
| Prepared-interpreter `python3 -B tests/contracts/run.py --report <new private path>` | 45 passed |

RV-SCHEMA-CLOSURE, RV-PREPARE-LOCK, RV-REFERENCE-POINTER-FIELD and
RV-REFERENCE-POINTER-DUPLICATE each produced the expected native assertion
failure and were restored byte-identically. B2's field and duplicate-ID reversals
run separately so the earlier field failure cannot hide a broken duplicate path.
The new A2 concurrency/preparation spec took 2.656 seconds and the B2 full-path
spec 0.904 seconds in the focused run. These are individual spec durations, not
a full aggregate budget comparison. Neither parser admission nor migration DDL
changed; the prepared-input boundary implements r3's existing lock-scope rule.
The M1 product boundary and outstanding whole-candidate gate remain unchanged.

## M2 publication and work boundary

`internal/adapters/sqlite/import.go` adds the references enqueue to the existing
publication transaction. `internal/application/work/maintenance.go` routes both
search/references kinds in both entrypoints and sweeps all published versions
search-first. Selection gates references before ranking/LIMIT and the processor
checks again before claim. Cancellation can finish while search is nonterminal.
There is one work key per version/reference revision; terminal keys never reopen.

`internal/application/references` reads the verified immutable manifest, prepares
shared-decoder declarations outside Write, commits each declaration/checkpoint,
attempts outgoing exact pins and scans indexed incoming references. The row bound
is 64 and the iterator yields after 8 ms, including skipped/already-resolved rows.
Page-only endpoints use the resolved regular HTML entry under the renderer
classification and its immutable inventory hash, independently of search extraction. Only region
endpoints require current page EOF, extractor revision and matching page/anchor
hashes. Version/document mismatch, missing/ambiguous/pending anchors and
purged targets create no edge. Conflicting recorded hashes preserve the old edge;
there is no silent re-pin. Incoming changes update no referring source state/event.
The typed source cleanup is bounded and preserves other sources' incoming edges.

The reference fence uses captured system identity, immutable version/content-set/
manifest inputs, current source restrictions and attempt generations. It does
not reconstruct publishing credentials or invoke a search fence. Cancellation,
source suspension, lost acknowledgment and restart retain honest checkpoints and
progress. Failed/cancelled manifest reads keep previously committed source-only
counters. Revision coupling pins the extractor and anchor/parser source bytes in
`internal/application/references/revision_test.go`.

The eight ordered reference-work specs share one small profile. They cover
RF02-AUTHFREE atomic rollback/publication without staged references, RF04 explicit
source context, RF06 cancellation/fault recovery, RF07-O1/O2/R2/TERMINAL/FAIR/REV,
stable identity and drift. The pre-attempt fixture barrier independently reads
the exact durable declaration in a fresh transaction. O1 also releases target
extraction/incoming after the source outgoing checkpoint and before source work
continues. The incoming key case resumes after restart behind a target's saved
cursor. The fairness case uses 65 lightweight blocked rows, not 65 public imports.
The existing source-event fixture checks reference live/replay eligibility in its
same profile. Paired target-event/cursor projection and Reader click assertions
remain in M3/M4 with their implementing routes and UI.

Public E2E imports 70 declarations through the actual CLI/daemon, kills/restarts
work and verifies stable job identity/completion and original file bytes. A
separate executed public upgrade check built the unchanged schema-8 production
baseline, imported two versions with 70 legacy declarations, then opened that
profile with the current binary. Startup backfilled both reference jobs through
the same processor and retained their identities across another restart. No
SQLite assertion or alternate daemon is used for these public checks.

The canonical F12 bytes and its 600-second transfer deadline are unchanged. Its
existing public search observer is followed by a reference-work timing observer;
that observation has its own workload bound and does not extend transfer time.
M2 enables no reference route, CLI, Reader, backlinks or capability. RF02 read-only
resolver/410, RF03 paired cursor projection and the Reader part of RF07-R2 remain
with M3/M4. Existing M1 migration/repository qualifications remain in force.


### M2 executed checks

| Check | Observed result and scope |
| --- | --- |
| Restored-code complete `make test-unit` | 128 race Ginkgo specs and 10 pytest tests passed |
| Restored-code complete `make test-int` | 33 race Ginkgo specs and 51 pytest tests passed |
| Focused references/migrations/eligibility | 26 race persistence specs passed |
| Focused publication/work/recovery | 20 race persistence specs passed across SQLite, import and auth |
| Selected-contract handler | 45 pytest tests passed |
| Public import/restart | 1 E2E passed; 70 declarations, same job across force-stop/start and another startup, original bytes unchanged |
| Public schema-8 upgrade/backfill | 2 published versions and 70 declarations; both reference jobs completed in 6.789 seconds including current startup; another restart reused their identities |
| Canonical F12 plus reference completion observation | 1 E2E passed in 752.06 seconds including the separate search observer; original transfer assertions kept their 600-second limit |

F12's sampled small-file reads had p95 297 ms (max 385 ms), metadata reads p95
112 ms (max 200 ms), and search p95 1,448 ms. The reference completion readback
after the search observer took 28.524 ms for its 551-byte manifest. This is a
completion-readback duration, not a measurement of the reference job's runtime.
These are public request observations, not a new search-ranking or UI claim.

M2 reversals cover publication/staging and publisher authority, incoming,
commit-first and outgoing order, both own-search gates, terminal states,
pre-LIMIT admission, terminal-key reuse, revision coupling and historical sweep,
attempt fencing/cancellation, target-derived source events/revisions, drift,
terminal progress preservation, source replay filtering and cleanup ownership.
The commit-first reversal failed the direct fresh-transaction declaration
assertion, rather than relying only on a delayed-target final edge assertion.
The coupling reversal was repeated after adding the generated-manifest scanner
to its pinned parser mapping. The nine RF06 DDL/admission/index/recovery reversals
were repeated as well. Every mutation was restored byte-identically.

Early fixture attempts exposed an invalid null references array, a mistaken
session table/principal fixture and a per-spec cleanup registered during an
ordered-suite restart. They were corrected and rerun. A final state/progress
check added preservation of committed counters on cancellation, and malformed
target anchor hashes remain a target-derived unresolved fact rather than a
source completeness failure. No required assertion, layer or timeout was waived.


The complete restored-code `make test-persistence` passed all 427 selected specs
in 1,075.529 seconds (17m55.529s). Against the M1 baseline of 836 seconds, the
observed stage delta is +239.529 seconds. SQLite passed 104 specs in 501.976
seconds (8m21.976s), +99.976 seconds against its 402-second baseline. The eight
new ordered specs account for 13.010 seconds of native spec runtime in that full
run. Cross-run totals include host/load/order variation; their full delta is not
an isolated measurement of the added specs. The Supervisor-owned persistence
budget is 1,800 seconds; the Writer did not change its runner or TESTING record.
The aggregate `make test` and independent milestone verdict remain Supervisor-owned.

All 29 distinct named reversals (30 executions, including the repeated coupling
check) discriminated through actual native assertion failures and restored every
mutated file byte-identically. The canonical runner counts 427 selected specs;
two additional passed suite setup/cleanup reports are not product test cases.


## M2 correction boundary

Page-only target and explicit `from` endpoints use the immutable regular HTML
entry under the shared renderer classification and its inventory hash in both
resolution and commit recheck.
They do not require a search index, page unit or successful EOF. Region endpoints
retain all current-revision, complete-unit and unique-anchor checks. Failed or
cancelled extraction leaves those region endpoints pending.

`internal/application/references/resolve.go` follows in-version symlinks for both
endpoints with the existing query resolver's exact-first lookup at each hop,
cycle detection and rejection of absolute, NUL, invalid UTF-8 and escaping link
text. Edges store the canonical resolved paths; declarations retain authored
aliases. UUIDv5 remains the library/source-version/local-reference identity,
independent of navigation spelling. Unresolvable aliases produce no edge or
target-disclosing source state. The shared renderer classification is the page-admission authority: the resolved
regular entry needs a case-insensitive `.html`/`.htm` suffix. A suffix-less alias
resolving to an HTML entry is a page; metadata alone does not turn a suffix-less
file into one. The package contract's conditional supported `text/html`
classification is not supplied by metadata `pages` membership. The earlier
page-set-only S1 conclusion was incorrect and is
superseded by the M2-4 correction below.

`internal/application/jsonstream/json.go` contains the existing importer's value
walker, moved without a second grammar. The importer facade preserves its error
identity. Manifest maintenance skips unrelated values into `io.Discard`, using
bounded buffers, and starts its slice deadline before the top-level pre-scan.
After a completed top-level member it checkpoints the consumed byte offset when
the deadline has passed. A large member finishes before yielding; there is no
hard wall-clock interruption inside a JSON value. The checkpoint distinguishes
member boundaries from reference-array offsets; old zero-offset checkpoints
still start at the object, and old positive array offsets retain their meaning.
A resumed scan does not revisit preceding members. Reference item transactions
and declaration-before-resolution ordering are unchanged.

The M2-2 parser correction advanced `referenceRevision` to 2, preserving revision-1
job history and canonical edge identities. The unit coupling mapping includes
the shared streaming grammar; search extractor and search ordering revisions
remain unchanged. Migration DDL and public surfaces are unchanged.


### Executed correction checks

The offline prepared toolchain passed the full `make test-unit` layer (129 Go
specs and 10 Python cases) and `make test-int` layer (33 Go specs and 51 Python
cases). The focused race persistence command selected 16 SQLite specs across
reference migration/storage, resolution and ordered work; all passed in a
43.607-second suite (55.054 seconds including command/build overhead). The work
package's two unit scenarios passed in the full unit layer; it has no separately
selected persistence scenarios.

The shared ordered reference fixture now has 10 specs totalling 21.337 seconds,
compared with the earlier M2 eight-spec sum of 13.010 seconds: +8.327 seconds,
including fixture expansion and execution variation. The two added resolution
specs themselves totalled 4.810 seconds. No new full persistence-stage or full
SQLite-suite timing was measured in this correction round; the earlier aggregate
measurements above remain historical evidence.

`RF04-PAGE` proves page-only target and `from` resolution after failed/cancelled
search before EOF, including absent extractor output. Region counterparts remain
`anchor_pending`. `RF04-ALIAS` proves canonical paths and UUIDv5 identity for both
alias endpoints, authored alias preservation, unresolved cyclic/outside/absolute/
dangling links. Its former extension-less-file success assertion was incorrect;
the M2-4 correction inverts it to unresolved for both endpoints.
`RF06-PRESCAN` finds all 70 declarations after 20/40 MiB questions and 100,001 objects
in the pages array. Full-layer scan allocations were 748,328 and 739,304 bytes respectively,
both below the fixed 1 MiB assertion. Each pre-scan yielded twice with zero
backward member rescans; legacy zero-offset checkpoint decoding also passed.

Six native reversal executions failed their intended assertions: restore the
page-only EOF dependency, reject aliases, restore the suffix gate, restore token
skipping, suppress the scan deadline, and start that deadline only after pre-scan.
The token mutation allocated 95,234,608 bytes; both deadline mutations exposed
zero pre-scan yields. Each execution restored every mutated source byte-identically. The historical
suffix-gate reversal only distinguished the former S1 assertion; it is not
evidence that metadata can classify a non-HTML entry as a renderer page.

Public E2E ran all job/event/derived-work-observing modules except the explicitly
excluded F12 module: `tests/e2e/test_references.py`, `tests/e2e/test_search_jobs.py`,
`tests/e2e/test_search.py` and `tests/e2e/test_headless.py`. All 51 cases passed,
including the independent SC16 large-body case. `tests/contracts/run.py` passed
all 45 cases with a new private report. `gofmt -l` was empty, `go vet ./...` and
`git diff --check` passed. The shared streaming walker body was compared with the
previous importer implementation and is identical apart from package/error naming.
F12, `make test-e2e` and `make test` were not run in this correction round; the
Supervisor retains the canonical gate. No independent review result is claimed.


## M2-4 / M2-5 correction boundary

`internal/domain/html.go` owns `IsHTMLPath`, extracted byte-for-byte from the
renderer's classifier. Renderer delivery, query page classification and reference
resolution share it. It runs on the canonical entry after link resolution;
metadata `pages` declarations do not override renderer classification. Page-only
commit rechecks use the same helper and inventory hash, without search EOF.
Region validation keeps its current extractor/unit/EOF/anchor checks.

The reference resolver and page-only anchor snapshot no longer call `tx.Page`.
`FinalizeImport` already inserts every regular `.html`/`.htm` inventory entry into
`version_pages`, so the lookup adds no constraint beyond the inventory plus shared
HTML classification. Metadata can also insert non-HTML entries there, which is
why membership alone cannot authorize a page pin. This changes no renderer bytes
or payload retention. The shared classifier joins the pinned reference algorithm
mapping and advances `referenceRevision` to 3; search revisions and migration DDL
remain unchanged.

`RF04-ALIAS` now covers both endpoint directions for suffix-less `alias` resolving
to `real.html`, retaining authored aliases and canonical edge paths/UUIDs. A
metadata-declared extension-less regular file stays unresolved for target and
`from`, while source materialization remains ready and non-disclosing. The actual
HTTP renderer regression also follows a suffix-less alias and compares the full
HTML derivative byte-for-byte with its canonical delivery.

`RF07-REV sweeps` creates its own region-bearing and page-only edges. The drift
step selects its exact source/reference key with explicit non-null target page/
region predicates and asserts that the edge exists and has both fields before
use. No arbitrary UUID ordering or preceding ordered spec supplies its inputs.
Page-only edge identity and tuple are checked independently across revision
revalidation. The other new `LIMIT 1` occurrence is constrained by exact version
and work kind, ordered by the unique extractor revision; it intentionally selects
the newest work key and has no arbitrary-edge ambiguity.


### Executed M2-4 / M2-5 checks

The full `make test-unit` layer passed 129 Go specs and 10 Python cases;
`make test-int` passed 33 Go specs and 51 Python cases. Focused race unit suites
passed all 3 reference and 12 renderer specs. The focused HTTP renderer race run
passed 2 specs covering actual verified delivery/aliases and corrupted HTML.

`go tool ginkgo --race --label-filter=persistence --repeat=5
--focus='RF07-REV sweeps' ./internal/adapters/sqlite` passed all six independent
suite attempts. The one-spec JSON report records the last attempt; the native
repeat log records all six successful attempts. The whole SQLite persistence
suite then ran once and passed all 106 specs, with zero failed, panicked, pending
or skipped cases. Its drift test executes on a required region-bearing edge;
page-only identity revalidation is a separate assertion.

The `RV-HTML` reversal restored page-set-only admission in initial resolution and
page-only commit snapshots. The inverted non-HTML assertion failed in the native
SQLite spec, then both mutated files were restored byte-identically. Suffix-less
aliases remain successful, because classification uses their canonical entries.

Public E2E passed all 51 cases from `tests/e2e/test_references.py`,
`tests/e2e/test_headless.py`, `tests/e2e/test_search_jobs.py` and
`tests/e2e/test_search.py`. The contract matrix passed all 45 cases with a new
private report. `gofmt -l` was empty; `go vet ./...` and `git diff --check` passed.
The four protected BUDGET1 files and all three protected E2E files retained their
starting bytes throughout this correction. No F12 module, `make test-e2e` or
`make test` was run; the Supervisor retains the full canonical gate.

Command durations are information only, not functional or quality judgments:
repeat run 16.816 seconds, full SQLite command
413.842 seconds and public E2E 494.984 seconds.


## M3 implemented boundary

`internal/application/query/references.go` implements the source-only paginated
list and exact query-ID resolver. Each bounded transaction authenticates and
checks the referring version; target projection applies the same `source.Check`
rule. D4 uses `ReferenceLifecycle`, a minimal retained project/lifecycle SQL read,
and a consistent existing edge; prior reads supply no witness. Trash is hidden
for owner and scoped callers. Target lifecycle is filtered before diagnostics.
`internal/application/references/inspect.go` shares maintenance's exact inventory/
HTML/anchor inspection without writes. Verified static marked placements run
outside `Store.Write` in `internal/application/query/reference_context.go` and
rejoin immutable inventory/anchors and current eligibility before output.

`internal/adapters/http/v1/references.go` owns closed query admission and
GET/HEAD dispatch. The outer auth router starts the deadline before its first
Guard; both routers suppress all HEAD bodies and preserve future routes.
`cmd/haeryong/reference_cli.go` owns the additive list/resolve commands and
feature-specific unavailable exit mapping, with one feature GET and no retry.
All client/transports, compiled frontend and Reader behavior remain M4 work;
`features.references` remains absent/false. No independent milestone or Task
completion verdict is claimed here.

The lean ordered seeded fixture in
`internal/adapters/sqlite/reference_events_test.go` covers RF02-READONLY pending,
ready, denied and never-before-read historical snapshots; RF03-C old-cursor
continuation and byte equality; RF03-E complete paired live/replay objects;
current authority and cancellation of the resolver's own store-lock waits. It
branches one physical database baseline sequentially, preserving library/runtime/stream/principal identities
and source declarations. The alternate branch substitutes matched unrelated
hidden control work for the unknown referenced target. It compares complete
projected events without removing differing fields. The incoming slice appends
no event; source materialization revision remains unchanged. Target-authorized
subscribers retain the target's own source event.

Incoming reference checkpoint persistence now omits per-row job progress events
because those would reveal hidden incoming cardinality through generic masked
invalidations. It still persists bounded restart progress; ordinary target job
completion remains. This completes the D8 event boundary without changing the
source-event shape or introducing a public edge revision.

`manual_references` keeps exhaustive seeded anchor/lifecycle/query variants
outside the canonical layers. Public `tests/e2e/test_references.py` inspects only
HTTP/CLI responses and durable job identities, including unknown-to-ready target
publication, unchanged cursor bytes, pinned older-target navigation and bodyless
HEAD errors. It never opens SQLite. Historical purge is seeded internal evidence;
no production lifecycle API is claimed. Contract examples/negatives retain the
closed response states and now admit the authorized unresolved page fallback.

### Executed M3 checks

`make test-unit` passed 137 Go specs and 10 Python cases. `make test-int`
passed 33 Go specs and 51 Python cases. The focused race run passed all 8 new
query/HTTP/auth/CLI unit specs. Related seeded race persistence passed 21
canonical specs (20 SQLite reference/migration/maintenance/projection specs plus
the existing auth live/replay event regression); the separate `manual_references`
case also passed. The manual matrix checks static duplicate contexts, identical
placements inside an explicit region, ambiguous canonical paths, ambiguous and
missing anchors, drift before/after authority masking, trash precedence, closed
query admission and stale permission captures. It stays outside canonical layers
using the existing manually labelled `Describe` registration pattern.

RF02-READONLY snapshots cover every reference/materialization/edge/job/derived
checkpoint/outbox row in pending, ready, denied and historical states. RF03-E
compares complete paired live and replay event objects, independently asserting
zero incoming-slice events. RF03-C compares exact cursor bytes and complete
source-only rows before/after incoming work; old cursors continue and authorized
rows can resolve live. RF05 verifies pinned exact Reader routes/navigation.
RF08 checks closed query/CLI admission, errors/HEAD, cancellation of the resolver's
own store-lock waits and deadlines before both outer and product authentication,
including malformed encoded source IDs.

All seven M3 reversals produced native assertion failures and were restored
byte-identically: RV-GETWRITE (snapshot equality), RV-READHISTORY (never-read D4
410), RV-TARGETEVENT (paired complete source-only event equality),
RV-EDGEREVISION (source-state/cursor equality), RV-REPLAYFILTER (complete hidden
source replay/live equality), RV-CURSOR (cursor bytes) and RV-READFILTER
(source-only row equality). No reversal uses a static prose check as evidence.

Public E2E passed all 53 cases across `tests/e2e/test_references.py`,
`tests/e2e/test_headless.py`, `tests/e2e/test_search_jobs.py` and
`tests/e2e/test_search.py`. The final reference module was repeated after the
encoded-path deadline correction and passed its one public scenario again.
The selected contract handler passed all 45 cases with a new private report;
those 45 are included in the 51 integration Python cases. `make test-prepare`,
`gofmt -l`, `go vet ./...`, documentation validation and `git diff --check` passed.
Prepare includes the four headless compilation targets and dependency boundary.

Initial failed attempts corrected fixture admission (credential spelling,
fragment decoding, empty checkpoints, explicit new-document import selection
and manual-label registration). The implementation also preserves a pending
target diagnostic when a bound source placement succeeds. Final executions
above passed; native failed-attempt and reversal evidence remains private.

Elapsed command times are information only: related seeded persistence/auth
28.339 seconds, public E2E 497.00 seconds, repeated final reference E2E
21.20 seconds. They are not functional or quality judgments. BUDGET1's limits
are hang guards. No full `make test`, F12 module, new browser/native/client gate
or independent review was run by this Writer in M3; Supervisor owns the frozen
canonical gate and independent milestone verdict. Production lifecycle actions
and M4/M5 full-delivery/real-sample acceptance remain unclaimed.


## M3 correction: shared HTML scope

`internal/application/search/element_scope.go` exposes the existing
`elementStack` through a narrow adapter; it adds no second HTML grammar.
`internal/application/query/reference_context.go` uses that same implied-end/
scope implementation for marked placements. The extractor, `element_stack.go`
and existing extractor regression sources remain byte-identical. No stored
anchor/parser behavior changes, so the existing reference revision/coupling
mapping stays unchanged. No other newly added reference DOM walker was found.

The ordered reference fixture adds one source publication containing eight
placements: omitted paragraph close before a block or paragraph sibling, list
item, table cell and row siblings, plus nested and omitted-end valid placements.
Every wrong placement returns `source_context_mismatch` with null target/label/
edge/route and zero target eligibility/structural reads. Valid placements still
resolve. The complete observation matrix is asserted together, so its reversal
runs every sibling case instead of stopping at the first wrong placement.

The source-context read uses the existing foundation reader, whose pin release
is not cancellable (pre-existing behavior); a cancelled or expired request
returns after the store gate frees.

### Executed M3 scope-only correction checks

`internal/adapters/foundation/foundation.go` and
`docs/testing/storage-foundation.md` match HEAD `5f94cd1` byte-for-byte.
The added `internal/adapters/foundation/read_cancellation_test.go` is removed.
The shared-scope adapter, reference placement and eight-case regression remain
byte-identical to the preceding candidate. The HTTP record had no added M3-2
sentence to remove; earlier M3 and shared-scope text is unchanged.

| Check | Observed result and scope |
| --- | --- |
| Complete `make test-persistence`, standalone execution | 435 race Ginkgo specs passed across all 17 suites, including every canonical reference spec |
| Renderer delivery within that persistence execution | All 15 passed, including `drains an active verified read and releases its durable pin before renderer shutdown returns` |
| Shared-scope matrix within that persistence execution | Passed; five wrong placements reject before target projection and three valid placements resolve |
| Complete `make test-unit` | 137 race Ginkgo specs and 10 pytest tests passed |
| Complete `make test-int` | 33 race Ginkgo specs and 51 pytest tests passed |
| Public `tests/e2e/test_references.py` | 1 passed |
| Selected-contract handler with a fresh private report | 45 passed; also included in the integration layer's 51 pytest tests |

RV-SCOPE was run once for this correction: restoring the local push-only stack
failed at the native eight-case matrix assertion. The shared-scope implementation
was restored byte-identically before the positive checks. The initial persistence
attempt could not start its client suite because concurrent Ginkgo invocations
shared and cleaned up the same compiled test path; no product assertion failed
in that attempt. After all other invocations ended, the complete standalone
persistence execution above passed.

`make test-prepare`, empty `gofmt -l`, `go vet ./...`, documentation validation,
the regenerated documentation manifest and `git diff --check` passed. Elapsed
times remain informational. No full `make test` or independent review was run
for this correction; the Supervisor owns the frozen canonical gate and
independent milestone verdict. Capability enablement and M4/M5 delivery remain
unclaimed.


## M4 shared clients and Reader boundary

`frontend/src/client/types.ts`, `http.ts` and `native.ts` supply the two closed
`versionReferences` and `resolveReference` operations. The HTTP adapter encodes
IDs/query values once; `desktop/readerfacade/reader.go` forwards only the named
fixed GET routes, preserves typed denial/gone results and binds cancellation to
the captured attachment. The facade's operation parity and call/denial tests
cover the same closed operation set. No arbitrary HTTP, URL or native operation
is exposed to imported content.

`frontend/src/viewer/{loader,channel}.ts`, `isolated-viewer.tsx` and
`frontend/src/app/screens/reader-navigation.ts`
carry only an opt-in reference ID under the current page/channel authority.
The default-disabled `reference.navigation {enabled}` gate admits only explicit
`features.references=true`; capability loss removes callbacks/interception and
discards snapshots/resolutions. Fabricated and stale messages cannot choose a
target or source version. The host sends the captured canonical source page and
declared region to the bound resolver, preserving authored alias spellings.

Each resolved reference pushes one exact target entry and retains a validated,
principal/runtime-bound source URL, context and position. Return pushes one
explicit saved-source entry, so an intervening ordinary child page does not
change the destination. Browser Back/Forward and nested immediate-context
replacement retain their ordinary semantics. No return chain, credential or new
return query parameter is serialized. Whole-version references retain version
context without inventing a default page; page-only legacy pins remain usable.

The Reader owns the once-parsed host region, selects it under the full
version/page/context/reload identity and waits for the exact ready channel to
focus. Missing/duplicate anchors report honest failure; static reference
resolution never upgrades a runtime-only DOM element into a validated anchor.
Actual browser proof also exposed two composition defects: browser-normalized
initial location echoes changed opaque query/fragment strings, and state-less
hash/connect entries could leave the Reader without a context. The shared
`frontend/src/viewer/reader-location.ts` uses one tracker per viewer effect/channel.
Repeated normalized initial echoes retain the original opaque tuple only until
the first different tuple. Every later report stays exact, including a return to
the normalized initial spelling; a reload starts a fresh tracker. No percent
decoding occurs. `frontend/src/app/router.tsx` retains the live context
for hash-only entries and establishes a new context for a different route before
rendering. The same-page hash proof checks that the actual frame instance stays
unchanged; explicit connect-return and reload retain the complete encoded route.

`internal/application/query/service.go` advertises `features.references=true`
for this shared delivery. The public reference E2E assertion now expects true.
The two previously named command-boundary/HTTP test files already contained no
reference-absent assertion at the accepted Part A base; their unrelated future
capability assertions are unchanged. Test-owned absent/false projections remain
required browser/native cases, rather than claims of old-server compatibility.

### Reproducible M4 proof entrypoints

The browser driver is
`internal/adapters/http/auth/browser/check_references.py --browser-cli <installed-playwright-cli> --receipt <private>`.
It uses the existing CLI pattern and an isolated tagged production daemon.
`tests/manual/reference_support.py` builds explicit F03/F13 trees and external
metadata using runtime service identities, with one import per tree. F03 retains
the old exact target after a successor removes its pages. F13 distinguishes
same-ID sibling pages, Unicode/percent/plus/space paths, id/data/both anchors,
duplicate/missing/runtime-only anchors and an unannotated page. The opt-in
`--corpus` path prepares the unchanged Gaori G12 → Mulgae M11 payload mapping for
M5; M4 synthetic results do not claim that real-sample checkpoint passed.

`spikes/viewer/check_reader_native.py --stages app,references --receipt <private>`
uses the actual pinned Wails beta28 build, compiled native App and closed Go
facade. `reference_native.py` owns the labelled loopback capability/fault
projection; `reference_probe.js` observes actual loader traffic and host state.
Native keyboard events target only the owned test application's PID. Authored
fixture observers report actual DOM/loader results and provide no host or native
authority. The delayed-loss probes observe settlement of the mounted client's
ordinary resolver before any fallback page move; the observer preserves the
original result/error and changes no authority guard. Native readiness matches
the current frame's actual announcement/render instance, not a prior page's
record. Drivers retain every evaluated RF row and private source/input/binary
hashes; browser and native renderer-origin users run serially.

The enabled ordinary reference paths use the real daemon/resolver. Absent/false,
delayed capability loss and 403/410 error responses are explicitly seeded;
unavailable and runtime-only-anchor outcomes use real resolution. The explicit
existing safe `/connect?return=` path and reload are in scope. Full automatic
login/reconnect composition and C05 remain the **TASK-004-05** handoff. M5/M11,
whole-Task acceptance and Supervisor-owned independent/canonical gates remain
unclaimed.

### Executed M4 Writer checks

| Check | Observed result and scope |
| --- | --- |
| Actual Chrome F03/F13 driver | 18 rows passed: exact cross-project old-version click, host selection and child focus, source scroll/return, one-step Back/Forward, reload, encoded query/fragment/region, keyboard Enter, Meta/new-window fallback, whole-version/page-only legacy pins, hash-only same frame, sibling/stale ack, missing/duplicate/runtime-only anchors, phone touch/modal close and explicit safe connect return |
| Actual pinned Wails `app,references` | Original compiled-App/large-context/guard/204-close case plus all four reference capability modes passed; 17 RF rows, real PID-targeted Enter/ArrowDown inputs and two guard-observed ordinary fallback popup destinations |
| RF09-G browser/native projections | Absent/false: zero resolver calls and ordinary fallback; true: one resolver call for the first exact click; live true→false: one held real resolver call, observed disabled channel, actual client settlement before fallback, no stale navigation or fabricated disabled dispatch |
| Frontend regeneration and validation, explicit npm suite | 348 Node tests passed; typecheck, byte-for-byte committed browser/loader dist verification and the separate native build validation passed |
| Desktop race suite and vet | All three desktop packages passed `go -C desktop test -race ./...`; `go -C desktop vet ./...` passed |
| Root unit layer | 137 race Ginkgo specs and 10 Python cases passed |
| Root integration layer | 33 race Ginkgo specs and 51 Python cases passed |
| Public reference E2E (preceding Part B run) | 1 passed, including true capability, immutable bytes, delayed target eligibility and exact pinned HTTP/CLI results |

Native focus remains qualified: the initial programmatic exact-reference ack
reported `found=true, focused=false`. After actual ArrowDown input and host
outline re-selection following the modal's real close, the current native frame
reported `found=true, focused=true`. The receipt preserves every initial and
subsequent acknowledgment; delivery alone is never counted as focus success.
The native recovery row requires the current channel's ack to contain both
`found=true` and `focused=true`; other rows still accept and record an initial
`focused=false` as `native_focus_limit`. The browser requires actual
`focused=true`, and separately observes the selected host scope and active
artifact element. In both drivers, each unavailable/gone/denied/runtime-only
notice row asserts the unchanged route, the same ready source page/channel and
render instance, the same connected visible frame and present visible `#origin`
content. Native `inspect` adds only fixture-owned DOM/channel observations.
Native popup proof observes safe destinations through the guard; it does not
claim browser pairing in that popup.

The location regression checks repeated initial echoes, a move, a later return
kept as reported and a fresh tracker's reset. Its isolated reversal maps every
matching report and fails the later-return Node assertion. The router's pure
`reader-context.ts` helper has regressions for absent-state hash-only retention,
a new context for a different document and preservation of an entry's own stamp.
Its isolated reversal creates a new context for hash-only entries and fails that
Node assertion. Each helper was restored byte-identically and its regression
passed again; working-tree inputs stayed unchanged during both reversals.
Accepted Part A capability/alias/history guards remain in the full suite.

Earlier attempts retained failed RF rows. The final drivers bind observations
to the current frame and actual client settlement, close native modal dialogs
using their real state, and execute browser/native renderer-origin users
serially. Chrome's existing sandbox-attribute warning and expected 403/404/410
failure-case diagnostics are retained; the actual artifact origin was distinct
from the trusted host, with no page/framework errors. No network installation,
full `make test` or independent milestone verdict is part of these Writer runs.


`make test-prepare` passed, including root vet, module/dependency checks and
all four headless compilation targets. Root Go formatting, documentation
integrity with the regenerated manifest and `git diff --check` also passed.


## M5 real samples and final regression evidence

RF12 imports the complete checked-in Mulgae and Gaori sample directories using
reviewed explicit lists of four files each in `tests/manual/reference_support.py`.
No sample directory walk or ambient Git selection is used. Mulgae is imported
first; external Gaori metadata maps `prior-compose` from `gaori/lifecycle.html`
/ `G12` to that runtime Mulgae version / `recovery/compose.html` / `M11`.
External region declarations enable the ordinary host outline without changing
any authored HTML. Every imported file set equals its explicit list and every
file round-trips byte-identically. Historical explanatory prose stays verbatim.
A labelled synthetic successor of the Mulgae document removes the old compose
page; the exact reference continues to open the pinned old version.

`tests/manual/check_reference_corpus.py --receipt <private>` owns six public
real-sample worksheet rows: list, exact resolve, both HEADs, HTTP/CLI parity,
no-retarget after the successor, and complete byte-identical inventories.
Its successor row also reverses the expected version, page and region separately;
each wrong tuple fails a native Python assertion, then the expected tuple is
restored byte-identically and the positive assertion passes. These are labelled
oracle controls, not product-code mutation evidence.

The same driver explicitly runs `manual_references` with race detection. The
coverage map in `internal/adapters/sqlite/reference_matrix_test.go` first names
26 cells represented in canonical persistence and one already covered by the
existing manual stale-grant case. It evaluates the remaining 261 cells of six
actors × six target states × eight surfaces, with no duplicate cells. Actors
are owner, source-only scope, both endpoints, missing `version.read`, stale
captured permissions and a denied F10 project. Target states are active,
archived, trash, purged with a pre-existing never-read edge, purged without an
edge and unknown. Surfaces are list/resolve GET and HEAD, the actual CLI's list
and resolve, production live-event filtering and replay. Live filtering here
uses `FilterEvent`; this matrix does not claim a second SSE transport test.

Every remaining cell compares the reference/materialization/edge/job/checkpoint/
outbox snapshot before and after the read. Structural edges exist before any
public read and survive seeded lifecycle changes; already-purged/unknown targets
create none. Masked responses contain no hidden title, body, region label,
identity, edge ID or count. Every success envelope has exactly HTTP `data` and
`request_id`, or CLI `data`, null `error` and `request_id`, with a canonical UUID.
Complete JSON-object equality requires every ReferenceView member, declared
`id`/`from`/`relation`/`label`, exact resolved document/version/page/region and
requested/resolved spelling, empty omitted navigation strings, canonical edge
and exact encoded Reader URL. Lists require exactly one item, null `next_cursor`
and exactly `materialization={state:"ready",reason:null}`. Unavailable rows
require `reason="target_unavailable"`; historical targets remain only the
retained document/version pair, with null label/edge/route.
Canary and hidden-identity checks cover the complete raw success/error envelope
bytes and every decoded JSON key and string, including `request_id`, before
error-envelope validation can return.
Every nonzero CLI cell requires exactly null `data`, the established
exit-to-error-code mapping, the expected HTTP error code as its message and a
UUID `request_id`. Every HTTP 403/404/410 GET requires the exact closed error
object, matching code/message and `retryable=false`; 403/404 details are exactly
empty, and 410 details are exactly the retained document/version pair. Required
members cannot disappear into struct zero values. Visible live/replay events
equal their persisted originals completely and independently require
`kind="references.updated"`, `resource_kind="version"`, the expected source or
target version identity, no body and that version's current reference
materialization revision read from the store. Masked events equal only the
original epoch/sequence plus `resource_kind="library"` and
`kind="library.invalidated"`. Live projections retain their input-original
pairing, replay rejects duplicate selected sequences, and stale grants still
require `ErrDenied`. The driver predeclares every
row and retains all misses before exiting; private evidence binds driver/source/
payload hashes, commands, runtime mapping and expected/actual exact tuples.
These seeds do not implement grant, trash or purge workflows.

M3 mapped `NOT_FOUND` to exit 5 for reference commands; final review aligned
these commands with the shared error registry's exit 3.

The search corpus helpers now select `work_kind == "search"` when locating the
one search-derived job; reference publication adds a separate derived job.
The driver-wide search found no other stale one-job assumption. Search ranking,
worksheet queries, sample bytes and historical TASK-004-02 evidence are unchanged.

### RF01–RF12 sources and discriminating controls

The milestone records and M4 Part A supplement below retain the executed
product-reversal details;
M5 does not substitute static prose matching for those assertions.

| Group | Test source | Reversal or control |
| --- | --- | --- |
| RF01 metadata and identity | `internal/application/import/references_test.go`, `tests/contracts/test_reference_contracts.py` | RV-FIELDS, RV-CLOSED, RV-SCHEMA-PARITY, RV-UUID |
| RF02 exact structural pins and read-only state | `internal/adapters/sqlite/reference_worker_test.go`, `reference_events_test.go`, `tests/e2e/test_references.py` | RV-STAGE, RV-ACTOR, RV-GETWRITE, RV-READHISTORY |
| RF03 eligibility, historical masking, cursor and events | `internal/adapters/sqlite/reference_events_test.go`, `reference_matrix_test.go` | RV-READFILTER, RV-CURSOR, RV-TARGETEVENT, RV-EDGEREVISION, RV-REPLAYFILTER |
| RF04 source DOM and canonical endpoints | `internal/adapters/sqlite/reference_worker_test.go`, `reference_events_test.go` | RV-PAGE, RV-ALIAS, RV-HTML, RV-SCOPE |
| RF05 target anchors and drift | `internal/adapters/sqlite/reference_events_test.go`, `tests/manual/reference_support.py`, browser/native reference drivers | Wrong-page/same-ID, missing/duplicate/runtime-only and drift controls; RV-READFILTER keeps hidden anchor reasons private |
| RF06 migration, admission and backfill | `internal/adapters/sqlite/references_test.go`, `reference_migration_test.go`, `internal/application/references/manifest_test.go` | RV-FK, RV-ENUM, RV-ADMISSION, RV-TARGETINDEX, RV-RECOVERY, RV-TOKENS, RV-DEADLINE |
| RF07 ordering, recovery, fences and revisions | `internal/adapters/sqlite/reference_worker_test.go`, `internal/application/work/dispatcher_test.go`, `internal/application/references/revision_test.go` | RV-INCOMING, RV-COMMITFIRST, RV-OUTGOING, RV-SOURCEGATE, RV-ROUTERGATE, RV-GATESTATES, RV-LIMITFIRST, RV-REOPEN, RV-FENCE, RV-CANCEL, RV-COUPLING, RV-SWEEP |
| RF08 HTTP/CLI contracts | HTTP `v1/references_test.go`, `auth/references_test.go`, `cmd/haeryong/reference_cli_test.go`, public reference E2E | Closed-query/HEAD/error/one-GET controls; RV-GETWRITE and RF03 masking reversals cover underlying read authority |
| RF09 transports and capability | `frontend/src/client/references.test.ts`, `reader-navigation.test.ts`, desktop facade tests, browser/native capability modes | RV-FEATUREGATE and RV-STALEFLAG absent/false/true and delayed-loss controls |
| RF10 host region binding | `frontend/src/app/screens/reader-navigation.test.ts`, browser/native RF10 rows | Same-ID sibling/stale ack, hash-only same-frame, missing-region and successful recovery controls; hash-context helper reversal |
| RF11 real click and return | `reader-navigation.test.ts`, `reader-context.test.ts`, browser/native reference drivers | Nonadjacent return, nested replacement, encoding/initial-echo sequence and byte-restored context/echo reversals |
| RF12 real M11 analogy | `tests/manual/check_reference_corpus.py`, `check_references.py --corpus`, native `reference-corpus` | Synthetic newer target plus wrong-version/page/region oracle controls, exact click/selection/focus/return/Back/Forward/reload |

The following execution index connects every named reversal above to its
retained milestone record; the earlier results are unchanged:

| Reversals | Execution record in this document |
| --- | --- |
| RV-FIELDS, RV-CLOSED, RV-SCHEMA-PARITY, RV-UUID, RV-FK, RV-ENUM, RV-ADMISSION (READ/WRITE/WORK), RV-TARGETINDEX, RV-RECOVERY | **M1 focused results**, the 13 native assertion failures and byte-identical restores; repeated DDL/admission/index/recovery checks in **M2 executed checks** |
| RV-STAGE, RV-ACTOR, RV-INCOMING, RV-COMMITFIRST, RV-OUTGOING, RV-SOURCEGATE, RV-ROUTERGATE, RV-GATESTATES, RV-LIMITFIRST, RV-REOPEN, RV-FENCE, RV-CANCEL, RV-COUPLING, RV-SWEEP | **M2 executed checks**, publication/staging and publisher authority, incoming/commit-first/outgoing order, both own-search gates, terminal states, pre-LIMIT admission, terminal-key reuse, attempt fencing/cancellation, revision coupling and historical sweep; 29 distinct discriminating reversals, 30 executions |
| RV-PAGE, RV-ALIAS, RV-TOKENS, RV-DEADLINE | **Executed correction checks** under **M2 correction boundary**: six failed native reversals restoring page-only EOF dependence, rejecting aliases, restoring token skipping, suppressing the scan deadline or starting it only after pre-scan (plus the separately qualified historical suffix-gate reversal) |
| RV-HTML | **Executed M2-4 / M2-5 checks**, restored page-set-only initial/commit admission, failed the non-HTML assertion, then restored both files byte-identically |
| RV-SCOPE | **Executed M3 scope-only correction checks**, restored the local push-only stack and failed the eight-case matrix |
| RV-GETWRITE, RV-READHISTORY, RV-TARGETEVENT, RV-EDGEREVISION, RV-REPLAYFILTER, RV-CURSOR, RV-READFILTER | **Executed M3 checks**, seven named native assertion failures and byte-identical restores |
| RV-FEATUREGATE, RV-STALEFLAG | M4 Part A execution detail immediately below, supplementing **Executed M4 Writer checks** |

M4 Part A executed RV-FEATUREGATE by making `featureEnabled` in
`frontend/src/app/screens/library-data.ts` enable references regardless of the
flag, and removing the `referencesEnabled` check from marked-link interception
in `frontend/src/viewer/loader.ts`. The intended RF09-G Node absent/false
zero-call and ordinary-fallback assertions failed, including the call-count
assertion (expected zero). RV-STALEFLAG retained an already-enabled flag in
`loader.ts`, retained the channel callback/enable message in
`frontend/src/viewer/channel.ts`, and ignored capability loss in
`frontend/src/app/screens/reader-navigation.ts`. The intended RF09-G live
true→false pending-callback/no-new-call assertions failed. Both mutations match
r3 §10.1, were restored byte-identically and their positive Node checks passed
again. The Part A correction run repeated both discriminating failures,
byte-identical restores and positive re-passes.
These are executed frontend reversals, distinct from the later actual
browser/native capability projections.

RF12 native uses `spikes/viewer/check_reader_native.py --stages
app,reference-corpus --receipt <private>`; `reference-corpus` is distinct from the
existing search `corpus` stage. Original sample HTML has no injected observer.
Real PID-targeted ArrowDown followed by host outline re-selection requires
`found=true, focused=true`; initial programmatic false results remain recorded.
Actual Option+Tab/Enter activates the authored `prior-compose` link. Loader
announcements and current-channel focus acks verify exact navigation and reload.
The source departure snapshot and saved history coordinates must agree and
must differ from both fresh-load and region-focus-only coordinates after real
PID-targeted scroll input. Return requires the current source instance's host
`reader.position.apply` with those saved coordinates and a nonempty correlation,
the matching child `reader.position.applied` with `applied="scroll"`, and a
subsequent actual loader snapshot at the saved coordinates. Source context and
exact Back/Forward tuples are checked separately.
The test-owned projection seeds only the existing status reply to acknowledge
that native input was posted; ordinary resolver and Reader traffic are forwarded
to the real daemon. This instrumentation grants no artifact/native authority.

A direct installed-CLI probe verified that `URL` and `URLSearchParams` are
undefined in its `run-code` context while the page realm defines `URL` as a
function. The former Reader/recovery driver bytes used those globals in
Node-side request listeners and route handlers. Earlier attempts stopped before
any product assertion was evaluated. A listener/handler exception causing that
termination is an inference, not an observed diagnostic in the earlier receipts.
URL parsing now runs in the browser or uses API-path strings and the actual
handoff form field; handoff counting retains exact pathname equality after
stripping scheme/authority and query. Product expectations are unchanged.
The Reader receipt binds its driver bytes and supports bounded redacted CLI
failure diagnostics; the designated earlier failed receipts contained no such
diagnostic. Failed instrumentation attempts remain private and are not counted
as passing product checks.

Final-tree regression execution results are recorded below. The correction
execution repeats the 6-row public worksheet, all 261 seeded remainder cells
and both manual specs with the exact CLI/HTTP/event oracles, all 9 recovery
scenarios with exact handoff pathname equality, and native `app,reference-corpus`
with all 4 RF12 rows. An initial correction-probe attempt tried to read fresh
coordinates from a `viewer.ready` event before its observer was attached; it
failed the discrimination check. The corrected probe uses the actual
`viewer.loaded.location` announcement. All failed rows remain retained privately;
no product assertion or code was weakened.


| M5 check | Observed result and scope |
| --- | --- |
| RF12 actual Chrome | 2 compound rows passed: exact pinned old cross-project target, host selection and true frame-focus ack, source return/position, Back/Forward and reload |
| RF12 pinned Wails | Original compiled-App case plus 4 RF12 rows passed; initial G12 ack was `found=true, focused=false`, then real input and outline recovery produced `found=true, focused=true`; exact authored link, old-version reload and source return/Back/Forward passed; fresh-load y=0 and region-only y=3426 differed from saved/restored y=3486, with correlated scroll apply/ack and actual snapshot equality |
| Public reference worksheet and seeded remainder | 6 worksheet rows and all 261 remaining matrix cells passed; 2 manual Ginkgo specs passed, including the pre-existing anchor/stale variants; 3 wrong-tuple oracle controls failed as expected and restored exactly |
| Existing Chrome Reader | 1 compound smoke passed: ordinary JS/module/worker, external Preact, exact large context, child history, saved positions/default, popup and import preselection |
| Existing Chrome evidence | 58 real/labelled-seed assertions passed, including first-commit/remount/page/region/service separation and keyboard/touch |
| Existing Chrome recovery | 9 scenarios passed: consumed handoff, initial renew/close, unknown open, inactive position, three held/lost history variants, lost close and independent dead receipt |
| Existing Chrome search | 8 real/labelled-seed cases passed |
| Existing SC19 real corpus and manual matrix | 67 query observations: 66 positives plus 1 expected negative-copy control, zero unexpected misses; manual_sc19 2 race specs, nested Chrome corpus 2 cases and native app/corpus 3 cases passed |
| Frontend validation | Typecheck, 348 Node tests and byte-identical browser/loader/native build verification passed; no frontend input change or regeneration |
| Root unit and integration | 137 Go + 10 Python unit, 33 Go + 51 Python integration passed |

The native selection `selfcheck,app,seed,restart,crash,evidence,search,references,corpus`
passed all 16 top-level cases and 23 selfcheck rows. This includes the original
compiled App, actual search and both corpus examples, real K seed/restart,
real/labelled-seed evidence, all four reference capability modes (17 RF rows),
forced crash without a fabricated close and a healthy subsequent attach.
Every selected stage completed, and the daemon closed. The receipt's legacy
`full_qualification` boolean tests exact default-set equality, so adding the
optional `corpus` stage leaves that boolean false; `selected_stages` records all
nine executed stages. These runs do not claim release/platform qualification.

The preceding evidence-only correction refreshed native `app,corpus` for the
RF12 helper/probe fingerprint and reused unchanged Chrome evidence. The final
CLI correction changes a fingerprinted product input, so the worksheet/manual
matrices, both Chrome reference modes, Reader/evidence/recovery/search and SC19
(including its Chrome/native corpus clients), native `app,reference-corpus` and
the full default native stages plus `corpus` are all repeated. All final selected
driver input fingerprints match the final source/product bytes.
Expected negative-copy and wrong-tuple controls remain separately labelled;
none is hidden as a positive result. Desktop bytes are unchanged, so the accepted
M4 desktop race/vet evidence is retained without repeating those checks.
Root prepare, vet/format, documentation integrity and whitespace checks complete
the Writer's checks; no full `make test`, independent result verdict or Task
lifecycle transition is claimed.

## Review and handoff

The Task Evaluator and the two non-authoring Executor reviewers reviewed the
plan revisions and every milestone candidate. The Epic Evaluator reviewed the
plan and the final Task result. Their findings were resolved before acceptance:
- plan r1's witness/GET-cache edge creation and pre-publication staging, replaced
  by the structural-edge, publication-first and own-search-gate decisions of r2
  and r3;
- the milestone corrections recorded in the M1–M4 sections above;
- the M4 location-echo and Reader-context composition defects that the actual
  browser proof exposed;
- the stale one-job assumption in the TASK-004-02 manual search drivers, which
  the Supervisor found while preparing M5;
- the M5 oracle and record corrections: exact CLI and HTTP error envelopes,
  hidden-identity checks that also cover error responses, exact event objects,
  exact handoff path, discriminating correlated native position restore, the
  RF09-G reversal record and the verified CLI-context cause;
- the Supervisor's final contract review. It aligned the reference CLI's
  `NOT_FOUND` exit with the shared registry (exit 3, as for every other
  command) and corrected the stale data-model and schema-copy statements.

The Supervisor completed the final project/product review and accepted the Task
under Master's delegated authority; overall EPIC-004 acceptance remains pending.

On the final candidate:
- The canonical `make test` passed: Go unit 137, integration 33 and persistence
  435 specs; pytest unit 10, integration 51 and E2E 55.
- Documentation integrity passed.
- The final evidence set passed:
  - frontend build equality and unit tests;
  - the Chrome F03/F13 and real-sample RF12 reference drivers;
  - the native `app,references` and `app,reference-corpus` stages, and the full
    default stages plus `corpus`;
  - the real-sample reference worksheet with the `manual_references` matrix;
  - the existing Chrome Reader, evidence, recovery and search drivers;
  - the real-sample search corpus driver with `manual_sc19`.
- The M4 desktop race and vet results stand, because the desktop did not change
  afterwards.

In the final regression round, two first attempts failed before passing on an
identical-input rerun. They are retained privately and are not counted as passes:
- The Chrome evidence driver's in-frame region selection ran before the frame's
  page channel was ready ("Page channel is not ready"). The driver does not wait
  for that readiness before selecting.
- One unlabelled 30-second wait in the native search stage's Library probe
  expired after the search index was complete. Its cause is unknown.

Neither attempt showed a product assertion failure. Neither could be attributed
to a TASK-004-03 change with confidence, and a passing rerun does not rule out
an intermittent cause.

Handoffs:
- TASK-004-04 owns the backlink panels and the reverse-index rebuild over these
  canonical edges.
- TASK-004-05 owns copied links and the automatic login and reconnect return.
  The router establishes a new Reader context for any new route entry without
  state, including the explicit connect return; the automatic return must
  compose with that rule.
- TASK-004-06 owns the two-project gate and reuses this record's real-sample
  drivers with RC01–RC14 and `manual_sc19`.

Limits:
- Seeded lifecycle and grant rows are not production trash, purge or grant
  workflows; TASK-005 repeats real transitions.
- Browser emulated touch is not a physical-device test.
- The native runs use the reused pinned ReaderMode harness with full
  qualification false.
- No provider, private-ingress or release claim is added.
