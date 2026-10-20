# EPIC-003 shared Reader and Wails gate

**Epic:** [EPIC-003](../epics/EPIC-003.md)
**Decision:** `pass` for the implemented shared Reader and available macOS/browser environment.
**Reviewed implementation:** member commits below, assembled at `d3ae38d`.
**Review:** agent-operated; the standing Epic Judge and two fresh non-author Workers independently assessed the assembled result, followed by Main's project/product review.
**Acceptance:** Main accepted individual Tasks under delegated authority. Master accepted the assembled Epic for this closeout; these are separate decisions.

## Member deliveries

| Task | Local completion commit | Implementation and actual evidence | Review |
| --- | --- | --- | --- |
| [TASK-003-01](../tasks/EPIC-003/TASK-003-01.md) | `2a4e6a5` | [Shared shell and adapters](frontend-shell.md) | Task Judge, non-author Workers, Epic Judge and Main: pass/accepted |
| [TASK-003-02](../tasks/EPIC-003/TASK-003-02.md) | `5255974` | [Library and complete streamed transfers](result-transfer.md) | Task Judge, non-author Workers, Epic Judge and Main: pass/accepted |
| [TASK-003-03](../tasks/EPIC-003/TASK-003-03.md) | `06d4520` | [Multi-page Reader, delivery and recovery](reader-viewer.md) | Task Judge, non-author Workers, Epic Judge and Main: pass/accepted |
| [TASK-003-04](../tasks/EPIC-003/TASK-003-04.md) | `16916be` | [Exact version and evidence context](evidence-presentation.md) | Task Judge, non-author Workers, Epic Judge and Main: pass/accepted |
| [TASK-003-05](../tasks/EPIC-003/TASK-003-05.md) | `6dcc322` | [Independent thin desktop companion](desktop-companion.md) | Task Judge, non-author Workers, Epic Judge and Main: pass/accepted |
| [TASK-003-06](../tasks/EPIC-003/TASK-003-06.md) | `d3ae38d` | [Responsive and cross-client qualification](responsive-cross-client-gate.md) | Task Judge, non-author Workers, Epic Judge and Main: pass/accepted |

Each completion commit was inspected against its accepted candidate. The
assembled review is separate from those Task approvals; no empty implementation
commit represents it. Detailed checks and historical qualifications stay with
the linked records rather than being generalized beyond their measured scope.

## Assembled outcome and verification

The shared trusted shell imports selected folder/ZIP/HTML inputs and streams
complete original-directory downloads through the existing authenticated service.
Browser reading works without Wails. The thin Wails companion uses the same
Reader and transfer contracts through a closed, authenticated Go facade; its
window lifetime does not own the daemon or library. Exact immutable
version/page/query/fragment identity, nested links, page-local context and
on-demand files survive navigation and reopening. Normal html_js_v1 scripts,
modules and workers remain useful while frames have no host DOM, management
token or native authority. Broken-page recovery remains reachable.

The actual reused Mulgae/Gaori directories passed R1–R4 reading questions,
inline evidence inspection and meaningful controls, including the await-deadline
branch, exact reopening and original raw/ZIP byte comparisons across clients.
These are reader observations of the supplied explanations, retaining their
Illustration labels; they are not new upstream provider executions or general
semantic-accuracy certification. X18 preserved all seven indexless-tree entries,
including Unicode HTML, hidden/unknown files, an empty directory and the exact
relative symlink target. Instrumented X07 retained modules/workers/click behavior
while host DOM/storage and native dispatch attempts were denied.

| Final check | Actual result and boundary |
| --- | --- |
| Canonical `make test` | pass; prepare, Go unit/integration/additional persistence and Python unit 10, integration 40, public E2E 46; source hashes unchanged during execution; no selected failure/error/skip |
| Frontend type/unit/browser-native builds and committed-byte equality | pass on the current source; regenerated assets accompany their authoritative inputs |
| Real Chrome responsive regression | pass, 298 assertions across the four declared viewports, light/dark/reduced-motion fixtures, scaling, targets/contrast, keyboard/touch, real transfer and reading paths; separate full persistent-session transfer/fault regression also passed |
| Native qualification | pass by the documented approved composition: all original journey/cross-client/tree/isolation stages plus six completed region cases bound to source/binary/dist applicability; no claim of one fresh inline six-case matrix |
| Desktop checks | tagged race/vet/build and actual untagged automation/probe-flag rejection passed; test instrumentation remains absent from production builds |
| Documentation integrity | pass for the accepted Task records; the closeout manifest is regenerated and validated at the closeout commit boundary |

Responsive panels preserve one live viewer/channel across layout changes.
The first programmatic native region-focus attempt can honestly fail; actual
keyboard reading followed by re-selection recovered the four positive cases,
and missing regions remained honest. The UI describes this measured recovery
as a possibility. Both-width host-control measurements are distinct from the
wide-only authored skip-link Tab+Enter observation. Native select geometry was
repaired and measured; popup opening remains unmeasured. An asserted stale-focus
notice race was inspected without establishing a concrete defect; no speculative
fix or extra gate was added. The required authored-link documentation overclaim
was corrected and independently reassessed before Task acceptance. Original
failed runs and superseded assessments remain preserved.

## Boundaries and next dependency

Browser FileList cannot represent empty directories or symlinks; the disclosed
ZIP route preserves the whole tree. Native picker fixtures and synthetic
walkthroughs retain their labels; the separately recorded genuine chooser
callbacks are not generalized to every prompt or device. Additional physical
devices, remote/private-device deployment, routine owner usefulness, native
select popup opening, source reading/verification, wiki search and later AI
features remain with their implementing owners. This is not signed release,
remote publication or complete Stage A acceptance. Runtime reports, captures,
credentials and provider/session identities remain private.

All six member Tasks and the Epic exit gate pass for this scope. The next
ordered implementation is [TASK-004-01](../tasks/EPIC-004/TASK-004-01.md).
Closeout satisfies its Epic dependency but does not activate it or authorize
new source/provider access, installation, host changes or deployment.
