# Multi-device reconciliation and conflict UI (TASK-007-04)

**Task:** [TASK-007-04](../tasks/EPIC-007/TASK-007-04.md)
**Implementation status:** done for the owner-amended bounded web member.
**Evidence status:** pass by current direct and qualified preserved composition. The corrected frozen product passed its complete common. Original first/affected negative reports and failed executions remain immutable. The same original roles confirmed current bounded criteria with zero due gaps; Coordinator delegated acceptance is complete. Normal completion commit/native finalization remain separate effects.
**Source revision:** TASK-007-04 implementation on schema revision 19. The complete common covers the frozen product before the separately verified factual/lifecycle and AST-identical EOF-format closeout overlay.
**Environment/toolchain:** macOS arm64, Go 1.27.1, Python 3.14.7, Node 26.7.0.
**Implementer / independent reviewer:** AGY Argon/high Task Operator and Coordinator corrections; Opus 5.5/high Task Evaluator, two nonauthoring Argon/high Operators and the continuous GPT 6.1 Sol/high Epic Evaluator. The approved v4 design and all original verdicts remain preserved. Final common-evidence confirmations pass the current member scope; the Epic Evaluator retains its separate global evidence-needed qualification.

## Actual outcome

The shared browser and native client, HTTP/auth adapters, and desktop `readerfacade` implement multi-device reconciliation, passive background refresh isolation, cursor-resumable SSE/native invalidation with an awaitable full-state resync barrier, receipt-first recovery separated from current resource state, and conflict/unsaved comparison UI across personal records. No SQLite migration or new persistent database table is introduced (schema revision remains 19). These boundaries preserve current [personal-tool](../changes/2026-10-10-personal-tool-revision.md) and [directory-results](../changes/2026-10-10-directory-results-revision.md) applicability.

### 1. Passive background read isolation (`Haeryong-Passive-Read: 1` and native `passive: true`)

- Background invalidation, `resync.required` barrier sweeps, `visibilitychange` rechecks, and background job/upload status polls use `client.passive()`, which attaches `Haeryong-Passive-Read: 1` on `GET`/`HEAD` requests (or `passive: true` on closed read-only native operations in `desktop/readerfacade`).
- The HTTP adapters (`internal/adapters/http/auth`, `internal/adapters/http/v1`, `internal/adapters/http/imports`) validate `Haeryong-Passive-Read` before session authentication: only `GET` and `HEAD` accept a single exact `"1"` header value; any other value, multiple header values, or any mutating method (`POST`, `PUT`, `PATCH`, `DELETE`) is rejected with `400 INVALID_REQUEST` before touching session state.
- Valid passive reads propagate `auth.WithPassiveRead(ctx)` so `Authorize`, `Authenticate`, `Operation`, `Job`, `Jobs`, `ListSessions`, `PendingPairings`, `Providers`, `Health`, and `Uploads` enforce revocation, `auth_epoch`, idle expiry, and absolute expiry without advancing `last_seen_at`. Interactive owner actions omit the header and continue to update `last_seen_at`.

### 2. Cursor-resumable SSE/native invalidation and awaitable full-state resync barrier

