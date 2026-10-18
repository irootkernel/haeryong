# TASK-003-04 evidence presentation

**Task:** [TASK-003-04](../tasks/EPIC-003/TASK-003-04.md) — status `done`.
This record states the fixed evidence-presentation contract and its reproducible
checks; results below come from the frozen implementation candidate. The dedicated Task Judge and Epic Judge approved the current result; two
non-author Worker reviews were resolved and Main completed the final project
and product review under Master's delegated authority. Overall Epic acceptance
remains pending.
**Scope:** SourcePanel and VersionHeader in the actual shared Reader, the shared
presentation query adapter (SourceQueries plus the pure derivations in
`evidence-view`), and the typed browser/native/Go evidence query operations
including the supplied-metadata `{etag, stream}` bridge. Presentation only: no
source reader, verification action, assessment backend or other new
source/backend functionality. Supplied and seeded records qualify presentation
only, never actual verification or semantic production. TASK-003-01/02/03
behavior is preserved, with bounded shared corrections carried by this
Task: the native stream opens (package, file and supplied metadata) now cancel
their own late-arriving handle when the client era was replaced, and one
render-session test fixture holds an exact known operation key instead of a
key suffix. The Reader also cancels its already-open metadata stream when its
version/digest effect is replaced or unmounted, including a body waiting for its
next chunk or EOF.

## Fixed presentation contract

- **VersionHeader** renders the exact version sequence, nullable base, current page
  path and `source_basis` (`evidence_recorded` is evidence recorded, not a
  correctness guarantee), payload/manifest/supplied-metadata digests, and each
  supplied source identity (git `repo_id`/`object_format`/`exact_revision` or
  `snapshot_sha256`, optional `release_label`, `retention_mode`). Source identity
  disclosures expose the full value, including all 64 snapshot digest characters.
  Detail is matched to the current version at render/use time and stored before
  document/project lookups, so a same-document version switch followed by a
  detail failure cannot reuse the preceding version's sequence or metadata
  validator. Existing document-bound import state keeps its mounted form,
  captured base and unsaved input. Absent supplied metadata renders
  unknown/unverified and never prevents opening or reading the
  result; the pre-existing version ID display stays untouched.
- **Supplied metadata load** is version-scoped in the Reader: exact bytes
  accumulate to a clean EOF and are compared against every supplied validator —
  the stream's strong `"sha256-<digest>"` ETag plus the version's
  `supplied_metadata_sha256` when known — before anything parses. 404
  `metadata_not_supplied` is an honest absent state; malformed bytes, a failed
  comparison or no usable validator at all is an independent metadata error with
  retry, never a Reader failure and never a silent unverified parse. Metadata
  state stays separate from Reader selection, import and session state.
- **SourcePanel** binds optional page-local regions (identity is `page_path` plus
  region id; the same region name on another page never leaks in) to stored
  evidence records; unresolved evidence ids stay visibly unresolved, region-less
  pages keep page/version context, and each authored `capture_status` is labeled
  as an author declaration. Author-chosen local IDs are arbitrary nonempty
  Unicode strings preserved exactly — inherited object-key names such as
  `constructor` must read as absent, never as already-true, and snapshot
  dictionaries never inherit another item's phase. Expanding a row loads
  the service evidence view — authored/source records beside the current service
  truth (`availability` `unavailable` with reason `source_read_not_implemented`,
  `bytes_origin` null, separate nullable `service_verification`) — and can
  enumerate that evidence's verification history.
  A separate version-wide list exposes every supplied evidence record, including
  unbound records when all pages have regions and when no page is selected;
  these records are never implicitly assigned to the current region.
