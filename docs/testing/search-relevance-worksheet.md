# Static search relevance worksheet

TASK-004-02 M1 freezes these expectations before production search execution.
M4 executes the X09 and calibration40 portions through the production query
service and SQLite adapter. M6 executes the fixed real-sample portion with the approved A1 tie amendment.
This worksheet declares expectations; the implementation record owns observed
results. Owner use is unmeasured.
The historical `tests/contracts/calibration-plan.json` remains unchanged.


Write a machine worksheet matching these rows before first product execution.
Record corpus hashes, runtime project/document/version alias mapping, principal/
filters and expected exact tuples/ranks. Do not invent UUIDs.
R=required repair; L=the specified unsupported behavior is disclosed, with the
declared negative outcome still required. No post-run R→L reclassification.
All filtering/identity/input-loss defects are R regardless of relevance score.

### 9.1 Isolated X09 seed rows

Reuse rows1..8 from spikes/stack/retrieval_test.go:32–35 separately.
Rows6/7 denied/trash; eligible1,2,3,4,5,8. Top5 means every named relevant row
within five eligible raw hits; isolated single-row controls assert exact membership.

| Query | Expected product targets/rank | Disposition |
| --- | --- | --- |
| 검토 | 1,8 top5; no6/7 | R |
| 검 | 1,8 top5 via corpus fallback with no FTS candidates | R |
| 재시도 | 1 top5 | R |
| "검토 승인" | 1; exclude reversed8 | R |
| "재 시도" on row1 only | No match; no whitespace-removal normalization | L |
| "재 시도" on row5 | 5 top5 | R |
| source / "source_access" | 2 top5; literal full symbol outranks aliases | R |
| Source against readSource only | 3 top5 by component/substring/casefold | R |
| readSource / source against readSource only | 3 top5; retain unicode61-alone negative control | R |
| "source/path.go" / go / "path.go" | 4 top5; short fallback where applicable | R |
| 검토 승인 unquoted | 1,8 top5, AND semantics | R |
| "source-path.go" on row4 only | No literal phrase match despite unicode61 separators | L |
| 비공개 / 휴지통 | Zero eligible items/count/snippets, all applicable paths | R |

Keep historical raw engine expected/observed controls unchanged; product aliases
do not rewrite old measurements. Add decoded fixtures.md selectiveRetry,
retry_failed_roles, retry.go, qualified symbols and Korean phrases 선택적 재시도,
작업 승인, 부분 실패; exact page/region top5 R.
Add NFC/NFD equivalence with original stored path returned, the §4.1 expanded
maximum-length boundary phrases (SC02), two pages sharing retry, regionless HTML, runtime-only anchor and unknown
attachment path; correct identity/coverage R. No broad spacing/morphology promise.

### 9.1.1 Alias-output and fixed component-rank oracle (Q1; SC01)

The isolated source→readSource membership row remains useful retrieval evidence,
but cannot discriminate aliases: the existing trigram control at
spikes/stack/retrieval_test.go:55–57 already finds it without aliases. Do not claim
that removing aliases makes this membership check fail.

M2 normalizer unit directly asserts generated components before FTS or SQL:
- readSource retains normalized literal readsource and emits exact component set
  {read,source}; named assertion aliases_readSource_has_source requires that set.
- HTTPServer retains httpserver and emits {http,server}.
- source_access retains source_access and emits {source,access}.
- resourceful retains resourceful and does NOT emit source as a component.
Split camel/acronym boundaries before case folding, normalize each emitted value
and compare component sets (not unrelated literal/substring output). Removing
GenerateIdentifierAliases makes the first three positive component assertions
fail directly even if all literal/trigram query paths remain intact.

M4 actual search-service/SQLite oracle keeps both trigram and literal substring
paths enabled. Use one eligible active version with exactly two ready page hits,
identical title Control, no matching metadata/topics/sources/regions, same project,
document and version, and no other source occurrence:

| Page | Only body text | source match class | Expected unquoted source rank |
| --- | --- | --- | --- |
| z.html | readSource | generated exact component, tier4 | 1 |
| a.html | resourceful | literal substring only, tier5 | 2 |

Search kind=page, versions=latest, limit=2; assertion
component_precedes_substring requires exact page order [z.html,a.html] and
membership of BOTH rows. Both have the same one body-field contribution and all
other tie keys equal until binary path. Without aliases both still match via
trigram/substring at tier5; a.html then sorts first by the existing final tie-break.
Thus removing aliases, or ignoring the component-versus-substring rank branch,
fails the order assertion while the old membership assertion can still pass.
No test-only disabling of fallback/trigram or extra scoring factor is permitted.
These two direct scenarios are required repair (R), additional to the unchanged
calibration40 denominator and approved real-sample worksheet.