- Browser SSE (`frontend/src/client/search-events.ts`) tracks the last committed `stream_epoch:sequence` cursor and sends `Last-Event-ID` on same-authority reconnect. It parses complete blank-line-terminated SSE frames (`id`, `event`, `data`), treats any non-empty domain event type (including `review.changed`, `revisit.changed`, `reading_position.changed`, `page_position.changed`, `reader_preference.changed`, `annotation.changed`, `question.changed`, `upload.updated`, `project.updated`, `draft.changed`, `assessment.published`, `source.*`, `policy.changed`, `job.*`, `document.*`, and `*.invalidated`) as an invalidation hint, and commits the frame's `id` cursor only after the full frame finishes and the awaited `onInvalidate` or `onResync` callback succeeds. A disconnect or failed callback before completion leaves the prior cursor intact so the event is replayed or resynced on reconnect.
- Closed native event operations (`subscribeEvents` and `eventsPoll` in `desktop/readerfacade/reader.go` and `frontend/src/client/native-wire.ts`) accept an optional `cursor`. When `subscribeEvents` is opened without a cursor, the Go facade performs a passive no-cursor bootstrap against `/api/v1/events`, captures `resync.required` (`reason: "bootstrap"`, `stream_epoch`, `head`, `resume_cursor`), and returns `resync` and `cursor` alongside `subscription_id`. `native-wire.ts` defers bootstrap delivery via a `listenerReady` gate until the outer `subscribeSearchEvents` listener is attached, preventing lost commits between initial subscription and first poll.
- `ShellController` (`frontend/src/client/controller.ts`) coordinates a single-flight `#runResyncBarrier` on `resync.required` or `stream_epoch` rotation: it binds each barrier and retry timer to a private `#resyncLifetime` retired on any authority/credential/library/runtime-generation replacement or `stop()`, runs a passive `recheck(true)` first, increments `readGeneration` after the barrier's authority check so pre-barrier in-flight reads cannot overwrite post-barrier state (without double-triggering when `recheck` already rotated the epoch), and awaits all registered resync sweep handlers. A new authority bootstrap never joins a retired barrier, and a retired or stopped barrier rejects without clearing the new authority's `#pendingResync`, scheduling a late retry, or reopening `#events`. If `recheck` cannot confirm healthy authority (the current authority is not verified) or any sweep handler fails under the active lifetime, `#pendingResync` remains set and a bounded retry timer re-attempts the sweep rather than dropping the resync or advancing the cursor past `H`.
- `useReviewInvalidation` (`frontend/src/app/screens/review-refresh.ts`) awaits `waitIdle` when `busy()` is true during `ev.resync` so the barrier does not resolve before the screen's post-busy passive refresh completes. Mounted `GenerationControl` (`frontend/src/app/screens/generation-control.tsx`) and `DraftControl` (`frontend/src/app/screens/generation-drafts.tsx`) subscribe through `useReviewInvalidation` and `refreshGenerationMountedState` / `refreshDraftMountedState` (`frontend/src/app/screens/generation-context.ts`) to passively refresh configuration defaults, project documents/sources, active saved job state, draft list, selected draft, and opened draft history while preserving local form inputs, source/provider/revision overrides, selected replacement inputs/native handles, and journaled generation/resume/draft-action recovery records.

### 3. Receipt-first recovery separated from current resource state

- In-memory and journaled operation controllers (`PersonalOperations` in `frontend/src/app/screens/personal-operations.ts`, `ReviewOperations` in `frontend/src/app/screens/review-operations.ts`, Notes in `frontend/src/app/screens/notes-panel.tsx`, and Generation/Draft/Ask/Configuration recovery) persist `operation_id` and `runtime_generation` before dispatch.
- After an uncertain response (network error, `503 SERVICE_RECOVERING`, or post-commit `401` on security policy commit), recovery queries `GET /api/v1/operations/{id}` first instead of blindly re-dispatching the mutation:
  - If the receipt is `completed`, the client never treats the historical receipt snapshot as the current resource version. Instead, it performs a fresh authorized `GET` on the target resource (`review`, `revisit`, `reader-preference`, `reading-position`, `page-position`, `annotations`, `drafts`, `questions`, or `jobs`) and adopts the current server resource `ETag` and state.
  - In `PersonalOperations`, if the user queued a newer local edit `B` while operation `K` was uncertain, `#reconcileRecoveredCompletion` compares `K`'s historical receipt metadata against the fresh server `GET` result `C`: if no peer modified the resource after `K` (`C`'s ETag matches `K`'s receipt ETag), `B` continues cleanly against `C`'s confirmed validator; if a peer modified the resource after `K` (`C`'s ETag differs from `K`'s receipt), `B` is preserved as an explicit conflict (`state: "conflict"`) comparing the local queued value against `C`'s server state rather than silently overwriting `C`.
  - If the receipt returns `404 NOT_FOUND` under the **same** `runtime_generation`, `PersonalOperations` and `ReviewOperations` refresh the current resource state: if the resource moved ahead of the captured precondition, they surface an explicit conflict; if the precondition is still current, they retry with the **same** `operation_id` and `If-Match`/`If-None-Match` precondition. If `runtime_generation` changed (e.g., after restore), a `404` receipt never auto-resends; the client refreshes current server state and requires explicit owner action with a new key.

