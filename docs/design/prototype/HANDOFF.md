# R5 prototype implementation handoff

**Status:** Unapproved design concept and implementation reference; not a production application or a passed implementation gate.
**Baseline:** [Execution specification v1.6.2](../../README.md), with D-08/D-09/D-10 unchanged.
**Evidence cut-off:** The R5 review record dated 2026-10-12, with latest observations on 2026-10-13 KST.

## Use this reference within the existing baseline

Start with the [prototype](prototype.html), [design notes](DESIGN-NOTES.md) and [review record](REVIEW.md).
Use them to understand proposed information hierarchy, copy, screen composition and sample transitions.
The [screen specifications](../../ui/screens.md), [flows](../../ui/flows.md), [route/action ownership](../../ui/information-architecture.md) and [contracts](../../contracts/README.md) remain the implementation references.
The [schema registry](../../contracts/schema-registry.md) remains the canonical route/shared-state authority; prototype route strings are examples.

The [execution Status](../../README.md) directs implementation through the existing dependencies, beginning with TASK-001-01; that Task records the initial baseline choices.
It explicitly says that an additional specification-only review round is not a prerequisite.
This handoff adds no design-approval gate, changes no task status, grants no new implementation authorization and reopens no product scope.
R5 remains unapproved; its presence does not establish owner approval of individual visual choices.

## What the recorded evidence establishes

The latest R5 sections of [DESIGN-NOTES.md](DESIGN-NOTES.md) and [REVIEW.md](REVIEW.md) take precedence over their preserved R0–R4 historical sections.

| Evidence | Recorded scope and limit |
| --- | --- |
| 63 captures and metadata | 49 initial/state/partial-fix captures plus 14 temporary R5 CSS previews; major routes, entry dialogs, settings sections and representative states were inspected. These are captures, not 63 distinct implemented screens. |
| Actual browser surface | The user-opened tab retained R3 HTML and JavaScript. R4 and progressively reviewed R5 CSS were temporarily applied without loading or refreshing the new R5 file. |
| Complete R5 artifact | Source integration, syntax/regeneration and bounded state checks were recorded. A browser load of the complete new R5 HTML and JavaScript was not run. |
| Input-blocking overlay fix | The closed-menu backdrop defect and its correction were checked at the same recorded 920×1324 size, including hit testing, menu opening/closing and a sample note save. |
| New settings/recovery behavior | New markup, per-section drafts, pairing transitions and recovery/focus adapters were inspected in source or limited in-memory checks; this does not prove browser keyboard traversal. |
| Responsive evidence | Early 1280×1368 and later mainly 920×1324 observations were not a controlled viewport sweep. Phone/touch and systematic size changes remain unverified. |
| Visual precision | Low-quality JPEG transfers support structural observations, not final judgments about fine typography, color or pixel quality. |
| Product validation | Full accessibility, screen-reader behavior, real data, authentication, files, AI, backup integration, user-task usability and design approval were not established. |

Actual Grok requests produced design candidates and revisions; ChatGPT reviewed the screenshots.
Grok received source and written observations, not the screenshot pixels; do not describe it as having visually inspected those images.
Use [STATIC-R5.json](STATIC-R5.json), [VALIDATION.json](VALIDATION.json) and the [review record](REVIEW.md) for their stated scopes.
The image-embedded capture gallery `haeryong-design-review-r5.html` and its JPEG/metadata files were delivered separately; they are not installed in this repository folder.
Do not relabel old-tab captures, earlier revision checks or isolated state checks as a full R5 end-to-end pass.

## Screen-to-specification-to-task map

