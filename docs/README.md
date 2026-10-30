# Haeryong Execution Specification

**Bundle version:** 1.6.2

**Edition:** EPIC-001 execution baseline: initial frontend/layout selections, dated subscription and deployment limits, and faithful task metadata. Product scope, D-08/D-09/D-10 and unrestricted directory/JavaScript behavior are unchanged; G-STACK, bounded G-AUTH, G-VIEW and G-AI have executed; bounded G-VALUE has executed; selected G-CONTRACT has executed.

**Baseline:** Haeryong Concept Proposal, Revision 4, as amended by the owner's 2026-10-10 personal-tool and directory-result directions (D-08 and D-09), plus the 2026-10-11 approval of additional tailnet-only HTTPS renderer-port experiments (D-10).

**Prepared:** 2026-10-13

**Status:** EPIC-001 has completed internal validation; its [prepared closeout record](epics/EPIC-001.md#prepared-closeout-record) takes effect only when committed after actual Master overall acceptance. TASK-001-01 through TASK-001-05 and TASK-001-07 are complete; TASK-001-06 is complete after the verified Task07 commit; [G-CONTRACT](testing/contract-freeze.md) records bounded `pass` for the selected schema/fixture/calibration and reviewed evidence composition. Whole-Epic audit and final internal validation passed at the recorded revision; actual Master overall acceptance and the conditional closeout commit remain separate. Follow canonical Task states and explicit dependencies. [G-STACK](testing/stack-spike.md) and bounded [G-AUTH](testing/auth-ingress-spike.md) record `pass`; [G-VIEW](testing/viewer-spike.md) records `pass`; [G-AI](testing/provider-source-spike.md) records bounded `pass`; [G-VALUE](testing/knowledge-value-spike.md) records bounded `pass`; [G-CONTRACT](testing/contract-freeze.md) records bounded `pass`. The [2026-10-15 owner amendment](decisions.md#owner-amendment-2026-10-15-agent-operated-private-browser-gate) selects an agent-operated actual Aside browser on private HTTPS for the initial G-VIEW proof, with Android Chrome and Safari retained for later platform-support/release verification. The stack/auth/viewer spikes are executable experiments, not a production application. The closeout boundary and later implementing owners retain their actual-evidence obligations; no additional specification-only review round is a prerequisite.

Haeryong is a personal developer's AI-maintained, source-grounded knowledge wiki. The owner uses tools such as Codex, Claude or Grok to produce readable explanations of their work, then uses Haeryong to read, interact, ask questions and keep review history. One result is a directory that can contain many HTML pages, nested folders and any supporting files. Haeryong imports the selected tree, generates its inventory, preserves its paths and renders pages on demand. Normal HTML, CSS and JavaScript are the default, including libraries, modules and external resources under ordinary browser rules. There is no Haeryong-imposed HTML/package size, file-count or directory-depth ceiling, mandatory author manifest, required index.html or file-extension allowlist. This bundle turns that workflow into implementation contracts, a proposed product UI, sequential work packages, and focused delivery checks. The immediate reading workflow works for ordinary AI output without source enrichment. Exact source evidence, Q&A and maintenance add value when available without making the raw directory harder to import. Specification prose is Markdown. The read-only documentation validator is a development utility; embedded code, JSON and configuration blocks remain specification examples, not existing Haeryong commands or deployed services.

## Current implementation boundary

EPIC-002 has completed its verified headless foundation, beginning with [TASK-002-01](tasks/EPIC-002/TASK-002-01.md). The root Go module now supplies explicit profile initialization, a single-writer foreground daemon and authenticated local-control discovery/stop; [the implementation evidence](testing/profile-lifecycle.md) records executed checks and limits. [TASK-002-02](tasks/EPIC-002/TASK-002-02.md) now supplies real profile/bootstrap, SQLite and immutable-object/local-operation foundations; [its evidence](testing/storage-foundation.md) records actual checks and current review limits. The daemon reports storage readiness only after durable bootstrap and reference verification. [TASK-002-03](tasks/EPIC-002/TASK-002-03.md) adds the current owner bootstrap/session and trusted connect boundary; [its evidence](testing/authenticated-sessions.md) distinguishes real executed checks and remaining review/private-ingress/native limits. [TASK-002-04](tasks/EPIC-002/TASK-002-04.md) adds resumable streamed uploads, durable seal/import jobs and atomic directory publication; [its evidence](testing/atomic-import.md) records current backend checks and limits. [TASK-002-05](tasks/EPIC-002/TASK-002-05.md) adds authenticated queries/streamed files, durable import/validation/recovery CLI and revisioned configuration authority; [its evidence](testing/query-cli-configuration.md) records current execution and review limits. [TASK-002-06](tasks/EPIC-002/TASK-002-06.md) repeats actual process/fault/pin recovery and current macOS/Linux ARM64 regressions; [its evidence](testing/foundation-recovery.md) records the verified offline headless entrypoint, exact canonical F12 and four compilation targets. All six member Tasks, current native checks, independent whole technical review and Task/Epic Judge assessments have passed; the [EPIC-002 gate](testing/epic-002-foundation-gate.md) records pass for this headless scope. TASK-003-01 is complete for its shell/adapter scope: the shared React trusted shell, browser/native transport adapters and committed compiled assets are delivered on the existing listener, with [frontend shell evidence](testing/frontend-shell.md) recording current executed checks and remaining limits. TASK-003-02 delivers shared-UI result transfers with resumable streams and real host-destination delivery; [the transfer record](testing/result-transfer.md) owns its executed checks and limits. TASK-003-03 delivers the shared multi-page Reader with isolated renderer delivery, personal reader resources and the reused native ReaderMode harness; [the Reader record](testing/reader-viewer.md) owns its executed checks and limits. TASK-003-04 adds the shared Reader's evidence presentation — SourcePanel/VersionHeader, the typed evidence query adapter and its closed browser/native/Go operations with the supplied-metadata `{etag, stream}` bridge — with [its record](testing/evidence-presentation.md) stating the fixed contract, executed candidate checks and remaining review limits; TASK-004-01 adds approved exact-revision evidence verification/history, owner-triggered source observation, committed target-bound coverage and availability summaries, project registration/source configuration and CLI `sources observe` / `assessments list`. The shared three transports and Evidence action use `features.source_verification`; [the source-observation record](testing/source-observation.md) records the executed final-candidate checks and limits. TASK-004-02 delivers authenticated static text/region search through HTTP, CLI and the shared Library/native clients; [its evidence record](testing/text-region-search.md) records the approved A1 tie repair, the real-sample evidence and its limits. TASK-004-03 delivers canonical pinned references: immutable authored declarations with structural exact-version edges, read-time-eligible GET/HEAD list and resolve with the matching CLI, and capability-gated exact cross-project Reader navigation with host region selection and source return in the browser and native clients. [Its evidence record](testing/pinned-references.md) records the real-sample Gaori → Mulgae M11 case, the seeded matrix and their limits. Semantic assessment production remains with its ordered owner; TASK-005-01 supplies explicit personal Review as described below. Remote CI remains not_run, AMD64 is compilation only, and the assembled responsive/cross-client, provider and private-device gates remain with their ordered owners. The existing EPIC-001, spike and R5 qualifications remain unchanged.

The root [TESTING.md](../TESTING.md) owns common headless test commands, strict layer boundaries, canonical frameworks and isolated E2E setup. The [EPIC-005 verification profile](testing/epic-harmony-verification-profile.md)
selects native `make test KEEP_GOING=1` plus its scoped supplements.
The shared `make test` aggregate replaces the former monolithic runner without changing the historical EPIC-002 evidence or later product gates.

TASK-003-05 is complete: the thin Wails companion uses the shared native
facade, Go-held onboarding and credentials, and the shared import/download
engines. [Its implementation record](testing/desktop-companion.md) describes
the qualified packaged fixture, actual owner picker callbacks, independent
daemon lifetime, streamed transfers, native isolation and page/transfer
recovery on the available Darwin host. Whole-Task review and Main delegated acceptance establish this delivery
boundary; TASK-003-06 owns the assembled responsive/cross-client gate.

TASK-004-04 adds canonical scoped backlinks, separate Reader Links sections and owner-only deterministic index validation/rebuild without a schema migration or AI. [The backlink record](testing/backlinks.md) records its accepted delivery, executed checks and recovery limits.

TASK-004-05 adds pinned Copy link, automatic session-return navigation and the shared Versions view with progressive structural comparison. [Its record](testing/deep-links-versions.md) distinguishes executed browser/native checks, synthetic personal/assessment evidence and verification limits. TASK-005-01 now adds explicit version Review, history, Home summaries and the last-Reviewed comparison default; [its record](testing/version-review.md) records the executed final-candidate checks and limits.

TASK-004-06 adds the two-project knowledge gate composition over the existing source/search/link/Versions surfaces. [Its record](testing/two-project-knowledge.md) contains the relevance report, agent-only saved-reader worksheet and EPIC-004 evidence index.

TASK-005-02 adds exact anchored Notes, conflict-safe Save and explicit
reattachment/history, safe Markdown, saved-context recovery and independent
Revisit/Home with three-transport parity at SQLite revision 11. The
[Notes record](testing/anchored-notes.md) records the executed final-candidate
checks and limits.

TASK-005-03 adds document Archive/Trash/Restore, revision-bound impact plans and an
owner Trash list across HTTP/native/desktop and CLI at schema revision 12. The
[lifecycle record](testing/document-lifecycle.md) records the executed final-candidate
checks and limits; protected restore retains its later owner.

TASK-005-04 adds guarded manual purge, permanent minimal version reservations and
resumable pin-safe content GC at schema revision 13. The shared owner confirmation,
CLI and three transports retain exact plan/202 identity. The [purge record](testing/manual-purge.md)
records the executed final-candidate checks and limits.

TASK-005-05 implements coherent host-local backup, exact scoped directory/ZIP export
and exclusive protected restore at schema revision 14. Settings and purge read real
backup status. [The recovery record](testing/backup-recovery.md#review-and-acceptance)
records the final-candidate checks, delegated Task acceptance and remaining limits.
Encryption and scheduling are not delivered; DF-02 remains explicitly deferred.

TASK-005-06 completes the two-project owner-state composition: actual browser and
native Save/Review, exact anchors/history/context through restart and protected
recovery, lifecycle safety and complete original-directory export/reimport.
[The owner record](testing/owner-records.md) states the execution boundaries;
[the Epic handoff](testing/epic-005-owner-gate.md) separates Task delivery from
assembled Epic review and Master's overall acceptance.

## Start here

TASK-006-01, TASK-006-02 and TASK-006-03 are complete within their bounded
foundation/source/transport scopes. The [managed-source implementation record](testing/managed-sources.md)
covers capture, selected snapshots and remembered Sources defaults. The
[provider-attempt foundation](testing/provider-attempts.md) describes durable
attempt/input/result accounting, one-use fencing and recovery at revision 15;
[provider transport](testing/provider-adapter.md) adds the verified personal
subscription adapter and owner status at revision 17. Public generation/skill
publication, saved Q&A and the assembled Epic exit retain TASK-006-04/05/06
ownership; TASK-006-04 follows Task03's normal member closeout.

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
| Work | [Roadmap](roadmap.md), [Epic index](epics/README.md), [task index](tasks/README.md), [execution protocol](execution-protocol.md), [deferred feedback](deferred-feedback/README.md) |
| Quality | [Knowledge quality](testing/knowledge-quality.md), [acceptance](testing/acceptance-catalog.md), [fixtures](testing/fixtures.md), [traceability](testing/traceability.md), [strategy](testing/test-strategy.md), [stack experiment](testing/stack-spike.md), [auth/ingress experiment](testing/auth-ingress-spike.md), [provider/source experiment](testing/provider-source-spike.md), [understanding experiment](testing/knowledge-value-spike.md), [bundle validation](testing/bundle-validation.md) |
| Operations | [Security](operations/security.md), [deployment](operations/deployment.md), [recovery](operations/backup-recovery.md), [observability](operations/observability.md), [release](operations/release-checklist.md) |

Mermaid diagrams and layout tables are editable Markdown source, not generated application screenshots. Canonical Epic/Task frontmatter owns current planning states; [G-STACK](testing/stack-spike.md) records `pass` for the declared ARM64 combination; [G-AUTH](testing/auth-ingress-spike.md) records `pass` for the bounded one-owner/private-HTTPS experiment; [G-VIEW](testing/viewer-spike.md) records `pass`; [G-AI](testing/provider-source-spike.md) records bounded `pass`; [G-VALUE](testing/knowledge-value-spike.md) records bounded `pass`; [G-CONTRACT](testing/contract-freeze.md) records bounded `pass`. Historical documentation integrity checks for specification 1.6.2 are recorded in the [bundle validation report](testing/bundle-validation.md). The separate 2026-10-12 [R5 interactive concept](design/prototype/README.md) and [UI handoff](design/prototype/HANDOFF.md) provide UI baseline inputs with dated prototype review evidence. TASK-001-01 resolves initial layout choices; the complete R5 visual design remains unapproved.

## Current owner revision

[The Grok reconciliation](changes/2026-10-11-grok-reconciliation.md) records the D-10 approval, the 1.6.1 corrections and the [1.6.2 implementation handoff](changes/2026-10-11-grok-reconciliation.md#2026-10-12-implementation-handoff-162). The 2026-10-13 execution baseline records initial frontend/layout selections, subscription and deployment limits, the preliminary `not_available` reader comparison and handler metadata mapping. [EPIC-001 closeout](epics/EPIC-001.md#prepared-closeout-record) records completed internal validation and the conditional acceptance boundary; canonical Task frontmatter owns its sequential delivery state, and [G-STACK](testing/stack-spike.md) records `pass` for the tested combination; [G-AUTH](testing/auth-ingress-spike.md) records `pass` for the bounded auth experiment; [G-VIEW](testing/viewer-spike.md) records `pass`; [G-AI](testing/provider-source-spike.md) records bounded `pass`; [G-VALUE](testing/knowledge-value-spike.md) records bounded `pass`; [G-CONTRACT](testing/contract-freeze.md) records bounded `pass`. The earlier 1.6.2 handoff clarified future-consumer operation IDs, unified viewer-gate summaries and recorded absent historical review files without changing product scope or task order. [The additional Opus reconciliation](changes/2026-10-11-opus-follow-up.md) records the preceding 1.5.0 corrections. [The earlier Opus reconciliation](changes/2026-10-11-opus-review-reconciliation.md) records the 1.4.0 baseline. [The directory-result revision](changes/2026-10-10-directory-results-revision.md) establishes the product direction: import and export a complete result directory without artificial size/count restrictions, navigate multiple HTML pages, preserve page-specific links/notes, and transfer/load large content progressively. [The personal-tool revision](changes/2026-10-10-personal-tool-revision.md) establishes normal JavaScript, remembered owner sessions and focused host separation. The [earlier review revision](changes/2026-10-10-review-revision.md) records retained data/contract corrections and superseded proposals. The original concept and external reviews remain historical inputs. The bundle version does not change or claim compatibility for any implemented runtime schema.

## Integrity inventory

[MANIFEST.md](MANIFEST.md) lists all other Markdown files and the two Python documentation-check utilities with byte sizes and SHA-256 values. Run `python3 -B docs/testing/validate_docs.py` from the checkout; use `--root PATH` for another checkout. The validator requires Python 3 and PyYAML and writes no project files. `python3 -B docs/testing/test_validate_docs.py` exercises status admission and assertion ownership in disposable copies; it needs Git and cleans up its own fixtures. Its default scratch parent is the system temporary directory; `HAERYONG_DOCS_TEST_TMP_ROOT` optionally selects an existing operator-chosen parent. Agents set that variable to their verified task temporary directory under the applicable host guidance. These checks do not validate product behavior. [The historical validation report](testing/bundle-validation.md) distinguishes its snapshot from current execution.

## Provider transport implementation

TASK-006-03 adds the verified Codex 0.164.0 personal host-local stdio adapter,
revision 17 attempt audit and owner provider status in HTTP/CLI/Settings/native.
The [implementation record](testing/provider-adapter.md) documents the actual
historical and pre-correction E01 source fixtures, controlled auth/quota/interruption/cleanup cases and
browser/native interaction and completed common/final-assessment results. Final
independent assessment and delegated member acceptance establish this bounded
Task03 delivery; the normal completion commit and native finalization follow
under the same authority. Logical source/provider choices and normal reading/Review remain independent of AI
availability. Public generation, saved Q&A and assembled interruption/corpus
qualification retain TASK-006-04/05/06 ownership.


[Provider correction evidence](testing/provider-corrections.md) records R1's
corrected settlement, full-history and provenance boundaries, a separately
executed 1,454-byte real source fixture and shared Chrome/Wails feature checks.
Its later Stop-only error-retention delta has separate offline proof and does
not claim whole-core byte identity with those executions. The failed first
common round, completed prior-candidate common2/R2, Work4 recovery/ACK/refusal/
cleanup/General-navigation corrections and later local6 producer-fact/late-setup
proof remain explicit. R3 and R4 completed; R4 predates the local corrections.
The same-code full common4 passed 1,461 checks across 15 stages, including all
197 exact feature cases once. Subsequent factual documentation overlays change
no Go/UI/assets/tests and have separate docs-integrity/diff proof. The final
independent assessment passed all 24 current criteria and four native Goal
criteria, with no mandatory unmet/unverified row; the bounded Task Goal is
achieved. Final independent assessment and delegated member acceptance establish
this bounded Task03 delivery. The normal completion commit and native
finalization follow under the same authority; this record claims no commit or
Master overall Epic acceptance. Later Tasks remain separate. Historical
real success remains qualified by exact changed-path applicability, not
current-byte identity.
