---
id: "EPIC-007"
type: "epic"
title: "Private remote web and multi-device operation"
status: "done"
depends_on: ["EPIC-006"]
completion_requires: ["TASK-007-01","TASK-007-02","TASK-007-03","TASK-007-04","TASK-007-05","TASK-007-06"]
---

# EPIC-007: Private remote web and multi-device operation

The recorded overall owner acceptance selects this run's web-only delivery as
the terminal Epic scope. The original Goal and Exit gate below retain the full
physical Stage A definition and are not claimed passed by this closeout.

## Goal

Complete Stage A by delivering the real connected understanding workflow from authenticated mobile/desktop browsers while Wails is closed.

## Entry gate

Complete and review EPIC-006, including its passing exit gate, before starting this Epic. Apply the [execution protocol](../execution-protocol.md) and current [personal-tool amendment](../changes/2026-10-10-personal-tool-revision.md) and [directory-results amendment](../changes/2026-10-10-directory-results-revision.md). No task depends on an unimplemented future Epic to meet its stated exit.

## Ordered tasks

| Order | Task | Outcome area | Dependency |
| --- | --- | --- | --- |
| 1 | [TASK-007-01](../tasks/EPIC-007/TASK-007-01.md) | Implement controlled private ingress and endpoint setup | EPIC-006 |
| 2 | [TASK-007-02](../tasks/EPIC-007/TASK-007-02.md) | Complete browser pairing and session management UX | TASK-007-01 |
| 3 | [TASK-007-03](../tasks/EPIC-007/TASK-007-03.md) | Complete mobile/tablet workflow parity | TASK-007-02 |
| 4 | [TASK-007-04](../tasks/EPIC-007/TASK-007-04.md) | Implement multi-device reconciliation and conflict UI | TASK-007-03 |
| 5 | [TASK-007-05](../tasks/EPIC-007/TASK-007-05.md) | Verify headless-host source and provider boundaries | TASK-007-04 |
| 6 | [TASK-007-06](../tasks/EPIC-007/TASK-007-06.md) | Close Stage A remote connected-reader gate | TASK-007-05 |

Task IDs remain stable; use the explicit order and dependencies. Task count follows coherent outcomes, not a fixed per-Epic quota.

## Exit gate

**C01–C09, M08, M10, M14 and M15–M20 pass under current owner-amended applicability for the selected Stage A environment and real knowledge-quality corpus. Actual private second-device reading/notes/review/Q&A, useful JavaScript, host/native separation, headless lifetime and reconnect consistency are demonstrated.**

Every task listed in completion_requires must be done and reviewed under the shared definition of done. Record actual evidence with the canonical pass/fail/blocked/not_run status. Required failures or not-run checks prevent completion; a future Epic is not evidence for this exit.

## Acceptance contribution

C01, C02, C03, C04, C05, C06, C07, C08, C09, M08, M10, M14, M15, M16, M17, M18, M19, M20. See [traceability](../testing/traceability.md) and the task-specific supplemental cases. Contribution does not certify an unexercised full scenario. Knowledge-related gates use [knowledge quality](../testing/knowledge-quality.md).

## Handoff

Stage A passing record with actual provider, primary browser/WebView, second device, source and private route; real two-project reader outcomes. Manual/periodic maintenance is not claimed complete. Use the [Epic gate template](../templates/epic-gate.md) as an index to actual artifacts and identify the exact next task.

## Canonical Outcomes

All six ordered members delivered the owner-amended selected-web scope and have
separate verified completion commits and native handoffs. The assembled audit,
its actual independent conclusions and the qualified overall acceptance are
indexed by the [selected private-web gate](../testing/stage-a-gate.md).

| Member | Delivered behavior and canonical evidence |
| --- | --- |
| [TASK-007-01](../tasks/EPIC-007/TASK-007-01.md) | Controlled private trusted/renderer ingress, additive permanent origin ownership and endpoint doctor: [private ingress](../testing/private-ingress.md) |
| [TASK-007-02](../tasks/EPIC-007/TASK-007-02.md) | Pairing/session management, passive activity and safe policy/receipt reconciliation: [private sessions](../testing/private-device-sessions.md) |
| [TASK-007-03](../tasks/EPIC-007/TASK-007-03.md) | Shared responsive Reader/control workflows and the qualified actual retained Ask: [web workflows](../testing/mobile-reader-workflows.md) |
| [TASK-007-04](../tasks/EPIC-007/TASK-007-04.md) | Independent resource revisions, conflict drafts and reconnect/current-authority recovery: [reconciliation](../testing/multi-device-reconciliation.md) |
| [TASK-007-05](../tasks/EPIC-007/TASK-007-05.md) | Independent headless host and current-source/provider/retained-input boundaries: [headless source boundaries](../testing/headless-source-boundaries.md) |
| [TASK-007-06](../tasks/EPIC-007/TASK-007-06.md) | Actual two-project connected-reader corpus, recovery and internal normal-package handoff: [selected gate](../testing/stage-a-gate.md) and [deployment](../operations/deployment.md) |

