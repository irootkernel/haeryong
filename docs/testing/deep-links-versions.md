# Deep links and version navigation

**Task:** [TASK-004-05](../tasks/EPIC-004/TASK-004-05.md), EPIC-004 — status `done`
**Implementation:** delivered: pinned Copy link, automatic Connect return and the shared S05 Versions view
**Source revision:** HEAD `8898abc` plus the TASK-004-05 change set; the completion suite ran on the frozen final candidate equal to the committed tree; exact run bindings stay in private receipts
**Environment:** macOS arm64, prepared Go 1.27.1/Python 3.14.7/Node 26.7.0, Chrome/installed Playwright CLI and pinned Wails ReaderMode; no installs, provider calls or ingress changes
**Evidence status:** pass for the executed checks below, on the candidates stated; earlier failed attempts stay recorded as failures; no native transfer from S05 is claimed
**Implementer / independent reviewer:** Executor Writer; Task Evaluator, two non-authoring Executor reviewers and the Epic Evaluator; final project/product review and acceptance by the Coordinator

## Behavior and contracts

Copy link constructs a credential-free exact document/version/page/query/fragment/region
route, resolved against approved configuration or the trusted attachment/browser origin.
Local-only links are labelled; clipboard failure leaves selectable text. Automatic
Connect return runs per entry and stamps fresh contexts. Existing stamped history,
hash-only frames, reference/backlink generations and validated source-return state remain.
Native startup is `checking` until the Go-held attachment settles, so an ordinary
reload is not mistaken for a definitive authentication loss or a new Connect entry.
S05 returns focus after the actual selected frame/region acknowledgment, or once a
frameless Reader settles (no HTML, renderer unavailable or bootstrap failure).
Region IDs preserve nonempty Unicode data, including encoded controls such as `%09`;
the shared hash grammar rejects literal raw controls before decoding.

Versions mounts the existing ImportControl with the exact document and pre-transfer base.
The pair is frozen independently from latest publication notices: Reader selection/latest,
or target predecessor/latest for direct history; a single version compares to itself.
Resolved defaults replace the current URL's pair without adding a history entry.
Same-authority degraded/verified transitions retain the screen and open ImportControl;
reload and Back/Forward reuse the pinned pair rather than a later publication.
Explicit foreign versions are unavailable without their title/files. At the TASK-004-05
checkpoint Review was unavailable; [TASK-005-01](version-review.md) now owns real
Review/history and the last-Reviewed default. This historical record adds no seeded default.
The required seeded personal rows test only preservation/non-migration before EPIC-005.

Two cursor pages merge in UTF-8 byte order with bounded input/output batches. Visible
results precede later cursor completion; retry retains the failed cursor and completion
requires both EOFs. Metadata and exact page/region differences remain structural facts.
Only `metadata_not_supplied` confirms absent metadata; a generic 404 remains unavailable.
Unavailable/loading metadata never masquerades as empty metadata. Text previews retain
one visible block and advance on request, verifying all bytes at EOF; binary/unsupported
content retains download fallback. Decode/read failure before clean EOF immediately
cancels the owned reader and stream. Download consent, destination and completion belong
to the exact version/path selection. Old pages never resolve through a successor.

Source availability/last observation, selected-target semantic coverage and personal
Review remain separate. The existing SourcePanel/SourceQueries keep fixed units and
reasons, capture versus assessment time, and the attempt locator across first/next/retry.
Older-target history cannot replace the selected applicable capture. Labelled supplied
assessments do not establish a semantic producer or real EPIC-008 workflow.

