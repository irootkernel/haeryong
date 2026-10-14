# Haeryong Execution Specification

**Bundle version:** 1.6.2

**Edition:** EPIC-001 execution baseline: initial frontend/layout selections, dated subscription and deployment limits, and faithful task metadata. Product scope, D-08/D-09/D-10 and unrestricted directory/JavaScript behavior are unchanged; G-STACK has executed on the declared stack; remaining product gates are unrun.

**Baseline:** Haeryong Concept Proposal, Revision 4, as amended by the owner's 2026-10-10 personal-tool and directory-result directions (D-08 and D-09), plus the 2026-10-11 approval of additional tailnet-only HTTPS renderer-port experiments (D-10).

**Prepared:** 2026-10-13

**Status:** EPIC-001 execution authorized on 2026-10-13. TASK-001-01 is complete; follow canonical Task states and explicit dependencies; no additional specification-only review round is a prerequisite. The baseline/UI selections and provider/environment limits are recorded in that Task. Actual implementation evidence and the existing gates remain required; [TASK-001-02](tasks/EPIC-001/TASK-001-02.md) now supplies a bounded executable stack spike, not a production application.

Haeryong is a personal developer's AI-maintained, source-grounded knowledge wiki. The owner uses tools such as Codex, Claude or Grok to produce readable explanations of their work, then uses Haeryong to read, interact, ask questions and keep review history. One result is a directory that can contain many HTML pages, nested folders and any supporting files. Haeryong imports the selected tree, generates its inventory, preserves its paths and renders pages on demand. Normal HTML, CSS and JavaScript are the default, including libraries, modules and external resources under ordinary browser rules. There is no Haeryong-imposed HTML/package size, file-count or directory-depth ceiling, mandatory author manifest, required index.html or file-extension allowlist. This bundle turns that workflow into implementation contracts, a proposed product UI, sequential work packages, and focused delivery checks. The immediate reading workflow works for ordinary AI output without source enrichment. Exact source evidence, Q&A and maintenance add value when available without making the raw directory harder to import. Specification prose is Markdown. The read-only documentation validator is a development utility; embedded code, JSON and configuration blocks remain specification examples, not existing Haeryong commands or deployed services.

## Start here

| Reader | Recommended path |
| --- | --- |
| Owner reviewing the proposal | [Review guide](REVIEW-GUIDE.md), [decision register](decisions.md), [UI overview](ui/README.md), [R5 prototype](design/prototype/README.md), [wireframes](ui/wireframes.md), [roadmap](roadmap.md) |
| Implementing agent | [Execution protocol](execution-protocol.md), [requirements](requirements.md), [architecture](architecture.md), [contract index](contracts/README.md), [UI handoff](design/prototype/HANDOFF.md), current Epic and Task |
| Reviewer or release owner | [Traceability](testing/traceability.md), [acceptance catalog](testing/acceptance-catalog.md), [test strategy](testing/test-strategy.md), [release checklist](operations/release-checklist.md) |
| Operator | [Deployment](operations/deployment.md), [configuration](contracts/configuration.md), [backup and recovery](operations/backup-recovery.md) |

## What is authoritative

1. The owner's explicit decisions, including D-10's additional private renderer ports and the directory-result direction: prioritize a personal developer's speed; accept their chosen AI-authored directory and normal JavaScript; avoid artificial input/output quotas, compulsory packaging work and repeated authentication/approval. Go, Wails v3, Markdown specs, desktop **and** private remote access, a preferred 9000-series port, configurable policies, a Haeryong-owned skill and version-specific review remain established.
2. Revision 4 requirements and invariants, preserved in [the unchanged source](sources/haeryong-concept-proposal-v4.md), with the explicit [personal-tool amendment](changes/2026-10-10-personal-tool-revision.md) and [directory-result amendment](changes/2026-10-10-directory-results-revision.md) taking precedence where older assumptions conflict.
3. Concrete implementation proposals in this bundle. They become the execution baseline when accepted. They must not be misrepresented as independently approved owner choices.
4. A passing implementation gate is evidence recorded during development, not a checked box in this specification.

The [decision register](decisions.md) distinguishes inherited direction, proposed choices, and evidence gates. The [source map](source-map.md) separates source-derived scope from new elaboration. External facts are confined to [references](references.md) and attributed notes; they do not replace the owner's specification.

### EPIC-001 execution source of truth

The owner explicitly waived creation of a separate execution dossier for EPIC-001 on 2026-10-13. The composite source of truth is the canonical set linked here: [roadmap](roadmap.md), [EPIC-001 and its ordered Tasks](epics/EPIC-001.md), [execution protocol](execution-protocol.md), decisions/requirements, architecture/data model/ADRs, contracts/schema registry, UI specifications/R5 handoff, testing/knowledge-quality, operations, references and source map. Each keeps its existing requirement ownership. The waiver omits only the separate dossier; it waives no product gate, source binding, account/device proof or D-10 exposure boundary. Final Epic acceptance remains an owner decision after all required work and gates pass.

## Delivery shape

- **10 sequential Epics, 61 task specifications.** Complete the current Epic's exit gate before starting the next one. Task count follows required outcomes, with no fixed six-task rule. EPIC-001 includes a cheap reader-task checkpoint and a real-project usefulness spike before contract freeze, followed by actual-use checkpoints as features arrive.
- **Stage A:** EPIC-001 through EPIC-007, connected reader including authenticated remote web access.
- **Stage B:** EPIC-008, manual AI maintenance.
- **Stage C:** EPIC-009, periodic maintenance and explicitly enabled retention execution.
- **Release gate:** EPIC-010, integrated verification and distribution.