### 4. Independent personal reader resources and conflict/unsaved UI

- `reader-preference`, `reading-position` (the version's last-opened pointer), and each `page-position?page={path}` use independent `PersonalOperations` slots and their own server `ETag` / `If-None-Match: *` validators. Scrolling or saving `page-position` on page A never mutates `reading-position` or invalidates page B's `page-position` validator.
- Stale writes (`412 PRECONDITION_FAILED` / `409`) across Review (`review-control.tsx`), Revisit (`revisit-control.tsx`), Notes create/edit/reattach/delete (`notes-panel.tsx`), and personal reader preferences/positions (`reader.tsx`) retain the unsaved local draft or target value and display a side-by-side comparison of local unsaved intent vs current server state, offering explicit **서버 상태 불러오기** (reload server state) or **현재 서버 버전 기준으로 내 변경 다시 적용** (overwrite current server revision using a fresh `Idempotency-Key` and the latest server `ETag`).

## Case evidence

Focused Writer checks and the later Coordinator initial-transport receipt correction checks executed via the repository's Gaori verification runner against the current candidate source. The first Coordinator-owned private Tailscale HTTPS execution retained two passed stages and two failed stages, as qualified below. Subsequent selected executions passed personal conflicts, actual resumed SSE/passive idle, and the affected controlled Ask/Provider presentation. The original common passed before the result corrections; it is historical input-qualified evidence, not a corrected-candidate aggregate pass.

| Case | Fixture or exact source target | Expected observable | Command or procedure | Actual result | Evidence location |
| --- | --- | --- | --- | --- | --- |
| Focused Go persistence suites (`auth`, `v1`, `imports`, `owner-records`, `ai-workflows`) | `./internal/adapters/http/auth`, `./internal/adapters/http/v1`, `./internal/adapters/http/imports`, `./tests/integration/owner-records`, `./tests/integration/ai-workflows` | `Haeryong-Passive-Read: 1` validation (`400` on invalid value or mutating method, no `last_seen_at` bump on valid `GET`/`HEAD`); `OR07` independent personal ETags, `412` stale conflict retention, `resync.required` barrier, receipt-vs-current-state separation; `AIWF07P` lost-202 receipt recovery without duplicate `Execute` and durable job processing through a controlled coordinator, with receipt/job reconciliation from a second owner session. This direct-service case does not execute browser closure or session revocation. | Offline Go test with `-race` and `--label-filter=persistence` via Gaori (the corrected focused Go run). | `pass` (exit 0, 13.5s; all 5 packages passed). | `internal/adapters/http/auth/handler_test.go`, `internal/adapters/http/v1/handler_test.go`, `internal/adapters/http/imports/handler_test.go`, `tests/integration/owner-records/owner_records_test.go`, `tests/integration/ai-workflows/ai_workflows_test.go` |
| Separate desktop module (`readerfacade` and `cmd/companion`) | `desktop/readerfacade`, `desktop/cmd/companion` | Closed native `passive: true` forwards `Haeryong-Passive-Read: 1` only on read operations; `subscribeEvents` no-cursor bootstrap returns `resync` + `cursor`; `eventsPoll` forwards `stream_epoch`/`after` cursor and reports `invalidated`/`resync`/`cursor`; `companion` vets and compiles cleanly. | `cd desktop && go test -race -count=1 ./readerfacade && go vet ./readerfacade ./cmd/companion && go build ./cmd/companion` via Gaori (the corrected full desktop run). | `pass` (exit 0, 5.7s). | `desktop/readerfacade/reader.go`, `desktop/readerfacade/reader_test.go`, `desktop/readerfacade/search_test.go` |
| Focused Python E2E (`test_owner_records_multi_device_reconciliation_passive_idle_and_receipt_separation`) | `tests/e2e/test_owner_records.py` | Independent `reader-preference`, `reading-position`, and `page-position` validators across two pages; stale `If-Match` `412` rejection; historical `GET /api/v1/operations/{id}` receipt ETag remains distinct from subsequent peer `PUT` current resource ETag; `Haeryong-Passive-Read: 1` reads preserve `last_seen_at` and allow configured idle expiry (`401 SESSION_EXPIRED`). | `.venv/bin/python -B -m pytest -x --strict-config --strict-markers -W error --basetemp <private-path> --junitxml=<private-report> tests/e2e/test_owner_records.py -k test_owner_records_multi_device_reconciliation_passive_idle_and_receipt_separation` via Gaori (the corrected focused public E2E run). | `pass` (exit 0, 11.7s; 1 passed). | `tests/e2e/test_owner_records.py` |
| Frontend default verification and bundle reproducibility | `frontend/check_build.py`, `internal/webassets/dist`, `internal/webassets/dist/assets.manifest.json` | TypeScript typecheck, the registered Node tests (including SSE frame/cursor/resync, native bootstrap `listenerReady`, `#runResyncBarrier` lifetime/retry/cleanup, `useReviewInvalidation` busy-resync wait, mounted `GenerationControl`/`DraftControl` passive refresh and fencing, `PersonalOperations` receipt-first and queued-edit conflict preservation, and conflict UI), browser/native bundle separation, and exact committed `dist` manifest match. | `python3 -B frontend/check_build.py` via Gaori (the current frontend default verification). | `pass` (exit 0; all selected checks passed). | `frontend/src/client/search.test.ts`, `frontend/src/client/controller.test.ts`, `frontend/src/client/native.test.ts`, `frontend/src/client/generation-recovery.test.ts`, `frontend/src/app/screens/review-operations.test.ts`, `frontend/src/app/screens/notes-operations.test.ts`, `frontend/src/app/screens/personal-operations.test.ts` |
| Private HTTPS Tailscale browser cohort (`--task04-only`) | `internal/adapters/http/auth/browser/check_private_ingress.py`, `check_private_ingress.js` | Stages `personal-and-conflicts`, `receipt-and-lost-ack`, `durable-job-browser-close`, and `resync-and-passive-idle` over actual same-Mac Tailscale HTTPS with two independent browser contexts. | Coordinator-owned private Serve handshake + `check_private_ingress.py --task04-only`. | `pass` by qualified stage composition: original receipt recovery and browser-close job observations, corrected personal conflicts, then corrected actual SSE/passive-idle stage. Earlier whole cohorts remain failed; this is not a new full-cohort run. All selected sessions closed, daemon exited 0 and the full Serve baseline was restored. | `internal/adapters/http/auth/browser/check_private_ingress.py`, `internal/adapters/http/auth/browser/check_private_ingress.js` |
| Complete canonical gate (`make test KEEP_GOING=1`) | Root Makefile (`prepare`, `unit`, `integration`, `persistence`, `e2e`) | Single canonical full gate after all modifying work settles. | Coordinator-owned `make test KEEP_GOING=1`. | Original frozen-candidate pass remains earlier input-qualified history. Corrected frozen product `pass`: all 15 native command statuses zero, 1,378 unique Go It specs plus six suite setup/cleanup entries, and 253 Python tests. The raw Go intake contains 1,384 passed report entries; they are not 1,384 It specs. The later closeout overlay has separate affected verification. | [TESTING.md](../../TESTING.md) |

The first private cohort observed one committed Review write despite the lost response, receipt-based recovery of the original operation, a fresh peer state rather than its historical result, and first-call Revisit transport recovery with one PUT and a fresh current read. The durable import job was `queued` before both application contexts closed; a separate host HTTP observer saw completion and a retained sealed reusable upload before either app reopened. These are same-Mac private HTTPS observations with zero provider calls. They do not establish physical suspension or an AI request count; the offline controlled `AIWF07P` supplies that distinct Execute-count evidence.

The first cohort failed its personal-state wait because its expected notice differed from the real UI text. Its resync stage injected the exact single owned history-row trim but did not observe a new application subscription: the interception was registered after an established stream, which browser offline emulation did not reliably interrupt. The correction installs the held subscription before initial Library navigation, then preserves unsent input through actual same-epoch resumed SSE. This controlled history trim does not establish automatic retention scheduling. Browser closure, daemon exit 0 and full private Serve baseline restoration succeeded after the failed cohort. Only the two affected stages are selected for continuation; passing recovery/job observations are retained.

The Coordinator also corrected first-call Review, Revisit and Notes consumers because the shared transport can internally reconcile an uncertain response before returning. A successful historical DTO now leads to a fresh current resource read even without an explicit retry. Current frontend regeneration/default verification, browser driver syntax and companion build passed after these product corrections; the independent result assessments remain due.

### Result correction and current web proof

The original Task panel and Epic contribution assessment each returned rework.
The consolidated corrections retain those verdicts and original priorities:
Review queries its original receipt even across runtime generations before
allowing a new intent; missing same-generation receipts recheck the current
precondition. Required mounted refresh failures propagate to the barrier, a
mid-sweep generation change triggers another sweep, and losing verified
connectivity prevents completion. Initial/explicit and passive reads share
resource ordering so a late response cannot replace a newer snapshot. Local
supersession during mandatory refresh remains unacknowledged. Ordinary Library
hints coalesce and preserve the loaded page window.

A healthy initial Provider bootstrap preserves its entry status read; gap or
previously degraded resync clears stale readiness and requires explicit refresh,
with no automatic provider probe. Inspected saved Ask output survives ordinary
hints and is revalidated under current authority. Automatic Home/Review and
PurgeProgress reads are passive; known unfinished lifecycle receipts remain
unresolved without being confused with failed reads. Notes rejected-action
resolution holds its admission guard before its first read through submission.
Rejected awaited SSE callbacks abort/cancel the stream before replacement.

The corrected canonical frontend regeneration/default, driver syntax,
`go test -race -count=1 ./internal/webassets` and an untagged normal host build
passed. Two preceding correction runs failed TypeScript fixture/signature checks;
one then exposed a regression fixture attempting a resync callback while the
controller was still degraded. Correcting its explicit healthy recheck preserved
the original failure assertion. These failures were not product gate passes.

The selected personal stage passed independent page positions, actual scrolling
with unrelated validators unchanged, Personal/Review/Revisit conflicts, Notes
edit/reattach/delete conflicts, and preservation of unsaved text. The selected
resync stage passed the application's actual open-SSE first `trimmed_cursor`
control, full project refresh, preserved unsent search, passive headers and
unchanged passive `last_seen_at`, actual idle `401 SESSION_EXPIRED`, credential
clearing and protected Library removal. The fault removed exactly one owned
history row after a newer head existed; it does not prove automatic retention.

Affected Task03 `controlled-ask,provider-maintenance` passed on the corrected
shared frontend: real body/context preparation and full controlled saved answer,
context and citation streams; readiness removal during controlled outage; no
automatic probe on disconnected entry/reconnect; one explicit controlled refresh;
and honest unavailable-provider/feature-absence UI. The actual provider binary
was deliberately nonexistent. These browser peers made zero native/model calls
and do not repeat the separately authorized Task03 real Ask.

Private UI executions used `haeryong_test_state` only to isolate fixture state.
Normal untagged host build and canonical backend results are separate evidence.
Same-Mac private browser contexts establish no physical second-device, mobile,
OS suspension or Wails GUI qualification.

### Subsequent affected assessment and bounded correction

The first affected confirmation did not establish completion: the Task Evaluator
and Epic Evaluator each returned rework, while the two Operators returned
evidence-needed with no new product finding. The Coordinator retained the
concrete evaluator paths over the Operators' broad product-complete statements.
The same approved design then received five bounded corrections:

- The mounted invalidation event carries the existing resync reason. Healthy
  initial bootstrap preserves the entry Provider read, while gap or degraded
  recovery still requires manual recheck with no passive probe. A failed direct
  entry execution exposed the HTTP adapter's fabricated zero-epoch cursor:
  without a committed cursor it now omits Last-Event-ID and uses the existing
  server bootstrap contract. Committed resume cursors and acknowledgement after
  complete frame/callback success are unchanged.
- Reader requests page-position only for the current canonical HTML page,
  confirmed by its bound channel or matching bootstrap selection. Requested
  missing/non-HTML paths keep page/file recovery, without being confused with
  whole-document loss. Current child navigation takes precedence over the
  initial selected page. Version/document authority loss retains its closure.
- Links sets the existing current reference/backlink issue on refresh failure
  before rejecting mandatory completion, and clears it after success.
- An explicit global read-generation bump schedules a coalesced passive
  successor through the existing invalidation channel when no barrier is
  active. During a barrier its existing generation-change loop supplies the
  successor. A sibling initial read cannot remain retired without refresh merely
  because a rejected action emitted no server event.
- Required personal refresh rejects busy, pending or superseded skips. It keeps
  the barrier unconfirmed and the exact original intent until actual settlement;
  ordinary optional refresh retains its null result. Held-write and unknown
  receipt regressions prove no duplicate PUT, unchanged pending identity, and
  fresh peer state after settlement.

The later canonical frontend/default, syntax, embedded-assets race and untagged
host build passed after these corrections. The intermediate frontend pass before
the canonical-page adjustment and the next pass before no-cursor correction keep
their own inputs. Selected actual private `provider-maintenance` then passed
direct load and reload with one initial status read each, outage clearing,
disconnected entry without a probe, explicit manual recovery and unavailable
provider/feature presentation, with zero native/model calls. The failed direct
entry attempt remains failed; the earlier SPA-only observation did not cover it.

Selected actual private `reader-page-recovery` passed a missing HTML URL (real
bootstrap404), an ordinary Markdown file URL (real bootstrap200), retained
readable document and passive pointer/detail refresh, zero invalid page-position
requests, and same-context child navigation refreshing the second page's actual
position. Browser contexts closed, daemon exited0 and the full Serve baseline
was restored after both selected runs. Existing personal/receipt/job, trimmed
cursor/passive-idle and controlled Ask proof retain their bounded unaffected
scope; they are not reclassified as new executions.

The second affected panel confirmed H1–H5. The Task Evaluator identified one
additional Low presentation defect: a required personal skip correctly rejected
the barrier but also displayed a false document-read failure. Reader now leaves
that stale-response signal out of the detail alert while continuing to reject
the sweep and preserve the original pending banner. Current canonical frontend,
embedded-assets race and normal host build passed. The selected actual private
`reader-pending-resync` stage held one personal PUT through a real-server epoch
cursor fault and two passive detail sweeps: the pending state remained visible,
no false detail alert appeared, and the original write committed once after
release. Browser closure, daemon exit 0 and full Serve restoration passed, with
zero native/model calls.

On HTTP, a cursorless initial connection receives `connected` and retained
replay rather than a resync frame. Native no-cursor bootstrap supplies the
existing resync reason; its healthy-bootstrap branch has deterministic proof,
not a Wails GUI observation. The direct-entry HTTP proof and native qualification
remain distinct.

The original Task Evaluator confirmed the finite presentation correction. The
final frozen-candidate common then passed all selected layers: 179 unit, 98
integration and 1,101 persistence It specs, plus six suite setup/cleanup entries;
96 unit, 64 integration and 93 E2E Python tests. All 15 native command statuses
were zero without a command watchdog timeout. Exact inventory bytes/modes,
deletions, HEAD and the empty index matched the frozen start after native
transient-binary cleanup. No separate Task04 mandatory-tuple list is invented;
the complete TESTING.md layer selection passed without duplicated It specs.

The same four original nonauthoring roles confirmed current applicability and
all three bounded original parts with zero due unmet/unverified counts. This
short final continuation consumed the new common evidence; it did not restart
the plan or whole product review. Original rework and evidence-needed reports
stay readable, including the separate global future qualifications. Coordinator
product review and strict current composition support exact delegated member
acceptance; no completion commit or whole Stage A follows merely from a test.

### Retained authoring and diagnostic failures

All earlier diagnostic and focused-check failures remain retained in local workflow state and are not relabelled as passes:
- Initial direct `./node_modules/.bin/tsc --noEmit` / `node --test` and `frontend/check_build.py --regenerate` repair diagnostics during transport/controller/screen integration caught intermediate TypeScript signature mismatches and test mock expectations before the final clean regeneration and default verification (the current frontend default verification).
- One pending Gaori Go command was declined before execution so offline Go environment variables (`GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, `GOFLAGS=-mod=readonly`), task-owned physical `TMPDIR`, and `--parser generic` could be bound explicitly.
- Three focused Go persistence runs failed during test authoring before the corrected focused Go run passed: the first focused Go run (the new `auth` test called always-passive `/api/v1/session` instead of a touching route `/api/v1/status`, and `imports/handler_test.go` unpacked 3 return values from `Authenticate`), the second focused Go run (`imports` test passed `"directory", ""` to single-entry `f.upload` helper instead of `"file", "selected.html"`, and `owner_records_test.go` used wrong `PersonalPut` argument order), and the third focused Go run (`AIWF07P` hit `429 RATE_LIMITED` by creating a pairing inside the subcase loop from the same `"test"` client address as `bootstrap()`; fixed by creating the peer session once before the loop with `"test-peer"`).
- Two desktop `readerfacade` runs failed before the corrected full desktop run passed: the first focused desktop run (the new test used `"kind"` instead of `"type"` in poll items and checked `Last-Event-ID` instead of `stream_epoch`/`after` query parameters) and the first full desktop run (existing `TestSearchEventHintsFollowAuthorizedCursorWithoutResourceBodies` in `desktop/readerfacade/search_test.go` expected `eventsPoll` without `"cursor": "epoch:2"`, updated to match the cursor-continuity shape).
- One focused Python E2E run (the initial focused public E2E run) failed before the corrected focused public E2E run passed because the test read `polled["session_token"]` instead of `product.owner("peer-device")` and top-level `etag` instead of `op_view["result_metadata"]["etag"]`.
- Coordinator readback after the initial Writer handoff identified that `GenerationControl` and `DraftControl` lacked `useReviewInvalidation` mounted passive refresh and that `ShellController.#runResyncBarrier` needed explicit resync-lifetime retirement across authority replacement and `stop()`. During the continuation, one focused `controller.test.ts` Gaori run reproduced the two pre-fix resync-lifetime failures (`2` failures) before `controller.ts` was updated, one `check_build.py --regenerate` Gaori run failed `tsc --noEmit` on an unused helper import in `generation-recovery.test.ts`, and one `check_build.py --regenerate` Gaori run caught a missing `#closeEvents()` call during credential rebinding / library change in `controller.ts` before the final regeneration, default verification, and companion build passed.

Additional private failures remain explicit: a peer Notes injection omitted
required nullable content fields; the next continuation waited for EOF on an
intentionally open SSE; the finite first-control observer repaired that harness.
Two later resync/idle continuations failed fixture-administrator recovery. The
admin needed activity before shortening idle policy, and the expiry loop waited
for `.service-state` after the actual unauthenticated shell had removed it,
pausing the keeper until it expired. The corrected loop reads the actual cleared
credential and ConnectScreen expired notice without an absent-element wait,
retains the separate admin, verifies actual owner 401 and restores original policy.
Browser/daemon cleanup and full Serve restoration passed after each failure.
Passing stages were reused; only the affected resync stage was repeated.

### Original assertion and case applicability

| ID | Meaning | Current contribution and qualification limit |
| --- | --- | --- |
| `T-007-04-01` | Stale note/review write cannot silently overwrite a peer update; independent `reader-preference`, `reading-position`, and per-page `page-position` revisions; visible conflict comparison/resolution. | Focused Go persistence (`OR07`), Python E2E, and the registered Node tests passed; the actual corrected private `personal-and-conflicts` stage passed. Same-Mac Tailscale browser contexts do not prove physical second-device/mobile hardware. |
| `T-007-04-02` | Suspending/closing browsers does not cancel durable jobs. | Focused Go persistence (`AIWF07P`) and Node tests passed; the original actual private `durable-job-browser-close` stage passed with queued-to-completed observation while both app contexts were closed. This unchanged job/receipt path is retained through the frontend correction. |
| `T-007-04-03` | Lost acknowledgement does not trigger a duplicate AI request or false Saved. | Focused Go persistence (`AIWF07P`, `OR07`), Python E2E receipt-vs-resource separation, and Node tests passed; the original actual private `receipt-and-lost-ack` stage passed and is retained for its unchanged same-generation loss/recovery scope; new cross-generation behavior has separate current regressions and affected assessment. |
| `C07`, `C08`, `M18`, `M20` | Exact version/page/region personal records, independent reading positions, multi-device conflict resolution without silent overwrite, and honest disconnected/revoked/resync recovery. | Bounded Task 04 implementation and focused offline/desktop/E2E checks passed; private HTTPS cohort and full Stage A/physical-device exclusions follow the [2026-11-05 web-only owner direction](../decisions.md#owner-amendment-2026-11-05-web-only-epic-007-execution). |
| `X11`, `X12`, `X13` | Lost mutation response recovery via operation receipts, stale ETag conflict preservation with comparison/reload/overwrite, and expired event cursor `resync.required` full-state barrier without treating a historical receipt as current resource state. | Covered across `OR07`, `AIWF07P`, `test_owner_records_multi_device_reconciliation_passive_idle_and_receipt_separation`, `desktop/readerfacade`, and frontend suites; actual private resync and receipt observations passed within their selected scopes; current bounded correction judgments and the corrected frozen aggregate passed; physical/global qualifications remain separate. |

## Contract, state and recovery effects

- **Passive read header (`Haeryong-Passive-Read: 1`) and native `passive: true`**: Additive transport-level activity control on existing authenticated `GET`/`HEAD` routes and read-only native facade operations. Invalid header values or non-`GET`/`HEAD` methods fail `400 INVALID_REQUEST` before touching session activity.
- **Event cursor continuity and `resync.required` barrier**: `subscribeEvents` accepts optional `cursor` and returns bootstrap `resync` + `cursor` when opened without a cursor; `eventsPoll` returns `cursor` alongside `closed`, `invalidated`, and `resync`. Browser SSE commits `Last-Event-ID` only after a complete blank-line-framed event and successful handler completion. `ShellController` enforces a single-flight resync barrier with `readGeneration` fencing and retry on incomplete sweeps.
- **Receipt-vs-resource separation**: `GET /api/v1/operations/{id}` returns the historical operation outcome (`result_metadata`), never the live resource revision. Clients always follow a completed receipt recovery with a fresh `GET` on the mutable resource before clearing `UNKNOWN` or continuing a queued local mutation.

## Review and handoff

All original negative reports and priorities remain unchanged. Finite G1–G9,
H1–H5 and the Reader presentation correction have current direct proof and actual
original-role confirmation. The corrected frozen product's complete common,
current frontend/normal build, qualified selected private-web composition and
unchanged desktop evidence establish a strict current composition with no due
criterion or mandatory pending gap. Coordinator accepts this bounded member
under the existing delegation. The factual/lifecycle closeout and one removed
EOF blank line are separately verified; Python AST equality preserves the tested
behavior. Those bytes are not a fresh common execution.

The normal local member commit, native finalization and retained-output handoff
precede TASK-007-05. Original physical/mobile/OS-suspension/Wails qualification,
Task05/06, complete original Stage A and Master overall acceptance retain their
separate scope and owner.