### 9.2 Adopted calibration40

Retain calibration-plan.json unchanged: 1,000 documents, 5,000 versions,
20 topics, every17th denied/every19th trash, sequence5 actual latest.
Use actual production search service/adapter over a labelled seeded persistence
fixture with the same title/body/topic/lifecycle rule, not 5,000 public uploads.
Small public-import E2E and real-sample runner separately prove publication/
extraction. Never label seeded scale public-import E2E.

| IDs | Queries in order | Relevant topics in order |
| --- | --- | --- |
| Q01–04 | 검; 검토 reviewState; 재시도; retryFailed | 0 or3; 0; 1; 1 |
| Q05–08 | 승인; approveDraft; 검색; searchIndex | 2; 2; 3; 3 |
| Q09–12 | 취소; cancelRequested; 백업; backupSnapshot | 4; 4; 5; 5 |
| Q13–16 | 복원; restoreEpoch; 버전; versionID | 6; 6; 7; 7 |
| Q17–20 | 인용; observedInput; 출처; source/path.go | 8; 8; 9; 9 |
| Q21–24 | 권한; 권한 permissionGeneration; 임시; staging_upload | 10; 10; 11; 11 |
| Q25–28 | 연결; rememberedSession; 노트; pageAnnotation | 12; 12; 13; 13 |
| Q29–32 | 범; coverageSet; 보존; retentionPolicy | 14; 14; 15; 15 |
| Q33–36 | 참조; pinnedReference; 작업; jobAttempt | 16; 16; 17; 17 |
| Q37–40 | 조정; reconcileOnly; 설정; configRevision | 18; 18; 19; 19 |

Before run: >=36/40 have relevant eligible raw rank<=5, otherwise R.
Every exact code/path/mixed identifier remains findable (R). Short literal
correctness/exclusion R. Individual topical rank misses may be L only while the
fixed aggregate passes and relevant result is findable; disclose each cause.
Report all40 top-five tuples, first relevant rank, eligible relevance counts,
false positives and latency. No broad recall/owner-usefulness guarantee.

### 9.3 Agent-predeclared real sample queries

Public-import complete spikes/knowledge-value/samples/{mulgae,gaori} trees into
two projects using production initializer/importer; preserve all bytes/paths.
Sample source revisions: Mulgae cef337cf927b820316f42a41f1441fbc653203f8,
Gaori 393af5999bbbb30b5d11e2a17a2325c8cf1833b7.
Search consumes these HTML files, not sibling checkouts; no provider/source read.
Runtime IDs come from actual import. One version per project for this worksheet.

M-I=recovery/inventory.html; M-C=recovery/compose.html;
G-O=gaori/outcomes.html; G-L=gaori/lifecycle.html.
Each row: named project, active/latest, kind=region; expected first relevant
exact page/DOM-region raw rank<=5, all R.
Also RC02/08/12 across both projects with no kind filter, relevant raw rank<=5.

| ID | Query | Expected eligible page/region(s) | Static source location |
| --- | --- | --- | --- |
| RC01 | failed_run_recovery | M-I/M01 | inventory.html:20–45 |
| RC02 | "cleanup receipt" | M-I/M04 or M01 | inventory.html:61–67 |
| RC03 | "accepted role" | M-I/M03 or M02 | inventory.html:47–59 |
| RC04 | "cooperative" | M-I/M05 | inventory.html:69–73 |
| RC05 | "ordinary root" | M-C/M07 | compose.html:20–24 |
| RC06 | "optional role" | M-C/M08 | compose.html:26–48 |
| RC07 | reconciliation_state | M-C/M11 | compose.html:64–68 |
| RC08 | "missing required source" | M-C/M12 | compose.html:70–75 |
| RC09 | internal_error | G-O/G02 or G03 | outcomes.html:26–48 |
| RC10 | "parser miss" | G-O/G05 | outcomes.html:57–61 |
| RC11 | await_run | G-L/G09 | lifecycle.html:32–36 |
| RC12 | "accepted cancellation" | G-L/G10 | lifecycle.html:38–50 |
| RC13 | "invocation registry" | G-L/G11 | lifecycle.html:52–56 |
| RC14 | "replacement start" | G-L/G12 | lifecycle.html:58–63 |

Additional R checks: four exact page-path queries with kind=page, rank1 in their
project; opaque attachment exact path rank1; optional imported source metadata
logical repo-id query kind=source rank1 in isolated seeded-metadata import.
Label source metadata seeded, not a source verification result.
Add F10 denied and trash canary copies; repeat owner/scoped/denied variants.
No canary identities/bytes/count contributions/cursor payload/events.