- **SourceQueries** drives every evidence/assessment query for one exact
  selection. Assessment history is enumerated across cursor pages; each page may
  carry the typed `applicable` freshness member, which is retained across
  later-page failures and never inferred from history items. A later-page
  failure keeps the accumulated rows and the exact failed cursor (`resumeFrom`);
  retry reissues from that cursor under the same captured version/page/region
  and query identity, and order-reversed replies never merge. Coverage units
  are followed only from validated `units_url` data — the request is rebuilt
  from typed fields when it names the exact fixed local route for the same
  version and coverage set; foreign or protocol-relative references are
  rejected, and an absolute URL is accepted only on the current service origin.
  Coverage and verification retries stay reachable. 503 `FEATURE_NOT_READY`
  renders an explicit unavailable phase instead of derived content.
- **Selection guarding:** every version/page/region change — and a same-page
  history/render/reload context change — clears and rebinds the enumeration, so
  a late success, error or retry from an earlier selection (including a
  same-name region on another page) can never label the current one. Region
  selection events carry the channel's captured version/page tuple and are
  accepted only while that tuple still matches the current selection.
- **First-commit identity (TASK-003-04 R9/R10):** the Reader's supplied-metadata
  state is stored with the version it was captured for and render-guarded — a
  same-document version switch renders an honest loading state on its FIRST
  commit, never the previous version's ready/error metadata under the new
  identity. A stored region selection carries its exact
  `[version,page,host-context,reload]` ownership and renders as unselected on
  any mismatch, so a sidebar navigation to a page that declares the same
  region name starts neither a selected-region display nor a region-scoped
  first query; the page fallback uses only a selection of the current version
  and host context, and the panel's context key derives from the CURRENT route
  context. The exported SourcePanel carries a keyed inner selection boundary
  (`JSON.stringify([versionId,pagePath,regionId,contextKey])`), giving every
  caller a fresh snapshot, disclosure state and SourceQueries instance on the
  first commit of a new selection; all panel-local state lives inside that
  boundary and nothing outside the panel resets. `SourceQueries.dispose()`
  also invalidates its pending guard generation, so a cursor page released
  after the keyed unmount never merges, emits or requests its follow-up
  cursor, while new independent instances work normally. These are verified
  by the labelled first-commit harness oracles (a scripted-client fixture for
  metadata/query identity and a REAL production-App caller fixture driven
  through actual sidebar navigation) — presentation proofs, never native or
  source verification.
- **Coverage derivation:** counts and status derive only from the fully
  enumerated fixed required-unit records, and the enumerated tuples must match
  the coverage set's selected `unit_kind` and its scope's tuple structure — a
  version set is the distinct HTML page units or one whole-version unit, a page
  set is its single page unit or an explicit distinct region set of that page,
  a region set is that one exact tuple. An omitted selection scope means
  version scope for both unit admission and status derivation; region-only
  units cannot align that default selection. `partial`, `blocked` and `not_checked`
  units stay visible with reasons and never leave the denominator;
  contributions recorded against another captured target or outside the
  selected scope stay visible but never count as assessed; and a set violating
  the fixed nonoverlapping shape — duplicate tuples across cursors, impossible
  null/path combinations, mixed unit kinds, a homogeneous set of another
  recorded unit kind, or a page unit overlapping its own region units — remains
  inspectable as an aggregation problem with no silent deduplication. While any
  cursor remains or a page failed, counts/status stay unknown with no
  denominator claim, and zero/unknown eligibility is never aligned. Membership
  comes only from the enumerated service set, never from DOM structure or
  partially loaded file lists.
- **Status derivation:** an evidenced discrepancy outranks everything; an
  outstanding recorded `reassessment_required` outranks blocked units; a
  recorded overlay `unable_to_verify` (or any blocked unit) keeps verification
  unavailable even when every unit is assessed — completed coverage plus
  verification unavailable, never derived aligned. Alignment additionally
  requires an explicitly positive applicable assessment (`status`
  `aligned_within_scope`) with an actually recorded exact target
  (`target_set_id`/`captured_at` non-null and matching the selection): capture
  ID and time are recorded facts, not current source availability, so an
  unknown or `not_assessed` view cannot become aligned from them alone, while
  a recorded aligned historical assessment for the same target/scope may
  remain visible independent of current access. An absent or unknown overlay
  stays unknown (`not_assessed`). The recorded overlay badge is displayed but
  never trusted; a conflict with the unit-derived result is surfaced, not
  promoted.