The [registry](../contracts/schema-registry.md#deep-link-and-versions-consumption-task-004-05)
and [HTTP composition](../contracts/http-api.md#versions-client-composition-task-004-05)
own additive shell-query/consumer behavior. Existing HTTP/native/Go operations are reused;
no wire DTO, schema/configuration migration or server diff endpoint is added. Obligations
1–10, including source eligibility, D4 tombstones and unchanged index recovery, remain.

## Focused verification index

| Scope | Executable evidence | Observed status |
| --- | --- | --- |
| T-004-05-01/02 | Router/base/login unit tests; existing Links driver `--versions` | pass on executed candidate: approved/local-only copy, unsafe-return pure checks, two automatic remembered-session return cycles, reload and Back/Forward |
| T-004-05-03 | `test_references.py -k T5` | pass after closing fixture DB connections; explicitly seeded note/Review rows unchanged across real successor import and exact old-page reads; same-name new anchors receive no state |
| T-004-05-04 / S1–S4 | `versions-data.test.ts`; `check_links.py --versions` | eight browser cases pass: metadata/regions and verified side-by-side text/reopening, pending-page visible rows, same-cursor retry, both EOFs, foreign-pair non-disclosure, generation/stream cancellation, keyboard/emulated touch, actual conflict and publication recovery |
| X19 / native links | Existing `check_references.py`, default `check_links.py`; native `versions,evidence,references` | browser drivers pass; native Versions Copy/keyboard/selection/focus/latest/no-HTML, exact backlink return and all four reference capability modes pass, including actual reload context and explicit Connect return |
| T-004-05-05 / 7(d) | SourceQueries/coverage tests; existing evidence browser/native stages | unit, browser and the first round's three-stage native run pass; attempt locator preserved on first page / failed next page / same-page retry; assessments remain labelled supplied data |
| Build/preparation | frontend check_build regeneration/validation; desktop race/vet; `make test-prepare` | frontend regeneration/validation and desktop race/vet pass; final preparation exit status is bound in the private Writer handoff |

Candidate attribution: the canonical suite, frontend validation and desktop race/vet
ran on the final candidate; Chrome Versions and the ReaderMode Versions stage on the
final product sources; the general browser and NativePort Connect drivers on the first
correction round's sources; the Links, References and Evidence browser drivers and the
ReaderMode Evidence and References stages in the first implementation round. Later
changes do not alter the flows those earlier runs cover.

## R1 correction verification

The current frontend gate passes the encoded `%09` parse/return/Copy regression,
immediate cancellation of malformed UTF-8 before EOF, and confirmed metadata absence
versus a generic 404 with a supplied digest. The existing eight-case S05 browser run
also passes frameless return focus/flag consumption, download selection lifetime,
and a real same-authority degraded→verified cycle after publication: the ImportControl
instance and receipt survive, and the pinned pair survives reload and Back/Forward.
The added download target is explicitly an in-memory FSA fixture for consent/completion
and late-picker ownership; it is not a disk or OS-picker qualification. Session 503s
are test-owned responses through the existing route barrier and actual App recheck.

Both existing App-wide drivers pass without expectation changes: `check_browser.py`
over the production HTTP/auth/SQLite fixture (108 recorded facts, including automatic
return and disconnected recovery), and `check_native_connect.py` (49 cases over a
labelled synthetic NativePort). The real ReaderMode `versions` stage also passes.
Earlier Links/References/Evidence, persistence and desktop results retain their original
scope; cumulative input-delta facts and proposed reuse are in the Writer handoff for
Coordinator judgment. They do not replace a final-candidate completion backstop.

## R2 correction verification

- R2-1: every committed viewer connection failure reports its captured version/context/reload to the Reader; only the current context consumes the S05 return-focus intent. The existing S05 browser case passes error display, opener focus and flag consumption with only render-session open returning a labelled 503; normal acknowledgment and frameless paths remain.
- R2-2: S05 captures authority per action and rechecks after Open latest's await; failed history loads have an explicit retry preserving pinned pair and open ImportControl. The existing browser case passes three separate degraded mounts followed by recovered pair/return/latest actions, plus failed-initial-load recovery with the same ImportControl instance and pair.

Browser fixtures use real immutable imported inventories and metadata. The later-page
503/delay barrier, cross-tab credential removal/restoration and dropping one committed
import response are test-owned. A loopback-only fixture hook publishes a real competing
version while the first S05 request waits: the product returns `VERSION_CONFLICT`.
Explicit fresh-base import uses a new operation key; recovery after the lost response
reuses its original key/body/base/upload. The publication receipt and frozen pair remain
visible. The same-page selected-region change also invalidates a delayed Copy action.
Narrow touch uses an actual hasTouch context, with keyboard activation and Playwright's
native-select choice action. It is emulation, not physical-device evidence. Private
screenshots retain desktop progress and narrow control states. No browser plugin is
available, so the repository's installed Playwright CLI drives Chrome.

Earlier failures remain in private receipts: test type/Node strip-only syntax, initial
Reader focus timing, a touch-context mismatch and missing SQLite fixture close. The
native Copy probe originally sent Enter before region acknowledgment; it now waits for
that acknowledgment and confirms host focus before sending Enter to its own App PID.
Fixes preserve the prior behavioral assertions and do not raise inherited timeouts.
The new native stage uses bounded receipt polling rather than a settling delay. The selected
coverage fixture now adds an attributed attempt and one failed-page retry, traced to
Task criterion 05 / Obligation 7(d); that changes fixture semantics, not product authority.
The second native run completed Versions but stopped before References when both stages
prepared the same fixture directory. They now share the already imported immutable
reference fixture; the subsequent run verifies that composition.
The next run exposed a native reload context regression: the initial pending attachment
was labelled unauthenticated and triggered premature Connect navigation. The controller
now starts native attachment in checking state; a held-attachment regression retains
the verified-dispatch gate, and the first round's last native run (Versions, Evidence
and References) repeats the actual reload case.

The bounded native selection launches the same compiled App directly for Versions and
References. It omits the unrelated CDN-dependent `app` smoke to honor offline execution;
common build/daemon/session setup and stable-input checks remain. Full qualification is
false. The real S05 ImportControl displays the exact document and captured base.
ReaderMode registers no PickerPresenter, so its system-picker action returns
`NATIVE_PICKER_UNAVAILABLE` by design. Under Coordinator decision S6, the unchanged
native chooser/transfer remains covered by [desktop-companion.md](desktop-companion.md);
this Task claims no native transfer from S05. The private receipt retains the attempt.

## Review and acceptance

The Task Evaluator and the two non-authoring Executor reviewers reviewed the plan
(two rounds), the first result and both correction rounds; the Epic Evaluator
reviewed the plan and the final result. R1 corrected encoded region IDs, frameless
return focus, stream cancellation before EOF, download selection lifetime, pair
retention across same-authority recovery and confirmed metadata absence, aligned
this record with decision S6, and added the App-wide browser and NativePort
Connect drivers. R2 extended return focus to viewer connection failure and moved
S05 authority checks to each action, with a retryable history load. The
Coordinator completed the final project/product review and accepted the Task under
Master's delegated authority; overall EPIC-004 acceptance remains pending.

On the final candidate the canonical `make test`, frontend build equality and unit
tests, and the desktop race and vet run passed. The browser and native driver runs
above executed on the candidates stated; later changes do not alter their flows.
Real Review/notes workflows remain TASK-005-01/02/06, real semantic production
EPIC-008, and private ingress, physical devices and full native qualification their
ordered owners.