Freeze expectations and hashes before first search. Wait for explicit completion,
query public HTTP and CLI, save ordered tuples; separately interrupt indexing to
prove partial coverage. Repeat startup/cold service and warm reads without an OS
cache flush claim. Browser/native search-and-open examples from both projects.
Search success does not validate claims or turn Gaori→Mulgae analogy into dependency.

Master may add personally authored queries before result review. Absence is
owner use unmeasured, never pass. Do not wait on optional queries to execute the
approved worksheet. Keep added authorship/date/expectations/disposition separate
from the fixed40 calibration denominator.


### M4 bounded observations

The frozen X09 machine worksheet executes all 18 rows: 16 R expectations and two
L negative controls pass, with no miss or reclassification. Calibration40 passes
40/40 (first relevant rank 1 for each row, no returned topical false positives)
on the declared seeded persistence corpus. Runtime UUID maps, all raw top-five
tuples and latency rows remain private execution reports. This result does not
execute RC01–14, a human study, native UI, private-device or real-provider gates.

### M6 real-sample results — stopped at RC03

The public production initializer, owner bootstrap/session, project registration
and complete imports created one Mulgae version and one Gaori version in two
projects. Every selected path and byte round-tripped through public file delivery:
Mulgae four files / 55,189 bytes; Gaori four files / 52,259 bytes. Both derived jobs
explicitly completed through public job reads before the first search. The driver
parsed the unchanged §9.3 table and froze its expectations, full source-file maps
and tree identities in a private receipt before importing or searching.

| Row | First relevant rank | Observed exact page / region | HTTP/CLI equality | Outcome |
| --- | --- | --- | --- | --- |
| `RC01` | 1 | recovery/inventory.html / M01 | Identical complete pages and ordered tuples | pass |
| `RC02` | 1 | recovery/inventory.html / M01 | Identical complete pages and ordered tuples | pass |
| `RC03` | 6 | recovery/inventory.html / M03 | Identical complete pages and ordered tuples | fail: required raw rank at most 5 |
| `RC04–RC14` | Not run | No observation | Not run | not_run after the required stop |

RC03 returned twelve eligible region hits. In exact raw order, all belong to the
single imported active/latest Mulgae document/version and have kind region and
null source identity:

| Raw rank | Exact page | DOM region |
| --- | --- | --- |
| 1 | recovery/compose.html | M09 |
| 2 | recovery/compose.html | main |
| 3 | recovery/compose.html | source-M-S08 |
| 4 | recovery/compose.html | source-M-S09 |
| 5 | recovery/inventory.html | M01 |
| 6 | recovery/inventory.html | M03 |
| 7 | recovery/inventory.html | inventory-role |
| 8 | recovery/inventory.html | main |
| 9 | recovery/inventory.html | source-M-S02 |
| 10 | recovery/inventory.html | source-M-S08 |
| 11 | recovery/inventory.html | source-M-S09 |
| 12 | recovery/inventory.html | source-M-S10 |

All twelve match only the body field: the current classifier assigns tier 4 and
score 1, then binary page/region ties put the five earlier entries before M03.
The fixed query and expected M03-or-M02 target were not changed. This is a
required relevance miss, not missing bytes, incomplete indexing, an HTTP/CLI
disagreement or an approved limitation. No post-hoc R-to-L change is made.
The first run obeyed the stop rule. The Supervisor subsequently approved A1:
per-page diversification within equal (tier, score), with an explicit cursor
ordering revision transition. The query, expected targets, strength tiers,
weights, filtering and all checked-in sample bytes remain unchanged.

### M6 A1 repair and complete rerun

The complete driver evaluates every row and independent check, retains all
actual ordered tuples and full HTTP/CLI pages, then lists every miss and exits
nonzero at the end. Frozen inputs are the complete checked-in sample trees:
eight files / 107,448 bytes. Ignored tool runtime artifacts are outside that
canonical corpus; no tracked sample file is omitted or modified. Each public
import has its own project and version; every file is downloaded and compared,
and each derived job explicitly reaches complete before search.

The before column below is the Supervisor's unchanged-code survey beyond the
original stopped run. Initial, cold-service restart and both warm repeats have
the same after ranks and identical HTTP/CLI pages. All fourteen required rows
pass; worst first-relevant rank is 4. Full ordered tuples remain in private
receipts, including the twelve RC03 hits with M03 now at rank 4.

