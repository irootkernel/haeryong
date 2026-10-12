# Opus review reconciliation: 2026-10-11

**Documentation bundle:** 1.3.0 → 1.4.0

**Authorization:** After reviewing the supplied Opus feedback and the proposed disposition, the owner requested that the corrections be applied to docs.

**Product baseline:** D-01 through D-09 remain in force. The [personal-tool amendment](2026-10-10-personal-tool-revision.md) and [directory-result amendment](2026-10-10-directory-results-revision.md) continue to govern normal JavaScript, multiple HTML pages, remembered use and unrestricted artifact intake/export.

**Implementation status:** Specification changes only. All 10 Epics and 61 Tasks remain planned. G-STACK, G-AUTH, G-VIEW, G-AI, G-VALUE and G-CONTRACT remain not_run; no provider, Wails, Safari, service or deployment was executed by this revision.

## Inputs and interpretation

Historical input set at the time of 1.4.0: `../../reviews/2026-10-10-opus/00-summary.md`, `../../reviews/2026-10-10-opus/01-usefulness.md`, `../../reviews/2026-10-10-opus/02-feasibility.md`, `../../reviews/2026-10-10-opus/03-consistency.md`, `../../reviews/2026-10-10-opus/04-contracts.md`, `../../reviews/2026-10-10-opus/05-security-ops.md`, `../../reviews/2026-10-10-opus/06-delivery-testing.md`, `../../reviews/2026-10-10-opus/06_crosscheck.py` and `../../reviews/2026-10-10-opus/06_crosscheck_output.txt`.

**Availability correction in 1.6.0:** Those nine paths are absent from the current checkout. They are retained here as provenance, not working links or a claim that their contents were re-inspected. No replacements were fabricated or silently substituted. The earlier validation PASS applies to its recorded historical snapshot, not to current availability. See [Grok reconciliation](2026-10-11-grok-reconciliation.md) and the current validation report for this update.

The review identifies real missing contracts and delivery ambiguities. It also treats some deliberate scoped contributions as missing coverage and sometimes recommends broader restrictions than this personal tool needs. Findings are evaluated against the actual current contract and D-08/D-09, rather than accepted solely because multiple reports repeat them.

The immediate user workflow is to import and read useful AI output. Source-aware Q&A, evidence and maintenance enrich that workflow when the owner wants them; lack of source metadata does not make ordinary HTML unreadable or unacceptable.

## Accepted corrections

| Area | Revision and resulting behavior |
| --- | --- |
| Provider source evidence | Verified citations distinguish observed_input with a real attempt input observation from posthoc_verified exact-target verification after generation. Native tool evidence must actually identify consumed bytes. A source view, current checkout HEAD or model-authored identifier cannot manufacture proof of original input. Semantic claim support and current availability remain separate. |
| Freshness without regions | Assess explicit version, page or optional-region scope with eligible/assessed units. Region-free HTML is a normal page; a no-HTML result can use version scope. Missing targets, unknown coverage and zero eligible units cannot be aligned. Default display uses the project's remembered source mode and an applicable already-captured target set, without launching source reads or AI from a list. |
| Repeat imports | Explicit project/document/new-document intent overrides a remembered client/workflow association. Otherwise import creates a new document, using a stable Unfiled project if none was selected. Wrong or stale explicit targets fail instead of quietly making a new document. Same-operation receipt recovery retains its original normalization context and is distinct from a new import operation. |
| Upload and large context | Define upload modes, exact byte chunks/checkpoints, open/sealing/sealed/abandoned states, completion and abandon. Define sender-bound context.transfer messages and direct versus sealed-upload POST contexts. A ready handle requires sealed bytes; Save/Ask adoption assigns record lifetime, and cleanup cannot erase saved, active or unknown work. No artifact/state quota or progressing-transfer deadline is introduced. |
| Reader state | Separate the version's last-opened pointer, each page's reading position and a personal preferred default, each with its own validator. Explicit page links, Continue and open-without-page have distinct selection behavior. Personal choices do not republish content or change the immutable default. |
| UI mutations and configuration | Define draft reject/replace, finding-to-generation/scan actions, pairing rejection and remembered project source/provider/output settings. Map UI actions to existing or explicit contract operations. Version comparison composes file inventories; portable personal-record export remains the documented local CLI action. |
| Retries, jobs and plans | Align Idempotency-Key, operation_id and CLI identity; give natural plan commits a result lookup and safe exact replay. Recheck current authority before receipt replay and original preconditions only for new work. Cancel intent remains monotonic, including resume and unknown provider outcomes. |
| Shared state and lifecycle | Clarify principal versus session identity, permission versus config revisions, minimum shared enums, immutable title/provenance snapshots, unresolved in-version links and migration unavailability. Specialized contracts and the registry use the same meanings. |
| Renderer and file delivery | Distinguish one listener from browser origins and version content roots. Require concurrent versions with identical root-relative paths but distinct bytes, normal modules/workers/history, actual private Safari and Wails evidence, and executable preview/native-boundary checks. The selected construction remains unproven until G-VIEW. |
| Credentials and persistence | Any optional content-delivery credential is honestly a narrow read credential, distinct from owner/agent bearers and nonauthorizing context handles. Define multi-request Range/retry/resume lifetime and revocation. Ordinary remembered restart and actual browser storage loss have different recovery behavior. |
| Operations | Preserve active/adopted object pins during staging cleanup and real disk backpressure. Distinguish no backup yet, an older complete verified set, the latest failed attempt and actual trial restore. Keep manual backup as the normal default and report actual recovery evidence. |