## Truthfulness boundary

- Authored declarations are immutable; service verification, verification
  history and assessments are separate records. Presentation never upgrades
  `capture_status`, rewrites provenance or turns an author claim into a service
  result. `observed_input` and `posthoc_verified` remain distinct: `captured`
  records exact-target resolution without expectations only, `matched` records
  a supplied-expectation comparison, and neither claims original provider
  input, author-byte agreement or claim correctness. An evidence pointer is
  not correctness proof.
- The **Check source evidence** action is capability-disabled until
  TASK-004-01 supplies the real authorized source reader and verification
  operation; this Task cannot pass that later implementation with a fixture.

## Current service boundary

Browser item queries use the strict single-item query alias
`GET /versions/{id}/source-evidence?evidence_id=...`, which reuses the same
authentication, version visibility, evidence read and post-read guard as the
legacy path. Exactly one nonempty ID is decoded once; `.`, `..`, literal `%2e`,
slash and Unicode IDs cannot be normalized into another resource. The native
item operation preserves its existing escaped path. Both history operations and
all returned `verifications_url` values use the fixed
`/versions/{id}/source-evidence-verifications?evidence_id=...` locator. It is
currently only an authenticated GET future-capability 503 (retryable false,
no Retry-After); POST and HEAD remain 404. The legacy future-history matcher
does not match IDs containing a decoded `/`; current SDKs and locators avoid
that limitation through the query alias. This is a transport correction, not
a new source reader or verification backend.

Implemented today: `GET /versions/{id}/metadata?kind=supplied` (exact bytes
plus strong ETag) and `GET /versions/{id}/source-evidence/{evidence_id}`
(authored view, no fresh source read). Verification history, `assessments` and
`assessments/coverage` remain known future capabilities returning 503
`FEATURE_NOT_READY`, which the clients render as honest unavailable states.

## Reproducible checks

| Check | Command | Result |
| --- | --- | --- |
| Frontend typecheck | `cd frontend && npx tsc --noEmit` | pass through both canonical build checks |
| Frontend suites incl. `evidence-view`/`source-queries` regressions | `cd frontend && npm run test` | pass through both canonical build checks |
| Committed dist regeneration | `python3 -B frontend/check_build.py --regenerate` | pass |
| Bundle/drift normal verification | `python3 -B frontend/check_build.py` | pass |
| Browser evidence regression (real daemon + Chrome) | `python3 -B internal/adapters/http/auth/browser/check_reader_evidence.py --browser-cli /absolute/path/to/playwright-cli --receipt <private-scratch>/reader-evidence.json [--screenshot <private-scratch>/panel.png]` | pass, 55 assertions (42 base + 7 first-commit scripted + 6 first-commit REAL caller); browser and daemon closed |
| Native Reader evidence stage (macOS beta.28 harness) | `python3 -B spikes/viewer/check_reader_native.py --stages app,evidence --receipt <private-scratch>/reader-native.json` | pass, current app/evidence subset (3 cases); not full qualification |
| Native desktop module race suites | `go -C spikes/viewer/desktop test -race -count=1 -timeout 240s .` | pass; 23 existing SDK linker warnings |
| Canonical root gate | `make test` | pass, all serial layers including 178 persistence cases and 46 Python E2E cases |
| Documentation integrity | `python3 -B docs/testing/validate_docs.py` (Gaori `docs-integrity` wraps it) | pass |