This map follows the existing [Navigation](../../ui/information-architecture.md#navigation) and [Action-to-contract ownership](../../ui/information-architecture.md#action-to-contract-ownership) tables.
It identifies initial and later owners; it is not an alternative task sequence or evidence that later capabilities exist.

| R5 surface | Existing UI specification | Existing implementation owner |
| --- | --- | --- |
| Global navigation, shell and context layout | [Initial execution baseline](../../ui/design-system.md#initial-execution-baseline), [responsive behavior](../../ui/design-system.md#responsive-behavior), [core components](../../ui/design-system.md#core-component-contract) | [TASK-003-01](../../tasks/EPIC-003/TASK-003-01.md) shared shell/transports; [TASK-003-06](../../tasks/EPIC-003/TASK-003-06.md) responsive/cross-client verification |
| Home, Library, Projects and empty results | [S02 Home](../../ui/screens.md#s02-home), [S03 Library](../../ui/screens.md#s03-library-and-project-view), [WF-01](../../ui/wireframes.md#wf-01-desktop-home), [WF-02](../../ui/wireframes.md#wf-02-library-with-explicit-filters) | [TASK-003-02](../../tasks/EPIC-003/TASK-003-02.md) shared browse/transfer UI; [TASK-004-02](../../tasks/EPIC-004/TASK-004-02.md) search; [TASK-005-01](../../tasks/EPIC-005/TASK-005-01.md) personal Review |
| Reader, Files/Pages, preferred page and position | [S04 Reader](../../ui/screens.md#s04-reader), [reader preferences](../../ui/flows.md#reader-preferences-and-position), [WF-03](../../ui/wireframes.md#wf-03-desktop-reader) | [TASK-003-03](../../tasks/EPIC-003/TASK-003-03.md) multi-page Reader; [TASK-003-04](../../tasks/EPIC-003/TASK-003-04.md) exact version/evidence context |
| Versions and exact comparison | [S05 Versions](../../ui/screens.md#s05-versions-and-changes) | [TASK-004-05](../../tasks/EPIC-004/TASK-004-05.md) progressive comparison; [TASK-005-01](../../tasks/EPIC-005/TASK-005-01.md) version-specific Review |
| Review, Revisit, Notes and conflicts | [S04 Reader](../../ui/screens.md#s04-reader), [multi-device conflict](../../ui/flows.md#multi-device-conflict) | [TASK-005-01](../../tasks/EPIC-005/TASK-005-01.md) Review/Revisit; [TASK-005-02](../../tasks/EPIC-005/TASK-005-02.md) anchored notes; [TASK-007-04](../../tasks/EPIC-007/TASK-007-04.md) cross-device reconciliation |
| Ask, saved answers and source-mode choice | [S04 Reader](../../ui/screens.md#s04-reader), [read/verify/review flow](../../ui/flows.md#read-verify-and-review) | [TASK-006-05](../../tasks/EPIC-006/TASK-006-05.md) region-aware questions/history; [TASK-006-06](../../tasks/EPIC-006/TASK-006-06.md) real AI workflow verification |
| Sources, Evidence and project/provider configuration | [S09 Sources](../../ui/screens.md#s09-sources), [S04 Evidence behavior](../../ui/screens.md#s04-reader) | [TASK-003-04](../../tasks/EPIC-003/TASK-003-04.md) display/disabled check; [TASK-004-01](../../tasks/EPIC-004/TASK-004-01.md) enabled exact-source check/settings; [TASK-006-02](../../tasks/EPIC-006/TASK-006-02.md) source/provider integration |
| Connect, comparison code and remembered devices | [S01 Connect](../../ui/screens.md#s01-connect-and-device-approval), [connect/resume flow](../../ui/flows.md#connect-once-and-resume), [WF-08](../../ui/wireframes.md#wf-08-device-pairing) | [TASK-002-03](../../tasks/EPIC-002/TASK-002-03.md) auth foundation; [TASK-003-01](../../tasks/EPIC-003/TASK-003-01.md) shell; [TASK-007-02](../../tasks/EPIC-007/TASK-007-02.md) complete pairing/session UX |
| Import, new version, download and Storage/uploads | [Import/export controls](../../ui/screens.md#import-export-and-file-operations), [import flow](../../ui/flows.md#import-and-open-a-result-directory), [S07 Settings](../../ui/screens.md#s07-settings) | [TASK-002-05](../../tasks/EPIC-002/TASK-002-05.md) upload discovery service; [TASK-003-02](../../tasks/EPIC-003/TASK-003-02.md) shared controls/storage UI; [TASK-003-03](../../tasks/EPIC-003/TASK-003-03.md) Reader mount; [TASK-004-05](../../tasks/EPIC-004/TASK-004-05.md) Versions mount; [TASK-003-05](../../tasks/EPIC-003/TASK-003-05.md) native handoff |
| Maintenance, coverage, relationships, topics and findings | [S06 Maintenance](../../ui/screens.md#s06-maintenance-and-findings), [proposal lifecycle](../../ui/flows.md#maintenance-proposal-lifecycle), [WF-06](../../ui/wireframes.md#wf-06-maintenance-report) | [TASK-006-01](../../tasks/EPIC-006/TASK-006-01.md) AI job foundation; [EPIC-008 ordered tasks](../../epics/EPIC-008.md#ordered-tasks) scans, candidates, scoped assessment and presentation; [TASK-008-05](../../tasks/EPIC-008/TASK-008-05.md) decisions/reassessment/undo |
| Content draft review, replacement and publication | [S06 Maintenance](../../ui/screens.md#s06-maintenance-and-findings), [proposal lifecycle](../../ui/flows.md#maintenance-proposal-lifecycle) | [TASK-008-06](../../tasks/EPIC-008/TASK-008-06.md) draft rejection/replacement and separate explicit publication |
| Settings, schedules and retention impact | [S07 Settings](../../ui/screens.md#s07-settings), [WF-07](../../ui/wireframes.md#wf-07-settings-and-retention-impact) | [TASK-003-02](../../tasks/EPIC-003/TASK-003-02.md) initial settings/storage UI; [TASK-007-02](../../tasks/EPIC-007/TASK-007-02.md) sessions; [TASK-009-01](../../tasks/EPIC-009/TASK-009-01.md) policy controls; [TASK-009-05](../../tasks/EPIC-009/TASK-009-05.md) enabled retention execution |
| Archive, Trash, purge, backup and restore | [S08 lifecycle](../../ui/screens.md#s08-archive-trash-and-purge), [destructive flow](../../ui/flows.md#destructive-lifecycle), [S07 backup observations](../../ui/screens.md#s07-settings) | [TASK-005-03](../../tasks/EPIC-005/TASK-005-03.md) archive/trash/restore; [TASK-005-04](../../tasks/EPIC-005/TASK-005-04.md) purge; [TASK-005-05](../../tasks/EPIC-005/TASK-005-05.md) backup/portability/protected restore |
| Page failure, disconnect, revocation and recovery | [Page errors](../../ui/screens.md#page-errors-and-external-resources), [shared error model](../../ui/screens.md#shared-error-model), [restored-session refresh](../../ui/flows.md#cross-device-and-restored-session-refresh) | [TASK-003-03](../../tasks/EPIC-003/TASK-003-03.md) page recovery; [TASK-005-06](../../tasks/EPIC-005/TASK-005-06.md) lifecycle recovery; [TASK-007-04](../../tasks/EPIC-007/TASK-007-04.md) reconciliation; [TASK-007-06](../../tasks/EPIC-007/TASK-007-06.md) integrated Stage A |

## Preserve these interaction meanings

Use the linked screen/flow sections for full details; the following distinctions are easy to lose when translating the demo.

- One Library row is a directory result; pages and files remain inside its exact immutable version.
- Explicit page links, preferred default, Continue pointer and each page's position are separate concepts/records.
- Opening a page never marks Review complete; Review, freshness, Revisit and lifecycle remain separate.
- Notes/questions retain exact version/page/region and the captured question target across navigation.
- Import retries retain the explicit document, captured base and operation intent; names/paths/hashes never select a target.
- Saved means server-confirmed or reconciled success; stale conflicts preserve still-authorized drafts instead of overwriting them.
- Stop waiting ends local polling, not server cancellation; disconnected, rejected, expired and revoked states need accurate actions.
- A retention duration does not enable purge; impact confirmation rechecks current revisions, with backup information rather than a new backup prerequisite.
- Approving an overlay, generating/replacing a draft and publishing a version are separate actions.
- Unsupported production actions stay hidden or capability-disabled under [progressive disclosure](../../ui/information-architecture.md#progressive-capability-disclosure).

## Reconcile visual proposals in the existing UI baseline review

The earlier design-system proposal and R5 were not identical; the [current design system](../../ui/design-system.md) records the initial execution selection.
R5 retains the R4 Reader layout adjustments described in [DESIGN-NOTES.md](DESIGN-NOTES.md); they remain concept choices; [the initial execution baseline](../../ui/design-system.md#initial-execution-baseline) records the selected implementation values without approving the full R5 design.

| Choice | Earlier design-system proposal (historical) | R5 source choice | Current follow-through |
| --- | --- | --- | --- |
| Wide context width | 352 px at the proposed wide breakpoint | 340 px | TASK-001-01 selects 340 px in [the initial baseline](../../ui/design-system.md#initial-execution-baseline); TASK-003-06 verifies actual content and panels. |
| Reading typography | 16 px body, line-height 1.6 | 16 px default, line-height 1.7; preference controls | TASK-001-01 selects 16 px / 1.7 in [the initial baseline](../../ui/design-system.md#initial-execution-baseline); TASK-003-06 inspects long real prose/code/tables. |
| 768–1279 px layout | Compact navigation and overlay context with focus containment | Compact navigation with Reader/context columns; context width clamped between 320 and 340 px | TASK-001-01 selects a focus-contained overlay in [the initial baseline](../../ui/design-system.md#initial-execution-baseline); TASK-003-06 verifies readable width, overflow and focus. |

[TASK-001-01](../../tasks/EPIC-001/TASK-001-01.md) already owns the shared frontend/layout baseline and UI review.
[TASK-003-06](../../tasks/EPIC-003/TASK-003-06.md) owns responsive/cross-client implementation verification; [TASK-007-03](../../tasks/EPIC-007/TASK-007-03.md) later completes mobile/tablet workflow parity.
Use the proposed 390×844, 844×390, 820×1180 and 1440×900 fixtures from [Responsive behavior](../../ui/design-system.md#responsive-behavior).
Measure applicable contrast, touch targets, focus, keyboard/screen-reader behavior and text scaling in the delivered implementation.
A CSS declaration, review frame or old-tab image does not establish those results.
These differences are existing-task review items, not a new approval gate or a reason to restart the specification process.

## Continue in the existing dependency order

[EPIC-001](../../epics/EPIC-001.md#ordered-tasks) specifies this order; numeric IDs alone do not define it.

1. [TASK-001-01](../../tasks/EPIC-001/TASK-001-01.md): ratify baseline/UI brief, provider eligibility preflight, handler metadata and environment prerequisites; record the preliminary reader comparison.
2. [TASK-001-02](../../tasks/EPIC-001/TASK-001-02.md): prove the headless stack and native companion.
3. [TASK-001-03](../../tasks/EPIC-001/TASK-001-03.md): validate pairing and private ingress.
4. [TASK-001-04](../../tasks/EPIC-001/TASK-001-04.md): prove useful ordinary JavaScript and host separation.
5. [TASK-001-05](../../tasks/EPIC-001/TASK-001-05.md): validate the real supported provider and exact source reads.
6. [TASK-001-07](../../tasks/EPIC-001/TASK-001-07.md): run the bounded real two-project understanding experiment.
7. [TASK-001-06](../../tasks/EPIC-001/TASK-001-06.md): freeze evidence-backed contracts after TASK-001-07 and close readiness only when the required gates pass.

TASK-001-01 explicitly excludes product code, real data import and public exposure.
R5 can supply its UI review input; it does not replace its eligibility, metadata, environment or reader-comparison deliverables.
A preliminary comparison records benefit_observed, no_difference or not_available; these observations are not additional gate statuses.
Carry the latter two into TASK-001-07's declared risks as that task requires.
TASK-001-07 needs approved real sources/revisions, a named evaluator, inspectable claims/evidence and the JavaScript/Markdown comparison.
Synthetic R5 examples and sample answers cannot pass G-VALUE or establish owner usefulness.

Follow all existing Epic dependencies after EPIC-001; [EPIC-003's entry gate](../../epics/EPIC-003.md#entry-gate) requires completed/reviewed EPIC-002.
The selected UI-owner map above does not skip EPIC-004 or change the ten-Epic sequence.
[EPIC-005](../../epics/EPIC-005.md), [EPIC-006](../../epics/EPIC-006.md), [EPIC-007](../../epics/EPIC-007.md), [EPIC-008](../../epics/EPIC-008.md) and [EPIC-009](../../epics/EPIC-009.md) retain their own entry/exit gates.

## Do not copy demonstration restrictions or state into production

The standalone demo's CSP sets default-src 'none', connect-src 'none', inline-only script/style permissions, data images and disabled forms/objects/base URLs.
That policy isolates this self-contained demonstration; it is not the production html_js_v1 renderer or security configuration.
Do not copy the demo CSP, inline-script allowance or no-network behavior into the production shell/renderer.
Use the [viewer protocol](../../contracts/viewer-protocol.md), [architecture](../../architecture.md) and the owning G-VIEW task.
Ordinary author JavaScript/modules and browser-permitted external resources remain in scope, with generated content separated from host management authority.

Sample documents, statuses, comparison codes, approvals, imports, AI answers, jobs, settings and backup outcomes are mock state.
They live in tab memory and reset on reload; they prove no persistence, authentication, authorization, receipt reconciliation or real provider work.
Do not promote simulation buttons, fabricated success states or the prototype's state adapters into production service behavior.
Do not treat synthetic document text as verified source explanations or copied page-location strings as working cross-device deep links.
Implement supported actions against [documented read/mutation contracts](../../ui/information-architecture.md#action-to-contract-ownership) and actual capability responses.
Keep the demo distinguishable from the real application, including its collapsed pairing simulation controls.

## Record implementation evidence under its existing owner

Prioritize a fresh full-R5 walkthrough when reviewing this concept further: new settings drafts/apply/cancel, connection return state, maintenance tabs and note-panel focus.
That walkthrough remains design-reference evidence, not a substitute for a delivered feature's acceptance tests.
At each feature gate, exercise the delivered subset of the [UI review checklist](../../ui/flows.md#ui-review-checklist) and identify later owners of unavailable features.
Use the [execution protocol](../../execution-protocol.md) and [task evidence template](../../templates/task-evidence.md) for actual pass/fail/blocked/not_run records.
G-STACK, G-AUTH, G-VIEW, G-AI, G-VALUE and G-CONTRACT still require their owning evidence before [TASK-001-06](../../tasks/EPIC-001/TASK-001-06.md) and EPIC-001 can close.
No full R5 browser run, production implementation, rendering test or new gate result was performed to prepare this handoff.