The [web-only owner direction](../decisions.md#owner-amendment-2026-11-05-web-only-epic-007-execution)
and its recorded overall acceptance govern this terminal selected-web delivery.
Original physical second-device/mobile, Wails GUI, sleep/wake and complete
physical Stage A assertions remain unverified. Original Task assertions and their
unchecked broader qualifications are preserved. No public exposure, installation,
maintenance, schedules, release or untested-platform qualification is implied.

Durable contracts retain their existing owners: [authentication](../contracts/authentication.md),
[renderer isolation](../contracts/viewer-protocol.md), [source access](../contracts/source-access.md),
[preconditions/receipts](../contracts/http-api.md#preconditions-receipts-and-retry-matrix)
and [jobs/events](../contracts/jobs-events.md). Complete result directories and
ordinary JavaScript retain the owner amendments without new author manifests,
extension allowlists or size/count/depth caps. Tailnet reachability never supplies
bearer identity. Exact source observation stays distinct from retained provider
input and later verification. Review and page notes remain version-specific;
resource ETags and current state remain distinct from request receipts.

The temporary execution dossier's mapping below is promoted without changing its
attribution. Its product requirements remain in their canonical owners. Historical
failures and proof applicability remain in the six evidence records. The next
ordered owner is [TASK-008-01](../tasks/EPIC-008/TASK-008-01.md), after its own entry
gate; this closeout does not start it.

## Assertion coverage owners

A derived reading; the Task files' assertion texts and frontmatter lists remain authoritative, and each Task records its actual
bounded contribution.

| Assertion | Contributes to | Canonical owners | Prerequisite |
| --- | --- | --- | --- |
| T-007-01-01 | M16 | [Network baseline](../operations/deployment.md#simple-network-baseline), [listener configuration](../contracts/configuration.md#listener-origin-and-remembered-sessions) | Disposable profile; no network change |
| T-007-01-02 | M15, M17, X04 | [Tailscale candidate](../operations/deployment.md#tailscale-serve-candidate), [render endpoints](../contracts/http-api.md#endpoint-catalog) | Private second device, host and route; route-change authority |
| T-007-01-03 | M16, M17, X04 | [Request protections](../contracts/authentication.md#request-protections), [network baseline](../operations/deployment.md#simple-network-baseline) | Same route as T-007-01-02 |
| T-007-02-01 | M17, X03 | [Pairing sequence](../contracts/authentication.md#pairing-sequence), [S01](../ui/screens.md#s01-connect-and-device-approval) | Local or remote client |
| T-007-02-02 | C05, M17, M20, X04 | [Remembered sessions](../contracts/authentication.md#remembered-sessions-and-transport), [content delivery authority](../contracts/authentication.md#content-delivery-authority) | Private second device on the route |
| T-007-02-03 | M17, X03, X04 | [Pairing sequence](../contracts/authentication.md#pairing-sequence), [remembered sessions](../contracts/authentication.md#remembered-sessions-and-transport) | Same as T-007-02-02 |
| T-007-03-01 | C03, C08, M15, X02, X19, X20 | [Environment scope](../tasks/EPIC-007/TASK-007-03.md#environment-prerequisites-and-completion-scope), [flows](../ui/flows.md), [viewer protocol](../contracts/viewer-protocol.md) | Selected physical device; TASK-007-01 renderer construction; provider authority for Ask |
| T-007-03-02 | C03, M15 | [Accessibility criteria](../ui/design-system.md#accessibility-requirements-for-this-project), [TASK-003-06 baseline](../testing/responsive-cross-client-gate.md) | Same device |
| T-007-03-03 | M15, X18 | [Large result directories](../operations/deployment.md#large-result-directories-and-transfer), [screens](../ui/screens.md) | Same device; actual picker and destination |
| T-007-04-01 | C07, C08, M18, X11, X13 | [Preconditions and receipts](../contracts/http-api.md#preconditions-receipts-and-retry-matrix), [reader preferences and position](../ui/flows.md#reader-preferences-and-position) | Two clients, including a remote device |
| T-007-04-02 | M18, X12 | [Jobs and events](../contracts/jobs-events.md) | A real provider job only under provider authority |
| T-007-04-03 | M18, M20, X11, X12, X13 | [Replay and resync](../contracts/jobs-events.md#replay-and-resync), [multi-device conflict](../ui/flows.md#multi-device-conflict) | Offline faults first; real provider only within scope |
| T-007-05-01 | M15 | [Startup and recovery](../operations/deployment.md#startup-and-recovery), [source and provider availability](../operations/deployment.md#source-and-provider-availability) | Selected headless host without Wails; original checkout unavailable |
| T-007-05-02 | M19 | [Remote source behavior](../contracts/source-access.md#remote-source-behavior) | Same host |
| T-007-05-03 | M19 | [Source access](../contracts/source-access.md), [AI adapter](../contracts/ai-adapter.md) | Host source scope and provider host-mode authority |
| T-007-06-01 | C01–C09, M15–M19, X03, X04, X11, X12, X13, X15, X19, X20 | [Stage closure](../testing/acceptance-catalog.md#stage-and-quality-closure), [traceability](../testing/traceability.md#inherited-acceptance-to-primary-work) | Complete selected Stage A environment, real provider, physical device with Wails closed |
| T-007-06-02 | M08, M10, M14, M20, X04, X07, X13, X16 | [Current applicability](../testing/acceptance-catalog.md#current-applicability), [startup and recovery](../operations/deployment.md#startup-and-recovery), [restore protocol](../operations/backup-recovery.md#restore-protocol) | Selected installation; disposable profile for restore |
| T-007-06-03 | C03, C04, C08, M19, X15 | [Progressive checkpoints](../testing/knowledge-quality.md#progressive-use-checkpoints) | Real corpus; evaluator recorded |

Initial supplemental owners and procedures stay in [contract cases](../testing/contract-cases.md); EPIC-007 repeats only the
regressions its Tasks list. The Stage A closing set is TASK-007-06's three assertions. EPIC-010 references to M08/M14/M20 are
release regression, not prerequisites.