G-VIEW and G-AI each close against the complete checklist of their owning TASK-001-04 and TASK-001-05; later production behaviors are not extra early prerequisites. D-10 selects the additional tailnet-only port experiment, not a passed renderer or permission for public exposure. No network setting is changed by this bundle.

Stage A requires the connected desktop and private remote reader, real source-grounded AI integration, and the shared quality corpus. A deterministic fixture or fake provider does not satisfy the real provider integration gate. Durable local work and retry/event foundations belong to EPIC-002, before lifecycle/backup consumers; later Epics extend them.

## Navigation

| Area | Documents |
| --- | --- |
| Product | [Requirements](requirements.md), [glossary](glossary.md), [decisions](decisions.md) |
| Design | [Architecture](architecture.md), [data model](data-model.md), [ADRs](adr/README.md) |
| Interfaces | [Schema registry](contracts/schema-registry.md), [Package](contracts/package.md), [Import workflow](contracts/import-workflow.md), [HTTP API](contracts/http-api.md), [authentication](contracts/authentication.md), [viewer protocol](contracts/viewer-protocol.md), [CLI](contracts/cli.md), [jobs/events](contracts/jobs-events.md), [AI adapter](contracts/ai-adapter.md), [source access](contracts/source-access.md), [configuration](contracts/configuration.md) |
| UI | [Design index](design/README.md), [R5 prototype](design/prototype/README.md), [implementation handoff](design/prototype/HANDOFF.md), [Information architecture](ui/information-architecture.md), [screen specifications](ui/screens.md), [wireframes](ui/wireframes.md), [flows](ui/flows.md), [design system](ui/design-system.md) |
| Work | [Roadmap](roadmap.md), [Epic index](epics/README.md), [task index](tasks/README.md), [execution protocol](execution-protocol.md) |
| Quality | [Knowledge quality](testing/knowledge-quality.md), [acceptance](testing/acceptance-catalog.md), [fixtures](testing/fixtures.md), [traceability](testing/traceability.md), [strategy](testing/test-strategy.md), [stack experiment](testing/stack-spike.md), [bundle validation](testing/bundle-validation.md) |
| Operations | [Security](operations/security.md), [deployment](operations/deployment.md), [recovery](operations/backup-recovery.md), [observability](operations/observability.md), [release](operations/release-checklist.md) |

Mermaid diagrams and layout tables are editable Markdown source, not generated application screenshots. Canonical Epic/Task frontmatter owns current planning states; [G-STACK](testing/stack-spike.md) records `pass` for the declared ARM64 combination; the remaining five gates stay `not_run`. Historical documentation integrity checks for specification 1.6.2 are recorded in the [bundle validation report](testing/bundle-validation.md). The separate 2026-10-12 [R5 interactive concept](design/prototype/README.md) and [UI handoff](design/prototype/HANDOFF.md) provide UI baseline inputs with dated prototype review evidence. TASK-001-01 resolves initial layout choices; the complete R5 visual design remains unapproved.

## Current owner revision

[The Grok reconciliation](changes/2026-10-11-grok-reconciliation.md) records the D-10 approval, the 1.6.1 corrections and the [1.6.2 implementation handoff](changes/2026-10-11-grok-reconciliation.md#2026-10-12-implementation-handoff-162). The 2026-10-13 execution baseline records initial frontend/layout selections, subscription and deployment limits, the preliminary `not_available` reader comparison and handler metadata mapping. EPIC-001 is in progress; canonical Task frontmatter owns its sequential delivery state, and [G-STACK](testing/stack-spike.md) records `pass` for the tested combination; the other five gates remain `not_run`. The earlier 1.6.2 handoff clarified future-consumer operation IDs, unified viewer-gate summaries and recorded absent historical review files without changing product scope or task order. [The additional Opus reconciliation](changes/2026-10-11-opus-follow-up.md) records the preceding 1.5.0 corrections. [The earlier Opus reconciliation](changes/2026-10-11-opus-review-reconciliation.md) records the 1.4.0 baseline. [The directory-result revision](changes/2026-10-10-directory-results-revision.md) establishes the product direction: import and export a complete result directory without artificial size/count restrictions, navigate multiple HTML pages, preserve page-specific links/notes, and transfer/load large content progressively. [The personal-tool revision](changes/2026-10-10-personal-tool-revision.md) establishes normal JavaScript, remembered owner sessions and focused host separation. The [earlier review revision](changes/2026-10-10-review-revision.md) records retained data/contract corrections and superseded proposals. The original concept and external reviews remain historical inputs. The bundle version does not change or claim compatibility for any implemented runtime schema.

## Integrity inventory

[MANIFEST.md](MANIFEST.md) lists all other Markdown files and the two Python documentation-check utilities with byte sizes and SHA-256 values. Run `python3 -B docs/testing/validate_docs.py` from the checkout; use `--root PATH` for another checkout. The validator requires Python 3 and PyYAML and writes no project files. `python3 -B docs/testing/test_validate_docs.py` exercises status admission and assertion ownership in disposable copies; it needs Git and cleans up its own fixtures. Its default scratch parent is the system temporary directory; `HAERYONG_DOCS_TEST_TMP_ROOT` optionally selects an existing operator-chosen parent. Agents set that variable to their verified task temporary directory under the applicable host guidance. These checks do not validate product behavior. [The historical validation report](testing/bundle-validation.md) distinguishes its snapshot from current execution.
