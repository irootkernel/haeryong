# EPIC-008 execution dossier

**Consumer:** [EPIC-008](../epics/EPIC-008.md) only.
**Role:** Detailed SOT: a thin integration map. This file owns no product requirement,
work identity, dependency, order or lifecycle state.

## Authority and entry

The [documentation index](../README.md), [owner decisions](../decisions.md),
[requirements](../requirements.md) and [execution protocol](../execution-protocol.md)
own precedence and delivery rules. The [roadmap](../roadmap.md), Epic and original
Task frontmatter retain all work identities and dependencies. Original Task
assertions and supplemental owners remain normative; this map does not replace
them or certify a passed check.

EPIC-007's [Canonical Outcomes](../epics/EPIC-007.md#canonical-outcomes) provide
the terminal selected-web prerequisite. Its original physical-device and full
physical Stage A qualifications remain unverified. This dossier grants no new
device qualification and does not reopen that accepted delivery.

The outcome is useful manual structural/semantic maintenance, honest captured
coverage and policy-authorized reversible changes. Ordinary imported directories
remain readable without author manifests, source metadata or declared regions.
Original bytes, authored references, provenance and personal Review stay separate
from later management records.

## Canonical contract owners

| Concern | Owner |
| --- | --- |
| Process, storage and shared clients | [Architecture](../architecture.md) and [data model](../data-model.md) |
| Jobs, capture, fencing, findings and events | [Jobs/events](../contracts/jobs-events.md) |
| Route authority and preconditions | [HTTP API](../contracts/http-api.md) and [CLI](../contracts/cli.md) |
| Shared enums, scopes, coverage and target precedence | [Schema registry](../contracts/schema-registry.md) |
| Action modes, budgets and current policy | [Configuration](../contracts/configuration.md) |
| Exact bytes, availability and citation basis | [Source access](../contracts/source-access.md) and [AI adapter](../contracts/ai-adapter.md) |
| Screens and owner actions | [Screens](../ui/screens.md) and [flows](../ui/flows.md) |
| Acceptance and semantic decision rules | [Acceptance catalog](../testing/acceptance-catalog.md), [traceability](../testing/traceability.md), [knowledge quality](../testing/knowledge-quality.md) and [contract cases](../testing/contract-cases.md) |

## Ordered delivery and handoffs

Task IDs are stable. Only the current Task receives implementation authority;
read-only preparation of later work does not start it.

| Order | Task and original assertions | Consumed foundation and delivered handoff |
| --- | --- | --- |
| 1 | [TASK-008-01](../tasks/EPIC-008/TASK-008-01.md), T-008-01-01 through 03 | Consume accepted EPIC-007 and existing storage/jobs/search/references. Deliver reproducible scan inventory, deterministic inspection/authorized repair and structural coverage for Task02. |
| 2 | [TASK-008-02](../tasks/EPIC-008/TASK-008-02.md), T-008-02-01 through 04 | Consume Task01's captured eligibility. Deliver bounded candidate retrieval, actual-content relationship/theme evidence and held-out quality results for Task03. |
| 3 | [TASK-008-03](../tasks/EPIC-008/TASK-008-03.md), T-008-03-01 through 04; X15 | Consume exact captures, candidate evidence and the registry's existing scope model. Deliver scoped semantic contributions, fixed coverage units, history/uncertainty presentation and indirect-impact proof for Task04. |
| 4 | [TASK-008-04](../tasks/EPIC-008/TASK-008-04.md), T-008-04-01 through 03 | Consume Task03 records. Deliver separately stored, origin-labelled proposals/overlays, policy modes and Maintenance presentation for Task05. |
| 5 | [TASK-008-05](../tasks/EPIC-008/TASK-008-05.md), T-008-05-01 through 03; X08/X11/X12 | Consume proposals and current validators. Deliver atomic guarded decisions, rejection fingerprints, pin preservation, conflict-safe undo and explicit reassessment for Task06. |
| 6 | [TASK-008-06](../tasks/EPIC-008/TASK-008-06.md), T-008-06-01 through 04; X08 | Consume Task05 and existing generation/draft/import. Deliver finding-to-draft flow, replacement/rejection, full Maintenance report and assembled manual-maintenance Stage B proof. |

The six Tasks' requirement/acceptance mappings remain in their frontmatter.
EPIC-008 contributes C06, M01-M05, M11, M12 and M19; contribution is not a pass of
an unexercised full scenario. Later scheduling and release work retain their
existing owners; no EPIC-009 scheduler is needed for this exit.

## Integration boundaries