Detailed authorities are the [schema registry](../contracts/schema-registry.md), [HTTP API](../contracts/http-api.md), [source access](../contracts/source-access.md), [AI adapter](../contracts/ai-adapter.md), [package](../contracts/package.md), [jobs/events](../contracts/jobs-events.md), [configuration](../contracts/configuration.md), [authentication](../contracts/authentication.md) and [viewer protocol](../contracts/viewer-protocol.md).

## Delivery corrections and limits on the review's counts

The ten-Epic, 61-Task sequence remains. A cheap reader-task worksheet begins in EPIC-001, and real-use checkpoints continue as the reader, AI and remote paths arrive. G-VALUE retains its full usefulness and semantic-support conditions; withdrawing bad claims does not itself pass the gate, and a fixed withdrawal percentage or compulsory two-week experiment is not added. Expected reader facts and useful interaction are made explicit.

Blocked unrelated evidence permits bounded work inside the current Epic. All required EPIC-001 gates still need passing evidence before EPIC-002 production work starts. This permits productive repair without relabelling blocked proofs as completed or quietly replacing Stage A with a CLI-only product.

| Finding | Disposition |
| --- | --- |
| M01/M05/M06 metadata omissions | Corrected the five actual missing connections: M01 to TASK-004-01; M05 to TASK-002-02; M06 to TASK-002-02, TASK-002-06 and TASK-005-05. |
| Stage A depends on EPIC-010 | Clarified first integrated ownership by EPIC-007 for M08/M14/M20 and a separate EPIC-010 release-regression column. Stage A does not wait for a future release task merely because both appeared in one old column. |
| X18/X19 and X15 early evidence | Added explicit early bounded evidence ownership, including TASK-001-04 for X18/X19 and TASK-001-07 for X15, alongside production and regression owners. |
| “31 of 41 X cases are inconsistent” | Not accepted as 31 missing features. There are 20 X cases; 41 counts initial-owner relationships in that review. Its heuristic required every owner's Task metadata to repeat every requirement for the entire case, despite scoped contributions. Validate named ownership in both directions and the case's collective requirement coverage instead. |
| “X15 has no early owner” | The existing procedure already named TASK-001-07. Making the role an explicit column repairs discoverability without claiming it was absent from the specification. |
| UI and planning gaps | Connected import/download/native chooser controls to Tasks, source-proof wording to actual observation levels, UI mutations to routes and reader actions to independent resources. Added two focused Task assertions, bringing the inventory to 200. |
| Inconsistent evidence vocabulary and load target | Standardized gate outcomes as not_run/pass/fail/blocked and distinguished author/executor/reviewer roles without requiring multiple people. Calibrate three owner-client sessions plus one configured background worker at TASK-001-06; label simulations and retain the real private second-device requirement. |
| Broken historical links | The five original review paths under reviews/2026-10-10 are absent from this checkout. The old revision record now preserves literal historical filenames and states the absence. These Opus reports are separate inputs, not silently substituted sources. |

See [execution protocol](../execution-protocol.md), [traceability](../testing/traceability.md), [contract cases](../testing/contract-cases.md), [knowledge quality](../testing/knowledge-quality.md) and the [UI action mapping](../ui/screens.md).

## Proposals not adopted

- No JavaScript allowlist, declarative replacement runtime, mandatory OS worker jail, per-read grant, repeated administrative elevation or artificial HTML/directory/file-count/depth/context limit.
- No mandatory author manifest, hand-calculated IDs/digests, required index page or implicit omission of selected files. Source files already selected as payload retain their ordinary payload identity; separately fetched sources still follow remembered source/disclosure policy.
- No mandatory second API service, wildcard DNS/certificate setup or universal renderer design selected from prose alone. A candidate must demonstrate the required paths and targets.
- No CLI-only Stage A, removal of Wails or private remote use, broad provider expansion, or silent API billing fallback.
- No full-backup-before-every-purge rule, compulsory purge delay, fixed seven-day staging deletion, new backup scheduler or monitoring platform.
- No “every already committed plan must fail” retry rule, “reassessment flag iff one display status” rule, or blanket rejection of indefinite retention with automatic operation. Exact committed replay, independent assessment facts and explicitly authorized retention semantics remain.
- No separate endpoint for every UI control. Local-only operations, composed reads and existing configuration/generation/scan contracts are identified honestly.

These limits preserve the owner's speed and chosen product scope while making correctness and actual failure recovery implementable.

## Remaining implementation evidence

G-VIEW must select and prove the actual path/origin/delivery construction on the supported private browser and Wails contexts. G-AI must verify a supported subscription integration and record the evidence it actually observes. G-CONTRACT must turn the revised shapes into executable fixtures before freeze. Early usefulness, source quality, real-device behavior, migration/restore and resource failure remain tasks to execute.

A failed necessary capability may create a concrete later owner choice. No new product decision is required to complete this authorized documentation revision, and unrun evidence is not an implied request for new account, source, network, signing or deployment authority.

The original [Revision 4 source](../sources/haeryong-concept-proposal-v4.md) and all supplied Opus review files remain unchanged. Runtime schema 1.0 and /api/v1 remain proposed, independent of documentation 1.4.0. The [integrity report](../testing/bundle-validation.md) records the actual documentation checks; [MANIFEST.md](../MANIFEST.md) inventories the final bytes. Git staging, commit and push are outside this revision.