| Row | Before A1 | After A1 | First relevant exact page / region |
| --- | --- | --- | --- |
| RC01 | 1 | 1 | recovery/inventory.html / M01 |
| RC02 | 1 | 1 | recovery/inventory.html / M01 |
| RC03 | 6 | 4 | recovery/inventory.html / M03 |
| RC04 | 1 | 1 | recovery/inventory.html / M05 |
| RC05 | 1 | 1 | recovery/compose.html / M07 |
| RC06 | 1 | 1 | recovery/compose.html / M08 |
| RC07 | 1 | 1 | recovery/compose.html / M11 |
| RC08 | 1 | 1 | recovery/compose.html / M12 |
| RC09 | 1 | 1 | gaori/outcomes.html / G02 |
| RC10 | 1 | 1 | gaori/outcomes.html / G05 |
| RC11 | 1 | 1 | gaori/lifecycle.html / G09 |
| RC12 | 1 | 1 | gaori/lifecycle.html / G10 |
| RC13 | 2 | 2 | gaori/lifecycle.html / G11 |
| RC14 | 1 | 1 | gaori/lifecycle.html / G12 |

Cross-project RC02/08/12 without project or kind filters remain ranks 2/1/1.
All four exact HTML page-path queries and `assets/recovery.js` as an attachment
remain rank 1. A separate seeded import adds a logical Git source and its
repo-id query with kind=source is rank 1. It uses compact metadata through the
existing public import path; this is an imported identity check, not source
verification. The two worksheet versions remain untouched.

Explicit `manual_sc19` variants import the same trees through the production
import service/worker. Existing test-owned lifecycle and scoped-credential
writes seed four canary copies: F10 denied-project and trash copies of each
sample. Owner default active/latest in its named project and an exact anchored
scoped reader repeat all fourteen queries plus source with page size 2. Items,
coverage, counts and cursors remain equal to their canary-free baseline; no
canary marker, identity, snippet or decoded cursor payload appears. A denied
reader is rejected. Positive controls prove both canary kinds were indexed
before hiding; replay/live event filtering masks protected version identities
and bodies. The owner can legitimately read other projects; the owner F10 check
therefore uses the predeclared named-project selection, not a false owner denial.
Issuance and trash lifecycle are seeded, not new production workflows.
The full fifteen-query matrix is excluded from canonical layers and is required
by `tests/manual/check_search_corpus.py`. A separate lean ordered persistence
pair shares one setup and two original imports, reuses the Gaori sample as F10
then trash, and retains partial/reopen, owner/scoped/denied, non-null cursor,
count/snippet and event discrimination. Explicit four-file lists remove the
persistence spec's Git-state dependency. No assertion in the exhaustive matrix
or temporary-copy SC19 reversals is removed.

Deterministic worker turns stop each sample after exactly one of its two HTML
pages. No-match items remain empty while coverage is partial with one pending
page; reopening the store and finishing makes it complete with zero pending.
The seeded mechanism adds no product hook or timing race.

| Worksheet phase | Queries | HTTP p50 / p95 / max, ms |
| --- | --- | --- |
| initial | 14 | 12.462 / 25.350 / 25.350 |
| cold-service | 14 | 13.532 / 19.260 / 19.260 |
| warm-1 | 14 | 11.455 / 19.264 / 19.264 |
| warm-2 | 14 | 12.345 / 21.586 / 21.586 |

Cold means a daemon restart, with no OS cache-flush claim. Latency is a host
observation, not an SLO. The actual Chrome and pinned Wails App each search and
open Mulgae M01 at `recovery/inventory.html` and Gaori G10 at
`gaori/lifecycle.html`, asserting returned and actual rendered document/version/
page identities. Screenshots alone do not satisfy the oracle.

SC19 controls on temporary copies remove M03, substitute a genuinely newer
version, and substitute a region's cross-page identity. Every substituted
oracle fails, the positive identity succeeds, and path/byte maps restore exactly.
A1's seven named temporary reversals independently fail alternation/traversal,
strict ordinal decoding, ordering-revision binding, strength precedence,
per-block reset, exact-version grouping and the discriminating hidden-before-
visible same-path case; all source maps restore. No existing assertion of the
old cross-page tie order required editing.

After A1, X09 remains 18/18, including both declared unsupported negatives;
calibration40 remains 40/40 with zero returned topical false positives.
Per-row changes: Q01–Q40 each 1→1 (no changed row). The fixed SC01
component-over-substring pair and rejected 35/40 admission control pass.
Independent review and the Supervisor's final canonical gate remain pending.

**owner use unmeasured**: no Master-authored queries were added. Search success
neither validates claims nor makes the Gaori→Mulgae analogy a dependency.