- Extend the existing dispatcher, durable receipts, job ownership, object pins,
  source capture and shared client adapters. Do not introduce a second scheduler
  or allow provider code to mutate application storage directly.
- Capture eligible scope, exact source selection/targets and effective policy
  before dispatch. Incrementally enumerate the full captured inventory. Changes,
  exclusions, missing evidence and semantic budget exhaustion never shrink the
  denominator or fabricate complete alignment.
- Resolve declared scan/finding/overlay routes through HTTP, CLI and the shared
  browser/native contracts. Page and region identity include the exact version
  and page path; regionless input remains assessable at its available scope.
- Store immutable original evidence separately from later assessments. Keep
  capture time, assessment time, target precedence and per-unit outcomes/reasons
  inspectable. Source capture, observed provider input, posthoc verification and
  semantic support are distinct facts.
- Apply only under current revision, lifecycle, source authority and policy.
  Preserve pinned/rejected owner choices; equivalent retries cannot add duplicate
  overlays. Undo cannot overwrite an intervening owner decision. Reassessment is
  a new captured scan, never force-apply.
- A selected finding may start an authorized draft generation. Draft reject and
  sealed-upload replacement preserve the captured base/history and fence late
  linked results. Publish is a separate explicit owner action that creates a new
  immutable version; no maintenance mode grants automatic publication or Review.

## Required evidence and reuse

Use [TESTING.md](../../TESTING.md) for actual layer placement and canonical common
commands. Internal white-box SQLite, migration, restart and fault/recovery checks
belong in labelled persistence coverage. Public CLI/HTTP/process behavior remains
black-box E2E, including its production-created and migrated SQLite databases;
persistence checks do not substitute for those required E2E scenarios.
Keep offline fakes, actual provider operations and browser/
native/physical-device observations separately qualified.

Before tuning, record the held-out relationship/freshness cases, exact approved
targets, expected supported/unsupported/uncertain outcomes and decision rule under
knowledge quality. Include false keyword dependencies, useful cross-project
relations, missing themes, direct/indirect changes, unrelated commits, historical
intent, unavailable source and partial coverage. False dependencies, false
outdated judgments, missed changes and abstentions remain separate; invariant
failures cannot be averaged away or replaced with easier examples.

Run the original T-* assertions and due supplemental regressions at each owning
feature. Exercise disabled/propose/auto overlays and content-generation modes,
mid-flight policy/lifecycle changes, idempotency, restart, cancellation, late
results and conflict-safe undo. Repeat the actual Maintenance owner flow on
supported compact viewports with keyboard/touch; do not label emulation as
physical-device proof.

Reuse earlier results only when their command, inputs, coverage, dependencies
and environment remain applicable. Record preserved scope and cumulative delta;
do not relabel reuse as a fresh run. Exact-current gates require current execution.
Retain failed aggregates and run mandatory cases left unexecuted by fail-fast.
Focused corrections refresh only affected checks and independent judgment unless
impact is unknown or a broader gate is due. The canonical completion selection
is `make test KEEP_GOING=1`; progress output does not prove native completion.

## Authority, retention and closeout

The execution envelope and actual current configuration own permissions. Reuse
verified existing source/provider approvals within their exact scope without
per-read prompts. New source disclosure, account/billing choices, installation,
host/network changes, other repositories and publication require their applicable
authority; this dossier supplies none. Use disposable dev/test libraries for
lifecycle/restore proof rather than owner production data.

The Coordinator alone transfers bounded Writer authority, adjudicates permission
requests, accepts exact member/correction candidates and invokes authorized commit
tooling. Only one Writer mutates shared files or generated assets at a time.
Independent assessors do not author their reviewed plan/result. Keep Task
contribution and whole-Epic acceptance distinct. Verify each member's actual
commit and native finalization before starting its successor.

Keep runtime evidence private and local under its producer's retention contract;
do not copy runtime paths, credentials, private source or hidden reasoning into
tracked documents. Put agent-owned scratch under a unique `/tmp` directory and
use its physical path where required. Creators own cleanup; retained outputs need
a named next consumer, owner and retention end. Resolve unexplained leftovers
before crossing Task boundaries.

The assembled gate requires the original Epic exit, all due checks, independent
integration assessment and Coordinator product review. Failed or missing required
quality proof blocks Stage B. Prepare the final candidate before requesting
Master overall acceptance; perform only its matching conditional closeout commit
after that actual acceptance.

At successful closeout, promote durable information to its canonical owners and
replace this Epic's Detailed SOT with Canonical Outcomes. Before removal, inventory
all references to this dossier. Delete it and its TODO index entry only when no
consumer needs it and the approved last-consumer closeout includes that deletion.