The browser regression builds the tagged test-state daemon, pairs a disposable
owner and imports a plain directory plus a same-shape directory with exact
supplied metadata (an inherited-key evidence id and the same region name on
two pages); it exercises keyboard and emulated-touch activation of the changed
controls, absent/supplied metadata, authored/service separation, honest 503
future capabilities and the disabled source-check, and a labelled local seeded
fixture backend for assessment/verification/coverage presentation including
later-cursor failure and retry. The native check exercises the closed Go
facade and shared App through the existing pinned harness; its handler
invocation is programmatic, not a physical-user observation. Receipt paths are
portable placeholders; private run logs and receipts stay in the authoring
session's scratch and are not tracked evidence.

The real-daemon browser phase also imports distinct `.`, `..`, literal `%2e`,
slash/Unicode and `verifications` evidence IDs, checks actual HTTP response
identities and rendered authored paths, and opens their unavailable histories.
Keyboard and emulated touch operate these rows and the full source-identity
disclosure. The seeded phase switches versions inside one live Reader while a
document lookup fails, verifies the new metadata and usable JavaScript, and
checks that the same import DOM, captured base and unsaved text survive both
failure and healed retry. A separate same-client-era transition parks the old
metadata body after its first byte: without another byte or EOF, the browser
closes that connection on version change while the replacement metadata and
page JavaScript remain usable. A directory-listing selection with no active page
still exposes its unbound version evidence. Separate root HTTP race regressions
verify alias/legacy data equality, HEAD, malformed/duplicate/empty query errors,
authentication, scoped version visibility and the history alias's real
non-retryable future gate.

The first-commit phases extend the same driver. A labelled scripted-client
harness (private scratch build served under `/harness`, selected only by the
`?mode=committest` parameter of the existing preselection harness page) mounts
the actual ReaderScreen/SourcePanel under a parent CommitProbe — a public
`useLayoutEffect` that snapshots the DOM on the FIRST commit of a new prop
selection, strictly before any passive reset effect — and proves with distinct
metadata-provenance and service-query markers that a same-document version
switch, page switch, region switch and context switch each render without the
previous selection's content. A labelled REAL caller harness (`?mode=real`)
mounts the actual production App with a real HTTP client and credential store
at the fixture origin and drives the actual sidebar navigation: after selecting
the same-named region on the first page, the new page's first commit shows no
selected region and its first assessment query is page-scoped without the old
region; a page-less same-document version route renders page-less on its first
commit and its first query is version-scoped for the new version. The scripted
fixture proves commit-time identity with deterministic markers; the REAL
fixture proves caller identity through actual production navigation; both are
labelled presentation proofs and neither is native or source verification.

## Verification status and limits

The root gate passed all selected layers: Go unit 71, integration 13 and
persistence 178; Python unit 10, integration 40 and E2E 46, with no failures,
errors or skipped cases. Both canonical frontend checks include typecheck,
235 tests and compiled-asset verification. No interim development diagnostic
is cited as a gate result.

- Presentation only: seeded/supplied assessment, verification and coverage
  records prove presentation, not actual verification, source observation or
  semantic production. Those remain TASK-004-01 and EPIC-008; TASK-003-05,
  TASK-003-06 and EPIC-007 stay downstream.
- The browser check qualifies the loopback real daemon plus Chrome (Playwright
  CLI) only — no private ingress, second device or provider proof;
  keyboard/touch cover the changed evidence controls, not the full TASK-003-06
  accessibility matrix.
- The current native check runs the existing macOS beta.28 harness with
  `app,evidence` selected: three cases, `full_qualification=false`. It exercises
  the current compiled shared App, protected renderer/context and evidence
  presentation. An earlier six-stage run supports unchanged guard/lifecycle
  behavior only; it is not a full qualification of the current candidate. The
  driver starts the daemon; companion-started discovery and service lifetime
  remain TASK-003-05. No cross-client or production Wails release qualification
  is claimed.
- Independent Task Judge and two non-author Worker result reviews passed;
  the Epic Judge then approved this Task in its Epic context. Main subsequently
  accepted the result after the final project/product review under delegated
  authority. This does not establish overall Epic acceptance.
